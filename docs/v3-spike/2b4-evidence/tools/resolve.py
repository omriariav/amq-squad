#!/usr/bin/env python3
"""resolve.py MODE FILE...: resolve git conflict blocks. MODE both = ours then theirs, ours = HEAD side."""
import re
import sys

mode = sys.argv[1]
for path in sys.argv[2:]:
    text = open(path).read()
    pat = re.compile(r"<<<<<<< [^\n]*\n(.*?)=======\n(.*?)>>>>>>> [^\n]*\n", re.S)
    if mode == "both":
        text = pat.sub(lambda m: m.group(1) + m.group(2), text)
    else:
        text = pat.sub(lambda m: m.group(1), text)
    open(path, "w").write(text)
    print(path, "resolved", mode)
