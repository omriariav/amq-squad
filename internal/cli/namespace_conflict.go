package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

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
	if conflict := namespaceConflictForProfileSession(projectDir, profile, session); conflict != nil {
		return namespaceConflictError(operation, conflict)
	}
	conflict, err := defaultProfileShadowConflict(projectDir, profile, session, explicitProfile)
	if err != nil {
		return fmt.Errorf("%s refused: scan named profiles for session %q: %w", operation, session, err)
	}
	return namespaceConflictError(operation, conflict)
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
		parts := strings.Split(rel, "/")
		if len(parts) <= 2 && parts[0] == "agents" {
			return false
		}
		return true
	}
	if strings.HasSuffix(rel, "/presence.json") {
		return false
	}
	return true
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
