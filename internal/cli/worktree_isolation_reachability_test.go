package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

// #538 acceptance criterion 2: a two-implementer squad must reach `ready` from a
// clean repo WITHOUT hand-editing team.json and WITHOUT trial-and-error across
// three commands.
//
// The original failure was a loop: --prepare blocked on worktree_isolation, the
// fix text named a --cwd flag the operator could not find and an exception
// command that needed a profile --prepare had just rolled back. These tests pin
// that each remedy the row names is reachable in ONE roster-creation command.

// remedy 1, at creation: per-member isolated working directories.
func TestSharedCwdRemedyIsolatedCwdsReachableInOneCommand(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	wtA := filepath.Join(dir, "wt-a")
	wtB := filepath.Join(dir, "wt-b")
	for _, p := range []string{wtA, wtB} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	if _, _, err := captureOutput(t, func() error {
		return runNew([]string{"profile", "squad",
			"--project", dir,
			"--roles", "cto,dev-1,dev-2",
			"--binary", "cto=claude,dev-1=claude,dev-2=codex",
			"--actor-mode", "cto=review,dev-1=implementation,dev-2=implementation",
			"--cwd", "dev-1=" + wtA + ",dev-2=" + wtB,
			"--no-session-pin",
		})
	}); err != nil {
		t.Fatalf("one-command roster creation with isolated cwds failed: %v", err)
	}

	tm, err := team.ReadProfile(dir, "squad")
	if err != nil {
		t.Fatal(err)
	}
	// The readiness row must be satisfied without an exception being recorded.
	row := worktreeIsolationReadinessRow(tm, "squad")
	if row.Status != "ready" {
		t.Fatalf("worktree_isolation = %s (%s); isolated cwds must satisfy it. fix text was: %s", row.Status, row.Evidence, row.Fix)
	}
	if strings.TrimSpace(tm.SharedCwdException) != "" {
		t.Fatal("isolated cwds must not require a recorded exception")
	}
}

// remedy 2, at creation: deliberately accept the shared checkout.
//
// --shared-cwd-exception was one of the 17 value-taking team-init flags that
// `new profile` silently dropped, so this remedy was not merely undocumented but
// actively rejected with an error blaming the operator's argument.
func TestSharedCwdRemedyExceptionReachableInOneCommand(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)

	if _, _, err := captureOutput(t, func() error {
		return runNew([]string{"profile", "squad",
			"--project", dir,
			"--roles", "cto,dev-1,dev-2",
			"--binary", "cto=claude,dev-1=claude,dev-2=codex",
			"--actor-mode", "cto=review,dev-1=implementation,dev-2=implementation",
			"--shared-cwd-exception", "single checkout accepted for this run",
			"--no-session-pin",
		})
	}); err != nil {
		t.Fatalf("one-command roster creation with a recorded exception failed: %v", err)
	}

	tm, err := team.ReadProfile(dir, "squad")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(tm.SharedCwdException) == "" {
		t.Fatal("--shared-cwd-exception was accepted but not recorded")
	}
	row := worktreeIsolationReadinessRow(tm, "squad")
	if row.Status != "ready" {
		t.Fatalf("worktree_isolation = %s (%s); a recorded exception must satisfy it", row.Status, row.Evidence)
	}
}

