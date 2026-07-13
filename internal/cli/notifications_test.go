package cli

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/omriariav/amq-squad/v2/internal/attention"
	"github.com/omriariav/amq-squad/v2/internal/notifier"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

type notificationsProbeSink struct {
	id      string
	calls   *int
	event   *attention.Event
	receipt string
	err     error
}

func (s notificationsProbeSink) ID() string { return s.id }
func (s notificationsProbeSink) Deliver(context.Context, attention.Event) error {
	panic("probe should prefer DeliverWithReceipt when the sink supports it")
}
func (s notificationsProbeSink) DeliverWithReceipt(_ context.Context, event attention.Event) (string, error) {
	*s.calls++
	*s.event = event
	return s.receipt, s.err
}

func TestNotificationsDoctorAndHistoryAreStrictlyReadOnly(t *testing.T) {
	now := time.Date(2026, 7, 13, 9, 0, 0, 0, time.UTC)
	project, base, statePath := seedNotificationsObservabilityProject(t)
	seedNotifyLaunch(t, project, base, "s", "cto")
	seedNotifyMessage(t, base, "s", "user", "new", notifyMsg{ID: "gate-1", From: "cto", To: "user", Thread: "gate/release", Subject: "APPROVAL: release", Kind: "question", Created: now})
	key := "default/s\x00gate\x00gate/release"
	longError := strings.Repeat("x", 520) + "\nforged row\tcontinued"
	state := notifyStateFile{Schema: notifyStateSchema, Items: map[string]notifyStateRecord{
		key: {
			LatestID: "gate-1", Fingerprint: "gate-1", Active: true, LastObserved: now.Add(-time.Minute),
			Deliveries: map[string]attention.Delivery{"desktop": {Fingerprint: "gate-1", LastAttempt: now.Add(-40 * time.Second), LastFailure: now.Add(-40 * time.Second), FailureCount: 2, LastError: longError, ReservationToken: "owner-token", ReservationExpires: now.Add(time.Minute)}},
		},
		"default/older\x00gate\x00gate/old": {LatestID: "old", Active: false, LastObserved: now.Add(-time.Hour)},
	}}
	if err := writeNotifyState(statePath, state); err != nil {
		t.Fatal(err)
	}

	before := snapshotNotificationsTree(t, project)
	var doctorOut bytes.Buffer
	err := executeNotificationsDoctor(notificationsDoctorExecution{ProjectDir: project, Profile: team.DefaultProfile, Session: "s", StatePath: statePath, Limit: 10, JSON: true, Out: &doctorOut, Now: func() time.Time { return now }})
	if err == nil || !strings.Contains(err.Error(), "runtime is absent") {
		t.Fatalf("doctor should report enabled+absent watcher unhealthy, got %v", err)
	}
	doctor := decodeJSONEnvelope[notificationsDoctorData](t, doctorOut.String()).Data
	if doctor.Healthy || !doctor.Policy.Enabled || !doctor.Watcher.PolicyEnabled || doctor.Watcher.Expected || doctor.Watcher.Running || doctor.Watcher.Health != "unhealthy" {
		t.Fatalf("doctor watcher view = %+v", doctor.Watcher)
	}
	if doctor.State.Schema != notifyStateSchema || doctor.State.PendingEventCount != 1 || doctor.State.TotalEventCount != 1 || len(doctor.State.Events) != 1 {
		t.Fatalf("doctor state view = %+v", doctor.State)
	}
	delivery := doctor.State.Events[0].Deliveries[0]
	if delivery.LastAttempt.IsZero() || !delivery.Reservation.Active || strings.ContainsAny(delivery.LastError, "\r\n\t") || utf8.RuneCountInString(delivery.LastError) > 512 {
		t.Fatalf("doctor delivery view = %+v", delivery)
	}

	var historyOut bytes.Buffer
	if err := executeNotificationsHistory(notificationsHistoryExecution{ProjectDir: project, Profile: team.DefaultProfile, StatePath: statePath, Limit: 1, JSON: true, Out: &historyOut, Now: func() time.Time { return now }}); err != nil {
		t.Fatal(err)
	}
	history := decodeJSONEnvelope[notificationsHistoryData](t, historyOut.String()).Data
	if history.State.TotalEventCount != 2 || history.State.ShownEventCount != 1 || !history.State.Truncated || history.State.Events[0].Session != "s" {
		t.Fatalf("bounded history = %+v", history.State)
	}
	after := snapshotNotificationsTree(t, project)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("doctor/history mutated project tree\nbefore=%v\nafter=%v", before, after)
	}
}

