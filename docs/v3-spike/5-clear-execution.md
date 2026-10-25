# Phase 5: the Buzz-signed seat clear (execution log)

Branch `v3-5-clear`, from `v3` at 4d23ec61. Plan 4.3 (seat clear), 7.3 (typed gates, the signed answer path, squad identity), amendment 28 (the owner proof and its same-UID limit, R1, R5, R9). Evidence in `5-clear-evidence/`.

## What changed

The pty-only clear is now the request half. `seat clear --seat HANDLE` still runs only from the owner's real pty (every earlier check stays: no `AM_ME` or seat/lead marker, real pty on stdin, no seat pane's tty, no descent from a pane, the handle typed with echo off). What follows depends on the mode in force:

- `required` (the default whenever the owner's Buzz enrolment, `~/.amq-squad/gate-binding.json`, exists): the verb raises a typed gate of action `quarantine_clear` (class `clear`, gate kind `quarantine_clear`, human only) and posts it to the owner through `gateCore` and `gateTransport`, the 3-1 and 4-5 path the four typed gates use. The target is `clear <handle> nonce=<32 hex> incarnation=<task>@<unix nanos> config=sha256:<digest of the quarantine's config diff>`; the gate id is `clear-<nonce>`; the request time is the record's `issued_at`. The seat's current request is `clears/<handle>.json` in the gate store. The clear completes only when `gateverify.Verifier.AuthorizeClear` accepts the owner's answer: the gate is a quarantine_clear gate of this store's squad, for this handle and this quarantine (incarnation and config digest); its nonce is the seat's current request's; no ClearRecord on the seat's sealed ledger carries the answer's event id (the ledger is the only use mark, F3 below); and `Verifier.Accepted` passes, which is the check the four typed gates get (owner key, DM channel, thread binding, window, edit and delete history, squad identity, single use, envelope re-verified against the owner's trust store). Without it the verb returns `clear_unsigned`; nothing is written (not even the preflight refresh), the task store quarantine and the ledger stay. A second run reuses the open request. Running the verb again after the answer completes the clear.
- `pty_only`: today's behaviour, with no gate. Accepted only when no enrolment exists.

The signed clear record (`seatcommit.ClearRecord`) now also carries `gate_id`, `event_id` and `config_digest`, inside the signed bytes. At the commit step, the exit check and the widen dry run (see F1), `ownerTrust{Root}.Verify` verifies the issuer signature and then `checkClearRecord`: a record that names a gate is re-verified against the gate store (`Verifier.CheckClear`: gate, handle, quarantine, the full answer check, the event id); a record with no gate counts only when the mode is not `required`. A same-UID shell that signs a record with the issuer key it can read therefore no longer lifts a quarantine in `required` mode.

Grant key `clear.signature` (owner file `clear:` block, `signature: required | pty_only`): the squad.yaml cannot set it. `grants.LoadFrom` returns an error (`grants_invalid` at every consumer) when the owner file says `pty_only` and `gate-binding.json` sits beside it. Doctor row `seat clear signature` names the mode in force (or the grants error); a `seat clear <handle>` warning row per open quarantine_clear gate says whether the owner's answer is awaited on Buzz or the question never reached it (`gate post`), and that there is no fallback.

## Rows

One failing-when-reverted test each (`5-clear-evidence/revert-check.txt`):

| row | test |
| --- | --- |
| same-UID shell with a real pty and no signature: `clear_unsigned`, quarantine stays | `TestSeatClearWithoutTheOwnersSignatureIsClearUnsigned` (internal/cli) |
| answer carrying an old nonce: refused as replayed (superseded request; already-used answer) | `TestClearAnswerWithAnOldNonceIsRefusedAsReplayed` (internal/gateverify) |
| answer for another handle: refused (`clear_wrong_handle`) | `TestClearAnswerForAnotherHandleIsRefused` (internal/gateverify) |
| pty_only grant with an enrolment present: `grants_invalid` | `TestPtyOnlyGrantWithAnEnrolmentIsGrantsInvalid` (internal/cli), `TestClearSignatureModes` (internal/grants) |
| request half refuses without a real pty and with `AM_ME` set | `TestSignedClearRequestHalfKeepsTheOwnerProof` (internal/cli); the existing `TestSeatClearRunsOnlyFromTheOwnersTerminal`, `TestSeatClearNeedsTheOwnersOwnTerminal`, `TestSeatCommitQuarantineAndOwnerClear` stay green (no enrolment, `pty_only` by default) |
| signed clear record carries gate id and event id and verifies | `TestSignedClearRecordCarriesGateAndEventAndVerifies` (internal/cli) |

Also covered: a non-owner key and a plain-DM (outside the thread) answer never open the gate (`TestClearCompletesOnlyOnTheOwnersVerifiedAnswer`); doctor names the mode and why a clear waits, Buzz down then back (`TestDoctorNamesTheClearModeAndAWaitingClear`); the target grammar (`TestClearTargetRoundTrip`).

Code against the gateverify fixtures (`newFixture`, the in-memory relay, the throwaway issuer key) and the CLI gate fixture (`newGateCLIFixture`); no network, no credential-shaped literal, no environment in any failure message.

## Judgment calls

- The clear record and the commit-time check needed edits outside the listed packages, kept to the minimum: `internal/seatcommit/ledger.go` (three optional fields on `ClearRecord`, `ClearQuarantineWith` with an authorize callback that runs under the ledger lock; `ClearQuarantine` is unchanged in signature), `internal/operatorauth/policy.go` and `internal/gatetarget/gatetarget.go` (the new action, class and target builder), `internal/cli/scheduler_run.go` and `task_widen.go` (`ownerTrust{Root: root}` in three places), `internal/cli/doctor.go` (one line wrapping the existing hook). The verifier agent's seatcommit work may touch `ledger.go`; the hunks are additive.
- Two-phase completion: the verb does not wait for the phone. The first run is the request (exit non-zero, `clear_unsigned`); after the owner answers, the owner runs it again from the pty and it completes. Both runs pass the full owner proof, so the answer alone never clears.
- "Seat incarnation" is the quarantine's identity: `<task id>@<quarantine time in unix nanos>`. The config diff's digest is the sha256 of the quarantine's `Config` list as JSON (a quarantine on the task store alone, with no ledger record, has the digest of the empty list and consumes the answer without a record to carry the proof).
- The namespace `Verifier.Accepted` checks at the commit step is read from the issuer-signed envelope of the stored answer, because the commit step and the exit check hold only the session root. What binds the answer to this squad is the gate record's squad identity (`SquadID` of the root), checked in `clearGate`; the envelope signature, owner key, thread and single use are checked as for any typed gate.
- The mode is evaluated where it is used: a downgrade needs the owner to remove the enrolment or edit the grants file, both owner files a same-UID process can write (the recorded same-UID limit of 7.3). A record that names a gate is checked whatever the mode.
- `seat clear` keeps `--seat HANDLE` (the brief's `<handle>` is the same verb); no positional argument was added.

- Gate raised with `raised_by: owner`, window the 30 minute default.

## Narrow-pass fixes (reviewer's pass on 1d6dbf8e), rebased on v3 3d3be1ab

One commit with the task-store-only work. Each has a failing-when-reverted row (`5-clear-evidence/revert-check.txt`).

- F1 every consumer verifies. The earlier claim was wrong for the exit check (`ExitCheck` never read `o.Clear`) and for `task widen` (the dry run skipped the quarantine block). Now `ExitCheck`, `Run` (a dry run included: it skips the push, never the verification) and `CheckWiden` call `ClearRecord.Verified` on a cleared quarantine and refuse with `quarantine_unverified` on any failure, an unreadable gate store included (`ReasonClearUnsigned` is now the same code). Rows: `TestEveryConsumerOfAClearVerifiesIt` (seatcommit), `TestEveryConsumerRefusesAClearWhoseAnswerIsGone` (cli: answer file deleted, and the gates directory unreadable, at all three consumers).
- F2 request spam. `operatorauth.Reserved("quarantine_clear")`; `gate raise` (and `storeGateRaise`) refuse it with `action_reserved`; only `seat clear`, after the pty proof, raises it. Row: `TestGenericGateRaiseRefusesTheReservedClearAction`.
- F3 one durable write. The `clears/used/` marks are gone. `Ledger.Cleared` keeps the lifted quarantines with their ClearRecords; "used" is a ClearRecord with the answer's event id on the sealed ledger (`seatcommit.ClearEventUsed`; an unreadable ledger refuses). `AuthorizeClear` takes the use check as a function and writes nothing; the record is signed and written to the ledger in one write, so a failure before it leaves the answer usable. A deleted record is `ledger_tampered` like any sealed record. Rows: `TestClearRecordOnTheLedgerIsTheOnlyUseMark` (crash after authorization, then the same answer completes), `TestClearAnswerWithAnOldNonceIsRefusedAsReplayed` (used from the ledger alone).
- F4 recurrence. `LedgerQuarantine.Launch` records the launch id (incarnation) of the quarantine and `ClearRecord` names `launch` and `condition`; `exitChanges` does not raise a condition a verified ClearRecord (pending or lifted) names for the current launch. A new lease has a new launch id and is checked fresh. Row: `TestClearedHooksSilenceIsNotRaisedAgainOnTheSameIncarnation`.
- Seed entry. A clear of a quarantine that lives in the task store alone creates a ledger entry marked `seeded` (no worktree, branch or base), sealed like any other, with the quarantine's launch id and reason. `RecordBase` continues a seed (the base lands in it, `seeded` is dropped, the clear history stays); the exit check treats a seed with no base as "no diff yet"; a diff with no `RecordBase` is `base_unrecorded`. Rows: `TestSeedEntryIsContinuedByRecordBaseAndKeepsTheClear`, `TestTaskStoreOnlyQuarantineClearsThroughTheSignedPathAndLeavesARecord` (the quarantine is built by `ExitCheck` and the scheduler's `Store.Quarantine`, not by hand).

Re-pass of fb22d2f4 (four edge defects, one small commit; rows in `revert-check.txt`):

- P1 a deleted ledger. `ReadLedger` consults the sealed head when the file is missing (`sealchain.Absent`): a ledger whose head records a link is `ledger_tampered`, never "no ledger", `base_unrecorded` or "unused"; only a ledger with no head is absent. `ClearEventUsed`, `quarantineIdentity` and the seed fallback therefore refuse. Row: `TestDeletedLedgerIsTamperedNotUnused` (clear a task-store-only quarantine, stop before the task-store clear, delete the ledger, retry the same answer). `TestNoRecordedBaseIsRefused` now removes the head as well as the file, since a ledger removed alone is tampered by this rule.
- P2 widen. `CheckWiden` allows only a clean dry run or one whose refusal is diff policy (it carries violations, which become crossings); every other outcome refuses with its own code. Row: `TestWidenRefusesWhatTheDryRunRefuses` (a seeded lifted quarantine with no base; a ledger edited without resealing).
- P2b (last tightening). The widen whitelist is exact: a refusal turns into crossings only when it carries violations and its code is the first violation's code (`diffPolicyRefusal`, what `Run` sets); violations under another code refuse. Row: `TestOnlyTheDiffPolicyRefusalIsTurnedIntoCrossings`. It is the one-line condition plus a four-line helper, so the case can be unit-tested (`Run` cannot produce that shape).
- P3 scheduler exit step. `Committer.CheckExit` treats any refusal it does not name as a block that quarantines the seat (`ledger_tampered` above all), never OK. Row: `TestExitCheckRefusalIsABlockNotOK` (schedproc).
- P4 concurrent completion. Under the ledger lock an already-lifted quarantine returns `seatcommit.ErrAlreadyCleared` (`already_cleared`), which `seat clear` reports as such and never as success. Row: `TestConcurrentClearsWriteOneRecordAndOneIsAlreadyCleared` (two callers, one record, one success, one already_cleared).

Recorded same-UID limits (no code): replacing `clears/<handle>.json` supersedes the owner's open request and denies the clear; removing the enrolment file or editing `grants.yaml` downgrades the mode at the next evaluation; the mode check runs at squad load and scheduler start, not on every standalone lead invocation.

## Not done

- The live row (a real clear answered from the phone): `docs/v3-buzz/owner-run.md`, "Third item". Not run by this branch.
- No `seat clear --wait`: a poll loop would hold the owner's pty open for an answer from the phone; the second run is the completion.
- A seat with neither a ledger quarantine nor a task-store quarantine says "not quarantined" (the scheduler's path persists the task quarantine before marking the seat).

## Gate

Quiet host (`ps -axo command` showed no seat-exec, scheduler run, pi run, amq coop, amq-remote or amq-squad lead/team/launch process). Output in `5-clear-evidence/gate.txt` (through `scripts/redact-evidence`); the revert check in `5-clear-evidence/revert-check.txt`.
