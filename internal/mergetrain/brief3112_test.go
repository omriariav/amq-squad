package mergetrain

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/gatestore"
	"github.com/omriariav/amq-squad/v3/internal/gatetarget"
	"github.com/omriariav/amq-squad/v3/internal/gateverify"
)

// decisionAccepted stands in for the owner-signature check in the rows that
// exercise the drift check's binding rules: a gate counts when its store's
// recovered decision is accepted. Production wires gateverify's full check
// (internal/cli releaseGateVerifier); the rows below that need it use it.
func decisionAccepted(_ string, s gatestore.Store, rec gatestore.Record) error {
	if s.Decision(rec.GateID) != gatestore.StateAccepted {
		return errors.New("not accepted")
	}
	return nil
}

type listed struct {
	tags     []Tag
	releases []Release
}

func (l *listed) Tags(context.Context, string) ([]Tag, error)         { return l.tags, nil }
func (l *listed) Releases(context.Context, string) ([]Release, error) { return l.releases, nil }

// gateIn writes an accepted (consumed) release gate into a session's store.
func gateIn(t *testing.T, sessionRoot, id, action, target string, accepted bool) {
	t.Helper()
	store, err := gatestore.Open(sessionRoot)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := gatestore.Render(gatestore.Spec{GateID: id, Action: action, Target: target, RaisedBy: "lead", Squad: gatestore.SquadID(sessionRoot)}, time.Now(), gateIdentity())
	if err != nil || store.Create(rec) != nil {
		t.Fatal(id, err)
	}
	if accepted {
		writeFile(t, filepath.Join(sessionRoot, "extensions", "amq-squad", "consumed", "gates", id), "approved test\n")
	}
}

