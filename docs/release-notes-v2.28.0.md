# amq-squad v2.28.0

## Summary

v2.28.0 is the Simple Mode release. It removes the ceremony layer that grew
around launching, messaging, and task coordination, and returns those flows to
their authoritative inputs: the roster and briefs for launch, `launch.json`
plus live process probes for runtime truth, AMQ transport acceptance for sends,
and the task-store lock for claims.

The motivating defects were not failures that the ceremony layer caught. They
were failures caused by re-rendering, re-hashing, and re-validating state the
tool had itself already written. A prepared prompt could disagree with a later
render; a readiness check could dead-end recovery across its own state
boundary; status could rediscover a different root from the current working
directory instead of reading the recorded launch coordinates. Simple Mode
removes those gaps. A brief travels with the spawn, a selected launch record is
authoritative for captured coordinates, and current liveness is always probed.

The operator-facing release surface centers on nine core workflows: `start`,
`status`, `send`, `task`, `goal`, `gate`, `verify`, `down`, and `doctor`.
v2.28 also retains the ruled exceptions: public `resume`, team roster editing,
`evidence`, and the utility and diagnostic surfaces preserved by the final CLI
census. The final CLI pruning lands last so each earlier behavioral change
remains attributable.

## Delivery sequence

### Contract first (#646, PR #647)

