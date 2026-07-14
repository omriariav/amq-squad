package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/procinfo"
	"github.com/omriariav/amq-squad/v2/internal/team"
	"github.com/omriariav/amq-squad/v2/internal/tmuxpane"
)

// PaneCleanupOutcome is the complete fail-closed result vocabulary for one
// requested pane cleanup.  Preserved outcomes intentionally distinguish why no
// mutation happened so later lifecycle wiring can render and recover safely.
type PaneCleanupOutcome string

const (
	PaneCleanupClosed                       PaneCleanupOutcome = "closed"
	PaneCleanupAlreadyGone                  PaneCleanupOutcome = "already_gone"
	PaneCleanupPreservedIdentityUnconfirmed PaneCleanupOutcome = "preserved_identity_unconfirmed"
	PaneCleanupPreservedExternal            PaneCleanupOutcome = "preserved_external"
	PaneCleanupCloseFailed                  PaneCleanupOutcome = "close_failed"
	PaneCleanupInspectionUnavailable        PaneCleanupOutcome = "inspection_unavailable"
	PaneCleanupNotRequested                 PaneCleanupOutcome = "not_requested"
)

// PaneCleanupScope is caller-owned authority for the launch record being
// considered. CWD is the member launch cwd; ProjectDir/TeamHome identify the
// owning project rather than being inferred from the record under cleanup.
type PaneCleanupScope struct {
	ProjectDir string `json:"project"`
	TeamHome   string `json:"team_home"`
	Profile    string `json:"profile"`
	Root       string `json:"root"`
	BaseRoot   string `json:"base_root"`
	Session    string `json:"session"`
	Role       string `json:"role"`
	Handle     string `json:"handle"`
	Binary     string `json:"binary"`
	CWD        string `json:"cwd"`
}

// PaneCleanupAgentAttestation is pre-signal evidence supplied by the lifecycle
// caller's existing liveness classifier. The policy accepts no inferred or
// presence-only ownership: the exact recorded PID must be live and verified as
// the exact binary before one process-table snapshot proves pane ancestry.
type PaneCleanupAgentAttestation struct {
	PID         int    `json:"pid"`
	Binary      string `json:"binary"`
	Live        bool   `json:"live"`
	BinaryMatch bool   `json:"binary_match"`
}

type PaneCleanupRequest struct {
	Requested   bool                        `json:"requested"`
	Scope       PaneCleanupScope            `json:"scope"`
	Record      launch.Record               `json:"-"`
	Attestation PaneCleanupAgentAttestation `json:"attestation"`
}

// PaneCleanupMismatch is stable structured evidence; Detail remains prose.
type PaneCleanupMismatch struct {
	Field    string `json:"field"`
	Expected string `json:"expected,omitempty"`
	Actual   string `json:"actual,omitempty"`
}

// PaneCleanupIdentity is the immutable pane identity carried from pre-signal
// preparation into post-signal revalidation and durable cleanup manifests. It
// is identity evidence, not a claim of OS file-descriptor confinement.
type PaneCleanupIdentity struct {
	Scope       PaneCleanupScope `json:"scope"`
	PaneID      string           `json:"pane_id"`
	TmuxSession string           `json:"tmux_session"`
	WindowID    string           `json:"window_id"`
	AgentPID    int              `json:"agent_pid"`
}

type PaneCleanupPaneEvidence struct {
	PaneID       string `json:"pane_id"`
	Session      string `json:"session"`
	WindowID     string `json:"window_id"`
	PanePID      int    `json:"pane_pid"`
	CWD          string `json:"cwd"`
	CanonicalCWD string `json:"canonical_cwd,omitempty"`
	Title        string `json:"title,omitempty"`
	WindowName   string `json:"window_name,omitempty"`
}

type PaneCleanupRecovery struct {
	Identity    PaneCleanupIdentity      `json:"identity"`
	InitialPane *PaneCleanupPaneEvidence `json:"initial_pane,omitempty"`
	CurrentPane *PaneCleanupPaneEvidence `json:"current_pane,omitempty"`
}

type PaneCleanupResult struct {
	Outcome    PaneCleanupOutcome    `json:"outcome"`
	Mismatches []PaneCleanupMismatch `json:"mismatches,omitempty"`
	Detail     string                `json:"detail,omitempty"`
	Recovery   *PaneCleanupRecovery  `json:"recovery,omitempty"`
}

