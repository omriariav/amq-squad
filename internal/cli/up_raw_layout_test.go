package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/sealchain"
	"github.com/omriariav/amq-squad/v3/internal/seatlaunch"
)

type downgradeFixtureForm string

const (
	downgradeLegacy      downgradeFixtureForm = "legacy document"
	downgradeInterrupted downgradeFixtureForm = "authenticated raw successor"
	downgradeBadSeal     downgradeFixtureForm = "raw-shaped bad seal"
	downgradeWrongPrev   downgradeFixtureForm = "raw-shaped wrong sequence"
	downgradeNoDocument  downgradeFixtureForm = "no document"
	downgradeNoLayout    downgradeFixtureForm = "neither layout"
)

func interruptedDowngradeUpRun(t *testing.T, kind string, form downgradeFixtureForm) (*upRun, string, string) {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "s")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if form == downgradeNoLayout {
		return &upRun{q: seatlaunch.Squad{BaseRoot: base, Session: "s"}}, "", ""
	}
	seed, err := sealchain.SessionKey(root)
	if err != nil {
		t.Fatal(err)
	}
	name, label, documentPath := "exits", "amq-squad exits queue v1", filepath.Join(root, "extensions", "amq-squad", "scheduler", "exits.json")
	legacyBody := map[string]any{"schema": 1, "queue": []any{}}
	rawBody := []byte(`{"schema":1,"queue":[]}`)
	if kind == "commit" {
		name, label = "commits.qa", "amq-squad commits ledger v1"
		documentPath = filepath.Join(root, "extensions", "amq-squad", "commits", "qa.json")
		legacyBody = map[string]any{"schema": 1, "handle": "qa", "worktree": "/tmp/qa", "branch": "seat/qa", "base": strings.Repeat("a", 40), "commits": []any{}, "at": "1970-01-01T00:00:01Z"}
		rawBody = []byte(`{"schema":1,"handle":"qa","worktree":"/tmp/qa","branch":"seat/qa","base":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","commits":[],"at":"1970-01-01T00:00:02Z"}`)
	}
	if err := os.MkdirAll(filepath.Dir(documentPath), 0o700); err != nil {
		t.Fatal(err)
	}
	legacyLink, err := sealchain.Write(root, name, seed, label, nil, legacyBody, func(link *sealchain.Link) error {
		if form == downgradeNoDocument {
			return nil
		}
		doc := make(map[string]any, len(legacyBody)+1)
		for key, value := range legacyBody {
			doc[key] = value
		}
		doc["seal"] = link
		raw, marshalErr := json.Marshal(doc)
		if marshalErr != nil {
			return marshalErr
		}
		return os.WriteFile(documentPath, raw, 0o600)
	}, time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	headPath := sealchain.HeadPath(root, name)
	if form == downgradeLegacy || form == downgradeNoDocument {
		return &upRun{q: seatlaunch.Squad{BaseRoot: base, Session: "s"}}, documentPath, headPath
	}
	head, err := os.ReadFile(headPath)
	if err != nil {
		t.Fatal(err)
	}
	if form == downgradeWrongPrev {
		wrong := &sealchain.Link{Seq: legacyLink.Seq + 1, Tag: legacyLink.Tag}
		seal := sealchain.NewRawSeal(seed.Key, label, wrong, rawBody, "v3.2.0-rc.2", 1)
		raw, joinErr := sealchain.JoinRaw(rawBody, seal)
		if joinErr != nil {
			t.Fatal(joinErr)
		}
		if err := os.WriteFile(documentPath, raw, 0o600); err != nil {
			t.Fatal(err)
		}
	} else {
		stop := errors.New("stop before head")
		_, err = sealchain.WriteRawBeforeHead(root, name, seed, label, head, rawBody, "v3.2.0-rc.2", 1, func(raw []byte) error {
			return os.WriteFile(documentPath, raw, 0o600)
		}, func(sealchain.RawSeal) error { return stop }, time.Unix(2, 0))
		if !errors.Is(err, stop) {
			t.Fatalf("interrupted fixture = %v", err)
		}
		if form == downgradeBadSeal {
			raw, readErr := os.ReadFile(documentPath)
			if readErr != nil {
				t.Fatal(readErr)
			}
			raw = bytes.Replace(raw, []byte(`"schema":1`), []byte(`"schema":2`), 1)
			if err := os.WriteFile(documentPath, raw, 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	return &upRun{q: seatlaunch.Squad{BaseRoot: base, Session: "s"}}, documentPath, headPath
}

func rawLayoutUpRun(t *testing.T) *upRun {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "s")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	seed, err := sealchain.SessionKey(root)
	if err != nil {
		t.Fatal(err)
	}
	_, err = sealchain.WriteRaw(root, "commits.qa", seed, "test", nil, []byte(`{"schema":1}`), "v3.2.0-rc.2", 1, func([]byte) error { return nil }, time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	return &upRun{q: seatlaunch.Squad{BaseRoot: base, Session: "s"}}
}

func migratedRawLayoutUpRun(t *testing.T) *upRun {
	return migratedRawLayoutUpRunWithLegacySibling(t, false)
}

func mixedRawAndLegacyUpRun(t *testing.T) *upRun {
	return migratedRawLayoutUpRunWithLegacySibling(t, true)
}

func migratedRawLayoutUpRunWithLegacySibling(t *testing.T, sibling bool) *upRun {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "s")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	seed, err := sealchain.SessionKey(root)
	if err != nil {
		t.Fatal(err)
	}
	const name, label = "commits.qa", "test"
	if _, err := sealchain.Write(root, name, seed, label, nil, struct {
		Schema int `json:"schema"`
	}{Schema: 1}, func(*sealchain.Link) error { return nil }, time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
	head, err := os.ReadFile(sealchain.HeadPath(root, name))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sealchain.WriteRawBeforeHead(root, name, seed, label, head, []byte(`{"schema":1}`), "v3.2.0-rc.2", 1, func([]byte) error { return nil }, nil, time.Unix(2, 0)); err != nil {
		t.Fatal(err)
	}
	if sibling {
		if _, err := sealchain.Write(root, "commits.legacy", seed, label, nil, struct {
			Schema int `json:"schema"`
		}{Schema: 1}, func(*sealchain.Link) error { return nil }, time.Unix(2, 0)); err != nil {
			t.Fatal(err)
		}
	}
	return &upRun{q: seatlaunch.Squad{BaseRoot: base, Session: "s"}}
}

func TestUpRefusesEveryBinaryBeforeTheRawLayout(t *testing.T) {
	for _, fixture := range []struct {
		name string
		run  func(*testing.T) *upRun
	}{
		{name: "fresh raw chain", run: rawLayoutUpRun},
		{name: "migrated legacy chain", run: migratedRawLayoutUpRun},
		{name: "migrated chain with legacy sibling", run: mixedRawAndLegacyUpRun},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			u := fixture.run(t)
			for _, version := range []string{"v3.1.2", "v3.2.0-rc.1"} {
				err := u.checkLedgerDowngrade(version)
				if err == nil || !strings.Contains(err.Error(), "predates that layout") || !strings.Contains(err.Error(), "roll forward or start a new session") {
					t.Fatalf("%s: %v", version, err)
				}
			}
		})
	}
	u := migratedRawLayoutUpRun(t)
	for _, version := range []string{"v3.2.0-rc.2", "v3.2.0", "v4.0.0"} {
		if err := u.checkLedgerDowngrade(version); err != nil {
			t.Fatalf("%s: %v", version, err)
		}
	}
}

func TestUpDowngradeInterruptedMigrationMatrix(t *testing.T) {
	forms := []downgradeFixtureForm{
		downgradeLegacy,
		downgradeInterrupted,
		downgradeBadSeal,
		downgradeWrongPrev,
		downgradeNoDocument,
		downgradeNoLayout,
	}
	for _, kind := range []string{"commit", "exits"} {
		for _, form := range forms {
			t.Run(kind+"/"+string(form), func(t *testing.T) {
				u, documentPath, headPath := interruptedDowngradeUpRun(t, kind, form)
				var beforeDocument, beforeHead []byte
				if documentPath != "" {
					beforeDocument, _ = os.ReadFile(documentPath)
				}
				if headPath != "" {
					beforeHead, _ = os.ReadFile(headPath)
				}
				err := u.checkLedgerDowngrade("v3.1.2")
				if form == downgradeInterrupted {
					var refusal *upRefusal
					if !errors.As(err, &refusal) || refusal.Name != "ledger_layout_downgrade" || !strings.Contains(err.Error(), "predates that layout") {
						t.Fatalf("interrupted migration downgrade = %v", err)
					}
					if err := u.checkLedgerDowngrade("v3.2.0-rc.1"); err == nil {
						t.Fatal("rc.1 downgrade was admitted")
					}
					for _, version := range []string{"v3.2.0-rc.2", "v3.2.0", "v4.0.0"} {
						if err := u.checkLedgerDowngrade(version); err != nil {
							t.Fatalf("%s current-or-later = %v", version, err)
						}
					}
				} else if err != nil {
					t.Fatalf("%s changed the existing preflight answer: %v", form, err)
				}
				if documentPath != "" {
					after, _ := os.ReadFile(documentPath)
					if !bytes.Equal(after, beforeDocument) {
						t.Fatal("downgrade preflight changed the document")
					}
				}
				if headPath != "" {
					after, _ := os.ReadFile(headPath)
					if !bytes.Equal(after, beforeHead) {
						t.Fatal("downgrade preflight changed the head")
					}
				}
				for _, path := range []string{
					filepath.Join(u.q.SessionRoot(), "extensions", "amq-squad", "scheduler", "decisions.jsonl"),
					filepath.Join(u.q.SessionRoot(), "extensions", "amq-squad", "quarantine"),
				} {
					if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
						t.Fatalf("read-only preflight created %s: %v", path, statErr)
					}
				}
			})
		}
	}
}

