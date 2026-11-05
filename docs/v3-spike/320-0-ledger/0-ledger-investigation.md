# Reviewer ledger investigation: sequence 152

Read-only investigation for t77. The only session data read was the lead-provided
snapshot at `/Users/omri/scratch-320-0/snapshot`; the live session root was not
read. Each tag was checked in a fresh detached scratch worktree under
`/private/tmp/amq-ledger-t77.t0NwsB`. The snapshot was not modified. Test-only
copies of its seed lived in `t.TempDir()` and were removed with that directory;
this note contains neither seed content nor a seed hash.

## Snapshot manifest

The lead's manifest says the head was sequence 152 both before and after the
copy. It records a present 32-byte `routing/seed`, device `16777229`, inode
`51060489`, and no hash or content. The reviewer ledger has 28 commit entries
and 12 discarded entries. Its head at the copy was:

| field | value |
| --- | --- |
| sequence | 152 |
| seed device / inode | 16777229 / 51060489 |
| head timestamp | 2026-11-05T13:50:16.628025Z |
| reviewer ledger SHA-256 | `a109872b2ec013e52084949d31920fbc62e883933c07fd1113d58f82cd0f4e18` |

## Verifier runs

Each harness is copied in `harness/testdata/<tag>/ledger_snapshot_test.go` and was copied
into that tag's `internal/seatcommit` package for its run. It copies the seed,
ledger, and head to a new temporary root, unmarshals the ledger, clears `seal`,
and calls that version's direct `sealchain.Link.Verify` seam with
`sealchain.Key(seed)` and `ledgerLabel`. The head's device/inode and copied path
are deliberately not inputs to this seam. This avoids `ReadLedger`'s separate
copied-inode guard, and requires no re-seal. The ledger body bytes remain
unchanged before and after every run: each harness records the copied body's
SHA-256 before `Verify` and fails if the afterward SHA-256 differs. The hash
above is the snapshot manifest's recorded body hash.

| tag | seam | result | verbatim refusal / outside input |
| --- | --- | --- | --- |
| v3.1.0-rc.1 | `Link.Verify` | refused | `seq-152 ledger body seal rejected`; input is the 32-byte seed via `Key`, then seq, prev, and decoded ledger body |
| v3.1.1 | `Link.Verify` | accepted | same direct inputs; no path or inode comparison |
| v3.1.2 | `Link.Verify` | accepted | same direct inputs; no path or inode comparison |

All three offline runs used:

```text
AMQ_LEDGER_SNAPSHOT=/Users/omri/scratch-320-0/snapshot go test ./internal/seatcommit -run TestSnapshotSeq152Seal -count=1
```

The rc.1 command failed only with the refusal above. The 3.1.1 and 3.1.2
commands returned `ok`.

## Code read, per tag

All three tags use the same seal algorithm and label. `Tag` JSON-marshals its
value, then HMAC-SHA256s `label`, a zero byte, and those JSON bytes.
`Key` hex-decodes the seed when possible, otherwise uses the seed bytes. `Seal`
and `Verify` marshal a `sealedBody` in field order `seq`, `prev`, `body`.
The label is `amq-squad commits ledger v1`.

| tag | tag / key / body code | ledger verification / discard schema |
| --- | --- | --- |
| v3.1.0-rc.1 | `internal/sealchain/sealchain.go:53-75,228-249` | `internal/seatcommit/ledger.go:82,195-205,371-410`; no `Attempt` field |
| v3.1.1 | `internal/sealchain/sealchain.go:53-75,228-249` | `internal/seatcommit/ledger.go:82,195-206,372-411`; `Attempt int json:"attempt,omitempty"` |
| v3.1.2 | `internal/sealchain/sealchain.go:53-75,228-249` | `internal/seatcommit/ledger.go:82,195-206,372-411`; `Attempt int json:"attempt,omitempty"` |

The sequence-152 body has one discard with `attempt`: t69 attempt 1, at
`2026-11-05T12:26:08.093296Z`, reason `blocked: widen_requested`. Rc.1 drops
that unknown JSON field on unmarshal, then verifies a re-encoding of the
shorter `Ledger` value. It therefore computes a different tag. 3.1.1 and 3.1.2
retain the field and accept. This establishes which computation applies to the
current seq-152 body: the 3.1.1/3.1.2 computation includes `attempt`, and the
rc.1 computation cannot verify a body containing it.

## Task provenance

The snapshot ledger has no commit entry for t64, t70, or t71. It has one t64
discard only. The task snapshots record t64 attempt 1 as reviewer/expired and
attempt 2 as dev/cancelled; t70 attempt 1 as reviewer/expired and attempt 2 as
dev/blocked. The t71 snapshot has one live reviewer attempt and no report or PR
binding. Consequently the snapshot alone cannot establish an earlier-attempt
commit carried into t71.

