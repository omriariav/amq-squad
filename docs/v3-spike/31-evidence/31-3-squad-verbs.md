# 31-3 PR 1: the squad verbs, R55's two doors, the start through up's shared functions

Brief: docs/v3-spike/briefs/brief-31-3-squad-verbs.md (copied verbatim in
this PR), PR 1 only: sections 1 to 3c, rows 1a to 1f, 3 and 6. Authority:
docs/v3.1-plan.md section 3.1 (one lead; R55 the confirmation),
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
16-hex prefix, and the two doors. Its one write is the proposal, beside the
records (2.2): the Buzz door looks a prefix up there, and the apply plans
its squad.yaml again from it. docs/squad-yaml.md gains the propose-rendered
form; a row keeps its JSON and YAML pair equal to `RenderPlan`.

The pi face is `amq_team_propose` (pi/extensions/lib/team.ts), registered
for the solo lead only (`AMQ_SQUAD_SOLO_LEAD=1`, set by `runLead`'s solo
branch). It hands the plan to `team propose --from /dev/stdin --project
<repo> --json` and returns the plan, the digest and the prefix; the plan
block below the editor shows them with the confirm command.

### 2.2 R55's confirmation: witnessed by the lead, no file is authority (section 2)

The plan reviewer's ruling on Codex's P1 on #834: the first form of this PR
made a file any process of the owner's uid can write
(`extensions/amq-squad/confirm/<digest>.json`) the authority for the
start, and no door's proof was checked by anyone but the claimant (the
guard's directory protection was bypassed by `cd ... && cp payload
confirm/D.json`). Now **no file is authority anywhere**, and every check
moves from the claimant to the verifier: the solo lead's own process, the
one that serves the start socket, takes the lead lock and supervises the
scheduler.

- **The witness.** The lead keeps the confirmations it witnessed in memory
  (`leadStarter.witnessed`, internal/cli/lead_start.go), keyed by digest,
  each with its door and what the lead itself read, expiring 30 minutes
  after the witness. The start accepts only a witnessed, unexpired
  confirmation of the exact digest (`witnessFor`): none is
  `confirmation_missing`, an expired one `confirmation_stale`, each naming
  the two doors with the prefix and the full digest. **A lead restart
  forgets every confirmation, and the owner confirms again.**
- **The log.** `<root>/extensions/amq-squad/confirm/<content hex>.json` is
  the lead's log, written by the lead alone through atomicfile (an
  exclusive create of a random temp name beside it, `os.CreateTemp`, its
  mode set to 0600, then a rename): one entry
  per witnessed confirmation with the door and what the lead read (the pi
  pid and stamp; the peer's chain; the event id). Nothing reads it for
  authority: a row plants and edits it and the start is unchanged. The one
  read of it is for refusal: the Buzz event ids the lead witnessed
  (`confirm/events/<event id>.json`) refuse a replay, across a restart too;
  nothing in it ever grants. The
  guard's refusal of writes into it stays as a cheap second layer that
  decides nothing.
- **The socket's address.** The lead writes
  `<root>/extensions/amq-squad/lead-start.json` (the socket path and its
  pid) so the processes that are not its descendants (the Buzz attachment,
  and the shell apply's check of whether a lead runs) can reach it. It is no authority either: the dial checks
  the socket is private and the owner's (`seatlaunch.DialPrivate`), and the
  lead proves every peer itself.

`<root>` is the solo lead's AMQ root for the repository (`soloHome`:
`~/.amq-squad/solo/<dir>-<sha12>/root`, the lead's `AM_ROOT`).

### 2.3 The two doors, each proved by the lead (sections 2 and 3c)

| door | the claimant sends | the lead checks itself | the lead records |
|---|---|---|---|
| tui | `/squad confirm <prefix>`: the digest and its pane over the start socket | the peer is exactly the pi process it spawned: its pid, with the start stamp read at the spawn | the pi pid and stamp, the pane |
| buzz | the attachment hands it the raw event bytes it fetched on its own connection | with no connection and no key: the id recomputed from the bytes and matched, the signature under the pinned owner key, the DM kind, the pinned channel, the window, once-only per event id (in memory and in its log, under one lock), the prefix against its own proposals | the event id it verified |

- **(c) the TUI door.** `/squad confirm` is a pi command registered by the
  extension for the solo lead (`installSoloTeam`), never a tool: pi hands a
  command to the extension, never to the model (31-2's pattern). The
  extension matches the typed prefix against the digest it printed through
  `amq_team_propose` and sends `{kind: confirm-tui, digest, pane}` over the
  start socket; it writes nothing. The lead accepts it only when the
  kernel's peer pid is the pi it spawned and still carries the start stamp
  the lead read at the spawn (`runProcess` hands the lead pi's pid as it
  starts it, `piStartedHook`). The model's shell commands are pi's
  children, never pi itself, and a reused pid carries another stamp: both
  are `peer_not_lead_pi`.
- **(a) the shell door is dropped** (the brief's own rule: "if that cannot
  be built, door (a) is dropped and the note says why"). Under one uid, no
  kernel read tells the owner's shell from a tree the model built; a shell
  door needs a key or a different uid. Codex's re-check of #834 showed the
  path: a helper the model writes forks, calls setsid, opens a new
  controlling pty and waits for its parent to exit; it is then its own
  session leader with a terminal and no lead or seat ancestor, and it
  passed every kernel read the shell door made. Closing "parent is pid 1"
  does not help (a double fork gives the orphan an orphaned session leader
  as parent). So `amq-squad team confirm` is gone (an unknown verb), the
  lead's socket refuses the confirm-shell kind by name
  (`shell_door_removed`), and the procinfo reads only the shell door used
  (a process's initial environment, its session, its terminal) are
  removed.
- **(b) the Buzz door.** The mailbox handler (internal/buzz/inbound.go)
  fetches the event by its id on its own connection and verifies the
  owner's signed `yes <16 hex>` on the typed path first, as it does for a
  gate answer. Its confirmer (`confirmByPrefix`, wired in `remote attach`)
  finds the solo root whose recent proposal the prefix names and hands that
  root's lead the raw event bytes, over the lead's start socket (no
  attach-to-lead channel existed). The lead never holds a key and signs in
  with nothing, ever (the plan reviewer's ruling: `gateTransport`'s callers
  are harness processes, not a lead with a model under it), so it opens no
  connection: it verifies the bytes itself (`witnessBuzz`): the event id
  recomputed from the bytes and matched, the signature under the pinned
  owner key (from the owner's gate binding, a pin, not a key), the DM kind,
  the pinned channel, the routing window, `yes <prefix>` against its own
  recent proposals, once-only per event id; only then is the confirmation
  witnessed, with the event id it verified. The attachment can withhold an
  event but cannot alter one (the id stops recomputing) or forge one (the
  owner's signature). The handler's reply names the team only (`confirmed
  team`, `refused team <code>`), since no handler reply carries an answer's
  digest prefix.

### 2.4 Which apply needs a confirmation, and the no-lead case

Under the solo lead, the apply of the lead's proposal starts only on a
confirmation the lead witnessed (2.2). From a shell (without the lead's
socket in the environment), the owner's `team apply --digest D`, or `team
apply --from PLAN.json`, is the confirmation by the act: the digest is in
front of the owner and they typed the apply. The kernel cannot tell the
owner's shell from one the model built (2.3), so the fact that decides is
whether a model runs at all: the shell apply proceeds only when **no lead
holds the repository** (`noLeadHolds`, internal/cli/team_confirm.go):
neither the solo lead (its start socket answers) nor a team lead (the
session root's lead lock is held). With a lead running it refuses
`lead_running` naming the two doors, from any shell, the owner's included;
with none there is no model under it, and the apply writes squad.yaml
through the digest flow and prints the owner's `up`. No file is read for
it; a planted `confirm/<digest>.json` changes nothing. The 3.0 flag form
(`--roles`, or a squad.yaml `--from`) and the screen's add key
(`--add-seat`, confirmed at the key) stay as they were; from a seat or the
lead's tools both are refused by the guard (2.7).

### 2.5 The start in the lead's process (section 3)

The scheduler and the lead lock must live in the lead's process for its
life, and the apply runs as a short child of pi. So the solo lead serves a
private socket from its own process (`leadStarter`, internal/cli/
lead_start.go; `seatlaunch.ListenPrivate` under its AMQ root, the decision
socket's machinery) and gives its pi the path (`AMQ_SQUAD_LEAD_START_SOCKET`).
`team apply --digest D` under the solo lead sends `{digest, project}` there;
the lead's process proves the peer descends from it (`callerChain`, the
pattern of `panechain.Reaches`: the kernel's peer pid, the parent chain
walked to the lead's own pid, each pid's start stamp read at first sight,
the parent read again after each hop, every stamp read again at the end and
once more just before the lead acts; `caller_unproved` on any change, Codex
P2 on #834), and then:

1. takes the confirmation it witnessed of exactly D (2.2);
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
   launch budget's start (`AMQ_SQUAD_START`) is the witness's moment,
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

From a shell (no socket in the environment) `team apply --digest D` is the
owner's confirmation by the act while no lead holds the repository (2.4):
it writes the files through the digest flow and prints the owner's start,
`amq-squad up --project P`.

### 2.6 One implementation, counted (row 6)

up.go exports the steps as named functions: `UpCompile`, `UpSession`,
`UpDoctor`, `UpLeadLock`, `UpScheduler`, `UpRosterOutcome`. `up`'s methods
call the first three and the last; `lead --team` (which `up` starts in
tmux) calls `UpLeadLock` and `UpScheduler`; the start of a confirmed team
calls all six. Each counts its calls (`countShared`), and a row reads the
counter over `up`, over `lead --team` and over the start.

### 2.7 3b: the hole in the solo phase, closed where it can be and stated

- **(i)** a kernel-level shell door cannot be built under one uid (2.3);
  it is dropped, and the shell apply turns on whether a lead runs (2.4).
- **(ii) the solo lead's shell** refuses `tmux` and every `amq-squad` verb
  outside a read-only allowlist before the guard runs (`soloShellRefusal`,
  pi/extensions/lib/team.ts, from the extension's `tool_call` hook when
  `AMQ_SQUAD_SOLO_LEAD=1`). Before the yes the solo lead holds no lead
  lock, so `amq-squad up`, `lead --team` or `scheduler run` from its shell
  would start a whole team with no confirmation; the allowlist is
  `status`, `context`, `doctor`, `version`, `task show`, `task list`,
  `team propose`, and every other verb, a bare `amq-squad` included, is
  refused. It reads the line as the shell would: quote characters,
  backslashes and `$'` dropped and the line lowercased before the split
  (`t""mux`, `t\mux`, `$'tmux'`, `"tm"'ux'`, `TMUX`); `tmux` inside any
  token (a path, `xtmux`); `amq-squad` (by base name, inside any token)
  followed by a verb outside the allowlist; an expansion or a command
  substitution joined to letters (`tm${x}ux`, `tm$(true)ux`) and backticks;
  `eval`, `exec -a` or `env -S` with a token holding t, m, u, x in order.
  In an argv form argv[0]'s base name decides, and an argv[0] that runs a
  command line (`sh -c`, `env`, `eval`, `exec`) has its words read as a
  line. Ordinary quoted commands run (`echo "hello world"`, `git log
  --format="%h %s"`). The forms and the allowlist are one table
  (pi/test/solo-shell-forms.json) the extension's row and the guard's row
  both read.
