package launch

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestWriteReadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	in := Record{
		CWD:              "/some/project",
		Binary:           "claude",
		Argv:             []string{"--flag", "value"},
		Session:          "stream1",
		SharedWorkstream: true,
		Conversation:     "drive-fix",
		Handle:           "cpo",
		Role:             "cpo",
		Root:             dir,
		BaseRoot:         filepath.Dir(dir),
		RootSource:       "project_amqrc",
		AMQVersion:       "0.34.0",
		NoGitignore:      true,
		WakeInjectVia:    "/opt/amq-inject",
		WakeInjectArgs:   []string{"--pane", "%42"},
		WakePID:          1234,
		StartedAt:        time.Now().UTC().Truncate(time.Second),
	}
	if err := Write(dir, in); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := os.Stat(Path(dir)); err != nil {
		t.Fatalf("Write did not create extension launch record: %v", err)
	}
	if _, err := os.Stat(LegacyPath(dir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Write created legacy launch record, err=%v", err)
	}

	out, err := Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	if out.Schema != SchemaVersion {
		t.Errorf("Schema = %d, want %d", out.Schema, SchemaVersion)
	}
	// Write sets Schema, so zero out in.Schema for comparison of other fields.
	in.Schema = SchemaVersion
	if out.CWD != in.CWD || out.Binary != in.Binary || out.Session != in.Session ||
		out.SharedWorkstream != in.SharedWorkstream ||
		out.Conversation != in.Conversation ||
		out.Handle != in.Handle || out.Role != in.Role || out.Root != in.Root ||
		out.BaseRoot != in.BaseRoot || out.RootSource != in.RootSource ||
		out.AMQVersion != in.AMQVersion || out.WakeInjectVia != in.WakeInjectVia ||
		out.NoGitignore != in.NoGitignore || out.WakePID != in.WakePID {
		t.Errorf("round-trip mismatch: got %+v, want %+v", out, in)
	}
	if !reflect.DeepEqual(out.WakeInjectArgs, in.WakeInjectArgs) {
		t.Errorf("WakeInjectArgs = %v, want %v", out.WakeInjectArgs, in.WakeInjectArgs)
	}
	if len(out.Argv) != len(in.Argv) {
		t.Fatalf("Argv len mismatch: %v vs %v", out.Argv, in.Argv)
	}
	for i := range in.Argv {
		if out.Argv[i] != in.Argv[i] {
			t.Errorf("Argv[%d] = %q, want %q", i, out.Argv[i], in.Argv[i])
		}
	}
	if !out.StartedAt.Equal(in.StartedAt) {
		t.Errorf("StartedAt = %v, want %v", out.StartedAt, in.StartedAt)
	}
}

