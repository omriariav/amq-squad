package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

// #539: a Claude member with a generated tool profile could not launch in a
// project containing .claude/settings.local.json, because prepare recorded that
// capability source RELATIVE while spawn compared it ABSOLUTE -- the same three
// files, reported as a changed source set.

// seedClaudeToolPolicyProject builds a project with a project-local
// .claude/settings.local.json plus a fake user home, which is the exact
// configuration that triggered #539.
func seedClaudeToolPolicyProject(t *testing.T, role string) (project, home string) {
	t.Helper()
	home = t.TempDir()
	writeFile(t, filepath.Join(home, ".claude", "settings.json"), `{"enabledPlugins":{"user@thing":true}}`)
	writeFile(t, filepath.Join(home, ".claude.json"), `{}`)

	project = seedTeam(t, team.Team{
		Members: []team.Member{{Role: role, Binary: "claude", Handle: role, Session: "s"}},
	})
	// The project-local file whose relative recording caused the bug.
	writeFile(t, filepath.Join(project, ".claude", "settings.local.json"), `{"enabledPlugins":{"project@thing":true}}`)

	withModelLookupRoots(t, filepath.Join(home, "config"), home, nil)
	return project, home
}

// Acceptance criterion 1 + 3: the member launches, and the recorded source set
// is compared representation-independently.
func TestClaudeMemberWithGeneratedPolicyLaunchesWithProjectLocalSettings(t *testing.T) {
	project, _ := seedClaudeToolPolicyProject(t, "backend")
	if _, _, err := captureOutput(t, func() error {
		return runTeamOverlay([]string{"init", "--role", "backend", "--tool-profile", "coding",
			"--allow-tools", "plugin:project@thing,plugin:user@thing"})
	}); err != nil {
		t.Fatalf("overlay init: %v", err)
	}

	m := readTeamMember(t, project, "backend")
	if len(m.ToolPolicySources) == 0 {
		t.Fatal("no capability sources recorded; the fixture did not exercise a generated policy")
	}
	// Every recorded source must be absolute. A relative entry is the #539 bug.
	for _, src := range m.ToolPolicySources {
		if !filepath.IsAbs(src) {
			t.Fatalf("capability source %q recorded relative; sources must be one representation: %v", src, m.ToolPolicySources)
		}
	}
	// And the spawn-time predicate must accept its own record.
	cfg, err := team.Read(project)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateMemberToolPolicyDrift(cfg, m); err != nil {
		t.Fatalf("spawn rejected the policy it just recorded: %v", err)
	}
}

