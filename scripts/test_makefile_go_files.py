#!/usr/bin/env python3
"""Prove GO_FILES excludes nested worktrees, and that the exclusion is what does it.

Peer worktrees live under .worktrees/ inside the project. A bare `find .` walks into
them, which made `make fmt-check` fail on another agent's in-progress files and made
`make fmt` rewrite them. See #560.

Each behaviour here is proved against a synthetic offender created for the test, and
the central claim carries a removal proof that varies exactly one thing: the same find
command with and without the exclusion clause.
"""

import hashlib
import os
import re
import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]

# Deliberately misformatted: gofmt -l reports it, and it is not valid layout for any
# formatter setting, so the signal cannot come from a gofmt version difference.
BAD_GO = "package  x\nfunc  F( )  {\n\t\t x:=1\n_ = x\n}\n"


def make(*targets, env=None):
    return subprocess.run(
        ["make", *targets],
        cwd=ROOT, capture_output=True, text=True,
        env={**os.environ, **(env or {})},
    )


def go_files(exclude_worktrees: bool) -> list[str]:
    """Reproduce the Makefile's GO_FILES computation, with the exclusion as the variable."""
    cmd = ["find", ".", "-name", "*.go", "-not", "-path", "./vendor/*"]
    if exclude_worktrees:
        cmd += ["-not", "-path", "./.worktrees/*"]
    out = subprocess.run(cmd, cwd=ROOT, capture_output=True, text=True).stdout
    return [line for line in out.splitlines() if line]


def digest(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


class PeerWorktreeFile:
    """A synthetic misformatted .go file at the path a nested worktree would occupy."""

    def __init__(self, name="proof-560"):
        self.dir = ROOT / ".worktrees" / name / "internal" / "cli"
        self.path = self.dir / "bad.go"
        self.created_root = ROOT / ".worktrees" / name

    def __enter__(self):
        if self.created_root.exists():
            raise RuntimeError(f"{self.created_root} already exists; refusing to clobber it")
        self.dir.mkdir(parents=True)
        self.path.write_text(BAD_GO)
        # A test whose fixture does not actually reproduce the condition proves nothing.
        stray = subprocess.run(
            ["gofmt", "-l", str(self.path)], capture_output=True, text=True
        ).stdout.strip()
        if not stray:
            raise RuntimeError("fixture is not misformatted; gofmt -l reports it clean")
        return self

    def __exit__(self, *exc):
        shutil.rmtree(self.created_root, ignore_errors=True)
        return False


class GoFilesScope(unittest.TestCase):
    def test_peer_worktree_file_is_excluded(self):
        with PeerWorktreeFile() as peer:
            rel = "./" + str(peer.path.relative_to(ROOT))
            self.assertNotIn(rel, go_files(exclude_worktrees=True))

    def test_removal_proof_the_exclusion_is_what_excludes_it(self):
        """Vary exactly one thing: the -not -path './.worktrees/*' clause.

        Without this, `test_peer_worktree_file_is_excluded` would still pass if find
        happened never to reach the path for some unrelated reason.
        """
        with PeerWorktreeFile() as peer:
            rel = "./" + str(peer.path.relative_to(ROOT))
            self.assertIn(rel, go_files(exclude_worktrees=False),
                          "without the exclusion the peer file must be IN scope")
            self.assertNotIn(rel, go_files(exclude_worktrees=True),
                             "with the exclusion it must be OUT of scope")

    def test_fmt_check_passes_despite_a_misformatted_peer_file(self):
        with PeerWorktreeFile():
            result = make("fmt-check")
            self.assertEqual(result.returncode, 0,
                             f"fmt-check must ignore peer worktrees\n{result.stderr}")

    def test_fmt_leaves_the_peer_file_byte_identical(self):
        """`make fmt` writing to another agent's checkout is the severe half of #560."""
        clean = subprocess.run(
            ["gofmt", "-l", *go_files(exclude_worktrees=True)],
            cwd=ROOT, capture_output=True, text=True,
        ).stdout.strip()
        # Not skipped: running `make fmt` against an already-dirty tree would mutate
        # real files, so a dirty tree is a broken precondition, not an absent one.
        self.assertEqual(clean, "", f"precondition: tree must be gofmt-clean, found:\n{clean}")

        with PeerWorktreeFile() as peer:
            before = digest(peer.path)
            result = make("fmt")
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(digest(peer.path), before,
                             "make fmt rewrote a file belonging to a peer worktree")

    def test_fmt_check_prints_the_offending_files(self):
        """`@test -z "$(shell ...)"` swallowed the list, so a failure printed only
        'Error 1' and the developer had to re-derive which file was at fault."""
        with tempfile.NamedTemporaryFile(
            mode="w", suffix=".go", prefix="zz_fmt_offender_", dir=ROOT, delete=False
        ) as f:
            offender = Path(f.name)
            f.write(BAD_GO)
        try:
            result = make("fmt-check")
            self.assertNotEqual(result.returncode, 0, "a real offender must fail fmt-check")
            combined = result.stdout + result.stderr
            self.assertIn(offender.name, combined,
                          f"fmt-check must name the offending file; got:\n{combined}")
        finally:
            offender.unlink(missing_ok=True)

    def test_fmt_check_fails_when_gofmt_itself_fails(self):
        """A check that passes when its instrument breaks is worse than no check.

        `offenders="$(gofmt -l ...)"` drops gofmt's own exit status, and make recipes
        run sh WITHOUT -e, so the next command runs regardless. If gofmt errors while
        printing nothing to stdout -- a stat error on a missing path, or a path with
        spaces word-splitting into invalid arguments -- the offender list is empty and
        fmt-check reports success having formatted-checked nothing.

        The GO_FILES override is a clean one-variable fixture: a nonexistent file makes
        gofmt fail WITHOUT producing an offender, separating "instrument broke" from
        "found a problem".
        """
        result = make("fmt-check", "GO_FILES=./definitely-missing-560.go")
        self.assertNotEqual(
            result.returncode, 0,
            "fmt-check must fail when gofmt cannot run; it reported success instead",
        )
        combined = result.stdout + result.stderr
        self.assertIn("gofmt -l failed", combined,
                      f"the failure must say the check could not run; got:\n{combined}")

    def test_exclusion_is_path_based_not_git_tracked(self):
        """Untracked new .go files must stay in coverage, so the fix cannot be
        `git ls-files`: a newly written, not-yet-added file is exactly when formatting
        feedback matters most."""
        makefile = (ROOT / "Makefile").read_text()
        go_files_line = next(
            line for line in makefile.splitlines() if line.startswith("GO_FILES")
        )
        self.assertIn(".worktrees", go_files_line)
        self.assertNotIn("git ls-files", go_files_line)

    def test_untracked_go_file_at_project_root_stays_in_scope(self):
        """The companion property, proved directly rather than inferred from the rule."""
        with tempfile.NamedTemporaryFile(
            mode="w", suffix=".go", prefix="zz_untracked_", dir=ROOT, delete=False
        ) as f:
            untracked = Path(f.name)
            f.write("package main\n")
        try:
            tracked = subprocess.run(
                ["git", "ls-files", "--error-unmatch", untracked.name],
                cwd=ROOT, capture_output=True, text=True,
            )
            self.assertNotEqual(tracked.returncode, 0, "fixture must be untracked")
            self.assertIn("./" + untracked.name, go_files(exclude_worktrees=True))
        finally:
            untracked.unlink(missing_ok=True)


if __name__ == "__main__":
    unittest.main(verbosity=2)
