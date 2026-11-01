package status

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/mergetrain"
	"github.com/omriariav/amq-squad/v3/internal/schedproc"
	"github.com/omriariav/amq-squad/v3/internal/seatlaunch"
)

// What the screen reads (brief 31-1, section 6): the lead and the scheduler
// process, the decisions tail, the merge train's runs, each seat's launch
// with the launch budget's two marks and the seat warnings that replaced
// the deleted refusals. Every read is lock-free: the records are written by
// rename or appended, and status never takes or probes a lock.

// collectProcesses reads the lead record and the scheduler's launch record.
func collectProcesses(o CollectOptions, in *Input, warn func(string, ...any)) {
	if rec, err := schedproc.ReadLeadRecord(o.Root); err == nil {
		l := &Lead{PID: rec.PID, Start: rec.Start, Pane: rec.Pane, RecordedAt: rec.At}
		if rec.PID > 0 && o.PIDAlive(rec.PID) {
			stamp, ok := o.StartToken(rec.PID)
			l.Live = ok && stamp == rec.Start
		}
		if rec.Pane != "" {
			l.Attach = "tmux attach -t " + rec.Pane
		}
		in.Lead = l
	} else if !os.IsNotExist(err) {
		warn("lead record: %v", err)
	}
	if lr, err := schedproc.ReadLaunchRecord(o.Root); err == nil {
		started := lr.StartedAt
		in.SchedulerProc = Scheduler{PID: lr.PID, StartedAt: &started, LaunchStart: lr.Start, Live: lr.PID > 0 && o.PIDAlive(lr.PID)}
		if in.Lead != nil {
			in.Lead.Handle = lr.Lead
		}
	} else if !os.IsNotExist(err) {
		warn("scheduler launch record: %v", err)
	}
}

// collectDecisions reads the decisions log's tail.
func collectDecisions(o CollectOptions, in *Input, warn func(string, ...any)) {
	lines, err := schedproc.ReadDecisionsTail(o.Root)
	if err != nil {
		warn("decisions log: %v", err)
	}
	for _, d := range lines {
		in.Decisions = append(in.Decisions, Decision{At: d.At, Kind: d.Kind, Seat: d.Seat, TaskID: d.TaskID, Reason: d.Reason, ReasonCode: d.ReasonCode, Stage: d.Stage})
	}
}

// collectTrain groups the merge train's audit log by run: the last two,
// newest first.
func collectTrain(o CollectOptions, in *Input, warn func(string, ...any)) {
	entries, err := mergetrain.AuditFor(o.Root).Read()
	if err != nil {
		warn("merge train audit log: %v", err)
	}
	var order []string
	runs := map[string]*TrainRun{}
	for _, e := range entries {
		at, _ := time.Parse(time.RFC3339Nano, e.At)
		r := runs[e.Run]
		if r == nil {
			r = &TrainRun{Run: e.Run, StartedAt: at, Repository: e.Repository, Base: e.Base, PRs: []int{}, Merged: []int{}}
			runs[e.Run] = r
			order = append(order, e.Run)
		}
		r.LastAt, r.LastKind = at, e.Kind
		r.DryRun = r.DryRun || e.DryRun
		if len(e.Pairs) > 0 && (e.Kind == "batch" || e.Kind == "plan") {
			r.PRs = r.PRs[:0]
			for _, p := range e.Pairs {
				r.PRs = append(r.PRs, p.Number)
			}
		}
		switch e.Kind {
		case "merge":
			if !slices.Contains(r.Merged, e.PR) {
				r.Merged = append(r.Merged, e.PR)
			}
		case "refusal":
			r.Refusal = strings.TrimSpace(fmt.Sprintf("refusal PR #%d %s: %s", e.PR, strings.Join(e.Rules, ","), e.Detail))
		case "stop":
			r.Refusal, r.Done = "stop: "+e.Detail, true
		case "plan":
			r.Done = true
		}
	}
	for i := len(order) - 1; i >= 0 && len(in.TrainRuns) < 2; i-- {
		r := runs[order[i]]
		if len(r.PRs) > 0 && len(r.Merged) >= len(r.PRs) {
			r.Done = true
		}
		in.TrainRuns = append(in.TrainRuns, *r)
	}
}

