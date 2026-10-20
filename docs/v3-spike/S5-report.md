PASS

# S5 report: pi seat on Ollama under `amq coop exec`, R12 probe, hold seam, pi lead loop

Date: 2026-10-21. Owner: pi-dev (Claude Code, Opus 5.5). Branch `spike/s5-pi`
(from `v3`). Revision 2: the Codex review edits are applied (last section).

Criterion (docs/v3-phase1-plan.md S5): the probe passes on at least one
local model, the hold seam works, the three tasks complete. All three hold:

| Criterion | Result | Evidence |
| --- | --- | --- |
| R12 probe passes on a local model | Hardened predicate (v2). qwen3.6:27b 6/6 (verified). qwen2.5:32b 6/7, one plain failure, zero false successes (unverified). gemma3:4b: no tool calls. | `S5-evidence/probe-results-v2.jsonl`, `probe-recheck-v2.jsonl` |
| Hold seam | The hold primitive works. An extension holds a tool call with no upper bound. Allow, deny, timeout, abort, hash mismatch, and handler exception all behave; all but allow block. | `S5-evidence/hold-tests.jsonl`, `hold-log-tui-pi-1.jsonl` |
| Three tasks via a pi lead | Converged in 2 of 2 runs with the v2 brief (qwen3.6:27b lead and worker). An earlier attempt with a different brief failed. Run 2 dropped a task, and the lead recovered it. | `S5-evidence/lead-loop-*.txt`, `amq-messages.txt` |

16.3 does not narrow on the evidence of the hold primitive. A pi
extension can hold, allow, and block a tool call with no change to pi. The
rest of bridge revision 4 is untested (Step 3, "Untested"); it is 2b
integration work.

## Versions and host

| Item | Value |
| --- | --- |
| pi | `@earendil-works/pi-coding-agent` 1.3.0 (published 2026-10-15), installed with `npm install --prefix ~/.amq-squad/spike/pi` |
| pi package name | `@mariozechner/pi-coding-agent` is deprecated at 0.73.1 ("please use @earendil-works/pi-coding-agent instead"). The AMQ reference bridge imports `@earendil-works/pi-coding-agent`. AMQ `docs/remote-compat.md` pins evidence at pi 0.85.1. |
| amq | 0.102.0 (Homebrew); upstream reference read at tag v0.102.0 |
| Ollama | 0.34.4, auto context 131072 on this Mac (`ollama ps`), `llama-server -np 1` |
| Models | qwen2.5:32b (19 GB), qwen3.6:27b (17 GB, pulled for this spike, digest bcbdbd4bcf86), gemma3:4b (3.3 GB) |
| Host | Apple M5 Max, 64 GB, node 22.21.1, go 1.27.1, tmux 3.7c (private socket `-L s5spike`) |

## Step 1: pi seat under `amq coop exec`

amq 0.102.0 rejects the brief's form `amq coop exec --root R --me pi-1 -- pi`
("command required"). The grammar is
`amq coop exec [options] <command> [-- <command-flags>]`:

```sh
amq coop exec --root ~/.amq-squad/spike/s5/project/.agent-mail/collab --me pi-1 \
  ~/.amq-squad/spike/s5/bin/spi -- --model ollama/qwen2.5:32b
```

Observed:

- `amq who` listed `pi-1  active (notifier_live)`.
- `amq wake check --me pi-1 --json` reported a live wake in `paste` mode,
  version 0.102.0, `owner_bound: false`.
- Inside pi, `!env` showed `AM_ROOT`, `AM_ROOT_ID`, `AM_BASE_ROOT`,
  `AM_ME=pi-1`, and `AM_SESSION=collab`.
- pi 1.3.0 started its TUI (`(ollama) qwen2.5:32b`, `0.0%/131k`). It warns
  that tmux `extended-keys` is off; this did not affect the spike.

pi config (this spike uses no file under `~/.pi`; see "Host state"):

