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
	Kind               string   `json:"kind"`
	Profile            string   `json:"profile"`
	Session            string   `json:"session"`
	NamespaceID        string   `json:"namespace_id"`
	RequestedAMQRoot   string   `json:"requested_amq_root"`
	ConflictingAMQRoot string   `json:"conflicting_amq_root"`
	Detail             string   `json:"detail"`
	RecoveryCommands   []string `json:"recovery_commands,omitempty"`
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
		},
	}
}

func namespaceConflictError(operation string, conflict *namespaceConflictData) error {
	if conflict == nil {
		return nil
	}
	return fmt.Errorf("%s refused: %s", operation, conflict.Detail)
}

func ensureNoNamespaceConflict(operation, projectDir, profile, session string) error {
	return namespaceConflictError(operation, namespaceConflictForProfileSession(projectDir, profile, session))
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
		if strings.HasSuffix(path, ".lock") || strings.HasSuffix(path, ".tmp") {
			return nil
		}
		found = true
		return filepath.SkipAll
	})
	return found
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
