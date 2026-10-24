package fleet

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/grants"
)

type fakeWriter struct {
	proposed, applied []string
}

func (w *fakeWriter) Propose(projectPath, squadYAML string) (string, error) {
	if !strings.HasPrefix(squadYAML, "schema: 1") {
		return "", errors.New("not a squad.yaml")
	}
	w.proposed = append(w.proposed, projectPath)
	return "sha256:" + filepath.Base(projectPath), nil
}

func (w *fakeWriter) Apply(projectPath, digest string) error {
	if digest != "sha256:"+filepath.Base(projectPath) {
		return errors.New("digest mismatch")
	}
	w.applied = append(w.applied, projectPath)
	return nil
}

type fakeLauncher struct{ launched []LaunchRequest }

func (l *fakeLauncher) Launch(_ context.Context, req LaunchRequest) error {
	l.launched = append(l.launched, req)
	return nil
}

type spend float64

func (s spend) SpentToday(_ time.Time) float64 { return float64(s) }

func fleetGrant(maxSquads int, repos ...string) grants.Resolved {
	g := grants.Defaults()
	g.Fleet = grants.Fleet{MaxSquads: maxSquads, CostPerDayUSD: 50, PermittedRepositories: repos}
	return grants.Resolved{Grants: g, Source: "test"}
}

type mayorFixture struct {
	grant    grants.Resolved // what the owner file says now; reloaded on every start
	m        *Mayor
	writer   *fakeWriter
	launcher *fakeLauncher
	dir      string
	stopped  map[string]bool
}

func newMayor(t *testing.T, g grants.Resolved) *mayorFixture {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := &mayorFixture{grant: g, writer: &fakeWriter{}, launcher: &fakeLauncher{}, dir: dir, stopped: map[string]bool{}}
	f.m = &Mayor{Grant: func() (grants.Resolved, error) { return f.grant, nil }, RegistryPath: filepath.Join(f.dir, "home", ".amq-squad", "fleet.yaml"), Squads: f.writer, Launcher: f.launcher,
		Live: func(s Squad) bool { return !f.stopped[s.Name] }, Session: true}
	return f
}

// request is a start for owner/<name> in a checkout whose origin remote is
// git@github.com:owner/<name>.git and that has an .amqrc (provisionable),
// unless bare.
func (f *mayorFixture) request(t *testing.T, name string, bare bool) StartRequest {
	t.Helper()
	return f.requestAt(t, name, "owner/"+name, bare)
}

