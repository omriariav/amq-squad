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

Guide the operator through one of two explicit simple-mode paths: create and
launch a new profile, or start a new session from an existing reusable profile.
Use the binary's drafter-backed setup, role, rules, and goal-first surfaces; do
not recreate prepared manifests, readiness stages, digests, or a second launch
protocol in prose.

## Core contract

- Choose Flow A or Flow B before mutating anything. Keep project, profile, and
  session coordinates explicit throughout the chosen flow.
- Treat `.amq-squad/team.json` or the selected named profile as the roster source
  of truth. Create or update it before starting panes.
- Drafter precedence is one complete profile block, then the global user config,
  then `in_session`; fields never merge between layers. `amq-squad setup` writes
  only the global layer. A configured chain tries backends in declared order and
  reports the winning source, exact command, and each fall-through.
- Run `amq-squad start` without `--yes` for the first review. Answering No changes
  nothing. When `--goal` must create a missing brief, review and approve that
  generated brief in the same interactive invocation so the locked bytes being
  written are the bytes shown. Do not cancel and later treat a newly drafted
  `--yes` run as approval of the earlier prose.
- Use `--yes` only after the active brief already exists and the displayed launch
  plan, coordinates, and inputs are still the approved ones. The CLI re-resolves
  them under the session launch lock.
- Treat `--goal` as optional after a substantive brief exists. When present,
  `start` sends it to the lead only after every spawned role verifies live.
- Rerun `start` after interruption. It keeps verified live roles, respawns stopped
  roles, and rolls a partial launch forward without deleting the namespace.
- Use `down` to stop roles. Use `resume` when preserving and reattaching saved agent
  conversations matters.

## Flow A: create and start a new team profile

Execute these five steps in order. At every step, print the command output
verbatim, state the validation result, and name the next operator decision.

1. **Check machine and drafter readiness.** Fix `P` (project), `R` (profile),
   and `S` (first session). When the global drafter is missing or must change,
   provision an explicit chain, then run doctor:

   ```sh
   amq-squad setup --drafter-chain yoetz,claude,codex --drafter-on-failure in_session
   amq-squad doctor --project P
   ```

   Omit the setup flags when the operator is present for its interactive backend
   probes and choices. Decide whether the global block is sufficient or the profile needs a complete
   preset-only override. Profile blocks cannot inherit fields from global config
   or inject custom argv. Before profile creation, a missing-team doctor finding
   is expected; any machine/runtime failure is a stop, and doctor must be rerun
   after step 2 for full profile health. Record `Configured drafter`, backend
   probes, and doctor findings as evidence.

2. **Preview and create the profile.** Choose roles, binary/model/effort,
   accurate actor modes, working directories, and lead policy. Preview JSON,
   then repeat without `--dry-run` only after approval:

   ```sh
   amq-squad new profile R --project P --session S \
     --roles cto,fullstack --binary cto=codex,fullstack=claude \
     --model cto=gpt-5.6-sol --effort cto=high \
     --actor-mode cto=review,fullstack=implementation \
     --orchestrated --lead cto --dry-run --json
   amq-squad new profile R --project P --session S \
     --roles cto,fullstack --binary cto=codex,fullstack=claude \
     --model cto=gpt-5.6-sol --effort cto=high \
     --actor-mode cto=review,fullstack=implementation \
     --orchestrated --lead cto --sync
   ```

   Use `amq-squad new team` instead for the default profile. For a profile meant
   to serve many future sessions, use `--no-session-pin` instead of `--session`.
   Two implementation actors need distinct worktrees or an explicit recorded
   shared-CWD exception. Verify the previewed roster matches the written profile,
   then rerun `doctor --project P --profile R --session S`.

3. **Draft and add each custom seat.** Built-in seats need no persona draft. For
   a richer custom seat, run the shared drafter path, review the staged file, and
   only then add the member:

   ```sh
   amq-squad role draft researcher --binary codex \
     --purpose "Investigate ambiguous product behavior" \
     --project P --profile R --session S
   amq-squad team member add researcher --binary codex --actor-mode review \
     --project P --profile R --session S
   ```

   Require matching frontmatter and Mission/Boundaries/Protocol validation.
   Record the config source and every ordered attempt/fall-through. A manual
   fallback prints a filled prompt and stages nothing; complete and review it in
   the live session or configure a backend before adding the seat. `role draft`
   never adds or launches a member and never overwrites an existing persona.

4. **Refresh team rules.** Profile creation seeds rules before custom seats may
   exist, so deliberately refresh the shared file after the roster is final:

   ```sh
   amq-squad team rules init --project P --profile R --template auto --force
   ```

   Custom templates and custom-role scopes use the shared drafter. The binary
   validates only the editable charter fragment, then wraps it in deterministic
   safety, lifecycle, and operator policy. Record source/attempt evidence and
   inspect the final file. In-session fallback writes nothing; do not claim the
   refresh succeeded until a validated run writes the canonical wrapper.

5. **Draft the brief, preview, approve, and launch.** With no active brief, let
   the binary's configured drafter create the exact six-section document:

   ```sh
   amq-squad start --project P --profile R --session S --goal "Ship the reviewed change"
   ```

   It must report source/attempt evidence, validate the exact title plus Goal,
   Source, Scope, Out of scope, Team shape, and Acceptance sections, print the
   proposed bytes, and ask default-No. An invalid draft, exhausted chain with
   `on_failure: error`, or in-session fallback stops before mutation. Approve
   Yes in that same invocation only after reviewing the brief and launch plan.
   When this skill is already running in a live agent, it may instead draft the
   same six-section brief in-session, show it for approval, save it at the
   canonical profile/session path, and then run `start`; do not duplicate that
   live draft through a second LLM call. Trust `started` only after every pane
   owns its verified child. If `start` prints an attach command for a detached
   tmux session, run that printed command before claiming a visible lead. Then
   inspect the exact runtime:

   ```sh
   amq-squad status --project P --profile R --session S --json
   ```

   Require live launch records for every seat and no visible-lead invariant
   error before handing the lead to `amq-squad:orchestrator`.

