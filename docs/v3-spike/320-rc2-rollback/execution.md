# 3.2.0-rc.2 migrated-ledger rollback measurement

Date: 2026-11-10

Base: `b4575767941cd59fc1ecfe9c48bdebcd60dd95dd`, the squash of #940 on
`origin/v3`.

Old tag commits:

- `v3.1.2`: `5bd394e6dcf9fd06254f80ebc846d92130dc9048`
- `v3.2.0-rc.1`: `3966afb72b85e1ab6c0fc8c879f1c5ef03f92a40`

## Fixture construction

Each tag was exported with `git archive` into its own disposable directory.
The saved package-local probes in this directory were copied into that tree,
and every tag command ran with `GOPROXY=off`. Each of the eight fixture roots
was created in place because copying a root changes the routing seed's inode
binding.

The old tag's own writers first wrote two links for both `qa` and `legacy`, and
two entries in the exits queue. The current build then produced these states:

| Fixture | Commit ledger | Exits ledger |
| --- | --- | --- |
| F1 | The current writer continued `qa`, completed its raw head, and recorded one layout migration. | The current scheduler continued the old queue and completed the raw head. |
| F2 | The `afterLayoutMigrationDecision` fault seam stopped after writing the raw successor and migration decision but before completing the head. | The `afterExitsMigrationDecision` seam stopped at the same boundary. |
| F3 | `qa` was migrated as F1; `legacy` remained exactly as the old tag wrote it. | Same as F1. |
| F4 | The current build quarantined a damaged `qa` link and called the scheduler recovery primitive `seatcommit.RecoverLedger`; the resulting record reports `verified:true`, link 3, and `seq_from:verified_head`. | Same as F1. |

The current fixture command was:

```sh
AMQSQ_ROLLBACK_FIXTURE_ROOT="$root" AMQSQ_ROLLBACK_MODE="$mode" \
  go test ./internal/seatcommit -run TestContinueRollbackCompatibilityFixture -count=1 -v
AMQSQ_ROLLBACK_FIXTURE_ROOT="$root" AMQSQ_ROLLBACK_MODE="$mode" \
  go test ./internal/scheduler -run TestContinueRollbackExitsFixture -count=1 -v
```

Representative F2 and F4 output, verbatim:

