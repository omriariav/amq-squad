package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// Row 1a: team apply --digest D of a recorded proposal with no
// confirmation refuses naming the three doors; a record older than 30
// minutes refuses the same; a record without its door's proof refuses
// confirmation_unproved (row 1f's record half). Nothing is written.
func TestTeamApplyWithoutAFreshConfirmationRefusesNamingTheThreeDoors(t *testing.T) {
	proj, root := soloProject(t)
	t.Setenv(leadStartSocketEnv, "")
	digest := proposeSoloPlan(t, proj, soloTeamPlan())["digest"].(string)
	before := snapshotTree(t, proj)
	apply := func() error {
		_, _, err := captureOutput(t, func() error { return runTeam([]string{"apply", "--digest", digest, "--project", proj}) })
		return err
	}
	doors := func(err error, name string) {
		t.Helper()
		if err == nil || !strings.HasPrefix(err.Error(), name+":") {
			t.Fatalf("apply = %v, want %s", err, name)
		}
		for _, door := range []string{"/squad confirm " + team.DigestPrefix(digest), "amq-squad team confirm " + digest, `Buzz DM "yes ` + team.DigestPrefix(digest)} {
			if !strings.Contains(err.Error(), door) {
				t.Fatalf("%s does not name the door %q: %v", name, door, err)
			}
		}
	}
	doors(apply(), ReasonConfirmMissing)
	// the plan file given again (team apply --from PLAN) needs it as well
	raw, _ := json.Marshal(soloTeamPlan())
	planFile := filepath.Join(t.TempDir(), "plan.json")
	mustWrite(t, planFile, string(raw))
	_, _, err := captureOutput(t, func() error {
		return runTeam([]string{"apply", "--digest", digest, "--project", proj, "--from", planFile})
	})
	doors(err, ReasonConfirmMissing)
	stale := confirmRecord{Digest: digest, Door: DoorShell, At: time.Now().Add(-31 * time.Minute), By: "uid 1", Proof: confirmProof{PIDChain: []confirmHop{{PID: 10, Name: "zsh"}}}}
	if _, err := writeConfirmRecord(root, stale); err != nil {
		t.Fatal(err)
	}
	doors(apply(), ReasonConfirmStale)
	for door, rec := range map[string]confirmRecord{
		DoorTUI:   {Digest: digest, Door: DoorTUI, At: time.Now(), Proof: confirmProof{Pane: "%3"}},
		DoorShell: {Digest: digest, Door: DoorShell, At: time.Now()},
		DoorBuzz:  {Digest: digest, Door: DoorBuzz, At: time.Now(), Proof: confirmProof{EventID: "e1"}},
	} {
		if _, err := writeConfirmRecord(root, rec); err != nil {
			t.Fatal(err)
		}
		if err := apply(); err == nil || !strings.HasPrefix(err.Error(), ReasonConfirmUnproved+": the "+door+" door") {
			t.Fatalf("a %s record without its proof: %v", door, err)
		}
	}
	if !mapsEqual(snapshotTree(t, proj), before) {
		t.Fatal("a refused apply wrote inside the repository")
	}
}

// fakeTree is a process tree for door (a): pid -> parent, argv, env, and
// this process's session.
type fakeTree struct {
	self, sid int
	parent    map[int]int
	argv      map[int][]string
	env       map[int][]string
	tty       bool
}

func (f fakeTree) install(t *testing.T) {
	t.Helper()
	prev := []any{confirmSelf, confirmParent, confirmArgv, confirmEnviron, confirmSession, confirmTerminal}
	t.Cleanup(func() {
		confirmSelf = prev[0].(func() int)
		confirmParent = prev[1].(func(int) (int, bool))
		confirmArgv = prev[2].(func(int) ([]string, bool))
		confirmEnviron = prev[3].(func(int) ([]string, bool))
		confirmSession = prev[4].(func(int) (int, bool))
		confirmTerminal = prev[5].(func() bool)
	})
	confirmSelf = func() int { return f.self }
	confirmParent = func(p int) (int, bool) { q, ok := f.parent[p]; return q, ok }
	confirmArgv = func(p int) ([]string, bool) { a, ok := f.argv[p]; return a, ok }
	confirmEnviron = func(p int) ([]string, bool) { e, ok := f.env[p]; return e, ok }
	confirmSession = func(int) (int, bool) { return f.sid, true }
	confirmTerminal = func() bool { return f.tty }
}