func TestNotificationsProbeUsesOneSinkAndDoesNotMutateStateOrGateMailboxes(t *testing.T) {
	now := time.Date(2026, 7, 13, 9, 30, 0, 0, time.UTC)
	project, base, statePath := seedNotificationsObservabilityProject(t)
	seedNotifyLaunch(t, project, base, "s", "cto")
	seedNotifyMessage(t, base, "s", "user", "new", notifyMsg{ID: "gate-1", From: "cto", To: "user", Thread: "gate/release", Subject: "APPROVAL: release", Kind: "question", Created: now})
	if err := writeNotifyState(statePath, notifyStateFile{Schema: notifyStateSchema, Items: map[string]notifyStateRecord{"default/s\x00gate\x00gate/release": {LatestID: "gate-1", Active: true}}}); err != nil {
		t.Fatal(err)
	}
	before := snapshotNotificationsTree(t, project)

	oldFactory := notificationSinkFactory
	defer func() { notificationSinkFactory = oldFactory }()
	calls := 0
	var got attention.Event
	notificationSinkFactory = func(cfg team.OperatorNotificationSinkConfig) notifier.Sink {
		return notificationsProbeSink{id: cfg.ID, calls: &calls, event: &got, receipt: "receipt-123"}
	}
	var out bytes.Buffer
	if err := executeNotificationsProbe(notificationsProbeExecution{ProjectDir: project, Profile: team.DefaultProfile, Session: "s", SinkID: "desktop", JSON: true, Out: &out, Now: func() time.Time { return now }}); err != nil {
		t.Fatal(err)
	}
	probe := decodeJSONEnvelope[notificationsProbeData](t, out.String()).Data
	if calls != 1 || !probe.Success || probe.Receipt != "receipt-123" || probe.ProbeID == "" {
		t.Fatalf("probe=%+v calls=%d", probe, calls)
	}
	if got.EventType != "probe" || !got.AttentionOnly || strings.Contains(got.Key, "\x00gate\x00") || got.Fingerprint != probe.ProbeID {
		t.Fatalf("probe event = %+v", got)
	}
	after := snapshotNotificationsTree(t, project)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("probe mutated notify state or mailbox tree\nbefore=%v\nafter=%v", before, after)
	}
}

func TestPersistedDeliveryRecordsAttemptAndSanitizedBoundedError(t *testing.T) {
	oldFactory := notificationSinkFactory
	defer func() { notificationSinkFactory = oldFactory }()
	calls := 0
	notificationSinkFactory = func(cfg team.OperatorNotificationSinkConfig) notifier.Sink {
		return deliveryFakeSink{id: cfg.ID, calls: &calls, err: errors.New(strings.Repeat("failure ", 90) + "\nforged\trow")}
	}
	path := filepath.Join(t.TempDir(), "notify-state.json")
	now := time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC)
	item := operatorAttention{EventType: "gate", Key: "default/s\x00gate\x00gate/release", LatestID: "gate-1", Profile: team.DefaultProfile, Session: "s"}
	policy := team.OperatorNotificationPolicy{Sinks: []team.OperatorNotificationSinkConfig{{ID: "desktop", Type: "desktop", Timeout: "10s"}}}
	_, summary, err := deliverNotificationSinksPersisted(context.Background(), "/project", path, []operatorAttention{item}, policy, time.Hour, now, false)
	if err != nil || summary.Failed != 1 || calls != 1 {
		t.Fatalf("summary=%+v calls=%d err=%v", summary, calls, err)
	}
	state, err := readNotifyState(path)
	if err != nil {
		t.Fatal(err)
	}
	delivery := state.Items[item.Key].Deliveries["desktop"]
	if !delivery.LastAttempt.Equal(now) || !delivery.LastFailure.Equal(now) || delivery.FailureCount != 1 {
		t.Fatalf("delivery timestamps/count = %+v", delivery)
	}
	if delivery.LastError == "" || strings.ContainsAny(delivery.LastError, "\r\n\t") || utf8.RuneCountInString(delivery.LastError) > 512 {
		t.Fatalf("last_error was not sanitized and bounded: %q", delivery.LastError)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte(`"schema": 2`)) {
		t.Fatalf("additive fields should keep notify-state schema 2:\n%s", raw)
	}
}

