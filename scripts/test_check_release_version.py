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
