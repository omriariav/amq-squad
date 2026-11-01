# 31-3 PR 2: R53's switch and the first turn

Brief: docs/v3-spike/briefs/brief-31-3-squad-verbs.md, PR 2: section 4
(R53, the switch) and section 5 (the first turn and the host facts), rows
2, 4 and 5. PR 1 (#834, merged as `95b12207`) built the verbs, R55's two
doors witnessed by the lead, and the start through up's shared functions;
its note (31-3-squad-verbs.md, 2.9) named the two seams this PR takes.
Branch v3-31-3b, from origin/v3 `95b12207`, with origin/v3 merged in at
`6188663d`. On #837 the plan reviewer required two changes (the marker
is never authority; the host line on the first turn and on change), and
Codex's static pass at `6188663d` added five more (items 2, 3, 5, 6 and
7; its items 1 and 4 are closed by the reviewer's first change). This note
describes the branch after both.

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
refusal). A tool may also declare `executionMode: "sequential"`
(types.d.ts, `ToolDefinition`): a batch holding one runs one call at a
time instead of preparing all before running any.

## 2. The design

### 2.1 R53, the switch

The plan reviewer's ruling on #837: the marker is never authority. pi
switches on the lead process's own reply, never on reading a file; the
marker is a record nobody reads for authority; a restart comes up solo.

- **The team tools exist before the yes.** `registerTools`
  (pi/extensions/lib/tools.ts) registers the team lead's tools for the
  solo lead too, with `defaultActive: false`: registered, not declared.
  The solo lead's declared set is pi's built-ins (read, bash, edit,
  write), the seat tools, and `amq_team_propose` and `amq_team_apply`.
- **The switch rides the lead's reply.** Before anything starts,
  `leadStarter.start` (internal/cli/lead_start.go) resolves the team
  lead's identity from the confirmed squad (the squad's session root,
  base root, session and lead handle, as `lead --team` pins them); a
  confirmed squad whose identity cannot be resolved (one naming no lead)
  refuses `team_identity` with nothing started. When the start succeeds,
  the lead keeps the identity in memory and returns it with the confirmed
  digest in the start's reply over the start socket
  (`leadStartReply.Team`), so a started team always carries it. `amq_team_apply`'s result hands that reply to
  `switchToTeam` (pi/extensions/lib/team.ts), which moves the lead to the
  squad's identity (`AM_ROOT`, `AM_BASE_ROOT`, `AM_SESSION`, `AM_ME` and
  `AMQ_SQUAD_LEAD` for this process and every verb it runs; the shared
  config's root, handle and session) and calls
  `pi.setActiveTools(the team list)`: the solo set (read, bash, edit,
  write, `amq_team_propose`, `amq_team_apply`) is gone from the declared
  list, and the `tool_call` hook refuses any of it `phase_team`. A reply
  without a team identity switches nothing.
