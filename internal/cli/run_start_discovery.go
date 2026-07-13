package cli

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/omriariav/amq-squad/v2/internal/catalog"
	"github.com/omriariav/amq-squad/v2/internal/launch"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/team"
	runwizard "github.com/omriariav/amq-squad/v2/internal/wizard"
)

var (
	versionBranchPattern           = regexp.MustCompile(`(?i)(?:^|[/_-])v([0-9]+)[._-]([0-9]+)[._-]([0-9]+)(?:$|[/_-])`)
	issueBranchPattern             = regexp.MustCompile(`(?i)^(?:feat|fix|chore|docs|refactor|test)/([0-9]+)(?:[-_/].*)?$`)
	runStartWizardPlanMemberResume = planMemberResume
)

func inspectRunStartWizardProject(project string) (runwizard.ProjectContext, error) {
	root, err := nearestGitRoot(project)
	if err != nil {
		return runwizard.ProjectContext{}, err
	}
	ctx := runwizard.ProjectContext{
		Project:           root,
		PreferredBinaries: map[string]string{},
	}
	ctx.Catalog = loadAgentCatalogAndWarn(root)
	for _, entry := range catalog.All() {
		ctx.PreferredBinaries[entry.ID] = entry.PreferredBinary
	}
	gitDir := resolveGitDir(root)
	ctx.OriginSlug = gitOriginSlug(readGitOriginURL(gitDir))
	ctx.Branch = readGitBranch(gitDir)
	ctx.SessionSuggestion = suggestRunStartSession(ctx.Branch, root)
	ctx.NewProfileSuggestion = suggestedWizardProfile(ctx.SessionSuggestion)
	ctx.Profiles, err = runStartWizardProfiles(root, ctx.SessionSuggestion)
	if err != nil {
		return runwizard.ProjectContext{}, err
	}
	return ctx, nil
}

func nearestGitRoot(start string) (string, error) {
	start = strings.TrimSpace(start)
	if start == "" {
		return "", fmt.Errorf("project directory cannot be empty")
	}
	abs, err := filepath.Abs(start)
	if err != nil {
		return "", fmt.Errorf("resolve project directory: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("project directory does not exist: %s", abs)
	}
	for dir := filepath.Clean(abs); ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return filepath.Clean(abs), nil
		}
	}
}

func resolveGitDir(root string) string {
	dotGit := filepath.Join(root, ".git")
	info, err := os.Stat(dotGit)
	if err != nil || info.IsDir() {
		return dotGit
	}
	body, err := os.ReadFile(dotGit)
	if err != nil {
		return dotGit
	}
	line := strings.TrimSpace(string(body))
	if !strings.HasPrefix(line, "gitdir:") {
		return dotGit
	}
	dir := strings.TrimSpace(strings.TrimPrefix(line, "gitdir:"))
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(root, dir)
	}
	return filepath.Clean(dir)
}

