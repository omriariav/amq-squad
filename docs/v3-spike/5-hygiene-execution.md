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
