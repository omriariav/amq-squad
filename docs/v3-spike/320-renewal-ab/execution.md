# 3.2.0 renewal provenance and Codex admission

Base: `c65a709c690dd0ed5877076cfeebf470da339262` (`origin/v3`).

## Round 2 carry

The reissue first refreshed the harness-cleared index stat data with
`git update-index -q --refresh`; it exited 0 with no output. The exact patch
`81f60af0.patch` had sha256
`ee0e9fff9561104326d52de0c2130680c716f30c2d7bcd93854d7b61cd3ddbe9`.
`git apply --3way` then exited 0 and produced exactly the 15 paths in
`blobs.txt` (sha256
`23e0f08178907302f437a781d6cb69b567c690033da96cf02cc782ffad1151c6`).
Every working blob matched its listed blob, with no mismatch output, and
`git add -A && git write-tree` printed
`d31738a38cc2aadb0873db6fdbf439b748ed5f25`.

This change labels every lease renewal as `activity` or `manual` while
retaining `renewals` as the total. The first labelled activity renewal of each
attempt writes the existing `lease_renewed` decision even when manual or
pre-upgrade unlabelled renewals came first. `task show` prints total, activity,
manual, and any unlabelled remainder.

A Codex seat's manual fallback now has two independent admission legs, checked
at every doorbell: exact-command trust read once at that seat's current launch,
or a tool result row written by that same handle since the launch.
Relaunch replaces the trust value and activity boundary. Every runtime's
doorbell also carries the long-command rule before the Codex-only fallback.

## Positive rows

All focused commands used scratch HOME, CODEX_HOME, and Go build cache, the
existing read-only module cache, and `GOPROXY=off`:

```sh
env HOME=/tmp/amq-t148/home CODEX_HOME=/tmp/amq-t148/codex GOCACHE=/tmp/amq-t148/gocache GOMODCACHE="$(go env GOMODCACHE)" GOPROXY=off go test -count=1 -run 'TestRenewalSourcesAndAttemptsAreRecordedSeparately|TestHolderReclaimsAreManualOnBothClaimPaths' ./internal/task
env HOME=/tmp/amq-t148/home CODEX_HOME=/tmp/amq-t148/codex GOCACHE=/tmp/amq-t148/gocache GOMODCACHE="$(go env GOMODCACHE)" GOPROXY=off go test -count=1 -run 'TestEvidenceIngestRenewsTheClaimedLease|TestEachAttemptLogsOnlyItsFirstActivityRenewal|TestLegacyUnlabelledRenewalsDoNotHideFirstActivity' ./internal/cli
env HOME=/tmp/amq-t148/home CODEX_HOME=/tmp/amq-t148/codex GOCACHE=/tmp/amq-t148/gocache GOMODCACHE="$(go env GOMODCACHE)" GOPROXY=off go test -count=1 -run '^TestCodexLaunchAdmissionControlsTheDoorbellManualRenewFallback$' ./internal/schedproc
```

Each command exited 0.

| Row | Positive assertion |
| --- | --- |
| A1 | Claim, real `task renew` CLI call, then PostToolUse gives total 2, activity 1, manual 1, and exactly `first activity renewal of attempt 1`. |
| A2 | `task renew`, holder re-claim through both `Claim` and `ClaimServing`, and hook activity update their own counts and `last_renewal`. |
| A3 | A second activity renewal writes no second decision; release and re-claim starts attempt 2, whose first activity writes its own decision even after a manual renewal. |
| A4 | A persisted lease with total 3 and no labels accepts activity, writes `first activity renewal of attempt 1 after 3 unlabelled renewals`, and `task show` prints `4 renewals (1 activity, 0 manual, 3 unlabelled)`. |
| B1 | A trusted current Codex launch has no manual fallback before any ingest. |
| B5 | An untrusted launch stays manual after a row naming another handle, including one placed in the target handle's ledger file. No config is read at ring time. |
| B6 | This handle's own tool result after launch removes the fallback; an untrusted relaunch restores it until another own tool result row arrives. |
| B7 | Trusted Codex, untrusted Codex, and non-Codex doorbells each contain the long-command rule exactly once; only untrusted Codex contains the manual sentence, after the long-command rule. |

