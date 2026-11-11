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

AMQ v0.140.0 still declares launchapi contract `0.61.1`. Its Unix
`platformCompatibilityFeaturesV1` retains every feature used by
`AdoptionFloorFeatures`; AMQ v0.139.0 removed the unused `lifecycle_v1`
surface. The comment at `internal/adoptionseam/floor.go:68-72` now records
that boundary.

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

- `internal/adoptionseam/floor.go:21` and
  `internal/registry/versions.go:6` move the linked pin to v0.140.0.
- `internal/cli/doctor.go:27` moves the one installed-binary floor to
  0.140.0.
- `.github/workflows/ci.yml:99,113` moves the pinned real-AMQ lane, and line
  142 drops only `TestRealLaunchapiInspectSessionTargetShape`. Before that
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

Each revert first changed only its named target and produced a non-empty
`git diff --stat`. After restoration,
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
| `PYTHONPATH=/private/tmp/amq-squad-reviewer-pyyaml python3 scripts/generate-plugin-skills.py --check` | exit 0, no output; PyYAML 6.0.3 was installed only in that temporary directory |
| `python3 scripts/check-release-version.py --print-amq-floor` | exit 0, `0.140.0` |
| `go run ./scripts/redact-evidence -check docs/v3-spike/330-r0-inspect-floor/ docs/v3-spike/330-pin-move-1/` | exit 0, no output |
| `git diff --check` | exit 0, no output |
| `git diff --stat 6a0fe4b1742f5c331d8e3c87d6cf9a6c603a1e41` | exit 0; 26 tracked files, 123 insertions, 263 deletions before this note's final result-row update |
| `git diff --no-index --stat /dev/null docs/v3-spike/330-pin-move-1/README.md` | expected exit 1; one new file, 225 insertions |

No whole-tree `go test ./...`, `make ci`, or documentation render was run.

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
