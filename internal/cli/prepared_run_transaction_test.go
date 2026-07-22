package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

func preparedTransactionTestToken() preparedRunToken {
	return preparedRunToken{
		Generation: "generation-1", ManifestDigest: "sha256:manifest",
		GoalNamespace: "default/sess", GoalDigest: "sha256:goal",
	}
}

func writePreparedManagedTransactionRecord(t *testing.T, project, profile, session, role, paneID, windowID string, token preparedRunToken) (string, launch.Record) {
	t.Helper()
	tm, err := team.ReadProfile(project, profile)
	if err != nil {
		t.Fatal(err)
	}
	member, ok := memberByRole(tm, role)
	if !ok {
		t.Fatalf("missing role %s", role)
	}
	cwd, err := canonicalDir(member.EffectiveCWD(tm.Project))
	if err != nil {
		t.Fatal(err)
	}
	env, err := resolveAMQEnvForTeamLaunchProfile(cwd, profile, session, memberHandle(member))
	if err != nil {
		t.Fatal(err)
	}
	agentDir := filepath.Join(absoluteAMQRoot(cwd, env.Root), "agents", memberHandle(member))
	rec := launch.Record{
		Schema: 1, CWD: cwd, Binary: member.Binary, Role: role, Handle: memberHandle(member),
		Session: session, TeamProfile: profile,
		Tmux: &launch.TmuxInfo{PaneID: paneID, WindowID: windowID},
	}
	applyPreparedRunTokenToRecord(&rec, token)
	if err := launch.Write(agentDir, rec); err != nil {
		t.Fatal(err)
	}
	stored, err := launch.Read(agentDir)
	if err != nil {
		t.Fatal(err)
	}
	return agentDir, stored
}

func TestPreparedManagedRollbackClassifiesRecordBeforeTopologyCleanup(t *testing.T) {
	for _, tc := range []struct {
		name       string
		mutate     func(*launch.Record)
		killErr    error
		wantKill   bool
		wantRecord bool
	}{
		{name: "exact owned cleanup", wantKill: true},
		{name: "mismatch preserves pane and record", mutate: func(rec *launch.Record) { rec.Tmux.PaneID = "%99" }, wantRecord: true},
		{name: "kill failure retains exact record", killErr: fmt.Errorf("forced kill failure"), wantKill: true, wantRecord: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			project := seedTeam(t, team.Team{Members: []team.Member{{Role: "cto", Handle: "cto", Binary: "codex", Session: "sess"}}})
			token := preparedTransactionTestToken()
			agentDir, exact := writePreparedManagedTransactionRecord(t, project, team.DefaultProfile, "sess", "cto", "%2", "@1", token)
			if tc.mutate != nil {
				replacement := exact
				tc.mutate(&replacement)
				if err := launch.Write(agentDir, replacement); err != nil {
					t.Fatal(err)
				}
			}
			oldRun := tmuxRunCommand
			var kills []string
			tmuxRunCommand = func(_ string, args ...string) error {
				kills = append(kills, strings.Join(args, " "))
				return tc.killErr
			}
			t.Cleanup(func() { tmuxRunCommand = oldRun })
			err := rollbackPreparedManagedLaunchPane(project, team.DefaultProfile, "sess", "current-window", token, teamLaunchResultPane{Role: "cto", PaneID: "%2", WindowID: "@1"})
			if tc.killErr != nil {
				if err == nil || !strings.Contains(err.Error(), tc.killErr.Error()) {
					t.Fatalf("kill failure=%v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if (len(kills) > 0) != tc.wantKill {
				t.Fatalf("kills=%v wantKill=%t", kills, tc.wantKill)
			}
			stored, readErr := launch.Read(agentDir)
			if tc.wantRecord {
				if readErr != nil {
					t.Fatalf("record was not preserved: %v", readErr)
				}
				if tc.mutate != nil && (stored.Tmux == nil || stored.Tmux.PaneID != "%99") {
					t.Fatalf("replacement changed: %+v", stored)
				}
			} else if !os.IsNotExist(readErr) {
				t.Fatalf("owned record remained: %v %+v", readErr, stored)
			}
		})
	}
}

func TestPreparedManagedRollbackCleansAllExactRoleWindows(t *testing.T) {
	project := seedTeam(t, team.Team{Members: []team.Member{
		{Role: "cto", Handle: "cto", Binary: "codex", Session: "sess"},
		{Role: "qa", Handle: "qa", Binary: "codex", Session: "sess"},
	}})
	token := preparedTransactionTestToken()
	dirs := make([]string, 0, 2)
	for i, role := range []string{"cto", "qa"} {
		dir, _ := writePreparedManagedTransactionRecord(t, project, team.DefaultProfile, "sess", role, fmt.Sprintf("%%%d", i+2), fmt.Sprintf("@%d", i+2), token)
		dirs = append(dirs, dir)
	}
	oldRun := tmuxRunCommand
	var kills []string
	tmuxRunCommand = func(_ string, args ...string) error {
		kills = append(kills, strings.Join(args, " "))
		return nil
	}
	t.Cleanup(func() { tmuxRunCommand = oldRun })
	result := teamLaunchResult{Panes: []teamLaunchResultPane{
		{Role: "cto", PaneID: "%2", WindowID: "@2"},
		{Role: "qa", PaneID: "%3", WindowID: "@3"},
	}}
	if err := rollbackPreparedManagedLaunch(project, team.DefaultProfile, "sess", "new-window", token, result); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(kills, []string{"kill-window -t @2", "kill-window -t @3"}) {
		t.Fatalf("window cleanup=%v", kills)
	}
	for _, dir := range dirs {
		if _, err := launch.Read(dir); !os.IsNotExist(err) {
			t.Fatalf("owned record remains at %s: %v", dir, err)
		}
	}
}

func assertNoPreparedGoalArtifacts(t *testing.T, project, profile, session string) {
	t.Helper()
	for _, dir := range []string{goalAttemptDir(project, profile, session), deliveryReceiptDir(project, profile, session)} {
		entries, err := os.ReadDir(dir)
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if strings.HasSuffix(entry.Name(), ".json") {
				t.Fatalf("prepared mismatch left goal attempt or receipt %s in %s", entry.Name(), dir)
			}
		}
	}
}

