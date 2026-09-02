package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/launch"
)

// gh#758/t11 slice B commit 3: these two generic test helpers lived in
// team_resume_test.go (now deleted along with the classifier it tested),
// but down_test.go, effort_test.go, global_status_test.go, and
// liveness_test.go all still use them for unrelated reasons.

// writeMemberLaunchRecord drops a v0.6 launch.json under the fake AMQ base
// for the given session/handle so a test can seed a member as already
// launched.
func writeMemberLaunchRecord(t *testing.T, base, session, handle string, rec launch.Record) {
	t.Helper()
	agentDir := filepath.Join(base, session, "agents", handle)
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	rec.Handle = handle
	rec.Session = session
	if err := launch.Write(agentDir, rec); err != nil {
		t.Fatalf("write launch record: %v", err)
	}
}

func resumeChdir(t *testing.T, dir string) {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(old); err != nil {
			t.Errorf("restore cwd: %v", err)
		}
	})
}
