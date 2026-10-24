# amq-squad 3.0, brief 2b-3: registry, detection and doctor

Branch `v3-2b-3`, cut from `v3` at 2aa2baa8. Brief: docs/v3-phase2-briefs.md "2b-3", with its rulings and review 1. Plan revision 2.6, sections 5, 8, 16.2, 16.8; R9 to R12, R16 (doctor parts), amendments 1, 17, 18, 22.

Contracts from branches that are not merged yet, coded against their committed shapes and rebased once they merge: the usage-limit interface of 2b-1 (`git show v3-2b-1:internal/scheduler/usage_limit.go`, commit 46f1177b), the grants schema and the insecure-file mode of 3-1 (`git show v3-3-1:internal/grants/`).

Standard gate on every commit: `go build ./... && go vet ./... && go test ./... && scripts/manifest-check.sh --final`, plus `GOOS=linux go build ./...` and `GOOS=windows go build ./...`.

## Lead rulings during the brief

1. (2026-10-24, at start) The implementer never reads `~/.claude`, `~/.codex` or `~/.amq-squad`. The usage-limit detector is built against the S6 scanner's Claude classification and the Codex rollout format (`sessions/YYYY/MM/DD/rollout-*.jsonl`, the name AMQ v0.107.0's capture uses), tested on fixtures shaped like the S6 files, plus a read-only owner-run command that reports which files carried a signal; the owner runs it and the measured list goes into the plan. The "usage limit resets available" banner line is excluded and pinned with a fixture row.
2. (same) The preflight copy that 2b-2 writes and doctor diffs lives at `~/.amq-squad/preflight/<name>`: `claude-settings.json` and `codex-config.toml`. The lead tells 2b-2.

## Commit 1: registry entry type and file format

Built:
- `internal/registry` (new): the entry type of plan 5 and R10, one entry per runtime x model x effort with runtime, provider, model, effort, verified, state and reason, cost tier, privacy class, quota pool, concurrency pool, cap and the cap's source (measured, extrapolated, unmeasured), untrusted_input, probation, latency (direct text TTFT, plus reasoning TTFT for thinking models), the catalog routing metadata (capability_tier, cost_index, latency_index, strengths, work_classes; 2a-2 ruling 13), and the R12 probe record (runs, passes, false successes, tool errors, predicate, version probed against). Tool and host rows sit next to the entries. `Proposable` returns verified entries only (R11).
- registry.yaml format: a strict YAML subset written only by `Format`; `Parse` rejects unknown keys and any schema but 1. `Write` is atomic (temp file and rename), mode 0600, and two writes of one registry give the same bytes. No YAML module dependency.

Rows: two new rows (`internal/registry/registry.go`, `internal/registry/format.go`), fate new.

Tests: `internal/registry/registry_test.go`: round trip and stable format, unknown key and schema rejection, atomic idempotent private write, proposable is verified only, sort order.

Behavior changes: none (no caller yet). 2b-4 can code against `registry.Entry` from this commit.
