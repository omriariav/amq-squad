package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/flock"
	"github.com/omriariav/amq-squad/v2/internal/launch"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/tmuxpane"
)

const (
	globalNOCRegistrySchema   = 1
	globalNOCBootstrapVersion = 1
	globalNOCRegistryDir      = "noc"
	globalNOCRegistryFile     = "registry.json"
	globalNOCRole             = "noc"

	globalNOCLaunchPrepared = "prepared"
	globalNOCLaunchActive   = "active"
	globalNOCLaunchFailed   = "failed"
	globalNOCLaunchStopped  = "stopped"

	globalNOCRunPlanned      = "registration_planned"
	globalNOCRunRegistered   = "wake_registered"
	globalNOCRunPollRequired = "poll_required"
	globalNOCRunOptOut       = "polling_opt_out"
)

type globalNOCBackstop struct {
	IntervalSeconds int `json:"interval_seconds"`
	TimeoutSeconds  int `json:"timeout_seconds"`
	MaxTicks        int `json:"max_ticks"`
}

func (b globalNOCBackstop) validate() error {
	if b.IntervalSeconds <= 0 || b.TimeoutSeconds <= 0 || b.MaxTicks <= 0 {
		return fmt.Errorf("NOC stall backstop must have positive interval, timeout, and max_ticks")
	}
	return nil
}