func TestPreparedGoalAtomicAdmissionRejectsBoundaryIdentityMutationWithoutArtifacts(t *testing.T) {
	for _, tc := range []struct {
		name                string
		mutate              func(*testing.T, string, string, preparedRunManifest) *launch.Record
		want                string
		wantRecordPreserved bool
	}{
		{
			name: "profile-member",
			mutate: func(t *testing.T, project, _ string, manifest preparedRunManifest) *launch.Record {
				tm, err := team.ReadProfile(project, team.DefaultProfile)
				if err != nil {
					t.Fatal(err)
				}
				for i := range tm.Members {
					if tm.Members[i].Role == "cto" {
						if manifest.Members["cto"].ActorMode == team.ActorModeImplementation {
							tm.Members[i].ActorMode = team.ActorModeReview
						} else {
							tm.Members[i].ActorMode = team.ActorModeImplementation
						}
					}
				}
				if err := team.WriteProfileUnderLock(project, team.DefaultProfile, tm); err != nil {
					t.Fatal(err)
				}
				return nil
			},
			want: "current lead member identity differs",
		},
		{
			name: "launch-record-goal-binding",
			mutate: func(t *testing.T, _ string, agentDir string, _ preparedRunManifest) *launch.Record {
				rec, err := launch.Read(agentDir)
				if err != nil {
					t.Fatal(err)
				}
				changed := *rec.GoalBinding
				changed.DeliveryState = goalBindingDeliveryDelivered
				rec.GoalBinding = &changed
				rec.Conversation = "concurrent-boundary-mutation"
				if err := goalLaunchWriteUnderRecordLock(agentDir, rec); err != nil {
					t.Fatal(err)
				}
				return &rec
			},
			want:                "not the exact pinned prepared binding",
			wantRecordPreserved: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			project := seedTeam(t, team.Team{Orchestrated: true, Lead: "cto", Members: []team.Member{
				{Role: "cto", Handle: "cto", Binary: "codex", Session: "sess"},
				{Role: "qa", Handle: "qa", Binary: "codex", Session: "sess"},
			}})
			useFakeTmuxBackend(t)
			stubCurrentRunStartPane(t, "%42")
			stubRunStartLeadWake(t)
			args := prepareRunStartTestInvocation(t, []string{"-p", project, "-s", "sess", "--external-lead", "--go"}, true)
			manifest, _, err := readPreparedRunManifestSnapshot(project, team.DefaultProfile, "sess")
			if err != nil {
				t.Fatal(err)
			}
			agentDir := filepath.Join(squadnamespace.AMQRoot(project, team.DefaultProfile, "sess"), "agents", "cto")
			oldReady := runStartLeadReadyCheck
			runStartLeadReadyCheck = func(string, string, string, string) (runStartLeadReadiness, error) {
				return runStartLeadReadiness{Ready: true}, nil
			}
			t.Cleanup(func() { runStartLeadReadyCheck = oldReady })
			var mutated *launch.Record
			oldBoundary := preparedGoalAdmissionBeforeClaim
			preparedGoalAdmissionBeforeClaim = func() error {
				mutated = tc.mutate(t, project, agentDir, manifest)
				return nil
			}
			t.Cleanup(func() { preparedGoalAdmissionBeforeClaim = oldBoundary })
			oldSend := sendPromptToPane
			paneSends := 0
			sendPromptToPane = func(string, string) error { paneSends++; return nil }
			t.Cleanup(func() { sendPromptToPane = oldSend })
			oldFallback := goalFallbackAMQSend
			amqSends := 0
			goalFallbackAMQSend = func(goalDeliveryOptions) (goalFallbackDelivery, error) {
				amqSends++
				return goalFallbackDelivery{}, nil
			}
			t.Cleanup(func() { goalFallbackAMQSend = oldFallback })

			_, _, err = captureOutput(t, func() error { return runRunStart(args, "test") })
			if err == nil || !isPreparedRunIdentityMismatch(err) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("boundary identity mutation error=%v want=%q", err, tc.want)
			}
			if _, statErr := os.Stat(preparedRunGoalEventPath(project, team.DefaultProfile, "sess", manifest.Generation)); !os.IsNotExist(statErr) {
				t.Fatalf("boundary mutation created prepared goal event: %v", statErr)
			}
			assertNoPreparedGoalArtifacts(t, project, team.DefaultProfile, "sess")
			if paneSends != 0 || amqSends != 0 {
				t.Fatalf("boundary mutation reached pane=%d amq=%d side effects", paneSends, amqSends)
			}
			stored, readErr := launch.Read(agentDir)
			if tc.wantRecordPreserved {
				if readErr != nil || mutated == nil || !reflect.DeepEqual(stored, *mutated) {
					t.Fatalf("concurrent record mutation was not preserved: err=%v got=%+v want=%+v", readErr, stored, mutated)
				}
			} else if !os.IsNotExist(readErr) {
				t.Fatalf("owned record survived failed transaction: err=%v record=%+v", readErr, stored)
			}
		})
	}
}

