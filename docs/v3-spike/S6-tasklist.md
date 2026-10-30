# S6 load test: draft task list (25 tasks, 2 repositories)

Drafted by the manifest-analyst seat for plan section 3, task S6. Repositories:
`/Users/omri/code/amq-noc` (13 tasks, ids N1-N13) and `/Users/omri/code/workspace-cli`
(12 tasks, ids W1-W12). Both repos were read-only for this draft.

## How the list was sourced

1. Open GitHub issues first. amq-noc has 22 open issues and workspace-cli has 14. Almost all are
   feature-sized or tracking/umbrella issues (fleet control plane, upstream asks, multi-account,
   plugin system). Only a scoped slice of an issue fits "about one file", so 4 amq-noc and 6
   workspace-cli tasks are labelled "slice" and the description states the slice. Issues that
   cannot be sliced to one file (#62, #63, #73, #76 trackers; #112, #113, #116, #118, #121-#123)
   were skipped.
2. `go vet ./...`: clean in both repos (no findings).
3. Lint: `staticcheck` is not installed; `gofmt -l .` is clean in both repos. No findings.
4. TODO/FIXME: amq-noc has three in `internal/rules/rules.go` (template placeholders, N13);
   workspace-cli has none in non-test code.
5. Because sources 2-4 are nearly empty, the remainder are zero-coverage pure helpers found with
   `go test -coverprofile` and `go tool cover -func` (0.0% entries). Each is "add a table test".
   These are coverage tasks, not defect fixes; say so when presenting the run.

Verify commands run from the repo root. `make ci` in amq-noc is `fmt-check vet test`; workspace-cli
has `make test`, `make vet`, `make lint`. Every task also needs `go build ./...` green.

## amq-noc (13)

| id | source | description | file(s) | verify | size | conflict |
| --- | --- | --- | --- | --- | --- | --- |
| N1 | issue #57 (slice) | Add GitHub Actions PR workflow: gofmt check, `go vet`, `go test` on main pushes and PRs; update RELEASING.md step 7 with the workflow name | `.github/workflows/ci.yml`, `RELEASING.md` | `make ci` (workflow not run locally; `gofmt -l .` empty) | S | none |
| N2 | issue #61 (slice) | Annotate QA-001..QA-011 in the QA doc as shipped/superseded/still-open; add the newest-first convention note to PLAN.md | `QA-02-06-26.md`, `PLAN.md` | `grep -c "Open triage" QA-02-06-26.md` is 0; `go build ./...` | S | none |
| N3 | issue #60 (slice) | Golden snapshot test of the board at 80x24 in ASCII for one seeded needs-you fixture, with a `-update` flag | `internal/console/noc_golden_test.go` (+ `testdata/`) | `go test ./internal/console -run Golden -count=1` | M | none (new file) |
| N4 | issue #72 (slice) | Fleet registry persistence only: load/save `~/.amq-noc/fleet.json` (path injectable), last-seen, forget call | `internal/noc/fleet.go`, `internal/noc/fleet_test.go` | `go test ./internal/noc -run Fleet -count=1` | M | none |
| N5 | coverage | Table tests for `parseBinaryArgFlags`, `parseAgentArgs`, `mergeBinaryArgs`, `formatBinaryArgs` (0.0%) | `internal/cli/agent_args_test.go` | `go test ./internal/cli -run AgentArgs -count=1` | S | none |
| N6 | coverage | Tests for `absoluteAMQRoot`, `chooseProjectBaseRoot`, `scanBaseRootForProject` with temp dirs | `internal/cli/amq_env_test.go` | `go test ./internal/cli -run AMQEnv -count=1` | S | none |
| N7 | coverage | Test `renderIssueBody` and `resolveSeedIssue` with the gh call stubbed | `internal/cli/briefs_seed_test.go` | `go test ./internal/cli -run Seed -count=1` | S | none |
| N8 | coverage | Tests for `nonOperatorParticipants` and `squadRecipients` | `internal/act/act_test.go` (existing) | `go test ./internal/act -count=1` | S | edits an existing test file; no other task touches it |
| N9 | coverage | Tests for `compareSemverParts`, `parseSemverParts`, `doctorProfileSummaryStatus`, `countFails`, `countNonOK` | `internal/cli/doctor_test.go` | `go test ./internal/cli -run Doctor -count=1` | S | none |
| N10 | coverage | Tests for `sortRestorableEntries`, `formatListTime`, `looksActive`, `presencePath` | `internal/cli/list_test.go` | `go test ./internal/cli -run List -count=1` | S | none |
| N11 | coverage | Tests for `cloneStringMap`, `looksLikeGitHubIssueSeed`, `nocActionBinaryFlagValue` | `internal/cli/noc_helpers_test.go` | `go test ./internal/cli -run NocHelpers -count=1` | S | none |
| N12 | coverage | Tests for `effectiveVersion` and `buildInfoVersion` | `cmd/amq-noc/main_test.go` | `go test ./cmd/amq-noc -count=1` | S | none |
| N13 | TODO | Resolve the three TODO placeholders in the default rules template (`rules.go:43,48,53`): concrete guidance or mark intentional, pin in a test | `internal/rules/rules.go`, `internal/rules/rules_test.go` | `go test ./internal/rules -count=1` | S | none |

## workspace-cli (12)

| id | source | description | file(s) | verify | size | conflict |
| --- | --- | --- | --- | --- | --- | --- |
| W1 | issue #199 | `gws chat send --at <time>` scheduled delivery. First confirm the Chat API supports it; if not, record why and stop | `cmd/chat.go`, `cmd/chat_test.go` | `go build ./... && go test ./cmd -run Chat -count=1` | M | none |
| W2 | issue #198 | `drive restore-revision` command (force a prior revision to head) | `cmd/drive.go`, `cmd/drive_test.go` | `go build ./... && go test ./cmd -run Drive -count=1` | M | none |
| W3 | issue #204 (slice) | `sheets` data validation: ONE_OF_LIST set command only | `cmd/sheets.go`, `cmd/sheets_test.go` | `go build ./... && go test ./cmd -run Sheets -count=1` | M | conflicts with W7 (same files) |
| W4 | issue #203 | `slides` native paragraph bullets (CreateParagraphBulletsRequest) | `cmd/slides.go`, `cmd/slides_test.go` | `go build ./... && go test ./cmd -run Slides -count=1` | M | none |
| W5 | issue #215 (slice) | Client-side `--rate-limit` flag only, applied in the HTTP client; skip the 429 messaging | `cmd/root.go` or `internal/client/factory.go` | `go build ./... && go test ./cmd ./internal/client -count=1` | M | conflicts with W12 if factory.go is chosen |
| W6 | issue #104 (slice) | `gmail` Settings API: vacation get/set only | `cmd/gmail.go`, `cmd/gmail_test.go` | `go build ./... && go test ./cmd -run Gmail -count=1` | M | none |
| W7 | coverage | Table tests for `parseRange` and `parseCellRange` | `cmd/sheets_test.go` (existing) | `go test ./cmd -run 'ParseRange\|ParseCellRange' -count=1` | S | conflicts with W3 |
| W8 | coverage | Tests for `hookWorkspaceKey`, `hookConfigPath`, `hookSnapshotPath`, `renderHook` | `cmd/hooks_test.go` (existing) | `go test ./cmd -run Hook -count=1` | S | none |
| W9 | coverage | Tests for `generateCodeVerifier`, `generateCodeChallenge` (RFC 7636 vector), `generateState` | `internal/auth/oauth_test.go` | `go test ./internal/auth -count=1` | S | none |
| W10 | coverage | Tests for `DetectDuplicateTimeHooks`, `UserConfigPath`, `WorkspaceConfigPath` | `internal/hooks/config_test.go` | `go test ./internal/hooks -count=1` | S | none |
| W11 | coverage | Load/Save round-trip test for the hooks runtime snapshot | `internal/hooks/runtime_test.go` | `go test ./internal/hooks -count=1` | S | none |
| W12 | coverage | Test `serviceAccountScopes` and `AuthMode` | `internal/client/factory_test.go` | `go test ./internal/client -count=1` | S | conflicts with W5 if W5 edits factory.go |

Conflict summary: W3/W7 (sheets), W5/W12 (only if W5 lands in factory.go). Everything else is a new
file or a file no other task touches. If N3 wires goldens into `make ci` it would touch the
Makefile; keep that out of N3 so it cannot clash with N1.

## Caveats for the owner

- 10 of 25 tasks come from open issues, and every one is a slice or a reduced scope. The cuts are
  my reading of the issue bodies; the issue authors may not agree.
- 15 of 25 are coverage tasks because vet, gofmt and TODO sources are empty. They exercise the
  build-and-test loop at low risk; consider swapping some for feature issues if the load test
  should stress review and merge conflicts harder.
- Size M tasks (W1-W6, N3, N4) may touch two files (code plus test).

## Baseline suite timings (`go test ./... -count=1`, go1.27.1 darwin/arm64)

| repo | wall time | result |
| --- | --- | --- |
| amq-noc | 5 s | pass (10 packages ok) |
| workspace-cli | 13 s | pass (11 packages ok) |

Wall time includes compilation on a warm build cache; a cold cache will be slower.

Awaiting owner approval
