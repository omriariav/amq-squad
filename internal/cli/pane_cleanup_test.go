package cli

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/tmuxpane"
)

type paneCleanupFixture struct {
	req  PaneCleanupRequest
	pane tmuxpane.TmuxPane
}

func newPaneCleanupFixture(t *testing.T) paneCleanupFixture {
	t.Helper()
	project := t.TempDir()
	cwd := filepath.Join(project, "member")
	if err := os.Mkdir(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(project, ".agent-mail")
	root := filepath.Join(base, "issue-465")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	tmux := &launch.TmuxInfo{Session: "mux-465", WindowID: "@7", WindowName: "cto", PaneID: "%9", Target: "new-window"}
	rec := launch.Record{
		CWD: cwd, Binary: "codex", Session: "issue-465", Handle: "cto", Role: "cto",
		Root: root, BaseRoot: base, TeamProfile: "release", TeamHome: project,
		AdoptionMode: "managed_window", AgentPID: 300,
		Tmux: tmux, Terminal: launch.TerminalInfoFromTmux(tmux),
	}
	return paneCleanupFixture{
		req: PaneCleanupRequest{Requested: true, Record: rec,
			Scope: PaneCleanupScope{ProjectDir: project, TeamHome: project, Profile: "release", Root: root,
				BaseRoot: base, Session: "issue-465", Role: "cto", Handle: "cto", Binary: "codex", CWD: cwd},
			Attestation: PaneCleanupAgentAttestation{PID: 300, Binary: "codex", Live: true, BinaryMatch: true}},
		pane: tmuxpane.TmuxPane{Session: "mux-465", WindowID: "@7", PaneID: "%9", PID: 100, CWD: cwd,
			Title: "operator changed this title", WindowName: "renamed-diagnostic-only"},
	}
}

func cleanupDeps(inspections []tmuxpane.PaneInspection, closeErr error, closeCalls *int) PaneCleanupDependencies {
	index := 0
	return PaneCleanupDependencies{
		Inspect: func(string) tmuxpane.PaneInspection {
			if index >= len(inspections) {
				return tmuxpane.PaneInspection{State: tmuxpane.PaneInspectionUnavailable, Detail: "unexpected extra inspection"}
			}
			got := inspections[index]
			index++
			return got
		},
		Close: func(string) error {
			*closeCalls++
			return closeErr
		},
		ChildrenIndex: func() (func(int) []int, error) {
			return func(pid int) []int {
				if pid == 100 {
					return []int{200}
				}
				if pid == 200 {
					return []int{300}
				}
				return nil
			}, nil
		},
	}
}

func foundPane(p tmuxpane.TmuxPane) tmuxpane.PaneInspection {
	return tmuxpane.PaneInspection{State: tmuxpane.PaneInspectionFound, Pane: p}
}

func TestPaneCleanupExactPositiveClosesOnce(t *testing.T) {
	fx := newPaneCleanupFixture(t)
	closeCalls := 0
	deps := cleanupDeps([]tmuxpane.PaneInspection{foundPane(fx.pane), foundPane(fx.pane)}, nil, &closeCalls)
	snapshots := 0
	originalSnapshot := deps.ChildrenIndex
	deps.ChildrenIndex = func() (func(int) []int, error) {
		snapshots++
		return originalSnapshot()
	}
	prepared := PreparePaneCleanup(fx.req, deps)
	if !prepared.Ready {
		t.Fatalf("preparation not ready: %+v", prepared.Result)
	}
	result := ClosePreparedPane(prepared, deps)
	if result.Outcome != PaneCleanupClosed || closeCalls != 1 || snapshots != 1 {
		t.Fatalf("result=%+v close calls=%d snapshots=%d, want closed/1/1", result, closeCalls, snapshots)
	}
	if result.Recovery == nil || result.Recovery.InitialPane == nil || result.Recovery.CurrentPane == nil {
		t.Fatalf("closed result lacks pane-identity recovery evidence: %+v", result.Recovery)
	}
}

func TestPaneCleanupSymlinkEqualAndLexicalPrefixNegative(t *testing.T) {
	t.Run("symlink equal", func(t *testing.T) {
		fx := newPaneCleanupFixture(t)
		link := filepath.Join(t.TempDir(), "cwd-link")
		if err := os.Symlink(fx.req.Record.CWD, link); err != nil {
			t.Fatal(err)
		}
		fx.pane.CWD = link
		closeCalls := 0
		deps := cleanupDeps([]tmuxpane.PaneInspection{foundPane(fx.pane), foundPane(fx.pane)}, nil, &closeCalls)
		prepared := PreparePaneCleanup(fx.req, deps)
		if !prepared.Ready || ClosePreparedPane(prepared, deps).Outcome != PaneCleanupClosed {
			t.Fatalf("symlink-equal cwd should close: %+v", prepared.Result)
		}
	})
	t.Run("lexical prefix is not identity", func(t *testing.T) {
		fx := newPaneCleanupFixture(t)
		other := fx.req.Record.CWD + "-other"
		if err := os.Mkdir(other, 0o755); err != nil {
			t.Fatal(err)
		}
		fx.pane.CWD = other
		closeCalls := 0
		prepared := PreparePaneCleanup(fx.req, cleanupDeps([]tmuxpane.PaneInspection{foundPane(fx.pane)}, nil, &closeCalls))
		if prepared.Result.Outcome != PaneCleanupPreservedIdentityUnconfirmed || closeCalls != 0 {
			t.Fatalf("prefix cwd result=%+v close calls=%d", prepared.Result, closeCalls)
		}
	})
}

func TestPaneCleanupRootAndBaseRootCanonicalExisting(t *testing.T) {
	t.Run("symlink equal", func(t *testing.T) {
		fx := newPaneCleanupFixture(t)
		links := t.TempDir()
		baseLink := filepath.Join(links, "base")
		rootLink := filepath.Join(links, "root")
		if err := os.Symlink(fx.req.Record.BaseRoot, baseLink); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(fx.req.Record.Root, rootLink); err != nil {
			t.Fatal(err)
		}
		fx.req.Scope.BaseRoot = baseLink
		fx.req.Scope.Root = rootLink
		closeCalls := 0
		prepared := PreparePaneCleanup(fx.req, cleanupDeps([]tmuxpane.PaneInspection{foundPane(fx.pane)}, nil, &closeCalls))
		if !prepared.Ready {
			t.Fatalf("symlink-equal root/base should prepare: %+v", prepared.Result)
		}
	})

	for _, name := range []string{"missing root", "broken root symlink", "missing base root", "broken base symlink"} {
		t.Run(name, func(t *testing.T) {
			fx := newPaneCleanupFixture(t)
			missing := filepath.Join(t.TempDir(), "missing")
			candidate := missing
			if name == "broken root symlink" || name == "broken base symlink" {
				candidate = filepath.Join(t.TempDir(), "broken")
				if err := os.Symlink(missing, candidate); err != nil {
					t.Fatal(err)
				}
			}
			if name == "missing root" || name == "broken root symlink" {
				fx.req.Record.Root = candidate
			} else {
				fx.req.Record.BaseRoot = candidate
			}
			closeCalls := 0
			prepared := PreparePaneCleanup(fx.req, cleanupDeps([]tmuxpane.PaneInspection{foundPane(fx.pane)}, nil, &closeCalls))
			if prepared.Result.Outcome != PaneCleanupPreservedIdentityUnconfirmed || closeCalls != 0 {
				t.Fatalf("outcome=%q close calls=%d", prepared.Result.Outcome, closeCalls)
			}
		})
	}
}

func TestPaneCleanupCWDAndInspectionFailuresFailClosed(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*paneCleanupFixture)
		state  tmuxpane.PaneInspectionState
		want   PaneCleanupOutcome
	}{
		{name: "empty recorded cwd", mutate: func(f *paneCleanupFixture) { f.req.Record.CWD = "" }, state: tmuxpane.PaneInspectionFound, want: PaneCleanupPreservedIdentityUnconfirmed},
		{name: "missing recorded cwd", mutate: func(f *paneCleanupFixture) { f.req.Record.CWD = filepath.Join(f.req.Record.CWD, "missing") }, state: tmuxpane.PaneInspectionFound, want: PaneCleanupPreservedIdentityUnconfirmed},
		{name: "empty pane cwd", mutate: func(f *paneCleanupFixture) { f.pane.CWD = "" }, state: tmuxpane.PaneInspectionFound, want: PaneCleanupPreservedIdentityUnconfirmed},
		{name: "missing pane cwd", mutate: func(f *paneCleanupFixture) { f.pane.CWD = filepath.Join(f.pane.CWD, "missing") }, state: tmuxpane.PaneInspectionFound, want: PaneCleanupPreservedIdentityUnconfirmed},
		{name: "broken symlink pane cwd", mutate: func(f *paneCleanupFixture) {
			link := filepath.Join(t.TempDir(), "broken")
			if err := os.Symlink(filepath.Join(t.TempDir(), "absent"), link); err != nil {
				t.Fatal(err)
			}
			f.pane.CWD = link
		}, state: tmuxpane.PaneInspectionFound, want: PaneCleanupPreservedIdentityUnconfirmed},
		{name: "inspection unavailable", state: tmuxpane.PaneInspectionUnavailable, want: PaneCleanupInspectionUnavailable},
		{name: "inspection denied", state: tmuxpane.PaneInspectionUnavailable, want: PaneCleanupInspectionUnavailable},
		{name: "inspection malformed", state: tmuxpane.PaneInspectionMalformed, want: PaneCleanupInspectionUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fx := newPaneCleanupFixture(t)
			if tt.mutate != nil {
				tt.mutate(&fx)
			}
			closeCalls := 0
			inspection := tmuxpane.PaneInspection{State: tt.state, Pane: fx.pane, Detail: tt.name}
			prepared := PreparePaneCleanup(fx.req, cleanupDeps([]tmuxpane.PaneInspection{inspection}, nil, &closeCalls))
			if prepared.Result.Outcome != tt.want || closeCalls != 0 {
				t.Fatalf("outcome=%q close calls=%d, want %q/0; %+v", prepared.Result.Outcome, closeCalls, tt.want, prepared.Result)
			}
		})
	}
}

