package releaseflow

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/compoundrelease"
	"github.com/omriariav/amq-squad/v3/internal/gatestore"
	"github.com/omriariav/amq-squad/v3/internal/mergetrain"
	squadnamespace "github.com/omriariav/amq-squad/v3/internal/namespace"
	"github.com/omriariav/amq-squad/v3/internal/operatorauth"
)

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=owner", "GIT_AUTHOR_EMAIL=o@example.com", "GIT_COMMITTER_NAME=owner",
		"GIT_COMMITTER_EMAIL=o@example.com", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// repoExec is the fixture repository's forge: a tag is a ref in the bare
// repository, a release a record that needs the tag to exist.
type repoExec struct {
	t        *testing.T
	bare     string
	tags     int
	releases []string
}

func (e *repoExec) Tag(_ context.Context, repository, tag, commit string) error {
	e.tags++
	run(e.t, e.bare, "update-ref", "refs/tags/"+tag, commit)
	return nil
}

func (e *repoExec) Release(_ context.Context, repository, tag, notes string) (string, error) {
	cmd := exec.Command("git", "-C", e.bare, "rev-parse", "--verify", "refs/tags/"+tag)
	if cmd.Run() != nil {
		return "", errors.New("release before its tag")
	}
	e.releases = append(e.releases, repository+"@"+tag)
	return "https://example.test/" + repository + "/releases/tag/" + tag, nil
}

func (e *repoExec) TagCommit(_ context.Context, repository, tag string) (string, error) {
	out, err := exec.Command("git", "-C", e.bare, "rev-parse", "--verify", "-q", "refs/tags/"+tag+"^{commit}").Output()
	if err != nil {
		return "", nil
	}
	return strings.TrimSpace(string(out)), nil
}

