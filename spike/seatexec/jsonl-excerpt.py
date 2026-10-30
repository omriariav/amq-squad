"""Print minimal JSONL excerpts (tool_use/tool_result timestamps, hook attachments) per fault trial.

Usage: python3 jsonl-excerpt.py SESSION.jsonl > evidence/fault-jsonl-excerpts.jsonl
"""
import json, sys

IDS = {"toolu_014UCztc1wZio7zoVpAcGkmi": "baseline-ok", "toolu_01TaTqW8g5JsQ7ccRBbXt3Eb": "deny",
       "toolu_01ESvTh61wRkXQT8QtLJcRNi": "a-sleep", "toolu_013Hwydm3Ngzy3XUQVAV7SZM": "b-exit1",
       "toolu_01YByGEoa37pCHg2CvrRNbQU": "c-missing"}
for line in open(sys.argv[1]):
    if not any(i in line for i in IDS):
        continue
    d = json.loads(line)
    a = d.get("attachment")
    if a:
        tid = a.get("toolUseID")
        print(json.dumps({"trial": IDS.get(tid, "?"), "kind": "hook_attachment", "timestamp": d.get("timestamp"),
                          "tool_use_id": tid, "type": a.get("type"), "hookEvent": a.get("hookEvent"),
                          "command": a.get("command"), "durationMs": a.get("durationMs"),
                          "exitCode": a.get("exitCode"), "timedOut": a.get("timedOut"),
                          "timeoutMs": a.get("timeoutMs"), "stderr": a.get("stderr")}))
        continue
    content = (d.get("message") or {}).get("content")
    if isinstance(content, list):
        for c in content:
            if c.get("type") in ("tool_use", "tool_result"):
                tid = c.get("id") or c.get("tool_use_id")
                rec = {"trial": IDS.get(tid, "?"), "kind": c["type"], "timestamp": d.get("timestamp"), "tool_use_id": tid}
                if c["type"] == "tool_use":
                    rec["command"] = c["input"].get("command")
                else:
                    rec["is_error"] = c.get("is_error", False)
                print(json.dumps(rec))
