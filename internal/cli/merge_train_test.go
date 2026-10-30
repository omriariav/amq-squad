package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/mergetrain"
)

// P1-4 (fix 2, lead ruling): a real run's request names PR numbers only; a
// request that carries declared_files or a task id is refused before
// anything is read or resolved. The task comes from the harness binding
// (mergetrain.BindTasks). The dry run still accepts both (the S6 run).
func TestMergeTrainRealRunNamesPRNumbersOnly(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	withFiles := write("files.json", `{"schema":1,"repository":"o/r","base":"main","prs":[{"number":1,"declared_files":["a.go"]}]}`)
	withTask := write("task.json", `{"schema":1,"repository":"o/r","base":"main","prs":[{"number":1,"task":"t-1"}]}`)
	for path, want := range map[string]string{withFiles: "declared_files", withTask: "names task t-1"} {
		if err := runMergeTrain([]string{"--request", path}); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", filepath.Base(path), err)
		}
	}
	if err := realRunScope(mustReadRequest(t, write("numbers.json", `{"schema":1,"repository":"o/r","base":"main","prs":[{"number":1}]}`))); err != nil {
		t.Fatalf("a PR-number request was refused: %v", err)
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
