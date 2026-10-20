PASS

# S5 report: pi seat on Ollama under `amq coop exec`, R12 probe, hold seam, pi lead loop

Date: 2026-10-21. Owner: pi-dev (Claude Code, Opus 5.5). Branch `spike/s5-pi`
(from `v3`). Criterion (docs/v3-phase1-plan.md S5): the probe passes on at
least one local model, the hold seam works, the three tasks complete. All
three hold:

| Criterion | Result | Evidence |
| --- | --- | --- |
| R12 probe passes on a local model | qwen3.6:27b 3/3, qwen2.5:32b 2/3 | `S5-evidence/probe-results.jsonl` |
| Hold seam | an extension can hold a tool call with no upper bound; allow, deny, timeout, abort, hash mismatch and handler exception all behave (the last four block) | `S5-evidence/hold-tests.jsonl`, `S5-evidence/hold-log-tui-pi-1.jsonl` |
| Three tasks via a pi lead | converged 2 of 2 runs with the v2 brief (qwen3.6:27b lead and worker); 1 earlier attempt with a weaker brief failed | `S5-evidence/lead-loop-*.txt`, `S5-evidence/amq-messages.txt` |

So 16.3 does not narrow: a pi package can implement bridge revision 4 tool
approval as an extension, without changing pi.

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

The brief's form `amq coop exec --root R --me pi-1 -- pi` is rejected by amq
0.102.0 ("command required"). The grammar is
`amq coop exec [options] <command> [-- <command-flags>]`:

```sh
amq coop exec --root ~/.amq-squad/spike/s5/project/.agent-mail/collab --me pi-1 \
  ~/.amq-squad/spike/s5/bin/spi -- --model ollama/qwen2.5:32b
```

`amq who` then listed `pi-1  active (notifier_live)`. `amq wake check --me
pi-1 --json` reported a live wake in `paste` mode, version 0.102.0,
`owner_bound: false`. Inside pi, `!env` showed `AM_ROOT`, `AM_ROOT_ID`,
`AM_BASE_ROOT`, `AM_ME=pi-1`, `AM_SESSION=collab`. pi 1.3.0 started its TUI
(`(ollama) qwen2.5:32b`, `0.0%/131k`). It warns that tmux `extended-keys` is
off; this did not affect the spike.

pi config (no file under `~/.pi` is used; see "Host state" below):

- `spike/pi-config/spi`: wrapper that sets `PI_CODING_AGENT_DIR=~/.amq-squad/spike/s5/pi-agent`,
  `PI_OFFLINE=1`, `PI_SKIP_VERSION_CHECK=1`, `PI_TELEMETRY=0`,
  `GOCACHE=~/.amq-squad/spike/s5/gocache`, then execs the prefix-installed pi.
- `spike/pi-config/models.json` → `$PI_CODING_AGENT_DIR/models.json`:
  provider `ollama`, `baseUrl http://localhost:11434/v1`,
  `api openai-completions`, dummy `apiKey ollama`, three models with
  `contextWindow 131072`; qwen3.6:27b has `reasoning: true`. No Modelfile or
  Ollama server change was needed because Ollama already sizes context to
  131072 here.

## Step 2: R12 probe

`spike/piprobe/probe.mjs` (driver `probe.sh`) copies `spike/piprobe/fixture`
(a Go module whose `TestGreeting` fails until `Greeting` returns
`"hello, world"`) into a fresh git repo per run. It runs
`pi --mode json --no-session --model <m> --tools read,edit,write,bash --no-context-files --no-skills --no-prompt-templates "<task>"`
and timestamps the JSON events. The task: read calc.go, make one stated
one-line edit, run `go test ./...`, report, and end with `RESULT: PASS|FAIL`.
The verdict is mechanical. A run passes only when all four of these hold:

1. An independent `go test ./...` after the run passes.
2. `git diff --numstat` is exactly `1 1 calc.go`.
3. The agent itself ran `go test` through bash.
4. The agent's last line is `RESULT: PASS`.

