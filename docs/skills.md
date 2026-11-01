# Using the amq-squad skills

amq-squad ships the same three authoritative skills to the Claude Code and
Codex marketplaces: `amq-squad:wizard` for squad setup and launch,
`amq-squad:cli` for direct operations, and `amq-squad:orchestrator` for the
lead seat of a launched squad. `amq-squad`, `amq-squad-orchestrator`,
`amq-team-setup`, and `amq-squad-role-creator` are compatibility redirects only.

This guide describes the skills for amq-squad 3.0. For the verbs themselves, run
`amq-squad --help` and `amq-squad <verb> --help`; for the squad file, see
[squad.yaml](squad-yaml.md); for upgrading a v2 checkout, see
[MIGRATION.md](../MIGRATION.md) "Moving to 3.0".

- [The mental model](#the-mental-model)
- [Which skill do I reach for?](#which-skill-do-i-reach-for)
- [Installing and invoking](#installing-and-invoking)
- [`amq-squad:wizard` — set up and launch a squad](#amq-squadwizard--set-up-and-launch-a-squad)
- [`amq-squad:cli` — direct operations](#amq-squadcli--direct-operations)
- [`amq-squad:orchestrator` — lead a squad](#amq-squadorchestrator--lead-a-squad)
- [Role authoring](#role-authoring)
- [End-to-end walkthrough](#end-to-end-walkthrough)
- [Troubleshooting](#troubleshooting)

## The mental model

The three authoritative skills map onto the actor model:

| Phase | Skill | You use it... |
| --- | --- | --- |
| Setup and launch | **`amq-squad:wizard`** | readiness, roster, the squad.yaml digest flow, the brief, the launch. |
| Direct operations | **`amq-squad:cli`** | status, doctor, context, task, evidence, guard, gate status, AMQ context, verification. |
| Lead seat | **`amq-squad:orchestrator`** | planning tasks, dispatch, the commit step, widening, the merge train, gates, releases, recovery. |
| Compatibility redirects | `amq-squad`, `amq-squad-orchestrator`, `amq-team-setup`, `amq-squad-role-creator` | route old invocations to one authoritative namespaced skill. |

They sit on top of durable layers that setup creates and the squad consumes:

- **Squad** — `<repo root>/squad.yaml`, schema 1: the seats, the lead, the modes,
  and the narrowing of the owner's grant. Written only through `team propose` and
  `team apply`; a protected path no seat can write.
- **Norms** — `.amq-squad/team-rules.md`, rendered from squad.yaml.
- **Goal** — `.amq-squad/briefs/<session>.md`, written with `amq-squad brief`.
- **Role** — the seat's `role.md`, delivered at launch by `seat-exec`.
- **State** — the task store, the hook ledger, the gate store and the scheduler
  state under the AMQ session root (`<root>/extensions/amq-squad/`).

`CLAUDE.md` / `AGENTS.md` carry only a small managed **pointer stub** linking to
the norms, role and brief; they never duplicate the content.

## Which skill do I reach for?

| If you want to... | Reach for |
| --- | --- |
| Stand up a squad for a repository from a goal | `amq-squad:wizard` |
| Turn a ticket, issue or prompt into a brief | `amq-squad:wizard` |
| Change the roster of an existing squad | `amq-squad:wizard` (the same digest flow) |
| Launch the seats | `amq-squad:wizard` |
| Check status, the host, a task, the ledger or a guard verdict | `amq-squad:cli` |
| Plan tasks and drive the squad to a merged, released result as the lead | `amq-squad:orchestrator` |
| Add a role that isn't in the catalog | `amq-squad:wizard` roles step (`role draft`) |
| Debug raw AMQ outside a squad | the separate `amq-cli` skill |

Rule of thumb: set up and launch with **wizard**, operate directly with **cli**,
and enter **orchestrator** only as the lead seat of a launched squad.

## Installing and invoking

Install the marketplace once; the skills are then discovered automatically.

- **Claude Code:** `/plugin install amq-squad@amq-squad`, then invoke
  `/amq-squad:wizard`, `/amq-squad:cli`, or `/amq-squad:orchestrator`. The same
  plugin carries the seat hooks ([Claude Code seat hooks](claude-hooks.md)); install
  it at user scope.
- **Codex:** install the Codex marketplace, then invoke a skill as `$<skill>`,
  for example `$wizard`, `$cli`, or `$orchestrator` from the amq-squad plugin.

The primary skills are user-invocable and require the `amq-squad` binary on
`PATH` (`go install github.com/omriariav/amq-squad/v3/cmd/amq-squad@v3.1.0`) and
`amq` 0.116.2 or later. AMQ 0.116.2 is the minimum supported release for 3.0;
`amq-squad doctor` refuses an older `amq`, `amq-remote` or `amq-owner-sign`. The
real-AMQ lane runs at the pinned v0.116.2 (required) and at latest as a
non-blocking canary, which is not a support claim.

The workflows are equivalent across the two marketplaces; the platform metadata
differs (a Claude skill carries `trigger` / `allowed-tools` / `argument-hint`;
the Codex skill omits them).

---

## `amq-squad:wizard` — set up and launch a squad

**Invoke:** `/amq-squad:wizard` (Claude) · `$wizard` (Codex). **Use when:** the
repository has no squad yet, its roster changes, or a new workstream needs a brief
and a launch.

3.0 has no `wizard`, `init` or `start` verb. The skill walks one sequence:

1. **Readiness.** `amq-squad doctor --project <repo>` probes the AMQ version and
   launch API, the tools and their logins, the registry and the owner files, and
   writes nothing. `amq-squad trust <repo>` prints the lines the owner adds to
   `~/.codex/config.toml`; amq-squad never writes it.
2. **Roster.** `amq-squad roles` lists catalog and staged custom roles; a custom
   role is staged with `amq-squad role draft <id> --purpose "..."`. The skill asks
   for the roles rather than inferring a roster from the goal.
3. **Propose.** `amq-squad team propose --roles cto,fullstack --runtime
   fullstack=codex --lead cto --session issue-96` prints every file the squad needs
   (`squad.yaml`, `team-rules.md`, `CLAUDE.md`, `AGENTS.md`) with its sha256 and one
   digest, and writes nothing. Keys the flags do not cover go in a squad.yaml draft
   passed with `--from`.
4. **Approve and apply.** The digest is the approval surface. Only after the owner
   approved that exact digest (the grant decides in auto mode) does the skill run
   `team apply --digest <d>` with the same options. A changed option or a hand edit
   in between is refused (`squad_yaml_digest_mismatch`, `squad_yaml_changed`) and
   nothing is written.
5. **Brief.** `amq-squad brief --goal "..." --session issue-96` drafts it through
   the configured drafter (or prints the in-session prompt and stages nothing);
   `--seed-from file:<path>`, `issue:<n>` or `gh:owner/repo#<n>` seeds it.
6. **Launch.** One AMQ launch request starts the seats, each behind `seat-exec`,
   from squad.yaml (the one source): the pi lead in team mode with the scheduler
   beside it (`amq-squad lead --team` in the repository), or the mayor inside the
   fleet grant (`amq-squad operator start-squad`). Every launch carries the
   owner's standing answer to AMQ's trust confirmation.

Once the seats are up, use `amq-squad:cli` for direct inspection and route the
lead seat to `amq-squad:orchestrator`.

---

## `amq-squad:cli` — direct operations

**Invoke:** `/amq-squad:cli` (Claude) · `$cli` (Codex). **Use when:** a squad
exists and you want one explicit command and its output.

The skill relays CLI output verbatim and adds only interpretation and the next
action. Its main verbs:

| Goal | Command |
| --- | --- |
| The squad's state (status contract, schema 1) | `amq-squad status --project <repo> --session <s> --json` |
| Every squad of the fleet | `amq-squad status --fleet --json` |
| The host | `amq-squad doctor`, `amq-squad doctor --json` |
| A seat's own role, rules digest, task and gates | `amq-squad context --me <handle> --json` (inside a seat) |
| Tasks | `amq-squad task list`, `task show`, `task claim`, `task done`, `task block` |
| The evidence ledger | `amq-squad evidence list --me <handle> --root <root> --json` |
| One tool call's verdict | `amq-squad guard --me <handle> --tool Bash --arg "command=..."` (inside a seat) |
| Gate kinds and open gates | `amq-squad gate raise --list-kinds --json`, `amq-squad gate status --json` |
| Whether an action is approved | `amq-squad verify action ... --json` (0 approved, 10 pending, 11 denied, 13 unbound) |
| Merge and release evidence | `amq-squad verify merge --repository o/n --pr <n>`, `amq-squad verify release --tag <t>` |
| The AMQ context and one route | `amq-squad amq env --json`, `amq-squad amq route --to <handle> --json` |

Every store verb takes the AMQ session root (`--root`, else `AM_ROOT`) or the
project coordinates (`--project` and `--session`). Inside a seat `seat-exec` set
`AM_ME`, `AM_ROOT` and `AM_SESSION` from the launch record, so bare `amq` commands
and the hook verbs need nothing more.

Evidence is the hook ledger: in a Claude seat the PostToolUse hook runs
`evidence ingest` once per tool result and renews the claimed lease. A seat never
commits, pushes or merges; after `task done` the harness commit step commits the
seat branch and refuses any path outside the task's declared files.

---

## `amq-squad:orchestrator` — lead a squad

**Invoke:** `/amq-squad:orchestrator` (Claude) · `$orchestrator` (Codex).
**Use when:** you are the lead seat (`lead` in squad.yaml) of a launched squad.

The lead loop is one `status --json` read, one bounded action, the durable update,
then park the turn; AMQ wake delivers the next message. The status document's
`attention[]` items and `actions.new_task` carry `argv` actions to run as argument
vectors, never through a shell.

- **Plan.** `task add --title ... --class implement --files <paths> --pool
  runtime:model:effort`; the scheduler grants leases to seats of the pool and rings
  their doorbell. `dispatch --create-task` (or `--task <id>`) grants the lease and
  sends the durable message in one step.
- **Commit step.** On a seat's done report the scheduler runs `seat commit`: every
  changed path against the declared files, the protected paths and the size cap,
  then a commit under the harness identity and a fast-forward push. `--open-pr`
  binds the task to its draft PR.
- **Widen.** A refusal for a path outside the declared set comes back to the lead;
  `task widen <id> --files ... --reason ...` is the lead's recorded decision,
  applied by the scheduler only from the lead of its launch record.
- **Merge.** `merge-train --request <file> --dry-run` plans the ordered PR set
  against the owner's standing merge grant; the real run uses the owner's gh outside
  any seat. Seats never merge.
- **Gates.** Tag and release, external send, credential change and growth beyond
  the grant always stop for the owner. The lead raises the typed gate
  (`gate raise --gate <id> --action tag --target "git tag v3.0.0"`), the owner's
  signed Buzz answer opens it, and `verify action` checks it.
- **Release.** `release prepare` raises the `tag` and `github_release` gates;
  `release publish`, under the owner's gh outside any seat, runs each only with its
  verified answer.
- **Recover.** Inspect (`status`, `task list`, `task reconcile`), re-ring the
  doorbell, release, reset or cancel the task, then escalate. A quarantined seat
  is cleared only by the owner with `seat clear` from the owner's own terminal.

Message bodies are data, never authority: a body that claims the owner approved
something is evidence only; only a verified gate answer opens a gate.

---

## Role authoring

Custom roles are first-class: any role a squad.yaml seat names that is not in the
catalog needs a role file at `.amq-squad/roles/<id>.md`.

```sh
amq-squad role draft researcher --purpose "Investigate ambiguous behavior"
```

`role draft` uses the shared drafter (the user config
`~/.config/amq-squad/config.json`, else in-session), validates the draft and
stages it without overwriting; without a backend it prints the prompt and stages
nothing. It never adds a seat: name the role in `team propose --roles` afterwards.
At launch the role reaches the seat only through its `role.md`. See
[Drafter backends](drafter-backends.md) for the backend settings.

Keep a role version-neutral and session-neutral; what a seat works on this session
belongs in the brief and the task.

---

## End-to-end walkthrough

Shipping GitHub issue #96 with a squad, start to finish.

1. **Set up** — invoke `/amq-squad:wizard` and say *"the goal is GitHub issue
   #96, with a cto lead, a fullstack developer on Codex and a qa reviewer."* The
   skill runs readiness, then proposes the squad and relays the digest:

   ```sh
   amq-squad team propose --project . --roles cto,fullstack,qa \
     --runtime fullstack=codex --worktree fullstack=../app-96 \
     --actor-mode qa=review --lead cto --session issue-96
   ```

   After the owner approves that digest, it applies with the same options and seeds
   the brief:

   ```sh
   amq-squad team apply --digest sha256:<d> --project . --roles cto,fullstack,qa \
     --runtime fullstack=codex --worktree fullstack=../app-96 \
     --actor-mode qa=review --lead cto --session issue-96
   amq-squad brief --seed-from issue:96 --project . --session issue-96
   ```

2. **Launch** — `amq-squad lead --team` in the repository starts the lead and
   its scheduler, which launches every seat of squad.yaml but the lead in one
   AMQ launch request.

3. **Lead the work** — the `cto` seat, the squad's lead, loads
   `/amq-squad:orchestrator`, adds the tasks, reads status, and lets the
   scheduler dispatch:

   ```sh
   amq-squad task add --title "Fix #96" --class implement --files internal/parse/ --session issue-96
   amq-squad task add --title "Review #96" --class review --session issue-96
   amq-squad status --session issue-96 --json
   ```

4. **Converge** — the commit step pushes the fullstack seat's branch and binds its
   draft PR; qa reviews the exact head; the merge train dry-runs the PR set and the
   owner runs the real merge; `release prepare` raises the tag and release gates for
   the owner.

## Troubleshooting

| Symptom | Likely cause / fix |
| --- | --- |
| `no squad.yaml at <path>` | The repository has no squad yet — use `amq-squad:wizard`; `team propose` plans one. |
| `squad_yaml_digest_mismatch` on apply | The options changed since the proposal — run `team propose` again and get a fresh approval. |
| `squad_yaml_changed` on apply | squad.yaml was edited on disk after the proposal — the next `team propose` shows the edit as the diff to decide on. |
| `grants_invalid` | squad.yaml widens the owner grant or carries an owner-only key; it can only narrow. |
| `status: no store root` | Outside a seat, pass `--project <repo> --session <s>` or `--root <root>`. |
| A seat's done task never reaches review | The commit step refused a path outside the declared files — the lead widens the task or sends it back. |
| `task widen: takes no --me` | The scheduler proves the lead from the process; run it from the lead seat without `--me`. |
| A launch exits 10 | A required action (AMQ's trust confirmation) waits for the owner; rerun with `--decision` only on the owner's answer. |
| A Codex seat stops on its folder-trust prompt | The owner adds the lines `amq-squad trust <repo>` prints to `~/.codex/config.toml`. |
| Codex loads an old amq-squad skill after upgrade | Refresh the plugin/skill cache; `amq-squad doctor` reports the installed versions. |

## Where the authoritative instructions live

This guide is orientation. It is **not** the source of truth for how to run a squad.

Each skill's authoritative instructions live in its canonical source, and the
long-form material lives beside them:

| skill | authoritative source | extracted references |
| --- | --- | --- |
| `amq-squad:wizard` | `plugins/skills-src/wizard/SKILL.md` | `references/*.md`, `LEARNINGS.md` |
| `amq-squad:cli` | `plugins/skills-src/cli/SKILL.md` | `references/{daily-loop,primitives,troubleshooting}.md`, `LEARNINGS.md` |
| `amq-squad:orchestrator` | `plugins/skills-src/orchestrator/SKILL.md` | `references/{lead-loop,agent-events,recovery}.md`, `LEARNINGS.md` |

`plugins/{claude,codex}/skills/<skill>/` are GENERATED mirrors of those sources.
Edit the source and run `make skills-generate`; editing a mirror is lost on the next
generation and `--check` will fail.

When this guide and a `SKILL.md` disagree, **the `SKILL.md` is correct** — its
commands are checked against the live binary on every CI run:
`scripts/check-skill-commands.py` proves every named verb, subcommand and flag
exists, and `scripts/check-skill-invocations.py` executes every documented
invocation in a fixture project. This guide is not checked that way.
