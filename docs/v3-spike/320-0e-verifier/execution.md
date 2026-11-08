# 3.2.0-0e verifier execution

Date: 2026-11-07

Base: `a4dff16c6726caf96a088a16efa1577f9ebd00ad`, origin/v3 after #915.

Round 2 carries `da3842f157c9f79fa1cb00fe717ab06965821ed3`
from the unmerged #918 and applies the plan reviewer's findings on 2026-11-08.

Round 3 starts from `93cb87820cb8d82473504b07484f69ad745b857e`,
carries `7c331d72e4caf3e5b147061ff300d1a3144a9553`, and applies the
locked-writer and remaining contract findings on 2026-11-08.

## Result

The commit and exits ledgers now seal the exact bytes on disk. Their document
and head files each have this exact layout:

1. JSON body bytes without an embedded seal
2. one newline
3. one JSON seal line containing `seq`, `prev`, `tag`, `version`, and `schema`
4. one final newline

The tag covers the body bytes and all seal metadata. The head body includes
`layout: "raw-bytes-v1"`, and that field is covered by the head tag. A missing
final newline, extra blank line, carriage return at the split, body edit, seal
metadata edit, or head layout edit is tampering.

After byte verification, decoding is strict. An unknown field under a higher
schema is `newer_writer`, routed to the owner. An unknown field under the same
or lower schema is `schema_mismatch`, routed to the lead. The version is only
evidence and operator guidance, so `(devel)` follows the same schema rule.
Neither compatibility refusal is quarantined or rewritten.

Legacy embedded-seal documents still verify once. A reader accepts an
authenticated immediate raw successor of a legacy head without writing. A
locked writer reads and verifies again, durably records
`ledger_layout_migrated`, then completes the head. The record includes the
ledger kind, seat when applicable, and new link number. Once a raw head
exists, legacy bytes do not downgrade the chain.

## Verification

The focused suites were run from the seat worktree:

```sh
go test ./internal/sealchain
go test ./internal/seatcommit ./internal/scheduler ./internal/schedproc
go test ./internal/cli ./internal/sealchain -run '^(TestUpRefusesEveryBinaryBeforeTheRawLayout|TestUpRefusesAnUnparseableTargetWhenRawLedgersExist|TestUpChecksRawLayoutBeforeTaggedPiBuild|TestRawLayoutSemverPrecedence|TestDoctorNamesRawLedgerCompatibilityActions|TestRunThreadsStampedVersionIntoLedgerWriters|TestRaw.*|TestLegacy.*|TestWriterVersion.*)$' -count=1
```

The rows cover exact raw-byte and metadata tagging, exact endings, both strict
decode classifications including `(devel)`, migration, downgrade prevention,
typed commit blocks, scheduler-wide exits stops, route selection, no duplicate
stop, no compatibility quarantine, version threading, build-info fallback,
doctor words, and semver prerelease ordering.

The final repository gate is recorded by the task harness with the PR.

## E9: v3.1.2 reads current raw-layout fixtures

The current build wrote both fixtures into a disposable session root:

```sh
AMQSQ_0E_FIXTURE_ROOT=/private/tmp/amq-squad-t110-compat.tM6AKa go test -count=1 ./internal/seatcommit ./internal/scheduler -run 'TestWriteRaw.*CompatibilityFixture' -v
git archive --format=tar --output=/private/tmp/amq-squad-t116-v312.tar v3.1.2
tar -xf /private/tmp/amq-squad-t116-v312.tar -C /private/tmp/amq-squad-t116-v312.c3Zdcd
cd /private/tmp/amq-squad-t116-v312.c3Zdcd
GOPROXY=off go test -count=1 ./internal/seatcommit ./internal/scheduler -run TestCompatProbe -v
```

The tagged worktree was at
`5bd394e6dcf9fd06254f80ebc846d92130dc9048`. A temporary package-local
`TestCompatProbe` called the tag's own commit-ledger reader and scheduler over
the fixture paths. It was present only in the disposable tag worktree. The
command compiled and ran the v3.1.2 packages entirely offline from the seeded
module cache.

Observed result:

