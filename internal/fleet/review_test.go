package fleet

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Codex review of bfe61050. One row per item; each fails with its fix
// reverted.

// P1 1: the grant is checked on the repository the checkout is, never on
// the request's label.
func TestMayorAuthorizesTheCheckoutNotTheLabel(t *testing.T) {
	f := newMayor(t, fleetGrant(4, "owner/app"))
	req := f.requestAt(t, "app", "https://github.com/owner/evil.git", false)
	if req.Squad.Repository != "owner/app" {
		t.Fatalf("fixture label = %q", req.Squad.Repository)
	}
	r := mustStart(t, f.m, req)
	if r.Outcome != OutcomeRefused || r.Reason != ReasonRepository || !strings.Contains(r.Detail, "owner/evil") || !strings.Contains(r.Detail, "display only") {
		t.Fatalf("a permitted label beside a disallowed checkout: %+v", r)
	}
	if len(f.writer.proposed)+len(f.launcher.launched)+len(f.registered(t)) != 0 {
		t.Fatal("the refused start wrote")
	}
	// a checkout that is owner/app starts under any label, registered as itself
	ok := f.requestAt(t, "real", "git@github.com:owner/app.git", false)
	ok.Squad.Repository = "someone/else"
	if r := mustStart(t, f.m, ok); r.Outcome != OutcomeStarted {
		t.Fatalf("permitted checkout: %+v", r)
	}
	reg, _ := Read(f.m.RegistryPath)
	if reg.Squads[0].Repository != "owner/app" || reg.Squads[0].ProjectPath != filepath.Join(f.dir, "real") {
		t.Fatalf("registered as %+v", reg.Squads[0])
	}
	// no origin remote and no identity file: unidentified, refused
	if r := mustStart(t, f.m, f.requestAt(t, "anon", "", false)); r.Outcome != OutcomeRefused || r.Reason != ReasonUnidentified {
		t.Fatalf("unidentified checkout: %+v", r)
	}
}

