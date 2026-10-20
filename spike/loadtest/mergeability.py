#!/usr/bin/env python3
"""PR mergeability for the S6 load test. Called by mergeability.sh.

usage: mergeability.py OUT_DIR REPO [REPO...]

For every PR whose head branch starts with loadtest/ (all states are listed, only OPEN ones
are merge-tested):
  1. `gh pr view N --json mergeable,mergeStateStatus,files,...` (retried while UNKNOWN)
  2. dry-run merge in a TEMPORARY git worktree (detached at the base commit), never pushing:
       solo        the PR head merged alone onto the base
       cumulative  all open PRs merged in PR-number order onto the base, committing each clean
                   one inside the temp worktree so later PRs see it; a PR that conflicts is
                   aborted, recorded, and skipped
     after the cumulative pass, `go build ./...` runs in the temp worktree (semantic conflicts)
  3. flags: files outside the task's listed files, CLAUDE.md/AGENTS.md/.amq* staged, N3 touching Makefile
Fetches `pull/N/head` and the base branch from origin into FETCH_HEAD only (no refs or branches
are created in the repo). The temp worktree is removed at the end.
Env: S6_GH (gh binary), S6_TASKS_TSV, S6_TMPDIR.
"""
import csv
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
import time

HERE = os.path.dirname(os.path.abspath(__file__))
GH = os.environ.get("S6_GH", "gh")
IDENT = ["-c", "user.name=s6-dryrun", "-c", "user.email=s6-dryrun@example.invalid", "-c", "commit.gpgsign=false"]


def run(cmd, cwd=None, timeout=300):
    r = subprocess.run(cmd, cwd=cwd, capture_output=True, text=True, timeout=timeout)
    return r.returncode, r.stdout, r.stderr


def git(repo, *args, timeout=300):
    return run(["git", "-C", repo, *IDENT, *args], timeout=timeout)


def slug_of(repo):
    _, url, _ = git(repo, "remote", "get-url", "origin")
    return re.sub(r"\.git$", "", re.sub(r"^(https://github.com/|git@github.com:)", "", url.strip()))


def expected_files():
    path = os.environ.get("S6_TASKS_TSV", os.path.join(HERE, "tasks", "tasks.tsv"))
    out = {}
    try:
        for r in csv.DictReader(open(path, newline=""), delimiter="\t", quoting=csv.QUOTE_NONE):
            toks = []
            for part in re.split(r";| OR ", r["files"]):
                part = re.sub(r"\(.*?\)", "", part).strip()
                if part:
                    toks.append(part)
            out[r["id"]] = toks
    except Exception:
        pass
    return out


def conflicted_files(wt):
    _, o, _ = git(wt, "diff", "--name-only", "--diff-filter=U")
    return [x for x in o.split("\n") if x]


def reset_clean(wt, base_sha):
    git(wt, "merge", "--abort")
    git(wt, "reset", "--hard", "-q", base_sha)
    git(wt, "clean", "-fdq")