- `spike/pi-config/spi`: a wrapper that execs the prefix-installed pi with
  these settings:
  - `PI_CODING_AGENT_DIR=~/.amq-squad/spike/s5/pi-agent`, which isolates
    config and credential storage;
  - `PI_OFFLINE=1`, which only disables pi's startup network operations
    (`dist/cli/args.js:336`);
  - `PI_SKIP_VERSION_CHECK=1` and `PI_TELEMETRY=0`;
  - `GOCACHE=~/.amq-squad/spike/s5/gocache`.
- `spike/pi-config/models.json`, copied to `$PI_CODING_AGENT_DIR/models.json`:
  - provider `ollama`: `baseUrl http://localhost:11434/v1`,
    `api openai-completions`, dummy `apiKey ollama`;
  - three models with `contextWindow 131072`; qwen3.6:27b has
    `reasoning: true`.

  No Modelfile or Ollama server change was needed, because Ollama already
  sizes context to 131072 here.

## Step 2: R12 probe

`spike/piprobe/probe.mjs` copies `spike/piprobe/fixture` into a fresh git
repo for each run. The fixture is a Go module whose `TestGreeting` fails
until `Greeting` returns `"hello, world"`. The probe runs
`pi --mode json --no-session --model <m> --tools read,edit,write,bash --no-context-files --no-skills --no-prompt-templates "<task>"`
and timestamps the JSON events. The task: read calc.go, make one stated
one-line edit, run `go test ./...`, report, and end with a line
`RESULT: PASS` or `RESULT: FAIL`.

### Predicate v2 (hardened after the Codex review)

A run passes only when all five checks hold:

1. `read_confirmed`: a `read` of calc.go (or a bash cat, head, sed, or less of
   it) has a `tool_execution_end` that is not an error.
2. `go_test_confirmed`: a bash call runs `go test`, and its
   `tool_execution_end` is not an error and shows an `ok` package line.
   A `tool_execution_start` alone never counts.
3. `worktree_exact`: `git status --porcelain --untracked-files=all` lists
   only calc.go (no staged extras, no untracked files), and
   `git diff HEAD --numstat -- calc.go` is exactly `1 1`.
4. `independent_go_test`: `go test -count=1 ./...`, run by the probe, passes.
5. `final_line_exact`: the last non-empty line of the final assistant
   message is exactly `RESULT: PASS`.

`false_success` = `final_line_exact` and not (`independent_go_test` and
`worktree_exact`). In words: the agent says PASS and the verifier says FAIL.
`node probe.mjs --recheck <run dirs>` re-scores finished runs. Predicate v1
was: an independent go test passes, `git diff --numstat` equals `1 1 calc.go`,
some bash `tool_execution_start` contains `go test`, and the last line
matches `RESULT:\s*PASS`.

### Counts, old and new predicate

| Model | Batch 1, scored v1 (spike) | Batch 1, re-scored v2 | Batch 2, fresh runs v2 | False successes |
| --- | --- | --- | --- | --- |
| qwen3.6:27b | 3/3 | 3/3 | 3/3 | 0 |
| qwen2.5:32b | 2/3, plus 1 smoke pass | 2/3, plus smoke pass | 3/3 | 0 |
| gemma3:4b | 0/3 | 0/3 | 0/1 | 0 |

The v2 predicate changed no verdict on the batch-1 runs.

### Per-run detail (batch 2, fresh, predicate v2; times in ms from spawn)

| Model | Run | Result | First reasoning | First tool call | First visible text | Total | Tool calls (`!` = error) |
| --- | --- | --- | --- | --- | --- | --- | --- |
| qwen3.6:27b | 1 (cold load) | PASS | 6977 | 8823 | 14104 | 14657 | read, edit, bash |
| qwen3.6:27b | 2 | PASS | 260 | 2201 | 8060 | 8750 | read, edit, bash |
| qwen3.6:27b | 3 | PASS | 250 | 2307 | 7975 | 8628 | read, edit, bash |
| qwen2.5:32b | 1 (cold load) | PASS | n/a | 8056 | 15192 | 16300 | read, edit!, edit, bash |
| qwen2.5:32b | 2 | PASS | n/a | 2929 | 10065 | 11142 | read, edit!, edit, bash |
| qwen2.5:32b | 3 | PASS | n/a | 3089 | 10098 | 10438 | read, edit!, edit, bash |
| gemma3:4b | 1 | FAIL | n/a | n/a | n/a | 183 | none: Ollama 400 "gemma3:4b does not support tools" |