func TestRepositoryIdentity(t *testing.T) {
	for url, want := range map[string]string{
		"git@github.com:owner/app.git":      "owner/app",
		"https://github.com/owner/app":      "owner/app",
		"https://github.com/owner/app.git/": "owner/app",
		"ssh://git@github.com:22/owner/app": "owner/app",
		"git://example.org/group/owner/app": "owner/app",
		"/srv/git/app.git":                  "",
		"https://github.com/onlyone":        "",
	} {
		if got := identityFromURL(url); got != want {
			t.Errorf("identityFromURL(%q) = %q, want %q", url, got, want)
		}
	}
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	// a linked worktree: .git is a gitdir file, the config is in the commondir
	main, wt := filepath.Join(dir, "main"), filepath.Join(dir, "wt")
	gitdir := filepath.Join(main, ".git", "worktrees", "wt")
	for _, d := range []string{gitdir, wt} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(p, s string) {
		if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(main, ".git", "config"), "[remote \"origin\"]\n\turl = git@github.com:owner/app.git\n")
	write(filepath.Join(gitdir, "commondir"), "../..\n")
	write(filepath.Join(wt, ".git"), "gitdir: "+gitdir+"\n")
	if id, canon, err := RepositoryIdentity(wt); err != nil || id != "owner/app" || canon != wt {
		t.Fatalf("worktree identity: %q %q %v", id, canon, err)
	}
	// the identity file when there is no remote; a symlinked one is refused
	plain := filepath.Join(dir, "plain")
	if err := os.MkdirAll(filepath.Join(plain, ".amq-squad"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(plain, IdentityFile), "owner/plain\n")
	if id, _, err := RepositoryIdentity(plain); err != nil || id != "owner/plain" {
		t.Fatalf("identity file: %q %v", id, err)
	}
	link := filepath.Join(dir, "linked")
	if err := os.MkdirAll(filepath.Join(link, ".amq-squad"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(plain, IdentityFile), filepath.Join(link, IdentityFile)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := RepositoryIdentity(link); err == nil {
		t.Fatal("a symlinked identity file was read")
	}
	// a symlinked checkout path resolves to the canonical checkout
	alias := filepath.Join(dir, "alias")
	if err := os.Symlink(plain, alias); err != nil {
		t.Fatal(err)
	}
	if _, canon, err := RepositoryIdentity(alias); err != nil || canon != plain {
		t.Fatalf("canonical = %q %v", canon, err)
	}
}

type slowLauncher struct {
	mu   sync.Mutex
	n    int
	fail bool
}

func (l *slowLauncher) Launch(context.Context, LaunchRequest) error {
	time.Sleep(20 * time.Millisecond)
	l.mu.Lock()
	defer l.mu.Unlock()
	l.n++
	if l.fail {
		return errors.New("launch failed")
	}
	return nil
}

// P1 2: admission runs inside the registry lock: two mayors with
// max_squads 1 starting at once take one slot; two starts of one checkout
// under two names register one; a failed launch releases its slot.
func TestConcurrentStartsTakeOneSlot(t *testing.T) {
	f := newMayor(t, fleetGrant(1, "owner/app", "owner/web"))
	reqs := []StartRequest{f.request(t, "app", false), f.request(t, "web", false)}
	run := func(reqs []StartRequest) []Result {
		launcher := &slowLauncher{}
		results := make([]Result, len(reqs))
		var wg sync.WaitGroup
		for i, req := range reqs {
			wg.Add(1)
			go func(i int, req StartRequest) {
				defer wg.Done()
				m := &Mayor{Grant: f.m.Grant, RegistryPath: f.m.RegistryPath, Squads: &fakeWriter{}, Launcher: launcher, Session: true}
				r, err := m.Start(context.Background(), req)
				if err != nil {
					t.Error(err)
				}
				results[i] = r
			}(i, req)
		}
		wg.Wait()
		return results
	}
	res := run(reqs)
	started := 0
	for _, r := range res {
		if r.Outcome == OutcomeStarted {
			started++
		} else if r.Outcome != OutcomeWaiting || r.Reason != ReasonMaxSquads {
			t.Fatalf("the losing start: %+v", r)
		}
	}
	if got := f.registered(t); started != 1 || len(got) != 1 {
		t.Fatalf("max_squads 1, two concurrent starts: %d started, registry %v", started, got)
	}

	// one checkout, two names, at once: one registers, the other is refused
	g := newMayor(t, fleetGrant(4, "owner/app"))
	a := g.request(t, "app", false)
	b := a
	b.Squad.Name = "app2"
	f.m.RegistryPath = g.m.RegistryPath
	f.m.Grant = g.m.Grant
	res = run([]StartRequest{a, b})
	if got := g.registered(t); len(got) != 1 {
		t.Fatalf("one checkout under two names: registry %v, results %+v", got, res)
	}
	refused := res[0].Reason == ReasonRegistered || res[1].Reason == ReasonRegistered
	if !refused {
		t.Fatalf("no squad_registered refusal: %+v", res)
	}

	// a failed launch releases the reserved slot
	h := newMayor(t, fleetGrant(1, "owner/app"))
	h.m.Launcher = &slowLauncher{fail: true}
	if r := mustStart(t, h.m, h.request(t, "app", false)); r.Outcome != OutcomeRefused || r.Reason != ReasonLaunchFailure {
		t.Fatalf("failed launch: %+v", r)
	}
	if got := h.registered(t); len(got) != 0 {
		t.Fatalf("a failed launch kept its slot: %v", got)
	}
}

// P1 3: the owner grant is reloaded on every start and every retry; a
// queued start the grant no longer permits leaves the queue with a named
// reason and its waiting row is dropped.
func TestRetryReloadsTheGrant(t *testing.T) {
	f := newMayor(t, fleetGrant(1, "owner/app", "owner/web"))
	mustStart(t, f.m, f.request(t, "app", false))
	if r := mustStart(t, f.m, f.request(t, "web", false)); r.Outcome != OutcomeWaiting {
		t.Fatalf("queued: %+v", r)
	}
	if reg, _ := Read(f.m.RegistryPath); len(reg.Waiting) != 1 {
		t.Fatalf("waiting row: %+v", reg.Waiting)
	}
	f.grant = fleetGrant(1, "owner/app") // the owner removed owner/web
	f.stopped["app"] = true              // a slot is free
	res, err := f.m.Retry(context.Background())
	if err != nil || res["web"].Outcome != OutcomeRefused || res["web"].Reason != ReasonRepository {
		t.Fatalf("retry after the permission was removed: %+v %v", res, err)
	}
	if len(f.m.Waiting) != 0 || len(f.launcher.launched) != 1 {
		t.Fatalf("queue %d, launches %d", len(f.m.Waiting), len(f.launcher.launched))
	}
	if reg, _ := Read(f.m.RegistryPath); len(reg.Waiting) != 0 {
		t.Fatalf("the waiting row survived the refusal: %+v", reg.Waiting)
	}
	// a fleet grant removed entirely stops a start the same way
	f.grant.Fleet.MaxSquads = 0
	if r := mustStart(t, f.m, f.request(t, "app2", false)); r.Outcome != OutcomeRefused || r.Reason != ReasonNoFleetGrant {
		t.Fatalf("no fleet grant: %+v", r)
	}
}

// P1 4: under HOME the registry is reached component by component from HOME
// with O_NOFOLLOW, so a symlink at any level, not only the final parent, is
// refused for update and read, and nothing lands outside.
func TestRegistryAnchoredFromHome(t *testing.T) {
	home, outside := realTempDir(t), realTempDir(t)
	t.Setenv("HOME", home)
	if err := os.Symlink(outside, filepath.Join(home, ".amq-squad")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(outside, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".amq-squad", "sub", "fleet.yaml")
	if err := Update(path, func(*Registry) error { return nil }); err == nil {
		t.Fatal("an update through a symlinked ~/.amq-squad succeeded")
	}
	if names, _ := os.ReadDir(filepath.Join(outside, "sub")); len(names) != 0 {
		t.Fatalf("the update wrote outside HOME: %v", names)
	}
	if _, err := Read(path); err == nil {
		t.Fatal("a read through a symlinked ~/.amq-squad succeeded")
	}
	good := filepath.Join(home, "real", "fleet.yaml")
	if err := Update(good, func(r *Registry) error { r.Squads = sampleRegistry().Squads; return nil }); err != nil {
		t.Fatal(err)
	}
	if r, err := Read(good); err != nil || len(r.Squads) != 2 {
		t.Fatalf("anchored read: %+v %v", r, err)
	}
}

// P2 5: a second operator: block after a squad item is a repeated key, not
// an overwrite of the routing values.
func TestParseRefusesASecondOperatorBlock(t *testing.T) {
	text := Render(sampleRegistry()) + "operator:\n  root: /evil\n  project: evil\n  session: evil\n"
	if _, err := Parse(text, "f"); err == nil || !strings.Contains(err.Error(), "repeated") {
		t.Fatalf("a second operator block parsed: %v", err)
	}
	text = strings.Replace(Render(sampleRegistry()), "  session: fleet\n", "  session: fleet\n  session: evil\n", 1)
	if _, err := Parse(text, "f"); err == nil {
		t.Fatal("a repeated operator key parsed")
	}
}
