package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// gh#758/t11 slice B: resume --exec's fold into simple_start's shared
// machinery (cto ruling, task/t11). "resume --exec becomes start --apply
// <digest>" is a shared-seam fold through runSimpleStartWithRequest, not an
// argv re-invocation of `start` -- there is no precedent elsewhere in this
// codebase for one command shelling into another's CLI entry point, and
// --role has no start equivalent by design (growing start's own flag
// surface back for a resume-specific need would reverse the shrinking
// gh#757 just did). The subject_digest binds the --role-filtered roster:
// runResumeExec's own preview probe computes it over the filtered
// SpawnTeam, and runSimpleStartWithRequest's fresh re-Prepare under the
// session lock re-applies the same RoleFilter before comparing -- a
// roster/liveness change inside the subset between the two digest-mismatches
// and refuses closed (same TOCTOU discipline as t8/gh#757).
//
// Deliberately out of scope for this commit, tracked for a follow-up in
// this slice: resume's own goal-redelivery step (--redeliver-goal) is not
// wired into this path yet; it remains reachable only via team_resume.go's
// now-legacy executeResume until that is folded in too.

type resumeExecRequest struct {
	ProjectDir      string
	Profile         string
	Session         string
	Roles           []string
	Force           bool
	TrustRaw        string
	ModelRaw        string
	EffortRaw       string
	CodexArgsRaw    string
	ClaudeArgsRaw   string
	Target          string
	Layout          string
	TerminalSession string
	Stagger         time.Duration
	LaunchVia       string
	SkipLeadCheck   bool
}

func runResumeExec(r resumeExecRequest, out io.Writer) error {
	return runResumeExecWithDependencies(r, defaultSimpleStartDependencies(), out)
}

// runResumeExecWithDependencies is runResumeExec with an injectable
// simpleStartDependencies, mirroring runStartWithDependencies's own
// testability seam so a test can exercise the real
// simpleStartLaunch -> launchapiTeamLaunchBackend.launch digest gate
// (deps.Launch left nil/default) while stubbing everything else
// (ResolveAMQEnv, LookPath, probes, ListPanes, StartWatcher) hermetically.
func runResumeExecWithDependencies(r resumeExecRequest, deps simpleStartDependencies, out io.Writer) error {
	trustMode, err := normalizeTrustMode(r.TrustRaw)
	if err != nil {
		return err
	}
	modelOverrides, err := parseKV(r.ModelRaw)
	if err != nil {
		return fmt.Errorf("parse --model: %w", err)
	}
	modelOverrides = lowercaseKeys(modelOverrides)
	effortOverrides, err := parseEffortOverrides(r.EffortRaw)
	if err != nil {
		return err
	}
	binaryArgs, err := parseBinaryArgFlags(r.CodexArgsRaw, r.ClaudeArgsRaw)
	if err != nil {
		return err
	}

	opts := teamLaunchOptions{
		Terminal: "tmux", Target: r.Target, Layout: r.Layout, TerminalSession: r.TerminalSession,
		Stagger: r.Stagger, LaunchVia: r.LaunchVia,
		Trust: trustMode, ModelOverrides: modelOverrides, EffortOverrides: effortOverrides,
		BinaryArgs: binaryArgs, Profile: r.Profile,
		NoBootstrap: true, SimpleStart: true, AllowExistingSession: true,
	}
	backend, err := resolveTeamLaunchBackend(opts)
	if err != nil {
		return err
	}
	launchapiPath := backend.Name() == "launchapi"
	if !launchapiPath && r.Force {
		return usageErrorf("resume --exec: --force-duplicate is not supported on the legacy launch path (this session resolved to --launch-via legacy); omit --force-duplicate or drop --launch-via legacy")
	}
	opts.ForceDuplicate = launchapiPath && r.Force

	req := simpleStartRequest{
		Project: r.ProjectDir, Profile: r.Profile, Session: r.Session, SessionExplicit: true,
		// Yes: true unconditionally -- resume --exec has never prompted
		// interactively (the operator already opted in by passing --exec);
		// on the launchapi path this is moot (ExpectedSubjectDigest below
		// drives that branch instead), but on the legacy path
		// (--launch-via legacy) runSimpleStartWithRequest's non-launchapi
		// branch would otherwise call confirmSimpleStart against a nil
		// reader (resume has no interactive stdin plumbed into this path).
		Yes:           true,
		Options:       opts,
		LaunchapiPath: launchapiPath,
		RoleFilter:    r.Roles,
	}

	accepted, err := buildSimpleStartPlan(req, deps)
	if err != nil {
		return err
	}
	if err := simpleStartRefuseLegacyMintedRestoreOnLaunchapi(req.LaunchapiPath, accepted); err != nil {
		return err
	}
	if err := resolveResumeLeadGate(accepted, r.SkipLeadCheck); err != nil {
		return err
	}

	if req.LaunchapiPath && len(accepted.SpawnTeam.Members) > 0 {
		probePrepared, _, err := (launchapiTeamLaunchBackend{}).prepare(accepted.SpawnTeam, accepted.LaunchOptions)
		if err != nil {
			return fmt.Errorf("preview plan: %w", err)
		}
		req.Options.ExpectedSubjectDigest = probePrepared.Result.SubjectDigest
	}

	_, err = runSimpleStartWithRequest(req, deps, nil, out)
	return err
}

