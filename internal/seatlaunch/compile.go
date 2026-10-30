// Package seatlaunch is the 3.0 launch compiler and runner (plan 4.3, R3 to
// R5, R48 to R52): a typed seat list compiles to one AMQ launch request in
// which every seat runs behind `amq-squad seat-exec --seat <handle>`. The
// Go core stages each seat's launch record and role.md under the session
// root before the launch; seat-exec verifies them at exec time.
package seatlaunch

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/avivsinai/agent-message-queue/launchapi"

	"github.com/omriariav/amq-squad/v3/internal/claudeseat"
	"github.com/omriariav/amq-squad/v3/internal/hostcheck"
	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/launchintent"
	"github.com/omriariav/amq-squad/v3/internal/registry"
	"github.com/omriariav/amq-squad/v3/internal/seatexec"
)

// Seat is one typed seat. squad.yaml (2b-5) compiles to this list.
type Seat struct {
	Handle  string `json:"handle"`
	Runtime string `json:"runtime"` // claude | codex | pi
	Model   string `json:"model,omitempty"`
	Effort  string `json:"effort,omitempty"`
	Role    string `json:"-"` // role.md text; required
	// RoleName is the seat's role id (lead, implementer, ...); the launch
	// record carries it, and status joins it into the seat row.
	RoleName string   `json:"role,omitempty"`
	Worktree string   `json:"worktree"`
	Branch   string   `json:"branch,omitempty"`
	EnvNeeds []string `json:"env_needs,omitempty"`
	// CredentialNeeds are the credential names the registry entry declares
	// (the only way a scrubbed credential reaches a seat; fix 3 item 6).
	CredentialNeeds []string `json:"credential_needs,omitempty"`
	Resume          bool     `json:"resume,omitempty"`
	// InitialInput is extra first-turn text. It may point at files; it may
	// never carry the role (plan 4.3, amendment 2).
	InitialInput string `json:"initial_input,omitempty"`
	// Provider is the provider executable; empty resolves the runtime on PATH.
	Provider string `json:"provider,omitempty"`
	// RequestID is the scheduler's SeatRequest.ID; it travels in the launch
	// record and the seat environment (AMQ_SQUAD_REQUEST_ID) and comes back
	// in the seat's SeatUp event.
	RequestID string `json:"request_id,omitempty"`

	// roleInFirstMessage is the role-delivery matrix's control arm only
	// (plan 13, amendment 15): the role text rides in initial_input instead
	// of the pointer. Unexported, so no squad file or caller can set it.
	roleInFirstMessage bool
}

// Squad is one launch: one AMQ session, one request.
type Squad struct {
	ProjectRoot string `json:"project_root"`
	BaseRoot    string `json:"base_root"` // default <project>/.agent-mail
	Session     string `json:"session"`
	Operator    string `json:"operator,omitempty"` // default user
	TeamHome    string `json:"team_home,omitempty"`
	Profile     string `json:"profile,omitempty"`
	RulesPath   string `json:"rules_path,omitempty"`
	Launcher    string `json:"launcher,omitempty"` // tmux (default), or another AMQ launcher
	Seats       []Seat `json:"seats"`
	// Wrapper is the absolute amq-squad executable; AMQPath the real amq (R51).
	Wrapper string `json:"-"`
	AMQPath string `json:"-"`
	// ClaudePins is the registry's claude pin of each Claude seat, by
	// handle, resolved by the launch: a Claude seat's executable is its pin,
	// never a provider path (the narrow pass's HIGH: the probe certifies the
	// bytes the seat runs).
	ClaudePins map[string]registry.Pin `json:"-"`
	// PiShim is the pi launch shim (piruntime.EnsureShim) the launch wrote
	// for this binary: every pi seat's executable, the file AMQ's pi adapter
	// probes; seat-exec runs the verified embedded runtime in its place.
	PiShim string `json:"-"`
	// WakeConditions are the launch's reads of each seat's wake conditions
	// (3.0.1), by handle. Never from the seat file; a seat with no entry
	// has none met, so its wake stays off.
	WakeConditions map[string]SeatWakeConditions `json:"-"`
}

