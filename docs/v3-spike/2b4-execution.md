# amq-squad 3.0, brief 2b-4: routing table and outcome log

Branch `v3-2b4`, cut from `v3` at 1b920331 (2b-1 task store, scheduler and status contract; 2b-2 milestone A; 2b-3 registry; 2b-5 squad.yaml and atomicfile; 3-1 grants and gate store; 4-2 hooks plugin; the three 2a hook verbs). Brief: docs/v3-phase2-briefs.md "2b-4", with its three rulings (size bands by declared file count 1, 2 to 3, more than 3; cost cap from both the registry `cost_index` for chain selection and the grant's per-day budget as the stop with `wait_reason: cost_cap`; the outcome log under the AMQ root per session beside the hook ledger) and lead rulings 14 and 15 (defaults are measured proposals; the scheduler process is 2b-2 milestone B's, which uses `scheduler.DefaultRouter` until this Router lands, so `routing.Router` implements `scheduler.Router` exactly). Acceptance 8 moved to 4-3. Plan revision 2.6 section 6, 16.8; R19 to R28, R43.

Standard gate on every commit: `go build ./... && go vet ./... && go test ./... && scripts/manifest-check.sh --final`, plus `GOOS=linux go build ./...`, `GOOS=windows go build ./...` and `go test -race` on internal/routing, internal/task, internal/scheduler and internal/status. The gate script and the revert harness are in `docs/v3-spike/2b4-evidence/tools/` (`gate.sh`, `revert.py`, `casesN.py`); every gate run's full `go test -v` output is `docs/v3-spike/2b4-evidence/commitN-gate.txt`, and every revert run's output (the rule undone, the row run and failing on its assertion, the source restored and the row passing) is `commitN-revert.txt`. A build failure under a revert does not count as a failing row. Each gate run records the host check (no `seat-exec` or amq-squad lead, team, scheduler or launch process) in its header.

## Commit 1: the routing table and routing.yaml

