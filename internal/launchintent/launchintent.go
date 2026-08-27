// Package launchintent compiles an already-resolved team profile, session,
// and per-seat runtime facts into AMQ's public launchapi.LaunchIntentV1 and
// launchapi.TargetV1. Compile is a pure function: it does no launching, no
// filesystem or AMQ I/O, and no side effects, no probing. Every fact it
// needs (worktree cwd, bootstrap prompt text, resolved native args, and as
// of gh#747 the observed launchapi argv-grammar capability for the seat) is
// resolved by the caller and handed in; this package only shapes that data
// into the contract types and applies the argv guarantees measured against
// released AMQ (see gh#732, gh#736, gh#747, gh#748, and
// docs/amq-0.73.0-adoption-verdict.md): the equals-joined --allowedTools
// spelling is never accepted upstream at any measured version and is always
// dropped; the two-token scoped grant and approvals_reviewer survive only
// when the caller's per-seat capability facts say the negotiated contract
// supports them, and the compiler still refuses (regardless of what the
// caller passed in) a bare `Bash` grant or any value that could be
// reinterpreted as a flag. A seat's managed-plan naming (-n <session>/
// <handle>, gh#748) is likewise gated on the caller's observed
// AllowedArgumentForms, never assumed.
package launchintent

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/avivsinai/agent-message-queue/launchapi"
)

// ScopedPreauthGrant is the single source of truth for the PR-creation
// preauth pattern this compiler may emit on the new path (gh#747), gated on
// the seat's observed ArgvGrammarVersion. It mirrors
// internal/cli/agent_defaults.go's legacy claudeInScopePreauthAllowlist
// literal exactly; that legacy function is updated to reference this
// constant instead of its own copy, so the pattern is spelled out in
// exactly one place regardless of which path emits it.
const ScopedPreauthGrant = "Bash(gh pr create:*)"

// scopedGrammarFloor is the minimum ArgvGrammarVersion (see
// docs/amq-0.73.0-adoption-verdict.md section 3/6) at or above which the
// scoped --allowedTools grant is accepted by the real grammar. Below it,
// the grant is refused upstream regardless of how it is spelled, so the
// sanitizer drops it in the safe direction.
const scopedGrammarFloor = 2

// namedSeatArgumentForms are the argv forms gh#748's measured facts (see
// docs/amq-0.73.0-adoption-verdict.md section 5) confirm accept a managed
// session/handle label on Claude: "-n" and "--name", both two-token only
// (no "--name=" inline special-case exists for either, matching the two-
// token-only pattern already established for --allowedTools in gh#747).
// Codex has no such argRules entry on any measured version, so a codex
// seat's AllowedArgumentForms never contains either and naming is skipped
// -- Compile does not special-case the provider, only what the caller
// observed.
var namedSeatArgumentForms = map[string]bool{"-n": true, "--name": true}

// canonicalSessionLabelPattern mirrors the real grammar's own
// canonicalSessionPattern exactly (internal/launch/config.go in the pinned
// module): lowercase alphanumeric/underscore, starting with one of those,
// then any run of lowercase alphanumeric/underscore/hyphen.
var canonicalSessionLabelPattern = regexp.MustCompile(`^[a-z0-9_][a-z0-9_-]*$`)

// validManagedSessionLabel mirrors the real grammar's own validSessionLabel
// exactly (internal/launch/adapter.go in the pinned module): split on '/',
// at most two parts, each matching canonicalSessionLabelPattern and not
// leading with '-' (redundant with the pattern, kept for parity with
// upstream's own belt-and-suspenders check), and the whole value non-empty.
// Compile validates locally before ever emitting -n so a malformed
// SessionName fails closed here with a clear error, not as an opaque
// Prepare/launchapi refusal downstream.
func validManagedSessionLabel(value string) bool {
	if value == "" {
		return false
	}
	parts := strings.Split(value, "/")
	if len(parts) > 2 {
		return false
	}
	for _, part := range parts {
		if !canonicalSessionLabelPattern.MatchString(part) || strings.HasPrefix(part, "-") {
			return false
		}
	}
	return true
}

// allowsManagedName reports whether the seat's observed AllowedArgumentForms
// includes either accepted spelling of the naming flag.
func allowsManagedName(forms []string) bool {
	for _, f := range forms {
		if namedSeatArgumentForms[f] {
			return true
		}
	}
	return false
}

