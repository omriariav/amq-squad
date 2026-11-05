package gateverify

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/gatestore"
	"github.com/omriariav/amq-squad/v3/internal/gatetarget"
)

func TestGrowthApprovalBindsTheResolvedFileAcrossAliases(t *testing.T) {
	f := newFixture(t)
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "real"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "real", "file.go"), []byte("package real\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, alias := range []string{"first-link", "second-link"} {
		if err := os.Symlink(filepath.Join("real", "file.go"), filepath.Join(repo, alias)); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
	}
	resolved := func(alias string) string {
		t.Helper()
		p, err := filepath.EvalSymlinks(filepath.Join(repo, alias))
		if err != nil {
			t.Fatal(err)
		}
		rel, err := filepath.Rel(repo, p)
		if err != nil {
			t.Fatal(err)
		}
		return filepath.ToSlash(rel)
	}
	first := resolved("first-link")
	target := gatetarget.GrowTask("/r/s1", "t1", "protected_paths", `["`+first+`"]`)
	rec, q, err := f.core.Raise(context.Background(), gatestore.Spec{GateID: "widen-t1", Action: "spawn", Target: target, RaisedBy: "lead", Facts: gatestore.SafeFactsWithLinks("task", "", []gatestore.PathLink{{Path: first, Alias: "first-link"}})})
	if err != nil {
		t.Fatal(err)
	}
	if r := f.core.HandleEvent(context.Background(), f.answer(f.owner, tuple("approve", rec), q.EventID, testNow.Add(time.Second))); r.Outcome != "approved" {
		t.Fatalf("approve first alias: %+v", r)
	}
	second := resolved("second-link")
	if second != first {
		t.Fatalf("aliases resolved differently: first=%q second=%q", first, second)
	}
	secondTarget := gatetarget.GrowTask("/r/s1", "t1", "protected_paths", `["`+second+`"]`)
	v := Verifier{Trust: f.trust, Pin: f.core.Pin, Namespace: f.core.Namespace}
	if _, ok := v.AcceptedFor(f.store, "spawn", secondTarget); !ok {
		t.Fatal("the resolved-file approval did not cover a second alias")
	}
}