Batch 1 (spike, `S5-evidence/probe-results.jsonl`, re-scored in
`probe-recheck-v2.jsonl`), with the failed tool calls described exactly:

- qwen2.5:32b, smoke, run 1, and run 3: PASS. Each first emitted read, edit,
  and bash in parallel. The parallel `edit` was malformed: it carried bash's
  arguments `{"command":"go test ./..."}` and failed schema validation. The
  model then re-issued a correct edit and bash.
- qwen2.5:32b, run 2: FAIL. The parallel `edit` was well-formed but guessed
  8 spaces for the tab, so it failed with "Could not find the exact text".
  The parallel `bash` ran `go test`, which exited 1 because the test still
  failed; that was not a tool fault. The model then wrote "The edit was
  applied correctly, but the test failed…", which is a false claim about the
  edit. It also wrote `RESULT: FAIL` and then one more sentence. It reported
  FAIL, so this is not a false success under the verifier definition. It is
  recorded as a reliability finding: a false claim about its own edit.
- qwen3.6:27b, runs 1-3: PASS. Read, one edit, one bash, no tool errors.
- gemma3:4b, runs 1-3: Ollama 400, no tools.

In batch 2 all three qwen2.5 runs again sent the malformed edit carrying
bash's arguments first. That happened in 6 of 7 qwen2.5 runs overall.
qwen3.6 made no tool error in 6 runs.

### Direct TTFT against Ollama (`spike/piprobe/ttft.mjs`, raw in `S5-evidence/ttft-raw.jsonl`)

| Model | Run 1, reasoning / visible text (ms) | Warm, reasoning (ms) | Warm, visible text (ms) |
| --- | --- | --- | --- |
| qwen2.5:32b | n/a / 294 | n/a | 43-46 |
| qwen3.6:27b (max_tokens 512) | 5219 (load) / none within 512 tokens | 106-118 | 6827-9334 |
| gemma3:4b | n/a / 2225 (load) | n/a | 22-24 |

qwen3.6 thinks before it answers. Its first visible text includes the whole
reasoning phase. The first version of `ttft.mjs` counted reasoning and text
together, so the "89-90 ms" first reported for qwen3.6 was reasoning
TTFT, not visible text. pi starts in 129-153 ms (spawn to session header).

### Registry rule (adopted, lead ruling; reclassified after the Codex review)

- The R12 probe uses the five-check v2 predicate. Each registry entry
  records passes over N, tool-error count, and false-success count.
- **verified** = N/N passes and zero false successes. N=3 in the spike;
  N=5 before a registry entry ships. The rule stands.
- qwen3.6:27b: **verified**. 3/3 under v2 in the spike batch, 3/3 again in
  batch 2, zero false successes, zero tool errors.
- qwen2.5:32b: **unverified**. 2/3 in the spike batch, zero false successes,
  so it fails on the pass count. The fresh 3/3 does not erase the earlier
  failure.
  - Reliability findings recorded against the entry: a false claim about
    its own edit (batch 1, run 2), and a malformed first edit in 6 of 7 runs.
  - The earlier label "probation, one false success" was wrong and is
    withdrawn.
- gemma3:4b: state **no tool calls**, not a failure. Ollama refuses tools
  for the model, so the probe cannot run.
- Registry latency is direct TTFT, not pi's first event. On a tool-call turn
  Ollama sends the call as one chunk when it is complete, so pi's first
  event measures the time to generate the whole call. Record both reasoning
  TTFT and visible-text TTFT. For a thinking model, only the latter is what
  a user waits for.