func readGitOriginURL(gitDir string) string {
	config := filepath.Join(gitDir, "config")
	if _, err := os.Stat(config); os.IsNotExist(err) {
		if common, readErr := os.ReadFile(filepath.Join(gitDir, "commondir")); readErr == nil {
			config = filepath.Join(gitDir, strings.TrimSpace(string(common)), "config")
		}
	}
	f, err := os.Open(config)
	if err != nil {
		return ""
	}
	defer f.Close()
	inOrigin := false
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "[") {
			inOrigin = strings.EqualFold(line, `[remote "origin"]`)
			continue
		}
		if !inOrigin {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if ok && strings.EqualFold(strings.TrimSpace(key), "url") {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func gitOriginSlug(raw string) string {
	raw = strings.TrimSpace(strings.TrimSuffix(raw, ".git"))
	if raw == "" {
		return ""
	}
	if strings.Contains(raw, "://") {
		parts := strings.SplitN(raw, "://", 2)
		path := parts[1]
		if slash := strings.Index(path, "/"); slash >= 0 {
			return strings.Trim(path[slash+1:], "/")
		}
	}
	if at := strings.Index(raw, "@"); at >= 0 {
		if colon := strings.Index(raw[at:], ":"); colon >= 0 {
			return strings.Trim(raw[at+colon+1:], "/")
		}
	}
	return strings.Trim(raw, "/")
}

func readGitBranch(gitDir string) string {
	body, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return ""
	}
	head := strings.TrimSpace(string(body))
	if !strings.HasPrefix(head, "ref:") {
		return ""
	}
	return strings.TrimPrefix(strings.TrimSpace(strings.TrimPrefix(head, "ref:")), "refs/heads/")
}

func suggestRunStartSession(branch, project string) string {
	if match := versionBranchPattern.FindStringSubmatch(branch); len(match) == 4 {
		return "v" + match[1] + "-" + match[2] + "-" + match[3]
	}
	if match := issueBranchPattern.FindStringSubmatch(branch); len(match) == 2 {
		return "issue-" + match[1]
	}
	return defaultWorkstreamName(project)
}

func suggestedWizardProfile(session string) string {
	suggestion := sanitizeWorkstreamName("squad-" + strings.TrimSpace(session))
	if suggestion == "" {
		return "squad-project"
	}
	return suggestion
}

func runStartWizardProfiles(project, sessionSuggestion string) ([]runwizard.ProfileSummary, error) {
	var names []string
	if team.Exists(project) {
		names = append(names, team.DefaultProfile)
	}
	named, err := team.ListProfiles(project)
	if err != nil {
		return nil, fmt.Errorf("list team profiles: %w", err)
	}
	names = append(names, named...)
	history, err := launch.ScanEntries(project)
	if err != nil {
		return nil, fmt.Errorf("scan launch history: %w", err)
	}
	profiles := make([]runwizard.ProfileSummary, 0, len(names))
	for _, name := range names {
		t, err := team.ReadProfile(project, name)
		if err != nil {
			return nil, fmt.Errorf("read team profile %q: %w", name, err)
		}
		summary := runwizard.ProfileSummary{
			Name:                  name,
			MemberCount:           len(t.Members),
			PinnedSession:         runStartPinnedSession(t),
			Lead:                  t.Lead,
			LeadMode:              team.EffectiveLeadMode(t),
			OperatorMode:          team.EffectiveOperator(t).InteractionMode,
			OperatorNotifications: team.EffectiveOperatorNotifications(t.Operator).Enabled,
		}
		if view := team.EffectiveSelfOperator(t, runStartPinnedSession(t)); t.Operator != nil && t.Operator.InteractionMode == team.OperatorInteractionSelfOperator {
			summary.SelfOperatorLead = view.LeadRole
			summary.SelfOperatorAllow = strings.Join(view.AllowedGateKinds, ",")
			summary.SelfOperatorRevision = view.PolicyRevision
			summary.SelfOperatorPaused = view.Paused
		}
		for _, member := range t.Members {
			summary.Members = append(summary.Members, runwizard.MemberSummary{
				Role:   member.Role,
				Binary: member.Binary,
				Model:  member.Model,
				Effort: memberEffort(member),
			})
		}
		summary.Sessions = runStartWizardProfileSessions(project, name, t, history, sessionSuggestion)
		profiles = append(profiles, summary)
	}
	return profiles, nil
}

func runStartWizardProfileSessions(project, profile string, t team.Team, history []launch.Entry, sessionSuggestion string) []runwizard.SessionSummary {
	pinned := runStartPinnedSession(t)
	sources := map[string]runwizard.SessionSource{}
	historySessions := map[string]struct{}{}
	if pinned != "" {
		source := runwizard.SessionSourceProfile
		if inferredSharedMemberSession(t) != "" {
			source = runwizard.SessionSourceMemberPin
		}
		sources[pinned] = source
	}
	for _, entry := range history {
		rec := entry.Record
		if !squadnamespace.ProfilesEqual(profile, rec.TeamProfile) || strings.TrimSpace(rec.Session) == "" {
			continue
		}
		if home := strings.TrimSpace(rec.TeamHome); home != "" && filepath.Clean(home) != filepath.Clean(project) {
			continue
		}
		if _, exists := sources[rec.Session]; !exists {
			sources[rec.Session] = runwizard.SessionSourceLaunchHistory
		}
		historySessions[rec.Session] = struct{}{}
	}
	if len(sources) == 0 {
		if suggested := strings.TrimSpace(sessionSuggestion); suggested != "" {
			sources[suggested] = runwizard.SessionSourceSuggestedFirst
		} else {
			return nil
		}
	}
	names := make([]string, 0, len(sources))
	for name := range sources {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		if names[i] == pinned {
			return true
		}
		if names[j] == pinned {
			return false
		}
		return names[i] < names[j]
	})
	knownHistory := make([]string, 0, len(historySessions))
	for name := range historySessions {
		knownHistory = append(knownHistory, name)
	}
	sort.Strings(knownHistory)
	out := make([]runwizard.SessionSummary, 0, len(names))
	for _, session := range names {
		out = append(out, discoverRunStartWizardSession(t, profile, session, sources[session], knownHistory, history))
	}
	return out
}

