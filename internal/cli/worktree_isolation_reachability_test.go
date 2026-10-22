package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/team"
)

// #538 acceptance criterion 2: a two-implementer squad must reach `ready` from a
// clean repo WITHOUT hand-editing team.json and WITHOUT trial-and-error across
// three commands.
//
// The original failure was a loop: --prepare blocked on worktree_isolation, the
// fix text named a --cwd flag the operator could not find and an exception
// command that needed a profile --prepare had just rolled back. These tests pin
// that each remedy the row names is reachable in ONE roster-creation command.

// A blocked DEFAULT profile must not be told to pass --profile default.
func TestWorktreeIsolationFixOmitsDefaultProfileScope(t *testing.T) {
	dir := t.TempDir()
	fix := worktreeIsolationFix(dir, team.DefaultProfile, "", []string{"dev-1", "dev-2"})
	if strings.Contains(fix, "--profile") {
		t.Fatalf("default profile needs no --profile scope; text was: %s", fix)
	}
	if !strings.Contains(fix, "--project "+shellQuote(dir)) {
		t.Fatalf("fix text must still scope --project; text was: %s", fix)
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
	row := worktreeIsolationCheck(tm, "squad")
	if row.Status != "blocked" {
		t.Fatalf("two members on symlink-equivalent directories share one Git index and must block; got %s (%s)", row.Status, row.Evidence)
	}
	// The evidence should show a path the operator recognises, not only the
	// canonical form.
	if !strings.Contains(row.Evidence, "dev-1") || !strings.Contains(row.Evidence, "dev-2") {
		t.Fatalf("evidence must name both colliding roles; got %s", row.Evidence)
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
	row := worktreeIsolationCheck(tm, "squad")
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
	row := worktreeIsolationCheck(tm, "squad")
	if row.Status != "blocked" {
		t.Fatalf("two members planned into one directory must block; got %s (%s)", row.Status, row.Evidence)
	}
	if !strings.Contains(row.Evidence, "planned directory") {
		t.Fatalf("evidence must disclose that this group was proxied, not observed; got: %s", row.Evidence)
	}
	// Distinct planned directories are the predictor of distinct future indexes.
	tm.Members[1].CWD = filepath.Join(project, "also-not-created")
	if row := worktreeIsolationCheck(tm, "squad"); row.Status != "ready" {
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
	row := worktreeIsolationCheck(tm, "squad")
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
	row := worktreeIsolationCheck(tm, "squad")
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
