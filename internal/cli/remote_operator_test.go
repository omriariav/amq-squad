package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/buzz"
	"github.com/omriariav/amq-squad/v3/internal/registry"
)

// Operator mode (AttachToOperator, 4-3 merged): with no --lead the Buzz
// agent is bound to the operator session's user mailbox from the fleet
// registry; presence follows the fleet document's operator row; a message
// naming a squad lead goes to that squad by AMQ cross-project routing; a
// handle that is no squad's lead is refused at attach.
func TestRemoteAttachToTheOperator(t *testing.T) {
	f := newGateCLIFixture(t)
	ep, mailer := &remoteFakeEndpoint{}, &remoteFakeMailer{}
	prevE, prevM, prevS := remoteEndpoint, remoteMailer, remoteService
	remoteEndpoint = func(string) buzz.Endpoint { return ep }
	remoteMailer = mailer
	remoteService = remoteFakeService{buzz.ServiceState{State: "current", Root: "/m", Running: true}}
	t.Cleanup(func() { remoteEndpoint, remoteMailer, remoteService = prevE, prevM, prevS })
	if !buzz.AttachToOperator {
		t.Fatal("AttachToOperator is off after 4-3 merged")
	}
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	file := filepath.Join(dir, "fleet.yaml")
	opRoot := filepath.Join(dir, "operator", ".agent-mail", "ops")
	if err := os.MkdirAll(filepath.Join(opRoot, "agents", "user"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"operator", "--fleet-file", file, "--root", opRoot, "--amq-project", "fleet", "--session", "ops"},
		{"add", "--fleet-file", file, "--name", "app", "--root", f.root, "--project-path", f.project, "--repository", "owner/app", "--lead", "cto", "--session", "s"},
	} {
		if _, _, err := captureOutput(t, func() error { return runFleet(args) }); err != nil {
			t.Fatalf("fleet %v: %v", args, err)
		}
	}
	base := []string{"--fleet-file", file, "--project", f.project, "--session", "s", "--me", "cto"}
	// a destination that is no squad's lead is refused
	if _, _, err := captureOutput(t, func() error {
		return runRemote(append([]string{"attach", "--once", "--leads", "worker"}, base...), "test")
	}); err == nil || !strings.Contains(err.Error(), "no squad's lead") {
		t.Fatalf("--leads worker: %v", err)
	}

	// the operator session runs: its row in the fleet document is live
	raw, _ := json.Marshal(map[string]any{"status": "active", "last_seen": time.Now().UTC()})
	mustWrite(t, filepath.Join(opRoot, "agents", "user", "presence.json"), string(raw))
	f.answer(t, "@cto rebase #12", "")
	stdout, _, err := captureOutput(t, func() error {
		return runRemote(append([]string{"attach", "--once", "--json"}, base...), "test")
	})
	if err != nil {
		t.Fatalf("attach: %v\n%s", err, stdout)
	}
	if !strings.Contains(stdout, `"mailbox":"user"`) || !strings.Contains(stdout, `"operator":true`) || ep.starts != 1 {
		t.Fatalf("operator attach: starts %d\n%s", ep.starts, stdout)
	}
	if len(mailer.sent) != 2 {
		t.Fatalf("sent = %+v", mailer.sent)
	}
	first, fwd := mailer.sent[0], mailer.sent[1]
	if first.Root != opRoot || first.To != "user" || first.Project != "" {
		t.Fatalf("mailbox copy = %+v", first)
	}
	if fwd.Root != opRoot || fwd.To != "cto" || fwd.Project != "app" || fwd.Session != "s" || fwd.Body != "rebase #12" || len(fwd.Refs) != 0 {
		t.Fatalf("cross-project forward = %+v", fwd)
	}

	// the operator session stopped: not present, the endpoint stops
	raw, _ = json.Marshal(map[string]any{"status": "active", "last_seen": time.Now().Add(-time.Hour).UTC()})
	mustWrite(t, filepath.Join(opRoot, "agents", "user", "presence.json"), string(raw))
	stdout, _, err = captureOutput(t, func() error {
		return runRemote(append([]string{"status", "--json"}, base...), "test")
	})
	var env struct{ Data remoteStatusData }
	if err != nil || json.Unmarshal([]byte(stdout), &env) != nil || env.Data.Presence.Present || env.Data.Presence.Handle != "user" {
		t.Fatalf("status after the operator stopped: %v\n%s", err, stdout)
	}
}

// The 2b-3 doctor row (plan 8, 4-5 follow-up): amq-remote's own login
// service state, from the same check remote status uses.
func TestDoctorLoginServiceStateRow(t *testing.T) {
	det := registry.Detection{Tools: []registry.Tool{{Name: "amq-remote", Found: true}}}
	for state, want := range map[string]string{"missing": "absent", "stale": "another amq-remote binary"} {
		h := &doctorHost{GOOS: "darwin", Home: t.TempDir(), LoginService: remoteFakeService{buzz.ServiceState{State: state, Root: "/m"}}}
		found := false
		for _, c := range h.serviceChecks(context.Background(), det) {
			if c.Name == "amq-remote login service state" {
				found = c.Status == doctorWarn && strings.Contains(c.Detail, want)
			}
		}
		if !found {
			t.Errorf("state %s: no warning row naming %q", state, want)
		}
	}
	h := &doctorHost{GOOS: "darwin", Home: t.TempDir(), LoginService: remoteFakeService{buzz.ServiceState{State: "current", Root: "/m", Running: true}}}
	for _, c := range h.serviceChecks(context.Background(), det) {
		if c.Name == "amq-remote login service state" {
			t.Errorf("a current, running service gave a row: %+v", c)
		}
	}
}
