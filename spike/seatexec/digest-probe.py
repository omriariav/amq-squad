"""Prepare-only probes: mutate one field at a time and report digest changes.

Usage: python3 digest-probe.py REQUEST.json AMQ_SPIKE_SH
Runs `amq launch --request - --prepare --json` (read-only) per variant.
"""
import copy, json, os, subprocess, sys

request = json.load(open(sys.argv[1]))
runner = sys.argv[2]
env = dict(os.environ, CODEX_PIN="0147")


def prepare(req):
    out = subprocess.run([runner, "launch", "--request", "-", "--prepare", "--json"],
                         input=json.dumps(req), capture_output=True, text=True, env=env)
    d = json.loads(out.stdout)
    return {k: d.get(k) for k in ("outcome", "reason", "trust_digest", "plan_digest", "subject_digest")}


def part(req, handle):
    return next(p for p in req["intent"]["participants"] if p["handle"] == handle)


def mutate(fn):
    req = copy.deepcopy(request)
    fn(req)
    return req


def set_arg(handle, flag, value):
    def fn(req):
        args = part(req, handle)["args"]
        args[args.index(flag) + 1] = value
    return fn


def set_c(value):
    def fn(req):
        args = part(req, "s4-codex")["args"]
        args[args.index("model_reasoning_effort=medium")] = value
    return fn


variants = {
    "baseline": lambda req: None,
    "claude --model opus": set_arg("s4-claude", "--model", "opus"),
    "claude --effort high": set_arg("s4-claude", "--effort", "high"),
    "codex --model gpt-5.5": set_arg("s4-codex", "--model", "gpt-5.5"),
    "codex effort high": set_c("model_reasoning_effort=high"),
    "codex --sandbox read-only": set_arg("s4-codex", "--sandbox", "read-only"),
    "wrapper args --seat other": lambda req: part(req, "s4-claude")["wrapper"].__setitem__("args", ["--seat", "other"]),
    "initial_input text": lambda req: part(req, "s4-claude")["initial_input"].__setitem__("text", "different bootstrap"),
}
base = None
rows = {}
for name, fn in variants.items():
    r = prepare(mutate(fn))
    if base is None:
        base = r
    r["trust_changed"] = r["trust_digest"] != base["trust_digest"]
    r["plan_changed"] = r["plan_digest"] != base["plan_digest"]
    rows[name] = r
print(json.dumps(rows, indent=2))
