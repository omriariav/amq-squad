package console

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/noc"
	"github.com/omriariav/amq-squad/v2/internal/state"
)

// nocTestNow is the deterministic clock all NOC render fixtures age against.
var nocTestNow = time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)

// nocSeedAgent writes a launch.json under
// <projectDir>/.agent-mail/<session>/agents/<handle>/ so noc.Collect (via
// state.BuildWithThresholds) discovers it. Returns the agent dir.
func nocSeedAgent(t *testing.T, projectDir, session, handle string, rec launch.Record) string {
	t.Helper()
	agentDir := filepath.Join(projectDir, noc.AgentMailDirName, session, "agents", handle)
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatalf("mkdir agent dir: %v", err)
	}
	rec.Session = session
	rec.Handle = handle
	if err := launch.Write(agentDir, rec); err != nil {
		t.Fatalf("write launch: %v", err)
	}
	return agentDir
}

func nocSeedPresence(t *testing.T, agentDir, handle, status string, lastSeen time.Time) {
	t.Helper()
	body := `{"schema":1,"handle":"` + handle + `","status":"` + status +
		`","last_seen":"` + lastSeen.UTC().Format(time.RFC3339Nano) + `"}`
	if err := os.WriteFile(filepath.Join(agentDir, "presence.json"), []byte(body), 0o600); err != nil {
		t.Fatalf("write presence: %v", err)
	}
}

func nocSeedTeamProfile(t *testing.T, projectDir string) {
	t.Helper()
	dir := filepath.Join(projectDir, noc.SquadDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir squad dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "team.json"), []byte(`{"schema":2}`), 0o600); err != nil {
		t.Fatalf("write team profile: %v", err)
	}
}

// nocSeedQuestionToOperator drops a needs-you question (addressed to "user") into
// a discovered agent's inbox/new, so the coordination model flags the thread.
func nocSeedQuestionToOperator(t *testing.T, agentDir, from string, created time.Time) {
	t.Helper()
	inbox := filepath.Join(agentDir, "inbox", "new")
	if err := os.MkdirAll(inbox, 0o755); err != nil {
		t.Fatalf("mkdir inbox: %v", err)
	}
	msg := "---json\n" +
		`{"schema":1,"id":"q1","thread":"decision/ship","from":"` + from + `","to":["user"],` +
		`"kind":"question","subject":"ship the migration?",` +
		`"created":"` + created.UTC().Format(time.RFC3339Nano) + `"}` + "\n" +
		"---\n" +
		"Should we ship?\n"
	if err := os.WriteFile(filepath.Join(inbox, "q1.md"), []byte(msg), 0o600); err != nil {
		t.Fatalf("write msg: %v", err)
	}
}

// seedNOCFixture builds a three-project workspace under a temp root:
//
//	alpha  - one alive codex agent (running)
//	beta   - one alive claude agent with a needs-you question to the operator
//	gamma  - one dead codex agent (stopped)
//
// and returns the root plus a deterministic probe.
func seedNOCFixture(t *testing.T) (root string, probe state.Probe) {
	t.Helper()
	root = t.TempDir()

	alpha := filepath.Join(root, "alpha")
	nocSeedTeamProfile(t, alpha)
	aDir := nocSeedAgent(t, alpha, "main", "cto", launch.Record{Binary: "codex", AgentPID: 4001})
	nocSeedPresence(t, aDir, "cto", "active", nocTestNow.Add(-10*time.Second))

	beta := filepath.Join(root, "beta")
	nocSeedTeamProfile(t, beta)
	bDir := nocSeedAgent(t, beta, "main", "qa", launch.Record{Binary: "claude", AgentPID: 5001})
	nocSeedQuestionToOperator(t, bDir, "qa", nocTestNow)

	gamma := filepath.Join(root, "gamma")
	nocSeedTeamProfile(t, gamma)
	gDir := nocSeedAgent(t, gamma, "main", "dev", launch.Record{Binary: "codex", AgentPID: 6001})
	nocSeedPresence(t, gDir, "dev", "offline", nocTestNow.Add(-48*time.Hour))

	probe = state.Probe{
		PIDAlive: func(pid int) bool { return pid == 4001 || pid == 5001 },
		ProcessMatch: func(pid int, predicate func(args string) bool) bool {
			switch pid {
			case 4001, 6001:
				return predicate("codex")
			case 5001:
				return predicate("claude")
			}
			return false
		},
		Now: func() time.Time { return nocTestNow },
	}
	return root, probe
}

