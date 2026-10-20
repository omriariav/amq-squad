#!/usr/bin/env python3
"""Per-task wall time from the v2.31 task store. Called by task-times.sh.

usage: task_times.py snapshot OUT_DIR REPO [REPO...]
       task_times.py report   OUT_DIR REPO [REPO...]

The store keeps created_at, updated_at, and (while a claim is live) lease.issued_at, and
completion_lifecycle.completed_at once done. A completed or failed task LOSES its lease,
so the claim time must be captured while the task is in_progress: `snapshot` (looped by
`task-times.sh watch`) appends a line to OUT_DIR/task-snapshots.jsonl whenever a task's
status, assignee or lease changes. `report` merges the final store with the snapshots and,
unless S6_NO_GH=1, with the PR created time from `gh pr list`.

wall_sec        = terminal time (completed_at, or updated_at for failed/blocked/cancelled) - claim time
claim_to_pr_sec = PR created time - claim time
total           = first claim over all repos -> last terminal time (also from run start if known)
"""
import csv
import datetime
import glob
import json
import os
import re
import subprocess
import sys


def p_ts(s):
    if not s:
        return None
    s = re.sub(r"(\.\d{6})\d+", r"\1", s).replace("Z", "+00:00")
    try:
        return datetime.datetime.fromisoformat(s).timestamp()
    except Exception:
        return None


def session_of(repo):
    try:
        d = json.load(open(os.path.join(repo, ".amq-squad", "team.json")))
        for m in d.get("members", []):
            if m.get("session"):
                return m["session"]
    except Exception:
        pass
    return None


def load_tasks(repo):
    ses = session_of(repo)
    if not ses:
        return ses, []
    out = []
    for f in sorted(glob.glob(os.path.join(repo, ".amq-squad", "tasks", ses, "t*.json"))):
        try:
            out.append(json.load(open(f)))
        except Exception:
            pass
    return ses, out


def tid(task):
    m = re.match(r"\s*([NW]\d+)\b", task.get("title", ""))
    return m.group(1) if m else task.get("id", "?")


def snapshot(out, repos):
    state_path = os.path.join(out, ".task-state.json")
    try:
        state = json.load(open(state_path))
    except Exception:
        state = {}
    now = datetime.datetime.now().timestamp()
    n = 0
    with open(os.path.join(out, "task-snapshots.jsonl"), "a") as fh:
        for repo in repos:
            ses, tasks = load_tasks(repo)
            for t in tasks:
                key = f"{os.path.basename(repo)}/{t.get('id')}"
                sig = [t.get("status"), t.get("assigned_to"), (t.get("lease") or {}).get("issued_at"), t.get("updated_at")]
                if state.get(key) == sig:
                    continue
                state[key] = sig
                fh.write(json.dumps(dict(observed_at=now, repo=os.path.basename(repo), session=ses, id=t.get("id"),
                                         task=tid(t), status=t.get("status"), assigned_to=t.get("assigned_to"),
                                         lease_issued_at=(t.get("lease") or {}).get("issued_at"),
                                         updated_at=t.get("updated_at"),
                                         completed_at=(t.get("completion_lifecycle") or {}).get("completed_at"),
                                         failure_reason=t.get("failure_reason"))) + "\n")
                n += 1
    json.dump(state, open(state_path, "w"))
    print(f"snapshot: {n} change(s)")


def gh_prs(repo):
    if os.environ.get("S6_NO_GH") == "1":
        return {}
    try:
        url = subprocess.run(["git", "-C", repo, "remote", "get-url", "origin"], capture_output=True, text=True, timeout=20).stdout.strip()
        slug = re.sub(r"\.git$", "", re.sub(r"^(https://github.com/|git@github.com:)", "", url))
        gh = os.environ.get("S6_GH", "gh")
        r = subprocess.run([gh, "pr", "list", "--repo", slug, "--state", "all", "--limit", "200",
                            "--json", "number,headRefName,createdAt,url,state,isDraft"],
                           capture_output=True, text=True, timeout=60)
        prs = json.loads(r.stdout or "[]")
    except Exception:
        return {}
    return {p["headRefName"]: p for p in prs if p.get("headRefName", "").startswith("loadtest/")}


def fmt_d(sec):
    if sec is None:
        return "-"
    sec = int(sec)
    return f"{sec // 3600}h{(sec % 3600) // 60:02d}m{sec % 60:02d}s" if sec >= 3600 else f"{sec // 60}m{sec % 60:02d}s"


