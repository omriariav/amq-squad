package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/bootstrapack"
	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

// seedLaunchReservation writes a launch reservation for a namespace generation,
// which is the durable proof that a launch was attempted.
func seedLaunchReservation(t *testing.T, project, profile, session, generation string) string {
	t.Helper()
	path := preparedRunReservationPath(project, profile, session, generation)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"kind":"launch_reservation"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// seedAcceptedRoster writes the GENERATION manifest naming the accepted initial
// roster, so the escalation can tell an accepted member from a bystander.
//
// This writes the generation's own manifest, not the pointer at
// preparedRunPath: the row reads the roster of the generation that reserved the
// launch, deliberately bypassing pointer/digest validation so a drifted
// preparation cannot silence the failure.
func seedAcceptedRoster(t *testing.T, project, profile, session, generation string, roles ...string) {
	t.Helper()
	path := preparedRunGenerationManifestPath(project, profile, session, generation)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(preparedRunManifest{
		Project: project, Profile: profile, Session: session,
		InitialRoster: roles,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestFindPreparedLaunchAttemptRequiresPositiveProof pins the predicate's
// fail-closed direction. Everything that is not a readable reservation must
// report "no evidence", because the escalation it feeds turns a skip into a
// hard failure and must never fire on a directory listing error.
func TestFindPreparedLaunchAttemptRequiresPositiveProof(t *testing.T) {
	project := t.TempDir()
	profile, session := team.DefaultProfile, "ns"

	if _, ok := findPreparedLaunchAttempt(project, profile, session); ok {
		t.Error("a namespace with no prepared tree at all reported a launch attempt")
	}

	// A generation directory with no reservation is preparation without a
	// launch, which is exactly the case that must stay silent.
	if err := os.MkdirAll(preparedRunGenerationDir(project, profile, session, "aaa111"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, ok := findPreparedLaunchAttempt(project, profile, session); ok {
		t.Error("a prepared generation with no reservation reported a launch attempt")
	}

	want := seedLaunchReservation(t, project, profile, session, "aaa111")
	got, ok := findPreparedLaunchAttempt(project, profile, session)
	if !ok {
		t.Fatal("a real reservation was not recognized as proof of a launch attempt")
	}
	if got.Generation != "aaa111" || got.Path != want {
		t.Errorf("evidence = %+v, want generation aaa111 at %s", got, want)
	}
}

// doctorBootstrapRowFixture builds a single-member team whose AMQ root exists
// but whose agent has no launch record, and returns the doctor execution plus
// the agent directory.
func doctorBootstrapRowFixture(t *testing.T, project, profile, session, role string) (doctorExecution, string) {
	t.Helper()
	root := filepath.Join(project, ".agent-mail", session)
	agentDir := filepath.Join(root, "agents", role)
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := team.WriteProfile(project, profile, team.Team{
		Project: project,
		Members: []team.Member{{Role: role, Handle: role, Binary: "claude", Session: session, CWD: project}},
	}); err != nil {
		t.Fatal(err)
	}
	d := newDoctorExec(t, project)
	d.ProjectDir = project
	d.Profile = profile
	d.WorkstreamHint = session
	return d, agentDir
}

func bootstrapRowFor(t *testing.T, checks []doctorCheck, role string) doctorCheck {
	t.Helper()
	for _, c := range checks {
		if c.Name == "bootstrap/"+role {
			return c
		}
	}
	t.Fatalf("no bootstrap row for %q in %+v", role, checks)
	return doctorCheck{}
}

// TestDoctorBootstrapRowFailsWhenAReservedLaunchLeftNoRecord is the #598 root
// cause 3 regression for the doctor half.
//
// During the fresh-namespace brick, doctor reported EVERY row ok, including
// `bootstrap/<role>: no launch record; skipped`, while the namespace was
// unusable and the agents had died seconds after spawning. A launch was
// reserved for those exact roles, so "no launch record" was not honest silence,
// it was the missing diagnostic.
func TestDoctorBootstrapRowFailsWhenAReservedLaunchLeftNoRecord(t *testing.T) {
	project := t.TempDir()
	profile, session, role := team.DefaultProfile, "bricked", "cto"
	d, agentDir := doctorBootstrapRowFixture(t, project, profile, session, role)

	reservation := seedLaunchReservation(t, project, profile, session, "gen0001")
	seedAcceptedRoster(t, project, profile, session, "gen0001", role)

	row := bootstrapRowFor(t, doctorCheckBootstrap(d), role)
	if row.Status != doctorFail {
		t.Fatalf("a reserved launch that left no record must FAIL, got %s: %s", row.Status, row.Detail)
	}
	// Actionability: the operator must be able to act without reading source.
	for _, want := range []string{role, "gen0001", reservation, "absent", bootstrapLaunchRecordPath(agentDir)} {
		if !strings.Contains(row.Detail, want) {
			t.Errorf("failure detail omits %q\n  detail: %s", want, row.Detail)
		}
	}
}

// TestDoctorBootstrapRowDistinguishesMalformedFromAbsent keeps the two failures
// from sharing a message. An agent that died before writing a record and one
// that wrote a corrupt record need different remedies.
func TestDoctorBootstrapRowDistinguishesMalformedFromAbsent(t *testing.T) {
	project := t.TempDir()
	profile, session, role := team.DefaultProfile, "corrupt", "cto"
	d, agentDir := doctorBootstrapRowFixture(t, project, profile, session, role)

	seedLaunchReservation(t, project, profile, session, "gen0001")
	seedAcceptedRoster(t, project, profile, session, "gen0001", role)
	if err := os.MkdirAll(filepath.Dir(launch.Path(agentDir)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(launch.Path(agentDir), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	row := bootstrapRowFor(t, doctorCheckBootstrap(d), role)
	if row.Status != doctorFail {
		t.Fatalf("a reserved launch with a malformed record must FAIL, got %s: %s", row.Status, row.Detail)
	}
	if !strings.Contains(row.Detail, "malformed") {
		t.Errorf("detail must say malformed, not absent: %s", row.Detail)
	}
}

// TestDoctorBootstrapRowStaysSilentWithoutAReservation is the false-positive
// guard, and it is the reason the predicate exists at all. Members legitimately
// have no launch record: never launched, staged but not started, an external
// lead adopted in a pane amq-squad did not spawn. Failing those would make
// doctor noisy for people who did nothing wrong, which is its own way of hiding
// a real problem.
func TestDoctorBootstrapRowStaysSilentWithoutAReservation(t *testing.T) {
	project := t.TempDir()
	profile, session, role := team.DefaultProfile, "never-launched", "cto"
	d, _ := doctorBootstrapRowFixture(t, project, profile, session, role)

	// Preparation exists and names the roster, but no launch was ever reserved.
	seedAcceptedRoster(t, project, profile, session, "gen0001", role)

	row := bootstrapRowFor(t, doctorCheckBootstrap(d), role)
	if row.Status != doctorOK {
		t.Fatalf("no reservation means nothing was attempted and silence is honest; got %s: %s", row.Status, row.Detail)
	}
}

// TestDoctorBootstrapRowStaysSilentForAMemberOutsideTheAcceptedRoster is the
// second half of the predicate. A reservation proves a launch was attempted for
// the ACCEPTED roster, and says nothing about a member who was not part of it.
func TestDoctorBootstrapRowStaysSilentForAMemberOutsideTheAcceptedRoster(t *testing.T) {
	project := t.TempDir()
	profile, session, role := team.DefaultProfile, "bystander", "cto"
	d, _ := doctorBootstrapRowFixture(t, project, profile, session, role)

	seedLaunchReservation(t, project, profile, session, "gen0001")
	// The accepted roster names a DIFFERENT role.
	seedAcceptedRoster(t, project, profile, session, "gen0001", "someone-else")

	row := bootstrapRowFor(t, doctorCheckBootstrap(d), role)
	if row.Status != doctorOK {
		t.Fatalf("a member outside the accepted initial roster must not be failed by another member's reservation; got %s: %s", row.Status, row.Detail)
	}
}

// TestDoctorBootstrapReservedRelaunchInsideGraceIsNotFailed is Reviewer A's
// false-positive, reproduced and closed.
//
// bootstrapack.Evaluate consults the grace period ONLY when the marker is
// missing. A marker that exists but belongs to a PREVIOUS launch returns
// "mismatch" immediately, and ordinary relaunch never removes the old marker:
// launch.go touches bootstrapack state nowhere, and the only removals live in
// the staged-launch rollback path. So a healthy agent that was just relaunched
// has a stale marker, a fresh expectation, and a reservation.
//
// Without the grace gate, doctor reported FAIL for a member that was starting
// normally and simply had not re-acknowledged yet. That is exactly the
// "failed while having done nothing wrong" case the predicate is supposed to
// exclude.
func TestDoctorBootstrapReservedRelaunchInsideGraceIsNotFailed(t *testing.T) {
	issued := time.Now().UTC()
	mismatch := bootstrapack.Result{
		State:    "mismatch",
		Required: true,
		IssuedAt: &issued,
		Detail:   "bootstrap acknowledgement does not match the current launch identity",
	}

	// Inside the window: still starting, must not be failed even with a
	// reservation.
	if got := doctorBootstrapStatus(mismatch, true, issued.Add(bootstrapack.GracePeriod/2)); got != doctorWarn {
		t.Errorf("reserved relaunch INSIDE the ack grace period = %s, want warn (the agent is still starting)", got)
	}
	// Boundary: exactly at expiry is outside the window.
	if got := doctorBootstrapStatus(mismatch, true, issued.Add(bootstrapack.GracePeriod)); got != doctorFail {
		t.Errorf("reserved mismatch AT grace expiry = %s, want fail", got)
	}
	// Past the window: the launch genuinely did not complete.
	if got := doctorBootstrapStatus(mismatch, true, issued.Add(2*bootstrapack.GracePeriod)); got != doctorFail {
		t.Errorf("reserved mismatch AFTER the grace period = %s, want fail", got)
	}
	// No reservation: unchanged historical behavior in and out of the window.
	for _, at := range []time.Time{issued.Add(bootstrapack.GracePeriod / 2), issued.Add(2 * bootstrapack.GracePeriod)} {
		if got := doctorBootstrapStatus(mismatch, false, at); got != doctorWarn {
			t.Errorf("unreserved mismatch = %s, want warn", got)
		}
	}
	// A missing issue time must not become an indefinite amnesty.
	noIssue := bootstrapack.Result{State: "mismatch", Required: true}
	if got := doctorBootstrapStatus(noIssue, true, issued); got != doctorFail {
		t.Errorf("reserved mismatch with no issue time = %s, want fail (cannot prove it is inside the window)", got)
	}
	// Nor must a FUTURE one. Found by Reviewer B on the third pass over this
	// function: a bad clock at launch or a hand-edited record would otherwise
	// hold a dead agent at WARN for as long as the timestamp stays ahead. Same
	// class as the missing-timestamp case directly above, so it fails closed
	// the same way.
	future := issued.Add(24 * time.Hour)
	futureIssue := bootstrapack.Result{State: "mismatch", Required: true, IssuedAt: &future}
	if got := doctorBootstrapStatus(futureIssue, true, issued); got != doctorFail {
		t.Errorf("reserved mismatch with a FUTURE issue time = %s, want fail (a future stamp must not extend the window)", got)
	}
}
