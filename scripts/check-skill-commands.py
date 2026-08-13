#!/usr/bin/env python3
"""Fail the build when a skill names a CLI command or flag the binary does not have.

#534: the skills drift from the binary between releases, and the drift is only
found when an operator follows an instruction that no longer works. This converts
that from a recurring release-note problem into a build failure, and it is the
prerequisite for #522's verb-grammar rewrite: with this gate in place that rewrite
becomes a mechanical regeneration instead of a manual audit.

The command surface is read from the binary's own --help output, so there is no
second list of verbs to keep in sync. That is deliberate: a hand-maintained mirror
of the command surface would be the very defect this gate exists to catch.
"""

from __future__ import annotations

import math
import re
import subprocess
import sys
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
SKILLS = REPO / "plugins" / "skills-src"

# A command reference must START its line, or appear inside a Bash(...) permission
# string. Occurrences mid-sentence are prose: "This project uses amq-squad for
# agent team coordination" is English, and the fences in these skills legitimately
# contain file templates as well as shell.
#
# MEDIUM 4: Bash(amq-squad review-worktree remove:*) is a REAL command reference --
# it is what an operator puts in an allowlist -- and the line-start rule alone
# ignored it, so renames could break permission strings silently.
# `rest` stops at the next `amq-squad` token, so an inline span holding TWO
# commands does not attribute the second command's verb and flags to the first.
# M3: collapsing spans unconditionally (needed for wrapped references) made
# `amq-squad team sync --apply` + `amq-squad doctor --json` in one span read as
# `team sync --apply amq-squad doctor --json`, i.e. invented drift.
COMMAND_LINE = re.compile(
    r"(?:^\s*(?:\$\s+|&&\s+|\|\s+)?|\bBash\(\s*)amq-squad\s+(?P<rest>(?:(?!amq-squad)[^\n])*)",
    re.M,
)

# An inline shell comment is not part of the command. Without stripping it,
# `amq-squad doctor    # AMQ version, tmux, wake` extracted `#` as doctor's
# SUBCOMMAND, which then hit the `doctor takes no positional arguments` error and
# (before H1) was reported as a PROVEN path.
def strip_inline_comment(text: str) -> str:
    """Drop a trailing shell comment, ignoring `#` inside quotes.

    Finding 5: a blunt whitespace-hash-to-end pattern deleted the rest of
    `--note "issue #123" --force`, losing --force. Second half of the shell-syntax
    lesson: text that looks like shell must be read with shell rules.
    """
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

# A trailing permission-string suffix: Bash(amq-squad x remove:*) -> drop ":*)".
PERMISSION_SUFFIX = re.compile(r"[:)].*$")

# Continuation joining (HIGH 2). A reference wraps in these files:
#   `amq-squad evidence run TASK --profile PROFILE --session SESSION --me ACTOR
#   --subject TEXT --attempt-id ID -- COMMAND [ARG...]`
# Capturing only to the first newline SILENTLY dropped every flag after the wrap
# (--subject, --attempt-id, and in other references --lead, --launch-shape, --go).
# Silently dropping flags is the same class as dropping whole references: the gate
# reports agreement about text it never read.
# In a FENCE, only a genuine continuation is joined: a trailing backslash, or a
# following line indented under the command. Joining unconditionally would merge
# distinct commands that simply sit on consecutive lines.
FENCE_CONTINUATION = re.compile(r"\\\n\s*|\n[ \t]+(?=--)")

FENCE = re.compile(r"```.*?```", re.S)
# Inline spans WRAP ACROSS LINES in these files (cli/SKILL.md names
# `amq-squad evidence run TASK --profile ...` broken over two lines), so the span
# body must tolerate newlines. A newline-hostile pattern misses it SILENTLY, which
# is the dangerous kind of miss.
INLINE = re.compile(r"`([^`]+)`", re.S)

# Inside an inline span every `amq-squad` occurrence starts a reference, because the
# span is code. Fences keep the stricter line-start rule (they also carry templates).
SPAN_COMMAND = re.compile(r"\bamq-squad\s+(?P<rest>(?:(?!amq-squad)[^\n])*)")

