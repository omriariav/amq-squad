package scheduler

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Brief 31-1's second PR, row 14 (the scheduler's half): the launch
// budget's second mark. A seat this incarnation launched is asked for its
// first AMQ message since its launch request each tick until it has one;
// seat_first_message is logged once, its reason the message's own created
// stamp. An adopted seat (no launch of this incarnation) is never asked.
func TestFirstMessageOfALaunchedSeatIsOneDecision(t *testing.T) {
	sent := map[string]time.Time{}
	var asked []string
	var since time.Time
	cfg := Config{MaxSeats: 2, FirstMessage: func(h string, s time.Time) (time.Time, bool) {
		asked, since = append(asked, h), s
		at, ok := sent[h]
		return at, ok
	}}
	h, fr := newRosterHarness(t, cfg, rosterAB...)
	launchedAt := h.clk.Now()
	if ds := h.tick(); len(fr.launches) != 1 || count(ds, KindSeatFirstMessage) != 0 {
		t.Fatalf("the launch tick: %+v", ds)
	}
	h.clk.Add(time.Second)
	if ds := h.tick(); count(ds, KindSeatFirstMessage) != 0 || len(asked) == 0 || !since.Equal(launchedAt) {
		t.Fatalf("no message yet: %+v (asked %v since %v, launched %v)", ds, asked, since, launchedAt)
	}
	at := launchedAt.Add(57 * time.Second)
	sent["s-a"] = at
	h.clk.Add(time.Minute)
	ds := h.tick()
	if count(ds, KindSeatFirstMessage) != 1 || !has(ds, KindSeatFirstMessage, "", "s-a") {
		t.Fatalf("the first message: %+v", ds)
	}
	for _, d := range ds {
		if d.Kind == KindSeatFirstMessage && d.Reason != at.UTC().Format(time.RFC3339Nano) {
			t.Fatalf("reason %q, want the message's created stamp %s", d.Reason, at)
		}
	}
	asked = nil
	if ds := h.tick(); count(ds, KindSeatFirstMessage) != 0 {
		t.Fatalf("logged twice: %+v", ds)
	}
	for _, a := range asked {
		if a == "s-a" {
			t.Fatal("s-a is asked again after its first message")
		}
	}

	h2, _ := newRosterHarness(t, cfg, rosterAB...)
	h2.s.AdoptUp("s-a", sonnet, h2.clk.Now())
	h2.s.AdoptUp("s-b", gpt, h2.clk.Now())
	asked = nil
	sent["s-b"] = h2.clk.Now()
	if ds := h2.tick(); count(ds, KindSeatFirstMessage) != 0 || len(asked) != 0 {
		t.Fatalf("an adopted seat got a first-message mark: %+v (asked %v)", ds, asked)
	}
}

// OutboxFirstMessage reads the seat's outbox: the earliest message created
// at or after since, skipping older ones.
func TestOutboxFirstMessageReadsTheSeatsSentMessages(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "agents", "dev", "outbox", "sent")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	since := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	write := func(name string, created time.Time) {
		body := "---json\n{\"id\":\"" + name + "\",\"from\":\"dev\",\"created\":\"" + created.Format(time.RFC3339Nano) + "\"}\n---\nhello\n"
		p := filepath.Join(dir, name+".md")
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, created, created); err != nil { // AMQ writes the file when it sends
			t.Fatal(err)
		}
	}
	find := OutboxFirstMessage(root)
	if _, ok := find("dev", since); ok {
		t.Fatal("a first message in an empty outbox")
	}
	write("old", since.Add(-time.Hour))
	write("second", since.Add(90*time.Second))
	write("first", since.Add(57*time.Second))
	got, ok := find("dev", since)
	if !ok || !got.Equal(since.Add(57*time.Second)) {
		t.Fatalf("first message %v %v", got, ok)
	}
	if _, ok := find("qa", since); ok {
		t.Fatal("a seat without an outbox has a first message")
	}
}
