package cli

import (
	"path/filepath"
	"testing"

	squadnamespace "github.com/omriariav/amq-squad/v3/internal/namespace"
	"github.com/omriariav/amq-squad/v3/internal/team"
)

const (
	exactStopProfile = "plane-cli"
	exactStopSession = "plane-cli"
)

func TestAllowsExactNamedProfileDownMatrix(t *testing.T) {
	project := t.TempDir()
	root := squadnamespace.AMQRoot(project, exactStopProfile, exactStopSession)
	conflict := &namespaceConflictData{
		Kind:             "legacy_session_root",
		Profile:          exactStopProfile,
		Session:          exactStopSession,
		RequestedAMQRoot: root,
	}
	valid := exactDownNamespaceScope{
		Verb: "down", ProjectDir: project, Profile: exactStopProfile, Session: exactStopSession,
		All: true, ExplicitProject: true, ExplicitProfile: true, ExplicitSession: true,
	}
	if !allowsExactNamedProfileDown(conflict, valid) {
		t.Fatal("fully explicit named legacy-root stop should be allowed")
	}

	tests := map[string]func(*namespaceConflictData, *exactDownNamespaceScope){
		"nil conflict":              func(c *namespaceConflictData, _ *exactDownNamespaceScope) { *c = namespaceConflictData{} },
		"future conflict kind":      func(c *namespaceConflictData, _ *exactDownNamespaceScope) { c.Kind = "future_kind" },
		"creation collision":        func(c *namespaceConflictData, _ *exactDownNamespaceScope) { c.Kind = "profile_session_root_collision" },
		"conflict profile mismatch": func(c *namespaceConflictData, _ *exactDownNamespaceScope) { c.Profile = "other" },
		"conflict session mismatch": func(c *namespaceConflictData, _ *exactDownNamespaceScope) { c.Session = "other" },
		"conflict root mismatch": func(c *namespaceConflictData, _ *exactDownNamespaceScope) {
			c.RequestedAMQRoot = filepath.Join(project, "other")
		},
		"different verb":     func(_ *namespaceConflictData, s *exactDownNamespaceScope) { s.Verb = "resume" },
		"project inferred":   func(_ *namespaceConflictData, s *exactDownNamespaceScope) { s.ExplicitProject = false },
		"profile inferred":   func(_ *namespaceConflictData, s *exactDownNamespaceScope) { s.ExplicitProfile = false },
		"session inferred":   func(_ *namespaceConflictData, s *exactDownNamespaceScope) { s.ExplicitSession = false },
		"project empty":      func(_ *namespaceConflictData, s *exactDownNamespaceScope) { s.ProjectDir = "" },
		"profile default":    func(_ *namespaceConflictData, s *exactDownNamespaceScope) { s.Profile = team.DefaultProfile },
		"profile empty":      func(_ *namespaceConflictData, s *exactDownNamespaceScope) { s.Profile = "" },
		"session empty":      func(_ *namespaceConflictData, s *exactDownNamespaceScope) { s.Session = "" },
		"selector missing":   func(_ *namespaceConflictData, s *exactDownNamespaceScope) { s.All = false },
		"selectors both set": func(_ *namespaceConflictData, s *exactDownNamespaceScope) { s.Role = "cto" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			gotConflict := *conflict
			gotScope := valid
			mutate(&gotConflict, &gotScope)
			var candidate *namespaceConflictData = &gotConflict
			if name == "nil conflict" {
				candidate = nil
			}
			if allowsExactNamedProfileDown(candidate, gotScope) {
				t.Fatal("non-exact stop scope unexpectedly allowed")
			}
		})
	}

	roleScope := valid
	roleScope.All = false
	roleScope.Role = "cto"
	if !allowsExactNamedProfileDown(conflict, roleScope) {
		t.Fatal("role-scoped exact down should be allowed")
	}
}
