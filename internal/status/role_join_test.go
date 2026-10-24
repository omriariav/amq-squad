package status

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/launch"
)

// The seat row's role joins from the launch record (2b-2 milestone B): the
// staged record before seat-exec publishes, the published one after; the
// runtime and effort come from the 3.0 fields.
func TestSeatRoleJoinsFromTheLaunchRecord(t *testing.T) {
	root := t.TempDir()
	for _, h := range []string{"lead", "impl-1"} {
		if err := os.MkdirAll(filepath.Join(root, "agents", h), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	stage := func(rec launch.Record) {
		dir := launch.StagedDir(root, rec.Handle)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := launch.WriteFile(filepath.Join(dir, launch.FileName), rec); err != nil {
			t.Fatal(err)
		}
	}
	stage(launch.Record{Handle: "lead", Role: "lead", Runtime: "pi", Model: "qwen3.6:27b", Effort: "medium", CWD: "/repo", Binary: "pi"})
	impl := launch.Record{Handle: "impl-1", Role: "implementer", Runtime: "claude", Model: "sonnet", Effort: "high", CWD: "/wt/impl-1", Binary: "claude"}
	stage(impl)
	if err := launch.WriteUnderRecordLock(filepath.Join(root, "agents", "impl-1"), impl); err != nil {
		t.Fatal(err)
	}
	st := Build(Collect(CollectOptions{Root: root, Now: now}))
	got := map[string]Seat{}
	for _, s := range st.Seats {
		got[s.Handle] = s
	}
	if s := got["lead"]; s.Role != "lead" || s.Runtime != "pi" || s.Effort != "medium" || s.Model != "qwen3.6:27b" {
		t.Fatalf("lead row from the staged record: %+v", s)
	}
	if s := got["impl-1"]; s.Role != "implementer" || s.Runtime != "claude" || s.Effort != "high" {
		t.Fatalf("impl-1 row from the published record: %+v", s)
	}
	if got["lead"].State != StateUnknown {
		t.Fatalf("a staged record alone is not a liveness signal: %+v", got["lead"])
	}
}
