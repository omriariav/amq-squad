package gateverify

import (
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/gatestore"
)

// TestTheCoreBindsAnUnthreadedBareAnswerOnlyThroughItsHook proves the core
// lends no channel-wide authority of its own: only a timely, unthreaded bare
// answer, before any re-ask, may be accepted through the handler's hook.
func TestTheCoreBindsAnUnthreadedBareAnswerOnlyThroughItsHook(t *testing.T) {
	t0 := testNow.Add(time.Minute)
	type row struct {
		id  string
		run func(*testing.T, *fixture, gatestore.Record, gatestore.Question, *[]string)
	}
	rows := []row{
		{"C1 no hook", func(t *testing.T, f *fixture, _ gatestore.Record, _ gatestore.Question, asked *[]string) {
			r := f.core.HandleEvent(ctx, f.answer(f.owner, "yes", "", t0))
			if r.Outcome != "reasked" || r.Code != RejectThread || len(*asked) != 0 {
				t.Fatalf("result %+v asked %v", r, *asked)
			}
		}},
		{"C2 hook false", func(t *testing.T, f *fixture, _ gatestore.Record, q gatestore.Question, asked *[]string) {
			f.core.Unthreaded = recordingHook(asked, false)
			r := f.core.HandleEvent(ctx, f.answer(f.owner, "yes", "", t0))
			if r.Outcome != "reasked" || r.Code != RejectThread || !equalStrings(*asked, []string{q.EventID}) {
				t.Fatalf("result %+v asked %v", r, *asked)
			}
		}},
		{"C3 older answer", func(t *testing.T, f *fixture, _ gatestore.Record, _ gatestore.Question, asked *[]string) {
			f.core.Unthreaded = recordingHook(asked, true)
			r := f.core.HandleEvent(ctx, f.answer(f.owner, "yes", "", t0.Add(-time.Second)))
			if r.Outcome != "reasked" || len(*asked) != 0 {
				t.Fatalf("result %+v asked %v", r, *asked)
			}
		}},
		{"C4 equal seconds", func(t *testing.T, f *fixture, _ gatestore.Record, q gatestore.Question, asked *[]string) {
			f.core.Unthreaded = recordingHook(asked, true)
			r := f.core.HandleEvent(ctx, f.answer(f.owner, "yes", "", t0))
			ra, _ := f.store.ReadReask(q.GateID)
			if r.Outcome != "approved" || !equalStrings(*asked, []string{q.EventID}) || ra != nil {
				t.Fatalf("result %+v asked %v reask %+v", r, *asked, ra)
			}
		}},
		{"C5 typed tuple", func(t *testing.T, f *fixture, rec gatestore.Record, _ gatestore.Question, asked *[]string) {
			f.core.Unthreaded = recordingHook(asked, true)
			r := f.core.HandleEvent(ctx, f.answer(f.owner, tuple("approve", rec), "", t0))
			if r.Outcome != "reasked" || len(*asked) != 0 {
				t.Fatalf("result %+v asked %v", r, *asked)
			}
		}},
		{"C6 reasked gate", func(t *testing.T, f *fixture, _ gatestore.Record, _ gatestore.Question, asked *[]string) {
			first := f.core.HandleEvent(ctx, f.answer(f.owner, "yes", "", t0))
			f.clock = t0.Add(time.Second)
			f.core.Unthreaded = recordingHook(asked, true)
			r := f.core.HandleEvent(ctx, f.answer(f.owner, "no", "", f.clock))
			if first.Outcome != "reasked" || r.Outcome != "rejected" || r.Code != RejectThread || len(*asked) != 0 {
				t.Fatalf("first %+v result %+v asked %v", first, r, *asked)
			}
		}},
		{"C7 wrong thread", func(t *testing.T, f *fixture, _ gatestore.Record, _ gatestore.Question, asked *[]string) {
			f.core.Unthreaded = recordingHook(asked, true)
			r := f.core.HandleEvent(ctx, f.answer(f.owner, "yes", strings.Repeat("ab", 32), t0))
			if r.Outcome != "rejected" || r.Code != RejectWrongThread || len(*asked) != 0 {
				t.Fatalf("result %+v asked %v", r, *asked)
			}
		}},
		{"C8 future answer", func(t *testing.T, f *fixture, _ gatestore.Record, _ gatestore.Question, asked *[]string) {
			f.core.Unthreaded = recordingHook(asked, true)
			r := f.core.HandleEvent(ctx, f.answer(f.owner, "yes", "", t0.Add(5*time.Second)))
			if r.Outcome != "reasked" || len(*asked) != 0 {
				t.Fatalf("result %+v asked %v", r, *asked)
			}
		}},
	}
	for _, tc := range rows {
		t.Run(tc.id, func(t *testing.T) {
			f := newFixture(t)
			f.clock = t0
			rec, q := f.raise(strings.ToLower(strings.Fields(tc.id)[0]), "tag", "git tag v3.3.0")
			var asked []string
			tc.run(t, f, rec, q, &asked)
		})
	}
}

// recordingHook records the question event presented by the core and returns
// the channel-wide decision chosen by the row.
func recordingHook(asked *[]string, bind bool) func(gatestore.Question) bool {
	return func(q gatestore.Question) bool {
		*asked = append(*asked, q.EventID)
		return bind
	}
}

// equalStrings compares the short ordered event-id lists used by the hook
// assertions without obscuring failures behind a generic reflection helper.
func equalStrings(a, b []string) bool {
	return strings.Join(a, "\x00") == strings.Join(b, "\x00")
}