// PaneCleanupPreparation is the pre-signal handoff. Ready means the launch,
// pane, process, and ancestry identities were all positively attested for pane
// cleanup. Ready=false NEVER withholds authority to terminate the agent; agent
// lifecycle is independent. It only withholds authority to close the pane.
type PaneCleanupPreparation struct {
	Ready    bool                    `json:"ready"`
	Identity PaneCleanupIdentity     `json:"identity"`
	Initial  PaneCleanupPaneEvidence `json:"initial_pane"`
	Result   PaneCleanupResult       `json:"result,omitempty"`
}

type PaneCleanupDependencies struct {
	Inspect       func(string) tmuxpane.PaneInspection
	Close         func(string) error
	ChildrenIndex func() (func(int) []int, error)
	CanonicalDir  func(string) (string, error)
}

func (d PaneCleanupDependencies) withDefaults() PaneCleanupDependencies {
	if d.Inspect == nil {
		d.Inspect = tmuxpane.InspectPaneExactByID
	}
	if d.Close == nil {
		d.Close = tmuxpane.ClosePane
	}
	if d.ChildrenIndex == nil {
		d.ChildrenIndex = procinfo.ChildrenIndex
	}
	if d.CanonicalDir == nil {
		d.CanonicalDir = cleanupCanonicalDir
	}
	return d
}

// PreparePaneCleanup performs every check that must happen while the verified
// agent PID is still live. It never signals or closes a pane.
func PreparePaneCleanup(req PaneCleanupRequest, deps PaneCleanupDependencies) PaneCleanupPreparation {
	deps = deps.withDefaults()
	identity := identityForCleanup(req)
	recovery := &PaneCleanupRecovery{Identity: identity}
	finish := func(outcome PaneCleanupOutcome, detail string, mismatches ...PaneCleanupMismatch) PaneCleanupPreparation {
		return PaneCleanupPreparation{Identity: identity, Result: PaneCleanupResult{
			Outcome: outcome, Detail: detail, Mismatches: mismatches, Recovery: recovery,
		}}
	}
	if !req.Requested {
		return finish(PaneCleanupNotRequested, "pane cleanup was not requested")
	}
	if recordIsExternal(req.Record) {
		return finish(PaneCleanupPreservedExternal, "external/operator-owned pane is never closed")
	}
	if mismatches := validatePaneCleanupRecord(req, deps.CanonicalDir); len(mismatches) > 0 {
		return finish(PaneCleanupPreservedIdentityUnconfirmed, "launch identity is not fully confirmed", mismatches...)
	}

	inspection := deps.Inspect(identity.PaneID)
	switch inspection.State {
	case tmuxpane.PaneInspectionGone:
		return finish(PaneCleanupAlreadyGone, inspection.Detail)
	case tmuxpane.PaneInspectionFound:
		// Continue below.
	default:
		return finish(PaneCleanupInspectionUnavailable, inspection.Detail,
			PaneCleanupMismatch{Field: "inspection", Expected: string(tmuxpane.PaneInspectionFound), Actual: string(inspection.State)})
	}

	initial, paneMismatches := attestInspectedPane(identity, req.Record.CWD, inspection.Pane, deps.CanonicalDir)
	recovery.InitialPane = &initial
	if len(paneMismatches) > 0 {
		return finish(PaneCleanupPreservedIdentityUnconfirmed, "inspected pane identity is not fully confirmed", paneMismatches...)
	}
	children, err := deps.ChildrenIndex()
	if err != nil || children == nil {
		detail := "process table snapshot unavailable"
		if err != nil {
			detail += ": " + err.Error()
		}
		return finish(PaneCleanupPreservedIdentityUnconfirmed, detail,
			PaneCleanupMismatch{Field: "process_snapshot", Expected: "available", Actual: "unavailable"})
	}
	if !strictDescendant(children, initial.PanePID, req.Attestation.PID) {
		return finish(PaneCleanupPreservedIdentityUnconfirmed, "verified agent PID is not a descendant of the inspected pane PID",
			PaneCleanupMismatch{Field: "agent_pid_ancestry", Expected: fmt.Sprintf("descendant of %d", initial.PanePID), Actual: fmt.Sprintf("pid %d", req.Attestation.PID)})
	}

	return PaneCleanupPreparation{Ready: true, Identity: identity, Initial: initial}
}

