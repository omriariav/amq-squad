#!/usr/bin/env bash
# gate.sh OUT: the phase 5 re-pass routing gate, full output into OUT
# (written outside the tree, copied in through scripts/redact-evidence).
set -uo pipefail
WT=/Users/omri/code/amq-squad/.claude/worktrees/agent-adfe31c4c91ad349f
OUT="$1"
cd "$WT" || exit 2
pat='seat-exec|scheduler run|pi run|amq coop|amq-remote|amq-squad (lead|team|launch)'
LIVE="$(ps -axo pid,ppid,command | grep -E "$pat" | grep -v grep || true)"
if [ -n "$LIVE" ]; then
  echo "gate refused: a live seat or session is on the host:"
  echo "$LIVE"
  exit 3
fi
{
  echo "# gate run $(date -u +%Y-%m-%dT%H:%M:%SZ) at $(git rev-parse --short HEAD) (+ working tree: the 3-2 re-pass commit, uncommitted)"
  echo "# host check (seat-exec, scheduler run, pi run, amq coop, amq-remote, amq-squad lead/team/launch): none live"
  echo "# go version: $(go version)"
} > "$OUT"
step() {
  echo "## $*" >> "$OUT"
  if ! "$@" >> "$OUT" 2>&1; then
    echo "## FAILED: $*" >> "$OUT"
    echo "gate failed at: $*"
    exit 1
  fi
}
step go build ./...
step go vet ./...
step env GOOS=linux go build ./...
step env GOOS=windows go build ./...
step scripts/manifest-check.sh --final
step go test -count=1 -v ./...
step go test -count=1 -race -timeout 45m -v ./internal/mergetrain/ ./internal/releaseflow/ ./internal/ledger/ ./internal/cli/
echo "## gate OK" >> "$OUT"
echo "gate OK: $OUT ($(grep -c -- '--- PASS' "$OUT") PASS lines, $(grep -c -- '--- FAIL' "$OUT") FAIL lines)"
