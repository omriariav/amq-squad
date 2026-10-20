"""Build an Apply request for only trust_confirmation actions from saved Prepare.

S4 authorizes the exact launch. Unexpected decisions are refused for inspection.
This script prints JSON; it never invokes Apply.
"""
import json
import sys

request = json.load(open(sys.argv[1]))
prepared = json.load(open(sys.argv[2]))
if prepared["outcome"] != "action_required" or prepared.get("reason") != "untrusted_config_digest":
    raise SystemExit("expected a fresh Prepare requiring only exact trust confirmation")
decisions = []
for action in prepared["required_actions"]:
    if action["kind"] != "trust_confirmation" or "trust_exact_subject" not in action["allowed_decisions"]:
        raise SystemExit("unexpected action: " + json.dumps(action))
    decisions.append({"action_id": action["action_id"], "choice": "trust_exact_subject"})
print(json.dumps({"request_version": 1, "prepare": request,
                  "subject_schema": prepared["subject_schema"],
                  "subject_digest": prepared["subject_digest"],
                  "decisions": decisions}, indent=2))
