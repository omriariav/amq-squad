#!/usr/bin/env python3
"""Tests for the amq-squad Claude Code hooks plugin (brief 4-2, plugins/claude/hooks).

Usage: test_claude_hooks.py [path to the amq-squad binary]
       (default: $AMQ_SQUAD_BIN, then ./amq-squad)
       HOOK_FAULT_REPORT=<file> writes the measured fault table as JSON.

The hook is run the way Claude Code runs a command hook: the command string
from hooks.json with CLAUDE_PLUGIN_ROOT set, the payload on stdin, killed at the
configured timeout. Claude's own decision rule is modelled in claude_decision()
from the S4 fault table (docs/v3-spike/S4-report.md): a JSON deny (or exit 2)
blocks; a timeout, a non-zero exit without a deny and a missing command do not.
Every case uses a fresh HOME under a temp directory; nothing touches ~/.claude.
"""
import json
import os
import shlex
import shutil
import signal
import subprocess
import sys
import tempfile
import time
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
REPO = os.path.dirname(HERE)
PLUGIN = os.path.join(REPO, "plugins", "claude")


def read(path):
    with open(path) as f:
        return f.read()


HOOKS = json.loads(read(os.path.join(PLUGIN, "hooks", "hooks.json")))["hooks"]
BIN = os.path.abspath(sys.argv.pop(1)) if len(sys.argv) > 1 and not sys.argv[1].startswith("-") else None
BIN = BIN or os.path.abspath(os.environ.get("AMQ_SQUAD_BIN") or os.path.join(REPO, "amq-squad"))
REPORT = os.environ.get("HOOK_FAULT_REPORT")
FAULTS = []

EVENTS = {"SessionStart": "session-start", "UserPromptSubmit": "user-prompt", "PreToolUse": "pre-tool", "PostToolUse": "post-tool"}


def hook_entry(event):
    return HOOKS[event][0]["hooks"][0]


def shell_argv(command):
    """The hook command under HOOK_SHELL (dash on Linux CI), else /bin/sh."""
    return [os.environ.get("HOOK_SHELL") or "/bin/sh", "-c", command]


def guard_stub(base, name, body):
    """A stand-in amq-squad whose guard child runs body; the adapter
    (guard --decision claude) is the real binary."""
    p = os.path.join(base, name)
    with open(p, "w") as f:
        f.write('#!/bin/sh\nif [ "$2" = --decision ]; then exec %s "$@"; fi\n%s' % (shlex.quote(BIN), body))
    os.chmod(p, 0o755)
    return p


def run_hook(event, payload, env, timeout=None):
    """Run the plugin's command for event. Returns (rc, stdout, stderr, seconds, timed_out)."""
    entry = hook_entry(event)
    timeout = entry["timeout"] if timeout is None else timeout
    e = dict(env)
    e["CLAUDE_PLUGIN_ROOT"] = PLUGIN
    data = payload if isinstance(payload, (bytes, str)) else json.dumps(payload)
    if isinstance(data, str):
        data = data.encode()
    t0 = time.monotonic()
    p = subprocess.Popen(shell_argv(entry["command"]), stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                         stderr=subprocess.PIPE, env=e, start_new_session=True)
    try:
        out, err = p.communicate(data, timeout=timeout)
        return p.returncode, out.decode(), err.decode(), time.monotonic() - t0, False
    except subprocess.TimeoutExpired:
        os.killpg(p.pid, signal.SIGKILL)
        out, err = p.communicate()
        return None, out.decode(), err.decode(), time.monotonic() - t0, True


def claude_decision(rc, out, timed_out):
    """What Claude Code does with a PreToolUse hook result (S4 fault table)."""
    if timed_out:
        return "proceed"
    if rc == 2:
        return "block"
    if rc != 0:
        return "proceed"
    try:
        spec = json.loads(out).get("hookSpecificOutput", {}) if out.strip() else {}
    except ValueError:
        return "proceed"
    return {"deny": "block", "ask": "prompt"}.get(spec.get("permissionDecision"), "proceed")


