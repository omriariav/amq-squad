# amq-squad 3.0 plan, revision 2

Date: 2026-10-20. Status: approved for the spike. Planning only; no code
exists yet. Revision 1 (sections 1-16, with the two Opus 5.5 reviews and
the routing research) is the history behind this document.

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
   a grant answers the digest. The five typed gates always wait for the
   owner.
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

### 3.2 Solo mode

The lead is the built-in pi with the amq-squad package. You work with it
as with any coding agent. The hooks inject the rules, record evidence,
and guard the five actions.

### 3.3 Team mode

You give a task. The lead decides: solo or team. For a team, the lead
proposes `squad.yaml` as a digest: seats, roles, runtimes, models, efforts,
worktrees, gates. The file is written only after approval. In manual mode
you approve the digest. In auto mode the grant
approves it. The seats start in tmux. The scheduler sends tasks. The
lead reviews and converges. The squad stops for you only at the five
typed gates.

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
they enter the trust digest. The wrapper takes only `--seat <handle>`. It
writes the launch record and the role into the seat's extension directory,
sets the seat environment, and execs the provider in place. It never adds
provider flags. The Claude Code hooks come from a user-scope plugin that
reads the seat environment; the Claude grammar has no plugin or settings
flag.

pi seats start through `amq coop exec` until AMQ ships a pi adapter.
They get no digest-gated apply, resume, or liveness from AMQ. The status
says so.

amq-squad never creates, moves, resizes, focuses, or closes a pane.
tmux is the substrate. cmux and Ghostty come from AMQ launchers. iTerm2
uses `tmux -CC`.

### 4.4 Hooks per runtime

| Runtime | Inject | Guard | Evidence |
| --- | --- | --- | --- |
| built-in pi | before_agent_start | tool_call, fails closed | tool_result |
| Claude Code | SessionStart, UserPromptSubmit | PreToolUse deny; fails open on crash | PostToolUse |
| Codex | AGENTS.md marker and skill, at launch only | sandbox and approval flags at launch | AMQ capture or Codex session files; AMQ owns the notify hook |
| Grok Build, Cursor | skill, at launch only | none in session | none in session |

Where a hook cannot block, the documentation says so. Security does not
depend on it.

### 4.5 Scheduler and task store

The scheduler is a deterministic Go process, one per squad. It holds the
task queue, the leases, the retries, and the pools keyed by runtime x
model x effort. It gives each task a lease. It sends the task to an idle
seat with the chosen key, or starts one inside the grant. An idle seat
closes after a timeout. The lead does not poll tasks. The lead
decomposes, labels each task with a class, prioritizes, and converges.

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
and measure the time to first token per entry.

`~/.amq-squad/registry.yaml` holds one entry per runtime x model x
effort with: verified, cost tier, privacy class (local or vendor), quota
pool (API and subscription are separate pools; Opus 5.5 has its own API
bucket), concurrency cap (all Ollama entries share one GPU pool),
untrusted_input flag, probation flag, measured latency. A local entry
gets `verified` only after a tool-call and edit probe passes. The lead
proposes seats from verified entries only. A missing tool is one status
line, not an error.

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
  qwen3.6:27b low (after probe); pi + anthropic haiku 5.5 low (API pool);
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

### 7.1 The five typed gates

These always stop and wait for the owner: merge to the default branch,
tag and release, external send, credential change, growth beyond the
grant. A typed gate binds one action to one target digest.

### 7.2 Standing grants

Everything else is a standing grant per session: seat-owned feature
branches, push to seat branches, open PR, run tests, start seats inside
the composition budget. The fleet grant adds: maximum squads, cost per
day, permitted repositories.

### 7.3 Enforcement outside hooks

- Seats hold no credentials for the default branch or for releases.
- Branch protection on the server requires CI. The owner-signed typed
  gate is the review. There is no bot identity; the owner owns every
  GitHub identity, and seats push with it to seat branches only.
- Codex seats launch with `--sandbox workspace-write` and
  `--ask-for-approval on-request`, both admitted by the AMQ launch grammar.
- Merges go through one batch gate: the owner approves a digest over an
  ordered set of PRs, and a deterministic merge train lands it. The digest
  covers the ordered (PR, head SHA) pairs plus the base SHA. A rebase that
  changes a tree re-opens the gate. The merge train runs under the owner's
  identity after the signed batch gate; no seat process holds a merge
  path to the default branch. About 5
  to 10 approvals a day for the fleet. If GitHub's merge queue is not
  available on user-owned repositories, the train is our Go loop.
