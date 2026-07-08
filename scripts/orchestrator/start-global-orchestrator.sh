#!/usr/bin/env bash
#
# start-global-orchestrator.sh
#
# Launch a *global / NOC* orchestrator: a control-plane agent conversation that
# supervises MANY runs across repos from a neutral root. It is a POLLER by
# design -- it owns no single mailbox, so there is nothing to wake it on. It
# drives each run by explicit --project/--profile/--session and keeps the
# multi-run board (see the amq-squad-orchestrator skill).
#
# What this script does (the deterministic setup around the conversation):
#   1. Preflight: tmux present, amq-squad + amq on PATH, doctor sanity.
#   2. Open a new tmux window at the neutral root.
#   3. Launch the agent binary (claude|codex) there.
# It does NOT register a pane or start wake -- that is Mode B (project) work.
#
# Usage:
#   start-global-orchestrator.sh [-C root_dir] [-a claude|codex] [-n window_name]
#
# Options:
#   -C  root dir the supervisor runs from        (default: $HOME/Code)
#   -a  agent binary: claude | codex             (default: claude)
#   -n  tmux window name                         (default: global-orch)
#   -h  show this help
#
set -euo pipefail

ROOT="${HOME}/Code"
AGENT="claude"
WNAME="global-orch"

usage() { sed -n '2,30p' "$0" | sed 's/^# \{0,1\}//'; exit "${1:-0}"; }

while getopts ":C:a:n:h" opt; do
  case "$opt" in
    C) ROOT="$OPTARG" ;;
    a) AGENT="$OPTARG" ;;
    n) WNAME="$OPTARG" ;;
    h) usage 0 ;;
    \?) echo "unknown option: -$OPTARG" >&2; usage 1 ;;
    :) echo "option -$OPTARG needs a value" >&2; usage 1 ;;
  esac
done

die() { echo "[error] $*" >&2; exit 1; }
note() { echo "[global-orch] $*"; }

# --- Preflight ---------------------------------------------------------------
[ -n "${TMUX:-}" ] || die "not inside tmux. Start tmux first: visible spawns and the default wake injector both require it."
command -v amq-squad >/dev/null 2>&1 || die "amq-squad not on PATH."
command -v amq        >/dev/null 2>&1 || die "amq not on PATH (amq-squad needs it; floor is 0.40.0)."
[ -d "$ROOT" ] || die "root dir does not exist: $ROOT"
case "$AGENT" in claude|codex) ;; *) die "agent must be claude or codex, got: $AGENT" ;; esac
command -v "$AGENT" >/dev/null 2>&1 || die "$AGENT not on PATH."

note "amq-squad $(amq-squad version 2>/dev/null | head -1)"
note "root=$ROOT  agent=$AGENT  window=$WNAME"

# doctor is advisory here (no single project); surface skew but don't block.
amq-squad doctor 2>&1 | sed 's/^/[doctor] /' || true

# --- Launch ------------------------------------------------------------------
tmux new-window -c "$ROOT" -n "$WNAME" "$AGENT"
tmux select-window -t "$WNAME" 2>/dev/null || true

cat <<'EOF'

Global orchestrator launched (poller mode -- no wake by design).
Inside the new window, invoke the amq-squad-orchestrator skill, then drive each
run by explicit namespace (never by cwd):

  amq-squad goal draft  --goal "..." --repo <owner/repo> --session <s> --profile <p> --lead <role> --skill-invocation
  amq-squad goal start  --project <repo> --profile <p> --session <s> --goal "..." --dry-run --json
  amq-squad goal start  --project <repo> --profile <p> --session <s> --goal "..." --yes --json

Poll / steer / approve:
  amq-squad monitor  --project <repo> --profile <p> --session <s> --once --json
  amq-squad status   --project <repo> --profile <p> --session <s> --json
  amq-squad next     --project <repo> --profile <p> --session <s> --json
  amq-squad operator answer   --project <repo> --profile <p> --session <s> --gate <topic> --to <lead> --approved --reason "..."
  amq-squad operator directive --project <repo> --profile <p> --session <s> --to <lead> --subject "..." --body "..."

To drive ONE run wake-first instead of polling it, use start-project-orchestrator.sh
with --project <repo> (it registers a pane + starts wake).
EOF