// ClosePreparedPane immediately revalidates the exact prepared identity and
// then invokes the mutating closer at most once. It performs no mutating retry.
func ClosePreparedPane(prepared PaneCleanupPreparation, deps PaneCleanupDependencies) PaneCleanupResult {
	if !prepared.Ready {
		return prepared.Result
	}
	deps = deps.withDefaults()
	recovery := &PaneCleanupRecovery{Identity: prepared.Identity, InitialPane: &prepared.Initial}
	inspection := deps.Inspect(prepared.Identity.PaneID)
	switch inspection.State {
	case tmuxpane.PaneInspectionGone:
		return PaneCleanupResult{Outcome: PaneCleanupAlreadyGone, Detail: inspection.Detail, Recovery: recovery}
	case tmuxpane.PaneInspectionFound:
		// Continue below.
	default:
		return PaneCleanupResult{Outcome: PaneCleanupInspectionUnavailable, Detail: inspection.Detail, Recovery: recovery,
			Mismatches: []PaneCleanupMismatch{{Field: "revalidation_inspection", Expected: string(tmuxpane.PaneInspectionFound), Actual: string(inspection.State)}}}
	}

	current, mismatches := revalidatePreparedPane(prepared, inspection.Pane, deps.CanonicalDir)
	recovery.CurrentPane = &current
	if len(mismatches) > 0 {
		return PaneCleanupResult{Outcome: PaneCleanupPreservedIdentityUnconfirmed,
			Detail: "pane identity changed during immediate revalidation", Mismatches: mismatches, Recovery: recovery}
	}
	if err := deps.Close(prepared.Identity.PaneID); err != nil {
		return PaneCleanupResult{Outcome: PaneCleanupCloseFailed, Detail: err.Error(), Recovery: recovery}
	}
	return PaneCleanupResult{Outcome: PaneCleanupClosed, Detail: "tmux pane closed", Recovery: recovery}
}

func identityForCleanup(req PaneCleanupRequest) PaneCleanupIdentity {
	d := PaneCleanupIdentity{Scope: req.Scope, AgentPID: req.Record.AgentPID}
	if req.Record.Tmux != nil {
		d.PaneID = strings.TrimSpace(req.Record.Tmux.PaneID)
		d.TmuxSession = strings.TrimSpace(req.Record.Tmux.Session)
		d.WindowID = strings.TrimSpace(req.Record.Tmux.WindowID)
	}
	return d
}

func recordIsExternal(rec launch.Record) bool {
	mode := strings.TrimSpace(rec.AdoptionMode)
	return rec.External || mode == adoptionModeExternal || mode == adoptionModeExternalProjectLead || strings.HasPrefix(mode, "external")
}

