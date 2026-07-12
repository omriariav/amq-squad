package cli

import (
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/launch"
)

func TestLaunchArgsFromRecordIncludesLauncher(t *testing.T) {
	rec := launch.Record{
		Binary: "claude", Handle: "qa", Role: "qa", Session: "beta",
		Launcher: "/opt/launch.sh", LauncherArgs: []string{"--pull", "--workspace", "/x"},
	}
	// Resume/restore must reconstruct the wrapper as pre-binary launch flags, in
	// order — not relaunch the raw binary. Pin the exact arg slice so a reorder
	// (e.g. a launcher flag leaking past the binary positional or the "--"
	// child boundary) fails loudly rather than passing a substring check.
	// rec carries no Conversation, so this is a re-orient resume: bootstrap
	// must re-run, hence no --no-bootstrap in the replayed args.
	want := []string{
		"--role", "qa", "--session", "beta",
		"--launcher", "/opt/launch.sh", "--launcher-args=--pull --workspace /x",
		"--me", "qa", "claude",
	}
	if got := launchArgsFromRecord(rec); !reflect.DeepEqual(got, want) {
		t.Errorf("launchArgsFromRecord(rec)\n got: %v\nwant: %v", got, want)
	}
	// emitCommandWithOptions: pin the exact launcher segment, value included.
	if cmd := emitCommandWithOptions(rec, emitCommandOptions{}); !strings.Contains(cmd, "--launcher /opt/launch.sh --launcher-args='--pull --workspace /x'") {
		t.Errorf("emit command missing exact launcher segment: %s", cmd)
	}

	// With child argv present, the launcher flags must stay before the "--"
	// passthrough boundary rather than leaking into the child args.
	argvRec := rec
	argvRec.NoDefaultArgs = true
	argvRec.Argv = []string{"hello-prompt"}
	ac := emitCommandWithOptions(argvRec, emitCommandOptions{})
	li, di := strings.Index(ac, "--launcher /opt/launch.sh --launcher-args='--pull --workspace /x'"), strings.Index(ac, " -- ")
	if li < 0 || di < 0 || li > di {
		t.Errorf("full launcher segment must precede the -- child boundary: %s", ac)
	}
	if after := ac[di:]; strings.Contains(after, "--launcher") {
		t.Errorf("launcher flags must not appear after the -- child boundary: %s", ac)
	}

	// A launcher with no args must not emit an empty --launcher-args=, in either
	// the replay arg slice or the emitted command.
	noArgs := rec
	noArgs.LauncherArgs = nil
	jn := strings.Join(launchArgsFromRecord(noArgs), " ")
	if !strings.Contains(jn, "--launcher /opt/launch.sh") || strings.Contains(jn, "--launcher-args") {
		t.Errorf("no-args replay should have --launcher without --launcher-args: %s", jn)
	}
	if nc := emitCommandWithOptions(noArgs, emitCommandOptions{}); !strings.Contains(nc, "--launcher /opt/launch.sh") || strings.Contains(nc, "--launcher-args") {
		t.Errorf("no-args emit should have --launcher without --launcher-args: %s", nc)
	}
}

func TestLaunchArgsFromRecordReplaysWakeInject(t *testing.T) {
	rec := launch.Record{
		Binary:         "codex",
		Handle:         "cto",
		Role:           "cto",
		Session:        "issue-96",
		WakeInjectVia:  "/opt/amq-inject",
		WakeInjectArgs: []string{"--pane", "%42"},
		WakeInjectMode: "raw",
	}
	want := []string{
		"--role", "cto", "--session", "issue-96",
		"--wake-inject-via", "/opt/amq-inject",
		"--wake-inject-arg=--pane", "--wake-inject-arg=%42",
		"--wake-inject-mode", "raw",
		"--trust", "sandboxed", "--me", "cto", "codex",
	}
	if got := launchArgsFromRecord(rec); !reflect.DeepEqual(got, want) {
		t.Errorf("launchArgsFromRecord(rec)\n got: %v\nwant: %v", got, want)
	}
	cmd := emitCommandWithOptions(rec, emitCommandOptions{})
	for _, want := range []string{
		"--wake-inject-via /opt/amq-inject",
		"--wake-inject-arg=--pane",
		"--wake-inject-arg='%42'",
		"--wake-inject-mode raw",
	} {
		if !strings.Contains(cmd, want) {
			t.Errorf("emit command missing %q in: %s", want, cmd)
		}
	}
}

