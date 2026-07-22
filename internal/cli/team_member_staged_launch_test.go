package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/bootstrapack"
	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/liveidentity"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

func TestPreparedStagedParentTransactionHundredIterationITerm2ControlModeHarness(t *testing.T) {
	for _, binary := range []string{"codex", "claude"} {
		t.Run(binary, func(t *testing.T) {
			project, manifest, token, claim := preparedStagedProjectionFixture(t, binary)
			env, err := resolveAMQEnvForTeamLaunchProfile(project, team.DefaultProfile, "prepared", claim.Handle)
			if err != nil {
				t.Fatal(err)
			}
			agentDir := filepath.Join(absoluteAMQRoot(project, env.Root), "agents", claim.Handle)
			if err := os.MkdirAll(agentDir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(agentDir, "unrelated.keep"), []byte("operator-owned"), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("TMUX", "/tmp/harness-tmux,1,0")
			t.Setenv("TMUX_PANE", "%1")

			oldAbsent := preparedRunStagedTargetAbsent
			oldOutput := tmuxOutputCommand
			oldRun := tmuxRunCommand
			oldVerifyTarget := preparedRunStagedVerifyTarget
			oldNow := preparedRunStagedLaunchNow
			oldSleep := preparedRunStagedLaunchSleep
			oldBoundary := preparedRunStagedTopologyBoundary
			oldConsume := preparedRunStagedConsume
			t.Cleanup(func() {
				preparedRunStagedTargetAbsent = oldAbsent
				tmuxOutputCommand = oldOutput
				tmuxRunCommand = oldRun
				preparedRunStagedVerifyTarget = oldVerifyTarget
				preparedRunStagedLaunchNow = oldNow
				preparedRunStagedLaunchSleep = oldSleep
				preparedRunStagedTopologyBoundary = oldBoundary
				preparedRunStagedConsume = oldConsume
			})

			harnessNow := time.Now().UTC()
			preparedRunStagedLaunchNow = func() time.Time { return harnessNow }
			preparedRunStagedLaunchSleep = func(d time.Duration) { harnessNow = harnessNow.Add(d + time.Millisecond) }

			currentClaim := claim
			currentPane := ""
			currentWindow := ""
			currentTarget := "new-window"
			failPostflight := false
			failBoundary := ""
			focusedPane := "%1"
			livePanes := map[string]bool{"%999": true}
			maxReviewerLive := 0
			createdOwned := map[string]bool{}
			preparedRunStagedTargetAbsent = func(_, _, _, _ string) error {
				for pane := range livePanes {
					if pane != "%999" {
						return fmt.Errorf("simulated staged reviewer is still live in %s", pane)
					}
				}
				return nil
			}
			preparedRunStagedTopologyBoundary = func(stage string) error {
				if focusedPane != "%1" {
					return fmt.Errorf("launcher focus moved to %s at %s", focusedPane, stage)
				}
				if stage == failBoundary {
					return fmt.Errorf("injected %s failure", stage)
				}
				return nil
			}
			preparedRunStagedConsume = func(project, profile, session string, token preparedRunToken, role, handle string) error {
				if failBoundary == "consume" {
					return fmt.Errorf("injected consume failure")
				}
				return consumePreparedRunStagedClaim(project, profile, session, token, role, handle)
			}
			var calls []string
			stagedSubmissions := 0
			continuationSubmissions := 0
			boundaryCoverage := map[string]map[string]int{"new-window": {}, "current-window": {}}
			tmuxOutputCommand = func(name string, args ...string) (string, error) {
				call := name + " " + strings.Join(args, " ")
				calls = append(calls, call)
				switch {
				case len(args) > 0 && args[0] == "display-message" && strings.Contains(call, "#{pane_active}"):
					if focusedPane == "%1" {
						return "1\t1\n", nil
					}
					return "0\t0\n", nil
				case len(args) > 0 && args[0] == "display-message" && strings.Contains(call, "#{session_name}"):
					if strings.Contains(call, "#{window_index}") {
						return "iterm2-control-harness:0\n", nil
					}
					return "iterm2-control-harness\n", nil
				case len(args) > 0 && (args[0] == "new-window" || args[0] == "split-window"):
					currentPane = fmt.Sprintf("%%%d", 1000+len(calls))
					currentWindow = fmt.Sprintf("@%d", 1000+len(calls))
					createdOwned[currentPane] = true
					return currentPane + "\n", nil
				case len(args) > 0 && args[0] == "display-message" && strings.Contains(call, "#{window_id}"):
					return currentWindow + "\n", nil
				default:
					return "", fmt.Errorf("unexpected tmux output command: %s", call)
				}
			}
			tmuxRunCommand = func(name string, args ...string) error {
				call := name + " " + strings.Join(args, " ")
				calls = append(calls, call)
				if len(args) > 0 && args[0] == "select-pane" && len(args) > 2 && args[1] == "-t" && !containsString(args, "-T") {
					focusedPane = args[2]
				}
				if len(args) > 0 && (args[0] == "kill-window" || args[0] == "kill-pane") {
					target := args[len(args)-1]
					if !createdOwned[target] {
						return fmt.Errorf("rollback targeted unowned topology %s", target)
					}
					delete(livePanes, target)
					delete(createdOwned, target)
				}
				if len(args) > 0 && args[0] == "send-keys" && containsString(args, "C-m") {
					target := ""
					for i := range args {
						if args[i] == "-t" && i+1 < len(args) {
							target = args[i+1]
						}
					}
					if target == "%1" {
						continuationSubmissions++
						return nil
					}
					if target != currentPane {
						return fmt.Errorf("staged input targeted %s, want owned pane %s", target, currentPane)
					}
					stagedSubmissions++
					livePanes[target] = true
					reviewerLive := len(livePanes) - 1
					if reviewerLive > maxReviewerLive {
						maxReviewerLive = reviewerLive
					}
					if err := writeHarnessStagedLaunchRecord(project, manifest, token, currentClaim, currentPane, currentWindow, currentTarget, harnessNow); err != nil {
						return err
					}
				}
				return nil
			}

			preparedRunStagedVerifyTarget = func(project, profile, session, handle string) (liveidentity.Result, error) {
				if failPostflight {
					return liveidentity.Result{}, fmt.Errorf("injected exact target verification failure")
				}
				rec, agentDir, err := readHarnessStagedLaunchRecord(project, profile, session, handle)
				if err != nil {
					return liveidentity.Result{}, err
				}
				_ = agentDir
				canonicalProject, err := liveidentity.CanonicalProject(project)
				if err != nil {
					return liveidentity.Result{}, err
				}
				verified := liveidentity.Verified{
					Key:  liveidentity.Key{Project: canonicalProject, Profile: profile, Session: session, Handle: handle, PreparedGeneration: token.Generation, PreparedDigest: token.ManifestDigest, LaunchID: rec.BootstrapExpectation.LaunchID},
					Role: currentClaim.Role, Binary: currentClaim.Effective.Binary, Model: currentClaim.Effective.Model,
					PID: rec.AgentPID, WakePolicy: liveidentity.WakeDisabled, WakeMode: liveidentity.WakeDisabled,
					Terminal: liveIdentityTerminal(rec),
				}
				return liveidentity.Result{SchemaVersion: liveidentity.SchemaVersion, Verified: &verified}, nil
			}

			for iteration := 0; iteration < 100; iteration++ {
				if iteration > 0 {
					replacement, err := admitPreparedRunStagedClaim(project, team.DefaultProfile, "prepared", token, preparedRunStagedAdmissionRequest{
						Role: "qa", Handle: "qa", AuthorizingRole: "cto", AuthorizingHandle: "cto", ActorMode: team.ActorModeReview,
						SupersedesClaimID: currentClaim.ClaimID, LifecycleReason: fmt.Sprintf("iTerm2 control-mode harness iteration %d", iteration+1),
					})
					if err != nil {
						t.Fatalf("iteration %d replacement: %v", iteration+1, err)
					}
					currentClaim = replacement
				}
				target := "new-window"
				boundaryCycle := []string{"window creation", "window creation postcondition", "pane metadata", "result collection", "command barrier", "command dispatch postcondition", "postflight", "consume", "", ""}
				if iteration%2 == 1 {
					target = "current-window"
					boundaryCycle = []string{"pane creation", "pane creation postcondition", "pane metadata", "result collection", "command barrier", "command dispatch postcondition", "postflight", "consume", "", ""}
				}
				currentTarget = target
				failBoundary = boundaryCycle[(iteration/2)%len(boundaryCycle)]
				boundaryCoverage[target][failBoundary]++
				failPostflight = failBoundary == "postflight"
				request := preparedRunStagedLaunchRequest{
					Project: project, Profile: team.DefaultProfile, Session: "prepared", Role: "qa", ClaimID: currentClaim.ClaimID,
					Target: target, Layout: "vertical", Timeout: time.Millisecond,
				}
				result, err := executePreparedRunStagedLaunch(request)
				pointer, pointerErr := readPreparedRunStagedClaimPointer(preparedRunStagedClaimActivePath(project, team.DefaultProfile, "prepared", token.Generation, "qa"))
				if pointerErr != nil {
					t.Fatalf("iteration %d pointer: %v", iteration+1, pointerErr)
				}
				if failBoundary != "" {
					if err == nil || pointer.LifecycleState != stagedClaimStateAbandoned {
						t.Fatalf("iteration %d failed boundary=%s err=%v lifecycle=%s", iteration+1, failBoundary, err, pointer.LifecycleState)
					}
					assertHarnessStagedArtifactsAbsent(t, project, currentClaim.Handle)
				} else if err != nil || result.Lifecycle != stagedClaimStateConsumed || pointer.LifecycleState != stagedClaimStateConsumed {
					t.Fatalf("iteration %d result=%+v err=%v lifecycle=%s", iteration+1, result, err, pointer.LifecycleState)
				}
				if focusedPane != "%1" {
					t.Fatalf("iteration %d lost launcher keyboard focus: %q", iteration+1, focusedPane)
				}
				if err := tmuxRunCommand("tmux", "send-keys", "-l", "-t", "%1", fmt.Sprintf("continuation-%d", iteration+1)); err != nil {
					t.Fatal(err)
				}
				if err := tmuxRunCommand("tmux", "send-keys", "-t", "%1", "C-m"); err != nil {
					t.Fatal(err)
				}
				if livePanes[currentPane] {
					_, replacementErr := admitPreparedRunStagedClaim(project, team.DefaultProfile, "prepared", token, preparedRunStagedAdmissionRequest{
						Role: "qa", Handle: "qa", AuthorizingRole: "cto", AuthorizingHandle: "cto", ActorMode: team.ActorModeReview,
						SupersedesClaimID: currentClaim.ClaimID, LifecycleReason: "must refuse while prior reviewer is live",
					})
					if replacementErr == nil || !strings.Contains(replacementErr.Error(), "not absent") {
						t.Fatalf("iteration %d replacement while live error=%v", iteration+1, replacementErr)
					}
					// Model an independently verified exact stop before allowing the
					// next replacement, including the owned runtime artifacts.
					owned := preparedRunStagedOwnedTopology{Target: target, PaneID: currentPane, WindowID: currentWindow}
					if err := rollbackPreparedRunStagedTmuxTopology(owned); err != nil {
						t.Fatalf("iteration %d verified stop topology: %v", iteration+1, err)
					}
					if err := cleanupPreparedRunStagedArtifacts(request, token, currentClaim, owned); err != nil {
						t.Fatalf("iteration %d verified stop artifacts: %v", iteration+1, err)
					}
					assertHarnessStagedArtifactsAbsent(t, project, currentClaim.Handle)
				}
			}

			if stagedSubmissions == 0 || continuationSubmissions != 100 || maxReviewerLive > 1 {
				t.Fatalf("submission counts staged=%d continuation=%d max_reviewer_live=%d", stagedSubmissions, continuationSubmissions, maxReviewerLive)
			}
			if !livePanes["%999"] || len(livePanes) != 1 {
				t.Fatalf("rollback or stop touched unrelated/user topology: live=%v", livePanes)
			}
			for target, boundaries := range map[string][]string{
				"new-window":     {"window creation", "window creation postcondition", "pane metadata", "result collection", "command barrier", "command dispatch postcondition", "postflight", "consume", ""},
				"current-window": {"pane creation", "pane creation postcondition", "pane metadata", "result collection", "command barrier", "command dispatch postcondition", "postflight", "consume", ""},
			} {
				for _, boundary := range boundaries {
					if boundaryCoverage[target][boundary] == 0 {
						t.Fatalf("target %s never exercised boundary %q: %+v", target, boundary, boundaryCoverage[target])
					}
				}
			}
			joined := strings.Join(calls, "\n")
			for _, forbidden := range []string{"detach-client", "attach-session", "switch-client", "join-pane", "move-pane"} {
				if strings.Contains(joined, forbidden) {
					t.Fatalf("harness used unsafe iTerm2 control-mode transition %q", forbidden)
				}
			}
			if !strings.Contains(joined, "set-option -p -t") || !strings.Contains(joined, "@amq_squad_title amq:prepared:qa") {
				t.Fatalf("staged panes were not assigned the deterministic non-selecting discovery token:\n%s", joined)
			}
			for _, call := range calls {
				if strings.Contains(call, "tmux split-window") && !strings.Contains(call, " -d") {
					t.Fatalf("current-window staged split was focus-selecting: %s", call)
				}
				if strings.Contains(call, "tmux select-pane") && strings.Contains(call, " -T") {
					t.Fatalf("staged metadata selected a child pane: %s", call)
				}
			}
		})
	}
}

// #508 review finding B1: executePreparedRunStagedLaunch used to have no
// generation-or-claim-level lock; target-absence was checked (TOCTOU) and
// only the final consume was lock-guarded, after topology and processes
// already existed. This adversarially fires N concurrent full launches for
// the SAME claim and asserts exactly one winner ends up fully consumed with
// live owned topology, every other racer fails closed at the reservation
// gate before touching tmux, and unrelated topology is never touched.
// R1 (#508 review): runTeamMemberStagedLaunch previously never resolved the
// current-pane caller, unlike admit/replace - any process holding a claim ID
// could trigger topology mutation regardless of who was actually running the
// command. This proves the caller must be the exact actor that authorized
// the claim.
func TestTeamMemberStagedLaunchRequiresCallerToBeExactClaimAuthorizer(t *testing.T) {
	project, _, _, claim := preparedStagedProjectionFixture(t, "codex")
	old := stagedAdmissionResolveAuthorizer
	t.Cleanup(func() { stagedAdmissionResolveAuthorizer = old })

	stagedAdmissionResolveAuthorizer = func(_, profile, session string, _ team.Team) (verifiedOperatorActor, error) {
		return verifiedOperatorActor{Role: "qa", Handle: "not-the-authorizer", Profile: profile, Session: session, PaneID: "%9"}, nil
	}
	if err := runTeamMemberStagedLaunch([]string{
		claim.Role, "--claim", claim.ClaimID, "--project", project, "--session", "prepared", "--dry-run",
	}); err == nil || !strings.Contains(err.Error(), "is not the exact actor") {
		t.Fatalf("wrong-caller staged launch error = %v", err)
	}

	stagedAdmissionResolveAuthorizer = func(_, profile, session string, _ team.Team) (verifiedOperatorActor, error) {
		return verifiedOperatorActor{Role: claim.Authorizer.Role, Handle: claim.Authorizer.Handle, Profile: profile, Session: session, PaneID: "%1"}, nil
	}
	if _, _, err := captureOutput(t, func() error {
		return runTeamMemberStagedLaunch([]string{
			claim.Role, "--claim", claim.ClaimID, "--project", project, "--session", "prepared", "--dry-run",
		})
	}); err != nil {
		t.Fatalf("exact-authorizer dry-run staged launch: %v", err)
	}
}

func TestPreparedStagedLaunchConcurrentAttemptsForSameClaimYieldExactlyOneWinner(t *testing.T) {
	project, manifest, token, claim := preparedStagedProjectionFixture(t, "codex")
	t.Setenv("TMUX", "/tmp/race-tmux,1,0")
	t.Setenv("TMUX_PANE", "%1")

	oldOutput := tmuxOutputCommand
	oldRun := tmuxRunCommand
	oldVerifyTarget := preparedRunStagedVerifyTarget
	oldNow := preparedRunStagedLaunchNow
	oldSleep := preparedRunStagedLaunchSleep
	oldBoundary := preparedRunStagedTopologyBoundary
	t.Cleanup(func() {
		tmuxOutputCommand = oldOutput
		tmuxRunCommand = oldRun
		preparedRunStagedVerifyTarget = oldVerifyTarget
		preparedRunStagedLaunchNow = oldNow
		preparedRunStagedLaunchSleep = oldSleep
		preparedRunStagedTopologyBoundary = oldBoundary
	})

	var mu sync.Mutex
	harnessNow := time.Now().UTC()
	preparedRunStagedLaunchNow = func() time.Time { mu.Lock(); defer mu.Unlock(); return harnessNow }
	preparedRunStagedLaunchSleep = func(d time.Duration) { mu.Lock(); harnessNow = harnessNow.Add(d + time.Millisecond); mu.Unlock() }
	preparedRunStagedTopologyBoundary = func(string) error { return nil }

	// Only the single reservation winner ever reaches these fakes: every
	// other racer fails closed at reservePreparedRunStagedLaunchAttempt
	// before calling into the tmux backend at all, so a single shared
	// currentPane/currentWindow (mutex-guarded) is sufficient here - this is
	// not modeling concurrent topology creation, it is proving there is
	// only ever one.
	var currentPane, currentWindow string
	tmuxOutputCommand = func(name string, args ...string) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		call := name + " " + strings.Join(args, " ")
		switch {
		case len(args) > 0 && args[0] == "display-message" && strings.Contains(call, "#{pane_active}"):
			return "1\t1\n", nil
		case len(args) > 0 && args[0] == "display-message" && strings.Contains(call, "#{session_name}"):
			if strings.Contains(call, "#{window_index}") {
				return "race-harness:0\n", nil
			}
			return "race-harness\n", nil
		case len(args) > 0 && (args[0] == "new-window" || args[0] == "split-window"):
			currentPane = fmt.Sprintf("%%%d", 3000+int(harnessNow.UnixNano()%1000))
			currentWindow = fmt.Sprintf("@%d", 3000+int(harnessNow.UnixNano()%1000))
			return currentPane + "\n", nil
		case len(args) > 0 && args[0] == "display-message" && strings.Contains(call, "#{window_id}"):
			return currentWindow + "\n", nil
		default:
			return "", fmt.Errorf("unexpected tmux output command: %s", call)
		}
	}
	tmuxRunCommand = func(name string, args ...string) error {
		mu.Lock()
		defer mu.Unlock()
		if len(args) > 0 && args[0] == "send-keys" && containsString(args, "C-m") {
			target := ""
			for i := range args {
				if args[i] == "-t" && i+1 < len(args) {
					target = args[i+1]
				}
			}
			if target == "%1" {
				return nil
			}
			return writeHarnessStagedLaunchRecord(project, manifest, token, claim, currentPane, currentWindow, "new-window", harnessNow)
		}
		return nil
	}
	preparedRunStagedVerifyTarget = func(project, profile, session, handle string) (liveidentity.Result, error) {
		rec, _, err := readHarnessStagedLaunchRecord(project, profile, session, handle)
		if err != nil {
			return liveidentity.Result{}, err
		}
		canonicalProject, err := liveidentity.CanonicalProject(project)
		if err != nil {
			return liveidentity.Result{}, err
		}
		verified := liveidentity.Verified{
			Key:  liveidentity.Key{Project: canonicalProject, Profile: profile, Session: session, Handle: handle, PreparedGeneration: token.Generation, PreparedDigest: token.ManifestDigest, LaunchID: rec.BootstrapExpectation.LaunchID},
			Role: claim.Role, Binary: claim.Effective.Binary, Model: claim.Effective.Model,
			PID: rec.AgentPID, WakePolicy: liveidentity.WakeDisabled, WakeMode: liveidentity.WakeDisabled,
			Terminal: liveIdentityTerminal(rec),
		}
		return liveidentity.Result{SchemaVersion: liveidentity.SchemaVersion, Verified: &verified}, nil
	}

	const racers = 6
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make([]error, racers)
	datas := make([]stagedLaunchData, racers)
	for i := range racers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			request := preparedRunStagedLaunchRequest{
				Project: project, Profile: team.DefaultProfile, Session: "prepared", Role: claim.Role, ClaimID: claim.ClaimID,
				Target: "new-window", Layout: "vertical", Timeout: 2 * time.Second,
			}
			data, err := executePreparedRunStagedLaunch(request)
			results[i], datas[i] = err, data
		}(i)
	}
	close(start)
	wg.Wait()

	successes := 0
	var winner stagedLaunchData
	for i, err := range results {
		if err == nil {
			successes++
			winner = datas[i]
			continue
		}
		if !strings.Contains(err.Error(), "already reserved by a concurrent attempt") {
			t.Fatalf("racer %d unexpected non-reservation error: %v", i, err)
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent staged launch successes=%d want=1 (results=%v)", successes, results)
	}
	if winner.Lifecycle != stagedClaimStateConsumed || winner.PaneID == "" || winner.WindowID == "" {
		t.Fatalf("winner=%+v is not a fully consumed live topology", winner)
	}
	pointer, err := readPreparedRunStagedClaimPointer(preparedRunStagedClaimActivePath(project, team.DefaultProfile, "prepared", token.Generation, claim.Role))
	if err != nil || pointer.LifecycleState != stagedClaimStateConsumed || pointer.ClaimID != claim.ClaimID {
		t.Fatalf("pointer=%+v err=%v, want exactly the one claim consumed", pointer, err)
	}
	if _, err := os.Stat(preparedRunStagedLaunchReservationPath(project, team.DefaultProfile, "prepared", token.Generation, claim.Role, claim.ClaimID)); err != nil {
		t.Fatalf("winning reservation evidence missing: %v", err)
	}
}

