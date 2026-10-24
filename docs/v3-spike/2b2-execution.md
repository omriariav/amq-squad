# amq-squad 3.0, brief 2b-2 milestone A: execution log

Branch `v3-2b-2`, cut from `v3` at 2aa2baa8. Scope: milestone A of brief 2b-2
(seat-exec, the launch record, the seat identity at launch, the launch
compiler, R48 to R52, the pi launch). Milestone B (the harness-side commit
step, `amq-squad seat commit`) is a separate later run and is not built here.

Every commit passes the standard gate: `go build ./... && go vet ./... &&
go test ./... && scripts/manifest-check.sh --final`, plus `GOOS=linux go
build ./...` and `GOOS=windows go build ./...`.

## Commit 1: AMQ library v0.75.0 -> v0.107.0 (ruling E6, lead ruling 8)

- `go get github.com/avivsinai/agent-message-queue@v0.107.0`, `go mod tidy`.
- Side effects of the bump, both required by the AMQ module: the `go`
  directive moved from 1.25.12 to 1.26.0, `golang.org/x/sys` from v0.47.0
  to v0.49.0.
- Nothing broke: build, vet, the full test suite and manifest-check --final
  passed with no code change.
- `go list -m -versions` shows v0.107.0 as the newest tag, so no tag carries
  PR #1188 (pi adapter) yet; the second bump waits (see open items).
- The installed CLI is amq 0.107.0 (`/opt/homebrew/bin/amq`).

## Commit 2: launch record schema 1 (internal/launch/record.go)

Built:
- `launch.Record` schema 1, the 2b-2 shared contract: identity (`handle`,
  `root` = AM_ROOT, `base_root` = AM_BASE_ROOT, `session` = AM_SESSION),
  `runtime`, `model`, `effort`, `role_path`/`role_sha256`,
  `rules_path`/`rules_sha256`, `branch` (AMQ_SQUAD_BRANCH), `worktree`,
  `amq_path` (R51), `env_needs`, `resume`, `pi_agent_dir`, `launch_nonce`,
  `apply` (outcome, reason code, failure detail: R48) and `exec` (time, pid,
  provider argv, drain proof: R52). The schema number stays 1, so every older
  record still decodes: unknown fields are ignored, and the fields live
  readers still use (cwd, binary, argv, role, team home and profile,
  agent_pid, started_at, stopped_at, tmux) stay.
- `StagedDir(root, handle)` = `<root>/extensions/amq-squad/launch/<handle>`:
  the core stages the record and role.md there before the launch. guard
  already protects `<root>/extensions/amq-squad/` (2a-3 implicit entry), so a
  seat cannot rewrite its own record or role source.
- `Marshal`, `Decode` (refuses a schema newer than 1), `WriteFile` (0600,
  temp file plus rename).

