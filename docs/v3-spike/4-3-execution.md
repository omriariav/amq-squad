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

## Commit 6: Codex second pass on d13ded88, and the operator seat row

Second pass: P1 3, P2 5 and P2 7 were complete; P1 1, P1 2, P1 4 and P2 6
were partial; one new P1 and one new P2 came in. Item 8 below is from 4-5's
end checkpoint. Each item has a test row, and the reverts were run: see
`docs/v3-spike/4-3-evidence/c6-revert-check.txt`. It shows each revert's diff
and go test output. With its fix undone, every row fails on its assertion,
not on a build error. The restored tree passes all of them.

1. P1 1, identity sources (lead ruling: the origin remote is not an
   identity source). `RepositoryIdentity(projectPath, fleet grant)` takes
   only owner-controlled sources:
   - the fleet grant's new `repository_paths` (repository, canonical path,
     optional remote). This is added to the grants schema additively:
     `grants.Fleet.RepositoryPaths`, `grants.RepositoryPath`, and owner-file
     keys `fleet.repository_paths[].repository|path|remote`;
   - the protected `.amq-squad/repository` file.
   The cases:
   - Both sources present and disagreeing: `repository_unidentified`.
   - Neither present: `repository_unidentified`, even when the origin
     names a permitted repository.
   - A `repository_paths` entry that names a remote: an origin that
     differs gives `repository_unidentified`. The comparison is
     `RemoteAddress`, host plus full path with only the transport (scheme,
     user, scp colon) and `.git` stripped. https://evil.example/owner/app.git
     is evil.example/owner/app, and gitlab.example/group/owner/app stays
     whole.
   `identityFromURL` is gone. Rows: `TestOriginRemoteIsNotAnIdentitySource`
   (revert 1a: the origin used when no other source), `TestRepositoryIdentity`
   (revert 1b: over-normalized comparison), `TestMayorAuthorizesTheCheckoutNotTheLabel`,
   and `TestFleetRepositoryPaths` (grants).
