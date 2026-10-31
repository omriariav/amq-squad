# 3.0.2 t12fix: a task re-pin is honoured until the first attempt is granted (execution note)

Branch v3-302-t12fix on origin/v3 2f88eca1. Brief: 3.0.2-t12fix, the
reviewer rulings of 2026-10-30 and the Codex pass on c338d3fe.

## The seat's commit

The dev seat of the first squad run on 3.0.1 built task t12 as c338d3fe
(PR #815, on a stale base, a203f183): one guard in `Store.Set`
(internal/task/lease.go) refusing a pool change once `Attempt > 0`, with a
row each in internal/task/task_test.go, internal/cli/task_test.go and
internal/routing/router_test.go. It was cherry-picked onto origin/v3 first,
unchanged and with its harness trailers (ee48fe4b); the merge into the
tip's lease.go, which carries `RefusedPaths` and the other changes the
seat's base lacked, was clean.

## The corrections

1. **The boundary is the first granted attempt.** The grant is the routing
   record (`grant` raises `Attempt`, and `endAttempt`
   writes the granted pool into the attempt row), so the seat's guard stands where it was and only the words
   change: the doc comment, the refusal, and the test names say "after the
   first attempt is granted", not "after the first claim". A task never
   granted is re-pinned freely. A task back in pending after an unclaimed
   expiry keeps its attempt count and is refused; the message says so.
2. **A named refusal code.** `ReasonRePinAfterAttempt` (`repin_after_attempt`)
   and `ErrRePinAfterAttempt`, built from it as the quarantine refusal is,
   wrap every refusal, so callers and rows match the code with `errors.Is`.
   `task set` prints the code (`task set: repin_after_attempt: ...`) and the
   message ends with `use task reset --pool`. Reset takes only a blocked
   task, and on a leased task only the holder may block, so the hint names
   the whole path: release its lease if it has one and block it if it is
   not blocked, then use task reset --pool.
3. **The router row.** The seat's row called `f.decide` twice; each call
   built a new router and `Decide` persists nothing, so the row passed with
   the change reverted. The row now runs one rooted router over a real task
   store: `Route` records the decision for attempt 1 on the first pin, the
   store re-pins before the grant, the next `Route` of the same router takes
   the new pool under `lead_pin`, and a restarted router reads the new entry
   from the decision log. After `Grant`, the store refuses a further re-pin
   with the code and the granted pool stands.

README's task section does not describe re-pins (its "Re-pin" lines are
the Claude binary pin), so it is unchanged.

## The rows, and what fails each

| Row | Covers |
| --- | --- |
| task: `TestSetRepinsOnlyBeforeTheFirstGrant` | re-pin before the first grant honoured; after a grant (unclaimed) refused with the code, task unchanged; after the unclaimed expiry (pending, attempt 1) refused, task unchanged |
| cli: `TestTaskCLISetRepinBoundary` | `task set` before the grant; after `task claim` the error matches the code and ends with `use task reset --pool`; release, block and `task reset --pool` then succeed |
| routing: `TestPendingRepinReevaluatesTheNextRoute` | as in correction 3 |

Mutations run against the three rows (each restored after):

| Mutation | Fails |
| --- | --- |
| the guard removed | task, cli, routing |
| the guard moved to the claim (`Lease.ClaimedAt != nil`) | task, routing |
| the code not wrapped (`%v` for `%w`) | task, cli, routing |
| the reset hint dropped | task, cli |
| the route keeps attempt 1's recorded entry over the re-pin | routing |
| `remember` does not re-record a changed entry | routing (the restart read) |

Gate: `go build ./... && go vet ./internal/task ./internal/routing
./internal/cli && go test -count=1 ./internal/task ./internal/routing
./internal/cli`, green.
