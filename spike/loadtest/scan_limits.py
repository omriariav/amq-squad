#!/usr/bin/env python3
"""Scan for 429 and usage-limit events during the S6 run window. Called by scan-limits.sh.

usage: scan_limits.py OUT_DIR REPO [REPO...]

Window: OUT_DIR/run-start.epoch (or $S6_SINCE epoch) to OUT_DIR/run-end.epoch
(or $S6_UNTIL epoch, or now).

Claude: session JSONL under $S6_CLAUDE_PROJECTS (default ~/.claude/projects) in
project directories whose encoded name starts with an encoded repo path (this
covers the repo root and the .s6-wt/* worktrees), including subagent files.
Counts, per kind:
  usage_limit   assistant API-error messages whose text says the session / weekly /
                spend / usage limit was hit (these also carry status 429)
  http_429      other assistant API-error messages with status 429 / error rate_limit
  retry_429     system api_error retry records that mention 429 / rate_limit
  server_error  5xx and overloaded API errors (informational)
  other_error   auth failures and anything else (informational)
Codex: scanned only with S6_SCAN_CODEX=1 (Claude-only run: reported as pending).
Fallback: live tmux pane captures of every seat pane (panes-seen.tsv plus panes in the
repo paths) are saved under OUT_DIR/pane-captures/ and grepped for limit phrases.
Read-only except for OUT_DIR.
"""
import csv
import datetime
import glob
import json
import os
import re
import subprocess
import sys

USAGE_RE = re.compile(r"hit your .{0,40}limit|spend limit|session limit|usage limit|weekly limit|limit reached|usage_limit", re.I)
PANE_RE = re.compile(r"usage limit|hit your .{0,40}limit|session limit|spend limit|rate.?limit|\b429\b|overloaded|limit reached|try again in", re.I)


def enc(path):
    return re.sub(r"[^A-Za-z0-9]", "-", path).lower()


def parse_ts(s):
    try:
        return datetime.datetime.fromisoformat(s.replace("Z", "+00:00")).timestamp()
    except Exception:
        return None


def text_of(d):
    c = (d.get("message") or {}).get("content")
    if isinstance(c, list):
        return " ".join(x.get("text", "") for x in c if isinstance(x, dict))
    return c if isinstance(c, str) else ""


def read_epoch(path, env):
    if os.environ.get(env):
        return float(os.environ[env])
    if os.path.exists(path):
        try:
            return float(open(path).read().strip())
        except Exception:
            return None
    return None


