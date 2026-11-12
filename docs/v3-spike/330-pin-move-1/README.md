# 3.3.0 pin move 1: AMQ v0.140.0

Line numbers are as of `6a0fe4b1742f5c331d8e3c87d6cf9a6c603a1e41`
(`origin/v3`), the dispatched base.

## 1. Approved inputs and base

The worktree began at exactly the dispatched base. Sixteen lead-owned inputs
were copied byte-for-byte from
`/Users/omri/.claude/jobs/6dd424c9/tmp/pin-inputs/tree`; they were not
regenerated or hand-edited.

```text
$ shasum -a 256 /Users/omri/.claude/jobs/6dd424c9/tmp/pin-inputs/MANIFEST.sha256
0f3bba9387e9e0dbb5999c9c97894bfaa219d12e65943e27d12cf68a34e175a7  /Users/omri/.claude/jobs/6dd424c9/tmp/pin-inputs/MANIFEST.sha256

$ shasum -a 256 -c /Users/omri/.claude/jobs/6dd424c9/tmp/pin-inputs/MANIFEST.sha256
(exit 0; 16/16 entries OK)
```

The manifest check passed both immediately after the copy and after the source
and documentation work. The pre-copy check
`git diff --stat bb4334e2... 6a0fe4... -- <the sixteen paths>` printed
nothing.

## 2. Module before and after

`git diff 6a0fe4b1742f5c331d8e3c87d6cf9a6c603a1e41 -- go.mod` records:

| Requirement | Before | After |
| --- | --- | --- |
| Go directive | 1.26.0 | 1.26.11 |
| agent-message-queue | v0.116.2 | v0.140.0 |
| btcec/v2 | v2.5.0 | v2.5.1 |
| secp256k1/v4 indirect | v4.4.0 | v4.4.1 |
| go-toml/v2 indirect | absent | v2.4.3 |

`go mod tidy -diff` exited 0 with no output, so the approved `go.mod` and
`go.sum` needed no local rewrite.

## 3. Upstream scratch reading and F3 row

AMQ v0.140.0 still declares launchapi contract `0.61.1`. The six names in
`AdoptionFloorFeatures` are all negotiable at v0.140.0
(`TestNegotiateAdoptionFloorSatisfiedByPinnedAMQ` passes at the pin; see
Corrections); AMQ v0.139.0 removed the unused `lifecycle_v1`
surface. `internal/adoptionseam/floor.go:68-72` is the old comment on the
base; on this PR's head the sentence that records that boundary is at
`:72-74`. (corrected, see Corrections)

The provider-static capability function is byte-identical at both module
pins:

```text
$ for v in v0.116.2 v0.140.0; do
    awk '/^func ProviderStaticInputCapabilities/,/^}/' \
      "/Users/omri/go/pkg/mod/github.com/avivsinai/agent-message-queue@$v/internal/launch/adapter.go" |
      shasum -a 256
  done
295fb9fcc22eba011fe6b0fb6d97a34d5d25c18746782fa960c29365024d8a63  -
295fb9fcc22eba011fe6b0fb6d97a34d5d25c18746782fa960c29365024d8a63  -
```

Therefore the new `launchVerified` row is:

| AMQ module | claude | codex | grok | cursor-agent | amq |
| --- | --- | --- | --- | --- | --- |
| v0.140.0 | 2.1.233 | 0.163.0 | 1.0.5 | 2026.08.11-e8db854 | 0.140.0 |

Older rows remain.

## 4. Pin, one floor, and five tests

