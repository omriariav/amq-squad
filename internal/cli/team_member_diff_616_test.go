package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

// These tests drive the real `team member update --dry-run` command and read
// its actual stdout. #616 is about what an operator sees before approving a
// risky lead edit, so asserting on the renderer alone would not establish that
// the command shows it.

func seedDiffTeam(t *testing.T) string {
	t.Helper()
	return seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-616", Model: "gpt-5", CodexArgs: []string{"-c", "model_reasoning_effort=medium"}},
			{Role: "qa", Binary: "claude", Handle: "qa", Session: "issue-616"},
		},
	})
}

// TestMemberUpdateDryRunShowsFieldLevelDiff is the #616 regression: the preview
// must name the field and both values, not merely announce that an update would
// happen.
func TestMemberUpdateDryRunShowsFieldLevelDiff(t *testing.T) {
	seedDiffTeam(t)
	stdout, _, err := captureOutput(t, func() error {
		return runTeamMember([]string{"update", "cto", "--model", "gpt-5-codex", "--dry-run"})
	})
	if err != nil {
		t.Fatalf("member update --dry-run: %v", err)
	}
	for _, want := range []string{"FIELD", "BEFORE", "AFTER", "model", "gpt-5", "gpt-5-codex"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("preview missing %q; stdout:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "would update cto (codex) in profile") {
		t.Errorf("preview still emits the old would-update summary; stdout:\n%s", stdout)
	}
}

// TestMemberUpdateDryRunDiffReportsEffortAsWrittenArgs pins the reason the diff
// reads the actual before/after members rather than the flags that were set.
// --effort is one flag but lands in codex_args; a flag-driven diff would print
// "effort" and hide what the profile would really receive.
func TestMemberUpdateDryRunDiffReportsEffortAsWrittenArgs(t *testing.T) {
	seedDiffTeam(t)
	stdout, _, err := captureOutput(t, func() error {
		return runTeamMember([]string{"update", "cto", "--effort", "high", "--dry-run"})
	})
	if err != nil {
		t.Fatalf("member update --dry-run: %v", err)
	}
	if !strings.Contains(stdout, "codex_args") {
		t.Errorf("effort change did not surface as codex_args; stdout:\n%s", stdout)
	}
	// Codex expresses effort as `-c model_reasoning_effort=<tier>`, not `--effort
	// <tier>`. Asserting on the real native spelling is the point: the whole
	// reason this diff reads written args instead of flags is so the operator
	// sees the profile's actual content.
	if !strings.Contains(stdout, "model_reasoning_effort=medium") || !strings.Contains(stdout, "model_reasoning_effort=high") {
		t.Errorf("effort diff lost one side of the before/after; stdout:\n%s", stdout)
	}
}

// TestMemberUpdateDryRunDiffIsNotEmptyForEveryEdit guards the in-place trap.
// buildUpdated mutates t.Members[idx]; reading the "current" member after
// calling it compares the proposal against itself and yields an empty diff for
// every edit. That failure is silent — the command still exits 0 and prints a
// well-formed preview claiming nothing would change.
func TestMemberUpdateDryRunDiffIsNotEmptyForEveryEdit(t *testing.T) {
	for _, tc := range []struct{ name, flag, value string }{
		{"model", "--model", "gpt-5-codex"},
		{"handle", "--handle", "cto-2"},
		{"session", "--session", "issue-617"},
		{"actor mode", "--actor-mode", "review"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seedDiffTeam(t)
			stdout, _, err := captureOutput(t, func() error {
				return runTeamMember([]string{"update", "cto", tc.flag, tc.value, "--dry-run"})
			})
			if err != nil {
				t.Fatalf("member update --dry-run: %v", err)
			}
			if strings.Contains(stdout, "already in the requested state") {
				t.Fatalf("%s edit reported no change; the before-member was captured after buildUpdated mutated it.\nstdout:\n%s", tc.flag, stdout)
			}
			if !strings.Contains(stdout, tc.value) {
				t.Errorf("preview does not show the new value %q; stdout:\n%s", tc.value, stdout)
			}
		})
	}
}

