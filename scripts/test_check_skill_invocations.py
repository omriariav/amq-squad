#!/usr/bin/env python3
"""Tests for the skill invocation-execution gate.

The gate exists because flag EXISTENCE is not invocation CORRECTNESS
(cli/LEARNINGS.md): a documented command can name only real flags and still
error as written. These tests prove the two properties that make the gate
worth trusting: it extracts what the docs actually say (including the wrapped,
piped, and inline-span forms that hid the original four failures), and its
placeholder substitution cannot silently skip an unmapped token.
"""

from __future__ import annotations

import importlib.util
import tempfile
import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
GATE = REPO / "scripts" / "check-skill-invocations.py"


def load_gate():
    spec = importlib.util.spec_from_file_location("invocation_gate", GATE)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


gate = load_gate()


class ExtractFences(unittest.TestCase):
    def extract(self, text: str):
        return [cmd for (_, _, cmd) in gate.extract_from_fences(text, Path("x.md"))]

    def test_plain_and_dollar_prefixed_lines(self):
        text = "```sh\namq-squad status --session S --json\n$ amq-squad doctor --session S\n```\n"
        self.assertEqual(
            self.extract(text),
            ["amq-squad status --session S --json", "amq-squad doctor --session S"],
        )

    def test_backslash_continuation_joins(self):
        text = "```sh\namq-squad new profile R --project P \\\n  --roles cto,qa\n```\n"
        self.assertEqual(self.extract(text), ["amq-squad new profile R --project P --roles cto,qa"])

    def test_pipeline_segment_is_the_invocation(self):
        text = "```sh\ncat prompt.md | amq-squad send --session S --body-file -\n```\n"
        self.assertEqual(self.extract(text), ["amq-squad send --session S --body-file -"])

    def test_inline_comment_stripped(self):
        text = "```sh\namq-squad doctor --session S   # setup check\n```\n"
        self.assertEqual(self.extract(text), ["amq-squad doctor --session S"])

    def test_outside_fence_ignored(self):
        self.assertEqual(self.extract("amq-squad status --session S\n"), [])

    def test_skip_marker_on_previous_line(self):
        text = "```sh\n# skill-invocation-check:skip\namq-squad status --session S\n```\n"
        self.assertEqual(self.extract(text), [])


class ExtractInline(unittest.TestCase):
    def extract(self, text: str):
        return [cmd for (_, _, cmd) in gate.extract_inline(text, Path("x.md"))]

    def test_routing_table_cell(self):
        text = '| "claim this" | `amq-squad task claim ID --me H --session S` |\n'
        self.assertEqual(self.extract(text), ["amq-squad task claim ID --me H --session S"])

    def test_verb_only_prose_mention_skipped(self):
        # `amq-squad send` in prose is a reference, not an invocation; running
        # it bare would fail on a missing selector and invent doc drift.
        self.assertEqual(self.extract("**`amq-squad send` is pane delivery.**\n"), [])

    def test_wrapped_span_flattens(self):
        text = "`amq-squad evidence run TASK --me ACTOR\n--subject TEXT -- COMMAND [ARG...]`\n"
        self.assertEqual(
            self.extract(text),
            ["amq-squad evidence run TASK --me ACTOR --subject TEXT -- COMMAND [ARG...]"],
        )


class Substitute(unittest.TestCase):
    def substitute(self, command: str, fixture: Path) -> str:
        return gate.substitute(command, fixture, "merge", "default_branch_push")

    def test_placeholders_and_catalog(self):
        with tempfile.TemporaryDirectory() as tmp:
            got = self.substitute(
                "amq-squad gate raise --gate TOPIC --kind KIND --action ACTION --target TARGET --session S --me H",
                Path(tmp),
            )
        self.assertEqual(
            got,
            "amq-squad gate raise --gate demo --kind merge --action default_branch_push "
            "--target target-1 --session issue-1 --me cto",
        )

    def test_version_and_repo_literals(self):
        with tempfile.TemporaryDirectory() as tmp:
            got = self.substitute(
                "amq-squad verify release-plan --repository OWNER/REPO --remote-url https://github.com/OWNER/REPO.git --version vX.Y.Z",
                Path(tmp),
            )
        self.assertIn("--repository owner/repo ", got)
        self.assertIn("repo.git", got)
        self.assertIn("--version v9.9.9", got)

    def test_command_argv_placeholder(self):
        with tempfile.TemporaryDirectory() as tmp:
            got = self.substitute(
                "amq-squad evidence run ID --me H --session S --subject TEXT -- COMMAND [ARG...]",
                Path(tmp),
            )
        self.assertTrue(got.endswith("-- make ci"))

    def test_fixture_path_with_capital_segment_is_not_a_placeholder(self):
        # macOS tempdirs contain "/T/"; the fixture path must be spliced in
        # after the token pass or its own segments read as unmapped tokens.
        with tempfile.TemporaryDirectory() as tmp:
            fixture = Path(tmp) / "T" / "inv-1"
            fixture.mkdir(parents=True)
            got = self.substitute("amq-squad doctor --project P --session S", fixture)
        self.assertIn(str(fixture), got)

    def test_illustrative_paths_created_under_fixture(self):
        with tempfile.TemporaryDirectory() as tmp:
            fixture = Path(tmp)
            got = self.substitute(
                'amq-squad new profile NAME --cwd "dev-1=/path/to/wt-a,dev-2=/path/to/wt-b"',
                fixture,
            )
        self.assertNotIn("/path/to/", got)
        self.assertIn(str(fixture / "paths"), got)

    def test_unknown_placeholder_fails_loudly(self):
        with tempfile.TemporaryDirectory() as tmp:
            with self.assertRaises(KeyError):
                self.substitute("amq-squad frob --thing UNMAPPEDTOKEN", Path(tmp))


class Collect(unittest.TestCase):
    def test_learnings_excluded_and_something_extracted(self):
        invocations = gate.collect()
        self.assertTrue(invocations)
        for path, _, _ in invocations:
            self.assertNotEqual(path.name, "LEARNINGS.md")


if __name__ == "__main__":
    unittest.main()
