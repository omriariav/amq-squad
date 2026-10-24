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