func TestNotificationProbeSinkSelection(t *testing.T) {
	sinks := []team.OperatorNotificationSinkConfig{{ID: "desktop"}, {ID: "audit"}}
	if _, err := selectNotificationProbeSink(sinks, ""); err == nil || !strings.Contains(err.Error(), "requires --sink") {
		t.Fatal(err)
	}
	if got, err := selectNotificationProbeSink(sinks, "audit"); err != nil || got.ID != "audit" {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	if _, err := selectNotificationProbeSink(sinks, "missing"); err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatal(err)
	}
}

func TestNotificationsPendingCountHonorsEffectivePolicyAndRecentOrderIgnoresReservationExpiry(t *testing.T) {
	now := time.Date(2026, 7, 13, 11, 0, 0, 0, time.UTC)
	state := notifyStateFile{Schema: notifyStateSchema, Items: map[string]notifyStateRecord{
		"default/s\x00gate\x00gate/current": {
			Active: true, LastObserved: now.Add(-time.Hour),
			Deliveries: map[string]attention.Delivery{"desktop": {LastAttempt: now.Add(-time.Minute), ReservationToken: "hidden", ReservationExpires: now.Add(24 * time.Hour)}},
		},
		"default/s\x00local_input_blocked\x00cto": {Active: true, LastObserved: now},
		"default/s\x00surface\x00question/x":      {Active: true, LastObserved: now.Add(time.Minute)},
	}}
	policy := team.OperatorNotificationPolicy{Enabled: true, Events: []string{"gate"}, Sinks: []team.OperatorNotificationSinkConfig{{ID: "desktop"}}}
	view := buildNotificationsStateView("state.json", state, team.DefaultProfile, "s", 10, now, policy)
	if view.PendingEventCount != 1 {
		t.Fatalf("pending=%d, want only the policy-allowed gate", view.PendingEventCount)
	}
	gate := view.Events[2]
	for _, event := range view.Events {
		if event.EventType == "gate" {
			gate = event
		}
	}
	if gate.RecentAt.After(now) || !gate.RecentAt.Equal(now.Add(-time.Minute)) {
		t.Fatalf("reservation expiry must not drive recent order: %+v", gate)
	}
	if len(gate.Deliveries) != 1 || !gate.Deliveries[0].Reservation.Present || !gate.Deliveries[0].Reservation.Active {
		t.Fatalf("reservation observability = %+v", gate.Deliveries)
	}
}