func validatePaneCleanupRecord(req PaneCleanupRequest, canonicalDir func(string) (string, error)) []PaneCleanupMismatch {
	rec, scope, att := req.Record, req.Scope, req.Attestation
	var out []PaneCleanupMismatch
	match := func(field, expected, actual string) {
		if strings.TrimSpace(expected) == "" || strings.TrimSpace(actual) == "" || strings.TrimSpace(expected) != strings.TrimSpace(actual) {
			out = append(out, PaneCleanupMismatch{Field: field, Expected: expected, Actual: actual})
		}
	}
	pathMatch := func(field, expected, actual string) {
		want, wantErr := canonicalDir(expected)
		got, gotErr := canonicalDir(actual)
		if wantErr != nil || gotErr != nil || want != got {
			out = append(out, PaneCleanupMismatch{Field: field, Expected: expected, Actual: actual})
		}
	}

	switch strings.TrimSpace(rec.AdoptionMode) {
	case "managed_window", "managed_current_window", "managed_session":
	default:
		out = append(out, PaneCleanupMismatch{Field: "adoption_mode", Expected: "managed tmux", Actual: rec.AdoptionMode})
	}
	pathMatch("project", scope.ProjectDir, rec.TeamHome)
	pathMatch("team_home", scope.TeamHome, rec.TeamHome)
	pathMatch("cwd", scope.CWD, rec.CWD)
	pathMatch("root", scope.Root, rec.Root)
	pathMatch("base_root", scope.BaseRoot, rec.BaseRoot)
	if !squadnamespace.ProfilesEqual(scope.Profile, rec.TeamProfile) {
		out = append(out, PaneCleanupMismatch{Field: "profile", Expected: squadnamespace.NormalizeProfile(scope.Profile), Actual: squadnamespace.NormalizeProfile(rec.TeamProfile)})
	}
	match("session", scope.Session, rec.Session)
	match("role", scope.Role, rec.Role)
	match("handle", scope.Handle, rec.Handle)
	match("binary", scope.Binary, rec.Binary)

	if rec.AgentPID <= 0 || att.PID != rec.AgentPID {
		out = append(out, PaneCleanupMismatch{Field: "agent_pid", Expected: fmt.Sprintf("%d", rec.AgentPID), Actual: fmt.Sprintf("%d", att.PID)})
	}
	if !att.Live {
		out = append(out, PaneCleanupMismatch{Field: "agent_live", Expected: "true", Actual: "false"})
	}
	if !att.BinaryMatch || strings.TrimSpace(att.Binary) != strings.TrimSpace(scope.Binary) {
		out = append(out, PaneCleanupMismatch{Field: "agent_binary", Expected: scope.Binary, Actual: att.Binary})
	}

	if rec.Tmux == nil {
		out = append(out, PaneCleanupMismatch{Field: "tmux", Expected: "full identity", Actual: "missing"})
		return out
	}
	if _, err := exactTmuxPaneID(rec.Tmux.PaneID); err != nil {
		out = append(out, PaneCleanupMismatch{Field: "tmux.pane_id", Expected: "%<digits>", Actual: rec.Tmux.PaneID})
	}
	if _, err := exactTmuxWindowID(rec.Tmux.WindowID); err != nil {
		out = append(out, PaneCleanupMismatch{Field: "tmux.window_id", Expected: "@<digits>", Actual: rec.Tmux.WindowID})
	}
	if strings.TrimSpace(rec.Tmux.Session) == "" {
		out = append(out, PaneCleanupMismatch{Field: "tmux.session", Expected: "nonempty", Actual: rec.Tmux.Session})
	}
	wantTarget := map[string]string{"managed_window": "new-window", "managed_current_window": "current-window", "managed_session": "new-session"}[strings.TrimSpace(rec.AdoptionMode)]
	if strings.TrimSpace(rec.Tmux.Target) != wantTarget {
		out = append(out, PaneCleanupMismatch{Field: "tmux.target", Expected: wantTarget, Actual: rec.Tmux.Target})
	}
	if rec.Terminal == nil {
		out = append(out, PaneCleanupMismatch{Field: "terminal", Expected: "tmux mirror", Actual: "missing"})
		return out
	}
	match("terminal.backend", "tmux", rec.Terminal.Backend)
	match("terminal.session", rec.Tmux.Session, rec.Terminal.Session)
	match("terminal.window_id", rec.Tmux.WindowID, rec.Terminal.WindowID)
	match("terminal.pane_id", rec.Tmux.PaneID, rec.Terminal.PaneID)
	match("terminal.target", rec.Tmux.Target, rec.Terminal.Target)
	return out
}

func attestInspectedPane(identity PaneCleanupIdentity, recordedCWD string, pane tmuxpane.TmuxPane, canonicalDir func(string) (string, error)) (PaneCleanupPaneEvidence, []PaneCleanupMismatch) {
	canonicalCWD, cwdErr := canonicalDir(pane.CWD)
	evidence := paneEvidence(pane, canonicalCWD)
	var out []PaneCleanupMismatch
	check := func(field, expected, actual string) {
		if strings.TrimSpace(expected) == "" || strings.TrimSpace(actual) == "" || strings.TrimSpace(expected) != strings.TrimSpace(actual) {
			out = append(out, PaneCleanupMismatch{Field: field, Expected: expected, Actual: actual})
		}
	}
	check("pane.id", identity.PaneID, pane.PaneID)
	check("pane.session", identity.TmuxSession, pane.Session)
	check("pane.window_id", identity.WindowID, pane.WindowID)
	if pane.PID <= 0 {
		out = append(out, PaneCleanupMismatch{Field: "pane.pid", Expected: "positive", Actual: fmt.Sprintf("%d", pane.PID)})
	}
	recordedCanonical, recErr := canonicalDir(recordedCWD)
	if cwdErr != nil || recErr != nil || recordedCanonical != canonicalCWD {
		out = append(out, PaneCleanupMismatch{Field: "pane.cwd", Expected: recordedCWD, Actual: pane.CWD})
	}
	return evidence, out
}

