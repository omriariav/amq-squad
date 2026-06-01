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

amq-squad roles                      # list role IDs and market numbers
amq-squad new team --dry-run --roles cto,qa
amq-squad new team --dry-run --json --roles cto,qa
amq-squad new team --sync --session issue-96
amq-squad new profile review --roles cto,qa --sync --session review

amq-squad up --dry-run               # preview the launch plan
amq-squad up --project ~/Code/other-app --dry-run
amq-squad new session issue-96       # NEW work: bring the team up live in tmux
amq-squad new session issue-98 --seed-from issue:31
amq-squad new session --project ~/Code/other-app issue-97

amq-squad                            # bare command -> multi-session status board
amq-squad status --session issue-96  # single-session detail
amq-squad status --project ~/Code/other-app --session issue-97
amq-squad console                    # live read-only Mission Control TUI
amq-squad console --project ~/Code/other-app --once
amq-squad noc --json | jq .          # machine-readable multi-root NOC snapshot
amq-squad doctor                     # AMQ version / tmux / wake / pointer sync
amq-squad doctor --project ~/Code/other-app --profile release
amq-squad doctor --project ~/Code/other-app --all-profiles

amq-squad stop --all                 # SIGTERM the team (--force = SIGKILL); stays resumable
amq-squad stop --project ~/Code/other-app --all --session issue-97
amq-squad resume                     # re-orient / reattach the saved session
amq-squad resume --project ~/Code/other-app --session issue-97
amq-squad fork --from issue-96 --as issue-96-review  # branch a fresh workstream
amq-squad fork --project ~/Code/other-app --from issue-96 --as issue-96-review
amq-squad rm issue-96                # remove a finished session (confirm-gated; or `archive`)
```

### The 2.0 lifecycle

A session moves through one small state machine:

```text
(none) --up--> running --stop--> stopped --rm/archive--> (none)
                  ^                  |
                  +------ resume ----+
```

- **`up [<name>]`** means NEW work. It refuses a session that already exists — use `resume` to continue it, `up --reset` to start it over, or pick a new name.
- **`new session [<name>]`** is the create-focused alias for `up [<name>]`. It follows the same NEW-work refusal rules.
- **`new team`** is the create-focused alias for `team init`.
- **`new profile NAME`** is the named-profile alias for `team init --profile NAME`.
- **`stop`** is the primary teardown: SIGTERM the live agents (`--force` = SIGKILL), but PRESERVE all on-disk state so the session stays resumable. (`down` is a deprecated alias for one release.)
- **`resume`** re-orients a stopped session. If an agent has a saved conversation, amq-squad reattaches it; otherwise it re-runs bootstrap so the agent re-reads its brief and AMQ history. It does NOT replay prior hidden reasoning.
- **`rm` / `archive`** are the session-destructive ops. Both are confirm-gated (`--yes` to skip the prompt) and refuse a session with live agents unless `--force`. `rm` deletes the session root + brief; `archive` moves them aside, recoverable. `team rm` is separate: it removes one team profile config only.
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
amq-squad new session issue-96 --seed-from issue:31    # create-focused spelling
amq-squad up --seed-from issue:31 --force              # overwrite an existing brief
amq-squad up                                           # preserve existing brief
amq-squad brief --session issue-96                     # read the current brief
amq-squad brief seed --session issue-96 --seed-from issue:31
```

`--seed-from` semantics:

- With `--dry-run`: prints the candidate brief envelope and writes nothing.
- Without `--dry-run`: writes `.amq-squad/briefs/<session>.md` and brings the team up in the same call. An existing brief is preserved unless `--force` is set.

### Profiles (schema 2)

Profiles let one team-home hold parallel team shapes (for example a release team and a research team).

```sh
amq-squad new team --session issue-96        # default profile -> .amq-squad/team.json
amq-squad new profile release --session qa   # named profile -> .amq-squad/teams/release.json
amq-squad team profiles                      # list configured profiles
amq-squad team profiles --project ~/Code/app # list another team-home
amq-squad team profiles --json
amq-squad team rm --profile release          # delete one profile config only
amq-squad up --profile release               # operate on a named profile
amq-squad doctor --profile release           # check that profile's config, wake, and pointer sync
```

`team.json` files use `schema: 2` (the JSON key in persisted team profiles is `schema`; `schema_version` is reserved for the read-only JSON command envelopes documented below). Omit `--profile` (or pass `--profile default`) for the default profile.

## Verbs

Team-level verbs:

