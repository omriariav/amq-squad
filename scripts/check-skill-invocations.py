#!/usr/bin/env python3
"""Fail the build when a documented skill invocation cannot actually be run.

The existing gate (check-skill-commands.py, #534) proves every named verb and
flag EXISTS on the binary. cli/LEARNINGS.md records why that is not enough:
four documented invocations errored as written while that gate stayed green —
two were missing required arguments (`evidence run` without `-- COMMAND`,
`gate raise` without --gate/--kind/--action/--target) and two passed a real
flag to a command that does not accept it (`verify merge --session S`). Flag
existence is not invocation correctness; the only method that catches this
class is EXECUTING the table.

This gate does exactly that. It extracts every complete `amq-squad ...`
invocation from plugins/skills-src (SKILL.md + references/), substitutes the
docs' uppercase placeholders with fixture values, and executes each one in a
throwaway fixture directory with no team configured.

Classification rides the binary's exit-code taxonomy (internal/cli/exit.go):
exit 1 is ExitUser — UsageError: unknown flag, bad argument shape, missing
required selector — which for a DOCUMENTED invocation means the doc is wrong.
Any other exit (0; 2 state/system e.g. "no team configured"; 3 partial; 10-13
policy) proves the invocation parsed and reached state resolution, which is
the property this gate exists to hold. Required-argument validation in the
audited commands runs before namespace resolution (verified for `gate raise`
and `evidence run`), so the empty fixture does not mask the failure class.

Deliberately out of scope:
- LEARNINGS.md files: they quote broken invocations as lessons, on purpose.
- bare `amq ...` commands: a different tool, not this binary's contract.
- verb-only prose mentions like `amq-squad send` with no flags or arguments:
  those are references, not invocations, and the existence gate covers them.
"""

from __future__ import annotations

import json
import re
import shlex
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
SKILLS = REPO / "plugins" / "skills-src"

RUN_TIMEOUT_SECONDS = 30

# One entry per uppercase placeholder used by the skills. An invocation using
# a placeholder with no mapping fails the gate with an "add a mapping" error
# rather than being skipped: a skipped invocation is exactly how drift hides.
# Values must be VALID shapes (session-name rules, hex widths, catalog kinds)
# so a substituted command can only fail usage-classification when the doc
# itself is wrong. {fixture} expands to the per-invocation fixture directory.
PLACEHOLDERS = {
    "P": "{fixture}",
    "DIR": "{fixture}",
    "PROJECT": "{fixture}",
    "R": "relprof",
    "PROFILE": "relprof",
    "NAME": "relprof",
    "S": "issue-1",
    "SESSION": "issue-1",
    "H": "cto",
    "ACTOR": "cto",
    "OPERATOR": "operator",
    "ID": "t1",
    "TASK": "t1",
    "ATTEMPT": "a1",
    "TEXT": "text",
    "TOPIC": "demo",
    "THREAD": "p2p/cto__qa",
    "TARGET": "target-1",
    "ROLE": "qa",
    "B": "codex",
    "FILE": "{fixture}/ev.md",
    "OWNER": "owner",
    "REPO": "repo",
    "X": "9",
    "Y": "9",
    "Z": "9",
}

# `gate raise` validates --kind/--action against the shared catalog before
# resolving any context, so the fixture values must be real catalog entries.
# They are read from the binary itself (--list-kinds --json) rather than
# hard-coded: a hand-maintained mirror of the catalog would be the very
# drift this gate exists to catch.
def catalog_kind_action(binary: str) -> tuple[str, str]:
    out = subprocess.run(
        [binary, "gate", "raise", "--list-kinds", "--json"],
        capture_output=True, text=True, timeout=RUN_TIMEOUT_SECONDS,
    )
    if out.returncode != 0:
        raise SystemExit(f"gate raise --list-kinds --json failed:\n{out.stderr}")
    data = json.loads(out.stdout)
    # Envelope shape: {..., "data": {"actions": [{"action": ..., "gate_kind": ...}]}}.
    # Walk for the first actions entry carrying both fields rather than pinning
    # the nesting, so an envelope reshape does not silently break the fixture.
    def walk(node):
        if isinstance(node, dict):
            action = node.get("action")
            gate_kind = node.get("gate_kind")
            if isinstance(action, str) and isinstance(gate_kind, str):
                return gate_kind, action
            for value in node.values():
                found = walk(value)
                if found:
                    return found
        if isinstance(node, list):
            for value in node:
                found = walk(value)
                if found:
                    return found
        return None
    found = walk(data)
    if not found:
        raise SystemExit("could not find a kind/action pair in --list-kinds --json output")
    return found