VERB = re.compile(r"^[a-z][a-z0-9-]*$")
# A subcommand token may be malformed; we still check it rather than drop it.
SUBCOMMAND_TOKEN = re.compile(r"^[A-Za-z][A-Za-z0-9-]*$")
# Sentinel key for a flag-only reference such as `amq-squad --version`.
GLOBAL_FLAG_KEY = "<global-flags>"
# Sentinel for flag-shaped tokens that are not valid flag syntax (e.g. --apply_bad).
# They are REPORTED, never dropped.
MALFORMED_FLAG_KEY = "<malformed-flags>"
# A flag token may be MALFORMED and must still be extracted so it can be reported.
# A lowercase-only pattern silently dropped `--launch-shapeX`, which is the
# vanishing-reference class a third time: fixed in the verb position, found by
# review in the subcommand position, and still live in the flag position until a
# test caught it. Extract anything flag-shaped; let verification judge it.
FLAG = re.compile(r"^--[A-Za-z][A-Za-z0-9-]*$")

# Strings that are formatted as commands but are not commands.
#
# This list must stay EMPTY-able: every entry is a concession, and
# test_check_skill_commands.py asserts each one still actually appears in the
# skills, so a concession cannot outlive its cause silently.
# Exemptions for backticked tokens that look like commands but are not. Empty is the
# correct steady state: an entry here is a claim that the surface contains a false
# positive, and NotACommandListIsNotStale fails if the claimed text is no longer
# present, so a stale exemption cannot outlive what it exempted.
#
# The "skill" entry lived here for exactly that reason and was deleted with the
# version-announcement preamble it covered (#534).
NOT_A_COMMAND: dict[str, str] = {}

# Anti-vacuity floors. A gate that silently finds nothing is worse than no gate:
# it reports success while checking zero things, which is exactly the state the
# skills are in before they name commands.
MIN_COMMANDS = 8
MIN_VERBS_IN_SURFACE = 20
# Below this many claimed flags the zero-verified floor would be noise; above it,
# verifying none of them means flag parsing has broken.
MIN_FLAGS_CLAIMED_FOR_FLOOR = 4
# Committed coverage baseline. At the time of writing the gate verifies 15 of 23
# claimed flags; these floors make a regression from that a BUILD failure rather
# than a quiet shrink. Raise them when coverage genuinely improves.
# Floors sit AT committed coverage with one flag of headroom. Set far below it (4
# and 5 against real coverage of 13) they permitted a 13 -> 5 collapse while exiting
# 0, which is the silent shrink the floor exists to prevent.
MIN_VERIFIED_FLAGS = 12
# The VERIFIABLE set must not silently shrink either, or the ratio is vacuous.
MIN_VERIFIABLE_FLAGS = 12
MIN_VERIFIED_FLAG_RATIO = 0.5


def required_verified(verifiable: int) -> int:
    """Minimum verified flags for a given verifiable count.

    Production calculation, exposed so a test exercises THIS rather than Python's
    math library: asserting math.ceil(23 * 0.5) == 12 stays green when production
    truncates with int(), which made that test vacuous.
    """
    return max(MIN_VERIFIED_FLAGS, math.ceil(verifiable * MIN_VERIFIED_FLAG_RATIO))


def run_help(binary: str, *args: str) -> str:
    proc = subprocess.run([binary, *args, "--help"], capture_output=True, text=True)
    return proc.stdout + proc.stderr


def verb_surface(binary: str) -> set[str]:
    """Top-level verbs, parsed from the binary's own two-column help."""
    text = run_help(binary)
    return {m for m in re.findall(r"^\s{2,}([a-z][a-z0-9-]{1,30})\s{2,}\S", text, re.M)}


# Help text that DELEGATES its flags rather than listing them, e.g.
#   amq-squad new profile NAME ... [team init options]
# A command like that accepts flags its own help never enumerates, so its flag set
# is not observable from --help and must not be treated as exhaustive.
DELEGATES_FLAGS = re.compile(r"\[[^\]]*\b(?:flags|options)\]")

# Section headers that introduce a FLAG LIST. Anything else (Examples:, Exit codes:,
# Commands:, prose) is not a flag surface.
FLAG_SECTION_HEADER = re.compile(r"\b(?:flags|options)\b", re.I)


# Tri-state subcommand observation. Returning "exists" for every response except
# the recognized negative was FAIL-OPEN IN A VERIFIER: an I/O error, a config
# failure, or `doctor takes no positional arguments` all PROVED the path, so
# `amq-squad doctor totallybogus` verified clean.
#
# This is the probe-posture rule for the third time in one milestone (the #538 git
# probe, the earlier version of this prober, and now here): a readiness/verification
# probe must fail CLOSED, and "closed" for an unreadable observation means failing
# the BUILD loudly, not failing the reference silently — a silent reference failure
# would be an invented drift report, which is its own bad outcome.
SUB_PROBE = "__amq_squad_probe_invalid__"