```text
amq-squad new team [--project DIR] [--sync] [--dry-run [--json]] [team init options]
                                  Create the default team profile. Alias for
                                  team init.
                                  --dry-run previews the profile, rules path,
                                  workstream, trust, and member roster without
                                  writing files. Add --json for a
                                  team_profile_plan envelope.
                                  Add --sync to also write CLAUDE.md / AGENTS.md
                                  managed pointer stubs. --roles accepts IDs,
                                  market numbers, or all; --session sets the
                                  initial shared workstream;
                                  --project targets a team-home without cd.
amq-squad new profile NAME [--project DIR] [--sync] [--dry-run [--json]] [team init options]
                                  Create a named team profile. Alias for
                                  team init --profile NAME. Supports --sync,
                                  --roles IDs, market numbers, all, and
                                  role=binary overrides, plus --session for
                                  the initial shared workstream.
amq-squad roles [--json]
                                  List built-in role IDs, market numbers, default
                                  CLIs, and short profile copy for team creation.
amq-squad new session [--project DIR] [<session>] [up options]
                                  Create NEW work. Alias for up, with the same
                                  refusal when the session already exists.
                                  Supports --profile and --seed-from for named
                                  profiles and seeded briefs. --project targets
                                  a team-home without cd.

amq-squad team init [--project DIR] [--profile NAME] [--roles a,b|numbers|all] [--binary role=bin,...]
                     [--session ws] [--trust sandboxed|trusted]
                     [--model role=model,...] [--codex-args ...] [--claude-args ...] [--dry-run [--json]]
                                  Write a team profile and seed .amq-squad/team-rules.md.
                                  --dry-run builds and prints the profile plan
                                  without writing team.json or team-rules.md.
                                  Add --json for a team_profile_plan envelope.
                                  --project targets a team-home without cd.
amq-squad team rules init [--project DIR] [--force]
                                  Seed or refresh .amq-squad/team-rules.md.
amq-squad team rules show [--project DIR]
                                  Print .amq-squad/team-rules.md.
amq-squad team sync [--project DIR] [--apply] [--allow-outside]
                                  Sync the pointer stub into CLAUDE.md and AGENTS.md.
                                  Exit 1 on drift when --apply is not set.
amq-squad team profiles [--project DIR] [--json]
                                  List configured profiles (default + named).
amq-squad team rm [PROFILE] [--project DIR] [--profile NAME] [--dry-run] [--yes|-y]
                                  Delete one team profile config. Prompts by
                                  default and does not delete AMQ sessions,
                                  briefs, team-rules.md, or pointer stubs.

amq-squad up [<session>] [--project DIR] [--profile NAME] [--session ws] [--reset [--yes] [--force]]
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
                                  --project targets a team-home without cd.
amq-squad stop [--all | --role R] [--project DIR] [--force]
                                  Stop members: SIGTERM the live, binary-matched
                                  agent PID (--force = SIGKILL), reap the wake
                                  sidecar, flip presence offline. On-disk state is
                                  preserved, so the session stays resumable.
                                  --project targets a team-home without cd.
                                  ('down' is a deprecated alias for one release.)
amq-squad resume [--project DIR] [--profile NAME] [--session ws] [--restore-existing]
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
                                  `up`. --project targets a team-home without cd. Use
                                  `fork --from <current> --as <new>` for a NEW workstream.
amq-squad fork --from <current> --as <new> [--project DIR] [--force-duplicate]
                                  Plan fresh launches in a new workstream branched off
                                  the current one. Plan-only; does not copy launch
                                  records, briefs, conversations, or team.json. The
                                  workstream brief at .amq-squad/briefs/<new>.md is
                                  created or preserved by the subsequent
                                  `up --session <new>` (or `agent up`) live launch.
                                  --project targets a team-home without cd.
amq-squad rm <session> [--project DIR] [--yes] [--force]
                                  Permanently remove a finished session (its AMQ root dir
                                  + brief). Previews + prompts
                                  (default No) unless --yes; refuses a live session unless
                                  --force; never touches a sibling session. --project
                                  targets a team-home without cd.
amq-squad archive <session> [--project DIR] [--yes] [--force]
                                  Move a finished session aside instead of deleting it
                                  (to <baseRoot>/.archive/<session>/, recoverable).
                                  Confirm-gated; refuses a live session unless --force.
                                  --project targets a team-home without cd.
amq-squad status [--project DIR] [--json]
                                  Multi-session BOARD over every discovered session
amq-squad status --session NAME [--project DIR] [--json]
                                  (rolled-up state, agent health, brief, last-activity).
                                  With --session: the single-session detail table. The
                                  bare `amq-squad` runs the board too. --project targets
                                  a team-home without cd.
amq-squad brief --session NAME [--project DIR] [--json]
                                  Print the full workstream brief and classify it as
                                  missing, stub, or real. --project targets a team-home
                                  without cd.
amq-squad brief seed --session NAME --seed-from REF [--project DIR] [--force]
                                  Write a workstream brief from file:<path>,
                                  issue:<n>, or gh:owner/repo#<n> without
                                  launching the team. Use --dry-run to preview.
amq-squad noc [--root DIR ...] [--filter EXPR] [--once] [--tree] [--hide-stale] [--json]
                                  Multi-root NOC over discovered squads and
                                  candidate git team-homes. In the live TUI,
                                  control keys preview and confirm before they
                                  mutate: T creates a team profile (roles accept
                                  IDs, market numbers, all, and role=binary,
                                  and writes pointer stubs via --sync),
                                  N chooses a profile when needed and starts a
                                  new detached workstream
                                  session (rejecting existing names, including
                                  empty AMQ session dirs, before preview),
                                  S/R/X stop/resume/restart (asking for profile
                                  on mixed sessions), i lists unread inboxes
                                  read-only, and v/d/a/r/x/m/b
                                  read/drain/approve/reply/deny/message/broadcast via AMQ. The
                                  p palette finds project/action rows too, so
                                  new-team/new-profile/new-session are one fuzzy
                                  search away, including create team and start
                                  session aliases. Session action rows include
                                  brief for the full workstream brief and
                                  brief-seed to write one after confirmation.
                                  Add --json for a one-shot noc_snapshot
                                  envelope for automation.
amq-squad console [--project DIR] [--session NAME] [--refresh 2s] [--at-risk-wait 5m]
                  [--review-age 15m] [--once]
                                  Mission Control TUI over this project. Renders
                                  to /dev/tty (stdout stays clean). --once prints
                                  a single static board to stdout for CI / non-TTY.
                                  --project targets a team-home without cd.
amq-squad history [--json] [--project a,b]
                                  Restorable launch records across known projects.
amq-squad doctor [--project DIR] [--profile NAME|--all-profiles] [--json]
                                  AMQ version, profile config, tmux, wake,
                                  marker integrity, and pointer-sync drift.
amq-squad version [--json]        Print the installed amq-squad version.
amq-squad completion <bash|zsh|fish>
                                  Print a shell completion script to stdout.
```

