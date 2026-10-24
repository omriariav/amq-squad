# 4-3 execution log: operator session and mayor

Brief: `docs/v3-phase2-briefs.md`, "4-3 Operator session and mayor", with
lead rulings 6, 15, 16 and 17. Branch `v3-4c`, cut from `v3` at 6acb80f0.
Implementer: Opus 5.5, medium effort, one worktree, no helper agents.
Gate output of every commit, in full: `docs/v3-spike/4-3-evidence/`.

## Rulings received during the run

- Lead, 2026-10-24: `lead_handle` moves from 2b-4 (its acceptance 8) to
  4-3 as acceptance 7, exactly per the 2b-4 text: top level, nullable
  string, in `status.NullableFields`, the golden gains it, schema_version
  stays 1. Source order: the single-squad producer uses the seat whose role
  is `lead` (null when none); the fleet form carries each squad document
  unchanged, with fleet.yaml's lead handle filling a null `lead_handle`. The
  single-squad golden carries a non-null value, the fleet golden a null one.
  4-4's consumer is unchanged: it already reads `lead_handle` first.
- Lead, same message: the squad.yaml digest flow (2b-5) and the roster
  launcher (2b-2) are not on v3; the mayor codes against two interfaces,
  named here, so that each adapter is one function after those merge.
- No grants schema change: `grants.Fleet` already carries `max_squads`,
  `cost_per_day_usd` and `permitted_repositories` (3-1).

## Interfaces the adapters fill (after 2b-5 and 2b-2 merge)

- `fleet.SquadWriter` (`internal/fleet/mayor.go`): `Propose(projectPath,
  squadYAML) (digest, error)` and `Apply(projectPath, digest) error`. The
  adapter is one function over 2b-5's `team.ParseSquad`, `team.NewPlan` and
  `Plan.Apply` (`internal/team/digest.go` on v3-2b-5 at 077b043b).
- `fleet.RosterLauncher` (`internal/fleet/mayor.go`): `Launch(ctx,
  LaunchRequest) error`. The adapter is one function over 2b-2's seat
  launch compiler and the AMQ `launchapi` Prepare/Apply.

## Commit 1: status lead_handle, fleet document, fleet registry, lead-session entrypoint

Built:

- `internal/status`: `lead_handle` (acceptance 7): `Status.LeadHandle
  *string`, set by `Build` from the first seat (sorted by handle) whose role
  is `lead`, `null` otherwise; `NullableFields` lists it; the single-squad
  golden gains `"lead_handle": "lead"`. `status.LeadRole` names the role.
- `internal/status/fleet.go`: the fleet document of ruling 16 (`Fleet`,
  `BuildFleet`, `KindFleet`) and `IsFleet` (a document with `squads` is a
  fleet). Fleet golden `internal/status/testdata/status-fleet-v1.golden.json`:
  the single-squad golden wrapped in the fleet header, its `lead_handle`
  null (generated with `jq` from `status-v1.golden.json`; a test keeps the
  two equal except that field).
- `internal/fleet/registry.go`: `~/.amq-squad/fleet.yaml`, schema 1: an
  optional `operator:` block (the operator session's AMQ root, project,
  session) and `squads:` (name, root, project_path, repository, lead,
  optional session and project). Strict parse (unknown or repeated key, tab,
  misplaced line, missing schema all fail), canonical `Render`,
  `Validate`, `Read` (refuses a symlink or non-regular file), `Update`
  (0700 non-symlink directory, `flock` exclusive lock, temp file, fsync,
  rename; a refused mutation leaves the file as it was). Ported from the
  global_noc.go extract (`canonicalGlobalNOCControlRoot`,
  `ensureGlobalNOCRegistryDirectory`, `readGlobalNOCRegistry`,
  `writeGlobalNOCRegistry`, `validateGlobalNOCRegistry`): the same
  symlink refusals, lock and atomic replace; the NOC launch and run records
  had no reader and are not carried over.
- `internal/fleet/status.go`: `fleet.Status` builds the fleet document from
  the registry through a `Collector` (the single-squad producer), and
  `GroupInbox`, the manual-mode list (open gates of all squads with their
  squad, oldest first; lead questions with a relay argv).
- `internal/fleet/mailbox.go`: the operator mailbox is the owner's `user`
  handle (ruling 16). `LeadToOperatorArgv` (lead to operator) and
  `RelayArgv` (operator to a squad seat) are `amq send --root R --me H
  --project P --session S --to T ...` argv arrays: AMQ cross-project routing
  with the sender-origin metadata a reply needs (AMQ
  `docs/session-routing.md`).
