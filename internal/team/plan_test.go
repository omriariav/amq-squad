package team

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/grants"
)

// Brief 31-3, section 1: the lead's plan renders squad.yaml. The plan and
// the squad.yaml docs/squad-yaml.md shows as the propose-rendered form are
// the same pair: the documented plan, read strictly, renders the documented
// squad.yaml exactly, and that text parses against a default grant. A key
// the plan does not know, a plan with no goal or no seat, are refused.
func TestThePlanRendersTheDocumentedSquadYAML(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "squad-yaml.md"))
	if err != nil {
		t.Fatal(err)
	}
	section := string(raw)[strings.Index(string(raw), "### The propose-rendered form"):]
	blocks := regexp.MustCompile("(?s)```(json|yaml)\n(.*?)```").FindAllStringSubmatch(section, 2)
	if len(blocks) != 2 || blocks[0][1] != "json" || blocks[1][1] != "yaml" {
		t.Fatalf("the section has no json and yaml pair: %d blocks", len(blocks))
	}
	p, err := ParsePlan(blocks[0][2])
	if err != nil {
		t.Fatal(err)
	}
	text := RenderPlan(p)
	if text != blocks[1][2] {
		t.Fatalf("rendered\n%s\ndocumented\n%s", text, blocks[1][2])
	}
	if _, err := ParseSquad(text, "squad.yaml", grants.Grants{Mode: grants.ModeAuto, MaxSeats: 4}); err != nil {
		t.Fatalf("the rendered squad.yaml does not parse: %v", err)
	}
	if !IsPlanText(blocks[0][2]) || IsPlanText(text) {
		t.Fatal("IsPlanText cannot tell a plan from a squad.yaml")
	}
	for _, bad := range []string{`{"goal":"x","seats":[{"handle":"a","runtime":"pi"}],"mode":"auto"}`, `{"seats":[{"handle":"a","runtime":"pi"}]}`, `{"goal":"x"}`, `{"goal":"x","seats":[{"handle":"a"}]} {}`} {
		if _, err := ParsePlan(bad); err == nil {
			t.Errorf("ParsePlan(%s) accepted", bad)
		}
	}
	if got := DigestPrefix("sha256:" + strings.Repeat("ab", 32) + "@absent"); got != "abababababababab" {
		t.Fatalf("prefix %q", got)
	}
}
