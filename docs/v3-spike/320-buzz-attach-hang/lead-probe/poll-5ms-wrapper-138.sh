#!/bin/sh
# Lead's probe: the real amq-remote v0.138.0 with serve's --poll shortened. Never committed.
real=<lead-tmp>/probe945/real-138/amq-remote
if [ "$1" = up ] || [ "$1" = serve ]; then
  verb=$1; shift
  exec "$real" "$verb" --poll 5ms "$@"
fi
exec "$real" "$@"
