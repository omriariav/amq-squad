# amq-squad 3.0, brief 4-2: the Claude Code hooks plugin

Branch `v3-4b`, cut from `v3` at 2c7a38e6. Scope: brief 4-2 (plan 4.3, 4.4; R35, R36). Hooks only; no skill changes; `main` untouched.

## Commit 1: the hook adapter, the mapping and acceptance 1, 2, 6

### Built

- `plugins/claude/hooks/hooks.json`: four events (SessionStart, UserPromptSubmit, PreToolUse `*`, PostToolUse `*`), each a command hook `sh "${CLAUDE_PLUGIN_ROOT}/hooks/amq-squad-hook.sh" <event>` with `timeout: 5` (seconds).
- `plugins/claude/hooks/amq-squad-hook.sh` (POSIX sh, no jq or python at run time). First line of logic: `AM_ME` unset or empty exits 0 with no output. In a seat:
  - session-start, user-prompt: `context --me $AM_ME --json`, output wrapped as `hookSpecificOutput.additionalContext` (JSON-escaped with awk), every turn (open ruling 2: R35).
  - pre-tool: `guard --me $AM_ME --stdin`; the single JSON line is parsed with `sed -E` (the verdict is the first field; `reason` and `target` are already JSON-escaped by the verb and are copied as-is, so no unescaping is needed).
  - post-tool: `evidence ingest --me $AM_ME`, stdout discarded.
- `scripts/test_claude_hooks.py` (unittest, runs against the built binary; 15 tests) and a `claude-hooks-test` target in `Makefile`, added to `make ci`.
- No Go file added or changed, so no manifest row.

### Mapping (acceptance 1)

| guard | hook output |
| --- | --- |
| allow (`allowed`) | `permissionDecision: allow` |
| deny (`seat_merge`, `force_push`, `push_delete`, `push_other_branch`, `protected_path`, `grants_invalid`) | `deny`, reason `amq-squad guard: <guard reason>` |
| needs_gate (`gate_release`, `gate_send`, `gate_credential`, `gate_grow`) | `deny`, reason `raise the gate for <action_class> <target>` |
| guard exit != 0, unparsable output, `amq-squad` not on PATH | no output, exit 0, a line in `$AM_ROOT/extensions/amq-squad/hooklog/$AM_ME.log` |

