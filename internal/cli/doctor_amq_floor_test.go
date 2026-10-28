//go:build unix

package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubAMQBinaries puts amq, amq-remote and amq-owner-sign stubs answering
// --version with the given outputs first (and alone) on PATH.
func stubAMQBinaries(t *testing.T, versions map[string]string) {
	t.Helper()
	dir := t.TempDir()
	for name, out := range versions {
		script := "#!/bin/sh\n[ \"$1\" = --version ] && { echo '" + out + "'; exit 0; }\nexit 2\n"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
}

func floorRows(t *testing.T) map[string]doctorCheck {
	t.Helper()
	rows := map[string]doctorCheck{}
	for _, c := range doctorCheckInstalledAMQFloor(doctorExecution{ToolVersion: defaultToolVersion}) {
		rows[strings.TrimSuffix(c.Name, " installed floor")] = c
	}
	return rows
}

// Brief 5-amq-A acceptance 5: doctor refuses an installed AMQ below
// v0.116.2 (a stub amq on PATH answering an older version), with the install
// line, and passes at v0.116.2; amq-remote and amq-owner-sign are held to
// the same floor (their --version output shapes, "amq-remote 0.107.0" and a
// bare "0.107.0", as the real binaries print them).
func TestDoctorInstalledAMQFloor(t *testing.T) {
	stubAMQBinaries(t, map[string]string{"amq": "0.116.1", "amq-remote": "amq-remote 0.107.0", "amq-owner-sign": "0.116.2"})
	rows := floorRows(t)
	for _, name := range []string{"amq", "amq-remote"} {
		c := rows[name]
		if c.Status != doctorFail || !strings.Contains(c.Detail, "below the installed floor 0.116.2") || !strings.Contains(c.Detail, "amq upgrade --all -y") {
			t.Errorf("%s below the floor: %+v", name, c)
		}
	}
	if c := rows["amq-owner-sign"]; c.Status != doctorOK {
		t.Errorf("amq-owner-sign at the floor: %+v", c)
	}

	stubAMQBinaries(t, map[string]string{"amq": "0.116.2", "amq-remote": "amq-remote 0.116.2", "amq-owner-sign": "v0.117.0"})
	for name, c := range floorRows(t) {
		if c.Status != doctorOK {
			t.Errorf("%s at or above the floor: %+v", name, c)
		}
	}

	// A prerelease of the floor is below it; garbage is a refusal; a missing
	// amq is a refusal, a missing amq-remote a warning.
	stubAMQBinaries(t, map[string]string{"amq": "0.116.2-rc1", "amq-owner-sign": "no version here"})
	rows = floorRows(t)
	if rows["amq"].Status != doctorFail || rows["amq-owner-sign"].Status != doctorFail || rows["amq-remote"].Status != doctorWarn {
		t.Errorf("prerelease, garbage, missing: %+v", rows)
	}
	stubAMQBinaries(t, map[string]string{})
	if c := floorRows(t)["amq"]; c.Status != doctorFail || !strings.Contains(c.Detail, "not on PATH") {
		t.Errorf("no amq: %+v", c)
	}
	// The rows are part of the doctor run, and a nil seam skips them.
	if got := doctorCheckInstalledAMQFloor(doctorExecution{}); got != nil {
		t.Errorf("nil seam: %+v", got)
	}
}
