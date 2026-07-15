#!/usr/bin/env python3
"""Validate release-facing version metadata before publishing a tag."""

from __future__ import annotations

import json
import os
import re
import sys


VERSION_RE = re.compile(r"^v?([0-9]+\.[0-9]+\.[0-9]+)$")
SKILL_MARKER_RE = re.compile(r"Skill version:\s*([0-9]+\.[0-9]+\.[0-9]+)")
AMQ_MIN_VERSION = "0.42.1"
AMQ_TESTED_CURRENT_VERSION = "0.43.1"
AMQ_COMPATIBILITY_POLICY = (
    f"The minimum {AMQ_MIN_VERSION} compatibility floor is unchanged. "
    f"This release is explicitly validated against pinned {AMQ_TESTED_CURRENT_VERSION}; "
    "latest remains a forward-compatibility canary."
)


def read(path: str) -> str:
    with open(path, encoding="utf-8") as f:
        return f.read()


def fail_if_missing(path: str, needle: str, failures: list[str]) -> None:
    if needle not in read(path):
        failures.append(f"{path}: missing {needle!r}")


def fail_if_missing_normalized(path: str, needle: str, failures: list[str]) -> None:
    if " ".join(needle.split()) not in " ".join(read(path).split()):
        failures.append(f"{path}: missing normalized {needle!r}")


def main() -> int:
    if len(sys.argv) != 2:
        sys.stderr.write("usage: check-release-version.py VERSION\n")
        return 2
    m = VERSION_RE.match(sys.argv[1].strip())
    if not m:
        sys.stderr.write("VERSION must look like v2.8.1 or 2.8.1\n")
        return 2

    version = m.group(1)
    tag = "v" + version
    root = os.getcwd()
    failures: list[str] = []

    mirrors = {
        "claude": "plugins/claude/.claude-plugin/plugin.json",
        "codex": "plugins/codex/.codex-plugin/plugin.json",
    }
    for mirror, manifest_rel in mirrors.items():
        manifest_path = os.path.join(root, manifest_rel)
        manifest_version = str(json.loads(read(manifest_path)).get("version", "")).strip()
        if manifest_version != version:
            failures.append(f"{manifest_rel}: version {manifest_version!r} != {version!r}")

        skill_rel = f"plugins/{mirror}/skills/amq-squad/SKILL.md"
        skill_body = read(os.path.join(root, skill_rel))
        marker = SKILL_MARKER_RE.search(skill_body)
        if not marker:
            failures.append(f"{skill_rel}: missing Skill version marker")
        elif marker.group(1) != version:
            failures.append(f"{skill_rel}: Skill version {marker.group(1)!r} != {version!r}")
        expected_echo = f"amq-squad skill {tag}"
        if expected_echo not in skill_body:
            failures.append(f"{skill_rel}: missing startup echo {expected_echo!r}")

    readme = os.path.join(root, "README.md")
    fail_if_missing(readme, f"go install github.com/omriariav/amq-squad/v2/cmd/amq-squad@{tag}", failures)
    fail_if_missing(readme, f"- `amq` {AMQ_MIN_VERSION}+ on `PATH`", failures)
    for rel in (
        "README.md",
        "docs/skills.md",
        "docs/global-orchestrator-runbook.md",
        "plugins/skills-src/amq-squad/SKILL.md",
        "plugins/claude/skills/amq-squad/SKILL.md",
        "plugins/codex/skills/amq-squad/SKILL.md",
    ):
        fail_if_missing_normalized(os.path.join(root, rel), AMQ_COMPATIBILITY_POLICY, failures)

    readme_html = os.path.join(root, "README.html")
    if os.path.exists(readme_html):
        fail_if_missing(readme_html, f"github.com/omriariav/amq-squad/v2/cmd/amq-squad@{tag}", failures)
        fail_if_missing(
            readme_html,
            f"<li><code>amq</code> {AMQ_MIN_VERSION}+ on <code>PATH</code></li>",
            failures,
        )
        fail_if_missing_normalized(readme_html, AMQ_COMPATIBILITY_POLICY, failures)

    skills_html = os.path.join(root, "docs/skills.html")
    if os.path.exists(skills_html):
        fail_if_missing_normalized(skills_html, AMQ_COMPATIBILITY_POLICY, failures)

    if failures:
        for failure in failures:
            sys.stderr.write("FAIL  " + failure + "\n")
        return 1

    print(f"release metadata matches {tag}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
