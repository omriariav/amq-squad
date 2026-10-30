#!/usr/bin/env python3
"""Print the tool calls and results recorded in probe run directories (events.jsonl)."""
import json
import sys

for d in sys.argv[1:]:
    print("==", d)
    with open(f"{d}/events.jsonl") as f:
        for line in f:
            rec = json.loads(line)
            e = rec.get("ev") or {}
            t = e.get("type")
            if t == "tool_execution_start":
                print(f"  {rec['t_ms']:>7}ms START {e['toolName']} {json.dumps(e.get('args'))[:300]}")
            elif t == "tool_execution_end":
                content = (e.get("result") or {}).get("content")
                print(f"  {rec['t_ms']:>7}ms END   {e['toolName']} isError={e.get('isError')} {json.dumps(content)[:300]}")
            elif t == "message_end" and (e.get("message") or {}).get("role") == "assistant":
                m = e["message"]
                text = "".join(c.get("text", "") for c in m.get("content", []) if c.get("type") == "text")
                print(f"  {rec['t_ms']:>7}ms ASSISTANT stop={m.get('stopReason')} {text[:200]!r}")