Single-agent primitives:

```text
amq-squad agent up <binary> [--project DIR] [--role R] [--session ws] [--team-profile NAME]
                            [--conversation ref] [--no-bootstrap]
                            [--trust sandboxed|trusted] [--model NAME]
                            [--codex-args ...] [--claude-args ...]
                            [--force-duplicate] [-- <native flags>]
                                  Launch one agent. Writes launch.json + role.md
                                  in the AMQ mailbox, injects bootstrap, then execs
                                  amq coop exec. --project targets a team-home
                                  without cd.
amq-squad agent resume <role> [--project a,b]
                                  Replay one saved launch record.
```

For `agent up`, recognized launcher flags after `<binary>` (such as `--role`, `--session`, `--trust`, `--model`, `--codex-args`, `--claude-args`, `--help`) keep flowing into the launcher; unrecognized flags and the first non-flag positional are treated as child args. Use `--` for an explicit child boundary. `amq-squad agent up codex --help` prints launcher help; `amq-squad agent up codex -- --help` passes native help to the child. `--codex-args` and `--claude-args` accept dash-prefixed values such as `--codex-args '--enable goals'`.

Global output flags work before or after the subcommand: `--quiet`, `--verbose`, `--color auto|always|never`. `NO_COLOR` overrides `--color=always`. `--quiet` and `--verbose` are mutually exclusive. Deprecation warnings survive `--quiet`.

## Status board and Mission Control console

`amq-squad status` (and the bare `amq-squad`) prints a **multi-session board** over every discovered session — docker-ps / `git branch -v` style: session name, rolled-up state (running / stopped / degraded), agent health (N/M alive + at-risk), a one-line brief, and last-activity. Add `--session NAME` for the single-session detail table.