// ReasonProviderNotPinned refuses a Claude seat whose provider is not the
// file the registry pins.
const ReasonProviderNotPinned = "provider_not_pinned"

// Runtimes the compiler knows. pi admits no provider arguments (amendment
// 23 (b)); its model restriction stays on the registry side (R16).
var Runtimes = []string{"claude", "codex", "pi"}

// ReasonPiShimMissing refuses a pi seat when the launch named no pi shim, or
// a provider that is not the shim (R2: never a pi from PATH).
const ReasonPiShimMissing = "pi_not_embedded"

// ClaudePermissionMode is the permission mode every Claude seat is launched
// with, recorded in its launch record and checked by seat-exec together with
// the user-scope hook plugin (plan 4.3, R36). dontAsk: guard's explicit allow
// answers every hooked call; a call nothing approved, such as one whose hook
// failed open (timeout, exit 1, missing command), is denied rather than
// prompted, so an unattended seat neither stalls nor runs a call unguarded.
// AMQ v0.107.0 and later admit acceptEdits, auto, manual, dontAsk and plan. Explicit,
// so the owner's settings.json defaultMode never decides it.
const ClaudePermissionMode = "dontAsk"

// CodexSandboxArgs are the Codex sandbox and approval flags (plan 4.3, 7.3).
var CodexSandboxArgs = []string{"--sandbox", "workspace-write", "--ask-for-approval", "on-request"}

// WakeRule is the 3.0.1 wake rule: the scheduler's doorbell is the seat's
// next turn. A seat that learns of a task only from an AMQ message in its
// inbox idles after its first turn unless an AMQ wake turns that message
// into terminal input (found 2026-10-30, session v31, seat dev). AMQ's
// grammar forbids an audit reason on an enabled wake
// (internal/launch/execution_options_grammar.go:35-38 at v0.116.2), so the
// rule lives here and in the execution note, not in the launch request.
const WakeRule = "amq-squad 3.0.1: the scheduler's doorbell is the seat's next turn"

// PiWakeAuditReason is a pi seat's disabled-wake audit reason. AMQ's coop
// exec would wake a pi seat too (paste, internal/harness/harness.go:85), but
// its dialog guard runs only when pi loads AMQ's pi extension
// (docs/wake-operations.md:587-599), which the embedded runtime does not, so
// a doorbell could answer pi's folder-trust or an extension prompt.
const PiWakeAuditReason = "amq-squad 3.0.1: a pi seat has no AMQ wake until its embedded runtime loads AMQ's pi extension (the doorbell's dialog guard)"

// SeatWakeConditions are the fail-closed conditions of a seat's AMQ wake
// (3.0.1, the reviewer's ruling), read by the launch. The zero value meets
// none of them. Each Why names the unmet condition by file and key only.
type SeatWakeConditions struct {
	// Claude Code: the seat's config dir runs the guard that denies
	// AskUserQuestion and ExitPlanMode (seatexec.ClaudeDialogGuardWired) ...
	ClaudeGuardWired bool
	ClaudeGuardWhy   string
	// ... and rules out every first-run screen
	// (claudeseat.StartupScreensClosed; false until t6).
	ClaudeScreensClosed bool
	ClaudeScreensWhy    string
	// Codex: AMQ's Codex dialog hook entries are on the host
	// (seatexec.ReadCodexDialogHook) ...
	CodexHookReady bool
	CodexHookWhy   string
	// ... and the Codex config trusts the seat's worktree
	// ([projects."<worktree>"] trust_level = "trusted", hostcheck).
	CodexTrusted  bool
	CodexTrustWhy string
}

// ClaudeNoWake and CodexNoWake begin a Claude Code or Codex seat's
// disabled-wake audit reason; the unmet condition follows.
const (
	ClaudeNoWake = "amq-squad 3.0.1: no AMQ wake for this Claude Code seat: "
	CodexNoWake  = "amq-squad 3.0.1: no AMQ wake for this Codex seat: "
)

