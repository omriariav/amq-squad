package team

import (
	"reflect"
	"strings"
	"testing"

	"github.com/avivsinai/agent-message-queue/launchapi"

	"github.com/omriariav/amq-squad/v3/internal/grants"
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

// fixtureCaps is the capability advertisement shape AMQ returns from Prepare
// (ProviderCapabilitiesV1.AllowedArgumentForms and ConfigOverrides).
func fixtureCaps() []launchapi.ProviderCapabilitiesV1 {
	return []launchapi.ProviderCapabilitiesV1{
		{Provider: "claude", GrammarVersion: 1, AllowedArgumentForms: []string{"--model", "--effort", "-n", "--name"}},
		{Provider: "codex", GrammarVersion: 1, AllowedArgumentForms: []string{"--model", "-c", "--sandbox", "--ask-for-approval"},
			ConfigOverrides: []launchapi.ConfigOverrideCapabilityV1{{Key: "model_reasoning_effort", AllowedValues: []string{"low", "medium", "high"}}}},
		{Provider: "pi", GrammarVersion: 1},
	}
}

// Acceptance 4: one Claude, one Codex and one pi seat compile to one typed
// seat list for the launch request; the pi seat carries no provider argument.
func TestSquadCompilesToOneLaunchSeatList(t *testing.T) {
	s, err := ParseSquad(threeRuntimeSquad, "squad.yaml", grants.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	seats := s.LaunchSeats("/repo")
	want := []LaunchSeat{
		{Handle: "lead", Runtime: "claude", Model: "opus", Effort: "high", Role: "cto", Worktree: "/repo"},
		{Handle: "dev", Runtime: "codex", Model: "gpt-5.5", Effort: "medium", Role: "fullstack", Worktree: "/dev-wt"},
		{Handle: "scout", Runtime: "pi", Role: "researcher", Worktree: "/repo"},
	}
	if !reflect.DeepEqual(seats, want) {
		t.Fatalf("seats = %+v", seats)
	}
	if got := ProviderArgs(seats[0]); !reflect.DeepEqual(got, []string{"--model", "opus", "--effort", "high"}) {
		t.Fatalf("claude args = %v", got)
	}
	if got := ProviderArgs(seats[1]); !reflect.DeepEqual(got, []string{"--model", "gpt-5.5", "-c", "model_reasoning_effort=medium", "--sandbox", "workspace-write", "--ask-for-approval", "on-request"}) {
		t.Fatalf("codex args = %v", got)
	}
	if got := ProviderArgs(seats[2]); got != nil {
		t.Fatalf("pi args = %v, want none", got)
	}
	if err := CheckArgumentForms(seats, fixtureCaps()); err != nil {
		t.Fatalf("advertised forms refused: %v", err)
	}
}

// Acceptance 5: a form the fixture advertisement does not list is refused
// before launch, and so is a -c override outside the advertised values.
func TestArgumentFormsComeFromTheAdvertisement(t *testing.T) {
	s, err := ParseSquad(threeRuntimeSquad, "squad.yaml", grants.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	seats := s.LaunchSeats("/repo")
	noEffort := fixtureCaps()
	noEffort[0].AllowedArgumentForms = []string{"--model"}
	if err := CheckArgumentForms(seats, noEffort); err == nil || !strings.Contains(err.Error(), "does not admit --effort for claude") {
		t.Fatalf("unlisted form = %v", err)
	}
	badOverride := fixtureCaps()
	badOverride[1].ConfigOverrides[0].AllowedValues = []string{"low"}
	if err := CheckArgumentForms(seats, badOverride); err == nil || !strings.Contains(err.Error(), "-c model_reasoning_effort=medium") {
		t.Fatalf("unlisted override = %v", err)
	}
	if err := CheckArgumentForms(seats, fixtureCaps()[:1]); err == nil || !strings.Contains(err.Error(), "no argument forms for codex") {
		t.Fatalf("missing provider = %v", err)
	}
	// pi needs no advertisement: it carries no provider argument
	if err := CheckArgumentForms(seats[2:], nil); err != nil {
		t.Fatalf("pi seat = %v", err)
	}
}