func TestLaunchArgsFromRecordReplaysNoGitignore(t *testing.T) {
	rec := launch.Record{
		Binary:      "codex",
		Handle:      "cto",
		Role:        "cto",
		Session:     "issue-96",
		NoGitignore: true,
	}
	want := []string{
		"--role", "cto", "--session", "issue-96",
		"--no-gitignore",
		"--trust", "sandboxed", "--me", "cto", "codex",
	}
	if got := launchArgsFromRecord(rec); !reflect.DeepEqual(got, want) {
		t.Errorf("launchArgsFromRecord(rec)\n got: %v\nwant: %v", got, want)
	}
	if cmd := emitCommandWithOptions(rec, emitCommandOptions{}); !strings.Contains(cmd, "--no-gitignore") {
		t.Errorf("emit command missing --no-gitignore: %s", cmd)
	}
}

func TestLaunchArgsFromRecordReplaysSymphony(t *testing.T) {
	rec := launch.Record{
		Binary:   "codex",
		Handle:   "cto",
		Role:     "cto",
		Session:  "issue-96",
		Symphony: true,
	}
	want := []string{
		"--role", "cto", "--session", "issue-96",
		"--symphony",
		"--trust", "sandboxed", "--me", "cto", "codex",
	}
	if got := launchArgsFromRecord(rec); !reflect.DeepEqual(got, want) {
		t.Errorf("launchArgsFromRecord(rec)\n got: %v\nwant: %v", got, want)
	}
	if cmd := emitCommandWithOptions(rec, emitCommandOptions{}); !strings.Contains(cmd, "--symphony") {
		t.Errorf("emit command missing --symphony: %s", cmd)
	}
}

func TestLaunchArgsFromRecordPreservesClaudeIdentityForRenameOnResume(t *testing.T) {
	rec := launch.Record{
		Binary:  "claude",
		Handle:  "fullstack",
		Role:    "fullstack",
		Session: "issue-96",
	}
	want := []string{"--role", "fullstack", "--session", "issue-96", "--me", "fullstack", "claude"}
	if got := launchArgsFromRecord(rec); !reflect.DeepEqual(got, want) {
		t.Errorf("launchArgsFromRecord(rec)\n got: %v\nwant: %v", got, want)
	}
	if got, want := claudeSessionRenameName(rec), "fullstack-issue-96"; got != want {
		t.Fatalf("resume rename name = %q, want %q", got, want)
	}
}

// TestLaunchArgsFromRecordDoesNotReplayWakeInjectCmd guards #283 option B: the
// drain inject-cmd lives only on the external wake path (amq wake --inject-cmd).
// amq coop exec has no --inject-cmd, so restore/replay (which rebuilds a
// coop-exec launch) must NOT emit a --wake-inject-cmd/--inject-cmd flag the
// launch path cannot consume. Resume repair of the drain injection is via
// re-running lead register / register-orchestrator, not coop-exec restore. The
// record still carries WakeInjectCmd as durable evidence, and older records
// (no field) stay compatible.
func TestLaunchArgsFromRecordDoesNotReplayWakeInjectCmd(t *testing.T) {
	rec := launch.Record{
		Binary:        "codex",
		Handle:        "cto",
		Role:          "cto",
		Session:       "issue-96",
		WakeInjectCmd: "amq-squad: drain now",
	}
	args := launchArgsFromRecord(rec)
	for _, arg := range args {
		if strings.Contains(arg, "inject-cmd") {
			t.Fatalf("restore must not emit an unsupported inject-cmd flag, got args: %v", args)
		}
	}
	cmd := emitCommandWithOptions(rec, emitCommandOptions{})
	if strings.Contains(cmd, "inject-cmd") {
		t.Fatalf("restore command-string must not emit an unsupported inject-cmd flag: %s", cmd)
	}
}

func TestRunRestoreProjectFlagExpandsHome(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	home := t.TempDir()
	dir := filepath.Join(home, "repos", "app")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96",
		CWD: dir, StartedAt: time.Now().Add(-1 * time.Hour),
	})
	empty := t.TempDir()
	chdir(t, empty)
	stdout, _, err := captureOutput(t, func() error {
		return runRestore([]string{"--project", "~/repos/app", "--role", "cto"})
	})
	if err != nil {
		t.Fatalf("restore --project ~/repos/app: %v", err)
	}
	if !strings.Contains(stdout, "agent up codex") || !strings.Contains(stdout, "cd "+shellQuote(dir)) {
		t.Fatalf("restore --project should scan expanded dir, got:\n%s", stdout)
	}
}

