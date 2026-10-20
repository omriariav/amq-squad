#!/usr/bin/env bash
# analysis.sh RUN_ID REPO_PATH [REPO_PATH...]
#
# Runs every report after the run and prints the five numbers for docs/v3-spike/S6-report.md:
#   1. peak RAM   2. 429 + usage-limit events   3. wall time per task and total
#   4. merge conflicts   5. time to first usage limit
# Writes five-numbers.txt and per-task-table.md (markdown rows for the report) under
# ~/.amq-squad/spike/s6/RUN_ID/. Read-only on GitHub and on the repos (mergeability.sh makes a
# temporary worktree and removes it). Set SKIP_MERGE=1 to skip the PR/merge step.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
. "$HERE/lib.sh"
s6_args "$@"
{
  echo "== run $RUN_ID  ($(cat "$OUT/run-start.iso" 2>/dev/null || echo 'start not recorded') -> $(cat "$OUT/run-end.iso" 2>/dev/null || echo 'end not recorded'))"
  echo
  echo "== 1. peak RAM (sum of RSS over seat process trees; vm_stat alongside)"
  "$HERE/sample-ram.sh" --summary "$RUN_ID" "${REPOS[@]}" 2>&1
  echo
  echo "== 2 + 5. 429 and usage-limit events, time to first usage limit"
  "$HERE/scan-limits.sh" "$RUN_ID" "${REPOS[@]}" 2>&1
  echo
  echo "== 3. wall time per task and total"
  "$HERE/task-times.sh" report "$RUN_ID" "${REPOS[@]}" 2>&1
  echo
  echo "== 4. merge conflicts"
  if [ "${SKIP_MERGE:-0}" = 1 ]; then echo "skipped (SKIP_MERGE=1)"; else "$HERE/mergeability.sh" "$RUN_ID" "${REPOS[@]}" 2>&1; fi
} | tee "$OUT/five-numbers.txt"

python3 -I - "$OUT" <<'PY'
import csv, os, sys
out = sys.argv[1]
def rd(n):
    p = os.path.join(out, n)
    return list(csv.DictReader(open(p))) if os.path.exists(p) else []
tasks, merge = rd("tasks.csv"), {(r["repo"], r["task"]): r for r in rd("mergeability.csv")}
def d(s):
    if not s: return "-"
    s = int(s); return f"{s//3600}h{(s%3600)//60:02d}m" if s >= 3600 else f"{s//60}m{s%60:02d}s"
lines = ["| Task | Repo | Outcome | Seat | Wall time | PR | GitHub mergeable | Solo dry-run | Cumulative dry-run | Notes |", "|---|---|---|---|---|---|---|---|---|---|"]
for t in tasks:
    m = merge.get((t["repo"], t["task"]), {})
    pr = t["pr_url"] or ""
    note = t["failure_reason"] or m.get("flags", "")
    lines.append(f"| {t['task']} | {t['repo']} | {t['status']} | {t['assignee'] or '-'} | {d(t['wall_sec'])} | {pr or '-'} | "
                 f"{m.get('gh_mergeable','-') or '-'} | {m.get('solo','-')} | {m.get('cumulative','-')} | {note} |")
open(os.path.join(out, "per-task-table.md"), "w").write("\n".join(lines) + "\n")
print(f"\nper-task table rows: {len(tasks)} -> {out}/per-task-table.md")
PY
