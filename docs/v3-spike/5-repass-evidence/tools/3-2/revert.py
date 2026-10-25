#!/usr/bin/env python3
"""Revert check: for each case, undo one rule in the source, run the test row,
confirm it fails on its assertion (a build failure does not count), restore
the source and confirm the row passes.

usage: revert.py WORKTREE CASES.py OUT
CASES.py defines CASES: (name, file, old, new, test_regexp, package), or
(name, [(file, old, new), ...], test_regexp, package) for a rule written in
more than one place. Same harness as docs/v3-spike/2b4-evidence/tools/revert.py,
with the worktree as an argument.
"""
import runpy
import subprocess
import sys

WT = sys.argv[1]
cases = runpy.run_path(sys.argv[2])["CASES"]
out = open(sys.argv[3], "w")
bad = 0


def run(test, pkg):
    p = subprocess.run(["go", "test", "-count=1", "-run", test, "-v", pkg], cwd=WT,
                       capture_output=True, text=True)
    return p.returncode, p.stdout + p.stderr


for case in cases:
    if len(case) == 6:
        name, path, old, new, test, pkg = case
        edits = [(path, old, new)]
    else:
        name, edits, test, pkg = case
    out.write("=" * 72 + "\n## rule: %s\n## test: %s %s\n" % (name, pkg, test))
    saved = {}
    ok_edit = True
    for path, old, new in edits:
        full = WT + "/" + path
        src = saved.get(full) or open(full).read()
        saved.setdefault(full, src)
        cur = open(full).read()
        out.write("## edit %s:\n-%s\n+%s\n" % (path, old, new))
        if cur.count(old) != 1:
            out.write("## HARNESS ERROR: old text found %d times\n" % cur.count(old))
            ok_edit = False
            break
        open(full, "w").write(cur.replace(old, new))
    reverted_failed = False
    try:
        if ok_edit:
            code, text = run(test, pkg)
            out.write("## reverted run (exit %d):\n%s\n" % (code, text))
            reverted_failed = code != 0 and "--- FAIL" in text and "[build failed]" not in text
    finally:
        for full, src in saved.items():
            open(full, "w").write(src)
    code2, text2 = run(test, pkg)
    out.write("## restored run (exit %d):\n%s\n" % (code2, text2))
    ok = reverted_failed and code2 == 0
    out.write("## verdict: %s\n" % ("OK (fails reverted, passes restored)" if ok else "NOT OK"))
    print("%-60s %s" % (name, "OK" if ok else "NOT OK"))
    bad += 0 if ok else 1
out.write("=" * 72 + "\n## summary: %d rule(s), %d not OK\n" % (len(cases), bad))
out.close()
sys.exit(1 if bad else 0)
