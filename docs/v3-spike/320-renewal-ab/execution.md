# 3.2.0 renewal provenance and Codex admission

Base: `c65a709c690dd0ed5877076cfeebf470da339262` (`origin/v3`).

This change labels every lease renewal as `activity` or `manual` while
retaining `renewals` as the total. The first labelled activity renewal of each
attempt writes the existing `lease_renewed` decision even when manual or
pre-upgrade unlabelled renewals came first. `task show` prints total, activity,
manual, and any unlabelled remainder.

A Codex seat's manual fallback now has two independent admission legs, checked
at every doorbell: exact-command trust read once at that seat's current launch,
or a qualifying activity row written by that same handle since the launch.
Relaunch replaces the trust value and activity boundary. Every runtime's
doorbell also carries the long-command rule before the Codex-only fallback.

## Positive rows

All focused commands used scratch HOME, CODEX_HOME, and Go build cache, the
existing read-only module cache, and `GOPROXY=off`:

```sh
env HOME=/tmp/amq-t148/home CODEX_HOME=/tmp/amq-t148/codex GOCACHE=/tmp/amq-t148/gocache GOMODCACHE=/Users/omri/go/pkg/mod GOPROXY=off go test -count=1 -run 'TestRenewalSourcesAndAttemptsAreRecordedSeparately|TestHolderReclaimsAreManualOnBothClaimPaths' ./internal/task
env HOME=/tmp/amq-t148/home CODEX_HOME=/tmp/amq-t148/codex GOCACHE=/tmp/amq-t148/gocache GOMODCACHE=/Users/omri/go/pkg/mod GOPROXY=off go test -count=1 -run 'TestEvidenceIngestRenewsTheClaimedLease|TestEachAttemptLogsOnlyItsFirstActivityRenewal|TestLegacyUnlabelledRenewalsDoNotHideFirstActivity' ./internal/cli
env HOME=/tmp/amq-t148/home CODEX_HOME=/tmp/amq-t148/codex GOCACHE=/tmp/amq-t148/gocache GOMODCACHE=/Users/omri/go/pkg/mod GOPROXY=off go test -count=1 -run '^TestCodexLaunchAdmissionControlsTheDoorbellManualRenewFallback$' ./internal/schedproc
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
| B6 | This handle's own activity after launch removes the fallback; an untrusted relaunch restores it until another own activity row arrives. |
| B7 | Trusted Codex, untrusted Codex, and non-Codex doorbells each contain the long-command rule exactly once; only untrusted Codex contains the manual sentence, after the long-command rule. |

## Controlled reverts

Each revert changed only the expression named below, ran the focused command,
copied the first failing line, and was immediately restored.

### A1

Reverted `t.Lease.ActivityRenewals == 1` to
`t.Lease.Renewals == 1` in `renewLeaseOnToolResult`.

```sh
env HOME=/tmp/amq-t148/home CODEX_HOME=/tmp/amq-t148/codex GOCACHE=/tmp/amq-t148/gocache GOMODCACHE=/Users/omri/go/pkg/mod GOPROXY=off go test -count=1 -run '^TestEvidenceIngestRenewsTheClaimedLease$' ./internal/cli
```

First failing line, exit 1:

```text
hook_ingest_lease_test.go:58: first-renewal decisions = [], err <nil>
```

### A2

Reverted the `RenewAttempt` source argument from `renewalManual` to
`renewalActivity`.

```sh
env HOME=/tmp/amq-t148/home CODEX_HOME=/tmp/amq-t148/codex GOCACHE=/tmp/amq-t148/gocache GOMODCACHE=/Users/omri/go/pkg/mod GOPROXY=off go test -count=1 -run '^TestRenewalSourcesAndAttemptsAreRecordedSeparately$' ./internal/task
```

First failing line, exit 1:

```text
brief320_test.go:94: manual renewal: renewed=true lease=&{Holder:dev GrantedAt:2026-10-24 12:00:00 +0000 UTC ExpiresAt:2026-10-24 12:12:00 +0000 UTC ClaimedAt:2026-10-24 12:00:00 +0000 UTC RenewedAt:2026-10-24 12:02:00 +0000 UTC Renewals:2 ActivityRenewals:1 ManualRenewals:1 LastRenewal:activity Attempt:1 BudgetEndsAt:2026-10-24 13:00:00 +0000 UTC Snapshot: SnapshotTag: SnapshotCommand: Verdict:<nil>} err=<nil>
```

### A3

Reverted the decision condition from `ActivityRenewals == 1` to
`ActivityRenewals >= 1`.

```sh
env HOME=/tmp/amq-t148/home CODEX_HOME=/tmp/amq-t148/codex GOCACHE=/tmp/amq-t148/gocache GOMODCACHE=/Users/omri/go/pkg/mod GOPROXY=off go test -count=1 -run '^TestEachAttemptLogsOnlyItsFirstActivityRenewal$' ./internal/cli
```

First failing line, exit 1:

```text
hook_ingest_lease_test.go:151: attempt 1 decisions = [{At:2026-10-23 12:00:00 +0000 UTC Decision:{Kind:lease_renewed TaskID:t1 Seat:cto Pool: Reason:first activity renewal of attempt 1 Stage: ReasonCode:}} {At:2026-10-23 12:02:00 +0000 UTC Decision:{Kind:lease_renewed TaskID:t1 Seat:cto Pool: Reason:first activity renewal of attempt 1 Stage: ReasonCode:}}], err <nil>
```

### A4

Reverted legacy handling by requiring
`Renewals == ActivityRenewals + ManualRenewals` before the first-activity
decision, which treats unlabelled renewals as already classified activity.

```sh
env HOME=/tmp/amq-t148/home CODEX_HOME=/tmp/amq-t148/codex GOCACHE=/tmp/amq-t148/gocache GOMODCACHE=/Users/omri/go/pkg/mod GOPROXY=off go test -count=1 -run '^TestLegacyUnlabelledRenewalsDoNotHideFirstActivity$' ./internal/cli
```

First failing line, exit 1:

```text
hook_ingest_lease_test.go:211: legacy decision = [], err <nil>
```

### B1

Reverted the launch-trust leg from
`admitted && admission.trusted` to
`admitted && admission.trusted && false`.

```sh
env HOME=/tmp/amq-t148/home CODEX_HOME=/tmp/amq-t148/codex GOCACHE=/tmp/amq-t148/gocache GOMODCACHE=/Users/omri/go/pkg/mod GOPROXY=off go test -count=1 -run '^TestCodexLaunchAdmissionControlsTheDoorbellManualRenewFallback$' ./internal/schedproc
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