- `internal/leadsession`: the one entrypoint `Start(Config)`. `Config` is
  a `Program` (runtime, model, root, project path, handle) and a grant;
  the grant alone gives the scope. `grants.Resolved.FleetScope()` is true
  for a fleet grant (`fleet.max_squads > 0`, not insecure); auto mode adds
  the mayor verb. No verb answers a typed gate.
- `internal/grants`: `Resolved.FleetScope`, `Fleet.RepositoryPermitted`
  (case-insensitive owner/name, an empty list permits none).
- `internal/cli/fleet.go`: `amq-squad fleet list|add|remove|operator
  [--fleet-file PATH]`; `amq-squad status --fleet [--fleet-file PATH]
  [--json]` (status.go gains the two flags and the branch).

Behavior changes:

1. `grants.LoadFrom(owner, repoRoot)` with a repository root returns no
   fleet grant (`Fleet` zeroed). A repository's grant is a squad grant; the
   fleet grant is the operator session's alone (plan 3.5, R39).
   `LoadFrom(owner, "")` is unchanged. No v3 caller read `Fleet` before.
2. `status --json` gains `lead_handle` (additive; schema 1 stays 1).
3. New verb `fleet`; `status --fleet`.

Rows retired (non-test lines before -> after / target):

- `internal/cli/global_noc.go` 265 -> deleted / 265. Ledger (2a-2
  execution, kept-dead extract table): extract "the fleet registry
  read/write extract (globalNOCRegistryPath, canonicalGlobalNOCControlRoot,
  ensureGlobalNOCRegistryDirectory, readGlobalNOCRegistry,
  writeGlobalNOCRegistry, validateGlobalNOCRegistry) and its closure",
  kept for "`status --fleet` reading fleet.yaml (plan 16.3)"; manifest
  reason "fleet board belongs to amq-noc", extract "fleet registry
  read/write (:117-265) kept per 4.4 default". Renamed into
  `internal/fleet/registry.go` (fate new). Its test file
  (`global_noc_test.go`, 20 lines, the symlink refusal) is replaced by
  `TestSymlinksRefused` in `internal/fleet/registry_test.go`.
- New rows (fate new): `internal/cli/fleet.go` 162,
  `internal/fleet/registry.go` 349, `internal/fleet/status.go` 115,
  `internal/fleet/mailbox.go` 38, `internal/leadsession/leadsession.go`
  86, `internal/status/fleet.go` 46.
- Rows resized: `internal/status/classify.go` 377 -> 386,
  `internal/status/schema.go` 261 -> 265, `internal/status/compat.go`
  109 -> 110, `internal/grants/grants.go` 307 -> 320 (new rows, target =
  size), `internal/cli/status.go` 453 -> 460 / 900,
  `internal/cli/command_registry.go` 88 -> 90 / 101.

Tests (acceptance rows):

- Acceptance 1, `TestSameProgramDifferentGrants`
  (`internal/leadsession`): two grant fixtures from one owner file
  (`LoadFrom(owner, repo)` and `LoadFrom(owner, "")`); the two configs are
  equal once the grant is zeroed; `Start` gives the same `Program`, squad
  scope and no fleet or mayor verb for the squad grant, fleet scope and the
  mayor for the fleet grant. `TestFleetScopeNarrowing`: manual mode keeps
  fleet scope without the mayor; an insecure grant has no fleet scope.
- Acceptance 2, `TestFleetStatusValidatesAgainstTheFleetGolden`
  (`internal/fleet`): three fixture squads give one fleet document that
  validates against the fleet golden; each squad validates against the
  single-squad golden; the producer's lead seat wins, fleet.yaml fills a
  null `lead_handle`; a wrapped squad equals the producer's document byte
  for byte. CLI form: `TestStatusFleetJSONIsTheFleetDocument` (three
  squads registered with `fleet add`, real task stores, `status --fleet
  --json` against the fleet golden), `TestStatusFleetEmpty`.
- Acceptance 3 (code in this commit, the CLI view in commit 2),
  `TestGateGrouping`: open gates of two squads in one list with their
  squad, oldest first; an accepted gate is not listed; the lead question
  carries a relay argv.
- Acceptance 7, `TestLeadHandle`, and `TestFleetGoldenIsTheSquadGoldenWrapped`,
  `TestIsFleet` (`internal/status`).
- Registry: `TestRenderParseRoundTrip`, `TestParseIsStrict`,
  `TestReadUpdate` (eight concurrent `Update` calls keep all eight squads;
  a refused update leaves the bytes), `TestSymlinksRefused`,
  `TestRoutesAreArgv`.

