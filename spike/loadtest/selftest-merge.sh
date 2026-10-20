#!/usr/bin/env bash
# selftest-merge.sh: dry run of mergeability.sh against a throwaway bare "origin", three
# loadtest/* branches (N5, N6 clean; N7 conflicts with N5 once N5 is merged) and a fake gh.
# No network, no GitHub, none of the real repos touched.
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export S6_OUT_BASE="${S6_OUT_BASE:-$HOME/.amq-squad/spike/s6}"
RUN=dryrun-merge
rm -rf "${S6_OUT_BASE:?}/$RUN"
T="$(mktemp -d)"; T="$(cd "$T" && pwd -P)"
trap 'rm -rf "$T"' EXIT
G="git -c user.name=t -c user.email=t@example.invalid -c commit.gpgsign=false"
$G init -q --bare "$T/origin.git"
$G clone -q "$T/origin.git" "$T/fakerepo" 2>/dev/null
cd "$T/fakerepo"
$G checkout -q -b main
printf 'one\ntwo\nthree\n' > a.txt; printf 'x\ny\nz\n' > b.txt
printf 'module fake\n\ngo 1.21\n' > go.mod; printf 'package main\n\nfunc main() {}\n' > main.go
$G add . && $G commit -q -m base && $G push -q origin main
mk() { # branch file newline-content
  $G checkout -q -b "$1" main; printf '%s\n' "$3" > "$2"; $G add "$2"; $G commit -q -m "$1"; $G push -q origin "$1"
  $G checkout -q main
}
mk loadtest/N5 a.txt "N5 version"
mk loadtest/N6 b.txt "N6 version"
mk loadtest/N7 a.txt "N7 version"
n=1
for b in N5 N6 N7; do
  $G --git-dir="$T/origin.git" update-ref "refs/pull/$n/head" "$($G --git-dir="$T/origin.git" rev-parse "refs/heads/loadtest/$b")"
  n=$((n+1))
done
cat > "$T/gh" <<'SH'
#!/bin/sh
if [ "$2" = "list" ]; then
cat <<'JSON'
[{"number":1,"headRefName":"loadtest/N5","baseRefName":"main","url":"https://example.invalid/1","state":"OPEN","isDraft":true,"title":"N5"},
 {"number":2,"headRefName":"loadtest/N6","baseRefName":"main","url":"https://example.invalid/2","state":"OPEN","isDraft":true,"title":"N6"},
 {"number":3,"headRefName":"loadtest/N7","baseRefName":"main","url":"https://example.invalid/3","state":"OPEN","isDraft":true,"title":"N7"},
 {"number":9,"headRefName":"feature/other","baseRefName":"main","url":"https://example.invalid/9","state":"OPEN","isDraft":false,"title":"ignored"}]
JSON
elif [ "$2" = "view" ]; then
case "$3" in
 1) echo '{"mergeable":"MERGEABLE","mergeStateStatus":"CLEAN","files":[{"path":"a.txt"}],"headRefOid":"x","baseRefName":"main"}';;
 2) echo '{"mergeable":"MERGEABLE","mergeStateStatus":"CLEAN","files":[{"path":"b.txt"},{"path":"CLAUDE.md"}],"headRefOid":"x","baseRefName":"main"}';;
 3) echo '{"mergeable":"CONFLICTING","mergeStateStatus":"DIRTY","files":[{"path":"a.txt"}],"headRefOid":"x","baseRefName":"main"}';;
esac
fi
SH
chmod +x "$T/gh"
# point origin at the local bare repo with a github-looking url is unnecessary: slug is only passed to the fake gh
S6_GH="$T/gh" "$HERE/mergeability.sh" "$RUN" "$T/fakerepo" | tee "$T/out.txt"
echo "--- checks"
grep -qE 'N5 +1 OPEN +MERGEABLE +CLEAN +clean +clean' "$T/out.txt" && echo "PASS N5 clean" || { echo FAIL N5; exit 1; }
grep -qE 'N7 +3 OPEN +CONFLICTING +DIRTY +clean +CONFLICT +a.txt' "$T/out.txt" && echo "PASS N7 conflicts only after N5 (cumulative)" || { echo FAIL N7; exit 1; }
grep -q 'staged-local-files:CLAUDE.md' "$T/out.txt" && echo "PASS local-file flag" || { echo FAIL flag; exit 1; }
grep -q 'cumulative dry-run conflicts=1' "$T/out.txt" && echo "PASS summary" || { echo FAIL summary; exit 1; }
grep -q 'go build after merging clean PRs: ok' "$T/out.txt" && echo "PASS build check" || { echo FAIL build; exit 1; }
test -z "$($G -C "$T/fakerepo" worktree list | grep s6-merge || true)" && echo "PASS temp worktree removed" || { echo FAIL worktree left; exit 1; }
test "$($G -C "$T/fakerepo" branch --list | wc -l | tr -d ' ')" = "4" && echo "PASS no branches created" || { $G -C "$T/fakerepo" branch --list; echo "FAIL branches"; exit 1; }
echo "selftest-merge OK"
