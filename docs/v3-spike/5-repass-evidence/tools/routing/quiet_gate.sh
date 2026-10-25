#!/usr/bin/env bash
# quiet_gate.sh OUT [RACE_PACKAGES...]: wait for 60 s of continuous quiet (no
# seat-exec, scheduler run, pi run, amq coop, amq-remote or amq-squad lead,
# team or launch process), then run gate.sh beside this script; a gate
# refused at its start waits again. Gives up after 3 hours (exit 4).
pat='seat-exec|scheduler run|pi run|amq coop|amq-remote|amq-squad (lead|team|launch)'
here="$(cd "$(dirname "$0")" && pwd)"
deadline=$(( $(date +%s) + 10800 ))
while [ "$(date +%s)" -lt "$deadline" ]; do
  quiet=0
  while [ "$quiet" -lt 6 ] && [ "$(date +%s)" -lt "$deadline" ]; do
    if ps -axo command | grep -E "$pat" | grep -qv grep; then quiet=0; else quiet=$((quiet + 1)); fi
    sleep 10
  done
  echo "quiet for 60 s at $(date -u +%H:%M:%S)"
  bash "$here/gate.sh" "$@"
  code=$?
  if [ "$code" -ne 3 ]; then exit "$code"; fi
done
echo "no quiet window in 3 hours"
exit 4
