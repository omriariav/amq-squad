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
        policy = CHECK_RELEASE_VERSION.amq_compatibility_policy("0.116.2")
        markdown = policy.replace(
            "minimum supported release",
            "**minimum supported release**",
        ).replace("0.116.2", "`0.116.2`")
        html_body = (
            "<p>"
            + policy.replace(
                "minimum supported release",
                "<strong>minimum supported release</strong>",
            ).replace("0.116.2", "<code>0.116.2</code>")
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

    def test_amq_floor_is_read_from_the_doctor_constant(self) -> None:
        # The repository's own doctor.go: the floor the script requires is
        # the one doctor enforces (internal/cli TestReleaseCheckReadsTheDoctorFloor
        # compares it with the compiled constant).
        repo = os.path.dirname(os.path.dirname(os.path.abspath(SCRIPT)))
        floor = CHECK_RELEASE_VERSION.read_amq_floor(repo)
        with open(os.path.join(repo, "internal/cli/doctor.go"), encoding="utf-8") as f:
            self.assertIn(f'const doctorMinAMQVersion = "{floor}"\n', f.read())
        self.assertEqual(
            CHECK_RELEASE_VERSION.amq_compatibility_policy(floor),
            f"AMQ {floor} is the minimum supported release",
        )
        self.assertFalse(hasattr(CHECK_RELEASE_VERSION, "AMQ_MIN_VERSION"))

    def test_amq_floor_follows_a_changed_constant_and_fails_closed(self) -> None:
        with tempfile.TemporaryDirectory() as root:
            os.makedirs(os.path.join(root, "internal", "cli"))
            path = os.path.join(root, "internal", "cli", "doctor.go")
            for body, want in (
                ('package cli\n\nconst doctorMinAMQVersion = "9.8.7"\n', "9.8.7"),
                ("package cli\n", None),
                ('const doctorMinAMQVersion = "1.2.3"\nconst doctorMinAMQVersion = "1.2.4"\n', None),
                ('const doctorMinAMQVersion = "v1.2"\n', None),
            ):
                with open(path, "w", encoding="utf-8") as f:
                    f.write(body)
                if want is None:
                    with self.assertRaises(ValueError):
                        CHECK_RELEASE_VERSION.read_amq_floor(root)
                else:
                    self.assertEqual(CHECK_RELEASE_VERSION.read_amq_floor(root), want)

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
