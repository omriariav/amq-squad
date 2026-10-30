//go:build unix

package seatlaunch

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/launch"
)

// t16 round 6 (Codex on round 5, item 1): a restage of the handle that lands
// inside the expiry mark's compare window waits for the record's lock, so
// the earlier launch's mark never writes its old record ("expired") over
// the later launch's whole record. The seam runs B's restage between A's
// compare and A's write. Reverted (Stage writing without the sidecar lock),
// B's record lands inside the window and A's write replaces it.
func TestRestageInsideTheMarkWindowKeepsTheLaterRecord(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	dir := launch.StagedDir(root, "cto")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, launch.FileName)
	rec := func(launchID, requestID, token string) launch.Record {
		return launch.Record{Handle: "cto", Root: root, Session: "s1", Runtime: "claude", LaunchID: launchID, RequestID: requestID,
			Proof: &launch.ProofTicket{Token: token}}
	}
	if err := launch.WriteFile(path, rec("launch-a", "req-a", "tok-a")); err != nil {
		t.Fatal(err)
	}
	a := &proofServer{root: root, launchID: "launch-a", now: time.Now, lim: proofLimits{logCap: rejectLogCap}}
	restaged := make(chan error, 1)
	prev := markCompareHook
	t.Cleanup(func() { markCompareHook = prev })
	markCompareHook = func(string) {
		go func() { restaged <- writeStaged(path, rec("launch-b", "req-b", "tok-b")) }()
		// Give B's restage the time to land inside the window if it can.
		time.Sleep(200 * time.Millisecond)
	}
	if err := a.markToken("cto", "req-a", launch.TokenExpired); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-restaged:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("B's restage never finished")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := launch.Decode(raw)
	if err != nil || got.LaunchID != "launch-b" || got.RequestID != "req-b" || got.Proof == nil || got.Proof.Token != "tok-b" {
		t.Fatalf("B's record after A's mark: launch %s request %s token %+v (%v)", got.LaunchID, got.RequestID, got.Proof, err)
	}
}

// t16 round 6 (Codex on round 5, item 2): every proof server shares
// proof-rejects.log, so its stat, rotation, open and append run under one
// file lock. Two servers write 100 lines each at once against a cap of 120
// lines: the log rotates exactly once, no line is lost (200 distinct lines
// across the log and .1) and neither file passes the cap. Reverted (a
// per-server mutex), the writers rotate over each other's .1, drop lines or
// pass the cap.
func TestProofLogWritersShareOneLock(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	if err := os.MkdirAll(filepath.Join(root, "extensions", "amq-squad", "launch"), 0o700); err != nil {
		t.Fatal(err)
	}
	line := func(who string, i int) []byte {
		return []byte(fmt.Sprintf(`{"code":"x","server":"%s","n":%04d}`, who, i))
	}
	limit := int64(120 * (len(line("s1", 0)) + 1))
	servers := []*proofServer{
		{root: root, now: time.Now, lim: proofLimits{logCap: limit}},
		{root: root, now: time.Now, lim: proofLimits{logCap: limit}},
	}
	var wg sync.WaitGroup
	for i, s := range servers {
		wg.Add(1)
		go func(who string, s *proofServer) {
			defer wg.Done()
			for n := 0; n < 100; n++ {
				s.appendLog(line(who, n))
			}
		}(fmt.Sprintf("s%d", i+1), s)
	}
	wg.Wait()
	seen := map[string]bool{}
	path := filepath.Join(root, "extensions", "amq-squad", "launch", "proof-rejects.log")
	for _, p := range []string{path, path + ".1"} {
		f, err := os.Open(p)
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		st, _ := f.Stat()
		if st.Size() > limit {
			t.Errorf("%s holds %d bytes, past the cap %d", filepath.Base(p), st.Size(), limit)
		}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			seen[sc.Text()] = true
		}
		f.Close()
	}
	if len(seen) != 200 {
		t.Fatalf("%d distinct lines across the log and .1, want 200", len(seen))
	}
}