`amq-squad console` is the project-scoped Mission Control TUI. `amq-squad noc` is the multi-root command center for discovered squads and candidate git team-homes. Discovery includes `.agent-mail/`, `.amq-squad/team.json`, and `.git` markers. NOC control keys are deliberate: every mutating action opens a preview/confirm overlay first. `T` creates a team profile for the selected team-home (roles accept IDs, market numbers, `all`, `role=binary` overrides, and optional `session=issue-96`) and includes `--sync` so the managed `CLAUDE.md` / `AGENTS.md` pointer stubs are written too, `N` chooses a profile when needed and starts a new detached workstream session, rejecting existing names and empty AMQ session dirs before preview, `S` / `R` / `X` stop, resume, and restart, `c` opens the selected needs-you thread transcript read-only, `D` lists a selected agent's DLQ read-only, `i` lists a selected agent's unread inbox read-only, and `v` / `d` / `a` / `r` / `x` / `m` / `b` act through AMQ. Press `p` for a command palette over projects, actions, teams, and agents; action rows such as `project/action/status`, `project/action/amq-env`, `project/action/amq-who`, `project/action/history`, `project/action/resume-plan`, `project/action/team-rules`, `project/action/new-team`, `project/action/new-profile`, `project/action/new-session`, `project/action/delete-team`, and `project/action/sync-pointers` open read-only status plus preview-gated creation, deletion, and repair flows, `project/action/doctor` opens all-profile project health, `project/action/amq-env` shows AMQ root, project, and peer routing JSON, `project/action/amq-who` lists AMQ sessions and agents, `project/action/history` opens restorable launch records, `project/action/resume-plan` opens the per-member recovery plan, `project/action/team-rules` shows durable `.amq-squad/team-rules.md`, `project/action/roles` opens the role market, `project/action/team-profiles` lists configured profiles, session/action rows expose status, fork-plan, stop, resume, restart, presence, in-NOC thread context, read-needs-you, reply, approve, deny, broadcast, AMQ ops, AMQ cleanup, archive, and remove, agent/action rows expose in-NOC thread context, read-needs-you, reply, approve, deny, DLQ, DLQ read, DLQ retry, DLQ purge, DLQ retry-all, receipts, receipts wait, inbox, message, message wait, drain, and single-agent resume flows, and aliases like `doctor`, `create team`, `delete team`, `sync pointers`, `role market`, `team rules`, `team profiles`, `history`, `resume plan`, `fork plan`, `amq env`, `amq who`, `project status`, `session status`, `presence`, `stop session`, `resume session`, `restart session`, `start session`, `context`, `read needs-you`, `reply`, `approve`, `deny`, `broadcast`, `dlq`, `read DLQ`, `retry DLQ`, `purge DLQ`, `retry all DLQ`, `receipts`, `wait receipts`, `inbox`, `message`, `wait message`, `drain`, `resume agent`, `archive session`, `remove session`, `amq cleanup`, or `amq ops` find those rows.

The live `N` editor and palette new-session action also accept an inline brief seed, for example `issue-97 seed-from=issue:31`, before opening the confirm preview.

```sh
amq-squad console                    # interactive TUI on /dev/tty
amq-squad console --once             # single static board to stdout (CI / no TTY)
amq-squad console --project ~/Code/app --once
amq-squad console --session issue-96 --at-risk-wait 5m
amq-squad console --filter needs-you
amq-squad console --root ~/Code --filter needs-you --json
amq-squad noc --root ~/Code          # multi-project NOC
amq-squad noc --filter needs-you
amq-squad noc --filter gated
amq-squad noc --filter stale-blocked
amq-squad noc --json | jq .          # machine-readable snapshot
```

The console gives you:

- a **board** of all sessions, grouped attention-first (needs-you > blocked > gated > at-risk > running > stopped),
- per-session **detail** with each agent's liveness and a **collapsed-thread bus** ("qa ↔ cto  blocked · subject  N msgs · 7m"),
- **peek** (`space`) for a read-only view of an agent's recent output, unread inbox, and what it is blocked on,
- a **triage rollup** headline (`N needs-you threads · N blocked threads · N gated threads · N at-risk threads`) and `/`-filters (`needs-you`, `needs-user`, `gated`, `at-risk`, `blocked`, `stale-blocked`, `unread`, `agent:<h>`, `model:<m>`, `session:<n>`).

It renders to `/dev/tty`, so `stdout` stays clean for the other verbs. With `--once` it emits one static board to stdout and exits — use this when there is no terminal attached.

## JSON envelopes

Verbs that produce machine-readable output accept `--json` and emit a schema-versioned envelope on stdout. Diagnostics stay on stderr; stdout under `--json` is pure JSON.

