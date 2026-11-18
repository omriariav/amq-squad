package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/launch"
)

// stageForAgentDir writes rec as the record the core stages for the seat
// whose agent directory is agentDir (<root>/agents/<handle>): since C5a the
// deciders read the staged record, never the one in the seat's own agent
// directory. A record without a handle gets the directory's.
func stageForAgentDir(t *testing.T, agentDir string, rec launch.Record) {
	t.Helper()
	root, handle := filepath.Dir(filepath.Dir(agentDir)), filepath.Base(agentDir)
	if rec.Handle == "" {
		rec.Handle = handle
	}
	if err := os.MkdirAll(launch.StagedDir(root, handle), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := launch.WriteFile(filepath.Join(launch.StagedDir(root, handle), launch.FileName), rec); err != nil {
		t.Fatal(err)
	}
}
