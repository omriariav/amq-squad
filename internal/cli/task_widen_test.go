package cli

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/gatestore"
	"github.com/omriariav/amq-squad/v3/internal/schedproc"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// widenLineCode is the first snake_case code or hex run in a plain line (""
// for none), as the lead's screen reads it (pi/extensions/lib/words.ts
// codeIn): a token with a slash or a dot (a path, a file name) is not a
// code.
func widenLineCode(line string) string {
	snake := regexp.MustCompile(`^[a-z][a-z0-9]*(?:_[a-z0-9]+)+$`)
	for _, tok := range regexp.MustCompile(`[\s,;:()"'\[\]{}<>!?]+`).Split(line, -1) {
		tok = strings.Trim(tok, ".`*")
		if tok == "" || strings.ContainsAny(tok, "/.") {
			continue
		}
		if snake.MatchString(tok) {
			return tok
		}
	}
	for _, m := range regexp.MustCompile(`(?i)[0-9a-f]{8,}`).FindAllString(line, -1) {
		if strings.ContainsAny(m, "0123456789") && strings.ContainsAny(strings.ToLower(m), "abcdef") {
			return m
		}
	}
	return ""
}

// splitWidenRule is a refusal's text above its rule and below it.
func splitWidenRule(t *testing.T, msg string) (string, string) {
	t.Helper()
	plain, record, ok := strings.Cut(msg, "\n\n"+gatestore.Rule+"\n")
	if !ok {
		t.Fatalf("no rule in %q", msg)
	}
	return plain, record
}

// widenThroughTheVerb runs `task widen` for a task of a fresh store, over
// the scheduler's decision socket (the test's lead server, its sender the
// lead's pane when fromLead), and returns the verb's refusal. The task is
// blocked with reason (and, for outside_declared_files, no recorded paths)
// before the widening. The refusal comes from the production path: the
// scheduler's widenDecision (applyLeadWiden, widenRefused) and the verb's
// own refusal of the reply (task_widen.go, runTaskWiden).
func widenThroughTheVerb(t *testing.T, reason string, fromLead bool) (string, string) {
	t.Helper()
	root, _ := filepath.EvalSymlinks(t.TempDir())
	proj, _ := filepath.EvalSymlinks(t.TempDir())
	s, err := task.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	tk, err := s.Add(task.AddInput{Title: "x", Class: "implement", DeclaredFiles: []string{"a.go"}, ProjectPath: proj}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Block(tk.ID, "", reason, "a detail 0de97c89a1b2c3d4", task.RouteLead, time.Now()); err != nil {
		t.Fatal(err)
	}
	prev := taskWidenSend
	taskWidenSend = func(string, []string) ([]byte, error) { return nil, nil }
	t.Cleanup(func() { taskWidenSend = prev })
	leadServer(t, storeSelection{store: s, root: root, release: func() {}}, proj, fromLead)
	_, _, err = captureOutput(t, func() error {
		return runTaskWiden([]string{tk.ID, "--root", root, "--path", "b.go", "--reason", "r"}, os.Stdout)
	})
	var coded *codedExitError
	if !errors.As(err, &coded) {
		t.Fatalf("the verb did not refuse with its coded error: %v", err)
	}
	return tk.ID, coded.msg
}

// Brief 3.1.1-3, row 4 (the widenRefused Error form, task_widen.go): a
// widening the scheduler refuses says what happened and what to do above the
// rule; the reason code and the detail, which carries the task's state and
// block reason, go below it. Through the verb: the scheduler's reply detail
// is widenRefused's text, and the verb keeps its plain line. Reverted (the
// `widen refused (%s): %s` form), the verb's plain line is the socket's
// generic one, and this fails.
func TestTheWidenRefusalPutsItsCodeBelowTheRule(t *testing.T) {
	for _, c := range []struct{ reason, code, plain string }{
		{"needs_decision", "widen_not_requested", widenPlain("widen_not_requested")},
		// the commit step's block with no paths recorded
		{"outside_declared_files", "widen_paths_missing", widenPlain("widen_paths_missing")},
	} {
		id, msg := widenThroughTheVerb(t, c.reason, true)
		plain, record := splitWidenRule(t, msg)
		if want := "task widen " + id + " was refused: " + c.plain; plain != want {
			t.Errorf("%s: the plain line is %q, want %q", c.code, plain, want)
		}
		if code := widenLineCode(plain); code != "" {
			t.Errorf("%s: a code above the rule (%q): %q", c.code, code, plain)
		}
		if !strings.Contains(record, "refused ("+c.code+"): ") || !strings.Contains(record, "widen refused ("+c.code+"): ") {
			t.Errorf("%s: the codes are not below the rule: %q", c.code, record)
		}
	}
	_, msg := widenThroughTheVerb(t, "needs_decision", true)
	if !strings.Contains(msg, "(state blocked, reason needs_decision)") {
		t.Errorf("the record lost the state and the reason: %q", msg)
	}
}

// Brief 3.1.1-3, row 4 (the verb's refusal of the socket reply,
// task_widen.go's runTaskWiden): a refusal the socket gives before any
// widening (the sender is not the lead's pane) is named in words above the
// rule, with the code and the detail below. Reverted (`task widen %s
// refused (%s): %s` at the call), the code is on the first line and there is
// no rule.
func TestTheWidenVerbsRefusalPutsItsCodeBelowTheRule(t *testing.T) {
	id, msg := widenThroughTheVerb(t, "outside_declared_files", false)
	plain, record := splitWidenRule(t, msg)
	if want := "task widen " + id + " was refused: " + widenReplyPlain(schedproc.RefusedNotLead); plain != want {
		t.Errorf("the plain line is %q, want %q", plain, want)
	}
	if code := widenLineCode(plain); code != "" {
		t.Errorf("a code above the rule (%q): %q", code, plain)
	}
	if !strings.HasPrefix(record, "refused ("+schedproc.RefusedNotLead+"): ") {
		t.Errorf("the code is not below the rule: %q", record)
	}
}