# The binary lists its complete valid-subcommand surface under one contract:
#   error: unknown 'team' subcommand: "X". Try 'init', 'resume', or 'rm'.
# Commands without subcommands say so explicitly instead:
#   error: doctor takes no positional arguments; got 2
#
# Asking once per verb yields an AUTHORITATIVE set, so membership is a pure set
# check rather than a per-path guess. That replaces an earlier prober that returned
# "exists" for every response except the recognized negative -- fail-open in a
# verifier, which made `amq-squad doctor totallybogus` verify clean.
# The parser intentionally accepts ONLY that documented contract (#561). A producer
# rewording therefore becomes an unobservable surface and fails the build loudly,
# rather than silently widening a tolerant union regex again. It is anchored to the
# whole error line and bound to the probed verb.
#
# The old generalization was unanchored, which opened a false-observation door. Prose
# such as
#     "Note: an unknown fake subcommand is recoverable. Use help for examples"
# yielded subcommands {help, for, examples}, and a flag list yielded {json, verbose}.
#
# Three defences, because one is not enough:
#   1. anchor at a line-leading `error:` so prose in a description cannot match;
#   2. capture the verb the error names and require it to EQUAL the verb we probed,
#      so an error about something else cannot populate this verb's surface;
#   3. require every list item to use the canonical quoted subcommand grammar, so
#      flags and arbitrary prose cannot become commands.
UNKNOWN_SUBCOMMAND_LIST = re.compile(
    r"^[ \t]*error:\s*unknown\s+'(?P<verb>[A-Za-z][A-Za-z0-9-]*(?: [A-Za-z][A-Za-z0-9-]*)*)'\s+subcommand:\s+"
    r'"(?:\\.|[^"\\])*"\.\s+Try\s+'
    r"(?P<list>'[A-Za-z][A-Za-z0-9-]*'(?:, '[A-Za-z][A-Za-z0-9-]*')*(?:, or '[A-Za-z][A-Za-z0-9-]*'| or '[A-Za-z][A-Za-z0-9-]*')?)\.\s*$",
    re.M,
)

NO_POSITIONALS = re.compile(r"takes no positional arguments", re.I)

_SUB_SURFACE_CACHE: dict[tuple[str, str], tuple[set[str], bool]] = {}


def subcommand_surface(binary: str, verb: str) -> tuple[set[str], bool]:
    """Return (valid subcommands, observable).

    observable=False means the binary's answer could not be interpreted. The caller
    must fail the BUILD in that case: failing the reference would invent drift, and
    passing it would be fail-open in a verifier. This is the probe-posture rule
    (#538's git probe, and this prober twice) applied to a third surface.
    """
    key = (binary, verb)
    if key in _SUB_SURFACE_CACHE:
        return _SUB_SURFACE_CACHE[key]
    result: tuple[set[str], bool]
    # PRIMARY source: the verb's own help usage block, which lists
    # `amq-squad <verb> <sub>` lines uniformly across verbs. This is preferred over
    # error-text parsing because help may be incomplete. A verb with an implicit
    # default subcommand (review-worktree) also needs its canonical probe response;
    # otherwise its argument complaint enumerates nothing.
    own = run_help(binary, verb)
    # [ \t]+ rather than \s+: \s matches NEWLINES, so a usage line ending at the
    # verb ran into the next line and captured "amq-squad" as a subcommand of
    # `doctor`. Same newline-crossing class as the wrapped-reference bug; a wrong
    # authoritative set is worse than none, because it is trusted.
    usage = set(
        re.findall(rf"^[ \t]+amq-squad[ \t]+{re.escape(verb)}[ \t]+([a-z][a-z0-9-]*)", own, re.M)
    )
    # Drop tokens that are placeholders rather than subcommands.
    usage -= {"help"}
    # SECOND source: ask with a bogus subcommand and parse the canonical complete
    # list. The usage block may still omit hidden or compatibility aliases, so retain
    # the UNION rather than coupling correctness to help layout.
    text = run_help(binary, verb, SUB_PROBE)
    match = UNKNOWN_SUBCOMMAND_LIST.search(text)
    listed: set[str] = set()
    if match and match.group("verb").lower() == verb.lower():
        listed = set(re.findall(r"'([A-Za-z][A-Za-z0-9-]*)'", match.group("list")))

    combined = usage | listed
    if combined:
        result = (combined, True)
    elif NO_POSITIONALS.search(text):
        # The verb genuinely has no subcommands, so any claimed one is drift.
        result = (set(), True)
    else:
        result = (set(), False)
    _SUB_SURFACE_CACHE[key] = result
    return result