// remedy 1, post-creation: fixing an EXISTING roster without editing team.json.
// This is the path that did not exist at all before #538.
func TestSharedCwdRemedyIsolatedCwdReachableOnExistingRoster(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)

	if _, _, err := captureOutput(t, func() error {
		return runNew([]string{"profile", "squad",
			"--project", dir,
			"--roles", "cto,dev-1,dev-2",
			"--binary", "cto=claude,dev-1=claude,dev-2=codex",
			"--actor-mode", "cto=review,dev-1=implementation,dev-2=implementation",
			"--no-session-pin",
		})
	}); err != nil {
		t.Fatalf("roster creation failed: %v", err)
	}

	// Precondition: this roster IS blocked, so the test proves a real fix.
	tm, err := team.ReadProfile(dir, "squad")
	if err != nil {
		t.Fatal(err)
	}
	blocked := worktreeIsolationReadinessRow(tm, "squad")
	if blocked.Status != "blocked" {
		t.Fatalf("expected a shared-cwd collision to start blocked, got %s (%s)", blocked.Status, blocked.Evidence)
	}

	wt := filepath.Join(dir, "wt-a")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := captureOutput(t, func() error {
		return runTeamMember([]string{"update", "dev-2", "--project", dir, "--profile", "squad", "--cwd", wt})
	}); err != nil {
		t.Fatalf("team member update --cwd failed: %v", err)
	}

	fixed, err := team.ReadProfile(dir, "squad")
	if err != nil {
		t.Fatal(err)
	}
	if row := worktreeIsolationReadinessRow(fixed, "squad"); row.Status != "ready" {
		t.Fatalf("worktree_isolation = %s (%s) after giving dev-2 its own cwd", row.Status, row.Evidence)
	}
	// And it must be reversible: clearing the override restores the collision.
	if _, _, err := captureOutput(t, func() error {
		return runTeamMember([]string{"update", "dev-2", "--project", dir, "--profile", "squad", "--cwd", ""})
	}); err != nil {
		t.Fatalf("clearing --cwd failed: %v", err)
	}
	cleared, err := team.ReadProfile(dir, "squad")
	if err != nil {
		t.Fatal(err)
	}
	if row := worktreeIsolationReadinessRow(cleared, "squad"); row.Status != "blocked" {
		t.Fatalf("clearing the cwd override should restore the collision, got %s", row.Status)
	}
}

// #538 acceptance criterion 1: every remedy the row NAMES must be executable
// against the EXACT blocked roster.
//
// Second review F1: naming a command is not enough, it must be SCOPED. An
// unscoped `team member update` targets the default profile, so for a blocked
// named profile the printed command would mutate a different roster. And a
// creation-time form is wrong here by construction: this profile already exists,
// so `new profile NAME` would build an unrelated roster. Creation forms belong to
// the rollback message, not this row.
func TestWorktreeIsolationFixNamesScopedExecutableRemedies(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	seedBlockedSquad(t, dir)
	tm, err := team.ReadProfile(dir, "squad")
	if err != nil {
		t.Fatal(err)
	}
	fix := worktreeIsolationReadinessRow(tm, "squad").Fix
	if fix == "" {
		t.Fatal("a blocked row must carry a fix")
	}
	for _, want := range []string{
		"amq-squad team member update",
		"--cwd /path/to/worktree",
		"amq-squad team shared-cwd-exception set",
		"--profile " + shellQuote("squad"),
		"--project " + shellQuote(dir),
		"dev-1",
	} {
		if !strings.Contains(fix, want) {
			t.Fatalf("fix text is missing %q; text was: %s", want, fix)
		}
	}
	// The blocked profile already exists, so a creation form here would be wrong.
	if strings.Contains(fix, "new profile") {
		t.Fatalf("fix text must not offer a creation remedy for an EXISTING blocked profile; text was: %s", fix)
	}
}

// A blocked DEFAULT profile must not be told to pass --profile default.
func TestWorktreeIsolationFixOmitsDefaultProfileScope(t *testing.T) {
	dir := t.TempDir()
	fix := worktreeIsolationFix(dir, team.DefaultProfile, []string{"dev-1", "dev-2"})
	if strings.Contains(fix, "--profile") {
		t.Fatalf("default profile needs no --profile scope; text was: %s", fix)
	}
	if !strings.Contains(fix, "--project "+shellQuote(dir)) {
		t.Fatalf("fix text must still scope --project; text was: %s", fix)
	}
}

