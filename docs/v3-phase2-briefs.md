# amq-squad 3.0: implementation briefs for phases 2b, 3 and 4

Date: 2026-10-24. Base: branch `v3` at dfe9aad3 (phase 2a closed).
Inputs: `docs/v3-plan.md` revision 2.5; `docs/v3-spike/amendments.md`
items 19 to 24 (23 and 24 are not yet in the plan; they are for
revision 2.6); `docs/v3-spike/2a2-execution.md` (the three-class ledger,
"Notes for 2b and phase 3", the deferral ledger); `docs/v3-spike/2a3-notes.md`;
`docs/v3-spike/spike-report.md`; `docs/v3-upstream-asks.md` (final
sections). This file is planning output. It contains no code.

Each brief is self-contained. An implementer reads its brief, the plan
sections that the brief names, and the notes that the brief names. Where
the plan wording is binding, the brief quotes it with the section number.

## Overview

### Dependency order

```
start now:      2b-1  2b-2  2b-3  3-1  4-1
after 2b-3:     2b-4
after 2b-1:     4-3  4-4
after 2b-1's first commit (task schema only):  2b-5
after 3-1:      4-5
after 3-1+2b-2: 3-2  4-2
after 4-1:      4-3 live operator run (4-3's Go-level acceptance 1 to 6 do not wait)
```

2b-5 no longer waits for 2b-1 to merge: squad.yaml lives at `<repo root>/squad.yaml` and the task store lives under the AMQ root, so 2b-5 needs only the task schema from 2b-1's first commit. [review 1] 4-3's live operator run waits for 4-1, because 16.5 puts the lead and the mayor on the built-in pi. [review 1]

All phase 4 briefs depend on the three 2a verbs (`context`, `guard`,
`evidence ingest`). These are merged (2a-3, 2026-10-24). 4-4 depends on
the status contract from 2b-1. The plan rule is: "Phase 4 depends on the
2a verbs. amq-noc depends on the 2b contract. Within those dependencies,
2b, 3, and 4 run in parallel." (section 13).

Shared contracts that cross briefs. The owner of each contract writes it
first, in its first commit, so that parallel briefs code against it:

| Contract | Owner | Consumers |
| --- | --- | --- |
| Task schema fields `class`, `declared_files`, `pool`, lease fields | 2b-1 | 2b-2 (commit step), 2b-4, 3-2, 4-4 |
| `status --json` schema 1 and the golden fixture | 2b-1 | 2b-3 (doctor self-check), 4-3, 4-4 |
| Usage-limit event interface (the scheduler stop) | 2b-1 (interface) | 2b-3 (detector) |
| Launch record schema 1 (role sha256, rules path and sha256, `AMQ_SQUAD_BRANCH`, identity) | 2b-2 | `context`, `guard`, 3-2, 4-2 |
| Registry entry type | 2b-3 | 2b-4, 4-1, 4-3 |
| Grants schema and the narrowing function | 3-1 | 2b-5 (squad.yaml), 4-3 (fleet grant) |
| Gate store writer and the target-string table | 3-1 | 3-2, 4-2, 4-5 |

### First wave

The S6 load numbers allow twelve seats: six Sonnet-medium seats peaked at
4.19 GB and had zero usage-limit events in ten minutes of work, and twelve
seats extrapolate to 8.4 GB typical on 64 GB (S6 report, amendment 22).
But RAM is not the limit. The usage window is. The plan's phase 2 roster
rule puts implementers on Opus 5.5 in their own worktrees, and the rate-limit
stops of the last days argue for fewer parallel agents: the orchestrating
session hit its usage limit at 02:10 on 2026-10-21 with six agents and
three Codex reviewers busy (spike log); on 2026-10-22 the first 2a-1
implementer stopped on a rate limit after cut 5 and its successor made no
commit (2a1-execution.md); the 2a-2 implementer stopped on an API rate
limit in the middle of commit 4 (2a2-execution.md).

Proposal: four parallel implementers in the first wave.

| Slot | Brief | Why in wave 1 |
| --- | --- | --- |
| 1 | 2b-1 task store, scheduler, status contract | critical path: 2b-5, 4-3, 4-4 wait for it |
| 2 | 3-1 gates, grants, gate store | critical path: 3-2, 4-2, 4-5 wait for it |
| 3 | 2b-2 seat-exec and launch | 3-2 and 4-2 wait for it; first seat launch from the v3 binary |
| 4 | 2b-3 registry, detection, doctor | shortest brief; unlocks 2b-4 |

Roster: 2b-3 is Opus 5.5 at medium effort; Sonnet 5.5 at medium effort is for 4-2 and 4-4 only; 4-5 is Opus 5.5 at medium effort. [review 1]

Wave 2, in the order slots free: 2b-4 (after 2b-3), 4-1 (no dependents,
so it waits), 2b-5 (after 2b-1's first commit) and 4-4 (after 2b-1), 3-2 and 4-2 (after 3-1 and
2b-2), 4-3 (after 2b-1; live run after 4-1), 4-5 (after 3-1). The cap stays at four parallel
implementers for the whole of phases 2b to 4. Codex reviews at merge do
not count against the four, but the lead does not start a fifth agent of
any kind while four implementers run.

### Estimated days per brief

Implementer-days. Elapsed time is shorter because briefs run in parallel.

| Phase | Brief | Days | Phase total (plan section 13) |
| --- | --- | --- | --- |
| 2b | 2b-1 | 1.0 | |
| 2b | 2b-2 | 0.5 to 0.75 | |
| 2b | 2b-3 | 0.25 to 0.5 | |
| 2b | 2b-4 | 0.25 to 0.5 | |
| 2b | 2b-5 | 0.25 | 2.25 to 3.0 (plan: 2 to 3) |
| 3 | 3-1 | 2.0 | |
| 3 | 3-2 | 1.0 | 3.0 (plan: 3) |
| 4 | 4-1 | 1.0 to 1.25 | |
| 4 | 4-2 | 0.5 | |
| 4 | 4-3 | 0.75 to 1.0 | |
| 4 | 4-4 | 0.5 to 0.75 | |
| 4 | 4-5 | 0.25 to 0.5 | 3.0 to 4.0 (plan: 3 to 4) |

### Rules for every brief

- One implementer per brief, in its own worktree with the work branch
  checked out (phase 2 roster rule, section 13). No helper agents without
  the lead's ruling (2a-2 ruling 5). Codex reviews the branch at merge.
- No v2 code comes back. A file that 2a deleted stays deleted. The spike
  code under `spike/` (seatexec, gateverify, embedpi, pi-hold-ext,
  pi-lead-loop, piprobe, loadtest) is a reference to port with tests, not
  a module to import.
- The standard gate for every commit: `go build ./... && go vet ./... &&
  go test ./... && scripts/manifest-check.sh --final`, plus
  `GOOS=linux go build ./...` and `GOOS=windows go build ./...`.
- Every new non-test Go file under `internal/` gets a manifest row with
  fate `new` and status `new` (2a-3 rule). A rewritten ledger row ends as
  `shrunk` (at or under target), `deleted`, or is split into `new` rows
  with the old row `deleted`. No row ends as `pending` or `trimmed`.
- Plain sentences in docs and commit messages. Evidence for every
  measured claim.

### Ledger rows that no brief in this set retires

These accepted-3 rows are brief drafting, an off-path verb that no 2b,
3 or 4 workstream redesigns: `internal/cli/workstream.go`,
`internal/cli/briefs_seed.go`, `internal/drafter/run.go`,
`internal/drafter/config.go`. They stay accepted-3 into phase 5 unless
the lead assigns them. `internal/cli/wait_posture.go` and
`internal/cli/session_reset_support.go` (kept-dead extracts) are touched
only if a brief needs them.

---

## 2b-1 Task store, scheduler and status contract

### Goal

Replace the v2 task store and the v2 status output with the 3.0 task
store (five states, dependency gating, file locks, leases, pools) driven
by a deterministic Go scheduler, one per squad. Publish `amq-squad status
--json` as the versioned contract with a golden fixture, with the
five-state classifier and the attention queue moved in from amq-noc.

### Reads

- Plan 4.5, 4.6, 7.2 (seat cap and usage-limit stop), 8, 16.4 (restart
  recovery is phase 5), 16.5 (process ownership).
- R13 (mode is read here, set in 2b-5 and 3-1), R17, R18, R19 (the lead
  does not poll), R43, and the scheduler side of R16 (seat cap, usage-limit
  stop). R20 to R28 are 2b-4's; this brief gives them the `class` and
  `pool` fields and the dispatch hook.
- Binding text: "Dispatch is tracked by task leases, not by mail. [...]
  an unclaimed assignment returns to the pool on lease expiry and the lead
  reassigns." (4.5). "After the first usage-limit event in the window no
  new seat starts" (7.2). "a suggested action as argv plus an action id,
  never a shell string" (4.6).
- 2a-2 accepted-3 rows owned here: `internal/cli/task.go`,
  `internal/cli/task_selection.go`, `internal/cli/task_completion_evidence.go`,
  `internal/task/task.go`, `transaction.go`, `reconcile.go`,
  `command_evidence.go`, `completion_reconcile.go`, `lifecycle_event.go`,
  `leadership.go`; the state rows `internal/state/collapse.go`,
  `message.go`, `coordination.go`, `external_evidence.go`, `mailbox.go`,
  `snapshot.go`, `state.go`; `internal/cli/operator_attention.go`
  (attention projection only, see Replaces); `internal/activity/activity.go`;
  `internal/procinfo/procinfo.go`, `parse.go`, `native_other.go`.
- 2a-3 open question 2 ("current task" before the 2b task store).
- Evidence: S6 finding A and task N4 (an unclaimed assignment sat for
  three hours); S5 lead loop run 2 (a doorbell in the middle of a turn
  dropped a task).
- amq-noc source of the classifier, for porting only:
  `amq-noc/internal/state/collapse.go`, `attention.go`,
  `attention_queue.go`, `snapshot.go`. Do not edit amq-noc in this brief.

### Builds

- `internal/task` (3.0 shape): one JSON file per task under the squad's
  task directory, the existing journal commit protocol kept
  (`docs/task-store-design.md`). Five states. Fields added: `class` (closed
  list of 6.1), `declared_files` (the task-declared file set of 7.2, path
  globs relative to the repo root), `pool` (runtime x model x effort key),
  `lease` (holder, granted_at, expires_at, renewals), `attempt`,
  `wait_reason`. v2-only fields (outbox, dispatch delivery, completion
  notification suppression, lifecycle generations) go. Write the field
  list in the first commit; 2b-2 and 2b-5 code against it. The store
  lives under the AMQ root at `<root>/extensions/amq-squad/tasks/`,
  beside the gate store, the answers and the hook ledger, not under the
  team home. [review 1]
- `internal/scheduler` (new): a deterministic loop. Inputs: the task
  store, the registry entry type (an interface until 2b-3 merges), the
  grant's seat cap, the usage-limit event source (an interface; 2b-3
  supplies the detector). It grants leases, sends a task to an idle seat
  of the chosen pool or requests a new seat inside the grant, returns an
  unclaimed task to the pool on lease expiry, retries per 6.2's retry rule
  through a hook that 2b-4 fills, closes idle seats after a timeout (R18),
  and starts no new seat after the first usage-limit event in the window.
  The scheduler requests seats through an interface that 2b-2 implements;
  it does not launch.