func TestPaneCleanupCanonicalDirUnavailableFailsClosed(t *testing.T) {
	fx := newPaneCleanupFixture(t)
	link := filepath.Join(t.TempDir(), "pane-cwd")
	if err := os.Symlink(fx.req.Record.CWD, link); err != nil {
		t.Fatal(err)
	}
	fx.pane.CWD = link
	closeCalls := 0
	deps := cleanupDeps([]tmuxpane.PaneInspection{foundPane(fx.pane)}, nil, &closeCalls)
	deps.CanonicalDir = func(path string) (string, error) {
		if path == link {
			return "", errors.New("permission denied resolving pane cwd")
		}
		return cleanupCanonicalDir(path)
	}
	prepared := PreparePaneCleanup(fx.req, deps)
	if prepared.Result.Outcome != PaneCleanupPreservedIdentityUnconfirmed || closeCalls != 0 {
		t.Fatalf("outcome=%q close calls=%d, want preserved/0", prepared.Result.Outcome, closeCalls)
	}
}

func TestPaneCleanupRecordFieldMismatchesFailClosed(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*paneCleanupFixture)
	}{
		{name: "project", mutate: func(f *paneCleanupFixture) { f.req.Scope.ProjectDir = t.TempDir() }},
		{name: "team home", mutate: func(f *paneCleanupFixture) { f.req.Scope.TeamHome = t.TempDir() }},
		{name: "profile", mutate: func(f *paneCleanupFixture) { f.req.Record.TeamProfile = "other" }},
		{name: "root", mutate: func(f *paneCleanupFixture) { f.req.Record.Root += "-other" }},
		{name: "base root", mutate: func(f *paneCleanupFixture) { f.req.Record.BaseRoot += "-other" }},
		{name: "session", mutate: func(f *paneCleanupFixture) { f.req.Record.Session = "other" }},
		{name: "role", mutate: func(f *paneCleanupFixture) { f.req.Record.Role = "qa" }},
		{name: "handle", mutate: func(f *paneCleanupFixture) { f.req.Record.Handle = "qa" }},
		{name: "binary", mutate: func(f *paneCleanupFixture) { f.req.Record.Binary = "claude" }},
		{name: "pane id", mutate: func(f *paneCleanupFixture) { f.req.Record.Tmux.PaneID = "%10" }},
		{name: "tmux session", mutate: func(f *paneCleanupFixture) { f.req.Record.Tmux.Session = "other" }},
		{name: "window", mutate: func(f *paneCleanupFixture) { f.req.Record.Tmux.WindowID = "@8" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fx := newPaneCleanupFixture(t)
			tt.mutate(&fx)
			closeCalls := 0
			prepared := PreparePaneCleanup(fx.req, cleanupDeps([]tmuxpane.PaneInspection{foundPane(fx.pane)}, nil, &closeCalls))
			if prepared.Result.Outcome != PaneCleanupPreservedIdentityUnconfirmed || closeCalls != 0 {
				t.Fatalf("outcome=%q close calls=%d mismatches=%+v", prepared.Result.Outcome, closeCalls, prepared.Result.Mismatches)
			}
		})
	}
}