func revalidatePreparedPane(prepared PaneCleanupPreparation, pane tmuxpane.TmuxPane, canonicalDir func(string) (string, error)) (PaneCleanupPaneEvidence, []PaneCleanupMismatch) {
	current, out := attestInspectedPane(prepared.Identity, prepared.Initial.CWD, pane, canonicalDir)
	if pane.PID != prepared.Initial.PanePID {
		out = append(out, PaneCleanupMismatch{Field: "pane.pid", Expected: fmt.Sprintf("%d", prepared.Initial.PanePID), Actual: fmt.Sprintf("%d", pane.PID)})
	}
	if current.CanonicalCWD == "" || current.CanonicalCWD != prepared.Initial.CanonicalCWD {
		out = append(out, PaneCleanupMismatch{Field: "pane.cwd_revalidation", Expected: prepared.Initial.CanonicalCWD, Actual: current.CanonicalCWD})
	}
	return current, out
}

func paneEvidence(p tmuxpane.TmuxPane, canonicalCWD string) PaneCleanupPaneEvidence {
	return PaneCleanupPaneEvidence{PaneID: p.PaneID, Session: p.Session, WindowID: p.WindowID,
		PanePID: p.PID, CWD: p.CWD, CanonicalCWD: canonicalCWD, Title: p.Title, WindowName: p.WindowName}
}

func cleanupCanonicalDir(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("empty path")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("not a directory")
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	return filepath.Clean(resolved), nil
}

func strictDescendant(children func(int) []int, root, want int) bool {
	if children == nil || root <= 0 || want <= 0 || root == want {
		return false
	}
	seen := map[int]bool{root: true}
	stack := append([]int(nil), children(root)...)
	for len(stack) > 0 {
		pid := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if pid <= 0 || seen[pid] {
			continue
		}
		if pid == want {
			return true
		}
		seen[pid] = true
		stack = append(stack, children(pid)...)
	}
	return false
}

func paneCleanupUnavailableWithoutRecord(requested bool, detail string) PaneCleanupResult {
	if !requested {
		return PaneCleanupResult{Outcome: PaneCleanupNotRequested, Detail: "pane cleanup was not requested"}
	}
	return PaneCleanupResult{Outcome: PaneCleanupPreservedIdentityUnconfirmed, Detail: detail,
		Mismatches: []PaneCleanupMismatch{{Field: "launch_record", Expected: "complete", Actual: "unavailable"}}}
}

func paneCleanupPreservedAfterPreparation(prepared PaneCleanupPreparation, detail string) PaneCleanupResult {
	recovery := &PaneCleanupRecovery{Identity: prepared.Identity, InitialPane: &prepared.Initial}
	return PaneCleanupResult{Outcome: PaneCleanupPreservedIdentityUnconfirmed, Detail: detail,
		Mismatches: []PaneCleanupMismatch{{Field: "agent_signal", Expected: "succeeded", Actual: "failed"}}, Recovery: recovery}
}

func paneCleanupRequestForMember(t team.Team, projectDir, profile, workstream string, member team.Member, handle, cwd, root, baseRoot string, rec launch.Record, requested bool, att PaneCleanupAgentAttestation) PaneCleanupRequest {
	return PaneCleanupRequest{Requested: requested, Record: rec, Attestation: att, Scope: PaneCleanupScope{
		ProjectDir: projectDir,
		TeamHome:   t.Project,
		Profile:    profile,
		Root:       root,
		BaseRoot:   absoluteAMQRoot(cwd, baseRoot),
		Session:    workstream,
		Role:       member.Role,
		Handle:     handle,
		Binary:     member.Binary,
		CWD:        cwd,
	}}
}
