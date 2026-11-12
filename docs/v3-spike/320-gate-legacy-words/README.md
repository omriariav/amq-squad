# Gate-store legacy warning words

The source and test line numbers in this note are measured on the final tree based on `e70c33db6e13ae637748a681b2e93376b26a32da`.

## What was seen

The squad lead's first v3.2.0-rc.2 screen at 2026-11-11T13:54:07Z showed:

```text
waiting on you (0)
  Nothing is waiting on you.
  ! the gate store could not be read; its error is below the line
----------
  gate store: gate clear-0a227ca8d88dc97c65c540a4d0fa18a7 is a legacy quarant...
```

The gate store had been read. Status held three warnings for stored legacy `quarantine_clear` records and no directory-read error.

## Cause and forms

`internal/cli/status.go:88-91` names the two warning prefixes. A failed directory read is appended at `internal/cli/status.go:99-100`; a stored legacy record is appended in id order at `internal/cli/status.go:102-105`. The directory reader returns its non-not-exist error at `internal/cli/hook_store.go:53-61`. The screen separates and renders the two forms at `pi/extensions/lib/screen.ts:335-348`.

The read-failure form is `gate store: <error>`. The legacy-record form is `gate store legacy: ` followed by `gatestore.LegacyLine`, one warning per stored record.

The screen prints the could-not-be-read line and its detail only for the read-failure form. It prints one count below the rule for all legacy-record warnings: singular for one, plural for more than one. It never prints a legacy record's gate id. The count is independent of a gate alert or read failure.

An old status document from rc.2 or earlier still carries a legacy record under the `gate store: ` form. The screen and binary normally ship together, so this is the mixed-version case only, and the screen continues to take its read-failure path.

The warning table in `docs/v3-spike/320-0d-screen/README.md` records the earlier form and is unchanged.

## Rows

G-legacy is `internal/cli/status_gate_warnings_test.go:26-34`. One stored legacy record produces exactly the new prefix plus `gatestore.LegacyLine`, and no read-failure warning.

G-legacy-3 is `internal/cli/status_gate_warnings_test.go:36-54`. Three records written out of order produce three legacy warnings in id order.

G-read is `internal/cli/status_gate_warnings_test.go:56-69`. A regular file at the gates path produces exactly one read-failure warning and no legacy warning.

The carried gate-status/status row is `internal/cli/rollback_rows_test.go:74-83`; only its status expectation takes the new prefix.

S-legacy-3 is `pi/test/screen.test.ts:209-218`, S-legacy-1 is `pi/test/screen.test.ts:220-225`, S-read is `pi/test/screen.test.ts:227-234`, S-both is `pi/test/screen.test.ts:236-248`, and S-old is `pi/test/screen.test.ts:250-256`.

## The reverts

RG1 edited `internal/cli/status.go:105`.

Before: `warnings = append(warnings, gateStoreLegacyWarningPrefix+gatestore.LegacyLine(id, kind))`

After: `warnings = append(warnings, gateStoreReadWarningPrefix+gatestore.LegacyLine(id, kind))`

Command: `go test ./internal/cli -run 'TestStatusGateWarnings|TestRollbackLegacyGateLineInGateStatusAndStatus' -count=1`

Exit code: 1.

First failure: `--- FAIL: TestRollbackLegacyGateLineInGateStatusAndStatus (0.03s)`

First test line: `rollback_rows_test.go:83: status warnings = ["gate store: gate clear-0a227ca8d88dc97c65c540a4d0fa18a7 is a legacy quarantine_clear gate (a kind this build no longer carries); skipped"]`

G-legacy and G-legacy-3 also failed. After restoration, `git diff --stat -- internal/cli/status.go` returned `internal/cli/status.go | 11 +++++++++--`.

RG2 edited `internal/cli/status.go:100`.

Before: `warnings = append(warnings, gateStoreReadWarningPrefix+err.Error())`

After: line removed.

Command: `go test ./internal/cli -run 'TestStatusGateWarnings|TestRollbackLegacyGateLineInGateStatusAndStatus' -count=1`

Exit code: 1.

First failure: `--- FAIL: TestStatusGateWarningsReadFailure (0.02s)`

First test line: `status_gate_warnings_test.go:67: read-failure warnings = []`

After restoration, `git diff --stat -- internal/cli/status.go` returned `internal/cli/status.go | 11 +++++++++--`.

RS1 edited `pi/extensions/lib/screen.ts:348`.

Before: ``rec(`${legacyGateWarnings.length} legacy gate record${legacyGateWarnings.length === 1 ? "" : "s"} skipped`);``

After: line removed.

Command: `cd pi && node --test --test-reporter=spec test/screen.test.ts`

Exit code: 1.