func TestPaneCleanupResumeAbsoluteRecordedBinaryMatchesBareConfigured(t *testing.T) {
	fx := newPaneCleanupFixture(t)
	absoluteBinary := filepath.Join(t.TempDir(), "codex")
	if err := os.WriteFile(absoluteBinary, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	fx.req.Record.Binary = absoluteBinary
	fx.req.Attestation.Binary = absoluteBinary
	closeCalls := 0
	deps := cleanupDeps([]tmuxpane.PaneInspection{foundPane(fx.pane), foundPane(fx.pane)}, nil, &closeCalls)
	prepared := PreparePaneCleanup(fx.req, deps)
	if !prepared.Ready {
		t.Fatalf("absolute resume-style binary should prepare against bare configured executable: %+v", prepared.Result)
	}
	if result := ClosePreparedPane(prepared, deps); result.Outcome != PaneCleanupClosed || closeCalls != 1 {
		t.Fatalf("result=%+v close calls=%d, want closed/1", result, closeCalls)
	}
}

func TestPaneCleanupBinaryIdentityNegativeMatrix(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*paneCleanupFixture)
	}{
		{name: "different basename", mutate: func(f *paneCleanupFixture) {
			f.req.Record.Binary = "/opt/agents/claude"
			f.req.Attestation.Binary = "/opt/agents/claude"
		}},
		{name: "prefix lookalike", mutate: func(f *paneCleanupFixture) {
			f.req.Record.Binary = "/opt/agents/codex-helper"
			f.req.Attestation.Binary = "/opt/agents/codex-helper"
		}},
		{name: "suffix lookalike", mutate: func(f *paneCleanupFixture) {
			f.req.Record.Binary = "/opt/agents/my-codex"
			f.req.Attestation.Binary = "/opt/agents/my-codex"
		}},
		{name: "relative observed path", mutate: func(f *paneCleanupFixture) {
			f.req.Record.Binary = "./codex"
			f.req.Attestation.Binary = "./codex"
		}},
		{name: "record attestation explicit path drift", mutate: func(f *paneCleanupFixture) {
			f.req.Record.Binary = "/opt/one/codex"
			f.req.Attestation.Binary = "/opt/two/codex"
		}},
		{name: "configured explicit path drift", mutate: func(f *paneCleanupFixture) {
			f.req.Scope.Binary = "/opt/one/codex"
			f.req.Record.Binary = "/opt/two/codex"
			f.req.Attestation.Binary = "/opt/two/codex"
		}},
		{name: "stale or reused pid", mutate: func(f *paneCleanupFixture) {
			f.req.Attestation.PID++
		}},
		{name: "dead fresh attestation", mutate: func(f *paneCleanupFixture) {
			f.req.Attestation.Live = false
		}},
		{name: "process matcher refused", mutate: func(f *paneCleanupFixture) {
			f.req.Attestation.BinaryMatch = false
		}},
		{name: "missing fresh attestation", mutate: func(f *paneCleanupFixture) {
			f.req.Attestation = PaneCleanupAgentAttestation{}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fx := newPaneCleanupFixture(t)
			tt.mutate(&fx)
			closeCalls := 0
			prepared := PreparePaneCleanup(fx.req, cleanupDeps([]tmuxpane.PaneInspection{foundPane(fx.pane)}, nil, &closeCalls))
			if prepared.Ready || prepared.Result.Outcome != PaneCleanupPreservedIdentityUnconfirmed || closeCalls != 0 {
				t.Fatalf("prepared=%t outcome=%q close calls=%d mismatches=%+v", prepared.Ready, prepared.Result.Outcome, closeCalls, prepared.Result.Mismatches)
			}
		})
	}
}

