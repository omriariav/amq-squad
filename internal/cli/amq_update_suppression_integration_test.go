package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/team"
	"github.com/omriariav/amq-squad/v2/internal/tmuxpane"
)

const noisyAMQNotice = "AMQ UPDATE NOTICE"

// setupNoisyAMQ installs a deterministic fake which deliberately contaminates
// both stdout and stderr unless each production subprocess receives
// AMQ_NO_UPDATE_CHECK=1. It supports the representative read and mutation
// surfaces below so these tests exercise the real command boundaries rather
// than replacing runAMQCommand with a recorder seam.
func setupNoisyAMQ(t *testing.T, base string) (noticeMarker, callsPath string) {
	t.Helper()
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	noticeMarker = filepath.Join(t.TempDir(), "notice-fired")
	callsPath = filepath.Join(t.TempDir(), "calls.log")
	t.Setenv("AMQ_NO_UPDATE_CHECK", "0")
	t.Setenv("AMQ_FAKE_BASE", base)
	t.Setenv("AMQ_FAKE_NOTICE_MARKER", noticeMarker)
	t.Setenv("AMQ_FAKE_CALLS", callsPath)
	setupFakeAMQScript(t, `#!/bin/sh
if [ "$AMQ_NO_UPDATE_CHECK" != "1" ]; then
  echo "AMQ UPDATE NOTICE"
  echo "AMQ UPDATE NOTICE" >&2
  printf 'fired\n' >> "$AMQ_FAKE_NOTICE_MARKER"
fi
printf '%s %s|AMQ_NO_UPDATE_CHECK=%s\n' "$1" "$2" "$AMQ_NO_UPDATE_CHECK" >> "$AMQ_FAKE_CALLS"

has_json=0
has_new=0
for arg in "$@"; do
  if [ "$arg" = "--json" ]; then
    has_json=1
  fi
  if [ "$arg" = "--new" ]; then
    has_new=1
  fi
done

if [ "$1" = "env" ]; then
  session=""
  root_arg=""
  while [ "$#" -gt 0 ]; do
    case "$1" in
      --session)
        shift
        session="$1"
        ;;
      --root)
        shift
        root_arg="$1"
        ;;
    esac
    shift
  done
  root="$AMQ_FAKE_BASE"
  if [ -n "$root_arg" ]; then
    root="$root_arg"
  elif [ -n "$session" ]; then
    root="$AMQ_FAKE_BASE/$session"
  fi
  printf '{"schema_version":1,"amq_version":"0.42.0","root":"%s","base_root":"%s","session_name":"%s","me":"outsider"}\n' "$root" "$AMQ_FAKE_BASE" "$session"
  exit 0
fi

case "$1:$2" in
  doctor:--ops)
    printf '%s\n' '{"ops":"ok"}'
    ;;
  route:explain)
    printf '%s\n' '{"routable":true,"argv":["amq","send","--to","qa"]}'
    ;;
  presence:list)
    printf '%s\n' '[]'
    ;;
  receipts:list)
    printf '%s\n' '[]'
    ;;
  receipts:wait)
    printf '%s\n' 'receipt msg-123 drained'
    ;;
  dlq:list)
    printf '%s\n' '[]'
    ;;
  dlq:read)
    printf '%s\n' '{"id":"dlq-1"}'
    ;;
  dlq:retry)
    printf '%s\n' 'retried dlq-1'
    ;;
  dlq:purge)
    printf '%s\n' 'purged dlq'
    ;;
  *)
    case "$1" in
      who)
        if [ "$has_json" = "1" ]; then printf '%s\n' '{"sessions":[]}'; else printf '%s\n' 'no sessions'; fi
        ;;
      drain)
        if [ "$has_json" = "1" ]; then printf '%s\n' '[]'; else printf '%s\n' '[AMQ] drained'; fi
        ;;
      list)
        if [ "$has_json" = "1" ] && [ "$has_new" = "1" ]; then
          printf '%s\n' '[{"id":"m1","from":"qa","thread":"p2p/outsider__qa","box":"new","path":"inbox/new/m1.md"}]'
        elif [ "$has_json" = "1" ]; then printf '%s\n' '[]'; else printf '%s\n' 'no messages'; fi
        ;;
      read)
        if [ "$has_json" = "1" ]; then printf '%s\n' '{"id":"m1"}'; else printf '%s\n' 'read m1'; fi
        ;;
      thread)
        if [ "$has_json" = "1" ]; then printf '%s\n' '[]'; else printf '%s\n' 'plain thread'; fi
        ;;
      send)
        printf '%s\n' 'Sent msg-123 to qa'
        ;;
      reply)
        printf '%s\n' 'Replied msg-124'
        ;;
      cleanup)
        printf '%s\n' 'cleaned tmp'
        ;;
      *)
        printf 'unexpected fake amq command: %s\n' "$*" >&2
        exit 92
        ;;
    esac
    ;;
esac
`)
	return noticeMarker, callsPath
}

