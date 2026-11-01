#!/usr/bin/env python3
"""Validate release-facing version metadata before publishing a tag."""

from __future__ import annotations

import html
import json
import os
import re
import subprocess
import sys


# A release candidate is x.y.z-rc.N (3.1.0-rc.1); no other prerelease form.
# Its notes live in the base version's file (docs/v3.1.0-release-notes.md),
# whose heading names the candidate.
VERSION_RE = re.compile(r"^v?([0-9]+\.[0-9]+\.[0-9]+(?:-rc\.[0-9]+)?)$")
# Keep the opening-block and field shapes aligned with the Go skill
# frontmatter readers in internal/cli: strict byte-zero fence, LF/CRLF, no
# BOM or leading whitespace, and no fallback to searching the document body.
SKILL_FRONTMATTER_BLOCK_RE = re.compile(
    r"\A---\r?\n(.*?)\r?\n---\r?(?:\n|\Z)",
    re.DOTALL,
)
SKILL_FRONTMATTER_VERSION_RE = re.compile(
    # The whole field value must be the version: after it only the closing
    # quote, blanks, a trailing comment and the line end (CR tolerated), so a
    # suffix such as -rc.1-beta.2 or +meta is not read as the prefix it starts
    # with.
    r'^version:[ \t]*"?([0-9]+\.[0-9]+\.[0-9]+(?:-rc\.[0-9]+)?)"?[ \t]*(?:#[^\r\n]*)?\r?$',
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


# The plugin marketplaces (.claude-plugin/marketplace.json and
# .agents/plugins/marketplace.json) install from this repository's default
# branch, main, so a release whose version main does not carry is not what an
# installer gets (3.0's release check missed it: the marketplace served v2.31
# while the release said 3.0). The owner's A of 2026-10-30: v3 is merged to
# main before every tag from 3.0.1 on.
#
# This row is opt-in: it runs only with --marketplace-ref REF (no default),
# and fails closed when the plugin manifests at REF do not carry the released
# version; a missing ref or an unreadable manifest fails too. CI runs this
# script on the version-bump PR into v3 (.github/workflows/ci.yml, "Run
# release metadata check"), when main cannot carry the new version yet (the
# v3-to-main PR comes after), so the row is not run there. By hand, before
# the tag: `make release-check VERSION=vX.Y.Z MARKETPLACE_REF=origin/main`.
# The enforced pre-tag check is `amq-squad release prepare`'s containment of
# --commit in --branch (default main), before any gate is raised: the tag's
# commit on main implies main carries the version.
MARKETPLACE_MANIFESTS = (
    "plugins/claude/.claude-plugin/plugin.json",
    "plugins/codex/.codex-plugin/plugin.json",
)


def require_marketplace_ref(root: str, version: str, failures: list[str], ref: str) -> None:
    """Fail unless every plugin manifest at ref says version."""
    for manifest_rel in MARKETPLACE_MANIFESTS:
        try:
            # Reading a ref needs no config: the owner's global and system
            # git config are not read.
            raw = subprocess.run(
                ["git", "-C", root, "show", f"{ref}:{manifest_rel}"],
                capture_output=True, text=True, check=True,
                env={**os.environ, "GIT_CONFIG_GLOBAL": os.devnull, "GIT_CONFIG_NOSYSTEM": "1"},
            ).stdout
            at_ref = str(json.loads(raw).get("version", "")).strip()
        except (OSError, subprocess.CalledProcessError, ValueError, AttributeError) as err:
            detail = getattr(err, "stderr", "") or str(err)
            failures.append(
                f"{ref}:{manifest_rel}: the marketplace's ref is unreadable "
                f"({detail.strip()}); merge v3 to main and fetch before the tag"
            )
            continue
        if at_ref != version:
            failures.append(
                f"{ref}:{manifest_rel}: the marketplace serves version "
                f"{at_ref!r}, not {version!r}; merge v3 to main before the tag"
            )


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
    notes_tag = re.sub(r"-rc\.[0-9]+$", "", tag)
    release_notes_rel = f"docs/{notes_tag}-release-notes.md"
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
    args = sys.argv[1:]
    marketplace_ref = None
    if len(args) == 3 and args[1] == "--marketplace-ref" and args[2].strip():
        marketplace_ref = args[2].strip()
    elif len(args) != 1:
        sys.stderr.write(
            "usage: check-release-version.py VERSION [--marketplace-ref REF] | --print-amq-floor\n"
        )
        return 2
    m = VERSION_RE.match(args[0].strip())
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
    if marketplace_ref is not None:
        require_marketplace_ref(root, version, failures, marketplace_ref)

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
