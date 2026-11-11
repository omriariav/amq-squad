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