// Second review F5: run the ACTUAL displayed command, parsed out of the fix text,
// so the row cannot print something that does not work. Absolute-everything tests
// are what masked F1 and F2.
func TestFixTextCommandExecutesAsDisplayed(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	seedBlockedSquad(t, dir)
	// #538 F5: run the displayed command from a NEUTRAL directory. Executing it
	// from inside the blocked project lets an implicit cwd stand in for --project,
	// which would mask exactly the mis-scoping F1 was about.
	chdir(t, t.TempDir())
	tm, err := team.ReadProfile(dir, "squad")
	if err != nil {
		t.Fatal(err)
	}
	fix := worktreeIsolationReadinessRow(tm, "squad").Fix

	argv := extractQuotedCommand(t, fix, "amq-squad team member update")
	// Substitute a real worktree for the placeholder, changing nothing else.
	wt := filepath.Join(dir, "wt-a")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	for i, a := range argv {
		if a == "/path/to/worktree" {
			argv[i] = wt
		}
	}
	if len(argv) < 3 || argv[0] != "amq-squad" || argv[1] != "team" || argv[2] != "member" {
		t.Fatalf("unexpected command shape: %v", argv)
	}
	if _, _, err := captureOutput(t, func() error { return runTeamMember(argv[3:]) }); err != nil {
		t.Fatalf("the command the row DISPLAYS failed to run: %v\nargv: %v", err, argv)
	}
	fixed, err := team.ReadProfile(dir, "squad")
	if err != nil {
		t.Fatal(err)
	}
	if row := worktreeIsolationReadinessRow(fixed, "squad"); row.Status != "ready" {
		t.Fatalf("running the displayed remedy left the row %s (%s)", row.Status, row.Evidence)
	}
}

