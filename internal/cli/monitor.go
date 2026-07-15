package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	activitystore "github.com/omriariav/amq-squad/v2/internal/activity"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/state"
	taskstore "github.com/omriariav/amq-squad/v2/internal/task"
	"github.com/omriariav/amq-squad/v2/internal/team"
	"github.com/omriariav/amq-squad/v2/internal/tmuxpane"
)

const (
	defaultMonitorInterval          = 30 * time.Second
	defaultMonitorTimeout           = 30 * time.Minute
	defaultMonitorMaxTicks          = 60
	defaultMonitorCodingStaleAfter  = 30 * time.Minute
	defaultMonitorTestingStaleAfter = 60 * time.Minute
	monitorActivityFutureSkew       = 5 * time.Second
	monitorFinalSchemaVersion       = 1
)

// monitor event types (#295). Structured and additive so #299's watchdog can
// add event types later without breaking consumers.
const (
	monitorEventBlockedTask = "blocked_or_failed_task"
	monitorEventMergeReady  = "merge_gate_ready"
	monitorEventOpenGate    = "open_operator_gate"
	monitorEventInbox       = "operator_inbox_message"
	// monitorEventIdleActiveTask (#299) flags an agent that owns an in_progress
	// task but has halted: either its owner is not live, or it is live-but-idle
	// past --stale-after with no legitimate wait condition. Read-only signal;
	// recovery is the orchestrator's job via the existing controlled wake-first
	// dispatch re-nudge, never monitor itself.
	monitorEventIdleActiveTask = "idle_with_active_task"
)

const defaultMonitorStaleAfter = 15 * time.Minute

// monitorStatusRows resolves per-member liveness for a session (read-only),
// overridable in tests. Returns nil rows when no team/liveness is resolvable;
// the caller treats absent owner liveness conservatively (no flag).
var monitorStatusRows = func(projectDir, profile, session string) ([]statusRecord, error) {
	t, err := team.ReadProfile(projectDir, profile)
	if err != nil {
		return nil, err
	}
	return buildStatusRows(t, profile, session, defaultDuplicateLaunchProbe), nil
}

// monitorPaneBusy reports whether a pane is busy (mid-turn). Best-effort: the
// second return is false when busyness cannot be determined (e.g. tmux access
// denied), in which case the caller must not assume idle. Overridable in tests.
var monitorPaneBusy = func(paneID string) (busy bool, known bool) {
	if strings.TrimSpace(paneID) == "" {
		return false, false
	}
	b, err := tmuxpane.PaneBusy(paneID)
	if err != nil {
		return false, false
	}
	return b, true
}

// monitorEvent is one operator-needed signal. Fields are populated where
// available; absent fields are omitted.
type monitorEvent struct {
	Type    string `json:"type"`
	Session string `json:"session"`
	Source  string `json:"source,omitempty"`
	Thread  string `json:"thread,omitempty"`
	Path    string `json:"path,omitempty"`
	Issue   string `json:"issue,omitempty"`
	Detail  string `json:"detail,omitempty"`
	// Idle carries the liveness/busy evidence behind an idle_with_active_task
	// flag (#299), so a consumer can tell a confirmed not-busy idle from any
	// other state. Present only on idle_with_active_task events.
	Idle *monitorIdleEvidence `json:"idle_evidence,omitempty"`
}

// monitorIdleEvidence records exactly why an idle_with_active_task event fired.
// A live owner is only ever flagged when Busy is positively known false
// (BusyKnown=true, Busy=false); unknown busy state is never classified as idle.
type monitorIdleEvidence struct {
	Owner                     string `json:"owner"`
	OwnerStatus               string `json:"owner_status"`
	Live                      bool   `json:"live"`
	BusyKnown                 bool   `json:"busy_known"`
	Busy                      bool   `json:"busy"`
	PaneID                    string `json:"pane_id,omitempty"`
	UnreadInbox               int    `json:"unread_inbox,omitempty"`
	AgeSeconds                int    `json:"age_seconds"`
	ThresholdSeconds          int    `json:"threshold_seconds"`
	ActivitySource            string `json:"activity_source"`
	ActivityPhase             string `json:"activity_phase,omitempty"`
	ActivityAgeSeconds        int    `json:"activity_age_seconds"`
	ActivityValid             bool   `json:"activity_valid"`
	ActivityReason            string `json:"activity_reason"`
	BaseThresholdSeconds      int    `json:"base_threshold_seconds"`
	EffectiveThresholdSeconds int    `json:"effective_threshold_seconds"`
}

