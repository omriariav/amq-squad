package sealchain

import (
	"bytes"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRawWriterRefusesSequenceExhaustionBeforeSave(t *testing.T) {
	root := t.TempDir()
	seed, err := SessionKey(root)
	if err != nil {
		t.Fatal(err)
	}
	prev := &Link{Seq: math.MaxInt, Tag: "authenticated"}
	saved := false
	_, err = WriteRawAfter(root, "fixture", seed, "test", prev, []byte(`{"ok":true}`), "v3.2.0-rc.2", 1, func([]byte) error { saved = true; return nil }, nil, time.Now())
	var exhausted *ErrSeqExhausted
	if !errors.As(err, &exhausted) || exhausted.Seq != math.MaxInt || saved {
		t.Fatalf("exhaustion = %v (%+v), saved=%v", err, exhausted, saved)
	}
}

func TestSealchainProductionNeverReadsDocumentHeads(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			headVars := map[string]bool{}
			// Propagate simple local aliases of HeadPath or a heads directory.
			for changed := true; changed; {
				changed = false
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					a, ok := n.(*ast.AssignStmt)
					if !ok {
						return true
					}
					for i, rhs := range a.Rhs {
						if i < len(a.Lhs) && expressionNamesHead(rhs, headVars) {
							if id, ok := a.Lhs[i].(*ast.Ident); ok && !headVars[id.Name] {
								headVars[id.Name], changed = true, true
							}
						}
					}
					return true
				})
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || len(call.Args) == 0 || !expressionNamesHead(call.Args[0], headVars) {
					return true
				}
				name := ""
				switch f := call.Fun.(type) {
				case *ast.SelectorExpr:
					if id, ok := f.X.(*ast.Ident); ok && id.Name == "os" {
						name = f.Sel.Name
					}
				case *ast.Ident:
					name = f.Name
				}
				for _, reader := range []string{"ReadFile", "ReadDir", "Open", "OpenFile", "Stat", "Lstat", "openNoFollow", "readNoFollow"} {
					if name == reader {
						// headsExist probes only the directory entry to prevent
						// recreating a lost seed. It never opens or reads a head.
						if fn.Name.Name == "headsExist" && name == "Lstat" {
							continue
						}
						t.Errorf("%s: %s reads a head path inside sealchain", path, reader)
					}
				}
				return true
			})
		}
	}
}

func expressionNamesHead(expr ast.Expr, vars map[string]bool) bool {
	found := false
	ast.Inspect(expr, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.Ident:
			found = found || v.Name == "HeadPath" || vars[v.Name]
		case *ast.BasicLit:
			found = found || v.Value == `"heads"`
		}
		return !found
	})
	return found
}

type rawFixtureDoc struct {
	Schema int    `json:"schema"`
	Known  string `json:"known"`
}

func writeRawFixture(t *testing.T, body []byte, version string, schema int) (root, name, label string, seed Seed, document, headBytes []byte) {
	t.Helper()
	root, name, label = t.TempDir(), "commits.qa", "test raw ledger"
	var err error
	seed, err = SessionKey(root)
	if err != nil {
		t.Fatal(err)
	}
	_, err = WriteRaw(root, name, seed, label, nil, body, version, schema, func(raw []byte) error {
		document = append([]byte(nil), raw...)
		return os.WriteFile(root+"/document", raw, 0o600)
	}, time.Unix(10, 0))
	if err != nil {
		t.Fatal(err)
	}
	headBytes, err = os.ReadFile(HeadPath(root, name))
	if err != nil {
		t.Fatal(err)
	}
	return
}

