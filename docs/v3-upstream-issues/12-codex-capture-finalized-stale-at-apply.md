# Capture-mode Codex seat is finalized stale at Apply, so __codex-notify never records

## Problem

A Codex seat launched through the launch API never reaches a `ready` conversation. Apply finalizes capture-mode seats with the backend's create-time evidence. No backend supplies Codex evidence at create, so the record is written `stale` with reason `evidence_missing`. The notify receiver then refuses any record that is not `pending`. The identity capture and the operator notify forward never run.

`docs/launch-api.md` says "An unused Codex launch stays pending and cannot be resumed." The code does not match the document.

Measured on AMQ v0.104.0 (spike S4 re-run, 2026-10-22 11:29 to 11:33, codex-cli 0.163.0, tmux backend). The same result was measured on v0.102.0 with codex-cli 0.147.0 on 2026-10-21. PR #1137 (in v0.104.0) fixed the nonce and ticket binding only. It did not touch the Apply-time finalization or the `pending`-only check.

## Evidence (AMQ v0.104.0)

- `internal/launch/reconcile.go:792-798`, in `finalizeCreatedState`: capture-mode seats call `CaptureIdentity` with `Final: true` and `create.CaptureEvidence[handle]`. This file is byte-identical to v0.102.0.
- `internal/launch/backend.go:183` and `:241`: `CaptureEvidence` is declared. `internal/launch/tmux.go` and `internal/launch/commands.go` never populate it.
- `internal/launch/capture.go:235-240`: `captureCodexIdentity` with no evidence and `Final: true` returns `CaptureStale`, `CaptureReasonEvidenceMissing`.
- `internal/launch/execution.go:870-871`, in `recordCodexNotify`: a record that is not `CapturePending` is refused with `pending codex conversation does not match notify generation`.
- `internal/launch/execution.go:936-953`: `bindCaptureTicket`, the #1137 binding. It accepts `spawn_attempted` or `acknowledged` and requires the receiver to run under the ticket's `provider_process`.
- `docs/launch-api.md:229-230`: "An unused Codex launch stays pending and cannot be resumed."
- Measurement, 2026-10-22: after Apply the Codex ticket was `spawn_attempted` with `provider_process` pid 9233, the pane pid. codex-cli ran `amq __codex-notify` twice under that pid, at 11:30:44.838 and 11:30:49.295. The conversation record stayed `stale` / `evidence_missing` with an empty identity. No evidence file exists under `meta/launch`. A Prepare-only resume probe exited 6 with `stale_conversation`. A replay of the second payload from outside the process tree was refused `codex capture receiver does not run under the seat's provider process`, which is #1137 working as designed.

## What amq-squad needs

Either keep the record `pending` after Apply until the first notify, as the document says, or let `__codex-notify` promote a `stale` / `evidence_missing` record that carries the same launch nonce. Document which.

Caveat for the fix. codex-tui 0.163.0 fires `notify` for its title-generation side thread first. In the run above the first notify carried `thread-id` `01a162bb-ded9-…` with `input-messages` beginning `Generate a concise, single-line task title…` and `last-assistant-message` `{"title":"Show seat role"}`. The second notify carried the real thread `01a162bb-cc74-…`, the only rollout file written. Both ids are UUIDv7 and share the seat cwd, so both pass `ParseCodexNotifyEvidence` (`internal/launch/capture.go:109`). A fix that binds the first notify would record the title thread and refuse the real one as a `CodexNotifyConflictError`. The receiver must match on the launch nonce in the input, or accept the first notify whose `input-messages` carries the seat's initial input. Arrival order is not a safe key.

amq-squad needs the completed capture and the notify forward that follows it. Both are evidence sources for routing and gates.

## Why this belongs in AMQ

It makes AMQ's own capture path do what its own document says. No layer concept enters.

Context: amq-squad 3.0 phase 1 spike, 2026-10-21/22. Filed after the maintainer's Slack review of 2026-10-22.
