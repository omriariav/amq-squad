# 3.0.1 t18: the buzz dry run waits on the serve loop's own signal

Branch v3-301-t18 from origin/v3 at a1a67151. Host: the owner's macOS laptop
(Apple M5 Max, 18 cores, macOS 26.6.2, go1.27.1), 2026-10-30. AMQ binaries:
the installed amq, amq-remote and amq-owner-sign 0.116.2 (the floor).

## The defect

`TestOwnerScriptDryRun` (internal/buzz/enroll_test.go) waited on the serve
loop with polling deadlines: `waitFor` checked a condition every 200 ms and
failed the test when its wall-clock bound passed. Under load (the full-suite
gates in the spike log, and the shared macOS runner) one of the 20 s bounds
ran out while every behaviour assertion was correct.

## Before: one polling deadline per step

In the dry run itself:

| line | bound | step |
|---|---|---|
| 230 | 20 s | profile and online status from the enrollment endpoint |
| 242 | 15 s | offline at the enrollment endpoint's graceful stop |
| 294 | 30 s | online while the lead runs |
| 305 | 15 s | offline after the lead stops |
| 347 | 30 s | online before the DMs |
| 365 | 20 s | serve delivers the answer into the attach inbox |
| 382 | 5 s | the verifier's connection closes |
| 386 | 20 s | serve posts the accepted result row |
| 401 | 20 s | serve delivers the route |
| 412 | 20 s | serve posts the routed result row |
| 423 | 15 s | offline after the serve rows |

The brief named the five 20 s ones; the other six are the same pattern on the
same loop. The rehearsal the dry run calls (`rehearseOwnerRun`,
rehearsal_test.go) had six more (30 s, then five of 20 s) and a fixed one
second sleep after the owner's `/status`. Each script run under `ptyRun` had
its own 90 s bound, which failed the run through its exit code.

Run time before the change, this host, unloaded: 32.13 s and 32.44 s.

## After: one budget, every step awaited on a signal

- **The signal.** The serve loop is the external amq-remote process; what it
  emits lands on the test's websocket relay (internal/buzz/relay_test.go).
  The relay now rings a bell after each event it stores and each connection
  it opens, signs in or closes. `await` (internal/buzz/signal_test.go) checks
  the step's condition, then blocks until the next ring and checks again.
  Nothing polls.
- **Delivery into the attach inbox.** Serve posts nothing on the relay at
  the moment it writes the inbox, so the step awaits serve's running row for
  that DM. Serve's core sets a request running only on the mailbox adapter's
  admission, and the admission returns after the prompt is written (amq-remote
  v0.116.2, internal/remote/core/transition.go `causeAdmitted` and
  internal/remote/mailbox/mailbox.go `submitLocked`). The test then reads the
  inbox as a behaviour check: exactly one prompt, with the delivery shape.
- **The rehearsal.** The `remote attach` output buffer rings the same bell, so
  the attach loop's own lines (`started`, `approved`, the routed lead,
  `stopped`) wake the waits on it. The sleep after `/status` is now a wait for
  serve's own reply row to that DM.
- **The budget.** One context per run, `runBudget` = 5 minutes, starting after
  the amq-squad build. It exists only to end a hung run. The script runs, the
  rehearsal's commands and every await run under it. When it ends, the
  failure names the step, for example
  `hung: no signal for "serve delivers the answer into the attach inbox" (the run's 40s budget ran out)`
  (shown with the budget cut to 40 s and a signal that never comes, not
  committed). `ptyRun` takes the caller's context; the crash test passes its
  former 90 s.
- **Timing and behaviour are apart.** The behaviour checks are unchanged and
  run after each signal. The MEASURED log lines stay as logs. No duration
  fails the test.
- **Production code.** No production line changed. The bell is in the test
  relay; no hook was added to the package.

## The other waitFor calls in internal/buzz

| test | waits | treatment |
|---|---|---|
| `TestVerifierConnectionOnTheRelay` (connection_test.go) | three 5 s waits for a relay connection to close | converted: the same relay rings on the close; same budget |
| `TestOwnerSignerCrashLeavesNoKey` (crash_test.go) | two 30 s waits for files the signer shim writes during the enrollment script | left: no serve loop and no relay signal at that point; the files would need a file watch. Not a timing contract, so a candidate for the same treatment later |

Left on purpose as real timing contracts:

- `TestVerifierConnectionOnTheRelay` checks that a stalled fetch returns
  within 3 s with `VerifyTimeout` at 300 ms. That is the cap under test.
- The dry run's 1.1 s sleep before the renewal: a new grant window must end
  after the current one, and the expiry has one-second resolution.
- `ptyRun` types the body prefix after 4 s and `stopAttach` kills after 20 s.
  Both are delays, not checks, and neither fails the test.

## Runs after the change

Gate: `go build ./... && go vet ./internal/buzz && go test -count=1 ./internal/buzz`
green (41.2 s for the package).

