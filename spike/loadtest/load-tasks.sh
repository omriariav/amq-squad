#!/usr/bin/env bash
# load-tasks.sh: load the S6 task briefs into one repo's v2.31 task store.
#
#   load-tasks.sh render                      write tasks/<ID>.md briefs (no store writes)
#   load-tasks.sh load  REPO_PATH SESSION     amq-squad task add, one per task (idempotent)
#   load-tasks.sh check REPO_PATH SESSION     compare the store with tasks.tsv
#
# Source of truth: tasks/tasks.tsv (21 tasks: N1-N10, N13, W1-W9, W12).
# Held back for the later Codex run: N11, N12, W10, W11.
# The store lives in REPO_PATH/.amq-squad/tasks/SESSION/ and stays untracked.
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TSV="$HERE/tasks/tasks.tsv"
BIN="${AMQ_SQUAD_BIN:-$HOME/go/bin/amq-squad}"
MODE="${1:-}"

body_for() { # $1 = task id; prints the full brief text for the task
  python3 -I - "$TSV" "$1" <<'PY'
import csv, sys
tsv, tid = sys.argv[1], sys.argv[2]
with open(tsv, newline='') as f:
    for r in csv.DictReader(f, delimiter='\t', quoting=csv.QUOTE_NONE):
        if r['id'] == tid:
            extra = (r['extra'] or '').strip()
            out = f"""Task {r['id']} ({r['repo']}, source: {r['source']}, size {r['size']})

What to do:
{r['description']}

File(s): {r['files']}
Verify command (run it from your worktree root BEFORE opening the PR; paste the result in the PR body):
  {r['verify']}
  Also keep `go build ./...` green.
Conflict note: {r['conflict']}
"""
            if extra:
                out += f"Special instruction: {extra}\n"
            out += f"""
Workflow (team rules apply): one PR for this task only. Branch name: loadtest/{r['id']}
(create it from a fresh `git fetch origin main`, base origin/main). PR title: "[loadtest] {r['id']}: {r['title']}".
Open the PR as a draft against main, never merge, never push to main. After the PR is open, mark the
task done in the task store, report the PR URL and the verify result to the lead over AMQ, and stop.
If you cannot finish, mark the task failed with the reason and report it. Do not start another task
until the lead dispatches one.
"""
            print(out, end='')
            sys.exit(0)
sys.exit(f"unknown task id {tid}")
PY
}

ids_for_repo() { # $1 = repo dir name (amq-noc | workspace-cli)
  python3 -I - "$TSV" "$1" <<'PY'
import csv, sys
with open(sys.argv[1], newline='') as f:
    for r in csv.DictReader(f, delimiter='\t', quoting=csv.QUOTE_NONE):
        if r['repo'] == sys.argv[2]:
            print(r['id'])
PY
}

title_for() {
  python3 -I - "$TSV" "$1" <<'PY'
import csv, sys
with open(sys.argv[1], newline='') as f:
    for r in csv.DictReader(f, delimiter='\t', quoting=csv.QUOTE_NONE):
        if r['id'] == sys.argv[2]:
            print(f"{r['id']}: {r['title']}")
PY
}

case "$MODE" in
  render)
    for repo in amq-noc workspace-cli; do
      for id in $(ids_for_repo "$repo"); do
        body_for "$id" > "$HERE/tasks/$id.md"
      done
    done
    echo "rendered $(ls "$HERE"/tasks/*.md | wc -l | tr -d ' ') briefs under $HERE/tasks/"
    ;;
  load|check)
    REPO="${2:?usage: load-tasks.sh $MODE REPO_PATH SESSION}"
    SESSION="${3:?usage: load-tasks.sh $MODE REPO_PATH SESSION}"
    NAME="${S6_REPO_NAME:-$(basename "$REPO")}"
    existing="$("$BIN" task list --project "$REPO" --session "$SESSION" --json 2>/dev/null || true)"
    added=0; present=0; missing=0
    for id in $(ids_for_repo "$NAME"); do
      title="$(title_for "$id")"
      if printf '%s' "$existing" | grep -q "\"title\": \"$id: "; then
        present=$((present+1)); continue
      fi
      if [ "$MODE" = check ]; then
        echo "missing: $id"; missing=$((missing+1)); continue
      fi
      "$BIN" task add --project "$REPO" --session "$SESSION" --title "$title" --desc "$(body_for "$id")" >/dev/null
      echo "added $id"; added=$((added+1))
    done
    echo "$NAME/$SESSION: added=$added already_present=$present missing=$missing"
    [ "$missing" -eq 0 ]
    ;;
  *)
    sed -n '2,10p' "$0"; exit 1;;
esac