// monitorTick is the per-tick envelope emitted under --json (one NDJSON object
// per line).
type monitorTick struct {
	Kind        string         `json:"kind"`
	Tick        int            `json:"tick"`
	EventsFound bool           `json:"events_found"`
	Events      []monitorEvent `json:"events,omitempty"`
}

// monitorFinal is the stable, self-contained JSON result for one bounded run.
// Consumers should use it instead of reconstructing state from NDJSON order.
type monitorFinal struct {
	SchemaVersion  int            `json:"schema_version"`
	Kind           string         `json:"kind"`
	ExitReason     string         `json:"exit_reason"`
	Ticks          int            `json:"ticks"`
	EventsFound    bool           `json:"events_found"`
	Events         []monitorEvent `json:"events,omitempty"`
	Error          string         `json:"error,omitempty"`
	TimeoutSeconds int            `json:"timeout_seconds"`
	MaxTicks       int            `json:"max_ticks"`
}

type monitorActivityEvidence struct {
	Source    string
	Phase     string
	Age       time.Duration
	Valid     bool
	Reason    string
	Threshold time.Duration
}

// monitorOperatorState is the read-only operator signal seam, overridable in
// tests. It must never claim a lease or consume messages.
var monitorOperatorState = func(projectDir, profile, session string) (openGates int, unread int, err error) {
	data, err := buildOperatorStatusData(operatorExecution{
		ProjectDir: projectDir,
		Profile:    profile,
		Session:    session,
		ReadOnly:   true,
	})
	if err != nil {
		return 0, 0, err
	}
	if data.Operator.Poll != nil {
		return data.Operator.Poll.OpenGates, data.Operator.Poll.Unread, nil
	}
	return 0, 0, nil
}

