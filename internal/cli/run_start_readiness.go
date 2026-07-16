package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/catalog"
	"github.com/omriariav/amq-squad/v2/internal/launch"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/role"
	"github.com/omriariav/amq-squad/v2/internal/rules"
	"github.com/omriariav/amq-squad/v2/internal/runtimecontrol"
	"github.com/omriariav/amq-squad/v2/internal/team"
	runwizard "github.com/omriariav/amq-squad/v2/internal/wizard"
)

const preparedRunSchema = 1

type preparedRunManifest struct {
	SchemaVersion    int                                  `json:"schema_version"`
	Project          string                               `json:"project"`
	Profile          string                               `json:"profile"`
	Session          string                               `json:"session"`
	Namespace        string                               `json:"namespace"`
	LaunchShape      string                               `json:"launch_shape"`
	InitialRoster    []string                             `json:"initial_roster"`
	StagedRoster     []string                             `json:"staged_roster"`
	Lead             string                               `json:"lead"`
	ExecutionMode    string                               `json:"execution_mode"`
	ControlRoot      string                               `json:"control_root"`
	TargetRoot       string                               `json:"target_root"`
	TargetContract   string                               `json:"target_contract,omitempty"`
	LeadMode         string                               `json:"lead_mode"`
	Topology         preparedRunTopology                  `json:"topology"`
	Members          map[string]preparedRunMemberIdentity `json:"members"`
	Environment      preparedRunEnvironment               `json:"environment"`
	GoalText         string                               `json:"goal_text"`
	GoalNamespace    string                               `json:"goal_namespace"`
	GoalDigest       string                               `json:"goal_digest"`
	GoalSource       string                               `json:"goal_source"`
	ArtifactDigests  map[string]string                    `json:"artifact_digests"`
	RoleDigests      map[string]string                    `json:"role_digests"`
	BootstrapDigests map[string]string                    `json:"bootstrap_digests"`
	PreparedAt       time.Time                            `json:"prepared_at"`
}

type preparedRunTopology struct {
	Visibility   string `json:"visibility"`
	LayoutPreset string `json:"layout_preset,omitempty"`
	LauncherPane string `json:"launcher_pane,omitempty"`
	Target       string `json:"target"`
	SpawnLayout  string `json:"spawn_layout"`
	ExternalLead bool   `json:"external_lead,omitempty"`
}

type preparedRunMemberIdentity struct {
	Role          string `json:"role"`
	Handle        string `json:"handle"`
	Binary        string `json:"binary"`
	Model         string `json:"model,omitempty"`
	Effort        string `json:"effort"`
	TaskOwnership string `json:"task_ownership"`
	ToolProfile   string `json:"tool_profile"`
	ToolConfig    string `json:"tool_config,omitempty"`
	ToolMCPConfig string `json:"tool_mcp_config,omitempty"`
}

type preparedRunEnvironment struct {
	BinaryVersion string   `json:"binary_version"`
	SkillVersion  string   `json:"skill_version"`
	AMQMinimum    string   `json:"amq_minimum"`
	Capabilities  []string `json:"capabilities"`
}

type acceptedRunContext struct {
	Version      string
	Topology     preparedRunTopology
	PointerPlans []rules.SyncPlan
}

var preparedRunRequiredCapabilities = []string{"amq-routing", "bootstrap-render", "goal-binding", "pointer-sync", "terminal-context", "tmux-topology", "tool-policy"}

type preparedRunEnvironmentObservation struct {
	BinaryVersion string
	Skill         doctorCheck
	AMQ           doctorCheck
	Terminal      doctorCheck
	HostContext   runtimecontrol.HostContext
	Capabilities  []string
}

var observePreparedRunEnvironment = func(project, version string) preparedRunEnvironmentObservation {
	version = strings.TrimSpace(version)
	if version == "test" {
		host := runtimecontrol.DetectHostContext([]string{"TERM_PROGRAM=iTerm.app", "TMUX=test"}, true)
		return preparedRunEnvironmentObservation{
			BinaryVersion: version,
			Skill:         doctorCheck{Name: "skill version", Status: doctorOK, Detail: "test harness supplied matching packaged skill"},
			AMQ:           doctorCheck{Name: "amq version", Status: doctorOK, Detail: "test harness supplied compatible AMQ " + doctorMinAMQVersion},
			Terminal:      doctorCheck{Name: "terminal context", Status: doctorOK, Detail: "test harness supplied tmux tier-A launch capability"},
			HostContext:   host,
			Capabilities:  []string{"amq-routing", "terminal-context", "tmux-topology"},
		}
	}
	d := defaultDoctorExecution(project)
	d.RunningVersion = version
	tmux := doctorCheckTmux(d)
	host := observeRunStartTerminalContext()
	observation := preparedRunEnvironmentObservation{
		BinaryVersion: version,
		Skill:         doctorCheckSkillVersion(d),
		AMQ:           doctorCheckAMQVersion(d),
		Terminal:      runStartTerminalDoctorCheck(host, tmux),
		HostContext:   host,
	}
	if observation.AMQ.Status == doctorOK {
		observation.Capabilities = append(observation.Capabilities, "amq-routing")
	}
	if host.SchemaVersion == runtimecontrol.HostContextSchemaVersion {
		observation.Capabilities = append(observation.Capabilities, "terminal-context")
	}
	if tmux.Status == doctorOK {
		observation.Capabilities = append(observation.Capabilities, "tmux-topology")
	}
	return observation
}