// TestMemberUpdateDryRunNoOpSaysSo: an update that changes nothing must say so
// in words rather than print an empty table, which reads as a rendering bug.
func TestMemberUpdateDryRunNoOpSaysSo(t *testing.T) {
	seedDiffTeam(t)
	stdout, _, err := captureOutput(t, func() error {
		return runTeamMember([]string{"update", "cto", "--model", "gpt-5", "--dry-run"})
	})
	if err != nil {
		t.Fatalf("member update --dry-run: %v", err)
	}
	if !strings.Contains(stdout, "already in the requested state") {
		t.Errorf("no-op preview did not say so; stdout:\n%s", stdout)
	}
	if strings.Contains(stdout, "FIELD") {
		t.Errorf("no-op preview printed an empty diff table; stdout:\n%s", stdout)
	}
}

// TestMemberUpdateDryRunStillWritesNothing keeps the preview a preview. The
// diff reads the profile and builds a proposed member, so this asserts the
// obvious-but-critical property that none of that reaches disk.
func TestMemberUpdateDryRunStillWritesNothing(t *testing.T) {
	dir := seedDiffTeam(t)
	if _, _, err := captureOutput(t, func() error {
		return runTeamMember([]string{"update", "cto", "--model", "gpt-5-codex", "--handle", "cto-2", "--dry-run"})
	}); err != nil {
		t.Fatalf("member update --dry-run: %v", err)
	}
	members := teamMembers(t, dir)
	if members[0].Model != "gpt-5" {
		t.Errorf("dry-run mutated model: %q", members[0].Model)
	}
	if members[0].Handle != "cto" {
		t.Errorf("dry-run mutated handle: %q", members[0].Handle)
	}
}

