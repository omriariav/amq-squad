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

    def test_release_candidate_bump_runs_check_with_the_candidate_version(self) -> None:
        got = DETECT_RELEASE_CI.decide_release(
            "v3-31-rc1",
            versions("3.0.2", "3.0.2"),
            versions("3.1.0-rc.1", "3.1.0-rc.1"),
        )
        self.assertEqual(got, (True, "v3.1.0-rc.1", "manifest_version_bump"))
        got = DETECT_RELEASE_CI.decide_release(
            "release/v3.1.0-rc.1",
            versions("3.0.2", "3.0.2"),
            versions("3.0.2", "3.0.2"),
        )
        self.assertEqual(got, (True, "v3.1.0-rc.1", "release_branch"))
        self.assertEqual(
            DETECT_RELEASE_CI.manifest_version('{"version": "3.1.0-rc.1"}', "m"), "3.1.0-rc.1"
        )
        for malformed in ("3.1.0-beta.1", "3.1.0-rc.1-beta.2", "3.1.0-rc.1+meta"):
            with self.assertRaises(ValueError, msg=malformed):
                DETECT_RELEASE_CI.manifest_version('{"version": "%s"}' % malformed, "m")

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

    def test_check_version_is_this_scripts_parse_of_version_and_manifests(self) -> None:
        # brief 3.1.1-2 item 3: make release-check runs it, so a form this
        # script refuses fails the local check as it fails CI (#833)
        import json
        import pathlib
        import tempfile

        with tempfile.TemporaryDirectory() as tmp:
            root = pathlib.Path(tmp)

            def manifests(version: str) -> None:
                for rel in DETECT_RELEASE_CI.MANIFESTS:
                    path = root / rel
                    path.parent.mkdir(parents=True, exist_ok=True)
                    path.write_text(json.dumps({"version": version}), encoding="utf-8")

            manifests("3.1.1-rc.1")
            self.assertEqual(DETECT_RELEASE_CI.check_version(root, "v3.1.1-rc.1"), [])
            self.assertEqual(DETECT_RELEASE_CI.check_version(root, "3.1.1-rc.1"), [])
            self.assertEqual(len(DETECT_RELEASE_CI.check_version(root, "v3.1.1")), 2)
            self.assertTrue(DETECT_RELEASE_CI.check_version(root, "v\u0663.1.1"))
            manifests("3.1.1-beta.1")
            self.assertTrue(DETECT_RELEASE_CI.check_version(root, "v3.1.1-beta.1"))


if __name__ == "__main__":
    unittest.main()
