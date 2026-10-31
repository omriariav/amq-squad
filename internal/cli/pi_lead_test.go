package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/procinfo"
	"github.com/omriariav/amq-squad/v3/internal/schedproc"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// 3.0.2 t19 fix, after brief 31-1: `lead --team` in a repository with a
// squad.yaml gives the pi lead the squad's identity (squad.yaml's lead
// handle and session) through runLead itself, takes the root's lead lock
// and starts the scheduler over that squad.yaml.

type leadSchedulerStart struct {
	project, root string
}

// teamLeadSquad is the fixture's squad.yaml: lead cto (a pi seat), session
// v31, one Codex seat.
const teamLeadSquad = `schema: 1
session: v31
lead: cto
seats:
  - handle: cto
    role: cto
    runtime: pi
  - handle: dev
    role: senior-dev
    runtime: codex
    model: gpt-5.6-sol
    effort: high
`

// teamLeadFixture installs the pi exec seam, a scheduler start seam that
// records instead of running `scheduler run`, a repository <tmp>/proj (a
// .git directory and squad.yaml; base root <proj>/.agent-mail, session v31)
// and a working directory inside it.
func teamLeadFixture(t *testing.T) (call *piExecCall, starts *[]leadSchedulerStart, project, root string) {
	t.Helper()
	call, _ = piFixture(t)
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	project = filepath.Join(dir, "proj")
	root = filepath.Join(project, ".agent-mail", "v31")
	if err := os.MkdirAll(filepath.Join(project, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(project, "squad.yaml"), teamLeadSquad)
	sub := filepath.Join(project, "internal")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	chdir(t, sub)
	var got []leadSchedulerStart
	old := startLeadScheduler
	t.Cleanup(func() { startLeadScheduler = old })
	startLeadScheduler = func(project, root string, _ []string) (*supervisor, error) {
		got = append(got, leadSchedulerStart{project: project, root: root})
		return &supervisor{}, nil
	}
	for _, k := range []string{"AM_ME", "AM_ROOT", "AM_BASE_ROOT", "AM_SESSION", leadStartEnv} {
		t.Setenv(k, "")
	}
	t.Setenv("TMUX_PANE", "%41")
	return call, &got, project, root
}

// No identity in the environment: squad.yaml fills it. The pi lead's AM_ME
// is squad.yaml's lead, its AM_ROOT the squad's session root, the scheduler
// starts over the repository, and AMQ_SQUAD_PROJECT is the repository, not
// the directory the lead started in. The lead's lock records its pid and
// pane, and the start stamp of the launch is set for the scheduler.
func TestLeadTeamTakesTheSquadIdentityFromSquadYAML(t *testing.T) {
	call, starts, project, root := teamLeadFixture(t)
	if _, _, err := captureOutput(t, func() error { return runLead([]string{"--team"}) }); err != nil {
		t.Fatal(err)
	}
	if len(*starts) != 1 || (*starts)[0] != (leadSchedulerStart{project: project, root: root}) {
		t.Fatalf("scheduler starts %+v, want one over %s", *starts, project)
	}
	for name, want := range map[string]string{
		"AM_ME": "cto", "AM_ROOT": root, "AM_BASE_ROOT": filepath.Dir(root), "AM_SESSION": "v31", "AMQ_SQUAD_PROJECT": project,
	} {
		if got := call.getenv(name); got != want {
			t.Fatalf("%s=%q, want %q", name, got, want)
		}
	}
	role, err := os.ReadFile(call.getenv("AMQ_SQUAD_ROLE_PATH"))
	if err != nil || !strings.Contains(string(role), "never write or edit code") {
		t.Fatalf("team role not delivered: %v", err)
	}
	rec, err := schedproc.ReadLeadRecord(root)
	if err != nil || rec.PID != os.Getpid() || rec.Pane != "%41" || rec.Start == "" {
		t.Fatalf("lead.json %+v %v", rec, err)
	}
	if _, err := time.Parse(time.RFC3339Nano, os.Getenv(leadStartEnv)); err != nil {
		t.Fatalf("no launch start stamp for the scheduler: %q", os.Getenv(leadStartEnv))
	}
}

// An identity in the environment that disagrees with squad.yaml is a
// refusal at start naming both values: no scheduler, no pi.
func TestLeadTeamRefusesAnEnvironmentIdentityThatDisagrees(t *testing.T) {
	for _, tc := range []struct {
		name, env, value, me, want string
	}{
		{name: "root", env: "AM_ROOT", value: "/elsewhere/.agent-mail/v31"},
		{name: "base root", env: "AM_BASE_ROOT", value: "/elsewhere/.agent-mail"},
		{name: "session", env: "AM_SESSION", value: "v30"},
		{name: "lead handle", me: "rev", want: `lead "cto" disagrees with AM_ME="rev"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			call, starts, project, root := teamLeadFixture(t)
			t.Setenv("AM_ME", "cto")
			if tc.me != "" {
				t.Setenv("AM_ME", tc.me)
			}
			t.Setenv("AM_ROOT", root)
			want := tc.want
			if tc.env != "" {
				t.Setenv(tc.env, tc.value)
				squadValue := map[string]string{"AM_ROOT": root, "AM_BASE_ROOT": filepath.Dir(root), "AM_SESSION": "v31"}[tc.env]
				want = tc.env + `="` + tc.value + `" disagrees with ` + filepath.Join(project, "squad.yaml") + ` ("` + squadValue + `")`
			}
			_, _, err := captureOutput(t, func() error { return runLead([]string{"--team"}) })
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("err %v, want it to name %s", err, want)
			}
			if len(*starts) != 0 || call.path != "" {
				t.Fatalf("started after the refusal: scheduler %+v, pi %q", *starts, call.path)
			}
		})
	}
}

// An environment identity that agrees passes: the pi lead keeps it and
// squad.yaml fills what the environment left out.
func TestLeadTeamKeepsAnAgreeingEnvironmentIdentity(t *testing.T) {
	call, starts, project, root := teamLeadFixture(t)
	t.Setenv("AM_ME", "cto")
	t.Setenv("AM_ROOT", root+string(filepath.Separator))
	if _, _, err := captureOutput(t, func() error { return runLead([]string{"--team"}) }); err != nil {
		t.Fatal(err)
	}
	if len(*starts) != 1 {
		t.Fatalf("scheduler starts %+v", *starts)
	}
	for name, want := range map[string]string{
		"AM_ME": "cto", "AM_ROOT": root, "AM_BASE_ROOT": filepath.Dir(root), "AM_SESSION": "v31", "AMQ_SQUAD_PROJECT": project,
	} {
		if got := call.getenv(name); got != want {
			t.Fatalf("%s=%q, want %q", name, got, want)
		}
	}
}

// The manager path: the lead's task tools run `amq-squad task list --json`
// (amq_task_list's argv) and `task show ID --json` with the pi lead's
// environment; against the root runLead resolved they list the squad's task.
func TestLeadTeamTaskToolsSeeTheSquadStore(t *testing.T) {
	call, _, _, root := teamLeadFixture(t)
	store, err := task.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	added, err := store.Add(task.AddInput{Title: "squad task", Class: "plan"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := captureOutput(t, func() error { return runLead([]string{"--team"}) }); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"AM_ME", "AM_ROOT", "AM_BASE_ROOT", "AM_SESSION"} {
		t.Setenv(k, call.getenv(k))
	}
	for _, args := range [][]string{{"list", "--json"}, {"show", added.ID, "--json"}} {
		out, _, err := captureOutput(t, func() error { return runTask(args) })
		if err != nil || !strings.Contains(out, added.ID) || !strings.Contains(out, "squad task") {
			t.Fatalf("task %v through the lead's identity: %v\n%s", args, err, out)
		}
	}
}

// The lead's root is the scheduler's: squadOfProject (scheduler run's
// reading of the same squad.yaml) normalizes to the root the pi lead gets.
func TestLeadTeamRootIsTheSchedulersRoot(t *testing.T) {
	call, _, project, _ := teamLeadFixture(t)
	if _, _, err := captureOutput(t, func() error { return runLead([]string{"--team"}) }); err != nil {
		t.Fatal(err)
	}
	q, lead, err := squadOfProject(project)
	if err != nil {
		t.Fatal(err)
	}
	q.Wrapper, q.AMQPath = "/bin/amq-squad", "/bin/amq"
	sched, err := q.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	if got := call.getenv("AM_ROOT"); got != sched.SessionRoot() || lead != call.getenv("AM_ME") {
		t.Fatalf("pi lead AM_ROOT %q (lead %q), the scheduler's %q (lead %q)", got, call.getenv("AM_ME"), sched.SessionRoot(), lead)
	}
}

// Brief 31-1, row 11: a second `lead --team` on the same root refuses
// lead_running, naming the first lead's pid and tmux pane, before its
// scheduler starts and before pi runs.
func TestSecondLeadOnTheSameRootIsRefused(t *testing.T) {
	call, starts, _, root := teamLeadFixture(t)
	pid := os.Getpid()
	stamp, ok := procinfo.StartToken(pid)
	if !ok {
		t.Skip("no start stamp on this platform")
	}
	first, err := schedproc.AcquireLeadLock(root, schedproc.LeadRecord{PID: pid, Start: stamp, Pane: "%7"})
	if err != nil {
		t.Fatal(err)
	}
	defer first.Release()
	_, _, err = captureOutput(t, func() error { return runLead([]string{"--team"}) })
	if err == nil || !strings.Contains(err.Error(), schedproc.ReasonLeadRunning) || !strings.Contains(err.Error(), fmt.Sprintf("pid %d", pid)) || !strings.Contains(err.Error(), "tmux pane %7") {
		t.Fatalf("a second lead: %v", err)
	}
	if len(*starts) != 0 || call.path != "" {
		t.Fatalf("started after the refusal: scheduler %+v, pi %q", *starts, call.path)
	}
	first.Release()
	if _, _, err := captureOutput(t, func() error { return runLead([]string{"--team"}) }); err != nil || len(*starts) != 1 {
		t.Fatalf("after the first lead ended: %v (starts %+v)", err, *starts)
	}
}

// Brief 31-1, row 12: a lead whose pi exits non-zero ends through piExit
// (os.Exit, which skips the deferred stop); its scheduler is stopped first.
func TestLeadExitingNonZeroStopsItsScheduler(t *testing.T) {
	call, _, _, _ := teamLeadFixture(t)
	call.code = 3
	stopped := false
	done := make(chan struct{})
	close(done)
	startLeadScheduler = func(string, string, []string) (*supervisor, error) {
		return &supervisor{cancel: func() { stopped = true }, done: done}, nil
	}
	exitStopped := false
	prev := piExit
	piExit = func(code int) { exitStopped = stopped; prev(code) }
	if _, _, err := captureOutput(t, func() error { return runLead([]string{"--team"}) }); err != nil {
		t.Fatal(err)
	}
	if call.exit != 3 || !exitStopped {
		t.Fatalf("exit %d, the scheduler stopped before the exit: %t", call.exit, exitStopped)
	}
}
