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
	versionBranchPattern = regexp.MustCompile(`(?i)(?:^|[/_-])v([0-9]+)[._-]([0-9]+)[._-]([0-9]+)(?:$|[/_-])`)
	issueBranchPattern   = regexp.MustCompile(`(?i)^(?:feat|fix|chore|docs|refactor|test)/([0-9]+)(?:[-_/].*)?$`)
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
	for _, entry := range catalog.All() {
		ctx.PreferredBinaries[entry.ID] = entry.PreferredBinary
	}
	gitDir := resolveGitDir(root)
	ctx.OriginSlug = gitOriginSlug(readGitOriginURL(gitDir))
	ctx.Branch = readGitBranch(gitDir)
	ctx.SessionSuggestion = suggestRunStartSession(ctx.Branch, root)
	ctx.NewProfileSuggestion = suggestedWizardProfile(ctx.SessionSuggestion)
	ctx.Profiles, err = runStartWizardProfiles(root)
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

func runStartWizardProfiles(project string) ([]runwizard.ProfileSummary, error) {
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
		summary.Sessions = runStartWizardProfileSessions(project, name, t, history)
		profiles = append(profiles, summary)
	}
	return profiles, nil
}

func runStartWizardProfileSessions(project, profile string, t team.Team, history []launch.Entry) []runwizard.SessionSummary {
	pinned := runStartPinnedSession(t)
	sources := map[string]runwizard.SessionSource{}
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
	}
	if len(sources) == 0 {
		return nil
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
	out := make([]runwizard.SessionSummary, 0, len(names))
	for _, session := range names {
		out = append(out, discoverRunStartWizardSession(t, profile, session, sources[session], names, history))
	}
	return out
}

func discoverRunStartWizardSession(t team.Team, profile, session string, source runwizard.SessionSource, knownSessions []string, history []launch.Entry) runwizard.SessionSummary {
	active, _ := filterMembersBySession(t.Members, session)
	if len(active) == 0 && len(t.Members) > 0 {
		return runwizard.SessionSummary{Name: session, Source: source, Classification: runwizard.RunClassification{State: runwizard.RunStateBlocked, Detail: "no current profile members belong to this session"}, Blocked: len(t.Members)}
	}
	planningTeam := t
	planningTeam.Members = active
	plans := make([]resumePlan, 0, len(active))
	recordCount := 0
	for _, member := range orderedTeamMembers(active) {
		plan, err := planMemberResume(memberPlanInput{
			Member: member, Team: planningTeam, Workstream: session, Mode: resumeModeDefault,
			SquadBin: teamSquadBin(), BinaryArgs: t.BinaryArgs, Trust: string(trustModeSandboxed), Profile: profile, Probe: defaultDuplicateLaunchProbe,
		})
		if err != nil {
			return runwizard.SessionSummary{Name: session, Source: source, Classification: runwizard.RunClassification{State: runwizard.RunStateBlocked, Detail: err.Error()}, Blocked: len(active)}
		}
		if plan.HasRestoreRecord {
			recordCount++
		}
		plans = append(plans, plan)
	}
	conflict := namespaceConflictForProfileSession(t.Project, profile, session)
	if conflict != nil {
		plans = blockResumePlansForNamespaceConflict(plans, conflict)
	}
	classification := classifyRunStartWizardExistingProfile(len(active), recordCount, plans, conflict != nil)
	summary := runwizard.SessionSummary{Name: session, Source: source, Classification: classification}
	fingerprint := runwizard.DiscoveryFingerprintInput{Profile: profile, Lead: t.Lead, LeadMode: team.EffectiveLeadMode(t), Session: session, SessionSource: string(source), MatchingHistorySessions: append([]string(nil), knownSessions...), RecordCount: recordCount}
	operator := team.EffectiveOperator(t)
	self := team.EffectiveSelfOperator(t, session)
	notifications := team.EffectiveOperatorNotifications(t.Operator)
	fingerprint.Operator = runwizard.DiscoveryOperator{Enabled: operator.Enabled, InteractionMode: operator.InteractionMode, Handle: operator.Handle, SelfLead: self.LeadRole, SelfAllow: append([]string(nil), self.AllowedGateKinds...), SelfRevision: self.PolicyRevision, SelfPaused: self.Paused, Notifications: runwizard.DiscoveryNotificationPolicy{Enabled: notifications.Enabled, DeliverySemantics: notifications.DeliverySemantics, Events: append([]string(nil), notifications.Events...)}}
	for _, sink := range notifications.Sinks {
		fingerprint.Operator.Notifications.Sinks = append(fingerprint.Operator.Notifications.Sinks, runwizard.DiscoveryNotificationSink{ID: sink.ID, Type: sink.Type, Argv: append([]string(nil), sink.Argv...), Timeout: sink.Timeout})
	}
	briefPath := briefPathForProfile(t.Project, profile, session)
	fingerprint.Brief.Path = briefPath
	if body, err := os.ReadFile(briefPath); err == nil {
		digest := sha256.Sum256(body)
		fingerprint.Brief.ContentDigest = hex.EncodeToString(digest[:])
	}
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
	savedByRole := map[string]string{}
	for _, entry := range history {
		rec := entry.Record
		if !squadnamespace.ProfilesEqual(profile, rec.TeamProfile) || rec.Session != session {
			continue
		}
		identity := entry.AgentDir + "|" + rec.Role + "|" + rec.Handle + "|" + rec.StartedAt.UTC().Format("2006-01-02T15:04:05.000000000Z")
		fingerprint.RecordIDs = append(fingerprint.RecordIDs, identity)
		savedByRole[rec.Role] = identity
	}
	for _, member := range active {
		native := composeBinaryArgs(member.Binary, binaryArgsFor(member.Binary, t.BinaryArgs), member.ExtraArgs())
		fingerprint.Roster = append(fingerprint.Roster, runwizard.DiscoveryMember{Role: member.Role, Handle: member.Handle, Binary: member.Binary, CWD: member.EffectiveCWD(t.Project), Session: member.Session, NativeArgs: native, Model: member.Model, Effort: memberEffort(member)})
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
		liveness := ""
		var livenessSignals []string
		if plan.Liveness != nil {
			liveness = string(plan.Liveness.Status)
			if payload, err := json.Marshal(plan.Liveness); err == nil {
				livenessSignals = []string{string(payload)}
			}
		}
		fingerprint.MemberPlans = append(fingerprint.MemberPlans, runwizard.DiscoveryMemberPlan{Role: plan.Role, Action: action, LivenessStatus: liveness, LivenessSignals: livenessSignals, SavedLaunchIdentity: savedByRole[plan.Role], Blocker: plan.Note})
	}
	summary.Fingerprint = runwizard.DiscoveryFingerprint(fingerprint)
	return summary
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