// SeatWake is a seat's AMQ wake policy (3.0.1). Raw typing into a pane
// means a doorbell that lands on a prompt or a dialog answers it
// unattended, so the wake is required only when every condition of the
// seat's runtime holds; an unmet condition keeps the wake off with the
// named audit reason, never a launch failure (the seat runs and is kicked
// by hand, as in 3.0):
//   - Claude Code: permission mode dontAsk (no permission prompt); the
//     guard denies AskUserQuestion and ExitPlanMode in the seat's config
//     dir; the startup screens are closed (claudeseat, t6).
//   - Codex: AMQ's Codex dialog hook on the host (it holds the doorbell at
//     an approval prompt); the Codex config trusts the seat's worktree.
//   - pi: off (PiWakeAuditReason).
//
// An enabled wake is the one amq coop exec starts beside the provider
// (internal/launch/commands.go:135-150 maps it to --require-wake and
// --wake-inject-mode), typing raw: AMQ's own choice for both harnesses,
// whose Ink input drops a bracketed-paste Enter
// (internal/harness/harness.go:61-64, 81-82). claude-native is not open to
// a seat: it refuses a wrapper and a custom CLAUDE_CONFIG_DIR
// (internal/cli/wake_claude_native_unix.go:56-60), and every seat runs
// behind seat-exec.
func SeatWake(runtime, permissionMode string, c SeatWakeConditions) (require bool, injector *launchapi.InjectorOptionsV1, auditReason string) {
	raw := &launchapi.InjectorOptionsV1{Mode: launchapi.InjectorRaw}
	switch runtime {
	case "claude":
		switch {
		case permissionMode != "dontAsk":
			return false, nil, ClaudeNoWake + "permission mode " + permissionMode + " can prompt, and the seat's config dir carries no AMQ dialog hook"
		case !c.ClaudeGuardWired:
			return false, nil, ClaudeNoWake + "the guard does not deny AskUserQuestion and ExitPlanMode in the seat's config dir (" + c.ClaudeGuardWhy + ")"
		case !c.ClaudeScreensClosed:
			return false, nil, ClaudeNoWake + "the startup screens are not verified closed (" + c.ClaudeScreensWhy + ")"
		}
		return true, raw, ""
	case "codex":
		switch {
		case !c.CodexHookReady:
			return false, nil, CodexNoWake + "AMQ's Codex dialog hook is not on the host (" + c.CodexHookWhy + "); fix: " + seatexec.CodexDialogHookFix
		case !c.CodexTrusted:
			return false, nil, CodexNoWake + "the Codex config does not trust the seat's worktree (" + c.CodexTrustWhy + ")"
		}
		return true, raw, ""
	}
	return false, nil, PiWakeAuditReason
}

// readWakeConditions reads every seat's wake conditions through the launch's
// read seams. The Codex reads run once, only for a squad with a Codex seat,
// on the Codex home the seats read ($CODEX_HOME of the launch environment,
// else <home>/.codex; seat-exec overrides neither).
func readWakeConditions(q Squad, o Options) map[string]SeatWakeConditions {
	out := map[string]SeatWakeConditions{}
	var hook *seatexec.CodexDialogHook
	var trust *hostcheck.CodexTrust
	var trustErr error
	codexDir := seatexec.CodexDir(o.Home, seatexec.Getenv(o.Env, "CODEX_HOME"))
	for _, s := range q.Seats {
		var c SeatWakeConditions
		switch s.Runtime {
		case "claude":
			c = o.ClaudeWakeConditions(permissionMode(s.Runtime), s.Worktree)
		case "codex":
			if hook == nil {
				h := o.CodexDialogHook(codexDir)
				hook = &h
				t, err := o.CodexTrust(filepath.Join(codexDir, "config.toml"))
				trust, trustErr = &t, err
			}
			c.CodexHookReady, c.CodexHookWhy = hook.Ready(), codexHookWhy(*hook)
			c.CodexTrusted, c.CodexTrustWhy = codexTrusts(*trust, trustErr, filepath.Join(codexDir, "config.toml"), s.Worktree)
		}
		out[s.Handle] = c
	}
	return out
}

