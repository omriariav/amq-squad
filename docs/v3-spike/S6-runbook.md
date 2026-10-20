# S6 load test runbook (Claude-only, 21 tasks)

For the operator agent. Start at or after 05:05 (local, Asia/Jerusalem). This file is the whole procedure; do not improvise.
Everything is already prepared and verified by the preparation agent; you start it, watch it, stop it, and write the report.

## What is set up (already done, do not redo)

- Two repositories, one squad each, session names `s6-noc` (`/Users/omri/code/amq-noc`, 11 tasks) and `s6-wcli`
  (`/Users/omri/code/workspace-cli`, 10 tasks). 21 tasks in total. Held back for the later Codex run: N11, N12, W10, W11.
- Each squad: lead (`lead`, planner, does not implement) plus two implementers (`fullstack`, `backend-dev`), all Claude,
  `claude-sonnet-5-5`, `--effort medium`, v2.31 default trust `approve-for-me` (Claude `--permission-mode auto`). The v2.31 role catalog
  did not force another model: `lead`, `fullstack` and `backend-dev` are free-form roles with `--binary` and `--model` set explicitly.
  Roster cwd: lead in the repo root, `fullstack` in `<repo>/.s6-wt/impl-a`, `backend-dev` in `<repo>/.s6-wt/impl-b`
  (two detached git worktrees per repo, so the two implementers never share a Git index; no branches were created).
- Tasks are in each repo's native task store (`<repo>/.amq-squad/tasks/<session>/t*.json`, titles `N5: ...`), all `pending`.
  Source: `spike/loadtest/tasks/tasks.tsv`, rendered briefs in `spike/loadtest/tasks/<ID>.md`.
- Brief: `<repo>/.amq-squad/briefs/<session>.md` (rendered from `spike/loadtest/briefs/`), team rules appended with the S6 rules.
- Local-only files (all ignored via `.git/info/exclude` or the repo `.gitignore`, none tracked, none to be committed):
  `.amq-squad/`, `.agent-mail/`, `.amqrc`, `.amq/`, `.s6-wt/` in both repos. No tracked file in either repo was changed
  (workspace-cli's tracked `CLAUDE.md`/`AGENTS.md` were restored after `init` wrote pointer stubs; doctor therefore warns about
  missing managed blocks there, which is expected).
- Record of the setup: `~/.amq-squad/spike/s6/setup/` (doctor and plan output for both repos).

## Hard rules for you

1. Never answer a trust prompt, a permission prompt, or any prompt in a seat pane. Never type into a seat pane. If a folder-trust
   prompt appears anywhere: capture it (`pane-check.sh`), stop everything (section 6), write it in the report, and tell the owner.
2. Never merge, never push, never create or edit PRs or issues. You only read GitHub (`gh pr list`, `gh pr view`).
3. Do not write `~/.codex/config.toml`, `~/.claude/settings.json`, or anything under `~/.claude`, `~/.codex`, `~/.amq-squad`
   (except the run folder `~/.amq-squad/spike/s6/<run-id>/`, which the scripts own).
4. Do not run any other agent, build or test load on this machine during the run. Do not start the amq-squad binary from the working
   tree or install anything; use `~/go/bin/amq-squad` (v2.31.0) only.
5. Do not edit the briefs, rules, rosters or task stores once the run has started.
6. If a command below fails in a way this file does not cover, stop and report; do not work around it.

Every command block starts with the environment (shell state does not persist between tool calls):

```sh
. /Users/omri/code/amq-squad/.claude/worktrees/agent-ab721e7ddbc5ca3be/spike/loadtest/env.sh
cd "$WT"
```

This defines `$L` (scripts), `$WT` (the worktree on branch `spike/s6-loadtest`), `$NOC`, `$WCLI`, `$RUN` (`s6-claude-20261021`) and `$OUT`.
All scripts take `RUN_ID REPO_PATH...` and write under `~/.amq-squad/spike/s6/$RUN/`.

## 1. Pre-flight (no seats, no launches)

