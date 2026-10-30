#!/bin/bash
# usage: run.sh <label> <cwd> <filename> [extra codex args...]
label=$1; cwd=$2; fname=$3; shift 3
L=~/.amq-squad/spike/s4/adddir
MODEL=${MODEL:-gpt-6-astra}
PROMPT="Create a file named $fname containing the single line ok, run git add $fname and git commit -m 'probe', then print git log --oneline -1. Do not ask questions."
{
  echo "=== run $label ==="
  echo "date: $(date -u +%FT%TZ)"
  echo "codex: $(codex --version)"
  echo "cwd: $cwd"
  echo "model: $MODEL"
  echo "cmd: codex exec --sandbox workspace-write -c approval_policy=never -C $cwd -m $MODEL $* --skip-git-repo-check \"$PROMPT\""
  echo "--- before: git status/log ---"
  git -C "$cwd" status --short; git -C "$cwd" log --oneline -2
} | tee "$L/$label.meta.txt"
codex exec --sandbox workspace-write -c approval_policy=never -C "$cwd" -m "$MODEL" "$@" --skip-git-repo-check -o "$L/$label.last.txt" "$PROMPT" >"$L/$label.stdout.txt" 2>"$L/$label.stderr.txt"
rc=$?
{
  echo "exit code: $rc"
  echo "--- after: git -C $cwd status --short ---"; git -C "$cwd" status --short
  echo "--- after: git -C $cwd log --oneline -2 ---"; git -C "$cwd" log --oneline -2
} | tee -a "$L/$label.meta.txt"
echo "--- stdout ---"; cat "$L/$label.stdout.txt"
echo "--- stderr ---"; cat "$L/$label.stderr.txt"
