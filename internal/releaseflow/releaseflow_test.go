package releaseflow

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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
	t           *testing.T
	bare        string
	tags        int
	releases    []string
	unprotected bool // no tag rule (fix 2, P1-6a)
	drafts      map[int64]Draft
	deleted     int
	failDelete  bool
	onDraft     func()       // runs after a draft is created
	onRead      func(n int)  // runs before the n-th ReadDraft (1-based)
	failRead    map[int]bool // the n-th ReadDraft errors
	reads       int
	nextID      int64
	bodies      map[int64]string
	loseCreate  int // the n-th CreateDraft (1-based) creates the draft and loses its response
	losePublish int // the n-th PublishDraft publishes and loses its response
	creates     int
	publishes   int
}

func (e *repoExec) Tag(_ context.Context, repository, tag, commit string) error {
	e.tags++
	run(e.t, e.bare, "update-ref", "refs/tags/"+tag, commit)
	return nil
}

func (e *repoExec) TagProtected(context.Context, string, string) (bool, string, error) {
	if e.unprotected {
		return false, "no tag rule", nil
	}
	return true, "", nil
}

func (e *repoExec) CreateDraft(_ context.Context, repository, tag, commit, notes string) (Draft, error) {
	if exec.Command("git", "-C", e.bare, "rev-parse", "--verify", "refs/tags/"+tag).Run() != nil {
		return Draft{}, errors.New("release before its tag")
	}
	if e.drafts == nil {
		e.drafts = map[int64]Draft{}
	}
	e.nextID++
	d := Draft{ID: e.nextID, Tag: tag, TargetCommitish: commit, Draft: true}
	e.drafts[d.ID] = d
	if e.bodies == nil {
		e.bodies = map[int64]string{}
	}
	e.bodies[d.ID] = notes
	if e.onDraft != nil {
		e.onDraft()
	}
	if e.creates++; e.creates == e.loseCreate {
		return Draft{}, errors.New("connection reset by peer")
	}
	return d, nil
}

func (e *repoExec) FindDrafts(_ context.Context, _ string, nonce string) ([]Draft, error) {
	var out []Draft
	for id, d := range e.drafts {
		if strings.Contains(e.bodies[id], NonceMarker(nonce)) {
			out = append(out, d)
		}
	}
	slices.SortFunc(out, func(a, b Draft) int { return int(a.ID - b.ID) })
	return out, nil
}

func (e *repoExec) ReadDraft(_ context.Context, _ string, id int64) (Draft, bool, error) {
	e.reads++
	if e.onRead != nil {
		e.onRead(e.reads)
	}
	if e.failRead[e.reads] {
		return Draft{}, false, errors.New("HTTP 502")
	}
	d, ok := e.drafts[id]
	return d, ok, nil
}

// PublishDraft publishes as GitHub does: the release stays at its id, now
// with draft false (5 re-pass, P1-1: the fake used to remove it).
func (e *repoExec) PublishDraft(_ context.Context, repository string, id int64) (string, error) {
	d := e.drafts[id]
	d.Draft, d.URL = false, "https://example.test/"+repository+"/releases/tag/"+d.Tag
	e.drafts[id] = d
	e.releases = append(e.releases, repository+"@"+d.Tag)
	if e.publishes++; e.publishes == e.losePublish {
		return "", errors.New("connection reset by peer")
	}
	return d.URL, nil
}

// published counts the releases the fake holds as published.
func (e *repoExec) published() int {
	n := 0
	for _, d := range e.drafts {
		if !d.Draft {
			n++
		}
	}
	return n
}