Gate: `docs/v3-spike/4-3-evidence/c1-fleet-registry-status.txt`, PASS
(build, vet, `go test -count=1 -v ./...`, manifest-check --final, linux and
windows builds, `-race` on fleet, leadsession, status, grants). No live
seat on the host (pgrep recorded in the file).

Deviations:

- fleet.yaml has its own small strict parser in `internal/fleet`. 2b-5's
  `internal/yamlsub` is not on v3; after 2b-5 merges the parser can move
  onto it (same subset: scalars, one map block, one list of maps).
- The ledger's 4.4 default "about 150 lines" for the registry: the new
  registry is 349 lines with its parser and renderer (fate new, outside the
  band), against the 265-line extract it replaces.

Open rulings: none.

## Commit 2: operator inbox (manual mode) replaces the v2 operator status; operator.go shrunk

Lead rulings for this commit (2026-10-24): replace `operator status` with
`operator inbox`, delete the v2 verb; follow-up strings in gate_close.go,
compound_release_attention.go, operator_attention.go and team_rules.go
print `amq-squad status --json`; the deleted verb's production code is not
moved into a test support file; tests whose subject is the deleted verb
are retired with it, named here; tests that reached operator_attention.go
through the verb call the projection directly; `operator answer` stays
untouched (3-2 decides what of it survives). And from the halfway review:
the fleet-grant zeroing of a repository grant gets a test with a named
reason.

Built:

- `amq-squad operator inbox [--fleet-file PATH] [--json]`
  (`internal/cli/fleet.go`, `runOperatorInbox`): the manual-mode view of
  plan 3.5. It reads the status contract of every squad of fleet.yaml and
  prints `fleet.GroupInbox`: every open gate-store gate with its squad and
  root, oldest first, and every lead question to the owner with the relay
  argv (`amq send --root <operator root> --me user --project <squad project>
  --session <squad session> --to <seat> --kind answer --thread <thread>`).
  A typed gate carries no answer action; only the owner answers it, with a
  signature. JSON kind `operator_inbox`.
- `grants.Narrowing.FleetKeys` and `grants.ReasonFleetInSquad`:
  `ParseSquadNarrowing` collects the squad.yaml keys `fleet`, `max_squads`
  and `permitted_repositories`, and `Narrow` names each in `Ignored` as
  "<key> fleet grant (the operator session's alone; a squad.yaml cannot
  grant it)". 2b-5's strict `ParseSquad` turns the first Ignored entry into a
  `WidenError` with that field, so on 2b-5 such a squad.yaml is rejected by
  name; on v3 the lenient guard path ignores it by name.

Behavior changes:

1. `amq-squad operator status` is gone: `operator` dispatches `answer` and
   `inbox`; the unknown-subcommand hint reads "Try 'answer' or 'inbox'."
2. Follow-up actions that printed `amq-squad operator status --project P
   [--profile F] --session S --json` now print `amq-squad status --project P
   [--profile F] --session S --json` (operator.go answer follow-ups,
   gate_close.go, compound_release_attention.go, operator_attention.go); the
   follow-up label is "show the squad status and attention". The team-rules
   line names `amq-squad status --json` only.
3. A squad.yaml naming fleet keys gets them in `Resolved.Ignored`.

Rows retired (non-test lines before -> after / target):

- `internal/cli/operator.go` 756 -> 495 / 600, status accepted-3 ->
  shrunk. Ledger (2a-2 execution, row table): "keep gate raise/answer and
  attention projection", 942 / 600; manifest note before this commit:
  "3-1: the typed-gate answer moved to the gate store, 942 -> 756 ...; 4-3
  owns the rest". Deleted: `runOperatorStatus`, `executeOperatorStatus`,
  `buildOperatorStatusData`, `operatorExecution`,
  `operatorStatusEnvelopeData`, `activeOperatorAttention`,
  `operatorSessionSnapshot`, `operatorUnreadBacklogWithProjected`,
  `operatorOpenGates`, `isOpenGateAttention`, `writeOrRenderOperatorStatus`
  (the v2 verb) and `releaseOperatorAnswerEnvelope` (no reader). The
  remainder is `operator answer` and its send path (`sendOperatorAMQ`,
  used by gate.go and team_launch_launchapi.go): merge-kind and launchapi
  decision AMQ gates, 3-2's question.
- `internal/cli/fleet.go` 162 -> 208 (new row), `internal/grants/grants.go`
  320 -> 329, `internal/grants/parse.go` 458 -> 460 (new rows, target =
  size). gate_close.go, compound_release_attention.go,
  operator_attention.go and team_rules.go change one string each; line
  counts unchanged.