// Acceptance criterion 2: tool_policy_sources is byte-identical after
// `team overlay init --force` and after a re-derivation for the same inputs.
//
// This is the AC that keeps the readiness carve-out safe: readiness skips the
// on-disk materialization comparison for a role whose files it is about to
// write, which is only sound while the writer is deterministic.
func TestToolPolicySourcesAreByteIdenticalAcrossRegeneration(t *testing.T) {
	project, _ := seedClaudeToolPolicyProject(t, "backend")
	args := []string{"init", "--role", "backend", "--tool-profile", "coding",
		"--allow-tools", "plugin:project@thing,plugin:user@thing"}

	if _, _, err := captureOutput(t, func() error { return runTeamOverlay(args) }); err != nil {
		t.Fatalf("first overlay init: %v", err)
	}
	first := readTeamMember(t, project, "backend").ToolPolicySources

	if _, _, err := captureOutput(t, func() error {
		return runTeamOverlay(append(append([]string{}, args...), "--force"))
	}); err != nil {
		t.Fatalf("overlay init --force: %v", err)
	}
	second := readTeamMember(t, project, "backend").ToolPolicySources

	if !reflect.DeepEqual(first, second) {
		t.Fatalf("regeneration changed the recorded source set:\n first=%v\nsecond=%v", first, second)
	}
	// Re-deriving the plan the way `run start --prepare` does must also agree, so
	// a later prepare cannot undo what overlay init recorded. That mutual undoing
	// is the #539 loop, and this half of the assertion is what makes the
	// readiness RecordOnly carve-out safe: it omits the materialization check on
	// the assumption that the writer is deterministic.
	//
	// SAME INPUTS is the condition the AC states, so the re-derivation must use
	// the same project origin `team overlay init` just used. Overlay init resolved
	// its project from the working directory, so read it from there rather than
	// from the test's own spelling of the same directory: passing a DIFFERENT
	// spelling would be different inputs, and that case is covered separately by
	// TestMixedOriginRecordingsDoNotReadAsDrift.
	sameOrigin, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := team.Read(sameOrigin)
	if err != nil {
		t.Fatal(err)
	}
	// A REAL assignment string. buildRunStartToolProfilePlans returns (nil, nil)
	// for an empty one, which would make every assertion below unreachable.
	plans, err := buildRunStartToolProfilePlans(cfg, "", "backend=coding")
	if err != nil {
		t.Fatalf("re-derive plans: %v", err)
	}
	if len(plans) == 0 {
		t.Fatal("no policy plans were derived; the assertion below would be vacuous")
	}
	checked := 0
	for _, plan := range plans {
		if plan.After.Role != "backend" {
			continue
		}
		checked++
		if len(plan.After.ToolPolicySources) == 0 {
			t.Fatal("re-derived plan recorded no capability sources; the comparison would be vacuous")
		}
		if !reflect.DeepEqual(plan.After.ToolPolicySources, second) {
			t.Fatalf("re-derived source set differs from the recorded one:\nrecorded=%v\nderived =%v", second, plan.After.ToolPolicySources)
		}
	}
	if checked != 1 {
		t.Fatalf("expected exactly one derived plan for role backend, checked %d", checked)
	}
}

// Back-compat: a team.json written by <= v2.24.0 still holds the project-relative
// entry. That is not drift, and it must not be drift from ANY working directory
// -- the out-of-repo `--project /path/to/repo` invocation must reach the same
// verdict as the same check run inside the repo.
func TestLegacyRelativeToolPolicySourceIsNotDriftFromAnyWorkingDirectory(t *testing.T) {
	project, _ := seedClaudeToolPolicyProject(t, "backend")
	if _, _, err := captureOutput(t, func() error {
		return runTeamOverlay([]string{"init", "--role", "backend", "--tool-profile", "coding",
			"--allow-tools", "plugin:project@thing,plugin:user@thing"})
	}); err != nil {
		t.Fatalf("overlay init: %v", err)
	}

	// Rewrite the record the way the older release wrote it: the project-local
	// entry relative, which also sorts differently from the absolute form.
	cfg, err := team.Read(project)
	if err != nil {
		t.Fatal(err)
	}
	localRel := filepath.Join(".claude", "settings.local.json")
	// Identify the project-local entry by canonical location, not by raw string
	// prefix: the recorded set legitimately mixes bases (the project entry is
	// derived from the resolved cwd, the home entries from the fixture's HOME),
	// which is precisely why comparison canonicalizes.
	wantLocal := canonicalFilesystemPath(filepath.Join(project, localRel))
	for i, m := range cfg.Members {
		if m.Role != "backend" {
			continue
		}
		legacy := make([]string, 0, len(m.ToolPolicySources))
		rewritten := false
		for _, src := range m.ToolPolicySources {
			if canonicalFilesystemPath(src) == wantLocal {
				legacy = append(legacy, localRel)
				rewritten = true
				continue
			}
			legacy = append(legacy, src)
		}
		if !rewritten {
			t.Fatalf("fixture did not record the project-local source; got %v", m.ToolPolicySources)
		}
		cfg.Members[i].ToolPolicySources = legacy
	}
	if err := team.Write(project, cfg); err != nil {
		t.Fatal(err)
	}

	legacyCfg, err := team.Read(project)
	if err != nil {
		t.Fatal(err)
	}
	member := team.Member{}
	for _, m := range legacyCfg.Members {
		if m.Role == "backend" {
			member = m
		}
	}

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	elsewhere := t.TempDir()

	for _, from := range []struct{ name, dir string }{
		{"inside the project", project},
		{"outside the project", elsewhere},
	} {
		t.Run(from.name, func(t *testing.T) {
			if err := os.Chdir(from.dir); err != nil {
				t.Fatal(err)
			}
			if err := validateMemberToolPolicyDrift(legacyCfg, member); err != nil {
				t.Fatalf("legacy relative capability source read as drift from %s: %v", from.name, err)
			}
		})
	}
}

