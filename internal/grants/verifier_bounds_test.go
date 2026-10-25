package grants

import (
	"reflect"
	"testing"
	"time"
)

// v3-5-verifier rules 1 and 12: the verifier's bounds are owner-settable
// (verifier_timeout, verifier_output_limit, verifier_parallelism) and default
// to 10 minutes, 1 MiB and 2; a value out of range is refused. The classes
// a squad.yaml adds to the gate with no grant command are named for the
// dispatch refusal.
func TestVerifierBoundsFromTheGrant(t *testing.T) {
	g, err := ParseOwner("schema: 1\nverifier_timeout: 90s\nverifier_output_limit: 65536\nverifier_parallelism: 3\n", "grants.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if to, limit, par := g.VerifierBounds(); to != 90*time.Second || limit != 65536 || par != 3 {
		t.Fatalf("the grant's bounds: %s %d %d", to, limit, par)
	}
	if to, limit, par := Defaults().VerifierBounds(); to != 10*time.Minute || limit != 1<<20 || par != 2 {
		t.Fatalf("the default bounds: %s %d %d", to, limit, par)
	}
	for name, text := range map[string]string{
		"a zero timeout":       "schema: 1\nverifier_timeout: 0s\n",
		"over a day":           "schema: 1\nverifier_timeout: 25h\n",
		"not a duration":       "schema: 1\nverifier_timeout: ten\n",
		"a tiny output limit":  "schema: 1\nverifier_output_limit: 10\n",
		"a zero parallelism":   "schema: 1\nverifier_parallelism: 0\n",
		"a negative parallism": "schema: 1\nverifier_parallelism: -1\n",
	} {
		if _, err := ParseOwner(text, "grants.yaml"); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	g, err = ParseOwner("schema: 1\nverifiers:\n  script: /opt/check\n", "grants.yaml")
	if err != nil {
		t.Fatal(err)
	}
	n, err := ParseSquadNarrowing("verifier_classes: [script, implement, extract]\n", "squad.yaml")
	if err != nil {
		t.Fatal(err)
	}
	out, _ := Narrow(g, n)
	if got := out.UnconfiguredGate(); !reflect.DeepEqual(got, []string{"extract", "implement"}) {
		t.Fatalf("the gate's classes with no command: %v", got)
	}
}
