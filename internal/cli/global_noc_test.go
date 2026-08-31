package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/tmuxpane"
)

const testGlobalNOCPID = 4242

func installActiveGlobalNOC(t *testing.T, root string) globalNOCLaunch {
	t.Helper()
	now := time.Unix(1_700_000_000, 0).UTC()
	id := globalNOCLaunchID(root, now)
	identity := &tmuxpane.PaneIdentity{
		Session: "tmux-main", WindowID: "@9", WindowName: "noc", PaneID: "%90",
	}
	backstop := globalNOCBackstop{IntervalSeconds: 30, TimeoutSeconds: 1800, MaxTicks: 60}
	launchRecord, err := beginGlobalNOCLaunch(root, id, "codex", "gpt-5", testGlobalNOCPID, identity, "sha256:bootstrap", backstop, now)
	if err != nil {
		t.Fatalf("begin NOC launch: %v", err)
	}
	if err := transitionGlobalNOCLaunch(root, id, globalNOCLaunchActive, "ready", now.Add(time.Second)); err != nil {
		t.Fatalf("activate NOC launch: %v", err)
	}
	launchRecord.State = globalNOCLaunchActive
	launchRecord.Detail = "ready"
	launchRecord.UpdatedAt = now.Add(time.Second)
	return launchRecord
}

func stubVerifiedGlobalNOCPane(t *testing.T, launchRecord globalNOCLaunch) {
	t.Helper()
	oldCurrent := globalNOCCurrentPaneIdentity
	oldInspector := statusPaneInspector
	oldProbe := defaultDuplicateLaunchProbe
	globalNOCCurrentPaneIdentity = func() (*tmuxpane.PaneIdentity, error) {
		tmux := launchRecord.Record.Tmux
		return &tmuxpane.PaneIdentity{
			Session: tmux.Session, WindowID: tmux.WindowID,
			WindowName: tmux.WindowName, PaneID: tmux.PaneID,
		}, nil
	}
	statusPaneInspector = func(id string) (tmuxpane.TmuxPane, bool) {
		return tmuxpane.TmuxPane{Pane: id, Title: paneTitleToken(launchRecord.Record.Session, launchRecord.Record.Role)}, id == launchRecord.Record.Tmux.PaneID
	}
	defaultDuplicateLaunchProbe = duplicateLaunchProbe{
		PIDAlive: func(pid int) bool { return pid == launchRecord.Record.AgentPID },
		ProcessMatch: func(pid int, predicate func(string) bool) bool {
			return pid == launchRecord.Record.AgentPID && predicate(launchRecord.Record.Binary)
		},
		ProcessTTY: func(int) (string, bool) { return "", false },
		ProcessStartTime: func(pid int) (time.Time, bool) {
			return launchRecord.Record.StartedAt, pid == launchRecord.Record.AgentPID
		},
		Now: time.Now,
	}
	t.Cleanup(func() {
		globalNOCCurrentPaneIdentity = oldCurrent
		statusPaneInspector = oldInspector
		defaultDuplicateLaunchProbe = oldProbe
	})
}

func TestGlobalNOCDetectionRequiresExactStampedCurrentPane(t *testing.T) {
	root := t.TempDir()
	launchRecord := installActiveGlobalNOC(t, root)
	stubVerifiedGlobalNOCPane(t, launchRecord)

	context, err := detectRegisteredGlobalNOC(root)
	if err != nil {
		t.Fatalf("detect registered NOC: %v", err)
	}
	if context == nil || context.Launch.ID != launchRecord.ID {
		t.Fatalf("verified context = %+v", context)
	}

	globalNOCCurrentPaneIdentity = func() (*tmuxpane.PaneIdentity, error) {
		return &tmuxpane.PaneIdentity{Session: "tmux-main", WindowID: "@9", PaneID: "%other"}, nil
	}
	context, err = detectRegisteredGlobalNOC(root)
	if err != nil || context != nil {
		t.Fatalf("mismatched current pane context=%+v err=%v", context, err)
	}

	globalNOCCurrentPaneIdentity = func() (*tmuxpane.PaneIdentity, error) {
		tmux := launchRecord.Record.Tmux
		return &tmuxpane.PaneIdentity{Session: tmux.Session, WindowID: tmux.WindowID, PaneID: tmux.PaneID}, nil
	}
	statusPaneInspector = func(id string) (tmuxpane.TmuxPane, bool) {
		return tmuxpane.TmuxPane{Pane: id, Title: "unverified"}, true
	}
	context, err = detectRegisteredGlobalNOC(root)
	if err == nil || context != nil || !strings.Contains(err.Error(), "canonical runtime identity is unverified") {
		t.Fatalf("unstamped pane context=%+v err=%v", context, err)
	}
}

