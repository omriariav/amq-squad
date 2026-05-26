package cli

import (
	"flag"
	"reflect"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/internal/launch"
)

func TestLaunchArgsFromRecordIncludesLauncher(t *testing.T) {
	rec := launch.Record{
		Binary: "claude", Handle: "qa", Role: "qa", Session: "beta",
		Launcher: "/opt/launch.sh", LauncherArgs: []string{"--pull", "--workspace", "/x"},
	}
	// Resume/restore must reconstruct the wrapper, not relaunch the raw binary.
	joined := strings.Join(launchArgsFromRecord(rec), " ")
	if !strings.Contains(joined, "--launcher /opt/launch.sh") {
		t.Errorf("resume args missing --launcher: %s", joined)
	}
	if !strings.Contains(joined, "--launcher-args=--pull --workspace /x") {
		t.Errorf("resume args missing exact --launcher-args value: %s", joined)
	}
	if cmd := emitCommandWithOptions(rec, emitCommandOptions{}); !strings.Contains(cmd, "--launcher /opt/launch.sh") || !strings.Contains(cmd, "--launcher-args=") {
		t.Errorf("emit command missing launcher flags: %s", cmd)
	}

	// A launcher with no args must not emit an empty --launcher-args=.
	noArgs := rec
	noArgs.LauncherArgs = nil
	jn := strings.Join(launchArgsFromRecord(noArgs), " ")
	if !strings.Contains(jn, "--launcher /opt/launch.sh") {
		t.Errorf("no-args case missing --launcher: %s", jn)
	}
	if strings.Contains(jn, "--launcher-args") {
		t.Errorf("no-args case should not emit --launcher-args: %s", jn)
	}
}

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
		"amq-squad agent up claude",
		"--no-bootstrap",
		"--role qa",
		"--session stream1",
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
		"--trust trusted",
	} {
		if !strings.Contains(cmd, want) {
			t.Errorf("emitCommand missing %q in: %s", want, cmd)
		}
	}
	if strings.Contains(cmd, " resume cto-thread") {
		t.Errorf("emitCommand should strip generated resume args when --conversation is present: %s", cmd)
	}
	if strings.Contains(cmd, " -- --dangerously-bypass-approvals-and-sandbox") {
		t.Errorf("emitCommand should not duplicate trusted defaults after --: %s", cmd)
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

func TestEmitCommandIncludesBaseRootWithSession(t *testing.T) {
	rec := launch.Record{
		CWD:      "/p",
		Binary:   "claude",
		Session:  "stream1",
		Handle:   "claude",
		Root:     "/tmp/mail/stream1",
		BaseRoot: "/tmp/mail",
	}
	cmd := emitCommand(rec)
	for _, want := range []string{"--root /tmp/mail", "--session stream1"} {
		if !strings.Contains(cmd, want) {
			t.Errorf("emitCommand missing %q in: %s", want, cmd)
		}
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
	// Built-in claude defaults are stripped from the -- block on emission;
	// they are re-prepended on replay.
	want := []string{
		"--no-bootstrap",
		"--role", "fullstack",
		"--session", "stream1",
		"--conversation", "abc",
		"--me", "fullstack",
		"claude",
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
	// codex defaults to sandboxed; --trust sandboxed is emitted explicitly so
	// the trust boundary is visible on replay.
	want := []string{"--no-bootstrap", "--role", "cto", "--session", "cto", "--team-workstream", "--trust", "sandboxed", "--me", "cto", "codex"}
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
	want := []string{"--no-bootstrap", "--root", "/p/.agent-mail", "--trust", "sandboxed", "--me", "codex", "codex"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("launchArgsFromRecord = %#v, want %#v", got, want)
	}
}

func TestLaunchArgsFromRecordPreservesBaseRootWithSession(t *testing.T) {
	rec := launch.Record{
		Binary:   "claude",
		Session:  "stream1",
		Handle:   "claude",
		Root:     "/tmp/mail/stream1",
		BaseRoot: "/tmp/mail",
	}
	got := launchArgsFromRecord(rec)
	want := []string{"--no-bootstrap", "--session", "stream1", "--root", "/tmp/mail", "--me", "claude", "claude"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("launchArgsFromRecord = %#v, want %#v", got, want)
	}
}

// Restore round-trip: a launch made with --conversation plus --codex-args must
// re-emit through the same flag rather than baking the binary args into the
// argv after "--", otherwise the conversation gate on the second pass rejects
// them as "extra codex args".
func TestLaunchArgsFromRecordRoundTripsConversationWithCodexArgs(t *testing.T) {
	rec := launch.Record{
		CWD:          "/p",
		Binary:       "codex",
		Argv:         []string{"--dangerously-bypass-approvals-and-sandbox", "--enable", "goals", "resume", "X"},
		Session:      "cto",
		Conversation: "X",
		Handle:       "cto",
		Role:         "cto",
		CodexArgs:    []string{"--enable", "goals"},
	}
	got := launchArgsFromRecord(rec)
	// Legacy record (Trust unset, bypass in argv) classifies as trusted; the
	// trust default is re-prepended on replay so it is stripped from the
	// emitted -- block.
	want := []string{
		"--no-bootstrap",
		"--role", "cto",
		"--session", "cto",
		"--conversation", "X",
		"--trust", "trusted",
		"--codex-args=--enable goals",
		"--me", "cto",
		"codex",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("launchArgsFromRecord = %#v, want %#v", got, want)
	}

	assertConversationReplayAccepted(t, got)
}

func TestLaunchArgsFromRecordRoundTripsConversationWithClaudeArgs(t *testing.T) {
	rec := launch.Record{
		CWD:          "/p",
		Binary:       "claude",
		Argv:         []string{"--permission-mode", "auto", "--chrome", "--resume", "Y"},
		Session:      "fs",
		Conversation: "Y",
		Handle:       "fullstack",
		Role:         "fullstack",
		ClaudeArgs:   []string{"--chrome"},
	}
	got := launchArgsFromRecord(rec)
	// Built-in claude defaults are stripped on emission; replay re-prepends.
	// claude has no trust concept so no --trust flag is emitted.
	want := []string{
		"--no-bootstrap",
		"--role", "fullstack",
		"--session", "fs",
		"--conversation", "Y",
		"--claude-args=--chrome",
		"--me", "fullstack",
		"claude",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("launchArgsFromRecord = %#v, want %#v", got, want)
	}
	assertConversationReplayAccepted(t, got)
}

// joinedAgentArgs writes a value that parseAgentArgs must re-tokenize
// identically; pin the round-trip for spaces, single quotes, double quotes,
// and backslash escapes.
func TestBinaryArgsJoinedRoundTripPreservesQuotingAndEscapes(t *testing.T) {
	cases := [][]string{
		{"--enable", "goals"},
		{"--prompt", "hello world"},
		{"--label", "it's fine"},
		{`--regex`, `a\nb`},
		{"--quoted", `say "hi"`},
		{"--empty", ""},
	}
	for _, args := range cases {
		joined := joinedAgentArgs(args)
		got, err := parseAgentArgs(joined)
		if err != nil {
			t.Fatalf("parseAgentArgs(%q) error: %v", joined, err)
		}
		if !reflect.DeepEqual(got, args) {
			t.Errorf("round-trip %q -> %q -> %v, want %v", args, joined, got, args)
		}
	}
}

// --no-default-args must round-trip; without persistence the restore replay
// silently re-injects the binary defaults the original launch opted out of.
func TestLaunchArgsFromRecordPreservesNoDefaultArgs(t *testing.T) {
	rec := launch.Record{
		CWD:           "/p",
		Binary:        "codex",
		Argv:          []string{"--enable", "goals"},
		Session:       "rt",
		Handle:        "cto",
		Role:          "cto",
		CodexArgs:     []string{"--enable", "goals"},
		NoDefaultArgs: true,
	}
	got := launchArgsFromRecord(rec)
	// NoDefaultArgs records have no bypass in argv, so trust classifies as
	// sandboxed. --no-default-args is preserved.
	want := []string{
		"--no-bootstrap",
		"--role", "cto",
		"--session", "rt",
		"--no-default-args",
		"--trust", "sandboxed",
		"--codex-args=--enable goals",
		"--me", "cto",
		"codex",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("launchArgsFromRecord = %#v, want %#v", got, want)
	}
	if cmd := emitCommand(rec); !strings.Contains(cmd, "--no-default-args") {
		t.Errorf("emitCommand missing --no-default-args: %s", cmd)
	}
}

// A literal "--" inside CodexArgs must not be intercepted by splitDashDash on
// replay. The =VALUE emission keeps the value glued to the flag name; the
// only bare "--" left in the emitted args is the binary/child separator,
// which sits AFTER the binary token and is intentionally split there.
func TestLaunchArgsFromRecordEscapesLiteralDashDashInBinaryArgs(t *testing.T) {
	rec := launch.Record{
		CWD:       "/p",
		Binary:    "codex",
		Argv:      []string{"--dangerously-bypass-approvals-and-sandbox", "--"},
		Session:   "rt",
		Handle:    "cto",
		Role:      "cto",
		CodexArgs: []string{"--"},
	}
	got := launchArgsFromRecord(rec)
	squadArgs, postDash := splitDashDash(got)
	// The emitted "--codex-args=--" token must survive into squadArgs; the
	// only thing past the binary/child separator should be the leftover argv
	// (the built-in default). If splitDashDash had grabbed the "--" inside
	// the flag value, --codex-args would land with no value and the flag
	// state would shift entirely.
	for _, a := range squadArgs {
		if a == "--codex-args" || a == "--codex-args=" {
			t.Fatalf("--codex-args lost its value on replay: %#v", squadArgs)
		}
	}
	// Legacy record with bypass in argv classifies as trusted, so the bypass
	// arg is stripped from the -- block (replay re-prepends via --trust).
	if len(postDash) != 0 {
		t.Fatalf("postDash = %#v, want empty for trusted-classified legacy record", postDash)
	}
	parsed, err := parseAgentArgs(extractFlagValue(squadArgs, "--codex-args"))
	if err != nil {
		t.Fatalf("parseAgentArgs: %v", err)
	}
	if !reflect.DeepEqual(parsed, []string{"--"}) {
		t.Errorf("parsed CodexArgs = %#v, want [--]", parsed)
	}
}

func extractFlagValue(args []string, name string) string {
	prefix := name + "="
	for _, a := range args {
		if strings.HasPrefix(a, prefix) {
			return strings.TrimPrefix(a, prefix)
		}
	}
	return ""
}

func TestRemoveContiguousSubsequenceFirstMatchOnly(t *testing.T) {
	// Stripping is intentionally first-match. Document the behavior so a
	// future refactor doesn't quietly change it.
	got := removeContiguousSubsequence(
		[]string{"--enable", "goals", "x", "--enable", "goals"},
		[]string{"--enable", "goals"},
	)
	want := []string{"x", "--enable", "goals"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("removeContiguousSubsequence = %v, want %v", got, want)
	}
}

// assertConversationReplayAccepted reproduces the replay path runLaunch takes
// and confirms the conversation gate accepts the restored argv.
func assertConversationReplayAccepted(t *testing.T, args []string) {
	t.Helper()
	squadArgs, postDash := splitDashDash(args)
	fs := flag.NewFlagSet("launch", flag.ContinueOnError)
	codexArgsRaw := fs.String("codex-args", "", "")
	claudeArgsRaw := fs.String("claude-args", "", "")
	conversation := fs.String("conversation", "", "")
	trustRaw := fs.String("trust", "", "")
	model := fs.String("model", "", "")
	_ = fs.Bool("no-bootstrap", false, "")
	_ = fs.Bool("no-default-args", false, "")
	_ = fs.Bool("force-duplicate", false, "")
	_ = fs.Bool("team-workstream", false, "")
	_ = fs.String("role", "", "")
	_ = fs.String("session", "", "")
	_ = fs.String("root", "", "")
	_ = fs.String("team-home", "", "")
	_ = fs.String("me", "", "")
	if err := fs.Parse(squadArgs); err != nil {
		t.Fatalf("parse restore squadArgs: %v", err)
	}
	trustMode, err := normalizeTrustMode(*trustRaw)
	if err != nil {
		t.Fatalf("normalizeTrustMode: %v", err)
	}
	binaryArgs, err := parseBinaryArgFlags(*codexArgsRaw, *claudeArgsRaw)
	if err != nil {
		t.Fatalf("parseBinaryArgFlags: %v", err)
	}
	remaining := fs.Args()
	if len(remaining) == 0 {
		t.Fatalf("restore args missing binary: %#v", args)
	}
	binary := remaining[0]
	childArgs := append([]string(nil), remaining[1:]...)
	childArgs = append(childArgs, postDash...)
	defaultArgs := launchDefaultChildArgsWithTrust(binary, true, modelArgsForBinary(binary, *model), binaryArgsFor(binary, binaryArgs), trustMode)
	childArgs = ensureLeadingChildArgs(defaultArgs, childArgs)
	if _, err := applyConversationRestoreArgsWithDefaults(binary, childArgs, *conversation, defaultArgs); err != nil {
		t.Fatalf("conversation restore rejected: %v", err)
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
