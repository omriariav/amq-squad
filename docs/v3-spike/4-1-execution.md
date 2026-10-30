# amq-squad 3.0, brief 4-1: the built-in pi package

Branch `v3-4-1`, cut from `v3` at f0129ebf. Brief: docs/v3-phase2-briefs.md "4-1" with its rulings and review 1. Plan revision 2.6: 3.1, 3.2, 4.1 (including the pi launch and resume paragraphs), 4.2, 4.4, 16.3; R1, R2, R5, R7, R8, R35, R36, R38; amendments 11 and 23.

Contracts from branches that are not merged yet, coded against their committed shapes (lead ruling, 2026-10-24): 2b-2's seat-exec (`git show v3-2b-2:internal/seatexec/pi.go`, the resume caveat and `PiSessionFileExists`, "provisional until 4-1 pins the embedded pi's layout") and 2b-3's registry (`registry.RehashRuntime`, `registry.PiCommand`, `registry.RunProbe`, `RuntimeManifestName` / `RuntimeMarkerName`). The wiring into seat-exec and `doctor --probe` lands after those merge.

Standard gate on every commit: `go build ./... && go vet ./... && go test ./... && scripts/manifest-check.sh --final`, `GOOS=linux go build ./...`, `GOOS=windows go build ./...`; plus the package's `npm run lint` and `npm test` in `pi/`, and the darwin arm64 build with the payload (`scripts/build-pi-runtime.sh build`).

## Lead rulings during the brief

1. (2026-10-24, at start) Build on v3 alone; code against the committed shapes of v3-2b-2 and v3-2b-3; do not merge unmerged branches. `internal/piruntime` with embed, unpack, launch check, argv and env, the pinned session-file layout and `CheckResume` is the shape; the wiring into seat-exec and `doctor --probe` is a follow-up after those merge; the N=5 real-model R12 run goes through the kept command path of this branch.
2. (same) The full runtime re-hash at every launch stands (2b-3 reviewer ruling, registry review findings 3 to 5: tampered runtime files, replaceable manifest, symlink bypass). The embedded manifest digest is the trust root and every launch verifies the whole tree against it. Plan 4.1's "manifest and executables only on a warm launch" is corrected in revision 2.7. Acceptance 2 is reported as two numbers: the re-hash and the rest of the warm launch (the under-60 ms bound applies to the rest).

## Commit 1: the embedded runtime, the pi and lead verbs, the @amq-squad/pi package

Built:

