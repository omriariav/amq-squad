# launchapi resolves AMQPath with os.Executable(); an embedding binary cannot use tmux

## Problem

`launchapi` resolves the AMQ executable with `os.Executable()` and passes it as `AMQPath` to every managed backend. A Go program that embeds the `launchapi` library is not `amq`. The tmux backend then types `<that binary> coop exec --root <root> --me <handle> ...` into each pane. The pane exits at once, tmux reports the failure, Create returns `backend_create_failed`, and the journal stays at phase `intent`.

The `commands` backend returns its argv to the caller, so a caller could rewrite `argv[0]` before it runs the line, although that is not a documented contract. A caller that asks for `tmux`, `cmux` or Ghostty has no such point.

Measured in spike S6. amq-squad 2.31.0 embeds the library at v0.75.0. Every `start --apply` from the amq-squad binary with the tmux backend failed at Create: three runs on 2026-10-21 (08:06 to 08:14) and the root-cause run on 2026-10-22 at 06:24. The resolution point is unchanged at v0.104.0 and on `main` as of 2026-10-22.

## Evidence (AMQ v0.104.0)

- `launchapi/prepare.go:53`: `amqPath, err := os.Executable()`. `:86`: `AMQPath: amqPath` in `PrepareDependencies`.
- `launchapi/types.go:172-179`: `PrepareRequestV1` has `request_version`, `target`, `launcher`, `placement`, `caller_context` and `intent`. No field names the AMQ executable.
- `internal/launch/backend_common.go:50-61`: `agentCommand` builds the pane line from `req.AMQPath` through `coopExecArgv` (`internal/launch/commands.go:113-114`): `[amq, "coop", "exec", "--root", sessionRoot, "--me", handle, ...]`.
- `internal/launch/tmux.go:106-117`: the tmux backend only resolves `AMQPath` with `LookPath` and `EvalSymlinks`. `:155-157` and `:173-174`: that line is the `new-session` and `new-window` command.
- `internal/launch/execution.go:66-67`: the execution ticket already records `amq_executable` and `amq_executable_identity`.
- `internal/launch/adapter.go:463-473` and `:541`: a provider executable is made absolute, resolved, and refused when project-contained (`provider_project_contained`).
- Measurement, S6: each run printed `error: launchapi: 0 applied command(s) for 3 team member(s)` after the trust decision was recorded. The journal recorded `backend: tmux`. Every execution ticket carried `"amq_executable"` equal to the embedding binary's path. No pane survived. The same roster launched on 2026-10-22 at 06:41 through amq-squad's legacy pane driver, which calls `amq` from PATH.

## What amq-squad needs

An optional executable field on `PrepareRequestV1`, for example `amq_executable`, validated like a provider executable: absolute or found on PATH, resolved, not project-contained, identity recorded in the ticket, and part of the subject digest. `os.Executable()` stays the fallback when the field is empty.

An environment override read at the same point would also work, but a request field is explicit and digest-covered.

## Why this belongs in AMQ

Any embedder of the library hits it. The resolution point and the pane command are both inside AMQ, and only AMQ can validate the path it will type into a pane.

Context: amq-squad 3.0 phase 1 spike, 2026-10-21/22. Filed after the maintainer's Slack review of 2026-10-22.