// Brief 3.1.1-2 item 5: each release is cut in its own fresh session (v301,
// v303, v31rc1, v310), so the squad's session v31 holds none of their gates
// and the drift check raised "ungated tag" and "ungated release" for every
// release. It reads the gate store of every session under the AMQ root and
// clears an alert only on a verified gate whose target names that tag and
// that commit. Reverted (the squad's own store only), v3.1.0 alerted.
func TestDriftReadsTheGateStoresOfEverySession(t *testing.T) {
	const repo = "owner/app"
	base := t.TempDir()
	squad, release := filepath.Join(base, "v31"), filepath.Join(base, "v310")
	for _, d := range []string{squad, release, filepath.Join(base, "not-a-session")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	l := &listed{}
	now := time.Date(2026, 11, 1, 22, 0, 0, 0, time.UTC)
	if a, err := Drift(context.Background(), l, decisionAccepted, squad, repo, now); err != nil || len(a) != 0 {
		t.Fatalf("baseline: %v %+v", err, a)
	}
	sha, other := strings.Repeat("4", 40), strings.Repeat("5", 40)
	l.tags, l.releases = []Tag{{Name: "v3.1.0", SHA: sha}}, []Release{{ID: 31, Tag: "v3.1.0"}}
	kinds := func(a []Alert) []string {
		var out []string
		for _, x := range a {
			out = append(out, x.Kind)
		}
		slices.Sort(out)
		return out
	}
	// a gate whose target names another commit clears nothing; nor do
	// another session's gated commands (a tag's names no commit; a seat's
	// release command is its own session's)
	gateIn(t, release, "rel-tag-other", "tag", gatetarget.ReleaseTag(repo, "v3.1.0", other), true)
	gateIn(t, release, "rel-cmd", "tag", gatetarget.Command("git tag", "v3.1.0"), true)
	gateIn(t, release, "rel-gh-cmd", "github_release", gatetarget.Command("gh release create", "v3.1.0"), true)
	if a, err := Drift(context.Background(), l, decisionAccepted, squad, repo, now); err != nil || !slices.Equal(kinds(a), []string{"release", "tag"}) {
		t.Fatalf("a gate naming another commit: %v %+v", err, a)
	}
	// a record that is not accepted clears nothing
	gateIn(t, release, "rel-tag-open", "tag", gatetarget.ReleaseTag(repo, "v3.1.0", sha), false)
	if a, _ := Drift(context.Background(), l, decisionAccepted, squad, repo, now); !slices.Contains(kinds(a), "tag") {
		t.Fatalf("an unaccepted gate cleared the tag: %+v", a)
	}
	// the release session's accepted gates for this tag at this commit, and
	// for its release, clear both
	gateIn(t, release, "rel-tag", "tag", gatetarget.ReleaseTag(repo, "v3.1.0", sha), true)
	gateIn(t, release, "rel-gh", "github_release", gatetarget.ReleaseCreate(repo, "v3.1.0"), true)
	if a, err := Drift(context.Background(), l, decisionAccepted, squad, repo, now); err != nil || len(a) != 0 {
		t.Fatalf("a release gated in another session: %v %+v", err, a)
	}
	// with no verifier nothing clears
	if a, _ := Drift(context.Background(), l, nil, squad, repo, now); !slices.Equal(kinds(a), []string{"release", "tag"}) {
		t.Fatalf("no verifier: %+v", a)
	}
}

// Item 5, the unsigned row: the production check is gateverify's, never a
// file's presence. A record whose decision is accepted by its consumed mark
// alone, with no owner-signed approval, does not verify and clears nothing.
func TestDriftClearsOnlyOnTheOwnerSignedAcceptance(t *testing.T) {
	const repo = "owner/app"
	base := t.TempDir()
	squad, release := filepath.Join(base, "v31"), filepath.Join(base, "v310")
	_ = os.MkdirAll(squad, 0o700)
	_ = os.MkdirAll(release, 0o700) // so only the signature can refuse it
	l := &listed{}
	now := time.Date(2026, 11, 1, 22, 0, 0, 0, time.UTC)
	_, _ = Drift(context.Background(), l, nil, squad, repo, now)
	sha := strings.Repeat("4", 40)
	l.tags = []Tag{{Name: "v3.1.0", SHA: sha}}
	gateIn(t, release, "rel-tag", "tag", gatetarget.ReleaseTag(repo, "v3.1.0", sha), true)
	signed := func(session string, s gatestore.Store, rec gatestore.Record) error {
		return gateverify.Verifier{Namespace: gateverify.Namespace{Session: session}}.Accepted(s, rec)
	}
	if a, err := Drift(context.Background(), l, signed, squad, repo, now); err != nil || len(a) != 1 || a[0].Kind != "tag" {
		t.Fatalf("an unsigned record cleared the tag: %v %+v", err, a)
	}
}

// Item 5, the unreadable row: a session's gate store that cannot be read is
// an alert of its own, naming it, and clears nothing.
func TestDriftNamesAnUnreadableGateStore(t *testing.T) {
	const repo = "owner/app"
	base := t.TempDir()
	squad := filepath.Join(base, "v31")
	_ = os.MkdirAll(squad, 0o700)
	broken := filepath.Join(base, "v303", "extensions", "amq-squad", "gates")
	writeFile(t, broken, "not a directory\n")
	l := &listed{}
	now := time.Date(2026, 11, 1, 22, 0, 0, 0, time.UTC)
	_, _ = Drift(context.Background(), l, decisionAccepted, squad, repo, now)
	a, err := Drift(context.Background(), l, decisionAccepted, squad, repo, now)
	if err != nil || len(a) != 1 || a[0].Kind != "gate_store_unreadable" || a[0].Name != broken || !strings.Contains(a[0].Detail, "could not be read") {
		t.Fatalf("an unreadable store: %v %+v", err, a)
	}
	if got := ReadAlerts(squad); len(got) != 1 || got[0].Kind != "gate_store_unreadable" {
		t.Fatalf("alerts on disk %+v", got)
	}
}

// Codex P2 3 on #854: a gate accepted for a tag in this squad's own session
// clears only the commit its target names. A seat's gated `git tag NAME`
// (no commit) cleared the first NAME@SHA the check saw, whatever commit the
// owner meant; a release-flow gate for NAME at commit A does not clear NAME
// at commit B.
func TestDriftBindsATagGateToTheCommitItsTargetNames(t *testing.T) {
	const repo = "owner/app"
	base := t.TempDir()
	squad := filepath.Join(base, "v31")
	_ = os.MkdirAll(squad, 0o700)
	l := &listed{}
	now := time.Date(2026, 11, 1, 22, 0, 0, 0, time.UTC)
	_, _ = Drift(context.Background(), l, decisionAccepted, squad, repo, now)
	a, b := strings.Repeat("a", 40), strings.Repeat("b", 40)
	l.tags = []Tag{{Name: "v9", SHA: b}}
	gateIn(t, squad, "own-cmd", "tag", gatetarget.Command("git tag", "v9"), true)
	gateIn(t, squad, "own-push", "tag", gatetarget.TagPush("refs/tags/v9"), true)
	gateIn(t, squad, "own-a", "tag", gatetarget.ReleaseTag(repo, "v9", a), true)
	if got, err := Drift(context.Background(), l, decisionAccepted, squad, repo, now); err != nil || len(got) != 1 || got[0].Kind != "tag" || got[0].SHA != b {
		t.Fatalf("v9 at B with gates for no commit and for A: %v %+v", err, got)
	}
	gateIn(t, squad, "own-b", "tag", gatetarget.ReleaseTag(repo, "v9", b), true)
	if got, err := Drift(context.Background(), l, decisionAccepted, squad, repo, now); err != nil || len(got) != 0 {
		t.Fatalf("v9 at B with B's gate: %v %+v", err, got)
	}
}

// Codex P2 4 on #854: a gate record that does not load is an alert of its
// own, naming the record, as a store whose list cannot be read is; it was
// skipped silently.
func TestDriftNamesAGateRecordThatDoesNotLoad(t *testing.T) {
	const repo = "owner/app"
	base := t.TempDir()
	squad := filepath.Join(base, "v31")
	_ = os.MkdirAll(squad, 0o700)
	bad := filepath.Join(base, "v310", "extensions", "amq-squad", "gates", "rel-torn.json")
	writeFile(t, bad, `{"gate_id": "rel-torn", "class": `)
	l := &listed{}
	now := time.Date(2026, 11, 1, 22, 0, 0, 0, time.UTC)
	_, _ = Drift(context.Background(), l, decisionAccepted, squad, repo, now)
	a, err := Drift(context.Background(), l, decisionAccepted, squad, repo, now)
	if err != nil || len(a) != 1 || a[0].Kind != "gate_store_unreadable" || a[0].Name != bad {
		t.Fatalf("a torn gate record: %v %+v", err, a)
	}
}

// Codex P2 5 on #854: an ungated alert stayed after a verified gate later
// covered its tag and release, and status kept showing it. The alert is
// resolved: once the gate covers the object, the alert file is gone.
func TestDriftResolvesAnAlertAVerifiedGateLaterCovers(t *testing.T) {
	const repo = "owner/app"
	base := t.TempDir()
	squad, release := filepath.Join(base, "v31"), filepath.Join(base, "v310")
	_ = os.MkdirAll(squad, 0o700)
	_ = os.MkdirAll(release, 0o700) // the squad identity is the existing root's
	l := &listed{}
	now := time.Date(2026, 11, 1, 22, 0, 0, 0, time.UTC)
	_, _ = Drift(context.Background(), l, decisionAccepted, squad, repo, now)
	sha := strings.Repeat("4", 40)
	l.tags, l.releases = []Tag{{Name: "v3.1.0", SHA: sha}}, []Release{{ID: 31, Tag: "v3.1.0"}}
	if a, _ := Drift(context.Background(), l, decisionAccepted, squad, repo, now); len(a) != 2 || len(ReadAlerts(squad)) != 2 {
		t.Fatalf("before the gates: %+v, on disk %+v", a, ReadAlerts(squad))
	}
	gateIn(t, release, "rel-tag", "tag", gatetarget.ReleaseTag(repo, "v3.1.0", sha), true)
	gateIn(t, release, "rel-gh", "github_release", gatetarget.ReleaseCreate(repo, "v3.1.0"), true)
	if a, err := Drift(context.Background(), l, decisionAccepted, squad, repo, now); err != nil || len(a) != 0 {
		t.Fatalf("after the gates: %v %+v", err, a)
	}
	if got := ReadAlerts(squad); len(got) != 0 {
		t.Fatalf("the covered alerts stay on disk, so status shows them: %+v", got)
	}
}

// The plan reviewer on Codex P2 5: when a verified gate clears an earlier
// ungated alert, status stops showing it and the train's audit log keeps
// both records, the alert's and its clearing's, under the alert's id.
func TestTrainAuditKeepsAnAlertAndItsClearing(t *testing.T) {
	f := newFixture(t)
	root := filepath.Join(f.dir, "root")
	release := filepath.Join(f.dir, "v310")
	_ = os.MkdirAll(release, 0o700)
	f.train.ReleaseGates, f.train.Root, f.train.DryRun, f.train.VerifyGate = &f.store, root, true, decisionAccepted
	f.req.PRs = f.req.PRs[:1]
	run := func() []Alert {
		t.Helper()
		res, err := f.train.Run(context.Background(), f.req)
		if err != nil {
			t.Fatal(err)
		}
		return res.Alerts
	}
	run() // baseline
	mustGit(t, f.bare, "tag", "v1", "main")
	head := f.head("main")
	a := run()
	if len(a) != 1 || a[0].Kind != "tag" {
		t.Fatalf("the ungated tag: %+v", a)
	}
	id := a[0].ID
	gateIn(t, release, "rel-v1", "tag", gatetarget.ReleaseTag(fixtureRepo, "v1", head), true)
	if a := run(); len(a) != 0 || len(ReadAlerts(root)) != 0 {
		t.Fatalf("after the release session's gate: %+v, on disk %+v", a, ReadAlerts(root))
	}
	entries, err := f.train.Audit.Read()
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, e := range entries {
		if e.GateID == id {
			kinds = append(kinds, e.Kind)
		}
	}
	if !slices.Equal(kinds, []string{"alert", "alert_cleared"}) {
		t.Fatalf("audit entries of %s: %v", id, kinds)
	}
}
