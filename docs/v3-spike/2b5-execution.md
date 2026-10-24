# Brief 2b-5 execution log: squad.yaml and the team rows

Branch `v3-2b-5` from `v3` at f0129ebf. Implementer: Opus 5.5. Standard
gate on every commit: `go build ./... && go vet ./... && go test ./... &&
scripts/manifest-check.sh --final`, `GOOS=linux go build ./...`,
`GOOS=windows go build ./...`.

## Lead rulings at the start (2026-10-24)

1. Stay on `v3`. squad.yaml seats are a typed list with the fields of 2b-2's
   `seatlaunch.Seat` (read from `v3-2b-2`, not merged). Argument forms are
   checked against a `launchapi.ProviderCapabilitiesV1` fixture. Acceptance 4
   is recorded as "compiles once 2b-2 merges"; the one-function adapter is a
   follow-up. The files 2b-2 deletes are avoided.
2. The v2 profile model goes; `internal/team` becomes the in-memory form of
   `<repo root>/squad.yaml`, one squad per repository. The namespace profile
   dimension stays as identity; "default" moves to `internal/namespace`. A
   named non-default profile with no squad config gets a named error that
   points at phase 5 `migrate`. Strict parse goes through
   `grants.ParseSquadNarrowing`, every "ignored" narrowing becomes a named
   widening error; guard keeps its lenient reader and the two agree on the
   narrowing. Both are behavior changes.

## Commit 1: squad.yaml schema 1, location, loader and validator

Built:
- `internal/yamlsub/yamlsub.go` (new, 261): the strict YAML subset reader,
  moved out of `internal/grants/parse.go` with exported names, so the owner
  file and squad.yaml share one syntax. `grants/parse.go` 458 -> 227 (new
  row, current_lines updated); behavior unchanged (grants and guard tests
  pass unchanged).
- `internal/team/squad.go` (new, 488): squad.yaml schema 1. `Squad` (session,
  lead, lead_mode, execution_mode, control_root, target_project_root,
  shared_cwd_exception, defaults, seats, the grant narrowing), `Seat`
  (handle, role, runtime, model, effort, worktree, tools, actor_mode),
  `ParseSquad` (strict, against the owner grant), `LoadSquad`,
  `LoadSquadWith`, `SquadPath`, `SeatWorktree`, `Resolved` (defaults
  applied; pi never takes model or effort), `Render` (canonical form, round
  trips), `WidenError` / `ErrWidens`, `ExampleSquad`.
- `docs/squad-yaml.md`: schema, location, writer, narrowing rules, the
  example (a test keeps it equal to `ExampleSquad`).

Rulings applied: location `<repo root>/squad.yaml` (E2); mode narrows auto to
manual, never widens (ruling 2); R37 defaults stay in `grants.Implicit`,
squad.yaml only adds protected paths (ruling 3).

Tests (`internal/team/squad_test.go`):
- acceptance 1: `TestSquadWideningIsANamedParseError`, ten widening cases
  (max_seats above the owner cap, `!` path removal, a removal key, manual to
  auto, privacy widened, runtimes outside the owner list, a seat runtime
  outside the grant, more seats than the cap, owner-only `default_branch`
  and `merge`), each a `WidenError` naming the field.
- acceptance 2: the guard tests in `internal/cli/hook_verbs_test.go`
  (fix9 row included) and `internal/grants` pass unchanged; guard still
  reads through `grants.LoadFrom`, and `TestSquadNarrowingMatchesGuardReader`
  checks that squad check's narrowing equals guard's.
- strict parse: twenty refusal cases, none reported as widening.
- example parse and round trip, doc equality.

Behavior changes: none at runtime yet (no caller of the new loader). A
squad.yaml that `team check` refuses for widening still narrows guard
leniently, as before.

Deviations: none.

## Commit 2: the team model is squad.yaml; team verbs propose, apply, show, check

Built:
- `internal/team/team.go` rewritten: `Team` and `Member` are squad.yaml
  resolved against its repository root (`FromSquad`, `ToSquad`), read with
  `Read` / `ReadFor` / `Exists` / `ExistsFor`, written with `Write` /
  `WithLock` / `WriteUnderLock`, which render squad.yaml and refuse a
  widening field against the owner grant. `ErrNamedProfile` and
  `CheckProfile` refuse a v2 named profile and point at phase 5 `migrate`.
- `internal/team/digest.go` (new, 107): the R8 digest flow, `NewPlan`
  (squad.yaml, team-rules.md, CLAUDE.md and AGENTS.md for the root and each
  existing seat directory, one digest) and `Plan.Apply(digest)`. The format
  is in docs/squad-yaml.md ("The digest").
