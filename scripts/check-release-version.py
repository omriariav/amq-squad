#!/usr/bin/env python3
"""Validate release-facing version metadata before publishing a tag."""

from __future__ import annotations

import html
import json
import os
import re
import sys


VERSION_RE = re.compile(r"^v?([0-9]+\.[0-9]+\.[0-9]+)$")
# Keep the opening-block and field shapes aligned with the Go skill
# frontmatter readers in internal/cli: strict byte-zero fence, LF/CRLF, no
# BOM or leading whitespace, and no fallback to searching the document body.
SKILL_FRONTMATTER_BLOCK_RE = re.compile(
    r"\A---\r?\n(.*?)\r?\n---\r?(?:\n|\Z)",
    re.DOTALL,
)
SKILL_FRONTMATTER_VERSION_RE = re.compile(
    r'^version:[ \t]*"?([0-9]+\.[0-9]+\.[0-9]+)"?',
    re.MULTILINE,
)
# The AMQ floor has one source: doctor's constant, the version below which
# `amq-squad doctor` refuses an installed amq, amq-remote or amq-owner-sign.
# This script reads it from the Go source and never carries a second copy, so
# a raised floor that the docs do not follow fails the release check.
# internal/cli's TestReleaseCheckReadsTheDoctorFloor runs --print-amq-floor
# and compares the result with doctorMinAMQVersion.
AMQ_FLOOR_SOURCE = "internal/cli/doctor.go"
AMQ_FLOOR_RE = re.compile(
    r'^const doctorMinAMQVersion = "([0-9]+\.[0-9]+\.[0-9]+)"$',
    re.MULTILINE,
)


def read_amq_floor(root: str) -> str:
    """Return doctorMinAMQVersion as written in AMQ_FLOOR_SOURCE under root.

    Fails closed (ValueError) when the declaration is missing, repeated or
    not a plain x.y.z literal.
    """
    path = os.path.join(root, AMQ_FLOOR_SOURCE)
    matches = AMQ_FLOOR_RE.findall(read(path))
    if len(matches) != 1:
        raise ValueError(
            f"{AMQ_FLOOR_SOURCE}: want exactly one "
            f"'const doctorMinAMQVersion = \"x.y.z\"', found {len(matches)}"
        )
    return matches[0]


def amq_compatibility_policy(floor: str) -> str:
    """The sentence every release-facing doc carries for the AMQ floor."""
    return f"AMQ {floor} is the minimum supported release"


def read(path: str) -> str:
    with open(path, encoding="utf-8") as f:
        return f.read()


def fail_if_missing(path: str, needle: str, failures: list[str]) -> None:
    if needle not in read(path):
        failures.append(f"{path}: missing {needle!r}")


def fail_if_missing_normalized(path: str, needle: str, failures: list[str]) -> None:
    if normalize_policy_text(needle) not in normalize_policy_text(read(path)):
        failures.append(f"{path}: missing normalized {needle!r}")


def normalize_policy_text(value: str) -> str:
    value = html.unescape(value)
    value = re.sub(r"<[^>]+>", "", value)
    value = value.replace("**", "").replace("__", "").replace("`", "")
    return " ".join(value.split())


def skill_frontmatter_version(value: str) -> str | None:
    block = SKILL_FRONTMATTER_BLOCK_RE.match(value)
    if block is None:
        return None
    marker = SKILL_FRONTMATTER_VERSION_RE.search(block.group(1))
    if marker is None:
        return None
    return marker.group(1)


def require_release_notes(root: str, tag: str, failures: list[str]) -> None:
    release_notes_rel = f"docs/{tag}-release-notes.md"
    release_notes = os.path.join(root, release_notes_rel)
    if not os.path.isfile(release_notes):
        failures.append(f"{release_notes_rel}: missing canonical release notes")
        return

    expected_heading = f"# amq-squad {tag}"
    first_nonempty_line = next(
        (line.strip() for line in read(release_notes).splitlines() if line.strip()),
        "",
    )
    if first_nonempty_line != expected_heading:
        failures.append(
            f"{release_notes_rel}: first heading {first_nonempty_line!r} "
            f"!= {expected_heading!r}"
        )


def main() -> int:
    if sys.argv[1:] == ["--print-amq-floor"]:
        try:
            print(read_amq_floor(os.getcwd()))
        except (OSError, ValueError) as err:
            sys.stderr.write(f"FAIL  {err}\n")
            return 1
        return 0
    if len(sys.argv) != 2:
        sys.stderr.write("usage: check-release-version.py VERSION | --print-amq-floor\n")
        return 2
    m = VERSION_RE.match(sys.argv[1].strip())
    if not m:
        sys.stderr.write("VERSION must look like v2.8.1 or 2.8.1\n")
        return 2

    version = m.group(1)
    tag = "v" + version
    root = os.getcwd()
    failures: list[str] = []

    try:
        amq_floor = read_amq_floor(root)
    except (OSError, ValueError) as err:
        sys.stderr.write(f"FAIL  {err}\n")
        return 1
    policy = amq_compatibility_policy(amq_floor)

    require_release_notes(root, tag, failures)

    mirrors = {
        "claude": "plugins/claude/.claude-plugin/plugin.json",
        "codex": "plugins/codex/.codex-plugin/plugin.json",
    }
    for mirror, manifest_rel in mirrors.items():
        manifest_path = os.path.join(root, manifest_rel)
        manifest_version = str(json.loads(read(manifest_path)).get("version", "")).strip()
        if manifest_version != version:
            failures.append(f"{manifest_rel}: version {manifest_version!r} != {version!r}")

        for skill_id in (
            "wizard", "cli", "orchestrator", "amq-squad",
            "amq-squad-orchestrator", "amq-team-setup", "amq-squad-role-creator",
        ):
            skill_rel = f"plugins/{mirror}/skills/{skill_id}/SKILL.md"
            skill_body = read(os.path.join(root, skill_rel))
            skill_version = skill_frontmatter_version(skill_body)
            if skill_version is None:
                failures.append(f"{skill_rel}: missing frontmatter version")
            elif skill_version != version:
                failures.append(
                    f"{skill_rel}: frontmatter version {skill_version!r} "
                    f"!= {version!r}"
                )

    readme = os.path.join(root, "README.md")
    fail_if_missing(readme, f"go install github.com/omriariav/amq-squad/v3/cmd/amq-squad@{tag}", failures)
    fail_if_missing(readme, f"- `amq` {amq_floor} or later on `PATH`", failures)
    for rel in (
        "README.md",
        "docs/skills.md",
        "plugins/skills-src/cli/SKILL.md",
        "plugins/claude/skills/cli/SKILL.md",
        "plugins/codex/skills/cli/SKILL.md",
    ):
        fail_if_missing_normalized(os.path.join(root, rel), policy, failures)

    readme_html = os.path.join(root, "README.html")
    if os.path.exists(readme_html):
        fail_if_missing(readme_html, f"github.com/omriariav/amq-squad/v3/cmd/amq-squad@{tag}", failures)
        fail_if_missing(
            readme_html,
            f"<li><code>amq</code> {amq_floor} or later on <code>PATH</code>",
            failures,
        )
        fail_if_missing_normalized(readme_html, policy, failures)

    skills_html = os.path.join(root, "docs/skills.html")
    if os.path.exists(skills_html):
        fail_if_missing_normalized(skills_html, policy, failures)

    if failures:
        for failure in failures:
            sys.stderr.write("FAIL  " + failure + "\n")
        return 1

    print(f"release metadata matches {tag}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
