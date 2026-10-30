#!/usr/bin/env python3
"""S4 instrumentation hook. argv[1] is the event name.

PreToolUse behavior is read at run time from evidence/pretool-mode:
ok (exit 0), sleep (sleep 30 s, past the 5 s configured timeout),
exit1 (exit 1, no JSON deny), deny (JSON deny, control case).
The "missing" fault is produced by renaming pretool.py away.
"""
import json, os, sys, time
from pathlib import Path

EVID = Path("/Users/omri/.amq-squad/spike/s4/evidence")
t0 = time.time()
event = sys.argv[1]
try:
    payload = json.load(sys.stdin)
except Exception:
    payload = {}
rec = {"event": event, "t": t0, "pid": os.getpid(),
       "tool_use_id": payload.get("tool_use_id"), "tool_name": payload.get("tool_name"),
       "session_id": payload.get("session_id")}
if event == "PreToolUse":
    try:
        rec["mode"] = (EVID / "pretool-mode").read_text().strip()
    except FileNotFoundError:
        rec["mode"] = "ok"
    rec["command"] = (payload.get("tool_input") or {}).get("command")
if event == "SessionStart":
    env = {k: v for k, v in os.environ.items() if k.startswith("AMQ_SQUAD_")}
    env.update({k: os.environ.get(k) for k in ("AM_ME", "AM_ROOT")})
    rec["env"] = env
    rec["source"] = payload.get("source")
    with (EVID / "sessionstart-env.log").open("a") as f:
        f.write(time.strftime("%Y-%m-%dT%H:%M:%S") + " " + json.dumps(env, sort_keys=True) + "\n")
with (EVID / "hook-events.jsonl").open("a") as f:
    f.write(json.dumps(rec) + "\n")
if event == "SessionStart":
    role = os.environ.get("AMQ_SQUAD_ROLE_PATH")
    if role and Path(role).is_file():
        print(json.dumps({"hookSpecificOutput": {"hookEventName": "SessionStart",
              "additionalContext": "amq-squad seat role (from AMQ_SQUAD_ROLE_PATH):\n" + Path(role).read_text()}}))
elif event == "PreToolUse":
    mode = rec["mode"]
    if mode == "sleep":
        time.sleep(30)
        with (EVID / "hook-events.jsonl").open("a") as f:
            f.write(json.dumps({"event": "PreToolUse-sleep-finished", "t": time.time(), "pid": os.getpid()}) + "\n")
    elif mode == "exit1":
        sys.stderr.write("s4 fault: exit 1 without JSON deny\n")
        sys.exit(1)
    elif mode == "deny":
        print(json.dumps({"hookSpecificOutput": {"hookEventName": "PreToolUse",
              "permissionDecision": "deny", "permissionDecisionReason": "S4 control: deny"}}))
