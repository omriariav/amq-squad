package launch

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

func TestWriteReadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	in := Record{
		CWD:       "/some/project",
		Binary:    "claude",
		Argv:      []string{"--flag", "value"},
		Session:   "stream1",
		Handle:    "cpo",
		Role:      "cpo",
		Root:      dir,
		StartedAt: time.Now().UTC().Truncate(time.Second),
	}
	if err := Write(dir, in); err != nil {
		t.Fatalf("Write: %v", err)
	}

	out, err := Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	if out.Schema != SchemaVersion {
		t.Errorf("Schema = %d, want %d", out.Schema, SchemaVersion)
	}
	// Write sets Schema, so zero out in.Schema for comparison of other fields.
	in.Schema = SchemaVersion
	if out.CWD != in.CWD || out.Binary != in.Binary || out.Session != in.Session ||
		out.Handle != in.Handle || out.Role != in.Role || out.Root != in.Root {
		t.Errorf("round-trip mismatch: got %+v, want %+v", out, in)
	}
	if len(out.Argv) != len(in.Argv) {
		t.Fatalf("Argv len mismatch: %v vs %v", out.Argv, in.Argv)
	}
	for i := range in.Argv {
		if out.Argv[i] != in.Argv[i] {
			t.Errorf("Argv[%d] = %q, want %q", i, out.Argv[i], in.Argv[i])
		}
	}
	if !out.StartedAt.Equal(in.StartedAt) {
		t.Errorf("StartedAt = %v, want %v", out.StartedAt, in.StartedAt)
	}
}

func TestReadMissing(t *testing.T) {
	dir := t.TempDir()
	_, err := Read(dir)
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("err = %v, want ErrNotExist", err)
	}
}

func TestScanFindsRecordsAcrossSessions(t *testing.T) {
	project := t.TempDir()
	// Simulate two sessions, one agent each.
	makeAgent := func(session, handle, role string) {
		dir := filepath.Join(project, ".agent-mail", session, "agents", handle)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		rec := Record{
			CWD:       project,
			Binary:    "claude",
			Session:   session,
			Handle:    handle,
			Role:      role,
			Root:      filepath.Join(project, ".agent-mail", session),
			StartedAt: time.Now().UTC(),
		}
		if err := Write(dir, rec); err != nil {
			t.Fatal(err)
		}
	}
	makeAgent("stream1", "cpo", "cpo")
	makeAgent("stream2", "fullstack", "fullstack")

	// Add a noise file that shouldn't be picked up.
	if err := os.WriteFile(filepath.Join(project, ".agent-mail", "not-a-launch.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	recs, err := Scan(project)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("Scan returned %d records, want 2", len(recs))
	}

	roles := []string{recs[0].Role, recs[1].Role}
	sort.Strings(roles)
	if roles[0] != "cpo" || roles[1] != "fullstack" {
		t.Errorf("roles = %v, want [cpo fullstack]", roles)
	}
}

func TestScanEmptyProject(t *testing.T) {
	recs, err := Scan(t.TempDir())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(recs) != 0 {
		t.Errorf("Scan on empty project returned %d records", len(recs))
	}
}
