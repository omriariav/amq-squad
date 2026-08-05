#!/usr/bin/env python3
"""Focused tests for the canonical release-notes gate."""

from __future__ import annotations

import importlib.util
import os
import sys
import tempfile
import unittest


SCRIPT = os.path.join(os.path.dirname(__file__), "check-release-version.py")
sys.dont_write_bytecode = True
SPEC = importlib.util.spec_from_file_location("check_release_version", SCRIPT)
assert SPEC is not None and SPEC.loader is not None
CHECK_RELEASE_VERSION = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(CHECK_RELEASE_VERSION)


class RequireReleaseNotesTest(unittest.TestCase):
    def test_missing_canonical_release_notes_fails(self) -> None:
        with tempfile.TemporaryDirectory() as root:
            failures: list[str] = []

            CHECK_RELEASE_VERSION.require_release_notes(root, "v2.22.0", failures)

            self.assertEqual(
                failures,
                ["docs/v2.22.0-release-notes.md: missing canonical release notes"],
            )

    def test_policy_normalization_ignores_markdown_and_html_markup(self) -> None:
        policy = CHECK_RELEASE_VERSION.AMQ_COMPATIBILITY_POLICY
        markdown = policy.replace(
            "AMQ 0.52.x is the supported series",
            "AMQ **0.52.x is the supported series**",
        ).replace("latest", "`latest`")
        html_body = (
            "<p>"
            + policy.replace(
                "AMQ 0.52.x is the supported series",
                "AMQ <strong>0.52.x is the supported series</strong>",
            ).replace("latest", "<code>latest</code>")
            + "</p>"
        )

        self.assertEqual(
            CHECK_RELEASE_VERSION.normalize_policy_text(markdown),
            CHECK_RELEASE_VERSION.normalize_policy_text(policy),
        )
        self.assertEqual(
            CHECK_RELEASE_VERSION.normalize_policy_text(html_body),
            CHECK_RELEASE_VERSION.normalize_policy_text(policy),
        )

    def test_skill_frontmatter_version_matches_generated_shape(self) -> None:
        body = (
            "---\n"
            "name: cli\n"
            'version: "2.24.0"  # x-release-please-version\n'
            "description: test\n"
            "---\n"
            "# CLI\n"
        )

        version = CHECK_RELEASE_VERSION.skill_frontmatter_version(body)

        self.assertEqual(version, "2.24.0")

    def test_skill_frontmatter_version_matches_crlf_shape(self) -> None:
        body = (
            "---\r\n"
            "name: cli\r\n"
            'version:\t"2.24.0"\r\n'
            "---\r\n"
            "# CLI\r\n"
        )

        self.assertEqual(
            CHECK_RELEASE_VERSION.skill_frontmatter_version(body),
            "2.24.0",
        )

    def test_skill_frontmatter_version_ignores_body_version_line(self) -> None:
        body = (
            "---\n"
            "name: cli\n"
            "---\n"
            "# CLI\n"
            "```yaml\n"
            "version: 9.9.9\n"
            "```\n"
        )

        self.assertIsNone(CHECK_RELEASE_VERSION.skill_frontmatter_version(body))

    def test_skill_frontmatter_version_requires_opening_frontmatter(self) -> None:
        body = "# CLI\n\nversion: 9.9.9\n"

        self.assertIsNone(CHECK_RELEASE_VERSION.skill_frontmatter_version(body))

    def test_skill_frontmatter_version_rejects_split_field_value(self) -> None:
        body = (
            "---\n"
            "name: cli\n"
            "version:\n"
            "9.9.9\n"
            "---\n"
            "# CLI\n"
        )

        self.assertIsNone(CHECK_RELEASE_VERSION.skill_frontmatter_version(body))

    def test_matching_canonical_release_notes_passes(self) -> None:
        with tempfile.TemporaryDirectory() as root:
            docs = os.path.join(root, "docs")
            os.makedirs(docs)
            with open(
                os.path.join(docs, "v2.22.0-release-notes.md"),
                "w",
                encoding="utf-8",
            ) as release_notes:
                release_notes.write("# amq-squad v2.22.0\n\nRelease notes.\n")
            failures: list[str] = []

            CHECK_RELEASE_VERSION.require_release_notes(root, "v2.22.0", failures)

            self.assertEqual(failures, [])

    def test_mismatched_release_notes_heading_fails(self) -> None:
        with tempfile.TemporaryDirectory() as root:
            docs = os.path.join(root, "docs")
            os.makedirs(docs)
            with open(
                os.path.join(docs, "v2.22.0-release-notes.md"),
                "w",
                encoding="utf-8",
            ) as release_notes:
                release_notes.write("# amq-squad v2.21.0\n")
            failures: list[str] = []

            CHECK_RELEASE_VERSION.require_release_notes(root, "v2.22.0", failures)

            self.assertEqual(
                failures,
                [
                    "docs/v2.22.0-release-notes.md: first heading "
                    "'# amq-squad v2.21.0' != '# amq-squad v2.22.0'"
                ],
            )


if __name__ == "__main__":
    unittest.main()
