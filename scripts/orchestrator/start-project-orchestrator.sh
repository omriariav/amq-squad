#!/usr/bin/env bash
#
# start-project-orchestrator.sh  --  CREATE a single orchestrated run.
#
# The hard part of orchestration is not polling; it is typing the create
# sequence correctly every time -- the roster, the spawn, the pane+wake binding
# -- with --project/--profile/--session repeated without a typo. This script
# fills the namespace ONCE and emits (and, with --go, runs) that create chain.
#
# Two create models:
#   managed        (default)  amq-squad spawns the whole team, incl. the lead, into
#                             sibling tmux tabs. Panes are registered + wake-live
#                             automatically. You attach to the lead window.
#                               new team (if --roles) -> up --visibility sibling-tabs
#   --external-lead           YOUR current agent pane IS the lead (e.g. a Claude
#                             Code conversation). Emits the in-pane commands to run
#                             FROM that pane: lead register --wake binds the pane,
#                             then up spawns the remaining workers as siblings.
#
# Safety: default is PREVIEW -- every real command is printed and the read-only
# --dry-run variants are executed so you see the plan with no mutation. Add --go
# to actually create (managed model only; external-lead always emits a paste block
# because pane binding must happen in the lead pane, not this shell).
#
# Usage:
#   start-project-orchestrator.sh -p PROJECT -s SESSION [options]
#
# Required:
#   -p PROJECT        project / team-home dir (repo root)
#   -s SESSION        workstream session name
# Options:
#   -P PROFILE        team profile                       (default: default profile)
#   -r ROLE           lead role                          (default: cto)
#   --roles "a,b,c"   create the roster first (new team --orchestrated --lead ROLE)
#   --binary R=BIN    per-role binary, e.g. fullstack=codex (repeatable; needs --roles)
#   -g "GOAL"         after spawn, deliver this goal to the lead
#   --seed-from REF   seed the workstream brief (e.g. issue:96)
#   --external-lead   your current pane is the lead; emit in-pane commands
#   -a claude|codex   agent for external-lead notes                (default: claude)
#   --go              execute for real (managed model); omit for preview
#   -h                help
#
set -euo pipefail

PROJECT="" ; SESSION="" ; PROFILE="" ; ROLE="cto"
ROLES="" ; GOAL="" ; SEED="" ; AGENT="claude"
EXTERNAL=0 ; GO=0
BINARIES=()

usage() { sed -n '2,50p' "$0" | sed 's/^# \{0,1\}//'; exit "${1:-0}"; }

while [ $# -gt 0 ]; do
  case "$1" in
    -p) PROJECT="$2"; shift 2 ;;
    -s) SESSION="$2"; shift 2 ;;
    -P) PROFILE="$2"; shift 2 ;;
    -r) ROLE="$2"; shift 2 ;;
    --roles) ROLES="$2"; shift 2 ;;
    --binary) BINARIES+=("$2"); shift 2 ;;
    -g) GOAL="$2"; shift 2 ;;
    --seed-from) SEED="$2"; shift 2 ;;
    --external-lead) EXTERNAL=1; shift ;;
    -a) AGENT="$2"; shift 2 ;;
    --go) GO=1; shift ;;
    -h|--help) usage 0 ;;
    *) echo "unknown arg: $1" >&2; usage 1 ;;
  esac
done

die()  { echo "[error] $*" >&2; exit 1; }
note() { echo "[project-orch] $*"; }

# Render a command for humans: quote only args that need it (spaces, commas...).
q() { case "$1" in ''|*[!A-Za-z0-9_/=:.@%+-]*) printf '"%s"' "$1" ;; *) printf '%s' "$1" ;; esac; }
show() { local out="" a; for a in "$@"; do out+=" $(q "$a")"; done; printf '  %s\n' "${out# }"; }

[ -n "$PROJECT" ] || die "missing -p PROJECT"
[ -n "$SESSION" ] || die "missing -s SESSION"
[ -d "$PROJECT" ] || die "project dir does not exist: $PROJECT"
[ -n "${TMUX:-}" ] || die "not inside tmux (spawns + default wake injector require it)."
command -v amq-squad >/dev/null 2>&1 || die "amq-squad not on PATH."
command -v amq        >/dev/null 2>&1 || die "amq not on PATH."

PROF_ARG=() ; [ -n "$PROFILE" ] && PROF_ARG=(--profile "$PROFILE")