An allowed outside-test fetch of `seat/reviewer` found that exact remote ref no
longer exists. The available `origin/seat/reviewer-t71` ref has one t71 commit
outside `origin/v3`: `781ca72d0b40daae500d0877fbecce8be1488a1d`, tree
`8487bb8a52380f05998c5e1f030040a5f747f891`, timestamp
`2026-11-05T14:36:37Z`. That is after the snapshot's 13:50:16Z head and the
ledger has no t64/t70/t71 commit entry to compare with it. It is therefore not
evidence that an earlier attempt was carried into t71.

| task / attempt | snapshot ledger row | branch result | carried into t71 PR? |
| --- | --- | --- | --- |
| t64 / 1 reviewer | discarded tree `446d506f825d07ad0949b4852157f5227d328f04` at `2026-11-05T04:41:10.88107Z`; no commit | no matching branch commit established | not established; no t71 PR binding |
| t64 / 2 dev | no row | not a reviewer-branch attempt | not applicable |
| t70 / 1 reviewer | no row | no matching branch commit established | not established; no t71 PR binding |
| t70 / 2 dev | no row | not a reviewer-branch attempt | not applicable |
| t71 / 1 reviewer | no row at snapshot | `781ca72d0…`, tree `8487bb8a…`, 14:36:37Z, after snapshot | cannot be established from this snapshot; no earlier target-task commit is present |

## Writers and scheduler timeline

Ledger entries carry task, commit or discard data, trees, and timestamps; none
carries a writer identity. The only entry in the listed 3.1.1 interval is the
t69 discard at 12:26:08Z. The scheduler decision log names the immediately
following `seat_cleaned reviewer` at 12:26:09Z as the only 3.1.1 ledger write,
but the ledger itself does not identify its writer. The table below therefore
states the record's identity, not an inference about the writer.

| time (UTC) | entry | task | tree / commit | writer carried by entry |
| --- | --- | --- | --- | --- |
| 2026-10-30 through 2026-11-05T06:39:17Z | 28 commits, 11 discards | t10, t19, t24, t26-t29, t30, t32-t41, t43-t46, t49, t51-t53, t55-t57, t59-t68 | all entries predate the incident intervals | none |
| 2026-11-05T12:26:08.093296Z | discard | t69 attempt 1 | `827781245a7ca9ec32bb1ca5abfa935ef028635b` | none |

For completeness, this is the copy's complete time-ordered index. `none` is
literal: no entry carries a writer field. A commit artifact is its commit SHA;
a discard artifact is its retained tree SHA.

