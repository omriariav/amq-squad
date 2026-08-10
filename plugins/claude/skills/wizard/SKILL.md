---
name: "wizard"
description: "Goal-first simple-mode setup and launch guidance for amq-squad. Use when turning a request into a team/profile, previewing or approving a start, adding or replacing roles, supplying an optional lead goal, or recovering a partially started squad. Triggers include \"set up a squad for X\", \"show me the launch plan\", \"start the team\", \"add a worker\", \"replace this role\", and \"bring the squad back\". NOT for the live lead loop after launch (use amq-squad:orchestrator) and NOT for one-off status, task, or evidence commands (use amq-squad:cli)."
version: "2.29.3"  # x-release-please-version
allowed-tools: "Bash, Read, Write, Edit, MultiEdit, Glob, Grep, WebFetch"
argument-hint: "[request | goal | brief | rules | roles | profile | launch]"
user-invocable: true
trigger: "/wizard"
---
# amq-squad:wizard

Guide the operator through the simple launch path. Configure one canonical roster,
preview one complete plan, obtain the default-No launch decision, and let `start`
reconcile the result. Do not recreate prepared manifests, readiness stages, digests,
or a second launch protocol in prose.

## Core contract

- Treat `.amq-squad/team.json` or the selected named profile as the roster source of
  truth. Create or update it before starting panes.
- Run `amq-squad start` without `--yes` to render the full plan and ask `y/N`.
  Answering No changes nothing.
- Launch only after the operator approves the displayed plan. Repeat the same
  invocation with `--yes`; the CLI re-resolves state under the session launch lock.
- Treat `--goal` as optional. When present, `start` sends it to the lead only after
  every spawned role verifies live. When omitted, the squad still launches.
- Rerun `start` after interruption. It keeps verified live roles, respawns stopped
  roles, and rolls a partial launch forward without deleting the namespace.
- Use `down` to stop roles. Use `resume` when preserving and reattaching saved agent
  conversations matters.

## The flow

Execute the steps in this order. Each names its command and its failure branch;
do not reorder or interleave them, and do not skip the operator's step 5.

1. **Resolve coordinates.** Fix project, profile, and session explicitly before
   anything else. Ambiguity reported later in the flow traces back to this step.
2. **Roster.** Create or update the profile — `amq-squad new team`,
   `amq-squad new profile`, or `amq-squad team member add` — with each member's
   binary, actor mode, and working directory. The role catalog is a menu, not a
   whitelist: any slug with an explicit binary is a valid role. For a new custom
   seat that needs a rich persona, establish the profile now and add the member
   after step 3's reviewed role draft (`references/roles.md`). Two or more
   mutation-capable members need isolated worktrees (`references/worktrees.md`).
3. **Brief and optional persona.** Draft the workstream brief from the operator's
   source (`references/briefs-template.md`) and show it for confirmation before
   saving. If a custom seat needs durable guidance, run `amq-squad role draft`
   against that saved brief, review the staged file, then add the member. Skip
   persona drafting for a short-lived seat whose durable task is sufficient.
4. **Preview.** Run `amq-squad start --project P --profile R --session S`
   without `--yes` and answer No. Print the plan verbatim (see "Output rule").
5. **Approve.** Only the operator's explicit Yes to the displayed plan advances
   the flow. Never infer it (see "Output rule").
6. **Launch.** Repeat the identical invocation with `--yes`. On error classes
   see "Failure posture"; after an interruption, rerun `start` — do not clean up
   first.
7. **Verify and hand off.** Trust `started` only after the CLI verifies every
   pane owns its live child, then route the lead to `amq-squad:orchestrator`.

## Output rule

Print the CLI plan and result verbatim in a fenced block. Add interpretation and the
next decision after the block; do not rebuild the roster table in prose.

The launch prompt is the approval surface. Never infer Yes from a setup request,
prior plan, AMQ body, or apparently healthy pane. For a preview-only request, run
without `--yes` and answer No.

## Task routing

