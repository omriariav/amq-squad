#!/usr/bin/env bash
# manifest-check.sh: validate docs/v3-manifest.yaml against the tree under internal/.
# Checks:
#   1. every non-test Go file under internal/ appears exactly once in the manifest
#   2. no manifest path with status "deleted" still exists
#   3. v2_lines matches wc -l within 2 percent for files still present, unless status is "trimmed":
#      a surviving file a 2a-1 cut edited (dead callers removed). Its entry keeps v2_lines as the v3
#      baseline and carries current_lines (wc -l after the cut), which is checked instead.
#   4. sum of target_lines is within 10 percent of 33,100
# Also validates fate and status values. Prints a summary by fate. Exits non-zero on any violation.
#
# Manifest format (strict, one key per line, documented in the manifest header):
#   - path: <file>
#     v2_lines: N / [current_lines: N] / fate: F / target_lines: N / cluster: C / extract: "..." / reason: "..." / status: S
# Usage: scripts/manifest-check.sh [manifest-path]   (run from anywhere; operates on the repo root)
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MANIFEST="${1:-$ROOT/docs/v3-manifest.yaml}"
EXPECTED_TOTAL=33100
cd "$ROOT"

[ -f "$MANIFEST" ] || { echo "FAIL: manifest not found: $MANIFEST" >&2; exit 2; }

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# Flatten manifest to TSV: path, v2_lines, fate, target_lines, cluster, status, current_lines
awk '
  function flush() {
    if (path != "") print path "\t" v2 "\t" fate "\t" tgt "\t" cluster "\t" status "\t" cur
  }
  /^- path: /      { flush(); path=$3; v2=""; fate=""; tgt=""; cluster=""; status=""; cur=""; next }
  /^  v2_lines: /  { v2=$2; next }
  /^  current_lines: / { cur=$2; next }
  /^  fate: /      { fate=$2; next }
  /^  target_lines: / { tgt=$2; next }
  /^  cluster: /   { cluster=$2; next }
  /^  status: /    { status=$2; next }
  END { flush() }
' "$MANIFEST" > "$TMP/entries.tsv"

find internal -name '*.go' ! -name '*_test.go' | LC_ALL=C sort > "$TMP/tree.txt"

errors=0
fail() { echo "FAIL: $*" >&2; errors=$((errors + 1)); }

# Field completeness and value domains
while IFS=$'\t' read -r path v2 fate tgt cluster status cur; do
  [[ "$v2" =~ ^[0-9]+$ ]]  || fail "$path: bad v2_lines '$v2'"
  [[ "$tgt" =~ ^[0-9]+$ ]] || fail "$path: bad target_lines '$tgt'"
  [[ "$fate" =~ ^(keep|shrink|delete|replace|review)$ ]] || fail "$path: bad fate '$fate'"
  [[ "$status" =~ ^(pending|deleted|trimmed)$ ]] || fail "$path: bad status '$status'"
  if [ "$status" = "trimmed" ]; then
    [[ "$cur" =~ ^[0-9]+$ ]] || fail "$path: status trimmed needs numeric current_lines (got '$cur')"
  fi
  [ -n "$cluster" ] || fail "$path: missing cluster"
done < "$TMP/entries.tsv"

# 1. exactly once
cut -f1 "$TMP/entries.tsv" | LC_ALL=C sort > "$TMP/man_paths.txt"
dups="$(uniq -d < "$TMP/man_paths.txt")"
[ -z "$dups" ] || while read -r p; do fail "duplicate manifest entry: $p"; done <<< "$dups"
uniq "$TMP/man_paths.txt" > "$TMP/man_uniq.txt"
while read -r p; do fail "file not in manifest: $p"; done < <(comm -23 "$TMP/tree.txt" "$TMP/man_uniq.txt")

# 2 and 3, plus entries pointing at missing files that are not marked deleted
while IFS=$'\t' read -r path v2 fate tgt cluster status cur; do
  if [ -f "$path" ]; then
    if [ "$status" = "deleted" ]; then
      fail "$path: status deleted but file still exists"
      continue
    fi
    actual="$(wc -l < "$path" | tr -d ' ')"
    # trimmed: a 2a-1 cut removed dead callers; v2_lines stays the baseline, current_lines is checked
    want="$v2"; label="v2_lines"
    if [ "$status" = "trimmed" ] && [[ "$cur" =~ ^[0-9]+$ ]]; then want="$cur"; label="current_lines"; fi
    # within 2 percent: |actual - want| * 100 <= 2 * want
    diff=$((actual > want ? actual - want : want - actual))
    if [ $((diff * 100)) -gt $((2 * want)) ]; then
      fail "$path: $label $want vs wc -l $actual (outside 2 percent)"
    fi
  elif [ "$status" != "deleted" ]; then
    fail "$path: missing from tree but status is $status"
  fi
done < "$TMP/entries.tsv"

# 4. target sum within 10 percent of 33,100
total="$(awk -F'\t' '{s += $4} END {print s + 0}' "$TMP/entries.tsv")"
lo=$((EXPECTED_TOTAL * 90 / 100)); hi=$((EXPECTED_TOTAL * 110 / 100))
if [ "$total" -lt "$lo" ] || [ "$total" -gt "$hi" ]; then
  fail "sum of target_lines $total outside [$lo, $hi] (10 percent of $EXPECTED_TOTAL)"
fi

# Summary by fate
echo "Manifest: $MANIFEST"
printf '%-9s %6s %10s %12s\n' fate count v2_lines target_lines
awk -F'\t' '
  { c[$3]++; v[$3] += $2; t[$3] += $4; C += 1; V += $2; T += $4 }
  END {
    n = split("keep shrink replace review delete", order, " ")
    for (i = 1; i <= n; i++) { f = order[i]; if (f in c) printf "%-9s %6d %10d %12d\n", f, c[f], v[f], t[f] }
    printf "%-9s %6d %10d %12d\n", "TOTAL", C, V, T
  }' "$TMP/entries.tsv"
echo "target sum $total vs expected $EXPECTED_TOTAL (allowed $lo..$hi)"

if [ "$errors" -gt 0 ]; then
  echo "manifest-check: $errors violation(s)" >&2
  exit 1
fi
echo "manifest-check: OK"