func TestPreparedStagedTargetPostflightRejectsStrategyMismatchBothDirections(t *testing.T) {
	for _, tc := range []struct {
		requestTarget string
		recordTarget  string
	}{
		{requestTarget: "current-window", recordTarget: "new-window"},
		{requestTarget: "new-window", recordTarget: "current-window"},
	} {
		t.Run(tc.requestTarget+"_recorded_as_"+tc.recordTarget, func(t *testing.T) {
			project, manifest, token, claim := preparedStagedProjectionFixture(t, "codex")
			now := time.Now().UTC()
			if err := writeHarnessStagedLaunchRecord(project, manifest, token, claim, "%52", "@52", tc.recordTarget, now); err != nil {
				t.Fatal(err)
			}
			request := preparedRunStagedLaunchRequest{
				Project: project, Profile: team.DefaultProfile, Session: "prepared", Role: claim.Role, ClaimID: claim.ClaimID,
				Target: tc.requestTarget,
			}
			owned := preparedRunStagedOwnedTopology{Target: tc.requestTarget, PaneID: "%52", WindowID: "@52"}
			_, detail, ready := inspectPreparedRunStagedTarget(request, manifest, token, claim, owned)
			if ready || !strings.Contains(detail, "exact parent-created topology") {
				t.Fatalf("request=%s record=%s ready=%t detail=%q", tc.requestTarget, tc.recordTarget, ready, detail)
			}
		})
	}
}

