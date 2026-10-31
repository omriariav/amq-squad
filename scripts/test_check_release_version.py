#!/usr/bin/env python3
"""Focused tests for the canonical release-notes gate."""

from __future__ import annotations

import contextlib
import importlib.util
import io
import json
import os
import subprocess
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


GIT_ENV = {
    **os.environ,
    "GIT_AUTHOR_NAME": "owner", "GIT_AUTHOR_EMAIL": "o@example.com",
    "GIT_COMMITTER_NAME": "owner", "GIT_COMMITTER_EMAIL": "o@example.com",
    "GIT_CONFIG_GLOBAL": os.devnull, "GIT_CONFIG_NOSYSTEM": "1",
}


def git(root: str, *args: str) -> None:
    subprocess.run(["git", "-C", root, *args], check=True, capture_output=True, env=GIT_ENV)


class RequireMarketplaceRefTest(unittest.TestCase):
    """The marketplace installs from main: the release check fails closed
    when main's plugin manifests do not carry the released version (3.0.2,
    the owner's A of 2026-10-30)."""

    def repo(self, root: str, claude: str, codex: str) -> None:
        git(root, "init", "-q", "-b", "main")
        for rel, version in zip(CHECK_RELEASE_VERSION.MARKETPLACE_MANIFESTS, (claude, codex)):
            os.makedirs(os.path.join(root, os.path.dirname(rel)), exist_ok=True)
            with open(os.path.join(root, rel), "w", encoding="utf-8") as f:
                f.write(json.dumps({"name": "amq-squad", "version": version}) + "\n")
        git(root, "add", "-A")
        git(root, "commit", "-q", "-m", "manifests")
        # The local view of the remote's main, as a fetch leaves it.
        git(root, "update-ref", "refs/remotes/origin/main", "HEAD")

    def test_main_carrying_the_version_passes(self) -> None:
        with tempfile.TemporaryDirectory() as root:
            self.repo(root, "3.0.2", "3.0.2")
            failures: list[str] = []
            CHECK_RELEASE_VERSION.require_marketplace_ref(root, "3.0.2", failures, "origin/main")
            self.assertEqual(failures, [])

    def test_main_on_another_version_fails_closed(self) -> None:
        with tempfile.TemporaryDirectory() as root:
            self.repo(root, "3.0.2", "2.31.0")
            failures: list[str] = []
            CHECK_RELEASE_VERSION.require_marketplace_ref(root, "3.0.2", failures, "origin/main")
            self.assertEqual(len(failures), 1)
            self.assertIn("origin/main:plugins/codex/.codex-plugin/plugin.json: the marketplace serves version '2.31.0', not '3.0.2'", failures[0])

            failures = []
            CHECK_RELEASE_VERSION.require_marketplace_ref(root, "3.0.3", failures, "origin/main")
            self.assertEqual(len(failures), 2)

    def test_a_missing_ref_fails_closed(self) -> None:
        with tempfile.TemporaryDirectory() as root:
            self.repo(root, "3.0.2", "3.0.2")
            git(root, "update-ref", "-d", "refs/remotes/origin/main")
            failures: list[str] = []
            CHECK_RELEASE_VERSION.require_marketplace_ref(root, "3.0.2", failures, "origin/main")
            self.assertEqual(len(failures), 2)
            self.assertTrue(all("the marketplace's ref is unreadable" in f for f in failures), failures)

    def run_main(self, *args: str) -> list[tuple[str, str]]:
        """Run main() on this repository with args, recording the row's calls
        (the row is replaced by a recorder; main's result is not asserted)."""
        repo = os.path.dirname(os.path.dirname(os.path.abspath(SCRIPT)))
        calls: list[tuple[str, str]] = []
        real, argv, cwd = CHECK_RELEASE_VERSION.require_marketplace_ref, sys.argv, os.getcwd()
        CHECK_RELEASE_VERSION.require_marketplace_ref = lambda root, version, failures, ref: calls.append((version, ref))
        sys.argv = [SCRIPT, *args]
        try:
            os.chdir(repo)
            with contextlib.redirect_stdout(io.StringIO()), contextlib.redirect_stderr(io.StringIO()):
                CHECK_RELEASE_VERSION.main()
        finally:
            os.chdir(cwd)
            sys.argv = argv
            CHECK_RELEASE_VERSION.require_marketplace_ref = real
        return calls

    def test_the_row_does_not_run_without_the_flag(self) -> None:
        # CI runs the check on the version-bump PR into v3, when main cannot
        # carry the new version yet: without --marketplace-ref the row is off.
        self.assertFalse(hasattr(CHECK_RELEASE_VERSION, "MARKETPLACE_REF"))
        self.assertEqual(self.run_main("v3.0.2"), [])

    def make_n(self, *extra: str) -> str:
        """`make -n release-check` in this repository (prints, runs nothing)."""
        repo = os.path.dirname(os.path.dirname(os.path.abspath(SCRIPT)))
        env = {k: v for k, v in os.environ.items() if k not in ("MARKETPLACE_REF", "MAKEFLAGS")}
        return subprocess.run(
            ["make", "-n", "-C", repo, "release-check", "VERSION=v3.0.1", *extra],
            check=True, capture_output=True, text=True, env=env,
        ).stdout

    def test_the_makefile_passes_the_flag_only_with_marketplace_ref(self) -> None:
        # CI's make release-check sets no MARKETPLACE_REF: no flag, no row.
        without = [ln for ln in self.make_n().splitlines() if "check-release-version.py" in ln]
        self.assertEqual(len(without), 1, without)
        self.assertNotIn("--marketplace-ref", without[0])
        with_ref = [ln for ln in self.make_n("MARKETPLACE_REF=origin/main").splitlines() if "check-release-version.py" in ln]
        self.assertEqual(len(with_ref), 1, with_ref)
        self.assertIn('--marketplace-ref "origin/main"', with_ref[0])

    def test_the_flag_runs_the_row_with_its_ref(self) -> None:
        self.assertEqual(self.run_main("v3.0.2", "--marketplace-ref", "origin/main"), [("3.0.2", "origin/main")])


if __name__ == "__main__":
    unittest.main()
