# amq-squad 3.0, phase 5 test hygiene: outcome log

Branch `v3-5-hygiene`, cut from `v3` at 1bc8c680. Tests only; one commit per item of the spike log's "Phase 5 test-hygiene list". Evidence is in `docs/v3-spike/5-hygiene-evidence/`, passed through `scripts/redact-evidence`.

## Item 1: TestRealPTYWaitPostureRefusesNamedGateBeforeTimeout

Evidence of the flake: `docs/v3-spike/2b4-evidence/fix4-gate-run1-pty-flake.txt` (16.4 s under the full run, 5.4 s alone).

Mechanism: the test types a command into a fresh tmux pane and times it from send-keys to the done-marker with a fixed 5 s deadline. The timed step includes the pane's login shell starting (rc files, prompt hooks) and then running the binary; under full-suite load that startup alone eats most of the 5 s. The refusal itself is fast. The check guards against a different failure, the dispatch blocking until `--wait-timeout`.

Fix (test only): before timing, run `amq-squad version` through the pane and wait for its marker (2 minute bound; a synchronisation point). That measured time is the load yardstick: the dispatch deadline is max(5 s, 10 x measured), capped at half the wait timeout, and `--wait-timeout` is 60 s, so a real block still fails but a load spike does not.

Revert check (`item1-revert-check.txt`): CPU load alone (72 busy loops) did not reproduce the failure (old test passed at 6.9 to 8.3 s); a pane shell that takes 7 s to start does. With the fix reverted the test fails ("dispatch did not refuse before deadline"); restored, it passes (11.6 s).

## Item 3: internal/seatlaunch TestExpiredTokenIsRefused

Evidence of the flake: `docs/v3-spike/2b2b-evidence/fix5-gate-first.txt` ("TempDir RemoveAll cleanup ... launch/cto: directory not empty", no assertion failing).

Mechanism: `Launch` returns with the proof socket's window still open (`res.proof`; production waits for it with `Result.WaitProofWindow`). A goroutine polls every 200 ms and, once every ticket is past its deadline, runs `close()`, which marks each unused token expired with `launch.UpdateStaged` (a lock file and a temp-and-rename write under `launch/<handle>`). The test returned and TempDir's RemoveAll started while that goroutine was still to fire, so RemoveAll met a file being created. It reproduced at will: 4800 runs across 16 parallel processes failed in 16 of 16 processes (192 runs on the cleanup error).

The same reproduction exposed a second race in the same test: it moved the proof clock past the TTL inside `onApply`, before the prover process was started. The window poll shares that clock, so it saw every ticket expired and closed the socket while the prover (slow to start under load) was still dialling, giving `helper_dial_failed` (153 runs).

Fix (test only, no production change): `proofLaunch` (the shared helper of the proof tests) registers `res.proof.close` with `t.Cleanup` (close is synchronous and idempotent; cleanups run last-in first-out, so it finishes before TempDir is removed). The expired-token test sets the clock offset from the existing `judgeHook` seam, which runs after the proof message is read and before it is judged. The cleanup error is not swallowed or ignored.

Revert check (`item3-revert-check.txt`): the pre-fix binary fails in 16 of 16 processes (both kinds above); the fixed binary passes 4800 of 4800 runs.

## Item 2: internal/mergetrain under -race

Measured, `go test -race -count=1 -timeout 45m -v ./internal/mergetrain/`, on a host with no seat, scheduler or lead process:
- Before: 944.2 s (`item2-before-race.txt`). Slowest tests: TestTrainRebindsAfterItsRebaseAndRefusesAnUnboundPush 150.6 s, TestAcceptance1FiveCleanPRsLandInOrder 101.4 s, TestGatesCarryAndMatchTheSquadIdentity 81.2 s, TestAcceptance2Refusals 76.1 s (the fixture tests ran one after another, each gh call starting the race-instrumented test binary and running real git).
- After: 47.5 s (`item2-after-race.txt`); the longest test is the subprocess pair row at 46 s.

Mechanism: the fake gh was the test binary itself, re-executed through `GH.Bin` for every gh call (`TestMain` with `FAKE_GH_STATE`). A race-instrumented binary's start costs seconds, and a five-PR train makes dozens of gh calls.

Fix:
- Production seam (the only non-test change): an unexported `GH.runner` field in `internal/mergetrain/gh.go`. When nil (always, in production) `GH.run` execs gh exactly as before; the error text is built in one place for both paths. 15 lines; `docs/v3-manifest.yaml` current_lines for gh.go 333 to 348.
- `fakegh_test.go`: `fakeGH` is split into `fakeGHRun(statePath, args, stdout, stderr)` (the same fake, the same bare-repository git work, the same `calls.log`) and a wrapper for the subprocess. `inProcessRunner` calls it and turns a nonzero exit into the same `exit status N` error; a panic in the fake becomes exit status 2, as the crashed subprocess would have.
- `newFixture` now wires `GH.runner`; `newSubprocessFixture` restores the exec path for the subprocess rows.
- `t.Parallel()` on every test (and subtest) that builds a fixture (each has its own TempDir, bare repository and state file; no test sets a package variable or environment).

