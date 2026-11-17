# 3.3.0 pin move 2: the words

TIP `8b86ded84b4b0adbac623309c331adf8037e48ac`; fourteen lead inputs landed byte for byte; this note records the commands.

## Input checks

`shasum -a 256 "$IN/MANIFEST.sha256"` — exit 0:

```text
813f8828b574d8e6aceb8c4e063a6739804bb910978af8a7ab55dbc444e12250  MANIFEST.sha256
```

`(cd "$IN/tree" && shasum -a 256 -c ../MANIFEST.sha256)` — exit 0:

```text
README.html: OK
README.md: OK
RELEASING.md: OK
docs/v3-buzz/README.md: OK
docs/v3-buzz/enroll.sh: OK
docs/v3-buzz/owner-run.md: OK
docs/v3-spike/330-pin-move-1/README.md: OK
docs/v3-spike/330-r0-inspect-floor/README.md: OK
docs/v3-upstream-asks.md: OK
internal/adoptionseam/floor.go: OK
internal/buzz/rehearsal_test.go: OK
internal/cli/scheduler_run.go: OK
internal/seatexec/codexhook.go: OK
internal/status/screen.go: OK
```

The precheck exited 0 with empty output:

```sh
git diff --stat 8b86ded84b4b0adbac623309c331adf8037e48ac HEAD -- README.md README.html RELEASING.md docs/v3-buzz/README.md docs/v3-buzz/enroll.sh docs/v3-buzz/owner-run.md docs/v3-upstream-asks.md docs/v3-spike/330-pin-move-1/README.md docs/v3-spike/330-r0-inspect-floor/README.md internal/adoptionseam/floor.go internal/buzz/rehearsal_test.go internal/cli/scheduler_run.go internal/seatexec/codexhook.go internal/status/screen.go
```

`shasum -a 256 -c "$IN/MANIFEST.sha256"` from the worktree root — exit 0:

```text
README.html: OK
README.md: OK
RELEASING.md: OK
docs/v3-buzz/README.md: OK
docs/v3-buzz/enroll.sh: OK
docs/v3-buzz/owner-run.md: OK
docs/v3-spike/330-pin-move-1/README.md: OK
docs/v3-spike/330-r0-inspect-floor/README.md: OK
docs/v3-upstream-asks.md: OK
internal/adoptionseam/floor.go: OK
internal/buzz/rehearsal_test.go: OK
internal/cli/scheduler_run.go: OK
internal/seatexec/codexhook.go: OK
internal/status/screen.go: OK
```

`git diff --summary 8b86ded84b4b0adbac623309c331adf8037e48ac` — exit 0, no mode change line.

## Commands

- `go build ./...` — exit 0, no output.
- `go vet ./internal/adoptionseam/ ./internal/buzz/ ./internal/cli/ ./internal/seatexec/ ./internal/status/` — exit 0, no output.
- `gofmt -l internal/adoptionseam/floor.go internal/buzz/rehearsal_test.go internal/cli/scheduler_run.go internal/seatexec/codexhook.go internal/status/screen.go` — exit 0, no output.
- `go test ./internal/adoptionseam/ -count=1` — exit 0:

  ```text
  ok  	github.com/omriariav/amq-squad/v3/internal/adoptionseam	0.550s
  ```

- `go test ./internal/seatexec/ -run '^TestCodex' -count=1` — exit 0:

  ```text
  ok  	github.com/omriariav/amq-squad/v3/internal/seatexec	0.446s
  ```

- `go test ./internal/cli/ -run '^(TestSkillIdentity|TestGeneralOperationFloorUnchanged|TestReleaseCheck)' -count=1` — exit 0:

  ```text
  ok  	github.com/omriariav/amq-squad/v3/internal/cli	0.522s
  ```

- `bash -n docs/v3-buzz/enroll.sh` — exit 0, no output.
- `python3 scripts/check-release-version.py --print-amq-floor` — exit 0:

  ```text
  0.140.0
  ```

- `go run ./scripts/redact-evidence -check docs/v3-spike/330-pin-move-1/` — exit 0, no output.
- `go run ./scripts/redact-evidence -check docs/v3-spike/330-r0-inspect-floor/` — exit 0, no output.
- `go run ./scripts/redact-evidence -check docs/v3-spike/330-pin-move-2-words/` — exit 0, no output.
- `git diff --check` — exit 0, no output.
- `git diff --stat 8b86ded84b4b0adbac623309c331adf8037e48ac` — exit 0:

  ```text
   README.html                                  | 13 +++--
   README.md                                    | 11 +++--
   RELEASING.md                                 | 12 +++--
   docs/v3-buzz/README.md                       |  9 ++--
   docs/v3-buzz/enroll.sh                       |  5 +-
   docs/v3-buzz/owner-run.md                    | 36 ++++++++------
   docs/v3-spike/330-pin-move-1/README.md       | 71 +++++++++++++++++++++++-----
   docs/v3-spike/330-r0-inspect-floor/README.md | 13 ++++-
   docs/v3-upstream-asks.md                     |  2 +-
   internal/adoptionseam/floor.go               | 20 ++++----
   internal/buzz/rehearsal_test.go              |  8 ++--
   internal/cli/scheduler_run.go                |  4 ++
   internal/seatexec/codexhook.go               | 10 ++--
   internal/status/screen.go                    |  2 +-
   14 files changed, 150 insertions(+), 66 deletions(-)
  ```

The comments-only check exited 0:

```sh
git diff -U0 8b86ded84b4b0adbac623309c331adf8037e48ac -- '*.go' | grep '^[-+]' | grep -v '^[-+][-+]' | grep -v '^[-+][[:space:]]*//'
```

```text
-	wakeDialogMarkerName = ".wake.dialog" // AMQ's dialog marker (internal/cli/wake_dialog.go at v0.116.2)
+	wakeDialogMarkerName = ".wake.dialog" // AMQ's dialog marker (internal/dialogguard/dialogguard.go:11 at v0.140.0)
```

## Not run by the seat

- Whole-repo `make ci`: the lead's, on the PR head.
- `go test ./...`: the lead's, on the PR head.
- HTML checks: the lead's, on the PR head.
- `release-check`: the lead's, on the PR head.
- Any launch: the lead's, on the PR head.
