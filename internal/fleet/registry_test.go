package fleet

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func sampleRegistry() Registry {
	return Registry{Schema: Schema,
		Operator: Operator{Root: "/Users/owner/.agent-mail/fleet", Project: "fleet", Session: "fleet"},
		Squads: []Squad{
			{Name: "app", Root: "/Users/owner/code/app/.agent-mail/squad", ProjectPath: "/Users/owner/code/app", Repository: "owner/app", Lead: "lead"},
			{Name: "noc", Root: "/Users/owner/code/noc/.agent-mail/s2", ProjectPath: "/Users/owner/code/noc", Repository: "owner/amq-noc", Lead: "cto", Session: "s2", Project: "amq-noc"},
		}}
}

func TestRenderParseRoundTrip(t *testing.T) {
	r := sampleRegistry()
	text := Render(r)
	got, err := Parse(text, "fleet.yaml")
	if err != nil {
		t.Fatalf("%v\n%s", err, text)
	}
	if !reflect.DeepEqual(got, r) || got.Validate() != nil {
		t.Fatalf("round trip:\n%+v\n%+v\n%s", got, r, text)
	}
	empty, err := Parse(Render(Registry{Schema: Schema}), "e")
	if err != nil || len(empty.Squads) != 0 || empty.Validate() != nil {
		t.Fatalf("empty registry: %+v %v", empty, err)
	}
	if s, _ := got.Find("app"); s.SessionName() != "squad" || s.ProjectName() != "app" {
		t.Fatalf("defaults: session %q project %q", s.SessionName(), s.ProjectName())
	}
}

func TestParseIsStrict(t *testing.T) {
	good := Render(sampleRegistry())
	for name, text := range map[string]string{
		"no schema":       strings.Replace(good, "schema: 1\n", "", 1),
		"schema 2":        strings.Replace(good, "schema: 1", "schema: 2", 1),
		"unknown top key": good + "extra: x\n",
		"unknown squad":   strings.Replace(good, "    lead: lead", "    leader: lead", 1),
		"repeated key":    strings.Replace(good, "    lead: lead", "    lead: lead\n    lead: x", 1),
		"tab":             strings.Replace(good, "  - name: app", "\t- name: app", 1),
		"misplaced":       strings.Replace(good, "  root: /Users/owner/.agent-mail/fleet", "      root: /x", 1),
	} {
		if _, err := Parse(text, "f"); err == nil {
			t.Errorf("%s: parsed", name)
		}
	}
	bad := sampleRegistry()
	bad.Squads[1].Name = "app"
	if bad.Validate() == nil {
		t.Error("a repeated squad name validated")
	}
	bad = sampleRegistry()
	bad.Squads[0].Lead = OperatorHandle
	if bad.Validate() == nil {
		t.Error("the operator handle as a lead validated")
	}
	bad = sampleRegistry()
	bad.Squads[0].Repository = "app"
	if bad.Validate() == nil {
		t.Error("a repository that is not owner/name validated")
	}
}

func TestReadUpdate(t *testing.T) {
	dir := canonicalTemp(t)
	path := filepath.Join(dir, ".amq-squad", "fleet.yaml")
	if _, err := Read(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing file: %v", err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := Update(path, func(r *Registry) error {
				r.Squads = append(r.Squads, Squad{Name: fmt.Sprintf("s%d", i), Root: fmt.Sprintf("/r/s%d", i),
					ProjectPath: fmt.Sprintf("/p/s%d", i), Repository: "owner/x", Lead: "lead"})
				return nil
			})
			if err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	r, err := Read(path)
	if err != nil || len(r.Squads) != 8 {
		t.Fatalf("eight concurrent updates: %d squads, %v", len(r.Squads), err)
	}
	if err := Update(path, func(r *Registry) error { r.Squads[0].Root = "relative"; return nil }); err == nil {
		t.Fatal("an invalid update was written")
	}
	if r2, _ := Read(path); !reflect.DeepEqual(r2, r) {
		t.Fatal("a refused update changed the file")
	}
}

func TestSymlinksRefused(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, ".amq-squad")); err != nil {
		t.Fatal(err)
	}
	err := Update(filepath.Join(dir, ".amq-squad", "fleet.yaml"), func(*Registry) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "symlinked") {
		t.Fatalf("symlinked directory: %v", err)
	}
	target := filepath.Join(outside, "real.yaml")
	if err := os.WriteFile(target, []byte(Render(sampleRegistry())), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(outside, "fleet.yaml")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(link); err == nil {
		t.Fatal("a symlinked fleet.yaml was read")
	}
}

func TestRoutesAreArgv(t *testing.T) {
	r := sampleRegistry()
	noc := r.Squads[1]
	up := LeadToOperatorArgv(r.Operator, noc, "status", StatusThread(noc), "s", "b")
	want := []string{"amq", "send", "--root", noc.Root, "--me", "cto", "--project", "fleet", "--session", "fleet", "--to", "user",
		"--kind", "status", "--thread", "fleet/noc", "--subject", "s", "--body", "b"}
	if !reflect.DeepEqual(up, want) {
		t.Fatalf("lead to operator:\n%q\n%q", up, want)
	}
	down := RelayArgv(r.Operator, noc, "", "fleet/noc", "")
	want = []string{"amq", "send", "--root", r.Operator.Root, "--me", "user", "--project", "amq-noc", "--session", "s2", "--to", "cto",
		"--kind", "answer", "--thread", "fleet/noc"}
	if !reflect.DeepEqual(down, want) {
		t.Fatalf("operator to lead:\n%q\n%q", down, want)
	}
}
