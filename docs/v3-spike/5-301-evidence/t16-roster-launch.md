# 3.0.1 t16: the roster launch (execution note)

Branch v3-301-t16 from origin/v3 a1a67151. Brief: 3.0.1-t16, the first half
of the reviewer ruling of 2026-10-30 (session v31).

## The defect, cited in AMQ v0.116.2

The scheduler launched one seat per seat request (Slots.Plan narrowed the
seat list to one seat; Launcher.RequestSeat ran one Prepare and one Apply).
AMQ's reconcile refuses a launch into a present binding unless every
planned seat resumes:

- `internal/launch/reconcile.go:1204`: a planned agent is a write when
  `disposition != DispositionResumed`; a fresh seat is always a write.
- `reconcile.go:374-377`: `attachSafe` is false when any planned agent writes.
- `reconcile.go:1355-1360` (`chooseReconcileBackend`, from line 1280): a
  present binding (`InspectPresent`) with `!attachSafe` returns outcome
  `action_required`, reason `binding_present_without_resumable_conversation`.

So the first seat's Apply commits the session binding and every later
single-seat Apply is refused while that seat's tmux session lives.

The join path does not help a replacement, which is why ask 20 stands:
`internal/launch/liveseat.go:121` (`reconcileLiveSeats`) joins new seats
into a present binding only with `on_live: keep` and only for a seat that is
`Missing` from the binding's resources (`liveseat.go:58-72`, `seatFacts
FromBinding`): a seat that ran and is gone still has its resource in the
binding, and the binding-level inspection reports it present, so it
classifies as kept or refused, never created.

## Design (written before the code)

What moves:

1. **One roster launch.** The scheduler gets the roster
   (`scheduler.Config.Roster`, one `RosterSeat{Handle, Pool}` per seat of the
   typed seat list that has a worktree of its own; the CLI builds it from
   the slots, and a seat whose worktree is the repository root is left out
   and printed, as today it could never start). In roster mode the first
   tick that passes the start-up barrier issues one request id per roster
   seat (the same `seat-req-<incarnation>-<n>` reservations, each now
   naming its handle) and calls the launcher's new `RosterLauncher.
   LaunchRoster(reqs)`: one `seatlaunch.Launch` of the whole roster, so one
   Prepare and one Apply, one binding, every seat fresh. Each seat that
   proves posts its `SeatUp` with its request id; the scheduler applies
   them in the same tick (`seat_up` per seat). The launcher records each
   seat's request with the same state machine as a single launch (settle
   generalised to many seats), so a seat that did not prove is cancelled
   through the existing reservation expiry, never relaunched alone.
2. **Slots govern dispatch.** In roster mode dispatch never requests a
   seat. A pending task goes to an idle roster seat whose pool serves it
   (`PoolServes`: the slot rule, a pi seat without a model serves any pi
   model). `max_seats` caps live leases: when the in-progress leases reach
   it, the task waits `seat_cap` even with an idle seat. The router's
   concurrency caps see only seats that hold something: `Occupancy` omits a
   parked roster seat (idle, not closing), and the router already counts
   every leased task's holder. Parked seats are never closed for idleness;
   an unresponsive one (it let a lease lapse) still closes after the idle
   timeout. A usage-limit window still makes a task with no idle seat wait
   `usage_limit`. A pool with no roster seat is refused as today
   (`seat_request_failed`, "no free seat in the squad's seat list for pool
   P").
3. **Restart adopts.** In roster mode `schedproc.Run` adopts each live seat
   of the launch ledger (`LiveSeats`: the accepted proof's pid and raw start
   stamp match the live process, the check a launch's close uses) as up when
   its handle is a roster seat and its staged record's pool is the roster
   pool: decision `seat_adopted`. A live seat outside the roster, or whose
   pool changed, is closed as before (`seat_adopted_closing`, with the
   reason). Every roster seat adopted: no launch (`roster_adopted`). None:
   the roster launches. Some: no launch (it would be refused); decision
   `roster_incomplete` names the missing seats, which take the replacement
   path below when a task needs them.
4. **Replacement is honest.** When a pending task's pool has roster seats
   but none of them is live (gone, closed, quarantined, never up) and no
   reservation of that pool is pending, the scheduler logs
   `seat_replacement_needs_relaunch` (seat: the gone handles, reason:
   `binding_present_without_resumable_conversation`, the AMQ code a fresh
   seat would get) and blocks the task for the lead with reason
   `seat_replacement_needs_relaunch`; the block detail names the verb.
   Status shows the blocked task (row and attention). No `seat_requested`,
   no retry.
5. **The lead's relaunch.** `amq-squad scheduler relaunch [--reason]` is the
   lead's decision, sent over the scheduler's decision socket (proved like a
   widening, R4) as `amq-squad.relaunch.v1`. The process posts a
   `RosterRelaunch` event. The scheduler holds dispatch
   (`roster_relaunch_requested`, then `dispatch_held` naming the leases it
   waits for), lets every leased seat finish, then closes every live seat
   (`seat_closed`, reason `roster_relaunch`), cancels any reservation, and
   launches the roster again in one apply. After any full roster launch the
   tasks blocked `seat_replacement_needs_relaunch` are reset to pending
   (`task_reset`).
6. **A failed roster launch is logged and ends the process.** Stage
   (`prepare_refused`, `decisions_pending`, `apply_failed`, `launch_error`),
   AMQ's reason code and the failure detail (through `redact.Text`) go into
   one decision `roster_launch_failed`; Tick returns a
   `*scheduler.RosterLaunchError`, and `schedproc.Run` logs it and returns
   it, so `scheduler run` exits with it printed. Seats that did not prove
   after an applied launch are `seat_not_up` decisions, not a failure.

What stays: the on-demand path (no roster in the config) is unchanged, so
every existing row keeps its meaning; the request-id identity of every
`SeatUp`; the proof, the close by proved identity, the exits ledger and the
start-up barrier; the decision socket's proof of the lead.

Decision kinds added: `roster_launched`, `roster_launch_failed`,
`seat_not_up`, `roster_adopted`, `roster_incomplete`, `seat_adopted`,
`seat_replacement_needs_relaunch`, `roster_relaunch_requested`,
`task_reset`. Reused with a new reason: `dispatch_held` (relaunch),
`seat_closed` (`roster_relaunch`), `seat_adopted_closing` (not in the
roster, pool changed).

## Rows (fake AMQ, fake launcher; no network, no real HOME)

No row reaches the tmux server: every seatlaunch row runs with a refusing
tmux stub first on PATH (round 3; it answers only `tmux -V`, the version
probe of AMQ's launcher detection, and fails the package run if anything
else calls it), and the pane questions go through seams. Round 1 claimed
"no tmux" without that proof, and it was not true: the proof server's pane
id lookup ran `tmux list-panes -a` from every proving row (Codex finding 6).

| Brief row | Test | Package |
| --- | --- | --- |
| 1. one prepare carries every roster seat, one apply, all up, seat_up each | TestRosterLaunchIsOnePrepareAndOneApplyForEverySeat (real Launcher over the fake amq CLI, real proofs over the launch socket) | seatlaunch |
| 1. the verb launches the roster at start, a root-worktree seat left out | TestSchedulerRunLaunchesTheRosterAtStart | cli |
| 2. two seats of different pools hold leases at once, max_seats 2 | TestRosterSeatsOfTwoPoolsHoldLeasesAtOnce | scheduler |
| 3. seat cap 1 with two seats up: one lease at a time | TestRosterSeatCapOfOneMeansOneLeaseAtATime | scheduler |
| 4. restart with two live seats adopts both | TestRestartAdoptsTheLiveRosterSeats | schedproc |
| 4. a live seat outside the roster, or with another pool, is closed with the reason | TestRestartClosesALiveSeatTheRosterDoesNotProve | schedproc |
| 4. partial roster: adopted, no launch; whole roster: roster_adopted | TestPartialRosterIsAdoptedAndNotLaunched | scheduler |
| 5. gone seat: seat_replacement_needs_relaunch once, lead block, no seat_requested; the relaunch drains, closes, relaunches, resets | TestGoneRosterSeatBlocksForTheLeadsRelaunch | scheduler |
| 5. the relaunch over the decision socket; refused with no roster | TestLeadsRelaunchReachesTheSchedulerOverItsSocket | schedproc |
| 5. `scheduler relaunch` verb (reason, accepted, refused, starting, no --me) | TestSchedulerRelaunchVerb | cli |
| 5. status shows the relaunch block (row and attention) | TestStatusShowsTheRelaunchBlock | cli |
| 6. failed apply: one decision with stage, reason, detail, credential-free | TestRosterApplyRefusalIsLoggedWithItsReason | seatlaunch |
| 6. the process ends with the failure | TestFailedRosterLaunchEndsTheProcess | schedproc |
| parked seats stay up past the idle timeout; an unresponsive one closes | TestParkedRosterSeatIsNotClosedForIdleness | scheduler |
| concurrency caps count only seats that hold something | TestRosterOccupancyOmitsParkedSeats | scheduler |
| the roster launch waits for a pending exit check (N3) | TestRosterLaunchWaitsForAPendingExitCheck | scheduler |
| seat_not_up, reservation expiry then the relaunch block; no roster launcher | TestRosterSeatNotUpAndALauncherWithoutRoster | scheduler |
| a seat that did not prove is cancelled by its own pane (not ambiguous) | TestRosterSeatThatDidNotProveIsCancelledByItsOwnPane | seatlaunch |
| no roster seat for a pool; a modelless pi seat serves any pi model | TestRosterPoolRules | scheduler |
| relaunch refused with no roster; the state file names roster and relaunch | TestRelaunchRefusedWithoutARosterAndStateNamesIt | scheduler |
| the roster and its plan from the slots | TestSlotsRosterAndItsPlan | schedproc |
| the roster goes to one launcher for one apply | TestLauncherSendsTheRosterToOneLauncher | schedproc |

Changed existing rows: the CLI's fake launcher gained LaunchRoster (the verb
always launches a roster now), and the preflight-baseline fixture
(internal/cli/seat_fix1b_test.go) creates its seat worktree, because the
roster launches at start even with an empty store.

### Revert check

Each change undone alone, the rows that cover it run and must fail
(`revert_check.py` in the job's scratch directory; the file restored from
git after each).

| Mutation | Row | Result |
| --- | --- | --- |
| roster mode off | TestRosterLaunchIsOnePrepareAndOneApplyForEverySeat, TestRosterSeatsOfTwoPoolsHoldLeasesAtOnce, TestSchedulerRunLaunchesTheRosterAtStart | fail |
| the lease cap off | TestRosterSeatCapOfOneMeansOneLeaseAtATime | fail |
| restart closes every live seat | TestRestartAdoptsTheLiveRosterSeats | fail |
| on-demand dispatch in roster mode | TestGoneRosterSeatBlocksForTheLeadsRelaunch | fail |
| the failure not logged, not returned | TestRosterApplyRefusalIsLoggedWithItsReason, TestFailedRosterLaunchEndsTheProcess | fail |
| parked seats closed for idleness | TestParkedRosterSeatIsNotClosedForIdleness | fail |
| parked seats in the occupancy | TestRosterOccupancyOmitsParkedSeats, TestRosterLaunchIsOnePrepareAndOneApplyForEverySeat | fail |
| another roster seat's pane makes a request ambiguous | TestRosterSeatThatDidNotProveIsCancelledByItsOwnPane | fail |
| no hold for a pending exit check | TestRosterLaunchWaitsForAPendingExitCheck | fail |
| the socket ignores the relaunch kind | TestLeadsRelaunchReachesTheSchedulerOverItsSocket | fail |
| the failure detail not redacted | TestRosterApplyRefusalIsLoggedWithItsReason | fail |
| no `relaunch` verb | TestSchedulerRelaunchVerb | fail |
| a root-worktree seat kept in the roster | TestSlotsRosterAndItsPlan, TestSchedulerRunLaunchesTheRosterAtStart | fail |
| a roster split across launchers forwarded | TestLauncherSendsTheRosterToOneLauncher | fail |
| no reset after the relaunch | TestGoneRosterSeatBlocksForTheLeadsRelaunch | fail |
| the relaunch does not wait for leases | TestGoneRosterSeatBlocksForTheLeadsRelaunch | fail |
| the modelless pi rule off | TestRosterPoolRules | fail |
| the state file without the relaunch | TestRelaunchRefusedWithoutARosterAndStateNamesIt | fail |
| a partial roster launched anyway | TestPartialRosterIsAdoptedAndNotLaunched | fail |
| seat_not_up not logged | TestRosterSeatNotUpAndALauncherWithoutRoster | fail |

TestStatusShowsTheRelaunchBlock covers no code change (status already
renders a blocked task's reason); it pins the "status shows it" clause.

### Gate

```
go build ./... && go vet ./internal/seatlaunch ./internal/schedproc ./internal/scheduler ./internal/cli && go test -count=1 ./internal/seatlaunch ./internal/schedproc ./internal/scheduler ./internal/cli
ok  	github.com/omriariav/amq-squad/v3/internal/seatlaunch
ok  	github.com/omriariav/amq-squad/v3/internal/schedproc
ok  	github.com/omriariav/amq-squad/v3/internal/scheduler
ok  	github.com/omriariav/amq-squad/v3/internal/cli
```

`make fmt-check readme-html-check` pass (README.html regenerated from
README.md).

## Owner-run row (3.0.1 owner run): two seats of different pools up at once

In a scratch repository with the owner's grants file and registry, a lead
proved through the seat launch (the decision socket proves the lead), and
the 3.0.1 build on PATH. `ROOT` is the session root.

```sh
cd ~/scratch/t16-live && git init -q -b main && git commit -q --allow-empty -m base
mkdir -p roles && printf '# Role: implementer\n\nYou implement tasks.\n' > roles/impl.md
amq session create t16 --root "$PWD/.agent-mail" --me lead
ROOT="$PWD/.agent-mail/t16"
cat > seats.json <<JSON
{"project_root": "$PWD", "session": "t16", "seats": [
 {"handle": "impl-1", "runtime": "claude", "model": "sonnet", "effort": "medium", "role_file": "$PWD/roles/impl.md", "worktree": "$PWD/../t16-wt/impl-1", "branch": "seat/impl-1"},
 {"handle": "rev-1", "runtime": "codex", "model": "gpt-5.4", "effort": "medium", "role_file": "$PWD/roles/impl.md", "worktree": "$PWD/../t16-wt/rev-1", "branch": "seat/rev-1"}]}
