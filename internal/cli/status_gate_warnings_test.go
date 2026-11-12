package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/gatestore"
)

func writeStatusLegacyGate(t *testing.T, root, id string) {
	t.Helper()
	dir := filepath.Join(root, "extensions", "amq-squad", "gates")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := strings.ReplaceAll(cliRollbackLegacyGate, cliRollbackLegacyGateID, id)
	if err := os.WriteFile(filepath.Join(dir, id+".json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestStatusGateWarningsLegacy(t *testing.T) {
	f := newGateCLIFixture(t)
	writeStatusLegacyGate(t, f.root, cliRollbackLegacyGateID)
	got := buildStatusContract(f.root, f.project, "test", time.Unix(1, 0)).Warnings
	want := gateStoreLegacyWarningPrefix + gatestore.LegacyLine(cliRollbackLegacyGateID, "quarantine_clear")
	if !slices.Equal(got, []string{want}) || strings.HasPrefix(got[0], gateStoreReadWarningPrefix) {
		t.Fatalf("legacy warnings = %q, want [%q]", got, want)
	}
}

func TestStatusGateWarningsLegacyThreeInIDOrder(t *testing.T) {
	f := newGateCLIFixture(t)
	ids := []string{
		"clear-00000000000000000000000000000001",
		"clear-00000000000000000000000000000002",
		"clear-00000000000000000000000000000003",
	}
	for _, id := range []string{ids[2], ids[0], ids[1]} {
		writeStatusLegacyGate(t, f.root, id)
	}
	got := buildStatusContract(f.root, f.project, "test", time.Unix(1, 0)).Warnings
	want := make([]string, 0, len(ids))
	for _, id := range ids {
		want = append(want, gateStoreLegacyWarningPrefix+gatestore.LegacyLine(id, "quarantine_clear"))
	}
	if !slices.Equal(got, want) {
		t.Fatalf("legacy warnings = %q, want %q", got, want)
	}
}

func TestStatusGateWarningsReadFailure(t *testing.T) {
	f := newGateCLIFixture(t)
	path := filepath.Join(f.root, "extensions", "amq-squad", "gates")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not a directory\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := buildStatusContract(f.root, f.project, "test", time.Unix(1, 0)).Warnings
	if len(got) != 1 || !strings.HasPrefix(got[0], gateStoreReadWarningPrefix) || strings.HasPrefix(got[0], gateStoreLegacyWarningPrefix) {
		t.Fatalf("read-failure warnings = %q", got)
	}
}
