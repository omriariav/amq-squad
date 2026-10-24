# squad.yaml, schema 1

`squad.yaml` describes one squad: its seats and the narrowing of the owner's
standing grant for this repository (plan 3.3, 3.4, 4.3, 7.2; brief 2b-5).
The Go form is `internal/team` (`squad.go`): `ParseSquad`, `LoadSquad`,
`Render`, `ExampleSquad`.

## Location and writer

- One file per repository: `<repo root>/squad.yaml` (ruling E2). guard,
  context and the launch compiler read it there. The task store does not
  follow it; it lives under the AMQ root (2b-1).
- The lead writes it, a seat does not: `squad.yaml` is in the implicit
  protected set (`grants.Implicit`, R37), so guard denies a seat's write.
- The write goes through the digest flow (R8): `amq-squad team propose`
  prints one digest for every file it would create and writes nothing;
  `amq-squad team apply --digest <d>` writes them when the digest still
  matches. `amq-squad team show` prints the parsed file and its effective
  grant; `amq-squad team check` validates it.

## Format

The strict YAML subset of `internal/yamlsub` (the same reader as the owner
file `~/.amq-squad/grants.yaml`): `#` comments, `key: value`, `key: [a, b]`,
and indented blocks of `- item`, `- key: value` maps, or `key: value` maps.
No tabs. An unknown key, a repeated key, a malformed line or a bad value is
a parse error.

## Keys

| Key | Value | Notes |
| --- | --- | --- |
| `schema` | `1` | required |
| `session` | AMQ session name | optional |
| `lead` | a seat handle | the visible lead seat |
| `lead_mode` | `builder` or `planner` | was `team init --lead-mode` |
| `execution_mode` | `global_orchestrator`, `project_lead`, `project_team`, `direct_lead_session` | was `team init --mode` |
| `control_root`, `target_project_root` | absolute paths | were `team init --control-root`, `--target-project-root` |
| `shared_cwd_exception` | text | the recorded reason for two mutation-capable seats sharing a tree |
| `defaults` | map of `runtime`, `model`, `effort`, `tools` | filled into a seat that leaves the field empty; a pi seat never takes model or effort |
| `seats` | list of maps | at least one |
| `mode`, `max_seats`, `cost_per_day_usd`, `permitted_runtimes`, `privacy_class`, `protected_paths`, `merge_repositories`, `merge_size_cap_lines` | as in the owner file | grant narrowing, below |

Seat keys: `handle` (required, `[a-z0-9][a-z0-9_-]*`, unique, not the
operator's `user`), `role` (catalog or custom role id; default the handle),
`runtime` (`claude`, `codex`, `pi`), `model`, `effort`, `worktree`
(repo-relative or absolute; default the repo root), `tools` (tool policy:
`minimal`, `coding`, `browser`, `data`, `full`), `actor_mode`
(`implementation` or `review`).

A seat's fields are the typed seat list of the 2b-2 launch compiler. Model
and effort travel as provider arguments for Claude and Codex seats only;
pi admits none (plan 4.3, amendment 23), so a pi seat with a model or effort
is a parse error. The argument forms are the ones AMQ advertises
(`ProviderCapabilitiesV1.AllowedArgumentForms`, amendment 20 (b)); amq-squad
keeps no list of its own, and a form AMQ does not list is refused before the
launch.

## Grant narrowing

The owner grant lives only in `~/.amq-squad/grants.yaml` (R16), else the
built-in defaults; the R37 protected set lives in the grants defaults
(`grants.Implicit`, ruling 3) and squad.yaml only adds to it. squad.yaml can
narrow the grant and never widen it:

- `mode: manual` narrows `auto`; `mode: auto` under an owner `manual` widens
  (ruling 2). Mode precedence (R13): the owner file sets it globally,
  squad.yaml narrows it per repository, and `--auto` / `--manual` and the
  `/auto` / `/manual` commands are the owner's keystrokes.
- `max_seats` and `cost_per_day_usd` at or under the owner's; the number of
  seats at or under the effective `max_seats`.
- `permitted_runtimes` a subset of the owner's; every seat's runtime in the
  effective list. `privacy_class: local` narrows `vendor`.
- `protected_paths` adds paths. An entry starting with `!` or `-`, or a
  removal key such as `unprotected_paths`, widens.
- `default_branch` and `merge` are the owner's alone.

A widening field is a parse error that names the field (`team.WidenError`,
`errors.Is(err, team.ErrWidens)`). guard reads the same grant keys through
the same function (`grants.ParseSquadNarrowing`) but leniently: a widening
field there is ignored and named in the resolved grant's `ignored` list, so a
hand-edited squad.yaml can never widen what a seat may do.

## Example

```yaml
# squad.yaml schema 1: one squad per repository, at the repository root.
# The lead proposes it as a digest (amq-squad team propose) and writes it
# after approval (amq-squad team apply); seats cannot write it.
schema: 1
session: feature-x            # the AMQ session name
lead: lead                    # a seat handle
lead_mode: planner            # builder | planner
execution_mode: project_team  # global_orchestrator | project_lead | project_team | direct_lead_session
# Grant narrowing: each key can only narrow ~/.amq-squad/grants.yaml.
mode: manual                  # auto -> manual narrows; manual -> auto is an error
max_seats: 4                  # at or under the owner cap
permitted_runtimes: [claude, codex, pi]
protected_paths:              # added to the owner's and the implicit set
  - docs/adr/
defaults:                     # filled into a seat that leaves the field empty
  runtime: claude
  effort: medium
seats:
  - handle: lead
    role: cto
    model: opus
    actor_mode: review
  - handle: dev
    role: senior-dev
    runtime: codex
    model: gpt-5.5
    worktree: ../feature-x-dev
    tools: coding
  - handle: scout
    role: researcher
    runtime: pi                # pi takes no model or effort (plan 4.3)
```
