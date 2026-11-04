#!/usr/bin/env python3
"""The revert runs of the 3.1.1-3 follow-up, the decision kinds in the scan
(docs/v3-spike/311-evidence/311-squad-ux-decisions.md).

Each case puts one change back in the working tree, runs the row that must
catch it, and expects it to fail; the touched file is restored with git
checkout after every run, in a finally. Run it from a clean checkout of the
branch:
  python3 docs/v3-spike/311-evidence/311-squad-ux-decisions-revert-runs.py [N ...]
It refuses a tree with any change. No network, no real AMQ, tmux or Buzz:
the rows run on the stand-in pi and Go's in-process fakes. It prints one
JSON line per case: its number, the row, the revert, caught or NOT CAUGHT,
and the failing output's first line."""
import subprocess, sys, re, json
W = subprocess.check_output(["git", "rev-parse", "--show-toplevel"], text=True).strip()
if subprocess.run(["git", "status", "--porcelain", "--untracked-files=all"], cwd=W, capture_output=True, text=True).stdout:
    sys.exit("refused: the tree has changes; the runs rewrite files and restore them")
PI = "cd pi && node --test test/%s.test.ts"
def PIN(f, pat): return "cd pi && node --test --test-name-pattern='%s' test/%s.test.ts" % (pat, f)
GO = "go test -count=1 -run '%s' ./%s/"
L = "pi/extensions/lib/"
R = [
 ("A", "a stop kind dropped from the table (seat_gone)", L+"words.ts",
  "	seat_gone: (w) => [`seat ${w}'s process is gone`, \"ask the lead to relaunch the roster\"],\n", '', PIN("words", "every decision kind Go writes")),
 ("A", "a routine kind dropped from the table (legacy_merge_head_missing)", L+"words.ts",
  '"lease_expired", "lease_renewed", "legacy_merge_head_missing",', '"lease_expired", "lease_renewed",', PIN("words", "every decision kind Go writes")),
 ("B", "the selector shows only the stops it knows (an unknown kind hidden)", L+"screen.ts",
  '		if (k === "routine" || (d.kind === "scheduler_stopped" && !d.reason)) continue;', '		if (k !== "stop" || (d.kind === "scheduler_stopped" && !d.reason)) continue;', PIN("words", "an unknown decision kind shows")),
 ("B", "an unknown kind's words as the 3.1.1-3 fallback (no kind below the line)", L+"words.ts",
  '	if (!words) return UNKNOWN_STOP.test(d.kind) ? "the scheduler stopped something; its kind is below the line" : "the scheduler recorded something; its kind is below the line";', '	if (!words) return "a seat stopped; the record is below the line";', PIN("words", "an unknown decision kind shows")),
 ("B", "an unknown kind's record without its kind", L+"screen.ts",
  '		const code = k === "unknown" ? d.kind : d.reason_code || d.kind;', '		const code = d.reason_code || "decision";', PIN("words", "an unknown decision kind shows")),
 ("B", "a routine record treated as a stop (it hides the stop before it)", L+"screen.ts",
  '		if (k === "routine" || (d.kind === "scheduler_stopped" && !d.reason)) continue;', '		if (d.kind === "scheduler_stopped" && !d.reason) continue;', PIN("words", "an unknown decision kind shows")),
]
only = sys.argv[1:]
out = []
for i, (row, what, f, old, new, cmd) in enumerate(R, 1):
    if only and str(i) not in only: continue
    p = f"{W}/{f}"
    s = open(p).read()
    if old not in s:
        out.append((i, row, what, "PATCH MISSING", "")); continue
    open(p, "w").write(s.replace(old, new, 1))
    try:
        r = subprocess.run(cmd, shell=True, cwd=W, capture_output=True, text=True, timeout=600)
    finally:
        subprocess.run(["git", "checkout", "-q", "--", f], cwd=W)
    text = r.stdout + r.stderr
    first = ""
    for pat in [r"^not ok \d+ - .*", r"^--- FAIL: .*", r"^FAIL.*", r"^SyntaxError.*", r"^.*Error.*"]:
        m = re.search(pat, text, re.M)
        if m: first = m.group(0); break
    # the failing assertion's message line after a not ok
    msg = ""
    m = re.search(r"^not ok \d+ .*\n(?:.*\n){0,8}?\s+error: (.*)", text, re.M)
    if m: msg = m.group(1).strip()
    m2 = re.search(r"^\s+\S+_test\.go:\d+: (.*)", text, re.M)
    if m2: msg = m2.group(1).strip()
    out.append((i, row, what, "caught" if r.returncode != 0 else "NOT CAUGHT", (first + (" | " + msg if msg else ""))[:300]))
for o in out:
    print(json.dumps(o))
dirty = subprocess.run(["git", "status", "--short"], cwd=W, capture_output=True, text=True).stdout
print("DIRTY:" + dirty if dirty.strip() else "clean")
