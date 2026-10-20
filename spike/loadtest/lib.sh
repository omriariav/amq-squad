# shellcheck shell=bash
# Shared helpers for the S6 load-test scripts. Source, do not execute.
#
# Every script takes:  RUN_ID REPO_PATH [REPO_PATH...]
# and writes under ${S6_OUT_BASE:-$HOME/.amq-squad/spike/s6}/RUN_ID/.

s6_args() { # usage: s6_args "$@" ; sets RUN_ID, REPOS[], OUT
  if [ "$#" -lt 2 ]; then
    echo "usage: $(basename "$0") RUN_ID REPO_PATH [REPO_PATH...]" >&2
    exit 64
  fi
  RUN_ID="$1"; shift
  case "$RUN_ID" in
    *[!A-Za-z0-9._-]*|"") echo "bad RUN_ID '$RUN_ID' (use letters, digits, . _ -)" >&2; exit 64;;
  esac
  REPOS=()
  local r
  for r in "$@"; do
    [ -d "$r" ] || { echo "not a directory: $r" >&2; exit 64; }
    REPOS+=("$(cd "$r" && pwd -P)")
  done
  OUT="${S6_OUT_BASE:-$HOME/.amq-squad/spike/s6}/$RUN_ID"
  mkdir -p "$OUT"
}

s6_here() { cd "$(dirname "${BASH_SOURCE[1]}")" && pwd; }

# The AMQ session of a repo's default squad profile (first member's session pin).
s6_session_of() { # $1 = repo
  python3 -I - "$1/.amq-squad/team.json" <<'PY'
import json, sys
try:
    d = json.load(open(sys.argv[1]))
    for m in d.get("members", []):
        if m.get("session"):
            print(m["session"]); break
except Exception:
    pass
PY
}

s6_run_start() { # prints run start epoch from OUT/run-start.epoch, or empty
  [ -s "$OUT/run-start.epoch" ] && cat "$OUT/run-start.epoch" || true
}

# GitHub owner/name from a repo's origin remote.
s6_slug_of() { # $1 = repo
  git -C "$1" remote get-url origin 2>/dev/null | sed -E 's#^(https://github.com/|git@github.com:)##; s#\.git$##'
}