| Model | Run | Result | First event (ms) | First text (ms) | Total (ms) | Tool calls (`!` = error) |
| --- | --- | --- | --- | --- | --- | --- |
| qwen2.5:32b | smoke | PASS | 3231 | 11996 | 13142 | read, edit!, edit, bash |
| qwen2.5:32b | 1 | PASS | 2387 | 9139 | 10312 | read, edit!, edit, bash |
| qwen2.5:32b | 2 | FAIL | 2584 | 7128 | 10023 | read, edit!, bash! |
| qwen2.5:32b | 3 | PASS | 2811 | 10919 | 12104 | read, edit!, edit, bash |
| qwen3.6:27b | 1 (cold load) | PASS | 7926 | 15131 | 15791 | read, edit, bash |
| qwen3.6:27b | 2 | PASS | 276 | 8513 | 9158 | read, edit, bash |
| qwen3.6:27b | 3 | PASS | 238 | 8254 | 8897 | read, edit, bash |
| gemma3:4b | 1-3 | FAIL | n/a | n/a | 170-189 | none: Ollama 400 "gemma3:4b does not support tools" |

Direct time to first token against Ollama's streaming API
(`spike/piprobe/ttft.mjs`, text only, 3 runs, first run includes load if
not resident):

| Model | Run 1 (ms) | Warm (ms) |
| --- | --- | --- |
| qwen2.5:32b | 296 | 42-45 |
| qwen3.6:27b | 404 | 89-90 |
| gemma3:4b | 2375 (load) | 22-26 |

pi itself starts in 129-153 ms (spawn to session header).

Reading the numbers:

- "First event" is the first streamed assistant delta seen by pi. qwen3.6
  streams thinking, so it shows warm TTFT. qwen2.5 answers with tool calls
  only, and Ollama sends a tool call as one chunk when it is complete, so for
  qwen2.5 this column is the time to generate the whole first tool call (about
  2.4-2.8 s). A registry latency field should use the direct text TTFT, not
  pi's first event on a tool turn.
- qwen2.5:32b failure mode, in every run: it emits read, edit, and bash in
  parallel in one message, before it has seen the file. It guesses spaces
  instead of a tab, so the first edit fails. Twice the first edit was even
  malformed (bash arguments passed to edit). In run 2 it then reported
  "The edit was applied correctly" although the edit had failed. That is a
  false success report, and only the mechanical check caught it.
- qwen3.6:27b read first, edited once, tested once, with no tool errors in
  three runs.
- gemma3:4b is structurally ineligible: Ollama 0.34.4 refuses tools for its
  template. A registry probe should record this as `no_tools`, not as a
  quality failure.

R12 probe design recommendation: keep the four-part mechanical verdict.
Record per entry: pass count over N, tool-error count, and false-success
count (the agent says PASS but the verifier says FAIL). Run at least 3
times. Mark a model verified only with N/N passes and zero false successes.
By that rule qwen3.6:27b verifies and qwen2.5:32b does not.

## Step 3: hold seam (bridge revision 4 approval)

Verdict: **works**. A pi extension can hold a tool call for as long as it
wants and then allow or block it. Stock pi 1.3.0 awaits every `tool_call`
handler before it executes the tool (`dist/core/agent-session.js`
`_beforeToolCall` → `dist/core/extensions/runner.js` `emitToolCall`: a
plain `await handler(event, ctx)`, with no timeout and no race).
`docs/extensions.md` states it: "A `tool_call` handler failure blocks the
tool as a fail-safe."

Extension: `spike/pi-hold-ext/hold.ts`.

- It is active only when `AM_ROOT` and `AM_ME` are set, like the reference
  bridge.
- For tools in `AMQ_SQUAD_HOLD_TOOLS` (default bash, edit, write) it writes
  `<AM_ROOT>/agents/<AM_ME>/extensions/io.github.omriariav.amq-squad/pending-<id>.json`.
  The record carries the tool, input, `manifest_hash`, and `expires_at`.
  `<id>` is the tool call id.
- It then awaits `approve-<id>` (allow) or `deny-<id>` (block), a timeout
  (block), or `ctx.signal` abort (block).
- A JSON approve whose `manifest_hash` differs from the pending call is
  refused (block).
- It logs every hold and decision to `hold-log.jsonl` and shows a TUI status
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

Findings that bind the phase 4 bridge (revision 4):