```sh
. /Users/omri/code/amq-squad/.claude/worktrees/agent-ab721e7ddbc5ca3be/spike/loadtest/env.sh
cd "$WT"
date                                              # must be 05:05 or later
git branch --show-current                         # spike/s6-loadtest
tmux ls                                           # must list NO spike sessions (amq-*, s6*, s4*, s3*, s5*)
pmset -g assertions | grep -i 'asserting forever' # caffeinate -dims must be active
$L/window-note.sh show $RUN $NOC $WCLI            # window reference 00:05, fresh window 05:05
```

If caffeinate is not active, start it (`caffeinate -dims &`) and note it in the report.

Optional but recommended (about one minute, uses throwaway tmux sessions `s6dry-*` and a throwaway task store, touches no repo;
run it BEFORE the `tmux ls` check above, or run `tmux ls` again after it):

```sh
$L/selftest-ram.sh | tail -4 && $L/selftest-limits.sh | tail -4 && $L/selftest-tasks.sh | tail -2 && $L/selftest-merge.sh | tail -2 && $L/selftest-pane.sh | tail -2
```

Then the gate for the whole run:

```sh
$L/preflight.sh pre $RUN $NOC $WCLI
```

It must end with `preflight pre: ALL PASS`. It checks versions (amq-squad 2.31.0, amq 0.102.0, plugin 2.31.0), that no other
claude/codex/amq-squad/amq-wake processes run (your own session and known background helpers are listed as INFO, not counted),
no spike tmux sessions, caffeinate, both repos clean on `main == origin/main`, local files ignored, 11 + 10 tasks all pending,
briefs present, worktrees clean and detached, `amq-squad doctor` exit 0 in both, `gh` authenticated; it records baseline hashes of
`~/.claude/settings.json` and `~/.codex/config.toml`. If any line is FAIL, stop and report it; do not fix other agents' processes yourself.

Then preview the plan once per repo (zero-write) and read it:

```sh
amq-squad plan s6-noc --project $NOC
amq-squad plan s6-wcli --project $WCLI
```

Expected: `outcome: action_required`, `reason: untrusted_config_digest`, one `trust_confirmation` required action,
`roster: desired=[backend-dev fullstack lead user] present=[...]`. That trust confirmation is the AMQ launch-API digest confirmation
for this exact roster, not Claude's folder trust. If the plan shows any other required action, or a roster that differs, stop and report.

Expected doctor result (preparation agent, both repos): exit 0, no `fail`. Warnings only: `amq vs pinned launchapi module`
(amq 0.102.0 newer than the module amq-squad 2.31.0 pins), `member tool profiles` (two workers inherit the full tool surface),
and pointer-stub/marker warnings for the implementer worktrees (and, in workspace-cli, the root checkout) which are expected.

## 2. Start the samplers (baseline first)

```sh
. /Users/omri/code/amq-squad/.claude/worktrees/agent-ab721e7ddbc5ca3be/spike/loadtest/env.sh
cd "$WT"
$L/samplers.sh start $RUN $NOC $WCLI
sleep 30 && $L/samplers.sh status $RUN $NOC $WCLI    # ram.csv growing, task snapshots written
```

This starts three detached loops: RAM (every 10 s), task-store watcher (every 10 s; mandatory, the store drops the claim time when
a task completes), and the limit scanner (every 5 min). Leave them running until section 7. (This deliberately comes before the
squad start so the series has a baseline and covers the lead-only phase.)

## 3. Record the start and run the folder-trust sanity check (lead of s6-noc only)

```sh
. /Users/omri/code/amq-squad/.claude/worktrees/agent-ab721e7ddbc5ca3be/spike/loadtest/env.sh
cd "$WT"
$L/window-note.sh start $RUN $NOC $WCLI     # refuses before 05:05; writes run-start and a spike-log section
amq-squad resume s6-noc --project $NOC --role lead     # zero-write preview of the lead-only launch; note subject_digest and action_id
```