Built:
- `internal/routing/table.go` (new): the closed class list of plan 6.1 with its verifiers (extract schema, summarize none, script exit code, implement tests, plan none, review none) and tiers (smol, smol, smol, task, plan, advisor); the four tiers and the escalation ladder (smol, task, plan; advisor is review's and has no tier above); the default chains of 6.2, version 1, per tier, with an optional `max_cost_index` per tier (0: no cap); `exploration_share` default 0.05; `Validate` (every class has a row, its verifier is the plan's, a class runs below its plan tier only with a mechanical verifier, so plan and review never route down, only review runs on advisor, every tier has a non-empty chain without repeats); `SizeBand` (1, 2-3, 4+); `Family` (the vendor of a pool: claude anthropic, codex openai, pi the provider prefix of its model).
- The class table's `required_companions` field (coordinator request, 3-2 dry run reviewer ruling): per class, a list of declared_files globs that a task of the class needs beside its own files, the documentation targets a repository's CI requires (workspace-cli's skills test fails a CLI command PR whose task omitted SKILL.md). Shape: `classes.<class>.required_companions: [globs]` in routing.yaml, cleaned with the declared_files rules; default empty for every class. `Table.Companions(class, files)` returns what the lead's widening path adds by default. 3-2's per-repository grant carries the repository-specific list; that side is 3-2's.
- `internal/routing/file.go` (new): `Load(home, repoRoot)`: the default, then `~/.amq-squad/routing.yaml`, then `<repo root>/.amq-squad/routing.yaml`, each read through `internal/yamlsub` in strict mode, so anything outside the amendment 27 subset (anchors, aliases, tags, flow maps, a second document, a repeated key, a malformed line) and every unknown key refuses the file; a missing file is skipped. Keys: `schema` (required, 1), `exploration_share` (0 to 1), `classes.<class>.{tier, required_companions}` (`verifier` is refused: fixed by the plan), `tiers.<tier>.{chain, max_cost_index}` (chain entries are `runtime:model:effort`, the `task --pool` form). Repository file only: `privacy_paths` (list of `{glob, class}`, class local or vendor), `sensitive_paths`, `no_tests`. A class row replaces the fields it names; a chain is replaced whole; privacy and sensitive paths accumulate. `RequiredPrivacy(files)`: local when a declared file may touch a local glob (a literal path is matched exactly, `**` spanning segments; a declared glob or tree uses the file-lock overlap, which is conservative: a false match keeps the task local). `Sensitive(files)` the same for sensitive paths.

Tests: `internal/routing/file_test.go`: the default validates, has 6 classes and 13 chain entries, no max effort, the probation entry last; required companions default empty, a repository list gives `SKILL.md` for an implement task that lacks it (the test row the coordinator asked for); user then repository overlay, privacy and sensitive matching; 25 refusal rows (anchor, alias, tag, flow map, second document, repeated key, unknown key, malformed line, missing schema, schema 2, share above 1, unknown class, unknown tier, plan routed down, review routed down, implement on advisor, verifier changed, bad chain entry, empty chain, repeated chain entry, negative cost cap, privacy in the user file, unknown privacy class, privacy glob escaping the repository, incomplete privacy rule); the allowed tier moves (summarize up, implement down, back to the plan tier).

Revert checks (`commit1-revert.txt`, 22 rules, all fail reverted and pass restored): strict subset read, unknown key, schema required, schema 1 only, share range, closed class list (two places), known tiers (two places), plan never down, review never down, advisor review only, verifier fixed, chain entry form, chain not empty, chain entry unique, cost cap not negative, repository-only keys, privacy class closed, privacy glob relative, privacy rule complete, local path rule matches, companions from the class row, no max effort in the default. The YAML-feature rows (anchor, alias, tag, flow map, second document, repeated key) are yamlsub's own rules with their own tests and reverts (2b-5); this package's rule is the strict read, reverted above.

Rows: two new rows (`internal/routing/table.go`, `internal/routing/file.go`), fate new.

Behavior changes: none yet (no caller).

Deviations from the plan text in the default table (open ruling 1 below):
- Codex ids: the plan names gpt-5.4-nano, gpt-5.4 and gpt-5.5. The registry writes the gpt-5.6 family the pinned codex-cli lists (S4: 0.147.0 lists gpt-5.6-sol, -terra, -luna; gpt-5.5 is refused on it, the 2b-3 fixture `s4d`). The default uses gpt-5.6-luna low (smol), gpt-5.6-terra medium (task), gpt-5.6-sol high (plan) and xhigh (advisor). gpt-5.6-luna is not in the agent catalog, so the registry has no entry for it yet and the smol Codex entry is skipped as `not_in_registry` until it does.
- pi + Anthropic: the plan's "pi + anthropic haiku 5.5" and "sonnet 5.5" are `pi:anthropic/haiku:low` and `pi:anthropic/sonnet:medium`, placeholders until the pi package (4-1) registers its model ids; until then they are skipped as `not_in_registry` (2b-3 wrote no pi + Anthropic entries, its deviation).
- Grok Build as a third advisor family waits for its probe (plan: "after a probe").

Lead ruling on the ids (2026-10-24, after commit 1): the default table's entries are resolved against the registry's entries at selection time, so an id the registry has no verified entry for is skipped like any failed check, with the skip reason naming it (`not_in_registry`, `unverified`); the ids in the binary are placeholders the registry overrides. gpt-5.6 for Codex is fine as the placeholder: the pinned codex-cli 0.147.0 refuses gpt-5.5 (S4 closure pane `s4d`, classified refused by 2b-3's `CheckCodexModel`). The pi + Anthropic placeholders stand until 4-1 lands on v3; at the rebase the model ids come from 4-1's registry rows and the lead model row, with no hardcoded default of this brief's own.

## Commit 2: chain selection, escalation, the reviewer family (halfway checkpoint)

Built:
- `internal/routing/router.go` (new): `Router`, which implements `scheduler.Router` exactly (`Route(task.Task) scheduler.Route`), so 2b-2 milestone B swaps it in for `DefaultRouter` at its rebase. `New(Options{Table, Registry, Grants, Root, Tasks, Seed, InputTokens, Usage, Now, MaxAttempts})`; `Tasks` is the lock-free `task.Snapshot(store.Dir)` (the scheduler calls Route inside its tick, so the router must not take the store lock).
- Per pending task: the table tier of its class; implement escalates to plan with more than 3 declared files, a sensitive path or `no_tests` (6.1). The first attempt's policy is drawn once per task (below); a retry moves one tier up the ladder smol, task, plan per failed attempt (failed, expired, commit_refused, blocked; unclaimed and released attempts are seat or lead events and retry on the same tier), and on plan or advisor, which have no tier above, takes the next entry. The tier is a function of the task and its attempts, so it is the same on every tick and after a restart.
- Per chain entry, in order, the first that passes: never an entry a failed attempt ran on (`used_before`); input above 32k tokens skips Ollama (`input_over_32k`), above 100k skips Haiku (`input_over_100k`); a review skips entries of the implementer's family (`reviewer_family`; the family of the implement task the review depends on, from its last attempt's pool); then the four checks: privacy class first (a vendor entry for a task whose declared files touch a local glob, or under a local grant, is `privacy_class`, so a local-only task's vendor skips always say so), the grant's permitted runtimes (`runtime_not_permitted`), verified (`not_in_registry`, `unverified`; a probation entry is skipped on the first attempt, `probation`), the concurrency cap of the entry's concurrency pool counted over leased tasks (`concurrency_cap`), the cost cap: the tier's `max_cost_index` against the registry `cost_index`, and the grant's `cost_per_day_usd` against the known spend of the UTC day from the outcome log, for every entry whose cost tier is not free (`cost_cap`, briefs ruling 2).
- No passing entry: the task waits, and the wait reason names the closest cause: `concurrency_cap`, then `cost_cap`, then `privacy_class`, then `reviewer_family`, else `no_route`; the route detail lists every skipped entry with its reason. Every entry of the tier used by failed attempts, or `MaxAttempts` (3, as DefaultRouter) failures: blocked with `retries_exhausted` for the lead.
- A retry's skipped list starts from the previous attempt's (recomputed: the decision is a function of the task and its attempts) and adds the failed entries, so it grows with every attempt.
- A lead pin (`task add --pool`, first attempt only) is honored and still checked; a pin below the table tier of a class that may not route down (plan, review, summarize) is ignored and the table routes.
- `internal/task/class.go`: the routing wait reasons `privacy_class`, `concurrency_cap`, `cost_cap`, `reviewer_family` join the vocabulary (status already shows any wait reason as the task row's `reason_code` and raises a low attention item for it).
- `internal/routing/outcome.go` (new): the outcome row type and the append-only writer (used by the router from this commit; its tests are commit 3's).

Tests: `router_test.go`, `scheduler_test.go`:
- Acceptance 1 (`TestAcceptance1ClassTable`, `TestAcceptance1ReviewerFamilyDiffers`, `TestImplementEscalatesToPlan`): with every draw an exploration, extract, script and implement route one tier down when there is a tier below (script with its tier set to task routes to smol), summarize only explores up (never below smol, never below task when set there), plan and review are never explored; with exploration off every class runs on its tier. A review of a Claude implementation takes `codex:gpt-5.6-sol:xhigh`, of a Codex one `claude:opus:xhigh`; with only Anthropic entries left the review waits with `reviewer_family`. More than 3 files, a sensitive path, `no_tests` escalate implement to plan, and an escalated implement is not explored down.
- Acceptance 2 (`TestAcceptance2LocalOnlyPathNeverGoesToAVendor`): an implement task touching `internal/secrets/` with no local entry on the task tier waits with `privacy_class`, and every vendor entry is skipped as `privacy_class`; a script task on the same path takes the verified local entry; with that entry at its cap it waits with `concurrency_cap` and still takes no vendor; a local grant makes a plan task wait with `privacy_class`.
- Acceptance 3 (`TestAcceptance3Escalation`): 10k tokens takes local qwen3.6:27b; 40k skips it (`input_over_32k`) and takes claude haiku; 120k skips both (`input_over_100k`) and takes Codex; a failed smol attempt retries on task with the failed entry in the skipped list and the list longer, a second failure goes to plan and the list grows again; a failed plan attempt takes the next plan entry, and two failures there block with `retries_exhausted`; an unclaimed attempt retries the same entry on the same tier.
- Also: the four checks one by one (`TestFourChecks`), the grant's daily budget (`TestGrantDailyBudgetStops`: over budget a plan task waits with `cost_cap` while a free local entry still runs; yesterday's spend does not count), probation never first, lead pins, retries exhausted, the tier fixed across ticks, and `TestRouterInsideTheScheduler`: a real `scheduler.New` with this Router requests a seat for `claude:sonnet:medium` for a plain task and leaves a local-only task pending with `wait_reason: privacy_class` in the store.

Revert checks (`commit2-revert.txt`, 30 rules, all fail reverted and pass restored): explore down only with a mechanical verifier; plan and review never explored (held three ways: the explicit guard, no mechanical verifier, no tier above plan or advisor; the revert removes the guard and gives plan a tier above); reviewer family; implementer family from the dependency; the three implement escalations; escalated implement not explored down; local path never to a vendor; local grant; privacy names the wait; 32k; 100k; one tier up; never repeats an entry; skip list grows; unclaimed is not a failure; chain used up blocks; max attempts; verified; registry entry required; permitted runtimes; concurrency cap; cost_index cap; per-day budget; budget per UTC day; probation; pin never routes plan down; pin still checked; the router's answer reaches the scheduler.

Measured (`commit2-bench.txt`): one `Route` call against a store of 100 tasks (snapshot read, flush scan, one decision) takes 1.29, 1.72, 1.77 ms on this Mac (Apple M5 Max). The scheduler calls Route once per pending task per tick, so 100 pending tasks cost about 0.17 s a tick; a per-tick snapshot shared across calls is the fix if milestone B's tick interval needs it.

Rows: two new rows (`router.go`, `outcome.go`); `internal/task/class.go` current_lines refreshed (93).

Behavior changes: none on a running path until milestone B wires the Router into the scheduler process.

## Commit 3: exploration and the outcome log

Built:
- Exploration (6.4, R26): `exploration_share` (default 0.05, 0 disables) is the share of tasks that run one tier away from the table, never plan or review: down when the class's verifier is mechanical and no implement escalation fired, else up; a class with no tier on either side is not explored. The draw is `Draw(seed, task id)`, a uniform [0, 1) from SHA-256, so it is drawn once per task, the same on every tick and after a restart, and reproducible under a seed (the router's seed defaults to the session root). Every decision and row records the policy (`table`, `explore`, `lead_pin`; `jev_shadow` is reserved for phase 5), the propensity (share for explore, 1 minus share for table, 1 when the class is not eligible or exploration is off) and the share in force.
- The outcome log (R27): `<AM_ROOT>/extensions/amq-squad/routing/outcomes.jsonl`, beside the hook ledger (briefs ruling 3), inside the guard's implicit protected path, mode 0600 in a 0700 directory. `AppendJSONL` is the 2a-3 ledger's writer: an exclusive flock on the file itself across a torn-tail check and the write, then fsync before the lock is released; a torn last line is terminated so the new line stays whole, and readers skip it. One row per attempt with every 6.4 field: task, attempt, class, features (declared file count, size band, input tokens, sensitive, no_tests, privacy and the matching glob, implementer family), table tier, used tier, entry, skipped entries with reasons, policy, propensity, exploration share, tokens in and out, cost in USD, wall time, wait time and the wait reasons seen before the dispatch, outcome, verifier result (`pass`, `fail`, `not_run`, `none` for a class with no verifier), review findings (a completed review's count from its evidence, `findings=N` or `findings: N`), consumed smol outputs (the dependencies whose completed attempt ran on smol), plus holder and the attempt times. Tokens and cost are null when unknown.
- `Router.Flush` writes the row of every ended attempt that has none; Route calls it on every call and the scheduler process calls it once per tick (milestone B), so the last attempt of a task that never routes again is logged. The router reads the log back at start (no attempt is logged twice after a restart, and the day's known spend feeds the budget stop) and keeps each attempt's decision in `routing/decisions.jsonl` (same writer) until the attempt ends, so a restart keeps the policy, propensity and skips. An attempt dispatched outside the router (a lead `task claim` or `dispatch`) gets a row with policy `lead_pin`.
- `routing.LedgerUsage(root, prices)`: an attempt's tokens are the sum of the token counts in the holder's hook ledger lines inside the attempt's window (the status contract's reader, now exported as `status.PayloadTokens`); cost is tokens times the pool's price in USD per million tokens, null without a price.

Tests: `outcome_test.go`, `outcome_lock_unix_test.go`:
- Acceptance 4 (`TestAcceptance4Exploration`): 15,000 seeded tasks, classes in rotation, one attempt each, through Route and Flush: of the 10,000 eligible draws 484 explored, a share of 0.0484 (within 0.01 of 0.05); the 5,000 plan and review rows are all policy table, propensity 1, on their table tier; every row records its policy, its propensity (0.05 or 0.95) and the share 0.05; with share 0, 2,000 draws give no exploration. `TestDrawIsUniform`: 100,000 draws fall within 10 percent of even across ten buckets.
- Acceptance 5 (`TestAcceptance5EveryAttemptHasARow`): a seeded run on the real store and the real scheduler with this Router: six tasks (one per class, the review depending on the implement task), seats joining on request, claims and done reports, one failed script attempt, ledger lines with usage. 7 attempts, 7 rows, each with every 6.4 key, none twice; the failed attempt's row says `failed`/`fail` and its retry runs on task with the failed entry in its skipped list; the review row has 2 findings, implementer family anthropic and a Codex entry; the implement row costs 0.006 USD (1000 in at 3, 200 out at 15 per million) with positive wait and wall times; the summarize row has a null cost and verifier `none`; a restarted router writes no row again. `TestConsumedSmolOutputs`, `TestDecisionSurvivesRestart`, `TestOutcomeTornTail`.
- Acceptance 5 concurrency (`TestAcceptance5ConcurrentAppendKeepsLinesWhole`), as in 2a-3: eight writer processes each append one row of more than 512 KiB; the log has eight whole rows, none unparseable, mode 0600. Measured: with the flock removed this test still passes on this Mac, because APFS keeps one O_APPEND write of 512 KiB whole; the lock is therefore shown by `TestAppendWaitsForTheLock` (a held flock on the log keeps the writer waiting at least 300 ms; reverted, it writes at once). The lock also covers the torn-tail check, which the single write does not.

Revert checks (`commit3-revert.txt`, 19 rules, all fail reverted and pass restored): explore below the share; explore propensity; table propensity; the share in force on the row; the draw uniform; every ended attempt gets a row; a restart never logs twice; a decision survives a restart; the flock; the torn tail; the 0600 mode; verifier fail; no verifier means `none`; review findings; consumed smol outputs; ledger tokens only in the attempt window; cost from the price; wait time; wall time. The fsync has no test that can see it (durability across a power cut); it is the same call as the 2a-3 ledger's.

Rows: one new row (`internal/routing/usage.go`); current_lines refreshed for router.go, outcome.go and `internal/status/collect.go` (the exported `PayloadTokens` wrapper, 5 lines).

Behavior changes: none on a running path until milestone B wires the Router; `status.PayloadTokens` is new API, the status document is unchanged.

Lead rulings on commit 3's open points (2026-10-24):
- (a) Price source: an optional price pair per entry (input and output, USD per million tokens) that the owner sets in `~/.amq-squad/routing.yaml`, never fetched. An entry with no price: the per-day budget stop does not apply to it, its rows carry `cost_usd: null` with `cost_index` still filled, the weekly report names it, and doctor warns when the grant sets `cost_per_day_usd` and an entry in use has no price. Done in commit 4.
- (b) Review findings as `findings=N` from the evidence: accepted as the 3.0 form. The report traces a finding to a smol output when the review's row names that smol attempt's id. Done in commit 4.

## Commit 4: the weekly report, prices, the status wait reason

Built:
- Prices (ruling a): `prices:` in the user routing.yaml only (the repository file is refused: it is not the owner's guarded file), a list of `{entry, input_usd_per_mtok, output_usd_per_mtok}`, all three required, non-negative, an entry at most once. The ruling says "the registry entry carries" the pair; it is kept in the routing table and joined to the registry entry by its runtime/model/effort key at selection and at row time, so 2b-3's registry file format is unchanged (deviation, recorded for the lead). The per-day budget stop applies only to a priced entry. The row carries `cost_index` from the registry entry. Under a session root the router's default usage reader is `LedgerUsage(root, table.Prices)`. Evidence completeness for 16.8 is the token counts: a null cost with known tokens is a missing price, named per entry, not missing evidence.
- Consumed smol outputs (ruling b) are named by attempt id, `t1/1`, and the report counts a review's findings once for each bucket whose smol attempts it consumed.
- `internal/routing/report.go` (new): `Weekly(rows, now, table)`: the rows that ended in the seven days before now; runtimes with any row lacking token evidence are left out and listed with their counts; entries with rows but no price are listed. Buckets are class x used tier x size band (1, 2-3, 4+, briefs ruling 1). Per bucket: attempts, tasks, tasks redone at a higher tier (a failed attempt with a later attempt on a higher tier) and the rate, the mean cost of a completed task that started in the bucket with its redo included (unknown when any attempt is unpriced), the same one tier up for the same class and band and whether the bucket is dominated (its cost with redo at or above one tier up), mean and max wait with the reasons (wait reasons and skip reasons) behind it, and, for smol, the review findings traced to its outputs. One proposal per bucket, in priority order: too few tasks (under 3) means no change; raise one tier on a redo rate of 0.3 or more; raise on dominated cost; raise off smol on traced review findings; change the tier's chain on a mean wait above 10 minutes, naming the main reason; try one tier down after 5 or more clean tasks of a class that may route down; else no change. The thresholds are proposals (lead ruling 14). `Print` writes the report and ends with "proposals are for the owner; none is applied".
- `amq-squad routing report --week [--now T] [--repo DIR] [--json]` (new verb, `internal/cli/routing.go`): reads the session's outcome log (store flags: `--root`, `AM_ROOT`, or `--session`), loads the table (binary, user file, `<repo>/.amq-squad/routing.yaml`; a file outside the subset stops the report), prints or emits the `routing_report` JSON envelope. It writes nothing.
- doctor: a `routing` row (`routingCheck`): the effective table's sources and size; fail with kind `routing_invalid` when a routing.yaml does not parse or breaks an invariant; warn when the grant sets `cost_per_day_usd` and a verified vendor registry entry in the chains has no price, naming each (ruling a).
- docs/task-store-design.md: the wait_reason row lists the routing reasons.

Tests:
- Acceptance 6 (`TestAcceptance6WeeklyReport`, `TestRoutingReportWeekOnTheFixtureLog`): the fixture log `internal/routing/testdata/outcomes-week.jsonl` (29 rows, generated by `fixtureWeek` and pinned by `TestFixtureLogIsCurrent`): 28 rows in the window (one older row ignored), the codex runtime excluded (3 of 3 rows without token evidence), `pi:ollama/qwen3.6:27b:low` named as unpriced, 8 buckets with 8 proposals, each the one the bucket was built for (raise extract smol band 1 on a 2-of-4 redo; raise script smol band 2-3 on dominated cost 0.0100 against 0.0050 USD; raise summarize smol band 1 on 4 traced review findings; change the task chain for implement band 2-3 on a 20-minute mean wait on `concurrency_cap`; try implement band 1 on smol after 5 clean tasks; no change for the three thin or quiet buckets), and no plan bucket (its only runtime is excluded). Through the verb: the same 8 proposals, the JSON envelope, the log unchanged after the run, `--week` required, an unknown verb refused, and a repository file with an anchor stops the report. `TestReportRedoAndCostRules`: a same-tier retry is no redo; an unpriced attempt makes the task's cost unknown.
- Acceptance 7 (`TestStatusNamesTheRoutingWaitReason`): a task touching `internal/secrets/` with a repository rule making it local and only vendor entries in the registry; one scheduler tick with this Router; `amq-squad status --json` shows the task row with `wait_reason: privacy_class`, `reason_code: privacy_class`, state `waiting`, pool null, and the attention item `task:t1:wait:privacy_class`.
- `TestDoctorRoutingRow`: the default row names 13 chain entries; an invalid repository file fails with `routing_invalid`; a grant with `cost_per_day_usd: 5`, two verified Claude entries and a price for one warns naming the other. Price refusal rows: in the repository file, incomplete, repeated, negative, bad entry. `TestGrantDailyBudgetStops` now prices the plan entries and shows an unpriced entry is not stopped.

Revert checks (`commit4-revert.txt`, 28 rules, all fail reverted and pass restored): the six proposal rules; runtimes with incomplete evidence excluded; evidence is the token counts; the week window; redo only at a higher tier; an unpriced attempt means unknown cost; unpriced entries named; findings once per bucket; findings traced by attempt id; the row names the consumed attempt; the row records cost_index; ledger usage by default under a root; the budget stop only for priced entries; prices in the owner file only; price complete; price not repeated; price entry form; status names the wait reason (internal/status) and the router sets the privacy wait (both against acceptance 7); the report needs `--week`; the report reads the repository file; doctor fails an invalid table; doctor warns on unpriced entries. "The report never applies a change" has no rule to revert (it writes nothing); the verb test checks the log bytes are unchanged.

Report sample: `docs/v3-spike/2b4-evidence/report-sample.txt` (the fixture log through `Report.Print`).

Rows: two new rows (`internal/routing/report.go`, `internal/cli/routing.go`); current_lines refreshed for the routing files, `internal/cli/doctor_host.go` (one line) and `internal/cli/command_registry.go` (keep, the `routing` verb row).

Behavior changes: a new verb `amq-squad routing report`; `amq-squad doctor` has a new `routing` row (ok by default; fails on an invalid routing.yaml; warns on unpriced entries under a budget); the public command catalog lists `routing`.

## End of brief

Acceptance (all rows green on the commit 4 gate, `commit4-gate.txt`):

| row | test |
| --- | --- |
| 1 per-class table, script down, summarize/plan/review never down, review of Claude picks non-Anthropic | `TestAcceptance1ClassTable`, `TestAcceptance1ReviewerFamilyDiffers`, `TestImplementEscalatesToPlan` |
| 2 local-only path never to a vendor, waits with privacy_class | `TestAcceptance2LocalOnlyPathNeverGoesToAVendor`, `TestRouterInsideTheScheduler` |
| 3 40k skips Ollama, 120k skips Haiku, retry one tier up, skipped list grows | `TestAcceptance3Escalation` |
| 4 10,000 draws at 0.05 within 0.01, never plan or review, policy and propensity on every row | `TestAcceptance4Exploration` (0.0484) |
| 5 a row per attempt with every 6.4 field; 8 concurrent writers, whole lines | `TestAcceptance5EveryAttemptHasARow`, `TestAcceptance5ConcurrentAppendKeepsLinesWhole`, `TestAppendWaitsForTheLock` |
| 6 weekly report, one proposal per bucket, incomplete-evidence runtimes excluded | `TestAcceptance6WeeklyReport`, `TestRoutingReportWeekOnTheFixtureLog` |
| 7 status --json names the wait reason | `TestStatusNamesTheRoutingWaitReason` |
| 8 | moved to 4-3 |
| the Router running inside the scheduler process | deferred to 2b-2 milestone B, not met in this brief: the scheduler still picks `DefaultRouter` when none is supplied (Codex pass 1, limit c, recorded as it stands); `routing.Open` is the start step milestone B calls, and it refuses an invalid table with `routing_invalid` (fix 1) |

Size of the default table: 6 classes, 4 tiers, 13 chain entries (smol 5, task 4, plan 2, advisor 2), exploration_share 0.05, no prices, no path rules. Code: 2,157 non-test lines (internal/routing 2,012 in 7 files, internal/cli/routing.go 145), 1,571 test lines. Revert checks over the brief: 99 rules (22, 30, 19, 28), every one fails reverted and passes restored.

Measured numbers (lead ruling 14): exploration 0.0484 over 10,000 draws at the default 0.05; Route 1.3 to 1.8 ms against a 100-task store; a 512 KiB O_APPEND write stays whole on APFS without the flock.

For milestone B's rebase: construct with `routing.New(routing.Options{Table: routing.Load(home, repo), Registry: registry.Read(registry.Path(home)), Grants: <resolved grant>, Root: <AM_ROOT>, Tasks: func() { return task.Snapshot(store.Dir) }})`, pass it as `scheduler.Options.Router`, and call `router.Flush()` once per tick. A table that does not load means no Router: the scheduler should refuse to start rather than fall back to DefaultRouter silently (proposal).

## Open rulings

1. Default chain ids: ruled (above).
2. Required companions (coordinator, from the 3-2 dry run): the field is `classes.<class>.required_companions`, a list of declared_files globs, default empty, read from routing.yaml (user and repository file). Who calls `Table.Companions` (the lead's widening path at `task add`, or 2b-2's widening) and whether the repository grant's list (3-2) and this list merge or one wins is not decided here; proposal: union, the widening path adds both.
3. Price source: ruled (commit 4, ruling a). Open part: the ruling says the registry entry carries the price; it lives in the routing table, keyed by the entry, so the registry format is unchanged. Proposal: keep it there (the owner's routing.yaml is where the ruling puts it); move it into `registry.Entry` only if 4-1 or 4-3 need it without the table.
4. Review findings: ruled (commit 4, ruling b). The reviewer role's done report must write `findings=N`; that text belongs to the role (4-1) or 3-2's review step.
5. Router startup failure (milestone B): when routing.yaml does not load, the proposal is that the scheduler refuses to start, not that it falls back to DefaultRouter without saying so.
6. Report thresholds (redo rate 0.3, 3 tasks minimum, 5 clean tasks for a tier down, a 10-minute mean wait): proposals under ruling 14, to be adjusted from the first real weeks of the log.
7. Pinned-task retries: a lead pin governs the first attempt only. Its retries follow the table from the tier the draw gives, not from the pin's tier. Proposal: accept for 3.0.

## Lead rulings at the end checkpoint (2026-10-24, d0b0b39e; to apply in the fix commit after the Codex pass)

1. Class companions and 3-2's per-repository list merge as a union (open ruling 2).
2. The scheduler refuses to start when routing.yaml does not load, with the named reason `routing_invalid`; it never falls back to the default router silently (open ruling 5). Doctor's routing row already fails on an invalid table.
3. The report thresholds stay proposals, adjusted from real weeks of the log in phase 5 (open ruling 6).
4. A lead pin fixes the task's starting tier: retries move one tier up from the pinned tier per the table and never below it, and the table never lowers the pin (open ruling 7; this replaces the commit 2 behavior, where retries of a pinned task followed the drawn tier).
5. Deviation accepted: the price pair stays in the routing table keyed by entry; the registry format is unchanged (open ruling 3).
6. Rebase item: 4-1 is merged; take its pi model ids from v3's registry rows in place of the pi + Anthropic placeholders.

Status: rulings 1, 2 and 4 landed in fix 1 (below); ruling 6 is the rebase item.

## Fix 1: Codex pass 1 on d0b0b39e, and the end-checkpoint rulings

One commit on 31d81513. Every item has a test row that fails when its rule is reverted: `docs/v3-spike/2b4-evidence/fix1-revert-check.txt` (27 rules, every one fails reverted and passes restored; cases in `tools/cases_fix1.py`); gate output `fix1-gate.txt`. Line numbers below are against the d0b0b39e export.

P1:
1. Pins with no tier (router.go:240): a pin resolves to the lowest tier the class may run on whose chain lists the pinned entry (review only advisor; the others their table tier and up, and below it only with a mechanical verifier and no implement escalation); otherwise the task blocks for the lead with `pin_unresolved`, naming why (in no chain, or only on tiers the class may not run on). Rows: `TestLeadPin` (plan pinned to haiku, plan pinned to an entry in no chain, review pinned to a plan-tier entry: all `pin_unresolved`; a review pinned to an advisor entry runs there).
2. Privacy on the declared spelling (file.go:399): path rules now match the canonical repository path (`internal/routing/canon.go`). The literal prefix of each declared path is walked from the repository root (the task's project_path, else the router's `RepoRoot`) one component at a time through directory entries, never following a link; each component takes its on-disk name, found case-folded when the filesystem folds case (probed on the root: its name with the case swapped names the same directory; a root with no letters takes the platform default, macOS and Windows fold); the match is then case-folded on a folding filesystem. A declared path that passes through a symlink may resolve anywhere, inside or outside the repository, so it is local-only by default whenever a privacy rule exists (and sensitive whenever a sensitive path exists). A component that does not exist yet keeps its spelling. Rows: `TestFix1PrivacyMatchesTheCanonicalPath` (on this Mac the temp root is case-insensitive: `alias/vault.go` through a link to `internal/secrets`, `alias/` as a tree, `out/x.go` through a link out of the repository, `Internal/Secrets/vault.go`, `INTERNAL/SECRETS/new.go`, `ALIAS/vault.go`, and `Vendor-Keys/a.pem` under a rule for a tree that does not exist yet are all local; `cmd/main.go` and `internal/secretsx/a.go` stay vendor; through the router the alias waits with `privacy_class`; sensitive paths match the same way). Deviation: the walk uses `os.ReadDir` entries (the entry type is never a followed link) rather than `internal/fsanchor`, which is on v3 but not on this base (31d81513); a component swapped for a link between the walk and the seat's work is outside what routing can see. At the rebase the walk moves onto fsanchor's anchored descriptors.
3. Dotted keys (file.go:185): child keys are matched exactly (`child(parent, entry)` keeps a dotted key whole), so `override.class: vendor` is an unknown key and the file is refused; `routing.Open` and `routing.New` return it as `routing_invalid`. Rows: six refusal rows (dotted key in a privacy rule, in a class row, in a tier, in a price; dotted class name; dotted tier name) and `TestFix1OpenRefusesAnInvalidTable`.
4. Ignored flush errors (router.go:183): a failed outcome row or decision write stops every dispatch with `wait_reason: outcome_log_unwritable` (new in internal/task) until the write succeeds; the row stays unlogged, so the next Route retries it, and the spend the budget stop reads and the record a retry reads both come from durable rows only. A decision that cannot be written is not kept. Row: `TestFix1UnwritableLogStopsDispatch` (the outcome log path is a directory: a plan task waits with `outcome_log_unwritable`; once the path is free it dispatches and the row is written; the decisions path is a directory: the next dispatch waits the same way).

P2:
5. One counter (usage.go:39, status/collect.go:277): `status.PayloadTokenCounts` reports each counter separately (`PayloadTokens` is gone; the status seat row keeps its own either-counter rule, unchanged); an attempt's usage is known only when every payload in its window that names a counter names both; one counter anywhere makes the usage unknown, which is incomplete evidence for the report. Row: `TestFix1UsageNeedsBothCounters`.
6. Non-finite numbers (file.go:179): `exploration_share`, `max_cost_index` and both prices refuse NaN and the infinities (`strconv` accepts "NaN", "Inf", "+Infinity"). Rows: four refusal rows.
7. Recomputed skips (router.go:273): a retry starts from the previous attempt's recorded skipped list (its outcome row, else its persisted decision), never a recomputation from the conditions of now. The recursive recompute is gone. Rows: `TestRetryKeepsTheRecordedSkips` (the local entry at its cap on attempt 1; the cap frees; the retry still lists `concurrency_cap` for it), and `TestAcceptance3Escalation`, which now runs its retries through Route and Flush.
8. Tier down without success (report.go:302): the clean-task threshold counts tasks with a completion that passed its mechanical verifier in the bucket (`verifier_passed`); the proposal says "N of M tasks passed their verifier, no redo". Row: `TestReportRedoAndCostRules` (five expired tasks, no redo, no pass: no tier-down proposal).

Limits:
- (a) and ruling 4: a lead pin fixes the task's starting tier. A retry reads the first attempt's record (row, else decision): its tier, policy and propensity carry over, and the tier moves one up per failed attempt from there, never below; an unclaimed pinned attempt retries the pin itself. Rows: `TestLeadPinFixesTheStartingTier` (implement pinned to opus high retries on plan with the next plan entry; pinned to sonnet medium retries on plan; pinned down to haiku low retries on task; pinned and unclaimed retries the pin).
- (b) the exploration seed: `routing.SessionSeed(root)`: 32 bytes from crypto/rand, hex, at `<AM_ROOT>/extensions/amq-squad/routing/seed` (0600), written once by link (never replaced, so two first starts keep one seed) and read back after a restart; a damaged seed file stops `New`. The router uses it whenever no explicit seed is given; the propensity is recorded as before. Row: `TestFix1SessionSeed`.
- (c) recorded in the acceptance table as deferred to milestone B, not met.

Rulings:
- 1, the companions union: `Table.Companions(class, files, repository)` returns the class's required companions and the repository's list (3-2's per-repository grant) as one union, each once, without the ones the task carries. Rows: `TestRequiredCompanionsDefaultEmptyAndRepositoryList`.
- 2, `routing_invalid`: `routing.Open(StartOptions{Home, RepoRoot, Root, Registry, Grants, Tasks, Now})` is the scheduler process's start step: a table that does not load is an `*InvalidError` (`routing.IsInvalid`, message prefixed `routing_invalid:`), and the process refuses to start on it; `routing.New` returns the same for a broken table. Doctor's row uses the same reason code. The scheduler itself still defaults to DefaultRouter when no router is passed (limit c); the refusal is the process's, which is milestone B's.
- 4: limit (a) above.
- 6, 4-1's pi ids: the rebase item, not in this commit.

Rows: three new rows (`internal/routing/canon.go`, `seed.go`, `open.go`); current_lines refreshed for the routing files, `internal/task/class.go`, `internal/status/collect.go` and `internal/cli/routing.go`.

Behavior changes: path rules see through spelling (links, case) and are stricter: a declared path through a symlink is local-only when any privacy rule exists; a pinned task outside its class's tiers blocks with `pin_unresolved` where it ran before; dispatch stops on an unwritable outcome log; exploration draws from the session seed, so the same task id draws differently in different sessions.

## Fix 2: Codex pass 2 on 2f1a46d2

One commit on 2f1a46d2. Pass 2 found P1-3, P2-5 to P2-8 and ruling 1 complete, and P1-1, P1-2, P1-4 and limit b partial, with two new P1s in the retry logic. Each item has a test row that fails when its rule is reverted: `docs/v3-spike/2b4-evidence/fix2-revert-check.txt` (15 rules, every one fails reverted and passes restored; cases in `tools/cases_fix2.py`); gate output `fix2-gate.txt`. Line numbers are against the 2f1a46d2 export.

- N1 (router.go:329), history trusted without validation: every retry re-validates the recorded starting tier against the class's own rules (its plan 6.1 minimum tier, routing down only with a mechanical verifier; review only on advisor) and blocks with `pin_unresolved` when the record disagrees. A plan task once dispatched by hand to a smol entry, or to an entry in no chain, no longer retries there as `lead_pin`. Row: `TestFix2HistoryIsCheckedNotTrusted` (both cases refused; a valid smol record of an implement task still carries over).
- N2 (router.go:314, :329), history above the current escalation: the starting tier is the higher of the recorded one and the current escalation, so history never lowers it. A task that becomes wider than three files, sensitive or test-less after its first attempt starts on plan, and a pin below that floor is dropped. Row: `TestFix2EscalationIsAFloor` (with and without a pin on sonnet medium, the retry after the widening runs on plan, `claude:opus:high`).
- P1-4 residuals (router.go:646, :613-614): before any route, `durable()` (new `internal/routing/durable.go`) runs four checks. (1) It re-verifies the outcome log against the size the router last read or wrote, and checks that the last line ends with a newline and parses as a row. (2) When the log has changed (deleted, truncated, appended elsewhere, torn), it re-reads it, so the missing rows are written again from the attempts in the store. (3) It writes every ended attempt's row. (4) It writes every pending decision. A decision that cannot be written stays queued and stops every route, also routes whose own decision did not change, until it is durable. A last line that is no row, after the re-read, stops routing; that is a log someone else wrote into, for the owner to look at. Row: `TestFix2LogDurabilityIsReverified` (truncated, then deleted, the row is back before the next dispatch; a torn foreign tail and a terminated non-row line both stop routing; a failed decision for one task stops another task's unchanged route, and once the path is writable the pending decision is written exactly once).
- P1-2 residuals (canon.go:45, :64, :74): a declared tree, or a glob under its literal prefix, is now expanded. The directory of the literal prefix is walked without following links (`filepath.WalkDir`, which reports a link as a link), and a symlink whose target, resolved against its own directory, leaves that directory makes the task local-only. A tree larger than 20,000 entries is local-only, because it could not be checked. The repository root's ancestors are resolved once per check (`ResolveRoot`, EvalSymlinks on the root only; on macOS the temp roots sit under the `/var` link). Every walk below starts from the real directory. Row: `TestFix2DeclaredTreesAreWalked`. With the root as given and with the root reached through a link: `public/`, `public/*.go` and `public/**` (with `public/link -> ../internal/secrets`) are local; `public/sub/` and `docs/` (whose links, one relative and one absolute, stay inside) are vendor. Deviation, unchanged from fix 1: the walk uses `os.ReadDir` and `filepath.WalkDir` rather than `internal/fsanchor`, which is not on this base; it moves onto fsanchor at the rebase.
- Limit b residuals (seed.go:35, :69): the seed is 32 raw bytes, mode 0600, a regular file (the mode is not checked on Windows). At a later start, meaning the session already has an outcome log or decisions, a missing seed or a malformed one (wrong size, wrong mode, the old hex text) is `routing_invalid`. It is refused and never regenerated, because a new seed would change pending draws. Only a session's first start writes one. Row: `TestFix2SeedIsCheckedNotRegenerated` (missing, mode 0644, 31 bytes and hex text are each refused; the missing seed is not recreated).
- Limit c stays deferred to milestone B (acceptance table).

Measured (`fix2-bench.txt`): one Route against a 100-task store with the durability checks takes 1.48, 2.28, 1.74 ms (fix 1 measured 1.29 to 1.77). Each Route adds a stat and a tail read of the outcome log.

Rows: one new row (`internal/routing/durable.go`); current_lines refreshed for router.go, canon.go, seed.go.

Behavior changes:
- A plan or review task with a hand-dispatched history outside its tiers blocks with `pin_unresolved` on retry.
- An escalation that appears between attempts raises the retry's tier.
- Routing stops on a log whose last line is not a row.
- A declared tree with an outward link is local-only.
- A session whose seed is missing or damaged no longer starts its router; it fails with `routing_invalid`.

## Fix 3: Codex pass 3 on 9fb1e131, the history rulings

One commit on 9fb1e131. Pass 3 found N1, N2, P1-4, P1-2 and the seed partial and two defects added, all one pattern: the router trusted its own recorded history as fact. The lead's six rulings are below, each with its test row. `docs/v3-spike/2b4-evidence/fix3-revert-check.txt` holds 20 rules, every one failing when reverted and passing when restored (cases in `tools/cases_fix3.py`); the gate output is `fix3-gate.txt`. Line numbers are against the 9fb1e131 export.

1. Rows carry an HMAC (durable.go:56, router.go:674).
   - Every outcome row and every decision carries `tag`, the HMAC-SHA256 of all its other fields keyed by the session seed's 32 raw bytes, with a domain label per kind.
   - On every full read of the log a row whose tag fails is treated as absent and reported as `history_tampered`. The router lists it in `Router.Tampered()` for the scheduler process to surface, and its attempt is logged again from the kept decision. A decision whose tag fails at start is ignored and listed the same way.
   - `routing report` verifies rows under the session seed (`VerifySessionRows`, which never creates a seed). It leaves tampered rows out and prints `history_tampered: N row(s) left out`. Without a seed file it prints that the rows are unverified (the testdata fixture log is unsigned and reports that way).
   - Detection: the router re-reads the log in full when its size, mtime or file identity changed, and at least every `FullCheckInterval` (1 minute), so a same-size rewrite with the mtime put back is caught within the interval.
   - Row: `TestFix3TamperedRowsAreAbsentAndReported` (a same-size hand edit of `holder`, once with the mtime changed and once with it restored and the clock past the interval: the row is absent, `row t1/1` is reported, a fresh signed row follows, the report counts one tampered row; a decision edit is ignored and reported at the next start).
   - Same-UID limit (recorded): a process running as the owner can read the seed and forge tags or set mtimes; the tags catch every edit that does not know the seed, and every accident.
2. Policy is a closed list, and `lead_pin` means a real pin (router.go:705, :715).
   - `Verify` accepts a row or decision only when its policy is on the closed list: table, explore, jev_shadow, lead_pin, and the new `manual`.
   - `lead_pin` is written only when the router itself saw a lead pin. An attempt that ran on another pool than the router decided, or one with no decision (a lead's `task claim` or `dispatch`, or a lost decision), is `manual` with propensity 0, meaning not drawn.
   - Row: `TestFix3PolicyIsAClosedListAndLeadPinIsReal`, plus `TestFix2HistoryIsCheckedNotTrusted` (a hand dispatch is `manual`).
3. Tiers come from the actual entry (router.go:347, :366).
   - A retry's starting tier is the tier of the entry the first attempt actually ran on, as the table places it among the tiers the class may run on by its own rules. The recorded tier string only breaks a tie between tiers that list the same entry.
   - An entry on no such tier refuses the task with `pin_unresolved`. The escalation floor applies to that actual tier.
   - Row: `TestFix3ActualTierNotTheRecordedString`. Each case uses a validly signed record:
     - A plan task whose record claims plan but whose entry is haiku is refused.
     - An implement task whose record claims smol but whose entry is sonnet retries on task.
     - Widened past three files, it retries on plan.
4. The tail is checked before any flush appends, and recovery keeps the policy (durable.go:34, :41, :64, :68; router.go:677).
   - `durable()` checks the tail before any flush appends: the log must end with a newline and its last line must parse as a row. A torn tail stops routing and nothing is appended past it. A complete row whose tag fails is not torn; ruling 1 handles it.
   - Decisions are kept after their row is written, so a row written again after a truncation takes its policy and propensity from the decision, never a new one.
   - Row: `TestFix3TailBeforeFlushAndRecoveryKeepsPolicy` (an explored attempt's row, truncated away, comes back as explore 0.05 and verifies; a torn tail with a row still to write stops routing, and the file is byte-for-byte unchanged).
5. The seed is read through a no-follow open (seed.go:101, :114).
   - The seed is read through `openNoFollow` (`O_NOFOLLOW` on Unix, `seed_unix.go`; an Lstat refusal and then open on Windows, `seed_other.go`). The checks run on the opened file (fstat): regular, mode 0600, exactly 32 bytes, read back as exactly 32.
   - Row: `TestFix3SeedIsNotReadThroughALink` (a symlink to a valid seed file is `routing_invalid`).
6. An unchecked tree is a refusal (canon.go:113, :204).
   - A declared tree that cannot be checked (more than `MaxTreeEntries`, or a walk error) is a refusal with its reason: the task blocks with `declared_tree_unchecked` and the detail names the path and the limit. It is no longer silently local-only.
   - Row: `TestFix3UncheckedTreeIsARefusal` (limit 5, a ten-file tree blocks; the privacy answer for it stays vendor; a narrowed declaration routes).
   - The race between the walk and a later swap is the same-UID limit, recorded.

Measured (`fix3-bench.txt`): Route against a 100-task store takes 1.50, 1.49 and 2.39 ms.

Rows: two new rows (`internal/routing/seed_unix.go`, `seed_other.go`); current_lines refreshed for durable.go, router.go, canon.go, seed.go, outcome.go, report.go and `internal/cli/routing.go`.

Behavior changes:
- Outcome rows and decisions carry `tag`, and unsigned or edited rows no longer count for the router.
- `manual` joins the policy list.
- A torn log tail stops routing.
- A declared tree over the limit blocks for the lead.
- The weekly report prints `history_tampered` and unverified-row lines.

## Fix 4: Codex pass 4 on f989e4c7 (the last Codex round), and the hold ruling

One commit on f989e4c7. Pass 4 found N2 complete and N1 and P1-4 partial, plus one new defect. The lead's ruling on the fix 3 design call is folded in here. Each item has a test row that fails when its rule is reverted: `docs/v3-spike/2b4-evidence/fix4-revert-check.txt` (16 rules, every one failing reverted and passing restored; cases in `tools/cases_fix4.py`); gate output `fix4-gate.txt`, run only on a host with no live seat or session (the gate script now refuses otherwise). Line numbers are against the f989e4c7 export.

- N1a (outcome.go:112-157, router.go:100): the outcome log is now a hash chain (`internal/routing/chain.go`).
  - Every row carries `seq` (its place, from 1) and `prev` (the previous row's tag) inside the signed bytes. Verification walks the chain in file order:
    - A seq ahead of the chain is a gap (`gap:a-b`), and the row re-anchors the chain.
    - The right seq with another predecessor is `prev:<hash>`.
    - A seq at or behind the chain is a duplicate or reorder (`order:<hash>`), and the row is not used.
  - The router and the report both walk it. A duplicated cost row is counted once (the duplicate never reaches the spend), and `routing report` counts every break as `history_tampered`. The router appends with the next seq and the last good tag, and stops at the first failed append so the chain stays contiguous.
  - Row: `TestFix4ChainCatchesDeleteReorderDuplicate`. Each case is read by a fresh router, standing in for a restart:
    - Deleting the middle row is `gap:2-2`, and its task is held.
    - A reorder is reported.
    - A duplicate is `order:` and is counted once.
- N1b (durable.go:127, router.go:312, :718), with the lead's ruling: a task whose own history may have lost a row waits with `history_tampered` until the lead clears it, and other tasks route.
  - When a full read finds a break the router has not seen, it holds every task the break may have cost a row:
    - the task a broken line names;
    - the task of a row that was in the log at the last read and is gone (`lost:<task/attempt>`, a deletion or truncation under the running router);
    - for a break whose task is not known for sure, every task with an ended attempt the log does not hold.
  - A held task's attempts are not logged again, and its route answers a block `history_tampered` routed to the lead. It no longer re-logs from the kept decision or reconstructs as manual.
  - The lead clears it with `amq-squad task reset`. The router sees the task pending again with an `updated_at` after its hold, records the clearance, then logs its attempts (from the kept decisions) and routes it.
  - Holds, clearances and every break already seen are signed records in the decision log, so a known break never holds again and a restart keeps the hold.
  - The hold is a block, not a pending `wait_reason`, because the lead's reset only takes a blocked task. The status row therefore shows `block_reason: history_tampered`, routed to the lead.
  - A completed task that lost a row cannot be reset. It stays reported and its row stays out of the log; it routes nothing anyway.
  - Rows:
    - `TestHoldTamperedTaskOthersRoute`: a tampered row on A blocks A with `history_tampered` while B routes. A restart keeps the hold. The lead's reset clears it, and the clearance survives a restart. A torn tail still stops routing for all.
    - `TestFix4KnownBreakHoldsOnce`.
    - `TestFix2LogDurabilityIsReverified`, now: a truncation or deletion holds the task instead of re-logging.
    - `TestFix3TailBeforeFlushAndRecoveryKeepsPolicy`, now: the row comes back after the reset, with the explore policy of its decision.
- N1c (outcome.go:233, durable.go:123): the chain reader checks each line's tag before trusting any field. A line that does not decode, has another schema, or whose tag fails is `line:<hash>` (`history_tampered`), reported, and the task it names is held. Row: `TestFix4WrongSchemaLineIsReported`.
- P1-4 (router.go:709): the public `Flush()` now runs the same checks as `Route()` (`durable`): the chain is verified and the tail checked before anything is appended. A torn tail makes `Flush` return the stop error and write nothing. Row: `TestFix3TailBeforeFlushAndRecoveryKeepsPolicy` (Flush past a torn tail: error, file byte-identical).
- New (seed.go:121-134, seed_unix.go:12): the seed open adds `O_NONBLOCK` to `O_NOFOLLOW`, then fstat must show a regular file before any read, the shape of the registry's runtime open. A FIFO at the seed path no longer hangs `routing.New` or the report. Row: `TestFix4SeedFIFODoesNotHang`.

Limits (recorded, the lead's list):
- A process with the owner's uid can read the seed and forge tags and chains.
- A same-UID writer can create a torn tail and stop routing; the guard's protected set denies guarded seats this write.

Rows: two new rows (`internal/routing/chain.go`, `tamper.go`); current_lines refreshed for durable.go, router.go, seed.go, seed_unix.go, outcome.go, report.go and `internal/cli/routing.go`. The testdata fixture log is regenerated (rows gained `seq` and `prev`; it stays unsigned, so the report prints it as unverified).

Behavior changes:
- Outcome rows carry `seq` and `prev`.
- A break in the history holds the tasks it may have cost a row, for the lead's reset.
- `Flush` can return the torn-tail stop.
- The report's line reads "history_tampered: N break(s)".

Gate note:
- The first quiet-host gate run of fix 4 failed in `internal/cli` `TestRealPTYWaitPostureRefusesNamedGateBeforeTimeout`, a real-PTY tmux test outside this brief. It took 16.4 s under the full run's load against 5.4 s alone, where it passes; it passed in every earlier gate. The output is kept as `fix4-gate-run1-pty-flake.txt`.
- The second run on the same tree is green (`fix4-gate.txt`).
- Both runs waited for 60 s of no `seat-exec`, `scheduler run`, `pi run`, `amq coop`, `amq-remote` or amq-squad lead, team or launch process (`tools/quiet_gate.sh`); milestone B's live run was up before that.

## Rebase onto v3 at 8abc1b7a, and the integration commit

The nine commits of the brief, with fixes 1 to 4, are rebased onto `v3` at 8abc1b7a (4-1 merged, with `internal/fsanchor` and the pi registry rows; 4-3 with fsanchor's fleet extras; 3-2; and the 4-2 fix). The rebased heads are 20310d5c (commit 1) through 9e6ac18e (fix 4).

Conflicts:
- `internal/cli/command_registry.go` and `internal/cli/cli_test.go`: v3's `fleet` verb and this brief's `routing` verb, both kept.
- `docs/v3-manifest.yaml`: current_lines of `command_registry.go` and `doctor_host.go`, refreshed after the merge.

The resolver is `tools/resolve.py`.

Integration commit:
- The canonical-path walk now runs on `internal/fsanchor` (the deviation recorded since fix 1 is closed):
  - The repository root, resolved once (`ResolveRoot`), is opened with `fsanchor.OpenAnchored`: from / one component at a time with O_NOFOLLOW, so a symlinked ancestor is refused, not followed.
  - Every component is listed and opened through its parent's descriptor (`Names`, `OpenEntry`: O_NOFOLLOW, O_NONBLOCK, fstat).
  - A symlink never opens. A new `AnchoredDir.Readlink` (readlinkat, `internal/fsanchor/routing_extra.go`, with a non-unix twin) tells a link from an unreadable entry and gives its target, so a declared tree's links are checked without following them.
  - The file is added beside 4-3's `fleet_extra.go`, so the change to 4-1's package is additive.
- 4-1's pi model ids:
  - On v3 the registry writes pi rows only for local Ollama models, as `ollama/<model>`. The lead's fallback model is `ollama/qwen3.6:27b`, and otherwise the registry's first verified pi row.
  - The default table keeps the pi entries in that form (`pi:ollama/qwen3.6:35b-a3b:low`, `pi:ollama/qwen3.6:27b:low`, and `pi:ollama/qwen3.6:27b:high` on probation).
  - It drops the two pi + Anthropic placeholders, which no registry row backs (lead ruling: no hardcoded ids of this brief's own). The default table is now 6 classes and 11 chain entries (smol 4, task 3, plan 2, advisor 2).
  - `TestFourChecks` shows `not_in_registry` with an override chain instead.

Evidence:
- `rebase-revert-check.txt`: 4 rules on the fsanchor walk (a link component found by readlinkat; a tree link that leaves the tree; the root resolved before the anchored open; the tree limit), every one failing reverted and passing restored.
- `rebase-gate.txt`: the full gate on a quiet host.

Rows: two new rows (`internal/fsanchor/routing_extra.go`, `routing_extra_other.go`); current_lines refreshed for `internal/routing/canon.go` and `table.go`.

Still open:
- Limit c: the Router in the scheduler process is milestone B's; `scheduler run` is not on v3 at this base.
- The phase 5 Codex re-pass list.
