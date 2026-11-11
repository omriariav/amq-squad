package buzz

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/nostr"
)

// runBudget is the one deadline of a run that waits on the serve loop. It
// exists only to end a hung run: no step has a bound of its own, and how
// long a step took never fails a test. The dry run takes about 30 s on an
// idle laptop; the budget leaves room for a loaded shared runner and stays
// well inside make ci's 20 minute go test timeout.
const runBudget = 5 * time.Minute

const evidenceRunBudgetEnv = "AMQ_SQUAD_EVIDENCE_RUN_BUDGET"
const evidencePresenceWaitEnv = "AMQ_SQUAD_EVIDENCE_PRESENCE_WAIT"
const evidencePollIntervalEnv = "AMQ_SQUAD_EVIDENCE_POLL_INTERVAL"

// effectiveRunBudget permits count-table runs to fail a reproduced hang
// sooner. It is evidence-only: ordinary tests and gates leave the variable
// unset and retain five minutes. A requested bound can only shorten the
// default and never falls below three passing-run lengths.
func effectiveRunBudget(t *testing.T) time.Duration {
	t.Helper()
	raw := os.Getenv(evidenceRunBudgetEnv)
	if raw == "" {
		return runBudget
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		t.Fatalf("%s: %v", evidenceRunBudgetEnv, err)
	}
	if d < 2*time.Minute {
		return 2 * time.Minute
	}
	if d > runBudget {
		return runBudget
	}
	return d
}

// budget returns the run's context: it ends when the test ends or the
// budget runs out, whichever is first.
func budget(t *testing.T) context.Context {
	t.Helper()
	limit := effectiveRunBudget(t)
	ctx, cancel := context.WithTimeoutCause(t.Context(), limit, fmt.Errorf("the run's %s budget ran out", limit))
	t.Cleanup(cancel)
	return ctx
}

func TestEvidenceRunBudgetIsBoundedAndDefaultsToFiveMinutes(t *testing.T) {
	t.Setenv(evidenceRunBudgetEnv, "")
	if got := effectiveRunBudget(t); got != 5*time.Minute {
		t.Fatalf("unset budget = %s", got)
	}
	t.Setenv(evidenceRunBudgetEnv, "1s")
	if got := effectiveRunBudget(t); got != 2*time.Minute {
		t.Fatalf("short budget = %s", got)
	}
	t.Setenv(evidenceRunBudgetEnv, "130s")
	if got := effectiveRunBudget(t); got != 130*time.Second {
		t.Fatalf("evidence budget = %s", got)
	}
	t.Setenv(evidenceRunBudgetEnv, "10m")
	if got := effectiveRunBudget(t); got != 5*time.Minute {
		t.Fatalf("long budget = %s", got)
	}
}

func evidencePresenceWait() bool { return os.Getenv(evidencePresenceWaitEnv) == "1" }

func TestPresenceWaitEvidenceIsOffByDefault(t *testing.T) {
	t.Setenv(evidencePresenceWaitEnv, "")
	if evidencePresenceWait() {
		t.Fatal("presence-wait evidence row is on without its variable")
	}
	t.Setenv(evidencePresenceWaitEnv, "1")
	if !evidencePresenceWait() {
		t.Fatal("presence-wait evidence row did not turn on explicitly")
	}
}

func evidencePollInterval() string { return os.Getenv(evidencePollIntervalEnv) }

func TestPollIntervalEvidenceIsOffByDefault(t *testing.T) {
	t.Setenv(evidencePollIntervalEnv, "")
	if got := evidencePollInterval(); got != "" {
		t.Fatalf("unset poll interval = %q", got)
	}
	t.Setenv(evidencePollIntervalEnv, "5ms")
	if got := evidencePollInterval(); got != "5ms" {
		t.Fatalf("explicit poll interval = %q", got)
	}
}

// bell wakes waiters when what it observes changes. The zero value is
// ready to use.
type bell struct {
	mu sync.Mutex
	ch chan struct{}
}

// next returns a channel that closes at the next ring.
func (b *bell) next() <-chan struct{} {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.ch == nil {
		b.ch = make(chan struct{})
	}
	return b.ch
}

func (b *bell) ring() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.ch != nil {
		close(b.ch)
		b.ch = nil
	}
}

// await blocks until ok holds. It checks ok once, then again at every ring
// of b: the fake relay rings on every event it stores and every connection
// it opens, signs in or closes, so each check follows something the serve
// loop (or the attach loop, whose output rings the same bell) emitted.
// There is no polling deadline: only ctx, the run's one budget, ends a step
// that never comes, and the failure names that step.
func await(ctx context.Context, t *testing.T, b *bell, step string, ok func() bool) {
	t.Helper()
	for {
		rung := b.next() // armed before the check: a ring after it is not lost
		if ok() {
			return
		}
		select {
		case <-rung:
		case <-ctx.Done():
			t.Fatalf("hung: no signal for %q (%v)", step, context.Cause(ctx))
		}
	}
}

