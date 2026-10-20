"""Build the PreToolUse fault table from hook events and the Claude session JSONL.

Usage: python3 fault-table.py HOOK_EVENTS.jsonl SESSION.jsonl
Elapsed A: tool_use entry timestamp -> tool_result entry timestamp (JSONL, all trials).
Elapsed B: PreToolUse hook start -> PostToolUse hook start (hook log; none for "missing").
"""
import json, sys
from datetime import datetime

events = [json.loads(l) for l in open(sys.argv[1])]
pre = {e["tool_use_id"]: e for e in events if e["event"] == "PreToolUse"}
post = {e["tool_use_id"]: e for e in events if e["event"] == "PostToolUse"}

def ts(s):
    return datetime.fromisoformat(s.replace("Z", "+00:00")).timestamp()

use, result = {}, {}
for line in open(sys.argv[2]):
    d = json.loads(line)
    msg = d.get("message") or {}
    content = msg.get("content")
    if "timestamp" not in d or not isinstance(content, list):
        continue
    for c in content:
        if c.get("type") == "tool_use" and c.get("name") == "Bash":
            use[c["id"]] = (ts(d["timestamp"]), c["input"].get("command"))
        if c.get("type") == "tool_result":
            text = c.get("content")
            if isinstance(text, list):
                text = " ".join(x.get("text", "") for x in text if isinstance(x, dict))
            result[c["tool_use_id"]] = (ts(d["timestamp"]), c.get("is_error", False), (text or "")[:120])

hooks = []
for line in open(sys.argv[2]):
    if "pretool.py" in line and '"hookEvent":"PreToolUse"' in line:
        d = json.loads(line)
        a = d.get("attachment") or d
        hooks.append({k: a.get(k) for k in ("toolUseID", "durationMs", "timedOut", "timeoutMs", "exitCode", "stderr")})
print("PreToolUse attachments for pretool.py (Claude's own record):")
for h in hooks:
    print(" ", json.dumps(h))
print()
print("| trial | command | tool proceeded | is_error | tool_use->tool_result (s) | PreToolUse->PostToolUse hook (s) | result excerpt |")
print("|---|---|---|---|---|---|---|")
for tid, (t_use, cmd) in use.items():
    t_res, err, text = result.get(tid, (None, None, ""))
    b = f"{post[tid]['t'] - pre[tid]['t']:.2f}" if tid in pre and tid in post else "n/a (hook never ran)"
    a = f"{t_res - t_use:.2f}" if t_res else "n/a"
    proceeded = "yes" if tid in post else "no"
    print(f"| {pre.get(tid, {}).get('mode', 'missing')} | `{cmd}` | {proceeded} | {err} | {a} | {b} | {text.strip().replace('|', '/')} |")