- **the guard** (`owner_only`, internal/cli/hook_guard.go) refuses the same
  forms and the same allowlist for a solo lead's shell call (session
  `solo`, a root under `~/.amq-squad/solo/`) as a second layer
  (`soloLeadShellRefusal`); it refuses `amq-squad team confirm` and `team
  apply` in a shell call from any seat or lead, and writes into the lead's
  log directory (deciding nothing now).
- **(iii) the solo-phase limit**, the line the owner reads: *Before the
  yes the solo lead holds the owner's uid, a shell, edit and write. No
  file it writes is a confirmation, the lead proves both doors itself, and
  its shell cannot reach them: the TUI door accepts only pi itself (the
  model's commands are pi's children), and the Buzz door needs bytes whose
  id recomputes and that the owner's pinned key signed, which the lead
  checks itself with no connection and no key; a shell apply runs only
  while no lead runs. The one path left is a model typing into its own
  pane: a keystroke into the pane where the owner types `/squad confirm`
  reaches pi as the owner's would. The shell's string layer stops the
  direct spellings; Codex's re-check confirms these still evade it, and
  each is a way a model reaches its own pane (or runs an amq-squad verb):
  a script file it writes and runs; an encoded python or perl one-liner;
  an alias; a renamed symlink to tmux or amq-squad; a glob such as
  `/usr/bin/t?ux`; a standalone `$(printf '\164mux')`. That residual is
  bounded by the mayor's cost cap and fleet grant (no start outside a
  permitted repository, none past max_squads, none while a cap cannot be
  metered) and closed when 3.2 brings the signed door.* A named close, a
  3.2 candidate (not work, not built): the solo lead's shell tool runs in
  a sandbox that denies exec of the amq-squad binary and tmux and any
  write under the AMQ root (seatbelt on macOS; the Codex seat already runs
  under its sandbox), which turns the string layer's evasion list into a
  kernel refusal; it sits beside the 3.2 signed door.

