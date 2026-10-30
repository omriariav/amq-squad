#!/bin/bash
# S6 dry run of brief 3-2 (acceptance 3): read-only gh calls only.
# usage: dryrun.sh <grants file> <out prefix>
set -u
W=/Users/omri/code/amq-squad/.claude/worktrees/agent-a83ff47e3e447b571
E=$W/docs/v3-spike/3-2-evidence/s6-dry-run
BIN=/tmp/amq-squad-3b/amq-squad
ROOT=/tmp/amq-squad-3b/root
G=$1
P=$2
for r in amq-noc workspace-cli; do
  start=$(date +%s.%N)
  "$BIN" merge-train --dry-run --root "$ROOT" --grants "$G" --request "$E/request-$r.json" > "$E/$P-$r.txt" 2>&1
  echo "exit $?" >> "$E/$P-$r.txt"
  "$BIN" merge-train --dry-run --root "$ROOT" --grants "$G" --request "$E/request-$r.json" --json > "$E/$P-$r.json" 2>&1
  end=$(date +%s.%N)
  echo "$r: two dry runs (text and json) $(echo "$end - $start" | bc) s" >> "$E/$P-timing.txt"
done
