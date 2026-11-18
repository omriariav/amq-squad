package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/buzz"
	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/status"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// C5a PR 1, the failing row first (design T/design-33x-c5a.md, row D1): a
// launch record a seat writes into its own agent directory
// (agents/<h>/extensions/io.github.omriariav.amq-squad/launch.json, which a
// Codex seat can write under AMQ v0.145.0's U2 token and a Claude or pi seat
// can write today) names no lead role for task show; the record the core
// staged for the launch (<root>/extensions/amq-squad/launch/<h>/launch.json,
// which no seat can write) does.
func TestC5aASeatWrittenLaunchRecordGrantsNoLeadRole(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AM_ROOT", root)
	t.Setenv("AM_ME", "")
	now := withFixedTaskNow(t)
	s, err := task.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	added, err := s.Add(task.AddInput{Title: "t", Class: "plan"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim(added.ID, "seat-a", nil, time.Hour, now); err != nil {
		t.Fatal(err)
	}
	next := func(t *testing.T, me string) []task.NextEntry {
		t.Helper()
		out, err := taskCLI(t, "show", added.ID, "--json", "--me", me)
		if err != nil {
			t.Fatal(err)
		}
		var env struct {
			Data map[string]json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal([]byte(out), &env); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
		var got []task.NextEntry
		if err := json.Unmarshal(env.Data["next"], &got); err != nil {
			t.Fatal(err)
		}
		return got
	}
	// the seat seat-b writes a record in its own agent directory naming
	// itself the lead
	forged := filepath.Join(root, "agents", "seat-b", "extensions", launch.LayerName)
	if err := os.MkdirAll(forged, 0o700); err != nil {
		t.Fatal(err)
	}
	raw, _ := launch.Marshal(launch.Record{Schema: launch.SchemaVersion, Handle: "seat-b", Root: root, Role: status.LeadRole, StartedAt: now})
	if err := os.WriteFile(filepath.Join(forged, launch.FileName), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := next(t, "seat-b"); !reflect.DeepEqual(got, []task.NextEntry{}) {
		t.Errorf("a launch record seat-b wrote in its own agent directory gave it entries: %+v", got)
	}
	// the core staged cto's launch as the lead
	staged := launch.StagedDir(root, "cto")
	if err := os.MkdirAll(staged, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := launch.WriteFile(filepath.Join(staged, launch.FileName), launch.Record{Schema: launch.SchemaVersion, Handle: "cto", Root: root, Role: status.LeadRole, StartedAt: now}); err != nil {
		t.Fatal(err)
	}
	want := []task.NextEntry{{Verb: "task cancel", Who: task.NextLead}, {Verb: "task release", Who: task.NextLead}}
	if got := next(t, "cto"); !reflect.DeepEqual(got, want) {
		t.Errorf("the record the core staged for cto's launch as the lead gave %+v, want %+v", got, want)
	}
}

// plantSeatRecord writes a launch record the way a seat can: into its own
// agent directory (agents/<h>/extensions/io.github.omriariav.amq-squad/).
func plantSeatRecord(t *testing.T, root string, rec launch.Record) {
	t.Helper()
	dir := filepath.Join(root, "agents", rec.Handle, "extensions", launch.LayerName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	raw, _ := launch.Marshal(rec)
	if err := os.WriteFile(filepath.Join(dir, launch.FileName), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

// stageSeatRecord writes the record the core stages for a seat's launch.
func stageSeatRecord(t *testing.T, root string, rec launch.Record) {
	t.Helper()
	if err := os.MkdirAll(launch.StagedDir(root, rec.Handle), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := launch.WriteFile(filepath.Join(launch.StagedDir(root, rec.Handle), launch.FileName), rec); err != nil {
		t.Fatal(err)
	}
}

// C5a PR 1, row D4 (the claim pool, internal/cli/task.go
// taskClaimSeatPool): a claim takes the pool the core staged for the seat's
// launch, and the seat counts live only on the launcher's proof ledger
// (proof.json, written by the core's proof server); a record the seat wrote
// in its own agent directory, naming another pool and a live exec, decides
// neither.
func TestC5aASeatWrittenLaunchRecordSetsNoClaimPool(t *testing.T) {
	root := t.TempDir()
	const pid, start = 4242, "start-4242"
	prevAlive, prevStart := taskClaimSeatAlive, taskClaimSeatStartToken
	taskClaimSeatAlive = func(p int) bool { return p == pid }
	taskClaimSeatStartToken = func(p int) (string, bool) { return start, p == pid }
	t.Cleanup(func() { taskClaimSeatAlive, taskClaimSeatStartToken = prevAlive, prevStart })
	// seat-a writes a record naming a codex pool and a live exec
	plantSeatRecord(t, root, launch.Record{Handle: "seat-a", Root: root, Runtime: "codex", Model: "gpt-x", LaunchID: "L1",
		Exec: &launch.Exec{Outcome: "exec", PID: pid, ProcessStart: start}})
	// nothing the core wrote names a live seat: no pool is taken from it
	if p, err := taskClaimSeatPool(root, "seat-a", nil); err != nil || p != nil {
		t.Errorf("a record seat-a wrote set the claim's pool: %+v %v", p, err)
	}
	opus := task.Pool{Runtime: "claude", Model: "opus", Effort: "high"}
	if p, err := taskClaimSeatPool(root, "seat-a", &opus); err != nil || p == nil || *p != opus {
		t.Errorf("a record seat-a wrote refused or changed the named pool: %+v %v", p, err)
	}
	// the core staged seat-a on claude:opus and its proof is open and live
	stageSeatRecord(t, root, launch.Record{Handle: "seat-a", Root: root, Runtime: "claude", Model: "opus", Effort: "high", LaunchID: "L1"})
	if err := launch.WriteProof(root, launch.ProofEntry{Handle: "seat-a", LaunchID: "L1", PID: pid, Start: start, At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if p, err := taskClaimSeatPool(root, "seat-a", nil); err != nil || p == nil || *p != opus {
		t.Errorf("the staged pool with an open proof: %+v %v, want %+v", p, err, opus)
	}
}

// C5a PR 1, row D5 (the guard's rules path, internal/cli/hook_guard_env.go
// protectedPath): the guard protects the rules source the core staged for
// the seat's launch; a record the seat wrote naming no rules source does
// not lift that protection.
func TestC5aASeatWrittenLaunchRecordLiftsNoRulesProtection(t *testing.T) {
	f := newHookFixture(t)
	source := filepath.Join(f.project, "custom", "squad-rules.md")
	mustWrite(t, source, "# rules\n")
	stageSeatRecord(t, f.root, launch.Record{Handle: "cto", Root: f.root, Session: "s1", RulesPath: source})
	plantSeatRecord(t, f.root, launch.Record{Handle: "cto", Root: f.root, Session: "s1"})
	if v := evaluateGuard(f.id(), guardCall{Tool: "Write", Args: map[string]string{"file_path": source}, CWD: f.repo}, f.now); v.ReasonCode != "protected_path" {
		t.Errorf("a write to the staged rules source after the seat wrote a record without it: %s %s", v.Verdict, v.ReasonCode)
	}
}

// C5a PR 1, row D6 (the wait-posture read, internal/state/snapshot.go via
// internal/cli/wait_posture.go): the lead's blocking-wait guard finds a
// session only from the records the core staged for its seats; a record a
// seat wrote in its own agent directory does not make an absent session
// present (the guard refuses a wait for a session it cannot find).
func TestC5aASeatWrittenLaunchRecordMakesNoSessionPresent(t *testing.T) {
	f := newHookFixture(t)
	rec := launch.Record{Handle: "cto", Root: f.root, Session: "s1", Role: "cto", CWD: f.project}
	plantSeatRecord(t, f.root, rec)
	snap, err := defaultWaitPostureSnapshot(f.project, "user")
	if err != nil {
		t.Fatal(err)
	}
	if _, found := callerOpenGates(snap, "", "s1", f.root, "cto"); found {
		t.Errorf("a record cto wrote in its own agent directory made session s1 present to the wait-posture guard")
	}
	stageSeatRecord(t, f.root, rec)
	snap, err = defaultWaitPostureSnapshot(f.project, "user")
	if err != nil {
		t.Fatal(err)
	}
	if _, found := callerOpenGates(snap, "", "s1", f.root, "cto"); !found {
		t.Errorf("the record the core staged for cto's launch did not make session s1 present")
	}
}

// C5a PR 1, the guard row (exec/ is safe): a Claude or pi seat's write into
// the staged directory, where seat-exec writes the exec record and the
// snapshots and the launcher its proof ledger, is refused by the guard,
// for the seat's own launch and another seat's.
func TestC5aTheGuardRefusesASeatWriteUnderTheStagedDirectory(t *testing.T) {
	f := newHookFixture(t)
	for _, p := range []string{
		filepath.Join(launch.StagedDir(f.root, "cto"), "exec", launch.FileName),
		filepath.Join(filepath.Join(launch.StagedDir(f.root, "cto"), "exec"), launch.RoleFileName),
		filepath.Join(filepath.Join(launch.StagedDir(f.root, "cto"), "exec"), "rules.md"),
		filepath.Join(launch.StagedDir(f.root, "cto"), launch.FileName),
		launch.ProofLedgerPath(f.root, "cto"),
		filepath.Join(launch.StagedDir(f.root, "qa"), "exec", launch.FileName),
		filepath.Join(launch.StagedDir(f.root, "qa"), launch.FileName),
	} {
		for _, tool := range []string{"Write", "Edit"} {
			if v := evaluateGuard(f.id(), guardCall{Tool: tool, Args: map[string]string{"file_path": p}, CWD: f.repo}, f.now); v.ReasonCode != "protected_path" {
				t.Errorf("%s %s: %s %s", tool, p, v.Verdict, v.ReasonCode)
			}
		}
		for _, cmd := range []string{"echo x > " + p, "echo x | tee " + p, "cp /etc/hosts " + p, "mv /tmp/x " + p} {
			if v := evaluateGuard(f.id(), guardCall{Tool: "Bash", Args: map[string]string{"command": cmd}, CWD: f.repo}, f.now); v.Verdict == "allow" {
				t.Errorf("Bash %q: %s %s", cmd, v.Verdict, v.ReasonCode)
			}
		}
	}
}

// C5a PR 1, the presence row: AMQ's presence.json and .wake.dialog in a
// seat's agent directory decide nothing: a seat with a fresh active presence
// and a dialog marker, staged as an implementer with no proof, gets no lead
// entries, no claim pool and no lead route.
func TestC5aPresenceAndTheWakeDialogDecideNothing(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AM_ROOT", root)
	t.Setenv("AM_ME", "")
	now := withFixedTaskNow(t)
	s, err := task.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	added, err := s.Add(task.AddInput{Title: "t", Class: "plan"}, now)
	if err != nil {
		t.Fatal(err)
	}
	stageSeatRecord(t, root, launch.Record{Handle: "worker", Root: root, Role: "implementer", Runtime: "claude", Model: "opus", LaunchID: "L1"})
	dir := filepath.Join(root, "agents", "worker")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(dir, "presence.json"), fmt.Sprintf(`{"schema":1,"handle":"worker","status":"active","last_seen":%q}`, now.Format(time.RFC3339)))
	mustWrite(t, filepath.Join(dir, ".wake.dialog"), "dialog\n")
	out, err := taskCLI(t, "show", added.ID, "--json", "--me", "worker")
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatal(err)
	}
	var next []task.NextEntry
	if err := json.Unmarshal(env.Data["next"], &next); err != nil || len(next) != 0 {
		t.Errorf("presence and the dialog marker gave entries: %s %v", env.Data["next"], err)
	}
	if p, err := taskClaimSeatPool(root, "worker", nil); err != nil || p != nil {
		t.Errorf("presence made the seat live for the claim pool: %+v %v", p, err)
	}
	if d, err := (buzz.LaunchRecordLeads{Roles: buzz.DefaultLeadRoles}).Lead(root, "worker"); err == nil {
		t.Errorf("presence routed the owner to an implementer: %+v", d)
	}
}

// C5a PR 1, row D4 (liveness): a claim takes the staged pool only while the
// launcher's proof ledger holds an open proof for the staged launch, by this
// seat, whose pid runs with the proven start stamp.
func TestC5aClaimLivenessIsTheOpenProofOfTheStagedLaunch(t *testing.T) {
	const pid, start = 4242, "start-4242"
	opus := task.Pool{Runtime: "claude", Model: "opus", Effort: "high"}
	closed := time.Now()
	for _, tc := range []struct {
		name  string
		proof *launch.ProofEntry
		alive bool
		stamp string
		live  bool
	}{
		{"open proof of the staged launch", &launch.ProofEntry{Handle: "seat-a", LaunchID: "L1", PID: pid, Start: start}, true, start, true},
		{"no proof", nil, true, start, false},
		{"closed proof", &launch.ProofEntry{Handle: "seat-a", LaunchID: "L1", PID: pid, Start: start, ClosedAt: &closed}, true, start, false},
		{"proof of an earlier launch", &launch.ProofEntry{Handle: "seat-a", LaunchID: "L0", PID: pid, Start: start}, true, start, false},
		{"proof naming another seat", &launch.ProofEntry{Handle: "seat-b", LaunchID: "L1", PID: pid, Start: start}, true, start, false},
		{"proof without a start", &launch.ProofEntry{Handle: "seat-a", LaunchID: "L1", PID: pid}, true, "", false},
		{"pid gone", &launch.ProofEntry{Handle: "seat-a", LaunchID: "L1", PID: pid, Start: start}, false, start, false},
		{"pid reused", &launch.ProofEntry{Handle: "seat-a", LaunchID: "L1", PID: pid, Start: start}, true, "start-other", false},
		{"staged launch without an id", &launch.ProofEntry{Handle: "seat-a", PID: pid, Start: start}, true, start, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			prevAlive, prevStart := taskClaimSeatAlive, taskClaimSeatStartToken
			taskClaimSeatAlive = func(p int) bool { return tc.alive && p == pid }
			taskClaimSeatStartToken = func(p int) (string, bool) { return tc.stamp, p == pid }
			t.Cleanup(func() { taskClaimSeatAlive, taskClaimSeatStartToken = prevAlive, prevStart })
			launchID := "L1"
			if tc.name == "staged launch without an id" {
				launchID = ""
			}
			stageSeatRecord(t, root, launch.Record{Handle: "seat-a", Root: root, Runtime: "claude", Model: "opus", Effort: "high", LaunchID: launchID})
			if tc.proof != nil {
				e := *tc.proof
				e.At = time.Now()
				// the ledger file is the seat's own (launch/seat-a/proof.json)
				// whatever handle the entry names
				if err := os.MkdirAll(launch.StagedDir(root, "seat-a"), 0o700); err != nil {
					t.Fatal(err)
				}
				raw, _ := json.Marshal(e)
				if err := os.WriteFile(launch.ProofLedgerPath(root, "seat-a"), raw, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			p, err := taskClaimSeatPool(root, "seat-a", nil)
			if err != nil {
				t.Fatal(err)
			}
			if live := p != nil && *p == opus; live != tc.live {
				t.Errorf("pool %+v, want live=%v", p, tc.live)
			}
		})
	}
}

// C5a PR 1 (the context hook, internal/cli/hook_context.go): the seat's
// context names the tree and the role from seat-exec's exec record and role
// snapshot under the staged directory, else from what the core staged; a
// record and a role file the seat wrote in its own agent directory change
// neither.
func TestC5aASeatWrittenRecordAndRoleChangeNoContext(t *testing.T) {
	root := t.TempDir()
	id := hookIdentity{Handle: "cto", Root: root, Session: "s", AgentDir: filepath.Join(root, "agents", "cto")}
	staged := launch.StagedDir(root, "cto")
	stageSeatRecord(t, root, launch.Record{Handle: "cto", Root: root, LaunchID: "L1", Worktree: "/w/staged"})
	mustWrite(t, filepath.Join(staged, launch.RoleFileName), "staged role\n")
	plantSeatRecord(t, root, launch.Record{Handle: "cto", Root: root, Worktree: "/w/planted"})
	mustWrite(t, filepath.Join(id.AgentDir, "extensions", launch.LayerName, launch.RoleFileName), "planted role\n")
	mustWrite(t, filepath.Join(id.AgentDir, "role.md"), "planted legacy role\n")
	if d := buildHookContext(id, "", time.Now()); d.Worktree != "/w/staged" || d.RoleText != "staged role\n" {
		t.Errorf("before seat-exec: worktree %q role %q, want the staged ones", d.Worktree, d.RoleText)
	}
	// an earlier launch's exec record and role snapshot, left by a restage
	// whose seat-exec has not run (Codex pass 1, finding 3)
	mustWrite(t, filepath.Join(staged, "exec", launch.FileName), mustMarshal(t, launch.Record{Handle: "cto", Root: root, LaunchID: "L0", Worktree: "/w/exec-L0"}))
	mustWrite(t, filepath.Join(staged, "exec", launch.RoleFileName), "exec role L0\n")
	if d := buildHookContext(id, "", time.Now()); d.Worktree != "/w/staged" || d.RoleText != "staged role\n" {
		t.Errorf("an earlier launch's exec record: worktree %q role %q, want the staged ones", d.Worktree, d.RoleText)
	}
	mustWrite(t, filepath.Join(staged, "exec", launch.FileName), mustMarshal(t, launch.Record{Handle: "cto", Root: root, LaunchID: "L1", Worktree: "/w/exec"}))
	mustWrite(t, filepath.Join(staged, "exec", launch.RoleFileName), "exec role\n")
	if d := buildHookContext(id, "", time.Now()); d.Worktree != "/w/exec" || d.RoleText != "exec role\n" {
		t.Errorf("after seat-exec: worktree %q role %q, want the exec ones", d.Worktree, d.RoleText)
	}
}

func mustMarshal(t *testing.T, rec launch.Record) string {
	t.Helper()
	raw, err := launch.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// C5a PR 1, row D6 (Codex pass 2, finding 2): a staged-looking record placed
// under another directory of the AMQ base root (a seat's worktree there, or
// a Claude seat's Write outside its own session root's extensions/) never
// speaks for the session: one naming the real session root is not read, and
// one naming its own directory as root never takes the session's root.
func TestC5aARecordElsewhereInTheBaseRootDecidesNoSession(t *testing.T) {
	f := newHookFixture(t)
	base := filepath.Dir(f.root)
	fake := filepath.Join(base, "fake-profile", "fake-session")
	plant := func(root, handle string, rec launch.Record) {
		t.Helper()
		dir := filepath.Join(root, "extensions", "amq-squad", "launch", handle)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := launch.WriteFile(filepath.Join(dir, launch.FileName), rec); err != nil {
			t.Fatal(err)
		}
	}
	// a record elsewhere naming the real session and its root: read as
	// nobody's, so the absent session stays absent
	plant(fake, "cto", launch.Record{Handle: "cto", Root: f.root, Session: "s1", Role: "cto", CWD: f.project})
	snap, err := defaultWaitPostureSnapshot(f.project, "user")
	if err != nil {
		t.Fatal(err)
	}
	if _, found := callerOpenGates(snap, "", "s1", f.root, "cto"); found {
		t.Errorf("a record under %s naming session s1 at its root made the session present", fake)
	}
	// the core stages cto; a record elsewhere naming its own directory as
	// root, and the same session, must not take the session's root
	stageSeatRecord(t, f.root, launch.Record{Handle: "cto", Root: f.root, Session: "s1", Role: "cto", CWD: f.project})
	plant(fake, "cto", launch.Record{Handle: "cto", Root: fake, Session: "s1", Role: "cto", CWD: f.project})
	snap, err = defaultWaitPostureSnapshot(f.project, "user")
	if err != nil {
		t.Fatal(err)
	}
	if _, found := callerOpenGates(snap, "", "s1", f.root, "cto"); !found {
		t.Errorf("a record under %s naming its own root displaced session s1's root %s", fake, f.root)
	}
}

// C5a PR 1 (Codex pass 3, finding 1): the guard refuses a seat's write into
// any session's extensions/amq-squad/ under the AMQ base root, not only its
// own session root's: another session's staged records decide that
// session's routing and roles.
func TestC5aTheGuardRefusesAWriteIntoAnotherSessionsStagedDirectory(t *testing.T) {
	f := newHookFixture(t)
	base := filepath.Dir(f.root)
	for _, p := range []string{
		filepath.Join(launch.StagedDir(filepath.Join(base, "s2"), "worker"), launch.FileName),
		filepath.Join(launch.StagedDir(filepath.Join(base, "prof", "s3"), "worker"), "proof.json"),
		filepath.Join(base, "s2", "extensions", "amq-squad", "grants.yaml"),
		filepath.Join(base, "extensions", "amq-squad", "launch", "x", launch.FileName),
	} {
		for _, call := range []guardCall{
			{Tool: "Write", Args: map[string]string{"file_path": p}, CWD: f.repo},
			{Tool: "Bash", Args: map[string]string{"command": "cp /tmp/x " + p}, CWD: f.repo},
		} {
			if v := evaluateGuard(f.id(), call, f.now); v.ReasonCode != "protected_path" {
				t.Errorf("%s %s: %s %s", call.Tool, p, v.Verdict, v.ReasonCode)
			}
		}
	}
	// the seat's own mailbox and pi's tmp under it stay writable
	for _, p := range []string{filepath.Join(f.agentDir, "extensions", launch.LayerName, "tmp", "x"), filepath.Join(base, "s2", "agents", "w", "notes.md")} {
		if v := evaluateGuard(f.id(), guardCall{Tool: "Write", Args: map[string]string{"file_path": p}, CWD: f.repo}, f.now); v.Verdict != "allow" {
			t.Errorf("Write %s: %s %s", p, v.Verdict, v.ReasonCode)
		}
	}
}

// C5a PR 1 (Codex pass 3, finding 2): a seat never runs seat-exec: AMQ's
// launch runs the wrapper, and a seat that ran it would have it write the
// exec record (a refusal the launcher reads) for its launch.
func TestC5aTheGuardRefusesASeatRunningSeatExec(t *testing.T) {
	f := newHookFixture(t)
	for _, cmd := range []string{"amq-squad seat-exec --seat cto /bin/false", "/opt/amq-squad/bin/amq-squad seat-exec --seat qa -- true", "env AM_ME=qa amq-squad seat-exec --seat qa /bin/false"} {
		if v := evaluateGuard(f.id(), guardCall{Tool: "Bash", Args: map[string]string{"command": cmd}, CWD: f.repo}, f.now); v.ReasonCode != "harness_only" {
			t.Errorf("%q: %s %s", cmd, v.Verdict, v.ReasonCode)
		}
	}
}

// C5a PR 1 (Codex pass 4, test strength): with a custom AMQ base root and
// profile sessions (<base>/<profile>/<session>), the guard takes the base
// root from the staged record, so a sibling profile's sessions are covered,
// not only the seat's own profile directory's.
func TestC5aTheGuardCoversSiblingProfilesUnderACustomBaseRoot(t *testing.T) {
	f := newHookFixture(t)
	base := filepath.Join(filepath.Dir(f.project), "custom-base")
	root := filepath.Join(base, "profile-a", "s1")
	id := hookIdentity{Handle: "cto", Root: root, Session: "s1", AgentDir: filepath.Join(root, "agents", "cto")}
	mustMkdir(t, id.AgentDir)
	stageSeatRecord(t, root, launch.Record{Handle: "cto", Root: root, BaseRoot: base, Session: "s1"})
	t.Setenv("AM_ROOT", root)
	t.Setenv("AM_BASE_ROOT", "")
	for _, p := range []string{
		filepath.Join(launch.StagedDir(filepath.Join(base, "profile-b", "s2"), "worker"), launch.FileName),
		filepath.Join(launch.StagedDir(filepath.Join(base, "s3"), "worker"), "proof.json"),
	} {
		if v := evaluateGuard(id, guardCall{Tool: "Write", Args: map[string]string{"file_path": p}, CWD: f.repo}, f.now); v.ReasonCode != "protected_path" {
			t.Errorf("Write %s: %s %s", p, v.Verdict, v.ReasonCode)
		}
	}
}