For 3-2: `collectProjectedOperatorAttention`
(`internal/cli/compound_release_attention.go`, keep row) had one production
caller, the deleted `buildOperatorStatusData`. It is production-dead until
3-2 retires or re-targets operator_attention.go; its tests now call it
directly.

Tests retired with the verb (subject gone):

- `TestOperatorStatusDisabledProfileIsUnconfigured` (operator_test.go):
  the verb's "operator gates disabled" envelope message.
- `TestProjectedUnreadBacklogUsesThreadUnionAndExactMailboxState` and
  `TestProjectedUnreadBacklogComposesTerminalLifecycleWithSecureBarriers`
  (compound_release_attention_test.go): `operatorUnreadBacklogWithProjected`,
  the verb's unread-backlog counter.
- The `operator status` row of `TestScopedShortFlagAliasesRegistered`
  (scoped_flags_test.go): the verb's scoped flags.
- Assertions on the verb's envelope and poll block (kind, readonly,
  canonical inbox, poll owner, unread and open_gates counts) in the five
  operator_test.go tests, and the backlog assertions in the tests below.
- Helpers `operatorGatesOpenForTest`, `operatorBacklogForTest`.

Tests rewritten to call the projection directly (subject operator_attention.go;
new test-side helpers `projectAttentionForTest` and `openGatesForTest` in
`operator_attention_projection_test.go`):

- `TestOperatorStatusReportsAgedGateEscalation` ->
  `TestOperatorAttentionReportsAgedGateEscalation`.
- `TestOperatorStatusJSONReportsAttentionAndUnreadCounts` ->
  `TestOperatorAttentionReportsOneGateWithoutDirectiveDuplicate` (one gate
  attention, initial escalation, the directive not duplicated).
- `TestOperatorStatusKeepsGateOpenAfterStatusUpdate` ->
  `TestOperatorAttentionKeepsGateOpenAfterStatusUpdate`.
- `TestOperatorStatusClosesGateAfterOperatorAnswer` ->
  `TestOperatorAttentionClosesGateAfterOperatorAnswer`.
- `TestCompoundReleaseStatusPreservesReadBacklog` ->
  `TestCompoundReleaseAttentionKeepsOpenGatesAfterRead` (two open gates
  before and after the read; the backlog count went with the verb).
- `TestTerminalGateSuppressedFromOperatorStatusAndAgedWarnings` ->
  `TestTerminalGateSuppressedFromOperatorAttentionAndAgedWarnings`.
- In place: `TestTypedGateNewerExactBoundAnswerReplacesInvalidAnswerBarrier`,
  `TestAuthorityDuplicateVariantsFailClosedAcrossSurfaces`
  (authorization_envelope_test.go), `TestSafeTypedGateRaiseAnswerVerifyRoundTrip`
  (gate_test.go), `TestExplicitActionabilityPreservesNonGateSurfaceBehavior`
  (compound_release_attention_test.go).
- `TestOperatorGroupHelp` and the unknown-subcommand test name `inbox`.

New tests:

- Acceptance 3 through the verb, `TestOperatorInboxGroupsGatesOfTwoSquads`
  (internal/cli): two squads, a gate-store gate each (tag, external_send),
  registered with `fleet add`; `operator inbox --json` lists both, each
  with its squad, both open; the human form names them.
- `TestRepositoryGrantCarriesNoFleetGrant` (internal/grants): the owner's
  fleet grant reaches `LoadFrom(owner, "")` and not `LoadFrom(owner, repo)`;
  a squad.yaml with `fleet:`, `max_squads` and `permitted_repositories`
  gets three named Ignored entries; `RepositoryPermitted` is
  case-insensitive and an empty list permits none.

Gate: `docs/v3-spike/4-3-evidence/c2-operator-inbox.txt`, PASS. No live
seat on the host.

Deviations: none. Open rulings: none.

## Commit 3: the mayor (auto mode), operator start-squad

Built:

- `internal/fleet/mayor.go`: `Mayor` with the operator session's grant,
  the registry path, a `SquadWriter`, a `RosterLauncher`, an optional
  `CostMeter`, `Live` (default: every registered squad counts) and
  `NeedsProvisioning` (default `AMQProvisioned`). `Start` checks in order:
  a fleet grant (`no_fleet_grant`, refused), the repository in
  `permitted_repositories` (`repository_not_permitted`, refused), one squad
  per name and repository root (`squad_registered`, refused; plan 8: the
  mayor partitions work by repository), manual mode (`manual_mode`,
  proposed), a free slot under `max_squads` (`max_squads`, waiting), the
  day's spend under `cost_per_day_usd` (`cost_per_day`, waiting), and
  provisioning (`root_needs_provisioning`, proposed). Only then does it
  propose squad.yaml, apply the same digest, launch the roster and register
  the squad in fleet.yaml. Nothing is written for any other outcome. A
  waiting start joins `Waiting`; `Retry` starts the waiting ones in order
  when they fit.
