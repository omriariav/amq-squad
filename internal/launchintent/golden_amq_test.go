package launchintent

import (
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/avivsinai/agent-message-queue/launchapi"
)

// TestCompiledIntentAcceptedByReleasedAMQ proves the compiled golden intent
// for amq-squad's real default profile (cto: codex lead, fullstack: claude,
// plus the non-runnable operator -- see internal/cli/simple_wizard.go's
// `--lead cto` default and `--roles cto,fullstack --binary cto=codex`
// example) passes launchapi's own strict decode — the same decode path the
// pinned v0.70.0 adoption floor performs on an intent it receives. It
// follows the same real-binary-gated convention already used by
// internal/cli/real_amq_compatibility_test.go: set AMQ_SQUAD_REAL_AMQ to a
// real amq binary to prove the floor is genuinely available in this
// environment; the test skips (not fails) when that floor binary is absent,
// since the round trip through the imported launchapi package below already
// exercises the same v0.70.0 contract code the binary embeds.
func TestCompiledIntentAcceptedByReleasedAMQ(t *testing.T) {
	binary := strings.TrimSpace(os.Getenv("AMQ_SQUAD_REAL_AMQ"))
	if binary == "" {
		t.Skip("set AMQ_SQUAD_REAL_AMQ to a real amq binary to run the floor-binary proof; skipped when floor binary absent")
	}
	if info, err := os.Stat(binary); err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
		t.Skipf("AMQ_SQUAD_REAL_AMQ is unavailable or not executable: %v", err)
	}
	if out, err := exec.Command(binary, "version").CombinedOutput(); err != nil {
		t.Skipf("floor binary %q did not report a version: %v (%s)", binary, err, strings.TrimSpace(string(out)))
	}

	in := Input{
		Operator: OperatorFacts{Handle: "user"},
		Seats: []SeatFacts{
			{
				Handle:          "cto",
				Executable:      "/usr/bin/codex",
				Args:            []string{"--sandbox", "workspace-write", "--ask-for-approval", "on-request"},
				Cwd:             SeatCWD{Kind: launchapi.WorkingDirectoryAbsolute, Path: "/Users/omri.a/Code/amq-squad"},
				ResumePolicy:    launchapi.ResumePolicyResume,
				BootstrapPrompt: "You are cto. Orchestrate the v2.31.0 milestone.",
				RequireWake:     true,
			},
			{
				Handle:          "fullstack",
				Executable:      "/usr/bin/claude",
				Args:            []string{"--permission-mode", "auto", "--model", "claude-fable-5"},
				Cwd:             SeatCWD{Kind: launchapi.WorkingDirectoryAbsolute, Path: "/Users/omri.a/Code/amq-squad-wt-fable-squad-v2-31-0-fullstack"},
				ResumePolicy:    launchapi.ResumePolicyFresh,
				BootstrapPrompt: "You are fullstack. Wait for a task on AMQ.",
				RequireWake:     true,
				// gh#748: a named seat, gated on the pinned module's real
				// argv grammar the same way the scoped grant and
				// approvals_reviewer already are in this fixture --
				// intent.Validate() below (called inside Compile) enforces
				// the real per-provider argRules against whatever launchapi
				// version is pinned, so this seat's -n token round-tripping
				// through Marshal/Decode below is the "accepted by the real
				// v0.73.0 binary" proof this issue's acceptance criteria asks
				// for, exercised in-process against the pinned module exactly
				// like docs/amq-0.73.0-adoption-verdict.md section 3/5's own
				// measurements were.
				AllowedArgumentForms: []string{"-n", "--name"},
				SessionName:          "v2-31-0/fullstack",
			},
		},
		Target: TargetFacts{
			ProjectRoot: "/Users/omri.a/Code/amq-squad",
			BaseRoot:    "/Users/omri.a/Code/amq-squad/.agent-mail/fable-squad/v2-31-0",
			SessionRoot: "/Users/omri.a/Code/amq-squad",
			Session:     "v2-31-0",
		},
	}

	intent, _, err := Compile(in)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	data, err := launchapi.MarshalLaunchIntentV1(intent)
	if err != nil {
		t.Fatalf("MarshalLaunchIntentV1: %v", err)
	}
	decoded, err := launchapi.DecodeLaunchIntentV1(data)
	if err != nil {
		t.Fatalf("DecodeLaunchIntentV1 rejected the golden compiled intent: %v\n%s", err, data)
	}
	if !reflect.DeepEqual(decoded, intent) {
		t.Fatalf("decoded intent does not round-trip:\n got:  %+v\n want: %+v", decoded, intent)
	}
}