func acceptedTopology(layout runStartLayoutSelection, externalLead bool) preparedRunTopology {
	return preparedRunTopology{
		Visibility: layout.Visibility, LayoutPreset: layout.Preset, LauncherPane: layout.LauncherPane,
		Target: layout.Target, SpawnLayout: layout.SpawnLayout, ExternalLead: externalLead,
	}
}

func acceptedTaskOwnership(tm team.Team, member team.Member) string {
	if member.Role != tm.Lead {
		return "durable_task_assignee"
	}
	if team.EffectiveLeadMode(tm) == team.LeadModePlanner {
		return "plan_dispatch_review_gates; implementation=delegated_workers"
	}
	return "plan_dispatch_review_gates; implementation=lead_or_delegated_workers"
}

func acceptedMemberIdentity(tm team.Team, member team.Member) preparedRunMemberIdentity {
	return preparedRunMemberIdentity{
		Role: member.Role, Handle: member.Handle, Binary: normalizedAgentBinary(member.Binary),
		Model: member.Model, Effort: memberEffort(member), TaskOwnership: acceptedTaskOwnership(tm, member),
		ToolProfile: member.EffectiveToolProfile(), ToolConfig: member.ToolConfig, ToolMCPConfig: member.ToolMCPConfig,
	}
}

func acceptedExecutionRoots(tm team.Team) (string, string) {
	control := strings.TrimSpace(tm.ControlRoot)
	if control == "" {
		control = tm.Project
	}
	target := strings.TrimSpace(tm.TargetProjectRoot)
	if target == "" {
		target = tm.Project
	}
	return control, target
}

type acceptedGoalBinding struct {
	Text      string
	Source    string
	Namespace string
	Digest    string
}

type runReadinessRow struct {
	Artifact string `json:"artifact"`
	Status   string `json:"status"`
	Evidence string `json:"evidence"`
	Fix      string `json:"fix,omitempty"`
}

type runReadinessResult struct {
	Namespace     string              `json:"namespace"`
	Ready         bool                `json:"ready"`
	ExecutionMode string              `json:"execution_mode"`
	Topology      preparedRunTopology `json:"topology"`
	LaunchShape   string              `json:"launch_shape"`
	InitialRoster []string            `json:"initial_roster"`
	InitialCount  int                 `json:"initial_count"`
	StagedRoster  []string            `json:"staged_roster"`
	StagedCount   int                 `json:"staged_count"`
	Lead          string              `json:"lead"`
	Rows          []runReadinessRow   `json:"rows"`
}

func preparedRunPath(project, profile, session string) string {
	profile = squadnamespace.NormalizeProfile(profile)
	return filepath.Join(project, team.DirName, "prepared", profile, session+".json")
}

func writePreparedRunManifest(path string, manifest preparedRunManifest) error {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func readPreparedRunManifest(project, profile, session string) (preparedRunManifest, error) {
	path := preparedRunPath(project, profile, session)
	data, err := os.ReadFile(path)
	if err != nil {
		return preparedRunManifest{}, err
	}
	var manifest preparedRunManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return preparedRunManifest{}, fmt.Errorf("decode prepared run %s: %w", path, err)
	}
	if manifest.SchemaVersion != preparedRunSchema {
		return preparedRunManifest{}, fmt.Errorf("prepared run schema %d is unsupported", manifest.SchemaVersion)
	}
	return manifest, nil
}

func digestRunArtifactBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func digestFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return digestRunArtifactBytes(data), nil
}

func sortedUniqueRoles(raw string) []string {
	seen := map[string]bool{}
	var roles []string
	for _, part := range strings.Split(raw, ",") {
		role := strings.TrimSpace(part)
		if role != "" && !seen[role] {
			seen[role] = true
			roles = append(roles, role)
		}
	}
	return roles
}

func teamMemberRoles(members []team.Member) []string {
	roles := make([]string, 0, len(members))
	for _, member := range members {
		roles = append(roles, member.Role)
	}
	sort.Strings(roles)
	return roles
}

func sameRoleSet(left, right []string) bool {
	a := append([]string(nil), left...)
	b := append([]string(nil), right...)
	sort.Strings(a)
	sort.Strings(b)
	return strings.Join(a, "\x00") == strings.Join(b, "\x00")
}

func roleContractDigest(project, roleID string) (string, string, error) {
	if entry := catalog.Lookup(roleID); entry != nil {
		body := strings.Join([]string{entry.ID, entry.Label, entry.Description, strings.Join(entry.Skills, ","), entry.MinimumToolProfile}, "\n")
		return digestRunArtifactBytes([]byte(body)), "catalog:" + roleID, nil
	}
	path := team.CustomRolePath(project, roleID)
	data, err := os.ReadFile(path)
	if err != nil {
		return "", path, err
	}
	if strings.TrimSpace(string(data)) == "" {
		return "", path, fmt.Errorf("custom role contract is empty")
	}
	return digestRunArtifactBytes(data), path, nil
}

func customRoleContractStatus(data []byte) string {
	body := strings.ToLower(strings.TrimSpace(string(data)))
	if body == "" {
		return "missing"
	}
	for _, marker := range []string{
		"no catalog description is configured for this custom role",
		"todo: describe this role",
		"todo: list the role ids",
		"generic custom role",
	} {
		if strings.Contains(body, marker) {
			return "generic"
		}
	}
	return "ready"
}