func TestPreparedGoalAtomicAdmissionCompletesBeforeOrdinaryWriterEnters(t *testing.T) {
	project, manifest, token := reservePreparedRunStateFixture(t)
	if err := consumePreparedRunMember(project, team.DefaultProfile, "prepared", token, "cto", "cto"); err != nil {
		t.Fatal(err)
	}
	tm, err := team.ReadProfile(project, team.DefaultProfile)
	if err != nil {
		t.Fatal(err)
	}
	member, ok := memberByRole(tm, "cto")
	if !ok {
		t.Fatal("missing prepared lead")
	}
	binding, err := preparedGoalBinding(tm, team.DefaultProfile, "prepared", member, acceptedGoalBinding{
		Text: manifest.GoalText, Source: manifest.GoalSource, Namespace: manifest.GoalNamespace, Digest: manifest.GoalDigest,
	})
	if err != nil {
		t.Fatal(err)
	}
	agentDir, rec := writePreparedManagedTransactionRecord(t, project, team.DefaultProfile, "prepared", "cto", "%42", "@1", token)
	rec.GoalBinding = binding
	if err := launch.Write(agentDir, rec); err != nil {
		t.Fatal(err)
	}
	opts, err := resolveGoalDeliveryOptions(project, team.DefaultProfile, "prepared", "cto", manifest.GoalText, true, true, true, "goal start", namespaceConflictOverrideOptions{})
	if err != nil {
		t.Fatal(err)
	}
	opts.PreparedRunToken = token
	contract, err := goalDeliveryContractForBinary(opts.Member.Binary)
	if err != nil {
		t.Fatal(err)
	}
	receipt := newDeliveryReceipt(opts.Project, opts.Profile, opts.Session, opts.Role, opts.Member.Handle, opts.Mode, contract.Mode)
	applyPreparedRunTokenToReceipt(&receipt, token)
	opts.AttemptID = receipt.AttemptID
	prompt := contract.prompt(opts.Goal, opts.Team, opts.Profile, opts.Session, opts.Role, receipt.AttemptID)
	mr, workstream, err := resolveMemberRuntime(opts.Project, opts.Profile, opts.Session, true, opts.Role)
	if err != nil {
		t.Fatal(err)
	}

	writerStarted := make(chan struct{})
	writerDone := make(chan error, 1)
	oldBoundary := preparedGoalAdmissionBeforeClaim
	preparedGoalAdmissionBeforeClaim = func() error {
		go func() {
			close(writerStarted)
			writerDone <- team.WithProfileLock(project, team.DefaultProfile, func() error {
				if _, err := os.Stat(preparedRunGoalEventPath(project, team.DefaultProfile, "prepared", token.Generation)); err != nil {
					return fmt.Errorf("ordinary writer entered before prepared goal claim: %w", err)
				}
				attemptPath, err := goalAttemptPath(project, team.DefaultProfile, "prepared", receipt.AttemptID)
				if err != nil {
					return err
				}
				if _, err := os.Stat(attemptPath); err != nil {
					return fmt.Errorf("ordinary writer entered before goal attempt reservation: %w", err)
				}
				stored, err := launch.Read(agentDir)
				if err != nil {
					return err
				}
				if !exactGoalBinding(stored.GoalBinding, contract, opts.Goal, receipt.AttemptID, prompt, "goal-control") {
					return fmt.Errorf("ordinary writer entered before exact launch binding reservation")
				}
				current, err := team.ReadProfile(project, team.DefaultProfile)
				if err != nil {
					return err
				}
				for i := range current.Members {
					if current.Members[i].Role == "cto" {
						current.Members[i].ActorMode = team.ActorModeReview
					}
				}
				return team.WriteProfileUnderLock(project, team.DefaultProfile, current)
			})
		}()
		<-writerStarted
		return nil
	}
	t.Cleanup(func() { preparedGoalAdmissionBeforeClaim = oldBoundary })

	reservation, err := admitPreparedGoalClaim(&opts, contract, &receipt, &prompt, mr, workstream, nil)
	if err != nil {
		t.Fatalf("prepared admission: %v", err)
	}
	if err := <-writerDone; err != nil {
		t.Fatal(err)
	}
	if reservation.AttemptPath == "" || !sameFilesystemPath(reservation.Runtime.AgentDir, agentDir) {
		t.Fatalf("incomplete prepared reservation: %+v", reservation)
	}
}

