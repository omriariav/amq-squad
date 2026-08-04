# The simple start flow

Simple Mode has one launch command and one approval. There is no prepare stage,
readiness stage, accepted digest, or separate go command.

## Authoritative inputs

Before launch, resolve and review these inputs:

| Input | Source | Purpose |
|---|---|---|
| project/profile/session | operator coordinates | selects one canonical namespace |
| roster | `team.json` or the named profile | defines roles, binaries, actor modes, and working directories |
| rules | `.amq-squad/team-rules.md` | defines shared operating constraints |
| brief | active workstream brief | defines the workstream context |
| goal | optional operator text | gives the lead an initial goal after all roles are live |

The inputs are authoritative directly. Do not persist a second owned
representation merely to certify them.

## Preview and approve

Run the complete start command without `--yes`:

```sh
amq-squad start --project P --profile R --session S --goal "Ship the reviewed change"
```

The CLI renders the roster, briefs, optional goal, and launch actions, then asks
for a default-No `y/N` decision. A No answer changes nothing. After the operator
approves the displayed plan, repeat the same coordinates with `--yes` for an
automated or non-interactive invocation.

`start` re-resolves the inputs under the session launch lock. It writes the
briefs, creates or adopts the canonical namespace, keeps verified live roles,
spawns missing or stopped roles, verifies every child process, writes launch
records, and only then sends the optional goal to the lead.

## Roster changes and recovery

- Add a role to the roster, then rerun `start`; only the missing role starts.
- To replace a role, run `down` for that role, update its roster entry, then
  rerun `start`.
- After an interrupted fresh launch, rerun `start`; do not delete the namespace.
- Use `resume` when preserving and reattaching saved conversations is the goal.
- For an existing session, edit the resolved active brief directly before
  rerunning `start`.

## Roster setup

```sh
amq-squad new team --roles cto,fullstack,qa --binary cto=codex --sync
amq-squad new team --roles cto,fullstack,qa --orchestrated --lead cto --sync
amq-squad new team --dry-run --json --roles cto,fullstack,qa --orchestrated --lead cto
```

`--orchestrated [--lead ROLE]` records the lead in `team.json` and writes a
generated `## Orchestration` reporting norm into `team-rules.md` when that file
is first seeded. Existing rules remain untouched; regenerate them deliberately
with:

```sh
amq-squad team rules init --force
```

The lead must be a team member and is never the operator.
