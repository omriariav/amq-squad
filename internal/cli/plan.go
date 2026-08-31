package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/avivsinai/agent-message-queue/launchapi"

	"github.com/omriariav/amq-squad/v2/internal/adoptionseam"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

// runPlan implements `amq-squad plan SESSION [--profile] [--json]` (gh#756):
// a zero-write preview that compiles the selected profile and its active
// brief through internal/launchintent.Compile into a
// launchapi.PrepareRequestV1, calls adoptionseam.Prepare (the same seam
// launchapiTeamLaunchBackend uses -- see prepare in
// team_launch_launchapi.go, reused here directly so plan and the launch
// path can never drift), and prints the PrepareResultV1.
//
// plan never writes to disk, AMQ, or launch state: it always runs with
// DryRun set, and adoptionseam.Prepare/launchapi.Prepare are themselves
// read-only and deterministic (see docs/amq-0.73.0-adoption-verdict.md).
// With --json, the emitted envelope embeds the exact PrepareRequestV1 that
// was sent verbatim (not re-derived), so replaying it through
// launchapi.Prepare reproduces the identical subject_digest and plan_digest
// (see TestPlanJSONRequestRoundTripsThroughAMQLaunchPrepare).
func runPlan(args []string) error {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		printPlanUsage()
		return nil
	}
	session, rest, ok := peelPositional(args)
	if !ok {
		return usageErrorf("plan requires a SESSION argument, e.g. 'amq-squad plan issue-96'")
	}
	session = strings.TrimSpace(session)
	if session == "" {
		return usageErrorf("plan requires a non-empty SESSION argument")
	}

	fs := flag.NewFlagSet("plan", flag.ContinueOnError)
	projectFlag := fs.String("project", "", "project/team-home directory (default: current directory)")
	profileFlag := fs.String("profile", "", "team profile to plan (default: default profile)")
	jsonOut := fs.Bool("json", false, "emit a schema-versioned JSON envelope containing the exact PrepareRequestV1")
	fs.Usage = printPlanUsage
	if err := parseFlags(fs, rest); err != nil {
		return err
	}
	if extra := fs.Args(); len(extra) > 0 {
		return usageErrorf("plan accepts exactly one SESSION argument; unexpected extra argument(s): %v", extra)
	}

	resolvedContext, err := resolveCanonicalContext(contextResolveOptions{
		ProjectFlag: *projectFlag, ProfileFlag: *profileFlag, SessionFlag: session,
		ProjectExplicit: flagWasSet(fs, "project"), ProfileExplicit: flagWasSet(fs, "profile"), SessionExplicit: true,
	})
	if err != nil {
		return err
	}
	emitContextDiagnostics(resolvedContext)

	prepared, err := planPrepare(resolvedContext.ProjectDir, resolvedContext.Profile, resolvedContext.Session)
	if err != nil {
		return err
	}

	if *jsonOut {
		return printJSONEnvelope("plan", planEnvelopeData{Request: prepared.Request, Result: prepared.Result})
	}
	printPlanResult(os.Stdout, prepared.Result)
	return nil
}

func printPlanUsage() {
	fmt.Fprint(os.Stderr, `amq-squad plan - zero-write preview of a team launch via launchapi.Prepare

Usage:
  amq-squad plan SESSION [--project DIR] [--profile NAME] [--json]

plan compiles the selected profile and its active brief through
internal/launchintent.Compile into a launchapi.PrepareRequestV1, calls
adoptionseam.Prepare, and prints the resulting PrepareResultV1 (planned
writes, required actions, roster drift, capabilities, placement preview).
It never writes to disk, AMQ, or launch state.

With --json, the emitted envelope carries the exact PrepareRequestV1 that
was sent, so 'amq launch --plan - --prepare --json' on it reproduces the
identical subject_digest and plan_digest.

Examples:
  amq-squad plan issue-96
  amq-squad plan issue-96 --profile review --json
`)
}

// planPrepare resolves the team for profile/session and runs it through the
// same two-phase adoptionseam.Prepare seam launchapiTeamLaunchBackend uses
// for a real launch (cto's decision on task/t6: never a hand-built
// PrepareRequestV1). opts.DryRun is always true here: a floor violation on
// any member surfaces as data in the result rather than failing plan
// closed, since a preview must still be able to describe a broken team.
func planPrepare(project, profile, session string) (adoptionseam.Prepared, error) {
	return planPrepareFiltered(project, profile, session, nil)
}

