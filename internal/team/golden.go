package team

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/omriariav/amq-squad/v3/internal/grants"
	"github.com/omriariav/amq-squad/v3/internal/yamlsub"
)

// The embedded golden squad.yaml of schema 1 and the structure it must parse
// to (final form A2, as 2b-3 did for the status contract). doctor runs
// GoldenCheck at start: a build whose parser, defaults or key list drifted
// from the frozen schema fails with squad_yaml_golden_drift.

//go:embed golden/squad-v1.yaml
var GoldenYAML []byte

//go:embed golden/squad-v1.expected.json
var GoldenExpected []byte

// goldenOwner is the owner grant the golden narrows: the built-in defaults
// with the fixture's seat cap room.
func goldenOwner() grants.Grants {
	g := grants.Defaults()
	g.Merge = []grants.Merge{{Repository: "golden/repo", BaseBranch: "main"}}
	g.Verifiers = map[string]string{}
	return g
}

// GoldenCheck parses golden as squad.yaml schema 1 and compares the result
// with expected (a JSON encoding of Squad); every key of SquadKeys must also
// appear in golden, so a key added to the list without the fixture fails.
// It returns nil when the build agrees with the frozen schema.
func GoldenCheck(golden, expected []byte) error {
	parsed, err := ParseSquad(string(golden), "golden squad.yaml", goldenOwner())
	if err != nil {
		return fmt.Errorf("the golden fixture does not parse: %w", err)
	}
	got, err := json.Marshal(parsed)
	if err != nil {
		return err
	}
	var want bytes.Buffer
	if err := json.Compact(&want, expected); err != nil {
		return fmt.Errorf("the expected structure is not JSON: %w", err)
	}
	if !bytes.Equal(got, want.Bytes()) {
		return fmt.Errorf("the golden fixture parses to %s, the expected structure is %s", got, want.Bytes())
	}
	again, err := ParseSquad(Render(parsed), "golden squad.yaml (rendered)", goldenOwner())
	if err != nil {
		return fmt.Errorf("the rendered golden does not parse: %w", err)
	}
	if rt, _ := json.Marshal(again); !bytes.Equal(rt, got) {
		return fmt.Errorf("the golden fixture does not round trip through Render")
	}
	entries, err := yamlsub.Split(string(golden), "golden squad.yaml", true)
	if err != nil {
		return err
	}
	// every key of the list must be present: a top-level key as an entry, a
	// key of defaults or of a seat as a line of that entry's block
	top := map[string]bool{}
	blocks := map[string][]string{}
	for _, e := range entries {
		top[e.Key] = true
		for _, b := range e.Block {
			blocks[e.Key] = append(blocks[e.Key], strings.TrimPrefix(b.Text, "- "))
		}
	}
	for _, k := range SquadKeys {
		parent, sub, nested := strings.Cut(k.Name, ".")
		found := top[k.Name]
		if nested {
			found = slices.ContainsFunc(blocks[strings.TrimSuffix(parent, "[]")], func(line string) bool { return strings.HasPrefix(line, sub+":") })
		}
		if !found {
			return fmt.Errorf("the golden fixture lacks the schema 1 key %s", k.Name)
		}
	}
	return nil
}