// A genuinely changed source set must still fail closed. The fix removes a false
// positive; it must not remove the check.
func TestGenuinelyChangedCapabilitySourceSetStillFailsClosed(t *testing.T) {
	project, _ := seedClaudeToolPolicyProject(t, "backend")
	if _, _, err := captureOutput(t, func() error {
		return runTeamOverlay([]string{"init", "--role", "backend", "--tool-profile", "coding",
			"--allow-tools", "plugin:project@thing,plugin:user@thing"})
	}); err != nil {
		t.Fatalf("overlay init: %v", err)
	}
	cfg, err := team.Read(project)
	if err != nil {
		t.Fatal(err)
	}
	member := team.Member{}
	for _, m := range cfg.Members {
		if m.Role == "backend" {
			member = m
		}
	}
	// Drop a real source from the record: the set genuinely differs now.
	if len(member.ToolPolicySources) < 2 {
		t.Skipf("fixture recorded too few sources to drop one: %v", member.ToolPolicySources)
	}
	member.ToolPolicySources = member.ToolPolicySources[:len(member.ToolPolicySources)-1]
	if err := validateMemberToolPolicyDrift(cfg, member); err == nil {
		t.Fatal("a genuinely changed capability source set must still be reported as drift")
	}
}

// #539 acceptance criterion 4: readiness must fail closed on the same condition
// spawn rejects, so a member cannot be accepted at readiness and die at spawn.
//
// The two entry points must share one predicate. RecordOnly omits ONLY the
// on-disk materialization clause; the capability-source clause -- the actual
// #539 defect -- must fail in both scopes.
func TestReadinessAndSpawnShareOneToolPolicyPredicate(t *testing.T) {
	project, _ := seedClaudeToolPolicyProject(t, "backend")
	if _, _, err := captureOutput(t, func() error {
		return runTeamOverlay([]string{"init", "--role", "backend", "--tool-profile", "coding",
			"--allow-tools", "plugin:project@thing,plugin:user@thing"})
	}); err != nil {
		t.Fatalf("overlay init: %v", err)
	}
	cfg, err := team.Read(project)
	if err != nil {
		t.Fatal(err)
	}
	member := team.Member{}
	for _, m := range cfg.Members {
		if m.Role == "backend" {
			member = m
		}
	}

	// A healthy policy is accepted in both scopes.
	for _, scope := range []toolPolicyCheckScope{toolPolicyCheckFull, toolPolicyCheckRecordOnly} {
		if err := validateMemberToolPolicy(cfg, member, scope); err != nil {
			t.Fatalf("healthy policy rejected at scope %v: %v", scope, err)
		}
	}

	// A drifted capability source set must be rejected in BOTH scopes. If
	// RecordOnly let this through, readiness could pass what spawn rejects,
	// which is the whole #539 dead end.
	drifted := member
	drifted.ToolPolicySources = append([]string{filepath.Join(project, ".claude", "not-a-real-source.json")}, member.ToolPolicySources...)
	for _, scope := range []toolPolicyCheckScope{toolPolicyCheckFull, toolPolicyCheckRecordOnly} {
		err := validateMemberToolPolicy(cfg, drifted, scope)
		if err == nil {
			t.Fatalf("scope %v accepted a drifted capability source set; readiness and spawn must agree", scope)
		}
		if !strings.Contains(err.Error(), "capability source set changed") {
			t.Fatalf("scope %v reported the wrong reason: %v", scope, err)
		}
	}
}

