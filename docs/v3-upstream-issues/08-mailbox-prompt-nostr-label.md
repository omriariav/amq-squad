# Label mailbox-binding prompts with nostr:<eventID> and document layer-side gate checks

## Problem

On the mailbox binding (`amq-remote attach --self`), a Buzz DM arrives in the handle's inbox as an AMQ message from `buzz` with the labels `acp` and `buzz`. The message carries no Nostr event id. The handle cannot find the owner's signed event on the relay, so it cannot verify that the owner, not a model, answered a gate.

The owner's signed evidence reaches only the Claude hook path. Codex and pi get a bare option string that amq-remote decided. Steering messages on the same binding already carry a `nostr:<eventID>` label, and cockpit prompts from the ACP bridge do too. Gate prompts do not.

The maintainer ruled on 2026-10-22 that a message header is not a sound carrier for the event itself: on the mailbox binding amq-acp never holds the signed event, and any local process can write a header. Replay is stopped only by the checks the layer must run against the relay anyway. This issue asks for the maintainer's alternative.

## Evidence (AMQ v0.104.0)

- `internal/acp/mailbox.go:566-576`: `publishOnce` writes the prompt with `Labels: []string{"acp", "buzz"}`. The event id is known to the caller as `r.eventID` (`:445`).
- `internal/acp/mailbox_steer.go:301-304`: a steer appends `"nostr:"+eventID` to its labels.
- `internal/acp/deliver.go:69-72`: the ACP cockpit path appends the same label once, replay-stable.
- `internal/remote/buzzio/carrier.go:1029-1042`: evidence is attached only for an approve gesture and passed in `Command.Evidence`.
- `internal/remote/protocol/protocol.go:208`: `Evidence json.RawMessage` is `json:"-"`, in-process only.
- `internal/remote/core/respond.go:154-155`: the endpoint passes evidence only to an `EvidenceResponder` (`internal/remote/core/attachment.go:184-192`).
- `internal/remote/claude/approvals.go:521`: the only implementer. Codex answers `{"decision": option}` (`internal/remote/codex/attachment.go:1010`, `:1078`). pi writes an `answerRecord` whose `subject` is reserved and unchecked (`internal/remote/pi/deliver.go:170-180`, `docs/pi-bridge-protocol.md:369`).
- `docs/amq-remote-approvals.md:36-62`: the checks the Claude hook runs on an allow.
- Spike S1, owner run of 2026-10-22 with the owner's real key: the layer's own verifier resolved, fetched and verified a kind 9 thread reply end to end. Gate g-owner-02 passed in 23 490 ms from post to verified envelope. Gate g-owner-01, answered outside the gate thread, was rejected `not_in_gate_thread`.

## What amq-squad needs

1. Add `nostr:<eventID>` to the labels of every mailbox-binding prompt in `publishOnce`, with the label the steer path already uses. Keep the bytes identical on redelivery.
2. Document the label as a contract: the label names the Nostr event that caused the prompt, and the handle reads that event from the relay itself.
3. Carry the event id to the pi bridge answer file as well. The reserved `subject` field, or a new field, with its meaning stated in `docs/pi-bridge-protocol.md`.
4. State in the document that the layer runs the same checks as the Claude hook on the event it fetched: pinned owner and body key, DM channel, gate message `e` tag or thread, nonce in the content, time window, edit and delete history, single use. AMQ verifies nothing on the layer's behalf.

The layer side exists. The S1 verifier already does `resolve`, `fetch`, `verify` against the relay.

## Why this belongs in AMQ

Only AMQ knows which event produced a mailbox prompt. One label, written where the steer label is written, gives every layer the same anchor the Claude hook gets.

Context: amq-squad 3.0 phase 1 spike, 2026-10-21/22. Filed after the maintainer's Slack review of 2026-10-22.
