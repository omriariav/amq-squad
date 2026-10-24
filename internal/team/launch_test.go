package team

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/avivsinai/agent-message-queue/launchapi"

	"github.com/omriariav/amq-squad/v3/internal/grants"
	"github.com/omriariav/amq-squad/v3/internal/seatlaunch"
)

const threeRuntimeSquad = `schema: 1
session: s1
seats:
  - handle: lead
    role: cto
    runtime: claude
    model: opus
    effort: high
  - handle: dev
    role: fullstack
    runtime: codex
    model: gpt-5.5
    effort: medium
    worktree: ../dev-wt
  - handle: scout
    role: researcher
    runtime: pi
`

// fixtureCaps is the capability advertisement AMQ returns from Prepare.
func fixtureCaps() []launchapi.ProviderCapabilitiesV1 {
	return []launchapi.ProviderCapabilitiesV1{
		{Provider: "claude", GrammarVersion: 1, AllowedArgumentForms: []string{"--model", "--effort", "--permission-mode", "-n", "--name"}},
		{Provider: "codex", GrammarVersion: 1, AllowedArgumentForms: []string{"--model", "-c", "--sandbox", "--ask-for-approval"},
			ConfigOverrides: []launchapi.ConfigOverrideCapabilityV1{{Key: "model_reasoning_effort", AllowedValues: []string{"low", "medium", "high"}}}},
		{Provider: "pi", GrammarVersion: 1},
	}
}

func roleText(id string) (string, error) {
	return "# Role: " + id + "\n\nThe " + id + " seat of this squad.\n", nil
}

func compileSquad(t *testing.T, text string) (seatlaunch.Squad, string) {
	t.Helper()
	s, err := ParseSquad(text, "squad.yaml", grants.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(base, "repo")
	seats, err := s.LaunchSeats(repo, roleText)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, ".agent-mail", "s1", "agents"), 0o700); err != nil {
		t.Fatal(err)
	}
	q := seatlaunch.Squad{ProjectRoot: repo, Session: s.Session, Wrapper: "/opt/amq-squad/bin/amq-squad", AMQPath: "/opt/homebrew/bin/amq", Seats: seats}
	if q, err = q.Normalize(); err != nil {
		t.Fatalf("2b-2's compiler refused the squad: %v", err)
	}
	return q, base
}

func lookPath(name string) (string, error) { return "/usr/local/bin/" + name, nil }

// Acceptance 4: a squad.yaml with one Claude, one Codex and one pi seat is
// 2b-2's typed seat list; the pi seat carries no provider argument. With the
// pinned AMQ library, which has no pi launch adapter yet (upstream PR #1188,
// seatlaunch.PiAdapterInLibrary), the compiler refuses the pi seat by name and
// compiles the Claude and Codex seats to one launch request.
func TestSquadCompilesToOneLaunchRequest(t *testing.T) {
	q, base := compileSquad(t, threeRuntimeSquad)
	want := []seatlaunch.Seat{
		{Handle: "lead", Runtime: "claude", Model: "opus", Effort: "high", Role: "# Role: cto\n\nThe cto seat of this squad.\n", RoleName: "cto", Worktree: filepath.Join(base, "repo")},
		{Handle: "dev", Runtime: "codex", Model: "gpt-5.5", Effort: "medium", Role: "# Role: fullstack\n\nThe fullstack seat of this squad.\n", RoleName: "fullstack", Worktree: filepath.Join(base, "dev-wt")},
		{Handle: "scout", Runtime: "pi", Role: "# Role: researcher\n\nThe researcher seat of this squad.\n", RoleName: "researcher", Worktree: filepath.Join(base, "repo")},
	}
	if !reflect.DeepEqual(q.Seats, want) {
		t.Fatalf("seats = %+v", q.Seats)
	}
	if got := seatlaunch.ProviderArgs(q.Seats[2]); got != nil {
		t.Fatalf("pi args = %v, want none", got)
	}
	if err := seatlaunch.CheckArgumentForms(q, fixtureCaps()); err != nil {
		t.Fatalf("advertised forms refused: %v", err)
	}
	_, err := seatlaunch.Compile(q, "", lookPath, time.Unix(1, 0))
	if seatlaunch.PiAdapterInLibrary {
		if err != nil {
			t.Fatalf("compile: %v", err)
		}
	} else if err == nil || !strings.Contains(err.Error(), "seat scout") || !strings.Contains(err.Error(), "pi launch adapter") {
		t.Fatalf("pi seat without the library adapter = %v", err)
	}
	q.Seats = q.Seats[:2]
	c, err := seatlaunch.Compile(q, "", lookPath, time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Request.Validate(); err != nil {
		t.Fatal(err)
	}
	if ps := c.Request.Intent.Participants; len(ps) != 3 || ps[1].Handle != "lead" || ps[2].Handle != "dev" {
		t.Fatalf("participants %+v", ps)
	}
}

// Acceptance 5: a form the fixture advertisement does not list is refused
// before launch, so is a -c override outside the advertised values.
func TestArgumentFormsComeFromTheAdvertisement(t *testing.T) {
	q, _ := compileSquad(t, threeRuntimeSquad)
	noEffort := fixtureCaps()
	noEffort[0].AllowedArgumentForms = []string{"--model"}
	if err := seatlaunch.CheckArgumentForms(q, noEffort); err == nil || !strings.Contains(err.Error(), "does not admit --effort for claude") {
		t.Fatalf("unlisted form = %v", err)
	}
	badOverride := fixtureCaps()
	badOverride[1].ConfigOverrides[0].AllowedValues = []string{"low"}
	if err := seatlaunch.CheckArgumentForms(q, badOverride); err == nil || !strings.Contains(err.Error(), "-c model_reasoning_effort=medium") {
		t.Fatalf("unlisted override = %v", err)
	}
	if err := seatlaunch.CheckArgumentForms(q, fixtureCaps()[:1]); err == nil || !strings.Contains(err.Error(), "no argument forms for codex") {
		t.Fatalf("missing provider = %v", err)
	}
}

func TestLaunchSeatsReportsAMissingRole(t *testing.T) {
	s, err := ParseSquad(threeRuntimeSquad, "squad.yaml", grants.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.LaunchSeats("/repo", func(id string) (string, error) { return "", os.ErrNotExist })
	if err == nil || !strings.Contains(err.Error(), "seat lead") {
		t.Fatalf("missing role = %v", err)
	}
}
