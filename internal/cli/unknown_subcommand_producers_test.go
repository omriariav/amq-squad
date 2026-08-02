package cli

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestUnknownSubcommandProducersExposeCompleteCanonicalSurfaces(t *testing.T) {
	tests := []struct {
		name  string
		verb  string
		run   func([]string) error
		valid []string
	}{
		{name: "team operator", verb: "team operator", run: runTeamOperator, valid: []string{"set", "self"}},
		{name: "activity", verb: "activity", run: runActivity, valid: []string{"set", "clear"}},
		{name: "operator", verb: "operator", run: runOperator, valid: []string{"answer", "self-approve", "send", "directive", "poll", "status", "watch"}},
		{name: "team lead", verb: "team lead", run: runTeamLead, valid: []string{"set", "clear", "show"}},
		{name: "lead", verb: "lead", run: runLead, valid: []string{"register"}},
		{name: "namespace", verb: "namespace", run: runNamespace, valid: []string{"migrate", "recover", "rollback"}},
		{name: "team member", verb: "team member", run: runTeamMember, valid: []string{"add", "update", "admit", "replace", "launch", "control-continue", "status", "history", "rm", "remove", "list", "ls"}},
		{name: "verify", verb: "verify", run: runVerify, valid: []string{"action", "authorization", "rebind", "merge", "release", "release-plan"}},
		{name: "team overlay", verb: "team overlay", run: runTeamOverlay, valid: []string{"init"}},
		{name: "team autonomous", verb: "team autonomous", run: runTeamAutonomous, valid: []string{"show", "pause", "resume", "disable"}},
		{name: "goal", verb: "goal", run: func(args []string) error { return runGoalWithVersion(args, "test") }, valid: []string{"draft", "deliver", "claim", "retry-attempt", "start", "apply", "supervise-resume"}},
		{name: "team", verb: "team", run: runTeam, valid: []string{"init", "resume", "rules", "lead", "overlay", "member", "autonomous", "operator", "sync", "profiles", "rm", "delete", "shared-cwd-exception"}},
		{name: "team rules", verb: "team rules", run: runTeamRules, valid: []string{"init"}},
		{name: "task", verb: "task", run: runTask, valid: []string{"add", "list", "ls", "show", "claim", "renew", "done", "complete", "event", "fail", "block", "reset", "cancel", "release", "deliver", "retry-delivery", "reconcile", "leadership", "handoff"}},
		{name: "global", verb: "global", run: runGlobal, valid: []string{"start", "status"}},
		{name: "run", verb: "run", run: func(args []string) error { return runRunCmd(args, "test") }, valid: []string{"start"}},
		{name: "amq", verb: "amq", run: runAMQ, valid: []string{"env", "ops", "route", "who", "presence", "send", "reply", "drain", "watch", "list", "read", "thread", "receipts", "dlq", "cleanup"}},
		{name: "amq receipts", verb: "amq receipts", run: runAMQReceipts, valid: []string{"list", "wait"}},
		{name: "amq dlq", verb: "amq dlq", run: runAMQDLQ, valid: []string{"list", "read", "retry", "retry-all", "purge"}},
		{name: "new", verb: "new", run: runNew, valid: []string{"team", "profile", "session"}},
		{name: "worktree", verb: "worktree", run: runWorktree, valid: []string{"plan", "materialize", "create", "inspect", "activate", "handoff", "cleanup", "exception"}},
		{name: "worktree exception", verb: "worktree exception", run: runWorktreeException, valid: []string{"set", "clear"}},
		{name: "notifications", verb: "notifications", run: runNotifications, valid: []string{"doctor", "probe", "events", "history"}},
		{name: "receipt", verb: "receipt", run: runReceipt, valid: []string{"show"}},
		{name: "agent", verb: "agent", run: runAgent, valid: []string{"up", "resume"}},
		{name: "context", verb: "context", run: runContext, valid: []string{"explain", "cleanup"}},
		{name: "evidence", verb: "evidence", run: runEvidence, valid: []string{"run", "show", "list", "recover", "lookup"}},
		{name: "gate", verb: "gate", run: runGate, valid: []string{"raise", "close"}},
		{name: "team shared-cwd-exception", verb: "team shared-cwd-exception", run: runTeamSharedCwdException, valid: []string{"set", "clear", "show"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const given = "__amq_squad_probe_invalid__"
			got := tt.run([]string{given})
			want := unknownSubcommandError(tt.verb, given, tt.valid...)
			if got == nil || got.Error() != want.Error() {
				t.Fatalf("error = %v, want %q", got, want)
			}
		})
	}
}

func TestUnknownSubcommandProducersUseCanonicalHelper(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source path")
	}
	dir := filepath.Dir(thisFile)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	const canonicalFormat = "unknown '%s' subcommand: %q. Try %s."
	unknownSubcommandPhrase := regexp.MustCompile(`(?i)\bunknown\b[^\n]{0,80}\bsubcommand\b`)
	producerCalls := 0
	fileset := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		parsed, parseErr := parser.ParseFile(fileset, filepath.Join(dir, name), nil, 0)
		if parseErr != nil {
			t.Fatalf("parse %s: %v", name, parseErr)
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			switch typed := node.(type) {
			case *ast.CallExpr:
				if ident, isIdent := typed.Fun.(*ast.Ident); isIdent && ident.Name == "unknownSubcommandError" {
					producerCalls++
				}
			case *ast.BasicLit:
				if typed.Kind != token.STRING {
					break
				}
				value, unquoteErr := strconv.Unquote(typed.Value)
				if unquoteErr != nil {
					t.Fatalf("unquote string in %s: %v", name, unquoteErr)
				}
				if !unknownSubcommandPhrase.MatchString(value) {
					break
				}
				if name != "cli.go" || value != canonicalFormat {
					t.Errorf("%s contains ad-hoc unknown-subcommand wording %q; use unknownSubcommandError", name, value)
				}
			}
			return true
		})
	}
	if producerCalls != 30 {
		t.Fatalf("canonical producer calls = %d, want 30; update the audited producer inventory intentionally", producerCalls)
	}
}
