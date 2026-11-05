# 3.2.0 activity renewal evidence

Observed on 2026-11-05 with Codex CLI 0.166.0. This is the second attempt after
closed PR #886 at `781ca72d`.

## Codex producer and trust

Codex 0.166.0 exposes `PostToolUse` as an in-turn work event. The seat adds
`PostToolUse` and `Stop` through `-c` session flags. It does not write the
owner's `CODEX_HOME`.

The trust behavior is from the tagged Codex source:

- [`discovery.rs` lines 427-447](https://github.com/openai/codex/blob/rust-v0.166.0/codex-rs/hooks/src/engine/discovery.rs#L427-L447) assigns `-c` hooks the synthetic source `/<session-flags>/config.toml`.
- [`discovery.rs` lines 776-808](https://github.com/openai/codex/blob/rust-v0.166.0/codex-rs/hooks/src/engine/discovery.rs#L776-L808) constructs the handler key and compares the current declaration hash with `hooks.state`.
- [`discovery.rs` lines 881-887](https://github.com/openai/codex/blob/rust-v0.166.0/codex-rs/hooks/src/engine/discovery.rs#L881-L887) runs a non-managed handler only when it is bypassed, plugin-approved, managed, or trusted. amq-squad uses trusted, never bypass.
- [`discovery.rs` lines 934-958](https://github.com/openai/codex/blob/rust-v0.166.0/codex-rs/hooks/src/engine/discovery.rs#L934-L958) hashes the event, matcher group, and normalized handler. The handler includes the command string, so the installed binary path is part of trust. A reinstall at a different path requires trust again.
- [`fingerprint.rs` lines 51-62](https://github.com/openai/codex/blob/rust-v0.166.0/codex-rs/config/src/fingerprint.rs#L51-L62) converts TOML to recursively sorted JSON and stores its SHA-256 as `sha256:<hex>`.

An app-server `hooks/list` probe with scratch `HOME` and `CODEX_HOME` reported
the session flag key as enabled but untrusted before approval. For the known
declaration `echo hi`, amq-squad's implementation produces the same hash Codex
reported: `sha256:ffc93b7ed01e5d85ae11c6ef9b2a9f3d795165d49f717975ba86d4e38d026710`.

Doctor and seat launch use this wording when any exact hash is absent. The
warning also prints the three declaration keys with their exact command
strings and says that reinstalling either binary at another path drops trust:

```text
codex seats will not renew on their own until you trust the amq-squad hooks: launch one Codex seat, run /hooks, and trust the three /<session-flags>/config.toml amq-squad activity entries
```

Codex seats renew on their own only after that owner trust step. Until a launch
admits the hashes, its task doorbell tells the seat to run `amq-squad task renew
--me "$AM_ME"` after every tool batch and at least every five minutes, and to
stop and block if renewal is refused.

The `Stop` session overlay replaces the owner's complete `Stop` configuration
for that seat process. It therefore includes AMQ's dialog-close command itself.
`TestCodexStopOverlayUsesAMQsInstalledDialogCloseCommand` reads the installed
AMQ v0.116.2 dry-run output and compares the complete command byte for byte.

## Contract rows

| Row | Command | Protection |
| --- | --- | --- |
| attempt and task binding | `go test ./internal/cli -run TestEventStampedBeforeAReplacementAttemptCannotRenewIt -count=1` | an ingest stamped before attempt 2 does not renew attempt 2 |
| two seats do not cross | `go test ./internal/cli -run TestActivityUsesTheMailboxIdentityAndFiltersNonWorkEvents -count=1` | the mailbox identity selects the held lease |
| receipts are not work | same command | `receipt` and `evidence_post` renew nothing |
| old lease normalization | `go test ./internal/task -run TestPreUpgradeLeaseIsNormalizedExactlyOnce -count=1` | attempt and budget end persist once |
| manual normalization | `go test ./internal/cli -run TestManualRenewNormalizesAndBindsTheLegacyAttempt -count=1` | manual renew uses the normalized attempt and clamp |
| first decision writer | `go test ./internal/cli -run TestEvidenceIngestRenewsTheClaimedLease -count=1` | ingest appends the first `lease_renewed` decision |
| scheduler is not a producer | `go test ./internal/scheduler -run TestSeatActivityDoesNotRenewALease -count=1` | compatibility liveness events do not renew |
| Codex hash parity | `go test ./internal/seatexec -run TestCodexActivityTrustMatchesCodex0166HashAndState -count=1` | hash, state key, enabled flag, and command path |
| AMQ dialog parity | `go test ./internal/seatexec -run TestCodexStopOverlayUsesAMQsInstalledDialogCloseCommand -count=1` | Stop close cannot drift from installed AMQ |

Focused execution with `GOCACHE` under `/private/tmp` passed all rows in
`internal/task`, `internal/cli`, `internal/scheduler`, and `internal/seatexec`.

## Row-by-row revert proof

Each row was reverted alone, its command was run, and the production line was
restored before the next row.

| Reverted production line | Exact command | First failure |
| --- | --- | --- |
| remove `began.Before(*held.Lease.ClaimedAt)` from `RenewNoWaitActivity` | `go test ./internal/cli -run TestEventStampedBeforeAReplacementAttemptCannotRenewIt -count=1` | `old attempt renewed ... "lease_renewed":true` |
| restore `receipt` and `evidence_post` in `isLeaseActivity` | `go test ./internal/cli -run TestActivityUsesTheMailboxIdentityAndFiltersNonWorkEvents -count=1` | `receipt activity ... "lease_renewed":true` |
| bypass the `normalizeLeases` loop | `go test ./internal/task -run TestPreUpgradeLeaseIsNormalizedExactlyOnce -count=1` | `normalization = [], err <nil>` |
| restore manual `s.Renew` without normalization and attempt selection | `go test ./internal/cli -run TestManualRenewNormalizesAndBindsTheLegacyAttempt -count=1` | `task renew: no lease held` |
| suppress ingest's `Lease.Renewals == 1` decision append | `go test ./internal/cli -run TestEvidenceIngestRenewsTheClaimedLease -count=1` | `first-renewal decisions = [], err <nil>` |
| restore scheduler renewal from `SeatActivity` | `go test ./internal/scheduler -run TestSeatActivityDoesNotRenewALease -count=1` | `SeatActivity prevented expiry: []` |
| remove the command string from the Codex normalized identity | `go test ./internal/seatexec -run TestCodexActivityTrustMatchesCodex0166HashAndState -count=1` | hash `4dde3c...` instead of `ffc93b...` |
| restore the shortened dialog-close command | `go test ./internal/seatexec -run TestCodexStopOverlayUsesAMQsInstalledDialogCloseCommand -count=1` | `Stop overlay drifted from AMQ installer` |
| change the required doctor warning wording | `go test ./internal/cli -run TestCodexActivityTrustWarningUsesTheRequiredOwnerStep -count=1` | warning began `Codex seats`, not required `codex seats` |

## Live run

Scratch locations were `/private/tmp/amq-squad-t79-live/{home,codex,root}`.
The candidate binary created and claimed scratch task `t1`; no manual renewal
was issued for that task. Exact session-flag hashes were written only to the
scratch `CODEX_HOME`. Codex 0.166.0 started with the scratch `HOME` and
`CODEX_HOME`, but its model request returned HTTP 401 before a tool call because
the scratch home intentionally contained no owner authentication. Owner auth
was not copied or symlinked. Therefore the model-driven PostToolUse, expiry
movement, and decision-line portion of the live row is pending the lead's
credential-safe authenticated scratch run. The lead first logs into the scratch
Codex home, opens `/hooks` on the same overlay, and trusts it. From the repository
root the exact build and live command is:

```sh
go build -o /private/tmp/amq-squad-t79-live/amq-squad ./cmd/amq-squad
env HOME=/private/tmp/amq-squad-t79-live/home \
  CODEX_HOME=/private/tmp/amq-squad-t79-live/codex \
  AM_ME=live \
  AM_ROOT=/private/tmp/amq-squad-t79-live/root \
  AM_SESSION=t79-live \
  /opt/homebrew/bin/codex exec \
  --skip-git-repo-check \
  -C /private/tmp/amq-squad-t79-live \
  -s workspace-write \
  -c 'hooks.PostToolUse=[{hooks=[{type="command",command="'\''/private/tmp/amq-squad-t79-live/amq-squad'\'' evidence ingest --me \"$AM_ME\"",timeout=5}]}]' \
  'Use the shell tool exactly once to run: printf live-hook-ok. Then reply only done.'
```

Codex's own app-server did complete the local trust half for the exact live
command. `hooks/list` returned `enabled: true`, `isManaged: false`,
`trustStatus: trusted`, and current hash
`sha256:3211749a85ae512e67686c7868d988c14bfed419f0fc200c9589d03284b1e8dd`.
A direct replay of Codex's real `PostToolUse` payload through that command,
still without `task renew`, moved `ExpiresAt` from
`2026-11-05T15:28:28.412647Z` to `2026-11-05T15:33:10.691502Z` and appended:

```json
{"at":"2026-11-05T15:23:10.691192Z","kind":"lease_renewed","task_id":"t1","seat":"live","reason":"first activity renewal of attempt 1"}
```

That replay proves the command, store, and decision path. It does not replace
the missing authenticated model-driven tool call.

No live-run file was written below `~/.codex`, `~/.amq-squad`, or
`.agent-mail/v31`. No test performs network access or formats the environment.

## Restore provenance and review split

This attempt was restored from discard ref
`refs/amq-squad/evidence/reviewer/t79/15`, whose object is tree
`adf6e4be19f0879443989bc5142cf08a5b7721b7`. The lead's fallback patch is
51,891 bytes with SHA-256
`2b2326207be8cfed2c790c011ddad09801deae49d177632c8b392683a144c73b`.

The grouped review stat, including the four new files, is 52 files and 1,502
insertions / 128 deletions:
producer and attempt binding, lease normalization, trust admission with doctor
and dispatch fallback, and generated skill copies. There are 40 code and test
files before docs and generated skill copies, so this should split at the
trust-admission seam: producer and lease behavior first; Codex trust admission,
doctor wording, launch warning, and dispatch fallback second. The evidence note
is shared by the two review parts and records the final combined proof.
