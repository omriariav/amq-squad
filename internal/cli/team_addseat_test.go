package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/rules"
	"github.com/omriariav/amq-squad/v3/internal/team"
)

// Brief 31-2 PR 2, change 6 (row 4, the verb's half): `team propose
// --add-seat` plans squad.yaml with the one seat appended and writes
// nothing; `team apply --digest D --add-seat` (the add key's exact two
// verbs) writes it under that digest, and the team rules the owner
// approved stay as they were. A stale digest, a malformed spec, a seat
// that repeats a handle, and --add-seat with --roles are refused; a model
// holding colons (an Ollama tag) keeps them. Reverted (no --add-seat), the
// flag is unknown.
func TestTeamAddSeatAppendsOneSeatThroughTheDigest(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	project, _ := filepath.EvalSymlinks(t.TempDir())
	writeSquadProject(t, project, "schema: 1\nsession: s1\nlead: lead\nseats:\n"+
		"  - handle: lead\n    role: lead\n    runtime: pi\n"+
		"  - handle: impl-1\n    role: implementer\n    runtime: claude\n    model: sonnet\n    effort: medium\n", "lead", "implementer")
	const ownRules = "# Team rules\n\nThe owner's own prose, approved once.\n"
	mustWrite(t, rules.Path(project), ownRules)
	before := snapshotTree(t, project)
	add := []string{"--project", project, "--add-seat", "rev-1:implementer:codex:gpt-5.4:high"}
	p := teamProposeJSON(t, append([]string{"propose"}, add...)...)
	if snap := snapshotTree(t, project); len(snap) != len(before) || snap[team.SquadPath(project)] != before[team.SquadPath(project)] {
		t.Fatal("team propose --add-seat changed the tree")
	}
	if p.Seats != 3 || p.Files[0].Action != "replace" || p.Files[1].Action != "unchanged" {
		t.Fatalf("the proposal: %+v", p)
	}
	if _, _, err := captureOutput(t, func() error { return runTeam(append([]string{"apply", "--digest", "sha256:stale"}, add...)) }); err == nil || !strings.Contains(err.Error(), "squad_yaml_digest_mismatch") {
		t.Fatalf("a stale digest: %v", err)
	}
	applied := teamProposeJSON(t, append([]string{"apply", "--digest", p.Digest}, add...)...)
	if applied.Digest != p.Digest {
		t.Fatalf("apply digest %s, proposed %s", applied.Digest, p.Digest)
	}
	s, err := team.LoadSquad(project)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Seats) != 3 || s.Seats[2] != (team.Seat{Handle: "rev-1", Role: "implementer", Runtime: "codex", Model: "gpt-5.4", Effort: "high"}) || s.Lead != "lead" || s.Session != "s1" {
		t.Fatalf("squad.yaml after the add: %+v", s)
	}
	if got, _ := os.ReadFile(rules.Path(project)); string(got) != ownRules {
		t.Fatalf("the team rules changed:\n%s", got)
	}
	// a pi seat whose model holds a colon, with no effort and the role empty
	spec := "local-1::pi:ollama/qwen3:32b:"
	mustWrite(t, team.CustomRolePath(project, "local-1"), "# Role: local-1\n\nA local worker.\n")
	p = teamProposeJSON(t, "propose", "--project", project, "--add-seat", spec)
	teamProposeJSON(t, "apply", "--digest", p.Digest, "--project", project, "--add-seat", spec)
	if s, err = team.LoadSquad(project); err != nil || s.Seats[3] != (team.Seat{Handle: "local-1", Role: "local-1", Runtime: "pi", Model: "ollama/qwen3:32b"}) {
		t.Fatalf("the pi seat: %+v %v", s.Seats, err)
	}
	for _, bad := range [][]string{
		{"--add-seat", "rev-2:implementer:codex"},                              // malformed
		{"--add-seat", "impl-1:implementer:claude:sonnet:medium"},              // a repeated handle
		{"--add-seat", "rev-2:implementer:codex:gpt-5.4:high", "--roles", "x"}, // with --roles
	} {
		if _, _, err := captureOutput(t, func() error { return runTeam(append([]string{"propose", "--project", project}, bad...)) }); err == nil {
			t.Fatalf("%v was accepted", bad)
		}
	}
}
