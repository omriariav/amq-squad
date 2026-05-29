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

2.0 moved the module to a `/v2` import path (Go semantic import versioning). Install the v2 line with the `/v2/` segment:

```sh
go install github.com/omriariav/amq-squad/v2/cmd/amq-squad@v2.0.0
amq-squad version
```

For the latest v2 build, `@latest` resolves against the `/v2` path too:

```sh
go install github.com/omriariav/amq-squad/v2/cmd/amq-squad@latest
```

Requires Go 1.25+, the `amq` binary on `PATH` (v0.34+), and `tmux` on `PATH` for `amq-squad up`.

> Upgrading from 1.x? See [Migrating from 1.x to 2.0](#migrating-from-1x-to-20). The verb model and the install path both changed.

## Quick start

```sh
cd ~/Code/my-project

amq-squad team init                  # write .amq-squad/team.json + team-rules.md
amq-squad team sync --apply          # sync pointer stub into CLAUDE.md / AGENTS.md

amq-squad up --dry-run               # preview the launch plan
amq-squad up issue-96                # NEW work: bring the team up live in tmux

amq-squad                            # bare command -> multi-session status board
amq-squad status --session issue-96  # single-session detail
amq-squad console                    # live read-only Mission Control TUI
amq-squad doctor                     # AMQ version / tmux / wake / markers

amq-squad stop --all                 # SIGTERM the team (--force = SIGKILL); stays resumable
amq-squad resume                     # re-orient / reattach the saved session
amq-squad fork --from issue-96 --as issue-96-review  # branch a fresh workstream
amq-squad rm issue-96                # the only destructive op (confirm-gated; or `archive`)
```

### The 2.0 lifecycle

A session moves through one small state machine:

```text
(none) --up--> running --stop--> stopped --rm/archive--> (none)
                  ^                  |
                  +------ resume ----+
```

- **`up [<name>]`** means NEW work. It refuses a session that already exists — use `resume` to continue it, `up --reset` to start it over, or pick a new name.
- **`stop`** is the primary teardown: SIGTERM the live agents (`--force` = SIGKILL), but PRESERVE all on-disk state so the session stays resumable. (`down` is a deprecated alias for one release.)
- **`resume`** re-orients a stopped session. If an agent has a saved conversation, amq-squad reattaches it; otherwise it re-runs bootstrap so the agent re-reads its brief and AMQ history. It does NOT replay prior hidden reasoning.
- **`rm` / `archive`** are the only destructive ops. Both are confirm-gated (`--yes` to skip the prompt) and refuse a session with live agents unless `--force`. `rm` deletes the session root + brief; `archive` moves them aside, recoverable.
- A **restart** is just `stop` then `up` (after `rm`/`archive`) or `resume` for the same session.

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

`up` is for NEW work and refuses a session that already exists. To continue an
existing session use `amq-squad resume`; to start it over use `amq-squad up
--reset` (destructive: it tears down and removes the session first, with a
confirmation prompt unless `--yes`). Add `--force-duplicate` only when stale
live-agent signals remain and you deliberately want a second agent alongside.

Single-agent primitives:

```sh
amq-squad agent up codex --role cto --session issue-96
amq-squad agent resume fullstack
```

The old 1.x verbs (`launch`, `restore`, `list`, `team show`, `team launch`) were **removed in 2.0**; each prints a one-line migration hint pointing at its replacement. See [Removed in 2.0](#removed-in-20) and [Migrating from 1.x to 2.0](#migrating-from-1x-to-20).

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

The context model is three durable layers. Each layer has exactly one source of truth.

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

amq-squad up [<session>] [--profile NAME] [--session ws] [--reset [--yes] [--force]]
             [--dry-run] [--json] [--seed-from file:|issue:|gh: [--force]]
             [--terminal tmux] [--target current-window|new-session]
             [--layout vertical|horizontal|tiled] [--terminal-session name]
             [--stagger 750ms] [--no-bootstrap] [--force-duplicate]
                                  NEW work. Bring the configured team up live (tmux) or
                                  print the plan with --dry-run. REFUSES a session that
                                  already exists (use `resume`, or `up --reset` to start
                                  over). The name comes from the <session> positional or
                                  --session (both is an error); inferred otherwise. With
                                  no --seed-from/--brief the brief is AUTO-STUBBED (with a
                                  one-line notice) so CI/send-keys flows keep working.
amq-squad stop [--all | --role R] [--force]
                                  Stop members: SIGTERM the live, binary-matched
                                  agent PID (--force = SIGKILL), reap the wake
                                  sidecar, flip presence offline. On-disk state is
                                  preserved, so the session stays resumable.
                                  ('down' is a deprecated alias for one release.)
amq-squad resume [--profile NAME] [--session ws] [--restore-existing]
                 [--exec] [--dry-run] [--force-duplicate]
                 [--no-bootstrap] [--trust sandboxed|trusted]
                 [--model role=model,...]
                 [--codex-args args] [--claude-args args]
                                  Re-orient an existing session. Reattaches a saved
                                  conversation if present; otherwise re-runs bootstrap so
                                  the agent re-reads its brief + AMQ history (it does NOT
                                  replay prior hidden reasoning). Plan-only by default;
                                  classifies each member as live / restore / launch fresh
                                  / blocked and prints copy-pasteable commands. With
                                  --exec it opens them through the terminal backend, like
                                  `up`. Use `fork --from <current> --as <new>` for a NEW
                                  workstream.
amq-squad fork --from <current> --as <new> [--force-duplicate]
                                  Plan fresh launches in a new workstream branched off
                                  the current one. Plan-only; does not copy launch
                                  records, briefs, conversations, or team.json. The
                                  workstream brief at .amq-squad/briefs/<new>.md is
                                  created or preserved by the subsequent
                                  `up --session <new>` (or `agent up`) live launch.
amq-squad rm <session> [--yes] [--force]
                                  Permanently remove a finished session (its AMQ root dir
                                  + brief). The only destructive verb. Previews + prompts
                                  (default No) unless --yes; refuses a live session unless
                                  --force; never touches a sibling session.
amq-squad archive <session> [--yes] [--force]
                                  Move a finished session aside instead of deleting it
                                  (to <baseRoot>/.archive/<session>/, recoverable).
                                  Confirm-gated; refuses a live session unless --force.
amq-squad status [--json]         Multi-session BOARD over every discovered session
amq-squad status --session NAME [--json]
                                  (rolled-up state, agent health, brief, last-activity).
                                  With --session: the single-session detail table. The
                                  bare `amq-squad` runs the board too.
amq-squad console [--session NAME] [--refresh 2s] [--at-risk-wait 5m]
                  [--review-age 15m] [--once]
                                  Live read-only Mission Control TUI over all sessions:
                                  board / detail / collapsed-thread bus / peek, and a
                                  triage rollup (needs-you · at-risk · blocked). Renders
                                  to /dev/tty (stdout stays clean). --once prints a single
                                  static board to stdout for CI / non-TTY.
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

## Status board and Mission Control console

`amq-squad status` (and the bare `amq-squad`) prints a **multi-session board** over every discovered session — docker-ps / `git branch -v` style: session name, rolled-up state (running / stopped / degraded), agent health (N/M alive + at-risk), a one-line brief, and last-activity. Add `--session NAME` for the single-session detail table.

`amq-squad console` is a full-screen, **read-only Mission Control TUI** over the same sessions. It never launches, stops, or mutates anything — it only observes.

```sh
amq-squad console                    # interactive TUI on /dev/tty
amq-squad console --once             # single static board to stdout (CI / no TTY)
amq-squad console --session issue-96 --at-risk-wait 5m
```

The console gives you:

- a **board** of all sessions, grouped attention-first (needs-you → blocked → at-risk → running → stopped),
- per-session **detail** with each agent's liveness and a **collapsed-thread bus** ("qa ↔ cto  blocked · subject  N msgs · 7m"),
- **peek** (`space`) for a read-only view of an agent's recent output, unread inbox, and what it is blocked on,
- a **triage rollup** headline (`N needs-you threads · N at-risk threads · N blocked threads`) and `/`-filters (`needs-you`, `at-risk`, `blocked`, `unread`, `agent:<h>`, `model:<m>`, `session:<n>`).

It renders to `/dev/tty`, so `stdout` stays clean for the other verbs. With `--once` it emits one static board to stdout and exits — use this when there is no terminal attached.

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
| `3` | partial success (some targets succeeded, some failed; e.g. `stop` with mixed stopped + failed) |

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

Session (workstream) resolution: the `<session>` positional or `--session` > inference from team members and the sanitized team-home directory name. The pinned `team.json` `workstream` default was **dropped in 2.0** — a profile that still carries it gets a one-line deprecation warning (removal in 2.1); pass `--session` (or the positional) or rely on inference instead.

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

## Removed in 2.0

The 1.x top-level verbs below were **removed in 2.0**. Each is still recognized for one release as an explicit pointer: running it prints a one-line `stderr` migration hint (not an "unknown command") and exits with a usage error. Use the replacement.

| Removed verb | Replacement |
| --- | --- |
| `amq-squad launch <binary>` | `amq-squad agent up <binary>` |
| `amq-squad restore` (print) | `amq-squad history` |
| `amq-squad restore --exec --role R` | `amq-squad agent resume R` |
| `amq-squad list` | `amq-squad status` (live) or `amq-squad history` (records) |
| `amq-squad team show` | `amq-squad up --dry-run` |
| `amq-squad team launch` | `amq-squad up` |
| `amq-squad team launch --fresh --session X` | `amq-squad fork --from <current> --as X` |

`down` is **deprecated** (not removed): it is an alias for `stop` that keeps working for one release and runs the identical logic. Prefer `stop`.

Replay paths that emit copy-paste commands use the modern `agent up <binary>` command shape.

## Migrating from 1.x to 2.0

2.0 is a breaking release. The major changes and what to do:

| 1.x | 2.0 | Migration |
| --- | --- | --- |
| Module `github.com/omriariav/amq-squad/...` | `/v2` import path | `go install github.com/omriariav/amq-squad/v2/cmd/amq-squad@v2.0.0` |
| Pinned `team.json` `workstream` default | dropped (deprecation shim) | Pass `--session <name>` (or the `up <name>` positional), or rely on inference. A pinned profile still works but warns (removed in 2.1). |
| `up` (re)used an existing session | `up` is NEW work and **refuses** an existing session | Use `amq-squad resume` to continue, or `amq-squad up --reset` to start over. |
| `down` | `stop` (primary) | Use `amq-squad stop`. `down` still works for one release as a deprecated alias. |
| `up` required a brief or `--fresh` juggling | brief **auto-stubs** when no `--seed-from`/`--brief` | Nothing — `up` writes a stub brief and prints a one-line notice; edit it or pass `--seed-from`. |
| `launch <binary>` | removed | `amq-squad agent up <binary>` |
| `restore` (print) / `restore --exec --role R` | removed | `amq-squad history` / `amq-squad agent resume R` |
| `list` | removed | `amq-squad status` (live) or `amq-squad history` (records) |
| `team show` / `team launch` | removed | `amq-squad up --dry-run` / `amq-squad up` |
| — | new: `amq-squad console` | Read-only Mission Control TUI; the bare `amq-squad` now shows a multi-session board. |

Each removed verb prints a migration hint when invoked, so muscle-memory commands get a pointer rather than a crash. JSON callers on the deprecated `down` alias still get pure JSON on stdout; the warning goes to stderr.

## Known gaps

- True cross-workstream sends from a setup terminal still depend on upstream AMQ semantics tracked in [avivsinai/agent-message-queue#96](https://github.com/avivsinai/agent-message-queue/issues/96). The normal team flow avoids that path by launching one workstream and routing peer conversations with `--thread`.
- Multi-cwd teams need manual `peers` config in each project's `.amqrc` for cross-project AMQ routing. `team sync` does not touch `.amqrc`.

## Requires

- Go 1.25+
- `amq` binary on `PATH` (v0.34+)
- `tmux` on `PATH` for `amq-squad up`
