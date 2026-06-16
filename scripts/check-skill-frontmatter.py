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

Exits non-zero (with a per-file report) if any file fails.
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
    if failures:
        sys.stderr.write(f"\n{failures} SKILL.md file(s) have invalid frontmatter.\n")
        return 1
    print(f"\nall {len(files)} SKILL.md frontmatters valid")
    return 0


if __name__ == "__main__":
    sys.exit(main())
