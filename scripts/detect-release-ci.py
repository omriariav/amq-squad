#!/usr/bin/env python3
"""Detect whether a pull request needs the VERSION-bound release check."""

from __future__ import annotations

import argparse
import json
import pathlib
import re
import subprocess
import sys
from collections.abc import Mapping


MANIFESTS = (
    "plugins/claude/.claude-plugin/plugin.json",
    "plugins/codex/.codex-plugin/plugin.json",
)
VERSION_SOURCE = "plugins/codex/.codex-plugin/plugin.json"
# A release candidate is x.y.z-rc.N (3.1.0-rc.1); no other prerelease form,
# as scripts/check-release-version.py reads it. ASCII digits only, as there:
# Python's \d also takes any Unicode digit (brief 3.1.1-2 item 3;
# scripts/test_release_version_forms.py holds the three scripts to one
# grammar).
VERSION_RE = re.compile(r"^v?([0-9]+\.[0-9]+\.[0-9]+(?:-rc\.[0-9]+)?)$")
RELEASE_BRANCH_VERSION_RE = re.compile(
    r"^release/v?([0-9]+\.[0-9]+\.[0-9]+(?:-rc\.[0-9]+)?)(?:[-/].*)?$"
)


def manifest_version(raw: str, source: str) -> str:
    try:
        value = str(json.loads(raw).get("version", "")).strip()
    except (AttributeError, json.JSONDecodeError) as exc:
        raise ValueError(f"{source}: invalid plugin manifest JSON") from exc
    match = VERSION_RE.fullmatch(value)
    if not match:
        raise ValueError(f"{source}: invalid plugin version {value!r}")
    return match.group(1)


def decide_release(
    head_ref: str,
    base_versions: Mapping[str, str],
    head_versions: Mapping[str, str],
) -> tuple[bool, str, str]:
    release_branch = head_ref.startswith("release/")
    manifest_bump = any(
        base_versions[path] != head_versions[path] for path in MANIFESTS
    )
    if not release_branch and not manifest_bump:
        return False, "", "non_release"

    branch_match = RELEASE_BRANCH_VERSION_RE.fullmatch(head_ref)
    if branch_match:
        version = branch_match.group(1)
    else:
        version = head_versions[VERSION_SOURCE]

    if release_branch and manifest_bump:
        reason = "release_branch_and_manifest_version_bump"
    elif release_branch:
        reason = "release_branch"
    else:
        reason = "manifest_version_bump"
    return True, "v" + version, reason


def read_head_versions(root: pathlib.Path) -> dict[str, str]:
    return {
        path: manifest_version((root / path).read_text(encoding="utf-8"), path)
        for path in MANIFESTS
    }


def read_base_versions(root: pathlib.Path, base_sha: str) -> dict[str, str]:
    versions: dict[str, str] = {}
    for path in MANIFESTS:
        result = subprocess.run(
            ["git", "-C", str(root), "show", f"{base_sha}:{path}"],
            check=False,
            capture_output=True,
            text=True,
        )
        if result.returncode != 0:
            detail = result.stderr.strip() or "git show failed"
            raise RuntimeError(f"read base manifest {path}: {detail}")
        versions[path] = manifest_version(result.stdout, f"{base_sha}:{path}")
    return versions


def check_version(root: pathlib.Path, version: str) -> list[str]:
    """This script's own parse of VERSION and of the head manifests, as CI
    runs it: `make release-check` calls it, so a version form this script
    refuses fails the local check too (brief 3.1.1-2 item 3, #833)."""
    match = VERSION_RE.fullmatch(version.strip())
    if not match:
        return [f"VERSION {version!r} is not x.y.z or x.y.z-rc.N"]
    try:
        head = read_head_versions(root)
    except (OSError, ValueError) as exc:
        return [str(exc)]
    return [
        f"{path}: version {got!r} != {match.group(1)!r}"
        for path, got in head.items()
        if got != match.group(1)
    ]


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--base", help="pull request base commit SHA")
    parser.add_argument("--head-ref", help="pull request head branch")
    parser.add_argument("--repo", default=".", help="repository root")
    parser.add_argument(
        "--check-version",
        metavar="VERSION",
        help="check VERSION and the head manifests with this script's parse (make release-check)",
    )
    args = parser.parse_args()

    root = pathlib.Path(args.repo).resolve()
    if args.check_version is not None:
        errors = check_version(root, args.check_version)
        for err in errors:
            sys.stderr.write(f"release CI version parse failed: {err}\n")
        return 1 if errors else 0
    if not args.base or not args.head_ref:
        parser.error("--base and --head-ref are required")
    try:
        head_versions = read_head_versions(root)
        base_versions = read_base_versions(root, args.base)
        run, version, reason = decide_release(
            args.head_ref, base_versions, head_versions
        )
    except (OSError, RuntimeError, ValueError) as exc:
        sys.stderr.write(f"release CI detection failed: {exc}\n")
        return 1

    print(f"run_release_check={'true' if run else 'false'}")
    print(f"release_version={version}")
    print(f"release_reason={reason}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
