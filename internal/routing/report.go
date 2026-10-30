package routing

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/task"
)

// The weekly report (6.4, R28): per class, used tier and size band, the
// redo-at-higher-tier rate, the cost with redo against one tier up
// (dominated cost), wait time with the reasons behind it, and review findings
// traced to smol outputs; one proposed table change per bucket, printed for
// the owner and never applied. Only runtimes with complete evidence count
// (16.8): a runtime with any row in the window that lacks tokens or cost is
// left out, with the reason printed.

// Proposal thresholds (proposals under lead ruling 14; adjust from the log).
const (
	RedoRateRaise     = 0.3              // at or above: raise the bucket one tier
	MinTasksForChange = 3                // fewer tasks: no change proposed
	MinTasksForDown   = 5                // a tier down needs this many clean tasks
	LongWait          = 10 * time.Minute // a mean wait above this proposes a chain change
)

// Bucket is one report bucket.
type Bucket struct {
	Class string `json:"class"`
	Tier  string `json:"tier"`
	Band  string `json:"size_band"`
}

func (b Bucket) String() string { return b.Class + " " + b.Tier + " band " + b.Band }

// WaitReason is the wait attributed to one reason in a bucket.
type WaitReason struct {
	Reason string `json:"reason"`
	Rows   int    `json:"rows"`
	WaitMS int64  `json:"wait_ms"`
}

// BucketStats is one bucket's line of the report.
type BucketStats struct {
	Bucket
	Attempts     int          `json:"attempts"`
	Tasks        int          `json:"tasks"`
	Redone       int          `json:"redone_higher"`
	Passed       int          `json:"verifier_passed"` // tasks with a completion that passed its mechanical verifier here
	RedoRate     float64      `json:"redo_rate"`
	CostUSD      *float64     `json:"cost_per_task_usd"`    // mean cost of a completed task that started here, redo included
	UpCostUSD    *float64     `json:"cost_one_tier_up_usd"` // the same for one tier up, same class and band
	Dominated    bool         `json:"dominated"`
	MeanWaitMS   int64        `json:"mean_wait_ms"`
	MaxWaitMS    int64        `json:"max_wait_ms"`
	Waits        []WaitReason `json:"waits"`
	SmolFindings int          `json:"review_findings_traced"` // findings of reviews that consumed this bucket's smol outputs
	Proposal     string       `json:"proposal"`
}

// Excluded is a runtime left out for incomplete evidence.
type Excluded struct {
	Runtime    string `json:"runtime"`
	Rows       int    `json:"rows"`
	Incomplete int    `json:"incomplete_rows"`
}

// Report is the weekly report.
type Report struct {
	Since    time.Time  `json:"since"`
	Until    time.Time  `json:"until"`
	Rows     int        `json:"rows_in_window"`
	Used     int        `json:"rows_used"`
	Excluded []Excluded `json:"excluded_runtimes"`
	Unpriced []string   `json:"unpriced_entries"` // entries with rows but no owner price: their cost is unknown
	// UsageInvalid counts rows in the window whose usage is unknown because a
	// token counter was not a count (negative, fractional, out of range).
	UsageInvalid int `json:"usage_invalid_rows"`
	// HistoryTampered counts rows left out because their tag failed under
	// the session seed; Unverified says no seed was there to check them.
	HistoryTampered int           `json:"history_tampered"`
	Unverified      bool          `json:"unverified"`
	Buckets         []BucketStats `json:"buckets"`
}

