#!/usr/bin/env python3
"""Generate plugin skill mirrors from canonical skill sources.

Canonical sources live under plugins/skills-src/<skill>/SKILL.md. They carry the
shared body and neutral name/description frontmatter. This script writes the
Claude and Codex plugin variants with each platform's frontmatter while keeping
the body identical.
"""

import argparse
import difflib
import json
import re
import sys
from pathlib import Path

try:
    import yaml
except ImportError:
    sys.stderr.write(
        "PyYAML is required for skill generation: pip3 install pyyaml\n"
    )
    sys.exit(2)


ROOT = Path(__file__).resolve().parents[1]
SOURCE_ROOT = ROOT / "plugins" / "skills-src"
MIRRORS = ("claude", "codex")
SKILLS = (
    "wizard",
    "cli",
    "orchestrator",
    "amq-squad",
    "amq-squad-orchestrator",
    "amq-team-setup",
    "amq-squad-role-creator",
)

FRONTMATTER = re.compile(r"^---[ \t]*\r?\n(.*?)\r?\n---[ \t]*(?:\r?\n|\Z)", re.S)

CLAUDE_META = {
    "wizard": {
        "allowed-tools": "Bash, Read, Write, Edit, MultiEdit, Glob, Grep, WebFetch",
        "argument-hint": "[request | goal | brief | rules | roles | profile | launch]",
        "user-invocable": True,
        "trigger": "/wizard",
    },
    "cli": {
        "allowed-tools": "Bash, Read, Write, Edit, MultiEdit, Glob, Grep",
        "argument-hint": "[status | doctor | task | gate | resume | down | amq]",
        "user-invocable": True,
        "trigger": "/cli",
    },
    "orchestrator": {
        "allowed-tools": "Bash, Read, Write, Edit, Glob, Grep",
        "argument-hint": "[compose | start | dispatch | status | review | recover]",
        "user-invocable": True,
        "trigger": "/orchestrator",
    },
    "amq-squad": {
        "allowed-tools": "Bash, Read, Write, Edit, MultiEdit, Glob, Grep",
        "argument-hint": "[drain | review | handoff | status | start | focus | send | resume | down | doctor]",
        "user-invocable": True,
        "trigger": "/amq-squad",
    },
    "amq-squad-orchestrator": {
        "allowed-tools": "Bash, Read, Write, Edit, Glob, Grep",
        "argument-hint": "[compose | start | dispatch | status | coordinate | recover | example]",
        "user-invocable": True,
        "trigger": "/amq-squad-orchestrator",
    },
    "amq-team-setup": {
        "allowed-tools": "Bash, Read, Write, Edit, MultiEdit, Glob, Grep, WebFetch",
        "argument-hint": "[setup | brief | roles]",
        "user-invocable": True,
        "trigger": "/amq-team-setup",
    },
    "amq-squad-role-creator": {
        "allowed-tools": "Bash, Read, Write, Edit, Glob, Grep",
        "argument-hint": "[role-id] [codex|claude]",
        "user-invocable": True,
        "trigger": "/amq-squad-role-creator",
    },
}


COMPANION_FILES = ("LEARNINGS.md",)

MANIFESTS = {
    "claude": ".claude-plugin/plugin.json",
    "codex": ".codex-plugin/plugin.json",
}

# release-please rewrites the value on the line carrying this marker, so a release bumps
# the manifest and every generated frontmatter together.
RELEASE_PLEASE_MARKER = "# x-release-please-version"


def split_frontmatter(text: str, path: Path) -> tuple[dict, str]:
    match = FRONTMATTER.match(text)
    if not match:
        raise ValueError(f"{path}: missing frontmatter")
    data = yaml.safe_load(match.group(1))
    if not isinstance(data, dict):
        raise ValueError(f"{path}: frontmatter must be a mapping")
    for key in ("name", "description"):
        if not str(data.get(key, "")).strip():
            raise ValueError(f"{path}: missing {key}")
    return data, text[match.end() :].lstrip("\r\n")


def scalar(value) -> str:
    if isinstance(value, bool):
        return "true" if value else "false"
    return json.dumps(str(value), ensure_ascii=False)


