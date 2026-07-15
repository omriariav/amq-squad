package cli

import (
	"context"
	"crypto/rand"
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
	"github.com/omriariav/amq-squad/v2/internal/flock"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/notifier"
	"github.com/omriariav/amq-squad/v2/internal/operatorauth"
	"github.com/omriariav/amq-squad/v2/internal/state"
	"github.com/omriariav/amq-squad/v2/internal/team"
	"github.com/omriariav/amq-squad/v2/internal/tmuxpane"
)

var notifyLocalInputDetector = tmuxpane.DetectLocalInputBlocker
var notificationSinkFactory = notificationSink

const defaultOperatorRenotifyAfter = 30 * time.Minute

const notifyStateSchema = 2

type notifyExecution struct {
	Context         context.Context
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
	ProjectDir      string                `json:"project_dir"`
	BaseRoot        string                `json:"base_root,omitempty"`
	Profile         string                `json:"profile"`
	Operator        team.OperatorView     `json:"operator"`
	RenotifyAfter   string                `json:"renotify_after"`
	Notifications   []operatorAttention   `json:"notifications"`
	Suppressed      int                   `json:"suppressed"`
	StatePath       string                `json:"state_path,omitempty"`
	OperatorGates   bool                  `json:"operator_gates"`
	Message         string                `json:"message,omitempty"`
	SinkResults     []notifier.Result     `json:"sink_results,omitempty"`
	DeliverySummary notifyDeliverySummary `json:"delivery_summary"`
}
type notifyDeliverySummary struct {
	Selected   int `json:"selected"`
	Delivered  int `json:"delivered"`
	Failed     int `json:"failed"`
	Suppressed int `json:"suppressed"`
}

