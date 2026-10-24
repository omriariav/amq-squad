#!/usr/bin/env bash
# gate.sh OUT: the standard gate plus the cross builds and -race on internal/routing,
# full output into OUT. Exits non-zero on the first failing step.
set -uo pipefail
WT=/Users/omri/code/amq-squad/.claude/worktrees/agent-a4e402e08c367d4c2
OUT="$1"
cd "$WT" || exit 2
mkdir -p "$(dirname "$OUT")"
{
  echo "# gate run $(date -u +%Y-%m-%dT%H:%M:%SZ) at $(git rev-parse --short HEAD) (+ working tree)"
  echo "# host check: amq-squad seat or lead processes (seat-exec, amq-squad lead/team, scheduler)"
  ps -axo pid,command | grep -E 'seat-exec|amq-squad (lead|team|scheduler|launch)' | grep -v grep || echo "none"
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