func preparedGoalBinding(tm team.Team, profile, session string, member team.Member, binding acceptedGoalBinding) (*launch.GoalBinding, error) {
	if member.Role != tm.Lead {
		return nil, nil
	}
	if err := validateAcceptedGoalBinding(binding); err != nil {
		return nil, err
	}
	contract, err := goalDeliveryContractForBinary(member.Binary)
	if err != nil {
		return nil, err
	}
	attemptHash := strings.TrimPrefix(binding.Digest, "sha256:")
	if len(attemptHash) > 20 {
		attemptHash = attemptHash[:20]
	}
	attempt := "prepared-" + attemptHash
	prompt := contract.prompt(binding.Text, tm, profile, session, member.Role, attempt)
	return contract.binding(binding.Text, attempt, prompt, "prepared-run", "accepted preparation goal binding source="+binding.Source+" digest="+binding.Digest), nil
}

func preparedBootstrap(project, profile, session string, binding acceptedGoalBinding, tm team.Team, member team.Member) (string, error) {
	root := squadnamespace.AMQRoot(project, profile, session)
	agentDir := filepath.Join(root, "agents", member.Handle)
	rec := launch.Record{
		Role: member.Role, Handle: member.Handle, Binary: member.Binary,
		ToolProfile: member.EffectiveToolProfile(), ToolConfig: member.ToolConfig,
		Session: session, CWD: member.EffectiveCWD(tm.Project), Root: root,
		TeamHome: project, TeamProfile: profile, SharedWorkstream: true,
	}
	if member.Role == tm.Lead {
		goalBinding, err := preparedGoalBinding(tm, profile, session, member, binding)
		if err != nil {
			return "", err
		}
		rec.GoalBinding = goalBinding
	}
	prompt, err := buildBootstrapPrompt(bootstrapContextFor(rec, agentDir, project))
	if err != nil {
		return "", err
	}
	required := []string{
		root,
		session,
		project,
		briefPathForProfile(project, profile, session),
		rules.Path(project),
		role.ExistingPath(agentDir),
		member.Role,
		tm.Lead,
		"Current team routing:",
		"Operator gate routing:",
		"amq drain --include-body",
	}
	if member.Role == tm.Lead {
		contract, err := goalDeliveryContractForBinary(member.Binary)
		if err != nil {
			return "", err
		}
		required = append(required, "Goal binding: "+contract.Mode)
	} else {
		required = append(required, "Goal binding: amq_task_brief")
	}
	for _, required := range required {
		if !strings.Contains(prompt, required) {
			return "", fmt.Errorf("generated bootstrap for %s omits %q", member.Role, required)
		}
	}
	return prompt, nil
}

func buildPreparedRunManifest(project, profile, session, shape, stagedRaw string, binding acceptedGoalBinding, context acceptedRunContext) (preparedRunManifest, error) {
	profile = squadnamespace.NormalizeProfile(profile)
	tm, err := team.ReadProfile(project, profile)
	if err != nil {
		return preparedRunManifest{}, err
	}
	initial := teamMemberRoles(tm.Members)
	staged := sortedUniqueRoles(stagedRaw)
	controlRoot, targetRoot := acceptedExecutionRoots(tm)
	manifest := preparedRunManifest{
		SchemaVersion: preparedRunSchema, Project: project, Profile: profile, Session: session,
		Namespace: profile + "/" + session, LaunchShape: shape, InitialRoster: initial,
		StagedRoster: staged, Lead: tm.Lead, GoalText: binding.Text, GoalNamespace: binding.Namespace,
		GoalDigest: binding.Digest, GoalSource: binding.Source,
		ExecutionMode: effectiveTeamExecutionMode(tm), ControlRoot: controlRoot, TargetRoot: targetRoot,
		TargetContract: tm.TargetContract, LeadMode: team.EffectiveLeadMode(tm), Topology: context.Topology,
		Members:         map[string]preparedRunMemberIdentity{},
		Environment:     preparedRunEnvironment{BinaryVersion: strings.TrimSpace(context.Version), SkillVersion: strings.TrimSpace(context.Version), AMQMinimum: doctorMinAMQVersion, Capabilities: append([]string(nil), preparedRunRequiredCapabilities...)},
		ArtifactDigests: map[string]string{}, RoleDigests: map[string]string{}, BootstrapDigests: map[string]string{}, PreparedAt: time.Now().UTC(),
	}
	for label, path := range map[string]string{
		"brief":      briefPathForProfile(project, profile, session),
		"team_rules": rules.Path(project),
		"profile":    team.ProfilePath(project, profile),
	} {
		digest, err := digestFile(path)
		if err != nil {
			return preparedRunManifest{}, fmt.Errorf("snapshot %s: %w", label, err)
		}
		manifest.ArtifactDigests[label] = digest
	}
	for _, roleID := range append(append([]string(nil), initial...), staged...) {
		digest, _, err := roleContractDigest(project, roleID)
		if err != nil {
			return preparedRunManifest{}, fmt.Errorf("role %s is not ready: %w", roleID, err)
		}
		manifest.RoleDigests[roleID] = digest
	}
	for _, member := range tm.Members {
		manifest.Members[member.Role] = acceptedMemberIdentity(tm, member)
		prompt, err := preparedBootstrap(project, profile, session, binding, tm, member)
		if err != nil {
			return preparedRunManifest{}, err
		}
		manifest.BootstrapDigests[member.Role] = digestRunArtifactBytes([]byte(prompt))
	}
	return manifest, nil
}

func validatePreparedLaunchShape(manifest preparedRunManifest) error {
	if manifest.LaunchShape != runwizard.LaunchShapeWorkingTeamTogether && manifest.LaunchShape != runwizard.LaunchShapeLeadOnlyStaged {
		return fmt.Errorf("unsupported accepted launch shape %q", manifest.LaunchShape)
	}
	if strings.TrimSpace(manifest.Lead) == "" || !containsRole(manifest.InitialRoster, manifest.Lead) {
		return fmt.Errorf("accepted lead %q is not in the initial roster", manifest.Lead)
	}
	if manifest.LaunchShape == runwizard.LaunchShapeLeadOnlyStaged && (len(manifest.InitialRoster) != 1 || manifest.InitialRoster[0] != manifest.Lead) {
		return fmt.Errorf("lead-only-staged requires the exact initial roster [%s]", manifest.Lead)
	}
	for _, role := range manifest.StagedRoster {
		if containsRole(manifest.InitialRoster, role) {
			return fmt.Errorf("staged role %q overlaps the accepted initial roster", role)
		}
	}
	return nil
}