- Gate answers are authenticated. The owner signs the answer with the
  Buzz key (BIP-340) over the gate id, the action class, and the target
  digest. `amq-remote` and Buzz carry the request and the answer. The Go
  core verifies the signature and records the result in an operatorauth
  envelope. No model process holds the owner key. An answer relayed by a
  model is invalid. The Claude permission path in `amq-remote` covers
  single tool approvals only. Long-term upstream ask: `amq-remote`
  forwards the raw signed event.
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
- Tasks inside a squad can be coupled. Squads are independent. The mayor
  partitions work by repository.

## 9. Remote access

`amq-remote` plus Buzz is the remote face of 3.0. It is the only option
with owner-signed approvals. Buzz attaches to the operator session.
SagiMedina/handoff (tmux mirror on the phone over Tailscale) composes for
free on the same tmux and is a docs recipe. Grok Bot as a mesh peer and
the Slack ring adapter stay possible through AMQ and are not in 3.0.

## 10. What 3.0 deletes from v2, and what stays

Deleted: tmux pane driver and harness, runtime control, console, wizard
state machine, iTerm2 and Terminal.app backends, wake mirroring, coop
provisioning shim, most of doctor, the legacy launch driver.

Stays, simplified: typed gates and operator auth, role catalog, rules,
brief drafting as an off-path verb, worktree per seat, task store with
leases, launch intent compiler, release gate flow.

Moves in from amq-noc: the five-state classifier and the attention queue.

## 11. Upstream asks, filed on day one

1. A pi adapter in the AMQ launch API.
2. `amq launch` may provision unknown sessions.
3. Non-interactive consent by digest and decisions on the launch path.
4. `--model` advertised as a stable argument form; an env allowlist
   extension for seat configuration.
5. Codex argv grammar v2.
6. Optional: an iTerm2 launcher backend.

## 12. Requirements

Core and launch
- R1 One Go executable with a pinned pi runtime and the amq-squad pi package.
- R2 The lead and all pi seats use the built-in pi only.
- R3 Claude Code and Codex seats start through the AMQ launch API with the wrapper `seat-exec`.
- R4 Model, effort, and sandbox flags are AMQ provider arguments; the wrapper takes only `--seat <handle>`, sets the seat environment and the role, and execs the provider in place.
- R5 pi seats start through `amq coop exec` until an upstream pi adapter exists.
- R6 amq-squad never creates, moves, resizes, focuses, or closes a pane.
- R7 First run without `.amq-squad` starts solo mode and writes nothing inside the repository; it writes only under `~/.amq-squad/`.
- R8 The first team proposal shows one digest for all files it creates; nothing is written before approval.

Detection and registry
- R9 First run and `doctor` detect tools, versions, login state, Ollama models, RAM, GPU, and latency.
- R10 The registry holds one entry per runtime x model x effort with verified, cost tier, privacy class, quota pool, concurrency cap, untrusted_input, probation, latency.
- R11 The lead proposes seats from verified entries only.
- R12 A local entry is verified only after a tool-call and edit probe passes.

Modes, lead, scheduler
- R13 Two modes, auto and manual, set globally, per repository, by flag, or by lead command.
- R14 In auto mode the lead chooses runtime, model, and effort per task; a deterministic filter checks the choice against the grant.
- R15 In manual mode a new seat starts only after approval; dispatch to a running seat needs none.
- R16 A standing grant per session sets maximum seats, cost per day, permitted runtimes, permitted privacy class; grants and mode live only in the guarded owner file `~/.amq-squad/grants.yaml`, and `squad.yaml` can only narrow them.
- R17 The scheduler is deterministic Go with queue, leases, retries, and pools keyed by runtime x model x effort.
- R18 Idle seats close after a timeout.
- R19 The lead does not poll tasks; it decomposes, labels, prioritizes, converges.

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
- R30 Five actions always stop for the owner: merge to default, tag and release, external send, credential change, growth beyond the grant.
- R31 A typed gate binds one action to one target digest.
- R32 A typed-gate answer is owner-signed with the Buzz key (BIP-340) over gate id, action class, and target digest; `amq-remote` is transport only; the Go core verifies it and records it in an operatorauth envelope; a seat or a model cannot produce a valid answer.
- R33 Seats hold no default-branch or release credentials.
- R34 Merges go through one batch gate and a deterministic merge train; the batch digest covers the ordered (PR, head SHA) pairs plus the base SHA, and any rebase that changes a tree re-opens the gate; the owner-signed gate is the review, and no seat process holds a merge path to the default branch.
- R35 In the built-in pi and Claude Code, hooks inject role and rules every turn and record evidence on every tool result; Codex, Grok Build, and Cursor receive role and rules at launch only, and Codex evidence comes from AMQ capture or Codex session files.
- R36 In the built-in pi a guard failure blocks the call; Claude Code PreToolUse fails open on crash or timeout and the docs say so; no other runtime blocks in session.
- R37 Hook configuration and rules files are guarded paths.

