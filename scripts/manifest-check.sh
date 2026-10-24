#!/usr/bin/env bash
# manifest-check.sh: validate docs/v3-manifest.yaml against the tree under internal/.
# Checks:
#   1. every non-test Go file under internal/ appears exactly once in the manifest
#   2. no manifest path with status "deleted" still exists
#   3. v2_lines matches wc -l within 2 percent for files still present, unless status is "trimmed":
#      a surviving file a 2a-1 cut edited (dead callers removed). Its entry keeps v2_lines as the v3
#      baseline and carries current_lines (wc -l after the cut), which is checked instead.
#   4. sum of target_lines is within 10 percent of 33,100
#   5. (2a-2; floor retired at the 2b-5 merge) the ceiling: the sum of wc -l over present non-new files ("v2 rows
#      kept"), excluding deferred-replace rows, must stay at or under 33,100 plus 10 percent. With --final an
#      overrun is a violation unless the manifest carries the decision marker `decisions: [end-of-2a-2]`. There
#      is no floor. The report prints the v2 rows kept against the ceiling, the 3.0 additions (rule 7) beside
#      it, and their sum as the size of 3.0, plus the like-for-like line and the class totals.
#   6. (--final only) no row is left with status pending or trimmed.
#   7. (2a-3) fate "new" rows are 3.0 additions (code written for 3.0, v2_lines 0). The target-sum rule (4)
#      and the band gate (5) were written for the shrink of v2 code, so both exclude new rows; their
#      target_lines is informational (equal to the file size). They are printed as their own line,
#      "3.0 additions: N files, M lines". A new row carries status new and current_lines, checked like keep.
#   8. (--final only; 2b-5 merge ruling) a row retired from 2b on (status deleted, or status shrunk with
#      current_lines below half its target_lines) names what took its place in replaced_by: the path of the
#      fate-new row that replaces it (a row of this manifest, present in the tree) or the brief acceptance
#      item that covers its function ("<brief> acceptance <n>", for example "2b-5 acceptance 4"). A row is
#      from 2b on when it carries retired_in (a brief id such as 2b-5) or when its note names a 2b, 3 or 4
#      brief (2b-N, 3-N, 4-N); rows retired in 2a-1 and 2a-2 name no such brief and are exempt.
# Statuses (2a-2): pending | deleted | trimmed | shrunk | keep | deferred-replace | deferred-2b | deferred-3 | accepted-3.
#   trimmed: an edited surviving file not (yet) at its target; current_lines is checked instead of v2_lines.
#   shrunk: a shrink or review row at or under target_lines; carries current_lines, which must be <= target_lines.
#   keep: a fate keep row at close; carries current_lines (targets unchanged).
#   new: a fate new row (a 3.0 addition, 2a-3); carries current_lines.
#   deferred-replace: a fate replace row kept until its 2b replacement exists; carries current_lines.
#   deferred-2b: class 1, a row over target whose manifest reason names its 2b rewrite (squad.yaml, seat-exec,
#     the probe); carries current_lines and a note quoting the reason.
#   deferred-3: class 2, a row over target whose rewrite belongs to phase 3 (the release flow); same fields.
#   accepted-3: class 3, plain overage accepted by the end-of-2a-2 decision; same fields.
# Also validates fate and status values. Prints a summary by fate. Exits non-zero on any violation.
#
# Manifest format (strict, one key per line, documented in the manifest header):
#   - path: <file>
#     v2_lines: N / [current_lines: N] / fate: F / target_lines: N / cluster: C / extract: "..." / reason: "..." / status: S / [note: "..."]
#   replaced_by, retired_in (optional, after status, before note): see check 8.
#   note (optional, last key): free text; an entry whose note starts with "2a-2 debit" is listed in the summary
#   (the 2a-1 holding files), so the debit stays visible until 2a-2 pays it.
# Usage: scripts/manifest-check.sh [--final] [manifest-path]   (run from anywhere; operates on the repo root)
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
FINAL=0
if [ "${1:-}" = "--final" ]; then FINAL=1; shift; fi
MANIFEST="${1:-$ROOT/docs/v3-manifest.yaml}"
EXPECTED_TOTAL=33100
cd "$ROOT"