class Seat:
    """A seeded seat: session root, mailbox, a seat worktree on seat-cto, a fresh HOME."""

    def __init__(self, test):
        self.base = os.path.realpath(tempfile.mkdtemp(prefix="hooktest-"))
        test.addCleanup(shutil.rmtree, self.base, True)
        self.home = os.path.join(self.base, "home")
        self.project = os.path.join(self.base, "project")
        self.root = os.path.join(self.project, ".agent-mail", "s1")
        self.repo = os.path.join(self.base, "wt")
        self.agent = os.path.join(self.root, "agents", "cto")
        for d in (self.home, self.agent, os.path.join(self.root, "agents", "qa"),
                  os.path.join(self.repo, ".git", "refs", "remotes", "origin")):
            os.makedirs(d, exist_ok=True)
        self.write(os.path.join(self.repo, ".git", "HEAD"), "ref: refs/heads/seat-cto\n")
        self.write(os.path.join(self.repo, ".git", "refs", "remotes", "origin", "HEAD"), "ref: refs/remotes/origin/main\n")
        self.env = {"PATH": os.environ["PATH"], "HOME": self.home, "AM_ME": "cto", "AM_ROOT": self.root,
                    "AM_SESSION": "s1", "AMQ_SQUAD_BIN": BIN, "TMPDIR": self.base}

    def write(self, path, body, mode=0o600):
        os.makedirs(os.path.dirname(path), exist_ok=True)
        with open(path, "w") as f:
            f.write(body)
        os.chmod(path, mode)

    def grants(self, body):
        self.write(os.path.join(self.home, ".amq-squad", "grants.yaml"), body)

    def log(self):
        p = os.path.join(self.root, "extensions", "amq-squad", "hooklog", "cto.log")
        return read(p) if os.path.exists(p) else ""

    def pre(self, tool, tool_input, **kw):
        payload = {"hook_event_name": "PreToolUse", "tool_name": tool, "tool_input": tool_input, "cwd": self.repo,
                   "session_id": "x", "tool_use_id": "toolu_1"}
        return run_hook("PreToolUse", payload, self.env, **kw)

    def guard_direct(self, tool, tool_input):
        payload = json.dumps({"tool_name": tool, "tool_input": tool_input, "cwd": self.repo})
        p = subprocess.run([BIN, "guard", "--me", "cto", "--stdin"], input=payload, capture_output=True, text=True, env=self.env)
        return p.returncode, json.loads(p.stdout)


def decision(out):
    return json.loads(out)["hookSpecificOutput"]


def bash(cmd):
    return "Bash", {"command": cmd}


def seed_max_seats_two(s):
    s.grants("max_seats: 2\n")


def seed_protected(s):
    s.grants("protected_paths:\n  - .amq-squad/team-rules.md\n")


def seed_bad_grants(s):
    s.grants("bogus: 1\n")


# reason code -> (setup, tool, input, expected hook decision, expected reason check)
MAPPING = [
    ("allowed", None, *bash("go test ./..."), "allow"),
    ("gate_release", None, *bash("git tag v1"), "deny"),
    ("gate_send", None, "mcp__slack__send_message", {"channel": "C1", "text": "hi"}, "deny"),
    ("gate_credential", None, *bash("gh auth login --with-token"), "deny"),
    ("gate_grow", seed_max_seats_two, *bash("amq launch --seat dev"), "deny"),
    ("seat_merge", None, *bash("gh pr merge 12 --squash"), "deny"),
    ("force_push", None, *bash("git push --force origin seat-cto"), "deny"),
    ("push_delete", None, *bash("git push origin :seat-cto"), "deny"),
    ("push_other_branch", None, *bash("git push origin feature-x"), "deny"),
    ("protected_path", seed_protected, "Write", {"file_path": ".amq-squad/team-rules.md"}, "deny"),
    ("grants_invalid", seed_bad_grants, *bash("go test ./..."), "deny"),
]


