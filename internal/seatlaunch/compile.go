// Package seatlaunch is the 3.0 launch compiler and runner (plan 4.3, R3 to
// R5, R48 to R52): a typed seat list compiles to one AMQ launch request in
// which every seat runs behind `amq-squad seat-exec --seat <handle>`. The
// Go core stages each seat's launch record and role.md under the session
// root before the launch; seat-exec verifies them at exec time.
package seatlaunch

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	// Base is the base branch's tip the scheduler put the worktree and
	// branch on at this launch (3.0.2 t22); the launch record carries it.
	// Never from the seat file.
	Base string `json:"-"`

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
	// PiShim is the pi launch shim (piruntime.EnsureShim) the launch wrote
	// for this binary: every pi seat's executable, the file AMQ's pi adapter
	// probes; seat-exec runs the verified embedded runtime in its place.
	PiShim string `json:"-"`
	// CodexHookMissing is why AMQ's Codex dialog hook is not on the host,
	// "" when it is (the launch reads it once for a squad with a Codex
	// seat): a Codex seat's wake needs it (Q3 of brief 31-1). Never from
	// the seat file.
	CodexHookMissing string `json:"-"`
}

// Runtimes the compiler knows: the providers AMQ v0.116.2's launch adapters
// serve (internal/launch/adapter.go:21-36 there; brief 31-1, Q4). pi admits
// no provider arguments (amendment 23 (b)); its model reaches the embedded
// runtime's launcher (seatexec.PiLauncherArgv).
var Runtimes = []string{"claude", "codex", "cursor-agent", "grok", "pi"}

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

// CodexNoWake begins a Codex seat's disabled-wake audit reason.
const CodexNoWake = "amq-squad: no AMQ wake for this Codex seat: "

// SeatWake is a seat's AMQ wake policy (brief 31-1): on for Claude Code; on
// for Codex when AMQ's Codex dialog hook is on the host (codexHookMissing
// is ""), because a doorbell at a Codex approval prompt cancels the pending
// command (AMQ docs/wake-operations.md:460-461 at v0.116.2), else off with
// the reason; off for pi (PiWakeAuditReason) and for any other runtime. The wake never refuses a
// launch: a seat without one idles after its first turn, a line on the
// screen. An enabled wake is the one amq coop exec
// starts beside the provider (internal/launch/commands.go:135-150 at
// v0.116.2 maps it to --require-wake and --wake-inject-mode), typing raw:
// AMQ's own choice for both harnesses, whose Ink input drops a
// bracketed-paste Enter (internal/harness/harness.go:61-64, 81-82).
// claude-native is not open to a seat: it refuses a wrapper and a custom
// CLAUDE_CONFIG_DIR (internal/cli/wake_claude_native_unix.go:56-60), and
// every seat runs behind seat-exec. A Claude seat rung at a dialog under
// dontAsk can stall there: a stalled seat is a status line with its state,
// never a refusal (plan 7.2 (b) item 5).
func SeatWake(runtime, codexHookMissing string) (require bool, injector *launchapi.InjectorOptionsV1, auditReason string) {
	raw := &launchapi.InjectorOptionsV1{Mode: launchapi.InjectorRaw}
	switch runtime {
	case "claude":
		return true, raw, ""
	case "codex":
		if codexHookMissing != "" {
			return false, nil, CodexNoWake + "AMQ's Codex dialog hook is not on the host (" + codexHookMissing + "); fix: " + seatexec.CodexDialogHookFix
		}
		return true, raw, ""
	case "pi":
		return false, nil, PiWakeAuditReason
	}
	return false, nil, "amq-squad: no AMQ wake for a " + runtime + " seat (amq-squad wakes Claude Code and Codex seats only)"
}

// codexHookWhy names what the hook read found missing (names only), "" when
// the hook is ready.
func codexHookWhy(h seatexec.CodexDialogHook) string {
	switch {
	case h.Ready():
		return ""
	case len(h.HooksOff) > 0:
		return strings.Join(h.HooksOff, " and ") + " is false in " + h.ConfigPath
	case h.Unreadable != "":
		return h.HooksPath + ": " + h.Unreadable
	}
	return "no AMQ_DIALOG_HOOK entry for " + strings.Join(h.Missing, ", ") + " in " + h.HooksPath
}

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

// WakeFileName is a seat's admitted wake beside its staged record
// (launch.StagedDir): AMQ's Prepare echo of the seat's wake, written at each
// launch with its launch id, so status shows the wake a seat runs with
// (brief 31-1: SeatState.Wake reaches only Launch's caller).
const WakeFileName = "wake.json"

// SeatWakeRecord is the content of WakeFileName: Wake is nil when AMQ's
// preview carried no execution options for the seat.
type SeatWakeRecord struct {
	LaunchID string         `json:"launch_id"`
	Wake     *SeatWakeFacts `json:"wake"`
}

// writeWakes records each staged seat's admitted wake (best effort: a seat
// whose record cannot be written shows no wake in status, nothing else).
func writeWakes(c Compiled, launchID string, wakes map[string]*SeatWakeFacts) {
	for _, rec := range c.Records {
		b, err := json.Marshal(SeatWakeRecord{LaunchID: launchID, Wake: wakes[rec.Handle]})
		if err != nil {
			continue
		}
		_ = writeAtomic(filepath.Join(launch.StagedDir(rec.Root, rec.Handle), WakeFileName), append(b, '\n'))
	}
}

// ReadSeatWake reads a seat's admitted wake record (os.ErrNotExist before
// its first launch).
func ReadSeatWake(root, handle string) (SeatWakeRecord, error) {
	var r SeatWakeRecord
	b, err := os.ReadFile(filepath.Join(launch.StagedDir(root, handle), WakeFileName))
	if err != nil {
		return r, err
	}
	return r, json.Unmarshal(b, &r)
}