Launch only the lead, with the digest and the single decision shown by the preview (copy the values from that output):

```sh
amq-squad resume s6-noc --project $NOC --role lead --exec --target new-session \
  --apply <subject_digest> --decision <action_id>=trust_exact_subject
```

If `resume --exec` rejects `--apply`/`--decision` or refuses to launch a never-started session, use its printed usage only if it keeps the
same digest and the same single decision; otherwise stop and report (do not fall back to launching all six seats before the trust check).

Wait about 45 seconds, then look at the lead pane and record the result:

```sh
$L/pane-check.sh $RUN $NOC $WCLI
```

- Exit 0 and the pane shows the lead reading the brief: no trust prompt. Record it:
  `NOTE="Folder-trust sanity: lead of s6-noc launched, no trust prompt in its pane (pane-check exit 0)" $L/window-note.sh note $RUN $NOC $WCLI`
- Exit 2 with `TRUST` (or any prompt): do NOT answer it. Leave the capture in `$OUT/pane-check-*/`, run section 6 (stop), write
  `NOTE="Folder-trust prompt seen on <pane>, run stopped"`, and report to the owner. The run is over.

## 4. Start the rest of both squads

Order: finish s6-noc (the two implementers), then s6-wcli (all three). Preview, read, launch with the digest of the fresh preview.
The digest changes with the roster being launched, so always take it from the preview just printed.

```sh
. /Users/omri/code/amq-squad/.claude/worktrees/agent-ab721e7ddbc5ca3be/spike/loadtest/env.sh
cd "$WT"
amq-squad plan s6-noc --project $NOC       # lead live; missing=[backend-dev fullstack]
amq-squad start s6-noc --project $NOC --target new-session --apply <subject_digest> --decision <action_id>=trust_exact_subject
amq-squad plan s6-wcli --project $WCLI     # missing=[backend-dev fullstack lead]
amq-squad start s6-wcli --project $WCLI --target new-session --apply <subject_digest> --decision <action_id>=trust_exact_subject
```

(`start` with no `--yes` and no matching `--apply` only previews and asks; answering No changes nothing. Do not use `--yes`.)
Seats come up in tmux; the panes' cwd is what the samplers match on, so the tmux session names do not matter.

Right after the last launch, wait 60 seconds and check every pane (implementer worktrees are one level below the trusted repo roots,
this is the part of the folder-trust assumption that only a live run can confirm):

```sh
sleep 60
$L/pane-check.sh $RUN $NOC $WCLI
tmux ls
```

Exit 0 and six panes (`pane-check: 6 seat pane(s), 0 flagged`): continue. Any `TRUST` or `PROMPT`: stop (section 6) and report; never answer.
Record the count: `NOTE="All six seats up, 0 prompts" $L/window-note.sh note $RUN $NOC $WCLI`.

Each lead starts dispatching on its own (the brief tells it to). Do not message the seats and do not nudge them. If after 10 minutes a
lead has dispatched nothing (`amq-squad task list --project <repo> --session <session>` still all `pending`), look at the pane, record it,
and report it to the owner; it is a finding about the launch-time path (no goal message is sent), not something to fix by typing.

## 5. Watch progress (hands off)

Poll at most every 5 minutes. Nothing here changes any state.

```sh
. /Users/omri/code/amq-squad/.claude/worktrees/agent-ab721e7ddbc5ca3be/spike/loadtest/env.sh
cd "$WT"
$L/progress.sh $RUN $NOC $WCLI                 # elapsed, task counts, loadtest PRs, sampler state, VERDICT; exit 0 = complete
amq-squad status --project $NOC --session s6-noc
amq-squad status --project $WCLI --session s6-wcli
$L/task-times.sh report $RUN $NOC $WCLI        # per-task table so far
gh pr list --repo omriariav/amq-noc --state all --limit 100 --json number,headRefName,isDraft,state,url --jq '.[] | select(.headRefName|startswith("loadtest/"))'
gh pr list --repo omriariav/workspace-cli --state all --limit 100 --json number,headRefName,isDraft,state,url --jq '.[] | select(.headRefName|startswith("loadtest/"))'
$L/pane-check.sh $RUN $NOC $WCLI               # every 15 min: prompts, limit messages; exit 2 = something to record
```

