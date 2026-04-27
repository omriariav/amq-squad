package cli

import (
	"reflect"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/internal/launch"
)

func TestShellQuoteSafeString(t *testing.T) {
	cases := map[string]string{
		"claude":          "claude",
		"/usr/bin/amq":    "/usr/bin/amq",
		"stream1":         "stream1",
		"role_123":        "role_123",
		"with space":      "'with space'",
		"with'apostrophe": `'with'\''apostrophe'`,
		"":                "''",
		"a;b":             "'a;b'",
	}
	for in, want := range cases {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEmitCommandIncludesRoleAndSession(t *testing.T) {
	rec := launch.Record{
		CWD:     "/home/user/proj",
		Binary:  "claude",
		Session: "stream1",
		Handle:  "claude",
		Role:    "qa",
	}
	cmd := emitCommand(rec)
	for _, want := range []string{
		"cd /home/user/proj",
		"amq-squad launch",
		"--no-bootstrap",
		"--role qa",
		"--session stream1",
		"claude",
	} {
		if !strings.Contains(cmd, want) {
			t.Errorf("emitCommand missing %q in: %s", want, cmd)
		}
	}
	// Handle equals defaultHandleFor(binary), so --me should be omitted.
	if strings.Contains(cmd, "--me") {
		t.Errorf("emitCommand should omit --me when handle == default; got: %s", cmd)
	}
}

func TestEmitCommandIncludesMeWhenHandleDiffers(t *testing.T) {
	rec := launch.Record{
		CWD:     "/p",
		Binary:  "codex",
		Session: "s",
		Handle:  "cpo",
		Role:    "cpo",
	}
	cmd := emitCommand(rec)
	if !strings.Contains(cmd, "--me cpo") {
		t.Errorf("expected --me cpo in: %s", cmd)
	}
}

func TestEmitCommandIncludesConversationWithoutDuplicatingResumeArgs(t *testing.T) {
	rec := launch.Record{
		CWD:          "/p",
		Binary:       "codex",
		Argv:         []string{"--dangerously-bypass-approvals-and-sandbox", "resume", "cto-thread"},
		Session:      "cto",
		Conversation: "cto-thread",
		Handle:       "cto",
		Role:         "cto",
	}
	cmd := emitCommand(rec)
	for _, want := range []string{
		"--conversation cto-thread",
		"-- --dangerously-bypass-approvals-and-sandbox",
	} {
		if !strings.Contains(cmd, want) {
			t.Errorf("emitCommand missing %q in: %s", want, cmd)
		}
	}
	if strings.Contains(cmd, " resume cto-thread") {
		t.Errorf("emitCommand should strip generated resume args when --conversation is present: %s", cmd)
	}
}

func TestEmitCommandQuotesArgvWithSpaces(t *testing.T) {
	rec := launch.Record{
		CWD:    "/p",
		Binary: "claude",
		Argv:   []string{"--prompt", "hello world"},
	}
	cmd := emitCommand(rec)
	if !strings.Contains(cmd, "'hello world'") {
		t.Errorf("expected quoted argv in: %s", cmd)
	}
	if !strings.Contains(cmd, " -- ") {
		t.Errorf("expected -- separator before argv in: %s", cmd)
	}
}

func TestEmitCommandIncludesRootWhenSessionMissing(t *testing.T) {
	rec := launch.Record{
		CWD:    "/p",
		Binary: "claude",
		Handle: "claude",
		Root:   "/p/.agent-mail",
	}
	cmd := emitCommand(rec)
	if !strings.Contains(cmd, "--root /p/.agent-mail") {
		t.Errorf("expected --root for base-root restore in: %s", cmd)
	}
}

func TestLaunchArgsFromRecord(t *testing.T) {
	rec := launch.Record{
		CWD:          "/p",
		Binary:       "claude",
		Argv:         []string{"--permission-mode", "auto", "--resume", "abc"},
		Session:      "stream1",
		Conversation: "abc",
		Handle:       "fullstack",
		Role:         "fullstack",
		Root:         "/p/.agent-mail/stream1",
	}
	got := launchArgsFromRecord(rec)
	want := []string{
		"--no-bootstrap",
		"--role", "fullstack",
		"--session", "stream1",
		"--conversation", "abc",
		"--me", "fullstack",
		"claude",
		"--",
		"--permission-mode", "auto",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("launchArgsFromRecord = %#v, want %#v", got, want)
	}
}

func TestLaunchArgsFromRecordPreservesSharedWorkstream(t *testing.T) {
	rec := launch.Record{
		Binary:           "codex",
		Session:          "cto",
		SharedWorkstream: true,
		Handle:           "cto",
		Role:             "cto",
	}
	got := launchArgsFromRecord(rec)
	want := []string{"--no-bootstrap", "--role", "cto", "--session", "cto", "--team-workstream", "--me", "cto", "codex"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("launchArgsFromRecord = %#v, want %#v", got, want)
	}
	if cmd := emitCommand(rec); !strings.Contains(cmd, "--team-workstream") {
		t.Fatalf("emitCommand should preserve shared workstream metadata: %s", cmd)
	}
}

func TestLaunchArgsFromRecordUsesRootWithoutSession(t *testing.T) {
	rec := launch.Record{
		Binary: "codex",
		Handle: "codex",
		Root:   "/p/.agent-mail",
	}
	got := launchArgsFromRecord(rec)
	want := []string{"--no-bootstrap", "--root", "/p/.agent-mail", "--me", "codex", "codex"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("launchArgsFromRecord = %#v, want %#v", got, want)
	}
}

func TestMatchesRestoreFilters(t *testing.T) {
	rec := launch.Record{Role: "cto", Handle: "cto", Session: "stream1", Conversation: "cto-thread"}
	cases := []struct {
		name         string
		role         string
		handle       string
		session      string
		conversation string
		want         bool
	}{
		{name: "no filters", want: true},
		{name: "role match", role: "cto", want: true},
		{name: "role mismatch", role: "qa", want: false},
		{name: "handle match", handle: "cto", want: true},
		{name: "handle mismatch", handle: "fullstack", want: false},
		{name: "session match", session: "stream1", want: true},
		{name: "session mismatch", session: "stream2", want: false},
		{name: "conversation match", conversation: "cto-thread", want: true},
		{name: "conversation mismatch", conversation: "other", want: false},
	}
	for _, tc := range cases {
		got := matchesRestoreFilters(rec, tc.role, tc.handle, tc.session, tc.conversation)
		if got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestRestoreMetadataMarksLegacyHistory(t *testing.T) {
	entry := launch.Entry{
		Record: launch.Record{
			CWD:          "/p",
			Binary:       "claude",
			Session:      "stream1",
			Conversation: "bug-fix-chat",
			Handle:       "claude",
		},
		AgentDir: "/p/.agent-mail/stream1/agents/claude",
		Source:   "amq history",
	}
	got := restoreMetadata(entry)
	for _, want := range []string{
		"session: stream1",
		"conversation: bug-fix-chat",
		"handle: claude",
		"persona: missing",
		"source: amq",
		"cwd: /p",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("restoreMetadata missing %q in: %s", want, got)
		}
	}
}

func TestDefaultHandleFromPath(t *testing.T) {
	if got := defaultHandleFor("/usr/local/bin/Claude"); got != "claude" {
		t.Errorf("defaultHandleFor lower-cases basename, got %q", got)
	}
	if got := defaultHandleFor("codex"); got != "codex" {
		t.Errorf("defaultHandleFor plain = %q", got)
	}
}