```text
commit error: ledger_tampered: commit ledger qa does not decode: invalid character '{' after top-level value
commit tampered: true
commit quarantine: no such file or directory
exits error: invalid character '{' after top-level value
scheduler tick error: <nil>
decision: ledger_tampered reason=owner: ...
decision: exits_ledger_unwritable reason=...
```

v3.1.2 cannot decode the two-part document as one JSON value. It classifies
the commit ledger as tampered, creates no quarantine, and records the commit
tamper plus an unwritable exits-ledger decision.

## E10: v3.2.0-rc.1 reads current raw-layout fixtures

The same procedure used a separately generated fixture root so the two old
readers could not affect each other:

```sh
AMQSQ_0E_FIXTURE_ROOT=/private/tmp/amq-squad-t110-compat-rc1.xI9ddD go test -count=1 ./internal/seatcommit ./internal/scheduler -run 'TestWriteRaw.*CompatibilityFixture' -v
git archive --format=tar --output=/private/tmp/amq-squad-t116-v320rc1.tar v3.2.0-rc.1
tar -xf /private/tmp/amq-squad-t116-v320rc1.tar -C /private/tmp/amq-squad-t116-v320rc1.Htyyiv
cd /private/tmp/amq-squad-t116-v320rc1.Htyyiv
GOPROXY=off go test -count=1 ./internal/seatcommit ./internal/scheduler -run TestCompatProbe -v
```

The tagged worktree was at
`3966afb72b85e1ab6c0fc8c879f1c5ef03f92a40`. The temporary read-only probe was
specific to rc.1 and the tag packages were compiled and run offline.

Observed result:

```text
commit error: ledger_tampered: commit ledger qa does not decode: invalid character '{' after top-level value; the refused document is quarantined at .../quarantine/commits.qa.sequnknown
commit tampered: true
commit quarantine: commits.qa.sequnknown
exits error: invalid character '{' after top-level value
scheduler tick error: <nil>
decision: ledger_tampered reason=owner: ...
decision: exits_ledger_unwritable reason=...
```

rc.1 also treats the raw layout as malformed JSON. Its 0c-A behavior copies
the refused commit bytes to `commits.qa.sequnknown`. The exits read records the
same unwritable-ledger decision as v3.1.2.

Both scratch worktrees were removed with `git worktree remove --force` after
the probes. They did not modify the seat branch.

## Compatibility conclusion

E9 and E10 are reachable only after manually installing an old binary. On the
supported path, `amq-squad up --binary` reads the target's reported version
before generic PI validation and refuses every version before
v3.2.0-rc.2. It also refuses an unparseable or `(devel)` target because layout
support cannot be known. The operator must roll forward or start a new
session.

The legacy format cannot distinguish a newer document from tampering because
its tag is verified only after decoding and re-encoding into the old struct.
That is why a legacy document containing a field unknown to the old binary
remains a tamper result rather than a compatibility result.

## Round 2 findings

- A raw document left beside a verified legacy head by an interrupted
  migration is accepted only as the authenticated next link. Round 3 moves
  head completion out of both readers and into the locked writers. A skipped
  sequence or wrong previous tag remains tampering.
- Raw head tags are verified before strict decoding. A verified unknown head
  field is `newer_writer` for a higher head-seal schema and
  `schema_mismatch` otherwise. The caller reads the head once; sealchain no
  longer reopens it while checking or writing.
- `newer_writer` and `schema_mismatch` during seat preparation exclude that
  seat. `idleSeat` consults the single `seatExcluded` boundary, the healthy
  peer receives the pool's work, and a later verified read clears the hold.
  #904 adds its ledger-tamper hold at that same predicate.
- Release comparison rejects leading-zero core versions and ignores accepted
  build metadata for precedence. The lead screen names the authenticated
  writer version and the command `amq-squad up --binary <that build>`.
- A persistent `exits_ledger_unwritable` is emitted once and then counted in
  memory until the exact error changes or the ledger recovers.

## Round 2 focused rows

```sh
GOCACHE=/tmp/amq-squad-reviewer-t114-gocache go test ./internal/sealchain ./internal/seatcommit ./internal/scheduler ./internal/schedproc ./internal/routing ./internal/cli -run 'Test(Raw|Legacy|Interrupted|Compatibility|UpRefuses|UpChecks|UpWithout|DoctorNames|RunThreads|StampedMain)' -count=1
node --test --test-reporter=spec pi/test/words.test.ts
```

