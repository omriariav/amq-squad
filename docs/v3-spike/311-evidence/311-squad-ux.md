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
  pi's select first. It offers the tasks blocked `widen_requested` or
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
repeated there. A seat's warning lines (`    ! <code>: <text>`) and the
phone-request rows keep Go's text and ids verbatim: `/dismiss` acts on a
warning by its code, and the rows are records.

## 4. Refusals read like the 3.1.1 gate questions

Every key outcome is a plain line on the screen ("/reset: Nothing to reset:
no task is blocked.", "/dismiss: Nothing to dismiss: seat dev has no
warning showing.", "/reset: t1 was not reset: task t1 was granted attempt
2; ..."), then, below a rule above the screen's last line, the record: the
verb's own text as it printed it and, when the verb printed its own rule,
the codes below that. A verb's text is shown above the rule only after its
leading `verb: code:` segments and parenthesized codes are taken out, and
only if what is left carries no code or hex run (`plainDetail`). The
blocks' last-stop lines are words ("last stop: seat old-dev's exit was
refused", `DECISION_WORDS`), their codes below the rule.

The four named lines:

- internal/cli/task_widen.go, `widenRefused.Error` (was `widen refused
  (%s): %s`): a plain line by code (`widenPlain`), the rule, then `widen
  refused (<code>): <detail>`.
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
plain-line lookups by the existing codes. The existing Go rows that read the
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

```
=== team phase: the lead's screen ===
amq-squad: lead cto (pid 4300, pane %3) · root /Users/owner/code/app/.agent-mail/v31
seats (3)
  qa  codex gpt-5.6-sol high  idle  · up in 12.4s (over the 10s ceiling), first message 57.4s
    ! launch_budget_exceeded: 12.400 s from the start to seat live, over the 10s ceiling; slowest segment seat_exec 8.059 s
    ! wake_off: amq-squad: no AMQ wake for this Codex seat: AMQ's Codex dialog hook is not on the host; the seat idles afte...
  scribe  claude sonnet medium  working on t2: Write the notes  · up in 4.7s
    ! seat_stalled_at_dialog: a harness dialog has been open since 2026-11-01T09:02:00Z (3m0s); the seat waits at it and AM...
  pi-dev  pi anthropic/claude-sonnet-5 medium  idle  · up in 4.5s
    ! pi_provider_key_missing: model anthropic/claude-sonnet-5 needs one of ANTHROPIC_API_KEY, ANTHROPIC_OAUTH_TOKEN, ANTHR...
  last stop: seat old-dev's exit was refused
tasks (3)
  t1 blocked: the commit step refused it, it changed files outside its task; accept them with /widen or send it back with /reset
  t2 in progress on scribe: Write the notes  PR #812
  t26 waiting: t20, t23 hold its files
train
  current: r2 refusal 81h ago, PRs #811,#812, merged 1
  last: r1 merge 82h ago, PRs #810, merged 1
    ! tag v1.2.0 has no accepted gate
  last stop: the merge train stopped: refusal PR #812 checks: a required check failed
waiting on you (1)
  Create a tag named v1?  (answer on the phone; /answer shows it)
requests (2)
  q-7c41d09e2a55  cto  not relayed, the relay refused it
  q-3f2a9c1e7b04  cto  relayed 81h ago
----------
  exit_refused: old-dev ledger_tampered at gone
  ungated_tag
  train r2: refusal PR #812 checks: a required check failed
  g1  tag  8502aa0011223344  age 81h  waiting on Buzz
  q-7c41d09e2a55 refused: relay_refused_gate_shape
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

### The dialogs and a refusal (after)

```
=== /confirm: pi's confirm dialog, title "Start this team?" ===
goal: ship the parser
session s1, lead lead
seat lead (lead) pi
seat impl-1 (implementer) claude/sonnet/medium

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
  /widen: t5: task widen t5 was refused: the widened files overlap another active task's files; widen it once that task is done
```

## 7. Rows

| row | what | where |
| --- | --- | --- |
| 1 | every action its own command with a one-line plain description; no pi built-in name (pi's `BUILTIN_SLASH_COMMANDS` at test time); bare `/squad` toggles nothing and prints the phase's commands | pi/test/commands.test.ts "row 1" |
| 2 | the screen's last line lists exactly the current phase's commands, both phases, and after the switch | pi/test/commands.test.ts "row 2" (two tests) |
| 3 | a seat with a lease "working on <id>: <title>", idle, starting, stopped; a waiting task's plain reason; a blocked task what happened and what to do | pi/test/words.test.ts "row 3"; pi/test/screen.test.ts |
| 4 | no refusal or outcome line above its rule carries a snake_case code or a hex run; the code is below it: the screen over every reason and seat code, every key's refusals, the plan block, a verb's own below-rule code; tools.ts:143; the three Go lines | pi/test/words.test.ts "row 4" (two tests); pi/test/keys.test.ts (widen refusal record); internal/cli/task_widen_test.go `TestTheWidenRefusalPutsItsCodeBelowTheRule`, `TestTheWidenVerbsRefusalPutsItsCodeBelowTheRule`; internal/mergetrain/drift_test.go `TestTheGateStoreUnreadableAlertPutsThePathBelowTheRule` |
| 5 | `/confirm`'s yes confirms the digest the dialog showed; a re-propose while it is open changes nothing; a no or a cancel confirms nothing; two proposals: select, then the dialog; a model message containing "/confirm" invokes nothing (the 31-3 row kept) | pi/test/team.test.ts "/confirm" (two tests), "a model message containing /confirm" |
| 6 | the lead's team tools are Go's eleven; no command is a tool | pi/test/commands.test.ts "row 6"; pi/test/phase.test.ts (unchanged) |
| 7 | every wait and block reason in Go's sources (read at test time) and every seat code maps to words in one table; an unmapped reason falls back with its code below the rule; the size cap's numbers from the record; the dependency-merge wait verbatim; a title with a canary and a control character redacted and stripped; each empty block one plain line | pi/test/words.test.ts "row 7" (five tests) |
| 8 | `/widen` shows the requested paths and the seat's reason from the task's record, runs `task widen` only on the dialog's yes, picks among several, and refuses in plain words when no task is blocked with a widen request | pi/test/keys.test.ts "widen" |

The Go reasons the row 7 table is checked against: internal/task
(class.go `Wait*`, `Block*`; task.go `Reason*`; verdict.go
`ReasonGrantsInvalid`; scope.go `RefusalPRBoundElsewhere`),
internal/seatcommit (policy.go, commit.go, ledger.go, verifier.go
`Reason*`), internal/scheduler (scheduler.go `WaitNoRosterSeat`; hooks.go
and roster.go `Reason*`, `AMQBindingPresent`, the `Route{Block: ...}`
literals), internal/routing (canon.go, router.go `Block*` and literals,
outcome.go `HistoryTampered`), internal/registry pins.go `PinMismatch`, and
the failed launch stages (internal/seatlaunch run.go, roster.go,
internal/scheduler roster.go; `prepared` and `applied` are successes). A
block reason a seat or the lead writes as free text (`task block --reason`)
is open by design: it reads "<id> blocked; its reason is below the line"
with the reason below the rule. So does an AMQ launch reason code that is
not one of the stages.

## 8. Revert runs

`python3 docs/v3-spike/311-evidence/311-squad-ux-revert-runs.py` from a
clean checkout, at code head `c7105890`: 32 runs, 32 caught. The first run
missed one (row 8 offered `/widen` only tasks blocked for something; no
test had a task blocked for another reason); the row now has that case
(`154444eb`), and the run catches it.

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
| 9 | 4 | a refusal's verb text above the rule (sentence without the strip) | caught | `not ok 7 - row 4: no line above the rule carries a snake_case code or a hex run; the code is below it` |
| 10 | 4 | the gate line with its id and prefix above the rule | caught | `not ok 7 - row 4: no line above the rule carries a snake_case code or a hex run; the code is below it` |
| 11 | 4 | tools.ts:143, the state and reason first (3.1.1-2 form) | caught | `not ok 8 - row 4: the lead's widen refusal (tools.ts) puts its state and reason below the rule` |
| 12 | 4 | task_widen.go widenRefused.Error, the `widen refused (%s): %s` form | caught | `--- FAIL: TestTheWidenRefusalPutsItsCodeBelowTheRule (0.00s)` |
| 13 | 4 | task_widen.go the verb's `refused (%s)` form from the socket reply | caught | `--- FAIL: TestTheWidenVerbsRefusalPutsItsCodeBelowTheRule (0.00s)` |
| 14 | 4 | drift.go the gate_store_unreadable alert with the path and error in its line | caught | `--- FAIL: TestTheGateStoreUnreadableAlertPutsThePathBelowTheRule (0.01s)` |
| 15 | 4 | a verb's below-rule code dropped from the record | caught | `not ok 11 - widen: the dialog shows the requested paths and the seat's reason; only its yes runs task widen` |
| 16 | 5 | the confirm reads the newest plan after the dialog | caught | `not ok 5 - /confirm: a re-propose while the dialog is open does not change what is confirmed; two plans: select, then the dialog` |
| 17 | 5 | the dialog's no confirms anyway | caught | `not ok 4 - /confirm: the dialog's yes asks the lead to witness the plan it showed; a no sends nothing` |
| 18 | 5 | two live plans: no select, the newest confirmed | caught | `not ok 5 - /confirm: a re-propose while the dialog is open does not change what is confirmed; two plans: select, then the dialog` |
| 19 | 5 | a confirm tool beside the command (a model message could confirm) | caught | `not ok 6 - a model message containing /confirm reaches no lead; the confirm is a command, never a tool` |
| 20 | 6 | an action registered as a tool too | caught | `not ok 4 - row 6: the team lead's tools are Go's eleven and no command is a tool` |
| 21 | 7 | a block reason dropped from the table (verdict_failed) | caught | `not ok 1 - row 7: every wait and block reason in Go's sources maps to plain words in the one table` |
| 22 | 7 | a wait reason dropped from the table (seat_cap) | caught | `not ok 1 - row 7: every wait and block reason in Go's sources maps to plain words in the one table` |
| 23 | 7 | a seat code dropped from the table (idle) | caught | `not ok 1 - row 7: every wait and block reason in Go's sources maps to plain words in the one table` |
| 24 | 7 | the size cap's numbers guessed away (no detail read) | caught | `not ok 2 - row 7: the size cap's numbers come from the recorded detail through task show` |
| 25 | 7 | the screen reads no task record (no detail fetch) | caught | `not ok 2 - row 7: the size cap's numbers come from the recorded detail through task show` |
| 26 | 7 | a title without the redaction | caught | `not ok 5 - row 7: a title with a canary and a control character shows redacted and stripped` |
| 27 | 7 | a title without the control-character strip | caught | `not ok 5 - row 7: a title with a canary and a control character shows redacted and stripped` |
| 28 | 7 | an empty seats block shows nothing | caught | `not ok 6 - row 7: each empty block says so in one plain line` |
| 29 | 7 | the hold not mapped (held by the lead) | caught | `not ok 1 - row 7: every wait and block reason in Go's sources maps to plain words in the one table` |
| 30 | 8 | /widen runs task widen whatever the dialog answers | caught | `not ok 11 - widen: the dialog shows the requested paths and the seat's reason; only its yes runs task widen` |
| 31 | 8 | the dialog without the seat's reason | caught | `not ok 11 - widen: the dialog shows the requested paths and the seat's reason; only its yes runs task widen` |
| 32 | 8 | /widen offers every blocked task, not only a widen request | caught | `not ok 11 - widen: the dialog shows the requested paths and the seat's reason; only its yes runs task widen` |

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
  dialog quotes a cost line only when a plan line starts with "cost".
- The brief names the answer key's verb `gate show`; the verb is `gate
  status` (and `gate post`), unchanged.

## 11. For the 3.1.1 release notes

- The lead's screen speaks plainly and each action is its own command: in
  the solo phase `/plan` and `/confirm` (a dialog quotes the plan; nothing
  to type), in the team phase `/add`, `/reset`, `/dismiss`, `/answer` and
  `/widen` (a seat's widen request, accepted in a dialog that shows the
  paths and the reason); `/team` and `/team hide` in both. Each seat line
  says what the seat is doing, each task line why it waits or what stopped
  it and what to do; refusals say what happened and what to do, the codes
  below a rule. `/squad` only lists the commands now and goes in 3.2.

## 12. Gates

At code head `c7105890` (the docs commits after it change no code):

- `go build ./... && go vet ./... && go test -count=1 ./...`: exit 0, 56 packages ok, 0 FAIL lines. Tail:

```
ok  	github.com/omriariav/amq-squad/v3/scripts/pipack	0.505s
?   	github.com/omriariav/amq-squad/v3/scripts/redact-evidence	[no test files]
?   	github.com/omriariav/amq-squad/v3/spike/claude-probes	[no test files]
?   	github.com/omriariav/amq-squad/v3/spike/pi-r19	[no test files]
exit 0
```

- `cd pi && npm ci --offline --ignore-scripts && npm test`: exit 0. Tail:

```
ℹ tests 98
ℹ suites 0
ℹ pass 98
ℹ fail 0
ℹ cancelled 0
ℹ skipped 0
ℹ todo 0
ℹ duration_ms 15274.368333
exit 0
```

- `make ci` (PyYAML venv on PATH): exit 0. Tail:

```
context injection: last run 44.7 ms
PERF pre-tool allow in a seat: p50 21.4 ms, p95 22.4 ms
PERF outside a seat: 60 runs, p50 5.7 ms, p95 6.9 ms, max 7.1 ms (budget p95 < 20 ms: met)
exit 0
```
