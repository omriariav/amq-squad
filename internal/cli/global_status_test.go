package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/state"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

type globalStatusTestEnvelope struct {
	SchemaVersion int                      `json:"schema_version"`
	Kind          string                   `json:"kind"`
	Data          globalStatusEnvelopeData `json:"data"`
}

func decodeGlobalStatusTestEnvelope(t *testing.T, body []byte) globalStatusTestEnvelope {
	t.Helper()
	var env globalStatusTestEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode global status JSON: %v\n%s", err, body)
	}
	if env.SchemaVersion != JSONSchemaVersion || env.Kind != "global_status" {
		t.Fatalf("global status envelope = schema:%d kind:%q", env.SchemaVersion, env.Kind)
	}
	return env
}

func TestGlobalStatusNamespaceComparisonCanonicalizesBothSides(t *testing.T) {
	realRoot := t.TempDir()
	aliasRoot := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(realRoot, aliasRoot); err != nil {
		t.Fatalf("symlink namespace root: %v", err)
	}

	recorded := squadnamespace.Resolve(realRoot, team.DefaultProfile, "status-test")
	projected := squadnamespace.Resolve(aliasRoot, team.DefaultProfile, "status-test")
	if !sameGlobalStatusNamespace(recorded, projected) {
		t.Fatalf("equivalent namespace paths compared unequal:\nrecorded=%+v\nprojected=%+v", recorded, projected)
	}

	projected.ID = "forged/session"
	if sameGlobalStatusNamespace(recorded, projected) {
		t.Fatal("namespace comparison ignored non-path identity contradiction")
	}
}

func TestGlobalStatusMissingRegistryIsVisibleReadOnlyAndNonFatal(t *testing.T) {
	var out bytes.Buffer
	teamRead := false
	err := executeGlobalStatus(globalStatusExecution{
		ControlRoot: "/missing-control-root",
		Out:         &out,
		JSON:        true,
		ReadRegistry: func(string) (globalNOCRegistry, error) {
			return globalNOCRegistry{}, os.ErrNotExist
		},
		ReadTeam: func(string, string) (team.Team, error) {
			teamRead = true
			return team.Team{}, errors.New("must not scan projects without a registry")
		},
	})
	if err != nil {
		t.Fatalf("missing registry should render degradation without a command error: %v", err)
	}
	if teamRead {
		t.Fatal("missing registry triggered arbitrary project scanning")
	}
	env := decodeGlobalStatusTestEnvelope(t, out.Bytes())
	if !env.Data.ReadOnly || env.Data.Health != globalStatusUnknown ||
		env.Data.Registry.State != "missing" || env.Data.Readiness.Ready {
		t.Fatalf("missing registry projection = %+v", env.Data)
	}
	if len(env.Data.SourceErrors) != 1 || env.Data.SourceErrors[0].Source != "registry" {
		t.Fatalf("missing registry source errors = %+v", env.Data.SourceErrors)
	}
	assertGlobalStatusActionsConfirmGated(t, env.Data.Actions, "global_status", "global_start")
}

func TestRunGlobalStatusRoutesAndKeepsDegradationNonFatal(t *testing.T) {
	root := t.TempDir()
	stdout, stderr, err := captureOutput(t, func() error {
		return runGlobal([]string{"status", "--root", root, "--json"})
	})
	if err != nil {
		t.Fatalf("global status with no registry should render degradation: %v", err)
	}
	if stderr != "" {
		t.Fatalf("global status JSON wrote stderr: %q", stderr)
	}
	env := decodeGlobalStatusTestEnvelope(t, []byte(stdout))
	if env.Data.Registry.State != "missing" || env.Data.Health != globalStatusUnknown ||
		env.Data.Readiness.Ready || !env.Data.ReadOnly {
		t.Fatalf("routed degraded status = %+v", env.Data)
	}
	if _, statErr := os.Stat(filepath.Join(root, ".amq-squad")); !os.IsNotExist(statErr) {
		t.Fatalf("read-only global status created control state: %v", statErr)
	}
}

func TestRunGlobalStatusHumanOutputNamesVisibleDegradation(t *testing.T) {
	root := t.TempDir()
	stdout, _, err := captureOutput(t, func() error {
		return runGlobal([]string{"status", "--root", root})
	})
	if err != nil {
		t.Fatalf("human global status with no registry: %v", err)
	}
	for _, want := range []string{
		"global NOC status: unknown (registry: missing)",
		"source error [global/registry]",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("human global status missing %q:\n%s", want, stdout)
		}
	}
}