- `internal/adoptionseam/floor.go:21` (the constant's line on the base;
  `:22` on this PR's head) and
  `internal/registry/versions.go:6` move the linked pin to v0.140.0.
- `internal/cli/doctor.go:27` moves the one installed-binary floor to
  0.140.0.
- `.github/workflows/ci.yml:99,113` moves the pinned real-AMQ lane, and line
  142 drops one name from the `CLI_TESTS` pattern,
  `TestRealLaunchapiInspectSessionTargetShape` (the file's diff is three
  changed lines; see Corrections). Before that
  edit, `grep -rn TestRealLaunchapiInspectSessionTargetShape
  --include='*_test.go' .` exited 1 with no output.
- The moved expectations are
  `TestDoctorInstalledAMQFloor`,
  `TestDoctorAMQVersionResolutionDisablesChildUpdateCheck`,
  `TestExecuteDoctorAMQVersionAcceptsTheFloorAndNewerCanary`,
  `TestExecuteDoctorAMQVersionRejectsOlderThanTheFloor`, and
  `TestGeneralOperationFloorUnchanged`. Historical older-release literals
  remain; the floor, immediately-below, prerelease, and newer-canary cases
  move to the v0.140.0 boundary.

## 5. Removed-name guard

The last run of the AST guard at the new pin passed:

```text
$ go test ./internal/adoptionseam/ -run '^TestNoRemovedLaunchapiNames$' -count=1 -v
=== RUN   TestNoRemovedLaunchapiNames
--- PASS: TestNoRemovedLaunchapiNames (0.15s)
PASS
ok  github.com/omriariav/amq-squad/v3/internal/adoptionseam  0.537s
```

`internal/adoptionseam/launchapi_removed_names_test.go` was then deleted:
the v0.140.0 compiler now enforces the same thirteen-name boundary. After
deletion, both documented removed-floor greps exited 1 with no output and
`go vet ./internal/adoptionseam/` exited 0 with no output.

## 6. Five negative reverts

Each revert was an edit to its named target and produced a non-empty
`git diff --stat`; the stat's file list was not recorded (the lead's re-run
with the file lists is in the Corrections section). After restoration,
`git diff --binary | shasum -a 256` returned the same baseline each time:

```text
$ git diff --binary | shasum -a 256
3649d1b9fc9dab514395c5a7f0b540edae9136bf0423404c79461338b5b547e6  -
```

1. Registry pin constant back to v0.116.2:

   ```text
   $ go test ./internal/registry/ -run '^TestPinnedAMQModuleMatchesGoMod$' -count=1
   (exit 1)
   detect_test.go:250: go.mod pins [github.com/avivsinai/agent-message-queue v0.140.0 v0.140.0], registry.PinnedAMQModule is v0.116.2
   ```

2. Adoption pin constant back to v0.116.2:

   ```text
   $ go test ./internal/adoptionseam/ -run '^TestLinkedAMQModuleIsThePin$' -count=1
   (exit 1)
   pin_test.go:15: go.mod pins github.com/avivsinai/agent-message-queue v0.140.0, PinnedAMQModule is v0.116.2
   ```

3. Remove only the v0.140.0 launch-verified row:

   ```text
   $ go test ./internal/registry/ -run '^TestPinnedAMQModuleMatchesGoMod$' -count=1
   (exit 1)
   detect_test.go:253: no launch-verified versions for v0.140.0
   ```

4. Doctor production floor back to 0.116.2:

   ```text
   $ go test ./internal/cli/ -run '^(TestDoctorInstalledAMQFloor|TestDoctorAMQVersionResolutionDisablesChildUpdateCheck|TestExecuteDoctorAMQVersionAcceptsTheFloorAndNewerCanary|TestExecuteDoctorAMQVersionRejectsOlderThanTheFloor|TestGeneralOperationFloorUnchanged)$' -count=1
   (exit 1)
   --- FAIL: TestDoctorInstalledAMQFloor
   --- FAIL: TestExecuteDoctorAMQVersionRejectsOlderThanTheFloor
       --- FAIL: .../0.42.1
       --- FAIL: .../0.60.0
       --- FAIL: .../0.74.1
       --- FAIL: .../0.107.0
       --- FAIL: .../0.116.1
       --- FAIL: .../0.139.9
       --- FAIL: .../0.140.0-rc1
   --- FAIL: TestGeneralOperationFloorUnchanged
   ```

   The first failure reported the 0.139.9 stub as OK against the reverted
   0.116.2 floor. The general-floor failure reported
   `doctorMinAMQVersion = "0.116.2", want "0.140.0"`.
   `TestDoctorAMQVersionResolutionDisablesChildUpdateCheck` and
   `TestExecuteDoctorAMQVersionAcceptsTheFloorAndNewerCanary` passed.

