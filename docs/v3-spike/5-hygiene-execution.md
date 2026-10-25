# amq-squad 3.0, phase 5 test hygiene: outcome log

Branch `v3-5-hygiene`, cut from `v3` at 1bc8c680. Tests only; one commit per item of the spike log's "Phase 5 test-hygiene list". Evidence is in `docs/v3-spike/5-hygiene-evidence/`, passed through `scripts/redact-evidence`.

## Item 1: TestRealPTYWaitPostureRefusesNamedGateBeforeTimeout

Evidence of the flake: `docs/v3-spike/2b4-evidence/fix4-gate-run1-pty-flake.txt` (16.4 s under the full run, 5.4 s alone).

Mechanism: the test types a command into a fresh tmux pane and times it from send-keys to the done-marker with a fixed 5 s deadline. The timed step includes the pane's login shell starting (rc files, prompt hooks) and then running the binary; under full-suite load that startup alone eats most of the 5 s. The refusal itself is fast. The check guards against a different failure, the dispatch blocking until `--wait-timeout`.

Fix (test only): before timing, run `amq-squad version` through the pane and wait for its marker (2 minute bound; a synchronisation point). That measured time is the load yardstick: the dispatch deadline is max(5 s, 10 x measured), capped at half the wait timeout, and `--wait-timeout` is 60 s, so a real block still fails but a load spike does not.

Revert check (`item1-revert-check.txt`): CPU load alone (72 busy loops) did not reproduce the failure (old test passed at 6.9 to 8.3 s); a pane shell that takes 7 s to start does. With the fix reverted the test fails ("dispatch did not refuse before deadline"); restored, it passes (11.6 s).
