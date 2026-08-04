package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

type worktreeIsolationResult struct {
	Artifact string
	Status   string
	Evidence string
	Fix      string
}

// worktreeIsolationCheck is #497's pre-launch isolation check:
// launch fails closed when 2+ mutation-capable members would share one
// resolved working directory (one Git index/checkout) without an explicit
// recorded exception (team.Team.SharedCwdException). This is a static check
// over the accepted team profile; the live worktree drift/staleness doctor
// rows are a separate runtime concern owned elsewhere.
//
// AGREEMENT WITH doctor's shared-index-collision (#538 acceptance criterion 4,
// and the reason the two texts are NOT identical):
//
// Both checks honour the SAME exception (SharedCwdException clears it) and use the
// same vocabulary. On the CONDITION the claim is deliberately QUALIFIED, because
// an unqualified "same condition" was false and was caught in review twice:
//
//   - WHERE OBSERVABLE, this check uses doctor's own observable: a member cwd that
//     exists inside a Git checkout is grouped by its resolved index path
//     (git rev-parse --git-path index). That is what makes two SUBDIRECTORIES of
//     one checkout collide here as they do at runtime -- comparing directories
//     alone would call them distinct.
//   - WHERE NOT OBSERVABLE, a planned cwd that does not exist yet has no index to
//     resolve, so it is grouped by canonical directory as a declared PROXY: at
//     preparation the best available predictor of a distinct future index is a
//     distinct planned directory. doctor excludes such members entirely, because
//     at runtime a nonexistent worktree is a different finding.
//
// So: same observable where observable, declared proxy where not, same exception
// semantics and vocabulary always. Do not restate this as plain "same condition".
//
// What else differs is lifecycle-bound and deliberate:
//
//   - SEVERITY. This check runs at PREPARATION, before any agent exists, and
//     fails closed per #497. doctor's severity is runtime-derived, and under the
//     #537 contract (t4, landing separately) becomes explicitly liveness-derived:
//     fail only when 2+ affected members are LIVE, warn otherwise. Either way the
//     rule cannot be copied here -- at preparation nothing is live, so a
//     liveness-derived severity would always warn and defeat fail-closed. This
//     sentence describes #537's target contract; if it has not landed in the tree
//     you are reading, doctor still fails unconditionally on the collision.
//   - REMEDY. doctor names `worktree plan|materialize`, which require --task ID.
//     At preparation no tasks exist, so that remedy is unrunnable here and naming
//     it would be false. This row names the pre-launch mechanisms instead.
//
// So "agree" means same condition, same exception semantics, same vocabulary,
// with stage-appropriate severity and remedy. If you change one side's condition
// or exception handling, change both.
func worktreeIsolationCheck(t team.Team, profile string) worktreeIsolationResult {
	return worktreeIsolationCheckForSession(t, profile, "")
}

