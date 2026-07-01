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

	taskstore "github.com/omriariav/amq-squad/v2/internal/task"
)

const defaultMonitorInterval = 30 * time.Second

// monitor event types (#295). Structured and additive so #299's watchdog can
// add event types later without breaking consumers.
const (
	monitorEventBlockedTask = "blocked_or_failed_task"
	monitorEventMergeReady  = "merge_gate_ready"
	monitorEventOpenGate    = "open_operator_gate"
	monitorEventInbox       = "operator_inbox_message"
)

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
}

// monitorTick is the per-tick envelope emitted under --json (one NDJSON object
// per line).
type monitorTick struct {
	Kind        string         `json:"kind"`
	Tick        int            `json:"tick"`
	EventsFound bool           `json:"events_found"`
	Events      []monitorEvent `json:"events,omitempty"`
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
	interval := fs.Duration("interval", defaultMonitorInterval, "poll interval in loop mode")
	once := fs.Bool("once", false, "run one tick and exit (no loop)")
	timeout := fs.Duration("timeout", 0, "stop the loop after this long with no operator-needed event (0 = no timeout)")
	maxTicks := fs.Int("max-ticks", 0, "stop the loop after this many idle ticks (0 = unlimited)")
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
	if *timeout < 0 {
		return usageErrorf("--timeout must be >= 0")
	}
	if *maxTicks < 0 {
		return usageErrorf("--max-ticks must be >= 0")
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
		ProjectDir:    projectDir,
		Profile:       profile,
		Sessions:      append([]string(nil), sessions...),
		Handled:       handled,
		FeaturePrefix: strings.TrimSpace(*featurePrefix),
		Interval:      *interval,
		Once:          *once,
		Timeout:       *timeout,
		MaxTicks:      *maxTicks,
		JSON:          *jsonOut,
		Out:           os.Stdout,
	})
}

type monitorLoopOptions struct {
	ProjectDir    string
	Profile       string
	Sessions      []string
	Handled       map[string]bool
	FeaturePrefix string
	Interval      time.Duration
	Once          bool
	Timeout       time.Duration
	MaxTicks      int
	JSON          bool
	Out           io.Writer
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
			return nil
		}
		if o.Once {
			return nil
		}
		if o.MaxTicks > 0 && tick >= o.MaxTicks {
			return nil
		}
		if !deadline.IsZero() && !time.Now().Add(o.Interval).Before(deadline) {
			// The next sleep would reach/pass the deadline: stop cleanly.
			return nil
		}
		timer := time.NewTimer(o.Interval)
		select {
		case <-timer.C:
		case <-sigCh:
			timer.Stop()
			return nil
		}
	}
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
		// 1. Task store: blocked/failed tasks. A missing task dir is empty (nil
		// error); a real read/parse failure fails closed so a broken source is
		// never reported as idle.
		tasks, err := taskstore.ListForProfile(o.ProjectDir, o.Profile, session)
		if err != nil {
			return nil, fmt.Errorf("monitor: read task store for session %q: %w", session, err)
		}
		for _, tk := range tasks {
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
	}
	return events, nil
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