// The materialization clause is the ONLY difference between the scopes: with the
// generated policy file removed, Full must reject and RecordOnly must accept.
// That is what makes the readiness carve-out narrow and auditable.
func TestRecordOnlyScopeOmitsExactlyTheMaterializationClause(t *testing.T) {
	project, _ := seedClaudeToolPolicyProject(t, "backend")
	if _, _, err := captureOutput(t, func() error {
		return runTeamOverlay([]string{"init", "--role", "backend", "--tool-profile", "coding",
			"--allow-tools", "plugin:project@thing,plugin:user@thing"})
	}); err != nil {
		t.Fatalf("overlay init: %v", err)
	}
	cfg, err := team.Read(project)
	if err != nil {
		t.Fatal(err)
	}
	member := team.Member{}
	for _, m := range cfg.Members {
		if m.Role == "backend" {
			member = m
		}
	}
	// Remove the materialized policy file, modelling "planned but not yet
	// written" -- exactly the state readiness sees while planning to write it.
	settings := member.ToolConfig
	if !filepath.IsAbs(settings) {
		settings = filepath.Join(member.EffectiveCWD(cfg.Project), settings)
	}
	if err := os.Remove(settings); err != nil {
		t.Fatalf("remove materialized policy: %v", err)
	}
	if err := validateMemberToolPolicy(cfg, member, toolPolicyCheckFull); err == nil {
		t.Fatal("full scope must reject a missing materialized policy; that is what spawn enforces")
	}
	if err := validateMemberToolPolicy(cfg, member, toolPolicyCheckRecordOnly); err != nil {
		t.Fatalf("record-only scope must tolerate a not-yet-written policy file: %v", err)
	}
}

