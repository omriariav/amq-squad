package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/attention"
	"github.com/omriariav/amq-squad/v2/internal/notifier"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

const (
	defaultNotificationsHistoryLimit = 50
	maxNotificationsHistoryLimit     = 200
)

type notificationPolicySinkView struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Timeout string `json:"timeout"`
}

type notificationPolicyView struct {
	Enabled           bool                         `json:"enabled"`
	DeliverySemantics string                       `json:"delivery_semantics"`
	Events            []string                     `json:"events"`
	Sinks             []notificationPolicySinkView `json:"sinks"`
}

type notificationReservationView struct {
	Present   bool      `json:"present"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
	Active    bool      `json:"active"`
}

type notificationDeliveryView struct {
	SinkID         string                      `json:"sink_id"`
	Fingerprint    string                      `json:"fingerprint,omitempty"`
	Reservation    notificationReservationView `json:"reservation"`
	LastAttempt    time.Time                   `json:"last_attempt,omitempty"`
	LastSuccess    time.Time                   `json:"last_success,omitempty"`
	LastFailure    time.Time                   `json:"last_failure,omitempty"`
	FailureCount   int                         `json:"failure_count"`
	LastError      string                      `json:"last_error,omitempty"`
	LastNotified   time.Time                   `json:"last_notified,omitempty"`
	LastEscalation string                      `json:"last_escalation,omitempty"`
}

type notificationEventView struct {
	Key          string                     `json:"key"`
	Profile      string                     `json:"profile"`
	Session      string                     `json:"session"`
	EventType    string                     `json:"event_type"`
	Target       string                     `json:"target,omitempty"`
	LatestID     string                     `json:"latest_id,omitempty"`
	Fingerprint  string                     `json:"fingerprint,omitempty"`
	Active       bool                       `json:"active"`
	LastObserved time.Time                  `json:"last_observed,omitempty"`
	RecentAt     time.Time                  `json:"recent_at,omitempty"`
	Deliveries   []notificationDeliveryView `json:"deliveries"`
}

type notificationsStateView struct {
	Path              string                  `json:"path"`
	Schema            int                     `json:"schema"`
	PendingEventCount int                     `json:"pending_event_count"`
	TotalEventCount   int                     `json:"total_event_count"`
	ShownEventCount   int                     `json:"shown_event_count"`
	Truncated         bool                    `json:"truncated"`
	Events            []notificationEventView `json:"events"`
}

type notificationWatcherView struct {
	PolicyEnabled  bool      `json:"policy_enabled"`
	Expected       bool      `json:"expected"`
	Running        bool      `json:"running"`
	Health         string    `json:"health"`
	Reason         string    `json:"reason,omitempty"`
	RuntimePath    string    `json:"runtime_path"`
	SchemaVersion  int       `json:"schema_version,omitempty"`
	PID            int       `json:"pid,omitempty"`
	Host           string    `json:"host,omitempty"`
	Owner          string    `json:"owner,omitempty"`
	LeaseTTL       string    `json:"lease_ttl,omitempty"`
	LeaseExpiresAt time.Time `json:"lease_expires_at,omitempty"`
	HeartbeatAt    time.Time `json:"heartbeat_at,omitempty"`
	LastScanAt     time.Time `json:"last_scan_at,omitempty"`
	LastEventAt    time.Time `json:"last_event_at,omitempty"`
	StatePath      string    `json:"state_path,omitempty"`
	LastError      string    `json:"last_error,omitempty"`
}

type notificationsDoctorData struct {
	ProjectDir string                  `json:"project_dir"`
	Profile    string                  `json:"profile"`
	Session    string                  `json:"session"`
	Healthy    bool                    `json:"healthy"`
	Degraded   bool                    `json:"degraded"`
	Policy     notificationPolicyView  `json:"policy"`
	Watcher    notificationWatcherView `json:"watcher"`
	State      notificationsStateView  `json:"state"`
}

type notificationsHistoryData struct {
	ProjectDir string                 `json:"project_dir"`
	Profile    string                 `json:"profile"`
	Session    string                 `json:"session,omitempty"`
	State      notificationsStateView `json:"state"`
}

type notificationsProbeData struct {
	ProjectDir string                     `json:"project_dir"`
	Profile    string                     `json:"profile"`
	Session    string                     `json:"session"`
	ProbeID    string                     `json:"probe_id"`
	Sink       notificationPolicySinkView `json:"sink"`
	Success    bool                       `json:"success"`
	Latency    string                     `json:"latency"`
	LatencyMS  int64                      `json:"latency_ms"`
	Receipt    string                     `json:"receipt,omitempty"`
	Error      string                     `json:"error,omitempty"`
}

type notificationsDoctorExecution struct {
	ProjectDir string
	Profile    string
	Session    string
	StatePath  string
	Limit      int
	JSON       bool
	Out        io.Writer
	Now        func() time.Time
}

type notificationsHistoryExecution struct {
	ProjectDir string
	Profile    string
	Session    string
	StatePath  string
	Limit      int
	JSON       bool
	Out        io.Writer
	Now        func() time.Time
}

type notificationsProbeExecution struct {
	Context    context.Context
	ProjectDir string
	Profile    string
	Session    string
	SinkID     string
	JSON       bool
	Out        io.Writer
	Now        func() time.Time
}

// notificationReceiptSink is an optional extension used by sinks that can
// return a provider acknowledgment. Existing desktop and command sinks only
// report success/failure, so receipt stays omitted for them.
type notificationReceiptSink interface {
	notifier.Sink
	DeliverWithReceipt(context.Context, attention.Event) (string, error)
}

func runNotifications(args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		printNotificationsUsage()
		if len(args) == 0 {
			return usageErrorf("notifications requires a subcommand: doctor, probe, events, or history")
		}
		return flag.ErrHelp
	}
	switch args[0] {
	case "doctor":
		return runNotificationsDoctor(args[1:])
	case "probe":
		return runNotificationsProbe(args[1:])
	case "events", "history":
		return runNotificationsHistory(args[0], args[1:])
	default:
		return usageErrorf("unknown notifications subcommand %q: use doctor, probe, events, or history", args[0])
	}
}

func printNotificationsUsage() {
	fmt.Fprint(os.Stderr, `amq-squad notifications - inspect and probe notification delivery

Usage:
  amq-squad notifications doctor [options]
  amq-squad notifications probe [options]
  amq-squad notifications events [options]
  amq-squad notifications history [options]

The events and history subcommands are aliases for the same bounded delivery
history view. All commands are read-only except probe, which sends one unique
in-memory attention event directly to one configured sink. Probe never creates
or answers an operator gate.

Examples:
  amq-squad notifications doctor --session issue-435
  amq-squad notifications probe --sink desktop --json
  amq-squad notifications events --limit 25
`)
}

func runNotificationsDoctor(args []string) error {
	fs := flag.NewFlagSet("notifications doctor", flag.ContinueOnError)
	projectFlag := fs.String("project", "", "project/team-home directory to inspect (default: cwd)")
	profileFlag := fs.String("profile", "", "team profile to inspect (default: default profile)")
	sessionFlag := fs.String("session", "", "notification watcher workstream (default: resolved team workstream)")
	limit := fs.Int("limit", defaultNotificationsHistoryLimit, "maximum recent events to include (1-200)")
	jsonOut := fs.Bool("json", false, "emit a schema-versioned notifications_doctor envelope")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad notifications doctor - inspect notification policy, watcher, and delivery state

Usage:
  amq-squad notifications doctor [--project DIR] [--profile NAME]
                                   [--session NAME] [--limit 50] [--json]

Reports effective policy and sinks, watcher lease/heartbeat/scan health, state
schema and pending count, plus recent per-event/per-sink delivery evidence.

Examples:
  amq-squad notifications doctor
  amq-squad notifications doctor --session issue-435 --json
`)
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return usageErrorf("notifications doctor takes no positional arguments; got %d", fs.NArg())
	}
	projectDir, profile, tm, err := resolveNotificationsTeam(*projectFlag, flagWasSet(fs, "project"), *profileFlag)
	if err != nil {
		return err
	}
	session, err := resolveTeamWorkstreamName(tm, strings.TrimSpace(*sessionFlag), strings.TrimSpace(*sessionFlag) != "")
	if err != nil {
		return err
	}
	return executeNotificationsDoctor(notificationsDoctorExecution{ProjectDir: projectDir, Profile: profile, Session: session, Limit: *limit, JSON: *jsonOut, Out: os.Stdout})
}

