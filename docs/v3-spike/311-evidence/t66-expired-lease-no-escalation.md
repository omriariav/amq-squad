# T66: expired leases count without escalation

Release behavior: an expired lease consumes one of `MaxAttempts`, but the retry stays on the same routing tier, retains the lead's pin, and does not mark the pinned entry `used_before`. Work failures still escalate and policy refusals still neither escalate nor count.

## Acceptance evidence

1. `TestExpiredAttemptCountsWithoutEscalating` proves the first expiry retries the same entry at the same tier with `lead_pin`, leaves the task's pool intact, and records no `used_before` skip. Revert command: `go test ./internal/routing -run TestExpiredAttemptCountsWithoutEscalating -count=1`. Mutating the expiry row to `{escalate: true, count: true}` first fails at `brief3112_test.go:86` with `expiry escalated or dropped its pin`.
2. The same routing row proves three expiries produce `retries_exhausted`; `TestMaxAttemptExpiriesBlockForLead` proves the scheduler's usual blocked state and lead route. Revert command: `go test ./internal/routing -run TestExpiredAttemptCountsWithoutEscalating -count=1`. Mutating the expiry row to `{}` first fails at `brief3112_test.go:92` with `3 expiries did not exhaust retries`.
3. The escalation mutation in row 1 specifically proves an expiry is not a work failure.
4. `TestEveryCommitRefusalCodeIsClassified` pins expiry as its own `{escalate: false, count: true}` outcome row. Revert command: `go test ./internal/routing -run TestEveryCommitRefusalCodeIsClassified -count=1`. Removing that row first fails at `brief3112_test.go:123` with `expired classification = {escalate:false count:false} (present false), want count without escalation`.

No new Go reason, rule, block, wait, outcome, or decision name was introduced, so `pi/extensions/lib/words.ts` needs no new mapping. The PI words completeness row remains green.

## Verification

- `go test ./internal/routing ./internal/scheduler -count=1`
- `cd pi && npm ci --offline --ignore-scripts && npm test`

Both gates ran with a temporary `HOME` and without live AMQ variables. The PI gate passed 110/110 tests; its socket cases required the normal unsandboxed test environment.
