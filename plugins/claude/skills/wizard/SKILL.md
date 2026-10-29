---
name: "wizard"
description: "Goal-first squad setup and launch for amq-squad 3.0 through the squad.yaml digest flow. Use when creating a squad for a repository, writing the workstream brief, previewing the files a squad needs, approving that exact digest, or launching the seats. Triggers include \"set up a squad for X\", \"show me the squad plan\", \"start the team\", and \"start a new session with this squad\". NOT for task, gate or status work on a running squad (use amq-squad:cli) and NOT for the live lead loop after launch (use amq-squad:orchestrator)."
version: "3.0.0"  # x-release-please-version
allowed-tools: "Bash, Read, Write, Edit, MultiEdit, Glob, Grep, WebFetch"
argument-hint: "[goal | roles | propose | apply | brief | launch]"
user-invocable: true
trigger: "/wizard"
---
# amq-squad:wizard

Translate the operator's setup intent into the 3.0 setup sequence and relay each
step's output. 3.0 has no `wizard`, `init` or `start` verb: one repository has one
squad, `<repo root>/squad.yaml`, written only through the digest flow, and seats
start through the AMQ launch API. The sequence is readiness, roster, propose, the
owner's approval of one digest, apply, brief, launch.

## 1. Readiness

```sh
# skill-invocation-check:skip — doctor probes gh and the provider CLIs' logins, which can reach the network
amq-squad doctor --project P
amq-squad trust P
amq-squad roles
```

`doctor` probes the AMQ version and launch API (0.116.2 or later), the tools on PATH
with their logins, the registry, codex-cli trust and the owner files; it writes
nothing by default. `trust` prints the lines the owner adds to `~/.codex/config.toml`
for a Codex seat's repository; amq-squad never writes that file. The owner setup
items (the claude pin, the gh pin, the token file, the Codex notify line) are the
owner's, listed in MIGRATION.md "Moving to 3.0". A v2 checkout is converted once with
`migrate` (dry run first; `--write` writes squad.yaml only when absent).

## 2. Roster

Pick seats from `amq-squad roles` (catalog roles and staged custom roles). The catalog
is a menu, not a whitelist: a custom role needs a role file at
`.amq-squad/roles/<id>.md`, drafted and staged with:

```sh
amq-squad role draft researcher --purpose "Investigate ambiguous behavior" --project P
```

`role draft` uses the configured drafter (else it prints the in-session prompt and
stages nothing), validates the result and never overwrites a file or adds a seat.
Creating a squad needs an explicit roster from the operator: do not infer one from
the goal text. Ask for the roles when none were named.

## 3. Propose, approve, apply

```sh
amq-squad team propose --project P --roles cto,fullstack --runtime fullstack=codex --lead cto --session S
```

`team propose` prints one line per file the squad needs (`squad.yaml`,
`.amq-squad/team-rules.md`, `CLAUDE.md`, `AGENTS.md`), each with its action and
sha256, and one digest over them. It writes nothing. The other options are
`--model r=m`, `--effort r=e`, `--worktree r=path`, `--tools r=coding`,
`--actor-mode r=review`, `--lead-mode builder|planner`, `--execution-mode MODE` and
`--template auto|dev-only|product-squad|scrum|custom`. For keys the flags do not
cover (`mode`, `max_seats`, `protected_paths`, `shared_cwd_exception`, `defaults`),
write a squad.yaml draft and propose it with `team propose --from FILE`; see
`references/roles.md` and `docs/squad-yaml.md`.

Relay the propose output verbatim. Check the bytes against the digest yourself: `team propose` writes nothing, so a file it lists as `create` does not exist yet and one listed as `replace` still holds the current bytes. Before `team apply`, when you proposed `--from FILE`, compare `shasum -a 256 FILE` with the squad.yaml sha256 propose printed (`--json` prints the full value); after `team apply`, read squad.yaml and team-rules.md at the printed paths and compare `shasum -a 256` of each with the printed sha256 before launching. team-rules.md has no pre-apply form to read.