// Weekly builds the report over the rows that ended in the seven days
// before now, with tbl for the route-down rule of the proposals.
func Weekly(rows []Row, now time.Time, tbl Table) Report {
	rep := Report{Since: now.Add(-7 * 24 * time.Hour), Until: now, Excluded: []Excluded{}, Unpriced: []string{}, Buckets: []BucketStats{}}
	var win []Row
	perRuntime := map[string]*Excluded{}
	for _, r := range rows {
		if r.EndedAt.Before(rep.Since) || !r.EndedAt.Before(now) {
			continue
		}
		win = append(win, r)
		e := perRuntime[r.Runtime]
		if e == nil {
			e = &Excluded{Runtime: r.Runtime}
			perRuntime[r.Runtime] = e
		}
		e.Rows++
		if !r.EvidenceComplete() {
			e.Incomplete++
		}
		if r.UsageInvalid != "" {
			rep.UsageInvalid++
		}
		// Unpriced entries come from every verified row of the window, before
		// the runtime filter (phase 5 re-pass, P2-6): known tokens with no
		// cost is a missing owner price.
		if r.CostUSD == nil && r.TokensIn != nil && r.TokensOut != nil && r.Entry != "" && !containsStr(rep.Unpriced, r.Entry) {
			rep.Unpriced = append(rep.Unpriced, r.Entry)
		}
	}
	rep.Rows = len(win)
	var used []Row
	for _, r := range win {
		if perRuntime[r.Runtime].Incomplete == 0 {
			used = append(used, r)
		}
	}
	for _, e := range perRuntime {
		if e.Incomplete > 0 {
			rep.Excluded = append(rep.Excluded, *e)
		}
	}
	sort.Slice(rep.Excluded, func(i, j int) bool { return rep.Excluded[i].Runtime < rep.Excluded[j].Runtime })
	rep.Used = len(used)

	byTask := map[string][]Row{}
	usedRow := map[string]bool{}
	for _, r := range used {
		byTask[r.Task] = append(byTask[r.Task], r)
		usedRow[key(r.Task, r.Attempt)] = true
	}
	// Whole task histories (phase 5 re-pass, P2-5): every verified row of a
	// task, in the window or before it, so an attempt left out (an excluded
	// runtime, an earlier week) never makes a later attempt the task's
	// first: redo and cost are attributed from the history.
	hist := map[string][]Row{}
	for _, r := range rows {
		hist[r.Task] = append(hist[r.Task], r)
	}
	for _, rs := range byTask {
		sort.Slice(rs, func(i, j int) bool { return rs[i].Attempt < rs[j].Attempt })
	}
	for _, rs := range hist {
		sort.Slice(rs, func(i, j int) bool { return rs[i].Attempt < rs[j].Attempt })
	}
	inWindow := func(r Row) bool { return !r.EndedAt.Before(rep.Since) && r.EndedAt.Before(now) }
	// excluded: the attempt's evidence does not count (its runtime is left out
	// of this window, or, before the window, the row itself is incomplete).
	excluded := func(r Row) bool {
		if inWindow(r) {
			return perRuntime[r.Runtime].Incomplete > 0
		}
		return !r.EvidenceComplete()
	}
	bucketOf := func(r Row) Bucket {
		return Bucket{Class: r.Class, Tier: r.UsedTier, Band: SizeBand(r.Features.DeclaredFiles)}
	}

	stats := map[Bucket]*BucketStats{}
	get := func(b Bucket) *BucketStats {
		s := stats[b]
		if s == nil {
			s = &BucketStats{Bucket: b, Waits: []WaitReason{}}
			stats[b] = s
		}
		return s
	}
	tasksIn := map[Bucket]map[string]bool{}
	redone := map[Bucket]map[string]bool{}
	passed := map[Bucket]map[string]bool{}
	waitBy := map[Bucket]map[string]*WaitReason{}
	var waitSum = map[Bucket]int64{}
	// cost per completed task, by the bucket of its first attempt
	costSum, costN := map[Bucket]float64{}, map[Bucket]int{}
	smolOut := map[string]Bucket{} // task -> bucket of its completed smol attempt
	for id, rs := range byTask {
		for _, r := range rs {
			b := bucketOf(r)
			s := get(b)
			s.Attempts++
			if tasksIn[b] == nil {
				tasksIn[b], redone[b], passed[b], waitBy[b] = map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]*WaitReason{}
			}
			tasksIn[b][id] = true
			if r.Outcome == task.OutcomeCompleted && r.VerifierResult == VerifierPass {
				passed[b][id] = true
			}
			if failure(r.Outcome) || r.VerifierResult == VerifierFail || r.VerifierResult == VerifierInvalid {
				for _, later := range hist[id] {
					if later.Attempt <= r.Attempt {
						continue
					}
					if TierRank(later.UsedTier) > TierRank(r.UsedTier) {
						redone[b][id] = true
						break
					}
				}
			}
			waitSum[b] += r.WaitMS
			if r.WaitMS > s.MaxWaitMS {
				s.MaxWaitMS = r.WaitMS
			}
			reasons := append([]string{}, r.WaitReasons...)
			for _, sk := range r.Skipped {
				if !strings.HasPrefix(sk.Reason, SkipUsedBefore) && !containsStr(reasons, sk.Reason) {
					reasons = append(reasons, sk.Reason)
				}
			}
			for _, reason := range reasons {
				w := waitBy[b][reason]
				if w == nil {
					w = &WaitReason{Reason: reason}
					waitBy[b][reason] = w
				}
				w.Rows++
				w.WaitMS += r.WaitMS
			}
			if r.Outcome == task.OutcomeCompleted && r.UsedTier == TierSmol {
				smolOut[key(id, r.Attempt)] = b
			}
		}
	}
	for _, h := range hist {
		// The cost of a task completed in this window, redo included, by the
		// bucket of its first attempt. Every attempt from the first to the
		// completion is required: one missing, excluded or unpriced makes the
		// task's cost unknown, never the cost of the attempts that remain.
		last := h[len(h)-1]
		if last.Outcome != task.OutcomeCompleted || !usedRow[key(last.Task, last.Attempt)] {
			continue
		}
		total, known := 0.0, true
		for i, r := range h {
			if r.Attempt != i+1 || r.CostUSD == nil || excluded(r) {
				known = false
				break
			}
			total += *r.CostUSD
		}
		if known {
			// The first attempt's bucket is instantiated here: one that ran
			// in an earlier week has no row of this window, and its cost must
			// still be reported (narrow pass, P2-5).
			first := bucketOf(h[0])
			get(first)
			costSum[first] += total
			costN[first]++
		}
	}
	for _, rs := range byTask {
		for _, r := range rs {
			if r.Class != "review" || r.ReviewFindings == nil || *r.ReviewFindings == 0 {
				continue
			}
			// The review's findings count once for each bucket whose smol
			// attempts it consumed, named by attempt id (lead ruling).
			traced := map[Bucket]bool{}
			for _, dep := range r.ConsumedSmolOutputs {
				if b, ok := smolOut[dep]; ok && !traced[b] {
					traced[b] = true
					get(b).SmolFindings += *r.ReviewFindings
				}
			}
		}
	}
	mean := func(b Bucket) *float64 {
		if costN[b] == 0 {
			return nil
		}
		v := costSum[b] / float64(costN[b])
		return &v
	}
	for b, s := range stats {
		s.Tasks = len(tasksIn[b])
		s.Redone = len(redone[b])
		s.Passed = len(passed[b])
		if s.Tasks > 0 {
			s.RedoRate = float64(s.Redone) / float64(s.Tasks)
		}
		if s.Attempts > 0 {
			s.MeanWaitMS = waitSum[b] / int64(s.Attempts)
		}
		for _, w := range waitBy[b] {
			s.Waits = append(s.Waits, *w)
		}
		sort.Slice(s.Waits, func(i, j int) bool {
			if s.Waits[i].WaitMS != s.Waits[j].WaitMS {
				return s.Waits[i].WaitMS > s.Waits[j].WaitMS
			}
			return s.Waits[i].Reason < s.Waits[j].Reason
		})
		s.CostUSD = mean(b)
		if u := up(b.Tier); u != b.Tier {
			s.UpCostUSD = mean(Bucket{Class: b.Class, Tier: u, Band: b.Band})
		}
		s.Dominated = s.CostUSD != nil && s.UpCostUSD != nil && *s.CostUSD >= *s.UpCostUSD
		s.Proposal = propose(*s, tbl)
		rep.Buckets = append(rep.Buckets, *s)
	}
	sort.Strings(rep.Unpriced)
	sort.Slice(rep.Buckets, func(i, j int) bool {
		a, b := rep.Buckets[i], rep.Buckets[j]
		if a.Class != b.Class {
			return classRank(a.Class) < classRank(b.Class)
		}
		if a.Tier != b.Tier {
			return TierRank(a.Tier) < TierRank(b.Tier)
		}
		return a.Band < b.Band
	})
	return rep
}