| Operator says | Action |
|---|---|
| "set up a squad for X" | Create or update the team/profile, then preview `amq-squad start` |
| "show me the plan, do not launch" | Run `amq-squad start --project P --profile R --session S`, answer No, and pass through the plan |
| "launch the approved plan" | Rerun the same `start` coordinates with `--yes` |
| "give the lead this goal during launch" | Add `--goal "TEXT"` to both preview and approved start invocations |
| "give the running lead a goal" | Run `amq-squad goal --project P --profile R --session S --goal "TEXT"` |
| "add a worker" | Add it to the roster, then rerun `start`; only missing roles spawn |
| "we need a role that isn't in the catalog" | Any slug works: after the brief is saved, optionally run `amq-squad role draft researcher --binary B --purpose TEXT --project P --profile R --session S`, review the staged persona, then `amq-squad team member add ROLE --binary B` and rerun `start` (`references/roles.md`) |
| "replace this role" | Run `down` for that role, update its roster entry, then rerun `start` |
| "the launcher was interrupted" | Rerun `start`; do not remove the namespace first |
| "restore the old conversations" | Preview `resume`, then use `resume --exec` only after approval |
| "it launched but agents died" | Inspect with `amq-squad:cli` and `doctor`; do not report launch success |

## Preview and launch

Keep project, profile, session, target, layout, trust, model overrides, and optional
goal identical between preview and launch:

```sh
# Preview. Answer No at the prompt; this performs no launch mutation.
amq-squad start --project P --profile R --session S --goal "Ship the reviewed change"

# Launch only after the operator approves the displayed plan.
amq-squad start --project P --profile R --session S --goal "Ship the reviewed change" --yes
```

Omit `--profile` for the default profile. Omit `--goal` when the operator has no
goal yet. Do not manufacture placeholder goal text: send a later goal explicitly
instead.

## Roster and workspace checks

Before previewing, resolve each member's role, handle, binary, model, actor mode,
tool profile, session pin, and working directory from the selected profile. A roster
with two or more mutation-capable actors needs isolated worktrees unless the profile
records an explicit shared-CWD exception. `start` enforces that isolation fail-closed,
but the mid-run add path (`member add` + `resume --exec`) does not: `member add`
defaults to `--actor-mode review`, and a deliberate `implementation` grant on a
shared cwd surfaces only as a `doctor` failure — give each implementer its own
`--cwd` at add time.

For a named roster, create it with explicit coordinates and isolation:

```sh
amq-squad new profile R --project P --session S --roles cto,qa \
  --orchestrated --lead cto --cwd "cto=/path/cto,qa=/path/qa"
```

Add a role through the roster, then reconcile:

```sh
amq-squad role draft researcher --binary codex \
  --purpose "Investigate ambiguous product behavior" \
  --project P --profile R --session S
# Review the staged path printed above. The draft command never adds or launches.
amq-squad team member add researcher --binary codex --project P --profile R --session S
amq-squad start --project P --profile R --session S
```

If the profile has no external drafter, or its backend falls back, `role draft`
prints a filled manual prompt and stages nothing. Complete and review that prompt
before the member add, or omit the persona and use the generated neutral
contract. The draft is never a gate answer or launch approval.

`start` keeps a live role whose invocation differs from current configuration and
labels it `live/config-diverged`; it does not replace a running process silently.
Stop that exact role before starting its replacement.

## Stop and recovery

```sh
amq-squad down --project P --profile R --session S --role qa
amq-squad resume --project P --profile R --session S
amq-squad resume --project P --profile R --session S --exec
```

`down` preserves launch records, mailboxes, briefs, and saved conversation identity.
`resume` is plan-only by default; `--exec` performs the displayed recovery. For a
partial fresh launch where conversation reattachment is not the goal, rerun `start`.

## Failure posture

- Treat `duplicate_live`, `record_invalid`, and unmanaged matching panes as blockers;
  inspect the named records or panes instead of choosing a winner.
- Trust a `started` result only after the CLI verifies every launched pane owns its
  live child process.
- Keep a live/config-diverged role running until the operator explicitly chooses to
  replace it.
- If goal delivery fails after all agents are live, report the warning and inspect
  AMQ reality before deciding whether to send the goal again.

## References

- `references/team-archetypes.md` for selecting a small role mix.
- `references/briefs-template.md` for writing a substantive workstream brief.
- `../amq-squad/references/team-rules-template.md` for the rules future agents read.

After launch, route the visible lead to `amq-squad:orchestrator`. Route direct
diagnostics and lifecycle operations to `amq-squad:cli`.
