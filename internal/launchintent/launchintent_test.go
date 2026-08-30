package launchintent

import (
	"reflect"
	"strings"
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

func TestCompileIntentOperatorSeatIsNonRunnableHandleOnly(t *testing.T) {
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

func TestCompileIntentWorktreeSeatCarriesCwd(t *testing.T) {
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

// TestCompileIntentEmitsNoAllowedToolsBelowFloor replaces the old
// unconditional TestCompileIntentEmitsNoAllowedTools (gh#747): below the
// scoped-grammar floor (ArgvGrammarVersion's zero value included), every
// --allowedTools spelling is still dropped, matching gh#732's original
// behavior byte-for-byte.
func TestCompileIntentEmitsNoAllowedToolsBelowFloor(t *testing.T) {
	seat := baseSeat("fullstack", "/Users/omri.a/Code/amq-squad")
	// Simulate a caller that (incorrectly) tried to smuggle every native
	// spelling of the flag through resolved args; Compile must still emit
	// none of them below the floor.
	seat.Args = []string{
		"--permission-mode", "auto",
		"--allowedTools", "Bash(gh pr create:*)",
		"--allowedTools=Bash,Read",
	}
	seat.ArgvGrammarVersion = 1 // below scopedGrammarFloor (2)
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
				t.Fatalf("participant %q emitted forbidden --allowedTools token below floor: %q", p.Handle, arg)
			}
		}
	}
}

// TestCompileIntentEmitsScopedAllowedToolsAtOrAboveFloor (gh#747): at or
// above the scoped-grammar floor, the two-token grant survives; the
// equals-joined spelling is still always dropped (never accepted upstream
// at any measured version, see docs/amq-0.73.0-adoption-verdict.md section
// 3), and a bare `Bash` value is still refused even at the floor (the
// compiler's own "never widen the grant" defense-in-depth, independent of
// what the caller passed in).
func TestCompileIntentEmitsScopedAllowedToolsAtOrAboveFloor(t *testing.T) {
	seat := baseSeat("fullstack", "/Users/omri.a/Code/amq-squad")
	seat.Args = []string{
		"--permission-mode", "auto",
		"--allowedTools", ScopedPreauthGrant,
		"--allowedTools=Bash,Read",
	}
	seat.ArgvGrammarVersion = 2
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
	want := []string{"--permission-mode", "auto", "--allowedTools", ScopedPreauthGrant}
	if !reflect.DeepEqual(member.Args, want) {
		t.Fatalf("compiled args = %v, want %v (equals-joined form must still be dropped)", member.Args, want)
	}

	bareSeat := baseSeat("senior-dev", "/Users/omri.a/Code/amq-squad")
	bareSeat.Args = []string{"--allowedTools", "Bash"}
	bareSeat.ArgvGrammarVersion = 2
	in2 := Input{Operator: OperatorFacts{Handle: "user"}, Seats: []SeatFacts{bareSeat}, Target: baseTarget()}
	intent2, _, err := Compile(in2)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	for _, arg := range intent2.Participants[1].Args {
		if arg == "Bash" {
			t.Fatalf("bare Bash grant must be refused even at or above the floor: %v", intent2.Participants[1].Args)
		}
	}
}

// TestCompileIntentDropsForeignAllowedToolsValuesAtFloor (gh#747, lead
// review finding): at or above the floor, only ScopedPreauthGrant exactly
// survives. Any other value, including one shaped like a legitimate scoped
// pattern, is dropped -- "never widen the grant" means exact equality, not
// "looks safe."
func TestCompileIntentDropsForeignAllowedToolsValuesAtFloor(t *testing.T) {
	cases := []struct {
		name  string
		value string
	}{
		{"destructive rm pattern", "Bash(rm -rf:*)"},
		{"unrelated bare tool name", "Read"},
		{"legitimate grant plus an extra entry", "Bash(gh pr create:*),Read"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			seat := baseSeat("fullstack", "/Users/omri.a/Code/amq-squad")
			seat.Args = []string{"--allowedTools", tc.value}
			seat.ArgvGrammarVersion = 2
			in := Input{
				Operator: OperatorFacts{Handle: "user"},
				Seats:    []SeatFacts{seat},
				Target:   baseTarget(),
			}
			intent, _, err := Compile(in)
			if err != nil {
				t.Fatalf("Compile: %v", err)
			}
			for _, arg := range intent.Participants[1].Args {
				if arg == tc.value {
					t.Fatalf("foreign --allowedTools value %q survived at the floor: %v", tc.value, intent.Participants[1].Args)
				}
			}
			if len(intent.Participants[1].Args) != 0 {
				t.Fatalf("expected --allowedTools entirely dropped for a foreign value, got %v", intent.Participants[1].Args)
			}
		})
	}
}

