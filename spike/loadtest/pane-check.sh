#!/usr/bin/env bash
# pane-check.sh RUN_ID REPO_PATH [REPO_PATH...]
#
# Read-only look at every seat pane (tmux panes whose cwd is inside a repo path): prints the last
# lines of each pane and flags
#   TRUST   a folder-trust prompt ("Do you trust the files in this folder", "trust this folder"...)
#   PROMPT  any other blocking prompt (permission dialog, "Press Enter to continue", "Esc to cancel")
#   LIMIT   a usage / rate limit message
# Saves the captures to OUT/pane-check-<HHMMSS>/ and appends a one-line verdict per pane to
# OUT/pane-check.log. Types NOTHING into any pane. Exit 0 = no TRUST/PROMPT/LIMIT flag,
# 2 = at least one flag (the operator stops and reports; never answers a trust prompt).
# TAIL=25 sets the number of lines shown per pane.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
. "$HERE/lib.sh"
s6_args "$@"
TAIL="${TAIL:-25}"
stamp="$(date +%H%M%S)"
dir="$OUT/pane-check-$stamp"
mkdir -p "$dir"
flags=0; n=0
TRUST_RE='Do you trust|trust the (files|contents)|trust this (folder|directory|project)|Yes, I trust|Quick safety check|Is this a project you (created|trust)'
PROMPT_RE='Do you want to (proceed|allow|make this edit|create)|Press Enter to continue|Esc to cancel|\(y/n\)|Allow once|Always allow'
LIMIT_RE='hit your .{0,40}limit|usage limit|session limit|spend limit|rate.?limit|try again in'
while IFS=$'\t' read -r pid sess path; do
  [ -z "$pid" ] && continue
  rp="$(cd "$path" 2>/dev/null && pwd -P || echo "$path")"
  hit=0
  for r in "${REPOS[@]}"; do case "$rp" in "$r"|"$r"/*) hit=1;; esac; done
  [ "$hit" = 1 ] || continue
  n=$((n+1))
  f="$dir/${pid//%/p}.txt"
  tmux capture-pane -p -J -t "$pid" -S -200 > "$f" 2>/dev/null
  verdict="ok"
  grep -Eiq "$TRUST_RE" "$f" && { verdict="TRUST"; flags=$((flags+1)); }
  [ "$verdict" = ok ] && grep -Eiq "$PROMPT_RE" "$f" && { verdict="PROMPT"; flags=$((flags+1)); }
  [ "$verdict" = ok ] && grep -Eiq "$LIMIT_RE" "$f" && { verdict="LIMIT"; flags=$((flags+1)); }
  echo "=== pane $pid  session $sess  cwd ${rp#"$HOME"/}  verdict: $verdict"
  grep -v '^[[:space:]]*$' "$f" | tail -n "$TAIL" | sed 's/^/    | /'
  echo "$(date +%H:%M:%S) pane=$pid session=$sess cwd=$rp verdict=$verdict" >> "$OUT/pane-check.log"
done < <(tmux list-panes -a -F $'#{pane_id}\t#{session_name}\t#{pane_current_path}' 2>/dev/null)
echo
echo "pane-check: $n seat pane(s), $flags flagged. Captures: $dir"
[ "$n" -eq 0 ] && echo "WARNING: no seat panes found under: ${REPOS[*]}"
[ "$flags" -eq 0 ]
rc=$?
[ "$rc" -eq 0 ] || rc=2
exit "$rc"