# --- Preflight ---------------------------------------------------------------
note "$(amq-squad version 2>/dev/null | head -1)"
note "project=$PROJECT  profile=${PROFILE:-<default>}  session=$SESSION  lead=$ROLE  model=$([ $EXTERNAL = 1 ] && echo external-lead || echo managed)  mode=$([ $GO = 1 ] && echo GO || echo PREVIEW)"
amq-squad doctor --project "$PROJECT" "${PROF_ARG[@]}" 2>&1 | sed 's/^/[doctor] /' || true

# --- Compose the create commands as arrays (no eval) -------------------------
NT_CMD=()
if [ -n "$ROLES" ]; then
  NT_CMD=(amq-squad new team --project "$PROJECT" "${PROF_ARG[@]}" --roles "$ROLES" --orchestrated --lead "$ROLE")
  for b in "${BINARIES[@]}"; do NT_CMD+=(--binary "$b"); done
fi
UP_CMD=(amq-squad up "$SESSION" --project "$PROJECT" "${PROF_ARG[@]}" --visibility sibling-tabs)
[ -n "$SEED" ] && UP_CMD+=(--seed-from "$SEED")
REG_CMD=(amq-squad lead register --project "$PROJECT" "${PROF_ARG[@]}" --session "$SESSION" --role "$ROLE" --wake)
GOAL_CMD=() ; [ -n "$GOAL" ] && GOAL_CMD=(amq-squad goal start --project "$PROJECT" "${PROF_ARG[@]}" --session "$SESSION" --role "$ROLE" --goal "$GOAL")
STATUS_CMD=(amq-squad status --session "$SESSION" --project "$PROJECT" "${PROF_ARG[@]}" --json)

# --- External-lead model: emit the in-pane paste block, never auto-run --------
if [ "$EXTERNAL" = 1 ]; then
  cat <<EOF

=== EXTERNAL-LEAD create block ===================================================
Your current agent pane ($AGENT) becomes the lead. Run these FROM THAT PANE
(pane binding + wake must happen in the lead's own pane, so this shell can't do
it for you). If you drive Claude Code in that pane, run each with the '! ' prefix.
EOF
  local_n=1
  if [ -n "$ROLES" ]; then echo "# $local_n) roster (once):"; show "${NT_CMD[@]}"; local_n=2; fi
  echo "# $local_n) bind THIS pane as lead + start wake:"; show "${REG_CMD[@]}"
  echo "# spawn the remaining workers as sibling tabs:"; show "${UP_CMD[@]}"
  if [ -n "$GOAL" ]; then
    echo "# hand the goal to the lead (this pane) + register it as orchestrator:"
    show "${GOAL_CMD[@]}" --register-orchestrator --yes
  fi
  echo "# verify topology (mode should be sibling-tabs):"; show "${STATUS_CMD[@]}"
  echo "=================================================================================="
  exit 0
fi

# --- Managed model: plan, then preview (dry-run) or execute (--go) ------------
echo ""
echo "=== MANAGED create plan =========================================================="
[ -n "$ROLES" ] && { echo "# roster:"; show "${NT_CMD[@]}"; }
echo "# spawn team (lead + workers) into sibling tabs:"; show "${UP_CMD[@]}"
[ -n "$GOAL" ] && { echo "# deliver goal to lead:"; show "${GOAL_CMD[@]}" --yes; }
echo "=================================================================================="

if [ "$GO" != 1 ]; then
  echo ""
  note "PREVIEW only -- running read-only --dry-run validation; nothing is created."
  [ -n "$ROLES" ] && { note "roster dry-run:"; "${NT_CMD[@]}" --dry-run 2>&1 | sed 's/^/  /' || true; }
  note "spawn dry-run:"; "${UP_CMD[@]}" --dry-run 2>&1 | sed 's/^/  /' || true
  [ -n "$GOAL" ] && { note "goal dry-run:"; "${GOAL_CMD[@]}" --dry-run 2>&1 | sed 's/^/  /' || true; }
  echo ""
  note "Looks right? Re-run the same command with --go to create it."
  exit 0
fi

# --- Execute (managed) --------------------------------------------------------
if [ -n "$ROLES" ]; then
  if amq-squad team profiles --project "$PROJECT" --json 2>/dev/null | grep -q "\"${PROFILE:-default}\""; then
    note "profile ${PROFILE:-default} already exists; skipping new team."
  else
    note "creating roster..."; "${NT_CMD[@]}"
  fi
fi
note "spawning team into sibling tabs..."; "${UP_CMD[@]}"
note "verifying topology..."; "${STATUS_CMD[@]}" | grep -E '"mode"|"visible_problem"' || true
[ -n "$GOAL" ] && { note "delivering goal to lead..."; "${GOAL_CMD[@]}" --yes; }
note "done. Attach to the lead window and drive with dispatch/monitor/collect."