// OperatorFacts is the resolved identity of the non-runnable operator seat.
// The compiled participant carries nothing beyond the handle: launchapi
// itself rejects a non-runnable participant with any other field set.
type OperatorFacts struct {
	Handle string
}

// SeatCWD is an already-resolved seat working directory. Compile never
// touches the filesystem to produce or validate this value; the caller
// resolves worktree/session placement (sibling worktrees included) first.
type SeatCWD struct {
	Kind launchapi.WorkingDirectoryKind
	Path string
}

// SeatFacts is the fully resolved runtime shape of one runnable participant:
// a team member's binary/args resolution, its placement, and its bootstrap
// text, all assembled by the caller before Compile runs.
type SeatFacts struct {
	Handle          string
	Executable      string
	Args            []string
	Wrapper         *launchapi.WrapperV1
	Cwd             SeatCWD
	EnvOverlay      map[string]string
	ResumePolicy    launchapi.ResumePolicy
	OnLive          launchapi.OnLivePolicyV1
	BootstrapPrompt string
	RequireWake     bool
	NoGitignore     bool
	WakeAuditReason string
	Injector        *launchapi.InjectorOptionsV1
	Symphony        *launchapi.SymphonyOptionsV1

	// The following are caller-resolved capability facts (gh#747), derived
	// from a prior read-only launchapi.Prepare probe's
	// PreviewV1.Capabilities for this seat's provider
	// (docs/amq-0.73.0-adoption-verdict.md: Prepare is read-only and
	// deterministic, safe to call as a probe). Compile never probes
	// anything itself; it only gates on what the caller observed.

	// ArgvGrammarVersion is the seat provider's observed
	// ProviderCapabilitiesV1.GrammarVersion. Zero means "not observed" and
	// is treated as below the scoped-grammar floor, the safe direction.
	ArgvGrammarVersion int
	// ReviewerOverrideAllowed is whether the seat provider's observed
	// capabilities carry a config_override entry with key
	// "approvals_reviewer". False (including "not observed") drops the
	// approvals_reviewer override, the safe direction.
	ReviewerOverrideAllowed bool
	// AllowedArgumentForms is the seat provider's observed
	// ProviderCapabilitiesV1.AllowedArgumentForms. gh#748: Compile now
	// consumes this itself to gate whether SessionName below is ever
	// emitted (previously threaded through unconsumed for a future caller;
	// that caller is this package, now).
	AllowedArgumentForms []string
	// SessionName is the caller-resolved "<session>/<handle>" managed-plan
	// label (gh#748). Compile emits it as -n <SessionName> (two argv
	// tokens, never --name=) only when AllowedArgumentForms says the
	// negotiated contract accepts the naming flag; empty means "do not
	// attempt naming for this seat" (the default, and always the case for
	// a provider gh#748's measured facts say never accepts it, such as
	// codex). A non-empty value that fails local validation is a caller
	// bug and errors rather than silently launching unnamed.
	SessionName string
}

// TargetFacts is the resolved launch target: the project root, the accepted
// base root, and the session this intent is being compiled for. BaseRoot is
// always explicit here; Compile never performs upward discovery to find it
// (gh#734 is the seam that enforces this at the adoption boundary — this
// package simply never has a code path that could search for one).
type TargetFacts struct {
	ProjectRoot string
	BaseRoot    string
	SessionRoot string
	Session     string
}

// Input is Compile's complete pure-function input.
type Input struct {
	Operator OperatorFacts
	Seats    []SeatFacts
	Target   TargetFacts
}

