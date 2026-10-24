#!/usr/bin/env bash
# quiet_gate.sh OUT: wait for 60 s of continuous quiet (no live seat or session), then run gate.sh OUT.
# The gate script itself refuses if a live process appears at its start; then this waits again.
pat='seat-exec|scheduler run|pi run|amq coop|amq-remote|amq-squad (lead|team|launch)'
for round in 1 2 3 4 5 6 7 8 9 10; do
  quiet=0
  while [ "$quiet" -lt 6 ]; do
    if ps -axo command | grep -E "$pat" | grep -qv grep; then quiet=0; else quiet=$((quiet + 1)); fi
    sleep 10
  done
  echo "quiet for 60 s at $(date -u +%H:%M:%S) (round $round)"
  bash /tmp/2b4tools/gate.sh "$1"
  code=$?
  if [ "$code" -ne 3 ]; then exit "$code"; fi
done
exit 3
