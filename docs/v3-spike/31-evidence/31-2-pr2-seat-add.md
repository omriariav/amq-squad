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
is bounded (Codex P2 1 on 5d20b5ff; `schedproc.CheckRelaunchStart`),
measured against the relaunch (the plan reviewer's settled reading): more
than 5 s after the relaunch decision (`StartSkew`), or before the start of
the launch running now (`RunningLaunchStart`: the last relaunch's own
start, else the scheduler's launch start; no floor for a scheduler started
by hand), it is refused `start_out_of_range` by name, at the verb with
nothing sent (the verb reads the last relaunch's start from the
scheduler's state, `last_relaunch_at`) and again at the socket before
anything drains. The ruling's words were "not before the relaunch
decision"; read literally that refuses every key press, which always comes
a moment before the relaunch it asks for, so the floor is the running
launch's start (a press older than it is stale) and the 5 s skew is the
ceiling.

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

### 2.4 The start mark (plan reviewer point 3, and the settled reading)

Each launch's start is that launch's, so the launch budget binds every
launch from its own mark (the plan reviewer's settled reading before the
merge):

- **The launch at `scheduler run`** keeps `seatlaunch.Options.Start`, the
  squad's `AMQ_SQUAD_START`, for every seat. That is the only launch it
  marks.
- **A relaunch stamps its own start**, the relaunch decision's time
  (`relaunchDecision` hands `s.Now()` to `RequestRelaunchAt`; a relaunch
  asked without one is stamped at the tick that takes it). `launchRoster`
  puts it on every seat's request (`SeatRequest.Start`), and
  `LaunchRoster` copies it into `Options.SeatStart` (a per-launch map,
  cloned so the launcher's own options never keep it), which `writeMarks`
  writes as each seat's `marks.json` start. A plain relaunch an hour after
  `up` therefore shows no `launch_budget_exceeded` for that hour. The first
  cut kept the squad's start for relaunched seats; that gap is what the
  reading closed.
- **A seat the relaunch's reload added** starts from the add key's press
  instead, which the request also carries as `SeatRequest.AddedAt`.
  `writeMarks` writes it as the seat's `added_at`, the record of why it
  exists, and carries `added_at` from the previous marks into every later
  launch's, so it survives relaunches and a scheduler restart. Status shows
  it as `launch.added_at`. It is never a later launch's start: from its
  next relaunch on the added seat is an existing seat and takes that
  relaunch's stamp (Codex P2 2 on 5d20b5ff had asked to carry the press;
  the reviewer's ruling replaced that commit's rows).
- **The scheduler keeps the last relaunch's start** (`LastRelaunchAt`, and
  `last_relaunch_at` in its state) as the floor of the next key press.

The key press is the confirmation's moment, not the moment `/squad add` was
typed: the owner's typing of five fields is not launch time. The drain
before the swap is: a relaunch waits for every seat to finish what it
holds, and that wait counts against every relaunched seat's budget, named
`drain` (2.4a).

### 2.4a The drain as a named segment (plan reviewer, line 1)

On a busy squad the drain is the slowest part of the added seat's budget,
and the number must say so rather than blame seat_exec or prepare.
`LaunchRoster` records when the drain ended and the launch began
(`Options.DrainEnd`), stamped before the roster plan, which ensures the
worktrees and fetches the base tips, so a slow fetch counts as `prepare`,
never as `drain` (Codex re-check on 34a5eaa2), and `writeMarks` writes it as `drain_end` in the
marks of every seat a relaunch launched (each has its own start, the
relaunch's stamp or the key press), since the drain is inside every one of
their windows. Status (`launchTiming`, internal/status/screen.go) then
lists five segments for such a seat: `drain` (its start to the drain's
end), `prepare` (the drain's end to the staged record), `mint`, `apply`,
`seat_exec`. The
over-ceiling warning names the slowest, `slowest segment drain 30.000 s`
on a busy squad. A seat of the launch at `scheduler run` has no
`drain_end` and keeps the four segments.
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
A cap of none is `squad_invalid` before the drain. That cap is the early
refusal only: the one installed is read again at the drain's end, inside
the swap's step (`RosterReload.Cap`; Codex re-check on 34a5eaa2), so a grant
lowered during a long drain is the one installed, and a cap that cannot be
read there fails the swap before anything is written. The scheduler writes
`Config.MaxSeats` with `Config.Roster`, after the slots and the router took
theirs, so the cap is part of the one step; a failed swap leaves it as it
was. `roster_reloaded` names the new cap.

Squad.yaml itself cannot outgrow `max_seats`: `team propose --add-seat`
refuses a seat past it (the owner's grant is never exceeded). A cap below
the roster at a relaunch comes only from a narrowing after the propose:
the reload's `squadOfProject` parses squad.yaml against the owner's grant
again, so a narrowed grant file refuses the relaunch `squad_invalid`, and
in practice the case is `scheduler run --max-seats`.

Over the cap, the reload follows the start (the plan reviewer's settled
reading, which withdrew the ruling that left seats unlaunched): 3.0.1's rule
for `--max-seats` is that `max_seats` caps the leases held at once, not the
seats launched. So every seat of the new roster launches, the router admits
every launched seat's pool, and the leases follow the cap: a seat beyond it
waits `seat_cap` while the others hold theirs. The scheduler logs
`roster_over_cap` naming the seats beyond the cap as "launched, leases
capped at N", and the lead's screen shows one line from status's
`live_seats` over `max_seats` (`! over the lease cap: ...`). The relaunch is
not refused. A first follow-up commit (cf7d2768) left those seats unlaunched,
with a `scheduler.unlaunched_seats` status field; a forward commit undid it
and removed the field, since nothing fills it.

The squad seat count includes the lead, which holds no lease, so a cap of
`min(max_seats, seats)` already covered one added roster seat. The
revert-failing row adds two.

## 3. Rows

Each row fails with its change reverted (section 4).

| Row | Test |
| --- | --- |
| 4 (add, seat) | pi `add, seat: team apply with the digest, one confirmation, one relaunch carrying the key press` (the exact propose, apply and relaunch argv; one confirmation; `--start` is the confirmation's moment); pi `add, seat: squad_invalid and the relaunch's refusal by name; a declined confirmation writes nothing`; pi `add, seat: a cancel at any dialog runs no verb`; cli `TestTeamAddSeatAppendsOneSeatThroughTheDigest` (propose writes nothing, apply writes the seat under the digest, the rules unchanged, a stale digest, a malformed spec, a repeated handle and `--roles` refused, a colon model kept); the start: scheduler `TestAReloadSwapsAtTheDrainsEndAndOnlyTheAddedSeatStartsFromTheKeyPress`, schedproc `TestARelaunchReloadsSquadYAMLAndLaunchesTheAddedSeat`, seatlaunch `TestTheAddedSeatsMarksStartIsTheKeyPress` (the new seat's `marks.json` start is the key press; a seat whose request carries no start of its own keeps the launch's) |
| A | cli `TestARelaunchAfterASquadYAMLEditLaunchesTheNewSeatAndAdmitsItsPool` (through `scheduler run` and `scheduler relaunch --start`: a task pinned to the new seat's pool is refused `pin_unresolved` before; squad.yaml gains rev-1; the real `PlanRoster` finds it; its request carries the key press and impl-1's none; a task pinned to its pool dispatches to rev-1 after); routing `TestARosterSeatAddedByAReloadIsAdmittedAfterTheSwapAndNotBefore`; schedproc `TestARelaunchReloadsSquadYAMLAndLaunchesTheAddedSeat` |
| B | schedproc `TestARelaunchWhoseSquadDoesNotCompileIsRefusedBeforeTheDrain` (`squad_invalid` and `relaunch_malformed` by name; no `roster_relaunch_requested`, no close, no launch; roster, squad and pools unchanged); schedproc `TestAHalfAppliedReloadIsNeverObserved` (a failure injected after the slots' write and after the pools' write: slots, pools and roster all old, `roster_reload_failed`, the old roster relaunched); scheduler `TestAFailedSwapRelaunchesTheRunningRosterUnchanged` |
| C | scheduler `TestAReloadSwapsAtTheDrainsEndAndOnlyTheAddedSeatStartsFromTheKeyPress` (s-b, removed from squad.yaml, keeps its lease and is not closed while it holds it; nothing swaps; once it finishes it closes with `roster_relaunch`, never another reason, and is not launched again; s-a's changed line, sonnet to opus, launches on its new pool in the relaunch's apply) |
| D | seatlaunch `TestTheAddedSeatsMarksStartIsTheKeyPress` (the key press 11 s before seat live and the relaunch's decision 2 s before: the mark is the press, so the seat is over the 10 s ceiling counted from the press though under it from the decision) |
| E | pi `the lead's model tool list carries no key verb and no gate-answer verb` (PR 1's row, unchanged: the exact thirteen tools) |
| Start bound (Codex P2 1, measured against the relaunch) | schedproc `TestARelaunchStartOutOfRangeIsRefusedBeforeTheDrain` (a stamp a minute ahead and one before the launch start: `start_out_of_range`, no `roster_relaunch_requested`, no launch, squad, pools and roster unchanged; an in-range stamp relaunches); schedproc `TestARelaunchStartBeforeTheLastRelaunchIsRefused` (a press after the launch start but before the last relaunch is refused; a later one relaunches); cli `TestSchedulerRelaunchBoundsItsStart` and `TestSchedulerRelaunchStartFloorIsTheLastRelaunch` (the verb refuses with nothing sent, its floor the state's `last_relaunch_at`) |
| Every launch's own start (the plan reviewer's settled reading, replacing Codex P2 2's rows) | scheduler `TestEveryRelaunchStampsItsOwnStart` (the launch at scheduler run carries no start; s-c added at T1 by a relaunch decided at R1: s-a and s-b start at R1, s-c at T1 with added_at T1; a plain relaunch at R2: every seat at R2, no added_at; `LastRelaunchAt` is R2); seatlaunch `TestAnAddedSeatKeepsAddedAtAndTakesEachLaunchsStart` (A added at T1, B at T2: each relaunch gives every seat its own start and a drain end, the presses kept as added_at T1 and T2); status `TestAPlainRelaunchsSeatsAreNotOverTheBudgetForTheGapSinceUp` (a seat relaunched an hour after up counts about 4 s from the relaunch, with a drain segment and no warning; from the squad's start the warning shows); status `TestARelaunchedSeatsBudgetNamesTheDrain` (the press shown as `launch.added_at`); cli `TestARelaunchAfterASquadYAMLEdit...` (impl-1 starts from the relaunch decision, rev-1 from the press with added_at) |
| State agrees (Codex P2 3) | scheduler `TestTheStateWrittenInTheRelaunchsTickNamesTheNewRoster` (scheduler.json read right after the relaunch's tick names s-a and s-c and max_seats 3) |
| Drain | status `TestARelaunchedSeatsBudgetNamesTheDrain` (a key press, 30 s of drain, then the launch: segments drain, prepare, mint, apply, seat_exec; drain 30000 ms, prepare 200 ms; the slowest is drain and the warning says `slowest segment drain 30.000 s`; a seat without a drain end keeps four segments); seatlaunch `TestTheAddedSeatsMarksStartIsTheKeyPress` (the added seat's `drain_end` lies between the key press and seat live; the existing seat has none); seatlaunch `TestASlowFetchLandsInPrepareNotDrain` (a 300 ms plan: drain_end before the plan began, the staged record at least 300 ms after it) |
| Cap | scheduler `TestAReloadRecomputesTheLeaseCap` (the lead's row: two roster seats under a cap of 2 add a third; under a recomputed cap of 3 it gets a lease while the other two hold theirs; under a cap of 2 it launches, `roster_over_cap` names it "launched, leases capped at 2", and its task waits `seat_cap`); schedproc `TestAReloadOverTheCapLaunchesEverySeatAndCapsTheLeases` (a cap of 3 over four roster seats: all four launch, the fourth's pool reaches the router, the cap is 3); cli `TestAReloadRecomputesTheLeaseCapFromTheNewSquad` (through the verbs, squad.yaml's max_seats 4 over lead and impl-1: rev-1 and rev-2 added, three tasks leased at once, `at most 4 leases`); cli `TestAReloadOverTheLeaseCapLaunchesEverySeatAndWarns` (under `--max-seats 2`: every added seat launches, `roster_over_cap` names rev-2 "launched, leases capped at 2", two leases and the third `seat_cap`, the state's 3 live seats over max_seats 2); pi `over the lease cap: a warning line in the seats block, never a refusal`; cli `TestTeamAddSeatAppendsOneSeatThroughTheDigest` (a seat past squad.yaml's max_seats refused at propose); schedproc rows: the cap travels with the reload, a cap of none is `squad_invalid` before the drain, a failed swap leaves the cap; schedproc `TestTheCapInstalledAtTheSwapIsTheGrantReadAtTheDrainsEnd` (the grant narrowed between acceptance and the swap: the narrowed cap is installed); scheduler `TestACapThatCannotBeReadAtTheSwapWritesNothing` |

No test reaches the network, the real HOME, tmux, AMQ or Buzz: the cli
rows run `scheduler run` with the fake launcher seam and a temporary HOME
and repository; the seatlaunch row runs the real launcher over the fake
AMQ CLI with every pane question stubbed; the pi rows run the stand-in pi
with a scripted runner.

## 4. The revert runs

Each change was reverted alone in the working tree, its rows were run, and
the file was restored from git. The script is
docs/v3-spike/31-evidence/31-2-pr2-revert-runs.py (it refuses a dirty
tree). Its output at the head that carries the plan reviewer's settled
readings (over the cap every seat launches and the leases stay capped; every
relaunch stamps its own start), verbatim (the pi lines are node's `not ok`
lines). The reverts of the withdrawn unlaunched-seat rows and of Codex P2
2's kept starts are gone with the rulings that replaced them:

```text
REVERT router pools fixed at open (SetRosterPools a no-op): fails (the row catches it)
   --- FAIL: TestARosterSeatAddedByAReloadIsAdmittedAfterTheSwapAndNotBefore (0.00s)
   --- FAIL: TestARelaunchAfterASquadYAMLEditLaunchesTheNewSeatAndAdmitsItsPool (10.19s)
REVERT the swap at the request, not the drain's end: fails (the row catches it)
   --- FAIL: TestAReloadSwapsAtTheDrainsEndAndOnlyTheAddedSeatStartsFromTheKeyPress (0.05s)
REVERT the roster written although Swap failed: fails (the row catches it)
   --- FAIL: TestAFailedSwapRelaunchesTheRunningRosterUnchanged (0.01s)
REVERT a compile error ignored (the relaunch drains): fails (the row catches it)
   --- FAIL: TestARelaunchWhoseSquadDoesNotCompileIsRefusedBeforeTheDrain (0.31s)
   --- FAIL: TestARelaunchWhoseSquadDoesNotCompileIsRefusedBeforeTheDrain/squad_invalid (0.08s)
   --- FAIL: TestARelaunchWhoseSquadDoesNotCompileIsRefusedBeforeTheDrain/no_seat_but_the_lead (0.08s)
   --- FAIL: TestARelaunchWhoseSquadDoesNotCompileIsRefusedBeforeTheDrain/no_cap (0.08s)
REVERT no undo between the swap's writes: fails (the row catches it)
   --- FAIL: TestAHalfAppliedReloadIsNeverObserved (0.05s)
   --- FAIL: TestAHalfAppliedReloadIsNeverObserved/pools (0.03s)
   --- FAIL: TestAHalfAppliedReloadIsNeverObserved/roster (0.02s)
REVERT the relaunch without the reload (process.go): fails (the row catches it)
   --- FAIL: TestARelaunchReloadsSquadYAMLAndLaunchesTheAddedSeat (0.03s)
REVERT scheduler run wiring no reload: fails (the row catches it)
   --- FAIL: TestARelaunchAfterASquadYAMLEditLaunchesTheNewSeatAndAdmitsItsPool (10.15s)
REVERT no key press start for the added seat in the roster launch: fails (the row catches it)
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
   --- FAIL: TestAReloadRecomputesTheLeaseCap (0.13s)
   --- FAIL: TestAReloadRecomputesTheLeaseCap/cap_3 (0.00s)
   --- FAIL: TestARelaunchReloadsSquadYAMLAndLaunchesTheAddedSeat (0.02s)
   --- FAIL: TestAReloadRecomputesTheLeaseCapFromTheNewSquad (0.11s)
REVERT scheduler run wiring no cap (Reload.Cap returning none): fails (the row catches it)
   --- FAIL: TestAReloadRecomputesTheLeaseCapFromTheNewSquad (0.13s)
REVERT the cap written before Swap (outside the one step): fails (the row catches it)
   --- FAIL: TestAHalfAppliedReloadIsNeverObserved (0.05s)
   --- FAIL: TestAHalfAppliedReloadIsNeverObserved/pools (0.02s)
   --- FAIL: TestAHalfAppliedReloadIsNeverObserved/roster (0.02s)
REVERT no drain segment in status: fails (the row catches it)
   --- FAIL: TestARelaunchedSeatsBudgetNamesTheDrain (0.04s)
REVERT no drain end recorded by the roster launch: fails (the row catches it)
   --- FAIL: TestTheAddedSeatsMarksStartIsTheKeyPress (0.09s)
REVERT the socket only parsing the start: fails (the row catches it)
   --- FAIL: TestARelaunchStartOutOfRangeIsRefusedBeforeTheDrain (0.07s)
   --- FAIL: TestARelaunchStartOutOfRangeIsRefusedBeforeTheDrain/in_the_future (0.02s)
   --- FAIL: TestARelaunchStartOutOfRangeIsRefusedBeforeTheDrain/before_the_launch_start (0.02s)
REVERT the verb only parsing the start: fails (the row catches it)
   --- FAIL: TestSchedulerRelaunchBoundsItsStart (0.00s)
REVERT the tick's state written from the config captured before the swap: fails (the row catches it)
   --- FAIL: TestTheStateWrittenInTheRelaunchsTickNamesTheNewRoster (0.00s)
REVERT added_at not carried into later marks: fails (the row catches it)
   --- FAIL: TestAnAddedSeatKeepsAddedAtAndTakesEachLaunchsStart (0.19s)
REVERT added_at not written at the press's launch: fails (the row catches it)
   --- FAIL: TestAnAddedSeatKeepsAddedAtAndTakesEachLaunchsStart (0.09s)
REVERT status not showing added_at: fails (the row catches it)
   --- FAIL: TestARelaunchedSeatsBudgetNamesTheDrain (0.04s)
REVERT no roster_over_cap decision: fails (the row catches it)
   --- FAIL: TestAReloadRecomputesTheLeaseCap (0.14s)
   --- FAIL: TestAReloadRecomputesTheLeaseCap/cap_2 (0.00s)
   --- FAIL: TestAReloadOverTheLeaseCapLaunchesEverySeatAndWarns (0.11s)
REVERT the roster trimmed to the cap at a reload (seats beyond it unlaunched): fails (the row catches it)
   --- FAIL: TestAReloadOverTheCapLaunchesEverySeatAndCapsTheLeases (0.02s)
   --- FAIL: TestAReloadOverTheLeaseCapLaunchesEverySeatAndWarns (0.11s)
REVERT the screen showing no over-cap line: fails (the row catches it)
   not ok 4 - over the lease cap: a warning line in the seats block, never a refusal
REVERT a relaunch carrying no stamp of its own (the squad's start for its seats): fails (the row catches it)
   --- FAIL: TestEveryRelaunchStampsItsOwnStart (0.00s)
   --- FAIL: TestARelaunchReloadsSquadYAMLAndLaunchesTheAddedSeat (0.03s)
   --- FAIL: TestARelaunchAfterASquadYAMLEditLaunchesTheNewSeatAndAdmitsItsPool (0.27s)
REVERT the key press kept as a later launch's start: fails (the row catches it)
   --- FAIL: TestEveryRelaunchStampsItsOwnStart (0.00s)
REVERT drain_end for the added seat only: fails (the row catches it)
   --- FAIL: TestAnAddedSeatKeepsAddedAtAndTakesEachLaunchsStart (0.09s)
REVERT the socket's floor the scheduler's launch start, not the last relaunch: fails (the row catches it)
   --- FAIL: TestARelaunchStartBeforeTheLastRelaunchIsRefused (0.05s)
REVERT the verb's floor the launch record's start, not the last relaunch: fails (the row catches it)
   --- FAIL: TestSchedulerRelaunchStartFloorIsTheLastRelaunch (0.00s)
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
amq-squad status --json | jq '.seats[] | select(.handle=="qa-2") | {handle, launch: .launch.launch_id, added_at: .launch.added_at, start: .launch.timing.start, seat_live: .launch.timing.seat_live, to_seat_live_ms: .launch.timing.to_seat_live_ms, ceiling_ms: .launch.timing.ceiling_ms, over_ceiling: .launch.timing.over_ceiling, slowest: .launch.timing.slowest_segment}'
cat .agent-mail/v31/extensions/amq-squad/launch/qa-2/marks.json
grep -E '"kind":"(roster_relaunch_requested|roster_reloaded|roster_launched)"' .agent-mail/v31/extensions/amq-squad/scheduler/decisions.jsonl | tail -n 3
```

Expect `added_at`, `start` and the `marks.json` start equal to the key
line's STAMP (UTC); every other seat's `start` is the relaunch decision's
time, a moment after it; `ceiling_ms` 10000, and `to_seat_live_ms` measured from it;
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
