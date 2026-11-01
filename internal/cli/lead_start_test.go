package cli

import (
	"context"
	"encoding/json"
	"maps"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/fleet"
	"github.com/omriariav/amq-squad/v3/internal/grants"
	"github.com/omriariav/amq-squad/v3/internal/procinfo"
	"github.com/omriariav/amq-squad/v3/internal/schedproc"
	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/team"
)

// Brief 31-3, PR 1, section 3: the start of a confirmed team in the solo
// lead's process. up's fake world (upWorld: a fake amq, a fake tmux, the
// doctor) serves the shared steps; the scheduler start is a seam that logs
// the roster's outcome; the mayor's grant, registry and repository identity
// are seams. Nothing reaches the real AMQ, tmux or the owner's files.

// autoFleet is a fleet grant in auto mode permitting owner/app.
func autoFleet(cost float64) grants.Resolved {
	g := grants.Resolved{Source: "test"}
	g.Mode, g.MaxSeats = grants.ModeAuto, 4
	g.Fleet = grants.Fleet{MaxSquads: 2, CostPerDayUSD: cost, PermittedRepositories: []string{"owner/app"}}
	return g
}

// startWorld is a confirmed proposal in up's fake world, the starter of
// its solo lead, and the seams of the start. identity is the repository
// the mayor derives.
func startWorld(t *testing.T, g grants.Resolved, identity string) (*upFake, *leadStarter, string) {
	t.Helper()
	f, target := upWorld(t)
	if err := os.Remove(team.SquadPath(f.proj)); err != nil {
		t.Fatal(err)
	}
	root, err := soloRootOf(f.proj)
	if err != nil {
		t.Fatal(err)
	}
	digest := proposeSoloPlan(t, f.proj, soloTeamPlan())["digest"].(string)
	if _, err := writeConfirmRecord(root, confirmRecord{Digest: digest, Door: DoorTUI, At: time.Now().UTC(), By: "pane %7", Proof: confirmProof{Pane: "%7", PID: 4242}}); err != nil {
		t.Fatal(err)
	}
	prev := []any{leadMayorGrant, leadMayorRegistry, leadMayorIdentify, leadSelfBinary, startLeadScheduler}
	t.Cleanup(func() {
		leadMayorGrant = prev[0].(func() (grants.Resolved, error))
		leadMayorRegistry = prev[1].(func() string)
		leadMayorIdentify, _ = prev[2].(func(string, grants.Fleet) (string, string, error))
		leadSelfBinary = prev[3].(func() (string, error))
		startLeadScheduler = prev[4].(func(string, string, []string) (*supervisor, error))
	})
	registry := filepath.Join(t.TempDir(), "fleet.yaml")
	leadMayorGrant = fleet.StaticGrant(g)
	leadMayorRegistry = func() string { return registry }
	leadMayorIdentify = func(p string, _ grants.Fleet) (string, string, error) { return identity, p, nil }
	leadSelfBinary = func() (string, error) { return target, nil }
	startLeadScheduler = func(project, root string, _ []string) (*supervisor, error) {
		// the scheduler the lead starts: it logs the roster's outcome
		_ = schedproc.AppendDecision(root, time.Now(), scheduler.Decision{Kind: "roster_launched", Reason: "launch L1: 2 seats in one apply"})
		return &supervisor{}, nil
	}
	t.Setenv(leadStartEnv, "")
	s := newLeadStarter(root, f.proj, "v3.1.0", nil)
	s.wait = 5 * time.Second
	t.Cleanup(s.close)
	return f, s, digest
}

func sharedSnapshot() map[string]int {
	sharedMu.Lock()
	defer sharedMu.Unlock()
	return maps.Clone(sharedCalls)
}

func sharedDelta(before map[string]int) map[string]int {
	out := map[string]int{}
	for k, v := range sharedSnapshot() {
		if d := v - before[k]; d != 0 {
			out[k] = d
		}
	}
	return out
}

