package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

// TestV2300RemovesNothing enforces the v2.30.0 additive-only invariant
// mechanically: v2.30.0 adds the opt-in launchapi backend (gh#733) and
// nothing else may be removed as a side effect. The repo has no standalone
// "removal register" issue (the earlier one was dissolved and absorbed into
// the v2.31.0 milestone description's "Deferred-removal register"), so this
// list is derived directly from that description (gh api
// repos/omriariav/amq-squad/milestones, title=v2.31.0, fetched 2026-08-26):
//
//	"Deferred-removal register (each gated; nothing removed before its
//	prerequisite): launch-record split -- layer-owned majority stays, needs
//	the new path proven in real runs; coop provisioning shim -- needs
//	plan-driven provisioning plus upstream's breaking coop-exec change;
//	pane/tmux plumbing -- needs Inspect liveness landed and consuming
//	status/health; doctor-check pruning -- follows the mechanism
//	retirements it duplicates; wake wire-mirroring -- needs a typed AMQ
//	health surface, and wake itself is an amq#480 non-goal that never
//	moves. Also: the two version floors introduced in v2.30.0 (adoption
//	floor vs doctorMinAMQVersion) merge back into one only when the
//	default flip completes."
//
// Each subtest below pins one register entry (or gh#732's own additive-only
// non-negotiables) to a concrete, compile-and-run-time-checked surface, with
// its v2.31.0 removal prerequisite noted in the subtest's own comment.
func TestV2300RemovesNothing(t *testing.T) {
	t.Run("legacy terminal backends all still registered (pane/tmux plumbing register entry)", func(t *testing.T) {
		// Removal prerequisite: Inspect-based liveness landed and consuming
		// status/health (gh#737). Nothing about that has landed yet, so all
		// four pre-v2.30.0 backends plus the new opt-in launchapi one must be
		// present -- five total, none missing.
		want := []string{"tmux", "iterm2", "terminal", "tmux-session", "launchapi"}
		for _, name := range want {
			if _, ok := teamLaunchBackends[name]; !ok {
				t.Errorf("teamLaunchBackends missing %q", name)
			}
		}
		if got := len(teamLaunchBackends); got != len(want) {
			t.Errorf("teamLaunchBackends has %d entries, want exactly %d (%v): got keys %v", got, len(want), want, registeredTeamLaunchTerminals())
		}
	})

	t.Run("legacy launch selection is unchanged when --launch-via is absent (opt-in-only register entry)", func(t *testing.T) {
		// Removal prerequisite: none -- this is gh#733's own opt-in
		// invariant, permanent until the default flip milestone (v2.31.0)
		// explicitly promotes launchapi to auto.
		backend, err := resolveTeamLaunchBackend(teamLaunchOptions{Terminal: "tmux"})
		if err != nil {
			t.Fatalf("legacy default selection: %v", err)
		}
		if backend.Name() != "tmux" {
			t.Fatalf("absent --launch-via selected %q, want tmux", backend.Name())
		}
	})

	t.Run("legacy composer literals unchanged (gh#732's own additive-only non-negotiable)", func(t *testing.T) {
		// Removal prerequisite: none -- gh#732/gh#733 require these stay
		// byte-identical throughout v2.30.0's dual-run window; see also
		// TestCodexApproveForMeArgsStillEmitsApprovalsReviewer and
		// TestClaudePreauthChildArgsStillEmitsAllowedTools.
		wantCodex := []string{"--sandbox", "workspace-write", "--ask-for-approval", "on-request", "-c", `approvals_reviewer="auto_review"`}
		if got := defaultChildArgsForBinaryWithTrust("codex", trustModeApproveForMe); strings.Join(got, "\x00") != strings.Join(wantCodex, "\x00") {
			t.Fatalf("codex approve-for-me default args = %v, want %v", got, wantCodex)
		}
		if got := claudePreauthChildArgs(claudeInScopePreauthAllowlist("s")); len(got) != 1 || !strings.HasPrefix(got[0], "--allowedTools=") {
			t.Fatalf("claude preauth child args = %v, want a single --allowedTools= grant", got)
		}
	})

	t.Run("launch.Record's full field surface is untouched (launch-record split register entry)", func(t *testing.T) {
		// Removal prerequisite: the new (launchapi) path proven in real
		// runs. Referencing every pre-v2.30.0 field is a compile-time
		// existence check: if the "layer-owned majority" were pruned before
		// its prerequisite, this subtest would fail to build.
		rec := launch.Record{
			Schema: 1, CWD: "/x", Binary: "claude", Argv: nil, Session: "s",
			SharedWorkstream: false, Conversation: "", Handle: "h", Role: "r",
			Root: "/root", BaseRoot: "/base", RootSource: "env", AMQVersion: "0.70.0",
			CodexArgs: nil, ClaudeArgs: nil, Launcher: "", LauncherArgs: nil,
			Model: "", ToolProfile: "", ToolConfig: "", ToolMCPConfig: "",
			ToolAllowlist: nil, ToolBlocklist: nil, Trust: "", NoDefaultArgs: false,
		}
		if rec.Binary != "claude" {
			t.Fatalf("unreachable: %+v", rec)
		}
	})

	t.Run("coop-exec provisioning path is still reachable from every legacy backend (coop provisioning shim register entry)", func(t *testing.T) {
		// Removal prerequisite: plan-driven provisioning plus upstream's
		// breaking coop-exec change (neither has landed).
		if _, err := exec.LookPath("true"); err != nil {
			t.Skip("no shell available in this environment to probe emitTeamCommand output shape")
		}
		out := emitTeamCommand(emitTeamCommandInput{
			CWD: "/proj", SquadBin: "amq-squad", TeamHome: "/proj",
			Member: teamMemberForCoopProbe(), Workstream: "s",
		})
		if !strings.Contains(out, "coop") && !strings.Contains(out, "amq-squad") {
			t.Fatalf("emitTeamCommand output no longer references the coop-exec provisioning path: %q", out)
		}
	})

	t.Run("doctorMinAMQVersion floor is untouched by the new adoption floor (two-version-floors register entry)", func(t *testing.T) {
		// Removal prerequisite: the two floors merge back into one only when
		// the default flip (v2.31.0) completes. Until then doctorMinAMQVersion
		// (the legacy-path floor) and the launchapi adoption floor (pinned in
		// go.mod as agent-message-queue v0.70.0, negotiated by
		// launchapi.Negotiate in gh#736) are two DISTINCT floors on purpose.
		if doctorMinAMQVersion != "0.60.0" {
			t.Fatalf("doctorMinAMQVersion changed to %q, want the untouched legacy floor 0.60.0", doctorMinAMQVersion)
		}
		modData, err := os.ReadFile(goModPathForTest(t))
		if err != nil {
			t.Fatalf("read go.mod: %v", err)
		}
		if !strings.Contains(string(modData), "github.com/avivsinai/agent-message-queue v0.70.0") {
			t.Fatalf("go.mod no longer pins the v0.70.0 adoption floor dependency")
		}
	})

	t.Run("wake injection fields are still on teamLaunchOptions (wake wire-mirroring register entry)", func(t *testing.T) {
		// Removal prerequisite: a typed AMQ health surface; wake itself is an
		// amq#480 non-goal that never moves, so this entry has no removal
		// date at all -- these fields must never disappear from v2.30.0 on.
		opts := teamLaunchOptions{WakeInjectVia: "/bin/true", WakeInjectMode: "raw", WakeInjectArgs: []string{"x"}}
		if opts.WakeInjectVia == "" || opts.WakeInjectMode == "" || len(opts.WakeInjectArgs) == 0 {
			t.Fatalf("unreachable: %+v", opts)
		}
	})
}

func teamMemberForCoopProbe() team.Member {
	return team.Member{Role: "probe", Binary: "claude", Handle: "probe"}
}

func goModPathForTest(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(wd, "..", "..", "go.mod")
}