type ownerReject int

const (
	ownerNotRejected ownerReject = iota
	ownerRejectedByPlant
	ownerRejectedForReal
)

// sendOwnerUntilDone sends one owner event and observes only state changes on
// the relay bell. A rejection tied to that event causes one new event. The
// deterministic test plant does not consume the one real resend; a second
// real rejection fails instead of creating an unbounded retry loop.
func sendOwnerUntilDone(
	ctx context.Context,
	b *bell,
	kind, step string,
	send func() string,
	done func(string) bool,
	rejected func(string) ownerReject,
	fail func(string),
	logLine func(string),
) (string, int) {
	id := send()
	realResends := 0
	for {
		rung := b.next()
		if done(id) {
			return id, realResends
		}
		rejection := rejected(id)
		if rejection != ownerNotRejected {
			if rejection == ownerRejectedForReal {
				realResends++
				if realResends > 1 {
					fail(kind + " rejected (native_error) twice; not sent again")
					return id, realResends - 1
				}
			}
			logLine(kind + " rejected (native_error), sent again")
			next := send()
			if next == id {
				fail(kind + " sent again with the same event id")
				return id, realResends
			}
			id = next
			continue
		}
		select {
		case <-rung:
		case <-ctx.Done():
			fail(fmt.Sprintf("hung: no signal for %q (%v)", step, context.Cause(ctx)))
			return id, realResends
		}
	}
}

func TestSendOwnerUntilDone(t *testing.T) {
	t.Run("done at once", func(t *testing.T) {
		sends := 0
		id, resends := sendOwnerUntilDone(t.Context(), &bell{}, "answer", "done",
			func() string { sends++; return fmt.Sprintf("event-%d", sends) },
			func(string) bool { return true },
			func(string) ownerReject { return ownerNotRejected },
			func(msg string) { t.Fatalf("unexpected failure: %s", msg) },
			func(line string) { t.Fatalf("unexpected log: %s", line) })
		if id != "event-1" || sends != 1 || resends != 0 {
			t.Fatalf("id=%q sends=%d real resends=%d", id, sends, resends)
		}
	})

	t.Run("rejected once then done", func(t *testing.T) {
		sends := 0
		var logs []string
		id, resends := sendOwnerUntilDone(t.Context(), &bell{}, "answer", "done",
			func() string { sends++; return fmt.Sprintf("event-%d", sends) },
			func(id string) bool { return id == "event-2" },
			func(id string) ownerReject {
				if id == "event-1" {
					return ownerRejectedForReal
				}
				return ownerNotRejected
			},
			func(msg string) { t.Fatalf("unexpected failure: %s", msg) },
			func(line string) { logs = append(logs, line) })
		if id != "event-2" || sends != 2 || resends != 1 || !slices.Equal(logs, []string{"answer rejected (native_error), sent again"}) {
			t.Fatalf("id=%q sends=%d real resends=%d logs=%v", id, sends, resends, logs)
		}
	})

	t.Run("rejected twice", func(t *testing.T) {
		sends := 0
		var failures, logs []string
		_, resends := sendOwnerUntilDone(t.Context(), &bell{}, "answer", "done",
			func() string { sends++; return fmt.Sprintf("event-%d", sends) },
			func(id string) bool { return id == "event-3" },
			func(id string) ownerReject {
				if id == "event-1" || id == "event-2" {
					return ownerRejectedForReal
				}
				return ownerNotRejected
			},
			func(msg string) { failures = append(failures, msg) },
			func(line string) { logs = append(logs, line) })
		if sends != 2 || resends != 1 || !slices.Equal(failures, []string{"answer rejected (native_error) twice; not sent again"}) || len(logs) != 1 {
			t.Fatalf("sends=%d real resends=%d failures=%v logs=%v", sends, resends, failures, logs)
		}
	})

	t.Run("same id after rejection", func(t *testing.T) {
		sends := 0
		var failures []string
		_, _ = sendOwnerUntilDone(t.Context(), &bell{}, "answer", "done",
			func() string { sends++; return "same-event" },
			func(string) bool { return sends >= 2 },
			func(string) ownerReject { return ownerRejectedForReal },
			func(msg string) { failures = append(failures, msg) },
			func(string) {})
		if sends != 2 || !slices.Equal(failures, []string{"answer sent again with the same event id"}) {
			t.Fatalf("sends=%d failures=%v", sends, failures)
		}
	})

	t.Run("plant then one real rejection then done", func(t *testing.T) {
		sends := 0
		var failures, logs []string
		id, resends := sendOwnerUntilDone(t.Context(), &bell{}, "answer", "done",
			func() string { sends++; return fmt.Sprintf("event-%d", sends) },
			func(id string) bool { return id == "event-3" },
			func(id string) ownerReject {
				switch id {
				case "event-1":
					return ownerRejectedByPlant
				case "event-2":
					return ownerRejectedForReal
				default:
					return ownerNotRejected
				}
			},
			func(msg string) { failures = append(failures, msg) },
			func(line string) { logs = append(logs, line) })
		if id != "event-3" || sends != 3 || resends != 1 || len(failures) != 0 || len(logs) != 2 {
			t.Fatalf("id=%q sends=%d real resends=%d failures=%v logs=%v", id, sends, resends, failures, logs)
		}
	})

	t.Run("plant then two real rejections", func(t *testing.T) {
		sends := 0
		var failures, logs []string
		_, resends := sendOwnerUntilDone(t.Context(), &bell{}, "answer", "done",
			func() string { sends++; return fmt.Sprintf("event-%d", sends) },
			func(id string) bool { return id == "event-4" },
			func(id string) ownerReject {
				if id == "event-1" {
					return ownerRejectedByPlant
				}
				return ownerRejectedForReal
			},
			func(msg string) { failures = append(failures, msg) },
			func(line string) { logs = append(logs, line) })
		if sends != 3 || resends != 1 || !slices.Equal(failures, []string{"answer rejected (native_error) twice; not sent again"}) || len(logs) != 2 {
			t.Fatalf("sends=%d real resends=%d failures=%v logs=%v", sends, resends, failures, logs)
		}
	})
}