def report(out, repos):
    snaps = []
    sp = os.path.join(out, "task-snapshots.jsonl")
    if os.path.exists(sp):
        for line in open(sp):
            try:
                snaps.append(json.loads(line))
            except Exception:
                pass
    run_start = None
    rs = os.path.join(out, "run-start.epoch")
    if os.path.exists(rs):
        run_start = float(open(rs).read().strip())

    rows = []
    for repo in repos:
        name = os.path.basename(repo)
        ses, tasks = load_tasks(repo)
        prs = gh_prs(repo)
        for t in tasks:
            task = tid(t)
            mine = [s for s in snaps if s["repo"] == name and s["id"] == t.get("id")]
            claims = [p_ts(t.get("lease", {}).get("issued_at"))] + [p_ts(s.get("lease_issued_at")) for s in mine]
            claims = [c for c in claims if c]
            claim, claim_src = (min(claims), "lease") if claims else (None, "")
            if claim is None:
                ip = [s["observed_at"] for s in mine if s.get("status") == "in_progress"]
                if ip:
                    claim, claim_src = min(ip), "first-seen"
            status = t.get("status")
            if status == "completed":
                term = p_ts((t.get("completion_lifecycle") or {}).get("completed_at")) or p_ts(t.get("updated_at"))
            elif status in ("failed", "blocked", "cancelled"):
                term = p_ts(t.get("updated_at"))
            else:
                term = None
            pr = prs.get(f"loadtest/{task}")
            pr_at = p_ts(pr["createdAt"]) if pr else None
            rows.append(dict(repo=name, task=task, store_id=t.get("id"), title=re.sub(r"^\s*[NW]\d+:\s*", "", t.get("title", "")),
                             status=status, assignee=t.get("assigned_to", ""), claimed=claim, claim_src=claim_src,
                             terminal=term, wall=(term - claim) if (term and claim) else None,
                             pr=(pr or {}).get("url", ""), pr_state=(pr or {}).get("state", ""), pr_at=pr_at,
                             claim_to_pr=(pr_at - claim) if (pr_at and claim) else None,
                             failure=t.get("failure_reason", "")))

    def order(r):
        return (0 if r["task"].startswith("N") else 1, int(re.sub(r"\D", "", r["task"]) or 0))

    rows.sort(key=order)
    iso = lambda t: datetime.datetime.fromtimestamp(t).astimezone().isoformat(timespec="seconds") if t else ""
    with open(os.path.join(out, "tasks.csv"), "w", newline="") as fh:
        w = csv.writer(fh)
        w.writerow(["repo", "task", "store_id", "title", "status", "assignee", "claimed_at", "claim_src", "terminal_at",
                    "wall_sec", "pr_url", "pr_state", "pr_created_at", "claim_to_pr_sec", "failure_reason"])
        for r in rows:
            w.writerow([r["repo"], r["task"], r["store_id"], r["title"], r["status"], r["assignee"], iso(r["claimed"]),
                        r["claim_src"], iso(r["terminal"]), "" if r["wall"] is None else int(r["wall"]), r["pr"],
                        r["pr_state"], iso(r["pr_at"]), "" if r["claim_to_pr"] is None else int(r["claim_to_pr"]), r["failure"]])

    lines = []
    lines.append(f"{'repo':14s} {'task':4s} {'status':11s} {'assignee':12s} {'wall':>9s} {'claim>PR':>9s}  PR / failure")
    for r in rows:
        tail = r["pr"] or (("FAIL: " + r["failure"]) if r["failure"] else "")
        lines.append(f"{r['repo']:14s} {r['task']:4s} {str(r['status']):11s} {r['assignee'] or '-':12s} {fmt_d(r['wall']):>9s} "
                     f"{fmt_d(r['claim_to_pr']):>9s}  {tail[:70]}")
    claims = [r["claimed"] for r in rows if r["claimed"]]
    terms = [r["terminal"] for r in rows if r["terminal"]]
    prs_at = [r["pr_at"] for r in rows if r["pr_at"]]
    n = len(rows)
    done = sum(1 for r in rows if r["status"] == "completed")
    failed = sum(1 for r in rows if r["status"] in ("failed", "blocked", "cancelled"))
    withpr = sum(1 for r in rows if r["pr"])
    lines.append("")
    lines.append(f"tasks={n} completed={done} failed/blocked/cancelled={failed} open={n - done - failed} with_pr={withpr}")
    if claims and terms:
        lines.append(f"total wall (first claim -> last terminal): {fmt_d(max(terms) - min(claims))}")
    if claims and prs_at:
        lines.append(f"total wall (first claim -> last PR created): {fmt_d(max(prs_at) - min(claims))}")
    if run_start and terms:
        lines.append(f"from run start -> last terminal: {fmt_d(max(terms) - run_start)}")
    walls = sorted(r["wall"] for r in rows if r["wall"] is not None)
    if walls:
        lines.append(f"per-task wall: min {fmt_d(walls[0])}  median {fmt_d(walls[len(walls) // 2])}  max {fmt_d(walls[-1])}")
    no_claim = [r["task"] for r in rows if r["claimed"] is None and r["status"] != "pending"]
    if no_claim:
        lines.append(f"WARNING no claim time captured (watch not running?): {' '.join(no_claim)}")
    text = "\n".join(lines)
    open(os.path.join(out, "tasks-report.txt"), "w").write(text + "\n")
    print(text)


if __name__ == "__main__":
    mode, out, repos = sys.argv[1], sys.argv[2], [os.path.realpath(r) for r in sys.argv[3:]]
    os.makedirs(out, exist_ok=True)
    {"snapshot": snapshot, "report": report}[mode](out, repos)