Fleet, mayor, remote
- R38 One operator session holds the fleet registry and receives all gates and status through AMQ routing.
- R39 The operator session and a squad lead are the same program with different grants.
- R40 In auto mode the operator session, the mayor, starts squads inside a fleet grant: maximum squads, cost per day, permitted repositories; the Go core provisions the roster through the launch API package, and where a new project root needs `amq launch` to provision an unknown session (upstream ask 2) the fallback is: the mayor proposes, the owner starts.
- R41 The mayor never approves a typed gate.
- R42 `amq-remote` plus Buzz is the remote face; Buzz attaches to the operator session.
- R43 `status --json` is the versioned contract in 4.6 with a golden fixture in CI.
- R44 amq-noc reads that contract and AMQ primitives only.

Scale and host
- R45 The Mac is the supported host; `doctor` warns on sleep and missing login service.
- R46 The load test runs before caps are set: 2 squads of 3 seats, 25 tasks, on amq-noc and workspace-cli; its results set the registry caps, and the 12-seat caps are extrapolated and labeled as such.
- R47 Capacity target about 12 live seats on 64 GB; the rest queue.

## 13. Phases

Squads of agents build 3.0 with v2.31. Owner decisions, the load test
wall time, and upstream changes are the only hard limits.

| Phase | Work | Time |
| --- | --- | --- |
| 1 Spike | wrapper launch of one Claude and one Codex seat with model, effort, hooks, role; Claude PreToolUse under crash and timeout; signed gate answer through amq-remote; pi seat with Ollama through coop exec; task loop with a pi lead; the load test; file the upstream asks | 2 to 3 days |
| 2a Subtraction | delete the v2 code in section 10; add the three verbs | 2 days |
| 2b Core | task store with leases and pools; status contract with the classifier moved in; registry and detection; routing table and outcome log | 2 to 3 days |
| 3 Security | credential custody; branch protection and merge-train checks; Codex sandbox defaults; signed gates; standing grants; release gate flow | 3 days |
| 4 Faces | built-in pi package with hooks; Claude Code hooks plugin; operator session and mayor; amq-noc 1.0 as renderer; Buzz attachment | 3 to 4 days |
| 5 Release 3.0 | `squad.yaml` final form; `migrate` from v2 profiles; real-AMQ CI; release smoke; local-model eval; Jev shadow behind a flag | 2 days |

Phase 1 runs first because its numbers set the limits for everything after
it. Phase 2a runs alone and next, because it deletes code the other phases
would otherwise edit. Phase 4 depends on the 2a verbs. amq-noc depends on
the 2b contract. Within those dependencies, 2b, 3, and 4 run in parallel.

First action: the load test. Two repositories, amq-noc and workspace-cli,
two squads of three seats mixed Claude Code and Codex, 25 small real
tasks that build and test. The 12-seat caps are extrapolated from it and
labeled as such.
Record peak RAM, 429 and usage-limit events per provider, wall time, and
merge conflicts.

## 14. Risks and facts to verify

- Claude Code PreToolUse fails open on crash or timeout. Security does
  not depend on it (R33, R34, R32).
- pi seats are second-class in launch until the upstream adapter lands.
- Codex-accepted model ids under the owner's login: verify in the spike.
- The lead and the mayor run on API keys; name their default model in the registry.
- GitHub merge queue on user-owned repositories: verify; else our train.
- Subscription seats may need costing as a share of the 5-hour window.
- Lossy summaries: `summarize` consumers get references only (R20).
- Prompt injection through cheap readers: `untrusted_input` forces
  read-only tools and schema-only output on smol entries.
- The binary grows by about 100 MB with the embedded pi runtime.
- Sensitive-path list ownership: repository file, user file as fallback.

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

## 16. Amendments from the final review (revision 2.1, 2026-10-20)

Verdict of the final review: green with edits. The inline edits are
applied above. These structural amendments take precedence over sections
10, 12, 13, and 14 where they differ.

### 16.1 Keep/delete manifest