func runMonitor(args []string) error {
	fs := flag.NewFlagSet("monitor", flag.ContinueOnError)
	projectFlag := fs.String("project", "", "project/team-home directory to inspect (default: cwd)")
	profileFlag := fs.String("profile", "", "team profile to inspect (default: default profile)")
	var sessions stringListFlag
	fs.Var(&sessions, "session", "workstream session to watch (repeatable)")
	fs.Var(&sessions, "s", "alias for --session")
	registerScopedFlagAliases(fs, projectFlag, nil, profileFlag)
	interval := fs.Duration("interval", defaultMonitorInterval, "poll interval in loop mode")
	staleAfter := fs.Duration("stale-after", defaultMonitorStaleAfter, "base freshness threshold for an active task or enumerated activity phase")
	codingStaleAfter := fs.Duration("coding-stale-after", defaultMonitorCodingStaleAfter, "extended freshness threshold for an exact coding heartbeat")
	testingStaleAfter := fs.Duration("testing-stale-after", defaultMonitorTestingStaleAfter, "extended freshness threshold for an exact testing heartbeat")
	once := fs.Bool("once", false, "run one tick and exit (no loop)")
	timeout := fs.Duration("timeout", defaultMonitorTimeout, "stop the loop after this duration (0 disables the deadline only when max-ticks remains finite)")
	maxTicks := fs.Int("max-ticks", defaultMonitorMaxTicks, "stop the loop after this many idle ticks (0 disables this bound only when timeout remains finite)")
	var handledIssues stringListFlag
	fs.Var(&handledIssues, "handled-issue", "issue number already handled; suppresses its merge_gate_ready event (repeatable)")
	featurePrefix := fs.String("feature-prefix", "", "feature-branch prefix context recorded on merge_gate_ready events (does not suppress anything)")
	jsonOut := fs.Bool("json", false, "emit one NDJSON monitor tick per line")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad monitor - no-wake polling loop for operator-needed events

Usage:
  amq-squad monitor --session S [--session S2 ...] [--once] [--interval D] [--timeout D] [--max-ticks N] [--handled-issue N ...] [--feature-prefix P] [--json]

The no-wake companion to wake-driven orchestration: instead of a hand-rolled
shell loop, monitor polls (read-only) the task store, the evidence dir, open
operator gates, and the operator inbox, and surfaces structured operator-needed
events. It exits as soon as the first event fires (the no-wake pull-back), so a
Claude-led local orchestrator can be reliably pulled back exactly when a
gate/blocker/merge-ready/inbox event needs it.

monitor is strictly READ-ONLY: it never answers or clears gates, marks messages
read, mutates tasks, dispatches, wakes panes, pushes, merges, tags, or releases.
It surfaces work; the operator/lead acts on it.

Bounds (no unbounded busy loop): --once runs a single tick; in loop mode pass
--timeout and/or --max-ticks. Exit is 0 whether an event fired or the run idled
out cleanly; the output distinguishes them (events_found / Events). Errors exit
non-zero.

Examples:
  amq-squad monitor --session v2-14-0 --once --json
  amq-squad monitor --session v2-14-0 --interval 30s --timeout 30m
  amq-squad monitor --session v2-14-0 --handled-issue 282 --handled-issue 287 --once
`)
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return usageErrorf("monitor takes no positional arguments; use --session")
	}
	if len(sessions) == 0 {
		return usageErrorf("monitor requires at least one --session")
	}
	if *interval <= 0 {
		return usageErrorf("--interval must be > 0")
	}
	if *staleAfter <= 0 {
		return usageErrorf("--stale-after must be > 0")
	}
	if *codingStaleAfter < *staleAfter {
		return usageErrorf("--coding-stale-after must be >= --stale-after")
	}
	if *testingStaleAfter < *staleAfter {
		return usageErrorf("--testing-stale-after must be >= --stale-after")
	}
	if *timeout < 0 {
		return usageErrorf("--timeout must be >= 0")
	}
	if *maxTicks < 0 {
		return usageErrorf("--max-ticks must be >= 0")
	}
	if !*once && *timeout == 0 && *maxTicks == 0 {
		return usageErrorf("monitor loop must be bounded: set --timeout and/or --max-ticks")
	}
	projectDir, profile, err := resolveProjectProfile(*projectFlag, *profileFlag, flagWasSet(fs, "project"))
	if err != nil {
		return err
	}
	handled := make(map[string]bool, len(handledIssues))
	for _, h := range handledIssues {
		handled[strings.TrimSpace(h)] = true
	}

	return runMonitorLoop(monitorLoopOptions{
		ProjectDir:        projectDir,
		Profile:           profile,
		Sessions:          append([]string(nil), sessions...),
		Handled:           handled,
		FeaturePrefix:     strings.TrimSpace(*featurePrefix),
		StaleAfter:        *staleAfter,
		CodingStaleAfter:  *codingStaleAfter,
		TestingStaleAfter: *testingStaleAfter,
		Interval:          *interval,
		Once:              *once,
		Timeout:           *timeout,
		MaxTicks:          *maxTicks,
		JSON:              *jsonOut,
		Out:               os.Stdout,
	})
}

type monitorLoopOptions struct {
	ProjectDir        string
	Profile           string
	Sessions          []string
	Handled           map[string]bool
	FeaturePrefix     string
	StaleAfter        time.Duration
	CodingStaleAfter  time.Duration
	TestingStaleAfter time.Duration
	Interval          time.Duration
	Once              bool
	Timeout           time.Duration
	MaxTicks          int
	JSON              bool
	Out               io.Writer
}

func runMonitorLoop(o monitorLoopOptions) error {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	deadline := time.Time{}
	if o.Timeout > 0 {
		deadline = time.Now().Add(o.Timeout)
	}
	for tick := 1; ; tick++ {
		events, err := collectMonitorEvents(o)
		if err != nil {
			// Fail closed: a broken required source is an error, never an idle
			// tick. Emit a clear error record (JSON or human) and exit non-zero.
			writeMonitorError(o.Out, tick, err, o.JSON)
			_ = writeMonitorFinal(o.Out, monitorFinalFor(o, "source_error", tick-1, nil, err), o.JSON)
			return err
		}
		if err := writeMonitorTick(o.Out, monitorTick{
			Kind:        "monitor_tick",
			Tick:        tick,
			EventsFound: len(events) > 0,
			Events:      events,
		}, o.JSON); err != nil {
			return err
		}
		if len(events) > 0 {
			// Exit-on-first-event: pull the no-wake orchestrator back now.
			if err := writeMonitorFinal(o.Out, monitorFinalFor(o, "event", tick, events, nil), o.JSON); err != nil {
				return err
			}
			return nil
		}
		if o.Once {
			if err := writeMonitorFinal(o.Out, monitorFinalFor(o, "once", tick, events, nil), o.JSON); err != nil {
				return err
			}
			return nil
		}
		if o.MaxTicks > 0 && tick >= o.MaxTicks {
			if err := writeMonitorFinal(o.Out, monitorFinalFor(o, "max_ticks", tick, events, nil), o.JSON); err != nil {
				return err
			}
			return nil
		}
		if !deadline.IsZero() && !time.Now().Add(o.Interval).Before(deadline) {
			// The next sleep would reach/pass the deadline: stop cleanly.
			if err := writeMonitorFinal(o.Out, monitorFinalFor(o, "timeout", tick, events, nil), o.JSON); err != nil {
				return err
			}
			return nil
		}
		timer := time.NewTimer(o.Interval)
		select {
		case <-timer.C:
		case <-sigCh:
			timer.Stop()
			if err := writeMonitorFinal(o.Out, monitorFinalFor(o, "signal", tick, events, nil), o.JSON); err != nil {
				return err
			}
			return nil
		}
	}
}

func monitorFinalFor(o monitorLoopOptions, reason string, ticks int, events []monitorEvent, runErr error) monitorFinal {
	final := monitorFinal{
		SchemaVersion: monitorFinalSchemaVersion,
		Kind:          "monitor_final", ExitReason: reason, Ticks: ticks,
		EventsFound: len(events) > 0, Events: events,
		TimeoutSeconds: int(o.Timeout / time.Second), MaxTicks: o.MaxTicks,
	}
	if runErr != nil {
		final.Error = runErr.Error()
	}
	return final
}

func writeMonitorFinal(out io.Writer, final monitorFinal, jsonOut bool) error {
	if !jsonOut {
		if final.Error != "" {
			return nil
		}
		fmt.Fprintf(out, "final: %s after %d tick(s); events_found=%t\n", final.ExitReason, final.Ticks, final.EventsFound)
		return nil
	}
	b, err := json.Marshal(final)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, string(b))
	return err
}

// collectMonitorEvents gathers operator-needed events across all watched
// sessions using only read-only, non-consuming readers.
func collectMonitorEvents(o monitorLoopOptions) ([]monitorEvent, error) {
	var events []monitorEvent
	for _, session := range o.Sessions {
		session = strings.TrimSpace(session)
		if session == "" {
			continue
		}
		sessionEventStart := len(events)
		// 1. Task store: blocked/failed tasks. A missing task dir is empty (nil
		// error); a real read/parse failure fails closed so a broken source is
		// never reported as idle.
		tasks, err := taskstore.ListForProfile(o.ProjectDir, o.Profile, session)
		if err != nil {
			return nil, fmt.Errorf("monitor: read task store for session %q: %w", session, err)
		}
		for _, tk := range tasks {
			if taskstore.IsAttentionLifecycleTerminal(tk) {
				continue
			}
			if tk.Status == taskstore.StatusBlocked || tk.Status == taskstore.StatusFailed {
				detail := tk.Title
				if r := strings.TrimSpace(tk.BlockReason + tk.FailureReason); r != "" {
					detail = tk.Title + " — " + r
				}
				events = append(events, monitorEvent{
					Type:    monitorEventBlockedTask,
					Session: session,
					Source:  "task:" + tk.ID + " (" + tk.Status + ")",
					Detail:  detail,
				})
			}
		}
		// 2. Evidence dir: merge evidence for an unhandled issue.
		mergeEvents, err := monitorMergeReadyEvents(o, session)
		if err != nil {
			return nil, fmt.Errorf("monitor: scan evidence dir for session %q: %w", session, err)
		}
		events = append(events, mergeEvents...)
		// 3 + 4. Open operator gates and operator inbox (read-only).
		openGates, unread, err := monitorOperatorState(o.ProjectDir, o.Profile, session)
		if err != nil {
			return nil, fmt.Errorf("monitor: inspect operator gates/inbox for session %q: %w", session, err)
		}
		if openGates > 0 {
			events = append(events, monitorEvent{
				Type:    monitorEventOpenGate,
				Session: session,
				Source:  "operator",
				Detail:  fmt.Sprintf("%d open operator gate(s) awaiting a decision", openGates),
			})
		}
		if unread > 0 {
			events = append(events, monitorEvent{
				Type:    monitorEventInbox,
				Session: session,
				Source:  "operator",
				Detail:  fmt.Sprintf("%d unread operator inbox message(s)", unread),
			})
		}
		// 5. #299 idle-with-active-task. Suppress when this session already has an
		// operator-needed event (a gate, blocker, merge-ready, or queued inbox
		// message already pulls the operator/orchestrator back — adding an idle
		// flag would be noise and the recovery path is already implied).
		if len(events) == sessionEventStart {
			idle, err := idleWithActiveTaskEvents(o, session, tasks)
			if err != nil {
				return nil, fmt.Errorf("monitor: assess idle-with-active-task for session %q: %w", session, err)
			}
			events = append(events, idle...)
		}
	}
	return events, nil
}

// idleWithActiveTaskEvents flags an agent that owns an in_progress task but has
// halted (#299). It only reaches here when the session has no other
// operator-needed event this tick. False-positive controls: a busy/mid-turn
// owner is never flagged; a fresh in_progress task (within --stale-after) is not
// flagged; an owner whose liveness cannot be resolved is treated conservatively
// (not flagged). A not-live owner with an active task is flagged regardless of
// age; a live-but-idle owner is flagged only when the task is stale past
// --stale-after. Read-only: it suggests a controlled recovery but performs none.
func idleWithActiveTaskEvents(o monitorLoopOptions, session string, tasks []taskstore.Task) ([]monitorEvent, error) {
	var inProgress []taskstore.Task
	for _, tk := range tasks {
		if tk.Status == taskstore.StatusInProgress || tk.Status == taskstore.StatusCompletedPendingReconcile {
			inProgress = append(inProgress, tk)
		}
	}
	if len(inProgress) == 0 {
		return nil, nil
	}
	rows, err := monitorStatusRows(o.ProjectDir, o.Profile, session)
	if err != nil {
		return nil, err
	}
	byHandle := make(map[string]statusRecord, len(rows))
	for _, r := range rows {
		byHandle[strings.TrimSpace(r.Handle)] = r
	}
	now := time.Now()
	unreadByOwner := monitorUnreadInboxCounts(o.ProjectDir, o.Profile, session, now)
	var out []monitorEvent
	for _, tk := range inProgress {
		owner := strings.TrimSpace(tk.AssignedTo)
		if owner == "" {
			continue // unowned in_progress: cannot attribute a stall conservatively
		}
		row, ok := byHandle[owner]
		if !ok {
			continue // owner liveness unknown: do not flag
		}
		live := row.Status == statusStateLive || row.Status == statusStateWakeLive
		age := now.Sub(tk.UpdatedAt)
		activityEvidence := monitorActivityForTask(o, session, tk, now)
		paneID := ""
		busy, busyKnown := false, false
		if row.Tmux != nil && row.Tmux.PaneAlive {
			paneID = row.Tmux.PaneID
			busy, busyKnown = monitorPaneBusy(paneID)
		}
		var reason string
		if live {
			// A live owner is flagged ONLY with positive evidence it is not busy
			// (busyKnown && !busy) AND a stale task. Busy/mid-turn is never idle,
			// and UNKNOWN busy state is treated conservatively as not-idle so an
			// owner we cannot inspect is never a false positive.
			if !busyKnown {
				continue // cannot confirm not-busy: do not classify as idle
			}
			if busy {
				continue // busy/mid-turn
			}
			if age < o.StaleAfter {
				continue // confirmed not-busy but task is fresh
			}
			if activityEvidence.Valid && activityEvidence.Age <= activityEvidence.Threshold {
				continue // exact bounded phase heartbeat proves current progress
			}
			reason = fmt.Sprintf("owner %q is live, confirmed not busy, and the task is untouched for %s", owner, age.Round(time.Second))
		} else {
			// A valid exact heartbeat is stronger than a stale liveness projection.
			// Otherwise a not-live owner still holding the task is flagged regardless
			// of task age; busy is irrelevant (and was not probed).
			if activityEvidence.Valid && activityEvidence.Age <= activityEvidence.Threshold {
				continue
			}
			reason = fmt.Sprintf("owner %q is %s while owning an in_progress task", owner, row.Status)
		}
		unread := unreadByOwner[owner]
		inboxDetail := ""
		if unread > 0 {
			inboxDetail = fmt.Sprintf("; owner has %d unread inbox message(s)", unread)
		}
		out = append(out, monitorEvent{
			Type:    monitorEventIdleActiveTask,
			Session: session,
			Source:  "task:" + tk.ID + " owner:" + owner + " status:" + string(row.Status),
			Detail: fmt.Sprintf("%s — %s%s (task age %s; activity %s phase=%q age=%s validity=%s; base threshold %s; effective threshold %s). Suggested: controlled wake-first re-nudge (amq-squad dispatch) to resume task %s; escalate to operator after repeated no-advance.",
				tk.Title, reason, inboxDetail, age.Round(time.Second), activityEvidence.Source, activityEvidence.Phase, activityAgeDetail(activityEvidence.Age), activityEvidence.Reason, o.StaleAfter, activityEvidence.Threshold, tk.ID),
			Idle: &monitorIdleEvidence{
				Owner: owner, OwnerStatus: string(row.Status), Live: live,
				BusyKnown: busyKnown, Busy: busy, PaneID: paneID, UnreadInbox: unread,
				AgeSeconds: int(age / time.Second), ThresholdSeconds: int(activityEvidence.Threshold / time.Second),
				ActivitySource: activityEvidence.Source, ActivityPhase: activityEvidence.Phase,
				ActivityAgeSeconds: int(activityEvidence.Age / time.Second), ActivityValid: activityEvidence.Valid,
				ActivityReason:            activityEvidence.Reason,
				BaseThresholdSeconds:      int(o.StaleAfter / time.Second),
				EffectiveThresholdSeconds: int(activityEvidence.Threshold / time.Second),
			},
		})
	}
	return out, nil
}

func monitorActivityForTask(o monitorLoopOptions, session string, tk taskstore.Task, now time.Time) monitorActivityEvidence {
	base := o.StaleAfter
	if base <= 0 {
		base = defaultMonitorStaleAfter
	}
	evidence := monitorActivityEvidence{Source: "none", Age: -time.Second, Reason: "heartbeat absent", Threshold: base}
	owner := strings.TrimSpace(tk.AssignedTo)
	agentDir := filepath.Join(squadnamespace.AMQRoot(o.ProjectDir, o.Profile, session), "agents", owner)
	snapshot, ok, err := activitystore.Read(agentDir, now, base)
	if err != nil {
		evidence.Source = activitystore.SourceHeartbeat
		evidence.Reason = "heartbeat malformed: " + err.Error()
		return evidence
	}
	if !ok {
		return evidence
	}
	evidence.Source = snapshot.Source
	evidence.Phase = strings.TrimSpace(snapshot.Phase)
	if snapshot.WrittenAt.IsZero() {
		evidence.Reason = "heartbeat timestamp missing"
		return evidence
	}
	evidence.Age = now.Sub(snapshot.WrittenAt)
	if snapshot.WrittenAt.After(now.Add(monitorActivityFutureSkew)) {
		evidence.Reason = "heartbeat timestamp is future-skewed"
		return evidence
	}
	if evidence.Age < 0 {
		evidence.Age = 0
	}
	if strings.TrimSpace(snapshot.Handle) != owner {
		evidence.Reason = "heartbeat handle does not match exact assignee"
		return evidence
	}
	if strings.TrimSpace(snapshot.TaskID) != strings.TrimSpace(tk.ID) {
		evidence.Reason = "heartbeat task does not match canonical task"
		return evidence
	}
	threshold, ok := monitorActivityPhaseThreshold(o, evidence.Phase)
	if !ok {
		evidence.Reason = "heartbeat phase is not in the bounded phase catalog"
		return evidence
	}
	evidence.Valid = true
	evidence.Reason = "exact namespace, assignee, task, phase, and timestamp binding"
	evidence.Threshold = threshold
	return evidence
}

func monitorActivityPhaseThreshold(o monitorLoopOptions, phase string) (time.Duration, bool) {
	base := o.StaleAfter
	if base <= 0 {
		base = defaultMonitorStaleAfter
	}
	switch strings.TrimSpace(phase) {
	case "coding":
		threshold := o.CodingStaleAfter
		if threshold < base {
			threshold = defaultMonitorCodingStaleAfter
		}
		return threshold, true
	case "testing":
		threshold := o.TestingStaleAfter
		if threshold < base {
			threshold = defaultMonitorTestingStaleAfter
		}
		return threshold, true
	case "reading", "planning", "review", "waiting", "waiting-on-command", "task_claimed", "task_in_progress":
		return base, true
	default:
		return base, false
	}
}

func activityAgeDetail(age time.Duration) string {
	if age < 0 {
		return "unavailable"
	}
	return age.Round(time.Second).String()
}

// monitorUnreadInboxCounts records durable messages still sitting unread in
// member inboxes. It is evidence for #309's pane-nudge corruption shape: the
// worker is idle while the AMQ message that should have been drained remains in
// inbox/new. This intentionally does not try to prevent terminal/model
// tool-call emission corruption; monitor's read-only contract is to surface the
// recoverable stalled state. Liveness and busy checks still decide whether the
// owner is idle.
func monitorUnreadInboxCounts(projectDir, profile, session string, now time.Time) map[string]int {
	root := squadnamespace.AMQRoot(projectDir, profile, session)
	if root == "" {
		return nil
	}
	msgs, _ := state.ScanSessionMessages(root, func() time.Time { return now })
	out := make(map[string]int)
	for _, m := range msgs {
		if m.State == state.MailboxNew {
			out[strings.TrimSpace(m.Owner)]++
		}
	}
	return out
}

// monitorMergeReadyEvents scans the evidence dir for this session's
// *-merge-evidence.json files and emits a merge_gate_ready event for each whose
// issue is not in the handled set. Read-only filesystem scan; no provider calls.
func monitorMergeReadyEvents(o monitorLoopOptions, session string) ([]monitorEvent, error) {
	dir := filepath.Join(o.ProjectDir, ".amq-squad", "evidence")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			// No evidence dir yet is a legitimately-empty optional source, not a
			// read failure.
			return nil, nil
		}
		return nil, err
	}
	prefix := session + "-"
	const suffix = "-merge-evidence.json"
	var out []monitorEvent
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, suffix) {
			continue
		}
		issue := strings.TrimSuffix(strings.TrimPrefix(name, prefix), suffix)
		if o.Handled[issue] {
			continue
		}
		detail := "merge evidence prepared; awaiting operator merge gate"
		if o.FeaturePrefix != "" {
			detail += " (feature-prefix " + o.FeaturePrefix + ")"
		}
		out = append(out, monitorEvent{
			Type:    monitorEventMergeReady,
			Session: session,
			Source:  "evidence",
			Path:    filepath.Join(dir, name),
			Issue:   issue,
			Detail:  detail,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// writeMonitorError emits a clear, distinct error record (NOT an idle tick) when
// a required source cannot be read. Best-effort: the non-zero exit comes from the
// returned error regardless.
func writeMonitorError(out io.Writer, tick int, err error, jsonOut bool) {
	if jsonOut {
		b, mErr := json.Marshal(struct {
			Kind  string `json:"kind"`
			Tick  int    `json:"tick"`
			Error string `json:"error"`
		}{Kind: "monitor_error", Tick: tick, Error: err.Error()})
		if mErr == nil {
			fmt.Fprintln(out, string(b))
		}
		return
	}
	fmt.Fprintf(out, "tick %d: ERROR (source unavailable, not idle): %v\n", tick, err)
}

func writeMonitorTick(out io.Writer, tick monitorTick, jsonOut bool) error {
	if jsonOut {
		b, err := json.Marshal(tick)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(out, string(b))
		return err
	}
	if !tick.EventsFound {
		_, err := fmt.Fprintf(out, "tick %d: idle (no operator-needed events)\n", tick.Tick)
		return err
	}
	if _, err := fmt.Fprintf(out, "tick %d: %d operator-needed event(s):\n", tick.Tick, len(tick.Events)); err != nil {
		return err
	}
	for _, ev := range tick.Events {
		line := "- [" + ev.Type + "] session " + ev.Session
		if ev.Source != "" {
			line += " · " + ev.Source
		}
		if ev.Issue != "" {
			line += " · issue " + ev.Issue
		}
		if ev.Detail != "" {
			line += " — " + ev.Detail
		}
		if _, err := fmt.Fprintln(out, line); err != nil {
			return err
		}
	}
	return nil
}
