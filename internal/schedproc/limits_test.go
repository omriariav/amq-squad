package schedproc

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/claudeseat"
	"github.com/omriariav/amq-squad/v3/internal/usagelimit"
)

// A Claude seat's session files live in its own config dir: the usage-limit
// source reads them there, beside the owner's home.
func TestLimitsReadTheSeatConfigDirs(t *testing.T) {
	root := t.TempDir()
	dir := claudeseat.Dir(root, "c1", "launch-1")
	proj := filepath.Join(dir, "projects", "-wt-c1")
	if err := os.MkdirAll(proj, 0o700); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"assistant","timestamp":"2026-10-24T13:00:00.000Z","isApiErrorMessage":true,"error":"rate_limit","apiErrorStatus":429,"sessionId":"s1","cwd":"/wt/c1","message":{"content":[{"type":"text","text":"You've hit your session limit · resets 5:00pm (Asia/Jerusalem)"}]}}` + "\n"
	if err := os.WriteFile(filepath.Join(proj, "s1.jsonl"), []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	l := Limits{Home: usagelimit.ForHome(t.TempDir(), "", ""), Root: root}
	evs, err := l.UsageLimitEvents(time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC))
	if err != nil || len(evs) != 1 || evs[0].Provider != "claude" {
		t.Fatalf("events: %+v %v", evs, err)
	}
	if evs, _ := l.UsageLimitEvents(time.Date(2026, 10, 24, 14, 0, 0, 0, time.UTC)); len(evs) != 0 {
		t.Fatalf("an event before since: %+v", evs)
	}
}