func TestGlobalStatusProjectsMixedRunsFromOneRegistrySnapshot(t *testing.T) {
	now := time.Date(2026, 7, 27, 9, 0, 0, 0, time.UTC)
	controlRoot := "/noc-control"
	registry := globalStatusTestRegistry(controlRoot, now)
	registry.Runs = []globalNOCRun{
		globalStatusTestRun(controlRoot, "/projects/ready", "ready", "run-ready", now),
		globalStatusTestRun(controlRoot, "/projects/healthy", "healthy", "run-healthy", now),
		globalStatusTestRun(controlRoot, "/projects/stale", "stale", "run-stale", now),
		globalStatusTestRun(controlRoot, "/projects/watcher", "watcher", "run-watcher", now),
		globalStatusTestRun(controlRoot, "/projects/stopped", "stopped", "run-stopped", now),
		globalStatusTestRun(controlRoot, "/projects/unreadable", "unreadable", "run-unreadable", now),
	}
	registryCalls := 0
	var out bytes.Buffer
	err := executeGlobalStatus(globalStatusExecution{
		ControlRoot: controlRoot,
		Out:         &out,
		JSON:        true,
		Now:         func() time.Time { return now },
		ReadRegistry: func(string) (globalNOCRegistry, error) {
			registryCalls++
			if registryCalls > 1 {
				t.Fatal("registry was read more than once")
			}
			return registry, nil
		},
		NOCIdentity: func(launch.Record, string) launchRuntimeIdentity {
			return launchRuntimeIdentity{
				Live: true, PIDLive: true, PaneLive: true, PIDAlive: true, BinaryMatch: true,
			}
		},
		ReadTeam: func(project, profile string) (team.Team, error) {
			if strings.Contains(project, "unreadable") {
				return team.Team{}, errors.New("team config unreadable")
			}
			session := filepath.Base(project)
			return team.Team{
				Project: project, Lead: "cto", Orchestrated: true,
				Members: []team.Member{{Role: "cto", Handle: "cto", Binary: "codex", Session: session}},
			}, nil
		},
		ClassifyLead: func(tm team.Team, _ string, member team.Member, session string) statusRecord {
			state := statusStateLive
			switch {
			case strings.Contains(tm.Project, "stopped"):
				state = statusStateMissing
			case strings.Contains(tm.Project, "stale"):
				state = statusStateStale
			}
			return statusRecord{
				Role: member.Role, Handle: member.Handle, Binary: member.Binary,
				Status: state, RecordState: string(state), Session: session,
			}
		},
		OperatorData: func(project, profile, session, _ string, _ func() time.Time) (operatorStatusEnvelopeData, error) {
			data := operatorStatusEnvelopeData{
				Namespace: squadnamespace.Resolve(project, profile, session),
				ReadOnly:  true,
			}
			if strings.Contains(project, "healthy") {
				data.Attention = []operatorAttention{
					{
						EventType: "gate", Thread: "gate/newer", Subject: "APPROVAL: newer",
						GateKind: "approval", Age: "1h", LastEventAt: now.Add(-time.Hour),
						Inspect: "amq thread --id gate/newer", Actionable: true,
						Profile: profile, Session: session, NamespaceID: squadnamespace.ID(profile, session),
					},
					{
						EventType: "gate", Thread: "gate/older", Subject: "APPROVAL: older",
						GateKind: "approval", Age: "3h", LastEventAt: now.Add(-3 * time.Hour),
						Inspect: "amq thread --id gate/older", Actionable: true,
						Profile: profile, Session: session, NamespaceID: squadnamespace.ID(profile, session),
					},
					{
						EventType: "gate", Thread: "gate/cleared", Subject: "APPROVED",
						Age: "4h", LastEventAt: now.Add(-4 * time.Hour),
						Actionable: true, Cleared: true,
						Profile: profile, Session: session, NamespaceID: squadnamespace.ID(profile, session),
					},
				}
			}
			return data, nil
		},
		Watcher: func(tm team.Team, _ string, _ string, _ time.Time) notificationWatcherStatus {
			if strings.Contains(tm.Project, "watcher") {
				return notificationWatcherStatus{Enabled: true, Health: "stale", Reason: "heartbeat expired"}
			}
			return notificationWatcherStatus{Enabled: true, Health: "healthy"}
		},
	})
	if err != nil {
		t.Fatalf("project mixed global status: %v", err)
	}
	if registryCalls != 1 {
		t.Fatalf("registry calls = %d, want exactly one", registryCalls)
	}

	env := decodeGlobalStatusTestEnvelope(t, out.Bytes())
	if env.Data.NOC == nil || env.Data.NOC.Health != globalStatusHealthy {
		t.Fatalf("NOC projection = %+v", env.Data.NOC)
	}
	if len(env.Data.Runs) != 6 {
		t.Fatalf("run count = %d, want 6", len(env.Data.Runs))
	}
	rows := globalStatusRowsBySession(env.Data.Runs)
	if ready := rows["ready"]; ready.Health != globalStatusHealthy || !ready.Readiness.Ready {
		t.Fatalf("ready row = %+v", ready)
	}
	healthy := rows["healthy"]
	if healthy.Health != globalStatusDegraded || healthy.OperatorGates.Open != 2 ||
		healthy.OperatorGates.OldestAge != "3h" {
		t.Fatalf("healthy-with-open-gates row = %+v", healthy)
	}
	if got := []string{healthy.OperatorGates.Items[0].Thread, healthy.OperatorGates.Items[1].Thread}; !reflect.DeepEqual(got, []string{"gate/older", "gate/newer"}) {
		t.Fatalf("gate order = %v", got)
	}
	if stopped := rows["stopped"]; stopped.Health != globalStatusStopped {
		t.Fatalf("stopped row = %+v", stopped)
	}
	if stale := rows["stale"]; stale.Health != globalStatusStopped {
		t.Fatalf("stale row = %+v", stale)
	}
	if watcher := rows["watcher"]; watcher.Health != globalStatusDegraded || watcher.Readiness.Ready {
		t.Fatalf("watcher-degraded row = %+v", watcher)
	}
	unreadable := rows["unreadable"]
	if unreadable.Health != globalStatusUnknown || len(unreadable.SourceErrors) == 0 ||
		unreadable.SourceErrors[0].Source != "team" {
		t.Fatalf("unreadable row = %+v", unreadable)
	}
	if env.Data.Health != globalStatusUnknown || env.Data.Readiness.Ready {
		t.Fatalf("mixed overall readiness = %+v / health=%s", env.Data.Readiness, env.Data.Health)
	}
	assertGlobalStatusActionsConfirmGated(t, env.Data.Actions, "global_status", "global_start")
	for _, row := range env.Data.Runs {
		assertGlobalStatusActionsConfirmGated(t, row.Actions, "status", "resume_preview", "resume_new_session")
	}
}