## Step 3: hold seam (the revision 4 hold primitive)

Verdict: **the hold primitive works**. A pi extension can hold a tool call
for as long as it wants, then allow or block it. Stock pi 1.3.0 awaits every
`tool_call` handler before it executes the tool:
`dist/core/agent-session.js` `_beforeToolCall` calls
`dist/core/extensions/runner.js` `emitToolCall`, which is a plain
`await handler(event, ctx)` with no timeout and no race.
`docs/extensions.md` states the failure rule: "A `tool_call` handler failure
blocks the tool as a fail-safe."

Extension: `spike/pi-hold-ext/hold.ts`.

- It is active only when `AM_ROOT` and `AM_ME` are set, like the reference
  bridge.
- For tools in `AMQ_SQUAD_HOLD_TOOLS` (default bash, edit, write), it writes
  `<AM_ROOT>/agents/<AM_ME>/extensions/io.github.omriariav.amq-squad/pending-<id>.json`.
  The record carries the tool, the input, a `manifest_hash`, and an
  `expires_at`. `<id>` is the tool call id.
- It then waits for one of these:
  - `approve-<id>`: allow;
  - `deny-<id>`: block;
  - a timeout: block;
  - `ctx.signal` abort: block.
- A JSON approve whose `manifest_hash` differs from the pending call is
  refused, and the call is blocked.
- It logs every hold and decision to `hold-log.jsonl`. It shows a TUI status
  line while it holds.

Test driver: `spike/pi-hold-ext/hold-test.mjs`. It runs pi under
`amq coop exec --me pi-hold --no-wake` in JSON mode with qwen2.5:32b. The
tool call is `touch <marker>`, so the marker file proves whether the tool
body ran.

| Scenario | Held | Marker before answer | Outcome | Marker after |
| --- | --- | --- | --- | --- |
| approve after 120 s | 120105 ms | absent | allow; tool ran 20 ms after the approve file appeared | created |
| timeout 8 s | 8002 ms | absent | blocked: `amq-squad hold: timeout (no decision in 8000 ms)` | absent |
| handler throws | 0 ms | absent | blocked: pi turned the exception into an error tool result (`hold extension test exception for call_…`) | absent |
| deny | 3032 ms | absent | blocked: `deny` | absent |
| approve with wrong manifest_hash | 3030 ms | absent | blocked: `refused (manifest_hash mismatch)` | absent |
| TUI seat pi-1 under coop exec with wake, approve from another shell | 36142 ms | absent | allow; TUI showed `holding bash call_jz0o46os: waiting for approve-call_jz0o46os` | created |
| TUI seat, Escape during hold | 8135 ms | absent | `aborted` via `ctx.signal`; pi showed "Operation aborted" | absent |

Findings:

1. pi emits `tool_execution_start` before the `tool_call` hook runs, so a
   held call shows as started in pi's event stream and in the reference
   bridge's `activity/` stream. This does not break the bridge contract,
   because the activity stream "never decides a request outcome"
   (`pi-bridge-protocol.md:186`). It is UX feedback: a viewer should see
   "held", not "running". Filed as "Revision 4 feedback" in ask 1.
2. pi applies no timeout of its own. The extension must own `expires_at`
   and the timeout, or one lost answer stalls the session forever. It must
   also honour `ctx.signal`, or Escape cannot cancel a held call. Tested:
   Escape works when the extension listens.
3. A throw blocks the call (fail closed), which matches plan 4.4. A throw in
   one extension's handler also skips the later `tool_call` handlers,
   because `emitToolCall` does not catch per handler.
4. The model sees the block reason as the tool result. qwen2.5 obeyed "do
   not retry". A model that retries creates a new call id and a new pending
   record each time, so the bridge needs a per-session cap or a back-off.