First failure: `✖ three legacy gate warnings print one count below the rule and no read failure or gate id`

First assertion line: `AssertionError [ERR_ASSERTION]: Expected values to be strictly deep-equal:`

After restoration, `git diff --stat -- pi/extensions/lib/screen.ts` returned `pi/extensions/lib/screen.ts | 6 ++++++`.

RS2 edited `pi/extensions/lib/screen.ts:338`.

Before: `const gateWarn = st.warnings.find((w) => w.startsWith("gate store:"));`

After: `const gateWarn = st.warnings.find((w) => w.startsWith("gate store"));`

Command: `cd pi && node --test --test-reporter=spec test/screen.test.ts`

Exit code: 1.

First failure: `✖ three legacy gate warnings print one count below the rule and no read failure or gate id`

First assertion line: `AssertionError [ERR_ASSERTION]: amq-squad: lead cto (pid 4300, pane %3) · root <fixture-root>`

After restoration, `git diff --stat -- pi/extensions/lib/screen.ts` returned `pi/extensions/lib/screen.ts | 6 ++++++`.

RS3 edited `pi/extensions/lib/screen.ts:343-346`.

Before:

```ts
} else if (gateWarn) {
	out.push("  ! the gate store could not be read; its error is below the line");
	rec(short(gateWarn, 200));
}
```

After: branch removed.

Command: `cd pi && node --test --test-reporter=spec test/screen.test.ts`

Exit code: 1.

First failure: `✖ a gate-store read failure keeps its warning above and detail below the rule`

First assertion line: `AssertionError [ERR_ASSERTION]: Expected values to be strictly equal:`

S-both and S-old also failed. RS1 and RS2 did not fail S-old. After restoration, `git diff --stat -- pi/extensions/lib/screen.ts` returned `pi/extensions/lib/screen.ts | 6 ++++++`.

## What ran

All Go commands used a temporary HOME, `GOPROXY=off`, the existing module cache, and a task-local build cache.

`go build ./...` exited 0. Go printed only the non-fatal module-stat-cache sandbox warning.

`go vet ./internal/cli` exited 0.

`go test ./internal/cli -run 'TestStatusGateWarnings|TestRollbackLegacyGateLineInGateStatusAndStatus' -count=1` exited 0: 4 passed, 0 skipped, 0 failed.

`npm ci --ignore-scripts --offline` exited 0 from `pi/`: 118 packages installed from cache and 0 vulnerabilities.

`npm test` exited 0 from `pi/` on its socket-capable rerun: 145 passed, 1 skipped, 0 failed. Its first sandboxed run reached 135 passed, 1 skipped, and 10 socket-bind failures before the exact rerun.

`node scripts/lint.mjs` exited 0 from `pi/`: 32 files, pi 1.3.0.

`node --test --test-reporter=spec test/screen.test.ts` exited 0 from `pi/`: 32 passed, 0 skipped, 0 failed.

`go run ./scripts/redact-evidence -check docs/v3-spike/320-gate-legacy-words/` exited 0.

`git diff --check` exited 0.

The required host-path count command printed `0` and exited 1 because grep found no matching line.

`git status --porcelain` exited 0 and listed exactly the seven declared paths: the status contract, rollback row, status implementation, new status-warning rows, screen implementation, screen rows, and this note.

## Row S-alert

The row is `a gate alert and legacy warnings print the alert line and the count below the rule`, placed at `pi/test/screen.test.ts:258-273`.

A0 asserts that the rule line `----------` is found.

A1 asserts that exactly one line above the rule equals `  ! a gate needs a look; its alert is below the line`.

A2 asserts that a record below the rule, trimmed, equals `merge_gate_drift: gate g9: the recorded head moved`.

A3 asserts that a record below the rule, trimmed, equals `2 legacy gate records skipped`.

A4 asserts that no screen line contains `clear-`.

The screen code is unchanged; the row pins what `pi/extensions/lib/screen.ts` already does when a gate alert and legacy gate warnings are both present.

### The reverts

The console blocks under The reverts and What ran print the lines the brief asked for, not every line the command printed; the ✖ lines, the exit codes and the counts are complete.

RA1 edited `pi/extensions/lib/screen.ts:347`.

Before: `if (legacyGateWarnings.length) {`

After: `if (!gateAlert && legacyGateWarnings.length) {`

```console
$ cd pi && node --test --test-reporter=spec test/screen.test.ts
✖ a gate alert and legacy warnings print the alert line and the count below the rule
ℹ tests 33
ℹ pass 32
ℹ fail 1
```

Exit code: 1.

```console
$ git show HEAD:pi/extensions/lib/screen.ts > pi/extensions/lib/screen.ts
$ git diff --quiet -- pi/extensions/lib/screen.ts; echo $?
0
```

