package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/flock"
	"github.com/omriariav/amq-squad/v3/internal/schedproc"
	taskstore "github.com/omriariav/amq-squad/v3/internal/task"
)

func ingestToolResult(t *testing.T, handle string) bool {
	t.Helper()
	withHookStdin(t, []byte(`{"hook_event_name":"PostToolUse","tool_name":"Bash","tool_response":{"stdout":"ok"}}`))
	out, _, err := captureOutput(t, func() error { return Run([]string{"evidence", "ingest", "--me", handle, "--json"}, "test") })
	if err != nil {
		t.Fatal(err)
	}
	return strings.Contains(out, `"lease_renewed":true`)
}

// Review 1 ruling 3: evidence ingest renews the seat's claimed lease on a
// tool result, at most once per renewal interval; never an unclaimed lease.
func TestEvidenceIngestRenewsTheClaimedLease(t *testing.T) {
	f := newHookFixture(t)
	store, err := taskstore.Open(f.root)
	if err != nil {
		t.Fatal(err)
	}
	for _, title := range []string{"cto's", "qa's"} {
		if _, err := store.Add(taskstore.AddInput{Title: title, Class: "script"}, f.now.Add(-time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.Claim("t1", "cto", nil, 10*time.Minute, f.now.Add(-5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	previousTaskNow := taskNow
	taskNow = func() time.Time { return f.now.Add(-2 * time.Minute) }
	t.Cleanup(func() { taskNow = previousTaskNow })
	if _, err := taskCLI(t, "renew", "--me", "cto"); err != nil {
		t.Fatalf("manual task renew: %v", err)
	}
	if !ingestToolResult(t, "cto") {
		t.Fatal("a tool result did not renew the claimed lease")
	}
	if got, _ := store.Get("t1"); !got.Lease.ExpiresAt.Equal(f.now.Add(taskstore.DefaultLeaseDuration)) || got.Lease.Renewals != 2 || got.Lease.ActivityRenewals != 1 || got.Lease.ManualRenewals != 1 || got.Lease.LastRenewal != "activity" {
		t.Fatalf("lease after renewal: %+v", got.Lease)
	}
	decisions, err := schedproc.ReadDecisionsTail(f.root)
	if err != nil || len(decisions) != 1 || decisions[0].Kind != "lease_renewed" || decisions[0].TaskID != "t1" || decisions[0].Reason != "first activity renewal of attempt 1" {
		t.Fatalf("first-renewal decisions = %+v, err %v", decisions, err)
	}
	if ingestToolResult(t, "cto") {
		t.Fatal("a second tool result inside the interval wrote the store")
	}
	if err := os.MkdirAll(filepath.Join(f.root, "agents", "qa"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Grant("t2", "qa", nil, 10*time.Minute, f.now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AM_ME", "qa")
	if ingestToolResult(t, "qa") {
		t.Fatal("ingest renewed an unclaimed lease")
	}
}

func TestActivityUsesTheMailboxIdentityAndFiltersNonWorkEvents(t *testing.T) {
	f := newHookFixture(t)
	store, _ := taskstore.Open(f.root)
	for _, title := range []string{"A", "B"} {
		if _, err := store.Add(taskstore.AddInput{Title: title, Class: "script"}, f.now.Add(-time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.Claim("t1", "cto", nil, 10*time.Minute, f.now.Add(-5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(f.root, "agents", "qa"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Claim("t2", "qa", nil, 10*time.Minute, f.now.Add(-5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	beforeB, _ := store.Get("t2")
	// Receipt traffic is not proof of work and does not renew either seat.
	withHookStdin(t, []byte(`{"type":"receipt","handle":"qa","outcome":"drained"}`))
	out, _, err := captureOutput(t, func() error { return Run([]string{"evidence", "ingest", "--me", "cto", "--json"}, "test") })
	if err != nil || strings.Contains(out, `"lease_renewed":true`) {
		t.Fatalf("receipt activity: %v %s", err, out)
	}
	if !ingestToolResult(t, "cto") {
		t.Fatal("the mailbox owner's real tool result did not renew")
	}
	afterA, _ := store.Get("t1")
	afterB, _ := store.Get("t2")
	if afterA.Lease.Renewals != 1 || afterB.Lease.Renewals != 0 || !afterB.Lease.ExpiresAt.Equal(beforeB.Lease.ExpiresAt) {
		t.Fatalf("mailbox attribution: A=%+v B=%+v", afterA.Lease, afterB.Lease)
	}
	for _, event := range []string{"PermissionRequest", "UserPromptSubmit"} {
		if isLeaseActivity(hookLedgerLine{Event: event, Outcome: "ok"}) {
			t.Fatalf("%s counted as work", event)
		}
	}
	for _, line := range []hookLedgerLine{{Event: "receipt", Outcome: "ok"}, {Event: "evidence_post", Outcome: "ok"}} {
		if isLeaseActivity(line) {
			t.Fatalf("transport event counted as work: %+v", line)
		}
	}
	for _, line := range []hookLedgerLine{
		{Event: "PostToolUse", Tool: "Bash", Outcome: "ok"},
		{Event: "tool_result", Tool: "bash", Outcome: "ok"},
	} {
		if !isLeaseActivity(line) {
			t.Fatalf("runtime work event was not shared activity: %+v", line)
		}
	}
}

func TestEachAttemptLogsOnlyItsFirstActivityRenewal(t *testing.T) {
	f := newHookFixture(t)
	current := f.now
	previous := hookNow
	hookNow = func() time.Time { return current }
	t.Cleanup(func() { hookNow = previous })
	store, _ := taskstore.Open(f.root)
	if _, err := store.Add(taskstore.AddInput{Title: "per attempt", Class: "script", Budget: time.Hour}, f.now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	first, err := store.Claim("t1", "cto", nil, 10*time.Minute, f.now.Add(-5*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	line := hookLedgerLine{Event: "PostToolUse", Tool: "Bash", Outcome: "ok"}
	if result, attempt := renewLeaseOnToolResult(f.id(), line, current); result != "renewed" || attempt != first.Attempt {
		t.Fatalf("first activity = %q attempt %d", result, attempt)
	}
	current = current.Add(2 * time.Minute)
	if result, attempt := renewLeaseOnToolResult(f.id(), line, current); result != "renewed" || attempt != first.Attempt {
		t.Fatalf("second activity = %q attempt %d", result, attempt)
	}
	decisions, err := schedproc.ReadDecisionsTail(f.root)
	if err != nil || len(decisions) != 1 {
		t.Fatalf("attempt 1 decisions = %+v, err %v", decisions, err)
	}
	if _, err := store.Release("t1", "cto", "retry", current.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	second, err := store.Claim("t1", "cto", nil, 10*time.Minute, current.Add(2*time.Second))
	if err != nil || second.Attempt == first.Attempt {
		t.Fatalf("second attempt = %+v, err %v", second, err)
	}
	if _, renewed, err := store.RenewAttempt("cto", second.Attempt, 10*time.Minute, 0, current.Add(time.Minute)); err != nil || !renewed {
		t.Fatalf("attempt 2 manual renewal: renewed=%v err=%v", renewed, err)
	}
	current = current.Add(2 * time.Minute)
	if result, attempt := renewLeaseOnToolResult(f.id(), line, current); result != "renewed" || attempt != second.Attempt {
		t.Fatalf("attempt 2 activity = %q attempt %d", result, attempt)
	}
	decisions, err = schedproc.ReadDecisionsTail(f.root)
	if err != nil || len(decisions) != 2 || decisions[1].Reason != "first activity renewal of attempt 2" {
		t.Fatalf("per-attempt decisions = %+v, err %v", decisions, err)
	}
}

func TestLegacyUnlabelledRenewalsDoNotHideFirstActivity(t *testing.T) {
	f := newHookFixture(t)
	store, _ := taskstore.Open(f.root)
	if _, err := store.Add(taskstore.AddInput{Title: "legacy", Class: "script"}, f.now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Claim("t1", "cto", nil, 10*time.Minute, f.now.Add(-5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(store.Dir, "t1.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var legacy taskstore.Task
	if err := json.Unmarshal(raw, &legacy); err != nil {
		t.Fatal(err)
	}
	legacy.Lease.Renewals = 3
	legacy.Lease.ActivityRenewals = 0
	legacy.Lease.ManualRenewals = 0
	legacy.Lease.LastRenewal = ""
	raw, err = json.MarshalIndent(legacy, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if !ingestToolResult(t, "cto") {
		t.Fatal("legacy lease did not take its first labelled activity renewal")
	}
	got, _ := store.Get("t1")
	if got.Lease.Renewals != 4 || got.Lease.ActivityRenewals != 1 || got.Lease.ManualRenewals != 0 || got.Lease.LastRenewal != "activity" {
		t.Fatalf("legacy lease = %+v", got.Lease)
	}
	decisions, err := schedproc.ReadDecisionsTail(f.root)
	if err != nil || len(decisions) != 1 || decisions[0].Reason != "first activity renewal of attempt 1 after 3 unlabelled renewals" {
		t.Fatalf("legacy decision = %+v, err %v", decisions, err)
	}
	shown, err := taskCLI(t, "show", "t1")
	if err != nil || !strings.Contains(shown, "4 renewals (1 activity, 0 manual, 3 unlabelled)") {
		t.Fatalf("task show = %q, err %v", shown, err)
	}
}

func TestEventStampedBeforeAReplacementAttemptCannotRenewIt(t *testing.T) {
	f := newHookFixture(t)
	currentNow := f.now
	hookNow = func() time.Time { return currentNow }
	store, _ := taskstore.Open(f.root)
	if _, err := store.Add(taskstore.AddInput{Title: "attempt-bound", Class: "script"}, f.now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	first, err := store.Claim("t1", "cto", nil, 10*time.Minute, f.now.Add(-10*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	var second taskstore.Task
	prev := ingestAfterStart
	ingestAfterStart = func() {
		if _, err := store.ExpireDue(f.now); err != nil {
			t.Fatal(err)
		}
		second, err = store.Claim("t1", "cto", nil, 10*time.Minute, f.now.Add(time.Nanosecond))
		if err != nil || first.Attempt == second.Attempt {
			t.Fatalf("second attempt: %+v %v", second, err)
		}
		currentNow = f.now.Add(2 * time.Minute)
	}
	t.Cleanup(func() { ingestAfterStart = prev })
	withHookStdin(t, []byte(`{"hook_event_name":"PostToolUse","tool_name":"Bash","tool_response":{"stdout":"late"},"attempt":1}`))
	out, _, err := captureOutput(t, func() error { return Run([]string{"evidence", "ingest", "--me", "cto", "--json"}, "test") })
	if err != nil || strings.Contains(out, `"lease_renewed":true`) {
		t.Fatalf("old attempt renewed: %v %s", err, out)
	}
	got, _ := store.Get("t1")
	if got.Lease.Renewals != 0 {
		t.Fatalf("late attempt changed lease: %+v", got.Lease)
	}
}

// A held store lock (a scheduler tick, another writer) never blocks the
// hook: the renewal is skipped and the ledger line says so (reviewer rule:
// try-lock, at most 20 ms of waiting).
func TestEvidenceIngestSkipsRenewalWhenTheStoreIsLocked(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no advisory locks on Windows (internal/flock)")
	}
	f := newHookFixture(t)
	store, _ := taskstore.Open(f.root)
	if _, err := store.Add(taskstore.AddInput{Title: "cto's", Class: "script"}, f.now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Claim("t1", "cto", nil, 10*time.Minute, f.now.Add(-5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	held, err := flock.AcquireExclusive(filepath.Join(store.Dir, ".lock"))
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	renewed := ingestToolResult(t, "cto")
	elapsed := time.Since(start)
	_ = held.Close()
	if renewed || elapsed > 20*time.Millisecond+50*time.Millisecond {
		t.Fatalf("ingest under a held lock: renewed %v in %v", renewed, elapsed)
	}
	lines := readLedger(t, hookExtPath(f.root, "ledger", "cto.jsonl"))
	if got := lines[len(lines)-1].LeaseRenewal; got != "skipped" {
		t.Fatalf("ledger lease_renewal = %q, want skipped", got)
	}
	if got, _ := store.Get("t1"); got.Lease.Renewals != 0 {
		t.Fatalf("the lease changed under a held lock: %+v", got.Lease)
	}
	t.Logf("ingest with the store lock held: %v (renewal skipped)", elapsed)
	if !ingestToolResult(t, "cto") {
		t.Fatal("the next tool result after the lock was released did not renew")
	}
}

// TestEvidenceIngestRenewalTiming logs the in-process cost of an ingest
// that renews (a store write) and one inside the interval (a read).
func TestEvidenceIngestRenewalTiming(t *testing.T) {
	f := newHookFixture(t)
	store, _ := taskstore.Open(f.root)
	for i := 0; i < 30; i++ {
		if _, err := store.Add(taskstore.AddInput{Title: "x", Class: "script"}, f.now.Add(-time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.Claim("t1", "cto", nil, 10*time.Minute, f.now.Add(-5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	measure := func(n int) []time.Duration {
		var d []time.Duration
		for i := 0; i < n; i++ {
			start := time.Now()
			ingestToolResult(t, "cto")
			d = append(d, time.Since(start))
		}
		sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
		return d
	}
	writing := measure(1)
	reading := measure(20)
	t.Logf("ingest with a renewal write: %v; inside the interval: p50 %v, p95 %v", writing[0], reading[9], reading[18])
}