5. Holds serialize within a batch. pi 1.3.0 handles a batch in
   `executeToolCallsParallel` (`pi-agent-core/dist/agent-loop.js:411`). It
   prepares calls one at a time: for each call it emits
   `tool_execution_start` and awaits `prepareToolCall`, which runs the
   `tool_call` hooks. Only then does it run the prepared batch concurrently.
   While the first call of a parallel batch is held, the later calls are not
   even raised for approval. Approvals in one batch are therefore
   sequential, and each one delays the next.

Untested, and listed as 2b integration tests, not spike gaps (lead ruling:
hold primitive accepted):

- ref ownership: an interaction raised only while the extension owns a ref;
- answer binding to `interaction_id` and `manifest_hash` through
  `answers/<hash>.json`;
- expiry through `expires_at` against a real adapter;
- `interaction` and `interaction_resolved` events, including `run_ended`;
- a local `ctx.ui.confirm` racing a remote answer (first decision from any
  face);
- parallel held calls under finding 5.

## Step 4: lead loop (pi lead, pi worker, AMQ)

Setup:

- Both seats run qwen3.6:27b under `amq coop exec`, with the live wake in
  paste mode, in one tmux session.
- The worker role (`spike/pi-lead-loop/worker-role.md`) and the lead role
  (`lead-role.md`) are passed with `--append-system-prompt`.
- The goal is three helpers (Max, Reverse, IsEven), each in its own file
  with a table test.

Both seats run qwen3.6 to avoid weight swaps: only one of these models
stayed resident at a time, and loading gemma3 evicted the others. Ollama
also serves one request at a time (`-np 1`), so all Ollama seats share one
GPU slot. That supports a single Ollama concurrency pool in the registry.

| Attempt | Brief | Result |
| --- | --- | --- |
| 0 | Role only in `--append-system-prompt`; goal as the user message (`lead-brief.txt`). | FAIL. qwen3.6 reasoned "I don't actually have access to any AMQ (Advanced Message Queuing) system or anything like pi-1. I should just create the files directly." I aborted it before it wrote anything. The session file shows the role text was in the system prompt. |
| 1 | Role inline in the first user message, saying `amq` is a CLI on PATH and to run `amq who` first (`lead-brief-v2.txt`). | PASS in about 5 min 20 s. |
| 2 | Same brief, fresh sessions, reset project. | PASS in about 10 min 43 s, with one dropped task that the lead recovered. |

Run 1 (`S5-evidence/lead-loop-transcript.txt`), times UTC:

- 21:20:27: the lead runs `amq who` (pi-1 active) and reads the module.
- 21:20:42: the lead runs three `amq send --to pi-1 --kind todo` in parallel.
  Each body names the files, the signature, and "reply when go test ./...
  passes".
- 21:20:42: the wake pastes
  `: AMQ doorbell run amq drain --include-body then act on it` into pi-1.
  pi-1 drains all three tasks and writes six files at 21:21:32.
- pi-1 fixes an invalid Unicode escape and a failing reverse case.
  `go test` is red twice, then green at 21:22:11.
- 21:22:28: `amq reply <id> "<text>"` fails ("reply does not accept
  positional arguments"). pi-1 reads `amq reply --help` and replies with
  `--id` and `--body` at 21:23:21.
- Lead: `amq watch --timeout 300s` returns at 21:23:21. amq notes that watch
  is redundant with a live wake.
- The lead's `amq drain --include-body .` fails on a stray `.` that came from
  the brief itself (`lead-brief-v2.txt` line 12, now fixed).
- The lead then drains correctly, runs `go test ./...` (ok), reads all six
  files, prints a summary, and ends with `CONVERGED`.
- The lead made no write or edit call. pi-1 made all writes.

Run 2 (`S5-evidence/lead-loop-run2-transcript.txt`):

- The lead sent the three tasks 11-19 s apart.
- Doorbells reached the worker mid-turn as extra user messages. It drained
  Max first, then Reverse and IsEven 36 s later.
- It implemented only Reverse and IsEven.
- It tried a non-existent `amq post`, recovered through `amq reply --help`,
  and replied to two of the three tasks.
- The lead waited through two `amq watch` timeouts. It then checked the files
  and sent a follow-up ("FOLLOW-UP: Implement Max function (was not
  completed)").
- The worker did Max. The lead verified `go test ./...` and converged.

Independent verification, uncached:

- Run 2: `go test -count=1 -v ./...` on the files pi-1 left
  (`S5-evidence/lead-loop-run2-go-test.txt`): TestGreeting, TestIsEven,
  TestMax, and TestReverse all PASS.
- Run 1: its files were removed before run 2. They were rebuilt
  from the lead's own `cat` of all six files at 21:25:20 (trailing newlines
  normalized)
  (`spike/pi-lead-loop/reconstruct-run1.py`), and the same uncached run
  passes (`S5-evidence/lead-loop-run1-go-test.txt`).

