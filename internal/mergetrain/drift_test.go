package mergetrain

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/gatestore"
)

// plainCode is the first snake_case code or hex run in a plain line ("" for
// none), as the lead's screen reads it (pi/extensions/lib/words.ts codeIn):
// a token with a slash or a dot (a path, a file name) is not a code.
func plainCode(line string) string {
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

// Brief 3.1.1-3, row 4 (the Go line in this package): the gate store
// unreadable alert says what happened in words above the rule; the record's
// path and error, which can carry long hex ids, go below it. Reverted (the
// path and the error in the one line), the plain part carries the path's
// hex id and the error's code.
func TestTheGateStoreUnreadableAlertPutsThePathBelowTheRule(t *testing.T) {
	const repo = "owner/app"
	base := t.TempDir()
	squad := filepath.Join(base, "v31")
	_ = os.MkdirAll(squad, 0o700)
	// a session named by a long hex id, as a launch names its roots
	broken := filepath.Join(base, "s-0de97c89a1b2c3d4e5f6", "extensions", "amq-squad", "gates")
	writeFile(t, broken, "not a directory\n")
	l := &listed{}
	now := time.Date(2026, 11, 1, 22, 0, 0, 0, time.UTC)
	_, _ = Drift(context.Background(), l, decisionAccepted, squad, repo, now)
	a, err := Drift(context.Background(), l, decisionAccepted, squad, repo, now)
	if err != nil || len(a) != 1 || a[0].Kind != "gate_store_unreadable" {
		t.Fatalf("an unreadable store: %v %+v", err, a)
	}
	plain, record, ok := strings.Cut(a[0].Detail, "\n\n"+gatestore.Rule+"\n")
	if !ok {
		t.Fatalf("the alert has no rule: %q", a[0].Detail)
	}
	if code := plainCode(plain); code != "" || strings.Contains(plain, broken) {
		t.Errorf("the alert's plain line carries %q: %q", code, plain)
	}
	if !strings.Contains(record, broken) || !strings.Contains(record, "could not be read") {
		t.Errorf("the record below the rule lacks the path and the error: %q", record)
	}
}
