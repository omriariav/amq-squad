#!/usr/bin/env bash
# selftest-pane.sh: dry run of pane-check.sh with throwaway tmux panes (no seats).
# One pane prints a folder-trust prompt, one prints normal output; expects exit 2 and 1 flag.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export S6_OUT_BASE="${S6_OUT_BASE:-$HOME/.amq-squad/spike/s6}"
RUN=dryrun-pane
rm -rf "${S6_OUT_BASE:?}/$RUN"
T="$(mktemp -d)"; T="$(cd "$T" && pwd -P)"
mkdir -p "$T/repo"
printf '#!/bin/sh\necho "Do you trust the files in this folder?"\necho "1. Yes, proceed"\nsleep 60\n' > "$T/trust.sh"
printf '#!/bin/sh\necho "Welcome. Reading the brief."\nsleep 60\n' > "$T/calm.sh"
chmod +x "$T/trust.sh" "$T/calm.sh"
tmux new-session -d -s s6dry-pane -c "$T/repo" "$T/trust.sh"
tmux new-window -t s6dry-pane -c "$T/repo" "$T/calm.sh"
trap 'tmux kill-session -t s6dry-pane 2>/dev/null || true; rm -rf "$T"' EXIT
sleep 1
"$HERE/pane-check.sh" "$RUN" "$T/repo"
rc=$?
echo "exit=$rc"
if [ "$rc" -eq 2 ]; then echo "selftest-pane OK"; else echo "selftest-pane FAIL"; exit 1; fi
