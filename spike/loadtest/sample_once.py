#!/usr/bin/env python3
"""One RAM sample for the S6 load test. Called by sample-ram.sh.

usage: sample_once.py OUT_DIR REPO [REPO...]

Finds seat panes (tmux panes whose current path is inside one of the repo
paths, or whose session name matches $S6_TMUX_RE), remembers them in
OUT_DIR/panes-seen.tsv so a seat that later cd's elsewhere is still counted,
sums resident memory over each pane's process tree (pane pid and all
descendants), and appends:
  OUT_DIR/ram.csv        one row per sample: seat totals plus a vm_stat summary
  OUT_DIR/ram-seats.csv  one row per seat pane per sample
Read-only with respect to everything except OUT_DIR.
"""
import csv
import datetime
import os
import re
import subprocess
import sys


def run(cmd):
    try:
        return subprocess.run(cmd, capture_output=True, text=True, timeout=20).stdout
    except Exception:
        return ""


def real(p):
    try:
        return os.path.realpath(p)
    except Exception:
        return p


def main():
    out = sys.argv[1]
    repos = [real(r).rstrip("/") for r in sys.argv[2:]]
    sess_re = re.compile(os.environ["S6_TMUX_RE"]) if os.environ.get("S6_TMUX_RE") else None
    os.makedirs(out, exist_ok=True)
    now = datetime.datetime.now(datetime.timezone.utc).astimezone()
    ts = int(now.timestamp())
    iso = now.isoformat(timespec="seconds")

    # --- tmux panes -------------------------------------------------------
    fmt = "#{pane_id}\t#{session_name}\t#{pane_pid}\t#{pane_current_path}\t#{@amq_agent}\t#{pane_title}"
    live = {}
    for line in run(["tmux", "list-panes", "-a", "-F", fmt]).splitlines():
        f = line.split("\t")
        if len(f) < 6:
            continue
        pid, sess, ppid, path, agent, title = f[0], f[1], f[2], f[3], f[4], f[5]
        live[pid] = dict(session=sess, pid=int(ppid) if ppid.isdigit() else 0, path=path, agent=agent, title=title)

    seen_path = os.path.join(out, "panes-seen.tsv")
    seen = {}
    if os.path.exists(seen_path):
        for line in open(seen_path):
            f = line.rstrip("\n").split("\t")
            if len(f) >= 4:
                seen[f[0]] = f
    for pid, p in live.items():
        if pid in seen:
            continue
        rp = real(p["path"]).rstrip("/")
        in_repo = any(rp == r or rp.startswith(r + "/") for r in repos)
        by_sess = bool(sess_re and sess_re.search(p["session"]))
        if in_repo or by_sess:
            label = p["agent"]
            if not label:
                for r in repos:
                    if rp == r or rp.startswith(r + "/"):
                        rel = os.path.relpath(rp, r)
                        label = os.path.basename(r) + ("" if rel == "." else "/" + rel.replace(".s6-wt/", ""))
                        break
            label = label or os.path.basename(rp) or p["title"]
            seen[pid] = [pid, p["session"], str(ts), label]
            with open(seen_path, "a") as fh:
                fh.write("\t".join(seen[pid]) + "\n")

    # --- process table ----------------------------------------------------
    rss = {}
    kids = {}
    for line in run(["ps", "-axo", "pid=,ppid=,rss="]).splitlines():
        f = line.split()
        if len(f) != 3 or not all(x.isdigit() for x in f):
            continue
        pid, ppid, r = int(f[0]), int(f[1]), int(f[2])
        rss[pid] = r
        kids.setdefault(ppid, []).append(pid)

    def tree(root):
        stack, seen_p = [root], set()
        while stack:
            x = stack.pop()
            if x in seen_p:
                continue
            seen_p.add(x)
            stack.extend(kids.get(x, []))
        return seen_p

    seat_rows = []
    total_kb, total_procs, counted = 0, 0, set()
    for pid, rec in seen.items():
        p = live.get(pid)
        if not p or not p["pid"]:
            continue  # pane gone
        procs = tree(p["pid"]) - counted
        counted |= procs
        kb = sum(rss.get(x, 0) for x in procs)
        total_kb += kb
        total_procs += len(procs)
        seat_rows.append([ts, iso, pid, p["session"], rec[3], len(procs), round(kb / 1024, 1)])

    # --- vm_stat / swap ---------------------------------------------------
    vm = run(["vm_stat"])
    m = re.search(r"page size of (\d+) bytes", vm)
    ps = int(m.group(1)) if m else 16384
    pages = {}
    for line in vm.splitlines():
        mm = re.match(r'"?([^":]+)"?:\s+(\d+)\.', line)
        if mm:
            pages[mm.group(1).strip()] = int(mm.group(2))

    def mb(key):
        return round(pages.get(key, 0) * ps / 1048576, 1)

    swap = run(["sysctl", "-n", "vm.swapusage"])
    ms = re.search(r"used = ([\d.]+)M", swap)
    free_pct = run(["sysctl", "-n", "kern.memorystatus_level"]).strip()

    row = [ts, iso, len(seat_rows), total_procs, round(total_kb / 1024, 1),
           mb("Pages free"), mb("Pages active"), mb("Pages inactive"), mb("Pages wired down"),
           mb("Pages occupied by compressor"), pages.get("Pageouts", 0), free_pct,
           ms.group(1) if ms else ""]
    head = ["ts_epoch", "ts_iso", "seat_panes", "seat_procs", "seat_rss_mb",
            "sys_free_mb", "sys_active_mb", "sys_inactive_mb", "sys_wired_mb",
            "sys_compressed_mb", "sys_pageouts", "mem_free_pct", "swap_used_mb"]

    def append(path, header, rows):
        new = not os.path.exists(path)
        with open(path, "a", newline="") as fh:
            w = csv.writer(fh)
            if new:
                w.writerow(header)
            w.writerows(rows)

    append(os.path.join(out, "ram.csv"), head, [row])
    if seat_rows:
        append(os.path.join(out, "ram-seats.csv"),
               ["ts_epoch", "ts_iso", "pane_id", "tmux_session", "seat_label", "procs", "rss_mb"], seat_rows)
    print(",".join(str(x) for x in row))


if __name__ == "__main__":
    main()
