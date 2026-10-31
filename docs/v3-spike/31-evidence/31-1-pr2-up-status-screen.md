# Brief 31-1, PR 2: `amq-squad up`, the status additions, the launch budget, the screen

Branch `v3-31-1b`, stacked on PR 1 (`v3-31-1` at `ad97f0ab`, PR #828, not
yet merged into v3). Authority: docs/v3.1-plan.md section 7 and
docs/v3-spike/briefs/brief-31-1-launch-and-screen.md sections 3, 5, 6, 7
and item 7 of section 1; PR 1's note (31-1-launch-and-screen.md), section 5
"Seams for PR 2". This PR is the brief's second half: rows 7, 13, 14, 15,
16, 18 (`up`'s part), 19 and 20.

## 1. The design

### 1.1 The two marks and their sources

| Mark | Source | Written by |
| --- | --- | --- |
| start | `launch.json`'s `start` (`schedproc.LaunchRecord.Start`), from `AMQ_SQUAD_START` | `up` sets `AMQ_SQUAD_START` to its own start (`tmux new-session -e`); `lead --team` keeps it (PR 1's `leadStartEnv`), the scheduler records it |
| seat live | the accepted proof's stamp, `proof.json`'s `at` | the launcher, when it accepts seat-exec's proof (internal/seatlaunch/proof_unix.go:406) |
| first message | the seat's first sent AMQ message since its launch request, the message's own `created` | the scheduler's new `seat_first_message` decision |

- **seat live is read, not recorded again.** No new decision kind and no
  change to `seat_up`: the `seat_up` decision's stamp is the tick's, not
  the proof's, and status reads `proof.json` (the record `seat_up` would
  have to copy) for the seat's current launch only (its `launch_id` must be
  the staged record's).
- **`seat_first_message`** (internal/scheduler/firstmessage.go): each tick,
  every seat this incarnation launched that has no first message yet is
  asked through `Config.FirstMessage` (wired in `scheduler run` to
  `scheduler.OutboxFirstMessage(root)`: the seat's
  `agents/<seat>/outbox/sent/*.md`, files older than the launch request
  skipped unread). It is logged once per launch, its reason the message's
  `created` stamp (the decision's own `at` is the tick's). An adopted seat
  (no launch of this incarnation) gets no mark.