class MappingTable(unittest.TestCase):
    def test_every_guard_reason_code_maps_per_the_table(self):
        seen = set()
        for code, setup, tool, tool_input, want in MAPPING:
            with self.subTest(reason_code=code):
                s = Seat(self)
                if setup:
                    setup(s)
                grc, g = s.guard_direct(tool, tool_input)
                self.assertEqual((grc, g["reason_code"]), (0, code), g)
                rc, out, err, secs, timed_out = s.pre(tool, tool_input)
                self.assertEqual((rc, err), (0, ""), out)
                d = decision(out)
                self.assertEqual(d["hookEventName"], "PreToolUse")
                self.assertEqual(d["permissionDecision"], want)
                self.assertNotEqual(d["permissionDecision"], "ask")
                if g["verdict"] == "needs_gate":
                    self.assertEqual(d["permissionDecisionReason"], "raise the gate for %s %s" % (g["action_class"], g["target"]))
                elif g["verdict"] == "deny":
                    self.assertEqual(d["permissionDecisionReason"], "amq-squad guard: " + g["reason"])
                seen.add(code)
        # every reason code the verbs can emit (docs/v3-spike/2a3-notes.md) has a row
        self.assertEqual(seen, {"allowed", "gate_release", "gate_send", "gate_credential", "gate_grow", "seat_merge",
                                "force_push", "push_delete", "push_other_branch", "protected_path", "grants_invalid"})

    def test_deny_reason_carries_quotes_and_backslashes_as_valid_json(self):
        s = Seat(self)
        seed_protected(s)
        rc, out, _, _, _ = s.pre("Write", {"file_path": '.amq-squad/team-rules.md'})
        self.assertEqual(rc, 0)
        json.loads(out)  # valid JSON
        # a target with quotes and a backslash round-trips through the needs_gate reason
        cmd = 'git tag "v1\\x" && echo \'q\''
        rc, out, _, _, _ = s.pre(*bash(cmd))
        d = decision(out)
        _, g = s.guard_direct(*bash(cmd))
        self.assertEqual(g["verdict"], "needs_gate")
        self.assertEqual(d["permissionDecisionReason"], "raise the gate for %s %s" % (g["action_class"], g["target"]))


class NeedsGateNeverAsks(unittest.TestCase):
    def test_git_tag_without_an_answer_is_denied_with_the_gate_reason(self):
        s = Seat(self)
        rc, out, err, secs, timed_out = s.pre(*bash("git tag v1"))
        self.assertEqual((rc, err, timed_out), (0, "", False))
        d = decision(out)
        self.assertEqual(d["permissionDecision"], "deny")
        self.assertEqual(d["permissionDecisionReason"], "raise the gate for tag git tag v1")
        self.assertNotIn("ask", out.replace("task", ""))  # no ask decision anywhere
        self.assertEqual(claude_decision(rc, out, timed_out), "block")  # the seat gets a tool error, not a prompt


class OutsideASeat(unittest.TestCase):
    def test_every_event_is_silent_and_exit_zero(self):
        for unset in ("AM_ME", None):
            s = Seat(self)
            env = dict(s.env)
            if unset:
                del env[unset]
            else:
                env["AM_ME"] = ""
            for event in EVENTS:
                with self.subTest(event=event, am_me=unset or "empty"):
                    rc, out, err, secs, _ = run_hook(event, {"tool_name": "Bash", "tool_input": {"command": "git tag v1"}}, env)
                    self.assertEqual((rc, out, err), (0, "", ""))
            self.assertEqual(s.log(), "")