`DefaultLeaseDuration` is a compile-time constant declared as ten minutes.
There is no runtime configuration that can vary it independently of the
doorbell. A second literal-duration revert is therefore not a distinct row:
changing the constant changes both the lease default and the rendered sentence
together. B7 is the retained revert for the sentence.

## Doorbell consumer audit

```sh
rg -n 'Declared files:|activity hooks are not trusted|runs longer than the lease|--body' --glob='*.go' --glob='*.sh' --glob='*.md' .
```

Exit 0. The only production construction of these words is
`internal/schedproc/amq.go`. Matches elsewhere are tests, documentation, or
generic AMQ `--body` producers. No consumer parses the scheduler doorbell body.

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

- `go build ./...`: exit 0.
- `go vet ./...`: exit 0.
- `go test -count=1 ./internal/task/... ./internal/cli/... ./internal/schedproc/... ./internal/seatlaunch/...`: exit 0. Final package lines were task 47.012 s, cli 92.486 s, schedproc 75.687 s, and seatlaunch 36.398 s.
- First `NPM_FLAGS=--offline make ci`: exit 2 after every Go and HTML gate passed, because scratch HOME had no PyYAML. No product test failed.
- PyYAML 6.0.3 was installed into `/tmp/amq-t148/python` from the existing local pip wheel with `PIP_NO_INDEX=1`.
- Final `PYTHONPATH=/tmp/amq-t148/python NPM_FLAGS=--offline make ci`: exit 0. It included the full Go suite, documentation and skill gates, hook tests, offline `npm ci`, and pi tests: 120 passed, 0 failed, 1 skipped.
- `go run ./scripts/redact-evidence -check docs/v3-spike/320-renewal-ab/`: exit 0.

No test or setup command reached the network. No host gate script was run.
