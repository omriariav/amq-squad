# amq-squad 3.0 plan, revision 2.5

Date: 2026-10-22. Status: the phase 1 spike has reported.
docs/v3-spike/spike-report.md holds the single five-criteria table. As of
that table: criterion 1 (S1) passed with the owner's real Buzz key on
2026-10-22 (gate g-owner-02, S1 report section 12); criterion 2 (S2)
mechanism passed, and the signature step is dropped by owner decision;
criterion 3 (S3) passed; criterion 4 (S4) passed on 2026-10-23 on AMQ v0.106.1 after upstream fixed issues #1157 and #1158 (filed from this spike) [2.5, S4 re-check]; criterion 5 (S6) passed for the Claude column,
and the Codex column is pending. S5 passed and answers 16.3. [2.5, spike report]
Planning only for 3.0; the spike code lives on spike branches.
Revision 1 (sections 1-16, with the two Opus 5.5 reviews and the routing
research) is the history behind this document. Revision 2.1 added section
16. Revision 2.3 integrated the spike amendments in place, tagged
"[2.3, amendment N]". Revision 2.4 integrated the day-2 owner rulings of
2026-10-22 the same way, tagged "[2.4, <source>]". Revision 2.5
integrates amendments 19 to 22 and the 2a-1 result, tagged
"[2.5, <source>]". Section 17 lists every change of each revision.

Amit, Taboola's pi distribution, is inspiration only. We do not copy its
code and we do not touch its repository.

## 1. Goal

amq-squad is the program that you start for any task. You type
`amq-squad` in a repository. One AI session starts. The session can start
a team. The team members run on Claude Code, Codex, Grok Build, or the
built-in pi with any model, including local models through Ollama. The
members send messages through AMQ. Several teams run in parallel. One
operator session, the mayor, speaks for the whole fleet and can start
teams on its own inside a grant. You answer five kinds of questions only.

amq-squad owns: roles, rules, gates, evidence, routing, the task queue,
worktrees, and the fleet view.

AMQ owns: launch, mail, wake, resume, liveness, routing between projects,
and remote access.

## 2. Principles

1. AMQ is the only transport. We call the AMQ launch API, `amq env`,
   `amq route explain`, the reserved extension namespace, and
   `amq-remote`. We never rebuild them.
2. We do not fork pi. The binary contains a pinned pi. A new pi version
   ships in a new amq-squad release.
3. Hooks give context and evidence. Hooks are not security. Security is
   credentials, branch protection, sandboxes, and signed gate answers.
4. Read-only by default. Each mutation shows a digest first. In auto mode
   a grant answers the digest. The four typed gates always wait for the
   owner. A merge to the default branch is a rules decision by the lead
   under the standing merge grant in 7.2. [2.4, S2 report owner decision]
5. Model and runtime are separate axes. A seat is runtime x model x
   effort. A role is a prompt. A seat is a process.
6. One file format. No compatibility with v2 files, except the status
   JSON contract, which is versioned and additive. `migrate` is a one-shot
   converter from v2 profiles, not a compatibility layer.
7. Deletion is a feature. 3.0 removes well over half of v2.

## 3. What the user sees

### 3.1 First run

You type `amq-squad` in a directory. The binary detects the installed
tools and writes the registry. If `.amq-squad/` is absent, the lead
starts in solo mode with the built-in rules and catalog. It writes nothing
inside the repository. It writes only under `~/.amq-squad/`. A directory without git gives solo mode only.

A quarantined launcher hangs on first start. Until the launcher ships
signed and notarized, the install paths are `go install` and brew only. [2.3, amendment 9]

### 3.2 Solo mode

The lead is the built-in pi with the amq-squad package. You work with it
as with any coding agent. The hooks inject the rules, record evidence,
and guard the four typed gates and the merge grant. [2.4, S2 report owner decision]

### 3.3 Team mode

You give a task. The lead decides: solo or team. For a team, the lead
proposes `squad.yaml` as a digest: seats, roles, runtimes, models, efforts,
worktrees, gates. The file is written only after approval. In manual mode
you approve the digest. In auto mode the grant
approves it. The seats start in tmux. The scheduler sends tasks. The
lead reviews and converges. The squad stops for you only at the four
typed gates. A merge inside the standing merge grant does not stop;
manual mode keeps the per-merge stop. [2.4, S2 report owner decision]

### 3.4 Modes

- Auto: the lead chooses the runtime, model, and effort per task. Seats
  start and tasks dispatch without a question. A standing grant sets the
  limits: maximum seats, cost per day, permitted runtimes, permitted
  privacy class.
- Manual: the team digest and each new seat need your approval. Dispatch
  to a running seat needs none.
- Grants and the mode live in the owner-only, guarded file
  `~/.amq-squad/grants.yaml`. A repository's `squad.yaml` can narrow them,
  never widen them. The flags `--auto` and `--manual` and the commands
  `/auto` and `/manual` are the owner's keystrokes; a lead cannot call
  them as a tool.

### 3.5 Fleet and the mayor

One operator session runs for the whole fleet. It is an amq-squad lead
with a fleet grant instead of a squad grant. It holds the fleet registry:
each squad, its root, its lead handle. Every squad lead sends gates and
status to the operator mailbox through AMQ cross-project routing.

In manual mode the operator session relays, answers lead questions, and
groups the open gates into one list. In auto mode it is the mayor: it
starts a squad on its own inside the fleet grant (maximum squads, cost
per day, permitted repositories), writes that squad's `squad.yaml`, and
runs the same digest flow a human would. The mayor never approves a
typed gate. Only the owner does, with a signature.

The operator session and a squad lead are the same program with
different grants. This is a design constraint, not an option.

### 3.6 Visibility

- Each seat is a tmux pane. AMQ creates it. You open it to watch or type.
- `amq-squad status` is the read-only table for one squad.
- amq-noc is the screen for the fleet. It renders the status contract.
- Buzz on your phone shows presence, activity, and approvals through
  `amq-remote`. One Buzz agent attaches to the operator session. One
  message from the phone reaches any lead through it.

## 4. Architecture

```
faces      tmux panes (AMQ) | amq-squad status | amq-noc (fleet) | Buzz via amq-remote
-------------------------------------------------------------------------------
amq-squad  Go binary: context / guard / evidence-ingest verbs, launch
           compiler, scheduler, task store, gates, registry, routing table,
           status contract, operator session and mayor
           built-in pi runtime + @amq-squad/pi package (lead and pi seats)
           adapters: Claude Code hooks plugin, Codex skill + sandbox flags,
           Grok Build and Cursor skills
-------------------------------------------------------------------------------
rail       AMQ: launchapi, mail, wake, route, extension namespace, amq-remote
-------------------------------------------------------------------------------
brain      pi core (pinned, unforked) | Claude Code | Codex | Grok Build | Ollama
```

### 4.1 The binary

One Go executable. It embeds a pinned pi runtime and the amq-squad pi
package. On first run it unpacks them to `~/.amq-squad/runtime/<version>/`
and verifies the digest. The lead and pi seats use this copy only, never
a pi from PATH. The package is a normal pi package; a user with own pi
can load it too.

The embedded pi is `@earendil-works/pi-coding-agent`. The old scope is
deprecated. It is pinned by version with a committed lockfile. The Node
floor is 22.19.0. [2.3, amendment 8]

Packaging is option (a): Node 22 plus the npm tree, fetched once with a
digest pin and built offline, with source maps and type files stripped.
Option (b2), the upstream Bun binary, is the only fallback. Option (b1)
is rejected as not faithful, and the own-Bun fallback is dropped. fd and
ripgrep ship as digest-pinned copies in a managed agent-bin inside the
payload, fetched once in build.sh. pi's tools-manager therefore never
reaches its run-time download, and nothing downloads at run time. [2.3, amendment 8]

The launcher sets PI_OFFLINE=1, PI_SKIP_VERSION_CHECK=1, PI_TELEMETRY=0
and PI_CODING_AGENT_DIR under `~/.amq-squad/pi-agent/<seat>` for the lead
and every pi seat. These flags do not isolate the network. Network
isolation stays a host concern (owner answer Q1). [2.3, amendment 8]

Launch-time integrity: cold install writes a manifest of path, size, mode
and SHA-256. Every launch verifies the manifest hash and every executable,
and checks every directory on the path to the entrypoint for symlinks.
Full file hashing runs only when the marker is absent or doctor asks. The
spike measured 43.5 ms median per warm launch and 190 ms for a full
re-hash. [2.3, amendment 8]

The pi package registers native `amq_send`, `amq_drain` and `amq_reply`
tools. It puts the role text in the first message and also in the system
prompt. The appended system prompt alone failed in the spike: the model
read AMQ as "Advanced Message Queuing". It restricts the model list to the
seat's registry entry. [2.3, amendments 11 and 12]