// TestWakeInjectCmdAdditiveBackCompat proves the #283 WakeInjectCmd field is
// additive: an older record JSON without wake_inject_cmd loads with an empty
// value, an empty value is omitted on write, and a set value round-trips.
func TestWakeInjectCmdAdditiveBackCompat(t *testing.T) {
	// Older record (no wake_inject_cmd key) must load with empty WakeInjectCmd.
	legacyJSON := `{"schema":1,"cwd":"/p","binary":"codex","session":"s","handle":"cto","root":"/r","started_at":"2026-06-30T00:00:00Z"}`
	var legacy Record
	if err := json.Unmarshal([]byte(legacyJSON), &legacy); err != nil {
		t.Fatalf("older record must still load: %v", err)
	}
	if legacy.WakeInjectCmd != "" {
		t.Fatalf("missing wake_inject_cmd must decode empty, got %q", legacy.WakeInjectCmd)
	}

	// Empty value is omitted (omitempty), so no key appears for older writers.
	emptyRaw, err := json.Marshal(Record{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(emptyRaw), "wake_inject_cmd") {
		t.Fatalf("empty WakeInjectCmd must be omitted from JSON: %s", emptyRaw)
	}

	// A set value round-trips.
	setRaw, err := json.Marshal(Record{WakeInjectCmd: "drain now"})
	if err != nil {
		t.Fatal(err)
	}
	var back Record
	if err := json.Unmarshal(setRaw, &back); err != nil {
		t.Fatal(err)
	}
	if back.WakeInjectCmd != "drain now" {
		t.Fatalf("WakeInjectCmd round-trip = %q, want %q", back.WakeInjectCmd, "drain now")
	}
}

// TestSymphonyAdditiveBackCompat proves the #336 Symphony flag is additive:
// older launch records without the key decode false, false omits the key, and
// true round-trips for explicit restore/replay.
func TestSymphonyAdditiveBackCompat(t *testing.T) {
	legacyJSON := `{"schema":1,"cwd":"/p","binary":"codex","session":"s","handle":"cto","root":"/r","started_at":"2026-06-30T00:00:00Z"}`
	var legacy Record
	if err := json.Unmarshal([]byte(legacyJSON), &legacy); err != nil {
		t.Fatalf("older record must still load: %v", err)
	}
	if legacy.Symphony {
		t.Fatalf("missing symphony must decode false")
	}

	emptyRaw, err := json.Marshal(Record{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(emptyRaw), "symphony") {
		t.Fatalf("false Symphony must be omitted from JSON: %s", emptyRaw)
	}

	setRaw, err := json.Marshal(Record{Symphony: true})
	if err != nil {
		t.Fatal(err)
	}
	var back Record
	if err := json.Unmarshal(setRaw, &back); err != nil {
		t.Fatal(err)
	}
	if !back.Symphony {
		t.Fatalf("Symphony true did not round-trip")
	}
}

func TestWriteReadTmuxMetadata(t *testing.T) {
	dir := t.TempDir()
	in := Record{
		CWD:       "/some/project",
		Binary:    "codex",
		Handle:    "cto",
		Role:      "cto",
		Root:      dir,
		StartedAt: time.Now().UTC().Truncate(time.Second),
		Tmux: &TmuxInfo{
			Session:    "main",
			WindowID:   "@42",
			WindowName: "amq-squad-issue-96",
			PaneID:     "%265",
			Target:     "current-window",
		},
	}
	if err := Write(dir, in); err != nil {
		t.Fatalf("Write: %v", err)
	}
	out, err := Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if out.Tmux == nil {
		t.Fatal("tmux metadata lost on round-trip")
	}
	if *out.Tmux != *in.Tmux {
		t.Fatalf("tmux round-trip mismatch: got %+v, want %+v", *out.Tmux, *in.Tmux)
	}

	// The on-disk JSON must nest the runtime identity under "tmux" with the
	// exact field names the NOC contract depends on.
	raw, err := os.ReadFile(Path(dir))
	if err != nil {
		t.Fatal(err)
	}
	var probe struct {
		Tmux *TmuxInfo `json:"tmux"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if probe.Tmux == nil || probe.Tmux.PaneID != "%265" {
		t.Fatalf("json tmux block missing/wrong: %s", raw)
	}
}

func TestReadLegacyRecordHasNilTmux(t *testing.T) {
	// A pre-1.5 record (no "tmux" key) must read back with a nil Tmux pointer,
	// so clients detect runtime-control availability by presence, not schema.
	dir := t.TempDir()
	legacy := `{"schema":1,"cwd":"/p","binary":"codex","handle":"cto","root":"` + dir + `","started_at":"2026-01-01T00:00:00Z"}`
	if err := os.MkdirAll(filepath.Dir(Path(dir)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path(dir), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if out.Tmux != nil {
		t.Fatalf("legacy record should have nil Tmux, got %+v", out.Tmux)
	}
}

func TestReadMissing(t *testing.T) {
	dir := t.TempDir()
	_, err := Read(dir)
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("err = %v, want ErrNotExist", err)
	}
}

func TestReadFallsBackToLegacyPath(t *testing.T) {
	dir := t.TempDir()
	rec := Record{
		Schema:  SchemaVersion,
		CWD:     "/some/project",
		Binary:  "codex",
		Session: "stream1",
		Handle:  "cto",
		Role:    "cto",
		Root:    "/some/project/.agent-mail/stream1",
	}
	b, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(LegacyPath(dir), b, 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := Read(dir)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.Handle != "cto" || got.Root != rec.Root {
		t.Fatalf("Read = %+v, want legacy record %+v", got, rec)
	}
	if ExistingPath(dir) != LegacyPath(dir) {
		t.Fatalf("ExistingPath = %q, want legacy path %q", ExistingPath(dir), LegacyPath(dir))
	}
}

func TestScanFindsRecordsAcrossSessions(t *testing.T) {
	project := t.TempDir()
	// Simulate two sessions, one agent each.
	makeAgent := func(session, handle, role string) {
		dir := filepath.Join(project, ".agent-mail", session, "agents", handle)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		rec := Record{
			CWD:       project,
			Binary:    "claude",
			Session:   session,
			Handle:    handle,
			Role:      role,
			Root:      filepath.Join(project, ".agent-mail", session),
			StartedAt: time.Now().UTC(),
		}
		if err := Write(dir, rec); err != nil {
			t.Fatal(err)
		}
	}
	makeAgent("stream1", "cpo", "cpo")
	makeAgent("stream2", "fullstack", "fullstack")

	// Add a noise file that shouldn't be picked up.
	if err := os.WriteFile(filepath.Join(project, ".agent-mail", "not-a-launch.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	recs, err := Scan(project)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("Scan returned %d records, want 2", len(recs))
	}

	roles := []string{recs[0].Role, recs[1].Role}
	sort.Strings(roles)
	if roles[0] != "cpo" || roles[1] != "fullstack" {
		t.Errorf("roles = %v, want [cpo fullstack]", roles)
	}
}

func TestScanEntriesIncludesAgentDir(t *testing.T) {
	project := t.TempDir()
	agentDir := filepath.Join(project, ".agent-mail", "stream1", "agents", "cto")
	rec := Record{
		CWD:       project,
		Binary:    "codex",
		Session:   "stream1",
		Handle:    "cto",
		Role:      "cto",
		Root:      filepath.Join(project, ".agent-mail", "stream1"),
		StartedAt: time.Now().UTC(),
	}
	if err := Write(agentDir, rec); err != nil {
		t.Fatal(err)
	}

	entries, err := ScanEntries(project)
	if err != nil {
		t.Fatalf("ScanEntries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("ScanEntries returned %d records, want 1", len(entries))
	}
	if entries[0].AgentDir != agentDir {
		t.Errorf("AgentDir = %q, want %q", entries[0].AgentDir, agentDir)
	}
	if entries[0].Record.Handle != "cto" {
		t.Errorf("Record.Handle = %q, want cto", entries[0].Record.Handle)
	}
}

func TestScanEntriesInRootFindsExtensionRecordsUnderCustomBaseRoot(t *testing.T) {
	project := t.TempDir()
	baseRoot := filepath.Join(t.TempDir(), "mail")
	agentDir := filepath.Join(baseRoot, "stream1", "agents", "cto")
	rec := Record{
		CWD:      project,
		Binary:   "codex",
		Session:  "stream1",
		Handle:   "cto",
		Role:     "cto",
		Root:     filepath.Join(baseRoot, "stream1"),
		BaseRoot: baseRoot,
	}
	if err := Write(agentDir, rec); err != nil {
		t.Fatal(err)
	}

	entries, err := ScanEntriesInRoot(project, baseRoot)
	if err != nil {
		t.Fatalf("ScanEntriesInRoot: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("ScanEntriesInRoot returned %d records, want 1", len(entries))
	}
	if entries[0].AgentDir != agentDir {
		t.Fatalf("AgentDir = %q, want %q", entries[0].AgentDir, agentDir)
	}
	if entries[0].Record.BaseRoot != baseRoot {
		t.Fatalf("BaseRoot = %q, want %q", entries[0].Record.BaseRoot, baseRoot)
	}
}

func TestScanLegacyEntriesFromPresence(t *testing.T) {
	project := t.TempDir()
	agentDir := filepath.Join(project, ".agent-mail", "stream1", "agents", "claude")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	lastSeen := "2026-04-25T05:48:47Z"
	if err := os.WriteFile(filepath.Join(agentDir, "presence.json"), []byte(`{"last_seen":"`+lastSeen+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	entries, err := ScanLegacyEntries(project)
	if err != nil {
		t.Fatalf("ScanLegacyEntries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("ScanLegacyEntries returned %d records, want 1", len(entries))
	}
	rec := entries[0].Record
	if rec.CWD != project || rec.Binary != "claude" || rec.Handle != "claude" ||
		rec.Session != "stream1" || rec.Root != filepath.Join(project, ".agent-mail", "stream1") {
		t.Errorf("unexpected legacy record: %+v", rec)
	}
	if entries[0].Source != "amq history" {
		t.Errorf("Source = %q, want amq history", entries[0].Source)
	}
	if got := rec.StartedAt.Format(time.RFC3339); got != lastSeen {
		t.Errorf("StartedAt = %q, want %q", got, lastSeen)
	}
}

func TestScanLegacyEntriesFromBaseRootHistory(t *testing.T) {
	project := t.TempDir()
	sentDir := filepath.Join(project, ".agent-mail", "agents", "codex", "outbox", "sent")
	if err := os.MkdirAll(sentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sentDir, "message.md"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	entries, err := ScanLegacyEntries(project)
	if err != nil {
		t.Fatalf("ScanLegacyEntries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("ScanLegacyEntries returned %d records, want 1", len(entries))
	}
	rec := entries[0].Record
	if rec.Binary != "codex" || rec.Handle != "codex" || rec.Session != "" ||
		rec.Root != filepath.Join(project, ".agent-mail") {
		t.Errorf("unexpected base-root legacy record: %+v", rec)
	}
}

func TestScanLegacyEntriesSkipsUnknownBinaryHandle(t *testing.T) {
	project := t.TempDir()
	agentDir := filepath.Join(project, ".agent-mail", "stream1", "agents", "fullstack")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "presence.json"), []byte(`{"last_seen":"2026-04-25T05:48:47Z"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	entries, err := ScanLegacyEntries(project)
	if err != nil {
		t.Fatalf("ScanLegacyEntries: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("ScanLegacyEntries returned %d records, want 0: %+v", len(entries), entries)
	}
}

func TestScanLegacyEntriesInfersRoleFromBinaryPrefixedHandle(t *testing.T) {
	project := t.TempDir()
	agentDir := filepath.Join(project, ".agent-mail", "stream1", "agents", "claude-qa")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	receiptDir := filepath.Join(agentDir, "receipts")
	if err := os.MkdirAll(receiptDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(receiptDir, "receipt.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	entries, err := ScanLegacyEntries(project)
	if err != nil {
		t.Fatalf("ScanLegacyEntries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("ScanLegacyEntries returned %d records, want 1", len(entries))
	}
	rec := entries[0].Record
	if rec.Binary != "claude" || rec.Handle != "claude-qa" || rec.Role != "qa" {
		t.Errorf("unexpected inferred record: %+v", rec)
	}
}

func TestScanRestorableEntriesDedupesLegacyWhenLaunchExists(t *testing.T) {
	project := t.TempDir()
	agentDir := filepath.Join(project, ".agent-mail", "stream1", "agents", "claude")
	rec := Record{
		CWD:     project,
		Binary:  "claude",
		Session: "stream1",
		Handle:  "claude",
		Root:    filepath.Join(project, ".agent-mail", "stream1"),
	}
	if err := Write(agentDir, rec); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "presence.json"), []byte(`{"last_seen":"2026-04-25T05:48:47Z"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	entries, err := ScanRestorableEntries(project)
	if err != nil {
		t.Fatalf("ScanRestorableEntries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("ScanRestorableEntries returned %d records, want 1", len(entries))
	}
	if entries[0].Source != FileName {
		t.Errorf("Source = %q, want %s", entries[0].Source, FileName)
	}
}

func TestScanMatchesBaseRootLayout(t *testing.T) {
	// Base-root agents (no session) live at .agent-mail/agents/<handle>,
	// not under .agent-mail/<session>/agents/<handle>. Scan must find both.
	project := t.TempDir()
	dir := filepath.Join(project, ".agent-mail", "agents", "claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := Record{
		CWD:       project,
		Binary:    "claude",
		Handle:    "claude",
		Role:      "fullstack",
		Root:      filepath.Join(project, ".agent-mail"),
		StartedAt: time.Now().UTC(),
	}
	if err := Write(dir, rec); err != nil {
		t.Fatal(err)
	}
	recs, err := Scan(project)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("Scan returned %d records, want 1", len(recs))
	}
	if recs[0].Handle != "claude" || recs[0].Role != "fullstack" {
		t.Errorf("unexpected record: %+v", recs[0])
	}
}

func TestScanDedupesAcrossPatterns(t *testing.T) {
	// Mix session and base-root layouts in one project. Both should be
	// returned, with no duplicates.
	project := t.TempDir()
	mk := func(path string, rec Record) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := Write(filepath.Dir(path), rec); err != nil {
			t.Fatal(err)
		}
	}
	mk(filepath.Join(project, ".agent-mail", "collab", "agents", "cto", FileName),
		Record{Handle: "cto", Role: "cto", Binary: "codex"})
	mk(filepath.Join(project, ".agent-mail", "agents", "claude", FileName),
		Record{Handle: "claude", Role: "fullstack", Binary: "claude"})
	recs, err := Scan(project)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 {
		t.Fatalf("want 2 records (cto + claude), got %d: %+v", len(recs), recs)
	}
}

func TestScanEmptyProject(t *testing.T) {
	recs, err := Scan(t.TempDir())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(recs) != 0 {
		t.Errorf("Scan on empty project returned %d records", len(recs))
	}
}
