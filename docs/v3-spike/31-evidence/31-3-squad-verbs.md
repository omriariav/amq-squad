# 31-3 PR 1: the squad verbs, R55's three doors, the start through up's shared functions

Brief: docs/v3-spike/briefs/brief-31-3-squad-verbs.md (copied verbatim in
this PR), PR 1 only: sections 1 to 3c, rows 1a to 1f, 3 and 6. Authority:
docs/v3.1-plan.md section 3.1 (one lead; R55 the confirmation record),
section 4's brief 1 row as lined by section 7.6, and the execution notes of
31-1 and 31-2. Branch v3-31-3, from origin/v3 `b7493715` (#832 merged;
internal/cli/up.go and pi/extensions/lib/keys.ts present). R53's switch
(section 4) and the first turn (section 5) are PR 2; their seams are named
in 2.9.

## 1. What the tip had, before any code

- **`up`'s steps were methods.** `upRun.compile`, `session`, `doctor`,
  `lead`, `tmux` and `roster` (internal/cli/up.go), with the lead lock and
  the scheduler taken by the tmux lead's `lead --team` through
  `takeLeadLock` and `startLeadScheduler` (internal/cli/lead_scheduler.go).
- **`team propose --from FILE` read a squad.yaml draft** (internal/cli/
  team.go); the digest flow is `team.NewPlan` and `Plan.Apply`
  (internal/team/digest.go).
- **The solo lead is `amq-squad lead` with no `--team`.** It sets no
  `AMQ_SQUAD_LEAD`, so pi starts with no `--tools` list: pi 1.3.0's
  built-in tools (read, bash, edit, write) plus every extension tool
  (pi's own help: `--no-builtin-tools` "keep[s] extension/custom tools
  enabled"). The `amq-squad lead` process stays pi's parent (`runProcess`,
  internal/cli/pi_exec_unix.go:21), so every tool's command descends from
  it.
- **R40's mayor library checks live in `internal/fleet`**: `Mayor.Start`
  (internal/fleet/mayor.go:292, `start`) checks, in order, the fleet grant
  (`grants.Resolved.FleetScope`), the derived repository identity against
  `permitted_repositories` (`RepositoryIdentity`), the mode, the cost cap
  (fail-closed: a cap with no meter is `cost_unmetered`), the provisioning,
  and `max_squads` under the registry lock; it then writes squad.yaml
  through a `SquadWriter` and launches through a `RosterLauncher`. The
  apply calls it, and nothing re-implements a check.
- **The scheduler accepts `--lead-pool` and proves the lead's decisions
  only from a parent whose argv is `amq-squad lead --team`**
  (`startedByLeadTeam`, `schedproc.SupervisingLead`).
- **The guard sits on every pi tool call, the lead's included**: the
  extension's `tool_call` hook runs `amq-squad guard` for every tool of
  every pi process it loads in (pi/extensions/amq-squad.ts). The brief's
  "the guard that refuses seats does not sit on the pi lead's tool" is not
  so (section 7).

## 2. The design

### 2.1 `team propose --from PLAN.json` (section 1)

The plan is a JSON object, read strictly (`team.LeadPlan`,
internal/team/plan.go): `goal`, `session`, `lead`, `seats` (handle, role,
runtime, model, effort), `files`, `tasks`. `--from` tells a plan from a
squad.yaml draft by its first character (`{`), the smallest change to the
flag. `team.RenderPlan` renders squad.yaml in canonical form; that text is
parsed against the owner grant exactly as a draft is (a widening runtime,
`max_seats`, a role neither in the catalog nor staged: refused), and planned
with the team rules and pointer stubs into one digest. The verb prints the
plan (goal, seats, files, tasks), the planned files, the digest and its
16-hex prefix, and the three doors. Its one write is the proposal, beside the
records (2.2): the Buzz door looks a prefix up there, and the apply plans
its squad.yaml again from it. docs/squad-yaml.md gains the propose-rendered
form; a row keeps its JSON and YAML pair equal to `RenderPlan`.

The pi face is `amq_team_propose` (pi/extensions/lib/team.ts), registered
for the solo lead only (`AMQ_SQUAD_SOLO_LEAD=1`, set by `runLead`'s solo
branch). It hands the plan to `team propose --from /dev/stdin --project
<repo> --json` and returns the plan, the digest and the prefix; the plan
block below the editor shows them with the confirm command.

### 2.2 R55's record (section 2)