// TestMemberUpdateDryRunJSONCarriesChanges: operators scripting a risky lead
// edit read the envelope, not the table.
func TestMemberUpdateDryRunJSONCarriesChanges(t *testing.T) {
	seedDiffTeam(t)
	stdout, _, err := captureOutput(t, func() error {
		return runTeamMember([]string{"update", "cto", "--model", "gpt-5-codex", "--dry-run", "--json"})
	})
	if err != nil {
		t.Fatalf("member update --dry-run --json: %v", err)
	}
	var envelope struct {
		Data struct {
			Status  string              `json:"status"`
			Changes []memberFieldChange `json:"changes"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatalf("unmarshal envelope: %v\n%s", err, stdout)
	}
	if envelope.Data.Status != "preview" {
		t.Errorf("status = %q, want preview", envelope.Data.Status)
	}
	if len(envelope.Data.Changes) != 1 {
		t.Fatalf("changes = %+v, want exactly one", envelope.Data.Changes)
	}
	got := envelope.Data.Changes[0]
	if got.Field != "model" || got.Before != "gpt-5" || got.After != "gpt-5-codex" {
		t.Errorf("change = %+v, want {model gpt-5 gpt-5-codex}", got)
	}
}

// TestMemberUpdateAppliedRunHasNoChangesKey confirms the envelope addition is
// additive: a real (non-preview) update is unchanged, so no other consumer of
// mutationResult sees a new key.
func TestMemberUpdateAppliedRunHasNoChangesKey(t *testing.T) {
	seedDiffTeam(t)
	stdout, _, err := captureOutput(t, func() error {
		return runTeamMember([]string{"update", "cto", "--model", "gpt-5-codex", "--json"})
	})
	if err != nil {
		t.Fatalf("member update --json: %v", err)
	}
	if strings.Contains(stdout, "\"changes\"") {
		t.Errorf("applied update envelope gained a changes key; stdout:\n%s", stdout)
	}
}

// TestMemberFieldChangesUnsetRendering: clearing a field is an edit an operator
// most wants confirmed, and an empty diff cell reads as "unchanged".
func TestMemberFieldChangesUnsetRendering(t *testing.T) {
	before := team.Member{Role: "cto", Session: "issue-616"}
	after := team.Member{Role: "cto"}
	changes := memberFieldChanges(before, after)
	if len(changes) != 1 {
		t.Fatalf("changes = %+v, want one", changes)
	}
	if changes[0].After != memberFieldUnset {
		t.Errorf("cleared field rendered as %q, want %q", changes[0].After, memberFieldUnset)
	}
}

// TestMemberArgsDiffPreservesArgvBoundaries covers the ambiguity a peer probe
// surfaced: joining argv with spaces makes a one-token arg containing a space
// indistinguishable from two separate tokens. Before this fix the two members
// below compared EQUAL, so a real edit produced an empty diff and the preview
// told the operator nothing would change.
func TestMemberArgsDiffPreservesArgvBoundaries(t *testing.T) {
	before := team.Member{Role: "qa", Binary: "claude", ClaudeArgs: []string{"--settings", "/a b/x.json"}}
	after := team.Member{Role: "qa", Binary: "claude", ClaudeArgs: []string{"--settings", "/a", "b/x.json"}}
	changes := memberFieldChanges(before, after)
	if len(changes) != 1 {
		t.Fatalf("argv boundary change produced %d changes, want 1: %+v", len(changes), changes)
	}
	if changes[0].Before == changes[0].After {
		t.Fatalf("argv boundary change rendered identically on both sides: %+v", changes[0])
	}
	if !strings.Contains(changes[0].Before, "'/a b/x.json'") {
		t.Errorf("space-bearing token was not quoted, so its boundary is invisible: %q", changes[0].Before)
	}
}

// TestMemberArgsDiffStillReportsNoChangeForIdenticalArgv is the other half:
// quoting must not make equal argv look different.
func TestMemberArgsDiffStillReportsNoChangeForIdenticalArgv(t *testing.T) {
	m := team.Member{Role: "qa", Binary: "codex", CodexArgs: []string{"-c", "model_reasoning_effort=high"}}
	if changes := memberFieldChanges(m, m); len(changes) != 0 {
		t.Fatalf("identical argv reported as changed: %+v", changes)
	}
}

// TestMemberUpdateDryRunEndToEndArgvBoundaries drives the argv-boundary case
// through the real command with real flag parsing, which is what the peer
// review asked for. parseAgentArgs is quote-aware, so `-c 'a b'` is two tokens
// and `-c a b` is three: the operator can produce this collision with ordinary
// quoting, without doing anything unusual.
func TestMemberUpdateDryRunEndToEndArgvBoundaries(t *testing.T) {
	seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-616a", CodexArgs: []string{"-c", "a b"}},
		},
	})
	stdout, _, err := captureOutput(t, func() error {
		return runTeamMember([]string{"update", "cto", "--codex-args", "-c a b", "--dry-run"})
	})
	if err != nil {
		t.Fatalf("member update --dry-run: %v", err)
	}
	if strings.Contains(stdout, "already in the requested state") {
		t.Fatalf("splitting one argv token into two was reported as no change:\n%s", stdout)
	}
	if !strings.Contains(stdout, "codex_args") {
		t.Errorf("argv change did not surface as codex_args:\n%s", stdout)
	}
}

// TestMemberUpdateDryRunJSONDistinguishesArgvBoundaries: the review required
// the JSON envelope to distinguish the edit too. A script gating on `changes`
// would otherwise see an empty array and conclude the update was a no-op.
func TestMemberUpdateDryRunJSONDistinguishesArgvBoundaries(t *testing.T) {
	seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-616b", CodexArgs: []string{"-c", "a b"}},
		},
	})
	stdout, _, err := captureOutput(t, func() error {
		return runTeamMember([]string{"update", "cto", "--codex-args", "-c a b", "--dry-run", "--json"})
	})
	if err != nil {
		t.Fatalf("member update --dry-run --json: %v", err)
	}
	var envelope struct {
		Data struct {
			Changes []memberFieldChange `json:"changes"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatalf("unmarshal envelope: %v\n%s", err, stdout)
	}
	if len(envelope.Data.Changes) != 1 {
		t.Fatalf("JSON changes = %+v, want the argv edit to appear", envelope.Data.Changes)
	}
	got := envelope.Data.Changes[0]
	if got.Before == got.After {
		t.Errorf("JSON renders both sides identically, so a script cannot see the edit: %+v", got)
	}
}

// TestMemberArgsDiffHandlesEmptyTokens covers the other half of the review's
// "spaces/empty tokens" requirement. An empty argv token is invisible under a
// plain join and must not silently vanish.
func TestMemberArgsDiffHandlesEmptyTokens(t *testing.T) {
	before := team.Member{Role: "qa", Binary: "codex", CodexArgs: []string{"-c", ""}}
	after := team.Member{Role: "qa", Binary: "codex", CodexArgs: []string{"-c"}}
	changes := memberFieldChanges(before, after)
	if len(changes) != 1 {
		t.Fatalf("dropping an empty token produced %d changes, want 1: %+v", len(changes), changes)
	}
	if changes[0].Before == changes[0].After {
		t.Fatalf("empty token is invisible on both sides: %+v", changes[0])
	}
}

// TestMemberUpdateDryRunUnsetSentinelIsInjective covers the collision a peer
// probe surfaced: memberFieldUnset is the literal "(unset)", so before this fix
// a genuine edit from model="" to model="(unset)" rendered Before and After
// identically. The preview reported one change while hiding what changed — the
// same silent-wrong-answer class as the argv collapse, one field over.
func TestMemberUpdateDryRunUnsetSentinelIsInjective(t *testing.T) {
	seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-616c"},
		},
	})
	stdout, _, err := captureOutput(t, func() error {
		return runTeamMember([]string{"update", "cto", "--model", memberFieldUnset, "--dry-run"})
	})
	if err != nil {
		t.Fatalf("member update --dry-run: %v", err)
	}
	if strings.Contains(stdout, "already in the requested state") {
		t.Fatalf("setting model to the literal %q was reported as no change:\n%s", memberFieldUnset, stdout)
	}
	// The set value must be visibly distinct from the unset sentinel, and the
	// real value must survive into the output.
	if !strings.Contains(stdout, "'"+memberFieldUnset+"'") {
		t.Errorf("literal %q was not rendered distinctly from the unset sentinel:\n%s", memberFieldUnset, stdout)
	}
}

