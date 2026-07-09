package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

const coldNamespaceMigrationIssueURL = "https://github.com/omriariav/amq-squad/issues/359"

type namespaceConflictData struct {
	Kind               string                       `json:"kind"`
	Profile            string                       `json:"profile"`
	Session            string                       `json:"session"`
	NamespaceID        string                       `json:"namespace_id"`
	RequestedAMQRoot   string                       `json:"requested_amq_root"`
	ConflictingAMQRoot string                       `json:"conflicting_amq_root"`
	Detail             string                       `json:"detail"`
	RecoveryCommands   []string                     `json:"recovery_commands,omitempty"`
	Conflicts          []namespaceConflictCandidate `json:"conflicts,omitempty"`
}

type namespaceConflictCandidate struct {
	Profile string   `json:"profile"`
	AMQRoot string   `json:"amq_root"`
	Reasons []string `json:"reasons,omitempty"`
}

type namespaceConflictOverrideOptions struct {
	Allowed bool
	Reason  string
}

type namespaceConflictAuditRecord struct {
	At                   time.Time `json:"at"`
	Operation            string    `json:"operation"`
	ProjectDir           string    `json:"project_dir"`
	Profile              string    `json:"profile"`
	Session              string    `json:"session"`
	NamespaceID          string    `json:"namespace_id"`
	Kind                 string    `json:"kind"`
	RequestedAMQRoot     string    `json:"requested_amq_root"`
	ConflictingAMQRoot   string    `json:"conflicting_amq_root"`
	Actor                string    `json:"actor"`
	ActorEnvSet          bool      `json:"actor_env_set"`
	ActorSource          string    `json:"actor_source"`
	Reason               string    `json:"reason"`
	ColdMigrationBacklog string    `json:"cold_migration_backlog"`
}

func namespaceConflictForProfileSession(projectDir, profile, session string) *namespaceConflictData {
	profile = squadnamespace.NormalizeProfile(profile)
	session = strings.TrimSpace(session)
	if projectDir == "" || session == "" || profile == team.DefaultProfile {
		return nil
	}
	requested := squadnamespace.AMQRoot(projectDir, profile, session)
	legacy := squadnamespace.AMQRoot(projectDir, team.DefaultProfile, session)
	if sameResolvedDir(requested, legacy) || !rootHasDurableState(legacy) {
		return nil
	}
	ns := squadnamespace.Resolve(projectDir, profile, session)
	return &namespaceConflictData{
		Kind:               "legacy_session_root",
		Profile:            profile,
		Session:            session,
		NamespaceID:        ns.ID,
		RequestedAMQRoot:   requested,
		ConflictingAMQRoot: legacy,
		Detail:             fmt.Sprintf("named profile %q resolves to %s, but legacy/default session root %s already has durable state for session %q; refusing mutating resume/attach actions until an operator chooses recovery or migration", profile, requested, legacy, session),
		RecoveryCommands: []string{
			"amq-squad status --project " + shellQuote(projectDir) + " --profile " + shellQuote(profile) + " --session " + shellQuote(session) + " --json",
			"amq-squad status --project " + shellQuote(projectDir) + " --profile default --session " + shellQuote(session) + " --json",
			"amq-squad goal deliver --project " + shellQuote(projectDir) + " --profile " + shellQuote(profile) + " --session " + shellQuote(session) + " --role <role> --goal <goal> --override-namespace-conflict --reason <why>",
			"amq-squad dispatch --project " + shellQuote(projectDir) + " --profile " + shellQuote(profile) + " --session " + shellQuote(session) + " --role <role> --subject <subject> --body <body> --override-namespace-conflict --reason <why>",
			"amq-squad send --project " + shellQuote(projectDir) + " --profile " + shellQuote(profile) + " --session " + shellQuote(session) + " --role <role> --body <prompt> --override-namespace-conflict --reason <why>",
			"stopped-run namespace migration backlog: " + coldNamespaceMigrationIssueURL,
			"amq-squad archive " + shellQuote(session) + " --project " + shellQuote(projectDir) + " --profile default",
			"amq-squad rm " + shellQuote(session) + " --project " + shellQuote(projectDir) + " --profile default",
			"amq send --root " + shellQuote(legacy) + " --me <sender> --to <recipient> --thread <thread> --kind todo --subject <subject> --body <body>",
		},
	}
}

