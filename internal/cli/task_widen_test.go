package cli

import (
	"regexp"
	"strings"
	"testing"

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

// Brief 3.1.1-3, row 4 (the widenRefused Error form): a refused widening
// says what happened and what to do above the rule; the reason code and the
// detail, which carries the task's state and block reason, go below it.
// Reverted (the `widen refused (%s): %s` form), the code is the first word.
func TestTheWidenRefusalPutsItsCodeBelowTheRule(t *testing.T) {
	_, err := validateBlockedWiden(task.Task{ID: "t9", State: task.StateBlocked, BlockReason: "needs_decision"}, []string{"b.go"})
	if err == nil {
		t.Fatal("a widen of a task blocked needs_decision was not refused")
	}
	msgs := []string{err.Error()}
	for _, code := range []string{"widen_invalid", "widen_not_requested", "widen_paths_missing", "widen_paths_mismatch", task.RefusalFileLock, task.ReasonGrowthGateRequired, task.RefusalSelfWiden, "made_up_refusal"} {
		msgs = append(msgs, (&widenRefused{code: code, msg: "task t9 (state in_progress, reason needs_decision), snapshot 0de97c89a1b2c3d4"}).Error())
	}
	for _, msg := range msgs {
		plain, record := splitWidenRule(t, msg)
		if code := widenLineCode(plain); code != "" {
			t.Errorf("a code above the rule (%q): %q", code, plain)
		}
		if !strings.HasPrefix(record, "widen refused (") {
			t.Errorf("the code is not below the rule: %q", record)
		}
	}
	if !strings.Contains(msgs[0], "(state blocked, reason needs_decision)") {
		t.Errorf("the record lost the state and the reason: %q", msgs[0])
	}
}

// Brief 3.1.1-3, row 4 (the socket reply's detail, the verb's refusal): the
// verb's refusal keeps the widening's plain line above the rule, or names
// the socket's refusal in words, and puts the code and the detail below.
// Reverted (`task widen %s refused (%s): %s`), the code is on the first line.
func TestTheWidenVerbsRefusalPutsItsCodeBelowTheRule(t *testing.T) {
	inner := (&widenRefused{code: "widen_not_requested", msg: "task t9 is not blocked (state blocked, reason needs_decision)"}).Error()
	for _, reply := range []schedproc.DecideReply{
		{Code: "widen_not_requested", Detail: inner},
		{Code: schedproc.RefusedNotLead, Detail: "pid 4242 does not descend from the lead's pane %3 (origin_unknown at hop 2)"},
		{Code: schedproc.RefusedStarting, Detail: "the scheduler is starting: its decision socket opens once the exits_ledger is loaded"},
		{Code: "not_squad", Detail: "the task is not in this squad's store: task t9: not_found"},
		{Code: "widen_failed", Detail: "open /r/tasks/0de97c89a1b2c3d4.json: no such file"},
	} {
		msg := widenReplyRefusal("t9", reply)
		plain, record := splitWidenRule(t, msg)
		if strings.Contains(plain, "\n") {
			t.Errorf("the plain part is more than one line: %q", plain)
		}
		if !strings.HasPrefix(plain, "task widen t9 was refused: ") {
			t.Errorf("the plain line does not say what was refused: %q", plain)
		}
		if code := widenLineCode(plain); code != "" {
			t.Errorf("%s: a code above the rule (%q): %q", reply.Code, code, plain)
		}
		if !strings.HasPrefix(record, "refused ("+reply.Code+"): ") {
			t.Errorf("%s: the code is not below the rule: %q", reply.Code, record)
		}
	}
}
