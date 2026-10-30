#!/usr/bin/env python3
"""Revert check for the 3-2 fix 3 commit (P1-4 re-bind ruling)."""
import subprocess, sys, os

W = "/Users/omri/code/amq-squad/.claude/worktrees/agent-a83ff47e3e447b571"
MT = "./internal/mergetrain/"
REVERTS = [
    ("the train re-binds after its own rebase", "internal/mergetrain/train.go",
     "\t\t\t\tit.BoundHead = cur.HeadSHA\n", "", MT, "TestTrainRebindsAfterItsRebaseAndRefusesAnUnboundPush"),
    ("a failed re-bind stops the run before the merge", "internal/mergetrain/train.go",
     "\t\t\t\tif err := t.Binder.BindPR(it.Task, p.Number, cur.HeadSHA); err != nil {\n\t\t\t\t\treturn",
     "\t\t\t\tif err := t.Binder.BindPR(it.Task, p.Number, cur.HeadSHA); err != nil && false {\n\t\t\t\t\treturn", MT, "TestTrainRebindsAfterItsRebaseAndRefusesAnUnboundPush"),
]


def main():
    out = open(sys.argv[1], "w")
    bad = []
    for name, rel, old, new, pkg, pat in REVERTS:
        path = os.path.join(W, rel)
        orig = open(path).read()
        if orig.count(old) != 1:
            out.write(f"## {name}: SETUP ERROR, anchor found {orig.count(old)} times in {rel}\n\n")
            bad.append(name)
            continue
        open(path, "w").write(orig.replace(old, new))
        try:
            r = subprocess.run(["go", "test", "-count=1", "-run", pat, pkg], cwd=W, capture_output=True, text=True)
        finally:
            open(path, "w").write(orig)
        text = r.stdout + r.stderr
        assertion = r.returncode != 0 and "build failed" not in text
        verdict = "FAILS on its assertion with the fix reverted (good)" if assertion else ("BUILD FAILURE (bad revert)" if r.returncode != 0 else "PASSES with the fix reverted (BAD)")
        if not assertion:
            bad.append(name)
        out.write(f"## {name}: revert {rel}; go test -run '{pat}' {pkg}: {verdict}\n-- reverted: {old!r}\n-- to:       {new!r}\n{text}\n")
        r2 = subprocess.run(["go", "test", "-count=1", "-run", pat, pkg], cwd=W, capture_output=True, text=True)
        out.write(f"-- restored: exit {r2.returncode}\n{r2.stdout.strip()[-300:]}\n\n")
        if r2.returncode != 0:
            bad.append(name + " (restored run failed)")
    out.write("SUMMARY: " + ("every row fails on its assertion when reverted and passes restored" if not bad else "problems: " + ", ".join(bad)) + "\n")
    print("problems:", bad)


main()