```sh
amq-squad status --json | jq .
amq-squad history --json | jq .
amq-squad doctor --json | jq .
amq-squad team profiles --json | jq .
amq-squad roles --json | jq .
amq-squad team init --dry-run --json --roles cto,qa | jq .
amq-squad new team --sync --dry-run --json --roles cto,qa | jq .
amq-squad up --dry-run --json | jq .
amq-squad noc --json --filter needs-you | jq .
amq-squad noc --actions --filter needs-you
amq-squad noc --actions --filter needs-you --action thread_context,read_needs_you,reply,approve,deny
amq-squad noc --actions --action resume --mutating
amq-squad noc --actions --action message,broadcast
amq-squad noc --filter session:issue-96 --run-action amq_cleanup --set tmp-older-than=36h --yes
amq-squad noc --actions --action dlq --commands
amq-squad noc --actions --action team_rules --commands
amq-squad noc --actions --action amq_env --commands
amq-squad noc --actions --action amq_who --commands
amq-squad noc --actions --action presence --commands
amq-squad noc --filter agent:cto --run-action dlq_retry --set dlq-id=dlq_123 --yes
amq-squad noc --filter agent:cto --run-action dlq_purge --set older-than=168h --yes
amq-squad noc --actions --action receipts --commands
amq-squad noc --filter agent:cto --run-action receipts_wait --set msg-id=msg_123 --set stage=drained --set timeout=60s
amq-squad noc --actions --action inbox --commands
amq-squad noc --filter agent:cto --run-action message_wait --set body='Please check status' --set timeout=60s --yes
amq-squad noc --actions --target-id 'session|/repo/app|issue-96' --scope session
amq-squad noc --actions --action archive,remove --commands
amq-squad noc --actions --action resume --commands
amq-squad noc --run-action sync_pointers --set profile=review --set allow-outside=true --yes
amq-squad noc --actions --json --filter needs-you | jq .
amq-squad noc --filter project:app --run-action new_session --set session=issue-97 --dry-run --json
amq-squad noc --filter project:app --run-action new_session --set session=issue-97 --set seed-from=issue:31 --yes
amq-squad noc --run-action 'project|/repo/app|action|new_team' --set roles=cto,qa --set session=issue-96 --yes
amq-squad noc --run-action 'agent|/repo/app|issue-96|cto|action|message' --set body='Please check status' --yes
amq-squad version --json | jq .
```