// planPrepareFiltered is planPrepare with an optional role subset (gh#758's
// resume fold: `resume --role a,b`'s plan-only preview restricts the
// roster the same way --exec does, both by calling this one function).
// roles == nil previews the full roster, unchanged from planPrepare's prior
// behavior -- plan.go's own call site never passes a filter.
func planPrepareFiltered(project, profile, session string, roles []string) (adoptionseam.Prepared, error) {
	t, err := team.ReadProfile(project, profile)
	if err != nil {
		return adoptionseam.Prepared{}, fmt.Errorf("read team: %w", err)
	}
	if len(t.Members) == 0 {
		return adoptionseam.Prepared{}, fmt.Errorf("team has no members")
	}
	active, skipped := filterMembersBySession(t.Members, session)
	if len(active) == 0 {
		return adoptionseam.Prepared{}, fmt.Errorf("no team members are pinned to session %q", session)
	}
	for _, m := range skipped {
		quietNotice("notice: skipping %s: pinned to session %q, not %q\n", m.Role, m.Session, session)
	}
	t.Members = active
	if len(roles) > 0 {
		memberRoles := make(map[string]bool, len(t.Members))
		for _, m := range t.Members {
			memberRoles[strings.ToLower(m.Role)] = true
		}
		var unknown []string
		var selected []team.Member
		wantRole := make(map[string]bool, len(roles))
		for _, role := range roles {
			if err := ensureTargetIsNotOperator(t, "resume", role); err != nil {
				return adoptionseam.Prepared{}, err
			}
			wantRole[role] = true
			if !memberRoles[role] {
				unknown = append(unknown, role)
			}
		}
		if len(unknown) > 0 {
			return adoptionseam.Prepared{}, usageErrorf("--role: no team member(s) with role %s (team roles: %s)",
				strings.Join(unknown, ", "), strings.Join(teamRoleList(t), ", "))
		}
		for _, m := range t.Members {
			if wantRole[strings.ToLower(m.Role)] {
				selected = append(selected, m)
			}
		}
		t.Members = selected
	}

	trustMode, err := resolveTeamTrustMode(t, "", false)
	if err != nil {
		return adoptionseam.Prepared{}, err
	}

	briefPath := squadnamespace.BriefPath(project, profile, session)
	startupPrompts := make(map[string]string, len(t.Members))
	for _, m := range t.Members {
		startupPrompts[m.Role] = "Read .amq-squad/team-rules.md and your brief at " + briefPath + "."
	}

	opts := teamLaunchOptions{
		Profile:        profile,
		Workstream:     session,
		Trust:          trustMode,
		DryRun:         true,
		SquadBin:       teamSquadBin(),
		StartupPrompts: startupPrompts,
	}

	backend := launchapiTeamLaunchBackend{}
	prepared, _, err := backend.prepare(t, opts)
	if err != nil {
		return adoptionseam.Prepared{}, err
	}
	return prepared, nil
}

// planEnvelopeData is the kind="plan" --json payload. Request is embedded
// verbatim (not re-derived from Result) so a caller can replay it through
// launchapi.Prepare and reproduce the identical subject_digest/plan_digest.
type planEnvelopeData struct {
	Request launchapi.PrepareRequestV1 `json:"request"`
	Result  launchapi.PrepareResultV1  `json:"result"`
}

// printPlanResult prints PrepareResultV1 in human form. required_actions
// prints amq's own action_id/kind/reason_code/allowed_decisions verbatim --
// no amq-squad paraphrase of what they mean (gh#756's
// TestPlanPrintsRequiredActionsVerbatim).
func printPlanResult(w io.Writer, result launchapi.PrepareResultV1) {
	fmt.Fprintf(w, "target: project=%s session=%s\n", result.Preview.Target.ProjectRoot, result.Preview.Target.Session)
	fmt.Fprintf(w, "outcome: %s\n", result.Outcome)
	if result.Reason != "" {
		fmt.Fprintf(w, "reason: %s\n", result.Reason)
	}
	fmt.Fprintf(w, "subject_digest: %s\n", result.SubjectDigest)
	fmt.Fprintf(w, "plan_digest: %s\n", result.PlanDigest)
	if result.TrustDigest != "" {
		fmt.Fprintf(w, "trust_digest: %s\n", result.TrustDigest)
	}
	if len(result.PlannedWrites) == 0 {
		fmt.Fprintln(w, "planned_writes: (none)")
	} else {
		fmt.Fprintln(w, "planned_writes:")
		for _, pw := range result.PlannedWrites {
			fmt.Fprintf(w, "  - write_id=%s kind=%s path=%s\n", pw.WriteID, pw.Kind, pw.Path)
		}
	}
	if len(result.RequiredActions) == 0 {
		fmt.Fprintln(w, "required_actions: (none)")
	} else {
		fmt.Fprintln(w, "required_actions:")
		for _, ra := range result.RequiredActions {
			fmt.Fprintf(w, "  - action_id=%s kind=%s reason_code=%s allowed_decisions=%v\n", ra.ActionID, ra.Kind, ra.ReasonCode, ra.AllowedDecisions)
		}
	}
	fmt.Fprintf(w, "roster: desired=%v present=%v missing=%v extra=%v\n",
		result.Preview.Roster.Desired, result.Preview.Roster.Present, result.Preview.Roster.Missing, result.Preview.Roster.Extra)
	if len(result.Preview.Capabilities) > 0 {
		fmt.Fprintln(w, "capabilities:")
		for _, cap := range result.Preview.Capabilities {
			fmt.Fprintf(w, "  - provider=%s grammar_version=%d verified_provider_version=%s\n", cap.Provider, cap.GrammarVersion, cap.VerifiedProviderVersion)
		}
	}
}
