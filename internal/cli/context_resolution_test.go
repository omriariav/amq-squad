package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/team"
)

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
		for _, want := range []string{contextSourceFlags, contextSourceEnv, contextSourceAMQRC, contextSourceDefault, "winner", "loser"} {
			if !strings.Contains(diagnostic, want) {
				t.Errorf("diagnostic missing %q:\n%s", want, diagnostic)
			}
		}
	})
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
			return runStatusWithVersion([]string{"--project", project, "--profile", "flagprof", "--session", "flagsession", "--json"}, "dev")
		}},
		{"task", func() error {
			return runTask([]string{"list", "--project", project, "--profile", "flagprof", "--session", "flagsession", "--json"})
		}},
		{"wrapped-amq", func() error {
			return runAMQ([]string{"route", "--project", project, "--profile", "flagprof", "--to", "peer"})
		}},
	}
	for _, command := range commands {
		t.Run(command.name, func(t *testing.T) {
			_, stderr, err := captureOutput(t, command.run)
			if err != nil {
				t.Fatalf("%s failed: %v\n%s", command.name, err, stderr)
			}
			for _, want := range []string{contextSourceEnv, contextSourceAMQRC, "loser"} {
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
		return runAMQ([]string{"route", "--project", project, "--session", "switched", "--to", "peer"})
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