func TestPaneCleanupInspectedRuntimeFieldMismatchesFailClosed(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*tmuxpane.TmuxPane)
	}{
		{name: "pane id", mutate: func(p *tmuxpane.TmuxPane) { p.PaneID = "%10" }},
		{name: "session", mutate: func(p *tmuxpane.TmuxPane) { p.Session = "other" }},
		{name: "window", mutate: func(p *tmuxpane.TmuxPane) { p.WindowID = "@8" }},
		{name: "missing pane pid", mutate: func(p *tmuxpane.TmuxPane) { p.PID = 0 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fx := newPaneCleanupFixture(t)
			tt.mutate(&fx.pane)
			closeCalls := 0
			prepared := PreparePaneCleanup(fx.req, cleanupDeps([]tmuxpane.PaneInspection{foundPane(fx.pane)}, nil, &closeCalls))
			if prepared.Result.Outcome != PaneCleanupPreservedIdentityUnconfirmed || closeCalls != 0 {
				t.Fatalf("outcome=%q close calls=%d mismatches=%+v", prepared.Result.Outcome, closeCalls, prepared.Result.Mismatches)
			}
		})
	}
}

func TestPaneCleanupExternalLegacyAndTerminalInconsistency(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*paneCleanupFixture)
		want   PaneCleanupOutcome
	}{
		{name: "external bit", mutate: func(f *paneCleanupFixture) { f.req.Record.External = true }, want: PaneCleanupPreservedExternal},
		{name: "external adoption", mutate: func(f *paneCleanupFixture) { f.req.Record.AdoptionMode = "external" }, want: PaneCleanupPreservedExternal},
		{name: "legacy adoption", mutate: func(f *paneCleanupFixture) { f.req.Record.AdoptionMode = "" }, want: PaneCleanupPreservedIdentityUnconfirmed},
		{name: "legacy tmux identity", mutate: func(f *paneCleanupFixture) { f.req.Record.Tmux = nil }, want: PaneCleanupPreservedIdentityUnconfirmed},
		{name: "legacy terminal", mutate: func(f *paneCleanupFixture) { f.req.Record.Terminal = nil }, want: PaneCleanupPreservedIdentityUnconfirmed},
		{name: "target does not match managed mode", mutate: func(f *paneCleanupFixture) {
			f.req.Record.Tmux.Target = "current-window"
			f.req.Record.Terminal.Target = "current-window"
		}, want: PaneCleanupPreservedIdentityUnconfirmed},
		{name: "non tmux terminal", mutate: func(f *paneCleanupFixture) { f.req.Record.Terminal.Backend = "iterm2" }, want: PaneCleanupPreservedIdentityUnconfirmed},
		{name: "inconsistent terminal pane", mutate: func(f *paneCleanupFixture) { f.req.Record.Terminal.PaneID = "%99" }, want: PaneCleanupPreservedIdentityUnconfirmed},
		{name: "inconsistent terminal session", mutate: func(f *paneCleanupFixture) { f.req.Record.Terminal.Session = "other" }, want: PaneCleanupPreservedIdentityUnconfirmed},
		{name: "inconsistent terminal window", mutate: func(f *paneCleanupFixture) { f.req.Record.Terminal.WindowID = "@99" }, want: PaneCleanupPreservedIdentityUnconfirmed},
		{name: "inconsistent terminal target", mutate: func(f *paneCleanupFixture) { f.req.Record.Terminal.Target = "current-window" }, want: PaneCleanupPreservedIdentityUnconfirmed},
		{name: "window name is diagnostic only", mutate: func(f *paneCleanupFixture) { f.req.Record.Terminal.WindowName = "renamed" }, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fx := newPaneCleanupFixture(t)
			tt.mutate(&fx)
			closeCalls := 0
			prepared := PreparePaneCleanup(fx.req, cleanupDeps([]tmuxpane.PaneInspection{foundPane(fx.pane)}, nil, &closeCalls))
			if tt.want == "" {
				if !prepared.Ready {
					t.Fatalf("diagnostic-only field blocked preparation: %+v", prepared.Result)
				}
				return
			}
			if prepared.Result.Outcome != tt.want || closeCalls != 0 {
				t.Fatalf("outcome=%q close calls=%d, want %q/0", prepared.Result.Outcome, closeCalls, tt.want)
			}
		})
	}
}

