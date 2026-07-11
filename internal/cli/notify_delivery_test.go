package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/attention"
	"github.com/omriariav/amq-squad/v2/internal/notifier"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

type deliveryFakeSink struct {
	id    string
	err   error
	calls *int
}

type atomicDeliverySink struct{ calls *int32 }

func (atomicDeliverySink) ID() string { return "desktop" }
func (s atomicDeliverySink) Deliver(context.Context, attention.Event) error {
	atomic.AddInt32(s.calls, 1)
	time.Sleep(20 * time.Millisecond)
	return nil
}
func TestPersistedReservationConcurrentConsumersAndExpiry(t *testing.T) {
	old := notificationSinkFactory
	defer func() { notificationSinkFactory = old }()
	var calls int32
	notificationSinkFactory = func(team.OperatorNotificationSinkConfig) notifier.Sink { return atomicDeliverySink{&calls} }
	path := filepath.Join(t.TempDir(), "state.json")
	item := operatorAttention{EventType: "gate", Key: "default/s\x00gate\x00gate/x", LatestID: "m1", Profile: "default", Session: "s"}
	p := team.OperatorNotificationPolicy{Sinks: []team.OperatorNotificationSinkConfig{{ID: "desktop", Type: "desktop"}}}
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, _ = deliverNotificationSinksPersisted(context.Background(), "/project", path, []operatorAttention{item}, p, time.Hour, time.Now(), false)
		}()
	}
	wg.Wait()
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("deliveries=%d", calls)
	}
	st, err := readNotifyState(path)
	if err != nil {
		t.Fatal(err)
	}
	rec := st.Items[item.Key]
	d := rec.Deliveries["desktop"]
	d.Fingerprint = ""
	d.ReservationToken = "dead"
	d.ReservationExpires = time.Now().Add(-time.Minute)
	rec.Deliveries["desktop"] = d
	st.Items[item.Key] = rec
	if err := writeNotifyState(path, st); err != nil {
		t.Fatal(err)
	}
	_, _, err = deliverNotificationSinksPersisted(context.Background(), "/project", path, []operatorAttention{item}, p, time.Hour, time.Now(), false)
	if err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Fatalf("expired reservation not recovered: %d", calls)
	}
}
func TestExternalDeliveryFiltersSurfaceHonorsEventsAndUsesSinkSummary(t *testing.T) {
	old := notificationSinkFactory
	defer func() { notificationSinkFactory = old }()
	var calls int32
	notificationSinkFactory = func(team.OperatorNotificationSinkConfig) notifier.Sink { return atomicDeliverySink{&calls} }
	path := filepath.Join(t.TempDir(), "state.json")
	gate := operatorAttention{EventType: "gate", Key: "default/s\x00gate\x00gate/x", LatestID: "m1", Profile: "default", Session: "s"}
	generic := operatorAttention{EventType: "surface", Key: "default/s\x00question/x", LatestID: "q1", Profile: "default", Session: "s"}
	st := notifyStateFile{Schema: 2, Items: map[string]notifyStateRecord{gate.Key: {LatestID: "m1", LastNotified: time.Now()}}}
	if err := writeNotifyState(path, st); err != nil {
		t.Fatal(err)
	}
	p := team.OperatorNotificationPolicy{Events: []string{"gate"}, Sinks: []team.OperatorNotificationSinkConfig{{ID: "desktop", Type: "desktop"}}}
	res, sum, err := deliverNotificationSinksPersisted(context.Background(), "/project", path, []operatorAttention{generic, gate}, p, time.Hour, time.Now(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || sum.Selected != 1 || sum.Delivered != 1 || sum.Failed != 0 || atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("res=%v sum=%+v calls=%d", res, sum, calls)
	}
	p.Events = []string{"local_input_blocked"}
	_, sum, err = deliverNotificationSinksPersisted(context.Background(), "/project", path, []operatorAttention{gate}, p, time.Hour, time.Now(), true)
	if err != nil || sum.Selected != 0 {
		t.Fatalf("policy filter sum=%+v err=%v", sum, err)
	}
}

func (s deliveryFakeSink) ID() string                                     { return s.id }
func (s deliveryFakeSink) Deliver(context.Context, attention.Event) error { *s.calls++; return s.err }

func TestDeliveryPerSinkRetryIdempotencyAndForce(t *testing.T) {
	old := notificationSinkFactory
	defer func() { notificationSinkFactory = old }()
	calls := map[string]*int{"ok": new(int), "bad": new(int)}
	notificationSinkFactory = func(c team.OperatorNotificationSinkConfig) notifier.Sink {
		err := error(nil)
		if c.ID == "bad" {
			err = errors.New("down")
		}
		return deliveryFakeSink{c.ID, err, calls[c.ID]}
	}
	p := team.OperatorNotificationPolicy{Enabled: true, Sinks: []team.OperatorNotificationSinkConfig{{ID: "ok", Type: "desktop"}, {ID: "bad", Type: "desktop"}}}
	item := operatorAttention{EventType: "gate", Key: "default/s\x00gate\x00gate/x", LatestID: "m1", Profile: "default", Session: "s", Thread: "gate/x", Escalation: "initial"}
	now := time.Now()
	res, st := deliverNotificationSinks(context.Background(), "/project", []operatorAttention{item}, p, notifyStateFile{Schema: 2, Items: map[string]notifyStateRecord{}}, time.Hour, now, false)
	if len(res) != 2 || *calls["ok"] != 1 || *calls["bad"] != 1 {
		t.Fatal(res, *calls["ok"], *calls["bad"])
	}
	res, st = deliverNotificationSinks(context.Background(), "/project", []operatorAttention{item}, p, st, time.Hour, now.Add(time.Minute), false)
	if len(res) != 1 || res[0].SinkID != "bad" || *calls["ok"] != 1 || *calls["bad"] != 2 {
		t.Fatal(res)
	}
	res, _ = deliverNotificationSinks(context.Background(), "/project", []operatorAttention{item}, p, st, time.Hour, now.Add(2*time.Minute), true)
	if len(res) != 2 || *calls["ok"] != 2 {
		t.Fatal(res)
	}
}

func TestNotifyFlagDeliveryGuards(t *testing.T) {
	if err := runNotify([]string{"--force-resend"}); err == nil || !strings.Contains(err.Error(), "requires --deliver") {
		t.Fatal(err)
	}
	if err := runNotify([]string{"--dry-run", "--deliver"}); err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatal(err)
	}
}