// ownerShellTree: team confirm (500) <- zsh (400, the session leader) <-
// login (300, another user's: unreadable) <- launchd.
func ownerShellTree() fakeTree {
	return fakeTree{self: 500, sid: 400, tty: true,
		parent: map[int]int{500: 400, 400: 300, 300: 1},
		argv:   map[int][]string{400: {"-zsh"}},
		env:    map[int][]string{400: {"HOME=/Users/owner", "AM_ME="}}}
}

// Row 1e: door (a)'s kernel-level refusal. With an amq-squad lead (the solo
// lead, bare or `lead`), the lead's pi (its environment carries AM_ME) or a
// seat (seat-exec, or a provider seat-exec exec'd, AM_ME in its
// environment) as an ancestor, team confirm refuses lead_descendant and
// writes nothing; without a terminal it refuses no_terminal; orphaned out
// of its shell (the chain misses its session leader) ancestry_unknown;
// from the owner's shell it writes the record with the pid chain as its
// proof (row 1f's shell half).
func TestTeamConfirmRefusesALeadOrSeatAncestorAndWritesFromTheOwnersShell(t *testing.T) {
	proj, root := soloProject(t)
	digest := proposeSoloPlan(t, proj, soloTeamPlan())["digest"].(string)
	confirm := func() error {
		_, _, err := captureOutput(t, func() error { return runTeam([]string{"confirm", digest, "--project", proj}) })
		return err
	}
	h, _ := digestHex(digest)
	written := func() bool { _, err := os.Stat(confirmPath(root, h)); return err == nil }
	cases := map[string]struct {
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
		"a seat-exec seat": {func(f *fakeTree) {
			f.parent[500], f.parent[450] = 450, 400
			f.argv[450] = []string{"amq-squad", "seat-exec", "--seat", "impl-1"}
		}, ReasonLeadDescendant},
		"a Claude seat (AM_ME)": {func(f *fakeTree) {
			f.parent[500], f.parent[450] = 450, 400
			f.argv[450], f.env[450] = []string{"claude", "--model", "sonnet"}, []string{"AM_ME=impl-1"}
		}, ReasonLeadDescendant},
		"no terminal":         {func(f *fakeTree) { f.tty = false }, ReasonNoTerminal},
		"orphaned out of zsh": {func(f *fakeTree) { f.parent[500] = 1 }, ReasonAncestryUnknown},
		"a parent unreadable": {func(f *fakeTree) { delete(f.parent, 400) }, ReasonAncestryUnknown},
	}
	for name, c := range cases {
		f := ownerShellTree()
		c.mutate(&f)
		f.install(t)
		if err := confirm(); err == nil || !strings.HasPrefix(err.Error(), c.want+":") || written() {
			t.Fatalf("%s: confirm = %v (written %v), want %s", name, err, written(), c.want)
		}
	}
	ownerShellTree().install(t)
	if err := confirm(); err != nil || !written() {
		t.Fatalf("the owner's shell: %v (written %v)", err, written())
	}
	rec, err := checkConfirmation(root, digest, proj)
	if err != nil || rec.Door != DoorShell || len(rec.Proof.PIDChain) != 2 || rec.Proof.PIDChain[0] != (confirmHop{PID: 400, Name: "-zsh"}) || rec.Proof.PIDChain[1].PID != 300 {
		t.Fatalf("record %+v %v", rec, err)
	}
}