// TestCompileIntentDropsApprovalsReviewerBelowFloor replaces
// TestCompileIntentDropsApprovalsReviewerOnNewPathOnly (gh#747): below the
// floor (ReviewerOverrideAllowed false, including its zero value), the
// override is still dropped, matching gh#732's original behavior
// byte-for-byte. The legacy composer's own emission of the same literal is
// locked separately in internal/cli/agent_defaults_gh732_legacy_lock_test.go,
// unaffected by this package.
func TestCompileIntentDropsApprovalsReviewerBelowFloor(t *testing.T) {
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
			t.Fatalf("below floor must drop approvals_reviewer, found it in compiled args: %v", member.Args)
		}
	}
	// The rest of the trust-mode args survive: dropping approvals_reviewer
	// is a targeted removal, not a wipe of the seat's native args.
	want := []string{"--sandbox", "workspace-write", "--ask-for-approval", "on-request"}
	if !reflect.DeepEqual(member.Args, want) {
		t.Fatalf("compiled args = %v, want %v", member.Args, want)
	}
}

// TestCompileIntentKeepsApprovalsReviewerAtOrAboveFloor (gh#747): at or
// above the floor (ReviewerOverrideAllowed true), the legacy literal
// survives byte-identically, including its exact quoting.
func TestCompileIntentKeepsApprovalsReviewerAtOrAboveFloor(t *testing.T) {
	seat := baseSeat("senior-dev", "/Users/omri.a/Code/amq-squad")
	seat.Executable = "/usr/bin/codex"
	seat.Args = []string{"--sandbox", "workspace-write", "--ask-for-approval", "on-request", "-c", `approvals_reviewer="auto_review"`}
	seat.ReviewerOverrideAllowed = true
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
	want := []string{"--sandbox", "workspace-write", "--ask-for-approval", "on-request", "-c", `approvals_reviewer="auto_review"`}
	if !reflect.DeepEqual(member.Args, want) {
		t.Fatalf("compiled args = %v, want %v (approvals_reviewer must survive byte-identically at or above the floor)", member.Args, want)
	}
}

// TestCompileIntentNamedSeatCarriesSessionHandleName (gh#748): a seat whose
// observed AllowedArgumentForms includes "-n" and whose SessionName is set
// gets -n <SessionName> appended as two argv tokens, in "<session>/<handle>"
// shape (docs/amq-0.73.0-adoption-verdict.md section 5), never --name=.
func TestCompileIntentNamedSeatCarriesSessionHandleName(t *testing.T) {
	seat := baseSeat("fullstack", "/Users/omri.a/Code/amq-squad")
	seat.AllowedArgumentForms = []string{"-n", "--name"}
	seat.SessionName = "v2-30-0/fullstack"
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
	want := []string{"--permission-mode", "auto", "-n", "v2-30-0/fullstack"}
	if !reflect.DeepEqual(member.Args, want) {
		t.Fatalf("compiled args = %v, want %v", member.Args, want)
	}
	for _, arg := range member.Args {
		if strings.HasPrefix(arg, "--name=") {
			t.Fatalf("named seat used the equals-joined --name= spelling, never allowed: %v", member.Args)
		}
	}
}

