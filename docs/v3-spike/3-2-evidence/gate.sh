#!/bin/bash
# The standard gate of brief 3-2, full output kept as evidence.
# usage: gate.sh <label> [race packages...]
set -u
W=/Users/omri/code/amq-squad/.claude/worktrees/agent-a83ff47e3e447b571
L=$1; shift
OUT=$W/docs/v3-spike/3-2-evidence/gate-$L.txt
cd "$W" || exit 2
{
  echo "# gate $L at $(date -u +%FT%TZ), HEAD $(git -C "$W" rev-parse --short HEAD) plus the working tree"
  echo "# host check: seat-exec or lead processes (none expected)"
  pgrep -fl 'amq-squad seat-exec|amq-squad lead' || echo "none"
  echo "## gofmt -l internal cmd (empty expected)"; gofmt -l internal cmd; echo "exit 0"
  echo "## go build ./..."; go build ./... 2>&1; echo "exit $?"
  echo "## go vet ./..."; go vet ./... 2>&1; echo "exit $?"
  echo "## GOOS=linux go build ./..."; GOOS=linux go build ./... 2>&1; echo "exit $?"
  echo "## GOOS=windows go build ./..."; GOOS=windows go build ./... 2>&1; echo "exit $?"
  echo "## go test ./..."; go test -count=1 ./... 2>&1; echo "exit $?"
  if [ $# -gt 0 ]; then echo "## go test -race $*"; go test -count=1 -timeout 45m -race "$@" 2>&1; echo "exit $?"; fi
  echo "## scripts/manifest-check.sh --final"; scripts/manifest-check.sh --final 2>&1; echo "exit $?"
} > "$OUT" 2>&1
grep -n '^exit\|^## \|^FAIL\|^--- FAIL' "$OUT"
