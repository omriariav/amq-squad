#!/bin/sh
# Bounded real-provider run for brief 2b-2 (criterion 4 re-run, role matrix N=1, pointer arm).
# Usage: step.sh before|init|launch [DECISION]|capture|teardown|after
W=/Users/omri/code/amq-squad/.claude/worktrees/agent-aad179a2686dbf79e
R=/tmp/2b2run
E=$R/evidence
mkdir -p "$E"
export XDG_STATE_HOME=$R/state          # AMQ trust store: not the owner's
export TMUX_TMPDIR=$R/tmux              # private tmux server
export AMQ_NO_UPDATE_CHECK=1
mkdir -p "$TMUX_TMPDIR"
unset AM_ME AM_ROOT AM_BASE_ROOT AM_SESSION AM_ROOT_ID TMUX
owner_files="$HOME/.claude/settings.json $HOME/.codex/config.toml $HOME/.claude.json"
case "$1" in
before|after)
  {
    date -u +%Y-%m-%dT%H:%M:%SZ
    shasum -a 256 $owner_files
    for f in "$HOME/.amq-squad/preflight/claude-settings.json" "$HOME/.amq-squad/preflight/codex-config.toml"; do
      if [ -f "$f" ]; then shasum -a 256 "$f"; else echo "absent $f"; fi
    done
  } | sed "s|$HOME|~|g" > "$E/hashes-$1.txt"
  cat "$E/hashes-$1.txt"
  ;;
versions)
  { amq --version; claude --version; codex --version; tmux -V; } > "$E/versions.txt" 2>&1
  cat "$E/versions.txt"
  ;;
init)
  cd "$W" || exit 1
  printf '{"root":".agent-mail"}\n' > .amqrc && chmod 600 .amqrc
  amq init --root "$W/.agent-mail" --agents operator,c4-claude,c4-codex > "$E/init.txt" 2>&1
  amq session create c4 --root "$W/.agent-mail" --me operator --json > "$E/session-create.json" 2>&1
  cat "$E/init.txt" "$E/session-create.json" | sed "s|$HOME|~|g"
  ;;
launch)
  cd "$W" || exit 1
  date -u +%Y-%m-%dT%H:%M:%SZ >> "$E/launch-times.txt"
  if [ -n "$2" ]; then
    $R/bin/amq-squad launch --seats $R/seats.json --decision "$2" --wait 60s > "$E/launch-apply.txt" 2>&1; echo "exit=$?" >> "$E/launch-apply.txt"
    $R/bin/amq-squad launch --seats $R/seats.json --dry-run --json > /dev/null 2>&1
    cat "$E/launch-apply.txt"
  else
    $R/bin/amq-squad launch --seats $R/seats.json > "$E/launch-prepare.txt" 2>&1; echo "exit=$?" >> "$E/launch-prepare.txt"
    cat "$E/launch-prepare.txt"
  fi
  ;;
capture)
  tmux list-panes -a -F '#{session_name} #{pane_id} #{pane_pid} #{pane_current_command} #{@amq_pane_agent}' > "$E/list-panes.txt" 2>&1
  cat "$E/list-panes.txt"
  for p in $(tmux list-panes -a -F '#{pane_id}'); do
    tmux capture-pane -p -t "$p" -S -200 > "$E/pane-$(echo $p | tr -d %).txt" 2>&1
  done
  ls "$E"
  ;;
teardown)
  tmux kill-server 2>&1
  sleep 2
  ps -axo pid,command | grep -E 'seat-exec|c4-claude|c4-codex|__codex-notify|2b2run' | grep -v grep > "$E/ps-after-teardown.txt"
  echo "lines: $(wc -l < "$E/ps-after-teardown.txt")"
  cat "$E/ps-after-teardown.txt"
  rm -f "$W/.amqrc"
  ;;
esac