// codexHookWhy names what the hook read found missing (names only).
func codexHookWhy(h seatexec.CodexDialogHook) string {
	switch {
	case len(h.HooksOff) > 0:
		return strings.Join(h.HooksOff, " and ") + " is false in " + h.ConfigPath
	case h.Unreadable != "":
		return h.HooksPath + ": " + h.Unreadable
	case len(h.Missing) > 0:
		return "no AMQ_DIALOG_HOOK entry for " + strings.Join(h.Missing, ", ") + " in " + h.HooksPath
	}
	return ""
}

// codexTrusts reports whether the Codex config has the trusted entry for the
// worktree (as given, or with symlinks resolved), the read doctor's "codex
// trust" row makes; key names only.
func codexTrusts(ct hostcheck.CodexTrust, err error, configPath, worktree string) (bool, string) {
	if err != nil {
		return false, "cannot read " + configPath
	}
	roots := []string{filepath.Clean(worktree)}
	if real, rerr := filepath.EvalSymlinks(worktree); rerr == nil && real != roots[0] {
		roots = append(roots, real)
	}
	for _, r := range roots {
		if len(ct.UntrustedRoots([]string{r})) == 0 {
			return true, ""
		}
	}
	return false, "no [projects.\"" + roots[0] + "\"] trust_level = \"trusted\" in " + configPath + "; 'amq-squad trust " + roots[0] + "' prints the owner's steps"
}

// scratchClaudeWakeConditions writes the config dir claudeseat.Write
// produces for a seat into a scratch directory, reads the Claude wake
// conditions there, and removes it. The conditions depend only on Write's
// inputs (mode, plugin files, seed), so the seat's own dir, written after
// Prepare, carries the same answer; the scratch copy keeps a dry run and a
// launch stopped at its decisions from staging anything.
func scratchClaudeWakeConditions(permissionMode, worktree string) SeatWakeConditions {
	var c SeatWakeConditions
	tmp, err := os.MkdirTemp("", "amq-squad-claude-wake-")
	if err != nil {
		c.ClaudeGuardWhy, c.ClaudeScreensWhy = "scratch config dir: "+err.Error(), "scratch config dir: "+err.Error()
		return c
	}
	defer os.RemoveAll(tmp)
	dir := filepath.Join(tmp, "config")
	if _, err := claudeseat.Write(dir, permissionMode, worktree, time.Unix(0, 0)); err != nil {
		c.ClaudeGuardWhy, c.ClaudeScreensWhy = "scratch config dir: "+err.Error(), "scratch config dir: "+err.Error()
		return c
	}
	c.ClaudeGuardWired, c.ClaudeGuardWhy = seatexec.ClaudeDialogGuardWired(dir)
	c.ClaudeScreensClosed, c.ClaudeScreensWhy = claudeseat.StartupScreensClosed(dir)
	return c
}

// ReasonWakeNotAdmitted refuses a launch whose Prepare preview shows a seat
// with another wake than the one compiled: a Claude Code or Codex seat
// without its wake idles after its first turn (WakeRule).
const ReasonWakeNotAdmitted = "wake_not_admitted"

// SeatWakeFacts is a seat's wake as AMQ reports it: Prepare's echo of the
// participant's execution options (launchapi/prepare.go:200-203,
// fromInternalExecutionOptions at :249 in v0.116.2). AMQ's ApplyResult and
// its observations carry no wake field, and the wake process itself is
// started by amq coop exec in the seat's pane, bound to the coop exec pid
// that seat-exec and the provider keep by exec; it exits with the seat, so
// a seat's close needs no wake close of its own.
type SeatWakeFacts struct {
	RequireWake bool   `json:"require_wake"`
	Mode        string `json:"mode"`               // enabled | disabled
	Injector    string `json:"injector,omitempty"` // raw | paste | auto | none | claude-native
	AuditReason string `json:"audit_reason,omitempty"`
	Source      string `json:"source"` // always "amq_prepare_preview"
}