2. P1 2, reservations.
   - A reserved row carries `phase` (reserved, launching), `reservation`
     (random id), `incarnation` (the mayor's, random on first use) and
     `expires` (`Mayor.TTL`, default 10 minutes).
   - `Squad.Counts`: a reserved or launching row counts until it expires,
     whatever `Live` says.
   - Admission deletes expired reservations under the registry lock (the
     only release path besides the owning mayor's own cleanup).
   - The row moves reserved, then launching, then running (the reservation
     fields cleared).
   - `fleet remove` refuses a reserved or launching row.
   - fleet.yaml gains the per-squad keys phase, reservation, incarnation,
     expires and live_seats, validated.
   Rows: `TestReservationCountsRegardlessOfLive` (revert 2a: counted by
   Live), `TestExpiredReservationIsReleasedUnderTheLock` (revert 2b: never
   released), and `TestFleetRemoveRefusesAReservation` (CLI; revert 2c).
3. P1 4, the anchor itself. `fsanchor.OpenAnchored` opens base from / one
   component at a time with O_NOFOLLOW and refuses a base that is not
   absolute and clean. Outside HOME, the fleet store anchors at / (with the
   volume on Windows).
   - On macOS this refuses the /var and /tmp symlinks. Tests use canonical
     /private paths (`canonicalTemp`, `realTemp`, `fleetTempDir`).
   - The exact diff went to impl-4-1 for a byte-identical copy:
     open_unix.go SHA-256 3281ecae9922182ae097a4bc3565aae7860c555d828bb0fca5c308f5295219e3;
     open_other.go and walk.go are unchanged.
   Rows: `TestOpenAnchoredRefusesASymlinkedAnchor` (fsanchor) and
   `TestRegistryAnchorIsNotFollowed` (a symlinked HOME; a symlinked
   ancestor outside HOME). Revert: the anchor opened by pathname.
4. P2 6, admission before the adapter check. A full fleet with no
   adapters wired reports `max_squads` and records its waiting row. With
   room, the missing adapters are an error and the reservation is released.
   Row: `TestFullFleetReportsMaxSquadsBeforeAdapters`.
5. New P1, partial launch. `fleet.PartialLaunchError{Live, Err}` is the
   launcher's way to report live seats. The row stays in phase
   `launch_failed_partial` with `live_seats`, counted against max_squads
   until the operator resolves it (`fleet remove` accepts it). The outcome
   is refused `launch_failed_partial`. Row: `TestPartialLaunchKeepsItsSlot`.
6. New P2, cleanup by reservation id. Every phase change and release
   (`setPhase`) finds its row by reservation id, never by name. A stale
   reservation cannot delete or promote another squad's row of the same
   name. Row: `TestLateCleanupDeletesByReservation`.
7. Found while testing item 8 (a defect from commit 1): `fleet add
   --project` and `fleet operator --project` went through the CLI's path
   normalization of every `--project` flag. The AMQ project name became a
   directory path, and `fleet operator` always failed validation. The flags
   are now `--amq-project`.
8. 4-5 end checkpoint, the operator seat row. The fleet document gains
   `operator` (additive, nullable, in `status.NullableFields`):
   - the operator session's seat row (handle `user`, role `operator`),
     collected by `status.OperatorSeat` from the operator's AMQ root in
     fleet.yaml;
   - classified and bucketed by the same rules as any seat;
   - present when the session has left a launch record, presence or
     evidence, `null` otherwise.
   The single-squad document is unchanged, and the fleet golden gains an
   `operator` row. Row: `TestStatusFleetCarriesTheOperatorSeat` (revert:
   the row not set).

Behavior changes:
- Identity comes only from the grant mapping and the identity file.
- Reservation rows and phases are in fleet.yaml, and `fleet remove`
  refuses a reservation.
- The anchor is opened from /, so a symlinked HOME or ancestor is refused.
- The adapter check moved after admission.
- A partial launch keeps its slot.
- `--amq-project` replaces `--project` on `fleet add` and `fleet operator`.
- The fleet document gains `operator`.

Rows (non-test lines, all fate new, target = size):
- `internal/fleet/mayor.go` 385 -> 491
- `internal/fleet/registry.go` 334 -> 394
- `internal/fleet/identity.go` 164 -> 185
- `internal/fleet/store.go` 121 -> 119
- `internal/fsanchor/open_unix.go` 164 -> 185
- `internal/grants/grants.go` 333 -> 347
- `internal/grants/parse.go` 460 -> 501
- `internal/status/fleet.go` 64 -> 95
- `internal/status/compat.go` 110 -> 111
- `internal/cli/fleet.go` 289 -> 299

Gate: `docs/v3-spike/4-3-evidence/c6-codex-second-pass.txt`, PASS;
`-race` on the 4-3 CLI tests: `c6-race-cli.txt`, PASS. No live seat on the
host.

Open for the third pass: a reservation's expiry is not extended during a
launch longer than its TTL (10 minutes). A launch past it can lose its row
to the registry's release, and its promotion then fails with
`errReservationGone`.

## Commit 7: reservation expiry (lead ruling, accepted by the reviewer)

This answers commit 6's open point: a reservation could expire under a long
launch. It lands on its own commit, ahead of the third pass, because Codex
is at its usage limit until 2026-10-31. The third-pass findings, when they
arrive, become the next commit.

Built:
- **Who holds the reservation.** A reservation row records its mayor's
  incarnation: the token, `mayor_pid`, and `mayor_start` (the process start
  time, from `procinfo.StartTime`). It also records `phase_since`.
- **Phases.** `reserved` (admitted), `applying` (squad.yaml through the
  digest flow) and `waiting_seats` (the roster launch). `launching` is gone.
  Each phase change restarts `phase_since`. The window is per phase:
  `fleet.PhaseWindow`, 10 minutes, overridable with `Mayor.PhaseWindow`.
- **Release.** `Registry.ReleaseStale` runs inside the admission update,
  under the registry lock. It releases an in-flight reservation in two
  cases:
  - `incarnation_gone`: the mayor's process is gone. Its pid is dead, or
    the pid is alive with another start time (a reused pid).
    `Mayor.IncarnationAlive` overrides the check for tests.
  - `phase_stalled`: the mayor is alive, but its phase has not advanced
    for one window.

  While the mayor is alive and advancing, the reservation does not expire.
  Each release is recorded in fleet.yaml under `released:` (name,
  reservation, incarnation, phase, reason, at), bounded to the last 20.
- **Counting.** `Squad.Counts` counts every in-flight reservation and every
  partial launch, whatever `Live` says. Stale rows are removed before
  counting. `fleet remove` refuses any in-flight row (`Squad.InFlight`).

Behavior changes:
- Reservation fields: `expires` is replaced by `mayor_pid`, `mayor_start`
  and `phase_since`.
- The phases are `reserved`, `applying` and `waiting_seats`.
- A reservation is never released by elapsed time alone while its mayor is
  alive and advancing.
- fleet.yaml gains a `released:` list.

Rows (non-test lines, fate new, target = size):
- `internal/fleet/mayor.go` 491 -> 527
- `internal/fleet/registry.go` 394 -> 497

Tests (the ruling's three rows):
- `TestReleaseWhenTheIncarnationIsGone`: a dead mayor's reservation one
  minute into its phase is released at the next admission, recorded as
  `incarnation_gone`.
- `TestReleaseWhenThePhaseStalls`: an alive mayor's reservation holds its
  slot one second inside the window, and is released at the window,
  recorded as `phase_stalled`.
- `TestSlowAdvancingLaunchKeepsItsRow`: each phase takes 8 of its 10
  minutes. At 16 minutes after the reservation, a second start under
  `max_squads` 1 still waits, and the slow launch then starts with nothing
  released.

The existing rows `TestReservationCountsRegardlessOfLive`,
`TestFleetRemoveRefusesAReservation` and `TestLateCleanupDeletesByReservation`
are updated to the new fields and phases.

Revert check: `docs/v3-spike/4-3-evidence/c7-revert-check.txt`. Each fix
was undone in place and its row run. With its fix undone, each row fails on
its assertion:
- a gone incarnation treated as alive: the slot was held;
- no phase window: the stuck reservation was never released;
- one window for the whole launch (`phase_since` not restarted): the slow
  launch lost its slot, and the second start began.

The restored tree passes all three.

Gate: `docs/v3-spike/4-3-evidence/c7-reservation-expiry.txt`, PASS;
`-race` on the 4-3 CLI tests: `c7-race-cli.txt`, PASS. No live seat on the
host.

Open: the third Codex pass on identity and reservation. It is blocked by
Codex's usage limit until 2026-10-31, pending the owner's decision on a
Claude reviewer as a recorded deviation.

## Third pass (lead, 2026-10-24)

The lead ran this pass as the final reviewer while Codex was at its usage
limit (amendment 29). It covers 949c8533 plus a29bb152. Verdict: both open
P1 items are fixed completely. One code change follows from the residual
below (unknown liveness is alive), made in the same commit as this section.

P1 1, identity: fixed completely.
- `internal/fleet/identity.go` `RepositoryIdentity` takes identity only from
  two sources:
  - the fleet grant's `repository_paths`, matched on the canonical checkout;
  - the protected `.amq-squad/repository`.
- Two sources that disagree are refused, and so is a checkout with neither.
- The origin is read only to refuse a disagreement with a mapped remote. The
  comparison is `RemoteAddress`: host plus full path, with the transport and
  `.git` stripped.
- Intended strictness: linked worktrees and symlinked checkouts resolve to
  their own canonical path, and each must be mapped or carry the identity
  file.
- The identity file's first writer is the owner. A fresh clone without it is
  unidentified until the owner adds it or maps the path.

P1 2, reservation: fixed completely.
- The `mayor.go` admission does all of this under the one `Update`:
  - releases stale rows;
  - refuses duplicates;
  - counts reservations and partial launches regardless of `Live`;
  - reserves with incarnation, pid, start time and phase.
- `setPhase` finds rows by reservation id only.
- A partial launch keeps a counted row.
- The expiry rule in a29bb152 is as ruled: `incarnation_gone` by pid plus
  start time, and `phase_stalled` per phase window.
- Residual, now fixed in this same commit (reviewer ruling, agreed: unknown
  liveness is alive). Before the fix, `incarnationAlive` returned false when
  `procinfo.StartTime` was unavailable for a foreign pid. A read error then
  released a live mayor's reservation as `incarnation_gone` and admitted a
  squad past the cap, the same shape as 2b-2's CloseSeat loop.
  - The check now returns one of three values (`fleet.Liveness`: alive,
    gone, unknown).
  - A live pid whose start time cannot be read is unknown, and unknown
    counts as alive: the row stays counted.
  - The per-phase stall window is the only path that releases an unknown
    row, and that release is recorded as `liveness_unknown` (not
    `phase_stalled`).
  - Row: `TestUnknownLivenessHoldsUntilTheWindow`. It runs the real
    `incarnationAlive` with the process probes `pidAlive` and `pidStartTime`
    stubbed so that StartTime returns ok=false. The row holds inside the
    window and is released at the window with `liveness_unknown`.
  - Revert check: `docs/v3-spike/4-3-evidence/c8-revert-check.txt`. With an
    unreadable start time read as gone, the reservation is released inside
    the window and a second squad starts past `max_squads` 1. With the
    reason mapping removed, the release is recorded as `phase_stalled`. The
    restored tree passes.
  - Gate: `docs/v3-spike/4-3-evidence/c8-liveness-unknown.txt`, PASS.
  - The `mayor_start` field and `TestReleaseWhenTheIncarnationIsGone`
    (the dead case) are unchanged.
- Recorded: a foreign pid's start time is compared with a 1 s tolerance
  against the recorded `mayor_start`. Our own pid is compared for equality.
- Rows (non-test lines, fate new, target = size): `internal/fleet/mayor.go`
  527 -> 545, `internal/fleet/registry.go` 497 -> 515.

Next: hold for the rebase. 4-3 merges after 2b-2, 2b-5, 2b-3 and 4-1, rebased
onto the v3 tip at that point. The rebase picks up:
- fsanchor from v3, in place of this branch's copy;
- atomicfile `anchored.go`, with `fsanchor.WriteAtomic` calling it;
- the `SquadWriter` and `RosterLauncher` adapters, one function each;
- the golden fixtures with 2b-5's and 4-1's changes.

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
  `TestStatusFleetMissingRootWarns`. Second-pass rows (commit 6):
  `TestOriginRemoteIsNotAnIdentitySource`, `TestRepositoryIdentity`,
  `TestFleetRepositoryPaths`, `TestReservationCountsRegardlessOfLive`,
  `TestExpiredReservationIsReleasedUnderTheLock`,
  `TestFleetRemoveRefusesAReservation`, `TestOpenAnchoredRefusesASymlinkedAnchor`,
  `TestRegistryAnchorIsNotFollowed`, `TestFullFleetReportsMaxSquadsBeforeAdapters`,
  `TestPartialLaunchKeepsItsSlot`, `TestLateCleanupDeletesByReservation`,
  `TestStatusFleetCarriesTheOperatorSeat`.
- `internal/cli/operator.go`: 756 -> 499 non-test lines, target 600,
  shrunk. The remainder is `operator answer` and its AMQ send path, 3-2's.
- `internal/cli/global_noc.go`: deleted, renamed into
  `internal/fleet/registry.go`.
- Not in 4-3: the live operator run on the built-in pi (waits for 4-1);
  the squad.yaml and launch adapters (one function each after 2b-5 and
  2b-2); amq-noc's vendored golden refresh for the one `lead_handle` line
  (the lead orders it on 4-4 at merge).
