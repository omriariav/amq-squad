#!/usr/bin/env python3
"""Tests for the #534 skill/command drift gate.

The gate's whole value is that it FAILS on drift, so these tests mutate a skill
and assert the exit code changes. A drift gate is worthless if never proven to
detect drift, and one specific bug found while writing these tests proves the
point: a rename containing an uppercase letter used to make the reference VANISH
from extraction (count silently dropped, exit 0) rather than fail.
"""

from __future__ import annotations

import importlib.util
import re
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
GATE = REPO / "scripts" / "check-skill-commands.py"
BINARY = REPO / "amq-squad"
SKILLS = REPO / "plugins" / "skills-src"


def load_gate():
    spec = importlib.util.spec_from_file_location("gate", GATE)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def run_gate() -> subprocess.CompletedProcess:
    return subprocess.run(
        [sys.executable, str(GATE), str(BINARY)], capture_output=True, text=True
    )


class MutatedSkill:
    """Rewrite one skill file for the duration of a test, then restore it byte-for-byte."""

    def __init__(self, path: Path, old: str, new: str):
        self.path, self.old, self.new = path, old, new

    def __enter__(self):
        self.original = self.path.read_bytes()
        text = self.original.decode()
        if self.old not in text:
            raise AssertionError(
                f"fixture is wrong: {self.path.name} does not contain {self.old!r}. "
                "A mutation that misses its target proves nothing."
            )
        self.path.write_text(text.replace(self.old, self.new))
        return self

    def __exit__(self, *exc):
        self.path.write_bytes(self.original)
        return False


class GatePassesOnCleanTree(unittest.TestCase):
    def test_clean_tree_passes(self):
        if not BINARY.exists():
            self.skipTest("binary not built; run make build")
        result = run_gate()
        self.assertEqual(result.returncode, 0, f"gate failed on a clean tree:\n{result.stderr}")

    def test_reports_what_it_verified(self):
        if not BINARY.exists():
            self.skipTest("binary not built; run make build")
        result = run_gate()
        # Coverage must be stated, not implied: a gate that verifies less than it
        # claims is the failure mode this file exists to prevent.
        self.assertIn("command(s)", result.stdout)
        self.assertIn("flag(s) verified", result.stdout)


class GateDetectsDrift(unittest.TestCase):
    def setUp(self):
        if not BINARY.exists():
            self.skipTest("binary not built; run make build")

    def test_renamed_verb_fails(self):
        target = SKILLS / "amq-squad" / "references" / "team-rules-template.md"
        with MutatedSkill(target, "amq-squad doctor", "amq-squad doctorq"):
            result = run_gate()
        self.assertEqual(result.returncode, 1, "a renamed verb must fail the gate")
        self.assertIn("doctorq", result.stderr)

    def test_renamed_verb_with_uppercase_fails_rather_than_vanishing(self):
        """The bug this gate nearly shipped with.

        Tokens failing a lowercase-only verb pattern were skipped, so `evidenceX`
        was DISCARDED instead of flagged: the extracted count dropped and the gate
        exited 0. A drift gate that loses references it cannot parse is worse than
        no gate, because the loss is indistinguishable from agreement.
        """
        target = SKILLS / "cli" / "SKILL.md"
        with MutatedSkill(target, "amq-squad evidence run", "amq-squad evidenceX run"):
            result = run_gate()
        self.assertEqual(result.returncode, 1, "an unparseable verb must fail, not vanish")
        self.assertIn("evidenceX", result.stderr)

    def test_unknown_flag_fails_on_an_exhaustive_surface(self):
        """Refutation is only legitimate where the surface is COMPLETE.

        `activity set` uses Go's default flag printer, which enumerates every flag,
        so absence there is genuine drift.
        """
        with tempfile.TemporaryDirectory() as tmp:
            for src in sorted(SKILLS.rglob("*.md")):
                (Path(tmp) / src.name).write_text(src.read_text())
            (Path(tmp) / "zz_exhaustive.md").write_text(
                "```\namq-squad activity set --me H --task T --phase coding --nope-not-a-flag\n```\n"
            )
            result = subprocess.run(
                [sys.executable, str(GATE), str(BINARY), tmp], capture_output=True, text=True
            )
        self.assertEqual(result.returncode, 1, f"drift on an exhaustive surface must fail:\n{result.stderr}")
        self.assertIn("--nope-not-a-flag", result.stderr)

    def test_real_flags_on_an_exhaustive_surface_verify(self):
        """The counter-case: Go's single-dash listing must be READ, or every real
        flag on such a command would be reported as drift. This is finding #10."""
        gate = load_gate()
        known, exhaustive = gate.flag_surface(str(BINARY), "activity", "set")
        self.assertTrue(exhaustive, "activity set's help is a complete definition list")
        for real in ("--me", "--task", "--phase"):
            self.assertIn(real, known, f"{real} is accepted and must be readable from help")

    def test_absent_flag_on_an_illustrative_surface_does_not_fail(self):
        """Confirm-only. `run start` documents `-p/-s` and never lists the long
        forms, yet --project and --session are accepted, so absence from an
        illustrative usage block proves nothing and must not fail the reference."""
        gate = load_gate()
        known, exhaustive = gate.flag_surface(str(BINARY), "run", "start")
        self.assertFalse(exhaustive, "run start's usage block is illustrative, not exhaustive")
        with tempfile.TemporaryDirectory() as tmp:
            for src in sorted(SKILLS.rglob("*.md")):
                (Path(tmp) / src.name).write_text(src.read_text())
            (Path(tmp) / "zz_illustrative.md").write_text(
                "```\namq-squad run start --project P --session S --unlisted-but-maybe-real\n```\n"
            )
            result = subprocess.run(
                [sys.executable, str(GATE), str(BINARY), tmp], capture_output=True, text=True
            )
        self.assertEqual(result.returncode, 0, f"must not fail on an illustrative surface:\n{result.stderr}")
        self.assertIn("NOT verifiable", result.stdout)