func TestUpAsksEveryInterruptedMigrationReader(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "*", "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	callers := map[string]bool{}
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, pkgOK := sel.X.(*ast.Ident)
			if pkgOK && pkg.Name == "sealchain" && sel.Sel.Name == "CheckRawMigration" {
				callers[filepath.Base(filepath.Dir(path))] = true
			}
			return true
		})
	}
	got := make([]string, 0, len(callers))
	for caller := range callers {
		got = append(got, caller)
	}
	slices.Sort(got)
	if !slices.Equal(got, []string{"scheduler", "seatcommit"}) {
		t.Fatalf("production CheckRawMigration ledger kinds = %v; add the new reader to sessionRawLayout", got)
	}
	source, err := os.ReadFile("up.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, asked := range []string{"scheduler.InterruptedExitsMigrationWriter", "seatcommit.InterruptedMigrationWriter"} {
		if !bytes.Contains(source, []byte(asked+"(")) {
			t.Errorf("sessionRawLayout does not ask %s", asked)
		}
	}
}

func TestUpRefusesAnUnparseableTargetWhenRawLedgersExist(t *testing.T) {
	err := rawLayoutUpRun(t).checkLedgerDowngrade("(devel)")
	if err == nil || !strings.Contains(err.Error(), "could not be read") || !strings.Contains(err.Error(), "install a released build") {
		t.Fatal(err)
	}
}