JSON
amq-squad lead --team --scheduler-seats seats.json --trust   # round 4: the owner run starts here, never a hand-started scheduler
LOG="$ROOT/extensions/amq-squad/scheduler/decisions.jsonl"
jq -c 'select(.kind|test("roster_|seat_up|seat_not_up"))' "$LOG"
# pass: one roster_launched ("2 seats in one apply"), seat_up impl-1, seat_up rev-1, no seat_not_up
jq -c '{handle, pid, launch_id}' "$ROOT"/extensions/amq-squad/launch/{impl-1,rev-1}/proof.json
# pass: two accepted proofs, two pids, one launch_id
amq-squad task add --title A --class implement --files a.txt --pool claude:sonnet:medium --root "$ROOT"
amq-squad task add --title B --class implement --files b.txt --pool codex:gpt-5.4:medium --root "$ROOT"
amq-squad status --root "$ROOT" --json | jq -c '.tasks[] | {id, bucket, lease_holder}'
# pass: t1 held by impl-1 and t2 by rev-1 at the same time (both "working")
```

Replacement leg (checks the relaunch against real AMQ; the rows prove it
against the fake only):

```sh
kill "$(jq .pid "$ROOT/extensions/amq-squad/launch/rev-1/proof.json")"   # rev-1 gone
# restart the scheduler (stop the lead's team mode and start it again)
jq -c 'select(.kind|test("seat_adopted|roster_incomplete"))' "$LOG"           # impl-1 adopted, rev-1 missing
amq-squad task add --title C --class implement --files c.txt --pool codex:gpt-5.4:medium --root "$ROOT"
jq -c 'select(.kind=="seat_replacement_needs_relaunch")' "$LOG"              # once, reason binding_present_without_resumable_conversation
# the relaunch (round 4): the owner asks the manager in the pi TUI, in words,
# to relaunch the roster because rev-1 is gone; the manager calls its tool
# amq_scheduler_relaunch, which runs `amq-squad scheduler relaunch --reason ...`
# from the lead's process. Record the owner's words and the tool call as the row.
jq -c 'select(.kind|test("roster_relaunch|seat_closed|roster_launch|task_reset"))' "$LOG"
# pass: seat_closed impl-1 roster_relaunch, roster_launched, task_reset t3, then t3 dispatched to rev-1
# if instead roster_launch_failed ... binding_present_without_resumable_conversation: the closed
# providers left the binding's tmux session present (see open point 1)
```

## What the brief got wrong, and open points

1. **Unverified: a relaunch finds the binding absent.** The relaunch closes
   every seat's provider process (SIGTERM by proved identity; amq-squad never
   closes a pane, R6) and then launches the roster again. That apply succeeds
   only if AMQ then inspects the binding as absent, which holds when every
   pane exits with its provider and the bound tmux session ends. If the
   session survives (a pane that outlives its process, or a lead pane in
   the same session), the relaunch apply gets the same refusal and the
   process exits with roster_launch_failed. The owner-run replacement leg
   decides it; the rows prove the scheduler side against the fake only.
2. **No producer of SeatGone or SeatActivity in production.** Nothing
   outside the scheduler posts either event (only tests do). So a roster
   seat whose process dies on its own is not seen as gone until a restart
   (it is then not adopted). The replacement path fires for seats the
   scheduler closed itself (quarantine, an expired claimed lease, an
   unresponsive seat after the idle timeout), for seats that never proved,
   and after a restart. Before this change the same gap existed; it now
   matters more because a parked seat lives long. A follow-up: post
   SeatGone from the launch ledger's pid check each tick.
3. **"Cleanly" is read strictly.** The relaunch waits until no seat holds a
   lease, a refused done report, or a running commit step; the lead may
   have to release or reset those for it to proceed. The dispatch_held
   decision names what it waits for.
4. **The concurrency pools.** A parked seat no longer counts toward a
   registry concurrency pool. For pi seats on a local GPU a parked process
   may still hold model memory; the brief's rule (leases, not panes) is
   implemented as written.
5. **A pool with no roster seat** logs seat_request_failed every tick, as
   the on-demand path did.
6. **The relaunch sends no AMQ record message** (task widen sends one);
   the decisions log and the lead's own transcript are the record.
7. Outside the declared files: internal/cli/seat_fix1b_test.go (a fixture
   creates its seat worktree) and README.html (regenerated from README.md
   for `make ci`). compile.go is untouched.
8. **A launch that needs a decision now stops the scheduler.** `lead --team`
   starts `scheduler run` without `--trust` (internal/cli/lead_scheduler.go),
   so when AMQ's prepare returns trust_confirmation the roster launch fails
   at decisions_pending, the process exits with it, and the lead's
   supervisor restarts it and gives up after five fast failures. Before,
   each seat launch failed quietly and the task waited. Whether team mode
   passes `--trust` (the owner's standing answer) is a lead decision; t17
   widens the failure reporting.

## Round 2: the reviewer's rulings on the open points (2026-10-30)

**Ruling 1: the roster's tmux session holds seats only.** Where the lead
and the scheduler live today:

- AMQ binds one tmux session per session root: the binding is
  `<session root>/…/binding.json` (AMQ v0.116.2 internal/launch/binding.go:132),
  its session named from the project and the AMQ session
  (`amq-<project>-<hash>-<session>-<hash>`, tmux_names.go:11), created at an
  Apply (tmux.go:140).
- `amq-squad lead --team --scheduler-seats FILE` (internal/cli/pi_lead.go:99)
  starts `scheduler run` as its own child (internal/cli/lead_scheduler.go:115,
  `exec.CommandContext(self, "scheduler", "run", …)`) and runs pi as another
  child (internal/cli/pi.go:420, `cmd.Run`). Neither creates or joins a tmux
  session: both live wherever the owner started `lead --team`, his own
  terminal or a session of his own, so they do not share the roster's
  session.
- They do share it when the lead's seat is itself a seat of the squad's
  launch: the squad.yaml lead (`lead: cto`, a seat) compiles into the same
  typed seat list (internal/team/launch.go:18), and an AMQ launch of it in the
  same session puts its pane in the roster's binding. And the decision socket
  proved the lead only from the proof ledger entry of such a launch
  (RecordedLeadPane, internal/schedproc/decide.go:99), so a lead outside the
  binding could send no decision (origin_unknown), a lead inside it made every
  relaunch fail by construction.

The change: (a) the roster leaves out the `--lead` seat (`Slots.Roster(lead)`,
"seat X is the lead (--lead); the lead runs outside the roster's tmux
session"), so the scheduler never launches the lead into the roster's
session; (b) a scheduler started by the lead's team mode (the existing
`startedByLeadTeam` check, the one `--lead-pool` relies on) takes its own
parent process, the team-mode lead, as the lead's incarnation
(`schedproc.SupervisingLead`: its pid and raw start stamp read at the
scheduler's start, the stamp re-read at every decision; gone or reused is
lead_gone from round 3 on). pi runs as that process's child, so the pi lead's commands
are proved by the same parent-chain walk as before. Outside team mode the
ledger path is unchanged. The lead is told by message (this changes who
counts as the lead from "the AMQ-launched proved seat" to "the team-mode
process that started this scheduler"). A pane that outlives its process
stays the live row's question; ask 20 is the fix.

Rows: TestSupervisingLeadProvesTheTeamModeLead (schedproc: the real socket,
kernel peer and parent chain; a reused parent is origin_unknown),
TestSchedulerTakesItsTeamModeParentAsTheLead (cli: the wiring, and none
outside team mode), TestSchedulerRunLaunchesTheRosterAtStart now also checks
the lead seat is left out.

**Ruling 2.** The SeatGone producer is t17's; open point 2 stays as written.

**Ruling 3: pi seats park like the others.** The model memory is the local
server's, not the seat's: ollama's keep_alive unloads an idle model, and the
seat process holds none, so a parked pi seat costs the GPU nothing after
the keep-alive window. If the owner run measures otherwise, close-on-park
for pi is the conditional 3.0.x line in docs/v3-followups.md.

**Ruling 4: `--trust` on the lead's command line.** `amq-squad lead --team
--scheduler-seats FILE --trust` passes `--trust` to `scheduler run`
(leadSchedulerCommand); a flag the owner typed is his standing answer for
that session, and there is no grant-file field. `--trust` without team mode
and a seat list is a usage error. Without it, a roster launch whose Prepare
asks for trust_confirmation is refused at start with a reason that names
the flag, and `scheduler run` exits with ExitSchedulerRefused (17). Every
refusal of AMQ (prepare_refused, decisions_pending, apply_failed) takes
that exit; a launch_error (a probe, a plan, an amq that did not answer)
keeps the crash exit and the supervisor's restarts. The supervisor does not
restart exit 17 ("refused to start … not restarted"), so the refusal
happens once.

Rows: TestLeadTeamPassesTrustToTheScheduler,
TestSchedulerRunRefusesAtStartNamingTrust,
TestSupervisorDoesNotRestartARefusal.

Files outside the first round's list: internal/cli/lead_scheduler.go and its
test (widened by the ruling), internal/cli/pi_lead.go (the `--trust` flag of
`lead`, three lines; the flag parsing lives there) and
internal/cli/repass_routing_test.go (one call site of leadSchedulerCommand).

## Round 3: Codex findings on 39e1ef01 and the reviewer's conditions

**Codex 1: adoption matches the proof to its record.** `LiveSeats` now
carries the proof's and the staged record's launch and request ids and the
record's worktree; the roster seat carries its worktree. A live roster seat
is adopted only when the proof's launch id is set and equals the record's,
the request ids match, the pool is the roster's and the worktree is the
roster's; otherwise it is closed with the reason (a later launch that
overwrote the record and failed at Apply leaves the old provider alive
under a record that is not its own). Row: TestRestartClosesALiveSeatTheRosterDoesNotProve
(the overwritten record, the other worktree).

**Codex 2: placement is each seat's own.** `placedSeat` judges the request's
own seat: another seat's seat-exec refusal no longer makes a placed seat
"not placed". Row: TestRosterPlacementIsEachSeatsOwn (cto proves, dev is
refused, ops is placed and never proves; ops's cancel waits for its own
pane).

**Codex 3: a lingering resource is refused before Apply, with one
correction.** Prepare's per-handle `resource` is the binding *record's*
resource, not a live inspection (AMQ v0.116.2 internal/launch/prepare.go:686,
`applyBindingResources` at 1424, copies `binding.Resources` into every
observation; `binding.json` is never removed, binding.go has no delete
path). So "a resource is named" holds after every launch, live or not, and
refusing on it would refuse every relaunch. The check therefore asks, for
each roster handle whose Prepare observation names a resource, whether that
pane still exists, through the launcher's read-only pane check (the one the
cancel path uses). A pane that exists, or whose state cannot be read,
refuses the launch before anything is staged or applied: stage
`resource_present`, reason `seat_replacement_resource_present`, the handles
and panes named (`Options.BeforeApply`, used by `LaunchRoster`). It applies
to every roster launch, the start and the relaunch. The scheduler logs
roster_launch_failed and exits with the refusal code. Row:
TestRosterRelaunchRefusedWhileARecordedPaneLingers (a lingering pane
refused with no Apply; a recorded pane that is gone applies).

The stated limit: a pane that outlives its process still leaves the roster
unreplaceable until ask 20 lands, and `seat_replacement_resource_present`
is the symptom the owner would see. The pane check is per pane; a roster
session that keeps a window with no seat pane is not seen by it (the
roster's session holds seats only, ruling 1), and AMQ's own refusal at
Apply remains the backstop.

**Codex 4: a dead parked seat goes undetected** (scheduler.go, closeIdle and
no SeatGone producer). Not this brief's: t17 adds SeatGone each tick (open
point 2 and ruling 2).

**Codex 5: multiline redaction.** The failure detail and seat_not_up go
through `redact.Block`, which masks a quoted value on the line after its key
(`redact.Text` is line-based and let it through). Row: the canary row
carries `api_key:` with the quoted value on the next line.

**Codex 6: no row touches the tmux server.** `Options.PaneID` is the seam of
the proof server's pane-id lookup; the shared test options stub it, and
the rows that built their own proof-server options got no-pane seams (what
a host tmux gave them before). The proof is the refusing stub on PATH in
the package's TestMain (above). It found six rows that reached tmux:
TestPiSeatPlansThroughTheUpstreamAdapter and
TestCodexSandboxPinsAreAdmittedAndDigested (`tmux -V`, AMQ's detection,
answered by the stub), TestVanishingProcessEntryIsOriginUnknown,
TestWindowIsPerTicketFromMinting, TestTokenMintedSixtyOneSecondsEarlierIsExpired
and TestPaneLookupIsBoundedAndOutsideTheMutex (`list-panes`, now seams).

**Codex 7.** Plan 4.5's earlier sentences are rewritten: the barrier closes a
seat of the previous incarnation only when the roster does not adopt it,
and the seat cap counts live leases.

**Reviewer conditions on the team-mode lead (approved with four).**
(1) The proof is panechain.Reaches against the recorded incarnation: an
ancestor whose pid and raw start stamp both equal it, the walk bounded at
64 hops, the stamp read through procinfo; no match is not_lead or
origin_unknown as before. A recorded incarnation that is gone or whose pid
carries another stamp refuses with `lead_gone` (schedproc.ErrLeadGone,
RefusedLeadGone), and `schedproc.Run` checks it before every tick and exits
(`scheduler_stopped`, reason lead_gone) rather than serve a dead lead's
socket. A team-mode start whose parent stamp cannot be read refuses to
start. (2) The ledger path is unchanged for every other start; one
scheduler uses one form (`teamModeLead` replaces the ledger lead only for
a team-mode start). (3) Rows, each failing when reverted:
TestSupervisingLeadProvesTheTeamModeLead (the lead and the pi manager's
child, a child process dialing, accepted; a dialer outside the lead's
descent, as a roster pane's process is, not_lead; the recorded pid in the
chain with another stamp, lead_gone; the lead ended, lead_gone),
TestSchedulerExitsWhenItsTeamModeLeadIsGone, and
TestSchedulerTakesItsTeamModeParentAsTheLead (the wiring, the unreadable
stamp, no parent lead outside team mode). (4) The amendment to R4 is in
docs/v3.1-plan.md 3.4 and docs/v3-plan.md 4.5.

**Open point found on the way (not built): the team-mode pi lead has no
tool that sends the relaunch.** TeamLeadTools (internal/piruntime/launch.go:
amq_send, amq_drain, amq_reply, amq_task_list, amq_task_add,
amq_task_reset, amq_repo_read, amq_repo_list, amq_status) carries no shell
and no relaunch tool, and only a descendant of the team-mode lead process
is proved. So `amq-squad scheduler relaunch` is provable but has no caller
in team mode until a tool such as `amq_scheduler_relaunch` (internal/piruntime,
outside this brief's files) runs it. `task widen` is in the same position
for a team-mode lead. The owner's own shell is not a descendant of the lead
process and is refused (not_lead), by design.

## Round 4: the lead's two decisions as tools (reviewer ruling A)

The open point of round 3 (no caller for the relaunch in team mode) is
closed by two native pi tools, `amq_scheduler_relaunch` and
`amq_task_widen`, added to TeamLeadTools (internal/piruntime/launch.go), so
R53's set stays the one list (docs/v3.1-plan.md's R53 row names them).

- **Where they live.** The native tools are the pi package's TypeScript,
  pi/extensions/lib/tools.ts, built into the embedded payload; Go holds
  only the name list. Each tool is a thin wrapper over the CLI verb, the
  same decision-socket call path: `amq-squad scheduler relaunch --reason R
  --json` and `amq-squad task widen ID --files ... --reason R --json`. No
  second implementation of either verb.
- **The widen tool's scope.** There is no seat-side widening request with an
  id; the request is the commit step's refusal on the task. The tool reads
  the task (`amq-squad task show ID --json`), requires state blocked with
  block_reason outside_declared_files, and takes the paths that refusal
  names from its block_detail (seatcommit's `describe`: "outside_declared_files:
  a, b (declared: ...)"). Its schema is the task id, the lead's reason and an
  optional `files` that must be a subset of those paths; the default is all
  of them. A fabricated path is refused before any verb runs; a refusal that
  lists more than eight paths ("and N more") is refused rather than guessed.
  The grant's limits and the growth gate stay the verb's (CheckWiden).
- **The role.** The team lead's role text (internal/cli/pi_lead_team.md)
  names both tools at the step where a task comes back blocked; the pi
  package README lists them.

Rows: TestLeadToolVerbsAreProvedFromTheLeadsDescendant (internal/cli: the two
CLI verbs run in a child of the team-mode lead, as the tool's child of pi
does, are proved over the real socket and acted on; the same verbs from a
process that does not descend from the lead, as a roster pane's process
does not, are refused not_lead and nothing is applied; not_lead is the
decision socket's code for a readable chain that does not reach the lead,
origin_unknown is kept for an unreadable one), and in pi/test/tools.test.ts
"argv: the relaunch and the widening are the CLI verbs; the widening takes
only refused paths" and "the lead's widen tool reads the refusal and never
widens by a fabricated path". TestLeadTeamHasNoWriteTools (internal/cli/pi_test.go)
pins the list with the two names.

**Limit of 3.0.1.** A hand-started scheduler (`amq-squad scheduler run`
outside `lead --team`: the ledger path, with no lead seat) has no caller for
the relaunch or the widening: no proved lead process exists to run the
verbs. So the 3.0.1 owner run starts through `amq-squad lead --team
--scheduler-seats FILE --trust`, and the replacement leg's owner-run row is
the owner asking the manager in the pi TUI to relaunch, recorded as such
(the owner-run row above). Brief 1 decides whether a hand start ever has a
lead.

Tests run with `npm ci --offline --ignore-scripts` in pi/ (the local npm
cache, no network); node_modules is gitignored.
