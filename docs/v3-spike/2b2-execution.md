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