## Controlled reverts

Line numbers in A1 to B7 below are as of 81f60af0 (round 1's head), except
A1, A3 and A4, which the follow-up re-measured on its own head; their
commands are in "Follow-up: rows and words".

Each revert changed only the expression named below, ran the focused command,
copied the first failing line, and was immediately restored.

### A1

Reverted `t.Lease.ActivityRenewals == 1` to
`t.Lease.Renewals == 1` in `renewLeaseOnToolResult`.

```sh
env HOME=/tmp/amq-t148/home CODEX_HOME=/tmp/amq-t148/codex GOCACHE=/tmp/amq-t148/gocache GOMODCACHE="$(go env GOMODCACHE)" GOPROXY=off go test -count=1 -run '^TestEvidenceIngestRenewsTheClaimedLease$' ./internal/cli
```

First failing line, exit 1:

```text
hook_ingest_lease_test.go:67: first-renewal decisions = [], err <nil>
```

### A2

Reverted the `RenewAttempt` source argument from `renewalManual` to
`renewalActivity`.

```sh
env HOME=/tmp/amq-t148/home CODEX_HOME=/tmp/amq-t148/codex GOCACHE=/tmp/amq-t148/gocache GOMODCACHE="$(go env GOMODCACHE)" GOPROXY=off go test -count=1 -run '^TestRenewalSourcesAndAttemptsAreRecordedSeparately$' ./internal/task
```

First failing line, exit 1:

```text
brief320_test.go:94: manual renewal: renewed=true lease=&{Holder:dev GrantedAt:2026-10-24 12:00:00 +0000 UTC ExpiresAt:2026-10-24 12:12:00 +0000 UTC ClaimedAt:2026-10-24 12:00:00 +0000 UTC RenewedAt:2026-10-24 12:02:00 +0000 UTC Renewals:2 ActivityRenewals:1 ManualRenewals:1 LastRenewal:activity Attempt:1 BudgetEndsAt:2026-10-24 13:00:00 +0000 UTC Snapshot: SnapshotTag: SnapshotCommand: Verdict:<nil>} err=<nil>
```

### A3

Reverted the decision condition from `ActivityRenewals == 1` to
`ActivityRenewals >= 1`.

```sh
env HOME=/tmp/amq-t148/home CODEX_HOME=/tmp/amq-t148/codex GOCACHE=/tmp/amq-t148/gocache GOMODCACHE="$(go env GOMODCACHE)" GOPROXY=off go test -count=1 -run '^TestEachAttemptLogsOnlyItsFirstActivityRenewal$' ./internal/cli
```

First failing line, exit 1:

```text
hook_ingest_lease_test.go:160: attempt 1 decisions = [{At:2026-10-23 12:00:00 +0000 UTC Decision:{Kind:lease_renewed TaskID:t1 Seat:cto Pool: Reason:first activity renewal of attempt 1 Stage: ReasonCode:}} {At:2026-10-23 12:02:00 +0000 UTC Decision:{Kind:lease_renewed TaskID:t1 Seat:cto Pool: Reason:first activity renewal of attempt 1 Stage: ReasonCode:}}], err <nil>
```

### A4

Reverted legacy handling by requiring
`Renewals == ActivityRenewals + ManualRenewals` before the first-activity
decision, which treats unlabelled renewals as already classified activity.

```sh
env HOME=/tmp/amq-t148/home CODEX_HOME=/tmp/amq-t148/codex GOCACHE=/tmp/amq-t148/gocache GOMODCACHE="$(go env GOMODCACHE)" GOPROXY=off go test -count=1 -run '^TestLegacyUnlabelledRenewalsDoNotHideFirstActivity$' ./internal/cli
```

First failing line, exit 1:

```text
hook_ingest_lease_test.go:241: legacy decision = [], err <nil>
```

### Follow-up gates

- `env -i PATH="$PATH" HOME=/tmp/amq-t154/home GOCACHE=/tmp/amq-t154/gocache GOMODCACHE="$(go env GOMODCACHE)" GOPROXY=off go build ./...`: exit 0. The sandbox printed a non-fatal module stat-cache write warning.
- `env -i PATH="$PATH" HOME=/tmp/amq-t154/home GOCACHE=/tmp/amq-t154/gocache GOMODCACHE="$(go env GOMODCACHE)" GOPROXY=off go vet ./internal/ledger/... ./internal/cli/...`: exit 0.
- `env -i PATH="$PATH" HOME=/tmp/amq-t154/home GOCACHE=/tmp/amq-t154/gocache GOMODCACHE="$(go env GOMODCACHE)" GOPROXY=off go test -count=1 ./internal/ledger/`: exit 0.
- `env -i PATH="$PATH" HOME=/tmp/amq-t154/home GOCACHE=/tmp/amq-t154/gocache GOMODCACHE="$(go env GOMODCACHE)" GOPROXY=off go test -count=1 -run 'TestEvidenceIngest|TestEachAttempt|TestLegacyUnlabelled|TestManualRenewalOmitsZeroActivityRenewals' ./internal/cli/`: exit 0.
- `env -i PATH="$PATH" HOME=/tmp/amq-t154/home TMPDIR="$TMPDIR" GOCACHE=/tmp/amq-t154/gocache GOMODCACHE="$(go env GOMODCACHE)" GOPROXY=off go test -count=1 ./internal/cli/`: exit 1. Its sole failure was the known sandbox PTY residual, `wait_posture_pty_test.go:88: tmux pane identity = "%0_3880\\n"`; every other CLI test passed.
- `PYTHONPATH=/tmp/amq-t148/python make skills-check`: exit 0; all 14 skill checks passed.
- `env -i PATH="$PATH" HOME=/tmp/amq-t154/home CODEX_HOME=/tmp/amq-t154/codex TMPDIR="$TMPDIR" GOCACHE=/tmp/amq-t154/gocache GOMODCACHE="$(go env GOMODCACHE)" GOPROXY=off PYTHONPATH=/tmp/amq-t148/python NPM_FLAGS=--offline make ci`: exit 2. Every Go package except `internal/cli` passed; its sole failure was the same known PTY residual at `%0_27555`. Make stopped before its later phases.
- `env HOME=/tmp/amq-t154/home GOCACHE=/tmp/amq-t154/gocache GOMODCACHE="$(go env GOMODCACHE)" GOPROXY=off go run ./scripts/redact-evidence -check docs/v3-spike/320-renewal-ab/`: exit 0.
- The source-input comparisons, `git diff --check`, and `gofmt -l internal/cli/hook_ingest_lease_test.go` exited 0 with no output. The absolute user-path and host-temp-path scans each printed `0`.

### B1

Reverted the launch-trust leg from
`admitted && admission.trusted` to
`admitted && admission.trusted && false`.

```sh
env HOME=/tmp/amq-t148/home CODEX_HOME=/tmp/amq-t148/codex GOCACHE=/tmp/amq-t148/gocache GOMODCACHE="$(go env GOMODCACHE)" GOPROXY=off go test -count=1 -run '^TestCodexLaunchAdmissionControlsTheDoorbellManualRenewFallback$' ./internal/schedproc
```

First failing line, exit 1:

```text
slots_test.go:180: a trusted Codex launch kept the manual fallback
```

### B5

Reverted exact-handle attribution by removing `row.Handle != handle` from the
ledger reader's refusal expression.

The B1 command above then exited 1. First failing line:

```text
slots_test.go:195: another handle's ingest supplied Codex activity admission
```

### B6

Reverted the ingest leg from `return err != nil || !active` to
`return true || err != nil || !active`.

The B1 command above then exited 1. First failing line:

```text
slots_test.go:199: this launch's own activity did not supply admission
```

### B7

Reverted the single append of the long-command sentence from `Doorbell.ring`.

The B1 command above then exited 1. First failing line:

```text
slots_test.go:236: trusted doorbell = "The scheduler leased you task t1 (work, class implement). Claim it with: amq-squad task claim t1 --me trusted. Declared files: none."
```

`scheduler.Config.LeaseDuration` exists. Zero becomes
`task.DefaultLeaseDuration`, and the scheduler uses the resulting value when it
grants a lease. The doorbell separately prints `task.DefaultLeaseDuration`.
This audit found no `LeaseDuration:` assignment under `internal/cli`, so both
are ten minutes today. A caller that sets the scheduler config would make the
doorbell number wrong. Replacing the doorbell argument with literal `11m0s`
is therefore a distinct controlled revert in Round 2.

## Doorbell consumer audit

```sh
rg -n 'Declared files:|activity hooks are not trusted|runs longer than the lease' --glob='*.go' --glob='*.sh' --glob='!*_test.go' internal cmd scripts
```

Exit 0. The grep shows that the words are built in one production place,
`internal/schedproc/amq.go`; no other Go or shell production code matches
them. A consumer that read the body by position would not be found by this
grep, so the audit makes no broader claim.

## Round 2

The three supplied inputs landed byte for byte. Their sha256 values are
`e860cd1fde099d0f187647fb397ee430e8bf345b258124a9830c1cbc57d50016`
for the frozen rc.1 task,
`9e3efb76a9425b1f60781578b0d0818810ef9c1fb248453fea201f5d8cb1dd4e`
for the skill paragraph, and
`694cedd67a2dde452c6cfe0df8745f5668be677001c39955c3f5f141399f0aea`
for the lease table rows.

### Stop and tool-result proof

`HasActivitySince` now accepts only a complete own `PostToolUse` or
`tool_result` row at or after the launch boundary. Stop still renews a lease
through `isLeaseActivity`, but it does not prove that the post-tool entry which
renews during a turn fires.

```sh
env -i PATH="$PATH" HOME=/tmp/amq-t152/home GOCACHE=/tmp/amq-t152/gocache GOMODCACHE="$(go env GOMODCACHE)" GOPROXY=off go test -count=1 -run '^TestCodexStopDoesNotProveThePostToolHook$' ./internal/schedproc
```

Reverted expression: `Stop` was counted again by `HasActivitySince`. Exit 1,
first failing line:

```text
slots_test.go:259: S1 Stop alone removed the manual fallback
```

### `HasActivitySince` table

Each table mutant used:

```sh
env -i PATH="$PATH" HOME=/tmp/amq-t152/home GOCACHE=/tmp/amq-t152/gocache GOMODCACHE="$(go env GOMODCACHE)" GOPROXY=off go test -count=1 -run '^TestHasActivitySinceRequiresACompleteOwnToolResultAtOrAfterTheBoundary$' ./internal/ledger
```

Each run exited 1 and the expression was restored immediately:

- Dropping `row.Malformed` first failed at `activity_test.go:71: T4 malformed row: got true, err <nil>, want false and nil`.
- Dropping `row.Truncated` first failed at `activity_test.go:71: T5 truncated row: got true, err <nil>, want false and nil`.
- Replacing the event predicate with `found = true` first failed at `activity_test.go:71: T6 other events: got true, err <nil>, want false and nil`.
- Dropping `&& row.Outcome != ""` first failed at `activity_test.go:71: T7 missing outcome: got true, err <nil>, want false and nil`.
- Dropping `row.Tool != ""` first failed at `activity_test.go:71: T8 missing tool: got true, err <nil>, want false and nil`.
- Disabling the `at.Before(since)` check with `false && at.Before(since)` first failed at `activity_test.go:71: T2 PostToolUse before boundary: got true, err <nil>, want false and nil`.
- Replacing `at.Before(since)` with `!at.After(since)` first failed at `activity_test.go:71: T1 PostToolUse at boundary: got false, err <nil>, want true and nil`.

### Ledger read error

```sh
env -i PATH="$PATH" HOME=/tmp/amq-t152/home GOCACHE=/tmp/amq-t152/gocache GOMODCACHE="$(go env GOMODCACHE)" GOPROXY=off go test -count=1 -run '^TestCodexLedgerReadErrorKeepsTheManualFallback$' ./internal/schedproc
```

Reverted expression: `return err != nil || !active` became
`return err == nil && !active`. Exit 1, first failing line:

```text
slots_test.go:302: read error removed the manual fallback
```

### Scheduler and launch wiring

The two scheduler option mutants used:

```sh
env -i PATH="$PATH" HOME=/tmp/amq-t152/home TMPDIR="$TMPDIR" GOCACHE=/tmp/amq-t152/gocache GOMODCACHE="$(go env GOMODCACHE)" GOPROXY=off go test -count=1 -run '^TestSchedulerRunWiresCodexActivityAdmissionToTheSessionLedger$' ./internal/cli
```

Removing `CodexActivityAdmission: slots.AdmitCodexActivity` exited 1 first at
`scheduler_roster_test.go:131: W1 scheduler launch options omitted CodexActivityAdmission`.
Removing `Root: root` from the real slots exited 1 first at
`scheduler_roster_test.go:147: W1 session ledger tool result did not remove manual renewal`.

The launch-time mutant used:

```sh
env -i PATH="$PATH" HOME=/tmp/amq-t152/home TMPDIR="$TMPDIR" GOCACHE=/tmp/amq-t152/gocache GOMODCACHE="$(go env GOMODCACHE)" GOPROXY=off go test -count=1 -run '^TestLaunchAdmitsCodexActivityTrustAndWarnsWithExactCommands$' ./internal/seatlaunch
```

Replacing `launched := o.Now().UTC()` with `time.Time{}` exited 1 first at:

```text
wake301_test.go:309: admission launch time = 0001-01-01 00:00:00 +0000 UTC, want 2026-11-11 00:30:00 +0000 UTC
```

### Initial offered claim

```sh
env -i PATH="$PATH" HOME=/tmp/amq-t152/home GOCACHE=/tmp/amq-t152/gocache GOMODCACHE="$(go env GOMODCACHE)" GOPROXY=off go test -count=1 -run '^TestClaimOfferedAcceptsOnlyTheLiveOfferForItsHolderAndPool$' ./internal/task
```

Reverted expression: the `else` in `claim` was removed so a first claim
incremented the renewal counters. Exit 1, first failing line:

```text
claim_offer_test.go:28: initial offered claim counted as a renewal
```

The renewal source passed by `ClaimOffered` cannot be observed on its first
claim because `claim` does not enter the renewal branch until a lease is
already claimed.

### Frozen rc.1 lease

Each legacy mutant used:

```sh
env -i PATH="$PATH" HOME=/tmp/amq-t152/home TMPDIR="$TMPDIR" GOCACHE=/tmp/amq-t152/gocache GOMODCACHE="$(go env GOMODCACHE)" GOPROXY=off go test -count=1 -run '^TestLegacyUnlabelledRenewalsDoNotHideFirstActivity$' ./internal/cli
```

- Requiring all renewals to be labelled before the decision exited 1 first at `hook_ingest_lease_test.go:241: legacy decision = [], err <nil>`.
- Keying the decision on `t.Lease.Renewals == 1` exited 1 first at `hook_ingest_lease_test.go:241: legacy decision = [], err <nil>`.
- Dropping `omitempty` from `manual_renewals` exited 1 first at `hook_ingest_lease_test.go:237: rewritten legacy task = {`.

Line numbers as of `b7663f14`, re-measured by the lead on 2026-11-11 in a
scratch worktree, one mutant at a time, with
`go test ./internal/cli -run '^TestLegacyUnlabelledRenewalsDoNotHideFirstActivity$' -count=1 -timeout 400s`.
The changed expressions: in `internal/cli/hook_ingest.go`,
`if t.Lease != nil && t.Lease.ActivityRenewals == 1 {` became
`if t.Lease != nil && t.Lease.ActivityRenewals == 1 && t.Lease.Renewals == t.Lease.ActivityRenewals+t.Lease.ManualRenewals {`
for the first and `if t.Lease != nil && t.Lease.Renewals == 1 {` for the
second; in `internal/task/task.go`, the tag
`json:"manual_renewals,omitempty"` became `json:"manual_renewals"` for the
third. This section first carried `:231`, `:231` and `:227`, where the two
assertions sat on #952's merge (`347ccf4e`); #954 moved them down ten lines.
The three runs' output is kept in the lead's gate files and is not in this
folder.

The restored row also proves the rewritten bytes contain
`"activity_renewals": 1` and `"last_renewal": "activity"`, omit the
`manual_renewals` key, and write exactly one decision.

### Doorbell duration

```sh
env -i PATH="$PATH" HOME=/tmp/amq-t152/home GOCACHE=/tmp/amq-t152/gocache GOMODCACHE="$(go env GOMODCACHE)" GOPROXY=off go test -count=1 -run '^TestCodexLaunchAdmissionControlsTheDoorbellManualRenewFallback$' ./internal/schedproc
```

Reverted expression: the doorbell's duration argument became literal
`"11m0s"`. Exit 1, first failing line:

```text
slots_test.go:236: trusted doorbell = "The scheduler leased you task t1 (work, class implement). Claim it with: amq-squad task claim t1 --me trusted. Declared files: none. A command that runs longer than the lease (11m0s) loses it: run long work in the background and poll it."
```

## Limits and facts

- An untrusted seat that never calls a tool keeps the manual-renew sentence. This is the safe side.
- "Once per attempt" does not survive an rc.1 rewrite of the task because the labels are dropped; the next activity renewal logs once more with the unlabelled suffix.
- The decision append error is ignored in `internal/cli/hook_ingest.go`; logging that error once is a 3.2.x item.
- A seat adopted by the scheduler instead of launched gains no admission and keeps the sentence until a launch.
- Admission is recorded before `Compile`, and the launch time is taken before the seat process starts. A failed launch leaves an admission for a seat that is not running, and its proof window opens early.
- Every ring of an unproven Codex seat scans its whole hook ledger. Bounding that scan is a 3.2.x candidate.

## Persistence compatibility

No sealed record format changed. The new fields are `omitempty` fields on the
task store's live lease JSON. Tasks use the journaled task store, while the
decision log and hook ledger retain their existing schemas. The existing
`lease_renewed` decision kind is reused.

An rc.1 binary can read and rewrite the task: unknown activity/manual labels
are dropped, but `renewals` remains the total and the lease still works. When a
new binary reads that rewritten lease, the remaining total is unlabelled and
the next labelled activity is again the first labelled activity for the live
attempt. No migration or schema gate is required.

## Gates

All final gate commands removed live AMQ and squad environment variables,
used scratch HOME and CODEX_HOME, and disabled module network access.

The following bullets are the carried Round 1 gate record:

- `go build ./...`: exit 0.
- `go vet ./...`: exit 0.
- `go test -count=1 ./internal/task/... ./internal/cli/... ./internal/schedproc/... ./internal/seatlaunch/...`: exit 0. Final package lines were task 47.012 s, cli 92.486 s, schedproc 75.687 s, and seatlaunch 36.398 s.
- First `NPM_FLAGS=--offline make ci`: exit 2 after every Go and HTML gate passed, because scratch HOME had no PyYAML. No product test failed.
- PyYAML 6.0.3 was installed into `/tmp/amq-t148/python` from the existing local pip wheel with `PIP_NO_INDEX=1`.
- Final `PYTHONPATH=/tmp/amq-t148/python NPM_FLAGS=--offline make ci`: exit 0. It included the full Go suite, documentation and skill gates, hook tests, offline `npm ci`, and pi tests: 120 passed, 0 failed, 1 skipped.
- `go run ./scripts/redact-evidence -check docs/v3-spike/320-renewal-ab/`: exit 0.

### Round 2 gate closeout

Each package gate was run separately with `-count=1`. The commands used the
same isolated environment as the controlled reverts, including scratch HOME,
scratch Go caches, and `GOPROXY=off`.

- `go build ./...`: exit 0.
- `go vet ./...`: exit 0.
- `go test -count=1 ./internal/task/...`: exit 0 in 119.204 s.
- `go test -count=1 ./internal/cli/...`: exit 1. The sole failure
  was the known host PTY residual
  `TestRealPTYWaitPostureRefusesNamedGateBeforeTimeout`, with first failure
  `wait_posture_pty_test.go:88: tmux pane identity = "%0_39762\\n"`.
- `go test -count=1 ./internal/ledger/...`: exit 0 in 1.837 s.
- `go test -count=1 ./internal/schedproc/...`: exit 0 in 179.015 s.
- `go test -count=1 ./internal/seatlaunch/...`: exit 0 in 21.854 s.
- `PYTHONPATH=/tmp/amq-t148/python make skills-check`: exit 0; all 14 skill
  checks passed.
- `env -i PATH="$PATH" HOME=/tmp/amq-t152/home CODEX_HOME=/tmp/amq-t152/codex TMPDIR="$TMPDIR" GOCACHE=/tmp/amq-t152/gocache GOMODCACHE="$(go env GOMODCACHE)" GOPROXY=off PYTHONPATH=/tmp/amq-t148/python NPM_FLAGS=--offline make ci`: exit 2. Every package except `internal/cli` passed. Its sole failure was the same known PTY residual, this time with pane identity `%0_73478`. Because the Go test phase failed, make stopped before later skill and npm phases.
- `env HOME=/tmp/amq-t152/home GOCACHE=/tmp/amq-t152/gocache GOMODCACHE="$(go env GOMODCACHE)" GOPROXY=off go run ./scripts/redact-evidence -check docs/v3-spike/320-renewal-ab/`: exit 0.
- The absolute user-path scan printed `0`.
- `git diff --check`: exit 0.

The focused positive rows for the new ledger table, Stop/read-error scheduler
cases, real scheduler wiring, launch timestamp, initial offered claim, and
frozen rc.1 rewrite all exited 0 after their controlled mutants were restored.

No test or setup command reached the network. No host gate script was run.

## Follow-up: rows and words

The four supplied text inputs landed byte for byte. Their sha256 values are
`8803bbf9017893dc328213323823f798943061c2a8f002e5b2ee98871c0ff539`
for the carried heading,
`f81488bd9ad16e2d8df6ae47262d00f7b0b4ccd6e8e0edeb0fbcbbd02459eb63`
for the limits sentence,
`1e0a49e3cbcb3010bd607e1fa650a7e27ca82158e6b14d8504a2693d24ec05c7`
for the release-note fallback, and
`155fc2650f831763ccf30c352a745c9df7625411d627a21ff4dbb12cbb1550f0`
for the skill renewal sentence.

### Sticky proof row

```sh
env -i PATH="$PATH" HOME=/tmp/amq-t154/home GOCACHE=/tmp/amq-t154/gocache GOMODCACHE="$(go env GOMODCACHE)" GOPROXY=off go test -count=1 -run '^TestHasActivitySinceRequiresACompleteOwnToolResultAtOrAfterTheBoundary$' ./internal/ledger
```

Reverted expression: removed the three-line `if found { return }` guard at
the start of the ledger callback. Exit 1, first failing line:

```text
activity_test.go:71: T12 tool result then Stop: got false, err <nil>, want true and nil
```

### Zero activity-renewal omission

```sh
env -i PATH="$PATH" HOME=/tmp/amq-t154/home GOCACHE=/tmp/amq-t154/gocache GOMODCACHE="$(go env GOMODCACHE)" GOPROXY=off go test -count=1 -run '^TestManualRenewalOmitsZeroActivityRenewals$' ./internal/cli/
```

Reverted expression: `json:"activity_renewals,omitempty"` became
`json:"activity_renewals"`. Exit 1, first failing line:

```text
hook_ingest_lease_test.go:375: manual-only task = {
```

### Stop ingest renewal

```sh
env -i PATH="$PATH" HOME=/tmp/amq-t154/home GOCACHE=/tmp/amq-t154/gocache GOMODCACHE="$(go env GOMODCACHE)" GOPROXY=off go test -count=1 -run '^TestEvidenceIngestStopRenewsTheClaimedLease$' ./internal/cli/
```

Reverted expression: `case "Stop": return true` became `return false`.
Exit 1, first failing line:

```text
hook_ingest_lease_test.go:393: Stop ingest did not report renewal
```

### Re-measured carried rows

A1 used:

```sh
env HOME=/tmp/amq-t148/home CODEX_HOME=/tmp/amq-t148/codex GOCACHE=/tmp/amq-t148/gocache GOMODCACHE="$(go env GOMODCACHE)" GOPROXY=off go test -count=1 -run '^TestEvidenceIngestRenewsTheClaimedLease$' ./internal/cli
```

Reverted expression: `t.Lease.ActivityRenewals == 1` became
`t.Lease.Renewals == 1`. Exit 1, first failing line:

```text
hook_ingest_lease_test.go:67: first-renewal decisions = [], err <nil>
```

A3 used:

```sh
env HOME=/tmp/amq-t148/home CODEX_HOME=/tmp/amq-t148/codex GOCACHE=/tmp/amq-t148/gocache GOMODCACHE="$(go env GOMODCACHE)" GOPROXY=off go test -count=1 -run '^TestEachAttemptLogsOnlyItsFirstActivityRenewal$' ./internal/cli
```

Reverted expression: `ActivityRenewals == 1` became
`ActivityRenewals >= 1`. Exit 1, first failing line:

```text
hook_ingest_lease_test.go:160: attempt 1 decisions = [{At:2026-10-23 12:00:00 +0000 UTC Decision:{Kind:lease_renewed TaskID:t1 Seat:cto Pool: Reason:first activity renewal of attempt 1 Stage: ReasonCode:}} {At:2026-10-23 12:02:00 +0000 UTC Decision:{Kind:lease_renewed TaskID:t1 Seat:cto Pool: Reason:first activity renewal of attempt 1 Stage: ReasonCode:}}], err <nil>
```

A4 used:

```sh
env HOME=/tmp/amq-t148/home CODEX_HOME=/tmp/amq-t148/codex GOCACHE=/tmp/amq-t148/gocache GOMODCACHE="$(go env GOMODCACHE)" GOPROXY=off go test -count=1 -run '^TestLegacyUnlabelledRenewalsDoNotHideFirstActivity$' ./internal/cli
```

Reverted expression: the first-activity decision additionally required
`Renewals == ActivityRenewals + ManualRenewals`. Exit 1, first failing line:

```text
hook_ingest_lease_test.go:241: legacy decision = [], err <nil>
```
