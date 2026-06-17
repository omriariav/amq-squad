#!/usr/bin/env python3
"""Validate the YAML frontmatter of every plugin SKILL.md.

A single unquoted ``: `` (colon-space) in a `description:` value is enough to
make the whole frontmatter invalid YAML, which makes the Claude and Codex skill
loaders silently SKIP the skill. That shipped once (the amq-squad-orchestrator
skill in 1.9.1) and broke goal-first orchestration on Codex without any build
error. This guard runs in `make ci` so it can never ship again.

Checks, per plugins/*/skills/*/SKILL.md:
  - a `---\\n...\\n---` frontmatter block exists,
  - it parses as a YAML mapping,
  - `name` and `description` are present and non-empty.

Also checks that the `Skill version: X.Y.Z` marker in each mirror's amq-squad
skill (which the agent echoes on startup) matches that mirror's plugin manifest
version, so the echoed version can never silently drift from the release.

Exits non-zero (with a per-file report) if any check fails.
"""
import glob
import os
import re
import sys

try:
    import yaml
except ImportError:
    sys.stderr.write(
        "PyYAML is required for the SKILL.md frontmatter check: pip3 install pyyaml\n"
    )
    sys.exit(2)

# Closing fence must be `---` alone on its own line (optional trailing spaces),
# so a `---`-prefixed line inside the body cannot be mistaken for the delimiter.
# CRLF tolerant.
FRONTMATTER = re.compile(r"^---[ \t]*\r?\n(.*?)\r?\n---[ \t]*(?:\r?\n|\Z)", re.S)

# Codex's frontmatter loader REJECTS a description longer than this many
# characters — the skill then silently fails to load (claude has no such cap).
# That shipped once unnoticed because nothing checked length here, so a long
# description loaded fine on claude but vanished on codex. Guard it for both.
DESCRIPTION_MAX = 1024


def check(path):
    """Return an error string for `path`, or None when it is valid."""
    # utf-8-sig strips a leading BOM so a BOM'd-but-valid file is not false-failed.
    text = open(path, encoding="utf-8-sig").read()
    m = FRONTMATTER.match(text)
    if not m:
        return "missing `---` frontmatter block"
    try:
        data = yaml.safe_load(m.group(1))
    except yaml.YAMLError as exc:
        detail = str(exc).replace("\n", " ")
        return f"invalid YAML: {detail}"
    if not isinstance(data, dict):
        return "frontmatter is not a YAML mapping"
    for key in ("name", "description"):
        # `key:` with no value parses to None, and `.get(key, "")` would miss it
        # because the key IS present -> check the value explicitly so an empty
        # name/description cannot false-pass (str(None) == "None" is truthy).
        value = data.get(key)
        if value is None or not str(value).strip():
            return f"missing or empty `{key}`"
    desc = str(data.get("description", ""))
    if len(desc) > DESCRIPTION_MAX:
        return (
            f"description is {len(desc)} chars; exceeds the {DESCRIPTION_MAX}-char cap "
            f"codex enforces (over it, the skill silently fails to load on codex)"
        )
    return None


# Each mirror's amq-squad skill carries a "Skill version: X.Y.Z" marker that the
# agent echoes on startup. It must match that mirror's plugin manifest version so
# the echoed version is trustworthy, not drifted.
VERSION_MARKER = re.compile(r"Skill version:\s*([0-9]+\.[0-9]+\.[0-9]+)")
VERSIONED_SKILL = "amq-squad"  # skill id (dir) that must carry the marker
MANIFEST = {
    "claude": ".claude-plugin/plugin.json",
    "codex": ".codex-plugin/plugin.json",
}


def check_version_marker(root, mirror):
    """Return an error string if the amq-squad skill marker != plugin manifest."""
    skill = os.path.join(root, "plugins", mirror, "skills", VERSIONED_SKILL, "SKILL.md")
    manifest = os.path.join(root, "plugins", mirror, MANIFEST[mirror])
    if not (os.path.isfile(skill) and os.path.isfile(manifest)):
        return None  # mirror not present; nothing to compare
    m = VERSION_MARKER.search(open(skill, encoding="utf-8-sig").read())
    if not m:
        return f"{os.path.relpath(skill, root)}: missing `Skill version: X.Y.Z` marker"
    import json

    want = str(json.load(open(manifest, encoding="utf-8")).get("version", "")).strip()
    if m.group(1) != want:
        return (
            f"{os.path.relpath(skill, root)}: marker version {m.group(1)} != "
            f"manifest version {want} ({os.path.relpath(manifest, root)})"
        )
    return None


def main():
    root = sys.argv[1] if len(sys.argv) > 1 else "."
    files = sorted(glob.glob(os.path.join(root, "plugins/*/skills/*/SKILL.md")))
    if not files:
        sys.stderr.write("no SKILL.md files found under plugins/*/skills/\n")
        return 1
    failures = 0
    for f in files:
        err = check(f)
        rel = os.path.relpath(f, root)
        if err:
            failures += 1
            sys.stderr.write(f"FAIL  {rel}\n      {err}\n")
        else:
            print(f"ok    {rel}")
    for mirror in ("claude", "codex"):
        err = check_version_marker(root, mirror)
        if err:
            failures += 1
            sys.stderr.write(f"FAIL  skill-version marker\n      {err}\n")
    if failures:
        sys.stderr.write(f"\n{failures} skill check(s) failed.\n")
        return 1
    print(f"\nall {len(files)} SKILL.md frontmatters valid; version markers match manifests")
    return 0


if __name__ == "__main__":
    sys.exit(main())
