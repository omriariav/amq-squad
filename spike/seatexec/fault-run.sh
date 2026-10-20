#!/bin/sh
# Drive one PreToolUse fault trial in the live Claude seat.
# Usage: fault-run.sh PANE MODE     MODE: ok|deny|sleep|exit1|missing
# Sets the hook mode (or removes the hook command for "missing"), sends one
# prompt that forces a single harmless Bash call, waits, saves the pane.
set -eu
pane="$1"; mode="$2"
S4="$HOME/.amq-squad/spike/s4"
hooks="$S4/project/.claude/hooks"
ev="$S4/evidence"
if [ "$mode" = "missing" ]; then
  [ -f "$hooks/pretool.py" ] && mv "$hooks/pretool.py" "$ev/pretool.py.parked"
  echo ok > "$ev/pretool-mode"
else
  [ -f "$hooks/pretool.py" ] || mv "$ev/pretool.py.parked" "$hooks/pretool.py"
  echo "$mode" > "$ev/pretool-mode"
fi
python3 -c 'import time; print("SENT", time.time())' >> "$ev/fault-$mode.txt"
tmux send-keys -t "$pane" -l "Use the Bash tool exactly once to run: ls -1 # s4-fault-$mode . Report what happened, then stop."
sleep 0.3
tmux send-keys -t "$pane" Enter
sleep "${WAIT:-45}"
tmux capture-pane -p -S -60 -t "$pane" > "$ev/pane-fault-$mode.txt"