// Rows 3 and 6: amq_team_apply's start with the owner's confirmation
// writes squad.yaml through the digest flow and starts the team through
// up's shared steps (compile, session, doctor, lead lock, scheduler, roster
// outcome, each counted once), the fake world seeing the same amq and
// doctor calls `up` makes; no install and no tmux; the lead lock is this
// process's; the launch budget starts at the confirmation's moment.
func TestTheStartOfAConfirmedTeamRunsUpsSharedStepsAndNoInstallOrTmux(t *testing.T) {
	f, s, digest := startWorld(t, autoFleet(0), "owner/app")
	// the mayor's canonical path spells the checkout another way (a
	// symlink): the squad files are planned over the confirmed spelling
	link := filepath.Join(t.TempDir(), "app-link")
	if err := os.Symlink(f.proj, link); err != nil {
		t.Fatal(err)
	}
	leadMayorIdentify = func(string, grants.Fleet) (string, string, error) { return "owner/app", link, nil }
	if r := s.start(context.Background(), leadStartRequest{Kind: leadStartKind, Digest: digest, Project: filepath.Join(f.proj, "sub")}); r.Refusal != "project_mismatch" {
		t.Fatalf("another project = %+v", r)
	}
	before := sharedSnapshot()
	r := s.start(context.Background(), leadStartRequest{Kind: leadStartKind, Digest: digest, Project: f.proj})
	if r.Outcome != "started" || !strings.HasPrefix(r.Detail, "roster_launched") {
		t.Fatalf("start = %+v", r)
	}
	if got, want := sharedDelta(before), map[string]int{"compile": 1, "session": 1, "doctor": 1, "lead_lock": 1, "scheduler": 1, "roster": 1}; !maps.Equal(got, want) {
		t.Fatalf("shared steps %v, want %v", got, want)
	}
	var names []string
	for _, st := range r.Steps {
		names = append(names, st.Name+":"+st.Outcome)
	}
	if strings.Join(names, " ") != "compile:ok session:ok doctor:ok lead:ok scheduler:ok roster:ok" {
		t.Fatalf("steps %v", names)
	}
	for _, c := range f.calls {
		if strings.HasPrefix(c, "tmux ") || strings.HasPrefix(c, "amq-squad version") || strings.HasPrefix(c, "amq-squad pi") {
			t.Fatalf("the start ran %q (an install or tmux): %q", c, f.calls)
		}
	}
	if f.callIndex("amq coop init") < 0 || f.callIndex("amq session create s1") < 0 || f.callIndex("amq-squad doctor --json --project "+f.proj) < 0 {
		t.Fatalf("calls %q", f.calls)
	}
	if raw, err := os.ReadFile(team.SquadPath(f.proj)); err != nil || string(raw) != team.RenderPlan(soloTeamPlan()) {
		t.Fatalf("squad.yaml %q %v", raw, err)
	}
	if rec, err := schedproc.ReadLeadRecord(f.root); err != nil || rec.PID != os.Getpid() {
		t.Fatalf("lead.json %+v %v", rec, err)
	}
	rec, _ := checkConfirmation(s.root, digest, f.proj)
	if os.Getenv(leadStartEnv) != rec.At.UTC().Format(time.RFC3339Nano) {
		t.Fatalf("launch start %q, want the confirmation's moment %s", os.Getenv(leadStartEnv), rec.At)
	}
	if again := s.start(context.Background(), leadStartRequest{Kind: leadStartKind, Digest: digest, Project: f.proj}); again.Refusal != schedproc.ReasonLeadRunning {
		t.Fatalf("a second start = %+v", again)
	}
}

// Row 3: the start writes only what the owner confirmed. A proposal whose
// plan changed since the confirmation (here a CLAUDE.md created at the
// repository root, so the pointer stubs plan differently) is refused
// squad_yaml_digest_mismatch before anything is written or started.
func TestTheStartRefusesAPlanThatChangedSinceTheConfirmation(t *testing.T) {
	f, s, digest := startWorld(t, autoFleet(0), "owner/app")
	mustWrite(t, filepath.Join(f.proj, "CLAUDE.md"), "# notes\n")
	before := sharedSnapshot()
	r := s.start(context.Background(), leadStartRequest{Kind: leadStartKind, Digest: digest, Project: f.proj})
	if r.Outcome != "refused" || !strings.Contains(r.Detail, team.CodeDigestMismatch) {
		t.Fatalf("start = %+v", r)
	}
	if _, err := os.Stat(team.SquadPath(f.proj)); err == nil {
		t.Fatal("squad.yaml written for a digest the owner did not confirm")
	}
	if len(sharedDelta(before)) != 0 {
		t.Fatalf("steps ran: %v", sharedDelta(before))
	}
}