func TestNotificationsPendingEventCountTracksUncommittedConfiguredSinks(t *testing.T) {
	now := time.Date(2026, 7, 13, 11, 30, 0, 0, time.UTC)
	success := now.Add(-2 * time.Minute)
	failed := now.Add(-time.Minute)
	committed := func() attention.Delivery {
		return attention.Delivery{Fingerprint: "current", LastSuccess: success, LastNotified: success}
	}
	tests := []struct {
		name  string
		sinks []team.OperatorNotificationSinkConfig
		del   map[string]attention.Delivery
		want  int
	}{
		{name: "no sinks means no pending work", want: 0},
		{name: "missing delivery", sinks: []team.OperatorNotificationSinkConfig{{ID: "desktop"}}, want: 1},
		{name: "matching successful delivery", sinks: []team.OperatorNotificationSinkConfig{{ID: "desktop"}}, del: map[string]attention.Delivery{"desktop": committed()}, want: 0},
		{name: "matching fingerprint without committed notification", sinks: []team.OperatorNotificationSinkConfig{{ID: "desktop"}}, del: map[string]attention.Delivery{"desktop": {Fingerprint: "current", LastSuccess: success}}, want: 1},
		{name: "fingerprint mismatch", sinks: []team.OperatorNotificationSinkConfig{{ID: "desktop"}}, del: map[string]attention.Delivery{"desktop": {Fingerprint: "old", LastSuccess: success}}, want: 1},
		{name: "failed force resend preserves prior commit", sinks: []team.OperatorNotificationSinkConfig{{ID: "desktop"}}, del: map[string]attention.Delivery{"desktop": {Fingerprint: "current", LastNotified: success, LastSuccess: success, LastFailure: failed}}, want: 0},
		{name: "live reservation remains pending", sinks: []team.OperatorNotificationSinkConfig{{ID: "desktop"}}, del: map[string]attention.Delivery{"desktop": {Fingerprint: "current", LastNotified: success, LastSuccess: success, ReservationToken: "private", ReservationExpires: now.Add(time.Minute)}}, want: 1},
		{name: "expired reservation with committed fingerprint", sinks: []team.OperatorNotificationSinkConfig{{ID: "desktop"}}, del: map[string]attention.Delivery{"desktop": {Fingerprint: "current", LastNotified: success, LastSuccess: success, ReservationToken: "private", ReservationExpires: now.Add(-time.Minute)}}, want: 0},
		{name: "one of two sinks missing", sinks: []team.OperatorNotificationSinkConfig{{ID: "desktop"}, {ID: "audit"}}, del: map[string]attention.Delivery{"desktop": committed()}, want: 1},
		{name: "all configured sinks committed", sinks: []team.OperatorNotificationSinkConfig{{ID: "desktop"}, {ID: "audit"}}, del: map[string]attention.Delivery{"desktop": committed(), "audit": committed()}, want: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			state := notifyStateFile{Schema: notifyStateSchema, Items: map[string]notifyStateRecord{
				"default/s\x00gate\x00gate/current": {LatestID: "current", Fingerprint: "current", Active: true, Deliveries: tc.del},
			}}
			policy := team.OperatorNotificationPolicy{Enabled: true, Events: []string{"gate"}, Sinks: tc.sinks}
			view := buildNotificationsStateView("state.json", state, team.DefaultProfile, "s", 10, now, policy)
			if view.PendingEventCount != tc.want {
				t.Fatalf("pending=%d want=%d view=%+v", view.PendingEventCount, tc.want, view)
			}
		})
	}
}

func TestNotificationsDoctorTreatsCleanlyStoppedWatcherAsHealthy(t *testing.T) {
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	project, _, statePath := seedNotificationsObservabilityProject(t)
	record := notificationWatcherRecord{
		SchemaVersion: notificationWatcherSchema, ProjectDir: project, Profile: team.DefaultProfile,
		Session: "s", NamespaceID: "default/s", Expected: false, StatePath: statePath,
		Health: "inactive", UpdatedAt: now,
	}
	if err := writeNotificationWatcherRecord(notificationWatcherRuntimePath(project, team.DefaultProfile, "s"), record); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := executeNotificationsDoctor(notificationsDoctorExecution{ProjectDir: project, Profile: team.DefaultProfile, Session: "s", StatePath: statePath, Limit: 10, JSON: true, Out: &out, Now: func() time.Time { return now }}); err != nil {
		t.Fatalf("clean stop should be healthy-by-design: %v", err)
	}
	data := decodeJSONEnvelope[notificationsDoctorData](t, out.String()).Data
	if !data.Healthy || !data.Watcher.PolicyEnabled || data.Watcher.Expected || data.Watcher.Running || data.Watcher.Health != "inactive" {
		t.Fatalf("clean-stop watcher = %+v", data.Watcher)
	}
}

