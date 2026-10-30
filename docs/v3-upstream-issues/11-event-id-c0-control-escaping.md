# Event id commitment writes most C0 controls verbatim; NIP-01 clients escape them

## Problem

amq-remote hashes events with `fiatjaf.com/nostr`. That library's id commitment escapes only `"`, `\`, `\n`, `\r` and `\t`. It writes every other C0 control byte verbatim. NIP-01 requires `\b` and `\f` to be escaped. `JSON.stringify`, which clients such as nostr-tools hash, escapes `\b` and `\f` and writes the other controls as `\u00XX`.

The same signed event therefore has two ids when its content or a tag holds `\b`, `\f`, or a C0 control other than `\t`, `\n` and `\r`. An event signed by such a client fails `CheckID` in amq-remote. The reverse also happens. The approval path is where this matters: `VerifyApproveEvidence` refuses on `CheckID`.

This claim was re-checked against AMQ v0.104.0 on 2026-10-22. The pinned library version is unchanged from v0.102.0, and the test vector below reproduced with that exact version.

## Evidence (AMQ v0.104.0)

- `go.mod:8`: `fiatjaf.com/nostr v0.0.0-20260916040958-27e395a0f6e7`.
- Library `event.go:73-76` (that version): `init` sets `escTable` for `"`, `\`, `\n`, `\r`, `\t` only. `event.go:199-204` and `:218-223`: `appendSerialized` writes any byte not in `escTable` as is.
- `internal/remote/buzzio/approve_proof.go:115` and `:137`: `VerifyApproveEvidence` refuses when `!r.CheckID()` or `!m.CheckID()`.
- Test vector, reproduced 2026-10-22 with the pinned module. pubkey `7e7e9c42a91bfef19fa929e5fda1b72e0ebc1a4c1141673e2794234d86addf4e`, `created_at` 1700000000, kind 9, tags `[["h","s1-vector"]]`. The id does not depend on the signature.

| content | NIP-01 reference id (`JSON.stringify`) | library `GetID` |
| --- | --- | --- |
| `"a\fb"` | `c1ce746eee1ef178e988713471e2a1062bed99175770cbb2f29cf7382807278c` | `d559be4e02658fe077bc203d34aa73356a5e54854a2a8696cd3c760f01b55174` |
| `"a\bb"` | `a7d8977f8f6a1240d1bc7542ffc9514b8988d1cb7f8418d091c72f013cd7fe22` | `60d419d2c4af0f1eaeaf30cd6bb6f2d4628c391bcf0f8efcb12f43bad7e967e3` |
| `"a\u0001b"` | `37847e11712fa7d0d1197c382c92b79106f736520ac3ccc9ad367a85105db0dd` | `6dde4a54306107d2ae43334cb097cbc4cbeab54a0e5dd167af5cb23cff7d462e` |
| `"a\nb"` (control) | `20123c9e3cb553383e7040d2375ce679933681b53c432a3538f837e4609317c7` | `20123c9e3cb553383e7040d2375ce679933681b53c432a3538f837e4609317c7` |

The reference commitment for the first row is `[0,"7e7e…df4e",1700000000,9,[["h","s1-vector"]],"a\fb"]` with `\f` as the two characters backslash and `f`. The library writes byte `0x0C` there.

## What amq-squad needs

Escape `\b` and `\f` as NIP-01 says, and the remaining C0 controls as `\u00XX` with lowercase hex, as `JSON.stringify` writes them. Do this in the id commitment, or refuse such events before `CheckID`. The fix belongs upstream in `fiatjaf.com/nostr`. AMQ can pin a fixed version, or add a guard in `VerifyApproveEvidence`.

amq-squad's own verifier refuses any event with such a byte in content or tags before it computes an id. That rule stays after a fix, because other relays and clients may still differ.

## Why this belongs in AMQ

It is a correctness fix in the event id computation that AMQ's approval proof depends on. It adds no concept.

Context: amq-squad 3.0 phase 1 spike, 2026-10-21/22. Filed after the maintainer's Slack review of 2026-10-22.
