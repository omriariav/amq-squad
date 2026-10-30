#!/usr/bin/env bash
# window-note.sh start|end|show|note RUN_ID REPO_PATH [REPO_PATH...]
#
#   start  record the run start (now) and the subscription window reference in
#          ~/.amq-squad/spike/s6/RUN_ID/run-start.{epoch,iso} AND append a section to
#          docs/v3-spike/spike-log.md. Refuses (exit 3) to start before the fresh window
#          opens (window reference + 5 h) unless S6_ALLOW_EARLY=1.
#   end    record run-end.{epoch,iso} and append the end entry (elapsed, why it ended: REASON env).
#   show   print the recorded window numbers.
#   note   append one timestamped bullet (NOTE="text") to the S6 section of the spike log.
#
# Window reference: the first Claude agent of the spike launched at 2026-10-21 00:05 local
# (spike-log.md, 00:05 entry), so the 5-hour subscription window resets at 05:05.
#   WINDOW_REF="2026-10-21 00:05"   override the reference (local time)
#   SPIKELOG=path                   override the log file (tests)
#   REASON="21/21 tasks have a PR or a recorded failure"   for `end`
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
. "$HERE/lib.sh"
MODE="${1:-}"; [ "$#" -gt 0 ] && shift
case "$MODE" in start|end|show|note) ;; *) echo "usage: window-note.sh start|end|show|note RUN_ID REPO_PATH..." >&2; exit 64;; esac
s6_args "$@"

SPIKELOG="${SPIKELOG:-$HERE/../../docs/v3-spike/spike-log.md}"
WINDOW_REF="${WINDOW_REF:-2026-10-21 00:05}"
ref_epoch="$(date -j -f '%Y-%m-%d %H:%M:%S' "$WINDOW_REF:00" +%s)"
reset_epoch=$((ref_epoch + 5 * 3600))
fmt() { date -r "$1" '+%Y-%m-%d %H:%M:%S %Z'; }
hm() { date -r "$1" '+%H:%M'; }

case "$MODE" in
  note)
    printf -- '- %s %s\n' "$(date +%H:%M)" "${NOTE:?set NOTE=text}" >> "$SPIKELOG"
    echo "noted in $SPIKELOG"
    ;;
  show)
    echo "window reference: $(fmt "$ref_epoch")   fresh window opens: $(fmt "$reset_epoch")"
    [ -s "$OUT/run-start.epoch" ] && echo "run start: $(fmt "$(cat "$OUT/run-start.epoch")")" || echo "run start: not recorded"
    [ -s "$OUT/run-end.epoch" ] && echo "run end:   $(fmt "$(cat "$OUT/run-end.epoch")")" || true
    ;;
  start)
    now="$(date +%s)"
    if [ -s "$OUT/run-start.epoch" ]; then
      echo "run start already recorded for $RUN_ID: $(fmt "$(cat "$OUT/run-start.epoch")") (delete $OUT/run-start.epoch to redo)" >&2
      exit 4
    fi
    early=""
    if [ "$now" -lt "$reset_epoch" ]; then
      if [ "${S6_ALLOW_EARLY:-0}" != 1 ]; then
        echo "REFUSING: now is $(fmt "$now"), the fresh window opens $(fmt "$reset_epoch"). Start at or after $(hm "$reset_epoch")." >&2
        exit 3
      fi
      early=" (EARLY START, S6_ALLOW_EARLY=1: not a fresh window; numbers are not valid for criterion 5)"
    fi
    echo "$now" > "$OUT/run-start.epoch"
    fmt "$now" > "$OUT/run-start.iso"
    {
      printf '\n## S6 run `%s`\n\n' "$RUN_ID"
      printf -- '- %s Run start%s. Window reference %s (first Claude agent of the spike); the 5-hour window reset was due at %s. Repos: %s.\n' \
        "$(hm "$now")" "$early" "$(fmt "$ref_epoch")" "$(hm "$reset_epoch")" "${REPOS[*]}"
      printf -- '- Samplers write to %s. Time to first usage limit is measured from this start.\n' "$OUT"
    } >> "$SPIKELOG"
    echo "recorded run start $(fmt "$now") for $RUN_ID; appended to $SPIKELOG"
    ;;
  end)
    [ -s "$OUT/run-start.epoch" ] || { echo "no run start recorded for $RUN_ID" >&2; exit 4; }
    now="$(date +%s)"
    echo "$now" > "$OUT/run-end.epoch"
    fmt "$now" > "$OUT/run-end.iso"
    el=$((now - $(cat "$OUT/run-start.epoch")))
    printf -- '- %s Run end for `%s`: elapsed %dh%02dm. %s\n' "$(hm "$now")" "$RUN_ID" $((el / 3600)) $(((el % 3600) / 60)) "${REASON:-reason not given}" >> "$SPIKELOG"
    echo "recorded run end $(fmt "$now") (elapsed $((el / 60)) min); appended to $SPIKELOG"
    ;;
esac