def main():
    out = sys.argv[1]
    repos = [os.path.realpath(r) for r in sys.argv[2:]]
    start = read_epoch(os.path.join(out, "run-start.epoch"), "S6_SINCE")
    end = read_epoch(os.path.join(out, "run-end.epoch"), "S6_UNTIL") or datetime.datetime.now().timestamp()
    if start is None:
        sys.exit("no run start: run window-note.sh first (writes run-start.epoch) or set S6_SINCE=<epoch>")
    fmt = lambda t: datetime.datetime.fromtimestamp(t).astimezone().isoformat(timespec="seconds")

    root = os.environ.get("S6_CLAUDE_PROJECTS", os.path.expanduser("~/.claude/projects"))
    prefixes = [enc(r) for r in repos]
    files = []
    if os.path.isdir(root):
        for name in os.listdir(root):
            if any(name.lower().startswith(p) for p in prefixes):
                for f in glob.glob(os.path.join(root, name, "**", "*.jsonl"), recursive=True):
                    try:
                        if os.path.getmtime(f) >= start:
                            files.append(f)
                    except OSError:
                        pass

    events = []
    for f in files:
        try:
            fh = open(f, errors="ignore")
        except OSError:
            continue
        with fh:
            for line in fh:
                if ("429" not in line and "rate_limit" not in line and "isApiErrorMessage" not in line
                        and "api_error" not in line and "limit" not in line):
                    continue
                try:
                    d = json.loads(line)
                except Exception:
                    continue
                t = parse_ts(d.get("timestamp", ""))
                if t is None or t < start or t > end:
                    continue
                kind = status = None
                txt = ""
                if d.get("type") == "assistant" and d.get("isApiErrorMessage"):
                    txt = text_of(d)
                    status = d.get("apiErrorStatus")
                    err = d.get("error")
                    if USAGE_RE.search(txt):
                        kind = "usage_limit"
                    elif status == 429 or err == "rate_limit":
                        kind = "http_429"
                    elif (isinstance(status, int) and status >= 500) or "overloaded" in txt.lower() or err == "server_error":
                        kind = "server_error"
                    else:
                        kind = "other_error"
                elif d.get("type") == "system" and d.get("subtype") == "api_error":
                    blob = json.dumps(d.get("error"))
                    if re.search(r"429|rate_limit|rate limit", blob, re.I) or (d.get("error") or {}).get("rateLimits"):
                        kind = "retry_429"
                        txt = blob[:160]
                if kind:
                    events.append(dict(provider="claude", ts=t, ts_iso=fmt(t), kind=kind, status=status or "",
                                       session=d.get("sessionId", ""), cwd=d.get("cwd", ""),
                                       file=os.path.relpath(f, root), text=txt[:160].replace("\n", " ")))

    codex_state = "pending (Claude-only run; set S6_SCAN_CODEX=1 to scan ~/.codex/sessions)"
    if os.environ.get("S6_SCAN_CODEX") == "1":
        codex_state = "scanned"
        croot = os.path.expanduser("~/.codex/sessions")
        cre = re.compile(r"usage_limit|rate_limit|\b429\b|usage limit|hit your .{0,40}limit", re.I)
        for f in glob.glob(os.path.join(croot, "**", "*.jsonl"), recursive=True):
            try:
                if os.path.getmtime(f) < start:
                    continue
                lines = open(f, errors="ignore").read().splitlines()
            except OSError:
                continue
            cwd = ""
            if lines:
                try:
                    cwd = json.loads(lines[0]).get("payload", {}).get("cwd", "")
                except Exception:
                    pass
            if not any(os.path.realpath(cwd or "/x") .startswith(r) for r in repos):
                continue
            for line in lines:
                if not cre.search(line):
                    continue
                try:
                    d = json.loads(line)
                except Exception:
                    continue
                t = parse_ts(d.get("timestamp", ""))
                if t is None or t < start or t > end:
                    continue
                if d.get("type") in ("event_msg", "error") or "error" in line[:200].lower():
                    kind = "usage_limit" if re.search(r"usage.?limit|hit your", line, re.I) else "http_429"
                    events.append(dict(provider="codex", ts=t, ts_iso=fmt(t), kind=kind, status="", session="",
                                       cwd=cwd, file=os.path.relpath(f, croot), text=line[:160]))

    events.sort(key=lambda e: e["ts"])
    with open(os.path.join(out, "limit-events.csv"), "w", newline="") as fh:
        w = csv.writer(fh)
        w.writerow(["provider", "ts_iso", "kind", "status", "session", "cwd", "file", "text"])
        for e in events:
            w.writerow([e[k] for k in ("provider", "ts_iso", "kind", "status", "session", "cwd", "file", "text")])

    # --- tmux pane capture fallback ---------------------------------------
    cap_dir = os.path.join(out, "pane-captures")
    os.makedirs(cap_dir, exist_ok=True)
    seen_ids = set()
    sp = os.path.join(out, "panes-seen.tsv")
    if os.path.exists(sp):
        for line in open(sp):
            seen_ids.add(line.split("\t")[0])
    fmtp = "#{pane_id}\t#{pane_current_path}"
    try:
        lst = subprocess.run(["tmux", "list-panes", "-a", "-F", fmtp], capture_output=True, text=True, timeout=20).stdout
    except Exception:
        lst = ""
    stamp = datetime.datetime.now().strftime("%H%M%S")
    pane_hits = []
    panes_checked = 0
    for line in lst.splitlines():
        pid, _, path = line.partition("\t")
        rp = os.path.realpath(path)
        if pid in seen_ids or any(rp == r or rp.startswith(r + "/") for r in repos):
            panes_checked += 1
            try:
                cap = subprocess.run(["tmux", "capture-pane", "-p", "-J", "-t", pid, "-S", "-3000"],
                                     capture_output=True, text=True, timeout=20).stdout
            except Exception:
                continue
            safe = pid.replace("%", "p")
            with open(os.path.join(cap_dir, f"{stamp}-{safe}.txt"), "w") as fh:
                fh.write(cap)
            for ln in cap.splitlines():
                if PANE_RE.search(ln):
                    pane_hits.append((pid, ln.strip()[:200]))
    with open(os.path.join(out, "limit-events-panes.txt"), "w") as fh:
        for pid, ln in pane_hits:
            fh.write(f"{pid}\t{ln}\n")

    # --- summary ------------------------------------------------------------
    kinds = ("usage_limit", "http_429", "retry_429", "server_error", "other_error")
    lines = [f"window: {fmt(start)} -> {fmt(end)}  ({(end - start) / 3600:.2f} h)",
             f"claude session files scanned: {len(files)}"]
    for prov in ("claude", "codex"):
        ev = [e for e in events if e["provider"] == prov]
        if prov == "codex" and codex_state.startswith("pending"):
            lines.append(f"codex: {codex_state}")
            continue
        counts = {k: sum(1 for e in ev if e["kind"] == k) for k in kinds}
        lines.append(f"{prov}: " + " ".join(f"{k}={v}" for k, v in counts.items()))
        first = next((e for e in ev if e["kind"] == "usage_limit"), None)
        if first:
            lines.append(f"{prov}: first usage limit at {first['ts_iso']} = {(first['ts'] - start) / 60:.1f} min after run start ({first['text'][:100]})")
        else:
            lines.append(f"{prov}: no usage limit event in window")
    lines.append(f"tmux pane fallback: panes captured={panes_checked} limit-phrase lines={len(pane_hits)} (see limit-events-panes.txt)")
    summary = "\n".join(lines)
    with open(os.path.join(out, "limit-summary.txt"), "w") as fh:
        fh.write(summary + "\n")
    print(summary)


if __name__ == "__main__":
    main()
