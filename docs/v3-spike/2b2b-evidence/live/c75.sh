#!/bin/sh
# Brief 2b-2 milestone B acceptance 7.5, one real Codex seat (bounded live
# session, lead ruling): the seat changes its worktree under
# --sandbox workspace-write; the harness commit step commits and pushes the
# seat branch to a local bare origin. Fixture under the worktree's ignored
# build/ dir (codex trusts the worktree by ancestor). Steps:
#   c75.sh setup | launch [DECISION] | capture | commit | teardown
W=~/code/amq-squad/.claude/worktrees/agent-a4c9efee2491e72e4/build/scratch-7-5
B=$JOB
E=$B/live/c75
BIN=$B/amq-squad
mkdir -p "$E"
export XDG_STATE_HOME=$B/c75-state
export TMUX_TMPDIR=$B/c75-tmux
export AMQ_NO_UPDATE_CHECK=1
mkdir -p "$TMUX_TMPDIR" "$XDG_STATE_HOME"
unset AM_ME AM_ROOT AM_BASE_ROOT AM_SESSION AM_ROOT_ID TMUX
G="git -c core.hooksPath=/dev/null -c user.name=owner -c user.email=o@local -c commit.gpgsign=false"
case "$1" in
setup)
  rm -rf "$W"; mkdir -p "$W"
  git init -q --bare -b main "$W/origin.git"
  mkdir -p "$W/repo" && cd "$W/repo" || exit 1
  $G init -q -b main
  printf 'note\n' > note.txt
  printf '.agent-mail/\n' > .gitignore
  $G add -A && $G commit -qm fixture
  $G remote add origin "$W/origin.git"
  $G push -q origin main
  $G worktree add -q -b seat/c75 "$W/wt-c75" main
  amq init --root "$W/repo/.agent-mail" --agents operator,c75,scheduler > "$E/init.txt" 2>&1
  amq session create c75 --root "$W/repo/.agent-mail" --me operator --json > "$E/session-create.json" 2>&1
  cat > "$B/c75-role.md" <<'EOF'
# Role: c75, a Codex seat of an amq-squad test squad

Do exactly this, then stop:
1. Append one line with the text codex-7.5 to the file note.txt in the current directory.
2. Run: "$AMQ_SQUAD_BIN" task done t1 --me c75 --evidence "appended codex-7.5 to note.txt"
   If that command is refused by the sandbox, say so in one line.
3. Do not run git commit: the harness commits for you.
Then print ROLE_DONE and stop.
EOF
  cat > "$B/c75-seats.json" <<EOF
{"project_root": "$W/repo", "session": "c75", "operator": "operator",
 "seats": [{"handle": "c75", "runtime": "codex", "model": "gpt-6-astra", "effort": "medium", "role": "implementer",
   "role_file": "$B/c75-role.md", "worktree": "$W/wt-c75", "branch": "seat/c75",
   "initial_input": "Run exactly one shell command: cat \"\$AMQ_SQUAD_ROLE_PATH\". Then do what it says."}]}
EOF
  R="$W/repo/.agent-mail/c75"
  "$BIN" task add --root "$R" --title "append codex-7.5" --class implement --files note.txt --project-path "$W/repo" --me operator > "$E/task-add.txt" 2>&1 || "$BIN" task add --root "$R" --title "append codex-7.5" --class implement --files note.txt --project-path "$W/repo" > "$E/task-add.txt" 2>&1
  "$BIN" task claim t1 --root "$R" --me c75 > "$E/task-claim.txt" 2>&1
  cat "$E/task-add.txt" "$E/task-claim.txt"
  ;;
launch)
  cd "$W/repo" || exit 1
  date -u +%Y-%m-%dT%H:%M:%SZ >> "$E/launch-times.txt"
  if [ -n "$2" ]; then
    "$BIN" launch --seats "$B/c75-seats.json" --decision "$2" --wait 90s > "$E/launch-apply.txt" 2>&1; echo "exit=$?" >> "$E/launch-apply.txt"
    cat "$E/launch-apply.txt"
  else
    "$BIN" launch --seats "$B/c75-seats.json" > "$E/launch-prepare.txt" 2>&1; echo "exit=$?" >> "$E/launch-prepare.txt"
    cat "$E/launch-prepare.txt"
  fi
  ;;
capture)
  tmux list-panes -a -F '#{session_name} #{pane_id} #{pane_pid} #{pane_current_command}' > "$E/list-panes.txt" 2>&1
  for p in $(tmux list-panes -a -F '#{pane_id}'); do
    tmux capture-pane -p -t "$p" -S -300 > "$E/pane-$(echo $p | tr -d %).txt" 2>&1
  done
  cat "$E/list-panes.txt"; tail -40 "$E"/pane-*.txt
  (cd "$W/wt-c75" && git status --porcelain --untracked-files=all; cat note.txt) > "$E/worktree-after-seat.txt" 2>&1
  "$BIN" task show t1 --root "$W/repo/.agent-mail/c75" > "$E/task-after-seat.txt" 2>&1
  cat "$E/worktree-after-seat.txt" "$E/task-after-seat.txt"
  ;;
commit)
  R="$W/repo/.agent-mail/c75"
  "$BIN" seat commit --seat c75 --root "$R" --json > "$E/seat-commit.json" 2>&1; echo "exit=$?" >> "$E/seat-commit.json"
  git -C "$W/origin.git" log --format='%H %an <%ae> | %s' seat/c75 > "$E/origin-log.txt" 2>&1
  git -C "$W/origin.git" log -1 --format=%B seat/c75 >> "$E/origin-log.txt" 2>&1
  git -C "$W/origin.git" show --stat --format= seat/c75 >> "$E/origin-log.txt" 2>&1
  cat "$E/seat-commit.json" "$E/origin-log.txt"
  ;;
teardown)
  tmux kill-server 2>&1
  sleep 2
  ps -axo pid,command | grep -E 'seat-exec|c75|__codex-notify|scratch-7-5' | grep -v grep > "$E/ps-after-teardown.txt"
  echo "lines: $(wc -l < "$E/ps-after-teardown.txt")"; cat "$E/ps-after-teardown.txt"
  ;;
esac
