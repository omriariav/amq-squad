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