- **The segments up to seat live** reuse baseline.py's definitions:
  `prepare` (the start to the staged record's `started_at`, compile),
  `mint` (to the proof ticket's `issued_at`), `apply` (to AMQ's apply
  outcome, `apply.at`), `seat_exec` (to the accepted proof). The brief's
  four names were "prepare, apply, seat-exec, proof"; the records have no
  stamp between seat-exec's start and its proof, so seat-exec and proof are
  one segment, and the baseline's mint is kept as its own. With the start
  now `up`'s own, `prepare` covers everything before compile: `up`'s
  install, doctor and session steps, the lead's start, the scheduler's
  spawn and admission.
- **The ceiling** (10 s, `status.LaunchCeiling`) binds the start to seat
  live. A seat over it carries `launch_budget_exceeded: <s> from the start
  to seat live, over the 10s ceiling; slowest segment <name> <s>`. The
  first message is a second column with no ceiling.
- Both marks, the segments, the ceiling and the slowest segment are in
  status (`seats[].launch.timing`), on the screen's seat line and in `up`'s
  roster print.

### 1.2 Status (internal/status, docs/status-contract.md)

Schema 1, additive only (`testdata/status-v1.golden.json` grew; the fleet
golden was regenerated from it, its squad with `lead_handle` null). The read
stays lock-free: the lead's liveness is its pid with its recorded start
stamp (never the lock), the scheduler's its pid; every record is read, never
locked.

- the document: `lead` (lead.json: handle, pid, raw start stamp, pane,
  recorded_at, live, attach), `decisions[]` (the last 30 lines of
  decisions.jsonl, `exit_refused` among them), `train` (the audit log's last
  two runs and the owner alerts), `scheduler.pid`, `started_at`,
  `launch_start`, `live`;
- a seat: `worktree`, `branch`, `launch` (launch id, outcome
  staged/applied/apply_failed/exec_refused/proof_expired/live, reason code,
  AMQ's admitted wake, the timing);
- a task: `evidence`, `pr_repository`, `pr_number`, `pr_head`;
- the seat warnings: PR 1's three, and `launch_budget_exceeded`, `wake_off`
  (AMQ's audit reason: a Codex seat without the dialog hook; a pi seat,
  compiled wake-off even when AMQ's preview echoes nothing),
  `seat_stalled_at_dialog` (AMQ's `.wake.dialog` marker in the seat's
  mailbox), `pi_provider_key_missing` (a pi `PROVIDER/ID` model whose
  provider's key is in neither the record's env or credential needs, which
  seat-exec keeps through its scrub, nor the seat's pi login, auth.json,
  whose values are never read out).
- **The admitted wake is now on disk** (`SeatState.Wake` reached only
  Launch's caller): seatlaunch writes `wake.json` beside the staged record,
  with the launch id, after staging; status reads it for the current launch.
- `amq_status`'s description (pi/extensions/lib/tools.ts) now names what the
  document carries.

### 1.3 `amq-squad up` (internal/cli/up.go)

Seven steps, in order, each refusing by name and nothing after it running;
`--json` prints each step's outcome.

1. **install**: the source is `--binary PATH`, else
   `<repo>/build/pi/amq-squad` (build-pi-runtime.sh's `OUT`). Its `version`
   must name a tag exactly (`vX.Y.Z[-pre]`, never `-N-g<sha>` or `-dirty`):
   `pi_binary_untagged`. Its `pi verify --json` must name neither
   `pi_not_embedded` (ErrNotEmbedded) nor `pi_runtime_rejected` (a
   `*Reject`), read through `piruntime.RefusalCodeText` (an unpacked-yet
   runtime is fine). Then it is copied to a temporary name beside the
   `amq-squad` on PATH (or `--to PATH`) and renamed over it, never written
   in place. `--skip-install` keeps the installed binary when it reports a
   tag. Also `pi_binary_missing`, `install_target_unknown`, `install_failed`.
2. **doctor**: the installed binary's `doctor --json --project <repo>`; a
   failing row refuses with the row's kind (else `doctor_failed`) and its
   name and detail. Deleted guards left no rows (PR 1).
3. **session**: squad.yaml's session; `ensureSquadSession` runs `amq coop
   init --root .agent-mail --agents <handles> --no-gitignore` in the
   repository when the AMQ root has no `meta/config.json`, and `amq session
   create <session> --root <base root>` when the session root is absent,
   through the injected runner; a `session_created` decision. The same
   function serves `scheduler run` (item 7).
4. **compile**: `squadOfProject` (team.LoadSquad, Squad.LaunchSeats, the
   roles) and `Normalize` with the installed binary as the wrapper.
5. **lead**, under the one rule: the lead lock (`flock.TryExclusive` on
   `lead.lock`, `lockHeld` in up.go) held and `lead.json`'s pane present
   (`tmux display-message -p -t <pane>`) is refused `lead_running` with the
   pid, the pane and `tmux attach -t <pane>`, and nothing is touched. A held
   lock whose pane is gone is the recorded pid, checked by its start stamp,
   stopped (SIGTERM, then SIGKILL, until the lock frees). A free lock with
   the lead's tmux session still present is a dead lead's leftover, killed.
   A scheduler still holding `run.lock` whose lead check (`LeadState` on
   lead.json) reports gone, and whose pid runs `scheduler run`, is stopped
   the same way. Each stop is an `up_stopped_lead` or
   `up_stopped_scheduler` decision. A scheduler with no recorded lead, or
   whose lead is not gone, refuses `scheduler_running` (ErrRunning).
6. **tmux**: `tmux new-session -d -s squad-lead-<repo<=30>-<sha5> -c <repo>
   -e AMQ_SQUAD_START=<up's start> -e AM_ME= ... <installed> lead --team`:
   the session name never matches AMQ's `amq-*`; the argv[0] basename is
   `amq-squad`, so the scheduler's lead-team parent check holds; the AM_*
   pins are emptied so a stale tmux-server environment cannot disagree with
   squad.yaml.
7. **roster**: decisions.jsonl from its size before step 6 until
   `roster_launched`, `roster_adopted`, `roster_incomplete` (success) or
   `roster_launch_failed` (a refusal), at most `--wait` (120 s). A
   `scheduler_stopped` with a reason, the scheduler log's new text, and the
   lead's tmux session ending stop the wait by name: `lead_running`,
   `scheduler_running` (ErrRunning), `lead_gone`, `scheduler_starting`,
   `seat_replacement_resource_present`,
   `binding_present_without_resumable_conversation`,
   `seat_replacement_needs_relaunch`, `am_identity_disagrees` (the AM_*
   disagreements), `tmux_session_exists` (AMQ's "tmux session ... already
   exists"); else `lead_exited` or `roster_wait_timeout`. Then the roster
   from `status` and the attach command.

### 1.4 The screen (pi/extensions/lib/screen.ts)

In the lead's own pi (the team lead only, `AMQ_SQUAD_LEAD=1`), a widget
**below the editor** (`ctx.ui.setWidget(key, lines, {placement:
"belowEditor"})`). Layout:

```
amq-squad: lead cto (pid 4300, pane %3) · root /…/.agent-mail/v31
seats (3)
  qa  codex/gpt-5.6-sol  online/idle  live  live 12.4s OVER first-msg 57.4s
    ! launch_budget_exceeded: … slowest segment seat_exec 8.059 s
  refusal: exit_refused: old-dev ledger_tampered at gone
tasks (2)
  t1  blocked  -  PR -  outside_declared_files
  t2  in_progress  scribe  PR #812
  refusal: commit_refused: t1 outside_declared_files: src/x.go
train
  current: r2 refusal 13m ago, PRs #811,#812, merged 1
  last: r1 merge 1h ago, PRs #810, merged 1
    ! ungated_tag: tag v1.2.0 has no accepted gate
  refusal: refusal PR #812 checks: a required check failed
gates (1 open)
  g1  tag  8502aa001122  age 6m
  refusal: none
```

- **Refusal lines.** seats: the newest decision of `roster_launch_failed`,
  `seat_not_up`, `seat_launch_failed`, `seat_prepare_failed`,
  `seat_request_failed`, `seat_refused`, `exit_refused`,
  `seat_close_failed`, `ledger_tampered`, or a `scheduler_stopped` with a
  reason; tasks: the newest `commit_refused` or `blocked`, else the newest
  blocked task's reason; train: the current (else last) run's refusal or
  stop; gates: the newest attention item about a gate that is not the gate
  itself (a hung verification), else a gate-store warning. A warning is
  never a refusal (row 19).
- **The poll.** Every second the screen stats
  `tasks/.generation` and `scheduler.json` (mtime and size); `status --root
  <root> --json` runs only when that key changed (the scheduler rewrites
  scheduler.json every 2 s tick, so a running squad refreshes at the tick).
  No other poll.
- **`/squad`** (`registerCommand`) toggles it; collapsed, the widget is
  removed and one footer line of counts is set with `setStatus` (the
  footer's status area; `setFooter` would replace pi's own footer).
- Read-only: no key or command changes state.

## 2. Item 7: the missing-session refusal

Deleted: scheduler_run.go's "session root ... does not exist; create the
session first" and seatlaunch `Stage`'s same check. `scheduler run` on an
absent root or session now creates it with `ensureSquadSession` (the
function `up` uses) and logs `session_created`
(`schedproc.AppendDecision`). No test pinned either refusal.

## 3. Rows

| Row | Test |
| --- | --- |
| 7 | cli `TestUpOnAProjectWithNoAMQRootCreatesItAndLaunches` |
| 13 | cli `TestUpRunsItsSevenStepsInOrderAndInstallsByRename` (the order, the inode check, the session name, the start stamp), `TestUpStopsADeadLeadsSessionAndItsScheduler`, `TestUpStopsALeadWhosePaneIsGone`, `TestUpInstallRefusesAnUntaggedOrUnembeddedBuildByName` |
| 14 | status `TestLaunchBudgetFromFixtureRecords`; scheduler `TestFirstMessageOfALaunchedSeatIsOneDecision`, `TestOutboxFirstMessageReadsTheSeatsSentMessages` |
| 15 | status `TestStatusAdditionsAreInTheGoldenAndSchema1ReadersReadIt` (against `testdata/status-v1.pre31.golden.json`, the fixture before this PR), `TestGoldenDecodesStrictly`, `TestGoldenRoundTripAdditiveBothWays`, `TestStatusCarriesTheScreensInputs`; seatlaunch `TestLaunchRecordsAWakeAMQAdmittedDifferently` (wake.json); pi `the poll runs status --json only after .generation or scheduler.json changed` |
| 16 | pi `the screen renders from a status fixture …`, `/squad toggles the widget …`, `a seat that is not the team lead gets no screen`; status `TestThePiScreensFixtureIsAStatusDocument` (the screen's fixture decodes strictly into this build's status) |
| 18 | cli `TestUpAgainstALiveLeadRefusesLeadRunningAndTouchesNothing` |
| 19 | status `TestStalledSeatAndKeylessPiSeatAreWarningsNotRefusals`; pi `a seat stalled at a dialog and a keyless pi seat are screen lines …` |
| 20 | cli `TestSchedulerRunOnAnAbsentSessionRootCreatesIt` |

Every test fakes AMQ (the `squadRun` seam, the scheduler's launcher seam),
tmux (the same seam) and process signals (`upSignal`); none reaches the
network, the real HOME, the real tmux server or the real AMQ.

## 4. The owner-run row: the day-4 launch budget

From the owner's terminal, in this repository (its squad.yaml), with the
pi build of the 3.1 tag (R54: never a build of the working tree; the build
is made in a checkout of the tag and handed to `up` by `--binary`):

```sh
git -C ~/code/amq-squad worktree add /tmp/amq-squad-v3.1.0 v3.1.0
(cd /tmp/amq-squad-v3.1.0 && scripts/build-pi-runtime.sh all)
cd ~/code/amq-squad
amq-squad up --binary /tmp/amq-squad-v3.1.0/build/pi/amq-squad --json | tee /tmp/up-day4.json
amq-squad status --json | jq '[.seats[] | {handle, runtime, model, outcome: .launch.outcome, to_seat_live_ms: .launch.timing.to_seat_live_ms, to_first_message_ms: .launch.timing.to_first_message_ms, slowest: .launch.timing.slowest_segment, segments: .launch.timing.segments, warnings}]'
```

(`up` installs that build over the `amq-squad` on PATH by temp name and
rename; on a re-run with the same build, `amq-squad up --skip-install`.) Set the numbers beside PR 1's baseline (from the
scheduler's start, 2026-10-31, launch `0de97c89…`):

| Seat | Runtime and model | Baseline to seat live | Day 4 to seat live | Day 4 to first message | Slowest segment |
| --- | --- | --- | --- | --- | --- |
| dev | codex gpt-5.6-terra | 4.485 s | | | |
| reviewer | codex gpt-5.6-sol | 4.419 s | | | |
| scribe | claude sonnet | 4.666 s | | | |

The baseline ran from `scheduler_started`; day 4 runs from `up`'s own start,
so its `prepare` adds `up`'s install, doctor and session steps, the lead's
start and the scheduler's spawn. The checked-in squad.yaml's seats are cto
(the lead), senior-dev, qa and scribe; the rows follow the roster that runs.

## 5. Scope notes and where the brief and the code disagreed

- **Declared files.** Everything is inside the brief's list. Two small
  additions inside listed directories that the list's parentheses did not
  name: internal/schedproc/decisions.go also appends a decision
  (`AppendDecision`, for `session_created` and `up`'s stops, written by
  processes that are not the scheduler), and internal/seatlaunch writes
  `wake.json` (the `SeatState.Wake` exposure). internal/launch,
  internal/mergetrain, internal/piruntime (beyond refusal.go),
  internal/cli/pi_lead.go and lead_scheduler.go are unchanged: PR 1's seams
  were enough.
- **The start includes doctor.** `up`'s start is its own process start, so
  the 10 s ceiling includes the installed binary's doctor (its host rows run
  `--version` and login probes of every tool). If the day-4 run shows
  `prepare` over the ceiling from doctor alone, the owner may move the
  stamp to the start of step 5 or 6 (one line in up.go); the brief names
  `up` as the start, so this PR keeps it there.
- **Doctor before the session.** The brief orders doctor (2) before the AMQ
  root (3). On a repository with no AMQ root, doctor's `amq ops` row runs
  `amq env` and `amq doctor --ops` there; whether those fail without a root
  is not readable offline (the tests fake doctor). If they do, the day-4
  run on a fresh clone refuses at step 2 naming the row, and steps 2 and 3
  swap.
- **A lead outside tmux** (no pane recorded) holding the lock is refused
  `lead_running` and never stopped: its pane cannot be "gone".
- **The tmux session of a lead started by hand** (outside `up`) is never
  killed by `up`: only the `squad-lead-…` session `up` names.
- **`seat_live` has no decision** (1.1); `seat_first_message` is the one new
  decision kind of the scheduler, and `session_created`, `up_stopped_lead`,
  `up_stopped_scheduler` are written by `scheduler run` and `up`.
- **The commit trailer** names the model this session ran on (Opus 5.5).

## 6. A line for the 3.1 release notes

- `amq-squad up` starts a repository's squad from squad.yaml in one
  command: it installs the tagged pi build by temp name and rename, runs
  its doctor, creates the AMQ root and session, refuses `lead_running`
  against a live lead (and stops only a dead one), starts the lead in its
  own tmux session and prints the roster with each seat's launch budget.
  The lead's pi shows the squad screen below the editor (`/squad` toggles
  it); status carries each seat's launch outcome, budget and wake, the lead,
  the scheduler, the decisions tail and the merge train.
