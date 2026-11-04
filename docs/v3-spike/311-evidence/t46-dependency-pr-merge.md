# t46: dependency release follows PR merge

The task store now persists `pr_merge_commit` on the task bound to a PR. The merge train records that fact after a successful merge, including reconciliation of a lost merge response or an earlier journaled merge.

Dependency gating uses that durable fact:

- A dependency without a bound PR still releases on task completion.
- A bound dependency releases only after its merge commit is recorded.
- Until then, including after task completion or cancellation, the dependent waits with `waiting for <id>'s PR #<n> to merge`.
- Rebinding a task to a different head clears any stale merge fact.

A PR merged by hand outside the merge train does not create the task-store fact. Its dependents intentionally remain waiting until the lead resets or otherwise clears them.

Verification: `go test ./internal/task ./internal/mergetrain ./internal/scheduler ./internal/status`.