// TestNoBootstrapIsConditionalOnConversation pins the PR2 contract on both
// the emitted (printed) path and the --exec replay path: a record WITH a
// saved conversation is a true reattach and still skips bootstrap; a record
// WITHOUT one is a re-orient resume that must RE-RUN bootstrap (so the agent
// re-reads its brief and drains AMQ history) and therefore must NOT carry
// --no-bootstrap. An explicit operator --no-bootstrap is still honored on
// the printed path even for a re-orient record.
func TestNoBootstrapIsConditionalOnConversation(t *testing.T) {
	withConv := launch.Record{
		CWD: "/p", Binary: "claude", Session: "s", Handle: "claude",
		Role: "qa", Conversation: "thread-1",
	}
	noConv := launch.Record{
		CWD: "/p", Binary: "claude", Session: "s", Handle: "claude",
		Role: "qa",
	}

	// emitCommandWithOptions: reattach keeps --no-bootstrap.
	if cmd := emitCommandWithOptions(withConv, emitCommandOptions{}); !strings.Contains(cmd, "--no-bootstrap") {
		t.Errorf("emit with conversation must keep --no-bootstrap (true reattach): %s", cmd)
	}
	// emitCommandWithOptions: re-orient drops --no-bootstrap.
	if cmd := emitCommandWithOptions(noConv, emitCommandOptions{}); strings.Contains(cmd, "--no-bootstrap") {
		t.Errorf("emit without conversation must drop --no-bootstrap (re-orient): %s", cmd)
	}
	// emitCommandWithOptions: explicit operator --no-bootstrap is honored
	// even for a re-orient record.
	if cmd := emitCommandWithOptions(noConv, emitCommandOptions{NoBootstrap: true}); !strings.Contains(cmd, "--no-bootstrap") {
		t.Errorf("explicit operator --no-bootstrap must be honored on the printed path: %s", cmd)
	}

	// launchArgsFromRecord: reattach keeps --no-bootstrap.
	if args := launchArgsFromRecord(withConv); !containsArg(args, "--no-bootstrap") {
		t.Errorf("replay with conversation must keep --no-bootstrap (true reattach): %v", args)
	}
	// launchArgsFromRecord: re-orient drops --no-bootstrap.
	if args := launchArgsFromRecord(noConv); containsArg(args, "--no-bootstrap") {
		t.Errorf("replay without conversation must drop --no-bootstrap (re-orient): %v", args)
	}
}

func containsArg(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
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
		"--role qa",
		"--session stream1",
	} {
		if !strings.Contains(cmd, want) {
			t.Errorf("emitCommand missing %q in: %s", want, cmd)
		}
	}
	// rec has no Conversation: this is a re-orient resume, so bootstrap must
	// re-run and --no-bootstrap must be absent.
	if strings.Contains(cmd, "--no-bootstrap") {
		t.Errorf("emitCommand must omit --no-bootstrap when record has no conversation; got: %s", cmd)
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

// TestEmitCommandOmitsRootWhenSessionPresent locks the fix for the
// `--session and --root are mutually exclusive` regression: amq treats
// --session NAME as shorthand for --root .agent-mail/<name>, so the
// emitted plan must drop --root whenever --session is set, regardless of
// what BaseRoot the record carries.
func TestEmitCommandOmitsRootWhenSessionPresent(t *testing.T) {
	rec := launch.Record{
		CWD:      "/p",
		Binary:   "claude",
		Session:  "stream1",
		Handle:   "claude",
		Root:     "/tmp/mail/stream1",
		BaseRoot: "/tmp/mail",
	}
	cmd := emitCommand(rec)
	if !strings.Contains(cmd, "--session stream1") {
		t.Errorf("emitCommand should keep --session stream1 in: %s", cmd)
	}
	if strings.Contains(cmd, "--root") {
		t.Errorf("emitCommand must not emit --root alongside --session (amq rejects the combo): %s", cmd)
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
	// Legacy codex records without a trust field restore as sandboxed; --trust
	// sandboxed is emitted explicitly so the trust boundary is visible on
	// replay. No Conversation -> re-orient resume, so --no-bootstrap is absent.
	want := []string{"--role", "cto", "--session", "cto", "--team-workstream", "--trust", "sandboxed", "--me", "cto", "codex"}
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
	// No Conversation -> re-orient resume, so --no-bootstrap is absent.
	want := []string{"--root", "/p/.agent-mail", "--trust", "sandboxed", "--me", "codex", "codex"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("launchArgsFromRecord = %#v, want %#v", got, want)
	}
}

// TestLaunchArgsFromRecordOmitsRootWhenSessionPresent locks the same
// session/root mutual-exclusion fix on the --exec replay path. Without
// this, `resume --exec` replays a saved record into amq env with both
// flags and amq rejects the call before the agent can launch.
func TestLaunchArgsFromRecordOmitsRootWhenSessionPresent(t *testing.T) {
	rec := launch.Record{
		Binary:   "claude",
		Session:  "stream1",
		Handle:   "claude",
		Root:     "/tmp/mail/stream1",
		BaseRoot: "/tmp/mail",
	}
	got := launchArgsFromRecord(rec)
	// No Conversation -> re-orient resume, so --no-bootstrap is absent.
	want := []string{"--session", "stream1", "--me", "claude", "claude"}
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
	// sandboxed. --no-default-args is preserved. No Conversation -> re-orient
	// resume, so --no-bootstrap is absent.
	want := []string{
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
