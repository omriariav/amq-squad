#!/usr/bin/env python3
"""Upsert fate-new manifest rows and refresh current_lines for given files.

usage: manifest_rows.py REPO path=reason [path=reason ...]
A path with an empty reason only refreshes current_lines/target_lines of an
existing new row.
"""
import re
import sys

repo = sys.argv[1]
man = repo + "/docs/v3-manifest.yaml"
text = open(man).read()

def lines(p):
    with open(repo + "/" + p) as f:
        return sum(1 for _ in f)

for arg in sys.argv[2:]:
    path, _, reason = arg.partition("=")
    n = lines(path)
    pat = re.compile(r"(- path: " + re.escape(path) + r"\n(?:  .*\n)*?)")
    m = re.search(r"^- path: " + re.escape(path) + r"\n((?:  .*\n)*)", text, re.M)
    if m:
        block = m.group(0)
        nb = re.sub(r"  current_lines: \d+", "  current_lines: %d" % n, block)
        if "  fate: new" in nb:
            nb = re.sub(r"  target_lines: \d+", "  target_lines: %d" % n, nb)
        text = text.replace(block, nb)
        continue
    if not reason:
        sys.exit("no row for " + path)
    cluster = path.split("/")[1]
    row = (
        "- path: %s\n  v2_lines: 0\n  current_lines: %d\n  fate: new\n  target_lines: %d\n"
        "  cluster: %s\n  extract: \"\"\n  reason: \"%s\"\n  status: new\n" % (path, n, n, cluster, reason)
    )
    # insert before the first row whose path sorts after this one
    rows = list(re.finditer(r"^- path: (\S+)\n", text, re.M))
    at = None
    for r in rows:
        if r.group(1) > path:
            at = r.start()
            break
    if at is None:
        text = text.rstrip("\n") + "\n" + row
    else:
        text = text[:at] + row + text[at:]
open(man, "w").write(text)