// admittedWakes reads each runnable seat's wake from the Prepare preview and
// checks it against the compiled request. Every seat needs that evidence: a
// seat the preview does not list, or lists without execution options, or
// whose admitted wake differs from the compiled one, refuses the launch
// before Apply.
func admittedWakes(req launchapi.PrepareRequestV1, preview launchapi.PreviewV1) (map[string]*SeatWakeFacts, error) {
	want := map[string]*launchapi.ExecutionOptionsV1{}
	var order []string
	for _, p := range req.Intent.Participants {
		if p.Runnable && p.Execution != nil {
			want[p.Handle] = p.Execution
			order = append(order, p.Handle)
		}
	}
	out := map[string]*SeatWakeFacts{}
	for _, p := range preview.Participants {
		w, ok := want[p.Handle]
		if !ok {
			continue
		}
		// The pinned AMQ echoes execution options for every runnable
		// participant (launchapi/prepare.go:200-203); none is no evidence.
		if p.Execution == nil {
			return nil, fmt.Errorf("seat %s: %s: AMQ's Prepare preview carries no execution options for the seat, so its wake is unverified (%s)", p.Handle, ReasonWakeNotAdmitted, WakeRule)
		}
		got := p.Execution
		facts := &SeatWakeFacts{RequireWake: got.RequireWake, Mode: string(got.Wake.Mode), AuditReason: got.Wake.AuditReason, Source: "amq_prepare_preview"}
		if got.Wake.Injector != nil {
			facts.Injector = string(got.Wake.Injector.Mode)
		}
		wantInjector := ""
		if w.Wake.Injector != nil {
			wantInjector = string(w.Wake.Injector.Mode)
		}
		if facts.RequireWake != w.RequireWake || facts.Mode != string(w.Wake.Mode) || facts.Injector != wantInjector {
			return nil, fmt.Errorf("seat %s: %s: AMQ admitted wake mode %q, require_wake %t, injector %q; the launch compiled mode %q, require_wake %t, injector %q (%s)",
				p.Handle, ReasonWakeNotAdmitted, facts.Mode, facts.RequireWake, orNone(facts.Injector), w.Wake.Mode, w.RequireWake, orNone(wantInjector), WakeRule)
		}
		out[p.Handle] = facts
	}
	for _, h := range order {
		if out[h] == nil {
			return nil, fmt.Errorf("seat %s: %s: AMQ's Prepare preview lists no participant for the seat, so its wake is unverified (%s)", h, ReasonWakeNotAdmitted, WakeRule)
		}
	}
	return out, nil
}

// RolePointer is the whole of the role's part in initial_input: a pointer.
const RolePointer = "Your seat role is in the file named by the environment variable AMQ_SQUAD_ROLE_PATH. Read it first and follow it."

var handlePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// ProviderArgs returns the provider arguments of a seat: model and effort
// (and Codex's sandbox and approval flags and its two sandbox pins) for
// Claude and Codex, none for pi.
func ProviderArgs(s Seat) []string {
	var args []string
	switch s.Runtime {
	case "claude":
		if s.Model != "" {
			args = append(args, "--model", s.Model)
		}
		if s.Effort != "" {
			args = append(args, "--effort", s.Effort)
		}
		args = append(args, "--permission-mode", ClaudePermissionMode)
	case "codex":
		if s.Model != "" {
			args = append(args, "--model", s.Model)
		}
		if s.Effort != "" {
			args = append(args, "-c", "model_reasoning_effort="+s.Effort)
		}
		args = append(args, CodexSandboxArgs...)
		// The sandbox pins as provider arguments (AMQ v0.110.0 admits both
		// keys, upstream ask 17), so admission and the trust digest cover
		// them; seat-exec checks them and adds none (brief 5-amq-A).
		args = append(args, seatexec.CodexSandboxPins(slices.Contains(s.EnvNeeds, seatexec.CodexNetworkNeed))...)
	}
	return args
}

// credentialSource is the launch record's note of a runtime's credential:
// a Claude seat's token comes from the owner's token file (never its value).
func credentialSource(runtime string) string {
	if runtime == "claude" {
		return claudeseat.CredentialSource
	}
	return ""
}

// permissionMode is the launch record's permission mode for a runtime.
func permissionMode(runtime string) string {
	if runtime == "claude" {
		return ClaudePermissionMode
	}
	return ""
}

