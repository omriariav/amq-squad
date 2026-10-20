#!/usr/bin/env bash
# mergeability.sh RUN_ID REPO_PATH [REPO_PATH...]
#
# For each loadtest/* PR: `gh pr view --json mergeable,mergeStateStatus,...` plus a
# dry-run merge onto the base in a temporary git worktree (solo and cumulative), then
# `go build ./...` on the cumulative result. Never pushes, never comments, never merges,
# creates no branches or refs in the repo (fetches go to FETCH_HEAD only).
# Writes mergeability.csv and mergeability.txt under ~/.amq-squad/spike/s6/RUN_ID/.
# Run it after the squads are done, and at any time to peek (read-only on GitHub).
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
. "$HERE/lib.sh"
s6_args "$@"
exec python3 -I "$HERE/mergeability.py" "$OUT" "${REPOS[@]}"
