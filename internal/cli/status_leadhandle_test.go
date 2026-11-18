package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/launch"
)

// 3.3.1 soak, bound (i): status's lead_handle names the squad's lead as
// squad.yaml names it (its lead: handle), the way v3's other readers name
// the lead (remote attach's team.ReadFor, the lead's own start), not only a
// seat whose role id is "lead": v31's lead is cto with role cto. The rows
// drive the real producer: a real squad.yaml and the real status command
// (`amq-squad status --json`, the collector), never a hand-built document.

// leadHandleSquad writes a repository with squad.yaml (session s, lead
// lead, the seats given as handle:role) and the session root's mailboxes.
func leadHandleSquad(t *testing.T, lead string, seats ...string) (project, root string) {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	project = filepath.Join(dir, "proj")
	root = filepath.Join(project, ".agent-mail", "s")
	var b strings.Builder
	b.WriteString("schema: 1\nsession: s\n")
	if lead != "" {
		b.WriteString("lead: " + lead + "\n")
	}
	b.WriteString("seats:\n")
	for _, s := range seats {
		h, role, _ := strings.Cut(s, ":")
		runtime := "codex"
		if h == lead {
			runtime = "pi"
		}
		b.WriteString("  - handle: " + h + "\n    role: " + role + "\n    runtime: " + runtime + "\n")
		if err := os.MkdirAll(filepath.Join(root, "agents", h, "inbox", "new"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite(t, filepath.Join(project, "squad.yaml"), b.String())
	return project, root
}

// statusLeadHandle is `amq-squad status --root R --project P --json`'s
// lead_handle ("" when null).
func statusLeadHandle(t *testing.T, project, root string) string {
	t.Helper()
	out, _, err := captureOutput(t, func() error {
		return runStatusWithVersion([]string{"--root", root, "--project", project, "--json"}, "test")
	})
	if err != nil {
		t.Fatalf("status: %v\n%s", err, out)
	}
	var doc struct {
		LeadHandle *string `json:"lead_handle"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if doc.LeadHandle == nil {
		return ""
	}
	return *doc.LeadHandle
}

// The failing row first: v31's shape, a lead whose role is cto, shows as
// the lead.
func TestStatusLeadHandleIsSquadYAMLsLead(t *testing.T) {
	project, root := leadHandleSquad(t, "cto", "cto:cto", "dev:senior-dev")
	if got := statusLeadHandle(t, project, root); got != "cto" {
		t.Errorf("lead_handle = %q, want cto (squad.yaml's lead, role cto)", got)
	}
}

// squad.yaml's lead wins over a seat whose role id is "lead": the document
// names the squad's lead, not any lead-roled seat.
func TestStatusLeadHandlePrefersSquadYAMLsLeadToALeadRole(t *testing.T) {
	project, root := leadHandleSquad(t, "cto", "cto:cto", "helper:lead")
	stageForAgentDir(t, filepath.Join(root, "agents", "helper"), launch.Record{Handle: "helper", Root: root, Role: "lead"})
	if got := statusLeadHandle(t, project, root); got != "cto" {
		t.Errorf("lead_handle = %q, want cto (squad.yaml's lead)", got)
	}
}

// Where squad.yaml names no lead for this session (another session's
// squad.yaml, or none), the seat whose role is lead still fills it, as
// before.
func TestStatusLeadHandleFallsBackToTheLeadRole(t *testing.T) {
	project, root := leadHandleSquad(t, "cto", "cto:cto", "helper:lead")
	stageForAgentDir(t, filepath.Join(root, "agents", "helper"), launch.Record{Handle: "helper", Root: root, Role: "lead"})
	mustWrite(t, filepath.Join(project, "squad.yaml"), strings.Replace(mustRead(t, filepath.Join(project, "squad.yaml")), "session: s\n", "session: other\n", 1))
	if got := statusLeadHandle(t, project, root); got != "helper" {
		t.Errorf("another session's squad.yaml: lead_handle = %q, want helper (the lead-roled seat)", got)
	}
	if err := os.Remove(filepath.Join(project, "squad.yaml")); err != nil {
		t.Fatal(err)
	}
	if got := statusLeadHandle(t, project, root); got != "helper" {
		t.Errorf("no squad.yaml: lead_handle = %q, want helper (the lead-roled seat)", got)
	}
}

func mustRead(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Codex pass 1: the squad.yaml read is the root's own squad's, wherever
// status runs: a different lead handle (arch) is named; status run from a
// subdirectory with --root alone names it; a repository elsewhere whose
// squad.yaml names the same session (run from there) does not; a named
// profile's root (<repo>/.agent-mail/<profile>/<session>) does not inherit
// the default squad's lead; a FIFO squad.yaml never hangs status.
func TestStatusLeadHandleIsTheRootsOwnSquads(t *testing.T) {
	project, root := leadHandleSquad(t, "arch", "arch:architect", "dev:senior-dev")
	sub := filepath.Join(project, "internal")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	chdir(t, sub)
	statusOf := func(args ...string) string {
		t.Helper()
		out, _, err := captureOutput(t, func() error { return runStatusWithVersion(append(args, "--json"), "test") })
		if err != nil {
			t.Fatalf("status %v: %v\n%s", args, err, out)
		}
		var doc struct {
			LeadHandle *string `json:"lead_handle"`
		}
		if err := json.Unmarshal([]byte(out), &doc); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
		if doc.LeadHandle == nil {
			return ""
		}
		return *doc.LeadHandle
	}
	if got := statusOf("--root", root); got != "arch" {
		t.Errorf("--root from a subdirectory: lead_handle = %q, want arch", got)
	}
	other, _ := leadHandleSquad(t, "boss", "boss:cto")
	chdir(t, other)
	if got := statusOf("--root", root); got != "arch" {
		t.Errorf("--root from another repository (same session name): lead_handle = %q, want arch (the root's own squad)", got)
	}
	if got := statusOf("--root", root, "--project", other); got != "arch" {
		t.Errorf("--root with another --project: lead_handle = %q, want arch", got)
	}
	profileRoot := filepath.Join(project, ".agent-mail", "review", "s")
	if err := os.MkdirAll(filepath.Join(profileRoot, "agents", "arch", "inbox", "new"), 0o700); err != nil {
		t.Fatal(err)
	}
	if got := statusOf("--root", profileRoot); got != "" {
		t.Errorf("a named profile's root: lead_handle = %q, want none (not the default squad's lead)", got)
	}
	if err := os.Remove(filepath.Join(project, "squad.yaml")); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(project, "squad.yaml"), 0o600); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	done := make(chan string, 1)
	go func() { done <- squadLeadFor(root) }()
	select {
	case got := <-done:
		if got != "" {
			t.Errorf("a FIFO squad.yaml: %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a FIFO squad.yaml blocked status")
	}
}

// Codex pass 2 (test strength): a squad.yaml of this session that names no
// lead leaves the lead-roled seat's fallback, and one larger than the read
// bound is not read (the fallback again).
func TestStatusLeadHandleWithoutALeadOrPastTheBound(t *testing.T) {
	project, root := leadHandleSquad(t, "", "cto:cto", "helper:lead")
	stageForAgentDir(t, filepath.Join(root, "agents", "helper"), launch.Record{Handle: "helper", Root: root, Role: "lead"})
	if got := statusLeadHandle(t, project, root); got != "helper" {
		t.Errorf("a squad.yaml with no lead: lead_handle = %q, want helper (the lead-roled seat)", got)
	}
	big := "schema: 1\nsession: s\nlead: cto\n" + strings.Repeat("# padding\n", (1<<20)/10+16)
	mustWrite(t, filepath.Join(project, "squad.yaml"), big)
	if got := squadLeadFor(root); got != "" {
		t.Errorf("a squad.yaml past the 1 MiB bound was read: %q", got)
	}
	if got := statusLeadHandle(t, project, root); got != "helper" {
		t.Errorf("a squad.yaml past the bound: lead_handle = %q, want helper", got)
	}
}
