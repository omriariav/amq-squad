#!/usr/bin/env bash
# preflight.sh pre|post RUN_ID REPO_PATH [REPO_PATH...]
#
#   pre   read-only checks before the run; records baseline hashes of the owner config files.
#         Exit 0 only if every check passes.
#   post  after the run: re-hashes the owner config files against the baseline, lists seats and
#         tmux sessions still alive, checks the repos have no tracked modifications.
# Starts nothing, writes only under ~/.amq-squad/spike/s6/RUN_ID/.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
. "$HERE/lib.sh"
MODE="${1:-}"; [ "$#" -gt 0 ] && shift
case "$MODE" in pre|post) ;; *) echo "usage: preflight.sh pre|post RUN_ID REPO_PATH..." >&2; exit 64;; esac
s6_args "$@"
BIN="${AMQ_SQUAD_BIN:-$HOME/go/bin/amq-squad}"
FAILS=0
ok()   { echo "PASS  $*"; }
bad()  { echo "FAIL  $*"; FAILS=$((FAILS+1)); }
info() { echo "INFO  $*"; }

hash_of() { [ -e "$1" ] && shasum -a 256 "$1" | cut -d' ' -f1 || echo "absent"; }
CFG_FILES=("$HOME/.claude/settings.json" "$HOME/.codex/config.toml")

if [ "$MODE" = pre ]; then
  # 1. versions
  v="$("$BIN" version 2>&1 | head -1)"; case "$v" in *2.31.0*) ok "amq-squad $v";; *) bad "amq-squad version is '$v', expected 2.31.0";; esac
  v="$(amq --version 2>&1 | head -1)"; case "$v" in *0.102.0*) ok "amq $v";; *) bad "amq version is '$v', expected 0.102.0";; esac
  [ -d "$HOME/.claude/plugins/cache/amq-squad/amq-squad/2.31.0" ] && ok "plugin 2.31.0 present" || bad "plugin 2.31.0 missing"

  # 2. no other agents
  # ancestors of this script (the operator's own session) are not "other agents"
  anc=" "; pp=$$
  while [ -n "$pp" ] && [ "$pp" -gt 1 ]; do anc="$anc$pp "; pp="$(ps -o ppid= -p "$pp" 2>/dev/null | tr -d ' ')"; done
  cand="$(ps -axo pid=,command= | grep -E '/claude/versions/|/\.local/bin/claude|(^|[ /])claude( |$)|(^|[ /])codex( |$)|codex app-server|amq-squad (up|start|resume|status)|amq (launch|wake|monitor)' | grep -v -E 'grep|preflight.sh|ps -axo' || true)"
  others=""; helpers=""
  while IFS= read -r line; do
    [ -z "$line" ] && continue
    pid="$(echo "$line" | awk '{print $1}')"
    case "$anc" in *" $pid "*) continue;; esac
    case "$line" in
      *stream-json*claude-haiku*|*"codex app-server"*|*app-server-broker*) helpers="$helpers$line"$'\n';;
      *) others="$others$line"$'\n';;
    esac
  done <<< "$cand"
  [ -n "$helpers" ] && { info "background helpers (claude-mem observer, codex app-server) are not seats; listed for the record:"; printf '%s' "$helpers" | cut -c1-140 | sed 's/^/        /'; }
  if [ -z "$others" ]; then ok "no other claude / codex / amq-squad / amq wake processes running"; else
    bad "other agent-like processes are running (stop them, or have the owner confirm, before starting):"; printf '%s' "$others" | cut -c1-170 | sed 's/^/        /'; fi

  # 3. tmux
  if tmux ls >/dev/null 2>&1; then
    sess="$(tmux ls -F '#{session_name}')"
    spike="$(echo "$sess" | grep -E '^(amq-|s6|s4|s3|s5|squad|.*-s[0-9]-)' || true)"
    info "tmux sessions: $(echo "$sess" | tr '\n' ' ')"
    [ -z "$spike" ] && ok "no spike tmux sessions" || bad "spike-looking tmux sessions exist: $(echo "$spike" | tr '\n' ' ')"
  else ok "no tmux server running"; fi

  # 4. caffeinate
  assertions="$(pmset -g assertions 2>/dev/null || true)"
  cafs=""
  for cp in $(pgrep -x caffeinate || true); do
    a="$(ps -o args= -p "$cp" 2>/dev/null || true)"
    case "$a" in *" -t "*) ;; "caffeinate -"*d*) cafs="$cp $a";; esac
  done
  if [[ "$assertions" == *"asserting forever"* && -n "$cafs" ]]; then
    ok "caffeinate running without a time limit: $cafs"
  else bad "no unlimited caffeinate (start: caffeinate -dims &)"; fi

  # 5. window
  "$HERE/window-note.sh" show "$RUN_ID" "${REPOS[@]}" | sed 's/^/INFO  /'

  # 6. repos
  for r in "${REPOS[@]}"; do
    n="$(basename "$r")"
    [ -z "$(git -C "$r" status --porcelain --untracked-files=no)" ] && ok "$n: no tracked modifications" || bad "$n: tracked files modified"
    [ "$(git -C "$r" rev-parse main)" = "$(git -C "$r" rev-parse origin/main)" ] && ok "$n: main == origin/main ($(git -C "$r" rev-parse --short main))" || bad "$n: local main differs from origin/main"
    for f in .amq-squad .agent-mail .amqrc .amq .s6-wt; do
      git -C "$r" check-ignore -q "$f" && ok "$n: $f ignored" || bad "$n: $f is NOT ignored (would show as untracked)"
    done
    s="$(s6_session_of "$r")"; [ -n "$s" ] && ok "$n: session $s" || bad "$n: no session in team.json"
    cnt="$(ls "$r/.amq-squad/tasks/$s" 2>/dev/null | grep -c '^t.*\.json$' || true)"
    pend="$(grep -l '"status": "pending"' "$r"/.amq-squad/tasks/"$s"/t*.json 2>/dev/null | wc -l | tr -d ' ')"
    [ "$cnt" -gt 0 ] && [ "$cnt" = "$pend" ] && ok "$n: $cnt tasks, all pending" || bad "$n: tasks=$cnt pending=$pend (expected all pending; do not reuse a dirty store)"
    [ -s "$r/.amq-squad/briefs/$s.md" ] && ok "$n: brief present" || bad "$n: brief missing"
    for w in impl-a impl-b; do
      [ -z "$(git -C "$r/.s6-wt/$w" status --porcelain)" ] && ok "$n: $w clean" || bad "$n: $w not clean"
      git -C "$r/.s6-wt/$w" symbolic-ref -q HEAD >/dev/null && bad "$n: $w is on a branch" || ok "$n: $w detached"
    done
    "$BIN" doctor --project "$r" >"$OUT/preflight-doctor-$n.txt" 2>&1 && ok "$n: doctor exit 0 (see preflight-doctor-$n.txt)" || bad "$n: doctor failed (see $OUT/preflight-doctor-$n.txt)"
  done

  # 7. gh
  gh auth status >/dev/null 2>&1 && ok "gh authenticated" || bad "gh not authenticated"

  # 8. baseline hashes
  : > "$OUT/config-baseline.txt"
  for f in "${CFG_FILES[@]}"; do echo "$(hash_of "$f")  $f" >> "$OUT/config-baseline.txt"; done
  info "config baseline recorded in $OUT/config-baseline.txt"
  sed 's/^/INFO  /' "$OUT/config-baseline.txt"