func containsRole(roles []string, want string) bool {
	for _, role := range roles {
		if role == want {
			return true
		}
	}
	return false
}

func validateAcceptedGoalBinding(binding acceptedGoalBinding) error {
	if strings.TrimSpace(binding.Namespace) == "" || strings.TrimSpace(binding.Text) == "" {
		return fmt.Errorf("accepted goal namespace and text are required")
	}
	if binding.Source != runwizard.GoalBindingSourceExplicit && binding.Source != runwizard.GoalBindingSourceAcceptedBrief {
		return fmt.Errorf("unsupported accepted goal source %q", binding.Source)
	}
	want := runwizard.GoalBindingDigest(binding.Namespace, binding.Source, binding.Text)
	if binding.Digest != want {
		return fmt.Errorf("accepted goal digest mismatch: recorded=%q computed=%q", binding.Digest, want)
	}
	return nil
}

func resolveAcceptedGoalBinding(project, profile, session, goal, source, claimedDigest string) (acceptedGoalBinding, error) {
	profile = squadnamespace.NormalizeProfile(profile)
	namespace := profile + "/" + session
	goal = strings.TrimSpace(goal)
	source = strings.TrimSpace(source)
	if source == "" {
		if goal == "" {
			source = runwizard.GoalBindingSourceAcceptedBrief
		} else {
			source = runwizard.GoalBindingSourceExplicit
		}
	}
	if source == runwizard.GoalBindingSourceAcceptedBrief {
		briefPath := briefPathForProfile(project, profile, session)
		data, err := os.ReadFile(briefPath)
		if err != nil {
			return acceptedGoalBinding{}, fmt.Errorf("accepted brief goal binding: %w", err)
		}
		if strings.TrimSpace(string(data)) == "" || strings.Contains(string(data), briefStubFirstLine) {
			return acceptedGoalBinding{}, fmt.Errorf("accepted brief goal binding requires a real non-stub brief at %s", briefPath)
		}
		if goal == "" {
			goal = fmt.Sprintf("Execute the accepted brief for namespace %s at %s.", namespace, briefPath)
		}
	}
	binding := acceptedGoalBinding{Text: goal, Source: source, Namespace: namespace}
	binding.Digest = runwizard.GoalBindingDigest(namespace, source, goal)
	if strings.TrimSpace(claimedDigest) != "" && strings.TrimSpace(claimedDigest) != binding.Digest {
		return acceptedGoalBinding{}, fmt.Errorf("accepted goal digest mismatch: claimed=%q computed=%q", strings.TrimSpace(claimedDigest), binding.Digest)
	}
	if err := validateAcceptedGoalBinding(binding); err != nil {
		return acceptedGoalBinding{}, err
	}
	return binding, nil
}

// preparedRunLiveGoalBinding hydrates the only goal accepted for a live run
// from the prepared manifest. Live flags are optional transport proof: when a
// caller supplies one, it must equal the accepted value byte-for-byte. This
// check runs before any external-lead registration or managed process spawn.
func preparedRunLiveGoalBinding(project, profile, session, goal, source, digest string) (acceptedGoalBinding, error) {
	profile = squadnamespace.NormalizeProfile(profile)
	manifest, err := readPreparedRunManifest(project, profile, session)
	if err != nil {
		return acceptedGoalBinding{}, err
	}
	binding := acceptedGoalBinding{
		Text: manifest.GoalText, Source: manifest.GoalSource,
		Namespace: manifest.GoalNamespace, Digest: manifest.GoalDigest,
	}
	if err := validateAcceptedGoalBinding(binding); err != nil {
		return acceptedGoalBinding{}, err
	}
	wantNamespace := profile + "/" + session
	if binding.Namespace != wantNamespace {
		return acceptedGoalBinding{}, fmt.Errorf("prepared goal namespace %q does not match live namespace %q", binding.Namespace, wantNamespace)
	}
	for _, check := range []struct {
		name     string
		provided string
		accepted string
	}{
		{name: "text", provided: goal, accepted: binding.Text},
		{name: "source", provided: source, accepted: binding.Source},
		{name: "digest", provided: digest, accepted: binding.Digest},
	} {
		if check.provided != "" && check.provided != check.accepted {
			return acceptedGoalBinding{}, fmt.Errorf("live goal %s mismatch: supplied=%q accepted=%q", check.name, check.provided, check.accepted)
		}
	}
	return binding, nil
}

func calculateRunReadiness(project, profile, session string) runReadinessResult {
	return calculateRunReadinessWithContext(project, profile, session, acceptedRunContext{})
}