class ExtractionRules(unittest.TestCase):
    def setUp(self):
        self.gate = load_gate()

    def test_prose_mid_sentence_is_not_a_command(self):
        """"This project uses amq-squad for agent team coordination" is English.

        It appears inside a FENCE, because these skills legitimately fence file
        templates as well as shell, so fencing alone cannot distinguish the two.
        The line-start rule does, with no allowlist.
        """
        found = self.gate.extract([SKILLS / "wizard" / "references" / "pointer-stub-template.md"])
        self.assertNotIn("for", {verb for verb, _ in found})

    def test_inline_span_wrapping_across_lines_is_extracted(self):
        """cli/SKILL.md names `amq-squad evidence run TASK --profile ...` across a line break.

        A newline-hostile inline pattern misses it SILENTLY, which is the dangerous
        kind of miss: the reference is simply never checked.
        """
        found = self.gate.extract([SKILLS / "cli" / "SKILL.md"])
        self.assertIn(("evidence", "run"), found, "wrapped inline span was not extracted")
        self.assertIn("--profile", found[("evidence", "run")])

    def test_leading_flag_is_not_treated_as_a_verb(self):
        """`amq-squad --version` names no verb.

        It is recorded under the global-flag key rather than discarded, so the flag
        itself can be verified against `amq-squad --help` (MEDIUM 3): discarding it
        was how a documented-but-nonexistent global flag would have gone unchecked.
        """
        with tempfile.TemporaryDirectory() as tmp:
            probe = Path(tmp) / "probe.md"
            probe.write_text("```\namq-squad --version\n```\n")
            found = self.gate.extract([probe])
        self.assertNotIn(("--version", None), found, "a flag must not occupy the verb position")
        self.assertEqual(set(found), {(self.gate.GLOBAL_FLAG_KEY, None)})


class AntiVacuity(unittest.TestCase):
    """The gate must fail loudly rather than pass while checking nothing."""

    def setUp(self):
        self.gate = load_gate()

    def test_help_parser_extracts_a_sane_verb_count(self):
        if not BINARY.exists():
            self.skipTest("binary not built; run make build")
        surface = self.gate.verb_surface(str(BINARY))
        self.assertGreaterEqual(
            len(surface),
            self.gate.MIN_VERBS_IN_SURFACE,
            "the --help parser found too few verbs; a help-format change must fail "
            "loudly rather than empty the surface silently",
        )
        # Spot-check real verbs so a parser that returns plausible garbage fails.
        for verb in ("doctor", "status", "run", "team"):
            self.assertIn(verb, surface)

    def test_floors_are_enforced_not_decorative(self):
        self.assertGreater(self.gate.MIN_COMMANDS, 0)
        self.assertGreater(self.gate.MIN_VERBS_IN_SURFACE, 0)

    def test_gate_fails_when_no_skills_are_found(self):
        """An empty skills tree must be an ERROR, not a pass.

        Asserting a permissive set of exit codes here would be the same vacuity the
        floors exist to prevent, so this pins exit 2 and the message exactly.
        """
        if not BINARY.exists():
            self.skipTest("binary not built; run make build")
        with tempfile.TemporaryDirectory() as empty:
            result = subprocess.run(
                [sys.executable, str(GATE), str(BINARY), empty],
                capture_output=True,
                text=True,
            )
        self.assertEqual(result.returncode, 2, f"empty skills tree must fail:\n{result.stdout}{result.stderr}")
        self.assertIn("no skill sources found", result.stderr)

    def test_gate_fails_when_skills_name_too_few_commands(self):
        """A tree that names almost nothing must trip the floor rather than pass."""
        if not BINARY.exists():
            self.skipTest("binary not built; run make build")
        with tempfile.TemporaryDirectory() as thin:
            (Path(thin) / "thin.md").write_text("```\namq-squad doctor\n```\n")
            result = subprocess.run(
                [sys.executable, str(GATE), str(BINARY), thin],
                capture_output=True,
                text=True,
            )
        self.assertEqual(result.returncode, 2, "a near-empty corpus must trip the anti-vacuity floor")
        self.assertIn("expected >=", result.stderr)


