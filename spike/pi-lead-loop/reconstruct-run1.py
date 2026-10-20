#!/usr/bin/env python3
"""Rebuild lead-loop run 1's six Go files from the lead's own `cat` of them.

The run 1 files were removed before run 2. The lead session (run 1) holds one
bash call `cat max.go; echo "---"; cat max_test.go; echo "==="; ...` whose tool
result is the full text of all six files. This writes them into <out_dir>.
Usage: reconstruct-run1.py <lead session.jsonl> <out_dir>
"""
import json
import os
import re
import sys

session, out = sys.argv[1], sys.argv[2]
order = ["max.go", "max_test.go", "reverse.go", "reverse_test.go", "even.go", "even_test.go"]
call_id = None
with open(session) as f:
    for line in f:
        msg = json.loads(line).get("message") or {}
        if msg.get("role") == "assistant":
            for c in msg.get("content", []):
                if c.get("type") == "toolCall" and str((c.get("arguments") or {}).get("command", "")).startswith("cat max.go;"):
                    call_id = c["id"]
        elif msg.get("role") == "toolResult" and call_id and msg.get("toolCallId") == call_id:
            text = "".join(c.get("text", "") for c in msg["content"] if c.get("type") == "text")
            parts = re.split(r"^(?:---|===)$\n?", text, flags=re.M)
            assert len(parts) == 6, f"expected 6 parts, got {len(parts)}"
            os.makedirs(out, exist_ok=True)
            for name, body in zip(order, parts):
                with open(os.path.join(out, name), "w") as w:
                    w.write(body.rstrip("\n") + "\n")
            print("wrote", ", ".join(order))
            sys.exit(0)
sys.exit("cat call or its result not found")
