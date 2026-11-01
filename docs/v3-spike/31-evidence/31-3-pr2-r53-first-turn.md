# 31-3 PR 2: R53's switch and the first turn

Brief: docs/v3-spike/briefs/brief-31-3-squad-verbs.md, PR 2: section 4
(R53, the switch) and section 5 (the first turn and the host facts), rows
2, 4 and 5. PR 1 (#834, merged as `95b12207`) built the verbs, R55's two
doors witnessed by the lead, and the start through up's shared functions;
its note (31-3-squad-verbs.md, 2.9) named the two seams this PR takes.
Branch v3-31-3b, from origin/v3 `95b12207`.

## 1. What pi 1.3.0 allows

pi's extension API has no unregister: a registered tool stays registered.
It has `getActiveTools()` and `setActiveTools(names)`: the active set is the
set declared to the model, and a tool of `direct` exposure (every
amq-squad tool and pi's built-ins) is "declared to the model while active,
and callable while active" (types.d.ts, `ToolExposure`). A tool registered
with `defaultActive: false` is registered but not active until
`setActiveTools` names it. So the switch is by that mechanism: the tool
list declared to the model is replaced, and a tool outside it is neither
declared nor callable. As a second layer the extension's `tool_call` hook
refuses any tool outside the team list with `phase_team` (a model that
names an undeclared tool, or a pi that kept one callable, still meets the
refusal).

## 2. The design

### 2.1 R53, the switch

- **The team tools exist before the yes.** `registerTools`
  (pi/extensions/lib/tools.ts) registers the team lead's tools for the
  solo lead too, with `defaultActive: false`: registered, not declared.
  The solo lead's declared set is pi's built-ins (read, bash, edit,
  write), the seat tools, and `amq_team_propose` and `amq_team_apply`.
- **The marker.** When the start of a confirmed team succeeds inside
  `leadStarter.start` (internal/cli/lead_start.go), the solo lead's
  process writes `<solo root>/extensions/amq-squad/phase.json`
  (atomicfile, 0600): `phase: team`, the project, and the team lead's
  identity as `lead --team` resolves it from squad.yaml
  (`resolveTeamLeadIdentity`: the squad's session root, base root,
  session and lead handle), the confirmed digest, the time, and the
  lead's pid and start stamp. The brief says "the apply writes": it is the
  lead's process that runs the start, so it writes it.
- **The switch.** `amq_team_apply`'s success, and a session start in the
  same lead process, read the marker (`SoloTeam.readMarker`,
  pi/extensions/lib/team.ts): a regular file whose pid is this pi's parent
  (the lead process that spawned it). `switchToTeam` then moves the lead
  to the squad's identity (`AM_ROOT`, `AM_BASE_ROOT`, `AM_SESSION`, `AM_ME`
  and `AMQ_SQUAD_LEAD` for this process and every verb it runs; the shared
  config's root, handle and session) and calls
  `pi.setActiveTools(the team list)`: the solo set (read, bash, edit,
  write, `amq_team_propose`, `amq_team_apply`) is gone from the declared
  list, and the `tool_call` hook refuses any of it `phase_team`.
- **The team list is the thirteen the pi lead registers**:
  `amq_send`, `amq_drain`, `amq_reply`, `amq_task_list`, `amq_task_claim`,
  `amq_task_done`, `amq_task_add`, `amq_repo_read`, `amq_repo_list`,
  `amq_status`, `amq_scheduler_relaunch`, `amq_task_widen`,
  `amq_task_reset`: `registerTools`' own list. `TeamLeadTools` in Go
  (internal/piruntime/launch.go) lists eleven (no `amq_task_claim`, no
  `amq_task_done`); the plan reviewer ruled the row names the registered
  list until the one-list follow-up lands. No gate-answer verb, no confirm
  tool (the confirm is a command).
- **A second apply in team phase** is refused twice: the extension's hook
  refuses `amq_team_apply` `phase_team`, and the lead's start refuses
  `lead_running` (PR 1).
- **A lead `up` starts** (`lead --team`) is team phase from its first turn
  with no marker: `AMQ_SQUAD_LEAD=1`, the team tools active, no solo set.
- **A restart.** A solo lead started again in a repository whose marker
  says team (and that has a squad.yaml) starts as `lead --team` (runLead,
  internal/cli/pi_lead.go): the team role and tools, the lead lock, the
  scheduler over squad.yaml's identity, the screen. It says so on stderr.
  A marker for another repository is no switch. The marker grants nothing
  the owner's own `amq-squad lead --team` would not: the confirmation was
  witnessed before it was written, squad.yaml is the owner's, and the
  restart is the owner's own command.

### 2.2 The first turn

The team lead's `before_agent_start` hook (pi/extensions/amq-squad.ts) adds
a system section, `amq-squad-host`, built from `amq-squad status --json`
(the lead's root) and `amq-squad context --json --host`, never from the
model: the repository, the branch and its tip, the installed AMQ and
amq-squad versions, the roster with each seat's runtime and model, the
open tasks (closed ones left out) and the open gates (answered ones left
out) (pi/extensions/lib/firstturn.ts `hostLine`). It is rebuilt every turn,
so a changed status changes it; a fact a verb does not give, or a verb that
failed, reads `missing`, never a guess.

Neither verb carried the branch, the tip or the AMQ version, so `context`
gains `--host` (internal/cli/hook_context.go): the repository
(`$AMQ_SQUAD_PROJECT`, else the repository of the working directory), its
branch and tip from git, and the installed amq's `--version`, each empty
when it cannot be read. Only the team lead asks for it, so a seat's
context costs nothing more. `status --json` gives the repository
(`project_path`), the amq-squad version (`producer_version`), the roster,
the tasks and the gates; docs/status-contract.md is unchanged (no status
field changed).

### 2.3 Choices where the brief left one

- Both mechanisms: `setActiveTools` (the declared list) and the
  `phase_team` refusal in the `tool_call` hook (1).
- The marker lives in the solo lead's own root, where its pi reads it; it
  is accepted in-process only when its pid is pi's parent, and on a
  restart by its project.
- The host line is every turn, not only the first (the first turn carries
  it, and later turns carry fresh facts).
- `context --host` is internal/cli/hook_context.go, in internal/cli/ but
  outside the two files the round named (lead_start.go, pi_lead.go).

### 2.4 Not in this PR

- The switched solo lead gets the team tools and identity but not the
  team screen in the same process (`installScreen` runs at load for
  `AMQ_SQUAD_LEAD=1`); a restart (which starts as `lead --team`) has it.
- The lead `up` starts runs with `--tools` of `TeamLeadTools`' eleven, so
  pi's allowlist keeps `amq_task_claim` and `amq_task_done` out of its
  loadout, while a switched solo lead (no `--tools`) has the thirteen; the
  one-list follow-up settles which list is the team's.

## 3. Rows

| row | test |
|---|---|
| 2 | pi `phase.test.ts` "R53: the owner's yes switches the lead's tools to exactly the team lead's thirteen and refuses the solo set phase_team" (before the yes the solo tools answer and the team tools are not declared; after it the declared list is the thirteen, the identity is the squad's, a shell call, an edit, a second `amq_team_apply` and `amq_team_propose` refuse `phase_team`, and `amq_status` answers), "R53: only this lead's marker switches; a session started again after the yes is team phase"; cli `TestASoloLeadRestartedInATeamMarkedRepositoryStartsAsTheTeamLead` (no marker: solo; a team marker: `lead --team`, the scheduler started, the squad's identity; another repository's marker: no switch), `TestTheStartOfAConfirmedTeamRunsUpsSharedStepsAndNoInstallOrTmux` (the yes writes the marker; a second start refuses `lead_running`) |
| 4 | pi `phase.test.ts` "the team lead's first turn carries the host facts from status and context, not the model" (the repository, branch and tip, the AMQ and amq-squad versions, the roster, the open tasks and gates; a changed status changes the line; missing facts read missing); cli `TestContextHostCarriesTheHostFactsAndLeavesAMissingOneEmpty` |
| 5 | pi `phase.test.ts` "R53: the owner's yes switches ..." (the declared list in team phase equals the thirteen exactly; no gate-answer or confirm tool), "R53: lead --team is team phase from its first turn with exactly the thirteen" |

No test reaches the network, the real HOME, tmux, AMQ or Buzz: the Go rows
run under TestMain's temp HOME with the lead's seams; the pi rows load the
whole extension into the stand-in pi with a fake amq-squad script.

## 4. The revert runs

`python3 docs/v3-spike/31-evidence/31-3-pr2-revert-runs.py` from a clean
checkout of the branch: each run reverts one change, runs its rows, and
restores every touched file to HEAD in a finally; it refuses a dirty tree.
The output, verbatim:

```
REVERT context --host ignored: fails (the row catches it)
   --- FAIL: TestContextHostCarriesTheHostFactsAndLeavesAMissingOneEmpty (0.00s)
REVERT the branch not read from git: fails (the row catches it)
   --- FAIL: TestContextHostCarriesTheHostFactsAndLeavesAMissingOneEmpty (0.00s)
REVERT the AMQ version not read: fails (the row catches it)
   --- FAIL: TestContextHostCarriesTheHostFactsAndLeavesAMissingOneEmpty (0.00s)
REVERT the yes writing no phase marker: fails (the row catches it)
   --- FAIL: TestTheStartOfAConfirmedTeamRunsUpsSharedStepsAndNoInstallOrTmux (0.07s)
REVERT a restarted solo lead not reading the marker: fails (the row catches it)
   --- FAIL: TestASoloLeadRestartedInATeamMarkedRepositoryStartsAsTheTeamLead (0.02s)
REVERT a marker for another repository taken: fails (the row catches it)
   --- FAIL: TestASoloLeadRestartedInATeamMarkedRepositoryStartsAsTheTeamLead (0.02s)
REVERT the team tools declared before the yes: fails (the row catches it)
   not ok 1 - R53: the owner's yes switches the lead's tools to exactly the team lead's thirteen and refuses the solo set phase_team
REVERT the switch not changing the declared tools (no setActiveTools): fails (the row catches it)
   not ok 1 - R53: the owner's yes switches the lead's tools to exactly the team lead's thirteen and refuses the solo set phase_team
   not ok 2 - R53: only this lead's marker switches; a session started again after the yes is team phase
REVERT no phase_team refusal of the solo set: fails (the row catches it)
   not ok 1 - R53: the owner's yes switches the lead's tools to exactly the team lead's thirteen and refuses the solo set phase_team
REVERT the lead's identity not switched to the squad's: fails (the row catches it)
   not ok 1 - R53: the owner's yes switches the lead's tools to exactly the team lead's thirteen and refuses the solo set phase_team
REVERT an earlier lead's marker taken (no pid check): fails (the row catches it)
   not ok 2 - R53: only this lead's marker switches; a session started again after the yes is team phase
REVERT amq_team_apply's success not switching: fails (the row catches it)
   not ok 1 - R53: the owner's yes switches the lead's tools to exactly the team lead's thirteen and refuses the solo set phase_team
REVERT a session started after the yes not switching: fails (the row catches it)
   not ok 2 - R53: only this lead's marker switches; a session started again after the yes is team phase
REVERT no host line: fails (the row catches it)
   not ok 4 - the team lead's first turn carries the host facts from status and context, not the model
REVERT context asked without --host: fails (the row catches it)
   not ok 4 - the team lead's first turn carries the host facts from status and context, not the model
REVERT closed tasks shown as open: fails (the row catches it)
   not ok 4 - the team lead's first turn carries the host facts from status and context, not the model
REVERT answered gates shown as open: fails (the row catches it)
   not ok 4 - the team lead's first turn carries the host facts from status and context, not the model
REVERT a missing fact left blank, not named missing: fails (the row catches it)
   not ok 4 - the team lead's first turn carries the host facts from status and context, not the model
18 runs, 18 caught, 0 missed
```

## 5. The owner-run rows

On this repository, with the installed tagged build, a fleet grant in auto
mode permitting it (PR 1's note, section 5), in the owner's terminal:

**A. The switch.**

```sh
cd /Users/omri/code/amq-squad
amq-squad lead
```

Propose a team, confirm it (`/squad confirm <prefix>`), and ask the lead to
apply. The tool's result ends `team phase: your tools are now the team
lead's`. Ask the lead to run `ls` in a shell: refused `phase_team`. Ask it
for the squad's status: `amq_status` answers from the squad's root. Then:

```sh
cat ~/.amq-squad/solo/amq-squad-*/root/extensions/amq-squad/phase.json
```

shows `"phase": "team"`, the squad's root, session and lead handle.

**B. A restart.** Quit the lead (Ctrl-C) and run `amq-squad lead` again in
the repository: stderr reads `... switched to team phase at ...; starting as
amq-squad lead --team`, the screen appears below the editor, and the tools
are the team lead's from the first turn.

**C. The first turn.** In a lead started either way (or by `amq-squad
up`), ask: "state the host facts you were given". The answer quotes the
repository, the branch and tip, the AMQ and amq-squad versions, the roster,
the open tasks and the open gates as `status --json` and `context --json
--host` give them:

```sh
amq-squad status --project /Users/omri/code/amq-squad --json
AM_ME=<lead> AM_ROOT=<squad root> AM_SESSION=<session> amq-squad context --json --host
```

## 6. What the brief got wrong

- "If pi cannot remove a registered tool, the tool's handler refuses every
  call in team phase": pi 1.3.0 cannot remove one, but it can deactivate it
  (`setActiveTools`), which removes it from the declared list and from
  what the model can call; the switch uses that, and the handler refusal
  as a second layer (1).
- "TeamLeadTools (the thirteen)": `TeamLeadTools` in Go lists eleven; the
  thirteen are the tools the pi lead registers (2.1, 2.4).
- "The apply writes the phase marker": the lead's process writes it; the
  apply is a request to that process (PR 1's 2.5).
- "The host facts from status --json and context --json": neither carried
  the branch, the tip or the AMQ version; context gains `--host` (2.2).

## 7. One line for the 3.1 release notes

On your yes the lead becomes the team's lead for the rest of the
conversation: its tools switch to the team lead's (no shell, no edit), each
turn opens with the host facts read from status and context, and a restart
of `amq-squad lead` in the repository starts it as the team's lead.
