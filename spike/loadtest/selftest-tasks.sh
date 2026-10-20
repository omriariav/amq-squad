#!/usr/bin/env bash
# selftest-tasks.sh: dry run of task-times.sh against a throwaway v2.31 task store
# (real `amq-squad task add/claim/done/fail`, no seats, no repos touched).
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BIN="${AMQ_SQUAD_BIN:-$HOME/go/bin/amq-squad}"
export S6_OUT_BASE="${S6_OUT_BASE:-$HOME/.amq-squad/spike/s6}" S6_NO_GH=1
RUN=dryrun-tasks
rm -rf "${S6_OUT_BASE:?}/$RUN"
T="$(mktemp -d)"; T="$(cd "$T" && pwd -P)"
trap 'rm -rf "$T"' EXIT
mkdir -p "$T/fake-noc/.amq-squad"
cat > "$T/fake-noc/.amq-squad/team.json" <<'JSON'
{"schema":5,"trust":"approve-for-me","members":[{"role":"lead","binary":"claude","handle":"lead","session":"s6-fake","actor_mode":"review"},{"role":"fullstack","binary":"claude","handle":"fullstack","session":"s6-fake","actor_mode":"implementation"}],"orchestrated":true,"lead":"lead"}
JSON
P="$T/fake-noc"
for spec in "N1: first" "N2: second" "N3: third" "N4: never claimed"; do
  "$BIN" task add --project "$P" --session s6-fake --title "$spec" --desc x >/dev/null
done
ONCE=1 "$HERE/task-times.sh" watch "$RUN" "$P"
"$BIN" task claim t1 --me fullstack --project "$P" --session s6-fake >/dev/null
"$BIN" task claim t2 --me fullstack --project "$P" --session s6-fake >/dev/null
"$BIN" task claim t3 --me fullstack --project "$P" --session s6-fake >/dev/null
ONCE=1 "$HERE/task-times.sh" watch "$RUN" "$P"
sleep 3
"$BIN" task done t1 --me fullstack --project "$P" --session s6-fake >/dev/null
"$BIN" task fail t2 --me fullstack --reason "not supported by API" --project "$P" --session s6-fake >/dev/null
ONCE=1 "$HERE/task-times.sh" watch "$RUN" "$P"
echo "--- report"
"$HERE/task-times.sh" report "$RUN" "$P" | tee "$T/report.txt"
echo "--- checks"
grep -q 'tasks=4 completed=1 failed/blocked/cancelled=1 open=2' "$T/report.txt" && echo "PASS counts" || { echo FAIL counts; exit 1; }
grep -q 'FAIL: not supported by API' "$T/report.txt" && echo "PASS failure reason" || { echo FAIL reason; exit 1; }
grep -qE 'N1 +completed +fullstack +0m0[3-9]s' "$T/report.txt" && echo "PASS N1 wall time from the snapshot lease" || { echo FAIL N1 wall; exit 1; }
head -3 "$S6_OUT_BASE/$RUN/tasks.csv"
echo "selftest-tasks OK"