// seatLaunch is a seat's last launch from its records: the staged record
// (compile's started_at, the proof ticket's issued_at, AMQ's apply), its
// published record (seat-exec's outcome), the accepted proof (seat live),
// the admitted wake, and the launch budget against LaunchCeiling from the
// seat's own marks (its launch's start and its first message). A seat
// without marks of this launch (launched before them, or by a scheduler
// started by hand), or whose proof predates its start, has null marks and
// no ceiling check.
func seatLaunch(root, handle string, staged launch.Record) *SeatLaunch {
	sl := &SeatLaunch{LaunchID: staged.LaunchID, Outcome: "staged"}
	if staged.Apply != nil {
		sl.Outcome = "applied"
		if staged.Apply.Outcome != "applied" {
			sl.Outcome, sl.ReasonCode = "apply_failed", staged.Apply.ReasonCode
		} else if staged.Proof != nil && staged.Proof.Token == launch.TokenExpired {
			sl.Outcome, sl.ReasonCode = "proof_expired", "proof_window_expired"
		}
	}
	if pub, err := launch.Read(filepath.Join(root, "agents", handle)); err == nil && pub.LaunchID == staged.LaunchID && pub.Exec != nil && pub.Exec.Outcome == "refused" {
		sl.Outcome, sl.ReasonCode = "exec_refused", pub.Exec.ReasonCode
	}
	var live *time.Time
	if proof, err := launch.ReadProof(root, handle); err == nil && staged.LaunchID != "" && proof.LaunchID == staged.LaunchID && proof.PID > 0 {
		at := proof.At.UTC()
		live = &at
		sl.Outcome, sl.ReasonCode = "live", ""
	}
	if w, err := seatlaunch.ReadSeatWake(root, handle); err == nil && w.LaunchID == staged.LaunchID && w.Wake != nil {
		sl.Wake = &SeatWake{Mode: w.Wake.Mode, RequireWake: w.Wake.RequireWake, Injector: w.Wake.Injector, AuditReason: w.Wake.AuditReason}
	}
	var start, first, drainEnd *time.Time
	if m, err := seatlaunch.ReadSeatMarks(root, handle); err == nil && staged.LaunchID != "" && m.LaunchID == staged.LaunchID {
		start, first, drainEnd = m.Start, m.FirstMessage, m.DrainEnd
		sl.AddedAt = m.AddedAt
	}
	if start != nil && live != nil && live.Before(*start) {
		start = nil // a proof older than the start is not this start's launch (an adopted seat)
	}
	sl.Timing = launchTiming(staged, start, live, first, drainEnd)
	return sl
}

// launchTiming computes the two marks and the segments up to seat live.
// A seat a relaunch launched from the add key (brief 31-2 PR 2) has its
// own start, the key press, and a drain end: the segment from the press to
// the end of the relaunch's drain is "drain", ahead of prepare, so a busy
// squad's wait is named and never counted as seat_exec's or prepare's.
func launchTiming(staged launch.Record, start, live, first, drainEnd *time.Time) LaunchTiming {
	ms := func(a, b *time.Time) *int64 {
		if a == nil || b == nil || a.IsZero() || b.IsZero() {
			return nil
		}
		v := b.Sub(*a).Milliseconds()
		return &v
	}
	ptr := func(t time.Time) *time.Time {
		if t.IsZero() {
			return nil
		}
		t = t.UTC()
		return &t
	}
	compiled := ptr(staged.StartedAt)
	var issued, applied *time.Time
	if staged.Proof != nil {
		issued = ptr(staged.Proof.IssuedAt)
	}
	if staged.Apply != nil {
		applied = ptr(staged.Apply.At)
	}
	t := LaunchTiming{Start: start, SeatLive: live, FirstMessage: first, CeilingMS: LaunchCeiling.Milliseconds(),
		ToSeatLiveMS: ms(start, live), ToFirstMessageMS: ms(start, first),
		Segments: []LaunchSegment{
			{Name: "prepare", MS: ms(start, compiled)},
			{Name: "mint", MS: ms(compiled, issued)},
			{Name: "apply", MS: ms(issued, applied)},
			{Name: "seat_exec", MS: ms(applied, live)},
		}}
	if drainEnd != nil && start != nil && !drainEnd.Before(*start) {
		t.Segments = append([]LaunchSegment{{Name: "drain", MS: ms(start, drainEnd)}}, t.Segments...)
		t.Segments[1].MS = ms(drainEnd, compiled)
	}
	var slowest int64 = -1
	for _, s := range t.Segments {
		if s.MS != nil && *s.MS > slowest {
			slowest, t.SlowestSegment = *s.MS, s.Name
		}
	}
	t.OverCeiling = t.ToSeatLiveMS != nil && *t.ToSeatLiveMS > t.CeilingMS
	return t
}

// The warnings of brief 31-1's second PR (none is a refusal).
const (
	WarnLaunchBudget     = "launch_budget_exceeded"
	WarnWakeOff          = "wake_off"
	WarnStalledAtDialog  = "seat_stalled_at_dialog"
	WarnPiProviderNoKey  = "pi_provider_key_missing"
	wakeDialogMarkerName = ".wake.dialog" // AMQ's dialog marker (internal/cli/wake_dialog.go at v0.116.2)
)