type operatorAttention struct {
	EventType      string           `json:"event_type,omitempty"`
	Key            string           `json:"key"`
	Profile        string           `json:"profile"`
	Session        string           `json:"session"`
	NamespaceID    string           `json:"namespace_id"`
	Thread         string           `json:"thread"`
	LatestID       string           `json:"latest_id"`
	From           string           `json:"from,omitempty"`
	Subject        string           `json:"subject"`
	Kind           state.Kind       `json:"kind"`
	Reason         state.AttnReason `json:"reason"`
	Age            string           `json:"age"`
	LastEventAt    time.Time        `json:"last_event_at,omitempty"`
	Escalation     string           `json:"escalation,omitempty"`
	Inspect        string           `json:"inspect"`
	Respond        string           `json:"respond"`
	Role           string           `json:"role,omitempty"`
	GateKind       string           `json:"gate_kind,omitempty"`
	Actor          string           `json:"actor,omitempty"`
	PolicyRevision int64            `json:"policy_revision,omitempty"`
	Summary        string           `json:"summary,omitempty"`
	Actionable     bool             `json:"actionable"`
	Answerable     bool             `json:"answerable"`
	Unread         bool             `json:"-"`
	Cleared        bool             `json:"-"`
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
	ctx, err := resolveScopedCommandContext(*projectFlag, *profileFlag, *sessionFlag, "", fs)
	if err != nil {
		return err
	}
	emitContextDiagnostics(ctx)
	var admission *namespaceAdmissionLocks
	if !*dryRun {
		if strings.TrimSpace(*sessionFlag) == "" {
			ctx, admission, err = acquireRevalidatedContextWriter(ctx, true, func() (contextResolution, error) {
				return resolveScopedCommandContext(*projectFlag, *profileFlag, *sessionFlag, "", fs)
			})
			if err != nil {
				return err
			}
			defer admission.close()
			if err := ensureNoNamespaceMigrationForProfile("notify", ctx.ProjectDir, ctx.Profile); err != nil {
				return err
			}
		} else {
			ctx, admission, err = acquireRevalidatedContextWriter(ctx, false, func() (contextResolution, error) {
				return resolveScopedCommandContext(*projectFlag, *profileFlag, *sessionFlag, "", fs)
			})
			if err != nil {
				return err
			}
			defer admission.close()
			if err := ensureNoNamespaceMigration("notify", ctx.ProjectDir, ctx.Profile, ctx.Session); err != nil {
				return err
			}
		}
	}
	return executeNotify(notifyExecution{
		ProjectDir:    ctx.ProjectDir,
		Profile:       ctx.Profile,
		Session:       strings.TrimSpace(*sessionFlag),
		BaseRoot:      ctx.BaseRoot,
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
	projected, err := collectProjectedOperatorAttention(t, n.ProjectDir, profile, snap, operator.Handle, strings.TrimSpace(n.Session), now())
	if err != nil {
		return fmt.Errorf("project compound release attention: %w", err)
	}
	items := projected.Items
	items = mergeOperatorAttention(items, collectSelfOperatorVisibilityAttentionCaptured(t, n.ProjectDir, profile, projected.Snapshot, strings.TrimSpace(n.Session), now(), projected.Captures))
	workstream, _ := resolveTeamWorkstreamName(t, strings.TrimSpace(n.Session), strings.TrimSpace(n.Session) != "")
	items = mergeOperatorAttention(items, collectLocalInputAttention(t, profile, workstream, now()))
	statePath := strings.TrimSpace(n.StatePath)
	if statePath == "" {
		statePath = defaultNotifyStatePath(n.ProjectDir)
	}
	var notifications []operatorAttention
	var suppressed int
	var next notifyStateFile
	if n.DryRun {
		prior, e := readNotifyState(statePath)
		if e != nil {
			return e
		}
		items = scopedSelfOperatorTombstones(items, prior, profile, strings.TrimSpace(n.Session))
		notifications, suppressed, next = selectNotifications(items, prior, n.RenotifyAfter, now())
	} else {
		err = flock.WithLock(statePath+".lock", func() error {
			prior, e := readNotifyState(statePath)
			if e != nil {
				return e
			}
			items = scopedSelfOperatorTombstones(items, prior, profile, strings.TrimSpace(n.Session))
			notifications, suppressed, next = selectNotifications(items, prior, n.RenotifyAfter, now())
			return writeNotifyState(statePath, next)
		})
		if err != nil {
			return err
		}
	}
	var sinkResults []notifier.Result
	var deliverySummary notifyDeliverySummary
	if n.Deliver {
		policy := team.EffectiveOperatorNotifications(t.Operator)
		if !policy.Enabled {
			return usageErrorf("--deliver requires operator.notifications.enabled=true")
		}
		ctx := n.Context
		if ctx == nil {
			ctx = context.Background()
		}
		sinkResults, deliverySummary, err = deliverNotificationSinksPersisted(ctx, n.ProjectDir, statePath, items, policy, n.RenotifyAfter, now(), n.ForceResend)
		if err != nil {
			return err
		}
		next, _ = readNotifyState(statePath)
	}
	data := notifyEnvelopeData{
		ProjectDir:      n.ProjectDir,
		BaseRoot:        snap.BaseRoot,
		Profile:         profile,
		Operator:        operator,
		RenotifyAfter:   n.RenotifyAfter.String(),
		Notifications:   notifications,
		Suppressed:      suppressed,
		StatePath:       statePath,
		OperatorGates:   true,
		SinkResults:     sinkResults,
		DeliverySummary: deliverySummary,
	}
	if n.JSON {
		return writeJSONEnvelope(out, "notify", data)
	}
	return renderNotify(out, data)
}

func deliverNotificationSinksPersisted(ctx context.Context, projectDir, path string, items []operatorAttention, policy team.OperatorNotificationPolicy, renotify time.Duration, now time.Time, force bool) ([]notifier.Result, notifyDeliverySummary, error) {
	var results []notifier.Result
	var summary notifyDeliverySummary
	allowed := map[string]bool{}
	for _, e := range policy.Events {
		allowed[e] = true
	}
	if len(allowed) == 0 {
		allowed["gate"] = true
		allowed["local_input_blocked"] = true
		allowed["self_approved"] = true
		allowed["human_only_gate"] = true
		allowed["compound_release_child"] = true
		allowed["compound_release_recovery"] = true
		allowed["compound_release_degraded"] = true
	}
	for _, cfg := range policy.Sinks {
		for _, item := range items {
			if item.Cleared || !allowed[item.EventType] || (item.EventType != "gate" && item.EventType != "local_input_blocked" && item.EventType != "self_approved" && item.EventType != "human_only_gate" && item.EventType != "compound_release_child" && item.EventType != "compound_release_recovery" && item.EventType != "compound_release_degraded") {
				continue
			}
			token := ""
			var event attention.Event
			reserved := false
			err := flock.WithLock(path+".lock", func() error {
				st, e := readNotifyState(path)
				if e != nil {
					return e
				}
				rec := st.Items[item.Key]
				d := rec.Deliveries[cfg.ID]
				notify := force || !rec.Active || d.Fingerprint != item.LatestID || d.LastNotified.IsZero()
				if !notify && operatorAttentionEscalated(item, notifyStateRecord{LastEscalation: d.LastEscalation}) {
					notify = true
				}
				if !notify && renotify > 0 && now.Sub(d.LastNotified) >= renotify {
					notify = true
				}
				if d.ReservationToken != "" && d.ReservationExpires.After(now) {
					notify = false
				}
				if !notify {
					return nil
				}
				token = randomToken()
				d.ReservationToken = token
				// Keep the claim alive for the entire configured sink deadline plus
				// a small commit margin. A fixed 30s reservation allowed a second
				// watcher/manual notify process to retry a valid 60s command sink
				// while the first delivery was still running.
				d.ReservationExpires = now.Add(notificationReservationTTL(cfg))
				d.LastAttempt = now
				if rec.Deliveries == nil {
					rec.Deliveries = map[string]attention.Delivery{}
				}
				rec.Deliveries[cfg.ID] = d
				rec.Active = true
				rec.Fingerprint = item.LatestID
				st.Items[item.Key] = rec
				event = attention.Event{SchemaVersion: 1, EventType: item.EventType, Key: item.Key, Fingerprint: item.LatestID, ProjectDir: projectDir, Profile: item.Profile, Session: item.Session, Thread: item.Thread, Role: item.Role, GateKind: item.GateKind, Actor: item.Actor, PolicyRevision: item.PolicyRevision, Subject: item.Subject, Summary: item.Summary, Escalation: item.Escalation, Age: item.Age, InspectCommand: item.Inspect, AttentionOnly: true, ObservedAt: now}
				reserved = true
				return writeNotifyState(path, st)
			})
			if err != nil {
				return results, summary, err
			}
			if !reserved {
				summary.Suppressed++
				continue
			}
			summary.Selected++
			deliveryErr := notificationSinkFactory(cfg).Deliver(ctx, event)
			results = append(results, notifier.Result{SinkID: cfg.ID, Delivered: deliveryErr == nil, Error: errorString(deliveryErr)})
			if deliveryErr == nil {
				summary.Delivered++
			} else {
				summary.Failed++
			}
			commitErr := flock.WithLock(path+".lock", func() error {
				st, e := readNotifyState(path)
				if e != nil {
					return e
				}
				rec := st.Items[item.Key]
				d := rec.Deliveries[cfg.ID]
				if d.ReservationToken != token {
					return nil
				}
				d.ReservationToken = ""
				d.ReservationExpires = time.Time{}
				if deliveryErr == nil {
					d.Fingerprint = item.LatestID
					d.LastNotified = now
					d.LastSuccess = now
					d.LastEscalation = item.Escalation
					d.LastError = ""
				} else {
					d.LastFailure = now
					d.FailureCount++
					d.LastError = attention.NormalizeDeliveryError(errorString(deliveryErr))
				}
				rec.Deliveries[cfg.ID] = d
				st.Items[item.Key] = rec
				return writeNotifyState(path, st)
			})
			if commitErr != nil {
				return results, summary, commitErr
			}
		}
	}
	return results, summary, nil
}

func notificationReservationTTL(cfg team.OperatorNotificationSinkConfig) time.Duration {
	timeout, err := time.ParseDuration(strings.TrimSpace(cfg.Timeout))
	if err != nil || timeout <= 0 {
		timeout = 10 * time.Second
	}
	return timeout + 5*time.Second
}
func randomToken() string {
	var b [12]byte
	if _, e := rand.Read(b[:]); e != nil {
		return fmt.Sprint(time.Now().UnixNano())
	}
	return fmt.Sprintf("%x", b[:])
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
			eventType := "surface"
			if strings.HasPrefix(th.ID, "gate/") {
				eventType = "gate"
			}
			answerable := eventType == "gate"
			item := operatorAttention{
				EventType:   eventType,
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
				Actionable:  true,
				Answerable:  answerable,
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

// collectGateAttentionProjection is the authoritative gate-open projection for
// notify, operator status, and next. Raw legacy gates retain structural
// any-answer closure. A typed authorization_request gate closes only when the
// shared evidence resolver accepts an exact v2 answer and durable receipt.
func collectGateAttentionProjection(t team.Team, projectDir, profile string, snap state.Snapshot, operatorHandle, onlySession string, now time.Time) []operatorAttention {
	return collectGateAttentionProjectionCaptured(t, projectDir, profile, snap, operatorHandle, onlySession, now, nil)
}

func collectGateAttentionProjectionCaptured(t team.Team, projectDir, profile string, snap state.Snapshot, operatorHandle, onlySession string, now time.Time, captures map[string]operatorSessionCapture) []operatorAttention {
	var out []operatorAttention
	profile = squadnamespace.NormalizeProfile(profile)
	for _, sess := range snap.Sessions {
		if !squadnamespace.ProfilesEqual(profile, sess.TeamProfile) {
			continue
		}
		if onlySession != "" && sess.Name != onlySession {
			continue
		}
		capture, captured := captures[sess.Name]
		msgs, warnings := capture.Messages, capture.Warnings
		if !captured {
			msgs, warnings = scanOperatorSessionMessages(sess.Root, func() time.Time { return now })
		}
		byThread := map[string][]state.Message{}
		for _, msg := range msgs {
			if strings.HasPrefix(msg.Thread, "gate/") {
				byThread[msg.Thread] = append(byThread[msg.Thread], msg)
			}
		}
		for thread, threadMsgs := range byThread {
			threadMsgs, conflict := dedupeSecurityMessages(threadMsgs)
			sort.SliceStable(threadMsgs, func(i, j int) bool { return messageAfter(threadMsgs[j], threadMsgs[i]) })
			lifecycleMessages := make([]state.Message, 0, len(threadMsgs))
			for _, msg := range threadMsgs {
				if msg.From == operatorHandle || sessionHasAgentHandle(sess.Agents, msg.From) {
					lifecycleMessages = append(lifecycleMessages, msg)
				}
			}
			gateState, gate := state.ResolveOperatorGate(lifecycleMessages, operatorHandle, now)
			question := latestGateQuestionCandidate(threadMsgs, thread)
			if question == nil || !question.AuthorizationRequestPresent {
				// Projection-only legacy observability: review_request/decision records
				// without typed context remain visible, but can never become an
				// authorization candidate. A typed question, even malformed, remains
				// the barrier and cannot be displaced by a later non-question.
				var legacy *state.Message
				for i := range threadMsgs {
					msg := threadMsgs[i]
					if gateState == state.OperatorGateStateOpen && gate != nil && msg.ID != gate.LatestID {
						continue
					}
					if (msg.Kind == state.KindQuestion || msg.Kind == state.KindReviewRequest || msg.Kind == state.KindDecision) && !msg.AuthorizationRequestPresent && rawOperatorGateRequestMessage(msg, operatorHandle, sess.Agents) && (legacy == nil || messageAfter(msg, *legacy)) {
						copy := msg
						legacy = &copy
					}
				}
				question = legacy
			}
			if question == nil {
				continue
			}
			item := operatorAttention{
				EventType:   "gate",
				Key:         notifyKey(profile, sess.Name, thread),
				Profile:     profile,
				Session:     sess.Name,
				NamespaceID: sess.NamespaceID,
				Thread:      thread,
				LatestID:    question.ID,
				From:        question.From,
				Subject:     question.Subject,
				Kind:        question.Kind,
				Reason:      state.ClassifyAttnSubject(question.Subject),
				LastEventAt: question.Created,
				Inspect:     notifyInspectCommand(projectDir, profile, sess.Name, thread),
				Respond:     notifyRespondCommand(operatorHandle, question.From, thread, state.ClassifyAttnSubject(question.Subject)),
				Actionable:  true,
				Answerable:  true,
			}
			age := now.Sub(question.Created)
			if age < 0 {
				age = 0
			}
			item.Age = roundDuration(age).String()
			item.Escalation = string(state.OperatorGateEscalationForAge(age))

			if conflict {
				item.Summary = "duplicate_message_conflict"
				item.Answerable = false
				item.Respond = ""
				out = append(out, item)
				continue
			}
			if len(warnings) > 0 {
				item.Summary = "message_scan_degraded"
				item.Answerable = false
				item.Respond = ""
				out = append(out, item)
				continue
			}
			if gateState == state.OperatorGateStateClosed || gateState == state.OperatorGateStateWithdrawn {
				item.Cleared = true
				item.Actionable = false
				item.Answerable = false
				item.Respond = ""
				out = append(out, item)
				continue
			}
			if !question.AuthorizationRequestPresent {
				closed := gateState == state.OperatorGateStateAnswered
				item.Cleared = closed
				item.Actionable = !closed
				item.Answerable = !closed
				if closed {
					item.Respond = ""
				}
				out = append(out, item)
				continue
			}

			questionEvidence := resolveTypedHumanEvidence(projectDir, profile, sess.Name, thread, operatorHandle, t, *question, nil)
			if questionEvidence.Reason != "gate_pending" {
				item.Summary = questionEvidence.Reason
				item.Answerable = false
				item.Respond = ""
				out = append(out, item)
				continue
			}
			facts := make([]operatorauth.MessageFact, 0)
			reasons := map[string]string{}
			for i := range threadMsgs {
				msg := threadMsgs[i]
				if msg.From != operatorHandle || !messageAfter(msg, *question) {
					continue
				}
				evidence := resolveTypedHumanEvidence(projectDir, profile, sess.Name, thread, operatorHandle, t, *question, &msg)
				bound := evidence.Disposition == actionDecisionApproved || evidence.Disposition == actionDecisionDenied
				reasons[msg.ID] = evidence.Reason
				facts = append(facts, operatorauth.MessageFact{ID: msg.ID, From: msg.From, Kind: string(msg.Kind), Decision: evidence.Disposition, Bound: bound, After: true, Order: int64(i + 1)})
			}
			precedence := operatorauth.ResolvePrecedence(facts, operatorHandle, "")
			switch {
			case precedence.Decision == actionDecisionApproved || precedence.Decision == actionDecisionDenied:
				item.Cleared = true
				item.Actionable = false
				item.Answerable = false
				item.Respond = ""
				item.LatestID = precedence.MessageID
			case precedence.Barrier:
				item.LatestID = precedence.MessageID
				item.Summary = reasons[precedence.MessageID]
				if item.Summary == "" {
					item.Summary = "human_intervention_pending"
				}
			default:
				item.Summary = "gate_pending"
			}
			out = append(out, item)
		}
	}
	sortOperatorAttention(out)
	return out
}

// collectSelfOperatorVisibilityAttention emits normalized, attention-only
// observations. These records never answer a gate and are not consumed by the
// action verifier.
func collectSelfOperatorVisibilityAttention(t team.Team, projectDir, profile string, snap state.Snapshot, onlySession string, now time.Time) []operatorAttention {
	return collectSelfOperatorVisibilityAttentionCaptured(t, projectDir, profile, snap, onlySession, now, nil)
}

func collectSelfOperatorVisibilityAttentionCaptured(t team.Team, projectDir, profile string, snap state.Snapshot, onlySession string, now time.Time, captures map[string]operatorSessionCapture) []operatorAttention {
	var out []operatorAttention
	if t.Operator == nil || t.Operator.InteractionMode != team.OperatorInteractionSelfOperator {
		return nil
	}
	profile = squadnamespace.NormalizeProfile(profile)
	for _, sess := range snap.Sessions {
		if !squadnamespace.ProfilesEqual(profile, sess.TeamProfile) || onlySession != "" && sess.Name != onlySession {
			continue
		}
		view := team.EffectiveSelfOperator(t, sess.Name)
		capture, captured := captures[sess.Name]
		msgs, warnings := capture.Messages, capture.Warnings
		if !captured {
			msgs, warnings = scanOperatorSessionMessages(sess.Root, func() time.Time { return now })
		}
		if len(warnings) > 0 {
			thread := "gate/degraded-scan"
			out = append(out, operatorAttention{EventType: "human_only_gate", Key: attention.HumanOnlyGateKey(profile, sess.Name, thread), Profile: profile, Session: sess.Name, NamespaceID: sess.NamespaceID, Thread: thread, LatestID: "degraded", Subject: "message scan degraded", Summary: "message scan degraded; visibility fails closed", Inspect: "amq-squad status --project " + notifyShellQuote(projectDir) + " --profile " + notifyShellQuote(profile) + " --session " + notifyShellQuote(sess.Name), LastEventAt: now, Actionable: true, Answerable: false})
			continue
		}
		var conflict bool
		msgs, conflict = dedupeSecurityMessages(msgs)
		byThread := map[string][]state.Message{}
		for _, msg := range msgs {
			if strings.HasPrefix(msg.Thread, "gate/") {
				byThread[msg.Thread] = append(byThread[msg.Thread], msg)
			}
		}
		for thread, threadMsgs := range byThread {
			inspect := notifyInspectCommand(projectDir, profile, sess.Name, thread)
			selfItem := operatorAttention{EventType: "self_approved", Key: attention.SelfApprovedKey(profile, sess.Name, thread), Profile: profile, Session: sess.Name, NamespaceID: sess.NamespaceID, Thread: thread, Cleared: true, Inspect: inspect, Actionable: false, Answerable: false}
			humanItem := operatorAttention{EventType: "human_only_gate", Key: attention.HumanOnlyGateKey(profile, sess.Name, thread), Profile: profile, Session: sess.Name, NamespaceID: sess.NamespaceID, Thread: thread, Cleared: true, Inspect: inspect, Actionable: false, Answerable: false}
			q := latestGateQuestionCandidate(threadMsgs, thread)
			if q == nil {
				out = append(out, selfItem, humanItem)
				continue
			}
			selfItem.LatestID = q.ID
			humanItem.LatestID = q.ID
			selfItem.LastEventAt = q.Created
			humanItem.LastEventAt = q.Created
			humanItem.Subject = q.Subject
			humanItem.Kind = q.Kind
			if conflict {
				activateInspectOnlyAttention(&humanItem)
				humanItem.Summary = "conflicting or malformed gate state requires human attention"
				out = append(out, selfItem, humanItem)
				continue
			}
			questionEvidence := resolveTypedHumanEvidence(projectDir, profile, sess.Name, thread, team.EffectiveOperator(t).Handle, t, *q, nil)
			typedRequestValid := questionEvidence.Reason == "gate_pending"
			var binding operatorauth.Binding
			if q.AuthorizationRequestPresent {
				if !typedRequestValid {
					activateInspectOnlyAttention(&humanItem)
					humanItem.Summary = questionEvidence.Reason
					out = append(out, selfItem, humanItem)
					continue
				}
				binding = questionEvidence.Binding
			} else {
				var bindErr error
				binding, bindErr = operatorauth.ParseStrictBinding(q.Subject + "\n" + q.Body)
				if bindErr != nil {
					activateInspectOnlyAttention(&humanItem)
					humanItem.Summary = "conflicting or malformed gate state requires human attention"
					out = append(out, selfItem, humanItem)
					continue
				}
			}
			humanItem.GateKind = binding.GateKind
			action, actionErr := operatorauth.CanonicalAction(binding.Action)
			if actionErr != nil {
				activateInspectOnlyAttention(&humanItem)
				humanItem.Summary = "conflicting or malformed gate state requires human attention"
				out = append(out, selfItem, humanItem)
				continue
			}
			target := binding.Target
			facts := []operatorauth.MessageFact{}
			reasons := map[string]string{}
			for i, msg := range threadMsgs {
				if msg.From != team.EffectiveOperator(t).Handle || !messageAfter(msg, *q) {
					continue
				}
				decision, bound := actionDecisionPending, false
				if typedRequestValid {
					evidence := resolveTypedHumanEvidence(projectDir, profile, sess.Name, thread, team.EffectiveOperator(t).Handle, t, *q, &msg)
					decision = evidence.Disposition
					bound = decision == actionDecisionApproved || decision == actionDecisionDenied
					reasons[msg.ID] = evidence.Reason
				} else if msg.Kind == state.KindAnswer && strictBindingMatches(msg.Subject+"\n"+msg.Body, action, target) {
					decision = classifyDecision(msg.Subject + "\n" + msg.Body)
					bound = decision == actionDecisionApproved || decision == actionDecisionDenied
					if msg.ApprovalPresent {
						bound = bound && validTypedHumanApproval(msg, team.EffectiveOperator(t).Handle, q.ID, binding, action, target)
					}
				}
				facts = append(facts, operatorauth.MessageFact{ID: msg.ID, From: msg.From, Decision: decision, Bound: bound, After: true, Order: int64(i + 1)})
			}
			precedence := operatorauth.ResolvePrecedence(facts, team.EffectiveOperator(t).Handle, view.LeadHandle)
			if precedence.Decision == actionDecisionApproved || precedence.Decision == actionDecisionDenied {
				out = append(out, selfItem, humanItem)
				continue
			}
			if precedence.Barrier {
				activateInspectOnlyAttention(&humanItem)
				humanItem.Summary = reasons[precedence.MessageID]
				if humanItem.Summary == "" {
					humanItem.Summary = "human intervention pending; inspect durable gate"
				}
				out = append(out, selfItem, humanItem)
				continue
			}
			var answer *state.Message
			for i := range threadMsgs {
				m := threadMsgs[i]
				if m.Kind == state.KindAnswer && m.From == view.LeadHandle && messageAfter(m, *q) && (answer == nil || messageAfter(m, *answer)) {
					c := m
					answer = &c
				}
			}
			allowed := typedRequestValid && view.Enabled && q.AuthorizationRequest.GateKind == operatorauth.GateMerge && operatorauth.Evaluate(q.AuthorizationRequest.GateKind, q.AuthorizationRequest.Action, view.AllowedGateKinds) == nil
			validSelf := false
			if allowed && answer != nil && answer.ApprovalValid && answer.Approval != nil {
				a := *answer.Approval
				validSelf = answer.From == view.LeadHandle && a.SchemaVersion == operatorauth.ApprovalSchemaVersion && a.TaxonomyVersion == operatorauth.ActionTaxonomyVersion && a.Source == "self_operator" && a.SelfApproved && a.AnsweredByRole == view.LeadRole && a.AnsweredByHandle == view.LeadHandle && a.QuestionMessageID == q.ID && canonicalGateActionMatches(a.GateKind, a.Action, q.AuthorizationRequest.GateKind, q.AuthorizationRequest.Action) && a.Target == q.AuthorizationRequest.Target && a.Note == q.AuthorizationRequest.Note && a.PolicyRevision == view.PolicyRevision && a.PolicyHash == view.PolicyHash && validateApprovalReceipt(projectDir, profile, sess.Name, thread, *answer, a) == nil && revalidateSelfApprovalEvidence(projectDir, a, target) == nil
				if validSelf {
					activateInspectOnlyAttention(&selfItem)
					selfItem.LatestID = answer.ID
					selfItem.Actor = a.AnsweredByHandle
					selfItem.GateKind = a.GateKind
					selfItem.PolicyRevision = a.PolicyRevision
					selfItem.Subject = answer.Subject
					selfItem.Summary = "self-approved gate observed; human may inspect, intervene, or revoke"
				}
			}
			if !validSelf && (!allowed || answer != nil) {
				activateInspectOnlyAttention(&humanItem)
				humanItem.Summary = "human-only gate requires operator attention; notification does not authorize an answer"
			}
			out = append(out, selfItem, humanItem)
		}
	}
	return out
}

func maxDuration(a, b time.Duration) time.Duration {
	if b > a {
		return b
	}
	return a
}

func collectLocalInputAttention(t team.Team, profile, session string, now time.Time) []operatorAttention {
	if strings.TrimSpace(session) == "" {
		return nil
	}
	type observed struct {
		blocker tmuxpane.LocalInputBlocker
		ok      bool
		err     error
	}
	seen := map[string]observed{}
	detector := func(pane string) (tmuxpane.LocalInputBlocker, bool) {
		b, ok, err := notifyLocalInputDetector(pane)
		seen[pane] = observed{b, ok, err}
		return b, ok && err == nil
	}
	rows := buildStatusRowsWithLocalInputDetector(t, profile, session, defaultDuplicateLaunchProbe, detector)
	var out []operatorAttention
	for _, row := range rows {
		if !statusLocalInputCandidate(t, row) {
			continue
		}
		obs, exists := seen[row.Tmux.PaneID]
		if !exists {
			continue
		}
		blocker, ok, err := obs.blocker, obs.ok, obs.err
		if err != nil {
			continue
		}
		key := attention.LocalInputKey(profile, session, row.Role)
		if !ok {
			out = append(out, operatorAttention{EventType: "local_input_blocked", Key: key, Profile: profile, Session: session, Role: row.Role, Cleared: true, Actionable: false, Answerable: false})
			continue
		}
		fp := attention.LocalInputFingerprint(row.Tmux.PaneID, blocker.Kind, blocker.Destructive, blocker.Summary)
		summary := blocker.Summary
		if blocker.Destructive {
			summary = "destructive local prompt: " + summary
		}
		out = append(out, operatorAttention{EventType: "local_input_blocked", Key: key, Profile: profile, Session: session, NamespaceID: squadnamespace.ID(profile, session), LatestID: fp, Role: row.Role, Subject: "local input blocked", Summary: summary, Age: "0s", LastEventAt: now, Inspect: "amq-squad focus --project " + notifyShellQuote(t.Project) + " --profile " + notifyShellQuote(profile) + " --session " + notifyShellQuote(session) + " --role " + notifyShellQuote(row.Role), Actionable: true, Answerable: false})
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

func activateInspectOnlyAttention(item *operatorAttention) {
	item.Cleared = false
	item.Actionable = true
	item.Answerable = false
	item.Respond = ""
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
			// Conflicting projections fail open: an active observation always
			// beats a cleared tombstone, independent of merge order.
			if merged[i].Cleared && !item.Cleared {
				merged[i] = item
			} else if !merged[i].Cleared && item.Cleared {
				continue
			} else {
				merged[i] = item
			}
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

func scopedSelfOperatorTombstones(items []operatorAttention, prior notifyStateFile, profile, session string) []operatorAttention {
	present := map[string]bool{}
	for _, item := range items {
		present[item.Key] = true
	}
	prefix := squadnamespace.NormalizeProfile(profile) + "/"
	if session != "" {
		prefix += session + "\x00"
	}
	for key := range prior.Items {
		if present[key] || !strings.HasPrefix(key, prefix) {
			continue
		}
		kind := ""
		if strings.Contains(key, "\x00self_approved\x00") {
			kind = "self_approved"
		} else if strings.Contains(key, "\x00human_only_gate\x00") {
			kind = "human_only_gate"
		}
		if kind != "" {
			items = append(items, operatorAttention{EventType: kind, Key: key, Cleared: true, Actionable: false, Answerable: false})
		}
	}
	return items
}

func selectNotifications(items []operatorAttention, prior notifyStateFile, renotifyAfter time.Duration, now time.Time) ([]operatorAttention, int, notifyStateFile) {
	next := notifyStateFile{Schema: notifyStateSchema, Items: map[string]notifyStateRecord{}}
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
			for id, d := range rec.Deliveries {
				d.Fingerprint = ""
				rec.Deliveries[id] = d
			}
			next.Items[item.Key] = rec
			continue
		}
		notify := !rec.Active || rec.LatestID != item.LatestID || rec.LastNotified.IsZero()
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
	st := notifyStateFile{Schema: notifyStateSchema, Items: map[string]notifyStateRecord{}}
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
	if st.Schema != 1 && st.Schema != notifyStateSchema {
		return st, fmt.Errorf("parse notify state %s: unsupported schema %d", path, st.Schema)
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
		st.Schema = notifyStateSchema
	}
	return st, nil
}

func writeNotifyState(path string, st notifyStateFile) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("ensure notify state dir: %w", err)
	}
	st.Schema = notifyStateSchema // optional delivery fields are additive.
	if st.Items == nil {
		st.Items = map[string]notifyStateRecord{}
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal notify state: %w", err)
	}
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create notify state temp: %w", err)
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err := f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename notify state: %w", err)
	}
	return nil
}

func notifyKey(profile, session, thread string) string {
	return attention.GateKey(squadnamespace.NormalizeProfile(profile), session, thread)
}

func deliverNotificationSinks(ctx context.Context, projectDir string, items []operatorAttention, policy team.OperatorNotificationPolicy, st notifyStateFile, renotify time.Duration, now time.Time, force bool) ([]notifier.Result, notifyStateFile) {
	events := make([]attention.Event, 0, len(items))
	allowed := map[string]bool{}
	for _, kind := range policy.Events {
		allowed[kind] = true
	}
	if len(allowed) == 0 {
		for _, kind := range []string{"gate", "local_input_blocked", "self_approved", "human_only_gate", "compound_release_child", "compound_release_recovery", "compound_release_degraded"} {
			allowed[kind] = true
		}
	}
	for _, item := range items {
		eventType := item.EventType
		if eventType == "" {
			eventType = "gate"
		}
		if !allowed[eventType] {
			continue
		}
		summary := item.Summary
		if summary == "" {
			summary = "operator answer required"
		}
		events = append(events, attention.Event{SchemaVersion: 1, EventType: eventType, Key: item.Key, Fingerprint: item.LatestID, ProjectDir: projectDir, Profile: item.Profile, Session: item.Session, Thread: item.Thread, Role: item.Role, GateKind: item.GateKind, Actor: item.Actor, PolicyRevision: item.PolicyRevision, Subject: item.Subject, Summary: summary, Escalation: item.Escalation, Age: item.Age, InspectCommand: item.Inspect, AttentionOnly: true, ObservedAt: now, Cleared: item.Cleared})
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
	d, _ := time.ParseDuration(cfg.Timeout)
	if cfg.Type == "desktop" {
		return notifier.DesktopSink{SinkID: cfg.ID, Timeout: d}
	}
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
