#!/usr/bin/env bash
# L2 smoke test — end-to-end verification of amq-squad v1.5.0 tmux runtime
# control (issues #61/#62). Runs the 10-step walkthrough automatically.
#
# By default it uses a throwaway `bash` "agent" so the run is deterministic and
# needs no real codex/claude. Set AGENT_BIN=codex (or claude) to drive real
# agents instead (then the verbatim/multiline delivery is a manual eyeball).
#
#   ./l2-smoke.sh            # dummy bash agent (deterministic)
#   AGENT_BIN=codex ./l2-smoke.sh
set -uo pipefail

REPO="${REPO:-/Users/omri.a/Code/amq-squad}"
AGENT_BIN="${AGENT_BIN:-bash}"
SESSION="${SESSION:-issue-96}"
PASS=0; FAIL=0
note(){ printf '\n\033[1m=== %s ===\033[0m\n' "$*"; }
ok(){   printf '  \033[32mPASS\033[0m %s\n' "$*"; PASS=$((PASS+1)); }
bad(){  printf '  \033[31mFAIL\033[0m %s\n' "$*"; FAIL=$((FAIL+1)); }

note "Step 0 — build amq-squad from $REPO"
BIN="$(mktemp -d)/amq-squad"
( cd "$REPO" && go build -ldflags "-X main.version=v1.5.0-rc" -o "$BIN" ./cmd/amq-squad ) \
  || { echo "build failed"; exit 1; }
"$BIN" version

PROJ="$(mktemp -d)"            # under /var/folders -> symlinked: exercises the cwd fix
cd "$PROJ"; git init -q
TMUX_SESSION=""
cleanup(){
  "$BIN" stop --all --project "$PROJ" >/dev/null 2>&1 || true
  [ -n "$TMUX_SESSION" ] && tmux kill-session -t "$TMUX_SESSION" 2>/dev/null || true
  rm -rf "$PROJ"
}
trap cleanup EXIT

note "Step 1 — create a 2-agent team (binary=$AGENT_BIN)"
"$BIN" new team --roles cto,qa --binary "cto=$AGENT_BIN,qa=$AGENT_BIN" --session "$SESSION" >/dev/null \
  && ok "team created" || bad "new team failed"

note "Step 2 — launch agents into a new tmux session"
UP_OUT="$("$BIN" up "$SESSION" --target new-session --no-bootstrap 2>&1)"
if grep -q "Created tmux session" <<<"$UP_OUT"; then ok "agents launched"; else bad "up did not launch"; echo "$UP_OUT"; fi
TMUX_SESSION="$(sed -n 's/.*Created tmux session \([A-Za-z0-9_-]*\).*/\1/p' <<<"$UP_OUT" | head -1)"
echo "  tmux session: $TMUX_SESSION"
sleep 2

js(){ "$BIN" status --session "$SESSION" --json 2>/dev/null; }

note "Step 3 — status --json carries tmux block + pane_alive + available actions"
if js | python3 -c '
import sys,json
recs=json.load(sys.stdin)["data"]["records"]
def good(r):
    t=r.get("tmux") or {}
    a={x["kind"]:x["available"] for x in r.get("actions",[])}
    return bool(t.get("pane_id")) and t.get("pane_alive") and a.get("focus") and a.get("send")
sys.exit(0 if recs and all(good(r) for r in recs) else 1)'; then
  ok "both members: real pane_id, pane_alive=true, focus/send available"
else
  bad "tmux/pane_alive/actions missing or unavailable"; js | python3 -m json.tool
fi

CTO_PANE="$(js | python3 -c 'import sys,json;d=json.load(sys.stdin);print(next(r["tmux"]["pane_id"] for r in d["data"]["records"] if r["role"]=="cto"))' 2>/dev/null)"
echo "  cto pane: $CTO_PANE"

note "Step 4/5 — send delivers a prompt and submits Enter (in a symlinked cwd)"
MARK="SMOKE${RANDOM}"
# Single-quote the arithmetic so the AGENT's shell evaluates it, not this script.
if "$BIN" send --session "$SESSION" --role cto --body 'echo '"$MARK"'_$((6*7))' >/dev/null 2>&1; then
  ok "send returned success"
else
  bad "send failed (the bug we fixed: cwd guard rejecting a symlinked pane)"
fi
sleep 1
if [ "$AGENT_BIN" = "bash" ] || [ "$AGENT_BIN" = "sh" ]; then
  if tmux capture-pane -p -t "$CTO_PANE" 2>/dev/null | grep -q "${MARK}_42"; then
    ok "prompt landed AND submitted (agent evaluated it -> ${MARK}_42)"
  else
    bad "prompt did not land/submit in the target pane"; tmux capture-pane -p -t "$CTO_PANE" 2>/dev/null | tail -5
  fi
fi

note "Step 6 — quotes / shell metacharacters survive delivery verbatim"
Q='q:a b|c;d&e'
"$BIN" send --session "$SESSION" --role cto --body 'echo "'"$Q"'"' >/dev/null 2>&1 || true
sleep 1
if [ "$AGENT_BIN" = "bash" ] || [ "$AGENT_BIN" = "sh" ]; then
  if tmux capture-pane -p -t "$CTO_PANE" 2>/dev/null | grep -qF "$Q"; then
    ok "quoted text with | ; & delivered verbatim"
  else
    bad "special characters were mangled in delivery"
  fi
fi

note "Step 7 — focus (best-effort; visible only when the session is attached)"
if "$BIN" focus --session "$SESSION" --role qa >/dev/null 2>&1; then
  ok "focus resolved the qa pane"
else
  echo "  (focus needs an attached client to switch view; resolution still ran)"
fi

note "Step 8 — dead pane errors clearly and pane_alive flips false"
tmux kill-pane -t "$CTO_PANE" 2>/dev/null || true
sleep 1
DEAD_OUT="$("$BIN" send --session "$SESSION" --role cto --body "should fail" 2>&1)"
echo "  send-after-kill output: [$DEAD_OUT]"
if grep -qiE "not available|no live tmux pane" <<<"$DEAD_OUT"; then
  ok "send to a dead pane errors clearly"
else
  bad "dead pane did not produce a clear error"
fi
if js | python3 -c 'import sys,json;d=json.load(sys.stdin);r=next(x for x in d["data"]["records"] if x["role"]=="cto");sys.exit(0 if not (r.get("tmux") or {}).get("pane_alive") else 1)'; then
  ok "cto pane_alive flipped to false"
else
  bad "pane_alive did not flip after the pane died"
fi

note "Step 9 — resume --json emits a resume_plan envelope"
if "$BIN" resume --session "$SESSION" --json 2>/dev/null | python3 -c 'import sys,json;sys.exit(0 if json.load(sys.stdin)["kind"]=="resume_plan" else 1)'; then
  ok "resume --json -> kind=resume_plan"
else
  bad "resume --json shape wrong"
fi

note "Result"
printf '  %d passed, %d failed\n' "$PASS" "$FAIL"
[ "$FAIL" -eq 0 ] && echo "  ALL GREEN" || echo "  SOME CHECKS FAILED"
exit "$FAIL"