// The record/compare contract, pinned at the field the reviewer defended:
// tool_policy_sources is printed in overlay plan output, exported in JSON
// envelopes, and digested as part of team.json for readiness. It must therefore
// be recorded ABSOLUTE and never symlink-resolved, so a canonical-at-record
// change cannot silently move operator-visible paths or digest inputs again.
func TestRecordedToolPolicySourcesAreNotSymlinkResolved(t *testing.T) {
	realHome := t.TempDir()
	writeFile(t, filepath.Join(realHome, ".claude", "settings.json"), `{"enabledPlugins":{"user@thing":true}}`)
	writeFile(t, filepath.Join(realHome, ".claude.json"), `{}`)

	// A project reached through a SYMLINK. Recording must keep the symlinked
	// spelling; only comparison may resolve it.
	realProject := seedTeam(t, team.Team{
		Members: []team.Member{{Role: "backend", Binary: "claude", Handle: "backend", Session: "s"}},
	})
	writeFile(t, filepath.Join(realProject, ".claude", "settings.local.json"), `{"enabledPlugins":{"project@thing":true}}`)
	withModelLookupRoots(t, filepath.Join(realHome, "config"), realHome, nil)

	link := filepath.Join(t.TempDir(), "linked-project")
	if err := os.Symlink(realProject, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if canonicalFilesystemPath(link) == absoluteFilesystemPath(link) {
		t.Skip("symlink does not change this path; nothing to distinguish")
	}

	cfg, err := team.Read(link)
	if err != nil {
		t.Fatal(err)
	}
	plans, err := buildRunStartToolProfilePlans(cfg, "", "backend=coding")
	if err != nil {
		t.Fatalf("derive plans: %v", err)
	}
	if len(plans) == 0 {
		t.Fatal("no plans derived; the assertion would be vacuous")
	}
	for _, plan := range plans {
		if plan.After.Role != "backend" {
			continue
		}
		if len(plan.After.ToolPolicySources) == 0 {
			t.Fatal("no sources recorded; the assertion would be vacuous")
		}
		found := false
		for _, src := range plan.After.ToolPolicySources {
			if !filepath.IsAbs(src) {
				t.Fatalf("source %q is not absolute: %v", src, plan.After.ToolPolicySources)
			}
			if strings.HasPrefix(src, absoluteFilesystemPath(link)) {
				found = true
			}
		}
		if !found {
			t.Fatalf("no source retained the symlinked project spelling %q; recording must not resolve symlinks: %v",
				absoluteFilesystemPath(link), plan.After.ToolPolicySources)
		}
	}
}

// The INTENT behind the byte-identity criterion: the prepare-undoes-overlay-init
// loop of #539 must never recur. Byte-identity across MIXED origins is not
// achievable without rewriting recorded paths (see pathnorm.go), so what
// actually has to hold is that mixed-origin recordings never read as DRIFT.
//
// This is the case that ships: `team overlay init` run from inside the repo takes
// its project from the working directory (symlink-resolved), while the wizard
// passes --project explicitly (unresolved). The two record different bytes for
// the same files, and that must be benign.
func TestMixedOriginRecordingsDoNotReadAsDrift(t *testing.T) {
	project, _ := seedClaudeToolPolicyProject(t, "backend")

	// Origin A: what an implicit-cwd invocation resolves (Getwd, symlink-resolved).
	implicitOrigin, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// Origin B: what an explicit --project invocation records (absolute, unresolved).
	explicitOrigin := absoluteFilesystemPath(project)
	if implicitOrigin == explicitOrigin {
		t.Skip("this platform's cwd and flag origins agree; there is no mixed-origin case to pin")
	}

	// Record with origin A, exactly as `team overlay init` from inside the repo does.
	if _, _, err := captureOutput(t, func() error {
		return runTeamOverlay([]string{"init", "--role", "backend", "--tool-profile", "coding",
			"--allow-tools", "plugin:project@thing,plugin:user@thing"})
	}); err != nil {
		t.Fatalf("overlay init: %v", err)
	}
	recorded := readTeamMember(t, project, "backend").ToolPolicySources
	if len(recorded) == 0 {
		t.Fatal("no sources recorded; the assertion would be vacuous")
	}

	// Confirm the origins really do produce different bytes, so this test is
	// pinning the mixed case rather than trivially passing.
	viaExplicit, err := team.Read(explicitOrigin)
	if err != nil {
		t.Fatal(err)
	}
	plans, err := buildRunStartToolProfilePlans(viaExplicit, "", "backend=coding")
	if err != nil {
		t.Fatalf("derive plans: %v", err)
	}
	var derived []string
	for _, plan := range plans {
		if plan.After.Role == "backend" {
			derived = plan.After.ToolPolicySources
		}
	}
	if len(derived) == 0 {
		t.Fatal("no sources derived; the assertion would be vacuous")
	}
	if reflect.DeepEqual(recorded, derived) {
		t.Skip("origins produced identical bytes here; the mixed case is not reproducible on this host")
	}

	// THE POINT: different bytes, same files, and therefore NO drift -- from a
	// team read through EITHER origin. This is what makes shipping divergent
	// recordings safe instead of lucky, and it is the #539 loop being impossible.
	for _, origin := range []struct{ name, dir string }{
		{"implicit cwd origin", implicitOrigin},
		{"explicit --project origin", explicitOrigin},
	} {
		t.Run(origin.name, func(t *testing.T) {
			cfg, readErr := team.Read(origin.dir)
			if readErr != nil {
				t.Fatal(readErr)
			}
			member := team.Member{}
			for _, m := range cfg.Members {
				if m.Role == "backend" {
					member = m
				}
			}
			// Spawn's predicate, which is what refused the launch in #539.
			if err := validateMemberToolPolicyDrift(cfg, member); err != nil {
				t.Fatalf("mixed-origin recording read as drift from %s: %v", origin.name, err)
			}
			// And readiness must agree, so this cannot pass one and fail the other.
			if err := validateMemberToolPolicy(cfg, member, toolPolicyCheckRecordOnly); err != nil {
				t.Fatalf("readiness rejected a mixed-origin recording from %s: %v", origin.name, err)
			}
		})
	}
}
