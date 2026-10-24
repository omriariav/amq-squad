package cli

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

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
	if !ingestToolResult(t, "cto") {
		t.Fatal("a tool result did not renew the claimed lease")
	}
	if got, _ := store.Get("t1"); !got.Lease.ExpiresAt.Equal(f.now.Add(taskstore.DefaultLeaseDuration)) || got.Lease.Renewals != 1 {
		t.Fatalf("lease after renewal: %+v", got.Lease)
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
