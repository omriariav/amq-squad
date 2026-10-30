#!/bin/sh
# Run amq from the S4 scratch project with spike-scoped launch state.
# CODEX_PIN=0147 puts the pinned codex-cli 0.147.0 prefix first on PATH.
# Usage: amq-spike.sh launch --request FILE --prepare --json
set -eu
S4="$HOME/.amq-squad/spike/s4"
export XDG_STATE_HOME="$S4/state"   # trust grants land in $S4/state/amq/launch, not ~/.amq
export AMQ_NO_UPDATE_CHECK=1
if [ "${CODEX_PIN:-}" = "0147" ]; then
  PATH="$HOME/.amq-squad/spike/codex-0.147/node_modules/.bin:$PATH"
  export PATH
fi
cd "${AMQ_SPIKE_CWD:-$S4/project}"
exec amq "$@"