func calculateRunReadinessWithContext(project, profile, session string, context acceptedRunContext) runReadinessResult {
	profile = squadnamespace.NormalizeProfile(profile)
	namespace := profile + "/" + session
	result := runReadinessResult{Namespace: namespace}
	manifest, err := readPreparedRunManifest(project, profile, session)
	if err != nil {
		result.Rows = append(result.Rows, runReadinessRow{Artifact: "preparation", Status: "missing", Evidence: err.Error(), Fix: "return to wizard preparation and approve the rendered artifact mutations"})
		return result
	}
	result.LaunchShape, result.InitialRoster, result.StagedRoster, result.Lead = manifest.LaunchShape, manifest.InitialRoster, manifest.StagedRoster, manifest.Lead
	result.ExecutionMode, result.Topology = manifest.ExecutionMode, manifest.Topology
	result.InitialCount, result.StagedCount = len(result.InitialRoster), len(result.StagedRoster)
	add := func(artifact, status, evidence, fix string) {
		result.Rows = append(result.Rows, runReadinessRow{Artifact: artifact, Status: status, Evidence: evidence, Fix: fix})
	}
	if manifest.Project != project || manifest.Profile != profile || manifest.Session != session || manifest.Namespace != namespace {
		add("preparation", "stale", fmt.Sprintf("accepted namespace=%s current=%s", manifest.Namespace, namespace), "return to preparation for the exact project/profile/session")
	} else {
		add("preparation", "ready", preparedRunPath(project, profile, session), "")
	}
	if err := validatePreparedLaunchShape(manifest); err != nil {
		add("launch_shape", "drifted", err.Error(), "return to preparation and explicitly accept a supported launch shape")
	} else {
		add("launch_shape", "ready", fmt.Sprintf("%s initial=%d staged=%d", manifest.LaunchShape, len(manifest.InitialRoster), len(manifest.StagedRoster)), "")
	}
	binding := acceptedGoalBinding{Text: manifest.GoalText, Source: manifest.GoalSource, Namespace: manifest.GoalNamespace, Digest: manifest.GoalDigest}
	if manifest.GoalNamespace != namespace {
		add("goal_binding", "drifted", fmt.Sprintf("accepted namespace=%q current=%q", manifest.GoalNamespace, namespace), "return to preparation for the exact namespace")
	} else if err := validateAcceptedGoalBinding(binding); err != nil {
		add("goal_binding", "drifted", err.Error(), "return to preparation and accept one canonical goal binding")
	} else {
		add("goal_binding", "ready", fmt.Sprintf("verified source=%s namespace=%s digest=%s", binding.Source, binding.Namespace, binding.Digest), "")
	}
	tm, teamErr := team.ReadProfile(project, profile)
	if teamErr != nil {
		add("profile", "missing", teamErr.Error(), "approve preparation to create the exact initial profile")
	} else {
		actual := teamMemberRoles(tm.Members)
		controlRoot, targetRoot := acceptedExecutionRoots(tm)
		if effectiveTeamExecutionMode(tm) != manifest.ExecutionMode || controlRoot != manifest.ControlRoot || targetRoot != manifest.TargetRoot || tm.TargetContract != manifest.TargetContract || team.EffectiveLeadMode(tm) != manifest.LeadMode {
			add("execution", "drifted", fmt.Sprintf("accepted mode=%s control=%s target=%s contract=%s lead_mode=%s; current mode=%s control=%s target=%s contract=%s lead_mode=%s",
				manifest.ExecutionMode, manifest.ControlRoot, manifest.TargetRoot, manifest.TargetContract, manifest.LeadMode,
				effectiveTeamExecutionMode(tm), controlRoot, targetRoot, tm.TargetContract, team.EffectiveLeadMode(tm)), "return to preparation and accept the exact execution/ownership contract")
		} else {
			add("execution", "ready", fmt.Sprintf("mode=%s control=%s target=%s contract=%s lead_mode=%s", manifest.ExecutionMode, manifest.ControlRoot, manifest.TargetRoot, manifest.TargetContract, manifest.LeadMode), "")
		}
		if !sameRoleSet(actual, manifest.InitialRoster) {
			add("profile", "drifted", fmt.Sprintf("initial roster mismatch: accepted %d [%s], profile %d [%s]", len(manifest.InitialRoster), strings.Join(manifest.InitialRoster, ", "), len(actual), strings.Join(actual, ", ")), "return to preparation; do not silently add or remove members")
		} else if digest, err := digestFile(team.ProfilePath(project, profile)); err != nil || digest != manifest.ArtifactDigests["profile"] {
			add("profile", "drifted", "profile content differs from the accepted preparation snapshot", "review the profile diff and approve preparation again")
		} else {
			add("profile", "ready", fmt.Sprintf("%d members - %s", len(actual), strings.Join(actual, ", ")), "")
		}
		for _, member := range tm.Members {
			accepted, ok := manifest.Members[member.Role]
			current := acceptedMemberIdentity(tm, member)
			if !ok || !reflect.DeepEqual(accepted, current) {
				add("member:"+member.Role, "drifted", fmt.Sprintf("accepted=%+v current=%+v", accepted, current), "return to preparation and accept the exact binary/model/effort/task/tool identity")
			} else {
				add("member:"+member.Role, "ready", fmt.Sprintf("handle=%s binary=%s model=%s effort=%s task=%s tool_policy=%s", current.Handle, current.Binary, current.Model, current.Effort, current.TaskOwnership, current.ToolProfile), "")
			}
		}
	}
	briefPath := briefPathForProfile(project, profile, session)
	briefData, briefErr := os.ReadFile(briefPath)
	switch {
	case briefErr != nil:
		add("brief", "missing", briefPath, "prepare a real accepted brief")
	case strings.Contains(string(briefData), briefStubFirstLine):
		add("brief", "stub", briefPath, "replace the stub during approved preparation")
	case digestRunArtifactBytes(briefData) != manifest.ArtifactDigests["brief"]:
		add("brief", "drifted", briefPath+" differs from accepted digest", "review the brief diff and approve preparation again")
	default:
		add("brief", "ready", briefPath+" sha256="+digestRunArtifactBytes(briefData), "")
	}
	rulesPath := rules.Path(project)
	if digest, err := digestFile(rulesPath); err != nil {
		add("team_rules", "missing", err.Error(), "approve preparation to generate current rules")
	} else if digest != manifest.ArtifactDigests["team_rules"] {
		add("team_rules", "stale", rulesPath+" differs from accepted digest", "review the rules diff and approve preparation again")
	} else {
		add("team_rules", "ready", rulesPath+" sha256="+digest, "")
	}
	actualInitial := map[string]bool{}
	if teamErr == nil {
		for _, member := range tm.Members {
			actualInitial[member.Role] = true
		}
	}
	for _, roleID := range append(append([]string(nil), manifest.InitialRoster...), manifest.StagedRoster...) {
		digest, source, err := roleContractDigest(project, roleID)
		artifact := "role:" + roleID
		if err != nil {
			add(artifact, "missing", source+": "+err.Error(), "author the custom role contract during preparation")
		} else if strings.HasPrefix(source, project+string(filepath.Separator)) {
			data, readErr := os.ReadFile(source)
			if readErr != nil {
				add(artifact, "missing", readErr.Error(), "author the custom role contract during preparation")
			} else if status := customRoleContractStatus(data); status == "generic" {
				add(artifact, "generic", source+" is a fallback/template contract", "author task intent, mutation authority, routing, and done criteria; then approve preparation again")
			} else if digest != manifest.RoleDigests[roleID] {
				add(artifact, "drifted", source+" differs from accepted digest", "review the role diff and approve preparation again")
			} else {
				add(artifact, "ready", source+" sha256="+digest, "")
			}
		} else if digest != manifest.RoleDigests[roleID] {
			add(artifact, "drifted", source+" differs from accepted digest", "review the role diff and approve preparation again")
		} else {
			add(artifact, "ready", source+" sha256="+digest, "")
		}
	}
	for _, roleID := range manifest.StagedRoster {
		if actualInitial[roleID] {
			add("staged_role:"+roleID, "drifted", "staged-only role is present in the initial profile", "return to preparation and choose the intended roster explicitly")
		} else {
			add("staged_role:"+roleID, "ready", "absent from initial profile; separate durable spawn gate required", "")
		}
	}
	if teamErr == nil {
		membersByRole := make(map[string]team.Member, len(tm.Members))
		for _, member := range tm.Members {
			membersByRole[member.Role] = member
		}
		for _, roleID := range manifest.InitialRoster {
			member, exists := membersByRole[roleID]
			artifact := "bootstrap:" + roleID
			if !exists {
				add(artifact, "missing", "accepted initial member is absent from the profile and bootstrap plan", "return to preparation and restore the exact accepted initial roster")
				continue
			}
			prompt, err := preparedBootstrap(project, profile, session, binding, tm, member)
			if err != nil {
				add(artifact, "drifted", err.Error(), "repair the referenced artifact and approve preparation again")
			} else if digestRunArtifactBytes([]byte(prompt)) != manifest.BootstrapDigests[member.Role] {
				add(artifact, "drifted", "generated bootstrap differs from accepted preview", "review the bootstrap diff and approve preparation again")
			} else {
				goalMode := "amq_task_brief"
				if member.Role == tm.Lead {
					if contract, contractErr := goalDeliveryContractForBinary(member.Binary); contractErr == nil {
						goalMode = contract.Mode
					}
				}
				add(artifact, "ready", fmt.Sprintf("namespace=%s/%s role=%s lead=%s brief=%s rules=%s role_path=%s goal_mode=%s goal_digest=%s routing=durable-amq gates=operator-contract sha256=%s",
					profile, session, member.Role, tm.Lead,
					briefPathForProfile(project, profile, session), rules.Path(project),
					role.ExistingPath(filepath.Join(squadnamespace.AMQRoot(project, profile, session), "agents", member.Handle)),
					goalMode, manifest.GoalDigest, digestRunArtifactBytes([]byte(prompt))), "")
			}
		}
		if evidence, err := validatePreparedRunEnvironment(project, profile, manifest, tm, context); err != nil {
			add("environment", "drifted", err.Error(), "repair the reported version, capability, topology, pointer, or policy drift and approve preparation again")
		} else {
			add("environment", "ready", evidence, "")
		}
	}
	result.Ready = len(result.Rows) > 0
	for _, row := range result.Rows {
		if row.Status != "ready" {
			result.Ready = false
			break
		}
	}
	return result
}