func TestManagedPreGoalManifestLossRollsBackExactTransaction(t *testing.T) {
	project := seedTeam(t, team.Team{Orchestrated: true, Lead: "cto", Members: []team.Member{
		{Role: "cto", Handle: "cto", Binary: "codex", Session: "sess"},
		{Role: "qa", Handle: "qa", Binary: "codex", Session: "sess"},
	}})
	useFakeTmuxBackend(t)
	args := prepareRunStartTestInvocation(t, []string{"-p", project, "-s", "sess", "--visibility", "detached", "--go"}, true)
	oldReady := runStartLeadReadyCheck
	runStartLeadReadyCheck = func(string, string, string, string) (runStartLeadReadiness, error) {
		return runStartLeadReadiness{Ready: true}, nil
	}
	t.Cleanup(func() { runStartLeadReadyCheck = oldReady })
	var agentDirs []string
	oldBeforeGoal := runStartBeforePinnedGoalDelivery
	runStartBeforePinnedGoalDelivery = func(opts runStartGoalDeliveryOptions) error {
		for i, role := range []string{"cto", "qa"} {
			agentDir, _ := writePreparedManagedTransactionRecord(t, project, team.DefaultProfile, "sess", role, fmt.Sprintf("%%%d", i+1), "@1", opts.PreparedRunToken)
			agentDirs = append(agentDirs, agentDir)
		}
		if err := os.Remove(preparedRunPath(project, team.DefaultProfile, "sess")); err != nil {
			t.Fatalf("remove prepared manifest: %v", err)
		}
		return nil
	}
	t.Cleanup(func() { runStartBeforePinnedGoalDelivery = oldBeforeGoal })
	oldRun := tmuxRunCommand
	var kills []string
	tmuxRunCommand = func(_ string, args ...string) error {
		kills = append(kills, strings.Join(args, " "))
		return nil
	}
	t.Cleanup(func() { tmuxRunCommand = oldRun })
	oldSend := sendPromptToPane
	sends := 0
	sendPromptToPane = func(string, string) error { sends++; return nil }
	t.Cleanup(func() { sendPromptToPane = oldSend })

	_, _, err := captureOutput(t, func() error { return runRunStart(args, "test") })
	if err == nil || !isPreparedRunIdentityMismatch(err) || !strings.Contains(err.Error(), "identity disappeared") {
		t.Fatalf("managed manifest loss=%v", err)
	}
	if !reflect.DeepEqual(kills, []string{"kill-pane -t %1", "kill-pane -t %2"}) {
		t.Fatalf("managed cleanup=%v", kills)
	}
	for _, agentDir := range agentDirs {
		if _, err := launch.Read(agentDir); !os.IsNotExist(err) {
			t.Fatalf("managed record remains at %s: %v", agentDir, err)
		}
	}
	if sends != 0 {
		t.Fatalf("managed manifest loss reached pane delivery %d time(s)", sends)
	}
	assertNoPreparedGoalArtifacts(t, project, team.DefaultProfile, "sess")
}

