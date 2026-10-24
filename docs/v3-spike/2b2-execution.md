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