func TestRawSealCoversExactBodyAndMetadata(t *testing.T) {
	body := []byte(`{"schema":1,"known":"kept"}`)
	_, name, label, seed, document, headBytes := writeRawFixture(t, body, "v3.2.0-rc.2", 1)
	var got rawFixtureDoc
	seal, err := CheckRaw(name, seed, label, "fixture", document, headBytes, 1, &got)
	if err != nil || got.Known != "kept" || seal.Version != "v3.2.0-rc.2" {
		t.Fatalf("untouched raw document: %+v, %+v, %v", got, seal, err)
	}

	t.Run("body byte", func(t *testing.T) {
		edited := bytes.Replace(document, []byte("kept"), []byte("lost"), 1)
		if _, err := CheckRaw(name, seed, label, "fixture", edited, headBytes, 1, &rawFixtureDoc{}); !errors.Is(err, ErrTampered) {
			t.Fatalf("body edit = %v", err)
		}
	})
	t.Run("version", func(t *testing.T) {
		body, seal, err := SplitRaw(document)
		if err != nil {
			t.Fatal(err)
		}
		seal.Version = "v9.0.0"
		edited, _ := JoinRaw(body, seal)
		if _, err := CheckRaw(name, seed, label, "fixture", edited, headBytes, 1, &rawFixtureDoc{}); !errors.Is(err, ErrTampered) {
			t.Fatalf("version edit = %v", err)
		}
	})
}

func TestRawSealEndingIsExact(t *testing.T) {
	body := []byte(`{"schema":1,"known":"kept"}`)
	_, name, label, seed, document, headBytes := writeRawFixture(t, body, "v3.2.0-rc.2", 1)
	cases := map[string][]byte{
		"missing final newline": document[:len(document)-1],
		"extra blank line":      append(append([]byte(nil), document...), '\n'),
		"carriage return":       bytes.Replace(document, []byte("\n{"), []byte("\r\n{"), 1),
	}
	for nameOf, edited := range cases {
		t.Run(nameOf, func(t *testing.T) {
			if _, err := CheckRaw(name, seed, label, "fixture", edited, headBytes, 1, &rawFixtureDoc{}); !errors.Is(err, ErrTampered) {
				t.Fatalf("ending = %v", err)
			}
		})
	}
}

func TestRawSealClassifiesUnknownFieldsBySchemaNotVersion(t *testing.T) {
	for _, tc := range []struct {
		name, version string
		schema        int
		want          error
	}{
		{"newer release", "v9.0.0", 2, ErrNewerWriter},
		{"newer devel", "(devel)", 2, ErrNewerWriter},
		{"same release", "v3.2.0-rc.2", 1, ErrSchemaMismatch},
		{"same devel", "(devel)", 1, ErrSchemaMismatch},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(`{"schema":1,"known":"kept","future":true}`)
			_, name, label, seed, document, headBytes := writeRawFixture(t, body, tc.version, tc.schema)
			_, err := CheckRaw(name, seed, label, "fixture", document, headBytes, 1, &rawFixtureDoc{})
			if !errors.Is(err, tc.want) || errors.Is(err, ErrTampered) || !strings.Contains(err.Error(), "future") || !strings.Contains(err.Error(), tc.version) {
				t.Fatalf("classification = %v", err)
			}
		})
	}
}

func TestRawHeadLayoutIsInsideItsTag(t *testing.T) {
	body := []byte(`{"schema":1,"known":"kept"}`)
	_, name, label, seed, document, headBytes := writeRawFixture(t, body, "v3.2.0-rc.2", 1)
	headBody, headSeal, err := SplitRaw(headBytes)
	if err != nil {
		t.Fatal(err)
	}
	editedBody := bytes.Replace(headBody, []byte(RawLayout), []byte("raw-bytes-v0"), 1)
	editedHead, _ := JoinRaw(editedBody, headSeal)
	if _, err := CheckRaw(name, seed, label, "fixture", document, editedHead, 1, &rawFixtureDoc{}); !errors.Is(err, ErrTampered) {
		t.Fatalf("layout edit = %v", err)
	}
}

