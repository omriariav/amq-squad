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