func TestExternalPreGoalManifestLossUsesRecordAndWakeCAS(t *testing.T) {
	for _, tc := range []struct {
		name               string
		replaceExternal    bool
		wantExternalRecord bool
		wantWakeSignals    bool
	}{
		{name: "exact transaction cleanup", wantWakeSignals: true},
		{name: "concurrent replacement preserves record pane and wake", replaceExternal: true, wantExternalRecord: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			project := seedTeam(t, team.Team{Orchestrated: true, Lead: "cto", Members: []team.Member{
				{Role: "cto", Handle: "cto", Binary: "codex", Session: "sess"},
				{Role: "qa", Handle: "qa", Binary: "codex", Session: "sess"},
			}})
			useFakeTmuxBackend(t)
			stubCurrentRunStartPane(t, "%42")
			oldWake, oldSignal := leadWakeStarter, externalLeadWakeProcessGroupSignal
			leadWakeStarter = func(leadWakeOptions) (leadWakeResult, error) {
				return leadWakeResult{PID: 2222, Started: true, Detail: "ready"}, nil
			}
			var wakeSignals []syscall.Signal
			externalLeadWakeProcessGroupSignal = func(pgid int, signal syscall.Signal) error {
				if pgid != 2222 {
					t.Fatalf("wake pgid=%d", pgid)
				}
				wakeSignals = append(wakeSignals, signal)
				if signal == 0 {
					return syscall.ESRCH
				}
				return nil
			}
			t.Cleanup(func() { leadWakeStarter, externalLeadWakeProcessGroupSignal = oldWake, oldSignal })
			args := prepareRunStartTestInvocation(t, []string{"-p", project, "-s", "sess", "--external-lead", "--go"}, true)
			oldReady := runStartLeadReadyCheck
			runStartLeadReadyCheck = func(string, string, string, string) (runStartLeadReadiness, error) {
				return runStartLeadReadiness{Ready: true}, nil
			}
			t.Cleanup(func() { runStartLeadReadyCheck = oldReady })
			var workerDir, externalDir string
			var replacement launch.Record
			oldBeforeGoal := runStartBeforePinnedGoalDelivery
			runStartBeforePinnedGoalDelivery = func(opts runStartGoalDeliveryOptions) error {
				workerDir, _ = writePreparedManagedTransactionRecord(t, project, team.DefaultProfile, "sess", "qa", "%1", "@1", opts.PreparedRunToken)
				externalDir = filepath.Join(squadnamespace.AMQRoot(project, team.DefaultProfile, "sess"), "agents", "cto")
				if tc.replaceExternal {
					current, err := launch.Read(externalDir)
					if err != nil {
						return err
					}
					replacement = current
					replacement.Conversation = "concurrent-replacement"
					if err := launch.Write(externalDir, replacement); err != nil {
						return err
					}
				}
				if err := os.Remove(preparedRunPath(project, team.DefaultProfile, "sess")); err != nil {
					t.Fatalf("remove prepared manifest: %v", err)
				}
				return nil
			}
			t.Cleanup(func() { runStartBeforePinnedGoalDelivery = oldBeforeGoal })
			oldRun := tmuxRunCommand
			var kills []string
			tmuxRunCommand = func(_ string, args ...string) error {
				kills = append(kills, strings.Join(args, " "))
				return nil
			}
			t.Cleanup(func() { tmuxRunCommand = oldRun })
			oldSend := sendPromptToPane
			sends := 0
			sendPromptToPane = func(string, string) error { sends++; return nil }
			t.Cleanup(func() { sendPromptToPane = oldSend })

			_, _, err := captureOutput(t, func() error { return runRunStart(args, "test") })
			if err == nil || !isPreparedRunIdentityMismatch(err) {
				t.Fatalf("external manifest loss=%v", err)
			}
			if !reflect.DeepEqual(kills, []string{"kill-window -t @1"}) {
				t.Fatalf("external worker cleanup=%v", kills)
			}
			if _, err := launch.Read(workerDir); !os.IsNotExist(err) {
				t.Fatalf("external worker record remains: %v", err)
			}
			stored, readErr := launch.Read(externalDir)
			if tc.wantExternalRecord {
				if readErr != nil || !reflect.DeepEqual(stored, replacement) {
					t.Fatalf("external replacement err=%v got=%+v want=%+v", readErr, stored, replacement)
				}
			} else if !os.IsNotExist(readErr) {
				t.Fatalf("owned external record remains: %v %+v", readErr, stored)
			}
			if tc.wantWakeSignals {
				if !reflect.DeepEqual(wakeSignals, []syscall.Signal{syscall.SIGTERM, 0}) {
					t.Fatalf("owned wake signals=%v", wakeSignals)
				}
			} else if len(wakeSignals) != 0 {
				t.Fatalf("replacement wake signaled: %v", wakeSignals)
			}
			if sends != 0 {
				t.Fatalf("external manifest loss reached pane delivery %d time(s)", sends)
			}
			assertNoPreparedGoalArtifacts(t, project, team.DefaultProfile, "sess")
		})
	}
}