func discoverRunStartWizardSession(t team.Team, profile, session string, source runwizard.SessionSource, knownSessions []string, history []launch.Entry) runwizard.SessionSummary {
	active, skipped := filterMembersBySession(t.Members, session)
	planningTeam := t
	planningTeam.Members = active
	plans := make([]resumePlan, 0, len(active))
	recordCount := 0
	for _, member := range orderedTeamMembers(active) {
		plan, err := runStartWizardPlanMemberResume(memberPlanInput{
			Member: member, Team: planningTeam, Workstream: session, Mode: resumeModeDefault,
			SquadBin: teamSquadBin(), BinaryArgs: t.BinaryArgs, Trust: string(trustModeSandboxed), Profile: profile, Probe: defaultDuplicateLaunchProbe,
		})
		if err != nil {
			plans = append(plans, resumePlan{Role: member.Role, Action: resumeBlocked, Note: err.Error()})
			continue
		}
		if plan.HasRestoreRecord {
			recordCount++
		}
		plans = append(plans, plan)
	}
	classificationMemberCount := len(active)
	if source == runwizard.SessionSourceSuggestedFirst {
		for _, member := range orderedTeamMembers(skipped) {
			plans = append(plans, resumePlan{Role: member.Role, Action: resumeBlocked, Note: fmt.Sprintf("member is pinned to session %q and cannot join suggested first session %q", member.Session, session)})
		}
		classificationMemberCount += len(skipped)
	}
	conflict := namespaceConflictForProfileSession(t.Project, profile, session)
	if conflict != nil {
		plans = blockResumePlansForNamespaceConflict(plans, conflict)
	}
	classification := classifyRunStartWizardExistingProfile(classificationMemberCount, recordCount, plans, conflict != nil)
	if len(active) == 0 && len(t.Members) > 0 {
		classification = runwizard.RunClassification{State: runwizard.RunStateBlocked, Detail: "no current profile members belong to this session"}
	}
	summary := runwizard.SessionSummary{Name: session, Source: source, Classification: classification, RecordCount: recordCount}
	fingerprint := runwizard.DiscoveryFingerprintInput{Profile: profile, Lead: t.Lead, LeadMode: team.EffectiveLeadMode(t), Session: session, SessionSource: string(source), MatchingHistorySessions: append([]string(nil), knownSessions...), RecordCount: recordCount}
	operator := team.EffectiveOperator(t)
	self := team.EffectiveSelfOperator(t, session)
	notifications := team.EffectiveOperatorNotifications(t.Operator)
	fingerprint.Operator = runwizard.DiscoveryOperator{Enabled: operator.Enabled, InteractionMode: operator.InteractionMode, Handle: operator.Handle, SelfLead: self.LeadRole, SelfAllow: append([]string(nil), self.AllowedGateKinds...), SelfRevision: self.PolicyRevision, SelfPaused: self.Paused, Notifications: runwizard.DiscoveryNotificationPolicy{Enabled: notifications.Enabled, DeliverySemantics: notifications.DeliverySemantics, Events: append([]string(nil), notifications.Events...)}}
	for _, sink := range notifications.Sinks {
		fingerprint.Operator.Notifications.Sinks = append(fingerprint.Operator.Notifications.Sinks, runwizard.DiscoveryNotificationSink{ID: sink.ID, Type: sink.Type, Argv: append([]string(nil), sink.Argv...), Timeout: sink.Timeout})
	}
	briefPath := briefPathForProfile(t.Project, profile, session)
	fingerprint.Brief = runStartWizardBriefDiscovery(briefPath)
	summary.BriefPath = fingerprint.Brief.Path
	summary.BriefGoal = fingerprint.Brief.Goal
	summary.BriefSeed = fingerprint.Brief.Source
	ns := squadnamespace.Resolve(t.Project, profile, session)
	_, stateErr := os.Stat(ns.AMQRoot)
	fingerprint.NamespaceFacts = append(fingerprint.NamespaceFacts, runwizard.DiscoveryNamespaceFact{Profile: profile, Session: session, AMQRoot: ns.AMQRoot, DurableState: stateErr == nil, ProfilePinsSession: runStartPinnedSession(t) == session})
	if conflict != nil {
		if payload, err := json.Marshal(conflict); err == nil {
			fingerprint.NamespaceConflicts = append(fingerprint.NamespaceConflicts, string(payload))
		}
		for _, candidate := range conflict.Conflicts {
			fingerprint.NamespaceFacts = append(fingerprint.NamespaceFacts, runwizard.DiscoveryNamespaceFact{Profile: candidate.Profile, Session: session, AMQRoot: candidate.AMQRoot, DurableState: true})
		}
	}
	for _, entry := range history {
		rec := entry.Record
		if !squadnamespace.ProfilesEqual(profile, rec.TeamProfile) || rec.Session != session {
			continue
		}
		identity := entry.AgentDir + "|" + rec.Role + "|" + rec.Handle + "|" + rec.StartedAt.UTC().Format("2006-01-02T15:04:05.000000000Z")
		fingerprint.RecordIDs = append(fingerprint.RecordIDs, identity)
	}
	for _, member := range t.Members {
		native := composeBinaryArgs(member.Binary, binaryArgsFor(member.Binary, t.BinaryArgs), member.ExtraArgs())
		fingerprint.Roster = append(fingerprint.Roster, runwizard.DiscoveryMember{Role: member.Role, Handle: member.Handle, Binary: member.Binary, CWD: member.EffectiveCWD(t.Project), Session: member.Session, NativeArgs: native, Model: member.Model, Effort: memberEffort(member)})
	}
	membersByRole := make(map[string]team.Member, len(t.Members))
	for _, member := range t.Members {
		membersByRole[member.Role] = member
	}
	for _, plan := range plans {
		action := runwizard.MemberActionBlocked
		switch plan.Action {
		case resumeLive:
			action, summary.Live = runwizard.MemberActionLive, summary.Live+1
		case resumeRestore:
			action, summary.Restore = runwizard.MemberActionRestore, summary.Restore+1
		case resumeFresh:
			action, summary.Fresh = runwizard.MemberActionFresh, summary.Fresh+1
		default:
			summary.Blocked++
		}
		member := membersByRole[plan.Role]
		row := runwizard.SessionMemberSummary{
			Role: plan.Role, Binary: member.Binary, Model: member.Model, Effort: memberEffort(member), Action: action,
			SavedLaunchIdentity: plan.SavedLaunchIdentity,
		}
		if plan.Saved != nil {
			row.SavedBinary = plan.Saved.Binary
			row.SavedModel = plan.Saved.Model
			row.SavedEffort = plan.Saved.Effort
			row.SavedNativeArgs = append([]string(nil), plan.Saved.NativeArgs...)
		}
		if plan.Tmux != nil {
			row.SavedTarget = plan.Tmux.Target
		}
		summary.Members = append(summary.Members, row)
		fingerprint.MemberPlans = append(fingerprint.MemberPlans, runStartWizardDiscoveryMemberPlan(plan, action))
	}
	summary.GoalPlan = buildResumeGoalPlan(planningTeam, profile, session, plans, false, false)
	fingerprint.GoalPlan = summary.GoalPlan
	if len(active) == 0 {
		summary.Blocked = len(t.Members)
	}
	summary.Fingerprint = runwizard.DiscoveryFingerprint(fingerprint)
	return summary
}

