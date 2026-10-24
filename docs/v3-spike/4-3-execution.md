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