class FaultTable(unittest.TestCase):
    """Acceptance 3: the S4 fault table against the plugin's PreToolUse hook."""

    def record(self, row, secs, **kw):
        FAULTS.append(dict(row=row, seconds=round(secs, 3), **kw))

    def test_deny_blocks(self):
        s = Seat(self)
        rc, out, _, secs, to = s.pre(*bash("git push --force origin seat-cto"))
        self.assertEqual(claude_decision(rc, out, to), "block")
        self.record("deny_blocks", secs, s4_seconds=0.166, outcome="blocked")

    def test_hung_guard_is_cut_by_the_hook_watchdog_and_fails_open(self):
        s = Seat(self)
        slow = guard_stub(s.base, "slow-amq-squad", "sleep 30\n")
        s.env["AMQ_SQUAD_BIN"] = slow
        configured = hook_entry("PreToolUse")["timeout"]
        self.assertEqual(configured, 5)
        # a generous harness timeout: the outcome under test is that the hook ends itself, not a time bound
        rc, out, _, secs, to = s.pre(*bash("git push --force origin seat-cto"), timeout=20)
        self.assertFalse(to)
        self.assertEqual((rc, out), (0, ""))
        self.assertEqual(claude_decision(rc, out, to), "proceed")
        self.assertIn("fail-open: guard timeout after 4.8 s", s.log())
        self.record("timeout_fails_open", secs, s4_seconds=5.111, configured_timeout=configured, outcome="proceeded",
                    ended_by="hook watchdog (4.8 s)", inside_hook_deadline=secs < configured)

    def test_unwritable_log_does_not_stop_guard(self):
        s = Seat(self)
        logdir = os.path.join(s.root, "extensions", "amq-squad", "hooklog")
        os.makedirs(os.path.join(logdir, "cto.log"))  # the log path is a directory
        rc, out, err, _, to = s.pre(*bash("git push --force origin seat-cto"))
        self.assertEqual((rc, err), (0, ""))  # no shell diagnostic reaches the model
        self.assertEqual(decision(out)["permissionDecision"], "deny")  # guard ran
        self.assertEqual(claude_decision(rc, out, to), "block")
        rc, out, err, _, _ = s.pre("Bash", "{not json")  # fail-open path with an unwritable log
        self.assertEqual((rc, out, err), (0, "", ""))
        for event in ("SessionStart", "PostToolUse"):
            self.assertEqual(run_hook(event, {"tool_name": "Bash"}, s.env)[0], 0)

    def test_guard_exit_one_fails_open_and_is_logged(self):
        s = Seat(self)
        rc, out, _, secs, to = s.pre("Bash", "{not json")  # guard: malformed payload -> exit 1, no stdout
        self.assertEqual((rc, out, to), (0, "", False))
        self.assertEqual(claude_decision(rc, out, to), "proceed")
        self.assertIn("fail-open: guard exit 1", s.log())
        self.record("guard_exit_1_fails_open", secs, s4_seconds=0.356, outcome="proceeded")

    def test_missing_command_fails_open_and_is_logged(self):
        s = Seat(self)
        s.env["AMQ_SQUAD_BIN"] = os.path.join(s.base, "no-such-amq-squad")
        rc, out, _, secs, to = s.pre(*bash("git tag v1"))
        self.assertEqual((rc, out, to), (0, "", False))
        self.assertEqual(claude_decision(rc, out, to), "proceed")
        self.assertIn("fail-open: ", s.log())
        self.record("missing_command_fails_open", secs, s4_seconds=0.440, outcome="proceeded")

    def test_missing_hook_script_fails_open(self):
        # The plugin's own command missing (S4 row c): the shell exits 127, a non-blocking error.
        s = Seat(self)
        e = dict(s.env, CLAUDE_PLUGIN_ROOT=os.path.join(s.base, "gone"))
        t0 = time.monotonic()
        p = subprocess.run(shell_argv(hook_entry("PreToolUse")["command"]), input=b"{}", capture_output=True, env=e)
        secs = time.monotonic() - t0
        self.assertNotIn(p.returncode, (0, 2))
        self.assertEqual(claude_decision(p.returncode, p.stdout.decode(), False), "proceed")
        self.record("missing_hook_script_fails_open", secs, exit_code=p.returncode, outcome="proceeded")

    def test_other_events_never_exit_two(self):
        # exit 2 would erase a prompt (UserPromptSubmit) or feed stderr to the model (PostToolUse).
        s = Seat(self)
        shutil.rmtree(s.agent)  # identity error: context and ingest exit 2 on their own
        for event in ("SessionStart", "UserPromptSubmit", "PostToolUse"):
            with self.subTest(event=event):
                rc, out, err, _, _ = run_hook(event, {"tool_name": "Bash"}, s.env)
                self.assertEqual((rc, out, err), (0, "", ""))
        self.assertIn("fail-open: context exit 2", s.log())
        self.assertIn("fail-open: evidence ingest exit 2", s.log())


