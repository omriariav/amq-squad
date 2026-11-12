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

// Brief 320-train-escape-legacy, L3: a result's notes (the one line of each
// legacy gate record the drift reader skipped) print one a line, right after
// the protection line; a result with none prints exactly what it printed
// before. Reverted (the lines not printed), they are missing.
func TestMergeTrainPrintsEachNoteOnItsOwnLineAfterTheProtectionLine(t *testing.T) {
	const first = "gate clear-0a227ca8d88dc97c65c540a4d0fa18a7 is a legacy quarantine_clear gate (a kind this build no longer carries); skipped"
	const second = "gate clear-87f1c6a4ea0b3ebb5c2aa9a48b0c1f8e is a legacy quarantine_clear gate (a kind this build no longer carries); skipped"
	r := mergetrain.Result{Run: "run", Repository: "o/r", Base: "main", BaseSHA: strings.Repeat("b", 40), DryRun: true, ProtectionMet: true,
		PRs: []mergetrain.PlanPR{{Number: 1, Head: strings.Repeat("a", 40), Status: "planned"}}}
	plain := FormatMergeTrain(r)
	r.Notes = []string{first, second}
	noted := FormatMergeTrain(r)
	lines := strings.Split(noted, "\n")
	if len(lines) < 4 || !strings.HasPrefix(lines[1], "branch protection met: true") || lines[2] != first || lines[3] != second {
		t.Fatalf("the notes, one a line, after the protection line:\n%s", noted)
	}
	if strings.Replace(noted, first+"\n"+second+"\n", "", 1) != plain || strings.Contains(plain, "legacy") {
		t.Fatalf("a result with no notes prints something else than before:\n%s\nwith notes:\n%s", plain, noted)
	}
}