def manifest_version(mirror: str) -> str:
    """The mirror's shipped version, read from its plugin manifest.

    Single source of truth. This identity used to be a hardcoded sentence in each of the
    7 skill bodies: 7 edits per release and 7 chances to drift. Deriving it here means a
    release bumps the manifest alone.
    """
    manifest = ROOT / "plugins" / mirror / MANIFESTS[mirror]
    return str(json.loads(manifest.read_text(encoding="utf-8")).get("version", "")).strip()


def render_frontmatter(skill: str, mirror: str, data: dict) -> str:
    fields = {
        "name": data["name"],
        "description": data["description"],
        "version": manifest_version(mirror),
    }
    if mirror == "claude":
        fields.update(CLAUDE_META[skill])
    lines = ["---"]
    for key, value in fields.items():
        suffix = f"  {RELEASE_PLEASE_MARKER}" if key == "version" else ""
        lines.append(f"{key}: {scalar(value)}{suffix}")
    lines.append("---")
    lines.append("")
    return "\n".join(lines)


def generated_skill_text(skill: str, mirror: str) -> str:
    source = SOURCE_ROOT / skill / "SKILL.md"
    data, body = split_frontmatter(source.read_text(encoding="utf-8"), source)
    return render_frontmatter(skill, mirror, data) + body


def sync_bytes(source: Path, dest: Path, check: bool, stale: list[str]) -> None:
    expected = source.read_bytes()
    actual = dest.read_bytes() if dest.exists() else None
    if actual == expected:
        return
    display = str(dest.relative_to(ROOT))
    if check:
        stale.append(display)
        return
    dest.parent.mkdir(parents=True, exist_ok=True)
    dest.write_bytes(expected)


def sync_references(skill: str, mirror: str, check: bool, stale: list[str]) -> None:
    source_refs = SOURCE_ROOT / skill / "references"
    if not source_refs.is_dir():
        return
    dest_refs = ROOT / "plugins" / mirror / "skills" / skill / "references"
    for source in sorted(source_refs.rglob("*")):
        if not source.is_file():
            continue
        sync_bytes(source, dest_refs / source.relative_to(source_refs), check, stale)


def sync_companions(skill: str, mirror: str, check: bool, stale: list[str]) -> None:
    """Mirror skill-owned files that sit beside SKILL.md rather than under references/.

    SKILL.md links these, so a file present in skills-src but absent from a mirror
    ships a broken link. Anything a SKILL.md may reference belongs here.
    """
    for name in COMPANION_FILES:
        source = SOURCE_ROOT / skill / name
        if not source.is_file():
            continue
        sync_bytes(source, ROOT / "plugins" / mirror / "skills" / skill / name, check, stale)


def write_or_check(path: Path, expected: str, check: bool, stale: list[str]) -> None:
    actual = path.read_text(encoding="utf-8") if path.exists() else None
    if actual == expected:
        return
    display = str(path.relative_to(ROOT))
    if check:
        stale.append(display)
        if actual is not None:
            diff = difflib.unified_diff(
                actual.splitlines(keepends=True),
                expected.splitlines(keepends=True),
                fromfile=display,
                tofile=f"{display} (generated)",
            )
            sys.stderr.writelines(diff)
        else:
            sys.stderr.write(f"{display}: missing generated file\n")
        return
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(expected, encoding="utf-8")


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--check", action="store_true", help="fail if generated files are stale")
    args = parser.parse_args()

    stale: list[str] = []
    for skill in SKILLS:
        for mirror in MIRRORS:
            dest = ROOT / "plugins" / mirror / "skills" / skill / "SKILL.md"
            write_or_check(dest, generated_skill_text(skill, mirror), args.check, stale)
            sync_references(skill, mirror, args.check, stale)
            sync_companions(skill, mirror, args.check, stale)

    if stale:
        sys.stderr.write("\nGenerated skill files are stale; run `make skills-generate`.\n")
        for path in stale:
            sys.stderr.write(f"  - {path}\n")
        return 1
    if not args.check:
        print("generated plugin skill mirrors")
    return 0


if __name__ == "__main__":
    sys.exit(main())
