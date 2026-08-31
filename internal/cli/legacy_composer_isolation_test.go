package cli

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// legacyArgvComposerSymbols is agent_defaults.go's legacy Claude preauth
// argv-splicing surface (gh#763): claudeInScopePreauthAllowlist plus the
// "friends" that shape it into launch argv. claudeWorkerPreauthEligible is
// deliberately excluded -- it is a shared eligibility check both the legacy
// backend and launchapiTeamLaunchBackend call (team_launch_launchapi.go
// documents this explicitly), not argv-composition, so its presence on the
// new path is intentional and not a leak.
var legacyArgvComposerSymbols = []string{
	"claudeInScopePreauthAllowlist",
	"claudeLauncherPreauthActions",
	"applyClaudeWorkerPreauth",
	"applyClaudeWorkerPreauthActions",
	"claudePreauthChildArgs",
	"collectClaudeAllowedTools",
	"replaceClaudeAllowedTools",
	"stripRecordedLauncherPreauth",
	"configuredClaudePermissionAllowlist",
	"childArgsAllowedTools",
}

// TestLegacyComposerReferencedOnlyByLegacyBackend is gh#763's named
// acceptance test. Its literal wording ("only --launch-via legacy
// references it") does not hold as stated: gh#755 only flipped
// executeTeamLaunch's backend selection (resolveTeamLaunchBackend), and two
// other call sites reach this same composer with no --launch-via gate at
// all, because they never go through resolveTeamLaunchBackend in the first
// place --
//   - internal/cli/simple_start.go (the `amq-squad start` single-session
//     planner) resolves its backend via a bare teamLaunchBackends[opts.Terminal]
//     lookup and always calls buildTeamLaunchPanes, which composes legacy
//     pane commands (composer included) unconditionally.
//   - internal/cli/launch.go (single-agent `agent up`/launch) has no
//     --launch-via concept at all; LaunchVia is a team-launch-only flag.
//
// Migrating simple_start and single-agent launch onto internal/launchintent
// or the launchapi path is out of gh#763's scope (and not in the v2.31.0
// milestone as read); until it happens, v2.32.0's planned deletion of this
// composer is not a plain file removal. That resizing is tracked in a
// release-time follow-up (see t14's deprecation table), not fixed here.
//
// What this test proves instead, honestly: the composer is never referenced
// from the launchapi-path files -- team_launch_launchapi.go and every file
// in internal/launchintent. That is the provable invariant gh#763 actually
// needs: the new path must never silently reabsorb the old argv-splicing
// logic, regardless of what the rest of the legacy surface still does.
// fileIdentifiers parses a Go source file and returns the set of all
// identifier names appearing in its code (declarations, calls, expressions).
// Comments are not part of the AST, so a symbol named only in a doc comment
// (e.g. launchintent.go's own doc comment cross-referencing
// claudeInScopePreauthAllowlist by name to explain where its literal is
// mirrored from) is correctly excluded -- this test cares about actual code
// references, not prose mentioning a name.
func fileIdentifiers(t *testing.T, path string) map[string]bool {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	idents := make(map[string]bool)
	ast.Inspect(f, func(n ast.Node) bool {
		if ident, ok := n.(*ast.Ident); ok {
			idents[ident.Name] = true
		}
		return true
	})
	return idents
}

func TestLegacyComposerReferencedOnlyByLegacyBackend(t *testing.T) {
	checkFileForbidsSymbols := func(readPath, label string) {
		idents := fileIdentifiers(t, readPath)
		for _, symbol := range legacyArgvComposerSymbols {
			if idents[symbol] {
				t.Fatalf("%s references legacy argv composer symbol %q in code; the launchapi path must never reabsorb legacy argv-splicing logic", label, symbol)
			}
		}
	}

	checkFileForbidsSymbols("team_launch_launchapi.go", "internal/cli/team_launch_launchapi.go")

	launchintentEntries, err := os.ReadDir("../launchintent")
	if err != nil {
		t.Fatalf("read internal/launchintent: %v", err)
	}
	sawLaunchintentFile := false
	for _, entry := range launchintentEntries {
		name := entry.Name()
		if entry.IsDir() || filepath.Ext(name) != ".go" || strings.HasSuffix(name, "_test.go") {
			continue
		}
		sawLaunchintentFile = true
		checkFileForbidsSymbols(filepath.Join("../launchintent", name), "internal/launchintent/"+name)
	}
	if !sawLaunchintentFile {
		t.Fatal("found no non-test .go files under internal/launchintent; test fixture is stale")
	}

	// Guard against a vacuous test: every symbol above must still be
	// referenced somewhere in internal/cli (its legacy callers), so a typo
	// or a symbol rename can never silently make this pass by checking
	// nothing.
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	legacyIdents := make(map[string]bool)
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || filepath.Ext(name) != ".go" || strings.HasSuffix(name, "_test.go") {
			continue
		}
		for ident := range fileIdentifiers(t, name) {
			legacyIdents[ident] = true
		}
	}
	for _, symbol := range legacyArgvComposerSymbols {
		if !legacyIdents[symbol] {
			t.Fatalf("legacy argv composer symbol %q is not referenced anywhere in internal/cli; test fixture is stale", symbol)
		}
	}
}
