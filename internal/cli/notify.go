package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/attention"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/notifier"
	"github.com/omriariav/amq-squad/v2/internal/state"
	"github.com/omriariav/amq-squad/v2/internal/team"
	"github.com/omriariav/amq-squad/v2/internal/tmuxpane"
)

var notifyLocalInputDetector = tmuxpane.DetectLocalInputBlocker
var notificationSinkFactory = notificationSink

const defaultOperatorRenotifyAfter = 30 * time.Minute

type notifyExecution struct {
	ProjectDir      string
	Profile         string
	Session         string
	BaseRoot        string
	StatePath       string
	RenotifyAfter   time.Duration
	DryRun          bool
	JSON            bool
	Out             io.Writer
	ResolveBaseRoot func(projectDir string) (string, error)
	Probe           state.Probe
	Now             func() time.Time
	Deliver         bool
	ForceResend     bool
}

type notifyEnvelopeData struct {
	ProjectDir    string              `json:"project_dir"`
	BaseRoot      string              `json:"base_root,omitempty"`
	Profile       string              `json:"profile"`
	Operator      team.OperatorView   `json:"operator"`
	RenotifyAfter string              `json:"renotify_after"`
	Notifications []operatorAttention `json:"notifications"`
	Suppressed    int                 `json:"suppressed"`
	StatePath     string              `json:"state_path,omitempty"`
	OperatorGates bool                `json:"operator_gates"`
	Message       string              `json:"message,omitempty"`
	SinkResults   []notifier.Result   `json:"sink_results,omitempty"`
}

type operatorAttention struct {
	EventType   string           `json:"event_type,omitempty"`
	Key         string           `json:"key"`
	Profile     string           `json:"profile"`
	Session     string           `json:"session"`
	NamespaceID string           `json:"namespace_id"`
	Thread      string           `json:"thread"`
	LatestID    string           `json:"latest_id"`
	From        string           `json:"from,omitempty"`
	Subject     string           `json:"subject"`
	Kind        state.Kind       `json:"kind"`
	Reason      state.AttnReason `json:"reason"`
	Age         string           `json:"age"`
	LastEventAt time.Time        `json:"last_event_at,omitempty"`
	Escalation  string           `json:"escalation,omitempty"`
	Inspect     string           `json:"inspect"`
	Respond     string           `json:"respond"`
	Role        string           `json:"role,omitempty"`
	Summary     string           `json:"summary,omitempty"`
	Cleared     bool             `json:"-"`
}

type notifyStateFile struct {
	Schema int                          `json:"schema"`
	Items  map[string]notifyStateRecord `json:"items"`
}

type notifyStateRecord struct {
	LatestID       string                        `json:"latest_id"`
	LastNotified   time.Time                     `json:"last_notified"`
	LastEscalation string                        `json:"last_escalation,omitempty"`
	Fingerprint    string                        `json:"fingerprint,omitempty"`
	Active         bool                          `json:"active"`
	LastObserved   time.Time                     `json:"last_observed,omitempty"`
	Deliveries     map[string]attention.Delivery `json:"deliveries,omitempty"`
}

