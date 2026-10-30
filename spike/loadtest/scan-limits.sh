#!/usr/bin/env bash
# scan-limits.sh RUN_ID REPO_PATH [REPO_PATH...]
#
# 429 and usage-limit events for the run window (OUT/run-start.epoch, written by
# window-note.sh, to OUT/run-end.epoch or now). Claude session JSONL under
# ~/.claude/projects for the repo roots and their .s6-wt worktrees, plus a tmux
# pane capture fallback. Writes limit-events.csv, limit-events-panes.txt,
# limit-summary.txt and pane-captures/ under ~/.amq-squad/spike/s6/RUN_ID/.
# Safe to run repeatedly during the run (idempotent); run it once more before
# the squads are stopped so the pane captures include the final screens.
#
#   S6_SINCE / S6_UNTIL  epoch overrides for the window
#   S6_SCAN_CODEX=1      also scan ~/.codex/sessions (Codex column; off for the Claude-only run)
#   S6_CLAUDE_PROJECTS   override the projects dir (tests)
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
. "$HERE/lib.sh"
s6_args "$@"
exec python3 -I "$HERE/scan_limits.py" "$OUT" "${REPOS[@]}"