func TestGlobalStatusNamedProfileUsesIsolatedAMQBaseRoot(t *testing.T) {
	now := time.Date(2026, 7, 27, 9, 0, 0, 0, time.UTC)
	controlRoot := "/noc-control"
	project := "/projects/release"
	session := "named-profile"
	profile := "release"
	registry := globalStatusTestRegistry(controlRoot, now)
	run := globalStatusTestRun(controlRoot, project, session, "run-named-profile", now)
	run.Namespace = squadnamespace.Resolve(project, profile, session)
	registry.Runs = []globalNOCRun{run}

	var out bytes.Buffer
	var projectedBaseRoot string
	err := executeGlobalStatus(globalStatusExecution{
		ControlRoot: controlRoot,
		Out:         &out,
		JSON:        true,
		Now:         func() time.Time { return now },
		ReadRegistry: func(string) (globalNOCRegistry, error) {
			return registry, nil
		},
		NOCIdentity: func(launch.Record, string) launchRuntimeIdentity {
			return launchRuntimeIdentity{Live: true, PIDLive: true, PaneLive: true}
		},
		ReadTeam: func(gotProject, gotProfile string) (team.Team, error) {
			if gotProject != project || gotProfile != profile {
				t.Fatalf("team read namespace = %s/%s, want %s/%s", gotProject, gotProfile, project, profile)
			}
			return team.Team{
				Project: project, Lead: "cto", Orchestrated: true,
				Members: []team.Member{{Role: "cto", Handle: "cto", Binary: "codex", Session: session}},
			}, nil
		},
		ClassifyLead: func(_ team.Team, _ string, member team.Member, session string) statusRecord {
			return statusRecord{
				Role: member.Role, Handle: member.Handle, Binary: member.Binary,
				Status: statusStateLive, RecordState: "live", Session: session,
			}
		},
		OperatorData: func(gotProject, gotProfile, gotSession, baseRoot string, _ func() time.Time) (operatorStatusEnvelopeData, error) {
			projectedBaseRoot = baseRoot
			return operatorStatusEnvelopeData{
				Namespace: squadnamespace.Resolve(gotProject, gotProfile, gotSession),
				ReadOnly:  true,
			}, nil
		},
		Watcher: func(team.Team, string, string, time.Time) notificationWatcherStatus {
			return notificationWatcherStatus{Enabled: true, Health: "healthy"}
		},
	})
	if err != nil {
		t.Fatalf("named-profile global status: %v", err)
	}
	if projectedBaseRoot != run.Namespace.AMQRoot {
		t.Fatalf("named-profile operator base root = %q, want isolated root %q", projectedBaseRoot, run.Namespace.AMQRoot)
	}
	env := decodeGlobalStatusTestEnvelope(t, out.Bytes())
	if len(env.Data.Runs) != 1 || env.Data.Runs[0].Health != globalStatusHealthy {
		t.Fatalf("named-profile row = %+v", env.Data.Runs)
	}
}

