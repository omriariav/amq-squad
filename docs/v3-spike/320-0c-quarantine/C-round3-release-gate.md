# 3.2.0-0c-B round 3 release gate

This round closes the successor and durability edges found after #934.

## Sequence rules

A sequence copied from refused head or body bytes is plausible only from 1 through 9007199254740990. This leaves a successor no greater than 9007199254740991, the largest integer carried exactly by interoperating JSON readers. A verified head is not subject to that copied-input bound. A copied value above the bound is not used for a slot name or chain link. If neither copy supplies a usable link, recovery requires `--seq unknown` and refuses with `recover_seq_unknown`.

No released binary wrote a `seq0` or negative-sequence quarantine slot. The new reader treats such edited head claims as unknown.

At the platform integer maximum, raw writers return `sealchain.ErrSeqExhausted` before invoking their save callback. A real chain above the copied-input bound can therefore be handled only when its head still verifies, or by `recover --alarm`; refused copies do not provide a continuation link.

## Layout and decision rules

Recovery records a layout migration only for legacy evidence. A usable head decides the evidence layout. Without one, the body decides it: legacy bodies migrate and raw bodies do not. An interrupted legacy-to-raw migration retains its existing completion rule.

`seq_from` describes the value that selected `Link-1`: `claimed_head` only for a plausible claim decoded from the copied head, `body_seal` for a copied body seal, and `verified_head` for an authenticated head. The slot's named sequence remains a separate value.

The recovery crash seam now runs after any required migration decision in the same pre-head closure. A retry can observe the durable decision before completing the head. If legacy evidence at different successor sequences is retried after separate refused copies, the decision log can contain one migration record for each sequence; deduplication is per ledger kind, seat, and sequence.

## Alarm append durability

The append open reports whether it created the file. The first append syncs the file and its parent directory once each. Later appends sync only the file. Directory-sync failure is returned to the recovery decision caller.

## Verification

Focused sealchain, quarantine, recovery, raw compatibility, and alarm tests run without network listeners. The full `internal/seatcommit` package reaches `TestHTTPSPushUsesOnlyThePinnedGhCredentialHelper` and fails only because this sandbox forbids its IPv6 loopback listener (`listen tcp6 [::1]:0: bind: operation not permitted`).

The release seat gate is subject to the same sandbox listener restriction.

Successor work for the remaining recovery and operational evidence is tracked in the 0e 3.2.x follow-up.