class NotACommandListIsNotStale(unittest.TestCase):
    """Every concession must still have a cause.

    #534's skill rewrite deletes the version-announcement preamble, which is the
    only reason this list is non-empty. When that lands, the entry must be removed,
    and this test is what forces it rather than leaving a permanent exemption
    nobody revisits.
    """

    def test_every_entry_still_appears_in_the_skills(self):
        gate = load_gate()
        corpus = "\n".join(p.read_text() for p in SKILLS.rglob("*.md"))
        for entry, reason in gate.NOT_A_COMMAND.items():
            self.assertIn(
                f"amq-squad {entry}",
                corpus,
                f"'{entry}' is exempted ({reason}) but no longer appears in the skills; "
                "remove the stale entry so the gate stops carrying a concession with no cause",
            )

    def test_entries_carry_a_documented_reason(self):
        gate = load_gate()
        for entry, reason in gate.NOT_A_COMMAND.items():
            self.assertTrue(reason.strip(), f"exemption '{entry}' has no documented reason")



class SecondReviewFindings(unittest.TestCase):
    """The five drift classes the second review found, each pinned.

    Every one was a hole the gate would have missed forever: it reported agreement
    about text it either never read or never checked.
    """

    def setUp(self):
        if not BINARY.exists():
            self.skipTest("binary not built; run make build")
        self.gate = load_gate()

    # HIGH 1 -----------------------------------------------------------------
    def test_unknown_subcommand_fails(self):
        """Only the top-level verb used to be verified, so `team syncX` passed
        because `team` resolves. Subcommands are where most renames happen."""
        target = SKILLS / "wizard" / "references" / "pointer-stub-template.md"
        for bogus in ("synchronise", "syncX"):
            with self.subTest(sub=bogus):
                with MutatedSkill(target, "amq-squad team sync --apply", f"amq-squad team {bogus} --apply"):
                    result = run_gate()
                self.assertEqual(result.returncode, 1, f"'team {bogus}' must fail the gate")
                self.assertIn(bogus, result.stderr)
                self.assertIn("no such subcommand", result.stderr)

    def test_real_subcommand_still_passes(self):
        """The fix must not reject valid subcommands."""
        self.assertEqual(run_gate().returncode, 0)

    # HIGH 2 -----------------------------------------------------------------
    def test_flags_after_a_line_wrap_are_extracted(self):
        """`rest` stopped at the first newline, so every flag after a wrap was
        silently dropped. The wrap in cli/SKILL.md has NO leading whitespace on the
        continuation line, so an indentation-based rule missed it too."""
        found = self.gate.extract([SKILLS / "cli" / "SKILL.md"])
        flags = found[("evidence", "run")]
        self.assertIn("--subject", flags, "a flag AFTER the wrap must be extracted")
        self.assertIn("--attempt-id", flags, "a flag further after the wrap must be extracted")
        self.assertIn("--profile", flags, "flags before the wrap must still be extracted")

    def test_post_wrap_flag_is_extracted_and_refuted_where_possible(self):
        """Extraction across a wrap is proven separately from refutation, because
        refutation now requires an exhaustive surface. The wrap fix is what makes the
        flag visible at all; whether it can be REFUTED depends on the surface kind.
        """
        found = self.gate.extract([SKILLS / "cli" / "SKILL.md"])
        self.assertIn("--subject", found[("evidence", "run")], "post-wrap flag must be extracted")
        with tempfile.TemporaryDirectory() as tmp:
            for src in sorted(SKILLS.rglob("*.md")):
                (Path(tmp) / src.name).write_text(src.read_text())
            (Path(tmp) / "zz_wrap.md").write_text(
                "```\namq-squad activity set --me H\n    --task T --bogus-after-wrap\n```\n"
            )
            result = subprocess.run(
                [sys.executable, str(GATE), str(BINARY), tmp], capture_output=True, text=True
            )
        self.assertEqual(result.returncode, 1, f"a post-wrap bogus flag on an exhaustive surface must fail:\n{result.stderr}")
        self.assertIn("--bogus-after-wrap", result.stderr)

    # MEDIUM 3 ---------------------------------------------------------------
    def test_malformed_verb_with_leading_dash_is_reported(self):
        """Blanket-skipping any leading-dash token was the vanishing-reference class
        surviving in the flag branch after being fixed in the verb branch."""
        with tempfile.TemporaryDirectory() as tmp:
            probe = Path(tmp) / "probe.md"
            probe.write_text("```\namq-squad -doctor\n```\n")
            found = self.gate.extract([probe])
        self.assertIn(("-doctor", None), found, "a malformed verb must be kept, not discarded")

    def test_lone_global_flag_is_still_accepted(self):
        """`amq-squad --version` names no verb and must not be reported as drift."""
        with tempfile.TemporaryDirectory() as tmp:
            probe = Path(tmp) / "probe.md"
            probe.write_text("```\namq-squad --version\n```\n")
            found = self.gate.extract([probe])
        self.assertIn((self.gate.GLOBAL_FLAG_KEY, None), found)
        self.assertNotIn(("--version", None), found)

    # MEDIUM 4 ---------------------------------------------------------------
    def test_bash_permission_string_is_checked(self):
        """Bash(amq-squad review-worktree remove:*) is what an operator puts in an
        allowlist, so a rename that breaks it must fail."""
        target = SKILLS / "amq-squad" / "SKILL.md"
        with MutatedSkill(
            target, "Bash(amq-squad review-worktree remove", "Bash(amq-squad review-worktreeX remove"
        ):
            result = run_gate()
        self.assertEqual(result.returncode, 1, "a rename inside a permission string must fail")
        self.assertIn("review-worktreeX", result.stderr)

    # MEDIUM 5 ---------------------------------------------------------------
    def test_flag_coverage_floor_exists(self):
        """Verbs were floored; flags were not, so a flag-help format change would
        make everything unverifiable and keep the build green."""
        self.assertGreater(self.gate.MIN_FLAGS_CLAIMED_FOR_FLOOR, 0)

    def test_zero_verified_flags_fails_when_flags_were_claimed(self):
        original = self.gate.flag_surface

        def blind(binary, verb, sub):
            return set(), False

        self.gate.flag_surface = blind
        try:
            # Reproduce main()'s floor with flag parsing broken.
            paths = sorted(SKILLS.rglob("*.md"))
            found = self.gate.extract(paths)
            claimed = sum(len(v) for k, v in found.items() if k[0] not in self.gate.NOT_A_COMMAND)
            self.assertGreaterEqual(claimed, self.gate.MIN_FLAGS_CLAIMED_FOR_FLOOR)
        finally:
            self.gate.flag_surface = original

    # The crash found while probing HIGH 1 ----------------------------------
    def test_same_verb_bare_and_with_subcommand_does_not_crash(self):
        """Sorting raw (verb, sub) tuples raised TypeError when one verb appeared
        both bare and with a subcommand, because None is not comparable to str. A
        gate that dies on a legitimate corpus shape is unusable."""
        with tempfile.TemporaryDirectory() as tmp:
            probe = Path(tmp) / "probe.md"
            probe.write_text("```\namq-squad team\namq-squad team sync --apply\n```\n")
            result = subprocess.run(
                [sys.executable, str(GATE), str(BINARY), tmp], capture_output=True, text=True
            )
        self.assertNotIn("Traceback", result.stderr, f"gate crashed:\n{result.stderr}")



