#!/bin/sh
# Lead's probe for PR #945: the real amq-remote 0.116.2 with serve's --poll
# shortened, to widen the reconcile-tick window. Never committed.
real=/Users/omri/go/bin/amq-remote
if [ "$1" = up ] || [ "$1" = serve ]; then
  verb=$1; shift
  exec "$real" "$verb" --poll 5ms "$@"
fi
exec "$real" "$@"
