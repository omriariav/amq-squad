# amq-squad 3.0, brief 2b-2 milestone B: the harness commit step and the scheduler process

Branch `v3-2b2-b`, cut from `v3` at 2c7a38e6 (2b-1, 3-1, the 2a hook verbs and 2b-2
milestone A merged at f4d3dc13). Brief: docs/v3-phase2-briefs.md "2b-2 seat-exec and
launch" (Builds: the harness-side commit step; acceptance 7, 9, 10; open rulings 4 and
5 as ruled) and the lead rulings 6, 14, 15, 16, 17. Plan 4.3, 4.5, 7.2, 16.5; R16, R17,
R19, R51, R52; amendments 21, 25, 26, 28. Opus 5.5, high effort (ruling 1: the
enforcement point of amendment 21).

Scope added by the lead during the run (2026-10-24): the status seat row's `role`
joined from the launch record; an R12 N=5 re-run through the scheduler process (the
scheduler-side half); `AMQ_SQUAD_BIN` pinned by seat-exec (from 4-2); the S4 fault
table against a real Claude seat at the live run (4-2's acceptance 3); seat-exec's
`claude_hooks_missing` preflight with the Claude permission mode recorded beside it
(plan 4.3, R36 ruling). The size cap is 3-2's measurement (below).

## Commit 1: the harness commit step, the lead widening path, the growth gate

### Built

- `internal/seatcommit` (new): the commit step of plan 4.3 / 7.2 and amendment 21.
  `Run(Options) Result` is the whole step for one done report, under a per-seat
  ledger lock:
  1. amendment 28: `seatexec.PreflightDiff` (the owner's global agent config against
     the preflight copy) at every seat exit, before anything is committed; a change
     refuses `global_config_changed`, routed to the **owner**;
  2. the grant (`grants.Load` of the task's repository); a load error refuses
     `grants_invalid`;
  3. the seat's **staged** launch record (`launch.StagedDir`, under the guard-protected
     extension dir; not the copy seat-exec publishes in the seat's mailbox) for the
     worktree and branch; the worktree must be the root of a checkout of the task's
     repository (same git common dir as `project_path`) with the record's branch
     checked out, else `worktree_mismatch`;
  4. the diff base from the **commit ledger** (`<root>/extensions/amq-squad/commits/<seat>.json`):
     the worktree HEAD the core recorded when it launched the seat, or the last
     harness commit; never the seat's refs (everything under `.git` is same-UID
     writable for a Claude seat: a branch ref, the base branch ref, a forged "harness"
     commit). No ledger refuses `base_unrecorded`; `seat commit --base SHA` records
     one as a lead decision;
  5. the tree of the worktree as it is now, built in a temporary index read from the
     base (`read-tree`, `add -A`, `write-tree`); the seat's own index and its own
     commits are not used. git refuses paths beyond a symbolic link;
  6. `diff-tree --raw` and `--numstat` (`--no-renames --no-ext-diff --no-textconv`)
     base..tree; the checks, most severe first: `protected_path` (the grant's
     `ProtectedEntries`: grants.Implicit, the owner's and squad.yaml's lists, plus the
     session root's extension dir, `~/.amq-squad/` and the rules file when they fall
     inside the repository; case folded; an entry covers a path or any ancestor; a
     `managed:` entry protects only the amq-squad managed block, compared block for
     block between base and tree), `outside_declared_files` (`task.MatchPath`, new:
     `path.Match` per segment, `**` for zero or more segments, a trailing `/` or `/**`
     covers a tree; case-sensitive, so a case mismatch refuses), `nested_repository`
     (a gitlink), `size_cap_exceeded` (one reason code, the detail names
     `size_cap_lines` or `size_cap_files`);
  7. on success: `commit-tree` under the harness identity (`amq-squad harness
     <harness@amq-squad.invalid>`, author and committer) with the parent from the
     ledger, the message `<id>: <title>` plus trailers `Amq-Squad-Task`,
     `Amq-Squad-Seat`, `Amq-Squad-Declared-Files: sha256:` (the declared set),
     `Amq-Squad-Diff: sha256:` (the raw diff); ledger append; `update-ref` of the seat
     branch and `read-tree` of the seat's index (files untouched); `push --porcelain
     --no-verify <remote> <commit>:refs/heads/<branch>`, never `--force`; a rejected
     push refuses `push_rejected`; a push or git failure is **deferred** (nothing
     decided, the next tick runs the step again). A re-run after a crash between the
     commit and `task.Complete` finds the recorded commit and only pushes.
  Every harness git call runs with `-c core.hooksPath=/dev/null -c
  core.fsmonitor=false -c core.untrackedCache=false` (command-line config outranks a
  seat-written `.git/config`) and an environment without `GIT_*` redirection.
- `CheckWiden` (seatcommit/widen.go): the lead widening path. The widened globs are
  checked against the grant's protected entries (conservative glob overlap, case
  folded); with a done report the commit step runs as a dry run on the seat's real
  diff with the widened set (size cap; paths still outside are notes). A crossing
  needs the owner-signed **growth gate** for that exact crossing:
  `gatetarget.GrowTask(root, task, "protected_paths", <JSON list of the globs>)` or
  `GrowTask(root, task, "size_cap", {"lines":N,"files":M})`. Once the owner's gate is
  accepted, the same widening is allowed and its target is recorded on the scope change
  (`scope_changes[].gates`, additive); the commit step re-verifies every recorded gate
  (exact target for this root and task, then the full owner-signature check) before it
  lets a protected path or a size past the cap. A recorded but unaccepted gate, or a
  gate for another task, opens nothing.
- `internal/task/scope.go` (new; `Widen` moved here from lease.go): `WidenGated`
  records the gates; the store refuses a widening by the seat that holds or reported
  the task (`self_widen`: the seat never widens its own set) and keeps 2b-1's
  `file_lock_conflict` refusal; `Escalate` routes a blocked task to the owner.
  `task.MatchPath` in files.go.
- `amq-squad seat commit --seat H` (internal/cli/seat_commit.go, new; briefs ruling 4):
  runs the step and records the result (`task.Complete`, or `task.RefuseCommit` with
  the reason code and route); `--dry-run`, `--base SHA`, `--json`; exit 15 refused,
  16 deferred. The gate checker is `gateverify.Verifier.AcceptedFor("spawn", target)`
  in the namespace of the resolved `--project/--profile/--session`; with `--root` only
  no gate is accepted (fail closed).
- `amq-squad task widen ID --me LEAD --files ... --reason ...` (internal/cli/task_widen.go,
  new): the core's decision first; a crossing raises the growth gate in the gate store
  (record first, the question when a transport is attached; id
  `widen-<task>-<sha256(target)[:12]>`, idempotent), escalates the blocked task to the
  owner (`growth_gate_required`, the gate ids in the detail) and exits 15. Refused too:
  `not_lead` (the scheduler state names another lead) and a `--me` that differs from
  the environment's `AM_ME` (a seat's environment carries its own handle).
- Scheduler hooks (2b-1, additive): `CommitResult.Deferred` (logged
  `commit_deferred`, no store write); `LeadNotifier.CommitRefused(task)` called after
  `RefuseCommit` (the store record first, then the AMQ message: the route 4-1 already
  implements on its side); `Config.Lead`, written to the scheduler state file.
- seat-exec `PreflightDiff` ignores exactly one write, codex-cli's own `last_updated`
  inside a `[marketplaces.*]` table of `~/.codex/config.toml` (amendment 17: codex-cli
  owns that file); any other byte of either file is a change.
- `seatlaunch.Launch` records each git seat worktree's HEAD as the commit base after
  staging (a non-git worktree records nothing and its commit step refuses
  `base_unrecorded`); `Options.KeepPreflight` fills a missing preflight copy and never
  refreshes one (the scheduler process refreshes once at its start; a refresh per seat
  launch would absorb a running seat's change before that seat's exit diff).
  `Seat.RoleName` (`"role"`) travels into the launch record's `role`.
- status (2b-1 collect): the seat row's role, runtime, model and effort join from the
  launch record, the staged one before seat-exec publishes, the published one after;
  only the published record is a liveness signal.
- guard: `managedShape` moved to `seatcommit.ManagedShape` (one copy for the guard and
  the commit step).
- grants (3-1, additive, coordinated with 3-2 by the lead): `Merge.SizeCapFiles`
  (`merge.size_cap_files`), the squad.yaml narrowing `merge_size_cap_files`,
  `Grants.SizeCap(repository, base)`, defaults `DefaultSizeCapLines = 300` and
  `DefaultSizeCapFiles = 5`. 3-2 owns the rest of that schema change (making
  `size_cap_lines` optional in a merge entry); a merge grant still requires it here.

### Size cap (ruling 5)

Measured by 3-2's S6 dry run (relayed by the lead, 2026-10-24): the largest clean S6 PR
changed 207 lines (additions plus deletions) and touched 2 files; rounded up to the next
hundred lines and the next five files: **300 lines, 5 files**. Both are grant fields
(`size_cap_lines`, `size_cap_files` per merge grant; squad.yaml only lowers them). I did
not run the gh queries (lead ruling). Binary files count as files, not lines (open item 3).

### Behavior changes

- `task.Widen` refuses the reporting or holding seat (`self_widen`). The 2b-1 tests call
  it as `lead` and still pass.
- `seatlaunch.Launch` writes the commit ledger for git worktrees.
- `PreflightDiff` no longer reports codex-cli's `last_updated` rewrite.
- status seat rows carry the role (and runtime, effort) from launch records; before, the
  3.0 record's role was never read (the v2 `Binary`/`Role` fields only).

### Tests (acceptance 7 and the security rows)

Fixture: a bare origin, a main checkout, one seat worktree on `seat/s1`, the staged
launch record, the recorded base, a real task store (internal/seatcommit/fixture_test.go).

| row | test |
| --- | --- |
| 7.1 a diff inside declared_files commits (harness identity, parent = recorded base, trailers) and pushes | TestCommitInsideDeclaredFilesCommitsAndPushes |
| 7.2 one file outside (the S6 W5 case) is refused with `outside_declared_files`; nothing committed or pushed | TestCommitOutsideDeclaredFilesIsRefused |
| 7.3 a lead widening commits | TestLeadWideningCommits |
| 7.4 a widening onto a protected path is refused and names the growth-gate target; with the gate accepted it goes through | TestWideningOntoProtectedPathNeedsTheGrowthGate, TestRecordedButUnacceptedGateDoesNotOpen |
| 7.4 through the verbs: seat commit refuses, task widen raises the gate record in the gate store and routes the task to the owner; the owner's signed thread answer (gate answer) opens it; widen and seat commit then push | TestSeatCommitAndWidenThroughTheGrowthGate (cli) |
| 7.5 a Codex seat under workspace-write (simulated: the git common dir read-only while the seat works) ends with a branch the harness pushed | TestCodexSeatEndsWithAHarnessPushedBranch |
| seat commits are not in the pushed history; an out-of-scope file in a seat commit is caught | TestSeatCommitsAreNotTheBase |
| a moved base branch ref hides nothing | TestMovedBaseRefDoesNotHideAChange |
| no repository hook runs in the harness's git | TestHarnessGitRunsNoHooks |
| amendment 28: a global config change fails the task to the owner | TestGlobalConfigChangeFailsTheTask, TestPreflightDiffIgnoresOnlyCodexSelfRewrite |
| size cap, both halves | TestSizeCapRefuses, TestSizeCapFilesRefuses, TestSizeCapDefaultsAndFields (grants) |
| managed block, case folding, nested repository, deletions, no force push, idempotent re-run, second task on the same seat, no recorded base | TestManagedBlockIsProtected, TestProtectedPathsFoldCase, TestNestedRepositoryIsRefused, TestDeletionsAreChecked, TestPushRejectedIsNeverForced, TestRerunAfterCommitIsIdempotent, TestSecondTaskDiffsFromTheLastHarnessCommit, TestNoRecordedBaseIsRefused |
| the seat never widens its own set; file_lock_conflict to the lead | TestSeatNeverWidensItsOwnSet, TestWideningFileLockConflict |
| widen refuses a non-lead and an AM_ME mismatch | TestTaskWidenRefusesANonLead (cli) |
| scheduler: a deferral writes nothing; a refusal is recorded, then the lead is told | TestCommitDeferralAndTheLeadNotice |
| the launch records the base, carries the role id, keeps the preflight copies | TestLaunchRecordsTheCommitBaseAndKeepsPreflight |
| status role join | TestSeatRoleJoinsFromTheLaunchRecord |
| MatchPath | TestMatchPath |

Revert check: 19 rows, each fix undone in place, the row run with `-race` and seen to fail
on its assertion, the fix restored and the row seen to pass:
`docs/v3-spike/2b2b-evidence/c1-revert-check.txt` (ALL ROWS CONFIRMED).

Gate (no live seat or lead on the host): `docs/v3-spike/2b2b-evidence/c1-gate.txt`
(build, vet, `go test ./...`, manifest-check --final, linux and windows builds, `-race` on
seatcommit, task, scheduler, seatlaunch, status, grants, seatexec): PASS.

### Measured

Commit step wall time on the fixture (`docs/v3-spike/2b2b-evidence/c1-timing.txt`, median
of 10): commit and push to a local bare origin 121 ms (113 to 155); refusal 49 ms (47 to
49).

### Deviations

- Acceptance 7.5 is a fixture (the git common dir made read-only while the "seat" works),
  not a real Codex seat: a real one costs provider allowance and runs under the lead's
  bounded-run ruling (asked at the halfway checkpoint).
- A global config change blocks the task routed to the owner (`global_config_changed`)
  instead of `task.Fail`: a failed attempt returns to the pool and would be retried into
  the same unchanged config until retries run out. Ruling asked.

### Open items

1. Clearing a `global_config_changed` block: the owner inspects the change; the
   preflight copy is refreshed at the next scheduler start or `amq-squad launch`. A verb
   to refresh it by hand is not built.
2. After a refusal the lead may `reset`: the report is discarded, and the seat's
   worktree still holds the refused diff, so that seat's next task is refused for it
   (fail closed). Discarding the diff is destructive and is not done by the core.
3. Binary files count toward the file cap only.
4. The identity of the lead on `task widen` is the CLI's `--me` (with the AM_ME and
   scheduler-lead checks). A guard rule denying `amq-squad task widen|reset|cancel|add`
   from a non-lead seat belongs to the hook verbs (proposal for 4-2's follow-up).
5. Unicode normalization of paths (NFD on macOS) is not folded; a mismatch refuses.

Halfway checkpoint accepted by the lead (2026-10-24), with rulings: a global config
change blocks to the owner rather than `task.Fail` (the precise form of amendment 28's
"fails the task"); the bounded real-provider session goes after 4-1 merges, on a rebased
head (Codex seat for 7.5, the role-delivery matrix at N=3 per cell both arms, the S4
fault table against one real Claude seat, the R19 re-run through `amq-squad scheduler
run`); R12's scheduler-side half is 4-1's harness driven through the scheduler process,
N=5; the verb name `amq-squad scheduler run` is accepted.

Rebase: commit 1 was rebased onto v3 at 1b920331 (2b-5, 4-2 and 2b-3 merged) right after
the halfway report, before the lead's "rebase at the end" note arrived; conflicts only in
the manifest and grants/parse.go (2b-5 exported the yamlsub entry methods). Gate on the
rebased commit (75affadb): `docs/v3-spike/2b2b-evidence/c1-rebased-gate.txt`, PASS.

## Commit 2: the Claude hook unit, AMQ_SQUAD_BIN, the global-config quarantine

### Built

- seat-exec pins `AMQ_SQUAD_BIN` to its own resolved executable in the seat environment
  (4-2: the hook script honors it), overriding any inherited value.
- Claude seats: the hook and the permission mode as one unit (plan 4.3, R36 ruling).
  The launch compiler adds `--permission-mode dontAsk` to every Claude seat's provider
  arguments (in the trust digest) and records `permission_mode` in the launch record
  (schema 1, additive; record.go stays at its 250-line target). seat-exec refuses
  `claude_permission_mismatch` when the record names no mode, the argv carries another
  mode, two modes or a permission bypass, and `claude_hooks_missing` unless the
  amq-squad plugin is enabled in `settings.json` (`enabledPlugins["amq-squad@<marketplace>"]`),
  installed at user scope in `plugins/installed_plugins.json`, and its installed
  `hooks/hooks.json` runs `amq-squad-hook.sh pre-tool` on PreToolUse with the script
  present; the lookup honors `CLAUDE_CONFIG_DIR` as it survives the scrub.
  Mode choice: AMQ v0.107.0 admits `acceptEdits`, `auto`, `manual`, `dontAsk`, `plan`
  (adapter_claude.go; `default` is refused by the launch validation). `dontAsk`: guard's
  explicit allow answers every hooked call, and a call nothing approved (a hook that
  failed open) is denied instead of prompted, so an unattended seat neither stalls nor
  runs unguarded. To be confirmed at the live S4 fault-table run (open ruling 1 below).
- The global_config_changed block (reviewer conditions):
  1. before the commit step: the seat's tree is snapshotted in a temporary index and kept
     on the commit ledger (`quarantine`: task, parent, tree, diff digest, and the
     PreflightDiff result as evidence); nothing is committed, the branch ref does not move,
     nothing is pushed. A re-run stays refused until the owner clears; after the clear only
     that same tree commits (`quarantine_tree_changed` otherwise, to the owner).
  2. the seat is quarantined: `task.Store.Quarantine` (one file per seat under the store,
     read under the store lock) makes `Grant` and the `Claim` pull refuse it with
     `seat_quarantined`; the scheduler logs `seat_quarantined`, closes the seat (retried
     while the close fails) and never dispatches to it; a new seat is requested for the
     pool's next task.
  `amq-squad seat clear --seat H [--me OWNER]` is the owner's clear: it refreshes the
  preflight copies to the current config, marks the ledger quarantine cleared, lifts the
  store quarantine, and re-arms the blocked task (`task.Rearm`) so the commit step runs
  again. A seat never clears its own quarantine (`--me` or `AM_ME` equal to the seat).
- Ledger writes go through `internal/atomicfile` (2b-5's one durable writer).
- squad.yaml (2b-5) carries the role id into the typed seat list (`RoleName`, so the
  launch record's `role` and the status join are filled from squad.yaml), and accepts and
  renders `merge_size_cap_files`.

### Behavior changes

- Every Claude seat launches with `--permission-mode dontAsk`, and seat-exec refuses a
  Claude seat without the user-scope plugin. A host without the plugin installed can no
  longer start a Claude seat (by design).
- A global config change no longer just blocks: the seat is quarantined and closed.

### Tests

| row | test |
| --- | --- |
| AMQ_SQUAD_BIN is the launching binary | TestSeatExecPinsAMQSquadBin |
| claude_hooks_missing: not installed, not enabled, project scope only, hooks.json without the guard, script missing; CLAUDE_CONFIG_DIR honored | TestClaudeSeatRefusesWithoutTheUserScopeHook |
| the permission mode recorded (published record) and checked: none recorded, another mode, none in argv, two modes, a permission skip | TestClaudePermissionModeIsRecordedAndChecked |
| the launch puts `--permission-mode dontAsk` in the Claude provider arguments and the record (and none for Codex) | TestLaunchRecordsTheCommitBaseAndKeepsPreflight |
| the tree kept on the ledger, nothing committed or pushed; refused until cleared; only that tree after the clear | TestGlobalConfigQuarantineKeepsTheTreeAndPushesNothing |
| a lease (grant and pull) for a quarantined seat refused with the reason | TestQuarantinedSeatGetsNoLease |
| the scheduler quarantines, closes, and dispatches the next task elsewhere | TestQuarantineClosesTheSeatAndDispatchesElsewhere |
| through the verbs: seat commit quarantines, seat clear by the owner re-arms, seat commit pushes; a seat cannot clear itself | TestSeatCommitQuarantineAndOwnerClear |
| squad.yaml merge_size_cap_files round trip | TestSquadSizeCapFilesRoundTrips |

Revert check: 11 rows, `docs/v3-spike/2b2b-evidence/c2-revert-check.txt` (ALL ROWS
CONFIRMED). Gate: `docs/v3-spike/2b2b-evidence/c2-gate.txt`, PASS (no live seat or lead
on the host).

### Denied by the harness

- Learning the user-scope install layout from a scratch `claude plugin install` under a
  temporary HOME was refused by the worktree harness (a command that sets HOME). Not
  retried in another form. The check follows the layout the 4-2 install evidence names
  (`installed_plugins.json` entry at scope user, `enabledPlugins` in settings.json, the
  plugin cache under HOME/.claude/plugins); the live session confirms it against a real
  install.

### Open rulings

1. `dontAsk` as the Claude seat permission mode (the alternative is `manual`, which
   prompts and stalls an unattended seat when a hook fails open). The live S4 fault table
   records what each fault does under it. Ruled by the lead and the reviewer: dontAsk,
   with the condition that the owner's user-scope allow rules never reach a seat (commit 4:
   a per-seat CLAUDE_CONFIG_DIR).

## Commit 3: the scheduler process (`amq-squad scheduler run`)

### Built

- `internal/schedproc` (new), briefs ruling 15: `Run(ctx, Options)` constructs 2b-1's
  `scheduler.New` with the real hooks and ticks it (default every 2 s, an early tick on
  `Wake`) until the context ends. One process per squad: a non-blocking exclusive lock on
  `<root>/extensions/amq-squad/scheduler/run.lock`; a second process is refused
  (`ErrRunning`). Every decision is a JSON line in `scheduler/decisions.jsonl`
  (`scheduler_started` with the incarnation, every Tick decision, `tick_error`,
  `scheduler_stopped`). A tick error is logged and the next tick retries (the store is
  transactional). The usage-limit window starts with the process: a limit reported before
  the start is history, not a stop. A router that implements `Flush()` (2b-4's
  `routing.Router`) is flushed once per tick; the swap from `DefaultRouter` is the one
  `Router:` line in `scheduler_run.go`.
- Hooks: `schedproc.Launcher` sends a request to the launcher of its runtime and frees the
  seat slot on a confirmed cancel or close; `Slots` takes the first free seat of the typed
  seat list (the `launch --seats` format) whose runtime (and for Claude and Codex model and
  effort) matches the pool, creates its worktree on `seat/<handle>` from the base branch
  with git hooks off, and never starts a seat whose worktree is the repository root (the
  commit step moves its branch). Claude and Codex seats go through 2b-2's
  `seatlaunch.Launcher` (KeepPreflight: the copies are refreshed once at process start);
  pi seats go to `schedproc.External`, the stand-in launcher for AMQ 0.107.0 (no pi
  adapter): the core still stages the launch record and role.md and records the commit
  base, then writes `scheduler/requests/<id>.json` for an external runner, which posts
  `seat_up` with the request id (`amq-squad scheduler post`); a cancel or close writes a
  file the runner obeys and is confirmed at once (the stand-in's limit). The doorbell is an
  AMQ message (`amq send --me scheduler --to <seat> --kind todo`) that the seat's AMQ wake
  delivers; the lead notice of a refusal is an AMQ message to the lead after the store
  record; both send as the `scheduler` mailbox with the session identity, never the
  caller's (status does not list that mailbox as a seat). The committer is the seat commit
  step (the verb's own function), with a deferral that repeats 5 ticks in a row turned into
  `commit_failed` for the lead. The usage-limit source is 2b-3's `usagelimit.Detector` for
  the owner's home.
- `amq-squad scheduler run --seats FILE --lead H [--tick] [--trust] [--max-seats N]
  [--base-branch B]` and `amq-squad scheduler post --root R --kind ... --handle H
  [--request-id] [--pool]`. `--trust` is the owner's standing answer
  `trust_exact_subject` to AMQ's trust_confirmation for this squad's launches
  (`seatlaunch.Options.Standing`, used only when the action allows that choice and no
  explicit decision is given). `--max-seats` only narrows the grant. The lead's handle is
  written to the scheduler state file (task widen's not_lead check).
- Not yet: the lead's team mode starting and supervising this process lands with 4-1's
  `lead --team` on v3 (rebase before the live session).

### Tests

| row | test |
| --- | --- |
| ruling 15 row 1: the process starts, dispatches a seeded task to a fake launcher (seat requested, joined, rung), stops starting seats on a usage-limit event (the next task waits `usage_limit`, no second request), refuses a second process, stops on cancel; the decision log and state file | TestProcessDispatchesAndStopsOnAUsageLimit |
| the same through the verb, with the real slot choice and the AMQ doorbell argv from the scheduler mailbox | TestSchedulerRunVerbDispatchesRingsAndStops (cli) |
| a usage limit before the start is history | TestUsageLimitBeforeTheStartIsHistory |
| external events reach the scheduler; an unknown kind is refused | TestPostedEventsReachTheScheduler |
| slots: matching, worktree and branch creation, no double use, no repository-root seat, release | TestSlotsPlanWorktreesAndRelease |
| the external (pi stand-in) launcher stages, records the base, writes the request, cancel and close files | TestExternalLauncherStagesAndWritesTheRequest |
| deferrals to commit_failed, the quarantine passed through | TestCommitterDeferralsAndQuarantine |
| doorbell and lead notice argv and identity | TestDoorbellAndLeadNoticeSend |
| the doorbell lands in the seat inbox with the installed amq 0.107.0 | TestDoorbellLandsInTheSeatInboxWithTheInstalledAMQ |
| status does not list the scheduler mailbox | TestSeatRoleJoinsFromTheLaunchRecord (extended) |

Ruling 15 row 2 (4-1's R19 loop re-run against this process) and the R12 scheduler-side
half run in the bounded live session after 4-1 merges.

Revert check: 9 rows, `docs/v3-spike/2b2b-evidence/c3-revert-check.txt` (ALL ROWS
CONFIRMED; the one-process row's second Run is bounded by a 300 ms context so a reverted
lock fails on its assertion instead of hanging). Gate:
`docs/v3-spike/2b2b-evidence/c3-gate.txt`, PASS.

### Measured

Tick budget (`docs/v3-spike/2b2b-evidence/c3-tick-budget.txt`): one tick over 100 tasks
(96 pending, 4 leased, cap reached), DefaultRouter: median 7.5 ms of 10 (6.0 to 10.0). With
2b-4's router at 1.3 to 1.8 ms per pending task (its halfway number) a tick at 100 pending
is about 0.18 s, inside the 2 s tick; no shared snapshot is needed at this size.

## Commit 4: every Claude seat runs with its own CLAUDE_CONFIG_DIR

Ruling (lead and reviewer, 2026-10-24), replacing commit 2's owner-HOME plugin check: under
dontAsk a hook that fails open hands the call to Claude's permission rules, and the owner's
user-scope allow rules (`Bash(git:*)` and the like) would run it unguarded. A per-launch
`--settings` file cannot remove them (array settings merge across scopes, as far as I know;
the live probes record it), so the seat gets its own config dir instead.

### Built

- `internal/claudeseat` (new): `Write` creates, fresh for every launch (`os.Mkdir`, files
  `O_EXCL`), 0700, `<root>/extensions/amq-squad/launch/<handle>/claude-config/<launch id>`
  (the guard-protected, core-written staged dir, ruled over the seat-writable published
  mailbox dir) with exactly: `settings.json` (`permissions.defaultMode` the launch's mode,
  `permissions.allow` empty, `enabledPlugins["amq-squad@amq-squad"]`), the hooks plugin
  installed at user scope in Claude Code's layout (`plugins/installed_plugins.json`
  version 2 with the install path, `plugins/known_marketplaces.json` with a directory
  source, `plugins/cache/amq-squad/amq-squad/<version>/` and `plugins/marketplaces/amq-squad/`
  holding `plugin.json`, `hooks.json` and `amq-squad-hook.sh` 0700), all from the files
  embedded in this binary (`plugins/embed.go`, new, outside internal/), and the first-run
  seed. The seed is an explicit list (`claudeseat.Seed`), empty now: nothing is seeded
  until the live run shows a fresh dir stops at a first-run or login prompt; `Write`
  reports the seeded keys. The old dir of a relaunched handle is kept as evidence.
- `Digest` covers the managed files only (settings.json and everything under plugins/:
  path, permission bits, content, length prefixed); Claude Code's own state in the dir
  (projects, session files, todos, .claude.json updates: process writes, not tool calls)
  is outside it. The launch record carries it (`claude_config_sha256`, record.go stays at
  250 lines).
- seat-exec for a Claude seat: `claudeseat.Check` refuses `claude_permission_mismatch` when
  the dir is missing, not a directory, not 0700, not owned by the user, or its managed
  files are not what the launch wrote (digest), or its settings carry another mode or any
  allow rule; `claude_hooks_missing` when its plugin is not enabled or its user-scope
  install does not run `amq-squad-hook.sh pre-tool` on PreToolUse; the argv check adds a
  `--settings` refusal. The seat environment's `CLAUDE_CONFIG_DIR` is that dir, overriding
  anything inherited. `claude_allow_rules_present` refuses a seat whose worktree
  `.claude/settings.json` or `.claude/settings.local.json` (which still merge) carry allow
  rules (an unreadable file counts), unless the launch's one switch
  `seatlaunch.Options.PermitWorktreeAllowRules` set `claude_allow_rules: permit` in the
  record.
- doctor: `claude seat allow rules`, a warning naming each allow rule of the repository's
  `.claude/settings.json` and `settings.local.json`.
- The scheduler's usage-limit source reads every Claude seat's
  `claude-config/<launch>/projects` beside the owner's home (`schedproc.Limits`): a seat's
  session files no longer land in `~/.claude/projects`.
- `global_config_changed` keeps its detector (PreflightDiff on the owner's files) and now
  means only a seat reaching outside its own config dir.

### Behavior changes

- A Claude seat no longer reads the owner's `~/.claude` at all (settings, permission
  rules, plugins, sessions). The owner's own user-scope allow rules no longer block Claude
  seats (they never reach one).
- A Claude seat in a repository whose `.claude/settings*.json` carries allow rules is
  refused.
- A Claude resume across launches gets a new config dir, so the earlier transcript is not
  in it (open item 1).

### Tests

| row | test |
| --- | --- |
| the dir laid out (0700, settings, the embedded hook script 0700), fresh per launch (a second write and a pre-existing dir refused), Check passes, no seed written | TestWriteLaysOutTheSeatConfigDir |
| the digest covers only the managed files | TestDigestCoversOnlyTheManagedFiles |
| the seed is explicit | TestSeedIsExplicit |
| worktree allow rules listed, an unreadable file counts | TestWorktreeAllowRules |
| the seat runs with its own CLAUDE_CONFIG_DIR, the owner's allow rule not in its settings | TestClaudeSeatRunsWithItsOwnConfigDir |
| missing, not 0700, an allow rule added after the launch, no digest: claude_permission_mismatch; a plugin without the guard (digest re-recorded): claude_hooks_missing | TestClaudeSeatConfigDirIsChecked |
| worktree allow rules refuse; the switch permits | TestClaudeSeatRefusesWorktreeAllowRules |
| the mode and the config digest in the published record; a --settings argument refused | TestClaudePermissionModeIsRecordedAndChecked |
| each launch writes the dir and the record carries its digest; the earlier launch's dir kept; none for Codex | TestLaunchRecordsTheCommitBaseAndKeepsPreflight |
| doctor warning | TestDoctorWarnsOnRepositoryClaudeAllowRules |
| usage limits from the seat config dirs | TestLimitsReadTheSeatConfigDirs |

Acceptance rows for the live session (the two probes, ruled): (a) the owner's user-scope
`Bash(echo:*)` allow does not reach the seat (hook absent, dontAsk: echo is denied); (b) a
worktree-scope deny of Bash against the hook's explicit allow, outcome recorded with the
Claude Code version. Both under a scratch config dir, never the owner's settings.

Revert check: 11 rows, `docs/v3-spike/2b2b-evidence/c4-revert-check.txt` (ALL ROWS
CONFIRMED). Gate: `docs/v3-spike/2b2b-evidence/c4-gate.txt`, PASS.

### To establish at the live run

- Login and first run: whether a fresh CLAUDE_CONFIG_DIR finds the login (macOS Keychain)
  and starts without a first-run prompt (theme, trust dialog). If it stops, the seeded keys
  go into `claudeseat.Seed` and the log records them with the Claude Code version. If the
  Keychain credential is keyed by the config dir, the seat cannot log in from a fresh dir;
  that would be a finding for the lead, not something the core works around.
- That Claude Code loads the plugin from the written layout (installed_plugins.json plus
  known_marketplaces.json) without a `claude plugin install`.

### Open items

1. Fresh dir per launch versus Claude resume: AMQ resumes a Claude seat with
   `--resume <conversation id>`, whose transcript lives in the previous launch's dir. A
   resumed launch could copy `projects/` from the handle's previous dir (Claude's own state,
   outside the digest), or resume stays unsupported for Claude seats. Ruled: a Claude
   resume across launches is unsupported in 3.0; a relaunch is a fresh launch with a fresh
   dir, the previous dir stays as evidence until the record is swept (beside pi's resume
   refusal).

## Rebase onto 4-1 (v3 at 07d9d1a3)

4-1 (the built-in pi package and runtime) merged at 0ded255d, with 4-2's fix after it.
The four commits rebased with manifest-only conflicts (line counts and 4-1's
`internal/task/reset_by.go` row beside mine); the seat-exec pi pieces were already wired to
`piruntime.CheckResume` by 4-1's integration commit. Gate on the rebased head:
`docs/v3-spike/2b2b-evidence/rebase-4-1-gate.txt`, PASS.

## Commit 5: the lead's team mode supervises the scheduler; the default pool

### Built

- `amq-squad lead --team --scheduler-seats FILE` (ruling 15: started and supervised by the
  lead's team mode): before the lead's pi session starts, the lead starts
  `amq-squad scheduler run --seats FILE --lead <its handle>` as a child, restarts it when it
  exits (1 s backoff doubling to 30 s; after 5 runs in a row shorter than 10 s it stops
  restarting and logs it), and stops it when the lead ends (SIGTERM, a kill after 10 s). The
  child's output goes to `FILE.scheduler.log`. The lead stays the parent of pi (4-1), so it
  is alive to supervise. The scheduler's own lock keeps one per squad.
- `scheduler run --default-pool runtime:model:effort`: DefaultRouter routes a task that
  names no pool to it (the lead's `amq_task_add` sets none). Temporary: the single-pool
  stand-in until 2b-4's routing table merges, when the swap is the one Router line.

### Tests

| row | test |
| --- | --- |
| the lead starts the scheduler process, restarts it when it dies, stops it within 5 s when the lead ends | TestLeadSupervisesTheSchedulerProcess |
| repeated fast failures stop the restarts | TestLeadSchedulerGivesUpOnFastFailures |
| the default pool routes a pool-less task, a task's own pool wins, none means no_route | TestDefaultRouterDefaultPool |

Revert check: 4 rows, `docs/v3-spike/2b2b-evidence/c5-revert-check.txt` (ALL ROWS
CONFIRMED). Gate: `docs/v3-spike/2b2b-evidence/c5-gate.txt`, PASS (a first run failed on
4-1's `TestNoPackageUnderInternalReadsAnNsec`: the word "consecutive" in a field comment;
reworded).

## Live session, part 1 (2026-10-24, 17:33Z to 18:12:34Z)

Bounded session under the lead's approval; no gate ran during it (mine: c5 gate ended
before 17:33Z; the c6 gate ran after 18:12:34Z). Binary: `amq-squad` built offline with the
embedded pi from db692811 (`scripts/build-pi-runtime.sh build`, the S3 download cache read
in place, the npm cache copied to the job tmp, nothing written under `~/.amq-squad/spike`).
Owner config before and after: `~/.claude/settings.json` 65fb8dfe…, `~/.codex/config.toml`
21911a00…, unchanged; doctor's config diff after the pi part: both match the preflight copy.
Pi seats ran with a scratch HOME under the job tmp; `amq-squad launch` for the Codex seat
refreshed the owner's preflight copies (as milestone A's run did).

| item | result |
| --- | --- |
| Claude: a fresh per-seat CLAUDE_CONFIG_DIR authenticates? | **No**: Claude Code 2.1.310, `claude -p` with a fresh dir: "Not logged in · Please run /login", exit 1 in 0.8 s, `apiKeySource: none`. Ruled: the owner token file (commit 6); the Claude rows (plugin load, probes (a) and (b), the S4 fault table, the Claude half of the role matrix) move to a second bounded session once the owner has written the token |
| 7.5 Codex seat (real, codex-cli 0.163.0) | launched through `amq-squad launch` (trust_exact_subject, applied, up, identity proof); the seat stopped at codex-cli's folder-trust prompt: the fixture is its own git repository (`build/scratch-7-5/repo`), and codex trusts by repository root, not by an ancestor directory, so the worktree's trust did not carry. The prompt was not answered; teardown by `tmux kill-server`, no seat process left. 7.5 stays at the fixture level (TestCodexSeatEndsWithAHarnessPushedBranch); `amq-squad trust <path>` goes on the owner's list |
| R12 N=5 through `amq-squad scheduler run` (scheduler-side half) | 5 of 5 completed through the real process (request file, seat_up, AMQ doorbell, `pi run`, `amq_task_done`, seat commit, harness commit with numstat `1 1 calc.go`, task completed); worktree exact 5/5, go test confirmed 5/5, independent go test 5/5, zero false successes; final line exact 1/5: the worker role ends with a status message to the lead after the probe's RESULT line (a conflict of the probe's predicate with the worker role, not of the process). Re-run with the probe role in part 2 |
| R19 run 1 through the process | **PASS** in 251 s: lead first tool `amq_status`, no write or bash (it has none), 3 classed and declared tasks, 3 completed through the commit step (3 harness commits), `CONVERGED`, TestMax, TestReverse, TestIsEven pass in an independent go test |
| R19 run 2 | **FAIL, a defect found**: t1 was refused (`outside_declared_files`), the lead reset it, and the seat's next lease inherited the refused diff in its worktree, so every later attempt was refused for the same file: 15 refusals in 20 minutes, stopped by hand. Fixed in commit 6 (CleanSeat); re-run in part 2 |

Stand-ins in the R19 and R12 runs, exactly: the pi launch (AMQ 0.107.0 has no pi adapter:
the harness is the External runner, reads the request file, posts seat_up with `scheduler
post`, and runs one `pi run` turn per doorbell on a persistent session id); the wake (the
harness reads the AMQ doorbell message from the seat's inbox and starts the turn with it);
the merge train (after a completed task the harness fast-forwards main to the seat branch
so the lead's read-only view sees the files). Real: the scheduler process, the store, the
AMQ doorbell and lead notice, DefaultRouter with `--default-pool` (temporary), the
usage-limit detector, the commit step, the External launcher's core half, the lead and the
worker sessions with their hooks and tools.

Findings: (1) the R19 loop above; (2) R12 run 1: the worker first ran `go test` in the
repository root, which the task's `project_path` names, before correcting to its worktree;
(3) `seat_request_failed` logged every tick (204 lines in R19 run 1) while the one seat of
the list was busy: the seat cap did not count the seat list's slots.

## Commit 6: the Claude seat token, the seat worktree in the context, CleanSeat

### Built

- The owner token file (ruling: option 1, with the reviewer's conditions). `claudeseat`:
  `ReadToken` reads `~/.amq-squad/claude-token` only: a regular file (no symlink), owned
  by the user, no group or world bits, in an owner-only directory, one token on one line;
  otherwise `claude_token_insecure` with the reason (never the content). seat-exec refuses
  a Claude seat without it (no fallback to the owner's config dir) and sets
  `CLAUDE_CODE_OAUTH_TOKEN` from it for Claude seats only; a parent-supplied value is
  dropped for every seat whatever the runtime declares. The Claude runtime's credential
  need is that one name (`claudeseat.CredentialNeeds`); the launch record notes
  `credential_source: owner token file`, never the value. The file is in
  `grants.Implicit`, and guard denies any seat call whose arguments name it (a read, a
  shell command, a write). doctor: `claude seat token` (presence, mode, owner, age; warns
  past the 30-day rotation window, a proposal). The per-seat settings.json carries
  `env.CLAUDE_CODE_SUBPROCESS_ENV_SCRUB=1`, the candidate Bash-tool credential scrub, to be
  confirmed in the Claude session (condition 3).
  Residual (recorded as ruled): the token is in the seat process environment, readable by
  the seat's shell tool and its children (unless the scrub above holds), the same reach a
  same-UID process had to the Keychain credential before, bounded to the owner's
  subscription (plan 7.3).
- The seat context names the seat's own working tree (`worktree` in `amq-squad context`
  from the published launch record, else the core's staged one; a text line and, in the pi
  package's context section, "Your working tree is …: run every command and make every
  change there. A task's project_path names the repository, not your working tree."). The
  Claude hook injects the whole context JSON, so it carries the field too.
- `seatcommit.CleanSeat` and the scheduler's `SeatPreparer` hook: before an idle seat gets
  a new lease, its worktree returns to the commit ledger's parent; a leftover diff (a
  refused and reset, failed or expired attempt) is snapshotted and kept on the ledger
  (`discarded`: parent, tree, diff digest, reason) as evidence, then the seat branch is
  reset to the parent and untracked, non-ignored files are removed; a quarantined seat is
  never cleaned (its tree is the owner's evidence). The process logs `seat_cleaned`.
- `scheduler run`'s seat cap is the grant's, at most the seat list's slot count.

### Tests

| row | test |
| --- | --- |
| the token from the owner file only; a parent value replaced; a Codex seat gets none | TestClaudeTokenComesOnlyFromTheOwnerFile |
| missing, readable by others, a symlink, a group-writable directory, two lines: claude_token_insecure, the refusal never prints the token | TestClaudeSeatRefusesWithoutASecureTokenFile |
| condition 2: a canary token through a full fake-provider seat launch appears in no file and no argv, only in the provider env | TestClaudeTokenCanaryAppearsNowhereButTheProviderEnv |
| doctor row: missing, ok, past the window, wrong mode | TestDoctorClaudeTokenRow |
| guard denies a read, a shell command and a write naming the file | TestGuardDeniesTheClaudeTokenFile |
| the launch record's credential source | TestLaunchRecordsTheCommitBaseAndKeepsPreflight (extended) |
| the context names the seat worktree (staged, then published) | TestContextNamesTheSeatWorktree; pi `context section names the seat's working tree` |
| CleanSeat discards a leftover diff (with a seat commit and an untracked file), keeps it on the ledger, the next attempt commits | TestCleanSeatDiscardsALeftoverDiff |
| the preparer runs before a grant; an error grants nothing | TestPreparerRunsBeforeAGrant |

Revert check: 9 rows, `docs/v3-spike/2b2b-evidence/c6-revert-check.txt` (ALL ROWS
CONFIRMED). Gate: `docs/v3-spike/2b2b-evidence/c6-gate.txt`, PASS (after the live part 1).

## Live session, part 2 (2026-10-24, 18:23:54Z to 18:32:45Z; processes gone by 18:33:12Z)

Binary built offline from f3be5553 (`amq-squad v2.31.0-326-gf3be5553`, the embedded pi
with the worktree context line). Same stand-ins as part 1. No gate ran in the window.

| item | result |
| --- | --- |
| R19 run 1 | **PASS** in 132 s: first tool `amq_repo_list`, no write or bash, 3 classed tasks, 3 completed through the commit step (3 harness commits), `CONVERGED`, all three tests pass; no `seat_request_failed` (the slot cap) |
| R19 run 2 | **PASS** in 176 s: first tool `amq_task_list`, the same shape |
| R12 N=5 with the probe role | 3 of 5 strict passes (predicate v2: read, go test, harness-commit numstat `1 1 calc.go`, independent go test, final line exactly `RESULT: PASS`); all 5 completed through the process with an exact diff and zero false successes; the 2 misses are the final line only (the model ended with "t1 done." after the RESULT line) |

Evidence of both parts, redacted (home as `~`, the job tmp as `$JOB`; transcripts, decision
logs, summaries, the scheduler logs, the Codex pane, the probe stream, owner config hashes
and doctor's diff, the run scripts): `docs/v3-spike/2b2b-evidence/live/`.

## Credential exposure (commit 3, found at the end checkpoint)

`docs/v3-spike/2b2b-evidence/c3-revert-check.txt`, committed in commit 3 (the scheduler
process) and carried by the commits after it on this branch (never pushed), held one failing
test line that printed this session's whole process environment, including two credential
values. Cause: `TestDoorbellAndLeadNoticeSend` formatted the environment slice into its
failure message, and the revert check of commit 3 captured that failure. Rewrite: the branch
range v3..v3-2b2-b was rewritten with `git filter-branch --tree-filter`, redacting
credential-like `NAME=value` pairs in the evidence logs (commits 3 to 6 got new ids:
612a3d3a, 47a9cbbc, db84f2ab, 53cafca3; commits 1 and 2 are unchanged), and the backup ref was
removed. The lead verified that no commit reachable from the v3 branches carries either
value; the old objects remain in the shared object store through this worktree's reflog
(no gc or reflog expiry by me: the owner decides, with the rotation). The fix: the check no
longer formats the environment, and a row asserts a failing environment check never prints
another variable's value (TestEnvironmentCheckNeverPrintsTheEnvironment, a revert row of the
end commit).

## Commit 7 (end): CleanSeat conditions, the PR binding

### Built

- CleanSeat, as ruled a required part of the lease path, with the two conditions: (1) before
  the worktree is reset, the leftover diff is kept on the commit ledger with the task id and
  the reason of the seat's last ended attempt (for a refusal `commit_refused: <reason
  code>`), the tree and its diff digest; (2) it runs only between leases: a seat that holds a
  lease or a refused report keeps its worktree untouched (an error, so the scheduler skips the
  seat that tick).
- The PR binding for 3-2's merge train: `seat commit --open-pr` (and `scheduler run
  --open-pr`) opens, after the push, the seat branch's draft PR with gh as the harness
  (`gh pr create --draft --head <branch> --base <base>`, the harness commit message as the
  body), or finds the open one (`gh pr view <branch>`), and the recorder binds the task:
  **`pr_number`** (int, the PR) and **`pr_head`** (string, the pushed head), additive task
  fields, set by the commit step and re-set only by a later commit step on the same task
  (`task.Store.BindPR`). Without `--open-pr` only `pr_head` is bound. A gh failure is a note,
  not a refusal. The merge train supplies the PR number and refuses a PR with no bound task
  (`task_unbound`, 3-2's side).
- The scheduler-side test env check no longer prints the environment (above).

### Tests

| row | test |
| --- | --- |
| condition 1: the discarded diff on the ledger with the task id, `commit_refused: outside_declared_files`, the tree and digest | TestCleanSeatDiscardsALeftoverDiff (extended) |
| condition 2: never mid-task | TestCleanSeatNeverRunsMidTask |
| the commit step opens the draft PR and returns its number and the pushed head; a re-run finds it | TestCommitStepOpensAndBindsThePR |
| the scheduler binds `pr_number` and `pr_head` (`pr_bound`); a binding needs a head | TestCommitBindsThePR |
| `seat commit --open-pr` binds the task | TestSeatCommitOpenPRBindsTheTask |
| a failing environment check never prints the environment | TestEnvironmentCheckNeverPrintsTheEnvironment |

Revert check: 6 rows, `docs/v3-spike/2b2b-evidence/c7-revert-check.txt` (ALL ROWS
CONFIRMED). Gate: `docs/v3-spike/2b2b-evidence/c7-gate.txt`.

## End checkpoint

### Definition of done

| row | status | evidence |
| --- | --- | --- |
| 7.1 a diff inside declared_files commits and pushes | met | TestCommitInsideDeclaredFilesCommitsAndPushes; R19 runs (6 harness commits on real pi seats) |
| 7.2 one file outside is refused with a reason code (S6 W5) | met | TestCommitOutsideDeclaredFilesIsRefused; live R19 part 1 run 2 (real refusals) |
| 7.3 a lead widening commits | met | TestLeadWideningCommits |
| 7.4 a widening onto a protected path is refused and raises the growth gate | met | TestWideningOntoProtectedPathNeedsTheGrowthGate; TestSeatCommitAndWidenThroughTheGrowthGate (gate record, owner's signed answer, widen, push) |
| 7.5 a Codex seat under workspace-write ends with a harness-pushed branch | fixture only | TestCodexSeatEndsWithAHarnessPushedBranch; the real Codex seat stopped at codex-cli's folder-trust prompt (codex trusts by repository root, not by ancestor directory); waits for the owner's `amq-squad trust` of the fixture path |
| 9 role-delivery matrix | not extended | milestone A's pointer arm N=1 stands; the N=3 matrix needs the Claude seats, which wait for the owner token (and the Codex cells for the trust step) |
| 10 global config | met | the Codex sandbox refusal and the post-exit diff (milestone A); the quarantine (TestGlobalConfigQuarantineKeepsTheTreeAndPushesNothing, TestQuarantinedSeatGetsNoLease); live: owner config unchanged, doctor's diff clean |
| ruling 15 row 1: the process starts, dispatches a seeded task to a fake launcher, stops on a usage-limit event | met | TestProcessDispatchesAndStopsOnAUsageLimit; TestSchedulerRunVerbDispatchesRingsAndStops |
| ruling 15 row 2: 4-1's R19 loop against this process | met with the named stand-ins | live part 1 run 1 PASS, run 2 FAIL (the loop defect, fixed), part 2 runs 1 and 2 PASS |
| R12 N=5, the scheduler-side half | met for the process | 10 of 10 probes completed through the process with exact harness-commit diffs and zero false successes; strict predicate 1/5 (worker role) and 3/5 (probe role), the misses all final-line only; the registry half comes with 2b-3's probe |
| status seat role join | met | TestSeatRoleJoinsFromTheLaunchRecord |
| AMQ_SQUAD_BIN | met | TestSeatExecPinsAMQSquadBin |
| the Claude hook unit (claude_hooks_missing, the permission mode recorded) | met, per-seat config dir | commit 4 rows |
| the owner token | met in code | commit 6 rows; the live Claude rows wait for the token |
| the S4 fault table against a real Claude seat; probes (a), (b); plugin load; Bash-tool scrub | not run | the second bounded session, once the owner has written `~/.amq-squad/claude-token` |

### Measured

- seat-exec wall time to exec, median of 10 (`end-seatexec-timing.txt`): 50.8 ms (wrapped
  54.1 ms, provider alone 3.3 ms; the identity proof against the installed amq included).
  Milestone A measured 21.6 ms; the Claude seat now adds the config dir check (digest of the
  managed files), the worktree settings read and the token file read, and the host was busier
  (the commit step median below doubled against commit 1's 121 ms on the same fixture).
- commit step (`end-commit-timing.txt`): commit and push 256 ms, refusal 103 ms (medians of 10).
- scheduler tick at 100 tasks (`end-tick-budget.txt`): 12.1 ms median.
- live: R19 132 s to 251 s per converged run; R12 35 s to 72 s per probe.

### Manifest statuses of the 2b-2 Replaces rows

| row | lines / target | status |
| --- | --- | --- |
| internal/launch/record.go | 250 / 250 | shrunk (the fields of this milestone fit by merging comment lines) |
| internal/cli/context_resolution.go | 302 / 551 | shrunk (2b-5) |
| internal/cli/launch.go | 138 / 300 | shrunk |
| internal/cli/agent_args.go | 88 / 200 | shrunk |
| internal/cli/agent_defaults.go | 53 / 300 | shrunk |
| internal/cli/team_launch_launchapi.go, team_launch.go, bootstrap.go, model_defaults.go | - | deleted |
| internal/cli/amq.go, amq_send.go, amq_env.go | 417, 314, 178 | accepted-3 (owners 4-3, 3-2, 4-3) |

New rows of milestone B: internal/seatcommit (commit, git, ledger, policy, widen),
internal/claudeseat (claudeseat, token, owner_unix, owner_other), internal/schedproc (process,
amq, committer, slots, limits, prepare), internal/task (scope, quarantine), internal/cli
(seat_commit, task_widen, scheduler_run, lead_scheduler); all fate new, status new.
`plugins/embed.go` is outside internal/ and has no row. manifest-check --final: OK.

### Rulings asked or recorded

1. Recorded: dontAsk; the per-seat CLAUDE_CONFIG_DIR; the token file with its residual; a
   Claude resume across launches unsupported; the default pool temporary until 2b-4.
2. Open: the second bounded session (the Claude rows) needs the owner's token file.
3. Open: 7.5 needs the owner's `amq-squad trust` of the fixture path (the fixture is torn
   down; it is recreated at the same path for the run: `<worktree>/build/scratch-7-5/repo`).
4. Open: the R12 predicate's final-line rule against a seat role that reports to its lead.
5. The 2b-4 router swap is one line plus `Flush` per tick at the final rebase.

## Final rebase

Rebased onto v3 at 0b4472b3 (4-3 and 4-5 merged): manifest-only conflicts (line counts),
one in grants.go (4-3's `FleetKeys` beside `MergeSizeCapFiles` in the Narrowing). Gate on the
rebased head: `docs/v3-spike/2b2b-evidence/final-gate.txt`, PASS (no live session on the host).
