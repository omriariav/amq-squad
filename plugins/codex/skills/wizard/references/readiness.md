# Readiness and launch preflight

Setup starts with a read-only readiness pass. Do not certify a launch from a
hand-assembled checklist; read what `doctor`, `team check` and the launch itself
report.

```sh
amq-squad doctor --project P
amq-squad team check --project P
```

## What doctor checks

- the AMQ version and launch API (0.116.2 or later for `amq`, `amq-remote` and
  `amq-owner-sign`), the amq-squad on PATH against this build, the AMQ identity pin;
- the tools on PATH (claude, codex, the built-in pi, grok, cursor-agent, ollama, amq,
  amq-remote, tmux, gh) with their versions against the versions the launch API
  verified, and their login state;
- RAM and GPU, the registry entries, the full re-hash of the embedded pi runtime;
- project roots codex-cli does not trust, the Codex notify fan-out line, exported key
  names in the shell profile, the owner's global agent config against the preflight
  copy, and the grants file mode;
- the embedded squad.yaml golden (`squad_yaml_golden_drift` when the parser drifted).

A missing optional tool is one status line, not a failure. Without `--write-registry`,
`--probe` or `--fix` doctor writes nothing, and it exits non-zero when any check is
`fail`. `doctor --json` is the same rows as a JSON envelope. The owner runs `--probe`
(and `--write-registry`, `--fix`) from their own terminal; seats never do: inside a
seat those flags refuse with `seat_owner_state_refused`.

## Drafter readiness

`brief --goal`, `role draft` and `team draft-rules` share one drafter resolution: the
user config `~/.config/amq-squad/config.json` (or `AMQ_SQUAD_CONFIG`), else the
in-session path. The in-session path prints the filled prompt and stages nothing;
complete it in the session and seed the result (`brief --seed-from file:<path>`).
A drafter failure is not a success: nothing is written.

## What the launch checks

`launch` sends one AMQ launch request for the squad. It stops, or a seat refuses to start, when:

- a required action (AMQ's trust confirmation, for example) needs an answer: it
  prints the action ids and exits 10; rerun with `--decision ACTION_ID=CHOICE` only
  on the owner's answer;
- a seat's role file, rules digest or launch record does not verify in `seat-exec`
  (one stderr line naming the reason code; `identity_incomplete` exits 14).

The launch does not check worktree isolation: one worktree per mutation-capable seat
is the squad's rule (see `worktrees.md`), so check the seat list before launching.

A seat is reported up only after seat-exec proved its identity with one read-only
AMQ check; nothing is drained.

## Reading failures

| Code | Meaning | Safe response |
|---|---|---|
| `squad_yaml_invalid` | a parse or validation error, the key named | fix the draft and propose again |
| `grants_invalid` | a key widens the owner grant or is owner-only | remove it; squad.yaml only narrows |
| `squad_yaml_schema_unsupported` | not schema 1 | 3.0 reads schema 1 only |
| `squad_yaml_digest_mismatch` | the options changed since the proposal | run `team propose` again and get a fresh approval |
| `squad_yaml_changed` | squad.yaml was edited on disk since the proposal | run `team propose`; the edit is the diff the owner decides on |
| launch exit 10 | a required action is unanswered | relay the action ids to the owner; never answer it yourself |