func defaultProfileShadowConflict(projectDir, profile, session string, explicitProfile bool) (*namespaceConflictData, error) {
	profile = squadnamespace.NormalizeProfile(profile)
	session = strings.TrimSpace(session)
	if projectDir == "" || session == "" || profile != team.DefaultProfile || explicitProfile {
		return nil, nil
	}
	profiles, err := team.ListProfiles(projectDir)
	if err != nil {
		return nil, err
	}
	conflicts := namedProfileSessionConflicts(projectDir, session, profiles, true)
	if len(conflicts) == 0 {
		return nil, nil
	}
	requested := squadnamespace.AMQRoot(projectDir, team.DefaultProfile, session)
	ns := squadnamespace.Resolve(projectDir, team.DefaultProfile, session)
	profileNames := make([]string, 0, len(conflicts))
	rootDetails := make([]string, 0, len(conflicts))
	for _, c := range conflicts {
		profileNames = append(profileNames, c.Profile)
		rootDetails = append(rootDetails, fmt.Sprintf("%s at %s (%s)", c.Profile, c.AMQRoot, strings.Join(c.Reasons, ", ")))
	}
	detail := fmt.Sprintf("implicit default-profile mutation for session %q would write legacy/default root %s, but named profile %s already owns that session: %s; refusing before write. Rerun with --profile %s to target the named namespace, or pass --profile default to intentionally use the legacy/default root",
		session, requested, pluralProfileList(profileNames), strings.Join(rootDetails, "; "), shellQuote(profileNames[0]))
	if len(profileNames) > 1 {
		detail = fmt.Sprintf("implicit default-profile mutation for session %q would write legacy/default root %s, but multiple named profiles already own that session: %s; refusing before write. Rerun with exactly one --profile <name> from this list (%s), or pass --profile default to intentionally use the legacy/default root",
			session, requested, strings.Join(rootDetails, "; "), strings.Join(profileNames, ", "))
	}
	return &namespaceConflictData{
		Kind:               "default_profile_shadowed",
		Profile:            team.DefaultProfile,
		Session:            session,
		NamespaceID:        ns.ID,
		RequestedAMQRoot:   requested,
		ConflictingAMQRoot: conflicts[0].AMQRoot,
		Detail:             detail,
		RecoveryCommands: []string{
			"amq-squad status --project " + shellQuote(projectDir) + " --profile default --session " + shellQuote(session) + " --json",
			"amq-squad dispatch --project " + shellQuote(projectDir) + " --profile default --session " + shellQuote(session) + " --role <role> --subject <subject> --body <body> --override-namespace-conflict --reason <why>",
			"stopped-run namespace migration backlog: " + coldNamespaceMigrationIssueURL,
		},
		Conflicts: conflicts,
	}, nil
}

func namedProfileSessionConflicts(projectDir, session string, profiles []string, includePins bool) []namespaceConflictCandidate {
	var conflicts []namespaceConflictCandidate
	for _, named := range profiles {
		root := squadnamespace.AMQRoot(projectDir, named, session)
		var reasons []string
		if rootHasDurableState(root) {
			reasons = append(reasons, "durable state")
		}
		if includePins && profilePinsSession(projectDir, named, session) {
			reasons = append(reasons, "profile pins session")
		}
		if len(reasons) > 0 {
			conflicts = append(conflicts, namespaceConflictCandidate{
				Profile: named,
				AMQRoot: root,
				Reasons: reasons,
			})
		}
	}
	return conflicts
}

func profilePinsSession(projectDir, profile, session string) bool {
	t, err := team.ReadProfile(projectDir, profile)
	if err != nil {
		return false
	}
	if strings.TrimSpace(t.Workstream) == session {
		return true
	}
	for _, m := range t.Members {
		if strings.TrimSpace(m.Session) == session {
			return true
		}
	}
	return false
}

func pluralProfileList(profiles []string) string {
	if len(profiles) == 1 {
		return fmt.Sprintf("%q", profiles[0])
	}
	quoted := make([]string, 0, len(profiles))
	for _, p := range profiles {
		quoted = append(quoted, fmt.Sprintf("%q", p))
	}
	return strings.Join(quoted, ", ")
}

func namespaceConflictError(operation string, conflict *namespaceConflictData) error {
	if conflict == nil {
		return nil
	}
	if len(conflict.RecoveryCommands) == 0 {
		return fmt.Errorf("%s refused: %s", operation, conflict.Detail)
	}
	return fmt.Errorf("%s refused: %s\nRecovery commands:\n  %s", operation, conflict.Detail, strings.Join(conflict.RecoveryCommands, "\n  "))
}

func ensureNoNamespaceConflict(operation, projectDir, profile, session string, explicitProfile bool) error {
	return ensureNoNamespaceConflictWithOverride(operation, projectDir, profile, session, explicitProfile, namespaceConflictOverrideOptions{})
}