// TestCompileIntentUnnamedSeatIsByteIdenticalToV1 (gh#748): a seat with an
// empty SessionName (the default) compiles identically whether or not
// AllowedArgumentForms includes naming forms -- naming is opt-in per seat
// via SessionName, not auto-applied whenever the capability is present.
func TestCompileIntentUnnamedSeatIsByteIdenticalToV1(t *testing.T) {
	withForms := baseSeat("fullstack", "/Users/omri.a/Code/amq-squad")
	withForms.AllowedArgumentForms = []string{"-n", "--name"}
	withoutForms := baseSeat("fullstack", "/Users/omri.a/Code/amq-squad")

	for name, seat := range map[string]SeatFacts{"forms present, SessionName empty": withForms, "forms absent, SessionName empty": withoutForms} {
		t.Run(name, func(t *testing.T) {
			intent, _, err := Compile(Input{
				Operator: OperatorFacts{Handle: "user"},
				Seats:    []SeatFacts{seat},
				Target:   baseTarget(),
			})
			if err != nil {
				t.Fatalf("Compile: %v", err)
			}
			want := []string{"--permission-mode", "auto"}
			if !reflect.DeepEqual(intent.Participants[1].Args, want) {
				t.Fatalf("compiled args = %v, want %v (byte-identical to the pre-gh#748 V1 shape)", intent.Participants[1].Args, want)
			}
		})
	}
}

// TestCompileIntentDropsNameWhenArgumentFormsLackIt (gh#748): SessionName
// set but AllowedArgumentForms does not include "-n"/"--name" (a codex
// seat's actual shape on every measured version, or a claude seat below the
// naming floor) -- naming is silently skipped, matching gh#747's established
// safe-direction default, not an error.
func TestCompileIntentDropsNameWhenArgumentFormsLackIt(t *testing.T) {
	cases := []struct {
		name  string
		forms []string
	}{
		{"no forms observed at all", nil},
		{"codex-shaped forms (no -n/--name entry)", []string{"--sandbox", "--ask-for-approval"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			seat := baseSeat("senior-dev", "/Users/omri.a/Code/amq-squad")
			seat.AllowedArgumentForms = tc.forms
			seat.SessionName = "v2-30-0/senior-dev"
			intent, _, err := Compile(Input{
				Operator: OperatorFacts{Handle: "user"},
				Seats:    []SeatFacts{seat},
				Target:   baseTarget(),
			})
			if err != nil {
				t.Fatalf("Compile: %v", err)
			}
			for _, arg := range intent.Participants[1].Args {
				if arg == "-n" || arg == "--name" || strings.HasPrefix(arg, "--name=") {
					t.Fatalf("naming form emitted despite AllowedArgumentForms lacking it: %v", intent.Participants[1].Args)
				}
			}
		})
	}
}

// TestCompileIntentRejectsMalformedSessionName (gh#748): a non-empty
// SessionName that fails the same local shape check the real grammar uses
// (validManagedSessionLabel, mirroring upstream's validSessionLabel) errors
// at Compile time rather than silently launching unnamed or deferring the
// failure to an opaque Prepare/launchapi refusal.
func TestCompileIntentRejectsMalformedSessionName(t *testing.T) {
	cases := []string{
		"",                 // handled separately (empty means "no naming"), included for contrast
		"a/b/c",            // more than 2 parts
		"-leading-dash",    // leading '-'
		"Has/Upper",        // canonical pattern is lowercase only
		"has space/handle", // space not in canonical pattern
	}
	for _, value := range cases {
		if value == "" {
			continue // empty SessionName means "skip naming", covered elsewhere, not a malformed-value case
		}
		t.Run(value, func(t *testing.T) {
			seat := baseSeat("fullstack", "/Users/omri.a/Code/amq-squad")
			seat.AllowedArgumentForms = []string{"-n"}
			seat.SessionName = value
			_, _, err := Compile(Input{
				Operator: OperatorFacts{Handle: "user"},
				Seats:    []SeatFacts{seat},
				Target:   baseTarget(),
			})
			if err == nil {
				t.Fatalf("Compile accepted malformed SessionName %q", value)
			}
		})
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