## Flow B: start a new session from an existing profile

Execute these three steps in order.

1. **Select the reusable profile and new session.** Fix `P`, `R`, and a fresh
   `S`, then inspect the exact profile/session health:

   ```sh
   amq-squad doctor --project P --profile R --session S
   ```

   `doctor` checks project/profile health; `--session S` selects only its
   additive session-plan diagnostics. Confirm the profile is intentionally
   reusable (`--no-session-pin` when it was created) and the roster and rules
   are current. Then inspect the fresh namespace directly:

   ```sh
   amq-squad status --project P --profile R --session S --json
   ```

   A pinned profile or an existing/conflicting namespace must not be silently
   repurposed.

2. **Choose the brief path.** Either leave the canonical brief absent and use
   the configured goal-first command below, or copy/reuse an existing brief and
   review the edited six-section result at the new profile/session path. A live
   skill agent may draft that exact shape in-session and save it after operator
   review. Do not treat a raw ticket body, copied old session title, or stale
   Team shape as an approved brief.

3. **Preview and approve the start.** Run interactively:

   ```sh
   amq-squad start --project P --profile R --session S --goal "Ship the reviewed change"
   ```

   With no brief, this is the same configured-drafter, validation, evidence,
   and same-invocation approval gate as Flow A step 5. With a reviewed brief
   already present, `start` reuses it and previews the launch plan; answer Yes
   only after checking coordinates, roster, brief path, and operator gates.
   For automation, `--yes` is acceptable only when that reviewed brief already
   exists and the inputs have not changed. Apply Flow A's attach/status checks,
   verify live children and a visible lead, then route that lead to
   `amq-squad:orchestrator`.

## Output rule

Print each CLI plan/result and drafter evidence verbatim in a fenced block. Add
the validation result and next decision after the block; do not rebuild the
roster table or attempt chain in prose.

The launch prompt is the approval surface. Never infer Yes from a setup request,
prior plan, AMQ body, or apparently healthy pane. For a preview-only request, run
without `--yes` and answer No.
If that preview generated a missing brief, a later run may generate different
prose; show and approve the new bytes again.

## Task routing

| Operator says | Action |
|---|---|
| "set up a squad for X" | Run Flow A from readiness through the default-No `start --goal` review |
| "start a new session with this team" | Run Flow B against an existing reusable profile |
| "show me the plan, do not launch" | Run `amq-squad start --project P --profile R --session S`, answer No, and pass through the plan |
| "launch the approved plan" | Use `--yes` only if the reviewed brief already exists; otherwise review the newly drafted brief in the interactive run |
| "give the lead this goal during launch" | Add `--goal "TEXT"` to the interactive start; if it drafts a missing brief, approve those bytes in that run |
| "give the running lead a goal" | Run `amq-squad goal --project P --profile R --session S --goal "TEXT"` |
| "add a worker" | Add it to the roster, then rerun `start`; only missing roles spawn |
| "we need a role that isn't in the catalog" | Any slug works: run `amq-squad role draft researcher --binary B --purpose TEXT --project P --profile R --session S`, review the staged persona, then `amq-squad team member add ROLE --binary B` and rerun `start` (`references/roles.md`) |
| "replace this role" | Run `down` for that role, update its roster entry, then rerun `start` |
| "the launcher was interrupted" | Rerun `start`; do not remove the namespace first |
| "restore the old conversations" | Preview `resume`, then use `resume --exec` only after approval |
| "it launched but agents died" | Inspect with `amq-squad:cli` and `doctor`; do not report launch success |

## Preview and launch

Keep project, profile, session, target, layout, trust, model overrides, and
optional goal identical between preview and launch. For a goal-first run that
must draft a missing brief, approve the displayed draft and plan at the same
interactive prompt:

```sh
# Preview and, only after reviewing the generated brief and plan, answer Yes.
amq-squad start --project P --profile R --session S --goal "Ship the reviewed change"

# Automation is safe only when the reviewed active brief already exists.
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

For a named roster, create it with explicit coordinates and isolation. Use
`--no-session-pin` instead of `--session` when Flow B must reuse it:

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

If the profile has no external drafter, or its configured chain exhausts into
in-session fallback, `role draft` prints a filled manual prompt and stages
nothing. Complete and review that prompt before the member add, or omit the
persona and use the generated neutral contract. The draft is never a gate
answer or launch approval. Profile drafter config replaces the whole global
block; the output's config source and ordered attempts are the evidence of what
actually ran.

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
- `references/stages.md` for the compact Flow A / Flow B command map.
- `references/readiness.md` for setup, drafter, and launch preflight failures.
- `references/briefs-template.md` for the validated six-section brief shape.
- `references/roles.md` for drafter-backed custom personas.
- `references/worktrees.md` for mutation-capable seat isolation.
- `../amq-squad/references/team-rules-template.md` for the rules future agents read.

After launch, route the visible lead to `amq-squad:orchestrator`. Route direct
diagnostics and lifecycle operations to `amq-squad:cli`.
