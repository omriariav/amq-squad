package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/omriariav/amq-squad/v3/internal/rules"
	"github.com/omriariav/amq-squad/v3/internal/team"
)

// initPlan is the fully-computed set of writes `init` would perform, plus
// the deterministic digest over them.
type initPlan struct {
	Team         team.Team
	RulesContent string
	PointerPlans []rules.SyncPlan
	Digest       string
}

// computeInitPlan runs team init's own plan-then-write logic in capture mode
// (teamInitRunOptions.CapturePlan) to get the planned team.Team and rendered
// team-rules.md content WITHOUT writing or dry-run-printing anything, then
// computes the pointer-stub plan from that same rendered content, and
// finally the init_digest over all three. Called once for a bare preview and
// again, fresh, right before an --apply write -- never trusting a
// caller-supplied plan across that boundary (the same ABA-safety
// rules.Apply's own verifyPlanCurrent already enforces for the pointer-stub
// half).
func computeInitPlan(rest []string) (initPlan, error) {
	var captured *team.Team
	var capturedRules string
	captureErr := runTeamInitWithOptions(rest, teamInitRunOptions{
		CapturePlan: func(t team.Team, rulesContent string) {
			tCopy := t
			captured = &tCopy
			capturedRules = rulesContent
		},
	})
	if captureErr != nil {
		return initPlan{}, captureErr
	}
	if captured == nil {
		return initPlan{}, fmt.Errorf("init: internal error, team init did not reach the capture point")
	}
	pointerPlans, err := rules.Plan(captured.Project, capturedRules)
	if err != nil {
		return initPlan{}, fmt.Errorf("plan pointer stubs: %w", err)
	}
	digest, err := computeInitDigest(*captured, capturedRules, pointerPlans)
	if err != nil {
		return initPlan{}, err
	}
	return initPlan{Team: *captured, RulesContent: capturedRules, PointerPlans: pointerPlans, Digest: digest}, nil
}

// computeInitDigest is init's amq-squad-internal digest scheme (gh#762,
// task/t12 ruling 3) -- deliberately NOT launchapi's SubjectDigest (plan/
// start/wizard), which is a different, unrelated identifier for a different
// domain (agent launch, not project init). init_digest is a sha256 over a
// fixed, deterministic serialization of every planned write:
//
//  1. the planned team.Team, JSON-marshaled with indent (stable field order
//     -- Go's encoding/json always marshals struct fields in declaration
//     order, so this is deterministic for a fixed team.Team type);
//  2. a NUL separator;
//  3. the rendered team-rules.md content;
//  4. a NUL separator;
//  5. each planned pointer-stub write (CLAUDE.md/AGENTS.md), sorted by
//     target path for determinism (rules.Plan's own return order is not
//     documented as stable), each entry as "<path>\x00<content>\x00".
//
// Two calls with byte-identical planned writes produce the same digest;
// changing any single byte anywhere in the three inputs changes it --
// TestInitApplyRejectsStaleDigest asserts both properties directly.
func computeInitDigest(t team.Team, rulesContent string, pointerPlans []rules.SyncPlan) (string, error) {
	teamJSON, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal planned team profile: %w", err)
	}
	var b strings.Builder
	b.Write(teamJSON)
	b.WriteByte(0)
	b.WriteString(rulesContent)
	b.WriteByte(0)
	sorted := append([]rules.SyncPlan(nil), pointerPlans...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Target < sorted[j].Target })
	for _, p := range sorted {
		b.WriteString(p.Target)
		b.WriteByte(0)
		b.WriteString(p.After)
		b.WriteByte(0)
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:]), nil
}

func applyInitPlan(rest []string, plan initPlan, jsonOut bool) error {
	// A matched init_digest already proves what --force exists to protect
	// against (an unintended overwrite of an existing profile): the caller
	// just confirmed these exact planned writes by matching a fresh
	// recompute. Passing --force here is what makes "creates or refreshes"
	// idempotent -- rerunning init against an existing, unchanged profile
	// must not need a SEPARATE --force the operator never had to pass to
	// the deprecated `team init` in the create case.
	if err := runTeamInitWithOptions(append(append([]string{}, rest...), "--force"), teamInitRunOptions{}); err != nil {
		return err
	}
	freshPlans, err := rules.Plan(plan.Team.Project, plan.RulesContent)
	if err != nil {
		return fmt.Errorf("plan pointer stubs: %w", err)
	}
	n, err := rules.Apply(freshPlans)
	if err != nil {
		return fmt.Errorf("apply pointer stubs: %w", err)
	}
	if jsonOut {
		return printJSONEnvelope("init_apply", struct {
			TeamHome        string `json:"team_home"`
			Members         int    `json:"members"`
			PointerStubsSet int    `json:"pointer_stubs_written"`
			InitDigest      string `json:"init_digest"`
		}{TeamHome: plan.Team.Project, Members: len(plan.Team.Members), PointerStubsSet: n, InitDigest: plan.Digest})
	}
	fmt.Fprintf(os.Stderr, "init applied: %d pointer stub(s) written.\n", n)
	return nil
}
