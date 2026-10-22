package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/drafter"
	squadnamespace "github.com/omriariav/amq-squad/v3/internal/namespace"
	"github.com/omriariav/amq-squad/v3/internal/team"
)

func TestBriefRejectsGoalAndSeedFromTogether(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	if err := team.Write(dir, team.Team{Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}}}); err != nil {
		t.Fatal(err)
	}
	err := runBrief([]string{"--goal", "ship it", "--seed-from", "file:./x.md", "--session", "issue-96"})
	if err == nil || !strings.Contains(err.Error(), "--goal and --seed-from are mutually exclusive") {
		t.Fatalf("want mutual-exclusivity error, got %v", err)
	}
}

func TestBriefRequiresGoalOrSeedFrom(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	if err := team.Write(dir, team.Team{Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}}}); err != nil {
		t.Fatal(err)
	}
	err := runBrief([]string{"--session", "issue-96"})
	if err == nil || !strings.Contains(err.Error(), "brief requires --goal TEXT or --seed-from REF") {
		t.Fatalf("want missing-source error, got %v", err)
	}
}

func TestBriefSeedFromFileWritesBrief(t *testing.T) {
	dir := canonicalFilesystemPath(t.TempDir())
	chdir(t, dir)
	if err := team.Write(dir, team.Team{Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}}}); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(dir, "notes.md")
	if err := os.WriteFile(source, []byte("do the thing\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout, _, err := captureOutput(t, func() error {
		return runBrief([]string{"--seed-from", "file:" + source, "--session", "issue-96"})
	})
	if err != nil {
		t.Fatalf("brief --seed-from: %v", err)
	}
	briefPath := squadnamespace.BriefPath(dir, team.DefaultProfile, "issue-96")
	if !strings.Contains(stdout, "wrote brief "+briefPath) {
		t.Errorf("stdout missing wrote-brief line: %s", stdout)
	}
	if strings.Contains(stdout, "amq-squad plan") {
		t.Errorf("stdout names the removed plan verb: %s", stdout)
	}
	got, err := os.ReadFile(briefPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "do the thing") || !strings.Contains(string(got), "source: file:"+source) {
		t.Fatalf("seeded brief = %q, want the body plus provenance frontmatter", got)
	}
}