// Second review F2: a RELATIVE --cwd must resolve against the PROJECT, not the
// shell working directory. `--project /repo --cwd ../wt` run from /tmp previously
// recorded /tmp/wt on update and /repo/../wt on create -- two writers, two
// origins, which is the #539/#540 defect class.
func TestRelativeCwdResolvesAgainstProjectNotShellCwd(t *testing.T) {
	project := t.TempDir()
	sibling := filepath.Join(project, "wt-rel")
	if err := os.MkdirAll(sibling, 0o755); err != nil {
		t.Fatal(err)
	}
	seedBlockedSquadIn(t, project)

	// Run from an UNRELATED directory, which is what exposes the wrong anchor.
	elsewhere := t.TempDir()
	chdir(t, elsewhere)

	if _, _, err := captureOutput(t, func() error {
		return runTeamMember([]string{"update", "dev-2", "--project", project, "--profile", "squad", "--cwd", "wt-rel"})
	}); err != nil {
		t.Fatalf("relative --cwd update failed: %v", err)
	}
	tm, err := team.ReadProfile(project, "squad")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range tm.Members {
		if m.Role != "dev-2" {
			continue
		}
		if !sameFilesystemPath(m.CWD, sibling) {
			t.Fatalf("relative --cwd recorded %q; must resolve against the project (%q), not the shell cwd (%q)", m.CWD, sibling, elsewhere)
		}
	}
	// Create and update must agree for the same relative input.
	viaCreate := t.TempDir()
	if err := os.MkdirAll(filepath.Join(viaCreate, "wt-rel"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := captureOutput(t, func() error {
		return runNew([]string{"profile", "squad", "--project", viaCreate,
			"--roles", "cto,dev-1,dev-2", "--binary", "cto=claude,dev-1=claude,dev-2=codex",
			"--actor-mode", "cto=review,dev-1=implementation,dev-2=implementation",
			"--cwd", "dev-2=wt-rel", "--no-session-pin"})
	}); err != nil {
		t.Fatalf("relative --cwd at creation failed: %v", err)
	}
	created, err := team.ReadProfile(viaCreate, "squad")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range created.Members {
		if m.Role != "dev-2" {
			continue
		}
		want := filepath.Join(viaCreate, "wt-rel")
		if !sameFilesystemPath(m.CWD, want) {
			t.Fatalf("create path recorded %q for a relative --cwd; want %q. create and update must share one origin", m.CWD, want)
		}
	}
}

// Second review F4: readiness must detect the collision doctor detects. Grouping
// raw strings let a symlink and its target count as two directories at
// preparation and one Git index at runtime -- ready, then doctor fail.
func TestReadinessGroupsSymlinkEquivalentCwdsAsCollision(t *testing.T) {
	project := t.TempDir()
	real := filepath.Join(project, "shared")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(project, "shared-link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if canonicalFilesystemPath(link) != canonicalFilesystemPath(real) {
		t.Skip("symlink does not canonicalize onto its target here")
	}

	tm := team.Team{Project: project, Members: []team.Member{
		{Role: "dev-1", Binary: "claude", Handle: "dev-1", ActorMode: team.ActorModeImplementation, CWD: real},
		{Role: "dev-2", Binary: "codex", Handle: "dev-2", ActorMode: team.ActorModeImplementation, CWD: link},
	}}
	row := worktreeIsolationReadinessRow(tm, "squad")
	if row.Status != "blocked" {
		t.Fatalf("two members on symlink-equivalent directories share one Git index and must block; got %s (%s)", row.Status, row.Evidence)
	}
	// The evidence should show a path the operator recognises, not only the
	// canonical form.
	if !strings.Contains(row.Evidence, "dev-1") || !strings.Contains(row.Evidence, "dev-2") {
		t.Fatalf("evidence must name both colliding roles; got %s", row.Evidence)
	}
}

// seedBlockedSquad creates a named profile whose two implementers share the
// team-home, i.e. the exact blocked state #538 is about.
func seedBlockedSquad(t *testing.T, dir string) {
	t.Helper()
	seedBlockedSquadIn(t, dir)
}

func seedBlockedSquadIn(t *testing.T, dir string) {
	t.Helper()
	if _, _, err := captureOutput(t, func() error {
		return runNew([]string{"profile", "squad", "--project", dir,
			"--roles", "cto,dev-1,dev-2",
			"--binary", "cto=claude,dev-1=claude,dev-2=codex",
			"--actor-mode", "cto=review,dev-1=implementation,dev-2=implementation",
			"--no-session-pin"})
	}); err != nil {
		t.Fatalf("seed blocked squad: %v", err)
	}
}

// extractQuotedCommand pulls the single-quoted command beginning with prefix out
// of a fix string and splits it into argv, honouring the shellQuote form the row
// emits.
func extractQuotedCommand(t *testing.T, text, prefix string) []string {
	t.Helper()
	i := strings.Index(text, "'"+prefix)
	if i < 0 {
		t.Fatalf("fix text does not contain a quoted %q command: %s", prefix, text)
	}
	rest := text[i+1:]
	j := strings.Index(rest, "'")
	if j < 0 {
		t.Fatalf("unterminated quoted command in fix text: %s", text)
	}
	return strings.Fields(rest[:j])
}

// #538 F4 counterexample (b): two SUBDIRECTORIES of one checkout are distinct
// directories but share ONE Git index, so a directory-only key misses a real
// collision that doctor reports.
func TestReadinessGroupsSubdirectoriesOfOneCheckoutAsCollision(t *testing.T) {
	project := t.TempDir()
	subA := filepath.Join(project, "pkg", "a")
	subB := filepath.Join(project, "pkg", "b")
	for _, p := range []string{subA, subB} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Both subdirectories resolve to the same index, as they would in a checkout.
	sharedIndex := filepath.Join(project, ".git", "index")
	withIsolationIndexProbe(t, func(dir string) (string, isolationObservation) {
		if strings.HasPrefix(dir, canonicalFilesystemPath(project)) {
			return sharedIndex, isolationObserved
		}
		return "", isolationNotACheckout
	})

	tm := team.Team{Project: project, Members: []team.Member{
		{Role: "dev-1", Binary: "claude", Handle: "dev-1", ActorMode: team.ActorModeImplementation, CWD: subA},
		{Role: "dev-2", Binary: "codex", Handle: "dev-2", ActorMode: team.ActorModeImplementation, CWD: subB},
	}}
	row := worktreeIsolationReadinessRow(tm, "squad")
	if row.Status != "blocked" {
		t.Fatalf("two subdirectories of one checkout share one Git index and must block; got %s (%s)", row.Status, row.Evidence)
	}
}

// #538 F4 counterexample (a): a PLANNED cwd that does not exist yet has no index
// to observe. It must still be grouped (predicting the collision is the point of a
// pre-launch check), by canonical directory as a declared proxy -- and the evidence
// must SAY it is a proxy rather than implying an observation.
func TestReadinessProxiesPlannedDirectoriesAndDisclosesIt(t *testing.T) {
	project := t.TempDir()
	planned := filepath.Join(project, "not-created-yet")
	withIsolationIndexProbe(t, func(string) (string, isolationObservation) { return "", isolationNotACheckout })

	tm := team.Team{Project: project, Members: []team.Member{
		{Role: "dev-1", Binary: "claude", Handle: "dev-1", ActorMode: team.ActorModeImplementation, CWD: planned},
		{Role: "dev-2", Binary: "codex", Handle: "dev-2", ActorMode: team.ActorModeImplementation, CWD: planned},
	}}
	row := worktreeIsolationReadinessRow(tm, "squad")
	if row.Status != "blocked" {
		t.Fatalf("two members planned into one directory must block; got %s (%s)", row.Status, row.Evidence)
	}
	if !strings.Contains(row.Evidence, "planned directory") {
		t.Fatalf("evidence must disclose that this group was proxied, not observed; got: %s", row.Evidence)
	}
	// Distinct planned directories are the predictor of distinct future indexes.
	tm.Members[1].CWD = filepath.Join(project, "also-not-created")
	if row := worktreeIsolationReadinessRow(tm, "squad"); row.Status != "ready" {
		t.Fatalf("distinct planned directories must not collide; got %s (%s)", row.Status, row.Evidence)
	}
}

// An OBSERVED collision must not be labelled a proxy.
func TestObservedCollisionIsNotDisclosedAsProxy(t *testing.T) {
	project := t.TempDir()
	shared := filepath.Join(project, "shared")
	if err := os.MkdirAll(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	withIsolationIndexProbe(t, func(string) (string, isolationObservation) {
		return filepath.Join(project, ".git", "index"), isolationObserved
	})
	tm := team.Team{Project: project, Members: []team.Member{
		{Role: "dev-1", Binary: "claude", Handle: "dev-1", ActorMode: team.ActorModeImplementation, CWD: shared},
		{Role: "dev-2", Binary: "codex", Handle: "dev-2", ActorMode: team.ActorModeImplementation, CWD: shared},
	}}
	row := worktreeIsolationReadinessRow(tm, "squad")
	if row.Status != "blocked" {
		t.Fatalf("expected blocked, got %s", row.Status)
	}
	if strings.Contains(row.Evidence, "planned directory") {
		t.Fatalf("an observed index collision must not be disclosed as a proxy; got: %s", row.Evidence)
	}
}

func withIsolationIndexProbe(t *testing.T, probe func(string) (string, isolationObservation)) {
	t.Helper()
	orig := worktreeIsolationIndexProbe
	t.Cleanup(func() { worktreeIsolationIndexProbe = orig })
	worktreeIsolationIndexProbe = probe
}

// #538 F4 round 4: an observation FAILURE is not a negative result.
//
// The probe previously treated every git error as "no index here", so if git were
// missing or failing, two subdirectories of one checkout would fall back to
// distinct directory keys and readiness would report READY -- resurrecting the
// exact divergence F4 exists to prevent, this time caused by infrastructure. Under
// #497 an unverifiable isolation claim is a blocker.
func TestUnobservableGitBlocksReadinessInsteadOfSilentlyProxying(t *testing.T) {
	project := t.TempDir()
	shared := filepath.Join(project, "shared")
	if err := os.MkdirAll(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	withIsolationIndexProbe(t, func(string) (string, isolationObservation) {
		return "", isolationUnobservable
	})

	// Deliberately a roster that would look FINE under the old silent fallback:
	// two distinct directories, so directory-keyed grouping reports no collision.
	tm := team.Team{Project: project, Members: []team.Member{
		{Role: "dev-1", Binary: "claude", Handle: "dev-1", ActorMode: team.ActorModeImplementation, CWD: filepath.Join(project, "a")},
		{Role: "dev-2", Binary: "codex", Handle: "dev-2", ActorMode: team.ActorModeImplementation, CWD: filepath.Join(project, "b")},
	}}
	row := worktreeIsolationReadinessRow(tm, "squad")
	if row.Status != "blocked" {
		t.Fatalf("unverifiable isolation must block, not pass; got %s (%s)", row.Status, row.Evidence)
	}
	if !strings.Contains(row.Evidence, "cannot verify") {
		t.Fatalf("evidence must name the observation failure; got: %s", row.Evidence)
	}
	// It must NOT be presented as a collision that isn't there, nor as a proxy.
	if strings.Contains(row.Evidence, "share one working directory") {
		t.Fatalf("must not claim a collision it did not observe; got: %s", row.Evidence)
	}
	if strings.Contains(row.Evidence, "planned directory") {
		t.Fatalf("an observation failure is not a proxy; got: %s", row.Evidence)
	}
	if !strings.Contains(row.Fix, "git") {
		t.Fatalf("fix must tell the operator to repair git; got: %s", row.Fix)
	}
}

// A REAL git failure, not a stubbed one: point the probe at a directory while git
// is unavailable on PATH, and confirm the production probe classifies it as
// unobservable rather than as a clean negative.
func TestProductionProbeTreatsMissingGitAsUnobservable(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", t.TempDir()) // no git here
	_, obs := worktreeIsolationIndexProbe(dir)
	if obs != isolationUnobservable {
		t.Fatalf("git missing from PATH must be unobservable, got %v", obs)
	}
}

// A directory that does not exist is a CLEAN negative (the planned-worktree case),
// which must stay distinguishable from infrastructure failure.
func TestProductionProbeTreatsMissingDirectoryAsCleanNegative(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "not-created-yet")
	_, obs := worktreeIsolationIndexProbe(missing)
	if obs != isolationNotACheckout {
		t.Fatalf("a nonexistent planned directory must be a clean negative, got %v", obs)
	}
}

// A real directory that is genuinely not a checkout must also be a clean negative,
// so the proxy path still works where it should.
func TestProductionProbeTreatsNonCheckoutAsCleanNegative(t *testing.T) {
	dir := t.TempDir()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable; this case needs a working git to answer 'not a repository'")
	}
	// t.TempDir() is under /var/folders, outside any checkout.
	_, obs := worktreeIsolationIndexProbe(dir)
	if obs == isolationUnobservable {
		t.Fatal("a real non-checkout directory must be a clean negative, not an observation failure")
	}
}

// The exec must be bounded so a hung git cannot hang preparation, and the timeout
// must be classified as an observation failure.
func TestProbeTimeoutIsAnObservationFailure(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	orig := worktreeIsolationIndexProbeTimeout
	t.Cleanup(func() { worktreeIsolationIndexProbeTimeout = orig })
	// A timeout this small cannot complete a real exec.
	worktreeIsolationIndexProbeTimeout = 1 * time.Nanosecond
	_, obs := worktreeIsolationIndexProbe(t.TempDir())
	if obs != isolationUnobservable {
		t.Fatalf("a timed-out probe must be an observation failure, got %v", obs)
	}
}