Phase 1 produces a manifest per package and per file under `internal/`,
with a target line count for 3.0. `internal/cli` is about 74.5k of the
111k non-test lines and is the main deletion target; section 10 names
only about 12k lines. Packages with no stated fate get one:
`compoundrelease` stays (release gate flow), `state` is reviewed line by
line, `commandevidence` is replaced by the evidence ledger,
`reviewrebind` and `team` shrink to what `squad.yaml` needs. Phase 2a
executes the manifest alone, before any parallel phase edits the tree.

### 16.2 doctor

`doctor` survives as the probe in section 5: tool detection, versions,
login state, local model probes, latency, sleep and login-service
warnings, and the status contract self-check. The v2 team, pane, and
wake-mirror checks go.

### 16.3 Phase 4 additions

- The pi package implements pi bridge revision 4 (tool approval and
  session identity), because the reference bridge is revision 3 and Buzz
  approvals on a pi operator need revision 4.
- `amq-squad status --fleet --json` reads `~/.amq-squad/fleet.yaml` and
  is the only fleet input for amq-noc.
- R7 and R8 (solo first run, digest before any write) are delivered here
  with the built-in pi package.

### 16.4 Phase 5 additions

Uninstall (Claude plugin, `~/.amq-squad/runtime`, the amq-remote
approval hook); TypeScript package CI; darwin arm64 and amd64 bundles
with the embedded runtime, codesigned; scheduler restart recovery from
the task store; MIGRATION.md for v2 users.

### 16.5 Process ownership

The amq-squad binary starts and supervises one scheduler per squad. The
scheduler recovers from the task store on restart. The lead and the
mayor run on the built-in pi with a named default model from the
registry, on API keys. The fleet registry is `~/.amq-squad/fleet.yaml`.

### 16.6 Requirement to phase map

- Phase 1: R3, R5, R12 probe design, R46; spike exit criteria below.
- Phase 2a: R6, the three verbs behind R35 and R36, section 10 deletions.
- Phase 2b: R9, R10, R11, R13, R17, R18, R19, R20 to R28, R43.
- Phase 3: R16, R30 to R34, R37.
- Phase 4: R1, R2, R4, R7, R8, R14, R15, R29, R35, R36, R38 to R42, R44.
- Phase 5: R45, R47, migrate, uninstall, CI, bundles, docs.

### 16.7 Spike exit criteria (pass or fail)

1. A typed-gate answer is signed by the owner's Buzz key (BIP-340) over
   the gate id, the action class, and the target digest, carried by
   `amq-remote`, verified by the Go core, and wrapped as evidence in an
   operatorauth envelope. No model process holds the owner key. Fail
   means phase 3 cannot start.
2. A seat pushes a seat branch and the merge train merges it under the
   owner's identity on a personal repository with CI-only branch
   protection, with the signed typed gate as the review.
3. A darwin-arm64 build with the embedded pi runs the lead from the
   unpacked copy.
4. One Claude seat and one Codex seat launch through the launch API with
   model and effort in the provider arguments and the wrapper setting
   only environment and role.
5. The load test numbers exist: peak RAM, 429 and usage-limit events per
   provider, wall time, merge conflicts.

### 16.8 Upstream ask 7

Notify chaining for Codex, so a second notify hook can run after AMQ's
session-identity capture. Until then Codex evidence comes from AMQ
capture or the Codex session files, and the weekly routing report uses
only runtimes with complete evidence.

### 16.9 Owner answers to the phase 1 questions (2026-10-20)

- Codex version: the exact 0.147.0 capture pin is being fixed upstream
  (avivsinai/agent-message-queue issue #1107, PR #1108: accept codex-cli
  at or above the verified floor, verified on 0.154.0). Aviv owns the
  merge. If a release ships before the spike, no 0.147.0 install is
  needed. This is upstream ask 9.
- Bot identity: none. The owner owns every GitHub identity. The "one
  review" rule in R34 is dropped; the owner-signed typed gate is the
  review; branch protection requires CI only.
- Load test: amq-noc and workspace-cli, two squads of three seats, 25
  tasks. Twelve-seat caps are extrapolated and labeled.
- Branch: a long-lived `v3` branch. `main` stays frozen at v2.31 until
  the 3.0 release, because the plugin marketplace points at `main`. The
  `docs/v3-plan` branch merges into `v3`.
- API key for leads: not needed in phase 1; closed.
- Step zero before the spike: upgrade `amq` from 0.77.3 to 0.98.x and
  install `amq-remote` and `amq-owner-sign`.
- The spike starts only on the owner's explicit go.