func runNotify(args []string) error {
	fs := flag.NewFlagSet("notify", flag.ContinueOnError)
	projectFlag := fs.String("project", "", "project/team-home directory to inspect (default: cwd)")
	profileFlag := fs.String("profile", "", "team profile to inspect (default: default profile)")
	sessionFlag := fs.String("session", "", "scope notifications to one AMQ workstream")
	renotifyAfter := fs.Duration("renotify-after", defaultOperatorRenotifyAfter, "re-notify unchanged operator gates after this duration (0 disables repeats)")
	dryRun := fs.Bool("dry-run", false, "print notifications without updating the de-duplication state file")
	deliver := fs.Bool("deliver", false, "deliver selected events to configured sinks (default: off)")
	forceResend := fs.Bool("force-resend", false, "re-deliver active selected events; requires --deliver")
	jsonOut := fs.Bool("json", false, "emit a schema-versioned notification envelope instead of text")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad notify - emit operator attention notifications

Usage:
  amq-squad notify [--project DIR] [--profile NAME] [--session NAME]
                   [--renotify-after 30m] [--dry-run] [--json]
                   [--deliver [--force-resend]]

Scans AMQ state for live needs-you threads addressed to the configured operator
handle, prints only new or stale-threshold notifications, and records what was
shown under .amq-squad/notify-state.json. It is an event/hook-friendly attention
primitive: it does not approve, answer, clear, or poll in a loop.

Examples:
  amq-squad notify
  amq-squad notify --project ~/Code/app --profile review
  amq-squad notify --session issue-96 --renotify-after 1h
  amq-squad notify --json | jq '.data.notifications[]'
`)
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return usageErrorf("notify takes no positional arguments; got %d", fs.NArg())
	}
	if *dryRun && *deliver {
		return usageErrorf("--dry-run and --deliver are mutually exclusive")
	}
	if *forceResend && !*deliver {
		return usageErrorf("--force-resend requires --deliver")
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	projectDir, err := resolveProjectDirFlag(cwd, *projectFlag, flagWasSet(fs, "project"))
	if err != nil {
		return err
	}
	profile, err := resolveProfileFlag(*profileFlag)
	if err != nil {
		return err
	}
	return executeNotify(notifyExecution{
		ProjectDir:    projectDir,
		Profile:       profile,
		Session:       *sessionFlag,
		RenotifyAfter: *renotifyAfter,
		DryRun:        *dryRun,
		JSON:          *jsonOut,
		Deliver:       *deliver,
		ForceResend:   *forceResend,
		Out:           os.Stdout,
	})
}

func executeNotify(n notifyExecution) error {
	out := n.Out
	if out == nil {
		out = os.Stdout
	}
	now := n.Now
	if now == nil {
		now = time.Now
	}
	profile := strings.TrimSpace(n.Profile)
	if profile == "" {
		profile = team.DefaultProfile
	}
	t, err := team.ReadProfile(n.ProjectDir, profile)
	if err != nil {
		return fmt.Errorf("read team profile %q: %w", profile, err)
	}
	operator := team.EffectiveOperator(t)
	if !team.SupportsOperatorGates(t) {
		data := notifyEnvelopeData{
			ProjectDir:    n.ProjectDir,
			Profile:       profile,
			Operator:      operator,
			OperatorGates: false,
			Message:       "operator gates disabled for this profile",
		}
		if n.JSON {
			return writeJSONEnvelope(out, "notify", data)
		}
		fmt.Fprintln(out, "amq-squad notify: operator gates disabled for this profile.")
		return nil
	}
	if n.RenotifyAfter < 0 {
		return usageErrorf("--renotify-after must be >= 0")
	}
	resolve := n.ResolveBaseRoot
	if resolve == nil {
		resolve = scanBaseRootForProject
	}
	baseRoot := strings.TrimSpace(n.BaseRoot)
	if baseRoot == "" {
		baseRoot, err = resolve(n.ProjectDir)
		if err != nil {
			return fmt.Errorf("resolve AMQ base root: %w", err)
		}
	}
	if baseRoot == "" {
		return fmt.Errorf("resolve AMQ base root: empty root")
	}
	snap, err := state.BuildWithThresholds(n.ProjectDir, baseRoot, n.Probe, state.Thresholds{OperatorHandle: operator.Handle})
	if err != nil {
		return fmt.Errorf("scan AMQ base root: %w", err)
	}
	items := collectOperatorAttention(n.ProjectDir, profile, snap, operator.Handle, strings.TrimSpace(n.Session), now())
	items = mergeOperatorAttention(items, collectRawOpenGateAttention(n.ProjectDir, profile, snap, operator.Handle, strings.TrimSpace(n.Session), now()))
	workstream, _ := resolveTeamWorkstreamName(t, strings.TrimSpace(n.Session), strings.TrimSpace(n.Session) != "")
	items = mergeOperatorAttention(items, collectLocalInputAttention(t, profile, workstream, now()))
	statePath := strings.TrimSpace(n.StatePath)
	if statePath == "" {
		statePath = defaultNotifyStatePath(n.ProjectDir)
	}
	prior, err := readNotifyState(statePath)
	if err != nil {
		return err
	}
	notifications, suppressed, next := selectNotifications(items, prior, n.RenotifyAfter, now())
	var sinkResults []notifier.Result
	if n.Deliver {
		policy := team.EffectiveOperatorNotifications(t.Operator)
		if !policy.Enabled {
			return usageErrorf("--deliver requires operator.notifications.enabled=true")
		}
		sinkResults, next = deliverNotificationSinks(context.Background(), items, policy, next, n.RenotifyAfter, now(), n.ForceResend)
	}
	if !n.DryRun {
		if err := writeNotifyState(statePath, next); err != nil {
			return err
		}
	}
	data := notifyEnvelopeData{
		ProjectDir:    n.ProjectDir,
		BaseRoot:      snap.BaseRoot,
		Profile:       profile,
		Operator:      operator,
		RenotifyAfter: n.RenotifyAfter.String(),
		Notifications: notifications,
		Suppressed:    suppressed,
		StatePath:     statePath,
		OperatorGates: true,
		SinkResults:   sinkResults,
	}
	if n.JSON {
		return writeJSONEnvelope(out, "notify", data)
	}
	return renderNotify(out, data)
}

func collectOperatorAttention(projectDir, profile string, snap state.Snapshot, operatorHandle, onlySession string, now time.Time) []operatorAttention {
	var out []operatorAttention
	profile = squadnamespace.NormalizeProfile(profile)
	for _, sess := range snap.Sessions {
		if !squadnamespace.ProfilesEqual(profile, sess.TeamProfile) {
			continue
		}
		if onlySession != "" && sess.Name != onlySession {
			continue
		}
		for _, th := range sess.Coordination.NeedsYouThreads() {
			if th.Historical {
				continue
			}
			if !notifyStructuralOperatorAttention(th, operatorHandle) {
				continue
			}
			if !notifyThreadMatchesSessionAgents(th, operatorHandle, sess.Agents) {
				continue
			}
			age := now.Sub(th.LastEventAt)
			if age < 0 {
				age = 0
			}
			from := firstNonOperatorParticipant(th, operatorHandle)
			item := operatorAttention{
				EventType:   "gate",
				Key:         notifyKey(profile, sess.Name, th.ID),
				Profile:     profile,
				Session:     sess.Name,
				NamespaceID: squadnamespace.ID(profile, sess.Name),
				Thread:      th.ID,
				LatestID:    th.LatestID,
				From:        from,
				Subject:     th.Subject,
				Kind:        th.Kind,
				Reason:      th.AttnReason,
				Age:         roundDuration(age).String(),
				LastEventAt: th.LastEventAt,
				Inspect:     notifyInspectCommand(projectDir, profile, sess.Name, th.ID),
				Respond:     notifyRespondCommand(operatorHandle, from, th.ID, th.AttnReason),
			}
			applyThreadOperatorGateAttention(&item, th)
			item.Respond = notifyRespondCommand(operatorHandle, item.From, th.ID, item.Reason)
			out = append(out, item)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := out[i].Reason.Rank(), out[j].Reason.Rank()
		if ri != rj {
			return ri < rj
		}
		if !out[i].LastEventAt.Equal(out[j].LastEventAt) {
			return out[i].LastEventAt.Before(out[j].LastEventAt)
		}
		if out[i].Session != out[j].Session {
			return out[i].Session < out[j].Session
		}
		return out[i].Thread < out[j].Thread
	})
	return out
}

type rawGateState struct {
	pending state.Message
}

func collectRawOpenGateAttention(projectDir, profile string, snap state.Snapshot, operatorHandle, onlySession string, now time.Time) []operatorAttention {
	var out []operatorAttention
	profile = squadnamespace.NormalizeProfile(profile)
	for _, sess := range snap.Sessions {
		if !squadnamespace.ProfilesEqual(profile, sess.TeamProfile) {
			continue
		}
		if onlySession != "" && sess.Name != onlySession {
			continue
		}
		msgs, _ := state.ScanSessionMessages(sess.Root, func() time.Time { return now })
		byThread := map[string]rawGateState{}
		seen := map[string]bool{}
		for _, msg := range msgs {
			if msg.ID != "" {
				if seen[msg.ID] {
					continue
				}
				seen[msg.ID] = true
			}
			if !strings.HasPrefix(msg.Thread, "gate/") {
				continue
			}
			gate := byThread[msg.Thread]
			switch msg.Kind {
			case state.KindQuestion, state.KindReviewRequest, state.KindDecision:
				if rawOperatorGateRequestMessage(msg, operatorHandle, sess.Agents) {
					gate.pending = msg
				}
			case state.KindAnswer:
				if msg.From == operatorHandle {
					gate.pending = state.Message{}
				}
			}
			byThread[msg.Thread] = gate
		}
		for thread, gate := range byThread {
			msg := gate.pending
			if msg.ID == "" {
				continue
			}
			age := now.Sub(msg.Created)
			if age < 0 {
				age = 0
			}
			out = append(out, operatorAttention{
				EventType:   "gate",
				Key:         notifyKey(profile, sess.Name, thread),
				Profile:     profile,
				Session:     sess.Name,
				NamespaceID: sess.NamespaceID,
				Thread:      thread,
				LatestID:    msg.ID,
				From:        msg.From,
				Subject:     msg.Subject,
				Kind:        msg.Kind,
				Reason:      state.ClassifyAttnSubject(msg.Subject),
				Age:         roundDuration(age).String(),
				LastEventAt: msg.Created,
				Escalation:  string(state.OperatorGateEscalationForAge(age)),
				Inspect:     notifyInspectCommand(projectDir, profile, sess.Name, thread),
				Respond:     notifyRespondCommand(operatorHandle, msg.From, thread, state.ClassifyAttnSubject(msg.Subject)),
			})
		}
	}
	sortOperatorAttention(out)
	return out
}

func collectLocalInputAttention(t team.Team, profile, session string, now time.Time) []operatorAttention {
	if strings.TrimSpace(session) == "" {
		return nil
	}
	rows := buildStatusRows(t, profile, session, defaultDuplicateLaunchProbe)
	var out []operatorAttention
	for _, row := range rows {
		if !statusLocalInputCandidate(t, row) {
			continue
		}
		blocker, ok, err := notifyLocalInputDetector(row.Tmux.PaneID)
		if err != nil {
			continue
		}
		key := attention.LocalInputKey(profile, session, row.Role)
		if !ok {
			out = append(out, operatorAttention{EventType: "local_input_blocked", Key: key, Profile: profile, Session: session, Role: row.Role, Cleared: true})
			continue
		}
		fp := attention.LocalInputFingerprint(row.Tmux.PaneID, blocker.Kind, blocker.Destructive, blocker.Summary)
		summary := blocker.Summary
		if blocker.Destructive {
			summary = "destructive local prompt: " + summary
		}
		out = append(out, operatorAttention{EventType: "local_input_blocked", Key: key, Profile: profile, Session: session, NamespaceID: squadnamespace.ID(profile, session), LatestID: fp, Role: row.Role, Subject: "local input blocked", Summary: summary, Age: "0s", LastEventAt: now, Inspect: "amq-squad focus --project " + notifyShellQuote(t.Project) + " --profile " + notifyShellQuote(profile) + " --session " + notifyShellQuote(session) + " --role " + notifyShellQuote(row.Role)})
	}
	return out
}

func applyThreadOperatorGateAttention(item *operatorAttention, th state.ThreadSummary) {
	if item == nil || th.OperatorGate == nil {
		return
	}
	gate := th.OperatorGate
	item.LatestID = gate.LatestID
	item.From = gate.From
	item.Subject = gate.Subject
	item.Kind = gate.Kind
	item.Reason = gate.Reason
	item.Age = roundDuration(gate.Age).String()
	item.LastEventAt = gate.Since
	item.Escalation = string(gate.Escalation)
}

func rawOperatorGateRequestMessage(msg state.Message, operatorHandle string, agents []state.Agent) bool {
	return operatorMessageToContains(msg, operatorHandle) && sessionHasAgentHandle(agents, msg.From)
}

func mergeOperatorAttention(base, extra []operatorAttention) []operatorAttention {
	if len(extra) == 0 {
		return base
	}
	merged := make([]operatorAttention, 0, len(base)+len(extra))
	index := map[string]int{}
	for _, item := range base {
		index[item.Key] = len(merged)
		merged = append(merged, item)
	}
	for _, item := range extra {
		if i, ok := index[item.Key]; ok {
			merged[i] = item
			continue
		}
		index[item.Key] = len(merged)
		merged = append(merged, item)
	}
	sortOperatorAttention(merged)
	return merged
}

func sortOperatorAttention(items []operatorAttention) {
	sort.SliceStable(items, func(i, j int) bool {
		ri, rj := items[i].Reason.Rank(), items[j].Reason.Rank()
		if ri != rj {
			return ri < rj
		}
		if !items[i].LastEventAt.Equal(items[j].LastEventAt) {
			return items[i].LastEventAt.Before(items[j].LastEventAt)
		}
		if items[i].Session != items[j].Session {
			return items[i].Session < items[j].Session
		}
		return items[i].Thread < items[j].Thread
	})
}

func operatorMessageToContains(msg state.Message, handle string) bool {
	for _, to := range msg.To {
		if to == handle {
			return true
		}
	}
	return false
}

func sessionHasAgentHandle(agents []state.Agent, handle string) bool {
	handle = strings.TrimSpace(handle)
	if handle == "" {
		return false
	}
	for _, agent := range agents {
		if agent.Handle == handle {
			return true
		}
	}
	return false
}

func notifyStructuralOperatorAttention(th state.ThreadSummary, operatorHandle string) bool {
	if !notifyUnreadBy(th, operatorHandle) {
		return false
	}
	if strings.HasPrefix(th.ID, "gate/") {
		return true
	}
	switch th.Kind {
	case state.KindQuestion, state.KindDecision:
		return true
	default:
		return false
	}
}

func notifyThreadMatchesSessionAgents(th state.ThreadSummary, operatorHandle string, agents []state.Agent) bool {
	handles := map[string]bool{}
	for _, a := range agents {
		if h := strings.TrimSpace(a.Handle); h != "" {
			handles[h] = true
		}
	}
	for _, p := range th.Participants {
		p = strings.TrimSpace(p)
		if p == "" || p == operatorHandle {
			continue
		}
		if handles[p] {
			return true
		}
	}
	return false
}

func notifyUnreadBy(th state.ThreadSummary, handle string) bool {
	for _, unread := range th.UnreadBy {
		if unread == handle {
			return true
		}
	}
	return false
}

func selectNotifications(items []operatorAttention, prior notifyStateFile, renotifyAfter time.Duration, now time.Time) ([]operatorAttention, int, notifyStateFile) {
	next := notifyStateFile{Schema: 2, Items: map[string]notifyStateRecord{}}
	for key, rec := range prior.Items {
		next.Items[key] = rec
	}
	var selected []operatorAttention
	suppressed := 0
	for _, item := range items {
		rec := prior.Items[item.Key]
		if item.Cleared {
			rec.Active = false
			rec.LastObserved = now
			next.Items[item.Key] = rec
			continue
		}
		notify := rec.LatestID != item.LatestID || rec.LastNotified.IsZero()
		if !notify && operatorAttentionEscalated(item, rec) {
			notify = true
		}
		if !notify && renotifyAfter > 0 && now.Sub(rec.LastNotified) >= renotifyAfter {
			notify = true
		}
		if notify {
			selected = append(selected, item)
			rec = notifyStateRecord{LatestID: item.LatestID, LastNotified: now, LastEscalation: item.Escalation}
		} else {
			suppressed++
		}
		next.Items[item.Key] = rec
		rec.Fingerprint = item.LatestID
		rec.Active = true
		rec.LastObserved = now
		next.Items[item.Key] = rec
	}
	return selected, suppressed, next
}

func operatorAttentionEscalated(item operatorAttention, rec notifyStateRecord) bool {
	current := state.OperatorGateEscalation(item.Escalation)
	if current == "" {
		return false
	}
	previous := state.OperatorGateEscalation(rec.LastEscalation)
	currentRank := state.OperatorGateEscalationRank(current)
	return currentRank >= state.OperatorGateEscalationRank(state.OperatorGateEscalationReminder) &&
		currentRank > state.OperatorGateEscalationRank(previous)
}

func renderNotify(out io.Writer, data notifyEnvelopeData) error {
	if len(data.Notifications) == 0 {
		if data.Suppressed > 0 {
			fmt.Fprintf(out, "amq-squad notify: no new operator attention items (%d suppressed by throttle).\n", data.Suppressed)
		} else {
			fmt.Fprintln(out, "amq-squad notify: no operator attention items.")
		}
		return nil
	}
	fmt.Fprintf(out, "amq-squad notify: %d operator attention %s for %s\n", len(data.Notifications), pluralize(len(data.Notifications), "item", "items"), data.Operator.Handle)
	for _, n := range data.Notifications {
		reason := string(n.Reason)
		if reason == "" {
			reason = "generic"
		}
		escalation := ""
		if n.Escalation != "" {
			escalation = ", " + n.Escalation
		}
		fmt.Fprintf(out, "- %s %s %s (%s%s, age %s)\n", n.Session, n.Thread, n.Subject, reason, escalation, n.Age)
		fmt.Fprintf(out, "  inspect: %s\n", n.Inspect)
		fmt.Fprintf(out, "  respond: %s\n", n.Respond)
	}
	if data.Suppressed > 0 {
		fmt.Fprintf(out, "%d unchanged %s suppressed by throttle.\n", data.Suppressed, pluralize(data.Suppressed, "item", "items"))
	}
	return nil
}

func defaultNotifyStatePath(projectDir string) string {
	return filepath.Join(projectDir, ".amq-squad", "notify-state.json")
}

func readNotifyState(path string) (notifyStateFile, error) {
	st := notifyStateFile{Schema: 2, Items: map[string]notifyStateRecord{}}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return st, nil
		}
		return st, fmt.Errorf("read notify state: %w", err)
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		return st, nil
	}
	if err := json.Unmarshal(b, &st); err != nil {
		return st, fmt.Errorf("parse notify state %s: %w", path, err)
	}
	if st.Items == nil {
		st.Items = map[string]notifyStateRecord{}
	}
	if st.Schema == 1 {
		migrated := map[string]notifyStateRecord{}
		for key, rec := range st.Items {
			newKey := key
			if !strings.Contains(key, "\x00gate\x00") {
				if i := strings.IndexByte(key, 0); i >= 0 {
					newKey = key[:i] + "\x00gate\x00" + key[i+1:]
				}
			}
			rec.Fingerprint = rec.LatestID
			rec.Active = true
			if rec.Deliveries == nil {
				rec.Deliveries = map[string]attention.Delivery{}
			}
			rec.Deliveries["surface:notify"] = attention.Delivery{Fingerprint: rec.LatestID, LastNotified: rec.LastNotified, LastEscalation: rec.LastEscalation}
			migrated[newKey] = rec
		}
		st.Items = migrated
		st.Schema = 2
	}
	return st, nil
}

func writeNotifyState(path string, st notifyStateFile) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("ensure notify state dir: %w", err)
	}
	st.Schema = 2 // notify-state schema 2 is independent of team.json schema 3.
	if st.Items == nil {
		st.Items = map[string]notifyStateRecord{}
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal notify state: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return fmt.Errorf("write notify state: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename notify state: %w", err)
	}
	return nil
}

func notifyKey(profile, session, thread string) string {
	return attention.GateKey(squadnamespace.NormalizeProfile(profile), session, thread)
}

func deliverNotificationSinks(ctx context.Context, items []operatorAttention, policy team.OperatorNotificationPolicy, st notifyStateFile, renotify time.Duration, now time.Time, force bool) ([]notifier.Result, notifyStateFile) {
	events := make([]attention.Event, 0, len(items))
	for _, item := range items {
		eventType := item.EventType
		if eventType == "" {
			eventType = "gate"
		}
		summary := item.Summary
		if summary == "" {
			summary = "operator answer required"
		}
		events = append(events, attention.Event{SchemaVersion: 1, EventType: eventType, Key: item.Key, Fingerprint: item.LatestID, ProjectDir: "", Profile: item.Profile, Session: item.Session, Thread: item.Thread, Role: item.Role, Subject: item.Subject, Summary: summary, Escalation: item.Escalation, Age: item.Age, InspectCommand: item.Inspect, AttentionOnly: true, ObservedAt: now, Cleared: item.Cleared})
	}
	state2 := attention.State{Schema: 2, Items: map[string]attention.Item{}}
	for key, rec := range st.Items {
		state2.Items[key] = attention.Item{Fingerprint: rec.Fingerprint, Active: rec.Active, LastObserved: rec.LastObserved, Deliveries: rec.Deliveries}
	}
	var results []notifier.Result
	for _, cfg := range policy.Sinks {
		sink := notificationSinkFactory(cfg)
		selected, next, _ := attention.Select(events, state2, cfg.ID, renotify, now, force)
		state2 = next
		for _, event := range selected {
			err := sink.Deliver(ctx, event)
			results = append(results, notifier.Result{SinkID: cfg.ID, Delivered: err == nil, Error: errorString(err)})
			state2 = attention.Commit(state2, event.Key, cfg.ID, event, now, err)
		}
	}
	for key, item := range state2.Items {
		rec := st.Items[key]
		rec.Fingerprint = item.Fingerprint
		rec.Active = item.Active
		rec.LastObserved = item.LastObserved
		rec.Deliveries = item.Deliveries
		st.Items[key] = rec
	}
	return results, st
}
func notificationSink(cfg team.OperatorNotificationSinkConfig) notifier.Sink {
	if cfg.Type == "desktop" {
		return notifier.DesktopSink{SinkID: cfg.ID}
	}
	d, _ := time.ParseDuration(cfg.Timeout)
	return notifier.CommandSink{SinkID: cfg.ID, Argv: append([]string(nil), cfg.Argv...), Timeout: d}
}
func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func notifyInspectCommand(projectDir, profile, session, thread string) string {
	profile = squadnamespace.NormalizeProfile(profile)
	profileArg := ""
	if profile != team.DefaultProfile {
		profileArg = " --profile " + notifyShellQuote(profile)
	}
	return fmt.Sprintf("amq-squad thread --project %s%s --session %s --id %s --include-body", notifyShellQuote(projectDir), profileArg, notifyShellQuote(session), notifyShellQuote(thread))
}

func notifyRespondCommand(operatorHandle, to, thread string, reason state.AttnReason) string {
	if strings.TrimSpace(to) == "" {
		to = "<agent-handle>"
	}
	subject := "ANSWER: <response>"
	if reason == state.AttnApprove {
		subject = "APPROVED: <decision>"
	}
	return fmt.Sprintf("amq send --me %s --to %s --thread %s --kind answer --subject %s",
		notifyShellQuote(operatorHandle), notifyShellQuote(to), notifyShellQuote(thread), notifyShellQuote(subject))
}

func firstNonOperatorParticipant(th state.ThreadSummary, operatorHandle string) string {
	for _, p := range th.Participants {
		if p != operatorHandle {
			return p
		}
	}
	return ""
}

func roundDuration(d time.Duration) time.Duration {
	if d < time.Minute {
		return d.Round(time.Second)
	}
	return d.Round(time.Minute)
}

func notifyShellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if strings.ContainsAny(s, " \t\n'\"\\$`!*?[]{}()<>|&;") {
		return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
	}
	return s
}
