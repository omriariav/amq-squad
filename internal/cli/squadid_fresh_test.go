package cli

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/gatestore"
	"github.com/omriariav/amq-squad/v3/internal/gateverify"
	"github.com/omriariav/amq-squad/v3/internal/mergetrain"
	"github.com/omriariav/amq-squad/v3/internal/nostr"
)

// 3.3.x item 1 (the soak, KL7): a gate raised into a session whose AMQ root
// does not exist yet is answered. The raise creates the root and only then
// takes the squad identity (gatestore.SquadID: the root's resolved path,
// device and inode), so the record carries the identity the verifier reads
// from the same root; before, the record carried the path-only form of a
// missing root and every answer was refused no_squad_identity.
func TestAGateRaisedIntoAFreshSessionIsAnswered(t *testing.T) {
	f := newGateCLIFixture(t)
	fresh := filepath.Join(f.base, "fresh")
	if _, err := os.Stat(fresh); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the session root %s exists before the raise: %v", fresh, err)
	}
	stdout, _, err := captureOutput(t, func() error {
		return runGate([]string{"raise", "--project", f.project, "--session", "fresh", "--me", "cto", "--gate", "rel-fresh", "--action", "tag", "--target", "git tag v3.3.1", "--json"})
	})
	if err != nil {
		t.Fatalf("gate raise into a fresh session: %v\n%s", err, stdout)
	}
	store, _ := gatestore.Open(fresh)
	rec, err := store.Load("rel-fresh")
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if want := gatestore.SquadID(fresh); rec.Squad != want {
		t.Errorf("the record's squad identity is %q; the root's is %q", rec.Squad, want)
	}
	q, _ := store.Question("rel-fresh")
	if q == nil {
		t.Fatalf("no question was posted:\n%s", stdout)
	}
	tuple := "approve rel-fresh release " + strings.TrimPrefix(rec.Digest, "sha256:")[:16]
	threaded := f.answer(t, tuple, q.EventID)
	out, _, err := captureOutput(t, func() error {
		return runGate([]string{"answer", "--project", f.project, "--session", "fresh", "--from", "buzz", "--label", "acp", "--label", "nostr:" + threaded, "--json"})
	})
	if err != nil || !strings.Contains(out, `"outcome": "approved"`) {
		t.Fatalf("the owner's answer to a gate raised into a fresh session: %v\n%s", err, out)
	}
}

// freshSession is a session of the fixture's project whose AMQ root does not
// exist yet, and its resolved context.
func freshSession(t *testing.T, f gateCLIFixture, name string) (string, contextResolution) {
	t.Helper()
	root := filepath.Join(f.base, name)
	if _, err := os.Stat(root); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the session root %s exists: %v", root, err)
	}
	sel, err := newGateStoreFlags("fresh", "").resolve([]string{"--project", f.project, "--session", name, "--me", "cto"})
	if err != nil {
		t.Fatal(err)
	}
	return root, sel
}