func TestBriefSeedFromRefusesOverwriteWithoutForce(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	if err := team.Write(dir, team.Team{Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}}}); err != nil {
		t.Fatal(err)
	}
	briefPath := squadnamespace.BriefPath(dir, team.DefaultProfile, "issue-96")
	if err := os.MkdirAll(filepath.Dir(briefPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(briefPath, []byte("# existing\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(dir, "notes.md")
	if err := os.WriteFile(source, []byte("new content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := runBrief([]string{"--seed-from", "file:" + source, "--session", "issue-96"})
	if err == nil || !strings.Contains(err.Error(), "already exists") || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("want already-exists refusal naming --force, got %v", err)
	}
	got, _ := os.ReadFile(briefPath)
	if string(got) != "# existing\n" {
		t.Fatalf("existing brief must be untouched without --force, got %q", got)
	}
	if err := runBrief([]string{"--seed-from", "file:" + source, "--session", "issue-96", "--force"}); err != nil {
		t.Fatalf("brief --force: %v", err)
	}
	got, _ = os.ReadFile(briefPath)
	if !strings.Contains(string(got), "new content") {
		t.Fatalf("--force did not overwrite; got %q", got)
	}
}

func TestBriefDryRunPrintsWithoutWriting(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	if err := team.Write(dir, team.Team{Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}}}); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(dir, "notes.md")
	if err := os.WriteFile(source, []byte("dry run body\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout, _, err := captureOutput(t, func() error {
		return runBrief([]string{"--seed-from", "file:" + source, "--session", "issue-96", "--dry-run"})
	})
	if err != nil {
		t.Fatalf("brief --dry-run: %v", err)
	}
	if !strings.Contains(stdout, "dry run body") {
		t.Fatalf("dry-run stdout missing candidate body: %s", stdout)
	}
	briefPath := squadnamespace.BriefPath(dir, team.DefaultProfile, "issue-96")
	if _, err := os.Stat(briefPath); !os.IsNotExist(err) {
		t.Fatalf("--dry-run must not write the brief: %v", err)
	}
}

func TestBriefGoalPrintsManualPromptAndRefusesWhenInSessionOnly(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	member := team.Member{Role: "dev", Handle: "dev", Binary: "codex", Session: "work"}
	if err := team.Write(dir, team.Team{Members: []team.Member{member}}); err != nil {
		t.Fatal(err)
	}
	installBriefDrafter(t, func(*drafter.Config) (drafter.Resolution, error) {
		return drafter.Resolution{Source: drafter.SourceInSession}, nil
	}, func(context.Context, *drafter.Config, drafter.Request) (drafter.Result, error) {
		return drafter.Result{
			UseInSession: true, Reason: "no external drafter is configured", Remedy: "complete the filled prompt in session",
			Evidence: drafter.Evidence{Backend: drafter.BackendInSession, ExitCode: 0},
		}, nil
	})
	stdout, _, err := captureOutput(t, func() error {
		return runBrief([]string{"--goal", "ship it", "--session", "work"})
	})
	if err == nil || !strings.Contains(err.Error(), "in-session completion") {
		t.Fatalf("want in-session-completion refusal, got %v", err)
	}
	for _, want := range []string{"No brief was staged.", "Manual drafting prompt:", "ship it"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q:\n%s", want, stdout)
		}
	}
	briefPath := squadnamespace.BriefPath(dir, team.DefaultProfile, "work")
	if _, err := os.Stat(briefPath); !os.IsNotExist(err) {
		t.Fatalf("manual-drafting path must not stage a brief: %v", err)
	}
}

func TestBriefGoalRejectsInvalidDraftAndDoesNotWrite(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	member := team.Member{Role: "dev", Handle: "dev", Binary: "codex", Session: "work"}
	if err := team.Write(dir, team.Team{Members: []team.Member{member}}); err != nil {
		t.Fatal(err)
	}
	installBriefDrafter(t, func(*drafter.Config) (drafter.Resolution, error) {
		return drafter.Resolution{Config: &drafter.Config{Backend: drafter.BackendClaude}, Source: drafter.SourceProfile}, nil
	}, func(context.Context, *drafter.Config, drafter.Request) (drafter.Result, error) {
		return drafter.Result{Text: "# work brief\n\n## Goal\nship it\n", Evidence: drafter.Evidence{Backend: drafter.BackendClaude, ExitCode: 0}}, nil
	})
	err := runBrief([]string{"--goal", "ship it", "--session", "work"})
	if err == nil || !strings.Contains(err.Error(), `missing heading "## Source"`) {
		t.Fatalf("invalid brief error = %v", err)
	}
	briefPath := squadnamespace.BriefPath(dir, team.DefaultProfile, "work")
	if _, statErr := os.Stat(briefPath); !os.IsNotExist(statErr) {
		t.Fatalf("invalid draft must not stage a brief: %v", statErr)
	}
}

func TestBriefGoalDraftsAndWritesReviewedDocument(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	member := team.Member{Role: "dev", Handle: "dev", Binary: "codex", Session: "work"}
	if err := team.Write(dir, team.Team{Members: []team.Member{member}}); err != nil {
		t.Fatal(err)
	}
	document := validBriefDraft("work", "ship it", member)
	installBriefDrafter(t, func(*drafter.Config) (drafter.Resolution, error) {
		return drafter.Resolution{Config: &drafter.Config{Chain: []string{drafter.BackendClaude}}, Source: drafter.SourceGlobal}, nil
	}, func(context.Context, *drafter.Config, drafter.Request) (drafter.Result, error) {
		return drafter.Result{Text: document, Evidence: drafter.Evidence{Backend: drafter.BackendClaude, ExitCode: 0}}, nil
	})
	stdout, _, err := captureOutput(t, func() error {
		return runBrief([]string{"--goal", "ship it", "--session", "work"})
	})
	if err != nil {
		t.Fatalf("brief --goal: %v\n%s", err, stdout)
	}
	briefPath := squadnamespace.BriefPath(dir, team.DefaultProfile, "work")
	got, err := os.ReadFile(briefPath)
	if err != nil || string(got) != document {
		t.Fatalf("drafted brief = %q, %v; want the validated document", got, err)
	}
}

func TestValidateBriefDraftExactShape(t *testing.T) {
	member := team.Member{Role: "dev", Handle: "dev", Binary: "codex"}
	valid := validBriefDraft("work", "ship it", member)
	if got, err := validateBriefDraft(valid, "work", "ship it", []team.Member{member}); err != nil || got != valid {
		t.Fatalf("valid brief = %q, %v", got, err)
	}
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "changed goal", body: strings.Replace(valid, "ship it", "ship something else", 1), want: "exact operator goal"},
		{name: "missing source provenance", body: strings.Replace(valid, "the operator goal through the configured drafter", "a template", 1), want: "operator goal and configured drafter"},
		{name: "extra heading", body: strings.Replace(valid, "## Acceptance", "## Risks\n- hidden\n\n## Acceptance", 1), want: "unexpected level-two heading"},
		{name: "changed tuple", body: strings.Replace(valid, "`dev` (`dev`, `codex`)", "`dev` (`other`, `codex`)", 1), want: "missing or changed role"},
		{name: "prose scope", body: strings.Replace(valid, "- Implement", "Implement", 1), want: "only Markdown bullets"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := validateBriefDraft(tt.body, "work", "ship it", []team.Member{member})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("validation error = %v, want %q", err, tt.want)
			}
		})
	}
}