RA2 edited `pi/extensions/lib/screen.ts:340`.

Before: `if (gateAlert) {`

After: `if (gateAlert && !legacyGateWarnings.length) {`

```console
$ cd pi && node --test --test-reporter=spec test/screen.test.ts
✖ a gate alert and legacy warnings print the alert line and the count below the rule
ℹ tests 33
ℹ pass 32
ℹ fail 1
```

Exit code: 1.

```console
$ git show HEAD:pi/extensions/lib/screen.ts > pi/extensions/lib/screen.ts
$ git diff --quiet -- pi/extensions/lib/screen.ts; echo $?
0
```

RA3 added one line after `pi/extensions/lib/screen.ts:348`.

Before: no line after the count record.

After: `for (const w of legacyGateWarnings) rec(short(w, 200));`

```console
$ cd pi && node --test --test-reporter=spec test/screen.test.ts
✖ three legacy gate warnings print one count below the rule and no read failure or gate id
✖ a gate alert and legacy warnings print the alert line and the count below the rule
ℹ tests 33
ℹ pass 31
ℹ fail 2
```

Exit code: 1.

```console
$ git show HEAD:pi/extensions/lib/screen.ts > pi/extensions/lib/screen.ts
$ git diff --quiet -- pi/extensions/lib/screen.ts; echo $?
0
```

RA4 edited `pi/extensions/lib/screen.ts:342`.

Before: ``rec(`${gateAlert.kind}: ${short(gateAlert.subject)}`);``

After: line removed.

```console
$ cd pi && node --test --test-reporter=spec test/screen.test.ts
✖ a gate alert and legacy warnings print the alert line and the count below the rule
ℹ tests 33
ℹ pass 32
ℹ fail 1
```

Exit code: 1.

```console
$ git show HEAD:pi/extensions/lib/screen.ts > pi/extensions/lib/screen.ts
$ git diff --quiet -- pi/extensions/lib/screen.ts; echo $?
0
```

RA5 added one line directly after `pi/extensions/lib/screen.ts:339`.

Before: no line after the `legacyGateWarnings` declaration.

After: `const RULE = "";`

```console
$ cd pi && node --test --test-reporter=spec test/screen.test.ts
✖ the screen renders from a status fixture: the lead and the root, each block in plain words, the codes below the rule
✖ three legacy gate warnings print one count below the rule and no read failure or gate id
✖ one legacy gate warning prints the singular count
✖ a gate alert and legacy warnings print the alert line and the count below the rule
✖ the requests block marks each phone request relayed or refused, the name below the rule
✖ the widget measures the rendered screen with no fixed records, includes content at fixed plus three, and never exceeds the pane
ℹ tests 33
ℹ pass 27
ℹ fail 6
```

Exit code: 1.

```console
$ git show HEAD:pi/extensions/lib/screen.ts > pi/extensions/lib/screen.ts
$ git diff --quiet -- pi/extensions/lib/screen.ts; echo $?
0
```

With the rule line removed, five other tests of the file fail with this row.

### What ran

```console
$ cd pi && npm ci --ignore-scripts --offline
added 118 packages, and audited 119 packages in 2s
found 0 vulnerabilities
```

Exit code: 0.

```console
$ cd pi && node --test --test-reporter=spec test/screen.test.ts
ℹ tests 33
ℹ pass 33
ℹ fail 0
ℹ skipped 0
```

Exit code: 0.

```console
$ cd pi && npm test
ℹ tests 147
ℹ pass 146
ℹ fail 0
ℹ skipped 1
```

Exit code: 0.

```console
$ cd pi && node scripts/lint.mjs
lint ok: 32 files, pi 1.3.0
```

Exit code: 0.

```console
$ go run ./scripts/redact-evidence -check docs/v3-spike/320-gate-legacy-words/
```

Exit code: 0.

```console
$ git diff --check
```

Exit code: 0.

```console
$ grep -c '/Us''ers/' docs/v3-spike/320-gate-legacy-words/README.md
0
```

Exit code: 1, as expected for no matching line.

```console
$ grep -cE '[[:space:]]+$' docs/v3-spike/320-gate-legacy-words/README.md
0
```

Exit code: 1, as expected for no matching line.

```console
$ git status --porcelain --untracked-files=all
 M docs/v3-spike/320-gate-legacy-words/README.md
 M pi/test/screen.test.ts
```

Exit code: 0.

```console
$ git diff --stat
 docs/v3-spike/320-gate-legacy-words/README.md | 43 +++++++++++++++++++++++----
 pi/test/screen.test.ts                        |  1 +
 2 files changed, 39 insertions(+), 5 deletions(-)
```

Exit code: 0.
