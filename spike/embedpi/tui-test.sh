#!/bin/bash
# S3 probe: start the embedded pi as an interactive lead in a detached tmux
# session (stripped PATH), wait for the TUI to draw, send one prompt, capture
# the pane, then kill the session. Requires tmux (build/test host only).
set -u
cand="${1:-bun}"
here="$(cd "$(dirname "$0")" && pwd)"
s="s3tui-$cand"
tmux="$(command -v tmux)"
cd "$HOME/.amq-squad/spike/s3/cwd"
"$tmux" kill-session -t "$s" 2>/dev/null
"$tmux" new-session -d -s "$s" -x 120 -y 40 \
  "/usr/bin/env -i HOME=$HOME PATH=/usr/bin:/bin TERM=xterm-256color PI_CODING_AGENT_DIR=$HOME/.amq-squad/spike/s3/agent PI_OFFLINE=1 PI_SKIP_VERSION_CHECK=1 PI_TELEMETRY=0 $here/bin/embedpi-$cand --provider ollama --model qwen2.5:32b --no-session --no-extensions --no-skills --no-context-files --no-mcp --no-tools --thinking off; echo PI_EXITED=\$?; read"
wait_for() {  # poll the pane for a pattern, up to $2 tenths of a second
  for _ in $(seq 1 "$2"); do
    "$tmux" capture-pane -p -t "$s" | grep -q "$1" && return 0
    /bin/sleep 0.1
  done
  return 1
}
if wait_for 'qwen2.5' 100; then echo "tui_drawn=yes"; else echo "tui_drawn=no"; fi
"$tmux" send-keys -t "$s" 'What is 17 plus 25? Reply with just the number.' Enter
if wait_for '^ *42 *$' 600; then echo "answered=yes"; else echo "answered=no"; fi
echo "----- pane -----"
"$tmux" capture-pane -p -t "$s" | sed -e '/^[[:space:]]*$/d' | tail -25
"$tmux" kill-session -t "$s"
