# shellcheck shell=bash
# Source at the top of every operator command block:  . /path/to/spike/loadtest/env.sh
# (shell state does not persist between tool calls). Defines the paths and the run id used by the runbook.
L="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" && pwd)"
export L
export WT="$(cd "$L/../.." && pwd)"          # the harness worktree (branch spike/s6-loadtest)
export NOC=/Users/omri/code/amq-noc
export WCLI=/Users/omri/code/workspace-cli
export RUN="${RUN:-s6-claude-20261022}"      # one id for the whole run
export OUT="$HOME/.amq-squad/spike/s6/$RUN"  # all run data lands here
export PATH="$HOME/go/bin:$PATH"             # amq-squad v2.31.0
