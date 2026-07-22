package cli

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/bootstrapack"
	"github.com/omriariav/amq-squad/v2/internal/catalog"
	"github.com/omriariav/amq-squad/v2/internal/launch"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/role"
	"github.com/omriariav/amq-squad/v2/internal/rules"
	"github.com/omriariav/amq-squad/v2/internal/runtimeaction"
	"github.com/omriariav/amq-squad/v2/internal/runtimecontrol"
	"github.com/omriariav/amq-squad/v2/internal/team"
	runwizard "github.com/omriariav/amq-squad/v2/internal/wizard"
)

const preparedRunSchema = 3
const preparedRunGoalDeliveryPlanned = "planned_unverified"
const internalPreparedRunTokenEnv = "AMQ_SQUAD_INTERNAL_PREPARED_RUN_TOKEN"
const internalPreparedRunRestoreEnv = "AMQ_SQUAD_INTERNAL_PREPARED_RUN_RESTORE"

type preparedRestoreDescriptor struct {
	Token          preparedRunToken `json:"token"`
	AttemptID      string           `json:"attempt_id"`
	RecordDigest   string           `json:"record_digest"`
	SemanticDigest string           `json:"semantic_digest"`
}

func encodePreparedRestoreDescriptor(d preparedRestoreDescriptor) string {
	b, _ := json.Marshal(d)
	return string(b)
}

type preparedRunManifest struct {
	SchemaVersion       int                                  `json:"schema_version"`
	Generation          string                               `json:"generation"`
	Project             string                               `json:"project"`
	Profile             string                               `json:"profile"`
	Session             string                               `json:"session"`
	Namespace           string                               `json:"namespace"`
	LaunchShape         string                               `json:"launch_shape"`
	InitialRoster       []string                             `json:"initial_roster"`
	StagedRoster        []string                             `json:"staged_roster"`
	Lead                string                               `json:"lead"`
	ExecutionMode       string                               `json:"execution_mode"`
	ControlRoot         string                               `json:"control_root"`
	TargetRoot          string                               `json:"target_root"`
	TargetContract      string                               `json:"target_contract,omitempty"`
	LeadMode            string                               `json:"lead_mode"`
	Topology            preparedRunTopology                  `json:"topology"`
	Members             map[string]preparedRunMemberIdentity `json:"members"`
	StagedMembers       map[string]preparedRunMemberIdentity `json:"staged_members"`
	Environment         preparedRunEnvironment               `json:"environment"`
	GoalText            string                               `json:"goal_text"`
	GoalNamespace       string                               `json:"goal_namespace"`
	GoalDigest          string                               `json:"goal_digest"`
	GoalSource          string                               `json:"goal_source"`
	GoalDeliveryState   string                               `json:"goal_delivery_state"`
	ArtifactDigests     map[string]string                    `json:"artifact_digests"`
	RoleDigests         map[string]string                    `json:"role_digests"`
	BootstrapDigests    map[string]string                    `json:"bootstrap_digests"`
	BootstrapBindings   map[string]string                    `json:"bootstrap_goal_bindings"`
	PreparationRecord   preparedRunPreparationRecord         `json:"preparation_record"`
	ResumeAuthorization preparedRunResumeAuthorization       `json:"resume_authorization"`
	PreparedAt          time.Time                            `json:"prepared_at"`
}

type preparedRunPreparationRecord struct {
	Generation  string `json:"generation"`
	Namespace   string `json:"namespace"`
	LaunchShape string `json:"launch_shape"`
	Lead        string `json:"lead"`
}

type preparedRunResumeAuthorization struct {
	Policy          string `json:"policy"`
	SingleUse       bool   `json:"single_use"`
	RecordBound     bool   `json:"record_bound"`
	GenerationBound bool   `json:"generation_bound"`
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
	Role                string   `json:"role"`
	Handle              string   `json:"handle"`
	Binary              string   `json:"binary"`
	Model               string   `json:"model,omitempty"`
	Effort              string   `json:"effort"`
	TaskOwnership       string   `json:"task_ownership"`
	ActorMode           string   `json:"actor_mode"`
	Trust               string   `json:"trust"`
	NativeArgs          []string `json:"native_args"`
	EffectiveArgs       []string `json:"effective_args"`
	ToolProfile         string   `json:"tool_profile"`
	ToolConfig          string   `json:"tool_config,omitempty"`
	ToolMCPConfig       string   `json:"tool_mcp_config,omitempty"`
	ToolAllowlist       []string `json:"tool_allowlist"`
	ToolBlocklist       []string `json:"tool_blocklist"`
	PermissionAllowlist []string `json:"permission_allowlist"`
	LauncherAuthority   []string `json:"launcher_preauthorized_actions"`
	NoPreauthorize      bool     `json:"no_preauthorize_inscope"`
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

// preparedRunToken is the immutable identity of one accepted schema-3
// preparation. run start --go pins it for the whole transaction and carries it
// through every child launch and the final goal reservation.
type preparedRunToken struct {
	Generation     string `json:"generation"`
	ManifestDigest string `json:"manifest_digest"`
	GoalNamespace  string `json:"goal_namespace"`
	GoalDigest     string `json:"goal_digest"`
	LaunchAttempt  string `json:"launch_attempt,omitempty"`
}

type preparedRunIdentityMismatchError struct {
	detail string
}

func (e *preparedRunIdentityMismatchError) Error() string {
	return e.detail
}

func preparedRunIdentityMismatchf(format string, args ...any) error {
	return &preparedRunIdentityMismatchError{detail: fmt.Sprintf(format, args...)}
}

func isPreparedRunIdentityMismatch(err error) bool {
	var mismatch *preparedRunIdentityMismatchError
	return errors.As(err, &mismatch)
}

func encodePreparedRunToken(t preparedRunToken) string {
	b, _ := json.Marshal(t)
	return string(b)
}

func preparedRunTokenFromInternalEnv() (preparedRunToken, error) {
	raw := strings.TrimSpace(os.Getenv(internalPreparedRunTokenEnv))
	if raw == "" {
		return preparedRunToken{}, nil
	}
	var token preparedRunToken
	if err := json.Unmarshal([]byte(raw), &token); err != nil {
		return preparedRunToken{}, fmt.Errorf("invalid internal prepared run token: %w", err)
	}
	if !token.complete() {
		return preparedRunToken{}, fmt.Errorf("internal prepared run token is incomplete")
	}
	if err := validatePreparedRunTokenPathIDs(token, token.LaunchAttempt != ""); err != nil {
		return preparedRunToken{}, fmt.Errorf("invalid internal prepared run token: %w", err)
	}
	return token, nil
}

func envWithoutPreparedRunToken(env []string) []string {
	prefix := internalPreparedRunTokenEnv + "="
	out := make([]string, 0, len(env))
	for _, entry := range env {
		if !strings.HasPrefix(entry, prefix) {
			out = append(out, entry)
		}
	}
	return out
}

func envWithoutPreparedRunRestore(env []string) []string {
	prefix := internalPreparedRunRestoreEnv + "="
	out := make([]string, 0, len(env))
	for _, entry := range env {
		if !strings.HasPrefix(entry, prefix) {
			out = append(out, entry)
		}
	}
	return out
}

func preparedRestoreDescriptorFromInternalEnv() (*preparedRestoreDescriptor, error) {
	raw := strings.TrimSpace(os.Getenv(internalPreparedRunRestoreEnv))
	if raw == "" {
		return nil, nil
	}
	var d preparedRestoreDescriptor
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		return nil, fmt.Errorf("invalid internal prepared restore descriptor: %w", err)
	}
	if !d.Token.complete() || strings.TrimSpace(d.AttemptID) == "" || strings.TrimSpace(d.RecordDigest) == "" || strings.TrimSpace(d.SemanticDigest) == "" {
		return nil, fmt.Errorf("internal prepared restore descriptor is incomplete")
	}
	if err := validatePreparedRunTokenPathIDs(d.Token, true); err != nil {
		return nil, fmt.Errorf("invalid internal prepared restore descriptor: %w", err)
	}
	if err := validatePreparedRunPathID("prepared resume attempt", d.AttemptID); err != nil {
		return nil, fmt.Errorf("invalid internal prepared restore descriptor: %w", err)
	}
	return &d, nil
}

