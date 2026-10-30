#!/usr/bin/env bash
# gate.sh OUT: the standard gate plus the cross builds and -race on internal/routing,
# full output into OUT. Exits non-zero on the first failing step.
set -uo pipefail
WT=/Users/omri/code/amq-squad/.claude/worktrees/agent-a4e402e08c367d4c2
OUT="$1"
cd "$WT" || exit 2
mkdir -p "$(dirname "$OUT")"
LIVE="$(ps -axo pid,command | grep -E 'seat-exec|scheduler run|pi run|amq coop|amq-remote|amq-squad (lead|team|launch)' | grep -v grep || true)"
if [ -n "$LIVE" ]; then
  echo "gate refused: a live seat or session is on the host:"
  echo "$LIVE"
  exit 3
fi
{
  echo "# gate run $(date -u +%Y-%m-%dT%H:%M:%SZ) at $(git rev-parse --short HEAD) (+ working tree)"
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
step go test -count=1 -v ./...
step scripts/manifest-check.sh --final
step env GOOS=linux go build ./...
step env GOOS=windows go build ./...
step go test -count=1 -race -v ./internal/routing/ ./internal/task/ ./internal/scheduler/ ./internal/status/
echo "## gate OK" >> "$OUT"
echo "gate OK: $OUT ($(grep -c '^--- PASS' "$OUT") PASS lines, $(grep -c '^--- FAIL' "$OUT") FAIL lines)"