The named rows include the commit and exits interrupted-write cases and their
skip/prev refusals, the authenticated head unknown-field and head-swap rows,
E3 attempt replay, E4 legacy unknown-field tamper, E6a seat exclusion, E7 no
raw chain, and E11's real `go build -ldflags='-X main.version=v8.7.6'` path.
E11 invokes that built binary's public `seat commit --base` command and
inspects `extensions/amq-squad/commits/qa.json`; its raw seal line carries
`"version":"v8.7.6"`.

The exact package-local sources used for E9 and E10 are kept beside this note:

- `compat_probe_v3.1.2_seatcommit_test.go.txt`
- `compat_probe_v3.2.0-rc.1_seatcommit_test.go.txt`
- `compat_probe_scheduler_test.go.txt`

They are copied without editing into the named package in each detached tag
worktree. The commands are:

```sh
cp docs/v3-spike/320-0e-verifier/compat_probe_v3.1.2_seatcommit_test.go.txt internal/seatcommit/compat_probe_test.go
cp docs/v3-spike/320-0e-verifier/compat_probe_scheduler_test.go.txt internal/scheduler/compat_probe_test.go
GOPROXY=off go test -count=1 ./internal/seatcommit ./internal/scheduler -run TestCompatProbe -v
```

The rc.1 run substitutes
`compat_probe_v3.2.0-rc.1_seatcommit_test.go.txt`. The v3.1.2 source does not
refer to `QuarantineDir`, which does not exist in that tag.

## Round 2 revert runs

These negative controls were run against the round 2 worktree, one source
revert at a time, and the source was restored before the next run. The first
failing line is quoted so the row proves the production condition rather than
only exercising its happy path.

| Fix or row | Command and reverted line | First failure |
| --- | --- | --- |
| P2-1, commit and exits admit only the exact successor | `go test ./internal/seatcommit ./internal/scheduler -run 'TestInterrupted(Commit\|Exits)MigrationRejectsSkipAndWrongPrev' -count=1`; changed `seq != head+1 || prev != link` to `&&` | `raw_compat_test.go:176: invalid migration = <nil>`; the exits row also logged `ledger_layout_migrated` for the invalid link |
| P2-1, the read completes the interrupted migration | `go test ./internal/scheduler -run TestInterruptedExitsMigrationCompletesHeadAndLogsOnce -count=1`; replaced the `CompleteRawHead` write with a revert-probe panic | `panic: revert probe`, from `exits.go:286` |
| P2-2 / E6a, the exclusion is the single idle-seat boundary | `go test ./internal/scheduler -run TestCompatibilityRefusalExcludesSeatUntilLedgerVerifies -count=1 -timeout=3s`; removed `!s.seatExcluded(h)` from `idleSeat` | `panic: test timed out after 3s`; `preparedIdleSeat` repeatedly selected the incompatible seat |
| P2-3, authenticate a raw head before classifying its fields | `go test ./internal/sealchain -run TestRawHeadUnknownFieldIsClassifiedAfterItsTagVerifies -count=1`; bypassed `seal.Verify` | `raw_test.go:163: unverified unknown head = newer_writer: ... unknown field "future"` |
| P2-3, use the caller's one head read and quarantine those bytes | `go test ./internal/seatcommit -run TestRawHeadSwapQuarantinesTheHeadThatWasChecked -count=1`; added a second `snapHead()` after the swap seam | `quarantine_test.go:506: .../commits.s1.seq1/head.json: no such file or directory` |
| Schema, never writer-version, decides compatibility | `go test ./internal/sealchain -run TestRawSealClassifiesUnknownFieldsBySchemaNotVersion -count=1`; changed `seal.Schema > readerSchema` to a version test | `raw_test.go:101: classification = schema_mismatch: ... schema 2 written by (devel)` |
| P3-1, core leading zeros are invalid and build metadata has no precedence | `go test ./internal/cli -run TestRawLayoutSemverPrecedence -count=1`; restored the permissive core regex and removed `+build` | `up_raw_layout_test.go:87: compare(v3.2.0+build.1,v3.2.0+other) = 0,false; want 0,true` |
| P3-2, writer compatibility is not seat work | `go test ./internal/routing -run TestPolicyRefusalIsNotAFailedAttempt -count=1`; made `ReasonNewerWriter` a work failure | `brief3112_test.go:56: newer_writer = {escalate:true count:true}` |
| P3-3, screen words name the writer and exact install command | `node --test --test-reporter=spec pi/test/words.test.ts`; replaced the detailed block words with generic upgrade words | `AssertionError`: actual `the commits ledger was written by a newer build`, expected `... v9.1.0+build.7` |
| E3, replay the 3.1.1 attempt | `go test ./internal/seatcommit -run TestRawLedgerReplaysAttemptNumber -count=1`; changed `Attempt` to `json:"-"` | `raw_compat_test.go:195: attempt replay = ... Attempt:0` |
| E4, a legacy unknown field remains tampering | `go test ./internal/seatcommit -run TestLegacyUnknownFieldStaysTampered -count=1`; returned `ErrSchemaMismatch` for a legacy link failure | `raw_compat_test.go:228: legacy unknown field = commit ledger s1: schema_mismatch` |
| E7, sessions without a raw chain are unrestricted | `go test ./internal/cli -run TestUpWithoutRawChainProceeds -count=1`; removed the `if !raw { return nil }` exit | `up_raw_layout_test.go:106: (devel) without a raw chain: ledger_layout_downgrade` |
| E11, the public CLI threads the stamped build into the seal | `go test ./internal/cli -run TestStampedMainBinaryWritesItsVersionInTheLedgerSeal -count=1`; cleared `seatcommit.BinaryVersion = version` | `version_seam_test.go:108: written seal = ... Version:v3.2.0-rc.1...;` wanted the stamped `v8.7.6` |
| Persistent exits failure logs once and is counted thereafter | `go test ./internal/scheduler -run TestExitsCompatibilityStopsAreTypedRoutedAndLoggedOnce -count=1`; changed the log guard from `count == 1` to `count >= 1` | `raw_exits_test.go:85: persistent stop was not counted silently: [{Kind:exits_ledger_unwritable ...}] count=2` |