func TestPaneCleanupAgentAttestationAndAncestryFailClosed(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*paneCleanupFixture)
		deps   func(tmuxpane.PaneInspection, *int) PaneCleanupDependencies
	}{
		{name: "missing pid", mutate: func(f *paneCleanupFixture) { f.req.Record.AgentPID = 0; f.req.Attestation.PID = 0 }},
		{name: "dead pid", mutate: func(f *paneCleanupFixture) { f.req.Attestation.Live = false }},
		{name: "reused pid attestation", mutate: func(f *paneCleanupFixture) { f.req.Attestation.PID = 301 }},
		{name: "binary mismatch", mutate: func(f *paneCleanupFixture) {
			f.req.Attestation.Binary = "claude"
			f.req.Attestation.BinaryMatch = false
		}},
		{name: "missing attestation", mutate: func(f *paneCleanupFixture) { f.req.Attestation = PaneCleanupAgentAttestation{} }},
		{name: "non descendant", deps: func(in tmuxpane.PaneInspection, calls *int) PaneCleanupDependencies {
			d := cleanupDeps([]tmuxpane.PaneInspection{in}, nil, calls)
			d.ChildrenIndex = func() (func(int) []int, error) { return func(int) []int { return nil }, nil }
			return d
		}},
		{name: "process table failure", deps: func(in tmuxpane.PaneInspection, calls *int) PaneCleanupDependencies {
			d := cleanupDeps([]tmuxpane.PaneInspection{in}, nil, calls)
			d.ChildrenIndex = func() (func(int) []int, error) { return nil, errors.New("ps denied") }
			return d
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fx := newPaneCleanupFixture(t)
			if tt.mutate != nil {
				tt.mutate(&fx)
			}
			closeCalls := 0
			deps := cleanupDeps([]tmuxpane.PaneInspection{foundPane(fx.pane)}, nil, &closeCalls)
			if tt.deps != nil {
				deps = tt.deps(foundPane(fx.pane), &closeCalls)
			}
			prepared := PreparePaneCleanup(fx.req, deps)
			if prepared.Result.Outcome != PaneCleanupPreservedIdentityUnconfirmed || closeCalls != 0 {
				t.Fatalf("outcome=%q close calls=%d mismatches=%+v", prepared.Result.Outcome, closeCalls, prepared.Result.Mismatches)
			}
		})
	}
}

