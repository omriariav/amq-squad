# t48: merge records are bound to the merged head

The durable task merge fact is the pair `pr_merge_head` and `pr_merge_commit`. A bound dependency releases only when the recorded head equals its current `pr_head`.

At the start of a real train run, reconciliation handles three cases:

- A matching journal entry records the task merge.
- A journal entry for a stale PR binding or head writes a `reconcile` audit row and is skipped without stopping the other PRs.
- With no journal entry, a PR the server already reports merged at the bound head is recorded without another merge call. A different server head is audited and not recorded.

Dry runs never reconcile task merge facts. Replaying an identical fact is read-only.

Revert rows:

1. `TestStaleJournalMergeDoesNotReleaseAReboundDependency`
2. `TestMismatchedReconcileContinuesWithOtherPRs`
3. `TestRealRunRecordsAHandMergedPRWithoutAMergeCall`
4. `TestServerMergedAtAnotherHeadIsAuditedAndNotRecorded`
5. `TestDryRunDoesNotRecordAnAlreadyMergedPR`
6. `TestRecordedMergeReconcileIsIdempotent`

Verification: `go test ./internal/task ./internal/mergetrain ./internal/scheduler ./internal/status` with live AMQ seat variables removed and a temporary `HOME`.