func TestPreparedStagedTargetPostflightRejectsVerifiedStrategyMismatchBothDirections(t *testing.T) {
	for _, tc := range []struct {
		requestTarget  string
		verifiedTarget string
	}{
		{requestTarget: "current-window", verifiedTarget: "new-window"},
		{requestTarget: "new-window", verifiedTarget: "current-window"},
	} {
		t.Run(tc.requestTarget+"_verified_as_"+tc.verifiedTarget, func(t *testing.T) {
			project, manifest, token, claim := preparedStagedProjectionFixture(t, "claude")
			now := time.Now().UTC()
			if err := writeHarnessStagedLaunchRecord(project, manifest, token, claim, "%53", "@53", tc.requestTarget, now); err != nil {
				t.Fatal(err)
			}
			rec, _, err := readHarnessStagedLaunchRecord(project, team.DefaultProfile, "prepared", claim.Handle)
			if err != nil {
				t.Fatal(err)
			}
			canonicalProject, err := liveidentity.CanonicalProject(project)
			if err != nil {
				t.Fatal(err)
			}
			terminal := liveIdentityTerminal(rec)
			terminal.Target = tc.verifiedTarget
			oldVerify := preparedRunStagedVerifyTarget
			preparedRunStagedVerifyTarget = func(_, _, _, _ string) (liveidentity.Result, error) {
				verified := liveidentity.Verified{
					Key:  liveidentity.Key{Project: canonicalProject, Profile: team.DefaultProfile, Session: "prepared", Handle: claim.Handle, PreparedGeneration: token.Generation, PreparedDigest: token.ManifestDigest, LaunchID: rec.BootstrapExpectation.LaunchID},
					Role: claim.Role, Binary: claim.Effective.Binary, Model: claim.Effective.Model, PID: rec.AgentPID, Terminal: terminal,
				}
				return liveidentity.Result{SchemaVersion: liveidentity.SchemaVersion, Verified: &verified}, nil
			}
			t.Cleanup(func() { preparedRunStagedVerifyTarget = oldVerify })
			request := preparedRunStagedLaunchRequest{
				Project: project, Profile: team.DefaultProfile, Session: "prepared", Role: claim.Role, ClaimID: claim.ClaimID,
				Target: tc.requestTarget,
			}
			owned := preparedRunStagedOwnedTopology{Target: tc.requestTarget, PaneID: "%53", WindowID: "@53"}
			_, detail, ready := inspectPreparedRunStagedTarget(request, manifest, token, claim, owned)
			if ready || !strings.Contains(detail, "verified target differs") {
				t.Fatalf("request=%s verified=%s ready=%t detail=%q", tc.requestTarget, tc.verifiedTarget, ready, detail)
			}
		})
	}
}