type globalNOCLaunch struct {
	ID               string            `json:"id"`
	Generation       uint64            `json:"generation"`
	State            string            `json:"state"`
	Record           launch.Record     `json:"launch_record"`
	BootstrapVersion int               `json:"bootstrap_version"`
	BootstrapDigest  string            `json:"bootstrap_digest"`
	Backstop         globalNOCBackstop `json:"stall_backstop"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
	Detail           string            `json:"detail,omitempty"`
}

type globalNOCRun struct {
	ID                   string                           `json:"id"`
	NOCLaunchID          string                           `json:"noc_launch_id"`
	NOCGeneration        uint64                           `json:"noc_generation"`
	Namespace            squadnamespace.Ref               `json:"namespace"`
	LeadRole             string                           `json:"lead_role"`
	Policy               string                           `json:"policy"`
	State                string                           `json:"state"`
	ExternalRegistration *launch.OrchestratorRegistration `json:"external_registration,omitempty"`
	CreatedAt            time.Time                        `json:"created_at"`
	UpdatedAt            time.Time                        `json:"updated_at"`
	Detail               string                           `json:"detail,omitempty"`
}

type globalNOCRegistry struct {
	SchemaVersion     int               `json:"schema_version"`
	ControlRoot       string            `json:"control_root"`
	CurrentGeneration uint64            `json:"current_generation"`
	UpdatedAt         time.Time         `json:"updated_at"`
	Launches          []globalNOCLaunch `json:"launches"`
	Runs              []globalNOCRun    `json:"runs,omitempty"`
}

type globalNOCContext struct {
	ControlRoot string
	Launch      globalNOCLaunch
}

type globalNOCRegistrationPlan struct {
	Context *globalNOCContext
	Enabled bool
	Handle  string
	Policy  string
	Strict  bool
	OptOut  bool
}

var globalNOCNow = time.Now
var globalNOCCurrentPaneIdentity = currentPaneIdentity
var globalNOCPaneIdentityFor = tmuxpane.PaneIdentityFor
var globalNOCPaneInspection = tmuxpane.InspectPaneExactByID
var globalNOCProcessWaitTimeout = 5 * time.Second
var globalNOCProcessWaitInterval = 50 * time.Millisecond
var globalNOCProcessWaitSleep = time.Sleep
var globalNOCRuntimeIdentity = func(rec launch.Record, currentPane string) launchRuntimeIdentity {
	return classifyLaunchRuntimeIdentity(
		rec,
		rec.Binary,
		currentPane,
		launchRuntimeProbeFromDuplicate(defaultDuplicateLaunchProbe),
	)
}

func resolveGlobalNOCRegistrationPlan(explicitHandle string, explicit, optOut bool) (globalNOCRegistrationPlan, error) {
	if explicit && optOut {
		return globalNOCRegistrationPlan{}, usageErrorf("--register-orchestrator and --no-register-orchestrator are mutually exclusive")
	}
	cwd, err := os.Getwd()
	if err != nil {
		return globalNOCRegistrationPlan{}, fmt.Errorf("resolve current directory for NOC registration: %w", err)
	}
	context, contextErr := detectRegisteredGlobalNOC(cwd)
	if contextErr != nil {
		fmt.Fprintf(os.Stderr, "warning: NOC registry/identity could not be verified; implicit wake registration is disabled and polling is required: %v\n", contextErr)
	}
	if explicit {
		handle := strings.TrimSpace(explicitHandle)
		if handle == "" {
			handle = defaultGoalOrchestratorHandle
		}
		return globalNOCRegistrationPlan{
			Context: context, Enabled: true, Handle: handle,
			Policy: "explicit", Strict: true,
		}, nil
	}
	if context == nil {
		return globalNOCRegistrationPlan{}, nil
	}
	if optOut {
		return globalNOCRegistrationPlan{
			Context: context, Policy: "registered_noc_opt_out", OptOut: true,
		}, nil
	}
	return globalNOCRegistrationPlan{
		Context: context, Enabled: true, Handle: defaultGoalOrchestratorHandle,
		Policy: "registered_noc_default", Strict: false,
	}, nil
}

func globalNOCRegistryPath(root string) string {
	return filepath.Join(root, ".amq-squad", globalNOCRegistryDir, globalNOCRegistryFile)
}

func canonicalGlobalNOCControlRoot(root string) (string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", fmt.Errorf("NOC control root is required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve NOC control root: %w", err)
	}
	evaluated, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("resolve NOC control root identity: %w", err)
	}
	info, err := os.Lstat(evaluated)
	if err != nil {
		return "", fmt.Errorf("stat NOC control root: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("NOC control root must be a non-symlink directory")
	}
	return filepath.Clean(evaluated), nil
}

func ensureGlobalNOCRegistryDirectory(root string) (string, error) {
	current := root
	for _, name := range []string{".amq-squad", globalNOCRegistryDir} {
		current = filepath.Join(current, name)
		info, err := os.Lstat(current)
		switch {
		case err == nil:
			if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return "", fmt.Errorf("NOC registry component %s must be a non-symlink directory", current)
			}
		case os.IsNotExist(err):
			if err := os.Mkdir(current, 0o700); err != nil {
				return "", fmt.Errorf("create NOC registry directory %s: %w", current, err)
			}
		default:
			return "", fmt.Errorf("stat NOC registry directory %s: %w", current, err)
		}
	}
	return current, nil
}

func readGlobalNOCRegistry(root string) (globalNOCRegistry, error) {
	canonical, err := canonicalGlobalNOCControlRoot(root)
	if err != nil {
		return globalNOCRegistry{}, err
	}
	path := globalNOCRegistryPath(canonical)
	info, err := os.Lstat(path)
	if err != nil {
		return globalNOCRegistry{}, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return globalNOCRegistry{}, fmt.Errorf("NOC registry must be a non-symlink regular file")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return globalNOCRegistry{}, err
	}
	var registry globalNOCRegistry
	if err := json.Unmarshal(body, &registry); err != nil {
		return globalNOCRegistry{}, fmt.Errorf("parse NOC registry: %w", err)
	}
	if err := validateGlobalNOCRegistry(registry, canonical); err != nil {
		return globalNOCRegistry{}, err
	}
	return registry, nil
}

func writeGlobalNOCRegistry(root string, mutate func(*globalNOCRegistry) error) error {
	canonical, err := canonicalGlobalNOCControlRoot(root)
	if err != nil {
		return err
	}
	dir, err := ensureGlobalNOCRegistryDirectory(canonical)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, globalNOCRegistryFile)
	lockPath := path + ".lock"
	if info, statErr := os.Lstat(lockPath); statErr == nil && (!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
		return fmt.Errorf("NOC registry lock must be a non-symlink regular file")
	} else if statErr != nil && !os.IsNotExist(statErr) {
		return statErr
	}
	return flock.WithLock(lockPath, func() error {
		registry := globalNOCRegistry{SchemaVersion: globalNOCRegistrySchema, ControlRoot: canonical}
		if existing, readErr := readGlobalNOCRegistry(canonical); readErr == nil {
			registry = existing
		} else if !os.IsNotExist(readErr) {
			return readErr
		}
		if err := mutate(&registry); err != nil {
			return err
		}
		if err := validateGlobalNOCRegistry(registry, canonical); err != nil {
			return err
		}
		body, err := json.MarshalIndent(registry, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal NOC registry: %w", err)
		}
		return atomicWriteJSONBytes(path, append(body, '\n'))
	})
}

func validateGlobalNOCRegistry(registry globalNOCRegistry, expectedRoot string) error {
	if registry.SchemaVersion != globalNOCRegistrySchema {
		return fmt.Errorf("unsupported NOC registry schema %d", registry.SchemaVersion)
	}
	// ControlRoot is canonicalized exactly once when the registry is created.
	// Treat the stored value as immutable authority bytes after that: resolving
	// it again would let a later symlink swap transfer authority to a different
	// directory.
	if registry.ControlRoot == "" || !filepath.IsAbs(registry.ControlRoot) ||
		registry.ControlRoot != filepath.Clean(registry.ControlRoot) ||
		registry.ControlRoot != expectedRoot {
		return fmt.Errorf("NOC registry control root mismatch")
	}
	if registry.CurrentGeneration != uint64(len(registry.Launches)) {
		return fmt.Errorf("NOC registry generation sequence is invalid")
	}
	for i, item := range registry.Launches {
		wantGeneration := uint64(i + 1)
		if item.Generation != wantGeneration || strings.TrimSpace(item.ID) == "" {
			return fmt.Errorf("NOC launch generation %d identity is invalid", wantGeneration)
		}
		switch item.State {
		case globalNOCLaunchPrepared, globalNOCLaunchActive, globalNOCLaunchFailed, globalNOCLaunchStopped:
		default:
			return fmt.Errorf("NOC launch generation %d has invalid state %q", wantGeneration, item.State)
		}
		if item.Record.Tmux == nil || strings.TrimSpace(item.Record.Tmux.PaneID) == "" ||
			strings.TrimSpace(item.Record.Session) == "" || strings.TrimSpace(item.Record.Role) == "" {
			return fmt.Errorf("NOC launch generation %d has incomplete stamped runtime identity", wantGeneration)
		}
		if item.Record.CWD != registry.ControlRoot {
			return fmt.Errorf("NOC launch generation %d control root binding mismatch", wantGeneration)
		}
		if item.Record.AgentPID <= 0 {
			return fmt.Errorf("NOC launch generation %d has no positive agent PID", wantGeneration)
		}
		if item.BootstrapVersion != globalNOCBootstrapVersion {
			return fmt.Errorf("NOC launch generation %d has unsupported bootstrap version %d", wantGeneration, item.BootstrapVersion)
		}
		if err := item.Backstop.validate(); err != nil {
			return fmt.Errorf("NOC launch generation %d: %w", wantGeneration, err)
		}
		if item.CreatedAt.IsZero() || item.UpdatedAt.Before(item.CreatedAt) {
			return fmt.Errorf("NOC launch generation %d timestamps are invalid", wantGeneration)
		}
		if i+1 < len(registry.Launches) && item.State == globalNOCLaunchActive {
			return fmt.Errorf("NOC launch generation %d remained active after replacement", wantGeneration)
		}
	}
	seenRunIDs := make(map[string]struct{}, len(registry.Runs))
	for _, item := range registry.Runs {
		runID := strings.TrimSpace(item.ID)
		if runID == "" || runID != item.ID || item.NOCGeneration == 0 || item.NOCGeneration > registry.CurrentGeneration {
			return fmt.Errorf("NOC run registration identity is invalid")
		}
		if _, exists := seenRunIDs[runID]; exists {
			return fmt.Errorf("duplicate run registrations share ID %q", runID)
		}
		seenRunIDs[runID] = struct{}{}
		if registry.Launches[item.NOCGeneration-1].ID != item.NOCLaunchID {
			return fmt.Errorf("NOC run registration launch binding mismatch")
		}
		switch item.State {
		case globalNOCRunPlanned, globalNOCRunRegistered, globalNOCRunPollRequired, globalNOCRunOptOut:
		default:
			return fmt.Errorf("NOC run registration %s has invalid state %q", item.ID, item.State)
		}
		if strings.TrimSpace(item.Namespace.ID) == "" || item.CreatedAt.IsZero() || item.UpdatedAt.Before(item.CreatedAt) {
			return fmt.Errorf("NOC run registration %s is incomplete", item.ID)
		}
	}
	return nil
}

func globalNOCLaunchID(root string, now time.Time) string {
	sum := sha256.Sum256([]byte(filepath.Clean(root) + "\x00" + now.UTC().Format(time.RFC3339Nano)))
	return "noc-" + hex.EncodeToString(sum[:8])
}

func globalNOCBootstrapDigest(prompt string) string {
	sum := sha256.Sum256([]byte(prompt))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func beginGlobalNOCLaunch(root, id, binary, model string, agentPID int, identity *tmuxpane.PaneIdentity, bootstrapDigest string, backstop globalNOCBackstop, now time.Time) (globalNOCLaunch, error) {
	if identity == nil || strings.TrimSpace(identity.PaneID) == "" {
		return globalNOCLaunch{}, fmt.Errorf("NOC launch requires exact tmux pane identity")
	}
	if agentPID <= 0 {
		return globalNOCLaunch{}, fmt.Errorf("NOC launch requires a positive pane/agent PID")
	}
	now = now.UTC()
	var created globalNOCLaunch
	err := writeGlobalNOCRegistry(root, func(registry *globalNOCRegistry) error {
		if len(registry.Launches) > 0 {
			current := &registry.Launches[len(registry.Launches)-1]
			if current.State == globalNOCLaunchPrepared {
				inspection := globalNOCPaneInspection(current.Record.Tmux.PaneID)
				if inspection.State != tmuxpane.PaneInspectionGone {
					return fmt.Errorf("NOC launch generation %d is still prepared for pane %s; inspect or close that exact pane before replacing it", current.Generation, current.Record.Tmux.PaneID)
				}
				current.State = globalNOCLaunchStopped
				current.UpdatedAt = now
				current.Detail = "stale prepared generation re-armed after its exact pane was confirmed gone"
			}
			if current.State == globalNOCLaunchActive {
				inspection := globalNOCPaneInspection(current.Record.Tmux.PaneID)
				runtimeIdentity := globalNOCRuntimeIdentity(current.Record, "")
				if inspection.State != tmuxpane.PaneInspectionGone {
					detail := strings.TrimSpace(inspection.Detail)
					if runtimeIdentity.PIDLive {
						detail = "canonical launch PID runtime identity is live"
					} else if detail == "" {
						detail = "exact pane state or stamped launch identity is unverified"
					}
					return fmt.Errorf("NOC launch generation %d cannot be replaced at pane %s: %s; reuse it or stop that exact runtime before replacement", current.Generation, current.Record.Tmux.PaneID, detail)
				}
				current.State = globalNOCLaunchStopped
				current.UpdatedAt = now
				current.Detail = "canonical runtime identity was not live when a replacement was explicitly launched"
			}
		}
		generation := registry.CurrentGeneration + 1
		rec := launch.Record{
			CWD:       registry.ControlRoot,
			Binary:    strings.TrimSpace(binary),
			Model:     strings.TrimSpace(model),
			Session:   id,
			Handle:    globalNOCRole,
			Role:      globalNOCRole,
			External:  true,
			AgentPID:  agentPID,
			StartedAt: now,
			Tmux: &launch.TmuxInfo{
				Session: identity.Session, WindowID: identity.WindowID,
				WindowName: identity.WindowName, PaneID: identity.PaneID, Target: "new-window",
			},
		}
		rec.Terminal = launch.TerminalInfoFromTmux(rec.Tmux)
		created = globalNOCLaunch{
			ID: id, Generation: generation, State: globalNOCLaunchPrepared,
			Record: rec, BootstrapVersion: globalNOCBootstrapVersion,
			BootstrapDigest: bootstrapDigest, Backstop: backstop,
			CreatedAt: now, UpdatedAt: now,
		}
		registry.CurrentGeneration = generation
		registry.UpdatedAt = now
		registry.Launches = append(registry.Launches, created)
		return nil
	})
	return created, err
}

func waitForGlobalNOCPIDIdentity(rec launch.Record) error {
	deadline := time.Now().Add(globalNOCProcessWaitTimeout)
	for {
		identity := globalNOCRuntimeIdentity(rec, "")
		if identity.PIDLive {
			return nil
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("NOC agent PID %d did not establish canonical %s runtime identity within %s", rec.AgentPID, rec.Binary, globalNOCProcessWaitTimeout)
		}
		globalNOCProcessWaitSleep(globalNOCProcessWaitInterval)
	}
}

func transitionGlobalNOCLaunch(root, id, state, detail string, now time.Time) error {
	now = now.UTC()
	return writeGlobalNOCRegistry(root, func(registry *globalNOCRegistry) error {
		if len(registry.Launches) == 0 {
			return fmt.Errorf("NOC registry has no launch to transition")
		}
		current := &registry.Launches[len(registry.Launches)-1]
		if current.ID != id {
			return fmt.Errorf("NOC launch generation changed")
		}
		if current.State != globalNOCLaunchPrepared {
			return fmt.Errorf("NOC launch %s cannot transition from %s", id, current.State)
		}
		if state != globalNOCLaunchActive && state != globalNOCLaunchFailed {
			return fmt.Errorf("invalid NOC launch transition target %q", state)
		}
		current.State = state
		current.Detail = strings.TrimSpace(detail)
		current.UpdatedAt = now
		registry.UpdatedAt = now
		return nil
	})
}

func detectRegisteredGlobalNOC(root string) (*globalNOCContext, error) {
	registry, err := readGlobalNOCRegistry(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	if len(registry.Launches) == 0 {
		return nil, nil
	}
	current := registry.Launches[len(registry.Launches)-1]
	if current.State != globalNOCLaunchActive {
		return nil, nil
	}
	pane, err := globalNOCCurrentPaneIdentity()
	if err != nil {
		return nil, fmt.Errorf("resolve current pane for NOC registration: %w", err)
	}
	if pane == nil || current.Record.Tmux == nil || pane.PaneID != current.Record.Tmux.PaneID ||
		pane.WindowID != current.Record.Tmux.WindowID || pane.Session != current.Record.Tmux.Session {
		return nil, nil
	}
	identity := globalNOCRuntimeIdentity(current.Record, pane.PaneID)
	if !identity.PIDLive || !identity.PaneLive {
		return nil, fmt.Errorf("current pane matches NOC generation %d but its stamped canonical runtime identity is unverified", current.Generation)
	}
	return &globalNOCContext{ControlRoot: registry.ControlRoot, Launch: current}, nil
}

func beginGlobalNOCRun(ctx *globalNOCContext, project, profile, session, lead, policy string, now time.Time) (globalNOCRun, error) {
	if ctx == nil {
		return globalNOCRun{}, fmt.Errorf("NOC run registration requires verified NOC context")
	}
	now = now.UTC()
	namespace := squadnamespace.Resolve(project, profile, session)
	sum := sha256.Sum256([]byte(ctx.Launch.ID + "\x00" + namespace.ID + "\x00" + now.Format(time.RFC3339Nano)))
	run := globalNOCRun{
		ID: "run-" + hex.EncodeToString(sum[:8]), NOCLaunchID: ctx.Launch.ID,
		NOCGeneration: ctx.Launch.Generation, Namespace: namespace,
		LeadRole: strings.TrimSpace(lead), Policy: strings.TrimSpace(policy),
		State: globalNOCRunPlanned, CreatedAt: now, UpdatedAt: now,
	}
	err := writeGlobalNOCRegistry(ctx.ControlRoot, func(registry *globalNOCRegistry) error {
		if registry.CurrentGeneration != ctx.Launch.Generation ||
			registry.Launches[len(registry.Launches)-1].ID != ctx.Launch.ID ||
			registry.Launches[len(registry.Launches)-1].State != globalNOCLaunchActive {
			return fmt.Errorf("verified NOC generation changed before run registration")
		}
		registry.Runs = append(registry.Runs, run)
		registry.UpdatedAt = now
		return nil
	})
	return run, err
}

func finishGlobalNOCRun(ctx *globalNOCContext, runID, state, detail string, registration *launch.OrchestratorRegistration, now time.Time) error {
	if ctx == nil {
		return fmt.Errorf("NOC run completion requires verified NOC context")
	}
	now = now.UTC()
	return writeGlobalNOCRegistry(ctx.ControlRoot, func(registry *globalNOCRegistry) error {
		for i := len(registry.Runs) - 1; i >= 0; i-- {
			item := &registry.Runs[i]
			if item.ID != runID {
				continue
			}
			if item.NOCLaunchID != ctx.Launch.ID || item.NOCGeneration != ctx.Launch.Generation {
				return fmt.Errorf("NOC run completion launch binding changed")
			}
			switch state {
			case globalNOCRunRegistered, globalNOCRunPollRequired, globalNOCRunOptOut:
			default:
				return fmt.Errorf("invalid NOC run completion state %q", state)
			}
			if item.State != globalNOCRunPlanned {
				if item.State == state && item.Detail == strings.TrimSpace(detail) {
					return nil
				}
				return fmt.Errorf("NOC run registration %s already finalized as %s", runID, item.State)
			}
			item.State = state
			item.Detail = strings.TrimSpace(detail)
			item.ExternalRegistration = registration
			item.UpdatedAt = now
			registry.UpdatedAt = now
			return nil
		}
		return fmt.Errorf("NOC run registration %s not found", runID)
	})
}

func buildGlobalNOCBootstrap(root, launchID, registryPath string, backstop globalNOCBackstop) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are the amq-squad global/NOC orchestrator for neutral control root %s.\n\n", root)
	fmt.Fprintf(&b, "NOC bootstrap contract version: %d\n", globalNOCBootstrapVersion)
	fmt.Fprintf(&b, "Durable NOC launch id: %s\nRegistry: %s\n", launchID, registryPath)
	fmt.Fprintf(&b, "Stall backstop: interval=%ds timeout=%ds max_ticks=%d. Every sweep is bounded; never author an unbounded watch or polling loop.\n\n", backstop.IntervalSeconds, backstop.TimeoutSeconds, backstop.MaxTicks)
	b.WriteString("Step 1 — Preview and bind scope\n")
	b.WriteString("- Turn each operator goal into an explicit preview with project, profile, session, lead, goal binding, and exact target checkout. Never infer a project tree from this neutral root.\n")
	b.WriteString("- Keep the durable registry as the board source of truth; prose summaries are projections, not state.\n\n")
	b.WriteString("Step 2 — Launch through the project lead\n")
	b.WriteString("- Run amq-squad start from this control root with explicit --project, --profile, and --session. A verified NOC generation defaults the run to external-orchestrator registration; use --no-register-orchestrator only for an intentional polling-only run.\n")
	b.WriteString("- The NOC observes and coordinates. It never adopts the project lead identity or edits the target project directly.\n\n")
	b.WriteString("Step 3 — Monitor, gate, and close\n")
	b.WriteString("- Consume status, tasks, gates, and AMQ as durable evidence. Surface gates and blockers to the operator; never answer or clear a gate on the operator's behalf.\n")
	b.WriteString("- When wake registration is unavailable, keep the run in poll_required and take bounded amq-squad status --json snapshots at the configured cadence. Do not hide degradation and do not create an agent-authored watch loop.\n")
	b.WriteString("- Treat merge, release, destructive filesystem actions, external communications, and provider side effects as operator-gated actions.\n\n")
	b.WriteString("Board protocol\n")
	b.WriteString("- For every registered run track namespace, exact lead, runtime liveness, registration state, open gates and ages, last checked time, last action, next action, and polling fallback. Demote closed runs; never delete evidence to make a board look healthy.\n")
	b.WriteString("- Use explicit root/profile/session arguments for every project command. If registry, identity, or scope evidence is missing or contradictory, do not claim registration; report the run as poll_required and inspect it.\n")
	return b.String()
}

// writeGlobalNOCBootstrapPayload persists the generated bootstrap next to its launch
// generation so the dispatch line can substitute it in rather than TYPE it.
//
// #577 finding 1: the bootstrap is at least 1,783 bytes and the pane tty drops any line over
// MAX_CANON = 1,024, so typing it was a silent loss. Writing it also leaves the exact
// dispatched text on disk beside the digest the registry already records, which makes a lost
// or altered prompt an inspectable fact instead of an inference.
func writeGlobalNOCBootstrapPayload(root, launchID, bootstrap string) (string, error) {
	dir := filepath.Join(root, ".amq-squad", globalNOCRegistryDir, "bootstrap")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create bootstrap payload dir: %w", err)
	}
	path := filepath.Join(dir, launchID+".prompt")
	// 0o600: the bootstrap carries the control root and registry layout. It is not a
	// secret, but it is this operator's control-plane detail and does not need to be
	// world-readable.
	if err := os.WriteFile(path, []byte(bootstrap), 0o600); err != nil {
		return "", fmt.Errorf("write bootstrap payload: %w", err)
	}
	return path, nil
}

// nocPromptDigest is the digest the dispatch line verifies before exec.
//
// Taken over the SUBSTITUTED form of the prompt -- trailing newlines removed -- because that
// is what `$(cat ...)` yields and therefore what the agent actually receives. Digesting the
// raw file instead would mismatch every time and the guard would reject every healthy launch,
// which is the failure mode a guard must not have.
func nocPromptDigest(bootstrap string) string {
	sum := sha256.Sum256([]byte(strings.TrimRight(bootstrap, "\n")))
	return hex.EncodeToString(sum[:])
}
