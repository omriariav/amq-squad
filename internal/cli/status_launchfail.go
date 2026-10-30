package cli

import (
	"fmt"
	"sort"

	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/status"
)

// withLaunchFailures shows the scheduler's last launch failure of each pool
// (3.0.1 t17): the failed seat's row carries the failure's reason in place
// of its starting or no-signal reason, and the attention queue names the
// stage, AMQ's reason code and the (redacted) detail. A task that waits for
// the seat already carries the reason as its wait reason (the scheduler
// writes it in place of seat_starting). A row of a seat that is live is
// left as it is.
func withLaunchFailures(st status.Status, failures []scheduler.LaunchFailure) status.Status {
	if len(failures) == 0 {
		return st
	}
	for _, f := range failures {
		for i := range st.Seats {
			if st.Seats[i].Handle == f.Handle && !st.Seats[i].Live {
				st.Seats[i].ReasonCode = f.Code()
			}
		}
		sev := "medium"
		if f.Count >= 2 {
			sev = "high"
		}
		st.Attention = append(st.Attention, status.Attention{
			ID: "seat:" + f.Handle + ":launch_failed:" + f.Pool.Key(), Kind: scheduler.KindSeatLaunchFailed, Severity: sev, Since: f.At, Seat: f.Handle,
			Subject: truncateLine(fmt.Sprintf("seat %s (pool %s) did not launch: stage %s, reason %s, %d in a row: %s",
				orDash(f.Handle), f.Pool.Key(), f.Stage, f.Code(), f.Count, orDash(f.Detail)), 400),
			Bucket: status.BucketNeedsYou,
		})
	}
	rank := map[string]int{"high": 0, "medium": 1, "low": 2}
	sort.SliceStable(st.Attention, func(i, j int) bool {
		a, b := st.Attention[i], st.Attention[j]
		if rank[a.Severity] != rank[b.Severity] {
			return rank[a.Severity] < rank[b.Severity]
		}
		if !a.Since.Equal(b.Since) {
			return a.Since.Before(b.Since)
		}
		return a.ID < b.ID
	})
	return st
}

// truncateLine cuts s to at most n bytes on a rune boundary.
func truncateLine(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && s[n]&0xc0 == 0x80 {
		n--
	}
	return s[:n]
}