def main():
    out = sys.argv[1]
    repos = [os.path.realpath(r) for r in sys.argv[2:]]
    exp = expected_files()
    rows, notes = [], []
    for repo in repos:
        name = os.path.basename(repo)
        slug = slug_of(repo)
        rc, o, e = run([GH, "pr", "list", "--repo", slug, "--state", "all", "--limit", "200", "--json",
                        "number,headRefName,baseRefName,url,state,isDraft,title"])
        if rc != 0:
            notes.append(f"{name}: gh pr list failed: {e.strip()[:200]}")
            continue
        prs = sorted([p for p in json.loads(o or "[]") if p["headRefName"].startswith("loadtest/")], key=lambda p: p["number"])
        if not prs:
            notes.append(f"{name}: no loadtest/* PRs")
            continue
        info = {}
        for p in prs:
            for attempt in range(4):
                rc, o, e = run([GH, "pr", "view", str(p["number"]), "--repo", slug, "--json",
                                "mergeable,mergeStateStatus,files,headRefOid,baseRefName,additions,deletions"])
                d = json.loads(o) if rc == 0 and o.strip() else {}
                if d.get("mergeable") != "UNKNOWN":
                    break
                time.sleep(3)
            info[p["number"]] = d

        # --- dry-run merges in a temp worktree ----------------------------
        solo, cumu, conflicts_solo, conflicts_cumu, build = {}, {}, {}, {}, "n/a"
        openprs = [p for p in prs if p["state"] == "OPEN"]
        tmp = None
        if openprs:
            base = openprs[0]["baseRefName"]
            rc, _, e = git(repo, "fetch", "--no-tags", "origin", base)
            _, base_sha, _ = git(repo, "rev-parse", "FETCH_HEAD")
            base_sha = base_sha.strip()
            heads = {}
            for p in openprs:
                rc, _, e = git(repo, "fetch", "--no-tags", "origin", f"pull/{p['number']}/head")
                _, sha, _ = git(repo, "rev-parse", "FETCH_HEAD")
                heads[p["number"]] = sha.strip() if rc == 0 else ""
            tmp = tempfile.mkdtemp(prefix="s6-merge-", dir=os.environ.get("S6_TMPDIR"))
            wt = os.path.join(tmp, "wt")
            rc, _, e = git(repo, "worktree", "add", "--detach", wt, base_sha)
            if rc != 0:
                notes.append(f"{name}: temp worktree failed: {e.strip()[:200]}")
            else:
                try:
                    for p in openprs:  # solo
                        n, sha = p["number"], heads[p["number"]]
                        if not sha:
                            solo[n] = "FETCH_FAILED"
                            continue
                        rc, _, _ = git(wt, "merge", "--no-commit", "--no-ff", sha)
                        if rc != 0:
                            solo[n] = "CONFLICT"
                            conflicts_solo[n] = conflicted_files(wt)
                        else:
                            solo[n] = "clean"
                        reset_clean(wt, base_sha)
                    for p in openprs:  # cumulative
                        n, sha = p["number"], heads[p["number"]]
                        if not sha:
                            cumu[n] = "FETCH_FAILED"
                            continue
                        rc, _, _ = git(wt, "merge", "--no-commit", "--no-ff", sha)
                        if rc != 0:
                            cumu[n] = "CONFLICT"
                            conflicts_cumu[n] = conflicted_files(wt)
                            git(wt, "merge", "--abort")
                            git(wt, "reset", "--hard", "-q", "HEAD")
                        else:
                            cumu[n] = "clean"
                            git(wt, "commit", "-q", "--no-edit", "--allow-empty")
                    if os.path.exists(os.path.join(wt, "go.mod")):
                        rc, _, e = run(["go", "build", "./..."], cwd=wt, timeout=600)
                        build = "ok" if rc == 0 else "FAIL: " + e.strip().splitlines()[-1][:160] if e.strip() else "FAIL"
                finally:
                    git(repo, "worktree", "remove", "--force", wt)
                    git(repo, "worktree", "prune")
            shutil.rmtree(tmp, ignore_errors=True)
        notes.append(f"{name}: cumulative go build after merging clean PRs: {build}")

        for p in prs:
            n = p["number"]
            d = info.get(n, {})
            task = p["headRefName"].split("/", 1)[1]
            files = [f["path"] for f in d.get("files", [])]
            flags = []
            want = exp.get(task, [])
            if want:
                extra = [f for f in files if not any(f == w or f.startswith(w.rstrip("/") + "/") for w in want)]
                if extra:
                    flags.append("outside-task-files:" + ",".join(extra[:5]))
            bad = [f for f in files if f in ("CLAUDE.md", "AGENTS.md") or f.startswith(".amq")]
            if bad and task not in ("N2",):
                flags.append("staged-local-files:" + ",".join(bad))
            if task == "N3" and "Makefile" in files:
                flags.append("N3-touched-Makefile")
            rows.append(dict(repo=name, task=task, pr=n, url=p["url"], state=p["state"], draft=p["isDraft"],
                             gh_mergeable=d.get("mergeable", ""), gh_state=d.get("mergeStateStatus", ""),
                             solo=solo.get(n, "not-tested"), solo_files=";".join(conflicts_solo.get(n, [])),
                             cumulative=cumu.get(n, "not-tested"), cumulative_files=";".join(conflicts_cumu.get(n, [])),
                             nfiles=len(files), flags=" ".join(flags)))

    cols = ["repo", "task", "pr", "url", "state", "draft", "gh_mergeable", "gh_state", "solo", "solo_files",
            "cumulative", "cumulative_files", "nfiles", "flags"]
    with open(os.path.join(out, "mergeability.csv"), "w", newline="") as fh:
        w = csv.writer(fh)
        w.writerow(cols)
        for r in rows:
            w.writerow([r[c] for c in cols])

    lines = [f"{'repo':14s} {'task':4s} {'PR':>5s} {'state':7s} {'gh mergeable':12s} {'gh state':10s} {'solo':9s} {'cumulative':10s} conflicts / flags"]
    for r in rows:
        extra = (r["cumulative_files"] or r["solo_files"])
        lines.append(f"{r['repo']:14s} {r['task']:4s} {r['pr']:>5d} {r['state']:7s} {r['gh_mergeable']:12s} {r['gh_state']:10s} "
                     f"{r['solo']:9s} {r['cumulative']:10s} {extra} {r['flags']}".rstrip())
    openr = [r for r in rows if r["state"] == "OPEN"]
    lines.append("")
    lines.append(f"PRs={len(rows)} open={len(openr)}")
    lines.append(f"gh CONFLICTING={sum(1 for r in openr if r['gh_mergeable'] == 'CONFLICTING')}  "
                 f"solo dry-run conflicts={sum(1 for r in openr if r['solo'] == 'CONFLICT')}  "
                 f"cumulative dry-run conflicts={sum(1 for r in openr if r['cumulative'] == 'CONFLICT')}")
    lines.append(f"flagged PRs={sum(1 for r in rows if r['flags'])}")
    lines.extend(notes)
    text = "\n".join(lines)
    open(os.path.join(out, "mergeability.txt"), "w").write(text + "\n")
    print(text)


if __name__ == "__main__":
    main()