// launchWarnings are the seat warnings from its launch, its mailbox and its
// pi provider: over the ceiling (naming the slowest segment up to seat
// live), the wake off, an open harness dialog, a pi model with no key.
func launchWarnings(root, handle string, staged launch.Record, sl *SeatLaunch, now time.Time) []string {
	var out []string
	if sl != nil && sl.Timing.OverCeiling {
		slow := "-"
		for _, s := range sl.Timing.Segments {
			if s.Name == sl.Timing.SlowestSegment && s.MS != nil {
				slow = fmt.Sprintf("%s %.3f s", s.Name, float64(*s.MS)/1000)
			}
		}
		out = append(out, fmt.Sprintf("%s: %.3f s from the start to seat live, over the %s ceiling; slowest segment %s", WarnLaunchBudget, float64(*sl.Timing.ToSeatLiveMS)/1000, LaunchCeiling, slow))
	}
	switch {
	case sl != nil && sl.Wake != nil && sl.Wake.Mode != "enabled":
		out = append(out, WarnWakeOff+": "+firstNonEmpty(sl.Wake.AuditReason, "AMQ admitted no wake")+"; the seat idles after each turn until it is rung by hand")
	case sl != nil && sl.Wake == nil && staged.Runtime == "pi":
		// A pi seat is compiled wake-off (Q6 of brief 31-1) whether or not
		// AMQ's preview echoed it.
		_, _, why := seatlaunch.SeatWake("pi", "")
		out = append(out, WarnWakeOff+": "+why+"; the seat idles after each turn until it is rung by hand")
	}
	if st, err := os.Stat(filepath.Join(root, "agents", handle, wakeDialogMarkerName)); err == nil {
		out = append(out, fmt.Sprintf("%s: a harness dialog has been open since %s (%s); the seat waits at it and AMQ holds its doorbell", WarnStalledAtDialog, st.ModTime().UTC().Format(time.RFC3339), now.Sub(st.ModTime()).Round(time.Second)))
	}
	if why := piKeyMissing(staged); why != "" {
		out = append(out, WarnPiProviderNoKey+": "+why)
	}
	return out
}

// piProviderKeys are the key variables pi reads for the common providers
// (@earendil-works/pi-ai dist/env-api-keys.js at pi 1.3.0). A provider
// that is not listed (ollama, a local server) needs no key here.
var piProviderKeys = map[string][]string{
	"anthropic":  {"ANTHROPIC_API_KEY", "ANTHROPIC_OAUTH_TOKEN", "ANTHROPIC_AUTH_TOKEN"},
	"openai":     {"OPENAI_API_KEY"},
	"google":     {"GEMINI_API_KEY"},
	"groq":       {"GROQ_API_KEY"},
	"xai":        {"XAI_API_KEY"},
	"openrouter": {"OPENROUTER_API_KEY"},
	"mistral":    {"MISTRAL_API_KEY"},
	"deepseek":   {"DEEPSEEK_API_KEY"},
	"cerebras":   {"CEREBRAS_API_KEY"},
	"fireworks":  {"FIREWORKS_API_KEY"},
	"together":   {"TOGETHER_API_KEY"},
}

// piKeyMissing names a pi seat whose PROVIDER/ID model has no key: none of
// the provider's key variables is among the record's env or credential
// needs (seat-exec scrubs every other credential), and the seat's pi agent
// dir holds no stored login for it (auth.json; its values are never read
// out). Such a seat goes live, then stalls at its first turn.
func piKeyMissing(rec launch.Record) string {
	if rec.Runtime != "pi" {
		return ""
	}
	provider, _, ok := strings.Cut(rec.Model, "/")
	keys := piProviderKeys[provider]
	if !ok || len(keys) == 0 {
		return ""
	}
	for _, k := range keys {
		if slices.Contains(rec.EnvNeeds, k) || slices.Contains(rec.CredNeeds, k) {
			return ""
		}
	}
	if rec.PiAgentDir != "" {
		if raw, err := os.ReadFile(filepath.Join(rec.PiAgentDir, "auth.json")); err == nil {
			var stored map[string]json.RawMessage
			if json.Unmarshal(raw, &stored) == nil {
				if _, ok := stored[provider]; ok {
					return ""
				}
			}
		}
	}
	return fmt.Sprintf("model %s needs one of %s, and none is in the seat's env needs or its pi login; the seat goes live, then stalls at its first turn", rec.Model, strings.Join(keys, ", "))
}
