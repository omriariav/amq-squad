# Brief 31-1, PR 2: `amq-squad up`, the status additions, the launch budget, the screen

Branch `v3-31-1b`, from PR 1's head (`v3-31-1` at `ad97f0ab`, PR #828,
merged into v3 as `e5660330` while this PR was built; nothing to rebase). Authority: docs/v3.1-plan.md section 7 and
docs/v3-spike/briefs/brief-31-1-launch-and-screen.md sections 3, 5, 6, 7
and item 7 of section 1; PR 1's note (31-1-launch-and-screen.md), section 5
"Seams for PR 2". This PR is the brief's second half: rows 7, 13, 14, 15,
16, 18 (`up`'s part), 19 and 20.

## 1. The design

### 1.1 The two marks and their sources

| Mark | Source | Written by |
| --- | --- | --- |
| start | the seat's own `marks.json` `start` (beside its staged record), the squad launch's start, from `AMQ_SQUAD_START` | `up` sets `AMQ_SQUAD_START` to its own start (`tmux new-session -e`); `lead --team` keeps it (PR 1's `leadStartEnv`); the scheduler records it in `launch.json` and hands it to each launch (`seatlaunch.Options.Start`), which writes it into each seat's marks |
| seat live | the accepted proof's stamp, `proof.json`'s `at` | the launcher, when it accepts seat-exec's proof (internal/seatlaunch/proof_unix.go:406) |
| first message | the seat's own `marks.json` `first_message`: its first sent AMQ message since its launch request, the message's own `created` | the scheduler, once per launch: `seatlaunch.RecordFirstMessage`, as it logs its new `seat_first_message` decision |

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
  `created` stamp (the decision's own `at` is the tick's), and the stamp is
  recorded in the seat's `marks.json`, which status reads, so the mark
  survives the decisions log scrolling past its tail read. An adopted seat
  (no launch of this incarnation) gets no new mark.
- **Each seat's marks are its own launch's** (Codex P2 6, 7). A seat
  adopted by a restarted scheduler keeps the start of the launch that
  started it, not the later scheduler's; a seat with no marks of its
  current launch (launched before them, or by a scheduler started by hand),
  or whose proof predates its recorded start, has null timing and no
  ceiling check.
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
- **Redaction** (Codex P2 10). The document feeds the screen and the
  lead's model (`amq_status`), so every free-text field passes
  `redact.Block` before it is serialized: seat message bodies (before they
  are cut), warnings, AMQ's wake audit reasons, task titles and evidence,
  attention subjects, decision reasons, the train's refusals and alert
  details.

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
`--json` prints each step's name and outcome (the names carry no numbers).
squad.yaml compiles before anything is installed or created, and the AMQ
root and session exist before doctor runs (Codex P2 4, 5; section 5).

1. **compile**: `squadOfProject` (team.LoadSquad, Squad.LaunchSeats, the
   roles) and `Normalize`, the wrapper the install target. A squad.yaml
   that does not compile refuses `squad_invalid` and leaves nothing behind.
2. **install**: the source is `--binary PATH`, else
   `<repo>/build/pi/amq-squad` (build-pi-runtime.sh's `OUT`). Its `version`
   must name a tag exactly (`vX.Y.Z[-pre]`, never `-N-g<sha>` or `-dirty`):
   `pi_binary_untagged`. Its `pi path --json` (which unpacks the embedded
   runtime when absent and re-hashes it, as the lead's first start would)
   must exit 0 with its envelope naming the runtime: ErrNotEmbedded's text
   refuses `pi_not_embedded`, any other non-zero exit or unparseable reply
   `pi_runtime_rejected` (Codex P2 9; `pi verify` exits non-zero by design
   for a runtime not unpacked yet, so its exit could not be the rule). Then
   it is copied to an exclusive temporary name beside the `amq-squad` on
   PATH (or `--to PATH`) and renamed over it, never written in place.
   `--skip-install` keeps the installed binary when it reports a tag. Also
   `pi_binary_missing`, `install_target_unknown`, `install_failed`.
3. **session**: `ensureSquadSession` reads the AMQ root and the session root
   with Lstat and refuses a symlinked one, `amq_root_symlinked` (Codex P2
   8); it runs `amq coop init --root .agent-mail --agents <handles>
   --no-gitignore` in the repository when the root has no
   `meta/config.json`, and `amq session create <session> --root <base
   root>` when the session root is absent, through the injected runner; a
   `session_created` decision, whose failed append refuses
   `decision_unrecorded`. The same function serves `scheduler run` (item 7).
4. **doctor**: the installed binary's `doctor --json --project <repo>`, in
   the root step 3 made; a failing row refuses with the row's kind (else
   `doctor_failed`) and its name and detail. Deleted guards left no rows
   (PR 1).
5. **lead**, under the one rule, and only on what up can prove (Codex P1
   1, 2, 3):
   - One up per root: `up.lock` beside `lead.lock`, held from this step to
     the roster's outcome; a second up refuses `up_running`.
   - The lead lock is held across each decision and its stop: taken when
     free; when held, the recorded lead is stopped first and the lock then
     taken. `lead.json` is re-read before every signal.
   - A held lead lock: no readable record is `lead_running` ("starting");
     a record with no pane (a lead outside tmux) is `lead_running` and is
     never stopped, since its pane cannot be gone. Otherwise tmux is asked,
     `tmux list-panes -a -F '#{session_name}\t#{pane_id}'`: the pane listed
     is `lead_running` with the pid, the pane and `tmux attach -t <pane>`;
     tmux that cannot be asked (no tmux, no server, a wrong socket, an
     unparseable reply) is `tmux_unavailable`, naming the probe failure;
     an answer that lists neither the pane nor the lead's `squad-lead-*`
     session is `lead_pane_unknown`. Those three stop nothing. Only an
     answer that lists the lead's session and not its pane is "gone": the
     recorded pid is stopped by its recorded start stamp (SIGTERM, then
     SIGKILL only if the same identity still holds after the grace) and
     its leftover session killed.
   - A free lead lock with the lead's tmux session present is a dead
     lead's leftover, killed only when `lead.json` is readable; else
     `lead_record_unreadable`, nothing killed.
   - A scheduler still holding `run.lock` whose lead check (`LeadState` on
     lead.json) reports gone is stopped only as its recorded identity: the
     pid, its raw start stamp (launch.json's new `process_start`) and this
     project in its `scheduler run --project` argv, rechecked before each
     signal; SIGKILL only if that identity still holds after SIGTERM's
     grace. No readable lead record is `lead_record_unreadable`; a lead not
     gone is `scheduler_running` (ErrRunning); another identity is
     `scheduler_unknown`.
   - Each stop is an `up_stopped_lead` or `up_stopped_scheduler` decision; a
     decision that cannot be recorded refuses `decision_unrecorded` before
     the lead starts.
6. **tmux**: the lead lock is dropped (the new lead takes it; `up.lock` is
   kept), then `tmux new-session -d -s squad-lead-<repo<=30>-<sha5> -c
   <repo> -e AMQ_SQUAD_START=<up's start> -e AM_ME= ... <installed> lead
   --team`: the session name never matches AMQ's `amq-*`; the argv[0]
   basename is `amq-squad`, so the scheduler's lead-team parent check
   holds; the AM_* pins are emptied so a stale tmux-server environment
   cannot disagree with squad.yaml.
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

**What a refusal leaves behind** (Codex P2 5):

| Step refused | Left behind |
| --- | --- |
| compile | nothing |
| install | nothing (a temporary file is removed; the installed binary is untouched) |
| session | an installed tagged binary (idempotent, harmless); possibly an AMQ root from `amq coop init` when the session create failed (idempotent, harmless) |
| doctor | the installed tagged binary, the AMQ root and session (idempotent, harmless) |
| lead | the above; any stop already done is recorded as its decision |
| tmux | the above; no lead |
| roster | the lead running in its tmux session for the owner to attach to: the refusal names the session and the attach command (`roster_wait_timeout`, `roster_launch_failed`, a scheduler refusal by name) |

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
| 7 | cli `TestUpOnAProjectWithNoAMQRootCreatesItAndLaunches` (the real ordering: its fake doctor fails without an AMQ root, as AMQ's `amq env` does) |
| 13 | cli `TestUpRunsItsSevenStepsInOrderAndInstallsByRename` (the order, the inode check, the session name, the start stamp), `TestUpStopsADeadLeadsSessionAndItsScheduler`, `TestUpStopsALeadWhosePaneIsGone`, `TestUpInstallRefusesAnUntaggedOrUnembeddedBuildByName` (with `pi path`'s non-zero exit and unparseable reply), `TestUpDoctorRefusesAFailingRowByName`, `TestUpCompilesSquadYAMLBeforeInstallingOrCreatingAnything` |
| 14 | status `TestLaunchBudgetFromFixtureRecords`, `TestEachSeatsMarksAreItsOwnLaunchs` (an adopted seat, a seat without marks, a proof before its start, a first message past the decisions tail); scheduler `TestFirstMessageOfALaunchedSeatIsOneDecision`, `TestOutboxFirstMessageReadsTheSeatsSentMessages`; seatlaunch `TestLaunchRecordsAWakeAMQAdmittedDifferently` (marks.json's start), `TestRecordFirstMessageIsOncePerLaunch` |
| 15 | status `TestStatusAdditionsAreInTheGoldenAndSchema1ReadersReadIt` (against `testdata/status-v1.pre31.golden.json`, the fixture before this PR), `TestGoldenDecodesStrictly`, `TestGoldenRoundTripAdditiveBothWays`, `TestStatusCarriesTheScreensInputs`; seatlaunch `TestLaunchRecordsAWakeAMQAdmittedDifferently` (wake.json); pi `the poll runs status --json only after .generation or scheduler.json changed` |
| 16 | pi `the screen renders from a status fixture …`, `/squad toggles the widget …`, `a seat that is not the team lead gets no screen`; status `TestThePiScreensFixtureIsAStatusDocument` (the screen's fixture decodes strictly into this build's status) |
| 18 | cli `TestUpAgainstALiveLeadRefusesLeadRunningAndTouchesNothing`, `TestUpStopsALeadOnlyWhenTmuxSaysItsPaneIsGone` (tmux_unavailable for no server and an unparseable reply, lead_pane_unknown, lead_running: none stops anything), `TestUpKillsNoSessionWithoutTheLeadRecord`, `TestTwoUpsRacingOneProceedsAndNoLiveLeadDies`, `TestUpStopsASchedulerOnlyAsItsRecordedIdentity` (another project in its argv, a reused pid, an identity changed during SIGTERM's grace: no SIGKILL, a stop decision that cannot be recorded); cli `TestSchedulerRecordsTheLaunchStart` (launch.json's `process_start`) |
| 19 | status `TestStalledSeatAndKeylessPiSeatAreWarningsNotRefusals`; pi `a seat stalled at a dialog and a keyless pi seat are screen lines …` |
| 20 | cli `TestSchedulerRunOnAnAbsentSessionRootCreatesIt` |
| symlinked roots | cli `TestUpRefusesASymlinkedAMQRootOrSession` |
| redaction | status `TestStatusRedactsEveryFreeTextField` |
| day 3: a file-lock wait names its holders | status `TestAFileLockWaitNamesItsHolders` (`wait_holders`); pi `a pending task waiting on a file lock names its holder tasks` |

Every test fakes AMQ (the `squadRun` seam, the scheduler's launcher seam),
tmux (the same seam) and process signals (`upSignal`); none reaches the
network, the real HOME, the real tmux server or the real AMQ.

## 4. The owner-run row: the day-4 launch budget

From the owner's terminal. First the build at the tag (R54: never a build
of the working tree); the binary is `~/scratch-v31/build/pi/amq-squad`:

```sh
git worktree add ~/scratch-v31 v3.1.0 && cd ~/scratch-v31 && scripts/build-pi-runtime.sh all
```

Then `up` from the repository, on its squad.yaml (it installs that build
over the `amq-squad` on PATH by temp name and rename):

```sh
cd ~/code/amq-squad
amq-squad up --binary ~/scratch-v31/build/pi/amq-squad
```

**Where the per-seat numbers appear.** `up` prints each seat's line when
the roster's outcome is in (`seat <handle> <runtime>/<model> <state>
<outcome> seat live <s>, first message <s>`); the screen below the lead's
editor shows them on each seat line (`live <s>`, `first-msg <s>`, `OVER`
past the ceiling, the warning naming the slowest segment); and status
carries them per seat in `launch.timing`:

```sh
amq-squad status --json | jq '[.seats[] | {handle, runtime, model, outcome: .launch.outcome, to_seat_live_ms: .launch.timing.to_seat_live_ms, to_first_message_ms: .launch.timing.to_first_message_ms, slowest: .launch.timing.slowest_segment, segments: .launch.timing.segments, warnings}]'
```

The first message lands after seat live (the first turn), so read status
again a minute or two after `up` returns. Compare with PR 1's baseline
(31-1-launch-and-screen.md 1.1, from the scheduler's start, 2026-10-31,
launch `0de97c89…`):

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

- **Day 3, two findings from the squad's record.** t26 sat four hours
  pending on a file lock (wait reason `file_lock`) held, by the record, by
  t20 and t23, already cancelled, and nothing showed it. Status now carries
  each file-lock wait's holders (`wait_holders`, by the store's lock rule)
  and the screen's task line reads "waiting on t20, t23"; a lock no task
  holds any more reads "waiting on a file lock no task holds (stale)". By
  the store's rule a cancelled task holds no lock, so if t20 and t23 were
  cancelled, t26's wait reason was stale, and that is the line it shows.
  And t26's `pin_unresolved` block tonight ("the entry is only on tiers the
  class may not run on") is the first real failure on record behind #828's
  roster-pool admission.

- **Declared files.** Everything is inside the brief's list. Two small
  additions inside listed directories that the list's parentheses did not
  name: internal/schedproc/decisions.go also appends a decision
  (`AppendDecision`, for `session_created` and `up`'s stops, written by
  processes that are not the scheduler), and internal/seatlaunch writes
  `wake.json` (the `SeatState.Wake` exposure) and `marks.json` (the
  per-seat marks), and schedproc's launch record carries the scheduler's
  `process_start`. internal/launch,
  internal/mergetrain, internal/piruntime (beyond refusal.go),
  internal/cli/pi_lead.go and lead_scheduler.go are unchanged: PR 1's seams
  were enough.
- **The ceiling counts from `up`'s own start, and stays there** (the
  reviewer's ruling). The owner's wait runs from his command, and the
  install and the doctor are part of it, so the 10 s ceiling includes the
  installed binary's doctor (its host rows run `--version` and login probes
  of every tool). If the day-4 run shows the doctor eating the budget, the
  fix is a faster doctor (its host probes off the launch path), not a later
  mark.
- **The brief's order put doctor before the AMQ root; it runs after it
  now.** The brief ordered install, doctor, root and session, compile. On a
  fresh clone doctor's `amq ops` row runs `amq env` and `amq doctor --ops`
  in a repository with no AMQ root, and AMQ v0.116.2's `amq env` refuses a
  repository without `.amqrc` or `.agent-mail` (env.go:428), so `up` would
  have refused every fresh clone at doctor. The order is now compile,
  install, session, doctor (Codex P2 4, 5): compile first, so a squad.yaml
  that does not compile installs and creates nothing; the root and session
  before doctor. Row 7's fake doctor fails without a root, so the row runs
  the real ordering.
- **A live lead with no recorded pane is refused and never stopped.** A
  lead started outside tmux holds the lead lock with no pane in lead.json;
  `up` refuses `lead_running` and stops nothing, because a pane that was
  never recorded cannot be "gone".
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
