package activity

import (
	"os"
	"testing"
	"time"
)

func TestWriteReadRoundTripFresh(t *testing.T) {
	dir := t.TempDir()
	writtenAt := time.Date(2026, 7, 6, 9, 0, 0, 0, time.UTC)

	if err := Write(dir, File{
		Handle:    " qa ",
		TaskID:    " t11 ",
		Phase:     " testing ",
		Detail:    " make ci ",
		WrittenAt: writtenAt,
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	snap, ok, err := Read(dir, writtenAt.Add(30*time.Second), DefaultStaleAfter)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !ok {
		t.Fatal("Read ok = false, want true")
	}
	if snap.Source != SourceHeartbeat || snap.Quality != StateFresh || snap.Stale {
		t.Fatalf("fresh snapshot = %+v", snap)
	}
	if snap.Handle != "qa" || snap.TaskID != "t11" || snap.Phase != "testing" || snap.Detail != "make ci" {
		t.Fatalf("snapshot fields not normalized: %+v", snap)
	}
	info, err := os.Stat(Path(dir))
	if err != nil {
		t.Fatalf("stat activity file: %v", err)
	}
	if got := info.Mode().Perm(); got != FilePerm {
		t.Fatalf("activity file mode = %#o, want %#o", got, FilePerm)
	}
}

func TestReadMarksStale(t *testing.T) {
	dir := t.TempDir()
	writtenAt := time.Date(2026, 7, 6, 9, 0, 0, 0, time.UTC)
	if err := Write(dir, File{Handle: "qa", WrittenAt: writtenAt}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	snap, ok, err := Read(dir, writtenAt.Add(DefaultStaleAfter+time.Second), DefaultStaleAfter)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !ok {
		t.Fatal("Read ok = false, want true")
	}
	if snap.Source != SourceHeartbeat || snap.Quality != StateStale || !snap.Stale {
		t.Fatalf("stale snapshot = %+v", snap)
	}
}

func TestTaskStoreSnapshotNeverClaimsFreshProgress(t *testing.T) {
	updatedAt := time.Date(2026, 7, 6, 9, 0, 0, 0, time.UTC)
	snap := TaskStoreSnapshot("qa", "t11", "review fix", updatedAt, updatedAt.Add(5*time.Second), DefaultStaleAfter)
	if snap.Source != SourceTaskStore || snap.Quality != StateUnknown || snap.Stale {
		t.Fatalf("task-store snapshot should be separated from heartbeat freshness: %+v", snap)
	}
	if snap.Phase != "task_in_progress" || snap.TaskID != "t11" || snap.Detail != "review fix" {
		t.Fatalf("task-store fields = %+v", snap)
	}
}
