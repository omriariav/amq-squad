# Brief 3.1-31-2: the keys on the screen

Repository: /Users/omri/code/amq-squad (Go module github.com/omriariav/amq-squad/v3; AMQ pinned at github.com/avivsinai/agent-message-queue v0.116.2). Work ONLY in a fresh scratch worktree under $HOME: `git -C /Users/omri/code/amq-squad fetch -q origin && git -C /Users/omri/code/amq-squad worktree add /Users/omri/scratch-31-2 -b v3-31-2 origin/v3`, after brief 31-1 and t26 have merged into v3. Never touch the main checkout, .worktrees/, ~/.amq-squad, ~/.claude, ~/.codex, or the live session under /Users/omri/code/amq-squad/.agent-mail. Never push, amend or rebase. Never run the amq-squad binary from your branch against owner state, the real tmux server, the real AMQ or the real Buzz relay. `go build`, `go vet`, `go test` and `npm test` in pi/ are fine. No network, no live AMQ, no tmux in tests; the pi tests use pi/test/fakepi.ts, and the gate tests use the existing test relay and fixtures.

Authority: docs/v3.1-plan.md section 7 (the amendment of 2026-10-31, the owner's A) and 7.5's row for this brief. File:line references are at d5094b68 from the launch-path map of the day; brief 31-1 moves many of them, so re-read each site on the tip.

## Depends on

- **Brief 31-1:** the read-only screen in the lead's process, the status additions, squad.yaml as the one source, `up`.
- **t26:** its typed widen block is the widen key's surface. Without it there is no widen key; the key never parses a free-text request.
- **The answer path:** R55's terminal door for the four typed gates (below). The screen does not depend on it; the answer key does.

## The keys

Five keys on brief 31-1's screen. Each runs inside the lead's process: the extension's input path reads the key, never the model, and the key runs its CLI verb from the lead's own process, as the t16 tools do today (`amq_task_widen`, `amq_scheduler_relaunch`, `amq_task_reset`: pi/extensions/lib/tools.ts:266, 278, 302). So the scheduler's R4 check, a decision accepted only from a sender whose stamped parent chain reaches the recorded lead incarnation (internal/schedproc/decide.go:144-184, 264-340; internal/cli/scheduler_run.go:395-412), holds by construction (plan 3.4, 7.2 (a)). The model cannot press a key, and the lead's tool set still carries no gate-answer verb (ruling 3.1, R40).

1. **add.** A task: the owner types the title, the declared files and the pool; the key runs `task add` (the lead's `amq_task_add`, tools.ts:215). A seat: the key writes the squad.yaml line through `team apply` with its digest, and the new seat starts by the roster relaunch (`scheduler relaunch`, internal/scheduler/roster.go:276-342), since a fresh seat into a present binding is refused until AMQ's ask 20 (`binding_present_without_resumable_conversation`, internal/scheduler/roster.go:78-80; plan 7.9 question 5). The screen says that the add relaunches the roster and asks once before it does. The launch budget (plan 7.3) runs from this key press.
2. **widen.** On a task that t26's typed widen block holds: the screen shows the requested files and the reason from the block; the owner confirms; the key runs `task widen ID --files ... --reason ...` over the scheduler's socket, as `amq_task_widen` does (`task show --json`, then `task widen ... --json`).
3. **reset.** On a blocked task: the key runs `task reset`, with `--pool` when the owner picks another pool (the 3.0.2 re-pin rule: `repin_after_attempt` names this path).
4. **clear.** On a warning line that brief 31-1 put in place of a deleted refusal (a silent hook, a config change seen, an external include, a seat over the launch budget): the key records the owner's acknowledgement as a decision naming the warning and the seat's launch, and the line leaves the screen. The same condition on a later launch is a new line. A clear is never a gate and never changes a seat's state.
5. **answer.** On an open typed gate: R55's terminal door (below).

Each key shows its refusal line on the screen when its verb refuses, with the refusal name as the verb printed it.

## The answer path: R55's terminal door for the four typed gates

The four typed gates (3.0's 7.1: tag and release, which the release flow raises as the tag gate and the gh release gate; external send; credential change; growth beyond the grant) stay owner-signed through either door. This brief adds the terminal door beside Buzz; it changes nothing about what a gate binds or how an answer verifies.

- The screen's gates block lists each open typed gate with its kind, target and the 16-hex typed prefix. The answer key opens a prompt; the runtime's input path, not the model, takes the owner's typed line and writes the answer only when the line carries the gate's printed prefix and the verdict (R55's third door, plan 3.1).
- The answer verifies through the same verifier as a Buzz answer (internal/gateverify) and lands in the gate store (internal/gatestore) with its door recorded. A gate answered at one door is closed at the other: a second answer is refused as the store refuses one today.
- **Q7, the terminal answer's signature** (answer before code, in the execution note). A Buzz answer is signed by the owner's Buzz key. The pty `seat clear` wrote an issuer-signed record only from a real pty that is no tmux pane's (3.0 plan 4.3), and under `up` the lead runs in a tmux pane. Name which key signs a terminal answer and what proves the owner typed it, so that it verifies as owner-signed. If no answer meets that bar, the answer key shows the gate, posts it over Buzz and says so, and the terminal door waits for the reviewer's ruling. It is never weakened to fit.
- When the owner is away, Buzz stays the door, unchanged: the gate's question posts over Buzz as today, and the screen shows that the gate is waiting there.

## Rows (each fails when its change is reverted)

1. Each key runs its verb from the lead's process, and the scheduler accepts the decision under R4; the same verb sent from a seat's shell is refused (`origin_unknown`).
2. The model's tool list carries no key's verb that it lacked before, and no gate-answer verb (a row that fails when one is added).
3. add, task: a task added from the key appears in the store with its declared files and pool.
4. add, seat: the key writes the squad.yaml line through `team apply` with the digest and requests one roster relaunch; the confirmation is asked once; the launch budget's start stamp is the key press.
5. widen: on a fixture of t26's typed widen block the key widens with the block's files and reason; with no typed block there is no widen key.
6. reset: `task reset` runs; with a picked pool it runs `--pool`; a re-pin refusal shows on the screen by name.
7. clear: the acknowledgement is a decision; the line leaves the screen; a new launch with the same condition brings a new line; the seat's state does not change.
8. answer: a typed line with the gate's prefix and verdict writes an answer that verifies through gateverify; a wrong prefix writes nothing; a model tool call cannot write one; a gate answered over Buzz is closed on the screen and a second answer from the screen is refused.
9. Each of the four typed gate kinds is answerable at the terminal door under Q7's answer, or, if Q7 has no answer, each shows "waiting on Buzz" and no terminal answer is written.

Owner-run rows, with the exact commands: one typed gate answered at the screen; one answered over Buzz with the screen showing it closed; the launch budget from the add key on the day's record.

## Declared files

pi/extensions/ and pi/test/ (the keys and the prompt), internal/cli/ (task.go, task_widen.go, team.go, gate.go, gate_store.go and their tests, only where a verb needs a flag the key passes), internal/gatestore/ and internal/gateverify/ (the terminal door), internal/schedproc/ (the clear decision kind only), internal/status/ (the acknowledged warnings), docs/status-contract.md, README.md, README.html (regenerated), docs/v3-spike/31-evidence/. Anything else: stop and report; the lead widens.

## Rules

Briefs ruling 18: no test reaches the network; no test touches the real HOME, the real tmux server, the real AMQ or the real Buzz relay; canaries must not match redact.RealCredentialShapes; never print a credential value or a key. `go build ./... && go vet ./... && go test -count=1 ./...` and `cd pi && npm test` green, and `make ci` green before the report. Every change has a row that fails when reverted. Execution note at docs/v3-spike/31-evidence/31-2-keys.md: Q7's answer, the design (the extension's input path, the verbs each key runs), the rows, the owner-run rows.

Commits on v3-31-2 (several are fine; each message ends with these two lines exactly):
```
Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01DweoyBP65psWdJtwN3gdnu
```

## Report

Branch, head SHA, `git log --oneline origin/v3..HEAD`, `git diff --stat origin/v3...HEAD`, the test output, Q7's answer, the design summary, anything this brief got wrong.
