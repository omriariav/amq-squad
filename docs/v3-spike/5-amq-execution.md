# amq-squad 3.0, brief 5-amq-A: AMQ v0.116.2, pi seats through the upstream adapter, Codex pins through admission

Branch `v3-5-amq-a`, cut from `v3` at 83a155d0. Brief: brief 5-amq-A (owner decision 2026-10-28: the 3.0.1 AMQ bump folds into 3.0.0). Mid-brief, the coordinator moved the pin target to v0.116.2, released 15:00Z the same day (production Go changes are confined to `internal/acp` and `internal/remote/buzzio`, the Desktop approval path; tests, plugin metadata, skills and docs also differ; `launchapi`, the launch adapters, owner-sign and the mailbox code are byte-identical to v0.116.1, checked with `diff -r` of the two module trees). No v0.116.1 binaries were built, so only the v0.116.2 digests are recorded.

Evidence: `docs/v3-spike/5-amq-evidence/` (every file passed through `scripts/redact-evidence`; `-check` is clean).

## What changed

1. **The bump.** `go.mod` `github.com/avivsinai/agent-message-queue` v0.107.0 -> v0.116.1 -> v0.116.2. The bump compiled and vetted clean. One test broke: `TestPinnedAMQModuleMatchesGoMod` (the launch-verified table had no row for the new pin). `registry.PinnedAMQModule` and its row moved (codex 0.163.0, grammar 4; claude 2.1.233; grok 1.0.5; cursor-agent unchanged). `adoptionseam.PinnedAMQModule` is new. `TestLinkedAMQModuleIsThePin` checks that go.mod and the module linked into the binary (from build info) are both v0.116.2. The adoption floor stays v0.74.1. Note: go1.27 test binaries list their dependencies, which the go1.25 comment in `floor_test.go` says they did not; the new row skips if a toolchain lists none.
2. **Codex sandbox pins through launch admission.** The compiler now appends `-c sandbox_workspace_write.writable_roots=[]` and `-c sandbox_workspace_write.network_access=<false|true>` to every Codex seat's provider arguments. The value is `true` only when the seat declares `CODEX_NETWORK_ACCESS` in env_needs. AMQ v0.110.0 and later admit both keys (upstream ask 17), so the library's admission and trust digest now cover the pins. seat-exec no longer adds them. `CheckCodexSandboxPins` refuses an admitted argv unless it carries each pin exactly once with the value the record requires; the refusal code is `sandbox_pins_missing`, published as the exec record. Amendment 28's refusals keep their meaning: any other `sandbox_workspace_write` key, or a widening value, is still `codex_sandbox_missing`. The preflight diff is unchanged.
3. **pi seats through the upstream adapter.** `PiAdapterInLibrary = true` and the compiler's refusal is gone. The adapter selects pi by the executable's file name, then probes `<pi> --version` (floor 1.2.0, release builds only) and `<pi> --help` (`--session-id` must be listed), and plans `<wrapper> seat-exec --seat H <pi> --session-id <id>`. The embedded runtime is node plus a preload plus pi's entry, not one file. So the launch writes a **pi launch shim**, `~/.amq-squad/pi-launch/pi` (0700). The shim execs `amq-squad pi shim "$@"`. It is rewritten only when it would name another binary, so its identity, which AMQ's trust subject binds, stays stable. `amq-squad pi shim` answers exactly those two probes from the verified embedded runtime: it runs Ensure (the full re-hash), `CheckLaunch` and the forced environment, without the package, and refuses anything else.
   - A pi seat's executable is the shim. A provider path (a pi from PATH) is refused `pi_not_embedded`. It carries no provider arguments and no initial input (amendment 23 (b)).
   - seat-exec checks argv[0] is this binary's shim: the path, a private regular file, owned by the user, and the exact bytes. Otherwise it refuses `pi_not_embedded`.
   - seat-exec runs the amendment 23 (a) resume check on the adapter's argv.
   - It then execs, in place, `amq-squad pi run --seat H [--model M] [--thinking E] -- <adapter argv[1:]>`. That launcher re-hashes the runtime, checks the launch set, forces the pi environment (`JITI_FS_CACHE=false`, the seat's private TMPDIR under its extension directory, `AMQ_SQUAD_AMQ` from the record) and supervises node (amendment 26).
   - The exec record keeps the adapter's argv (`provider_argv`) and the launcher's (`launcher_argv`).
4. **doctor.** One row per installed binary (`amq`, `amq-remote`, `amq-owner-sign`), read from `--version` with `AMQ_NO_UPDATE_CHECK=1`. A binary at or above 0.116.2 is ok. Below it, a prerelease of it, or unparseable output is a fail row with the install line (`amq upgrade --all -y`, Homebrew `brew upgrade avivsinai/tap/amq`). A missing `amq` is a fail; a missing `amq-remote` or `amq-owner-sign` is a warning. `doctorMinAMQVersion` stays 0.60.0: it is the v2 general-operation floor that the real-AMQ CI lanes run against, pinned by `TestGeneralOperationFloorUnchanged`.
5. **Docs.** README, MIGRATION and `docs/claude-hooks.md` do not name v0.107.0. The release notes' "Release (phase 5)" gets one bullet; the README requirements move to 0.116.2 in the fix round.

`schedproc.External` (the request-file stand-in) is no longer used by any 3.0 path: `scheduler run` routes pi to the AMQ launcher, because the stand-in is chosen only when `PiAdapterInLibrary` is false. Its deletion is deferred (see "Deferred" below).

## Live finding, fixed

With `AMQ_SQUAD_PI_TIMING=1` in the launch environment, Apply answered `launch_action_required` (`5-amq-evidence/live-pi-timing-env-before-fix.log`). The adapter's probe reads `CombinedOutput`, and the preload's self-check timing line on stderr joined `1.3.0`, so the version did not parse. The fix: the shim drops `AMQ_SQUAD_PI_TIMING` from the probe's environment, and it holds the child's stderr, showing it only when pi fails, because a Node warning on stderr would break the version the same way. Live, the same launch now applies (all five runs below had the variable set). Row: `TestPiShimAnswersOnlyTheAdapterProbes`.

## Acceptance

| # | Row | Result | Evidence |
| --- | --- | --- | --- |
| 1 | gofmt, `go build ./...`, `go vet ./...`, GOOS=linux and GOOS=windows builds, `go test -count=1 ./...`, `scripts/manifest-check.sh --final` | pass: 56 packages ok, 0 FAIL, 142 s (go1.27.1 darwin/arm64); manifest-check OK | gate run on cc1295b4, repeated on the final head |
| 2 | A pi seat launches through `amq launch` with the embedded runtime; the record carries the adapter's argv with `--session-id`; the seat answers one prompt; timings | met, 5 of 5 runs. Exec record `provider_argv` = `<home>/.amq-squad/pi-launch/pi --session-id <uuid v8>`, `launcher_argv` = `amq-squad pi run --seat scout --model ollama/qwen2.5:32b -- --session-id <same id>`. pi persisted `sessions/--<wt>--/<ts>_<same id>.jsonl` after the first turn, and the seat answered `PINEAPPLE`. Medians of five (ranges in the timings file): Prepare 14.1 ms; **launch to up** (Apply, the adapter's two shim probes, pane, seat-exec, identity proof, proof accepted) **2,465 ms**; **up to pi ready** (launcher re-hash, node start, pi's TUI drawn with the model in its footer) **806 ms**; **prompt to answer** (a full qwen2.5:32b turn including the first prefill of the 2.5k-token system prompt) **3,873 ms**. Cold install (unpack + re-hash, before the launch) 1,178 ms. Shim probes on their own: cold `--version` 2,466 ms (it unpacks), warm `--version` 510 ms, warm `--help` 532 ms, so about 1 s of launch-to-up is the probe pair | `TestLivePiSeatThroughTheAdapter`; `live-pi-timings.txt`, `live-pi-run1.log`, `shim-probe.txt` |
| 3 | Amendment 23 (a) resume refusal on the new path: fires with a tool result and no session file; not when either is absent | met, three rows on the adapter's argv (shim + `--session-id`): refused `pi_resume_context_lost`; no tool result resumes; session file at the pinned 1.3.0 path resumes (and a file in another cwd's folder does not count) | `TestSeatExecPiResumeRefusal` |
| 4 | Codex launch request carries the two pins; admission accepts them; the trust digest changes when a pin changes (two rows); seat-exec refuses an admitted argv without them | met. The library's own `launchapi.Prepare` runs in process on the compiled request: action_required (trust), both keys advertised, digest stable across two prepares. Row 1: network pin flipped, digest moves. Row 2: writable-roots pin dropped, digest moves. A widening `writable_roots=["/"]` is refused by the library. seat-exec: seven refusal rows (no pins, roots only, network only, the network value wrong either way, a pin twice, a wrong value in the attached form) are `sandbox_pins_missing`; widening and another key are `codex_sandbox_missing`; the compiled pins exec unchanged | `TestCodexSandboxPinsAreAdmittedAndDigested`, `TestSeatExecRefusesAnArgvWithoutTheSandboxPins` |
| 5 | doctor refuses an installed AMQ below v0.116.2 and passes at v0.116.2 | met: stub binaries on PATH (0.116.1 and `amq-remote 0.107.0` fail with the install line; 0.116.2 and v0.117.0 ok; 0.116.2-rc1 and garbage fail; no amq fails; no amq-remote warns). Live on this host: the Homebrew 0.107.0 trio fails; the v0.116.2 builds first on PATH pass | `TestDoctorInstalledAMQFloor`; `doctor-floor-and-digests.txt` |
| 6 | The adoption-seam tests prove the linked module is v0.116.2 | met: go.mod and the build info's dependency are both v0.116.2, with no replace, at or above the v0.74.1 floor | `TestLinkedAMQModuleIsThePin` |
| 7 | No test reaches the network | met: the new rows use stub executables, the library's Prepare in process (never starts tmux; the rows skip without tmux on PATH) and temp dirs. The live row (skipped by default) talks only to the local Ollama and a private tmux server | the new test files |

