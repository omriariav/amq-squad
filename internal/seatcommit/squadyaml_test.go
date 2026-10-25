package seatcommit

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Final form A5: squad.yaml is a repository file, so a seat can put it in
// its diff. It is protected by default in the commit step's policy: the diff
// is refused as protected_path, the widening onto it raises the protected
// growth gate, and only the owner's answer to that exact gate lets the
// commit through. The grant here is the built-in default, which carries no
// protected path of its own beyond the implicit set.
func TestSeatDiffTouchingSquadYamlRaisesTheGrowthGate(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n// edit\n")
	write(t, filepath.Join(f.wt, "squad.yaml"), "schema: 1\nseats:\n  - handle: dev\n    runtime: claude\n")
	tk := f.reported("cmd/")
	res := f.run(tk)
	if res.ReasonCode != ReasonProtected || !strings.Contains(res.Detail, "squad.yaml") {
		t.Fatalf("a seat diff touching squad.yaml: %+v", res)
	}
	blocked, _ := f.store.RefuseCommit(tk.ID, res.ReasonCode, res.Detail, res.Route, f.now)
	widen := WidenOptions{Root: f.root, Task: blocked, Added: []string{"squad.yaml"}, Grants: f.grants, RepoRoot: f.main, Home: f.home}
	d := CheckWiden(widen)
	want := ProtectedTarget(f.root, tk.ID, []string{"squad.yaml"})
	if d.Allowed || d.ReasonCode != ReasonGrowthGate || len(d.Crossings) != 1 || d.Crossings[0].Target != want || d.Crossings[0].Accepted {
		t.Fatalf("want the growth gate for %q, got %+v", want, d)
	}
	f.gates = fakeGates{want: true}
	widen.Gates = f.gates
	d = CheckWiden(widen)
	if !d.Allowed || len(d.Gates) != 1 || d.Gates[0] != want {
		t.Fatalf("with the gate accepted: %+v", d)
	}
	if _, err := f.store.WidenGated(tk.ID, "lead", "squad.yaml change approved by the owner", widen.Added, d.Gates, time.Hour, f.now); err != nil {
		t.Fatal(err)
	}
}