// MarksFileName holds a seat's launch budget marks beside its staged record
// (brief 31-1): the squad launch's start, written with each launch, and
// the seat's first AMQ message, written once by the scheduler. Status reads
// a seat's marks from here, its own launch's, never a later scheduler's
// start or a decisions log that scrolls.
const MarksFileName = "marks.json"

// SeatMarks is the content of MarksFileName.
type SeatMarks struct {
	LaunchID     string     `json:"launch_id"`
	Start        *time.Time `json:"start"`
	FirstMessage *time.Time `json:"first_message"`
}

func marksPath(root, handle string) string {
	return filepath.Join(launch.StagedDir(root, handle), MarksFileName)
}

// writeMarks records each staged seat's launch start (best effort, like
// the wake record).
func writeMarks(c Compiled, launchID string, start time.Time, seatStart map[string]time.Time) {
	for _, rec := range c.Records {
		m := SeatMarks{LaunchID: launchID}
		at := start
		if own, ok := seatStart[rec.Handle]; ok && !own.IsZero() {
			at = own // the key press of the add key (brief 31-2 PR 2)
		}
		if !at.IsZero() {
			t := at.UTC()
			m.Start = &t
		}
		if b, err := json.Marshal(m); err == nil {
			_ = writeAtomic(marksPath(rec.Root, rec.Handle), append(b, '\n'))
		}
	}
}

// ReadSeatMarks reads a seat's marks (os.ErrNotExist before a launch that
// wrote them).
func ReadSeatMarks(root, handle string) (SeatMarks, error) {
	var m SeatMarks
	b, err := os.ReadFile(marksPath(root, handle))
	if err != nil {
		return m, err
	}
	return m, json.Unmarshal(b, &m)
}

// RecordFirstMessage writes a seat's first AMQ message into its marks, once,
// for the launch its staged record names (a mark of another launch, or one
// already set, is left as it is).
func RecordFirstMessage(root, handle string, at time.Time) error {
	staged, err := os.ReadFile(filepath.Join(launch.StagedDir(root, handle), launch.FileName))
	if err != nil {
		return err
	}
	rec, err := launch.Decode(staged)
	if err != nil {
		return err
	}
	m, err := ReadSeatMarks(root, handle)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if m.LaunchID != rec.LaunchID {
		m = SeatMarks{LaunchID: rec.LaunchID}
	}
	if m.FirstMessage != nil {
		return nil
	}
	t := at.UTC()
	m.FirstMessage = &t
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return writeAtomic(marksPath(root, handle), append(b, '\n'))
}

// admittedWakes reads each participant's admitted wake from the Prepare
// preview, as a fact: a seat the preview lists without execution options,
// or whose admitted wake differs from the compiled one, is recorded as AMQ
// admitted it, never refused (brief 31-1).
func admittedWakes(preview launchapi.PreviewV1) map[string]*SeatWakeFacts {
	out := map[string]*SeatWakeFacts{}
	for _, p := range preview.Participants {
		if p.Execution == nil {
			continue
		}
		got := p.Execution
		facts := &SeatWakeFacts{RequireWake: got.RequireWake, Mode: string(got.Wake.Mode), AuditReason: got.Wake.AuditReason, Source: "amq_prepare_preview"}
		if got.Wake.Injector != nil {
			facts.Injector = string(got.Wake.Injector.Mode)
		}
		out[p.Handle] = facts
	}
	return out
}

// RolePointer is the whole of the role's part in initial_input: a pointer.
const RolePointer = "Your seat role is in the file named by the environment variable AMQ_SQUAD_ROLE_PATH. Read it first and follow it."

var handlePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// ProviderArgs returns the provider arguments of a seat: model and effort
// (and Codex's sandbox and approval flags and its two sandbox pins) for
// Claude and Codex; --model and --effort for grok and --model for
// cursor-agent, the forms AMQ v0.116.2's adapters admit
// (internal/launch/adapter_grok.go:133-134, adapter_cursor.go:135 there;
// CheckArgumentForms checks them against the advertisement); none for pi.
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
	case "grok":
		if s.Model != "" {
			args = append(args, "--model", s.Model)
		}
		if s.Effort != "" {
			args = append(args, "--effort", s.Effort)
		}
	case "cursor-agent":
		if s.Model != "" {
			args = append(args, "--model", s.Model)
		}
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
		requireWake, injector, wakeReason := SeatWake(s.Runtime, q.CodexHookMissing)
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
			RulesPath: q.RulesPath, RulesSHA256: rulesSHA, Branch: s.Branch, Base: s.Base, Worktree: s.Worktree,
			AMQPath: q.AMQPath, EnvNeeds: s.EnvNeeds, CredNeeds: s.CredentialNeeds, Resume: s.Resume, RequestID: s.RequestID, PermissionMode: permissionMode(s.Runtime), Credential: credentialSource(s.Runtime),
			CWD: s.Worktree, Binary: s.Runtime, Role: s.RoleName, TeamHome: q.TeamHome, TeamProfile: q.Profile, StartedAt: now.UTC(),
			PiShim: piShimFor(s, q), WakeRequired: requireWake,
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
// the session root (0700 directories, 0600 files). The session is created
// before a launch (brief 31-1: `amq-squad up`, and scheduler run on an
// absent session root), so Stage no longer refuses one that is missing.
func Stage(c Compiled) error {
	for i, rec := range c.Records {
		dir := launch.StagedDir(rec.Root, rec.Handle)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		if err := writeAtomic(filepath.Join(dir, launch.RoleFileName), c.Roles[i]); err != nil {
			return err
		}
		if err := writeStaged(filepath.Join(dir, launch.FileName), rec); err != nil {
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