// worktreeIsolationCheckForSession carries the active workstream into remedies
// whose CLI accepts it. The exception itself remains a profile-wide property;
// accepting the session keeps the printed command scoped without changing the
// isolation rule.
func worktreeIsolationCheckForSession(t team.Team, profile, session string) worktreeIsolationResult {
	groups := map[string][]string{}
	groupDisplay := map[string]string{}
	proxied := map[string]bool{}
	var unobservable []string
	for _, m := range t.Members {
		if team.EffectiveActorMode(t, m) != team.ActorModeImplementation {
			continue
		}
		cwd := m.EffectiveCWD(t.Project)
		key, obs := memberIsolationKey(cwd)
		if obs == isolationUnobservable {
			// #538 F4 round 4: git missing, hung, or failing is NOT "no index
			// here". Silently proxying it would let two subdirectories of one
			// checkout group as distinct and report ready -- the very divergence
			// this check exists to prevent, caused by infrastructure rather than
			// configuration. Under #497 an unverifiable isolation claim is a
			// blocker, so fail closed and say why.
			unobservable = append(unobservable, fmt.Sprintf("%s: %s", m.Role, cwd))
			continue
		}
		// Display the path as recorded; group by the observable (or its proxy).
		if _, seen := groupDisplay[key]; !seen {
			groupDisplay[key] = cwd
		}
		if obs == isolationNotACheckout {
			proxied[key] = true
		}
		groups[key] = append(groups[key], m.Role)
	}
	if len(unobservable) > 0 {
		sort.Strings(unobservable)
		return worktreeIsolationResult{
			Artifact: "worktree_isolation",
			Status:   "blocked",
			Evidence: fmt.Sprintf("cannot verify working-directory isolation for %s", strings.Join(unobservable, "; ")),
			Fix:      "install/repair git so 'git rev-parse --git-path index' runs in each member working directory, then re-run preparation. Isolation cannot be confirmed without it, and an unverifiable claim is treated as a blocker (#497)",
		}
	}
	var collisions []string
	for key, roles := range groups {
		if len(roles) < 2 {
			continue
		}
		sort.Strings(roles)
		shown := groupDisplay[key]
		if shown == "" {
			shown = key
		}
		detail := fmt.Sprintf("%s: %s", shown, strings.Join(roles, ", "))
		if proxied[key] {
			// Be explicit that this group was matched by planned directory rather
			// than an observed index, so the evidence never overstates what was
			// actually checked.
			detail += " (planned directory; no Git index to observe yet)"
		}
		collisions = append(collisions, detail)
	}
	if len(collisions) == 0 {
		return worktreeIsolationResult{Artifact: "worktree_isolation", Status: "ready", Evidence: "no 2+ mutation-capable members share one working directory"}
	}
	sort.Strings(collisions)
	evidence := strings.Join(collisions, "; ")
	if reason := strings.TrimSpace(t.SharedCwdException); reason != "" {
		return worktreeIsolationResult{Artifact: "worktree_isolation", Status: "ready", Evidence: fmt.Sprintf("shared-cwd collision accepted (%s); exception: %s", evidence, reason)}
	}
	return worktreeIsolationResult{
		Artifact: "worktree_isolation",
		Status:   "blocked",
		Evidence: fmt.Sprintf("2+ mutation-capable members share one working directory without a recorded exception: %s", evidence),
		Fix:      worktreeIsolationFix(t.Project, profile, session, sharedCwdCollisionRoles(groups)),
	}
}

// worktreeIsolationFix names remedies that are ACTUALLY EXECUTABLE against the
// EXACT roster that is blocked.
//
// #538: the original text said "give each mutation-capable member its own --cwd
// (isolated worktree)" and named no command, so the operator concluded the flag
// did not exist and lost more time here than anywhere else in the run. A remedy
// the reader cannot execute is not a remedy.
//
// Second review F1: naming commands is not enough -- they must be SCOPED. An
// unscoped `team member update` targets the default profile, so for a blocked
// NAMED profile the suggested command would silently mutate a different roster.
// And a creation-time remedy is wrong here by construction: this profile already
// exists (readiness just read it), so `new profile NAME` would build an unrelated
// roster rather than fix this one. The creation forms belong only to the
// transactional-rollback case, which the preparation failure message owns.
func worktreeIsolationFix(project, profile, session string, roles []string) string {
	example := "ROLE"
	if len(roles) > 0 {
		example = roles[0]
	}
	scope := ""
	if p := strings.TrimSpace(project); p != "" {
		scope += " --project " + shellQuote(p)
	}
	// Only a non-default profile needs naming; adding --profile default would be
	// noise that invites cargo-culting it onto commands that reject it.
	if pr := strings.TrimSpace(profile); pr != "" && pr != team.DefaultProfile {
		scope += " --profile " + shellQuote(pr)
	}
	// team member update uses --session as a mutation, not a scoping flag. Add
	// the preparation session only to the exception command that treats it as
	// compatibility context.
	exceptionScope := scope
	if session := strings.TrimSpace(session); session != "" {
		exceptionScope += " --session " + shellQuote(session)
	}
	return strings.Join([]string{
		"give each mutation-capable member its own working directory with " +
			"'amq-squad team member update " + example + " --cwd /path/to/worktree" + scope + "'" +
			" (repeat per member; a relative --cwd resolves against the project)",
		"or accept the shared checkout deliberately with " +
			`'amq-squad team shared-cwd-exception set "<reason>"` + exceptionScope + "'",
	}, "; ")
}

// sharedCwdCollisionRoles returns the colliding roles in deterministic order, so
// the fix text can name a real role from the operator's own roster instead of a
// placeholder they have to translate.
func sharedCwdCollisionRoles(groups map[string][]string) []string {
	var out []string
	for _, roles := range groups {
		if len(roles) < 2 {
			continue
		}
		out = append(out, roles...)
	}
	sort.Strings(out)
	return out
}