func TestNotifySchema1MigrationQualifiesKeyAndSeedsSurfaceOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	body := `{"schema":1,"items":{"default/s\u0000gate/x":{"latest_id":"m1","last_notified":"2026-07-10T00:00:00Z","last_escalation":"initial"}}}`
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	st, err := readNotifyState(path)
	if err != nil {
		t.Fatal(err)
	}
	rec, ok := st.Items["default/s\x00gate\x00gate/x"]
	if !ok || st.Schema != 2 {
		t.Fatalf("%+v", st)
	}
	if len(rec.Deliveries) != 1 || rec.Deliveries["surface:notify"].Fingerprint != "m1" {
		t.Fatalf("%+v", rec.Deliveries)
	}
	if _, ok := rec.Deliveries["desktop"]; ok {
		t.Fatal("migration claimed desktop success")
	}
}
func TestNotifyStateRejectsUnknownSchemas(t *testing.T) {
	for _, schema := range []int{0, 3, 99} {
		path := filepath.Join(t.TempDir(), fmt.Sprintf("s%d.json", schema))
		if err := os.WriteFile(path, []byte(fmt.Sprintf(`{"schema":%d,"items":{}}`, schema)), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := readNotifyState(path); err == nil || !strings.Contains(err.Error(), "unsupported schema") {
			t.Fatalf("schema %d err=%v", schema, err)
		}
	}
}
