package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/piruntime"
)

func addOldRuntime(t *testing.T, base, name string, age time.Duration) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(base, name), 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(base, name, piruntime.MarkerName)
	if err := os.WriteFile(marker, []byte("x"), 0o444); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-age)
	if err := os.Chtimes(marker, at, at); err != nil {
		t.Fatal(err)
	}
}

// doctor keeps the last two runtimes and with --fix removes the rest
// (ruling 3); without --fix it writes nothing.
func TestDoctorPiRuntimeVersions(t *testing.T) {
	_, home := piFixture(t)
	chdir(t, t.TempDir())
	if got := doctorCheckPiRuntime(false); got[0].Status != doctorOK || !strings.Contains(got[0].Detail, "not unpacked") {
		t.Fatalf("before unpack: %+v", got)
	}
	if err := Run([]string{"pi", "run", "--no-package"}, "test"); err != nil {
		t.Fatal(err)
	}
	if got := doctorCheckPiRuntime(false); got[0].Status != doctorOK || !strings.Contains(got[0].Detail, "re-hashed") {
		t.Fatalf("verified: %+v", got)
	}
	base := filepath.Join(home, ".amq-squad", "runtime")
	addOldRuntime(t, base, "pi-old-1", 3*time.Hour)
	addOldRuntime(t, base, "pi-old-2", 2*time.Hour)
	rows := doctorCheckPiRuntime(false)
	if rows[1].Status != doctorWarn || !strings.Contains(rows[1].Detail, "pi-old-1") || strings.Contains(rows[1].Detail, "pi-old-2") {
		t.Fatalf("old versions: %+v", rows)
	}
	if _, err := os.Stat(filepath.Join(base, "pi-old-1")); err != nil {
		t.Fatal("doctor without --fix removed a runtime")
	}
	rows = doctorCheckPiRuntime(true)
	if rows[1].Status != doctorOK || rows[1].Detail != "removed pi-old-1" {
		t.Fatalf("--fix: %+v", rows)
	}
	left, _ := piruntime.List(base, "")
	if len(left) != 2 {
		t.Fatalf("%d runtimes kept, want 2", len(left))
	}
}

// doctor --fix removes a rejected current runtime; the next launch unpacks
// it again.
func TestDoctorPiRuntimeRepair(t *testing.T) {
	call, _ := piFixture(t)
	chdir(t, t.TempDir())
	if err := Run([]string{"pi", "run", "--no-package"}, "test"); err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(call.path, 0o755)
	if err := os.WriteFile(call.path, []byte("tampered"), 0o555); err != nil {
		t.Fatal(err)
	}
	if rows := doctorCheckPiRuntime(false); rows[0].Status != doctorFail || rows[0].Kind != "pi_runtime_rejected" {
		t.Fatalf("tamper not failed: %+v", rows)
	}
	if err := Run([]string{"pi", "run", "--no-package"}, "test"); err == nil {
		t.Fatal("tampered runtime launched")
	}
	if rows := doctorCheckPiRuntime(true); rows[0].Kind != "pi_runtime_repaired" {
		t.Fatalf("--fix: %+v", rows)
	}
	if err := Run([]string{"pi", "run", "--no-package"}, "test"); err != nil {
		t.Fatalf("relaunch after repair: %v", err)
	}
}
