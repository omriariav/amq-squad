#!/usr/bin/env bash
# sample-ram.sh RUN_ID REPO_PATH [REPO_PATH...]
#
# Every INTERVAL seconds (default 10) append one row to
# ~/.amq-squad/spike/s6/RUN_ID/ram.csv: the sum of resident memory over every
# seat process tree (tmux pane pids whose cwd is inside a repo path, plus all
# descendants) and a vm_stat summary. Per-seat rows go to ram-seats.csv.
# Runs until it is killed or OUT/stop-sampler exists.
#
#   ONCE=1                 take one sample and exit (dry run)
#   INTERVAL=10            seconds between samples
#   S6_TMUX_RE=regex       also count panes whose tmux session name matches
#   sample-ram.sh --summary RUN_ID REPO_PATH...   peak and averages from ram.csv
#
# RSS summed over trees double counts pages shared between processes; treat
# the peak as an upper bound and compare it with the sys_* columns.
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
. "$HERE/lib.sh"

SUMMARY=0
if [ "${1:-}" = "--summary" ]; then SUMMARY=1; shift; fi
s6_args "$@"

if [ "$SUMMARY" = 1 ]; then
  python3 -I - "$OUT" <<'PY'
import csv, sys, os, datetime
out = sys.argv[1]
if not os.path.exists(os.path.join(out, "ram.csv")):
    sys.exit("no ram.csv yet (sampler not started?)")
rows = list(csv.DictReader(open(os.path.join(out, "ram.csv"))))
if not rows:
    sys.exit("no samples")
f = lambda r, k: float(r[k]) if r[k] not in ("", None) else 0.0
peak = max(rows, key=lambda r: f(r, "seat_rss_mb"))
t = lambda r: datetime.datetime.fromtimestamp(int(r["ts_epoch"])).strftime("%H:%M:%S")
print(f"samples={len(rows)} first={t(rows[0])} last={t(rows[-1])}")
print(f"peak_seat_rss_mb={f(peak,'seat_rss_mb')} at {t(peak)} (panes={peak['seat_panes']} procs={peak['seat_procs']})")
print(f"avg_seat_rss_mb={sum(f(r,'seat_rss_mb') for r in rows)/len(rows):.1f}")
print(f"max_swap_used_mb={max(f(r,'swap_used_mb') for r in rows)}  min_mem_free_pct={min(f(r,'mem_free_pct') for r in rows)}")
print(f"max_sys_compressed_mb={max(f(r,'sys_compressed_mb') for r in rows)}  pageouts_delta={int(f(rows[-1],'sys_pageouts')-f(rows[0],'sys_pageouts'))}")
sp = os.path.join(out, "ram-seats.csv")
if os.path.exists(sp):
    best = {}
    for r in csv.DictReader(open(sp)):
        k = (r["seat_label"], r["pane_id"])
        best[k] = max(best.get(k, 0.0), float(r["rss_mb"]))
    for (label, pane), v in sorted(best.items(), key=lambda x: -x[1]):
        print(f"  seat {label:14s} pane {pane:6s} peak_rss_mb={v}")
PY
  exit 0
fi

INTERVAL="${INTERVAL:-10}"
echo "sampling every ${INTERVAL}s into $OUT/ram.csv for: ${REPOS[*]}" >&2
while :; do
  python3 -I "$HERE/sample_once.py" "$OUT" "${REPOS[@]}" >/dev/null || echo "sample failed at $(date +%T)" >&2
  [ "${ONCE:-0}" = 1 ] && break
  [ -e "$OUT/stop-sampler" ] && break
  sleep "$INTERVAL"
done