func TestGlobalStatusDefaultProfileScannerCannotReadSiblingSession(t *testing.T) {
	project := t.TempDir()
	registered := squadnamespace.Resolve(project, team.DefaultProfile, "registered")
	sibling := squadnamespace.Resolve(project, team.DefaultProfile, "sibling")
	for _, fixture := range []struct {
		namespace squadnamespace.Ref
		handle    string
	}{
		{registered, "registered-lead"},
		{sibling, "sibling-lead"},
	} {
		agentDir := filepath.Join(fixture.namespace.AMQRoot, "agents", fixture.handle)
		if err := os.MkdirAll(agentDir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := launch.Write(agentDir, launch.Record{
			CWD: project, Root: fixture.namespace.AMQRoot, Handle: fixture.handle,
			Role: "cto", Binary: "codex", Session: fixture.namespace.Session,
			TeamProfile: fixture.namespace.Profile, StartedAt: time.Now().UTC(),
		}); err != nil {
			t.Fatal(err)
		}
	}

	snapshot, err := state.Build(project, globalStatusAMQBaseRoot(registered), state.Probe{})
	if err != nil {
		t.Fatalf("build exact-session snapshot: %v", err)
	}
	if len(snapshot.Sessions) != 1 ||
		snapshot.Sessions[0].Name != registered.Session ||
		len(snapshot.Sessions[0].Agents) != 1 ||
		snapshot.Sessions[0].Agents[0].Handle != "registered-lead" {
		t.Fatalf("exact-session scanner crossed into sibling namespace: %+v", snapshot.Sessions)
	}
}

func TestGlobalStatusMalformedLeadClassificationIsUnknownAndHumanVisible(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	project := t.TempDir()
	session := "malformed-lead"
	agentDir := filepath.Join(base, session, "agents", "cto")
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, launch.FileName), []byte("{not-json"), 0o600); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 7, 27, 9, 0, 0, 0, time.UTC)
	controlRoot := "/noc-control"
	registry := globalStatusTestRegistry(controlRoot, now)
	registry.Runs = []globalNOCRun{
		globalStatusTestRun(controlRoot, project, session, "run-malformed-lead", now),
	}
	var out bytes.Buffer
	err := executeGlobalStatus(globalStatusExecution{
		ControlRoot: controlRoot, Out: &out, JSON: true, Now: func() time.Time { return now },
		ReadRegistry: func(string) (globalNOCRegistry, error) { return registry, nil },
		NOCIdentity: func(launch.Record, string) launchRuntimeIdentity {
			return launchRuntimeIdentity{Live: true, PIDLive: true, PaneLive: true}
		},
		ReadTeam: func(string, string) (team.Team, error) {
			return team.Team{
				Project: project, Lead: "cto", Orchestrated: true,
				Members: []team.Member{{Role: "cto", Handle: "cto", Binary: "codex", Session: session}},
			}, nil
		},
		OperatorData: func(project, profile, session, _ string, _ func() time.Time) (operatorStatusEnvelopeData, error) {
			return operatorStatusEnvelopeData{
				Namespace: squadnamespace.Resolve(project, profile, session), ReadOnly: true,
			}, nil
		},
		Watcher: func(team.Team, string, string, time.Time) notificationWatcherStatus {
			return notificationWatcherStatus{Enabled: true, Health: "healthy"}
		},
	})
	if err != nil {
		t.Fatalf("malformed lead projection: %v", err)
	}
	env := decodeGlobalStatusTestEnvelope(t, out.Bytes())
	row := env.Data.Runs[0]
	if row.Health != globalStatusUnknown || row.Readiness.Ready ||
		!strings.Contains(row.Lead.Detail, "read launch record") {
		t.Fatalf("malformed lead was normalized instead of fail-visible: %+v", row)
	}
	foundLeadError := false
	for _, sourceErr := range row.SourceErrors {
		foundLeadError = foundLeadError || (sourceErr.Source == "lead" && strings.Contains(sourceErr.Detail, "read launch record"))
	}
	if !foundLeadError {
		t.Fatalf("malformed lead source error missing: %+v", row.SourceErrors)
	}
	var human bytes.Buffer
	if err := renderGlobalStatus(&human, env.Data, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(human.String(), "read launch record") {
		t.Fatalf("human status hid lead classification failure:\n%s", human.String())
	}
}

func TestGlobalStatusStoppedNOCWithLiveRuntimeIsDegradedAndCannotStart(t *testing.T) {
	now := time.Date(2026, 7, 27, 9, 0, 0, 0, time.UTC)
	item := globalStatusTestRegistry("/noc-control", now).Launches[0]
	item.State = globalNOCLaunchStopped
	noc := projectGlobalStatusNOC(item, func(launch.Record, string) launchRuntimeIdentity {
		return launchRuntimeIdentity{Live: true, PIDLive: true, PaneLive: true}
	})
	if noc.Health != globalStatusDegraded || !strings.Contains(noc.Detail, "persisted NOC state is stopped") {
		t.Fatalf("stopped/live contradiction = %+v", noc)
	}
	actions := globalNOCStatusActions("/noc-control", noc, "healthy")
	assertGlobalStatusActionsConfirmGated(t, actions, "global_status")
	for _, action := range actions {
		if action.Kind == "global_start" {
			t.Fatalf("live canonical NOC exposed a second-start command: %+v", action)
		}
	}
}

func TestGlobalStatusOpenGatesRejectsForeignItemBinding(t *testing.T) {
	expected := squadnamespace.Resolve("/projects/registered", team.DefaultProfile, "registered")
	now := time.Date(2026, 7, 27, 9, 0, 0, 0, time.UTC)
	items := []operatorAttention{
		{
			EventType: "gate", Thread: "gate/registered", Subject: "APPROVAL: registered",
			Actionable: true, Profile: expected.Profile, Session: expected.Session,
			NamespaceID: expected.ID, LastEventAt: now, Age: "1m",
		},
		{
			EventType: "gate", Thread: "gate/foreign", Subject: "APPROVAL: foreign",
			Actionable: true, Profile: expected.Profile, Session: "foreign",
			NamespaceID: squadnamespace.ID(expected.Profile, "foreign"), LastEventAt: now, Age: "1m",
		},
	}
	gates, sourceErrors := globalStatusOpenGates(items, expected)
	if gates.Open != 1 || gates.Items[0].Thread != "gate/registered" ||
		len(sourceErrors) != 1 || !strings.Contains(sourceErrors[0], "gate/foreign") {
		t.Fatalf("foreign attention binding was projected: gates=%+v errors=%+v", gates, sourceErrors)
	}
}