`go test -count=10 -run TestOwnerScriptDryRun -v ./internal/buzz`, three
times, each 10 of 10 PASS:

| condition | load average at end | per-run time, s | total |
|---|---|---|---|
| no added load (other jobs on the host) | 79 | 29.9 to 31.2 | 304.1 s |
| 36 `yes` CPU hogs on 18 cores | 54 | 29.9 to 33.4 | 310.1 s |
| the whole repository's `go test -count=1 ./...` in parallel | 4.6 (peak 58 at start) | 30.2 to 41.6 | 339.2 s |

The parallel full suite also passed, its own dry run included. Per-run times:

- no added load: 30.29 31.16 30.06 30.30 30.00 29.88 30.97 30.47 30.30 30.12
- CPU hogs: 30.21 30.22 30.40 32.16 33.40 32.15 30.87 30.34 30.09 29.94
- full suite: 41.63 38.20 34.65 32.29 32.04 30.94 30.19 30.40 33.02 33.57

The step from the owner's answer to serve's running row takes about 1 s
(serve's tick), and to the result row about 2 s, in every run. The slowest
full-suite run took 1.994 s and 3.04 s.

## The reverting check

The forward to lead-b in `Deliver` (internal/buzz/route.go) was replaced by a
fake message id, uncommitted:

```
-	lid, err := m.Send(ctx, fwd)
+	lid, err := "t18-revert-dropped", error(nil) // REVERTING CHECK: the forward is never sent
+	_ = fwd
```

The dry run then fails on the behaviour assertion, not on a timeout:

```
enroll_test.go:423: the routed message is not in lead-a's and lead-b's inboxes
--- FAIL: TestOwnerScriptDryRun (25.63s)
```

route.go was restored with `git checkout` and the tree was clean after.

## Fix round (Codex pass on 20717346)

- **The budget ends the whole process tree.** `exec.CommandContext`'s default
  cancel kills only the direct child, so serve or attach could outlive the
  budget and race the scratch HOME's removal. Each run now goes through
  `guardTree` (internal/buzz/proctree_test.go): the root starts its own
  process group, and on cancel the guard records the root's descendants
  from `ps` and the group, then sends them TERM. That covers the plain
  script runs, the pty runs, the rehearsal commands, `amq init` and
  `remote attach`. The descendants walk is what reaches the pty child, since
  script(1) starts it in its own session outside the group. After the run
  returns, `reap` sends KILL to anything left after a 3 s grace. It waits
  until no recorded pid and no group member is alive, and only then does any
  cleanup run. A survivor fails the test.
- **Hang path, the reap row.** These checks used a temporary budget and were
  not committed:

  ```
  REAPED: run 2 (owner signs through the pipe): process group 67954 and pids [67955 67973 67974 67954] (the root and its descendants when recorded), none alive
  hung: run 2 (owner signs through the pipe) did not finish (the run's 5s budget ran out)
  REAPED: run 1 (phase A): process group 68701 and pids [68703 68701] (the root and its descendants when recorded), none alive
  hung: run 1 (phase A) did not finish (the run's 300ms budget ran out)
  ```

  A `ps` diff of amq-remote, script(1) and enroll.sh processes before and
  after each check showed no new process left.
- **`remote attach`.** `stopAttach` records attach's tree before the
  interrupt and reaps it after. Every normal run logs
  `REAPED: rehearsal: 4.2 remote attach: ... none alive` (10 of 10 below).
- **`amq init` in the dry run.** It runs under the budget and with the
  test's environment, so it gets the scratch HOME and
  `AMQ_NO_UPDATE_CHECK=1`. Before, it inherited the real environment, and
  amq v0.116.2 starts its update notifier before it dispatches init
  (internal/cli/cli.go). `amq --version` returns before the notifier, so the
  version probes are unchanged.
- **The amq-squad build is offline.** The test builds amq-squad with
  `go build` under the real HOME, before the budget starts, now with
  `GOPROXY=off` (reviewer ruling 2026-10-30). The build compiles this tree
  from the same go.mod the test binary was just built from, so the module
  cache holds every module by construction. A missing module fails the
  build closed with a clear message instead of being downloaded. The crash
  test builds through the same helper. `GOFLAGS=-mod=mod` was not needed.
  Fail-closed check against an empty module cache
  (`GOMODCACHE=<empty dir> GOPROXY=off go build ./cmd/amq-squad`): exit 1,
  nothing fetched (the cache held only go's lock files), and each import
  reads:

  ```
  ../nostr/event.go:26:2: module lookup disabled by GOPROXY=off
  ```

Runs after the fix round, this host, with no added load (load average 6.6
to 9.2):

- Gate: `go build ./... && go vet ./internal/buzz && go test -count=1 ./internal/buzz`
  green (41.3 s).
- `go test -count=10 -run TestOwnerScriptDryRun -v ./internal/buzz`: 10 of
  10 PASS, 308.2 s. Per-run times: 30.84 29.81 31.89 30.71 32.91 30.31
  30.32 30.42 30.54 30.17.