// isolationObservation is the outcome of trying to observe a member cwd's Git
// index. The three cases are deliberately distinct: #538 F4 round 4 found that
// collapsing "not a checkout" and "could not run git" into one boolean made
// INFRASTRUCTURE FAILURE look like a clean answer, so two subdirectories of one
// checkout fell back to distinct directory keys and readiness reported ready --
// resurrecting the exact divergence F4 was about.
type isolationObservation int

const (
	// isolationObserved: git ran and resolved an index path. Authoritative.
	isolationObserved isolationObservation = iota
	// isolationNotACheckout: git ran and reported this is not a checkout, or the
	// directory does not exist yet. A clean, trustworthy negative -> proxy.
	isolationNotACheckout
	// isolationUnobservable: git is missing, failed to execute, timed out, or
	// returned an error we cannot interpret. NOT a negative result. Readiness must
	// fail closed, because "cannot verify isolation" is a blocker under #497, not
	// a pass.
	isolationUnobservable
)

// memberIsolationKey returns the grouping key for a member working directory and
// how that key was obtained.
//
// #538 F4: doctor groups by resolved Git index path, so grouping by directory
// string made the two checks disagree on the condition itself. Two counterexamples
// drove the shape:
//
//   - two SUBDIRECTORIES of one checkout are distinct directories but ONE index,
//     so a directory-only key misses a real collision;
//   - a planned worktree that does not exist yet has NO index, so an index-only
//     key cannot classify it (doctor excludes such members; preparation must not,
//     since predicting the collision is the point pre-launch).
//
// Hence: observe the index when it can be observed, fall back to canonical
// directory as a DECLARED proxy on a clean negative, and refuse to answer at all
// when the observation itself failed.
func memberIsolationKey(cwd string) (key string, obs isolationObservation) {
	canonical := canonicalFilesystemPath(cwd)
	if canonical == "" {
		canonical = strings.TrimSpace(cwd)
	}
	index, obs := worktreeIsolationIndexProbe(canonical)
	switch obs {
	case isolationObserved:
		if resolved := canonicalFilesystemPath(index); resolved != "" {
			return resolved, isolationObserved
		}
		// git answered but the path did not canonicalize; treat as unobservable
		// rather than quietly downgrading to the proxy.
		return canonical, isolationUnobservable
	case isolationUnobservable:
		return canonical, isolationUnobservable
	default:
		return canonical, isolationNotACheckout
	}
}

// worktreeIsolationIndexProbeTimeout bounds the git call so a hung git cannot hang
// preparation. Exceeding it is an observation FAILURE, never a negative result.
var worktreeIsolationIndexProbeTimeout = 5 * time.Second

// worktreeIsolationIndexProbe resolves a directory's Git index path using the same
// observable doctor uses (git rev-parse --git-path index). It is a seam so tests
// can drive all three outcomes without constructing real checkouts or removing git
// from PATH.
var worktreeIsolationIndexProbe = func(dir string) (string, isolationObservation) {
	if dir == "" {
		return "", isolationNotACheckout
	}
	// A directory that does not exist yet is a clean negative: there is genuinely
	// no index to observe, which is the planned-worktree case.
	if info, err := os.Stat(dir); err != nil {
		if os.IsNotExist(err) {
			return "", isolationNotACheckout
		}
		return "", isolationUnobservable
	} else if !info.IsDir() {
		return "", isolationNotACheckout
	}
	// git absent from PATH is infrastructure failure, not a negative answer.
	if _, err := exec.LookPath("git"); err != nil {
		return "", isolationUnobservable
	}
	ctx, cancel := context.WithTimeout(context.Background(), worktreeIsolationIndexProbeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--git-path", "index")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if ctx.Err() != nil {
		return "", isolationUnobservable
	}
	if err != nil {
		// The ONE error we can interpret as a clean negative: git ran and told us
		// this is not a repository. Anything else is uninterpretable and must fail
		// closed rather than masquerade as "no index here".
		if strings.Contains(strings.ToLower(stderr.String()), "not a git repository") {
			return "", isolationNotACheckout
		}
		return "", isolationUnobservable
	}
	path := strings.TrimSpace(string(out))
	if path == "" {
		return "", isolationUnobservable
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(dir, path)
	}
	return filepath.Clean(path), isolationObserved
}