[ -f "$MANIFEST" ] || { echo "FAIL: manifest not found: $MANIFEST" >&2; exit 2; }

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# Flatten manifest to TSV: path, v2_lines, fate, target_lines, cluster, status, current_lines, note, replaced_by, retired_in
# (empty current_lines and note print as "-": tab is IFS whitespace, so read would collapse empty fields)
awk '
  function flush() {
    if (path != "") print path "\t" v2 "\t" fate "\t" tgt "\t" cluster "\t" status "\t" (cur == "" ? "-" : cur) "\t" (note == "" ? "-" : note) "\t" (rb == "" ? "-" : rb) "\t" (ri == "" ? "-" : ri)
  }
  /^- path: /      { flush(); path=$3; v2=""; fate=""; tgt=""; cluster=""; status=""; cur=""; note=""; rb=""; ri=""; next }
  /^  replaced_by: / { rb=substr($0, 16); gsub(/^"|"$/, "", rb); next }
  /^  retired_in: /  { ri=substr($0, 15); gsub(/^"|"$/, "", ri); next }
  /^  v2_lines: /  { v2=$2; next }
  /^  current_lines: / { cur=$2; next }
  /^  fate: /      { fate=$2; next }
  /^  target_lines: / { tgt=$2; next }
  /^  cluster: /   { cluster=$2; next }
  /^  status: /    { status=$2; next }
  /^  note: /      { note=substr($0, 9); gsub(/^"|"$/, "", note); next }
  END { flush() }
' "$MANIFEST" > "$TMP/entries.tsv"

find internal -name '*.go' ! -name '*_test.go' | LC_ALL=C sort > "$TMP/tree.txt"

fmtn() { printf "%'d" "$1" 2>/dev/null || echo "$1"; }
errors=0
fail() { echo "FAIL: $*" >&2; errors=$((errors + 1)); }

# Field completeness and value domains
while IFS=$'\t' read -r path v2 fate tgt cluster status cur note rb ri; do
  [[ "$v2" =~ ^[0-9]+$ ]]  || fail "$path: bad v2_lines '$v2'"
  [[ "$tgt" =~ ^[0-9]+$ ]] || fail "$path: bad target_lines '$tgt'"
  [[ "$fate" =~ ^(keep|shrink|delete|replace|review|new)$ ]] || fail "$path: bad fate '$fate'"
  [[ "$status" =~ ^(pending|deleted|trimmed|shrunk|keep|deferred-replace|deferred-2b|deferred-3|accepted-3|new)$ ]] || fail "$path: bad status '$status'"
  if [[ "$status" =~ ^(trimmed|shrunk|keep|deferred-replace|deferred-2b|deferred-3|accepted-3|new)$ ]]; then
    [[ "$cur" =~ ^[0-9]+$ ]] || fail "$path: status $status needs numeric current_lines (got '$cur')"
  fi
  if [ "$status" = "shrunk" ] && [[ "$cur" =~ ^[0-9]+$ ]] && [[ "$tgt" =~ ^[0-9]+$ ]] && [ "$cur" -gt "$tgt" ]; then
    fail "$path: status shrunk but current_lines $cur > target_lines $tgt"
  fi
  if [ "$status" = "keep" ] && [ "$fate" != "keep" ]; then
    fail "$path: status keep needs fate keep (got '$fate')"
  fi
  if [ "$FINAL" = "1" ] && [[ "$status" =~ ^(pending|trimmed)$ ]]; then
    fail "$path: status $status left at close (--final)"
  fi
  if { [ "$status" = "new" ] && [ "$fate" != "new" ]; } || { [ "$fate" = "new" ] && [ "$status" != "new" ]; }; then
    fail "$path: status new and fate new go together (fate '$fate', status '$status')"
  fi
  if [ "$status" = "deferred-replace" ] && [ "$fate" != "replace" ]; then
    fail "$path: status deferred-replace needs fate replace (got '$fate')"
  fi
  [ -n "$cluster" ] || fail "$path: missing cluster"
