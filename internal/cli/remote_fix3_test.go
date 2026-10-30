package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/buzz"
	"github.com/omriariav/amq-squad/v3/internal/fleet"
	"github.com/omriariav/amq-squad/v3/internal/gatestore"
	"github.com/omriariav/amq-squad/v3/internal/gateverify"
)

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}
func (l *lockedBuffer) String() string { l.mu.Lock(); defer l.mu.Unlock(); return l.b.String() }

type syncEndpoint struct {
	mu            sync.Mutex
	running       bool
	starts, stops int
}

func (e *syncEndpoint) Running() bool { e.mu.Lock(); defer e.mu.Unlock(); return e.running }
func (e *syncEndpoint) Start() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.running = true
	e.starts++
	return nil
}
func (e *syncEndpoint) Stop() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.running {
		e.stops++
	}
	e.running = false
	return nil
}
func (e *syncEndpoint) counts() (bool, int, int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.running, e.starts, e.stops
}

type syncMailer struct {
	mu   sync.Mutex
	sent []buzz.Mail
}

func (m *syncMailer) Send(_ context.Context, mail buzz.Mail) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, mail)
	return fmt.Sprintf("m%d", len(m.sent)), nil
}
func (m *syncMailer) to(handle string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.sent {
		if s.To == handle {
			return true
		}
	}
	return false
}

// operatorFixture is the gate fixture plus a fleet registry with an
// operator session (present) and squad app led by cto.
func operatorFixture(t *testing.T) (gateCLIFixture, string, string, *syncEndpoint, *syncMailer) {
	t.Helper()
	f := newGateCLIFixture(t)
	ep, mailer := &syncEndpoint{}, &syncMailer{}
	prevE, prevM, prevS, prevC := remoteEndpoint, remoteMailer, remoteService, remoteContext
	remoteEndpoint = func(string) buzz.Endpoint { return ep }
	remoteMailer = mailer
	remoteService = remoteFakeService{buzz.ServiceState{State: "current", Root: "/m", Running: true}}
	t.Cleanup(func() { remoteEndpoint, remoteMailer, remoteService, remoteContext = prevE, prevM, prevS, prevC })
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	file := filepath.Join(dir, "fleet.yaml")
	opRoot := filepath.Join(dir, "operator", ".agent-mail", "ops")
	for _, args := range [][]string{
		{"operator", "--fleet-file", file, "--root", opRoot, "--amq-project", "fleet", "--session", "ops"},
		{"add", "--fleet-file", file, "--name", "app", "--root", f.root, "--project-path", f.project, "--repository", "owner/app", "--lead", "cto", "--session", "s"},
	} {
		if _, _, err := captureOutput(t, func() error { return runFleet(args) }); err != nil {
			t.Fatalf("fleet %v: %v", args, err)
		}
	}
	raw, _ := json.Marshal(map[string]any{"status": "active", "last_seen": time.Now().UTC()})
	mustWrite(t, filepath.Join(opRoot, "agents", "user", "presence.json"), string(raw))
	return f, file, opRoot, ep, mailer
}

// attachAsync runs remote attach until stop is called.
func attachAsync(t *testing.T, args []string) (*lockedBuffer, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	remoteContext = func() (context.Context, context.CancelFunc) { return ctx, func() {} }
	out := &lockedBuffer{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := runRemoteAttach(args, "test", out); err != nil {
			fmt.Fprintf(out, "ATTACH ERROR %v\n", err)
		}
	}()
	stopped := false
	stop := func() {
		if !stopped {
			stopped = true
			cancel()
			<-done
		}
	}
	t.Cleanup(stop)
	return out, stop
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

// Re-pass medium 4: the attachment reads the fleet registry again every
// tick: a squad added while it runs is routable at once (squad-qualified).
func TestRemoteAttachRereadsTheFleetRegistry(t *testing.T) {
	f, file, _, ep, mailer := operatorFixture(t)
	out, stop := attachAsync(t, []string{"--fleet-file", file, "--project", f.project, "--session", "s", "--me", "cto", "--interval", "50ms", "--json"})
	eventually(t, "the endpoint to start", func() bool { _, starts, _ := ep.counts(); return starts == 1 })
	lib := filepath.Join(t.TempDir(), "lib")
	libRoot := filepath.Join(lib, ".agent-mail", "w")
	launch := fmt.Sprintf(`{"schema":1,"handle":"dev","role":"lead","binary":"codex","cwd":%q,"agent_pid":0,"started_at":"2026-10-24T11:00:00Z","root":%q}`, lib, libRoot)
	mustWrite(t, filepath.Join(libRoot, "agents", "dev", "extensions", "io.github.omriariav.amq-squad", "launch.json"), launch)
	if _, _, err := captureOutput(t, func() error {
		return runFleet([]string{"add", "--fleet-file", file, "--name", "lib", "--root", libRoot, "--project-path", lib, "--repository", "owner/lib", "--lead", "dev", "--session", "w"})
	}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond) // at least one tick reads the new registry
	f.answer(t, "@lib/dev pick up the release", "")
	eventually(t, "the message routed to the new squad's lead", func() bool { return mailer.to("dev") })
	stop()
	if !strings.Contains(out.String(), `"lead":"lib/dev"`) {
		t.Fatalf("attach output:\n%s", out.String())
	}
}

// Re-pass medium 6: when the status source goes away (the fleet file is
// removed or corrupt), presence expires after two missed status ticks.
func TestRemotePresenceExpiresWithoutStatus(t *testing.T) {
	f, file, _, ep, _ := operatorFixture(t)
	out, stop := attachAsync(t, []string{"--fleet-file", file, "--project", f.project, "--session", "s", "--me", "cto", "--interval", "50ms", "--json"})
	eventually(t, "the endpoint to start", func() bool { _, starts, _ := ep.counts(); return starts == 1 })
	if err := os.WriteFile(file, []byte("schema: [not yaml\n"), 0o600); err != nil { // a corrupt registry
		t.Fatal(err)
	}
	eventually(t, "presence to expire", func() bool { running, _, stops := ep.counts(); return !running && stops == 1 })
	stop()
	if !strings.Contains(out.String(), `"reason":"status unavailable"`) {
		t.Fatalf("attach output:\n%s", out.String())
	}
}

// Re-pass medium 2: a squad's gate core is built on its registered root, or
// not at all: a squad whose context resolves elsewhere is reported.
func TestSquadCoresUseTheRegisteredRoot(t *testing.T) {
	f := newGateCLIFixture(t)
	primary := &gateverify.Core{Store: gatestore.Store{Root: "/nowhere"}}
	good := fleet.Squad{Name: "app", Root: f.root, ProjectPath: f.project, Lead: "cto", Session: "s"}
	cores, problems := squadCores(context.Background(), &fleet.Registry{Squads: []fleet.Squad{good}}, primary)
	if len(cores) != 1 || len(problems) != 0 || cores[0].Store.Root != f.root {
		t.Fatalf("registered root: %d cores, %v", len(cores), problems)
	}
	stray := good
	stray.Root = filepath.Join(f.base, "elsewhere")
	cores, problems = squadCores(context.Background(), &fleet.Registry{Squads: []fleet.Squad{stray}}, primary)
	if len(cores) != 0 || len(problems) != 1 || !strings.Contains(problems[0], "not the registered root") {
		t.Fatalf("a squad resolving away from its registered root: %d cores, %v", len(cores), problems)
	}
}