### 2.8 docs

README's First run is the conversation (`amq-squad lead`, say what you want,
the plan with its digest, the two doors, the apply); the host setup steps
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
  thread binding, so the two doors read alike. Not built in this PR: the
  seam is the handler's `teamYes` dispatch (internal/buzz/inbound.go) and
  `confirmByPrefix` (internal/cli/team_confirm.go), which would take the
  proposal from the thread's root event instead of the prefix.

### 2.10 Choices where the brief left one

- `--from` tells a plan by its first character (`{`) rather than a new
  flag.
- The TUI door reaches the lead over its start socket (one private socket,
  a message kind per door); the Buzz attachment reaches it the same way,
  since no attach-to-lead channel existed.
- A shell's apply is the confirmation by the act, only while no lead holds
  the repository (2.4).
- The start reaches the lead's process over a private socket (2.5); the
  mayor's `NeedsProvisioning` is off on this path, because up's session
  step provisions the root (`amq coop init`), so the launch API's `.amqrc`
  precondition (upstream ask 2) does not apply. The four checks the brief
  names all run.
- The Buzz door needed one dispatch in internal/buzz/inbound.go (the
  mailbox handler lives there; the brief declares internal/gateverify/, so
  the parse and the confirmer's type are in internal/gateverify/teamyes.go).
- The stamped walks re-read every stamp at the end and check a reparent at
  each hop; a re-read of the child's stamp mid-hop was dropped as redundant
  with the end's read (a reused pid never carries its old stamp again), so
  every check has its own row.

## 3. Rows

| row | test |
|---|---|
| 1a | cli `TestTheStartAcceptsOnlyAWitnessedFreshConfirmation` (none, a planted log entry, an expired witness, a witness of another digest: refused naming the two doors; an edited log after a witness changes nothing), `TestTheApplyReachesTheLeadOverItsSocketAndOnlyFromItsDescendants` (the same refusal over the socket) |
| 1b | cli `TestGuardRefusesTheOwnersVerbsFromASeat`, `TestTheShellApplyProceedsOnlyWithNoLeadHoldingTheRoot` (with the solo lead serving or a team lead holding the root, the shell apply and the plan-file apply refuse `lead_running` naming the two doors; with none the apply writes and prints the owner's `up`; a planted record changes nothing) |
| 1c | cli `TestTheTUIDoorIsWitnessedOnlyFromTheLeadsOwnPi` (a child of pi and pi's pid with another stamp refused `peer_not_lead_pi`; pi witnessed with its pid, stamp and pane; `runProcess` reports pi's pid), `TestTheSoloLeadServesTheStartSocketToItsPi` (the lead records its pi); pi `team.test.ts` "/squad confirm with the printed prefix asks the lead to witness it", "a model message containing /squad confirm reaches no lead; the confirm is a command, never a tool" |
| 1d | buzz `TestTheBuzzDoorConfirmsOnlyTheOwnersSignedYes` (the handler's typed path, the confirmer given the fetched bytes); cli `TestTheBuzzDoorIsWitnessedByTheLeadFromTheEventBytesItVerified` (no connection opened; an event the attachment altered by one byte `bad_id`, an event the attachment signed with its own key `wrong_pubkey`, no proposal, an hour-old yes outside the window, bytes that are no event `event_unverified`, the owner's yes witnessed once, its replay refused, and refused again by a restarted lead), `TestTheBuzzDoorWitnessesOneEventOnceUnderConcurrency` (sixteen concurrent submissions, one witness), `TestTheOwnersSignedYesOverBuzzIsWitnessedByTheLead` (end to end through the test relay and `remote attach --once`) |
| 1e | cli `TestTheShellDoorIsGone` (`amq-squad team confirm` is an unknown verb; the socket refuses confirm-shell by name, `shell_door_removed`) |
| 1f | pi `team.test.ts` "the solo lead's shell refuses tmux and the owner's verbs by argv[0] and by token", "the solo lead's shell sees through quoting, expansions and eval" (the shared table's refused and running forms, and its amq-squad allowlist equal to the extension's: up, lead, scheduler run, launch, team apply, task reset and bare amq-squad refused; status, task list and the other read-only verbs run); cli `TestTheGuardRefusesTheSoloLeadsQuotedTmuxAsASecondLayer` (the same table and allowlist); what each door records is the lead's own read (rows 1c, 1d) |
| 3 | cli `TestTeamProposeFromAPlanPrintsThePlanAndDigestAndWritesOnlyTheProposal`, `TestTheStartOfAConfirmedTeamRunsUpsSharedStepsAndNoInstallOrTmux`, `TestTheStartRefusesAPlanThatChangedSinceTheConfirmation`, `TestTheMayorsChecksRefuseByNameOnTheStartsPath` (a repository outside the grant, a cost cap, no fleet grant); team `TestThePlanRendersTheDocumentedSquadYAML`; pi "amq_team_propose prints the plan and digest; amq_team_apply returns the roster line", "amq_team_apply without a confirmation carries the lead's refusal naming the doors" |
| 6 | cli `TestTheStartRefusesLeadRunningWhileATeamLeadHoldsTheRoot`, `TestUpAndTheLeadTeamCountOnTheSameSharedSteps`, `TestTheStartOfAConfirmedTeamRunsUpsSharedStepsAndNoInstallOrTmux` (the counter, no install, no tmux), `TestTheStartSocketProvesItsCallerWithStartStamps` (stamps at first sight, a reparent mid-hop, a reuse before the act), `TestTheSoloLeadServesTheStartSocketToItsPi`, `TestASoloLeadIsALeadParentOfItsScheduler` |

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
REVERT the start without the witness: fails (the row catches it)
   --- FAIL: TestTheApplyReachesTheLeadOverItsSocketAndOnlyFromItsDescendants (0.09s)
   --- FAIL: TestTheStartAcceptsOnlyAWitnessedFreshConfirmation (0.07s)
REVERT no 30-minute bound on the witness: fails (the row catches it)
   --- FAIL: TestTheStartAcceptsOnlyAWitnessedFreshConfirmation (0.09s)
REVERT a witness of another digest taken for this one: fails (the row catches it)
   --- FAIL: TestTheStartAcceptsOnlyAWitnessedFreshConfirmation (0.10s)
REVERT the lead's log read for authority: fails (the row catches it)
   --- FAIL: TestTheStartAcceptsOnlyAWitnessedFreshConfirmation (0.08s)
REVERT the refusal naming one door (no Buzz door): fails (the row catches it)
   --- FAIL: TestTheStartAcceptsOnlyAWitnessedFreshConfirmation (0.02s)
REVERT the TUI door from any peer: fails (the row catches it)
   --- FAIL: TestTheTUIDoorIsWitnessedOnlyFromTheLeadsOwnPi (0.03s)
REVERT the TUI door ignoring pi's start stamp: fails (the row catches it)
   --- FAIL: TestTheTUIDoorIsWitnessedOnlyFromTheLeadsOwnPi (0.03s)
REVERT pi's pid not reported at the spawn: fails (the row catches it)
   --- FAIL: TestTheTUIDoorIsWitnessedOnlyFromTheLeadsOwnPi (0.03s)
REVERT the solo lead not recording its pi: fails (the row catches it)
   --- FAIL: TestTheSoloLeadServesTheStartSocketToItsPi (0.02s)
REVERT no lead-start.json (the shell apply cannot see the running lead): fails (the row catches it)
   --- FAIL: TestTheShellApplyProceedsOnlyWithNoLeadHoldingTheRoot (0.06s)
REVERT the shell apply with no lead check: fails (the row catches it)
   --- FAIL: TestTheShellApplyProceedsOnlyWithNoLeadHoldingTheRoot (0.06s)
REVERT the shell apply ignoring a running solo lead: fails (the row catches it)
   --- FAIL: TestTheShellApplyProceedsOnlyWithNoLeadHoldingTheRoot (0.07s)
REVERT the shell apply ignoring a team lead's lock: fails (the row catches it)
   --- FAIL: TestTheShellApplyProceedsOnlyWithNoLeadHoldingTheRoot (0.07s)
REVERT the plan-file apply with no lead check: fails (the row catches it)
   --- FAIL: TestTheShellApplyProceedsOnlyWithNoLeadHoldingTheRoot (0.07s)
REVERT the socket accepting the dropped confirm-shell kind: fails (the row catches it)
   --- FAIL: TestTheShellDoorIsGone (0.02s)
REVERT the lead not recomputing the event id from the bytes: fails (the row catches it)
   --- FAIL: TestTheBuzzDoorIsWitnessedByTheLeadFromTheEventBytesItVerified (0.11s)
REVERT the lead taking another key's yes: fails (the row catches it)
   --- FAIL: TestTheBuzzDoorIsWitnessedByTheLeadFromTheEventBytesItVerified (0.15s)
REVERT the lead taking an hour-old yes (no window): fails (the row catches it)
   --- FAIL: TestTheBuzzDoorIsWitnessedByTheLeadFromTheEventBytesItVerified (0.06s)
REVERT the lead witnessing a replayed event (no once-only): fails (the row catches it)
   --- FAIL: TestTheBuzzDoorIsWitnessedByTheLeadFromTheEventBytesItVerified (0.07s)
REVERT the once-only not kept across a restart (no event log read): fails (the row catches it)
   --- FAIL: TestTheBuzzDoorIsWitnessedByTheLeadFromTheEventBytesItVerified (0.07s)
REVERT the once-only's check and mark not under one lock: fails (the row catches it)
   --- FAIL: TestTheBuzzDoorWitnessesOneEventOnceUnderConcurrency (0.25s)
   --- FAIL: TestTheBuzzDoorWitnessesOneEventOnceUnderConcurrency (0.18s)
   --- FAIL: TestTheBuzzDoorWitnessesOneEventOnceUnderConcurrency (0.24s)
   --- FAIL: TestTheBuzzDoorWitnessesOneEventOnceUnderConcurrency (0.30s)
REVERT the Buzz door's witness without the event id: fails (the row catches it)
   --- FAIL: TestTheBuzzDoorIsWitnessedByTheLeadFromTheEventBytesItVerified (0.06s)
   --- FAIL: TestTheOwnersSignedYesOverBuzzIsWitnessedByTheLead (0.07s)
REVERT the guard letting a seat run team confirm and apply: fails (the row catches it)
   --- FAIL: TestGuardRefusesTheOwnersVerbsFromASeat (0.00s)
REVERT the guard letting a write into the records: fails (the row catches it)
   --- FAIL: TestGuardRefusesTheOwnersVerbsFromASeat (0.00s)
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
   --- FAIL: TestTheOwnersSignedYesOverBuzzIsWitnessedByTheLead (0.37s)
REVERT the socket taking any process (no caller chain): fails (the row catches it)
   --- FAIL: TestTheApplyReachesTheLeadOverItsSocketAndOnlyFromItsDescendants (0.04s)
REVERT the caller's stamps not re-read before the act: fails (the row catches it)
   --- FAIL: TestTheStartSocketProvesItsCallerWithStartStamps (0.03s)
REVERT the caller's reparent mid-hop not checked: fails (the row catches it)
   --- FAIL: TestTheStartSocketProvesItsCallerWithStartStamps (0.03s)
REVERT the lead taking another repository's request: fails (the row catches it)
   --- FAIL: TestTheStartOfAConfirmedTeamRunsUpsSharedStepsAndNoInstallOrTmux (0.07s)
REVERT the mayor's checks not applied (a grant that lets everything): fails (the row catches it)
   --- FAIL: TestTheMayorsChecksRefuseByNameOnTheStartsPath (0.22s)
   --- FAIL: TestTheMayorsChecksRefuseByNameOnTheStartsPath/a_repository_outside_the_grant (0.07s)
   --- FAIL: TestTheMayorsChecksRefuseByNameOnTheStartsPath/a_cost_cap (0.07s)
   --- FAIL: TestTheMayorsChecksRefuseByNameOnTheStartsPath/no_fleet_grant (0.07s)
REVERT no R40 fallback (the lead proposes, the owner starts): fails (the row catches it)
   --- FAIL: TestTheMayorsChecksRefuseByNameOnTheStartsPath (0.06s)
   --- FAIL: TestTheMayorsChecksRefuseByNameOnTheStartsPath/no_fleet_grant (0.02s)
   --- FAIL: TestTheMayorsChecksRefuseByNameOnTheStartsPath/a_repository_outside_the_grant (0.02s)
REVERT the start writing a digest the owner did not confirm: fails (the row catches it)
   --- FAIL: TestTheStartRefusesAPlanThatChangedSinceTheConfirmation (0.07s)
REVERT the squad files planned over the mayor's spelling of the checkout: fails (the row catches it)
   --- FAIL: TestTheStartOfAConfirmedTeamRunsUpsSharedStepsAndNoInstallOrTmux (0.03s)
REVERT the start ignoring a held lead lock: fails (the row catches it)
   --- FAIL: TestTheStartRefusesLeadRunningWhileATeamLeadHoldsTheRoot (0.09s)
REVERT the launch budget not starting at the confirmation: fails (the row catches it)
   --- FAIL: TestTheStartOfAConfirmedTeamRunsUpsSharedStepsAndNoInstallOrTmux (0.08s)
REVERT a second start in the same lead: fails (the row catches it)
   --- FAIL: TestTheStartOfAConfirmedTeamRunsUpsSharedStepsAndNoInstallOrTmux (0.07s)
REVERT the session step uncounted (up's and the apply's path not shared): fails (the row catches it)
   --- FAIL: TestTheStartOfAConfirmedTeamRunsUpsSharedStepsAndNoInstallOrTmux (0.07s)
   --- FAIL: TestUpAndTheLeadTeamCountOnTheSameSharedSteps (0.01s)
REVERT lead --team taking the lead lock outside the shared function: fails (the row catches it)
   --- FAIL: TestUpAndTheLeadTeamCountOnTheSameSharedSteps (0.03s)
REVERT lead --team starting the scheduler outside the shared function: fails (the row catches it)
   --- FAIL: TestUpAndTheLeadTeamCountOnTheSameSharedSteps (0.03s)
REVERT the solo lead not a lead parent of its scheduler: fails (the row catches it)
   --- FAIL: TestASoloLeadIsALeadParentOfItsScheduler (0.00s)
REVERT the lead check widened to the binary (any amq-squad parent): fails (the row catches it)
   --- FAIL: TestASoloLeadIsALeadParentOfItsScheduler (0.00s)
REVERT the solo lead's pi without the solo flag: fails (the row catches it)
   --- FAIL: TestTheSoloLeadServesTheStartSocketToItsPi (0.02s)
REVERT the TUI door taking any prefix: fails (the row catches it)
   not ok 3 - /squad confirm with the printed prefix asks the lead to witness it
REVERT the TUI door sending no pane: fails (the row catches it)
   not ok 3 - /squad confirm with the printed prefix asks the lead to witness it
REVERT the TUI door showing a confirmation the lead refused: fails (the row catches it)
   not ok 3 - /squad confirm with the printed prefix asks the lead to witness it
REVERT the shell's argv form unchecked: fails (the row catches it)
   not ok 5 - the solo lead's shell refuses tmux and the owner's verbs by argv[0] and by token
   not ok 6 - the solo lead's shell sees through quoting, expansions and eval
REVERT the shell's line not dequoted: fails (the row catches it)
   not ok 6 - the solo lead's shell sees through quoting, expansions and eval
REVERT tmux only as a whole token: fails (the row catches it)
   not ok 5 - the solo lead's shell refuses tmux and the owner's verbs by argv[0] and by token
   not ok 6 - the solo lead's shell sees through quoting, expansions and eval
REVERT no rule for an expansion joined to letters: fails (the row catches it)
   not ok 6 - the solo lead's shell sees through quoting, expansions and eval
REVERT no rule for eval, exec -a and env -S: fails (the row catches it)
   not ok 6 - the solo lead's shell sees through quoting, expansions and eval
REVERT the guard's second layer not applied to the solo lead: fails (the row catches it)
   --- FAIL: TestTheGuardRefusesTheSoloLeadsQuotedTmuxAsASecondLayer (0.00s)
REVERT the guard's second layer not dequoting: fails (the row catches it)
   --- FAIL: TestTheGuardRefusesTheSoloLeadsQuotedTmuxAsASecondLayer (0.01s)
REVERT the shell's refusal not wired into tool_call: fails (the row catches it)
   not ok 5 - the solo lead's shell refuses tmux and the owner's verbs by argv[0] and by token
REVERT propose's plan not on stdin: fails (the row catches it)
   not ok 1 - amq_team_propose prints the plan and digest; amq_team_apply returns the roster line
REVERT any amq-squad verb allowed in the solo lead's shell: fails (the row catches it)
   not ok 5 - the solo lead's shell refuses tmux and the owner's verbs by argv[0] and by token
   not ok 6 - the solo lead's shell sees through quoting, expansions and eval
REVERT the extension's allowlist not the table's (up added): fails (the row catches it)
   not ok 6 - the solo lead's shell sees through quoting, expansions and eval
REVERT any amq-squad verb allowed by the guard's second layer: fails (the row catches it)
   --- FAIL: TestTheGuardRefusesTheSoloLeadsQuotedTmuxAsASecondLayer (0.00s)
REVERT the team tools and /squad confirm given to the team lead too: fails (the row catches it)
   not ok 2 - the lead's model tool list carries no key verb and no gate-answer verb
66 runs, 66 caught, 0 missed
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

The block shows `confirmed at ... (door tui, witnessed by the lead)`. Ask
the lead to apply; it
calls `amq_team_apply`, which returns the steps (compile, session, doctor,
lead, scheduler, roster) and one line per seat, then `team started:
roster_launched: ...`. From another shell:

```sh
amq-squad status --project /Users/omri/code/amq-squad --json
cat ~/.amq-squad/solo/amq-squad-*/root/extensions/amq-squad/confirm/*.json
```

The roster is the plan's seats; the lead's log entry has `door: tui` with
the pi pid, its start stamp and the pane (the log is a record of what the
lead witnessed, never read for authority). In PR 1 the roster comes back in the conversation and the plan
block; the team lead's screen and tools on the yes are R53's switch (PR 2),
or the next `amq-squad up`.

**B. The same, confirmed over Buzz.** Steps of A, with the attachment
running (`amq-squad remote attach --project /Users/omri/code/amq-squad
--session <session> --me <lead>`), and instead of `/squad confirm` the
owner's DM in the Buzz thread:

```
yes <prefix>
```

The DM's result row reads `confirmed team`; the lead's log entry has
`door: buzz` and the event id it verified; the apply then starts the team
as in A.

**C. An apply refused with no witnessed confirmation.** Propose as in A,
then, before any door, ask the lead to apply: it refuses
`confirmation_missing` naming the two doors (`/squad confirm <prefix>`, the
Buzz DM `yes <prefix>`); squad.yaml is unchanged. Confirm, quit the lead, start it again
and ask it to apply: it refuses the same (a restart forgets every
confirmation). Writing a file into `confirm/` changes nothing.

**D. The owner's own apply.** With the lead running, from your own shell:

```sh
amq-squad team apply --digest <digest> --project /Users/omri/code/amq-squad
```

It refuses `lead_running` naming the two doors. Quit the lead and run it
again: with no lead holding the repository the act is the confirmation,
squad.yaml is written and the start is `amq-squad up --project
/Users/omri/code/amq-squad`.

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
- Door (a), the shell door with a kernel-level refusal (3b (i)), cannot be
  built: under one uid, no kernel read tells the owner's shell from a tree
  the model built; a shell door needs a key or a different uid. It is
  dropped (2.3), as the brief's own rule says, and the shell apply turns on
  whether a lead runs (2.4).
- "The record carries the door's proof" (3c): a proof the record carries
  is the claimant's assertion, and the first form of this PR checked it
  nowhere but in the claimant's own write. Codex's P1 on #834 found it; the
  plan reviewer's ruling moved every proof to the verifier: the lead reads
  each door's proof itself and records what it read (2.2, 2.3), and no
  file is authority.
- A confirmation as the one gate on "every" apply would change the owner's
  3.0 flag-form apply and the add key; it binds the lead's artifacts, and
  the owner's own apply is the confirmation by the act (2.4).
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
(`/squad confirm <prefix>`) or over Buzz (`yes <prefix>`), the lead
witnesses it itself, and it starts the team
through `up`'s own steps, inside your fleet grant.
