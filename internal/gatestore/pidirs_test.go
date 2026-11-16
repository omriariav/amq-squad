package gatestore

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// The pi extension reads the gate store's directories from a generated
// module (window-pi; the S1 pattern of pi/test/state-contract.json): the
// host facts key lists them, and the tests make them. It is generated from
// TopDirs here and checked against it, so a directory added to the store
// without a regeneration fails this test.

var updatePiGateDirs = flag.Bool("update-pi-gate-dirs", false, "rewrite pi/extensions/lib/gatestore.ts")

func piGateDirsPath() string {
	return filepath.Join("..", "..", "pi", "extensions", "lib", "gatestore.ts")
}

// piGateDirs is the generated module's bytes for TopDirs.
func piGateDirs() []byte {
	var b bytes.Buffer
	b.WriteString("// Code generated from internal/gatestore.TopDirs by\n")
	b.WriteString("// `go test ./internal/gatestore/ -run TestPiGateDirsAreCurrent -update-pi-gate-dirs`. DO NOT EDIT.\n\n")
	b.WriteString("// GATE_STORE_DIRS are the directories the gate store writes under\n")
	b.WriteString("// <root>/extensions/amq-squad, in TopDirs order.\n")
	// a JSON array is a TypeScript array literal whose strings are the same
	list, err := json.Marshal(TopDirs)
	if err != nil {
		panic(err)
	}
	fmt.Fprintf(&b, "export const GATE_STORE_DIRS: readonly string[] = %s;\n", list)
	return b.Bytes()
}

func TestPiGateDirsAreCurrent(t *testing.T) {
	want := piGateDirs()
	if *updatePiGateDirs {
		if err := os.WriteFile(piGateDirsPath(), want, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(piGateDirsPath())
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("pi/extensions/lib/gatestore.ts is stale; run: go test ./internal/gatestore/ -run TestPiGateDirsAreCurrent -update-pi-gate-dirs: %v", err)
	}
}