class ThirdRoundFindings(unittest.TestCase):
    """The six convergence findings plus the three they exposed.

    Every one was the gate reporting a verdict it had not earned.
    """

    def setUp(self):
        if not BINARY.exists():
            self.skipTest("binary not built; run make build")
        self.gate = load_gate()

    # H1 --------------------------------------------------------------------
    def test_subcommand_surface_is_queried_not_guessed(self):
        """Per-path error interpretation proved `doctor totallybogus` valid, because
        doctor's error is an arity complaint, not the one recognized negative.
        Membership over a queried set has no such blind spot."""
        subs, observable = self.gate.subcommand_surface(str(BINARY), "doctor")
        self.assertTrue(observable, "doctor's subcommand surface must be observable")
        self.assertEqual(subs, set(), "doctor has no subcommands")
        self.assertNotIn("totallybogus", subs)

    def test_error_list_only_subcommands_resolve(self):
        """The UNION proof. team's usage block lists 8 subcommands; lead, autonomous
        and shared-cwd-exception appear ONLY in the error list. Using either source
        alone would fail VALID references -- fail-closed on incomplete data invents
        drift, which is a defect in the opposite direction from fail-open."""
        subs, observable = self.gate.subcommand_surface(str(BINARY), "team")
        self.assertTrue(observable)
        for only_in_error_list in ("lead", "autonomous", "shared-cwd-exception"):
            self.assertIn(only_in_error_list, subs, f"{only_in_error_list} must resolve")
        for in_usage_block in ("init", "sync", "member"):
            self.assertIn(in_usage_block, subs)

    def test_subcommands_resolve_for_every_form_of_verb(self):
        """Three different verb shapes: enumerates via 'use', via 'Try', and one with
        an implicit default subcommand that enumerates neither."""
        for verb, expected in (("evidence", "run"), ("team", "sync"), ("review-worktree", "remove")):
            with self.subTest(verb=verb):
                subs, observable = self.gate.subcommand_surface(str(BINARY), verb)
                self.assertTrue(observable, f"{verb} surface must be observable")
                self.assertIn(expected, subs)

    # M2 --------------------------------------------------------------------
    def test_global_flags_are_parsed_from_real_top_level_help(self):
        """flag_surface(binary, "", None) ran `amq-squad "" --help`, returned an empty
        set, and empty was then treated as all-verified."""
        top = self.gate.run_help(str(BINARY))
        flags = set(re.findall(r"--[A-Za-z][A-Za-z0-9-]*", top))
        self.assertGreater(len(flags), 5, "top-level help must yield real global flags")

    # M3 --------------------------------------------------------------------
    def test_two_commands_in_one_span_stay_separate(self):
        """Unconditional span collapse merged them, inventing drift."""
        with tempfile.TemporaryDirectory() as tmp:
            probe = Path(tmp) / "x.md"
            probe.write_text("Run `amq-squad team sync --apply` then `amq-squad doctor --json`.\n")
            found = self.gate.extract([probe])
        self.assertEqual(found.get(("team", "sync")), {"--apply"})
        self.assertEqual(found.get(("doctor", None)), {"--json"})

    # M4 --------------------------------------------------------------------
    def test_single_dash_token_after_verb_is_not_discarded(self):
        with tempfile.TemporaryDirectory() as tmp:
            probe = Path(tmp) / "x.md"
            probe.write_text("```\namq-squad team -sync\n```\n")
            found = self.gate.extract([probe])
        self.assertIn(("team", "-sync"), found, "a single-dash token must be reported, not dropped")

    def test_malformed_flag_is_reported_not_dropped(self):
        with tempfile.TemporaryDirectory() as tmp:
            probe = Path(tmp) / "x.md"
            probe.write_text("```\namq-squad team sync --apply_bad\n```\n")
            found = self.gate.extract([probe])
        self.assertIn((self.gate.MALFORMED_FLAG_KEY, None), found)
        self.assertIn("--apply_bad", found[(self.gate.MALFORMED_FLAG_KEY, None)])

    # M5 --------------------------------------------------------------------
    def test_flag_floor_is_ratio_based_not_exactly_zero(self):
        """An exactly-zero floor was bypassed by a mass drop or by one verified flag."""
        self.assertGreater(self.gate.MIN_VERIFIED_FLAGS, 1, "absolute floor must exceed 1")
        self.assertGreaterEqual(self.gate.MIN_VERIFIED_FLAG_RATIO, 0.25)

    # Self-exposed 7: shell comments -----------------------------------------
    def test_inline_shell_comment_is_not_a_subcommand(self):
        """`amq-squad doctor   # AMQ version, tmux` extracted `#` as a SUBCOMMAND,
        which is what made the bogus path reachable at all."""
        with tempfile.TemporaryDirectory() as tmp:
            probe = Path(tmp) / "x.md"
            probe.write_text("```\namq-squad doctor        # AMQ version, team config, tmux\n```\n")
            found = self.gate.extract([probe])
        self.assertIn(("doctor", None), found)
        self.assertNotIn(("doctor", "#"), found)

    # Self-exposed 8: the -- separator ---------------------------------------
    def test_flags_after_double_dash_belong_to_the_wrapped_command(self):
        """`evidence run ... -- make ci`: make's flags are not amq-squad's, and `--`
        is a separator rather than a flag claim."""
        with tempfile.TemporaryDirectory() as tmp:
            probe = Path(tmp) / "x.md"
            probe.write_text("```\namq-squad evidence run T --me A -- make --wrong-flag\n```\n")
            found = self.gate.extract([probe])
        flags = found.get(("evidence", "run"), set())
        self.assertIn("--me", flags)
        self.assertNotIn("--wrong-flag", flags, "a wrapped command's flags must not be attributed")
        self.assertNotIn("--", flags)

    # Self-exposed 9: whitespace crossing newlines ---------------------------
    def test_usage_parse_does_not_cross_lines(self):
        r"""`\s+` matches NEWLINES, so a usage line ending at the verb ran into the
        next line and captured "amq-squad" as a subcommand of doctor. A wrong
        authoritative set is worse than none, because it is trusted."""
        subs, _ = self.gate.subcommand_surface(str(BINARY), "doctor")
        self.assertNotIn("amq-squad", subs, "the parse must not cross line boundaries")

    # L6 --------------------------------------------------------------------
    def test_none_safe_sort_is_actually_reached(self):
        """The earlier regression test exited on the MIN_COMMANDS floor before ever
        reaching the sort, so it would have passed with the fix reverted. Feed a
        corpus large enough to clear the floor."""
        real = sorted(SKILLS.rglob("*.md"))
        with tempfile.TemporaryDirectory() as tmp:
            for src in real:
                (Path(tmp) / src.name).write_text(src.read_text())
            # Force the mixed bare/subcommand shape that triggered the crash.
            (Path(tmp) / "zz_mixed.md").write_text("```\namq-squad team\namq-squad team sync --apply\n```\n")
            result = subprocess.run(
                [sys.executable, str(GATE), str(BINARY), tmp], capture_output=True, text=True
            )
        self.assertNotIn("Traceback", result.stderr, f"gate crashed:\n{result.stderr}")
        self.assertNotIn("MIN_COMMANDS", result.stderr)
        self.assertNotIn("expected >=", result.stderr, "corpus must be large enough to reach the sort")