Revert check: `5-amq-evidence/revert-check-a.txt`, 11 rows, every row fails with its fix reverted and passes once it is restored.

Binaries for the live rows were built from the module cache into the worktree (`build/`, gitignored), with `GOFLAGS=-mod=mod -trimpath -ldflags "-X main.version=0.116.2"`, and put first on PATH for those runs only. Nothing was installed. sha256: amq `795bfae1…fdf42`, amq-remote `73e04f06…031e7`, amq-owner-sign `c2aa34f7…85ef`, amq-squad (payload, final) `b698601f…8a987`; the full digests are in `doctor-floor-and-digests.txt`.

## Not run, and why

- The launcher's own split (re-hash, launch rest, launch check), as 4-1 reported it, was not captured in the live pane. pi's TUI redraws over the `AMQ_SQUAD_PI_TIMING` stderr line, so the live row reports up-to-ready as one number. 4-1's split still describes that launcher, which is unchanged here.
- No live Codex seat: acceptance 4 is met by the library's own Prepare and seat-exec rows. A real codex-cli seat would also need the owner's Codex trust and login.
- No live resume of a pi seat. Acceptance 3 is three unit rows on the adapter-shaped argv.

## Upstream behaviour that differed from the brief's notes

- **The adapter does not mint `--session-id <launch nonce>`.** It mints an RFC 9562 v8 UUID from sha256("amq-pi-session", nonce, handle), so the seats of one launch never share a pi session.
- **Mint-mode adapters are not probed at Prepare** (`internal/launch/prepare.go`: "Mint-mode adapters are not probed"). The pi version floor and the `--session-id` help check run at Apply (reconcile), against the shim. Capture-mode Codex is probed at Prepare (`--version`, `--help`, `resume --help`).
- **The probe uses `CombinedOutput`.** Anything on the pi executable's stderr becomes part of the version string; that caused the live finding above.
- The adapter refuses initial input that begins with `@` (pi reads it as a file attachment). Our pi seats send none.
- `launchapi` gained `InjectorClaudeNative` (claude-native requires Claude Code 2.1.311, macOS, fresh and unwrapped). 3.0 does not use it.
- The Codex grammar is 4, and its config overrides are `model_reasoning_effort`, `approvals_reviewer` and the two sandbox pins. pi's grammar is 1 with no forms (amendment 23 (b) unchanged; the watch item stays).

