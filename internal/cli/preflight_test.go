package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/launch"
)

func writeWakeLock(t *testing.T, agentDir string, lock wakeLockFile) {
	t.Helper()
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(lock)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wakeLockPath(agentDir), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func writePresence(t *testing.T, agentDir string, pres presenceFile) {
	t.Helper()
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(pres)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "presence.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// fakeProbe returns a probe with controllable PID/process behavior.
func fakeProbe(alive map[int]bool, match map[int]string, now time.Time) duplicateLaunchProbe {
	return duplicateLaunchProbe{
		PIDAlive: func(pid int) bool { return alive[pid] },
		ProcessMatch: func(pid int, predicate func(args string) bool) bool {
			args, ok := match[pid]
			if !ok {
				return false
			}
			return predicate(args)
		},
		Now: func() time.Time { return now },
	}
}

func TestPreflightStaleWakeLockIsPreservedForAMQ(t *testing.T) {
	agentDir := t.TempDir()
	writeWakeLock(t, agentDir, wakeLockFile{PID: 99999, Root: "/r"})

	probe := fakeProbe(map[int]bool{}, nil, time.Now())
	pf := agentLaunchPreflight{AgentDir: agentDir, Handle: "cto", Workstream: "w", Root: "/r"}
	blocker, err := pf.check(probe)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if blocker != nil {
		t.Fatalf("stale lock should not block: %v", blocker)
	}
	if _, err := os.Stat(wakeLockPath(agentDir)); err != nil {
		t.Fatalf("stale wake lock must be preserved for AMQ cleanup: %v", err)
	}
}

func TestPreflightLiveWakeLockBlocks(t *testing.T) {
	agentDir := t.TempDir()
	writeWakeLock(t, agentDir, wakeLockFile{PID: 1234, Root: "/r"})

	probe := fakeProbe(
		map[int]bool{1234: true},
		map[int]string{1234: "amq wake --me cto --root /r"},
		time.Now(),
	)
	pf := agentLaunchPreflight{AgentDir: agentDir, Handle: "cto", Workstream: "w", Root: "/r"}
	blocker, err := pf.check(probe)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if blocker == nil {
		t.Fatal("live wake lock should block")
	}
	if !strings.Contains(blocker.Error(), "wake") || !strings.Contains(blocker.Error(), "1234") {
		t.Fatalf("blocker should name wake source and pid: %s", blocker.Error())
	}
	if !strings.Contains(blocker.Error(), "--force-duplicate") {
		t.Fatalf("ordinary live blocker should retain force override guidance: %s", blocker.Error())
	}
}

func TestPreflightLiveWakePIDReuseIsStale(t *testing.T) {
	agentDir := t.TempDir()
	writeWakeLock(t, agentDir, wakeLockFile{PID: 1234, Root: "/r"})

	probe := fakeProbe(
		map[int]bool{1234: true},
		map[int]string{1234: "node /usr/local/bin/something-else"},
		time.Now(),
	)
	pf := agentLaunchPreflight{AgentDir: agentDir, Handle: "cto", Workstream: "w", Root: "/r"}
	blocker, err := pf.check(probe)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if blocker != nil {
		t.Fatalf("PID reuse with non-wake command should be stale: %v", blocker)
	}
	if _, err := os.Stat(wakeLockPath(agentDir)); err != nil {
		t.Fatalf("PID-reuse stale wake lock must be preserved for AMQ cleanup: %v", err)
	}
}

func TestPreflightLiveAgentRecordBlocks(t *testing.T) {
	agentDir := t.TempDir()
	rec := launch.Record{
		Binary:    "codex",
		Handle:    "cto",
		AgentPID:  4242,
		AgentTTY:  "/dev/ttys001",
		StartedAt: time.Now().Add(-5 * time.Minute),
	}
	if err := launch.Write(agentDir, rec); err != nil {
		t.Fatal(err)
	}

	probe := fakeProbe(
		map[int]bool{4242: true},
		map[int]string{4242: "/usr/local/bin/codex"},
		time.Now(),
	)
	pf := agentLaunchPreflight{AgentDir: agentDir, Handle: "cto", Workstream: "w", Root: "/r", Binary: "codex"}
	blocker, err := pf.check(probe)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if blocker == nil {
		t.Fatal("live agent record should block")
	}
	if !strings.Contains(blocker.Error(), "launch") || !strings.Contains(blocker.Error(), "4242") {
		t.Fatalf("blocker should name launch source and pid: %s", blocker.Error())
	}
}

func TestPreflightDeadAgentRecordIsNonBlocking(t *testing.T) {
	agentDir := t.TempDir()
	if err := launch.Write(agentDir, launch.Record{Binary: "codex", Handle: "cto", AgentPID: 9999, StartedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	probe := fakeProbe(map[int]bool{}, nil, time.Now())
	pf := agentLaunchPreflight{AgentDir: agentDir, Handle: "cto", Workstream: "w", Root: "/r", Binary: "codex"}
	blocker, err := pf.check(probe)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if blocker != nil {
		t.Fatalf("dead PID should not block: %v", blocker)
	}
}

func TestPreflightReusedSameBinaryAgentPIDIsNonBlocking(t *testing.T) {
	agentDir := t.TempDir()
	started := time.Now().Add(-10 * time.Minute)
	if err := launch.Write(agentDir, launch.Record{
		Binary: "codex", Handle: "cto", AgentPID: 4242,
		AgentTTY: "/dev/ttys001", StartedAt: started,
	}); err != nil {
		t.Fatal(err)
	}
	probe := fakeProbe(
		map[int]bool{4242: true},
		map[int]string{4242: "/usr/local/bin/codex"},
		time.Now(),
	)
	probe.ProcessTTY = func(pid int) (string, bool) { return "/dev/ttys001", pid == 4242 }
	probe.ProcessStartTime = func(pid int) (time.Time, bool) {
		return started.Add(launchProcessStartSkewEpsilon + time.Nanosecond), pid == 4242
	}
	pf := agentLaunchPreflight{AgentDir: agentDir, Handle: "cto", Workstream: "w", Root: "/r", Binary: "codex"}
	blocker, err := pf.check(probe)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if blocker != nil {
		t.Fatalf("same-binary reused PID must not block relaunch: %v", blocker)
	}
}

func TestPreflightTTYReusedAgentPIDIsNonBlocking(t *testing.T) {
	agentDir := t.TempDir()
	started := time.Now().Add(-time.Minute)
	if err := launch.Write(agentDir, launch.Record{
		Binary: "codex", Handle: "cto", AgentPID: 4242,
		AgentTTY: "/dev/ttys001", StartedAt: started,
	}); err != nil {
		t.Fatal(err)
	}
	probe := fakeProbe(
		map[int]bool{4242: true},
		map[int]string{4242: "/usr/local/bin/codex"},
		time.Now(),
	)
	probe.ProcessTTY = func(pid int) (string, bool) { return "/dev/ttys099", pid == 4242 }
	probe.ProcessStartTime = func(pid int) (time.Time, bool) { return started, pid == 4242 }
	pf := agentLaunchPreflight{AgentDir: agentDir, Handle: "cto", Workstream: "w", Root: "/r", Binary: "codex"}
	blocker, err := pf.check(probe)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if blocker != nil {
		t.Fatalf("same-binary PID on a different TTY must not block relaunch: %v", blocker)
	}
}

func TestPreflightActivePresenceWithLiveWakeBlocks(t *testing.T) {
	agentDir := t.TempDir()
	writePresence(t, agentDir, presenceFile{Schema: 1, Handle: "cto", Status: "active", LastSeen: time.Now().Add(-5 * time.Second)})

	probe := fakeProbe(map[int]bool{}, nil, time.Now())
	pf := agentLaunchPreflight{AgentDir: agentDir, Handle: "cto", Workstream: "w", Root: "/r"}
	blocker, err := pf.check(probe)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if blocker == nil {
		t.Fatal("fresh active presence should block")
	}
	if !strings.Contains(blocker.Error(), "presence") {
		t.Fatalf("blocker should mention presence: %s", blocker.Error())
	}
}

func TestPreflightStalePresenceDoesNotBlock(t *testing.T) {
	agentDir := t.TempDir()
	writePresence(t, agentDir, presenceFile{Schema: 1, Handle: "cto", Status: "active", LastSeen: time.Now().Add(-10 * time.Minute)})

	probe := fakeProbe(map[int]bool{}, nil, time.Now())
	pf := agentLaunchPreflight{AgentDir: agentDir, Handle: "cto", Workstream: "w", Root: "/r"}
	blocker, err := pf.check(probe)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if blocker != nil {
		t.Fatalf("stale presence should not block: %v", blocker)
	}
}

func TestPreflightForceDuplicateOverridesAllSignals(t *testing.T) {
	agentDir := t.TempDir()
	writeWakeLock(t, agentDir, wakeLockFile{PID: 1234, Root: "/r"})
	probe := fakeProbe(
		map[int]bool{1234: true},
		map[int]string{1234: "amq wake --me cto --root /r"},
		time.Now(),
	)
	pf := agentLaunchPreflight{AgentDir: agentDir, Handle: "cto", Workstream: "w", Root: "/r", Force: true}
	blocker, err := pf.check(probe)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if blocker != nil {
		t.Fatalf("--force-duplicate should override blockers: %v", blocker)
	}
}

func TestPreflightDryRunDoesNotRemoveStaleWakeLock(t *testing.T) {
	agentDir := t.TempDir()
	writeWakeLock(t, agentDir, wakeLockFile{PID: 9999, Root: "/r"})

	// PID is dead -> normally classified stale and removed. With DryRun,
	// the file must remain.
	probe := fakeProbe(map[int]bool{}, nil, time.Now())
	pf := agentLaunchPreflight{AgentDir: agentDir, Handle: "cto", Workstream: "w", Root: "/r", DryRun: true}
	blocker, err := pf.check(probe)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if blocker != nil {
		t.Fatalf("dead PID under dry-run should not block: %v", blocker)
	}
	if _, err := os.Stat(wakeLockPath(agentDir)); err != nil {
		t.Fatalf("dry-run preflight must not remove stale wake lock: %v", err)
	}
}

func TestWakeProcessMatcherRejectsForeignRoot(t *testing.T) {
	// Regression: the previous handle-only matcher accepted any live
	// `amq wake --me cto` for the lock PID, even if the live process
	// belonged to a wake from a different workstream/root. The matcher
	// must still recognize relative-root equivalents but reject unrelated
	// absolute roots.
	cases := []struct {
		name     string
		args     string
		expected string
		want     bool
	}{
		{
			name:     "relative root matches absolute expected (suffix)",
			args:     "amq wake --me cto --root .agent-mail/issue-96",
			expected: "/abs/proj/.agent-mail/issue-96",
			want:     true,
		},
		{
			name:     "absolute root equal to expected",
			args:     "amq wake --me cto --root /abs/proj/.agent-mail/issue-96",
			expected: "/abs/proj/.agent-mail/issue-96",
			want:     true,
		},
		{
			name:     "absolute root different from expected blocks match",
			args:     "amq wake --me cto --root /other/proj/.agent-mail/issue-96",
			expected: "/abs/proj/.agent-mail/issue-96",
			want:     false,
		},
		{
			name:     "no --root token in args defers to agent-dir anchoring",
			args:     "amq wake --me cto",
			expected: "/abs/proj/.agent-mail/issue-96",
			want:     true,
		},
		{
			name:     "wrong handle never matches",
			args:     "amq wake --me fullstack --root /abs/proj/.agent-mail/issue-96",
			expected: "/abs/proj/.agent-mail/issue-96",
			want:     false,
		},
		{
			name:     "--root=value form accepted",
			args:     "amq wake --me=cto --root=/abs/proj/.agent-mail/issue-96",
			expected: "/abs/proj/.agent-mail/issue-96",
			want:     true,
		},
		{
			name:     "absolute root with spaces survives strings.Fields",
			args:     "amq wake --me cto --root /Users/me/my project/.agent-mail/issue-96",
			expected: "/Users/me/my project/.agent-mail/issue-96",
			want:     true,
		},
		{
			name:     "relative root with spaces against absolute expected",
			args:     "amq wake --me cto --root my project/.agent-mail/issue-96",
			expected: "/Users/me/my project/.agent-mail/issue-96",
			want:     true,
		},
		{
			name:     "absolute root with spaces unrelated to expected blocks match",
			args:     "amq wake --me cto --root /Users/me/other project/.agent-mail/issue-96",
			expected: "/Users/me/my project/.agent-mail/issue-96",
			want:     false,
		},
		{
			name:     "trailing flag stops the joined --root value",
			args:     "amq wake --me cto --root /Users/me/my project/.agent-mail/issue-96 --tty foo",
			expected: "/Users/me/my project/.agent-mail/issue-96",
			want:     true,
		},
		{
			name:     "expected root that is only a prefix of actual root rejects",
			args:     "amq wake --me cto --root /Users/me/proj/.agent-mail/issue-96-old",
			expected: "/Users/me/proj/.agent-mail/issue-96",
			want:     false,
		},
		{
			name:     "expected root prefix of a deeper actual path rejects",
			args:     "amq wake --me cto --root /Users/me/proj/.agent-mail/issue-96/sub",
			expected: "/Users/me/proj/.agent-mail/issue-96",
			want:     false,
		},
		{
			name:     "expected root that is only a suffix of actual root rejects",
			args:     "amq wake --me cto --root /tmp/Users/me/proj/.agent-mail/issue-96",
			expected: "/Users/me/proj/.agent-mail/issue-96",
			want:     false,
		},
		{
			name:     "prefix handle does not false-match (cto vs cto2)",
			args:     "amq wake --me cto2 --root /Users/me/proj/.agent-mail/issue-96",
			expected: "/Users/me/proj/.agent-mail/issue-96",
			want:     false,
		},
		{
			name:     "prefix handle with hyphen suffix does not false-match",
			args:     "amq wake --me cto-extra --root /Users/me/proj/.agent-mail/issue-96",
			expected: "/Users/me/proj/.agent-mail/issue-96",
			want:     false,
		},
		{
			name:     "prefix handle with --me=value form does not false-match",
			args:     "amq wake --me=cto2 --root /Users/me/proj/.agent-mail/issue-96",
			expected: "/Users/me/proj/.agent-mail/issue-96",
			want:     false,
		},
	}
	for _, tc := range cases {
		got := wakeProcessMatcher("cto", tc.expected)(tc.args)
		if got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestWakeProcessMatcherAcceptsSymlinkedAbsoluteRoot(t *testing.T) {
	realBase := t.TempDir()
	linkBase := filepath.Join(t.TempDir(), "linked")
	if err := os.Symlink(realBase, linkBase); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	expected := filepath.Join(realBase, ".agent-mail", "issue-96")
	actual := filepath.Join(linkBase, ".agent-mail", "issue-96")
	if err := os.MkdirAll(expected, 0o755); err != nil {
		t.Fatal(err)
	}
	args := "amq wake --me cto --root " + actual
	if !wakeProcessMatcher("cto", expected)(args) {
		t.Fatalf("wake matcher should accept symlink-equivalent root: args=%q expected=%q", args, expected)
	}
}

func TestPreflightLiveWakeWithSpacesInRootBlocks(t *testing.T) {
	// Regression: extractRootFromArgs used to split paths with spaces on
	// strings.Fields and reject the live wake as PID reuse. The fast-path
	// substring check + joined-token fallback must keep the live wake live.
	agentDir := t.TempDir()
	expectedRoot := "/Users/me/my project/.agent-mail/issue-96"
	writeWakeLock(t, agentDir, wakeLockFile{PID: 7777, Root: expectedRoot})

	probe := fakeProbe(
		map[int]bool{7777: true},
		map[int]string{7777: "amq wake --me cto --root " + expectedRoot},
		time.Now(),
	)
	pf := agentLaunchPreflight{AgentDir: agentDir, Handle: "cto", Workstream: "w", Root: expectedRoot}
	blocker, err := pf.check(probe)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if blocker == nil {
		t.Fatal("live wake with spaces in root should still block")
	}
	if _, err := os.Stat(wakeLockPath(agentDir)); err != nil {
		t.Fatalf("live wake lock with spaces in root must remain on block: %v", err)
	}
}

// Regression: a wake.lock for handle "cto" whose live PID is actually a
// sibling wake for "cto2" must classify as stale, not pid:N.
func TestWakeHealthForMemberRejectsPrefixHandleReuse(t *testing.T) {
	agentDir := t.TempDir()
	expectedRoot := "/abs/proj/.agent-mail/issue-96"
	writeWakeLock(t, agentDir, wakeLockFile{PID: 8888, Root: expectedRoot})

	original := defaultDuplicateLaunchProbe
	defaultDuplicateLaunchProbe = duplicateLaunchProbe{
		PIDAlive: func(pid int) bool { return pid == 8888 },
		ProcessMatch: func(pid int, predicate func(args string) bool) bool {
			return predicate("amq wake --me cto2 --root " + expectedRoot)
		},
		Now: time.Now,
	}
	t.Cleanup(func() { defaultDuplicateLaunchProbe = original })

	got := wakeHealthForMember(agentDir, expectedRoot, "cto", launch.Record{}, false)
	if got != "stale" {
		t.Errorf("prefix-handle PID reuse must classify as stale, got %q", got)
	}
}

func TestPreflightSiblingWorkstreamRootIsStale(t *testing.T) {
	// Regression: the literal-substring fast path used to accept a sibling
	// workstream's wake whose --root was a strict superstring of expected
	// (e.g. issue-96 vs issue-96-old). Bounded matching must reject it and
	// the stale lock must be preserved for AMQ's guarded cleanup.
	agentDir := t.TempDir()
	expectedRoot := "/Users/me/proj/.agent-mail/issue-96"
	writeWakeLock(t, agentDir, wakeLockFile{PID: 5555, Root: expectedRoot})

	probe := fakeProbe(
		map[int]bool{5555: true},
		map[int]string{5555: "amq wake --me cto --root /Users/me/proj/.agent-mail/issue-96-old"},
		time.Now(),
	)
	pf := agentLaunchPreflight{AgentDir: agentDir, Handle: "cto", Workstream: "w", Root: expectedRoot}
	blocker, err := pf.check(probe)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if blocker != nil {
		t.Fatalf("sibling workstream PID reuse should not block: %v", blocker)
	}
	if _, err := os.Stat(wakeLockPath(agentDir)); err != nil {
		t.Fatalf("sibling-root stale lock must be preserved for AMQ cleanup: %v", err)
	}
}

func TestPreflightSuffixOfForeignRootIsStale(t *testing.T) {
	// Regression: the fast path needs a left boundary too; a different
	// absolute root that ends with the expected root must classify the
	// lock as stale.
	agentDir := t.TempDir()
	expectedRoot := "/Users/me/proj/.agent-mail/issue-96"
	writeWakeLock(t, agentDir, wakeLockFile{PID: 6666, Root: expectedRoot})

	probe := fakeProbe(
		map[int]bool{6666: true},
		map[int]string{6666: "amq wake --me cto --root /tmp/Users/me/proj/.agent-mail/issue-96"},
		time.Now(),
	)
	pf := agentLaunchPreflight{AgentDir: agentDir, Handle: "cto", Workstream: "w", Root: expectedRoot}
	blocker, err := pf.check(probe)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if blocker != nil {
		t.Fatalf("foreign root containing expected as suffix should not block: %v", blocker)
	}
	if _, err := os.Stat(wakeLockPath(agentDir)); err != nil {
		t.Fatalf("foreign-suffix stale lock must be preserved for AMQ cleanup: %v", err)
	}
}

func TestWakeHealthWithSpacesInRoot(t *testing.T) {
	// Regression for list/restore wake-health column.
	agentDir := t.TempDir()
	expectedRoot := "/Users/me/my project/.agent-mail/issue-96"
	rec := launch.Record{
		Binary:   "codex",
		Handle:   "cto",
		Root:     expectedRoot,
		AgentPID: 100,
	}
	if err := launch.Write(agentDir, rec); err != nil {
		t.Fatal(err)
	}
	writeWakeLock(t, agentDir, wakeLockFile{PID: 42, Root: expectedRoot})
	entry := launch.Entry{Record: rec, AgentDir: agentDir}

	probe := fakeProbe(
		map[int]bool{100: true, 42: true},
		map[int]string{
			100: "/usr/local/bin/codex",
			42:  "amq wake --me cto --root " + expectedRoot,
		},
		time.Now(),
	)
	if got := wakeHealthForEntry(entry, probe); got != "pid:42" {
		t.Errorf("spaces-in-root wake health: got %q, want pid:42", got)
	}
}

func TestPreflightForeignRootPIDReuseIsStale(t *testing.T) {
	// PID reused by another project's `amq wake --me cto` must classify
	// the stale lock as stale, not block the new launch.
	agentDir := t.TempDir()
	writeWakeLock(t, agentDir, wakeLockFile{PID: 1234, Root: "/abs/proj/.agent-mail/issue-96"})

	probe := fakeProbe(
		map[int]bool{1234: true},
		map[int]string{1234: "amq wake --me cto --root /other/proj/.agent-mail/issue-96"},
		time.Now(),
	)
	pf := agentLaunchPreflight{AgentDir: agentDir, Handle: "cto", Workstream: "w", Root: "/abs/proj/.agent-mail/issue-96"}
	blocker, err := pf.check(probe)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if blocker != nil {
		t.Fatalf("foreign-root PID reuse should not block: %v", blocker)
	}
	if _, err := os.Stat(wakeLockPath(agentDir)); err != nil {
		t.Fatalf("foreign-root stale lock must be preserved for AMQ cleanup: %v", err)
	}
}

func TestPreflightLiveWakeWithRelativeRootBlocks(t *testing.T) {
	// Regression: ps args may carry --root as a relative path while p.Root
	// is the canonical absolute resolution. Identity is anchored on the
	// agent dir + --me; root tokens must not be required to match literally.
	agentDir := t.TempDir()
	writeWakeLock(t, agentDir, wakeLockFile{PID: 1234, Root: "/abs/.agent-mail/issue-96"})

	probe := fakeProbe(
		map[int]bool{1234: true},
		map[int]string{1234: "amq wake --me cto --root .agent-mail/issue-96"},
		time.Now(),
	)
	pf := agentLaunchPreflight{AgentDir: agentDir, Handle: "cto", Workstream: "w", Root: "/abs/.agent-mail/issue-96"}
	blocker, err := pf.check(probe)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if blocker == nil {
		t.Fatal("relative-root wake should still block")
	}
	if _, err := os.Stat(wakeLockPath(agentDir)); err != nil {
		t.Fatalf("live wake lock must remain on block: %v", err)
	}
}

func TestWakeHealthForRelativeRootStillReportsLive(t *testing.T) {
	// Regression: list/restore must not report a relative-root live wake
	// as stale.
	agentDir := t.TempDir()
	rec := launch.Record{
		Binary:   "codex",
		Handle:   "cto",
		Root:     "/abs/.agent-mail/issue-96",
		AgentPID: 100,
	}
	if err := launch.Write(agentDir, rec); err != nil {
		t.Fatal(err)
	}
	writeWakeLock(t, agentDir, wakeLockFile{PID: 42, Root: "/abs/.agent-mail/issue-96"})
	entry := launch.Entry{Record: rec, AgentDir: agentDir}

	probe := fakeProbe(
		map[int]bool{100: true, 42: true},
		map[int]string{
			100: "/usr/local/bin/codex",
			42:  "amq wake --me cto --root .agent-mail/issue-96",
		},
		time.Now(),
	)
	if got := wakeHealthForEntry(entry, probe); got != "pid:42" {
		t.Errorf("relative-root wake health: got %q, want pid:42", got)
	}
}

func TestPreflightTeamAggregatesBlockers(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()
	writeWakeLock(t, dir1, wakeLockFile{PID: 11, Root: "/r"})
	writeWakeLock(t, dir2, wakeLockFile{PID: 22, Root: "/r"})
	probe := fakeProbe(
		map[int]bool{11: true, 22: true},
		map[int]string{
			11: "amq wake --me cto --root /r",
			22: "amq wake --me fullstack --root /r",
		},
		time.Now(),
	)
	plans := []agentLaunchPreflight{
		{AgentDir: dir1, Handle: "cto", Workstream: "w", Root: "/r"},
		{AgentDir: dir2, Handle: "fullstack", Workstream: "w", Root: "/r"},
	}
	err := preflightTeam(plans, probe)
	if err == nil {
		t.Fatal("two live members should block team launch")
	}
	if !strings.Contains(err.Error(), "cto") || !strings.Contains(err.Error(), "fullstack") {
		t.Fatalf("aggregated error should name both members: %s", err.Error())
	}
	if !strings.Contains(err.Error(), "2 member(s) blocked") {
		t.Fatalf("aggregated error should count blockers: %s", err.Error())
	}
}

func TestWakeHealthForEntry(t *testing.T) {
	agentDir := t.TempDir()
	rec := launch.Record{
		Binary:   "codex",
		Handle:   "cto",
		Root:     "/r",
		AgentPID: 100,
	}
	if err := launch.Write(agentDir, rec); err != nil {
		t.Fatal(err)
	}
	entry := launch.Entry{Record: rec, AgentDir: agentDir}

	now := time.Now()

	// looks active but no wake.lock → "missing"
	probeNoLock := fakeProbe(
		map[int]bool{100: true},
		map[int]string{100: "/usr/local/bin/codex"},
		now,
	)
	if got := wakeHealthForEntry(entry, probeNoLock); got != "missing" {
		t.Errorf("missing wake: got %q, want missing", got)
	}

	// wake lock present, PID alive, command matches → "pid:42"
	writeWakeLock(t, agentDir, wakeLockFile{PID: 42, Root: "/r"})
	probeAlive := fakeProbe(
		map[int]bool{100: true, 42: true},
		map[int]string{
			100: "/usr/local/bin/codex",
			42:  "amq wake --me cto --root /r",
		},
		now,
	)
	if got := wakeHealthForEntry(entry, probeAlive); got != "pid:42" {
		t.Errorf("alive wake: got %q, want pid:42", got)
	}

	// wake lock present but PID dead → "stale"
	probeDead := fakeProbe(
		map[int]bool{100: true},
		map[int]string{100: "/usr/local/bin/codex"},
		now,
	)
	if got := wakeHealthForEntry(entry, probeDead); got != "stale" {
		t.Errorf("dead wake: got %q, want stale", got)
	}

	// not active-looking → "" (no inspection)
	probeInactive := fakeProbe(map[int]bool{}, nil, now)
	if got := wakeHealthForEntry(entry, probeInactive); got != "" {
		t.Errorf("inactive: got %q, want empty", got)
	}
}

// TestPreflightZombiePresenceDoesNotBlock covers #38/#44: a fresh active
// presence file written by an orphan wake that has since died (and a
// launch.json whose AgentPID is also dead) must not keep `up` from
// relaunching. Before the fix, presence freshness alone blocked even
// after both writers were gone.
func TestPreflightZombiePresenceDoesNotBlock(t *testing.T) {
	agentDir := t.TempDir()
	writePresence(t, agentDir, presenceFile{
		Schema: 1, Handle: "cto", Status: "active",
		LastSeen: time.Now().Add(-5 * time.Second),
	})
	writeWakeLock(t, agentDir, wakeLockFile{PID: 1111, Root: "/r"})
	if err := launch.Write(agentDir, launch.Record{
		Binary: "codex", Handle: "cto", AgentPID: 2222, StartedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	// Both writers dead. Presence is fresh purely because of a recently
	// killed orphan that updated the file before exit.
	probe := fakeProbe(map[int]bool{1111: false, 2222: false}, nil, time.Now())
	pf := agentLaunchPreflight{
		AgentDir: agentDir, Handle: "cto", Workstream: "w", Root: "/r", Binary: "codex",
	}
	blocker, err := pf.check(probe)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if blocker != nil {
		t.Fatalf("zombie presence (both writers dead) should not block: %v", blocker)
	}
}

func TestPreflightReusedSameBinaryPIDDoesNotPreserveFreshPresence(t *testing.T) {
	agentDir := t.TempDir()
	started := time.Now().Add(-10 * time.Minute)
	writePresence(t, agentDir, presenceFile{
		Schema: 1, Handle: "cto", Status: "active",
		LastSeen: time.Now().Add(-5 * time.Second),
	})
	writeWakeLock(t, agentDir, wakeLockFile{PID: 1111, Root: "/r"})
	if err := launch.Write(agentDir, launch.Record{
		Binary: "codex", Handle: "cto", AgentPID: 4242, StartedAt: started,
	}); err != nil {
		t.Fatal(err)
	}
	probe := fakeProbe(
		map[int]bool{1111: false, 4242: true},
		map[int]string{4242: "/usr/local/bin/codex"},
		time.Now(),
	)
	probe.ProcessStartTime = func(pid int) (time.Time, bool) {
		return started.Add(launchProcessStartSkewEpsilon + time.Nanosecond), pid == 4242
	}
	pf := agentLaunchPreflight{
		AgentDir: agentDir, Handle: "cto", Workstream: "w", Root: "/r", Binary: "codex",
	}
	blocker, err := pf.check(probe)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if blocker != nil {
		t.Fatalf("fresh presence from dead writers must not survive same-binary PID reuse: %v", blocker)
	}
}

// TestPreflightFreshPresenceWithLiveAgentStillBlocks ensures the guard does
// not over-correct: a live launch.json PID means there's a real agent that
// could legitimately be writing presence. Includes both .wake.lock and
// launch.json so the both-record evaluation path is exercised, and asserts
// the blocker reasons keep their stable wake → launch → presence order.
func TestPreflightFreshPresenceWithLiveAgentStillBlocks(t *testing.T) {
	agentDir := t.TempDir()
	writePresence(t, agentDir, presenceFile{
		Schema: 1, Handle: "cto", Status: "active",
		LastSeen: time.Now().Add(-5 * time.Second),
	})
	writeWakeLock(t, agentDir, wakeLockFile{PID: 1111, Root: "/r"})
	if err := launch.Write(agentDir, launch.Record{
		Binary: "codex", Handle: "cto", AgentPID: 4242, StartedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	// Wake dead but matching, launch alive: presenceWriterIsKnownDead must
	// return false because the launch writer is alive.
	probe := fakeProbe(
		map[int]bool{1111: false, 4242: true},
		map[int]string{4242: "codex"},
		time.Now(),
	)
	pf := agentLaunchPreflight{
		AgentDir: agentDir, Handle: "cto", Workstream: "w", Root: "/r", Binary: "codex",
	}
	blocker, err := pf.check(probe)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if blocker == nil {
		t.Fatal("fresh presence with live agent must still block")
	}
	sources := make([]string, 0, len(blocker.Reasons))
	for _, r := range blocker.Reasons {
		sources = append(sources, r.Source)
	}
	// Wake is stale so inspectWakeLock won't emit a blocker, but launch is
	// alive and presence is fresh: both must appear, launch before presence.
	if got := strings.Join(sources, ","); got != "launch,presence" {
		t.Errorf("blocker source order = %q, want launch,presence", got)
	}
}

// TestPreflightLiveWakeAndDeadLaunchStillBlocks: wake is the writer, agent
// is gone. Wake itself raises a blocker; presence must NOT short-circuit
// to "writer dead" since the wake writer is alive.
func TestPreflightLiveWakeAndDeadLaunchStillBlocks(t *testing.T) {
	agentDir := t.TempDir()
	writePresence(t, agentDir, presenceFile{
		Schema: 1, Handle: "cto", Status: "active",
		LastSeen: time.Now().Add(-5 * time.Second),
	})
	writeWakeLock(t, agentDir, wakeLockFile{PID: 1111, Root: "/r"})
	if err := launch.Write(agentDir, launch.Record{
		Binary: "codex", Handle: "cto", AgentPID: 4242, StartedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	probe := fakeProbe(
		map[int]bool{1111: true, 4242: false},
		map[int]string{1111: "amq wake --me cto --root /r"},
		time.Now(),
	)
	pf := agentLaunchPreflight{
		AgentDir: agentDir, Handle: "cto", Workstream: "w", Root: "/r", Binary: "codex",
	}
	blocker, err := pf.check(probe)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if blocker == nil {
		t.Fatal("live wake + dead launch + fresh presence must still block")
	}
	sources := make([]string, 0, len(blocker.Reasons))
	for _, r := range blocker.Reasons {
		sources = append(sources, r.Source)
	}
	if got := strings.Join(sources, ","); got != "wake,presence" {
		t.Errorf("blocker source order = %q, want wake,presence", got)
	}
}

// TestPreflightCorruptWakeLockKeepsPresenceConservative: corrupt lock has
// no usable PID, so wakeWriterDead must report unknown — and presence must
// keep blocking rather than treat the corrupt file as "writer dead."
func TestPreflightCorruptWakeLockKeepsPresenceConservative(t *testing.T) {
	agentDir := t.TempDir()
	writePresence(t, agentDir, presenceFile{
		Schema: 1, Handle: "cto", Status: "active",
		LastSeen: time.Now().Add(-5 * time.Second),
	})
	if err := os.WriteFile(wakeLockPath(agentDir), []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := launch.Write(agentDir, launch.Record{
		Binary: "codex", Handle: "cto", AgentPID: 4242, StartedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	probe := fakeProbe(map[int]bool{4242: false}, nil, time.Now())
	pf := agentLaunchPreflight{
		AgentDir: agentDir, Handle: "cto", Workstream: "w", Root: "/r", Binary: "codex",
	}
	blocker, err := pf.check(probe)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if blocker == nil {
		t.Fatal("corrupt lock must not let presence pass: no proven dead writer")
	}
	sawPresence := false
	for _, r := range blocker.Reasons {
		if r.Source == "presence" {
			sawPresence = true
			break
		}
	}
	if !sawPresence {
		t.Errorf("expected presence in blocker reasons; got %+v", blocker.Reasons)
	}
	if _, statErr := os.Stat(wakeLockPath(agentDir)); statErr != nil {
		t.Fatalf("corrupt lock must be preserved: %v", statErr)
	}
	pf.Force = true
	forced, err := pf.check(probe)
	if err != nil || forced == nil {
		t.Fatalf("--force-duplicate must not override a fail-closed lock: blocker=%v err=%v", forced, err)
	}
	message := forced.Error()
	for _, want := range []string{"amq wake check --root /r --me cto", "amq doctor --ops"} {
		if !strings.Contains(message, want) {
			t.Fatalf("fail-closed blocker omitted remediation %q: %s", want, message)
		}
	}
	if strings.Contains(message, "--force-duplicate") {
		t.Fatalf("fail-closed blocker suggested an impossible force override: %s", message)
	}
}

// TestPreflightWakeLockOnDifferentMachineStillBlocks is the #488 preflight
// regression: a wake lock naming a different machine looks locally dead by
// PID (both the lock's PID and the launch record's AgentPID are reused/dead
// on this host), but the writer is on another machine and may well be
// alive there. A different-machine lock must never authorize the
// dead-writer conclusion that would let fresh presence be discounted.
func TestPreflightWakeLockOnDifferentMachineStillBlocks(t *testing.T) {
	agentDir := t.TempDir()
	writePresence(t, agentDir, presenceFile{
		Schema: 1, Handle: "cto", Status: "active",
		LastSeen: time.Now().Add(-5 * time.Second),
	})
	writeWakeLock(t, agentDir, wakeLockFile{PID: 4242, Root: "/r", MachineID: "some-other-machine"})
	if err := launch.Write(agentDir, launch.Record{
		Binary: "codex", Handle: "cto", AgentPID: 4242, StartedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	probe := fakeProbe(map[int]bool{4242: false}, nil, time.Now())
	pf := agentLaunchPreflight{
		AgentDir: agentDir, Handle: "cto", Workstream: "w", Root: "/r", Binary: "codex",
	}
	blocker, err := pf.check(probe)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if blocker == nil {
		t.Fatal("a different-machine wake lock must not let presence pass: no proven dead writer")
	}
	sawPresence := false
	for _, r := range blocker.Reasons {
		if r.Source == "presence" {
			sawPresence = true
			break
		}
	}
	if !sawPresence {
		t.Errorf("expected presence in blocker reasons; got %+v", blocker.Reasons)
	}
}

func TestPreflightDuplicateKnownWakeLockFieldFailsClosed(t *testing.T) {
	agentDir := t.TempDir()
	path := wakeLockPath(agentDir)
	if err := os.WriteFile(path, []byte(`{"pid":1234,"PID":5678,"root":"/r"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	pf := agentLaunchPreflight{AgentDir: agentDir, Handle: "cto", Workstream: "w", Root: "/r", Force: true}
	blocker, err := pf.check(fakeProbe(map[int]bool{1234: false, 5678: false}, nil, time.Now()))
	if err != nil || blocker == nil || !strings.Contains(blocker.Error(), "duplicate known field") {
		t.Fatalf("ambiguous lock must fail closed: blocker=%v err=%v", blocker, err)
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatalf("ambiguous lock must be preserved: %v", statErr)
	}
}

// TestPreflightFreshPresenceWithCodexSeatStillBlocks: codex seats often
// have no captured AgentPID. The launch record exists but cannot prove the
// writer dead, so presence still blocks (conservative).
func TestPreflightFreshPresenceWithCodexSeatStillBlocks(t *testing.T) {
	agentDir := t.TempDir()
	writePresence(t, agentDir, presenceFile{
		Schema: 1, Handle: "cpo", Status: "active",
		LastSeen: time.Now().Add(-5 * time.Second),
	})
	if err := launch.Write(agentDir, launch.Record{
		Binary: "codex", Handle: "cpo", AgentPID: 0, StartedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	probe := fakeProbe(map[int]bool{}, nil, time.Now())
	pf := agentLaunchPreflight{
		AgentDir: agentDir, Handle: "cpo", Workstream: "w", Root: "/r", Binary: "codex",
	}
	blocker, err := pf.check(probe)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if blocker == nil {
		t.Fatal("no captured pid means we cannot prove writer dead; presence must still block")
	}
}

// TestPreflightFreshPresenceWithoutLockOrRecordStillBlocks: when neither
// .wake.lock nor launch.json exist on disk, we cannot prove the writer is
// dead. Presence keeps the conservative block.
func TestPreflightFreshPresenceWithoutLockOrRecordStillBlocks(t *testing.T) {
	agentDir := t.TempDir()
	writePresence(t, agentDir, presenceFile{
		Schema: 1, Handle: "qa", Status: "active",
		LastSeen: time.Now().Add(-5 * time.Second),
	})

	probe := fakeProbe(map[int]bool{}, nil, time.Now())
	pf := agentLaunchPreflight{
		AgentDir: agentDir, Handle: "qa", Workstream: "w", Root: "/r", Binary: "claude",
	}
	blocker, err := pf.check(probe)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if blocker == nil {
		t.Fatal("with no on-disk writer records, presence must still block (cannot prove writer dead)")
	}
}

// The fork-free liveness probe (signal/EPERM, KERN_PROCARGS2 / /proc parsing,
// ps-fallback retry) now lives in internal/procinfo; its tests moved there.
