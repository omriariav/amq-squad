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
usage error at the verb and `relaunch_malformed` at the socket. The stamp
is bounded (Codex P2 1 on 5d20b5ff; `schedproc.CheckRelaunchStart`): more
than 5 s in the future (`StartSkew`), or before the scheduler's own launch
start (the launch record's `start`; no lower bound for a scheduler started
by hand), it is refused `start_out_of_range` by name, at the verb with
nothing sent and again at the socket before anything drains.

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

The tick reads its config again after the roster step (Codex P2 3 on
5d20b5ff), so dispatch and the state written at the tick's end
(scheduler.json, which status reads) carry the new roster and cap in the
same tick as the launch, never the old roster beside the seats just
launched.

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

The rule's honest form until AMQ's ask 20: the add key today is a roster
relaunch, so every live seat drains and comes back. "The reload only adds"
means two things now: a seat removed from squad.yaml is not launched again,
and no seat is killed outside the drain the owner confirmed. When ask 20
lands, the hot add (a fresh seat joined into the present binding) makes the
first clause literal, and the live seats stay up.

The reload stops no seat of its own. The relaunch already drains every seat
(each finishes what it holds, then closes with the reason
`roster_relaunch`), and the reload changes nothing about that. Because the swap waits for the
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
`launchRoster` puts it on that seat's request. The scheduler keeps each
added seat's press until the seat leaves the roster, so every later
relaunch carries it again and the seat's marks never fall back to the
squad's start (Codex P2 2 on 5d20b5ff: the first cut cleared it after one
launch). A later launch that carries the same press writes no `drain_end`:
the wait before that launch is not the seat's drain. The starts live in the
scheduler's memory; a restarted scheduler starts the squad again from its
own start. `seatlaunch.Launcher.LaunchRoster` copies each
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

### 2.4a The drain as a named segment (plan reviewer, line 1)