// Row 1b: the guard refuses team confirm and team apply from a seat's (or
// the lead's) shell, and a write into the confirmation records; team
// confirm from the owner's shell writes the record and the apply from the
// owner's shell succeeds (squad.yaml through the digest flow; the owner
// starts the team with up).
func TestGuardRefusesTheConfirmFromASeatAndTheOwnersConfirmLetsTheApplyRun(t *testing.T) {
	proj, root := soloProject(t)
	t.Setenv(leadStartSocketEnv, "")
	digest := proposeSoloPlan(t, proj, soloTeamPlan())["digest"].(string)
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
	ownerShellTree().install(t)
	if _, _, err := captureOutput(t, func() error { return runTeam([]string{"confirm", digest, "--project", proj}) }); err != nil {
		t.Fatal(err)
	}
	out, _, err := captureOutput(t, func() error { return runTeam([]string{"apply", "--digest", digest, "--project", proj}) })
	if err != nil || !strings.Contains(out, "the owner starts") {
		t.Fatalf("apply after the owner's confirm: %v\n%s", err, out)
	}
	if raw, err := os.ReadFile(team.SquadPath(proj)); err != nil || string(raw) != team.RenderPlan(soloTeamPlan()) {
		t.Fatalf("squad.yaml %q %v", raw, err)
	}
}

// Row 1d (the cli half): the Buzz door's write, after the handler verified
// the owner's signature: the one fresh proposal the prefix names gets the
// record with the event id as its proof; a wrong prefix writes nothing.
func TestBuzzDoorConfirmsTheProposalItsPrefixNames(t *testing.T) {
	proj, root := soloProject(t)
	digest := proposeSoloPlan(t, proj, soloTeamPlan())["digest"].(string)
	event := strings.Repeat("ab", 32)
	if _, err := confirmByPrefix("0000000000000000", event, "owner", time.Now()); err == nil || !strings.HasPrefix(err.Error(), ReasonNoProposal+":") {
		t.Fatalf("wrong prefix: %v", err)
	}
	h, _ := digestHex(digest)
	if _, err := os.Stat(confirmPath(root, h)); err == nil {
		t.Fatal("a wrong prefix wrote a record")
	}
	got, err := confirmByPrefix(team.DigestPrefix(digest), event, "owner", time.Now())
	if err != nil || got != digest {
		t.Fatalf("confirm by prefix: %q %v", got, err)
	}
	rec, err := checkConfirmation(root, digest, proj)
	if err != nil || rec.Door != DoorBuzz || rec.Proof.EventID != event {
		t.Fatalf("record %+v %v", rec, err)
	}
}

// Row 1d end to end through the test relay: the owner's signed DM `yes
// <prefix>`, delivered by serve to the attachment (remote attach --once),
// is fetched and verified on the handler's typed path and confirms the
// proposal: the record carries the event id, and the reply names the team
// only. A DM signed by another key confirms nothing.
func TestTheOwnersSignedYesOverBuzzConfirmsTheProposal(t *testing.T) {
	f := newGateCLIFixture(t)
	proj, root := soloProject(t)
	digest := proposeSoloPlan(t, proj, soloTeamPlan())["digest"].(string)
	attach := func() {
		t.Helper()
		if _, _, err := captureOutput(t, func() error {
			return runRemote([]string{"attach", "--once", "--json", "--project", f.project, "--session", "s", "--me", "cto", "--lead", "cto"}, "test")
		}); err != nil {
			t.Fatalf("attach: %v", err)
		}
	}
	h, _ := digestHex(digest)
	stranger, _ := nostr.NewSecretKey()
	e := nostr.Event{CreatedAt: time.Now().Unix(), Kind: 9, Tags: [][]string{{"h", "dm-chan"}}, Content: "yes " + team.DigestPrefix(digest)}
	_ = stranger.Sign(&e)
	_ = f.relay.Publish(context.Background(), e.Marshal())
	foreign := f.serve().deliver(e.ID, e.Content)
	attach()
	if _, err := os.Stat(confirmPath(root, h)); err == nil {
		t.Fatal("a DM signed by another key confirmed the proposal")
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
	rec, err := checkConfirmation(root, digest, proj)
	if err != nil || rec.Door != DoorBuzz || rec.Proof.EventID != id || rec.By != "owner key "+f.owner.PublicHex() {
		t.Fatalf("record %+v %v", rec, err)
	}
	if res, _ := f.serve().result(msg); res != "confirmed team" {
		t.Fatalf("reply %q", res)
	}
}
