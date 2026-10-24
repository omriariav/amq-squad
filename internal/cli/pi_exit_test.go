package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/piruntime"
)

// Review 2, item 2 (c): a pi exit of 70 (the preload's runtime refusal) is
// the launcher's refusal: the launcher closes the seat (it exits with 70),
// writes runtime_mismatch_in_process to the seat's ledger, and says so.
func TestPiExit70IsALauncherRefusal(t *testing.T) {
	call, _ := piFixture(t)
	chdir(t, t.TempDir())
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "agents", "pi-1"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AM_ROOT", root)
	t.Setenv("AM_ME", "pi-1")
	call.code = piruntime.ExitRuntimeMismatch
	_, stderr, err := captureOutput(t, func() error { return Run([]string{"pi", "run", "--no-package"}, "test") })
	if err != nil {
		t.Fatal(err)
	}
	if call.exit != 70 {
		t.Fatalf("the launcher exited with %d, want 70", call.exit)
	}
	if !strings.Contains(stderr, "runtime_mismatch_in_process") || !strings.Contains(stderr, "seat pi-1 closed") {
		t.Fatalf("no named refusal on stderr:\n%s", stderr)
	}
	raw, err := os.ReadFile(piruntime.LedgerPath(root, "pi-1"))
	if err != nil {
		t.Fatalf("no ledger line: %v", err)
	}
	var line struct {
		Event   string          `json:"event"`
		Outcome string          `json:"outcome"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(raw))), &line); err != nil {
		t.Fatal(err)
	}
	if line.Event != "runtime_mismatch_in_process" || line.Outcome != "refused" || !strings.Contains(string(line.Payload), `"seat_closed"`) {
		t.Fatalf("ledger line %s", raw)
	}
	// A refused runtime is not counted as a tool result for a later resume.
	if took, _ := piruntime.LedgerHasToolResult(piruntime.LedgerPath(root, "pi-1")); took {
		t.Fatal("the refusal line counts as a tool result")
	}
}

// Other exit codes pass through; 0 exits normally.
func TestPiExitCodesPassThrough(t *testing.T) {
	call, _ := piFixture(t)
	chdir(t, t.TempDir())
	call.code = 3
	if err := Run([]string{"pi", "run", "--no-package"}, "test"); err != nil || call.exit != 3 {
		t.Fatalf("exit 3: err %v, launcher exit %d", err, call.exit)
	}
	call.code, call.exit = 0, -1
	if err := Run([]string{"pi", "run", "--no-package"}, "test"); err != nil || call.exit != -1 {
		t.Fatalf("exit 0: err %v, launcher exit %d", err, call.exit)
	}
}