// SessionRoot is <base root>/<session>.
func (q Squad) SessionRoot() string { return filepath.Join(q.BaseRoot, q.Session) }

// Normalize fills the defaults and checks the squad.
func (q Squad) Normalize() (Squad, error) {
	q.ProjectRoot = filepath.Clean(strings.TrimSpace(q.ProjectRoot))
	if !filepath.IsAbs(q.ProjectRoot) {
		return q, fmt.Errorf("project root %q must be absolute", q.ProjectRoot)
	}
	if q.BaseRoot == "" {
		q.BaseRoot = filepath.Join(q.ProjectRoot, ".agent-mail")
	}
	q.BaseRoot = filepath.Clean(q.BaseRoot)
	if !handlePattern.MatchString(q.Session) {
		return q, fmt.Errorf("session %q is not a valid AMQ session name", q.Session)
	}
	if q.Operator == "" {
		q.Operator = "user"
	}
	if q.Launcher == "" {
		q.Launcher = "tmux"
	}
	if q.TeamHome == "" {
		q.TeamHome = q.ProjectRoot
	}
	if !filepath.IsAbs(q.Wrapper) || !filepath.IsAbs(q.AMQPath) {
		return q, fmt.Errorf("the wrapper (%q) and the amq path (%q) must be absolute (R51)", q.Wrapper, q.AMQPath)
	}
	if len(q.Seats) == 0 {
		return q, fmt.Errorf("the squad has no seats")
	}
	seen := map[string]bool{q.Operator: true}
	for i, s := range q.Seats {
		if !handlePattern.MatchString(s.Handle) || seen[s.Handle] {
			return q, fmt.Errorf("seat %d: handle %q is invalid or repeated", i, s.Handle)
		}
		seen[s.Handle] = true
		if !slices.Contains(Runtimes, s.Runtime) {
			return q, fmt.Errorf("seat %s: runtime %q is not one of %v", s.Handle, s.Runtime, Runtimes)
		}
		if !filepath.IsAbs(s.Worktree) {
			return q, fmt.Errorf("seat %s: worktree %q must be absolute", s.Handle, s.Worktree)
		}
		if err := seatexec.ValidateNeeds(s.EnvNeeds, s.CredentialNeeds); err != nil {
			return q, fmt.Errorf("seat %s: %w", s.Handle, err)
		}
		if err := checkRoleDelivery(s); err != nil {
			return q, fmt.Errorf("seat %s: %w", s.Handle, err)
		}
	}
	return q, nil
}

// checkRoleDelivery: the role comes only from role.md; role text in
// initial_input without role.md is a malformed launch (plan 4.3).
func checkRoleDelivery(s Seat) error {
	if s.roleInFirstMessage && strings.TrimSpace(s.Role) != "" {
		return nil
	}
	if strings.TrimSpace(s.Role) == "" {
		if strings.TrimSpace(s.InitialInput) != "" {
			return fmt.Errorf("malformed launch: initial_input text without a role.md (the role comes only from role.md)")
		}
		return fmt.Errorf("no role: every seat needs role.md text")
	}
	in := launchintent.NormalizeGoalPrompt(s.InitialInput)
	for _, line := range strings.Split(s.Role, "\n") {
		if l := launchintent.NormalizeGoalPrompt(line); len(l) >= 24 && strings.Contains(in, l) {
			return fmt.Errorf("malformed launch: initial_input carries role text (%q); it may carry only a pointer to role.md", l)
		}
	}
	return nil
}

// Compiled is the launch request plus the staged records and roles.
type Compiled struct {
	Request launchapi.PrepareRequestV1
	Records []launch.Record
	Roles   [][]byte
}