Row retired: `internal/launch/record.go` 536 -> 248 / 250, status `shrunk`
(was `deferred-2b`, class 1 ledger reason quoted in the note: "minimal
launch record; seat-exec writes it").

Behavior changes, named:
1. Launch records are no longer inferred from mailbox activity
   (ScanLegacyEntriesInRoot, ScanRestorableEntriesInRoot and their helpers
   are gone). `state.BuildWithThresholds` scans real records only. A v2
   mailbox with presence or messages but no launch.json no longer appears as
   a restorable agent.
2. `status --json` no longer copies `preauthorized_actions`, `spawn_origin`
   and `spawn_depth` from the launch record (3.0 launches do not write them;
   the profile member's spawn fields still show).
3. The kept-dead fleet registry (global_noc.go) carries
   `external_registration` as raw JSON; the v2 type is gone from the record
   package.

Tests: retired TestScanLegacyEntriesFromPresence, ...FromBaseRootHistory,
...SkipsUnknownBinaryHandle, ...InfersRoleFromBinaryPrefixedHandle,
TestScanRestorableEntriesDedupesLegacyWhenLaunchExists (record_test.go) and
TestStatusJSONSurfacesPreauthorizedActions (status_test.go);
TestExecuteStatusJSONIncludesSpawnMetadata now expects the profile's spawn
values. New (schema1_test.go): TestSchema1SeatFieldsRoundTrip,
TestV2RecordStillDecodes (a 2.x record with dropped fields), 
TestDecodeRefusesNewerSchema, TestStagedDirIsUnderTheGuardedExtension.

## Commit 3: seat-exec (internal/seatexec, `amq-squad seat-exec`)

Built (`internal/seatexec`, fate new; `internal/cli/seat_exec.go` parses the
verb):
- Interface: `amq-squad seat-exec --seat HANDLE [--] PROVIDER ARGS...`. No
  `--root`; any other flag before the provider argv is a usage error. AM_ME
  and AM_ROOT come from the environment; `--seat` must equal AM_ME.
- Anchored traversal (anchor_unix.go): the AMQ root is opened from `/` one
  component at a time with `openat(O_NOFOLLOW|O_DIRECTORY)`, so a symlink
  anywhere in the initial path is refused (`symlink_in_path`). The root and
  every directory below it that seat-exec opens must be owned by the seat's
  user (`wrong_owner`) and not group- or world-writable
  (`dir_writable_by_others`). Files are read with O_NOFOLLOW and published
  with an O_EXCL|O_NOFOLLOW temp file and renameat. This closes the three S4
  prototype limits (S4 report, "seat-exec prototype limits").
- Record and role: the staged record at `<root>/extensions/amq-squad/launch/
  <handle>/launch.json` (written by the core, guarded) must name the seat
  and root and carry all four identity values (`record_incomplete`
  otherwise, R52). role.md's sha256 must equal the record's (`role_mismatch`,
  `role_missing`), and the rules file's sha256 must equal the digest
  recorded at launch (`rules_mismatch`; 2a-3 open question 3). seat-exec
  publishes role.md and launch.json into `agents/<handle>/extensions/
  io.github.omriariav.amq-squad/` (where `context` reads them) and re-hashes
  the published role.md before exec.
- Identity (R52): AM_ME, AM_ROOT, AM_BASE_ROOT, AM_SESSION are set from the
  record only. An inherited value that disagrees is refused
  (`identity_mismatch`), never repaired. Also set: AMQ_SQUAD_SEAT,
  AMQ_SQUAD_ROLE_PATH and _SHA256, AMQ_SQUAD_BRANCH (2a-3 open question 6),
  AMQ_SQUAD_RULES_PATH and _SHA256; for pi seats PI_OFFLINE=1,
  PI_SKIP_VERSION_CHECK=1, PI_TELEMETRY=0 and PI_CODING_AGENT_DIR (plan 4.1).
- Scrub (R16, plan 4.3): every `*_API_KEY`, `*_TOKEN`, `*_SECRET`,
  `*_PASSWORD`, any name with `_SECRET_` inside (AWS_SECRET_ACCESS_KEY), a
  short vendor list, and SSH_AUTH_SOCK / SSH_AGENT_PID (ruling 3). A name in
  the record's `env_needs` (the runtime's declared need) is kept.
- Drain proof (R52): one `amq drain --me H --json` with the seat's final
  environment, run with the record's `amq_path`, in the worktree. When the
  inbox already holds new mail the proof is the read-only `amq list --new`
  instead, so the proof never consumes a message meant for the seat. A
  failed proof refuses the exec (`drain_failed`) and the published record
  carries `exec.outcome: refused`, so the launch never reports the seat up.
- Global agent config (R16, amendment 18): seat-exec fills a missing
  preflight copy at `~/.amq-squad/preflight/claude-settings.json` and
  `codex-config.toml` (the 2b-3 shared contract); it never refreshes an
  existing copy, so a later seat start cannot hide a change. guard now
  denies writes to `~/.claude/settings.json`, `~/.codex/config.toml` and
  `~/.amq-squad/preflight/` as protected paths (hook_guard_env.go).
- pi resume (amendment 23 (a)): for a pi record with `resume`, seat-exec
  refuses (`pi_resume_context_lost`) when the ledger
  `<root>/extensions/amq-squad/ledger/<handle>.jsonl` holds a tool result and
  no `*.jsonl` session file whose name carries the `--session-id` value
  exists under `<pi agent dir>/sessions`. The session-file layout is
  provisional until 4-1 pins the embedded pi.
- Provider: argv[0]'s base name must be the recorded runtime
  (`runtime_mismatch`); the argv is exec'd unchanged with `syscall.Exec`.
- Windows: the package builds; Run returns "unix hosts only" (R45).
- `context` now reports the rules path from the record when
  AMQ_SQUAD_RULES_PATH is unset, and warns "rules changed since launch" when
  the file's digest differs from the one recorded at launch.

Measured: amq 0.107.0 accepts a drain with AM_SESSION unset (it derives the
session from AM_ROOT), so S6 finding A no longer reproduces in its exact
form upstream; it still refuses a conflicting pin with exit 5 ("session
context mismatch"). The real-amq test reproduces the failed drain with a
wrong pin; the unit test reproduces finding A with AM_SESSION removed
against a fake amq that refuses it, as 0.102.0 did.

Tests (internal/seatexec, unix): TestSeatExecSetsIdentityRoleAndExecsInPlace,
TestSeatExecRoleIntegrity (one changed byte; no role.md),
TestSeatExecRulesDigestRecordedAtLaunch, TestSeatExecTraversalRefusals
(symlink in the initial path, group-writable existing directory, wrong
owner, symlinked staged record), TestSeatExecScrubsCredentials (GEMINI_API_KEY,
GH_TOKEN, AWS_SECRET_ACCESS_KEY, SSH_AUTH_SOCK gone; a declared need keeps
only that one), TestSeatExecIdentityFromRecordOnly,
TestSeatExecFailedDrainIsNotReportedUp, TestSeatExecDrainProofNeverConsumesWaitingMail,
TestSeatExecRuntimeMismatch, TestSeatExecPreflightCopy,
TestSeatExecPiResumeRefusal (tool result and no session file refused; no
tool result resumes; session file present resumes), TestScrubbedNames,
TestSeatExecDrainProofWithInstalledAMQ (skips without amq on PATH). In
internal/cli: TestSeatExecTakesOnlySeat, TestSeatExecRefusalExitsOne,
TestContextFlagsRulesDriftFromLaunch, TestSeatWriteToGlobalAgentConfigIsDenied
(acceptance 10). TestCompletionTopCommandsMatchesPublicCatalog lists
seat-exec.

Lines: internal/seatexec 761 (6 files), seat_exec.go 63; hook_guard_env.go
261 -> 269, hook_context.go 182 -> 186, command_registry.go 88 -> 90.

## Commit 4: the launch compiler and runner (internal/seatlaunch, `amq-squad launch`)

Built (`internal/seatlaunch`, fate new; `internal/cli/launch_seats.go`):
- Typed seat list -> one AMQ `PrepareRequestV1` per squad, through the kept
  `internal/launchintent.Compile` shaper. Every participant runs behind the
  wrapper `{executable: <amq-squad>, args: [seat-exec, --seat, <handle>]}`.
  Provider arguments: Claude `--model M --effort E`; Codex `--model M -c
  model_reasoning_effort=E --sandbox workspace-write --ask-for-approval
  on-request`; pi none (amendment 23 (b)). `initial_input` is the role
  pointer only ("Your seat role is in the file named by ...
  AMQ_SQUAD_ROLE_PATH ..."), plus caller text that never carries the role.
- Role delivery (plan 4.3, amendment 2): a seat without role.md text is
  refused; `initial_input` text without role.md is refused as a malformed
  launch; `initial_input` that contains a role line is refused.
- Argument forms (plan 4.3, amendment 20 (b)): after Prepare, every
  provider flag must be in the `allowed_argument_forms` AMQ advertised for
  the provider, and every `-c key=value` in its `config_overrides`; no local
  list of forms.
- R51, temporary until upstream PR #1186 ships: the library resolves its
  AMQ path with `os.Executable()` (v0.107.0 `launchapi/prepare.go:53`), so
  the runner drives Prepare and Apply through the installed CLI: `amq launch
  --request - --prepare --json` and `amq launch --apply - --json`, the
  library types on both sides. The live run shows the pane runs
  `/opt/homebrew/Cellar/amq/0.107.0/bin/amq coop exec ... amq-squad --
  seat-exec --seat cto ...`, and the Codex notify hook is the real amq
  (`-c notify=[".../amq","__codex-notify",...]`). The CLI's stdout and stderr
  go to temp files, not pipes: amq starts a tmux server that inherits the
  descriptors and would hold a pipe open for the server's life (measured:
  the first live run hung in Wait).
- R48: `Report` prints the Prepare outcome and reason, the Apply outcome,
  reason code, disposition, backend and the failure detail (the DTO excludes
  `failure_detail`, so it is taken from the CLI's stderr), and each seat's
  observation. The staged record keeps the apply outcome and the nonce.
- R49: the amq launch subprocess runs without AM_ME, AM_ROOT, AM_BASE_ROOT,
  AM_SESSION and AM_ROOT_ID, so a decision send cannot register the caller
  as live. The launch sends no AMQ message at all: a required action stops
  the launch (exit 10) with the action ids printed.
- R50: next actions name only `amq-squad launch --decision ID=<choices>`
  (and, for a missing session, `amq session create`); never
  `--force-duplicate`.
- R52: a seat is reported up only when seat-exec published an exec block
  with a drain proof after this Apply started; a refusal (for example
  `drain_failed`) is a launch failure with its reason; no record within
  `--wait` is `not_started`.
- Preflight copies of the owner's global agent config are refreshed once
  per launch before Apply (seat-exec only fills a missing one).
- The default launcher is `tmux` (`auto` returned `launcher_not_available`
  outside a tmux client in the live run).
- pi: v0.107.0 has no pi adapter (PR #1188 is not in a tag; `go list -m
  -versions` ends at v0.107.0), and the library's own intent validation
  refuses a pi executable. `PiAdapterInLibrary = false` makes the compiler
  refuse a pi seat with that reason and point at the `amq coop exec`
  fallback; flip it with the bump. The pi launch on the seat-exec side is
  tested with a fake pi (commit 3).

Retired (kept-dead ancestors and their closure):
- `internal/cli/team_launch_launchapi.go` (579) deleted,
  `internal/cli/team_launch.go` (88) deleted, `internal/cli/bootstrap.go`
  (470) and `bootstrap.md` deleted: the compiler and the role pointer
  replace the v2 Prepare/Apply backend and the bootstrap prompt closure.
- `internal/cli/launch.go` 270 -> 138 / 300 (shrunk): the launch record
  write and rollback, `seedRoleStub`, `ensureLauncherExecutable` and the
  dead AMQ version helpers are gone (staging and seat-exec replace them).
- The ancestor's closure in shrunk rows: `model_defaults.go` (183, deleted;
  model resolution moves to the typed seat), `agent_args.go` 199 -> 88
  (composeBinaryArgs family), `agent_defaults.go` 167 -> 53 (the Claude
  preauth composer and the trust-mode child args), `team.go`
  memberEffectiveModel, `amq_env.go` 239 -> 178 (resolveAMQEnvForTeamLaunchProfile,
  freshProjectDefaultAMQBootstrapAllowed).
- `internal/adoptionseam` stays (keep fate). Its Prepare and Apply are now
  unreachable from the binary; they are the in-process path that returns
  when #1186 ships, so this brief does not delete them.

Tests retired (they test deleted code): team_launch_launchapi_test.go and
team_launch_launchapi_real_test.go (whole files), bootstrap_test.go (whole
file; TestActorExecutionContractRejectsRoleAndHandleCaseDrift moved to
actor_execution_test.go), legacy_composer_isolation_test.go (whole file),
TestV228ContractMiscasedProjectRendersOneCanonicalBootstrap, and:
TestComposeBinaryArgsClaudePrecedenceAndRepeatables,
...CodexConfigByKeyAndNativeSingletons, ...CodexConfigMixedAliasesByKey,
...PreservesLiteralDashDashTail, ...LowerLayerDashDashMakesLaterLayersPositional,
...AliasesEqualsAndDanglingFlag, TestMergeBinaryArgsNormalizesTeamAndLaunchLayers,
TestClaudeInScopePreauthAllowlistContents, TestClaudeWorkerPreauthEligible,
TestSeedGenericRoleStubUsesNeutralCatalogText, TestSeedRoleStubCustomRoleFallbackText,
TestRoleFileInlinePathStagesVerbatimDoc, TestEnsureLauncherExecutableAcceptsSymlinkedExecutable,
TestValidateSupportedAMQVersionUses0600Floor, TestEnsureLauncherExecutable,
TestResolveModelUsesAMQSquadConfigBeforeCodexLocal, TestResolveModelUsesCodexProfileConfig,
TestModelArgsForBinary, TestDefaultChildArgsForBinaryWithTrust (and the
withModelLookupRoots helper). TestMain drops the model lookup seams.

Tests new (internal/seatlaunch): TestCompileOneRequestWithTheWrapper,
TestPiSeatShape, TestRoleDeliveryRules, TestArgumentFormsComeFromAMQ,
TestPendingDecisionsStopBeforeApplyAndSendNothing (R49 env, R50 next
actions, stale and out-of-set decisions refused),
TestForcedBackendCreateFailedShowsOutcomeReasonAndDetail (acceptance 6,
R48), TestSeatIsUpOnlyWithItsDrainProof, TestStaleExecRecordNeverReportsUp,
and TestLiveTmuxLaunchWithInstalledAMQ (darwin, AMQ_SQUAD_LIVE=1).
TestCompletionTopCommandsMatchesPublicCatalog lists launch.

### Live tmux launch on darwin with the installed amq

`AMQ_SQUAD_LIVE=1 go test -run Live -v ./internal/seatlaunch/` (skipped by
default and in CI: it needs tmux and amq and starts real panes). Fake
`claude` and `codex` scripts stand in for the providers (they answer
`--version` and the `--help` probes AMQ's adapters run, then record argv and
the environment); HOME, XDG_STATE_HOME (AMQ's trust store) and TMUX_TMPDIR
(a private tmux server, killed at the end) are test directories, so nothing
touches ~/.claude, ~/.codex or ~/.amq-squad. Result, amq 0.107.0, tmux 3.7c:

- first pass: Prepare `action_required` / `untrusted_config_digest`, one
  `trust_confirmation` for cto and dev; the launch stops, nothing staged.
- second pass with `--decision <id>=trust_exact_subject`: Apply `applied`,
  disposition `committed`, backend tmux, panes `%0` and `%1`; both seats
  `up` with drain proof `amq drain`. Apply pass wall time 1.58 s for two
  seats (Prepare, Apply, both seat-execs and drains).
- seat environment: AM_ME, AM_ROOT, AM_BASE_ROOT, AM_SESSION and
  AMQ_SQUAD_BRANCH from the record; GEMINI_API_KEY, GH_TOKEN,
  AWS_SECRET_ACCESS_KEY and SSH_AUTH_SOCK, set in the launching
  environment, absent.
- argv: claude `--model sonnet --effort medium --session-id <nonce>
  '<role pointer>'` (AMQ adds the mint-mode id); codex `--model gpt-6-astra
  -c model_reasoning_effort=medium --sandbox workspace-write
  --ask-for-approval on-request -c notify=[<real amq> __codex-notify ...]`.
- R49: no `lead` mailbox or presence appeared, although the launching
  environment carried AM_ME=lead.

The criterion 4 re-run with the real Claude and Codex providers is not run
here: a real seat writes the owner's ~/.claude and ~/.codex state, which
this run may not touch. It needs the owner's go (open item 1).

## Rebase onto v3 at f0129ebf (3-1 and 2b-1 merged)

The four commits were replayed onto v3 after the 3-1 and 2b-1 merges.
Conflicts and how they were settled: go.mod/go.sum (v3's new btcec and
websocket requirements kept; the AMQ bump re-run with `go get` and `go mod
tidy`); internal/state/state.go and internal/cli/status_test.go (v3's 2b-1
versions kept; they no longer name the dropped scanners or record fields);
internal/cli/hook_context.go and hook_guard_env.go (v3's versions kept, the
2b-2 rules-drift warning and the preflight protected entry re-applied; 3-1's
`grants.Implicit` already protects `~/.claude/settings*.json` and
`~/.codex/config.toml`, so the 2b-2 copy of that list is dropped);
internal/cli/v228_contract_canonical_path_test.go (the bootstrap test removed
again); docs/v3-manifest.yaml (v3's rows kept, the 2b-2 rows re-applied by
script). Every rebased commit passed the full gate.

## Commit 5: the scheduler's SeatLauncher (internal/seatlaunch/launcher.go)

Built against the merged 2b-1 contract (internal/scheduler/hooks.go,
docs/task-store-design.md "Scheduler hooks"):
- `RequestSeat(SeatRequest{ID, Pool, TaskID})`: a `SeatPlanner` (squad.yaml
  and the registry supply it in 2b-5 and 2b-3) turns the request into a
  one-seat squad; the launch runs in the background; the request id goes
  into the staged launch record (`request_id`, new schema 1 field) and the
  seat environment (`AMQ_SQUAD_REQUEST_ID`, set by seat-exec from the record
  only); once the seat proved its drain the launcher posts
  `SeatUp{Handle, Pool, RequestID}`. A repeated or empty id is refused.
- `CancelSeatRequest`: nil only when no seat will join for that id (the
  launch failed, the request is unknown here, or the seat was cancelled and
  closed). A launch still in flight returns an error and is marked: when its
  seat comes up it is closed and no SeatUp is posted. A seat that already
  joined is closed before the cancel is confirmed.
- `CloseSeat(handle, reason)`: SIGTERM to the pid seat-exec recorded (the
  provider runs in seat-exec's process), after checking the pid is alive and
  was not started after the exec (pid reuse); waits up to 5 s; a seat with
  no running provider is already closed. The pane belongs to AMQ (R6).
- Unattended trust: a scheduler launch carries the standing decisions in
  `Options.Decisions`; a request whose Prepare needs an unanswered decision
  fails (and its cancel is confirmed). AMQ's trust store keeps an exact
  trust, so the same seat config needs the owner's trust once.

Tests: TestLauncherPostsSeatUpWithTheRequestID,
TestLauncherCancelInFlightClosesTheSeatAndPostsNothing,
TestLauncherCancelConfirmsFailedAndUnknownRequests,
TestLauncherCloseSeatStopsTheProvider (real child processes; run with
-race); TestSeatExecSetsIdentityRoleAndExecsInPlace checks
AMQ_SQUAD_REQUEST_ID.

## Commit 6: context_resolution.go launch-side cut; seat-exec timing

- `internal/cli/context_resolution.go` 846 -> 783 / 551. Cut (the
  launch-side uses, plan R52: a seat's identity comes from its launch record
  only): the `--root` and `--base-root` inputs of the v2 agent launch, the
  `SkipSessionResolution` project-only path of `resume --last`, and the
  `AllowMalformedEnv` / `explicitContextCanOverrideMalformedEnv` launch
  override (a malformed injected identity is still overridden by explicit
  `--profile` and `--session`, as before). No production caller set any of
  them. Status stays `deferred-2b`, handed to 2b-5 with the remaining lines
  named in the manifest note: the `.amqrc` source (readProjectAMQRCContext
  and the amqrc branches of contextRootMatches, contextBaseRootMatches,
  externalContextRootMatchesProject) and the candidate diagnostics (the
  contextCandidate tuple machinery, selectContextCandidate,
  emitContextDiagnostics, contextDiagnosticLines,
  contextFieldGenuinelyAmbiguous, the malformed-env losing candidates).
  squad.yaml replaces them; 2b-5 owns its schema, so this brief does not
  add a squad.yaml source here. Open ruling 2 below.
- Tests retired: TestExplicitRootOverridesMalformedInjectedIdentityWithWarning,
  TestExplicitExternalRootWinsForNamedProfile (both drove the removed
  `RootFlag`).
- amq.go (417 / 400), amq_send.go (314 / 150), amq_env.go (178 / 150): not
  shrunk further in this commit. What is left is live: amq.go's context and
  namespace helpers (amqSendPassthroughFlagConsumesValue is dead but still
  called by the kept-dead wait_posture.go); amq_send.go's owned send
  boundary (the durable invocation boundary used by the compound-release
  operator answer path and the committed-delivery evidence parser); amq_env.go's
  `amq env` resolution used by status and doctor. Env pinning for seats now
  comes from the launch record (seat-exec), so none of these is on the seat
  path any more. Open ruling 3 below.

Measured (`AMQ_SQUAD_TIMING=1 go test -run WallTime -v ./internal/seatexec/`,
the built binary, the drain proof against the installed amq 0.107.0, a
provider script that exits at once): seat-exec wall time to exec, median of
10 after one warm-up, 21.6 ms (wrapped 24.8 ms minus the provider alone
3.3 ms). Lead ruling 14 proposed warm launch under 60 ms. Most of it is the
`amq drain` subprocess.

## Halfway checkpoint (end of milestone A's core)

- AMQ library: v0.107.0 (the newest tag; no tag carries PR #1188 yet).
  Installed CLI 0.107.0.
- R51 is carried by neither #1186 (still unreleased; v0.107.0's
  `launchapi.Prepare` resolves the AMQ path with `os.Executable()`) nor the
  `coop exec` fallback: the runner drives Prepare and Apply through the
  installed amq CLI, so the panes run the real amq. Recorded as temporary
  until #1186 ships; then the in-process `adoptionseam` path with an
  explicit AMQPath can replace the CLI calls.
- Acceptance:
  1. Criterion 4 from the v3 binary: PASS with fake providers (live tmux run
     above: both seats up through the launch API, model and effort in the
     provider arguments and therefore in the trust digest, seat-exec set
     only environment and role). The re-run with the real Claude and Codex
     is not done: it writes the owner's ~/.claude and ~/.codex state.
  2. Role integrity: PASS (TestSeatExecRoleIntegrity: one changed byte
     refuses `role_mismatch`, no role.md refuses `role_missing`;
     TestRoleDeliveryRules: role text in initial_input without role.md is a
     malformed launch, role text next to role.md is refused too).
  3. Traversal: PASS (symlink in the initial path, group-writable existing
     directory, wrong owner, plus a symlinked staged record).
  4. Scrub: PASS (unit and live; a declared need keeps only that name).
  5. Identity: PASS (four AM_* from the record, unit and live; a failed
     drain is not reported up, unit with AM_SESSION removed against a fake
     amq, real amq with a wrong pin). Finding: amq 0.107.0 no longer refuses
     a drain with AM_SESSION unset.
  6. R48 to R50: PASS (forced backend_create_failed shows outcome, reason
     and detail; the launch subprocess carries no AMQ identity and sends no
     message, live: no lead mailbox; no suggested action names
     --force-duplicate).
  8. pi resume refusal: PASS with a fake pi (seat-exec side). The pi launch
     through `amq launch` waits for a tag with #1188.
- Also done: acceptance 10 (global config writes denied under a test HOME,
  preflight copy at ~/.amq-squad/preflight/<name>); the SeatLauncher
  against the merged 2b-1 contract; seat-exec wall time to exec 21.6 ms
  median.

## Commit 7: identity_incomplete exit code; row owners reassigned

- Reviewer addition (R52, fail closed): amq 0.107.0 accepts a drain with
  AM_SESSION unset, so the drain proof alone no longer catches S6 finding A.
  seat-exec already refused a launch record without one of the four AM_*
  values; the refusal is now `identity_incomplete` and `amq-squad seat-exec`
  exits 14 (`ExitIdentityIncomplete`, internal/cli/exit.go), independent of
  what amq accepts. Test: TestSeatExecIdentityIncompleteExitCode (cli; a
  record without a session exits 14 naming AM_SESSION), and
  TestSeatExecIdentityFromRecordOnly checks the reason code.
- Lead rulings at the halfway checkpoint: (2) context_resolution.go stays
  deferred-2b, handed to 2b-5; (3) amq_send.go, amq_env.go and amq.go keep
  accepted-3 with new owners, recorded in the manifest notes and in the
  briefs document (2b-2, 3-2 and 4-3 "Replaces or deletes"): amq_send.go to
  3-2, amq.go and amq_env.go to 4-3.
- The role-delivery matrix control arm: an unexported `Seat` field puts the
  role text in initial_input instead of the pointer; no squad file or caller
  outside the package can set it, and the compiler's role-delivery checks
  stay on for every other seat.

## Bounded real-provider run: criterion 4 re-run and role-delivery matrix (pointer arm)

Lead ruling at the halfway checkpoint (owner's standing approval, bounded:
one Claude seat and one Codex seat, one short turn each, the S4 request
shape, at most ten minutes of seats, teardown verified by ps; provider
session files are the only owner-side writes besides the preflight copies;
before/after sha256 of the owner's config). Driver: /tmp/2b2run/step.sh;
scrubbed evidence (launch nonce redacted, $HOME as ~, environment names
only, never values) under `docs/v3-spike/2b2-evidence/c4-real/`.

Setup: the built `amq-squad` (from this branch, at /tmp/2b2run/bin, outside
the project) ran `amq-squad launch --seats seats.json`. Project root and
both seat cwds: this worktree, a linked worktree of /Users/omri/code/amq-squad,
which ~/.claude.json and ~/.codex/config.toml already trust, so neither
provider showed a trust prompt and none was answered. `.amqrc` and
`.agent-mail` (both gitignored) were created for the run and removed after.
AMQ's trust store: a temp XDG_STATE_HOME. tmux: a private server
(TMUX_TMPDIR), killed at teardown. Versions: amq 0.107.0, claude 2.1.310,
codex-cli 0.163.0, tmux 3.7c. Seats: c4-claude (claude, sonnet, medium),
c4-codex (codex, gpt-6-astra, medium, `--sandbox workspace-write
--ask-for-approval on-request`), the S4 shape: wrapper `seat-exec --seat
<handle>` only, initial_input = the role pointer plus "Run exactly one shell
command: cat "$AMQ_SQUAD_ROLE_PATH". Then do what it says."; the role.md
holds an acknowledgement token found nowhere else.

Usage window: Prepare at 09:34:01Z, Apply at 09:34:07Z, seats up within the
Apply call, teardown at 09:34:51Z: under one minute of seats, one turn each
(Claude "Crunched for 6s", Codex "Worked for 7s").

| # | Item | Result | Evidence |
| --- | --- | --- | --- |
| 0 | Prepare | exit 10 from amq-squad (action pending): `action_required` / `untrusted_config_digest`, one `trust_confirmation` for c4-claude and c4-codex; nothing staged | `launch-prepare.txt` |
| 1 | Apply with `--decision <id>=trust_exact_subject` | `applied`, disposition `committed`, backend tmux, panes `%0` and `%1`; failure detail none; both seats `up` with drain proof `amq drain` | `launch-apply.txt`, `list-panes.txt`, `launch-record-*.json` |
| 2 | Role acknowledgement through role.md | **PASS** both: `ROLE_ACK c4-claude ACK-1cd1c5` and `ROLE_ACK c4-codex ACK-5c74ae`; each seat ran `cat "$AMQ_SQUAD_ROLE_PATH"` | `pane-0.txt`, `pane-1.txt` |
| 3 | Model and effort in the trust digest | provider argv from the published records: claude `--model sonnet --effort medium --session-id <nonce> <input>`; codex `--model gpt-6-astra -c model_reasoning_effort=medium --sandbox workspace-write ...` (the digest-sensitivity of these fields was measured in S4) | `launch-record-*.json` |
| 4 | seat-exec set only environment and role | seat environment names: AM_ME, AM_ROOT, AM_BASE_ROOT, AM_SESSION, AMQ_SQUAD_SEAT, AMQ_SQUAD_ROLE_PATH, AMQ_SQUAD_ROLE_SHA256 present; strict credential flags 0 for both seats | `env-names-*.txt` |
| 5 | AMQ conversations | both `ready`; execution evidence backend `tmux`; Codex captured through `__codex-notify` | `conversation-*.json`, `execution-*.json` |
| 6 | Owner config | `~/.claude/settings.json` 65fb8dfe… and `~/.codex/config.toml` c67c4a9c… unchanged before/after (codex did not rewrite its config this time); `~/.claude.json` changed (c216fd47… -> 45538fdc…): Claude's own bookkeeping, and the orchestrating Claude session on the same host writes it too, so the change is not attributable to the seat | `hashes-before.txt`, `hashes-after.txt` |
| 7 | Preflight copies (the doctor diff input) | absent before; after the launch `~/.amq-squad/preflight/claude-settings.json` and `codex-config.toml` equal the live files byte for byte (same sha256), so the diff is clean. doctor's parsed diff is 2b-3's and runs on the merged tree later | `hashes-after.txt` |
| 8 | Teardown | `tmux kill-server` on the private server; the two pane processes are gone, no seat-exec or seat process remains | `ps-after-teardown.txt` |

**Verdict: criterion 4 PASSES from the v3 binary on AMQ 0.107.0** (both
seats under the launch API with the wrapper, model and effort as provider
arguments, seat-exec setting only environment and role, both roles
acknowledged, both conversations ready, owner config unchanged).

Finding: the seats inherited the launching process's environment through
the tmux server AMQ started (here the orchestrating Claude Code session:
CLAUDECODE, CLAUDE_CODE_SESSION_ID, CODEX_COMPANION_* and similar names).
No credential name passed the scrub, but a host session's identity
variables reach the seat. In production the launcher is the lead, so this
is the lead's environment. Open item 3.

### Role-delivery matrix (plan section 13, amendment 15)

| runtime | delivery | N | acknowledged | rate |
| --- | --- | --- | --- | --- |
| Claude (sonnet, medium) | role.md pointer in initial_input | 1 | 1 | 1/1 |
| Codex (gpt-6-astra, medium) | role.md pointer in initial_input | 1 | 1 | 1/1 |
| Claude | role text in the first message | 0 | - | not run |
| Codex | role text in the first message | 0 | - | not run |

The control arm (role in the first message) is built (an unexported
compiler field) but not run: the ruling bounds the run to one Claude and
one Codex seat. N=1 per cell does not support a rate; open item 1.

## End checkpoint (milestone A)

- Acceptance 7 (harness-side commit step): not in milestone A; milestone B.
- Acceptance 9 (role-delivery matrix): the pointer arm, N=1 per runtime,
  both acknowledged; the control arm is not run (bound of the ruling).
- Acceptance 10: PASS (TestSeatWriteToGlobalAgentConfigIsDenied; 3-1's
  grants.Implicit protects the global config; preflight copies under
  ~/.amq-squad/preflight/, equal to the live files after the real run).
- Manifest statuses of the Replaces rows:

| row | lines / target | status |
| --- | --- | --- |
| internal/launch/record.go | 250 / 250 | shrunk |
| internal/cli/context_resolution.go | 783 / 551 | deferred-2b, handed to 2b-5 (remaining lines named in the note) |
| internal/cli/team_launch_launchapi.go | - | deleted |
| internal/cli/team_launch.go | - | deleted |
| internal/cli/launch.go | 138 / 300 | shrunk |
| internal/cli/bootstrap.go | - | deleted |
| internal/cli/amq.go | 417 / 400 | accepted-3, owner 4-3 |
| internal/cli/amq_send.go | 314 / 150 | accepted-3, owner 3-2 |
| internal/cli/amq_env.go | 178 / 150 | accepted-3, owner 4-3 |
| internal/cli/model_defaults.go (closure) | - | deleted |
| internal/cli/agent_args.go (closure) | 88 / 200 | shrunk |
| internal/cli/agent_defaults.go (closure) | 53 / 300 | shrunk |

- Launch timings: seat-exec wall time to exec 21.6 ms median of 10 (drain
  proof against the installed amq included); live launches: Apply pass
  1.58 s for two fake-provider seats, the real two-seat Apply returned with
  both seats up inside the 44 s seat window (its own wall time was not
  separated from the provider start).
- manifest-check --final: OK; 3.0 additions 50 files on the branch after
  the rebase (all briefs merged so far).

## Open items and rulings for the lead

1. The role-delivery matrix needs N > 1 and the control arm to give a rate;
   each cell costs seats and provider allowance (an owner ruling on N).
2. PR #1188 (pi adapter) is not in a tag: when one ships, bump go.mod in its
   own commit and flip `PiAdapterInLibrary`; pi seats run on the `amq coop
   exec` fallback until then (not built in 2b-2).
3. Host-session environment reaching seats (finding above): scrub a named
   list of host agent-session variables (CLAUDECODE, CLAUDE_CODE_*,
   CODEX_COMPANION_*) in seat-exec, or leave it to the lead's environment?
4. R51 rides on the installed amq CLI until PR #1186 ships; then the
   in-process `adoptionseam` path with an explicit AMQPath replaces the CLI
   calls (the request and result types are already the library's).
5. Milestone B (seat commit, `amq-squad seat commit --seat H`) codes
   against task.Widen's `file_lock_conflict` refusal (2b-1) as the lead
   noted.

## Commit 10: seat-exec scrubs host agent-session variables

Lead ruling on the real-run finding: a seat must not inherit the launcher's
session identity, and R16's scrub is the place. `seatexec.Scrubbed` now also
drops the host agent-session variables: the prefixes `CLAUDE_CODE_`,
`CODEX_COMPANION_`, `CODEX_SANDBOX`, and the names `CLAUDECODE`, `AI_AGENT`,
`CLAUDE_PID`, `CLAUDE_EFFORT`, `CLAUDE_JOB_DIR`, `CLAUDE_PLUGIN_DATA`,
`CLAUDE_PLUGIN_ROOT`, `CLAUDE_PROJECT_DIR`, `CLAUDE_ENV_FILE`,
`CODEX_THREAD_ID`, `CODEX_SESSION_ID`, `CODEX_INTERNAL_ORIGINATOR_OVERRIDE`
(the names seen in the real run's seat environment plus the documented
session names of the two harnesses). A runtime that declares one in
`env_needs` keeps it, as for credentials. Not scrubbed: `CODEX_HOME`, the
owner's statusline settings, TERM and the AMQ variables AMQ sets in the
pane. Test: TestSeatExecScrubsHostAgentSession (a launcher environment
carrying those names yields seat and drain environments without them).

## Commit 11: fix 1 (Codex review of e0f0906d, items 1 to 9)

Lines refer to the e0f0906d export. One test row per item; each row fails
with its fix reverted in place and passes with the fix restored
(`docs/v3-spike/2b2-evidence/fix1-revert-check.txt`, produced by reverting
each hunk, running the row, restoring, running again). The full gate output
of this commit is `docs/v3-spike/2b2-evidence/fix1-gate.txt`; it ran with
no live seat on the host.

| # | finding | fix | test row |
| --- | --- | --- | --- |
| 1 | launcher.go:87, :127: a timed-out launch whose pane had not started was confirmed cancelled while the provider could still start | a request that AMQ placed (Apply ran, not `not_applied`/`rolled_back`, seat-exec did not refuse) but whose exec record has not arrived is `unresolved`; CancelSeatRequest re-reads the published record (a late exec is closed first, a late refusal confirms) and otherwise confirms only when the pane is gone (read-only `tmux list-panes -a`; no tmux server counts as gone; unknown is not confirmed) | TestLauncherCancelOfAPlacedSeatWaitsForTermination |
| 2 | launcher.go:86, :92: cancelled was set before CloseSeat and its error dropped | a `closing` state; `cancelled` only after a confirmed close; the close error is kept and returned by every CancelSeatRequest retry (the scheduler's closing retry) | TestLauncherCancelledOnlyAfterAConfirmedClose |
| 3 | launcher.go:145: any birth time before Exec.At + 2 s was accepted, and an unavailable start time still signalled | seat-exec records the exact birth time (`exec.process_start`, procinfo, kept across exec); CloseSeat signals only when the live pid's birth time equals it, checked again immediately before the signal; a different birth time means the seat's process is gone (no signal, nil); a missing or unreadable birth time fails closed (no signal, an error, the request stays closing) | TestCloseSeatSignalsOnlyTheRecordedProcess |
| 4 | run.go:279: a record up to one second older than the launch counted, and the request id was never compared | each launch draws a nonce (`launch_id`, new schema 1 field) staged in every record; a seat is up only when its published record carries this launch's id and the seat's request id | TestRelaunchWithinASecondNeedsItsOwnProof |
| 5 | seatexec_unix.go:140: the global-config denial lived only in the guard hook | amendment 28, layered and named: Codex seats: the sandbox is the enforcement, and seat-exec refuses a Codex argv without `--sandbox workspace-write` (or read-only), with a bypass flag, or with a `-c sandbox_mode=` override (`codex_sandbox_missing`); Claude seats: the hook denies, and `seatexec.PreflightDiff` (live config against the preflight copy, a file on one side only counts) is the post-exit check milestone B's seat commit runs at every seat exit. The guard-based acceptance test is retired; acceptance 10 is now these two rows | TestSeatExecRefusesCodexWithoutTheSandbox, TestPreflightDiffCatchesAGlobalConfigWrite |
| 6 | env.go:97: AWS_SHARED_CREDENTIALS_FILE and AWS_CONFIG_FILE passed | indirect credential sources are scrubbed unless declared in env_needs: every `AWS_*_FILE`, GOOGLE_APPLICATION_CREDENTIALS (already), KUBECONFIG, DOCKER_CONFIG, NPM_CONFIG_USERCONFIG, GIT_CONFIG_GLOBAL, CLOUDSDK_CONFIG, AZURE_CONFIG_DIR, GH_CONFIG_DIR, NETRC | TestSeatExecScrubsIndirectCredentialSources |
| 7 | seatexec_unix.go:100, :130: role and rules were hashed before an up-to-20 s proof, and the provider got the source paths | after the identity proof seat-exec re-hashes the staged role, the published role, the rules source and the rules snapshot; the provider gets 0400 snapshots of the verified bytes in the seat's extension directory (`role.md`, `rules.md`; AMQ_SQUAD_RULES_PATH names the snapshot); `context` also warns when the rules source drifts from the recorded digest | TestSeatExecRechecksRoleAndRulesBeforeExec |
| 8 | seatexec_unix.go:208/213: the mail check and the drain could race and lose a message | amendment 28: the proof never drains. R52's "one drain" is now "one read-only identity proof": `amq env --me H --json` with the seat's final environment, whose resolved root, handle and session must equal the record's. Measured on amq 0.107.0 (scratch roots): `amq env` refuses a mismatched AM_SESSION pin with exit 5 like a drain does; `amq list --new` only warns, so it is not a proof; with AM_SESSION unset both accept, which is why the record check (`identity_incomplete`, exit 14) stays | TestIdentityProofNeverDrains |
| 9 | options.go:98: one `<destination>.tmp` across processes | `os.CreateTemp` in the same directory, 0600, then rename; move to internal/atomicfile once 2b-5 is on v3 (to do at that rebase) | TestPreflightCopyConcurrentWriters |

Wording: every "drain proof" in the code, the verb help and this log's
later sections now reads "identity proof"; the published field is
`exec.identity_proof` (was `drain_proof`). Earlier log sections above
describe the drain proof as it was built in commits 3 and 4 and are left as
history.

The three stated limits stand: a provider tool shell that sources the
owner's profile can restore secrets (doctor warns); Claude hooks fail open,
and enforcement does not depend on them (item 5); the wrapper binary is
outside the trust digest, under the owner-controlled install.

## Rebase checklist (open, for the next rebase and milestone B)

- 2b-5 on v3: move the preflight copy writer (seatexec/options.go
  WritePreflight) to internal/atomicfile (fix 1 item 9).
- 4-1 on v3 (from 9fdaa66e on v3-4-1): `amq-squad pi run` no longer execs
  node in place; the launcher stays the parent of the pi process, forwards
  signals, passes pi's exit code through, and turns exit 70 into a seat
  refusal (`runtime_mismatch_in_process`, `seat_closed`, written to the
  ledger). For a pi seat the pid and birth time seat-exec records are the
  launcher's, and CloseSeat signals the launcher. At the rebase: call
  piruntime.Prepare, CheckResume (in place of or beside the 2b-2 pi resume
  refusal), ExitReason and TeamLeadTools from 4-1 instead of 2b-2's own pi
  pieces, and re-run the pi seat-exec tests against them.
- First AMQ tag carrying PR #1188: bump go.mod in its own commit and flip
  seatlaunch.PiAdapterInLibrary.
- PR #1186 released: replace the amq CLI calls of the runner with the
  in-process path and an explicit AMQPath (R51).
- Milestone B: wire seatexec.PreflightDiff into `amq-squad seat commit` at
  every seat exit (amendment 28); pass task.Widen's `file_lock_conflict`
  refusal to the lead on the widening path.