// Row 6: one implementation. up runs the same compile, session, doctor and
// roster functions (its tmux lead, `lead --team`, the same lead lock and
// scheduler functions), counted on the shared functions themselves.
func TestUpAndTheLeadTeamCountOnTheSameSharedSteps(t *testing.T) {
	f, _ := upWorld(t)
	before := sharedSnapshot()
	if r, err := runUpJSON(t, f.proj); err != nil {
		t.Fatalf("up: %v %+v", err, r.Steps)
	}
	if got, want := sharedDelta(before), map[string]int{"compile": 1, "session": 1, "doctor": 1, "roster": 1}; !maps.Equal(got, want) {
		t.Fatalf("up's shared steps %v, want %v", got, want)
	}
	_, _, _, _ = teamLeadFixture(t)
	before = sharedSnapshot()
	if _, _, err := captureOutput(t, func() error { return runLead([]string{"--team"}) }); err != nil {
		t.Fatal(err)
	}
	if got, want := sharedDelta(before), map[string]int{"lead_lock": 1, "scheduler": 1}; !maps.Equal(got, want) {
		t.Fatalf("lead --team's shared steps %v, want %v", got, want)
	}
}

// Row 6: a team start while a team lead holds the root's lead lock refuses
// lead_running, starts no scheduler, and leaves squad.yaml as it was (the
// mayor rolls the write back).
func TestTheStartRefusesLeadRunningWhileATeamLeadHoldsTheRoot(t *testing.T) {
	f, s, digest := startWorld(t, autoFleet(0), "owner/app")
	held := holdLock(t, filepath.Join(schedproc.Dir(f.root), schedproc.LeadLockName))
	defer held.Close()
	stamp, _ := procinfo.StartToken(os.Getpid())
	writeLeadRecord(t, f.root, schedproc.LeadRecord{PID: os.Getpid(), Start: stamp, Pane: "%9", At: time.Now()})
	before := sharedSnapshot()
	r := s.start(context.Background(), leadStartRequest{Kind: leadStartKind, Digest: digest, Project: f.proj})
	if r.Refusal != schedproc.ReasonLeadRunning || !strings.Contains(r.Detail, "pid") {
		t.Fatalf("start = %+v", r)
	}
	if sharedDelta(before)["scheduler"] != 0 {
		t.Fatal("a scheduler started under another lead")
	}
	if _, err := os.Stat(team.SquadPath(f.proj)); err == nil {
		t.Fatal("squad.yaml stayed after a refused start")
	}
}

// Row 3: the mayor's library checks (internal/fleet, R40) on the start's
// path refuse by name: a repository outside the fleet grant (R40's
// fallback: the lead proposes, the owner starts, with the owner's two
// commands), a cost cap with no meter (fail-closed), and a root with no
// fleet grant at all. Nothing is written and no step runs.
func TestTheMayorsChecksRefuseByNameOnTheStartsPath(t *testing.T) {
	noFleet := autoFleet(0)
	noFleet.Fleet = grants.Fleet{}
	for name, c := range map[string]struct {
		g        grants.Resolved
		identity string
		want     string
		fallback bool
	}{
		"a repository outside the grant": {autoFleet(0), "other/repo", fleet.ReasonRepository, true},
		"a cost cap":                     {autoFleet(5), "owner/app", fleet.ReasonCostUnmetered, false},
		"no fleet grant":                 {noFleet, "owner/app", fleet.ReasonNoFleetGrant, true},
	} {
		t.Run(name, func(t *testing.T) {
			f, s, digest := startWorld(t, c.g, c.identity)
			before := sharedSnapshot()
			r := s.start(context.Background(), leadStartRequest{Kind: leadStartKind, Digest: digest, Project: f.proj})
			if r.Outcome != "refused" || r.Refusal != c.want {
				t.Fatalf("start = %+v, want %s", r, c.want)
			}
			if c.fallback != (len(r.OwnerSteps) == 2 && slices.Equal(r.OwnerSteps[1], []string{"amq-squad", "up", "--project", f.proj})) || c.fallback != strings.Contains(r.Detail, "the lead proposes, the owner starts") {
				t.Fatalf("fallback %v: %+v", c.fallback, r)
			}
			if len(sharedDelta(before)) != 0 {
				t.Fatalf("steps ran: %v", sharedDelta(before))
			}
			if _, err := os.Stat(team.SquadPath(f.proj)); err == nil {
				t.Fatal("squad.yaml written on a refusal")
			}
		})
	}
}

