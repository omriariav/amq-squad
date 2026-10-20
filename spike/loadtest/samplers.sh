#!/usr/bin/env bash
# samplers.sh start|stop|status RUN_ID REPO_PATH [REPO_PATH...]
#
# Runs the three background loops for a run, detached (nohup), pid files and logs under
# ~/.amq-squad/spike/s6/RUN_ID/:
#   sample-ram.sh      every 10 s            -> ram.csv, ram-seats.csv
#   task-times.sh watch every 10 s           -> task-snapshots.jsonl
#   scan-limits.sh     every 300 s (LIMITS_EVERY) -> limit-events*.{csv,txt}, limit-scan.log
# `stop` creates OUT/stop-sampler (the loops exit on their own within one interval) and
# kills any survivor. Starting a second time while pid files are live is refused.
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
. "$HERE/lib.sh"
MODE="${1:-}"; [ "$#" -gt 0 ] && shift
case "$MODE" in start|stop|status) ;; *) echo "usage: samplers.sh start|stop|status RUN_ID REPO_PATH..." >&2; exit 64;; esac
s6_args "$@"
LIMITS_EVERY="${LIMITS_EVERY:-300}"

alive() { [ -s "$1" ] && kill -0 "$(cat "$1")" 2>/dev/null; }

case "$MODE" in
  start)
    for n in ram tasks limits; do
      alive "$OUT/$n.pid" && { echo "$n sampler already running (pid $(cat "$OUT/$n.pid"))" >&2; exit 4; }
    done
    rm -f "$OUT/stop-sampler"
    nohup "$HERE/sample-ram.sh" "$RUN_ID" "${REPOS[@]}" >>"$OUT/ram.log" 2>&1 &
    echo $! > "$OUT/ram.pid"
    nohup "$HERE/task-times.sh" watch "$RUN_ID" "${REPOS[@]}" >>"$OUT/tasks.log" 2>&1 &
    echo $! > "$OUT/tasks.pid"
    OUT="$OUT" nohup bash -c 'while :; do "$0" "$1" "${@:2}" >>"$OUT/limit-scan.log" 2>&1 || true; [ -e "$OUT/stop-sampler" ] && break; for i in $(seq 1 '"$LIMITS_EVERY"'); do sleep 1; [ -e "$OUT/stop-sampler" ] && break 2; done; done' \
      "$HERE/scan-limits.sh" "$RUN_ID" "${REPOS[@]}" >/dev/null 2>&1 &
    echo $! > "$OUT/limits.pid"
    sleep 2
    echo "started: ram pid $(cat "$OUT/ram.pid"), tasks pid $(cat "$OUT/tasks.pid"), limits pid $(cat "$OUT/limits.pid")  -> $OUT"
    ;;
  status)
    for n in ram tasks limits; do
      if alive "$OUT/$n.pid"; then echo "$n: running (pid $(cat "$OUT/$n.pid"))"; else echo "$n: not running"; fi
    done
    [ -s "$OUT/ram.csv" ] && echo "ram.csv: $(($(wc -l < "$OUT/ram.csv") - 1)) samples, last: $(tail -1 "$OUT/ram.csv")" || echo "ram.csv: empty"
    [ -s "$OUT/task-snapshots.jsonl" ] && echo "task-snapshots: $(wc -l < "$OUT/task-snapshots.jsonl") change records" || echo "task-snapshots: empty"
    [ -s "$OUT/limit-summary.txt" ] && sed 's/^/limits: /' "$OUT/limit-summary.txt" | head -6 || true
    ;;
  stop)
    touch "$OUT/stop-sampler"
    sleep "${STOP_WAIT:-12}"
    for n in ram tasks limits; do
      if alive "$OUT/$n.pid"; then kill "$(cat "$OUT/$n.pid")" 2>/dev/null || true; echo "$n: killed"; else echo "$n: stopped"; fi
      rm -f "$OUT/$n.pid"
    done
    ;;
esac