Subprocess rows (gh's arguments and output parsing through the real exec path):
- `TestSubprocessGHLandsARebasedPair` (new): PRs 2 and 4 land, PR 4 rebased after PR 2; asserts the merged state, the order, the rebase, and that pr view, updatePullRequestBranch, compare, check-runs, the rules read and the PUT merge were each called.
- `TestRequiredCheckIsBoundToItsAppAndLatestResult` (end-to-end half): check-runs of two apps, the status read.
- `TestDriftAlertsOnUngatedTagsAndReleases`: tags, releases and rulesets reads.

Tests whose mechanism changed (fake gh in process instead of the re-executed binary; every assertion kept, none weakened): TestAcceptance1FiveCleanPRsLandInOrder, TestAcceptance2Refusals (all subtests), TestAcceptance5NoRulesetRefusesToStart (all subtests), TestDryRunMergesAndRaisesNothing, TestAcceptedGateLiftsExactlyThatRefusal, TestManualModeStopsBeforeEachMerge, TestSeatProcessHoldsNoMergePath, TestNewHeadAfterPlanIsRechecked, TestHeadMovingDuringTheCheckIsReadAgain, TestManualConfirmationIsForTheHeadThatMerges, TestCIAbsentOnABehindBaseIsRebasedNotRefused, TestAuditFailureStopsBeforeAnyMerge, TestDriftBindsAGateToOneReleaseAndOneTagCommit, TestRealRunReadsTheTaskBoundToThePR, TestBindingWithoutAHeadIsUnbound, TestDriftBaselineMigratesOnceAndAnInvalidOneStops, TestTrainRebindsAfterItsRebaseAndRefusesAnUnboundPush, TestLocalPushTargetRefusesARealRun, TestGatesCarryAndMatchTheSquadIdentity. Unchanged mechanism (still subprocess): the drift tags/releases test and the end-to-end half of the required-check test. New: TestSubprocessGHLandsARebasedPair. Tests with no fixture (matchers, protection evaluation, custody) are untouched.

Revert check (`item2-before-race.txt`): the before run is the revert (every fixture through the subprocess); it passes in 944 s, against 47.5 s with the fix. The fix changes time, not results, so the revert shows slowness only, as the brief expected.

## Reviewer's narrow pass: two additions

- `internal/seatlaunch`: `TestTokenMintedSixtyOneSecondsEarlierIsExpired` restores the former contract as a deterministic row. A proof server on a test clock mints a ticket, the clock moves 61 s, and the proof message is judged with no hook between read and judgement: `proof_expired`, twice (a retry stays refused), and the seat's state says `proof_expired`. `TestExpiredTokenIsRefused` keeps the through-the-socket path with the clock moving at judgement.
- `internal/cli`: the PTY test starts tmux with `-f /dev/null`, so it does not load the host's tmux configuration. The warm-up's remaining wall-time bound (2 minutes, for the pane shell to run the marker command) is the accepted shape: it is a hang guard on a synchronisation point, not a timing assertion.
- Gate re-run on the committed head on a quiet host: `gate.txt` replaced.

# Second section: TestDecisionAdapterKillsAHungGuardGroup

Branch `v3-5-hygiene2`, cut from `v3` at f337b4a5. Before evidence: `hygiene2-before-full-run.txt` (the failing block of today's merge gate: "no sleeper pid", test at 1.00 s).

Mechanism: the adapter's watchdog (the test sets `guardWatchdog` to 1 s) starts at the adapter's entry, and the stand-in guard (`/bin/sh`: start, fork `sleep 30`, `echo $! > file`) runs inside that window. Under full-suite load the shell start alone can exceed 1 s, so the watchdog fired, killed the group and returned before the stand-in wrote the sleeper's pid: the test then read an empty file once. A second weakness in the same read: `echo $! > file` creates the file before it writes, so even a ready stand-in can be read empty. Production timeout is untouched.

Fix (test only):
- The stand-in publishes the pid atomically (write to a temp name, `mv` into place): the pid file is its readiness signal.
- Before the adapter runs, the test starts the stand-in once on its own, waits for that readiness file (30 s hang guard), measures the step and kills it. The adapter's watchdog is max(1 s, 10 x measured), the elapsed bound is watchdog + 3 s. The watchdog is still what ends the guard; the test only stops it racing the stand-in's start.
- After the adapter returns, the pid file is waited for (30 s hang guard) instead of read once.
- Kept: the hung guard's group is killed, the sleeper is gone within 2 s, nothing on stdout, the timeout log line on stderr.

Revert check (`hygiene2-revert-check.txt`): under 160 busy loops on 18 cores, 20 runs: old test failed 2 of 20 ("no sleeper pid"), new test 20 of 20 pass. `hygiene2-race-count20.txt`: `go test -race -count=20` of the new test passes on an idle host. Gate: `gate-hygiene2.txt`.
