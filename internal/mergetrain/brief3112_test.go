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
	// a gate whose target names another commit clears nothing; nor does
	// another session's gated command, which names no commit
	gateIn(t, release, "rel-tag-other", "tag", gatetarget.ReleaseTag(repo, "v3.1.0", other), true)
	gateIn(t, release, "rel-cmd", "tag", gatetarget.Command("git tag", "v3.1.0"), true)
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
