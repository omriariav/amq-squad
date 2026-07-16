package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

// contextCommandScopeInventory is the audited top-level command map for #463.
// "canonical" means the public selection point uses resolveCanonicalContext
// directly or through one of its thin wrappers. The other classes deliberately
// do not select one live AMQ namespace and therefore must not consult ambient
// launch identity.
var contextCommandScopeInventory = map[string]string{
	"activity": "canonical", "agent": "canonical", "amq": "canonical", "archive": "canonical",
	"brief": "canonical", "collect": "canonical", "console": "canonical", "context": "canonical",
	"dispatch": "canonical", "doctor": "canonical", "evidence": "canonical_task_selection", "focus": "canonical", "fork": "canonical",
	"gate": "canonical", "goal": "canonical_except_draft", "lead": "canonical", "monitor": "canonical_multi_session",
	"next": "canonical", "notifications": "canonical", "notify": "canonical", "open": "canonical",
	"namespace": "explicit_endpoint_pair",
	"operator":  "canonical", "prune-panes": "canonical", "receipt": "canonical", "resume": "canonical", "rm": "canonical",
	"send": "canonical", "status": "canonical", "stop": "canonical", "task": "canonical",
	"team": "canonical_except_init", "thread": "canonical", "threads": "canonical", "up": "canonical",
	"verify": "canonical", "bootstrap": "launch_record_bound",
	"new": "configuration_creation", "roles": "context_free", "global": "isolated_root",
	"run": "explicit_run_contract", "wizard": "explicit_run_contract", "history": "multi_project_scan",
	"review-worktree": "git_object_scope", "tmux-harness": "isolated_harness",
	"completion": "context_free",
}