pi seats get a launch adapter upstream (issue #1153, ask 1). It is a
mint-mode adapter like Grok's: `pi --session-id <launch nonce>`, with a
pinned verified pi version. Resume fails closed. pi's own `--session-id`
creates a session that is missing, so the adapter checks that the
recorded session exists before it resumes, and refuses the launch
otherwise. The identity never comes from the bridge session id, which
appears after start and changes on `/new` and on a fork. Until the
adapter ships, pi seats start through `amq coop exec` (4.3). [2.5, amendment 20]

### 4.2 The three verbs

Every hook that exists calls the same three commands. Hooks exist in the
built-in pi and in Claude Code only; Codex, Grok Build, and Cursor have no
in-session hooks:

- `context --me <handle>`: role, rules digest, current task, open gates.
- `guard`: a verdict for one tool call: allow, needs gate, deny.
- `evidence ingest`: the hook payload on stdin, appended to the ledger.

They are Go, non-interactive, and fast, because Claude Code hooks call
them on every tool use.

### 4.3 Seats and the launch API

`squad.yaml` compiles to one AMQ launch request. Claude Code and Codex
seats start through the launch API with the wrapper `amq-squad
seat-exec`, installed outside the project. Model, effort, and the Codex sandbox and
approval flags travel as AMQ provider arguments in the launch request, so
they enter the trust digest. The wrapper takes only `--seat <handle>`;
there is no `--root` flag, and `AM_ME` and `AM_ROOT` come from the
environment. [2.3, amendment 7] It
writes the launch record and the role into the seat's extension directory,
sets the seat environment, and execs the provider in place. It never adds
provider flags. The Claude Code hooks come from a user-scope plugin that
reads the seat environment; the Claude grammar has no plugin or settings
flag. The hook ships as that user-scope plugin, never as a project-scope
stub. [2.3, amendment 7]

The admitted argument forms come from upstream. AMQ derives
`AllowedArgumentForms` from its own validation tables and settles the
`grammar_version` rule (issue #1154, ask 4). Once that ships, `seat-exec`
stops carrying its own list of forms and uses the advertised ones. [2.5, amendment 20]

The role comes only from `role.md` in the seat directory. `initial_input`
carries at most a pointer to it. Role text in `initial_input` without
`role.md` is a malformed launch, and the seat stops. [2.3, amendment 2]

The AMQ trust digest binds the wrapper path and argv. It does not bind
the wrapper binary bytes or the `role.md` bytes. Content integrity of
`role.md` is amq-squad's job. The Go core records the SHA-256 of
`role.md` in the seat's launch record when it writes the file. The record
lives under the AMQ root, outside the project tree. `seat-exec` recomputes
the hash before exec and refuses to start on a mismatch. Wrapper binary
integrity is the same trust as the `amq` binary itself: an absolute path
outside the project, on the owner's filesystem. We accept this; we do not
solve it. [2.3, amendment 2]

`seat-exec` anchors directory traversal to the verified AMQ root and
follows no symlink in the initial path. It checks the ownership and
permissions of directories that already exist. These fixes land before
its first use in phase 2b. [2.3, amendment 7]

`seat-exec` execs the provider with a scrubbed environment. It removes
`*_API_KEY`, `*_TOKEN`, `*_SECRET` and the known vendor variable names,
unless the seat's runtime declares that it needs one. In the spike, pi
offered 23 Google models because GEMINI_API_KEY was set in the owner's
shell. The S4 report states whether a Claude seat under the wrapper
inherits the full parent environment. [2.3, amendment 12] The S4 closure
run answered it: the seat process environment is the tmux server's
global environment plus the AMQ overlay, so the scrub holds at process
level. Provider tool shells source the owner's shell profile, which
added `GEMINI_API_KEY` inside the Codex seat's shell tool. `doctor`
therefore checks the owner's shell profile for exported `*_API_KEY`,
`*_TOKEN` and `*_SECRET` names and warns, and the pi package's tool
shell runs without the profile. `SSH_AUTH_SOCK` reaches both seats and
is a phase 2b decision. [2.4, amendment 17]

`seat-exec` denies writes to the owner's global agent config,
`~/.claude/settings.json` and `~/.codex/config.toml`. `doctor` diffs
that config against the preflight copy. Evidence: S6 finding B, where
`~/.claude/settings.json` changed during the run at 06:56, unattributed. [2.4, amendment 18]

A per-seat CODEX_HOME is rejected; the env overlay allowlist cannot carry
it. [2.3, amendment 4] codex-cli rewrites its own `config.toml` at start
(`[marketplaces.*] last_updated`). The docs say that codex-cli owns that
file and amq-squad never writes it. [2.4, amendment 17]

A fresh codex-cli seat stops on its folder-trust prompt until the project
is trusted in the owner's Codex config. amq-squad never writes the
owner's Codex config and never answers the prompt for the owner. Phase 2b
adds an owner-run `amq-squad trust <repo>` step and a doctor check that
lists untrusted roots before a Codex seat launches. [2.3, amendment 1]

A seat never commits. A Codex seat under `--sandbox workspace-write`
cannot write `.git`. This was measured on codex-cli 0.163.0 in a linked
worktree and in a plain checkout, with and without `--add-dir`
(`docs/v3-upstream-asks.md`, the `--add-dir` probe). The harness commits
and pushes the seat branch: `seat-exec` does it on the seat's behalf
when the seat reports done, or the lead does. The seat only changes the
tree. That harness-side commit is where the Go core enforces the
task-declared file set (7.2): it sees the tree diff before it commits,
and it refuses a file outside the task. The refusal has a path: it is
reported to the lead; the lead may widen the task's declared file set as
a recorded lead decision, inside the grant's limits (protected paths and
the size cap still apply); the harness then commits; a widening that
would cross a grant limit escalates to the owner-signed gate. The seat
never widens its own set. [2.5, amendment 21] Claude seats take the same path,
although Claude can commit: one commit path, not two. `seat-exec` execs
the provider in place, so the commit runs as its own harness step after
the done report; the exact shape of that step is a phase 2b design
item. [2.5, amendment 21]

pi seats start through `amq coop exec` until AMQ ships a pi adapter.
They get no digest-gated apply, resume, or liveness from AMQ. The status
says so. The adapter is the mint-mode form in 4.1 (issue #1153). Once it
ships, pi seats start through the launch API like Claude and Codex
seats. [2.5, amendment 20]

amq-squad never creates, moves, resizes, focuses, or closes a pane.
tmux is the substrate. cmux and Ghostty come from AMQ launchers. iTerm2
uses `tmux -CC`.

### 4.4 Hooks per runtime

| Runtime | Inject | Guard | Evidence |
| --- | --- | --- | --- |
| built-in pi | before_agent_start | tool_call, fails closed | tool_result |
| Claude Code | SessionStart, UserPromptSubmit | PreToolUse deny; fails open on timeout, on non-zero exit without a deny, and on a missing command [2.3, amendment 3] | PostToolUse |
| Codex | AGENTS.md marker and skill, at launch only | sandbox and approval flags at launch | AMQ capture or Codex session files; AMQ owns the notify hook, and a fan-out script as the notify command chains a second receiver (16.8) [2.5, amendment 20] |
| Grok Build, Cursor | skill, at launch only | none in session | none in session |

Where a hook cannot block, the documentation says so. Security does not
depend on it.

The S4 fault table is the evidence. A deny blocks in 0.17 s. A timeout
fails open after exactly the configured timeout, 5.11 s in the run. Exit
1 fails open in 0.36 s, and a missing hook command in 0.44 s. Enforcement
stays in the Go core; hooks are UX and evidence. [2.3, amendment 3]

In the built-in pi the hold seam works with no pi change and fails
closed. pi 1.3.0 emits `tool_execution_start` before the hook runs, so a
held call shows as started; this is revision 4 feedback in upstream ask
1. pi awaits hooks one at a time, so one held call delays the approval of
later calls. `before_agent_start` injection, several held calls in
parallel, and a local confirm racing a remote answer are phase 2b tests. [2.3, amendments 11 and 15]

### 4.5 Scheduler and task store

The scheduler is a deterministic Go process, one per squad. It holds the
task queue, the leases, the retries, and the pools keyed by runtime x
model x effort. It gives each task a lease. It sends the task to an idle
seat with the chosen key, or starts one inside the grant. An idle seat
closes after a timeout. The lead does not poll tasks. The lead
decomposes, labels each task with a class, prioritizes, and converges.

Dispatch is tracked by task leases, not by mail. Evidence from S6 (2026-10-22): task N4 was
assigned to a seat that never claimed it, sat for three hours, and the lead parked; an
unclaimed assignment returns to the pool on lease expiry and the lead reassigns. In the S5 lead loop,
drain consumed mail with no lease, and a doorbell in the middle of a turn
dropped a task. This is direct evidence for D5. [2.3, amendment 11]

The task store is the simplified v2 store: five states, dependency
gating, file locks, plus leases and pools.

### 4.6 Status contract

`amq-squad status --json` is a versioned contract. It carries:
`schema_version` with additive minors, `producer_version`,
`generated_at`, root and session; per seat: handle, role, runtime,
model, effort, state from a closed list plus `unknown`, reason code; an
`attention[]` list with stable ids, kind, severity, subject, since, and
a suggested action as argv plus an action id, never a shell string; per
task: id, state, lease holder, lease expiry, pool; gates with id, action
class, target digest, state; and a `capabilities[]` list. A golden
fixture in amq-squad CI is vendored by amq-noc. The five-state
classifier (needs-you, blocked, waiting, online, stale) and the
attention queue move from amq-noc into amq-squad.

### 4.7 amq-noc

amq-noc 1.0 reads the status contract and AMQ primitives only. It never
reads `.amq-squad/` files and never composes amq-squad commands. Its
controls are three: copy the action from the contract, open a
conversation with a lead over the AMQ thread, and show a gate. Gate
answers follow the signed path. It keeps the fleet registry view and the
action log.

## 5. Registry and detection

First run and `doctor` probe PATH for `claude`, `codex`, Grok Build,
Cursor, `ollama`, `amq`, `amq-remote`, `tmux`, `gh`. They read versions,
compare them with the versions the AMQ launch API verified, read the
login state without a login, list the Ollama models, read RAM and GPU,
and measure the time to first token per entry. `doctor` also lists
project roots that codex-cli does not trust, before a Codex seat
launches. [2.3, amendment 1]

`~/.amq-squad/registry.yaml` holds one entry per runtime x model x
effort with: verified, cost tier, privacy class (local or vendor), quota
pool (API and subscription are separate pools; Opus 5.5 has its own API
bucket), concurrency cap (all Ollama entries share one GPU pool),
untrusted_input flag, probation flag, measured latency. A local entry
gets `verified` only after a tool-call and edit probe passes. The lead
proposes seats from verified entries only. A missing tool is one status
line, not an error.

The seat cap is a grant field next to these registry caps. The default
maximum is 12 seats per session. The owner changes it in
`~/.amq-squad/grants.yaml` (R16), and a repository's `squad.yaml` can
narrow it. RAM is not the binding limit on the reference host: S6 peaked
at 4.2 GB for 6 seats, and 12 seats extrapolate to 8.4 GB typical and
14 GB worst on 64 GB. The usage-limit signal is the enforced stop: after
the first usage-limit event in the window, the scheduler starts no new
seat. The first 12-seat run is measured and labelled before the default
moves. [2.5, amendment 22]

The registry checks a Codex model id against the pinned codex-cli
version before launch, not after the first turn, because Prepare accepts
any model id. In the S4 closure run the 0.147.0 capture pin refused
`gpt-6-astra` (needs a newer codex) and `gpt-5.5` (refused on a ChatGPT
account), while `gpt-5.6-sol` worked. [2.4, amendment 17]

"Verified" for a local model means N of N probe passes with zero false
successes. N is 3 in the spike and 5 before a registry entry ships. [2.3, amendment 10]
The probe predicate requires confirmed tool execution, staged and
untracked diff checks, an exact final line, and a read. A false claim in
the middle of a run that ends in RESULT: FAIL is not a false success. [2.3, amendment 15]

Spike results: qwen3.6:27b is verified at 3 of 3. qwen2.5:32b is "2 of
3, unverified" and stays on probation after one false success. gemma3:4b
is in a "no tool calls" state and never gets implement or script work. [2.3, amendments 10 and 15]

Registry latency uses direct text TTFT. On a tool-call turn Ollama returns
the whole call as the first event, so that number is not a first token. [2.3, amendment 10]

## 6. Routing

### 6.1 Classes

The lead labels each task with one class from a closed list when it
decomposes the work. This is the only AI step in routing. Each class has
a verifier.

| Class | Verifier | Tier | Rule |
| --- | --- | --- | --- |
| extract | schema | smol | structured output, checkable |
| summarize | none | smol | consumers get references only and reread the source |
| script | exit code | smol | cascade allowed |
| implement | tests | task | escalates to plan with more than 3 files, no tests, or a sensitive path |
| plan | none | plan | never routed down |
| review | none | advisor | never routed down; reviewer family differs from implementer family |

A task routes below its tier only when its verifier is mechanical. Work
with no verifier never starts cheap.

### 6.2 Chains

Each tier is an ordered list of (runtime, model, effort). The scheduler
takes the first entry that passes four checks: verified, privacy class,
concurrency cap, cost cap. No passing entry: the task waits and the
status names the reason. Local-only paths never go to a vendor.

Default chains, version 1 (from the routing research; adjust from the
log):

- smol: pi + ollama qwen3.6:35b-a3b low (after probe); pi + ollama
  qwen3.6:27b low (verified 3 of 3 in the spike; 5 of 5 before it
  ships [2.3, amendment 10]); pi + anthropic haiku 5.5 low (API pool);
  Claude Code haiku low (subscription pool); Codex gpt-5.4-nano low.
- task: Claude Code sonnet medium; Codex gpt-5.4 medium; pi + anthropic
  sonnet 5.5 medium; pi + ollama qwen3.6:27b high on probation, never
  first.
- plan: Claude Code opus high; Codex gpt-5.5 high.
- advisor: Codex gpt-5.5 xhigh when the implementer was Claude; Claude
  Code opus xhigh when the implementer was OpenAI; Grok Build as a third
  family after a probe.
- max effort is never a default.

Escalation rules: input above 32k tokens skips Ollama; input above 100k
tokens skips Haiku (price step x5, verified 2026-10-20); a retry moves
one tier up and never repeats the entry; the tier is fixed per task,
never switched per turn.

Cost alone no longer justifies local models. Haiku 5.5 reads 30k tokens
for about 0.003 USD. Local models serve privacy and spare capacity.

### 6.3 Where the table lives

A default table ships in the binary. `~/.amq-squad/routing.yaml`
overrides it for the user. `.amq-squad/routing.yaml` overrides it for a
repository and maps path globs to a required privacy class. The registry
decides what is usable on this machine. The table is machine-independent.

### 6.4 Exploration, the log, and Jev

- `exploration_share` in the routing file, default 0.05, zero disables.
  That share of tasks runs one tier away from the table, never plan or
  review. The log records the share in force.
- The outcome log has one row per attempt: task, attempt, class,
  features, table tier, used tier, entry, skipped entries with reasons,
  policy (table, explore, jev_shadow), propensity, tokens, cost, wall
  time, wait time, outcome, verifier result, review findings, consumed
  smol outputs.
- A weekly report per class, tier, and size band shows redo-at-higher-tier
  rate, dominated cost against one tier up, wait time with skip reason,
  and review findings traced to smol outputs. It proposes one table
  change per bucket. The owner approves each change.
- Jev (TypeSafe) is optional, behind a flag. It runs in shadow on every
  task. It may receive the task text (owner decision Q6) but never
  command arguments or secrets, and it never makes a security decision.
  It replaces a chain only when its confidence interval beats the table
  on the log.

## 7. Gates and security

### 7.1 The four typed gates

These always stop and wait for the owner: tag and release, external
send, credential change, growth beyond the grant. A typed gate binds one
action to one target digest. Merge to the default branch is not a typed
gate. It is a rules decision by the lead under the standing merge grant
in 7.2 (owner ruling 2026-10-22 07:12-07:15, S2 report). [2.4, S2 report owner decision]

### 7.2 Standing grants

Everything else is a standing grant per session: seat-owned feature
branches, push to seat branches, open PR, run tests, start seats inside
the composition budget. The session grant's maximum seats defaults to 12.
The guarded grants file sets it, and `squad.yaml` can only narrow it.
After the first usage-limit event in the window no new seat starts
(section 5). [2.5, amendment 22] The fleet grant adds: maximum squads,
cost per day, permitted repositories.

Merge to the default branch is a rules decision by the lead under a
standing merge grant set once per repository. The grant's rules: CI
green, the change inside the task scope, no protected-path change, a
size cap, no force push. The Go core enforces the grant's limits
(repository, base branch, CI green, size cap, no force push, no
protected-path change). The lead decides inside them. A merge outside
the grant is refused by the core, not by the lead's judgment, and that
refusal escalates to an owner-signed gate. The model is never the
enforcement point. Manual mode keeps the per-merge stop; auto mode is
the default. [2.4, S2 report owner decision] In 3.0 the core enforces
the six named limits at the merge. Task scope is enforced earlier, at the
harness-side commit (4.3): the Go core refuses a seat diff outside the
task-declared file set before it is committed, and the refusal goes to
the lead, who may widen the set inside the grant (4.3). Evidence: S6
task W5, a file outside its task. [2.5, amendment 21]

### 7.3 Enforcement outside hooks

- Seats hold no credentials for the default branch or for releases.
- Branch protection on the server requires CI and refuses direct push.
  It stays; it is what makes the lead's merge safe to automate. The
  merge grant in 7.2 is the review, and no signed gate is needed for a
  merge inside it. There is no bot identity; the owner owns every GitHub
  identity, and seats push with it to seat branches only. S2 measured
  this: a direct push to `main` was refused (GH013) and the seat's PR
  merged under the owner's identity after the CI check. [2.4, S2 report owner decision]
- Codex seats launch with `--sandbox workspace-write` and
  `--ask-for-approval on-request`, both admitted by the AMQ launch grammar.
  A Codex seat launches only into a project root that the owner has
  trusted in codex-cli; amq-squad never answers the folder-trust prompt. [2.3, amendment 1]
  A `workspace-write` Codex seat never commits. The sandbox refuses every
  `.git` write, in a linked worktree and in a plain checkout, with and
  without `--add-dir` (codex-cli 0.163.0, `index.lock: Operation not
  permitted`). The harness commits and pushes the seat branch (4.3), and
  ask 5 is withdrawn. [2.5, amendment 21]
- Merges go through the merge train under the standing merge grant: the
  lead decides an ordered set of PRs inside the grant, and a deterministic
  merge train lands it. The batch digest over the ordered (PR, head SHA)
  pairs plus the base SHA is still computed and written to the audit log;
  nobody signs it. A rebase that changes a tree recomputes the digest and
  re-runs the rules check. The merge train runs under the owner's
  identity; no seat process holds a merge path to the default branch.
  GitHub's merge queue is not available on user-owned repositories (S2
  measured a 422 on the `merge_queue` rule), so the train is our Go
  loop. [2.4, S2 report owner decision]
- Gate answers are authenticated. The owner signs the answer with the
  Buzz key (BIP-340) over the gate id, the action class, and the target
  digest. `amq-remote` and Buzz carry the request and the answer. The Go
  core verifies the signature and records the result in an operatorauth
  envelope. No model process holds the owner key. An answer relayed by a
  model is invalid. The Claude permission path in `amq-remote` covers
  single tool approvals only. On a mailbox binding, AMQ labels the
  prompt with `nostr:<eventID>` (issue #1155, ask 8). The layer reads the
  event from the relay and runs the Claude-hook checks itself: pinned
  owner and body key, DM channel, gate e-tag or thread, nonce, window,
  edit and delete history, single use. The S1 verifier already does
  resolve, fetch and verify, so the gate transport is confirmed, not
  changed. [2.5, amendment 20]
- The gate text says "reply in this thread", and thread binding stays
  on. A plain DM answer is never accepted as the "only candidate". When
  the core sees a non-thread answer that matches an open gate (gate id,
  action class, prefix, owner pubkey), it re-posts "reply here to
  confirm" in the gate thread, and the next thread reply is the answer.
  Evidence: the S1 owner run, where g-owner-01 was rejected
  `not_in_gate_thread` and g-owner-02 passed in the thread. [2.5, amendment 19]
- The typed digest prefix stays 16 hex characters, for the owner's
  thumbs. Binding closes the collision game, not length. The Go core
  writes the gate record under the AMQ root before it sends the question.
  The record holds the full target digest and is immutable. The verifier
  compares the typed prefix against that record only. The action after an
  approval runs against the record's full digest, never against anything
  a seat presents later. [2.3, amendment 13]
- Verifier conformance rules: an explicit s < n check on the Schnorr
  signature; strict event decoding that rejects invalid UTF-8, duplicate
  keys and null tag elements; a presentation window of created_at - 60 s
  <= now <= not_after; CR and LF rejected anywhere in the answer; the
  envelope written first, then the consumed mark, both fsynced. [2.3, amendment 13]
- Same-UID writes to the ledger are the host threat model on a single-user
  Mac. We accept them for phase 1. [2.3, amendment 13]
- Hook configuration files and rules files are guarded paths. A seat
  cannot edit them.

## 8. Scale and host

- Host for 3.0: the Mac. `doctor` warns when sleep is permitted or the
  login service for `amq-remote` is absent. A box recipe (Hetzner or Mac
  mini with Tailscale, or Cloudflare Tunnel plus Access) is a docs item.
- First target: 3 to 4 squads in parallel, 3 to 4 seats each, about 12
  live seats on 64 GB. Tasks beyond that wait in the queue. One resident
  local model, about 28 GB, fits next to the seats.
- Per-provider concurrency caps come from the load test, not from
  guesses. The load test records 429 and usage-limit events per provider.
  The session seat cap defaults to 12, and the usage-limit signal, not
  RAM, is the enforced stop (section 5). [2.5, amendment 22]
- Tasks inside a squad can be coupled. Squads are independent. The mayor
  partitions work by repository.

## 9. Remote access

`amq-remote` plus Buzz is the remote face of 3.0. It is the only option
with owner-signed approvals. Buzz attaches to the operator session.
SagiMedina/handoff (tmux mirror on the phone over Tailscale) composes for
free on the same tmux and is a docs recipe. Grok Bot as a mesh peer and
the Slack ring adapter stay possible through AMQ and are not in 3.0.

amq-remote 0.102.0 has no phone-side grant signing. Enrollment needs the
Buzz nsec once in `amq-owner-sign`, written nowhere. [2.3, amendment 14]
Secret rule: the nsec reaches the signer only through a password-manager
pipe into `amq-owner-sign`. The same command with its output on a
terminal, in a log or in a file is not allowed. [2.5, amendment 19]
Phone-side signing stays upstream ask 10. The interop finding on C0
escaping in event ids is upstream ask 11, filed as issue #1156. [2.5, upstream asks]
The owner answered the enrollment question with the S1 owner run of
2026-10-22, which passed with the real key (S1 report section 12). [2.5, amendment 19]

Enrollment defaults to `--enable buzz-profile --presence`. The body needs
a Buzz profile before the owner can find it in the new-message picker,
and the cost is one extra owner signature at enrollment. Gates follow
the thread rule in 7.3: the gate text says "reply in this thread", and a
matching answer outside the thread gets a re-ask in the thread. [2.5, amendment 19]
A gate answer on a mailbox binding arrives as a `nostr:<eventID>` label,
and the layer verifies the event against the relay (7.3). [2.5, amendment 20]

## 10. What 3.0 deletes from v2, and what stays

Deleted: tmux pane driver and harness, runtime control, console, wizard
state machine, iTerm2 and Terminal.app backends, wake mirroring, coop
provisioning shim, most of doctor, the legacy launch driver.

Stays, simplified: typed gates and operator auth, role catalog, rules,
brief drafting as an off-path verb, worktree per seat, task store with
leases, launch intent compiler, release gate flow.

Moves in from amq-noc: the five-state classifier and the attention queue.

## 11. Upstream asks

The ask texts, evidence and duplicate checks live in
`docs/v3-upstream-asks.md`, verified against AMQ v0.102.0. That file is
the source of truth. The table below copies its status table. Nothing is
filed until the owner approves. [2.3] Seven asks were filed on 2026-10-22
under the owner's login, after both planning sessions reviewed the
drafts: ask 1 = #1153, 4 = #1154, 8 = #1155, 11 = #1156, 12 = #1157,
13 = #1158, 14 = #1159. Ask 5 is withdrawn, ask 7 is dropped, asks 2, 3,
6 and 9 need no action, and ask 10 waits for the phone-side signing
decision. Asks 12 and 13 were re-checked on AMQ v0.104.0 and are still
open. [2.5, upstream asks]

| Ask | Title | Status at v0.102.0 | Recommendation |
| --- | --- | --- | --- |
| 1 | pi adapter in the launch API | not shipped | filed as #1153: mint-mode adapter, fail-closed resume (4.1) [2.5, amendment 20] |
| 2 | Provision under a project with no .amqrc | partly shipped | skip, or file as docs fix |
| 3 | Non-interactive consent by digest | shipped | do not file |
| 4 | Advertise admitted Claude and Codex argument forms | not shipped | filed as #1154: upstream derives the forms from its validation tables (4.3) [2.5, amendment 20] |
| 5 | Codex argv grammar v2 | not shipped | withdrawn: `--add-dir` was measured not to fix the commit, and the harness commits (4.3) [2.5, amendment 21] |
| 6 | iTerm2 launcher backend | not shipped | do not file |
| 7 | Notify chaining for Codex | partly shipped | dropped: declined by the maintainer; a fan-out script is the notify command (16.8) [2.5, amendment 20] |
| 8 | Forward the owner-signed event | not shipped | filed as #1155: `nostr:<eventID>` label, and the layer verifies against the relay (7.3) [2.5, amendment 20] |
| 9 | Codex capture version floor | shipped on main (PR #1108 merged 2026-10-21; AMQ v0.104.0) | no action [2.4, ask 9 shipped] |
| 10 | Phone-side signing of the share grant | not shipped | file after owner decision |
| 11 | Event id escaping of C0 controls differs from NIP-01 | not shipped | filed as #1156 [2.5, upstream asks] |
| 12 | Capture-mode Codex conversation is finalized `stale` at Apply, so `__codex-notify` never records | confirmed open on v0.104.0 | filed as #1157 (S4 closure) [2.5, upstream asks] |
| 13 | Claude mint-path conversation record hardcodes `backend: commands` | confirmed open on v0.104.0 | filed as #1158, minor [2.5, upstream asks] |
| 14 | `launchapi` resolves the AMQ executable with `os.Executable()`, so an embedding binary cannot use a managed backend | not shipped | filed as #1159 (S6 launch blocker) [2.5, upstream asks] |

Ask 4 carries the S4 finding: Claude capabilities advertise only
`--allowedTools`, `-n` and `--name`, while `--model` and `--effort` are
accepted. The Codex grammar has the same gap. [2.3, amendment 5] Ask 1
also asks upstream to re-verify the pi bridge against
`@earendil-works/pi-coding-agent`, because AMQ's own pi evidence pin is
0.85.1. [2.3, amendment 8] The maintainer widened ask 4 to Claude
`--permission-mode`, Codex `--search`, the Grok flags and Cursor
`--model`. [2.5, amendment 20]

## 12. Requirements

Core and launch
- R1 One Go executable with a pinned pi runtime and the amq-squad pi package; the pi is `@earendil-works/pi-coding-agent` pinned with a committed lockfile on Node 22.19.0 or later, packaged as option (a) with (b2) as the only fallback, with fd and ripgrep embedded as digest-pinned copies, no run-time download, and a launch-time integrity check against the cold-install manifest. [2.3, amendment 8] The upstream pi adapter pins a verified pi version (issue #1153); the embedded pi stays on a version that adapter verifies. [2.5, amendment 20]
- R2 The lead and all pi seats use the built-in pi only.
- R3 Claude Code and Codex seats start through the AMQ launch API with the wrapper `seat-exec`.
- R4 Model, effort, and sandbox flags are AMQ provider arguments; the wrapper takes only `--seat <handle>`, sets the seat environment and the role, and execs the provider in place. The role comes only from `role.md`, whose SHA-256 the Go core records in the launch record and `seat-exec` checks before exec [2.3, amendment 2]; `seat-exec` anchors traversal to the verified AMQ root and checks directory ownership and permissions [2.3, amendment 7]; a per-seat CODEX_HOME is rejected [2.3, amendment 4]; amq-squad never writes the owner's Codex config, and `amq-squad trust <repo>` is an owner-run step [2.3, amendment 1]. A seat never commits: the harness commits and pushes the seat branch (`seat-exec` on the seat's done report, or the lead), for Claude and Codex seats alike, and the Go core enforces the task-declared file set on that commit. [2.5, amendment 21]
- R5 pi seats start through `amq coop exec` until an upstream pi adapter exists. The adapter is mint mode (`pi --session-id <launch nonce>`), pins a verified pi version, and resumes fail closed: it checks that the recorded session exists before a resume, because pi's own `--session-id` creates a missing one (issue #1153). [2.5, amendment 20]
- R6 amq-squad never creates, moves, resizes, focuses, or closes a pane.
- R7 First run without `.amq-squad` starts solo mode and writes nothing inside the repository; it writes only under `~/.amq-squad/`.
- R8 The first team proposal shows one digest for all files it creates; nothing is written before approval.
- R48 The Go core surfaces the AMQ `ApplyResult` outcome, reason code and failure detail to the operator. A count of applied commands is never the only signal. Evidence: spike log 08:08, where 2.31 read only `Commands`, reported "0 applied command(s)" and hid `action_required` / `backend_create_failed`. [2.3, spike log 08:08]
- R49 Sending a launch decision never registers the lead as live. The duplicate-live preflight counts only a seat process, not the decision send. Evidence: spike log 08:07, where a decision send wrote an active `lead` presence and the next `--apply` was refused with `duplicate_live` for 90 s. [2.3, spike log 08:07]
- R50 "Next actions" and every suggested action name only flags and commands that work on the current path; a deprecated flag never appears. Evidence: spike log 08:07, where 2.31 suggested `--force-duplicate`, which does nothing on the launchapi path. [2.3, spike log 08:07]
- R51 A launch started from the amq-squad binary always hands the AMQ launch library a real `amq` executable path, or amq-squad itself serves the `coop exec` contract. The library resolves `os.Executable()` when no path is given, and inside amq-squad that is amq-squad. Evidence: spike log 2026-10-22 06:24, where every launchapi start from 2.31 failed at tmux Create because the pane ran `amq-squad coop exec`; the legacy pane driver, which calls `amq` from PATH, launched the same roster at once. Upstream ask 14 asks for an explicit path on the request. [2.4, spike log 2026-10-22 06:24]
- R52 The launch sets the complete AMQ identity in every seat environment (`AM_ME`, `AM_ROOT`, `AM_BASE_ROOT`, `AM_SESSION`) from the launch record only, and proves one `amq drain` before it reports the seat as up; a seat never chooses or repairs its own pin (five of six S6 seats exported `AM_SESSION` themselves, which is the model acting as the enforcement point; the one seat that followed the team rules lost its task). Evidence: S6 run of 2026-10-22, where the legacy driver omitted `AM_SESSION`, amq 0.102.0 refused every seat's first drain, five seats repaired their shell themselves and one stayed idle for the whole run. [2.4, S6 report finding A]

Detection and registry
- R9 First run and `doctor` detect tools, versions, login state, Ollama models, RAM, GPU, and latency; `doctor` lists project roots codex-cli does not trust [2.3, amendment 1]; latency is direct text TTFT [2.3, amendment 10].
- R10 The registry holds one entry per runtime x model x effort with verified, cost tier, privacy class, quota pool, concurrency cap, untrusted_input, probation, latency.
- R11 The lead proposes seats from verified entries only.
- R12 A local entry is verified only after a tool-call and edit probe passes N of N times with zero false successes, N=3 in the spike and N=5 before an entry ships; the probe requires confirmed tool execution, staged and untracked diff checks, an exact final line, and a read. [2.3, amendments 10 and 15]

Modes, lead, scheduler
- R13 Two modes, auto and manual, set globally, per repository, by flag, or by lead command.
- R14 In auto mode the lead chooses runtime, model, and effort per task; a deterministic filter checks the choice against the grant.
- R15 In manual mode a new seat starts only after approval; dispatch to a running seat needs none.
- R16 A standing grant per session sets maximum seats (default 12 per session [2.5, amendment 22]), cost per day, permitted runtimes, permitted privacy class; grants and mode live only in the guarded owner file `~/.amq-squad/grants.yaml`, and `squad.yaml` can only narrow them. The privacy class is enforced in two layers: `seat-exec` execs the provider with an environment scrubbed of `*_API_KEY`, `*_TOKEN`, `*_SECRET` and known vendor names unless the runtime declares a need, and the pi package restricts the model list to the seat's registry entry. [2.3, amendment 12] `seat-exec` denies writes to the owner's global agent config (`~/.claude/settings.json`, `~/.codex/config.toml`), and `doctor` diffs that config against the preflight copy. Evidence: S6 finding B, 2026-10-22, an unattributed change to `~/.claude/settings.json` during the run. [2.4, amendment 18] `doctor` checks the owner's shell profile for exported `*_API_KEY`, `*_TOKEN` and `*_SECRET` names and warns, because provider tool shells source the profile and bypass the process-level scrub; the pi package's tool shell runs without the profile; whether `SSH_AUTH_SOCK` reaches a seat is a phase 2b decision. [2.4, amendment 17] After the first usage-limit event in the window the scheduler starts no new seat; the usage-limit signal, not RAM, is the enforced stop. [2.5, amendment 22]
- R17 The scheduler is deterministic Go with queue, leases, retries, and pools keyed by runtime x model x effort; dispatch is tracked by task leases, not by mail. [2.3, amendment 11]
- R18 Idle seats close after a timeout.
- R19 The lead does not poll tasks; it decomposes, labels, prioritizes, converges. The lead loop test is "the lead does not start coding". [2.3, amendment 11]

Routing
- R20 Each task carries one class from the closed list with a verifier.
- R21 A task routes below its tier only with a mechanical verifier.
- R22 Plan and review never route down; the reviewer family differs from the implementer family.
- R23 Chain selection checks verified, privacy class, concurrency cap, cost cap; no match means wait with a reason.
- R24 Local-only paths never go to a vendor.
- R25 Escalation: above 32k tokens skip Ollama; above 100k skip Haiku; retry moves one tier up and never repeats the entry; tier is fixed per task.
- R26 `exploration_share` is configurable, default 0.05, never plan or review, logged.
- R27 One outcome log row per attempt with the fields in 6.4.
- R28 A weekly report proposes table changes; the owner approves them.
- R29 Jev is optional behind a flag, shadow by default, never a security decision, never receives command arguments.

Gates and security
- R30 Four actions always stop for the owner: tag and release, external send, credential change, growth beyond the grant. Merge to the default branch moves to the standing merge grant in 7.2: a rules decision by the lead inside limits the Go core enforces; a merge outside the grant is refused by the core, and that refusal escalates to an owner-signed gate; manual mode keeps the per-merge stop. [2.4, S2 report owner decision]
- R31 A typed gate binds one action to one target digest.
- R32 A typed-gate answer is owner-signed with the Buzz key (BIP-340) over gate id, action class, and target digest; `amq-remote` is transport only; the Go core verifies it and records it in an operatorauth envelope; a seat or a model cannot produce a valid answer. The typed prefix stays 16 hex and is checked only against the immutable gate record, which holds the full digest and is written before the question is sent; the verifier follows the conformance rules in 7.3. [2.3, amendment 13] Enforcement stays in the Go core; hooks are UX and evidence. [2.3, amendment 3] The gate text says "reply in this thread"; thread binding stays on; a non-thread answer that matches an open gate (gate id, class, prefix, owner pubkey) makes the core re-post "reply here to confirm" in the gate thread, and the next thread reply is the answer; there is no "only candidate" acceptance. [2.5, amendment 19] A mailbox-binding answer arrives as a `nostr:<eventID>` label, and the layer verifies the event against the relay with the Claude-hook checks. [2.5, amendment 20]
- R33 Seats hold no default-branch or release credentials. The Buzz nsec enters only `amq-owner-sign`, once, and is written nowhere. [2.3, amendment 14] It reaches the signer only through a password-manager pipe, never a terminal, a log or a file. [2.5, amendment 19]
- R34 Merges go through the standing merge grant and a deterministic merge train; the batch digest covers the ordered (PR, head SHA) pairs plus the base SHA and is written to the audit log, unsigned; any rebase that changes a tree re-runs the rules check; CI-only branch protection on the server stays, and no seat process holds a merge path to the default branch. [2.4, S2 report owner decision]
- R35 In the built-in pi and Claude Code, hooks inject role and rules every turn and record evidence on every tool result; Codex, Grok Build, and Cursor receive role and rules at launch only, and Codex evidence comes from AMQ capture or Codex session files.
- R36 In the built-in pi a guard failure blocks the call; Claude Code PreToolUse fails open on timeout, on non-zero exit without a deny, and on a missing command, a timeout costs exactly the configured timeout, and the docs say so; no other runtime blocks in session. [2.3, amendment 3]
- R37 Hook configuration and rules files are guarded paths.

Fleet, mayor, remote
- R38 One operator session holds the fleet registry and receives all gates and status through AMQ routing.
- R39 The operator session and a squad lead are the same program with different grants.
- R40 In auto mode the operator session, the mayor, starts squads inside a fleet grant: maximum squads, cost per day, permitted repositories; the Go core provisions the roster through the launch API package, and where a new project root needs `amq launch` to provision an unknown session (upstream ask 2) the fallback is: the mayor proposes, the owner starts.
- R41 The mayor never approves a typed gate.
- R42 `amq-remote` plus Buzz is the remote face; Buzz attaches to the operator session. Enrollment defaults to `--enable buzz-profile --presence`, at the cost of one extra owner signature. [2.5, amendment 19]
- R43 `status --json` is the versioned contract in 4.6 with a golden fixture in CI.
- R44 amq-noc reads that contract and AMQ primitives only.

Scale and host
- R45 The Mac is the supported host; `doctor` warns on sleep and missing login service.
- R46 The load test runs before caps are set: 2 squads of 3 seats, 25 tasks, on amq-noc and workspace-cli; its results set the registry caps, and the 12-seat caps are extrapolated and labeled as such. The default of 12 seats per session moves only after the first 12-seat run is measured and labelled. [2.5, amendment 22]
- R47 Capacity target about 12 live seats on 64 GB; the rest queue.

## 13. Phases

Squads of agents build 3.0 with v2.31. Owner decisions, the load test
wall time, and upstream changes are the only hard limits.

| Phase | Work | Time |
| --- | --- | --- |
| 1 Spike | wrapper launch of one Claude and one Codex seat with model, effort, hooks, role; Claude PreToolUse under crash and timeout; signed gate answer through amq-remote; pi seat with Ollama through coop exec; task loop with a pi lead; the load test; draft the upstream asks, filed only after the owner approves [2.3] | 2 to 3 days |
| 2a Subtraction | delete the v2 code in section 10; add the three verbs. From the C11 cut of 2a-1 until `seat-exec` lands in 2b, the v3 binary cannot launch seats; every run in that window (the Codex follow-up, S6 variant 2, any owner demo) uses the installed 2.31 binary. Not a bug. [2a-1 planner decision 6] Status 2026-10-22: 2a-1 merged (7b8c8f1): internal/ non-test 110,929 to 72,972 lines, tests 3,024 to 2,027 with every removal audited, bubbletea/bubbles/lipgloss/fsnotify out of go.mod. 2a-2 inherits, from `docs/v3-spike/2a1-execution.md`: 40 delete-fate files (10,819 lines) plus status_board.go and lead_identity.go with their kept callers named; six packages (runtimeaction, runtimecontrol, liveidentity, notifier, bootstrapack, tmuxpane) and attention/dedup.go; the holding file internal/cli/v3_holding.go (832 lines, target 0); the coverage debit (each flagged test is restored with a test-side fixture or retired with its subject's shrink, none dropped silently); stale command names in kept text (worktree-isolation remedy, generated team rules, team_overlay.go). [2a-1 result] | 2 days |
| 2b Core | task store with leases and pools; status contract with the classifier moved in; registry and detection; routing table and outcome log; `seat-exec` hardening before first use [2.3, amendment 7]; `amq-squad trust <repo>` and the doctor trust check [2.3, amendment 1]; provider environment scrub [2.3, amendment 12]; hold-seam and role-delivery tests [2.3, amendments 11 and 15] | 2 to 3 days |
| 3 Security | credential custody; branch protection and merge-train checks; Codex sandbox defaults; signed gates for the four typed classes; standing grants, including the per-repository merge grant [2.4, S2 report owner decision]; release gate flow | 3 days |
| 4 Faces | built-in pi package with hooks and native AMQ tools [2.3, amendment 11]; Claude Code hooks plugin; operator session and mayor; amq-noc 1.0 as renderer; Buzz attachment | 3 to 4 days |
| 5 Release 3.0 | `squad.yaml` final form; `migrate` from v2 profiles; real-AMQ CI; release smoke; local-model eval; Jev shadow behind a flag; signed and notarized launcher [2.3, amendment 9] | 2 days |

Phase 1 runs first because its numbers set the limits for everything after
it. Phase 2a runs alone and next, because it deletes code the other phases
would otherwise edit. Phase 4 depends on the 2a verbs. amq-noc depends on
the 2b contract. Within those dependencies, 2b, 3, and 4 run in parallel.

Phase 1 roster: the Codex subagent in the orchestrating Claude Code
harness cannot install, clone, or create branches. Opus implements and
Codex subagents review. This says nothing about codex-cli seats under
`amq launch`. [2.3, amendment 6]

Phase 2 roster (owner rule, 2026-10-22): Fable 5.1 high runs only the lead
session (planning, rulings, merges, review of agent output); implementers
are Opus 5.5 at high effort in their own worktrees with the work branch
checked out; probes, drafts and log collection are Sonnet 5.5 at medium;
second-opinion reviews are Codex. No agent starts without an explicit
model. Process evidence for the registry's "verified" rule (R12): on
2026-10-22 three implementer starts made no commit (a Fable agent stopped
by a session rate limit after five cuts, an Opus pane agent that died
silently, an Opus background agent blocked by the worktree guard and by
deletion denials); the fourth, an Opus background agent with the branch
checked out in its own worktree, worked. [2.4, roster rule]

Phase 2a, step 0 changes the module path from `/v2` to `/v3`.
`go install .../v3/...@$(VERSION)` cannot pass until the first v3 tag
exists, so `make release-smoke` is expected red on `v3` until phase 5.
README, MIGRATION, RELEASING and scripts/check-release-version.py stay on
v2 until the v3 tag. Branch `v3-2a0-module-path` stays unmerged until the
owner's go on the spike table. [2.3, amendment 16] Fact at the time of
this revision: the branch is merged into `v3` as 422199d.

Phase 2b also runs the controlled role-delivery matrix; the spike
comparison was not controlled. [2.3, amendment 15]

First action: the load test. Two repositories, amq-noc and workspace-cli,
two squads of three seats mixed Claude Code and Codex, 25 small real
tasks that build and test. The 12-seat caps are extrapolated from it and
labeled as such.
Record peak RAM, 429 and usage-limit events per provider, wall time, and
merge conflicts. The spike runs it first as Claude-only with 21 tasks.
The four Codex tasks wait for a Codex follow-up after the owner trusts
both repositories in codex-cli. Seats open PRs and merge nothing. [2.3, spike log 00:22]
The Codex follow-up runs on AMQ v0.104.0 or, until it is released, on
the 0.147.0 pin. [2.4, ask 9 shipped] AMQ v0.104.0 shipped on
2026-10-22, and the criterion 4 re-run used it. [2.5, amendment 20]

## 14. Risks and facts to verify

- Claude Code PreToolUse fails open on timeout, on non-zero exit without
  a deny, and on a missing command; the S4 fault table measured all three.
  Security does not depend on it (R33, R34, R32). [2.3, amendment 3]
- pi seats are second-class in launch until the upstream adapter lands.
- Codex-accepted model ids under the owner's login: measured in the S4
  closure run; the registry checks the id against the pinned codex-cli
  version before launch (section 5). [2.4, amendment 17]
- A Codex seat under `workspace-write` never commits (codex-cli 0.163.0,
  in a worktree and in a plain checkout, with and without `--add-dir`).
  The harness commits and pushes for every seat, and the Go core checks
  the task-declared file set on that commit (4.3, 7.2). Until that
  harness step exists in phase 2b, a Codex seat's work reaches a branch
  only by hand. [2.5, amendment 21]
- The lead and the mayor run on API keys; name their default model in the registry.
- GitHub merge queue on user-owned repositories: not available. S2
  measured a 422 on the `merge_queue` rule, so the train is our Go
  loop. [2.4, S2 report]
- Subscription seats may need costing as a share of the 5-hour window.
- Lossy summaries: `summarize` consumers get references only (R20).
- Prompt injection through cheap readers: `untrusted_input` forces
  read-only tools and schema-only output on smol entries.
- The binary grows with the embedded pi runtime. Measured after the
  strip: a 59.3 MB launcher, 179 MB unpacked in 6,432 files including
  fd 10.5.0 and ripgrep 15.2.0, 2.38 s cold start to first token and
  0.25 s warm. [2.3, amendment 8]
- Sensitive-path list ownership: repository file, user file as fallback.
- Codex folder trust: a fresh codex-cli seat stops on its folder-trust
  prompt. Answering it writes the owner's Codex config, which amq-squad
  never does. This is codex-cli behaviour, not a launch API ask. [2.3, amendment 1]
- A quarantined launcher hangs on first start. This is a release risk,
  not a spike failure. The cause is unexplained; reproduce it under
  notarization. [2.3, amendment 9]
- Vendor API keys in the owner's environment reach local-only seats
  unless `seat-exec` scrubs them (R16). [2.3, amendment 12]
- The wrapper binary bytes are outside the AMQ trust digest. We accept
  this as the same trust as the `amq` binary. [2.3, amendment 2]
- The owner's global agent config can change during a run. S6 finding B
  saw `~/.claude/settings.json` change at 06:56, unattributed; the
  preflight kept only a hash, so the change could not be shown.
  `seat-exec` denies the write and `doctor` diffs the file (R16). [2.4, amendment 18]
- A repository-local `.amq/launch.local.json` picks the AMQ library's
  launcher backend. With `launcher_preference: [tmux, commands]`, the
  pinned library (agent-message-queue v0.75.0, in process) ran its own
  TmuxBackend instead of the "commands" backend that 2.31 drives. That
  backend builds the pane command from `os.Executable()`, which is
  amq-squad, as `<amq> coop exec ...`. amq-squad has no `coop` command, so
  the pane exits, Create fails, and the journal stays at phase `intent`.
  The library's journal recovery then replays the journaled tmux backend
  and fails the same way. An in-process caller of the launch library
  cannot assume which backend runs or that `os.Executable()` is `amq`.
  Evidence: spike log 08:08 and 08:12. [2.3, spike log 08:08]

## 15. Decision log

- D1 Mac host. D2 amq-remote + Buzz. D3 tmux substrate, no terminal
  control. D4 amq-noc renders the status contract; classifier moves in.
  D5 scale by queue, ~12 live seats, task store stays, scheduler is Go.
  D6 built-in pi. D7 detect binaries into the registry. D8 solo first
  run, zero writes. D9 auto and manual modes with standing grants. D10
  lead picks runtime, model, effort per task; pools keyed by all three.
  D11 one operator session for the fleet. D12 the mayor in 3.0, inside a
  fleet grant.
- Review 1 (Opus 5.5): Go core kept, no rebrand of pi as a separate
  distribution (superseded by D6 on embedding only), wrapper seats,
  Claude or Codex default runtime, hooks are not security, Jev out of
  enforcement.
- Review 2: D5 restated; phase 2 split; status contract spec; D2 settled;
  gates at scale; load test first.
- Routing research: verifier-gated cascades; extract and summarize
  split; plan and review floors; cross-family review; local default
  qwen3.6 after probe; price steps; quota pools; outcome log;
  exploration.
- Owner answers: Q1 honest errors by default, stricter via grant; Q2
  TypeScript owned; Q4 lead in built-in pi; Q5 Jev optional; Q6 vendors
  may see raw code, privacy class absolute; Q7 3 to 4 squads now, mayor
  in 3.0; Q8 amq-remote + Buzz; Q9 exploration configurable.
- 2026-10-21, owner: go for the spike recorded at 00:02; `v3` branch
  created from `main`.
- 2026-10-21, amendment 1: amq-squad never writes the owner's Codex config
  or answers the folder-trust prompt; the owner pre-trusts each repository;
  phase 2b adds `amq-squad trust <repo>` and a doctor check.
- 2026-10-21, amendment 2: the role comes only from `role.md`; the Go core
  records its SHA-256 and `seat-exec` checks it; wrapper binary integrity
  is accepted, not solved.
- 2026-10-21, amendment 3: enforcement stays in the Go core; Claude hooks
  are UX and evidence.
- 2026-10-21, amendment 4: per-seat CODEX_HOME is rejected.
- 2026-10-21, amendment 6: in the spike, Opus implements and Codex
  subagents review.
- 2026-10-21, amendment 7: `seat-exec` drops `--root`, anchors traversal
  to the AMQ root, and checks directory ownership; the Claude hook ships as
  the user-scope plugin.
- 2026-10-21, amendment 8, lead rulings 1 to 3: pi from
  `@earendil-works/pi-coding-agent`; packaging (a) with strip; (b2) the
  only fallback; (b1) rejected; own-Bun dropped; fd and rg embedded;
  launch-time integrity check; offline flags set by the launcher.
- 2026-10-21, amendment 9, lead ruling 4: the launcher ships signed and
  notarized in phase 5; until then install is `go install` and brew only.
- 2026-10-21, amendments 10 and 15, lead ruling 6 and the S5 Codex review:
  "verified" is N of N with zero false successes, N=3 in the spike and N=5
  to ship; a false claim that ends in RESULT: FAIL is not a false success.
- 2026-10-21, amendment 11, lead rulings 7, 8 and 10: native AMQ tools in
  the pi package; dispatch by task leases; role in the first message and
  the system prompt.
- 2026-10-21, amendment 12, lead ruling 9: both fixes for vendor keys,
  the scrubbed environment and the model list restriction.
- 2026-10-21, amendment 13, lead ruling on the S1 Codex review: the typed
  prefix stays 16 hex; binding to the immutable gate record closes the
  collision game; same-UID ledger writes accepted for phase 1.
- 2026-10-21, amendment 14: phone-side signing does not exist; exporting
  the Buzz nsec once into `amq-owner-sign` is put to the owner as a yes or
  no question. Open.
- 2026-10-21, amendment 16, lead after accepting c899e8f: release-smoke is
  expected red on `v3` until phase 5; the v2 release docs change at the v3
  tag; `v3-2a0-module-path` waits for the owner's go.
- 2026-10-21, criterion numbering: C1 = S1, C2 = S2, C3 = S3, C4 = S4,
  C5 = S6; S5 answers 16.3, which does not narrow.
- 2026-10-22, merge gate (owner decision during S2): merges to the default branch are rules decisions by the lead under a standing per-repository grant, with the batch digest written to the audit log and not signed; the owner-signed typed gate stays only for tag and release, external send, credential change, and growth beyond the grant; manual mode keeps the per-merge stop. The Go core enforces the grant's limits (repository, base branch, CI green, size cap, no force push, no protected-path change) and the lead decides inside them; a merge outside the grant is refused by the core, not by the lead's judgment, and that refusal is what escalates to an owner-signed gate. Server-side CI-only branch protection stays. Criterion 2 is regraded against this wording; S2 report. Affects R30, R34, 7.1, 7.2, 7.3; integrated in revision 2.4. Restated to the owner at 07:17 by the peer session; a change from him is a one-line edit here.
- 2026-10-21, 00:21, S4: the Codex half of S4 is blocked by owner
  decision; no agent writes the owner's Codex config.
- 2026-10-21, 00:22, lead, option A: S6 runs Claude-only with six Claude
  seats and 21 tasks; PRs only, no merges; the four Codex tasks wait for
  the owner's pre-trust.
- 2026-10-21, 01:15, lead: S6 does not wait for the S1 re-review.
- 2026-10-21, 01:40, lead: phase 2a step 0 and the 2a-1 work list run in
  the idle hours, outside the five-criteria table.
- 2026-10-21, 05:06, lead, option b: S6 runs on a shared host, labeled
  "shared host, other agents idle; window shared with idle sessions"; the
  preflight process check is an overridden warning.
- 2026-10-21, 05:08: after the auto-mode classifier denial, no retry and
  no workaround; the owner launches S6 from his own session.
- 2026-10-21, 05:12: the fourth S1 Codex review is deferred; no Opus
  reviewer substitutes, so the family rule holds.
- 2026-10-21, 08:12: the launcher-preference fix stays in spike scope;
  ApplyResult surfacing and decision-send presence land in the 3.0 Go
  core, not in a wrapper (R48, R49).
- 2026-10-22, revision 2.4: the day-2 owner rulings are integrated in
  place. The merge gate as in the entry above. R51 and R52 are tagged to
  their evidence (spike log 06:24, S6 finding A). Amendment 17 (the S4
  closure run) and amendment 18 (S6 finding B: `seat-exec` denies writes
  to the owner's global agent config, `doctor` diffs it) land under R16,
  4.3, 5, 7.3, 14 and 16.2. Upstream ask 9 shipped in PR #1108 and AMQ
  v0.104.0, so the Codex follow-up uses v0.104.0 or the 0.147.0 pin.
  Criterion status follows docs/v3-spike/spike-report.md, the single
  five-criteria table. The Buzz setup (relay recorded, npub pending)
  replaces the nsec question of amendment 14 as the criterion 1 blocker.
- 2026-10-22, S1 owner run: criterion 1 passed with the real Buzz key (gate g-owner-02). Amendment 19 for revision 2.5: profile and presence on by default at enrollment; gate text says "reply in this thread"; thread binding stays on, and a matching non-thread answer triggers a re-ask in the gate thread instead of acceptance by heuristic. Secret rule: `op run --no-masking` only into the signer pipe.
- 2026-10-22, seat cap (owner decision): the default maximum is 12 seats per session, configurable per owner in the guarded grants file (R16) and narrowable per repository. RAM is not the binding limit on the reference host (S6: 6 seats 4.2 GB peak, 12 seats extrapolated 8.4 GB typical, 14 GB worst on 64 GB); the subscription window is, so the scheduler stops dispatching new seats at the first usage-limit signal, and the first 12-seat run is measured before any default above 12 is considered. Amendment 22, for 2.5.
- 2026-10-22, S6 draft PRs (owner decision): the 18 open draft PRs on amq-noc and workspace-cli stay open as the first real input of the phase 2b merge train: 16 clean, two predicted pairs that need ordering (W3/W7, W5/W12), one that fails the task file-set rule (W5). No manual merge, no closing.
- 2026-10-22, revision 2.5: amendments 19 to 22 and the 2a-1 result are
  integrated in place, tagged "[2.5, <source>]". Amendment 19 (S1 owner
  run) lands in 7.3, 9, R32, R33 and R42. Amendment 20 (maintainer
  rulings) lands in 4.1, 4.3, 4.4, 7.3, 9, 11, 13, 16.8, R1, R5 and R32.
  Asks 1, 4, 8, 11, 12, 13 and 14 are filed as issues #1153 to #1159;
  ask 5 is withdrawn and ask 7 is dropped. Amendment 21 (the harness
  commits for every seat) lands in 4.3, 7.2, 7.3, 14 and R4; it replaces
  the 2.4 sentence that called the task-declared file set a candidate
  core check for a later phase. Amendment 22 (seat cap) lands in 5, 7.2,
  8, R16 and R46. The 2a-1 result lands in 16.1. Criterion 4 re-ran on
  AMQ v0.104.0 and is still not passed.

## 16. Amendments from the final review (revision 2.1, 2026-10-20)

Verdict of the final review: green with edits. The inline edits are
applied above. These structural amendments take precedence over sections
10, 12, 13, and 14 where they differ. Revision 2.3 does not change this
section's decisions; it tags the spike amendments that touch it.
Revision 2.4 did the same for the day-2 owner rulings. Revision 2.5 does
the same for amendments 19 to 22 and the 2a-1 result.

### 16.1 Keep/delete manifest

Phase 1 produces a manifest per package and per file under `internal/`,
with a target line count for 3.0. `internal/cli` is about 74.5k of the
111k non-test lines and is the main deletion target; section 10 names
only about 12k lines. Packages with no stated fate get one:
`compoundrelease` stays (release gate flow), `state` is reviewed line by
line, `commandevidence` is replaced by the evidence ledger,
`reviewrebind` and `team` shrink to what `squad.yaml` needs. Phase 2a
executes the manifest alone, before any parallel phase edits the tree.

2a-1 result, from `docs/v3-spike/2a1-execution.md`. 2a-1 worked the
worklist's 138 whole-file deletions and its 13 package cuts. Of the 138
delete-fate rows, 96 are deleted. 40 are pending and 2 are trimmed
(status_board.go and lead_identity.go). These 42 files, 11,702 lines,
are deferred with their kept callers named. Six packages and
attention/dedup.go are deferred the same way. The holding file
internal/cli/v3_holding.go is 832 lines, with target 0. The execution
log holds the coverage debit list, and kept text still names removed
commands. 2a-2's scope is therefore the worklist's 27,531 in-file lines,
plus these 11,702 deferred lines, plus the holding file. [2.5, 2a-1 result]

### 16.2 doctor

`doctor` survives as the probe in section 5: tool detection, versions,
login state, local model probes, latency, sleep and login-service
warnings, the status contract self-check, the list of project roots
codex-cli does not trust [2.3, amendment 1], the shell-profile check for
exported key names [2.4, amendment 17], and the diff of the owner's
global agent config against the preflight copy [2.4, amendment 18]. The
v2 team, pane, and wake-mirror checks go.

### 16.3 Phase 4 additions

- The pi package implements pi bridge revision 4 (tool approval and
  session identity), because the reference bridge is revision 3 and Buzz
  approvals on a pi operator need revision 4. S5 showed the hold seam
  works with no pi change, so this does not narrow. [2.3, amendment 11]
- The pi package registers native `amq_send`, `amq_drain` and `amq_reply`
  tools, and delivers the role in the first message and the system
  prompt. [2.3, amendment 11]
- `amq-squad status --fleet --json` reads `~/.amq-squad/fleet.yaml` and
  is the only fleet input for amq-noc.
- R7 and R8 (solo first run, digest before any write) are delivered here
  with the built-in pi package.

### 16.4 Phase 5 additions

Uninstall (Claude plugin, `~/.amq-squad/runtime`, the amq-remote
approval hook); TypeScript package CI; darwin arm64 and amd64 bundles
with the embedded runtime, codesigned; scheduler restart recovery from
the task store; MIGRATION.md for v2 users.

The launcher ships signed and notarized. Release-smoke includes a
browser-download start on a clean user account that cold-launches through
the extracted children. [2.3, amendment 9]

### 16.5 Process ownership

The amq-squad binary starts and supervises one scheduler per squad. The
scheduler recovers from the task store on restart. The lead and the
mayor run on the built-in pi with a named default model from the
registry, on API keys. The fleet registry is `~/.amq-squad/fleet.yaml`.

### 16.6 Requirement to phase map

- Phase 1: R3, R5, R12 probe design, R46; spike exit criteria below.
- Phase 2a: R6, the three verbs behind R35 and R36, section 10 deletions.
- Phase 2b: R9, R10, R11, R13, R17, R18, R19, R20 to R28, R43, R48 to R52 [2.4].
- Phase 3: R16, R30 to R34, R37.
- Phase 4: R1, R2, R4, R7, R8, R14, R15, R29, R35, R36, R38 to R42, R44.
- Phase 5: R45, R47, migrate, uninstall, CI, bundles, docs.

### 16.7 Spike exit criteria (pass or fail)

The final report numbers them C1 = S1, C2 = S2, C3 = S3, C4 = S4 and
C5 = S6. S5 answers 16.3. [2.3] docs/v3-spike/spike-report.md is the
single five-criteria table; the status lines below follow it. [2.4, spike report]

1. A typed-gate answer is signed by the owner's Buzz key (BIP-340) over
   the gate id, the action class, and the target digest, carried by
   `amq-remote`, verified by the Go core, and wrapped as evidence in an
   operatorauth envelope. No model process holds the owner key. Fail
   means phase 3 cannot start. Status: passed. Verifier accepted by the
   fourth Codex review (97 tests green with `-race`); owner run of
   2026-10-22 with the real Buzz key from Buzz Desktop over the owner's
   relay: approve verified, envelope ok, negatives rejected, 23.5 s
   (S1 report section 12). [2.4, S1 owner run]
2. A seat pushes a seat branch and the merge train merges it under the
   owner's identity on a personal repository with CI-only branch
   protection, by a rules decision that is recorded. Status: mechanism
   passed (amq-noc #78 merged d7ff91b through a CI-only ruleset; direct
   push refused); the signature step is dropped by owner decision, and
   the criterion is graded against this wording. [2.4, S2 report owner decision]
3. A darwin-arm64 build with the embedded pi runs the lead from the
   unpacked copy. Status: passed. [2.3]
4. One Claude seat and one Codex seat launch through the launch API with
   model and effort in the provider arguments and the wrapper setting
   only environment and role. Status: not passed, blocked upstream.
   Both seats read their role and the trust prompt is gone after the
   owner's pre-trust, but Codex identity capture cannot complete on AMQ
   0.102.0 (upstream asks 12 and 13). The re-run on AMQ v0.104.0
   (2026-10-22) did not pass either: asks 12 and 13 are confirmed open
   and filed as issues #1157 and #1158. [2.5, spike report]
5. The load test numbers exist: peak RAM, 429 and usage-limit events per
   provider, wall time, merge conflicts. Status: passed for the Claude
   column (21 tasks, 19 PRs in 10m09s, 0 usage-limit or 429 events, peak
   seat RSS 4.19 GB, 0 prompt stops, 0 real conflicts); the Codex column
   is pending with the Codex follow-up. [2.4, spike report]

### 16.8 Upstream ask 7

Notify chaining for Codex, so a second notify hook can run after AMQ's
session-identity capture. The maintainer declined it on 2026-10-22:
`config.toml` belongs to Codex, and a forward failure must not block
AMQ's capture. The Codex notify design uses a small fan-out script as
the notify command, so AMQ's capture and a second receiver both run.
amq-squad never writes `config.toml` (4.3); how the script becomes the
notify command under that rule is a phase 2b item. Candidate direction,
not decided: the owner-run trust step of risk 16 (`amq-squad trust
<repo>`) is the one place the owner already edits Codex config on
amq-squad's instruction, so an owner-written notify line there, checked
by `doctor`, is the natural home. [2.5, amendment 20, candidate] Until then Codex
evidence comes from AMQ capture or the Codex session files, and the
weekly routing report uses only runtimes with complete evidence. [2.5, amendment 20]

### 16.9 Owner answers to the phase 1 questions (2026-10-20)

- Codex version: upstream PR #1108 (avivsinai/agent-message-queue,
  issue #1107) merged on 2026-10-21. It sets the floor at 0.147.0,
  verified on 0.154.0, and verified by review through 0.163.0. It ships
  in AMQ v0.104.0. The Codex follow-up (criterion 4 closure, S6 variant
  2) uses v0.104.0 or, until it is released, the 0.147.0 pin. Upstream
  ask 9 is shipped. [2.4, ask 9 shipped]
- Bot identity: none. The owner owns every GitHub identity. The "one
  review" rule in R34 is dropped; the owner-signed typed gate is the
  review; branch protection requires CI only. Superseded on 2026-10-22:
  the merge is a rules decision under the standing merge grant in 7.2,
  and the typed gate is no longer the review. [2.4, S2 report owner decision]
- Load test: amq-noc and workspace-cli, two squads of three seats, 25
  tasks. Twelve-seat caps are extrapolated and labeled.
- Branch: a long-lived `v3` branch. `main` stays frozen at v2.31 until
  the 3.0 release, because the plugin marketplace points at `main`. The
  `docs/v3-plan` branch merges into `v3`.
- API key for leads: not needed in phase 1; closed.
- Step zero before the spike: upgrade `amq` from 0.77.3 to 0.98.x and
  install `amq-remote` and `amq-owner-sign`.
- The spike starts only on the owner's explicit go.

## 17. Change lists

### 17.1 Revision 2.3 change list

- Header: revision 2.3, date 2026-10-21, spike status per criterion.
- 3.1: install paths limited to `go install` and brew until notarization (amendment 9).
- 4.1: pi package scope, packaging decision, offline flags, launch-time integrity check, native AMQ tools, role delivery, model list restriction (amendments 8, 11, 12).
- 4.3: `--root` dropped and user-scope hook (amendment 7); role only from `role.md` with SHA-256 check (amendment 2); traversal and ownership checks (amendment 7); environment scrub (amendment 12); CODEX_HOME rejected (amendment 4); Codex folder trust (amendment 1).
- 4.4: Claude fail-open rows and fault-table timings (amendment 3); pi hold seam findings and 2b tests (amendments 11, 15).
- 4.5: dispatch tracked by task leases (amendment 11).
- 5: doctor trust check (amendment 1); N of N verification, probe predicate, model results, TTFT method (amendments 10, 15).
- 6.2: qwen3.6:27b chain entry marked verified 3 of 3 (amendment 10).
- 7.3: Codex trust precondition (amendment 1); 16-hex prefix bound to the gate record, verifier conformance rules, same-UID threat model (amendment 13).
- 9: Buzz nsec enrollment as owner decision, asks 10 and 11 (amendment 14).
- 11: day-one list replaced by a pointer to docs/v3-upstream-asks.md and its 11-ask status table; nothing filed without owner approval; ask 4 and ask 1 notes (amendments 5, 8).
- 12: R1, R4, R9, R12, R16, R17, R19, R32, R33, R36 amended (amendments 1 to 4, 7, 8, 10 to 15); R48, R49, R50 added for the three 2.31 launch defects (spike log 08:07, 08:08).
- 13: phase 1 row says draft, not file; 2b, 4 and 5 rows extended; phase 1 roster (amendment 6); phase 2a step 0 and release-smoke (amendment 16); controlled role-delivery matrix (amendment 15); Claude-only first load-test run (spike log 00:22).
- 14: fail-open risk restated (amendment 3); binary size replaced by measured numbers (amendment 8); new risks for Codex folder trust, quarantine hang, vendor keys, wrapper bytes (amendments 1, 9, 12, 2); launcher-preference fact (spike log 08:08, 08:12).
- 15: decision entries dated 2026-10-21 appended; earlier entries unchanged.
- 16: intro notes 2.3 tags; 16.2 doctor trust check (amendment 1); 16.3 bridge note and native tools (amendment 11); 16.4 notarization and browser-download smoke (amendment 9); 16.6 maps R48 to R50 to phase 2b; 16.7 criterion numbering and status.
- 17: this list.

### 17.2 Revision 2.4 change list

- Header: revision 2.4, date 2026-10-22, criterion status per docs/v3-spike/spike-report.md; intro names the 2.4 tag form.
- 2, principle 4: four typed gates; merge under the standing merge grant (S2 report owner decision).
- 3.2 and 3.3: hooks guard the four typed gates and the merge grant; the squad stops only at the four typed gates, manual mode keeps the per-merge stop (S2 report owner decision).
- 4.3: codex-cli owns and rewrites `config.toml` (amendment 17); seat environment answer, shell-profile check, pi tool shell without the profile, `SSH_AUTH_SOCK` as a 2b decision (amendment 17); `seat-exec` denies writes to the owner's global agent config and `doctor` diffs it (amendment 18).
- 5: registry checks the Codex model id against the pinned codex-cli version before launch (amendment 17).
- 7.1: four typed gates; merge is not a typed gate (S2 report owner decision).
- 7.2: the standing merge grant per repository with its rule list, core enforcement, refusal escalation, manual-mode stop (S2 report owner decision).
- 7.3: branch protection bullet (grant is the review, S2 evidence); Codex `workspace-write` worktree commit failure answers ask 5 (amendment 17); batch-gate bullet replaced by the merge train under the grant, digest written to the audit log unsigned, merge queue 422 (S2 report owner decision).
- 9: Buzz setup replaces the nsec question as the criterion 1 blocker (spike report).
- 11: ask 5 recommendation (amendment 17); ask 9 shipped (PR #1108, v0.104.0); rows 12, 13 and 14 added.
- 12: R16 amendment 18 sentence retagged and worded as a requirement, shell-profile check added (amendment 17); R30 four actions, merge moves to the standing grant; R34 merge grant and unsigned digest (S2 report owner decision); R51 tagged [2.4, spike log 2026-10-22 06:24]; R52 tagged [2.4, S6 report finding A].
- 13: phase 3 row names the merge grant; the Codex follow-up runs on v0.104.0 or the 0.147.0 pin (ask 9 shipped).
- 14: Codex model ids measured (amendment 17); merge queue not available (S2 report); new risk for the global agent config change (amendment 18).
- 15: the 2026-10-22 merge gate entry extended with the 07:17 restatement; a 2026-10-22 revision 2.4 entry appended.
- 16: intro notes 2.4; 16.2 doctor shell-profile check and config diff (amendments 17, 18); 16.6 phase 2b line tagged [2.4]; 16.7 points to the spike report and restates criteria 1, 2, 4 and 5; 16.9 Codex version bullet rewritten (ask 9 shipped) and bot-identity bullet marked superseded.
- 17: this list.

### 17.3 Revision 2.5 change list

- Header: revision 2.5, date 2026-10-22; status paragraph per docs/v3-spike/spike-report.md, criterion 4 re-ran on v0.104.0 with issues #1157 and #1158 filed (spike report).
- Intro: revision 2.4 sentence in the past tense; revision 2.5 integrates amendments 19 to 22 and the 2a-1 result, tag form "[2.5, <source>]".
- 4.1: pi launch adapter upstream, mint mode with `pi --session-id <launch nonce>`, pinned verified pi version, fail-closed resume that checks the recorded session exists (amendment 20).
- 4.3: admitted argument forms derived upstream from the validation tables, `seat-exec` drops its own list once that ships (amendment 20).
- 4.3: a seat never commits; the harness commits and pushes the seat branch and the Go core enforces the task-declared file set on that commit; one commit path for Claude and Codex seats; the step's shape is a 2b design item (amendment 21).
- 4.3: pi seats move to the launch API once the mint-mode adapter ships (amendment 20).
- 4.4: Codex evidence row names the fan-out notify script (amendment 20).
- 5: seat cap paragraph: default 12 per session, set in grants.yaml, narrowed by squad.yaml, usage-limit signal as the enforced stop, first 12-seat run measured before the default moves (amendment 22).
- 7.2: session grant's maximum seats defaults to 12 and stops new seats after the first usage-limit event (amendment 22).
- 7.2: task scope enforced at the harness-side commit, replacing "candidate core check in a later phase" (amendment 21).
- 7.3: Codex sandbox bullet: `workspace-write` never commits, `--add-dir` does not help, the harness commits, ask 5 withdrawn (amendment 21).
- 7.3: gate answers bullet: the `nostr:<eventID>` label and layer-side relay checks replace "amq-remote forwards the raw signed event" (amendment 20).
- 7.3: new bullet: "reply in this thread", thread binding on, re-ask in the gate thread for a matching non-thread answer, no "only candidate" acceptance (amendment 19).
- 8: seat cap default 12, usage-limit signal not RAM (amendment 22).
- 9: secret rule (password-manager pipe into the signer only), owner run passed, ask 11 filed as #1156, enrollment defaults `--enable buzz-profile --presence`, thread rule, `nostr:<eventID>` label (amendments 19, 20; upstream asks).
- 11: filing line for issues #1153 to #1159; rows 1, 4, 8, 11, 12, 13 and 14 marked filed; rows 12 and 13 confirmed open on v0.104.0; row 5 withdrawn (amendment 21); row 7 dropped (amendment 20); ask 4 widened by the maintainer (amendment 20).
- 12, R1: embedded pi stays on a version the upstream adapter verifies (amendment 20).
- 12, R4: a seat never commits; harness commit with the task file-set check (amendment 21).
- 12, R5: pi adapter form and fail-closed resume (amendment 20).
- 12, R16: maximum seats default 12; usage-limit signal as the enforced stop (amendment 22).
- 12, R32: thread rule and re-ask (amendment 19); `nostr:<eventID>` label verified against the relay (amendment 20).
- 12, R33: nsec only through a password-manager pipe (amendment 19).
- 12, R42: enrollment defaults `--enable buzz-profile --presence` (amendment 19).
- 12, R46: the default of 12 moves only after the first measured 12-seat run (amendment 22).
- 13: AMQ v0.104.0 shipped and the criterion 4 re-run used it (amendment 20). The phase 2a status line and the phase 2 roster paragraph are unchanged.
- 14: new risk: a Codex seat never commits, the harness commits for every seat (amendment 21).
- 15: a 2026-10-22 revision 2.5 entry appended.
- 16: intro notes 2.5; 16.1 the 2a-1 result and 2a-2 scope (2a-1 result); 16.7 criterion 4 status after the v0.104.0 re-run (spike report); 16.8 notify chain declined, fan-out script, how it becomes the notify command is a 2b item (amendment 20).
- 17: this list.