func (e *repoExec) DeleteRelease(_ context.Context, _ string, id int64) error {
	if e.failDelete {
		return errors.New("HTTP 500")
	}
	delete(e.drafts, id)
	e.deleted++
	return nil
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

// fix 2, P1-6a: without a tag rule restricting update and deletion of the
// tag, the release flow runs nothing (protection_unmet).
func TestReleaseRequiresATagRule(t *testing.T) {
	f := newFixture(t)
	snap, err := f.flow.Prepare(f.spec)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range snap.Active.Children {
		f.approved[c.QuestionMessageID] = true
	}
	f.exec.unprotected = true
	if _, err := f.flow.Publish(context.Background(), f.root); !errors.Is(err, ErrProtectionUnmet) || f.exec.tags != 0 || len(f.exec.releases) != 0 {
		t.Fatalf("an unprotected repository published: %v", err)
	}
}

// fix 2, P1-6b: the release is a draft first; a tag moved between the check
// and the draft is caught on the read-back, the draft is deleted, nothing is
// published and the owner alert is raised.
func TestDraftReadBackCatchesAMovedTag(t *testing.T) {
	f := newFixture(t)
	snap, err := f.flow.Prepare(f.spec)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range snap.Active.Children {
		f.approved[c.QuestionMessageID] = true
	}
	other := run(t, f.exec.bare, "commit-tree", "-m", "other", run(t, f.exec.bare, "rev-parse", "main^{tree}"))
	f.exec.onDraft = func() { run(t, f.exec.bare, "update-ref", "refs/tags/v1.2.0", other) }
	_, err = f.flow.Publish(context.Background(), f.root)
	if !errors.Is(err, ErrTagMoved) || len(f.exec.releases) != 0 || f.exec.deleted != 1 || len(f.exec.drafts) != 0 {
		t.Fatalf("moved tag after the draft: %v releases %v deleted %d", err, f.exec.releases, f.exec.deleted)
	}
	if alerts := mergetrain.ReadAlerts(f.root); len(alerts) != 1 || alerts[0].Kind != "tag_moved" {
		t.Fatalf("owner alert %+v", alerts)
	}
}

// fix 4, item 7: only exact, ~ALL or single-segment prefix include
// patterns are accepted, and no exclusion list; anything else names the
// pattern.
func TestTagRuleCoversPatterns(t *testing.T) {
	for _, tc := range []struct {
		in, ex  []string
		covered bool
		refused bool
	}{
		{[]string{"~ALL"}, nil, true, false},
		{[]string{"refs/tags/v1.2.0"}, nil, true, false},
		{[]string{"refs/tags/v*"}, nil, true, false},
		{[]string{"refs/tags/release-*"}, nil, false, false},
		{[]string{"refs/tags/**"}, nil, false, true},
		{[]string{"refs/tags/v*/rc"}, nil, false, true},
		{[]string{"refs/tags/v?.*"}, nil, false, true},
		{[]string{"refs/heads/main"}, nil, false, true},
		{[]string{"~ALL"}, []string{"refs/tags/v1.**"}, false, true},
		{[]string{"refs/tags/v*"}, []string{"refs/tags/v0*"}, false, true},
	} {
		covered, why := TagRuleCovers(tc.in, tc.ex, "v1.2.0")
		if covered != tc.covered || (why != "") != tc.refused {
			t.Errorf("%v %v: covered %v why %q", tc.in, tc.ex, covered, why)
		}
	}
}

// fix 4, item 6: a covering tag ruleset with bypass actors is unmet, the
// same rule as the branch check.
func TestTagRulesetWithBypassActorsIsUnmet(t *testing.T) {
	var rs tagRuleset
	if err := json.Unmarshal([]byte(`{"id":3,"bypass_actors":[{"actor_type":"RepositoryRole","actor_id":5}],"conditions":{"ref_name":{"include":["~ALL"],"exclude":[]}},"rules":[{"type":"update"},{"type":"deletion"}]}`), &rs); err != nil {
		t.Fatal(err)
	}
	if ok, why := rs.protects("v1.2.0"); ok || !strings.Contains(why, "bypass actors") {
		t.Fatalf("a bypassable tag rule protected the tag: %v %q", ok, why)
	}
	rs.BypassActors = nil
	if ok, _ := rs.protects("v1.2.0"); !ok {
		t.Fatal("a rule without bypass actors did not protect")
	}
}

// fix 4, items 8 and 9: a draft whose tag is edited to another existing tag,
// after creation or right before the publish, is deleted (audited with its
// id, outcome and deletion result), draft_edited is raised and nothing is
// published; a failed deletion is a draft_orphan alert naming the draft.
func TestEditedDraftIsDeletedAuditedAndAlerted(t *testing.T) {
	for _, at := range []int{1, 2} { // the read-back after creation, the re-read before publish
		f := newFixture(t)
		snap, err := f.flow.Prepare(f.spec)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range snap.Active.Children {
			f.approved[c.QuestionMessageID] = true
		}
		run(t, f.exec.bare, "update-ref", "refs/tags/v0.9.0", f.commit) // another existing tag
		f.exec.onRead = func(n int) {
			if n == at {
				for id, d := range f.exec.drafts {
					d.Tag = "v0.9.0"
					f.exec.drafts[id] = d
				}
			}
		}
		if _, err := f.flow.Publish(context.Background(), f.root); !errors.Is(err, ErrDraftEdited) || len(f.exec.releases) != 0 || f.exec.deleted != 1 {
			t.Fatalf("read %d: an edited draft was published: %v %v", at, err, f.exec.releases)
		}
		if alerts := mergetrain.ReadAlerts(f.root); len(alerts) != 1 || alerts[0].Kind != "draft_edited" {
			t.Fatalf("read %d: alerts %+v", at, alerts)
		}
		raw, _ := os.ReadFile(filepath.Join(f.root, "extensions", "amq-squad", "releases", snap.Prepared.ReleaseID, "audit.jsonl"))
		if !strings.Contains(string(raw), `"kind":"draft_edited"`) || !strings.Contains(string(raw), "draft 1 ") || !strings.Contains(string(raw), "deleted") {
			t.Fatalf("read %d: audit %s", at, raw)
		}
	}
	g := newFixture(t)
	snap, err := g.flow.Prepare(g.spec)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range snap.Active.Children {
		g.approved[c.QuestionMessageID] = true
	}
	other := run(t, g.exec.bare, "commit-tree", "-m", "other", run(t, g.exec.bare, "rev-parse", "main^{tree}"))
	g.exec.onDraft = func() { run(t, g.exec.bare, "update-ref", "refs/tags/v1.2.0", other) }
	g.exec.failDelete = true
	if _, err := g.flow.Publish(context.Background(), g.root); !errors.Is(err, ErrTagMoved) {
		t.Fatalf("moved tag: %v", err)
	}
	alerts := mergetrain.ReadAlerts(g.root)
	if !slices.ContainsFunc(alerts, func(a mergetrain.Alert) bool {
		return a.Kind == "draft_orphan" && strings.Contains(a.Detail, "draft release 1 ")
	}) {
		t.Fatalf("a failed deletion raised no draft_orphan alert: %+v", alerts)
	}
}

// fix 4, item 10: a read-back error after creation leaves the draft id in
// the publication lock record; the next run recovers and completes that same
// draft, creating no second one.
func TestOrphanDraftIsRecoveredOnTheNextRun(t *testing.T) {
	f := newFixture(t)
	snap, err := f.flow.Prepare(f.spec)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range snap.Active.Children {
		f.approved[c.QuestionMessageID] = true
	}
	f.exec.failRead = map[int]bool{1: true}
	if _, err := f.flow.Publish(context.Background(), f.root); err == nil || len(f.exec.releases) != 0 {
		t.Fatalf("the read-back error published: %v", err)
	}
	record := filepath.Join(f.root, "extensions", "amq-squad", "releases", snap.Prepared.ReleaseID, "draft.json")
	if _, err := os.Stat(record); err != nil {
		t.Fatalf("no publication lock record for the draft: %v", err)
	}
	if _, err := f.flow.Publish(context.Background(), f.root); err != nil || len(f.exec.releases) != 1 || f.exec.nextID != 1 {
		t.Fatalf("recovery: %v releases %v drafts created %d", err, f.exec.releases, f.exec.nextID)
	}
	if _, err := os.Stat(record); !os.IsNotExist(err) {
		t.Fatalf("the lock record stayed after publication: %v", err)
	}
}

// 5 re-pass, P1-1: the publish request succeeds and its response is lost.
// The retry finds the release published at its id with the approved tag and
// commit: that is completion, never a deletion.
func TestRepassLostPublishResponseIsCompletionNotDeletion(t *testing.T) {
	f := newFixture(t)
	snap, err := f.flow.Prepare(f.spec)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range snap.Active.Children {
		f.approved[c.QuestionMessageID] = true
	}
	f.exec.losePublish = 1
	if _, err := f.flow.Publish(context.Background(), f.root); err == nil {
		t.Fatal("a lost publish response was reported as success")
	}
	steps, err := f.flow.Publish(context.Background(), f.root)
	if err != nil || len(steps) != 2 || steps[1].Status != "ran" || !strings.Contains(steps[1].Result, "/releases/tag/v1.2.0") {
		t.Fatalf("retry: %v %+v", err, steps)
	}
	if f.exec.deleted != 0 || f.exec.published() != 1 || f.exec.nextID != 1 || len(f.exec.releases) != 1 {
		t.Fatalf("the published release was deleted or published again: deleted %d published %d drafts %d", f.exec.deleted, f.exec.published(), f.exec.nextID)
	}
	raw, _ := os.ReadFile(filepath.Join(f.root, "extensions", "amq-squad", "releases", snap.Prepared.ReleaseID, "audit.jsonl"))
	if !strings.Contains(string(raw), "recovered: the release was already published") {
		t.Fatalf("audit %s", raw)
	}
}

// 5 re-pass, P2-4: the draft POST succeeds and its response is lost (no
// record written). The creation intent was written first and the draft
// carries its nonce, so the retry adopts that draft and creates no second.
func TestRepassLostDraftCreationIsAdoptedByItsNonce(t *testing.T) {
	f := newFixture(t)
	snap, err := f.flow.Prepare(f.spec)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range snap.Active.Children {
		f.approved[c.QuestionMessageID] = true
	}
	f.exec.loseCreate = 1
	if _, err := f.flow.Publish(context.Background(), f.root); err == nil {
		t.Fatal("a lost creation response was reported as success")
	}
	if _, err := f.flow.Publish(context.Background(), f.root); err != nil || f.exec.nextID != 1 || f.exec.published() != 1 || len(f.exec.drafts) != 1 {
		t.Fatalf("retry: %v drafts created %d published %d held %d", err, f.exec.nextID, f.exec.published(), len(f.exec.drafts))
	}
	dir := filepath.Join(f.root, "extensions", "amq-squad", "releases", snap.Prepared.ReleaseID)
	if _, err := os.Stat(filepath.Join(dir, "draft-intent.json")); !os.IsNotExist(err) {
		t.Fatalf("the intent stayed after publication: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "draft.json")); !os.IsNotExist(err) {
		t.Fatalf("the lock record stayed after publication: %v", err)
	}
}

// Narrow pass, coverage, and 3-2 P1-1's alert write: the recorded draft id
// is found published (Draft false) with another tag or another commit. It
// is never deleted: the mismatch is audited and an owner alert raised, and
// an alert that cannot be written is itself an error.
func TestRepassPublishedMismatchIsNeverDeleted(t *testing.T) {
	for _, tc := range []struct{ name, tag, commit string }{{"wrong tag", "v0.9.0", ""}, {"wrong commit", "v1.2.0", strings.Repeat("e", 40)}} {
		t.Run(tc.name, func(t *testing.T) {
			for _, alertFails := range []bool{false, true} {
				f := newFixture(t)
				snap, err := f.flow.Prepare(f.spec)
				if err != nil {
					t.Fatal(err)
				}
				f.approved[snap.Active.Children[0].QuestionMessageID] = true
				if _, err := f.flow.Publish(context.Background(), f.root); !errors.Is(err, ErrNotApproved) {
					t.Fatalf("tag step: %v", err)
				}
				dir := filepath.Join(f.root, "extensions", "amq-squad", "releases", snap.Prepared.ReleaseID)
				rec, _ := json.Marshal(Draft{ID: 1, Tag: "v1.2.0", TargetCommitish: f.commit, Draft: true})
				if err := os.WriteFile(filepath.Join(dir, "draft.json"), rec, 0o600); err != nil {
					t.Fatal(err)
				}
				commit := tc.commit
				if commit == "" {
					commit = f.commit
				}
				f.exec.drafts = map[int64]Draft{1: {ID: 1, Tag: tc.tag, TargetCommitish: commit, Draft: false}}
				if alertFails {
					alerts := filepath.Join(f.root, "extensions", "amq-squad", "alerts")
					if err := os.WriteFile(alerts, []byte("not a directory"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				f.approved[snap.Active.Children[1].QuestionMessageID] = true
				_, err = f.flow.Publish(context.Background(), f.root)
				if !errors.Is(err, ErrDraftEdited) || f.exec.deleted != 0 || len(f.exec.drafts) != 1 {
					t.Fatalf("alert fails %v: %v deleted %d", alertFails, err, f.exec.deleted)
				}
				if alertFails {
					if !errors.Is(err, ErrAlertUnwritten) {
						t.Fatalf("an unwritten owner alert passed silently: %v", err)
					}
					continue
				}
				if alerts := mergetrain.ReadAlerts(f.root); len(alerts) != 1 || alerts[0].Kind != "draft_edited" || !strings.Contains(alerts[0].Detail, "not deleted") {
					t.Fatalf("owner alert %+v", alerts)
				}
				raw, _ := os.ReadFile(filepath.Join(dir, "audit.jsonl"))
				if !strings.Contains(string(raw), `"kind":"release_mismatch"`) {
					t.Fatalf("audit %s", raw)
				}
			}
		})
	}
}
