package cli

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/liveidentity"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

func TestLiveIdentityRecoveryNamesRegisteredExecutableCommands(t *testing.T) {
	setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Workstream:   "audit",
		Orchestrated: true,
		Lead:         "cto",
		Members: []team.Member{
			{Role: "cto", Handle: "cto", Binary: "codex", Session: "audit"},
		},
	})
	chdir(t, dir)
	for _, displayed := range []string{"amq-squad status --json", "amq-squad team resume"} {
		if !strings.Contains(liveidentity.RecoveryAction, "'"+displayed+"'") {
			t.Fatalf("live-identity recovery lacks %q: %s", displayed, liveidentity.RecoveryAction)
		}
		argv := strings.Fields(displayed)
		if _, _, err := captureOutput(t, func() error { return Run(argv[1:], "test") }); err != nil {
			t.Fatalf("the live-identity recovery command %q did not execute: %v", displayed, err)
		}
	}
	if strings.Contains(liveidentity.RecoveryAction, "<") {
		t.Fatalf("live-identity recovery must not print unresolved placeholders: %s", liveidentity.RecoveryAction)
	}
}

func TestNextMissingTeamUsesSharedActionableError(t *testing.T) {
	dir := t.TempDir()
	err := runNext([]string{"--project", dir, "--profile", "review", "--session", "audit"})
	if err == nil {
		t.Fatal("next unexpectedly accepted an unconfigured profile")
	}
	for _, want := range []string{
		`no team configured for profile "review"`,
		"Run 'amq-squad new profile review' first.",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("next missing-team error lacks %q: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "read team:") {
		t.Fatalf("next leaked the raw storage failure instead of the shared remedy: %v", err)
	}
}

func TestReceiptCorruptionKeepsStablePrefixAndAddsRecovery(t *testing.T) {
	if strings.ContainsRune(receiptCorruptRecovery, '%') {
		t.Fatal("receipt recovery is spliced into a format string; % would become a formatting verb")
	}
	err := receiptCorruptf("committed-indeterminate evidence is incomplete for attempt %s", "attempt-1")
	if !strings.HasPrefix(err.Error(), "receipt_corrupt: committed-indeterminate evidence is incomplete for attempt attempt-1") {
		t.Fatalf("receipt error changed its stable parseable prefix/detail: %v", err)
	}
	for _, want := range []string{"; remedy: ", "preserve the receipt artifact", "amq-squad receipt show <message-id> --json"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("receipt error lacks %q: %v", want, err)
		}
	}
}

func TestReceiptMergeCorruptionSurfacesSharedRecovery(t *testing.T) {
	current := baseReceipt613()
	current.MessageID = "2026-08-02T15-00-00Z_current"
	incoming := baseReceipt613()
	incoming.MessageID = "2026-08-02T15-00-01Z_incoming"
	_, err := mergeDeliveryReceipt(current, incoming)
	if err == nil {
		t.Fatal("conflicting receipt message ids unexpectedly merged")
	}
	if !strings.HasPrefix(err.Error(), "receipt_corrupt: attempt "+incoming.AttemptID+" maps to conflicting message ids") {
		t.Fatalf("production merge lost stable receipt prefix/detail: %v", err)
	}
	if !strings.Contains(err.Error(), "; remedy: "+receiptCorruptRecovery) {
		t.Fatalf("production merge did not surface the shared receipt recovery: %v", err)
	}
}

// The receipt_corrupt prefix is consumed outside this package. Every producer
// in internal/cli (the complete producer set today) must go through
// receiptCorruptf so new refusal paths cannot lose either the stable prefix or
// the shared recovery suffix. This audit is deliberately package-local and
// non-recursive; a producer introduced in another package needs a matching
// package audit rather than being silently assumed covered here.
func TestNoRawReceiptCorruptErrorProducers(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		path := filepath.Clean(entry.Name())
		file, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if parseErr != nil {
			t.Fatalf("parse %s: %v", path, parseErr)
		}
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok {
				ast.Inspect(declaration, func(node ast.Node) bool {
					literal, literalOK := node.(*ast.BasicLit)
					if !literalOK || literal.Kind != token.STRING {
						return true
					}
					value, unquoteErr := strconv.Unquote(literal.Value)
					if unquoteErr == nil && strings.HasPrefix(value, "receipt_corrupt:") {
						t.Errorf("raw receipt_corrupt producer in package declaration in %s; use receiptCorruptf", path)
					}
					return true
				})
				continue
			}
			if function.Body == nil {
				continue
			}
			ast.Inspect(function.Body, func(node ast.Node) bool {
				literal, ok := node.(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					return true
				}
				value, unquoteErr := strconv.Unquote(literal.Value)
				if unquoteErr == nil && strings.HasPrefix(value, "receipt_corrupt:") && function.Name.Name != "receiptCorruptf" {
					t.Errorf("raw receipt_corrupt producer in %s function %s; use receiptCorruptf", path, function.Name.Name)
				}
				return true
			})
		}
	}
}