## Other finding

Node 22 writes its module compile cache (`node-compile-cache/v22.21.1-arm64-…`) into the seat's TMPDIR, the private 0700 directory under its extension directory. It showed up as a non-empty directory when the live test's temp dir was removed while pi was still running. It lands where ruling 12 of 4-1 wanted temp writers to land. Whether the verified-bytes property needs `NODE_DISABLE_COMPILE_CACHE=1` in the forced set is a question for the lead; V8 checks the cached code against the source hash.

## Fix round (reviewer rulings 1 to 3 and the Codex pass on d7e3fc1a)

One fix commit. Every fix has a row that fails when reverted (`revert-check-a.txt`, section "Fix round", 14 rows, all OK; the README text, the ci.yml shape and the diff wording of small (d) have no Go row, and their evidence is the local lane run and the regenerated README.html). Ruling 4 (deleting the stand-in) left the round; it is recorded under "Deferred".

### Ruling 1: the compile cache off; NODE_OPTIONS and NODE_PATH

- `NODE_DISABLE_COMPILE_CACHE=1` is in the forced pi environment (`piruntime.Forced`), so every start compiles from the bytes the re-hash verified. Live, the build without it left `node-compile-cache/` in the seat's TMPDIR and failed the row (`fix-live-cache-on.log`); with it, the row passes and the directory never appears (`fix-live-cache-off.log`).
- `NODE_OPTIONS` stays forced empty (4-1 review item 1; it loads nothing). `NODE_PATH` was already dropped with every `NODE_*` and `JITI_*` from the parent and from every overlay. Row `TestPiRunAsSeatExecStartsIt`: with the parent setting `NODE_OPTIONS=--require=…` and `NODE_PATH=…`, the seat's pi process environment, as the launcher execs it, has `NODE_OPTIONS` exactly once and empty, no `NODE_PATH`, and `NODE_DISABLE_COMPILE_CACHE=1`. The live node process's own environment cannot be read back on macOS: pi sets `process.title`, which overwrites the argv and environment area that `ps -E` reads (tried and recorded). So the live proof is the compile cache's absence, and the environment proof is the exec'd environment.
- Cold starts, 4-1's method (`cold5.py`: a fresh `amq-squad` process each run, the runtime unpacked, qwen2.5:32b loaded, five runs per series, medians), on this round's binary against the same binary with the cache on (`fix-cold5-cache-on.json`, `fix-cold5-cache-off.json`):

