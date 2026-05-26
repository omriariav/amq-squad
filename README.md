# amq-squad

Role-aware agent team launcher built on top of [AMQ](https://github.com/avivsinai/agent-message-queue) by [Aviv Sinai](https://github.com/avivsinai).

AMQ owns messaging between agents. `amq-squad` owns the layer above: who is on the team, what role each agent plays, what shared norms they follow, and how to bring the whole squad up, down, back, or into a new workstream.

## Why

AMQ's `coop exec` is a generic launcher. It sets up a mailbox and execs into `claude` or `codex`, but it does not know:

- Which agent is the "CTO" vs "Fullstack" vs "QA" vs "PM" vs "Designer"
- What command originally launched each one (cwd, binary, flags, workstream)
- What durable team norms each agent should read at session start
- Which peers a given agent actively talks to

`amq-squad` captures this at team-setup time (`.amq-squad/team.json`, `.amq-squad/team-rules.md`) and per-agent at launch time (`launch.json` + `role.md` inside the AMQ mailbox). AMQ itself stays unchanged.

## Install

```sh
go install github.com/omriariav/amq-squad/cmd/amq-squad@latest
amq-squad version
```

Requires Go 1.25+, the `amq` binary on `PATH` (v0.34+), and `tmux` on `PATH` for `amq-squad up`.

## Quick start

The 1.0 flow is six verbs:

```sh
cd ~/Code/my-project

amq-squad team init                  # write .amq-squad/team.json + team-rules.md
amq-squad team sync --apply          # sync pointer stub into CLAUDE.md / AGENTS.md
amq-squad up --dry-run               # preview the launch plan
amq-squad up                         # open the team live in tmux

amq-squad status                     # live state of configured members
amq-squad history                    # restorable launch records for this project
amq-squad doctor                     # AMQ version / tmux / wake / markers

amq-squad down --all --force         # SIGTERM the team
amq-squad resume                     # plan recovery after reboot / upgrade / close
amq-squad fork --from <cur> --as <new>  # branch a fresh workstream
```

### tmux targets

`amq-squad up` defaults to `--target current-window`, which splits the tmux
window where you run the command. To bring a team up inside an existing tmux
session such as `main`, switch or attach to the desired window first, then run
`up` from that pane:

```sh
tmux switch-client -t main:6
cd ~/Code/my-project
amq-squad up --session v1-0-0-qa --terminal tmux --target current-window --layout tiled
```

Use `--target new-session --terminal-session NAME` only when you want
amq-squad to create a separate tmux session. `--terminal-session` names the new
session; it does not select an existing tmux session for `current-window`.

Panes are anchored to the pane you launch from (`$TMUX_PANE`), not to whatever
window happens to be focused, so a `current-window` launch is safe even under
iTerm2's `tmux -CC` integration: changing tabs mid-launch will not rehome the
panes onto an unrelated window. If you launch from outside tmux, `current-window`
refuses with a hint rather than guessing a target.

If the AMQ workstream already exists, omit `--fresh`. Stop the old team first
with `amq-squad down --all --force --session <workstream>`, then run `up` again
from the target tmux window. Add `--force-duplicate` only when stale live-agent
signals remain after the old panes are stopped.

Single-agent primitives:

```sh
amq-squad agent up codex --role cto --session issue-96
amq-squad agent resume fullstack
```

Legacy 1.x verbs still work and emit a one-line deprecation warning. See [Deprecated 1.x verbs](#deprecated-1x-verbs).

### Custom launchers

A managed member can be launched through a project-specific wrapper script while
still receiving AMQ identity, bootstrap, a launch record, and status/resume. Pass
`--launcher` (and optional `--launcher-args`) on `agent up`, or set `launcher` /
`launcher_args` on the member in `team.json`:

```sh
amq-squad agent up claude --role qa --session beta --team-workstream \
  --launcher /path/claude-pm-os-dev.sh --launcher-args "--pull --workspace /ws"
```

The launcher is exec'd in place of the binary. `--launcher-args` come first, then
amq-squad appends the agent's normal child args (bootstrap + defaults), so **the
launcher script must forward its trailing args to the agent** (e.g. end with
`exec claude "$@"`). amq-squad refuses with a clear error if the launcher path is
missing or not executable. `up --dry-run` prints the resolved launcher command.

## Context model

The 1.0 context model is three durable layers. Each layer has exactly one source of truth.

| Layer | File | Owner |
| --- | --- | --- |
| Team norms | `.amq-squad/team-rules.md` | shared, hand-edited |
| Per-agent persona | `<agent-dir>/role.md` | seeded by launch, then user-editable |
| Workstream brief | `.amq-squad/briefs/<session>.md` | one per AMQ session |

`CLAUDE.md` and `AGENTS.md` carry a small managed pointer block that links to the three layers above; they never duplicate team-rules content. `amq-squad team sync --apply` writes and refreshes that block. Anything outside the markers is yours and is preserved.

```
<!-- amq-squad:managed:begin -->
This project uses amq-squad for agent team coordination.

- **Team norms:** `.amq-squad/team-rules.md`
- **Your role:** when launched via amq-squad, `<your-agent-dir>/role.md` carries your persona.
- **Active brief:** read `.amq-squad/briefs/<session>.md` for the current workstream (bootstrap names the exact path).

These files are the source of truth. Do not duplicate their content here.
<!-- amq-squad:managed:end -->
```

### Workstream briefs

A brief lives at `.amq-squad/briefs/<session>.md` and carries the goal, scope, and source-of-truth pointers for one AMQ session. Every team member reads the same file.

```sh
amq-squad up --dry-run --seed-from file:./brief.md     # preview candidate (no write)
amq-squad up --dry-run --seed-from issue:31            # preview from current-repo issue
amq-squad up --dry-run --seed-from gh:owner/repo#31    # preview from explicit GH issue

amq-squad up --seed-from issue:31                      # write brief + bring team up
amq-squad up --seed-from issue:31 --force              # overwrite an existing brief
amq-squad up                                           # preserve existing brief
```

`--seed-from` semantics:

- With `--dry-run`: prints the candidate brief envelope and writes nothing.
- Without `--dry-run`: writes `.amq-squad/briefs/<session>.md` and brings the team up in the same call. An existing brief is preserved unless `--force` is set.

### Profiles (schema 2)

Profiles let one team-home hold parallel team shapes (for example a release team and a research team).

```sh
amq-squad team init                          # default profile -> .amq-squad/team.json
amq-squad team init --profile release        # named profile -> .amq-squad/teams/release.json
amq-squad team profiles                      # list configured profiles
amq-squad team profiles --json
amq-squad up --profile release               # operate on a named profile
```

`team.json` files use `schema: 2` (the JSON key in persisted team profiles is `schema`; `schema_version` is reserved for the read-only JSON command envelopes documented below). Omit `--profile` (or pass `--profile default`) for the default profile.

## Verbs

Team-level verbs:

```text
amq-squad team init [--profile NAME] [--personas a,b] [--binary role=bin,...]
                     [--session ws] [--trust sandboxed|trusted]
                     [--model role=model,...] [--codex-args ...] [--claude-args ...]
                                  Write a team profile and seed .amq-squad/team-rules.md.
amq-squad team rules init [--force]
                                  Seed or refresh .amq-squad/team-rules.md.
amq-squad team sync [--apply] [--allow-outside]
                                  Sync the pointer stub into CLAUDE.md and AGENTS.md.
                                  Exit 1 on drift when --apply is not set.
amq-squad team profiles [--json]  List configured profiles (default + named).

amq-squad up [--profile NAME] [--session ws] [--dry-run] [--json]
             [--seed-from file:|issue:|gh: [--force]]
             [--terminal tmux] [--target current-window|new-session]
             [--layout vertical|horizontal|tiled] [--terminal-session name]
             [--stagger 750ms] [--no-bootstrap] [--force-duplicate]
                                  Bring the configured team up live (tmux) or print the
                                  plan with --dry-run. Optionally seed/refresh the
                                  workstream brief from a deterministic source.
amq-squad down [--all | --role R] --force
                                  SIGTERM members. Without --force the verb refuses.
amq-squad resume [--profile NAME] [--session ws] [--restore-existing]
                 [--dry-run] [--force-duplicate]
                 [--no-bootstrap] [--trust sandboxed|trusted]
                 [--model role=model,...]
                 [--codex-args args] [--claude-args args]
                                  Classify each member as live / restore / launch fresh
                                  / blocked and print copy-pasteable commands.
                                  Restore/replay commands use `agent up <binary>`
                                  and preserve saved metadata. Forced live restores
                                  include `--force-duplicate`.
                                  Plan-only. Use `fork --from <current> --as <new>`
                                  to branch into a new workstream.
amq-squad fork --from <current> --as <new> [--force-duplicate]
                                  Plan fresh launches in a new workstream branched off
                                  the current one. Plan-only; does not copy launch
                                  records, briefs, conversations, or team.json. The
                                  workstream brief at .amq-squad/briefs/<new>.md is
                                  created or preserved by the subsequent
                                  `up --session <new>` (or `agent up`) live launch.
amq-squad status [--json]         Live state of configured team members.
amq-squad history [--json] [--project a,b]
                                  Restorable launch records across known projects.
amq-squad doctor [--json]         AMQ version, team config, tmux, wake, markers.
amq-squad version [--json]        Print the installed amq-squad version.
amq-squad completion <bash|zsh|fish>
                                  Print a shell completion script to stdout.
```

Single-agent primitives:

```text
amq-squad agent up <binary> [--role R] [--session ws] [--team-profile NAME]
                            [--conversation ref] [--no-bootstrap]
                            [--trust sandboxed|trusted] [--model NAME]
                            [--codex-args ...] [--claude-args ...]
                            [--force-duplicate] [-- <native flags>]
                                  Launch one agent. Writes launch.json + role.md
                                  in the AMQ mailbox, injects bootstrap, then execs
                                  amq coop exec.
amq-squad agent resume <role>     Replay one saved launch record.
```

For `agent up`, recognized launcher flags after `<binary>` (such as `--role`, `--session`, `--trust`, `--model`, `--codex-args`, `--claude-args`, `--help`) keep flowing into the launcher; unrecognized flags and the first non-flag positional are treated as child args. Use `--` for an explicit child boundary. `amq-squad agent up codex --help` prints launcher help; `amq-squad agent up codex -- --help` passes native help to the child. `--codex-args` and `--claude-args` accept dash-prefixed values such as `--codex-args '--enable goals'`.

Global output flags work before or after the subcommand: `--quiet`, `--verbose`, `--color auto|always|never`. `NO_COLOR` overrides `--color=always`. `--quiet` and `--verbose` are mutually exclusive. Deprecation warnings survive `--quiet`.

## JSON envelopes

Verbs that produce machine-readable output accept `--json` and emit a schema-versioned envelope on stdout. Diagnostics stay on stderr; stdout under `--json` is pure JSON.

```sh
amq-squad status --json | jq .
amq-squad history --json | jq .
amq-squad doctor --json | jq .
amq-squad team profiles --json | jq .
amq-squad up --dry-run --json | jq .
amq-squad version --json | jq .
```

Envelope shape:

```json
{
  "schema_version": 1,
  "kind": "<verb>",
  "data": { /* verb-specific payload */ }
}
```

## Exit codes

| Code | Meaning |
| --- | --- |
| `0` | success |
| `1` | usage / user error (unknown flag, bad argument, missing required input) |
| `2` | system / runtime error (IO, process, config, environment) |
| `3` | partial success (some targets succeeded, some failed; e.g. `down` with mixed force-sent + failed) |

## Shell completions

```sh
amq-squad completion bash > /etc/bash_completion.d/amq-squad
amq-squad completion zsh  > "${fpath[1]}/_amq-squad"
amq-squad completion fish > ~/.config/fish/completions/amq-squad.fish
```

`completion zsh` is followed by a `compinit` step on most shells.

## Trust and binary defaults

Generated launch commands include these per-binary defaults:

- **Claude:** `--permission-mode auto`
- **Codex:** nothing by default (sandboxed). Pass `--trust trusted` to prepend `--dangerously-bypass-approvals-and-sandbox` for the local power-user profile.

`--trust trusted` is persisted in the team profile by `team init` and is re-emitted by `up`, `agent up`, `resume`, and `fork`. Combining `--trust trusted` with `--no-default-args` is rejected, as is sandboxed mode with the bypass flag smuggled through `--codex-args`.

Pass `--model NAME` to set the native `--model` flag on Codex or Claude. `team init --model role=model,...` persists per-member models.

```sh
amq-squad team init --personas cto,fullstack --trust trusted
amq-squad team init --personas cto,fullstack --model cto=gpt-5,fullstack=sonnet
amq-squad agent up codex --model gpt-5
```

## Workstreams and threads

- **Workstream** = AMQ `--session`. All members in one team run share it.
- AMQ session names are strict: lowercase `a-z`, digits, `-`, `_`. Use `v0-5-0`, not `v0.5.0`.
- **Threads** are focused conversations inside a workstream. Canonical p2p threads use sorted handles (`p2p/cto__fullstack`); decisions go under `decision/<topic>`.

Active workstream resolution: explicit `--session` > stored `team.Workstream` > sanitized team-home directory name.

## Cross-project teams

Members do not have to share a working directory. The dir where you run `team init` becomes the team-home; individual members can live in other repos.

```sh
cd ~/Code/project-a
amq-squad team init --personas cto,fullstack,qa --cwd qa=~/Code/project-b
```

`up --dry-run` emits a `cd <member-cwd>` per launch command, and `team sync --apply --allow-outside` walks each unique member cwd and writes `CLAUDE.md` / `AGENTS.md` in each one. Cwds outside the team-home need `--allow-outside` so a hand-edited `team.json` cannot write into unrelated folders by surprise.

For cross-project AMQ routing, each project's `.amqrc` needs a `peers` entry pointing at the other project's `.agent-mail/`. `team sync` does not touch `.amqrc`; that step is manual today.

```json
{
  "root": ".agent-mail",
  "project": "project-a",
  "peers": {
    "project-b": "/Users/you/Code/project-b/.agent-mail"
  }
}
```

## Messaging inside a squad

Once launched, agents use plain AMQ commands. `amq-squad` injects a routing block into each agent's bootstrap prompt with the live roster, handles, threads, and per-agent project context.

```sh
amq list --new
amq read --id <id>
amq drain --include-body

amq send \
  --to fullstack \
  --thread p2p/cto__fullstack \
  --kind review_request \
  --subject "Review: PR" \
  --body "Please review tests and framing." \
  --wait-for drained --wait-timeout 60s
```

`amq send` reads stdin when `--body` is omitted. There is no `--body-file` flag.

## Files amq-squad writes

```text
<project>/.amq-squad/team.json           Default team profile (schema: 2).
<project>/.amq-squad/teams/<name>.json   Named team profiles (schema: 2).
<project>/.amq-squad/team-rules.md       Durable team norms (user-edited).
<project>/.amq-squad/briefs/<session>.md Workstream brief, one per AMQ session.
<project>/CLAUDE.md, AGENTS.md           Managed pointer block; user content outside markers preserved.
<AM_ROOT>/agents/<handle>/extensions/io.github.omriariav.amq-squad/
                                         Per-agent launch.json and role.md inside the
                                         AMQ mailbox. Legacy direct-agent records
                                         remain readable.
```

`<AM_ROOT>` is resolved via AMQ's JSON env contract (`amq env --json`).

## Deprecated 1.x verbs

These verbs still work through the 1.x line and emit a one-line `stderr` deprecation warning. They will be removed in 2.0. Do not use them in new flows.

| Legacy | Modern equivalent |
| --- | --- |
| `amq-squad team show` | `amq-squad up --dry-run` |
| `amq-squad team launch` | `amq-squad up` |
| `amq-squad team launch --fresh --session X` | `amq-squad fork --from <current> --as X` |
| `amq-squad list` | `amq-squad status` (live) or `amq-squad history` (records) |
| `amq-squad launch <binary>` | `amq-squad agent up <binary>` |
| `amq-squad restore` (print) | `amq-squad history` |
| `amq-squad restore --exec --role R` | `amq-squad agent resume R` |

Help paths on deprecated verbs do not emit the warning. JSON callers on deprecated verbs still get pure JSON on stdout; the warning goes to stderr.

Legacy print/replay paths that emit copy-paste commands use the modern `agent up <binary>` command shape. The legacy verb invocation is deprecated, not the generated replay command.

## Known gaps

- True cross-workstream sends from a setup terminal still depend on upstream AMQ semantics tracked in [avivsinai/agent-message-queue#96](https://github.com/avivsinai/agent-message-queue/issues/96). The normal team flow avoids that path by launching one workstream and routing peer conversations with `--thread`.
- Multi-cwd teams need manual `peers` config in each project's `.amqrc` for cross-project AMQ routing. `team sync` does not touch `.amqrc`.

## Requires

- Go 1.25+
- `amq` binary on `PATH` (v0.34+)
- `tmux` on `PATH` for `amq-squad up`
