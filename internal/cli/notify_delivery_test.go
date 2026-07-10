package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
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
	res, st := deliverNotificationSinks(context.Background(), []operatorAttention{item}, p, notifyStateFile{Schema: 2, Items: map[string]notifyStateRecord{}}, time.Hour, now, false)
	if len(res) != 2 || *calls["ok"] != 1 || *calls["bad"] != 1 {
		t.Fatal(res, *calls["ok"], *calls["bad"])
	}
	res, st = deliverNotificationSinks(context.Background(), []operatorAttention{item}, p, st, time.Hour, now.Add(time.Minute), false)
	if len(res) != 1 || res[0].SinkID != "bad" || *calls["ok"] != 1 || *calls["bad"] != 2 {
		t.Fatal(res)
	}
	res, _ = deliverNotificationSinks(context.Background(), []operatorAttention{item}, p, st, time.Hour, now.Add(2*time.Minute), true)
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
