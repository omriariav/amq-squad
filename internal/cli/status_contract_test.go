package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/status"
	taskstore "github.com/omriariav/amq-squad/v3/internal/task"
)

func TestStatusJSONIsTheContractDocument(t *testing.T) {
	golden, err := os.ReadFile(filepath.Join("..", "status", "testdata", "status-v1.golden.json"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	chdir(t, root)
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	prevNow, prevAlive := statusNow, statusPIDAlive
	statusNow, statusPIDAlive = func() time.Time { return now }, func(int) bool { return true }
	t.Cleanup(func() { statusNow, statusPIDAlive = prevNow, prevAlive })
	store, _ := taskstore.Open(root)
	if _, err := store.Add(taskstore.AddInput{Title: "Fix the parser", Class: "implement"}, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Claim("t1", "impl-1", nil, 10*time.Minute, now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "agents", "impl-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AM_ROOT", root)
	stdout, _, err := captureOutput(t, func() error { return runStatusWithVersion([]string{"--json"}, "3.0.0-test") })
	if err != nil {
		t.Fatal(err)
	}
	if err := status.CheckAdditive(golden, []byte(stdout)); err != nil {
		t.Fatalf("status --json against the golden: %v", err)
	}
	var doc status.Status
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.SchemaVersion != 1 || doc.ProducerVersion != "3.0.0-test" || doc.Root != root || len(doc.Tasks) != 1 || doc.Tasks[0].Bucket != status.BucketWorking {
		t.Fatalf("status doc = %+v", doc)
	}
	text, _, err := captureOutput(t, func() error { return runStatusWithVersion(nil, "3.0.0-test") })
	if err != nil || !strings.Contains(text, "t1") || !strings.Contains(text, "working") || !strings.Contains(text, "1 working") {
		t.Fatalf("human status (%v):\n%s", err, text)
	}
}

func TestStatusNeedsARoot(t *testing.T) {
	chdir(t, t.TempDir())
	t.Setenv("AM_ROOT", "")
	if _, _, err := captureOutput(t, func() error { return runStatusWithVersion(nil, "x") }); err == nil || !strings.Contains(err.Error(), "no store root") {
		t.Fatalf("err = %v", err)
	}
}