func validatePreparedRunEnvironment(project, profile string, manifest preparedRunManifest, tm team.Team, context acceptedRunContext) (string, error) {
	if strings.TrimSpace(manifest.Environment.BinaryVersion) == "" || manifest.Environment.SkillVersion != manifest.Environment.BinaryVersion {
		return "", fmt.Errorf("accepted binary/skill identity is invalid: binary=%q skill=%q", manifest.Environment.BinaryVersion, manifest.Environment.SkillVersion)
	}
	observedVersion := strings.TrimSpace(context.Version)
	if observedVersion == "" {
		observedVersion = manifest.Environment.BinaryVersion
	}
	observation := observePreparedRunEnvironment(project, observedVersion)
	if observation.BinaryVersion != manifest.Environment.BinaryVersion {
		return "", fmt.Errorf("observed binary version drift: accepted=%q observed=%q", manifest.Environment.BinaryVersion, observation.BinaryVersion)
	}
	if observation.Skill.Status != doctorOK {
		return "", fmt.Errorf("observed binary/skill compatibility failed [%s]: %s", observation.Skill.Status, observation.Skill.Detail)
	}
	if observation.AMQ.Status != doctorOK {
		return "", fmt.Errorf("observed AMQ compatibility failed [%s]: %s", observation.AMQ.Status, observation.AMQ.Detail)
	}
	if observation.Terminal.Status != doctorOK {
		return "", fmt.Errorf("observed terminal capability failed [%s]: %s", observation.Terminal.Status, observation.Terminal.Detail)
	}
	if observation.HostContext.SchemaVersion != runtimecontrol.HostContextSchemaVersion {
		return "", fmt.Errorf("observed terminal context schema drift: observed=%d required=%d", observation.HostContext.SchemaVersion, runtimecontrol.HostContextSchemaVersion)
	}
	if manifest.Environment.AMQMinimum != doctorMinAMQVersion {
		return "", fmt.Errorf("accepted AMQ capability floor drift: accepted=%q required=%q", manifest.Environment.AMQMinimum, doctorMinAMQVersion)
	}
	resolved, err := resolveRunStartLayout(runStartLayoutInput{
		Visibility: manifest.Topology.Visibility, VisibilitySet: true,
		Preset: manifest.Topology.LayoutPreset, PresetSet: manifest.Topology.LayoutPreset != "",
		LauncherPane: manifest.Topology.LauncherPane, LauncherPaneSet: manifest.Topology.LauncherPane != "",
		ExternalLead: manifest.Topology.ExternalLead,
	})
	if err != nil {
		return "", fmt.Errorf("terminal/topology contract failed: %w", err)
	}
	wantTopology := acceptedTopology(resolved, manifest.Topology.ExternalLead)
	if !reflect.DeepEqual(wantTopology, manifest.Topology) {
		return "", fmt.Errorf("terminal/topology drift: accepted=%+v resolved=%+v", manifest.Topology, wantTopology)
	}
	if strings.TrimSpace(context.Topology.Visibility) != "" && !reflect.DeepEqual(context.Topology, manifest.Topology) {
		return "", fmt.Errorf("requested topology differs from accepted preparation: accepted=%+v current=%+v", manifest.Topology, context.Topology)
	}
	for _, member := range tm.Members {
		if binary := normalizedAgentBinary(member.Binary); binary != "codex" && binary != "claude" {
			return "", fmt.Errorf("binary/skill compatibility failed for %s: unsupported binary %q", member.Role, member.Binary)
		}
	}
	if err := validateMemberOverlayPaths(tm, tm.Members); err != nil {
		return "", fmt.Errorf("tool-policy launch capability failed: %w", err)
	}
	observedCapabilities := append([]string(nil), observation.Capabilities...)
	observedCapabilities = append(observedCapabilities, "bootstrap-render", "goal-binding", "tool-policy")
	pointerChecks := doctorCheckPointerSync(doctorExecution{ProjectDir: project, Profile: profile})
	for _, check := range pointerChecks {
		if check.Status != doctorOK {
			return "", fmt.Errorf("pointer sync failed [%s]: %s", check.Status, check.Detail)
		}
	}
	observedCapabilities = append(observedCapabilities, "pointer-sync")
	observedCapabilities = dedupeSortedStrings(observedCapabilities)
	if !sameRoleSet(manifest.Environment.Capabilities, observedCapabilities) || !sameRoleSet(observedCapabilities, preparedRunRequiredCapabilities) {
		return "", fmt.Errorf("observed launch capability mismatch: accepted=[%s] observed=[%s] required=[%s]", strings.Join(manifest.Environment.Capabilities, ","), strings.Join(observedCapabilities, ","), strings.Join(preparedRunRequiredCapabilities, ","))
	}
	return fmt.Sprintf("observed_binary=%s skill=%s amq=%s terminal=%s topology=%s/%s pointer_sync=%d capabilities=%s",
		observation.BinaryVersion, observation.Skill.Detail, observation.AMQ.Detail, observation.Terminal.Detail,
		manifest.Topology.Visibility, manifest.Topology.Target, len(pointerChecks), strings.Join(observedCapabilities, ",")), nil
}