// Compile turns a normalized squad into one PrepareRequestV1 and the staged
// records. rulesSHA is the digest of RulesPath ("" when there is none).
func Compile(q Squad, rulesSHA string, lookPath func(string) (string, error), now time.Time) (Compiled, error) {
	var out Compiled
	seats := make([]launchintent.SeatFacts, 0, len(q.Seats))
	for _, s := range q.Seats {
		provider := s.Provider
		if s.Runtime == "pi" {
			// The embedded runtime, through the shim the launch wrote; a
			// provider path (a pi from PATH) is refused (R2).
			if q.PiShim == "" {
				return out, fmt.Errorf("seat %s: %s: the launch wrote no pi launch shim for the embedded runtime", s.Handle, ReasonPiShimMissing)
			}
			if provider != "" && filepath.Clean(provider) != q.PiShim {
				return out, fmt.Errorf("seat %s: %s: the provider %s is not the embedded runtime's shim %s (a pi seat never runs a pi from PATH)", s.Handle, ReasonPiShimMissing, provider, q.PiShim)
			}
			provider = q.PiShim
		}
		if s.Runtime == "claude" {
			pin, ok := q.ClaudePins[s.Handle]
			if !ok || pin.Path == "" {
				return out, fmt.Errorf("seat %s: %s: the registry pins no claude (registry.yaml pins: name claude, its real path and sha256)", s.Handle, registry.PinMissing)
			}
			if filepath.Base(pin.Path) != "claude" {
				// AMQ selects the provider by the executable's file name, and
				// seat-exec checks it: pin a hard link named claude to the
				// versioned binary (a symlink is refused by the pin's walk).
				return out, fmt.Errorf("seat %s: %s: the pinned claude %s must have the file name claude (pin a hard link named claude)", s.Handle, registry.PinMissing, pin.Path)
			}
			if provider != "" && !sameFile(provider, pin.Path) {
				return out, fmt.Errorf("seat %s: %s: the provider %s is not the pinned claude %s", s.Handle, ReasonProviderNotPinned, provider, pin.Path)
			}
			provider = pin.Path
		}
		if provider == "" {
			p, err := lookPath(s.Runtime)
			if err != nil {
				return out, fmt.Errorf("seat %s: %s is not on PATH: %w", s.Handle, s.Runtime, err)
			}
			provider = p
		}
		input := ""
		if s.Runtime != "pi" {
			input = strings.TrimSpace(RolePointer + " " + launchintent.NormalizeGoalPrompt(s.InitialInput))
		}
		if s.roleInFirstMessage {
			input = launchintent.NormalizeGoalPrompt(s.Role)
		}
		policy := launchapi.ResumePolicyFresh
		if s.Resume {
			policy = launchapi.ResumePolicyResume
		}
		requireWake, injector, wakeReason := SeatWake(s.Runtime, permissionMode(s.Runtime), q.WakeConditions[s.Handle])
		seats = append(seats, launchintent.SeatFacts{
			Handle: s.Handle, Executable: provider, Args: ProviderArgs(s),
			Wrapper:         &launchapi.WrapperV1{Executable: q.Wrapper, Args: []string{"seat-exec", "--seat", s.Handle}},
			Cwd:             launchintent.SeatCWD{Kind: launchapi.WorkingDirectoryAbsolute, Path: s.Worktree},
			ResumePolicy:    policy,
			BootstrapPrompt: input,
			NoGitignore:     true,
			RequireWake:     requireWake,
			Injector:        injector,
			WakeAuditReason: wakeReason,
		})
		role := []byte(s.Role)
		out.Roles = append(out.Roles, role)
		out.Records = append(out.Records, launch.Record{
			Handle: s.Handle, Root: q.SessionRoot(), BaseRoot: q.BaseRoot, Session: q.Session,
			Runtime: s.Runtime, Model: s.Model, Effort: s.Effort, RoleSHA256: digest(role),
			RulesPath: q.RulesPath, RulesSHA256: rulesSHA, Branch: s.Branch, Worktree: s.Worktree,
			AMQPath: q.AMQPath, EnvNeeds: s.EnvNeeds, CredNeeds: s.CredentialNeeds, Resume: s.Resume, RequestID: s.RequestID, PermissionMode: permissionMode(s.Runtime), Credential: credentialSource(s.Runtime),
			CWD: s.Worktree, Binary: s.Runtime, Role: s.RoleName, TeamHome: q.TeamHome, TeamProfile: q.Profile, StartedAt: now.UTC(),
			PiShim: piShimFor(s, q),
		})
	}
	intent, target, err := launchintent.Compile(launchintent.Input{
		Operator: launchintent.OperatorFacts{Handle: q.Operator},
		Seats:    seats,
		Target:   launchintent.TargetFacts{ProjectRoot: q.ProjectRoot, BaseRoot: q.BaseRoot, SessionRoot: q.SessionRoot(), Session: q.Session},
	})
	if err != nil {
		return out, err
	}
	out.Request = launchapi.PrepareRequestV1{
		RequestVersion: launchapi.RequestVersionV1, Target: target, Launcher: q.Launcher, Intent: intent,
		CallerContext: map[string]string{"caller": "amq-squad", "session": q.Session},
	}
	return out, nil
}