func TestContextCommandScopeInventoryCoversEveryPublicCommand(t *testing.T) {
	var missing, stale []string
	registered := map[string]bool{}
	for _, command := range commandRegistry("test") {
		registered[command.Name] = true
		if contextCommandScopeInventory[command.Name] == "" {
			missing = append(missing, command.Name)
		}
	}
	for command := range contextCommandScopeInventory {
		if !registered[command] {
			stale = append(stale, command)
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)
	if len(missing) > 0 || len(stale) > 0 {
		t.Fatalf("context command inventory drift: missing=%v stale=%v", missing, stale)
	}
	for _, required := range []string{"status", "task", "amq", "agent", "up", "resume", "fork", "team", "thread", "brief", "stop", "notify", "doctor", "rm", "console", "verify", "activity", "collect", "dispatch", "operator", "goal", "lead", "receipt"} {
		if !strings.HasPrefix(contextCommandScopeInventory[required], "canonical") {
			t.Errorf("context-bearing command %q is classified %q", required, contextCommandScopeInventory[required])
		}
	}
}

func isolateCanonicalContextTest(t *testing.T, project string) {
	t.Helper()
	resumeChdir(t, project)
	for _, key := range []string{"AM_ROOT", "AM_BASE_ROOT", "AM_SESSION", "AM_ME", "TMUX_PANE"} {
		t.Setenv(key, "")
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}
	previousScan, previousAlive := contextScanLaunchEntries, contextPIDAlive
	contextScanLaunchEntries = func(string) ([]launch.Entry, error) { return nil, nil }
	contextPIDAlive = func(int) bool { return false }
	t.Cleanup(func() {
		contextScanLaunchEntries = previousScan
		contextPIDAlive = previousAlive
	})
}

func writeContextAMQRC(t *testing.T, project, root string) {
	t.Helper()
	body, err := json.Marshal(map[string]string{"root": root})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".amqrc"), body, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestResolveCanonicalContextPrecedenceMatrix(t *testing.T) {
	project := t.TempDir()
	isolateCanonicalContextTest(t, project)
	project, _ = os.Getwd()

	t.Run("documented defaults", func(t *testing.T) {
		ctx, err := resolveCanonicalContext(contextResolveOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if ctx.Profile != team.DefaultProfile || ctx.Sources["profile"] != contextSourceDefault {
			t.Fatalf("default profile = %q from %q", ctx.Profile, ctx.Sources["profile"])
		}
		if ctx.Sources["session"] != contextSourceDefault || ctx.PinMode != "sessionful" {
			t.Fatalf("default session source/pin = %q/%q", ctx.Sources["session"], ctx.PinMode)
		}
	})

	writeContextAMQRC(t, project, filepath.Join(".agent-mail", "configured"))
	t.Run("project amqrc", func(t *testing.T) {
		ctx, err := resolveCanonicalContext(contextResolveOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if ctx.Session != "configured" || ctx.Sources["session"] != contextSourceAMQRC {
			t.Fatalf("amqrc session = %q from %q", ctx.Session, ctx.Sources["session"])
		}
	})

	liveRoot := filepath.Join(project, ".agent-mail", "live", "live-s")
	contextPIDAlive = func(pid int) bool { return pid == 101 }
	contextScanLaunchEntries = func(string) ([]launch.Entry, error) {
		return []launch.Entry{{
			AgentDir: filepath.Join(liveRoot, "agents", "lead"),
			Record:   launch.Record{AgentPID: 101, TeamProfile: "live", Session: "live-s", Handle: "lead", Root: liveRoot, BaseRoot: liveRoot},
		}}, nil
	}
	t.Run("live launch", func(t *testing.T) {
		ctx, err := resolveCanonicalContext(contextResolveOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if ctx.Profile != "live" || ctx.Session != "live-s" || ctx.Sources["profile"] != contextSourceLaunch {
			t.Fatalf("launch context = profile %q session %q sources %#v", ctx.Profile, ctx.Session, ctx.Sources)
		}
	})

	envRoot := filepath.Join(project, ".agent-mail", "environment", "env-s")
	t.Setenv("AM_ROOT", envRoot)
	t.Setenv("AM_BASE_ROOT", envRoot)
	t.Setenv("AM_ME", "env-agent")
	t.Run("injected environment", func(t *testing.T) {
		ctx, err := resolveCanonicalContext(contextResolveOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if ctx.Profile != "environment" || ctx.Session != "env-s" || ctx.Sources["profile"] != contextSourceEnv || ctx.PinMode != "exact_root" {
			t.Fatalf("env context = profile %q session %q pin %q sources %#v", ctx.Profile, ctx.Session, ctx.PinMode, ctx.Sources)
		}
	})

	t.Run("explicit flags", func(t *testing.T) {
		ctx, err := resolveCanonicalContext(contextResolveOptions{
			ProfileFlag: "flags", SessionFlag: "flag-s", HandleFlag: "flag-agent",
			ProfileExplicit: true, SessionExplicit: true, HandleExplicit: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		if ctx.Profile != "flags" || ctx.Session != "flag-s" || ctx.Handle != "flag-agent" {
			t.Fatalf("flag context = profile %q session %q handle %q", ctx.Profile, ctx.Session, ctx.Handle)
		}
		for _, field := range []string{"profile", "session", "handle"} {
			if ctx.Sources[field] != contextSourceFlags {
				t.Errorf("%s source = %q", field, ctx.Sources[field])
			}
		}
		diagnostic := strings.Join(contextDiagnosticLines(ctx), "\n")
		for _, want := range []string{contextSourceFlags, contextSourceEnv, contextSourceLaunch, contextSourceAMQRC, contextSourceDefault, "winner", "loser"} {
			if !strings.Contains(diagnostic, want) {
				t.Errorf("diagnostic missing %q:\n%s", want, diagnostic)
			}
		}
	})
}

func TestContextExplainExplicitProfileRejectsConflictingTuples(t *testing.T) {
	project := t.TempDir()
	isolateCanonicalContextTest(t, project)
	if err := team.WriteProfile(project, "flagprof", team.Team{Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "flagsession"}}}); err != nil {
		t.Fatal(err)
	}
	writeContextAMQRC(t, project, filepath.Join(".agent-mail", "configprof", "configsession"))
	envRoot := filepath.Join(project, ".agent-mail", "envprof", "envsession")
	t.Setenv("AM_ROOT", envRoot)
	t.Setenv("AM_BASE_ROOT", envRoot)
	t.Setenv("AM_ME", "env-agent")
	liveRoot := filepath.Join(project, ".agent-mail", "liveprof", "livesession")
	contextPIDAlive = func(int) bool { return true }
	contextScanLaunchEntries = func(string) ([]launch.Entry, error) {
		return []launch.Entry{{
			AgentDir: filepath.Join(liveRoot, "agents", "live-agent"),
			Record: launch.Record{
				AgentPID: 1, TeamProfile: "liveprof", Session: "livesession", Handle: "live-agent",
				Root: liveRoot, BaseRoot: liveRoot,
			},
		}}, nil
	}

	stdout, stderr, err := captureOutput(t, func() error {
		return runContextExplain([]string{"--project", project, "--profile", "flagprof", "--json"})
	})
	if err != nil {
		t.Fatalf("public reproduction failed: %v\n%s", err, stderr)
	}
	var envelope struct {
		Data contextResolution `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatalf("parse context explain: %v\n%s", err, stdout)
	}
	ctx := envelope.Data
	wantRoot := filepath.Join(project, ".agent-mail", "flagprof", "flagsession")
	if ctx.Profile != "flagprof" || ctx.Session != "flagsession" || ctx.Handle != "" || ctx.Root != wantRoot || ctx.BaseRoot != wantRoot {
		t.Fatalf("spliced context: %#v", ctx)
	}
	for _, source := range []string{contextSourceEnv, contextSourceLaunch, contextSourceAMQRC} {
		found := false
		for _, candidate := range ctx.Candidates {
			if candidate.Source != source {
				continue
			}
			found = true
			if candidate.Selected {
				t.Errorf("rejected %s candidate selected: %#v", source, candidate)
			}
		}
		if !found {
			t.Errorf("missing rejected %s provenance: %#v", source, ctx.Candidates)
		}
	}
	diagnostic := strings.Join(contextDiagnosticLines(ctx), "\n")
	for _, want := range []string{contextSourceEnv, contextSourceLaunch, contextSourceAMQRC, "loser", "tuple profile"} {
		if !strings.Contains(diagnostic, want) {
			t.Errorf("public diagnostics missing %q:\n%s", want, diagnostic)
		}
	}
}

func TestExplicitSessionSwitchCarriesProfileButNotOldTupleIdentity(t *testing.T) {
	project := t.TempDir()
	isolateCanonicalContextTest(t, project)
	envRoot := filepath.Join(project, ".agent-mail", "envprof", "envsession")
	t.Setenv("AM_ROOT", envRoot)
	t.Setenv("AM_BASE_ROOT", envRoot)
	t.Setenv("AM_ME", "env-agent")

	resolve := func(handle string, explicit bool) contextResolution {
		t.Helper()
		ctx, err := resolveCanonicalContext(contextResolveOptions{
			SessionFlag: "flagsession", HandleFlag: handle,
			SessionExplicit: true, HandleExplicit: explicit,
		})
		if err != nil {
			t.Fatal(err)
		}
		return ctx
	}

	ctx := resolve("", false)
	wantRoot := filepath.Join(project, ".agent-mail", "envprof", "flagsession")
	if ctx.Profile != "envprof" || ctx.Sources["profile"] != contextSourceEnv || ctx.Session != "flagsession" || ctx.Sources["session"] != contextSourceFlags {
		t.Fatalf("same-profile switch anchors: %#v", ctx)
	}
	if ctx.Handle != "" || canonicalContextComparisonPath(ctx.Root) != canonicalContextComparisonPath(wantRoot) || canonicalContextComparisonPath(ctx.BaseRoot) != canonicalContextComparisonPath(wantRoot) || ctx.Sources["root"] != contextSourceDefault {
		t.Fatalf("old tuple identity leaked into switch: %#v", ctx)
	}
	for _, candidate := range ctx.Candidates {
		if candidate.Source == contextSourceEnv && candidate.Field != "profile" && candidate.Selected {
			t.Errorf("old env tuple field selected: %#v", candidate)
		}
	}

	explicitHandle := resolve("new-agent", true)
	if explicitHandle.Handle != "new-agent" || explicitHandle.Sources["handle"] != contextSourceFlags {
		t.Fatalf("explicit handle lost across switch: %#v", explicitHandle)
	}
	for _, candidate := range explicitHandle.Candidates {
		if candidate.Field == "handle" && candidate.Source == contextSourceEnv && candidate.Selected {
			t.Errorf("old environment handle selected over --me: %#v", candidate)
		}
	}
}

func TestMatchingLowerTupleContributesHandleAndExactRoot(t *testing.T) {
	project := t.TempDir()
	isolateCanonicalContextTest(t, project)
	envRoot := filepath.Join(project, ".agent-mail", "envprof", "envsession")
	t.Setenv("AM_ROOT", envRoot)
	t.Setenv("AM_BASE_ROOT", envRoot)
	t.Setenv("AM_ME", "env-agent")

	ctx, err := resolveCanonicalContext(contextResolveOptions{ProfileFlag: "envprof", ProfileExplicit: true})
	if err != nil {
		t.Fatal(err)
	}
	if ctx.Profile != "envprof" || ctx.Session != "envsession" || ctx.Handle != "env-agent" || ctx.Root != envRoot || ctx.BaseRoot != envRoot {
		t.Fatalf("matching lower tuple not preserved: %#v", ctx)
	}
	for _, field := range []string{"session", "handle", "root", "base_root"} {
		if ctx.Sources[field] != contextSourceEnv {
			t.Errorf("%s source = %q, want %q", field, ctx.Sources[field], contextSourceEnv)
		}
	}
}

func TestResolveCanonicalContextAmbiguousLaunchesReportEveryProvenance(t *testing.T) {
	project := t.TempDir()
	isolateCanonicalContextTest(t, project)
	writeContextAMQRC(t, project, filepath.Join(".agent-mail", "configured"))
	contextPIDAlive = func(int) bool { return true }
	contextScanLaunchEntries = func(string) ([]launch.Entry, error) {
		return []launch.Entry{
			{AgentDir: filepath.Join(project, ".agent-mail", "alpha", "s", "agents", "a"), Record: launch.Record{AgentPID: 1, TeamProfile: "alpha", Session: "s", Handle: "a"}},
			{AgentDir: filepath.Join(project, ".agent-mail", "beta", "s", "agents", "b"), Record: launch.Record{AgentPID: 2, TeamProfile: "beta", Session: "s", Handle: "b"}},
		}, nil
	}
	_, err := resolveCanonicalContext(contextResolveOptions{})
	if err == nil {
		t.Fatal("expected same-rank live-launch ambiguity")
	}
	message := err.Error()
	for _, want := range []string{"ambiguous profile", "no winner", "every candidate", "alpha", "beta", "agents/a", "agents/b", contextSourceLaunch, contextSourceAMQRC, contextSourceDefault, "lower precedence"} {
		if !strings.Contains(message, want) {
			t.Errorf("ambiguity missing %q: %s", want, message)
		}
	}
}

func TestResolveCanonicalContextSharedTupleDoesNotRequireHandle(t *testing.T) {
	project := t.TempDir()
	isolateCanonicalContextTest(t, project)
	root := filepath.Join(project, ".agent-mail", "release", "shared")
	contextPIDAlive = func(int) bool { return true }
	contextScanLaunchEntries = func(string) ([]launch.Entry, error) {
		return []launch.Entry{
			{AgentDir: filepath.Join(root, "agents", "cto"), Record: launch.Record{AgentPID: 1, TeamProfile: "release", Session: "shared", Handle: "cto", Root: root, BaseRoot: root, Tmux: &launch.TmuxInfo{PaneID: "%7"}}},
			{AgentDir: filepath.Join(root, "agents", "qa"), Record: launch.Record{AgentPID: 2, TeamProfile: "release", Session: "shared", Handle: "qa", Root: root, BaseRoot: root}},
		}, nil
	}
	if err := team.WriteProfile(project, "release", team.Team{Members: []team.Member{
		{Role: "cto", Binary: "codex", Handle: "cto", Session: "shared"},
		{Role: "qa", Binary: "codex", Handle: "qa", Session: "shared"},
	}}); err != nil {
		t.Fatal(err)
	}

	ctx, err := resolveCanonicalContext(contextResolveOptions{ProjectFlag: project, ProjectExplicit: true})
	if err != nil {
		t.Fatal(err)
	}
	if ctx.Profile != "release" || ctx.Session != "shared" || ctx.Root != root || ctx.Handle != "" {
		t.Fatalf("shared tuple = profile %q session %q root %q handle %q", ctx.Profile, ctx.Session, ctx.Root, ctx.Handle)
	}
	var handles []string
	for _, candidate := range ctx.Candidates {
		if candidate.Field == "handle" && candidate.Source == contextSourceLaunch {
			handles = append(handles, candidate.Value)
		}
	}
	sort.Strings(handles)
	if strings.Join(handles, ",") != "cto,qa" {
		t.Fatalf("handle candidates = %v", handles)
	}
	t.Setenv("TMUX_PANE", "%7")
	paneCtx, err := resolveCanonicalContext(contextResolveOptions{ProjectFlag: project, ProjectExplicit: true})
	if err != nil {
		t.Fatal(err)
	}
	if paneCtx.Handle != "cto" || paneCtx.Sources["handle"] != contextSourceLaunch {
		t.Fatalf("pane-matched handle = %q from %q", paneCtx.Handle, paneCtx.Sources["handle"])
	}
	if err := os.Unsetenv("TMUX_PANE"); err != nil {
		t.Fatal(err)
	}

	for name, run := range map[string]func() error{
		"status": func() error { return runStatus([]string{"--project", project, "--session", "shared", "--json"}) },
		"task":   func() error { return runTask([]string{"list", "--project", project, "--session", "shared", "--json"}) },
		"context": func() error {
			return runContextExplain([]string{"--project", project, "--session", "shared", "--json"})
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, stderr, err := captureOutput(t, run)
			if err != nil {
				t.Fatalf("%s rejected shared tuple: %v\n%s", name, err, stderr)
			}
		})
	}
}

func TestBareStatusAndContextResolveNamedExactRootWithoutAMSession(t *testing.T) {
	project := t.TempDir()
	isolateCanonicalContextTest(t, project)
	project, _ = os.Getwd()
	const (
		profile = "review"
		session = "issue-481"
	)
	root := filepath.Join(project, ".agent-mail", profile, session)
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := team.WriteProfile(project, profile, team.Team{Members: []team.Member{{
		Role: "cto", Binary: "codex", Handle: "cto", Session: session,
	}}}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AM_ROOT", root)
	t.Setenv("AM_BASE_ROOT", root)
	t.Setenv("AM_ME", "cto")
	if err := os.Unsetenv("AM_SESSION"); err != nil {
		t.Fatal(err)
	}

	for name, run := range map[string]func() error{
		"status":  func() error { return runStatus([]string{"--json"}) },
		"context": func() error { return runContextExplain([]string{"--json"}) },
	} {
		t.Run(name, func(t *testing.T) {
			_, stderr, err := captureOutput(t, run)
			if err != nil {
				t.Fatalf("bare %s rejected exact-root/sessionless identity: %v\n%s", name, err, stderr)
			}
		})
	}
}

func TestExplicitRootOverridesMalformedInjectedIdentityWithWarning(t *testing.T) {
	project := t.TempDir()
	isolateCanonicalContextTest(t, project)
	t.Setenv("AM_ROOT", filepath.Join(project, ".agent-mail", "stale", "old"))
	if err := os.Unsetenv("AM_BASE_ROOT"); err != nil {
		t.Fatal(err)
	}
	explicitRoot := filepath.Join(project, ".agent-mail", "review", "issue-463")
	ctx, err := resolveCanonicalContext(contextResolveOptions{RootFlag: explicitRoot, RootExplicit: true})
	if err != nil {
		t.Fatalf("complete explicit root should outrank malformed env: %v", err)
	}
	if ctx.Profile != "review" || ctx.Session != "issue-463" || ctx.Root != explicitRoot || ctx.BaseRoot != explicitRoot || ctx.PinMode != "exact_root" {
		t.Fatalf("explicit-root context: %#v", ctx)
	}
	warnings := strings.Join(ctx.Warnings, "\n")
	if !strings.Contains(warnings, "injected AMQ identity is incomplete") {
		t.Fatalf("malformed env warning missing: %v", ctx.Warnings)
	}
	diagnostic := strings.Join(contextDiagnosticLines(ctx), "\n")
	for _, want := range []string{contextSourceFlags, contextSourceEnv, "losing candidate", "incomplete"} {
		if !strings.Contains(diagnostic, want) {
			t.Errorf("diagnostic missing %q:\n%s", want, diagnostic)
		}
	}

	_, err = resolveCanonicalContext(contextResolveOptions{ProfileFlag: "review", ProfileExplicit: true})
	if err == nil || !strings.Contains(err.Error(), "injected AMQ identity is incomplete") {
		t.Fatalf("incomplete explicit tuple should not suppress malformed env: %v", err)
	}
}

func TestExplicitExternalRootWinsForNamedProfile(t *testing.T) {
	project := t.TempDir()
	isolateCanonicalContextTest(t, project)
	externalRoot := filepath.Join(t.TempDir(), "review-mail")

	ctx, err := resolveCanonicalContext(contextResolveOptions{
		ProfileFlag: "review", SessionFlag: "issue-463", RootFlag: externalRoot,
		ProfileExplicit: true, SessionExplicit: true, RootExplicit: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if ctx.Profile != "review" || ctx.Session != "issue-463" || ctx.Root != externalRoot || ctx.BaseRoot != externalRoot || ctx.PinMode != "exact_root" {
		t.Fatalf("explicit external-root context: %#v", ctx)
	}
	if ctx.Sources["root"] != contextSourceFlags || ctx.Sources["base_root"] != contextSourceFlags {
		t.Fatalf("explicit external root sources: %#v", ctx.Sources)
	}
}

func TestExplicitProjectOutranksForeignInjectedRoot(t *testing.T) {
	currentProject := t.TempDir()
	selectedProject := t.TempDir()
	isolateCanonicalContextTest(t, currentProject)
	foreignRoot := filepath.Join(currentProject, ".agent-mail", "foreign", "old")
	t.Setenv("AM_ROOT", foreignRoot)
	t.Setenv("AM_BASE_ROOT", foreignRoot)

	ctx, err := resolveCanonicalContext(contextResolveOptions{ProjectFlag: selectedProject, ProjectExplicit: true})
	if err != nil {
		t.Fatal(err)
	}
	if ctx.ProjectDir != selectedProject {
		t.Fatalf("project = %q, want explicit %q", ctx.ProjectDir, selectedProject)
	}
	if ctx.Root == foreignRoot || !strings.HasPrefix(ctx.Root, filepath.Join(selectedProject, ".agent-mail")) {
		t.Fatalf("foreign injected root won explicit project: %#v", ctx)
	}
	diagnostic := strings.Join(contextDiagnosticLines(ctx), "\n")
	if !strings.Contains(diagnostic, contextSourceEnv) || !strings.Contains(diagnostic, "losing candidate") {
		t.Fatalf("foreign env provenance missing: %s", diagnostic)
	}
}

func TestResolveAMQContextUsesCanonicalCustomRoot(t *testing.T) {
	project := t.TempDir()
	isolateCanonicalContextTest(t, project)
	customBase := filepath.Join(project, "custom-mail")
	writeContextAMQRC(t, project, customBase)

	previous := resolveAMQEnvForAMQCommand
	var selectedRoot, selectedSession string
	resolveAMQEnvForAMQCommand = func(cwd, rootFlag, session, handle string) (amqEnv, error) {
		selectedRoot, selectedSession = rootFlag, session
		return amqEnv{Root: rootFlag, BaseRoot: rootFlag, Me: handle}, nil
	}
	t.Cleanup(func() { resolveAMQEnvForAMQCommand = previous })

	ctx, err := resolveAMQContext(project, "", "issue-463", "cto", true)
	if err != nil {
		t.Fatal(err)
	}
	wantRoot := filepath.Join(customBase, "issue-463")
	if selectedRoot != wantRoot || selectedSession != "" {
		t.Fatalf("AMQ resolver called with root/session %q/%q, want %q/exact-root", selectedRoot, selectedSession, wantRoot)
	}
	if ctx.Root != wantRoot || ctx.Env.Root != wantRoot || ctx.Env.BaseRoot != customBase || ctx.Session != "issue-463" {
		t.Fatalf("resolved custom context: %#v", ctx)
	}
}

func TestContextExplainJSONHumanHelpAndCompletion(t *testing.T) {
	project := t.TempDir()
	isolateCanonicalContextTest(t, project)

	stdout, stderr, err := captureOutput(t, func() error {
		return Run([]string{"context", "explain", "--project", project, "--session", "issue-463", "--json"}, "test")
	})
	if err != nil {
		t.Fatalf("context explain json: %v\nstderr:\n%s", err, stderr)
	}
	var envelope struct {
		SchemaVersion int               `json:"schema_version"`
		Kind          string            `json:"kind"`
		Data          contextResolution `json:"data"`
	}
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatalf("parse context JSON: %v\n%s", err, stdout)
	}
	if envelope.SchemaVersion != JSONSchemaVersion || envelope.Kind != "context_explain" || envelope.Data.Session != "issue-463" || len(envelope.Data.Candidates) == 0 {
		t.Fatalf("unexpected context envelope: %#v", envelope)
	}

	stdout, _, err = captureOutput(t, func() error { return runContextExplain([]string{"--project", project}) })
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# amq-squad context explain", "FIELD", "SOURCE", "WINNER", contextSourceDefault} {
		if !strings.Contains(stdout, want) {
			t.Errorf("human output missing %q:\n%s", want, stdout)
		}
	}

	_, stderr, err = captureOutput(t, func() error { return Run([]string{"context", "explain", "--help"}, "test") })
	if err != nil || !strings.Contains(stderr, "amq-squad context explain") {
		t.Fatalf("context help err=%v stderr=%q", err, stderr)
	}
	for shell, script := range map[string]string{"bash": bashCompletionScript, "zsh": zshCompletionScript, "fish": fishCompletionScript} {
		if !strings.Contains(script, "context") || !strings.Contains(script, "explain") {
			t.Errorf("%s completion missing context explain", shell)
		}
	}
}

func TestContextExplainDegradesForExplicitlyEmptyInjectedSession(t *testing.T) {
	project := t.TempDir()
	isolateCanonicalContextTest(t, project)
	root := filepath.Join(project, ".agent-mail", "review", "issue-463")
	t.Setenv("AM_ROOT", root)
	t.Setenv("AM_BASE_ROOT", root)
	t.Setenv("AM_SESSION", "")
	t.Setenv("AM_ME", "cto")

	stdout, stderr, err := captureOutput(t, func() error { return runContextExplain(nil) })
	if err != nil {
		t.Fatalf("degraded context explain failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	for _, want := range []string{"profile:   review", "session:   issue-463", root, "explicitly empty AM_SESSION"} {
		if !strings.Contains(stdout+stderr, want) {
			t.Fatalf("degraded context explain missing %q:\nstdout:\n%s\nstderr:\n%s", want, stdout, stderr)
		}
	}
	if _, _, statusErr := captureOutput(t, func() error { return runStatus([]string{"--json"}) }); statusErr == nil || !strings.Contains(statusErr.Error(), "explicitly empty AM_SESSION") {
		t.Fatalf("ordinary status must still fail closed, got %v", statusErr)
	}
}

func TestOrdinaryEntrypointsEmitAllContextCandidates(t *testing.T) {
	project := t.TempDir()
	isolateCanonicalContextTest(t, project)
	if err := team.WriteProfile(project, "flags", team.Team{Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "flag-s"}}}); err != nil {
		t.Fatal(err)
	}
	writeContextAMQRC(t, project, filepath.Join(".agent-mail", "configured"))
	envRoot := filepath.Join(project, ".agent-mail", "environment", "env-s")
	t.Setenv("AM_ROOT", envRoot)
	t.Setenv("AM_BASE_ROOT", envRoot)
	t.Setenv("AM_ME", "env-agent")
	liveRoot := filepath.Join(project, ".agent-mail", "live", "live-s")
	contextPIDAlive = func(int) bool { return true }
	contextScanLaunchEntries = func(string) ([]launch.Entry, error) {
		return []launch.Entry{{AgentDir: filepath.Join(liveRoot, "agents", "live-agent"), Record: launch.Record{AgentPID: 1, TeamProfile: "live", Session: "live-s", Handle: "live-agent", Root: liveRoot, BaseRoot: liveRoot}}}, nil
	}

	previousResolve, previousRun := resolveAMQEnvForAMQCommand, runAMQCommand
	resolveAMQEnvForAMQCommand = func(cwd, rootFlag, session, handle string) (amqEnv, error) {
		return amqEnv{Root: rootFlag, BaseRoot: rootFlag, Me: handle}, nil
	}
	runAMQCommand = func(amqCommandRequest) ([]byte, error) { return []byte("[]\n"), nil }
	t.Cleanup(func() {
		resolveAMQEnvForAMQCommand = previousResolve
		runAMQCommand = previousRun
	})

	commands := []struct {
		name string
		run  func() error
	}{
		{"status", func() error {
			return runStatus([]string{"--project", project, "--profile", "flags", "--session", "flag-s", "--json"})
		}},
		{"task", func() error {
			return runTask([]string{"list", "--project", project, "--profile", "flags", "--session", "flag-s", "--json"})
		}},
		{"wrapped-amq", func() error {
			return runAMQPassthrough("list", []string{"--project", project, "--profile", "flags", "--session", "flag-s"})
		}},
	}
	for _, command := range commands {
		t.Run(command.name, func(t *testing.T) {
			_, stderr, err := captureOutput(t, command.run)
			if err != nil {
				t.Fatalf("%s: %v\nstderr:\n%s", command.name, err, stderr)
			}
			for _, want := range []string{contextSourceFlags, contextSourceEnv, contextSourceLaunch, contextSourceAMQRC, contextSourceDefault, "winner", "loser"} {
				if !strings.Contains(stderr, want) {
					t.Errorf("%s diagnostics missing %q:\n%s", command.name, want, stderr)
				}
			}
		})
	}
}

func TestOrdinaryCommandsDoNotSpliceConflictingTuples(t *testing.T) {
	project := t.TempDir()
	isolateCanonicalContextTest(t, project)
	if err := team.WriteProfile(project, "flagprof", team.Team{Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "flagsession"}}}); err != nil {
		t.Fatal(err)
	}
	writeContextAMQRC(t, project, filepath.Join(".agent-mail", "configprof", "configsession"))
	envRoot := filepath.Join(project, ".agent-mail", "envprof", "envsession")
	t.Setenv("AM_ROOT", envRoot)
	t.Setenv("AM_BASE_ROOT", envRoot)
	t.Setenv("AM_ME", "env-agent")
	liveRoot := filepath.Join(project, ".agent-mail", "liveprof", "livesession")
	contextPIDAlive = func(int) bool { return true }
	contextScanLaunchEntries = func(string) ([]launch.Entry, error) {
		return []launch.Entry{{
			AgentDir: filepath.Join(liveRoot, "agents", "live-agent"),
			Record: launch.Record{
				AgentPID: 1, TeamProfile: "liveprof", Session: "livesession", Handle: "live-agent",
				Root: liveRoot, BaseRoot: liveRoot,
			},
		}}, nil
	}

	type amqSelection struct{ root, session, handle string }
	var selections []amqSelection
	previousResolve, previousRun := resolveAMQEnvForAMQCommand, runAMQCommand
	resolveAMQEnvForAMQCommand = func(cwd, rootFlag, session, handle string) (amqEnv, error) {
		selections = append(selections, amqSelection{root: rootFlag, session: session, handle: handle})
		return amqEnv{Root: rootFlag, BaseRoot: rootFlag, Me: handle}, nil
	}
	runAMQCommand = func(amqCommandRequest) ([]byte, error) { return []byte("[]\n"), nil }
	t.Cleanup(func() {
		resolveAMQEnvForAMQCommand = previousResolve
		runAMQCommand = previousRun
	})

	commands := []struct {
		name string
		run  func() error
	}{
		{"status", func() error {
			return runStatus([]string{"--project", project, "--profile", "flagprof", "--session", "flagsession", "--json"})
		}},
		{"task", func() error {
			return runTask([]string{"list", "--project", project, "--profile", "flagprof", "--session", "flagsession", "--json"})
		}},
		{"wrapped-amq", func() error {
			return runAMQPassthrough("list", []string{"--project", project, "--profile", "flagprof"})
		}},
	}
	for _, command := range commands {
		t.Run(command.name, func(t *testing.T) {
			_, stderr, err := captureOutput(t, command.run)
			if err != nil {
				t.Fatalf("%s failed: %v\n%s", command.name, err, stderr)
			}
			for _, want := range []string{contextSourceEnv, contextSourceLaunch, contextSourceAMQRC, "loser"} {
				if !strings.Contains(stderr, want) {
					t.Errorf("%s diagnostics missing %q:\n%s", command.name, want, stderr)
				}
			}
		})
	}
	if len(selections) != 1 {
		t.Fatalf("wrapped AMQ selections = %v", selections)
	}
	wantFlagRoot := filepath.Join(project, ".agent-mail", "flagprof", "flagsession")
	if canonicalContextComparisonPath(selections[0].root) != canonicalContextComparisonPath(wantFlagRoot) || selections[0].session != "" || selections[0].handle != "" {
		t.Fatalf("explicit-profile AMQ selection spliced old tuple: %#v", selections[0])
	}

	_, stderr, err := captureOutput(t, func() error {
		return runAMQPassthrough("list", []string{"--project", project, "--session", "switched"})
	})
	if err != nil {
		t.Fatalf("symmetric explicit-session AMQ failed: %v\n%s", err, stderr)
	}
	if len(selections) != 2 {
		t.Fatalf("wrapped AMQ selections = %v", selections)
	}
	wantSwitchedRoot := filepath.Join(project, ".agent-mail", "envprof", "switched")
	if canonicalContextComparisonPath(selections[1].root) != canonicalContextComparisonPath(wantSwitchedRoot) || selections[1].session != "" || selections[1].handle != "" {
		t.Fatalf("explicit-session AMQ selection spliced old tuple: %#v", selections[1])
	}
}