func preparedRestoreRecordDigest(rec launch.Record) string {
	b, _ := json.Marshal(rec)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func preparedRestoreSemanticDigest(rec launch.Record) string {
	clone := preparedRestoreSemanticRecord(rec)
	b, _ := json.Marshal(clone)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func preparedRestoreSemanticRecord(rec launch.Record) launch.Record {
	clone := rec
	if clone.Conversation != "" {
		clone.Argv = stripConversationRestoreArgs(clone.Binary, clone.Argv, clone.Conversation)
	}
	clone.AgentPID, clone.AgentTTY, clone.StartedAt = 0, "", time.Time{}
	clone.Schema, clone.AdoptionMode, clone.LauncherPaneID = 0, "", ""
	if len(clone.CodexArgs) == 0 {
		clone.CodexArgs = nil
	}
	if len(clone.ClaudeArgs) == 0 {
		clone.ClaudeArgs = nil
	}
	if len(clone.LauncherArgs) == 0 {
		clone.LauncherArgs = nil
	}
	if len(clone.ToolAllowlist) == 0 {
		clone.ToolAllowlist = nil
	}
	if len(clone.ToolBlocklist) == 0 {
		clone.ToolBlocklist = nil
	}
	if len(clone.PreauthorizedActions) == 0 {
		clone.PreauthorizedActions = nil
	}
	if len(clone.LauncherPreauthorizedActions) == 0 {
		clone.LauncherPreauthorizedActions = nil
	}
	if len(clone.ExplicitAllowedTools) == 0 {
		clone.ExplicitAllowedTools = nil
	}
	if len(clone.WakeInjectArgs) == 0 {
		clone.WakeInjectArgs = nil
	}
	clone.BootstrapExpectation, clone.Tmux, clone.Terminal = nil, nil, nil
	return clone
}

func preparedRunTokenFromSnapshot(manifest preparedRunManifest, digest string) preparedRunToken {
	return preparedRunToken{
		Generation: strings.TrimSpace(manifest.Generation), ManifestDigest: strings.TrimSpace(digest),
		GoalNamespace: strings.TrimSpace(manifest.GoalNamespace), GoalDigest: strings.TrimSpace(manifest.GoalDigest),
	}
}

func (t preparedRunToken) empty() bool {
	return t.Generation == "" && t.ManifestDigest == "" && t.GoalNamespace == "" && t.GoalDigest == ""
}

func (t preparedRunToken) complete() bool {
	return t.Generation != "" && t.ManifestDigest != "" && t.GoalNamespace != "" && t.GoalDigest != ""
}

func (t preparedRunToken) generationRef() preparedRunToken {
	t.LaunchAttempt = ""
	return t
}

func samePreparedRunGeneration(left, right preparedRunToken) bool {
	return left.generationRef() == right.generationRef()
}

func validatePreparedRunToken(t preparedRunToken, manifest preparedRunManifest, digest string) error {
	if t.empty() {
		return nil
	}
	if !t.complete() {
		return fmt.Errorf("prepared run token is incomplete")
	}
	if err := validatePreparedRunTokenPathIDs(t, t.LaunchAttempt != ""); err != nil {
		return err
	}
	current := preparedRunTokenFromSnapshot(manifest, digest)
	if !samePreparedRunGeneration(current, t) {
		return preparedRunIdentityMismatchf("prepared run token changed: accepted_generation=%s current_generation=%s accepted_digest=%s current_digest=%s", t.Generation, current.Generation, t.ManifestDigest, current.ManifestDigest)
	}
	return nil
}

func preparedRunTokenForContext(context *preparedLaunchRecordContext) preparedRunToken {
	if context == nil {
		return preparedRunToken{}
	}
	return preparedRunTokenFromSnapshot(context.Manifest, context.Digest)
}

func preparedRunTokenFromRecord(rec launch.Record) preparedRunToken {
	return preparedRunToken{
		Generation: strings.TrimSpace(rec.PreparedRunGeneration), ManifestDigest: strings.TrimSpace(rec.PreparedRunDigest),
		GoalNamespace: strings.TrimSpace(rec.PreparedRunGoalNamespace), GoalDigest: strings.TrimSpace(rec.PreparedRunGoalDigest),
		LaunchAttempt: strings.TrimSpace(rec.PreparedRunLaunchAttempt),
	}
}

func applyPreparedRunTokenToRecord(rec *launch.Record, token preparedRunToken) {
	if rec == nil || token.empty() {
		return
	}
	rec.PreparedRunGeneration = token.Generation
	rec.PreparedRunDigest = token.ManifestDigest
	rec.PreparedRunGoalNamespace = token.GoalNamespace
	rec.PreparedRunGoalDigest = token.GoalDigest
	rec.PreparedRunLaunchAttempt = token.LaunchAttempt
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

func acceptedMemberIdentity(tm team.Team, member team.Member, profile, session string) preparedRunMemberIdentity {
	return resolvedMemberIdentity(tm, member, profile, session, nil, tm.BinaryArgs, false)
}

func partitionPreparedRunMembers(members []team.Member, session string, stagedRoles []string) ([]team.Member, []team.Member, error) {
	active, skipped := filterMembersBySession(members, session)
	configured := make(map[string]team.Member, len(members))
	for _, member := range members {
		configured[member.Role] = member
	}
	staged := make([]team.Member, 0, len(stagedRoles))
	for _, role := range stagedRoles {
		member, ok := configured[role]
		if !ok {
			return nil, nil, fmt.Errorf("staged role %q has no complete profile member definition; configure its handle, binary, model/args, and tool policy before preparation", role)
		}
		staged = append(staged, member)
	}
	initial := make([]team.Member, 0, len(active))
	for _, member := range active {
		if !containsRole(stagedRoles, member.Role) {
			initial = append(initial, member)
		}
	}
	for _, member := range skipped {
		if !containsRole(stagedRoles, member.Role) {
			return nil, nil, fmt.Errorf("preparation excludes profile member %q pinned to session %q; add it to --staged-roles or prepare its own session explicitly", member.Role, member.Session)
		}
	}
	return initial, staged, nil
}

func resolvedMemberIdentity(tm team.Team, member team.Member, profile, session string, modelOverrides map[string]string, binaryArgs map[string][]string, noPreauthorize bool) preparedRunMemberIdentity {
	binary := normalizedAgentBinary(member.Binary)
	nativeArgs := composeBinaryArgs(member.Binary, binaryArgsFor(member.Binary, binaryArgs), member.ExtraArgs())
	model := memberResolvedModel(member, modelOverrides, binaryArgs)
	toolAndNativeArgs := composeBinaryArgs(member.Binary, member.ToolArgs(), nativeArgs)
	trust := defaultTrustMode()
	if strings.TrimSpace(tm.Trust) != "" {
		// Team validation already rejects an invalid persisted trust value. Keep
		// this identity helper total while still matching launch resolution.
		if resolved, err := normalizeTrustMode(tm.Trust); err == nil {
			trust = resolved
		}
	}
	effectiveArgs := launchDefaultChildArgsWithTrust(member.Binary, true, modelArgsForBinary(member.Binary, model), toolAndNativeArgs, trust)
	var launcherAuthority []string
	if binary == "claude" {
		if !noPreauthorize && tm.Orchestrated && strings.TrimSpace(member.Role) != strings.TrimSpace(tm.Lead) {
			launcherAuthority = append(launcherAuthority, claudeInScopePreauthAllowlist(session)...)
		}
		launcherAuthority = append(launcherAuthority, member.PermissionAllowlist...)
		launcherAuthority = dedupeSortedStrings(launcherAuthority)
	}
	return preparedRunMemberIdentity{
		Role: member.Role, Handle: memberHandle(member), Binary: binary,
		Model: model, Effort: effortFromEffectiveArgs(binary, effectiveArgs), TaskOwnership: acceptedTaskOwnership(tm, member), ActorMode: team.EffectiveActorMode(tm, member),
		Trust: trust, NativeArgs: append([]string(nil), nativeArgs...), EffectiveArgs: append([]string(nil), effectiveArgs...),
		ToolProfile: member.EffectiveToolProfile(), ToolConfig: member.ToolConfig, ToolMCPConfig: member.ToolMCPConfig,
		ToolAllowlist: dedupeSortedStrings(member.ToolAllowlist), ToolBlocklist: dedupeSortedStrings(member.ToolBlocklist),
		PermissionAllowlist: dedupeSortedStrings(member.PermissionAllowlist),
		LauncherAuthority:   launcherAuthority, NoPreauthorize: noPreauthorize,
	}
}

func effortFromEffectiveArgs(binary string, args []string) string {
	member := team.Member{Binary: binary}
	switch normalizedAgentBinary(binary) {
	case "codex":
		member.CodexArgs = append([]string(nil), args...)
	case "claude":
		member.ClaudeArgs = append([]string(nil), args...)
	}
	return memberEffort(member)
}

func canonicalLaunchRecordArgs(rec launch.Record) []string {
	args := append([]string(nil), rec.Argv...)
	if len(rec.LauncherPreauthorizedActions) == 0 {
		return args
	}
	if len(rec.ExplicitAllowedTools) > 0 {
		return replaceClaudeAllowedTools(args, rec.ExplicitAllowedTools)
	}
	return stripRecordedLauncherPreauth(args, rec.PreauthorizedActions)
}

func memberResolvedEffort(member team.Member, binaryArgs map[string][]string) string {
	resolved := member
	args := composeBinaryArgs(member.Binary, binaryArgsFor(member.Binary, binaryArgs), member.ExtraArgs())
	switch normalizedAgentBinary(member.Binary) {
	case "codex":
		resolved.CodexArgs = args
	case "claude":
		resolved.ClaudeArgs = args
	}
	return memberEffort(resolved)
}

type preparedLaunchRecordContext struct {
	Manifest preparedRunManifest
	Digest   string
	Team     team.Team
	Member   team.Member
	Binding  acceptedGoalBinding
}

func preparedContextForLaunchRecord(rec launch.Record) (*preparedLaunchRecordContext, error) {
	return preparedContextForLaunchRecordMode(rec, false)
}

func preparedContextForLaunchRecordMode(rec launch.Record, restoring bool) (*preparedLaunchRecordContext, error) {
	project := strings.TrimSpace(rec.TeamHome)
	if project == "" {
		project = strings.TrimSpace(rec.CWD)
	}
	profile := squadnamespace.NormalizeProfile(rec.TeamProfile)
	session := strings.TrimSpace(rec.Session)
	manifest, manifestDigest, err := readPreparedRunManifestSnapshot(project, profile, session)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if manifest.Project != project || manifest.Profile != profile || manifest.Session != session || manifest.Namespace != profile+"/"+session {
		return nil, fmt.Errorf("prepared launch record namespace drift: accepted=%s current=%s/%s", manifest.Namespace, profile, session)
	}
	if !restoring && manifest.GoalDeliveryState != preparedRunGoalDeliveryPlanned {
		return nil, fmt.Errorf("prepared goal delivery state %q is invalid; want %q", manifest.GoalDeliveryState, preparedRunGoalDeliveryPlanned)
	}
	if restoring && manifest.GoalDeliveryState != preparedRunGoalDeliveryPlanned {
		return nil, fmt.Errorf("prepared goal delivery state %q is not restorable; want %q", manifest.GoalDeliveryState, preparedRunGoalDeliveryPlanned)
	}
	tm, err := team.ReadProfile(project, profile)
	if err != nil {
		return nil, err
	}
	binding := acceptedGoalBinding{Text: manifest.GoalText, Source: manifest.GoalSource, Namespace: manifest.GoalNamespace, Digest: manifest.GoalDigest}
	if err := validateAcceptedGoalBinding(binding); err != nil {
		return nil, err
	}
	if staged, ok := manifest.StagedMembers[rec.Role]; ok && containsRole(manifest.StagedRoster, rec.Role) {
		if staged.Role != rec.Role || staged.Handle != rec.Handle {
			return nil, fmt.Errorf("launch record staged actor %s/%s differs from accepted identity %s/%s", rec.Role, rec.Handle, staged.Role, staged.Handle)
		}
		if err := validateCurrentPreparedStagedIdentity(project, manifest, rec.Role); err != nil {
			return nil, err
		}
		member := team.Member{}
		for _, candidate := range tm.Members {
			if candidate.Role == rec.Role && memberHandle(candidate) == rec.Handle {
				member = candidate
				break
			}
		}
		recordToken := preparedRunTokenFromRecord(rec)
		launchIdentity := staged
		if strings.TrimSpace(recordToken.LaunchAttempt) != "" {
			if err := validatePreparedRunToken(recordToken, manifest, manifestDigest); err != nil {
				return nil, fmt.Errorf("launch record staged prepared identity differs from accepted preparation: %w", err)
			}
			claim, err := currentPreparedRunStagedClaim(project, profile, session, recordToken.generationRef(), rec.Role)
			if err != nil {
				return nil, err
			}
			if claim.ClaimID != recordToken.LaunchAttempt || !reflect.DeepEqual(claim.Accepted, staged) {
				return nil, preparedRunIdentityMismatchf("staged launch record is not bound to the exact authoritative claim")
			}
			launchIdentity = claim.Effective
		}
		actualNativeArgs := rec.ClaudeArgs
		if launchIdentity.Binary == "codex" {
			actualNativeArgs = rec.CodexArgs
		}
		actualEffectiveArgs := canonicalLaunchRecordArgs(rec)
		if restoring && rec.Conversation != "" {
			actualEffectiveArgs = stripConversationRestoreArgs(rec.Binary, actualEffectiveArgs, rec.Conversation)
		}
		if normalizedAgentBinary(rec.Binary) != launchIdentity.Binary || rec.Model != launchIdentity.Model || !sameFilesystemPath(rec.CWD, member.EffectiveCWD(tm.Project)) || rec.Trust != launchIdentity.Trust || !reflect.DeepEqual(actualNativeArgs, launchIdentity.NativeArgs) || !reflect.DeepEqual(dedupeSortedStrings(rec.LauncherPreauthorizedActions), launchIdentity.LauncherAuthority) || rec.NoPreauthorizeInScope != launchIdentity.NoPreauthorize || !reflect.DeepEqual(actualEffectiveArgs, launchIdentity.EffectiveArgs) || effortFromEffectiveArgs(rec.Binary, actualEffectiveArgs) != launchIdentity.Effort || rec.ToolProfile != launchIdentity.ToolProfile || rec.ToolConfig != launchIdentity.ToolConfig || rec.ToolMCPConfig != launchIdentity.ToolMCPConfig || !reflect.DeepEqual(dedupeSortedStrings(rec.ToolAllowlist), launchIdentity.ToolAllowlist) || !reflect.DeepEqual(dedupeSortedStrings(rec.ToolBlocklist), launchIdentity.ToolBlocklist) {
			return nil, fmt.Errorf("actual staged launch record input for %s differs from accepted binary/model/args/tool identity: accepted=%+v actual={binary:%s handle:%s model:%s trust:%s native:%v effective:%v effort:%s launcher_authority:%v no_preauthorize:%t tool_profile:%s tool_config:%s tool_mcp:%s tool_allow:%v tool_block:%v}", rec.Role, launchIdentity, normalizedAgentBinary(rec.Binary), rec.Handle, rec.Model, rec.Trust, actualNativeArgs, actualEffectiveArgs, effortFromEffectiveArgs(rec.Binary, actualEffectiveArgs), dedupeSortedStrings(rec.LauncherPreauthorizedActions), rec.NoPreauthorizeInScope, rec.ToolProfile, rec.ToolConfig, rec.ToolMCPConfig, dedupeSortedStrings(rec.ToolAllowlist), dedupeSortedStrings(rec.ToolBlocklist))
		}
		if !recordToken.empty() {
			if err := validatePreparedRunToken(recordToken, manifest, manifestDigest); err != nil {
				return nil, fmt.Errorf("launch record staged prepared identity differs from accepted preparation: %w", err)
			}
		}
		if restoring && rec.GoalBinding != nil {
			return nil, fmt.Errorf("restored staged worker unexpectedly carries a goal binding")
		}
		return &preparedLaunchRecordContext{Manifest: manifest, Digest: manifestDigest, Team: tm, Member: member, Binding: binding}, nil
	}
	readiness := calculateRunReadinessWithContext(project, profile, session, acceptedRunContext{Version: manifest.Environment.BinaryVersion, Topology: manifest.Topology})
	if !readiness.Ready {
		for _, row := range readiness.Rows {
			if row.Status != "ready" {
				return nil, fmt.Errorf("prepared launch readiness drift [%s/%s]: %s", row.Artifact, row.Status, row.Evidence)
			}
		}
		return nil, fmt.Errorf("prepared launch readiness drift")
	}
	active, _ := filterMembersBySession(tm.Members, session)
	tm.Members = active
	var member team.Member
	found := false
	for _, candidate := range tm.Members {
		if candidate.Role == rec.Role && memberHandle(candidate) == rec.Handle {
			member, found = candidate, true
			break
		}
	}
	if !found || !containsRole(manifest.InitialRoster, rec.Role) {
		return nil, fmt.Errorf("launch record actor %s/%s is not in the accepted exact-session roster", rec.Role, rec.Handle)
	}
	accepted, ok := manifest.Members[member.Role]
	if !ok || !reflect.DeepEqual(accepted, acceptedMemberIdentity(tm, member, profile, session)) {
		return nil, fmt.Errorf("launch record member identity for %s differs from accepted preparation", member.Role)
	}
	actualNativeArgs := rec.ClaudeArgs
	if accepted.Binary == "codex" {
		actualNativeArgs = rec.CodexArgs
	}
	actualEffectiveArgs := canonicalLaunchRecordArgs(rec)
	if restoring && rec.Conversation != "" {
		actualEffectiveArgs = stripConversationRestoreArgs(rec.Binary, actualEffectiveArgs, rec.Conversation)
	}
	recordToken := preparedRunTokenFromRecord(rec)
	if !recordToken.empty() {
		if err := validatePreparedRunToken(recordToken, manifest, manifestDigest); err != nil {
			return nil, fmt.Errorf("launch record prepared identity differs from accepted preparation: %w", err)
		}
	}
	if normalizedAgentBinary(rec.Binary) != accepted.Binary || rec.Handle != accepted.Handle || rec.Model != accepted.Model || !sameFilesystemPath(rec.CWD, member.EffectiveCWD(tm.Project)) || rec.Trust != accepted.Trust || !reflect.DeepEqual(actualNativeArgs, accepted.NativeArgs) || !reflect.DeepEqual(dedupeSortedStrings(rec.LauncherPreauthorizedActions), accepted.LauncherAuthority) || rec.NoPreauthorizeInScope != accepted.NoPreauthorize || !reflect.DeepEqual(actualEffectiveArgs, accepted.EffectiveArgs) || effortFromEffectiveArgs(rec.Binary, actualEffectiveArgs) != accepted.Effort || rec.ToolProfile != accepted.ToolProfile || rec.ToolConfig != accepted.ToolConfig || rec.ToolMCPConfig != accepted.ToolMCPConfig || !reflect.DeepEqual(dedupeSortedStrings(rec.ToolAllowlist), accepted.ToolAllowlist) || !reflect.DeepEqual(dedupeSortedStrings(rec.ToolBlocklist), accepted.ToolBlocklist) {
		return nil, fmt.Errorf("actual launch record input for %s differs from accepted binary/handle/cwd/tool identity: accepted=%+v actual={binary:%s handle:%s model:%s trust:%s native:%v effective:%v effort:%s launcher_authority:%v no_preauthorize:%t tool_profile:%s tool_config:%s tool_mcp:%s tool_allow:%v tool_block:%v}", member.Role, accepted, normalizedAgentBinary(rec.Binary), rec.Handle, rec.Model, rec.Trust, actualNativeArgs, actualEffectiveArgs, effortFromEffectiveArgs(rec.Binary, actualEffectiveArgs), dedupeSortedStrings(rec.LauncherPreauthorizedActions), rec.NoPreauthorizeInScope, rec.ToolProfile, rec.ToolConfig, rec.ToolMCPConfig, dedupeSortedStrings(rec.ToolAllowlist), dedupeSortedStrings(rec.ToolBlocklist))
	}
	if restoring && member.Role == tm.Lead {
		expected, err := preparedGoalBinding(tm, manifest.Profile, manifest.Session, member, binding)
		if err != nil {
			return nil, err
		}
		switch {
		case rec.GoalBinding != nil && rec.GoalBinding.DeliveryState == goalBindingDeliveryPrepared:
			if !reflect.DeepEqual(rec.GoalBinding, expected) {
				return nil, fmt.Errorf("restored prepared lead binding differs from accepted preparation")
			}
		case rec.GoalBinding != nil && rec.GoalBinding.DeliveryState == goalBindingDeliveryDelivered:
			contract, err := goalDeliveryContractForBinary(rec.Binary)
			if err != nil {
				return nil, err
			}
			goal, attempt, err := goalBindingPayload(rec.GoalBinding, contract)
			expectedPrompt := contract.prompt(binding.Text, tm, manifest.Profile, manifest.Session, member.Role, attempt)
			if err != nil || goal != binding.Text || strings.TrimSpace(attempt) == "" || !exactGoalBinding(rec.GoalBinding, contract, binding.Text, attempt, expectedPrompt, "goal-control") || rec.GoalBinding.Detail != contract.Label+" delivered as a first-class claim-once control action" {
				return nil, fmt.Errorf("restored delivered lead binding lacks exact claim-once delivery identity")
			}
			if err := validatePreparedRestoreDeliveredEvidence(rec, manifest, tm, member, contract, attempt); err != nil {
				return nil, err
			}
		case rec.GoalBinding != nil:
			return nil, fmt.Errorf("restored lead binding state %q is not restorable", rec.GoalBinding.DeliveryState)
		default:
			return nil, fmt.Errorf("restored lead lacks prepared or delivered goal binding")
		}
	} else if restoring && rec.GoalBinding != nil {
		return nil, fmt.Errorf("restored worker unexpectedly carries a goal binding")
	}
	return &preparedLaunchRecordContext{Manifest: manifest, Digest: manifestDigest, Team: tm, Member: member, Binding: binding}, nil
}

func validatePreparedRestoreDeliveredEvidence(rec launch.Record, manifest preparedRunManifest, tm team.Team, member team.Member, contract goalDeliveryContract, attemptID string) error {
	project := strings.TrimSpace(rec.TeamHome)
	if project == "" {
		project = rec.CWD
	}
	ns := squadnamespace.Resolve(project, manifest.Profile, manifest.Session)
	attemptPath, err := goalAttemptPath(project, manifest.Profile, manifest.Session, attemptID)
	if err != nil {
		return err
	}
	attempt, err := readGoalAttempt(attemptPath, attemptID)
	if err != nil {
		return fmt.Errorf("restored delivered goal attempt evidence: %w", err)
	}
	if err := validateResumeGoalAttempt(attempt, project, manifest.Profile, manifest.Session, member.Role, rec.Handle, manifest.GoalText, attemptID, ns); err != nil {
		return fmt.Errorf("restored delivered goal attempt evidence differs: %w", err)
	}
	claimBytes, err := os.ReadFile(goalAttemptClaimPath(attemptPath))
	if err != nil {
		return fmt.Errorf("restored delivered goal claim evidence: %w", err)
	}
	var claim goalAttemptClaim
	if err := json.Unmarshal(claimBytes, &claim); err != nil {
		return fmt.Errorf("restored delivered goal claim evidence is invalid: %w", err)
	}
	if err := validateResumeGoalClaim(claim, attempt); err != nil || claim.Route != contract.ClaimRoute {
		return fmt.Errorf("restored delivered goal claim route differs from binary contract")
	}
	receiptRoot, receiptDir, err := openReceiptDirRoot(project, manifest.Profile, manifest.Session, false)
	if err != nil {
		return fmt.Errorf("restored delivered receipt evidence: %w", err)
	}
	defer receiptRoot.Close()
	receiptPath := filepath.Join(receiptDir, attemptID+".json")
	receipt, err := readDeliveryReceiptAt(receiptRoot, attemptID+".json", receiptPath)
	if err != nil {
		return fmt.Errorf("restored delivered receipt evidence is invalid: %w", err)
	}
	token := preparedRunTokenFromRecord(rec)
	if receipt.SchemaVersion != deliveryReceiptSchemaVersion || receipt.AttemptID != attemptID || receipt.Kind != contract.Mode || receipt.Method != contract.Method || receipt.Status != contract.Mode+"_delivered" || canonicalPath(receipt.Target.ProjectDir) != canonicalPath(project) || !squadnamespace.ProfilesEqual(receipt.Target.Profile, manifest.Profile) || receipt.Target.Session != manifest.Session || receipt.Target.NamespaceID != manifest.Namespace || receipt.Target.Role != member.Role || receipt.Target.Handle != rec.Handle || receipt.Target.ExecutionMode != effectiveTeamExecutionMode(tm) || strings.TrimSpace(receipt.PaneID) == "" || receipt.Fallback || receipt.AMQInvoked || receipt.PreparedRunGeneration != token.Generation || receipt.PreparedRunDigest != token.ManifestDigest || receipt.PreparedRunGoalNamespace != token.GoalNamespace || receipt.PreparedRunGoalDigest != token.GoalDigest || !deliveryReceiptHasStage(receipt, "launch_record_updated") {
		return fmt.Errorf("restored delivered receipt evidence differs from exact prepared claim-once delivery")
	}
	return nil
}

func deliveryReceiptHasStage(receipt deliveryReceiptData, stage string) bool {
	for _, evidence := range receipt.Stages {
		if evidence.State == stage {
			return true
		}
	}
	return false
}

func preparedGoalBindingForLaunchRecord(rec launch.Record) (*launch.GoalBinding, error) {
	context, err := preparedContextForLaunchRecord(rec)
	if err != nil || context == nil || context.Member.Role != context.Team.Lead {
		return nil, err
	}
	return preparedGoalBinding(context.Team, context.Manifest.Profile, context.Manifest.Session, context.Member, context.Binding)
}

func validatePreparedBootstrapPromptForLaunch(rec launch.Record, prompt string) error {
	context, err := preparedContextForLaunchRecord(rec)
	if err != nil {
		return err
	}
	return validatePreparedBootstrapPromptAgainstContext(rec, prompt, context)
}

func revalidatePreparedBootstrapPromptForLaunch(rec launch.Record, prompt string, expected *preparedLaunchRecordContext) error {
	return revalidatePreparedBootstrapPromptForLaunchMode(rec, prompt, expected, false)
}

func revalidatePreparedBootstrapPromptForLaunchMode(rec launch.Record, prompt string, expected *preparedLaunchRecordContext, restoring bool) error {
	current, err := preparedContextForLaunchRecordMode(rec, restoring)
	if err != nil {
		return err
	}
	if expected != nil {
		if current == nil {
			return fmt.Errorf("accepted prepared launch identity disappeared before bootstrap validation")
		}
		if current.Manifest.Generation != expected.Manifest.Generation || current.Digest != expected.Digest || !reflect.DeepEqual(current.Manifest, expected.Manifest) {
			return fmt.Errorf("accepted prepared launch identity changed before bootstrap validation")
		}
	}
	if restoring && strings.TrimSpace(prompt) == "" {
		if strings.TrimSpace(rec.Conversation) == "" {
			return fmt.Errorf("prepared restore without saved conversation requires the accepted bootstrap prompt")
		}
		return nil
	}
	return validatePreparedBootstrapPromptAgainstContextMode(rec, prompt, current, restoring)
}

func validatePreparedBootstrapPromptAgainstContext(rec launch.Record, prompt string, context *preparedLaunchRecordContext) error {
	return validatePreparedBootstrapPromptAgainstContextMode(rec, prompt, context, false)
}

func validatePreparedBootstrapPromptAgainstContextMode(rec launch.Record, prompt string, context *preparedLaunchRecordContext, restoring bool) error {
	if context == nil {
		return nil
	}
	expectedBinding := context.Manifest.BootstrapBindings[context.Member.Role]
	if !bootstrapHasExactLine(prompt, "- "+expectedBinding) {
		return fmt.Errorf("actual bootstrap binding for %s differs from accepted %q", context.Member.Role, expectedBinding)
	}
	if context.Member.Role == context.Team.Lead {
		if rec.GoalBinding == nil || (rec.GoalBinding.DeliveryState == goalBindingDeliveryPrepared && rec.GoalBinding.Source != "prepared-run") || (rec.GoalBinding.DeliveryState == goalBindingDeliveryDelivered && (!restoring || rec.GoalBinding.Source != "goal-control")) || (rec.GoalBinding.DeliveryState != goalBindingDeliveryPrepared && rec.GoalBinding.DeliveryState != goalBindingDeliveryDelivered) {
			return fmt.Errorf("actual lead launch record for %s does not carry the accepted planned/unverified goal binding", context.Member.Role)
		}
		contract, err := goalDeliveryContractForBinary(rec.Binary)
		if err != nil {
			return err
		}
		goal, _, err := goalBindingPayload(rec.GoalBinding, contract)
		if err != nil || goal != context.Binding.Text {
			return fmt.Errorf("actual lead launch goal differs from accepted preparation")
		}
	} else if rec.GoalBinding != nil {
		return fmt.Errorf("non-lead launch record %s unexpectedly carries a goal binding", context.Member.Role)
	}
	digest := digestRunArtifactBytes([]byte(prompt))
	accepted := context.Manifest.BootstrapDigests[context.Member.Role]
	expectedTeam, expectedMember := context.Team, context.Member
	if containsRole(context.Manifest.StagedRoster, context.Member.Role) && strings.TrimSpace(rec.PreparedRunLaunchAttempt) != "" {
		projected, projectionErr := projectPreparedRunStagedTeamForRecord(context.Team, rec)
		if projectionErr != nil {
			return fmt.Errorf("authoritative staged bootstrap projection for %s failed: %w", context.Member.Role, projectionErr)
		}
		expectedTeam = projected
		claim, claimErr := currentPreparedRunStagedClaim(context.Manifest.Project, context.Manifest.Profile, context.Manifest.Session, preparedRunTokenFromRecord(rec).generationRef(), context.Member.Role)
		if claimErr != nil {
			return claimErr
		}
		if claim.ClaimID != strings.TrimSpace(rec.PreparedRunLaunchAttempt) || claim.BootstrapDigest == "" {
			return preparedRunIdentityMismatchf("staged bootstrap is not bound to the exact authoritative claim digest")
		}
		expectedMember, claimErr = preparedRunStagedProjectedMember(projected, claim)
		if claimErr != nil {
			return claimErr
		}
		accepted = claim.BootstrapDigest
	}
	if accepted != digest {
		expectedPrompt, expectedErr := preparedBootstrap(context.Manifest.Project, context.Manifest.Profile, context.Manifest.Session, context.Binding, expectedTeam, expectedMember, acceptedRunContext{Version: context.Manifest.Environment.BinaryVersion, Topology: context.Manifest.Topology})
		if expectedErr == nil {
			return fmt.Errorf("actual bootstrap digest drift for %s: accepted=%q actual=%q; %s", context.Member.Role, accepted, digest, firstBootstrapPromptDifference(expectedPrompt, prompt))
		}
		return fmt.Errorf("actual bootstrap digest drift for %s: accepted=%q actual=%q", context.Member.Role, accepted, digest)
	}
	return nil
}

func firstBootstrapPromptDifference(expected, actual string) string {
	wantLines, gotLines := strings.Split(expected, "\n"), strings.Split(actual, "\n")
	limit := len(wantLines)
	if len(gotLines) < limit {
		limit = len(gotLines)
	}
	for i := 0; i < limit; i++ {
		if wantLines[i] != gotLines[i] {
			return fmt.Sprintf("first differing line %d: accepted=%q actual=%q", i+1, wantLines[i], gotLines[i])
		}
	}
	return fmt.Sprintf("line count differs: accepted=%d actual=%d", len(wantLines), len(gotLines))
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
	GoalSource    string              `json:"goal_source"`
	GoalDigest    string              `json:"goal_digest"`
	Rows          []runReadinessRow   `json:"rows"`
	Actions       []runtimeActionJSON `json:"actions"`
}

func preparedRunPath(project, profile, session string) string {
	profile = squadnamespace.NormalizeProfile(profile)
	return filepath.Join(project, team.DirName, "prepared", profile, session+".json")
}

func writePreparedRunManifest(path string, manifest preparedRunManifest) error {
	if filepath.Clean(path) != filepath.Clean(preparedRunPath(manifest.Project, manifest.Profile, manifest.Session)) {
		return fmt.Errorf("prepared run publication path does not match manifest namespace")
	}
	return publishPreparedRunGeneration(manifest.Project, manifest.Profile, manifest.Session, manifest)
}

func readPreparedRunManifest(project, profile, session string) (preparedRunManifest, error) {
	manifest, _, err := readPreparedRunManifestSnapshot(project, profile, session)
	return manifest, err
}

func readPreparedRunManifestSnapshot(project, profile, session string) (preparedRunManifest, string, error) {
	path := preparedRunPath(project, profile, session)
	pointerData, err := os.ReadFile(path)
	if err != nil {
		return preparedRunManifest{}, "", err
	}
	var pointer preparedRunPointer
	if err := json.Unmarshal(pointerData, &pointer); err != nil || pointer.SchemaVersion != preparedRunPointerSchema || strings.TrimSpace(pointer.Generation) == "" || strings.TrimSpace(pointer.ManifestDigest) == "" || strings.TrimSpace(pointer.StateDigest) == "" {
		return preparedRunManifest{}, "", fmt.Errorf("prepared run pointer %s is incomplete or legacy; run preparation again", path)
	}
	if err := validatePreparedRunPathID("prepared generation", pointer.Generation); err != nil {
		return preparedRunManifest{}, "", fmt.Errorf("prepared run pointer %s is invalid: %w", path, err)
	}
	manifestPath := preparedRunGenerationManifestPath(project, profile, session, pointer.Generation)
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return preparedRunManifest{}, "", fmt.Errorf("read accepted generation manifest %s: %w", manifestPath, err)
	}
	digest := digestRunArtifactBytes(data)
	if digest != pointer.ManifestDigest {
		return preparedRunManifest{}, "", fmt.Errorf("accepted generation manifest digest changed for %s", pointer.Generation)
	}
	initialStatePath := preparedRunInitialStatePath(project, profile, session, pointer.Generation)
	initialStateData, err := os.ReadFile(initialStatePath)
	if err != nil {
		return preparedRunManifest{}, "", fmt.Errorf("read accepted generation initial state %s: %w", initialStatePath, err)
	}
	if digestRunArtifactBytes(initialStateData) != pointer.StateDigest {
		return preparedRunManifest{}, "", fmt.Errorf("accepted generation initial state digest changed for %s", pointer.Generation)
	}
	initialState, err := readPreparedRunInitialState(initialStatePath)
	if err != nil {
		return preparedRunManifest{}, "", fmt.Errorf("accepted generation initial state is invalid for %s", pointer.Generation)
	}
	var manifest preparedRunManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return preparedRunManifest{}, "", fmt.Errorf("decode prepared run %s: %w", manifestPath, err)
	}
	if manifest.SchemaVersion != preparedRunSchema {
		return preparedRunManifest{}, "", fmt.Errorf("prepared run schema %d is unsupported", manifest.SchemaVersion)
	}
	if strings.TrimSpace(manifest.Generation) == "" {
		return preparedRunManifest{}, "", fmt.Errorf("prepared run %s has no immutable generation", manifestPath)
	}
	if manifest.Generation != pointer.Generation || !samePreparedRunGeneration(initialState.Token, preparedRunTokenFromSnapshot(manifest, digest)) {
		return preparedRunManifest{}, "", fmt.Errorf("prepared run pointer, manifest, and initial state disagree for generation %s", pointer.Generation)
	}
	if manifest.PreparationRecord.Generation != manifest.Generation || manifest.PreparationRecord.Namespace != manifest.Namespace || manifest.PreparationRecord.LaunchShape != manifest.LaunchShape || manifest.PreparationRecord.Lead != manifest.Lead {
		return preparedRunManifest{}, "", fmt.Errorf("prepared run %s has an invalid immutable preparation record", path)
	}
	if manifest.ResumeAuthorization.Policy != "managed_launch_record" || !manifest.ResumeAuthorization.SingleUse || !manifest.ResumeAuthorization.RecordBound || !manifest.ResumeAuthorization.GenerationBound {
		return preparedRunManifest{}, "", fmt.Errorf("prepared run %s has an invalid managed resume authorization", path)
	}
	for _, role := range manifest.InitialRoster {
		if err := team.ValidateRoleID(role); err != nil {
			return preparedRunManifest{}, "", fmt.Errorf("prepared run %s has invalid initial role path token %q", path, role)
		}
		identity, ok := manifest.Members[role]
		if !ok || identity.Role != role {
			return preparedRunManifest{}, "", fmt.Errorf("prepared run %s has no exact initial identity for %q", path, role)
		}
	}
	if len(manifest.StagedMembers) != len(manifest.StagedRoster) {
		return preparedRunManifest{}, "", fmt.Errorf("prepared run %s staged identity set is incomplete", path)
	}
	for _, role := range manifest.StagedRoster {
		if err := team.ValidateRoleID(role); err != nil {
			return preparedRunManifest{}, "", fmt.Errorf("prepared run %s has invalid staged role path token %q", path, role)
		}
		identity, ok := manifest.StagedMembers[role]
		if !ok || identity.Role != role || strings.TrimSpace(identity.Handle) == "" {
			return preparedRunManifest{}, "", fmt.Errorf("prepared run %s has no exact staged identity for %q", path, role)
		}
	}
	canonicalProject, err := canonicalDir(project)
	if err != nil {
		return preparedRunManifest{}, "", err
	}
	manifestProject, err := canonicalDir(manifest.Project)
	if err != nil || manifestProject != canonicalProject {
		return preparedRunManifest{}, "", fmt.Errorf("prepared run %s project identity changed: accepted=%s current=%s", path, manifest.Project, canonicalProject)
	}
	return manifest, digest, nil
}

func newPreparedRunGeneration() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate prepared run identity: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
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

func preparedBootstrap(project, profile, session string, binding acceptedGoalBinding, tm team.Team, member team.Member, context acceptedRunContext) (string, error) {
	runtimeCWD, err := canonicalDir(member.EffectiveCWD(tm.Project))
	if err != nil {
		return "", err
	}
	handle := memberHandle(member)
	env, err := resolveAMQEnvForTeamLaunchProfile(runtimeCWD, profile, session, handle)
	if err != nil {
		return "", err
	}
	root := env.Root
	if context.Topology.ExternalLead && member.Role == tm.Lead {
		root = absoluteAMQRoot(runtimeCWD, root)
	}
	agentDir := filepath.Join(root, "agents", handle)
	requiredAck := !(context.Topology.ExternalLead && member.Role == tm.Lead)
	expectation := &bootstrapack.Expectation{Required: requiredAck}
	if !requiredAck {
		expectation.NotRequiredReason = "external lead is already running in the adopted pane"
	}
	rec := launch.Record{
		Role: member.Role, Handle: handle, Binary: member.Binary,
		ToolProfile: member.EffectiveToolProfile(), ToolConfig: member.ToolConfig,
		Session: session, CWD: runtimeCWD, Root: root,
		TeamHome: project, TeamProfile: profile, SharedWorkstream: true,
		External:             context.Topology.ExternalLead && member.Role == tm.Lead,
		BootstrapExpectation: expectation,
	}
	if err := validateAcceptedGoalBinding(binding); err != nil {
		return "", err
	}
	if member.Role == tm.Lead {
		goalBinding, err := preparedGoalBinding(tm, profile, session, member, binding)
		if err != nil {
			return "", err
		}
		rec.GoalBinding = goalBinding
	}
	bootstrapContext := bootstrapContextFor(rec, agentDir, project)
	bootstrapContext.CurrentTeam, bootstrapContext.Warnings = bootstrapCurrentTeamForTeam(rec, tm)
	goalBinding := bootstrapGoalBindingMode(rec, tm)
	execution := executionContractForTeam(tm, profile, session, goalBinding, "", "dev")
	actorExecution := actorExecutionContractForTeam(tm, member.Role, handle, execution)
	bootstrapContext.Execution = &execution
	bootstrapContext.ActorExecution = &actorExecution
	bootstrapContext.PlannerLead = actorExecution.IsLead && actorExecution.TeamLeadMode == team.LeadModePlanner && !actorExecution.ImplementationAllowedForYou
	prompt, err := buildBootstrapPrompt(bootstrapContext)
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
	for _, required := range required {
		if !strings.Contains(prompt, required) {
			return "", fmt.Errorf("generated bootstrap for %s omits %q", member.Role, required)
		}
	}
	expectedBinding, err := expectedPreparedBootstrapBindingLine(tm, profile, session, member, binding)
	if err != nil {
		return "", err
	}
	if !bootstrapHasExactLine(prompt, "- "+expectedBinding) {
		return "", fmt.Errorf("generated bootstrap for %s omits exact line %q", member.Role, expectedBinding)
	}
	return prompt, nil
}

func expectedPreparedBootstrapBindingLine(tm team.Team, profile, session string, member team.Member, binding acceptedGoalBinding) (string, error) {
	if member.Role != strings.TrimSpace(tm.Lead) {
		return "Goal binding: amq_task_brief", nil
	}
	if err := validateAcceptedGoalBinding(binding); err != nil {
		return "", err
	}
	prepared, err := preparedGoalBinding(tm, profile, session, member, binding)
	if err != nil {
		return "", err
	}
	mode, ok := preparedBootstrapGoalBindingMode(launch.Record{Binary: member.Binary, GoalBinding: prepared})
	if !ok {
		return "", fmt.Errorf("prepared bootstrap binding for %s is not an exact validated prepared-run state", member.Role)
	}
	contract, err := goalDeliveryContractForBinary(member.Binary)
	if err != nil {
		return "", err
	}
	if mode != contract.Mode {
		return "", fmt.Errorf("prepared bootstrap binding for %s resolved mode %q, want %q", member.Role, mode, contract.Mode)
	}
	return "Goal binding: " + mode, nil
}

func bootstrapHasExactLine(prompt, want string) bool {
	for _, line := range strings.Split(prompt, "\n") {
		if strings.TrimSpace(line) == strings.TrimSpace(want) {
			return true
		}
	}
	return false
}

func validatePreparedBootstrapSemantics(tm team.Team, profile, session string, binding acceptedGoalBinding) (map[string]string, error) {
	if strings.TrimSpace(tm.Lead) == "" {
		return nil, fmt.Errorf("preparation bootstrap blocker: declare a lead with `amq-squad team lead set <role>` before preparation")
	}
	lines := make(map[string]string, len(tm.Members))
	for _, member := range tm.Members {
		line, err := expectedPreparedBootstrapBindingLine(tm, profile, session, member, binding)
		if err != nil {
			return nil, fmt.Errorf("preparation bootstrap blocker [%s]: %w", member.Role, err)
		}
		lines[member.Role] = line
	}
	return lines, nil
}

func buildPreparedRunManifest(project, profile, session, shape, stagedRaw string, binding acceptedGoalBinding, context acceptedRunContext) (preparedRunManifest, error) {
	profile = squadnamespace.NormalizeProfile(profile)
	tm, err := team.ReadProfile(project, profile)
	if err != nil {
		return preparedRunManifest{}, err
	}
	allTeam := tm
	staged := sortedUniqueRoles(stagedRaw)
	initialMembers, stagedMembers, err := partitionPreparedRunMembers(tm.Members, session, staged)
	if err != nil {
		return preparedRunManifest{}, err
	}
	tm.Members = initialMembers
	if len(tm.Members) == 0 {
		return preparedRunManifest{}, fmt.Errorf("preparation has no members for session %q", session)
	}
	initial := teamMemberRoles(tm.Members)
	controlRoot, targetRoot := acceptedExecutionRoots(tm)
	generation, err := newPreparedRunGeneration()
	if err != nil {
		return preparedRunManifest{}, err
	}
	manifest := preparedRunManifest{
		SchemaVersion: preparedRunSchema, Generation: generation, Project: project, Profile: profile, Session: session,
		Namespace: profile + "/" + session, LaunchShape: shape, InitialRoster: initial,
		StagedRoster: staged, Lead: tm.Lead, GoalText: binding.Text, GoalNamespace: binding.Namespace,
		GoalDigest: binding.Digest, GoalSource: binding.Source, GoalDeliveryState: preparedRunGoalDeliveryPlanned,
		ExecutionMode: effectiveTeamExecutionMode(tm), ControlRoot: controlRoot, TargetRoot: targetRoot,
		TargetContract: tm.TargetContract, LeadMode: team.EffectiveLeadMode(tm), Topology: context.Topology,
		Members:         map[string]preparedRunMemberIdentity{},
		StagedMembers:   map[string]preparedRunMemberIdentity{},
		Environment:     preparedRunEnvironment{BinaryVersion: strings.TrimSpace(context.Version), SkillVersion: strings.TrimSpace(context.Version), AMQMinimum: doctorMinAMQVersion, Capabilities: append([]string(nil), preparedRunRequiredCapabilities...)},
		ArtifactDigests: map[string]string{}, RoleDigests: map[string]string{}, BootstrapDigests: map[string]string{}, BootstrapBindings: map[string]string{}, PreparedAt: time.Now().UTC(),
	}
	manifest.PreparationRecord = preparedRunPreparationRecord{Generation: generation, Namespace: manifest.Namespace, LaunchShape: shape, Lead: tm.Lead}
	manifest.ResumeAuthorization = preparedRunResumeAuthorization{Policy: "managed_launch_record", SingleUse: true, RecordBound: true, GenerationBound: true}
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
		manifest.Members[member.Role] = acceptedMemberIdentity(tm, member, profile, session)
		bindingLine, err := expectedPreparedBootstrapBindingLine(tm, profile, session, member, binding)
		if err != nil {
			return preparedRunManifest{}, err
		}
		prompt, err := preparedBootstrap(project, profile, session, binding, tm, member, context)
		if err != nil {
			return preparedRunManifest{}, err
		}
		manifest.BootstrapDigests[member.Role] = digestRunArtifactBytes([]byte(prompt))
		manifest.BootstrapBindings[member.Role] = bindingLine
	}
	for _, member := range stagedMembers {
		manifest.StagedMembers[member.Role] = acceptedMemberIdentity(allTeam, member, profile, session)
		bindingLine, err := expectedPreparedBootstrapBindingLine(allTeam, profile, session, member, binding)
		if err != nil {
			return preparedRunManifest{}, err
		}
		prompt, err := preparedBootstrap(project, profile, session, binding, allTeam, member, context)
		if err != nil {
			return preparedRunManifest{}, err
		}
		manifest.BootstrapDigests[member.Role] = digestRunArtifactBytes([]byte(prompt))
		manifest.BootstrapBindings[member.Role] = bindingLine
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
	if manifest.GoalDeliveryState != preparedRunGoalDeliveryPlanned {
		return acceptedGoalBinding{}, fmt.Errorf("prepared goal delivery state %q is invalid; want %q", manifest.GoalDeliveryState, preparedRunGoalDeliveryPlanned)
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

// validatePreparedLaunchBootstrapInputs re-derives the exact, binary-specific
// bootstrap input immediately before any external-lead registration or managed
// worker spawn. Preparation is accepted intent only: the visible lead carries
// the exact planned binding in its launch input, but it remains unverified
// until the post-start delivery CAS succeeds.
func validatePreparedLaunchBootstrapInputs(project, profile, session string, context acceptedRunContext, modelRaw, effortRaw, codexArgsRaw, claudeArgsRaw string) error {
	profile = squadnamespace.NormalizeProfile(profile)
	manifest, err := readPreparedRunManifest(project, profile, session)
	if err != nil {
		return err
	}
	if manifest.GoalDeliveryState != preparedRunGoalDeliveryPlanned {
		return fmt.Errorf("prepared goal delivery state %q is invalid; want %q", manifest.GoalDeliveryState, preparedRunGoalDeliveryPlanned)
	}
	if !reflect.DeepEqual(manifest.Topology, context.Topology) {
		return fmt.Errorf("launch topology differs from accepted bootstrap input: accepted=%+v current=%+v", manifest.Topology, context.Topology)
	}
	tm, err := team.ReadProfile(project, profile)
	if err != nil {
		return err
	}
	initial, _, err := partitionPreparedRunMembers(tm.Members, session, manifest.StagedRoster)
	if err != nil {
		return err
	}
	tm.Members = initial
	effortOverrides, err := parseEffortOverrides(effortRaw)
	if err != nil {
		return err
	}
	tm.Members, err = applyLaunchEffortOverridesCatalog(tm.Members, effortOverrides, loadAgentCatalogAndWarn(project))
	if err != nil {
		return err
	}
	binaryArgs, err := parseBinaryArgFlags(codexArgsRaw, claudeArgsRaw)
	if err != nil {
		return err
	}
	mergedBinaryArgs := mergeBinaryArgs(tm.BinaryArgs, binaryArgs)
	modelOverrides := parseRoleAssignments(modelRaw)
	actualRoles := teamMemberRoles(tm.Members)
	if !sameRoleSet(actualRoles, manifest.InitialRoster) {
		return fmt.Errorf("exact launch roster differs from accepted bootstrap roster: accepted=[%s] actual=[%s]", strings.Join(manifest.InitialRoster, ","), strings.Join(actualRoles, ","))
	}
	acceptedBootstrapCount := len(manifest.InitialRoster) + len(manifest.StagedRoster)
	if len(manifest.BootstrapDigests) != acceptedBootstrapCount || len(manifest.BootstrapBindings) != acceptedBootstrapCount {
		return fmt.Errorf("accepted bootstrap rows do not exactly match accepted initial+staged roster: roles=%d digests=%d bindings=%d", acceptedBootstrapCount, len(manifest.BootstrapDigests), len(manifest.BootstrapBindings))
	}
	for _, member := range tm.Members {
		actual := resolvedMemberIdentity(tm, member, profile, session, modelOverrides, mergedBinaryArgs, false)
		if accepted, ok := manifest.Members[member.Role]; !ok || !reflect.DeepEqual(accepted, actual) {
			return fmt.Errorf("actual launch identity drift for %s: accepted=%+v actual=%+v", member.Role, accepted, actual)
		}
	}
	binding := acceptedGoalBinding{Text: manifest.GoalText, Source: manifest.GoalSource, Namespace: manifest.GoalNamespace, Digest: manifest.GoalDigest}
	if err := validateAcceptedGoalBinding(binding); err != nil {
		return err
	}
	for _, member := range tm.Members {
		expectedBinding, err := expectedPreparedBootstrapBindingLine(tm, profile, session, member, binding)
		if err != nil {
			return err
		}
		if got := manifest.BootstrapBindings[member.Role]; got != expectedBinding {
			return fmt.Errorf("launch bootstrap binding drift for %s: accepted=%q actual=%q", member.Role, got, expectedBinding)
		}
		prompt, err := preparedBootstrap(project, profile, session, binding, tm, member, context)
		if err != nil {
			return err
		}
		actualDigest := digestRunArtifactBytes([]byte(prompt))
		if got := manifest.BootstrapDigests[member.Role]; got != actualDigest {
			return fmt.Errorf("launch bootstrap digest drift for %s: accepted=%q actual=%q", member.Role, got, actualDigest)
		}
	}
	return nil
}

func calculateRunReadiness(project, profile, session string) runReadinessResult {
	return calculateRunReadinessWithContext(project, profile, session, acceptedRunContext{})
}

func calculateRunReadinessWithContext(project, profile, session string, context acceptedRunContext) runReadinessResult {
	profile = squadnamespace.NormalizeProfile(profile)
	namespace := profile + "/" + session
	result := runReadinessResult{Namespace: namespace, InitialRoster: []string{}, StagedRoster: []string{}, Rows: []runReadinessRow{}, Actions: []runtimeActionJSON{}}
	manifest, manifestDigest, err := readPreparedRunManifestSnapshot(project, profile, session)
	if err != nil {
		result.Rows = append(result.Rows, runReadinessRow{Artifact: "preparation", Status: "missing", Evidence: err.Error(), Fix: "return to wizard preparation and approve the rendered artifact mutations"})
		return result
	}
	result.LaunchShape = manifest.LaunchShape
	result.InitialRoster = append([]string{}, manifest.InitialRoster...)
	result.StagedRoster = append([]string{}, manifest.StagedRoster...)
	result.Lead = manifest.Lead
	result.GoalSource, result.GoalDigest = manifest.GoalSource, manifest.GoalDigest
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
	} else if manifest.GoalDeliveryState != preparedRunGoalDeliveryPlanned {
		add("goal_binding", "drifted", fmt.Sprintf("accepted delivery state=%q want=%q", manifest.GoalDeliveryState, preparedRunGoalDeliveryPlanned), "return to preparation; prepared goal intent must remain planned and unverified until live delivery")
	} else {
		add("goal_binding", "ready", fmt.Sprintf("planned/unverified source=%s namespace=%s digest=%s", binding.Source, binding.Namespace, binding.Digest), "")
	}
	tm, teamErr := team.ReadProfile(project, profile)
	var skippedMembers []team.Member
	var stagedMembers []team.Member
	var fullTeam team.Team
	if teamErr != nil {
		add("profile", "missing", teamErr.Error(), "approve preparation to create the exact initial profile")
	} else {
		fullTeam = tm
		initial, staged, partitionErr := partitionPreparedRunMembers(tm.Members, session, manifest.StagedRoster)
		if partitionErr != nil {
			add("profile", "drifted", partitionErr.Error(), "restore every accepted initial and staged member definition or approve preparation again")
		}
		tm.Members = initial
		stagedMembers = staged
		_, skippedMembers = filterMembersBySession(fullTeam.Members, session)
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
			add("profile", "drifted", fmt.Sprintf("initial roster mismatch: accepted %d [%s], session-filtered profile %d [%s]", len(manifest.InitialRoster), strings.Join(manifest.InitialRoster, ", "), len(actual), strings.Join(actual, ", ")), "return to preparation; do not silently add or remove members")
		} else if digest, err := digestFile(team.ProfilePath(project, profile)); err != nil || digest != manifest.ArtifactDigests["profile"] {
			add("profile", "drifted", "profile content differs from the accepted preparation snapshot", "review the profile diff and approve preparation again")
		} else {
			add("profile", "ready", fmt.Sprintf("%d session members - %s", len(actual), strings.Join(actual, ", ")), "")
		}
		for _, member := range tm.Members {
			accepted, ok := manifest.Members[member.Role]
			current := acceptedMemberIdentity(tm, member, profile, session)
			if !ok || !reflect.DeepEqual(accepted, current) {
				add("member:"+member.Role, "drifted", fmt.Sprintf("accepted=%+v current=%+v", accepted, current), "return to preparation and accept the exact binary/model/effort/task/tool identity")
			} else {
				add("member:"+member.Role, "ready", fmt.Sprintf("handle=%s binary=%s model=%s effort=%s task=%s tool_policy=%s", current.Handle, current.Binary, current.Model, current.Effort, current.TaskOwnership, current.ToolProfile), "")
			}
		}
		for _, member := range stagedMembers {
			accepted, ok := manifest.StagedMembers[member.Role]
			current := acceptedMemberIdentity(fullTeam, member, profile, session)
			if !ok || !reflect.DeepEqual(accepted, current) {
				add("member:"+member.Role, "drifted", fmt.Sprintf("accepted=%+v current=%+v", accepted, current), "return to preparation and accept the exact staged binary/model/effort/task/tool identity")
			} else {
				add("member:"+member.Role, "ready", fmt.Sprintf("staged handle=%s binary=%s model=%s effort=%s task=%s tool_policy=%s", current.Handle, current.Binary, current.Model, current.Effort, current.TaskOwnership, current.ToolProfile), "")
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
	skippedByRole := map[string]team.Member{}
	if teamErr == nil {
		for _, member := range tm.Members {
			actualInitial[member.Role] = true
		}
		for _, member := range skippedMembers {
			skippedByRole[member.Role] = member
		}
		for _, member := range stagedMembers {
			skippedByRole[member.Role] = member
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
		if member, ok := skippedByRole[roleID]; ok {
			add("staged_role:"+roleID, "ready", fmt.Sprintf("configured as %s/%s and excluded from initial launch; separate durable spawn reservation required", roleID, memberHandle(member)), "")
		} else {
			add("staged_role:"+roleID, "drifted", "accepted staged member definition is missing", "restore the full staged profile member or approve preparation again")
		}
	}
	for _, member := range skippedMembers {
		if !containsRole(manifest.StagedRoster, member.Role) {
			add("staged_role:"+member.Role, "drifted", fmt.Sprintf("profile member is pinned to other session %q but was not explicitly staged", member.Session), "return to preparation and add the other-session member to the accepted staged roster")
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
			expectedBinding, bindingErr := expectedPreparedBootstrapBindingLine(tm, profile, session, member, binding)
			if bindingErr != nil {
				add(artifact, "drifted", bindingErr.Error(), "repair the accepted goal binding and approve preparation again")
				continue
			}
			if manifest.BootstrapBindings[member.Role] != expectedBinding {
				add(artifact, "drifted", fmt.Sprintf("accepted bootstrap goal binding=%q current=%q", manifest.BootstrapBindings[member.Role], expectedBinding), "approve preparation again with the exact binary-specific goal binding")
				continue
			}
			prompt, err := preparedBootstrap(project, profile, session, binding, tm, member, acceptedRunContext{Version: context.Version, Topology: manifest.Topology})
			if err != nil {
				add(artifact, "drifted", err.Error(), "repair the referenced artifact and approve preparation again")
			} else if digestRunArtifactBytes([]byte(prompt)) != manifest.BootstrapDigests[member.Role] {
				add(artifact, "drifted", "generated bootstrap differs from accepted preview", "review the bootstrap diff and approve preparation again")
			} else {
				goalMode := strings.TrimPrefix(expectedBinding, "Goal binding: ")
				add(artifact, "ready", fmt.Sprintf("namespace=%s/%s role=%s lead=%s brief=%s rules=%s role_path=%s goal_mode=%s goal_digest=%s routing=durable-amq gates=operator-contract sha256=%s",
					profile, session, member.Role, tm.Lead,
					briefPathForProfile(project, profile, session), rules.Path(project),
					role.ExistingPath(filepath.Join(squadnamespace.AMQRoot(project, profile, session), "agents", memberHandle(member))),
					goalMode, manifest.GoalDigest, digestRunArtifactBytes([]byte(prompt))), "")
			}
		}
		if evidence, err := validatePreparedRunEnvironment(project, profile, manifest, tm, context); err != nil {
			add("environment", "drifted", err.Error(), "repair the reported version, capability, topology, pointer, or policy drift and approve preparation again")
		} else {
			add("environment", "ready", evidence, "")
		}
		for _, member := range stagedMembers {
			artifact := "bootstrap:" + member.Role
			expectedBinding, bindingErr := expectedPreparedBootstrapBindingLine(fullTeam, profile, session, member, binding)
			if bindingErr != nil || manifest.BootstrapBindings[member.Role] != expectedBinding {
				add(artifact, "drifted", "staged bootstrap binding differs from accepted preparation", "approve preparation again with the exact staged bootstrap binding")
				continue
			}
			prompt, promptErr := preparedBootstrap(project, profile, session, binding, fullTeam, member, acceptedRunContext{Version: context.Version, Topology: manifest.Topology})
			if promptErr != nil || digestRunArtifactBytes([]byte(prompt)) != manifest.BootstrapDigests[member.Role] {
				add(artifact, "drifted", "staged generated bootstrap differs from accepted preparation", "review the staged bootstrap diff and approve preparation again")
			} else {
				add(artifact, "ready", fmt.Sprintf("staged role=%s handle=%s sha256=%s", member.Role, memberHandle(member), manifest.BootstrapDigests[member.Role]), "")
			}
		}
	}
	result.Ready = len(result.Rows) > 0
	for _, row := range result.Rows {
		if row.Status != "ready" {
			result.Ready = false
			break
		}
	}
	if result.Ready && len(manifest.StagedRoster) > 0 {
		actions, actionErr := preparedStagedSpawnActions(project, profile, session, manifest, manifestDigest, fullTeam, stagedMembers)
		if actionErr != nil {
			add("staged_actions", "drifted", actionErr.Error(), "return to preparation for the exact profile/session and regenerate staged-member actions")
			result.Ready = false
		} else {
			result.Actions = actions
			add("staged_actions", "ready", fmt.Sprintf("%d exact lifecycle-bound staged admission or parent launch actions", len(actions)), "")
		}
	}
	return result
}

func preparedStagedSpawnActions(project, profile, session string, manifest preparedRunManifest, manifestDigest string, tm team.Team, stagedMembers []team.Member) ([]runtimeActionJSON, error) {
	if manifest.Project != project || manifest.Profile != profile || manifest.Session != session || manifest.Namespace != profile+"/"+session {
		return nil, fmt.Errorf("cannot generate staged-spawn actions: accepted namespace identity is stale; run preparation again")
	}
	if err := validatePreparedLaunchShape(manifest); err != nil {
		return nil, fmt.Errorf("cannot generate staged-spawn actions from incomplete preparation: %w", err)
	}
	token := preparedRunTokenFromSnapshot(manifest, manifestDigest)
	if !token.complete() || token.LaunchAttempt != "" {
		return nil, fmt.Errorf("cannot generate staged-spawn actions: accepted prepared generation binding is incomplete; run preparation again")
	}
	if len(manifest.StagedRoster) != len(manifest.StagedMembers) || len(stagedMembers) != len(manifest.StagedRoster) {
		return nil, fmt.Errorf("cannot generate staged-spawn actions: accepted staged member set is incomplete; run preparation again")
	}
	byRole := make(map[string]team.Member, len(stagedMembers))
	for _, member := range stagedMembers {
		byRole[member.Role] = member
	}
	actions := make([]runtimeActionJSON, 0, len(manifest.StagedRoster))
	for _, roleID := range manifest.StagedRoster {
		member, ok := byRole[roleID]
		accepted, acceptedOK := manifest.StagedMembers[roleID]
		if !ok || !acceptedOK || !reflect.DeepEqual(accepted, acceptedMemberIdentity(tm, member, profile, session)) {
			return nil, fmt.Errorf("cannot generate staged-spawn action for %s: accepted role/binary/model/effort/tool identity is incomplete or drifted; run preparation again", roleID)
		}
		pointerPath := preparedRunStagedClaimActivePath(project, profile, session, token.Generation, roleID)
		pointer, pointerErr := readPreparedRunStagedClaimPointer(pointerPath)
		if os.IsNotExist(pointerErr) {
			command := shellCommand(generatedSquadCommand(), "team", "member", "admit", roleID,
				"--actor-mode", accepted.ActorMode, "--project", project, "--profile", profile, "--session", session,
				"--reason", "prepared readiness staged admission", "--json")
			actions = append(actions, runtimeActionJSON{
				Kind: "staged_admit", Label: "admit accepted staged member " + roleID, Scope: "agent",
				NamespaceID: manifest.Namespace, Command: command, Mutates: true, NeedsConfirmation: true, Available: true,
				Reason: "an exact verified authorizer must admit the staged member before terminal launch",
			})
			continue
		}
		if pointerErr != nil {
			return nil, fmt.Errorf("cannot resolve staged action for %s: read authoritative claim lifecycle: %w", roleID, pointerErr)
		}
		claim, claimErr := preparedRunStagedClaimForPointer(project, profile, session, token, roleID, pointer)
		if claimErr != nil {
			return nil, fmt.Errorf("cannot resolve staged action for %s: %w", roleID, claimErr)
		}
		if lifecycleErr := validatePreparedRunStagedPointerLifecycle(project, profile, session, token, claim, pointer); lifecycleErr != nil {
			return nil, fmt.Errorf("cannot resolve staged action for %s: %w", roleID, lifecycleErr)
		}
		switch pointer.LifecycleState {
		case stagedClaimStateAdmitted:
			if pointer.Consumption != nil {
				return nil, preparedRunIdentityMismatchf("admitted staged claim %s carries consumption evidence", claim.ClaimID)
			}
			command := shellCommand(generatedSquadCommand(), "team", "member", "launch", roleID,
				"--claim", claim.ClaimID, "--project", project, "--profile", profile, "--session", session,
				"--target", "new-window", "--json")
			actions = append(actions, runtimeActionJSON{
				Kind: "staged_spawn", Label: "launch admitted staged member " + roleID, Scope: "agent",
				NamespaceID: manifest.Namespace, Command: command, Mutates: true, NeedsConfirmation: true, Available: true,
			})
		case stagedClaimStateConsumed:
			command := shellCommand(generatedSquadCommand(), "team", "member", "replace", roleID,
				"--claim", claim.ClaimID, "--actor-mode", claim.Effective.ActorMode,
				"--project", project, "--profile", profile, "--session", session,
				"--reason", "prepared readiness staged replacement", "--json")
			absentErr := preparedRunStagedTargetAbsent(project, profile, session, claim.Handle)
			available := absentErr == nil
			reason := "the consumed staged claim normally identifies a live reviewer; replacement requires exact target-absence proof"
			if absentErr != nil {
				reason += ": " + absentErr.Error()
			}
			actions = append(actions, runtimeActionJSON{
				Kind: "staged_replace", Label: "replace consumed staged claim for " + roleID + " after verified stop", Scope: "agent",
				NamespaceID: manifest.Namespace, Command: command, Mutates: true, NeedsConfirmation: true, Available: available, Reason: reason,
			})
		case stagedClaimStateAbandoned:
			command := shellCommand(generatedSquadCommand(), "team", "member", "replace", roleID,
				"--claim", claim.ClaimID, "--actor-mode", claim.Effective.ActorMode,
				"--project", project, "--profile", profile, "--session", session,
				"--reason", "prepared readiness replacement of abandoned claim", "--json")
			absentErr := preparedRunStagedTargetAbsent(project, profile, session, claim.Handle)
			available := absentErr == nil
			reason := "the prior exact claim was abandoned and cannot be launched; replacement requires exact target-absence proof"
			if absentErr != nil {
				reason += ": " + absentErr.Error()
			}
			actions = append(actions, runtimeActionJSON{
				Kind: "staged_replace", Label: "replace abandoned staged claim for " + roleID, Scope: "agent",
				NamespaceID: manifest.Namespace, Command: command, Mutates: true, NeedsConfirmation: true, Available: available, Reason: reason,
			})
		default:
			return nil, preparedRunIdentityMismatchf("staged claim %s has unsupported lifecycle %s", claim.ClaimID, pointer.LifecycleState)
		}
	}
	return runtimeaction.ApplyCanonical(actions), nil
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
	for _, action := range result.Actions {
		fmt.Printf("  action:%-17s ready    %s\n", action.Kind, action.Label)
		fmt.Printf("  %-24s command  %s\n", "", action.Command)
	}
}

func displayRoleList(roles []string) string {
	if len(roles) == 0 {
		return "none"
	}
	return strings.Join(roles, ", ")
}

var buildPreparedRunManifestForPreparation = buildPreparedRunManifest

func prepareRunArtifacts(project, profile, session, shape, stagedRaw, goal, goalSource, goalDigest, seed string, context acceptedRunContext) (result runReadinessResult, err error) {
	profile = squadnamespace.NormalizeProfile(profile)
	if err := revalidateRunPreparationPointerPlans(context.PointerPlans); err != nil {
		return runReadinessResult{}, fmt.Errorf("revalidate accepted pointer plan before preparation writes: %w", err)
	}
	if shape != runwizard.LaunchShapeWorkingTeamTogether && shape != runwizard.LaunchShapeLeadOnlyStaged {
		return runReadinessResult{}, fmt.Errorf("preparation requires explicit --launch-shape working-team-together or lead-only-staged")
	}
	binding, err := resolveAcceptedGoalBinding(project, profile, session, goal, goalSource, goalDigest)
	if err != nil {
		return runReadinessResult{}, err
	}
	tm, err := team.ReadProfile(project, profile)
	if err != nil {
		return runReadinessResult{}, err
	}
	active, _ := filterMembersBySession(tm.Members, session)
	tm.Members = active
	if len(tm.Members) == 0 {
		return runReadinessResult{}, fmt.Errorf("preparation has no members for session %q", session)
	}
	if _, err := validatePreparedBootstrapSemantics(tm, profile, session, binding); err != nil {
		return runReadinessResult{}, err
	}
	briefPath := briefPathForProfile(project, profile, session)
	mutationPaths := []string{briefPath, rules.Path(project), preparedRunPath(project, profile, session)}
	for _, plan := range context.PointerPlans {
		mutationPaths = append(mutationPaths, plan.Target)
	}
	snapshots, err := snapshotRunPreparationFiles(mutationPaths...)
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
				err = fmt.Errorf("%w; preparation rollback failed: %v", err, rollbackErr)
			}
		}
	}()
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
	manifest, err := buildPreparedRunManifestForPreparation(project, profile, session, shape, stagedRaw, binding, context)
	if err != nil {
		return runReadinessResult{}, err
	}
	if err := writePreparedRunManifest(preparedRunPath(project, profile, session), manifest); err != nil {
		return runReadinessResult{}, err
	}
	result = calculateRunReadinessWithContext(project, profile, session, context)
	if !result.Ready {
		return result, fmt.Errorf("artifact readiness failed after preparation")
	}
	committed = true
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
