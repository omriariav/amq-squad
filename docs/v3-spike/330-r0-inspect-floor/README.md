# 3.3.0 R0: remove the session Inspect floor

Line numbers as of `0c72f069e388ef461e6c0b341a9156c291a4d4a4`.
This change removes the session `launchapi.Inspect` floor, its status
observation field, and the tests that existed only for that floor. The lead's
`deadcode ./...` run on `8d66f50e` reported the floor chain unreachable from
`main`; the measured caller search below found its production caller inside the
kept-dead `classifyMemberStatusFromEntries` and that classifier's caller only in
the test helper. The status command's live collection is in
`internal/status/collect.go`, where the measured `launchapi` and classifier
search printed nothing. On that evidence, the deletion changes no command
entry point. The disk classifier, launch-record selectors, verdict vocabulary,
`agentLiveness.Live`, and `classifyMemberStatusFromEntries` remain.

## Measured caller chain

Before the deletion:

The following are abbreviated excerpts with verified line numbers; the base
excerpt omits seven test-helper calls and two additional comment matches.
(corrected, see Corrections)

```text
$ rg -n 'classifyAgentLivenessForRollup|classifyMemberStatusFromEntries|classifyMemberStatusForTest' --glob '*.go' .
internal/cli/liveness.go:349:// classifyAgentLivenessForRollup is the disk classifier plus the session
internal/cli/liveness.go:351:func classifyAgentLivenessForRollup(...)
internal/cli/status.go:338:func classifyMemberStatusFromEntries(...)
internal/cli/status.go:392: live := classifyAgentLivenessForRollup(...)
internal/cli/liveness_test.go:430:// classifyMemberStatusForTest classifies one roster member the way status does
internal/cli/liveness_test.go:432:func classifyMemberStatusForTest(...)
internal/cli/liveness_test.go:434: return classifyMemberStatusFromEntries(...)
```

The omitted matches in that command were calls from the ten tests listed below.
After the deletion, the same command prints only the two retained test calls,
their helper, and the kept-dead classifier:

```text
internal/cli/liveness_test.go:103: rec := classifyMemberStatusForTest(...)
internal/cli/liveness_test.go:128: rec := classifyMemberStatusForTest(...)
internal/cli/liveness_test.go:134:// classifyMemberStatusForTest classifies one roster member the way status does
internal/cli/liveness_test.go:136:func classifyMemberStatusForTest(...)
internal/cli/liveness_test.go:138: return classifyMemberStatusFromEntries(...)
internal/cli/status.go:333:func classifyMemberStatusFromEntries(...)
```

The lead's earlier `deadcode` result named `agentLiveness.Live`,
`classifyAgentLivenessFromDisk`, the Inspect-floor functions, and
`classifyMemberStatusFromEntries`. This note does not claim a new `deadcode`
run; that run belongs to the lead on the PR head.

## Removed identifiers

Lines below are their positions on the dispatched base.

| Base lines | Removed |
| --- | --- |
| `internal/cli/liveness.go:32-48` | The Inspect/observation sentence and fields `InspectSignal` and `LaunchapiObservation`. |
| `internal/cli/liveness.go:138-178` | `launchapiInspectOutcome`; its five outcomes; present/absent constants; `launchapiInspectSignal`; `launchapiObservationJSON`. |
| `internal/cli/liveness.go:179-209` | Observation lookup/notability and the `launchapiInspect` seam. |
| `internal/cli/liveness.go:211-278` | Cached and uncached session Inspect calls and memo. |
| `internal/cli/liveness.go:280-315` | `applyLaunchapiInspectionFloor`. |
| `internal/cli/liveness.go:317-347` | Observation and missing-launch-record detail helpers. |
| `internal/cli/liveness.go:349-355` | `classifyAgentLivenessForRollup`. |
| `internal/cli/status.go:228-232` | JSON `LaunchapiObservation` field and comment. |
| `internal/cli/status.go:389-394` | Rollup-wrapper call and observation assignment; replaced by a direct `classifyAgentLivenessFromDisk` call. |
| `internal/cli/liveness_test.go:29-36` | `stubLaunchapiInspect`. |
| `internal/cli/liveness_real_launchapi_target_test.go:1-78` | The real Inspect target-shape test file. |