func TestAMQNoisyFakeRepresentativeJSONSurfaces(t *testing.T) {
	project := t.TempDir()
	base := filepath.Join(project, ".agent-mail")
	noticeMarker, _ := setupNoisyAMQ(t, base)
	chdir(t, project)

	cases := []struct {
		name string
		args []string
		want string
	}{
		{"ops", []string{"ops", "--project", project, "--session", "issue-96", "--me", "outsider", "--json"}, `{"ops":"ok"}`},
		{"route", []string{"route", "--project", project, "--session", "issue-96", "--me", "outsider", "--to", "qa", "--json"}, `{"routable":true,"argv":["amq","send","--to","qa"]}`},
		{"who", []string{"who", "--project", project, "--session", "issue-96", "--me", "outsider", "--json"}, `{"sessions":[]}`},
		{"presence", []string{"presence", "--project", project, "--session", "issue-96", "--me", "outsider", "--json"}, `[]`},
		{"drain", []string{"drain", "--project", project, "--session", "issue-96", "--me", "outsider", "--json"}, `[]`},
		{"list", []string{"list", "--project", project, "--session", "issue-96", "--me", "outsider", "--json"}, `[]`},
		{"read", []string{"read", "--project", project, "--session", "issue-96", "--me", "outsider", "--id", "m1", "--json"}, `{"id":"m1"}`},
		{"thread", []string{"thread", "--project", project, "--session", "issue-96", "--me", "outsider", "--id", "p2p/cto__qa", "--json"}, `[]`},
		{"receipts", []string{"receipts", "list", "--project", project, "--session", "issue-96", "--me", "outsider", "--json"}, `[]`},
		{"dlq-list", []string{"dlq", "list", "--project", project, "--session", "issue-96", "--me", "outsider", "--json"}, `[]`},
		{"dlq-read", []string{"dlq", "read", "--project", project, "--session", "issue-96", "--me", "outsider", "--id", "dlq-1", "--json"}, `{"id":"dlq-1"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, err := captureOutput(t, func() error { return runAMQ(tc.args) })
			if err != nil {
				t.Fatalf("runAMQ(%v): %v\nstderr:\n%s", tc.args, err, stderr)
			}
			assertJSONPayload(t, stdout, tc.want)
			assertNoNoisyAMQNotice(t, noticeMarker, stdout, stderr)
		})
	}
}

func TestAMQNoisyFakeExactTextSurfaces(t *testing.T) {
	project := t.TempDir()
	base := filepath.Join(project, ".agent-mail")
	root := filepath.Join(base, "issue-96")
	noticeMarker, _ := setupNoisyAMQ(t, base)
	chdir(t, project)

	cases := []struct {
		name string
		args []string
		want string
	}{
		{"send", []string{"send", "--project", project, "--session", "issue-96", "--me", "outsider", "--to", "qa", "--subject", "hello"}, "Sent msg-123 to qa\n"},
		{"reply", []string{"reply", "--project", project, "--session", "issue-96", "--me", "outsider", "--id", "m1", "--body", "ok"}, "Replied msg-124\n"},
		{"drain", []string{"drain", "--project", project, "--session", "issue-96", "--me", "outsider"}, "[AMQ] drained\n"},
		{"receipt-wait", []string{"receipts", "wait", "--project", project, "--session", "issue-96", "--me", "outsider", "--msg-id", "msg-123", "--timeout", "1s"}, "receipt msg-123 drained\n"},
		{"dlq-mutation", []string{"dlq", "retry", "--project", project, "--session", "issue-96", "--me", "outsider", "--id", "dlq-1", "--yes"}, fmt.Sprintf("AMQ command preview\nproject: %s\nroot:    %s\nme:      outsider\ncommand: amq dlq retry --root %s --me outsider --id dlq-1\nretried dlq-1\n", project, root, root)},
		{"cleanup", []string{"cleanup", "--project", project, "--session", "issue-96", "--me", "outsider", "--tmp-older-than", "36h", "--yes"}, fmt.Sprintf("AMQ command preview\nproject: %s\nroot:    %s\nme:      outsider\ncommand: amq cleanup --root %s --tmp-older-than 36h --yes\ncleaned tmp\n", project, root, root)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, err := captureOutput(t, func() error { return runAMQ(tc.args) })
			if err != nil {
				t.Fatalf("runAMQ(%v): %v\nstderr:\n%s", tc.args, err, stderr)
			}
			if stdout != tc.want {
				t.Fatalf("stdout\n got: %q\nwant: %q", stdout, tc.want)
			}
			assertNoNoisyAMQNotice(t, noticeMarker, stdout, stderr)
		})
	}

	seedAgentRecord(t, base, "issue-96", "outsider", launch.Record{
		CWD: project, Binary: "codex", Role: "outsider", Handle: "outsider", Session: "issue-96",
	})
	stdout, stderr, err := captureOutput(t, func() error {
		return runThread([]string{"--project", project, "--session", "issue-96", "--id", "p2p/cto__qa"})
	})
	if err != nil {
		t.Fatalf("plain thread: %v\nstderr:\n%s", err, stderr)
	}
	wantThread := fmt.Sprintf("# amq-squad thread\n# project: %s\n# session: issue-96\n# root: %s\n# thread: p2p/cto__qa\n\nplain thread\n", project, root)
	if stdout != wantThread {
		t.Fatalf("plain thread stdout\n got: %q\nwant: %q", stdout, wantThread)
	}
	assertNoNoisyAMQNotice(t, noticeMarker, stdout, stderr)

	stdout, stderr, err = captureOutput(t, func() error {
		return runThread([]string{"--project", project, "--session", "issue-96", "--id", "p2p/cto__qa", "--json"})
	})
	if err != nil {
		t.Fatalf("thread --json: %v\nstderr:\n%s", err, stderr)
	}
	threadEnv := decodeJSONEnvelope[threadEnvelopeData](t, stdout)
	if threadEnv.Kind != "thread" || threadEnv.Data.ProjectDir != project || threadEnv.Data.Session != "issue-96" || threadEnv.Data.Thread != "p2p/cto__qa" || string(threadEnv.Data.Entries) != "[]" {
		t.Fatalf("thread JSON envelope = %+v", threadEnv)
	}
	assertNoNoisyAMQNotice(t, noticeMarker, stdout, stderr)
}

func TestAMQNoisyFakeHighLevelCommands(t *testing.T) {
	t.Run("dispatch", func(t *testing.T) {
		project := t.TempDir()
		writeDispatchTeam(t, project)
		marker, calls := setupNoisyAMQ(t, filepath.Join(project, ".agent-mail"))
		stdout, stderr, err := captureOutput(t, func() error {
			return runDispatch([]string{"--project", project, "--session", "issue-96", "--role", "qa", "--subject", "test", "--body", "body", "--no-wake", "--json"})
		})
		if err != nil {
			t.Fatalf("dispatch: %v\nstderr:\n%s", err, stderr)
		}
		assertJSONFromByteZero(t, stdout)
		env := decodeJSONEnvelope[mutationResult](t, stdout)
		if env.Kind != "dispatch" || env.Data.MessageID != "msg-123" || env.Data.Handle != "qa" {
			t.Fatalf("dispatch JSON envelope = %+v", env)
		}
		assertNoNoisyAMQNotice(t, marker, stdout, stderr)
		assertNoisyAMQCall(t, calls, "send --root")
	})

	t.Run("goal", func(t *testing.T) {
		project := t.TempDir()
		base := filepath.Join(project, ".agent-mail")
		if err := team.Write(project, team.Team{
			Project: project, Orchestrated: true, Lead: "cto", ExecutionMode: executionModeProjectLead,
			Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
		}); err != nil {
			t.Fatal(err)
		}
		seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
			CWD: project, Binary: "codex", Role: "cto", Handle: "cto", Session: "issue-96", AgentPID: 4242,
			Tmux: &launch.TmuxInfo{PaneID: "%7"},
		})
		marker, calls := setupNoisyAMQ(t, base)
		previousLister := statusPaneLister
		previousSend := sendPromptToPane
		previousFallback := goalFallbackAMQSend
		statusPaneLister = func() ([]tmuxpane.TmuxPane, error) {
			return []tmuxpane.TmuxPane{{PaneID: "%7", CWD: project, Command: "codex", Title: "amq:issue-96:cto"}}, nil
		}
		sendPromptToPane = func(string, string) error {
			return &tmuxpane.SubmitUnconfirmedError{PaneID: "%7", Attempts: 3}
		}
		goalFallbackAMQSend = sendDurableGoalFallback
		t.Cleanup(func() {
			statusPaneLister = previousLister
			sendPromptToPane = previousSend
			goalFallbackAMQSend = previousFallback
		})
		stdout, stderr, err := captureOutput(t, func() error {
			return runGoal([]string{"deliver", "--project", project, "--session", "issue-96", "--role", "cto", "--goal", "ship safely", "--json"})
		})
		if err != nil {
			t.Fatalf("goal deliver: %v\nstderr:\n%s", err, stderr)
		}
		assertJSONFromByteZero(t, stdout)
		env := decodeJSONEnvelope[mutationResult](t, stdout)
		if env.Kind != "goal_deliver" || env.Data.Status != "durable_goal_fallback" || env.Data.MessageID != "msg-123" || env.Data.Role != "cto" {
			t.Fatalf("goal JSON envelope = %+v", env)
		}
		assertNoNoisyAMQNotice(t, marker, stdout, stderr)
		assertNoisyAMQCall(t, calls, "send --root")
	})

	t.Run("operator", func(t *testing.T) {
		project, base, _ := seedNotifyProject(t, team.DefaultOperator())
		seedLegacyOperatorQuestion(t, project, team.DefaultProfile, "s", "cto", "gate/release")
		marker, calls := setupNoisyAMQ(t, base)
		stdout, stderr, err := captureOutput(t, func() error {
			return runOperator([]string{"answer", "--project", project, "--session", "s", "--gate", "release", "--to", "cto", "--approved", "--json"})
		})
		if err != nil {
			t.Fatalf("operator answer: %v\nstderr:\n%s", err, stderr)
		}
		assertJSONFromByteZero(t, stdout)
		env := decodeJSONEnvelope[mutationResult](t, stdout)
		if env.Kind != "operator_send" || env.Data.MessageID != "msg-123" || env.Data.Thread != "gate/release" || env.Data.Handle != "cto" {
			t.Fatalf("operator answer JSON envelope = %+v", env)
		}
		assertNoNoisyAMQNotice(t, marker, stdout, stderr)
		assertNoisyAMQCall(t, calls, "send --root")

		stdout, stderr, err = captureOutput(t, func() error {
			return runOperator([]string{"directive", "--project", project, "--session", "s", "--to", "cto", "--subject", "ship it", "--body", "Proceed after checks.", "--json"})
		})
		if err != nil {
			t.Fatalf("operator directive: %v\nstderr:\n%s", err, stderr)
		}
		assertJSONFromByteZero(t, stdout)
		env = decodeJSONEnvelope[mutationResult](t, stdout)
		if env.Kind != "operator_send" || env.Data.MessageID != "msg-123" || env.Data.Thread != "p2p/cto__user" || env.Data.Handle != "cto" {
			t.Fatalf("operator directive JSON envelope = %+v", env)
		}
		assertNoNoisyAMQNotice(t, marker, stdout, stderr)
		assertNoisyAMQCall(t, calls, "send --root")
	})

	t.Run("collect", func(t *testing.T) {
		project := t.TempDir()
		base := filepath.Join(project, ".agent-mail")
		root := filepath.Join(base, "issue-96")
		seedCollectMessage(t, root, "outsider", "m1", "collected body")
		marker, calls := setupNoisyAMQ(t, base)
		stdout, stderr, err := captureOutput(t, func() error {
			return runCollect([]string{"--project", project, "--session", "issue-96", "--me", "outsider", "--include-body"})
		})
		if err != nil {
			t.Fatalf("collect: %v\nstderr:\n%s", err, stderr)
		}
		if !strings.Contains(stdout, "collected body") {
			t.Fatalf("collect stdout = %q", stdout)
		}
		assertNoNoisyAMQNotice(t, marker, stdout, stderr)
		assertNoisyAMQCall(t, calls, "read --root")
	})

	t.Run("doctor", func(t *testing.T) {
		project := t.TempDir()
		marker, calls := setupNoisyAMQ(t, filepath.Join(project, ".agent-mail"))
		stdout, stderr, _ := captureOutput(t, func() error {
			return runDoctor([]string{"--project", project, "--json"}, "dev")
		})
		assertJSONFromByteZero(t, stdout)
		env := decodeJSONEnvelope[doctorEnvelopeData](t, stdout)
		if env.Kind != "doctor" || env.Data.TeamHome != project || len(env.Data.Checks) == 0 {
			t.Fatalf("doctor JSON envelope = %+v", env)
		}
		assertNoNoisyAMQNotice(t, marker, stdout, stderr)
		assertNoisyAMQCall(t, calls, "doctor --ops")
	})
}

func TestAMQNoisyFakeNativeJSONStartsAtByteZero(t *testing.T) {
	project := t.TempDir()
	writeDispatchTeam(t, project)
	base := filepath.Join(project, ".agent-mail")
	if err := os.MkdirAll(filepath.Join(base, "issue-96", "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	marker, calls := setupNoisyAMQ(t, base)

	cases := []struct {
		name   string
		run    func() error
		assert func(*testing.T, string)
	}{
		{"status", func() error { return runStatus([]string{"--project", project, "--session", "issue-96", "--json"}) }, func(t *testing.T, stdout string) {
			env := decodeJSONEnvelope[statusEnvelopeData](t, stdout)
			if env.Kind != "status" || env.Data.TeamHome != project || env.Data.Workstream != "issue-96" {
				t.Fatalf("status JSON envelope = %+v", env)
			}
		}},
		{"history", func() error { return runHistory([]string{"--project", project, "--json"}) }, func(t *testing.T, stdout string) {
			env := decodeJSONEnvelope[historyEnvelopeData](t, stdout)
			if env.Kind != "history" || len(env.Data.Projects) != 1 || env.Data.Projects[0] != project {
				t.Fatalf("history JSON envelope = %+v", env)
			}
		}},
		{"resume", func() error { return runResume([]string{"--project", project, "--session", "issue-96", "--json"}) }, func(t *testing.T, stdout string) {
			env := decodeJSONEnvelope[resumeEnvelopeData](t, stdout)
			if env.Kind != "resume_plan" || env.Data.TeamHome != project || env.Data.Workstream != "issue-96" {
				t.Fatalf("resume JSON envelope = %+v", env)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, err := captureOutput(t, tc.run)
			if err != nil {
				t.Fatalf("%s: %v\nstderr:\n%s", tc.name, err, stderr)
			}
			assertJSONFromByteZero(t, stdout)
			tc.assert(t, stdout)
			assertNoNoisyAMQNotice(t, marker, stdout, stderr)
		})
	}
	assertNoisyAMQCall(t, calls, "env --json")
}

func assertJSONFromByteZero(t *testing.T, stdout string) {
	t.Helper()
	if stdout == "" || stdout[0] != '{' {
		t.Fatalf("JSON did not start at byte zero: %q", stdout)
	}
	if !json.Valid([]byte(stdout)) {
		t.Fatalf("stdout is not valid JSON: %q", stdout)
	}
}

func assertJSONPayload(t *testing.T, stdout, want string) {
	t.Helper()
	var gotValue, wantValue any
	if err := json.Unmarshal([]byte(stdout), &gotValue); err != nil {
		t.Fatalf("decode JSON payload: %v\nraw: %s", err, stdout)
	}
	if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
		t.Fatalf("decode expected JSON payload: %v\nraw: %s", err, want)
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Fatalf("JSON payload\n got: %s\nwant: %s", stdout, want)
	}
}

func assertNoNoisyAMQNotice(t *testing.T, marker, stdout, stderr string) {
	t.Helper()
	if strings.Contains(stdout, noisyAMQNotice) || strings.Contains(stderr, noisyAMQNotice) {
		t.Fatalf("AMQ update notice leaked\nstdout: %q\nstderr: %q", stdout, stderr)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatalf("fake AMQ observed an unsuppressed child; marker exists at %s", marker)
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat notice marker: %v", err)
	}
	if got := os.Getenv("AMQ_NO_UPDATE_CHECK"); got != "0" {
		t.Fatalf("parent AMQ_NO_UPDATE_CHECK = %q, want unchanged 0", got)
	}
}

func assertNoisyAMQCall(t *testing.T, callsPath, want string) {
	t.Helper()
	b, err := os.ReadFile(callsPath)
	if err != nil {
		t.Fatalf("read fake AMQ calls: %v", err)
	}
	if !strings.Contains(string(b), want) {
		t.Fatalf("fake AMQ calls missing %q:\n%s", want, b)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if !strings.HasSuffix(line, "|AMQ_NO_UPDATE_CHECK=1") {
			t.Fatalf("fake AMQ call did not receive suppression: %q", line)
		}
	}
}
