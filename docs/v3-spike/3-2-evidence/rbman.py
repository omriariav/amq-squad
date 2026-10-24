#!/usr/bin/env python3
"""Resolve docs/v3-manifest.yaml during the 3-2 rebase: take the upstream
manifest, then apply every row the original 3-2 commit changed relative to
the 3-2 base (2c7a38e6): rows added (new files), rows changed (status,
note), rows removed. Then refresh current_lines with manifest.py.
usage: rbman.py <original commit>"""
import re, subprocess, sys

W = "/Users/omri/code/amq-squad/.claude/worktrees/agent-a83ff47e3e447b571"
BASE = "2c7a38e6"


def show(rev):
    return subprocess.run(["git", "-C", W, "show", f"{rev}:docs/v3-manifest.yaml"], capture_output=True, text=True, check=True).stdout


def rows(text):
    i = text.index("\n- path: ") + 1
    head, body = text[:i], text[i:]
    out = {}
    order = []
    for r in re.split(r"(?m)^(?=- path: )", body):
        if not r:
            continue
        p = r.split("\n", 1)[0][len("- path: "):].strip()
        out[p] = r if r.endswith("\n") else r + "\n"
        order.append(p)
    return head, out, order


orig = sys.argv[1]
_, base_rows, _ = rows(show(BASE))
_, mine_rows, _ = rows(show(orig))
up_text = subprocess.run(["git", "-C", W, "show", "HEAD:docs/v3-manifest.yaml"], capture_output=True, text=True, check=True).stdout
head, up_rows, up_order = rows(up_text)

def strip_lines(r):
    return re.sub(r"(?m)^  (current_lines|target_lines): \d+\n", "", r or "")


# a row whose only change is its line counts keeps the upstream row (refresh
# recounts); any other change (status, note, a new row) is applied
changed = {p: r for p, r in mine_rows.items() if base_rows.get(p) != r and (p not in up_rows or strip_lines(base_rows.get(p)) != strip_lines(r))}
removed = [p for p in base_rows if p not in mine_rows]
for p, r in changed.items():
    if p in up_rows and base_rows.get(p) is not None and up_rows[p] != base_rows[p]:
        print("both changed:", p)
    prev = up_rows.get(p, "")
    m = re.search(r"(?m)^  replaced_by: .*\n", prev)
    if m and "replaced_by:" not in r:
        r = re.sub(r"(?m)^(  note: )", m.group(0) + r"\1", r, count=1) if "\n  note: " in r else r + m.group(0)
    up_rows[p] = r
    if p not in up_order:
        idx = next((i for i, q in enumerate(up_order) if q > p), len(up_order))
        up_order.insert(idx, p)
for p in removed:
    up_rows.pop(p, None)
up_order = [p for p in up_order if p in up_rows]
body = "".join(up_rows[p] if up_rows[p].endswith("\n") else up_rows[p] + "\n" for p in up_order)
open(W + "/docs/v3-manifest.yaml", "w").write(head + body)
print(f"applied {len(changed)} changed rows, removed {len(removed)}")