func TestGlobalNOCDetectionRejectsStampedPaneWithoutLivePID(t *testing.T) {
	root := t.TempDir()
	launchRecord := installActiveGlobalNOC(t, root)
	stubVerifiedGlobalNOCPane(t, launchRecord)
	defaultDuplicateLaunchProbe.PIDAlive = func(int) bool { return false }

	context, err := detectRegisteredGlobalNOC(root)
	if err == nil || context != nil || !strings.Contains(err.Error(), "canonical runtime identity is unverified") {
		t.Fatalf("title-only NOC context=%+v err=%v", context, err)
	}
}

func TestWaitForGlobalNOCPIDIdentityRejectsPaneOnlyLiveness(t *testing.T) {
	oldIdentity := globalNOCRuntimeIdentity
	oldTimeout := globalNOCProcessWaitTimeout
	globalNOCRuntimeIdentity = func(launch.Record, string) launchRuntimeIdentity {
		return launchRuntimeIdentity{Live: true, PaneLive: true}
	}
	globalNOCProcessWaitTimeout = 0
	t.Cleanup(func() {
		globalNOCRuntimeIdentity = oldIdentity
		globalNOCProcessWaitTimeout = oldTimeout
	})

	err := waitForGlobalNOCPIDIdentity(launch.Record{AgentPID: 4242, Binary: "codex"})
	if err == nil || !strings.Contains(err.Error(), "did not establish canonical codex runtime identity") {
		t.Fatalf("pane-only activation wait error = %v", err)
	}
}

func TestGlobalNOCRegistrySupersedesGenerationAndTracksPollingRun(t *testing.T) {
	root := t.TempDir()
	first := installActiveGlobalNOC(t, root)
	now := first.UpdatedAt.Add(time.Second)
	secondID := globalNOCLaunchID(root, now)
	secondIdentity := &tmuxpane.PaneIdentity{Session: "tmux-main", WindowID: "@10", WindowName: "noc-2", PaneID: "%91"}
	backstop := globalNOCBackstop{IntervalSeconds: 45, TimeoutSeconds: 900, MaxTicks: 20}
	oldInspector := statusPaneInspector
	oldExactInspection := globalNOCPaneInspection
	statusPaneInspector = func(id string) (tmuxpane.TmuxPane, bool) {
		return tmuxpane.TmuxPane{Pane: id, Title: paneTitleToken(first.Record.Session, first.Record.Role)}, id == first.Record.Tmux.PaneID
	}
	globalNOCPaneInspection = func(id string) tmuxpane.PaneInspection {
		return tmuxpane.PaneInspection{State: tmuxpane.PaneInspectionFound, Pane: tmuxpane.TmuxPane{Pane: id}}
	}
	if _, err := beginGlobalNOCLaunch(root, secondID, "claude", "", 4343, secondIdentity, "sha256:second", backstop, now); err == nil || !strings.Contains(err.Error(), "cannot be replaced") {
		t.Fatalf("live NOC replacement error = %v", err)
	}
	statusPaneInspector = func(string) (tmuxpane.TmuxPane, bool) { return tmuxpane.TmuxPane{}, false }
	globalNOCPaneInspection = func(id string) tmuxpane.PaneInspection {
		return tmuxpane.PaneInspection{State: tmuxpane.PaneInspectionGone, Detail: "no such pane " + id}
	}
	t.Cleanup(func() {
		statusPaneInspector = oldInspector
		globalNOCPaneInspection = oldExactInspection
	})
	second, err := beginGlobalNOCLaunch(root, secondID, "claude", "", 4343, secondIdentity, "sha256:second", backstop, now)
	if err != nil {
		t.Fatalf("begin second NOC: %v", err)
	}
	if err := transitionGlobalNOCLaunch(root, second.ID, globalNOCLaunchActive, "ready", now.Add(time.Second)); err != nil {
		t.Fatalf("activate second NOC: %v", err)
	}
	second.State = globalNOCLaunchActive
	second.UpdatedAt = now.Add(time.Second)
	context := &globalNOCContext{ControlRoot: root, Launch: second}
	run, err := beginGlobalNOCRun(context, filepath.Join(root, "project"), "release", "v2", "cto", "registered_noc_default", now.Add(2*time.Second))
	if err != nil {
		t.Fatalf("begin NOC run: %v", err)
	}
	if err := finishGlobalNOCRun(context, run.ID, globalNOCRunPollRequired, "wake unavailable", nil, now.Add(3*time.Second)); err != nil {
		t.Fatalf("finish NOC run: %v", err)
	}
	registry, err := readGlobalNOCRegistry(root)
	if err != nil {
		t.Fatalf("read registry: %v", err)
	}
	if len(registry.Launches) != 2 || registry.Launches[0].State != globalNOCLaunchStopped || registry.Launches[1].State != globalNOCLaunchActive {
		t.Fatalf("launch generations = %+v", registry.Launches)
	}
	if len(registry.Runs) != 1 || registry.Runs[0].State != globalNOCRunPollRequired || registry.Runs[0].NOCLaunchID != second.ID {
		t.Fatalf("run registrations = %+v", registry.Runs)
	}
}

