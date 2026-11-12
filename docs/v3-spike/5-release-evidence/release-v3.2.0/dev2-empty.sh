#!/bin/sh
# W: dev2's worktree emptied of the cancelled t178 attempt's leftover, under the owner's
# word of 2026-11-12 ("W - ok"). The leftover is saved first, outside the repository.
# Touches the seven paths the status lists and nothing else. Stops at the first surprise.
set -eu
W=<home>/code/amq-squad/.worktrees/dev2
O=<home>/amq-squad-lead/T/v320/dev2-leftover
mkdir -p "$O/untracked"
date -u +"start %Y-%m-%dT%H:%M:%SZ"
echo "--- processes with a cwd in dev2's worktree"
n=$(lsof -a -d cwd -Fpn 2>/dev/null | awk '/^p/{p=substr($0,2)} /^n<home>\/code\/amq-squad\/.worktrees\/dev2(\/|$)/{print p}' | sort -u | wc -l | tr -d ' ')
echo "count: $n"; [ "$n" = 0 ] || { echo "a process lives in the worktree; stop"; exit 1; }
echo "--- before"
git -C "$W" rev-parse HEAD; git -C "$W" symbolic-ref HEAD
git -C "$W" status --porcelain --untracked-files=all | tee "$O/status-before.txt"
exp=' M internal/statecontract/contract.go
 M pi/test/state-contract.json
?? docs/v3-spike/330-s1-2b-reasons/README.md
?? docs/v3-spike/330-s1-2b-reasons/corrections-to-s1-2a.md
?? docs/v3-spike/330-s1-2b-reasons/sites.md
?? internal/statecontract/reasons.go
?? internal/statecontract/reasons_test.go'
[ "$(cat "$O/status-before.txt")" = "$exp" ] || { echo "the status is not the seven expected lines; stop"; exit 1; }
echo "--- save"
git -C "$W" diff --binary HEAD -- internal/statecontract/contract.go pi/test/state-contract.json > "$O/tracked.patch"
for f in docs/v3-spike/330-s1-2b-reasons/README.md docs/v3-spike/330-s1-2b-reasons/corrections-to-s1-2a.md docs/v3-spike/330-s1-2b-reasons/sites.md internal/statecontract/reasons.go internal/statecontract/reasons_test.go; do
  mkdir -p "$O/untracked/$(dirname "$f")"; cp -p "$W/$f" "$O/untracked/$f"; cmp "$W/$f" "$O/untracked/$f"
done
(cd "$O" && find tracked.patch untracked -type f | LC_ALL=C sort | xargs shasum -a 256) | tee "$O/SHA256SUMS"
[ -s "$O/tracked.patch" ] || { echo "the patch is empty; stop"; exit 1; }
echo "--- restore the two tracked files, remove the five untracked"
git -C "$W" checkout HEAD -- internal/statecontract/contract.go pi/test/state-contract.json
for f in docs/v3-spike/330-s1-2b-reasons/README.md docs/v3-spike/330-s1-2b-reasons/corrections-to-s1-2a.md docs/v3-spike/330-s1-2b-reasons/sites.md internal/statecontract/reasons.go internal/statecontract/reasons_test.go; do rm "$W/$f"; done
rmdir "$W/docs/v3-spike/330-s1-2b-reasons" 2>/dev/null && echo "removed the emptied directory docs/v3-spike/330-s1-2b-reasons" || echo "the directory docs/v3-spike/330-s1-2b-reasons stays (not empty or tracked)"
echo "--- after"
echo "porcelain lines: $(git -C "$W" status --porcelain --untracked-files=all | wc -l | tr -d ' ')"
git -C "$W" rev-parse HEAD; git -C "$W" symbolic-ref HEAD
date -u +"end %Y-%m-%dT%H:%M:%SZ"