func runStartWizardDiscoveryMemberPlan(plan resumePlan, action runwizard.MemberAction) runwizard.DiscoveryMemberPlan {
	evidence := runwizard.DiscoveryMemberPlan{Role: plan.Role, Action: action, SavedLaunchIdentity: plan.SavedLaunchIdentity, Blocker: plan.Note}
	if plan.Tmux != nil {
		evidence.SavedTarget = plan.Tmux.Target
	}
	if plan.Liveness != nil {
		evidence.LivenessStatus = string(plan.Liveness.Status)
		if payload, err := json.Marshal(plan.Liveness); err == nil {
			evidence.LivenessSignals = []string{string(payload)}
		}
	}
	return evidence
}

func runStartWizardBriefDiscovery(path string) runwizard.DiscoveryBrief {
	brief := runwizard.DiscoveryBrief{Path: path}
	body, err := os.ReadFile(path)
	if err != nil {
		return brief
	}
	digest := sha256.Sum256(body)
	brief.ContentDigest = hex.EncodeToString(digest[:])
	brief.Goal = runStartWizardBriefGoal(string(body))
	lines := strings.Split(string(body), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return brief
	}
	var provenance []string
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "---" {
			break
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if key == "source" {
			brief.Source = value
		}
		if key == "source" || key == "generated_at" || key == "generator" {
			provenance = append(provenance, key+":"+value)
		}
	}
	brief.Provenance = strings.Join(provenance, "|")
	return brief
}

