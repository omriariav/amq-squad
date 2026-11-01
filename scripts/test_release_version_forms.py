#!/usr/bin/env python3
"""One version grammar across the three release scripts (brief 3.1.1-2 item 3).

On #833 a version form passed `make release-check` and failed CI, because
scripts/detect-release-ci.py carried its own pattern outside the local check.
This row holds the three scripts that parse a release version to one
grammar: for every form below, scripts/detect-release-ci.py (a manifest's
version and a release branch's), scripts/check-release-version.py (the
VERSION argument and a skill's frontmatter) and
scripts/check-skill-frontmatter.py (a skill's frontmatter marker) all accept
it with the same version, or all refuse it. A form one script accepts and
another refuses fails here, and `make release-check` runs it.

check-skill-frontmatter.py exits without PyYAML, which CI installs after the
release check runs, so its two patterns are read from its source (the
re.compile literals), never by importing it.
"""

from __future__ import annotations

import ast
import importlib.util
import json
import os
import re
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))


def load(name: str, file: str):
    spec = importlib.util.spec_from_file_location(name, os.path.join(HERE, file))
    module = importlib.util.module_from_spec(spec)
    assert spec.loader is not None
    spec.loader.exec_module(module)
    return module


DETECT = load("detect_release_ci", "detect-release-ci.py")
CHECK = load("check_release_version", "check-release-version.py")


def compiled_patterns(file: str, names: tuple[str, ...]) -> dict[str, re.Pattern[str]]:
    """The module-level `NAME = re.compile(<literals>)` patterns of file."""
    tree = ast.parse(open(os.path.join(HERE, file), encoding="utf-8").read())
    out: dict[str, re.Pattern[str]] = {}
    for node in tree.body:
        if not isinstance(node, ast.Assign) or len(node.targets) != 1:
            continue
        target = node.targets[0]
        if not isinstance(target, ast.Name) or target.id not in names:
            continue
        call = node.value
        assert isinstance(call, ast.Call), f"{file}: {target.id} is not re.compile(...)"
        # the pattern is a literal; the flags are re.S / re.M attributes
        flags = 0
        for arg in call.args[1:]:
            for attr in ast.walk(arg):
                if isinstance(attr, ast.Attribute):
                    flags |= getattr(re, attr.attr)
        out[target.id] = re.compile(ast.literal_eval(call.args[0]), flags)
    missing = set(names) - set(out)
    assert not missing, f"{file}: no {sorted(missing)}"
    return out


FRONTMATTER = compiled_patterns("check-skill-frontmatter.py", ("FRONTMATTER_BLOCK", "VERSION_MARKER"))

# Version forms: the accepted grammar (x.y.z, x.y.z-rc.N, ASCII digits) and
# its near misses. Whitespace and quoting are each reader's own framing and
# are not compared.
FORMS = (
    "3.1.0",
    "3.1.1",
    "10.20.30",
    "0.0.0",
    "03.1.0",
    "3.1.0-rc.1",
    "3.1.0-rc.12",
    "3.1",
    "3",
    "3.1.0.1",
    "3.1.0-rc",
    "3.1.0-rc.",
    "3.1.0-rc.1.2",
    "3.1.0-beta.1",
    "3.1.0-RC.1",
    "3.1.0+meta",
    "3.1.0-rc.1+meta",
    "3.1.0-",
    "3..0",
    "a.b.c",
    "٣.١.٠",  # Arabic-Indic digits: Python's \d takes them
    "３.1.0",  # a fullwidth digit
    "3.1.0-rc.١",
    "",
)


def detect_manifest(value: str) -> str | None:
    try:
        return DETECT.manifest_version(json.dumps({"version": value}), "fixture")
    except ValueError:
        return None


def detect_branch(value: str) -> str | None:
    # the version part only: a branch may go on after it (release/v3.1.1-notes),
    # its own framing, so a read that leaves a suffix is no read of the form
    branch = "release/" + value
    m = DETECT.RELEASE_BRANCH_VERSION_RE.fullmatch(branch)
    return m.group(1) if m and m.end(1) == len(branch) else None


def check_argument(value: str) -> str | None:
    m = CHECK.VERSION_RE.match(value.strip())
    return m.group(1) if m else None


