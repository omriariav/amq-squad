#!/bin/bash
# Run a command in the environment the S3 criterion requires:
# no pi, node or bun reachable on PATH; only HOME, PATH=/usr/bin:/bin, TERM.
# Equivalent to: env -i HOME=$HOME PATH=/usr/bin:/bin TERM=xterm <cmd>
# Extra VAR=value pairs before "--" are passed through (e.g. PI_CODING_AGENT_DIR).
extra=()
while [[ $# -gt 0 && "$1" != "--" ]]; do extra+=("$1"); shift; done
[[ "${1:-}" == "--" ]] && shift
exec /usr/bin/env -i HOME="$HOME" PATH=/usr/bin:/bin TERM=xterm "${extra[@]}" "$@"
