#!/usr/bin/env bash
# selftest-limits.sh: dry run of scan-limits.sh on a fixture (no seats, no Claude).
# Builds a fake projects dir with usage-limit, plain 429, retry-429 and 5xx records
# inside and outside the window, plus a throwaway tmux pane that prints a limit
# message, then checks the counts.
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export S6_OUT_BASE="${S6_OUT_BASE:-$HOME/.amq-squad/spike/s6}"
RUN=dryrun-limits
rm -rf "${S6_OUT_BASE:?}/$RUN"
T="$(mktemp -d)"; T="$(cd "$T" && pwd -P)"
REPO="$T/fakerepo"; mkdir -p "$REPO/.s6-wt/impl-a" "$T/projects"
ENC="$(printf '%s' "$REPO" | sed 's/[^A-Za-z0-9]/-/g')"
ENCW="$(printf '%s' "$REPO/.s6-wt/impl-a" | sed 's/[^A-Za-z0-9]/-/g')"
mkdir -p "$T/projects/$ENC" "$T/projects/$ENCW" "$T/projects/-unrelated-dir"
NOW="$(date +%s)"
START=$((NOW - 3600))
echo "$START" > /dev/null
iso() { python3 -I -c "import datetime,sys;print(datetime.datetime.fromtimestamp(float(sys.argv[1]),datetime.timezone.utc).strftime('%Y-%m-%dT%H:%M:%S.000Z'))" "$1"; }
IN1="$(iso $((START + 600)))"; IN2="$(iso $((START + 900)))"; IN3="$(iso $((START + 1200)))"; IN4="$(iso $((START + 1500)))"; OLD="$(iso $((START - 7200)))"
cat > "$T/projects/$ENC/aaaa.jsonl" <<JSON
{"type":"assistant","timestamp":"$IN1","isApiErrorMessage":true,"error":"rate_limit","apiErrorStatus":429,"sessionId":"s1","cwd":"$REPO","message":{"content":[{"type":"text","text":"You've hit your session limit · resets 5:00am (Asia/Jerusalem)"}]}}
{"type":"assistant","timestamp":"$IN2","isApiErrorMessage":true,"error":"rate_limit","apiErrorStatus":429,"sessionId":"s1","cwd":"$REPO","message":{"content":[{"type":"text","text":"Request rejected (429) · too many requests"}]}}
{"type":"assistant","timestamp":"$OLD","isApiErrorMessage":true,"error":"rate_limit","apiErrorStatus":429,"sessionId":"s0","cwd":"$REPO","message":{"content":[{"type":"text","text":"You've hit your session limit (outside window)"}]}}
JSON
cat > "$T/projects/$ENCW/bbbb.jsonl" <<JSON
{"type":"system","subtype":"api_error","level":"error","timestamp":"$IN3","sessionId":"s2","cwd":"$REPO/.s6-wt/impl-a","error":{"status":429,"message":"rate_limit_error"},"retryAttempt":1,"maxRetries":10}
{"type":"assistant","timestamp":"$IN4","isApiErrorMessage":true,"error":"server_error","apiErrorStatus":500,"sessionId":"s2","cwd":"$REPO/.s6-wt/impl-a","message":{"content":[{"type":"text","text":"API Error: 500"}]}}
JSON
echo '{"type":"assistant","timestamp":"'"$IN1"'","isApiErrorMessage":true,"error":"rate_limit","apiErrorStatus":429,"message":{"content":[{"type":"text","text":"unrelated project"}]}}' > "$T/projects/-unrelated-dir/cccc.jsonl"

printf '#!/bin/sh\necho "You have hit your usage limit, try again in 3h"\nsleep 60\n' > "$T/pane.sh"; chmod +x "$T/pane.sh"
tmux new-session -d -s s6dry-limits -c "$REPO" "$T/pane.sh"
trap 'tmux kill-session -t s6dry-limits 2>/dev/null || true; rm -rf "$T"' EXIT
sleep 1
ONCE=1 "$HERE/sample-ram.sh" "$RUN" "$REPO" >/dev/null 2>&1   # registers the pane in panes-seen.tsv
echo "$START" > "$S6_OUT_BASE/$RUN/run-start.epoch"
S6_CLAUDE_PROJECTS="$T/projects" "$HERE/scan-limits.sh" "$RUN" "$REPO" | tee "$T/summary.txt"
echo "--- checks"
grep -q 'claude: usage_limit=1 http_429=1 retry_429=1 server_error=1 other_error=0' "$T/summary.txt" && echo "PASS claude counts" || { echo "FAIL claude counts"; exit 1; }
grep -q 'first usage limit .* 10.0 min after run start' "$T/summary.txt" && echo "PASS time to first usage limit" || { echo "FAIL first usage limit time"; exit 1; }
grep -q 'panes captured=1 limit-phrase lines=[1-9]' "$T/summary.txt" && echo "PASS pane fallback" || { echo "FAIL pane fallback"; exit 1; }
echo "selftest-limits OK"
