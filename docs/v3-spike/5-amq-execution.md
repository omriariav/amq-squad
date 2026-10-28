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