The removal also leaves `context`, `filepath`, `sync` and the `launchapi` import
unused in `liveness.go`, so those imports are gone. `status.go` now calls:

```go
classifyAgentLivenessFromDisk(rec.AgentDir, root, profile, rec.Handle, m.Binary, probe)
```

## The ten tests

| Test on the base | Base lines | Disposition | Assertions before | Assertions after |
| --- | ---: | --- | --- | --- |
| `TestClassifierPresenceLiveWhenWriterUnknown` | 42-70 | kept unchanged | Presence verdict and status are live. | Same. |
| `TestStoppedLaunchRecordOverridesReusedLivePID` | 72-95 | kept unchanged | A stopped launch is stale/not live and detail names the stop. | Same. |
| `TestInspectLivenessUnknownFailsClosed` | 101-121 | removed | Inspect floor caps unknown/action-required signals. | None; floor removed. |
| `TestInspectLivenessParityWithPaneProbeForLiveSeats` | 128-155 | reduced and renamed `TestStatusRollupLiveSeatFromDisk` | Disk-backed status is live; Inspect outcome is present. | Disk-backed status is live. |
| `TestStatusRollupUnchangedForLegacyLaunches` | 161-188 | reduced | Exact live status/detail; Inspect outcome is not applicable. | Exact live status/detail `agent pid 5555 alive (codex)`. |
| `TestInspectAbsentConflictsWithLivePaneProbeFailsClosed` | 196-226 | removed | Inspect absent caps a disk-live seat and records the Inspect outcome. | None; floor removed. |
| `TestInspectErrorFallsBackToExistingSignalsWithWarning` | 233-263 | removed | Inspect error preserves live status and exact detail `agent pid 5555 alive (codex)`, and records the error outcome and evidence. (corrected, see Corrections) | None; floor removed. |
| `TestLaunchapiInspectMemoCollapsesRosterToOneCall` | 273-320 | removed | Three members remain live with a present Inspect outcome and exactly one Inspect call. (corrected, see Corrections) | None; call and memo removed. |
| `TestStatusSurfacesNotableLaunchapiObservation` | 329-390 | removed | Status remains live; detail names the notable observation, matched struct fields are asserted, and selected serialized JSON fields are asserted. (corrected, see Corrections) | None; observation removed. |
| `TestStatusOmitsUnremarkableLaunchapiObservation` | 396-428 | removed | The unremarkable observation is absent from detail and the matched observation is populated with `Runnable=true`; JSON serialization is not asserted. (corrected, see Corrections) | None; observation removed. |

The six removed tests contain floor, call, memo, or observation assertions.
Their disk-live fixture assertion is already retained by the two reduced
rollup tests.

## Focused classifier check

Command:

```text
go test ./internal/cli/ -run 'TestClassifier|TestStoppedLaunchRecord|TestStatusRollup' -count=1 -v
```

Output, exit 0:

```text
=== RUN   TestClassifierPresenceLiveWhenWriterUnknown
--- PASS: TestClassifierPresenceLiveWhenWriterUnknown (0.01s)
=== RUN   TestStoppedLaunchRecordOverridesReusedLivePID
--- PASS: TestStoppedLaunchRecordOverridesReusedLivePID (0.02s)
=== RUN   TestStatusRollupLiveSeatFromDisk
--- PASS: TestStatusRollupLiveSeatFromDisk (0.34s)
=== RUN   TestStatusRollupUnchangedForLegacyLaunches
--- PASS: TestStatusRollupUnchangedForLegacyLaunches (0.18s)
PASS
ok   github.com/omriariav/amq-squad/v3/internal/cli 0.999s
```

## Removed-name guard

`internal/adoptionseam/launchapi_removed_names_test.go` adds
`TestNoRemovedLaunchapiNames`. It walks module Go files, including tests; skips
`.git`, `node_modules`, `.worktrees` and nested modules; parses normal and
aliased imports of the launchapi package; and reports a file, line and selector
for any of the thirteen names removed by AMQ v0.139.0. It also requires at
least one launchapi-importing file to be visited.

Command:

```text
go test ./internal/adoptionseam/ -run TestNoRemovedLaunchapiNames -count=1 -v
```

Output, exit 0:

```text
=== RUN   TestNoRemovedLaunchapiNames
--- PASS: TestNoRemovedLaunchapiNames (0.16s)
PASS
ok   github.com/omriariav/amq-squad/v3/internal/adoptionseam 0.523s
```

