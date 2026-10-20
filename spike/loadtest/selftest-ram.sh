#!/usr/bin/env bash
# selftest-ram.sh: dry run of sample-ram.sh with a throwaway tmux session (no seats).
# Creates tmux session s6dry-ram running two sleeps under a temp dir, samples it
# three times (the last after the session is gone), prints the CSVs, cleans up.
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export S6_OUT_BASE="${S6_OUT_BASE:-$HOME/.amq-squad/spike/s6}"
RUN=dryrun-ram
rm -rf "${S6_OUT_BASE:?}/$RUN"
T="$(mktemp -d)"
T="$(cd "$T" && pwd -P)"
printf '#!/bin/sh\nsleep 120 &\nsleep 120\n' > "$T/dry.sh"
chmod +x "$T/dry.sh"
tmux new-session -d -s s6dry-ram -c "$T" "$T/dry.sh"
trap 'tmux kill-session -t s6dry-ram 2>/dev/null || true; rm -rf "$T"' EXIT
sleep 1
ONCE=1 "$HERE/sample-ram.sh" "$RUN" "$T"
ONCE=1 "$HERE/sample-ram.sh" "$RUN" "$T"
tmux kill-session -t s6dry-ram
ONCE=1 "$HERE/sample-ram.sh" "$RUN" "$T"
echo "--- ram.csv"; cat "$S6_OUT_BASE/$RUN/ram.csv"
echo "--- ram-seats.csv"; cat "$S6_OUT_BASE/$RUN/ram-seats.csv"
echo "--- panes-seen.tsv"; cat "$S6_OUT_BASE/$RUN/panes-seen.tsv"
echo "--- summary"; "$HERE/sample-ram.sh" --summary "$RUN" "$T"