class ContextInjection(unittest.TestCase):
    def seed(self, s):
        ext = os.path.join(s.agent, "extensions", "io.github.omriariav.amq-squad")
        s.write(os.path.join(ext, "role.md"), '# Role: CTO\nOwn the "architecture" \\ ok.\n')
        rules = os.path.join(s.project, ".amq-squad", "team-rules.md")
        s.write(rules, "# Team Rules\n")
        s.env["AMQ_SQUAD_RULES_PATH"] = rules
        for args in (["task", "add", "--root", s.root, "--title", "build the verbs", "--class", "implement"],
                     ["task", "claim", "--root", s.root, "--me", "cto"]):
            p = subprocess.run([BIN] + args, capture_output=True, text=True, env=s.env)
            self.assertEqual(p.returncode, 0, p.stderr)
        gate = {"schema": "s1", "gate_id": "g-open", "class": "tag", "digest": "sha256:" + "0" * 64,
                "not_after": "2099-01-01T00:00:00Z", "raised_by": "cto"}
        s.write(os.path.join(s.root, "extensions", "amq-squad", "gates", "g-open.json"), json.dumps(gate))

    def test_session_start_and_every_prompt_carry_role_rules_task_and_gates(self):
        s = Seat(self)
        self.seed(s)
        for event in ("SessionStart", "UserPromptSubmit", "UserPromptSubmit"):
            rc, out, err, secs, _ = run_hook(event, {"hook_event_name": event, "prompt": "hi"}, s.env)
            self.assertEqual((rc, err), (0, ""))
            d = decision(out)
            self.assertEqual(d["hookEventName"], event)
            text = d["additionalContext"]
            self.assertIn("build the verbs", text)  # current task
            self.assertIn("g-open", text)  # open gate
            self.assertIn("architecture", text)  # role text, quotes and backslash intact
            payload = json.loads(text[text.index("{"):])["data"]
            self.assertTrue(payload["role"]["sha256"].startswith("sha256:"))  # role digest
            self.assertTrue(payload["rules"]["sha256"].startswith("sha256:"))  # rules digest
            self.assertEqual(payload["task"]["title"], "build the verbs")
            self.assertEqual([g["gate_id"] for g in payload["open_gates"]], ["g-open"])
            self.assertEqual(payload["role_text"], '# Role: CTO\nOwn the "architecture" \\ ok.\n')
        print("context injection: last run %.1f ms" % (secs * 1000))


class Evidence(unittest.TestCase):
    def test_twenty_tool_calls_give_twenty_ledger_lines(self):
        s = Seat(self)
        for i in range(20):
            payload = {"hook_event_name": "PostToolUse", "tool_name": "Bash", "tool_use_id": "toolu_%d" % i,
                       "tool_input": {"command": "ls"}, "tool_response": {"stdout": "x"}}
            rc, out, err, _, _ = run_hook("PostToolUse", payload, s.env)
            self.assertEqual((rc, out, err), (0, "", ""))
        path = os.path.join(s.root, "extensions", "amq-squad", "ledger", "cto.jsonl")
        lines = [json.loads(l) for l in read(path).splitlines()]
        self.assertEqual([l["seq"] for l in lines], list(range(1, 21)))
        self.assertEqual(lines[0]["tool"], "Bash")
        self.assertEqual(s.log(), "")