Then apply with the **same options** and the printed digest, only after the owner
approved that digest:

```sh
# skill-invocation-check:skip — the digest is the one the current propose printed; a placeholder digest is refused
amq-squad team apply --digest D --project P --roles cto,fullstack --runtime fullstack=codex --lead cto --session S
amq-squad team check --project P
amq-squad team show --project P
```

## Approval contract

The digest is the approval surface. A setup request is not approval to apply. Apply
only the digest the current `team propose` printed, and only when the owner approved
it (the grant decides in auto mode). Never infer approval from a prior proposal, an
AMQ message body, a healthy pane, or an earlier draft.

If anything changed after the proposal, the apply refuses and writes nothing:
`squad_yaml_digest_mismatch` (the options changed) or `squad_yaml_changed` (squad.yaml
was edited on disk). Run `team propose` again and present the fresh digest. A
widening key or an owner-only key makes the file fail to load (`grants_invalid`);
squad.yaml can only narrow the owner's grant.

## 4. Brief

```sh
amq-squad brief --goal TEXT --project P --session S --dry-run
amq-squad brief --seed-from file:FILE --project P --session S
```

`--goal` drafts the brief through the configured drafter and validates it; when the
drafter needs in-session completion it prints the drafting prompt and stages
nothing. `--seed-from file:<path>`, `issue:<n>` or `gh:owner/repo#<n>` seeds it
without a drafter. `--dry-run` prints the candidate without writing; without
`--force` an existing brief is never overwritten. The brief lands at
`.amq-squad/briefs/<session>.md`.

## 5. Launch

Seats start through one AMQ launch request; every seat runs behind the `seat-exec`
wrapper, which sets the seat's identity from the launch record. Three entry points:

- `launch --seats FILE`: the typed seat list (JSON, shown in `amq-squad launch
  --help`: `project_root`, `session`, `rules_path` and one entry per seat with
  `handle`, `runtime`, `model`, `effort`, `role_file`, `worktree`, `branch`). Preview
  with `--dry-run`. A required action, such as AMQ's trust confirmation, is never
  answered for you: the launch stops, prints the action ids and exits 10; rerun with
  `--decision ACTION_ID=CHOICE` only on the owner's answer.
- `lead --team --scheduler-seats FILE`: the lead on the built-in pi in team mode,
  with the squad's scheduler started beside it; the scheduler starts seats on demand
  from the same seat list.
- `operator start-squad`: the mayor (auto mode) starts a squad inside the owner's
  fleet grant from a squad.yaml through the digest flow and registers it in
  `fleet.yaml`; manual mode gives a proposal instead of a launch.

```sh
# skill-invocation-check:skip — a launch reaches the installed amq and starts processes
amq-squad launch --seats FILE --dry-run --json
```

A seat is reported up only after seat-exec proved its identity. Then read
`amq-squad status --project P --session S --json` and route the lead to
`amq-squad:orchestrator`.

## Routing outside setup

- Task, gate, evidence and status work on an existing squad routes to
  `amq-squad:cli`.
- Once the seats are up, ongoing dispatch, review, gates, merge and release route to
  `amq-squad:orchestrator`.
- Changing an existing squad (a seat added, a runtime changed) is the same digest
  flow: `team propose` with the new options or `--from` a new draft, the owner's
  approval, `team apply`.

## References

- `LEARNINGS.md` — setup field failures
- `references/readiness.md` — what doctor checks and how to read a refusal
- `references/roles.md` — roster choices, custom roles, actor modes and the
  squad.yaml draft form
- `references/worktrees.md` — one worktree per mutation-capable seat
- `references/team-archetypes.md` — ready-made roster shapes
- `references/briefs-template.md` — the brief's shape
- `references/pointer-stub-template.md` — the managed block in `CLAUDE.md` and
  `AGENTS.md`