def frontmatter(value: str) -> str:
    return f'---\nname: x\nversion: "{value}"\n---\nbody\n'


def check_frontmatter(value: str) -> str | None:
    return CHECK.skill_frontmatter_version(frontmatter(value))


def skill_frontmatter(value: str) -> str | None:
    block = FRONTMATTER["FRONTMATTER_BLOCK"].match(frontmatter(value))
    m = FRONTMATTER["VERSION_MARKER"].search(block.group(1)) if block else None
    return m.group(1) if m else None


READERS = {
    "detect-release-ci.py manifest": detect_manifest,
    "detect-release-ci.py v-manifest": lambda v: detect_manifest("v" + v),
    "detect-release-ci.py branch": detect_branch,
    "detect-release-ci.py v-branch": lambda v: detect_branch("v" + v),
    "check-release-version.py VERSION": check_argument,
    "check-release-version.py vVERSION": lambda v: check_argument("v" + v),
    "check-release-version.py frontmatter": check_frontmatter,
    "check-skill-frontmatter.py frontmatter": skill_frontmatter,
}


def disagreements(readers=READERS, forms=FORMS) -> list[str]:
    out = []
    for form in forms:
        got = {name: read(form) for name, read in readers.items()}
        if len(set(got.values())) > 1:
            out.append(f"{form!r}: " + ", ".join(f"{n} -> {v!r}" for n, v in got.items()))
    return out


class ReleaseVersionFormsTest(unittest.TestCase):
    def test_the_three_scripts_accept_and_refuse_the_same_forms(self) -> None:
        self.assertEqual(disagreements(), [])

    def test_the_grammar_is_x_y_z_and_rc(self) -> None:
        accepted = [f for f in FORMS if detect_manifest(f) is not None]
        self.assertEqual(accepted, ["3.1.0", "3.1.1", "10.20.30", "0.0.0", "03.1.0", "3.1.0-rc.1", "3.1.0-rc.12"])

    def test_a_reader_with_another_grammar_is_named(self) -> None:
        # the row itself: a pattern taking any Unicode digit, as
        # detect-release-ci.py's did before 3.1.1, is a disagreement
        loose = re.compile(r"^v?(\d+\.\d+\.\d+(?:-rc\.\d+)?)$")
        readers = dict(READERS, loose=lambda v: (m.group(1) if (m := loose.fullmatch(v)) else None))
        got = disagreements(readers)
        self.assertTrue(got and all("loose" in line for line in got), got)
        self.assertTrue(any("٣" in line for line in got), got)



def release_check_recipe() -> str:
    """The Makefile's release-check recipe lines."""
    lines = open(os.path.join(HERE, "..", "Makefile"), encoding="utf-8").read().splitlines()
    start = lines.index("release-check:")
    recipe = []
    for line in lines[start + 1 :]:
        if not line.startswith("\t"):
            break
        recipe.append(line)
    return "\n".join(recipe)


class ReleaseCheckRunsCIValidatorsTest(unittest.TestCase):
    def test_release_check_runs_every_script_ci_runs_and_its_test(self) -> None:
        # each scripts/*.py CI's release path runs (the steps from "Detect
        # release validation scope" to "Run release metadata check": today
        # detect-release-ci.py, ahead of make release-check) is run by the
        # local release check too, with its own test when it has one (#833)
        ci = open(os.path.join(HERE, "..", ".github", "workflows", "ci.yml"), encoding="utf-8").read()
        start, end = ci.index("Detect release validation scope"), ci.index("Run release metadata check")
        scripts = sorted(set(re.findall(r"python3 scripts/([A-Za-z0-9_.-]+\.py)", ci[start:end])))
        self.assertIn("detect-release-ci.py", scripts)
        recipe = release_check_recipe()
        for script in scripts:
            self.assertIn(f"scripts/{script} ", recipe, f"make release-check does not run {script}")
            test = "test_" + script.replace("-", "_")
            if os.path.isfile(os.path.join(HERE, test)):
                self.assertIn(f"scripts/{test}", recipe, f"make release-check does not run {test}")
        self.assertIn("scripts/test_release_version_forms.py", recipe)


if __name__ == "__main__":
    unittest.main()