func TestGlobalStatusContradictorySourcesFailClosed(t *testing.T) {
	now := time.Date(2026, 7, 27, 9, 0, 0, 0, time.UTC)
	controlRoot := "/noc-control"
	registry := globalStatusTestRegistry(controlRoot, now)
	run := globalStatusTestRun(controlRoot, "/projects/contradictory", "session", "run-contradictory", now)
	run.Namespace.ID = "forged-namespace"
	run.ExternalRegistration.NOCLaunchID = "wrong-launch"
	registry.Runs = []globalNOCRun{run}
	var out bytes.Buffer
	teamRead := false
	operatorRead := false
	err := executeGlobalStatus(globalStatusExecution{
		ControlRoot: controlRoot,
		Out:         &out,
		JSON:        true,
		Now:         func() time.Time { return now },
		ReadRegistry: func(string) (globalNOCRegistry, error) {
			return registry, nil
		},
		NOCIdentity: func(launch.Record, string) launchRuntimeIdentity {
			return launchRuntimeIdentity{Live: true, PIDLive: true, PaneLive: true}
		},
		ReadTeam: func(project, _ string) (team.Team, error) {
			teamRead = true
			return team.Team{
				Project: project, Lead: "cto", Orchestrated: true,
				Members: []team.Member{{Role: "cto", Handle: "cto", Binary: "codex", Session: "session"}},
			}, nil
		},
		ClassifyLead: func(_ team.Team, _ string, member team.Member, session string) statusRecord {
			return statusRecord{
				Role: member.Role, Handle: member.Handle, Binary: member.Binary,
				Status: statusStateLive, RecordState: "live", Session: session,
			}
		},
		OperatorData: func(project, profile, session, _ string, _ func() time.Time) (operatorStatusEnvelopeData, error) {
			operatorRead = true
			return operatorStatusEnvelopeData{
				Namespace: squadnamespace.Resolve(project, profile, session),
				ReadOnly:  true,
			}, nil
		},
		Watcher: func(team.Team, string, string, time.Time) notificationWatcherStatus {
			return notificationWatcherStatus{Enabled: true, Health: "healthy"}
		},
	})
	if err != nil {
		t.Fatalf("contradictory global status should render fail-closed data: %v", err)
	}
	if teamRead || operatorRead {
		t.Fatalf("contradictory namespace triggered cross-root reads: team=%t operator=%t", teamRead, operatorRead)
	}
	env := decodeGlobalStatusTestEnvelope(t, out.Bytes())
	row := env.Data.Runs[0]
	if row.Health != globalStatusUnknown || row.Readiness.Ready || len(row.SourceErrors) < 2 || len(row.Actions) != 0 {
		t.Fatalf("contradictory row did not fail closed: %+v", row)
	}
	details := ""
	for _, sourceErr := range row.SourceErrors {
		details += sourceErr.Source + ": " + sourceErr.Detail + "\n"
	}
	for _, want := range []string{"namespace", "registration"} {
		if !strings.Contains(details, want) {
			t.Fatalf("contradictory source errors missing %q:\n%s", want, details)
		}
	}
}

func TestGlobalStatusTeamProjectContradictionStopsCrossRootProjection(t *testing.T) {
	now := time.Date(2026, 7, 27, 9, 0, 0, 0, time.UTC)
	controlRoot := "/noc-control"
	project := "/projects/registered"
	registry := globalStatusTestRegistry(controlRoot, now)
	registry.Runs = []globalNOCRun{
		globalStatusTestRun(controlRoot, project, "session", "run-team-contradiction", now),
	}

	var out bytes.Buffer
	classified := false
	operatorRead := false
	watcherRead := false
	err := executeGlobalStatus(globalStatusExecution{
		ControlRoot: controlRoot,
		Out:         &out,
		JSON:        true,
		Now:         func() time.Time { return now },
		ReadRegistry: func(string) (globalNOCRegistry, error) {
			return registry, nil
		},
		NOCIdentity: func(launch.Record, string) launchRuntimeIdentity {
			return launchRuntimeIdentity{Live: true, PIDLive: true, PaneLive: true}
		},
		ReadTeam: func(string, string) (team.Team, error) {
			return team.Team{
				Project: "/projects/different", Lead: "cto", Orchestrated: true,
				Members: []team.Member{{Role: "cto", Handle: "cto", Binary: "codex"}},
			}, nil
		},
		ClassifyLead: func(team.Team, string, team.Member, string) statusRecord {
			classified = true
			return statusRecord{}
		},
		OperatorData: func(string, string, string, string, func() time.Time) (operatorStatusEnvelopeData, error) {
			operatorRead = true
			return operatorStatusEnvelopeData{}, nil
		},
		Watcher: func(team.Team, string, string, time.Time) notificationWatcherStatus {
			watcherRead = true
			return notificationWatcherStatus{}
		},
	})
	if err != nil {
		t.Fatalf("team-project contradiction should render fail-closed data: %v", err)
	}
	if classified || operatorRead || watcherRead {
		t.Fatalf("team-project contradiction reached downstream sources: classify=%t operator=%t watcher=%t", classified, operatorRead, watcherRead)
	}
	env := decodeGlobalStatusTestEnvelope(t, out.Bytes())
	row := env.Data.Runs[0]
	if row.Health != globalStatusUnknown || row.Readiness.Ready || len(row.Actions) != 0 ||
		len(row.SourceErrors) == 0 || row.SourceErrors[0].Source != "team" {
		t.Fatalf("team-project contradiction row = %+v", row)
	}
}