// piShimFor is the record's shim path: the launch's shim for a pi seat.
func piShimFor(s Seat, q Squad) string {
	if s.Runtime == "pi" {
		return q.PiShim
	}
	return ""
}

// sameFile reports whether two paths name one file (device and inode).
func sameFile(a, b string) bool {
	fa, errA := os.Stat(a)
	fb, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(fa, fb)
}

// CheckArgumentForms checks every seat's provider arguments against the
// forms AMQ advertises (v0.106.2 and later derive them from the validation
// tables); amq-squad keeps no list of forms of its own (plan 4.3).
func CheckArgumentForms(q Squad, caps []launchapi.ProviderCapabilitiesV1) error {
	byProvider := map[string]launchapi.ProviderCapabilitiesV1{}
	for _, c := range caps {
		byProvider[c.Provider] = c
	}
	for _, s := range q.Seats {
		args := ProviderArgs(s)
		if len(args) == 0 {
			continue
		}
		c, ok := byProvider[s.Runtime]
		if !ok {
			return fmt.Errorf("seat %s: AMQ advertised no argument forms for %s", s.Handle, s.Runtime)
		}
		for i := 0; i < len(args); i++ {
			if !strings.HasPrefix(args[i], "-") {
				continue
			}
			if !slices.Contains(c.AllowedArgumentForms, args[i]) {
				return fmt.Errorf("seat %s: AMQ does not admit %s for %s (admitted: %v)", s.Handle, args[i], s.Runtime, c.AllowedArgumentForms)
			}
			if args[i] == "-c" && i+1 < len(args) {
				key, value, _ := strings.Cut(args[i+1], "=")
				if !overrideAllowed(c.ConfigOverrides, key, value) {
					return fmt.Errorf("seat %s: AMQ does not admit -c %s for %s", s.Handle, args[i+1], s.Runtime)
				}
			}
		}
	}
	return nil
}

func overrideAllowed(overrides []launchapi.ConfigOverrideCapabilityV1, key, value string) bool {
	for _, o := range overrides {
		if o.Key == key {
			return len(o.AllowedValues) == 0 || slices.Contains(o.AllowedValues, value)
		}
	}
	return false
}

// Stage writes each seat's record and role.md into launch.StagedDir under
// the session root (0700 directories, 0600 files). The session root must
// exist: AMQ never creates an unknown session.
func Stage(c Compiled) error {
	for i, rec := range c.Records {
		if st, err := os.Stat(rec.Root); err != nil || !st.IsDir() {
			return fmt.Errorf("session root %s does not exist; create the session first (amq session create %s --root %s --me <operator>)", rec.Root, rec.Session, rec.BaseRoot)
		}
		dir := launch.StagedDir(rec.Root, rec.Handle)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		if err := writeAtomic(filepath.Join(dir, launch.RoleFileName), c.Roles[i]); err != nil {
			return err
		}
		if err := launch.WriteFile(filepath.Join(dir, launch.FileName), rec); err != nil {
			return err
		}
	}
	return nil
}

func writeAtomic(path string, data []byte) error {
	tmp := fmt.Sprintf("%s.%d.tmp", path, os.Getpid())
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func digest(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// FileDigest is the sha256 of a file, "" for an empty path.
func FileDigest(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return digest(b), nil
}