// renderNOCOnce collects a fixture and returns the static board exactly as the
// --once path would emit it, in the given color mode.
func renderNOCOnce(t *testing.T, root string, probe state.Probe, mode ColorMode) string {
	t.Helper()
	rebuild := NOCRebuildConfig{
		Roots: []string{root},
		Depth: noc.DefaultDepth,
		Probe: probe,
	}
	ms := noc.Collect(rebuild.Roots, rebuild.Depth, rebuild.Probe, rebuild.Thresholds)
	m := newNOCModel(rebuild)
	m.colorMode = mode
	m.th = newNOCTheme(mode)
	m.ms = ms
	m.ready = true
	m.refreshGuidance()
	return m.staticView()
}

func TestNOCOnce_MultiProjectBoard(t *testing.T) {
	root, probe := seedNOCFixture(t)
	out := renderNOCOnce(t, root, probe, ColorNone)

	// Header pulse counts: 3 squads, 2 live (alpha+beta running), 1 needs-you.
	if !strings.Contains(out, "3 squads") {
		t.Errorf("header pulse missing '3 squads':\n%s", out)
	}
	if !strings.Contains(out, "2 live") {
		t.Errorf("header pulse missing '2 live':\n%s", out)
	}
	if !strings.Contains(out, "1 needs-you") {
		t.Errorf("header pulse missing '1 needs-you':\n%s", out)
	}

	// Project grouping: every project label appears.
	for _, p := range []string{"alpha", "beta", "gamma"} {
		if !strings.Contains(out, p) {
			t.Errorf("project %q missing from board:\n%s", p, out)
		}
	}

	// A needs-you row's TEXT label is present (color-independent).
	if !strings.Contains(out, "needs-you") {
		t.Errorf("expected a 'needs-you' text label in the board:\n%s", out)
	}

	// The --once default leads with the rollup digest sections.
	if !strings.Contains(out, "NEEDS ATTENTION") {
		t.Errorf("--once default should render a NEEDS ATTENTION section:\n%s", out)
	}
	if !strings.Contains(out, "PROJECTS") {
		t.Errorf("--once default should render a PROJECTS rollup section:\n%s", out)
	}
}

func TestNOCOnce_AttentionSortBetaFirst(t *testing.T) {
	root, probe := seedNOCFixture(t)
	out := renderNOCOnce(t, root, probe, ColorNone)

	ai := strings.Index(out, "alpha")
	bi := strings.Index(out, "beta")
	gi := strings.Index(out, "gamma")
	if bi < 0 || ai < 0 || gi < 0 {
		t.Fatalf("missing a project label: alpha=%d beta=%d gamma=%d\n%s", ai, bi, gi, out)
	}
	// Attention-first: beta (needs-you) before alpha (running) before gamma (stopped).
	if !(bi < ai && ai < gi) {
		t.Errorf("attention sort wrong: want beta<alpha<gamma, got beta=%d alpha=%d gamma=%d\n%s", bi, ai, gi, out)
	}
}

func TestNOCOnce_StoppedProjectReadsStopped(t *testing.T) {
	root, probe := seedNOCFixture(t)
	rebuild := NOCRebuildConfig{Roots: []string{root}, Depth: noc.DefaultDepth, Probe: probe}
	ms := noc.Collect(rebuild.Roots, rebuild.Depth, rebuild.Probe, rebuild.Thresholds)
	m := newNOCModel(rebuild)
	m.colorMode = ColorNone
	m.th = newNOCTheme(ColorNone)
	m.ms = ms
	m.ready = true

	// Select gamma's project node so the detail pane renders its stopped agent.
	gammaIdx := -1
	for i, n := range m.nodes() {
		if n.kind == nodeProject && n.label == "gamma" {
			gammaIdx = i
			break
		}
	}
	if gammaIdx < 0 {
		t.Fatalf("gamma project node not found in %d nodes", len(m.nodes()))
	}
	m.cursor = gammaIdx
	detail := m.detailView()
	if !strings.Contains(detail, "stopped") {
		t.Errorf("gamma detail should mark its agent 'stopped':\n%s", detail)
	}
}