- squad.yaml gains `operator_interaction` (was `team operator set`).
- `internal/namespace`: `DefaultProfile` and `ValidateProfile` move here
  (namespace imports team); `Paths.ProfileConfig` is the squad.yaml path.
- `internal/cli/team.go` rewritten: `team propose` (from `--from FILE` or
  the flags `--roles --runtime --model --effort --worktree --tools
  --actor-mode --lead --lead-mode --execution-mode --session --control-root
  --target-project-root --operator-interaction --template --rules-prose`),
  `team apply --digest D`, `team show [--json]`, `team check`, `team
  draft-rules`. Bare `team` prints usage. The v2 verbs are usage errors that
  name their replacement.
- `internal/cli/team_rules.go` rewritten for 3.0: rendered from the squad;
  tasks, declared files and the harness commit; the four typed gates;
  `renderTeamRules` kept as the extract; template tables instead of
  switch blocks.
- `internal/cli/roles.go` reduced to the roles a seat may name (catalog and
  staged custom roles).
- Mechanical rewrite of the profile API across 69 files
  (`team.ReadProfile(d, default)` -> `team.Read(d)`, named -> `team.ReadFor`,
  `WriteProfile` -> `Write`, `ProfilePath` -> `SquadPath`, locks,
  `team.DefaultProfile` -> `namespace.DefaultProfile`).

Rows (lines before -> after / target, status):
- internal/cli/team.go 1931 -> 335 / 350, deferred-2b -> shrunk.
- internal/team/team.go 1313 -> 360 / 631, deferred-2b -> shrunk.
- internal/cli/team_rules.go 512 -> 251 / 300, deferred-2b -> shrunk.
- internal/cli/roles.go 146 -> 69 / 80, accepted-3 -> shrunk.
- internal/cli/execution_mode.go 331 -> 301 / 331, shrunk (the v2
  lead-execution posture is gone; the execution and lead-mode fields live in
  squad.yaml).
- internal/cli/init.go 131 -> deleted (dead since 2a: no caller of
  computeInitPlan; manifest reason "legacy wizard/start scaffolding").
