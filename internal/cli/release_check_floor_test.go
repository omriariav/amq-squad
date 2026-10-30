package cli

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestReleaseCheckReadsTheDoctorFloor (brief 5-docs-F item 3): the AMQ floor
// scripts/check-release-version.py requires in the release-facing docs is the
// floor doctor enforces. The script reads it from doctor.go and keeps no copy
// of its own; this row runs the script and compares with the constant.
func TestReleaseCheckReadsTheDoctorFloor(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not on PATH (make release-validator-test needs it too)")
	}
	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(python, "-I", filepath.Join(repo, "scripts", "check-release-version.py"), "--print-amq-floor")
	cmd.Dir = repo
	cmd.Env = []string{"PATH=/usr/bin:/bin", "PYTHONDONTWRITEBYTECODE=1"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("check-release-version.py --print-amq-floor: %v: %s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != doctorMinAMQVersion {
		t.Fatalf("the release check reads AMQ floor %q, doctorMinAMQVersion is %q", got, doctorMinAMQVersion)
	}
	if doctorInstalledAMQFloor != doctorMinAMQVersion {
		t.Fatalf("doctorInstalledAMQFloor %q != doctorMinAMQVersion %q", doctorInstalledAMQFloor, doctorMinAMQVersion)
	}
}