# Multi-token placeholder for the evidence wrapper argv. Replaced before the
# single-token pass so COMMAND does not need (and must not have) its own entry.
COMMAND_ARGV = re.compile(r"COMMAND \[ARG\.\.\.\]")

# The canonical version placeholder. `vX` has no word boundary between the
# lowercase v and the X, so the single-token pass cannot reach it.
VERSION_PLACEHOLDER = re.compile(r"\bvX\.Y\.Z\b")

# Angle placeholders: <40-hex-sha>, <64-hex>, <title>, <reason>, <why>...
ANGLE = re.compile(r"<([0-9A-Za-z-]+)>")

# Illustrative absolute paths (/path/cto, /path/to/wt-a). Rewritten under the
# fixture and created, so a command validating directory existence sees a real
# directory instead of failing on the doc's illustrative path.
ILLUSTRATIVE_PATH = re.compile(r"/path(?:/[A-Za-z0-9._-]+)+")

# No `.` in the class: `OWNER/REPO.git` must yield REPO with `.git` untouched,
# and `vX.Y.Z` must yield X, Y, Z individually.
UPPER_TOKEN = re.compile(r"\b([A-Z][A-Z_-]*)\b")

SKIP_MARKER = "skill-invocation-check:skip"


def angle_value(name: str) -> str:
    lowered = name.lower()
    if "64" in lowered and "hex" in lowered:
        return "a" * 64
    if "hex" in lowered or "sha" in lowered:
        return "a" * 40
    return re.sub(r"[^a-z0-9-]", "", lowered) or "value"


def substitute(command: str, fixture: Path, kind: str, action: str) -> str:
    # Placeholder passes run on the documented text only; the fixture path is
    # spliced in LAST via a literal marker. Substituting real paths first would
    # feed them back into the token pass (a macOS tempdir contains "/T/",
    # which the first version of this script read as an unmapped placeholder).
    text = COMMAND_ARGV.sub("make ci", command)
    text = VERSION_PLACEHOLDER.sub("v9.9.9", text)
    text = ANGLE.sub(lambda m: angle_value(m.group(1)), text)

    unknown: list[str] = []

    def token_value(m: re.Match) -> str:
        token = m.group(1)
        if token == "KIND":
            return kind
        if token == "ACTION":
            return action
        if token in PLACEHOLDERS:
            return PLACEHOLDERS[token]
        unknown.append(token)
        return token

    text = UPPER_TOKEN.sub(token_value, text)
    if unknown:
        raise KeyError(
            f"no fixture mapping for placeholder(s) {sorted(set(unknown))} in: {command}\n"
            "add an entry to PLACEHOLDERS in scripts/check-skill-invocations.py"
        )
    text = ILLUSTRATIVE_PATH.sub(
        lambda m: "{fixture}/paths/" + m.group(0).lstrip("/").replace("/", "_"), text
    )
    text = text.replace("{fixture}", str(fixture))
    for segment in re.findall(re.escape(str(fixture)) + r"/paths/[A-Za-z0-9._-]+", text):
        Path(segment).mkdir(parents=True, exist_ok=True)
    return text


def strip_inline_comment(text: str) -> str:
    quote = None
    for i, ch in enumerate(text):
        if quote:
            if ch == quote:
                quote = None
            continue
        if ch in "\"'":
            quote = ch
            continue
        if ch == "#" and (i == 0 or text[i - 1] in " \t"):
            return text[:i]
    return text


def extract_from_fences(text: str, path: Path) -> list[tuple[Path, int, str]]:
    out: list[tuple[Path, int, str]] = []
    lines = text.splitlines()
    in_fence = False
    pending: str | None = None
    pending_line = 0
    for i, raw in enumerate(lines, start=1):
        stripped = raw.strip()
        if stripped.startswith("```"):
            in_fence = not in_fence
            pending = None
            continue
        if not in_fence:
            continue
        if pending is not None:
            joined = pending.rstrip("\\").rstrip() + " " + stripped
            if joined.rstrip().endswith("\\"):
                pending = joined
            else:
                out.append((path, pending_line, strip_inline_comment(joined).strip()))
                pending = None
            continue
        if i - 2 >= 0 and SKIP_MARKER in lines[i - 2]:
            continue
        candidate = stripped
        # A pipeline segment counts: `cat x | amq-squad send ...` documents the
        # amq-squad invocation, not the cat.
        if "| amq-squad " in candidate:
            candidate = candidate.split("| amq-squad ", 1)[1]
            candidate = "amq-squad " + candidate
        if candidate.startswith("$ "):
            candidate = candidate[2:]
        if not candidate.startswith("amq-squad "):
            continue
        if candidate.rstrip().endswith("\\"):
            pending = candidate
            pending_line = i
            continue
        out.append((path, i, strip_inline_comment(candidate).strip()))
    return out