- **A session started again in the same lead** (pi's `/new`, a resume)
  asks the lead over the same socket (`amq-squad.phase.v1`). The lead
  answers only a caller its peer proof places under it (`callerChain`, as
  for a confirm), and answers from its own memory: `team` with the
  identity after its own start, `solo` otherwise. No file is read.
- **The marker is a record only.** After the start the lead's process
  writes `<solo root>/extensions/amq-squad/phase.json` (atomicfile, 0600):
  `phase: team`, the project, the team lead's identity, the digest, the
  time, and the lead's pid and start stamp. Nothing reads it for
  authority: not pi, not the lead, not a restart. A write that fails (the
  model made phase.json a directory before the yes, so the atomic write
  cannot land) is a `phase_record_failed` decision in the solo root's log
  and a line in the start's detail, and nothing else: the start stays
  started, the reply still carries the identity, and the lead's pi
  switches.
- **amq_team_apply runs alone.** pi prepares a batch's tool calls, hooks
  included, before running any (pi-agent-core agent-loop.js, parallel by
  default), so a shell call admitted while solo could run after the
  apply switched the tools; neither `setActiveTools` nor the hook rechecks
  a call already prepared. The extension records this turn's batch: the
  assistant message's tool calls (from its `message_end`) and every call
  the `tool_call` hooks prepare, cleared at `turn_start`. The apply's
  execute refuses `apply_not_alone` when the batch holds any other call,
  before or after it, starts nothing, and tells the model: "Call
  amq_team_apply again on your next turn with nothing else in the
  batch." pi offers a per-tool execution mode, and the apply is
  `executionMode: "sequential"`: a batch holding it runs one call at a
  time, so nothing runs beside it. bash, edit and write are pi's own
  built-ins, which the extension cannot recheck at run time; that is why
  the refusal is the shape (the plan reviewer). The extension's own solo
  tools (`amq_team_propose`, `amq_team_apply`) recheck the phase at
  execute time and refuse `phase_team` if the switch landed between their
  preparing and their running. Every other extension tool is on the team
  list.
- **A restart comes up solo.** `amq-squad lead` in a repository whose
  marker says team reads it as information only (`markerInfo`, the
  project must match) and stays the solo lead: no team role, no team
  tools, no lead lock, no scheduler. stderr, and the lead's first turn
  (a message, once), say: "this session's team was started on <date>
  with digest <D>; run `amq-squad up` to resume it, or confirm it again
  at your pane (/squad confirm) or over Buzz". A marker for another
  repository gives no offer.
- **What a planted marker could have gained.** Codex's answer on the
  round before this one: under the marker-driven design, the solo lead's
  model (it has write and bash, and can read its own parent's pid and the
  lead's start stamp) could write a phase.json in its own root carrying
  the lead's pid and stamp. In the same process that switched its tools
  and identity to the squad's with no yes. On a restart it did more: the
  restart started as `lead --team`, which takes the lead lock and starts
  the scheduler over squad.yaml, a file the same model could also have
  written. So a planted marker could have gained a running scheduler
  launching seats from a squad.yaml nobody confirmed. In Codex's words on
  #837: "an unconfirmed scheduler start, not just fewer tools". Codex
  also found the in-process read checked only the pid against pi's
  parent, ignoring the stamp, the project and the digest. With the switch
  on the lead's reply and the restart solo, a planted marker, whatever it
  carries, switches nothing and starts nothing (rows 2a and 2b).
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
- **A lead `up` starts** (`lead --team`) is team phase from its first turn:
  `AMQ_SQUAD_LEAD=1`, the team tools active, no solo set.

### 2.2 The first turn

The team lead's host line is a message (`amq-squad-host`, shown in the
conversation and kept in its context) given by a `before_agent_start`
handler (pi/extensions/amq-squad.ts, `HostFacts` in
pi/extensions/lib/firstturn.ts). It is built from `amq-squad status --json`
(the lead's root) and `amq-squad context --json --host`, never from the
model: the repository, the branch and its tip, the installed AMQ and
amq-squad versions, the roster with each seat's runtime and model, the
open tasks (closed ones left out) and the open gates (answered ones left
out). A fact a verb does not give, or a verb that failed, reads `missing`,
never a guess.

It goes on the first turn, and again only when a fact in it changed;
never every turn. Before each turn the handler stats a key: the lead's
root, and under it `extensions/amq-squad/tasks/.generation` (the task
store), `scheduler.json`, `scheduler/decisions.jsonl` (a roster decision),
`gates` and `consumed` (the gate store), and in the repository `.git/HEAD`
and `.git/logs/HEAD` (the branch and its tip). An unchanged key runs no
verb and sends nothing. A changed key runs the two verbs; a line equal to
the last one sent is not sent again.

Neither verb carried the branch, the tip or the AMQ version, so `context`
gains `--host` (internal/cli/hook_context.go): the repository
(`$AMQ_SQUAD_PROJECT`, else the repository of the working directory), its
branch and tip from git, and the installed amq's `--version`, each empty
when it cannot be read. Each command runs under a 3 second deadline in its
own process group, which is killed with everything it started before the
reap; its output is capped at 4 KiB. A timeout or a flood reads missing.
Every fact passes `redact.Block` before it leaves `context --host`, and
the extension redacts each fact again before it is interpolated
(`redactFact` in firstturn.ts: a value under a credential name, and the
six real provider shapes of `redact.RealCredentialShapes`). A branch named
`api_key=<value>` shows as `api_key=<redacted>`. Only the team lead asks for it, so a seat's
context costs nothing more. `status --json` gives the repository
(`project_path`), the amq-squad version (`producer_version`), the roster,
the tasks and the gates; docs/status-contract.md is unchanged (no status
field changed).

### 2.3 Choices where the brief left one

- Both mechanisms: `setActiveTools` (the declared list) and the
  `phase_team` refusal in the `tool_call` hook (1).
- The phase query is a new kind on the existing start socket, answered
  under the same peer proof as a confirm; no new socket, no new file.
- The resume offer is said on stderr at start and as a one-time message
  on the solo lead's first turn; it is not repeated on later turns.
- The host key watches the files the facts come from (the task store's
  generation, the scheduler's state and decisions, the gate store, git's
  HEAD and its log); a change outside them (a seat's model edited in
  squad.yaml with no scheduler decision) waits for the next key change.
- `context --host` is internal/cli/hook_context.go, in internal/cli/ but
  outside the two files the round named (lead_start.go, pi_lead.go).
- The identity is resolved before the start, from the confirmed squad,
  and a squad that cannot give one refuses `team_identity`; before, it
  was resolved after the start, and a failure left a running team with a
  solo lead.
- The batch is the assistant message's calls plus the hooks' record of
  the turn: either source alone covers a case the other may miss (the
  message's end arriving late; a sequential batch, whose later calls are
  prepared only after the apply runs).
- The extension's second redaction pass is a narrow mirror of the Go
  rules (credential-name assignments and the six real shapes), not a port
  of them; it redacts more rather than less. The host commands' bounds
  are 3 seconds and 4 KiB.

### 2.4 Not in this PR

- The switched solo lead gets the team tools and identity but not the
  team screen in the same process (`installScreen` runs at load for
  `AMQ_SQUAD_LEAD=1`); `amq-squad up` (the resume) has it.
- The lead `up` starts runs with `--tools` of `TeamLeadTools`' eleven, so
  pi's allowlist keeps `amq_task_claim` and `amq_task_done` out of its
  loadout, while a switched solo lead (no `--tools`) has the thirteen; the
  one-list follow-up settles which list is the team's.

## 3. Rows

| row | test |
|---|---|
| 2 | pi `phase.test.ts` "R53: the owner's yes switches the lead's tools to exactly the team lead's thirteen and refuses the solo set phase_team" (no marker file exists: the switch is on the start's reply; before the yes the solo tools answer and the team tools are not declared; after it the declared list is the thirteen, the identity is the squad's, a shell call, an edit, a second `amq_team_apply` and `amq_team_propose` refuse `phase_team`, and `amq_status` answers) |
| 2a, a planted marker | pi "R53: a planted marker switches nothing; the switch rides the lead's answers; a restart offers the way back" (a marker carrying the lead's own pid and stamp, with a reply that carries no team, switches nothing; a session start switches only on the lead's `team` answer; the offer is given once with its exact text); cli `TestThePhaseQueryAnswersFromTheLeadsOwnStartNotAFile` (a planted marker: the lead answers `solo`; after its own start, `team` with the root and digest) |
| 2c, the record | cli `TestAStartWhosePhaseRecordCannotBeWrittenStillSwitches` (phase.json a directory: started, the reply carries the identity, a `phase_record_failed` decision, the lead answers team); `TestAConfirmedSquadWithNoLeadRefusesBeforeAnythingStarts` (`team_identity`, no squad.yaml, no lock); pi "R53: the owner's yes switches ..." (phase.json a directory before the apply; the switch happens) |
| 2d, apply alone | pi "R53: amq_team_apply runs alone (apply_not_alone), and a solo tool run after the switch refuses phase_team" (apply batched with a shell call before it, after it, or with a read while the message's end goes unseen: `apply_not_alone` with the retry line, no apply verb run, the tools unchanged; alone it switches; the apply is a sequential tool; a propose prepared while solo and run after a switch refuses `phase_team`) |
| 2b, the restart | cli `TestASoloLeadRestartedInAMarkedRepositoryOffersTheWayBackAndStartsNothing` (a planted marker with this pid and the real stamp: the solo environment, no scheduler start, no lead lock, the offer on stderr; another repository's marker: no offer); `TestTheStartOfAConfirmedTeamRunsUpsSharedStepsAndNoInstallOrTmux` (the start's reply carries the team identity; the record is written; a second start refuses `lead_running`) |
| 4 | pi "the team lead's first turn carries the host facts from status and context, not the model" (the first turn carries the line; the second with nothing changed carries none and runs no verb; after a task change the line comes again with the new facts; a key change with the same facts sends nothing; a missing fact reads missing; a canary in a branch name that reached the extension is redacted); cli `TestContextHostCarriesTheHostFactsAndLeavesAMissingOneEmpty` (a canary in a branch name leaves as `<redacted>`), `TestContextHostKillsAHangingCommandsGroupAndNamesTheFactMissing` (a git that hangs with a child holding its output: context returns, branch and tip read missing, the child is dead; an amq printing past the cap: missing) |
| 5 | pi "R53: the owner's yes switches ..." (the declared list in team phase equals the thirteen exactly; no gate-answer or confirm tool), "R53: lead --team is team phase from its first turn with exactly the thirteen" |

No test reaches the network, the real HOME, tmux, AMQ or Buzz: the Go rows
run under TestMain's temp HOME with the lead's seams; the pi rows load the
whole extension into the stand-in pi with a fake amq-squad script, and
every tool call goes through the stand-in's batch: pi 1.3.0's agent loop
(every call prepared through the hooks, then the admitted ones run; one
by one when a tool is sequential). Canaries are built at run time and
match no `redact.RealCredentialShapes`.

## 4. The revert runs

`python3 docs/v3-spike/31-evidence/31-3-pr2-revert-runs.py` from a clean
checkout of the branch: each run reverts one change, runs its rows, and
restores every touched file to HEAD in a finally; it refuses a dirty tree,
and a row that hangs past 600 seconds counts as failing (none did: the
phase rows close their fake leads in a finally, so a failed assertion no
longer leaves a socket keeping node alive). The output, verbatim:

```
REVERT context --host ignored: fails (the row catches it)
   --- FAIL: TestContextHostCarriesTheHostFactsAndLeavesAMissingOneEmpty (0.07s)
   --- FAIL: TestContextHostKillsAHangingCommandsGroupAndNamesTheFactMissing (0.08s)
REVERT the branch not read from git: fails (the row catches it)
   --- FAIL: TestContextHostCarriesTheHostFactsAndLeavesAMissingOneEmpty (0.03s)
REVERT the host commands with no deadline: fails (the row catches it)
   --- FAIL: TestContextHostKillsAHangingCommandsGroupAndNamesTheFactMissing (15.05s)
REVERT the hanging command killed alone, not its group: fails (the row catches it)
   --- FAIL: TestContextHostKillsAHangingCommandsGroupAndNamesTheFactMissing (6.82s)
REVERT the host commands' output not capped: fails (the row catches it)
   --- FAIL: TestContextHostKillsAHangingCommandsGroupAndNamesTheFactMissing (0.80s)
REVERT a host fact leaving context --host unredacted: fails (the row catches it)
   --- FAIL: TestContextHostCarriesTheHostFactsAndLeavesAMissingOneEmpty (0.00s)
REVERT the AMQ version not read: fails (the row catches it)
   --- FAIL: TestContextHostCarriesTheHostFactsAndLeavesAMissingOneEmpty (0.02s)
REVERT the yes writing no phase marker (the record): fails (the row catches it)
   --- FAIL: TestTheStartOfAConfirmedTeamRunsUpsSharedStepsAndNoInstallOrTmux (0.07s)
REVERT a failed record not logged phase_record_failed: fails (the row catches it)
   --- FAIL: TestAStartWhosePhaseRecordCannotBeWrittenStillSwitches (0.15s)
REVERT a failed record withholding the team identity from the reply: fails (the row catches it)
   --- FAIL: TestAStartWhosePhaseRecordCannotBeWrittenStillSwitches (0.10s)
REVERT the identity not resolved before the start (no team_identity refusal): fails (the row catches it)
   --- FAIL: TestAConfirmedSquadWithNoLeadRefusesBeforeAnythingStarts (0.03s)
REVERT the start's reply without the team identity: fails (the row catches it)
   --- FAIL: TestTheStartOfAConfirmedTeamRunsUpsSharedStepsAndNoInstallOrTmux (0.08s)
REVERT the phase query answering team before the start: fails (the row catches it)
   --- FAIL: TestThePhaseQueryAnswersFromTheLeadsOwnStartNotAFile (0.03s)
REVERT a restarted solo lead not saying the offer: fails (the row catches it)
   --- FAIL: TestASoloLeadRestartedInAMarkedRepositoryOffersTheWayBackAndStartsNothing (0.02s)
REVERT a marker for another repository read: fails (the row catches it)
   --- FAIL: TestASoloLeadRestartedInAMarkedRepositoryOffersTheWayBackAndStartsNothing (0.02s)
REVERT the team tools declared before the yes: fails (the row catches it)
   not ok 1 - R53: the owner's yes switches the lead's tools to exactly the team lead's thirteen and refuses the solo set phase_team
REVERT the switch not changing the declared tools (no setActiveTools): fails (the row catches it)
   not ok 1 - R53: the owner's yes switches the lead's tools to exactly the team lead's thirteen and refuses the solo set phase_team
   not ok 2 - R53: a planted marker switches nothing; the switch rides the lead's answers; a restart offers the way back
   not ok 3 - R53: amq_team_apply runs alone (apply_not_alone), and a solo tool run after the switch refuses phase_team
REVERT no phase_team refusal of the solo set: fails (the row catches it)
   not ok 1 - R53: the owner's yes switches the lead's tools to exactly the team lead's thirteen and refuses the solo set phase_team
REVERT the lead's identity not switched to the squad's: fails (the row catches it)
   not ok 1 - R53: the owner's yes switches the lead's tools to exactly the team lead's thirteen and refuses the solo set phase_team
   not ok 2 - R53: a planted marker switches nothing; the switch rides the lead's answers; a restart offers the way back
REVERT the switch riding the marker file, not the lead's reply: fails (the row catches it)
   not ok 2 - R53: a planted marker switches nothing; the switch rides the lead's answers; a restart offers the way back
REVERT amq_team_apply's success not switching: fails (the row catches it)
   not ok 1 - R53: the owner's yes switches the lead's tools to exactly the team lead's thirteen and refuses the solo set phase_team
   not ok 3 - R53: amq_team_apply runs alone (apply_not_alone), and a solo tool run after the switch refuses phase_team
REVERT a session started after the yes not switching on the lead's answer: fails (the row catches it)
   not ok 2 - R53: a planted marker switches nothing; the switch rides the lead's answers; a restart offers the way back
   not ok 3 - R53: amq_team_apply runs alone (apply_not_alone), and a solo tool run after the switch refuses phase_team
REVERT amq_team_apply run beside other calls (no apply_not_alone): fails (the row catches it)
   not ok 3 - R53: amq_team_apply runs alone (apply_not_alone), and a solo tool run after the switch refuses phase_team
REVERT the assistant message's calls not in the batch: fails (the row catches it)
   not ok 3 - R53: amq_team_apply runs alone (apply_not_alone), and a solo tool run after the switch refuses phase_team
REVERT the hooks' prepared calls not in the batch: fails (the row catches it)
   not ok 3 - R53: amq_team_apply runs alone (apply_not_alone), and a solo tool run after the switch refuses phase_team
REVERT the batch not reset at a turn's start: fails (the row catches it)
   not ok 1 - R53: the owner's yes switches the lead's tools to exactly the team lead's thirteen and refuses the solo set phase_team
   not ok 3 - R53: amq_team_apply runs alone (apply_not_alone), and a solo tool run after the switch refuses phase_team
REVERT amq_team_apply not a sequential tool: fails (the row catches it)
   not ok 3 - R53: amq_team_apply runs alone (apply_not_alone), and a solo tool run after the switch refuses phase_team
REVERT the refusal not telling the model to call apply alone next turn: fails (the row catches it)
   not ok 3 - R53: amq_team_apply runs alone (apply_not_alone), and a solo tool run after the switch refuses phase_team
REVERT a solo tool's execute not rechecking the phase: fails (the row catches it)
   not ok 3 - R53: amq_team_apply runs alone (apply_not_alone), and a solo tool run after the switch refuses phase_team
REVERT the restart's offer not said: fails (the row catches it)
   not ok 2 - R53: a planted marker switches nothing; the switch rides the lead's answers; a restart offers the way back
REVERT the restart's offer said every turn: fails (the row catches it)
   not ok 2 - R53: a planted marker switches nothing; the switch rides the lead's answers; a restart offers the way back
REVERT no host line: fails (the row catches it)
   not ok 5 - the team lead's first turn carries the host facts from status and context, not the model
REVERT the verbs run every turn (no key): fails (the row catches it)
   not ok 5 - the team lead's first turn carries the host facts from status and context, not the model
REVERT an unchanged host line sent again: fails (the row catches it)
   not ok 5 - the team lead's first turn carries the host facts from status and context, not the model
REVERT the task store not in the host key: fails (the row catches it)
   not ok 5 - the team lead's first turn carries the host facts from status and context, not the model
REVERT context asked without --host: fails (the row catches it)
   not ok 5 - the team lead's first turn carries the host facts from status and context, not the model
REVERT closed tasks shown as open: fails (the row catches it)
   not ok 5 - the team lead's first turn carries the host facts from status and context, not the model
REVERT answered gates shown as open: fails (the row catches it)
   not ok 5 - the team lead's first turn carries the host facts from status and context, not the model
REVERT a host fact interpolated unredacted in the extension: fails (the row catches it)
   not ok 5 - the team lead's first turn carries the host facts from status and context, not the model
REVERT a missing fact left blank, not named missing: fails (the row catches it)
   not ok 5 - the team lead's first turn carries the host facts from status and context, not the model
40 runs, 40 caught, 0 missed
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
for the squad's status: `amq_status` answers from the squad's root. The
record:

```sh
cat ~/.amq-squad/solo/amq-squad-*/root/extensions/amq-squad/phase.json
```

shows `"phase": "team"`, the squad's root, session and lead handle, the
digest.

**B. A restart.** Quit the lead (Ctrl-C) and run `amq-squad lead` again in
the repository. stderr reads `amq-squad lead: this session's team was
started on <date> with digest <D>; run amq-squad up to resume it, or
confirm it again ...`; the first turn shows the same offer once; the tools
are the solo set; no scheduler runs:

```sh
amq-squad status --project /Users/omri/code/amq-squad --json
```

shows the scheduler not running. Then `amq-squad up` resumes the team.

**C. The first turn.** In a team lead (switched, or started by
`amq-squad up`), the first turn shows the host facts. Send a second
message with nothing changed: no host line. Add a task (`amq_task_add`)
and send another: the host line comes again with the task open. Compare
with:

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
- "The apply writes the phase marker": the lead's process writes it, and
  as a record only; the switch rides the lead's reply (2.1).
- "The host facts from status --json and context --json": neither carried
  the branch, the tip or the AMQ version; context gains `--host` (2.2).

## 7. One line for the 3.1 release notes

On your yes the lead becomes the team's lead for the rest of the
conversation: its tools switch to the team lead's (no shell, no edit; the
apply runs alone, so nothing it admitted before is still running), its
first turn carries the host facts read from status and context and they
come again when they change, and a restart of `amq-squad lead` comes up
solo and offers `amq-squad up` or a fresh yes.
