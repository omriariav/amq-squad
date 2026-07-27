# The seven stages

`goal`, `brief`, `rules`, `roles`, `profile`, `readiness`, `launch`. Every stage
defaults to read-only, and a later stage consumes the accepted output of the earlier
one without silently changing its goal, namespace, roster, topology, role contracts,
or tool policy.

| Stage | Consumes | Produces | Writes? |
|---|---|---|---|
| goal | operator request or source | actionable goal binding with namespace and digest | no |
| brief | goal binding | workstream brief | at prepare |
| rules | project context | team rules | at prepare |
| roles | roster intent | role contracts | at prepare |
| profile | roles, binaries, models, tool policy | team profile | at prepare |
| readiness | written artifacts | pass/blocked rows with exact fixes | no |
| launch | accepted digest | running panes | at go |

## The three commands are not aliases

`--prepare-plan` renders the proposal and emits a digest. It writes nothing.

`--prepare` writes the coordination artifacts, then runs readiness against what it
wrote. It launches nothing.

`--go` launches exactly the displayed initial roster, consuming the accepted digest.
It repairs nothing.

Never move an operator from a generic preview straight to `--go`. The two approvals
are separate on purpose: preparation approves what will be written, launch approves
what will run.

## Transactionality

Preparation is transactional. If readiness fails after preparation, the artifacts it
wrote are rolled back — including a profile it created.

That has a consequence worth knowing before you hit it: a remedy that modifies an
existing profile has nothing to act on if this run created that profile. The failure
message says which case you are in, and points at the creation-time form when the
profile is gone.

## What the roster stage actually runs

```sh
amq-squad new team --roles cto,fullstack,qa --binary cto=codex --sync
amq-squad new team --roles cto,fullstack,qa --orchestrated --lead cto --sync
amq-squad new team --dry-run --json --roles cto,fullstack,qa --orchestrated --lead cto
```

`--orchestrated [--lead ROLE]` records the lead in `team.json` and writes a generated
`## Orchestration` reporting norm into `team-rules.md` **when that file is first
seeded**. It is a structured flag rather than pasted prose, so the norm cannot drift
from the roster.

Default off; exactly one lead; the lead is a team member, never the operator.

The seeding condition is the trap: an existing `team-rules.md` is left UNTOUCHED, so
adding `--orchestrated` to a team that already has rules silently gives you a lead
without the norm. Regenerate deliberately:

```sh
amq-squad team rules init --force
```

A brief can be seeded for an existing session, which is a different command from the
launch-time `--seed-from`:

```sh
amq-squad brief seed --session S --seed-from issue:96 --force
```

