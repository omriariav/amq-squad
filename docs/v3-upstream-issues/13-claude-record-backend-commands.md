# Claude mint-path conversation record hardcodes backend commands on a tmux launch

## Problem

The conversation record for a Claude seat launched by the tmux backend says `backend: "commands"` and `profile: "commands/any/v1"`. The Codex record of the same launch says `backend: "tmux"`. The Claude mint path writes the `commands` backend name as a constant instead of the backend that created the pane.

Measured on AMQ v0.104.0 (spike S4 re-run, 2026-10-22, claude 2.1.308, tmux 3.7c). First measured on v0.102.0 on 2026-10-21. Minor.

## Evidence (AMQ v0.104.0)

- `internal/launch/execution.go:680-683`, in `prepareExecution`, mint branch: `record.ExecutionEvidence` is written with `Backend: CommandsBackendName, Profile: CommandsProfile().Identity()`. Line `:681` is the constant. The line was `:660` in v0.102.0.
- Measurement, 2026-10-22, session `s4-104-1`, two seats in one tmux session: the Claude record is state `ready`, identity the launch nonce, `execution_evidence.backend: "commands"`, `profile: "commands/any/v1"`. The Codex record on the same launch is `backend: "tmux"`. Evidence files `conversation-s4-claude.json` and `conversation-s4-codex.json` in the spike.

## What amq-squad needs

Write the backend and profile that created the pane. The execution ticket or the binding record already knows the backend name; the mint branch should read it instead of the constant.

amq-squad reads the conversation record to attach a seat to its pane. A wrong backend name makes the record unusable as evidence.

## Why this belongs in AMQ

Record accuracy inside AMQ's own launch state.

Context: amq-squad 3.0 phase 1 spike, 2026-10-21/22. Filed after the maintainer's Slack review of 2026-10-22.