- `context`'s current-task read (hook_context.go) moves to the new store:
  the task whose lease the handle holds, most recently renewed.
- `internal/status` (new) and `amq-squad status --json`: the fields of
  4.6, extended by amendment 24's fields [review 1]: per task
  `title`, `updated_at`, `project_path`; per seat `last_message_at`,
  `last_message` (truncated), `prompt_state`, `tokens_in`, `tokens_out`
  (null when the runtime gives no number). `schema_version` 1,
  `producer_version`, `generated_at`. The five-state classifier
  (needs-you, blocked, waiting, online, stale) and the attention queue,
  ported from amq-noc and reduced to what the contract needs.
  `capabilities[]` lists what this producer supports.
- Golden fixture: `internal/status/testdata/status-v1.golden.json`, plus
  a test that fails on a non-additive change.

### Replaces or deletes

- `internal/cli/task.go`, `task_selection.go`, `task_completion_evidence.go`:
  rewritten on the 3.0 store; each ends `shrunk` at or under its target
  (800, 150, 250), or `deleted` with `new` rows for the replacement.
- `internal/task/*` accepted-3 rows: `shrunk` at target or `deleted`.
  `internal/task/lifecycle.go` is reviewed in the same pass.
- `internal/state/*` accepted-3 rows: the classifier base moves into
  `internal/status`; each state row ends `shrunk` or `deleted`.
- `internal/cli/operator_attention.go`: the attention projection moves
  into `internal/status`. The row ends `shrunk` (target 400). The gate
  answer parts stay for 3-1 and 4-3.
- `internal/activity/activity.go`, `internal/procinfo/*`: `shrunk` at
  target (seat state inputs only).
- `internal/cli/status.go` (current 838 lines): rewritten to print the
  contract; status by its manifest row's rule.

### Acceptance

1. Lease expiry test: a task assigned to a seat that never claims it
   returns to the pool at `expires_at` and is re-dispatched to another
   seat (the S6 N4 case), with a fake clock.
2. Doorbell test: a task sent while the seat is in the middle of a turn
   is not lost; the lease, not the mail, decides (the S5 run 2 case).
3. Determinism: the same store and the same event sequence give the same
   dispatch decisions, 100 runs with shuffled goroutine timing, `-race`.
4. Seat cap: with `max_seats` 3, the fourth seat request waits with
   `wait_reason: seat_cap`. After one usage-limit event, no new seat is
   requested; running seats keep their leases.
5. Idle close: an idle seat is closed after the configured timeout.
6. Contract: `status --json` validates against the golden fixture; a
   test removes a field and fails; a test adds an optional field and
   passes. Every suggested action is an argv array plus an action id.
7. `context --me H` reports the leased task from the new store (2a-3
   context tests updated, not dropped).
8. Concurrency: 8 processes claim from one store; no task gets two lease
   holders.

### Gates

The standard gate, plus `go test -race ./internal/task/... ./internal/scheduler/... ./internal/status/...`.

### Dependencies

None. Starts now.

### Out of scope

Routing chains and the outcome log (2b-4). Launching seats (2b-2).
Restart recovery of the scheduler from the store (phase 5, 16.4).
amq-noc changes (4-4). `status --fleet` (4-3).

### Agent

Opus 5.5, high effort. Reason: the lease and dispatch state machine is
the base that seven other briefs read, and a race in it loses tasks
silently, as S6 N4 and the S5 lead loop showed. One implementer, own
worktree, branch `v3-2b1`. Codex review at merge.

### Checkpoints

- Halfway: the task schema and the status schema committed (the shared
  contracts); acceptance 1, 2, 8 green; line count of the rewritten rows
  against their targets.
- End: all acceptance rows green; manifest statuses of every row in
  Replaces; `status --json` sample output on a seeded store; `-race`
  result; non-test line total under internal/.

### Open rulings

1. The five-state classifier against the four amq-noc buckets
   (amendment 24): does the contract carry both, or only the five states
   and amq-noc maps? Proposal: the contract carries the five states;
   4-4 maps.
2. Token counts: Claude hook payloads may carry no token numbers. Is a
   null field acceptable for schema 1, with the source named in
   `capabilities[]`?
3. Lease length and renewal cadence defaults (proposal: 10 minutes,
   renewed by `context` and by evidence ingest).
   Ruled: `context` is read-only (2a-3 contract) and never renews leases; renewal comes from `evidence ingest` on every tool result and from the scheduler on seat activity; the 10-minute default stays a proposal. [review 1]
4. Does the store stay under `<team home>/.amq-squad/tasks/` or move
   under the AMQ root? 2b-5 decides the squad.yaml location (2a-3 open
   question 5); the store path follows it.
   Ruled: the store moves under the AMQ root, `<root>/extensions/amq-squad/tasks/`, beside the gate store, answers and hook ledger. Reasons: runtime state stays out of the repository (R7, 4.7); seats carry AM_ROOT (R52); the three stores share one directory and one protection rule. squad.yaml lives at `<repo root>/squad.yaml`, so the 2b-1/2b-5 cycle is broken: 2b-5 needs only the task schema from 2b-1's first commit. [review 1]

---

## 2b-2 seat-exec and launch

### Goal

Make the v3 binary launch Claude Code, Codex and pi seats through the AMQ
launch API with the wrapper `amq-squad seat-exec`, which writes the launch
record and role, sets the full seat identity and execs the provider in
place. Add the harness-side commit step, where the Go core checks the
task-declared file set before it commits and pushes the seat branch.

### Reads

- Plan 4.1 (pi launch adapter), 4.3 in full, 7.2 (task scope at the
  commit), 7.3 (Codex sandbox bullet), 14 (launcher-preference risk).
- R3, R4, R5, R48 to R52, the `seat-exec` parts of R16 (environment
  scrub, denial of writes to `~/.claude/settings.json` and
  `~/.codex/config.toml`).
