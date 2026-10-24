# amq-squad 3.0, brief 2b-3: registry, detection and doctor

Branch `v3-2b-3`, cut from `v3` at 2aa2baa8 and rebased onto `v3` at 2992a3cd after the 3-1 merge (beabeadc); the commit hashes below are the rebased ones (b91849a1, b1fa4850, 129dc059, b383af2b, ed63a1c2, then commit 6). Brief: docs/v3-phase2-briefs.md "2b-3", with its rulings and review 1. Plan revision 2.6, sections 5, 8, 16.2, 16.8; R9 to R12, R16 (doctor parts), amendments 1, 17, 18, 22.

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

## Commit 2: detection, entries, Codex model id check, runtime re-hash

Built:
- `registry.Detect` (plan 5, R9): PATH probe for claude, codex, the built-in pi runtime, grok (Grok Build), cursor-agent, ollama, amq, amq-remote, tmux, gh. Version commands run in parallel under a 2 s deadline each. A missing tool is a row with `found: false`, never an error.
- Versions against the AMQ launch API: `registry.LaunchVerifiedVersion` is the table of provider versions the pinned AMQ library verified (claude 2.1.233, codex 0.147.0, grok 1.0.5 on v0.75.0; codex 0.163.0 and cursor-agent 2026.08.11-e8db854 on v0.107.0). The public launchapi shows these only inside a Prepare preview, so the table is copied from `internal/launch/adapter.go` of each module version, and `TestPinnedAMQModuleMatchesGoMod` fails when go.mod moves to a version the table does not know (2b-2's bump to v0.107.0 needs no edit; the table has that row). Each tool row says match, newer, older, unverified or unknown.
- Login state without a login: file presence only, never contents (`~/.claude/.credentials.json`, `~/.codex/auth.json`, `~/.config/gh/hosts.yml`, honoring `CLAUDE_CONFIG_DIR`, `CODEX_HOME`, `GH_CONFIG_DIR`); on macOS the existence of the `Claude Code-credentials` keychain item (`security find-generic-password -s ...` without `-g` or `-w`, so no secret is printed); API key names in the environment (names only). The login source sets the quota pool: subscription or API (Opus has its own API pool, `anthropic-api-opus`).
- Ollama: `/api/tags` and `/api/show` capabilities (tools, thinking), 1 s client timeout. A model whose capabilities lack `tools` is `no_tool_calls` without a probe (the gemma3:4b case) and takes extract and summarize only.
- RAM and GPU: `sysctl hw.memsize` and `machdep.cpu.brand_string` on macOS (Apple silicon: unified memory), `/proc/meminfo` and `nvidia-smi` on Linux. No `system_profiler` (seconds per call).
- `registry.Build`: entries from detection, the agent catalog and the previous registry. Claude: catalog models x efforts. Codex: same, plus the model id check. pi: one entry per Ollama model x low, medium, high, all in the `ollama-gpu` pool with cap 1. Local entries follow R12 (`ShipN` = 5): verified only at N of N with N at least 5 and zero false successes; 3 of 3 stays unprobed with "5 needed before the entry ships"; any false success or a missed pass is `probe_failed` and on probation. Probe records and latencies carry over from the previous file, so a plain detection run rewrites the same file.
- Concurrency caps: Claude subscription 6, measured for sonnet/medium (S6: six Sonnet-medium seats, zero usage-limit events) and labelled extrapolated for the other Claude entries in the same pool; Codex and API pools 4, labelled unmeasured (the S6 Codex column did not run); Ollama 1, measured (S5, `llama-server -np 1`). The seat cap stays a grant field (3-1).
- Codex model id check (amendment 17): `CheckCodexModel` against the model list of the codex-cli version that runs (0.147.0 lists gpt-5.6-sol, -terra, -luna: S4 report), else against a recorded probe for that version, else the entry is `unprobed` with "not checked against codex-cli X". `ProbeCodexModel` (only under `doctor --probe`, it spends one turn) runs `codex exec --skip-git-repo-check --sandbox read-only --model <id>` and `ClassifyCodexOutput` reads the refusal ("requires a newer version of Codex", "not supported when using Codex with a ChatGPT account"). `CheckLaunch` refuses an unverified entry, and a Codex entry checked against another codex-cli version than the one that will run.
- `registry.RehashRuntime`: the full re-hash of the embedded pi runtime against its on-disk manifest (every file, size, mode, SHA-256, nothing extra, no symlink), optionally against the SHA-256 of the embedded manifest. Ruled to run at every launch; 2b-2 / 4-1 call it before exec, doctor calls it too.

Measurements:
- Full re-hash on a synthetic tree the size of the S3 runtime (6,432 files, 178 MB, one 112 MB file), page cache warm, this Mac: 81, 121, 117 ms (`AMQ_SQUAD_REHASH_TIMING=1 go test -run Timing ./internal/registry/`). S3 measured 190 ms on the real runtime. Cold cache is not measured.

Rows: five new rows (detect.go, build.go, codexmodel.go, versions.go, runtime.go), fate new.

Tests: `detect_test.go` (empty host: every tool one missing row, no proposable entry; versions, login and host from canned output, and no secret-reading command runs; R12 states for the three S5 models over a fake Ollama server; acceptance 2: two runs give the same file except latency, a third run without measurements gives the same bytes; go.mod pin; re-hash catches a same-size edit and an extra file), `codexmodel_test.go` (acceptance 4: the S4 closure panes for gpt-6-astra and gpt-5.5 on 0.147.0 classify as refused, the reset banner does not; the refused id is `refused` in the registry, `CheckLaunch` refuses it, and the refusal survives a later run). Fixtures: `internal/registry/testdata/s4c-*.txt`, `s4d-*.txt`, copied from `spike/seatexec/evidence/closure/`.

Deviations:
- pi + Anthropic entries (the smol and task chains name pi + anthropic haiku 5.5 and sonnet 5.5) are not written: their pi model ids come from the pi package (4-1). Grok Build and Cursor get a tool row and no entry until a probe exists.
- The model check runs against the codex-cli that runs (the installed one), not the AMQ capture pin, because the installed binary is what answers; doctor reports when the two differ.

## Commit 3: doctor as the 16.2 probe, `amq-squad trust`

Built:
- `internal/cli/doctor.go` rewritten: the framework and the AMQ rows (amq version and floor, launch API contract, adoption floor, identity pin, `amq doctor --ops`) and the version skew row stay; the host probe moves to `internal/cli/doctor_host.go`. The v2 team config row and the seven `worktree/*` rows go (16.2: "The v2 team, pane, and wake-mirror checks go"), with the worktree diagnostics plumbing that only doctor used (doctorWorkstream, defaultDoctorWorktreeDiagnostics, doctorRuntimeRows, stateAwareSharedIndexDiagnostics). `amq vs pinned launchapi module` folds into the `tool/amq` row, which compares the PATH amq with the pinned library through the same launch-verified table as the other tools.
- `doctor_host.go` (new): one `tool/<name>` row per tool (a missing optional tool is one ok row; only a missing `amq` fails); `host` (RAM, GPU); `ollama models`; `registry` (entries, verified count, file absent, current or out of date; written only under `--write-registry`); `registry/<runtime>` naming the unverified models and why; `pi runtime` (the full re-hash); on macOS `sleep` (pmset) and `amq-remote login service` (a launchd plist under ~/Library/LaunchAgents); with codex on PATH `codex trust` (project roots codex-cli does not trust, exact `[projects."<root>"]` match) and `codex notify fan-out` (presence only, ruling 2); `shell profile`; `owner config/claude-settings.json` and `owner config/codex-config.toml` against `~/.amq-squad/preflight/` (lead ruling 2); `grants` (file mode; an untrusted file fails with kind `owner_grants_insecure`, ruling 1; the seat cap, default 12 per session, amendment 22). Flags: `--write-registry`, `--probe` (implies it), `--runs N` (default 5).
- `internal/hostcheck` (new): the shell-profile reader (zsh, bash, profile and fish files; `export`, `declare -x`, `typeset -x`, `set -x`; names only), the owner config line diff (LCS, secret-looking keys and token formats redacted, codex-cli's `last_updated` bookkeeping lines dropped and counted), the codex-cli trust and notify reader (two keys, not a TOML parser), `TrustSteps`, `SleepPermitted`, `LoginServices`, `GrantsMode`.
- `amq-squad trust <repo>` (new verb, `internal/cli/trust.go`): prints the owner-run steps (accept the prompt once, or add `[projects."<repo>"] trust_level = "trusted"` yourself) and the candidate notify line `notify = ["~/.amq-squad/bin/amq-squad-codex-notify"]`, says when the repo is already trusted, and writes nothing.
- The cli test binary sets `HOME` to a temp directory and unsets `CLAUDE_CONFIG_DIR` and `GH_CONFIG_DIR` (testmain_test.go), so no doctor run in the package reads the owner's files. Probe commands run with `AMQ_NO_UPDATE_CHECK=1`.

Rows retired: `internal/cli/doctor.go` deferred-2b, 866 against 600, ends `shrunk` at 564. New rows: doctor_host.go, trust.go, hostcheck/{profile,configdiff,codextrust,host}.go.

Tests: `doctor_host_test.go` (acceptance 1: only amq on PATH passes with one row per tool, a missing amq fails, no registry write without the flag; `--write-registry` writes and the next run reports current; acceptance 5, 6 and 7 through doctor; acceptance 7 writer side: `trust` output and a digest of every file under the test HOME before and after; grants absent, 0600 with `max_seats: 8`, 0666 fails with `owner_grants_insecure`; sleep and login service warnings, none off macOS), `internal/hostcheck/hostcheck_test.go` (names never values, the S4 GEMINI_API_KEY case; diff with redaction; codex bookkeeping ignored; untrusted roots; notify presence; pmset parsing; grants mode). Removed tests: the team config tests (4), the shared-index doctor test and the worktree rows test, each for a row 16.2 removes. Changed: the module-pin test now checks the tool row; the help test checks the new flags; the completion test lists `trust`; the scoped-alias test keeps `-p` for doctor.

Measurements: acceptance 9, doctor wall time with probes off on this Mac (Apple M5 Max, 64 GB; real PATH with claude, codex, grok, ollama with 5 models, amq, amq-remote, tmux, gh; temp HOME): 511 ms on the first run, 82 and 84 ms after (`/tmp` script timing three runs of the built binary). Under 2 s.

Behavior changes:
1. `doctor` no longer runs the team config or worktree checks, and `--profile` and `--session` are gone (the `-p` alias for `--project` stays). `doctor --json` drops `profile`, `workstream` and `profiles`, and the check rows drop `role`.
2. The `amq vs pinned launchapi module` row is now `tool/amq` (warn when the PATH amq is newer than the pin, as before).
3. New rows as listed; `doctor` writes nothing unless `--write-registry` or `--probe` is given.
4. New verb `trust`.
5. The cli help example and the `team init` next-step text no longer name `--profile` for doctor.

Open: the status contract self-check row waits for 2b-1's contract to merge (it reads `status --json` schema 1 and its golden fixture). Outside an AMQ project, the `amq version` and `amq ops` rows still fail as in 2a (unchanged behavior; doctor run from a project directory is the usage).

Lead: halfway checkpoint accepted at 4ae925b2; the status self-check and the switch to `grants.Insecure` / `grants.Load` land in a follow-up after 2b-1 and 3-1 merge.

## Commit 4: the R12 probe runner and direct TTFT

Built:
- `registry.Evaluate` (predicate v2 of the S5 report, ported from `spike/piprobe/probe.mjs`): read_confirmed, go_test_confirmed (an end event that is not an error and shows an `ok` line; a start alone never counts), worktree_exact (porcelain lists only calc.go, nothing untracked, numstat `1 1`), independent_go_test, final_line_exact. `false_success` = PASS reported and the verifier says FAIL. A false claim in a run that ends `RESULT: FAIL` is not a false success (amendment 15). Tool errors are counted (the qwen2.5 malformed first edit).
- `registry.RunProbe` and `Tally`: a fresh git repository per run from the embedded fixture (the spike's calc module, held as strings so no stray package compiles), pi in `--mode json --no-session` with read, edit, write and bash, the events timestamped, then `go test -count=1`, `git status --porcelain --untracked-files=all` and `git diff HEAD --numstat -- calc.go`; the run directory is removed. `Tally` gives the registry record (runs, passes, false successes, tool errors, predicate `r12-v2`, pi version).
- `registry.MeasureTTFT` (ported from `spike/piprobe/ttft.mjs`): direct streaming request to Ollama's `/v1/chat/completions`, first reasoning delta and first visible content delta. doctor asks for up to 2048 tokens so a thinking model reaches visible text.
- `registry.PiProbeEnv`: the isolated pi agent directory `~/.amq-squad/probe/pi-agent` with a `models.json` for the Ollama provider (every local model, context 131072, reasoning for thinking models), `PI_OFFLINE`, `PI_SKIP_VERSION_CHECK`, `PI_TELEMETRY=0` (the S5 setup; never `~/.pi`). `registry.PiCommand`: the PATH pi, or the embedded runtime's executable and entry from its manifest.
- `doctor --probe` wires them: per local model with tool support, N runs (`--runs`, default 5) and a TTFT; per Codex catalog model one model-id turn; one `probe/<model>` row each; the results go into the registry it writes. A no-tool-calls model is not probed.

Rows: two new rows (probe.go, localprobe.go).

Tests: `probe_test.go` (acceptance 3 on the predicate: an honest pass; a model that claims an edit it did not make is a false success; a false claim that ends `RESULT: FAIL` is not; a start-only go test does not count; an untracked extra breaks worktree_exact; tool errors counted; end to end with a shell stand-in for pi: 5 of 5 honest runs give `verified`, a lying run is a false success, and a 5-run tally with one false success is `probe_failed` on probation; probe directories are cleaned up; TTFT on a fake stream; pi env and command), `doctor_host_test.go` TestDoctorProbeWritesProbeResults (probe rows, the no-tool-calls model skipped, the registry written with the verified local entry, its latency and the refused Codex model).

Measurements:
- Five end-to-end runs with the shell stand-in: 6.8 s (two `go test` per run on a cold test cache).
- Direct TTFT on this Mac (Ollama 0.40.8, nothing else loaded; `AMQ_SQUAD_TTFT_LIVE=gemma3:4b,qwen3.6:27b go test -run TTFTLive ./internal/registry/`, max 512 tokens): gemma3:4b text 2211 ms (load), then 76 and 78 ms; qwen3.6:27b text 14592 ms with reasoning at 8370 ms (load), then text 6669 ms with reasoning at 183 ms, and a third run with no visible text within 512 tokens (reasoning at 219 ms). This matches S5 (6827 to 9334 ms visible text for qwen3.6) and is why doctor asks for 2048 tokens.
- The R12 probe itself was not run against a real model: pi is not installed on this host (the built-in pi is 4-1's). The N=5 record for qwen3.6:27b therefore waits for 4-1 or an owner run of `amq-squad doctor --probe`.

## Commit 5: the usage-limit detector and the owner scan

Built:
- `internal/usagelimit` (new). `Detector.UsageLimitEvents(since)` is the scheduler's `UsageLimitSource` (2b-1, `internal/scheduler/usage_limit.go`): the usage_limit events at or after `since`, oldest first, each with when, provider, seat (from the line's cwd or the rollout's session_meta cwd, mapped through the seat working directories), reset time when the provider names one, and the source as `file:line`. `Scan` returns every kind for the owner scan. After 2b-1 merges the method returns `[]scheduler.UsageLimitEvent` (same fields); the kind stays on `Scan`.
- Claude (`~/.claude/projects/<encoded cwd>/**/*.jsonl`, `CLAUDE_CONFIG_DIR` honored): the S6 scanner's classification. An assistant `isApiErrorMessage` line whose text says the session, weekly, spend or usage limit was hit is `usage_limit`, with the reset read from "resets 5:00am (Asia/Jerusalem)"; other 429s are `http_429`; 5xx and overloaded are `server_error`; a `system` `api_error` line that mentions 429 or rate_limit is `retry_429`. With seats set, only the project directories of the seat working directories are read.
- Codex (`$CODEX_HOME/sessions/YYYY/MM/DD/rollout-*.jsonl`): an `event_msg` `error` or `stream_error` whose message says the usage limit was hit is `usage_limit` (reset from "try again in 1 hour 0 minutes"); a `token_count` whose `rate_limits` primary or secondary window is at 100 percent is `usage_limit` (reset from `resets_at` or `resets_in_seconds`); other 429 messages are `http_429`.
- The reset banner "You have N usage limit resets available" (every S4 Codex pane) never counts, on either provider; it is pinned by a fixture row in the Claude and the Codex fixtures and in the doctor test.
- The owner scan: `amq-squad doctor --usage-scan <duration>` adds a `usage scan` row (count of usage-limit events and of all signals in the window, and the two directories read) and one `usage/<provider>` row per file that carried a signal, with counts by kind. Read-only. Per lead ruling 1 the owner runs it and the measured list of files goes into the plan.

Which files carry the signal (ruling 4 of the brief): not measured on the owner's host by the implementer, because the implementer may not read `~/.claude` or `~/.codex` (lead ruling 1). What the repository shows: the S6 Claude scanner found its signal in the Claude transcripts above (S6 report, `spike/loadtest/scan_limits.py`); the S4 Codex panes show the TUI banner and the model refusal as an `error` JSON event, which the rollout files record as `event_msg`. The Codex token_count rate-limit fields and the error message wording are taken from the codex-cli rollout format and stay unconfirmed until the owner runs the scan.

Rows: two new rows (usagelimit.go, codex.go).

Tests: `internal/usagelimit/usagelimit_test.go` (acceptance 8: S6-shaped Claude and Codex files, including normal text that mentions "rate limit", "429" and the banner, give 0 events; one usage-limit line gives 1 event with its time and reset, and the same file's 429 and retry lines are `http_429` and `retry_429` in `Scan`; the since filter; Codex token_count and error events with resets and seat; the seat filter on both providers; the per-file summary; missing directories), `doctor_host_test.go` TestDoctorUsageScan (the file rows, the banner not counted). Fixtures under `internal/usagelimit/testdata/` are shaped like the S6 files; the S6 originals sit under `~/.amq-squad/spike/s6/` and were not read (deviation from acceptance 8's "replays the S6 session files", ruled by the lead).

## Rebase onto v3 after the 3-1 merge, and commit 6: doctor reads the grant through internal/grants

Rebase: `v3-2b-3` rebased onto `v3` at 2992a3cd. One conflict, in `docs/v3-manifest.yaml` (3-1's new rows next to this brief's rows), resolved by keeping v3's manifest and re-inserting this brief's rows; every rebased commit builds.

Built (commit 6):
- The `grants` doctor row now resolves the grant with 3-1's `grants.LoadFrom(~/.amq-squad/grants.yaml, <project>)`: the owner file's trust comes from `grants.Insecure` (file and directory not group- or world-writable, owned by the user, not a symlink), an untrusted file fails with kind `owner_grants_insecure` and the reason, a file that does not parse fails with kind `grants_invalid`, and the row shows the source, the file mode, the mode (auto, manual) and the seat cap (`MaxSeats`, default 12 per session, amendment 22). A squad.yaml field that tried to widen the grant turns the row into a warning that names it.
- `hostcheck.GrantsMode` and its test are deleted (the local stand-in until 3-1 merged); `hostcheck.GrantsPath` stays.

Tests: TestDoctorGrantsFileMode passes unchanged against the 3-1 loader (absent, 0600 with `max_seats: 8`, 0666 failing with `owner_grants_insecure`).

## End checkpoint

Acceptance:
1. A missing tool gives one status line and exit 0; doctor on a host with only amq passes (TestDoctorHostWithoutOptionalToolsPasses). Met.
2. Registry write is idempotent: tested (TestRegistryWriteIdempotentAcrossRuns, TestDoctorWriteRegistry), and on this Mac two `doctor --write-registry` runs gave byte-identical files (no local latency measured in a plain run). Met.
3. Probe: a claimed edit that was not made is a false success and blocks `verified`; 5 of 5 gives `verified`; a false claim in a RESULT: FAIL run is not a false success (TestEvaluatePredicate, TestRunProbeAndTally). Met on fixtures and a stand-in pi; no real-model run (pi not installed here).
4. Codex model id: the S4 closure outputs mark gpt-6-astra and gpt-5.5 unusable on 0.147.0 and `CheckLaunch` refuses them (codexmodel_test.go). Met.
5. Shell profile: the `export GEMINI_API_KEY=` fixture warns with the name, never the value. Met.
6. Config diff: a changed fixture `~/.claude/settings.json` shows the diff against `~/.amq-squad/preflight/claude-settings.json`. Met.
7. Untrusted roots and `trust`: listed, and `trust` changes no file under the test HOME (digest before and after). Met.
8. Usage-limit detector: S6-shaped files 0 events, one usage-limit line 1 event. Met on fixtures (lead ruling 1).
9. doctor wall time on this Mac with probes off: 511 to 588 ms on a first run, 81 to 84 ms after. Met.

registry.yaml written on this Mac (`amq-squad doctor --write-registry`, real PATH, temp HOME, so every vendor login reads as logged out), keys only:
- top level: `schema`, `host` (`ram_gb`, `cpu`, `gpu`), `tools`, `entries`.
- tool keys: `name`, `found`, `path`, `version`, `verified_version`, `version_check`, `login`, `login_source`; ten tools, eight found (claude, codex, grok, ollama, amq, amq-remote, tmux, gh); pi and cursor-agent missing.
- entry keys: `runtime`, `provider`, `model`, `effort`, `verified`, `state`, `reason`, `cost_tier`, `privacy_class`, `quota_pool`, `concurrency_pool`, `concurrency_cap`, `concurrency_source`, `untrusted_input`, `probation`, `latency_ms`, `latency_reasoning_ms`, `capability_tier`, `cost_index`, `latency_index`, `strengths`, `work_classes`, `probe_runs`, `probe_passes`, `probe_false_successes`, `probe_tool_errors`, `probe_predicate`, `probe_version`.
- 45 entries: 20 Claude (4 models x 5 efforts) and 10 Codex (2 x 5), all `logged_out` under the temp HOME; 15 pi + Ollama (5 models x 3 efforts): gemma3:4b and qwen2.5vl:7b `no_tool_calls`, qwen3.6:27b, qwen2.5:32b and llama3.2-vision `missing_tool` (no pi runtime). 0 verified on this run, as expected without logins or pi.

doctor.go: 866 -> 567 lines (target 600), row `shrunk`. The probe code is in new rows: `internal/cli/doctor_host.go`, `internal/cli/trust.go`, `internal/registry/*` (8 files), `internal/hostcheck/*` (4), `internal/usagelimit/*` (2).

Open, for the follow-up after 2b-1 merges:
- The status contract self-check row (reads `status --json` schema 1 against its golden fixture).
- `usagelimit.Detector.UsageLimitEvents` returns `[]scheduler.UsageLimitEvent` and is wired as the scheduler's source.
- The owner runs `amq-squad doctor --usage-scan 168h` and the measured list of files that carry the limit signal goes into the plan (ruling 4).
- An R12 run with N=5 on a real model waits for the built-in pi (4-1) or an owner run of `amq-squad doctor --probe`.
- 2b-2 writes the preflight copies at `~/.amq-squad/preflight/` and calls `registry.RehashRuntime` and `registry.CheckLaunch` before exec (both exported here).

## Commit 7: the Codex review of 33a3d72d (ten defects) and the Detect data race

One commit, as ruled. Each defect has one test row. "Fails when reverted" means the row was run with the fix undone in place (`/tmp` revert script: replace the fix with the old code, run the row, restore the file), unless noted.

| # | Defect | Fix | Test row | Reverted |
| --- | --- | --- | --- | --- |
| P1 1 | The config diff's anchored key matcher missed nested keys; `{"env":{"GEMINI_API_KEY":"private-value"}}` printed the value | `hostcheck/redact.go` (new): settings.json is parsed as JSON and config.toml read as TOML (tables, dotted and quoted keys, inline tables). Secret-class fields are redacted recursively at any depth before any line is diffed: the name matches `*_API_KEY`, `*_TOKEN`, `*_SECRET`, `*KEY`, PASSWORD, TOKEN, SECRET, CREDENTIAL, Authorization, plus token formats (sk-, ghp_, github_pat_, xox, AKIA, AIza, eyJ). A changed secret shows as `-path: <redacted>` and `+path: <redacted>`. A file that does not parse falls back to unanchored line redaction | `hostcheck` TestDiffOwnerConfigRedactsNestedSecrets | fails |
| P1 2 | The Codex model probe discarded the exit status and took any "OK" substring | `ClassifyCodexOutput(model, version, out, exitOK)`: refusal texts first; otherwise usable only with exit 0 and a structured assistant completion (`codex exec --json`: `item.completed` / `agent_message`, or the older `msg.agent_message`) whose text is exactly `OK`; plain-text "unsupported model" is a refusal | `registry` TestClassifyCodexRequiresStructuredOK | fails |
| P1 3 | `doctor --probe` ran PiCommand before the runtime check | the runtime re-hash runs first; the R12 probe runs only when it passed, and only for the built-in runtime (a PATH pi is never probed, R2); otherwise a failing `probe/pi` row | `cli` TestDoctorProbeVerifiesRuntimeFirst | fails |
| P1 4 | doctor trusted the replaceable on-disk manifest (empty digest) | `RehashRuntime` requires the trusted digest; `registry.EmbeddedRuntimeManifestSHA256` is the binary's embedded manifest digest (set by the build that embeds pi, 4-1; "" here, so any installed runtime fails until then); doctor passes it | `registry` TestRehashRequiresTrustedDigest | fails (both the empty-digest guard and the comparison reverted) |
| P1 5 | A symlinked manifest with identical bytes passed, an ancestor symlink was followed, and a swap between Lstat and Open slipped past the type check | `runtime_open_unix.go` (new): the root is opened with `O_DIRECTORY\|O_NOFOLLOW` and every path one component at a time with `openat` and `O_NOFOLLOW`; the manifest and every file are checked (fstat) and hashed from the descriptor that was opened; the walk refuses any symlink entry. Off unix, an Lstat-per-component opener (`runtime_open_other.go`, not race-free; 3.0 hosts are macOS) | `registry` TestRehashRefusesSymlinks | the opener and the walk are two layers; reverting the opener alone leaves the walk's refusal, so the row was checked by reasoning against the old code (manifest name skipped before the type check, `os.ReadFile` following the link) |
| P1 6 | skip-worktree on calc_test.go, `t.Skip` in TestGreeting, a comment edit in calc.go and RESULT: PASS gave a pass with Greeting unchanged | `worktree_exact` compares every file outside `.git` with immutable expected bytes held in the probe (go.mod and calc_test.go unchanged, calc.go with exactly the stated edit, nothing extra, no symlink); `independent_go_test` runs the pristine tests on a fresh copy (pristine go.mod and calc_test.go plus the model's calc.go) with `GOWORK=off`, `GOENV=off`, `GOFLAGS=-count=1`; git metadata is not evidence | `registry` TestRunProbeRejectsTamperedFixture (a stand-in pi that does exactly that sequence) | fails |
| P1 7 | `~/.amq-squad/probe/pi-agent/models.json` as a symlink truncated an outside file | `registry.SafeDir` refuses a symlinked or non-directory component below home; models.json is written to a temp file and renamed over the name; the probe run directory uses the same check | `registry` TestPiProbeEnvRefusesSymlinks | fails |
| P2 8 | `OLLAMA_HOST=https://remote.example` was contacted, even with no ollama on PATH, and redirects were unrestricted | the server is contacted only with ollama on PATH and a loopback endpoint (localhost, 127.0.0.0/8, ::1); anything else is one status line "ollama host not local, skipped"; no redirect is followed; TTFT checks the same | `registry` TestDetectContactsOnlyLoopbackOllama | fails |
| P2 9 | A tally with zero passes and five false successes was an ok probe row | a tally short of N of N, with any false success, or with zero runs is `fail`; a probe that fails to run is `fail` | `cli` TestDoctorFailedProbeFails | fails |
| P2 10 | The same event in two rollout files counted twice | events carry the provider's session id (Claude `sessionId`, Codex `session_meta` id); `Scan` deduplicates on provider, session id, seat, time, kind and text; the first source wins | `usagelimit` TestRotatedCopiesCountOnce | fails |

Changed tests: the Codex fixture and probe tests use the new signature and `codex exec --json`; the re-hash tests pass the trusted digest; the probe predicate tests use the new worktree state; TestDoctorProbeWritesProbeResults runs on a verified runtime with the test's trusted digest; TestDiffOwnerConfig expects the canonical JSON lines and the redacted secret pair.

Stated limits, unchanged (lead): the scheduler event type and launch-time enforcement wait for 2b-1 and 2b-2; a real Codex session check and an N=5 real-model run stay open evidence until the owner runs them.

### The Detect data race

Found by the lead: `go test -race ./internal/registry` reported a race in `registry.Detect`. Each tool goroutine received the whole `Detection` by value, and copying it read `d.Host` while the host goroutine wrote `d.Host`. TestDetectVersionsLoginAndHost and TestRefusedCodexModelIsUnusableBeforeLaunch failed under `-race` and passed without it.

Fixed: every goroutine writes only its own result slot (`tools[i]`, `host`, `models` and `ollamaErr`), `detectTool` takes the pi runtime directory as a string instead of the `Detection`, and `Detection` is assembled after `Wait`. The test fake's command log (`fakeHost.ran`), appended to from the same goroutines, is now guarded by a mutex.

Gate: the standard gate plus `go test -race -count=1 ./internal/registry ./internal/hostcheck ./internal/usagelimit`, from this commit on. The three packages pass under `-race`.