func TestUpChecksRawLayoutBeforeTaggedPiBuild(t *testing.T) {
	u := rawLayoutUpRun(t)
	u.ctx = context.Background()
	u.report.Project = t.TempDir()
	old := squadRun
	t.Cleanup(func() { squadRun = old })
	var calls []string
	squadRun = func(_ context.Context, _ string, _ []string, _ string, args ...string) ([]byte, error) {
		calls = append(calls, strings.Join(args, " "))
		return []byte("amq-squad (devel)\n"), nil
	}
	err := u.checkLedgerTarget("/tmp/devel-amq-squad")
	if err == nil || !strings.Contains(err.Error(), "layout support cannot be known") {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0] != "version" {
		t.Fatalf("calls = %v; want only the version preflight", calls)
	}
}

func TestRawLayoutSemverPrecedence(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{
		{"v3.2.0-rc.1", "v3.2.0-rc.2", -1},
		{"v3.2.0-rc.2", "v3.2.0", -1},
		{"v3.2.0", "v3.2.0-rc.2", 1},
		{"v3.2.0", "v3.2.0", 0},
		{"v3.2.0+build.1", "v3.2.0+other", 0},
	} {
		if got, ok := compareReleaseVersions(tc.a, tc.b); !ok || got != tc.want {
			t.Errorf("compare(%s,%s) = %d,%v; want %d,true", tc.a, tc.b, got, ok, tc.want)
		}
	}
	for _, invalid := range []string{"(devel)", "v03.2.0", "v3.02.0", "v3.2.00", "v3.2.0-rc.02", "v3.2.0-rc..2", "v3.2.0+build..1"} {
		if _, ok := compareReleaseVersions(invalid, "v3.2.0"); ok {
			t.Fatalf("%s parsed as a release", invalid)
		}
	}
}

func TestRawLayoutInvalidSemverIsRefusedInWords(t *testing.T) {
	u := rawLayoutUpRun(t)
	for _, version := range []string{"v3.2.0-rc.02", "v3.2.0-rc..2", "v3.2.0+build..1"} {
		err := u.checkLedgerDowngrade(version)
		if err == nil || !strings.Contains(err.Error(), version) || !strings.Contains(err.Error(), "could not be read as a released version") {
			t.Fatalf("%s: %v", version, err)
		}
	}
}

// E7: without a raw chain there is no downgrade floor to enforce.
func TestUpWithoutRawChainProceeds(t *testing.T) {
	base := t.TempDir()
	u := &upRun{q: seatlaunch.Squad{BaseRoot: base, Session: "s"}}
	if err := os.MkdirAll(u.q.SessionRoot(), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, version := range []string{"(devel)", "v3.1.2", "v03.2.0"} {
		if err := u.checkLedgerDowngrade(version); err != nil {
			t.Fatalf("%s without a raw chain: %v", version, err)
		}
	}
}