// resolveResumeLeadGate is the local, never-forwarded-to-launchapi
// resolution of the two synthesized required actions from commit 1
// (resume_required_actions.go). The two apply under different
// preconditions, mirroring the two distinct mechanisms they replace:
//   - lead_not_live (replaces --skip-lead-check): only when dependents are
//     being spawned WITHOUT the lead itself co-spawning in this same call
//     (the lead is assumed already running elsewhere -- the mid-run
//     member-add case). When the lead IS part of this call's spawn set,
//     there is no pre-existing liveness to gate: runSimpleStartWithRequest's
//     shared launch brings the whole batch up together and its own
//     post-launch verification (every role must reach live/live-config-
//     diverged) already fails the call if the lead does not come up. This
//     mirrors team_resume.go's old lead-first special case in spirit,
//     without replicating its separate two-phase lead-then-dependents
//     launch sequencing, which is a launch-mechanics detail orthogonal to
//     this required-action gate.
//   - external_lead_record_dead (no bypass, same as the old
//     projectLeadExternalRecordBoundaryViolation check it replaces): checked
//     whenever the team has a configured lead at all, independent of
//     whether the lead is co-spawning -- an unauthorized external record is
//     a reason to refuse using it, not something a fresh co-launch excuses.
func resolveResumeLeadGate(plan simpleStartPlan, skipLeadCheck bool) error {
	lead := strings.TrimSpace(plan.Team.Lead)
	if lead == "" || !plan.Team.Orchestrated {
		return nil
	}
	hasDependentSpawn, leadIsSpawning := false, false
	for _, m := range plan.SpawnTeam.Members {
		if strings.EqualFold(m.Role, lead) {
			leadIsSpawning = true
		} else {
			hasDependentSpawn = true
		}
	}

	var actions []resumeRequiredAction
	if hasDependentSpawn && !leadIsSpawning {
		// AllRoles, not Roles: when --role excludes the lead, its row is
		// simply absent from the filtered Roles -- searching that would
		// misread "not part of this invocation" as "not live."
		var leadRow *simpleStartRolePlan
		for i := range plan.AllRoles {
			if strings.EqualFold(plan.AllRoles[i].Member.Role, lead) {
				leadRow = &plan.AllRoles[i]
				break
			}
		}
		leadLive := leadRow != nil && (leadRow.State == "live" || leadRow.State == "live/config-diverged")
		if !leadLive {
			actions = append(actions, newLeadNotLiveAction(lead))
		}
	}

	for _, m := range plan.Team.Members {
		if !strings.EqualFold(m.Role, lead) {
			continue
		}
		cwd := m.EffectiveCWD(plan.Project)
		baseRoot := filepath.Dir(plan.Root)
		handle := memberHandle(m)
		rec, found := findMemberRestoreRecord(baseRoot, plan.Project, cwd, plan.Profile, plan.Session, m.Role, handle)
		if found && projectLeadExternalRecordBoundaryViolation(plan.Team, m, rec, plan.Profile, plan.Session, plan.Root, handle) {
			actions = append(actions, newExternalLeadRecordDeadAction(lead))
		}
		break
	}

	if len(actions) == 0 {
		return nil
	}

	supplied := map[string]string{}
	if skipLeadCheck {
		supplied[resumeActionID(resumeActionKindLeadNotLive, lead)] = string(resumeDecisionProceedWithoutLead)
	}
	decided, missing, err := resolveResumeRequiredActions(actions, supplied)
	if err != nil {
		return err
	}
	if len(missing) > 0 {
		var reasons []string
		for _, a := range missing {
			reasons = append(reasons, fmt.Sprintf("%s (%s)", a.ActionID, a.ReasonCode))
		}
		return fmt.Errorf("resume refused: dependent roles were not launched, unresolved required action(s): %s (a stale lead record can be bypassed with --skip-lead-check where applicable)", strings.Join(reasons, ", "))
	}
	if choice, ok := decided[resumeActionID(resumeActionKindLeadNotLive, lead)]; ok && choice == resumeDecisionProceedWithoutLead {
		fmt.Fprintf(os.Stderr, "warning: --skip-lead-check: launching dependent roles without verifying lead %s is live and operator-addressable\n", lead)
	}
	return nil
}
