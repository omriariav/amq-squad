package status

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/claudeseat"
	"github.com/omriariav/amq-squad/v3/internal/launch"
)

// Brief 31-1, rows 1 and 4 (status's half): what the deleted launch
// refusals stopped is a seat warning. A Claude seat that ran (an accepted
// proof of its launch) with no hook report since carries
// claude_hooks_silent; its worktree's Claude allow rules carry
// claude_allow_rules_present; a CLAUDE.md that imports a file outside the
// worktree carries claude_external_includes_pending. A seat with none of
// them carries no warnings key at all.
func TestClaudeSeatWarningsReplaceTheDeletedRefusals(t *testing.T) {
	now := time.Date(2026, 10, 31, 20, 0, 0, 0, time.UTC)
	base, _ := filepath.EvalSymlinks(t.TempDir())
	root := filepath.Join(base, "root")
	seat := func(handle string, setup func(wt string)) {
		wt := filepath.Join(base, "wt-"+handle)
		for _, d := range []string{filepath.Join(root, "agents", handle), launch.StagedDir(root, handle), wt} {
			if err := os.MkdirAll(d, 0o700); err != nil {
				t.Fatal(err)
			}
		}
		rec := launch.Record{Handle: handle, Root: root, Session: "s", Runtime: "claude", Worktree: wt, LaunchID: "l-" + handle}
		if err := launch.WriteFile(filepath.Join(launch.StagedDir(root, handle), launch.FileName), rec); err != nil {
			t.Fatal(err)
		}
		if err := launch.WriteProof(root, launch.ProofEntry{Handle: handle, LaunchID: "l-" + handle, PID: 4242, Start: "s", At: now.Add(-time.Hour)}); err != nil {
			t.Fatal(err)
		}
		setup(wt)
	}
	write := func(p, body string) {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	seat("silent", func(string) {})
	seat("quiet", func(string) {
		_ = claudeseat.RecordReport(root, "quiet", claudeseat.Report{At: now.Add(-time.Minute)})
	})
	seat("rules", func(wt string) {
		_ = claudeseat.RecordReport(root, "rules", claudeseat.Report{At: now.Add(-time.Minute)})
		write(filepath.Join(wt, ".claude", "settings.local.json"), `{"permissions":{"allow":["Bash(*)"]}}`)
	})
	seat("include", func(wt string) {
		_ = claudeseat.RecordReport(root, "include", claudeseat.Report{At: now.Add(-time.Minute)})
		write(filepath.Join(base, "shared.md"), "shared\n")
		write(filepath.Join(wt, "CLAUDE.md"), "@"+filepath.Join(base, "shared.md")+"\n")
	})
	st := Build(Collect(CollectOptions{Root: root, Now: now, PIDAlive: func(int) bool { return false }}))
	want := map[string]string{"silent": claudeseat.HooksSilent, "rules": claudeseat.AllowRulesPresent, "include": claudeseat.ExternalIncludesPending, "quiet": ""}
	for _, s := range st.Seats {
		w, ok := want[s.Handle]
		if !ok {
			continue
		}
		switch {
		case w == "" && len(s.Warnings) != 0:
			t.Errorf("seat %s carries warnings %q", s.Handle, s.Warnings)
		case w != "" && (len(s.Warnings) != 1 || !strings.HasPrefix(s.Warnings[0], w+": ")):
			t.Errorf("seat %s warnings %q, want one %s line", s.Handle, s.Warnings, w)
		}
		delete(want, s.Handle)
	}
	if len(want) != 0 {
		t.Fatalf("seats missing from status: %v", want)
	}
	raw, _ := json.Marshal(st)
	if strings.Count(string(raw), `"warnings":[`) < 3 {
		t.Fatalf("the warnings are not in status --json:\n%s", raw)
	}
}