done < "$TMP/entries.tsv"

# 1. exactly once
cut -f1 "$TMP/entries.tsv" | LC_ALL=C sort > "$TMP/man_paths.txt"
dups="$(uniq -d < "$TMP/man_paths.txt")"
[ -z "$dups" ] || while read -r p; do fail "duplicate manifest entry: $p"; done <<< "$dups"
uniq "$TMP/man_paths.txt" > "$TMP/man_uniq.txt"
while read -r p; do fail "file not in manifest: $p"; done < <(LC_ALL=C comm -23 "$TMP/tree.txt" "$TMP/man_uniq.txt")

# 2 and 3, plus entries pointing at missing files that are not marked deleted
while IFS=$'\t' read -r path v2 fate tgt cluster status cur note rb ri; do
  if [ -f "$path" ]; then
    if [ "$status" = "deleted" ]; then
      fail "$path: status deleted but file still exists"
      continue
    fi
    actual="$(wc -l < "$path" | tr -d ' ')"
    # trimmed: a 2a-1 cut removed dead callers; v2_lines stays the baseline, current_lines is checked
    want="$v2"; label="v2_lines"
    if [[ "$status" =~ ^(trimmed|shrunk|keep|deferred-replace|deferred-2b|deferred-3|accepted-3|new)$ ]] && [[ "$cur" =~ ^[0-9]+$ ]]; then want="$cur"; label="current_lines"; fi
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
total="$(awk -F'\t' '$3 != "new" {s += $4} END {print s + 0}' "$TMP/entries.tsv")"
lo=$((EXPECTED_TOTAL * 90 / 100)); hi=$((EXPECTED_TOTAL * 110 / 100))
if [ "$total" -lt "$lo" ] || [ "$total" -gt "$hi" ]; then
  fail "sum of target_lines $total outside [$lo, $hi] (10 percent of $EXPECTED_TOTAL)"
fi

# 5. the gate "total within band" (deferred-replace rows excluded, nothing else dropped), the like-for-like
#    remainder without class 1 and 2 rows and their targets, and the class totals
cur_total=0; new_files=0; new_lines=0
c1_lines=0; c1_over=0; c1_tgt=0; c2_lines=0; c2_over=0; c2_tgt=0; c3_over=0
while IFS=$'\t' read -r path v2 fate tgt cluster status cur note rb ri; do
  [ -f "$path" ] || continue
  [ "$status" = "deferred-replace" ] && continue
  n=$(wc -l < "$path" | tr -d ' ')
  if [ "$fate" = "new" ]; then new_files=$((new_files + 1)); new_lines=$((new_lines + n)); continue; fi
  cur_total=$((cur_total + n))
  over=$((n > tgt ? n - tgt : 0))
  case "$status" in
    deferred-2b) c1_lines=$((c1_lines + n)); c1_over=$((c1_over + over)); c1_tgt=$((c1_tgt + tgt)) ;;
    deferred-3)  c2_lines=$((c2_lines + n)); c2_over=$((c2_over + over)); c2_tgt=$((c2_tgt + tgt)) ;;
    *) [ "$fate" != "keep" ] && c3_over=$((c3_over + over)) ;;
  esac
done < "$TMP/entries.tsv"
deferred_total="$(awk -F'\t' '$6 == "deferred-replace" {s += $7} END {print s + 0}' "$TMP/entries.tsv")"
decision=0
grep -Eq '^decisions:.*\bend-of-2a-2\b' "$MANIFEST" && decision=1
over_ceiling=0
if [ "$cur_total" -gt "$hi" ]; then
  over_ceiling=1
  if [ "$FINAL" = "1" ] && [ "$decision" = "0" ]; then
    fail "v2 rows kept: $cur_total over the ceiling $hi and no decision marker"
  fi
fi