1. pi emits `tool_execution_start` before the `tool_call` hook runs. A held
   call therefore already shows `tool_execution_start` in pi's event stream
   and in the reference bridge's `activity/` stream, while it has not
   executed. The revision-4 extension must write its `interaction` line at
   hold time. Consumers must not read `tool_start` as executed.
2. pi applies no timeout of its own. The extension must own `expires_at`
   and the timeout, or one lost answer stalls the session forever. The
   extension must also honour `ctx.signal`, or Escape cannot cancel a held
   call (tested: it can when the extension listens).
3. A throw blocks the call (fail closed), which matches plan 4.4. A throw in
   one extension's handler also skips later `tool_call` handlers, because
   `emitToolCall` does not catch per handler.
4. The model sees the block reason as the tool result. qwen2.5 obeyed "do not
   retry". A model that retries creates a new call id and a new pending
   record each time. The bridge needs a per-session cap or a back-off.
5. Not tested: parallel held calls from one assistant message (pi runs them
   concurrently; the per-id design should hold), and a local `ctx.ui.confirm`
   racing the remote answer (revision 4 "first decision from any face").
   Both are phase 4 tests.

## Step 4: lead loop (pi lead, pi worker, AMQ)

Setup: both seats run qwen3.6:27b under `amq coop exec` with the live wake
(paste mode) in one tmux session. The worker role
(`spike/pi-lead-loop/worker-role.md`) and the lead role (`lead-role.md`) are
passed with `--append-system-prompt`. The goal is three helpers (Max,
Reverse, IsEven), each in its own file with a table test.

Why both seats run qwen3.6: with one model per seat, Ollama would swap
weights. Only one of these models stayed resident at a time; loading gemma3
evicted the others. Ollama serves one request at a time (`-np 1`), so all
Ollama seats share one GPU slot, which supports the registry's single
Ollama concurrency pool.

| Attempt | Brief | Result |
| --- | --- | --- |
| 0 | role only in `--append-system-prompt`, goal as the user message (`lead-brief.txt`) | FAIL. qwen3.6 reasoned "I don't actually have access to any AMQ (Advanced Message Queuing) system or anything like pi-1. I should just create the files directly." I aborted it before it wrote anything. The role text was in the system prompt (checked in the session file). |
| 1 | role inline in the first user message, telling it `amq` is a CLI on PATH and to run `amq who` first (`lead-brief-v2.txt`) | PASS in about 5 min 20 s |
| 2 | same brief, fresh sessions, reset project | PASS in about 10 min 43 s, with one dropped task recovered by the lead |

Run 1 transcript (`S5-evidence/lead-loop-transcript.txt`), times UTC:

- 21:20:27 lead runs `amq who` (pi-1 active), reads the module.
- 21:20:42 lead runs three `amq send --to pi-1 --kind todo` in parallel. Each
  body names the files, the signature, and "reply when go test ./... passes".
- 21:20:42 the wake pastes `: AMQ doorbell run amq drain --include-body then act on it`
  into pi-1. pi-1 drains all three and writes six files at 21:21:32.
- pi-1 fixes an invalid Unicode escape and a failing reverse case
  (`go test` red twice, then green at 21:22:11).
