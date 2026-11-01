package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/gatestore"
	"github.com/omriariav/amq-squad/v3/internal/gateverify"
	"github.com/omriariav/amq-squad/v3/internal/nostr"
	"github.com/omriariav/amq-squad/v3/internal/procinfo"
	"github.com/omriariav/amq-squad/v3/internal/schedproc"
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

// Rows 1b and 5 after the shell door fell (the plan reviewer's ruling): a
// shell's team apply --digest D is the owner's confirmation by the act, so
// it proceeds only while no lead holds the repository. With the solo lead
// serving its start socket, or a team lead holding the session root's lead
// lock, it refuses lead_running naming the two doors, from any shell; with
// none it writes squad.yaml and prints the owner's start. The plan-file
// apply (--from PLAN) is the same act. A planted confirm/<digest>.json
// changes nothing.
func TestTheShellApplyProceedsOnlyWithNoLeadHoldingTheRoot(t *testing.T) {
	proj, root := soloProject(t)
	t.Setenv(leadStartSocketEnv, "")
	digest := proposeSoloPlan(t, proj, soloTeamPlan())["digest"].(string)
	if _, err := writeConfirmLog(root, confirmRecord{Digest: digest, Door: DoorTUI, At: time.Now(), By: "planted", Proof: confirmProof{Pane: "%1", PID: 4242}}); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(soloTeamPlan())
	planFile := filepath.Join(t.TempDir(), "plan.json")
	mustWrite(t, planFile, string(raw))
	forms := [][]string{{"apply", "--digest", digest, "--project", proj}, {"apply", "--digest", digest, "--project", proj, "--from", planFile}}
	refused := func(why string) {
		t.Helper()
		for _, args := range forms {
			_, _, err := captureOutput(t, func() error { return runTeam(args) })
			if err == nil || !strings.HasPrefix(err.Error(), schedproc.ReasonLeadRunning+":") || !strings.Contains(err.Error(), "/squad confirm "+team.DigestPrefix(digest)) || !strings.Contains(err.Error(), `Buzz DM "yes `) {
				t.Fatalf("%s: %v = %v", why, args[5:], err)
			}
		}
		if _, err := os.Stat(team.SquadPath(proj)); err == nil {
			t.Fatalf("%s: a refused apply wrote squad.yaml", why)
		}
	}
	lead := newLeadStarter(root, proj, "test", nil)
	if _, err := lead.serve(); err != nil {
		t.Fatal(err)
	}
	refused("the solo lead serving")
	lead.close()
	held := holdLock(t, filepath.Join(schedproc.Dir(filepath.Join(proj, ".agent-mail", "s1")), schedproc.LeadLockName))
	refused("a team lead holding the root")
	held.Close()
	out, _, err := captureOutput(t, func() error { return runTeam(forms[0]) })
	if err != nil || !strings.Contains(out, "the owner starts: amq-squad up --project "+proj) {
		t.Fatalf("no lead: %v\n%s", err, out)
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

// Row 1e after the shell door fell: `amq-squad team confirm` is an unknown
// verb, and the lead's socket refuses the confirm-shell kind by its name.
func TestTheShellDoorIsGone(t *testing.T) {
	_, _, err := captureOutput(t, func() error { return runTeam([]string{"confirm", "sha256:" + strings.Repeat("ab", 32) + "@absent"}) })
	if err == nil || !strings.Contains(err.Error(), "unknown 'team' subcommand") {
		t.Fatalf("team confirm = %v", err)
	}
	proj, root := soloProject(t)
	digest := proposeSoloPlan(t, proj, soloTeamPlan())["digest"].(string)
	s := soloLead(t, root, proj)
	c, err := dialLead(root)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	r, err := askLead(c, leadStartRequest{Kind: confirmShellKind, Digest: digest, Project: proj})
	if err != nil || r.Refusal != ReasonShellDoorRemoved {
		t.Fatalf("confirm-shell = %+v %v", r, err)
	}
	if _, err := s.witnessFor(digest); err == nil {
		t.Fatal("witnessed through the dropped shell door")
	}
}

// Row 1d of the witness design: the Buzz door. The lead takes the raw
// event bytes the attachment fetched and verifies them itself with no
// connection (the test fails if the lead dials anything): an event the
// attachment altered (one byte of the content, so its id no longer
// recomputes) is bad_id, an event the attachment signed with its own key
// wrong_pubkey, a yes whose prefix no proposal carries no_proposal, an
// hour-old yes outside the window; the owner's yes is witnessed once with
// the event id the lead verified, and a replay of that id is refused.
func TestTheBuzzDoorIsWitnessedByTheLeadFromTheEventBytesItVerified(t *testing.T) {
	f := newGateCLIFixture(t)
	proj, root := soloProject(t)
	digest := proposeSoloPlan(t, proj, soloTeamPlan())["digest"].(string)
	s := newLeadStarter(root, proj, "test", nil)
	prev := gateTransport
	t.Cleanup(func() { gateTransport = prev })
	gateTransport = func(context.Context, gateverify.Binding, gateverify.Pin) (gateverify.Relay, nostr.SecretKey, []string, error) {
		t.Fatal("the lead opened a relay connection")
		return nil, nostr.SecretKey{}, nil, nil
	}
	ask := func(raw []byte) leadStartReply {
		return s.witnessBuzz(leadStartRequest{Kind: confirmBuzzKind, Event: raw})
	}
	created := time.Now()
	event := func(key nostr.SecretKey, content string) nostr.Event {
		e := nostr.Event{CreatedAt: created.Unix(), Kind: 9, Tags: [][]string{{"h", "dm-chan"}}, Content: content}
		if err := key.Sign(&e); err != nil {
			t.Fatal(err)
		}
		return e
	}
	yes := "yes " + team.DigestPrefix(digest)
	genuine := event(f.owner, yes)
	altered := bytes.Replace(genuine.Marshal(), []byte(`"content":"yes `), []byte(`"content":"YES `), 1)
	if bytes.Equal(altered, genuine.Marshal()) {
		t.Fatal("the content was not altered")
	}
	if r := ask(altered); r.Refusal != gateverify.RejectBadID {
		t.Fatalf("an altered event = %+v", r)
	}
	attachKey, _ := nostr.NewSecretKey()
	if r := ask(event(attachKey, yes).Marshal()); r.Refusal != gateverify.RejectWrongPubkey {
		t.Fatalf("an event signed by the attachment = %+v", r)
	}
	if r := ask(event(f.owner, "yes 0000000000000000").Marshal()); r.Refusal != ReasonNoProposal {
		t.Fatalf("no proposal = %+v", r)
	}
	created = time.Now().Add(-time.Hour)
	if r := ask(event(f.owner, yes).Marshal()); r.Refusal != gateverify.RejectWindow {
		t.Fatalf("an hour-old yes = %+v", r)
	}
	if r := ask([]byte("not an event")); r.Refusal != ReasonEventUnverified {
		t.Fatalf("garbage = %+v", r)
	}
	if _, err := s.witnessFor(digest); err == nil {
		t.Fatal("witnessed before the owner's yes")
	}
	if r := ask(genuine.Marshal()); r.Outcome != "witnessed" || r.Digest != digest {
		t.Fatalf("the owner's yes = %+v", r)
	}
	rec, err := s.witnessFor(digest)
	if err != nil || rec.Door != DoorBuzz || rec.Proof.EventID != genuine.ID {
		t.Fatalf("witnessed %+v %v", rec, err)
	}
	if r := ask(genuine.Marshal()); r.Refusal != gatestore.RejectReplayed {
		t.Fatalf("a replay = %+v", r)
	}
	// a restarted lead (a new process on the same root) refuses the replay
	// too: the witnessed event ids are in the lead's log, read for refusal
	restarted := newLeadStarter(root, proj, "test", nil)
	if r := restarted.witnessBuzz(leadStartRequest{Kind: confirmBuzzKind, Event: genuine.Marshal()}); r.Refusal != gatestore.RejectReplayed {
		t.Fatalf("a replay to a restarted lead = %+v", r)
	}
	if _, err := restarted.witnessFor(digest); err == nil {
		t.Fatal("the restarted lead witnessed the replayed event")
	}
}

// Row 1d, once-only and atomic: sixteen concurrent submissions of one
// owner-signed event yield exactly one witness; the check and the mark sit
// under one lock.
func TestTheBuzzDoorWitnessesOneEventOnceUnderConcurrency(t *testing.T) {
	f := newGateCLIFixture(t)
	proj, root := soloProject(t)
	digest := proposeSoloPlan(t, proj, soloTeamPlan())["digest"].(string)
	s := newLeadStarter(root, proj, "test", nil)
	e := nostr.Event{CreatedAt: time.Now().Unix(), Kind: 9, Tags: [][]string{{"h", "dm-chan"}}, Content: "yes " + team.DigestPrefix(digest)}
	if err := f.owner.Sign(&e); err != nil {
		t.Fatal(err)
	}
	raw := e.Marshal()
	var wg sync.WaitGroup
	var mu sync.Mutex
	witnessed := 0
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if r := s.witnessBuzz(leadStartRequest{Kind: confirmBuzzKind, Event: raw}); r.Outcome == "witnessed" {
				mu.Lock()
				witnessed++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if witnessed != 1 {
		t.Fatalf("%d witnesses of one event", witnessed)
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
	var forms struct {
		Refused, Runs []string
		Allow         []string `json:"amq_squad_allowlist"`
	}
	if err := json.Unmarshal(raw, &forms); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(soloAmqSquadAllow, forms.Allow) {
		t.Fatalf("the guard's allowlist %v is not the table's %v", soloAmqSquadAllow, forms.Allow)
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

// Codex's third pass on #834 and the plan reviewer's liveness rule: the
// discovery record is read as the lead lock is. A malformed record, one
// whose pid carries another start stamp, and one whose pid is dead are
// stale: removed with a discovery_stale decision naming them, and the apply
// goes on as no lead; only a live pid with its stamp and a silent socket
// refuses lead_unreachable. A team lead holding any session's lead lock
// (not only the proposed session's) refuses lead_running naming that
// session; a lead starting between the probe and the write is refused
// apply_busy and the write goes on alone.
func TestTheShellApplyProbeFailsClosedAndHoldsThroughTheWrite(t *testing.T) {
	proj, root := soloProject(t)
	t.Setenv(leadStartSocketEnv, "")
	digest := proposeSoloPlan(t, proj, soloTeamPlan())["digest"].(string)
	apply := func() error {
		_, _, err := captureOutput(t, func() error { return runTeam([]string{"apply", "--digest", digest, "--project", proj}) })
		return err
	}
	rec := filepath.Join(root, "extensions", "amq-squad", leadStartRecordName)
	stale := func(why, body string) {
		t.Helper()
		mustWrite(t, rec, body)
		if err := noLeadHolds(proj, digest); err != nil {
			t.Fatalf("%s: %v", why, err)
		}
		if _, err := os.Stat(rec); err == nil {
			t.Fatalf("%s: the stale record was not removed", why)
		}
		found := false
		for _, d := range decisionKinds(t, root) {
			found = found || (d.Kind == KindDiscoveryStale && strings.Contains(d.Reason, rec))
		}
		if !found {
			t.Fatalf("%s: no %s decision names the record", why, KindDiscoveryStale)
		}
	}
	self := os.Getpid()
	stamp, _ := procinfo.StartToken(self)
	socket := filepath.Join(root, "extensions", "amq-squad", "launch", leadStartSocketName)
	stale("a malformed record", "{")
	stale("a live pid carrying another start stamp", fmt.Sprintf(`{"schema":1,"socket":%q,"pid":%d,"start":"another"}`, socket, self))
	mustWrite(t, rec, fmt.Sprintf(`{"schema":1,"socket":%q,"pid":%d,"start":%q}`, socket, self, stamp))
	if err := apply(); err == nil || !strings.HasPrefix(err.Error(), ReasonLeadUnreachable+":") || !strings.Contains(err.Error(), "alive with its start stamp") {
		t.Fatalf("a live lead whose socket does not answer = %v", err)
	}
	if err := os.Remove(rec); err != nil {
		t.Fatal(err)
	}
	held := holdLock(t, filepath.Join(schedproc.Dir(filepath.Join(proj, ".agent-mail", "s9")), schedproc.LeadLockName))
	if err := apply(); err == nil || !strings.HasPrefix(err.Error(), schedproc.ReasonLeadRunning+":") || !strings.Contains(err.Error(), "session s9") {
		t.Fatalf("a team lead on another session = %v", err)
	}
	held.Close()
	if _, err := os.Stat(team.SquadPath(proj)); err == nil {
		t.Fatal("a refused apply wrote squad.yaml")
	}
	// a record whose pid is dead: removed, and this apply goes on as no lead
	prevAlive := discoveryAlive
	t.Cleanup(func() { discoveryAlive = prevAlive })
	discoveryAlive = func(int) bool { return false }
	mustWrite(t, rec, `{"schema":1,"socket":"/nowhere","pid":999999,"start":"darwin-tv:1.1"}`)
	// a lead starting inside the probe-to-write window
	s1 := filepath.Join(proj, ".agent-mail", "s1")
	var leadErr error
	prev := []any{applyAfterProbe, applyLockWait}
	t.Cleanup(func() {
		applyAfterProbe, _ = prev[0].(func())
		applyLockWait = prev[1].(time.Duration)
	})
	applyAfterProbe = func() {
		applyLockWait = 50 * time.Millisecond
		_, leadErr = UpLeadLock(proj, s1)
		applyLockWait = prev[1].(time.Duration)
	}
	if err := apply(); err != nil {
		t.Fatalf("the apply with a lead starting in its window: %v", err)
	}
	if leadErr == nil || !strings.HasPrefix(leadErr.Error(), ReasonApplyBusy+":") {
		t.Fatalf("the lead that started in the window = %v", leadErr)
	}
	if h, _ := lockHeld(filepath.Join(schedproc.Dir(s1), schedproc.LeadLockName)); h {
		t.Fatal("a lead took the root while the apply wrote")
	}
	if _, err := os.Stat(rec); err == nil {
		t.Fatal("the dead lead's record was not removed")
	}
	if raw, err := os.ReadFile(team.SquadPath(proj)); err != nil || string(raw) != team.RenderPlan(soloTeamPlan()) {
		t.Fatalf("squad.yaml %q %v", raw, err)
	}
}

// Codex's narrow pass on #834, P1: solo startup takes the apply lock. A
// solo lead starting inside a shell apply's probe-to-write window cannot
// bind its socket or publish its record until the write is done (it waits
// on the lock); the apply then sees it on its next probe.
func TestASoloLeadStartingInTheApplyWindowWaitsForTheWrite(t *testing.T) {
	proj, root := soloProject(t)
	t.Setenv(leadStartSocketEnv, "")
	digest := proposeSoloPlan(t, proj, soloTeamPlan())["digest"].(string)
	rec := filepath.Join(root, "extensions", "amq-squad", leadStartRecordName)
	lead := newLeadStarter(root, proj, "test", nil)
	t.Cleanup(lead.close)
	served := make(chan error, 1)
	prev := applyAfterProbe
	t.Cleanup(func() { applyAfterProbe = prev })
	applyAfterProbe = func() {
		go func() { _, err := lead.serve(); served <- err }()
		time.Sleep(200 * time.Millisecond)
		if _, err := os.Stat(rec); err == nil {
			t.Error("the solo lead published its record inside the apply's window")
		}
	}
	if _, _, err := captureOutput(t, func() error { return runTeam([]string{"apply", "--digest", digest, "--project", proj}) }); err != nil {
		t.Fatalf("the apply: %v", err)
	}
	if err := <-served; err != nil {
		t.Fatalf("the solo lead that waited for the write: %v", err)
	}
	if err := noLeadHolds(proj, digest); err == nil || !strings.HasPrefix(err.Error(), schedproc.ReasonLeadRunning+":") {
		t.Fatalf("the next probe after the lead started = %v", err)
	}
}
