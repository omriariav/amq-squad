# Phase 5: the first CI run's failures (execution log, brief 5-ci-D)

Branch `v3-5-ci-d`, from `v3` at 01d11190. The first CI run on the v3 branch (run 40945638948) failed three ways that every macOS gate had missed. The reviewer's rulings D1 to D3 replace the brief's open choices. Evidence is in `5-ci-evidence/`, passed through `scripts/redact-evidence`.

## D1. The decision socket at a root deeper than sun_path (make ci, ubuntu)

**What failed.** `TestSeatCommitAndWidenThroughTheGrowthGate` and `TestTaskWidenIsAcceptedOnlyFromTheLeadsPane` failed with `dial unix /tmp/<test>/001/.agent-mail/s/extensions/amq-squad/launch/scheduler-decide.sock: connect: invalid argument`. That path is 125 bytes, and Linux's `sun_path` holds 108.

**Why darwin passes and Linux fails.** `seatlaunch.ListenPrivate` (internal/seatlaunch/private_socket_unix.go:31) binds through `proofDir` and `bindPrivate` (proof_unix.go:86-120, 190) and records `Path = filepath.Join(d.Path(), name)`.
- darwin: `proofDir` moves to a private `amqsq-` directory under the temp dir when the walked path reaches 100 bytes (proof_unix.go:93, `runtime.GOOS != "linux"`). The recorded path is that short temp path, so a dial by name works.
- Linux: `proofDir` keeps the launch directory under the root, because the bind goes through `/proc/self/fd/<dirfd>/<name>` (`socketAddr`, proof_unix.go:115), which is always short. The recorded path is the full walked path under the root, 125 bytes on the runner.
- The dial side: `schedproc.SendDecision` (internal/schedproc/decide.go:246, before the fix) dialed `net.DialTimeout("unix", path)` with the recorded path. Linux rejects a name longer than `sun_path` with EINVAL, so the dial failed with the error above. The seat's proof dial already did this correctly (internal/seatexec/seatexec_unix.go:670-686, `DialProof`: it opens the directory and dials `/proc/self/fd/<dirfd>/<name>` on Linux). The decision socket's dial did not.

This is a production exposure, not only a test one: on Linux, any project whose `<root>/extensions/amq-squad/launch/scheduler-decide.sock` exceeds 107 bytes could not run `task widen`.

**Fix (ruling D1).** `seatlaunch.DialPrivate(root, recordedPath, name)` (private_socket_unix.go):
- It requires the recorded base name to be `name`.
- It resolves the recorded directory. If that is the root's launch directory, it opens it through the anchored walk `OpenAnchored(root, [extensions amq-squad launch], false)`. If it is darwin's `amqsq-` temp directory, it uses `OpenAnchored(dir, nil, false)`. Any other directory is refused.
- It runs the same 0700-and-own `fstat` check as `proofDir`.
- It checks the entry with `Fstatat(AT_SYMLINK_NOFOLLOW)`, which must show a socket owned by this user.
- It dials through `socketAddr`: `/proc/self/fd` on Linux, the walked path elsewhere.

`SendDecision(root, path, req)` dials through `DialPrivate`, and `task_widen.go:111` (its only caller) passes the store's root. `DecisionServer.Root()` was added so the tests can pass the root. The launch record still records the full path, and darwin's temp fallback is unchanged. There is no second socket location and no chdir. The non-unix stub returns the same "needs a unix host" error as `ListenPrivate`.

**Rows.**
- `TestDecisionSocketDialsUnderARootPastSunPath` (internal/schedproc/deeproot_test.go, unix): the session root's launch path is longer than 108 bytes (`len(walked) > 108` is asserted), and one decision goes through `SendDecision`.
  - On Linux it also asserts that the record carries the walked path and that a plain `net.Dial` of it fails, which is the CI failure.
  - On darwin it asserts the temp fallback (under 104 bytes).
  - It compiles for both: `GOOS=linux go test -c ./internal/schedproc ./internal/seatlaunch ./internal/cli` succeeds.
- `TestDialPrivateRefusesAPlantedEntry` (internal/seatlaunch/private_dial_test.go): each of the following is refused before any connect:
  - a regular file planted under the name;
  - a symlink under the name pointing at a live socket of ours;
  - a socket of another uid. A test cannot plant a foreign-uid socket without root, so the `socketOwnerUID` seam makes our own socket read as foreign, and the same socket dials for its owner first;
  - a launch directory that is not 0700;
  - a recorded path outside the launch directory and the temp directory.
- The two CI failures pass here again.

**What could not run here.** There is no Linux on this host: colima and lima have no instance, and creating one downloads a VM image, which counts as installing. So the Linux branch of the deep-root row did not run here; it runs in `make ci` on ubuntu. The reasoning for Linux: before the fix, `SendDecision` passed the recorded path to `connect(2)`. With `len(walked)` asserted above 108, `net.Dial` returns EINVAL on Linux. That is the row's Linux assertion, and it is the CI message. After the fix the connect goes to `/proc/self/fd/<n>/scheduler-decide.sock` (under 40 bytes), the address `bindPrivate` itself bound and the one `DialProof` already uses on the same runner.