func runNotificationsProbe(args []string) error {
	fs := flag.NewFlagSet("notifications probe", flag.ContinueOnError)
	projectFlag := fs.String("project", "", "project/team-home directory to inspect (default: cwd)")
	profileFlag := fs.String("profile", "", "team profile to inspect (default: default profile)")
	sessionFlag := fs.String("session", "", "workstream label included in the probe (default: resolved team workstream)")
	sinkID := fs.String("sink", "", "configured sink ID (optional only when exactly one sink is configured)")
	jsonOut := fs.Bool("json", false, "emit a schema-versioned notifications_probe envelope")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad notifications probe - deliver one in-memory diagnostic event

Usage:
  amq-squad notifications probe [--project DIR] [--profile NAME]
                                  [--session NAME] [--sink ID] [--json]

The probe is sent directly to one configured sink and reports latency plus a
receipt when the sink provides one. It does not write AMQ state or create a gate.

Examples:
  amq-squad notifications probe --sink desktop
  amq-squad notifications probe --profile review --sink audit --json
`)
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return usageErrorf("notifications probe takes no positional arguments; got %d", fs.NArg())
	}
	projectDir, profile, tm, err := resolveNotificationsTeam(*projectFlag, flagWasSet(fs, "project"), *profileFlag)
	if err != nil {
		return err
	}
	session, err := resolveTeamWorkstreamName(tm, strings.TrimSpace(*sessionFlag), strings.TrimSpace(*sessionFlag) != "")
	if err != nil {
		return err
	}
	return executeNotificationsProbe(notificationsProbeExecution{ProjectDir: projectDir, Profile: profile, Session: session, SinkID: *sinkID, JSON: *jsonOut, Out: os.Stdout})
}

func runNotificationsHistory(verb string, args []string) error {
	fs := flag.NewFlagSet("notifications "+verb, flag.ContinueOnError)
	projectFlag := fs.String("project", "", "project/team-home directory to inspect (default: cwd)")
	profileFlag := fs.String("profile", "", "team profile to inspect (default: default profile)")
	sessionFlag := fs.String("session", "", "scope history to one workstream")
	limit := fs.Int("limit", defaultNotificationsHistoryLimit, "maximum recent events to return (1-200)")
	jsonOut := fs.Bool("json", false, "emit a schema-versioned notifications_history envelope")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `amq-squad notifications %s - show bounded recent notification delivery state

Usage:
  amq-squad notifications %s [--project DIR] [--profile NAME]
                              [--session NAME] [--limit 50] [--json]

Events are ordered by their most recent observation or delivery attempt. The
view includes per-sink fingerprints, reservations, attempts, successes,
failures, failure counts, and bounded last-error text.

Examples:
  amq-squad notifications %s --limit 25
  amq-squad notifications %s --session issue-435 --json
`, verb, verb, verb, verb)
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return usageErrorf("notifications %s takes no positional arguments; got %d", verb, fs.NArg())
	}
	projectDir, profile, _, err := resolveNotificationsTeam(*projectFlag, flagWasSet(fs, "project"), *profileFlag)
	if err != nil {
		return err
	}
	return executeNotificationsHistory(notificationsHistoryExecution{ProjectDir: projectDir, Profile: profile, Session: strings.TrimSpace(*sessionFlag), Limit: *limit, JSON: *jsonOut, Out: os.Stdout})
}

func resolveNotificationsTeam(project string, projectExplicit bool, profileValue string) (string, string, team.Team, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", "", team.Team{}, fmt.Errorf("getwd: %w", err)
	}
	projectDir, err := resolveProjectDirFlag(cwd, project, projectExplicit)
	if err != nil {
		return "", "", team.Team{}, err
	}
	profile, err := resolveProfileFlag(profileValue)
	if err != nil {
		return "", "", team.Team{}, err
	}
	tm, err := team.ReadProfile(projectDir, profile)
	if err != nil {
		return "", "", team.Team{}, fmt.Errorf("read team profile %q: %w", profile, err)
	}
	return projectDir, profile, tm, nil
}

func executeNotificationsDoctor(n notificationsDoctorExecution) error {
	out := n.Out
	if out == nil {
		out = os.Stdout
	}
	now := notificationsNow(n.Now)
	limit, err := validateNotificationsLimit(n.Limit)
	if err != nil {
		return err
	}
	tm, err := team.ReadProfile(n.ProjectDir, n.Profile)
	if err != nil {
		return fmt.Errorf("read team profile %q: %w", n.Profile, err)
	}
	policy := team.EffectiveOperatorNotifications(tm.Operator)
	watcher := inspectNotificationWatcher(tm, n.Profile, n.Session, now)
	statePath := strings.TrimSpace(n.StatePath)
	if statePath == "" {
		statePath = defaultNotifyStatePath(n.ProjectDir)
	}
	st, err := readNotifyState(statePath)
	if err != nil {
		return err
	}
	stateView := buildNotificationsStateView(statePath, st, n.Profile, n.Session, limit, now, policy)
	watcherView := buildNotificationWatcherView(policy.Enabled, watcher, now)
	degraded := policy.Enabled && watcher.Health == "degraded" && !watcher.LastScanAt.IsZero()
	healthy := !policy.Enabled || watcher.Health == "healthy" || watcher.Health == "external-active" || watcher.Health == "inactive" || degraded
	data := notificationsDoctorData{ProjectDir: n.ProjectDir, Profile: n.Profile, Session: n.Session, Healthy: healthy, Degraded: degraded, Policy: buildNotificationPolicyView(policy), Watcher: watcherView, State: stateView}
	if n.JSON {
		err = writeJSONEnvelope(out, "notifications_doctor", data)
	} else {
		err = renderNotificationsDoctor(out, data)
	}
	if err != nil {
		return err
	}
	if !healthy {
		return fmt.Errorf("%s", attention.NormalizeDeliveryError(fmt.Sprintf("notifications doctor: watcher %s: %s", watcher.Health, watcherView.Reason)))
	}
	return nil
}

func executeNotificationsHistory(n notificationsHistoryExecution) error {
	out := n.Out
	if out == nil {
		out = os.Stdout
	}
	limit, err := validateNotificationsLimit(n.Limit)
	if err != nil {
		return err
	}
	tm, err := team.ReadProfile(n.ProjectDir, n.Profile)
	if err != nil {
		return fmt.Errorf("read team profile %q: %w", n.Profile, err)
	}
	policy := team.EffectiveOperatorNotifications(tm.Operator)
	statePath := strings.TrimSpace(n.StatePath)
	if statePath == "" {
		statePath = defaultNotifyStatePath(n.ProjectDir)
	}
	st, err := readNotifyState(statePath)
	if err != nil {
		return err
	}
	data := notificationsHistoryData{ProjectDir: n.ProjectDir, Profile: n.Profile, Session: n.Session, State: buildNotificationsStateView(statePath, st, n.Profile, n.Session, limit, notificationsNow(n.Now), policy)}
	if n.JSON {
		return writeJSONEnvelope(out, "notifications_history", data)
	}
	return renderNotificationsHistory(out, data)
}

func executeNotificationsProbe(n notificationsProbeExecution) error {
	out := n.Out
	if out == nil {
		out = os.Stdout
	}
	tm, err := team.ReadProfile(n.ProjectDir, n.Profile)
	if err != nil {
		return fmt.Errorf("read team profile %q: %w", n.Profile, err)
	}
	policy := team.EffectiveOperatorNotifications(tm.Operator)
	if !policy.Enabled {
		return usageErrorf("notifications probe requires operator.notifications.enabled=true")
	}
	cfg, err := selectNotificationProbeSink(policy.Sinks, n.SinkID)
	if err != nil {
		return err
	}
	nowFn := n.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	started := nowFn()
	probeID := randomToken()
	event := attention.Event{
		SchemaVersion:  1,
		EventType:      "probe",
		Key:            n.Profile + "/" + n.Session + "\x00probe\x00" + probeID,
		Fingerprint:    probeID,
		ProjectDir:     n.ProjectDir,
		Profile:        n.Profile,
		Session:        n.Session,
		Subject:        "notification delivery probe",
		Summary:        "notification delivery probe " + probeID,
		InspectCommand: "amq-squad notifications doctor --project " + notifyShellQuote(n.ProjectDir) + " --profile " + notifyShellQuote(n.Profile) + " --session " + notifyShellQuote(n.Session),
		AttentionOnly:  true,
		ObservedAt:     started,
	}
	ctx := n.Context
	if ctx == nil {
		ctx = context.Background()
	}
	sink := notificationSinkFactory(cfg)
	receipt := ""
	if withReceipt, ok := sink.(notificationReceiptSink); ok {
		receipt, err = withReceipt.DeliverWithReceipt(ctx, event)
	} else {
		err = sink.Deliver(ctx, event)
	}
	latency := nowFn().Sub(started)
	if latency < 0 {
		latency = 0
	}
	data := notificationsProbeData{ProjectDir: n.ProjectDir, Profile: n.Profile, Session: n.Session, ProbeID: probeID, Sink: notificationPolicySinkView{ID: cfg.ID, Type: cfg.Type, Timeout: cfg.Timeout}, Success: err == nil, Latency: latency.String(), LatencyMS: latency.Milliseconds(), Receipt: strings.TrimSpace(receipt), Error: attention.NormalizeDeliveryError(errorString(err))}
	if n.JSON {
		if writeErr := writeJSONEnvelope(out, "notifications_probe", data); writeErr != nil {
			return writeErr
		}
	} else {
		renderNotificationsProbe(out, data)
	}
	if err != nil {
		return fmt.Errorf("%s", attention.NormalizeDeliveryError(fmt.Sprintf("notifications probe sink %q failed: %s", cfg.ID, err)))
	}
	return nil
}

func selectNotificationProbeSink(sinks []team.OperatorNotificationSinkConfig, requested string) (team.OperatorNotificationSinkConfig, error) {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		if len(sinks) == 1 {
			return sinks[0], nil
		}
		if len(sinks) == 0 {
			return team.OperatorNotificationSinkConfig{}, usageErrorf("notifications probe has no configured sinks")
		}
		return team.OperatorNotificationSinkConfig{}, usageErrorf("notifications probe requires --sink when %d sinks are configured", len(sinks))
	}
	for _, sink := range sinks {
		if sink.ID == requested {
			return sink, nil
		}
	}
	return team.OperatorNotificationSinkConfig{}, usageErrorf("notifications probe: sink %q is not configured", requested)
}

func buildNotificationPolicyView(policy team.OperatorNotificationPolicy) notificationPolicyView {
	view := notificationPolicyView{Enabled: policy.Enabled, DeliverySemantics: policy.DeliverySemantics, Events: append([]string(nil), policy.Events...), Sinks: make([]notificationPolicySinkView, 0, len(policy.Sinks))}
	for _, sink := range policy.Sinks {
		view.Sinks = append(view.Sinks, notificationPolicySinkView{ID: sink.ID, Type: sink.Type, Timeout: sink.Timeout})
	}
	return view
}

func buildNotificationWatcherView(expected bool, watcher notificationWatcherStatus, now time.Time) notificationWatcherView {
	rec := watcher.record
	runtimeExpected := rec.SchemaVersion == notificationWatcherSchema && rec.Expected
	running := runtimeExpected && watcher.PID > 0 && now.Before(watcher.LeaseExpiresAt) && (watcher.Health == "healthy" || watcher.Health == "external-active" || watcher.Health == "degraded")
	return notificationWatcherView{
		PolicyEnabled: expected, Expected: runtimeExpected, Running: running, Health: watcher.Health, Reason: boundedNotificationText(watcher.Reason),
		RuntimePath: watcher.RuntimePath, SchemaVersion: watcher.SchemaVersion, PID: watcher.PID,
		Host: watcher.Host, Owner: watcher.Owner, LeaseTTL: rec.LeaseTTL,
		LeaseExpiresAt: watcher.LeaseExpiresAt, HeartbeatAt: watcher.HeartbeatAt,
		LastScanAt: watcher.LastScanAt, LastEventAt: rec.LastEventAt, StatePath: watcher.StatePath,
		LastError: boundedNotificationText(rec.LastError),
	}
}

func buildNotificationsStateView(path string, st notifyStateFile, profile, session string, limit int, now time.Time, policy team.OperatorNotificationPolicy) notificationsStateView {
	view := notificationsStateView{Path: path, Schema: st.Schema, Events: []notificationEventView{}}
	allowed := map[string]bool{}
	for _, eventType := range policy.Events {
		allowed[eventType] = true
	}
	for key, rec := range st.Items {
		event := buildNotificationEventView(key, rec, now)
		if event.Profile != profile || strings.TrimSpace(session) != "" && event.Session != session {
			continue
		}
		view.TotalEventCount++
		if event.Active && policy.Enabled && allowed[event.EventType] && notificationEventHasPendingDelivery(rec, policy.Sinks, now) {
			view.PendingEventCount++
		}
		view.Events = append(view.Events, event)
	}
	sort.SliceStable(view.Events, func(i, j int) bool {
		if !view.Events[i].RecentAt.Equal(view.Events[j].RecentAt) {
			return view.Events[i].RecentAt.After(view.Events[j].RecentAt)
		}
		return view.Events[i].Key < view.Events[j].Key
	})
	if len(view.Events) > limit {
		view.Events = view.Events[:limit]
		view.Truncated = true
	}
	view.ShownEventCount = len(view.Events)
	return view
}

func notificationEventHasPendingDelivery(rec notifyStateRecord, sinks []team.OperatorNotificationSinkConfig, now time.Time) bool {
	if len(sinks) == 0 {
		return false
	}
	fingerprint := rec.Fingerprint
	if fingerprint == "" {
		fingerprint = rec.LatestID
	}
	if fingerprint == "" {
		return true
	}
	for _, sink := range sinks {
		delivery, ok := rec.Deliveries[sink.ID]
		if !ok {
			return true
		}
		if delivery.ReservationToken != "" && now.Before(delivery.ReservationExpires) {
			return true
		}
		if delivery.Fingerprint != fingerprint || delivery.LastNotified.IsZero() {
			return true
		}
	}
	return false
}

func buildNotificationEventView(key string, rec notifyStateRecord, now time.Time) notificationEventView {
	profile, session, eventType, target := parseNotificationStateKey(key)
	view := notificationEventView{Key: key, Profile: profile, Session: session, EventType: eventType, Target: target, LatestID: rec.LatestID, Fingerprint: rec.Fingerprint, Active: rec.Active, LastObserved: rec.LastObserved, RecentAt: rec.LastObserved, Deliveries: []notificationDeliveryView{}}
	for sinkID, delivery := range rec.Deliveries {
		reservation := notificationReservationView{Present: delivery.ReservationToken != "", ExpiresAt: delivery.ReservationExpires, Active: delivery.ReservationToken != "" && now.Before(delivery.ReservationExpires)}
		item := notificationDeliveryView{SinkID: sinkID, Fingerprint: delivery.Fingerprint, Reservation: reservation, LastAttempt: delivery.LastAttempt, LastSuccess: delivery.LastSuccess, LastFailure: delivery.LastFailure, FailureCount: delivery.FailureCount, LastError: boundedNotificationText(delivery.LastError), LastNotified: delivery.LastNotified, LastEscalation: delivery.LastEscalation}
		view.Deliveries = append(view.Deliveries, item)
		for _, candidate := range []time.Time{delivery.LastAttempt, delivery.LastSuccess, delivery.LastFailure, delivery.LastNotified} {
			if candidate.After(view.RecentAt) {
				view.RecentAt = candidate
			}
		}
	}
	sort.SliceStable(view.Deliveries, func(i, j int) bool { return view.Deliveries[i].SinkID < view.Deliveries[j].SinkID })
	return view
}

func parseNotificationStateKey(key string) (profile, session, eventType, target string) {
	parts := strings.SplitN(key, "\x00", 3)
	namespace := parts[0]
	if slash := strings.IndexByte(namespace, '/'); slash >= 0 {
		profile, session = namespace[:slash], namespace[slash+1:]
	} else {
		profile = namespace
	}
	if len(parts) > 1 {
		eventType = parts[1]
	}
	if len(parts) > 2 {
		target = parts[2]
	}
	return profile, session, eventType, target
}

func validateNotificationsLimit(limit int) (int, error) {
	if limit == 0 {
		limit = defaultNotificationsHistoryLimit
	}
	if limit < 1 || limit > maxNotificationsHistoryLimit {
		return 0, usageErrorf("--limit must be between 1 and %d", maxNotificationsHistoryLimit)
	}
	return limit, nil
}

func notificationsNow(now func() time.Time) time.Time {
	if now == nil {
		return time.Now()
	}
	return now()
}

func boundedNotificationText(text string) string {
	return attention.NormalizeDeliveryError(text)
}

func renderNotificationsDoctor(out io.Writer, data notificationsDoctorData) error {
	fmt.Fprintf(out, "Notifications doctor: healthy=%t degraded=%t\n", data.Healthy, data.Degraded)
	fmt.Fprintf(out, "Notifications: enabled=%t semantics=%s events=%s\n", data.Policy.Enabled, data.Policy.DeliverySemantics, strings.Join(data.Policy.Events, ","))
	fmt.Fprintf(out, "Sinks (%d):\n", len(data.Policy.Sinks))
	for _, sink := range data.Policy.Sinks {
		fmt.Fprintf(out, "- %s type=%s timeout=%s\n", sink.ID, sink.Type, sink.Timeout)
	}
	fmt.Fprintf(out, "Watcher: expected=%t running=%t health=%s", data.Watcher.Expected, data.Watcher.Running, data.Watcher.Health)
	if data.Watcher.Reason != "" {
		fmt.Fprintf(out, " reason=%s", data.Watcher.Reason)
	}
	fmt.Fprintln(out)
	fmt.Fprintf(out, "  pid=%d host=%s lease=%s heartbeat=%s last_scan=%s runtime=%s\n", data.Watcher.PID, data.Watcher.Host, formatNotificationTime(data.Watcher.LeaseExpiresAt), formatNotificationTime(data.Watcher.HeartbeatAt), formatNotificationTime(data.Watcher.LastScanAt), data.Watcher.RuntimePath)
	fmt.Fprintf(out, "State: path=%s schema=%d pending=%d events=%d shown=%d truncated=%t\n", data.State.Path, data.State.Schema, data.State.PendingEventCount, data.State.TotalEventCount, data.State.ShownEventCount, data.State.Truncated)
	renderNotificationEvents(out, data.State.Events)
	return nil
}

func renderNotificationsHistory(out io.Writer, data notificationsHistoryData) error {
	fmt.Fprintf(out, "Notification history: profile=%s", data.Profile)
	if data.Session != "" {
		fmt.Fprintf(out, " session=%s", data.Session)
	}
	fmt.Fprintf(out, " state=%s schema=%d pending=%d events=%d shown=%d truncated=%t\n", data.State.Path, data.State.Schema, data.State.PendingEventCount, data.State.TotalEventCount, data.State.ShownEventCount, data.State.Truncated)
	renderNotificationEvents(out, data.State.Events)
	return nil
}

func renderNotificationEvents(out io.Writer, events []notificationEventView) {
	for _, event := range events {
		fmt.Fprintf(out, "- %s/%s %s %s active=%t fingerprint=%s observed=%s\n", event.Profile, event.Session, event.EventType, event.Target, event.Active, event.Fingerprint, formatNotificationTime(event.LastObserved))
		for _, delivery := range event.Deliveries {
			reservation := "none"
			if delivery.Reservation.Present {
				reservation = fmt.Sprintf("active=%t expires=%s", delivery.Reservation.Active, formatNotificationTime(delivery.Reservation.ExpiresAt))
			}
			fmt.Fprintf(out, "  %s fingerprint=%s reservation=%s attempt=%s success=%s failure=%s failures=%d", delivery.SinkID, delivery.Fingerprint, reservation, formatNotificationTime(delivery.LastAttempt), formatNotificationTime(delivery.LastSuccess), formatNotificationTime(delivery.LastFailure), delivery.FailureCount)
			if delivery.LastError != "" {
				fmt.Fprintf(out, " error=%q", delivery.LastError)
			}
			fmt.Fprintln(out)
		}
	}
}

func renderNotificationsProbe(out io.Writer, data notificationsProbeData) {
	status := "success"
	if !data.Success {
		status = "failed"
	}
	fmt.Fprintf(out, "Notification probe %s: sink=%s type=%s latency=%s probe_id=%s", status, data.Sink.ID, data.Sink.Type, data.Latency, data.ProbeID)
	if data.Receipt != "" {
		fmt.Fprintf(out, " receipt=%s", data.Receipt)
	}
	if data.Error != "" {
		fmt.Fprintf(out, " error=%q", data.Error)
	}
	fmt.Fprintln(out)
}

func formatNotificationTime(value time.Time) string {
	if value.IsZero() {
		return "-"
	}
	return value.UTC().Format(time.RFC3339)
}
