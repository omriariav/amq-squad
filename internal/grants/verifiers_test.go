package grants

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Phase 5 re-pass, ruling on ruling 1 and the reviewer's correction: the
// per-class verifier command is the owner grant's `verifiers:` map (classes
// with a mechanical verifier only, a one-line command each, the strict
// subset). A squad.yaml's verifier_classes may add classes to the verifier
// gate (stricter: an added class requires a verdict with no command) but
// never remove one the grant verifies: a list that omits one is
// grants_invalid.
func TestVerifiersFromTheGrantAndOnlyNarrowed(t *testing.T) {
	g, err := ParseOwner("schema: 1\nverifiers:\n  implement: go test ./...\n  script: make check\n", "grants.yaml")
	if err != nil || !reflect.DeepEqual(g.Verifiers, map[string]string{"implement": "go test ./...", "script": "make check"}) {
		t.Fatalf("verifiers %+v, %v", g.Verifiers, err)
	}
	for name, text := range map[string]string{
		"a class with no verifier": "schema: 1\nverifiers:\n  plan: run\n",
		"an unknown class":         "schema: 1\nverifiers:\n  deploy: run\n",
		"an empty command":         "schema: 1\nverifiers:\n  implement: \"\"\n",
		"a scalar, not a map":      "schema: 1\nverifiers: go test\n",
		"a repeated class":         "schema: 1\nverifiers:\n  implement: a\n  implement: b\n",
	} {
		if g, err := ParseOwner(text, "grants.yaml"); err == nil {
			t.Errorf("%s accepted: %+v", name, g.Verifiers)
		}
	}
	// a superset adds a class to the gate; the grant's commands stay
	n, err := ParseSquadNarrowing("verifier_classes: [implement, script, extract]\n", "squad.yaml")
	if err != nil {
		t.Fatal(err)
	}
	out, ignored := Narrow(g, n)
	if !reflect.DeepEqual(out.Verifiers, g.Verifiers) || !reflect.DeepEqual(out.VerifierGate, []string{"extract"}) || len(ignored) != 0 {
		t.Fatalf("superset: verifiers %+v gate %v ignored %v", out.Verifiers, out.VerifierGate, ignored)
	}
	if !out.VerifierRequired("extract") || out.VerifierFor("extract") != "" || out.VerifierRequired("summarize") {
		t.Fatalf("gate: extract %v %q, summarize %v", out.VerifierRequired("extract"), out.VerifierFor("extract"), out.VerifierRequired("summarize"))
	}
	if _, err := ParseSquadNarrowing("verifier_classes: [review]\n", "squad.yaml"); err == nil {
		t.Fatal("a squad.yaml named a class with no verifier")
	}
	// a list that omits a class the grant verifies never removes it: Narrow
	// keeps it and names the attempt, and the repository's file is
	// grants_invalid (LoadFrom refuses it)
	n, _ = ParseSquadNarrowing("verifier_classes: [script]\n", "squad.yaml")
	if out, ignored := Narrow(g, n); out.VerifierFor("implement") == "" || len(ignored) != 1 {
		t.Fatalf("omission: verifiers %+v ignored %v", out.Verifiers, ignored)
	}
	dir := t.TempDir()
	owner := filepath.Join(dir, "grants.yaml")
	writeFile(t, owner, "schema: 1\nverifiers:\n  implement: go test ./...\n", 0o600)
	for _, list := range []string{"[]", "[script]"} {
		writeFile(t, filepath.Join(dir, "squad.yaml"), "verifier_classes: "+list+"\n", 0o644)
		if r, err := LoadFrom(owner, dir); err == nil || !strings.Contains(err.Error(), "omits implement") {
			t.Fatalf("verifier_classes %s omitting implement: %+v %v", list, r.Verifiers, err)
		}
	}
	writeFile(t, filepath.Join(dir, "squad.yaml"), "verifier_classes: [implement, script]\n", 0o644)
	if r, err := LoadFrom(owner, dir); err != nil || !r.VerifierRequired("script") || r.VerifierFor("implement") == "" {
		t.Fatalf("superset through LoadFrom: %+v %v", r.Grants, err)
	}
}