### Recorded revert

I temporarily restored the launchapi import and this line in
`internal/cli/liveness.go`:

```go
var launchapiInspect = launchapi.Inspect
```

Then I ran:

```text
go test ./internal/adoptionseam/ -run TestNoRemovedLaunchapiNames -count=1 -v
```

Exit code: 1. First failing line:

```text
launchapi_removed_names_test.go:101: internal/cli/liveness.go:14: removed launchapi selector Inspect
```

The complete recorded output was:

```text
=== RUN   TestNoRemovedLaunchapiNames
    launchapi_removed_names_test.go:101: internal/cli/liveness.go:14: removed launchapi selector Inspect
--- FAIL: TestNoRemovedLaunchapiNames (0.15s)
FAIL
FAIL github.com/omriariav/amq-squad/v3/internal/adoptionseam 0.398s
FAIL
```

The temporary import and seam line were removed again before the passing checks.

## Required deletion and build checks

```text
$ grep -rnE 'launchapi' internal/cli/liveness.go internal/cli/status.go
(exit 1; no matches and no output; corrected, see Corrections)

$ grep -rnE 'InspectSignal|LaunchapiObservation|launchapi_observation|launchapiInspect|737|766' internal/cli/liveness.go internal/cli/status.go internal/cli/liveness_test.go
(exit 1; no matches and no output; corrected, see Corrections)

$ go build ./...
(exit 0; no output)

$ go vet ./internal/cli/ ./internal/adoptionseam/
(exit 0; no output)
```

## Kept-dead classifier users outside its two files

Command after the change:

```text
rg -n 'agentLiveness|agentLivenessVerdict|statusRecord|classifyAgentLivenessFromDisk|classifyMemberStatusFromEntries' --glob '*.go' --glob '!**/*_test.go' internal | grep -v -E 'internal/cli/(liveness|status)\.go'
```

Output: none. No non-test code outside `internal/cli/liveness.go` and
`internal/cli/status.go` names those types or functions. In particular, no
doctor row names them. The separate search:

```text
rg -n 'launchapi|classifyAgentLiveness|classifyMemberStatus' internal/status/collect.go
```

also printed nothing. This answers the measured question only; it is not a
reason to delete the kept-dead classifier in this task.

## Left for the pin move

- The workflow's stale `TestRealLaunchapiInspectSessionTargetShape` pattern was
  removed by the pin move. (corrected, see Corrections)
- The approved module inputs moved Go to 1.26.11 and AMQ to v0.140.0.
  (corrected, see Corrections)
- The adoption and registry pin constants, launch-verified row, doctor floor,
  and five floor tests moved with the module pin. (corrected, see Corrections)
- The AMQ v0.140.0 scratch reading was completed. The Claude wake-hold probe
  was not run because it remains a lead gate requiring a real provider/session,
  outside this reviewer's local pin verification. (corrected, see Corrections)

## Not done here

- The kept-dead disk classifier, `agentLiveness.Live`, launch-record selectors,
  verdict vocabulary, and `classifyMemberStatusFromEntries` remain.
- No correction is made to `docs/v3.2.0-release-notes.md` or
  `docs/v3-followups.md`; those corrections belong to the lead.
- No `go.mod`, `go.sum`, adoption-floor, registry, or workflow edit is included.
- No module pin is moved.

## Corrections (2026-11-11, with the pin move)

The pin-move review re-read the four removed tests from
`3f702b12205f443e2048ba260d8ac93de5b186a0:internal/cli/liveness_test.go`.
The corrected table cells above now distinguish the exact disk status/detail,
Inspect outcome/evidence, memo call count, structured observation assertions,
and the absence of a JSON assertion in the unremarkable-observation test.

Both required deletion greps were rerun. Each returned exit 1 with no matches
and no output; the earlier exit-0 annotations were incorrect. The caller-chain
blocks are abbreviated excerpts, not complete command output, as the new
caveat states.

The removed-name guard's final v0.140.0 run passed before the file was deleted:
`go test ./internal/adoptionseam/ -run '^TestNoRemovedLaunchapiNames$' -count=1 -v`.
The pin-move note records that run and the compiler-enforced replacement,
including revert row 5's thirteen undefined removed names:
`../330-pin-move-1/README.md`.

