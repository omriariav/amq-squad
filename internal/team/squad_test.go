package team

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/grants"
)

const minimalSquad = "schema: 1\nseats:\n  - handle: dev\n    runtime: claude\n"

func ownerWith(t *testing.T, text string) grants.Grants {
	t.Helper()
	g, err := grants.ParseOwner(text, "owner")
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func TestExampleSquadParsesAndRoundTrips(t *testing.T) {
	s, err := ParseSquad(ExampleSquad, "example", grants.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Seats) != 3 || s.Lead != "lead" || s.Grants.Mode != grants.ModeManual || s.Grants.MaxSeats != 4 {
		t.Fatalf("example = %+v", s)
	}
	seats := s.Resolved()
	if seats[0].Runtime != "claude" || seats[0].Effort != "medium" || seats[2].Runtime != "pi" || seats[2].Effort != "" || seats[2].Model != "" {
		t.Fatalf("defaults applied wrongly: %+v", seats)
	}
	again, err := ParseSquad(Render(s), "render", grants.Defaults())
	if err != nil {
		t.Fatalf("render does not parse: %v\n%s", err, Render(s))
	}
	if !reflect.DeepEqual(again, s) {
		t.Fatalf("round trip:\n%+v\n%+v\n%s", s, again, Render(s))
	}
	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "squad-yaml.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(doc), ExampleSquad) {
		t.Fatal("docs/squad-yaml.md does not carry ExampleSquad verbatim")
	}
}

// Acceptance 1: a field that would widen the owner grant fails to parse with a
// named error.
func TestSquadWideningIsANamedParseError(t *testing.T) {
	owner := ownerWith(t, "mode: manual\nmax_seats: 3\npermitted_runtimes: [claude, codex]\nprivacy_class: local\n")
	cases := []struct{ name, text, field string }{
		{"max_seats above the owner cap", "max_seats: 4\n" + minimalSquad, "max_seats"},
		{"protected path removed with !", "protected_paths:\n  - '!docs/'\n" + minimalSquad, "protected_paths"},
		{"protected path removal key", "unprotected_paths: [docs/]\n" + minimalSquad, "unprotected_paths"},
		{"manual widened to auto", "mode: auto\n" + minimalSquad, "mode"},
		{"privacy widened", "privacy_class: vendor\n" + minimalSquad, "privacy_class"},
		{"runtime outside the owner list", "permitted_runtimes: [pi]\n" + minimalSquad, "permitted_runtimes"},
		{"seat runtime outside the grant", "schema: 1\nseats:\n  - handle: s\n    runtime: pi\n", "seats"},
		{"more seats than the cap", "schema: 1\ndefaults:\n  runtime: claude\nseats:\n  - handle: a\n  - handle: b\n  - handle: c\n  - handle: d\n", "seats"},
		{"owner-only default_branch", "default_branch: dev\n" + minimalSquad, "default_branch"},
		{"owner-only merge grant", "merge:\n  - repository: o/r\n" + minimalSquad, "merge"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseSquad(tc.text, "squad.yaml", owner)
			var w *WidenError
			if !errors.As(err, &w) || !errors.Is(err, ErrWidens) || w.Field != tc.field {
				t.Fatalf("err = %v, want a WidenError on %s", err, tc.field)
			}
			if !strings.Contains(err.Error(), tc.field) || !strings.Contains(err.Error(), "widens the owner grant") {
				t.Fatalf("error does not name the field: %v", err)
			}
		})
	}
	// narrowing is fine
	if _, err := ParseSquad("mode: manual\nmax_seats: 2\nprotected_paths: [docs/]\nprivacy_class: local\n"+minimalSquad, "squad.yaml", owner); err != nil {
		t.Fatalf("narrowing refused: %v", err)
	}
	// auto -> manual narrows (ruling 2)
	if s, err := ParseSquad("mode: manual\n"+minimalSquad, "squad.yaml", grants.Defaults()); err != nil || s.Grants.Mode != grants.ModeManual {
		t.Fatalf("auto -> manual: %+v, %v", s, err)
	}
}