func TestWriteGlobalNOCRegistryRejectsDuplicateRunIDsWithoutChangingBytes(t *testing.T) {
	for _, position := range []string{"prepend", "append"} {
		t.Run(position, func(t *testing.T) {
			root := t.TempDir()
			launchRecord := installActiveGlobalNOC(t, root)
			now := launchRecord.UpdatedAt.Add(time.Second)
			context := &globalNOCContext{ControlRoot: root, Launch: launchRecord}
			run, err := beginGlobalNOCRun(context, filepath.Join(root, "project"), "release", "v2", "cto", "registered_noc_default", now)
			if err != nil {
				t.Fatalf("begin NOC run: %v", err)
			}
			registryPath := filepath.Join(root, ".amq-squad", globalNOCRegistryDir, globalNOCRegistryFile)
			before, err := os.ReadFile(registryPath)
			if err != nil {
				t.Fatal(err)
			}
			err = writeGlobalNOCRegistry(root, func(registry *globalNOCRegistry) error {
				duplicate := registry.Runs[0]
				duplicate.Detail = "forged duplicate"
				duplicate.UpdatedAt = duplicate.UpdatedAt.Add(time.Second)
				if position == "prepend" {
					registry.Runs = append([]globalNOCRun{duplicate}, registry.Runs...)
				} else {
					registry.Runs = append(registry.Runs, duplicate)
				}
				return nil
			})
			if err == nil || !strings.Contains(err.Error(), "duplicate run registrations") || !strings.Contains(err.Error(), run.ID) {
				t.Fatalf("duplicate write error = %v", err)
			}
			after, readErr := os.ReadFile(registryPath)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if !bytes.Equal(after, before) {
				t.Fatal("duplicate-ID rejection changed registry bytes")
			}
		})
	}
}

func TestGlobalNOCRegistryRearmsPreparedGenerationAfterExactPaneGone(t *testing.T) {
	root := t.TempDir()
	now := time.Unix(1_700_000_000, 0).UTC()
	backstop := globalNOCBackstop{IntervalSeconds: 30, TimeoutSeconds: 1800, MaxTicks: 60}
	firstIdentity := &tmuxpane.PaneIdentity{Session: "tmux-main", WindowID: "@9", WindowName: "noc", PaneID: "%90"}
	first, err := beginGlobalNOCLaunch(root, "noc-first", "codex", "", 4242, firstIdentity, "sha256:first", backstop, now)
	if err != nil {
		t.Fatalf("begin prepared NOC: %v", err)
	}
	oldExactInspection := globalNOCPaneInspection
	globalNOCPaneInspection = func(id string) tmuxpane.PaneInspection {
		return tmuxpane.PaneInspection{State: tmuxpane.PaneInspectionGone, Detail: "no such pane " + id}
	}
	t.Cleanup(func() { globalNOCPaneInspection = oldExactInspection })

	secondIdentity := &tmuxpane.PaneIdentity{Session: "tmux-main", WindowID: "@10", WindowName: "noc-2", PaneID: "%91"}
	second, err := beginGlobalNOCLaunch(root, "noc-second", "claude", "", 4343, secondIdentity, "sha256:second", backstop, now.Add(time.Second))
	if err != nil {
		t.Fatalf("re-arm prepared NOC: %v", err)
	}
	registry, err := readGlobalNOCRegistry(root)
	if err != nil {
		t.Fatalf("read re-armed registry: %v", err)
	}
	if len(registry.Launches) != 2 || registry.Launches[0].ID != first.ID ||
		registry.Launches[0].State != globalNOCLaunchStopped ||
		!strings.Contains(registry.Launches[0].Detail, "re-armed") ||
		registry.Launches[1].ID != second.ID ||
		registry.Launches[1].State != globalNOCLaunchPrepared {
		t.Fatalf("re-armed launch generations = %+v", registry.Launches)
	}
}

