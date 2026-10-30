# Admit the workspace-write sandbox settings as Codex provider arguments

## Problem

A Codex seat under `--sandbox workspace-write` can still write outside its workspace if the project's own Codex config widens the sandbox. A trusted project's `config.toml` (or the owner's) may set `[sandbox_workspace_write] writable_roots = ["~/.claude"]` or `network_access = true`. Codex merges that config under the command line, so a seat started with the right `--sandbox` flag then runs with a wider sandbox than the launch request shows. The trust digest does not cover the widening, because it lives in a config file.

The fix on the command line is two `-c` overrides, which Codex applies above config.toml:

```
-c sandbox_workspace_write.writable_roots=[]
-c sandbox_workspace_write.network_access=false
```

AMQ's Codex adapter refuses both as provider arguments. So amq-squad cannot put them in the launch request, where they would enter the trust digest. Instead `seat-exec` adds them at exec time, after AMQ has admitted the launch. That is amq-squad's one exception to "the wrapper never adds a provider flag" (lead ruling, 2b-2 fix 3). The pins hold, but the trust digest does not bind them, and every other provider argument is digest-bound.

## Evidence (AMQ v0.107.0)

- `internal/launch/adapter.go:89-100`: `ProviderStaticInputCapabilities("codex")` advertises `ConfigOverrides` with exactly two keys, `model_reasoning_effort` and `approvals_reviewer`. Prepare publishes this list as `preview.capabilities[].config_overrides`.
- `internal/launch/adapter_codex.go:221`: the `-c` argument rule validates its value with `validCodexConfigOverride`.
- `internal/launch/adapter_codex.go:241-244`: `codexConfigOverrideValues` holds only those two keys. `:253-261`: any other key makes the value invalid, so Prepare refuses the participant.
- amq-squad's compiler checks every provider flag against the advertised forms and overrides (`internal/seatlaunch/compile.go`, CheckArgumentForms). With the two pins in the request, every Codex seat fails that check before AMQ is asked at all.
- Live launch on 2026-10-24 (amq-squad branch v3-2b2-fix at 52a358c0, `AMQ_SQUAD_LIVE=1`, amq 0.107.0, fake providers, a private tmux server):
  - The Codex argv after seat-exec was `codex -c sandbox_workspace_write.writable_roots=[] -c sandbox_workspace_write.network_access=false --model gpt-6-astra -c model_reasoning_effort=medium --sandbox workspace-write --ask-for-approval on-request -c notify=[...]`.
  - The two pins precede the arguments AMQ admitted and are absent from the request AMQ digested.

## What amq-squad needs

Two keys added to the Codex config-override allowlist, with fixed value sets so the digest stays meaningful:

- `sandbox_workspace_write.writable_roots`, admitting the empty array `[]` (and, if AMQ prefers, an explicit list of absolute paths).
- `sandbox_workspace_write.network_access`, admitting `true` and `false`.

Both should be advertised in `config_overrides` like the existing two. Once a release carries them, amq-squad moves the pins into the launch request's Codex provider arguments, where they enter the trust digest. It then deletes seat-exec's exec-time exception, so the wrapper again adds no provider flag.

## Why this belongs in AMQ

The allowlist and the trust digest are AMQ's. Only AMQ can admit a `-c` key and bind it into the subject it trusts. A wrapper that adds flags after admission works around the digest, which is the thing the launch API exists to protect. Any embedder that runs Codex in workspace-write against untrusted project config needs the same two keys.

Context: amq-squad 3.0 phase 2b, brief 2b-2 fix 3 (Codex review item 5), 2026-10-24.
