package cli

import (
	"errors"
	"flag"
	"strings"
)

// Exit-code taxonomy for amq-squad (epic #31, Step 11D):
//
//	0 ExitSuccess - success.
//	1 ExitUser    - user/usage error (UsageError, unknown flag, bad input).
//	2 ExitSystem  - system/runtime error (IO/process/env failure, panics
//	                surfaced as errors, anything the user cannot fix by
//	                changing arguments).
//	3 ExitPartial - partial success (PartialError: some targets succeeded,
//	                some failed; e.g. `stop` with mixed stopped + failed).
//
// Bump only on a breaking change; callers (CI scripts, dashboards) should
// be able to rely on these constants across 1.x.
const (
	ExitSuccess = 0
	ExitUser    = 1
	ExitSystem  = 2
	ExitPartial = 3
	// verify action uses stable policy exit codes so wrappers can distinguish
	// an unresolved gate from an explicit denial without parsing stderr.
	ExitActionPending = 10
	ExitActionDenied  = 11
	ExitActionNoGate  = 12
	ExitActionUnbound = 13
)

// PartialError signals partial success: the command made progress on some
// targets and explicitly reported per-target failure for the rest. main
// maps it to ExitPartial so wrapper scripts can tell "all-failed" (system
// error) from "mixed" (partial). Cause, when non-nil, lets callers attach
// the underlying error so errors.Is/As traversal keeps working.
type PartialError struct {
	Message string
	Cause   error
}

func (e *PartialError) Error() string { return e.Message }

// Unwrap returns the wrapped cause, if any, so errors.Is/As reach it.
func (e *PartialError) Unwrap() error { return e.Cause }

type ActionDecisionError struct {
	Decision string
	Message  string
}

func (e *ActionDecisionError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return "action authorization " + e.Decision
}

// ExitCode classifies err into the amq-squad exit-code taxonomy. nil is
// success; PartialError is partial success; UsageError is user error;
// anything else is a system/runtime error.
//
// PartialError is checked BEFORE UsageError so an outer PartialError that
// happens to wrap a UsageError (e.g. one target failed because of a usage
// problem on its row) still classifies as ExitPartial. The outer error
// type signals the operator's intent; wrapping a user-error cause must not
// flip the whole command to "user error".
func ExitCode(err error) int {
	if err == nil {
		return ExitSuccess
	}
	var pe *PartialError
	if errors.As(err, &pe) {
		return ExitPartial
	}
	var ade *ActionDecisionError
	if errors.As(err, &ade) {
		switch ade.Decision {
		case "pending":
			return ExitActionPending
		case "denied":
			return ExitActionDenied
		case "no_gate":
			return ExitActionNoGate
		case "unbound":
			return ExitActionUnbound
		}
		return ExitUser
	}
	var ue UsageError
	if errors.As(err, &ue) {
		return ExitUser
	}
	return ExitSystem
}

// parseFlags is the shared flag-parse helper. flag.ErrHelp bubbles up so
// Run can swallow it and exit 0; every other parse failure (unknown flag,
// malformed value) is wrapped as a UsageError so main exits via the user
// path.
func parseFlags(fs *flag.FlagSet, args []string) error {
	err := fs.Parse(args)
	if err == nil {
		return normalizePathFlags(fs)
	}
	if errors.Is(err, flag.ErrHelp) {
		return err
	}
	return UsageError(err.Error())
}

// commaSeparatedProjectFlagCommands names the flag sets whose --project is a
// comma-separated LIST of directories rather than a single directory, and which
// therefore cannot take single-path normalization.
//
// This exists as a named exemption rather than a "does the value contain a
// comma" heuristic on purpose: a heuristic would silently skip normalization
// for any legitimate directory whose name contains a comma, which is exactly
// the kind of representation-dependent behavior #539/#540 are about.
// This map is load-bearing: adding a command whose --project is a directory LIST
// without registering it here silently corrupts that flag. TestEveryCommaSeparatedProjectFlagIsExempt
// enumerates the flag registrations and fails when one is missing, so the next
// such command cannot be missed by inspection alone.
var commaSeparatedProjectFlagCommands = map[string]bool{
	// Each of these documents --project as "comma-separated project directories
	// to scan" and splits the value itself.
	"list":    true,
	"history": true,
	"restore": true,
}

// normalizePathFlags rewrites filesystem-path flags to their canonical
// representation immediately after parsing, before any command body can read
// them, so a path can never enter a prepared record, a launch record, or an
// identity tuple in the representation the operator happened to type.
//
// This is the single choke point for #540: `--project .` and
// `--project /abs/path` become the same recorded bytes here, which also fixes
// the #539 symptom downstream because tool_policy_sources entries are built
// with filepath.Join(project, ...).
//
// Only --project is normalized. --target-project is a cross-project AMQ project
// NAME, not a directory, and normalizing it would corrupt the routing key.
func normalizePathFlags(fs *flag.FlagSet) error {
	if commaSeparatedProjectFlagCommands[fs.Name()] {
		return nil
	}
	f := fs.Lookup("project")
	if f == nil {
		return nil
	}
	// Only rewrite an explicitly supplied value. An unset --project means
	// "default to cwd", and each command resolves that itself; forcing a value
	// in here would turn an absent flag into a present one and change
	// flagWasSet-driven behavior.
	if !flagWasSet(fs, "project") {
		return nil
	}
	raw := strings.TrimSpace(f.Value.String())
	if raw == "" {
		return nil
	}
	// Recording normalization: absolute, not symlink-resolved. The operator's
	// chosen location is preserved; comparators canonicalize further.
	normalized := absoluteFilesystemPath(raw)
	if normalized == "" || normalized == raw {
		return nil
	}
	if err := f.Value.Set(normalized); err != nil {
		return UsageError("resolve --project " + raw + ": " + err.Error())
	}
	return nil
}
