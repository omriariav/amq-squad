#!/usr/bin/env python3
"""List the tool calls in pi session files (JSONL), with timestamps, for the lead-loop transcript."""
import json
import sys

for path in sys.argv[1:]:
    print("==", path)
    with open(path) as f:
        for line in f:
            rec = json.loads(line)
            msg = rec.get("message") or {}
            if rec.get("type") != "message":
                continue
            ts = rec.get("timestamp", "")[11:19]
            if msg.get("role") == "assistant":
                for c in msg.get("content", []):
                    if c.get("type") == "toolCall":
                        args = c.get("arguments") or {}
                        shown = args.get("command") or args.get("path") or json.dumps(args)
                        print(f"  {ts} CALL {c.get('name')}: {str(shown)[:220]}")
            elif msg.get("role") == "toolResult":
                text = "".join(c.get("text", "") for c in msg.get("content", []) if c.get("type") == "text")
                err = " ERROR" if msg.get("isError") else ""
                print(f"  {ts} RESULT{err}: {text.strip()[:160]!r}")
            elif msg.get("role") == "user":
                content = msg.get("content")
                text = content if isinstance(content, str) else "".join(c.get("text", "") for c in content if c.get("type") == "text")
                print(f"  {ts} USER: {text.strip()[:200]!r}")