What to record as you go (in your own notes, then the report): any pane flagged by `pane-check.sh`, any seat idle for more than
15 minutes with its task still `in_progress`, the first usage-limit or 429 message (the limit scanner already timestamps it; see
`$OUT/limit-summary.txt`), memory trouble (`mem_free_pct` under 10, swap growing quickly: `./sample-ram.sh --summary`). A usage limit
is a RESULT, not a failure: note the time, keep watching; if all seats stop making progress because of it, that is the end of the run.
If the machine becomes unresponsive from memory pressure, that is also a result: stop (section 6) and report with the RAM series.

Checks you must make on the first PR from each repo (read-only, `gh pr view <n> --repo ... --json title,isDraft,baseRefName,headRefName,files,body`):
branch is `loadtest/<ID>`, draft, base `main`, only the task's files, the body contains the verify result. If a seat breaks a rule
(touches the Makefile in N3, stages `CLAUDE.md`/`AGENTS.md`, pushes anything but `loadtest/<ID>`, merges), stop, record, and report.

### When to call the run complete

Any one of:

1. All 21 tasks are terminal: `progress.sh` prints `VERDICT: complete (all 21 tasks terminal)` and every `completed` task has a PR
   (`with_pr` + failures = 21 in `task-times.sh report`). W1 may legitimately be `failed` with a reason.
2. 3 hours elapsed since the recorded run start (`progress.sh` says so; with a 05:05 start that is 08:05).
3. A usage limit (or an unrecoverable error) stopped every seat, and nothing has moved for 15 minutes.
4. The owner says stop.

## 6. Stop sequence (in this order)

```sh
. /Users/omri/code/amq-squad/.claude/worktrees/agent-ab721e7ddbc5ca3be/spike/loadtest/env.sh
cd "$WT"
$L/pane-check.sh $RUN $NOC $WCLI || true             # final screens saved under $OUT/pane-check-*
$L/scan-limits.sh $RUN $NOC $WCLI                    # last scan WHILE panes are still alive (pane-capture fallback)
REASON="<why the run ended: 21/21 terminal | 3h elapsed | usage limit | trust prompt | owner stop>" \
  $L/window-note.sh end $RUN $NOC $WCLI              # records run-end and appends to docs/v3-spike/spike-log.md
amq-squad down --project $NOC --session s6-noc --all --close-panes
amq-squad down --project $WCLI --session s6-wcli --all --close-panes
sleep 20
tmux ls; ps -axo pid=,command= | grep -E '/claude/versions/|\.local/bin/claude' | grep -v -E 'grep|stream-json|agent-id' || echo "no seat processes left"
```

If a seat survives `down`, `amq-squad down ... --all --force`, then report it. Then:

```sh
$L/samplers.sh stop $RUN $NOC $WCLI                  # last RAM and task-store samples, loops exit
$L/preflight.sh post $RUN $NOC $WCLI                 # config files unchanged? no tracked changes in the repos?
```

`preflight post` must show `unchanged` for `~/.claude/settings.json` and `~/.codex/config.toml` and no tracked modifications in either root
checkout. A `CHANGED` line is a finding for the owner: report it first, with the hash lines.

Do NOT remove the `.s6-wt` worktrees, the stores, the briefs, or any `loadtest/*` branch or PR; the owner decides in the morning.
Do not stop caffeinate.

## 7. Analysis

```sh
. /Users/omri/code/amq-squad/.claude/worktrees/agent-ab721e7ddbc5ca3be/spike/loadtest/env.sh
cd "$WT"
$L/analysis.sh $RUN $NOC $WCLI      # prints and saves five-numbers.txt and per-task-table.md; includes the PR dry-run merges (read-only)
cat $OUT/five-numbers.txt
cat $OUT/per-task-table.md
cat $OUT/pane-check.log
```

