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

	"github.com/omriariav/amq-squad/v3/internal/grants"
)

// Codex review of bfe61050. One row per item; each fails with its fix
// reverted.

// P1 1: the grant is checked on the repository the checkout is, never on
// the request's label.
func TestMayorAuthorizesTheCheckoutNotTheLabel(t *testing.T) {
	f := newMayor(t, fleetGrant(4, "owner/app"))
	req := f.requestAt(t, "app", "owner/evil", false)
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
	ok := f.requestAt(t, "real", "owner/app", false)
	ok.Squad.Repository = "someone/else"
	if r := mustStart(t, f.m, ok); r.Outcome != OutcomeStarted {
		t.Fatalf("permitted checkout: %+v", r)
	}
	reg, _ := Read(f.m.RegistryPath)
	if reg.Squads[0].Repository != "owner/app" || reg.Squads[0].ProjectPath != filepath.Join(f.dir, "real") {
		t.Fatalf("registered as %+v", reg.Squads[0])
	}
}

// P1 1, second pass: the checkout's own git config is not an identity
// source. A checkout whose origin says owner/app but that has no identity
// file and no repository_paths entry is unidentified.
func TestOriginRemoteIsNotAnIdentitySource(t *testing.T) {
	f := newMayor(t, fleetGrant(4, "owner/app"))
	req := f.requestAt(t, "app", "", false) // origin github.com/owner/app, no file
	if r := mustStart(t, f.m, req); r.Outcome != OutcomeRefused || r.Reason != ReasonUnidentified {
		t.Fatalf("an origin-only checkout: %+v, want repository_unidentified", r)
	}
	// the owner names the checkout in the grant: that identifies it
	g := fleetGrant(4, "owner/app")
	g.Fleet.RepositoryPaths = []grants.RepositoryPath{{Repository: "owner/app", Path: req.Squad.ProjectPath, Remote: "github.com/owner/app"}}
	f.grant = g
	if r := mustStart(t, f.m, req); r.Outcome != OutcomeStarted {
		t.Fatalf("a checkout the grant maps: %+v", r)
	}
}

func TestRepositoryIdentity(t *testing.T) {
	for url, want := range map[string]string{
		"git@github.com:owner/app.git":           "github.com/owner/app",
		"https://github.com/owner/app":           "github.com/owner/app",
		"https://github.com/owner/app.git/":      "github.com/owner/app",
		"ssh://git@github.com/owner/app":         "github.com/owner/app",
		"https://evil.example/owner/app.git":     "evil.example/owner/app",
		"https://gitlab.example/group/owner/app": "gitlab.example/group/owner/app",
		"git@gitlab.example:group/owner/app.git": "gitlab.example/group/owner/app",
	} {
		if got := RemoteAddress(url); got != want {
			t.Errorf("RemoteAddress(%q) = %q, want %q", url, got, want)
		}
	}
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	write := func(p, s string) {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	origin := func(repo, url string) {
		write(filepath.Join(repo, ".git", "config"), "[remote \"origin\"]\n\turl = "+url+"\n")
	}
	app := filepath.Join(dir, "app")
	origin(app, "git@github.com:owner/app.git")
	mapped := grants.Fleet{RepositoryPaths: []grants.RepositoryPath{{Repository: "owner/app", Path: app, Remote: "github.com/owner/app"}}}
	if id, canon, err := RepositoryIdentity(app, mapped); err != nil || id != "owner/app" || canon != app {
		t.Fatalf("grant mapping: %q %q %v", id, canon, err)
	}
	// an origin that disagrees with the grant's remote: unidentified, for
	// the over-normalizations of the first pass too
	for _, url := range []string{"https://evil.example/owner/app.git", "https://gitlab.example/group/owner/app"} {
		origin(app, url)
		if _, _, err := RepositoryIdentity(app, mapped); err == nil {
			t.Errorf("origin %s matched github.com/owner/app", url)
		}
	}
	origin(app, "git@github.com:owner/app.git")
	// the identity file disagreeing with the grant mapping: unidentified
	write(filepath.Join(app, IdentityFile), "owner/other\n")
	if _, _, err := RepositoryIdentity(app, mapped); err == nil {
		t.Fatal("a grant mapping and an identity file that disagree identified the checkout")
	}
	// a linked worktree: the origin for the comparison is in the commondir
	main, wt := filepath.Join(dir, "main"), filepath.Join(dir, "wt")
	gitdir := filepath.Join(main, ".git", "worktrees", "wt")
	origin(main, "https://evil.example/owner/wt")
	write(filepath.Join(gitdir, "commondir"), "../..\n")
	write(filepath.Join(wt, ".git"), "gitdir: "+gitdir+"\n")
	wtGrant := grants.Fleet{RepositoryPaths: []grants.RepositoryPath{{Repository: "owner/wt", Path: wt, Remote: "github.com/owner/wt"}}}
	if _, _, err := RepositoryIdentity(wt, wtGrant); err == nil {
		t.Fatal("a worktree whose shared origin disagrees with the grant was identified")
	}
	// the identity file alone; a symlinked one is refused
	plain := filepath.Join(dir, "plain")
	write(filepath.Join(plain, IdentityFile), "owner/plain\n")
	if id, _, err := RepositoryIdentity(plain, grants.Fleet{}); err != nil || id != "owner/plain" {
		t.Fatalf("identity file: %q %v", id, err)
	}
	link := filepath.Join(dir, "linked")
	if err := os.MkdirAll(filepath.Join(link, ".amq-squad"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(plain, IdentityFile), filepath.Join(link, IdentityFile)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := RepositoryIdentity(link, grants.Fleet{}); err == nil {
		t.Fatal("a symlinked identity file was read")
	}
	// a symlinked checkout path resolves to the canonical checkout
	alias := filepath.Join(dir, "alias")
	if err := os.Symlink(plain, alias); err != nil {
		t.Fatal(err)
	}
	if _, canon, err := RepositoryIdentity(alias, grants.Fleet{}); err != nil || canon != plain {
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
	home, outside := canonicalTemp(t), canonicalTemp(t)
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

// P1 4, second pass: the anchor itself is opened from / with O_NOFOLLOW,
// so a symlinked HOME, and a symlinked ancestor of a --fleet-file outside
// HOME, are refused, not followed.
func TestRegistryAnchorIsNotFollowed(t *testing.T) {
	real, outside := canonicalTemp(t), canonicalTemp(t)
	linkHome := filepath.Join(outside, "home-link")
	if err := os.Symlink(real, linkHome); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", linkHome)
	if err := Update(filepath.Join(linkHome, ".amq-squad", "fleet.yaml"), func(*Registry) error { return nil }); err == nil {
		t.Fatal("an update under a symlinked HOME succeeded")
	}
	if names, _ := os.ReadDir(real); len(names) != 0 {
		t.Fatalf("the update wrote through the symlinked HOME: %v", names)
	}
	t.Setenv("HOME", canonicalTemp(t))
	// outside HOME: /outside/link -> real, path /outside/link/sub/fleet.yaml
	if err := os.MkdirAll(filepath.Join(real, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(outside, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(link, "sub", "fleet.yaml")
	if err := Update(path, func(*Registry) error { return nil }); err == nil {
		t.Fatal("an update through a symlinked ancestor outside HOME succeeded")
	}
	if names, _ := os.ReadDir(filepath.Join(real, "sub")); len(names) != 0 {
		t.Fatalf("the update wrote through the symlinked ancestor: %v", names)
	}
	if _, err := Read(path); err == nil {
		t.Fatal("a read through a symlinked ancestor succeeded")
	}
}

func canonicalTemp(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
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