- The ask 2 fallback (R40): `AMQProvisioned` reports a root with no
  `.amqrc` (or no root) as needing provisioning, because the launch API
  refuses an unknown session under such a root (docs/v3-upstream-asks.md,
  ask 2, measured on AMQ v0.102.0). The outcome is a `Proposal` for the
  owner (the squad, the reason, the detail, the squad.yaml text, and the
  human flow as argv: `amq-squad team propose --project P`, `amq-squad
  fleet add ...`); the mayor runs none of it.
- `amq-squad operator start-squad --name --root --project-path
  --repository --lead --squad-yaml FILE [--session] [--fleet-file]
  [--json]` (`internal/cli/fleet.go`): the mayor on the owner's grant
  (`grants.Load("")`). Its adapters `mayorSquadWriter` and `mayorLauncher`
  are nil on v3, so a start that passes every grant check stops with an
  error before any write; each becomes one function after 2b-5 and 2b-2
  merge (interfaces named at the top of this log). The waiting queue lives
  in the operator session's `Mayor`; the CLI verb is one start and does not
  persist it.
- No path answers a typed gate: `internal/fleet` and `internal/leadsession`
  import none of gatestore, gateverify, nostr or operatorauth, and the
  verbs a fleet grant adds (`status --fleet`, `operator inbox`, `operator
  start-squad`) answer nothing.

Behavior changes: new subcommand `operator start-squad`; the
unknown-subcommand hint lists `answer`, `inbox`, `start-squad`.

Rows (non-test lines): `internal/fleet/mayor.go` 260 (new row);
`internal/cli/fleet.go` 208 -> 278 (new row, target = size);
`internal/cli/operator.go` 495 -> 499 / 600 (shrunk: the dispatch case
and two usage lines).

Tests (acceptance rows):

- Acceptance 4, `TestMayorInsideTheGrant` (internal/fleet): with
  `max_squads` 2, two starts start (squad.yaml proposed and applied with the
  same digest, roster launched, registered); the third waits
  (`max_squads`) and is queued; a repository not in the permitted list is
  refused; neither writes, launches or registers; `Retry` keeps the third
  waiting while two squads are live and starts it once one stops; a second
  squad for a registered root is refused. `TestMayorCostAndScope`: over the
  day's cost waits, under it starts on `Retry`; a squad grant (no fleet
  grant) is refused; manual mode proposes and writes nothing. CLI form:
  `TestOperatorStartSquadInsideTheOwnerFleetGrant` (owner grants.yaml under
  a temporary HOME, `max_squads` 1: proposed, refused, started, waiting;
  only the started squad is written and launched; without adapters the
  start stops before any write).
- Acceptance 5, `TestMayorAnswerForATypedGateIsRejectedByTheCore`
  (internal/fleet), for each typed class (tag, external_send,
  credential_change, spawn): a real `gateverify.Core` raises the gate; the
  mayor's process holds only its own key; its approval with the exact tuple
  in the gate thread is rejected `wrong_pubkey`; the store has no accepted
  decision; `Verifier.Accepted` and `AcceptedFor` find none;
  `BuildAnswerFile` with the issuer key refuses to wrap the mayor's event;
  control: the owner's answer on the same gate is approved and passes the
  consumer check. `TestMayorHasNoGateAnsweringPath`: the import check above.
- Acceptance 6, `TestMayorAsk2FallbackProposes`: a root with no `.amqrc`
  gives `proposed` / `root_needs_provisioning` with the squad, the
  squad.yaml and argv steps; nothing proposed, applied, launched,
  registered or queued; after the owner provisions the root the same start
  launches.

Gate: `docs/v3-spike/4-3-evidence/c3-mayor.txt`, PASS; `-race` on the
4-3 CLI tests: `docs/v3-spike/4-3-evidence/c3-race-cli.txt` (13 tests,
PASS). No live seat on the host.

Deviations:

- The real squad.yaml writer and roster launcher are interfaces with nil
  CLI adapters until 2b-5 and 2b-2 are on v3 (lead-agreed).
- Cost per day is the grant check against a `CostMeter`; no meter is wired
  in the CLI (cost accounting beyond the grant check is out of scope).

