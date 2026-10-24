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
