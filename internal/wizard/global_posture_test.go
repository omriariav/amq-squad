package wizard

import (
	"bytes"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// #455 item 2: a codex NOC under default sandboxing cannot drive tmux at all, and the
// wizard never asked -- operators hand-typed sandbox flags into free-text native args.
// These cover the structured posture step, both flows, and the visible-degradation rule.

func TestGlobalPostureArgsAreEmittedPerBinary(t *testing.T) {
	cases := []struct {
		agent, posture, wantFlag string
	}{
		{"codex", "full-access", "--sandbox danger-full-access"},
		{"codex", "workspace-write", "--sandbox workspace-write"},
		{"codex", "read-only", "--sandbox read-only"},
		{"claude", "full-access", "--dangerously-skip-permissions"},
	}
	for _, tc := range cases {
		t.Run(tc.agent+"/"+tc.posture, func(t *testing.T) {
			got := globalPostureArgs(tc.agent, tc.posture)
			if !strings.Contains(got, tc.wantFlag) {
				t.Errorf("globalPostureArgs(%q,%q) = %q, want it to contain %q", tc.agent, tc.posture, got, tc.wantFlag)
			}
		})
	}
}

// The tmux-capable posture must be FIRST for every binary: a global orchestrator that
// cannot drive tmux cannot do its job, so the working choice must not be second.
func TestTmuxCapablePostureIsListedFirst(t *testing.T) {
	for _, agent := range []string{"codex", "claude"} {
		choices := GlobalPostureChoices(agent)
		if len(choices) == 0 {
			t.Fatalf("no postures for %q", agent)
		}
		if !choices[0].DrivesTmux {
			t.Errorf("%s: first posture %q does not drive tmux", agent, choices[0].Value)
		}
	}
}

// Every posture must state its consequence, because #455's complaint is that the operator
// could not know the effect -- naming the flag is not enough.
func TestEveryPostureStatesItsConsequence(t *testing.T) {
	for _, agent := range []string{"codex", "claude"} {
		for _, c := range GlobalPostureChoices(agent) {
			if strings.TrimSpace(c.Consequence) == "" {
				t.Errorf("%s/%s has no consequence text", agent, c.Value)
			}
			if !c.DrivesTmux && !strings.Contains(c.Label, "CANNOT") {
				t.Errorf("%s/%s cannot drive tmux but its label does not say so: %q", agent, c.Value, c.Label)
			}
		}
	}
}

// Posture args PREPEND operator free text so an explicitly typed flag still wins: later
// flags generally take precedence, so the picker assists rather than overrides.
func TestPostureArgsPrecedeOperatorFreeText(t *testing.T) {
	s := Spec{
		Backend: BackendGlobalStart, GlobalRoot: "/tmp/root", GlobalAgent: "codex",
		GlobalPosture: "full-access", GlobalCodexArgs: "--sandbox read-only",
	}
	args := strings.Join(s.GlobalArgs(), " ")
	posture := strings.Index(args, "danger-full-access")
	operator := strings.Index(args, "--sandbox read-only")
	if posture < 0 || operator < 0 {
		t.Fatalf("expected both postures present in %q", args)
	}
	if posture > operator {
		t.Errorf("posture args must PRECEDE operator free text so the operator's flag wins; got %q", args)
	}
}

// Unknown posture is non-breaking but must NOT be invisible: silently applying no sandbox
// flags recreates the item-2 failure through the back door, and the operator has LESS
// reason to suspect it because they used the posture step.
func TestUnknownPostureDegradesVisibly(t *testing.T) {
	if got := globalPostureArgs("codex", "typo-posture"); got != "" {
		t.Errorf("unknown posture must apply no args, got %q", got)
	}
	line := globalPostureReviewLine("codex", "typo-posture")
	for _, want := range []string{"unknown posture", "typo-posture", "no sandbox flags applied"} {
		if !strings.Contains(line, want) {
			t.Errorf("review line %q must contain %q so the degradation is visible", line, want)
		}
	}
}

// The review gate shows the CONSEQUENCE, not just the stored value: the operator approves
// an outcome.
func TestReviewLineStatesTheTmuxConsequence(t *testing.T) {
	capable := globalPostureReviewLine("codex", "full-access")
	if !strings.Contains(capable, "can drive tmux") {
		t.Errorf("capable posture line %q must state it can drive tmux", capable)
	}
	restricted := globalPostureReviewLine("codex", "read-only")
	if !strings.Contains(restricted, "CANNOT drive tmux") {
		t.Errorf("restricted posture line %q must state it CANNOT drive tmux", restricted)
	}
}

// The tests above exercise the helpers. Findings 1 and 2 of #568's review lived in ADAPTER
// WIRING that the helpers never see: defaultCursor omitted the posture stage, and an unknown
// stored value was coerced to index 0. Helper-level tests plus adapter-level features is the
// union-vs-component disease again, so these drive each adapter END TO END.

func TestStoredSaferPostureSurvivesAcceptingDefaultsInBubble(t *testing.T) {
	defaults := Spec{
		Scope: "global", GlobalRoot: "/neutral", GlobalAgent: "codex",
		GlobalPosture: "workspace-write", GlobalWindow: "global-orch",
	}
	m, err := NewBubbleModel(NumberedOptions{Defaults: defaults})
	if err != nil {
		t.Fatal(err)
	}
	m.stage = stageGlobalPosture
	m.configureStage()
	m.cursor = m.defaultCursor()

	rows := m.choices()
	if got := rows[m.cursor].value; got != "workspace-write" {
		t.Fatalf("cursor sits on %q, want the STORED workspace-write; accepting defaults would escalate to %q", got, rows[0].value)
	}

	// Press Enter and assert the COMMITTED spec. Stopping at cursor inspection would let a
	// commitChoice wiring regression restore the escalation invisibly.
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.spec.GlobalPosture != "workspace-write" {
		t.Errorf("committed posture = %q, want workspace-write", m.spec.GlobalPosture)
	}
	if args := strings.Join(m.spec.GlobalArgs(), " "); strings.Contains(args, "danger-full-access") {
		t.Errorf("committing defaults escalated to danger-full-access: %s", args)
	}
}

func TestStoredSaferPostureSurvivesAcceptingDefaultsInNumbered(t *testing.T) {
	defaults := Spec{
		Scope: "global", GlobalRoot: "/neutral", GlobalAgent: "codex",
		GlobalPosture: "workspace-write", GlobalWindow: "global-orch",
	}
	got, err := RunNumbered(strings.NewReader(strings.Repeat("\n", 30)), &bytes.Buffer{}, NumberedOptions{Defaults: defaults})
	if err != nil {
		t.Fatal(err)
	}
	if got.GlobalPosture != "workspace-write" {
		t.Errorf("numbered posture = %q, want the stored workspace-write preserved", got.GlobalPosture)
	}
	if strings.Contains(strings.Join(got.GlobalArgs(), " "), "danger-full-access") {
		t.Errorf("accepting defaults escalated to danger-full-access: %v", got.GlobalArgs())
	}
}

func TestUnknownStoredPostureIsNotCoercedInBubble(t *testing.T) {
	defaults := Spec{
		Scope: "global", GlobalRoot: "/neutral", GlobalAgent: "codex",
		GlobalPosture: "typo-posture", GlobalWindow: "global-orch",
	}
	m, err := NewBubbleModel(NumberedOptions{Defaults: defaults})
	if err != nil {
		t.Fatal(err)
	}
	m.stage = stageGlobalPosture
	m.configureStage()
	m.cursor = m.defaultCursor()

	rows := m.choices()
	if got := rows[m.cursor].value; got != "typo-posture" {
		t.Fatalf("cursor sits on %q, want the unknown typo-posture offered and preserved", got)
	}
	if !strings.Contains(rows[m.cursor].label, "no sandbox flags applied") {
		t.Errorf("the unknown row must say what it does: %q", rows[m.cursor].label)
	}

	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.spec.GlobalPosture != "typo-posture" {
		t.Errorf("committed posture = %q, want the unknown value preserved", m.spec.GlobalPosture)
	}
	if args := strings.Join(m.spec.GlobalArgs(), " "); strings.Contains(args, "--sandbox") {
		t.Errorf("an unknown posture must commit NO sandbox flags, got: %s", args)
	}
}

func TestUnknownStoredPostureIsNotCoercedInNumbered(t *testing.T) {
	defaults := Spec{
		Scope: "global", GlobalRoot: "/neutral", GlobalAgent: "codex",
		GlobalPosture: "typo-posture", GlobalWindow: "global-orch",
	}
	var out bytes.Buffer
	got, err := RunNumbered(strings.NewReader(strings.Repeat("\n", 30)), &out, NumberedOptions{Defaults: defaults})
	if err != nil {
		t.Fatal(err)
	}
	if got.GlobalPosture != "typo-posture" {
		t.Errorf("numbered posture = %q, want the unknown value preserved rather than coerced", got.GlobalPosture)
	}
	// The whole point of the ruled degradation path: no flags, and the operator can see it.
	if args := strings.Join(got.GlobalArgs(), " "); strings.Contains(args, "--sandbox") {
		t.Errorf("an unknown posture must apply NO sandbox flags, got: %s", args)
	}
	if !strings.Contains(out.String(), "unknown posture typo-posture") {
		t.Errorf("the preview must render the degradation visibly:\n%s", out.String())
	}
}

// Finding 1 of #568 round 2: the escalation was re-openable through case or whitespace,
// because rows were matched case-insensitively while the cursor compared exactly. One
// canonicalization now feeds both.
func TestStoredPostureSurvivesCaseAndWhitespace(t *testing.T) {
	for _, stored := range []string{"WORKSPACE-WRITE", "  workspace-write  ", "Workspace-Write"} {
		t.Run(stored, func(t *testing.T) {
			m, err := NewBubbleModel(NumberedOptions{Defaults: Spec{
				Scope: "global", GlobalRoot: "/neutral", GlobalAgent: "codex",
				GlobalPosture: stored, GlobalWindow: "global-orch",
			}})
			if err != nil {
				t.Fatal(err)
			}
			m.stage = stageGlobalPosture
			m.configureStage()
			m.cursor = m.defaultCursor()
			if got := m.choices()[m.cursor].value; got != "workspace-write" {
				t.Errorf("stored %q put the cursor on %q; a case or whitespace variant must not escalate", stored, got)
			}
		})
	}
}

// Finding of #568 convergence: two helpers compared the same stored value independently,
// one trimming and one raw, so a padded-but-VALID posture applied sandbox flags while the
// review line called it unknown and claimed none were applied. Args and review disagreed
// about what would happen, which breaks the review-gate contract that an operator approves
// an OUTCOME.
//
// This asserts agreement rather than checking each helper separately, because separate
// correct-looking checks are exactly what let the disagreement through.
func TestArgsAndReviewAgreeOnTheSameStoredPosture(t *testing.T) {
	cases := []string{
		"workspace-write", " workspace-write ", "WORKSPACE-WRITE", "\tread-only\n",
		"full-access", " FULL-ACCESS ", "typo-posture", "  typo-posture  ",
	}
	for _, stored := range cases {
		t.Run(strings.TrimSpace(stored), func(t *testing.T) {
			args := globalPostureArgs("codex", stored)
			review := globalPostureReviewLine("codex", stored)

			appliesFlags := strings.Contains(args, "--sandbox")
			claimsNoFlags := strings.Contains(review, "no sandbox flags applied")

			if appliesFlags && claimsNoFlags {
				t.Errorf("stored %q: args APPLY %q while the review claims no flags are applied:\n  %s",
					stored, args, review)
			}
			if !appliesFlags && !claimsNoFlags {
				t.Errorf("stored %q: args apply NOTHING but the review does not say so:\n  %s",
					stored, review)
			}
		})
	}
}