func TestPreparedExternalRecordLookupIgnoresCallerCWDForRelativeRoot(t *testing.T) {
	project := t.TempDir()
	if err := team.Write(project, team.Team{
		Orchestrated: true,
		Lead:         "cto",
		Members: []team.Member{{
			Role: "cto", Handle: "cto", Binary: "codex", Session: "sess",
		}},
	}); err != nil {
		t.Fatal(err)
	}
	chdir(t, project)
	stubCurrentRunStartPane(t, "%42")
	stubRunStartLeadWake(t)
	prepareRunStartTestInvocation(t, []string{
		"--project", project,
		"--session", "sess",
		"--external-lead",
		"--visibility", "detached",
		"--go",
	}, true)
	manifest, digest, err := readPreparedRunManifestSnapshot(project, team.DefaultProfile, "sess")
	if err != nil {
		t.Fatal(err)
	}
	token := preparedRunTokenFromSnapshot(manifest, digest)
	launchAttempt, err := reservePreparedRunLaunch(project, team.DefaultProfile, "sess", token)
	if err != nil {
		t.Fatal(err)
	}
	token.LaunchAttempt = launchAttempt
	if err := runLeadRegisterWithPreparedToken([]string{
		"--project", project,
		"--session", "sess",
		"--role", "cto",
		"--adopt-project-lead",
	}, token); err != nil {
		t.Fatalf("register prepared external lead: %v", err)
	}

	env, err := resolveAMQEnvForTeamLaunchProfile(project, team.DefaultProfile, "sess", "cto")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.IsAbs(env.Root) {
		t.Fatalf("fixture requires a relative resolved root, got %q", env.Root)
	}
	memberAgentDir := filepath.Join(absoluteAMQRoot(project, env.Root), "agents", "cto")
	memberRecord, err := launch.Read(memberAgentDir)
	if err != nil {
		t.Fatal(err)
	}

	callerCWD := t.TempDir()
	callerAgentDir := filepath.Join(absoluteAMQRoot(callerCWD, env.Root), "agents", "cto")
	decoy := memberRecord
	decoy.Root = absoluteAMQRoot(callerCWD, env.Root)
	decoy.Conversation = "caller-root-decoy"
	if err := launch.Write(callerAgentDir, decoy); err != nil {
		t.Fatal(err)
	}
	chdir(t, callerCWD)

	snapshotDir, snapshot, err := preparedExternalLeadRecordSnapshot(project, team.DefaultProfile, "sess", "cto")
	if err != nil {
		t.Fatal(err)
	}
	if !sameFilesystemPath(snapshotDir, memberAgentDir) || snapshot == nil || snapshot.Conversation == decoy.Conversation {
		t.Fatalf("snapshot selected caller root: dir=%q record=%+v want member dir %q", snapshotDir, snapshot, memberAgentDir)
	}
	if err := validatePreparedExternalLeadStoredBeforeWorkerSpawn(project, team.DefaultProfile, "sess", "cto", token); err != nil {
		t.Fatalf("pre-worker validation selected caller root: %v", err)
	}
}
