#!/usr/bin/env python3
"""Brief 31-1 baseline: per seat, the launch segments of the last roster
launch from a read-only copy of a session's records.

Usage: python3 -I baseline.py COPY_DIR

COPY_DIR holds decisions.jsonl (the scheduler directory's), launch-dir/<seat>/
{launch.json,proof.json} (the core's staged record and accepted proof) and
agents/<seat>/outbox/sent/*.md (the seat's sent messages). Read only."""
import glob, json, os, sys
from datetime import datetime

def ts(s):
    return datetime.fromisoformat(s.replace("Z", "+00:00"))

def main(d):
    decs = [json.loads(l) for l in open(os.path.join(d, "decisions.jsonl"))]
    launched = [x for x in decs if x["kind"] == "roster_launched"]
    last = launched[-1]
    launch_id = last["reason"].split()[1].rstrip(":")
    started = [x for x in decs if x["kind"] == "scheduler_started" and ts(x["at"]) <= ts(last["at"])][-1]
    t0, troster = ts(started["at"]), ts(last["at"])
    print(f"roster launch {launch_id}")
    print(f"  scheduler_started  {started['at']}")
    print(f"  roster_launched    {last['at']}  (+{(troster - t0).total_seconds():.3f}s)")
    rows = []
    for seat in sorted(os.listdir(os.path.join(d, "launch-dir"))):
        rec = json.load(open(os.path.join(d, "launch-dir", seat, "launch.json")))
        if rec.get("launch_id") != launch_id:
            continue
        proof = json.load(open(os.path.join(d, "launch-dir", seat, "proof.json")))
        compiled = ts(rec["started_at"])
        issued = ts(rec["proof"]["issued_at"])
        applied = ts(rec["apply"]["at"])
        live = ts(proof["at"]) if proof.get("launch_id") == launch_id else None
        first = None
        for p in sorted(glob.glob(os.path.join(d, "agents", seat, "outbox", "sent", "*.md"))):
            body = open(p).read().split("---json", 1)[1].split("\n---", 1)[0]
            created = ts(json.loads(body)["created"])
            if created >= troster.replace(microsecond=0) and (first is None or created < first):
                first = created
        q = [x for x in decs if x.get("seat") == seat and x["kind"] in ("seat_quarantined", "seat_closed") and ts(x["at"]) >= troster]
        rows.append((seat, rec["runtime"], rec.get("model", ""), compiled, issued, applied, live, first, q))
    s = lambda a, b: f"{(b - a).total_seconds():7.3f}" if a and b else "      -"
    print("\nseat        runtime model           prepare  mint   apply  seat-exec+proof  start->live  start->first-msg  after")
    for seat, rt, model, compiled, issued, applied, live, first, q in rows:
        after = "; ".join(f"{x['kind']} {x.get('reason','')}" for x in q) or "-"
        print(f"{seat:11} {rt:7} {model:15} {s(t0, compiled)} {s(compiled, issued)} {s(issued, applied)} {s(applied, live):>16} {s(t0, live):>12} {s(t0, first):>17}  {after}")
    print("\nprepare = scheduler_started to the record's started_at (compile; the scheduler's admission and adoption precede it)")
    print("mint = started_at to the proof ticket's issued_at (preflight, config dirs, probes, proof socket)")
    print("apply = issued_at to the apply outcome (amq launch --apply)")
    print("seat-exec+proof = apply outcome to the accepted proof (seat live)")

if __name__ == "__main__":
    main(sys.argv[1])
