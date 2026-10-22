# Derive AllowedArgumentForms from the validation tables and document every admitted form

## Problem

The launch grammar admits more provider argument forms than `PreviewV1.capabilities` advertises. A caller that reads `allowed_argument_forms` or `docs/launch-api.md` cannot tell that `--model`, `--effort` or `--permission-mode` are stable, so it cannot rely on them without a trial launch.

Measured on AMQ v0.104.0 (spike S4 re-run, 2026-10-22, codex-cli 0.163.0, claude 2.1.308): Prepare admitted Claude `--model` and `--effort` and Codex `--model`, `--sandbox workspace-write` and `--ask-for-approval on-request`, and put each one in the trust digest. The same Prepare result advertised only `--allowedTools`, `-n`, `--name` for Claude and `-c` for Codex.

The gap exists for all four providers.

| Provider | Admitted by the validation table | Advertised |
| --- | --- | --- |
| Claude | `--allowedTools`, `--effort`, `--model`, `-n`, `--name`, `--no-chrome`, `--permission-mode`, `--safe-mode`, `--verbose` | `--allowedTools`, `-n`, `--name` |
| Codex | `-c`, `--ask-for-approval`, `--model`, `--no-alt-screen`, `--sandbox`, `--search` | `-c` |
| Grok | `--tools`, `--disallowed-tools`, `--effort`, `--model`, `--no-alt-screen`, `--no-auto-update`, `--sandbox` | `--tools`, `--disallowed-tools` |
| Cursor | `--model` | none |

## Evidence (AMQ v0.104.0)

- `internal/launch/adapter.go:78-105`: `ProviderStaticInputCapabilities`. Claude forms at `:83`, Codex at `:90`, Grok at `:100`. Cursor falls to the `default` branch at `:102-103` and advertises an empty list.
- `internal/launch/adapter_claude.go:124-135`: `claudeArgRules`. `--effort` takes `low|medium|high|xhigh|max`. `--permission-mode` takes `acceptEdits|auto|manual|dontAsk|plan`.
- `internal/launch/adapter_codex.go:216-224`: `codexArgRules`. `--sandbox` takes `read-only|workspace-write`. `--ask-for-approval` takes `on-request`.
- `internal/launch/adapter_grok.go:129-138`: `grokArgRules`.
- `internal/launch/adapter_cursor.go:134-136`: `cursorArgRules`.
- `docs/launch-api.md:79-91`: the provider argument grammar section names `--allowedTools`, the session labels, the Grok tool lists, Codex `-c` and `--ask-for-approval` only.
- `docs/launch-api.md:71-74`: `grammar_version` "changes when the allowed argument forms, configuration overrides, or carrier support change."

## What amq-squad needs

1. Derive `AllowedArgumentForms` from the validation tables, so the two cannot diverge again. One source for both.
2. Document each admitted form and its value rule in `docs/launch-api.md`, for all four providers.
3. Settle the `grammar_version` rule in the document. State whether a change to the advertised list alone, with no change to what validation admits, bumps the version. amq-squad compares `grammar_version` per provider and needs to know what a bump means.

amq-squad sends per-seat model, effort and permission mode as provider arguments. It must detect support by capability, not by trial.

## Why this belongs in AMQ

The capabilities projection is AMQ's own contract. The maintainer confirmed the wider gap on 2026-10-22 and that upstream will derive the list from the tables.

Context: amq-squad 3.0 phase 1 spike, 2026-10-21/22. Filed after the maintainer's Slack review of 2026-10-22.