5. Compile the approved removed-name probe:

   ```text
   $ shasum -a 256 /Users/omri/.claude/jobs/6dd424c9/tmp/pin-inputs/probe/zz_removed_name_probe.go.txt
   b0a1731ebf34737133aacddd0fd8fe38522b78fbf5bfa4dc9f668fc25269eab3  /Users/omri/.claude/jobs/6dd424c9/tmp/pin-inputs/probe/zz_removed_name_probe.go.txt

   $ go build -gcflags=-e ./internal/adoptionseam/
   (exit 1)
   internal/adoptionseam/zz_removed_name_probe.go:6:16: undefined: launchapi.Inspect
   internal/adoptionseam/zz_removed_name_probe.go:7:16: undefined: launchapi.Focus
   internal/adoptionseam/zz_removed_name_probe.go:8:16: undefined: launchapi.Close
   internal/adoptionseam/zz_removed_name_probe.go:9:14: undefined: launchapi.InspectRequestV1
   internal/adoptionseam/zz_removed_name_probe.go:10:14: undefined: launchapi.InspectResultV1
   internal/adoptionseam/zz_removed_name_probe.go:11:14: undefined: launchapi.FocusRequestV1
   internal/adoptionseam/zz_removed_name_probe.go:12:14: undefined: launchapi.FocusResultV1
   internal/adoptionseam/zz_removed_name_probe.go:13:14: undefined: launchapi.CloseRequestV1
   internal/adoptionseam/zz_removed_name_probe.go:14:14: undefined: launchapi.CloseResultV1
   internal/adoptionseam/zz_removed_name_probe.go:15:14: undefined: launchapi.LifecycleResultV1
   internal/adoptionseam/zz_removed_name_probe.go:16:16: undefined: launchapi.DecodeInspectRequestV1
   internal/adoptionseam/zz_removed_name_probe.go:17:16: undefined: launchapi.DecodeFocusRequestV1
   internal/adoptionseam/zz_removed_name_probe.go:18:16: undefined: launchapi.DecodeCloseRequestV1
   ```

   The probe was removed afterward and its path was absent from
   `git status --short`.

## 7. Section 9 command results

| Command | Result |
| --- | --- |
| `go build ./...` | exit 0, no output |
| `go vet ./...` | exit 0, no output |
| `go test ./internal/adoptionseam/ ./internal/registry/ -count=1` | exit 0 |
| `go test ./internal/cli/ -run '^(TestDoctor|TestExecuteDoctor|TestGeneralOperationFloor|TestReleaseCheck)' -count=1` | exit 0 |
| `go test ./internal/launchintent/ ./internal/seatlaunch/ -count=1` | exit 0 |
| `gofmt -l internal/ cmd/` | exit 0, no output |
| `PYTHONPATH=/private/tmp/amq-squad-reviewer-pyyaml python3 scripts/generate-plugin-skills.py --check` | exit 0, no output; PyYAML 6.0.3 was read from that temporary directory through `PYTHONPATH` (the install command was not recorded) |
| `python3 scripts/check-release-version.py --print-amq-floor` | exit 0, `0.140.0` |
| `go run ./scripts/redact-evidence -check docs/v3-spike/330-r0-inspect-floor/ docs/v3-spike/330-pin-move-1/` | exit 0, no output |
| `git diff --check` | exit 0, no output |
| `git diff --stat 6a0fe4b1742f5c331d8e3c87d6cf9a6c603a1e41` | exit 0; 26 tracked files, 123 insertions, 263 deletions before this note's final result-row update |
| `git diff --no-index --stat /dev/null docs/v3-spike/330-pin-move-1/README.md` | expected exit 1; one new file, 225 insertions |

