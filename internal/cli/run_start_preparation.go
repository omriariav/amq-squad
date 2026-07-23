package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/omriariav/amq-squad/v2/internal/catalog"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/role"
	"github.com/omriariav/amq-squad/v2/internal/rules"
	"github.com/omriariav/amq-squad/v2/internal/team"
	runwizard "github.com/omriariav/amq-squad/v2/internal/wizard"
)

var preparedManifestWriterAcquired = func(string, string, string) error { return nil }

type runPreparationProposal struct {
	Project       string
	Namespace     string
	ExecutionMode string
	Topology      preparedRunTopology
	LaunchShape   string
	Lead          string
	InitialRoster []string
	StagedRoster  []string
	Rows          []runReadinessRow
	PointerPlans  []rules.SyncPlan
	MutationPaths []string
}

type runPreparationProposalInput struct {
	Project, Profile, Session string
	LaunchShape, StagedRoles  string
	ToolProfile               string
	Goal, GoalSource          string
	GoalDigest, Seed          string
	Team                      team.Team
	ExistingProfile           bool
	Context                   acceptedRunContext
}

var runPreparationAfterProposal = func() {}

type runPreparationFileSnapshot struct {
	Path   string
	Exists bool
	Data   []byte
	Mode   os.FileMode
}

func snapshotRunPreparationFiles(paths ...string) ([]runPreparationFileSnapshot, error) {
	seen := make(map[string]bool, len(paths))
	snapshots := make([]runPreparationFileSnapshot, 0, len(paths))
	for _, path := range paths {
		path = filepath.Clean(strings.TrimSpace(path))
		if path == "." || seen[path] {
			continue
		}
		seen[path] = true
		info, err := os.Stat(path)
		if os.IsNotExist(err) {
			snapshots = append(snapshots, runPreparationFileSnapshot{Path: path})
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("snapshot preparation target %s: %w", path, err)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("snapshot preparation target %s: expected a regular file", path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("snapshot preparation target %s: %w", path, err)
		}
		snapshots = append(snapshots, runPreparationFileSnapshot{Path: path, Exists: true, Data: data, Mode: info.Mode().Perm()})
	}
	return snapshots, nil
}

func restoreRunPreparationFiles(snapshots []runPreparationFileSnapshot) error {
	var firstErr error
	for i := len(snapshots) - 1; i >= 0; i-- {
		snapshot := snapshots[i]
		var err error
		if !snapshot.Exists {
			err = os.Remove(snapshot.Path)
			if os.IsNotExist(err) {
				err = nil
			}
		} else {
			err = restoreRunPreparationFile(snapshot)
		}
		if err != nil && firstErr == nil {
			firstErr = fmt.Errorf("restore preparation target %s: %w", snapshot.Path, err)
		}
	}
	return firstErr
}

func restoreRunPreparationFile(snapshot runPreparationFileSnapshot) error {
	if err := os.MkdirAll(filepath.Dir(snapshot.Path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(snapshot.Path), ".amq-squad-prepare-rollback-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }
	if err := tmp.Chmod(snapshot.Mode); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if _, err := tmp.Write(snapshot.Data); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpPath, snapshot.Path); err != nil {
		cleanup()
		return err
	}
	return nil
}

func projectedRunPreparationTeam(project, session, lead, leadMode, rolesRaw, binaryRaw, modelRaw, effortRaw string) (team.Team, error) {
	roles := sortedUniqueRoles(rolesRaw)
	binaries := parseRoleAssignments(binaryRaw)
	models := parseRoleAssignments(modelRaw)
	efforts, err := parseEffortOverrides(effortRaw)
	if err != nil {
		return team.Team{}, err
	}
	tm := team.Team{
		Project: project, Lead: strings.TrimSpace(lead), LeadMode: leadModeForPersist(leadMode), Orchestrated: true,
		ExecutionMode: defaultExecutionModeForTeam(true), ControlRoot: project, TargetProjectRoot: project,
	}
	for _, roleID := range roles {
		binary := strings.TrimSpace(binaries[roleID])
		if binary == "" {
			if entry := catalog.Lookup(roleID); entry != nil {
				binary = entry.PreferredBinary
			}
		}
		member := team.Member{Role: roleID, Handle: roleID, Binary: binary, Session: session, Model: strings.TrimSpace(models[roleID])}
		if effort := strings.TrimSpace(efforts[roleID]); effort != "" {
			if err := applyMemberEffortCatalogMode(&member, effort, loadAgentCatalogAndWarn(project), false); err != nil {
				return team.Team{}, err
			}
		}
		tm.Members = append(tm.Members, member)
	}
	return tm, nil
}

// preparationOrAutomatic renders an empty recommended model/effort value as
// "automatic" for the readiness row, matching how the wizard displays it.
func preparationOrAutomatic(value string) string {
	if strings.TrimSpace(value) == "" {
		return "automatic"
	}
	return value
}

func buildRunPreparationProposal(in runPreparationProposalInput) (runPreparationProposal, error) {
	in.Project = strings.TrimSpace(in.Project)
	in.Profile = squadnamespace.NormalizeProfile(in.Profile)
	in.Session = strings.TrimSpace(in.Session)
	in.LaunchShape = strings.TrimSpace(in.LaunchShape)
	tm := in.Team
	tm.Project = in.Project
	tm.Lead = strings.TrimSpace(tm.Lead)
	stagedRoles := sortedUniqueRoles(in.StagedRoles)
	activeMembers, stagedMembers, err := partitionPreparedRunMembers(tm.Members, in.Session, stagedRoles)
	if err != nil {
		return runPreparationProposal{}, err
	}
	tm.Members = activeMembers
	proposal := runPreparationProposal{
		Project: in.Project, Namespace: in.Profile + "/" + in.Session, ExecutionMode: effectiveTeamExecutionMode(tm), Topology: in.Context.Topology,
		LaunchShape: in.LaunchShape, Lead: tm.Lead,
		InitialRoster: sortedUniqueRoles(strings.Join(teamMemberRoles(tm.Members), ",")), StagedRoster: stagedRoles,
	}
	add := func(artifact, status, evidence, fix string) {
		proposal.Rows = append(proposal.Rows, runReadinessRow{Artifact: artifact, Status: status, Evidence: evidence, Fix: fix})
	}
	if in.LaunchShape != runwizard.LaunchShapeWorkingTeamTogether && in.LaunchShape != runwizard.LaunchShapeLeadOnlyStaged {
		return proposal, fmt.Errorf("preparation proposal requires explicit launch shape")
	}
	resolvedTopology, topologyErr := resolveRunStartLayout(runStartLayoutInput{
		Visibility: in.Context.Topology.Visibility, VisibilitySet: true,
		Preset: in.Context.Topology.LayoutPreset, PresetSet: in.Context.Topology.LayoutPreset != "",
		LauncherPane: in.Context.Topology.LauncherPane, LauncherPaneSet: in.Context.Topology.LauncherPane != "",
		ExternalLead: in.Context.Topology.ExternalLead,
	})
	if topologyErr != nil || !reflect.DeepEqual(acceptedTopology(resolvedTopology, in.Context.Topology.ExternalLead), in.Context.Topology) {
		return proposal, fmt.Errorf("preparation topology blocker: accepted=%+v: %v", in.Context.Topology, topologyErr)
	}
	observation := observePreparedRunEnvironment(in.Project, in.Context.Version)
	for _, check := range []doctorCheck{observation.Skill, observation.AMQ, observation.Terminal} {
		if check.Status != doctorOK {
			detail := check.Detail
			if check.Name == "amq version" {
				if remedy := freshRunPreparationAMQRemedy(in.Project, tm, detail); remedy != "" {
					detail += "; " + remedy
				}
			}
			return proposal, fmt.Errorf("preparation environment blocker [%s/%s]: %s", check.Name, check.Status, detail)
		}
	}
	environmentEvidence := fmt.Sprintf("observed_binary=%s skill=%s amq=%s terminal=%s topology=%s/%s",
		observation.BinaryVersion, observation.Skill.Detail, observation.AMQ.Detail, observation.Terminal.Detail, in.Context.Topology.Visibility, in.Context.Topology.Target)
	if len(proposal.InitialRoster) == 0 || !containsRole(proposal.InitialRoster, tm.Lead) {
		if len(proposal.InitialRoster) > 0 {
			suggested := proposal.InitialRoster[0]
			return proposal, fmt.Errorf("preparation proposal lead %q must be in the non-empty initial roster; set a declared lead with `amq-squad team lead set %s --project %s --profile %s`, then rerun preparation for --session %s",
				tm.Lead, shellQuote(suggested), shellQuote(in.Project), shellQuote(in.Profile), shellQuote(in.Session))
		}
		return proposal, fmt.Errorf("preparation proposal requires a non-empty initial roster and declared lead")
	}
	if in.LaunchShape == runwizard.LaunchShapeLeadOnlyStaged && (len(proposal.InitialRoster) != 1 || proposal.InitialRoster[0] != tm.Lead) {
		return proposal, fmt.Errorf("lead-only-staged proposal requires exact initial roster [%s]", tm.Lead)
	}
	for _, staged := range proposal.StagedRoster {
		if containsRole(proposal.InitialRoster, staged) {
			return proposal, fmt.Errorf("staged role %q overlaps the initial roster", staged)
		}
	}
	binding, err := resolveAcceptedGoalBinding(in.Project, in.Profile, in.Session, in.Goal, in.GoalSource, in.GoalDigest)
	if err != nil {
		return proposal, err
	}
	add("goal_binding", "ready", fmt.Sprintf("planned/unverified source=%s namespace=%s digest=%s", binding.Source, binding.Namespace, binding.Digest), "")
	bootstrapBindings, err := validatePreparedBootstrapSemantics(tm, in.Profile, in.Session, binding)
	if err != nil {
		return proposal, err
	}

	policyPlans, err := buildRunStartToolProfilePlans(tm, in.Profile, in.ToolProfile)
	if err != nil {
		return proposal, err
	}
	if err := validateGeneratedToolPolicyPlans(policyPlans); err != nil {
		return proposal, err
	}
	policyFiles := make(map[string][]generatedPolicyFile, len(policyPlans))
	for _, plan := range policyPlans {
		tm.Members[plan.Index] = plan.After
		policyFiles[plan.After.Role] = append([]generatedPolicyFile(nil), plan.Files...)
		for _, file := range plan.Files {
			proposal.MutationPaths = append(proposal.MutationPaths, file.Path)
		}
	}
	agentCatalog := loadAgentCatalogAndWarn(in.Project)
	for _, member := range tm.Members {
		identity := acceptedMemberIdentity(tm, member, in.Profile, in.Session)
		add("member:"+member.Role, "ready", fmt.Sprintf("handle=%s binary=%s model=%s effort=%s task_ownership=%s tool_policy=%s config=%s mcp=%s",
			identity.Handle, identity.Binary, identity.Model, identity.Effort, identity.TaskOwnership, identity.ToolProfile, identity.ToolConfig, identity.ToolMCPConfig), "")
		// Advisory model/effort recommendation (#496): visibility only, never
		// authority -- it never gates readiness and never mutates the roster.
		rec := runwizard.RecommendModelEffort(identity.Binary, runwizard.DefaultWorkClassForRole(member.Role), runwizard.TaskProperties{}, agentCatalog)
		add("model_routing:"+member.Role, "advisory", fmt.Sprintf("recommended=%s/%s source=%s confidence=%s rationale=%s",
			preparationOrAutomatic(rec.Model), preparationOrAutomatic(rec.Effort), rec.PolicySource, rec.Confidence, rec.Rationale), "")
	}

	briefPath := briefPathForProfile(in.Project, in.Profile, in.Session)
	if data, readErr := os.ReadFile(briefPath); readErr == nil {
		switch {
		case strings.Contains(string(data), briefStubFirstLine):
			return proposal, fmt.Errorf("brief blocker [stub]: %s must be replaced by an explicitly approved real brief", briefPath)
		case strings.TrimSpace(string(data)) == "":
			return proposal, fmt.Errorf("brief blocker [generic]: %s is empty", briefPath)
		default:
			add("brief", "ready", "preserve existing "+briefPath, "")
		}
	} else if os.IsNotExist(readErr) {
		if strings.TrimSpace(in.Goal) == "" && strings.TrimSpace(in.Seed) == "" {
			return proposal, fmt.Errorf("brief blocker [missing]: accepted goal or --seed-from is required to create %s", briefPath)
		}
		add("brief", "ready", "planned create "+briefPath, "")
	} else {
		return proposal, readErr
	}

	rulesPath := rules.Path(in.Project)
	var rulesBody string
	if data, readErr := os.ReadFile(rulesPath); readErr == nil {
		lower := strings.ToLower(strings.TrimSpace(string(data)))
		if lower == "" || strings.Contains(lower, "todo:") || strings.Contains(lower, "stale rules") {
			return proposal, fmt.Errorf("team rules blocker [stale]: %s requires an approved replacement", rulesPath)
		}
		rulesBody = string(data)
		add("team_rules", "ready", "preserve existing "+rulesPath, "")
	} else if os.IsNotExist(readErr) {
		rendered, renderErr := renderTeamRules(tm)
		if renderErr != nil {
			return proposal, fmt.Errorf("team rules blocker: %w", renderErr)
		}
		rulesBody = rendered
		add("team_rules", "ready", "planned create "+rulesPath, "")
	} else {
		return proposal, readErr
	}
	pointerPlans, pointerErr := buildRunPreparationPointerPlans(in.Project, in.Profile, tm, rulesBody)
	if pointerErr != nil {
		return proposal, pointerErr
	}
	proposal.PointerPlans = pointerPlans
	pointerEvidence := make([]string, 0, len(pointerPlans))
	for _, plan := range pointerPlans {
		action := runPreparationPointerAction(plan)
		pointerEvidence = append(pointerEvidence, plan.Target+"="+action)
		add("pointer:"+plan.Basename, "ready", plan.Target+" action="+action, "")
	}
	add("environment_plan", "ready", environmentEvidence+fmt.Sprintf(" pointer_plan=%d [%s]", len(pointerPlans), strings.Join(pointerEvidence, ", ")), "")

	allRoles := append(append([]string(nil), proposal.InitialRoster...), proposal.StagedRoster...)
	for _, roleID := range allRoles {
		digest, source, roleErr := roleContractDigest(in.Project, roleID)
		if roleErr != nil {
			return proposal, fmt.Errorf("role blocker [missing] %s: %w", roleID, roleErr)
		}
		if !strings.HasPrefix(source, "catalog:") {
			data, readErr := os.ReadFile(source)
			if readErr != nil {
				return proposal, readErr
			}
			if customRoleContractStatus(data) != "ready" {
				return proposal, fmt.Errorf("role blocker [generic] %s: author task intent, authority, routing, and done criteria in %s", roleID, source)
			}
		}
		add("role:"+roleID, "ready", source+" sha256="+digest, "")
	}

	profilePath := team.ProfilePath(in.Project, in.Profile)
	proposal.MutationPaths = append(proposal.MutationPaths,
		profilePath,
		briefPath,
		rulesPath,
		preparedRunPath(in.Project, in.Profile, in.Session),
	)
	for _, plan := range pointerPlans {
		proposal.MutationPaths = append(proposal.MutationPaths, plan.Target)
	}
	if in.ExistingProfile {
		identities := make([]string, 0, len(tm.Members))
		for _, member := range tm.Members {
			identities = append(identities, fmt.Sprintf("%s(handle=%s binary=%s policy=%s)", member.Role, member.Handle, member.Binary, member.EffectiveToolProfile()))
		}
		evidence := "preserve exact session members at " + profilePath + ": " + strings.Join(identities, ", ")
		if len(stagedMembers) > 0 {
			skipped := make([]string, 0, len(stagedMembers))
			for _, member := range stagedMembers {
				skipped = append(skipped, fmt.Sprintf("%s(session=%s)", member.Role, member.Session))
			}
			evidence += "; explicitly staged other-session members: " + strings.Join(skipped, ", ")
		}
		add("profile", "ready", evidence, "")
	} else if _, readErr := os.Stat(profilePath); os.IsNotExist(readErr) {
		add("profile", "ready", "planned create "+profilePath, "")
	} else if readErr != nil {
		return proposal, readErr
	} else {
		return proposal, fmt.Errorf("profile blocker [drifted]: %s appeared while planning a fresh profile", profilePath)
	}

	root := squadnamespace.AMQRoot(in.Project, in.Profile, in.Session)
	for _, member := range tm.Members {
		roleID := member.Role
		handle := memberHandle(member)
		binary := strings.TrimSpace(member.Binary)
		if binary != "codex" && binary != "claude" {
			return proposal, fmt.Errorf("bootstrap blocker [missing] %s: explicit codex or claude binary is required", roleID)
		}
		policyEvidence := fmt.Sprintf("handle=%s binary=%s effective=%s config=%s mcp=%s", handle, binary, member.EffectiveToolProfile(), member.ToolConfig, member.ToolMCPConfig)
		for _, file := range policyFiles[roleID] {
			policyEvidence += fmt.Sprintf(" planned_%s=%s", file.Action, file.Path)
		}
		if len(policyFiles[roleID]) == 0 {
			policyEvidence += " preserve_effective_policy"
		}
		add("tool_policy:"+roleID, "ready", policyEvidence, "")
		agentDir := filepath.Join(root, "agents", handle)
		goalMode := strings.TrimPrefix(bootstrapBindings[roleID], "Goal binding: ")
		add("bootstrap:"+roleID, "ready", fmt.Sprintf("planned/unverified namespace=%s role=%s lead=%s root=%s brief=%s rules=%s role_path=%s goal_mode=%s goal_digest=%s routing=durable-amq gates=operator-contract",
			proposal.Namespace, roleID, tm.Lead, root, briefPath, rulesPath, role.ExistingPath(agentDir), goalMode, binding.Digest), "")
	}
	for _, roleID := range proposal.StagedRoster {
		add("staged_role:"+roleID, "ready", "configured and sealed for future spawn; excluded from initial roster/bootstrap execution; separate durable spawn gate required", "")
	}
	add("prepared_manifest", "ready", "planned create "+preparedRunPath(in.Project, in.Profile, in.Session), "")
	add("prepared_generation_state", "ready", "planned append under "+preparedRunGenerationsPath(in.Project, in.Profile, in.Session), "")
	proposal.MutationPaths = cleanUniquePreparationPaths(proposal.MutationPaths)
	sort.SliceStable(proposal.Rows, func(i, j int) bool { return proposal.Rows[i].Artifact < proposal.Rows[j].Artifact })
	return proposal, nil
}

func freshRunPreparationAMQRemedy(project string, tm team.Team, detail string) string {
	if !freshProjectDefaultAMQBootstrapAllowed(project, fmt.Errorf("%s", detail)) {
		return ""
	}
	handles := make([]string, 0, len(tm.Members)+1)
	for _, member := range tm.Members {
		if handle := strings.TrimSpace(member.Handle); handle != "" {
			handles = append(handles, handle)
		}
	}
	if operator := team.EffectiveOperator(tm); operator.Enabled && strings.TrimSpace(operator.Handle) != "" {
		handles = append(handles, strings.TrimSpace(operator.Handle))
	}
	handles = dedupeSortedStrings(handles)
	if len(handles) == 0 {
		handles = []string{"user"}
	}
	root := filepath.Join(project, defaultBaseRootName)
	return fmt.Sprintf("initialize the fresh project AMQ base without launching, then rerun the proposal: `amq init --root %s --agents %s`", shellQuote(root), shellQuote(strings.Join(handles, ",")))
}

func cleanUniquePreparationPaths(paths []string) []string {
	seen := make(map[string]bool, len(paths))
	result := make([]string, 0, len(paths))
	for _, path := range paths {
		path = filepath.Clean(strings.TrimSpace(path))
		if path == "." || seen[path] {
			continue
		}
		seen[path] = true
		result = append(result, path)
	}
	sort.Strings(result)
	return result
}

func preflightPreparedRunManifestAncestors(path string) error {
	parent := filepath.Dir(filepath.Clean(path))
	for {
		info, err := os.Stat(parent)
		switch {
		case err == nil:
			if !info.IsDir() {
				return fmt.Errorf("prepared manifest ancestor %s is not a directory", parent)
			}
			return nil
		case os.IsNotExist(err):
			next := filepath.Dir(parent)
			if next == parent {
				return nil
			}
			parent = next
		default:
			return fmt.Errorf("preflight prepared manifest ancestor %s: %w", parent, err)
		}
	}
}

func executeRunPreparationTransaction(project, profile, session string, paths []string, manifestPath string, mutate func() (runReadinessResult, error)) (result runReadinessResult, err error) {
	if err := preflightPreparedRunManifestAncestors(manifestPath); err != nil {
		return runReadinessResult{}, err
	}
	namespaceAdmission, err := acquireNamespaceWriterAdmission(project, profile, session)
	if err != nil {
		return runReadinessResult{}, err
	}
	defer namespaceAdmission.close()
	manifestAdmission, err := acquirePreparedManifestWriterAdmission(project, profile, session)
	if err != nil {
		return runReadinessResult{}, err
	}
	defer manifestAdmission.close()
	if err := preparedManifestWriterAcquired(project, profile, session); err != nil {
		return runReadinessResult{}, err
	}
	snapshots, err := snapshotRunPreparationFiles(paths...)
	if err != nil {
		return runReadinessResult{}, err
	}
	committed := false
	defer func() {
		if committed {
			return
		}
		if rollbackErr := restoreRunPreparationFiles(snapshots); rollbackErr != nil {
			if err == nil {
				err = rollbackErr
			} else {
				err = fmt.Errorf("%w; preparation transaction rollback failed: %v", err, rollbackErr)
			}
		}
	}()
	result, err = mutate()
	if err != nil {
		return result, err
	}
	committed = true
	return result, nil
}

func buildRunPreparationPointerPlans(project, profile string, tm team.Team, rulesBody string) ([]rules.SyncPlan, error) {
	targets, err := syncTargetDirs(project, tm.Members, false)
	if err != nil {
		return nil, fmt.Errorf("pointer plan blocker: %w", err)
	}
	if squadnamespace.NormalizeProfile(profile) == team.DefaultProfile {
		targets, err = ensureTeamHomeSyncTarget(targets, project)
		if err != nil {
			return nil, fmt.Errorf("pointer plan blocker: %w", err)
		}
	}
	var all []rules.SyncPlan
	for _, dir := range targets {
		plans, planErr := rules.Plan(dir, rulesBody)
		if planErr != nil {
			return nil, fmt.Errorf("pointer plan blocker for %s: %w", dir, planErr)
		}
		for _, plan := range plans {
			begins := strings.Count(plan.Before, rules.BeginMarker)
			ends := strings.Count(plan.Before, rules.EndMarker)
			if begins != ends || begins > 1 || (begins == 1 && strings.Index(plan.Before, rules.BeginMarker) > strings.Index(plan.Before, rules.EndMarker)) {
				return nil, fmt.Errorf("pointer plan blocker [malformed markers] %s: %d begin / %d end", plan.Target, begins, ends)
			}
			all = append(all, plan)
		}
	}
	return all, nil
}

func revalidateRunPreparationPointerPlans(plans []rules.SyncPlan) error {
	for _, plan := range plans {
		data, err := os.ReadFile(plan.Target)
		if plan.Creating {
			if err == nil {
				return fmt.Errorf("pointer target %s changed after the accepted proposal", plan.Target)
			}
			if !os.IsNotExist(err) {
				return err
			}
			continue
		}
		if err != nil {
			return fmt.Errorf("pointer target %s changed after the accepted proposal: %w", plan.Target, err)
		}
		if string(data) != plan.Before {
			return fmt.Errorf("pointer target %s changed after the accepted proposal", plan.Target)
		}
	}
	return nil
}

func runPreparationPointerAction(plan rules.SyncPlan) string {
	switch {
	case plan.Unchanged:
		return "unchanged"
	case plan.Creating:
		return "create"
	case plan.Adopting:
		return "adopt"
	default:
		return "update"
	}
}

func printRunPreparationProposal(proposal runPreparationProposal) {
	fmt.Printf("\nRead-only preparation proposal for %s\n", proposal.Namespace)
	fmt.Printf("Project: %s · execution mode: %s\n", proposal.Project, proposal.ExecutionMode)
	fmt.Printf("Visible lead: %s · topology: %s/%s · layout: %s · launcher: %s\n", proposal.Lead, proposal.Topology.Visibility, proposal.Topology.Target, proposal.Topology.LayoutPreset, proposal.Topology.LauncherPane)
	fmt.Printf("Initial launch: %d members - %s\n", len(proposal.InitialRoster), displayRoleList(proposal.InitialRoster))
	fmt.Printf("Staged for later: %d roles - %s\n", len(proposal.StagedRoster), displayRoleList(proposal.StagedRoster))
	fmt.Printf("Launch shape: %s · lead: %s\n", proposal.LaunchShape, proposal.Lead)
	for _, row := range proposal.Rows {
		fmt.Printf("  %-24s %-8s %s\n", row.Artifact, row.Status, row.Evidence)
	}
	fmt.Println("Proposal only. No profile, brief, rules, role, policy, manifest, mailbox, or pane was created.")
}
