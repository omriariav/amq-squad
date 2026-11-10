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

The tables below are the complete inventory, with paths relative to each
fixture root. `-` means the file did not exist before the old-build run.

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
scheduler appended to the existing current decision log because it could not
load the raw exits ledger.

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

All sixteen tests passed. F2 completed cleanly on the current writer. F3's
old-tag legacy commit migrated cleanly. rc.1's quarantine copy and the old
tamper result did not change the current answer. The current tick retained
the queued seats and deferred their exit checks because this fixture has no
preflight copies; it emitted no tamper decision.

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

## Release note

Installing v3.1.2 over a session whose legacy ledger rc.2 migrated makes the old commit reader and scheduler report `ledger_tampered`, leaves an untouched legacy sibling readable, and creates no quarantine copy (M1).

Installing v3.2.0-rc.1 produces the same refusal and additionally copies the refused commit document and head to `commits.<seat>.sequnknown` without changing the live files (M2).

If rc.2 stopped after writing the raw successor but before completing its head, both old versions still refuse it, and rc.1 names the copy from the verified legacy sequence when it can (M3).

In a mixed session, both old versions can still read and commit an untouched legacy seat, but they refuse the migrated seat and cannot load the migrated exits queue (M4).

A ledger recovered under rc.2 is still a raw-layout ledger to both old versions; v3.1.2 refuses it in place and rc.1 preserves the recovery slot while adding its own refusal copy (M5).

Neither old version changes, moves, or deletes a migrated live sealed document or head; the only live changes measured were the explicitly requested old-format F3 commit and its matching legacy head, while rc.1's quarantine and the task lock were additions outside live sealed paths (M6).

Rolling forward to rc.2 verifies every measured root, cleanly migrates the F3 legacy commit made during rollback, retains only the expected quarantine and decision evidence, and emits exit-check deferrals rather than `ledger_tampered` (M7).

The supported path prevents this state: `amq-squad up --binary` refuses v3.1.2 and v3.2.0-rc.1 for both a migrated root and a mixed migrated-plus-legacy root, so an owner must roll forward or start a new session (M8).