The release began with class-level tests for all fourteen acceptance criteria,
including the three field failures that forced the redesign (#643, #644,
#645). The guarded contract suite pins canonical-path launch, four
crash-injection roll-forward points, record-first `status`/`doctor`/`down`,
`duplicate_live`, concurrent-start serialization, receipt-free sends, the
simple task lifecycle, idle-agent wake, conversation restore, and deletion of
launch-path ceremony vocabulary.

### Simple launcher vertical (PR #648)

`start` gained the complete canonical vertical before it became the default:
resolve one namespace, build the roster and brief bytes, preview once, acquire
the session launch lock, keep verified live roles, launch only missing roles,
verify exact child identity, write authoritative launch records, and report
success only after every required child is proved live. `down` uses the same
canonical namespace identity rather than trusting a record to validate itself.

### Consumer rewiring (PR #649)

Compound-release decisions stopped consuming local receipt schema as
authority. Goal, task, dispatch, status, doctor, monitor, and operator paths
were simplified ahead of deletion. AMQ message IDs and current mailbox reality
replace locally duplicated send truth, while legacy receipt presence remains
readable only where compatibility requires it and never grants authority.

### Switch, prepared-path deletion, and skills migration (P3, PR #650)

`start` becomes the only launcher. The old orchestrator, prepare/go wizard,
readiness and drift machinery, prepared-run state, staged member admission, and
their tests are deleted. Existing launch records keep their v2.27 prepared
fields as opaque compatibility data; those fields no longer participate in
runtime selection or verification. A session notifier makes wake reliability a
launch property and restores record-first delivery after process restarts.

The final reviewed step-3 head is `ea07e69`, approximately **−27.4k net lines**
for the step-3 series after its fixes, collateral restorations, CI-fixture
repairs, and documentation migration.

The bundled Claude, Codex, and source skill mirrors now teach the same Simple
Mode lifecycle as the binary: `start` previews and reconciles a roster,
`down` retires actors without deleting their durable state, and rerunning
`start` restores missing actors and recorded conversations. The command-policy
checker was migrated with the skills, so removed `up`/`stop`/prepared-wizard
guidance cannot silently return in generated copies.

### Receipt deletion (P5a, PR #TBD — release placeholder)

Local delivery receipts, `receipt show`, `.amq-squad/receipts/`, receipt fields
in mutation envelopes, and local receipt persistence are removed. Accepted AMQ
transport plus its stable message ID is the send result. Gate, operator,
dispatch, goal fallback, task outbox, supervision, and external-orchestrator
callers use that transport result directly.

### Task/schema and CLI pruning (P6, PR #TBD — release placeholder)

The task surface contracts to `add`, `claim`, `done`, and `list`; extended
delivery, reconciliation, lease-renewal, leadership, completion-gate, and
handoff state is removed with the schema fields that only served it. The
top-level CLI contracts around the nine Simple Mode core workflows plus the
ruled public exceptions: `resume`, team roster editing, `evidence`, and the
retained utility and diagnostic surfaces. Necessary capabilities are folded
into those workflows rather than left as parallel verbs.

## Seamlessness bar delivered

Simple Mode treats continuity as part of launch correctness, not as a manual
recovery procedure layered on afterward.

- **Wake continuity is a session contract.** After `start` has verified every
  required child, it establishes one namespace-scoped notifier before sending
  the optional goal. The notifier resolves each target from its authoritative
  launch record, nudges the recorded pane for pending AMQ inbox messages,
  retries failed nudges, and deduplicates successful nudges within one process.
  Startup and heartbeat scans recover messages missed while the notifier was
  absent, deliberately providing at-least-once wake across restarts. Rerunning
  `start` adopts a healthy notifier; `down` never starts one and retires it only
  with the final managed actor.
- **Conversation restore is verified at the dispatch boundary.** `start`
  carries each recorded conversation ID into the composed child command with
  `--no-bootstrap`. The launcher must return the actual dispatched command,
  and `start` refuses success if that result dropped the exact conversation or
  would replay bootstrap. A spawnless rerun neither relaunches live actors nor
  resends the goal; a partial rerun restores only the missing actors.
- **Operator guidance matches the runtime contract.** The wizard, CLI
  primitives, team-rule template, and generated Claude/Codex skill mirrors all
  use `start`/`down` and describe the same preview, roll-forward, wake, and
  conversation-restore behavior. Skill command checks pin those examples.

## Production bugs found and fixed before merge

The release work found defects in the surviving path as well as code destined
for deletion. Each of the fourteen classes below was fixed before merge and
pinned at the class level. Five surfaced during the step-3 switch itself:
`down` notifier spawn, restart wake-loss, a missing notifier-lock parent,
injected-probe severance, and post-signal schema-zero corruption. Deletion
review then restored three ordinary safety guards that broad prepared-path
removal had accidentally swept up.

- **Dead process reported as started.** A titled tmux pane could make launch
  look successful after its child had already exited. `start` now proves that
  the pane owns the exact live child process before printing success.
- **Role aliasing across handles.** A launch record with the right role but a
  different handle could be selected as the configured actor. Managed identity
  now requires the exact roster handle; same-role foreign records are surfaced
  as unmanaged.
- **Stop-root tautology.** Exact stop compared a record's root to a value
  derived from that same record, so the check could never detect a requested
  namespace mismatch. `down` binds the record to the independently resolved
  canonical target root before any signal or pane mutation.
- **`down` spawned a notifier while stopping.** A read-oriented runtime path
  crossed the notifier startup boundary during teardown. Stop/down inspection
  is now side-effect-free and cannot create the process it is trying to retire.
- **Notifier restart lost wake events.** A notifier initialized after messages
  were already present could treat them as an old baseline and never nudge the
  recipient. Startup and heartbeat scans now recover unseen inbox messages.
- **Notifier lock parent missing.** Teardown/restart could try to acquire the
  notifier lock after its runtime directory was absent, failing before it could
  converge. The exact runtime parent is recreated before lock acquisition.
- **Injected runtime probe was severed.** The simplified `down` identity gate
  briefly fell back to production process inspection instead of the probe
  already selected by its caller. That broke the single-observation contract
  and made hermetic safety checks disagree with termination. Runtime action
  verification now carries the injected probe through the exact live-record
  check.
- **Post-signal notifier state could become schema zero.** A correctly owned
  notifier may remove its runtime directory while exiting. Teardown then read
  the now-absent record as an empty value and could persist an invalid
  schema-zero replacement. The post-signal path now recreates the parent,
  distinguishes absence from an empty or corrupt file, and uses the verified
  pre-signal record as the only safe baseline.
- **Eligibility claim leaked across ordinary attention.** A claimed compound
  release child ID could suppress an unrelated ordinary operator item sharing
  its thread. Filtering now consumes only the exact claimed message, preserving
  independent same-thread attention.
- **Concurrent identical operator answers lost replay reconciliation.** One
  answer could retain the release store's exact exclusive invocation lease
  while its sibling attempted nonblocking shared classification. The sibling
  then refused the valid identical answer as store-busy instead of reconciling
  the accepted message. A per-profile/session/gate coordination lock now
  serializes classification through send or replay while the exact claim guard
  remains the authorization boundary.
- **v2.27 launch-record schema break.** Deleting prepared semantics initially
  also deleted their JSON fields, which would make old records lossy on read or
  rewrite. The fields remain readable as opaque compatibility data and are
  deliberately ignored by Simple Mode runtime classification.
- **Blocked-goal exactness was removed as collateral.** Prepared-goal
  certification was deleted, but ordinary blocked native goals still need the
  typed goal, attempt, source, delivery state, and generated command to agree.
  That validator and its fail-closed supervision wiring were restored without
  restoring prepared-run authority.
- **Task lifecycle correlation was removed as collateral.** Deleting
  prepared-generation fixtures also removed the ordinary protection that
  compares lifecycle evidence by immutable value and rejects a forged actor,
  wrong evidence digest, or missing evidence. The prepared-free correlation
  boundary is restored and independently pinned.
- **Roster mutation concurrency was removed as collateral.** The old staged
  member path disappeared, but ordinary add/update/remove can still race with a
  concurrent profile edit between planning and the profile lock. A same-file
  digest compare-and-set now refuses stale mutations instead of overwriting the
  other edit.

These are issue-worthy regression classes, not incidental assertion updates:
immediate child death, identity aliasing, cross-root teardown, teardown side
effects, restart catch-up, lock-parent recovery, injected-observation
continuity, post-signal record integrity, attention filtering, forward-readable
legacy records, blocked-goal exactness, lifecycle evidence correlation, roster
compare-and-set, and operator-answer replay serialization each have a named
test boundary.

## Guarantees deliberately dropped

Simple Mode keeps guarantees grounded in external reality and removes
guarantees that only cross-certified amq-squad's own duplicate state.

- **Local send tamper detection is dropped.** v2.27 copied an accepted message
  identity into a second owned receipt and then protected the copy. v2.28 keeps
  AMQ's accepted message ID and checks mailbox reality when a decision needs
  it; a second local representation cannot make the first more true.
- **Prepared endpoint revalidation is dropped.** It existed to guard the gap
  between a prepared plan and later execution. That gap no longer exists:
  launch and send resolve the canonical target at execution, and runtime
  actions use recorded coordinates plus a current process probe.
- **Task-to-delivery intent atomicity is dropped.** v2.28 does not pretend one
  local transaction spans task creation, claim, an outbox intent, an external
  AMQ send, and receipt finalization. Task claim remains locked and atomic. If
  the subsequent send fails, the task remains plainly `in_progress` for
  explicit recovery; there is no automatic resend or parallel delivery state
  machine that might duplicate work.

The retained safety boundary is narrower and stronger: canonical path
selection, launch locking, exact PID/pane ownership, duplicate-live refusal,
task-claim compare-and-set, human gates, and verification against real CI and
review evidence.

## Migration from v2.27

The following replacements are semantic migrations, not aliases. Removed
commands return a usage error; automation must call the surviving workflow.

| Removed or changed v2.27 surface | v2.28 replacement |
|---|---|
| `amq-squad up` | `amq-squad start`; it previews the roster delta and reconciles missing or dead roles. |
| `amq-squad stop` | `amq-squad down [role]`; durable mailbox and launch history remain available for restore. |
| `amq-squad run`, `run start`, `wizard`, `--prepare`, and `--go` | `amq-squad start`; use `--yes` for non-interactive approval and rerun the same command to roll forward. |
| `amq-squad global` | Run `start` with an explicit project, profile, and session. There is no separate neutral-root launcher. |
| `amq-squad receipt show` and `amq-squad amq receipts list/wait` | Use the send command's exit status and returned AMQ message ID. Use `task list`, `status`, or direct mailbox/thread inspection when current state matters. |
| Local receipt fields in JSON mutation envelopes | Read `message_id` and `root` from the transport result. No local delivery-receipt object or path is returned. |
| `task renew` | `task claim` establishes the single owner; finish with `task done`. There is no renewable lease. |
| `task fail` and `task block` | Send the failure or blocker as an ordinary AMQ status message and expose it through plain status fields; the task remains visibly claimed until completed or superseded by explicit new work. |
| `task reset` and `task cancel` | Add a new task when the work changes; use `task list` for the flat history. There is no recovery or replacement graph. |
| `task release` | Keep or complete the claimed task explicitly. There is no lease-release audit transition. |
| `task deliver`, `task retry-delivery`, and `task reconcile` | Use ordinary `send`; after an uncertain result, inspect mailbox reality before an explicit resend. No task-owned outbox or automatic retry remains. |
| `task leadership` and `task handoff` | Send an ordinary durable AMQ handoff and report the actor in plain status fields. Leadership is team coordination, not task-store authority. |
| `task event` | Send ACK, progress, checkpoint, and review updates as ordinary AMQ messages. |
| `task complete` and `task ls` aliases | Use `task done` and `task list`. |
| Staged member `admit`, `replace`, and `launch` flows | Edit the roster, then run `start` to reconcile additions. For replacement, run `down <role>`, edit the roster, and run `start`. |
| `amq-squad context` | Use `status --json` for the resolved project/profile/session projection; use `doctor` when the selected namespace needs diagnosis. |
| `amq-squad activity` | Use `status`; activity is projected from current launch, task, process, and mailbox state rather than a separately written heartbeat. |
| `amq-squad bootstrap` | Use `start` to launch and verify the roster, then `status` to observe it. Manual bootstrap acknowledgement no longer gates readiness. |
| `amq-squad brief` | Use `doctor` to diagnose the selected workstream brief and setup. The brief remains the launch input consumed by `start`. |
| `amq-squad threads` | Use `status --json` for the session projection, and raw `amq list`/`amq thread` only when an exact mailbox transcript is required. |
| `amq-squad thread` | Use `status --json` for the current projection, or raw `amq thread --id <thread> --include-body` for an exact transcript. |
| `amq-squad collect` | Run one raw `amq drain --include-body`, act on the result, then park/end the turn; the session notifier supplies the next wake. |
| `amq-squad prune-panes` | Use `doctor` to diagnose orphaned or stale runtime state, then use the surviving explicit runtime workflow it recommends. |
| `amq-squad console` | Use `status` or `status --json`; there is no separate Mission Control projection. |
| `amq-squad monitor` | Check `status` once, act, then park/end the turn. The namespace-scoped notifier wakes pending AMQ work. |
| `amq-squad notify` | Raise a typed `gate` when human attention or a decision is required; otherwise expose the item through `status`. |
| `amq-squad notifications` | Use `doctor` for notification setup and health diagnosis; use `gate` for actionable human attention. |
| `amq-squad history` | Use public `resume` to inspect and restore saved conversations; `status` reports current and historical launch context. |
| `amq-squad fork` | Select or pin the fresh session in the roster, then run `start --project <dir> --profile <profile>` for that session. |
| `amq-squad review-worktree` | Use the retained `worktree` workflow for isolation and `evidence` for immutable exact-command review evidence. |
| `amq-squad tmux-harness` | Run the relevant project test directly and record it with the retained `evidence` workflow; there is no public harness wrapper. |
| `amq-squad rm` | Use `down`; v2.28 stops actors while deliberately retaining durable mailbox and launch history. |
| `amq-squad archive` | Use `down`; v2.28 does not move or delete durable session state automatically. |
| `amq-squad next` | Use `status` or `status --json` for the highest-priority projected action. |

`resume` remains a public v2.28 command for restoring saved conversations. The
`agent` dispatcher remains an internal child launch/restore boundary used by
`start` and `resume`, but is hidden from public help and completion and is not a
taught replacement for any removed command.

## Deprecations and removals

- `up`, `run`, and `wizard` are removed; use `start`.
- `stop` is removed; use `down` for a role or the whole squad.
- `global` is removed. Launch the roster in its target project/profile/session
  with `start`; external-lead setup, where still required during the transition,
  is folded into the ordinary project-scoped launch flow.
- Prepared runs, generation tokens, prepare/go, readiness rows, digests, drift
  comparison, reserved launches, and bootstrap acknowledgement are removed.
  Use plain `start`; rerun it after interruption to roll forward.
- Local send receipts, `receipt show`, receipt mutation-envelope fields, and
  `.amq-squad/receipts/` are removed. Use AMQ acceptance/message IDs and inspect
  the mailbox/thread when current delivery state matters.
- Staged member admission is removed. Change the roster and run `start` to add
  missing roles; use `down <role>` to stop one; replace with `down <role>` then
  `start`.
- Extended task verbs are removed. Coordination uses `task add`, `task claim`,
  `task done`, and `task list`; progress and handoffs travel as ordinary AMQ
  messages, and external checks belong under `verify`/`gate`.

## Upgrade notes for v2.27 users

- Existing v2.27 `launch.json` records remain readable. Prepared-run and digest
  fields are compatibility input only; v2.28 never treats them as authority.
- Existing `.amq-squad/prepared/` and `.amq-squad/receipts/` directories are
  ignored. The upgrade does not auto-delete operator data.
- A namespace dead-ended by v2.27 preparation does not need manual cleanup.
  Run `amq-squad start --project <dir> --profile <profile> --session <session>`;
  Simple Mode keeps verified live actors, relaunches missing/dead actors, and
  converges on the canonical namespace.
- Replace automation that calls `up`, `run`, or `wizard` with `start`, and
  replace `stop` with `down`.
- Replace receipt parsing with the command exit status and returned AMQ message
  ID. When an invoked send has no stable ID, treat it as uncertain and inspect
  the mailbox rather than blindly retrying.
- Replace extended task lifecycle automation with the four surviving task
  verbs before upgrading.

## Size and verification

The final release delta will be recorded at the release head after P3, P5a,
and P6 merge.

- Through merged PR #649, the branch is **+2,014 lines net** versus v2.27.0
  (4,938 insertions, 2,924 deletions): contract tests and the new vertical land
  before deletion.
- At final step-3 head `ea07e69` (PR #650), the reviewed switch/deletion series
  is **approximately −27.4k lines net** after the notifier and restore
  contracts, ordinary-safety restorations, CI-fixture repairs, and skills
  migration. P5a and P6 remain outside that checkpoint.
- **Final net delta: TBD at the v2.28.0 release head.** The approved plan's
  estimate is at least −30,000 lines; this draft does not present that estimate
  as a measured result.

Verification is contract-led: the fourteen Simple Mode acceptance criteria,
crash roll-forward, exact identity/root checks, real AMQ send and wake lanes,
and the full repository suite must pass at each final reviewed head. Reviews
remain exact-head evidence; rebasing invalidates and reissues them.