def flag_surface(binary: str, verb: str, sub: str | None) -> tuple[set[str], bool]:
    """Return (flags, exhaustive).

    Flags come from STRUCTURE only:
      (a) the USAGE BLOCK, which wraps across indented continuation lines that do not
          repeat `amq-squad` (notify and run start both wrap), so it must be read as a
          block rather than per-line on a leading token;
      (b) Go's DEFAULT flag printer, which lists `  -name type` with a SINGLE dash.
          Go accepts -name and --name identically, so they are one flag.

    Never from description prose or examples. Scraping every `--token` from the body
    pulled "alias for --profile" out of a DESCRIPTION and `--dry-run` out of an
    EXAMPLE, then treated both as the surface.

    `exhaustive` is True only for Go's printer, which is a complete definition list.
    A hand-written usage block is ILLUSTRATIVE: `run start` shows `-p PROJECT
    -s SESSION` and never lists the long forms, yet --project and --session parse
    fine. So a source may CONFIRM a flag; only an exhaustive one may REFUTE it.
    Turning "not shown" into "not accepted" false-failed correct documentation, which
    is the same over-claiming shape as trusting one incomplete subcommand source.
    """
    args = [] if not verb else ([verb] if sub is None else [verb, sub])
    text = run_help(binary, *args)
    if verb and text.lstrip().startswith("error:"):
        return set(), False
    lines = text.splitlines()

    usage_flags: set[str] = set()
    in_usage = False
    for line in lines:
        if line.startswith("Usage:"):
            in_usage = True
            usage_flags.update(re.findall(r"--([A-Za-z][A-Za-z0-9-]*)", line))
            continue
        if in_usage:
            if not line.strip() or not line.startswith((" ", "\t")):
                in_usage = False
                continue
            usage_flags.update(re.findall(r"--([A-Za-z][A-Za-z0-9-]*)", line))

    # Finding 1: scanning the WHOLE body let any indented dash-leading line become a
    # "definition", so an indented PROSE line mentioning a flag entered the surface.
    # Collect definitions only inside recognized FLAG SECTIONS: everything after Go's
    # `Usage of X:` header (that whole block is the definition list), or a hand-written
    # section whose header names flags/options. `Examples:` and prose sections are
    # excluded by construction rather than by pattern luck.
    definitions: set[str] = set()
    in_flag_section = False
    for line in lines:
        if line.startswith("Usage of "):
            in_flag_section = True
            continue
        # A column-0 line ending in ':' starts a new section.
        if line and not line[0].isspace() and line.rstrip().endswith(":"):
            in_flag_section = bool(FLAG_SECTION_HEADER.search(line))
            continue
        if not in_flag_section:
            continue
        m = re.match(r"^[ \t]{2,}--?([A-Za-z][A-Za-z0-9-]*)(?:[ \t=,]|$)", line)
        if m:
            definitions.add(m.group(1))

    exhaustive = any(line.startswith("Usage of ") for line in lines)
    flags = {"--" + f for f in usage_flags | definitions}
    # Delegation only matters when structure yielded NOTHING. Checking it first made
    # the top-level surface empty, because `amq-squad <command> [options]` reads as a
    # delegation even though the global flags are declared right there.
    if not flags and DELEGATES_FLAGS.search(text):
        return set(), False
    return flags, exhaustive


def code_blobs_typed(text: str) -> list[tuple[str, str]]:
    """Return (blob, kind) where kind is "fence" or "span".

    Finding 2: after an inline span is collapsed to one line (needed for wrapped
    references), a SECOND command in that span is no longer at a line start, so a
    line-start-only matcher silently dropped it. A span IS code, so any `amq-squad`
    occurrence inside one starts a reference; a FENCE keeps the line-start rule,
    because fences also hold file templates and prose.
    """
    out = [(FENCE_CONTINUATION.sub(" ", m.group(0)), "fence") for m in FENCE.finditer(text)]
    out.extend(
        (re.sub(r"\s*\n\s*", " ", m.group(1)), "span")
        for m in INLINE.finditer(FENCE.sub("", text))
    )
    return out