Open rulings: none.

## Commit 4: fleet cap on the CLI path refused, fleet_cap in status --fleet (reviewer ruling)

Reviewer ruling on bfe61050: the in-process waiting queue and one start per
CLI call are accepted for 3.0 (restart recovery is 16.4, phase 5). Two
changes, applied here:

1. `Mayor.Session`: true only in the running operator session. A start
   held by `max_squads` or `cost_per_day_usd` waits and is queued only
   then; with `Session` false (the `operator start-squad` verb) it is
   refused with the same named reason (`max_squads`, `cost_per_day`) and
   the detail "no running operator session to queue it", never queued.
2. `status --fleet --json` gains `waiting[]` (additive to the ruling 16
   shape; the single-squad golden is untouched): one row per squad the
   mayor declined or deferred for the cap, `wait_reason: "fleet_cap"`, with
   squad, repository, project_path, cap, since (the first hold) and detail.
   The mayor records it in fleet.yaml under a new `waiting:` list
   (`fleet.Waiting`: name, repository, project_path, cap, since, detail;
   strict parse, validated: cap is max_squads or cost_per_day, since is RFC
   3339, a registered squad cannot also wait) and removes it in the same
   locked update that registers the squad when it starts. The fleet golden
   gains one `waiting` row; `status.Fleet.Waiting`,
   `status.FleetWaiting`, `status.WaitReasonFleetCap`; `BuildFleet` takes
   the waiting rows.

Behavior changes: `operator start-squad` past the cap is refused, not
waiting, and writes the fleet.yaml waiting row; a capped start is the one
non-started outcome that writes (only that row).

Rows (non-test lines): `internal/fleet/mayor.go` 260 -> 291,
`internal/fleet/registry.go` 349 -> 408, `internal/fleet/status.go` 115 ->
121, `internal/status/fleet.go` 46 -> 64, `internal/cli/fleet.go` 278 ->
281 (new rows, target = size).

Tests:

- Ruling 1, `TestMayorWithoutSessionRefusesTheFleetCap`: with `Session`
  false, past `max_squads` and over cost per day are refused by name and
  not queued; nothing launches. CLI form in
  `TestOperatorStartSquadInsideTheOwnerFleetGrant`: past `max_squads` is
  refused.
- Ruling 2, `TestFleetStatusShowsFleetCap`: a deferred start (session) and
  a declined start (CLI path) give one `waiting` row with
  `wait_reason: fleet_cap`, `cap: max_squads` and the first hold's time;
  the document validates against the fleet golden; after a slot frees and
  `Retry` starts it, the row is gone. CLI form: `status --fleet --json`
  after the refused start names the squad with `"wait_reason":
  "fleet_cap"`. `TestWaitingRoundTripAndValidation`: render and parse round
  trip; a registered squad listed as waiting and a non-cap reason fail
  validation.
- The mayor fixture of `TestMayorInsideTheGrant` and `TestMayorCostAndScope`
  runs as a session (`Session: true`).

Gate: `docs/v3-spike/4-3-evidence/c4-fleet-cap.txt`, PASS; `-race` on the
4-3 CLI tests `c4-race-cli.txt`, PASS. Host note: pgrep at the start of the
gate found one `amq-remote up` process whose root is a Go test temp
directory (`.../TestOwnerScriptDryRun.../.amq-squad/buzz/root`), another
brief's test fixture; no seat-exec, no `amq coop`, no lead session.

Codex pass on bfe61050: no findings had arrived when this commit was made.

## Commit 5: Codex review of bfe61050 (four P1, three P2)

One test row per item; each fails with its fix reverted.

1. P1 1, authorization on the label. `fleet.RepositoryIdentity`
   (`internal/fleet/identity.go`): the checkout is resolved to its canonical
   path (EvalSymlinks) and its identity is derived from the origin remote in
   its own git config (a `.git` directory, or a `.git` gitdir file whose
   commondir holds the config, for a linked worktree; scp, ssh, https and
   git URLs give the last two path segments, `.git` stripped; a local-path
   remote names no hosted repository), else from the identity file
   `.amq-squad/repository` (one line, owner/name, a regular non-symlink
   file). The mayor authorizes that identity, registers it as the squad's
   repository and the canonical path as its project_path; the request's
   label is display only (named in the refusal detail when it differs). No
   identity: refused `repository_unidentified`. `.amq-squad/repository` joins
   `grants.Implicit` so a seat cannot rewrite the identity of its own
   checkout. Test: `TestMayorAuthorizesTheCheckoutNotTheLabel` (label
   owner/app, origin owner/evil: refused, nothing written; an owner/app
   checkout under another label starts and registers as owner/app; no remote
   and no file: unidentified); `TestRepositoryIdentity` (URL forms, the
   worktree gitdir and commondir, the identity file, a symlinked identity
   file refused, a symlinked checkout path resolved).
