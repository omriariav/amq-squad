#!/usr/bin/env bash
# teardown.sh [--keep-keys]
#
# Undo enroll.sh: stop the amq-remote endpoint it started, and remove the
# scratch AMQ root (body key, enrolled grants, manifest, ledgers) and the
# share/bundle files. --keep-keys keeps the root for a later run.
#
# What this script cannot undo, because it is the owner's relay state:
#   - the 1:1 DM channel with the body in Buzz (archive or leave it);
#   - the owner's kind 30177 policy for the body and the body's relay
#     admission: the grants expire on their own (share --days, default 30);
#     to revoke now, remove the body from the relay or ban its key
#     (cmd/amq-remote/README.md "What the relay checks").
set -uo pipefail

S1=${S1_HOME:-$HOME/.amq-squad/spike/s1}
ROOT=${S1_ROOT:-$S1/root}
KEEP=0
[[ ${1:-} == --keep-keys ]] && KEEP=1

case $ROOT in
  "$HOME"/.amq-squad/spike/*) ;;
  *) echo "refusing: S1_ROOT $ROOT is not under ~/.amq-squad/spike/" >&2; exit 2 ;;
esac

if [[ -f $S1/up.pid ]]; then
  pid=$(cat "$S1/up.pid")
  if kill -0 "$pid" 2>/dev/null; then
    echo "stopping amq-remote up (pid $pid)"
    kill -TERM "$pid"
    for _ in $(seq 1 20); do kill -0 "$pid" 2>/dev/null || break; sleep 0.5; done
    kill -0 "$pid" 2>/dev/null && kill -KILL "$pid"
  fi
  rm -f "$S1/up.pid"
fi
# A serve child that outlived its supervisor, for this root only.
pkill -TERM -f "amq-remote serve .*--root $ROOT" 2>/dev/null || true
pkill -TERM -f "amq-remote serve --root $ROOT" 2>/dev/null || true

if [[ $KEEP == 1 ]]; then
  echo "kept $ROOT"
  exit 0
fi
rm -rf "$ROOT" "$S1/share.txt" "$S1/bundle.json" "$S1/policy.published" "$S1/owner-sign.log" "$S1/dm-channel" "$S1/s1.env" "$S1/doctor.json" "$S1/doctor.err" "$S1/realkey"
echo "removed $ROOT and the share files"
echo "owner: archive the DM with the body in Buzz; the grants expire on their own"