func TestGlobalNOCRegistryRejectsSymlinkedMetadataDirectory(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, ".amq-squad")); err != nil {
		t.Fatal(err)
	}
	err := writeGlobalNOCRegistry(root, func(*globalNOCRegistry) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "non-symlink directory") {
		t.Fatalf("symlink registry error = %v", err)
	}
}

func TestGlobalNOCRegistryRejectsControlRootSymlinkAuthoritySwap(t *testing.T) {
	parent := t.TempDir()
	firstRoot := filepath.Join(parent, "first")
	secondRoot := filepath.Join(parent, "second")
	link := filepath.Join(parent, "control")
	if err := os.Mkdir(firstRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(secondRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(firstRoot, link); err != nil {
		t.Fatal(err)
	}
	installActiveGlobalNOC(t, link)
	body, err := os.ReadFile(globalNOCRegistryPath(firstRoot))
	if err != nil {
		t.Fatal(err)
	}
	secondRegistryDir := filepath.Dir(globalNOCRegistryPath(secondRoot))
	if err := os.MkdirAll(secondRegistryDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(globalNOCRegistryPath(secondRoot), body, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secondRoot, link); err != nil {
		t.Fatal(err)
	}

	if _, err := readGlobalNOCRegistry(link); err == nil || !strings.Contains(err.Error(), "control root mismatch") {
		t.Fatalf("symlink authority swap error = %v", err)
	}
}

func TestGlobalNOCRegistryRejectsLaunchControlRootMismatch(t *testing.T) {
	root := t.TempDir()
	installActiveGlobalNOC(t, root)
	registry, err := readGlobalNOCRegistry(root)
	if err != nil {
		t.Fatal(err)
	}
	registry.Launches[0].Record.CWD = t.TempDir()
	body, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(globalNOCRegistryPath(root), append(body, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readGlobalNOCRegistry(root); err == nil || !strings.Contains(err.Error(), "control root binding mismatch") {
		t.Fatalf("launch root mismatch error = %v", err)
	}
}

func TestResolveGlobalNOCRegistrationPlanDefaultsAndOptOut(t *testing.T) {
	root := t.TempDir()
	launchRecord := installActiveGlobalNOC(t, root)
	stubVerifiedGlobalNOCPane(t, launchRecord)
	oldCWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldCWD) })

	plan, err := resolveGlobalNOCRegistrationPlan("", false, false)
	if err != nil || !plan.Enabled || plan.Strict || plan.Policy != "registered_noc_default" || plan.Context == nil {
		t.Fatalf("default plan=%+v err=%v", plan, err)
	}
	optOut, err := resolveGlobalNOCRegistrationPlan("", false, true)
	if err != nil || !optOut.OptOut || optOut.Enabled || optOut.Context == nil {
		t.Fatalf("opt-out plan=%+v err=%v", optOut, err)
	}
	explicit, err := resolveGlobalNOCRegistrationPlan("control", true, false)
	if err != nil || !explicit.Enabled || !explicit.Strict || explicit.Handle != "control" || explicit.Policy != "explicit" {
		t.Fatalf("explicit plan=%+v err=%v", explicit, err)
	}
	if _, err := resolveGlobalNOCRegistrationPlan("control", true, true); err == nil {
		t.Fatal("conflicting registration flags were accepted")
	}
}

func TestGlobalNOCBootstrapPinsContractAndBounds(t *testing.T) {
	got := buildGlobalNOCBootstrap("/control", "noc-1", "/control/.amq-squad/noc/registry.json", globalNOCBackstop{
		IntervalSeconds: 30, TimeoutSeconds: 1800, MaxTicks: 60,
	})
	for _, want := range []string{
		"NOC bootstrap contract version: 1",
		"Step 1", "Step 2", "Step 3", "Board protocol",
		"--no-register-orchestrator", "poll_required",
		"interval=30s timeout=1800s max_ticks=60",
		"never author an unbounded watch or polling loop",
		"never answer or clear a gate",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("bootstrap missing %q:\n%s", want, got)
		}
	}
}

func TestGlobalNOCBootstrapV1Golden(t *testing.T) {
	got := buildGlobalNOCBootstrap("/control", "noc-1", "/control/.amq-squad/noc/registry.json", globalNOCBackstop{
		IntervalSeconds: 30, TimeoutSeconds: 1800, MaxTicks: 60,
	})
	want, err := os.ReadFile(filepath.Join("testdata", "global_noc_bootstrap_v1.golden"))
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Fatalf("bootstrap v%d drifted from golden\n--- got ---\n%s\n--- want ---\n%s", globalNOCBootstrapVersion, got, want)
	}
}
