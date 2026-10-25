package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/routing"
)

// Phase 5 re-pass, P2-8: a merged routing.yaml that does not load stops
// `scheduler run` at its start with routing_invalid (routing.Open), never a
// plain wrapped error; and --lead-pool takes only runtime:model:effort.
func TestRepassSchedulerRunRefusesAnInvalidTableAsRoutingInvalid(t *testing.T) {
	_, root, seatsFile := schedulerFixture(t)
	proj := filepath.Dir(filepath.Dir(root))
	mustWrite(t, filepath.Join(proj, ".amq-squad", "routing.yaml"), "schema: 1\nunknown_key: 1\n")
	_, _, err := captureOutput(t, func() error {
		return runSchedulerRun(context.Background(), []string{"--seats", seatsFile, "--lead", "lead"}, os.Stdout)
	})
	if !routing.IsInvalid(err) || !strings.Contains(err.Error(), routing.ReasonInvalid) {
		t.Fatalf("an invalid routing.yaml: %v", err)
	}
	_, _, err = captureOutput(t, func() error {
		return runSchedulerRun(context.Background(), []string{"--seats", seatsFile, "--lead", "lead", "--lead-pool", "pi"}, os.Stdout)
	})
	if err == nil || !strings.Contains(err.Error(), "--lead-pool") {
		t.Fatalf("a malformed --lead-pool: %v", err)
	}
}
