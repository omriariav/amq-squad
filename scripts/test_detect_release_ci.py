#!/usr/bin/env python3

import importlib.util
import os
import unittest


SCRIPT = os.path.join(os.path.dirname(__file__), "detect-release-ci.py")
SPEC = importlib.util.spec_from_file_location("detect_release_ci", SCRIPT)
DETECT_RELEASE_CI = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
SPEC.loader.exec_module(DETECT_RELEASE_CI)


def versions(claude: str, codex: str) -> dict[str, str]:
    return {
        DETECT_RELEASE_CI.MANIFESTS[0]: claude,
        DETECT_RELEASE_CI.MANIFESTS[1]: codex,
    }


class DetectReleaseCITest(unittest.TestCase):
    def test_manifest_version_bump_runs_release_check(self) -> None:
        got = DETECT_RELEASE_CI.decide_release(
            "fix/release-metadata",
            versions("2.29.2", "2.29.2"),
            versions("2.29.3", "2.29.3"),
        )
        self.assertEqual(got, (True, "v2.29.3", "manifest_version_bump"))

    def test_release_branch_runs_check_with_branch_version(self) -> None:
        got = DETECT_RELEASE_CI.decide_release(
            "release/v2.29.3",
            versions("2.29.2", "2.29.2"),
            versions("2.29.2", "2.29.2"),
        )
        self.assertEqual(got, (True, "v2.29.3", "release_branch"))

    def test_non_release_pr_is_unaffected(self) -> None:
        got = DETECT_RELEASE_CI.decide_release(
            "fix/send-resolver",
            versions("2.29.2", "2.29.2"),
            versions("2.29.2", "2.29.2"),
        )
        self.assertEqual(got, (False, "", "non_release"))

    def test_one_sided_manifest_bump_still_runs_and_fails_closed_later(self) -> None:
        got = DETECT_RELEASE_CI.decide_release(
            "fix/manifest",
            versions("2.29.2", "2.29.2"),
            versions("2.29.3", "2.29.2"),
        )
        self.assertEqual(got, (True, "v2.29.2", "manifest_version_bump"))


if __name__ == "__main__":
    unittest.main()
