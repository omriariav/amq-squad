package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/gatestore"
	"github.com/omriariav/amq-squad/v3/internal/gateverify"
	"github.com/omriariav/amq-squad/v3/internal/nostr"
	"github.com/omriariav/amq-squad/v3/internal/team"
)

// Brief 31-3, PR 1: the verbs and R55's three doors. Every test runs under
// TestMain's temp HOME, so the solo root is a temp directory; the process
// tree of door (a) is a fake one through the procinfo seams.

// soloTeamPlan is a plan for a two-seat squad: the pi lead and a Claude
// implementer (a staged custom role).
func soloTeamPlan() team.LeadPlan {
	return team.LeadPlan{Goal: "ship the parser", Session: "s1", Lead: "lead",
		Seats: []team.PlanSeat{{Handle: "lead", Role: "lead", Runtime: "pi"}, {Handle: "impl-1", Role: "implementer", Runtime: "claude", Model: "sonnet", Effort: "medium"}},
		Files: []string{"internal/parser/"}, Tasks: []team.PlanTask{{Title: "write the parser", Class: "implement", Files: []string{"internal/parser/parse.go"}}}}
}

// soloProject is a repository with the implementer role staged and no
// squad.yaml; it returns the project and its solo root.
func soloProject(t *testing.T) (string, string) {
	t.Helper()
	proj, _ := filepath.EvalSymlinks(t.TempDir())
	mustMkdir(t, filepath.Join(proj, ".git"))
	mustWrite(t, team.CustomRolePath(proj, "implementer"), "# Role: implementer\n\nYou implement.\n")
	root, err := soloRootOf(proj)
	if err != nil {
		t.Fatal(err)
	}
	return proj, root
}

// proposeSoloPlan runs team propose --from PLAN.json and returns its JSON.
func proposeSoloPlan(t *testing.T, proj string, plan team.LeadPlan) map[string]any {
	t.Helper()
	raw, _ := json.Marshal(plan)
	file := filepath.Join(t.TempDir(), "plan.json")
	mustWrite(t, file, string(raw))
	stdout, _, err := captureOutput(t, func() error { return runTeam([]string{"propose", "--from", file, "--project", proj, "--json"}) })
	if err != nil {
		t.Fatalf("propose: %v\n%s", err, stdout)
	}
	var env struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &env); err != nil {
		t.Fatalf("propose json: %v\n%s", err, stdout)
	}
	return env.Data
}

// Row 3 (the verb's half): team propose --from PLAN renders squad.yaml from
// the plan, validates it against the owner grant, prints the plan with the
// digest and its prefix, and writes nothing in the repository; the one
// write is the proposal beside the records. A plan that widens the grant is
// refused as a squad.yaml draft is.
func TestTeamProposeFromAPlanPrintsThePlanAndDigestAndWritesOnlyTheProposal(t *testing.T) {
	proj, root := soloProject(t)
	before := snapshotTree(t, proj)
	d := proposeSoloPlan(t, proj, soloTeamPlan())
	digest, _ := d["digest"].(string)
	if _, err := digestHex(digest); err != nil || d["prefix"] != team.DigestPrefix(digest) {
		t.Fatalf("digest %q prefix %v: %v", digest, d["prefix"], err)
	}
	lines := strings.Join(toStrings(d["plan"]), "\n")
	for _, want := range []string{"goal: ship the parser", "seat impl-1 (implementer) claude/sonnet/medium", "files: internal/parser/", "task 1: write the parser [implement]"} {
		if !strings.Contains(lines, want) {
			t.Fatalf("plan lacks %q:\n%s", want, lines)
		}
	}
	if !strings.Contains(d["doors"].(string), "/squad confirm "+team.DigestPrefix(digest)) || !strings.Contains(d["squad_yaml"].(string), "handle: impl-1") {
		t.Fatalf("doors %v squad_yaml %v", d["doors"], d["squad_yaml"])
	}
	if got := snapshotTree(t, proj); !mapsEqual(got, before) {
		t.Fatal("team propose --from PLAN wrote inside the repository")
	}
	if p, err := readProposal(root, digest); err != nil || p.Project != proj || p.SquadYAML != team.RenderPlan(soloTeamPlan()) {
		t.Fatalf("proposal %+v %v", p, err)
	}
	wide := soloTeamPlan()
	wide.Seats[1].Runtime = "rocket"
	raw, _ := json.Marshal(wide)
	file := filepath.Join(t.TempDir(), "wide.json")
	mustWrite(t, file, string(raw))
	if _, _, err := captureOutput(t, func() error { return runTeam([]string{"propose", "--from", file, "--project", proj}) }); err == nil {
		t.Fatal("a plan with an unknown runtime was proposed")
	}
}