| Series | Build | Re-hash ms | Launch rest ms | Node start to pi ready ms | Self-check ms | Model first token ms | Spawn to first token ms |
| --- | --- | --- | --- | --- | --- | --- | --- |
| lead | 4-1 final (cache on) | 147.3 | 56.7 | 516.6 | 202 | 91.5 | 859.5 |
| lead | 5-amq-A, cache on | 140.8 | 55.0 | 488.8 | 209 | 89.1 | 786.8 |
| lead | 5-amq-A, cache off | 142.6 | 56.1 | **520.7** | 196 | 90.4 | 822.6 |
| control | 4-1 final (cache on) | 152.0 | 57.0 | 359.4 | 206 | 75.9 | 644.8 |
| control | 5-amq-A, cache on | 141.6 | 55.6 | 330.2 | 196 | 75.5 | 608.9 |
| control | 5-amq-A, cache off | 141.6 | 56.0 | **358.3** | 197 | 78.0 | 641.8 |

The price of the fix is about 32 ms at node start to pi ready (488.8 against 520.7 ms with the package; 330.2 against 358.3 ms without it). The first run of the lead series, the one with no cache to reuse, is 1,465.7 ms with the cache on and 1,665.9 ms off (node start to pi ready; Ollama's first prefill dominates spawn to first token on that run: 7,311.7 and 6,495.2 ms). Every answer was `42`.

### Ruling 2: README

The Requirements line is now `amq` 0.116.2 or later (with `amq-remote` and `amq-owner-sign` at the same release when installed), and doctor refuses an older one. The 0.60.x paragraph and its lane table now describe one floor, the pinned required lane and the non-blocking `latest` canary. README.html was regenerated (`make readme-html`; `make readme-html-check` in sync).

### Ruling 3: CI and the one floor

`.github/workflows/ci.yml`: the v0.60.0 lanes, the `latest`-only lanes and the v0.74.1 adoption-floor lane are gone, with their comments. One job, `real-amq-compatibility`, runs on macOS (the 3.0 host; tmux installed) as a matrix of `pinned v0.116.2` (required) and `canary latest` (`continue-on-error: true`, named so: a failure reads as upstream drift). The macOS wake lane was dropped with the rest: its test, `TestRealAMQWakeCompatibility`, no longer exists in the tree (it ran no test: `[no tests to run]`), and neither does `TestLaunchapiBackendDualRunParity`, which the old adoption lane listed. The lane runs every test gated on `AMQ_SQUAD_REAL_AMQ`, by name:

- `./internal/cli`:
  - `TestRealAMQCompatibility`, with subtests sessionful_default_profile, exact_root_named_profile, three_actor_100_message_isolation, committed_delivery_wording_contract, coop_exec_drains_preexisting_goal_with_zero_injection, external_wake_suppresses_backlog_and_injects_post-baseline_resend, doctor_repairs_malformed_configured_mailbox, canonical_root_authority and post-coop_child_identity. **Skipped: orchestration_contract_default and orchestration_contract_review** (see "Deferred").
  - `TestRealAMQGateCloseReplyRefsCompatibility`
  - `TestRealAMQDoctorGlobalRepairScope` (8 subtests)
  - `TestRealAMQFixtureEnvSanitizesManagedContext`
  - `TestRealAMQProcessEnvSanitizesManagedContext`
  - `TestRealLaunchapiInspectSessionTargetShape`
  - `TestNestedWorktreeRootDiscoveryOnAMQ074`
- `./internal/launchintent`: `TestCompiledIntentAcceptedByReleasedAMQ`.
- `./internal/adoptionseam`: `TestApplyForwardsSubjectSchemaFromPrepareResult`.

All pass locally against the v0.116.2 build, run as ci.yml runs them. A second run without `-skip` lists the two skipped subtests by name with their reason (`fix-pinned-lane.txt`).

`doctorMinAMQVersion` is 0.116.2, and the installed-binary rows use the same constant (`doctorInstalledAMQFloor = doctorMinAMQVersion`). `TestGeneralOperationFloorUnchanged` now pins 0.116.2, the equality of the two names, and the go.mod pin. The v2 version rows of doctor_test moved to the new floor (`TestExecuteDoctorAMQVersionAcceptsTheFloorAndNewerCanary`, `…RejectsOlderThanTheFloor`: 0.42.1, 0.60.0, 0.74.1, 0.107.0, 0.116.1 and 0.116.2-rc1 fail). Required-check names in the branch protection change with the job names (owner setting, outside the repository).

### The Codex pass on d7e3fc1a

- **A-P1.** Declared credentials survive the handoff. seat-exec puts the record's `env_needs` and `credential_needs` on the launcher argv as `--env-need NAME`, and `pi run` hands them to `piruntime.Prepare` (`Spec.EnvNeeds`). The launcher's scrub therefore keeps exactly those names: a declared `ANTHROPIC_API_KEY` reaches pi, and `OPENAI_API_KEY` and `GEMINI_API_KEY` do not. Rows: `TestPiSeatKeepsItsDeclaredCredential` (end to end: seat-exec's `PiLauncherArgv` into `pi run`) and the A-P1 case in `TestSeatExecPiNeedsTheEmbeddedShim` (seat-exec's own environment and argv).
- **A-P2.** The shim is written with `atomicfile.Write` (a random O_EXCL temp, fsynced, renamed, the directory fsynced). Row: a symlink planted at the old predictable `pi.tmp-<pid>` name is never followed, and its target is unchanged.
- **A-P3.** The shim's mode must be exactly 0700, and `EnsureShim` repairs another mode. Row: a 0600 shim fails the check and is rewritten 0700.
- **Small (a).** The launch record carries the shim (`pi_shim`) and the pi agent dir (`pi_agent_dir`, under the launching HOME). seat-exec compares argv[0] with the record and never derives a path from its own HOME, and it passes `--agent-dir` to `pi run`. Rows: with HOME moved the recorded shim is accepted; a record naming another shim refuses `pi_not_embedded`.
- **Small (b).** `CheckShim` opens with O_NOFOLLOW and runs the fstat, mode, owner and content checks on that one descriptor. Row: a symlink to a byte-identical 0700 shim is refused.
- **Small (c).** The floor rows and the amq version row carry `amq_below_floor` (doctor's Kind) when they fail.
- **Small (d).** The v0.116.2 diff wording above is corrected.

The live pi-seat row ran again on the round's binary (`fix-live-cache-off.log`, pass).

## Narrow re-pass (two one-line fixes)

- The shim's open adds O_NONBLOCK. A FIFO named pi with no writer used to block both the launch and seat-exec in `open`; now it returns at once, and the fstat refuses the non-regular file (`pi_not_embedded` in seat-exec). Rows: `TestShimRefusesAFIFOAndRepairsSpecialBits` (CheckShim returns within 3 s) and the FIFO case in `TestSeatExecPiNeedsTheEmbeddedShim`.
- The mode check compares the full `FileMode` with 0700, where `Perm()` had ignored the setuid, setgid and sticky bits. An owned, byte-correct shim with setuid now fails the check, and `EnsureShim` repairs it to exactly 0700. Row: `TestShimRefusesAFIFOAndRepairsSpecialBits`.

`revert-check-a.txt`, section "Re-pass": 3 rows, all OK.

## Deferred

### The scheduler stand-in deletion (ruling 4, a separate brief on top of A's merge)

`amq coop exec` is AMQ's own CLI verb, not amq-squad code. The guard classification at `internal/cli/hook_guard.go:439`, which treats `amq coop exec` as a launch command so guard refuses a seat starting one, stays: AMQ still ships that verb. The real-AMQ v2 tests also exercise AMQ's `coop exec`. The deletable part is the scheduler stand-in. Deleting it is not pure removal; it drags:

- (a) `schedproc.External`, `Request` and `RequestsDir` (slots.go), and the `External` type check inside `Launcher.CloseUnconfirmed` (slots.go:205). `TestExternalCloseIsUnconfirmed` and `fix2_test.go:142` use `External` as the pi launcher and would have to be rewritten with a fake launcher.
- (b) The event-file path: `Event`, `PostEvent`, `EventsDir` and `drainEvents` in the `Run` loop (process.go). `fix4_test.go:154`, the adopted-seat close row, delivers its `seat_gone` through `PostEvent`. That row tests the decision-socket gating, so it needs another injection path (a rewrite, not a deletion).
- (c) The `amq-squad scheduler post` verb: usage, flags and dispatch in `scheduler_run.go`, and its mention in `2b2b-execution.md`.
- (d) `spike/sched-live/main.go` uses `External`'s request files and `scheduler post`. `go build ./...` compiles `spike/`, so the harness would be deleted or edited.
- (e) `seatlaunch.PiAdapterInLibrary` and its branches become dead code, and `TestPiSeatShape` checks the constant. It goes with the stand-in and is untouched here.

### The two orchestration-contract subtests (brief C)

`TestRealAMQCompatibility/orchestration_contract_default` and `/orchestration_contract_review` are skipped in the pinned lane and print their reason in a local run. They have a pre-existing break, independent of the AMQ version: `realAMQOrchestrationContract` builds a team with `Binary=/bin/sleep`, and squad.yaml has rejected a runtime outside claude, codex and pi since 2b-5 (4fc5dbf2). The other nine subtests, and the rest of the lane, pass at v0.116.2 (and fail the same two ways at 0.107.0). The cause appears in three places: the ci.yml comment, the test's own skip message (`realAMQOrchestrationContractSkip`), and here. Follow-up for brief C: port the two subtests to a schema-valid team (a runtime of claude, codex or pi with a stub binary, since dispatch queues and sends without launching), or state why the dispatch authorizer cannot accept any schema-valid team in that test. `dispatch` is a kept 3.0 verb, so the test is not deleted; brief C's acceptance includes the two subtests running unskipped against v0.116.2.

## Left for the lead

- Plan 4.1, 4.3 and amendment 28 still describe the Codex pin exception ("seat-exec adds the two config pins … AMQ 0.107.0 does not admit") and the pi launch as pending. The upstream-asks status table (asks 1 and 17) can move to shipped.
- This host's installed AMQ is 0.107.0 (Homebrew), so `doctor` now fails its three floor rows here until the owner upgrades.

## Part B: the Buzz share on the mailbox adapter

Branch `v3-5-amq-b`, cut from `v3` at 8b8a8879 (briefs ruling 20). Not yet
rebased onto part A (go.mod is still v0.107.0 on this branch; nothing in B
imports the AMQ module). Every row is one of:

- (a) a unit row whose upstream shapes come from the v0.116.2 module, file
  and tag named below, run against fakes (fake relay, fake serve);
- (b) a measured row against binaries built from the v0.116.2 module into
  the scratch directory (`go build -C <module cache>/agent-message-queue@v0.116.2
  -trimpath ./cmd/amq ./cmd/amq-remote ./cmd/amq-owner-sign`), labelled
  "scratch-built v0.116.2, not the installed binary"; never installed. sha256:
  amq-remote 25b2591787e97a0ac92f608e2aa4b2f8c83bc446712526f58e23b1d132c9d7fc,
  amq 212277d51b25603e7fd90b02eaab4ca06d76dbd9c20af1a2d8c2018e40e82298,
  amq-owner-sign 2975f4d53b16a409f909f37ca7965a9b86fac93b45ab60933b2f29342b476329;
- (c) a live row against the installed binaries (0.107.0 on this Mac), which
  waits for the owner's AMQ upgrade and is written into
  `docs/v3-buzz/owner-run.md`.

v0.116.2 differs from v0.116.1 only in internal/acp and
internal/remote/buzzio (the Desktop approval path, #1284); the mailbox
adapter and the amq-remote README are byte-identical (diff -rq of the two
module trees).

### Upstream shapes relied on (v0.116.2)

| shape | source |
| --- | --- |
| adapter kind `mailbox`, `config.handle` (an inbox in serve's own root, never created), `reply_timeout` (default 10m, max 24h), handle not `buzz`, `amq-remote` or `--me` | `internal/remote/mailbox/mailbox.go` (Factory); `cmd/amq-remote/README.md` "Manifest" |
| a Buzz DM delivered as one message: to the handle, from `buzz`, thread `cockpit/<channel>`, subject `Buzz DM`, labels `acp`, `buzz`, `nostr:<event>`, body = DM text + "This came from the owner's Buzz DM. Answer with `amq reply --id <id>` ..." | `mailbox.go` (prompt) |
| result = the newest reply to the prompt that is not `--kind status` and has a body; none within reply_timeout: the request fails, the message stays | `mailbox.go` (observe, finalReply, timeoutText) |
| bind the DM channel without signing: `share --target T --relay R --dm-channel C --native-session <handle> --min-evidence submitted`; existing presence kept | README "Strict share in one owner step"; `cmd/amq-remote/share.go` (relayBinding.apply) |
| `/inspect`, `/status [ref]`, `/cancel ref` and any `/...` are serve's own commands, never submitted | `internal/remote/buzzio/normalize.go` (Normalize) |
| one request per target at a time: a second submit while one runs is a terminal `busy` (no queue) | `internal/remote/core/admission.go` (runtimeInFlightLocked, busy tombstone) |
| the DM surface opens only when relay-signed 39000/39002 show a private DM of exactly owner and body | `internal/remote/buzzio/membership.go` |
| `amq-remote bindings` lists `attach --self` bindings under `~/.amq/remote/bindings`, not manifest shares | `cmd/amq-remote/attach.go` (bindings) |

### Built

- `internal/buzz/inbound.go` (new, replaces `pump.go`): the `Handler`.
  Lists the `attach` inbox, takes each message from `buzz` once (`amq read`
  moves it to cur: the once-only claim), parses the one `nostr:<id>` label,
  and for a gate answer sends `verifying <gate>` as a status line, opens
  the verifier's connection (`Dialer`), fetches the event by id
  (`gateverify.FetchEvent`), resolves the squad core on the fetched event,
  runs `Core.HandleEvent` on the fetched bytes with the connection lent for
  the call, closes. One result reply per message, through redact.Block:
  `accepted <gate> <class>`, `refused <gate> <code>` (plus `re-ask: answer
  in the gate thread (event <12 hex>)` under the thread rule), `routed
  <dest>` (plus `not_forwarded ambiguous_lead|not_a_lead`), `refused -
  not_delivered`, `refused <gate> ambiguous_gate`. A duplicate delivery of
  one event id: `refused <gate> already_consumed`, no connection. A
  verification capped at 10 s: on the cap a status line `timeout <gate>
  verify_timeout`, no result, Kind `attention`; the gate stays open.
- `internal/buzz/amqinbox.go` (new): `AMQInbox` over `amq list --new`,
  `amq read`, `amq reply --kind answer|status`, body on stdin.
- `internal/buzz/manifest.go` (new): `CheckMailboxShare`, the one path:
  the session's share on target `amq-squad` of kind mailbox, handle
  `attach`, reply_timeout 60s, DM commands on for the bound channel,
  native_session_id `attach`, min_evidence submitted, presence on, the
  inbox present; the `buzz-presence` fake target refused.
- `internal/buzz/transport.go`: `VerifyDialer` (per-verification connection).
- `internal/gateverify`: `FetchEvent`; `CheckHistory` takes the answer
  event (acceptance 10 below).
- `internal/cli/remote.go`: `remote bind` checks the manifest
  (`CheckMailboxShare`) before writing anything and reports the binding;
  `remote attach` builds the core without a transport, holds no relay
  connection, runs the Handler over `remoteInbox` with `remoteDialer`
  (gateTransport per verification), prints `attention` for a hang.
- `docs/v3-buzz/enroll.sh`: amq-remote floor check (v0.116.2), `amq init
  --agents remote,attach`, the mailbox manifest, the fake-target refusal,
  phase E's DM bind on the mailbox target.

Deleted outright: `Pump`, `MarksDir` and the marks directory, the 24 h
backlog read, the `since` cursor, the future-dated quarantine (serve's
admission window and the verifier's created_in_future cover it), the
attachment's body-signed replies, the `/status` activity reply.

### Acceptance

| # | row | kind | status | evidence |
| --- | --- | --- | --- | --- |
| 1 | enrollment binds kind mailbox, handle, reply_timeout 60s, DM commands on | (a)+(b) | met | (a) `TestCheckMailboxShare`, `TestRemoteBindWritesTheOwnerFiles` (fake target refused, nothing written; mailbox binding reported); (b) `TestOwnerScriptDryRun` with `AMQ_SQUAD_TEST_AMQ_BIN` = the scratch-built v0.116.2: the script's four runs, the manifest after phase D (mailbox adapter, presence on, no DM channel yet), after phase E `{Target:amq-squad Kind:mailbox Handle:attach ReplyTimeout:60s Channel:dm-4-5-dryrun Commands:true MinEvidence:submitted Presence:true}`. `amq-remote bindings` does not list a strict share (upstream, see below); the live check is owner-run 3.1 and 4.3 (c) |
| 2 | no DM-channel subscription in internal/buzz; marks and redelivery gone | (a) | met | `TestHandlerOnlyActsOnServeDeliveriesAndFetchedEvents`: two owner DMs on the relay, not delivered by serve, are never handled and open no connection; code: no Query of kind 9 in internal/buzz outside tests |
| 3 | verifier fetch: one REQ by id on a fresh connection, closed; 10 s cap; hang = attention + status, no result | (a)+(b) | met | (a) `TestVerifierConnectionOnTheRelay` (websocket relay: connections open at the REQ by id `[1]`, 0 after; stalled REQ: attention, two status lines, no result, connection closed), `TestHandlerVerifierConnectionPerAnswer`; (b) dry run: open connections at the verifier's REQ by id 2 (serve's 1 + the verifier's), back to serve's 1 after |
| 4 | reply shapes, one non-status reply per inbound, redact.Block, no answer text, canary not a credential shape | (a) | met | `TestHandlerReplyTable` (verified, already_consumed, wrong_pubkey, outside_window, bad_signature, not_in_gate_thread + re-ask line, routed, not_forwarded), `TestHandlerGatesAndRouting`; no reply contains the canary, `git tag` (question text) or 16 hex (the digest prefix) |
| 5 | the same event id delivered twice is handled once; an answer outside the thread gets the re-ask reply | (a) | met | `TestHandlerDuplicateDeliveryHandledOnce` (one dial, second reply `refused rel-3 already_consumed`, a route delivered twice forwarded once), `TestHandlerGatesAndRouting` |
| 6 | rehearsal: question, answer, verified, cleared; the signed seat clear through the same path | (a)+(b) | met | (a) `TestRehearsalThroughServe` (internal/cli: gate raise, the fake serve's delivery, `remote attach --once`, `accepted rel-r tag`, gate accepted, verify action; seat clear request, answer through serve, `accepted clear-... quarantine_clear`, the clear record names the gate and the delivered event); (b) dry run and `rehearseOwnerRun` with the real serve (scratch-built): serve's result rows `accepted rel-dry tag`, `accepted g45-owner-01 tag`, `routed lead-b`, `routed app/cto` on the relay |
| 7 | presence rows of deviation 2 unchanged | (a)+(b) | met | `TestSupervisorFollowsPresence`, `TestRemotePresenceExpires*`; (b) dry run: online 202 ms after the row went live, offline 17 ms after it went stale |
| 8 | owner-run.md re-walked; README, plan, 4-5 deviation 1 closed, follow-ups | docs | met | `docs/v3-buzz/owner-run.md` (rows 0.1b, 1.2, 3.1, 4.3, 4.4, 5.2, 5.4, 5.5, 6.1, 6.2, 9.3, 9.5 and the record table), `docs/v3-buzz/README.md`, plan section 9, R33, R42, `docs/v3-spike/4-5-execution.md` deviations 1 and 6, `docs/v3-followups.md`, `docs/v3-upstream-issues/15-*.md` |
| 9 | no test reaches the network; evidence through redact-evidence; credential-shape test green | (a) | met | every relay is `httptest` on 127.0.0.1 (`CheckRelayURL` refuses anything else for ws://); `go run ./scripts/redact-evidence` over `docs/v3-spike/5-amq-evidence/`; `TestNoCredentialShapedValueUnderDocsOrEvidence` in the gate run |
| 10 | a reaction deletion on the gate thread is no alteration; an edit or deletion of the answer refuses | (a) | met | `TestHistoryCheck` rows 10a (body-key reaction deletion, k=7), 10b (another key, with and without k; the owner's Desktop since the question), 10c (the answer edited; deleted by another key; deleted under a k=7 tag; a cleanup that also names the question; a body deletion with another k) |
| live | the same rows against the installed binaries with the real key | (c) | waits | owner-run.md; needs the owner's AMQ upgrade to v0.116.2 |

Every review-relevant row fails when reverted:
`docs/v3-spike/5-amq-evidence/revert-check-b.txt` (7 rows; the cap row's
reverted run fails by the test binary's own timeout).

### Measured (b: scratch-built v0.116.2, not the installed binary; local relay)

| what | value |
| --- | --- |
| owner answer published to serve's delivery in the attach inbox | 222 to 232 ms |
| handler step (fetch connection, verify, commit, reply) | 142 to 149 ms |
| owner answer to serve's result row on the relay | 970 to 988 ms (serve's poll 500 ms) |
| connections at the verifier's REQ by id / after | 2 (serve 1) / 1 |
| raise to verified through `remote attach` (1 s tick) | 1.0 s |
| canary grep | 176 channels and files, 98 713 to 98 757 bytes, 3 key forms: not found |
| serve's result row as recorded (kind 40003, the body's edit of its row) | `amq-squad: completed` + blank line + `accepted rel-dry tag` |

Full logs: `docs/v3-spike/5-amq-evidence/b-scratch-v0.116.2-dryrun.txt`
(the dry run and the crash-path canary with the scratch-built binaries),
`b-gate-*.txt` (build and vet, `go test ./...`, race on buzz, gateverify
and cli, manifest check), `revert-check-b.txt`. One `go test ./...` failure
is internal/seatlaunch's TempDir cleanup race, a package this branch does
not touch; reruns pass (noted in the file).

### Mechanism

What serve now does: holds the share's relay connection, opens the DM
surface on the relay-signed membership, normalizes each owner DM (its own
slash commands answered by itself), submits plain text to the mailbox
target, which writes one AMQ message into `attach`'s inbox; polls the
handle's replies, takes the newest non-status one as the result and posts
it (and edits it) as the body's result row in the DM; fails the request
after 60 s without one. It publishes the profile and presence as before.

What the attachment no longer does: subscribe to or query the DM channel,
keep marks or a cursor, quarantine future-dated DMs, post replies with
the body key, answer `/status`.

Where the body key is still used, and why: (1) `gate raise` / `gate post`
post the gate question body-signed (the verifier needs the question's
event id and bytes in the store; serve's result rows are not addressable
questions); (2) per answer, the core's own connection: sign in, one REQ by
the event id, the question's and the answer's history reads (part of the
verification), the gate's one re-ask post when the thread rule applies,
close. Both are recorded in R33 as the second authenticated connection with
a bounded purpose; ruling 20 point 1 named only the read, so (1) and the
re-ask post are named here as where the narrowing stops.

### Upstream behaviour that differed from the notes

1. `amq-remote bindings` does not show the mailbox binding: it lists
   `attach --self` bindings only (`cmd/amq-remote/attach.go`). The binding
   is shown by the manifest and, while serve runs, `amq-remote sessions`
   (owner-run 3.1, 4.3).
2. Two owner DMs inside one request do not serialize: the second is a
   terminal `busy` result (admission.go), and the owner sends it again. The
   first rehearsal run showed it: a routed DM sent right after a verified
   answer was never delivered; the rehearsal now waits for the result row,
   as the owner run does (5.2).
3. `/status` (and any `/...`) is serve's own command and never reaches the
   mailbox, so the 4-5 activity reply is gone (follow-up).
4. Ruling 20's code names map to gateverify's: replayed = `already_consumed`,
   wrong_handle = `wrong_pubkey`, signature_invalid = `bad_signature`,
   not_in_thread = `not_in_gate_thread`; outside_window is the same.
5. The verifier's one connection carries the REQ by id plus the history
   reads (four REQs) and, under the thread rule, the re-ask post; one REQ
   alone would drop the edit and delete checks of amendment 20.
6. Ask 15's presence wish did not ship: the mailbox target is live while
   the inbox exists, so presence still follows serve's process (deviation
   2 unchanged).

### Not run, and why

- The live rows (c) against the installed amq-remote: it is 0.107.0 and
  may not be upgraded here; they are owner-run.md's.
- The doctor floor row is part A's code; owner-run 0.1b names it.
- Not rebased onto part A: A has not merged yet.

### Harness notes

- Shell compound commands with variables, heredocs into interpreters and
  `cd && ...` chains were refused by the worktree isolation check; scripts
  went to the scratch directory and ran as single commands.