else
  [ -s "$OUT/config-baseline.txt" ] || { echo "no baseline: run 'preflight.sh pre' first" >&2; exit 4; }
  while read -r h f; do
    now="$(hash_of "$f")"; [ "$now" = "$h" ] && ok "unchanged: $f" || bad "CHANGED: $f (baseline $h, now $now)"
  done < "$OUT/config-baseline.txt"
  for r in "${REPOS[@]}"; do
    n="$(basename "$r")"
    [ -z "$(git -C "$r" status --porcelain --untracked-files=no)" ] && ok "$n: no tracked modifications in the root checkout" || bad "$n: tracked files modified in the root checkout"
    [ "$(git -C "$r" rev-parse main)" = "$(git -C "$r" rev-parse origin/main)" ] && ok "$n: local main untouched" || info "$n: local main differs from origin/main"
    info "$n branches: $(git -C "$r" branch --list 'loadtest/*' | tr -d ' *' | tr '\n' ' ')"
  done
  info "tmux: $(tmux ls 2>&1 | tr '\n' ';')"
  info "seat processes: $(ps -axo pid=,command= | grep -E '/claude/versions/|(^| )claude( |$)' | grep -v grep | wc -l | tr -d ' ') claude-like"
fi
echo
[ "$FAILS" -eq 0 ] && echo "preflight $MODE: ALL PASS" || echo "preflight $MODE: $FAILS FAIL"
[ "$FAILS" -eq 0 ]