func classRank(c string) int {
	for i, k := range task.Classes {
		if k == c {
			return i
		}
	}
	return len(task.Classes)
}

func containsStr(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// propose is the one change a bucket proposes, in priority order: raise on
// redo, raise on dominated cost, move off smol on traced review findings,
// change the chain on long waits, try a tier down on a clean record, else no
// change.
func propose(s BucketStats, tbl Table) string {
	upTier := up(s.Tier)
	canRaise := upTier != s.Tier
	switch {
	case s.Tasks < MinTasksForChange:
		return fmt.Sprintf("no change: %d task(s), too few to judge", s.Tasks)
	case canRaise && s.RedoRate >= RedoRateRaise:
		return fmt.Sprintf("raise %s size band %s from %s to %s: %d of %d tasks redone at a higher tier", s.Class, s.Band, s.Tier, upTier, s.Redone, s.Tasks)
	case canRaise && s.Dominated:
		return fmt.Sprintf("raise %s size band %s from %s to %s: cost with redo %.4f USD a task against %.4f USD one tier up", s.Class, s.Band, s.Tier, upTier, *s.CostUSD, *s.UpCostUSD)
	case s.Tier == TierSmol && s.SmolFindings > 0:
		return fmt.Sprintf("raise %s size band %s from smol to task: %d review finding(s) traced to its smol outputs", s.Class, s.Band, s.SmolFindings)
	case time.Duration(s.MeanWaitMS)*time.Millisecond > LongWait && len(s.Waits) > 0:
		return fmt.Sprintf("change the %s chain: mean wait %s, mostly %s; add or reorder an entry that passes", s.Tier, (time.Duration(s.MeanWaitMS) * time.Millisecond).Round(time.Second), s.Waits[0].Reason)
	// A tier down needs clean tasks that passed their verifier: a bucket with no
	// redo but no success never earns it (Codex pass 1, P2-8).
	case s.Passed >= MinTasksForDown && s.Redone == 0 && s.SmolFindings == 0 && tbl.MayRouteDown(s.Class) && down(s.Tier) != "":
		return fmt.Sprintf("try %s size band %s on %s, one tier down: %d of %d tasks passed their verifier, no redo", s.Class, s.Band, down(s.Tier), s.Passed, s.Tasks)
	}
	return fmt.Sprintf("no change: %d tasks, no signal", s.Tasks)
}

// Print writes the report for the owner.
func (r Report) Print(w io.Writer) {
	fmt.Fprintf(w, "routing report %s to %s: %d rows in the window, %d used\n",
		r.Since.UTC().Format(time.DateOnly), r.Until.UTC().Format(time.DateOnly), r.Rows, r.Used)
	for _, e := range r.Excluded {
		fmt.Fprintf(w, "excluded runtime %s: %d of %d rows lack token evidence (plan 16.8)\n", e.Runtime, e.Incomplete, e.Rows)
	}
	if r.HistoryTampered > 0 {
		fmt.Fprintf(w, "%s: %d break(s) in the outcome history left out (a tag that fails, a gap, a duplicate)\n", HistoryTampered, r.HistoryTampered)
	}
	if r.Unverified {
		fmt.Fprintln(w, "rows not verified: the session has no seed file")
	}
	if r.UsageInvalid > 0 {
		fmt.Fprintf(w, "usage_invalid: %d row(s) with a token counter that is not a count (negative, fractional, out of range): usage unknown\n", r.UsageInvalid)
	}
	if len(r.Unpriced) > 0 {
		fmt.Fprintf(w, "entries with no owner price (cost unknown, no budget stop): %s\n", strings.Join(r.Unpriced, ", "))
	}
	if len(r.Buckets) == 0 {
		fmt.Fprintln(w, "no bucket: no row with complete evidence in the window")
		return
	}
	fmt.Fprintln(w)
	for _, b := range r.Buckets {
		fmt.Fprintf(w, "%s\n", b.Bucket)
		fmt.Fprintf(w, "  attempts %d, tasks %d, redone at a higher tier %d (%.0f%%)\n", b.Attempts, b.Tasks, b.Redone, 100*b.RedoRate)
		fmt.Fprintf(w, "  cost per task %s, one tier up %s%s\n", usd(b.CostUSD), usd(b.UpCostUSD), map[bool]string{true: ", dominated", false: ""}[b.Dominated])
		var waits []string
		for _, x := range b.Waits {
			waits = append(waits, fmt.Sprintf("%s %d", x.Reason, x.Rows))
		}
		if len(waits) == 0 {
			waits = []string{"none"}
		}
		fmt.Fprintf(w, "  wait mean %s, max %s; reasons: %s\n", ms(b.MeanWaitMS), ms(b.MaxWaitMS), strings.Join(waits, ", "))
		if b.Tier == TierSmol {
			fmt.Fprintf(w, "  review findings traced to its smol outputs: %d\n", b.SmolFindings)
		}
		fmt.Fprintf(w, "  proposal: %s\n", b.Proposal)
	}
	fmt.Fprintln(w, "\nproposals are for the owner; none is applied (plan 6.4, R28)")
}

func usd(v *float64) string {
	if v == nil {
		return "n/a"
	}
	return fmt.Sprintf("%.4f USD", *v)
}

func ms(v int64) string { return (time.Duration(v) * time.Millisecond).Round(time.Second).String() }