func TestPaneCleanupRevalidationAndCloseOutcomes(t *testing.T) {
	tests := []struct {
		name       string
		revalidate func(tmuxpane.TmuxPane) tmuxpane.PaneInspection
		closeErr   error
		want       PaneCleanupOutcome
		wantCalls  int
	}{
		{name: "changed pane pid", revalidate: func(p tmuxpane.TmuxPane) tmuxpane.PaneInspection { p.PID++; return foundPane(p) }, want: PaneCleanupPreservedIdentityUnconfirmed},
		{name: "changed pane id", revalidate: func(p tmuxpane.TmuxPane) tmuxpane.PaneInspection { p.PaneID = "%10"; return foundPane(p) }, want: PaneCleanupPreservedIdentityUnconfirmed},
		{name: "changed session", revalidate: func(p tmuxpane.TmuxPane) tmuxpane.PaneInspection { p.Session = "other"; return foundPane(p) }, want: PaneCleanupPreservedIdentityUnconfirmed},
		{name: "changed window id", revalidate: func(p tmuxpane.TmuxPane) tmuxpane.PaneInspection { p.WindowID = "@8"; return foundPane(p) }, want: PaneCleanupPreservedIdentityUnconfirmed},
		{name: "changed cwd", revalidate: func(p tmuxpane.TmuxPane) tmuxpane.PaneInspection {
			p.CWD = t.TempDir()
			return foundPane(p)
		}, want: PaneCleanupPreservedIdentityUnconfirmed},
		{name: "gone", revalidate: func(tmuxpane.TmuxPane) tmuxpane.PaneInspection {
			return tmuxpane.PaneInspection{State: tmuxpane.PaneInspectionGone}
		}, want: PaneCleanupAlreadyGone},
		{name: "unavailable", revalidate: func(tmuxpane.TmuxPane) tmuxpane.PaneInspection {
			return tmuxpane.PaneInspection{State: tmuxpane.PaneInspectionUnavailable}
		}, want: PaneCleanupInspectionUnavailable},
		{name: "malformed", revalidate: func(tmuxpane.TmuxPane) tmuxpane.PaneInspection {
			return tmuxpane.PaneInspection{State: tmuxpane.PaneInspectionMalformed}
		}, want: PaneCleanupInspectionUnavailable},
		{name: "closer error no retry", revalidate: foundPane, closeErr: errors.New("kill-pane failed"), want: PaneCleanupCloseFailed, wantCalls: 1},
		{name: "title and window name diagnostic only", revalidate: func(p tmuxpane.TmuxPane) tmuxpane.PaneInspection {
			p.Title = "different title"
			p.WindowName = "different label"
			return foundPane(p)
		}, want: PaneCleanupClosed, wantCalls: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fx := newPaneCleanupFixture(t)
			closeCalls := 0
			deps := cleanupDeps([]tmuxpane.PaneInspection{foundPane(fx.pane), tt.revalidate(fx.pane)}, tt.closeErr, &closeCalls)
			prepared := PreparePaneCleanup(fx.req, deps)
			if !prepared.Ready {
				t.Fatalf("unexpected preparation failure: %+v", prepared.Result)
			}
			result := ClosePreparedPane(prepared, deps)
			if result.Outcome != tt.want || closeCalls != tt.wantCalls {
				t.Fatalf("outcome=%q close calls=%d, want %q/%d", result.Outcome, closeCalls, tt.want, tt.wantCalls)
			}
		})
	}
}