class FourthRoundFindings(unittest.TestCase):
    """Round-4 findings: five from convergence plus finding #10."""

    def setUp(self):
        if not BINARY.exists():
            self.skipTest("binary not built; run make build")
        self.gate = load_gate()

    # Finding 1 + #10: structure, not prose or examples ----------------------
    def test_flag_surface_ignores_prose_and_examples(self):
        """Asserts the NEGATIVE, which the previous version never did.

        Scanning the whole help body let any indented dash-leading line become a
        definition, so an indented PROSE line mentioning a flag entered the surface.
        A test that only checks a real flag is present cannot see that.
        """
        gate = self.gate
        fake_help = "\n".join(
            [
                "amq-squad fake - a fake command",
                "",
                "Usage:",
                "  amq-squad fake [--real-one VALUE]",
                "",
                "Description:",
                "  --bogus-prose-flag appears only in prose and is not a flag.",
                "",
                "Examples:",
                "  amq-squad fake --bogus-example-flag",
                "",
            ]
        )
        original = gate.run_help
        gate.run_help = lambda *a, **k: fake_help
        try:
            known, exhaustive = gate.flag_surface("binary", "fake", None)
        finally:
            gate.run_help = original
        self.assertIn("--real-one", known, "the usage block IS a flag surface")
        self.assertNotIn("--bogus-prose-flag", known, "a prose line must NOT enter the surface")
        self.assertNotIn("--bogus-example-flag", known, "an EXAMPLE must NOT enter the surface")
        self.assertFalse(exhaustive, "a hand-written usage block is illustrative")

    def test_go_definition_list_is_still_read(self):
        """Section scoping must not break the exhaustive case it exists to serve."""
        known, exhaustive = self.gate.flag_surface(str(BINARY), "activity", "set")
        self.assertTrue(exhaustive)
        for real in ("--me", "--task", "--phase"):
            self.assertIn(real, known)

    def test_bogus_global_flag_is_unverifiable_not_refuted(self):
        """Encodes the MODEL, not merely a nonzero exit.

        The earlier version exited at the MIN_COMMANDS floor before ever reaching
        global verification, so it passed for the wrong reason. Top-level help is
        hand-written (its Global flags section omits --version), therefore
        illustrative, therefore a bogus global flag is UNVERIFIABLE rather than
        refuted.
        """
        with tempfile.TemporaryDirectory() as tmp:
            for src in sorted(SKILLS.rglob("*.md")):
                (Path(tmp) / src.name).write_text(src.read_text())
            (Path(tmp) / "zz_global.md").write_text(
                "```\namq-squad --version\namq-squad --definitely-not-global\n```\n"
            )
            result = subprocess.run(
                [sys.executable, str(GATE), str(BINARY), tmp], capture_output=True, text=True
            )
        self.assertNotIn("expected >=", result.stderr, "corpus must clear the floors and reach the check")
        self.assertEqual(result.returncode, 0, f"illustrative surface must not refute:\n{result.stderr}")
        self.assertIn("NOT verifiable", result.stdout)
        self.assertIn("--definitely-not-global", result.stdout)

    # Finding 2: one span, two commands --------------------------------------
    def test_one_span_two_commands_both_extracted(self):
        """The earlier test used TWO spans, so it missed this: after a span is
        collapsed the second command is no longer at a line start, and the truncation
        discarded it instead of processing it."""
        with tempfile.TemporaryDirectory() as tmp:
            probe = Path(tmp) / "x.md"
            probe.write_text("Do `amq-squad team sync --apply then amq-squad doctor --json` now.\n")
            found = self.gate.extract([probe])
        self.assertIn(("team", "sync"), found, "first command must be extracted")
        self.assertIn(("doctor", None), found, "SECOND command in the SAME span must be extracted")
        self.assertIn("--json", found[("doctor", None)])

    # Finding 3: later single-dash tokens ------------------------------------
    def test_later_single_dash_token_is_not_dropped(self):
        """`team sync -apply` passed with no claimed flag: the collector retained only
        double-dash tokens, so a single-dash token vanished."""
        with tempfile.TemporaryDirectory() as tmp:
            probe = Path(tmp) / "x.md"
            probe.write_text("```\namq-squad team sync -apply\n```\n")
            found = self.gate.extract([probe])
        claimed = set()
        for flags in found.values():
            claimed |= flags
        self.assertIn("-apply", claimed, "a single-dash token must be reported, not dropped")

    # Finding 4: floor math, and the main()-level exit test ------------------
    def test_floor_uses_productions_ceiling_not_pythons(self):
        """Exercises required_verified(), at inputs where ceil and int DIVERGE.

        Two earlier versions of this test were vacuous. The first asserted
        math.ceil(23 * 0.5) == 12, which tests Python, not production. The second
        called production but at 23 verifiable, where max(12, ceil(11.5)) and
        max(12, int(11.5)) are BOTH 12 -- so an int() revert still passed.

        At 25 verifiable they differ: ceil(12.5) = 13, int(12.5) = 12. Choosing
        inputs where the behaviours diverge is the whole point; a correct assertion
        at an input that cannot distinguish them proves nothing.
        """
        self.assertEqual(self.gate.required_verified(25), 13, "ceil(12.5) must give 13, not int()'s 12")
        self.assertEqual(self.gate.required_verified(31), 16, "ceil(15.5) must give 16")
        # And the absolute floor still dominates below the crossover.
        self.assertEqual(self.gate.required_verified(10), self.gate.MIN_VERIFIED_FLAGS)

    def test_floors_sit_at_committed_coverage(self):
        """Floors set far below real coverage permitted a 13 -> 5 collapse at exit 0."""
        self.assertGreaterEqual(self.gate.MIN_VERIFIED_FLAGS, 12)
        self.assertGreaterEqual(self.gate.MIN_VERIFIABLE_FLAGS, 12)

    def test_floor_guards_the_verifiable_set_too(self):
        self.assertGreater(self.gate.MIN_VERIFIABLE_FLAGS, 0)
        self.assertGreater(self.gate.MIN_VERIFIED_FLAGS, 0)

    def test_main_exits_2_when_the_verifiable_set_collapses(self):
        """The main()-level exit-code assertion, asked for twice. Blind the flag
        surface so nothing is verifiable, then assert the process exits 2."""
        import unittest.mock as mock

        with tempfile.TemporaryDirectory() as tmp:
            for src in sorted(SKILLS.rglob("*.md")):
                (Path(tmp) / src.name).write_text(src.read_text())
            runner = (
                "import sys, importlib.util\n"
                f"spec = importlib.util.spec_from_file_location('g', {str(GATE)!r})\n"
                "g = importlib.util.module_from_spec(spec); spec.loader.exec_module(g)\n"
                # Blind only PER-COMMAND surfaces: blinding the global one too would
                # trip the earlier global-flag guard and never reach the floor.\n"
                "_orig = g.flag_surface\n"
                "g.flag_surface = lambda b, v, s2=None, **k: (_orig(b, v, s2) if not v else (set(), False))\n"
                f"sys.argv = ['g', {str(BINARY)!r}, {tmp!r}]\n"
                "raise SystemExit(g.main())\n"
            )
            result = subprocess.run([sys.executable, "-c", runner], capture_output=True, text=True)
        self.assertEqual(result.returncode, 2, f"a collapsed verifiable set must exit 2:\n{result.stderr}")
        self.assertIn("verifiable", result.stderr)

    # Finding 5: comment stripping respects quotes --------------------------
    def test_hash_inside_quotes_is_not_a_comment(self):
        """`--note "issue #123" --force` lost --force to a blunt comment strip."""
        with tempfile.TemporaryDirectory() as tmp:
            probe = Path(tmp) / "x.md"
            probe.write_text('```\namq-squad activity set --me H --detail "issue #123" --phase coding\n```\n')
            found = self.gate.extract([probe])
        flags = found.get(("activity", "set"), set())
        self.assertIn("--phase", flags, "a flag after a quoted # must survive")
        self.assertIn("--detail", flags)

    def test_real_trailing_comment_is_still_stripped(self):
        with tempfile.TemporaryDirectory() as tmp:
            probe = Path(tmp) / "x.md"
            probe.write_text("```\namq-squad doctor        # AMQ version, tmux, wake\n```\n")
            found = self.gate.extract([probe])
        self.assertIn(("doctor", None), found)
        self.assertNotIn(("doctor", "#"), found)



