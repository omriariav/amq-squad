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

// SeatWake is a seat's AMQ wake policy by runtime (3.0.1). Claude Code and
// Codex seats require the wake that amq coop exec starts beside the
// provider (internal/launch/commands.go:135-150 maps it to --require-wake
// and --wake-inject-mode), typing raw: AMQ's own choice for both harnesses,
// whose Ink input drops a bracketed-paste Enter
// (internal/harness/harness.go:61-64, 81-82). claude-native is not open to
// a seat: it refuses a wrapper and a custom CLAUDE_CONFIG_DIR
// (internal/cli/wake_claude_native_unix.go:56-60), and every seat runs
// behind seat-exec. A pi seat keeps its wake disabled (PiWakeAuditReason).
func SeatWake(runtime string) (require bool, injector *launchapi.InjectorOptionsV1, auditReason string) {
	switch runtime {
	case "claude", "codex":
		return true, &launchapi.InjectorOptionsV1{Mode: launchapi.InjectorRaw}, ""
	}
	return false, nil, PiWakeAuditReason
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
// checks it against the compiled request. A seat the preview does not list,
// or lists without execution options, has no facts (an older AMQ); a listed
// seat whose admitted wake differs from the compiled one refuses the launch.
func admittedWakes(req launchapi.PrepareRequestV1, preview launchapi.PreviewV1) (map[string]*SeatWakeFacts, error) {
	want := map[string]*launchapi.ExecutionOptionsV1{}
	for _, p := range req.Intent.Participants {
		if p.Runnable && p.Execution != nil {
			want[p.Handle] = p.Execution
		}
	}
	out := map[string]*SeatWakeFacts{}
	for _, p := range preview.Participants {
		w, ok := want[p.Handle]
		if !ok || p.Execution == nil {
			continue
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
		requireWake, injector, wakeReason := SeatWake(s.Runtime)
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