On a busy squad the drain is the slowest part of the added seat's budget,
and the number must say so rather than blame seat_exec or prepare.
`LaunchRoster` records when the drain ended and the launch began
(`Options.DrainEnd`), and `writeMarks` writes it as `drain_end` in the
marks of each seat whose start is its own (the key press). Status
(`launchTiming`, internal/status/screen.go) then lists five segments for
that seat: `drain` (the key press to the drain's end), `prepare` (the
drain's end to the staged record), `mint`, `apply`, `seat_exec`. The
over-ceiling warning names the slowest, `slowest segment drain 30.000 s`
on a busy squad. A seat without `drain_end` keeps the four segments.
internal/status is outside this brief's declared files; the lead's gate
on 5d20b5ff asked for the segment, which lives there.

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
- **The lease cap** (section 2.8) was computed once at the start in the
  first cut; the lead's gate on 5d20b5ff ruled that a gap, and it is now
  recomputed in the swap.
- **The status contract** gains no field: `launch.timing.start` keeps its
  name and now reads the key press for an added seat
  (docs/status-contract.md says so).

### 2.8 The lease cap, recomputed in the swap (the lead's gate on 5d20b5ff)

`scheduler run` computed the lease cap once, `min(max_seats, the squad's
seat count)` narrowed by `--max-seats` (now `leaseCap` in
internal/cli/scheduler_run.go). A squad that grew past its starting seat
count could never lease the extra seats. The reload now carries a cap
(`Reload.Cap`, scheduler run's `reloadCap`): the grant is read again (the
owner's file narrowed by the squad.yaml just compiled), it bounds the new
squad's seat count, and the start's `--max-seats` narrowing still applies.
A cap of none is `squad_invalid` before the drain. The scheduler writes
`Config.MaxSeats` with `Config.Roster`, after the slots and the router took
theirs, so the cap is part of the one step; a failed swap leaves it as it
was. `roster_reloaded` names the new cap.

Squad.yaml itself cannot outgrow `max_seats`: `team propose --add-seat`
refuses a seat past it (the owner's grant is never exceeded). A cap below
the roster at a relaunch comes only from a narrowing after the propose:
the reload's `squadOfProject` parses squad.yaml against the owner's grant
again, so a narrowed grant file refuses the relaunch `squad_invalid`, and
in practice the case is `scheduler run --max-seats`.

Over the cap, the plan reviewer ruled before the merge that the grant is
the owner's cap on seats: a launched seat that only waits `seat_cap` is
still a Claude or Codex process with a wake running outside what he
granted. So `Reload.prepare` keeps the first cap seats of the roster in
squad.yaml order and leaves the rest out (`RosterReload.Unlaunched`). The
router's pools and the roster are the launched set, so the leases follow
it. The scheduler logs `roster_over_cap` naming the unlaunched seats and
the cure, and writes them to its state (`unlaunched`). Status lists them
under `scheduler.unlaunched_seats`, and the lead's screen shows one line
per seat: `! rev-2 not launched: over max_seats 2; widen the grant or
remove the seat`. The relaunch itself is not refused. The first cut
launched every seat and warned; that row is replaced.

The bound applies at a relaunch's reload only. At `scheduler run`'s start,
3.0.1's rule still holds (`max_seats` caps the leases held at once, not the
seats up): `TestRosterSeatCapOfOneMeansOneLeaseAtATime` keeps it, and
changing the start is outside this brief.

The squad seat count includes the lead, which holds no lease, so a cap of
`min(max_seats, seats)` already covered one added roster seat. The
revert-failing row adds two.

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
| Start bound (Codex P2 1) | schedproc `TestARelaunchStartOutOfRangeIsRefusedBeforeTheDrain` (a stamp a minute ahead and one before the launch start: `start_out_of_range`, no `roster_relaunch_requested`, no launch, squad, pools and roster unchanged; an in-range stamp relaunches); cli `TestSchedulerRelaunchBoundsItsStart` (the verb refuses both with nothing sent; an in-range stamp is sent in UTC) |
| Kept starts (Codex P2 2) | scheduler `TestAnAddedSeatsStartIsKeptUntilItLeavesTheRoster` (s-c added at T1, s-d at T2: the next relaunch and a plain one after it carry T1 and T2; a reload removing s-c drops its start, s-d keeps T2); seatlaunch `TestAnAddedSeatKeepsItsStartAcrossRelaunches` (a later launch writes cto's marks with T1 again and no drain end, dev's with T2 and its drain end) |
| State agrees (Codex P2 3) | scheduler `TestTheStateWrittenInTheRelaunchsTickNamesTheNewRoster` (scheduler.json read right after the relaunch's tick names s-a and s-c and max_seats 3) |
| Drain | status `TestARelaunchedSeatsBudgetNamesTheDrain` (a key press, 30 s of drain, then the launch: segments drain, prepare, mint, apply, seat_exec; drain 30000 ms, prepare 200 ms; the slowest is drain and the warning says `slowest segment drain 30.000 s`; a seat without a drain end keeps four segments); seatlaunch `TestTheAddedSeatsMarksStartIsTheKeyPress` (the added seat's `drain_end` lies between the key press and seat live; the existing seat has none) |
| Cap | scheduler `TestAReloadRecomputesTheLeaseCap` (the lead's row: two roster seats under a cap of 2 add a third; under a recomputed cap of 3 it gets a lease while the other two hold theirs; under a cap of 2 it is not launched, `roster_over_cap` names it with the cure, the state lists it as unlaunched, and its task gets no lease); cli `TestAReloadRecomputesTheLeaseCapFromTheNewSquad` (through the verbs, squad.yaml's max_seats 4 over lead and impl-1: rev-1 and rev-2 added, three tasks leased at once, `at most 4 leases`); cli `TestAReloadOverTheCapLaunchesTheFirstSeatsAndNamesTheRest` (under `--max-seats 2`: impl-1 and rev-1 launch and hold leases, rev-2 is not launched and its task gets no lease, `roster_over_cap` names it, the state and the status document list it under unlaunched); schedproc `TestAReloadOverTheCapLaunchesTheFirstSeatsInSquadOrder` (a cap of 3: c1, x1, n1 launch, n2 does not, its pool never reaches the router); pi `unlaunched seats: one line each in the seats block, naming max_seats and the cure`; cli `TestTeamAddSeatAppendsOneSeatThroughTheDigest` (a seat past squad.yaml's max_seats refused at propose); schedproc rows: the cap travels with the reload, a cap of none is `squad_invalid` before the drain, a failed swap leaves the cap |

No test reaches the network, the real HOME, tmux, AMQ or Buzz: the cli
rows run `scheduler run` with the fake launcher seam and a temporary HOME
and repository; the seatlaunch row runs the real launcher over the fake
AMQ CLI with every pane question stubbed; the pi rows run the stand-in pi
with a scripted runner.

## 4. The revert runs

Each change was reverted alone in the working tree, its rows were run, and
the file was restored from git. The script is
docs/v3-spike/31-evidence/31-2-pr2-revert-runs.py (it refuses a dirty
tree). Its output at the head that carries the Codex P2 fixes, verbatim
(the pi lines are node's `not ok` lines):

```text
REVERT router pools fixed at open (SetRosterPools a no-op): fails (the row catches it)
   --- FAIL: TestARosterSeatAddedByAReloadIsAdmittedAfterTheSwapAndNotBefore (0.00s)
   --- FAIL: TestARelaunchAfterASquadYAMLEditLaunchesTheNewSeatAndAdmitsItsPool (10.23s)
REVERT the swap at the request, not the drain's end: fails (the row catches it)
   --- FAIL: TestAReloadSwapsAtTheDrainsEndAndOnlyTheAddedSeatStartsFromTheKeyPress (0.05s)
REVERT the roster written although Swap failed: fails (the row catches it)
   --- FAIL: TestAFailedSwapRelaunchesTheRunningRosterUnchanged (0.00s)
REVERT a compile error ignored (the relaunch drains): fails (the row catches it)
   --- FAIL: TestARelaunchWhoseSquadDoesNotCompileIsRefusedBeforeTheDrain (0.31s)
   --- FAIL: TestARelaunchWhoseSquadDoesNotCompileIsRefusedBeforeTheDrain/squad_invalid (0.08s)
   --- FAIL: TestARelaunchWhoseSquadDoesNotCompileIsRefusedBeforeTheDrain/no_seat_but_the_lead (0.08s)
   --- FAIL: TestARelaunchWhoseSquadDoesNotCompileIsRefusedBeforeTheDrain/no_cap (0.08s)
REVERT no undo between the swap's writes: fails (the row catches it)
   --- FAIL: TestAHalfAppliedReloadIsNeverObserved (0.05s)
   --- FAIL: TestAHalfAppliedReloadIsNeverObserved/pools (0.03s)
   --- FAIL: TestAHalfAppliedReloadIsNeverObserved/roster (0.03s)
REVERT the relaunch without the reload (process.go): fails (the row catches it)
   --- FAIL: TestARelaunchReloadsSquadYAMLAndLaunchesTheAddedSeat (0.02s)
REVERT scheduler run wiring no reload: fails (the row catches it)
   --- FAIL: TestARelaunchAfterASquadYAMLEditLaunchesTheNewSeatAndAdmitsItsPool (10.15s)
REVERT no per-request start in the roster launch: fails (the row catches it)
   --- FAIL: TestAReloadSwapsAtTheDrainsEndAndOnlyTheAddedSeatStartsFromTheKeyPress (0.07s)
   --- FAIL: TestARelaunchReloadsSquadYAMLAndLaunchesTheAddedSeat (0.03s)
REVERT marks.json with the launch's start for every seat: fails (the row catches it)
   --- FAIL: TestTheAddedSeatsMarksStartIsTheKeyPress (0.10s)
REVERT relaunch --start not passed to the request: fails (the row catches it)
   --- FAIL: TestARelaunchAfterASquadYAMLEditLaunchesTheNewSeatAndAdmitsItsPool (0.24s)
REVERT --add-seat ignored: fails (the row catches it)
   --- FAIL: TestTeamAddSeatAppendsOneSeatThroughTheDigest (0.00s)
REVERT --add-seat re-rendering the team rules: fails (the row catches it)
   --- FAIL: TestTeamAddSeatAppendsOneSeatThroughTheDigest (0.00s)
REVERT the add key adding tasks only: fails (the row catches it)
   not ok 1 - add, seat: team apply with the digest, one confirmation, one relaunch carrying the key press
   not ok 2 - add, seat: squad_invalid and the relaunch's refusal by name; a declined confirmation writes nothing
   not ok 3 - add, seat: a cancel at any dialog runs no verb
REVERT the add key's relaunch without --start: fails (the row catches it)
   not ok 1 - add, seat: team apply with the digest, one confirmation, one relaunch carrying the key press
REVERT the add key's confirmation skipped: fails (the row catches it)
   not ok 1 - add, seat: team apply with the digest, one confirmation, one relaunch carrying the key press
   not ok 2 - add, seat: squad_invalid and the relaunch's refusal by name; a declined confirmation writes nothing
REVERT a tool added to the lead (row E): fails (the row catches it)
   not ok 2 - the lead's model tool list carries no key verb and no gate-answer verb
REVERT the swap keeping the start's lease cap: fails (the row catches it)
   --- FAIL: TestAReloadRecomputesTheLeaseCap (0.14s)
   --- FAIL: TestAReloadRecomputesTheLeaseCap/cap_3 (0.00s)
   --- FAIL: TestARelaunchReloadsSquadYAMLAndLaunchesTheAddedSeat (0.02s)
   --- FAIL: TestAReloadRecomputesTheLeaseCapFromTheNewSquad (0.11s)
REVERT scheduler run wiring no cap (Reload.Cap returning none): fails (the row catches it)
   --- FAIL: TestAReloadRecomputesTheLeaseCapFromTheNewSquad (0.10s)
REVERT no roster_over_cap decision: fails (the row catches it)
   --- FAIL: TestAReloadRecomputesTheLeaseCap (0.14s)
   --- FAIL: TestAReloadRecomputesTheLeaseCap/cap_2 (0.00s)
   --- FAIL: TestAReloadOverTheLeaseCapLaunchesEverySeatAndWarns (0.11s)
REVERT the screen showing no over-cap line: fails (the row catches it)
   not ok 4 - over the lease cap: a warning line in the seats block, never a refusal
REVERT the cap written before Swap (outside the one step): fails (the row catches it)
   --- FAIL: TestAHalfAppliedReloadIsNeverObserved (0.05s)
   --- FAIL: TestAHalfAppliedReloadIsNeverObserved/pools (0.02s)
   --- FAIL: TestAHalfAppliedReloadIsNeverObserved/roster (0.03s)
REVERT no drain segment in status: fails (the row catches it)
   --- FAIL: TestARelaunchedSeatsBudgetNamesTheDrain (0.04s)
REVERT no drain end recorded by the roster launch: fails (the row catches it)
   --- FAIL: TestTheAddedSeatsMarksStartIsTheKeyPress (0.09s)
   --- FAIL: TestAnAddedSeatKeepsItsStartAcrossRelaunches (0.09s)
REVERT the socket only parsing the start: fails (the row catches it)
   --- FAIL: TestARelaunchStartOutOfRangeIsRefusedBeforeTheDrain (0.08s)
   --- FAIL: TestARelaunchStartOutOfRangeIsRefusedBeforeTheDrain/in_the_future (0.02s)
   --- FAIL: TestARelaunchStartOutOfRangeIsRefusedBeforeTheDrain/before_the_launch_start (0.03s)
REVERT the verb only parsing the start: fails (the row catches it)
   --- FAIL: TestSchedulerRelaunchBoundsItsStart (0.00s)
REVERT the added seats' starts cleared after each launch: fails (the row catches it)
   --- FAIL: TestAnAddedSeatsStartIsKeptUntilItLeavesTheRoster (0.00s)
REVERT a removed seat's start kept: fails (the row catches it)
   --- FAIL: TestAnAddedSeatsStartIsKeptUntilItLeavesTheRoster (0.00s)
REVERT a drain end written again on a later launch: fails (the row catches it)
   --- FAIL: TestAnAddedSeatKeepsItsStartAcrossRelaunches (0.20s)
REVERT the tick's state written from the config captured before the swap: fails (the row catches it)
   --- FAIL: TestTheStateWrittenInTheRelaunchsTickNamesTheNewRoster (0.00s)
```

Every revert fails its rows; none passes.

Two earlier runs needed redoing, recorded here so the record is whole:

- **The half-applied revert first failed at build.** Its first form
  dropped the undo calls and left `undoSquad` unused, so the package did not
  compile:

  ```text
  change 2 (row B, half-applied): no undo between the writes: FAILS (good)
      FAIL	github.com/omriariav/amq-squad/v3/internal/schedproc [build failed]
  ```

  A build failure proves nothing about the row. The redo keeps the
  variable (`_ = undoSquad`) and fails on the assertions, at both injection
  points:

  ```text
      --- FAIL: TestAHalfAppliedReloadIsNeverObserved (0.05s)
      --- FAIL: TestAHalfAppliedReloadIsNeverObserved/pools (0.03s)
      --- FAIL: TestAHalfAppliedReloadIsNeverObserved/roster (0.03s)
  ```

  The redo's summary line then read "PASSES (bad)" only because the command
  was piped through grep, which exited 0; the lines above are its output. The
  script above runs no command through a pipe.
- **The first cap revert pass ran on uncommitted edits.** Restoring a
  reverted file from git dropped the cap edits not yet committed, and a later
  revert then failed at build for that reason. The edits were reapplied and
  committed, and every cap revert was run again on the commit; the output
  above is from a clean tree.

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
`over_ceiling` is the verdict. Add `segments: .launch.timing.segments` to
the jq to read the five segments; on an idle squad `drain` is near zero.

**The idle run** is the one held to the ceiling: press the key with no
task leased (`amq-squad task list --json | jq '[.data.tasks[] |
select(.lease != null)] | length'` prints 0).

**The busy run** is recorded with no pass or fail: its drain number is the
evidence for AMQ's ask 20 upstream (a hot add would not drain). Add a
second seat the same way (handle `qa-3`, the same role, runtime, model and
effort) while at least one seat holds a task, then record:

```sh
amq-squad status --json | jq '.seats[] | select(.handle=="qa-3") | {handle, start: .launch.timing.start, to_seat_live_ms: .launch.timing.to_seat_live_ms, drain_ms: (.launch.timing.segments[] | select(.name=="drain") | .ms), slowest: .launch.timing.slowest_segment}'
```

Write down `drain_ms` and `to_seat_live_ms` beside the number of seats
that held a task at the press. The decisions show the request, then
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