INLINE_SPAN = re.compile(r"`(amq-squad [^`]+)`")


def extract_inline(text: str, path: Path) -> list[tuple[Path, int, str]]:
    out: list[tuple[Path, int, str]] = []
    lines = text.splitlines()
    # Inline spans can wrap across source lines; scan a newline-flattened copy
    # but recover the line number from the span's start offset.
    flat = text
    for m in INLINE_SPAN.finditer(flat):
        span = " ".join(m.group(1).split())
        line = flat.count("\n", 0, m.start()) + 1
        if 0 <= line - 1 < len(lines) and SKIP_MARKER in lines[line - 1]:
            continue
        if line - 2 >= 0 and SKIP_MARKER in lines[line - 2]:
            continue
        # Verb-only prose mentions (`amq-squad send`) are references, not
        # invocations. An invocation carries at least one flag or placeholder.
        if "--" not in span and not UPPER_TOKEN.search(span[len("amq-squad "):]):
            continue
        out.append((path, line, span))
    return out


def collect() -> list[tuple[Path, int, str]]:
    found: list[tuple[Path, int, str]] = []
    for md in sorted(SKILLS.glob("*/SKILL.md")) + sorted(SKILLS.glob("*/references/*.md")):
        if md.name == "LEARNINGS.md":
            continue
        text = md.read_text(encoding="utf-8")
        fence = extract_from_fences(text, md)
        found.extend(fence)
        fence_commands = {c for (_, _, c) in fence}
        for item in extract_inline(text, md):
            if item[2] not in fence_commands:
                found.append(item)
    return found


def make_fixture(root: Path, index: int) -> Path:
    fixture = root / f"inv-{index}"
    fixture.mkdir(parents=True)
    (fixture / "prompt.md").write_text("hello\n", encoding="utf-8")
    (fixture / "ev.md").write_text("evidence\n", encoding="utf-8")
    subprocess.run(
        ["git", "init", "-q", str(fixture)],
        check=False, capture_output=True, timeout=RUN_TIMEOUT_SECONDS,
    )
    return fixture


def clean_env() -> dict[str, str]:
    import os

    env = dict(os.environ)
    for key in list(env):
        if key.startswith(("AM_", "AMQ_")) or key in ("TMUX", "TMUX_PANE"):
            env.pop(key)
    return env


def run_invocation(binary: str, command: str, fixture: Path, env: dict[str, str]) -> tuple[int, str]:
    try:
        tokens = shlex.split(command)
    except ValueError as err:
        return -1, f"unparseable shell text: {err}"
    argv = [binary] + tokens[1:]
    try:
        proc = subprocess.run(
            argv, cwd=fixture, env=env, input="hello\n",
            capture_output=True, text=True, timeout=RUN_TIMEOUT_SECONDS,
        )
    except subprocess.TimeoutExpired:
        return -1, "timed out — a documented invocation must not block"
    detail = (proc.stderr or proc.stdout).strip().splitlines()
    return proc.returncode, detail[0] if detail else ""


def main() -> int:
    if len(sys.argv) != 2:
        print("usage: check-skill-invocations.py <amq-squad-binary>", file=sys.stderr)
        return 2
    binary = str(Path(sys.argv[1]).resolve())
    kind, action = catalog_kind_action(binary)
    invocations = collect()
    if not invocations:
        print("no invocations extracted — extraction is broken, refusing to pass", file=sys.stderr)
        return 1
    env = clean_env()
    failures: list[str] = []
    with tempfile.TemporaryDirectory(prefix="skill-inv-") as tmp:
        root = Path(tmp)
        for index, (path, line, command) in enumerate(invocations):
            fixture = make_fixture(root, index)
            try:
                concrete = substitute(command, fixture, kind, action)
            except KeyError as err:
                failures.append(f"{path.relative_to(REPO)}:{line}: {err}")
                continue
            code, detail = run_invocation(binary, concrete, fixture, env)
            if code == 1 or code == -1:
                failures.append(
                    f"{path.relative_to(REPO)}:{line}: usage failure (exit {code})\n"
                    f"    documented: {command}\n"
                    f"    executed:   {concrete}\n"
                    f"    error:      {detail}"
                )
            shutil.rmtree(fixture, ignore_errors=True)
    if failures:
        print(f"{len(failures)} documented invocation(s) do not run as written:\n", file=sys.stderr)
        for failure in failures:
            print(failure + "\n", file=sys.stderr)
        return 1
    print(f"skill-invocation-check: {len(invocations)} documented invocation(s) execute cleanly")
    return 0


if __name__ == "__main__":
    sys.exit(main())