Individual pieces if you need them again: `$L/sample-ram.sh --summary $RUN $NOC $WCLI`, `$L/scan-limits.sh ...`,
`$L/task-times.sh report ...`, `$L/mergeability.sh ...`.

What the numbers mean and where they come from:

- Peak RAM: `ram.csv` column `seat_rss_mb` (sum of resident memory over the process trees of every seat pane, found by tmux pane pid;
  RSS double counts shared pages, so it is an upper bound) next to the `sys_*` / `mem_free_pct` / `swap_used_mb` columns at the same time.
- 429 and usage-limit events: `limit-events.csv` / `limit-summary.txt`, from Claude session JSONL under `~/.claude/projects` (assistant
  API-error records with status 429; "You've hit your ... limit" text counted as `usage_limit`; retry records as `retry_429`), plus the
  tmux pane capture fallback `limit-events-panes.txt`. The Codex column stays "pending".
- Wall time per task: `tasks.csv` (first claim to done/failed, from the task store snapshots plus lease timestamps; `claim_to_pr_sec` from the
  PR created time). Total: first claim to last terminal time.
- Merge conflicts: `mergeability.csv`: GitHub `mergeable`/`mergeStateStatus` per PR, solo dry-run merge onto `main`, cumulative dry-run
  (all open PRs in number order; each conflicting PR is recorded and skipped), and `go build ./...` on the cumulative result.
  Expected: W3 vs W7 (both `cmd/sheets_test.go`) and W5 vs W12 (only if W5 chose `internal/client/factory.go`).
- Time to first usage limit: minutes from the recorded run start to the first `usage_limit` event ("none" if none).

## 8. Write the report

The skeleton is `docs/v3-spike/S6-report.md` (title: "Claude-only, 21 tasks"): the five numbers first, per provider (Codex column
"pending"), then the per-task table, then observations. Replace every `TBD`; leave Codex as `pending`; paste `per-task-table.md` over the
table; copy raw data:

```sh
mkdir -p $WT/docs/v3-spike/S6-data
cp $OUT/ram.csv $OUT/ram-seats.csv $OUT/limit-events.csv $OUT/limit-summary.txt $OUT/tasks.csv $OUT/tasks-report.txt \
   $OUT/mergeability.csv $OUT/mergeability.txt $OUT/five-numbers.txt $OUT/per-task-table.md $OUT/pane-check.log $WT/docs/v3-spike/S6-data/
cd "$WT" && git add docs/v3-spike/S6-report.md docs/v3-spike/S6-data docs/v3-spike/spike-log.md && git commit -m "S6: Claude-only load test results"
```

Commit on the branch `spike/s6-loadtest` only; do not push; do not touch `v3` or `main`. Report to the lead: the five numbers, anything
unexpected (prompts, rule breaks, stalled seats, config changes), the path of the report, and the run folder.

## Known unknowns (watch for these; they are findings, not reasons to improvise)

- `start --apply <digest> --decision <action_id>=trust_exact_subject` and `resume --exec --role lead --apply ...` follow MIGRATION.md
  and the 2.31 release notes but were not executed during preparation (nothing may be launched tonight). Use the printed usage if the flags differ.
- Where AMQ stores the exact-subject trust decision was not verified. `preflight.sh post` guards the two owner config files you must not change.
- Claude Code trust for the implementer worktrees (`<repo>/.s6-wt/impl-*`): both repo roots are marked trusted in `~/.claude.json`,
  subdirectories are expected to inherit it; confirmed only by the live `pane-check.sh` in section 4.
- Seats run in Claude `--permission-mode auto` with `Bash(gh pr create:*)` pre-allowed for implementers; whether the classifier lets
  `git push -u origin loadtest/<ID>` through unattended is unverified. A seat stuck on that is a finding.
- The lead's launch input is "read your brief"; whether it starts dispatching without a goal message is unverified (see section 4 for the 10 minute rule).