class UnknownSubcommandContract(unittest.TestCase):
    """#561: one exact, complete unknown-subcommand contract."""

    def setUp(self):
        if not BINARY.exists():
            self.skipTest("binary not built; run make build")
        self.gate = load_gate()

    def test_canonical_complete_list_resolves_without_usage_help(self):
        """Prove the canonical parser itself, with the usage source disabled.

        The list deliberately includes compatibility/hidden aliases so this test pins
        the completeness property rather than merely accepting the punctuation.
        """
        error_text = (
            "error: unknown 'team' subcommand: \"x\". "
            "Try 'init', 'operator', 'rm', or 'delete'."
        )
        self.gate._SUB_SURFACE_CACHE.clear()
        original = self.gate.run_help
        self.gate.run_help = lambda *a, **k: error_text
        try:
            subs, observable = self.gate.subcommand_surface("binary", "team")
        finally:
            self.gate.run_help = original
            self.gate._SUB_SURFACE_CACHE.clear()
        self.assertTrue(observable)
        self.assertEqual(subs, {"init", "operator", "rm", "delete"})

    def test_legacy_wordings_are_not_a_second_contract(self):
        legacy = (
            'error: unknown amq subcommand "x". Use env or cleanup',
            'error: unknown evidence subcommand "x"; use run or show',
        )
        for error_text in legacy:
            with self.subTest(error_text=error_text):
                self.gate._SUB_SURFACE_CACHE.clear()
                original = self.gate.run_help
                self.gate.run_help = lambda *a, **k: error_text
                try:
                    subs, observable = self.gate.subcommand_surface("binary", "team")
                finally:
                    self.gate.run_help = original
                    self.gate._SUB_SURFACE_CACHE.clear()
                self.assertFalse(observable)
                self.assertEqual(subs, set())

    def test_prose_mentioning_a_subcommand_is_not_an_authoritative_list(self):
        """The false-observation door the first generalization opened.

        Unanchored, this yielded subcommands {help, for, examples}.
        """
        self.gate._SUB_SURFACE_CACHE.clear()
        original = self.gate.run_help
        self.gate.run_help = lambda *a, **k: (
            "Note: an unknown fake subcommand is recoverable. Use help for examples"
        )
        try:
            subs, observable = self.gate.subcommand_surface("binary", "fake")
        finally:
            self.gate.run_help = original
            self.gate._SUB_SURFACE_CACHE.clear()
        self.assertFalse(observable, "prose must not be read as an authoritative list")
        self.assertEqual(subs, set())

    def test_a_flag_list_is_not_a_subcommand_list(self):
        """"; use --json, --verbose" yielded subcommands {json, verbose}."""
        self.gate._SUB_SURFACE_CACHE.clear()
        original = self.gate.run_help
        self.gate.run_help = lambda *a, **k: (
            "error: unknown 'fake' subcommand: \"x\". Try '--json' or '--verbose'."
        )
        try:
            subs, _ = self.gate.subcommand_surface("binary", "fake")
        finally:
            self.gate.run_help = original
            self.gate._SUB_SURFACE_CACHE.clear()
        self.assertNotIn("json", subs, "a flag must not become a subcommand")
        self.assertNotIn("verbose", subs)

    def test_an_error_about_a_different_verb_does_not_populate_this_verb(self):
        """Verb binding: the error names its verb, so it must match the probed one."""
        self.gate._SUB_SURFACE_CACHE.clear()
        original = self.gate.run_help
        self.gate.run_help = lambda *a, **k: (
            "error: unknown 'other' subcommand: \"x\". Try 'alpha' or 'beta'."
        )
        try:
            subs, observable = self.gate.subcommand_surface("binary", "fake")
        finally:
            self.gate.run_help = original
            self.gate._SUB_SURFACE_CACHE.clear()
        self.assertNotIn("alpha", subs, "another verb's error must not populate this surface")
        self.assertFalse(observable)

    def test_amq_resolves_all_fifteen(self):
        """The specific regression: `amq` was unobservable, so documenting any of its
        subcommands failed the build."""
        subs, observable = self.gate.subcommand_surface(str(BINARY), "amq")
        self.assertTrue(observable)
        self.assertGreaterEqual(len(subs), 15, f"expected all 15 amq subcommands, got {sorted(subs)}")

    def test_documenting_an_amq_subcommand_no_longer_blocks_the_build(self):
        """End to end: this is what the finding actually cost."""
        with tempfile.TemporaryDirectory() as tmp:
            for src in sorted(SKILLS.rglob("*.md")):
                (Path(tmp) / src.name).write_text(src.read_text())
            (Path(tmp) / "zz_amq.md").write_text(
                "```\namq-squad amq drain --include-body\namq-squad amq route explain\n```\n"
            )
            result = subprocess.run(
                [sys.executable, str(GATE), str(BINARY), tmp], capture_output=True, text=True
            )
        self.assertEqual(result.returncode, 0, f"documenting real amq subcommands must pass:\n{result.stderr}")
        self.assertNotIn("could not observe", result.stderr)

    def test_a_bogus_amq_subcommand_still_fails(self):
        """The counter-case: making amq observable must not make it permissive."""
        with tempfile.TemporaryDirectory() as tmp:
            for src in sorted(SKILLS.rglob("*.md")):
                (Path(tmp) / src.name).write_text(src.read_text())
            (Path(tmp) / "zz_amq_bad.md").write_text("```\namq-squad amq notasubcommand\n```\n")
            result = subprocess.run(
                [sys.executable, str(GATE), str(BINARY), tmp], capture_output=True, text=True
            )
        self.assertEqual(result.returncode, 1, "a bogus amq subcommand must be refuted")
        self.assertIn("notasubcommand", result.stderr)


if __name__ == "__main__":
    unittest.main(verbosity=2)
