package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/omriariav/amq-squad/v2/internal/rules"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

// initUsage documents `amq-squad init` (gh#762): the single idempotent
// project-init verb that replaces `new team`, `new profile`, `team init`,
// `team rules init`, and `team sync --apply`. Those five become deprecation
// redirects (still functional -- see runNewTeam/runNewProfile/
// runTeamInitWithOptions/runTeamRules's "init" case/runTeamSync) that print
// a notice and internally delegate.
const initUsage = `amq-squad init - create or refresh this project's team profile, team-rules.md, and pointer stubs

Usage:
  amq-squad init [--profile NAME] [--roles ...] [--binary ...] [--lead ...] [--project DIR] [other 'team init' options] [--json]
  amq-squad init [...] --apply INIT_DIGEST

Without --apply: computes the exact planned writes (profile JSON,
team-rules.md, CLAUDE.md/AGENTS.md pointer stubs) and prints a preview plus
an init_digest -- a deterministic content hash over those planned writes.
Touches NO files. This is a zero-write preview, the same shape as 'plan' and
'start's own preview/--apply <digest> flow, except init_digest is an
amq-squad-internal digest with no relationship to launchapi's SubjectDigest
(plan/start/wizard) -- do not confuse the two.

With --apply INIT_DIGEST: recomputes the same planned writes fresh and
compares the digest; refuses closed on any mismatch (the underlying inputs
changed since the digest was printed). On a match, writes the profile JSON
and team-rules.md (creating or refreshing them) and applies the pointer-stub
plan. Re-running init against an existing, unchanged profile computes the
SAME digest and performs no observable write.

Accepts every 'amq-squad team init' flag (--roles, --binary, --lead,
--session, --operator, --orchestrated, etc.) -- init is a thin wrapper over
the same underlying team-construction logic, adding only the digest-preview/
apply gate on top.

Examples:
  amq-squad init --roles cto,qa --dry-run
  amq-squad init --roles cto,qa
  amq-squad init --profile review --roles cto --apply 3f2a...
`

func runInit(args []string) error {
	if len(args) > 0 && wantsHelp(args) {
		fmt.Fprint(os.Stderr, initUsage)
		return nil
	}
	applyDigest, jsonOut, rest, err := peelInitApplyFlag(args)
	if err != nil {
		return err
	}
	plan, err := computeInitPlan(rest)
	if err != nil {
		return err
	}
	if applyDigest == "" {
		return printInitPreview(plan, jsonOut)
	}
	if applyDigest != plan.Digest {
		return usageErrorf("init --apply digest %q does not match a fresh recompute (%q); the planned writes changed since this digest was printed -- rerun init without --apply to get the current digest", applyDigest, plan.Digest)
	}
	return applyInitPlan(rest, plan, jsonOut)
}

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

// peelInitApplyFlag extracts --apply DIGEST and --json from args, returning
// the remainder unchanged for forwarding into team init's own flag parsing
// (init deliberately does not re-declare team init's ~50 flags a second
// time; everything but --apply/--json passes through verbatim).
func peelInitApplyFlag(args []string) (applyDigest string, jsonOut bool, rest []string, err error) {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			out = append(out, args[i:]...)
			break
		}
		switch {
		case a == "--apply":
			if i+1 >= len(args) {
				return "", false, nil, usageErrorf("--apply requires an init_digest value")
			}
			if applyDigest != "" {
				return "", false, nil, usageErrorf("--apply may be passed only once")
			}
			applyDigest = strings.TrimSpace(args[i+1])
			if applyDigest == "" {
				return "", false, nil, usageErrorf("--apply requires an init_digest value")
			}
			i++
		case strings.HasPrefix(a, "--apply="):
			applyDigest = strings.TrimSpace(strings.TrimPrefix(a, "--apply="))
			if applyDigest == "" {
				return "", false, nil, usageErrorf("--apply requires an init_digest value")
			}
		case a == "--json":
			jsonOut = true
			out = append(out, a)
		default:
			out = append(out, a)
		}
	}
	return applyDigest, jsonOut, out, nil
}

// initPreviewData is the `init --json` (no --apply) envelope payload.
type initPreviewData struct {
	Profile      string   `json:"profile"`
	TeamHome     string   `json:"team_home"`
	Members      int      `json:"members"`
	PointerStubs []string `json:"pointer_stubs"`
	InitDigest   string   `json:"init_digest"`
	ApplyCommand string   `json:"apply_command"`
}

func printInitPreview(plan initPlan, jsonOut bool) error {
	if jsonOut {
		var stubs []string
		for _, p := range plan.PointerPlans {
			stubs = append(stubs, p.Target)
		}
		return printJSONEnvelope("init_preview", initPreviewData{
			Profile:      squadDisplayProfile(plan.Team),
			TeamHome:     plan.Team.Project,
			Members:      len(plan.Team.Members),
			PointerStubs: stubs,
			InitDigest:   plan.Digest,
			ApplyCommand: fmt.Sprintf("amq-squad init --apply %s", plan.Digest),
		})
	}
	fmt.Printf("init preview: team_home=%s members=%d\n", plan.Team.Project, len(plan.Team.Members))
	for _, p := range plan.PointerPlans {
		state := "unchanged"
		switch {
		case p.Creating:
			state = "create"
		case !p.Unchanged:
			state = "update"
		}
		fmt.Printf("  pointer stub %s: %s\n", p.Target, state)
	}
	fmt.Printf("init_digest: %s\n", plan.Digest)
	fmt.Printf("Apply with: amq-squad init --apply %s\n", plan.Digest)
	return nil
}

// squadDisplayProfile has no reliable single field on team.Team naming the
// profile it was loaded/planned for (Team carries project/session/roster,
// not its own profile slug) -- init's caller already knows the --profile it
// passed, but the plan captured from team init's internals does not thread
// it back out. Report "default" when nothing more specific is knowable; this
// only affects the preview's cosmetic Profile field, never init_digest or
// the actual write path (team init resolves --profile independently, the
// same way it always has).
func squadDisplayProfile(t team.Team) string {
	return team.DefaultProfile
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