func TestGlobalStatusRootSessionCannotEscapeRegistryScope(t *testing.T) {
	now := time.Date(2026, 7, 27, 9, 0, 0, 0, time.UTC)
	controlRoot := "/noc-control"
	project := "/projects/root-session"
	registry := globalStatusTestRegistry(controlRoot, now)
	run := globalStatusTestRun(controlRoot, project, "ordinary", "run-root-session", now)
	run.Namespace = squadnamespace.Resolve(project, team.DefaultProfile, "")
	registry.Runs = []globalNOCRun{run}

	var out bytes.Buffer
	teamRead := false
	operatorRead := false
	err := executeGlobalStatus(globalStatusExecution{
		ControlRoot: controlRoot,
		Out:         &out,
		JSON:        true,
		Now:         func() time.Time { return now },
		ReadRegistry: func(string) (globalNOCRegistry, error) {
			return registry, nil
		},
		NOCIdentity: func(launch.Record, string) launchRuntimeIdentity {
			return launchRuntimeIdentity{Live: true, PIDLive: true, PaneLive: true}
		},
		ReadTeam: func(string, string) (team.Team, error) {
			teamRead = true
			return team.Team{}, nil
		},
		OperatorData: func(string, string, string, string, func() time.Time) (operatorStatusEnvelopeData, error) {
			operatorRead = true
			return operatorStatusEnvelopeData{}, nil
		},
	})
	if err != nil {
		t.Fatalf("root-session registry entry should render fail-closed data: %v", err)
	}
	if teamRead || operatorRead {
		t.Fatalf("root-session registry entry triggered scoped reads: team=%t operator=%t", teamRead, operatorRead)
	}
	env := decodeGlobalStatusTestEnvelope(t, out.Bytes())
	row := env.Data.Runs[0]
	if row.Health != globalStatusUnknown || row.Readiness.Ready || len(row.Actions) != 0 ||
		len(row.SourceErrors) == 0 || row.SourceErrors[0].Source != "namespace" {
		t.Fatalf("root-session row = %+v", row)
	}
}

