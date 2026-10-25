package routing

import (
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/task"
)

// v3-5-verifier rule 4, the start check: a class the squad's gate names with
// no command in the owner grant is refused dispatch at the router's start,
// grants_invalid with the class and the missing command named; another
// class still routes.
func TestVerifierGateWithNoCommandIsRefusedAtTheStart(t *testing.T) {
	f := newFixture(t)
	f.g.Verifiers = map[string]string{"script": "/opt/verify-script"}
	f.g.VerifierGate = []string{"implement"}
	r, err := New(Options{Table: f.tbl, Registry: f.reg, Grants: f.g, Seed: "verifier-gate", Now: func() time.Time { return t0 }})
	if err != nil {
		t.Fatal(err)
	}
	_, route := r.Decide(newTask("g1", "implement"), nil)
	if route.Block != task.ReasonGrantsInvalid || !strings.Contains(route.Detail, "class implement") || !strings.Contains(route.Detail, "verifiers.implement") {
		t.Fatalf("a gated class with no command: %+v", route)
	}
	if _, route := r.Decide(newTask("g2", "script"), nil); route.Block != "" || route.Pool == nil {
		t.Fatalf("a class with its command: %+v", route)
	}
}