`<root>/extensions/amq-squad/confirm/<content hex>.json` with `schema`,
`digest`, `door`, `at`, `by` and `proof`. `<root>` is the solo lead's AMQ
root for the repository (`soloHome`: `~/.amq-squad/solo/<dir>-<sha12>/root`,
the lead's `AM_ROOT` in the solo phase), so each door and the apply find it
from the repository alone. The file stem is the content part of the token
(the token holds `:` and `@`); the full token is inside and must match.
`checkConfirmation` (internal/cli/team_confirm.go) refuses by name:
`confirmation_missing` (no record, or one naming another digest),
`confirmation_stale` (older than 30 minutes, or dated more than a minute
ahead), `confirmation_unproved` (no door proof, 2.3). Every refusal names
the three doors with the prefix and the full digest.

### 2.3 The three doors and their proofs (sections 2 and 3c)

| door | how | proof in the record |
|---|---|---|
| tui | `/squad confirm <prefix>` in the lead's pane | `pane` ($TMUX_PANE) and `pid` (the extension's process) |
| shell | `amq-squad team confirm D [--project DIR]` from the owner's shell | `pid_chain`: each ancestor's pid and argv[0] base name |
| buzz | the owner's signed Buzz DM `yes <prefix>` | `event_id` (64 hex) |