func ensureNoNamespaceConflictWithOverride(operation, projectDir, profile, session string, explicitProfile bool, override namespaceConflictOverrideOptions) error {
	if conflict := namespaceConflictForProfileSession(projectDir, profile, session); conflict != nil {
		if override.Allowed {
			return writeNamespaceConflictAudit(operation, projectDir, profile, session, conflict, override)
		}
		return namespaceConflictError(operation, conflict)
	}
	conflict, err := defaultProfileShadowConflict(projectDir, profile, session, explicitProfile)
	if err != nil {
		return fmt.Errorf("%s refused: scan named profiles for session %q: %w", operation, session, err)
	}
	if conflict != nil && override.Allowed {
		return writeNamespaceConflictAudit(operation, projectDir, profile, session, conflict, override)
	}
	return namespaceConflictError(operation, conflict)
}

func writeNamespaceConflictAudit(operation, projectDir, profile, session string, conflict *namespaceConflictData, override namespaceConflictOverrideOptions) error {
	if conflict == nil {
		return nil
	}
	reason := strings.TrimSpace(override.Reason)
	if reason == "" {
		return usageErrorf("%s --override-namespace-conflict requires --reason <why>", operation)
	}
	actor, actorSet := os.LookupEnv("AM_ME")
	actorSource := "AM_ME"
	if !actorSet {
		actorSource = "unset"
	}
	dir := filepath.Join(projectDir, team.DirName, "namespace-audit")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("ensure namespace audit dir: %w", err)
	}
	rec := namespaceConflictAuditRecord{
		At:                   time.Now().UTC(),
		Operation:            operation,
		ProjectDir:           projectDir,
		Profile:              squadnamespace.NormalizeProfile(profile),
		Session:              strings.TrimSpace(session),
		NamespaceID:          conflict.NamespaceID,
		Kind:                 conflict.Kind,
		RequestedAMQRoot:     conflict.RequestedAMQRoot,
		ConflictingAMQRoot:   conflict.ConflictingAMQRoot,
		Actor:                strings.TrimSpace(actor),
		ActorEnvSet:          actorSet,
		ActorSource:          actorSource,
		Reason:               reason,
		ColdMigrationBacklog: coldNamespaceMigrationIssueURL,
	}
	b, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("marshal namespace audit: %w", err)
	}
	path := filepath.Join(dir, sanitizeWorkstreamName(session)+".jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open namespace audit: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("write namespace audit: %w", err)
	}
	return nil
}

func rootHasDurableState(root string) bool {
	root = strings.TrimSpace(root)
	if root == "" {
		return false
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return false
	}
	found := false
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || path == root {
			return nil
		}
		if immediateChildNamespaceRoot(root, path, d) {
			return filepath.SkipDir
		}
		if strings.HasSuffix(path, ".lock") || strings.HasSuffix(path, ".tmp") {
			return nil
		}
		if !legacyRootEntryIsDurable(root, path, d) {
			return nil
		}
		found = true
		return filepath.SkipAll
	})
	return found
}

// immediateChildNamespaceRoot reports whether an immediate child is another
// AMQ namespace root, not durable state for the legacy/default session root.
func immediateChildNamespaceRoot(root, path string, d os.DirEntry) bool {
	if !d.IsDir() {
		return false
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	rel = filepath.ToSlash(filepath.Clean(rel))
	if rel == "." || rel == "agents" || strings.Contains(rel, "/") {
		return false
	}
	info, err := os.Stat(filepath.Join(path, "agents"))
	return err == nil && info.IsDir()
}

func legacyRootEntryIsDurable(root, path string, d os.DirEntry) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return true
	}
	rel = filepath.ToSlash(rel)
	if rel == "." || rel == "agents" {
		return false
	}
	if d.IsDir() {
		if legacyMailboxSkeletonDir(rel) {
			return false
		}
		return true
	}
	if strings.HasSuffix(rel, "/presence.json") {
		return false
	}
	return true
}

func legacyMailboxSkeletonDir(rel string) bool {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) == 0 || parts[0] != "agents" {
		return false
	}
	switch len(parts) {
	case 1, 2:
		return true
	case 3:
		switch parts[2] {
		case "inbox", "cur", "new", "tmp":
			return true
		default:
			return false
		}
	case 4:
		if parts[2] != "inbox" {
			return false
		}
		switch parts[3] {
		case "cur", "new", "tmp":
			return true
		default:
			return false
		}
	default:
		return false
	}
}

func disableNamespaceConflictActions(actions []runtimeActionJSON, conflict *namespaceConflictData) []runtimeActionJSON {
	if conflict == nil {
		return actions
	}
	out := append([]runtimeActionJSON(nil), actions...)
	for i := range out {
		if !actionBlockedByNamespaceConflict(out[i]) {
			continue
		}
		out[i].Available = false
		out[i].Reason = conflict.Detail
	}
	return out
}

func actionBlockedByNamespaceConflict(action runtimeActionJSON) bool {
	if action.Mutates {
		return true
	}
	switch action.Kind {
	case "focus", "attach_control":
		return true
	default:
		return false
	}
}
