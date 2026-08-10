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
VERSION_RE = re.compile(r"^v?(\d+\.\d+\.\d+)$")
RELEASE_BRANCH_VERSION_RE = re.compile(
    r"^release/v?(\d+\.\d+\.\d+)(?:[-/].*)?$"
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


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--base", required=True, help="pull request base commit SHA")
    parser.add_argument("--head-ref", required=True, help="pull request head branch")
    parser.add_argument("--repo", default=".", help="repository root")
    args = parser.parse_args()

    root = pathlib.Path(args.repo).resolve()
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