### Dropped task on a mid-turn doorbell: evidence for D5

In run 2, pi-1 drained Max first. A doorbell then arrived mid-turn, and
pi-1 drained Reverse and IsEven. It implemented only those two, and Max was
silently dropped.

Two causes are separated here:

- The mail was not lost. AMQ delivered and receipted all three messages.
  `amq drain` moves each message to `cur` at once. AMQ has no completion
  lease or ack of the work (upstream `internal/cli/drain.go:25`: "Drains new
  messages: reads, moves to cur, emits receipts").
- What was lost is work tracking: once drained, nothing records that Max is
  still owed. Only the lead's file-level verification recovered it, after
  two watch timeouts.

That the mid-turn doorbell (the second drain's output displacing the first
task) caused the drop is plausible but not isolated; this run cannot tell
it apart from plain model forgetfulness.

This is direct evidence for D5: the task store holds the work, with leases,
and mail is not the queue.

### Role delivery (lead ruling, scoped after the Codex review)

- Path that worked: the role inline in the first user message (attempts 1
  and 2).
- Path that failed: the role only in `--append-system-prompt` (attempt 0).
- Failure signature: the lead reads "AMQ" as "Advanced Message Queuing",
  decides it has no worker, and starts coding.
- This is not a fair comparison. Between attempt 0 and attempts 1-2,
  placement changed (system prompt to user message), and so did the
  wording. The working brief also told the lead to run `amq who` first.
  `before_agent_start` injection, the plan 4.4 mechanism, was never tried.
- 2b rule for the pi package: put the role text in the first message and
  also in the system prompt.
- 2b lead-loop test: the lead does not start coding. It makes no write or
  edit call, and its first tool call is an `amq` command.
- 2b matrix: deliver the role three ways:
  - `before_agent_start` system-prompt injection;
  - `before_agent_start` message injection;
  - inline in the first user message.

  Hold the role text, the goal, the model, and the settings constant.
  Repeat each cell several times, with and without mid-turn doorbells.

Other lead loop findings:

- A local 27B model can drive AMQ as lead and as worker: send, watch,
  drain, reply, and verification.
- Wake paste mode works with the pi TUI. A doorbell that arrives while pi
  is busy is queued as a user message in the running turn.
- Both models guessed `amq` argument forms (a positional `reply`, a
  non-existent `post`) and self-corrected through `--help`. pi-native
  `amq_send` / `amq_reply` tools in the package would remove this class of
  error.

## Exact commands

```sh
npm view @mariozechner/pi-coding-agent name version deprecated
npm view @earendil-works/pi-coding-agent name version
npm install @earendil-works/pi-coding-agent@1.3.0 --prefix ~/.amq-squad/spike/pi
ollama pull qwen3.6:27b
cd ~/.amq-squad/spike/s5/project && amq coop init --agents pi-1,pi-lead,user
cp spike/pi-config/models.json ~/.amq-squad/spike/s5/pi-agent/models.json
cp spike/pi-config/spi ~/.amq-squad/spike/s5/bin/spi
tmux -L s5spike new-session -d -s s5 -c ~/.amq-squad/spike/s5/project
# step 1
amq coop exec --root ~/.amq-squad/spike/s5/project/.agent-mail/collab --me pi-1 ~/.amq-squad/spike/s5/bin/spi -- --model ollama/qwen2.5:32b
AMQ_NO_UPDATE_CHECK=1 amq who
# step 2 (batch 1 used probe.sh; batch 2 and the recheck use predicate v2)
spike/piprobe/probe.sh 3 ollama/qwen2.5:32b ollama/gemma3:4b
spike/piprobe/probe.sh 3 ollama/qwen3.6:27b
node spike/piprobe/probe.mjs --recheck <batch-1 run dirs>
node spike/piprobe/probe.mjs --model ollama/qwen3.6:27b --runs 3
node spike/piprobe/probe.mjs --model ollama/qwen2.5:32b --runs 3
node spike/piprobe/probe.mjs --model ollama/gemma3:4b --runs 1
node spike/piprobe/ttft.mjs qwen2.5:32b 3; node spike/piprobe/ttft.mjs qwen3.6:27b 3 512; node spike/piprobe/ttft.mjs gemma3:4b 3
# step 3
node spike/pi-hold-ext/hold-test.mjs throw timeout deny mismatch
HOLD_TEST_APPROVE_AFTER_S=120 node spike/pi-hold-ext/hold-test.mjs approve
AMQ_SQUAD_HOLD_TIMEOUT_MS=600000 amq coop exec --root <root> --me pi-1 ~/.amq-squad/spike/s5/bin/spi -- \
  --model ollama/qwen2.5:32b -e spike/pi-hold-ext/hold.ts
touch <root>/agents/pi-1/extensions/io.github.omriariav.amq-squad/approve-<id>
# step 4 (one tmux window per seat; brief pasted with tmux paste-buffer -p)
amq coop exec --root <root> --me pi-1 ~/.amq-squad/spike/s5/bin/spi -- --model ollama/qwen3.6:27b \
  --session-dir ~/.amq-squad/spike/s5/sessions/pi-1 --append-system-prompt spike/pi-lead-loop/worker-role.md
amq coop exec --root <root> --me pi-lead ~/.amq-squad/spike/s5/bin/spi -- --model ollama/qwen3.6:27b \
  --session-dir ~/.amq-squad/spike/s5/sessions/pi-lead --append-system-prompt spike/pi-lead-loop/lead-role.md
python3 spike/pi-lead-loop/session-tools.py <session.jsonl>...
python3 spike/pi-lead-loop/reconstruct-run1.py <run-1 lead session.jsonl> <dir>
spike/pi-lead-loop/verify-go-test.sh <dir> <label> <log>
```

`<root>` = `~/.amq-squad/spike/s5/project/.agent-mail/collab`.

## Host state

- Everything this spike wrote is under `~/.amq-squad/spike/pi` (the pi
  install) and `~/.amq-squad/spike/s5` (project, root, sessions, runs,
  logs, gocache), plus the pulled Ollama model qwen3.6:27b.
- The seats, the wakes, and the private tmux server (`-L s5spike`) are
  stopped; no pi or wake process is left. `amq who` shows the seats as
  `active (recent_activity)` until that ages out.
- `~/.pi/agent/` (an empty `auth.json` and `models-store.json`) appeared at
  00:16:36.
  - Every pi run of this spike went through `spi`, which sets
    `PI_CODING_AGENT_DIR`.
  - This spike's config dir got the same two files at 00:08.
  - S3's spike agent dir got its files in the same minute as `~/.pi`, so
    `~/.pi` most likely comes from an S3 pi run without
    `PI_CODING_AGENT_DIR`.
  - It holds no credentials. The S3 agent handles it.

## Open questions for phase 4

1. **Provider isolation**, a 2b requirement under R16 (lead ruling,
   corrected after the Codex review).
   - What was observed: `GEMINI_API_KEY` is set in this shell. pi resolves
     provider keys from the environment
     (`pi-ai/dist/env-api-keys.js:86`: `google: "GEMINI_API_KEY"`), so
     `--list-models` showed Google models next to the Ollama ones with no
     configuration. The count of 23 is report-only. Nothing in this spike
     sent a prompt to Google, and nothing shows that data left the machine.
   - The shortcuts are not enforcement:
     - `--models` only scopes Ctrl+P cycling (`dist/cli/args.js:309`).
     - `PI_OFFLINE` only disables startup network operations
       (`dist/cli/args.js:336`).
     - A `tool_call` guard fires too late to stop prompt transmission.
   - Enforcement is two layers:
     - The wrapper (`seat-exec`) strips provider credentials from the seat
       environment and isolates config and credential storage. It strips
       `*_API_KEY`, `*_TOKEN`, `*_SECRET`, and known vendor variable names,
       unless the runtime declares a need for them. It also points the seat
       at its own config dir.
     - The package or runtime restricts providers, models, and endpoints to
       the seat's registry entry before any prompt is sent.
2. **pi pin:** 1.3.0 (the version tested here) or 0.85.1 (AMQ's pinned
   evidence)? The hook API used here (`tool_call`, `session_start`,
   `ctx.signal`, `ctx.ui.setStatus`) is the one the reference bridge uses.
   Re-run `hold-test.mjs` on the pinned version.
3. **`tool_execution_start` before the hook:** UX only (finding 1). Filed as
   "Revision 4 feedback" in ask 1 of `docs/v3-upstream-asks.md`.
4. **Approval-retry storms:** a cap per session or per turn on held calls,
   and what the model is told on a block. This interacts with the
   serialized batch approvals of finding 5.
5. **Native AMQ tools** in the `@amq-squad/pi` package (`amq_send`,
   `amq_drain`, `amq_reply` via `registerTool`) instead of bash. They would
   remove the argument guessing seen in both runs and give the guard a
   typed call to check.

## Codex review (reviewer-openai, xhigh)

Verdict: S5 accepted with edits (read-only review). Severity list, and
where each edit is applied:

1. Major: harden the probe predicate. Require confirmed tool execution,
   staged and untracked diff checks, an exact final RESULT line, and a
   confirmed read. Applied: predicate v2 in `probe.mjs`, batch 1 re-scored,
   batch 2 run fresh (Step 2).
2. Major: the qwen2.5 "false success" label was wrong. That run reported
   FAIL. Applied: zero false successes; qwen2.5:32b is 2/3 and unverified;
   the false edit claim is a reliability finding (registry rule).
3. Major: the R16 shortcuts are not enforceable (`--models`, a late
   `tool_call`, `PI_OFFLINE`). Applied: enforcement is credential stripping
   plus config isolation in the wrapper, and a provider, model, and endpoint
   restriction before any prompt. The 23 count is report-only (open
   question 1).
4. Minor: scope the revision 4 conclusion to the hold primitive, and list
   the integration items as untested 2b tests. `tool_execution_start`
   before the hook is UX only. Applied (Step 3).
5. Minor: correct the concurrency claim. Hooks are awaited sequentially
   while calls are prepared, and then the batch runs concurrently. Applied
   (finding 5).
6. Minor: `before_agent_start` was never tried, and the role-delivery
   comparison was not fair. Give the 2b matrix. Applied (role delivery).
7. Minor:
   - Archive the uncached go test logs and the raw TTFT. Applied
     (`S5-evidence/lead-loop-run{1,2}-go-test.txt`, `ttft-raw.jsonl`).
   - Label reasoning versus visible-text TTFT. Applied (`ttft.mjs` split).
   - Fix the per-run tool descriptions. Applied (batch 1 detail).
   - Remove the stray "." in the brief. Applied.
   - Note that AMQ has no completion lease, and that the doorbell's causal
     role is not isolated. Applied (D5 section).
8. Minor: append this section. Applied.