- 21:22:28 `amq reply <id> "<text>"` fails ("reply does not accept positional
  arguments"). pi-1 reads `amq reply --help` and replies correctly with
  `--id/--body` at 21:23:21.
- Lead: `amq watch --timeout 300s` returns at 21:23:21. amq notes that watch is
  redundant with a live wake. `amq drain --include-body .` fails on the stray
  `.`, then it drains correctly. It runs `go test ./...` (ok) and reads all six
  files. It prints a summary and `CONVERGED`.
- The lead made no write or edit call. pi-1 made all writes.

Run 2 (`S5-evidence/lead-loop-run2-transcript.txt`):

- The lead sent the three tasks 11-19 s apart.
- The worker received the doorbells mid-turn as extra user messages. It
  drained Max first, and Reverse and IsEven 36 s later.
- It implemented only Reverse and IsEven: the second drain's output displaced
  the first task.
- It tried a non-existent `amq post`, recovered through `amq reply --help`,
  and replied to two of the three tasks.
- The lead waited through two `amq watch` timeouts. It checked the files and
  sent a follow-up ("FOLLOW-UP: Implement Max function (was not completed)").
  The worker did it, the lead verified `go test ./...` and converged.

Independent check after each run: `go test -count=1 -v ./...` ran
TestGreeting, TestIsEven, TestMax, and TestReverse, all PASS.

Lead loop findings:

- A local 27B model can drive AMQ as lead and as worker: send, watch,
  drain, reply, and verification. It needed a concrete inline brief. With
  only an appended system prompt, it read "AMQ" as Advanced Message Queuing
  and went solo. The built-in pi package's `before_agent_start` injection
  (plan 4.4) should carry a role that names the `amq` CLI and starts with a
  probe command, not just "talk over AMQ".
- `amq drain` consumes mail at once, with no lease or ack. A worker that
  drains two tasks and does one loses the other silently. Only the lead's
  file-level verification recovered it. This is direct evidence for the v3
  task store leases (4.5): dispatch must be lease-tracked, not mail-tracked.
- Wake paste mode works with the pi TUI. A doorbell that arrives while pi is
  busy is queued as a user message in the running turn. That is useful, but
  it is also how run 2 lost a task.
- Both models guessed amq argument forms (positional `reply`, `post`, a
  trailing `.`) and both self-corrected through `--help`. A pi-native
  `amq_send` / `amq_reply` tool registered by the package would remove this
  class of errors.

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
# step 2
spike/piprobe/probe.sh 3 ollama/qwen2.5:32b ollama/gemma3:4b
spike/piprobe/probe.sh 3 ollama/qwen3.6:27b
node spike/piprobe/ttft.mjs <model> 3
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
```

`<root>` = `~/.amq-squad/spike/s5/project/.agent-mail/collab`.

## Host state

- Everything this spike wrote is under `~/.amq-squad/spike/pi` (pi
  install), `~/.amq-squad/spike/s5` (project, root, sessions, runs, logs,
  gocache), and the pulled Ollama model qwen3.6:27b. The seats, wakes, and
  the private tmux server (`-L s5spike`) are stopped; no pi or wake process
  is left. `amq who` shows the seats as `active (recent_activity)` until
  that ages out.
- `~/.pi/agent/` (empty `auth.json` and `models-store.json`) appeared at
  00:16:36. Every pi run of this spike went through `spi`, which sets
  `PI_CODING_AGENT_DIR`, and this spike's config dir got the same two files
  at 00:08. S3's spike agent dir got its files at the same minute as
  `~/.pi`, so `~/.pi` most likely comes from an S3 pi run without
  `PI_CODING_AGENT_DIR`. Left in place for the owner. It holds no
  credentials.

## Open questions for phase 4

1. Provider leakage. `GEMINI_API_KEY` is set in this shell. pi lists 23
   Google models next to the Ollama ones with no configuration. A pi seat
   meant to be local-only can reach a vendor unless the launcher strips
   provider keys from the seat environment or the package restricts models
   (for example `--models ollama/*` plus a `tool_call`/provider guard). The
   privacy class in R16 needs one of these; which one?
2. Pin pi 1.3.0 (current, the one tested here) or 0.85.1 (AMQ's pinned
   evidence)? The hook API used here (`tool_call`, `session_start`,
   `ctx.signal`, `ctx.ui.setStatus`) is the one the reference bridge uses.
   Re-run `hold-test.mjs` on the pinned version.
3. Revision 4 with `tool_execution_start` before the hook (finding 1): ask
   upstream pi for a `tool_call_pending` event, or document it in the bridge
   protocol?
4. Approval-retry storms: a cap per session or per turn on held calls, and
   what the model is told on block.
5. Registry: R12 "verified" as N/N passes with zero false successes (this
   report's rule). Should qwen2.5:32b (2/3 with a false success) be
   `probation` or `unverified`? Should `no_tools` (gemma3) be a distinct
   state?
6. Lead role delivery: the brief worked inline but not as an appended system
   prompt. Phase 4 should test `before_agent_start` injection with the same
   qwen3.6 lead before relying on it.
7. Native AMQ tools in the `@amq-squad/pi` package (`amq_send`, `amq_drain`,
   `amq_reply` via `registerTool`) instead of bash. They would remove the
   argument guessing seen in both runs and give the guard a typed call to
   check.