`noc --json` is the automation-oriented command-center snapshot. Each project,
session, and agent row has a stable `id` and can include an `actions` array with
read-only commands and confirm-required mutating commands such as `history`, `resume_plan`, `fork_plan`, `brief`, `brief_seed`, `new_team`,
`new_session`, `delete_team`, `sync_pointers`, `roles`, `team_rules`, `amq_env`, `amq_who`, `amq_ops`, `amq_cleanup`, `presence`, `thread_context`, `read_needs_you`, `reply`, `approve`, `deny`,
`dlq`, `dlq_read`, `dlq_retry`, `dlq_retry_all`, `dlq_purge`, `receipts`, `receipts_wait`, `inbox`, `message`, `message_wait`, `broadcast`, `drain`, `resume`, `stop`, `restart`, `archive`, `remove`, and `agent_resume`. Each
action has its own `id`, `scope`, and `target_id`. Project rows expose
`base_root` when an AMQ session store exists. The snapshot also includes a
top-level flat `actions` index plus `action_count` and `mutating_action_count`,
already scoped by `--filter` and `--hide-stale`. Team/create-capable project
rows include a read-only `roles` action so the role market is discoverable from
the same queue. Configured team project rows include read-only `team_rules` for `.amq-squad/team-rules.md`. Project rows with AMQ sessions include read-only `amq_env` for AMQ root, project, and peer routing JSON, plus `amq_who` for AMQ session and agent inventory. Session rows include read-only `brief`, confirm-required `brief_seed`, `presence` for AMQ `presence list`, `amq_ops` for AMQ `doctor --ops`, and `amq_cleanup` for stale AMQ tmp-file cleanup;
session rows also include `thread_count`, `threads_returned`, and a capped
`threads` array with collapsed thread IDs, subjects, triage, status, latest
message IDs, and message counts, so automation can decide when to call
`amq-squad thread` for the full transcript;
needs-you session and agent rows include read-only `thread_context` plus
confirm-required `read_needs_you`, `reply`, `approve`, and `deny` actions for
the top human-needed thread.
`read_needs_you` uses `amq read --json`, so it moves unread mail to `cur` like AMQ read does.
Session rows also include confirm-required `broadcast` actions, and agent rows
include read-only `dlq`, `receipts`, `receipts_wait`, and `inbox`, confirm-required `dlq_read`, `dlq_retry`,
`dlq_retry_all`, `dlq_purge`, `message`, `message_wait`, and `drain` over that agent's AMQ mailbox.
`dlq_read` marks the DLQ item inspected, `dlq_retry` moves the original message
back to the agent inbox, `dlq_retry_all` retries all new DLQ items for that
agent, and `dlq_purge` requires an `older-than` duration and passes `--yes` to
AMQ only after the NOC action itself has been confirmed. `receipts` lists AMQ
delivery receipts for the agent, `receipts_wait` waits for a specific `msg-id`
and receipt `stage`, and `message_wait` sends a direct message with
`--wait-for drained` plus a required `timeout` value. Template actions carry placeholders like `<roles>` or
`<session>`, set `"template": true`, and include a `vars`
array that names required values, optional values, derived values such as
`tmux-session` from `session`, and `choices` when valid profile options are
known. Open-ended values include `examples`, for instance role selections like
`cto,qa`, `2,9`, `all`, or `cto=codex,qa`.
When a variable includes `choices`, `--run-action --set` accepts only those
values.
Use `noc --actions` when you only need the flat action queue; add `--json` for a
`noc_actions` envelope. The human table includes a `VARS` column for required
and derived template values, including known choices and compact examples for
open-ended required values. Add `--action doctor,history,resume_plan,fork_plan,status,amq_env,amq_who,roles,team_rules,team_profiles,delete_team,threads,thread_context_any,brief,brief_seed,presence,amq_ops,amq_cleanup,stop,resume,restart,new_team,new_profile,new_session,sync_pointers,thread_context,read_needs_you,reply,approve,deny,dlq,dlq_read,dlq_retry,dlq_retry_all,dlq_purge,receipts,receipts_wait,inbox,message,message_wait,broadcast,archive,remove,agent_resume`
and/or `--mutating` to narrow that queue further. Add `--action-id`, `--target-id`, and
`--scope` when a script already has stable row or action IDs. Add `--commands`
for one selected command per line. Add `--run-action ACTION_ID_OR_NAME` to execute one
action from the queue, or pass a unique action name after narrowing with
`--filter`; mutating actions preview and prompt unless `--yes` is set. Template
actions accept `--set key=value` for placeholders such as `session`, `seed-from`, `roles`,
`profile`, `allow-outside`, `subject`, `body`, `reason`, `msg-id`, `stage`, `dlq-id`, `older-than`, `tmp-older-than`, and `timeout`; `new_team` / `new_profile` can use optional `session=<name>` for the initial workstream and also accept `role=binary` inside
`roles` or optional `binary=role=cli,...`; `--session` and `--binary` are omitted when those values
are not set. `delete_team` removes one selected team profile config and leaves AMQ sessions, briefs, team-rules.md, and pointer stubs in place. `sync_pointers` repairs managed CLAUDE.md / AGENTS.md pointer blocks for a selected profile, and requires `allow-outside=true` before writing member cwds outside the team-home. `tmux-session` is derived from `session` when possible. Unknown
`--set` keys are rejected before execution.
Add `--dry-run` to resolve and render without executing, and combine it with
`--json` for a `noc_action_plan` envelope. Creation actions run local preflight
checks for invalid session/profile names, unknown profiles, missing team-rules.md, unsafe pointer-sync target directories, invalid role
seed reference shapes, invalid role selections, invalid binary overrides, invalid DLQ IDs, invalid DLQ purge
durations, invalid AMQ cleanup ages, invalid receipt/message wait timeouts, and duplicate sessions across the selected profile's team-home and
member AMQ roots before execution.
Session cleanup actions use the existing hardened `archive` and `rm` verbs with
`--yes` inside the rendered command after the NOC confirmation gate. `archive`
moves the session root and brief to `.archive/`; `remove` permanently deletes
the session root and brief. Both still inherit the underlying liveness guard and
refuse live sessions unless the lower-level command is run with force outside the
NOC action queue.

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

`up --dry-run` emits a `cd <member-cwd>` per launch command, and `team sync --apply --allow-outside` walks each unique member cwd and writes `CLAUDE.md` / `AGENTS.md` in each one. Add `--project DIR` to sync another team-home without changing directories. Cwds outside the team-home need `--allow-outside` so a hand-edited `team.json` cannot write into unrelated folders by surprise.

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