// requestAt makes a checkout whose protected identity file says identity
// ("" writes none) and whose origin remote is github.com/owner/<name>, so
// the remote never decides (it is not an identity source).
func (f *mayorFixture) requestAt(t *testing.T, name, identity string, bare bool) StartRequest {
	t.Helper()
	repo := filepath.Join(f.dir, name)
	for _, d := range []string{".git", ".amq-squad"} {
		if err := os.MkdirAll(filepath.Join(repo, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	config := "[remote \"origin\"]\n\turl = git@github.com:owner/" + name + ".git\n"
	if err := os.WriteFile(filepath.Join(repo, ".git", "config"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	if identity != "" {
		if err := os.WriteFile(filepath.Join(repo, IdentityFile), []byte(identity+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if !bare {
		if err := os.WriteFile(filepath.Join(repo, ".amqrc"), []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return StartRequest{
		Squad:     Squad{Name: name, Root: filepath.Join(repo, ".agent-mail", name), ProjectPath: repo, Repository: "owner/" + name, Lead: "lead"},
		SquadYAML: "schema: 1\nlead: lead\nseats:\n  - handle: lead\n    role: lead\n    runtime: pi\n",
	}
}

func (f *mayorFixture) registered(t *testing.T) []string {
	t.Helper()
	r, err := Read(f.m.RegistryPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, s := range r.Squads {
		names = append(names, s.Name)
	}
	return names
}

func mustStart(t *testing.T, m *Mayor, req StartRequest) Result {
	t.Helper()
	r, err := m.Start(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// Acceptance 4: with max_squads 2 the third start waits (and starts once a
// squad stops); a repository not in the permitted list is refused; nothing
// is written for either.
func TestMayorInsideTheGrant(t *testing.T) {
	f := newMayor(t, fleetGrant(2, "owner/app", "owner/noc", "Owner/Docs"))
	for _, name := range []string{"app", "noc"} {
		if r := mustStart(t, f.m, f.request(t, name, false)); r.Outcome != OutcomeStarted || r.Digest != "sha256:"+name {
			t.Fatalf("start %s: %+v", name, r)
		}
	}
	third := f.request(t, "docs", false)
	r := mustStart(t, f.m, third)
	if r.Outcome != OutcomeWaiting || r.Reason != ReasonMaxSquads {
		t.Fatalf("third start: %+v, want waiting max_squads", r)
	}
	evil := f.request(t, "evil", false)
	r = mustStart(t, f.m, evil)
	if r.Outcome != OutcomeRefused || r.Reason != ReasonRepository {
		t.Fatalf("unpermitted repository: %+v", r)
	}
	if len(f.writer.proposed) != 2 || len(f.writer.applied) != 2 || len(f.launcher.launched) != 2 {
		t.Fatalf("a waiting or refused start wrote: proposed %v applied %v launched %d", f.writer.proposed, f.writer.applied, len(f.launcher.launched))
	}
	if got := f.registered(t); strings.Join(got, ",") != "app,noc" {
		t.Fatalf("registry = %v", got)
	}
	if len(f.m.Waiting) != 1 || f.m.Waiting[0].Squad.Name != "docs" {
		t.Fatalf("waiting = %+v", f.m.Waiting)
	}
	// still full: Retry keeps it waiting
	if res, err := f.m.Retry(context.Background()); err != nil || res["docs"].Outcome != OutcomeWaiting || len(f.m.Waiting) != 1 {
		t.Fatalf("retry while full: %+v %v", res, err)
	}
	f.stopped["app"] = true
	res, err := f.m.Retry(context.Background())
	if err != nil || res["docs"].Outcome != OutcomeStarted || len(f.m.Waiting) != 0 {
		t.Fatalf("retry after a squad stopped: %+v %v", res, err)
	}
	if got := f.registered(t); strings.Join(got, ",") != "app,noc,docs" {
		t.Fatalf("registry = %v", got)
	}
	if l := f.launcher.launched[2]; l.ProjectPath != third.Squad.ProjectPath || l.Session != "docs" || l.Digest != "sha256:docs" || l.SquadYAML != third.SquadYAML {
		t.Fatalf("launch request = %+v", l)
	}
	// one squad per repository root
	if r := mustStart(t, f.m, f.request(t, "noc", false)); r.Outcome != OutcomeRefused || r.Reason != ReasonRegistered {
		t.Fatalf("second squad for a root: %+v", r)
	}
}

func TestMayorCostAndScope(t *testing.T) {
	f := newMayor(t, fleetGrant(4, "owner/app"))
	f.m.Cost = spend(60)
	if r := mustStart(t, f.m, f.request(t, "app", false)); r.Outcome != OutcomeWaiting || r.Reason != ReasonCostPerDay {
		t.Fatalf("over the day's cost: %+v", r)
	}
	f.m.Cost = spend(10)
	if res, _ := f.m.Retry(context.Background()); res["app"].Outcome != OutcomeStarted {
		t.Fatalf("under the day's cost: %+v", res)
	}

	// a squad grant (no fleet grant) never starts a squad
	squad := grants.Resolved{Grants: grants.Defaults()}
	g := newMayor(t, squad)
	if r := mustStart(t, g.m, g.request(t, "app", false)); r.Outcome != OutcomeRefused || r.Reason != ReasonNoFleetGrant {
		t.Fatalf("squad grant: %+v", r)
	}
	// manual mode: the operator session proposes, the owner starts
	manual := fleetGrant(4, "owner/app")
	manual.Mode = grants.ModeManual
	h := newMayor(t, manual)
	if r := mustStart(t, h.m, h.request(t, "app", false)); r.Outcome != OutcomeProposed || r.Reason != ReasonManualMode || r.Proposal == nil {
		t.Fatalf("manual mode: %+v", r)
	}
	if len(h.writer.proposed)+len(h.launcher.launched)+len(h.registered(t)) != 0 {
		t.Fatal("manual mode wrote")
	}
}

// Acceptance 6 (R40, upstream ask 2): a new root that needs provisioning (no
// .amqrc, so the launch API refuses an unknown session under it) gives a
// proposal for the owner, not a launch.
func TestMayorAsk2FallbackProposes(t *testing.T) {
	f := newMayor(t, fleetGrant(2, "owner/fresh"))
	req := f.request(t, "fresh", true)
	r := mustStart(t, f.m, req)
	if r.Outcome != OutcomeProposed || r.Reason != ReasonProvisioning || r.Proposal == nil {
		t.Fatalf("unprovisioned root: %+v", r)
	}
	if len(f.writer.proposed) != 0 || len(f.launcher.launched) != 0 || len(f.registered(t)) != 0 || len(f.m.Waiting) != 0 {
		t.Fatal("the ask 2 fallback wrote, launched, registered or queued")
	}
	p := r.Proposal
	if p.Squad != req.Squad || p.SquadYAML != req.SquadYAML || !strings.Contains(p.Detail, ".amqrc") || len(p.Steps) == 0 {
		t.Fatalf("proposal = %+v", p)
	}
	for _, step := range p.Steps {
		if len(step) == 0 || strings.ContainsAny(step[0], " ;|&$") {
			t.Fatalf("proposal step is not argv: %q", step)
		}
	}
	// once the owner provisions the root, the same start launches
	if err := os.WriteFile(filepath.Join(req.Squad.ProjectPath, ".amqrc"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if r := mustStart(t, f.m, req); r.Outcome != OutcomeStarted {
		t.Fatalf("after provisioning: %+v", r)
	}
	if need, why := AMQProvisioned(filepath.Join(f.dir, "absent")); !need || !strings.Contains(why, "does not exist") {
		t.Fatalf("absent root: %v %q", need, why)
	}
}