- Amendment 21 (the harness commits for every seat), amendment 23 (pi
  adapter shipped upstream in PR #1188, with two caveats), amendment 20
  (a) and (b).
- Binding text (4.3): "The wrapper takes only `--seat <handle>`; there is
  no `--root` flag, and `AM_ME` and `AM_ROOT` come from the environment."
  "It never adds provider flags." "The role comes only from `role.md` in
  the seat directory. `initial_input` carries at most a pointer to it."
  "`seat-exec` recomputes the hash before exec and refuses to start on a
  mismatch." "`seat-exec` anchors directory traversal to the verified AMQ
  root and follows no symlink in the initial path." "The refusal has a
  path: it is reported to the lead; the lead may widen the task's declared
  file set as a recorded lead decision, inside the grant's limits
  (protected paths and the size cap still apply); the harness then
  commits; a widening that would cross a grant limit escalates to the
  owner-signed gate. The seat never widens its own set."
- R52: "The launch sets the complete AMQ identity in every seat
  environment (`AM_ME`, `AM_ROOT`, `AM_BASE_ROOT`, `AM_SESSION`) from the
  launch record only, and proves one `amq drain` before it reports the
  seat as up".
- 2a-2 deferred-2b rows: `internal/launch/record.go` ("minimal launch
  record; seat-exec writes it"), `internal/cli/context_resolution.go`
  ("context resolution kept, simplified by squad.yaml"). Kept-dead
  extracts: `internal/cli/team_launch_launchapi.go` (the launchapi
  Prepare/Apply backend, the 2b ancestor), `internal/cli/team_launch.go`,
  the `launch.go` extracts (launch record write and rollback,
  `seedRoleStub`), `internal/cli/bootstrap.go` (prompt closure).
  Accepted-3 rows owned here: `internal/cli/amq.go`, `amq_send.go`,
  `amq_env.go` ("keep context resolution, send, env pinning").
- 2a-2 note: "the library needs a real amq executable path (plan R51,
  upstream #1159) before the kept-dead launchapi backend can launch from
  the amq-squad binary."
- 2a-3 open questions 3 (rules digest at launch) and 6
  (`AMQ_SQUAD_BRANCH`).
- Upstream state (`docs/v3-upstream-asks.md`, update 2026-10-24): #1154
  argument forms shipped in v0.106.2; #1157 and #1158 shipped in
  v0.106.1; #1153 pi adapter merged on main after v0.107.0 (PR #1188);
  #1159 AMQPath for embedders is PR #1186, still open. `go.mod` pins the
  AMQ library at v0.75.0.
- Reference code: `spike/seatexec/` (S4, criterion 4 PASS on v0.106.1).

### Builds

- `amq-squad seat-exec --seat <handle>` (new file set under internal/cli
  or a new `internal/seatexec` package): verify the AMQ root, anchor
  traversal, check ownership and modes, recompute the role.md sha256 and
  the rules file sha256 against the launch record, set `AM_ME`, `AM_ROOT`,
  `AM_BASE_ROOT`, `AM_SESSION` and `AMQ_SQUAD_BRANCH` from the record
  only, scrub `*_API_KEY`, `*_TOKEN`, `*_SECRET` and the known vendor
  names unless the runtime declares a need, deny writes to the owner's
  global agent config (and write the preflight copy of those files that
  2b-3's doctor diffs), then exec the provider in place.
- Launch record schema 1 (`internal/launch`): seat, runtime, model,
  effort, role path and sha256, rules path and sha256, seat branch,
  worktree, identity fields, launch nonce, AMQ apply outcome. Written by
  the Go core under the AMQ root before the launch.
- Launch compiler: one AMQ launch request per squad from a typed seat
  list. Model, effort and the Codex flags (`--sandbox workspace-write`,
  `--ask-for-approval on-request`) travel as provider arguments for
  Claude and Codex only. pi seats carry no provider arguments
  (amendment 23 (b)): the pi model restriction stays on the registry side.
  The admitted forms come from the AMQ capability advertisement (v0.106.2
  and later); no local list of forms.
- The launchapi backend from the kept-dead ancestor, on an AMQ library
  version that carries #1186. Until #1186 is released, the lead rules
  between waiting and the R51 fallback ("amq-squad itself serves the `coop
  exec` contract").
- R48: the operator output shows the `ApplyResult` outcome, reason code
  and failure detail. R49: a decision send never writes lead presence.
  R50: suggested actions name only flags that work on the launchapi path.
- R52 drain proof: one `amq drain` per seat before the seat is reported
  up; a failed drain is a launch failure with its reason.
- pi seats through `amq launch` with `--session-id <launch nonce>`
  (amendment 23). Caveat (a): upstream resume is not fail-closed, so
  seat-exec refuses a pi resume when the seat's evidence ledger shows at
  least one tool result and the session file for the recorded id is
  missing. Caveat (b) as above. `amq coop exec` stays as the fallback.
- Harness-side commit step (new): after the seat's done report,
  seat-exec (or the lead's command) computes the tree diff of the seat
  worktree, checks each path against the task's `declared_files`
  (2b-1's field), the grant's protected paths and the size cap, and then
  commits and pushes the seat branch, or refuses with a reason code sent
  to the lead. The lead widening path: a recorded lead decision adds paths
  to `declared_files` inside the grant; a widening that crosses a grant
  limit raises the growth gate (3-1). One commit path for Claude and Codex.
- `context_resolution.go`: the context sources reduce to flags, the
  injected `AM_*` identity and the launch record. The `.amqrc` source and
  the candidate diagnostics go when 2b-5 lands squad.yaml; this brief
  removes the launch-side uses.
- The controlled role-delivery matrix (plan section 13, amendment 15):
  Claude and Codex seats, role.md pointer in `initial_input` against role
  in the first message, N runs each, role acknowledgement measured.

### Replaces or deletes

- `internal/launch/record.go` (deferred-2b, 536 against 250): rewritten
  as launch record schema 1; ends `shrunk` at or under 250.
- `internal/cli/context_resolution.go` (deferred-2b, 846 against 551):
  ends `shrunk` at or under 551, or `deferred-2b` handed to 2b-5 with the
  remaining lines named (lead ruling at the end checkpoint).
- `internal/cli/team_launch_launchapi.go`, `team_launch.go`, the
  `launch.go` and `bootstrap.go` extracts: become live code of the 3.0
  launch, or are deleted where the new compiler replaces them; each row
  ends `shrunk` or `deleted`.
- `internal/cli/amq.go`, `amq_send.go`, `amq_env.go` (accepted-3): env
  pinning comes from the launch record; each ends `shrunk` at target
  (400, 150, 150).

### Acceptance

1. Criterion 4 re-run from the v3 binary: one Claude seat and one Codex
   seat through the launch API, model and effort in the trust digest,
   seat-exec sets only environment and role. Pass rule as in the spike
   report row 4.
2. Role integrity: a changed role.md byte makes seat-exec refuse; role
   text in `initial_input` without role.md stops the seat.
3. Traversal: a symlink in the initial path, a group-writable existing
   directory and a wrong owner each make seat-exec refuse (one test each).
4. Scrub: a seat started with `GEMINI_API_KEY`, `GH_TOKEN` and
   `AWS_SECRET_ACCESS_KEY` set sees none of them; a runtime that declares
   a need sees only that one.
5. Identity: every seat environment has the four `AM_*` values from the
   record; a seat whose drain fails is not reported up (the S6 finding A
   case reproduced with `AM_SESSION` removed).
6. R48 to R50: a forced `backend_create_failed` shows outcome, reason and
   detail; a decision send leaves no lead presence; no suggested action
   names `--force-duplicate`.
7. Commit step on a fixture repo: a diff inside `declared_files` commits
   and pushes; a diff with one file outside (the S6 W5 case) is refused
   with a reason code; a lead widening commits; a widening onto a
   protected path is refused and raises the growth gate request. A Codex
   seat under workspace-write ends with a pushed branch made by the
   harness.
8. pi resume refusal: ledger with one tool result and a missing session
   file is refused; a seat with no tool result resumes.
9. Role-delivery matrix: the table with N and the acknowledgement rate
   per cell is written to the execution log.
10. Global config: a seat write to `~/.claude/settings.json` under a test
    HOME is denied, and the preflight copy exists.

### Gates

The standard gate, plus a tmux-backed launch test on darwin with the
installed amq (skipped in CI with a stated reason), and the criterion 4
evidence directory committed under `docs/v3-spike/` or linked.

### Dependencies

None to start. The commit step reads 2b-1's `declared_files` field
(written in 2b-1's first commit). The launchapi path from the binary needs
#1186 or the R51 fallback. Real pi seats with the embedded runtime need
4-1; until then pi launch is tested with a fake pi.

### Out of scope

squad.yaml schema (2b-5; this brief compiles from a typed seat list).
Registry checks before launch (2b-3). Gate writing (3-1). The Claude
hooks plugin (4-2). The embedded pi runtime (4-1). The merge train (3-2).

### Agent

Opus 5.5. Two milestones in one worktree, branch `v3-2b2`, one
implementer at a time: milestone A (seat-exec, launch record, launch
compiler, R48 to R52, pi launch) at medium effort; milestone B (the
harness-side commit step and the widening path) at high effort. Reason
for high on B: the commit step is the only enforcement point of the task
file set (7.2), and a wrong diff check lets a seat land a file outside its
task with no later check before the merge train. Codex review at merge.

### Checkpoints

- Halfway (end of milestone A): acceptance 1 to 6 and 8; AMQ library
  version used; whether #1186 or the fallback carries R51.
- End: acceptance 7, 9, 10; manifest statuses of every Replaces row;
  launch timings (seat-exec wall time to exec, median of 10).

### Open rulings

1. #1186 not released: wait, or implement the R51 fallback (serve `coop
   exec` in amq-squad)?
   Ruled: as lead ruling 8: milestone B waits for #1186 if it has merged by then, else implements the R51 fallback and records it as temporary. [review 1]
2. AMQ library bump from v0.75.0 to the release after v0.107.0: one step,
   or first to v0.106.2 (argument forms) and later to the pi release?
   Ruled: the Go module versions track the CLI tags; milestone A bumps go.mod to v0.107.0 at its start (argument forms, Codex capture fixes, the AM_* identity change of #1176) and to the first tag carrying #1188 when it exists; not one bump that waits. [review 1]
3. `SSH_AUTH_SOCK` reaches both seat kinds (amendment 17). Scrub it,
   since the harness now pushes and seats need no SSH agent, or keep it?
   Proposal: scrub.
   Ruled: scrub `SSH_AUTH_SOCK`; the harness pushes and seats need no agent. [review 1]
4. Who runs the commit step: seat-exec as a post-exec harness step (the
   process exec'd in place, so a supervisor must exist) or the lead's
   command on the done report? 4.3 says "the exact shape of that step is a
   phase 2b design item". Proposal: a separate `amq-squad seat commit
   --seat H` that the scheduler runs on the done report.
   Ruled: as proposed, `amq-squad seat commit --seat H`, run by the scheduler on the done report, because seat-exec is exec'd in place and cannot run after the provider. Consequence: milestone B needs 2b-1's scheduler hook merged, or codes against the committed interface and rebases. [review 1]
5. Size cap default for the commit step (shared with 3-2's merge grant).
   Ruled: measured, not guessed, and shared with 3-2 (2). The S6 dry run reports changed lines and files per PR; the default is the largest clean S6 PR rounded up to the next hundred lines and the next five files, recorded in the plan. Until then no number appears in the briefs. [review 1]

---

## 2b-3 Registry, detection and doctor

### Goal

Write `~/.amq-squad/registry.yaml` from a probe of the installed tools
and models, and make `doctor` the probe of plan 16.2. Add the owner-run
`amq-squad trust <repo>` step and the checks that the spike amendments
named.

### Reads

- Plan 5 in full, 8 (doctor warnings on sleep and login service), 16.2,
  16.8 (the Codex notify fan-out, a 2b item).
- R9, R10, R11, R12; the doctor parts of R16 (shell-profile check,
  global agent config diff).
- Amendment 22 (seat cap: "The seat cap is a grant field next to these
  registry caps."), amendment 17 (Codex model id check against the pinned
  codex-cli version, the shell-profile check, codex-cli owns
  `config.toml`), amendment 18 (config diff), amendment 1 (trust).
- Binding text (5): "A local entry gets `verified` only after a tool-call
  and edit probe passes. The lead proposes seats from verified entries
  only. A missing tool is one status line, not an error." (16.2): "The v2
  team, pane, and wake-mirror checks go."
- 2a-2 deferred-2b row `internal/cli/doctor.go` ("probe per plan 16.2",
  866 against 600).
- 2a-2 note (ruling 13): "registry.yaml carries the catalog routing
  metadata that commit 7 cut from catalog.json: capability_tier,
  cost_index, latency_index, strengths, work_classes."
- S5 report (probe predicate, qwen3.6:27b 6 of 6, qwen2.5:32b on
  probation, gemma3:4b no tool calls); S6 report and `spike/loadtest/`
  (the usage-limit scanner).
- Reference code: `spike/piprobe/`.

### Builds

- `internal/registry` (new): entry type with runtime, model, effort,
  verified, cost tier, privacy class, quota pool, concurrency cap,
  untrusted_input, probation, latency (direct text TTFT), plus
  capability_tier, cost_index, latency_index, strengths, work_classes.
  Write the type in the first commit; 2b-4 codes against it.
- Detection: PATH probe for claude, codex, Grok Build, Cursor, ollama,
  amq, amq-remote, tmux, gh; versions compared with the versions the AMQ
  launch API verified; login state without a login; Ollama model list;
  RAM and GPU; TTFT per entry.
- Codex model id check against the pinned codex-cli version before
  launch (the S4 closure case: `gpt-6-astra` refused on 0.147.0).
- Local model probe (R12): N of N with zero false successes, N=5 for a
  shipped entry; the S5 predicate (confirmed tool execution, staged and
  untracked diff checks, exact final line, a read).
- Usage-limit detector: reads provider session files and emits events on
  2b-1's interface.
- `doctor` rewritten as the 16.2 probe: detection, versions, login state,
  local model probes, latency, sleep and login-service warnings, the
  status contract self-check (after 2b-1 merges), untrusted Codex roots,
  the shell-profile check for exported `*_API_KEY`, `*_TOKEN`,
  `*_SECRET`, the diff of the owner's global agent config against the
  preflight copy (written by 2b-2), and the grants file mode (2a-3 open
  question 4).
- `amq-squad trust <repo>`: prints the owner-run steps to trust a repo in
  codex-cli and, if the lead rules so, the notify fan-out line (16.8).
  It never writes the owner's Codex config.

### Replaces or deletes

- `internal/cli/doctor.go` (deferred-2b): the kept v2 checks (team
  config, worktree, version skew) are rewritten or cut; the row ends
  `shrunk` at or under 600, with the probe code in `new` rows where it
  lives in `internal/registry`.

### Acceptance

1. A missing tool gives one status line and exit 0; doctor on a host with
   none of the optional tools passes.
2. Registry write is idempotent: two runs on the same host give the same
   file except measured latency.
3. Probe: a fake model that claims an edit it did not make is a false
   success and blocks `verified`; 5 of 5 passes give `verified`; a
   false claim in a run that ends in `RESULT: FAIL` is not a false
   success (amendment 15).
4. Codex model id: an id that the pinned codex-cli refuses is marked
   unusable before launch (fixture of the S4 closure output).
5. Shell profile: a fixture `.zshrc` with `export GEMINI_API_KEY=` gives a
   warning naming the variable, never its value.
6. Config diff: a changed fixture `~/.claude/settings.json` shows the
   diff against the preflight copy.
7. Untrusted roots: a fixture Codex config without the repo lists the
   repo; `trust` prints steps and writes nothing (test asserts no file
   change under a test HOME).
8. Usage-limit detector: replays the S6 session files (0 events) and a
   fixture with one usage-limit line (1 event).
9. doctor wall time on the reference Mac with probes off: under 2 s.

### Gates

The standard gate.

### Dependencies

None to start. The status self-check waits for 2b-1's contract; the
config diff reads 2b-2's preflight copy (a fixture until 2b-2 merges).

### Out of scope

Routing chains (2b-4). The seat cap value (a grant field, 3-1); this
brief only puts the concurrency caps in the registry. Running a probe at
every launch.

### Agent

Opus 5.5, medium effort (roster ruling). [review 1] One implementer, own worktree, branch `v3-2b3`.
Codex review at merge.

### Checkpoints

- Halfway: registry type committed; detection and acceptance 1, 2, 4.
- End: all acceptance rows; the registry.yaml written on the reference
  Mac (attached, keys only); doctor wall time; doctor.go line count.

### Open rulings

1. Grants file mode (2a-3 open question 4): doctor reports it; does
   guard fail closed (deny every typed-gate class) instead of ignoring the
   file? 3-1 owns the guard side; doctor follows the ruling.
   Ruled: follows 3-1's insecure-file ruling: fail closed; doctor reports the file mode and the reason code `owner_grants_insecure`. [review 1]
2. Notify fan-out (16.8, candidate): does `trust` print an owner-written
   notify line, and does doctor check it?
   Ruled: `trust` prints the owner-written notify line as a candidate and doctor checks presence only; nothing writes config.toml (amendment 20). [review 1]
3. How often doctor runs the full runtime re-hash (S3 open question 1).
   Ruled: full runtime re-hash at every launch (190 ms is inside budget). [review 1]
4. Which provider files the usage-limit detector reads for Codex (S6
   report: the Codex scanner was not prepared).
   Ruled: 2b-3 measures which Codex files carry the limit signal and names them in its notes. [review 1]

---

## 2b-4 Routing table and outcome log

### Goal

Give the scheduler the routing of plan section 6: the closed class list
with verifiers, the tier chains with the four checks, the escalation
rules, exploration, and one outcome log row per attempt. Add the weekly
report that proposes table changes for the owner.

### Reads

- Plan 6 in full.
- R19, R20 to R28, R43 (the wait reason and the pool in the contract).
- Binding text (6.1): "A task routes below its tier only when its
  verifier is mechanical. Work with no verifier never starts cheap."
  (6.2): "No passing entry: the task waits and the status names the
  reason. Local-only paths never go to a vendor." "a retry moves one
  tier up and never repeats the entry; the tier is fixed per task, never
  switched per turn."
- 16.8: "the weekly routing report uses only runtimes with complete
  evidence."
- The registry entry type (2b-3) and the task fields `class`, `pool`,
  `attempt`, `wait_reason` (2b-1).

### Builds

- `internal/routing` (new): the default table in the binary, overridden
  by `~/.amq-squad/routing.yaml` and by `.amq-squad/routing.yaml`, which
  also maps path globs to a required privacy class. Chain selection with
  the four checks (verified, privacy class, concurrency cap, cost cap).
  Escalation: above 32k tokens skip Ollama, above 100k skip Haiku, retry
  one tier up with no repeated entry, tier fixed per task. Plan and
  review never route down; the reviewer family differs from the
  implementer family. `exploration_share` (default 0.05, zero disables,
  never plan or review).
- The class label: `task add --class <c>` (or the 2b-1 equivalent);
  a task without a class is refused.
- Outcome log (new, append-only JSONL under the AMQ root, flock and fsync
  as in the 2a-3 ledger): one row per attempt with every field of 6.4.
- `amq-squad routing report --week`: per class, tier and size band, the
  redo-at-higher-tier rate, dominated cost, wait time with skip reason,
  review findings traced to smol outputs; one proposed change per bucket,
  printed for the owner, never applied.

### Replaces or deletes

No ledger row. The catalog routing metadata cut in 2a-2 commit 7 now
lives in the registry (2b-3). New rows only.

### Acceptance

1. A table test per class: summarize never routes below its tier; script
   routes down; plan and review never route down; a review of a Claude
   implementation picks a non-Anthropic family.
2. Privacy: a task touching a path under a local-only glob never selects
   a vendor entry, even when no local entry passes (the task waits with
   `wait_reason: privacy_class`).
3. Escalation: 40k tokens skips Ollama; 120k skips Haiku; a retry moves
   one tier up and the skipped entry list grows.
4. Exploration: over 10,000 seeded draws the share is 0.05 within 0.01,
   never on plan or review, and every row records the policy and the
   propensity.
5. Outcome log: every attempt of a seeded run has a row with every 6.4
   field; a concurrent append test as in 2a-3 (8 writers, whole lines).
6. Weekly report on a fixture log prints one proposal per bucket and
   excludes runtimes with incomplete evidence.
7. `status --json` names the wait reason for a task with no passing
   entry.
8. `status --json` carries a top-level `lead_handle` (additive, schema 1
   stays 1): the handle of the seat whose role is `lead`, `null` when no
   such seat; the golden fixture gains the field, and `status.NullableFields`
   lists it. amq-noc's enter action reads it first and falls back to the
   seat with role `lead` (4-4 ruling, 2026-10-24).

### Gates

The standard gate.

### Dependencies

2b-3 (registry entry type) merged. Reads 2b-1's task fields; if 2b-1 is
not merged, code against its committed schema and rebase.

### Out of scope

Jev (R29; plan section 13 puts it in phase 5, section 16.6 in phase 4;
not in this brief). The lead's labeling prompt (4-1). The "lead does not
start coding" loop test of R19 (4-1, with a real pi lead).

### Agent

Opus 5.5, medium effort. One implementer, own worktree, branch `v3-2b4`.
Codex review at merge.

### Checkpoints

- Halfway: table and selection with acceptance 1 to 3.
- End: all acceptance rows; size of the default table; report sample.

### Open rulings

1. Size band boundaries for the weekly report (proposal: by declared
   file count 1, 2 to 3, more than 3, matching the implement escalation
   rule).
   Ruled: size bands as proposed. [review 1]
2. Cost cap source: per entry from the registry `cost_index`, or a
   per-day budget from the grant (3-1)?
   Ruled: both: `cost_index` for chain selection and the grant's per-day budget as the stop, with `wait_reason: cost_cap`. [review 1]
3. Where the outcome log lives: AMQ root (beside the hook ledger) or
   `~/.amq-squad/`? Proposal: AMQ root, per session.
   Ruled: the outcome log lives under the AMQ root per session, beside the hook ledger. [review 1]

---

## 2b-5 squad.yaml and the team rows

### Goal

Define `squad.yaml`, its location and its schema, and rewrite the v2
team model, team verb and team rules around it. Move the execution-mode
fields into squad.yaml and take the admitted argument forms from the AMQ
advertisement.

### Reads

- Plan 3.3, 3.4, 4.3 (config sentence: "`squad.yaml` compiles to one AMQ
  launch request."), 7.2 ("`squad.yaml` can only narrow it").
- R8 (digest before any write; delivered with the pi lead in 4-1, the
  digest format is defined here), R13, R16 (narrowing only).
- Amendment 20 (b): "argument forms derived upstream from the validation
  tables: section 4.3, seat-exec stops carrying its own list once
  upstream ships" (shipped in v0.106.2, PR #1175).
- 2a-2 deferred-2b rows: `internal/cli/team.go` (1,931 against 350),
  `internal/team/team.go` (1,316 against 631; "highest fan-in, do not
  delete in 2a"), `internal/cli/team_rules.go` (512 against 300),
  `internal/cli/team_rules_draft.go` (296 against 150), all "rewrite
  around squad.yaml in 2b".
- 2a-2 notes: "tool policy comes from squad.yaml at launch (replaces
  validateMemberOverlayPaths)" (ruling 1); "squad.yaml replaces the .amqrc
  source and the context candidate diagnostics" (ruling 8); "squad.yaml
  will own the execution-mode and lead-mode fields, so team init's
  --mode, --lead-mode, --control-root, --target-project-root (and the
  now-inert --target-contract) become config then" (ruling 10).
- Accepted-3 rows owned here: `internal/cli/role_draft.go`, `roles.go`,
  `model_defaults.go`, `agent_catalog.go` ("provider-argument assembly is
  R4"); `internal/cli/execution_mode.go` (shrunk; its fields move).
- 2a-3 notes, grants file format and open question 5.

### Builds

- `squad.yaml` schema 1: seats (handle, role, runtime, model, effort,
  worktree, tool policy), gates narrowing, `max_seats` (narrow only),
  `protected_paths` (add only), mode, lead mode, control root, target
  project root. Strict parse. A field that would widen a grant is a parse
  error.
- Location: `<repo root>/squad.yaml` (ruling 1 below), used by guard,
  context and the launch compiler. The task store does not follow it: it
  lives under the AMQ root (2b-1). [review 1]
- The team model (`internal/team`) as the in-memory form of squad.yaml;
  the v2 profile model, team.json pins and roster overlays go.
- The team verbs reduce to what 3.0 needs: propose (digest), apply after
  approval, show. Bare `team` prints usage.
- Team rules rendered from squad.yaml (`renderTeamRules` kept as the
  extract); the drafter seam stays one seam.
- Argument forms read from the AMQ capability advertisement; no local
  form list in `role_draft.go`, `model_defaults.go` or `agent_catalog.go`.
- The grants narrowing call goes through 3-1's function when it exists;
  until then the 2a-3 provisional reader stays.

### Replaces or deletes

- `internal/cli/team.go`, `internal/team/team.go`,
  `internal/cli/team_rules.go`, `internal/cli/team_rules_draft.go`
  (deferred-2b): each ends `shrunk` at or under target, or `deleted`
  with `new` rows.
- `internal/cli/context_resolution.go`: the `.amqrc` source and the
  candidate diagnostics go; if 2b-2 handed the row on as `deferred-2b`,
  it ends `shrunk` here.
- `internal/cli/role_draft.go`, `roles.go`, `model_defaults.go`,
  `agent_catalog.go` (accepted-3): `shrunk` at target (150, 80, 130,
  130).
- `internal/cli/execution_mode.go`: stays `shrunk` or goes `deleted` when
  its fields move into squad.yaml.

### Acceptance

1. Schema: a squad.yaml that raises `max_seats` above the owner grant, or
   removes a protected path, fails to parse with a named error.
2. The 2a-3 guard tests for squad.yaml narrowing pass unchanged against
   the new reader (fix9 row included).
3. Digest: `team propose` prints one digest for all files it would
   create and writes nothing (test asserts no file change); `apply` with
   the digest writes them.
4. Compile: a squad.yaml with one Claude, one Codex and one pi seat
   compiles to one launch request that 2b-2's compiler accepts (pi with no
   provider arguments).
5. Argument forms: a form that the fixture capability advertisement does
   not list is refused before launch.
6. Fan-in: `go build ./...` with no reference left to the v2 profile
   model (grep test on removed type names).

### Gates

The standard gate.

### Dependencies

The task schema from 2b-1's first commit only (the task store lives under
the AMQ root, so its path no longer follows the squad.yaml location). [review 1] Reads
2b-2's typed seat list; coordinates with 3-1 on the grants schema.

### Out of scope

The grants file schema (3-1). The lead that proposes squad.yaml (4-1,
4-3). `migrate` from v2 profiles (phase 5). The final form of squad.yaml
(phase 5 row: "`squad.yaml` final form").

### Agent

Opus 5.5, medium effort. One implementer, own worktree, branch `v3-2b5`.
Codex review at merge.

### Checkpoints

- Halfway: schema and location committed; acceptance 1, 2.
- End: all acceptance rows; line counts of the four deferred-2b rows
  against targets; the list of removed team verbs.

### Open rulings

1. Location (2a-3 open question 5): `<repo root>/squad.yaml` (what guard
   reads today) or `<repo root>/.amq-squad/squad.yaml`? Proposal: the
   repo root, since plan 3.3 names the file at top level and guard reads
   it there.
   Ruled: `<repo root>/squad.yaml` (E2). [review 1]
2. Mode precedence (R13: globally, per repository, by flag, by lead
   command): squad.yaml can narrow auto to manual but never widen manual
   to auto?
   Ruled: squad.yaml narrows auto to manual and never widens manual to auto. [review 1]
3. Do the R37 defaults (2a-3 open question 7) go into the squad.yaml
   template or into the grants defaults (3-1)?
   Ruled: R37 defaults live in the grants defaults (3-1); squad.yaml only adds. [review 1]

---

## 3-1 Gates, grants and the gate store written by the core

### Goal

Make the Go core write the gate record and the answer file, verify
owner-signed answers for all four typed classes, and define the grants
schema that `guard`, the scheduler and squad.yaml read. Close the 2a-3
gaps: credential_change in the taxonomy, the thread re-ask, the
`nostr:<eventID>` path, target-string conventions in one place.

### Reads

- Plan 7.1, 7.2, 7.3 (gate answer, thread, prefix, conformance, guarded
  paths bullets), 9 (secret rule, thread rule, label).
- R16, R30, R31, R32, R33 (the nsec rule), R37.
- Binding text (7.3): "The Go core writes the gate record under the AMQ
  root before it sends the question. The record holds the full target
  digest and is immutable. The verifier compares the typed prefix against
  that record only." "When the core sees a non-thread answer that matches
  an open gate (gate id, action class, prefix, owner pubkey), it re-posts
  'reply here to confirm' in the gate thread, and the next thread reply
  is the answer." "Verifier conformance rules: an explicit s < n check on
  the Schnorr signature; strict event decoding that rejects invalid
  UTF-8, duplicate keys and null tag elements; a presentation window of
  created_at - 60 s <= now <= not_after; CR and LF rejected anywhere in
  the answer; the envelope written first, then the consumed mark, both
  fsynced."
- Amendment 19 (re-ask), amendment 20 (e): "signed event carried as a
  `nostr:<eventID>` label, the layer verifies against the relay with the
  Claude-hook checks" (AMQ #1155 part 1 shipped in v0.106.0, PR #1168).
- 2a-3 notes: "Accepted gate answer", grants file format, open questions
  1, 4, 5, 7, 8, 9, 10. Rules inherited from the fix commit:
  "acceptance is terminal (a record's `not_after` governs only an
  unanswered gate [...])", and "gate ids follow operatorauth's grammar,
  including ids with a leading dot".
- 2a-2 ruling 6: "a local owner answer without Buzz, if ever wanted, is a
  phase 3 design decision; it will not be pane identity."
- S1 report sections 10 and 12 (history check, signed deny record, gate
  window, relay trust); reference code `spike/gateverify/`.
- Accepted-3 rows owned here: `internal/cli/verify_action.go`,
  `verify_authorization.go`, `verify_rebind.go` ("guard ancestor; keep
  decision flow and signed auth"), `internal/commandevidence/subject.go`
  ("kept as guard target-digest helper"). Shared row:
  `internal/cli/operator.go` (gate raise and answer parts; 4-3 owns the
  rest and the final status).

### Builds

- Gate store writer (new package, for example `internal/gatestore`):
  `gates/<id>.json` written O_EXCL under `<root>/extensions/amq-squad/`
  before the question is sent, immutable, with `raised_by`, action class,
  full target digest, target string, `not_after`; `answers/<id>.json`
  with the verified operatorauth envelope; envelope first, consumed mark
  second, both fsynced. The 2a-3 readers (`context`, `guard`) read it
  unchanged.
- Target-string table (one place): the conventions of 2a-3 open question
  9 (`git tag v1`, `gh release create v1`, `<mcp tool> <recipient>`,
  `grow <root> seats=N`, `write <path>`) and the digest formula; guard,
  the writer and the verifier all call it.
- Taxonomy: `credential_change` added to operatorauth so the credential
  gate can verify; a signed deny record (S1 open question 5).
- Verifier path for the four classes: the S1 verifier ported with the
  conformance rules, thread binding, the history check (edits and
  deletions of the question), the re-ask in the gate thread, and the
  `nostr:<eventID>` label path (read the event from the relay, run the
  Claude-hook checks).
- Grants schema 1 (`~/.amq-squad/grants.yaml`): mode, `max_seats`
  (default 12), cost per day, permitted runtimes, permitted privacy
  class, protected paths, default branch, the per-repository merge grant
  (repository, base branch, CI required, size cap; no force push and no
  protected-path change are fixed), fleet grant fields reserved for 4-3.
  One narrowing function that squad.yaml (2b-5) calls.
- Grants file mode: the guard behaviour per ruling 1.
- R37 defaults per ruling 3.
- The growth gate wired to the scheduler's seat request past the cap and
  to 2b-2's widening past a grant limit.

### Replaces or deletes

- `internal/cli/verify_action.go`, `verify_authorization.go`,
  `verify_rebind.go`: the decision flow moves onto the gate store; each
  ends `shrunk` at target (700, 300, 100) or `deleted`.
- `internal/commandevidence/subject.go`: becomes the target-string table
  or is folded into it; ends `shrunk` (400) or `deleted`.
- `internal/cli/operator.go`: gate raise and answer move to the gate
  store; the row stays accepted-3 for 4-3, with the moved line count
  recorded.
- `internal/cli/hook_store.go` (status new): the provisional grants
  reader is replaced by the schema 1 reader; the row stays `new` with its
  new size.

### Acceptance

1. Write-before-send: a test kills the process between record write and
   send; the record exists, no question was sent, and a second raise with
   the same id fails O_EXCL.
2. Each of the four classes: approve, deny, replay (`already_consumed`),
   wrong digest (`gate_mismatch`), wrong key (`wrong_pubkey`), forged
   signature, all through the core, on synthetic keys.
3. Conformance: s >= n, invalid UTF-8, duplicate keys, null tag element,
   CR or LF in the answer, created_at more than 60 s in the future, each
   rejected.
4. Thread: a non-thread answer that matches an open gate gets one re-ask
   in the thread and is not accepted; the next thread reply is accepted
   (the g-owner-01 and g-owner-02 cases replayed from the S1 evidence).
5. Label: a mailbox prompt with `nostr:<eventID>` resolves, fetches and
   verifies against a test relay; a label for an event with a different
   gate e-tag is rejected.
6. Acceptance-final: a verified answer past the record's `not_after`
   stays accepted; a dot-led gate id verifies (2a-3 fix12 and fix13 rows
   pass unchanged).
7. credential_change: a `gh auth login` guard verdict turns to allow
   after a verified credential_change answer.
8. Grants: unknown key denies `grants_invalid`; squad.yaml cannot widen;
   the insecure-mode behaviour matches ruling 1.
9. Owner run (one, owner present, real Buzz key through the password
   manager pipe only): one gate of class tag answered from Buzz in the
   thread, verified by the core, envelope written. Time from post to
   envelope recorded. This run shares one owner session of about 15 minutes with 4-5's acceptance 4, held at 4-5's end; 3-1 merges with this item recorded as pending that session, since S1 already proved the real-key path. [review 1]
10. No model process holds the owner key: a grep test that no package
    under internal/ reads an nsec.

### Gates

The standard gate, plus `go test -race` on the gate store and verifier
packages.

### Dependencies

None. Starts now. Coordinates with 2b-5 (narrowing function) and 2b-2
(growth gate on widening).

### Out of scope

The merge grant enforcement at merge time (3-2; this brief defines its
fields). Buzz enrollment and attachment to the operator session (4-5).
The plugin's needs_gate mapping (4-2). Phone-side signing (upstream ask
10).

### Agent

Opus 5.5, medium effort. One implementer, own worktree, branch `v3-3a`.
Codex review at merge.

### Checkpoints

- Halfway: gate store writer and target-string table committed;
  acceptance 1, 2, 3, 6.
- End: all acceptance rows; the owner run record; manifest statuses of
  the Replaces rows; verifier test count with `-race`.

### Open rulings

1. Insecure grants file (2a-3 open question 4): ignore with
   `owner_grants_insecure` (today) or fail closed and deny every
   typed-gate class? Proposal: fail closed.
   Ruled: fail closed. The file counts as present but untrusted: no new seat starts (every seat request is a growth gate), its protected paths still apply, typed gates stay needs_gate with reason code `owner_grants_insecure`, and doctor (2b-3) reports the mode. [review 1]
2. Trust store path: `~/.amq-squad/authorization-trust.json` (2a-3) or
   another path; `verify` takes `--trust-store` today.
   Ruled: the trust store stays `~/.amq-squad/authorization-trust.json`. [review 1]
3. R37 defaults (2a-3 open question 7): add team-rules.md, the managed
   blocks of CLAUDE.md and AGENTS.md, and `.claude/settings*.json` to the
   implicit protected paths? Proposal: yes.
   Ruled: R37 is binding. The implicit protected set is team-rules.md, the managed blocks of CLAUDE.md and AGENTS.md, `.claude/settings*.json`, the hook plugin files, and `squad.yaml` itself (the lead writes it through the digest flow; a seat must not). [review 1]
4. External send coverage (2a-3 open question 10): add `curl -X POST`
   and similar, or accept the name heuristic for 3.0?
   Ruled: curl and similar stay outside external_send for 3.0; the gate is for messages to people, not API calls. [review 1]
5. Default gate window (S1 open question 6): 30 minutes?
   Ruled: 30 minutes, as a proposal. [review 1]
6. Local owner answer without Buzz (2a-2 ruling 6): in 3.0 or not?
   Ruled: no local owner answer without Buzz in 3.0 (2a-2 ruling 6 stands). [review 1]
7. Owner time for acceptance 9: one session of about 15 minutes.
   Ruled: acceptance 9 shares one owner session of about 15 minutes with 4-5's acceptance 4, at 4-5's end; 3-1 merges with it recorded as pending that session, since S1 already proved the real-key path. [review 1]

---

## 3-2 Merge train, branch protection and the release flow

### Goal

Land seat PRs through a deterministic merge train under the standing
per-repository merge grant, with the Go core enforcing the grant's
limits, and check branch protection and credential custody. Build the
release gate flow on the compoundrelease writer.

### Reads

- Plan 7.2 (merge grant), 7.3 (branch protection, Codex sandbox, merge
  train bullets), 14 (merge queue not available), 16.1 (compoundrelease
  stays).
- R30, R33 (seats hold no default-branch or release credentials), R34.
- Binding text (7.2): "The Go core enforces the grant's limits
  (repository, base branch, CI green, size cap, no force push, no
  protected-path change). The lead decides inside them. A merge outside
  the grant is refused by the core, not by the lead's judgment, and that
  refusal escalates to an owner-signed gate." (7.3): "The batch digest
  over the ordered (PR, head SHA) pairs plus the base SHA is still
  computed and written to the audit log; nobody signs it. A rebase that
  changes a tree recomputes the digest and re-runs the rules check."
- Decision log, 2026-10-22, S6 draft PRs: "the 18 open draft PRs on
  amq-noc and workspace-cli stay open as the first real input of the
  phase 2b merge train: 16 clean, two predicted pairs that need ordering
  (W3/W7, W5/W12), one that fails the task file-set rule (W5). No manual
  merge, no closing."
- 2a-2 deferred-3 rows `internal/compoundrelease/store.go`,
  `reconcile.go` (ruling 9: "the compound release writer (store,
  reconcile, transition) is the release gate flow's ancestor"); the
  kept-dead writer functions listed in the 2a-2 extracts table.
- 2a-2 deferred-replace rows `internal/cli/evidence.go` and
  `internal/commandevidence/evidence.go` ("replaced by evidence ledger").
- Accepted-3 row `internal/cli/verify.go` ("live merge/release evidence
  (2b merge train)").
- S2 report (CI-only ruleset, GH013, merge_queue 422).

### Builds

- `internal/mergetrain` (new): input an ordered PR set from the lead;
  per PR, check repository, base branch, CI green, size cap, no force
  push, no protected-path change, and the task scope (the PR's diff
  against the task's `declared_files`, as at the commit step); compute
  the batch digest; write the audit log entry; merge in order under the
  owner's identity with `gh`; on a rebase that changes a tree, recompute
  and re-check. A refusal goes to the lead and raises an owner-signed
  gate through 3-1.
- Manual mode keeps the per-merge stop.
- Branch protection check: before the first train run on a repository,
  verify that the server requires CI and refuses direct push; refuse to
  run otherwise.
- Credential custody: seat environments carry no GitHub or release
  token (2b-2's scrub, verified here); the harness push and the train use
  the owner's identity outside the seat process.
- Codex sandbox defaults: the launch refuses a Codex seat without
  `--sandbox workspace-write` and `--ask-for-approval on-request`.
- Release gate flow: tag and release go through the typed gate (3-1),
  then the compoundrelease writer creates the release.
- Evidence for merge and release reads the hook ledger and the outcome
  log instead of the v2 evidence collect.

### Replaces or deletes

- `internal/compoundrelease/store.go`, `reconcile.go` (deferred-3): the
  writer paths become live in the release flow; the rows end `shrunk` at
  or under target (1,385, 452) or stay at their size as `keep` by lead
  ruling, never deferred-3.
- `internal/cli/evidence.go`, `internal/commandevidence/evidence.go`
  (deferred-replace): `deleted`, with their redaction and task-link
  semantics (`evidenceSecretName`, `evidenceTaskLink`) moved into the
  ledger readers.
- `internal/cli/verify.go` (accepted-3): `shrunk` at 500 or `deleted`.

### Acceptance

1. Fixture train on a local bare repo with a fake `gh`: a clean set of
   five PRs lands in order; the audit log holds the batch digest; a
   rebase that changes a tree changes the digest and re-runs the check.
2. Refusals, one test each: CI red, size cap, force push, protected path,
   wrong base, file outside the task. Each refusal raises a gate request
   and merges nothing.
3. S6 input, dry run first: the train computes the plan for the 18 open
   draft PRs on amq-noc and workspace-cli: 16 clean, W3/W7 and W5/W12
   ordered, W5 refused on the task file-set rule. The output matches the
   S6 report's prediction.
4. S6 input, real run, only after ruling 1: the clean PRs land under the
   owner's identity through the CI-only ruleset; a direct push to `main`
   is refused (GH013).
5. Branch protection: a fixture repo with no ruleset makes the train
   refuse to start.
6. Release flow on a fixture repo: tag without a verified answer is
   refused; with one, the release is created by the writer.
7. Manifest: `scripts/manifest-check.sh --final` shows no deferred-3 and
   no deferred-replace row.

### Gates

The standard gate, plus the dry-run output of acceptance 3 attached to
the execution log.

### Dependencies

3-1 (gate store, merge grant fields) and 2b-2 (commit step, launch
record, seat branches) merged. Reads 2b-1's `declared_files`.

### Out of scope

GitHub merge queue (not available). Signing the batch digest (dropped by
owner decision). The release of 3.0 itself (phase 5).

### Agent

Opus 5.5, medium effort. One implementer, own worktree, branch `v3-3b`.
Codex review at merge.

### Checkpoints

- Halfway: train with acceptance 1, 2, 5; the S6 dry run (acceptance 3).
- End: all acceptance rows; manifest statuses; train wall time for the
  S6 set.

### Open rulings

1. Does the train merge the 16 clean S6 PRs for real (owner repos,
   owner identity), or does the dry run close the S6 input? This needs a
   merge grant in the owner's grants file for amq-noc and workspace-cli.
2. Size cap default (shared with 2b-2's commit step).
3. Credential custody on a single-user Mac: seats run as the owner's UID
   and can reach the `gh` keychain entry. Is the scrub plus the guarded
   credential paths enough for 3.0 (same-UID threat accepted in 7.3), or
   is a separate keychain needed?
4. The S6 PRs are tasks of the v2 store, with no `declared_files`. Use
   the S6 task list (`docs/v3-spike/S6-tasklist.md`) as their declared
   file sets?

---

## 4-1 Built-in pi package

### Goal

Embed the pinned pi runtime in the amq-squad binary and ship the
`@amq-squad/pi` package that the lead and every pi seat load: hooks for
the three verbs, native AMQ tools, role delivery, and the model list
restriction. Deliver solo first run and the digest-before-write rule
with it.

### Reads

- Plan 3.1, 3.2, 4.1 in full, 4.4 (built-in pi row and hold-seam
  findings), 16.3.
- R1, R2, R7, R8, R14, R15, R35 and R36 for pi.
- Amendment 11 (native tools, role in first message and system prompt,
  dispatch by leases), amendment 23 (pi launches through `amq launch`;
  "the embedded pi version is the one upstream verifies"; upstream
  refuses pi below 1.2.0 or a non-release build; embedded 1.3.0 passes).
- Binding text (4.1): "The lead and pi seats use this copy only, never a
  pi from PATH." "It puts the role text in the first message and also in
  the system prompt. The appended system prompt alone failed in the
  spike". (4.4): "`before_agent_start` injection, several held calls in
  parallel, and a local confirm racing a remote answer are phase 2b
  tests."
- S3 report (packaging option (a), strip, fd and ripgrep pins, launch
  integrity, 43.5 ms warm, 190 ms full re-hash, open questions 1 to 4);
  S5 report (hold seam, lead loop, open questions for phase 4).
- 2a-3 notes: the three verbs' inputs and outputs (pi payload names
  `toolName`, `input`, `toolCallId`).
- Reference code: `spike/embedpi/`, `spike/pi-hold-ext/`,
  `spike/pi-lead-loop/`, `spike/pi-config/`.

### Builds

- Runtime embedding (Go): Node 22 plus the pinned npm tree, fetched once
  with a digest pin, stripped; unpack to `~/.amq-squad/runtime/<version>/`;
  cold-install manifest; launch-time integrity check; fd and ripgrep in
  the managed agent-bin; `PI_OFFLINE=1`, `PI_SKIP_VERSION_CHECK=1`,
  `PI_TELEMETRY=0`, per-seat `PI_CODING_AGENT_DIR`.
- `@amq-squad/pi` package (TypeScript, in the repo under a new
  directory, lockfile committed): `before_agent_start` calls `context`;
  `tool_call` calls `guard` and fails closed; `tool_result` calls
  `evidence ingest`; native `amq_send`, `amq_drain`, `amq_reply`; role in
  the first message and the system prompt; model list restricted to the
  seat's registry entry; tool shell without the owner's profile.
- Bridge revision 4 (tool approval and session identity), per 16.3.
- Solo first run (R7): no `.amq-squad/` writes in the repository; writes
  only under `~/.amq-squad/`. Digest before write (R8) for the first team
  proposal, using 2b-5's digest format.
- Auto and manual mode behaviour of the lead (R14, R15): the lead's
  choice of runtime, model and effort is checked by the deterministic
  filter against the grant.
- Hold-seam tests: `before_agent_start` injection, several held calls in
  parallel, a local confirm racing a remote answer; an approval-retry cap
  (S5 open question 4).

### Replaces or deletes

No ledger row. New Go rows with fate `new`. The TypeScript package is
outside the manifest.

### Acceptance

1. Criterion 3 re-run from the v3 binary: darwin-arm64 build starts the
   lead from the unpacked copy with no pi or Node on PATH.
2. Integrity: a changed byte in an executable under the runtime makes
   the launch refuse; a symlink on the entrypoint path makes it refuse;
   warm launch overhead median under 60 ms (spike: 43.5 ms).
3. Guard fails closed: a guard exit 1, a timeout and a missing binary
   each block the tool call in pi.
4. Native tools: a pi seat sends, drains and replies with no bash call
   to `amq`.
5. Role: in 10 runs the seat acknowledges its role and names AMQ
   correctly (not "Advanced Message Queuing").
6. Model restriction: with `GEMINI_API_KEY` set in the parent shell, the
   seat lists only its registry entry's models.
7. R7: a first run in a fresh git repo leaves `git status` clean and
   writes only under `~/.amq-squad/`.
8. R19 loop test: a pi lead given a three-task brief does not start
   coding; it decomposes, labels classes and converges through the
   scheduler (2 of 2 runs, as in S5).
9. Hold seam: the three 4.4 tests pass; the retry cap stops a storm.
10. pi launch through `amq launch` with the embedded runtime, on the AMQ
    release after v0.107.0.

### Gates

The standard gate, plus the package's own lint and test, and a darwin
arm64 build with the payload. Linux and windows Go builds must compile
with the payload absent (build tag or stub).

### Dependencies

The 2a verbs (merged). Starts now. Acceptance 8 needs 2b-1 and 2b-4;
acceptance 10 needs 2b-2. Run those last.

### Out of scope

Signed and notarized launcher (phase 5). darwin amd64 bundle (phase 5,
16.4). Uninstall (phase 5). The mayor (4-3).

### Agent

Opus 5.5, medium effort. One implementer, own worktree, branch `v3-4a`.
Codex review at merge.

### Checkpoints

- Halfway: embedding and integrity (acceptance 1, 2); package hooks
  (acceptance 3).
- End: all acceptance rows; binary size; cold start to first token; warm
  start; unpacked file count against S3 (59.3 MB, 179 MB, 6,432 files).

### Open rulings

1. pi pin: 1.3.0 (S5) is above upstream's 1.2.0 floor. Keep 1.3.0 or
   move to the version upstream verified in PR #1188?
   Ruled: keep pi 1.3.0 unless PR 1188's verified version is newer (above the 1.2.0 floor). [review 1]
2. Where the TypeScript package lives (for example `pi/` at the repo
   root) and how CI builds it before phase 5's package CI.
   Ruled: `pi/` at the repo root with its lockfile. [review 1]
3. Repair and garbage collection of old runtime versions (S3 open
   question 1).
   Ruled: keep the last two runtime versions; `doctor --fix` removes older ones. [review 1]
4. The hold-seam tests are "phase 2b tests" in 4.4 but need the pi
   package. This brief runs them; confirm.
   Ruled: the hold-seam tests run in 4-1. [review 1]

---

## 4-2 Claude Code hooks plugin

### Goal

Ship the user-scope Claude Code plugin that calls the three verbs on
every turn and tool call of a Claude seat. Map the guard verdicts to the
hook decision so that an unattended seat never waits on a prompt.

### Reads

- Plan 4.3 ("The Claude Code hooks come from a user-scope plugin that
  reads the seat environment; the Claude grammar has no plugin or
  settings flag. The hook ships as that user-scope plugin, never as a
  project-scope stub."), 4.4 (Claude Code row and the S4 fault table).
- R35, R36 ("Claude Code PreToolUse fails open on timeout, on non-zero
  exit without a deny, and on a missing command, a timeout costs exactly
  the configured timeout, and the docs say so").
- 2a-3 notes: the three verbs' contracts (inputs, guard output JSON, exit
  codes: guard 0 for every verdict and 1 on error, never 2; context and
  ingest 2 on a usage or identity error), and open question 9: "the
  plugin denies with 'raise the gate', and the lead raises it."
- Timings to keep: guard p95 8.7 ms, context 6.9 ms, ingest 10.4 ms
  (2a-3, built binary).

### Builds

- A plugin under `plugins/claude/` (hooks only; no skill changes in this
  brief), installed at user scope: SessionStart and UserPromptSubmit call
  `context --me $AM_ME --json` and inject the result; PreToolUse calls
  `guard --me $AM_ME --stdin`; PostToolUse calls `evidence ingest --me
  $AM_ME`.
- The verdict mapping: allow to allow; deny to deny with the reason;
  needs_gate to deny with a reason built from `action_class` and `target`
  ("raise the gate for <class> <target>"); guard exit 1 to no decision
  (fail open, as Claude Code does), logged.
- Outside a seat (no `AM_ME`), every hook exits 0 with no output.
- Hook timeout value and the docs section that states the fail-open
  cases with the S4 numbers.

### Replaces or deletes

No ledger row. Plugin files are outside the manifest.

### Acceptance

1. Mapping table test: for each guard reason code of 2a-3, the plugin's
   output decision matches the table.
2. needs_gate never maps to ask: a `git tag v1` call in a seat without an
   answer is denied with the gate reason, and the seat does not stop on a
   prompt.
3. Fault table re-run (S4): deny blocks; a timeout fails open after the
   configured timeout (measured); exit 1 fails open; a missing command
   fails open. Times recorded against S4 (0.17 s, 5.11 s, 0.36 s,
   0.44 s).
4. Context injection: SessionStart output carries role digest, rules
   digest, current task and open gates for a seeded seat.
5. Evidence: 20 tool calls give 20 ledger lines with seq 1 to 20.
6. Outside a seat: the hooks add under 20 ms per call and print nothing.
7. Install at user scope only: no file under the project's `.claude/`.

### Gates

The standard gate, plus the plugin manifest check that the repo's
`make ci` runs for plugins.

### Dependencies

2b-2 (seat environment from the launch record) and 3-1 (gate store, so
that needs_gate can turn to allow after an answer) merged. The 2a verbs
are merged.

### Out of scope

Codex, Grok Build and Cursor hooks (none in session). The pi hooks
(4-1). Plugin marketplace changes on `main` (main stays frozen at v2.31).

### Agent

Sonnet 5.5, medium effort (plugin work). One implementer, own worktree,
branch `v3-4b`. Codex review at merge.

### Checkpoints

- Halfway: mapping and acceptance 1, 2, 6.
- End: all acceptance rows; the fault table with measured times.

### Open rulings

1. Hook timeout value (proposal: 5 s, as in the S4 run).
   Ruled: 5 s, as a proposal. [review 1]
2. Does UserPromptSubmit inject the full context every turn or only on a
   digest change (R35 says every turn)?
   Ruled: every turn; R35 is binding. [review 1]

---

## 4-3 Operator session and mayor

### Goal

Run one operator session for the whole fleet: the same program as a
squad lead, with a fleet grant. In auto mode it is the mayor and starts
squads inside the fleet grant; it never approves a typed gate.

### Reads

- Plan 3.5, 3.6, 7.2 (fleet grant fields), 8 (the mayor partitions work
  by repository), 16.3 (`status --fleet --json` reads
  `~/.amq-squad/fleet.yaml`), 16.5.
- R38, R39, R40, R41, R42 (the operator side), R44 (the fleet input that
  amq-noc reads).
- Binding text (3.5): "The operator session and a squad lead are the
  same program with different grants. This is a design constraint, not
  an option." "The mayor never approves a typed gate. Only the owner
  does, with a signature." R40: "where a new project root needs `amq
  launch` to provision an unknown session (upstream ask 2) the fallback
  is: the mayor proposes, the owner starts."
- Kept-dead extract `internal/cli/global_noc.go` (the fleet registry
  read and write; "`status --fleet` reading fleet.yaml (plan 16.3)").
- Accepted-3 row `internal/cli/operator.go` ("keep gate raise/answer and
  attention projection"; the gate parts moved in 3-1).

### Builds

- Fleet registry `~/.amq-squad/fleet.yaml`: each squad, its root, its
  lead handle; from the global_noc.go extract, renamed.
- `amq-squad status --fleet --json`: the 2b-1 contract per squad plus a
  fleet header; the only fleet input for amq-noc.
- Operator mailbox: every squad lead sends gates and status to it
  through AMQ cross-project routing.
- Manual mode: relay, answer lead questions, group open gates into one
  list.
- Auto mode (the mayor): start a squad inside the fleet grant (maximum
  squads, cost per day, permitted repositories); write its squad.yaml
  through the digest flow; provision the roster through the launch API
  package; the ask 2 fallback.
- No code path where the mayor answers a typed gate; the verifier
  rejects any answer not signed by the owner key (3-1).

### Replaces or deletes

- `internal/cli/global_noc.go`: renamed into the fleet registry; ends
  `shrunk` (265) or `deleted` with a `new` row.
- `internal/cli/operator.go` (accepted-3): ends `shrunk` at 600 or
  `deleted`.

### Acceptance

1. Same program: one test starts a squad lead and an operator from the
   same entrypoint with two grant fixtures; the only difference in their
   config is the grant.
2. Fleet status: three fixture squads give one `status --fleet --json`
   that validates against the fleet golden fixture.
3. Gate grouping: open gates from two squads appear in one list with
   their squad.
4. Mayor inside the grant: with `max_squads` 2, the third start waits;
   a repository not in the permitted list is refused.
5. Mayor and typed gates: a mayor-produced answer for any typed gate is
   rejected by the core (test with the mayor's process holding no owner
   key).
6. Ask 2 fallback: a new root that needs provisioning gives a proposal
   for the owner, not a launch.

### Gates

The standard gate.

### Dependencies

2b-1 merged (contract). Reads 3-1's grant schema for the fleet fields;
if 3-1 is not merged, code against its committed schema. The live
operator run waits for 4-1 (16.5 puts the lead and the mayor on the built-in
pi); Go-level acceptance items 1 to 6 do not. [review 1]

### Out of scope

Buzz attachment (4-5). The amq-noc renderer (4-4). Cost accounting per
day beyond what the grant field checks.

### Agent

Opus 5.5, medium effort. One implementer, own worktree, branch `v3-4c`.
Codex review at merge.

### Checkpoints

- Halfway: fleet.yaml, `status --fleet --json`, acceptance 1, 2.
- End: all acceptance rows; operator.go line count.

### Open rulings

1. Default model for the operator and the lead on API keys (plan 14:
   "name their default model in the registry").
   Ruled: the registry's named default model (plan 14, 16.5). [review 1]
2. Does the operator session run in the built-in pi only (16.5), which
   makes acceptance 1 depend on 4-1?
   Ruled: yes for the live run, which depends on 4-1 (16.5 puts the lead and the mayor on the built-in pi); Go-level acceptance items 1 to 6 do not. "4-3 live run after 4-1" is in the dependency graph. [review 1]

---

## 4-4 amq-noc 1.0 command center

### Goal

Make amq-noc 1.0 a command center in the shape of the reference
screenshot: one list of every seat and task across projects, four status
buckets, a details pane, and keyboard navigation. It replaces the old
board, and the renderer reads only the 2b status JSON.

### Reads

- Plan 4.7, 3.6, 16.3; R44 ("amq-noc reads that contract and AMQ
  primitives only").
- Binding text (4.7): "It never reads `.amq-squad/` files and never
  composes amq-squad commands. Its controls are three: copy the action
  from the contract, open a conversation with a lead over the AMQ
  thread, and show a gate."
- Amendment 24 in full and the reference screenshot
  `docs/v3-research/codex-agent-command-center-2026-10-24.png`. Owner
  decision: "the command center replaces the current amq-noc board, and
  the old board view is removed, not kept beside it; amq-noc 1.0 ships the
  command center as its only view."
- 2b-1's contract and golden fixture; 4-3's `status --fleet --json`.

### Builds

In the amq-noc repository (own worktree there):

- One input: `amq-squad status --fleet --json` (or `status --json` for
  one squad), parsed against the vendored golden fixture.
- List view: rows grouped by project path with a count per group; one
  row per task with title, status and relative updated time; seats with
  no task as their own rows; a "show more" fold for long groups.
- Tabs with counts: All, Needs you, Working, Ready, Inactive; tab and
  shift-tab cycle them. Mapping (amendment 24): Needs you = open typed
  gates and refusals routed to the owner; Working = a task with a live
  lease; Ready = a seat with no task and a live lease; Inactive = a lease
  that expired or a session that ended.
- Details pane: status, model, reasoning effort, project path, tokens in
  and out, last message, prompt state.
- Keys: arrows move, enter opens, n new, esc back, ? help, tab and
  shift-tab filter.
- Delete the old board view and its code.

### Replaces or deletes

In amq-noc: the old board view and the code only it uses (candidates:
`internal/console/` board files, `internal/cli/status_board.go`; the
exact list is the first checkpoint item). The classifier and attention
queue code that 2b-1 ported into amq-squad is deleted from amq-noc. No
amq-squad ledger row.

### Acceptance

1. The renderer reads no file under `.amq-squad/` and runs no amq-squad
   command other than the status call (a test with a fake PATH and a
   file-access trace).
2. Golden: the vendored fixture renders to a committed text snapshot at
   120 columns and at 80 columns.
3. Buckets: a fixture with one item per bucket shows the right counts in
   each tab; All equals the sum.
4. Grouping: the fixture with 8 projects groups and counts as the
   screenshot does; a group over the fold shows "show more".
5. Details pane: every field of amendment 24 shown; a null token count
   shows a dash.
6. Keys: each key has a test.
7. Old board gone: no file of the old board view remains; `amq-noc`
   starts in the command center.
8. Refresh: 50 seats and 200 tasks render in under 100 ms per frame.

### Gates

amq-noc's own gate (`make ci` there), plus linux and windows builds.
The vendored fixture matches amq-squad's committed fixture byte for byte.

### Dependencies

2b-1's status contract merged. `status --fleet --json` from 4-3 for the
fleet view; one-squad mode works without it.

### Out of scope

Gate answers in amq-noc (they follow the signed path, 4-5). Any write to
amq-squad state. Pane control (R6).

### Agent

Sonnet 5.5, medium effort (renderer work). One implementer, own worktree
in the amq-noc repository, branch `v1-command-center`. Codex review at
merge.

### Checkpoints

- Halfway: the delete list for the old board; list view with buckets
  (acceptance 2, 3, 4).
- End: all acceptance rows; screenshot of the command center on a live
  squad next to the reference.

### Open rulings

1. "n new dispatches a task through the kept `dispatch` verb"
   (amendment 24) against "never composes amq-squad commands" (4.7).
   Proposal: the contract carries a "new task" action as argv plus an
   action id, and amq-noc runs the argv from the contract.
2. "enter opens": open the details pane, the AMQ conversation with the
   lead, or the seat's tmux pane? Proposal: the AMQ conversation; amq-noc
   does not focus panes.
3. Five states to four buckets: where blocked and waiting go (proposal:
   blocked to Needs you only when routed to the owner, else Working;
   waiting to Working).
4. Version: amq-noc 1.0 ships with amq-squad 3.0 or before it?
   Ruled: amq-noc 1.0 ships in the same release window as 3.0; phase 5 owns both releases; lead rulings 6 and 7 stand. [review 1]

---

## 4-5 Buzz attachment

### Goal

Attach one Buzz agent to the operator session through `amq-remote`, so
that one phone shows presence, activity and approvals for the fleet and
one message reaches any lead. Make the S1 enrollment and thread lessons
the defaults.

### Reads

- Plan 3.6 (Buzz bullet), 7.3 (gate answers and thread bullets), 9 in
  full.
- R32 (thread rule, label), R33 (nsec rule), R42.
- Amendment 19: "(a) `--enable buzz-profile --presence` is the
  enrollment default, one extra owner signature; (b) the gate text says
  'reply in this thread'; (c) thread binding stays on". Amendment 20 (e)
  (`nostr:<eventID>` label; AMQ #1155 part 1 in v0.106.0; part 2, the pi
  bridge answer file, is AMQ #1178, open).
- Secret rule (9): "the nsec reaches the signer only through a
  password-manager pipe into `amq-owner-sign`. The same command with its
  output on a terminal, in a log or in a file is not allowed."
- S1 report sections 7, 8 and 12 (enrollment dry run, owner run, the
  `printf` fault, the profile fix); `spike/gateverify/enroll.sh`.
- AMQ v0.107.0 #1179 (Claude tool-call approval with the owner's check
  mark in Buzz Desktop).

### Builds

- `amq-squad remote enroll` (or a documented owner script): enrollment
  with `--enable buzz-profile --presence`, the nsec only through a
  password-manager pipe, nothing printed.
- Attachment: the Buzz agent binds to the operator session's mailbox;
  presence and activity from the fleet status; gate questions from the
  3-1 gate store carry "reply in this thread".
- Routing: a phone message to the operator reaches the named lead
  through AMQ.
- doctor (2b-3) warning when the `amq-remote` login service is absent
  (plan 8), wired here if 2b-3 left a stub.
- Docs: owner steps, the secret rule, the box recipe pointer.

### Replaces or deletes

No ledger row.

### Acceptance

1. Enrollment dry run with a throwaway key: profile and presence on by
   default; grant count recorded (S1: 8 grants with the profile).
2. No secret output: the enroll path writes no nsec to stdout, stderr,
   a log or a file (test with a canary key and a grep of every output).
3. Gate text contains "reply in this thread".
4. Owner run (owner present, real key): one typed gate answered from the
   phone in the thread, verified by the core (3-1), and one plain message
   from the phone routed to a named lead. Times recorded. This run shares one owner session of about 15 minutes with 3-1's acceptance 9, held at 4-5's end; 3-1 merges with its acceptance 9 recorded as pending that session. [review 1]
5. Presence: the Buzz profile shows the operator session as present
   while it runs and not present after it stops.

### Gates

The standard gate.

### Dependencies

3-1 merged (gate store, verifier, re-ask). 4-3 for the operator session;
until it merges, attach to a single squad lead.

### Out of scope

Phone-side signing (upstream ask 10). Grok Bot and Slack ring adapters
(plan 9). SagiMedina/handoff (docs recipe only).

### Agent

Opus 5.5, medium effort (the enrollment path handles the owner key; lead ruling 13, roster ruling). [review 1] One implementer, own worktree, branch `v3-4e`. Codex review at merge.

### Checkpoints

- Halfway: enrollment and acceptance 1, 2, 3.
- End: owner run record (acceptance 4, 5).

### Open rulings

1. A command in the binary for enrollment, or an owner script in docs?
   Ruled: an owner script in docs for 3.0, with the S1 enroll.sh made the default; a binary command is phase 5. [review 1]
2. Grant window at enrollment (S1: 30 days). Keep?
   Ruled: keep 30 days. [review 1]
3. Owner time for acceptance 4: one session of about 15 minutes; can it
   share the session with 3-1's acceptance 9?
   Ruled: yes, shared with 3-1's acceptance 9: one owner session of about 15 minutes at 4-5's end. [review 1]

## Lead rulings on the drafter's open points (2026-10-24)

1. 2b-2 runs as two milestones with one implementer in one worktree: milestone A (seat-exec, launch record, identity at launch) at Opus medium; milestone B (the harness-side commit step with the file-set check and the lead widening path) at Opus high, reason: it is the enforcement point of amendment 21 and a mistake there is a silent loss of seat work.
2. Hold-seam tests live in 4-1 (they need the pi package); the role-delivery matrix in 2b-2. Accepted.
3. Row and work-item owners as proposed. Accepted.
4. Rows no brief retires (workstream.go, briefs_seed.go, drafter/run.go, drafter/config.go) are assigned to phase 5 explicitly (brief drafting is an off-path verb); their manifest note says "phase 5".
5. Status contract schema 1 carries task title, tokens in and out, last message and prompt state; token counts may be null when a runtime does not report them. Accepted.
6. amq-noc: the status contract carries the "new task" action as argv so the renderer never composes a command; enter opens the AMQ conversation, not the tmux pane; the five classifier states map to the four buckets by a table written in 2b-1 and read by 4-4 (needs_you = open gate or refusal routed to the owner; working = live lease with a task; ready = live lease, no task; inactive = expired lease or ended session; the fifth state, unknown, renders as inactive with a marker).
7. The exact delete list for the old board is 4-4's halfway checkpoint item. Accepted.
8. AMQ library pin: the Go module versions track the CLI tags; milestone A bumps go.mod to v0.107.0 at its start (argument forms, Codex capture fixes, the AM_* identity change of #1176) and to the first tag carrying PR #1188 (pi adapter) when it exists; not one bump that waits. [review 1] Milestone B waits for PR #1186 (AMQPath) if it has merged by then, else implements the R51 fallback (amq-squad serves `coop exec`) and records it as temporary. pi launch is tested with a fake pi until 4-1 lands.
9. Jev stays in phase 5 (section 13 wins); 16.6 is corrected in revision 2.6.
10. R19's "lead does not start coding" test in 4-1; R4's seat-exec work in 2b-2 with 16.6 corrected in 2.6. Accepted.
11. The 18 S6 draft PRs: the merge train's first run is a dry run against them (plan, order, conflicts, file-set checks, no merge); a real merge on the owner's repositories needs the owner's merge grant for amq-noc and workspace-cli, which is an owner decision put to him with the first-wave roster. S6-tasklist.md supplies declared_files for those PRs.
12. Credential custody on a single-user Mac is a 3-2 design ruling by the lead at its first checkpoint, after 3-2 measures what a seat can reach (gh keychain entry, SSH agent, env); the brief states the measurement, not the answer.
13. 4-5 runs at Opus medium, not Sonnet: the enrollment path handles the owner key.
14. Defaults (lease 10 min, hook timeout 5 s, gate window 30 min, warm launch under 60 ms, amq-noc 100 ms per frame) are proposals; each brief measures and reports the real number at its halfway checkpoint, and the plan records the measured value.
15. The scheduler process in the binary (2026-10-24). No brief named the verb that constructs `scheduler.Scheduler` with the real hooks and runs it; 2b-1 ships `Tick` and the hook interfaces only. It is 2b-2 milestone B's, because milestone B is the step the scheduler runs on the done report and cannot be tested without a running scheduler: one process per squad (plan 16.5), started and supervised by the lead's team mode, wired to 2b-2's `SeatLauncher`, the AMQ wake as the doorbell, `DefaultRouter` until 2b-4 merges, the milestone B committer, and 2b-3's usage-limit source once it is on v3. The implementer proposes the verb name at its halfway checkpoint (default `amq-squad scheduler run`); restart recovery stays phase 5 (16.4). Two acceptance rows join milestone B: the process starts, dispatches a seeded task to a fake launcher and stops on a usage-limit event; and 4-1's R19 loop test (its acceptance 8) is re-run against this process, replacing the stand-in doorbell 4-1 recorded as a deviation.
16. Fleet status document shape (2026-10-24, binding for 4-3 and 4-4): `{"schema_version": 1, "kind": "fleet", "producer": "amq-squad", "producer_version": "...", "generated_at": "<RFC 3339 UTC>", "squads": [<one schema 1 status document per squad, unchanged>]}`. The single-squad document carries no `kind`; a reader treats a document with `squads` as a fleet and anything else as one squad. The operator mailbox is the owner's `user` handle (2b-5); the contract carries no operator field.
17. amq-noc 1.0 delete list (2026-10-24, 4-4 halfway): 124 files, 57,610 lines, everything reachable only from the old board or from verbs that read `.amq-squad/` or composed amq-squad commands (noc up, orchestrated squads, runtime and pane controls, doctor, preflight, team launch, threads, workstream, briefs seed, the classifier and attention queue). Approved under plan 4.7 and R44; the execution log names where each removed verb's job lives in 3.0. Measured frame time 0.29 ms at 250 rows (limit 100 ms).

First wave (lead proposal, pending the reviewer's pass and the owner's go): 2b-1 (Opus high), 3-1 (Opus medium), 2b-2 milestone A (Opus medium), 2b-3 (Opus medium). [review 1] Concurrency note: two Opus implementers hit the session limit on 2026-10-22 and 2026-10-23 running alone; four at once will hit it sooner, so the wave starts with 2b-1 and 3-1 and adds 2b-2 and 2b-3 only if the first two run a full hour without a limit stop.

Review 1 (v3-0-0-work, 2026-10-24): edits E1 to E8 applied; every open ruling closed as written under its brief; first wave confirmed: 2b-1 (Opus high) and 3-1 (Opus medium) start on the owner's go; 2b-2 milestone A and 2b-3 (both Opus medium) join after one hour without a limit stop; four is the cap.
