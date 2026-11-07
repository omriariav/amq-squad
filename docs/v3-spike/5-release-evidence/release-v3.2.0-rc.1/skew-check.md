# v3.2.0-rc.1 skew check (step 1)

Tag commit candidate: fcc0beb72cd3e36f55002aab5a9446a570eedf30 (origin/v3 after #903's merge; `git rev-parse origin/v3`).
Base: v3.1.2 = 5bd394e6dcf9fd06254f80ebc846d92130dc9048 (`git rev-parse 'v3.1.2^{commit}'`).

Command: `git diff 5bd394e6dcf9fd06254f80ebc846d92130dc9048 fcc0beb72cd3e36f55002aab5a9446a570eedf30 -- internal/routing internal/scheduler internal/sealchain internal/seatcommit internal/task` (21 files, +2588/-101), json-tagged lines added/removed in non-test files.

Sealing call sites at the candidate (non-test): scheduler/exits.go (exits queue: sealchain.Write/Check), seatcommit/ledger.go (commit ledger), task/verdict.go (verdict and snapshot seals over explicit fields), routing (seed identity only).

Added json-tagged fields and where they live:
- task.Task: Budget, BudgetExtensions (task file, unsealed).
- task.Lease: Attempt, BudgetEndsAt (task file, unsealed; the snapshot/verdict seals in verdict.go are unchanged between the two commits).
- task.BudgetExtension: Actor, At, By (new type inside the unsealed task file).
- seatcommit/quarantine.go:382 `Seq *int` (a local decode struct for the head's seq; not a sealed body).
- WaitReason, BlockReason, BlockRoute, BlockDetail: moved (re-aligned), same tags; not added.
No change to the sealed exits queue (exitsLedger/exitItem) or the commit ledger (Ledger and its nested types): #909 and #904 are not in rc.1.

Result: no sealed struct changed; rc.1 and 3.1.2 read each other's ledgers. Rollback note: a 3.1.2 rewrite of a task after a rollback drops its unsealed budget fields (Budget, BudgetExtensions, BudgetEndsAt) and Lease.Attempt.

## Rerun on the tag commit

2026-11-06T12:14:01Z: `git diff fcc0beb72cd3e36f55002aab5a9446a570eedf30 3966afb72b85e1ab6c0fc8c879f1c5ef03f92a40 -- internal/routing internal/scheduler internal/sealchain internal/seatcommit internal/task` is empty; the only Go file #910 changes is internal/claudeseat/relprep_version_test.go. So the result above holds at the tag commit 3966afb72b85e1ab6c0fc8c879f1c5ef03f92a40 (#910 merge).
