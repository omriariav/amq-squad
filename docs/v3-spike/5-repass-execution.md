# amq-squad 3.0, phase 5: Codex re-pass fixes

## Routing re-pass (branch v3-5-repass-routing)

One commit on v3 at f91b253c: the eight findings of the phase 5 Codex re-pass on the routing package (internal/routing, internal/status/collect.go, internal/task/lease.go, the `scheduler run` verb). Each item has a test row that fails when its fix is reverted and passes when restored: `docs/v3-spike/5-repass-evidence/routing-revert-check.txt` (25 rules, all OK; harness and cases in `5-repass-evidence/tools/routing/`), written through `scripts/redact-evidence`. Gate on a quiet host: `routing-gate.txt` (written outside the tree, copied in through the redaction step). Line numbers are against the v3 tip.

| # | Finding | Fix | Test row |
| --- | --- | --- | --- |
| P1 ruling 1 | router.go:897 and task/lease.go:276 logged `verifier_result: pass` on a completion no verifier had decided: an implement task pinned to smol, claimed and reported done with empty evidence, completed as verified | The store's completion gate (`task.complete`): a class with a mechanical verifier (extract schema, script exit_code, implement tests; the class-to-verifier list now lives in `internal/task`, routing aliases it) completes only on the harness's recorded verdict on the current attempt (`Store.RecordVerdict`, `lease.verdict`: verifier, result pass or fail, source, detail). A done report with no verdict, at `done` (no declared files) or at the commit step's acceptance (`Complete`, `CompleteBound`), blocks the task for the lead with `verdict_missing`; a fail verdict with `verdict_failed`. The attempt ends `blocked`, a failed attempt for escalation, and carries the verdict. The row's `verifier_result` is `pass` only from a recorded pass verdict; a completion without one (an older store) is `not_run`, and `verdict_missing` is `not_run`. The scheduler logs such an acceptance as `blocked`, not `completed` | `TestRepassMechanicalClassCompletesOnlyOnAHarnessVerdict` (internal/task), `TestRepassVerdictMissingIsNeverAPassAndEscalates` (the pinned smol implement blocks, its row is `not_run`, the retry runs on task) |
| P1 ruling 2 | router.go:598 counted tasks against a pool's concurrency cap: a seat still closing after its task expired, and the lead's own seat on the same Ollama GPU, occupied nothing | `inUse` counts occupants, each once: the scheduler's seat view (`Scheduler.Occupancy`: live seats, closing seats, seats closed but not yet confirmed gone, pending seat requests), the holder of every leased task the view does not name, and the lead's seat (`Options.LeadPools`) when it shares the pool. A lead pool whose effort the registry does not list is placed by runtime and model. `scheduler run` passes its scheduler's view (bound at `Started`, before the first tick) and `--lead-pool runtime:model:effort` (repeatable); the lead's team mode passes `pi:<model>:<thinking>` for each of its models | `TestRepassConcurrencyCountsSeatsNotTasks` (a closing seat, the lead's seat, one seat with its task is one occupant), `TestRepassOccupancyNamesLiveClosingAndRequestedSeats` (internal/scheduler) |
| P2-3 | collect.go:310 decoded usage into `map[string]float64`, so a usage object with nested objects (`input_tokens_details`) was unknown, and `prompt_tokens`/`completion_tokens` were ignored | `status.PayloadTokenCounts` decodes only the named counters, from `usage`, `message.usage` or flat on the payload, with the aliases `prompt_tokens`, `promptTokens`, `completion_tokens`, `completionTokens` (a bare `input`/`output` only inside a usage object) | `TestRepassUsageCountersByNameNestedFlatAndAliases` |
| P2-4 | collect.go:316 and usage.go:54 accepted negative counters, which subtracted from the day's spend at router.go:351 | A counter that is negative, fractional, above 2^40 or not a number makes the payload's usage unknown with the reason (`TokenCounts.Invalid`); `LedgerUsage` reports it (`Usage.Invalid`), the row carries `usage_invalid`, the weekly report counts and prints the rows. A usage source's negative or out-of-range count, or a negative or non-finite cost, is refused at the row (`Usage.checked`), and a cost only ever adds to the spend | `TestRepassInvalidCountersAreUnknownUsageAndReported` |
| P2-5 | report.go:108, :207: an excluded first attempt made the second attempt the task's first, losing the failure and its cost; the same across the weekly boundary | The report keeps whole task histories (every verified row, any week). Redo looks at every later attempt of the history; a task completed in the window is costed by the bucket of its first attempt, and its cost is unknown when any attempt from the first to the completion is missing, excluded (its runtime is left out of the window, or an earlier row is incomplete) or unpriced | `TestRepassTaskHistoriesKeepTheFirstAttempt` (excluded first attempt, first attempt last week, history starting at attempt 2, the whole history in the week) |
| P2-6 | report.go:191: an unpriced entry on a runtime with any incomplete row vanished from the list | The unpriced names come from every verified row of the window before the runtime filter: known tokens and no cost | `TestRepassUnpricedEntriesBeforeTheRuntimeFilter` |
| P2-7 | seed.go:107, :124: the report accepted a log cut to a valid prefix with head.json removed, and a seed copied onto another inode, which startup refuses | `checkHead` (head.go) is the one head and seed-identity check; `routing.New` and `VerifySessionLog` both run it, so the report refuses both with `routing_invalid` | `TestRepassReportRunsTheStartChecks` |
| P2-8 | scheduler_run.go:170: a merged routing.yaml that does not load was a plain wrapped error | `scheduler run` builds its router through `routing.Open` (`StartOptions` gained `Seats` and `LeadPools`), so the refusal is an `*InvalidError`, `routing_invalid` | `TestRepassSchedulerRunRefusesAnInvalidTableAsRoutingInvalid` |

Judgment calls (the two rulings are applied as given):
- Ruling 1, where the verdict comes from: the commit step does not run tests today and no probe predicate decides task verdicts, so nothing in production calls `RecordVerdict` yet. Until the commit step's test run and the probe land, every extract, script and implement done report blocks with `verdict_missing` for the lead. This is the ruling's consequence, not a fallback; it is the main behavior change of this commit. The verdict is recorded on the lease, so a probe can record it before the seat's `done` and the commit step after it; a re-armed commit (a widening, a cleared quarantine) is a new lease and needs a new verdict. The report stays on the blocked task, as on a commit refusal, so the seat stays reserved until the lead's reset.
- Ruling 1, the existing tests: every test that completed an implement, script or extract task now records the harness's pass verdict first (`harnessPass`, a test helper per package: task, routing, scheduler, status, seatcommit, cli). The fixture log of acceptance 6 is unchanged (its completions are built with a pass verdict).
- Ruling 2, tasks the view does not name: a leased task whose holder is not in the scheduler's view (a lead's hand dispatch) still counts its holder once. Without a seat view (tests, `routing explain`) the holders of leased tasks are the occupants. `TestFourChecks`'s six busy Claude tasks now carry six distinct holders.
- P2-7: a session with no seed file still reports its rows as unverified (fix 3's rule and the fixture log); the start's checks apply whenever the seed is there.
- P2-5, a first attempt in an earlier week with complete evidence and a known cost counts toward the task's cost, attributed to that attempt's bucket.

Rows: current_lines refreshed for internal/task/task.go, class.go, lease.go; internal/routing/router.go, report.go, seed.go, head.go, open.go, outcome.go, table.go, usage.go; internal/status/collect.go; internal/scheduler/scheduler.go; internal/cli/scheduler_run.go, lead_scheduler.go, pi_lead.go. docs/task-store-design.md names the verdict and the two block reasons.

Behavior changes:
- A mechanical-class done report completes only with a harness verdict; otherwise `verdict_missing` (or `verdict_failed`) blocks it for the lead.
- Outcome rows: `verifier_result: pass` only from a recorded verdict; a new optional `usage_invalid` field. Attempts carry `verdict`.
- A concurrency cap counts seats, closing and pending ones included, and the lead's seat.
- `scheduler run --lead-pool`; an invalid routing.yaml stops it with `routing_invalid`.
- The weekly report: `usage_invalid` line, history-based redo and cost, unpriced entries of every runtime, and the start's head and seed checks.

Recorded limits stand: unpriced and zero-price entries by price policy; same-UID forgery and torn-tail denial.

## 3-2 re-pass (branch v3-5-repass-routing, second commit)

One commit after the routing commit: the eight findings of the phase 5 Codex re-pass on 3-2 (internal/mergetrain, internal/releaseflow, internal/ledger). Each item has a test row that fails when its fix is reverted and passes when restored: `docs/v3-spike/5-repass-evidence/3-2-revert-check.txt` (14 rules, all OK; harness and cases in `5-repass-evidence/tools/3-2/`), written through `scripts/redact-evidence`. Gate on a quiet host: `3-2-gate.txt`, with `-timeout 45m` on the race step. No test reaches the network: the forge is the in-process fake gh (the test binary over a local bare repository) and the release flow's fake executor. Line numbers are against the v3 tip.

| # | Finding | Fix | Test row |
| --- | --- | --- | --- |
| P1-1 | releaseflow.go:332: the publish PATCH succeeded, its response was lost, and the retry read `draft: false`, failed `same()` and deleted the published release; the fake removed published releases (releaseflow_test.go:95), which hid it | The recovery path treats a release found at the recorded id with `draft: false`, the approved tag and the approved commit as completion: the record and the intent are removed, `draft_published` is audited as recovered, and the step is done with the release URL (`Draft.URL`, `html_url`). A published release is never deleted: `discard` on a non-draft raises `draft_edited` and audits `release_mismatch`, "not deleted". The fake keeps a published release at its id with draft false, as GitHub does | `TestRepassLostPublishResponseIsCompletionNotDeletion` |
| P1-2 | gh.go:97: the force-push timeline was one page of 100, so after 100 audited train rebases an unauthorized force push was invisible; the fake returned everything (fakegh_test.go:169) | `forcePushes` reads the whole timeline with `pageInfo{hasNextPage endCursor}` and `after: $c`. More than `MaxTimelinePages` pages (default 50 of 100), a rate limit, or a page without a new cursor leaves the read partial: `PRInfo.HistoryIncomplete` with the reason, refused as `history_incomplete`. The fake pages 100 at a time behind a cursor and can answer a rate limit on a page | `TestRepassForcePushTimelineIsReadWhole` (150 audited rebases then a seat's push: `force_push`; page bound 1 and a rate limit on page 2: `history_incomplete`) |
| P1-3 | ledger.go:199: an unterminated quoted secret (`--password 'alpha beta`) redacted only its first word | The open-quote fallback (`scrubFields`) redacts everything after a secret flag, `Bearer`/`Basic` or a secret `key=`, to the end of the line; the flag or key stays | `TestRepassUnterminatedQuotedSecretIsRedactedWhole`; the earlier row for `deploy --token t88 'unterminated` now keeps `deploy --token [redacted]` |
| P2-4 | releaseflow.go:340: the draft POST succeeded but the record write did not (a crash or a lost response), so the retry posted a second draft and the first was untracked | Before the POST the flow writes the creation intent (`draft-intent.json`: tag, target, a 16-byte random nonce), and the draft's body carries the nonce (`NonceMarker`, an HTML comment). Before creating, `Executor.FindDrafts` lists the releases whose body carries it (every page): a matching draft is adopted (`draft_adopted`), any further draft of the intent is deleted and audited (`draft_duplicate`). The intent goes with the record after publication | `TestRepassLostDraftCreationIsAdoptedByItsNonce` |
| P2-5 | train.go:534: GitHub merged, the connection failed before the audit append, and the retry treated the PR as refused (verify.go:175 then said `not_merged`) | The train journals `merge_intent` (PR, task, head, digest) before the PUT. A failed PUT re-reads the PR: merged at the pinned head (`PRInfo.MergeCommit`, from `mergeCommit`) is the merge, audited as reconciled. At the start of every real run `reconcileMerges` checks each intent with no merge after it against the server and writes the merge entry; a PR whose last journaled merge step is a merge is reported `merged` and not checked again, so no refusal follows it and `verify merge` reads the merge | `TestRepassLostMergeResponseIsReconciled` (the same run; a run that crashed after the PUT, reconciled at the next start) |
| P2-6 | audit.go:111: a torn last line got the next record concatenated, and Read (:137) skipped both | Under the append lock the tail is checked first, the outcome log's rule: an unterminated last line is terminated before the entry, which stays one whole line | `TestRepassAuditTornTailIsRepaired` |
| P2-7 | ledger.go:289, :323: ForTask dropped Tail's truncation flag and Outcomes read only the first 8 MiB, so task verification silently omitted evidence | Both task-filtered reads stream the whole file (`streamLines`); a line over `MaxLine` (4 MiB) is skipped and named. `ForTask` and `Outcomes` return the reasons, and `verify merge|release --task` carries `incomplete: true` with `incomplete_reasons` and prints them | `TestRepassTaskEvidenceIsReadWholeOrMarkedIncomplete` (a row past 9 MiB is found; an over-long line is incomplete) |
| P2-8 | ledger.go:285: the live lease's evidence window ran to now, so a line written after an expiry the scheduler had not processed counted | The window ends at `min(now, ExpiresAt)` | `TestRepassLiveLeaseWindowEndsAtItsExpiry` |

Judgment calls:
- P1-1: a published release that does not match (another tag or commit) is not deleted either; it is an owner alert. The ruling named only the matching case.
- P2-5: a PR whose last journaled merge step is a merge (reconciled or from an earlier run) is reported `merged` at plan time instead of going through the checks to a `pr_state` refusal; otherwise the refusal entry would follow the reconciled merge and `verify` would read `not_merged` again.
- P2-7: incomplete evidence does not fail the verification by itself; the result says it, with the reasons. The `verify` wiring has no revert row of its own (the CLI has no `--task` verify fixture); the ledger rows cover the reads.
- P1-3: the flag or key stays visible (`--password [redacted]`), so the line still shows what was redacted.

Rows: current_lines refreshed for internal/releaseflow/releaseflow.go, gh.go; internal/mergetrain/gh.go, forge.go, checks.go, train.go, audit.go; internal/ledger/ledger.go; internal/cli/verify.go.

Behavior changes:
- The release flow writes `draft-intent.json` and a nonce comment in the draft body; a lost publish response completes on retry.
- The merge train journals `merge_intent`, reconciles lost merges in the run and at the next start, reports already-merged PRs as merged, and refuses a PR whose force-push timeline is partial (`history_incomplete`).
- `evidence` redaction of an open-quote line drops everything after the secret flag.
- `verify --task` reads the whole ledger and outcome log, reports incomplete evidence, and ends a live lease's window at its expiry.

Recorded limits stand: first-observation baselines, same-UID, the owner's tag-protection bypass, dry runs persisting the drift baseline.

## The class switch and the narrow pass (branch v3-5-repass-routing, third commit)

One commit after 15782597 (after the rebase onto v3 at 9fc4dc34, after 71224047). It carries the lead's ruling on ruling 1's consequence, and the rest of the reviewer's narrow pass on 15782597: twelve of sixteen were complete; R1, R2, P2-5 and 3-2 P1-1 were not, and five coverage rows were asked for. Each item has a test row that fails when its fix is reverted and passes when restored: `docs/v3-spike/5-repass-evidence/narrow-revert-check.txt` (22 rules, all OK, the reviewer's correction and the store-wiring row included; cases in `tools/routing/cases_narrow.py`). All three revert checks were rerun on the rebased head: `routing-revert-check-head.txt` (25 rules, all OK; the two ruling-1 gate cases now name the gate as it stands after the switch) and `3-2-revert-check-head.txt` (14 rules, all OK). The gate is committed and runs from the tree: `tools/routing/gate.sh` (with `quiet_gate.sh`, which waits for 60 s of quiet first); its output is `narrow-gate.txt`, written outside the tree and copied in through the redaction step. Line numbers are against the 15782597 export.

### The class switch (the lead's ruling on ruling 1)

- The configuration key: `verifiers:` in the owner grant (`~/.amq-squad/grants.yaml`), a map from class to verifier command. Only the classes with a mechanical verifier (extract, script, implement) are accepted, each command a non-empty single line, read through the same strict yamlsub subset (a scalar, an unknown or verifier-less class, an empty command and a repeated class are refused). squad.yaml may only make the gate stricter, with `verifier_classes: [class, ...]` (the reviewer's correction at the rebase): the list is the squad's verifier gate and must be a superset of the classes the grant verifies. A class it adds with no grant command still requires a verdict, so its done reports block with `verdict_missing` and the block detail names the missing grant command; a list that omits a class the grant verifies is refused (`grants_invalid`: `LoadFrom` fails, and `Narrow` keeps the grant's verifier and names the attempt); a verifier-less class is refused. `Grants.VerifierFor(class)` is the command, `Grants.VerifierRequired(class)` the gate.
- The task store: `task.Store.Verifiers` (a `VerifierSource`) gives a task's class its command and whether it is in the verifier gate. The completion gate (`verdict_missing`) applies only to a gated class; a class outside it completes on the done report. A recorded fail verdict always blocks (`verdict_failed`). A source that errors fails closed and requires the verdict, with the reason in the block detail. A nil source is strict (every mechanical class needs a verdict), so an opener that does not read the grant never completes such a task on the seat's word.
- Production source: `grants.TaskVerifier`, the owner grant with the squad.yaml of the task's `project_path` (the repository the lead named at `task add`), never a seat's tree; an untrusted owner file is an error, so the store fails closed. The CLI's task stores (`task done`, `seat commit`) and `scheduler run`'s (the process's and the commit step's) open through `openTaskStore`, which sets it; row `TestRepassCLIStoresReadTheVerifierGrant` (a grant verifying implement: an implement done report blocks, a script one completes, no verdict helper).
- The router: a class runs below its tier only when its verifier is mechanical and its command is in the grant (`mayRouteDown`): a class with no command routes at its tier and never below (an exploration draw goes up, a pin below the tier is `pin_unresolved`); a class outside the gate records `verifier_result: none`, never pass. `table.go:243` still fixes each class's verifier kind (plan 6.1); the switch is whether its command is configured.
- The producer that runs the command (records `RecordVerdict`) is v3-5-verifier's, not this branch's.
- Rows: `TestRepassUnconfiguredVerifierRoutesAtTierAndCompletes` (routing: at tier, explored up, a pin below the tier refused, done completes, result none), `TestRepassGateOnlyForAConfiguredVerifier` (task: outside the gate completes, a command blocks, a gated class with no command blocks and the detail names it, an unreadable source blocks), `TestVerifiersFromTheGrantAndOnlyNarrowed` (grants: the superset adds a class; a list omitting implement, which the grant verifies, is refused through `LoadFrom`). With a verifier configured the earlier rows stand (`TestRepassVerdictMissingIsNeverAPassAndEscalates`, `TestRepassMechanicalClassCompletesOnlyOnAHarnessVerdict`); the routing fixtures now configure a verifier for the three classes (`verifiedGrants`).

### The narrow pass

| # | Finding | Fix | Test row |
| --- | --- | --- | --- |
| R1 | every extract, script and implement done report blocked with verdict_missing (lease.go:277, :298) | the class switch above | the switch rows |
| R2 (a) | router.go:605 counted idle seats against the cap before scheduler.go:606 could reuse one: with cap 1 and one idle seat a pending task waited or routed elsewhere | Occupancy counts busy (holding a task), closing and requested seats; an idle seat of the entry's own pool is capacity for its pending task and is not counted; an idle seat of another pool in the same concurrency pool still holds its share | `TestRepassSchedulerSeatViewHoldsTheCap/idle_seat` (a real scheduler whose router reads its Occupancy, cap 1: the second task is dispatched to the idle seat) |
| R2 (b) | `--lead-pool` was settable by whoever runs `scheduler run` | the flag is refused unless the process's parent is the lead's team mode (`amq-squad lead --team`, read from the parent's arguments). Recorded: a same-UID process can still run its own `amq-squad lead --team` to start one; the scheduler process is the harness's | `TestRepassLeadPoolOnlyFromTheLeadsTeamMode` |
| R2 (c) | asynchronous launch failures hold occupancy until the request's timeout and its confirmed cancellation | recorded as right: a request keeps its place under the cap until the launcher confirms no seat will join, so a late join never exceeds the cap | none (no change) |
| P2-5 | report.go:248, :276 accumulated a cross-week first attempt's cost into a bucket never instantiated for output, so the smol cost of a task completed this week on the task tier disappeared | the first attempt's bucket is instantiated when its cost is accumulated | `TestRepassTaskHistoriesKeepTheFirstAttempt` (the smol bucket of a task whose first attempt ended last week reports 0.011 USD) |
| 3-2 P1-1 | releaseflow.go:484 ignored the alert-write error | `alert` and `tagMoved` return `ErrAlertUnwritten` on a failed write, and every caller joins it into its error: a mismatch never passes silently | `TestRepassPublishedMismatchIsNeverDeleted` (the alert directory unwritable) |

Coverage rows asked for:
- Seat-view wiring: `TestRepassSchedulerSeatViewHoldsTheCap` (cap 1: a seat still closing after its claimed lease expired holds the GPU, so a new smol task goes to the next entry; the idle-seat case above) and `TestRepassSchedulerRunSeatViewWiring` (`scheduler run`'s `schedulerSeatView`: nothing before the scheduler exists, then its Occupancy).
- `--lead-pool` forwarding: `TestRepassLeadForwardsItsPools` (two models, thinking `high`, the child argv `leadSchedulerCommand` builds).
- `verify --task` incomplete: `TestRepassVerifyTaskEvidenceIncomplete` (a 4 MiB outcome line: `incomplete: true` and the reason in the JSON, the reason in the text).
- The spend guard in `note()`: `TestRepassNegativeCostRowNeverLowersTheSpend` (a signed row with cost -5 at the head's next seq, read back at a restart: it verifies, and the day's spend stays 0).
- The published-deletion guard: `TestRepassPublishedMismatchIsNeverDeleted` (the recorded draft id found published with another tag, and with another commit: zero deletes, the `draft_edited` owner alert saying "not deleted", the `release_mismatch` audit line).

Rows: current_lines refreshed for internal/task/task.go, lease.go, scope.go; internal/grants/grants.go, parse.go; internal/routing/router.go, report.go; internal/cli/task.go, scheduler_run.go, lead_scheduler.go, pi_lead.go, verify.go; internal/schedproc/process.go; internal/releaseflow/releaseflow.go.

Behavior changes:
- With no `verifiers:` in the owner grant, extract, script and implement tasks complete on the done report and route at their tier, never below; their rows say `verifier_result: none`. With a verifier configured for a class, the earlier gate stands for it.
- New grant key `verifiers:` and squad.yaml key `verifier_classes:` (a superset of the grant's classes; omitting one is `grants_invalid`).
- An idle seat of the pool no longer counts against its concurrency cap.
- `scheduler run --lead-pool` is refused outside the lead's team mode.
- A release-flow owner alert that cannot be written is an error.

Recorded limits stand: unpriced and zero-price entries by price policy; same-UID forgery and torn-tail denial; the same-UID start of a team mode for `--lead-pool`.

### Rebase onto v3 at 9fc4dc34

The three commits were replayed onto v3 at 9fc4dc34, which carries the phase 5 test-hygiene merge (186fe04c). Conflicts, in the 3-2 commit only, resolved keeping both sides' intent:
- `internal/mergetrain/gh.go`: the hygiene branch's unexported `GH.runner` seam and shared error formatting stay; `MaxTimelinePages` sits beside it, and the paginated timeline read goes through the same `run`, so the in-process fake answers it.
- `internal/mergetrain/fakegh_test.go`: the in-process `fakeGHRun` split (writers, `t.Parallel`, the three subprocess rows) stays; the paged timeline, the rate-limit page, `mergeCommit` and the lost merge response write to its `stdout` writer.
- `docs/v3-manifest.yaml`: the gh.go counts, refreshed from the file.
The reviewer's correction to `verifier_classes` (a superset only) and the store-wiring row are folded into the third commit.

## Seat, hook, fleet, Buzz and milestone B re-passes (branch v3-5-repass-2b2)

Five commits, cut from `v3` at 1bc8c680 and rebased onto 1608eaba (after the
routing re-pass merge); lines in the findings refer to 1bc8c680. Each item has
one test row that fails with its fix reverted in place and passes restored
(run with -race); the revert checks and gates are under
`docs/v3-spike/5-repass-evidence/`, every file through
`scripts/redact-evidence`. Every gate ran on a quiet host (ps showed none of
seat-exec, scheduler run, pi run, amq coop, amq-remote or an amq-squad
lead/team/launch process; an orphaned v2 `_session-notify` with ppid 1 is
not counted).

### Commit 1: 2b-2 (seat-exec, the seat launch, the launcher)

Revert check: `5-repass-evidence/2b2-revert-check.txt` (11 reverts, all as
required). Gate: `5-repass-evidence/2b2-gate.txt`.

| # | finding (1bc8c680 lines) | fix | test row |
| --- | --- | --- | --- |
| P1-1 | seatexec_unix.go:347 read `-c VALUE` and `-c=VALUE` but not Codex's attached `-cVALUE`, so `-csandbox_workspace_write.writable_roots='["/"]'` passed and overrode the injected empty roots | one scanner (`codexOptions`) reads every value option the check uses in every form Codex admits, up to `--`: spaced, joined with `=`, and attached for a short option (`-cX`, `-sX`, `-aX`); the protected config keys (sandbox_mode, sandbox_workspace_write.*, approval_policy) are refused in all of them, and an attached `-s`/`-a` is read as the sandbox or approval | TestCodexAttachedShortOptionsAreRead |
| P1-2 | claude.go:47 accepted `--setting-sources project,local` and `--bare`, which drop the user settings that enable the hooks plugin | `CheckClaudeHooks` refuses, as claude_hooks_missing and before the proof and exec, a `--setting-sources` list without `user` (both forms, an empty list included) and the flags that skip hooks: `--bare` and `--safe-mode` (Claude Code's help: both skip hooks). Their variables, CLAUDE_CODE_SIMPLE and CLAUDE_CODE_SAFE_MODE, are CLAUDE_CODE_* host-session names the scrub already drops | TestClaudeArgvThatDropsTheHooksIsRefused |
| P1-3 | launcher.go:126 kept the last Apply observation for the handle, so two observations (a live pane and a nonexistent one) let the cancel check the wrong pane (:237), return nil and release the reservation while the live pane proves | an observation for a handle the request does not know, or a second one for its handle, marks the request apply_ambiguous: it stays unresolved and no pane check confirms its cancel. The cancel asks the launch's proof server: a seat that proved is held by its proved pid and stamp and closed, and only that confirmed close releases the request; without a proof the cancel keeps returning apply_ambiguous | TestAmbiguousApplyObservationsKeepTheRequestUnresolved |
| P2-4 | env.go:70 missed GIT_SSL_NO_VERIFY, GIT_SSL_CAINFO, GIT_SSL_CAPATH | added to NeverNeeded: scrubbed whatever the runtime declares, env_needs or credential_needs naming one is env_needs_invalid | TestGitTLSTrustVariablesNeverReachASeat |
| P2-5 | options.go:138 ran the identity proof with CombinedOutput and no WaitDelay, so a descendant of `amq env` holding the pipes hung seat-exec past the 20 s context | amq runs in its own process group; WaitDelay 5 s; a context end or a wait-delay expiry kills the whole group; either is identity_proof_timeout, a refusal (the detail names no output) | TestIdentityProofDescendantHoldingThePipesTimesOut |
| P2-6 | proof_peer_unix.go:34 ran `tmux list-panes` with no deadline while the proof server's mutex was held | the lookup (and the pane id lookup) has a 2 s context, as panechain's lookup for the decision socket, and a 500 ms WaitDelay; a timeout is "not known", so proof_origin_unknown. `judge` runs the lookup before it takes the mutex, and only for a proof whose token is live (a wrong-token sender never makes the server call tmux); the decision then runs under the lock with every check as before | TestPaneLookupIsBoundedAndOutsideTheMutex |

Judgment calls, stated:
- P1-3: "until the proof or a confirmed close settles it" is read literally.
  An ambiguous request with no accepted proof stays unresolved after the
  proof window ends (the scheduler shows it as a closing row from the first
  failed cancel); it is not released at the window's end, although no
  seat-exec can exec without a proof then. A late proof is acted on at the
  next cancel (the scheduler cancels a request that never came up); the
  launcher does not watch for it between cancels.
- P1-2: `--safe-mode` is refused beside `--bare`: Claude Code's help says it
  disables hooks too. The Claude scan, like the existing permission scan,
  reads the whole argv (it does not stop at `--`), the fail-closed side.
- P1-1: the attached form is parsed for the three value options the check
  reads (`-c`, `-s`, `-a`); clustered boolean short flags are not modelled
  (Codex has none that bear on the sandbox).

The fake tmux and amq in these rows are local scripts under the test's
temporary directory, the proofs come from in-process stubs or a prover child
of the test binary; no row reaches the network.

## Commit 2: 4-2 (the Claude hooks plugin, claudeseat, the hook verbs)

Revert check: `5-repass-evidence/4-2-revert-check.txt` (10 reverts, all as
required; the Go rows under -race, the shell rows through
`scripts/test_claude_hooks.py` against a binary built from this tree). Gate:
`5-repass-evidence/4-2-gate.txt` (it adds the hook script tests).

| # | finding (1bc8c680 lines) | fix | test row |
| --- | --- | --- | --- |
| P1-1 | claudeseat.go:368 checked the worktree settings for allow rules only; `{"env":{"AMQ_SQUAD_BIN":"/tmp/allow"}}` in `.claude/settings.json` replaced the pinned hook binary (Claude's settings env wins over the launch value), and those files sat outside the digest | `ForbiddenSettingsEnv`: an env key naming AMQ_SQUAD_BIN, AM_*, AMQ_SQUAD_* or CLAUDE_CODE_* in any settings source Claude merges for the seat (the config dir's settings.json, the worktree's .claude/settings.json and settings.local.json) refuses the seat as claude_settings_env_forbidden, inside `claudeseat.Check`, so at all three of seat-exec's checks; an unparsable source is refused. The launch records the worktree in the config dir (`amq-squad-worktree.json`, a managed file) and the digest covers the worktree's two settings files, present or absent, read through descriptors with no symlink followed, so a file written after the launch is claude_config_changed for the hooks and a refusal for seat-exec | TestWorktreeSettingsEnvAndDigest |
| P1-2 | token.go:51 set CLAUDE_CODE_SUBPROCESS_ENV_SCRUB=1, which on Claude Code 2.1.310 forces permission mode default and defeats dontAsk | the setting is removed and the seat's settings.json carries no env block; condition 3 is withdrawn for 2.1.310 and the token's visibility to the seat's Bash children is R33's recorded limit. Measured on this host (2.1.310, scratch config dir, no credential): with the setting the stream-json init event reports `permissionMode: "default"` and warns "Permission mode forced to default"; without it, `"dontAsk"`. The launch now probes every Claude seat (`claudeseat.ProbeMode`): the installed claude with a scratch copy of the seat's settings.json, in the seat's worktree, `-p --output-format stream-json --verbose --permission-mode <mode>`, no credential; the init event's permissionMode is read and the process group killed before any model request. Another mode, or no answer within 15 s, refuses the launch (claude_mode_unverified) before any seat applies | TestLaunchRefusesAClaudeSeatWhoseModeIsForced; the probe row TestModeProbeOnTheInstalledClaude (AMQ_SQUAD_LIVE=1; passed here on 2.1.310: dontAsk) |
| P2-3 | hook_guard.go:101 and hook_ingest.go:64 resolved identity without the config-dir check hook_context.go:185 runs | `hookFlags.seatIdentity` is identity plus `checkClaudeConfig` (the digest report and, on a change, the quarantine); guard and evidence ingest call it, context runs the same check through buildHookContext. A changed dir makes guard deny with claude_config_changed; ingest still records the payload | TestGuardAndIngestRunTheSharedConfigDirCheck |
| P2-4 | amq-squad-hook.sh:89 killed only the guard pid, and :93 waited without a redirection, so a stub's sleep survived and "Killed: 9" reached the hook's stderr | `set -m` around the guard job, so it runs as its own process group; the watchdog and the post-reap cleanup kill the group (`kill -9 -- -PGID`); both waits send the shell's job report to the log descriptor | Repass5.test_hung_guard_group_is_killed_and_the_reap_stays_out_of_stderr (scripts/test_claude_hooks.py) |
| P2-5 | amq-squad-hook.sh:106 mapped a prefix match of the verdict, so a truncated `{"verdict":"allow"` became an allow | the mapping moved to Go: `amq-squad guard --decision claude` reads the verdict, requires one complete JSON object with a known verdict, and prints the decision (encoded by encoding/json); anything else prints nothing and one line the hook logs. The shell no longer reads the verdict | Repass5.test_truncated_verdict_is_no_decision_and_logged; TestClaudeDecisionNeedsTheCompleteVerdict (Go) |

Judgment calls, stated:
- P1-1: a worktree settings file is digested by content, so a seat that edits
  its own `.claude/settings*.json` during the run is quarantined
  (claude_config_changed). Claude Code's managed (system) settings are the
  owner's and are not read.
- P1-2: the probe runs the installed claude once per Claude seat at launch
  (about 0.3 s measured); it reads only the init event and sends no
  credential. The probe row against the real binary is opt-in
  (AMQ_SQUAD_LIVE=1), so the default suite starts no claude; the default row
  stubs the probe.
- P2-3: on a changed config dir guard denies every call (the seat is
  quarantined); ingest keeps recording, so the ledger stays complete.
- P2-4: `set -m` is POSIX job control in a non-interactive shell (checked on
  macOS /bin/sh); not executed on Linux dash in this run.
