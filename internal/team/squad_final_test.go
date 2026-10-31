package team

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/grants"
)

// The squad.yaml final form (brief v3-5-migrate, part A).

func readSquadDoc(t *testing.T) string {
	t.Helper()
	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "squad-yaml.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(doc)
}

// A1: the documented key list is the code's key list, row for row, with the
// relation, and every never-settable key is documented.
func TestSquadYamlKeyListIsDocumentedKeyForKey(t *testing.T) {
	doc := readSquadDoc(t)
	for _, k := range SquadKeys {
		row := "| `" + k.Name + "` |"
		i := strings.Index(doc, row)
		if i < 0 {
			t.Errorf("docs/squad-yaml.md has no table row for the key %s", k.Name)
			continue
		}
		line, _, _ := strings.Cut(doc[i:], "\n")
		if !strings.Contains(line, "| "+string(k.Relation)+" |") {
			t.Errorf("the docs row of %s does not say its relation %q: %s", k.Name, k.Relation, line)
		}
	}
	for _, key := range NeverSettableKeys() {
		if !strings.Contains(doc, "| `"+key+"` |") {
			t.Errorf("docs/squad-yaml.md does not list the never-settable key %s", key)
		}
	}
}

// A1: schema is required, and an unknown key is refused.
func TestSquadYamlSchemaIsRequiredAndAnUnknownKeyIsRefused(t *testing.T) {
	if _, err := ParseSquad("seats:\n  - handle: dev\n    runtime: claude\n", "squad.yaml", grants.Defaults()); err == nil || !strings.Contains(err.Error(), "schema: missing") || CodeOf(err) != CodeInvalid {
		t.Fatalf("a squad.yaml without schema: %v (%s)", err, CodeOf(err))
	}
	_, err := ParseSquad("favourite_colour: green\n"+minimalSquad, "squad.yaml", grants.Defaults())
	if err == nil || !strings.Contains(err.Error(), "unknown key") || CodeOf(err) != CodeInvalid {
		t.Fatalf("an unknown key: %v (%s)", err, CodeOf(err))
	}
}

// A1: verifier_classes only adds: a class is accepted and round trips, and a
// list that omits a class the owner grant verifies widens.
func TestSquadYamlVerifierClassesOnlyAdd(t *testing.T) {
	owner := ownerWith(t, "verifiers:\n  implement: go test ./...\n")
	s, err := ParseSquad("verifier_classes: [implement, script]\n"+minimalSquad, "squad.yaml", owner)
	if err != nil {
		t.Fatal(err)
	}
	again, err := ParseSquad(Render(s), "render", owner)
	if err != nil || strings.Join(again.Grants.VerifierClasses, ",") != "implement,script" {
		t.Fatalf("verifier_classes did not round trip: %v %+v\n%s", err, again.Grants.VerifierClasses, Render(s))
	}
	_, err = ParseSquad("verifier_classes: [script]\n"+minimalSquad, "squad.yaml", owner)
	var w *WidenError
	if !errors.As(err, &w) || w.Field != "verifier_classes" || CodeOf(err) != CodeGrantsInvalid {
		t.Fatalf("a list that drops a verified class: %v", err)
	}
}

// A2: the embedded golden parses to the embedded expected structure, and a
// drift of either side fails with the structure it found.
func TestSquadYamlGoldenParsesToTheExpectedStructure(t *testing.T) {
	if err := GoldenCheck(GoldenYAML, GoldenExpected); err != nil {
		t.Fatal(err)
	}
	var s Squad
	if err := json.Unmarshal(GoldenExpected, &s); err != nil || s.Schema != SquadSchema {
		t.Fatalf("the expected structure: %v %+v", err, s)
	}
	changed := strings.Replace(string(GoldenExpected), `"MaxSeats": 6`, `"MaxSeats": 7`, 1)
	if err := GoldenCheck(GoldenYAML, []byte(changed)); err == nil || !strings.Contains(err.Error(), "expected structure") {
		t.Fatalf("a drifted expected structure passed: %v", err)
	}
	dropped := strings.Replace(string(GoldenYAML), "verifier_classes:\n  - implement\n", "", 1)
	if err := GoldenCheck([]byte(dropped), GoldenExpected); err == nil {
		t.Fatal("a golden fixture that lost a key passed")
	}
	// a fixture that parses to its own expected structure but lacks a key of
	// the list is also drift
	trimmed := strings.Replace(string(GoldenYAML), "  effort: medium\n  tools: coding\n", "  tools: coding\n", 1)
	parsed, err := ParseSquad(trimmed, "trimmed", goldenOwner())
	if err != nil {
		t.Fatal(err)
	}
	regenerated, _ := json.Marshal(parsed)
	if err := GoldenCheck([]byte(trimmed), regenerated); err == nil || !strings.Contains(err.Error(), "lacks the schema 1 key defaults.effort") {
		t.Fatalf("a golden fixture without defaults.effort, with a matching expected structure: %v", err)
	}
	lacking := strings.Replace(string(GoldenYAML), "  effort: medium\n  tools: coding\n", "  tools: coding\n", 1)
	if lacking == string(GoldenYAML) {
		t.Fatal("the fixture edit did not apply")
	}
	if err := GoldenCheck([]byte(lacking), GoldenExpected); err == nil {
		t.Fatal("a golden fixture without defaults.effort passed")
	}
}