On darwin, reverting the dial leaves the deep-root row green, because darwin never records a long path. The refusal row is the revert-sensitive row on this host (D1a in revert-check-d.txt).

## D2. TestRemoteAttachOnce on Linux ("starts 1", make ci, ubuntu)

**What failed.** At remote_test.go:196 the test failed with `attach with no live lead: starts 1`: presence `started, online, present: true` for `cto`, with no `presence.json` written yet.

**Diagnosis.**
- The attachment's presence is `buzz.FromStatus` over the status document. A seat row's `present` is its `live` field (internal/buzz/presence.go:33, `seatPresent`).
- `status.Collect` sets `live` from the launch record alone: `rec.StoppedAt == nil && rec.AgentPID > 0 && o.PIDAlive(rec.AgentPID)` (internal/status/collect.go:134).
- The default `PIDAlive` is `procinfo.Alive`, which is signal 0 with EPERM counted as alive (internal/procinfo/procinfo.go:43-56).
- The fixture seeds every launch record through `seedNotifyLaunch` with `AgentPID: 42` (internal/cli/removed_command_test_support_test.go:46; operator_test.go:81 did the same).
  - On this darwin host pid 42 does not exist (`ps -p 42` is empty).
  - On the ubuntu runner pid 42 is a live process (low pids on a fresh Linux VM are kernel threads), and signal 0 answers it.
- So on Linux the cto seat read live from its launch record, presence started the endpoint, and the test failed.
- The other suspects are ruled out:
  - The symlinked root: the CI output shows the `/tmp` root, and presence would not depend on it.
  - A tmux or process probe: none exists in this path; only signal 0 on the recorded pid.
  - A leftover `presence.json`: the refused `--leads` call writes none, and the presence file is written later in the test.
- Reproduced here: seeding pid 1 (launchd, alive through EPERM) gives the CI output byte for byte (D2b).

**Fix.** The cause is in the test fixture. The seeded records now carry `seededDeadPID = 1 << 30`, above Linux's `PID_MAX_LIMIT` (4194304) and darwin's `PID_MAX` (99999), so the pid is never a live process on any host. internal/buzz is unchanged, there is no `runtime.GOOS` skip, and the assertion is unchanged.

**Not a portability bug in presence.go.** Presence reads the status contract's `live` field correctly. A product note for the reviewer, outside this brief: `status.Collect`'s `live` trusts a bare signal-0 on the recorded pid. Unlike `classifyLaunchRuntimeIdentity` (internal/cli/launch.go:90), it does not match the binary or the start stamp. A reused pid after a seat's crash therefore reads live until its `StoppedAt` is written. Nothing here changes that.

**Rows.**
- `TestSeededLaunchPIDIsNeverALiveProcess` (internal/cli/remote_test.go) checks the range and that `procinfo.Alive` is false for the seeded pid.
- `TestRemoteAttachOnce` itself. On revert to 42, the guard fails on every host (D2a). Seeding a pid that is live here fails `TestRemoteAttachOnce` with the CI message (D2b).

The full shuffled internal/cli suite passes with the new seed, so no test depended on pid 42 being live.

## D3. The real-AMQ lane without a host claude (macOS runner)

**What failed.** At real_amq_nested_worktree_discovery_test.go:56, `amq setup -agents claude` failed with `agent "claude" is not available through a supported adapter probe`. The runner has no claude, and this host does, so it passed here.

**The probe** (AMQ v0.116.2, internal/launch/adapter_claude.go:29-57, `ClaudeAdapter.Capabilities`):
- `LookPath(claude)`;
- `claude --version`, whose first whitespace field is the provider version (`claudeVersion`, :271);
- `claude --help`, whose output must contain both `--session-id <uuid>` and `--resume [value]`.

That is more than a version line. `amq setup -h` says setup "detects Claude, Codex, Cursor, Grok, and pi through their adapter capability probes", so no agent is probe-free and the stub is the cheapest option.

**Fix (ruling D3).** `withClaudeProbeStub` gives the setup call alone a private 0700 temp directory, first on PATH in that call's env. The directory holds an executable `claude` that answers `--version` (`2.1.0 (Claude Code)`) and `--help` (the two flag lines) and exits 2 on anything else. Nothing else in the test sees it. A setup refusal still fails the test (`realAMQCommand` calls `t.Fatalf`); it never skips.

**The sweep.** Every `AMQ_SQUAD_REAL_AMQ`-gated test ran under a PATH of only the Go toolchain's bin directory and `/usr/bin:/bin:/usr/sbin:/sbin`, with no claude, codex, tmux or Homebrew directory (git is `/usr/bin/git`). Results:

