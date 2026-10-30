package team

import (
	"go/ast"
	"go/parser"
	gotoken "go/token"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/grants"
)

// One source for the key list (P2-3): SquadKeys is the table, the parser's
// grant-key set is built from it, and this test proves the two agree both
// ways. Every table key is recognized by the parser (driven with the key and
// a value that is not valid, so the answer is never "unknown key"), a key
// outside the table is "unknown key", and every key the parser switches on
// (read from the source of ParseSquad, parseDefaults and parseSeats) is in
// the table.
func TestSquadKeyTableAndTheParserAgreeBothWays(t *testing.T) {
	for _, k := range SquadKeys {
		var doc string
		switch {
		case strings.HasPrefix(k.Name, "defaults."):
			doc = "defaults:\n  " + strings.TrimPrefix(k.Name, "defaults.") + ": ?\n" + minimalSquad
		case strings.HasPrefix(k.Name, "seats[]."):
			doc = "schema: 1\nseats:\n  - handle: dev\n    runtime: claude\n    " + strings.TrimPrefix(k.Name, "seats[].") + ": ?\n"
		case k.Name == "schema":
			continue
		default:
			doc = k.Name + ": ?\n" + minimalSquad
		}
		if _, err := ParseSquad(doc, "squad.yaml", grants.Defaults()); err != nil && strings.Contains(err.Error(), "unknown key") {
			t.Errorf("the table key %s is unknown to the parser: %v", k.Name, err)
		}
	}
	for _, doc := range []string{
		"zz_unknown: 1\n" + minimalSquad,
		"defaults:\n  zz_unknown: 1\n" + minimalSquad,
		"schema: 1\nseats:\n  - handle: dev\n    runtime: claude\n    zz_unknown: 1\n",
	} {
		if _, err := ParseSquad(doc, "squad.yaml", grants.Defaults()); err == nil || !strings.Contains(err.Error(), "unknown key") {
			t.Errorf("an unknown key was not refused as unknown: %v\n%s", err, doc)
		}
	}
	inTable := map[string]bool{}
	for _, k := range SquadKeys {
		name := strings.TrimPrefix(strings.TrimPrefix(k.Name, "defaults."), "seats[].")
		inTable[name] = true
	}
	fset := gotoken.NewFileSet()
	file, err := parser.ParseFile(fset, "squad.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var cases []string
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || !slices.Contains([]string{"ParseSquad", "parseDefaults", "parseSeats"}, fn.Name.Name) {
			continue
		}
		ast.Inspect(fn, func(n ast.Node) bool {
			cc, ok := n.(*ast.CaseClause)
			if !ok {
				return true
			}
			for _, e := range cc.List {
				if lit, ok := e.(*ast.BasicLit); ok && lit.Kind == gotoken.STRING {
					v, _ := strconv.Unquote(lit.Value)
					cases = append(cases, strings.TrimPrefix(strings.TrimPrefix(v, "defaults."), "seats."))
				}
			}
			return true
		})
	}
	if len(cases) < 20 {
		t.Fatalf("read only %d parser cases from squad.go", len(cases))
	}
	for _, c := range cases {
		if !inTable[c] {
			t.Errorf("the parser has a case for %q that SquadKeys does not list", c)
		}
	}
	// the grant keys the parser accepts are exactly the table's narrow and add keys
	for _, k := range SquadKeys {
		if (k.Relation == RelNarrow || k.Relation == RelAdd) != slices.Contains(grantKeys, k.Name) {
			t.Errorf("grantKeys and SquadKeys disagree on %s", k.Name)
		}
	}
}