func TestRawHeadUnknownFieldIsClassifiedAfterItsTagVerifies(t *testing.T) {
	body := []byte(`{"schema":1,"known":"ok"}`)
	_, name, _, seed, _, headBytes := writeRawFixture(t, body, "v3.2.0-rc.2", 1)
	headBody, _, err := SplitRaw(headBytes)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(headBody, &doc); err != nil {
		t.Fatal(err)
	}
	doc["future"] = true
	headBody, _ = json.Marshal(doc)
	for _, tc := range []struct {
		name    string
		schema  int
		wantErr error
	}{
		{"newer head schema", 2, ErrNewerWriter},
		{"same head schema", 1, ErrSchemaMismatch},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seal := NewRawSeal(seed.Key, rawHeadLabel, nil, headBody, "v9.0.0", tc.schema)
			raw, err := JoinRaw(headBody, seal)
			if err != nil {
				t.Fatal(err)
			}
			_, err = HeadLayout(name, seed, raw)
			if !errors.Is(err, tc.wantErr) || errors.Is(err, ErrTampered) || !strings.Contains(err.Error(), "future") {
				t.Fatalf("verified head classification = %v", err)
			}
		})
	}
	badSeed := seed
	badSeed.Key = append([]byte(nil), seed.Key...)
	badSeed.Key[0] ^= 0xff
	badSeal := NewRawSeal(badSeed.Key, rawHeadLabel, nil, headBody, "v9.0.0", 2)
	badHead, err := JoinRaw(headBody, badSeal)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := HeadLayout(name, seed, badHead); !errors.Is(err, ErrTampered) || errors.Is(err, ErrNewerWriter) {
		t.Fatalf("unverified unknown head = %v", err)
	}
}

func TestLegacyDocumentMigratesOnceToRawLayout(t *testing.T) {
	type legacyDoc struct {
		Schema int    `json:"schema"`
		Value  string `json:"value"`
		Seal   *Link  `json:"seal,omitempty"`
	}
	root, name, label := t.TempDir(), "commits.qa", "legacy ledger"
	seed, err := SessionKey(root)
	if err != nil {
		t.Fatal(err)
	}
	legacy := legacyDoc{Schema: 1, Value: "before"}
	path := root + "/document"
	if _, err := Write(root, name, seed, label, nil, legacy, func(link *Link) error {
		legacy.Seal = link
		raw, _ := json.Marshal(legacy)
		return os.WriteFile(path, raw, 0o600)
	}, time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
	legacyBody := legacy
	legacyBody.Seal = nil
	legacyHead, _ := os.ReadFile(HeadPath(root, name))
	if err := CheckLegacy(name, seed, label, legacy.Seal, legacyBody, legacyHead); err != nil {
		t.Fatal(err)
	}

	next := []byte(`{"schema":1,"known":"after"}`)
	var document []byte
	if _, err := WriteRaw(root, name, seed, label, legacyHead, next, "v3.2.0-rc.2", 1, func(raw []byte) error {
		document = append([]byte(nil), raw...)
		return os.WriteFile(path, raw, 0o600)
	}, time.Unix(2, 0)); err != nil {
		t.Fatal(err)
	}
	headBytes, _ := os.ReadFile(HeadPath(root, name))
	if layout, err := HeadLayout(name, seed, headBytes); err != nil || layout != RawLayout {
		t.Fatalf("migrated head = %q, %v", layout, err)
	}
	var got rawFixtureDoc
	if _, err := CheckRaw(name, seed, label, "fixture", document, headBytes, 1, &got); err != nil || got.Known != "after" {
		t.Fatalf("migrated document = %+v, %v", got, err)
	}
	if err := CheckLegacy(name, seed, label, legacy.Seal, legacyBody, headBytes); !errors.Is(err, ErrTampered) {
		t.Fatalf("legacy downgrade after migration = %v", err)
	}
}

func TestWriterVersionFallsBackToBuildInfo(t *testing.T) {
	old := BinaryVersion
	BinaryVersion = ""
	t.Cleanup(func() { BinaryVersion = old })
	got := WriterVersion()
	if got == "" || got == "(unknown)" {
		t.Fatalf("build-info fallback = %q", got)
	}
	t.Logf("unstamped test binary build-info fallback: %s", got)
}