2. P1 2, capacity read outside the lock. Admission now runs inside one
   `Update` (the registry lock): duplicate name or checkout is
   `squad_registered`, a full fleet records the waiting row and is held, and
   otherwise the squad is registered in that same step, which reserves the
   slot before squad.yaml is written or the roster launched. A failed
   propose, apply or launch releases the slot and is refused
   `launch_failed`. Test: `TestConcurrentStartsTakeOneSlot` (two mayors,
   `max_squads` 1, a 20 ms launcher, started at once: one started, one
   waiting `max_squads`, one registered; one checkout under two names at
   once: one registered, one `squad_registered`; a failing launcher leaves
   the registry empty).
3. P1 3, a cached grant on retry. `Mayor.Grant` is now a source,
   `func() (grants.Resolved, error)`, called on every start and every retry
   (the CLI passes `grants.Load("")`; `fleet.StaticGrant` wraps a fixed
   grant). A grant that fails to load is `grant_invalid`; a grant that no
   longer permits the squad refuses it with the named reason
   (`no_fleet_grant`, `repository_not_permitted`, `repository_unidentified`),
   `Retry` drops it from the queue, and its fleet.yaml waiting row is
   removed (it is no longer waiting on the cap). Test:
   `TestRetryReloadsTheGrant`.
4. P1 4, the registry write by path. The registry is reached through the
   descriptor-anchored helper of 2b-3, lifted into `internal/fsanchor`
   under the lead's rule shared with 4-1: `open_unix.go`, `open_other.go`
   (2b-3's `runtime_open_unix.go` and `runtime_open_other.go` at 3cc7b779,
   package renamed, package-level identifiers and methods capitalized on
   the first letter only, fields and locals unchanged), `walk.go`
   (`WalkAnchored` with the manifest skip as a parameter, `ReadEntry`, the
   package doc). 4-1 confirmed byte-identical copies of all three (SHA-256
   walk.go a0101652...53e2, open_unix.go 7c3cc8e8...793b, open_other.go
   c88170bf...8d60); 4-1's addition is `pi_extra.go`, mine
   `fleet_extra.go` and `fleet_extra_other.go` (`OpenLock`: openat
   O_CREAT|O_NOFOLLOW, never replacing an existing lock, with a retry on
   ENOENT, which Darwin returns to the losers of a concurrent create:
   measured: before the retry, at least 3 of 10 runs of 8 concurrent first
   opens in a fresh directory failed (output truncated), 1 to 7 opens each; after it, 0 failures in
   50 runs; `Path`). `internal/fleet/store.go`: under
   HOME the anchor is HOME and every component below it is opened with
   O_NOFOLLOW; a `--fleet-file` outside HOME is anchored at its deepest
   real (Lstat) ancestor directory. The lock, the read, the temp file, the
   rename and the fsyncs all go through the directory descriptor
   (`WriteAtomic` as lifted, unchanged, per the lead's durable-write
   ruling). Tests: `TestRegistryAnchoredFromHome` (a symlinked
   `~/.amq-squad` above an existing directory is refused for update and
   read, nothing lands outside); `TestSymlinksRefused`;
   `TestOpenLockConcurrentCreate`, `TestOpenLockRefusesSymlinks`
   (internal/fsanchor).
5. P2 5, the shared seen map. Separate duplicate trackers for top-level
   keys, operator keys and the current list item. Test:
   `TestParseRefusesASecondOperatorBlock`.
6. P2 6, confirmed with no code change: the CLI path of `operator
   start-squad` builds a mayor with `Session` false, the same code path
   `TestMayorWithoutSessionRefusesTheFleetCap` drives, and
   `TestOperatorStartSquadInsideTheOwnerFleetGrant` runs the verb itself:
   past `max_squads` it is refused by name and `status --fleet --json`
   then names the squad with `"wait_reason": "fleet_cap"`;
   `TestFleetStatusShowsFleetCap` checks the declined (CLI-path) attempt
   reaches `waiting[]`.
7. P2 7, a missing root read as an empty document. `status --fleet` keeps
   the squad row of a registered root that is gone and attaches the warning
   `root_unavailable: squad <name> root <root>: <error>`
   (`fleet.WarningRootUnavailable`). Test: `TestStatusFleetMissingRootWarns`.

