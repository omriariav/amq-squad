#!/usr/bin/env bash
# setup-brief.sh REPO_PATH SESSION
# Renders briefs/brief.md.tmpl for one repo, seeds it as the workstream brief
# (amq-squad brief --seed-from file:), and appends the S6 rules to the repo's
# .amq-squad/team-rules.md once (marker-guarded). Writes only untracked files
# (.amq-squad/) plus a rendered copy under ~/.amq-squad/spike/s6/briefs/.
set -euo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="${1:?usage: setup-brief.sh REPO_PATH SESSION}"
SESSION="${2:?usage: setup-brief.sh REPO_PATH SESSION}"
BIN="${AMQ_SQUAD_BIN:-$HOME/go/bin/amq-squad}"
NAME="${S6_REPO_NAME:-$(basename "$REPO")}"
OUT="$HOME/.amq-squad/spike/s6/briefs"
mkdir -p "$OUT"

case "$NAME" in
  amq-noc) BASELINE='Go module, `go test ./... -count=1` takes about 5 s and passes (10 packages). `make ci` is fmt-check, vet, test.' ;;
  workspace-cli) BASELINE='Go module (gws CLI), `go test ./... -count=1` takes about 13 s and passes (11 packages). `make test`, `make vet`, `make lint` exist. Note: the repo root CLAUDE.md and AGENTS.md are tracked project files; do not modify or stage them.' ;;
  *) BASELINE='see the repo Makefile' ;;
esac

IDS="$(python3 -I - "$HERE/tasks/tasks.tsv" "$NAME" <<'PY'
import csv, sys
ids = [r['id'] for r in csv.DictReader(open(sys.argv[1], newline=''), delimiter='\t', quoting=csv.QUOTE_NONE) if r['repo'] == sys.argv[2]]
print(len(ids), ', '.join(ids))
PY
)"
COUNT="${IDS%% *}"; TASKIDS="${IDS#* }"

RENDERED="$OUT/$SESSION.md"
python3 -I - "$HERE/briefs/brief.md.tmpl" "$HERE/briefs/s6-rules.md" "$RENDERED" "$NAME" "$SESSION" "$REPO" "$COUNT" "$TASKIDS" "$BASELINE" <<'PY'
import sys
tmpl, rules, out, repo, session, root, count, ids, baseline = sys.argv[1:]
t = open(tmpl).read()
for k, v in {'{{RULES}}': open(rules).read().rstrip(), '{{REPO}}': repo, '{{SESSION}}': session,
             '{{ROOT}}': root, '{{COUNT}}': count, '{{TASKIDS}}': ids, '{{BASELINE}}': baseline}.items():
    t = t.replace(k, v)
open(out, 'w').write(t)
PY
echo "rendered $RENDERED"

"$BIN" brief --seed-from "file:$RENDERED" --session "$SESSION" --project "$REPO" --force

RULES_FILE="$REPO/.amq-squad/team-rules.md"
if ! grep -q 'S6 load test rules' "$RULES_FILE"; then
  { echo; cat "$HERE/briefs/s6-rules.md"; } >> "$RULES_FILE"
  echo "appended S6 rules to $RULES_FILE"
else
  echo "S6 rules already present in $RULES_FILE"
fi
