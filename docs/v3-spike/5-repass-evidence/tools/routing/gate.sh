#!/usr/bin/env bash
# gate.sh OUT [RACE_PACKAGES...]: the phase 5 re-pass gate, run from the
# repository this script lives in; the full output goes to OUT (write it
# outside the tree, then copy it in through scripts/redact-evidence).
# Steps: build, vet, the linux and windows builds, manifest-check --final,
# go test -count=1 ./..., then go test -race -timeout 45m on the packages
# given (default: the routing re-pass packages). Refuses (exit 3) when a
# live seat or session process is on the host; quiet_gate.sh waits for one.
set -uo pipefail
WT="$(cd "$(dirname "$0")/../../../../.." && pwd)"
OUT="$1"
shift
RACE=("$@")
if [ ${#RACE[@]} -eq 0 ]; then
  RACE=(./internal/routing/ ./internal/task/ ./internal/grants/ ./internal/scheduler/ ./internal/schedproc/ ./internal/status/ ./internal/seatcommit/ ./internal/releaseflow/ ./internal/ledger/ ./internal/mergetrain/ ./internal/cli/)
fi
cd "$WT" || exit 2
pat='seat-exec|scheduler run|pi run|amq coop|amq-remote|amq-squad (lead|team|launch)'
LIVE="$(ps -axo pid,ppid,command | grep -E "$pat" | grep -v grep || true)"
if [ -n "$LIVE" ]; then
  echo "gate refused: a live seat or session is on the host:"
  echo "$LIVE"
  exit 3
fi
dirty=""
[ -n "$(git status --porcelain)" ] && dirty=" (+ working tree)"
{
  echo "# gate run $(date -u +%Y-%m-%dT%H:%M:%SZ) at $(git rev-parse --short HEAD)$dirty"
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
step go test -count=1 -race -timeout 45m -v "${RACE[@]}"
echo "## gate OK" >> "$OUT"
echo "gate OK: $OUT ($(grep -c -- '--- PASS' "$OUT") PASS lines, $(grep -c -- '--- FAIL' "$OUT") FAIL lines)"
