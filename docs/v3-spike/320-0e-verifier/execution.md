# 3.2.0-0e verifier execution

Date: 2026-11-07

Base: `a4dff16c6726caf96a088a16efa1577f9ebd00ad`, origin/v3 after #915.

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

Legacy embedded-seal documents still verify once. Their next write migrates
the document and head to the raw layout and records
`ledger_layout_migrated`, including the seat and new link number. Once a raw
head exists, legacy bytes do not downgrade the chain.

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
git worktree add --detach /private/tmp/amq-squad-t110-v312 v3.1.2
cd /private/tmp/amq-squad-t110-v312
GOPROXY=off go test -count=1 ./internal/seatcommit ./internal/scheduler -run TestCompatProbe -v
```

The tagged worktree was at
`5bd394e6a717617dc034550f55c5473636388247`. A temporary package-local
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
git worktree add --detach /private/tmp/amq-squad-t110-v320rc1 v3.2.0-rc.1
cd /private/tmp/amq-squad-t110-v320rc1
GOPROXY=off go test -count=1 ./internal/seatcommit ./internal/scheduler -run TestCompatProbe -v
```

The tagged worktree was at
`3966afb7da3b12df664326321e7357482c9295c6`. The temporary read-only probe was
the same as E9 and the tag packages were compiled and run offline.

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