// TestAllMarkdownBulletsAcceptsAsteriskAndPlusMarkers proves gh#760's fix:
// a drafter that emits valid CommonMark "* " or "+ " bullets (e.g. the
// gemini-2.5-flash repro from the issue's 2026-08-30 comment) instead of
// "- " is no longer rejected, in both allMarkdownBullets' generic
// bullet-only sections and matchRoleBullets's exact-prefix
// roster match -- the same nonEmptyTrimmedLines normalization feeds both.
func TestAllMarkdownBulletsAcceptsAsteriskAndPlusMarkers(t *testing.T) {
	member := team.Member{Role: "dev", Handle: "dev", Binary: "codex"}
	valid := validBriefDraft("work", "ship it", member)
	body := valid
	body = strings.Replace(body, "- Implement the reviewed change.", "* Implement the reviewed change.", 1)
	body = strings.Replace(body, "- Do not release or send externally.", "+ Do not release or send externally.", 1)
	body = strings.Replace(body, "- Focused and full validation pass.", "* Focused and full validation pass.", 1)
	body = strings.Replace(body, "- `dev` (`dev`, `codex`)", "* `dev` (`dev`, `codex`)", 1)
	if body == valid {
		t.Fatal("test setup did not actually swap any bullet markers")
	}
	got, err := validateBriefDraft(body, "work", "ship it", []team.Member{member})
	if err != nil {
		t.Fatalf("validateBriefDraft with */+ bullets: %v", err)
	}
	if got != body {
		t.Fatalf("validateBriefDraft returned a rewritten document; want the reviewed input returned verbatim")
	}
}

// TestNormalizeMarkdownBulletMarkerLeavesNonBulletProseUntouched proves the
// gh#760 normalization only rewrites an exact "* "/"+ " leading marker, so
// the Goal and Source sections (never bullet-constrained, and not always
// starting with a bullet at all) are byte-identical afterward -- including
// prose that starts with a marker-like character with no following space.
func TestNormalizeMarkdownBulletMarkerLeavesNonBulletProseUntouched(t *testing.T) {
	for _, line := range []string{
		"ship it",
		"Generated from the operator goal through the configured drafter.",
		"**bold** starts the line",
		"+1 to this idea",
		"*emphasis* at the start",
	} {
		if got := normalizeMarkdownBulletMarker(line); got != line {
			t.Fatalf("normalizeMarkdownBulletMarker(%q) = %q, want unchanged", line, got)
		}
	}
	if got := normalizeMarkdownBulletMarker("* bullet"); got != "- bullet" {
		t.Fatalf("normalizeMarkdownBulletMarker(asterisk marker) = %q", got)
	}
	if got := normalizeMarkdownBulletMarker("+ bullet"); got != "- bullet" {
		t.Fatalf("normalizeMarkdownBulletMarker(plus marker) = %q", got)
	}
}

func validBriefDraft(session, goal string, members ...team.Member) string {
	var roster strings.Builder
	for _, member := range members {
		fmt.Fprintf(&roster, "- `%s` (`%s`, `%s`): Own the scoped implementation.\n", member.Role, memberHandle(member), member.Binary)
	}
	return "# " + session + " brief\n\n" +
		"## Goal\n" + goal + "\n\n" +
		"## Source\nGenerated from the operator goal through the configured drafter.\n\n" +
		"## Scope\n- Implement the reviewed change.\n\n" +
		"## Out of scope\n- Do not release or send externally.\n\n" +
		"## Team shape\n" + roster.String() + "\n" +
		"## Acceptance\n- Focused and full validation pass.\n"
}

// installBriefDrafter swaps the shared drafter seam (resolveDraftConfig,
// runDraft) for one test, so brief --goal runs without a configured drafter.
func installBriefDrafter(t *testing.T, resolve resolveCLIDrafterFunc, run cliDrafterRunner) {
	t.Helper()
	prevResolve, prevRun := resolveDraftConfig, runDraft
	resolveDraftConfig, runDraft = resolve, run
	t.Cleanup(func() { resolveDraftConfig, runDraft = prevResolve, prevRun })
}