func TestNOCOnce_NoColorHasNoEscapeCodes(t *testing.T) {
	root, probe := seedNOCFixture(t)
	out := renderNOCOnce(t, root, probe, ColorNone)
	if strings.Contains(out, "\x1b[") {
		t.Errorf("ColorNone render must not contain ANSI escape codes:\n%q", out)
	}
}

func TestNOCOnce_AsciiFallbackTextLabelsNoEscapes(t *testing.T) {
	root, probe := seedNOCFixture(t)
	out := renderNOCOnce(t, root, probe, ColorAscii)

	if strings.Contains(out, "\x1b[") {
		t.Errorf("ColorAscii render must not contain ANSI escape codes:\n%q", out)
	}
	// State TEXT labels are always present.
	for _, label := range []string{"running", "needs-you", "stopped"} {
		if !strings.Contains(out, label) {
			t.Errorf("ascii render missing text label %q:\n%s", label, out)
		}
	}
	// Ascii markers, not unicode glyphs, on the dumb-terminal fallback.
	if strings.ContainsAny(out, "●◐○⚠✕▾▸►·") {
		t.Errorf("ascii render must not contain unicode glyphs/separators:\n%s", out)
	}
}

func TestNOCOnce_FullColorEmitsEscapes(t *testing.T) {
	root, probe := seedNOCFixture(t)
	out := renderNOCOnce(t, root, probe, ColorFull)
	// The needs-you eye-grab is bold/hot, so full color must emit some ANSI.
	if !strings.Contains(out, "\x1b[") {
		t.Errorf("ColorFull render expected ANSI escape codes, got none:\n%q", out)
	}
}

func TestNOCOnce_NoProjectsGuidance(t *testing.T) {
	root := t.TempDir() // empty: no .agent-mail anywhere
	out := renderNOCOnce(t, root, deterministicNOCProbe(nocTestNow), ColorNone)
	if !strings.Contains(out, "No amq-squad projects found") {
		t.Errorf("empty roots should render guidance, got:\n%s", out)
	}
	if !strings.Contains(out, "amq-squad new team --project <team-home>") ||
		!strings.Contains(out, "amq-squad new session --project <team-home> <name>") {
		t.Errorf("empty roots guidance should point at create verbs, got:\n%s", out)
	}
	if strings.Contains(out, "panic") {
		t.Errorf("guidance must never look like a crash:\n%s", out)
	}
}

func TestNOCOnce_GitCandidateTeamHome(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "candidate")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	out := renderNOCOnce(t, root, deterministicNOCProbe(nocTestNow), ColorNone)
	if !strings.Contains(out, "candidate team-home") {
		t.Fatalf("git candidate should render as a team-home candidate, got:\n%s", out)
	}
}

func TestRunNOC_OnceWritesPlainTextToBuffer(t *testing.T) {
	root, _ := seedNOCFixture(t)
	var buf bytes.Buffer
	err := RunNOC(NOCConfig{
		Roots: []string{root},
		Depth: noc.DefaultDepth,
		Once:  true,
		Out:   &buf,
	})
	if err != nil {
		t.Fatalf("RunNOC --once: %v", err)
	}
	out := buf.String()
	// A bytes.Buffer is not a TTY, so output must be plain text (no escapes).
	if strings.Contains(out, "\x1b[") {
		t.Errorf("--once to a non-TTY writer must be plain text:\n%q", out)
	}
	if !strings.Contains(out, "squad") {
		t.Errorf("--once board missing header pulse:\n%s", out)
	}
}

func deterministicNOCProbe(now time.Time) state.Probe {
	return state.Probe{
		PIDAlive:     func(int) bool { return false },
		ProcessMatch: func(int, func(string) bool) bool { return false },
		Now:          func() time.Time { return now },
	}
}