- **(c) the TUI door.** `/squad confirm` is a pi command registered by the
  extension for the solo lead (`installSoloTeam`), never a tool: pi hands a
  command to the extension, never to the model (31-2's pattern). It matches
  the typed prefix against the digest the extension itself printed through
  `amq_team_propose` (held in the extension's memory), and writes the record
  from the extension's own process (temp name and rename, 0600). It runs no
  CLI verb: a verb would be one the model could also call from its shell,
  and the extension's process is the runner the brief names. Without a
  printed plan, or with another prefix, it writes nothing.
- **(a) the shell door, kept with a kernel-level refusal (3b (i)).**
  `confirmAncestry` reads only the kernel's records (procinfo): the process
  must have a controlling terminal (`/dev/tty` opens; a process the lead
  detaches with `setsid` has none: `no_terminal`); its parent chain is
  walked to pid 1 with `procinfo.ParentPID`, and refuses `lead_descendant`
  at an ancestor whose argv (`procinfo.Argv`) is an amq-squad lead or seat
  (bare `amq-squad`, `lead`, `seat-exec`, `pi`, `scheduler`) or whose
  initial environment (`procinfo.Environ`, new: darwin KERN_PROCARGS2's
  environment block, linux /proc/<pid>/environ) carries `AM_ME` (seat-exec
  sets it before it execs a provider; the lead's pi runs with it); the chain
  must pass through the process's session leader (`procinfo.SessionID`, new:
  getsid(2)), so a command orphaned out of the shell that ran it (reparented
  to pid 1) is `ancestry_unknown`, as is a parent that cannot be read. An
  ancestor whose argv cannot be read (another user's process: login, sshd)
  is passed over; the lead and its seats are the owner's own, always
  readable, and a process cannot rewrite an ancestor's records. Door (a)
  is not dropped.
- **(b) the Buzz door.** The mailbox handler (internal/buzz/inbound.go)
  dispatches an owner-signed `yes <16 hex>` to its confirmer only after the
  typed path's checks a gate answer gets: the event fetched by its id on the
  verifier's own connection, its id recomputed, the owner's pinned key, the
  DM kind, the pinned channel, the routing window; serve's copy is never
  read. The confirmer (`confirmByPrefix`, wired in `remote attach`) writes
  the record of the one proposal younger than 30 minutes whose digest starts
  with the prefix, among the solo roots; the event is then marked once-only.
  The reply names the team only (`confirmed team`, `refused team <code>`),
  since no handler reply carries an answer's digest prefix.

### 2.4 Which apply needs the record

The record binds the apply of the lead's artifacts: `team apply --digest D`
of a recorded proposal (no `--from`, `--roles` or `--add-seat`) and `team
apply --from PLAN.json`. The 3.0 flag form (`--roles`, or a squad.yaml
`--from`), which the owner types in their own shell with the files' digest in
front of them, and the screen's add key (`--add-seat`, confirmed at the
key), stay as they were; requiring the record there would change the
owner's manual flow and the add key, which the brief leaves alone ("adding
a seat afterwards is the add key and the reload, nothing new"). From a seat
or the lead's tools both are refused anyway (2.7).

### 2.5 The start in the lead's process (section 3)

The scheduler and the lead lock must live in the lead's process for its
life, and the apply runs as a short child of pi. So the solo lead serves a
private socket from its own process (`leadStarter`, internal/cli/
lead_start.go; `seatlaunch.ListenPrivate` under its AMQ root, the decision
socket's machinery) and gives its pi the path (`AMQ_SQUAD_LEAD_START_SOCKET`).
`team apply --digest D` under the solo lead sends `{digest, project}` there;
the lead's process proves the peer descends from it (the kernel's peer pid,
the parent chain walked to its own pid), and then:

1. checks the confirmation (2.2);
2. calls the mayor, `fleet.Mayor.Start`, with the owner's grant, the fleet
   registry, a `confirmedSquadWriter` (the digest flow over the repository
   the owner confirmed, and only for the confirmed digest: a plan that
   changed since the confirmation is `squad_yaml_digest_mismatch` and
   nothing is written; the mayor's canonical path is not used for the
   files, since it can spell the checkout another way) and a `leadLauncher`;
3. the launcher runs up's shared functions: `UpCompile` (the wrapper this
   binary), `UpSession`, `UpDoctor`, `UpLeadLock` (held by this process for
   its life; a held root is `lead_running` with the first lead's pid and
   pane), `UpScheduler` (`scheduler run --project` supervised in-process, as
   `lead --team` does through `leadSchedulerCommand`) and
   `UpRosterOutcome`. No install and no tmux: `up` alone does those. The
   launch budget's start (`AMQ_SQUAD_START`) is the confirmation's moment,
   as the add key's is its press. A failed step releases the lock and stops
   the scheduler; the mayor rolls squad.yaml back.

The mayor's outcomes map by name. Started: the roster lines and the
outcome. A root outside the fleet grant (`no_fleet_grant`,
`repository_not_permitted`, `repository_unidentified`) or a grant that does
not let the mayor start (`manual_mode`) is R40's fallback: refused, "the
lead proposes, the owner starts", with the owner's two commands
(`amq-squad team apply --digest D --project P` from their shell, then
`amq-squad up --project P`). Any other refusal (`cost_unmetered`,
`cost_per_day`, `max_squads`, `squad_registered`, a step's own name) is
returned by its name. A second start in the same lead is `lead_running`.

The scheduler a solo lead starts must accept the lead's pools and prove the
lead's decisions by its parent, so `startedByLeadTeam` now accepts a parent
`amq-squad lead` with or without `--team`, and a bare `amq-squad` (the solo
lead). The argument for it is unchanged: only a lead's direct child has it
as parent (the lead starts its pi and its scheduler; its tools' commands run
under pi). The widening is exactly the lead, not the binary: the parents
accepted are `amq-squad lead` with or without `--team` (and its other
flags) and bare `amq-squad`; `amq-squad up`, `seat-exec`, `scheduler run`,
`remote attach`, `pi run`, `team apply`, `leader` and every other verb as a
parent are refused (`TestASoloLeadIsALeadParentOfItsScheduler`). Bare
`amq-squad` is the solo lead and nothing else, as verified in
internal/cli/cli.go:84-88: a bare invocation (`len(args) == 0`) runs
`runBare` (internal/cli/pi_lead.go:320), which with the embedded pi and a
terminal on stdin calls `runLead(nil)`, the solo lead, and otherwise prints
the usage and a one-line hint and returns, so the process exits at once.
A long-lived bare `amq-squad` parent is therefore the solo lead.

From the owner's shell (no socket in the environment) `team apply --digest
D` checks the record, writes the files through the digest flow, and prints
the owner's start, `amq-squad up --project P`.

### 2.6 One implementation, counted (row 6)

up.go exports the steps as named functions: `UpCompile`, `UpSession`,
`UpDoctor`, `UpLeadLock`, `UpScheduler`, `UpRosterOutcome`. `up`'s methods
call the first three and the last; `lead --team` (which `up` starts in
tmux) calls `UpLeadLock` and `UpScheduler`; the start of a confirmed team
calls all six. Each counts its calls (`countShared`), and a row reads the
counter over `up`, over `lead --team` and over the start.

### 2.7 3b: the hole in the solo phase, closed where it can be and stated

- **(i)** door (a)'s kernel-level refusal, 2.3.
- **(ii) the solo lead's shell** refuses `tmux` and `amq-squad team`
  (confirm, apply, or `team` with either word) before the guard runs
  (`soloShellRefusal`, pi/extensions/lib/team.ts, called from the
  extension's `tool_call` hook when `AMQ_SQUAD_SOLO_LEAD=1`): by argv[0]'s
  base name when the call is an argv array, by token when it is a command
  string (split on blanks and shell punctuation, so `tmux` behind a pipe,
  a subshell, `sh -c`, `$(...)`, `env` or a path is seen). Every other
  command runs.
- **the guard** (`owner_only`, internal/cli/hook_guard.go) refuses `amq-squad
  team confirm` and `team apply` in a shell call from any seat or lead, and
  any write into a confirmation record directory
  (`extensions/amq-squad/confirm/`), by the Write and Edit tools or a shell
  redirect, cp, mv or tee.
- **(iii) the solo-phase limit**, the line the owner reads: *Before the
  yes the solo lead holds the owner's uid, a shell, edit and write. The
  refusals above stop the direct paths (its shell typing into its own pane
  through tmux, running the owner's verbs, writing a record with its tools,
  confirming from a process that descends from it or that it orphaned), but
  a model that finds another path, a new pty (`script`, `expect`, a
  terminal it spawns), a program it writes and runs that writes the record
  file, or a token the split does not see, can still forge a confirmation.
  That residual is bounded by the mayor's cost cap and fleet grant (no
  start outside a permitted repository, none past max_squads, none while a
  cap cannot be metered) and closes when 3.2 brings the signed door.*

### 2.8 docs

README's First run is the conversation (`amq-squad lead`, say what you want,
the plan with its digest, the three doors, the apply); the host setup steps
follow it. docs/squad-yaml.md gains the propose-rendered form.
docs/status-contract.md is unchanged: no status field changed.

### 2.9 The seams PR 2 takes

- **R53's switch.** The start's success is `leadLauncher.Launch` returning
  nil inside `leadStarter.start` (internal/cli/lead_start.go): the phase
  marker is written there. The solo tools are registered in one place,
  `installSoloTeam` (pi/extensions/lib/team.ts); after the start the lead's
  pi keeps its solo identity (`AM_ROOT` the solo root, the solo tools), and
  PR 2 switches it to `TeamLeadTools` and the squad's root. A second apply
  in the same lead is already refused (`lead_running`); after a restart the
  marker decides.
- **The first turn.** The team lead's `before_agent_start` hook
  (pi/extensions/amq-squad.ts) is where the host facts from `status --json`
  and `context --json` go.
- **3.1.1: the Buzz door reads like the other two.** The Buzz door's
  `yes <prefix>` is the form the owner has asked to retire for gates (his
  ruling of 2026-11-01: a bare yes or no replied in the question's thread,
  the signature plus the thread being the binding). When the two-way relay
  (brief 31-4) lands, the plan goes to the owner's DM as the lead's reply,
  and the confirm becomes a bare `yes` in that thread through the same
  thread binding, so the three doors read alike. Not built in this PR: the
  seam is the handler's `teamYes` dispatch (internal/buzz/inbound.go) and
  `confirmByPrefix` (internal/cli/team_confirm.go), which would take the
  proposal from the thread's root event instead of the prefix.

### 2.10 Choices where the brief left one

- `--from` tells a plan by its first character (`{`) rather than a new
  flag.
- The TUI door writes the record in the extension's process, not through a
  CLI verb (2.3).
- The record binds the lead's artifacts only (2.4).
- The start reaches the lead's process over a private socket (2.5); the
  mayor's `NeedsProvisioning` is off on this path, because up's session
  step provisions the root (`amq coop init`), so the launch API's `.amqrc`
  precondition (upstream ask 2) does not apply. The four checks the brief
  names all run.
- The Buzz door needed one dispatch in internal/buzz/inbound.go (the
  mailbox handler lives there; the brief declares internal/gateverify/, so
  the parse and the confirmer's type are in internal/gateverify/teamyes.go).

## 3. Rows

| row | test |
|---|---|
| 1a | cli `TestTeamApplyWithoutAFreshConfirmationRefusesNamingTheThreeDoors` (no record, the plan-file apply, a 31-minute record, a record without its door's proof) and `TestTheApplyReachesTheLeadOverItsSocketAndOnlyFromItsDescendants` (the same refusal from the lead over its socket) |
| 1b | cli `TestGuardRefusesTheConfirmFromASeatAndTheOwnersConfirmLetsTheApplyRun` |
| 1c | pi `team.test.ts`: "/squad confirm with the printed prefix writes the record with the pane and the pid", "a model message containing /squad confirm writes nothing; the confirm is a command, never a tool" |
| 1d | buzz `TestTheBuzzDoorConfirmsOnlyTheOwnersSignedYes` (owner-signed, another key, a broken signature, a wrong prefix, serve's copy); cli `TestBuzzDoorConfirmsTheProposalItsPrefixNames` and `TestTheOwnersSignedYesOverBuzzConfirmsTheProposal` (end to end through the test relay and `remote attach --once`) |
| 1e | cli `TestTeamConfirmRefusesALeadOrSeatAncestorAndWritesFromTheOwnersShell` (the fake process tree through the procinfo seams); procinfo `TestEnvironReadsTheInitialEnvironment` |
| 1f | pi `team.test.ts` "the solo lead's shell refuses tmux and the owner's verbs by argv[0] and by token"; the proof field of each door: tui (pi row 1c), shell (cli row 1e), buzz (cli row 1d), and `confirmation_unproved` for each door's missing proof (cli row 1a) |
| 3 | cli `TestTeamProposeFromAPlanPrintsThePlanAndDigestAndWritesOnlyTheProposal`, `TestTheStartOfAConfirmedTeamRunsUpsSharedStepsAndNoInstallOrTmux`, `TestTheStartRefusesAPlanThatChangedSinceTheConfirmation`, `TestTheMayorsChecksRefuseByNameOnTheStartsPath` (a repository outside the grant, a cost cap, no fleet grant); team `TestThePlanRendersTheDocumentedSquadYAML`; pi "amq_team_propose prints the plan and digest; amq_team_apply returns the roster line", "amq_team_apply without a confirmation carries the lead's refusal naming the doors" |
| 6 | cli `TestTheStartRefusesLeadRunningWhileATeamLeadHoldsTheRoot`, `TestUpAndTheLeadTeamCountOnTheSameSharedSteps`, `TestTheStartOfAConfirmedTeamRunsUpsSharedStepsAndNoInstallOrTmux` (the counter, no install, no tmux), `TestTheSoloLeadServesTheStartSocketToItsPi`, `TestASoloLeadIsALeadParentOfItsScheduler` |

No test reaches the network, the real HOME (TestMain's temp HOME; the pi
tests use temp roots), tmux, AMQ or Buzz (the fake amq and tmux of `up`'s
world, the in-memory relay). The one canary text in the handler table is
checked against `redact.RealCredentialShapes`; no new canary is added.

## 4. The revert runs

`python3 docs/v3-spike/31-evidence/31-3-pr1-revert-runs.py` from a clean
checkout of the branch: each run reverts one change, runs its rows, and
restores every touched file to HEAD in a finally; it refuses a dirty tree.
The output, verbatim:

```
REVERT a plan read as a squad.yaml draft (no plan branch): fails (the row catches it)
   --- FAIL: TestTeamProposeFromAPlanPrintsThePlanAndDigestAndWritesOnlyTheProposal (0.00s)
REVERT the plan's effort dropped from squad.yaml: fails (the row catches it)
   --- FAIL: TestThePlanRendersTheDocumentedSquadYAML (0.00s)
REVERT the plan read leniently (unknown keys kept quiet): fails (the row catches it)
   --- FAIL: TestThePlanRendersTheDocumentedSquadYAML (0.00s)
REVERT the proposal not recorded: fails (the row catches it)
   --- FAIL: TestTeamProposeFromAPlanPrintsThePlanAndDigestAndWritesOnlyTheProposal (0.00s)
REVERT the recorded proposal applied with no confirmation: fails (the row catches it)
   --- FAIL: TestTeamApplyWithoutAFreshConfirmationRefusesNamingTheThreeDoors (0.05s)
REVERT a plan file applied with no confirmation: fails (the row catches it)
   --- FAIL: TestTeamApplyWithoutAFreshConfirmationRefusesNamingTheThreeDoors (0.05s)
REVERT no 30-minute bound: fails (the row catches it)
   --- FAIL: TestTeamApplyWithoutAFreshConfirmationRefusesNamingTheThreeDoors (0.06s)
REVERT a record without its door's proof accepted: fails (the row catches it)
   --- FAIL: TestTeamApplyWithoutAFreshConfirmationRefusesNamingTheThreeDoors (0.07s)
REVERT the refusal naming two doors (no Buzz door): fails (the row catches it)
   --- FAIL: TestTeamApplyWithoutAFreshConfirmationRefusesNamingTheThreeDoors (0.01s)
REVERT door (a) blind to a lead or seat argv: fails (the row catches it)
   --- FAIL: TestTeamConfirmRefusesALeadOrSeatAncestorAndWritesFromTheOwnersShell (0.02s)
REVERT door (a) blind to an ancestor's AM_ME: fails (the row catches it)
   --- FAIL: TestTeamConfirmRefusesALeadOrSeatAncestorAndWritesFromTheOwnersShell (0.02s)
REVERT door (a) without a controlling terminal: fails (the row catches it)
   --- FAIL: TestTeamConfirmRefusesALeadOrSeatAncestorAndWritesFromTheOwnersShell (0.02s)
REVERT door (a) passing an orphaned chain: fails (the row catches it)
   --- FAIL: TestTeamConfirmRefusesALeadOrSeatAncestorAndWritesFromTheOwnersShell (0.02s)
REVERT the shell door's record without its pid chain (3c): fails (the row catches it)
   --- FAIL: TestTeamConfirmRefusesALeadOrSeatAncestorAndWritesFromTheOwnersShell (0.02s)
REVERT the guard letting a seat run team confirm and apply: fails (the row catches it)
   --- FAIL: TestGuardRefusesTheConfirmFromASeatAndTheOwnersConfirmLetsTheApplyRun (0.01s)
REVERT the guard letting a write into the records: fails (the row catches it)
   --- FAIL: TestGuardRefusesTheConfirmFromASeatAndTheOwnersConfirmLetsTheApplyRun (0.01s)
REVERT the Buzz door taking any proposal (prefix ignored): fails (the row catches it)
   --- FAIL: TestBuzzDoorConfirmsTheProposalItsPrefixNames (0.02s)
REVERT the Buzz door's record without its event id (3c): fails (the row catches it)
   --- FAIL: TestBuzzDoorConfirmsTheProposalItsPrefixNames (0.02s)
   --- FAIL: TestTheOwnersSignedYesOverBuzzConfirmsTheProposal (0.06s)
REVERT the handler routing the yes (no Buzz door): fails (the row catches it)
   --- FAIL: TestTheBuzzDoorConfirmsOnlyTheOwnersSignedYes (0.02s)
   --- FAIL: TestTheBuzzDoorConfirmsOnlyTheOwnersSignedYes/signed_by_the_owner (0.01s)
   --- FAIL: TestTheBuzzDoorConfirmsOnlyTheOwnersSignedYes/a_wrong_prefix (0.00s)
REVERT the yes confirmed before the owner's signature is checked: fails (the row catches it)
   --- FAIL: TestTheBuzzDoorConfirmsOnlyTheOwnersSignedYes (0.02s)
   --- FAIL: TestTheBuzzDoorConfirmsOnlyTheOwnersSignedYes/signed_by_another_key (0.00s)
   --- FAIL: TestTheBuzzDoorConfirmsOnlyTheOwnersSignedYes/a_broken_signature (0.00s)
REVERT a redelivered yes confirmed again (no once-only mark): fails (the row catches it)
   --- FAIL: TestTheBuzzDoorConfirmsOnlyTheOwnersSignedYes (0.02s)
   --- FAIL: TestTheBuzzDoorConfirmsOnlyTheOwnersSignedYes/signed_by_the_owner (0.01s)
REVERT the yes parsed loosely: fails (the row catches it)
   --- FAIL: TestTheBuzzDoorConfirmsOnlyTheOwnersSignedYes (0.02s)
REVERT remote attach's handler without the confirmer: fails (the row catches it)
   --- FAIL: TestTheOwnersSignedYesOverBuzzConfirmsTheProposal (0.79s)
REVERT the lead starting with no confirmation: fails (the row catches it)
   --- FAIL: TestTheApplyReachesTheLeadOverItsSocketAndOnlyFromItsDescendants (0.08s)
REVERT the socket taking any process (no descent check): fails (the row catches it)
   --- FAIL: TestTheApplyReachesTheLeadOverItsSocketAndOnlyFromItsDescendants (0.02s)
REVERT the lead taking another repository's request: fails (the row catches it)
   --- FAIL: TestTheStartOfAConfirmedTeamRunsUpsSharedStepsAndNoInstallOrTmux (0.07s)
REVERT the mayor's checks not applied (a grant that lets everything): fails (the row catches it)
   --- FAIL: TestTheMayorsChecksRefuseByNameOnTheStartsPath (0.23s)
   --- FAIL: TestTheMayorsChecksRefuseByNameOnTheStartsPath/a_repository_outside_the_grant (0.08s)
   --- FAIL: TestTheMayorsChecksRefuseByNameOnTheStartsPath/a_cost_cap (0.08s)
   --- FAIL: TestTheMayorsChecksRefuseByNameOnTheStartsPath/no_fleet_grant (0.07s)
REVERT no R40 fallback (the lead proposes, the owner starts): fails (the row catches it)
   --- FAIL: TestTheMayorsChecksRefuseByNameOnTheStartsPath (0.06s)
   --- FAIL: TestTheMayorsChecksRefuseByNameOnTheStartsPath/no_fleet_grant (0.02s)
   --- FAIL: TestTheMayorsChecksRefuseByNameOnTheStartsPath/a_repository_outside_the_grant (0.02s)
REVERT the start writing a digest the owner did not confirm: fails (the row catches it)
   --- FAIL: TestTheStartRefusesAPlanThatChangedSinceTheConfirmation (0.08s)
REVERT the squad files planned over the mayor's spelling of the checkout: fails (the row catches it)
   --- FAIL: TestTheStartOfAConfirmedTeamRunsUpsSharedStepsAndNoInstallOrTmux (0.04s)
REVERT the start ignoring a held lead lock: fails (the row catches it)
   --- FAIL: TestTheStartRefusesLeadRunningWhileATeamLeadHoldsTheRoot (0.07s)
REVERT the launch budget not starting at the confirmation: fails (the row catches it)
   --- FAIL: TestTheStartOfAConfirmedTeamRunsUpsSharedStepsAndNoInstallOrTmux (0.08s)
REVERT a second start in the same lead: fails (the row catches it)
   --- FAIL: TestTheStartOfAConfirmedTeamRunsUpsSharedStepsAndNoInstallOrTmux (0.08s)
REVERT the session step uncounted (up's and the apply's path not shared): fails (the row catches it)
   --- FAIL: TestTheStartOfAConfirmedTeamRunsUpsSharedStepsAndNoInstallOrTmux (0.08s)
   --- FAIL: TestUpAndTheLeadTeamCountOnTheSameSharedSteps (0.01s)
REVERT lead --team taking the lead lock outside the shared function: fails (the row catches it)
   --- FAIL: TestUpAndTheLeadTeamCountOnTheSameSharedSteps (0.05s)
REVERT lead --team starting the scheduler outside the shared function: fails (the row catches it)
   --- FAIL: TestUpAndTheLeadTeamCountOnTheSameSharedSteps (0.03s)
REVERT the solo lead not a lead parent of its scheduler: fails (the row catches it)
   --- FAIL: TestASoloLeadIsALeadParentOfItsScheduler (0.00s)
REVERT the lead check widened to the binary (any amq-squad parent): fails (the row catches it)
   --- FAIL: TestASoloLeadIsALeadParentOfItsScheduler (0.00s)
REVERT the solo lead's pi without the solo flag: fails (the row catches it)
   --- FAIL: TestTheSoloLeadServesTheStartSocketToItsPi (0.02s)
REVERT the environment read stopping at argv: fails (the row catches it)
   --- FAIL: TestEnvironReadsTheInitialEnvironment (0.00s)
REVERT the TUI door taking any prefix: fails (the row catches it)
   not ok 3 - /squad confirm with the printed prefix writes the record with the pane and the pid
REVERT the TUI door's record without the pid (3c): fails (the row catches it)
   not ok 3 - /squad confirm with the printed prefix writes the record with the pane and the pid
REVERT the shell's argv form unchecked: fails (the row catches it)
   not ok 5 - the solo lead's shell refuses tmux and the owner's verbs by argv[0] and by token
REVERT the shell's token form reading argv[0] only: fails (the row catches it)
   not ok 5 - the solo lead's shell refuses tmux and the owner's verbs by argv[0] and by token
REVERT the shell's refusal not wired into tool_call: fails (the row catches it)
   not ok 5 - the solo lead's shell refuses tmux and the owner's verbs by argv[0] and by token
REVERT propose's plan not on stdin: fails (the row catches it)
   not ok 1 - amq_team_propose prints the plan and digest; amq_team_apply returns the roster line
REVERT the team tools and /squad confirm given to the team lead too: fails (the row catches it)
   not ok 2 - the lead's model tool list carries no key verb and no gate-answer verb
47 runs, 47 caught, 0 missed
```

## 5. The owner-run rows

Run by the owner on this repository, in their own terminal, with the
installed tagged build (`up`'s install, or `scripts/build-pi-runtime.sh` at
a tag). The start runs inside the fleet grant: `~/.amq-squad/grants.yaml`
must hold a fleet grant in auto mode that permits this repository and maps
its checkout (the mayor derives the identity from `repository_paths` or
`.amq-squad/repository`, never from the checkout's git config), without a
`cost_per_day_usd` (no meter reads it yet, so a cap refuses
`cost_unmetered`):

```yaml
schema: 1
mode: auto
fleet:
  max_squads: 2
  permitted_repositories:
    - omriariav/amq-squad
  repository_paths:
    - repository: omriariav/amq-squad
      path: /Users/omri/code/amq-squad
```

Otherwise row A ends in R40's fallback, by name, with the owner's two
commands, which is itself the expected refusal.

**A. A team opened from the lead's conversation, confirmed at the TUI door.**

```sh
cd /Users/omri/code/amq-squad
amq-squad lead
```

In the lead's pi: "open a team for this repository: a pi lead and one
Claude implementer for the parser." The lead calls `amq_team_propose`; the
plan block shows the plan, `digest sha256:...@...` and `prefix <16 hex>`.
Type:

```
/squad confirm <prefix>
```

The block shows `confirmed at ... (door tui)`. Ask the lead to apply; it
calls `amq_team_apply`, which returns the steps (compile, session, doctor,
lead, scheduler, roster) and one line per seat, then `team started:
roster_launched: ...`. From another shell:

```sh
amq-squad status --project /Users/omri/code/amq-squad --json
cat ~/.amq-squad/solo/amq-squad-*/root/extensions/amq-squad/confirm/*.json
```

The roster is the plan's seats; the record has `door: tui` with the pane
and the pid. In PR 1 the roster comes back in the conversation and the plan
block; the team lead's screen and tools on the yes are R53's switch (PR 2),
or the next `amq-squad up`.

**B. The same, confirmed over Buzz.** Steps of A, with the attachment
running (`amq-squad remote attach --project /Users/omri/code/amq-squad
--session <session> --me <lead>`), and instead of `/squad confirm` the
owner's DM in the Buzz thread:

```
yes <prefix>
```

The DM's result row reads `confirmed team`; the record has `door: buzz`
and the event id; the apply then starts the team as in A.

**C. An apply refused with no record.** Propose as in A, then, before any
door, ask the lead to apply, and from the owner's shell:

```sh
amq-squad team apply --digest <digest> --project /Users/omri/code/amq-squad
```

Both refuse `confirmation_missing` naming the three doors (`/squad confirm
<prefix>`, `amq-squad team confirm <digest> --project ...`, the Buzz DM
`yes <prefix>`); squad.yaml is unchanged.

## 6. What the brief got wrong

- "The guard that refuses seats does not sit on the pi lead's tool": it
  does. The extension runs `amq-squad guard` on every tool call of every pi
  process it loads in, the solo lead's included. The guard's new
  `owner_only` rows therefore bind the solo lead's shell too, alongside the
  extension's own refusal (3b (ii)).
- "Find where R40's mayor library checks live ... and call them": they are
  `fleet.Mayor.Start` as a whole (the checks, then its own squad.yaml writer
  and roster launcher), not separable functions. The apply calls
  `Mayor.Start` with two adapters (2.5) rather than the checks one by one,
  and turns its provisioning check off on this path.
- The confirmation record as the one gate on "every" apply would change
  the owner's 3.0 flag-form apply and the add key; it binds the lead's
  artifacts (2.4).
- "The apply alone starts the scheduler in-process": the apply's own
  process is a child of pi that ends with the tool call. The process that
  holds the lead lock and supervises the scheduler for the lead's life is
  the solo lead's (`amq-squad lead`), reached over its start socket (2.5),
  and the scheduler's lead check had to accept the solo lead as a lead
  parent.
- The Buzz door's dispatch is in internal/buzz/inbound.go, outside the
  declared files (2.10).

## 7. One line for the 3.1 release notes

The lead opens the team from the conversation: `amq-squad lead`, say what
you want, and it proposes a plan with one digest; you confirm at your pane
(`/squad confirm <prefix>`), your shell (`amq-squad team confirm`) or Buzz
(`yes <prefix>`), and the lead starts the team through `up`'s own steps,
inside your fleet grant.
