// Package launchintent compiles an already-resolved team profile, session,
// and per-seat runtime facts into AMQ's public launchapi.LaunchIntentV1 and
// launchapi.TargetV1. Compile is a pure function: it does no launching, no
// filesystem or AMQ I/O, and no side effects. Every fact it needs (worktree
// cwd, bootstrap prompt text, resolved native args) is resolved by the
// caller and handed in; this package only shapes that data into the
// contract types and applies the two argv guarantees measured against
// released AMQ v0.70.0 (see gh#732): no --allowedTools token on any seat,
// and approvals_reviewer dropped from the new path's argv.
package launchintent

import (
	"fmt"
	"strings"

	"github.com/avivsinai/agent-message-queue/launchapi"
)

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

	participant := launchapi.ParticipantV1{
		Handle:       handle,
		Runnable:     true,
		Executable:   executable,
		Args:         sanitizeNewPathArgs(seat.Args),
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

// sanitizeNewPathArgs drops the two argv forms gh#732 measured as unsafe to
// carry on the new path (see the package doc comment): --allowedTools (both
// the equals-joined and two-token spellings) and -c approvals_reviewer=...
// (Codex's trust-mode reviewer override). Nothing else is touched, so a
// caller's model/effort/permission-mode args pass through unchanged. The
// legacy composer (internal/cli/agent_defaults.go) is untouched by this
// package and keeps emitting both on its own path.
func sanitizeNewPathArgs(args []string) []string {
	if len(args) == 0 {
		return nil
	}
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--allowedTools":
			// Two-token spelling: also drop the value that follows it, if any.
			if i+1 < len(args) {
				i++
			}
			continue
		case strings.HasPrefix(arg, "--allowedTools="):
			continue
		case arg == "-c" && i+1 < len(args) && strings.HasPrefix(args[i+1], "approvals_reviewer="):
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