// 3.3.x item 1, release prepare: the two gates prepare raises into a fresh
// session carry the identity of the root they are written into.
func TestReleasePrepareIntoAFreshSessionStampsTheRootsIdentity(t *testing.T) {
	f := newGateCLIFixture(t)
	gateTransport = func(context.Context, gateverify.Binding, gateverify.Pin) (gateverify.Relay, nostr.SecretKey, []string, error) {
		return nil, nostr.SecretKey{}, nil, errors.New("no Buzz transport")
	}
	t.Setenv("AMQ_SQUAD_SEAT", "")
	root, _ := freshSession(t, f, "fresh")
	git := func(args ...string) string {
		cmd := exec.Command("git", append([]string{"-C", f.project}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=owner", "GIT_AUTHOR_EMAIL=o@example.com", "GIT_COMMITTER_NAME=owner",
			"GIT_COMMITTER_EMAIL=o@example.com", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q", "-b", "main")
	git("commit", "-q", "--allow-empty", "-m", "v1")
	notes := filepath.Join(t.TempDir(), "notes.md")
	if err := os.WriteFile(notes, []byte("# v1.2.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := captureOutput(t, func() error {
		return runRelease([]string{"prepare", "--repository", "owner/fixture", "--tag", "v1.2.0", "--commit", git("rev-parse", "HEAD"), "--notes-file", notes,
			"--gh", releaseFakeGH(t, filepath.Join(t.TempDir(), "gh.log")), "--project", f.project, "--session", "fresh", "--me", "cto", "--json"})
	}); err != nil {
		t.Fatalf("release prepare into a fresh session: %v", err)
	}
	store, _ := gatestore.Open(root)
	ids, _ := store.IDs()
	if len(ids) != 2 {
		t.Fatalf("prepare raised %v", ids)
	}
	for _, id := range ids {
		rec, err := store.Load(id)
		if err != nil {
			t.Fatal(err)
		}
		if want := gatestore.SquadID(root); rec.Squad != want {
			t.Errorf("gate %s carries the squad identity %q; the root's is %q", id, rec.Squad, want)
		}
	}
}

// 3.3.x item 1, the merge train: the escalator and the accepted-gate lookup
// are attached before the session root exists; a gate the escalator raises
// afterwards is written into the root with its identity, and once the owner
// approves it the attached lookup finds it (Codex pass 2 on the prototype:
// the lookup's squad was frozen at attach too).
func TestTheMergeTrainsRaiseIntoAFreshSessionStampsTheRootsIdentity(t *testing.T) {
	f := newGateCLIFixture(t)
	root, sel := freshSession(t, f, "fresh")
	train := &mergetrain.Train{}
	attachMergeGates(context.Background(), train, sel)
	if train.Escalate == nil || train.Gates == nil {
		t.Fatal("no escalator or accepted-gate lookup attached")
	}
	rec, err := train.Escalate.Raise(gatestore.Spec{GateID: "merge-fresh", Action: "tag", Target: "git tag v3.3.1", RaisedBy: "lead"})
	if err != nil {
		t.Fatalf("the train's raise into a fresh session: %v", err)
	}
	if want := gatestore.SquadID(root); rec.Squad != want {
		t.Fatalf("the train's gate carries the squad identity %q; the root's is %q", rec.Squad, want)
	}
	store, _ := gatestore.Open(root)
	stored, lerr := store.Load("merge-fresh")
	q, _ := store.Question("merge-fresh")
	if lerr != nil || stored.Squad != rec.Squad || q == nil {
		t.Fatalf("the train's gate is not stored with its question: %+v %v (question %+v)", stored, lerr, q)
	}
	if _, ok := train.Gates.Accepted("tag", "git tag v3.3.1"); ok {
		t.Fatal("the lookup accepted a gate the owner has not answered")
	}
	threaded := f.answer(t, "approve merge-fresh release "+strings.TrimPrefix(rec.Digest, "sha256:")[:16], q.EventID)
	if out, _, err := captureOutput(t, func() error {
		return runGate([]string{"answer", "--project", f.project, "--session", "fresh", "--from", "buzz", "--label", "acp", "--label", "nostr:" + threaded, "--json"})
	}); err != nil || !strings.Contains(out, `"outcome": "approved"`) {
		t.Fatalf("the owner's answer: %v\n%s", err, out)
	}
	if id, ok := train.Gates.Accepted("tag", "git tag v3.3.1"); !ok || id != "merge-fresh" {
		t.Fatalf("the train attached before the root existed does not find the approved gate: %q %v", id, ok)
	}
}

// 3.3.x item 1, gate raise with no transport (Codex pass 2): the record
// written into a fresh session waits for gate post and carries the root's
// identity.
func TestAGateRaisedIntoAFreshSessionWithoutTransportStampsTheRootsIdentity(t *testing.T) {
	f := newGateCLIFixture(t)
	gateTransport = func(context.Context, gateverify.Binding, gateverify.Pin) (gateverify.Relay, nostr.SecretKey, []string, error) {
		return nil, nostr.SecretKey{}, nil, errors.New("no Buzz transport")
	}
	root, _ := freshSession(t, f, "fresh")
	if _, _, err := captureOutput(t, func() error {
		return runGate([]string{"raise", "--project", f.project, "--session", "fresh", "--me", "cto", "--gate", "rel-quiet", "--action", "tag", "--target", "git tag v3.3.1"})
	}); err != nil && !strings.Contains(err.Error(), "question") {
		t.Fatalf("gate raise with no transport: %v", err)
	}
	store, _ := gatestore.Open(root)
	rec, err := store.Load("rel-quiet")
	if err != nil {
		t.Fatal(err)
	}
	if want := gatestore.SquadID(root); rec.Squad != want {
		t.Fatalf("the record carries the squad identity %q; the root's is %q", rec.Squad, want)
	}
}

// 3.3.x item 1, the growth gate: raised with no transport (the record waits
// for gate post), it carries the identity of the root it is written into;
// before, that path wrote no identity at all.
func TestTheGrowthGateWithoutTransportStampsTheRootsIdentity(t *testing.T) {
	f := newGateCLIFixture(t)
	gateTransport = func(context.Context, gateverify.Binding, gateverify.Pin) (gateverify.Relay, nostr.SecretKey, []string, error) {
		return nil, nostr.SecretKey{}, nil, errors.New("no Buzz transport")
	}
	root, sel := freshSession(t, f, "fresh")
	id, err := raiseGrowthGate(storeSelection{root: root, ctx: &sel}, "lead", "t9", "Grow the parser", "seat qa over max_seats", nil)
	if err == nil || !strings.Contains(err.Error(), "question not sent") {
		t.Fatalf("the growth gate with no transport: %v", err)
	}
	store, _ := gatestore.Open(root)
	rec, lerr := store.Load(id)
	if lerr != nil {
		t.Fatal(lerr)
	}
	if want := gatestore.SquadID(root); rec.Squad != want {
		t.Fatalf("the growth gate carries the squad identity %q; the root's is %q", rec.Squad, want)
	}
}

// 3.3.x item 1 (Codex pass 1 on the prototype): a refused raise creates no
// session root: gate raise with a window the record refuses (no transport,
// so nothing else writes), and release prepare whose commit is not on the
// branch.
func TestARefusedRaiseIntoAFreshSessionCreatesNoRoot(t *testing.T) {
	f := newGateCLIFixture(t)
	gateTransport = func(context.Context, gateverify.Binding, gateverify.Pin) (gateverify.Relay, nostr.SecretKey, []string, error) {
		return nil, nostr.SecretKey{}, nil, errors.New("no Buzz transport")
	}
	t.Setenv("AMQ_SQUAD_SEAT", "")
	root, _ := freshSession(t, f, "fresh")
	if _, _, err := captureOutput(t, func() error {
		return runGate([]string{"raise", "--project", f.project, "--session", "fresh", "--me", "cto", "--gate", "bad-window", "--action", "tag", "--target", "git tag v1", "--window", "1s"})
	}); err == nil {
		t.Fatal("gate raise with a one-second window was accepted")
	}
	if _, err := os.Stat(root); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("a refused gate raise created the session root: %v", err)
	}
	cmd := exec.Command("git", "-C", f.project, "init", "-q", "-b", "main")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	notes := filepath.Join(t.TempDir(), "notes.md")
	if err := os.WriteFile(notes, []byte("# v1.2.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := captureOutput(t, func() error {
		return runRelease([]string{"prepare", "--repository", "owner/fixture", "--tag", "v1.2.0", "--commit", strings.Repeat("0", 40), "--notes-file", notes,
			"--gh", releaseFakeGH(t, filepath.Join(t.TempDir(), "gh.log")), "--project", f.project, "--session", "fresh", "--me", "cto"})
	}); err == nil {
		t.Fatal("release prepare of a commit not on the branch was accepted")
	}
	if _, err := os.Stat(root); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("a refused release prepare created the session root: %v", err)
	}
}

// 3.3.x item 1 (Codex pass 3 on the prototype), two prepares into a fresh
// session: the flow is built before the root exists; another writer then
// raises the same first gate (creating the root) before this prepare's own
// write. Prepare reads the root's identity at its check, so it takes that
// gate as its own and completes; a gate of the same id carrying another
// squad's identity is refused as a conflict.
func TestTwoReleasePreparesIntoAFreshSessionAgreeOnTheGate(t *testing.T) {
	for _, c := range []struct {
		name    string
		foreign bool
	}{{"the same squad's gate", false}, {"another squad's gate", true}} {
		t.Run(c.name, func(t *testing.T) {
			f := newGateCLIFixture(t)
			gateTransport = func(context.Context, gateverify.Binding, gateverify.Pin) (gateverify.Relay, nostr.SecretKey, []string, error) {
				return nil, nostr.SecretKey{}, nil, errors.New("no Buzz transport")
			}
			t.Setenv("AMQ_SQUAD_SEAT", "")
			root, _ := freshSession(t, f, "fresh")
			first := ""
			releasePrepareRaiseHook = func(spec gatestore.Spec) {
				if first != "" {
					return
				}
				first = spec.GateID
				other, _ := gatestore.Open(root)
				id := gatestore.Identity{OwnerPubKey: strings.Repeat("a", 64), IssuerKeyID: "ed25519-sha256:" + strings.Repeat("b", 64)}
				spec.Squad = ""
				if c.foreign {
					spec.Squad = mergetrain.SquadOf("/another/squad")
				}
				stamped, err := other.StampSquad(spec, time.Now(), id)
				if err != nil {
					t.Fatal(err)
				}
				rec, err := gatestore.Render(stamped, time.Now(), id)
				if err == nil {
					err = other.Create(rec)
				}
				if err != nil {
					t.Fatalf("the other writer's raise: %v", err)
				}
			}
			t.Cleanup(func() { releasePrepareRaiseHook = func(gatestore.Spec) {} })
			cmd := exec.Command("git", "-C", f.project, "init", "-q", "-b", "main")
			cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git init: %v: %s", err, out)
			}
			commit := exec.Command("git", "-C", f.project, "commit", "-q", "--allow-empty", "-m", "v1")
			commit.Env = append(os.Environ(), "GIT_AUTHOR_NAME=owner", "GIT_AUTHOR_EMAIL=o@example.com", "GIT_COMMITTER_NAME=owner",
				"GIT_COMMITTER_EMAIL=o@example.com", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
			if out, err := commit.CombinedOutput(); err != nil {
				t.Fatalf("git commit: %v: %s", err, out)
			}
			head, _ := exec.Command("git", "-C", f.project, "rev-parse", "HEAD").Output()
			notes := filepath.Join(t.TempDir(), "notes.md")
			if err := os.WriteFile(notes, []byte("# v1.2.0\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			_, _, err := captureOutput(t, func() error {
				return runRelease([]string{"prepare", "--repository", "owner/fixture", "--tag", "v1.2.0", "--commit", strings.TrimSpace(string(head)), "--notes-file", notes,
					"--gh", releaseFakeGH(t, filepath.Join(t.TempDir(), "gh.log")), "--project", f.project, "--session", "fresh", "--me", "cto"})
			})
			if first == "" {
				t.Fatal("prepare raised no gate")
			}
			if c.foreign {
				if err == nil || !strings.Contains(err.Error(), "conflict") {
					t.Fatalf("prepare took another squad's gate %s as its own: %v", first, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("prepare refused the gate %s another writer raised into the fresh root: %v", first, err)
			}
		})
	}
}

// 3.3.x item 1 (Codex pass 3), the merge train with no transport: the gate
// its escalator writes into a fresh session (waiting for gate post) carries
// the root's identity.
func TestTheMergeTrainsRaiseWithoutTransportStampsTheRootsIdentity(t *testing.T) {
	f := newGateCLIFixture(t)
	gateTransport = func(context.Context, gateverify.Binding, gateverify.Pin) (gateverify.Relay, nostr.SecretKey, []string, error) {
		return nil, nostr.SecretKey{}, nil, errors.New("no Buzz transport")
	}
	root, sel := freshSession(t, f, "fresh")
	train := &mergetrain.Train{}
	attachMergeGates(context.Background(), train, sel)
	if train.Escalate == nil {
		t.Fatal("no escalator attached")
	}
	if _, err := train.Escalate.Raise(gatestore.Spec{GateID: "merge-quiet", Action: "tag", Target: "git tag v3.3.1", RaisedBy: "lead"}); err != nil {
		t.Fatalf("the train's raise with no transport: %v", err)
	}
	store, _ := gatestore.Open(root)
	rec, err := store.Load("merge-quiet")
	if err != nil {
		t.Fatal(err)
	}
	if want := gatestore.SquadID(root); rec.Squad != want {
		t.Fatalf("the train's gate carries the squad identity %q; the root's is %q", rec.Squad, want)
	}
}