- internal/cli/team_operator.go 70 -> deleted (reason "keep gate raise/answer
  and attention projection"; squad.yaml carries operator_interaction).
- internal/cli/effort.go 98 -> deleted (reason "provider-argument assembly is
  R4"; effort is a seat field).
- internal/team/digest.go new (107).
- Touched rows with current_lines refreshed: namespace.go 120 -> 140 (keep),
  workstream.go 183 -> 176 (accepted-3), model_defaults.go 183 -> 186
  (accepted-3, commit 3), squad.go 488 -> 496 (new).

Tests:
- acceptance 3: `TestTeamProposeWritesNothingApplyWritesDigestedFiles`
  (internal/cli/team_test.go): propose leaves the tree byte-identical, the
  digest is deterministic, a stale digest writes nothing, apply with the
  digest writes squad.yaml, team-rules.md, CLAUDE.md (user content kept)
  and AGENTS.md, a second propose shows every file unchanged, check and show
  read it back. `TestTeamProposeFromFileAndRefusals`: `--from` the
  documented example, an unstaged custom role refused, a widening squad.yaml
  refused by name (max_seats against an owner cap of 3), the v2 verbs name
  their replacement.
- acceptance 6: `TestNoV2ProfileModelLeft` (internal/team/team_test.go) walks
  every Go file and fails on any of 22 removed names (ReadProfile,
  WriteProfile, ExistsProfile, ProfilePath, ListProfiles, WithProfileLock,
  WriteProfileUnderLock, ValidateProfileName, DefaultProfile,
  NormalizeForWrite, OperatorConfig, DefaultOperator, DisabledOperator,
  AutonomousPolicy, Composition*, LeadExecution, SchemaVersion, FileName,
  TeamsDirName, EffectiveCapabilities, SupportsOperatorGates).
- internal/team: round trip, missing file and named profile, Write refuses
  widening, actor mode and operator view, effort provider arguments.
- Deleted v2 tests (they pinned removed behavior): internal/team
  profiles_test.go, self_operator_test.go and the old team_test.go;
  internal/cli team_test.go (team init), team_no_session_pin_test.go,
  team_operator_test.go, operator_notifications_config_test.go,
  wizard_test.go, effort_test.go, the team init cases of custom_role_test.go
  (16), profiles_test.go (9), team_rules_draft_test.go (3),
  worktree_isolation_reachability_test.go (3 and the named-profile seed),
  init_test.go (1), output_test.go (1), and five named-profile tests
  (TestOrdinaryTypedOperatorAnswerUsesNamedSelectedQuestionDespiteDefaultRootResidue,
  TestOperatorAnswerNamedProfileUsesExactExplicitSessionRoot,
  TestOperatorAnswerExplicitSessionIgnoresLegacyCrossSessionResidue,
  TestGuardOwnedWaitNamedProfileUsesExactSelectedRoot, the two doctor
  named-profile tests), plus TestOperatorStatusDisabledProfileIsUnconfigured
  and the two bootstrap disabled/custom operator tests.
- Adapted: help, usage-registry, exit-code and removed-verb tests name the
  new verbs; the team-rules tests follow the 3.0 wording; role-draft tests
  take the drafter from the global config; compound release, dispatch and
  context tests run on the default profile.

Behavior changes:
1. One squad per repository at `<repo root>/squad.yaml`; `.amq-squad/team.json`
   and `.amq-squad/teams/<name>.json` are no longer read or written. A named
   profile has no squad config: commands that need one fail with
   `ErrNamedProfile` ("migrate a v2 profile with amq-squad migrate (phase
   5)"). The namespace profile dimension stays (IDs, `.agent-mail/<profile>/`
   roots). (Lead ruling 2.)
2. `team init`, `team rules`, `team sync`, `team operator` and `team resume`
   are usage errors naming their replacement; the top-level `init` digest
   code (dead) is deleted.
3. squad.yaml has no drafter key: drafting uses the owner's global config
   (`~/.amq-squad/config.json` or `AMQ_SQUAD_CONFIG`) else in-session.
4. The operator is always the owner's mailbox `user`; "operator disabled"
   and custom operator handles are gone (3.0 typed gates need the owner).
5. Seats keep squad.yaml order (no catalog reordering) in briefs, dispatch,
   doctor and launch.
6. squad.yaml has one session; every seat carries it (per-member sessions
   and the deprecated workstream pin are gone; the squad session is tier 2
   of workstream resolution, without a notice).
7. Dropped v2 seat fields: tool_config, tool overlays, launcher, claude_args
   and codex_args, permission_allowlist, spawn origin and depth, trust,
   binary_args, autonomous composition, max_spawn_depth, lead_execution,
   target_contract. Effort travels as a provider argument
   (`--effort` / `-c model_reasoning_effort=`) on the v3 launch path until
   2b-2's compiler replaces it.
8. team-rules.md is rendered from squad.yaml with 3.0 wording (tasks and
   declared files, the harness commit, the four typed gates) and no longer
   no-clobber: propose shows a replace and apply writes it.
9. `doctor --project` takes a literal path (no `~` expansion; the shell
   expands it).
10. `release plan` canonicalizes the project with the shared resolver.

Deviations:
- The R8 digest format lives in `internal/team/digest.go` (the core), so the
  4-1 pi lead proposes through the same function; this also keeps
  `internal/cli/team.go` under its target.
- `team_rules_draft.go` (296 / 150), `role_draft.go`, `model_defaults.go`,
  `agent_catalog.go` and `context_resolution.go` are commit 3.
- Test hygiene: the cli TestMain and the worktreeplan team fixture point
  HOME at a temp directory, because every squad.yaml write now reads the
  owner grants file; no test reads the real owner's file.

## Commit 3: drafting, model, context and argument forms around squad.yaml

Built:
- `internal/team/launch.go` (new, 96): `Squad.LaunchSeats(repoRoot)` (the
  typed seat list with the fields of 2b-2's `seatlaunch.Seat`, read from
  `v3-2b-2` at 91c7df54), `ProviderArgs` (model and effort for Claude and
  Codex, Codex sandbox and approval flags, none for pi) and
  `CheckArgumentForms` against the AMQ advertisement
  (`launchapi.ProviderCapabilitiesV1.AllowedArgumentForms` and
  `ConfigOverrides`); no local form list (amendment 20 (b)).
- `internal/drafter/text.go` (new, 83): the draft-text checks shared by the
  three drafting consumers (NormalizeText, MatchRoleBullets, NonEmptyLines,
  NormalizeBullet, AllBullets), moved out of cli/team_rules_draft.go.
- `internal/cli/team_rules_draft.go`: the one drafter seam (`runCLIDraft`
  without a per-team config, `cliDraft` with JSON tags, `writeManualDraft`)
  and the team-rules fragment drafting and validation.
- `internal/cli/role_draft.go`: `role draft <id> --purpose TEXT [--project]
  [--json]` drafts a plain role prompt (no frontmatter, binary, peers or
  label; runtime, model and effort are squad.yaml seat fields) through the
  shared seam and stages it with an exclusive create.
- `internal/cli/model_defaults.go`: the seat model, else the owner default
  from the amq-squad config; no native argument parsing and no read of
  `~/.codex/config.toml`.
- `internal/cli/context_resolution.go`: flags, then the injected AM_*
  identity (only when it is the selected tuple), then the documented
  defaults with the squad.yaml session. The project `.amqrc` source, the
  candidate list, tuple splicing machinery and the candidate diagnostics
  are gone; `emitContextDiagnostics` prints the warnings. Two different
  explicit values for one field (for example `--profile` and a profile
  inferred from `--root`) are still an ambiguity error.
- `internal/cli/execution_mode.go`: the execution-mode constants are
  squad.yaml's (`team.ExecutionMode*`); the dead lead-mode helpers go.
- Stale v2 wording fixed (lead ask 1): role/role.go, catalog/catalog.go,
  cli/json.go, flock/flock.go, namespace.go, the ErrNamedProfile text. A grep
  for the v2 verbs in non-test internal/ code now finds the removal note in
  cli/team.go and the package doc of team/team.go only.

Rows (lines before -> after / target, status):
- internal/cli/team_rules_draft.go 296 -> 147 / 150, deferred-2b -> shrunk.
- internal/cli/role_draft.go 494 -> 150 / 150, accepted-3 -> shrunk.
- internal/cli/model_defaults.go 186 -> 84 / 130, accepted-3 -> shrunk.
- internal/cli/context_resolution.go 846 -> 368 / 551, deferred-2b -> shrunk.
- internal/cli/agent_catalog.go 145 -> deleted (dead: its only caller was
  effort.go's team init flag; reason "provider-argument assembly is R4").
- internal/cli/execution_mode.go 301 -> 282 / 331, shrunk.
- internal/cli/briefs.go 168 -> 169 / 180, shrunk (one import line).
- internal/team/launch.go new (96); internal/drafter/text.go new (83).

Tests (full output: docs/v3-spike/2b5-evidence/c3-go-test.txt, manifest:
c3-manifest-check.txt):
- acceptance 4: `TestSquadCompilesToOneLaunchSeatList` (internal/team): a
  squad.yaml with one Claude, one Codex and one pi seat compiles to one
  typed seat list; the pi seat has no provider argument; the advertised
  fixture admits every form. "Compiles once 2b-2 merges" (lead ruling 1):
  the adapter from `LaunchSeat` to `seatlaunch.Seat` (role.md text for Role)
  is a one-function follow-up; this branch does not import seatlaunch.
- acceptance 5: `TestArgumentFormsComeFromTheAdvertisement`: a form the
  fixture advertisement does not list (`--effort` for Claude) is refused,
  so is a `-c` override outside the advertised values, so is a provider with
  no advertisement; pi needs none.
- role draft: rewritten test file (manual prompt, chain fallback evidence,
  fail-closed error evidence, staged document with 0600 and JSON evidence,
  refusal before the drafter over an existing file, invalid output not
  staged, validator cases, bad ids).
- context: the candidate and `.amqrc` assertions become source assertions;
  a "squad.yaml session" case replaces the "project amqrc" case;
  `TestResolveAMQContextUsesCanonicalCustomRoot` (custom `.amqrc` base
  root) is deleted with the source.
- model: `TestResolveModelUsesCodexProfileConfig` is deleted; the owner
  default test asserts that no Codex config is read.
- agent_catalog_test.go deleted with its file.

Behavior changes:
11. A project `.amqrc` is no longer a context source for profile, session or
    roots (2a-2 ruling 8: squad.yaml replaces it). AMQ itself still reads
    it. The candidate diagnostics (`warning: context <field> candidates:
    ...`) are gone; only real warnings print.
12. `role draft` drops `--binary`, `--peers`, `--label`, `--profile`,
    `--session` and the brief context; the staged role is a plain prompt
    starting `# Role: <id>`; session and branch neutrality checks go with the
    brief context (task id and version checks stay).
13. The model default no longer reads `~/.codex/config.toml` or native
    `--model` arguments; with no seat or owner model, the provider's own
    config decides.
14. Drafting has one config layer: the owner's global config, else
    in-session.

Open points:
- `internal/state/snapshot.go` `teamProfile` (lead ask 2) is the namespace
  profile copied from `launch.Record.TeamProfile`, not the v2 team profile
  model; the launch record is 2b-2's schema and the snapshot rows are the
  status rows (2b-1 / 2b-4). Left as is.
- The full test output is kept from commit 3 on (lead ask 3); commits 1 and
  2 predate the rule and their gate output was not kept.

Deviations:
- The shared draft-text checks moved to a new row in `internal/drafter`
  (the drafting package) rather than staying in team_rules_draft.go; they
  serve brief, team-rules and role drafting alike. Named here because a
  move into a new row lowers the counted total.

## End checkpoint

Gate on 8eb5e564: green (docs/v3-spike/2b5-evidence/end-go-test.txt,
end-manifest-check.txt; linux and windows builds; `go test -race` clean on
team, grants, drafter, namespace).

Acceptance:
1. Widening is a named parse error: `TestSquadWideningIsANamedParseError`
   (internal/team/squad_test.go), ten cases. Pass.
2. The 2a-3 guard tests for squad.yaml narrowing (fix9 row included) pass
   unchanged; `TestSquadNarrowingMatchesGuardReader`. Pass.
3. `team propose` prints one digest and writes nothing; `team apply
   --digest` writes: `TestTeamProposeWritesNothingApplyWritesDigestedFiles`
   (internal/cli/team_test.go). Pass.
4. One Claude, one Codex and one pi seat compile to one typed seat list,
   pi with no provider arguments: `TestSquadCompilesToOneLaunchSeatList`.
   Pass on this branch; "compiles once 2b-2 merges" (the seatlaunch adapter
   is the follow-up, lead ruling 1).
5. A form the fixture advertisement does not list is refused:
   `TestArgumentFormsComeFromTheAdvertisement`. Pass.
6. No reference to the v2 profile model: `TestNoV2ProfileModelLeft`
   (22 removed names across every Go file). Pass.

The four deferred-2b rows (before -> after / target):
- internal/cli/team.go 1931 -> 335 / 350, shrunk.
- internal/team/team.go 1313 -> 360 / 631, shrunk.
- internal/cli/team_rules.go 512 -> 251 / 300, shrunk.
- internal/cli/team_rules_draft.go 296 -> 147 / 150, shrunk.
Also owned: context_resolution.go 846 -> 368 / 551 shrunk; role_draft.go
494 -> 150 / 150 shrunk; roles.go 146 -> 69 / 80 shrunk; model_defaults.go
186 -> 84 / 130 shrunk; agent_catalog.go 145 deleted; execution_mode.go
331 -> 282 / 331 shrunk; init.go, team_operator.go, effort.go deleted. New:
team/squad.go, team/digest.go, team/launch.go, yamlsub/yamlsub.go,
drafter/text.go. Class 1 (deferred-2b) total: 6300 lines / 3468 over at the
start of 2b, 1394 lines / 544 over now (the rows left are other briefs':
launch/record.go, doctor.go).

Removed team verbs: `team init`, `team rules` (`init`, `show`,
`templates`), `team sync`, `team operator set`, and the `team resume`
redirect; the top-level `init` digest code. Each v2 verb is a usage error
naming its replacement. The 3.0 verbs: `team propose`, `team apply
--digest`, `team show`, `team check`, `team draft-rules`; bare `team` prints
usage.

Follow-ups:
- After 2b-2 merges: the one-function adapter `LaunchSeat` ->
  `seatlaunch.Seat` (role.md text from the staged custom role or the
  catalog stub) and one ProviderArgs/CheckArgumentForms (2b-2's or this
  branch's, not both); `model_defaults.go` and `team_launch_launchapi.go`
  are deleted by 2b-2.
- `internal/role/parse.go` (`ParseDocument`) has no caller left after the
  role draft rewrite; its row is not this brief's.

## Fix commit 1: drafter/text.go is a split row, counted in the band

Reviewer ruling on the commit 3 deviation: a fate-new row may hold only code
new in 3.0. Option (a) taken: `internal/drafter/text.go` is now fate shrink,
status shrunk, cluster C18, a split from `internal/cli/team_rules_draft.go`
(shrunk 296 -> 147), with a note naming the five moved functions
(normalizeDraftText, matchRoleBullets, nonEmptyTrimmedLines,
normalizeMarkdownBulletMarker, allMarkdownBullets). Both rows are counted.
target_lines 83 is the moved size and is added to the target sum.

manifest-check --final before -> after:
- gate total within band: 31354 -> 31437 (+83), MET against 29790..36410.
- target sum: 35087 -> 35170.
- 3.0 additions: 43 files / 9549 lines -> 42 files / 9466 lines.

Gate rerun green; full output in docs/v3-spike/2b5-evidence/fix1-go-test.txt
and fix1-manifest-check.txt. No code change. The commit 3 band figure in
this log (31354) is superseded by 31437.

## Fix commit 2: the Codex pass on 7f93a5f2

One test per finding. Each test was checked to fail with only its own fix
undone (docs/v3-spike/2b5-evidence/fix2-revert-check.txt: a semantic revert
in place, or the 7f93a5f2 file put back; a build failure is never counted as
a pin).

P1:
1. Partial apply. New `internal/atomicfile` (new, 124 + 17 + 8):
   `Tx.Stage` writes every output to a random temp beside its target, which
   proves each directory writable before anything changes; `Tx.Commit(check)`
   runs the preconditions with everything staged, then renames in order and,
   on a failed rename, restores every file already replaced (old content and
   mode) or removes the ones it created. `team.Plan.Apply` stages squad.yaml,
   team-rules.md and every changed pointer file, checks each against its
   plan-time content (`rules.Verify` for the pointer files), then commits.
   `rules.Apply` uses the same transaction (`rules.Stage`, `rules.Verify`).
   Tests: `TestPlanApplyIsAllOrNothing` (internal/team: a seat directory
   made unwritable after the proposal fails the apply with squad.yaml,
   team-rules.md, CLAUDE.md and AGENTS.md untouched; a file edited after the
   proposal refuses the apply), `TestCommitRollsBackCompletedRenames` and
   `TestCommitCheckRunsBeforeAnyRename` (internal/atomicfile).
2. Predictable temp names. `team.WriteFileAtomic` writes through
   `atomicfile.Write` (os.CreateTemp, O_EXCL with a random name, chmod,
   fsync, rename, directory fsync). The gate store's `writeAtomic` and the
   rules package's `atomicWriteFile` now use the same writer instead of
   their own copies (rules' copy is deleted; gatestore keeps only
   mkdirDurable); gatestore's syncdir files moved to atomicfile.
   Tests: `TestWriteDoesNotFollowAPlantedTempName` (internal/team: a
   symlink at the old `<squad.yaml>.<pid>.tmp` name pointing at an owner
   grants file stays untouched) and `TestWriteNeverFollowsAPlantedTempName`
   (internal/atomicfile).
3. YAML read as literal paths. yamlsub splits flow lists on commas outside
   quotes, and refuses an item or scalar starting with an anchor, alias,
   tag, flow map, nested list or block scalar marker (& * ! { [ | > % @ and
   the backquote) as unsupported syntax, in flow and block lists. Tests:
   `TestFlowListSplitsOutsideQuotesOnly`, `TestUnsupportedYAMLIsRefused`
   (internal/yamlsub); the quoted-comma and alias cases in
   `TestSquadNarrowingRefusesSecondDocumentAndRepeatedKeys` (grants).

P2:
4. Multi-document and repeated keys. A document marker (`---`, `...`) is an
   error in strict and lenient mode; `grants.ParseSquadNarrowing` refuses a
   repeated grant key, and protected paths only accumulate. guard therefore
   denies `grants_invalid` on such a squad.yaml (fail closed). Tests:
   `TestDocumentMarkerIsAnErrorInBothModes` (yamlsub),
   `TestSquadNarrowingRefusesSecondDocumentAndRepeatedKeys` (grants).
5. Lead identity. `Team.LeadHandle` carries the lead seat's handle;
   `team.IsLead` compares by handle (by role only for a hand-built Team
   without one); `EffectiveActorMode`, `ToSquad`, the execution contract and
   the orchestration rules use it. Test: `TestLeadHandleSurvivesASharedRole`.
6. Escaping. One rule on both sides: `yamlsub.Quote` renders a Go string
   literal when quoting is needed, `yamlsub.Unquote` reads a double-quoted
   value with `strconv.Unquote` and a single-quoted value with a doubled
   single quote for a quote; `stripComment` honours escapes. `team.Render`
   uses `Quote`. Tests: `TestQuoteUnquoteRoundTrip` (yamlsub),
   `TestRenderRoundTripsQuotesAndBackslashes` (team: `a"b`, `c\d`, `e"f`,
   `it's` and a worktree with a quote and a backslash round-trip exactly).

Rows: yamlsub.go 261 -> 339 (new), grants/parse.go 227 -> 239 (new),
team/team.go 360 -> 372 / 631 (shrunk), team/digest.go 107 -> 130 (new),
rules/rules.go 301 -> 279 (keep), gatestore/store.go 737 -> 721 (new),
gatestore/syncdir_unix.go and syncdir_windows.go deleted (fate delete:
moved to atomicfile), atomicfile.go, syncdir_unix.go and syncdir_windows.go
new. Band total 31437 -> 31427, MET.

Behavior changes:
15. A squad.yaml or owner grants file with a document marker, a repeated
    grant key, an anchor, alias, tag, flow map or nested list is a parse
    error (guard: grants_invalid). A double-quoted value is read with Go
    string escapes.
16. `team apply` and `rules.Apply` are all or nothing.

Gate: green (fix2-go-test.txt, fix2-manifest-check.txt; race clean on
atomicfile, team, gatestore, rules, grants, yamlsub: fix2-race.txt). The
first fix2 run failed once on `TestRealPTYWaitPostureRefusesNamedGateBeforeTimeout`
(a real-PTY wait test with a 9 s deadline under full-suite load); it passed
alone in 17.7 s and in the rerun. That output is kept as
fix2-run1-pty-flake-go-test.txt. Neither the test nor its file is touched by
this brief.

## Fix commit 3: the second review pass on 82349b6e

Seven items, one test each. Each test fails with only its own fix undone in
place (docs/v3-spike/2b5-evidence/fix3-revert-check.txt; none counted on a
build failure).

1. `rules.Verify` checks every planned file, the ones the plan found up to
   date included, so an edit between propose and apply refuses the apply.
   Test: `TestVerifyCoversUnchangedPlans` (internal/rules).
2. yamlsub refuses a plain item or scalar that contains `: ` or ends in `:`
   (a mapping), in flow and block lists; a quoted colon is a value. Test:
   `TestMappingItemsAreRefused` (internal/yamlsub).
3. A repeated top-level key is an error in both modes (amendment 27). Test:
   `TestRepeatedKeysAreAnErrorInBothModes`.
4. `---` or `...` followed by any whitespace or the end of the line is a
   document marker, checked before the strict tab check. Test:
   `TestDocumentMarkerFollowedByATab`.
5. The rollback fsyncs each directory it changed once. Test:
   `TestRollbackFsyncsTheDirectory` (internal/atomicfile).
6. `Tx.Stage` aborts the transaction on a read failure as on a write
   failure; a failed restore removes its temp; `team.Plan.Apply` defers
   `tx.Abort()`. Test: `TestFailuresLeaveNoTemps`.
7. Temp files are named `atomicfile.TempPrefix` (`.tmp-`) + target name +
   `-<random>` in the target directory; the gate store's sweep matches
   `atomicfile.TempPrefix`. Test: `TestTempNamesUseTheSharedPrefix`.

The atomicfile package comment now says that the rollback is best effort and
reported as joined errors, and that a symlinked parent directory is the
accepted same-UID limit until the descriptor-anchored move at the 2b-2
rebase.

Rows: atomicfile.go 124 -> 148 (new), yamlsub.go 339 -> 347 (new),
rules.go 279 -> 278 (keep), digest.go 130 -> 131 (new), gatestore/store.go
721 (new). Band total 31426, MET.

Behavior change 17: a repeated top-level key in squad.yaml is a parse error
for guard too (grants_invalid), as is a mapping where a value belongs.

Gate: green (fix3-go-test.txt, fix3-manifest-check.txt; race clean on
atomicfile, team, gatestore, rules, grants, yamlsub: fix3-race.txt).

## Rebase onto v3 after the 2b-2 milestone A merge

v3-2b-5 rebased from f0129ebf onto the v3 tip 2c7a38e6 (2b-2 merge
f4d3dc13). Conflicts and how they were resolved:
- 2b-2 deleted `model_defaults.go`, `team_launch.go`,
  `team_launch_launchapi.go` (and its test), `bootstrap.go`, `bootstrap.md`
  and `bootstrap_test.go`: taken as deleted. Nothing of them moved into
  squad.go; this branch's model-default rewrite (commit 3) and its trust
  model tests go with the file. `custom_role_test.go` is deleted: what was
  left of it tested `seedRoleStub`, which 2b-2 deleted.
- `agent_defaults.go`, `amq_env.go`, `launch.go`: 2b-2's side (the launch
  helpers this branch had edited are gone).
- `context_resolution.go`: this branch's resolver, reduced to 2b-2's
  option set (no `--root` / `--base-root` inputs, no project-only mode, no
  malformed-environment override beyond `--profile` plus `--session`);
  `pickFlag` goes with the root inputs. 368 -> 302 lines; 2b-2's own tests
  for the removed inputs stay removed.
- `testmain_test.go`: the HOME isolation stays; the model lookup seams went
  with model_defaults.go.
- manifest: 2b-2's deleted rows kept; the context_resolution row is shrunk
  with a note naming both steps (2b-2 846 -> 783, 2b-5 -> 302 / 551).

After the rebase (this commit):
- `internal/team/launch.go` is the one-function adapter:
  `Squad.LaunchSeats(repoRoot, roleText)` returns `[]seatlaunch.Seat`. The
  team package's own ProviderArgs, CheckArgumentForms and CodexSandboxArgs
  are deleted; 2b-2's are the only copy. `Member.ExtraArgs` (the third copy
  of the effort arguments, used only by the deleted v2 launch) is deleted
  with its test.
- `seatexec.WritePreflight` writes its copies through `atomicfile.Write`
  (2b-2's rebase checklist).
- Acceptance 4 now compiles against 2b-2: `TestSquadCompilesToOneLaunchRequest`
  normalizes the three-runtime squad with `seatlaunch.Squad.Normalize`, checks
  the pi seat has no provider argument and the fixture admits every form.
  `seatlaunch.Compile` refuses the pi seat by name because the pinned AMQ
  library has no pi launch adapter yet (`seatlaunch.PiAdapterInLibrary`,
  upstream PR #1188); the Claude and Codex seats compile to one launch
  request that validates. The test compiles all three in one request when
  that constant flips. Acceptance 5 runs through
  `seatlaunch.CheckArgumentForms`.

Rows: team/launch.go 96 -> 29 (new), team/team.go 372 -> 354 / 631
(shrunk), seatexec/options.go 184 -> 164 (2b-2's row).

Gate on the rebased tree: green
(docs/v3-spike/2b5-evidence/rebase-gate.txt: go test and manifest-check
--final; linux and windows builds). Race clean on team, grants, atomicfile,
seatexec (rebase-race.txt). manifest-check prints the band total 29426, below the
29790 floor, as NOT MET accepted by the end-of-2a-2 decision marker: with
2b-2's deletions and this branch's, the counted tree is now smaller than
the band's lower edge. That is a band question for the lead, not a violation.

## Manifest check 8 and the retired floor (2b-5 merge ruling)

Ruling (reviewer, agreed by the lead): the band's floor is retired at this
merge, the ceiling stays, and a new check takes the floor's place.

- `scripts/manifest-check.sh --final` check 8: a row retired from 2b on
  (status `deleted`, or `shrunk` with current_lines below half its
  target_lines) must carry `replaced_by`: the path of a fate-new row of the
  manifest that is present in the tree, or `"<brief> acceptance <n>"`. A row
  counts as from 2b on when its note names a 2b, 3 or 4 brief (`2b-N`,
  `3-N`, `4-N`) or when it carries `retired_in`. I used both: the note where
  it already names the brief (the 2b-2 and 2b-5 rows), and `retired_in` for
  the 2b-1 rows, whose notes name none. Rows retired in 2a-1 and 2a-2 name no
  such brief and are exempt.
- Check 5 is now the ceiling only: `v2 rows kept: 29,426, ceiling 36,410`,
  with no floor and no NOT MET wording; an overrun is still a --final
  violation unless the end-of-2a-2 marker accepts it. Beside it:
  `3.0 additions: 55 files, 11,926 lines` and `size of 3.0: 41,352 lines`.
- The script header and the manifest header document `replaced_by` and
  `retired_in`.

replaced_by filled (21 rows):
- 2b-1 (retired_in 2b-1): cli/evidence.go -> internal/cli/hook_evidence_list.go;
  cli/task_completion_evidence.go -> "2b-1 acceptance 7"; cli/task_selection.go
  -> "2b-1 acceptance 8"; state/external_evidence.go -> internal/status/classify.go;
  task/command_evidence.go, completion_reconcile.go, leadership.go,
  lifecycle.go, lifecycle_event.go, reconcile.go -> internal/task/lease.go.
- 2b-2: team_launch_launchapi.go, bootstrap.go, model_defaults.go ->
  internal/seatlaunch/compile.go; team_launch.go and launch.go (shrunk 138 /
  300, below half) -> internal/seatlaunch/run.go.
- 2b-5: agent_catalog.go, team_operator.go, effort.go -> internal/team/squad.go;
  init.go -> internal/team/digest.go; gatestore/syncdir_unix.go and
  syncdir_windows.go -> the atomicfile files of the same name.
- Not qualifying: context_resolution.go (302 / 551, above half);
  custom_role_test.go and bootstrap.md (not Go rows).

Test: `TestManifestCheckRequiresReplacedByFor2bRetirements`
(scripts/manifest_check_test.go, a Go test so the standard gate runs it). A
fixture root with the script, three files and a manifest: a 2b retired row
without replaced_by fails (named through the note and through retired_in),
with a fate-new path or an acceptance item it passes, with a non-new row or
free text it fails, a 2a row is exempt, and the report prints the ceiling
line, the additions and the size of 3.0 with no NOT MET. With check 8
undone the test fails (2b5-evidence/check8-revert-check.txt).

Gate green: 2b5-evidence/check8-go-test.txt, check8-manifest-check.txt.