func TestPreparedStagedRollbackRetainsEvidenceUntilExactProcessesExit(t *testing.T) {
	for _, tc := range []struct {
		name        string
		delayedExit bool
	}{
		{name: "terminate success but process remains live"},
		{name: "delayed agent and wake exit", delayedExit: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			project, manifest, token, claim := preparedStagedProjectionFixture(t, "codex")
			now := time.Now().UTC()
			request := preparedRunStagedLaunchRequest{Project: project, Profile: team.DefaultProfile, Session: "prepared", Role: claim.Role, ClaimID: claim.ClaimID, Target: "current-window"}
			owned := preparedRunStagedOwnedTopology{Target: request.Target, PaneID: "%42", WindowID: "@42"}
			if err := writeHarnessStagedLaunchRecord(project, manifest, token, claim, owned.PaneID, owned.WindowID, request.Target, now); err != nil {
				t.Fatal(err)
			}
			rec, agentDir, err := readHarnessStagedLaunchRecord(project, team.DefaultProfile, "prepared", claim.Handle)
			if err != nil {
				t.Fatal(err)
			}
			rec.AgentPID = 4242
			if err := launch.Write(agentDir, rec); err != nil {
				t.Fatal(err)
			}
			root := filepath.Dir(filepath.Dir(agentDir))
			if err := os.WriteFile(wakeLockPath(agentDir), []byte(fmt.Sprintf(`{"pid":%d,"root":%q}`, 4343, root)), 0o600); err != nil {
				t.Fatal(err)
			}

			alive := map[int]bool{4242: true, 4343: true}
			match := map[int]bool{4242: true, 4343: true}
			terminator := &recordingTerminator{}
			clock := now
			sleeps := 0
			oldDeps := preparedRunStagedCleanupDeps
			preparedRunStagedCleanupDeps = preparedRunStagedCleanupDependencies{
				Probe: downFakeProbe(alive, match), Terminator: terminator,
				Now: func() time.Time { return clock },
				Sleep: func(d time.Duration) {
					clock = clock.Add(d)
					sleeps++
					if tc.delayedExit {
						if sleeps == 1 {
							alive[4242] = false
						} else if sleeps == 2 {
							alive[4343] = false
						}
					}
				},
			}
			t.Cleanup(func() { preparedRunStagedCleanupDeps = oldDeps })

			err = cleanupPreparedRunStagedArtifacts(request, token, claim, owned)
			if !tc.delayedExit {
				if err == nil || !strings.Contains(err.Error(), "remained alive") {
					t.Fatalf("cleanup error=%v, want observed-death failure", err)
				}
				for _, path := range []string{launch.ExistingPath(agentDir), bootstrapack.Path(agentDir), wakeLockPath(agentDir)} {
					if _, statErr := os.Stat(path); statErr != nil {
						t.Fatalf("authoritative evidence removed before PID death: %s: %v", path, statErr)
					}
				}
				oldAbsent := preparedRunStagedTargetAbsent
				preparedRunStagedTargetAbsent = func(_, _, _, _ string) error {
					if alive[4242] {
						return fmt.Errorf("target is not absent: exact staged PID remains live")
					}
					return nil
				}
				t.Cleanup(func() { preparedRunStagedTargetAbsent = oldAbsent })
				_, replacementErr := admitPreparedRunStagedClaim(project, team.DefaultProfile, "prepared", token, preparedRunStagedAdmissionRequest{
					Role: claim.Role, Handle: claim.Handle, AuthorizingRole: "cto", AuthorizingHandle: "cto", ActorMode: team.ActorModeReview,
					SupersedesClaimID: claim.ClaimID, LifecycleReason: "must remain blocked until exact PID death",
				})
				if replacementErr == nil || !strings.Contains(replacementErr.Error(), "not absent") {
					t.Fatalf("replacement while terminated-but-live PID error=%v", replacementErr)
				}
				return
			}

			if err != nil {
				t.Fatalf("delayed exit cleanup: %v", err)
			}
			if len(terminator.calls) != 2 || terminator.calls[0] != 4242 || terminator.calls[1] != 4343 {
				t.Fatalf("termination calls=%v, want exact agent then wake PIDs", terminator.calls)
			}
			assertHarnessStagedArtifactsAbsent(t, project, claim.Handle)
		})
	}
}

