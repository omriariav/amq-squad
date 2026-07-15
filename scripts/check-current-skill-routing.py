#!/usr/bin/env python3
"""Reject stale compatibility-skill teaching in current release surfaces."""

from __future__ import annotations

import re
import sys
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]

REQUIRED = {
    "README.md": ("amq-squad:wizard", "amq-squad:cli", "amq-squad:orchestrator"),
    "README.html": ("amq-squad:wizard", "amq-squad:cli", "amq-squad:orchestrator"),
    "docs/skills.md": ("amq-squad:wizard", "amq-squad:cli", "amq-squad:orchestrator"),
    "docs/skills.html": ("amq-squad:wizard", "amq-squad:cli", "amq-squad:orchestrator"),
    "docs/global-orchestrator-runbook.md": ("amq-squad:orchestrator",),
    "plugins/skills-src/amq-squad/references/team-rules-template.md": (
        "amq-squad:wizard",
        "amq-squad:cli",
        "amq-squad:orchestrator",
    ),
    "plugins/claude/skills/amq-squad/references/team-rules-template.md": (
        "amq-squad:wizard",
        "amq-squad:cli",
        "amq-squad:orchestrator",
    ),
    "plugins/codex/skills/amq-squad/references/team-rules-template.md": (
        "amq-squad:wizard",
        "amq-squad:cli",
        "amq-squad:orchestrator",
    ),
}

PUBLIC_DOCS = (
    "README.md",
    "README.html",
    "docs/skills.md",
    "docs/skills.html",
    "docs/global-orchestrator-runbook.md",
)

FORBIDDEN = (
    (re.compile(r"The two primary skills", re.I), "stale two-skill model"),
    (re.compile(r"/amq-squad:amq-squad-orchestrator\b"), "legacy Claude orchestrator invocation"),
    (re.compile(r"/amq-squad:amq-squad\b"), "legacy Claude router invocation"),
    (re.compile(r"\$amq-squad-orchestrator\b"), "legacy Codex orchestrator invocation"),
    (re.compile(r"\$amq-squad\b"), "legacy Codex router invocation"),
    (re.compile(r"^##\s+`amq-squad-orchestrator`", re.M), "legacy primary-skill heading"),
    (re.compile(r"Role Authoring Inside `amq-squad`", re.I), "legacy role-authoring route"),
    (re.compile(r"Setup inside `amq-squad`", re.I), "legacy setup route"),
)


def main() -> int:
    failures: list[str] = []
    for rel, needles in REQUIRED.items():
        path = ROOT / rel
        if not path.is_file():
            failures.append(f"{rel}: missing current release surface")
            continue
        text = path.read_text(encoding="utf-8")
        for needle in needles:
            if needle not in text:
                failures.append(f"{rel}: missing authoritative route {needle!r}")

    for rel in PUBLIC_DOCS:
        path = ROOT / rel
        if not path.is_file():
            continue
        text = path.read_text(encoding="utf-8")
        for pattern, reason in FORBIDDEN:
            match = pattern.search(text)
            if match:
                line = text.count("\n", 0, match.start()) + 1
                failures.append(f"{rel}:{line}: {reason}: {match.group(0)!r}")

    if failures:
        for failure in failures:
            sys.stderr.write("FAIL  " + failure + "\n")
        return 1

    print("current docs and generated team rules route to wizard/cli/orchestrator")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
