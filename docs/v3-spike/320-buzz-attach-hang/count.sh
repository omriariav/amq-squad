#!/usr/bin/env bash
set -u

mode=${1:-}
runs=${2:-1}
out=${3:-${TMPDIR:-/tmp}/amq-squad-attach-hang-count}
start=${4:-1}

case "$mode" in
  A|B|C|D) ;;
  *)
    echo "usage: $0 A|B|C|D [runs] [output-directory] [start-index]" >&2
    exit 2
    ;;
esac
for value in "$runs" "$start"; do
  case "$value" in
    ''|*[!0-9]*|0)
      echo "runs and start-index must be positive integers" >&2
      exit 2
      ;;
  esac
done

mkdir -p "$out"
summary="$out/${mode}.tsv"
if [[ ! -e "$summary" ]]; then
  printf 'run\texit\tseconds\tload_before\tload_after\thang_point\tcompanion_exit\tlog\n' >"$summary"
fi

load_average() {
  if command -v sysctl >/dev/null 2>&1; then
    sysctl -n vm.loadavg 2>/dev/null || uptime
  else
    uptime
  fi
}

# The module-wide rows model the locked gate, which runs outside a live seat.
# Leaving the seat identity in place makes harness-only tests refuse by design.
gate_env=(env
  -u AM_ME -u AM_ROOT -u AM_BASE_ROOT -u AM_SESSION
  -u AM_ROOT_ID -u AM_BASE_ROOT_ID
  -u AMQ_SQUAD_SEAT -u AMQ_SQUAD_REQUEST_ID -u AMQ_SQUAD_BRANCH
  -u AMQ_SQUAD_ROLE_PATH -u AMQ_SQUAD_ROLE_SHA256
  -u AMQ_SQUAD_RULES_PATH -u AMQ_SQUAD_RULES_SHA256)

run_one() {
  log=$1
  case "$mode" in
    A)
      env AMQ_SQUAD_EVIDENCE_RUN_BUDGET=${AMQ_SQUAD_EVIDENCE_RUN_BUDGET:-120s} \
        go test ./internal/buzz -run '^TestOwnerScriptDryRun$' -count=1 -v >"$log" 2>&1
      ;;
    B)
      env AMQ_SQUAD_EVIDENCE_RUN_BUDGET=${AMQ_SQUAD_EVIDENCE_RUN_BUDGET:-120s} \
        go test ./internal/buzz -count=1 -v >"$log" 2>&1
      ;;
    C)
      "${gate_env[@]}" AMQ_SQUAD_EVIDENCE_RUN_BUDGET=${AMQ_SQUAD_EVIDENCE_RUN_BUDGET:-120s} \
        go test ./... -count=1 >"$log" 2>&1
      ;;
    D)
      companion=${log%.log}.companion.log
      "${gate_env[@]}" AMQ_SQUAD_EVIDENCE_RUN_BUDGET=${AMQ_SQUAD_EVIDENCE_RUN_BUDGET:-120s} \
        go test ./... -count=1 >"$companion" 2>&1 &
      companion_pid=$!
      env AMQ_SQUAD_EVIDENCE_RUN_BUDGET=${AMQ_SQUAD_EVIDENCE_RUN_BUDGET:-120s} \
        go test ./internal/buzz -run '^TestOwnerScriptDryRun$' -count=1 -v >"$log" 2>&1
      main_rc=$?
      wait "$companion_pid"
      companion_rc=$?
      return "$main_rc"
      ;;
  esac
}

i=$start
last=$((start+runs-1))
while [[ "$i" -le "$last" ]]; do
  log="$out/${mode}-${i}.log"
  before=$(load_average)
  started=$(date +%s)
  companion_rc='-'
  run_one "$log"
  rc=$?
  finished=$(date +%s)
  after=$(load_average)
  hang=$(sed -n 's/.*hung: no signal for "\([^"]*\)".*/\1/p' "$log" | tail -1)
  printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' \
    "$i" "$rc" "$((finished-started))" "$before" "$after" "${hang:--}" "$companion_rc" "$(basename "$log")" >>"$summary"
  i=$((i+1))
done

cat "$summary"