Behavior changes: the repository a squad registers with is the checkout's
derived identity and its project_path the canonical checkout; a failed
launch is the outcome `launch_failed` (refused) with its slot released,
not an error; `grants.Implicit` gains `.amq-squad/repository`; the
fleet.yaml lock and writes no longer follow a symlink anywhere below HOME.

Rows (non-test lines): new rows `internal/fsanchor/open_unix.go` 164,
`open_other.go` 105, `walk.go` 94, `fleet_extra.go` 46,
`fleet_extra_other.go` 27, `internal/fleet/store.go` 121,
`internal/fleet/identity.go` 164; resized new rows
`internal/fleet/mayor.go` 291 -> 385, `internal/fleet/registry.go` 408 ->
334 (Read and Update moved to store.go), `internal/fleet/status.go` 121 ->
125, `internal/cli/fleet.go` 281 -> 289, `internal/grants/grants.go` 329 ->
333.

Gate: `docs/v3-spike/4-3-evidence/c5-codex-review.txt`, PASS (race now
includes internal/fsanchor); `-race` on the 4-3 CLI tests
`c5-race-cli.txt`, PASS. No live seat on the host.

Revert check (lead request, docs commit after d13ded88):
`docs/v3-spike/4-3-evidence/c5-revert-check.txt`. Each P1 fix was undone
in the working tree, one at a time, and only its test rows were run; the
file shows the diff of each revert and the go test output. With the fix
undone, every row fails on its assertion, not on a build error:
- P1 1 (authorize the label): `TestMayorAuthorizesTheCheckoutNotTheLabel`
  got `started` for a permitted label beside an owner/evil checkout.
- P1 2 (no reservation, register after the launch):
  `TestConcurrentStartsTakeOneSlot` got 2 started with `max_squads` 1.
- P1 3 (grant cached per mayor): `TestRetryReloadsTheGrant` started the
  queued squad after its permission was removed.
- P1 4 (bfe61050's path-based Read and Update): `TestRegistryAnchoredFromHome`
  got an update through a symlinked `~/.amq-squad`.
The files were then restored byte for byte, and all four rows pass on the
restored tree. No code change.

At the rebase after 2b-5 is on v3 (lead ruling on durable writes): add the
anchored variant to 2b-5's `internal/atomicfile` in one new file,
`anchored.go` (directory-descriptor-relative CreateTemp, fsync, renameat,
directory fsync), unless 4-1 rebased first and added it, and make
`fsanchor.WriteAtomic` call it. atomicfile is not copied into this branch.

The two stated limits stand: nil launch adapters until 2b-5 and 2b-2; no
cost meter.

## End checkpoint

- Acceptance 1 to 7 at the Go level: 1 `TestSameProgramDifferentGrants`;
  2 `TestFleetStatusValidatesAgainstTheFleetGolden`,
  `TestStatusFleetJSONIsTheFleetDocument`; 3 `TestGateGrouping`,
  `TestOperatorInboxGroupsGatesOfTwoSquads`; 4 `TestMayorInsideTheGrant`,
  `TestOperatorStartSquadInsideTheOwnerFleetGrant`; 5
  `TestMayorAnswerForATypedGateIsRejectedByTheCore`,
  `TestMayorHasNoGateAnsweringPath`; 6 `TestMayorAsk2FallbackProposes`; 7
  `TestLeadHandle`, `TestFleetGoldenIsTheSquadGoldenWrapped`. Reviewer
  ruling rows: `TestMayorWithoutSessionRefusesTheFleetCap`,
  `TestFleetStatusShowsFleetCap`. Codex rows (commit 5):
  `TestMayorAuthorizesTheCheckoutNotTheLabel`, `TestRepositoryIdentity`,
  `TestConcurrentStartsTakeOneSlot`, `TestRetryReloadsTheGrant`,
  `TestRegistryAnchoredFromHome`, `TestOpenLockConcurrentCreate`,
  `TestOpenLockRefusesSymlinks`, `TestParseRefusesASecondOperatorBlock`,
  `TestStatusFleetMissingRootWarns`.
- `internal/cli/operator.go`: 756 -> 499 non-test lines, target 600,
  shrunk. The remainder is `operator answer` and its AMQ send path, 3-2's.
- `internal/cli/global_noc.go`: deleted, renamed into
  `internal/fleet/registry.go`.
- Not in 4-3: the live operator run on the built-in pi (waits for 4-1);
  the squad.yaml and launch adapters (one function each after 2b-5 and
  2b-2); amq-noc's vendored golden refresh for the one `lead_handle` line
  (the lead orders it on 4-4 at merge).