// Compile turns a resolved Input into a launchapi.LaunchIntentV1 and
// launchapi.TargetV1. It performs no launching, no filesystem or AMQ I/O.
func Compile(in Input) (launchapi.LaunchIntentV1, launchapi.TargetV1, error) {
	operatorHandle := strings.TrimSpace(in.Operator.Handle)
	if operatorHandle == "" {
		return launchapi.LaunchIntentV1{}, launchapi.TargetV1{}, fmt.Errorf("launchintent: operator handle is required")
	}
	if len(in.Seats) == 0 {
		return launchapi.LaunchIntentV1{}, launchapi.TargetV1{}, fmt.Errorf("launchintent: at least one runnable seat is required")
	}

	participants := make([]launchapi.ParticipantV1, 0, len(in.Seats)+1)
	participants = append(participants, launchapi.ParticipantV1{
		Handle:   operatorHandle,
		Runnable: false,
	})

	seen := map[string]struct{}{operatorHandle: {}}
	for _, seat := range in.Seats {
		handle := strings.TrimSpace(seat.Handle)
		if handle == "" {
			return launchapi.LaunchIntentV1{}, launchapi.TargetV1{}, fmt.Errorf("launchintent: seat handle is required")
		}
		if _, dup := seen[handle]; dup {
			return launchapi.LaunchIntentV1{}, launchapi.TargetV1{}, fmt.Errorf("launchintent: duplicate participant handle %q", handle)
		}
		seen[handle] = struct{}{}
		participant, err := compileSeat(seat)
		if err != nil {
			return launchapi.LaunchIntentV1{}, launchapi.TargetV1{}, fmt.Errorf("launchintent: seat %q: %w", handle, err)
		}
		participants = append(participants, participant)
	}

	intent := launchapi.LaunchIntentV1{
		IntentVersion: launchapi.IntentVersionV1,
		Participants:  participants,
	}
	if err := intent.Validate(); err != nil {
		return launchapi.LaunchIntentV1{}, launchapi.TargetV1{}, fmt.Errorf("launchintent: compiled intent failed validation: %w", err)
	}

	projectRoot := strings.TrimSpace(in.Target.ProjectRoot)
	baseRoot := strings.TrimSpace(in.Target.BaseRoot)
	if projectRoot == "" {
		return launchapi.LaunchIntentV1{}, launchapi.TargetV1{}, fmt.Errorf("launchintent: target project root is required")
	}
	if baseRoot == "" {
		return launchapi.LaunchIntentV1{}, launchapi.TargetV1{}, fmt.Errorf("launchintent: target base root is required")
	}

	target := launchapi.TargetV1{
		ProjectRoot: projectRoot,
		BaseRoot:    baseRoot,
		SessionRoot: strings.TrimSpace(in.Target.SessionRoot),
		Session:     strings.TrimSpace(in.Target.Session),
	}

	return intent, target, nil
}

func compileSeat(seat SeatFacts) (launchapi.ParticipantV1, error) {
	handle := strings.TrimSpace(seat.Handle)
	executable := strings.TrimSpace(seat.Executable)
	if executable == "" {
		return launchapi.ParticipantV1{}, fmt.Errorf("executable is required")
	}
	cwdPath := strings.TrimSpace(seat.Cwd.Path)
	if cwdPath == "" {
		return launchapi.ParticipantV1{}, fmt.Errorf("cwd is required")
	}
	cwdKind := seat.Cwd.Kind
	if cwdKind == "" {
		cwdKind = launchapi.WorkingDirectoryAbsolute
	}

	resumePolicy := seat.ResumePolicy
	if resumePolicy == "" {
		resumePolicy = launchapi.ResumePolicyFresh
	}

	var initialInput *launchapi.InitialInputV1
	if prompt := seat.BootstrapPrompt; prompt != "" {
		initialInput = &launchapi.InitialInputV1{
			Kind: launchapi.InitialInputArgument,
			Text: prompt,
		}
	}

	wake := launchapi.WakeOptionsV1{Mode: launchapi.WakeDisabled}
	if seat.RequireWake {
		wake.Mode = launchapi.WakeEnabled
	} else {
		// Disabled wake requires a non-empty audit reason (enabled wake
		// forbids one); default to a stable reason when the caller didn't
		// supply one rather than fail a seat over missing prose.
		reason := strings.TrimSpace(seat.WakeAuditReason)
		if reason == "" {
			reason = "wake not required for this seat"
		}
		wake.AuditReason = reason
	}
	if seat.Injector != nil {
		injector := *seat.Injector
		wake.Injector = &injector
	}

	var integrations launchapi.IntegrationsV1
	if seat.Symphony != nil {
		symphony := *seat.Symphony
		integrations.Symphony = &symphony
	}

	var wrapper *launchapi.WrapperV1
	if seat.Wrapper != nil {
		w := *seat.Wrapper
		w.Args = append([]string(nil), seat.Wrapper.Args...)
		wrapper = &w
	}

	args := sanitizeNewPathArgs(seat.Args, seat.ArgvGrammarVersion, seat.ReviewerOverrideAllowed)
	if seat.SessionName != "" {
		if !allowsManagedName(seat.AllowedArgumentForms) {
			// Below the floor (including "not observed"): drop naming
			// silently, the same safe direction gh#747 established for the
			// scoped grant and approvals_reviewer -- this is an expected,
			// routine outcome (e.g. a codex seat, or a claude seat on a
			// pre-v0.73.0 pin), not a caller error.
		} else if !validManagedSessionLabel(seat.SessionName) {
			return launchapi.ParticipantV1{}, fmt.Errorf("invalid SessionName %q for managed naming", seat.SessionName)
		} else {
			args = append(args, "-n", seat.SessionName)
		}
	}

	participant := launchapi.ParticipantV1{
		Handle:       handle,
		Runnable:     true,
		Executable:   executable,
		Args:         args,
		Wrapper:      wrapper,
		InitialInput: initialInput,
		Cwd:          &launchapi.WorkingDirectoryV1{Kind: cwdKind, Path: cwdPath},
		EnvOverlay:   seat.EnvOverlay,
		ResumePolicy: resumePolicy,
		Execution: &launchapi.ExecutionOptionsV1{
			RequireWake:  seat.RequireWake,
			NoGitignore:  seat.NoGitignore,
			Wake:         wake,
			Integrations: integrations,
		},
		OnLive: seat.OnLive,
	}
	return participant, nil
}

