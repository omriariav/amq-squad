# 31-2 PR 2: the add key's seat half, with the roster reload

Brief: brief-31-2 PR 2 (the lead's brief of 2026-11-01), under
docs/v3.1-plan.md section 7 (the owner's sentence: "launching
binaries/models should be FAST! like claude agents teams"),
docs/v3-spike/briefs/brief-31-2-keys.md key 1's seat half and row 4, and
the plan reviewer's three shape points of 2026-11-01. PR 1's note
(31-2-keys.md section 5) named the gap; this PR closes it. Branch
v3-31-2b, from origin/v3 b8ab8f82 (#831 merged; pi/extensions/lib/keys.ts
and internal/schedproc/clear.go present).

## 1. The gap, before any code

Re-read at origin/v3 `b8ab8f82` (the brief's lines are at `3f6c995d`;
#831 moved the cli ones by 19 lines). A seat newly added to squad.yaml
could not start by a roster relaunch:

- **The roster is compiled once.** `scheduler run` compiles squad.yaml at
  its start (`squadOfProject`, internal/cli/scheduler_run.go:169; the brief
  says :150) and derives the roster once (`slots.Roster(*lead)`, :225; the
  brief's :206). The result is fixed in `scheduler.Config.Roster` (:310)
  and in `Slots.Squad` (internal/schedproc/slots.go:29).
- **A relaunch stores only a reason.** `RequestRelaunch` sets
  `s.relaunchAsk = &reason` (internal/scheduler/roster.go:144), which the
  tick reads (internal/scheduler/scheduler.go:517).
- **The relaunch launches the fixed roster.** `launchRoster` iterates
  `s.Config.Roster` (internal/scheduler/roster.go:227), and `PlanRoster`
  (internal/schedproc/roster.go:40) looks each handle up in the fixed
  `Slots.Squad.Seats` (:50); an unknown handle refuses (:56, "which is not
  in the squad's seat list").
- **Every start is the scheduler's.** Each launch's start mark is
  `seatlaunch.Options.Start`, set once from `AMQ_SQUAD_START` (`launchStart`,
  scheduler_run.go:232 for the launcher, :292 for the launch record; the
  brief's :213 and :273). `LaunchRoster` copies the options unchanged
  (internal/seatlaunch/roster.go:71), and `writeMarks` writes that one start
  for every seat (internal/seatlaunch/run.go:273, internal/seatlaunch/
  compile.go:249). `RelaunchRequest` (internal/schedproc/decide.go:375)
  carries no start.
- **The router admits only the pools known at open.** `routing.Open` takes
  `RosterPools: rosterPools(roster)` (scheduler_run.go:263; the brief's
  :244 is now the runtime map at :245), kept in `Options.RosterPools`
  (internal/routing/router.go:116) and read at :551, :634 and :772.
- One more, found on the way: the runtime map (`byRuntime`,
  scheduler_run.go:243-246) had only the runtimes the squad started with,
  so a seat bringing a new runtime would refuse at launch ("no launcher for
  runtime").

## 2. The design

### 2.1 The relaunch carries the key press and a reload

`RelaunchRequest` gains `Start` (RFC3339Nano). `scheduler relaunch --start
STAMP` passes it (internal/cli/scheduler_run.go); a malformed stamp is a
usage error at the verb and `relaunch_malformed` at the socket.

The reload seam is `schedproc.Reload` (internal/schedproc/roster.go):
`Compile` reads squad.yaml again exactly as `scheduler run` does at its
start (`reloadSquad` in scheduler_run.go: `squadOfProject`, so
`team.LoadSquad`, `Squad.LaunchSeats` and `roleText`; the running squad's
wrapper and amq path; `Normalize`). A squad.yaml that names another session
or lead is refused: both are the scheduler's for its life. `scheduler run`
wires it in `DecideOptions.Reload` with the slots and the router;
internal/schedproc/process.go hands it to `relaunchDecision(s, d.Reload)`.

`relaunchDecision` compiles at the decision, before anything drains: a
compile error, or a squad.yaml with no seat but the lead, is refused
`squad_invalid` by name, and the scheduler is not asked to relaunch, so the
running roster is untouched. On success it hands the new roster, a swap
closure and the start to the scheduler (`RequestRelaunchReload`).

### 2.2 The swap: one step at the drain's end, or none

The scheduler keeps the reload until `drainForRelaunch` reports every seat
closed, then calls `swapReload` (internal/scheduler/roster.go) before
`launchRoster`. The swap is three writes:

1. the slots' squad, under `Slots.mu` (`Reload.swap`);
2. the router's roster pools, under the router's lock (`SetRosterPools`);
3. `Config.Roster`, written by the scheduler only once 1 and 2 returned
   nil.

`Reload.swap` undoes what it wrote when a step after it fails, so a failure
between any two writes leaves all three old. The scheduler then logs
`roster_reload_failed` and relaunches the running roster unchanged. On
success it logs `roster_reloaded`, naming the added seats and the removed
ones. A second relaunch accepted while one drains replaces the pending
reload with the newer squad.yaml and key press (the swap is still to
come). The test seam `reloadFault` injects the failure between the writes.

Nothing reads a half-applied state: the tick goroutine is the only writer
of `Config.Roster` and the only caller of `PlanRoster`, `CloseSeat` and the
router's `Route`; the slots and the router are each written under their own
lock, and both writes happen while no seat is live (after the drain).

### 2.3 The reload only adds (plan reviewer point 1)

The reload stops no seat. The relaunch already drains every seat (each
finishes what it holds, then closes with the reason `roster_relaunch`), and
the reload changes nothing about that. Because the swap waits for the
drain's end, a seat removed from squad.yaml while live keeps its lease, its
slot and its line in the slots' squad through the drain, and leaves at that
close. It is not launched again, since the new roster does not name it. A
changed line for a live seat (model, effort, role) applies at that seat's
launch in the same apply, never by a kill. A swap at the request would
have broken both: the removed seat's close looks its runtime up in the
slots' squad, and a squad without it closes nothing.

### 2.4 The start mark (plan reviewer point 3)

`SeatRequest` gains `Start`. `swapReload` records the key press for each
seat the reload added (in the new roster, not in the old), and
`launchRoster` puts it on that seat's request, for that launch only; a
later relaunch carries none. `seatlaunch.Launcher.LaunchRoster` copies each
request's start into `Options.SeatStart` (a per-launch map, cloned so the
launcher's own options never keep it), and `writeMarks` writes it as that
seat's `marks.json` start in place of `Options.Start`. Every seat that was
already in the roster keeps the squad's start. Status's launch budget
(`launchTiming`, internal/status/screen.go) reads `marks.json` as it is, so
the 10 s ceiling binds the added seat from the key press.

The key press is the confirmation's moment, not the moment `/squad add` was
typed: the owner's typing of five fields is not launch time. The drain
before the swap is: a relaunch waits for every seat to finish what it
holds, and that wait counts against the added seat's budget.

### 2.5 The router's pools per call (change 5)

`routing.Router.SetRosterPools` replaces the pools under the router's lock,
and `RosterPools` reads them; every read site (`decide`, `check`,
`rosterOffTable`) already runs under that lock through `Route` and
`Decide`. A setter was the smaller diff than a func.

### 2.6 The verbs and the key

- `team propose --add-seat HANDLE:ROLE:RUNTIME:MODEL:EFFORT` and `team
  apply --digest D --add-seat ...` (internal/cli/team.go): the squad.yaml on
  disk, parsed strictly against the owner grant, with the seat appended and
  rendered in canonical form (`team.Render`; comments are not kept). The
  role left empty is the handle; the model and effort may be empty, and the
  model may hold colons (the effort is after the last one). The team rules
  are kept as they are (the owner approved their prose; `team propose`
  without the flag re-renders them), so the new seat is not listed in
  `.amq-squad/team-rules.md` until the lead re-proposes them. Refused with
  `--from` or `--roles`. The digest flow is unchanged.
- `/squad add` (pi/extensions/lib/keys.ts) asks "task" or "seat". For a
  seat: handle, role, runtime (a pick of squad.yaml's runtimes), model and
  effort, with pi's dialogs; then `team propose --project <status
  project_path> --add-seat SPEC --json`; one confirmation, "this relaunches
  every seat; continue?"; `team apply --digest D ...`; and `scheduler
  relaunch --reason "add seat H (the screen's add key)" --start <the
  confirmation's ISO stamp> --json`. A propose refusal shows as
  `squad_invalid: <the verb's line>`; an apply refusal shows its own code
  (`squad_yaml_changed`, `squad_yaml_digest_mismatch`); the relaunch's
  refusal shows as the verb printed it (`scheduler relaunch refused
  (squad_invalid): ...`, `not_lead`, `scheduler_starting`). A cancel at any
  dialog runs no verb; a declined confirmation writes nothing.

### 2.7 Choices where the brief left one

- **The swap's owner.** The scheduler writes `Config.Roster` itself, and
  calls a `Swap` closure from schedproc for the slots and the router, since
  the scheduler cannot import schedproc or routing.
- **process.go.** `relaunchDecision` takes the reload, so the one call in
  internal/schedproc/process.go changes (one line). Files touched outside
  the brief's parentheses, all inside its declared directories:
  internal/schedproc/process.go, internal/scheduler/scheduler.go (the
  pending reload's fields) and hooks.go (`SeatRequest.Start`),
  internal/seatlaunch/run.go (`Options.SeatStart`) and compile.go
  (`writeMarks`), pi/extensions/lib/screen.ts (`project_path` on the status
  type).
- **The runtime map** names every runtime squad.yaml takes (`team.Runtimes`),
  so an added seat with a new runtime launches through the same AMQ
  launcher.
- **The lease cap** (`min(max_seats, the squad's seat count)`) stays fixed at
  the scheduler's start; an added seat shares it.
- **The status contract** gains no field: `launch.timing.start` keeps its
  name and now reads the key press for an added seat
  (docs/status-contract.md says so).

## 3. Rows

Each row fails with its change reverted (section 4).

| Row | Test |
| --- | --- |
| 4 (add, seat) | pi `add, seat: team apply with the digest, one confirmation, one relaunch carrying the key press` (the exact propose, apply and relaunch argv; one confirmation; `--start` is the confirmation's moment); pi `add, seat: squad_invalid and the relaunch's refusal by name; a declined confirmation writes nothing`; pi `add, seat: a cancel at any dialog runs no verb`; cli `TestTeamAddSeatAppendsOneSeatThroughTheDigest` (propose writes nothing, apply writes the seat under the digest, the rules unchanged, a stale digest, a malformed spec, a repeated handle and `--roles` refused, a colon model kept); the start: scheduler `TestAReloadSwapsAtTheDrainsEndAndOnlyTheAddedSeatStartsFromTheKeyPress`, schedproc `TestARelaunchReloadsSquadYAMLAndLaunchesTheAddedSeat`, seatlaunch `TestTheAddedSeatsMarksStartIsTheKeyPress` (the new seat's `marks.json` start is the key press; the existing seat keeps the squad's start) |
| A | cli `TestARelaunchAfterASquadYAMLEditLaunchesTheNewSeatAndAdmitsItsPool` (through `scheduler run` and `scheduler relaunch --start`: a task pinned to the new seat's pool is refused `pin_unresolved` before; squad.yaml gains rev-1; the real `PlanRoster` finds it; its request carries the key press and impl-1's none; a task pinned to its pool dispatches to rev-1 after); routing `TestARosterSeatAddedByAReloadIsAdmittedAfterTheSwapAndNotBefore`; schedproc `TestARelaunchReloadsSquadYAMLAndLaunchesTheAddedSeat` |
| B | schedproc `TestARelaunchWhoseSquadDoesNotCompileIsRefusedBeforeTheDrain` (`squad_invalid` and `relaunch_malformed` by name; no `roster_relaunch_requested`, no close, no launch; roster, squad and pools unchanged); schedproc `TestAHalfAppliedReloadIsNeverObserved` (a failure injected after the slots' write and after the pools' write: slots, pools and roster all old, `roster_reload_failed`, the old roster relaunched); scheduler `TestAFailedSwapRelaunchesTheRunningRosterUnchanged` |
| C | scheduler `TestAReloadSwapsAtTheDrainsEndAndOnlyTheAddedSeatStartsFromTheKeyPress` (s-b, removed from squad.yaml, keeps its lease and is not closed while it holds it; nothing swaps; once it finishes it closes with `roster_relaunch`, never another reason, and is not launched again; s-a's changed line, sonnet to opus, launches on its new pool in the relaunch's apply) |
| D | seatlaunch `TestTheAddedSeatsMarksStartIsTheKeyPress` (the key press 11 s before seat live and the relaunch's decision 2 s before: the mark is the press, so the seat is over the 10 s ceiling counted from the press though under it from the decision) |
| E | pi `the lead's model tool list carries no key verb and no gate-answer verb` (PR 1's row, unchanged: the exact thirteen tools) |

No test reaches the network, the real HOME, tmux, AMQ or Buzz: the cli
rows run `scheduler run` with the fake launcher seam and a temporary HOME
and repository; the seatlaunch row runs the real launcher over the fake
AMQ CLI with every pane question stubbed; the pi rows run the stand-in pi
with a scripted runner.

## 4. The revert runs

Each change was reverted alone and its rows run; each reverted run failed
(the restored tree passes):

| Reverted change | Failing row |
| --- | --- |
| the router's pools fixed at open (`SetRosterPools` a no-op) | routing `TestARosterSeatAddedByAReload...`, cli `TestARelaunchAfterASquadYAMLEdit...` |
| the swap at the request instead of the drain's end | scheduler `TestAReloadSwapsAtTheDrainsEnd...` |
| the roster written although `Swap` failed | scheduler `TestAFailedSwap...` |
| a compile error ignored (the relaunch drains) | schedproc `TestARelaunchWhoseSquadDoesNotCompile...` (both squad_invalid cases) |
| no undo between the swap's writes | schedproc `TestAHalfAppliedReloadIsNeverObserved` (both injection points) |
| the relaunch without the reload (process.go) | schedproc `TestARelaunchReloadsSquadYAML...` |
| `scheduler run` wiring no reload | cli `TestARelaunchAfterASquadYAMLEdit...` |
| no per-request start in the roster launch | scheduler `TestAReloadSwapsAtTheDrainsEnd...` |
| `marks.json` with the launch's start for every seat | seatlaunch `TestTheAddedSeatsMarksStartIsTheKeyPress` |
| `relaunch --start` not passed to the request | cli `TestARelaunchAfterASquadYAMLEdit...` |
| `--add-seat` ignored | cli `TestTeamAddSeat...` |
| `--add-seat` re-rendering the team rules | cli `TestTeamAddSeat...` |
| the add key adding tasks only | pi `add, seat: ...` (all three) |
| the relaunch without `--start` | pi `add, seat: team apply with the digest, ...` |
| the confirmation skipped | pi `add, seat: team apply with the digest, ...` and the declined row |
| a tool added to the lead (row E) | pi `the lead's model tool list ...` |

## 5. The owner-run row: the launch budget from the add key

From the owner's terminal, with this branch's tagged build installed by
`up` (as in 31-2-keys.md section 6), on the squad of session v31 (lead
cto). It adds a real seat to squad.yaml; the grant's `max_seats` must admit
five seats.

In the lead's pi: `/squad add`, then:

| Dialog | Input |
| --- | --- |
| add: a task or a seat | `seat` |
| add seat: handle | `qa-2` |
| add seat: role (empty: the handle) | `qa` |
| add seat: runtime | `codex` |
| add seat: model | `gpt-5.6-sol` |
| add seat: effort | `high` |
| add seat qa-2 (qa-2:qa:codex:gpt-5.6-sol:high): this relaunches every seat; continue? | yes |

Expect the key's line: `key add: seat qa-2 added; the roster relaunches
with it (every seat finishes what it holds first); its launch budget counts
from <STAMP>`. Once the relaunch launched (the screen shows qa-2 live):

```sh
cd ~/code/amq-squad
amq-squad status --json | jq '.seats[] | select(.handle=="qa-2") | {handle, launch: .launch.launch_id, start: .launch.timing.start, seat_live: .launch.timing.seat_live, to_seat_live_ms: .launch.timing.to_seat_live_ms, ceiling_ms: .launch.timing.ceiling_ms, over_ceiling: .launch.timing.over_ceiling, slowest: .launch.timing.slowest_segment}'
cat .agent-mail/v31/extensions/amq-squad/launch/qa-2/marks.json
grep -E '"kind":"(roster_relaunch_requested|roster_reloaded|roster_launched)"' .agent-mail/v31/extensions/amq-squad/scheduler/decisions.jsonl | tail -n 3
```

Expect `start` and the `marks.json` start equal to the key line's STAMP
(UTC), `ceiling_ms` 10000, and `to_seat_live_ms` measured from it;
`over_ceiling` is the verdict. The decisions show the request, then
`roster_reloaded` naming qa-2 with the key press, then `roster_launched`.
The relaunch drains first: a seat busy on a task when the key is pressed
adds its remaining work to qa-2's budget, so run this row with the squad
idle for a launch-only figure. To take qa-2 out again: `team propose
--from` a squad.yaml without it, `team apply` with that digest, then a
relaunch from the lead's process (its `amq_scheduler_relaunch` tool; the
socket refuses one from another shell, `not_lead`). The reload stops no
seat: qa-2 finishes what it holds and leaves at that relaunch's close.

## 6. One line for the 3.1 release notes

- A seat added to squad.yaml starts without restarting the squad: the
  lead's `/squad add` (or `team apply --add-seat` and `scheduler relaunch`)
  writes it under the digest and relaunches the roster, which reads
  squad.yaml again, and the new seat's launch budget counts from the key
  press.
