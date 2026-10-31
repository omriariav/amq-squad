package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/seatlaunch"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// 3.0.2 t19 fix: `lead --team --scheduler-seats FILE` gives the pi lead the
// squad's identity through runLead itself, not only through the helper.

type leadSchedulerStart struct {
	seats, lead string
	trust       bool
}

// teamLeadFixture installs the pi exec seam, a scheduler start seam that
// records instead of running `scheduler run`, a seats file for project
// <tmp>/proj session v31 (base root defaulted to <proj>/.agent-mail), and a
// working directory that is another checkout.
func teamLeadFixture(t *testing.T) (call *piExecCall, starts *[]leadSchedulerStart, seats, project, root string) {
	t.Helper()
	call, _ = piFixture(t)
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	project = filepath.Join(dir, "proj")
	root = filepath.Join(project, ".agent-mail", "v31")
	seats = filepath.Join(dir, "seats.json")
	raw, _ := json.Marshal(map[string]any{"project_root": project, "session": "v31", "seats": []any{}})
	if err := os.WriteFile(seats, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(dir, "other-checkout")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	chdir(t, other)
	var got []leadSchedulerStart
	old := startLeadScheduler
	t.Cleanup(func() { startLeadScheduler = old })
	startLeadScheduler = func(seats, lead string, _ []string, trust bool) (*supervisor, error) {
		got = append(got, leadSchedulerStart{seats: seats, lead: lead, trust: trust})
		return &supervisor{}, nil
	}
	for _, k := range []string{"AM_ME", "AM_ROOT", "AM_BASE_ROOT", "AM_SESSION"} {
		t.Setenv(k, "")
	}
	return call, &got, seats, project, root
}

// No identity in the environment: the seats file fills it. The pi lead's
// AM_ME is the --lead handle, its AM_ROOT the seats file's session root, the
// scheduler gets the same handle, and the repo tools' AMQ_SQUAD_PROJECT is
// the seats file's project_root, not the directory the lead started in.
func TestLeadTeamTakesTheSquadIdentityFromTheSeatsFile(t *testing.T) {
	call, starts, seats, project, root := teamLeadFixture(t)
	if _, _, err := captureOutput(t, func() error {
		return runLead([]string{"--team", "--scheduler-seats", seats, "--lead", "cto", "--trust"})
	}); err != nil {
		t.Fatal(err)
	}
	if len(*starts) != 1 || (*starts)[0] != (leadSchedulerStart{seats: seats, lead: "cto", trust: true}) {
		t.Fatalf("scheduler starts %+v, want one with lead cto", *starts)
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
}

// An identity in the environment that disagrees with the seats file is a
// refusal at start naming both values: no scheduler, no pi.
func TestLeadTeamRefusesAnEnvironmentIdentityThatDisagrees(t *testing.T) {
	for _, tc := range []struct {
		name, env, value, want string
		args                   []string
	}{
		{name: "root", env: "AM_ROOT", value: "/elsewhere/.agent-mail/v31"},
		{name: "base root", env: "AM_BASE_ROOT", value: "/elsewhere/.agent-mail"},
		{name: "session", env: "AM_SESSION", value: "v30"},
		{name: "lead handle", args: []string{"--lead", "rev"}, want: `--lead "rev" disagrees with AM_ME="cto"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			call, starts, seats, _, root := teamLeadFixture(t)
			t.Setenv("AM_ME", "cto")
			t.Setenv("AM_ROOT", root)
			want := tc.want
			if tc.env != "" {
				t.Setenv(tc.env, tc.value)
				seatsValue := map[string]string{"AM_ROOT": root, "AM_BASE_ROOT": filepath.Dir(root), "AM_SESSION": "v31"}[tc.env]
				want = tc.env + `="` + tc.value + `" disagrees with the seats file ` + seats + ` ("` + seatsValue + `")`
			}
			_, _, err := captureOutput(t, func() error {
				return runLead(append([]string{"--team", "--scheduler-seats", seats}, tc.args...))
			})
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("err %v, want it to name %s", err, want)
			}
			if len(*starts) != 0 || call.path != "" {
				t.Fatalf("started after the refusal: scheduler %+v, pi %q", *starts, call.path)
			}
		})
	}
}

// An environment identity that agrees passes: the scheduler gets AM_ME, the
// pi lead keeps it, the seats file fills what the environment left out, and
// AMQ_SQUAD_PROJECT is the seats file's project_root.
func TestLeadTeamKeepsAnAgreeingEnvironmentIdentity(t *testing.T) {
	call, starts, seats, project, root := teamLeadFixture(t)
	t.Setenv("AM_ME", "cto")
	t.Setenv("AM_ROOT", root+string(filepath.Separator))
	if _, _, err := captureOutput(t, func() error { return runLead([]string{"--team", "--scheduler-seats", seats}) }); err != nil {
		t.Fatal(err)
	}
	if len(*starts) != 1 || (*starts)[0].lead != "cto" {
		t.Fatalf("scheduler starts %+v, want lead cto", *starts)
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
	call, _, seats, _, root := teamLeadFixture(t)
	store, err := task.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	added, err := store.Add(task.AddInput{Title: "squad task", Class: "plan"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := captureOutput(t, func() error { return runLead([]string{"--team", "--scheduler-seats", seats}) }); err != nil {
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

// The lead's root is the scheduler's: a seats file whose base_root carries a
// trailing space gives the pi lead the AM_ROOT that scheduler run's own
// normalization (seatlaunch.Squad.Normalize, SessionRoot) produces, and an
// environment identity equal to that root is accepted, not refused.
func TestLeadTeamRootIsTheSchedulersNormalizedRoot(t *testing.T) {
	for _, withEnv := range []bool{false, true} {
		t.Run(map[bool]string{false: "seats file fills it", true: "environment agrees"}[withEnv], func(t *testing.T) {
			call, starts, seats, project, _ := teamLeadFixture(t)
			base := filepath.Join(filepath.Dir(seats), "mail") + " "
			raw, _ := json.Marshal(map[string]any{"project_root": project, "base_root": base, "session": "v31", "seats": []any{}})
			if err := os.WriteFile(seats, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			q, err := readLaunchSeats(seats)
			if err != nil {
				t.Fatal(err)
			}
			q.Wrapper, q.AMQPath = "/bin/amq-squad", "/bin/amq"
			q.Seats = []seatlaunch.Seat{{Handle: "dev", Runtime: "pi", Worktree: "/w", Role: "You are dev."}}
			sched, err := q.Normalize()
			if err != nil {
				t.Fatal(err)
			}
			want := sched.SessionRoot()
			if !strings.HasSuffix(want, "mail /v31") {
				t.Fatalf("the scheduler's root %q lost the base root's trailing space; the row proves nothing", want)
			}
			if withEnv {
				t.Setenv("AM_ME", "cto")
				t.Setenv("AM_ROOT", want)
				t.Setenv("AM_BASE_ROOT", sched.BaseRoot)
				t.Setenv("AM_SESSION", sched.Session)
			}
			if _, _, err := captureOutput(t, func() error { return runLead([]string{"--team", "--scheduler-seats", seats}) }); err != nil {
				t.Fatal(err)
			}
			if len(*starts) != 1 {
				t.Fatalf("scheduler starts %+v", *starts)
			}
			if got := call.getenv("AM_ROOT"); got != want {
				t.Fatalf("pi lead AM_ROOT %q, want the scheduler's %q", got, want)
			}
			if got := call.getenv("AM_BASE_ROOT"); got != sched.BaseRoot {
				t.Fatalf("pi lead AM_BASE_ROOT %q, want the scheduler's %q", got, sched.BaseRoot)
			}
		})
	}
}