// A3: a 3.0.x reader refuses schema 2 with squad_yaml_schema_unsupported and
// names the version, even when the newer file carries keys this reader does
// not know.
func TestSquadYamlSchemaTwoIsRefusedAndNamed(t *testing.T) {
	for _, text := range []string{
		"schema: 2\nseats:\n  - handle: dev\n    runtime: claude\n",
		"future_key: x\nschema: 2\n" + "seats:\n  - handle: dev\n    runtime: claude\n",
	} {
		_, err := ParseSquad(text, "squad.yaml", grants.Defaults())
		var se *SchemaError
		if !errors.As(err, &se) || se.Version != 2 || CodeOf(err) != CodeSchemaUnsupported {
			t.Fatalf("schema 2: %v (%s)", err, CodeOf(err))
		}
		if !strings.Contains(err.Error(), "squad_yaml_schema_unsupported") || !strings.Contains(err.Error(), "schema 2") {
			t.Fatalf("the refusal does not name the code and the version: %v", err)
		}
	}
}

// A4: a squad.yaml that tries any never-settable key is refused at parse
// with the key named, one case per key, flat and (for the map keys) nested.
func TestSquadYamlNeverSettableKeysAreRefusedWithTheKeyNamed(t *testing.T) {
	// the brief's set, written out: the code's list may grow, never shrink
	required := []string{"merge", "merge.push_url", "merge.local_push_ack", "push_url", "local_push_ack",
		"verifiers", "verifier_goproxy", "verifier_timeout", "verifier_output_limit", "verifier_parallelism", "default_branch", "fleet",
		"max_squads", "permitted_repositories", "pins", "registry", "unprotected_paths", "remove_protected_paths"}
	keys := NeverSettableKeys()
	for _, k := range required {
		if !slices.Contains(keys, k) {
			t.Errorf("%s left the never-settable set", k)
		}
		if !IsNeverSettable(k) {
			t.Errorf("%s is not never-settable", k)
		}
	}
	keys = append(slices.Clone(required), keys...)
	slices.Sort(keys)
	keys = slices.Compact(keys)
	for _, key := range keys {
		t.Run(key, func(t *testing.T) {
			_, err := ParseSquad(key+": x\n"+minimalSquad, "squad.yaml", grants.Defaults())
			var w *WidenError
			if !errors.As(err, &w) || w.Field != key || !errors.Is(err, ErrWidens) || CodeOf(err) != CodeGrantsInvalid {
				t.Fatalf("%s: %v (%s)", key, err, CodeOf(err))
			}
			if !strings.Contains(err.Error(), key) {
				t.Fatalf("the refusal does not name %s: %v", key, err)
			}
		})
	}
	nested := map[string]string{
		"merge.push_url":       "merge:\n  - repository: o/r\n    push_url: git@github.com:o/r.git\n    local_push_ack: true\n",
		"merge.local_push_ack": "merge:\n  - repository: o/r\n    local_push_ack: true\n",
		"verifiers":            "verifiers:\n  implement: go test ./...\n",
		"fleet":                "fleet:\n  max_squads: 4\n",
	}
	for key, block := range nested {
		_, err := ParseSquad(block+minimalSquad, "squad.yaml", grants.Defaults())
		var w *WidenError
		if !errors.As(err, &w) || w.Field != key || CodeOf(err) != CodeGrantsInvalid {
			t.Fatalf("nested %s: %v", key, err)
		}
	}
}

// A4, the doc side: a key nested under a never-settable one is also never
// settable (a dotted or flat spelling of it).
func TestNeverSettableCoversTheKeysUnderOne(t *testing.T) {
	for _, key := range []string{"merge.base_branch", "verifiers.implement", "registry.pins"} {
		if !IsNeverSettable(key) {
			t.Errorf("%s is not never-settable", key)
		}
	}
	for _, key := range []string{"mode", "max_seats", "verifier_classes", "merge_repositories", "protected_paths", "merger"} {
		if IsNeverSettable(key) {
			t.Errorf("%s is never-settable but narrows or adds", key)
		}
	}
}
