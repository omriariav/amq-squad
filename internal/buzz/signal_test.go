package buzz

import (
	"context"
	"fmt"
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

// budget returns the run's context: it ends when the test ends or the
// budget runs out, whichever is first.
func budget(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeoutCause(t.Context(), runBudget, fmt.Errorf("the run's %s budget ran out", runBudget))
	t.Cleanup(cancel)
	return ctx
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
