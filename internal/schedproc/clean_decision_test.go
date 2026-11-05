package schedproc

import (
	"errors"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/task"
)

type cleanDecisionLiveness struct {
	gone bool
	err  error
}

func (l cleanDecisionLiveness) SeatProcess(string, string) (bool, string, error) {
	return l.gone, "process exited", l.err
}

type cleanDecisionPreparer struct{ calls []string }

func (*cleanDecisionPreparer) PrepareSeat(string) error { return nil }
func (p *cleanDecisionPreparer) CleanStopped(handle string) (bool, error) {
	p.calls = append(p.calls, handle)
	return true, nil
}

func TestManualCleanRequiresAProvedStoppedSeat(t *testing.T) {
	store := task.Store{Dir: t.TempDir()}
	p := &cleanDecisionPreparer{}
	live := manualCleanDecision(store, cleanDecisionLiveness{}, p)("lead", CleanRequest{Seat: "dev"})
	if live.OK || live.Code != "seat_running" || !strings.Contains(live.Detail, "seat dev is still running; stop it first") || len(p.calls) != 0 {
		t.Fatalf("live seat clean: %+v calls=%v", live, p.calls)
	}
	unknown := manualCleanDecision(store, cleanDecisionLiveness{err: errors.New("proof unreadable")}, p)("lead", CleanRequest{Seat: "dev"})
	if unknown.OK || unknown.Code != "seat_liveness_unknown" || len(p.calls) != 0 {
		t.Fatalf("unknown seat clean: %+v calls=%v", unknown, p.calls)
	}
	stopped := manualCleanDecision(store, cleanDecisionLiveness{gone: true}, p)("lead", CleanRequest{Seat: "dev"})
	if !stopped.OK || len(p.calls) != 1 || p.calls[0] != "dev" {
		t.Fatalf("stopped seat clean: %+v calls=%v", stopped, p.calls)
	}
}