// Rows 1a and 3 through the socket: the apply under the solo lead (its
// start socket in the environment) reaches the lead's process; a process
// that does not descend from the lead is refused before its request is
// read; with no confirmation the lead refuses naming the doors.
func TestTheApplyReachesTheLeadOverItsSocketAndOnlyFromItsDescendants(t *testing.T) {
	f, s, digest := startWorld(t, autoFleet(0), "owner/app")
	h, _ := digestHex(digest)
	if err := os.Remove(confirmPath(s.root, h)); err != nil {
		t.Fatal(err)
	}
	sock, err := s.serve()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("AM_ROOT", s.root)
	t.Setenv(leadStartSocketEnv, sock)
	out, _, err := captureOutput(t, func() error { return runTeam([]string{"apply", "--digest", digest, "--project", f.proj, "--json"}) })
	if err == nil || !strings.HasPrefix(err.Error(), ReasonConfirmMissing+":") || !strings.Contains(err.Error(), "/squad confirm") {
		t.Fatalf("apply over the socket = %v\n%s", err, out)
	}
	var env struct {
		Data leadStartReply `json:"data"`
	}
	if json.Unmarshal([]byte(out), &env) != nil || env.Data.Refusal != ReasonConfirmMissing {
		t.Fatalf("reply %s", out)
	}
	prev := leadStartPeer
	t.Cleanup(func() { leadStartPeer = prev })
	leadStartPeer = func(*net.UnixConn) (int, bool) { return 1, true }
	if _, _, err := captureOutput(t, func() error { return runTeam([]string{"apply", "--digest", digest, "--project", f.proj}) }); err == nil || !strings.HasPrefix(err.Error(), "origin_not_lead:") {
		t.Fatalf("a foreign peer = %v", err)
	}
}

// The solo lead (no squad, no --team) gives its pi the solo flag and the
// start socket, and serves the socket from its own process.
func TestTheSoloLeadServesTheStartSocketToItsPi(t *testing.T) {
	call, _ := piFixture(t)
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	mustMkdir(t, filepath.Join(dir, ".git"))
	chdir(t, dir)
	for _, k := range []string{"AM_ME", "AM_ROOT", "AM_BASE_ROOT", "AM_SESSION"} {
		t.Setenv(k, "")
	}
	var served *leadStarter
	prev := serveLeadStart
	t.Cleanup(func() { serveLeadStart = prev })
	serveLeadStart = func(s *leadStarter) (string, error) { served = s; return "/tmp/lead-start.sock", nil }
	if _, _, err := captureOutput(t, func() error { return runLead(nil) }); err != nil {
		t.Fatal(err)
	}
	if served == nil || served.project != dir || served.root != call.getenv("AM_ROOT") || served.self != os.Getpid() {
		t.Fatalf("starter %+v", served)
	}
	if call.getenv("AMQ_SQUAD_SOLO_LEAD") != "1" || call.getenv(leadStartSocketEnv) != "/tmp/lead-start.sock" || call.getenv("AMQ_SQUAD_LEAD") != "" {
		t.Fatalf("solo env: solo %q sock %q lead %q", call.getenv("AMQ_SQUAD_SOLO_LEAD"), call.getenv(leadStartSocketEnv), call.getenv("AMQ_SQUAD_LEAD"))
	}
}

// The scheduler a solo lead starts accepts the lead's pools and proves the
// lead's decisions by its parent, as for `lead --team`.
func TestASoloLeadIsALeadParentOfItsScheduler(t *testing.T) {
	for args, want := range map[string]bool{
		"/usr/local/bin/amq-squad lead --team": true, "amq-squad lead": true, "amq-squad": true,
		"amq-squad up": false, "bash -c amq-squad": false, "amq-squad seat-exec --seat x": false,
	} {
		if leadParentArgs(args) != want {
			t.Errorf("leadParentArgs(%q) = %v, want %v", args, !want, want)
		}
	}
}
