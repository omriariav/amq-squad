package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/console"
	"github.com/omriariav/amq-squad/v2/internal/noc"
)

// captureNOC records the NOCConfig executeNOC assembled, so tests can assert the
// wiring without starting a Bubble Tea program.
type captureNOC struct {
	cfg    console.NOCConfig
	called bool
}

func (c *captureNOC) run(cfg console.NOCConfig) error {
	c.cfg = cfg
	c.called = true
	return nil
}

func TestExecuteNOC_PassesRootsAndThresholds(t *testing.T) {
	var cap captureNOC
	exec := nocExecution{
		Cwd:         "/tmp/proj",
		Roots:       []string{"/tmp/a", "/tmp/b"},
		Depth:       5,
		Once:        true,
		Out:         &bytes.Buffer{},
		StdoutIsTTY: false,
		RunNOC:      cap.run,
	}
	if err := executeNOC(exec); err != nil {
		t.Fatalf("executeNOC: %v", err)
	}
	if !cap.called {
		t.Fatal("RunNOC was not called")
	}
	if len(cap.cfg.Roots) != 2 || cap.cfg.Roots[0] != "/tmp/a" {
		t.Errorf("Roots not propagated: %v", cap.cfg.Roots)
	}
	if cap.cfg.Depth != 5 {
		t.Errorf("Depth = %d, want 5", cap.cfg.Depth)
	}
	if !cap.cfg.Once {
		t.Error("Once should propagate")
	}
}

func TestExecuteNOC_NoRootDefaultsToCwd(t *testing.T) {
	var cap captureNOC
	cwd := t.TempDir() // not an amq project (no .agent-mail)
	exec := nocExecution{
		Cwd:         cwd,
		Once:        true,
		Out:         &bytes.Buffer{},
		StdoutIsTTY: false,
		RunNOC:      cap.run,
	}
	if err := executeNOC(exec); err != nil {
		t.Fatalf("executeNOC: %v", err)
	}
	if len(cap.cfg.Roots) != 1 || cap.cfg.Roots[0] != cwd {
		t.Errorf("default root should be cwd %q, got %v", cwd, cap.cfg.Roots)
	}
}

func TestExecuteNOC_ProjectCwdDefaultsToParent(t *testing.T) {
	var cap captureNOC
	parent := t.TempDir()
	proj := filepath.Join(parent, "myproj")
	if err := mkAgentMail(proj); err != nil {
		t.Fatal(err)
	}
	exec := nocExecution{
		Cwd:         proj,
		Once:        true,
		Out:         &bytes.Buffer{},
		StdoutIsTTY: false,
		RunNOC:      cap.run,
	}
	if err := executeNOC(exec); err != nil {
		t.Fatalf("executeNOC: %v", err)
	}
	// A cwd that IS an amq project defaults its scan root to the PARENT so sibling
	// squads appear.
	if len(cap.cfg.Roots) != 1 || cap.cfg.Roots[0] != parent {
		t.Errorf("project cwd should default root to parent %q, got %v", parent, cap.cfg.Roots)
	}
}

func TestExecuteNOC_NoTTYForcesOnce(t *testing.T) {
	var cap captureNOC
	exec := nocExecution{
		Cwd:         "/tmp/proj",
		Roots:       []string{"/tmp/a"},
		Once:        false, // interactive requested
		Out:         &bytes.Buffer{},
		StdoutIsTTY: false, // but no TTY
		RunNOC:      cap.run,
	}
	if err := executeNOC(exec); err != nil {
		t.Fatalf("executeNOC: %v", err)
	}
	if !cap.cfg.Once {
		t.Error("no TTY should force Once=true so a piped invocation still works")
	}
}

func TestRunNOC_DispatchedFromCLI(t *testing.T) {
	// `amq-squad noc` is a recognized verb (not unknown-command). We drive it via
	// --once over a seeded fixture so it does not open /dev/tty.
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	if err := mkAgentMail(proj); err != nil {
		t.Fatal(err)
	}
	stdout, _, err := captureOutput(t, func() error {
		return runNOC([]string{"--once", "--root", root})
	})
	if err != nil {
		t.Fatalf("runNOC --once: %v", err)
	}
	if !containsCLI(stdout, "NOC") {
		t.Errorf("noc --once board should render the NOC header, got:\n%s", stdout)
	}
}

func TestConsoleRootRoutesToNOC(t *testing.T) {
	// `console --root DIR` reaches the same multi-root NOC surface.
	root := t.TempDir()
	proj := filepath.Join(root, "p")
	if err := mkAgentMail(proj); err != nil {
		t.Fatal(err)
	}
	stdout, _, err := captureOutput(t, func() error {
		return runConsole([]string{"--once", "--root", root})
	})
	if err != nil {
		t.Fatalf("console --root --once: %v", err)
	}
	if !containsCLI(stdout, "NOC") {
		t.Errorf("console --root should reach the NOC surface, got:\n%s", stdout)
	}
}

func mkAgentMail(projectDir string) error {
	return os.MkdirAll(filepath.Join(projectDir, noc.AgentMailDirName, "main", "agents"), 0o755)
}

func containsCLI(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