func TestNotificationsDoctorTreatsScannedDegradedWatcherAsOperationalAndSanitizesReasons(t *testing.T) {
	now := time.Date(2026, 7, 13, 12, 30, 0, 0, time.UTC)
	project, _, statePath := seedNotificationsObservabilityProject(t)
	unsafeError := strings.Repeat("watcher failure ", 60) + "\nforged\trow"
	record := notificationWatcherRecord{
		SchemaVersion: notificationWatcherSchema, ProjectDir: project, Profile: team.DefaultProfile,
		Session: "s", NamespaceID: "default/s", PID: 42, Host: "definitely-remote-host.invalid",
		Owner: "watcher", OwnerToken: "token", LeaseTTL: "15s", LeaseExpiresAt: now.Add(time.Minute),
		HeartbeatAt: now.Add(-time.Second), LastScanAt: now.Add(-2 * time.Second), StatePath: statePath,
		Expected: true, Health: "degraded", LastError: unsafeError, UpdatedAt: now,
	}
	path := notificationWatcherRuntimePath(project, team.DefaultProfile, "s")
	if err := writeNotificationWatcherRecord(path, record); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := executeNotificationsDoctor(notificationsDoctorExecution{ProjectDir: project, Profile: team.DefaultProfile, Session: "s", StatePath: statePath, Limit: 10, JSON: true, Out: &out, Now: func() time.Time { return now }}); err != nil {
		t.Fatalf("scanned degraded watcher should be operational: %v", err)
	}
	data := decodeJSONEnvelope[notificationsDoctorData](t, out.String()).Data
	if !data.Healthy || !data.Degraded || !data.Watcher.Running || data.Watcher.Health != "degraded" {
		t.Fatalf("degraded watcher = %+v", data)
	}
	assertBoundedSingleLine(t, "watcher reason", data.Watcher.Reason)
	assertBoundedSingleLine(t, "watcher last error", data.Watcher.LastError)

	record.Health = "failed"
	if err := writeNotificationWatcherRecord(path, record); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	err := executeNotificationsDoctor(notificationsDoctorExecution{ProjectDir: project, Profile: team.DefaultProfile, Session: "s", StatePath: statePath, Limit: 10, JSON: true, Out: &out, Now: func() time.Time { return now }})
	if err == nil {
		t.Fatal("failed watcher should remain unhealthy")
	}
	assertBoundedSingleLine(t, "doctor returned error", err.Error())
	failedData := decodeJSONEnvelope[notificationsDoctorData](t, out.String()).Data
	if failedData.Healthy || failedData.Degraded {
		t.Fatalf("failed watcher = %+v", failedData)
	}
	assertBoundedSingleLine(t, "failed watcher reason", failedData.Watcher.Reason)
}

func TestNotificationsProbeFailureSanitizesJSONAndReturnedError(t *testing.T) {
	project, _, _ := seedNotificationsObservabilityProject(t)
	oldFactory := notificationSinkFactory
	defer func() { notificationSinkFactory = oldFactory }()
	calls := 0
	var event attention.Event
	unsafeError := strings.Repeat("sink failure ", 70) + "\nforged\trow"
	notificationSinkFactory = func(cfg team.OperatorNotificationSinkConfig) notifier.Sink {
		return notificationsProbeSink{id: cfg.ID, calls: &calls, event: &event, err: errors.New(unsafeError)}
	}
	var out bytes.Buffer
	err := executeNotificationsProbe(notificationsProbeExecution{ProjectDir: project, Profile: team.DefaultProfile, Session: "s", SinkID: "desktop", JSON: true, Out: &out})
	if err == nil || calls != 1 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
	assertBoundedSingleLine(t, "probe returned error", err.Error())
	data := decodeJSONEnvelope[notificationsProbeData](t, out.String()).Data
	if data.Success {
		t.Fatalf("probe should report failure: %+v", data)
	}
	assertBoundedSingleLine(t, "probe JSON error", data.Error)
}

func assertBoundedSingleLine(t *testing.T, label, text string) {
	t.Helper()
	if text == "" || strings.ContainsAny(text, "\r\n\t") || utf8.RuneCountInString(text) > 512 {
		t.Fatalf("%s was not non-empty, single-line, and <=512 runes: %q", label, text)
	}
}

func seedNotificationsObservabilityProject(t *testing.T) (project, base, statePath string) {
	t.Helper()
	op := team.OperatorConfig{Enabled: true, Handle: "user"}
	op.Notifications = &team.OperatorNotificationPolicy{
		Enabled: true, DeliverySemantics: "attention_only", Events: []string{"gate"},
		Sinks: []team.OperatorNotificationSinkConfig{{ID: "desktop", Type: "desktop", Timeout: "10s"}, {ID: "audit", Type: "command", Argv: []string{"notify-audit"}, Timeout: "5s"}},
	}
	return seedNotifyProject(t, op)
}

func snapshotNotificationsTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			out[rel+"/"] = "dir"
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[rel] = string(body)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
