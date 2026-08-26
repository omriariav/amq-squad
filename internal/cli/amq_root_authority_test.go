package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

func TestAMQAuthorityHandlesIncludeEnabledOperator(t *testing.T) {
	got := amqAuthorityHandles(team.Team{
		Members: []team.Member{
			{Role: "worker", Handle: "zeta"},
			{Role: "lead", Handle: "alpha"},
			{Role: "duplicate", Handle: "zeta"},
		},
	})
	if want := []string{"alpha", "user", "zeta"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("amqAuthorityHandles() = %v, want %v", got, want)
	}

	got = amqAuthorityHandles(team.Team{
		Operator: &team.OperatorConfig{Enabled: false},
		Members:  []team.Member{{Role: "worker", Handle: "worker"}},
	})
	if want := []string{"worker"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("disabled operator handles = %v, want %v", got, want)
	}
}

func TestReconcileAMQRootConfigCreatesExactAtomicRoster(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".agent-mail", "session")
	frozen := time.Date(2026, time.July, 29, 1, 2, 3, 0, time.UTC)
	previousNow := amqRootAuthorityNow
	amqRootAuthorityNow = func() time.Time { return frozen }
	t.Cleanup(func() { amqRootAuthorityNow = previousNow })

	result, err := reconcileAMQRootConfig(root, []string{"worker", "user", "worker", " lead "})
	if err != nil {
		t.Fatalf("reconcileAMQRootConfig: %v", err)
	}
	if !result.Changed {
		t.Fatal("new config was not reported changed")
	}
	if result.Path != filepath.Join(root, "meta", "config.json") {
		t.Fatalf("config path = %q", result.Path)
	}
	var document amqRootConfigDocument
	data, err := os.ReadFile(result.Path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	if document.Version != 1 || document.CreatedUTC != frozen.Format(time.RFC3339Nano) {
		t.Fatalf("config identity = %+v", document)
	}
	if want := []string{"lead", "user", "worker"}; !reflect.DeepEqual(document.Agents, want) {
		t.Fatalf("agents = %v, want %v", document.Agents, want)
	}
	matches, err := filepath.Glob(filepath.Join(root, "meta", ".amq-root-config-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("atomic staging files leaked: %v", matches)
	}
}

func TestReconcileAMQRootConfigPreservesCreationAndUnknownFields(t *testing.T) {
	root := filepath.Join(t.TempDir(), "session")
	path := filepath.Join(root, "meta", "config.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	const original = `{"version":1,"created_utc":"2026-01-02T03:04:05Z","agents":["old"],"future":{"enabled":true}}`
	if err := os.WriteFile(path, []byte(original), 0o640); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	result, err := reconcileAMQRootConfig(root, []string{"new", "user"})
	if err != nil {
		t.Fatalf("reconcileAMQRootConfig: %v", err)
	}
	if !result.Changed {
		t.Fatal("roster update was not reported changed")
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(before, after) {
		t.Fatal("config update did not use atomic replacement")
	}
	if after.Mode().Perm() != 0o640 {
		t.Fatalf("config mode = %o, want 640", after.Mode().Perm())
	}
	var document map[string]json.RawMessage
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	var created string
	if err := json.Unmarshal(document["created_utc"], &created); err != nil {
		t.Fatal(err)
	}
	var future struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.Unmarshal(document["future"], &future); err != nil {
		t.Fatal(err)
	}
	if created != "2026-01-02T03:04:05Z" || !future.Enabled {
		t.Fatalf("preserved fields = %s", data)
	}
	var agents []string
	if err := json.Unmarshal(document["agents"], &agents); err != nil {
		t.Fatal(err)
	}
	if want := []string{"new", "user"}; !reflect.DeepEqual(agents, want) {
		t.Fatalf("agents = %v, want %v", agents, want)
	}

	unchangedBefore, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	result, err = reconcileAMQRootConfig(root, []string{"user", "new"})
	if err != nil {
		t.Fatal(err)
	}
	unchangedAfter, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if result.Changed || !os.SameFile(unchangedBefore, unchangedAfter) {
		t.Fatalf("semantic no-op rewrote config: result=%+v", result)
	}
}

func TestReconcileAMQRootConfigMalformedFailsClosed(t *testing.T) {
	root := filepath.Join(t.TempDir(), "session")
	path := filepath.Join(root, "meta", "config.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	const malformed = `{"version":1,"created_utc":"not-a-time","agents":["old"]}`
	if err := os.WriteFile(path, []byte(malformed), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := reconcileAMQRootConfig(root, []string{"new"}); err == nil || !strings.Contains(err.Error(), "created_utc") {
		t.Fatalf("malformed config error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != malformed {
		t.Fatalf("malformed config mutated to %q", data)
	}
}

func TestRepairAMQRootMailboxesPinsExactRootAndReportsWrites(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".agent-mail", "session")
	previousRun := runAMQCommand
	t.Cleanup(func() { runAMQCommand = previousRun })
	runAMQCommand = func(request amqCommandRequest) ([]byte, error) {
		if request.Dir != "/project" {
			t.Fatalf("command dir = %q", request.Dir)
		}
		want := []string{"doctor", "--root", root, "--fix-mailboxes", "--json"}
		if !reflect.DeepEqual(request.Arg, want) {
			t.Fatalf("command args = %v, want %v", request.Arg, want)
		}
		if value := envValue(request.Env, "AM_ROOT"); value != root {
			t.Fatalf("AM_ROOT = %q, want %q", value, root)
		}
		return []byte(`{"mailbox_repair":{"created_paths":["agents/user/inbox/new"]},"summary":{"error":0}}`), nil
	}
	got, err := repairAMQRootMailboxes("/project", root)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"agents/user/inbox/new"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("created paths = %v, want %v", got, want)
	}
}

func TestRepairAMQRootMailboxesFailsOnCommandOrDoctorErrors(t *testing.T) {
	previousRun := runAMQCommand
	t.Cleanup(func() { runAMQCommand = previousRun })
	runAMQCommand = func(amqCommandRequest) ([]byte, error) {
		return nil, errors.New("command failed")
	}
	if _, err := repairAMQRootMailboxes("/project", "/root"); err == nil || !strings.Contains(err.Error(), "command failed") {
		t.Fatalf("command error = %v", err)
	}

	runAMQCommand = func(amqCommandRequest) ([]byte, error) {
		return []byte(`{"mailbox_repair":{"created_paths":[]},"summary":{"error":2}}`), nil
	}
	if _, err := repairAMQRootMailboxes("/project", "/root"); err == nil || !strings.Contains(err.Error(), "2 error") {
		t.Fatalf("doctor error = %v", err)
	}
}

func TestPrepareSelectedAMQRootsCreatesExactRootAuthority(t *testing.T) {
	project := t.TempDir()
	base := filepath.Join(project, ".agent-mail")
	root := filepath.Join(base, "session")
	created, err := prepareSelectedAMQRoots([]agentLaunchPreflight{{
		Root:       root,
		BaseRoot:   base,
		AMQVersion: doctorMinAMQVersion,
	}}, []string{"worker", "user"})
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, "meta", "config.json")
	var config amqRootConfigDocument
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	if want := []string{"user", "worker"}; !reflect.DeepEqual(config.Agents, want) {
		t.Fatalf("agents = %v, want %v", config.Agents, want)
	}
	if _, err := os.Stat(filepath.Join(base, "meta", "config.json")); !os.IsNotExist(err) {
		t.Fatalf("base-root config unexpectedly materialized: %v", err)
	}
	if err := cleanupCreatedLaunchDirectories(created); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(base); !os.IsNotExist(err) {
		t.Fatalf("new authority root survived rollback: %v", err)
	}
}

func TestPrepareSelectedAMQRootsPreservesPreexistingRootRepair(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".agent-mail", "session")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	created, err := prepareSelectedAMQRoots([]agentLaunchPreflight{{Root: root, AMQVersion: doctorMinAMQVersion}}, []string{"worker"})
	if err != nil {
		t.Fatal(err)
	}
	if len(created) != 0 {
		t.Fatalf("pre-existing root returned rollback-owned paths: %v", created)
	}
	if err := cleanupCreatedLaunchDirectories(created); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "meta", "config.json")); err != nil {
		t.Fatalf("migration repair was not preserved: %v", err)
	}
}

