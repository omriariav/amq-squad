#!/usr/bin/env bash
# task-times.sh watch|report RUN_ID REPO_PATH [REPO_PATH...]
#
# Wall time per task, and in total, from the v2.31 task store
# (REPO/.amq-squad/tasks/<session>/t*.json, session read from REPO/.amq-squad/team.json).
#
#   watch   loop every INTERVAL s (default 10), appending task state changes to
#           ~/.amq-squad/spike/s6/RUN_ID/task-snapshots.jsonl. REQUIRED during the run:
#           the store drops a task's lease (the claim time) when the task is
#           completed or failed, so the claim time is only visible while it is in_progress.
#           Ends when OUT/stop-sampler exists. ONCE=1 takes one snapshot and exits.
#   report  merge the final store, the snapshots and (unless S6_NO_GH=1) `gh pr list`
#           into tasks.csv and tasks-report.txt, and print the table plus totals.
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
. "$HERE/lib.sh"
MODE="${1:-}"; [ "$#" -gt 0 ] && shift
case "$MODE" in watch|report) ;; *) echo "usage: task-times.sh watch|report RUN_ID REPO_PATH..." >&2; exit 64;; esac
s6_args "$@"

if [ "$MODE" = report ]; then
  exec python3 -I "$HERE/task_times.py" report "$OUT" "${REPOS[@]}"
fi

INTERVAL="${INTERVAL:-10}"
echo "watching task stores every ${INTERVAL}s into $OUT/task-snapshots.jsonl" >&2
while :; do
  python3 -I "$HERE/task_times.py" snapshot "$OUT" "${REPOS[@]}" >/dev/null || echo "snapshot failed at $(date +%T)" >&2
  [ "${ONCE:-0}" = 1 ] && break
  [ -e "$OUT/stop-sampler" ] && break
  sleep "$INTERVAL"
done