func toStrings(v any) []string {
	var out []string
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// fakeTree is a process tree the kernel reads see: pid -> parent, argv,
// initial environment, start stamp (default st-<pid>), controlling terminal
// and session leader.
type fakeTree struct {
	self, sid int
	parent    map[int]int
	argv      map[int][]string
	env       map[int][]string
	stamp     map[int]string
	notty     map[int]bool
	// reuseAt: the pid whose stamp reads "reused" at its read number n
	reuseAt [2]int
}

func (f fakeTree) install(t *testing.T) {
	t.Helper()
	prev := []any{confirmSelf, confirmParent, confirmArgv, confirmEnviron, confirmSession, confirmHasTTY, confirmStamp}
	t.Cleanup(func() {
		confirmSelf = prev[0].(func() int)
		confirmParent = prev[1].(func(int) (int, bool))
		confirmArgv = prev[2].(func(int) ([]string, bool))
		confirmEnviron = prev[3].(func(int) ([]string, bool))
		confirmSession = prev[4].(func(int) (int, bool))
		confirmHasTTY = prev[5].(func(int) (bool, bool))
		confirmStamp = prev[6].(func(int) (string, bool))
	})
	confirmSelf = func() int { return f.self }
	confirmParent = func(p int) (int, bool) { q, ok := f.parent[p]; return q, ok }
	confirmArgv = func(p int) ([]string, bool) { a, ok := f.argv[p]; return a, ok }
	confirmEnviron = func(p int) ([]string, bool) { e, ok := f.env[p]; return e, ok }
	confirmSession = func(int) (int, bool) { return f.sid, true }
	confirmHasTTY = func(p int) (bool, bool) { return !f.notty[p], true }
	reads := map[int]int{}
	confirmStamp = func(p int) (string, bool) {
		reads[p]++
		if f.reuseAt[0] == p && f.reuseAt[1] == reads[p] {
			return "reused", true
		}
		if s, ok := f.stamp[p]; ok {
			return s, true
		}
		return fmt.Sprintf("st-%d", p), true
	}
}

// ownerShellTree: the command (500) <- zsh (400, the session leader) <-
// login (300, another user's: unreadable) <- launchd.
func ownerShellTree() fakeTree {
	return fakeTree{self: 500, sid: 400,
		parent: map[int]int{500: 400, 400: 300, 300: 1},
		argv:   map[int][]string{500: {"amq-squad", "team"}, 400: {"-zsh"}},
		env:    map[int][]string{400: {"HOME=/Users/owner", "AM_ME="}},
		stamp:  map[int]string{}, notty: map[int]bool{}}
}

// The kernel trees a confirmation is refused from, by name (the lead's pid
// is lead): an amq-squad lead (bare or `lead`), the lead's pi (AM_ME), the
// lead's own pid, a seat (seat-exec, or a provider seat-exec exec'd, AM_ME
// in its environment), no terminal, orphaned out of its shell, a parent
// that cannot be read. With no lead (lead 0: the owner's apply) there is no
// lead pid to refuse; the lead and its pi are still refused by argv and
// AM_ME.
func refusedTrees(lead int) map[string]struct {
	mutate func(*fakeTree)
	want   string
} {
	trees := map[string]struct {
		mutate func(*fakeTree)
		want   string
	}{
		"the solo lead (amq-squad lead)": {func(f *fakeTree) {
			f.parent[500], f.parent[450] = 450, 400
			f.argv[450] = []string{"/usr/local/bin/amq-squad", "lead"}
		}, ReasonLeadDescendant},
		"the bare solo lead": {func(f *fakeTree) { f.parent[500], f.parent[450] = 450, 400; f.argv[450] = []string{"amq-squad"} }, ReasonLeadDescendant},
		"the lead's pi (AM_ME)": {func(f *fakeTree) {
			f.parent[500], f.parent[460], f.parent[450] = 460, 450, 400
			f.argv[460], f.env[460] = []string{"/bin/bash", "-c", "x"}, []string{"PATH=/bin"}
			f.argv[450], f.env[450] = []string{"node", "/rt/pi/cli.js"}, []string{"AM_ME=lead", "AM_ROOT=/r"}
		}, ReasonLeadDescendant},
		"the lead's own pid": {func(f *fakeTree) {
			f.parent[500], f.parent[lead] = lead, 400
			f.argv[lead] = []string{"/opt/bin/renamed"}
		}, ReasonLeadDescendant},
		"a seat-exec seat": {func(f *fakeTree) {
			f.parent[500], f.parent[450] = 450, 400
			f.argv[450] = []string{"amq-squad", "seat-exec", "--seat", "impl-1"}
		}, ReasonLeadDescendant},
		"a Claude seat (AM_ME)": {func(f *fakeTree) {
			f.parent[500], f.parent[450] = 450, 400
			f.argv[450], f.env[450] = []string{"claude", "--model", "sonnet"}, []string{"AM_ME=impl-1"}
		}, ReasonLeadDescendant},
		"no terminal":         {func(f *fakeTree) { f.notty[500] = true }, ReasonNoTerminal},
		"orphaned out of zsh": {func(f *fakeTree) { f.parent[500] = 1 }, ReasonAncestryUnknown},
		"a parent unreadable": {func(f *fakeTree) { delete(f.parent, 400) }, ReasonAncestryUnknown},
	}
	if lead == 0 {
		delete(trees, "the lead's own pid")
	}
	return trees
}

// Rows 1b, 1e and 5 of the witness design: with no lead (or from the
// owner's own shell), team apply --digest D is the confirmation by the act,
// so its own process must read as the owner's from the kernel: from a lead
// or seat descendant, without a terminal or orphaned it refuses by the same
// names, and a planted confirm/<digest>.json changes nothing (no file is
// read for it); from the owner's shell it applies. The plan-file apply
// (--from PLAN) is the same act.
func TestTheOwnersApplyIsTheConfirmationByTheAct(t *testing.T) {
	proj, root := soloProject(t)
	t.Setenv(leadStartSocketEnv, "")
	digest := proposeSoloPlan(t, proj, soloTeamPlan())["digest"].(string)
	if _, err := writeConfirmLog(root, confirmRecord{Digest: digest, Door: DoorTUI, At: time.Now(), By: "planted", Proof: confirmProof{Pane: "%1", PID: 4242}}); err != nil {
		t.Fatal(err)
	}
	before := snapshotTree(t, proj)
	raw, _ := json.Marshal(soloTeamPlan())
	planFile := filepath.Join(t.TempDir(), "plan.json")
	mustWrite(t, planFile, string(raw))
	for _, args := range [][]string{{"apply", "--digest", digest, "--project", proj}, {"apply", "--digest", digest, "--project", proj, "--from", planFile}} {
		for name, c := range refusedTrees(0) {
			f := ownerShellTree()
			c.mutate(&f)
			f.install(t)
			if _, _, err := captureOutput(t, func() error { return runTeam(args) }); err == nil || !strings.HasPrefix(err.Error(), c.want+":") {
				t.Fatalf("%v from %s = %v, want %s", args[3:], name, err, c.want)
			}
		}
	}
	if !mapsEqual(snapshotTree(t, proj), before) {
		t.Fatal("a refused apply wrote inside the repository")
	}
	ownerShellTree().install(t)
	out, _, err := captureOutput(t, func() error { return runTeam([]string{"apply", "--digest", digest, "--project", proj}) })
	if err != nil || !strings.Contains(out, "the owner starts") {
		t.Fatalf("the owner's apply: %v\n%s", err, out)
	}
	if raw, err := os.ReadFile(team.SquadPath(proj)); err != nil || string(raw) != team.RenderPlan(soloTeamPlan()) {
		t.Fatalf("squad.yaml %q %v", raw, err)
	}
}

// soloLead is a serving solo lead for a project's root (its start socket
// and lead-start.json), the seams restored after the test.
func soloLead(t *testing.T, root, proj string) *leadStarter {
	t.Helper()
	s := newLeadStarter(root, proj, "test", nil)
	if _, err := s.serve(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.close)
	return s
}

// Row 1e of the witness design: the shell door. team confirm D asks the
// lead over its socket and the lead reads the peer from the kernel: a peer
// descending from the lead (its pid, an amq-squad lead, the lead's pi) or
// a seat is refused lead_descendant, one without a terminal no_terminal,
// one orphaned ancestry_unknown; the owner's shell is witnessed, with the
// chain the lead read (pids and start stamps). The claimant asserts
// nothing. With no lead serving, team confirm refuses no_lead.
func TestTheShellDoorIsWitnessedByTheLeadFromTheKernel(t *testing.T) {
	proj, root := soloProject(t)
	digest := proposeSoloPlan(t, proj, soloTeamPlan())["digest"].(string)
	confirm := func() error {
		_, _, err := captureOutput(t, func() error { return runTeam([]string{"confirm", digest, "--project", proj}) })
		return err
	}
	if err := confirm(); err == nil || !strings.HasPrefix(err.Error(), ReasonNoLead+":") {
		t.Fatalf("no lead: %v", err)
	}
	s := soloLead(t, root, proj)
	prev := leadStartPeer
	t.Cleanup(func() { leadStartPeer = prev })
	leadStartPeer = func(*net.UnixConn) (int, bool) { return 500, true }
	for name, c := range refusedTrees(s.self) {
		f := ownerShellTree()
		c.mutate(&f)
		f.install(t)
		if err := confirm(); err == nil || !strings.HasPrefix(err.Error(), c.want+":") {
			t.Fatalf("%s: confirm = %v, want %s", name, err, c.want)
		}
		if _, err := s.witnessFor(digest); err == nil {
			t.Fatalf("%s: witnessed", name)
		}
	}
	// the peer's pid reused after the walk: its third read, the final
	// re-read of the chain, is caller_unproved
	reused := ownerShellTree()
	reused.reuseAt = [2]int{500, 3}
	reused.install(t)
	if err := confirm(); err == nil || !strings.HasPrefix(err.Error(), ReasonCallerUnproved+":") {
		t.Fatalf("a pid reused after the walk = %v", err)
	}
	ownerShellTree().install(t)
	if err := confirm(); err != nil {
		t.Fatalf("the owner's shell: %v", err)
	}
	rec, err := s.witnessFor(digest)
	if err != nil || rec.Door != DoorShell || len(rec.Proof.PIDChain) != 3 || rec.Proof.PIDChain[1] != (confirmHop{PID: 400, Stamp: "st-400", Name: "-zsh"}) || rec.Proof.PIDChain[0].PID != 500 {
		t.Fatalf("witnessed %+v %v", rec, err)
	}
}

// Row 1d of the witness design: the Buzz door. The lead takes only an
// event id, fetches the event itself on its read connection (the test
// relay) and runs the typed path's checks: a made-up id is
// event_unverified, an event signed by another key wrong_pubkey, the
// owner's yes is witnessed once with the event id it verified (a replay of
// the id is refused), and a yes whose prefix no proposal carries is
// no_proposal.
func TestTheBuzzDoorIsWitnessedByTheLeadFromTheEventItFetched(t *testing.T) {
	f := newGateCLIFixture(t)
	proj, root := soloProject(t)
	digest := proposeSoloPlan(t, proj, soloTeamPlan())["digest"].(string)
	s := newLeadStarter(root, proj, "test", nil)
	ask := func(id string) leadStartReply {
		return s.witnessBuzz(context.Background(), leadStartRequest{Kind: confirmBuzzKind, EventID: id})
	}
	created := time.Now()
	publish := func(key nostr.SecretKey, content string) string {
		e := nostr.Event{CreatedAt: created.Unix(), Kind: 9, Tags: [][]string{{"h", "dm-chan"}}, Content: content}
		if err := key.Sign(&e); err != nil {
			t.Fatal(err)
		}
		_ = f.relay.Publish(context.Background(), e.Marshal())
		return e.ID
	}
	if r := ask(strings.Repeat("cd", 32)); r.Refusal != ReasonEventUnverified {
		t.Fatalf("made-up id = %+v", r)
	}
	stranger, _ := nostr.NewSecretKey()
	if r := ask(publish(stranger, "yes "+team.DigestPrefix(digest))); r.Refusal != gateverify.RejectWrongPubkey {
		t.Fatalf("another key = %+v", r)
	}
	if r := ask(publish(f.owner, "yes 0000000000000000")); r.Refusal != ReasonNoProposal {
		t.Fatalf("no proposal = %+v", r)
	}
	created = time.Now().Add(-time.Hour)
	if r := ask(publish(f.owner, "yes "+team.DigestPrefix(digest))); r.Refusal != gateverify.RejectWindow {
		t.Fatalf("an hour-old yes = %+v", r)
	}
	created = time.Now()
	if _, err := s.witnessFor(digest); err == nil {
		t.Fatal("witnessed before the owner's yes")
	}
	id := publish(f.owner, "yes "+team.DigestPrefix(digest))
	if r := ask(id); r.Outcome != "witnessed" || r.Digest != digest {
		t.Fatalf("the owner's yes = %+v", r)
	}
	rec, err := s.witnessFor(digest)
	if err != nil || rec.Door != DoorBuzz || rec.Proof.EventID != id {
		t.Fatalf("witnessed %+v %v", rec, err)
	}
	if r := ask(id); r.Refusal != gatestore.RejectReplayed {
		t.Fatalf("a replay = %+v", r)
	}
}

// Row 1d end to end through the test relay: the owner's signed DM `yes
// <prefix>`, delivered by serve to the attachment (remote attach --once),
// passes the handler's typed path, and the attachment hands the repository's
// lead the event id; the lead verifies and witnesses it, and the reply names
// the team only. A DM signed by another key reaches no lead.
func TestTheOwnersSignedYesOverBuzzIsWitnessedByTheLead(t *testing.T) {
	f := newGateCLIFixture(t)
	proj, root := soloProject(t)
	digest := proposeSoloPlan(t, proj, soloTeamPlan())["digest"].(string)
	s := soloLead(t, root, proj)
	attach := func() {
		t.Helper()
		if _, _, err := captureOutput(t, func() error {
			return runRemote([]string{"attach", "--once", "--json", "--project", f.project, "--session", "s", "--me", "cto", "--lead", "cto"}, "test")
		}); err != nil {
			t.Fatalf("attach: %v", err)
		}
	}
	stranger, _ := nostr.NewSecretKey()
	e := nostr.Event{CreatedAt: time.Now().Unix(), Kind: 9, Tags: [][]string{{"h", "dm-chan"}}, Content: "yes " + team.DigestPrefix(digest)}
	_ = stranger.Sign(&e)
	_ = f.relay.Publish(context.Background(), e.Marshal())
	foreign := f.serve().deliver(e.ID, e.Content)
	attach()
	if _, err := s.witnessFor(digest); err == nil {
		t.Fatal("a DM signed by another key was witnessed")
	}
	if res, _ := f.serve().result(foreign); !strings.HasPrefix(res, "refused") {
		t.Fatalf("foreign reply %q", res)
	}
	yes := nostr.Event{CreatedAt: time.Now().Unix(), Kind: 9, Tags: [][]string{{"h", "dm-chan"}}, Content: "yes " + team.DigestPrefix(digest)}
	if err := f.owner.Sign(&yes); err != nil {
		t.Fatal(err)
	}
	_ = f.relay.Publish(context.Background(), yes.Marshal())
	id, msg := yes.ID, f.serve().deliver(yes.ID, yes.Content)
	attach()
	rec, err := s.witnessFor(digest)
	if err != nil || rec.Door != DoorBuzz || rec.Proof.EventID != id || rec.By != "owner key "+f.owner.PublicHex() {
		t.Fatalf("witnessed %+v %v", rec, err)
	}
	if res, _ := f.serve().result(msg); res != "confirmed team" {
		t.Fatalf("reply %q", res)
	}
}

// Row 1b: the guard refuses team confirm and team apply from a seat's (or
// the lead's) shell, and a write into the lead's confirmation log (a second
// layer: the log decides nothing).
func TestGuardRefusesTheOwnersVerbsFromASeat(t *testing.T) {
	proj, root := soloProject(t)
	digest := "sha256:" + strings.Repeat("ab", 32) + "@absent"
	id := hookIdentity{Handle: "impl-1", Root: filepath.Join(proj, ".agent-mail", "s1"), Session: "s1"}
	for _, call := range []guardCall{
		{Tool: "Bash", Args: map[string]string{"command": "amq-squad team confirm " + digest + " --project " + proj}, CWD: proj},
		{Tool: "bash", Args: map[string]string{"command": "cd /tmp && /usr/local/bin/amq-squad team apply --digest " + digest}, CWD: proj},
		{Tool: "Write", Args: map[string]string{"file_path": filepath.Join(root, "extensions", "amq-squad", "confirm", "x.json"), "content": "{}"}, CWD: proj},
		{Tool: "Bash", Args: map[string]string{"command": "echo {} > " + filepath.Join(root, "extensions", "amq-squad", "confirm", "x.json")}, CWD: proj},
	} {
		if v := evaluateGuard(id, call, time.Now()); v.Verdict != "deny" || v.ReasonCode != "owner_only" {
			t.Fatalf("guard %v = %+v, want deny owner_only", call.Args, v)
		}
	}
	if v := evaluateGuard(id, guardCall{Tool: "Bash", Args: map[string]string{"command": "amq-squad team show"}, CWD: proj}, time.Now()); v.ReasonCode == "owner_only" {
		t.Fatalf("team show refused: %+v", v)
	}
}

// Row 1f's second layer after Codex on #834: the guard refuses the same
// forms for a solo lead's shell call (session solo, a root under
// ~/.amq-squad/solo/) as the extension does, reading the extension's own
// table; a seat's call is not read by this rule; ordinary quoted commands
// run.
func TestTheGuardRefusesTheSoloLeadsQuotedTmuxAsASecondLayer(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "pi", "test", "solo-shell-forms.json"))
	if err != nil {
		t.Fatal(err)
	}
	var forms struct{ Refused, Runs []string }
	if err := json.Unmarshal(raw, &forms); err != nil {
		t.Fatal(err)
	}
	for _, line := range forms.Refused {
		if soloLeadShellRefusal(line) == "" {
			t.Errorf("runs: %s", line)
		}
	}
	for _, line := range forms.Runs {
		if why := soloLeadShellRefusal(line); why != "" {
			t.Errorf("refused (%s): %s", why, line)
		}
	}
	proj, root := soloProject(t)
	solo := hookIdentity{Handle: "lead", Root: root, Session: "solo"}
	call := guardCall{Tool: "bash", Args: map[string]string{"command": forms.Refused[0]}, CWD: proj}
	if v := evaluateGuard(solo, call, time.Now()); v.Verdict != "deny" || v.ReasonCode != "owner_only" {
		t.Fatalf("solo lead t\"\"mux = %+v", v)
	}
	seat := hookIdentity{Handle: "impl-1", Root: filepath.Join(proj, ".agent-mail", "s1"), Session: "s1"}
	if v := evaluateGuard(seat, call, time.Now()); v.ReasonCode == "owner_only" {
		t.Fatalf("a seat's call read by the solo rule: %+v", v)
	}
	if v := evaluateGuard(solo, guardCall{Tool: "bash", Args: map[string]string{"command": `git log --format="%h %s" -3`}, CWD: proj}, time.Now()); v.ReasonCode == "owner_only" {
		t.Fatalf("an ordinary quoted command refused: %+v", v)
	}
}