func writeHarnessStagedLaunchRecord(project string, manifest preparedRunManifest, token preparedRunToken, claim preparedRunStagedClaim, paneID, windowID, target string, now time.Time) error {
	env, err := resolveAMQEnvForTeamLaunchProfile(project, team.DefaultProfile, "prepared", claim.Handle)
	if err != nil {
		return err
	}
	root := absoluteAMQRoot(project, env.Root)
	agentDir := filepath.Join(root, "agents", claim.Handle)
	runtimeCWD, err := canonicalDir(project)
	if err != nil {
		return err
	}
	expect, err := bootstrapack.NewExpectation(true, now)
	if err != nil {
		return err
	}
	rec := launch.Record{
		Schema: launch.SchemaVersion, CWD: runtimeCWD, TeamHome: project, TeamProfile: team.DefaultProfile,
		Session: "prepared", SharedWorkstream: true, Role: claim.Role, Handle: claim.Handle, Root: root,
		BaseRoot: root, RootSource: env.RootSource, AMQVersion: env.AMQVersion,
		AgentPID: 99999999, StartedAt: now, Tmux: &launch.TmuxInfo{Session: "iterm2-control-harness", WindowID: windowID, PaneID: paneID, Target: target},
		BootstrapExpectation: &expect, NoWakeReason: "test harness", WakeInjectMode: "raw",
	}
	applyPreparedRunStagedEffectiveIdentity(&rec, claim.Effective)
	launchToken := token
	launchToken.LaunchAttempt = claim.ClaimID
	applyPreparedRunTokenToRecord(&rec, launchToken)
	rec.Terminal = launch.TerminalInfoFromTmux(rec.Tmux)
	if err := launch.Write(agentDir, rec); err != nil {
		return err
	}
	if err := os.WriteFile(wakeLockPath(agentDir), []byte(`{"pid":0,"root":"`+root+`"}`), 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(agentDir, "unrelated.keep"), []byte("operator-owned"), 0o600); err != nil {
		return err
	}
	return bootstrapack.Write(agentDir, bootstrapack.Marker{
		LaunchID: expect.LaunchID, PromptVersion: expect.PromptVersion, AcknowledgedAt: now,
		Handle: claim.Handle, Role: claim.Role, Profile: team.DefaultProfile, Session: "prepared", Root: root,
		SkillVersion: manifest.Environment.SkillVersion, Steps: append([]string(nil), bootstrapack.RequiredSteps...),
	})
}

func assertHarnessStagedArtifactsAbsent(t *testing.T, project, handle string) {
	t.Helper()
	env, err := resolveAMQEnvForTeamLaunchProfile(project, team.DefaultProfile, "prepared", handle)
	if err != nil {
		t.Fatal(err)
	}
	agentDir := filepath.Join(absoluteAMQRoot(project, env.Root), "agents", handle)
	for _, path := range []string{launch.ExistingPath(agentDir), bootstrapack.Path(agentDir), wakeLockPath(agentDir)} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("owned failed staged artifact remains at %s: %v", path, err)
		}
	}
	if data, err := os.ReadFile(filepath.Join(agentDir, "unrelated.keep")); err != nil || string(data) != "operator-owned" {
		t.Fatalf("unrelated artifact was changed: data=%q err=%v", data, err)
	}
}

func readHarnessStagedLaunchRecord(project, profile, session, handle string) (launch.Record, string, error) {
	env, err := resolveAMQEnvForTeamLaunchProfile(project, profile, session, handle)
	if err != nil {
		return launch.Record{}, "", err
	}
	agentDir := filepath.Join(absoluteAMQRoot(project, env.Root), "agents", handle)
	rec, err := launch.Read(agentDir)
	return rec, agentDir, err
}