`TestMappingTable` drives all 11 reason codes of 2a-3 through the real binary: it first calls `guard` directly to confirm the row produces that reason code, then runs the hook and checks the decision (and, for needs_gate, that the reason equals `raise the gate for <action_class> <target>` built from guard's own fields; for deny, `amq-squad guard: <reason>`). A second test pushes a target with quotes, a backslash and a single quote through the needs_gate reason and parses the output as JSON.

### Acceptance 2 and 6

- `git tag v1` in a seat with no answer: `deny`, reason exactly `raise the gate for tag git tag v1`, no `ask` anywhere in the output, exit 0; the modelled Claude decision is "block" (a tool error to the model), never "prompt".
- Outside a seat (both `AM_ME` unset and `AM_ME=""`), all four events: exit 0, empty stdout and stderr, no log file. Overhead over 60 runs (15 per event, `sh` start plus one test): p50 6.2 ms, p95 7.1 ms, max 7.9 ms (limit 20 ms). The timing test takes the best of three batches because other agents load this host; the first attempt passed in the recorded runs.

### Revert runs (evidence/reverts-mapping.txt)

Each mutation applied to the plugin, tests run and required to fail, plugin restored byte for byte, same tests re-run and required to pass. Six mutations, all caught: allow emits nothing; deny emits nothing; needs_gate mapped to `ask`; needs_gate mapped to `allow`; needs_gate reason without class and target; the `AM_ME` early exit removed (caught by the overhead test: p95 42 ms).

### Gate

`docs/v3-spike/4-2-evidence/gate-1.txt`: full output, PASS. go build, go vet, go test ./..., manifest-check --final, the linux and windows builds, and the plugin checks `make ci` runs (`fmt-check skills-check skill-routing-check release-validator-test go-files-scope-test claude-hooks-test`; skills-check is the plugin manifest check: 14 SKILL.md frontmatters valid, version markers match manifests). The venv under the job directory supplies PyYAML, which this host lacked.

`make -k ci` as a whole is red on v3 independent of this branch, recorded as informational in the same file: `readme-html-check` (README.html stale), `docs-html-check` (docs/skills.html stale), `skill-command-check` (the `./amq-squad --help` parser finds 19 verbs, expects 20) and `skill-invocation-check` (23 documented invocations, such as `amq-squad init`, `team member update`, `evidence recover`, do not run on the v3 binary). The file ends with `git diff --stat v3` over the inputs of those targets (README, docs/skills, plugins/skills-src, plugins/*/skills, internal, cmd): empty, so this branch did not cause them. They are phase 5 skill and doc work; named here so the lead can rule on whether a green `make ci` is required at merge.

### Deviations and facts not found in the repository docs

- The repository holds no Claude Code hooks API reference. What the hook relies on (the `hooks.json` shape for plugins, `${CLAUDE_PLUGIN_ROOT}`, `timeout` in seconds, `hookSpecificOutput` with `permissionDecision`/`permissionDecisionReason`/`additionalContext`, exit 2 blocking in UserPromptSubmit and feeding stderr in PostToolUse) is from the implementer's knowledge of Claude Code and was checked against: the S4 fault table (Claude 2.1.305, deny/timeout/exit 1/missing command), `claude plugin validate` and `claude plugin details` on the installed 2.1.310 (it lists the four hooks as "harness-only"). The live decision behavior of `allow` and of `additionalContext` was not run in a live seat: see open rulings.
- `allow` is emitted as an explicit `permissionDecision: allow` as the brief says. In Claude Code an explicit allow also skips the permission prompt the seat would otherwise raise for that tool. See open ruling A.

### Open rulings

A. Should `allow` emit an explicit `permissionDecision: allow` (brief, done) or emit nothing and leave Claude's own permission flow in charge? An explicit allow means guard's allow widens what an unattended seat may do without a prompt; emitting nothing keeps the seat's permission mode as the only authority. The mapping test pins the brief's reading.
B. No live Claude run was possible in this job (a fresh HOME has no credentials and the owner's real HOME is off limits), so the fault table and the injection are exercised through a runner that executes the hook command exactly as hooks.json states it (shell, stdin payload, killed at the configured timeout) and models Claude's decision rule from the S4 table. Needed from the lead for the end checkpoint: either accept that, or run the live table once on the owner's side with the installed plugin.

## Commit 2: the fault table, context, evidence, user-scope install and the docs section

### Built

- `docs/claude-hooks.md`: install at user scope, what each event does, the verdict mapping, and the fail-open cases with the S4 numbers (R36). No Go file changed; no manifest row.
- No plugin code change in this commit; the tests of commit 1 already carry acceptance 3, 4, 5. This commit adds their revert runs and the install evidence.

### Acceptance 3: the fault table (evidence/fault-table.json, evidence/test-run-verbose.txt)

| Condition | Claude decision | Measured (hook alone, run the way hooks.json states it) | S4 (live seat, tool call to result) |
| --- | --- | --- | --- |
| explicit deny (`git push --force`) | blocks | 0.019 s | 0.166 s |
| timeout (the verb replaced by `sleep 30`, configured 5 s) | proceeds; process group killed | 5.002 s | 5.111 s |
| guard exit 1 (malformed payload) | proceeds, a log line `fail-open: guard exit 1` | 0.017 s | 0.356 s |
| missing command (`amq-squad` absent) | proceeds, a log line | 0.010 s | 0.440 s |
| plugin script itself missing | shell exit 127, proceeds | 0.006 s | (S4 row c: 0.440 s, exit 127) |

The measured column times the hook process; the S4 column times the whole tool call in a live Claude, so only the shape and the 5 s timeout compare (5.002 s vs 5.011 s of hook time in S4, `durationMs 5034`). Not a live re-run (open ruling B). Also tested: context, evidence ingest and guard identity errors never leave the hook as exit 2 (UserPromptSubmit would be erased).

### Acceptance 4 and 5

- Context: for a seeded seat (role.md with quotes and a backslash, rules file, one task added and claimed through `amq-squad task`, one open gate record) SessionStart, then UserPromptSubmit twice, each carry `additionalContext` with role text and digest, rules digest, the current task and the open gate; the embedded JSON parses and the role text round-trips exactly. Each turn injects the full context (open ruling 2). Hook time with context about 15 ms.
- Evidence: 20 PostToolUse runs give 20 ledger lines with seq 1 to 20, no log lines.

### Acceptance 7 (evidence/install-user-scope.txt)

A fresh HOME and an empty project directory under the job directory: `claude plugin marketplace add <worktree> --scope user`, `claude plugin install amq-squad@amq-squad --scope user`. `claude plugin list` shows scope user; `claude plugin details` shows Hooks (4): SessionStart, UserPromptSubmit, PreToolUse, PostToolUse. The listing of the project directory after install is empty; the hook files sit under HOME/.claude/plugins/cache. `claude plugin validate plugins/claude` passes (one warning: no icon). A test also asserts the repository has no `.claude/settings.json` and the plugin no `.claude` directory.

### Revert runs (evidence/reverts-faults.txt)

Eleven mutations, all caught, each restored and re-run green: guard exit 1 turned into a deny; guard exit 1 propagated as exit 2; missing binary turned into a deny; timeout 2 s and 30 s in hooks.json; context and ingest failures propagating their exit code; ingest not called; user-prompt not injecting; quote escaping removed; `--json` dropped. The explicit-deny row was reverted in reverts-mapping.txt (deny emits nothing fails `test_deny_blocks`).

### Gate

evidence/gate-2.txt: same set as gate-1, PASS; the same four `make ci` targets are red for the reasons given under commit 1.

### Deviations and open rulings

As commit 1 (A: explicit allow; B: no live Claude run). Also: (C) plugin version stays 2.31.0 so a user-scope install shows 2.31.0 and Claude caches by version, so a hook change needs a plugin version bump at release; I did not bump (release prep owns it). (D) The "needs_gate turns to allow after an answer" path is the verb's (3-1, tested in the Go matrix); no end-to-end signed-answer run through the hook here.

### Lead rulings applied (after the halfway checkpoint)

1. Inherited red `make ci` targets, not fixed here (phase 5 docs pass): `readme-html-check`, `docs-html-check`, `skill-command-check`, `skill-invocation-check`. Every target this diff touches is green (`skills-check`, `skill-routing-check`, `release-validator-test`, `go-files-scope-test`, `claude-hooks-test`, `fmt-check`).
2. Open ruling A closed: allow emits an explicit `permissionDecision: allow`. The consequence is stated in docs/claude-hooks.md: in a seat, guard's verdict replaces Claude Code's own permission prompts for the hooked tool calls.
3. Open ruling B closed as a deviation: the fault table is exercised through a hook runner that models Claude's decision rule from S4, not a live Claude. The live confirmation against a real Claude seat (owner credentials, a live launch) is listed for 2b-2 milestone B's live run or the phase 5 release checks, whichever comes first.