The table above lists 12 command rows: zero whole-tree `go test ./...`
commands, zero `make ci` commands and zero documentation renders (the lead's
locked runs on the head are in the merge record of #972).

## 8. Not measured here

These remain lead gates or later environment-backed checks:

- the real-AMQ CI lane;
- real provider launches;
- the internal/Buzz launch trio with v0.140.0;
- HTML renders;
- whole `make ci`;
- the Claude wake-hold probe, which requires a real provider/session.

## 9. Not carried with the pin

The move does not change `CodexDaemonVersion`, `ClaudeNativeVersion`,
feature floors or their dated citations, or the go-installed `amq-remote`
`"dev"` handling. Those are separate contracts, not consequences of the
AMQ module pin.

## Corrections (2026-11-11, pin move 2: the words)

These are lead inputs, landed byte for byte by the seat task of that name.
Each sentence corrected above carries "(see Corrections)" or is named here.

- Section 3 said AMQ's Unix `platformCompatibilityFeaturesV1` "retains every
  feature used by `AdoptionFloorFeatures`" with no command under it. The
  count is six of six: at v0.140.0 `launchapi/compatibility_features_unix.go`
  lists `launch_intent_v1` (`:9`), `prepare_apply_v1` (`:10`),
  `managed_tmux_v1` (`:11`), `FeatureInitialInput` (`:13`),
  `FeatureBaseRoot` (`:15`) and `FeatureCallerContext` (`:17`). The lead's
  run on
  `8b86ded84b4b0adbac623309c331adf8037e48ac`:
  `go test ./internal/adoptionseam/ -run 'TestNegotiateAdoptionFloorSatisfiedByPinnedAMQ' -count=1 -v`,
  exit 0, one PASS.
- Section 3 cited `internal/adoptionseam/floor.go:68-72` for the comment that
  "now records that boundary". Those lines are the old comment on the base
  (`6a0fe4b1742f5c331d8e3c87d6cf9a6c603a1e41`); on #972's head
  (`3876ee265401f08adf7f1e6d08d97b5286791c03`) the new sentence is at
  `:72-74`. Pin move 2 rewrites that comment again, so neither range is its
  place after this PR.
- Section 4 cited `internal/adoptionseam/floor.go:21` for the pin constant.
  That is its line on the base, as this note's heading says; on #972's head
  the constant is at `:22`.
- Section 4 said line 142 of `.github/workflows/ci.yml` "drops only"
  `TestRealLaunchapiInspectSessionTargetShape`. The lead's
  `git diff 6a0fe4b1742f5c331d8e3c87d6cf9a6c603a1e41 3876ee265401f08adf7f1e6d08d97b5286791c03 -- .github/workflows/ci.yml`
  shows three changed lines: the comment's `v0.116.2` to `v0.140.0`, the
  pinned lane's `amq-version` the same way, and the `CLI_TESTS` pattern
  without that one name.
- Section 6 said each revert "first changed only its named target". Section 6
  lists five reverts and names `git diff --stat` once, in its first sentence,
  as non-empty for each; it prints zero stat outputs and zero file lists. The
  lead re-ran the five on `3876ee265401f08adf7f1e6d08d97b5286791c03` with
  section 6's commands
  (2026-11-11): `git diff --stat` named one file for each of rows 1 to 4
  (`internal/registry/versions.go`, `internal/adoptionseam/floor.go`,
  `internal/registry/versions.go`, `internal/cli/doctor.go`), and
  `git status --porcelain` named one untracked file for row 5, the probe
  (`internal/adoptionseam/zz_removed_name_probe.go`). Five of five exit 1:
  rows 1 to 3 print the row's line, row 4 fails the row's ten test names, and
  row 5 prints the row's thirteen `undefined:` lines.
- Section 7 said PyYAML 6.0.3 "was installed only in that temporary
  directory". The note records the `PYTHONPATH` the command ran with and no
  install command.
- Section 7 said no whole-tree `go test ./...`, `make ci` or documentation
  render was run by the seat. Section 7 lists 12 command rows: zero whole-tree
  `go test ./...` commands, zero `make ci` commands and zero documentation
  renders; the lead's locked `make ci` and `make docs-html-check` on #972's
  head both exited 0.

## Corrections (2026-11-12, the docs record)

Lead edits, by hand (the plan reviewer's ruling on #977, item 4).

- Three sentences stood without a count beside them: the sentence under
  section 7's table, and the Section 6 entry and the last Section 7 entry of
  the Corrections above. Each now carries its count. The counts are the lead's,
  from these commands on this file as this PR leaves it (`F` is this file):

  ```text
  $ awk '/^## 7\. /{f=1;next} /^## 8\. /{f=0} f' F | grep -c '^| `'
  12
  $ awk '/^## 7\. /{f=1;next} /^## 8\. /{f=0} f' F | grep -c -F '| `go test ./...`'
  0
  $ awk '/^## 7\. /{f=1;next} /^## 8\. /{f=0} f' F | grep '^| `' | grep -c -F 'make ci'
  0
  $ awk '/^## 7\. /{f=1;next} /^## 8\. /{f=0} f' F | grep '^| `' | grep -c -E 'pandoc|docs-html|make docs|\.html'
  0
  $ awk '/^## 6\. /{f=1;next} /^## 7\. /{f=0} f' F | grep -c -E '^[0-9]+\. '
  5
  $ awk '/^## 6\. /{f=1;next} /^## 7\. /{f=0} f' F | grep -c -F 'git diff --stat'
  1
  $ awk '/^## 6\. /{f=1;next} /^## 7\. /{f=0} f' F | grep -c -E 'files? changed| \| +[0-9]+ [+-]+'
  0
  ```

  A `grep -c` that prints 0 exits 1. The fourth pattern is what the lead took
  a documentation render's command to hold; the twelve rows were also read.
