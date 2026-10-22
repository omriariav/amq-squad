# Add a pi launch adapter: mint mode, pinned version, fail-closed resume

## Problem

`amq launch` prepares and applies Claude, Codex, Cursor and Grok seats only. `docs/launch-api.md` says "Pi stays direct-only." A pi seat therefore starts through `amq coop exec` by hand. It gets no digest-gated Apply, no trust record, no resume and no launch liveness.

amq-squad 3.0 compiles a roster into one launch request. pi is one of its runtimes. The phase 1 spike (S4, 2026-10-21 and 2026-10-22, AMQ v0.104.0) ran one Claude seat and one Codex seat through Prepare and Apply. A pi seat had no path.

This issue asks to reverse the direct-only decision. The maintainer agreed to this on 2026-10-22 and set the shape below; the pi flags were measured on 2026-10-22.

## Evidence (AMQ v0.104.0)

- `docs/launch-api.md:212-213`: "The adapter set supports Claude Code, Codex CLI, Cursor CLI, and Grok Build. Pi, Gemini CLI, and OpenCode have no launch adapter. Pi stays direct-only."
- `internal/launch/adapter.go:16-19` and `:25-35`: `ProviderForExecutable` maps `claude`, `codex`, `grok`, `agent` and `cursor-agent`. Any other executable maps to `""`.
- `internal/launch/adapter_grok.go:74-96`: the Grok mint path. `PlanFresh` appends `--session-id <launch nonce>` and sets `ConversationID` to the launch nonce. `:98-121`: `PlanResume` appends `--resume <conversation id>`.
- `internal/launch/adapter_grok.go:49-57`: the Grok probe checks `--version` and `--help` for `--session-id` and `--resume`.
- `docs/pi-bridge-protocol.md:281-289`: pi's session id matches `^[A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?$`, at most 128 characters. A UUID launch nonce fits this grammar.
- `docs/remote-compat.md:74`: the bridge `session_id` is new on `newSession` and on a fork, and exists only while the extension is live. It is not a launch identity.
- pi 1.3.0, `--help`, measured 2026-10-22 (the spike's embedded runtime copy, run offline): `--session-id <id>` "Use exact project session ID, creating it if missing"; `--session <path|id>` "Use specific session file or partial UUID"; `--resume, -r` "Select a session to resume".

## What amq-squad needs

A `pi` provider in mint mode, built like the Grok adapter:

1. `PlanFresh` appends `pi --session-id <launch nonce>`. The conversation id is the launch nonce, as for Claude and Grok.
2. `PlanResume` appends the recorded session id. When pi cannot find that session, the launch must fail closed: a typed refusal, not a fresh session. pi's own `--session-id` creates the session when it is missing, so the adapter must check that the recorded session exists before it launches a resume, or it silently gets a fresh session under the old id. Do not take identity from the bridge `session_id`. It appears after start, is not bound to the launch, and changes on `/new` and on a fork.
3. A pinned verified pi version constant, with a probe like the Grok probe, and a capture floor if one is needed.
4. A deny-by-default argument grammar and a `grammar_version` in `PreviewV1.capabilities`. The maintainer picks the admitted forms.
5. Update `docs/launch-api.md:212-213` and the participants list.

## Why this belongs in AMQ

This is core launch work. AMQ owns the launch seam and every caller of the launch API gets pi seats.

Context: amq-squad 3.0 phase 1 spike, 2026-10-21/22. Filed after the maintainer's Slack review of 2026-10-22.