func runStartWizardBriefGoal(body string) string {
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	inGoal := false
	var goal []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			heading := strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
			if inGoal {
				break
			}
			if strings.EqualFold(heading, "goal") {
				inGoal = true
			}
			continue
		}
		if inGoal {
			goal = append(goal, line)
		}
	}
	return strings.TrimSpace(strings.Join(goal, "\n"))
}

func runStartPinnedSession(t team.Team) string {
	if inferred := inferredSharedMemberSession(t); inferred != "" {
		return inferred
	}
	return strings.TrimSpace(t.Workstream)
}

// classifyRunStartWizardExistingProfile adapts the shared resume planner's
// internal action vocabulary to the UI package's backend-neutral discovery
// contract. The wizard never reimplements liveness or restore selection.
func classifyRunStartWizardExistingProfile(memberCount, recordCount int, plans []resumePlan, namespaceAmbiguous bool) runwizard.RunClassification {
	actions := make([]runwizard.MemberAction, 0, len(plans))
	for _, plan := range plans {
		switch plan.Action {
		case resumeLive:
			actions = append(actions, runwizard.MemberActionLive)
		case resumeRestore:
			actions = append(actions, runwizard.MemberActionRestore)
		case resumeFresh:
			actions = append(actions, runwizard.MemberActionFresh)
		default:
			actions = append(actions, runwizard.MemberActionBlocked)
		}
	}
	return runwizard.ClassifyExistingRun(memberCount, recordCount, actions, namespaceAmbiguous)
}