| test | file | host tool beyond amq and git |
| --- | --- | --- |
| TestRealLaunchapiInspectSessionTargetShape | internal/cli/liveness_real_launchapi_target_test.go | none |
| TestRealAMQGateCloseReplyRefsCompatibility | internal/cli/real_amq_compatibility_test.go | none |
| TestRealAMQDoctorGlobalRepairScope | internal/cli/real_amq_compatibility_test.go | none |
| TestRealAMQCompatibility (11 subtests) | internal/cli/real_amq_compatibility_test.go | none |
| TestRealAMQFixtureEnvSanitizesManagedContext | internal/cli/real_amq_env_test.go | none |
| TestRealAMQProcessEnvSanitizesManagedContext | internal/cli/real_amq_env_test.go | none |
| TestNestedWorktreeRootDiscoveryOnAMQ074 | internal/cli/real_amq_nested_worktree_discovery_test.go | claude (fixed: probe stub) |
| TestCompiledIntentAcceptedByReleasedAMQ | internal/launchintent/golden_amq_test.go | none |
| TestApplyForwardsSubjectSchemaFromPrepareResult | internal/adoptionseam/adoptionseam_test.go | claude and tmux (fixed: probe stubs) |

`TestApplyForwardsSubjectSchemaFromPrepareResult` runs `adoptionseam.Prepare` in-process:
- Prepare resolves the seat's `claude` (it failed with `resolve claude executable: exec: "claude": executable file not found` once claude was off PATH).
- It detects the tmux launcher (`launcher_not_available` once tmux was off PATH).

It now sets PATH to a private directory holding the same claude stub and a `tmux` stub that answers only `-V` (`tmux 3.5a`, inside v0.116.2's `>=3.2 <4.0`, internal/launch/tmux.go:20 and tmux_command.go:80). Apply still passes the caller_context and subject_schema validation the test exists for (internal/launch/prepare.go:567). It then reaches the create step and gets the stub's refusal (`backend_create_failed`, recorded in lane-pinned-minimal.txt).

Found on the way: with a real tmux and claude, this test's Apply ran `tmux new-session … <adoptionseam.test> coop exec … claude` on the host's default tmux server. That session died at once, but the test was starting the test binary in a host tmux session. With the stub it opens nothing.

The lane installs tmux on the runner (ci.yml) and no gated test now needs it. The lane step itself is unchanged.

**Rows.**
- The pinned lane as ci.yml runs it, against `~/go/bin/amq` (0.116.2), with PATH stripped of every directory that holds a claude: all green (`5-ci-evidence/lane-pinned-noclaude.txt`).
- The same lane with the minimal PATH: all green (`lane-pinned-minimal.txt`).
- Revert D3a: the setup call without the stub fails with the CI message.
- Revert D3b: adoptionseam without the stubs fails with `resolve claude executable`.

**Not run here:** the canary lane (AMQ latest, v0.119.0 on the runner). It is not in the module cache, and fetching it reaches the network. If its claude probe asks for more than v0.116.2's, the canary (non-blocking) will say so.

## Revert check

`5-ci-evidence/revert-check-d.txt`. Every revert was applied in the working tree, its rows run, and the file restored with `git checkout`; the tree was clean after the check.

| item | revert | row(s) | reverted | restored |
| --- | --- | --- | --- | --- |
| D1a | `DialPrivate` dials the recorded path by name (pre-fix SendDecision) | TestDialPrivateRefusesAPlantedEntry | FAIL | PASS |
| D1a | same | TestDecisionSocketDialsUnderARootPastSunPath | PASS on darwin (no long path recorded; Linux reasoning above) | PASS |
| D1 | (none) | the two CI failures | n/a | PASS |
| D2a | `seededDeadPID = 42` | TestSeededLaunchPIDIsNeverALiveProcess | FAIL | PASS |
| D2b | `seededDeadPID = 1` (live here, as 42 is on the runner) | TestRemoteAttachOnce | FAIL (`starts 1`) | PASS |
| D3a | the setup call without the claude stub, PATH stripped of claude | TestNestedWorktreeRootDiscoveryOnAMQ074 | FAIL (CI message) | PASS |
| D3b | adoptionseam without the probe stubs, minimal PATH | TestApplyForwardsSubjectSchemaFromPrepareResult | FAIL | PASS |

## Gates run here

The host was quiet before the live rows: `ps -axo command` showed no seat-exec, scheduler run, pi run, amq coop, amq-remote or amq-squad lead/team/launch process. `tmux ls` showed no `amq-` session after the lanes.
- `make ci` (darwin): fmt-check, vet, and `go test ./...` (every package) all green.
  - It then stopped at docs-html-check: `docs/skills.html is stale`. That is this host's pandoc 3.11 against CI's 3.8.3, and the branch touches no skill or doc source.
  - The remaining targets, run with `make -k`:
    - green: release-validator-test, go-files-scope-test, claude-hooks-test;
    - not run: skills-check, because PyYAML is absent on this host and installing is out of bounds;
    - failed: skill-command-check and skill-invocation-check. The flags and verbs they report as unknown (`--session`, `evidence run`, `amq thread`) are in plugins/skills-src/cli/SKILL.md. The branch changes no command surface; `task widen`'s flags are unchanged. This is for the lead to confirm against v3.
- The shuffled internal/cli suite (`go test -shuffle=on ./internal/cli`): green.
- `GOOS=linux go vet` and `GOOS=linux go test -c` for schedproc, seatlaunch and cli: green.
- The credential-shape test (internal/redact) over docs and the evidence: green.
- No test reaches the network and no test formats the environment (ruling 18).