# 8. (--final) every row retired from 2b on names what replaced it
if [ "$FINAL" = "1" ]; then
  while IFS=$'\t' read -r path v2 fate tgt cluster status cur note rb ri; do
    retired=0
    [ "$status" = "deleted" ] && retired=1
    if [ "$status" = "shrunk" ] && [[ "$cur" =~ ^[0-9]+$ ]] && [ $((cur * 2)) -lt "$tgt" ]; then retired=1; fi
    [ "$retired" = "1" ] || continue
    later=0
    [ "$ri" != "-" ] && later=1
    [[ "$note" =~ (^|[^0-9A-Za-z.])(2b|3|4)-[0-9]+([^0-9]|$) ]] && later=1
    [ "$later" = "1" ] || continue
    if [ "$rb" = "-" ]; then
      fail "$path: retired ($status) from 2b on without replaced_by (a fate-new row path or '<brief> acceptance <n>')"
    elif [[ "$rb" =~ ^[0-9a-z]+-[0-9]+\ acceptance\ [0-9]+ ]]; then
      :
    elif awk -F'\t' -v p="$rb" '$1 == p && $3 == "new" { found = 1 } END { exit !found }' "$TMP/entries.tsv" && [ -f "$rb" ]; then
      :
    else
      fail "$path: replaced_by '$rb' is neither a present fate-new row nor '<brief> acceptance <n>'"
    fi
  done < "$TMP/entries.tsv"
fi

rest_lines=$((cur_total - c1_lines - c2_lines))
rest_tgt=$((total - c1_tgt - c2_tgt))
rest_pct="$(awk -v a="$rest_lines" -v b="$rest_tgt" 'BEGIN { printf "%.1f", (a - b) * 100 / b }')"

# Summary by fate
echo "Manifest: $MANIFEST"
printf '%-9s %6s %10s %12s\n' fate count v2_lines target_lines
awk -F'\t' '
  { c[$3]++; v[$3] += $2; t[$3] += $4; C += 1; V += $2; T += $4 }
  END {
    n = split("keep shrink replace review delete new", order, " ")
    for (i = 1; i <= n; i++) { f = order[i]; if (f in c) printf "%-9s %6d %10d %12d\n", f, c[f], v[f], t[f] }
    printf "%-9s %6d %10d %12d\n", "TOTAL", C, V, T
  }' "$TMP/entries.tsv"
echo "target sum $total vs expected $EXPECTED_TOTAL (allowed $lo..$hi; fate new rows excluded)"
echo "v2 rows kept: $(LC_ALL=en_US.UTF-8 fmtn "$cur_total"), ceiling $(LC_ALL=en_US.UTF-8 fmtn "$hi") (deferred-replace rows excluded: $deferred_total lines)$( [ "$over_ceiling" = "1" ] && [ "$decision" = "1" ] && echo '; over the ceiling, accepted by the end-of-2a-2 decision' )$( [ "$FINAL" = "1" ] && echo ' [--final]' )"
echo "3.0 additions: $new_files files, $(LC_ALL=en_US.UTF-8 fmtn "$new_lines") lines (fate new; outside the target-sum rule and the ceiling)"
echo "size of 3.0: $(LC_ALL=en_US.UTF-8 fmtn $((cur_total + new_lines))) lines (v2 rows kept plus 3.0 additions)"
echo "like for like, class 1 and 2 rows and their targets removed: $rest_lines lines against a target sum of $rest_tgt ($rest_pct percent over)"
echo "class 1 (deferred-2b): $c1_lines lines, $c1_over over target; class 2 (deferred-3): $c2_lines lines, $c2_over over target; class 3 (accepted-3 and other non-keep rows): $c3_over over target"
awk -F'\t' '$8 ~ /^2a-2 debit/ { printf "2a-2 debit: %s (%s lines now): %s\n", $1, ($7 != "-" ? $7 : $2), $8 }' "$TMP/entries.tsv"

if [ "$errors" -gt 0 ]; then
  echo "manifest-check: $errors violation(s)" >&2
  exit 1
fi
echo "manifest-check: OK"