E9 and E10 are themselves rollback controls: the exact saved probe sources
above were compiled in the named detached old-tag worktrees. Their first
observed failures are the old readers' `invalid character '{' after top-level
value` lines recorded in those sections. The raw-byte coverage and ending
rows also mutate each body, metadata field, and delimiter independently in
`TestRawSealCoversExactBodyAndMetadata` and `TestRawSealEndingIsExact`.

## Round 3 locked-writer rows

- `ReadLedgerAs`, the commit reader, and the exits verifier accept the exact
  authenticated successor of a legacy head without changing the document,
  head, or decisions log. Two concurrent readers leave all three unchanged.
- The commit writer re-reads under the per-ledger lock. In the interleaving
  row, reader A pauses after capturing raw N+1 and legacy head N; the writer
  completes the migration and advances through N+3; reader A resumes and N+3
  remains intact. The durable log contains one migration record.
- Both writers append and sync the migration decision before completing the
  raw head. The dedupe key is `(ledger_kind, seat, seq)`. A crash seam after
  that append leaves the legacy head in place; the retry finds the record,
  completes the head, and does not append another record.
- Sealchain receives head bytes from its callers. The source guard scans all
  non-test Go files in `internal/sealchain`, tracks aliases of head paths,
  and rejects production file opens, reads, stats, and directory scans of
  those paths. The one allowed metadata probe is `headsExist` checking the
  heads directory itself to prevent regeneration of a lost seed; it never
  opens a head. There is no internal head reopen.
- The semver rows reject numeric prerelease leading zeros, empty prerelease
  identifiers, and empty build identifiers. E3 installs an embedded-seal
  legacy JSON document containing a discarded attempt and migrates that
  document before checking that attempt 3 survived.
- `seat_excluded` carries `reason_code`. `schema_mismatch` tells the lead to
  investigate the writer; only `newer_writer` gives the owner the exact
  `amq-squad up --binary <that build>` action.

### Round 3 revert runs

Each mutation below was applied alone, its named test failed, and the source
was restored before the next mutation.

| Contract | Reverted production condition | Command, exit, and first failure |
| --- | --- | --- |
| Paused reader cannot roll back N+3 | Restored reader-side `CompleteRawHead` after the reader's captured verification | `go test ./internal/seatcommit -run TestCommitMigrationPausedReaderCannotRollBackWriter -count=1`, exit 1: `paused reader rolled back the writer's head` |
| Two readers write nothing | Restored the same reader-side head completion | `go test ./internal/seatcommit -run TestCommitMigrationTwoReadersNeverWrite -count=1`, exit 1: `concurrent readers changed the legacy head` |
| Crash retry dedupes the durable record | Ignored an existing matching `(ledger_kind, seat, seq)` record during the decision-log scan | `go test ./internal/seatcommit ./internal/scheduler -run 'Test(Commit|Exits)MigrationDecisionSurvivesCrashAndDedupes' -count=1`, exit 1 in both packages: `decisions after retry = 2` |

### Round 3 verification

Subject base SHA: `93cb87820cb8d82473504b07484f69ad745b857e`.
Required carry SHA: `7c331d72e4caf3e5b147061ff300d1a3144a9553`.
The seat does not create a commit; the harness assigns the final tree SHA
after the done report.

- Full Go package gates, exit 0 across the recorded runs. The first combined
  command found an obsolete scheduler assertion and inherited seat-only AMQ
  variables in CLI. Sealchain (0.694 s), seatcommit (117.602 s), schedproc
  (29.204 s), and routing (10.643 s) passed in that run. After the assertion
  was changed to inspect the durable log, `go test ./internal/scheduler
  -count=1` passed in 13.887 s. With the inherited seat-only AMQ variables
  removed, `go test ./internal/cli -count=1` passed in 54.509 s.
- Focused locked-writer restore gate, exit 0:
  `go test ./internal/sealchain ./internal/seatcommit ./internal/scheduler -run 'Test(SealchainProduction|Interrupted|CommitMigration|ExitsMigration|RawLedgerReplays|LegacyExitsMigration)' -count=1`.
- `node --test --test-reporter=spec pi/test/words.test.ts pi/test/screen.test.ts`,
  exit 0, 28 tests passed.
- `npm --prefix pi run lint`, exit 0, `lint ok: 29 files, pi 1.3.0`.
- `git diff --check`, exit 0.
- Exact offline v3.1.2 and v3.2.0-rc.1 archive probes, exit 0 for both
  package pairs. Their observed old-reader refusals are recorded above.

Per the owner-approved lead answer, seat verification runs unlocked in the
sandbox. No host lock was used or claimed for the commands above. The lead
will rerun the gates through the host lock on the exact replacement PR head.

## Recovered-attempt final verification

After applying the preserved attempt-1 stash, the recovered tree contained
the expected 35 changed paths and the stash remained intact. The following
gates were rerun against the restored production source:

- `go test ./internal/sealchain ./internal/seatcommit ./internal/scheduler
  ./internal/schedproc ./internal/routing -count=1`: exit 0. Package times
  were 0.429 s, 109.549 s, 21.640 s, 25.785 s, and 9.400 s respectively.
- `go test ./internal/cli -count=1` under a scrubbed seat environment:
  exit 0, 59.270 s.
- The focused locked-writer restore gate: exit 0. Package times were
  0.360 s, 3.084 s, and 1.270 s.
- `node --test --test-reporter=spec pi/test/words.test.ts
  pi/test/screen.test.ts`: exit 0, 28 tests passed.
- `npm --prefix pi run lint`: exit 0, `lint ok: 29 files, pi 1.3.0`.
- `go build ./...`, `go vet ./...`, and `git diff --check`: exit 0.

The three revert controls were also rerun against isolated mutations. Restoring
reader-side `CompleteRawHead` made the paused-reader row fail with `paused
reader rolled back the writer's head` and the two-reader row fail with
`concurrent readers changed the legacy head`. Ignoring the matching durable
decision made both crash-retry rows fail with `decisions after retry = 2`.
Each mutation was removed before the next command, and the focused restored
source gate above then passed.