def code_blobs(text: str) -> list[str]:
    """Return code blobs already normalized for whole-reference extraction.

    HIGH 2: an inline span is ONE logical command even when the markdown wraps it
    mid-flag-list, and the wrap in cli/SKILL.md has NO leading whitespace on the
    continuation line:

        `amq-squad evidence run TASK --profile P --session S --me ACTOR
        --subject TEXT --attempt-id ID -- COMMAND [ARG...]`

    so a continuation rule requiring indentation missed it and every flag after the
    wrap was silently dropped. Inline spans therefore collapse ALL newlines; fences
    join only genuine continuations, because consecutive lines in a fence are
    usually separate commands.
    """
    blobs = [FENCE_CONTINUATION.sub(" ", m.group(0)) for m in FENCE.finditer(text)]
    blobs.extend(
        re.sub(r"\s*\n\s*", " ", m.group(1)) for m in INLINE.finditer(FENCE.sub("", text))
    )
    return blobs


def extract(paths: list[Path]) -> dict[tuple[str, str | None], set[str]]:
    """Map (verb, subcommand-or-None) -> set of flags named with it, per skills."""
    found: dict[tuple[str, str | None], set[str]] = {}
    for path in paths:
        for blob, kind in code_blobs_typed(path.read_text()):
            # A collapsed inline span is one line, so a SECOND command in it is no
            # longer at a line start: spans match every `amq-squad` occurrence, while
            # fences keep the line-start rule because they also carry file templates.
            pattern = SPAN_COMMAND if kind == "span" else COMMAND_LINE
            for match in pattern.finditer(blob):
                rest = strip_inline_comment(match.group("rest"))
                rest = PERMISSION_SUFFIX.sub("", rest)
                tokens = rest.split()
                if not tokens:
                    continue
                verb = tokens[0]
                # MEDIUM 3: a leading-dash token must not be discarded wholesale.
                # `amq-squad --version` is a legitimate flag-only reference; a
                # malformed verb like `-doctor`, or a global flag that does not
                # exist, is a CLAIM that must be checked. Blanket-skipping anything
                # starting with "-" was the vanishing-reference class surviving in
                # the flag branch after being fixed in the verb branch.
                if verb.startswith("-"):
                    if len(tokens) == 1 and verb.startswith("--"):
                        found.setdefault((GLOBAL_FLAG_KEY, None), set()).add(verb)
                    else:
                        found.setdefault((verb, None), set())
                    continue
                # M4: a token after the verb must never VANISH. `-sync` (single
                # dash) used to be skipped as "flag-ish" while failing the flag
                # shape too, so it disappeared entirely; the same held for
                # malformed flags like `--apply_bad`. Both are claims and both must
                # reach verification. This is the no-silent-discard guarantee in the
                # two spots it was still unmet.
                sub = None
                if len(tokens) > 1:
                    nxt = tokens[1]
                    if not nxt.startswith("--"):
                        # Includes a single-dash token: report it rather than drop it.
                        sub = nxt
                if sub is not None and not SUBCOMMAND_TOKEN.match(sub):
                    # A malformed subcommand (e.g. `syncX`) must be REPORTED, not
                    # silently folded into the bare verb: folding both loses the
                    # rename and, when the same verb also appears with a real
                    # subcommand, produced a None/str sort crash.
                    sub = sub.lower() if sub.isalpha() else sub
                # `--` ends amq-squad's own flags: everything after it belongs to the
                # wrapped command (`evidence run ... -- make ci`), so collecting it
                # would attribute make's flags to amq-squad. `--` itself is a
                # separator, not a flag claim.
                own_tokens = tokens[1:]
                if "--" in own_tokens:
                    own_tokens = own_tokens[: own_tokens.index("--")]
                # Finding 3: retaining only double-dash tokens made `-apply` VANISH.
                # Anything dash-prefixed is a claim and must reach verification.
                flags = {t for t in own_tokens if t.startswith("-") and t != "-"}
                malformed = {t for t in flags if not FLAG.match(t)}
                if malformed:
                    found.setdefault((MALFORMED_FLAG_KEY, None), set()).update(malformed)
                    flags -= malformed
                found.setdefault((verb, sub), set()).update(flags)
    return found