func TestPaneCleanupPreparationNeverGatesAgentLifecycle(t *testing.T) {
	fx := newPaneCleanupFixture(t)
	fx.req.Attestation.Live = false
	closeCalls := 0
	prepared := PreparePaneCleanup(fx.req, cleanupDeps(nil, nil, &closeCalls))
	if prepared.Ready || prepared.Result.Outcome != PaneCleanupPreservedIdentityUnconfirmed {
		t.Fatalf("expected pane-close authority withheld: %+v", prepared)
	}

	// The lifecycle caller remains free to terminate the agent. Ready controls
	// only whether ClosePreparedPane is authorized.
	terminated := false
	terminateAgent := func(pid int) { terminated = pid == fx.req.Record.AgentPID }
	terminateAgent(fx.req.Record.AgentPID)
	if !terminated || closeCalls != 0 {
		t.Fatalf("agent lifecycle gated=%v or pane close attempted=%d", !terminated, closeCalls)
	}
}

func TestPaneCleanupNotRequestedAndInitiallyGone(t *testing.T) {
	fx := newPaneCleanupFixture(t)
	closeCalls := 0
	fx.req.Requested = false
	if got := PreparePaneCleanup(fx.req, cleanupDeps(nil, nil, &closeCalls)); got.Result.Outcome != PaneCleanupNotRequested {
		t.Fatalf("not requested outcome = %q", got.Result.Outcome)
	}
	fx.req.Requested = true
	deps := cleanupDeps([]tmuxpane.PaneInspection{{State: tmuxpane.PaneInspectionGone, Detail: "affirmative fallback"}}, nil, &closeCalls)
	if got := PreparePaneCleanup(fx.req, deps); got.Result.Outcome != PaneCleanupAlreadyGone {
		t.Fatalf("initial gone outcome = %q", got.Result.Outcome)
	}
	if closeCalls != 0 {
		t.Fatalf("close calls = %d, want 0", closeCalls)
	}
}