// sanitizeNewPathArgs is the contract-aware new-path argv gate (gh#732,
// gh#747). The equals-joined --allowedTools spelling is dropped
// unconditionally: docs/amq-0.73.0-adoption-verdict.md section 3 measured
// it rejected by every real grammar version, so there is no floor at which
// keeping it would ever help. The two-token --allowedTools spelling and
// -c approvals_reviewer=... survive only when the caller's per-seat
// capability facts say the negotiated contract accepts them; below the
// floor (including "not observed", ArgvGrammarVersion's zero value) both
// are dropped, matching gh#732's original unconditional behavior exactly.
//
// gh#747's non-negotiable is "Bash(gh pr create:*) only, never widen the
// grant": at or above the floor, a two-token --allowedTools value survives
// only when it is EXACTLY ScopedPreauthGrant, byte for byte. Any other
// value, including a caller-supplied native --allowedTools arg smuggled
// through resolvedArgs, is dropped, not merely shape-checked -- a laxer
// "looks like a scoped pattern" check would let a value like
// `Bash(rm -rf:*)` through at the floor, which is exactly the widening this
// package must refuse. User-supplied --allowedTools is therefore not passed
// through on the launchapi path in v2.30.1; the legacy path is unaffected
// and keeps honoring configured native args normally.
//
// Nothing else is touched, so a caller's model/effort/permission-mode args
// pass through unchanged. The legacy composer
// (internal/cli/agent_defaults.go) is untouched by this package's logic
// and keeps emitting its own byte-identical literals on the legacy path.
func sanitizeNewPathArgs(args []string, argvGrammarVersion int, reviewerOverrideAllowed bool) []string {
	if len(args) == 0 {
		return nil
	}
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--allowedTools":
			// Two-token spelling: the value is the next token, if any.
			value := ""
			hasValue := i+1 < len(args)
			if hasValue {
				value = args[i+1]
				i++
			}
			if hasValue && argvGrammarVersion >= scopedGrammarFloor && value == ScopedPreauthGrant && isSafeScopedGrant(value) {
				out = append(out, arg, value)
			}
			continue
		case strings.HasPrefix(arg, "--allowedTools="):
			// Never accepted upstream at any measured version; always drop.
			continue
		case arg == "-c" && i+1 < len(args) && strings.HasPrefix(args[i+1], "approvals_reviewer="):
			if reviewerOverrideAllowed {
				out = append(out, arg, args[i+1])
			}
			i++
			continue
		}
		out = append(out, arg)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// isSafeScopedGrant is sanitizeNewPathArgs's inner defense-in-depth guard,
// run in addition to (never instead of) the exact ScopedPreauthGrant
// equality check: it refuses a bare `Bash` grant (gh#747's "never widen the
// grant" non-negotiable: only a scoped pattern like Bash(gh pr create:*) is
// acceptable, not blanket Bash) and any value that could be reinterpreted
// as a flag (leading '-', matching the real grammar's own rejection of such
// values, see docs/amq-0.73.0-adoption-verdict.md section 3). It stays
// correct even if ScopedPreauthGrant is ever edited to something unsafe,
// since it never trusts that the equality check alone is sufficient.
func isSafeScopedGrant(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || value == "Bash" || strings.HasPrefix(value, "-") {
		return false
	}
	return strings.Contains(value, "(")
}