func TestSquadStrictParse(t *testing.T) {
	cases := map[string]string{
		"unknown key":          "colour: red\n" + minimalSquad,
		"unknown seat key":     "schema: 1\nseats:\n  - handle: dev\n    runtime: claude\n    profile: x\n",
		"no schema":            "seats:\n  - handle: dev\n    runtime: claude\n",
		"schema 2":             "schema: 2\nseats:\n  - handle: dev\n    runtime: claude\n",
		"no seats":             "schema: 1\n",
		"bad runtime":          "schema: 1\nseats:\n  - handle: dev\n    runtime: gemini\n",
		"no runtime":           "schema: 1\nseats:\n  - handle: dev\n",
		"repeated handle":      "schema: 1\ndefaults:\n  runtime: claude\nseats:\n  - handle: dev\n  - handle: dev\n",
		"operator handle":      "schema: 1\nseats:\n  - handle: user\n    runtime: claude\n",
		"worktree at the root": "schema: 1\nseats:\n  - handle: p\n    runtime: pi\n    worktree: .\n",
		"cursor-agent effort":  "schema: 1\nseats:\n  - handle: c\n    runtime: cursor-agent\n    effort: high\n",
		"unadapted runtime":    "schema: 1\nseats:\n  - handle: g\n    runtime: gemini\n",
		"lead not a seat":      "lead: boss\n" + minimalSquad,
		"relative control":     "control_root: rel\n" + minimalSquad,
		"bad execution mode":   "execution_mode: wild\n" + minimalSquad,
		"bad max_seats":        "max_seats: lots\n" + minimalSquad,
		"tab":                  "schema: 1\nseats:\n\t- handle: dev\n",
		"bad tool policy":      "schema: 1\nseats:\n  - handle: dev\n    runtime: claude\n    tools: root\n",
		"bad lead mode":        "lead_mode: boss\n" + minimalSquad,
		"repeated top key":     "schema: 1\n" + minimalSquad,
		"bad model token":      "schema: 1\nseats:\n  - handle: dev\n    runtime: claude\n    model: \"a b\"\n",
		"defaults unknown key": "defaults:\n  colour: red\n" + minimalSquad,
	}
	for name, text := range cases {
		if _, err := ParseSquad(text, "squad.yaml", grants.Defaults()); err == nil {
			t.Errorf("%s: accepted", name)
		} else if errors.Is(err, ErrWidens) {
			t.Errorf("%s: reported as widening: %v", name, err)
		}
	}
}

// Guard and squad check read the grant keys through the same function, so
// they agree on the narrowing (acceptance 2 keeps the guard tests unchanged).
func TestSquadNarrowingMatchesGuardReader(t *testing.T) {
	dir := t.TempDir()
	owner := filepath.Join(dir, "grants.yaml")
	if err := os.WriteFile(owner, []byte("max_seats: 8\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	text := "max_seats: 5\nprotected_paths: [docs/]\nmode: manual\n" + minimalSquad
	if err := os.WriteFile(SquadPath(dir), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := LoadSquadWith(owner, dir)
	if err != nil {
		t.Fatal(err)
	}
	guard, err := grants.LoadFrom(owner, dir)
	if err != nil {
		t.Fatal(err)
	}
	ownerOnly, _ := grants.LoadFrom(owner, "")
	mine, _ := grants.Narrow(ownerOnly.Grants, s.Grants)
	if !reflect.DeepEqual(mine, guard.Grants) {
		t.Fatalf("squad narrowing %+v != guard %+v", mine, guard.Grants)
	}
	if _, err := LoadSquadWith(owner, t.TempDir()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("absent squad.yaml = %v", err)
	}
}

// Brief 31-1, rows 8 and 9 (the parse's half): a seat with no worktree
// gets one derived per handle under <repo>/.worktrees/, never the
// repository root; a pi seat may name a PROVIDER/ID model; grok and
// cursor-agent are runtimes, as far as AMQ's adapters map their flags.
func TestSeatWorktree(t *testing.T) {
	if got := SeatWorktree("/r", Seat{Handle: "dev"}); got != "/r/.worktrees/dev" {
		t.Fatal(got)
	}
	if got := SeatWorktree("/r", Seat{Worktree: "../w"}); got != "/w" {
		t.Fatal(got)
	}
	if got := SeatWorktree("/r", Seat{Worktree: "/abs/w"}); got != "/abs/w" {
		t.Fatal(got)
	}
}

func TestSquadAdmitsAPiModelAndTheAdaptedRuntimes(t *testing.T) {
	text := "schema: 1\nseats:\n  - handle: p\n    runtime: pi\n    model: ollama/qwen3.6:27b\n  - handle: g\n    runtime: grok\n    model: grok-4\n    effort: high\n  - handle: c\n    runtime: cursor-agent\n    model: auto\n"
	s, err := ParseSquad(text, "/repo/squad.yaml", grants.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	seats, err := s.LaunchSeats("/repo", func(string) (string, error) { return "# Role\n\nrole text for the seat.\n", nil })
	if err != nil {
		t.Fatal(err)
	}
	if seats[0].Model != "ollama/qwen3.6:27b" || seats[0].Worktree != "/repo/.worktrees/p" || seats[1].Runtime != "grok" || seats[2].Runtime != "cursor-agent" {
		t.Fatalf("seats %+v", seats)
	}
}