func printRunReadiness(result runReadinessResult) {
	fmt.Printf("\nArtifact readiness for %s\n", result.Namespace)
	fmt.Printf("Initial launch: %d members - %s\n", result.InitialCount, displayRoleList(result.InitialRoster))
	fmt.Printf("Staged for later: %d roles - %s\n", result.StagedCount, displayRoleList(result.StagedRoster))
	fmt.Printf("Launch shape: %s · lead: %s\n", result.LaunchShape, result.Lead)
	fmt.Printf("Execution mode: %s · topology: %s/%s\n", result.ExecutionMode, result.Topology.Visibility, result.Topology.Target)
	for _, row := range result.Rows {
		fmt.Printf("  %-24s %-8s %s\n", row.Artifact, row.Status, row.Evidence)
		if row.Fix != "" {
			fmt.Printf("  %-24s fix      %s\n", "", row.Fix)
		}
	}
}

func displayRoleList(roles []string) string {
	if len(roles) == 0 {
		return "none"
	}
	return strings.Join(roles, ", ")
}

func prepareRunArtifacts(project, profile, session, shape, stagedRaw, goal, goalSource, goalDigest, seed string, context acceptedRunContext) (runReadinessResult, error) {
	profile = squadnamespace.NormalizeProfile(profile)
	if err := revalidateRunPreparationPointerPlans(context.PointerPlans); err != nil {
		return runReadinessResult{}, fmt.Errorf("revalidate accepted pointer plan before preparation writes: %w", err)
	}
	if shape != runwizard.LaunchShapeWorkingTeamTogether && shape != runwizard.LaunchShapeLeadOnlyStaged {
		return runReadinessResult{}, fmt.Errorf("preparation requires explicit --launch-shape working-team-together or lead-only-staged")
	}
	briefPath := briefPathForProfile(project, profile, session)
	if _, err := os.Stat(briefPath); os.IsNotExist(err) {
		var content, source string
		if strings.TrimSpace(seed) != "" {
			body, err := resolveSeed(seed)
			if err != nil {
				return runReadinessResult{}, err
			}
			content, source = buildSeedBrief(seed, body, seedNow()), seed
		} else if strings.TrimSpace(goal) != "" {
			source = "operator_goal"
			body := fmt.Sprintf("# %s brief\n\n## Goal\n\n%s\n\n## Source\n\noperator prompt\n\n## Scope\n\nAccepted wizard preparation.\n\n## Out of scope\n\nUnspecified work.\n\n## Acceptance\n\nThe accepted goal and readiness contract are satisfied.\n", session, strings.TrimSpace(goal))
			content = buildSeedBrief(source, body, seedNow())
		} else {
			return runReadinessResult{}, fmt.Errorf("preparation requires an accepted goal or real --seed-from brief")
		}
		if _, err := writeSeedBriefForProfile(project, profile, session, content, false); err != nil {
			return runReadinessResult{}, err
		}
	} else if err != nil {
		return runReadinessResult{}, err
	}
	if _, err := os.Stat(rules.Path(project)); os.IsNotExist(err) {
		tm, readErr := team.ReadProfile(project, profile)
		if readErr != nil {
			return runReadinessResult{}, readErr
		}
		body, renderErr := renderTeamRules(tm)
		if renderErr != nil {
			return runReadinessResult{}, renderErr
		}
		if writeErr := rules.Write(project, body); writeErr != nil {
			return runReadinessResult{}, writeErr
		}
	} else if err != nil {
		return runReadinessResult{}, err
	}
	if _, err := rules.Apply(context.PointerPlans); err != nil {
		return runReadinessResult{}, fmt.Errorf("apply accepted pointer plan: %w", err)
	}
	binding, err := resolveAcceptedGoalBinding(project, profile, session, goal, goalSource, goalDigest)
	if err != nil {
		return runReadinessResult{}, err
	}
	manifest, err := buildPreparedRunManifest(project, profile, session, shape, stagedRaw, binding, context)
	if err != nil {
		return runReadinessResult{}, err
	}
	if err := writePreparedRunManifest(preparedRunPath(project, profile, session), manifest); err != nil {
		return runReadinessResult{}, err
	}
	result := calculateRunReadinessWithContext(project, profile, session, context)
	if !result.Ready {
		return result, fmt.Errorf("artifact readiness failed after preparation")
	}
	return result, nil
}