| at (UTC) | kind | task | artifact | writer |
| --- | --- | --- | --- | --- |
| 2026-10-30T20:46:49.405968Z | discard | - | bc72fbfc24126fac36cdb7b94a2aeb2cbd57e32e | none |
| 2026-10-30T20:55:45.980747Z | discard | t10 | fe268b0eed60ef54279410c86ad5e48e62e205a9 | none |
| 2026-10-30T21:02:44.184088Z | commit | t19 | e9e6c53b77e819a6a02b7ec9e399e6fbecb65e94 | none |
| 2026-10-31T23:27:39.137554Z | discard | t24 | bb55876af7b4ea569bf60caff42b8d298ac9be80 | none |
| 2026-11-01T17:15:39.563733Z | discard | t26 | e0d8db14636a5e59c2e664a08166984b26364208 | none |
| 2026-11-01T21:34:15.819751Z | discard | t27 | 902070b1fa1729205220066e61a7f8547f31a841 | none |
| 2026-11-01T21:37:58.324331Z | commit | t28 | 5087ba8eba1ebe32fb8d101819ed6de315874848 | none |
| 2026-11-01T21:57:17.945763Z | discard | t29 | 99027a60d917aa428d56fa6dbe78ce4f861b09e4 | none |
| 2026-11-01T22:01:52.333354Z | commit | t30 | 058c48ddf35707994ae29ca7e6675ee56543b74b | none |
| 2026-11-01T22:15:48.392557Z | commit | t32 | 85cd00a8115cd28809aa71e8b3abcb01b09c4204 | none |
| 2026-11-01T22:28:20.302688Z | commit | t33 | ea37f2a99e87f2584e36dfba3466382bb1be28b2 | none |
| 2026-11-01T22:40:46.302131Z | commit | t34 | e7b4641412d971ac5619cb9d3aa733cfd57b92ad | none |
| 2026-11-01T22:55:04.339046Z | commit | t35 | 36fcf7b5aae93d461d48e1ad6f69489ba3a9e8ef | none |
| 2026-11-01T23:09:05.879281Z | discard | t36 | ae5f28c3e88e462c33b3226f4db4bd2e9fd2a9b9 | none |
| 2026-11-01T23:13:50.305240Z | commit | t37 | 5be39df8615b2ac8839ca5d783b178ccfa455a08 | none |
| 2026-11-01T23:25:16.384751Z | commit | t38 | 06e50c9ebe4786fd1db5a2f21328b68d8ec2d669 | none |
| 2026-11-01T23:43:04.321835Z | commit | t39 | 824bc6325fb4279422f0d4c0bf6064d36195b33f | none |
| 2026-11-01T23:55:58.285230Z | commit | t40 | c15a6dd92eb6e1b9be073dd90c0d92e1f92b4ee0 | none |
| 2026-11-02T00:13:35.884016Z | discard | t41 | 5a65ce6594bcdf1fa3fe098d8fc947faa8e85d34 | none |
| 2026-11-02T00:27:26.342889Z | commit | t43 | f91327cb169aca097452809b5afa33c95d823179 | none |
| 2026-11-02T00:39:00.363606Z | commit | t44 | c794c527c71d6f44306f8b8cb52f32ca5f481538 | none |
| 2026-11-02T00:45:48.381295Z | commit | t46 | 8b9b6a13e494a4747780ee14454c16b8457e501d | none |
| 2026-11-04T16:38:43.594775Z | commit | t49 | 960f636c6130bbf437f0780cb90ae328059bd1cf | none |
| 2026-11-04T18:27:35.461664Z | commit | t51 | 3b70798b409dc681ce28f5bd91cdf5fdb5b58705 | none |
| 2026-11-04T18:59:48.368035Z | commit | t52 | 210ad86de66efe2becb484c148c0dd4b9701a1cb | none |
| 2026-11-04T20:08:28.938074Z | discard | t53 | 96d1b767ab96eeaf4e72492aa34330d6b2f010b3 | none |
| 2026-11-04T20:18:03.358078Z | commit | t55 | 309acd1b03281fc6e96c08d765076faccb1cfa22 | none |
| 2026-11-04T20:32:29.393935Z | commit | t56 | d1c9b1ef3b09ec0e345b8242e80bc20b0dccf5f9 | none |
| 2026-11-04T21:25:18.865256Z | discard | t57 | 2ad466591b2baca523d16a9697e9492f5e441ee1 | none |
| 2026-11-04T21:40:27.393751Z | commit | t59 | b39bcbeb407af3996dba47135e88eaa336efe867 | none |
| 2026-11-04T22:17:35.334107Z | commit | t60 | 979ca1f1f35a8da0748f6f2063665c3123db2394 | none |
| 2026-11-04T22:35:59.486807Z | commit | t61 | 0d85c97f5ee520dfef274279b8c6a1d0dcf08c7c | none |
| 2026-11-04T22:53:49.353016Z | commit | t62 | 9af66754027cabc59a6028b3e0a805e1f8f5296c | none |
| 2026-11-04T23:15:59.455643Z | commit | t63 | 6cd33fc7e9994d151017b375d5875419234fb6dd | none |
| 2026-11-05T04:41:10.881070Z | discard | t64 | 446d506f825d07ad0949b4852157f5227d328f04 | none |
| 2026-11-05T04:55:23.514757Z | commit | t65 | f84e911eb36add2a73efab79ce8941cd37cf09e6 | none |
| 2026-11-05T05:11:41.465584Z | commit | t66 | 472078f02307c2e9be97081f0de6626622688a48 | none |
| 2026-11-05T06:04:31.497017Z | commit | t67 | b2a327290d1c4817620cd37b9c9e1bf04a3983c9 | none |
| 2026-11-05T06:39:17.464753Z | commit | t68 | 104e39df1b42315ea4b7d4fb9444e4045732aae3 | none |
| 2026-11-05T12:26:08.093296Z | discard | t69 attempt 1 | 827781245a7ca9ec32bb1ca5abfa935ef028635b | none |

Relevant scheduler facts are: 3.1.1 incarnation `e8d657db060c` ran
12:26:07.775Z-12:26:10.498Z; 3.1.1 incarnation `27d56d896552` ran
12:27:18.100Z-12:27:18.720Z; rc.1 incarnation `a50f0aa880ed` began
12:28:14.654Z and raised `ledger_tampered at expired` for t64 at 12:28:14.907Z
and t70 at 12:37:31Z; 3.1.2 incarnation `5ecd55eb4fda` began 13:50:14.715Z.
The snapshot head records the 3.1.2 re-seal at 13:50:16.628Z, then t71 was
dispatched at 13:50:17.020Z. No head seed device or inode is zero in the
snapshot: both are 16777229 and 51060489.

## Lost body and bounded finding

The refused seq-150 body is gone. The ledger stores one seal over its current
body and each write re-seals that body, so the 3.1.2 writes that produced
sequences 151 and 152 overwrote the evidence. No conclusion here recovers the
seq-150 contents or attributes its writer.

It is established that rc.1 refuses the current seq-152 body because its
`LedgerDiscard` schema omits `attempt`, while 3.1.1 and 3.1.2 compute the tag
including that field and accept. Thus t71 meets the verifier-computation part
of its gate: the investigation identifies the applicable computation and shows
that 3.1.2 uses it. The separate provenance condition is not established by
this snapshot: it has no target-task commit entries and no t71 PR binding.