func checkApprovedOutcomes(flow string, outcomes int, fail func(string)) {
	if outcomes != 1 {
		fail(fmt.Sprintf("%s recorded %d approved outcomes, want exactly one", flow, outcomes))
	}
}

// serveAdmitted reports whether serve posted the running row for the owner
// DM dmID. Serve's core sets a request running only on the mailbox
// adapter's admission, which returns after the prompt is in the attach
// inbox (amq-remote v0.116.2 internal/remote/core/transition.go
// causeAdmitted, mailbox/mailbox.go submitLocked), so the row is serve's
// own signal that the delivery is done. The row is the request's root (kind
// 9, an e tag naming dmID) or an edit of it (kind 40003).
func serveAdmitted(r *wsRelay, body, dmID string) bool {
	running := func(e nostr.Event) bool { return strings.HasPrefix(e.Content, "amq-squad: running") }
	var roots []string
	for _, e := range r.mem.events(9, body) {
		if slices.Contains(nostr.TagValues(e, "e"), dmID) {
			if running(e) {
				return true
			}
			roots = append(roots, e.ID)
		}
	}
	for _, e := range r.mem.events(40003, body) {
		if running(e) && slices.ContainsFunc(nostr.TagValues(e, "e"), func(id string) bool { return slices.Contains(roots, id) }) {
			return true
		}
	}
	return false
}

// serveRejected reports the terminal native_error row for dmID. The root row
// e-tags the owner event; an edit e-tags that root, exactly like
// serveAdmitted. No unrelated rejection can trigger a resend.
func serveRejected(r *wsRelay, body, dmID string) bool {
	_, ok := serveRejectedEvent(r, body, dmID)
	return ok
}

// serveRejectedEvent returns the exact terminal row as well as the verdict so
// the evidence-only polling matrix can record one real row's tags.
func serveRejectedEvent(r *wsRelay, body, dmID string) (nostr.Event, bool) {
	const rejected = "amq-squad: rejected (native_error)"
	var roots []string
	for _, e := range r.mem.events(9, body) {
		if !slices.Contains(nostr.TagValues(e, "e"), dmID) {
			continue
		}
		if e.Content == rejected {
			return e, true
		}
		roots = append(roots, e.ID)
	}
	for _, e := range r.mem.events(40003, body) {
		if e.Content == rejected && slices.ContainsFunc(nostr.TagValues(e, "e"), func(id string) bool { return slices.Contains(roots, id) }) {
			return e, true
		}
	}
	return nostr.Event{}, false
}

// serveAnswered reports whether the body posted any row in reply to the
// owner DM dmID (serve's own answer to one of its commands).
func serveAnswered(r *wsRelay, body, dmID string) bool {
	for _, e := range r.mem.events(9, body) {
		if slices.Contains(nostr.TagValues(e, "e"), dmID) {
			return true
		}
	}
	return false
}
