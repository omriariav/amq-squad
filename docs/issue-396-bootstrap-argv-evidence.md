# Issue #396 bootstrap and argv evidence

## Scope and conclusion

The duplicated native argv was a real composition bug and is fixed here. The
new bootstrap acknowledgement is an advisory observability net. Neither change
proves that duplicated argv caused the reported missed Claude bootstrap;
delivery, client startup, prompt handling, and timing remain open alternatives.

## Redacted before reference

The affected session's `cto` launch record was inspected by selecting only the
native effort fields (no prompt, environment, unrelated argv, or secrets). It
contained the same Claude `--effort high` span twice. This is retained as the
BEFORE reference without copying the full process command.

## Installed-parser probe

Parser-only `--version` invocations were used; no prompt, network task, or
external mutation was attempted.

- Claude Code 2.1.207 accepted duplicate valid `--effort`, `--model`, and
  `--permission-mode` spans. It validated every permission-mode occurrence and
  warned on every invalid effort occurrence, so leaving duplicates in place is
  not benign.
- Codex CLI 0.144.1 accepted repeated `-c key=value` entries for the exercised
  keys `model_reasoning_effort`, `model`, and `approval_policy`. A reversible,
  local `features.shell_tool=false/true` probe rendered `true`, and the reverse
  order rendered `false`, directly confirming last-value-wins for `-c` config
  merging. It rejected
  duplicate native `--model`, `--profile`, `--sandbox`, and
  `--ask-for-approval` flags before launch.

Codex also accepted both short inline config spellings in parser-only feature
probes: `-c=key=value` and compact `-ckey=value`. The composer canonicalizes
both with split `-c key=value`, `--config key=value`, and
`--config=key=value`, applying precedence per config key.

The composer therefore applies last-layer-wins before invoking either binary,
for recognized singleton spans only. Precedence is team defaults, launch
override, then member-specific args. Unknown/repeatable args and a literal `--`
tail retain their original order.

## Operator workaround

Until running a build with this fix, configure native Claude/Codex args in one
layer only. Prefer team or member configuration and do not repeat the same args
on `run start`. Inspect the dry-run, select only the relevant fields from
`launch.json`, and inspect a redacted `ps` view when needed. Manually verify the
lead processed its bootstrap and workers sent READY; argv presence alone is not
bootstrap completion evidence.

## After-fix reproduction plan

Use an isolated temporary team with one Claude lead and one Codex worker. Put
the same recognized singleton in team, launch, and member layers; compare the
dry-run and selected launch-record fields, then inspect a redacted process view
and pane startup result. Expected: one effective singleton span, preserved
repeatables, the generated bootstrap prompt as the final positional argument,
and an independently visible bootstrap acknowledgement state.

The automated after-fix evidence exercises the integrated `runLaunch --dry-run`
plan and its launch-record expectation: it
asserts recognized singletons appear once, repeatables retain order, a literal
`--` terminates native option parsing, and generated bootstrap text is the
single final positional token even when its contents begin with flag-like
text. Existing positional tails and ambiguous unknown options suppress the
generated bootstrap and persist an explicit `not_required` reason; a terminal
bare `--` receives exactly one generated prompt. A live Claude/Codex pane launch was not attempted in this implementation
worktree because the task explicitly prohibited creating AMQ/runtime state;
the plan above remains the operator-run live check and is not claimed as
executed evidence.