def main() -> int:
    binary = sys.argv[1] if len(sys.argv) > 1 else str(REPO / "amq-squad")
    # Optional second argument: the skills root. Defaults to this repo's sources.
    # It exists so the no-skills-found branch is testable without faking the repo
    # layout, and so the gate can be pointed at another checkout.
    skills = Path(sys.argv[2]) if len(sys.argv) > 2 else SKILLS
    if not Path(binary).exists():
        print(f"error: binary not found at {binary}; run 'make build' first", file=sys.stderr)
        return 2

    surface = verb_surface(binary)
    if len(surface) < MIN_VERBS_IN_SURFACE:
        print(
            f"error: only {len(surface)} verbs parsed from '{binary} --help' "
            f"(expected >= {MIN_VERBS_IN_SURFACE}). The help format probably changed; "
            "this gate would otherwise pass by checking against an empty surface.",
            file=sys.stderr,
        )
        return 2

    paths = sorted(skills.rglob("*.md"))
    if not paths:
        print(f"error: no skill sources found under {skills}", file=sys.stderr)
        return 2

    found = extract(paths)
    checked = {k: v for k, v in found.items() if k[0] not in NOT_A_COMMAND}
    if len(checked) < MIN_COMMANDS:
        print(
            f"error: extracted only {len(checked)} command references from the skills "
            f"(expected >= {MIN_COMMANDS}). Either the skills stopped naming commands or the "
            "extractor broke; both must fail rather than silently check nothing.",
            file=sys.stderr,
        )
        return 2

    failures: list[str] = []
    unverifiable: list[str] = []
    unobservable_paths: list[str] = []
    flags_checked = 0
    flags_claimed = 0
    flags_verifiable = 0
    unverifiable_flags: list[str] = []
    verified_real_verb = False

    # M2: flag_surface(binary, "", None) executed `amq-squad "" --help`, returned an
    # empty set, and the empty set was then treated as "all flags verified", so
    # `amq-squad --definitely-not-global` stayed green. Parse the real top-level
    # help instead, and treat an unparseable top-level help as a BUILD failure
    # rather than as permission to skip the check.
    # Finding 1: this was wrong in BOTH directions. Scraping every flag-shaped token
    # accepted `--dry-run` from an EXAMPLE, while `--version` -- valid, but absent
    # from the help text -- was rejected. Use the same structural parse and the same
    # completeness rule as every other surface: confirm from structure, refute only
    # when the surface is exhaustive.
    global_flags, global_exhaustive = flag_surface(binary, "", None)
    if not global_flags:
        print(
            "error: no global flags parsed from 'amq-squad --help'; cannot verify "
            "flag-only references. Refusing to report success while checking nothing.",
            file=sys.stderr,
        )
        return 2

    # Sort with a None-safe key. Sorting raw (verb, sub) tuples crashed with
    # TypeError whenever one verb appeared both bare and with a subcommand, because
    # None is not comparable to str. That crash surfaced while probing HIGH 1 and is
    # its own defect: a gate that dies on a legitimate corpus shape is unusable.
    for (verb, sub), flags in sorted(checked.items(), key=lambda kv: (kv[0][0], kv[0][1] or "")):
        flags_claimed += len(flags)

        if verb == MALFORMED_FLAG_KEY:
            for flag in sorted(flags):
                failures.append(f"'{flag}' is named in the skills but is not valid flag syntax")
            continue

        # A flag-only reference (`amq-squad --version`): confirm from structure, and
        # refute only if the top-level surface is exhaustive. `--version` is valid but
        # absent from the help text, so refuting on absence would reject it.
        if verb == GLOBAL_FLAG_KEY:
            for flag in sorted(flags):
                if flag not in global_flags and global_exhaustive:
                    failures.append(f"global flag '{flag}' is named in the skills but 'amq-squad --help' does not list it")
                elif flag not in global_flags:
                    unverifiable.append(f"amq-squad {flag} (global)")
                else:
                    flags_checked += 1
            continue

        if verb.startswith("-"):
            failures.append(f"'amq-squad {verb}' is named in the skills but is not a command or a global flag")
            continue

        if verb not in surface:
            failures.append(f"verb 'amq-squad {verb}' is named in the skills but is not a command")
            continue
        verified_real_verb = True

        # HIGH 1: verify the FULL command path, not just the first token.
        if sub is not None:
            subs, observable = subcommand_surface(binary, verb)
            if not observable:
                unobservable_paths.append(f"amq-squad {verb} {sub} (could not read {verb}'s subcommand list)")
                continue
            if sub not in subs:
                failures.append(
                    f"subcommand 'amq-squad {verb} {sub}' is named in the skills but {verb} has no such "
                    f"subcommand (valid: {', '.join(sorted(subs)) or 'none'})"
                )
                continue

        target = f"{verb} {sub}" if sub else verb
        known, exhaustive = flag_surface(binary, verb, sub)
        for flag in sorted(flags):
            if flag in known:
                # Present on the surface: CONFIRMED, whichever kind of surface it is.
                flags_verifiable += 1
                flags_checked += 1
            elif exhaustive:
                # Absent from a COMPLETE definition list: genuine drift.
                flags_verifiable += 1
                failures.append(
                    f"flag '{flag}' is named for 'amq-squad {target}' but that command does not accept it"
                )
            else:
                # Absent from an ILLUSTRATIVE surface proves nothing. `run start`
                # documents `-p/-s` and never lists --project/--session, which are
                # accepted. Report it as unverifiable; never fail the reference.
                unverifiable.append(f"amq-squad {target} {flag}")

    if not verified_real_verb:
        print("error: no real verb was verified; the gate checked nothing", file=sys.stderr)
        return 2

    if unobservable_paths:
        # H1: an unreadable observation fails the BUILD, not the reference. Failing
        # the reference would invent drift; passing it would be fail-open in a
        # verifier. Neither is acceptable, so the gate refuses to render a verdict.
        print(
            f"error: could not observe {len(unobservable_paths)} command path(s); the gate cannot "
            "verify them and will not report success:",
            file=sys.stderr,
        )
        for path in sorted(unobservable_paths):
            print(f"  - {path}", file=sys.stderr)
        return 2

    # MEDIUM 5: floor the FLAG coverage too. Verbs were floored; flags were not, so
    # a help-format change that stopped yielding parsed flags would make everything
    # "unverifiable" and keep the build green -- the same silent shrink, one level
    # down.
    # M5: an exactly-zero floor was bypassed by a mass drop (23 claimed, 3 verified)
    # or by a single verified flag. Floor on the RATIO and on an absolute baseline so
    # coverage cannot silently shrink.
    # Guard BOTH numbers: a shrinking verifiable set would otherwise make the ratio
    # vacuously satisfiable.
    if flags_claimed >= MIN_FLAGS_CLAIMED_FOR_FLOOR and flags_verifiable < MIN_VERIFIABLE_FLAGS:
        print(
            f"error: only {flags_verifiable} of {flags_claimed} named flag(s) are verifiable at all "
            f"(need >= {MIN_VERIFIABLE_FLAGS}). The flag surfaces have probably stopped parsing; a "
            "shrinking verifiable set makes the coverage ratio meaningless.",
            file=sys.stderr,
        )
        return 2
    if flags_verifiable >= MIN_FLAGS_CLAIMED_FOR_FLOOR:
        # No int() truncation: 11/23 is 47.8% and used to pass a "50%" floor.
        # The floor measures against VERIFIABLE claims, because a flag on a
        # non-exhaustive surface can only ever be confirmed, never refuted, so
        # counting it as a miss would fail every build under the honest model.
        required = required_verified(flags_verifiable)
        if flags_checked < required:
            print(
                f"error: only {flags_checked} of {flags_claimed} named flag(s) were verified "
                f"(need >= {required}: at least {MIN_VERIFIED_FLAGS} and {int(MIN_VERIFIED_FLAG_RATIO * 100)}% "
                "of those claimed). Flag verification has probably regressed; this gate would "
                "otherwise pass while checking far less than it did before.",
                file=sys.stderr,
            )
            return 2

    if failures:
        print(f"skill/command drift ({len(failures)}):", file=sys.stderr)
        for f in failures:
            print(f"  - {f}", file=sys.stderr)
        print("\nFix the skill text, or the command, so they agree.", file=sys.stderr)
        return 1

    print(f"skills name {len(checked)} command(s); all verb/subcommand paths resolve, {flags_checked} of {flags_claimed} flag(s) verified")
    if unverifiable:
        print(f"flags NOT verifiable from --help for {len(unverifiable)} command(s) "
              "(help errors without required positionals, or delegates its flag set):")
        for u in unverifiable:
            print(f"  - {u}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
