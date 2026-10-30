#!/bin/bash
# S3 probe: with no PI_* variables set, the launcher applies the 2b defaults
# and pi uses ~/.amq-squad/pi-agent (with fd/rg linked into bin/), never ~/.pi.
# Runs under a throwaway HOME so the real ~/.amq-squad/pi-agent is not created.
set -u
cand="${1:-node}"
here="$(cd "$(dirname "$0")" && pwd)"
fake="$HOME/.amq-squad/spike/s3/fakehome"
/bin/rm -rf "$fake"; mkdir -p "$fake"
/usr/bin/env -i HOME="$fake" PATH=/usr/bin:/bin TERM=xterm EMBEDPI_RUNTIME_DIR="$HOME/.amq-squad/runtime" \
  "$here/bin/embedpi-$cand" --help > /dev/null
echo "exit=$?"
echo "== files created under the throwaway HOME"
(cd "$fake" && /usr/bin/find . | /usr/bin/sort)
echo "== bin links"; /bin/ls -l "$fake/.amq-squad/pi-agent/bin" 2>&1 | /usr/bin/sed 1d
[[ -e "$fake/.pi" ]] && echo "FAIL: ~/.pi created" || echo "ok: no ~/.pi"
/bin/rm -rf "$fake"
