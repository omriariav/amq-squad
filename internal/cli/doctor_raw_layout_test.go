package cli

import (
	"strings"
	"testing"
)

func TestDoctorNamesRawLedgerCompatibilityActions(t *testing.T) {
	u := rawLayoutUpRun(t)
	d := doctorExecution{
		ProjectDir: t.TempDir(),
		ResolveAMQEnv: func(string) (amqEnv, error) {
			return amqEnv{Root: u.q.SessionRoot()}, nil
		},
	}
	got := doctorCheckLedgerCompatibility(d)
	if got.Status != doctorOK {
		t.Fatalf("status = %s, detail = %s", got.Status, got.Detail)
	}
	for _, want := range []string{"v3.2.0-rc.2", "newer_writer", "owner", "up --binary", "schema_mismatch", "lead"} {
		if !strings.Contains(got.Detail, want) {
			t.Errorf("detail %q does not contain %q", got.Detail, want)
		}
	}
}
