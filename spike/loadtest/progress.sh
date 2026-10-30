#!/usr/bin/env bash
# progress.sh RUN_ID REPO_PATH [REPO_PATH...]
#
# One-screen progress snapshot, read-only: elapsed time, task counts per repo from the task
# store, loadtest/* PRs per repo (gh pr list, skipped with S6_NO_GH=1), latest RAM sample, sampler
# state, and the completion verdict (all tasks terminal, or 3 hours elapsed).
# Exit 0 = run is complete by the runbook's rule, 1 = keep going.
set -uo pipefail
HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
. "$HERE/lib.sh"
s6_args "$@"
now="$(date +%s)"; start="$(s6_run_start)"
if [ -n "$start" ]; then el=$((now - start)); echo "elapsed: $((el / 3600))h$(((el % 3600) / 60))m since run start (limit 3h00m)"; else el=0; echo "run start not recorded"; fi
terminal=0; total=0
for r in "${REPOS[@]}"; do
  n="$(basename "$r")"; s="$(s6_session_of "$r")"
  counts="$(python3 -I - "$r/.amq-squad/tasks/$s" <<'PY'
import glob, json, sys, collections
c = collections.Counter()
for f in glob.glob(sys.argv[1] + "/t*.json"):
    try: c[json.load(open(f)).get("status", "?")] += 1
    except Exception: c["unreadable"] += 1
t = sum(c.values()); term = sum(v for k, v in c.items() if k in ("completed", "failed", "cancelled", "blocked"))
print(f"{t} {term} " + " ".join(f"{k}={v}" for k, v in sorted(c.items())))
PY
)"
  t="${counts%% *}"; rest="${counts#* }"; tm="${rest%% *}"; detail="${rest#* }"
  total=$((total + t)); terminal=$((terminal + tm))
  prs="n/a"
  if [ "${S6_NO_GH:-0}" != 1 ]; then
    slug="$(s6_slug_of "$r")"
    prs="$(gh pr list --repo "$slug" --state all --limit 200 --json headRefName,isDraft,state 2>/dev/null | python3 -I -c 'import json,sys
try: d=[p for p in json.load(sys.stdin) if p["headRefName"].startswith("loadtest/")]
except Exception: d=[]
print(len(d), "(" + ",".join(sorted(set(p["state"] for p in d))) + ")")' 2>/dev/null || echo '?')"
  fi
  printf '%-14s session %-8s tasks: %s   loadtest PRs: %s\n' "$n" "$s" "$detail" "$prs"
done
echo "terminal tasks: $terminal / $total"
"$HERE/samplers.sh" status "$RUN_ID" "${REPOS[@]}" 2>/dev/null | sed 's/^/  /' | head -5
[ -s "$OUT/limit-summary.txt" ] && sed -n '3,4p' "$OUT/limit-summary.txt" | sed 's/^/  limits: /'
if [ "$total" -gt 0 ] && [ "$terminal" -eq "$total" ]; then echo "VERDICT: complete (all $total tasks terminal)"; exit 0; fi
if [ -n "$start" ] && [ "$el" -ge 10800 ]; then echo "VERDICT: complete (3 hours elapsed)"; exit 0; fi
echo "VERDICT: keep going"; exit 1
