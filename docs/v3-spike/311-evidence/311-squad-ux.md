# 3.1.1-3: the lead's screen and its commands, seamless like a Claude agent team

Brief: 3.1.1-3 (the lead's copy). Authority: the owner, 2026-11-01, after
trying 3.1.0 ("the /squad thing is not clear enough imho"; "i want seamless
like claude agents teams so whoever leading can see easily"), the plan
reviewer's four points of the same day, and the 3.1.1 gate language (brief
3.1.1-1, #838) for the refusal style. Branch `v3-311-ux`, from
`c7b2f5af` (PR #854's reviewed head, 3.1.1-2, not yet merged); it merges
origin/v3 once #854 lands, never rebased. pi/test/phase.test.ts at the base
pins exactly the eleven team tools (the brief's precondition).

The test the work serves: whoever leads sees, without reading docs, who is
on the team, what each seat is doing now and what waits on them, and acts
on any of it by a command named for the action.

## 1. One command per action

pi/extensions/lib/commands.ts registers every command in one table
(`COMMANDS`), each a pi command of its own, never a subcommand of /squad:

| command | phase | description (pi's one line) | verb it runs |
| --- | --- | --- | --- |
| `/plan` | solo | show the proposed team plan again | none (the plan block) |
| `/confirm` | solo | confirm the proposed team plan: a dialog quotes it and your yes starts nothing but that plan | the lead's start socket, `amq-squad.confirm-tui.v1` |
| `/add` | team | add a task or a seat to the running team | `task add`; `team propose`/`team apply --add-seat`, `scheduler relaunch` |
| `/reset` | team | send a blocked task back to the queue, on its pool or another | `task reset` |
| `/dismiss` | team | acknowledge a seat's warning line (the seat itself does not change) | `scheduler clear` |
| `/answer` | team | show an open owner gate's question and where to answer it | `gate status`, `gate post` |
| `/widen` | team | accept a seat's request to touch files outside its task | `task show`, `task widen` |
| `/team` | both | show the team screen; /team hide hides it | none |
| `/squad` | both | the old name: lists the commands you can use now (it goes in 3.2) | none |

- The names were checked against pi 1.3.0's `BUILTIN_SLASH_COMMANDS`
  (settings model tree thinking scoped-models export import share bug copy
  name session changelog hotkeys fork clone trust login logout new compact
  resume reload quit), read from the installed pi at test time.
- `/add` offers `task` and `seat` through `getArgumentCompletions`; `/add
  task` and `/add seat` skip the choice. `/dismiss SEAT` offers that seat's
  warnings only.
- The lead started with `--team` registers the team commands, `/team` and
  `/squad`. The solo lead registers all nine: on the owner's yes it becomes
  the team's lead in the same process (R53), and its team commands, which
  refuse in plain words while solo ("There is no team yet, so /add has
  nothing to act on. The commands you can use now: ..."), then act. The
  switched solo lead now gets the team screen too (it had none in 3.1.0):
  the screen starts when the tools switch, and the plan block goes.
- A bare `/squad` toggles nothing and prints the phase's commands; `/squad
  <anything>` says "There is no /squad <x> any more". It goes in 3.2.
- The verbs are the 3.1.0 keys' verbs, unchanged. `/widen` is the widen
  key of brief 31-2 PR 3, built here on #844's surface: it reads the task's
  own record (`task show --json`: `refused_paths` and the seat's reason in
  `block_detail`), shows both in pi's confirm dialog, and runs `task widen`
  with exactly those paths, each as one `--path` (the lead's
  `amq_task_widen` argv), only on the dialog's yes; several requests open
  pi's select first. A successful widening says "added 2 files to t5; its
  commit step runs again", the count and the id from the record (the plan
  reviewer's wording): the paths are the seat's free text and can carry a
  long hex run, so they go below the rule (Codex on `ed3d1e65`), and the
  dialog showed them to the owner before the yes. It offers the tasks blocked `widen_requested` or
  `outside_declared_files`, the two blocks the verb accepts. The lead's
  recorded reason is fixed: "the owner accepted the seat's widen request at
  the lead's screen (/widen)".

## 2. The screen is on by default; its last line is the commands of now

Solo: `/plan  /confirm  /team hide`. Team: `/add  /reset  /dismiss  /answer
/widen  /team hide`. Both lines come from the one table (`commandsLine`).
The solo plan block now shows before any plan, with its empty line.

## 3. Each seat line says what the seat is doing

The handle, the runtime, model and effort, then the doing words from the
seat row's `reason_code` and `task_id` (`SEAT_DOING`, words.ts): "working
on t28: <title>" (lease claimed; the title from the task row the lease
names), "starting on t28: <title>" (a lease not yet claimed), "idle",
"waiting: on your answer to its gate", "waiting: on your answer to its
question", "waiting: t28 is blocked (its line is below)" (the seat holds a
blocked task), "starting" (no signal yet, launch staged or applied),
"stopped" (no live signal, or "stopped: its lease on t28 expired"). An
unknown code reads "waiting", its code below the rule.

**Choice (stated):** the launch timing moves to the end of the line in a
short form (`· up in 12.4s (over the 10s ceiling), first message 57.4s`),
plain text, no ANSI dim: the widget lines are plain strings, and an escape
code would show in every capture.

Task lines: "t28 in progress on reviewer: <title>", "t29 waiting: <words>"
(`WAIT_WORDS`), "t27 blocked: <what happened>; <what to do>"
(`BLOCK_WORDS`), "t9 held by the lead" (block reason `hold`), "t5 done:
<title>", "t5 cancelled: <title>". The dependency-merge wait of #859
("waiting for t27's PR #812 to merge", composed by Go from the recorded id
and PR number) shows verbatim. The size cap's numbers ("16 files over the
cap of 8") are parsed from the commit step's recorded `block_detail`
("size_cap_files: 16 changed files over the cap of 8"), which status does
not carry: the screen reads `task show --json` for a task blocked
`size_cap_exceeded`, once per change of the task's `updated_at`; with no
detail on record it says only "the diff is over the size cap". No status
field was added, so docs/status-contract.md is unchanged.

**A failed launch's code (Codex on `ed3d1e65`).** internal/scheduler
launchfail.go makes a failed launch's `Code()` (its reason code, else its
stage) the wait reason of the pool's pending tasks (`waitForSeat`) and, on a
second failure with the same code, their block reason
(`blockOnLaunchFailure`). The first version had none of those codes in its
tables, so such a task read only "waiting". `LAUNCH_WORDS` now names every
code amq-squad's own launch path can give: the stages (`launch_error`,
`apply_failed`, `prepare_refused`, `prepared`, `seat_not_up`,
`decisions_pending`, `resource_present`), the launcher's codes and seat
states (`apply_ambiguous`, `not_observed`, `no_proof`, `not_started`,
`pending`, `refused`), the scheduler's roster codes, seat-exec's refusals,
the proof server's, and the Claude and pi checks. The wait reads "t30
waiting: its seat's launch failed, AMQ's apply failed; the scheduler tries
once more, then blocks it for the lead", the block "t32 blocked: its seat's
launch failed twice, its proof came after its token expired; fix the cause,
then /reset it (or relaunch the roster)". AMQ's own reason codes pass
through AMQ's launch API as a plain string, an open set with no list in the
API (launchapi), so they cannot all be named here: a wait or block reason
the decision log recorded for a failed launch (`seat_launch_failed`'s
reason code and stage) reads "its seat's launch failed (AMQ's reason is
below the line)", the code below the rule. The seat stop line of a launch
decision names its stage in words ("seat rev-1's launch failed: AMQ's apply
failed; ask the lead to relaunch the roster").

**Every lookup is an own-property lookup (Codex on `ed3d1e65`).** The
tables are plain objects, and a reason named `constructor` found Object's
own property: the destructuring threw, the render's catch swallowed it and
the screen went stale. Every table lookup now goes through `own()`
(`Object.hasOwn`), so `constructor`, `__proto__`, `toString` and
`hasOwnProperty` as a wait, block, seat, warning, decision, train-rule or
relay code render the fallback line.

Titles pass the extension's redaction (`redactFact`, the second layer
after Go's `redact.Block` in status) and a control-character strip before
the 60-character cut. Each block has one plain line when empty: "No seat is
running yet.", "No tasks yet; add one with /add.", "No merge train has run
yet.", "Nothing is waiting on you.", and solo "No team yet. Tell me what you
want built and I will propose a plan." The gates block is now "waiting on
you": each open gate's plain question, first line, "(answer on the phone;
/answer shows it)"; the gate id, action, digest prefix and age go below the
rule.

**Choice (stated):** "waiting on you" lists the open gates; a task blocked
for the owner already has its own task line with what to do, so it is not
repeated there.

**Seat warnings, the plan reviewer's change on `ed3d1e65`.** The first
version kept a seat's warning lines as Go's text (`    ! <code>: <detail>`);
the plan reviewer refused that: it is the language the owner asked to
retire and the first thing a new lead reads. Each warning above the rule
is now plain words from the one table (`WARNING_WORDS`, one row for each
of the seven codes of Go's `schedproc.ClearableWarnings`, the list status
keeps equal to its own): what the warning says and what to do, composed
from its recorded fields (the launch timing's `to_seat_live_ms`,
`ceiling_ms` and `slowest_segment`; the dialog's open duration and the
provider model named in the detail). Below the rule each stays verbatim,
`<seat>: <code>: <detail>`. An unmapped code at run time reads "warning;
its code is below the line". `/dismiss` picks the seat first (pi's select
when several seats have warnings; `/dismiss SEAT` names it), then the
warning by its words when the seat has several; the code the socket
request carries is read from status's record of the warning, never typed.
The phone requests got a plain first line ("a phone request to cto, not
answered yet: the relay held back a reply shaped like a gate answer; the
lead answers again in words", `RELAY_WORDS`); their 3.1.0 rows, ids and
refusal names, now sit below the rule.

## 4. Refusals read like the 3.1.1 gate questions

Every key outcome is a plain line on the screen ("/reset: Nothing to reset:
no task is blocked.", "/dismiss: Nothing to dismiss: seat dev has no
warning showing.", "/reset: t1 was not reset: task t1 was granted attempt
2; ..."), then, below a rule above the screen's last line, the record: the
verb's own text as it printed it and, when the verb printed its own rule,
the codes below that. A verb's text is shown above the rule only after its
leading `verb: code:` segments and parenthesized codes are taken out, and
only if what is left carries no code or hex run (`plainDetail`). The
stop lines say what happened and what to do, both from the tables (the plan
reviewer's second change; "last stop" was our own term and said no what to
do): the newest seat stop is "<what happened>: <the reason's words>; <what
to do>" when the decision's reason code is in the block table, else
"<what happened>; <the decision's own what to do>" (`decisionWords`,
`DECISION_WORDS`), so the fixture's `exit_refused` with reason
`ledger_tampered at gone` reads "seat old-dev did not stop cleanly: the
commits ledger does not verify; ask the owner to inspect the commits ledger
before anything commits". The train's line parses the run's refusal
("refusal PR #N RULE[,RULE]: detail", Go's internal/status screen.go) and
reads "the merge train stopped at PR #812: its CI is not green; fix the
failing check and the train tries again" from `TRAIN_RULE_WORDS`, one row
per rule of internal/mergetrain checks.go. Both records stay verbatim below
the rule ("exit_refused: old-dev ledger_tampered at gone", "train r2:
refusal PR #812 ci_green: ...").

The four named lines:

- internal/cli/task_widen.go, `widenRefused.Error` (was `widen refused
  (%s): %s`): a plain line by code (`widenPlain`), the rule, then `widen
  refused (<code>): <detail>`. The growth gate's plain line is composed from
  the decision's own record (the plan reviewer on #863): with no raise error
  in `GateErrs` "the owner's growth gate is raised; the task waits for the
  answer", with one "the widening needs the owner's growth gate, and raising
  it failed; try /widen again", the error below the rule. The raise goes
  through a seam in task_widen.go (`widenRaiseGrowthGate`, the
  seat_commit.go `raiseGrowthGate` it wraps is outside the declared files).
- internal/cli/task_widen.go, the verb's refusal from the socket reply
  (was `task widen %s refused (%s): %s`): `task widen <id> was refused:
  <plain>`, the rule, `refused (<code>): <detail>` (`widenReplyRefusal`;
  the plain part is the widening's own above its rule, or the socket's
  refusal in words by code).
- internal/mergetrain/drift.go, the `gate_store_unreadable` alert: "a gate
  store of this squad could not be read, so no gate in it clears an alert;
  fix or remove the record named below the line", the rule, then the path
  and the error. The screen shows an alert's plain part and puts its kind
  and the rest below its rule.
- pi/extensions/lib/tools.ts `refusedPaths` (around :143): the plain line,
  the rule, then `state <s>, reason <r>`; the empty-paths refusal likewise.

No code, route or logic changed in the Go files: only message text, and the
plain-line lookups by the existing codes. The two widen rows run the verb
itself (Codex on `ed3d1e65`: the first version called the formatting
function directly, so reverting only the call in `runTaskWiden` left it
green, and its revert case rewrote the test): `widenThroughTheVerb` blocks
a task in a fresh store, stands up the scheduler's decision socket with the
existing `leadServer` seam (the sender the lead's pane, or not), runs
`runTaskWiden` and reads its coded refusal. The widenRefused row checks the
verb's plain line is the widening's own words (`widen_not_requested`,
`widen_paths_missing`), so reverting `widenRefused.Error` fails it; the
verb's row checks the socket's `not_lead` refusal in words. Each revert case
reverts production code only. The existing Go rows that read the
old text by substring (brief3112_test.go, seat_fix1b_test.go,
brief3112_test.go in mergetrain) still pass: their substrings are now below
the rule.

## 5. /confirm with nothing to type

`/confirm` opens pi's `ui.confirm` titled "Start this team?" quoting the
plan's goal line, the session and lead line, and the seat lines (runtime,
model and effort), then what the yes does. The plan it quotes is fixed
before the dialog opens; the yes sends exactly that digest over the start
socket. A proposal made while the dialog is open is not the one confirmed;
a no or a cancel sends nothing. With two live proposals (this pi's plans
younger than the confirmation's 30 minutes, the lead's own window) pi's
`ui.select` over their goal lines comes first. The pane door's proof is
unchanged (the lead accepts a confirm only from its own pi by pid and spawn
stamp, `askLeadProved`). The digest stays on the plan block below the rule.
The plan text the extension returns to the model (and sends as the Buzz
reply) now names `/confirm` in place of Go's 3.1.0 doors line ("type /squad
confirm <prefix>"); its `digest` and `prefix` lines stay for the model and
the Buzz door's `yes <prefix>` form.

**The plan reviewer on #863.** A plan line is the model's free text: Go's
`oneLine` (internal/team plan.go) folds only whitespace, so an ESC or another
control character in a goal or a task title passed through to the plan
block, the confirm dialog and the select's labels, and the dialog is where
the owner's yes binds. Every plan line now passes words.ts `clean()` in all
three places (redaction and the control-character strip; the cut 300
characters for a line, 120 for a label). The select's labels are numbered
("1. goal: ...", "2. ..."): two plans with the same goal proposed in the
same second had the same label, and `labels.indexOf(pick)` mapped the pick
to the first of them. The dialog also gives the plan's size, counted from
its own lines: "and 1 task over 1 file" (team.PlanLines' `task N:` lines and
its `files:` line). There is still no cost line: team.PlanLines prints none.

## 6. Captures (plain text, fakepi)

The whole extension loaded in the stand-in pi as `lead --team` and as the
solo lead, a fake amq-squad answering `status --json` from
pi/test/status-fixture.json and `team propose` with a two-seat plan; the
widget lines as pi holds them. Before is the base `c7b2f5af`, after this
branch. The ages read 81h because the fixture is dated 2026-11-01.

### Before

```
=== team phase: the lead's screen ===
amq-squad: lead cto (pid 4300, pane %3) · root /Users/owner/code/app/.agent-mail/v31
seats (3)
  qa  codex/gpt-5.6-sol  online/idle  live  live 12.4s OVER first-msg 57.4s
    ! launch_budget_exceeded: 12.400 s from the start to seat live, over the 10s ceiling; slowest segment seat_exec 8.059 s
    ! wake_off: amq-squad: no AMQ wake for this Codex seat: AMQ's Codex dialog hook is not on the host; the seat idles afte...
  scribe  claude/sonnet  online/lease_claimed  live  live 4.7s first-msg -
    ! seat_stalled_at_dialog: a harness dialog has been open since 2026-11-01T09:02:00Z (3m0s); the seat waits at it and AM...
  pi-dev  pi/anthropic/claude-sonnet-5  online/idle  live  live 4.5s first-msg -
    ! pi_provider_key_missing: model anthropic/claude-sonnet-5 needs one of ANTHROPIC_API_KEY, ANTHROPIC_OAUTH_TOKEN, ANTHR...
  refusal: exit_refused: old-dev ledger_tampered at gone
tasks (3)
  t1  blocked  -  PR -  outside_declared_files
  t2  in_progress  scribe  PR #812
  t26  pending  -  PR -  waiting on t20, t23
  refusal: commit_refused: t1 outside_declared_files: src/x.go
train
  current: r2 refusal 81h ago, PRs #811,#812, merged 1
  last: r1 merge 82h ago, PRs #810, merged 1
    ! ungated_tag: tag v1.2.0 has no accepted gate
  refusal: refusal PR #812 checks: a required check failed
gates (1 open)
  Create a tag named v1?
  Asked by qa.
  Yes lets it run. No stops it.
    g1  tag  8502aa0011223344  age 81h  waiting on Buzz
  refusal: none
requests (2)
  q-7c41d09e2a55  cto  not relayed  refused: relay_refused_gate_shape
  q-3f2a9c1e7b04  cto  relayed 81h ago
keys: /squad add | /squad reset | /squad clear | /squad answer
=== solo phase, no plan yet: the plan block ===
(no widget: the block shows nothing until a plan)
=== solo phase, a plan proposed: the plan block ===
amq-squad: proposed team
  goal: ship the parser
  session s1, lead lead
  seat lead (lead) pi
  seat impl-1 (implementer) claude/sonnet/medium
  files: internal/parser/
  task 1: write the parser [implement]
  digest sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef@absent
  prefix 0123456789abcdef
  confirm: /squad confirm 0123456789abcdef  (or on the phone: reply yes to the lead's Buzz post of this plan)
```

### After

At `29ab4513` (unchanged since `1d2ad62a`, the plan reviewer's two changes). The fixture's train
refusal now names a real rule (`ci_green`; it said `checks`, which is no
rule), so the before capture keeps the old text.

```
=== team phase: the lead's screen ===
amq-squad: lead cto (pid 4300, pane %3) · root /Users/owner/code/app/.agent-mail/v31
seats (3)
  qa  codex gpt-5.6-sol high  idle  · up in 12.4s (over the 10s ceiling), first message 57.4s
    ! it came up in 12.4s, over the 10s ceiling (slowest step: the seat's own start); nothing to do unless it repeats, and /dismiss hides it once you know
    ! AMQ does not ring it when mail arrives, so it idles after each turn; ring it by hand, and /dismiss hides it once you know
  scribe  claude sonnet medium  working on t2: Write the notes  · up in 4.7s
    ! it is stopped at a harness dialog open for 3m0s; answer the dialog in its pane
  pi-dev  pi anthropic/claude-sonnet-5 medium  idle  · up in 4.5s
    ! its model anthropic/claude-sonnet-5 has no provider key, so it stalls at its first turn; add the key to its env needs or its pi login, then relaunch
  seat old-dev did not stop cleanly: the commits ledger does not verify; ask the owner to inspect the commits ledger before anything commits
tasks (3)
  t1 blocked: the commit step refused it, it changed files outside its task; accept them with /widen or send it back with /reset
  t2 in progress on scribe: Write the notes  PR #812
  t26 waiting: t20, t23 hold its files
train
  current: r2 refusal 82h ago, PRs #811,#812, merged 1
  last: r1 merge 83h ago, PRs #810, merged 1
    ! tag v1.2.0 has no accepted gate
  the merge train stopped at PR #812: its CI is not green; fix the failing check and the train tries again
waiting on you (1)
  Create a tag named v1?  (answer on the phone; /answer shows it)
phone requests (2)
  a phone request to cto, not answered yet: the relay held back a reply shaped like a gate answer; the lead answers again in words
  a phone request to cto, answered 82h ago
----------
  qa: launch_budget_exceeded: 12.400 s from the start to seat live, over the 10s ceiling; slowest segment seat_exec 8.059 s
  qa: wake_off: amq-squad: no AMQ wake for this Codex seat: AMQ's Codex dialog hook is not on the host; the seat idles after each turn until it is rung by hand
  scribe: seat_stalled_at_dialog: a harness dialog has been open since 2026-11-01T09:02:00Z (3m0s); the seat waits at it and AMQ holds its doorbell
  pi-dev: pi_provider_key_missing: model anthropic/claude-sonnet-5 needs one of ANTHROPIC_API_KEY, ANTHROPIC_OAUTH_TOKEN, ANTHROPIC_AUTH_TOKEN, and none is in the seat's env needs or its pi login; th...
  exit_refused: old-dev ledger_tampered at gone
  ungated_tag
  train r2: refusal PR #812 ci_green: a required check failed
  g1  tag  8502aa0011223344  age 82h  waiting on Buzz
  q-7c41d09e2a55  cto  not relayed  refused: relay_refused_gate_shape
  q-3f2a9c1e7b04  cto  relayed 82h ago
/add  /reset  /dismiss  /answer  /widen  /team hide
=== solo phase, no plan yet: the plan block ===
amq-squad: solo lead
  No team yet. Tell me what you want built and I will propose a plan.
/plan  /confirm  /team hide
=== solo phase, a plan proposed: the plan block ===
amq-squad: proposed team
  goal: ship the parser
  session s1, lead lead
  seat lead (lead) pi
  seat impl-1 (implementer) claude/sonnet/medium
  files: internal/parser/
  task 1: write the parser [implement]
  Confirm it with /confirm (a dialog quotes the plan; there is nothing to type), or on the phone reply yes to the lead's Buzz post of this plan.
----------
  digest sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef@absent
/plan  /confirm  /team hide
```

### A failed launch, a reason named constructor and a /widen (after)

At `29ab4513`, the fixture with launch failure waits and blocks, AMQ's own
code recorded by the decision log, a block reason named `constructor`, and
a successful `/widen` as the last outcome:

```
amq-squad: lead cto (pid 4300, pane %3) · root /Users/owner/code/app/.agent-mail/v31
seats (3)
  qa  codex gpt-5.6-sol high  idle  · up in 12.4s (over the 10s ceiling), first message 57.4s
  scribe  claude sonnet medium  working on t2  · up in 4.7s
  pi-dev  pi anthropic/claude-sonnet-5 medium  idle  · up in 4.5s
  seat rev-1's launch failed: AMQ's apply failed; ask the lead to relaunch the roster
tasks (4)
  t30 waiting: its seat's launch failed, AMQ's apply failed; the scheduler tries once more, then blocks it for the lead
  t31 waiting: its seat's launch failed (AMQ's reason is below the line); the scheduler tries once more, then blocks it for the lead
  t32 blocked: its seat's launch failed twice, its proof came after its token expired; fix the cause, then /reset it (or relaunch the roster)
  t33 blocked; its reason is below the line
train
  No merge train has run yet.
waiting on you (0)
  Nothing is waiting on you.
  /widen: added 2 files to t5; its commit step runs again
----------
  version_probe_failed: rev-1 AMQ refused the apply
  t31 waits: version_probe_failed
  t33 blocked: constructor
  /widen: t5 widened by src/helper.go, src/helper_test.go: the owner accepted the seat's widen request at the lead's screen (/widen)
/add  /reset  /dismiss  /answer  /widen  /team hide
```

### The dialogs and a refusal (after)

At `00ccfcda`:

```
=== /confirm: pi's confirm dialog, title "Start this team?" ===
goal: ship the parser
session s1, lead lead
seat lead (lead) pi
seat impl-1 (implementer) claude/sonnet/medium
and 1 task over 1 file

Yes confirms exactly this plan for 30 minutes; the lead then starts the team. A plan proposed after this dialog opened is not the one confirmed.
=== /widen: pi's confirm dialog ===
title "widen t5?"
t5: The helper
Its seat asks to touch these files outside its task:
  src/helper.go
  src/helper_test.go
The seat's reason: the parser needs the shared helper
Yes adds exactly these paths to the task's files; No changes nothing.
=== /widen refused by the verb: the screen's outcome line and its record ===
  /widen: t5 was not widened: task widen t5 was refused: the widened files overlap another active task's files; widen it once that task is done
----------
  /widen: t5: task widen t5 was refused: the widened files overlap another active task's files; widen it once that task is done (refused (file_lock_conflict): widen refused (file_lock_conflict): src/helper.go is t7's)
```

## 7. Rows

| row | what | where |
| --- | --- | --- |
| 1 | every action its own command with a one-line plain description; no pi built-in name (pi's `BUILTIN_SLASH_COMMANDS` at test time); bare `/squad` toggles nothing and prints the phase's commands | pi/test/commands.test.ts "row 1" |
| 2 | the screen's last line lists exactly the current phase's commands, both phases, and after the switch | pi/test/commands.test.ts "row 2" (two tests) |
| 3 | a seat with a lease "working on <id>: <title>", idle, starting, stopped; a waiting task's plain reason; a blocked task what happened and what to do | pi/test/words.test.ts "row 3"; pi/test/screen.test.ts |
| 4 | no refusal or outcome line above its rule carries a snake_case code or a hex run; the code is below it: the screen over every reason and seat code, every seat warning code, every stop kind, the train's rules and the relay's names, every key's refusals, the plan block, a verb's own below-rule code; each warning in words with its code below; the stop lines with what happened and what to do; tools.ts:143; the three Go lines | pi/test/words.test.ts "row 4" (four tests); pi/test/keys.test.ts "dismiss" (by seat); pi/test/keys.test.ts (widen refusal record; a widening's paths below the rule); internal/cli/task_widen_test.go `TestTheWidenRefusalPutsItsCodeBelowTheRule`, `TestTheWidenVerbsRefusalPutsItsCodeBelowTheRule` (both through the verb), `TestTheGrowthGateLineSaysWhatTheRaiseDid` (through applyLeadWiden, the raise stubbed to fail); internal/mergetrain/drift_test.go `TestTheGateStoreUnreadableAlertPutsThePathBelowTheRule` |
| 5 | `/confirm`'s yes confirms the digest the dialog showed; a re-propose while it is open changes nothing; a no or a cancel confirms nothing; two proposals: select, then the dialog; plans with the same goal: the label picked confirms that plan; a goal with a canary and ESC[2K shows redacted and stripped on the block, the dialog and the label, and the dialog gives the plan's size; a model message containing "/confirm" invokes nothing (the 31-3 row kept) | pi/test/team.test.ts "/confirm" (four tests), "a model message containing /confirm" |
| 6 | the lead's team tools are Go's eleven; no command is a tool | pi/test/commands.test.ts "row 6"; pi/test/phase.test.ts (unchanged) |
| 7 | every wait and block reason in Go's sources (read at test time, the scan below), every launch code, seat code, train rule and relay refusal name maps to words in one table; a failed launch's code reads as a launch failure, AMQ's through the decision log; a reason named `constructor` or `__proto__` renders the fallback; an unmapped reason falls back with its code below the rule; the size cap's numbers from the record; the dependency-merge wait verbatim; a title with a canary and a control character redacted and stripped; each empty block one plain line | pi/test/words.test.ts "row 7" (seven tests) |
| 8 | `/widen` shows the requested paths and the seat's reason from the task's record, runs `task widen` only on the dialog's yes, picks among several, and refuses in plain words when no task is blocked with a widen request | pi/test/keys.test.ts "widen" |

What the row 7 scan covers (Codex on `ed3d1e65`: it read only constants
named `Wait*` in a fixed list of files, so a typed constant, an alias or a
file not listed passed silently). It reads the Go sources at test time:

- **Waits:** every `Wait*` constant, typed or not, in every non-test file of
  internal/task, internal/seatcommit, internal/scheduler and
  internal/routing, and every literal wait (`.wait(t, "...")`, `WaitReason =
  "..."`).
- **Blocks:** every `Reason*`, `Block*` and `Refusal*` constant, typed or
  not, in the same four packages, every literal assigned to a block field
  (`Block: "..."`, `BlockReason = "..."`, a `Block`, `BlockWithPaths`,
  `RefuseCommit`, `RefuseCommitPaths`, `Escalate` or `SetWait` call with a
  literal reason), `AMQBindingPresent` and the pin check's `PinMismatch`.
  Two are left out by name, the widen refusals `file_lock_conflict` and
  `self_widen` (task/scope.go): replies of the widen verb, never a task's
  block. An alias of a constant has the constant's value and is covered by
  it.
- **Launch codes:** in every non-test file of internal/seatexec and
  internal/seatlaunch, internal/scheduler launchfail.go and roster.go,
  internal/piruntime session.go, refusal.go and launch.go, and
  internal/claudeseat claudeseat.go and token.go: the literal codes of
  `refusef`, `asRefusal`, `ResumeRefusal{...}`, `Code:`, the proof server's
  `forged` and `reject`, `.refused =`, `Stage` (assigned, set or compared),
  `State:`, the seat-state assignments, `launchFailure`'s returns and the
  `firstNonEmpty` defaults, and the code constants. Not codes: the success
  stages `applied` and `up`, and `none`, roster.go's message placeholder.
- **Seat codes:** `classifySeat`'s reason codes (internal/status). **Seat
  warnings:** `schedproc.ClearableWarnings`. **Train rules:**
  internal/mergetrain checks.go `Rule*`. **Relay names:** internal/status
  relay.go and internal/buzz relay.go `Relay*`.

The wider scan found three codes the table lacked: `repin_after_attempt`
(task lease.go), `evidence_ref_symbolic` (seatcommit evidence_purge.go) and
`routing_invalid` (routing open.go); all three are mapped. Open by design: a
block reason a seat or the lead writes as free text (`task block --reason`)
reads "<id> blocked; its reason is below the line", and AMQ's own launch
codes read as launch failures through the decision log (above).

## 8. Revert runs

`python3 docs/v3-spike/311-evidence/311-squad-ux-revert-runs.py` from a
clean checkout, at code head `00ccfcda`: 56 runs, 56 caught. Every case
reverts production code only and runs the row that must catch it. The
history: 32 at `c7105890`; 12 for the plan reviewer's warning, stop and
request rows (cases 16 to 27); six for Codex's gaps on `ed3d1e65` (cases 28
to 33); six for the plan reviewer's three fixes on #863 (cases 34 to 39:
each of the three raw plan-line places, unnumbered labels, the dialog's
size line, the growth gate's line). Case 13 was redone for Codex: it now
reverts only the call in `runTaskWiden`. Two runs missed before their row
was strengthened, both recorded: at `c7105890` row 8 offered `/widen` only
tasks blocked for something (the row has had a task blocked for another
reason since `154444eb`), and on #863 case 37 (unnumbered labels) was not
caught by a two-plan row, since the newest plan's label carries "the
newest" and two labels differ anyway; the row now uses three plans, where
the two older labels collide (`00ccfcda`). Case 31 shows the crash Codex
described: "f is not a function or its return value is not iterable".

| # | row | revert | result | the failing output's first line |
| --- | --- | --- | --- | --- |
| 1 | 1 | the actions not registered (only /squad and /team) | caught | `not ok 1 - row 1: one command per action, a one-line plain description each, no pi built-in name` |
| 2 | 1 | a command named for a pi built-in (/copy for /dismiss) | caught | `not ok 1 - row 1: one command per action, a one-line plain description each, no pi built-in name` |
| 3 | 1 | the 3.1.0 description (presses a key) | caught | `not ok 1 - row 1: one command per action, a one-line plain description each, no pi built-in name` |
| 4 | 1 | bare /squad toggles the screen | caught | `not ok 1 - row 1: one command per action, a one-line plain description each, no pi built-in name` |
| 5 | 2 | the 3.1.0 keys line on the team screen | caught | `not ok 2 - row 2: the screen's last line lists exactly the current phase's commands` |
| 6 | 2 | the plan block without the commands line | caught | `not ok 2 - row 2: the screen's last line lists exactly the current phase's commands` |
| 7 | 3 | the seat line's state/reason_code (3.1.0) | caught | `not ok 1 - row 3: a seat says what it is doing, a task why it waits or what stopped it and what to do` |
| 8 | 3 | a blocked task's line by its code | caught | `not ok 1 - row 3: a seat says what it is doing, a task why it waits or what stopped it and what to do` |
| 9 | 4 | a refusal's verb text above the rule (sentence without the strip) | caught | `not ok 11 - row 4: no line above the rule carries a snake_case code or a hex run; the code is below it` |
| 10 | 4 | the gate line with its id and prefix above the rule | caught | `not ok 11 - row 4: no line above the rule carries a snake_case code or a hex run; the code is below it` |
| 11 | 4 | tools.ts:143, the state and reason first (3.1.1-2 form) | caught | `not ok 12 - row 4: the lead's widen refusal (tools.ts) puts its state and reason below the rule` |
| 12 | 4 | task_widen.go widenRefused.Error, the `widen refused (%s): %s` form | caught | `--- FAIL: TestTheWidenRefusalPutsItsCodeBelowTheRule (0.15s)` |
| 13 | 4 | task_widen.go the verb's `refused (%s)` form at its call (runTaskWiden), production only | caught | `--- FAIL: TestTheWidenRefusalPutsItsCodeBelowTheRule (0.14s)` |
| 14 | 4 | drift.go the gate_store_unreadable alert with the path and error in its line | caught | `--- FAIL: TestTheGateStoreUnreadableAlertPutsThePathBelowTheRule (0.01s)` |
| 15 | 4 | a verb's below-rule code dropped from the record | caught | `not ok 11 - widen: the dialog shows the requested paths and the seat's reason; only its yes runs task widen` |
| 16 | 4 | a seat warning above the rule as the 3.1.0 line (! code: detail) | caught | `not ok 1 - row 4: every seat warning code in Go's list has plain words; the code is below the rule` |
| 17 | 4 | a warning code dropped from the table (wake_off) | caught | `not ok 1 - row 4: every seat warning code in Go's list has plain words; the code is below the rule` |
| 18 | 4 | an unmapped warning shown by its code above the rule | caught | `not ok 1 - row 4: every seat warning code in Go's list has plain words; the code is below the rule` |
| 19 | 4 | a warning's code and detail not kept below the rule | caught | `not ok 1 - row 4: every seat warning code in Go's list has plain words; the code is below the rule` |
| 20 | 4 | /dismiss without the seat choice | caught | `not ok 1 - dismiss: the acknowledgement names the warning and the launch; the line leaves the screen; the seat's state does not` |
| 21 | 4 | /dismiss offers a seat's warnings by their codes | caught | `not ok 1 - dismiss: the acknowledgement names the warning and the launch; the line leaves the screen; the seat's state does not` |
| 22 | 4 | the seat's stop line as the 3.1.0 'last stop' words | caught | `not ok 1 - row 4: a seat's stop and the train's stop say what happened and what to do` (read from a run by hand; the script's parse missed the line) `` |
| 23 | 4 | the seat's stop line without what to do | caught | `not ok 1 - row 4: a seat's stop and the train's stop say what happened and what to do` (read from a run by hand; the script's parse missed the line) `` |
| 24 | 4 | the train's stop line as the 3.1.0 refusal text | caught | `not ok 2 - row 4: a seat's stop and the train's stop say what happened and what to do` |
| 25 | 4 | a phone request's id and refusal names above the rule | caught | `not ok 1 - row 4: no line above the rule carries a snake_case code or a hex run; the code is below it` |
| 26 | 7 | a train rule dropped from the table (ci_green) | caught | `not ok 1 - row 7: every wait and block reason in Go's sources maps to plain words in the one table` |
| 27 | 7 | a relay refusal name dropped from the table | caught | `not ok 1 - row 7: every wait and block reason in Go's sources maps to plain words in the one table` |
| 28 | 7 | a launch code dropped from the launch table (apply_failed) | caught | `not ok 1 - row 7: every wait and block reason in Go's sources maps to plain words in the one table` |
| 29 | 7 | a wait reason that is a launch code not looked up in the launch table | caught | `not ok 1 - row 7: a launch failure's code as a wait or block reason reads in words` |
| 30 | 7 | AMQ's own launch code not recognized through the decision log | caught | `not ok 1 - row 7: a launch failure's code as a wait or block reason reads in words` |
| 31 | 7 | a table lookup that finds an inherited property (no own-property check) | caught | `not ok 1 - row 7: a reason named constructor or __proto__ renders the fallback line` |
| 32 | 4 | a launch decision's stage not read for the stop line | caught | `not ok 1 - row 4: a seat's stop and the train's stop say what happened and what to do` (read from a run by hand; the script's parse missed the line) `` |
| 33 | 4 | a successful /widen lists its paths above the rule | caught | `not ok 1 - row 4: no line above the rule carries a snake_case code or a hex run; the code is below it` |
| 34 | 5 | the plan block's lines raw (no clean) | caught | `not ok 1 - /confirm: a goal with a canary and ESC[2K shows redacted and stripped on the block, the dialog and the label` |
| 35 | 5 | the confirm dialog's lines raw (no clean) | caught | `not ok 1 - /confirm: a goal with a canary and ESC[2K shows redacted and stripped on the block, the dialog and the label` |
| 36 | 5 | the select's goal label raw (no clean) | caught | `not ok 1 - /confirm: a goal with a canary and ESC[2K shows redacted and stripped on the block, the dialog and the label` |
| 37 | 5 | the select's labels unnumbered (two plans with one goal share a label) | caught | `not ok 1 - /confirm: plans with the same goal: the label picked confirms that plan` |
| 38 | 5 | the dialog without the plan's size line | caught | `not ok 1 - /confirm: a goal with a canary and ESC[2K shows redacted and stripped on the block, the dialog and the label` |
| 39 | 4 | the growth gate's line claims the raise whatever GateErrs says | caught | `--- FAIL: TestTheGrowthGateLineSaysWhatTheRaiseDid (0.83s)` |
| 40 | 5 | the confirm reads the newest plan after the dialog | caught | `not ok 5 - /confirm: a re-propose while the dialog is open does not change what is confirmed; two plans: select, then the dialog` |
| 41 | 5 | the dialog's no confirms anyway | caught | `not ok 4 - /confirm: the dialog's yes asks the lead to witness the plan it showed; a no sends nothing` |
| 42 | 5 | two live plans: no select, the newest confirmed | caught | `not ok 5 - /confirm: a re-propose while the dialog is open does not change what is confirmed; two plans: select, then the dialog` |
| 43 | 5 | a confirm tool beside the command (a model message could confirm) | caught | `not ok 6 - a model message containing /confirm reaches no lead; the confirm is a command, never a tool` |
| 44 | 6 | an action registered as a tool too | caught | `not ok 4 - row 6: the team lead's tools are Go's eleven and no command is a tool` |
| 45 | 7 | a block reason dropped from the table (verdict_failed) | caught | `not ok 1 - row 7: every wait and block reason in Go's sources maps to plain words in the one table` |
| 46 | 7 | a wait reason dropped from the table (seat_cap) | caught | `not ok 1 - row 7: every wait and block reason in Go's sources maps to plain words in the one table` |
| 47 | 7 | a seat code dropped from the table (idle) | caught | `not ok 1 - row 7: every wait and block reason in Go's sources maps to plain words in the one table` |
| 48 | 7 | the size cap's numbers guessed away (no detail read) | caught | `not ok 4 - row 7: the size cap's numbers come from the recorded detail through task show` |
| 49 | 7 | the screen reads no task record (no detail fetch) | caught | `not ok 4 - row 7: the size cap's numbers come from the recorded detail through task show` |
| 50 | 7 | a title without the redaction | caught | `not ok 7 - row 7: a title with a canary and a control character shows redacted and stripped` |
| 51 | 7 | a title without the control-character strip | caught | `not ok 7 - row 7: a title with a canary and a control character shows redacted and stripped` |
| 52 | 7 | an empty seats block shows nothing | caught | `not ok 8 - row 7: each empty block says so in one plain line` |
| 53 | 7 | the hold not mapped (held by the lead) | caught | `not ok 1 - row 7: every wait and block reason in Go's sources maps to plain words in the one table` |
| 54 | 8 | /widen runs task widen whatever the dialog answers | caught | `not ok 11 - widen: the dialog shows the requested paths and the seat's reason; only its yes runs task widen` |
| 55 | 8 | the dialog without the seat's reason | caught | `not ok 11 - widen: the dialog shows the requested paths and the seat's reason; only its yes runs task widen` |
| 56 | 8 | /widen offers every blocked task, not only a widen request | caught | `not ok 11 - widen: the dialog shows the requested paths and the seat's reason; only its yes runs task widen` |

## 9. Owner-run row

To run once by the owner, at the owner's own terminal, in a scratch
repository (`mktemp -d`, `git init`, one commit), with the installed
amq-squad built from this branch:

1. `amq-squad lead`. The plan block reads "No team yet. Tell me what you
   want built and I will propose a plan." and its last line `/plan
   /confirm  /team hide`.
2. Ask for a two-seat team; the plan block shows it. Type `/confirm`: the
   dialog quotes the goal, the session and the seats; answer yes. The plan
   block says "Confirmed at ..., witnessed by the lead".
3. Let the lead apply it. The team screen replaces the plan block; read
   each seat's line ("idle" or "working on ...") and the last line `/add
   /reset  /dismiss  /answer  /widen  /team hide`.
4. Type `/add task`, give a title, a class, a file and an empty pool; the
   screen shows "/add: Added tN ..." and the task line.
5. Type `/squad`: the commands print and the screen stays.

Not run here: R54 keeps this branch's binary off owner state, the real
tmux, AMQ and Buzz.

## 10. Not done here, outside the declared files

- Go's own texts still name the 3.1.0 command: `doorsText` in
  internal/cli/team_confirm.go ("type /squad confirm %s in the lead's
  pane"), the `shell_door_removed` and `peer_not_lead_pi` details in
  internal/cli/lead_start.go, the `owner_only` deny in
  internal/cli/hook_guard.go, and `team` usage text in internal/cli/team.go.
  The extension replaces the doors line in what it returns to the model and
  sends to the phone; the other three reach the model or the owner as
  refusals. docs/squad-yaml.md (lines 105, 187) and docs/skills.md's
  generated pages still say `/squad confirm <prefix>` and `/squad add`.
  A follow-up with those files declared.
- The brief's "cost line": `team.PlanLines` prints none, so the confirm
  dialog quotes a cost line only when a plan line starts with "cost". It
  gives the plan's size instead ("and 4 tasks over 12 files"), counted from
  the plan's own task and files lines (the plan reviewer's suggestion on
  #863). A cost line needs Go's plan to carry one.
- The brief names the answer key's verb `gate show`; the verb is `gate
  status` (and `gate post`), unchanged.

## 11. For the 3.1.1 release notes

- After a failed seat launch, a waiting or blocked task says why in words
  (the launch's stage or seat-exec's refusal), AMQ's own code below the
  rule.
- The lead's screen speaks plainly and each action is its own command: in
  the solo phase `/plan` and `/confirm` (a dialog quotes the plan; nothing
  to type), in the team phase `/add`, `/reset`, `/dismiss`, `/answer` and
  `/widen` (a seat's widen request, accepted in a dialog that shows the
  paths and the reason); `/team` and `/team hide` in both. Each seat line
  says what the seat is doing and its warnings in words, each task line why
  it waits or what stopped it and what to do, and a stopped seat or train
  what happened and what to do; refusals say what happened and what to do, the codes
  below a rule. `/squad` only lists the commands now and goes in 3.2.

## 12. Gates

At code head `00ccfcda`, after the merge of origin/v3 at `52a8bc1c` (#854
landed; merge `b4df9475`, no conflict); the commits after it change docs
only:

- `go build ./... && go vet ./... && go test -count=1 ./...`: exit 0, 56 packages ok, 0 FAIL lines. Tail:

```
?   	github.com/omriariav/amq-squad/v3/scripts/redact-evidence	[no test files]
?   	github.com/omriariav/amq-squad/v3/spike/claude-probes	[no test files]
?   	github.com/omriariav/amq-squad/v3/spike/pi-r19	[no test files]
exit 0
```

- `cd pi && npm ci --offline --ignore-scripts && npm test`: exit 0. Tail:

```
ℹ tests 104
ℹ suites 0
ℹ pass 104
ℹ fail 0
ℹ cancelled 0
ℹ skipped 0
ℹ todo 0
ℹ duration_ms 15171.873292
exit 0
```

- `make ci` (PyYAML venv on PATH): exit 0. Tail:

```
PERF pre-tool allow in a seat: p50 21.5 ms, p95 22.9 ms
PERF outside a seat: 60 runs, p50 5.7 ms, p95 6.3 ms, max 6.6 ms (budget p95 < 20 ms: met)
exit 0
```