class Performance(unittest.TestCase):
    """Latency checks that report and never gate: a loaded host moves them."""

    def test_report_outside_a_seat_overhead(self):
        s = Seat(self)
        env = dict(s.env)
        del env["AM_ME"]
        samples = sorted(run_hook(e, {"tool_name": "Bash"}, env)[3] * 1000 for e in EVENTS for _ in range(15))
        p50, p95, worst = samples[len(samples) // 2], samples[int(len(samples) * 0.95)], samples[-1]
        print("PERF outside a seat: %d runs, p50 %.1f ms, p95 %.1f ms, max %.1f ms (budget p95 < 20 ms: %s)"
              % (len(samples), p50, p95, worst, "met" if p95 < 20 else "NOT met"))
        FAULTS.append({"row": "outside_seat_overhead", "runs": len(samples), "p50_ms": round(p50, 1), "p95_ms": round(p95, 1), "max_ms": round(worst, 1)})

    def test_report_guard_hook_latency(self):
        s = Seat(self)
        samples = sorted(s.pre(*bash("go test ./..."))[3] * 1000 for _ in range(30))
        print("PERF pre-tool allow in a seat: p50 %.1f ms, p95 %.1f ms" % (samples[15], samples[28]))


class Manifest(unittest.TestCase):
    def test_timeout_is_five_seconds_on_every_event_and_the_script_is_a_sh_file(self):
        for event, name in EVENTS.items():
            entry = hook_entry(event)
            self.assertEqual(entry["timeout"], 5, event)
            self.assertEqual(entry["type"], "command")
            self.assertTrue(entry["command"].endswith(" " + name), entry["command"])
        self.assertEqual(HOOKS["PreToolUse"][0]["matcher"], "*")
        self.assertEqual(HOOKS["PostToolUse"][0]["matcher"], "*")
        self.assertTrue(read(os.path.join(PLUGIN, "hooks", "amq-squad-hook.sh")).startswith("#!/bin/sh\n"))

    def test_no_project_scope_stub_in_the_repo(self):
        self.assertFalse(os.path.exists(os.path.join(REPO, ".claude", "settings.json")))
        self.assertFalse(os.path.exists(os.path.join(PLUGIN, ".claude")))


def alive(pid):
    try:
        os.kill(pid, 0)
        return True
    except ProcessLookupError:
        return False


class Repass5(unittest.TestCase):
    """Phase 5 re-pass of 4-2: the guard's process group and the Go verdict adapter."""

    def test_hung_guard_group_is_killed_and_the_reap_stays_out_of_stderr(self):
        # P2-4: a stub guard that starts a sleeper and hangs; the watchdog kills
        # the whole group, and the shell's job report never reaches stderr.
        s = Seat(self)
        pidf = os.path.join(s.base, "sleeper.pid")
        stub = guard_stub(s.base, "hung-amq-squad", "sleep 30 &\necho $! > %s\nwait\n" % pidf)
        s.env["AMQ_SQUAD_BIN"] = stub
        rc, out, err, _, to = s.pre(*bash("go test ./..."), timeout=20)
        self.assertFalse(to)
        self.assertEqual((rc, out, err), (0, "", ""))
        pid = int(read(pidf).strip())
        deadline = time.monotonic() + 2
        while alive(pid) and time.monotonic() < deadline:
            time.sleep(0.05)
        survived = alive(pid)
        if survived:
            os.kill(pid, signal.SIGKILL)
        self.assertFalse(survived, "the guard's sleeper survived the watchdog")
        self.assertIn("fail-open: guard timeout after 4.8 s", s.log())

    def test_truncated_verdict_is_no_decision_and_logged(self):
        # P2-5: a truncated {"verdict":"allow" is not an allow; the Go adapter
        # parses the complete object and logs anything else as no decision.
        s = Seat(self)
        stub = os.path.join(s.base, "truncating-amq-squad")
        s.write(stub, '#!/bin/sh\nif [ "$1" = guard ] && [ "$2" = --me ]; then cat >/dev/null; printf \'{"verdict":"allow"\'; exit 0; fi\nexec %s "$@"\n' % shlex.quote(BIN), 0o755)
        s.env["AMQ_SQUAD_BIN"] = stub
        rc, out, err, _, to = s.pre(*bash("git push --force origin seat-cto"))
        self.assertEqual((rc, out, to), (0, "", False))
        self.assertIn("unparsable guard output", s.log())
        # the complete verdict still maps (the real guard: deny)
        s.env["AMQ_SQUAD_BIN"] = BIN
        rc, out, _, _, to = s.pre(*bash("git push --force origin seat-cto"))
        self.assertEqual(decision(out)["permissionDecision"], "deny")


def tearDownModule():
    if REPORT:
        with open(REPORT, "w") as f:
            json.dump(FAULTS, f, indent=2)
            f.write("\n")


if __name__ == "__main__":
    unittest.main()
