package sealchain

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/atomicfile"
	"github.com/omriariav/amq-squad/v3/internal/flock"
)

func TestLayoutMigrationTerminatesTornDecisionTailAndDedupes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scheduler", "decisions.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"kind":"scheduler_started"`), 0o600); err != nil {
		t.Fatal(err)
	}
	appended, err := RecordLayoutMigration(path, "commits", "qa", 2, "raw layout", time.Unix(7, 0))
	if err != nil || !appended {
		t.Fatalf("append = %t, %v", appended, err)
	}
	lines := bytes.Split(bytes.TrimSuffix(mustReadMigrationFile(t, path), []byte{'\n'}), []byte{'\n'})
	if len(lines) != 2 || json.Valid(lines[0]) || !json.Valid(lines[1]) {
		t.Fatalf("torn tail and migration record = %q", lines)
	}
	var d layoutMigrationDecision
	if err := json.Unmarshal(lines[1], &d); err != nil || d.Kind != LayoutMigrationDecisionKind || d.Seq != 2 {
		t.Fatalf("migration line = %+v, %v", d, err)
	}
	appended, err = RecordLayoutMigration(path, "commits", "qa", 2, "raw layout", time.Unix(8, 0))
	if err != nil || appended {
		t.Fatalf("dedupe = %t, %v", appended, err)
	}
	if got := bytes.Count(mustReadMigrationFile(t, path), []byte(`"kind":"ledger_layout_migrated"`)); got != 1 {
		t.Fatalf("migration records = %d", got)
	}
}

func TestLayoutMigrationAndDecisionWriterShareTheJSONLFileLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scheduler", "decisions.jsonl")
	locked := make(chan struct{})
	resume := make(chan struct{})
	afterMigrationDecisionScan = func() {
		close(locked)
		<-resume
	}
	t.Cleanup(func() { afterMigrationDecisionScan = nil })
	type result struct {
		appended bool
		err      error
	}
	migrationDone := make(chan result, 1)
	go func() {
		appended, err := RecordLayoutMigration(path, "exits", "", 4, "raw layout", time.Unix(7, 0))
		migrationDone <- result{appended, err}
	}()
	<-locked

	writerStarted := make(chan struct{})
	writerDone := make(chan error, 1)
	go func() {
		close(writerStarted)
		writerDone <- flock.AppendJSONL(path, map[string]string{"kind": "scheduler_started"})
	}()
	<-writerStarted
	select {
	case err := <-writerDone:
		t.Fatalf("decision writer did not wait for migration lock: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(resume)
	if got := <-migrationDone; got.err != nil || !got.appended {
		t.Fatalf("migration = %+v", got)
	}
	if err := <-writerDone; err != nil {
		t.Fatal(err)
	}

	lines := bytes.Split(bytes.TrimSpace(mustReadMigrationFile(t, path)), []byte{'\n'})
	if len(lines) != 2 {
		t.Fatalf("decision lines = %d: %q", len(lines), lines)
	}
	migrations := 0
	for _, line := range lines {
		if !json.Valid(line) {
			t.Fatalf("glued or torn decision line: %q", line)
		}
		if bytes.Contains(line, []byte(`"kind":"ledger_layout_migrated"`)) {
			migrations++
		}
	}
	if migrations != 1 {
		t.Fatalf("migration records = %d", migrations)
	}
}

func TestLayoutMigrationDedupeHitSyncsFileAndDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scheduler", "decisions.jsonl")
	wantErr := errors.New("injected file sync failure")
	var fileSyncs atomic.Int32
	var dirSyncs atomic.Int32
	oldFile, oldDir := syncMigrationFile, syncMigrationDir
	syncMigrationFile = func(f *os.File) error {
		if fileSyncs.Add(1) == 1 {
			return wantErr
		}
		return f.Sync()
	}
	syncMigrationDir = func(dir string) error {
		dirSyncs.Add(1)
		return atomicfile.SyncDir(dir)
	}
	t.Cleanup(func() {
		syncMigrationFile, syncMigrationDir = oldFile, oldDir
	})

	appended, err := RecordLayoutMigration(path, "commits", "qa", 3, "raw layout", time.Unix(7, 0))
	if !appended || !errors.Is(err, wantErr) {
		t.Fatalf("first append = %t, %v", appended, err)
	}
	appended, err = RecordLayoutMigration(path, "commits", "qa", 3, "raw layout", time.Unix(8, 0))
	if appended || err != nil {
		t.Fatalf("durable dedupe = %t, %v", appended, err)
	}
	if fileSyncs.Load() != 2 || dirSyncs.Load() != 2 {
		t.Fatalf("sync calls after retry = file %d, dir %d", fileSyncs.Load(), dirSyncs.Load())
	}
	if got := bytes.Count(mustReadMigrationFile(t, path), []byte(`"kind":"ledger_layout_migrated"`)); got != 1 {
		t.Fatalf("migration records = %d", got)
	}
}

func mustReadMigrationFile(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
