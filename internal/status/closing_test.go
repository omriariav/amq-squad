package status

import (
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// A seat the scheduler could not close shows in the attention queue and
// in live_seats, so the operator sees what holds the cap.
func TestClosingSeatIsAttention(t *testing.T) {
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	st := Build(Input{Now: now, Scheduler: scheduler.State{UpdatedAt: now, MaxSeats: 1, LiveSeats: 2,
		Seats: []scheduler.SeatView{{Handle: "seat-1"}, {Handle: "seat-2", Pool: task.Pool{Runtime: "claude"}, Closing: "seat_cap"}}}})
	if len(st.Attention) != 1 || st.Attention[0].ID != "seat:seat-2:closing" || st.Attention[0].Severity != "high" || st.Scheduler.LiveSeats != 2 {
		t.Fatalf("attention %+v scheduler %+v", st.Attention, st.Scheduler)
	}
}
