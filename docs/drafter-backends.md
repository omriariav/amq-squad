# Drafter backends

amq-squad can delegate bounded prose generation to a headless local CLI while
keeping validation, file staging, launch policy, and lifecycle decisions in the
amq-squad binary. The setting is profile-scoped because different team shapes
may need different cost, latency, or credential policies.

Profiles without a `drafter` block keep the backward-compatible
`in_session` behavior. Adding the block writes team schema 6; older binaries
fail closed instead of silently rewriting the new field.

## Settings

The full block is:

```json
{
  "drafter": {
    "backend": "yoetz",
    "command": ["optional", "argv", "template"],
    "model": "optional-model",
    "effort": "optional-effort",
    "timeout_seconds": 180,
    "on_failure": "in_session"
  }
}
```

`backend` accepts `in_session`, `yoetz`, `claude`, `codex`, or `custom`.
`timeout_seconds` defaults to 180 and is capped at 3600. `on_failure` accepts:

- `in_session` (default): return a structured fallback with the reason and an
  actionable remedy so the caller can finish from the generated prompt.
- `error`: fail closed and return the same command evidence with an error.

`command` is an argv array, not a shell command. No shell parses it. A custom
backend requires this field; setting it on a preset overrides that preset's
default argv. Four tokens are available:

| Token | Expansion |
| --- | --- |
| `{prompt}` | private `0600` prompt file |
| `{out}` | output file the command must create |
| `{model}` | configured `model` value |
| `{effort}` | configured `effort` value |

When `{prompt}` is absent, the prompt is sent on stdin. When `{out}` is absent,
the draft is read from stdout. Model and effort values used with an overridden
or custom command require their matching token, which prevents a knob from
being accepted but silently ignored.

## Presets

### yoetz

[yoetz](https://github.com/avivsinai/yoetz) is a fast CLI-first LLM gateway:
one local command that fronts multiple model providers (and can convene a
multi-model "council"), with provider API keys configured once per yoetz
install — amq-squad never sees them.

```json
{
  "drafter": {
    "backend": "yoetz",
    "model": "gemini/gemini-3.5-flash",
    "timeout_seconds": 60,
    "on_failure": "in_session"
  }
}
```

The preset runs `yoetz ask` with `--prompt-file`, `--output-final`, text
response format, and notifications disabled. The configured model is passed as
`--model`. yoetz has no generic effort flag; use a custom command template when
a provider-specific effort option is required.

### Claude

```json
{
  "drafter": {
    "backend": "claude",
    "model": "fable",
    "effort": "low",
    "on_failure": "error"
  }
}
```

The preset sends the prompt on stdin to `claude -p --output-format text` with
session persistence disabled, plus the configured `--model` and `--effort`.

### Codex

```json
{
  "drafter": {
    "backend": "codex",
    "model": "gpt-5.6-luna",
    "effort": "medium"
  }
}
```

The preset sends the prompt on stdin to `codex exec --ephemeral --color never`
and maps effort to `--config model_reasoning_effort=...`.

### Custom command

```json
{
  "drafter": {
    "backend": "custom",
    "command": [
      "local-drafter",
      "--prompt-file", "{prompt}",
      "--output", "{out}",
      "--model", "{model}"
    ],
    "model": "fast-local"
  }
}
```

This shape also supports the field-tested yoetz form directly:

```json
{
  "drafter": {
    "backend": "yoetz",
    "command": [
      "yoetz", "ask",
      "--model", "{model}",
      "--prompt-file", "{prompt}",
      "--output-final", "{out}"
    ],
    "model": "gemini/gemini-3.5-flash"
  }
}
```

## Drafting a custom role

`role draft` is the first consumer of the shared drafter layer:

```sh
amq-squad role draft researcher --binary codex \
  --purpose "Investigate ambiguous product behavior" \
  --project P --profile R --session S
```

The binary fills a built-in role template, attaches the active brief as
untrusted context, and delegates only the prose. Before writing anything it
requires exact `id`, `label`, `binary`, and `peers` frontmatter; the `Mission`,
`Boundaries`, and `Protocol` shape; fewer than 45 lines; and a reusable draft
that does not name the active session, a task id, a version, or the current
branch. A valid draft is published without overwriting an existing
`.amq-squad/roles/<id>.md`.

With no external drafter configured, or when the configured backend falls back,
the command prints the filled prompt and stages nothing. External fallback
output includes the exact attempted command and remedy. With
`on_failure: error`, the command stops instead.

`role draft` never adds a roster member, raises or answers a gate, launches a
pane, or claims verification. Review the staged file, then run the printed
`team member add <id> --binary <binary>` command. For a short-lived seat whose
task body already supplies enough direction, skip persona drafting entirely;
the launch path generates a neutral contract.

## Keyless environments and evidence

A missing provider key, missing binary, timeout, non-zero exit, missing output
file, or empty draft is never treated as successful generation. With the
default `on_failure: in_session`, the result explicitly says that fallback was
used, preserves the provider or command failure, and tells the caller to check
credentials before retrying or finish from the prompt in the active session.
Use `on_failure: error` when automation must stop instead.

Every attempted external draft records the backend, exact argv and a
shell-escaped display form, model, effort, timeout, start time, duration, exit
code, and bounded stderr. Prompt content is carried in a private temporary file
or stdin rather than embedded in command evidence. Temporary prompt and output
files are removed after the attempt.