Line numbers in this block are those of the seat's working tree at the run,
one line before the commit that landed: as of
8a79e6a1537069e025d637ea5898b04d1e10fc2b the two log calls are at
`internal/seatcommit/raw_compat_test.go:132` and
`internal/scheduler/raw_exits_test.go:149`. (the #961 follow-up)

```text
=== RUN   TestContinueRollbackCompatibilityFixture
    raw_compat_test.go:131: current fixture mode=f2 handle=qa seq=3 recovered=<nil>
--- PASS: TestContinueRollbackCompatibilityFixture (0.03s)
=== RUN   TestContinueRollbackExitsFixture
    raw_exits_test.go:148: current exits fixture mode=f2 legacy=false migration=&{Seq:4 Prev:88d2161b2be0dca42e60f385a598e60f2be2633105ba89cb83265c3f7b88aa07 Tag:84c0d32852f939d85d486ff12e91bf5b5b22b0521a57e23cd6a7ecca34cbc64d Version:(devel) Schema:1} queue=2
--- PASS: TestContinueRollbackExitsFixture (0.04s)
=== RUN   TestContinueRollbackCompatibilityFixture
    raw_compat_test.go:131: current fixture mode=f4 handle=qa seq=3 recovered=&{Seq:2 Slot:/private/tmp/amq-squad-t137.yAZeej/v3.1.2-run-f4/extensions/amq-squad/quarantine/commits.qa.seq2 BodySHA256:4968e4c46bbcc026cffc520cb8f705001ac742691d3235909f4e51d245125609 HeadSHA256:475fb301920eb68d04043b59dd6ca4177848404b435d09f746459f6fefa6f1c1 By:cto Reason:rollback compatibility fixture At:1970-01-01 00:00:40 +0000 UTC Carried:[] Verified:true Link:3 SeqFrom:verified_head Alarm:}
--- PASS: TestContinueRollbackCompatibilityFixture (0.12s)
```

## Old-tag command

For each tag and fixture, the exact read command was:

```sh
AMQSQ_ROLLBACK_ROOT="$root" AMQSQ_ROLLBACK_PHASE="$phase" GOPROXY=off \
  go test -count=1 ./internal/seatcommit -run TestRollbackProbe -v
AMQSQ_ROLLBACK_ROOT="$root" AMQSQ_ROLLBACK_PHASE=read GOPROXY=off \
  go test -count=1 ./internal/scheduler -run TestRollbackProbe -v
```

`phase` was `read` except for F3, where it was
`read_and_commit_legacy`. The full package-local sources are:

- `compat_probe_v3.1.2_seatcommit_test.go.txt`
- `compat_probe_v3.2.0-rc.1_seatcommit_test.go.txt`
- `compat_probe_scheduler_test.go.txt`

## M1: v3.1.2 reads F1

Output, verbatim:

Line numbers as of 8a79e6a1537069e025d637ea5898b04d1e10fc2b: the block's
`rollback_probe_test.go` is the saved probe copied into the tag tree, and its
lines are those of the saved seatcommit probes (`:42`, `:43`, `:47`, `:52`,
`:56`) and of `compat_probe_scheduler_test.go.txt` (`:39`, `:41`) at that
commit. The exits ledger in this block is `exits.json` at the fixture root,
where that commit's scheduler probe (`:28`) and fixture kept it; since round 2
both keep it at `extensions/amq-squad/scheduler/exits.json`, so the saved
probe as it now stands does not reproduce this path. (the #961 follow-up)

```text
=== RUN   TestRollbackProbe
    rollback_probe_test.go:42: commit handle=qa error=ledger_tampered: commit ledger qa does not decode: invalid character '{' after top-level value
    rollback_probe_test.go:43: commit handle=qa tampered=true
    rollback_probe_test.go:42: commit handle=legacy error=<nil>
    rollback_probe_test.go:43: commit handle=legacy tampered=false
    rollback_probe_test.go:52: commit quarantine: open /private/tmp/amq-squad-t137.yAZeej/v3.1.2-run-f1/extensions/amq-squad/commits/quarantine: no such file or directory
--- PASS: TestRollbackProbe (0.00s)
PASS
ok  	github.com/omriariav/amq-squad/v3/internal/seatcommit	0.245s
=== RUN   TestRollbackProbe
    rollback_probe_test.go:39: scheduler tick error: <nil>
    rollback_probe_test.go:41: decision: ledger_tampered reason=owner: ledger_tampered: the exits ledger /private/tmp/amq-squad-t137.yAZeej/v3.1.2-run-f1/exits.json: invalid character '{' after top-level value
    rollback_probe_test.go:41: decision: exits_ledger_unwritable reason=ledger_tampered: the exits ledger /private/tmp/amq-squad-t137.yAZeej/v3.1.2-run-f1/exits.json: invalid character '{' after top-level value
--- PASS: TestRollbackProbe (0.00s)
PASS
ok  	github.com/omriariav/amq-squad/v3/internal/scheduler	0.238s
```

v3.1.2 rejects the migrated commit and exits documents as malformed JSON,
leaves the legacy sibling readable, and creates no quarantine copy.

## M2: v3.2.0-rc.1 reads F1

Output, verbatim:

Line numbers as of 8a79e6a1537069e025d637ea5898b04d1e10fc2b: the block's
`rollback_probe_test.go` is the saved probe copied into the tag tree, and its
lines are those of the saved seatcommit probes (`:42`, `:43`, `:47`, `:52`,
`:56`) and of `compat_probe_scheduler_test.go.txt` (`:39`, `:41`) at that
commit. The exits ledger in this block is `exits.json` at the fixture root,
where that commit's scheduler probe (`:28`) and fixture kept it; since round 2
both keep it at `extensions/amq-squad/scheduler/exits.json`, so the saved
probe as it now stands does not reproduce this path. (the #961 follow-up)

```text
=== RUN   TestRollbackProbe
    rollback_probe_test.go:42: commit handle=qa error=ledger_tampered: commit ledger qa does not decode: invalid character '{' after top-level value; the refused document is quarantined at /private/tmp/amq-squad-t137.yAZeej/v3.2.0-rc.1-run-f1/extensions/amq-squad/quarantine/commits.qa.sequnknown
    rollback_probe_test.go:43: commit handle=qa tampered=true
    rollback_probe_test.go:42: commit handle=legacy error=<nil>
    rollback_probe_test.go:43: commit handle=legacy tampered=false
    rollback_probe_test.go:56: commit quarantine: commits.qa.sequnknown
--- PASS: TestRollbackProbe (0.05s)
PASS
ok  	github.com/omriariav/amq-squad/v3/internal/seatcommit	0.314s
=== RUN   TestRollbackProbe
    rollback_probe_test.go:39: scheduler tick error: <nil>
    rollback_probe_test.go:41: decision: ledger_tampered reason=owner: ledger_tampered: the exits ledger /private/tmp/amq-squad-t137.yAZeej/v3.2.0-rc.1-run-f1/exits.json: invalid character '{' after top-level value
    rollback_probe_test.go:41: decision: exits_ledger_unwritable reason=ledger_tampered: the exits ledger /private/tmp/amq-squad-t137.yAZeej/v3.2.0-rc.1-run-f1/exits.json: invalid character '{' after top-level value
--- PASS: TestRollbackProbe (0.00s)
PASS
ok  	github.com/omriariav/amq-squad/v3/internal/scheduler	0.371s
```

rc.1 gives the same tamper answers and adds the expected
`commits.qa.sequnknown` quarantine copy.

## M3: both tags read interrupted F2

Output, verbatim:

Line numbers as of 8a79e6a1537069e025d637ea5898b04d1e10fc2b: the block's
`rollback_probe_test.go` is the saved probe copied into the tag tree, and its
lines are those of the saved seatcommit probes (`:42`, `:43`, `:47`, `:52`,
`:56`) and of `compat_probe_scheduler_test.go.txt` (`:39`, `:41`) at that
commit. The exits ledger in this block is `exits.json` at the fixture root,
where that commit's scheduler probe (`:28`) and fixture kept it; since round 2
both keep it at `extensions/amq-squad/scheduler/exits.json`, so the saved
probe as it now stands does not reproduce this path. (the #961 follow-up)

```text
v3.1.2
=== RUN   TestRollbackProbe
    rollback_probe_test.go:42: commit handle=qa error=ledger_tampered: commit ledger qa does not decode: invalid character '{' after top-level value
    rollback_probe_test.go:43: commit handle=qa tampered=true
    rollback_probe_test.go:42: commit handle=legacy error=<nil>
    rollback_probe_test.go:43: commit handle=legacy tampered=false
    rollback_probe_test.go:52: commit quarantine: open /private/tmp/amq-squad-t137.yAZeej/v3.1.2-run-f2/extensions/amq-squad/commits/quarantine: no such file or directory
--- PASS: TestRollbackProbe (0.00s)
=== RUN   TestRollbackProbe
    rollback_probe_test.go:39: scheduler tick error: <nil>
    rollback_probe_test.go:41: decision: ledger_tampered reason=owner: ledger_tampered: the exits ledger /private/tmp/amq-squad-t137.yAZeej/v3.1.2-run-f2/exits.json: invalid character '{' after top-level value
    rollback_probe_test.go:41: decision: exits_ledger_unwritable reason=ledger_tampered: the exits ledger /private/tmp/amq-squad-t137.yAZeej/v3.1.2-run-f2/exits.json: invalid character '{' after top-level value
--- PASS: TestRollbackProbe (0.00s)
v3.2.0-rc.1
=== RUN   TestRollbackProbe
    rollback_probe_test.go:42: commit handle=qa error=ledger_tampered: commit ledger qa does not decode: invalid character '{' after top-level value; the refused document is quarantined at /private/tmp/amq-squad-t137.yAZeej/v3.2.0-rc.1-run-f2/extensions/amq-squad/quarantine/commits.qa.seq2
    rollback_probe_test.go:43: commit handle=qa tampered=true
    rollback_probe_test.go:42: commit handle=legacy error=<nil>
    rollback_probe_test.go:43: commit handle=legacy tampered=false
    rollback_probe_test.go:56: commit quarantine: commits.qa.seq2
--- PASS: TestRollbackProbe (0.05s)
=== RUN   TestRollbackProbe
    rollback_probe_test.go:39: scheduler tick error: <nil>
    rollback_probe_test.go:41: decision: ledger_tampered reason=owner: ledger_tampered: the exits ledger /private/tmp/amq-squad-t137.yAZeej/v3.2.0-rc.1-run-f2/exits.json: invalid character '{' after top-level value
    rollback_probe_test.go:41: decision: exits_ledger_unwritable reason=ledger_tampered: the exits ledger /private/tmp/amq-squad-t137.yAZeej/v3.2.0-rc.1-run-f2/exits.json: invalid character '{' after top-level value
--- PASS: TestRollbackProbe (0.00s)
```

The incomplete head does not make either old tag accept the raw successor;
rc.1 derives the known `seq2` quarantine name from the still-legacy head.

## M4: both tags read F3 and commit the legacy sibling

Output, verbatim:

Line numbers as of 8a79e6a1537069e025d637ea5898b04d1e10fc2b: the block's
`rollback_probe_test.go` is the saved probe copied into the tag tree, and its
lines are those of the saved seatcommit probes (`:42`, `:43`, `:47`, `:52`,
`:56`) and of `compat_probe_scheduler_test.go.txt` (`:39`, `:41`) at that
commit. The exits ledger in this block is `exits.json` at the fixture root,
where that commit's scheduler probe (`:28`) and fixture kept it; since round 2
both keep it at `extensions/amq-squad/scheduler/exits.json`, so the saved
probe as it now stands does not reproduce this path. (the #961 follow-up)

```text
v3.1.2
=== RUN   TestRollbackProbe
    rollback_probe_test.go:42: commit handle=qa error=ledger_tampered: commit ledger qa does not decode: invalid character '{' after top-level value
    rollback_probe_test.go:43: commit handle=qa tampered=true
    rollback_probe_test.go:42: commit handle=legacy error=<nil>
    rollback_probe_test.go:43: commit handle=legacy tampered=false
    rollback_probe_test.go:47: legacy commit error: <nil>
    rollback_probe_test.go:52: commit quarantine: open /private/tmp/amq-squad-t137.yAZeej/v3.1.2-run-f3/extensions/amq-squad/commits/quarantine: no such file or directory
--- PASS: TestRollbackProbe (0.02s)
=== RUN   TestRollbackProbe
    rollback_probe_test.go:39: scheduler tick error: <nil>
    rollback_probe_test.go:41: decision: ledger_tampered reason=owner: ledger_tampered: the exits ledger /private/tmp/amq-squad-t137.yAZeej/v3.1.2-run-f3/exits.json: invalid character '{' after top-level value
    rollback_probe_test.go:41: decision: exits_ledger_unwritable reason=ledger_tampered: the exits ledger /private/tmp/amq-squad-t137.yAZeej/v3.1.2-run-f3/exits.json: invalid character '{' after top-level value
--- PASS: TestRollbackProbe (0.00s)
v3.2.0-rc.1
=== RUN   TestRollbackProbe
    rollback_probe_test.go:42: commit handle=qa error=ledger_tampered: commit ledger qa does not decode: invalid character '{' after top-level value; the refused document is quarantined at /private/tmp/amq-squad-t137.yAZeej/v3.2.0-rc.1-run-f3/extensions/amq-squad/quarantine/commits.qa.sequnknown
    rollback_probe_test.go:43: commit handle=qa tampered=true
    rollback_probe_test.go:42: commit handle=legacy error=<nil>
    rollback_probe_test.go:43: commit handle=legacy tampered=false
    rollback_probe_test.go:47: legacy commit error: <nil>
    rollback_probe_test.go:56: commit quarantine: commits.qa.sequnknown
--- PASS: TestRollbackProbe (0.08s)
=== RUN   TestRollbackProbe
    rollback_probe_test.go:39: scheduler tick error: <nil>
    rollback_probe_test.go:41: decision: ledger_tampered reason=owner: ledger_tampered: the exits ledger /private/tmp/amq-squad-t137.yAZeej/v3.2.0-rc.1-run-f3/exits.json: invalid character '{' after top-level value
    rollback_probe_test.go:41: decision: exits_ledger_unwritable reason=ledger_tampered: the exits ledger /private/tmp/amq-squad-t137.yAZeej/v3.2.0-rc.1-run-f3/exits.json: invalid character '{' after top-level value
--- PASS: TestRollbackProbe (0.00s)
```

Both tags reject migrated `qa`, read `legacy`, and advance only `legacy` and
its head successfully.

## M5: both tags read recovered F4

Output, verbatim:

Line numbers as of 8a79e6a1537069e025d637ea5898b04d1e10fc2b: the block's
`rollback_probe_test.go` is the saved probe copied into the tag tree, and its
lines are those of the saved seatcommit probes (`:42`, `:43`, `:47`, `:52`,
`:56`) and of `compat_probe_scheduler_test.go.txt` (`:39`, `:41`) at that
commit. The exits ledger in this block is `exits.json` at the fixture root,
where that commit's scheduler probe (`:28`) and fixture kept it; since round 2
both keep it at `extensions/amq-squad/scheduler/exits.json`, so the saved
probe as it now stands does not reproduce this path. (the #961 follow-up)

```text
v3.1.2
=== RUN   TestRollbackProbe
    rollback_probe_test.go:42: commit handle=qa error=ledger_tampered: commit ledger qa does not decode: invalid character '{' after top-level value
    rollback_probe_test.go:43: commit handle=qa tampered=true
    rollback_probe_test.go:42: commit handle=legacy error=<nil>
    rollback_probe_test.go:43: commit handle=legacy tampered=false
    rollback_probe_test.go:52: commit quarantine: open /private/tmp/amq-squad-t137.yAZeej/v3.1.2-run-f4/extensions/amq-squad/commits/quarantine: no such file or directory
--- PASS: TestRollbackProbe (0.00s)
=== RUN   TestRollbackProbe
    rollback_probe_test.go:39: scheduler tick error: <nil>
    rollback_probe_test.go:41: decision: ledger_tampered reason=owner: ledger_tampered: the exits ledger /private/tmp/amq-squad-t137.yAZeej/v3.1.2-run-f4/exits.json: invalid character '{' after top-level value
    rollback_probe_test.go:41: decision: exits_ledger_unwritable reason=ledger_tampered: the exits ledger /private/tmp/amq-squad-t137.yAZeej/v3.1.2-run-f4/exits.json: invalid character '{' after top-level value
--- PASS: TestRollbackProbe (0.00s)
v3.2.0-rc.1
=== RUN   TestRollbackProbe
    rollback_probe_test.go:42: commit handle=qa error=ledger_tampered: commit ledger qa does not decode: invalid character '{' after top-level value; the refused document is quarantined at /private/tmp/amq-squad-t137.yAZeej/v3.2.0-rc.1-run-f4/extensions/amq-squad/quarantine/commits.qa.sequnknown
    rollback_probe_test.go:43: commit handle=qa tampered=true
    rollback_probe_test.go:42: commit handle=legacy error=<nil>
    rollback_probe_test.go:43: commit handle=legacy tampered=false
    rollback_probe_test.go:56: commit quarantine: commits.qa.seq2
    rollback_probe_test.go:56: commit quarantine: commits.qa.sequnknown
--- PASS: TestRollbackProbe (0.04s)
=== RUN   TestRollbackProbe
    rollback_probe_test.go:39: scheduler tick error: <nil>
    rollback_probe_test.go:41: decision: ledger_tampered reason=owner: ledger_tampered: the exits ledger /private/tmp/amq-squad-t137.yAZeej/v3.2.0-rc.1-run-f4/exits.json: invalid character '{' after top-level value
    rollback_probe_test.go:41: decision: exits_ledger_unwritable reason=ledger_tampered: the exits ledger /private/tmp/amq-squad-t137.yAZeej/v3.2.0-rc.1-run-f4/exits.json: invalid character '{' after top-level value
--- PASS: TestRollbackProbe (0.01s)
```

The recovery record does not make the raw layout readable to either old tag;
rc.1 preserves the recovery slot and adds its own unknown-sequence copy.

## M6: before and after file hashes

The inventory command, run immediately around every old-build pair, was:

```sh
find "$root" -type f -exec shasum -a 256 {} \; | sort -k2
```

The tables below are the round-1 inventory, as of
8a79e6a1537069e025d637ea5898b04d1e10fc2b. They carry no line numbers. Their
`exits.json` rows at the fixture root are where that commit's fixture and
saved scheduler probe kept the exits document
(`compat_probe_scheduler_test.go.txt:28` at that commit); since round 2 both
keep it at `extensions/amq-squad/scheduler/exits.json`, so the saved probe as
it now stands does not reproduce those rows' paths. Paths are relative to each
fixture root. `-` means the file did not exist before the old-build run. (the
#961 follow-up)

### v3.1.2

| F | File | Before SHA-256 | After SHA-256 |
| --- | --- | --- | --- |
| F1 | `exits.json` | `b0755a9127615548a0d6297835e15d8ff652f1eba080461b77b1ffe93ee882ad` | same |
| F1 | `extensions/amq-squad/commits/legacy.json` | `6b066417c3de076fce21ec823d37ecbfb82f44323424d2cfb7bda325da86fec0` | same |
| F1 | `extensions/amq-squad/commits/qa.json` | `f8e9a1e5e2bc9dde0dd253b9e6869d5f57e3f3674df0c548b92ca4cd2e96bde2` | same |
| F1 | `extensions/amq-squad/routing/heads/commits.legacy.json` | `73998f951474456a882e15c799bd05edd9747db1d98abaecdfa80ae5f7a62197` | same |
| F1 | `extensions/amq-squad/routing/heads/commits.qa.json` | `3bb9e18c49b5d1fc9733fd9b10247294c59eb69a1c2c6d13b23e937c83bee305` | same |
| F1 | `extensions/amq-squad/routing/heads/exits.json` | `107fdfecf5884a8a7b0a3c4514786693d4ea9bb06df05e92089fbc94df51b1d0` | same |
| F1 | `extensions/amq-squad/routing/seed` | `5cdc83cf6230d9ed6ad6b8bc4c8fa6b337e28fb728e566c194c05f9ef407ea45` | same |
| F1 | `extensions/amq-squad/scheduler/decisions.jsonl` | `39e59544d468c61ea381e05bc30b9af8a69b3b4c454424bf84c99af1c8d374e9` | same |
| F2 | `exits.json` | `ccc36b7aec1b660d8808f177c5bab39756804f2359500be84f677221039dc049` | same |
| F2 | `extensions/amq-squad/commits/legacy.json` | `83451971db003363d05b8a0ef397f4d2fa6a680db752de3c1320ef8f0207ab5b` | same |
| F2 | `extensions/amq-squad/commits/qa.json` | `39f7ef0fcc0357171337d0b18a9b5cac9148f8855cc522cf095196fe09547969` | same |
| F2 | `extensions/amq-squad/routing/heads/commits.legacy.json` | `05b1ab5295095ba0ad1ec5cf7b3785257c6b43e9a95e3466b07678ce7fcf182a` | same |
| F2 | `extensions/amq-squad/routing/heads/commits.qa.json` | `67879ba08721658df425b284d4f25b3850b1ac85530edbc9679f90be751b68da` | same |
| F2 | `extensions/amq-squad/routing/heads/exits.json` | `5aa9af927d0f6c20f9ad3535d91ceede92a5717a4660cef235111f96a9549f5b` | same |
| F2 | `extensions/amq-squad/routing/seed` | `e23fcedc59b26a59db65d6bf509b45274fe35f51f7f6aa16f1d032d73ecff6d6` | same |
| F2 | `extensions/amq-squad/scheduler/decisions.jsonl` | `cf799ea8675fb13938c649e0df51e406323576324a9ac24738c7e75fc42ef006` | same |
| F3 | `exits.json` | `92977581e2f4bfd1084836dc499e3ebc637a43c0d096c1629629670b2c082994` | same |
| F3 | `extensions/amq-squad/commits/legacy.json` | `68fb0476a107902e381c0f2e5e0bac56b584c954fb033f443321a2eebef5930f` | `cc1c1465a790dbd2b85793fd80b095d9f76089ffebd7798a86b2c00375a5e30d` |
| F3 | `extensions/amq-squad/commits/qa.json` | `897917fd19e5d3fd00b39d0bafc77a6e8fa7d4a9ad55f9758a36ec64d0b04db0` | same |
| F3 | `extensions/amq-squad/routing/heads/commits.legacy.json` | `01ce6646314cb0dfa796eb94a618767e8877bfc7a314d987248ef0f94dcffad6` | `3621921e35ddfdc8da3b9a1eb290158cf527bc74f9b3b595d21a4d7e72c0f640` |
| F3 | `extensions/amq-squad/routing/heads/commits.qa.json` | `43d72a450398423fba1e580b7f4193aae7762a95c3f3d7d8183c67eb9b08d68e` | same |
| F3 | `extensions/amq-squad/routing/heads/exits.json` | `466c95f5062f488606a3c8836cb7ca6b08e4ad1ac9edf960d8bf6578affee8cd` | same |
| F3 | `extensions/amq-squad/routing/seed` | `4af9e7996c95adb1c81b33a9bc3fd0a609aff3226319f18efc0cfd103bd3b57f` | same |
| F3 | `extensions/amq-squad/scheduler/decisions.jsonl` | `6a1605f67d5805fe6f46f57da23d4602a81a06b089b634cb0c78393f5974eff8` | same |
| F4 | `exits.json` | `bc1ab5374e43b45c18e76e80b4f6c20cd2ffb52a0ca5bc2f4e784cdc2d846c71` | same |
| F4 | `extensions/amq-squad/commits/legacy.json` | `22db1f2ff9edfad9ea808dedeef1a7617e701fb734e5009a9bf838ee419a9a75` | same |
| F4 | `extensions/amq-squad/commits/qa.json` | `213b14abf39682d2b7949529d55910edde1490c20213fd817494d5fb6bcfed88` | same |
| F4 | `extensions/amq-squad/commits/qa.lock` | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` | same |
| F4 | `extensions/amq-squad/quarantine/commits.qa.seq2/body.json` | `4968e4c46bbcc026cffc520cb8f705001ac742691d3235909f4e51d245125609` | same |
| F4 | `extensions/amq-squad/quarantine/commits.qa.seq2/head.json` | `475fb301920eb68d04043b59dd6ca4177848404b435d09f746459f6fefa6f1c1` | same |
| F4 | `extensions/amq-squad/quarantine/commits.qa.seq2/quarantine.txt` | `ecbcb8fb88d8771a62c97d580815a8d0c96100dc3522b94ea40f65c3888716d7` | same |
| F4 | `extensions/amq-squad/quarantine/commits.qa.seq2/recovered.txt` | `a4864cb4988fbe8ed3cdd82af9ff34260e734a57b2b17c58eec17c8727d28c95` | same |
| F4 | `extensions/amq-squad/routing/heads/commits.legacy.json` | `8cb518e67943ff267a806a28ddce39f367fd07ebdce910708697ba7c4abfaf10` | same |
| F4 | `extensions/amq-squad/routing/heads/commits.qa.json` | `361e18555af62617e667a0804c570226ea5940ac5624c97e31de2a9633315bc0` | same |
| F4 | `extensions/amq-squad/routing/heads/exits.json` | `2bbf2a3cb01ecf070676958e68e9199bae562c20ab84702227e68f264290fc8b` | same |
| F4 | `extensions/amq-squad/routing/seed` | `5554b300bc21e7a8eac27967f0e2ca1ec41ba71d18ed1500ba88834007168e8c` | same |
| F4 | `extensions/amq-squad/scheduler/decisions.jsonl` | `f6700653608796cc364e0704e51f4f69aec7e4e497aea158593aa5d80ad1a4d8` | same |

Every v3.1.2 run also created an empty `extensions/amq-squad/tasks/.lock`
with SHA-256 `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855`.
That is not a sealed ledger document or head.

### v3.2.0-rc.1

| F | File | Before SHA-256 | After SHA-256 |
| --- | --- | --- | --- |
| F1 | `exits.json` | `46e2dc4897473319f194615303eef2a41e244fd74e383201382eccfa02f841b2` | same |
| F1 | `extensions/amq-squad/commits/legacy.json` | `8e00e4dfd639ddaf2342e5de08a4c8e243b552a01d00c633ae002932f86bb2f1` | same |
| F1 | `extensions/amq-squad/commits/qa.json` | `84c25aa6eb899b918925ebfa180ec9141bfc3a2ae7ade6cf7b68dd1a583b853d` | same |
| F1 | `extensions/amq-squad/routing/heads/commits.legacy.json` | `d4ea4351b12b1777960c7b9df3af12d045411c16cdcaaf3971eba6fe7734855e` | same |
| F1 | `extensions/amq-squad/routing/heads/commits.qa.json` | `695313f2bcafb7601239132b0cc9b54c8cd4f8a9b1574521681d37703e363b26` | same |
| F1 | `extensions/amq-squad/routing/heads/exits.json` | `ae25c4b5558993189a883a10ff82dbb32818ba37228067dbc6513456ec031db9` | same |
| F1 | `extensions/amq-squad/routing/seed` | `0a4b9c9cbddc75ce37068d7c11caa4ca005830a6311440c64229ca83f3bd0c59` | same |
| F1 | `extensions/amq-squad/scheduler/decisions.jsonl` | `98a58666a54e3ee3dc69814d0398c3832f41794384719771ab14f294ea1d164f` | same |
| F2 | `exits.json` | `509273f879fd7f9a4f0bb77f58036485e97710372498951399160d48685307a5` | same |
| F2 | `extensions/amq-squad/commits/legacy.json` | `ad51cc6b7bb2ab7bc7307bc8e92cac869ace30f1073a26f8b4c7d92e5eb831d2` | same |
| F2 | `extensions/amq-squad/commits/qa.json` | `ab41a50f27a99f2921354dfd2a7308cc0d793d398d8b449be3e794803ebfcc8e` | same |
| F2 | `extensions/amq-squad/routing/heads/commits.legacy.json` | `ac2e859d0140ca84c55b3db674aaab2a24459f3056f42037fb60a43b896c5a6b` | same |
| F2 | `extensions/amq-squad/routing/heads/commits.qa.json` | `06b1b38e4a98ec6933de846776d04c015c187da8fcc55c7ea6f99c0511c6f565` | same |
| F2 | `extensions/amq-squad/routing/heads/exits.json` | `f893924b2a72ed10e700e0794fe66f3d3007434b65a4c933772482d6b46e9083` | same |
| F2 | `extensions/amq-squad/routing/seed` | `ddf3941459759c6077dbf4d1783160660beeaa21a8179e81d71fa2afb3e9b74f` | same |
| F2 | `extensions/amq-squad/scheduler/decisions.jsonl` | `2a43671e7a34d860d164a08d9e90cf6722fe4c2459b4af32d5667bf2819e824e` | same |
| F3 | `exits.json` | `6272d1a5c0d5c1483ff5e01ae22b6d7705aa05655e24d6a477e72000ee818baf` | same |
| F3 | `extensions/amq-squad/commits/legacy.json` | `bd6c37ef008734be7b38ac3ae0dac62adef86cbddb7076e8c4c4c9a17ab659f6` | `1bd52440de906880a6010231535644de7071438abf332cf7e1d4664aaed5e272` |
| F3 | `extensions/amq-squad/commits/qa.json` | `f0bd1a5bac4b596280de4d7b6ab99c1c7dba8e992e3f13d9964e7d13b563f9fe` | same |
| F3 | `extensions/amq-squad/routing/heads/commits.legacy.json` | `117e5b8230462d6ed1d2e5e318c8d362ed85052f4bdd668089d7ae858b49ab42` | `3df67cce0512c5d87b03103903a9985f97b6868a5e1f4527bab7cdd1e55adc9b` |
| F3 | `extensions/amq-squad/routing/heads/commits.qa.json` | `bf6acba15521082249a4f8d0f0e34678a1e5c00320d53a6b1283fc643126c8b5` | same |
| F3 | `extensions/amq-squad/routing/heads/exits.json` | `d7ca908163e909d8051d342cfb2e458af1744501f4c6ae154ab785ced2547115` | same |
| F3 | `extensions/amq-squad/routing/seed` | `957a7b86094c5bc34cd8a2cabf00800bdccc51dff80f946ea3f97f2fef2720ec` | same |
| F3 | `extensions/amq-squad/scheduler/decisions.jsonl` | `c9ec0764503ceebef1ff2032eefcd6c246d7443089b1f1aa5c3addc3107efcba` | same |
| F4 | `exits.json` | `e2471c6204a3240771d64e69ddb8d766a338596f4a94690e802fa498acc4d525` | same |
| F4 | `extensions/amq-squad/commits/legacy.json` | `3a9405988dad84e67d1ae9a6dbbf2056140e6cd34d7a195d5a62f635897a4782` | same |
| F4 | `extensions/amq-squad/commits/qa.json` | `dfa71a88113395a3bb0251fd25643a444d5fed418ebd8d5a4caf4c7219122580` | same |
| F4 | `extensions/amq-squad/commits/qa.lock` | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` | same |
| F4 | `extensions/amq-squad/quarantine/commits.qa.seq2/body.json` | `afdb458798f32dedd236343d52fc96e394385c3c4a5ac5dd833b54a81a78fb34` | same |
| F4 | `extensions/amq-squad/quarantine/commits.qa.seq2/head.json` | `18c0278886b4041b928d61ceff8f6fc9aeaa0d90de83e6c15a054061f7620390` | same |
| F4 | `extensions/amq-squad/quarantine/commits.qa.seq2/quarantine.txt` | `88f70834e0a59b5161704c4874c56f1c3b6427584ad6f9241d3b32a795cff89f` | same |
| F4 | `extensions/amq-squad/quarantine/commits.qa.seq2/recovered.txt` | `a4864cb4988fbe8ed3cdd82af9ff34260e734a57b2b17c58eec17c8727d28c95` | same |
| F4 | `extensions/amq-squad/routing/heads/commits.legacy.json` | `e6ffbbce307af0768d2166a42d26df8e6b2d0307f9ee517c496d60e46afdd53e` | same |
| F4 | `extensions/amq-squad/routing/heads/commits.qa.json` | `429ff666ae6b4540076b490acc5e235c4bffcd0e1d6d7e16194227360ad39b1d` | same |
| F4 | `extensions/amq-squad/routing/heads/exits.json` | `7aff445a20d6ac090e2e78d6c05640e3091cba3b8d6eefe023c5be0ad1a5b1bc` | same |
| F4 | `extensions/amq-squad/routing/seed` | `db9883fa22382f32626beba54cc0f2f451176faac933440cb8ab49986fedbfab` | same |
| F4 | `extensions/amq-squad/scheduler/decisions.jsonl` | `757b1f13fdd56003f517c336cd1dc6f29836657b7ebcfd60b0f373022c2b98ac` | same |

For rc.1, each run added a quarantine copy whose `body.json` and `head.json`
hashes exactly matched the refused live `qa` document and head. F1 and F3
used `commits.qa.sequnknown`; F2 used `commits.qa.seq2`; F4 added
`commits.qa.sequnknown` beside the existing recovered `seq2` slot. The
corresponding `quarantine.txt` hashes were, in order, F1
`530979f859ae27fe0a5164d6438acaebccf2d2d3963ed1aba42d6699725933dd`,
F2 `bf3120549ee1715662adaac5a1694b8cc38c035d6e7cb2cee3827666c709749b`,
F3 `781dfade124b13ca98926b22f42823f36810f5bb9de6b5f7bb9fa27682da3b12`,
and F4 `ce72a7ab2ace98581403ac152c49d1e14483957ef45182a44cad872917cf0a6a`.
Each run also added the same empty task-store lock as v3.1.2.

The only changed live sealed document or head in the complete diff was the F3
legacy document and its head, which the probe deliberately asked the old tag
to advance. v3.1.2 did not change, move, or delete any migrated live sealed
document or head. rc.1 also did not change, move, or delete one; its
quarantine copies were additions outside the live paths. Neither old
scheduler appended in this round-1 bare-Scheduler probe; round 2 replaces the
cause with the process-path measurement below. (round 2)

## M7: roll forward

After each old run, the current build used the same root:

```sh
AMQSQ_ROLLFORWARD_ROOT="$root" \
  go test ./internal/seatcommit -run TestRollbackRollForwardCompatibility -count=1 -v
AMQSQ_ROLLFORWARD_ROOT="$root" \
  go test ./internal/scheduler -run TestRollbackRollForwardSchedulerCompatibility -count=1 -v
```

The test asserts that neither commit read returns `sealchain.ErrTampered` and
that no scheduler decision has kind `ledger_tampered`. Output, verbatim:

Line numbers in this block are those of the seat's working tree at the run,
one line before the commit that landed: as of
8a79e6a1537069e025d637ea5898b04d1e10fc2b the log calls are at
`internal/seatcommit/raw_compat_test.go:149` and
`internal/scheduler/raw_exits_test.go:165` and `:167`. The exits ledger here
is `exits.json` at the fixture root, as in M1 to M5. (the #961 follow-up)

```text
v3.1.2 F1
    raw_compat_test.go:148: roll-forward read handle=qa error=<nil>
    raw_compat_test.go:148: roll-forward read handle=legacy error=<nil>
    raw_exits_test.go:164: roll-forward scheduler tick error: <nil>
    raw_exits_test.go:166: roll-forward decision: exit_check_deferred reason=the preflight copy is unreadable
    raw_exits_test.go:166: roll-forward decision: exit_check_deferred reason=the preflight copy is unreadable
    raw_exits_test.go:166: roll-forward decision: exit_check_deferred reason=the preflight copy is unreadable
v3.1.2 F2
    raw_compat_test.go:148: roll-forward read handle=qa error=<nil>
    raw_compat_test.go:148: roll-forward read handle=legacy error=<nil>
    raw_exits_test.go:164: roll-forward scheduler tick error: <nil>
    raw_exits_test.go:166: roll-forward decision: exit_check_deferred reason=the preflight copy is unreadable
    raw_exits_test.go:166: roll-forward decision: exit_check_deferred reason=the preflight copy is unreadable
v3.1.2 F3
    raw_compat_test.go:148: roll-forward read handle=qa error=<nil>
    raw_compat_test.go:148: roll-forward read handle=legacy error=<nil>
    raw_exits_test.go:164: roll-forward scheduler tick error: <nil>
    raw_exits_test.go:166: roll-forward decision: exit_check_deferred reason=the preflight copy is unreadable
    raw_exits_test.go:166: roll-forward decision: exit_check_deferred reason=the preflight copy is unreadable
    raw_exits_test.go:166: roll-forward decision: exit_check_deferred reason=the preflight copy is unreadable
v3.1.2 F4
    raw_compat_test.go:148: roll-forward read handle=qa error=<nil>
    raw_compat_test.go:148: roll-forward read handle=legacy error=<nil>
    raw_exits_test.go:164: roll-forward scheduler tick error: <nil>
    raw_exits_test.go:166: roll-forward decision: exit_check_deferred reason=the preflight copy is unreadable
    raw_exits_test.go:166: roll-forward decision: exit_check_deferred reason=the preflight copy is unreadable
    raw_exits_test.go:166: roll-forward decision: exit_check_deferred reason=the preflight copy is unreadable
v3.2.0-rc.1 F1
    raw_compat_test.go:148: roll-forward read handle=qa error=<nil>
    raw_compat_test.go:148: roll-forward read handle=legacy error=<nil>
    raw_exits_test.go:164: roll-forward scheduler tick error: <nil>
    raw_exits_test.go:166: roll-forward decision: exit_check_deferred reason=the preflight copy is unreadable
    raw_exits_test.go:166: roll-forward decision: exit_check_deferred reason=the preflight copy is unreadable
    raw_exits_test.go:166: roll-forward decision: exit_check_deferred reason=the preflight copy is unreadable
v3.2.0-rc.1 F2
    raw_compat_test.go:148: roll-forward read handle=qa error=<nil>
    raw_compat_test.go:148: roll-forward read handle=legacy error=<nil>
    raw_exits_test.go:164: roll-forward scheduler tick error: <nil>
    raw_exits_test.go:166: roll-forward decision: exit_check_deferred reason=the preflight copy is unreadable
    raw_exits_test.go:166: roll-forward decision: exit_check_deferred reason=the preflight copy is unreadable
v3.2.0-rc.1 F3
    raw_compat_test.go:148: roll-forward read handle=qa error=<nil>
    raw_compat_test.go:148: roll-forward read handle=legacy error=<nil>
    raw_exits_test.go:164: roll-forward scheduler tick error: <nil>
    raw_exits_test.go:166: roll-forward decision: exit_check_deferred reason=the preflight copy is unreadable
    raw_exits_test.go:166: roll-forward decision: exit_check_deferred reason=the preflight copy is unreadable
    raw_exits_test.go:166: roll-forward decision: exit_check_deferred reason=the preflight copy is unreadable
v3.2.0-rc.1 F4
    raw_compat_test.go:148: roll-forward read handle=qa error=<nil>
    raw_compat_test.go:148: roll-forward read handle=legacy error=<nil>
    raw_exits_test.go:164: roll-forward scheduler tick error: <nil>
    raw_exits_test.go:166: roll-forward decision: exit_check_deferred reason=the preflight copy is unreadable
    raw_exits_test.go:166: roll-forward decision: exit_check_deferred reason=the preflight copy is unreadable
    raw_exits_test.go:166: roll-forward decision: exit_check_deferred reason=the preflight copy is unreadable
```

All sixteen tests passed. A read alone leaves F2's qa head legacy; the next
ordinary write completes its raw head and records exactly one migration.
F3's old-tag legacy commit migrates once on its next current write. (round 2)
The `the preflight copy is unreadable` text above comes from the round-1 test
double; the real process-path result is in P5/P6 below. (round 2)

## M8: downgrade refusal

`TestUpRefusesEveryBinaryBeforeTheRawLayout` now covers a fresh raw chain, a
legacy chain migrated by its next write as F1, and a migrated chain with an
untouched legacy sibling as F3. Each fixture rejects `v3.1.2` and
`v3.2.0-rc.1` with the `ledger_layout_downgrade` detail. Output, verbatim:

```text
=== RUN   TestUpRefusesEveryBinaryBeforeTheRawLayout
=== RUN   TestUpRefusesEveryBinaryBeforeTheRawLayout/fresh_raw_chain
=== RUN   TestUpRefusesEveryBinaryBeforeTheRawLayout/migrated_legacy_chain
=== RUN   TestUpRefusesEveryBinaryBeforeTheRawLayout/migrated_chain_with_legacy_sibling
--- PASS: TestUpRefusesEveryBinaryBeforeTheRawLayout (0.13s)
    --- PASS: TestUpRefusesEveryBinaryBeforeTheRawLayout/fresh_raw_chain (0.02s)
    --- PASS: TestUpRefusesEveryBinaryBeforeTheRawLayout/migrated_legacy_chain (0.03s)
    --- PASS: TestUpRefusesEveryBinaryBeforeTheRawLayout/migrated_chain_with_legacy_sibling (0.05s)
PASS
ok  	github.com/omriariav/amq-squad/v3/internal/cli	0.592s
```

The negative control removed the migrated fixture's raw successor and reran
the test. It failed on the first old version, proving the row depends on the
migrated raw layout:

`up_raw_layout_test.go:71` in this block is a line of the negative control's
edited tree, which was not kept; it matches no committed line. As of
8a79e6a1537069e025d637ea5898b04d1e10fc2b the same `t.Fatalf("%s: %v", version,
err)` is at `internal/cli/up_raw_layout_test.go:89`, `:97` and `:156`. (the
#961 follow-up)

```text
=== RUN   TestUpRefusesEveryBinaryBeforeTheRawLayout
=== RUN   TestUpRefusesEveryBinaryBeforeTheRawLayout/fresh_raw_chain
=== RUN   TestUpRefusesEveryBinaryBeforeTheRawLayout/migrated_legacy_chain
    up_raw_layout_test.go:71: v3.1.2: <nil>
--- FAIL: TestUpRefusesEveryBinaryBeforeTheRawLayout (0.21s)
    --- PASS: TestUpRefusesEveryBinaryBeforeTheRawLayout/fresh_raw_chain (0.07s)
    --- FAIL: TestUpRefusesEveryBinaryBeforeTheRawLayout/migrated_legacy_chain (0.07s)
FAIL
FAIL	github.com/omriariav/amq-squad/v3/internal/cli	0.688s
FAIL
```

The source was restored before the passing command above.

## Gates

Run in the seat sandbox with an isolated Go build cache:

- The focused M7/M8 command over `internal/seatcommit`, `internal/scheduler`,
  and `internal/cli` passed.
- The complete `internal/scheduler` package passed. The complete
  `internal/seatcommit` and `internal/cli` package command reached unrelated
  listener rows and failed because the sandbox denies both loopback TCP and
  Unix socket binds.
- `go build ./...` and `go vet ./...` exited 0. Go printed one non-fatal
  warning when it could not write the host module stat cache.
- `go run ./scripts/redact-evidence -check
  docs/v3-spike/320-rc2-rollback/` exited 0.
- `NPM_FLAGS=--offline make ci`, with live `AM_*` and `AMQ_*` variables
  removed in the subprocess, completed the repository-wide Go test pass and
  exited 1 only on the same sandbox boundaries. The failures were loopback
  listeners in `internal/buzz`, `internal/gateverify`, `internal/registry`,
  and `internal/seatcommit`; Unix decision, lead, and proof sockets in
  `internal/cli`, `internal/schedproc`, and `internal/seatlaunch`; and one
  unrelated `internal/mergetrain` temporary-directory cleanup race. The
  changed scheduler package and the other non-listener packages passed.

## Limits

- The old-tag halves compile and execute the old packages and their own
  unexported ledger writers from exact tag archives. No old tag binary was
  installed into the live session and no network access occurred.
- Every fixture began with commit and exits bytes written by the named tag's
  own writer. No measurement used a copied fixture root.
- Both old scheduler ticks, including both F3 halves, ran successfully in the
  seat sandbox. There is no host-only missing half.
- M8 is the in-package test of the same `checkLedgerDowngrade` boundary called
  by `amq-squad up --binary`; it does not launch a tag executable.
- The recovery fixture uses the scheduler's recovery write primitive directly
  after constructing the same verified quarantine slot. The command transport
  is covered separately by `internal/cli/scheduler_recover_test.go`.

All disposable tag trees, baseline roots, fixture roots, and the isolated Go
cache were removed after the final gates.

## Round 2

Round 2 is measured on base `30f4607e09a78cd025d5911161aea6053344b0d3`; the
task's carried attempt was applied byte-for-byte before the four reissued rows
were run. The production rows are `internal/cli/up_raw_layout_test.go`,
`internal/seatcommit/raw_compat_test.go`,
`internal/scheduler/raw_exits_test.go`,
`internal/schedproc/rollback_rows_test.go`, and the `rollback_rows_test.go`
guards in sealchain, task, routing, mergetrain, and cli. File:line citations
in this section are as of `487e8560f0d4e70af1452c10f01a1b3f8042c9b3`, the head
that was gated and merged (#961, merge commit
`d5de253a22fb9b759a316ed128497a5f4326af40`, whose tree equals that head's).

This section was corrected after the merge by a docs-only follow-up. As
merged, it carried no command and no output for P5, P6, P9, P13 and P15, and
several of its sentences said more than the rows assert. The commands and the
pasted lines below are from the lead's reruns on
`487e8560f0d4e70af1452c10f01a1b3f8042c9b3` on 2026-11-11. A sentence that
rests on a value a row logs and does not assert says "by the lead's rerun".
The revert entries, the gate list and the seat's own measurements are kept
where they stand and are named as the seat's.

In a pasted block `<root>` stands for the fixture root named above the block,
`<rerun>` for the lead's run directory, `<current>` for a checkout of
`487e8560f0d4e70af1452c10f01a1b3f8042c9b3`, and `<v3.1.2>` and `<v3.2.0-rc.1>`
for the unpacked tag archives; nothing else in a block is changed. A `$` line
is the command as the run script echoed it: in the P1, P5, P6, P7 and P9
blocks the `-run` pattern appears without its shell quotes (it was passed as
one argument) and the line carries no directory (P5 and P9 ran in the tag's
tree, P1, P6 and P7 in `<current>`). `exit N` is the command's exit status as
the script recorded it.

### How the lead's reruns were made

Each tag's tree was made by `git archive <tag> | tar -x -C <dir>`. The saved
probes were copied in as `internal/seatcommit/compat_probe_test.go` (the tag's
own `compat_probe_<tag>_seatcommit_test.go.txt`),
`internal/scheduler/compat_probe_test.go`
(`compat_probe_scheduler_test.go.txt`) and
`internal/schedproc/compat_probe_process_test.go`
(`compat_probe_process_test.go.txt`), so a block's `compat_probe_test.go:<n>`
is line `<n>` of that saved probe. Every command in a tag's tree ran with
`GOPROXY=off`.

F1 to F4 were made per root by four commands, as in Fixture construction
above. In the tag's tree:

```sh
AMQSQ_ROLLBACK_ROOT=<root> AMQSQ_ROLLBACK_PHASE=write GOPROXY=off go test -count=1 ./internal/seatcommit -run TestRollbackProbe -v
AMQSQ_ROLLBACK_ROOT=<root> AMQSQ_ROLLBACK_PHASE=write GOPROXY=off go test -count=1 ./internal/scheduler -run TestRollbackProbe -v
```

Then in `<current>`, with `<n>` the fixture's number:

```sh
AMQSQ_ROLLBACK_FIXTURE_ROOT=<root> AMQSQ_ROLLBACK_MODE=f<n> go test ./internal/seatcommit -run TestContinueRollbackCompatibilityFixture -count=1 -v
AMQSQ_ROLLBACK_FIXTURE_ROOT=<root> AMQSQ_ROLLBACK_MODE=f<n> go test ./internal/scheduler -run TestContinueRollbackExitsFixture -count=1 -v
```

F5a and F5b were written in `<current>`, one directory per tag, by the command
below, which writes `<dir>/f5a` and `<dir>/f5b`:

```sh
AMQSQ_WRITE_CANCEL_ROOT=<dir> go test ./internal/scheduler -run TestWriteRollbackCancelFixtures -count=1 -v
```

That is 20 roots, all created in place under one temporary directory: eight F1
to F4 roots for P9 (used again for P7 after the old-build reads), eight F1 to
F4 roots for P5 and P6, and F5a and F5b for each tag. Every fixture command
exited 0. Before and after each run a script wrote an inventory of the root
(the sha256 and the relative path of every regular file, sorted by path) and a
second script compared the two; the inventories are not printed here, and
"added", "changed" and "byte-equal" below are that comparison's answers. Paths
in those answers are relative to `<root>/extensions/amq-squad/`.

The runs, all on 2026-11-11 (IST): P13 12:37:21 to 12:37:30; the fixtures, P9,
P5, P6, the first P15 attempt and P1 12:37:33 to 12:38:47; P15 12:39:14; P7
13:12:50 to 13:12:59.

### P1 to P4: interrupted migration preflight

`TestUpDowngradeInterruptedMigrationMatrix`
(`internal/cli/up_raw_layout_test.go:204`) asks `checkLedgerDowngrade` about a
commit ledger and about the exits ledger, each in six forms of a legacy head
beside a document:

| Legacy head plus document | Answer the row asserts |
| --- | --- |
| legacy document | admitted for v3.1.2 |
| authenticated exact raw successor | v3.1.2 refused as `ledger_layout_downgrade` with "predates that layout" (`:227-228`); v3.2.0-rc.1 refused, asserted only as a non-nil error (`:230-231`); `v3.2.0-rc.2`, `v3.2.0` and `v4.0.0` admitted (`:233-236`) |
| raw shape with a bad seal | admitted for v3.1.2 |
| raw shape with a wrong sequence: the seal names the predecessor's sequence plus one and keeps its tag (`:85-86`) | admitted for v3.1.2 |
| no document | admitted for v3.1.2 |
| "neither layout": an empty session, with no head and no document (`:40-42`) | admitted for v3.1.2 |

The five forms other than the authenticated successor are asked only about
v3.1.2 (`:224`, `:238-239`). For every form the row then asserts that the
check wrote nothing: where the form has them, the document and the head are
byte-equal afterwards, and no decisions log and no quarantine directory exist
(`:241-260`). The fixture named "wrong sequence" changes the sequence only; a
predecessor with a wrong tag beside a legacy head has no row (Limits, below).
Malformed forms remain the ledger reader's tamper decision, not the install
preflight's.

`sessionRawLayout` (`internal/cli/up.go:411`) reads each head and then its
document once per call (`:434`, then `:446` or `:455`) and hands both to
`InterruptedMigrationWriter` (`:450`) or `InterruptedExitsMigrationWriter`
(`:459`). "Once" is per call: an install whose first call finds the raw layout
or an interrupted migration makes a second (`checkLedgerTarget` at `:379`,
then `checkLedgerDowngrade` at `:391`).
`TestUpAsksEveryInterruptedMigrationReader` (`up_raw_layout_test.go:266`) is a
source check, not a behavioural row: it collects the packages that call
`sealchain.CheckRawMigration` from the syntax trees (`:290`), requires them to
be `scheduler` and `seatcommit` (`:301`), and looks for the two recognisers'
names in `up.go`'s text (`:304-311`).

The lead's rerun, in `<current>`. The pattern's fourth name,
`TestUpRawMigrationCallersStayAligned`, is the lead's error: no such test
exists and it matches nothing. The source check ran in the second command of
the P15 block below.

```text
$ go test -count=1 ./internal/cli -run ^(TestUpRefusesEveryBinaryBeforeTheRawLayout|TestUpDowngradeInterruptedMigrationMatrix|TestUpRefusesAnUnparseableTargetWhenRawLedgersExist|TestUpRawMigrationCallersStayAligned)$ -v
=== RUN   TestUpRefusesEveryBinaryBeforeTheRawLayout
=== RUN   TestUpRefusesEveryBinaryBeforeTheRawLayout/fresh_raw_chain
=== RUN   TestUpRefusesEveryBinaryBeforeTheRawLayout/migrated_legacy_chain
=== RUN   TestUpRefusesEveryBinaryBeforeTheRawLayout/migrated_chain_with_legacy_sibling
--- PASS: TestUpRefusesEveryBinaryBeforeTheRawLayout (0.13s)
    --- PASS: TestUpRefusesEveryBinaryBeforeTheRawLayout/fresh_raw_chain (0.03s)
    --- PASS: TestUpRefusesEveryBinaryBeforeTheRawLayout/migrated_legacy_chain (0.03s)
    --- PASS: TestUpRefusesEveryBinaryBeforeTheRawLayout/migrated_chain_with_legacy_sibling (0.05s)
=== RUN   TestUpDowngradeInterruptedMigrationMatrix
=== RUN   TestUpDowngradeInterruptedMigrationMatrix/commit/legacy_document
=== RUN   TestUpDowngradeInterruptedMigrationMatrix/commit/authenticated_raw_successor
=== RUN   TestUpDowngradeInterruptedMigrationMatrix/commit/raw-shaped_bad_seal
=== RUN   TestUpDowngradeInterruptedMigrationMatrix/commit/raw-shaped_wrong_sequence
=== RUN   TestUpDowngradeInterruptedMigrationMatrix/commit/no_document
=== RUN   TestUpDowngradeInterruptedMigrationMatrix/commit/neither_layout
=== RUN   TestUpDowngradeInterruptedMigrationMatrix/exits/legacy_document
=== RUN   TestUpDowngradeInterruptedMigrationMatrix/exits/authenticated_raw_successor
=== RUN   TestUpDowngradeInterruptedMigrationMatrix/exits/raw-shaped_bad_seal
=== RUN   TestUpDowngradeInterruptedMigrationMatrix/exits/raw-shaped_wrong_sequence
=== RUN   TestUpDowngradeInterruptedMigrationMatrix/exits/no_document
=== RUN   TestUpDowngradeInterruptedMigrationMatrix/exits/neither_layout
--- PASS: TestUpDowngradeInterruptedMigrationMatrix (0.23s)
    --- PASS: TestUpDowngradeInterruptedMigrationMatrix/commit/legacy_document (0.02s)
    --- PASS: TestUpDowngradeInterruptedMigrationMatrix/commit/authenticated_raw_successor (0.02s)
    --- PASS: TestUpDowngradeInterruptedMigrationMatrix/commit/raw-shaped_bad_seal (0.02s)
    --- PASS: TestUpDowngradeInterruptedMigrationMatrix/commit/raw-shaped_wrong_sequence (0.02s)
    --- PASS: TestUpDowngradeInterruptedMigrationMatrix/commit/no_document (0.02s)
    --- PASS: TestUpDowngradeInterruptedMigrationMatrix/commit/neither_layout (0.00s)
    --- PASS: TestUpDowngradeInterruptedMigrationMatrix/exits/legacy_document (0.02s)
    --- PASS: TestUpDowngradeInterruptedMigrationMatrix/exits/authenticated_raw_successor (0.02s)
    --- PASS: TestUpDowngradeInterruptedMigrationMatrix/exits/raw-shaped_bad_seal (0.02s)
    --- PASS: TestUpDowngradeInterruptedMigrationMatrix/exits/raw-shaped_wrong_sequence (0.02s)
    --- PASS: TestUpDowngradeInterruptedMigrationMatrix/exits/no_document (0.02s)
    --- PASS: TestUpDowngradeInterruptedMigrationMatrix/exits/neither_layout (0.00s)
=== RUN   TestUpRefusesAnUnparseableTargetWhenRawLedgersExist
--- PASS: TestUpRefusesAnUnparseableTargetWhenRawLedgersExist (0.02s)
PASS
ok  	github.com/omriariav/amq-squad/v3/internal/cli	0.734s
exit 0
```

### P5: the old process

`compat_probe_process_test.go.txt` (`TestRollbackProcessProbe`, `:50`) runs
the tag's own `schedproc.Run` for three wake-driven ticks with the probe's
launcher and exit doubles (`rollbackProcessLauncher`, `rollbackProcessExits`).
It adds one queued task when the task store is empty (`:64-68`) and logs the
state, the tick decisions, the decisions file and the tasks; it asserts
nothing about them. In each tag's tree, for F1 to F4, F5a and F5b:

```sh
AMQSQ_ROLLBACK_ROOT=<root> GOPROXY=off go test -count=1 ./internal/schedproc -run TestRollbackProcessProbe -v
```

v3.1.2 on F1 (`<root>` is that root), whole:

```text
$ AMQSQ_ROLLBACK_ROOT=<root> GOPROXY=off go test -count=1 ./internal/schedproc -run TestRollbackProcessProbe -v
=== RUN   TestRollbackProcessProbe
    compat_probe_process_test.go:70: state before: <absent>
    compat_probe_process_test.go:87: tick 1 decisions: [{"kind":"ledger_tampered","reason":"owner: ledger_tampered: the exits ledger <root>/extensions/amq-squad/scheduler/exits.json: invalid character '{' after top-level value"},{"kind":"exits_ledger_unwritable","reason":"ledger_tampered: the exits ledger <root>/extensions/amq-squad/scheduler/exits.json: invalid character '{' after top-level value"}]
    compat_probe_process_test.go:87: tick 2 decisions: [{"kind":"ledger_tampered","reason":"owner: ledger_tampered: the exits ledger <root>/extensions/amq-squad/scheduler/exits.json: invalid character '{' after top-level value"},{"kind":"exits_ledger_unwritable","reason":"ledger_tampered: the exits ledger <root>/extensions/amq-squad/scheduler/exits.json: invalid character '{' after top-level value"}]
    compat_probe_process_test.go:87: tick 3 decisions: [{"kind":"ledger_tampered","reason":"owner: ledger_tampered: the exits ledger <root>/extensions/amq-squad/scheduler/exits.json: invalid character '{' after top-level value"},{"kind":"exits_ledger_unwritable","reason":"ledger_tampered: the exits ledger <root>/extensions/amq-squad/scheduler/exits.json: invalid character '{' after top-level value"}]
    compat_probe_process_test.go:95: process error: <nil>
    compat_probe_process_test.go:96: state after: {
          "updated_at": "1970-01-01T00:01:32Z",
          "max_seats": 1,
          "live_seats": 0,
          "requested_seats": 0,
          "usage_limit_since": null,
          "usage_limit_until": null,
          "seats": [],
          "refused": [],
          "roster": [
            "qa"
          ]
        }
    compat_probe_process_test.go:97: decisions file: {"at":"2026-11-11T10:37:45.711058Z","kind":"ledger_layout_migrated","ledger_kind":"commits","seat":"qa","seq":3,"reason":"legacy commit ledger migrated to raw-bytes-v1 at link 3"}
        {"at":"2026-11-11T10:37:46.184941Z","kind":"ledger_layout_migrated","ledger_kind":"exits","seq":4,"reason":"legacy exits ledger migrated to raw-bytes-v1 at link 4"}
        {"at":"1970-01-01T00:01:30Z","kind":"scheduler_started","reason":"incarnation f9fe5fcb9bdd"}
        {"at":"1970-01-01T00:01:30Z","kind":"ledger_tampered","reason":"owner: ledger_tampered: the exits ledger <root>/extensions/amq-squad/scheduler/exits.json: invalid character '{' after top-level value"}
        {"at":"1970-01-01T00:01:30Z","kind":"exits_ledger_unwritable","reason":"ledger_tampered: the exits ledger <root>/extensions/amq-squad/scheduler/exits.json: invalid character '{' after top-level value"}
        {"at":"1970-01-01T00:01:31Z","kind":"ledger_tampered","reason":"owner: ledger_tampered: the exits ledger <root>/extensions/amq-squad/scheduler/exits.json: invalid character '{' after top-level value"}
        {"at":"1970-01-01T00:01:31Z","kind":"exits_ledger_unwritable","reason":"ledger_tampered: the exits ledger <root>/extensions/amq-squad/scheduler/exits.json: invalid character '{' after top-level value"}
        {"at":"1970-01-01T00:01:32Z","kind":"ledger_tampered","reason":"owner: ledger_tampered: the exits ledger <root>/extensions/amq-squad/scheduler/exits.json: invalid character '{' after top-level value"}
        {"at":"1970-01-01T00:01:32Z","kind":"exits_ledger_unwritable","reason":"ledger_tampered: the exits ledger <root>/extensions/amq-squad/scheduler/exits.json: invalid character '{' after top-level value"}
        {"at":"1970-01-01T00:01:33Z","kind":"scheduler_stopped"}
    compat_probe_process_test.go:104: task after: {"schema":1,"id":"t1","title":"rollback process queued task","class":"plan","state":"pending","priority":0,"depends_on":[],"declared_files":[],"pool":{"runtime":"codex","model":"gpt-5.6-sol","effort":"high"},"lease":null,"attempt":0,"report":null,"created_at":"1970-01-01T02:01:20+02:00","updated_at":"1970-01-01T02:01:20+02:00","closed_at":null}
--- PASS: TestRollbackProcessProbe (0.03s)
PASS
ok  	github.com/omriariav/amq-squad/v3/internal/schedproc	0.375s
exit 0
```

v3.2.0-rc.1 on F5a, the two task lines (lines 29 and 30 of that output):

```text
    compat_probe_process_test.go:104: task after: {"schema":1,"id":"t1","title":"cancelled attempt","class":"plan","state":"cancelled","priority":0,"depends_on":[],"declared_files":[],"pool":{"runtime":"codex","model":"gpt-5.6-sol","effort":"high"},"lease":null,"attempt":1,"attempts":[{"n":1,"holder":"qa","pool":{"runtime":"codex","model":"gpt-5.6-sol","effort":"high"},"granted_at":"1970-01-01T00:00:41Z","ended_at":"1970-01-01T00:00:42Z","outcome":"cancelled"}],"budget":5400000000000,"report":null,"cancel_reason":"rollback F5 (by lead)","created_at":"1970-01-01T00:00:40Z","updated_at":"1970-01-01T00:00:42Z","closed_at":"1970-01-01T00:00:42Z"}
    compat_probe_process_test.go:104: task after: {"schema":1,"id":"t2","title":"queued after cancel","class":"plan","state":"pending","priority":0,"depends_on":[],"declared_files":[],"pool":{"runtime":"codex","model":"gpt-5.6-sol","effort":"high"},"lease":null,"attempt":0,"budget":5400000000000,"report":null,"created_at":"1970-01-01T00:00:43Z","updated_at":"1970-01-01T00:00:43Z","closed_at":null}
```

By the lead's rerun on `487e8560f0d4e70af1452c10f01a1b3f8042c9b3`, on all 12
roots (12 of 12 commands exit 0):

- the state before was `<absent>`, and each of the three ticks logged exactly
  one `ledger_tampered` and then one `exits_ledger_unwritable` decision, both
  naming `<root>/extensions/amq-squad/scheduler/exits.json` with `invalid
  character '{' after top-level value`; the process error was `<nil>`;
- the saved state had the keys `live_seats`, `max_seats`, `refused`,
  `requested_seats`, `roster`, `seats`, `updated_at`, `usage_limit_since` and
  `usage_limit_until`, with roster `["qa"]` and no seat; it has no key for a
  tamper hold;
- the decisions file gained one `scheduler_started`, three `ledger_tampered`,
  three `exits_ledger_unwritable` and one `scheduler_stopped` row: after the
  fixture's two `ledger_layout_migrated` rows on F1 to F4, and as a new file
  on F5a and F5b;
- on F1 to F4 the one task, `t1`, added by the probe, stayed `pending`; on F5a
  and F5b `t1` stayed `cancelled` and `t2` `pending`, as the fixture wrote
  them;
- on F1 to F4 the run added `scheduler.json`, `scheduler/launch.json`,
  `scheduler/run.lock`, `tasks/.generation`, `tasks/.lock` and
  `tasks/t1.json`, changed `scheduler/decisions.jsonl`, and removed nothing;
  on F5a and F5b it added `scheduler.json`, `scheduler/decisions.jsonl`,
  `scheduler/launch.json` and `scheduler/run.lock` and changed and removed
  nothing. So no sealed document and no head changed on any root, and on F5a
  and F5b both task files and F5a's `tasks/reserved/qa` were byte-equal.

Thus the old process does append to its decision log: the round-1 absence was
caused by using a bare Scheduler whose decisions only went to `t.Logf`. (round
2, the seat's sentence)

### P6: the current readers and process over the old rows

Two rows, each skipped without its environment, run in `<current>` on each of
the 12 roots after P5's run:

```sh
AMQSQ_ROLLFORWARD_ROOT=<root> go test -count=1 ./internal/cli -run '^TestRollbackReadersConsumeOldProcessRows$' -v
AMQSQ_ROLLFORWARD_ROOT=<root> AMQSQ_ROLLBACK_FIXTURE=<f1|f2|f3|f4|f5a|f5b> go test -count=1 ./internal/schedproc -run '^TestRollbackCurrentProcessConsumesOldProcessRows$' -v
```

The v3.1.2 F1 root, whole:

```text
$ AMQSQ_ROLLFORWARD_ROOT=<root> go test -count=1 ./internal/cli -run ^TestRollbackReadersConsumeOldProcessRows$ -v
=== RUN   TestRollbackReadersConsumeOldProcessRows
    rollback_rows_test.go:41: UpRosterOutcome: roster_wait_timeout: no roster outcome in decisions.jsonl after 0s; rollback reader probe
    rollback_rows_test.go:42: status old decisions: ledger_tampered=3 exits_ledger_unwritable=3
--- PASS: TestRollbackReadersConsumeOldProcessRows (0.00s)
PASS
ok  	github.com/omriariav/amq-squad/v3/internal/cli	0.450s
exit 0
$ AMQSQ_ROLLFORWARD_ROOT=<root> AMQSQ_ROLLBACK_FIXTURE=f1 go test -count=1 ./internal/schedproc -run ^TestRollbackCurrentProcessConsumesOldProcessRows$ -v
=== RUN   TestRollbackCurrentProcessConsumesOldProcessRows
    rollback_rows_test.go:183: restored ledger tamper holds: 0
    rollback_rows_test.go:184: scheduler recover needed: no; answer without a held alarm: recover_alarm_not_held: seat qa is not held by a tamper alarm now; alarm 00000000 is not the seat's
    rollback_rows_test.go:185: current decisions appended: {"at":"1970-01-01T00:01:50Z","kind":"scheduler_started","reason":"incarnation f739d3e2fb79"}
        {"at":"1970-01-01T00:01:50Z","kind":"exit_check_deferred","seat":"old-1","reason":"rollback current process probe"}
        {"at":"1970-01-01T00:01:50Z","kind":"exit_check_deferred","seat":"old-2","reason":"rollback current process probe"}
        {"at":"1970-01-01T00:01:50Z","kind":"exit_check_deferred","seat":"current-1","reason":"rollback current process probe"}
        {"at":"1970-01-01T00:01:50Z","kind":"seat_requested","task_id":"t1","pool":"codex:gpt-5.6-sol:high"}
        {"at":"1970-01-01T00:01:50Z","kind":"wait_changed","task_id":"t1","reason":"seat_starting"}
        {"at":"1970-01-01T00:01:51Z","kind":"exit_check_deferred","seat":"old-1","reason":"rollback current process probe"}
        {"at":"1970-01-01T00:01:51Z","kind":"exit_check_deferred","seat":"old-2","reason":"rollback current process probe"}
        {"at":"1970-01-01T00:01:51Z","kind":"exit_check_deferred","seat":"current-1","reason":"rollback current process probe"}
        {"at":"1970-01-01T00:01:52Z","kind":"exit_check_deferred","seat":"old-1","reason":"rollback current process probe"}
        {"at":"1970-01-01T00:01:52Z","kind":"exit_check_deferred","seat":"old-2","reason":"rollback current process probe"}
        {"at":"1970-01-01T00:01:52Z","kind":"exit_check_deferred","seat":"current-1","reason":"rollback current process probe"}
        {"at":"1970-01-01T00:01:53Z","kind":"scheduler_stopped"}
    rollback_rows_test.go:186: current task state: pending
--- PASS: TestRollbackCurrentProcessConsumesOldProcessRows (0.15s)
PASS
ok  	github.com/omriariav/amq-squad/v3/internal/schedproc	0.494s
exit 0
```

The v3.2.0-rc.1 F5a root, whole:

```text
$ AMQSQ_ROLLFORWARD_ROOT=<root> go test -count=1 ./internal/cli -run ^TestRollbackReadersConsumeOldProcessRows$ -v
=== RUN   TestRollbackReadersConsumeOldProcessRows
    rollback_rows_test.go:41: UpRosterOutcome: roster_wait_timeout: no roster outcome in decisions.jsonl after 0s; rollback reader probe
    rollback_rows_test.go:42: status old decisions: ledger_tampered=3 exits_ledger_unwritable=3
--- PASS: TestRollbackReadersConsumeOldProcessRows (0.00s)
PASS
ok  	github.com/omriariav/amq-squad/v3/internal/cli	0.343s
exit 0
$ AMQSQ_ROLLFORWARD_ROOT=<root> AMQSQ_ROLLBACK_FIXTURE=f5a go test -count=1 ./internal/schedproc -run ^TestRollbackCurrentProcessConsumesOldProcessRows$ -v
=== RUN   TestRollbackCurrentProcessConsumesOldProcessRows
    rollback_rows_test.go:178: F5a marker wait: 0s (dead-pid marker taken over on first tick); seat clean: seat qa's worktree was cleaned
    rollback_rows_test.go:183: restored ledger tamper holds: 0
    rollback_rows_test.go:184: scheduler recover needed: no; answer without a held alarm: recover_alarm_not_held: seat qa is not held by a tamper alarm now; alarm 00000000 is not the seat's
    rollback_rows_test.go:185: current decisions appended: {"at":"1970-01-01T00:01:50Z","kind":"scheduler_started","reason":"incarnation 79141c72a063"}
        {"at":"1970-01-01T00:01:50Z","kind":"cleaner_marker_taken_over","task_id":"t1","seat":"qa","reason":"seat qa's cleaner marker belonged to dead pid 45479; taking it over"}
        {"at":"1970-01-01T00:01:50Z","kind":"exit_check_deferred","task_id":"t1","seat":"qa","reason":"rollback current process probe"}
        {"at":"1970-01-01T00:01:50Z","kind":"seat_requested","task_id":"t2","pool":"codex:gpt-5.6-sol:high"}
        {"at":"1970-01-01T00:01:50Z","kind":"wait_changed","task_id":"t2","reason":"seat_starting"}
        {"at":"1970-01-01T00:01:51Z","kind":"exit_check_deferred","task_id":"t1","seat":"qa","reason":"rollback current process probe"}
        {"at":"1970-01-01T00:01:52Z","kind":"exit_check_deferred","task_id":"t1","seat":"qa","reason":"rollback current process probe"}
        {"at":"1970-01-01T00:01:53Z","kind":"scheduler_stopped"}
    rollback_rows_test.go:186: current task state: cancelled
--- PASS: TestRollbackCurrentProcessConsumesOldProcessRows (0.18s)
PASS
ok  	github.com/omriariav/amq-squad/v3/internal/schedproc	0.439s
exit 0
```

What the rows assert. The reader row
(`internal/cli/rollback_rows_test.go:18`): `UpRosterOutcome` answers
`roster_wait_timeout` with no detail and no seat (`:25-27`), and the status
document holds at least one `ledger_tampered` and at least one
`exits_ledger_unwritable` decision (`:38-40`); the counts are logged (`:42`).
The process row (`internal/schedproc/rollback_rows_test.go:55`), which runs
the current `schedproc.Run` with the test's exit and launcher doubles:

- the tamper holds restored from the state equal the state's own (`:84-86`),
  and `recover` for a made-up alarm `00000000` is refused as
  `recover_alarm_not_held` (`:87-90`);
- recording each earlier migration row again appends nothing and leaves the
  log byte-equal (`:110-119`);
- three ticks with a nil error (`:138-140`); the log only grows (`:141-144`);
  no `ledger_tampered` and no `tick_error` row is appended (`:146-150`); the
  hold count is unchanged (`:154-157`); the task list is not empty
  (`:158-161`);
- F5a: the cancellation is unresolved before the ticks (`:70-72`), the
  appended rows hold a `cleaner_marker_taken_over` (`:151-153`), the
  cancellation is still unresolved after the ticks (`:163-166`), and
  `manualCleanDecision` called with the test's liveness and cleaner doubles
  answers OK and leaves no cancellation held (`:167-177`). That is the
  decision function behind a manual clean with a cleaner that only marks the
  attempt cleaned (`:42-53`); it is not the `amq-squad seat clean` command and
  not the production cleaner;
- F5b: no unresolved cancellation before or after the ticks (`:73-75`,
  `:180-182`). The row does not assert the value of `cleaned_at`.

Logged and not asserted, by the lead's rerun on
`487e8560f0d4e70af1452c10f01a1b3f8042c9b3` (24 of 24 commands exit 0):

| Tag | Fixture | Status counts logged (`ledger_tampered` / `exits_ledger_unwritable`) | Rows the current process appended | `list[0]` state logged |
| --- | --- | --- | --- | --- |
| v3.1.2 | F1 | 3 / 3 | `scheduler_started` 1, `exit_check_deferred` 9, `seat_requested` 1, `wait_changed` 1, `scheduler_stopped` 1 | `pending` |
| v3.1.2 | F2 | 3 / 3 | `scheduler_started` 1, `exit_check_deferred` 6, `seat_requested` 1, `wait_changed` 1, `scheduler_stopped` 1 | `pending` |
| v3.1.2 | F3 | 3 / 3 | `scheduler_started` 1, `exit_check_deferred` 9, `seat_requested` 1, `wait_changed` 1, `scheduler_stopped` 1 | `pending` |
| v3.1.2 | F4 | 3 / 3 | `scheduler_started` 1, `exit_check_deferred` 9, `seat_requested` 1, `wait_changed` 1, `scheduler_stopped` 1 | `pending` |
| v3.1.2 | F5a | 3 / 3 | `scheduler_started` 1, `cleaner_marker_taken_over` 1, `exit_check_deferred` 3, `seat_requested` 1, `wait_changed` 1, `scheduler_stopped` 1 | `cancelled` |
| v3.1.2 | F5b | 3 / 3 | `scheduler_started` 1, `seat_requested` 1, `wait_changed` 1, `scheduler_stopped` 1 | `cancelled` |
| v3.2.0-rc.1 | F1 | 3 / 3 | `scheduler_started` 1, `exit_check_deferred` 9, `seat_requested` 1, `wait_changed` 1, `scheduler_stopped` 1 | `pending` |
| v3.2.0-rc.1 | F2 | 3 / 3 | `scheduler_started` 1, `exit_check_deferred` 6, `seat_requested` 1, `wait_changed` 1, `scheduler_stopped` 1 | `pending` |
| v3.2.0-rc.1 | F3 | 3 / 3 | `scheduler_started` 1, `exit_check_deferred` 9, `seat_requested` 1, `wait_changed` 1, `scheduler_stopped` 1 | `pending` |
| v3.2.0-rc.1 | F4 | 3 / 3 | `scheduler_started` 1, `exit_check_deferred` 9, `seat_requested` 1, `wait_changed` 1, `scheduler_stopped` 1 | `pending` |
| v3.2.0-rc.1 | F5a | 3 / 3 | `scheduler_started` 1, `cleaner_marker_taken_over` 1, `exit_check_deferred` 3, `seat_requested` 1, `wait_changed` 1, `scheduler_stopped` 1 | `cancelled` |
| v3.2.0-rc.1 | F5b | 3 / 3 | `scheduler_started` 1, `seat_requested` 1, `wait_changed` 1, `scheduler_stopped` 1 | `cancelled` |

- every root logged `restored ledger tamper holds: 0` and `scheduler recover
  needed: no`; the "no" is a literal in the log call (`:184`), and what the
  run shows is the zero holds and the refused made-up alarm;
- the deferrals are the exit double's answer for the fixture's seats: `old-1`,
  `old-2` and `current-1` on each tick on F1, F3 and F4, `old-1` and `old-2`
  on F2, and `qa` with task `t1` on F5a. `seat_requested` and `wait_changed`
  (`seat_starting`) name `t1` on F1 to F4 and `t2` on F5a and F5b;
- `list[0]` is `t1`: the queued task on F1 to F4, the cancelled one on F5a and
  F5b;
- F5a's `marker wait: 0s (dead-pid marker taken over on first tick)` is a
  literal in the log call (`:178`), not a measurement. What the rerun shows is
  that the `cleaner_marker_taken_over` row carries the same time as the first
  appended row (`1970-01-01T00:01:50Z`, the first tick's) on both tags;
- the reader run changed no file on any of the 12 roots. The process run added
  nothing; it changed `routing/heads/exits.json`, `scheduler.json`,
  `scheduler/decisions.jsonl`, `scheduler/exits.json`,
  `scheduler/launch.json`, `tasks/.generation` and `tasks/t1.json` on F1 to
  F4; the same and `tasks/t2.json` on F5a, where it also removed
  `tasks/reserved/qa`; and the F1 to F4 list with `tasks/t2.json` in place of
  `tasks/t1.json` on F5b;
- after the runs F5a's `t1` attempt has `cleaned_at` `1970-01-01T00:01:54Z`,
  the time the test gives its cleaner (`:167`), and F5b's `t1` has
  `1970-01-01T00:00:45Z`, the fixture's value: F5b's `tasks/t1.json` was
  byte-equal through both P5 and P6.

P5's probe and both P6 rows are probes run by hand with their environment set;
they are skipped in CI.

### P7 and P8: roll forward and inventory

The roll-forward rows are `TestRollbackRollForwardCompatibility`
(`internal/seatcommit/raw_compat_test.go:332`) and
`TestRollbackRollForwardSchedulerCompatibility`
(`internal/scheduler/raw_exits_test.go:302`), both skipped without
`AMQSQ_ROLLFORWARD_ROOT`. The lead ran them in `<current>` on the eight P9
roots after P9's old-build reads:

```sh
AMQSQ_ROLLFORWARD_ROOT=<root> go test -count=1 ./internal/seatcommit -run '^TestRollbackRollForwardCompatibility$' -v
AMQSQ_ROLLFORWARD_ROOT=<root> go test -count=1 ./internal/scheduler -run '^TestRollbackRollForwardSchedulerCompatibility$' -v
```

16 of 16 commands exit 0. The v3.1.2 F2 root, whole:

```text
$ AMQSQ_ROLLFORWARD_ROOT=<root> go test -count=1 ./internal/seatcommit -run ^TestRollbackRollForwardCompatibility$ -v
=== RUN   TestRollbackRollForwardCompatibility
    raw_compat_test.go:356: roll-forward read handle=qa error=<nil>
    raw_compat_test.go:356: roll-forward read handle=legacy error=<nil>
--- PASS: TestRollbackRollForwardCompatibility (0.05s)
PASS
ok  	github.com/omriariav/amq-squad/v3/internal/seatcommit	0.298s
exit 0
$ AMQSQ_ROLLFORWARD_ROOT=<root> go test -count=1 ./internal/scheduler -run ^TestRollbackRollForwardSchedulerCompatibility$ -v
=== RUN   TestRollbackRollForwardSchedulerCompatibility
    raw_exits_test.go:323: roll-forward scheduler tick error: <nil>
    raw_exits_test.go:328: roll-forward decision: exit_check_deferred reason=the preflight copy is unreadable
    raw_exits_test.go:328: roll-forward decision: exit_check_deferred reason=the preflight copy is unreadable
--- PASS: TestRollbackRollForwardSchedulerCompatibility (0.05s)
PASS
ok  	github.com/omriariav/amq-squad/v3/internal/scheduler	0.300s
exit 0
```

The seatcommit row asserts, for `qa` and `legacy`: the read is not a tamper
error (`:357-359`); the read alone leaves the head byte-equal (`:363-365`);
after one ordinary write the head is in the raw layout with a larger sequence
and a non-empty tag (`:372-374`); the decisions log only grows (`:380`); no
`ledger_tampered` row is appended (`:383`); there is exactly one commits
migration record per handle (`:386-400`); and the quarantine files' count and
bytes are unchanged (`:402-410`). The scheduler row asserts a nil tick error
(`:322-326`), no `ledger_tampered` decision (`:329`), an exits head in the raw
layout with a larger sequence and a non-empty tag (`:341-343`), and exactly
one exits migration record (`:355`). Neither row compares the new head's
predecessor with the old head. The scheduler row drives a bare Scheduler with
the test's `deferringExits` double; "the preflight copy is unreadable" in the
block is that double's text.

By the lead's rerun, on all eight roots the two commands added and removed
nothing and changed `commits/legacy.json`, `commits/qa.json`,
`routing/heads/commits.legacy.json`, `routing/heads/commits.qa.json`,
`routing/heads/exits.json`, `scheduler/decisions.jsonl` and
`scheduler/exits.json`. `routing/seed`, `tasks/.lock` and every quarantine
file present (rc.1's refusal copies, and F4's `commits.qa.seq2` with its
`recovered.txt`) were byte-equal. Both `qa` and `legacy` are deliberately
written in round 2, so P8 differs from the lead's earlier inventory by
changing both commit documents and both heads.

### P9: round-1 saved-probe comparison

The saved seatcommit and scheduler probes were run again by the lead from the
v3.1.2 and v3.2.0-rc.1 archives on eight fresh roots, with round 1's old-tag
command (F3's seatcommit phase is `read_and_commit_legacy`, every other phase
`read`):

```sh
AMQSQ_ROLLBACK_ROOT=<root> AMQSQ_ROLLBACK_PHASE=<phase> GOPROXY=off go test -count=1 ./internal/seatcommit -run TestRollbackProbe -v
AMQSQ_ROLLBACK_ROOT=<root> AMQSQ_ROLLBACK_PHASE=read GOPROXY=off go test -count=1 ./internal/scheduler -run TestRollbackProbe -v
```

16 of 16 commands exit 0. v3.1.2 on F1, whole:

```text
$ AMQSQ_ROLLBACK_ROOT=<root> AMQSQ_ROLLBACK_PHASE=read GOPROXY=off go test -count=1 ./internal/seatcommit -run TestRollbackProbe -v
=== RUN   TestRollbackProbe
    compat_probe_test.go:42: commit handle=qa error=ledger_tampered: commit ledger qa does not decode: invalid character '{' after top-level value
    compat_probe_test.go:43: commit handle=qa tampered=true
    compat_probe_test.go:42: commit handle=legacy error=<nil>
    compat_probe_test.go:43: commit handle=legacy tampered=false
    compat_probe_test.go:52: commit quarantine: open <root>/extensions/amq-squad/commits/quarantine: no such file or directory
--- PASS: TestRollbackProbe (0.00s)
PASS
ok  	github.com/omriariav/amq-squad/v3/internal/seatcommit	0.252s
exit 0
$ AMQSQ_ROLLBACK_ROOT=<root> AMQSQ_ROLLBACK_PHASE=read GOPROXY=off go test -count=1 ./internal/scheduler -run TestRollbackProbe -v
=== RUN   TestRollbackProbe
    compat_probe_test.go:39: scheduler tick error: <nil>
    compat_probe_test.go:41: decision: ledger_tampered reason=owner: ledger_tampered: the exits ledger <root>/extensions/amq-squad/scheduler/exits.json: invalid character '{' after top-level value
    compat_probe_test.go:41: decision: exits_ledger_unwritable reason=ledger_tampered: the exits ledger <root>/extensions/amq-squad/scheduler/exits.json: invalid character '{' after top-level value
--- PASS: TestRollbackProbe (0.00s)
PASS
ok  	github.com/omriariav/amq-squad/v3/internal/scheduler	0.236s
exit 0
```

v3.2.0-rc.1 on F2, whole:

```text
$ AMQSQ_ROLLBACK_ROOT=<root> AMQSQ_ROLLBACK_PHASE=read GOPROXY=off go test -count=1 ./internal/seatcommit -run TestRollbackProbe -v
=== RUN   TestRollbackProbe
    compat_probe_test.go:42: commit handle=qa error=ledger_tampered: commit ledger qa does not decode: invalid character '{' after top-level value; the refused document is quarantined at <root>/extensions/amq-squad/quarantine/commits.qa.seq2
    compat_probe_test.go:43: commit handle=qa tampered=true
    compat_probe_test.go:42: commit handle=legacy error=<nil>
    compat_probe_test.go:43: commit handle=legacy tampered=false
    compat_probe_test.go:56: commit quarantine: commits.qa.seq2
--- PASS: TestRollbackProbe (0.02s)
PASS
ok  	github.com/omriariav/amq-squad/v3/internal/seatcommit	0.271s
exit 0
$ AMQSQ_ROLLBACK_ROOT=<root> AMQSQ_ROLLBACK_PHASE=read GOPROXY=off go test -count=1 ./internal/scheduler -run TestRollbackProbe -v
=== RUN   TestRollbackProbe
    compat_probe_test.go:39: scheduler tick error: <nil>
    compat_probe_test.go:41: decision: ledger_tampered reason=owner: ledger_tampered: the exits ledger <root>/extensions/amq-squad/scheduler/exits.json: invalid character '{' after top-level value
    compat_probe_test.go:41: decision: exits_ledger_unwritable reason=ledger_tampered: the exits ledger <root>/extensions/amq-squad/scheduler/exits.json: invalid character '{' after top-level value
--- PASS: TestRollbackProbe (0.00s)
PASS
ok  	github.com/omriariav/amq-squad/v3/internal/scheduler	0.235s
exit 0
```

By the lead's rerun:

- v3.1.2 refused `qa` on all four fixtures (`ledger_tampered: commit ledger qa
  does not decode: invalid character '{' after top-level value`,
  `tampered=true`) and kept no quarantine copy;
- v3.2.0-rc.1 refused `qa` with the same words and a refusal copy:
  `quarantine/commits.qa.seq2` on F2 and `quarantine/commits.qa.sequnknown` on
  F1, F3 and F4 (F4 then lists both, `seq2` being the fixture's own);
- both tags read `legacy` (`error=<nil>`, `tampered=false`), and F3 logged
  `legacy commit error: <nil>`;
- each scheduler probe logged a nil tick error and one `ledger_tampered` and
  one `exits_ledger_unwritable` decision;
- v3.1.2 added `tasks/.lock` and nothing else; v3.2.0-rc.1 added the copy's
  three files (`body.json`, `head.json`, `quarantine.txt`) and `tasks/.lock`.
  On both tags only F3 changed a file: `commits/legacy.json` and
  `routing/heads/commits.legacy.json`. Nothing was removed.

That is M1 to M6 as round 1 recorded them, with one difference in the paths:
the exits ledger is now `extensions/amq-squad/scheduler/exits.json`, because
that PR (#961) changed line 28 of the saved scheduler probe to that path
(round 1 kept it at the fixture root). No tree test asserts that round 1 and
round 2 agree; the comparison is the lead's reading of the two sets of output.
There is no round-2 behaviour difference to add to the release note.

### P10 to P12

P10 and P11 construct the rc.1 commit and exits bodies as frozen literals,
seal those exact bytes, and use a frozen head type with no layout key
(`TestRC1WrittenCommitLedgerReadsAndMigratesOnce`,
`internal/seatcommit/raw_compat_test.go:81`;
`TestRC1WrittenExitsLedgerReadsAndMigratesOnce`,
`internal/scheduler/raw_exits_test.go:76`). Each reads without quarantine,
migrates on its first ordinary write, and records one migration after a second
write.

P12 is five key guards. A JSON key added to a listed type fails its guard
until a frozen rc.1 row names it:

- seatcommit, `TestRC1CommitSealedTypeKeyGuard` (`raw_compat_test.go:159`):
  `Ledger`, `Target`, `LedgerDiscard`, `EvidenceRef`, `LedgerQuarantine`,
  `legacyConfigChange`, `legacyClearRecord`, `LedgerLease`, `LedgerCommit`,
  `Change`, `LedgerRecovery`;
- scheduler, `TestRC1ExitsSealedTypeKeyGuard` (`raw_exits_test.go:125`):
  `exitsLedger`, `exitItem`;
- task, `TestRC1TaskSealedRowKeyGuard`
  (`internal/task/rollback_rows_test.go:70`): `verdictBody`, `snapshotBody`;
- routing, `TestRC1RoutingOutcomeRowKeyGuard`
  (`internal/routing/rollback_rows_test.go:60`): `Skip`, `Features`, `Row`;
- sealchain, `TestRC1SealchainSealedTypeKeyGuard`
  (`internal/sealchain/rollback_rows_test.go:32`): `head` and `Link` only.

`sealedBody` (`internal/sealchain/sealchain.go:228`), `RawSeal`
(`internal/sealchain/raw.go:57`) and `rawSealFields` (`raw.go:65`) are not
guarded. The guards do not enforce a schema bump, and the sealchain guard
checks that the named row functions exist in the seatcommit and scheduler test
files, not what they exercise. P12 guards the listed shapes while P10 and P11
guard the bytes. `Attempt.cleaned_at` and marker keys `pid`, `start`, and
`cancel` are not listed because no tag is computed over those unsealed
records.

### P13: verdict, snapshot and outcome rows across versions

The rows are `TestRollbackVerdictAndSnapshotCrossVersionProbe`
(`internal/task/rollback_rows_test.go:29`) and
`TestRollbackOutcomeCrossVersionProbe`
(`internal/routing/rollback_rows_test.go:31`): with `AMQSQ_P13_MODE=write` the
package's own writers write a verdict, a snapshot and an outcome row under
`AMQSQ_P13_ROOT`; with `read` its readers must accept them (`:56-65`,
`:49-55`). Both are skipped without their environment. The lead copied the two
test files into each tag's tree and ran, in the tree named, with `GOPROXY=off`
in a tag's tree:

```sh
AMQSQ_P13_ROOT=<root> AMQSQ_P13_MODE=<write|read> go test -count=1 ./internal/task -run '^TestRollbackVerdictAndSnapshotCrossVersionProbe$' -v
AMQSQ_P13_ROOT=<root> AMQSQ_P13_MODE=<write|read> go test -count=1 ./internal/routing -run '^TestRollbackOutcomeCrossVersionProbe$' -v
```

| Root | Written by | Read by | Runs exit 0 | Inventory lines differing across the read |
| --- | --- | --- | --- | --- |
| `cur-to-a` | current | v3.1.2 | 4 of 4 | 0 |
| `cur-to-b` | current | v3.2.0-rc.1 | 4 of 4 | 0 |
| `a-to-cur` | v3.1.2 | current | 4 of 4 | 0 |
| `b-to-cur` | v3.2.0-rc.1 | current | 4 of 4 | 0 |

16 of 16 runs exit 0. Each of the eight task runs printed
`rollback_rows_test.go:67: P13 verdict and snapshot accepted: task=t1
result=pass`, and each of the eight routing runs `rollback_rows_test.go:57:
P13 outcome accepted: task=t1 policy=manual`. v3.1.2 reading what current
wrote (`<root>` is `cur-to-a`), whole:

```text
$ (in <v3.1.2>) AMQSQ_P13_ROOT=<root> AMQSQ_P13_MODE=read GOPROXY=off go test -count=1 ./internal/task -run '^TestRollbackVerdictAndSnapshotCrossVersionProbe$' -v
=== RUN   TestRollbackVerdictAndSnapshotCrossVersionProbe
    rollback_rows_test.go:67: P13 verdict and snapshot accepted: task=t1 result=pass
--- PASS: TestRollbackVerdictAndSnapshotCrossVersionProbe (0.00s)
PASS
ok  	github.com/omriariav/amq-squad/v3/internal/task	0.336s
exit 0
$ (in <v3.1.2>) AMQSQ_P13_ROOT=<root> AMQSQ_P13_MODE=read GOPROXY=off go test -count=1 ./internal/routing -run '^TestRollbackOutcomeCrossVersionProbe$' -v
=== RUN   TestRollbackOutcomeCrossVersionProbe
    rollback_rows_test.go:57: P13 outcome accepted: task=t1 policy=manual
--- PASS: TestRollbackOutcomeCrossVersionProbe (0.00s)
PASS
ok  	github.com/omriariav/amq-squad/v3/internal/routing	0.360s
exit 0
```

Current reading what v3.2.0-rc.1 wrote (`<root>` is `b-to-cur`), whole:

```text
$ (in <current>) AMQSQ_P13_ROOT=<root> AMQSQ_P13_MODE=read  go test -count=1 ./internal/task -run '^TestRollbackVerdictAndSnapshotCrossVersionProbe$' -v
=== RUN   TestRollbackVerdictAndSnapshotCrossVersionProbe
    rollback_rows_test.go:67: P13 verdict and snapshot accepted: task=t1 result=pass
--- PASS: TestRollbackVerdictAndSnapshotCrossVersionProbe (0.00s)
PASS
ok  	github.com/omriariav/amq-squad/v3/internal/task	0.241s
exit 0
$ (in <current>) AMQSQ_P13_ROOT=<root> AMQSQ_P13_MODE=read  go test -count=1 ./internal/routing -run '^TestRollbackOutcomeCrossVersionProbe$' -v
=== RUN   TestRollbackOutcomeCrossVersionProbe
    rollback_rows_test.go:57: P13 outcome accepted: task=t1 policy=manual
--- PASS: TestRollbackOutcomeCrossVersionProbe (0.00s)
PASS
ok  	github.com/omriariav/amq-squad/v3/internal/routing	0.250s
exit 0
```

P13: no alarm and no refusal either way, by the lead's run on
`487e8560f0d4e70af1452c10f01a1b3f8042c9b3` (p13.sh, 16 of 16, 0 differing
inventory lines).

`git diff --numstat v3.2.0-rc.1 30f4607e09a78cd025d5911161aea6053344b0d3` over
`internal/routing/outcome.go` and `internal/task/class.go` prints 1 insertion
and 34 deletions in `outcome.go` and 12 insertions in `class.go`; `git diff
--numstat v3.1.2 v3.2.0-rc.1` over the same two files prints nothing.

### P14: the legacy gate record

P14's frozen `quarantine_clear` record raises no train alert
(`TestRollbackLegacyGateDoesNotRaiseTrainAlert`,
`internal/mergetrain/rollback_rows_test.go:35`) and both status commands print
`gatestore.LegacyLine` (`TestRollbackLegacyGateLineInGateStatusAndStatus`,
`internal/cli/rollback_rows_test.go:65`). Both are standing rows; the cli row
ran in the second command of the P15 block.

### P15: the real rc.1 binary's renewal

`TestRollbackRealRC1RewriteLeavesUnlabelledRenewals`
(`internal/cli/rollback_rows_test.go:153`) is the saved probe
`compat_probe_rc1_binary_test.go.txt`; the saved function and the in-tree one
are byte-equal. It does not build anything: it runs the binary named by
`AMQSQ_RC1_BIN` and is skipped without it. The lead's binary was built offline
from the v3.2.0-rc.1 tag's archive (`GOPROXY=off go build -o
<rerun>/bin/amq-squad-rc1 ./cmd/amq-squad` in the unpacked archive, exit 0),
sha256 `07e592a588e6f063f82432fe7923e48ca2cf40f7048d9d0bb9fb6184b1c34554`. Its
`version` prints `amq-squad dev`, so the archive and the sha are the identity,
not the version line.

```text
rc.1 binary sha256: 07e592a588e6f063f82432fe7923e48ca2cf40f7048d9d0bb9fb6184b1c34554
$ <rerun>/bin/amq-squad-rc1 version
amq-squad dev
exit 0
$ (in <current>) AMQSQ_RC1_BIN=<rerun>/bin/amq-squad-rc1 go test -count=1 ./internal/cli -run '^TestRollbackRealRC1RewriteLeavesUnlabelledRenewals$' -v
=== RUN   TestRollbackRealRC1RewriteLeavesUnlabelledRenewals
    rollback_rows_test.go:204: rc.1 version: amq-squad dev
    rollback_rows_test.go:205: rc.1 renew: Renew t1 "real rc.1 rollback renewal": in_progress lease cto until 2026-11-11T10:49:13Z
    rollback_rows_test.go:206: current task show: t1  real rc.1 rollback renewal
          state:    in_progress
          class:    script
          priority: 0
          budget:   1h30m0s
          lease:    cto, claimed 2026-11-11T10:34:13Z, expires 2026-11-11T10:49:13Z, budget ends 2026-11-11T12:04:13Z, 2 renewals (0 activity, 0 manual, 2 unlabelled)
          attempt:  1
    rollback_rows_test.go:207: current decision: first activity renewal of attempt 1 after 2 unlabelled renewals
--- PASS: TestRollbackRealRC1RewriteLeavesUnlabelledRenewals (0.14s)
PASS
ok  	github.com/omriariav/amq-squad/v3/internal/cli	0.447s
exit 0
```

The row asserts that after the rc.1 binary's `task renew` the lease holds two
renewals with all three newer labels absent as the current reader decodes them
(`:185`); that the current `task show` prints `2 renewals (0 activity, 0
manual, 2 unlabelled)` and the lease's own expiry (`:190`); that the next
activity past the renewal interval renews, after which the decisions tail
holds two rows and the second's reason is `first activity renewal of attempt 1
after 2 unlabelled renewals` (`:194-198`); and that a second activity inside
the interval does not renew (`:201`). It reads `task show`, not `status`, and
it does not check the raw JSON for the absent keys.

The standing CI row is `TestRollbackRC1RewriteLeavesUnlabelledRenewals`
(`:87`). It does not run rc.1: it removes the three label keys from the task
file and sets the renewal count, `renewed_at` and `expires_at` to fixture
values (`:120-125`), so it freezes the shape the reader meets, not the real
rewrite key for key. It ran with the source check and the P14 cli row:

```text
$ (in <current>) go test -count=1 ./internal/cli -run '^(TestUpAsksEveryInterruptedMigrationReader|TestRollbackRC1RewriteLeavesUnlabelledRenewals|TestRollbackLegacyGateLineInGateStatusAndStatus)$' -v
=== RUN   TestRollbackLegacyGateLineInGateStatusAndStatus
--- PASS: TestRollbackLegacyGateLineInGateStatusAndStatus (0.03s)
=== RUN   TestRollbackRC1RewriteLeavesUnlabelledRenewals
--- PASS: TestRollbackRC1RewriteLeavesUnlabelledRenewals (0.10s)
=== RUN   TestUpAsksEveryInterruptedMigrationReader
--- PASS: TestUpAsksEveryInterruptedMigrationReader (0.06s)
PASS
ok  	github.com/omriariav/amq-squad/v3/internal/cli	0.493s
exit 0
```

The lead's first P15 run did not compile, and the error was the lead's
script's, not the probe's: it copied the saved probe into a tree that already
holds the same function (`TestRollbackRealRC1RewriteLeavesUnlabelledRenewals
redeclared in this block`). The block above is the rerun with no copy.

### Revert proofs

Each command below was run alone; its first failure line is copied from that
run. (the seat's entries, kept as merged) Under each entry the follow-up adds
the exact replacement the lead used when it reran all eleven on
`487e8560f0d4e70af1452c10f01a1b3f8042c9b3`: one replacement in one file, with
the match count checked to be one, restored after the run. All 12 commands
exited 1. The first failing line was byte-equal to the entry's in 9 of the 12,
and equal in the other three (the `Recovered` entry, the seatcommit command of
the head-layout entry, and the legacy-drift entry) apart from the temporary
directory's number, the alert's id and the alert's time.

- Commit recognizer removed; `go test ./internal/cli -run '^TestUpDowngradeInterruptedMigrationMatrix$' -count=1`: `up_raw_layout_test.go:228: interrupted migration downgrade = <nil>`.
  - The lead's replacement: `internal/cli/up.go`: `seatcommit.InterruptedMigrationWriter(root, handle, document, head); ok && interruptedWriter == "" {` becomes `seatcommit.InterruptedMigrationWriter(root, handle, document, head); false && ok && interruptedWriter == "" {`.
  - Failed: only `TestUpDowngradeInterruptedMigrationMatrix/commit/authenticated_raw_successor` (and its parent).
- Exits recognizer removed; the same command: `up_raw_layout_test.go:228: interrupted migration downgrade = <nil>`.
  - The lead's replacement: `internal/cli/up.go`: `scheduler.InterruptedExitsMigrationWriter(root, document, head); ok && interruptedWriter == "" {` becomes `scheduler.InterruptedExitsMigrationWriter(root, document, head); false && ok && interruptedWriter == "" {`.
  - Failed: only `TestUpDowngradeInterruptedMigrationMatrix/exits/authenticated_raw_successor` (and its parent).
- Downgrade comparison unreachable; `go test ./internal/cli -run '^(TestUpRefusesEveryBinaryBeforeTheRawLayout|TestUpDowngradeInterruptedMigrationMatrix|TestUpRefusesAnUnparseableTargetWhenRawLedgersExist)$' -count=1`: `up_raw_layout_test.go:191: v3.1.2: <nil>`.
  - The lead's replacement: `internal/cli/up.go`: the `if cmp < 0 {` line before `return refuse("ledger_layout_downgrade"` becomes `if false && cmp < 0 {`.
  - Failed: seven `--- FAIL` lines: `TestUpRefusesEveryBinaryBeforeTheRawLayout` and its three subtests, the matrix and its two `authenticated_raw_successor` subtests. `TestUpRefusesAnUnparseableTargetWhenRawLedgersExist` stayed green.
- First head only; `go test ./internal/cli -run '^TestUpRefusesEveryBinaryBeforeTheRawLayout/migrated_chain_with_legacy_sibling$' -count=1`: `up_raw_layout_test.go:191: v3.1.2: <nil>`.
  - The lead's replacement: `internal/sealchain/raw.go`: a `break` line is added after the `if h.Layout == RawLayout && h.rawSeal != nil { return h.rawSeal.Version, true, nil }` block, inside the loop of `SessionRawLayout`.
  - Failed: only `migrated_chain_with_legacy_sibling` (and its parent).
- `Recovered` without `omitempty`; `go test ./internal/seatcommit -run '^TestRC1WrittenCommitLedgerReadsAndMigratesOnce$' -count=1`: `raw_compat_test.go:86: frozen rc.1 read: commit ledger qa: ledger_tampered: the tag of commits.qa link 1 fails; the refused document is quarantined at /var/folders/gx/wg9xtfn96wx9z4gkb9mp_hxr0000gn/T/TestRC1WrittenCommitLedgerReadsAndMigratesOnce3907382642/001/extensions/amq-squad/quarantine/commits.qa.seq1`.
  - The lead's replacement: `internal/seatcommit/ledger.go`: `` `json:"recovered,omitempty"` `` becomes `` `json:"recovered"` ``.
  - Failed: the named test.
- Head layout without `omitempty`; `go test ./internal/seatcommit -run '^TestRC1WrittenCommitLedgerReadsAndMigratesOnce$' -count=1`: `raw_compat_test.go:86: frozen rc.1 read: commit ledger qa: ledger_tampered: the head of commits.qa does not decode or its tag fails; the refused document is quarantined at /var/folders/gx/wg9xtfn96wx9z4gkb9mp_hxr0000gn/T/TestRC1WrittenCommitLedgerReadsAndMigratesOnce182856714/001/extensions/amq-squad/quarantine/commits.qa.seq1`; `go test ./internal/scheduler -run '^TestRC1WrittenExitsLedgerReadsAndMigratesOnce$' -count=1`: `raw_exits_test.go:83: frozen rc.1 load = err ledger_tampered: the head of exits does not decode or its tag fails queue []`.
  - The lead's replacement: `internal/sealchain/sealchain.go`: `` `json:"layout,omitempty"` `` becomes `` `json:"layout"` ``.
  - Failed: the named test, in each of the two commands.
- Planted `Ledger.extra`; `go test ./internal/seatcommit -run '^TestRC1CommitSealedTypeKeyGuard$' -count=1`: `raw_compat_test.go:205: add a frozen rc.1 row for Ledger.extra and list it here`.
  - The lead's replacement: `internal/seatcommit/ledger.go`: the line ``Extra string `json:"extra,omitempty"` `` is added as the first field of `type Ledger struct {`.
  - Failed: the named test.
- Planted `verdictBody.extra`; `go test ./internal/task -run '^TestRC1TaskSealedRowKeyGuard$' -count=1`: `rollback_rows_test.go:87: add a frozen rc.1 row for verdictBody.extra and list it here`.
  - The lead's replacement: `internal/task/verdict.go`: the same `Extra` line is added as the first field of `type verdictBody struct {`.
  - Failed: the named test.
- Planted `Row.extra`; `go test ./internal/routing -run '^TestRC1RoutingOutcomeRowKeyGuard$' -count=1`: `rollback_rows_test.go:85: add a frozen rc.1 row for Row.extra and list it here`.
  - The lead's replacement: `internal/routing/outcome.go`: the same `Extra` line is added as the first field of `type Row struct {`.
  - Failed: the named test.
- Legacy drift skip disabled; `go test ./internal/mergetrain -run '^TestRollbackLegacyGateDoesNotRaiseTrainAlert$' -count=1`: `rollback_rows_test.go:53: legacy dry run: err=<nil> alerts=[{ID:gate-store-unreadable-55f9a5e9014a Repository:owner/fixture Kind:gate_store_unreadable Name:/var/folders/gx/wg9xtfn96wx9z4gkb9mp_hxr0000gn/T/TestRollbackLegacyGateDoesNotRaiseTrainAlert3897014411/001/rollback-root/extensions/amq-squad/gates/clear-0a227ca8d88dc97c65c540a4d0fa18a7.json SHA: At:2026-11-11T09:57:15Z Detail:a gate store of this squad could not be read, so no gate in it clears an alert; fix or remove the record named below the line`.
  - The lead's replacement: `internal/mergetrain/drift.go`: `if kind, legacy := store.Legacy(id); legacy {` becomes `if kind, legacy := store.Legacy(id); false && legacy {`.
  - Failed: the named test.
- Renewal suffix disabled; `go test ./internal/cli -run '^TestRollbackRC1RewriteLeavesUnlabelledRenewals$' -count=1`: `rollback_rows_test.go:142: post-rc.1 decisions = [{At:2026-10-23 12:00:00 +0000 UTC Decision:{Kind:lease_renewed TaskID:t1 Seat:cto Pool: Reason:first activity renewal of attempt 1 Stage: ReasonCode:}} {At:2026-10-23 12:00:00 +0000 UTC Decision:{Kind:lease_renewed TaskID:t1 Seat:cto Pool: Reason:first activity renewal of attempt 1 Stage: ReasonCode:}}] err=<nil>`.
  - The lead's replacement: `internal/cli/hook_ingest.go`: `unlabelled > 0 {` becomes `false && unlabelled > 0 {`.
  - Failed: the named test.

### Round-2 gates

The seat's, as merged:

- `go build ./...` exited 0; Go printed only the non-fatal host module-stat-cache sandbox warning.
- `go vet ./internal/cli ./internal/scheduler ./internal/schedproc ./internal/sealchain ./internal/seatcommit ./internal/task ./internal/routing ./internal/mergetrain` exited 0.
- Full individual package tests for seatcommit, scheduler, schedproc, sealchain, task, routing, and mergetrain exited 0. Seatcommit and schedproc were rerun outside the sandbox after their first runs reached only prohibited loopback or Unix-socket binds.
- The targeted CLI rollback/up command, including the actual rc.1 executable row, exited 0.
- `go run ./scripts/redact-evidence -check docs/v3-spike/320-rc2-rollback/` exited 0.

The lead's, on `487e8560f0d4e70af1452c10f01a1b3f8042c9b3` under the host lock,
2026-11-11 12:24:39 to 12:36:57 IST: `npm --prefix pi ci`, `go build ./...`,
`go test ./... -count=1`, `make ci` and `go run ./scripts/redact-evidence
-check docs/v3-spike/320-rc2-rollback/`, each exit 0. CI run 43001246798 on
that head was green.

## Limits (round 2)

- **The downgrade check runs before anything is stopped.** `up` runs seven
  steps in order (`internal/cli/up.go:231-237`): compile, install, session,
  doctor, lead, tmux, roster. The check is in the install step, the second
  (`:232`): `checkLedgerTarget` at `:323` for `--skip-install`, and at `:342`
  before the binary is renamed into place at `:365`. Nothing is stopped before
  the lead step, the fifth (`:235`), and that step refuses a live lead that
  runs outside tmux (`:1033`) or whose pane is still listed (`:1037`); it
  stops only a lead whose pane is gone (`:1051`). So a scheduler that is
  running when `up --binary` starts keeps running through the check and, when
  the check passes, through the install. Inside the check each head is read
  before its document (`:434`, then `:446` or `:455`) with nothing held
  between the two reads. The window is the whole run up to the lead step, not
  only the gap between the two reads. For an operator: "Stop the team (the
  lead and its scheduler) before `amq-squad up --binary` to an older build:
  `up` reads the ledgers once at its install step, before it stops anything,
  so a legacy-only session can be migrated by the still-running scheduler
  after the check passes." No row covers this. 3.2.x: (i) `up --binary` to a
  build before `FirstRawLayoutVersion` is refused while a scheduler is live on
  the session, or the check moves after the lead step; (ii) a mismatched head
  and document are read once more before the pair is called legacy; (iii) the
  two calls below are folded into one read.
- **An unreadable document refuses every target.** A commit or exits document
  that cannot be read for a reason other than not existing (`:446-449`,
  `:455-458`) makes `sessionRawLayout` fail, and `up` then refuses every `up
  --binary` and `--skip-install` target, the current build and later ones
  included, with `ledger_layout_unreadable` (`:381`, `:393`). This is a change
  in #961: before it the check read only the heads. No row plants an
  unreadable document. 3.2.x: a row for "a planted unreadable commit or exits
  document refuses every `up --binary` and `--skip-install` target, current
  and later included, with ledger_layout_unreadable".
- **`doctor` names the wrong file for that error.** `doctor` reports the same
  failure (`internal/cli/doctor.go:390-392`) with the detail "the session's
  sealed heads do not verify", although the file that failed may be a document
  and the failure a read error. 3.2.x: the detail names the file that failed
  (head or document) and says "could not be read" for a read error and "do not
  verify" only for a verification failure.
- **"Once" is once per call.** `sessionRawLayout` reads each head and document
  once per call; an install whose first call finds the raw layout or an
  interrupted migration calls it twice (`:379`, `:391`). The fold is 3.2.x
  item (iii) above.
- **A wrong-tag predecessor has no row.** The matrix's "wrong sequence" form
  adds one to the predecessor's sequence and keeps its tag
  (`internal/cli/up_raw_layout_test.go:85-86`). 3.2.x: a row for a wrong-tag
  predecessor beside a legacy head.
- **The key guards have a gap.** `sealedBody`, `RawSeal` and `rawSealFields`
  are not in any guard's list (P12). 3.2.x: guard them.
- **P5 and P6 log more than they assert.** The per-tick decision counts, the
  status counts, the appended kinds and the task states above are the lead's
  reading of logged lines. 3.2.x: the P5 and P6 probes assert the per-tick
  counts, the status, the appended kinds.
- **The probes are not standing rows.** P5, P6, P7, P13 and P15's real-binary
  row need their environment and are skipped in CI; the saved P5 and P9 probes
  run only when copied into a tag's tree.

## Release note

Installing either older version over a completed raw-layout migration makes its commit and exits readers report `ledger_tampered`; rc.1 additionally preserves refusal copies without changing live sealed files. (M1-M5)

By the lead's rerun on 487e8560, the old process appended one `ledger_tampered` and one `exits_ledger_unwritable` decision on each of three ticks, saved scheduler state with no tamper hold, left the queued task pending and changed no sealed document or head. (P5)

Rolling forward, the current readers and process consume the old process rows without a new tamper alarm and record no migration twice. By the lead's rerun on 487e8560 no tamper hold is restored, so `scheduler recover` is not needed, and the queued task is requested. When a cancellation's cleaner marker belongs to a dead process, the marker is taken over (on the first tick, by the rerun) and the cancellation stays unresolved until a clean; the row's clean runs the manual-clean decision with the test's own cleaner, not the `amq-squad seat clean` command. A cancellation that was already cleaned is not held. (P6)

The next ordinary current write completes each legacy or interrupted ledger migration with exactly one migration record; both commit heads and the exits head become raw with a larger sequence, and existing rc.1 quarantine files stay byte-equal. (P7-P8)

`amq-squad up --binary` refuses v3.1.2 and rc.1 both for a completed raw layout and for an authenticated interrupted migration, and leaves a bad seal, a wrong sequence, a missing document and an empty session to the owning reader's tamper check. Since #961 it also refuses every target, the current build and later ones included, with `ledger_layout_unreadable` when a commit or exits document cannot be read. (P1-P4)

Frozen rc.1 commit and exits bytes remain readable and migrate once; a new JSON key on a listed sealed type fails a key guard until a frozen rc.1 row names it. (P10-P12)

Verdict, snapshot, and routing outcome rows cross current, v3.1.2, and rc.1 in both directions with no alarm and no refusal either way, by the lead's run on 487e8560 (16 of 16 runs). (P13)

Legacy `quarantine_clear` gates are reported as legacy records and skipped by merge-train drift instead of raising an unreadable-store alert. (P14)

After the actual rc.1 executable renews a current task's lease and drops the renewal labels, `task show` reports the renewals as unlabelled and the next activity renewal logs once with their count. (P15)