- `internal/piruntime` (new, ported from `spike/embedpi` with tests):
  - `manifest.go`: the payload manifest (schema 1: version, platform, payload SHA-256, pi and Node versions, executable and entry, the package directory, the fd and rg tool map, every file with path, size, mode 0444 or 0555 and SHA-256) and `ParseEmbedded`, which checks the trust root: the embedded `manifest.sha256` must carry the SHA-256 of the embedded `manifest.json`. A missing, malformed or mismatched digest is an error, never a warning. `Validate` refuses non-local paths, duplicates, a missing executable, entry, package or tool.
  - `install.go`: `Ensure`. Cold: the payload digest is checked against the manifest before anything is written; an exclusive flock (internal/flock); unpack into `.unpack-*` (regular files only, exact sizes, gzip footer read); full verify; the on-disk manifest (byte-identical to the embedded one) and the completion marker; atomic rename to `~/.amq-squad/runtime/<version>-<sha256[:12]>/`. Warm (ruling 2): the on-disk manifest's SHA-256 must equal the embedded digest, the marker must name this payload, the runtime base and directory must be real directories, and every file is re-hashed against the embedded manifest (not the on-disk copy, so a replaced manifest cannot widen what passes): no file missing, none extra, none a symlink, each size, mode and SHA-256 exact. A rejected runtime is refused (`Reject`, exit 2) and never repaired by the launcher. `Verify` is the same check for doctor. `Runtime.PiArgv` is node plus the pi entry (the command prefix 2b-3's `registry.ProbeConfig.Pi` takes).
  - `pack.go`: `Pack`, the normalized payload builder (sorted names, PAX, modes 0555 or 0444, no times or owners; a symlink or special file in the tree is refused), ported from `spike/embedpi/pack.py`. `scripts/pipack` is its command for the build script.
  - `prune.go`: `List` (runtimes with a marker, current first, then newest) and `Prune` (keeps the last two and never the current one; stale `.unpack-*` older than an hour; makes read-only directories writable first). Only `doctor --fix` calls it (ruling 3).
  - `launch.go`: `Prepare` for the lead and every pi seat: the agent dir `~/.amq-squad/pi-agent/<seat>` (seat name `[A-Za-z0-9._-]+`, not `.` or `..`); `models.json` with only the seat's models and `settings.json` with `enabledModels`, `defaultProvider`/`defaultModel`, `defaultThinkingLevel` (the registry entry's effort), `enableInstallTelemetry: false` and `shellPath: /bin/bash`; fd and rg linked into `<agent-dir>/bin` (an owner's regular file there is left alone); the environment: `PI_OFFLINE=1`, `PI_SKIP_VERSION_CHECK=1`, `PI_TELEMETRY=0` and `PI_CODING_AGENT_DIR` forced (not "unless set", unlike the spike), credentials scrubbed with seat-exec's rule (`*_API_KEY`, `*_TOKEN`, `*_SECRET`, `_SECRET_`, `*_PASSWORD`, the vendor names, `SSH_AUTH_SOCK`) unless the runtime declares the name, and `BASH_ENV`, `ENV`, `ZDOTDIR`, `PI_CODING_AGENT_SESSION_DIR`, `PI_PACKAGE_DIR` dropped (the tool shell runs without the owner's profile, R16, amendment 17). argv is absolute: `<runtime>/node/bin/node <runtime>/pi/node_modules/@earendil-works/pi-coding-agent/dist/bundle/cli.js -e <runtime>/pi [pi args]`. Nothing is looked up on PATH (R2).
  - `session.go`: the pi 1.3.0 session layout pinned from the package's `docs/session-format.md`: `<agent-dir>/sessions/--<cwd with the leading separator removed and / \ : as ->--/<timestamp>_<id>.jsonl`, matched exactly (a file for another id that contains this id does not count, unlike 2b-2's provisional substring walk); `CheckResume` (amendment 23 (a), R5): no `--session-id` is refused, a ledger without a tool result resumes (create-if-missing is upstream's intent for a seat that never took a turn), a ledger with a tool result and no session file is refused with `pi_resume_context_lost`. This is the function seat-exec calls in place of its provisional one once 2b-2 merges.
  - `embed_amqpi.go` (build tag `amqpi`) embeds `payload/{manifest.json,manifest.sha256,payload.tar.gz}`; `embed_none.go` is the stub for every other build: `Load` returns `ErrNotEmbedded`, so linux and windows compile without the payload and refuse to start a pi lead.
- `scripts/build-pi-runtime.sh` (ported from `spike/embedpi/build.sh`, packaging option (a)): `fetch` checks the S3 pins (Node 22.21.1, fd 10.5.0, ripgrep 15.2.0, the same SHA-256 values) and downloads only what is absent; it runs `npm ci` from `pi/package-lock.json` (`NPM_OFFLINE=1` uses only the cache). `build` stages Node, the pi tree with the S3 strip, the package and fd/rg, packs, and builds `build/pi/amq-squad` with `-tags amqpi -trimpath -ldflags '-s -w'`; it fails above 250 MB. No download happened in this brief: every file came from the S3 caches under `~/.amq-squad/spike/s3/` (`npm ci --offline`).
- `pi/` (the package, ruling 2): `package.json` (`@amq-squad/pi`, `pi.extensions`, pi as a pinned dev dependency and a `*` peer, never a runtime dependency) and `package-lock.json`, generated offline from the S3 cache; it resolves the same 118 packages with the same integrity hashes as `spike/embedpi/package-lock.json` (checked by script), so the runtime pin is unchanged. pi stays 1.3.0 (ruling 1): PR #1188 is not in any AMQ module in the cache (v0.107.0 is the newest), so no newer verified version is known. Extensions:
  - `extensions/amq-squad.ts`, the entry: inactive without a valid AMQ identity; otherwise it runs the bridge, the tools, the model restriction and the three hooks.
  - `lib/hooks.ts`: `before_agent_start` calls `amq-squad context --me H --json` and writes two system prompt sections every turn (`amq-squad-role`: the role text; `amq-squad`: seat, the AMQ definition, rules path and digest, the current task with its declared files, open gates, warnings); the first run of a session also returns the role as a custom message (`amq-squad-role`), and a resumed session that already holds it gets none. `tool_call` calls `amq-squad guard --me H --stdin` with `{toolName, input, toolCallId, cwd}` and a 5 s bound (`AMQ_SQUAD_HOOK_TIMEOUT_MS`); guard missing, not startable, timed out, exiting non-zero, printing no verdict or an unknown verdict all block (R36). `deny` blocks with its reason code; `needs_gate` holds (below). `tool_result` sends one `tool_result` payload (`outcome: ok|error`, so ingest renews the seat's lease, content capped at 64 KiB) to `amq-squad evidence ingest --me H`; a failed ingest never blocks.
  - `lib/tools.ts`: native `amq_send`, `amq_drain`, `amq_reply` (through the absolute `amq` the launcher resolved, bodies on stdin with `--body -` so an "@" body is never read as a file, recipients and kinds checked), `amq_task_list`, `amq_task_claim`, `amq_task_done` (through the kept `amq-squad task` verbs; `done` needs evidence), and for the lead `amq_task_add` (one class from the closed list, declared files, depends-on, pool).
  - `lib/models.ts`: the third layer of the model restriction: a model outside `AMQ_SQUAD_MODELS` is switched back on `session_start` and `model_select`, and `before_provider_request` throws for it.
  - `lib/hold.ts`: the hold seam: the first decision from any face (local dialog, remote answer, timer, the turn's abort signal) wins, a throwing face blocks, the loser is dismissed through its signal; the retry cap (S5 open question 4): the same call (tool and input) blocked once is blocked at once, at most 3 blocked holds a session and 5 holds a turn.
  - `lib/amq-bridge.ts`: AMQ's reference bridge (v0.107.0 `integrations/pi/amq-bridge.ts`, MIT, attributed) vendored and raised to revision 4 (16.3): liveness advertises `bridge_revision: 4` with the session id; a held call under a ref the bridge owns writes an `interaction` line (options "Allow once" / "Block", the manifest hash of the call, `expires_at`, presence `remote`, or `local_only` for a typed gate), applies an `answers/<sha256(ref NUL id)>.json` only when protocol, ref, interaction id, manifest hash, time and option bind, writes `interaction_resolved` (`answered` or `answered_elsewhere`), and closes open approvals as `run_ended` on the ref's terminal event.
  - Holds: `needs_gate` holds the call; the approving face is the core: guard is re-run every second and allows once a verified owner-signed answer is recorded (R32). A local dialog or a bridge answer can only block a typed gate. Tools named in `AMQ_SQUAD_APPROVE_TOOLS` hold for an approval that a local confirm and a bridge answer race for. Every hold and guard block is also an evidence line (`type: hold`, `decision`, `face`).
  - `scripts/lint.mjs` (erasable syntax through `node:module` `stripTypeScriptTypes`, `.ts` relative imports, pi imported as types only, package and lock pins agree, no `console.log`) and `test/*.test.ts` (node:test with Node's type stripping, no compiler: no TypeScript download was available within the S3 pins).
- `internal/cli/pi.go` (new): `amq-squad pi run` (the kept pi command path: `--seat`, `--model` repeatable, `--thinking`, `--lead`, `--approve-tools`, `--resume`, `--no-package`, `--dry-run`, pi arguments after `--`; `AMQ_SQUAD_BIN` is this binary's resolved path, `AMQ_SQUAD_AMQ` the absolute `amq`; `AMQ_SQUAD_PI_TIMING=1` prints `rehash_ms` and `rest_ms`), `pi verify`, `pi path`. `internal/cli/pi_lead.go` (new): `amq-squad lead`: under an AMQ identity it is that seat; without one it is the solo lead of the directory: AMQ root, mailbox, role, built-in rules and task store under `~/.amq-squad/solo/<dir>-<sha256(project)[:12]>/` and nothing inside the repository (R7); `--team` gives the team-lead role, `amq_task_add` and `--exclude-tools write,edit` (R19). Default model `ollama/qwen3.6:27b` (the S5-verified local entry) until the registry chooses. `pi_exec_unix.go` / `pi_exec_other.go`: the exec seam.
- `internal/cli/doctor_pi.go` (new) and two lines in `doctor.go`: the `pi runtime` row (full re-hash, or "not unpacked yet", or "not embedded in this build"), the `pi runtime versions` row (warns about runtimes beyond the last two), and `doctor --fix`, which removes a rejected current runtime (the next launch unpacks it again) and prunes the rest.

Tests:
- `internal/piruntime/piruntime_test.go`: cold then warm (the warm launch re-hashes every file); the trust root (missing, malformed, mismatched digest; a changed manifest under the pinned digest); the payload digest is checked before any write; acceptance 2 tamper table (a changed byte in the executable, a tool, a JS chunk, the package; an extra file; a missing file; a symlinked directory on the entry path; a symlinked entry file; a replaced on-disk manifest; a marker for another payload): each refuses and leaves the directory in place; a symlinked runtime directory; 4 concurrent installs give one cold install; deterministic pack; pack refuses a symlink; prune keeps the current and the newest other one; prune removes a read-only runtime; `Prepare` scrubs `GEMINI_API_KEY`, `OPENAI_API_KEY`, `GH_TOKEN`, `BASH_ENV`, `SSH_AUTH_SOCK`, keeps a declared `ANTHROPIC_API_KEY`, forces `PI_TELEMETRY=0`, writes one model and the settings, links rg, refuses seat `..`; the session layout and `CheckResume` (fresh seat, no tool result, lost context, a substring match does not count, present session, no `--session-id`).
- `internal/cli/pi_test.go`: acceptance 1 shape (the lead execs the unpacked Node with absolute paths, `PATH=/usr/bin:/bin`, `GEMINI_API_KEY` scrubbed); acceptance 7 (R7: `git init` + one commit, the lead started from a subdirectory: `git status --porcelain --untracked-files=all --ignored` is empty, the repository tree is unchanged, every file written is under `$HOME/.amq-squad/`, the project is the repository top level); the team lead has no write tools and the team role; the resume refusal through `pi run --resume`; a tampered runtime is not launched; no payload gives `ErrNotEmbedded`. `internal/cli/doctor_pi_test.go`: the version rows and `--fix` pruning; `--fix` repair of a rejected runtime and a clean relaunch.
- `pi/test/` (28 tests): guard fails closed on exit 1, timeout, missing and unset binary, no verdict, unknown verdict (acceptance 3, unit); verdict mapping and the exact payload and argv guard receives; the context section names AMQ as the Agent Message Queue and "not Advanced Message Queuing"; one ingest payload with an outcome; config needs an identity; the hold seam: remote before local and local before remote (the loser dismissed), a late second answer ignored, timeout, abort, a throwing face, three parallel holds resolving independently, the retry cap; the native tools' argv (bodies on stdin, bad recipient and kind refused, evidence required, class checked) and their execution against stand-in binaries; the entry against a stand-in pi: `before_agent_start` injection every turn with the role message once, none on a resumed session, guard allow / deny / garbage / timeout / missing binary on `tool_call`, `needs_gate` held until guard allows (a signed answer recorded mid-hold), `needs_gate` timeout then the same call capped, an approval tool with a local confirm, and bridge revision 4 end to end (liveness revision 4 with the session id, a remote request delivered and started, the `interaction` line, the answer file approving the call, `interaction_resolved: answered`, a mismatched manifest hash never approving, `run_ended` on the terminal event).
- Changed: `cli_test.go` (`lead` and `pi` join the public catalog).

Measurements (Apple M5 Max, macOS 25.6.0, Go 1.27.1; raw output in `~/.amq-squad/spike/4-1/measure-1.json` and `runs/`; prompts under `env -i HOME=... PATH=/usr/bin:/bin TERM=xterm`, where `command -v pi node bun npm` finds nothing):

| Measurement | v3 binary (4-1) | S3 decided build |
| --- | --- | --- |
| Binary | 68.16 MB (`build/pi/amq-squad`, amq-squad plus the payload) | 59.27 MB (the spike launcher alone) |
| Embedded payload | 55.65 MB | 54.52 MB |
| Unpacked runtime | 179.32 MB in the manifest, 6,439 files (6,441 on disk with the manifest and marker) | 179.25 MB, 6,432 files |
| Cold install (unpack + full SHA-256), real `~/.amq-squad/runtime/` | 1,064 and 1,072 ms in two runs; two outliers at 10,758 and 18,551 ms where wall time was far above CPU time (user 1.09 s, sys 2.04 s): I/O wait, cause not attributed | 1,127 ms |
| Warm launch, full re-hash of 6,439 files (ruling 2) | median 116.1 ms (112.3 to 120.5, 7 runs); 96 to 127 ms across later runs | 190 ms (doctor full re-hash) |
| Warm launch, the rest (manifest digest, parse, config, env, links) | 2.9 ms median for `pi path`; 11 to 17 ms for `lead` (it also writes the solo role, rules and pi config), under the 60 ms bound | 43.5 ms (executables only, earlier rule) |
| Cold start to first token, Ollama qwen2.5:32b, 1 sample | 12.84 s with the model not loaded (Ollama load included); the S3 figure had the model preloaded | 2.38 s |
| Warm start to first token, 3 runs | 0.367 to 0.386 s (answer `42` each time) | 0.248 s |

The measured prompts ran through `amq-squad lead` with the package loaded (role message, hooks, tools); the payload then differed from the committed one only in `pi/package.json` (the test script line).

Live checks on the decided build (the final package, `ollama/qwen2.5:32b`, spike HOME so the solo state and pi agent dir stay under `~/.amq-squad/spike/4-1/home`):

- Acceptance 1: `amq-squad lead -- --mode json -p ...` with `PATH=/usr/bin:/bin` answers from the unpacked copy (`42`; and a bash call `echo hello-from-bash` that ran, after the role message was delivered as the first message).
- Acceptance 2: a changed byte in `node/bin/node`, a changed byte in a bundle chunk (`dist/bundle/chunks/anthropic-messages-*.js`), and `dist/` moved away and symlinked back each make `pi path` refuse with exit 2 and the rejecting reason; restored, it launches again.
- Acceptance 3 in pi: `touch <marker>` through the bash tool is blocked and the marker is absent when guard times out (`AMQ_SQUAD_HOOK_TIMEOUT_MS=1`), when guard exits 1 (an `AM_ROOT` that is not a clean path), and when the binary is gone (the copy the lead was launched from deleted 1.5 s after start: `spawn ... ENOENT`). The model read each block reason and did not retry.
- Evidence: the tool result reached the ledger (`<solo root>/extensions/amq-squad/ledger/lead.jsonl`, `event: tool_result`, `outcome: ok`).

Rows: 13 new rows (8 under `internal/piruntime/`, `internal/cli/{pi,pi_lead,pi_exec_unix,pi_exec_other,doctor_pi}.go`), fate new. `command_registry.go` 88 -> 92, `cli.go` 194 -> 195, `doctor.go` 866 -> 871 (current_lines updated).

Behavior changes:
1. New verbs `lead` and `pi` (run, verify, path).
2. `doctor` has a `pi runtime` and a `pi runtime versions` row and a `--fix` flag; `--fix` is the only doctor path that writes (it removes runtime directories under `~/.amq-squad/runtime/`).
3. Bare `amq-squad` still prints the usage; plan 3.1's "you type amq-squad" first run is `amq-squad lead` for now (open ruling 1).

Deviations:
- The warm launch re-hashes the whole tree (ruling 2), not the executables only as in S3 and plan 4.1 revision 2.6.
- The pi agent dir defaults are forced, not "unless the caller set them" as in the spike: a caller cannot turn telemetry on or move the agent dir.
- No TypeScript compiler: the package is erasable TypeScript checked by `stripTypeScriptTypes` and run by Node 22.21.1's type stripping in tests and by pi's jiti at run time (a `tsc` would be a download outside the S3 pins).
- The vendored reference bridge creates `<root>/agents/<handle>/` at session start (reference behavior), so an AMQ identity without a mailbox gains one under pi; guard then sees a mailbox. Noted because it made a first exit-1 live probe allow (the probe was redone with an identity guard rejects).

Open rulings:
1. Bare `amq-squad`: plan 3.1 says typing `amq-squad` starts the lead in solo mode. Make the bare invocation start `lead` when the payload is embedded and stdin is a terminal (usage otherwise), or keep `amq-squad lead`?
2. The default lead model before the registry chooses: `ollama/qwen3.6:27b` (S5 verified at N=3).
3. A pi package lint with `tsc --noEmit` needs a TypeScript download (not in the S3 pins). Allow one pinned devDependency, or keep the strip-types lint until phase 5's package CI?

Lead: commit 1 accepted as the halfway checkpoint (gate re-run on a detached worktree, race clean on piruntime and pipack, no payload in the tree, secret scan clean). Rulings 3 to 5 below close the three open rulings.

3. (halfway) Bare `amq-squad`: with an embedded payload and a TTY on stdin it starts `lead`; without a TTY or without the payload it prints the help with a one-line hint. `amq-squad lead` stays the explicit form; one test per branch.
4. (halfway) The default lead model `ollama/qwen3.6:27b` is the fallback only: one named constant; once 2b-3 is on v3 the registry's model row decides and the constant applies when the registry has no usable provider (a rebase onto 2b-3 before the end checkpoint).
5. (halfway) No TypeScript pin: keep `stripTypeScriptTypes` and Node type stripping. The `tsc --noEmit` gap is a phase 5 item (package CI). The S3 pins are the only network-sourced inputs this phase.
6. (halfway, process) Each gate run's full go test and package test output is kept under `docs/v3-spike/4-1-evidence/` (`gate-<commit>-*.txt`). No gate run while a live seat or lead session is on the host; the gate script refuses to run when a pi process is up.

## Commit 2: bare invocation, acceptance 4 to 6 live, R12 at N=5, the session layout

Built:
- Bare `amq-squad` (ruling 3): `runBare` in `internal/cli/pi_lead.go`; the TTY check is a seam (`stdinIsTerminal`). `cli.go` calls it.
- `defaultLeadModel` carries the ruling 4 comment.
- `docs/v3-spike/4-1-evidence/`: the live runs below and the gate outputs of this commit.

Tests: `internal/cli/pi_bare_test.go`: payload and TTY start the solo lead; payload without a TTY prints the help and the hint and execs nothing; a TTY without a payload prints the help and the "no built-in pi" hint. `TestRunBarePrintsUsage` is unchanged (no payload in the test build).

Live acceptance (decided build, final package; raw events in `4-1-evidence/`):
- Acceptance 4, native tools (`a4-tools.jsonl`): an AMQ co-op root (`amq coop init --agents pi-1,pi-lead,user`, amq 0.107.0), one todo from pi-lead seeded with `amq send`; `amq-squad pi run --model ollama/qwen2.5:32b` as pi-1 with "Check your AMQ mail, do what the message asks, then send pi-lead one status message": `amq_drain`, `amq_reply` (body `pong`, the right message id), `amq_send` (status); no bash call. pi-lead's inbox holds both messages (`amq list --me pi-lead --new`). qwen2.5 sent one malformed parallel `amq_send` first (`{"ok":true}`), which the tool schema rejected; the model reissued it correctly (the S5 malformed-first-call pattern, now caught by the schema instead of an argument guess).
- Acceptance 5, role (`a5-role10-qwen36.jsonl`): 10 of 10 with `ollama/qwen3.6:27b`, the model that misread AMQ in S5 attempt 0. Each fresh session got the role message, and each answer named the seat (`pi-1`, worker), the lead (`pi-lead`) and "Agent Message Queue"; none said "Advanced Message Queuing"; no tool call. 3.4 to 4.6 s per run warm (12.6 s for the first, model load).
- Acceptance 6, model restriction (`a6-models.jsonl`): with `GEMINI_API_KEY` and `OPENAI_API_KEY` set in the parent environment, `amq-squad lead --model ollama/qwen3.6:27b -- --list-models` lists one model, `ollama qwen3.6:27b`. Control: the same runtime's node and pi entry started directly with `GEMINI_API_KEY` set (no launcher, no scrub) lists 23 models, all `google` (the S5 count).
- R12 at N=5 through the kept path (`r12-qwen36.jsonl`): `spike/piprobe/probe.mjs` (predicate v2, the reference that 2b-3's `registry.Evaluate` ports) with `--pi` set to a wrapper around `amq-squad pi run --no-package --seat probe --model ollama/qwen3.6:27b`: 5 of 5 pass, zero false successes, zero tool errors (read, edit, bash every run), 11.4 to 24.7 s per run. qwen3.6:27b is verified at N=5 (R12, amendment 15). After 2b-3 merges, `doctor --probe` runs the same with `registry.ProbeConfig{Pi: runtime.PiArgv()}`.
- The session layout (pins `piruntime.SessionDir`): `pi run -- --session-id amqn-layout-1` in `/Users/omri/.amq-squad/spike/4-1/amqproj` wrote `<agent-dir>/sessions/--Users-omri-.amq-squad-spike-4-1-amqproj--/2026-10-24T09-59-13-246Z_amqn-layout-1.jsonl` and printed "No project session found with id 'amqn-layout-1'; creating a new session with that id" (create-if-missing). The timestamp holds no underscore.
- The resume refusal live (`resume-lost.err`): with tool results in pi-1's ledger, `pi run --resume -- --session-id amqn-lost-1` exits 2 with `pi_resume_context_lost` before pi starts; `--session-id amqn-layout-1` (file present) resumes, the model recalls its earlier reply, and no second role message is sent.

Behavior changes: bare `amq-squad` starts the lead in a terminal when the build embeds pi (help otherwise, with a hint).

Lead: commit 2 checked (build, vet, manifest-check --final, cli and piruntime tests, evidence present, fallback constant).

7. (after commit 2) The R19 deviation is accepted on three conditions: the stand-in harness calls the real `scheduler.Tick` over the real task store, so only the doorbell is simulated; this file names exactly which components are substituted and that acceptance 8 is re-run against the real loop once 2b-4 is on v3 (a 2b-4 acceptance row, added by the lead); the loop output is kept under `4-1-evidence/`.
8. (same) Acceptance 10: if the installed amq lacks the pi adapter of PR #1188, record it as blocked on the next AMQ release with the exact error; no vendoring or patching around it.

## Commit 3: the R19 loop test, acceptance 10 status

9. (during commit 3) Correction: the scheduler process in the binary belongs to 2b-2 milestone B (briefs ruling 15), not 2b-4. The R19 re-run against the real process is a milestone B acceptance row: **re-run owned by 2b-2 milestone B**.

### Acceptance 8 (R19): the lead does not start coding, 2 of 2

Harness: `spike/pi-r19/main.go` (committed, part of the root module so it can import `internal/`). Real components: `scheduler.New` and `Tick` (internal/scheduler, 2b-1) over `task.Open` on the squad's AMQ root (internal/task); the lead (`amq-squad lead --team`, embedded pi, the package, `ollama/qwen3.6:27b`) and the worker (`amq-squad pi run`, same model) as real sessions with the real hooks, native tools and task verbs; AMQ 0.107.0 mail (`amq coop init --agents pi-1,pi-lead`). Substituted (lead ruling 7), because their briefs are not on v3: the scheduler loop itself (the harness calls `Tick` in a loop; the process is 2b-2 milestone B's, ruling 9); `Router` (2b-4's routing table: every task to the one pool `pi:qwen3.6:27b:medium`); `SeatLauncher` (2b-2's launch: the worker is announced with `SeatUp` under the scheduler's request id, and its process is one `pi run` per turn on a persistent `--session-id`); `Doorbell` (AMQ wake: a ring is the worker's next turn, prompt "AMQ doorbell: the scheduler leased you a task. Act on it."; the lead is woken with "AMQ doorbell: new mail arrived" when no task is open, or with the refusal text when the commit step blocks a task); `Committer` (2b-2 milestone B's harness commit step: `git status --porcelain --untracked-files=all`; every changed file must be inside the task's declared set, else `outside_declared_files`; then `git add` and `git commit` as the harness). The scheduler, store, sessions, verbs and mail are not faked. **Acceptance 8 is re-run against the real scheduler process once 2b-2 milestone B is on v3 (re-run owned by 2b-2 milestone B).**

Pass rule (per run): the lead makes no write or edit call and no bash call that writes a file (`touch`, `tee`, `cp`, `mv`, `rm`, `sed -i`, `gofmt -w`, redirection, heredoc); its first tool call is an AMQ or task tool; at least 3 tasks carry a class and declared files; the lead's final line is `CONVERGED`; an independent `go test -count=1 -v ./...` passes with TestMax, TestReverse and TestIsEven.

| Run | Role and package | Result | Evidence |
| --- | --- | --- | --- |
| 0 | team role of commit 1 | FAIL (stopped by hand): the lead's first call was `amq_task_list`, then three `amq_task_add` (class implement, declared files, priority) and no coding, but it then polled `amq_drain` and `amq_task_list` inside the same turn and never ended it (R19: the lead does not poll). Fix: the team role says to end the turn after dispatch and never call `amq_drain` or `amq_task_list` to wait; a doorbell wakes it. The harness bounds a turn at 15 minutes. | `4-1-evidence/r19/run0-polling/` |
| 2 | role fixed | FAIL (worker scope, harness stall): the lead dispatched three classed tasks and ended its turn; the scheduler requested the seat, saw `seat_up` and dispatched t1; the worker put `Max` into `calc.go` and `calc_test.go` instead of `max.go`/`max_test.go`; the commit step refused it (`commit_refused`, `outside_declared_files`: the amendment 21 enforcement working); the harness had no path to tell the lead, so the loop stalled. Fixes: (a) the package blocks a `write`/`edit` outside the current task's declared files in session (a hint with the files named; the commit step stays the enforcement point); (b) the lead has `amq_task_reset` (the kept `task reset` verb) and the team role says to reset a task refused for its files; (c) the harness routes a refusal to the lead and discards the seat's diff when the lead resets the task. | `4-1-evidence/r19/run2/` |
| 3 | fixed | PASS in 140 s: lead calls `amq_task_list`, `bash` (`ls -la && cat go.mod`), 3 x `amq_task_add`, then (doorbell) `amq_drain`, `bash` (`go test ./... -v`, twice, `pwd && ls`), 3 x `read`; tasks t1 Max, t2 Reverse, t3 IsEven, all `implement`, declared files, `completed`; scheduler decisions `seat_requested, seat_up, dispatched, completed` x 3; harness commits `t1: ...`, `t2: ...`, `t3: ...`; the scope hint blocked 3 out-of-scope writes by the worker, which then used its declared files; final line `CONVERGED`; independent go test passes all three. | `4-1-evidence/r19/run3/` |
| 4 | fixed | PASS in 201 s: the same shape (`amq_task_list`, `bash ls -la`, 2 x `read`, 3 x `amq_task_add`, then `amq_drain` and `go test`); three `implement` tasks completed; no scope block needed; `CONVERGED`; all three tests pass. | `4-1-evidence/r19/run4/` |

2 of 2 consecutive runs pass with the final role and package (as in S5). The lead's bash calls were `ls`, `cat`, `pwd` and `go test` only.

### Acceptance 10: blocked on the next AMQ release

The installed amq is 0.107.0 (Homebrew), the newest release and the newest module in the cache. Its launch adapters are `claude`, `codex`, `cursor-agent`, `grok` and `acp` (`internal/launch/adapter.go`, `adapter_acp.go` of the v0.107.0 module); there is no pi adapter. `amq launch` needs a committed launch config (`committed launch config not found; run 'amq setup'`, exit 3), and the non-writing setup preview refuses pi:

```
$ amq setup --agents pi --default-session collab --launcher-preference tmux --preview --json
agent "pi" is not available through a supported adapter probe
exit=2
```

So acceptance 10 is blocked on the first AMQ release that carries PR #1188. Nothing is vendored or patched. What is ready for it on this branch: the AMQ pi adapter will start `<wrapper> -- pi --session-id <launch nonce>` (plan 4.1), seat-exec (2b-2) resolves `pi`; the hand-over is `piruntime.Ensure` + `piruntime.Prepare{Seat, Args: argv[1:], Package: true}` in place of the PATH lookup (R2: never a pi from PATH), and `piruntime.CheckResume(LedgerPath(root, seat), AgentDirFor(home, seat), cwd, argv)` in place of seat-exec's provisional `CheckPiResume` when the launch record says resume. `amq-squad pi run --resume -- --session-id <id>` is that path today and is tested live (commit 2).

### Built in commit 3

- `pi/extensions/lib/hooks.ts`: `outsideDeclared` and `declaredMatch` (exact path, a directory or `/**` tree, a one-segment glob); `amq-squad.ts` caches the task from each turn's `context` and blocks a `write` or `edit` outside its declared files before guard runs (evidence line `type: scope_block`). Bash writes are left to guard and the commit step.
- `pi/extensions/lib/tools.ts`: `amq_task_reset` for the lead (`task reset ID --me H`).
- `internal/cli/pi_lead_team.md`: end the turn after dispatch, never poll `amq_drain` or `amq_task_list` inside it; reset a task the commit step refused for its files.
- `spike/pi-r19/main.go`: the R19 harness above.

Tests: `pi/test/scope.test.ts` (in-scope and out-of-scope writes and edits, `..` normalization, a glob and a tree, bash untouched, no task or a blocked task or no declared files means no scope; the reset tool's argv and that only the lead has it). The package now has 30 tests.

Behavior changes: a pi seat's `write`/`edit` outside its task's declared files is blocked in session with the files named; the team lead has `amq_task_reset`; the team-lead role text changed (steps 4 and 5).

## End checkpoint

Lead (after commit 3): name the run-2 fixes precisely, since they change the lead's contract, and say what carries the refusal to the lead. Done here and in `pi/README.md`:

1. **The in-session scope block is a hint only.** `outsideDeclared` blocks a pi `write` or `edit` outside the current task's declared files so the seat corrects itself early. The harness commit step stays the enforcement point (amendment 21): it refuses any diff with a file outside the declared set, whatever happened in session (bash writes are not checked in session at all).
2. **`amq_task_reset` maps to one store verb: `Reset`.** The tool runs `amq-squad task reset ID --me H`, which is `task.Store.Reset`: a `blocked` task (the commit step's `RefuseCommit` put it there with `block_reason`, `block_detail` and `block_route: lead`) goes back to `pending`, its report is discarded, and the scheduler leases it again. It does not widen. A widening is `Store.Widen` (a recorded lead decision inside the grant's limits; a widening beyond a grant limit is refused by the core and escalates to an owner-signed gate, never decided by the model). There is no widen tool in the package yet, so the evidence ledger can only show a reset; when milestone B adds the widening path it gets its own tool (`amq_task_widen`), never a mode of reset.
3. **The refusal route.** The truth is the task store record that `RefuseCommit` writes (state `blocked`, `block_reason` such as `outside_declared_files`, `block_detail` with the file, `block_route: lead`); `amq_task_list` shows it. The lead is woken by a doorbell: an AMQ message to the lead's handle naming the task, the reason code and the refused file; it is a hint, and a lost doorbell loses nothing because the store keeps the block. In the 4-1 harness the doorbell is simulated as the lead's next turn with exactly that text ("the harness commit step refused task t1 ...: outside_declared_files (calc.go)"). 2b-2 milestone B implements the same route: store record first, then an AMQ message to the lead's handle.

Rebase: `v3-4-1` rebased onto `v3` at 6acb80f0 (docs-only commits since f0129ebf: amendment 25, briefs rulings 15 to 17); no conflict. 2b-3 is not on v3 yet, so the registry's model row does not drive the lead's model yet; the fallback constant stays the only source (ruling 4), and the wiring is the follow-up below.

### Acceptance, all rows

| # | Row | Status | Evidence |
| --- | --- | --- | --- |
| 1 | Criterion 3 from the v3 binary, no pi or Node on PATH | met | commit 1 live; final build `42` cold and warm (`measure-final.json`) |
| 2 | Integrity: a changed byte, a symlink on the entry path refuse; warm overhead | met | unit tamper table; live tamper (commit 1). Three numbers, medians of five cold starts on the final build (fresh process, runtime unpacked, model loaded; `cold5-fixed.json`): **full re-hash 117.6 ms** (amendment 25: every launch re-hashes the whole tree; 115 to 125 ms in every run, first or later, with and without the package); **node start to pi ready 297.7 ms** (147.6 ms with the jiti cache on, `cold5.json`: the price of the cache-off fix); **spawn to first token 518.5 ms**. The 60 ms line applies to the launch overhead outside the runtime start (manifest digest, parse, config, environment, links): 2.8 ms (`pi path`) to 12 ms (`lead`), which meets it. The earlier single 7.37 s cold first token was Ollama's first-request prefill of an uncached prompt plus the 1.05 s unpack, not the re-hash |
| 3 | Guard fails closed: exit 1, timeout, missing binary block in pi | met | package tests; live `a3-*.jsonl` |
| 4 | Native tools: send, drain, reply with no bash `amq` | met | `a4-tools.jsonl` |
| 5 | Role: 10 runs acknowledge the role and name AMQ correctly | met, 10/10 | `a5-role10-qwen36.jsonl` |
| 6 | Model restriction with `GEMINI_API_KEY` set | met | `a6-models.jsonl` (1 model; control 23 google models) |
| 7 | R7: first run in a fresh git repo, clean status, writes only under `~/.amq-squad/` | met | `TestLeadSoloFirstRunWritesOnlyUnderHome`; the live solo state lives under `<HOME>/.amq-squad/solo/` |
| 8 | R19 loop: no coding, decompose, label, converge (2 of 2) | met with the named stand-ins; re-run owned by 2b-2 milestone B. Under amendment 26: the lead made no bash call because it has no Bash tool; the independent go test ran in the harness | `r19/run3`, `r19/run4` (runs 0 and 2 kept); `r19/run5` with the amendment 26 tool set |
| 9 | Hold seam: `before_agent_start` injection, parallel holds, local confirm racing a remote answer; retry cap | met | `hold.test.ts`, `extension.test.ts` (bridge revision 4 end to end) |
| 10 | pi launch through `amq launch` on the AMQ release after v0.107.0 | blocked on the next AMQ release | `agent "pi" is not available through a supported adapter probe`, exit 2 (amq 0.107.0) |

R12 at N=5 on a real local model through the kept path: qwen3.6:27b 5/5, zero false successes (`r12-qwen36.jsonl`).

### Final measurements (rebased build, payload 1a4bd2eeec38; `measure-final.json`)

| Measurement | 4-1 final | S3 decided build |
| --- | --- | --- |
| Binary | 68.18 MB (68,175,122 bytes) | 59.27 MB (launcher only) |
| Payload | 55.65 MB | 54.52 MB |
| Unpacked | 179.33 MB, 6,439 files in the manifest (6,441 on disk) | 179.25 MB, 6,432 files |
| Cold install, real `~/.amq-squad/runtime/`, 3 runs | 1,041, 1,058, 1,044 ms (earlier outliers at 10.8 and 18.6 s did not recur) | 1,127 ms |
| Warm: full re-hash, 7 runs | median 95.6 ms (92.4 to 96.9) | 190 ms (doctor full re-hash) |
| Warm: the rest | 2.8 ms (`pi path`), 11.9 to 13.3 ms (`lead`) | 43.5 ms (executables only) |
| Cold start to first token, qwen2.5:32b preloaded, 1 sample | 7.37 s (includes the 1.05 s unpack, the first exec of the new executables, loading the package, and the first prefill of the larger system prompt; not split further) | 2.38 s (no package, `--no-tools`) |
| Warm start to first token, 3 runs | 0.363 to 0.375 s, `42` each | 0.248 s |

### Follow-ups once the dependent branches are on v3

- 2b-2 (seat-exec): for runtime `pi`, replace the PATH lookup with `piruntime.Load` + `Ensure` + `Prepare{Seat, Args: argv[1:], Package: true}`, and `CheckPiResume` with `piruntime.CheckResume` (the pinned layout). Milestone B: the scheduler process, the commit step and its refusal route as above, the R19 re-run.
- 2b-3 (registry): `registry.PiCommand` reads the same manifest fields; `doctor --probe` passes `Runtime.PiArgv()` as `ProbeConfig.Pi`; `registry.RehashRuntime` and `piruntime.Verify` check the same tree (one should call the other); the lead's model comes from the registry's model row, with `defaultLeadModel` only when no provider is usable.
- Phase 5: `tsc --noEmit` in the package CI; release CI for fetch, pack and sign (S3 open questions 2 and 3); the signed and notarized launcher.

### Open rulings at the end

None from this brief beyond the follow-ups above.

## After the end checkpoint: reviewer rulings and the cold-start split

Reviewer rulings on the deviations: (a) the forced pi environment defaults stay forced (a caller override would let a seat choose its own runtime settings, which R16's model restriction forbids); (b) the bridge creating `<root>/agents/<handle>/` at session start is accepted as the reference bridge's behavior; (c) the single cold first-token sample of 7.37 s is not accepted as a number: five cold starts, split into the full re-hash, node start to pi ready, and the model's first token.

Method (`~/.amq-squad/spike/4-1/cold5.py`, raw rows in `4-1-evidence/cold5.json`): each run is a fresh `amq-squad` process; the runtime is already unpacked; `qwen2.5:32b` is already loaded in Ollama (a one-token request before the series); no other session on the host. `rehash_ms` and the launcher's rest come from the launcher's own timing line; node start to pi ready is from the end of the launch to pi's first JSON event (`session`, after Node started and pi and its extensions loaded); the model's first token is from pi's `agent_start` to the first visible `text_delta`. Two series: `lead` (the decided path, `amq-squad lead`, the package loaded) and `control` (`amq-squad pi run --no-package` with S3's `--no-tools --no-extensions --no-skills --no-prompt-templates --no-themes --no-context-files --no-mcp`), the same prompt, `--no-session --thinking off`.

| Series | Run | Re-hash ms | Launch rest ms | Node start to pi ready ms | pi ready to agent_start ms | Model first token ms | Spawn to first token ms |
| --- | --- | --- | --- | --- | --- | --- | --- |
| lead | 1 | 124.9 | 18.0 | 839.1 | 7.8 | 5011.5 | 6001.3 |
| lead | 2 | 114.6 | 10.9 | 147.6 | 7.5 | 81.9 | 362.5 |
| lead | 3 | 115.1 | 11.6 | 146.6 | 7.7 | 81.4 | 362.4 |
| lead | 4 | 119.7 | 12.5 | 148.4 | 7.4 | 81.6 | 369.6 |
| lead | 5 | 116.3 | 11.3 | 147.1 | 7.4 | 79.9 | 362.0 |
| **lead median** | | **116.3** | **11.6** | **147.6** | **7.5** | **81.6** | **362.5** |
| control | 1 | 115.4 | 12.3 | 132.6 | 0.9 | 1085.2 | 1346.4 |
| control | 2 | 119.6 | 12.2 | 134.5 | 0.9 | 70.5 | 337.7 |
| control | 3 | 118.5 | 12.1 | 128.3 | 1.0 | 72.7 | 332.6 |
| control | 4 | 117.0 | 12.3 | 129.1 | 0.9 | 70.0 | 329.2 |
| control | 5 | 121.3 | 12.0 | 129.7 | 0.9 | 69.2 | 333.1 |
| **control median** | | **118.5** | **12.2** | **129.7** | **0.9** | **70.5** | **333.1** |

Every run answered `42`, exit 0.

Where the gap to S3's 2.38 s is:
- Not in the re-hash: 115 to 125 ms in every run, first or later, with and without the package.
- In the model, on the first request with a new prompt: run 1 of each series pays Ollama's prefill of a prompt it has not cached (5,011 ms with the package, whose system prompt carries the role and context sections; 1,085 ms for the control's short prompt); later runs reuse the cached prefix (70 to 82 ms). S3's 2.38 s was one sample with the S3 prompt; the earlier 7.37 s here was a first run, so it carried this prefill, plus the 1.05 s unpack (that run also started from a removed runtime).
- In node start, on the first run with the package only: 839 ms against 147 ms afterwards and 133 ms for the control's first run. The extra ~690 ms is pi's jiti transpiling the package's TypeScript on first load. The transpile is cached, and that cache is the finding below.
- Steady state: 362.5 ms median to the first token with the package against 333.1 ms without it; the package costs about 18 ms at node start and about 7 ms for the context hook before the request.

### Finding (stopped before fixing, per the ruling): pi's jiti cache sits outside the verified runtime

The first-run node-start cost comes from jiti's transpile cache: pi 1.3.0 writes the transpiled package to `/tmp/jiti/extensions-amq-squad.<hash>.mjs` (two entries on this host, from 12:49 and 13:27, owner omri; `/tmp/jiti` is `drwxr-xr-x`, the files `-rw-r--r--`) and loads it from there on later starts. Two consequences:
1. R7: a lead's first run writes outside `~/.amq-squad/` (into the world-shared `/tmp`). `TestLeadSoloFirstRunWritesOnlyUnderHome` cannot see it, because the test execs no real pi.
2. Integrity: the launch re-hash verifies the package source under the runtime, but pi executes the cached `.mjs` in `/tmp/jiti`, which nothing verifies. If the cache key covers the source content (the names carry a hash), a planted or edited file in `/tmp/jiti` — or a `/tmp/jiti` created first by another local user — would run in place of the verified package in every lead and pi seat, which bypasses the trust-root rule.

Not fixed in that commit (bf1ee5ec); fixed in the next one, see "Fix: jiti cache off, the seat's own TMPDIR, the live R7 test". Candidate fixes as first proposed: point jiti's cache into the seat's agent dir or disable it for the package (jiti honors environment switches such as `JITI_FS_CACHE` / `JITI_CACHE`; to be checked against the pinned 1.3.0 bundle), or ship the package pre-transpiled as `.mjs` inside the payload so the verified bytes are the bytes pi runs (the transpile moving into `build-pi-runtime.sh`); and a live R7 test that runs the real lead under a fresh `HOME` and `TMPDIR` and lists every file written outside `~/.amq-squad/`.

### Fix: jiti cache off, the seat's own TMPDIR, the live R7 test

Lead rulings on the finding: (10) step 1, a pre-transpiled `.mjs`, is dropped: pi 1.3.0 still routes an `.mjs` through jiti (evidence below), so it would only move cost; with the cache off, jiti transforms in memory from the bytes the re-hash verified on every start, which is the property needed. (11) `JITI_FS_CACHE=false` is forced for every lead and pi seat launch, in the forced set of ruling (a), with a test that it is present and cannot be overridden. (12) The launch sets `TMPDIR` for the lead and every pi seat to a 0700 directory under the seat's own extension directory, created by us, so any other temp writer in pi or Node lands where the same-UID threat model applies and doctor can see it. (13) A live R7 test: fresh `HOME` and `TMPDIR`, one full lead turn, then nothing outside `~/.amq-squad`, no jiti cache, and no new file or changed mtime anywhere under the runtime tree. (14) Re-measure the five cold starts; "node start to pi ready" is expected to rise toward the first-run 839 ms, and the plan records the new median as the price of the fix (above 1 s the lead decides whether a pre-transpile pass is worth it for cost).

Evidence on pi 1.3.0 and jiti, from the pinned tree (`pi/package-lock.json`; read in `build/pi/npm/node_modules/`), so nobody re-derives it:
- `@earendil-works/pi-coding-agent/dist/core/extensions/loader.js` `loadExtensionModule` always calls `createJiti(...)` and `jiti.import(extensionPath, { default: true })`; there is no native `import()` path for any extension type.
- `usesEmbeddedModules = isBunBinary || isNodeSeaBinary || isBundledNode` (bundle chunk `chunk-66G7ITPS.js`); under the bundled Node distribution (our `dist/bundle/cli.js`) the options are `{ virtualModules, tryNative: false }`, so jiti transforms every extension, `.mjs` included (ESM to CJS), and does not try Node's own loader first.
- The bundle embeds jiti 2.7.0 (`dist/bundle/chunks/jiti-loader-*.js`; `node_modules/jiti` 2.7.0). Its options come from the environment: `fsCache: _booleanEnv("JITI_FS_CACHE", _booleanEnv("JITI_CACHE", true))`; `_booleanEnv` JSON-parses the value, so `JITI_FS_CACHE=false` turns the file cache off (`JITI_FS_CACHE` wins over `JITI_CACHE`).
- With `fsCache: true` the cache directory is `<module dir>/../node_modules/.cache/jiti` when that directory exists, else `os.tmpdir()/jiti` (which is how the entries reached `/tmp/jiti`; `JITI_RESPECT_TMPDIR_ENV` only matters when `TMPDIR` equals the cwd).

Built:
- `internal/piruntime/launch.go`: `Forced(agentDir, tmp)` is the forced set: `PI_OFFLINE=1`, `PI_SKIP_VERSION_CHECK=1`, `PI_TELEMETRY=0`, `PI_CODING_AGENT_DIR`, `JITI_FS_CACHE=false`, `TMPDIR`. `Prepare` applies it after the caller's overlay, so neither the parent environment nor an overlay changes it (before this commit an overlay could have replaced a `PI_*` value; the CLI never did, but nothing stopped it). Every `JITI_*` variable from the parent is dropped. `Spec.TmpDir` is the seat's TMPDIR (default `<agent-dir>/tmp`), created 0700 by `privateDir`, which refuses a symlink or a non-directory in its place and narrows an existing directory to 0700.
- `internal/cli/pi.go`: with an AMQ root the seat's TMPDIR is `<AM_ROOT>/agents/<seat>/extensions/io.github.omriariav.amq-squad/tmp` (the solo lead's is under `~/.amq-squad/solo/<dir>-<digest>/root/...`); without one (a probe) it is `<agent-dir>/tmp`.

Tests:
- `internal/piruntime/forced_test.go`: the parent sets `JITI_FS_CACHE=true`, `JITI_CACHE`, `JITI_TRY_NATIVE`, `JITI_ALIAS`, `TMPDIR=/tmp`, `PI_TELEMETRY=1`, `PI_OFFLINE=0` and the overlay sets `JITI_FS_CACHE=true`, `TMPDIR=/tmp`, `PI_TELEMETRY=1`, `PI_CODING_AGENT_DIR=/elsewhere`: the environment holds each forced value exactly once, with the forced value, and no other `JITI_*`; the overlay's other keys pass; TMPDIR is created 0700; the default TMPDIR is under the agent dir; a symlinked TMPDIR is refused.
- `internal/cli/pi_test.go` `TestLeadSoloFirstRunWritesOnlyUnderHome`: the lead's TMPDIR is the seat's extension directory under the solo root, mode 0700, and `JITI_FS_CACHE=false` is set.
- `internal/cli/pi_live_test.go` (build tag `amqpi`; skipped unless `AMQ_SQUAD_LIVE_BIN` names a payload build; `AMQ_SQUAD_LIVE_MODEL` defaults to `ollama/qwen2.5:32b`): `TestLiveR7FirstRun`. Fresh `HOME`, `TMPDIR` and a git repository with one commit; `amq-squad pi path` unpacks the runtime; then one full `amq-squad lead` turn that makes a bash call (`echo r7-live`), so context, guard and evidence ingest all run. Asserts: `git status --porcelain --untracked-files=all --ignored` empty and the repository tree unchanged; every file under `HOME` is under `~/.amq-squad/`; the caller's `TMPDIR` is empty; no `/tmp/jiti` entry written during the turn; no new file and no changed mtime (files and directories) anywhere under the runtime tree. Result: PASS in 11.7 s (`4-1-evidence/live-r7.txt`). Run with `AMQ_SQUAD_LIVE_BIN=$PWD/build/pi/amq-squad go test -count=1 -tags amqpi -run TestLiveR7FirstRun ./internal/cli/`; it is a live session, so it is run outside the gate (process ruling 6).
- After the fix `/tmp/jiti` holds no entry newer than 13:27, the last pre-fix run. The older entries there (pi's transpiles of this package from 12:37, 12:49 and 13:27, and `probe-ext-s3-probe.*.mjs` from 2026-10-21, S3) are outside the write areas of this brief and were left in place: they are for the owner to remove.

Cold starts after the fix (same method as above; `4-1-evidence/cold5-fixed.json`; model loaded, runtime unpacked, fresh process each run):

| Series | Run | Re-hash ms | Launch rest ms | Node start to pi ready ms | pi ready to agent_start ms | Model first token ms | Spawn to first token ms |
| --- | --- | --- | --- | --- | --- | --- | --- |
| lead | 1 | 117.6 | 11.5 | 404.3 | 7.6 | 5224.9 | 5765.9 |
| lead | 2 | 120.7 | 13.3 | 297.7 | 8.1 | 81.1 | 520.9 |
| lead | 3 | 117.6 | 12.0 | 299.1 | 8.0 | 81.8 | 518.5 |
| lead | 4 | 115.8 | 11.9 | 283.3 | 8.4 | 84.2 | 503.6 |
| lead | 5 | 118.0 | 12.4 | 289.4 | 8.2 | 80.5 | 508.5 |
| **lead median** | | **117.6** | **12.0** | **297.7** | **8.1** | **81.8** | **518.5** |
| control | 1 | 119.6 | 13.2 | 181.7 | 1.0 | 1197.9 | 1513.4 |
| control | 2 | 118.0 | 12.9 | 140.4 | 1.0 | 71.7 | 344.0 |
| control | 3 | 121.7 | 12.0 | 141.9 | 0.9 | 69.8 | 346.3 |
| control | 4 | 119.2 | 11.1 | 136.1 | 0.9 | 69.8 | 337.1 |
| control | 5 | 116.6 | 12.1 | 140.4 | 0.9 | 69.1 | 339.1 |
| **control median** | | **119.2** | **12.1** | **140.4** | **0.9** | **69.8** | **344.0** |

The price of the fix: with the package, node start to pi ready is 297.7 ms median against 147.6 ms with the cache, about 150 ms per start for jiti's in-memory transform; the first run is 404 ms (839 ms before, which also wrote the cache). Spawn to first token is 518.5 ms median (362.5 ms before). Below the 1 s threshold, so no pre-transpile pass is proposed. The re-hash and the model are unchanged (117.6 ms and 81.8 ms medians; the first run of each series again pays Ollama's prefill of an uncached prompt).

Behavior changes: every lead and pi seat runs with `JITI_FS_CACHE=false` and its own 0700 TMPDIR; parent `JITI_*` variables no longer reach pi; a caller overlay can no longer change a forced variable.

Rows: `internal/piruntime/launch.go` and `internal/cli/pi.go` current_lines updated (fate new).

## Codex review on b3dba8eb: five items and amendment 26

The Codex pass on b3dba8eb (the jiti cache already fixed in 6a46a0fd) returned no-go with three P1 and two P2 items, plus amendment 26 (binding). One fix commit on top of 116605c2, one test row per item that fails when reverted.

### Item 2 design (written and accepted by the lead before the code)

The /dev/fd probe (`4-1-evidence/devfd-probe.go.txt`): open `/bin/echo` `O_RDONLY`, clear close-on-exec, `execve("/dev/fd/3", ...)`: **`permission denied` (EACCES) on macOS 25.6.0**, so node cannot be exec'd through the held descriptor; the fallback the lead named applies.

(a) Launcher. Verification runs through fsanchor descriptors anchored at HOME (item 3) and records the identity (device, inode, size, mtime, from the hashed descriptor) of every file. Right before `execve`, after `Prepare`'s config writes, `Runtime.CheckLaunch` re-opens the launch set through descriptors from the same anchor (node, the entry, every file in the bundle directory, the package's files outside its `node_modules`, fd and rg) and refuses when any file is not the one hashed (another inode, size or mtime): a rename or a rewrite between `Ensure` and exec. The window left on the launcher side is from that check to `execve`.

(b) In process. The forced environment carries `AMQ_SQUAD_RUNTIME_DIR` and `AMQ_SQUAD_RUNTIME_MANIFEST_SHA256` (the embedded digest). The package's first statement, before it registers any tool or hook, reads the on-disk manifest, requires its SHA-256 to equal the variable, and re-hashes the pi bundle directory (the entry and every chunk) and the package's own files against it: no file missing, none extra, none a symlink. On a mismatch it prints `amq-squad pi refused (runtime_mismatch_in_process): <reason>`, writes one evidence line (`type: runtime_mismatch_in_process`), and exits 70. `piruntime.ExitRuntimeMismatch` (70) and `piruntime.ExitReason(70) == "runtime_mismatch_in_process"` are the named reason for the launch status (seat-exec, 2b-2, maps the exit code through `ExitReason`). Without the two variables (a user's own pi) the check is skipped.

The residual window, accepted as the same-UID limit (measured below): node reads the entry, the chunks and the package by path between `execve` and the end of the in-process check, about 0.3 s; a same-UID writer that swaps a file and swaps it back inside that window is not caught. The rest of `node_modules` (outside the bundle directory) is read lazily and is covered by the launcher's full re-hash and the launch-set identity check only.

### The items

1. **NODE_OPTIONS (P1).** `Forced` sets `NODE_OPTIONS` empty (`forcedEmpty`: emitted even though empty) and `ScrubEnv` drops every `NODE_*` variable of the parent (`NODE_OPTIONS`, `NODE_PATH`, `NODE_REPL_EXTERNAL_MODULE`, `NODE_EXTRA_CA_CERTS` and the rest). Rows: `TestForcedEnvCannotBeOverridden` (parent `NODE_OPTIONS=--require=/tmp/evil.cjs`, `NODE_PATH`, `NODE_REPL_EXTERNAL_MODULE`, `NODE_EXTRA_CA_CERTS`: `NODE_OPTIONS` is present once and empty, none of the others reaches pi) and the live `TestLiveNodeOptionsRequireNeverRuns` (amqpi tag: a planted `--require` file that writes a marker; a control run of a plain node with the same `NODE_OPTIONS` writes it, `amq-squad pi run ... -- --version` with `NODE_OPTIONS`, `NODE_PATH` and `NODE_REPL_EXTERNAL_MODULE` set does not; PASS, `4-1-evidence/live-review.txt`).
2. **Verified bytes to exec (P1).** As designed above: `Runtime.CheckLaunch` (internal/piruntime/install.go) is called by `piLaunch` right before `piExec`; `pi/extensions/lib/selfcheck.ts` is the package's first code. Rows: `TestCheckLaunchCatchesSwapAfterVerification` (after a clean `Ensure`: the entry renamed over with another file, node rewritten in place with one byte changed, a package file renamed over with the same bytes and a new inode: each fails the check; an unverified runtime fails it), `pi/test/selfcheck.test.ts` (the verified tree passes with the entry, the chunk, `package.json` and the extension; a changed chunk, a changed entry, a changed extension, an extra chunk, an extra extension, a missing chunk, a symlinked chunk and a changed manifest each fail; a mismatch exits 70 with `runtime_mismatch_in_process` and nothing after it runs; no variables means no check).
3. **Symlinked ancestors (P1).** `internal/fsanchor` is 2b-3's descriptor-anchored traversal (v3-2b-3 at 3cc7b779, internal/registry `runtime_open_unix.go`, `runtime_open_other.go`, and `walkAnchored` and `readEntry` from `runtime.go`), lifted once for 4-1 and 4-3 (lead ruling): `open_unix.go` and `open_other.go` are 2b-3's files with `package registry` changed to `package fsanchor` and every package-level identifier and method capitalized on its first letter (`AnchoredDir`, `OpenAnchored`, `CleanName`, `ContainsSlash`, `Sub`, `EntryKind`, `KindOther`, `KindDir`, `KindRegular`, `OpenEntry`, `AsDir`, `Names`, `WriteAtomic`), fields and locals unchanged; `walk.go` holds `WalkAnchored(d, prefix, skip []string, visit)` and `ReadEntry`. The three files are byte-identical with 4-3's (SHA-256 walk.go a0101652…53e2, open_unix.go 7c3cc8e8…793b, open_other.go c88170bf…8d60), so the merge of 4-1, 4-3 and 2b-3 keeps one copy (2b-3's registry should then call fsanchor). 4-1's only addition is `pi_extra.go` (`OpenPath`). `piruntime.Ensure`, `Verify` and `CheckLaunch` now take the home directory as the anchor: `.amq-squad`, `runtime` and the runtime directory are opened with `O_NOFOLLOW` through their parents' descriptors, the manifest and marker are read through the descriptor, the tree is enumerated with `Names` and every entry typed, size- and mode-checked and hashed from the descriptor that `OpenEntry` returned; the cold path creates the base through `OpenAnchored(..., mkdir)` and verifies the renamed tree the same way. Rows: `TestSymlinkedAncestorRefused` (`~/.amq-squad` a symlink to a directory holding a valid runtime, and `~/.amq-squad/runtime` a symlink to one: both refused; the old code accepted the first), `TestSymlinkedRuntimeDirRefused` (moved to the home anchor).
4. **Build trusts the old tree (P2).** `scripts/build-pi-runtime.sh build` removes `build/pi/npm` and runs `npm ci --offline` from the pinned cache (integrity-checked against the lockfile) before it stages and hashes. Rows: `TestBuildRebuildsNpmTreeBeforeStaging` (scripts/pipack: the build function removes the tree, runs `npm ci --offline`, then stages, in that order, and no longer trusts a matching lockfile); live `4-1-evidence/item4-build-tamper.txt`: a line appended to `dist/bundle/cli.js` in the old tree after fetch; after the build the manifest's entry SHA-256 is unchanged (`e79626f2…6774` before and after).
5. **TTY check (P2).** `isTerminal` is the termios ioctl that `golang.org/x/term`'s `IsTerminal` makes (`unix.IoctlGetTermios` with `TIOCGETA` on darwin and the BSDs, `TCGETS` on linux, `false` elsewhere), through `golang.org/x/sys`, already a dependency (no new module). Row: `TestDevNullIsNotATerminal` (`/dev/null` is a character device and not a terminal; with stdin from `/dev/null` and the payload present, bare `amq-squad` prints the help and does not start the lead; the old `ModeCharDevice` test started it).

### Amendment 26: the team lead keeps no Bash tool

Lead ruling: amendment 26 applies to the team lead only (`lead --team`, and `AMQ_SQUAD_LEAD` under `amq launch`); the solo lead stays a normal coding agent per plan 3.2, with the hooks guarding it.

- `piruntime.TeamLeadTools` is the team lead's whole tool set, passed as `pi --tools`: `amq_send`, `amq_drain`, `amq_reply`, `amq_task_list`, `amq_task_add`, `amq_task_reset`, `amq_repo_read`, `amq_repo_list`, `amq_status`. No built-in `bash`, `read`, `edit`, `write`, `grep`, `find` or `ls`; anything that runs a command is dispatched to a seat. The seat keeps its tools.
- New package tools for the lead (`pi/extensions/lib/repo.ts`, `tools.ts`): `amq_repo_read` (a file, optional line offset and limit, 256 KiB cap) and `amq_repo_list` (a directory), both scoped to the repository root (`AMQ_SQUAD_PROJECT`, else the cwd) with `realpath`, so a symlink out of the root is refused; `amq_status` (`amq-squad status --json`, read-only).
- The team-lead role says it cannot run commands, checks a seat's result through the task evidence (`amq_task_list`, `amq_status`) and the files (`amq_repo_read`), and dispatches anything that needs a command.
- Rows: `TestLeadTeamHasNoWriteTools` (the team lead's `--tools` is exactly the list, without any built-in; the solo lead has no `--tools` restriction), `pi/test/selfcheck.test.ts` "the lead's repository read and list stay inside the root" (`..`, an absolute path outside, a symlink out of the root refused for read and list).
- **R19 acceptance text (replaces the earlier pass rule):** the lead made no bash call because it has no Bash tool; the independent go test ran in the harness.
- **R19 re-run with that tool set** (run 5, `4-1-evidence/r19/run5/`, qwen3.6:27b, the same stand-ins): PASS in 159 s. Lead calls: `amq_repo_list`, `amq_task_list`, 2 x `amq_repo_read`, 3 x `amq_task_add` (t1 to t3, `implement`, declared files), then (doorbell) `amq_drain`, `amq_task_list`, 6 x `amq_repo_read`; scheduler decisions `seat_requested, seat_up, dispatched, completed` x 3; final line `CONVERGED`; the harness's independent `go test -count=1 -v ./...` passes TestMax, TestReverse and TestIsEven.

### Live tests after the fix (`4-1-evidence/live-review.txt`)

`AMQ_SQUAD_LIVE_BIN=$PWD/build/pi/amq-squad go test -count=1 -tags amqpi -run TestLive ./internal/cli/` (a live session, run outside the gate): `TestLiveR7FirstRun` PASS (15.2 s; with the anchored re-hash, the launch check, the self-check and the new forced variables: repository clean, nothing outside `~/.amq-squad`, no jiti cache, no new file or changed mtime under the runtime tree), `TestLiveNodeOptionsRequireNeverRuns` PASS.

### Cold starts after the fix (`4-1-evidence/cold5-review.json`)

Same method as before (fresh process, runtime unpacked, model loaded). New columns: the launch-set identity check (item 2 (a), inside "launch rest"), the in-process self-check (item 2 (b), its own cost) and the window from `execve` to the end of the self-check.

| Series | Run | Re-hash ms | Launch rest ms | of it: launch check ms | Node start to pi ready ms | Self-check ms | exec to self-check done ms | Model first token ms | Spawn to first token ms |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| lead | 1 | 156.2 | 30.0 | 6.8 | 377.8 | 9 | 351 | 10204.8 | 10776.9 |
| lead | 2 | 138.8 | 23.3 | 7.0 | 293.8 | 9 | 270 | 81.5 | 544.7 |
| lead | 3 | 137.0 | 20.5 | 7.0 | 288.5 | 8 | 264 | 79.5 | 532.6 |
| lead | 4 | 155.7 | 19.6 | 7.2 | 323.4 | 10 | 295 | 92.5 | 599.7 |
| lead | 5 | 166.3 | 21.3 | 7.7 | 337.8 | 10 | 313 | 86.1 | 619.0 |
| **lead median** | | **155.7** | **21.3** | **7.0** | **323.4** | **9** | **295** | **86.1** | **599.7** |
| control | 1 to 5 median | 145.7 | 20.8 | 6.8 | 143.8 | n/a | n/a | 70.2 | 375.8 |

- The full re-hash rose from 117.6 ms to 155.7 ms median (137 to 166 ms): the anchored walk opens every entry through its parent's descriptor and lists every directory through its descriptor (item 3's price; still inside amendment 25's 80 to 190 ms).
- The rest of the launch is 21.3 ms, of which the launch-set identity check is 7.0 ms; under the 60 ms line.
- The in-process self-check costs 9 ms (the bundle and the package, about 9 MB).
- **The residual window** (item 2, accepted as the same-UID limit): from `execve` to the end of the self-check, 295 ms median (264 to 351 ms). In that time node reads the entry, the chunks and the package by path before they are re-checked; a swap that is undone inside it is not caught.
- Run 1 of each series again pays Ollama's prefill of an uncached prompt (10.2 s and 1.7 s this time).

### Follow-ups added by this round

- At the rebase after 2b-5 is on v3 (lead ruling): `fsanchor.WriteAtomic`'s body becomes a call into `internal/atomicfile` (an anchored variant in one new file `atomicfile/anchored.go`, unless 4-3 added it first; check v3 first). Until then `WriteAtomic` stays as lifted from 2b-3. atomicfile is not copied into this branch.
- 2b-3 merge: `registry.RehashRuntime` and its `anchoredDir` helpers move onto fsanchor (one copy).
- 2b-2: seat-exec maps a pi exit of 70 to `runtime_mismatch_in_process` through `piruntime.ExitReason`, and launches the team lead with `piruntime.TeamLeadTools`.

## Codex second pass on fb1a152a

Items 4 and 5 complete; 1, 2, 3 and amendment 26 partial. One more fix commit on top of fb1a152a, one test row per item that fails when reverted; the reverts were run and are kept in `4-1-evidence/review2-revert-check.txt` (every row fails with its fix reverted and passes with it restored: ALL OK).

1. **NODE_* from an overlay.** `Prepare` now drops `NODE_*` and `JITI_*` keys from every overlay before the forced set is applied last (`loaderVar`); the parent filter uses the same rule. Rows (`TestLoaderVarsFilteredFromEveryOverlay`, one per overlay source): the caller's overlay (`NODE_PATH`, `NODE_TLS_REJECT_UNAUTHORIZED=0`, `NODE_OPTIONS=--require=...`, `NODE_REPL_EXTERNAL_MODULE`, `JITI_*`), a seat's overlay (the launch-record values seat-exec passes the same way), and the registry entry's declared needs (`EnvNeeds` naming `NODE_PATH`, `NODE_TLS_REJECT_UNAUTHORIZED` and `NODE_OPTIONS` that the parent sets): only the forced values reach pi.
2. **The verify-to-exec window**, the four rulings:
   - (a) `CheckLaunch` re-hashes by content the files the first instructions run: node, the preload, the pi entry and the package's files outside its `node_modules`; the rest of the launch set stays an identity check. Row: `TestCheckLaunchCatchesSwapAfterVerification` "node rewritten in place, mtime restored" and "extension rewritten in place, mtime restored" (same inode, size and mtime: only the hash catches them).
   - (b) The self-check is now `pi/preload/runtime-check.cjs`, a one-purpose CommonJS module that node loads with `--require` on its own command line before the pi entry (`Runtime.PiArgv`: `node --require <preload> <entry>`; the manifest's new `preload` field, validated to be in the payload). Its first statement is the check; it requires only `node:crypto`, `node:fs` and `node:path`. It hashes every manifest file (6,441 on the decided build, node and the whole pi tree included), refuses an extra file or directory, a missing file and any non-regular entry. The package's own in-extension check is removed. Rows: `TestPreloadRunsBeforeTheEntry` (argv order, the preload is content-checked, a changed preload fails `CheckLaunch`, a preload outside the payload does not validate), `pi/test/preload.test.ts` (every file is checked: a changed node, a changed lazy module outside the bundle, a changed tool, chunk or extension, an extra file, an extra directory, a missing file, a symlinked file, a changed manifest; loaded with `--require` it exits 70 before the script runs; the first code statement is the check).
   - (c) Exit 70 is a launcher refusal. The launcher no longer replaces itself with node: it runs node as a child (`runProcess`: the child shares the terminal and process group; the parent catches SIGINT and SIGQUIT, which then reset to default in the child, and forwards SIGTERM and SIGHUP) and waits. On exit 70 it writes `runtime_mismatch_in_process` to the seat's ledger (`event: runtime_mismatch_in_process`, `outcome: refused`, `action: seat_closed`), prints the named reason, and ends with 70, which closes the seat; any other code passes through. Rows: `TestPiExit70IsALauncherRefusal` (ledger line, stderr, launcher exit 70; the line is not counted as a tool result for a resume), `TestPiExitCodesPassThrough`.
   - (d) **The accepted limit, and no stronger claim:** an in-place swap of node or of the preload by the same user, between `CheckLaunch` (which hashes them) and node's own load of them, is not caught. Measured window, `execve` to the end of the preload's check: 221 ms median (cold-start table below). The lead records it in amendment 25 and puts "runtime executed from a read-only mounted image (`hdiutil attach -readonly`)" on the phase 5 list as the item that closes it.
3. **The anchor and the cold install.**
   - The anchor fix from 4-3 (component by component from `/` with `O_NOFOLLOW`; a base that is not absolute and clean is refused) is applied verbatim: `internal/fsanchor/open_unix.go` SHA-256 `3281ecae…19e3`, `open_other.go` `c88170bf…8d60`, `walk.go` `a0101652…53e2`, identical with 4-3's. Row: `TestSymlinkedHomeRefused` (HOME itself a symlink to a directory holding a valid runtime: `Ensure` and `Verify` refuse). Test homes now use their real paths (`tempHome`), because macOS temp directories sit under `/var`, a symlink to `/private/var`.
   - The cold install stages only through the held base descriptor: the lock file (`StageLock`, `openat` `O_CREAT|O_NOFOLLOW`, with a short retry on the Darwin `O_CREAT` race 4-3 met), the staging directory (`StageMkdir`, `mkdirat`), every directory and file of the payload (`unpackAnchored`: `mkdirat` and `openat` `O_CREAT|O_EXCL|O_NOFOLLOW` relative to each parent's descriptor, the mode set through the file's own descriptor), the manifest and the marker (`stageFile`), the staging directory's mode (`fchmod`), the rename into place (`renameat`) and the cleanup on failure (`StageRemoveAll`, `unlinkat`). These live in `internal/fsanchor/pi_extra_unix.go` (`pi_extra_other.go` off unix), named `Stage*` so they never collide with 4-3's additions. Row: `TestColdInstallIgnoresSwappedBase` (a test seam swaps `~/.amq-squad/runtime` for a symlink to an attacker directory after the base was opened and re-checked: no staging write reaches the attacker directory, the tree lands in the held directory, and the final verification refuses the symlinked base).
- **Amendment 26, the two residuals.**
   - `amq_task_reset`: `task reset` now calls `Store.ResetBy(id, caller, pool, now)` (new file `internal/task/reset_by.go`; `Store.Reset` stays for its existing callers): the store refuses a task whose `created_by` is another handle (another squad's lead) and any block routed to the owner, and needs a caller. Row: `TestResetByChecksCallerAndRoute` (own task resets; another lead's task, an owner-routed block and a reset without a caller are refused; a task without a recorded creator is the squad's own).
   - `amq_repo_read` / `amq_repo_list`: the path is validated with `realpath`, then opened, and the opened descriptor is checked against an `lstat` walk of the validated components (no symlink, same device and inode at the end); a listing is re-checked after it was read by path. Recorded as best effort within the same-UID limit. Row: `pi/test/preload.test.ts` "a swap between validation and open is refused" (the validated directory swapped for a symlink out of the root after validation).

Live (`4-1-evidence/live-review.txt`, after the rebuild): `TestLiveR7FirstRun` PASS (fresh HOME and TMPDIR, one lead turn with a bash call, through the preload and the child launcher), `TestLiveNodeOptionsRequireNeverRuns` PASS. A smoke turn printed `amq-squad pi self-check: files=6441 bytes=179335322 ms=222`.

Cold starts on this build (`4-1-evidence/cold5-review2.json`; fresh process, runtime unpacked, model loaded; medians of five):

| Series | Re-hash ms | Launch rest ms | of it: launch content and identity check ms | Node start to pi ready ms | of it: preload self-check ms | exec to preload done ms | Model first token ms | Spawn to first token ms |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| lead | 147.3 | 56.7 | 42.3 | 516.6 | 202 | 221 | 91.5 | 859.5 |
| control (no package) | 152.0 | 57.0 | 43.1 | 359.4 | 206 | 226 | 75.9 | 644.8 |

- The launch check now hashes node (112 MB) by content: 42.3 ms, which brings the launch rest to 56.7 ms, just under the 60 ms line.
- The preload's full in-process re-hash costs about 200 ms on every start; with it, node start to pi ready is 516.6 ms (323.4 ms before) and spawn to first token 859.5 ms (599.7 ms before).
- The accepted window (2 (d)) is 221 ms: node's start plus the preload's own re-hash, before any pi or package code runs.

Rows: new `internal/fsanchor/pi_extra_unix.go`, `pi_extra_other.go`, `internal/task/reset_by.go` (fate new); current_lines updated for the changed new rows and for `internal/cli/task.go` (523 -> 526, one call changed to `ResetBy`).

Behavior changes: the launcher runs pi as a child and exits with pi's code (no exec in place); a pi exit of 70 closes the seat with `runtime_mismatch_in_process` in the ledger; `task reset` needs the caller's handle and refuses another lead's task and owner-routed blocks; node starts with `--require <runtime>/pi/preload/runtime-check.cjs`.

## Rebase onto v3 after 2b-2 A, 2b-5, 4-2 and 2b-3

`v3-4-1` (head 9fdaa66e) rebased onto `v3` at 1b920331 (after the 2b-3 merge fec06084; 2b-2 milestone A f4d3dc13, 2b-5 fbf56347 and 4-2 6b27c0c4 before it). The first commit conflicted in `internal/cli/{cli.go,cli_test.go,command_registry.go,doctor.go}` and the manifest; the other eight replayed clean. Resolution: v3's side kept everywhere and 4-1's additions re-applied (the `lead` and `pi` verbs and catalog rows; in 2b-3's rewritten `doctor.go`, the `--fix` flag and the pi runtime rows); the manifest is v3's with 4-1's rows re-added. One integration commit follows, per the lead's rebase checklist:

- **atomicfile (2b-5).** New `internal/atomicfile/anchored.go`, `WriteAt(dirfd, dirPath, name, data, mode)`: the temp file is created relative to the directory descriptor (`openat`, `O_CREAT|O_EXCL|O_NOFOLLOW`, random `TempPrefix` name the gate store's sweep recognizes), written, chmodded, fsynced and closed, renamed over the target with `renameat` relative to the same descriptor, and the directory is fsynced through it; no pathname step. 4-1 is first, so it adds the file. `fsanchor.WriteAtomic`'s body is now a call into it (`atomicfile.WriteAt(d.fd, d.path, name, data, 0o600)`; off unix `atomicfile.Write` under the checked path). Row: `TestWriteAtStaysOnTheHeldDirectory` (the path swapped for a symlink after the descriptor was opened: the write lands in the held directory, the attacker directory stays empty; a symlink at the target name is replaced, never followed; no temp left; a name with a slash refused). 4-3's copy of `open_unix.go` and `open_other.go` must take this change at its rebase (told to 4-3 with the new SHA-256).
- **registry onto fsanchor (2b-3).** Deleted `internal/registry/runtime_open_unix.go` and `runtime_open_other.go` (2b-3's private copies) and the private `walkAnchored` and `readEntry` in `runtime.go`; `RehashRuntime`, `PiProbeEnv`, `SafeDir` and the review tests now call `fsanchor.OpenAnchored`, `WalkAnchored` (the manifest and marker as the `skip` list), `ReadEntry`, `OpenEntry`, `AsDir`, `Names`, `Sub` and `WriteAtomic`. The identifiers are fsanchor's, so 4-3's identical copy merges into the same files. Their manifest rows were fate-new 2b-3 rows: they are **retired by deleting the rows** (no `replaced_by`, which check 8 asks for on kept-file rows retired from 2b on). The registry inherits the anchor fix (from `/` with `O_NOFOLLOW`); its tests run with `TMPDIR` at its real path (`internal/registry/testmain_test.go`) because macOS temp directories are under the `/var` symlink.
- **doctor and the probe (2b-3).** With the payload embedded, doctor's `pi runtime` row is piruntime's (`piruntime.Open`, new: verify without unpacking): the runtime this binary embeds, opened from HOME through descriptors and re-hashed against the embedded digest; `--fix` removes a rejected runtime; the `pi runtime versions` row and its prune follow. The verified `Runtime` is kept on the host probe, and `doctor --probe` runs the R12 probe with `Runtime.PiArgv()` (node, the self-check preload, the pi entry) instead of 2b-3's manifest read; without a payload the 2b-3 path is unchanged. `syncRegistryRuntimePin` gives the registry the embedded manifest digest (`registry.EmbeddedRuntimeManifestSHA256`) and directory name (new `registry.EmbeddedRuntimeDirName`), and `registry.PiRuntimeDir` returns that directory rather than the lexically newest one. Row: `TestDoctorHostUsesEmbeddedRuntime`.
- **The lead's model from the registry (ruling 4).** Without `--model`, `amq-squad lead` reads `~/.amq-squad/registry.yaml` and takes the first verified pi entry in file order, preferring effort medium (its effort becomes pi's thinking level); with no registry or no verified pi entry it falls back to `defaultLeadModel` and says so on stderr. Row: `TestLeadModelFromRegistry`.
- **seat-exec's pi resume (2b-2 A).** `seatexec.CheckPiResume` delegates to `piruntime.CheckResume` (the pinned pi 1.3.0 layout: the session file in the folder of the seat's cwd, `firstNonEmpty(rec.Worktree, rec.CWD)`), replacing the provisional substring walk `PiSessionFileExists` (removed). `TestSeatExecPiResumeRefusal` now plants the session file at the pinned path and adds a row: a file for the same id in another cwd's folder does not count.
- The band floor is retired on v3 (manifest-check prints the size of 3.0 instead); check 8 is live and has no 4-1 row to apply to (no kept v2 file is retired here).

Not done in this commit, for 2b-2 milestone B or a follow-up: seat-exec still resolves the pi provider on PATH and execs it in place; handing a pi seat to the embedded runtime (`piruntime.Ensure`, `Prepare`, `CheckLaunch`, the preload, the child run and exit 70 through `piruntime.ExitReason`, the team lead's `TeamLeadTools`) is the remaining wiring, and needs AMQ's pi launch adapter (acceptance 10, blocked) to be exercised end to end.

Gate on the rebased head (`4-1-evidence/rebase-gate.txt`), race on piruntime, fsanchor and registry, and the live R7 test once: see the end of this file.

Results on the rebased head: the full gate PASS (`4-1-evidence/rebase-gate.txt`: gofmt, go build, go vet, the linux and windows builds, manifest-check --final, go test ./... with 44 packages ok, the package lint and 36 tests, the darwin arm64 build with the payload, 68,565,490 bytes, and the race run on piruntime, fsanchor, registry and atomicfile, all ok); the live tests once (`4-1-evidence/live-review.txt`): TestLiveR7FirstRun PASS (39.0 s), TestLiveNodeOptionsRequireNeverRuns PASS. The cli and registry test binaries now set TMPDIR to its real path in TestMain for the anchored re-hash.
