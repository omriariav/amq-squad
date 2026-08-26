package launchintent

import (
	"reflect"
	"testing"

	"github.com/avivsinai/agent-message-queue/launchapi"
)

func baseSeat(handle, cwd string) SeatFacts {
	return SeatFacts{
		Handle:      handle,
		Executable:  "/usr/bin/claude",
		Args:        []string{"--permission-mode", "auto"},
		Cwd:         SeatCWD{Kind: launchapi.WorkingDirectoryAbsolute, Path: cwd},
		RequireWake: true,
	}
}

func baseTarget() TargetFacts {
	return TargetFacts{
		ProjectRoot: "/Users/omri.a/Code/amq-squad",
		BaseRoot:    "/Users/omri.a/Code/amq-squad/.agent-mail/squad-v2-30-0/v2-30-0",
		SessionRoot: "/Users/omri.a/Code/amq-squad",
		Session:     "v2-30-0",
	}
}

func TestCompileIntentOperatorIsHandleOnly(t *testing.T) {
	in := Input{
		Operator: OperatorFacts{Handle: "user"},
		Seats:    []SeatFacts{baseSeat("fullstack", "/Users/omri.a/Code/amq-squad")},
		Target:   baseTarget(),
	}
	intent, _, err := Compile(in)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if len(intent.Participants) != 2 {
		t.Fatalf("expected 2 participants, got %d", len(intent.Participants))
	}
	operator := intent.Participants[0]
	want := launchapi.ParticipantV1{Handle: "user", Runnable: false}
	if !reflect.DeepEqual(operator, want) {
		t.Fatalf("operator participant not handle-only: got %+v, want %+v", operator, want)
	}
}

func TestCompileIntentSiblingWorktreeCwdPreserved(t *testing.T) {
	worktreeCWD := "/Users/omri.a/Code/amq-squad-wt-squad-v2-30-0-v2-30-0-senior-dev"
	in := Input{
		Operator: OperatorFacts{Handle: "user"},
		Seats:    []SeatFacts{baseSeat("senior-dev", worktreeCWD)},
		Target:   baseTarget(),
	}
	intent, target, err := Compile(in)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	member := intent.Participants[1]
	if member.Cwd == nil {
		t.Fatalf("expected member cwd to be set")
	}
	if member.Cwd.Kind != launchapi.WorkingDirectoryAbsolute {
		t.Fatalf("expected absolute cwd kind, got %q", member.Cwd.Kind)
	}
	if member.Cwd.Path != worktreeCWD {
		t.Fatalf("cwd path = %q, want sibling worktree path %q", member.Cwd.Path, worktreeCWD)
	}
	if member.Cwd.Path == target.SessionRoot {
		t.Fatalf("sibling worktree cwd must be distinct from session root, both were %q", member.Cwd.Path)
	}
}

func TestCompileIntentEmitsNoAllowedTools(t *testing.T) {
	seat := baseSeat("fullstack", "/Users/omri.a/Code/amq-squad")
	// Simulate a caller that (incorrectly) tried to smuggle every native
	// spelling of the flag through resolved args; Compile must still emit
	// none of them.
	seat.Args = []string{
		"--permission-mode", "auto",
		"--allowedTools", "Bash",
		"--allowedTools=Bash,Read",
	}
	in := Input{
		Operator: OperatorFacts{Handle: "user"},
		Seats:    []SeatFacts{seat},
		Target:   baseTarget(),
	}
	intent, _, err := Compile(in)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	for _, p := range intent.Participants {
		for _, arg := range p.Args {
			if arg == "--allowedTools" || len(arg) >= len("--allowedTools=") && arg[:len("--allowedTools=")] == "--allowedTools=" {
				t.Fatalf("participant %q emitted forbidden --allowedTools token: %q", p.Handle, arg)
			}
		}
	}
}

func TestCompileIntentDropsApprovalsReviewerOnNewPathOnly(t *testing.T) {
	seat := baseSeat("senior-dev", "/Users/omri.a/Code/amq-squad")
	seat.Executable = "/usr/bin/codex"
	// The exact legacy literal from internal/cli/agent_defaults.go
	// codexApproveForMeArgs, smuggled through resolved args.
	seat.Args = []string{"--sandbox", "workspace-write", "--ask-for-approval", "on-request", "-c", `approvals_reviewer="auto_review"`}
	in := Input{
		Operator: OperatorFacts{Handle: "user"},
		Seats:    []SeatFacts{seat},
		Target:   baseTarget(),
	}
	intent, _, err := Compile(in)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	member := intent.Participants[1]
	for i, arg := range member.Args {
		if arg == "-c" && i+1 < len(member.Args) && member.Args[i+1] == `approvals_reviewer="auto_review"` {
			t.Fatalf("new path must drop approvals_reviewer, found it in compiled args: %v", member.Args)
		}
	}
	// The rest of the trust-mode args survive: dropping approvals_reviewer
	// is a targeted removal, not a wipe of the seat's native args.
	want := []string{"--sandbox", "workspace-write", "--ask-for-approval", "on-request"}
	if !reflect.DeepEqual(member.Args, want) {
		t.Fatalf("compiled args = %v, want %v", member.Args, want)
	}
}

func TestCompileIntentBootstrapPromptGoesToInitialInput(t *testing.T) {
	seat := baseSeat("fullstack", "/Users/omri.a/Code/amq-squad")
	prompt := "You are fullstack. Read team-rules.md and the active brief, then wait for a task."
	seat.BootstrapPrompt = prompt
	in := Input{
		Operator: OperatorFacts{Handle: "user"},
		Seats:    []SeatFacts{seat},
		Target:   baseTarget(),
	}
	intent, _, err := Compile(in)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	member := intent.Participants[1]
	if member.InitialInput == nil {
		t.Fatalf("expected initial_input to carry the bootstrap prompt")
	}
	if member.InitialInput.Kind != launchapi.InitialInputArgument {
		t.Fatalf("initial_input.kind = %q, want %q", member.InitialInput.Kind, launchapi.InitialInputArgument)
	}
	if member.InitialInput.Text != prompt {
		t.Fatalf("initial_input.text = %q, want %q", member.InitialInput.Text, prompt)
	}
	for _, arg := range member.Args {
		if arg == prompt {
			t.Fatalf("bootstrap prompt must never appear in args, found it: %v", member.Args)
		}
	}
}

func TestCompileRejectsMissingBaseRoot(t *testing.T) {
	target := baseTarget()
	target.BaseRoot = ""
	in := Input{
		Operator: OperatorFacts{Handle: "user"},
		Seats:    []SeatFacts{baseSeat("fullstack", "/Users/omri.a/Code/amq-squad")},
		Target:   target,
	}
	if _, _, err := Compile(in); err == nil {
		t.Fatalf("expected Compile to reject a missing base root, gh#734 requires it always explicit")
	}
}