func TestGlobalStatusRealRegistryReadIsBytePreserving(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 7, 27, 9, 0, 0, 0, time.UTC)
	registry := globalNOCRegistry{
		SchemaVersion: globalNOCRegistrySchema,
		ControlRoot:   root,
		UpdatedAt:     now,
		Launches:      []globalNOCLaunch{},
		Runs:          []globalNOCRun{},
	}
	body, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := globalNOCRegistryPath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(body, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	beforeEntries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := executeGlobalStatus(globalStatusExecution{
		ControlRoot: root, Out: &out, JSON: true, Now: func() time.Time { return now },
	}); err != nil {
		t.Fatalf("real registry status: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	afterEntries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("global status mutated registry bytes")
	}
	if got, want := directoryEntryNames(afterEntries), directoryEntryNames(beforeEntries); !reflect.DeepEqual(got, want) {
		t.Fatalf("global status changed registry directory entries: before=%v after=%v", want, got)
	}
}

func TestStatusRecordProjectsOrchestratorRegistrationProvenance(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	project := t.TempDir()
	session := "registration-visible"
	now := time.Date(2026, 7, 27, 9, 0, 0, 0, time.UTC)
	member := team.Member{Role: "cto", Handle: "cto", Binary: "codex", Session: session}
	tm := team.Team{
		Project: project, Lead: member.Role, Orchestrated: true,
		Members: []team.Member{member},
	}
	registration := &launch.OrchestratorRegistration{
		Policy: "registered_noc_default", State: globalNOCRunRegistered, Handle: "global-orch",
		ExternalRegistrationID: "external-1", ExternalGeneration: 4,
		NOCControlRoot: "/noc-control", NOCLaunchID: "noc-launch-4", NOCGeneration: 4,
		NOCRunRegistrationID: "run-1", RegisteredAt: now.Add(-time.Minute),
	}
	writeMemberLaunchRecord(t, base, session, member.Handle, launch.Record{
		CWD: project, Root: filepath.Join(base, session), Binary: member.Binary, Role: member.Role,
		AgentPID: 4242, StartedAt: now.Add(-time.Hour), OrchestratorRegistration: registration,
	})
	row := classifyMemberStatus(tm, team.DefaultProfile, member, session, statusProbe(
		map[int]bool{4242: true},
		map[int]bool{4242: true},
		now,
	))
	if row.Status != statusStateLive || !reflect.DeepEqual(row.OrchestratorRegistration, registration) {
		t.Fatalf("status registration projection = status:%s registration:%+v", row.Status, row.OrchestratorRegistration)
	}
	body, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"orchestrator_registration"`,
		`"noc_launch_id":"noc-launch-4"`,
		`"noc_run_registration_id":"run-1"`,
	} {
		if !bytes.Contains(body, []byte(want)) {
			t.Fatalf("status JSON missing %s: %s", want, body)
		}
	}
}

func TestDoctorGlobalNOCRegistrationProjection(t *testing.T) {
	now := time.Date(2026, 7, 27, 9, 0, 0, 0, time.UTC)

	t.Run("no claim", func(t *testing.T) {
		check := doctorCheckGlobalNOCRegistrationAtRoot(
			team.Team{Project: t.TempDir()},
			team.DefaultProfile,
			"session",
			t.TempDir(),
		)
		if check.Status != doctorOK || !strings.Contains(check.Detail, "no global NOC registration claim") {
			t.Fatalf("no-claim doctor check = %+v", check)
		}
	})

	t.Run("verified binding", func(t *testing.T) {
		controlRoot := t.TempDir()
		canonicalControlRoot, err := canonicalGlobalNOCControlRoot(controlRoot)
		if err != nil {
			t.Fatal(err)
		}
		project := t.TempDir()
		sessionRoot := t.TempDir()
		session := "verified"
		registry := globalStatusTestRegistry(canonicalControlRoot, now)
		run := globalStatusTestRun(canonicalControlRoot, project, session, "run-verified", now)
		registry.Runs = []globalNOCRun{run}
		writeGlobalStatusTestRegistry(t, canonicalControlRoot, registry)
		writeGlobalStatusDoctorRecord(t, sessionRoot, "global-orch", project, session, run.ExternalRegistration)

		check := doctorCheckGlobalNOCRegistrationAtRoot(
			team.Team{Project: project},
			team.DefaultProfile,
			session,
			sessionRoot,
		)
		if check.Status != doctorOK ||
			!strings.Contains(check.Detail, "verified wake_registered") ||
			!strings.Contains(check.Detail, "run-verified") {
			t.Fatalf("verified doctor check = %+v", check)
		}
	})

	t.Run("partial binding fails closed", func(t *testing.T) {
		project := t.TempDir()
		sessionRoot := t.TempDir()
		registration := &launch.OrchestratorRegistration{
			Policy: "registered_noc_default", State: globalNOCRunRegistered, Handle: "global-orch",
			ExternalRegistrationID: "external-1", ExternalGeneration: 1,
			NOCControlRoot: t.TempDir(), NOCGeneration: 1,
			NOCRunRegistrationID: "run-partial", RegisteredAt: now,
		}
		writeGlobalStatusDoctorRecord(t, sessionRoot, "global-orch", project, "partial", registration)
		check := doctorCheckGlobalNOCRegistrationAtRoot(
			team.Team{Project: project},
			team.DefaultProfile,
			"partial",
			sessionRoot,
		)
		if check.Status != doctorFail || !strings.Contains(check.Detail, "partial global NOC binding") {
			t.Fatalf("partial-binding doctor check = %+v", check)
		}
	})

	t.Run("forged registration handle fails closed", func(t *testing.T) {
		controlRoot := t.TempDir()
		canonicalControlRoot, err := canonicalGlobalNOCControlRoot(controlRoot)
		if err != nil {
			t.Fatal(err)
		}
		project := t.TempDir()
		sessionRoot := t.TempDir()
		session := "forged-handle"
		registry := globalStatusTestRegistry(canonicalControlRoot, now)
		run := globalStatusTestRun(canonicalControlRoot, project, session, "run-forged-handle", now)
		run.ExternalRegistration.Handle = "different-orchestrator"
		registry.Runs = []globalNOCRun{run}
		writeGlobalStatusTestRegistry(t, canonicalControlRoot, registry)
		writeGlobalStatusDoctorRecord(t, sessionRoot, "global-orch", project, session, run.ExternalRegistration)

		check := doctorCheckGlobalNOCRegistrationAtRoot(
			team.Team{Project: project},
			team.DefaultProfile,
			session,
			sessionRoot,
		)
		if check.Status != doctorFail || !strings.Contains(check.Detail, "registration handle contradicts launch identity") {
			t.Fatalf("forged-handle doctor check = %+v", check)
		}
	})

	t.Run("copied launch identity fails closed", func(t *testing.T) {
		controlRoot := t.TempDir()
		canonicalControlRoot, err := canonicalGlobalNOCControlRoot(controlRoot)
		if err != nil {
			t.Fatal(err)
		}
		project := t.TempDir()
		sessionRoot := t.TempDir()
		session := "copied-launch"
		registry := globalStatusTestRegistry(canonicalControlRoot, now)
		run := globalStatusTestRun(canonicalControlRoot, project, session, "run-copied-launch", now)
		registry.Runs = []globalNOCRun{run}
		writeGlobalStatusTestRegistry(t, canonicalControlRoot, registry)
		writeGlobalStatusDoctorRecord(t, sessionRoot, "global-orch", filepath.Join(project, "different"), session, run.ExternalRegistration)

		check := doctorCheckGlobalNOCRegistrationAtRoot(
			team.Team{Project: project},
			team.DefaultProfile,
			session,
			sessionRoot,
		)
		if check.Status != doctorFail || !strings.Contains(check.Detail, "launch identity contradicts exact project/profile/session root") {
			t.Fatalf("copied-launch doctor check = %+v", check)
		}
	})

	t.Run("unreadable launch record fails closed", func(t *testing.T) {
		sessionRoot := t.TempDir()
		agentDir := filepath.Join(sessionRoot, "agents", "broken")
		if err := os.MkdirAll(agentDir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(agentDir, launch.FileName), []byte("{not-json"), 0o600); err != nil {
			t.Fatal(err)
		}
		check := doctorCheckGlobalNOCRegistrationAtRoot(
			team.Team{Project: t.TempDir()},
			team.DefaultProfile,
			"broken",
			sessionRoot,
		)
		if check.Status != doctorFail || !strings.Contains(check.Detail, "inspection is incomplete") {
			t.Fatalf("unreadable doctor check = %+v", check)
		}
	})

	t.Run("duplicate run id fails closed", func(t *testing.T) {
		for _, forgedFirst := range []bool{false, true} {
			order := "legitimate_then_forged"
			if forgedFirst {
				order = "forged_then_legitimate"
			}
			t.Run(order, func(t *testing.T) {
				controlRoot := t.TempDir()
				canonicalControlRoot, err := canonicalGlobalNOCControlRoot(controlRoot)
				if err != nil {
					t.Fatal(err)
				}
				project := t.TempDir()
				sessionRoot := t.TempDir()
				session := "duplicate-run"
				registry := globalStatusTestRegistry(canonicalControlRoot, now)
				legitimate := globalStatusTestRun(canonicalControlRoot, project, session, "run-duplicate", now)
				forged := globalStatusTestRun(
					canonicalControlRoot,
					filepath.Join(project, "forged"),
					"forged-session",
					"run-duplicate",
					now,
				)
				registry.Runs = []globalNOCRun{legitimate, forged}
				if forgedFirst {
					registry.Runs = []globalNOCRun{forged, legitimate}
				}
				if reflect.DeepEqual(registry.Runs[0], registry.Runs[1]) {
					t.Fatal("duplicate-ID fixture must use distinct legitimate and forged records")
				}
				writeGlobalStatusTestRegistry(t, canonicalControlRoot, registry)
				writeGlobalStatusDoctorRecord(t, sessionRoot, "global-orch", project, session, legitimate.ExternalRegistration)

				check := doctorCheckGlobalNOCRegistrationAtRoot(
					team.Team{Project: project},
					team.DefaultProfile,
					session,
					sessionRoot,
				)
				if check.Status != doctorFail || !strings.Contains(check.Detail, "duplicate run registrations") {
					t.Fatalf("%s doctor check = %+v", order, check)
				}
			})
		}
	})
}

func globalStatusTestRegistry(controlRoot string, now time.Time) globalNOCRegistry {
	return globalNOCRegistry{
		SchemaVersion:     globalNOCRegistrySchema,
		ControlRoot:       controlRoot,
		CurrentGeneration: 1,
		UpdatedAt:         now,
		Launches: []globalNOCLaunch{{
			ID: "noc-launch-1", Generation: 1, State: globalNOCLaunchActive,
			Record: launch.Record{
				CWD: controlRoot, Binary: "codex", Session: "noc-1", Role: globalNOCRole,
				AgentPID: 1234, Tmux: &launch.TmuxInfo{PaneID: "%1", WindowID: "@1", Session: "noc"},
			},
			BootstrapVersion: globalNOCBootstrapVersion,
			BootstrapDigest:  "sha256:test",
			Backstop:         globalNOCBackstop{IntervalSeconds: 30, TimeoutSeconds: 300, MaxTicks: 10},
			CreatedAt:        now.Add(-time.Hour), UpdatedAt: now,
		}},
	}
}

func globalStatusTestRun(controlRoot, project, session, id string, now time.Time) globalNOCRun {
	namespace := squadnamespace.Resolve(project, team.DefaultProfile, session)
	return globalNOCRun{
		ID: id, NOCLaunchID: "noc-launch-1", NOCGeneration: 1,
		Namespace: namespace, LeadRole: "cto", Policy: "registered_noc_default",
		State: globalNOCRunRegistered,
		ExternalRegistration: &launch.OrchestratorRegistration{
			Policy: "registered_noc_default", State: globalNOCRunRegistered, Handle: "global-orch",
			ExternalRegistrationID: "external-" + id, ExternalGeneration: 1,
			NOCControlRoot: controlRoot, NOCLaunchID: "noc-launch-1", NOCGeneration: 1,
			NOCRunRegistrationID: id, RegisteredAt: now.Add(-30 * time.Minute),
		},
		CreatedAt: now.Add(-30 * time.Minute), UpdatedAt: now,
	}
}

func writeGlobalStatusTestRegistry(t *testing.T, root string, registry globalNOCRegistry) {
	t.Helper()
	body, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := globalNOCRegistryPath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(body, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeGlobalStatusDoctorRecord(t *testing.T, root, handle, project, session string, registration *launch.OrchestratorRegistration) {
	t.Helper()
	agentDir := filepath.Join(root, "agents", handle)
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := launch.Write(agentDir, launch.Record{
		CWD: project, Root: root, Binary: "codex", Handle: handle,
		Role: goalOrchestratorRole, Session: session, External: true,
		AgentPID: 4242, StartedAt: time.Now().UTC(),
		OrchestratorRegistration: registration,
	}); err != nil {
		t.Fatal(err)
	}
}

func globalStatusRowsBySession(rows []globalStatusRunView) map[string]globalStatusRunView {
	out := make(map[string]globalStatusRunView, len(rows))
	for _, row := range rows {
		out[row.Session] = row
	}
	return out
}

func assertGlobalStatusActionsConfirmGated(t *testing.T, actions []runtimeActionJSON, expectedKinds ...string) {
	t.Helper()
	if len(actions) == 0 {
		t.Fatal("expected global status actions, got none")
	}
	byKind := make(map[string]runtimeActionJSON, len(actions))
	for _, action := range actions {
		byKind[action.Kind] = action
		if action.ID != action.Kind {
			t.Errorf("action ID does not mirror kind: %+v", action)
		}
		if action.Mutates {
			if !action.NeedsConfirmation || action.ActionKind != "run" ||
				!strings.Contains(strings.ToLower(action.Label), "confirm") {
				t.Errorf("mutating action is not explicitly confirm-gated: %+v", action)
			}
		} else if action.NeedsConfirmation || action.ActionKind != "display" {
			t.Errorf("read-only action is not canonical display action: %+v", action)
		}
	}
	for _, kind := range expectedKinds {
		if _, ok := byKind[kind]; !ok {
			t.Errorf("required action kind %q missing: %+v", kind, actions)
		}
	}
}

func directoryEntryNames(entries []os.DirEntry) []string {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}