func TestRepairTeamAMQRootAuthorityRepairsOnceAndLogsExactWrites(t *testing.T) {
	project := t.TempDir()
	root := filepath.Join(project, ".agent-mail", "session")
	tm := team.Team{
		Project: project,
		Members: []team.Member{
			{Role: "lead", Handle: "lead", CWD: project},
			{Role: "worker", Handle: "worker", CWD: project},
		},
	}
	resolve := func(projectDir, profile, session, handle string) (amqEnv, error) {
		if projectDir != project || profile != team.DefaultProfile || session != "session" {
			t.Fatalf("resolve tuple = %q %q %q", projectDir, profile, session)
		}
		return amqEnv{Root: root, AMQVersion: doctorMinAMQVersion}, nil
	}
	previousRun := runAMQCommand
	t.Cleanup(func() { runAMQCommand = previousRun })
	doctorCalls := 0
	runAMQCommand = func(request amqCommandRequest) ([]byte, error) {
		doctorCalls++
		if got := amqFlagValue(request.Arg, "root"); got != root {
			t.Fatalf("doctor root = %q, want %q", got, root)
		}
		return []byte(`{"mailbox_repair":{"created_paths":["agents/lead/inbox/new","agents/user/inbox/new"]},"summary":{"error":0}}`), nil
	}
	var log bytes.Buffer
	if err := repairTeamAMQRootAuthority(tm, team.DefaultProfile, "session", &log, resolve, true); err != nil {
		t.Fatal(err)
	}
	if doctorCalls != 1 {
		t.Fatalf("doctor calls = %d, want one per unique root", doctorCalls)
	}
	data, err := os.ReadFile(filepath.Join(root, "meta", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var config amqRootConfigDocument
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	if want := []string{"lead", "user", "worker"}; !reflect.DeepEqual(config.Agents, want) {
		t.Fatalf("authority roster = %v, want %v", config.Agents, want)
	}
	// --verbose is required for the per-path detail (#722): a fresh
	// session can legitimately bootstrap dozens of mailbox paths, and that
	// spam previously buried the one line (a partial-launch-failure
	// notice) that mattered on 'resume --exec'.
	for _, want := range []string{
		"wrote " + filepath.Join(root, "meta", "config.json"),
		"created " + filepath.Join(root, "agents", "lead", "inbox", "new"),
		"created " + filepath.Join(root, "agents", "user", "inbox", "new"),
	} {
		if !strings.Contains(log.String(), want) {
			t.Fatalf("repair log missing %q:\n%s", want, log.String())
		}
	}
}

// TestRepairTeamAMQRootAuthorityCollapsesLogWithoutVerbose is the
// non-verbose counterpart to the test above (#722): by default the
// per-path detail collapses into one summary line instead of drowning the
// launch table.
func TestRepairTeamAMQRootAuthorityCollapsesLogWithoutVerbose(t *testing.T) {
	project := t.TempDir()
	root := filepath.Join(project, ".agent-mail", "session")
	tm := team.Team{
		Project: project,
		Members: []team.Member{
			{Role: "lead", Handle: "lead", CWD: project},
			{Role: "worker", Handle: "worker", CWD: project},
		},
	}
	resolve := func(projectDir, profile, session, handle string) (amqEnv, error) {
		return amqEnv{Root: root, AMQVersion: doctorMinAMQVersion}, nil
	}
	previousRun := runAMQCommand
	t.Cleanup(func() { runAMQCommand = previousRun })
	runAMQCommand = func(request amqCommandRequest) ([]byte, error) {
		return []byte(`{"mailbox_repair":{"created_paths":["agents/lead/inbox/new","agents/user/inbox/new"]},"summary":{"error":0}}`), nil
	}
	var log bytes.Buffer
	if err := repairTeamAMQRootAuthority(tm, team.DefaultProfile, "session", &log, resolve, false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(log.String(), "created "+filepath.Join(root, "agents", "lead", "inbox", "new")) {
		t.Fatalf("expected per-path detail suppressed without --verbose, got:\n%s", log.String())
	}
	if !regexp.MustCompile(`^AMQ root authority: bootstrapped \d+ path\(s\) \(use --verbose for detail\)\n$`).MatchString(log.String()) {
		t.Fatalf("expected exactly one bootstrap summary line, got:\n%s", log.String())
	}
}

func TestWriteTeamProfileWithAMQRosterSyncCoversAddUpdateRemove(t *testing.T) {
	project := t.TempDir()
	session := "session"
	root := filepath.Join(project, ".agent-mail", session)
	if err := team.Write(project, team.Team{
		Members: []team.Member{{Role: "lead", Binary: "codex", Handle: "lead", Session: session}},
	}); err != nil {
		t.Fatal(err)
	}
	current, err := team.Read(project)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reconcileAMQRootConfig(root, amqAuthorityHandles(current)); err != nil {
		t.Fatal(err)
	}
	resolve := func(projectDir, profile, gotSession, handle string) (amqEnv, error) {
		if projectDir != project || profile != team.DefaultProfile || gotSession != session {
			t.Fatalf("resolve tuple = %q %q %q", projectDir, profile, gotSession)
		}
		return amqEnv{Root: root, AMQVersion: doctorMinAMQVersion}, nil
	}
	write := func(mutate func(*team.Team)) {
		t.Helper()
		if err := withProfileLock(project, team.DefaultProfile, func() error {
			before, err := team.Read(project)
			if err != nil {
				return err
			}
			after := before
			after.Members = append([]team.Member(nil), before.Members...)
			mutate(&after)
			return writeTeamProfileWithAMQRosterSyncUnderLock(project, team.DefaultProfile, before, after, resolve)
		}); err != nil {
			t.Fatal(err)
		}
	}
	assertAgents := func(want []string) {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(root, "meta", "config.json"))
		if err != nil {
			t.Fatal(err)
		}
		var config amqRootConfigDocument
		if err := json.Unmarshal(data, &config); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(config.Agents, want) {
			t.Fatalf("agents = %v, want %v", config.Agents, want)
		}
	}

	write(func(after *team.Team) {
		after.Members = append(after.Members, team.Member{
			Role: "worker", Binary: "codex", Handle: "worker", Session: session,
		})
	})
	assertAgents([]string{"lead", "user", "worker"})

	write(func(after *team.Team) {
		after.Members[1].Handle = "renamed"
	})
	assertAgents([]string{"lead", "renamed", "user"})

	write(func(after *team.Team) {
		after.Members = after.Members[:1]
	})
	assertAgents([]string{"lead", "user"})
}

func TestWriteTeamProfileWithAMQRosterSyncRejectsMalformedConfigBeforeProfileWrite(t *testing.T) {
	project := t.TempDir()
	session := "session"
	root := filepath.Join(project, ".agent-mail", session)
	if err := team.Write(project, team.Team{
		Members: []team.Member{{Role: "lead", Binary: "codex", Handle: "lead", Session: session}},
	}); err != nil {
		t.Fatal(err)
	}
	before, err := team.Read(project)
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, "meta", "config.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(`{"version":1,"created_utc":"bad","agents":["lead"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	profileBefore, err := os.ReadFile(team.ProfilePath(project, team.DefaultProfile))
	if err != nil {
		t.Fatal(err)
	}
	after := before
	after.Members = append(append([]team.Member(nil), before.Members...), team.Member{
		Role: "worker", Binary: "codex", Handle: "worker", Session: session,
	})
	resolve := func(string, string, string, string) (amqEnv, error) {
		return amqEnv{Root: root, AMQVersion: doctorMinAMQVersion}, nil
	}
	err = withProfileLock(project, team.DefaultProfile, func() error {
		return writeTeamProfileWithAMQRosterSyncUnderLock(project, team.DefaultProfile, before, after, resolve)
	})
	if err == nil || !strings.Contains(err.Error(), "created_utc") {
		t.Fatalf("malformed sync error = %v", err)
	}
	profileAfter, readErr := os.ReadFile(team.ProfilePath(project, team.DefaultProfile))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(profileBefore, profileAfter) {
		t.Fatal("profile changed despite malformed AMQ authority")
	}
}

func envValue(env []string, key string) string {
	prefix := key + "="
	for _, value := range env {
		if strings.HasPrefix(value, prefix) {
			return strings.TrimPrefix(value, prefix)
		}
	}
	return ""
}
