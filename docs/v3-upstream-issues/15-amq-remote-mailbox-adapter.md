# amq-remote: a mailbox adapter kind for a strict relay share

## Problem

A strict relay share (per-kind, expiring owner grants signed by `amq-owner-sign`) must bind a declared adapter target, and the adapter kinds are `claude`, `codex`, `pi`, `acp` and `fake`. None delivers to an AMQ handle's mailbox. The mailbox binding exists only as `amq-remote attach --self` (amq-acp in binding mode), whose Buzz agent is a Buzz Desktop managed agent: Desktop holds its key and signs its grant with no kind limit and no expiry (`cmd/amq-acp/README.md`, "Trust limits"). A layer that must sign its own messages in the owner's DM (amq-squad posts each typed gate question with the share body key, and the owner's answer is checked against that question) cannot use that agent, and a strict share cannot reach a mailbox.

## Evidence (AMQ v0.107.0)

- `cmd/amq-remote/README.md`, "Manifest": the adapter kind table, no mailbox kind; "Strict share in one owner step": a share binds one declared target; "Owner DM commands": plain text is one submit to that target.
- `cmd/amq-remote/README.md`, "`attach`, `detach`, `bindings`" and `cmd/amq-acp/README.md`, "Binding mode": the mailbox delivery (an AMQ message from `buzz` with the `nostr:<eventID>` label, the handle's `amq reply` as the answer) runs only on the amq-acp managed agent.
- amq-squad 3.0 brief 4-5 (`docs/v3-spike/4-5-execution.md`, deviation 1): the share is bound to a `fake` adapter with DM commands off, only for presence, and amq-squad reads the owner's DMs from the relay with the body key and delivers them into the mailbox itself. Measured with a throwaway key against a local relay and the real 0.107.0 binaries.

## What amq-squad needs

An adapter kind `mailbox` with `config.handle` (an AMQ handle in the root, as `acp` names one) that serve can bind to a strict share. An owner DM is delivered as the mailbox binding delivers it: one AMQ message from the carrier handle with the `nostr:<eventID>` label, the same bytes on redelivery, and the handle's reply posted as the body's result row. Presence follows the handle's AMQ presence instead of a process. Then amq-squad drops its own DM reader and takes gate answers through the label path it already verifies.

## Why this belongs in AMQ

serve already owns the share's relay connection, the DM surface checks, redelivery and the result rows. A layer that reads the DM channel itself duplicates that and opens a second authenticated connection with the same body key.

## Status (2026-10-28)

Shipped upstream: the mailbox adapter kind in v0.111.0 (PR #1226), strict replies from the mailbox carrier in v0.112.0 (PR #1230). amq-squad binds the strict share to it from brief 5-amq-B (floor v0.116.2); the attachment no longer reads the DM channel. The presence part ("presence follows the handle's AMQ presence instead of a process") did not ship: the target is live while the handle's inbox exists, so the attachment still supervises serve for presence (4-5 deviation 2).