func applyRunStartToolProfiles(project, profile, assignments string) error {
	if len(parseRoleAssignments(assignments)) == 0 {
		return nil
	}
	profile = squadnamespace.NormalizeProfile(profile)
	return team.WithProfileLock(project, profile, func() error {
		tm, err := team.ReadProfile(project, profile)
		if err != nil {
			return err
		}
		teamOverlayAfterRead()
		plans, err := buildRunStartToolProfilePlans(tm, profile, assignments)
		if err != nil {
			return err
		}
		if len(plans) == 0 {
			return nil
		}
		return applyGeneratedToolPolicyPlans(tm, func(updated team.Team) error {
			return team.WriteProfileUnderLock(project, profile, updated)
		}, plans, false)
	})
}

func buildRunStartToolProfilePlans(tm team.Team, profile, assignments string) ([]generatedPolicyPlan, error) {
	values := parseRoleAssignments(assignments)
	if len(values) == 0 {
		return nil, nil
	}
	memberIndex := make(map[string]int, len(tm.Members))
	for i, member := range tm.Members {
		memberIndex[member.Role] = i
	}
	roles := make([]string, 0, len(values))
	for role, selected := range values {
		idx, ok := memberIndex[role]
		if !ok {
			return nil, fmt.Errorf("--tool-profile has unknown initial role %q", role)
		}
		switch selected {
		case team.ToolProfileFull:
			if tm.Members[idx].EffectiveToolProfile() != team.ToolProfileFull {
				return nil, fmt.Errorf("member %s already has non-full tool profile %q; explicit full migration is required before preparation", role, tm.Members[idx].EffectiveToolProfile())
			}
		case team.ToolProfileMinimal, team.ToolProfileCoding, team.ToolProfileBrowser, team.ToolProfileData, team.ToolProfileCustom:
		default:
			return nil, fmt.Errorf("--tool-profile for %s must be full, minimal, coding, browser, data, or custom", role)
		}
		roles = append(roles, role)
	}
	sort.Strings(roles)
	plans := make([]generatedPolicyPlan, 0, len(roles))
	for _, role := range roles {
		selected := values[role]
		if selected == team.ToolProfileFull {
			continue
		}
		plan, err := buildGeneratedPolicyPlan(tm, memberIndex[role], generatedToolPolicyOptions{
			Role: role, TeamProfile: profile, Profile: selected,
		}, nil)
		if err != nil {
			return nil, fmt.Errorf("materialize %s tool profile %s: %w", role, selected, err)
		}
		plans = append(plans, plan)
	}
	return plans, nil
}

func parseRoleAssignments(raw string) map[string]string {
	values := map[string]string{}
	for _, part := range strings.Split(raw, ",") {
		role, value, ok := strings.Cut(part, "=")
		if ok && strings.TrimSpace(role) != "" && strings.TrimSpace(value) != "" {
			values[strings.TrimSpace(role)] = strings.TrimSpace(value)
		}
	}
	return values
}