type fixture struct {
	flow     *Flow
	exec     *repoExec
	root     string
	spec     operatorauth.ReleaseSpec
	approved map[string]bool
	commit   string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	bare := filepath.Join(dir, "repo.git")
	work := filepath.Join(dir, "work")
	run(t, dir, "init", "-q", "--bare", "-b", "main", bare)
	run(t, dir, "clone", "-q", bare, work)
	if err := os.WriteFile(filepath.Join(work, "README"), []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, work, "checkout", "-q", "-b", "main")
	run(t, work, "add", "-A")
	run(t, work, "commit", "-q", "-m", "v1")
	run(t, work, "push", "-q", "origin", "main")
	commit := run(t, bare, "rev-parse", "refs/heads/main")
	project, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	scope := compoundrelease.Scope{ProjectDir: project, Profile: squadnamespace.DefaultProfile, Session: "rel", NamespaceGeneration: "none", ParentGate: "gate/release-v1.2.0"}
	store, err := compoundrelease.Open(scope, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	root := filepath.Join(dir, "root")
	gates, err := gatestore.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	id := gatestore.Identity{OwnerPubKey: strings.Repeat("a", 64), IssuerKeyID: "ed25519-sha256:" + strings.Repeat("b", 64)}
	f := &fixture{exec: &repoExec{t: t, bare: bare}, root: root, approved: map[string]bool{}, commit: commit}
	f.flow = &Flow{Release: store, Gates: gates, Exec: f.exec, Lead: "lead",
		Raise: func(s gatestore.Spec) (gatestore.Record, error) {
			rec, err := gatestore.Render(s, time.Now(), id)
			if err != nil {
				return rec, err
			}
			return rec, gates.Create(rec)
		},
		Accepted: func(rec gatestore.Record) error {
			if !f.approved[rec.GateID] {
				return errors.New("gate " + rec.GateID + " has no accepted decision")
			}
			return nil
		}}
	f.spec, err = Spec(scope, "lead", "owner", "owner/fixture", "v1.2.0", commit, "first release")
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// Acceptance 6: on a fixture repository a tag without a verified answer is
// refused and nothing runs; with the tag's answer the tag is created and the
// release still waits; with both, the writer's active release runs the
// release, once.
func TestAcceptance6ReleaseFlow(t *testing.T) {
	f := newFixture(t)
	snap, err := f.flow.Prepare(f.spec)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Active == nil || len(snap.Active.Children) != 2 {
		t.Fatalf("not active: %+v", snap.Pointer)
	}
	tagGate, relGate := snap.Active.Children[0].QuestionMessageID, snap.Active.Children[1].QuestionMessageID
	for i, id := range []string{tagGate, relGate} {
		rec, err := f.flow.Gates.Load(id)
		if err != nil || rec.TypedClass != "release" || rec.Target != snap.Prepared.Children[i].Target {
			t.Fatalf("gate %s: %v %+v", id, err, rec)
		}
	}
	if !strings.HasPrefix(snap.Prepared.Children[0].Target, "git tag v1.2.0 "+f.commit+" on owner/fixture") {
		t.Fatalf("tag target %q", snap.Prepared.Children[0].Target)
	}

	steps, err := f.flow.Publish(context.Background(), f.root)
	if !errors.Is(err, ErrNotApproved) || len(steps) != 1 || steps[0].Status != "refused" || f.exec.tags != 0 || len(f.exec.releases) != 0 {
		t.Fatalf("tag without a verified answer: %v %+v tags %d", err, steps, f.exec.tags)
	}

	f.approved[tagGate] = true
	steps, err = f.flow.Publish(context.Background(), f.root)
	if !errors.Is(err, ErrNotApproved) || len(steps) != 2 || steps[0].Status != "ran" || steps[1].Status != "refused" || f.exec.tags != 1 || len(f.exec.releases) != 0 {
		t.Fatalf("tag approved, release not: %v %+v", err, steps)
	}

	f.approved[relGate] = true
	steps, err = f.flow.Publish(context.Background(), f.root)
	if err != nil || steps[0].Status != "done" || steps[1].Status != "ran" || len(f.exec.releases) != 1 || f.exec.tags != 1 {
		t.Fatalf("both approved: %v %+v", err, steps)
	}
	if steps, err = f.flow.Publish(context.Background(), f.root); err != nil || steps[1].Status != "done" || len(f.exec.releases) != 1 {
		t.Fatalf("a retry ran a child twice: %v %+v", err, steps)
	}
}

// The same spec prepares the same release and the same two gates; a gate id
// taken by another target is a conflict, not a second question.
func TestPrepareIsIdempotentAndRefusesAForeignGate(t *testing.T) {
	f := newFixture(t)
	a, err := f.flow.Prepare(f.spec)
	if err != nil {
		t.Fatal(err)
	}
	b, err := f.flow.Prepare(f.spec)
	if err != nil || b.Active.ActiveManifestID != a.Active.ActiveManifestID {
		t.Fatalf("second prepare: %v", err)
	}
	ids, _ := f.flow.Gates.IDs()
	if len(ids) != 2 {
		t.Fatalf("gates %v", ids)
	}

	g := newFixture(t)
	prepared, err := operatorauth.DerivePreparedRelease(g.spec, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.flow.Raise(gatestore.Spec{GateID: GateID(prepared.Children[0]), Action: "tag", Target: "git tag v9 " + g.commit + " on owner/other", RaisedBy: "seat"}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.flow.Prepare(g.spec); err == nil {
		t.Fatal("a foreign gate under the child's id was adopted")
	}
	snap, err := g.flow.Release.ReadCurrent()
	if err != nil || snap.Pointer.State != operatorauth.ReleaseStateConflict {
		t.Fatalf("release state %v %+v", err, snap.Pointer)
	}
}

// P1-6: the remote tag is resolved right before the release; a tag moved
// after the approved tag step is tag_moved, publishes nothing and raises the
// owner alert.
func TestReleaseRefusesAMovedTag(t *testing.T) {
	f := newFixture(t)
	snap, err := f.flow.Prepare(f.spec)
	if err != nil {
		t.Fatal(err)
	}
	f.approved[snap.Active.Children[0].QuestionMessageID] = true
	if _, err := f.flow.Publish(context.Background(), f.root); !errors.Is(err, ErrNotApproved) || f.exec.tags != 1 {
		t.Fatalf("tag step: %v", err)
	}
	other := run(t, f.exec.bare, "commit-tree", "-m", "other", run(t, f.exec.bare, "rev-parse", "main^{tree}"))
	run(t, f.exec.bare, "update-ref", "refs/tags/v1.2.0", other) // the seat moves the tag
	f.approved[snap.Active.Children[1].QuestionMessageID] = true
	steps, err := f.flow.Publish(context.Background(), f.root)
	if !errors.Is(err, ErrTagMoved) || len(f.exec.releases) != 0 || steps[len(steps)-1].Status != "refused" {
		t.Fatalf("moved tag published: %v %+v releases %v", err, steps, f.exec.releases)
	}
	if alerts := mergetrain.ReadAlerts(f.root); len(alerts) != 1 || alerts[0].Kind != "tag_moved" || alerts[0].SHA != other {
		t.Fatalf("owner alert %+v", alerts)
	}
}

// P2-10: a crash after the tag and before its marker: the retry finds the
// tag at the approved commit, reconciles it without tagging again, and
// continues to the release.
func TestRetryAfterACrashReconcilesTheTag(t *testing.T) {
	f := newFixture(t)
	snap, err := f.flow.Prepare(f.spec)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range snap.Active.Children {
		f.approved[c.QuestionMessageID] = true
	}
	run(t, f.exec.bare, "update-ref", "refs/tags/v1.2.0", f.commit) // the crashed run's tag, no marker
	steps, err := f.flow.Publish(context.Background(), f.root)
	if err != nil || f.exec.tags != 0 || len(f.exec.releases) != 1 || steps[0].Status != "ran" || !strings.Contains(steps[0].Detail, "reconciled") {
		t.Fatalf("retry: %v %+v tags %d", err, steps, f.exec.tags)
	}
}

// The release flow sets its squad identity on both child gates, and a gate
// under a child's id raised by another squad is a conflict, not adopted.
func TestReleaseGatesCarryTheSquadIdentity(t *testing.T) {
	f := newFixture(t)
	f.flow.Squad = mergetrain.SquadOf(f.root)
	snap, err := f.flow.Prepare(f.spec)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range snap.Active.Children {
		if rec, err := f.flow.Gates.Load(c.QuestionMessageID); err != nil || rec.Squad != f.flow.Squad {
			t.Fatalf("gate %s squad %q: %v", c.QuestionMessageID, rec.Squad, err)
		}
	}
	g := newFixture(t)
	g.flow.Squad = mergetrain.SquadOf(g.root)
	prepared, err := operatorauth.DerivePreparedRelease(g.spec, 1)
	if err != nil {
		t.Fatal(err)
	}
	child := prepared.Children[0]
	if _, err := g.flow.Raise(gatestore.Spec{GateID: GateID(child), Action: child.Action, Target: child.Target, RaisedBy: "seat", Squad: mergetrain.SquadOf("/other")}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.flow.Prepare(g.spec); err == nil {
		t.Fatal("another squad's gate under the child's id was adopted")
	}
}