// TestMemberUpdateDryRunJSONUnsetSentinelIsInjective is the same collision in
// the envelope. A script comparing before and after would otherwise see two
// equal strings and conclude nothing changed.
func TestMemberUpdateDryRunJSONUnsetSentinelIsInjective(t *testing.T) {
	seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-616d"},
		},
	})
	stdout, _, err := captureOutput(t, func() error {
		return runTeamMember([]string{"update", "cto", "--model", memberFieldUnset, "--dry-run", "--json"})
	})
	if err != nil {
		t.Fatalf("member update --dry-run --json: %v", err)
	}
	var envelope struct {
		Data struct {
			Changes []memberFieldChange `json:"changes"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatalf("unmarshal envelope: %v\n%s", err, stdout)
	}
	if len(envelope.Data.Changes) != 1 {
		t.Fatalf("changes = %+v, want the model edit to appear", envelope.Data.Changes)
	}
	got := envelope.Data.Changes[0]
	if got.Before == got.After {
		t.Fatalf("JSON renders both sides identically, so a script cannot see the edit: %+v", got)
	}
	if got.Before != memberFieldUnset {
		t.Errorf("Before = %q, want the unset sentinel %q", got.Before, memberFieldUnset)
	}
	if !strings.Contains(got.After, memberFieldUnset) {
		t.Errorf("After = %q, want it to preserve the actual value %q", got.After, memberFieldUnset)
	}
}
