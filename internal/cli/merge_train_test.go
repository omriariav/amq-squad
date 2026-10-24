package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/mergetrain"
)

// P1-4: a real run takes each PR's declared files only from the task store;
// a request that carries declared_files, or names no task, is refused before
// anything is read or resolved. The dry run still accepts them (the S6 run).
func TestMergeTrainRealRunTakesDeclaredFilesOnlyFromTheTaskStore(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	withFiles := write("files.json", `{"schema":1,"repository":"o/r","base":"main","prs":[{"number":1,"task":"t-1","declared_files":["a.go"]}]}`)
	noTask := write("notask.json", `{"schema":1,"repository":"o/r","base":"main","prs":[{"number":1}]}`)
	for path, want := range map[string]string{withFiles: "declared_files", noTask: "names no task"} {
		if err := runMergeTrain([]string{"--request", path}); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", filepath.Base(path), err)
		}
	}
	if err := realRunScope(mustReadRequest(t, write("store.json", `{"schema":1,"repository":"o/r","base":"main","prs":[{"number":1,"task":"t-1"}]}`))); err != nil {
		t.Fatalf("a task-store request was refused: %v", err)
	}
}

func mustReadRequest(t *testing.T, path string) mergetrain.Request {
	t.Helper()
	req, err := readMergeTrainRequest(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	return req
}
