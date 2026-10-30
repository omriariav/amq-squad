# amq-squad 3.0 plan, revision 2.7

Date: 2026-10-25. Status: phase 2 is closed. Brief 2b-2 milestone B
merged at 84f1524b on 2026-10-25, and phase 5 is next; its inputs and
the open acceptance rows of phases 2 to 4 are in 16.4. [2.7, milestone B
result] The phase 1 spike has reported.
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
integrated amendments 19 to 22 and the 2a-1 result, tagged
"[2.5, <source>]". Revision 2.6 integrated amendments 23 and 24, the phase 2a close and the phase 2b/3/4 briefs, tagged
"[2.6, <source>]". Revision 2.7 integrates amendments 25 to 30, briefs
rulings 15 to 19 and the phase 2 results, tagged "[2.7, <source>]"; some
2.7 tags were written merge by merge before the revision. Section 17
lists every change of each revision.

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
The solo lead keeps a coding agent's tools, Bash included. The team lead
of 3.3 has no Bash tool and dispatches anything that runs a command to a
seat (R19). [2.7, amendment 26]

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
- amq-noc is the screen for the fleet. It is a command center: rows grouped by
  project, four buckets (Needs you, Working, Ready, Inactive) as tabs, a
  details pane (4.7). It renders the status contract. [2.6, amendment 24]
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

Launch-time integrity: the manifest digest baked into the binary is the
trust root. Every launch re-hashes the whole unpacked runtime tree against
it, refuses a changed, missing, extra or symlinked file, and checks every
directory on the path to the entrypoint for symlinks. The package pi runs
is shipped pre-transpiled inside the verified payload, and the launch
disables pi's transpile cache and sets TMPDIR to a private directory under
the seat's own extension directory, so no unverified intermediate sits
between verify and exec. Right before launch the
launcher re-hashes by content node, the preload, the pi entry and the
package files through held descriptors, and a one-purpose preload that
node loads before pi's entry re-hashes every manifest file as its first
statement and exits with a named reason on mismatch, which the launcher
(now the parent of the pi process) turns into a seat refusal written to
the ledger; the window between those two checks is the accepted
same-user limit: about 221 ms as 4-1 measured it at its end, about 295 ms
at the second Codex pass, and the larger figure is the one amendment 25
records. [2.7, amendment 25 addendum] The read-only runtime image that
would close it is deferred to 3.1 by owner decision (2026-10-25, launch
speed); the window stays a recorded same-UID limit of 3.0. [2.7, owner
decision] Measured on brief 4-1 (2026-10-24, medians of
five cold starts with the runtime unpacked and the model loaded, final
build): the full re-hash is 147.3 ms over 6,441 files through the
descriptor walk; the launch rest is 56.7 ms, of which 42.3 ms hashes
node's 112 MB, inside the 60 ms line; node start to pi ready is 516.6 ms
including about 200 ms for the preload's re-hash; spawn to first token
is 859.5 ms. The spike's 43.5 ms
was the launch cost without the re-hash. [2.3, amendment 8; 2.6,
amendment 25; 4-1 result]

The pi package registers native `amq_send`, `amq_drain` and `amq_reply`
tools. It puts the role text in the first message and also in the system
prompt. The appended system prompt alone failed in the spike: the model
read AMQ as "Advanced Message Queuing". It restricts the model list to the
seat's registry entry. [2.3, amendments 11 and 12]

pi seats launch and resume through the upstream pi mint-mode adapter
(issue #1153, ask 1; PR #1188, on main after v0.107.0). It is a
mint-mode adapter like Grok's: `pi --session-id <launch nonce>`. Upstream
refuses pi below 1.2.0 and any non-release build. Our embedded 1.3.0
passes, so the pinned-version rule is an upstream check plus our pin, and
the pin stays. The identity never comes from the bridge session id, which
appears after start and changes on `/new` and on a fork. The S5
`amq coop exec` path is the fallback (4.3). [2.6, amendment 23]

Upstream resume is not fail-closed. It runs `pi --session-id <recorded id>`
in the recorded cwd and relies on pi's create-if-missing on purpose,
because a seat that never took a turn has no session file. Our fix is on
our side and needs no new upstream ask: `seat-exec` refuses a
pi resume when the evidence ledger shows at least one tool result and the
session file for the recorded id is missing. That is the only case where
create-if-missing loses context silently, and `doctor` reports the condition. It lands in phase 2b (brief 2b-2). [2.6, amendment 23] [2.6, review]
It landed in 2b-2 milestone A as the refusal `pi_resume_context_lost`,
tested with a fake pi. [2.7, 2b-2 milestone A result]

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
they enter the trust digest. This holds for Claude and Codex seats only.
pi admits no provider arguments (`allowed_argument_forms` empty, grammar
1), and managed pi seats are unnamed like Codex seats. A pi seat's model
and effort therefore cannot travel in the launch request or the trust
digest yet. The pi model restriction stays on our registry side (R16) plus
pi's own config. Watch item: the day upstream admits pi forms. [2.6, amendment 23]
The wrapper takes only `--seat <handle>`;
there is no `--root` flag, and `AM_ME` and `AM_ROOT` come from the
environment. [2.3, amendment 7] It
writes the launch record and the role into the seat's extension directory,
sets the seat environment, and execs the provider in place. It never adds
provider flags. Two exceptions are recorded. The built-in pi runtime is
not exec'd in place: its launcher supervises node as the parent,
forwards signals, passes the exit code through, and turns the preload's
exit 70 into a seat refusal written to the ledger; for a pi seat the pid
and birth time seat-exec records are the launcher's, and CloseSeat
targets it. [2.7, amendment 26] For a Codex seat seat-exec adds the two
config pins that only narrow the sandbox
(`sandbox_workspace_write.writable_roots=[]` and
`sandbox_workspace_write.network_access=false`), which AMQ 0.107.0 does
not admit into the trust digest, and it refuses an argv that sets
`sandbox_workspace_write` itself; upstream ask 17 asks AMQ to admit the
two keys as provider arguments so the exception can go. [2.7, amendment
28] The Claude Code hooks come from a user-scope plugin that
reads the seat environment; the Claude grammar has no plugin or settings
flag. The hook ships as that user-scope plugin, never as a project-scope
stub. [2.3, amendment 7] In a seat the permission boundary is guard: the
plugin answers an allowed call with an explicit allow, so Claude's own
permission prompt and permission rules never stop an unattended seat;
seat-exec sets the Claude permission mode and the hook as one unit at
launch and refuses to start a Claude seat whose plugin is absent (a
preflight check with a reason). [2.7, 4-2 result] Corrected after the
hooks-API re-check of 2026-10-25 (a hook allow does not override the
owner's deny or ask rules; dontAsk runs only pre-approved tools): the
pre-approval is the seat's settings allow list, written from the launch
record into the per-seat settings file, and the hook is deny-only. dontAsk
auto-denies everything outside the list; the plugin's PreToolUse emits
deny or nothing and never allow. The grounds: the hook is UX and fails
open to no decision, so if pre-approval depended on a hook allow, a hook
failure would turn every would-prompt call into an auto-deny and the seat
would stall on nothing; with the allow list as the approval, a hook
failure leaves the owner's rules in force and loses only the amq-squad
deny layer, which is the direction R36 wants. The owner session's live
row confirms the auto-deny on a tool outside the list with the hook
silent; if it shows that a hook allow can run a would-prompt call, that
is a capability 3.0 does not use. The adapter's allow emission is removed
after that live row. [2.7, hooks re-check, reviewer ruling] Since milestone B the
unit is permission mode dontAsk with a core-written seat settings file
and a per-seat CLAUDE_CONFIG_DIR, and the seat authenticates with the
owner's `claude setup-token` file; R36 holds the rule. [2.7, milestone B
ruling] The Bash-tool scrub candidate (CLAUDE_CODE_SUBPROCESS_ENV_SCRUB)
is withdrawn for Claude Code 2.1.310, where it forces permission mode
`default` and defeats dontAsk; the token's visibility to the seat's Bash
children is the recorded limit (R33). The hooks report the config-dir
digest so that a removed hook is itself the drift signal: the context
call reports it today, guard and ingest report from the phase 5 re-pass
fix. [2.7, 4-2 re-pass] The launch probes every Claude seat's effective
permission mode by running the installed claude with no credential and
reading its stream-json init event, then killing it, about 0.3 s per
Claude seat, accepted inside the launch budget; a forced mode refuses
the seat. The probe runs the pinned claude (the registry pin, never
PATH) with a scratch HOME, the full per-seat config dir as the seat will
see it, a scratch cwd with no project files and hooks disabled by the
seat's own settings; it runs before any ticket is minted and before
Apply, once per seat kind per launch when the settings are identical,
and a probe over 5 s is claude_mode_unverified. For a Claude seat the
executable is the registry pin, never a provider path: the compiler takes
the pin as the executable and refuses a provider path that is not the
same file (provider_not_pinned), and seat-exec re-opens argv[0] through
the pin (path and sha256) immediately before exec and refuses a mismatch
(tool_unpinned), so the probed bytes are the executed bytes. The Claude
hook script implements no watchdog in shell: `guard --decision claude` owns the
timeout, runs the guard in its own process group through the Go exec
API and kills and reaps the group on both platforms; the script only
pipes stdin to the adapter and prints its output. The adapter's
no-decision (a timeout, a truncated or absent verdict, output over 64
KiB) is fail-open and logged by design: hooks are UX, enforcement is in
the core. [2.7, 4-2 re-pass, reviewer]

The admitted argument forms come from upstream. AMQ derives
`AllowedArgumentForms` from its own validation tables and settles the
`grammar_version` rule (issue #1154, ask 4, shipped in PR #1175, AMQ
v0.106.2). `seat-exec` carries no list of forms of its own and uses the
advertised ones. [2.5, amendment 20] [2.6, upstream status]

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
its first use in phase 2b. [2.3, amendment 7] They landed with 2b-2
milestone A, on 2b-3's descriptor-anchored traversal. [2.7, 2b-2
milestone A and 2b-3 results]

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
is a phase 2b decision. [2.4, amendment 17] Decided in 2b-2 milestone A:
the scrub removes `SSH_AUTH_SOCK` and `SSH_AGENT_PID` (2b-2 ruling 3).
`launchctl getenv SSH_AUTH_SOCK` re-exposes the launchd agent socket, so
guard denies it as a credential read (3-2, R33). [2.7, 2b-2 and 3-2
results]

`seat-exec` denies writes to the owner's global agent config,
`~/.claude/settings.json` and `~/.codex/config.toml`. `doctor` diffs
that config against the preflight copy. Evidence: S6 finding B, where
`~/.claude/settings.json` changed during the run at 06:56, unattributed. [2.4, amendment 18]
No same-UID process can truly deny another's write, so that enforcement
is layered and named. For a Codex seat the sandbox is the enforcement,
and seat-exec refuses to start a Codex seat without it. For a Claude seat
guard denies the write, and the harness runs doctor's config diff
against the preflight copy, with the Claude config-dir digest, on every
terminal path of a seat (done, fail, gone, expired). A change blocks the
task with `global_config_changed` routed to the owner, before the commit
step: the seat's diff stays uncommitted and unpushed under the commits
ledger, the seat is quarantined (no new lease for its handle), and the
config diff is the block's evidence. Only the owner clears it, with
`seat clear`: it takes no identity from flags or the environment, runs
only from the real pty of stdin, compared by device number against
every tmux pane pty and refused on a match, reads the handle with echo
off under a 60 s timeout, and writes a signed clear record; an open or
lookup failure is `owner_proof_unknown`. By 7.3's model a same-UID shell
that is no seat pane and not descended from one is the owner; the
Buzz-signed clear is phase 5's answer (16.4). [2.7, amendment 28]
With the owner's Buzz enrolment present (grant `clear.signature`:
`required`, the default then) the pty is only the request half: `seat
clear` raises a typed gate of kind `quarantine_clear` (nonce, handle,
seat incarnation, the config diff's digest), posts it over Buzz, and the
clear completes only when the owner's signed answer to that gate verifies
as the four typed gates' answers do; the signed clear record then carries
the gate id and the answer's event id. A clear with no answer is
`clear_unsigned` and the quarantine stays; Buzz unreachable leaves an open
gate and no fallback. `pty_only` is accepted only with no enrolment (with
one it is `grants_invalid`). [5-clear]

A per-seat CODEX_HOME is rejected; the env overlay allowlist cannot carry
it. [2.3, amendment 4] codex-cli rewrites its own `config.toml` at start
(`[marketplaces.*] last_updated`). The docs say that codex-cli owns that
file and amq-squad never writes it. [2.4, amendment 17]

A fresh codex-cli seat stops on its folder-trust prompt until the project
is trusted in the owner's Codex config. amq-squad never writes the
owner's Codex config and never answers the prompt for the owner. Phase 2b
adds an owner-run `amq-squad trust <repo>` step and a doctor check that
lists untrusted roots before a Codex seat launches. [2.3, amendment 1]
Both landed in 2b-3. codex-cli trusts by repository root (7.3), so each
seat repository needs its own trust step. [2.7, 2b-3 and milestone B
results]

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
never widens its own set. [2.5, amendment 21] The widening reaches the
core as `task widen` over the scheduler's own Unix socket (7.2). [2.7,
amendment 28] Claude seats take the same path,
although Claude can commit: one commit path, not two. `seat-exec` execs
the provider in place, so the commit runs as its own harness step after
the done report; the exact shape of that step is a phase 2b design
item. [2.5, amendment 21] The shape, from 2b-2 milestone B: the
scheduler process (4.5) runs `seat commit` on the done report. It
matches the seat diff against the task-declared file set, snapshots the
tree with `hash-object --no-filters` and `update-index` into a temporary
index (never `git add`), and pushes only to the grant's `merge.push_url`
(7.2) from a per-step shadow git directory. The harness never reads
configuration from the seat repository: every harness git call runs
with GIT_CONFIG_GLOBAL=/dev/null, GIT_CONFIG_NOSYSTEM=1,
GIT_NO_REPLACE_OBJECTS=1, GIT_ATTR_NOSYSTEM=1, empty attributes and
hooks paths and every `filter.*` unset, and the push URL and base branch
come from the core-written launch ledger. [2.7, amendment 28] The step
then opens the draft PR as the harness, inside the standing grant's
"open PR", and binds the task through `task.Store.BindPR`
(`pr_repository`, `pr_number`, `pr_head`) in one transaction with
completion. An open PR on that branch bound to another task is refused
(`pr_bound_elsewhere`), never reused, and the binding in the store,
never a body trailer, is the PR's provenance. [2.7, briefs ruling 19;
amendment 28] Between leases CleanSeat resets the seat and keeps the
discarded diff on the ledger. [2.7, milestone B result]

pi seats start through the launch API like Claude and Codex seats, with
the upstream mint-mode adapter in 4.1 (issue #1153, PR #1188). The caveats
in 4.1 and the provider-argument limit above apply. `amq coop exec` is the
fallback. On that path pi seats get no digest-gated apply, resume, or
liveness from AMQ, and the status says so. [2.6, amendment 23]

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
stays in the Go core; hooks are UX and evidence. [2.3, amendment 3] The
3.0 plugin (4-2, 2026-10-24) re-ran the table through a hook runner that
models Claude's decision rule, hook process time alone: deny 19 ms, timeout
5.002 s at the 5 s setting, exit 1 17 ms, missing command 10 ms, outside a
seat p95 6.4 ms; the live confirmation against a real Claude seat is owed
to 2b-2 milestone B's live run (deviation recorded). [2.7, 4-2 result]
Milestone B's live run did not reach the Claude rows; they wait for the
owner's `claude setup-token` file and are a phase 5 owner-session item
(16.4). [2.7, milestone B result]

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

The scheduler runs as its own process, `amq-squad scheduler run`,
started and supervised by the lead's team mode (`lead --team
--scheduler-seats`). It is wired to 2b-2's seat launcher, uses the AMQ
wake as the doorbell, runs 2b-4's router and its Flush every tick and
the milestone B commit step, and takes 2b-3's usage-limit source. At
start it holds a barrier: adopted seats are closed and their close
confirmed, the exits ledger is loaded and writable, and its decision
socket stays closed until then, so nothing is dispatched before. A slot
is released only at seat_gone, deferred exit checks are persisted, and
the seat cap counts the seat list's slots. Restart recovery from the
task store stays phase 5 (16.4). [2.7, briefs rulings 15 and 19;
milestone B result]

The roster launch (3.0.1, reviewer ruling 2026-10-30). AMQ refuses a
fresh seat into a session's present binding unless every planned seat
resumes (`binding_present_without_resumable_conversation`, v0.116.2
reconcile), so a scheduler that launched one seat per request ran a
single-seat squad. The scheduler therefore launches the roster, every
seat of the typed seat list with a worktree of its own, in one AMQ
prepare and one apply at start (one binding, every seat fresh); a seat
that holds no lease is parked and gets the next task of its pool, and
the seat cap and the registry's concurrency pools bound the leases held
at once (max_seats caps live leases, not live panes). A restarted
scheduler adopts the live roster seats it re-proves from the launch
ledger and closes the rest. The roster's tmux session holds seats only: the
lead's seat is left out of the roster, and the team-mode lead and its
scheduler run in the owner's terminal, the scheduler taking its team-mode
parent as the lead for decisions. A gone seat is replaced only by relaunching
the roster: its pool's next task blocks for the lead
(`seat_replacement_needs_relaunch`), whose decision `amq-squad scheduler
relaunch` lets the remaining seats finish, closes them and launches the
roster again in one apply, until AMQ can join a fresh participant into a
present binding (ask 20). A roster launch that fails at prepare or apply
is one decision with its stage, AMQ's reason and the detail, and the
process exits with it, an exit the lead's supervisor does not restart;
`lead --team --trust` passes the owner's standing trust answer through. [3.0.1 t16; docs/v3-spike/5-301-evidence/t16-roster-launch.md]

### 4.6 Status contract

`amq-squad status --json` is a versioned contract. It carries:
`schema_version` with additive minors, `producer_version`,
`generated_at`, root and session; per seat: handle, role, runtime,
model, effort, state from a closed list plus `unknown`, reason code, tokens
in and out (null when the runtime does not report them), last message,
prompt state; an
`attention[]` list with stable ids, kind, severity, subject, since, and
a suggested action as argv plus an action id, never a shell string; per
task: id, title, state, lease holder, lease expiry, pool; gates with id,
action class, target digest, state; the "new task" action as argv plus an
action id; and a `capabilities[]` list. [2.6, amendment 24] A golden
fixture in amq-squad CI is vendored by amq-noc. The five-state
classifier (needs-you, blocked, waiting, online, stale) and the
attention queue move from amq-noc into amq-squad.

The contract also names the bucket of each row. A table written in 2b-1
maps the classifier states to the four command-center buckets, and amq-noc
reads it. [2.6, amendment 24, briefs ruling 6]

| Bucket | Rule |
| --- | --- |
| Needs you | an open typed gate, or a refusal routed to the owner |
| Working | a live lease with a task |
| Ready | a live lease and no task |
| Inactive | an expired lease or an ended session; the state `unknown` also lands here, with a marker |

Two classifier states fit no rule above: `blocked` and `waiting`. The
proposal: `blocked` goes to Needs you only when routed to the owner, else
to Working; `waiting` goes to Working. 2b-1 fixes the table. [2.6, briefs 2b-1 and 4-4]
2b-1 fixed it as proposed, and the contract carries both the classifier
state and the bucket per row. [2.7, 2b-1 result]

The fleet form, `status --fleet --json`, is one document:
`{"schema_version": 1, "kind": "fleet", "producer": "amq-squad",
"producer_version": "...", "generated_at": "<RFC 3339 UTC>", "squads":
[...]}`, with one unchanged schema 1 status document per squad. The
single-squad document carries no `kind`; a reader treats a document with
`squads` as a fleet and anything else as one squad. The operator mailbox
is the owner's `user` handle, and the contract carries no operator
field. [2.7, briefs ruling 16]

### 4.7 amq-noc

amq-noc 1.0 is a command center in the shape of the reference screenshot
(`docs/v3-research/codex-agent-command-center-2026-10-24.png`). It is the
only view. The old board view is removed, not kept beside it (owner
decision 2026-10-24). It reads the status contract and AMQ primitives
only. It never reads `.amq-squad/` files and never composes amq-squad
commands. [2.6, amendment 24]

The screen has rows grouped by project path, each group with a count and a
"show more" fold for long groups. Four buckets work as tab filters with
counts: Needs you, Working, Ready, Inactive, plus All. A row shows the
title, the status and the relative updated time. A details pane for the
selected row shows status, model and reasoning effort, project path,
tokens in and out, the last message and the prompt state. Keys: arrows,
enter, `n`, esc, `?`, tab to cycle the filters. [2.6, amendment 24]

A row is a task in the 2b task store, joined to its seat in the registry
(model, effort) and to the evidence ledger (tokens, last message). The
buckets follow the table in 4.6. Needs you is the set of open typed gates
and refusals routed to the owner (7.1, 7.2). Ready is a seat with a live
lease and no task. Inactive is a seat whose lease expired or whose session
ended. [2.6, amendment 24]

Its controls are four. Copy the action from the contract. Open the AMQ
conversation with enter; it does not focus a tmux pane. Show a gate. Press
`n` for a new task: that runs `task add` from the argv the contract
carries, so the renderer never composes a command, and dispatch is then
the scheduler's act (amendment 25 corrects the earlier "dispatches
through the kept `dispatch` verb"). [2.7, amendment 25] Gate answers
follow the signed path. It keeps the fleet registry view and the action
log. [2.6, amendment 24, briefs ruling 6]

The old board's delete list is 124 files and 57,610 lines: everything
reachable only from the board or from verbs that read `.amq-squad/` or
composed amq-squad commands. The 4-4 execution log names where each
removed verb's job lives in 3.0. Measured frame time: 0.29 ms at 250
rows against the 100 ms limit. [2.7, briefs ruling 17] amq-noc 1.0
lands by PR on the release train (16.4). [2.7, 4-4 status]

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
with no verifier never starts cheap. The mechanical verifier runs and its
verdict is recorded: a class with a mechanical verifier completes only on
a verdict the harness recorded (the commit step's test run or the
probe's predicate), never on the seat's report; a done report with no
verdict is blocked (`verdict_missing`) and the attempt counts as failed
for escalation. [2.7, 2b-4 re-pass ruling, 2026-10-25] That applies only
to a class whose verifier command is configured in the owner grant
(`verifiers:`, class to command; squad.yaml may add classes to the gate
but never remove one the grant names, and a list that omits a configured
class is grants_invalid), never read from the seat's tree; a class with no
configured verifier routes at its tier and never below, completes on the
done report, and records verifier_result "none", never "pass". The
producer is `seat commit`: in the order exit check, snapshot, diff checks
on the snapshot, verifier, second PreflightDiff, sealed verdict, push and
PR, it runs the class's verifier in the shadow worktree, bounded (10 minutes and 1 MiB of output by
default, grant-settable), with the command and arguments from the grant
or squad.yaml only and the hardened harness environment, and records the
verdict with the digest of the tree it ran on; the R12 probe records its
predicate the same way; RecordVerdict is reachable only from the harness
(the scheduler process), never from a seat verb. A gated class with no
command never reaches a seat: the start checks and the claim path refuse
dispatch of that class with the class and the missing command named
(grants_invalid for the verifiers key), so the done-report gate fires
only on a configuration change during a lease. The verifier runs with
the host network like a seat and no credential (a recorded limit: a
no-network default is not buildable on macOS without a sandbox we do not
have, and the module fetch needs it); its Go module cache is verifier-only
under the session root (0700, GOMODCACHE, never the harness's or the
owner's, GOFLAGS=-mod=readonly, GOSUMDB on, GOPROXY from the grant),
while GOCACHE and GOTMPDIR stay private per run; cross-seat cache
corruption is the same-UID limit beside the seed. The commit step runs
off the scheduler's tick loop under its parallelism slot: the tick
observes its state and keeps dispatching, cancelling and checking other
seats; a restart adopts a running step through the exits queue as a
deferred check and never re-runs the verifier on the same snapshot. An
escaped descendant that leaves the process group is a recorded limit on
unix (the bound stops the wait, the orphan belongs to the seat_gone sweep
or the owner); no seat host exists off unix in 3.0. A verdict is reused
only when its tag verifies (an untagged or wrong-tagged one is
verdict_missing and the verifier runs again); the capture is redacted
before it is truncated and a truncated capture ends at a line boundary
with a marker; a verdict for the same tree with another command digest
supersedes the earlier one, the supersession inside the sealed bytes, and
the newest verdict is authoritative at completion; a grant command may be
a string or a YAML list; the hook ledger's one writer redacts on write
and keeps a malformed payload as length, sha256 and a redacted printable
prefix. No lock reachable from the scheduler's tick is held across a
subprocess, a network call or a wait on another lock (the task-store lock
stays blocking and its holders stay short): the exit check for a seat
with a running step is deferred without touching the ledger, every
ledger lock on the tick path is a try-lock whose busy result is a
deferral, and CleanSeat's git work runs outside the store lock behind an
unclean marker in the task store (a nonce-carrying file, never a timed
lock: while it exists Grant and Claim refuse seat_unclean whatever its
age, only a completed CleanSeat removes its own nonce, after the TTL
another cleaner may take the marker over, and a malformed, unreadable or
far-future marker is seat_unclean with an attention row, never fail-open;
a ledger-read error in prepare is seat_prepare_failed, no grant). Merged
2026-10-25 as 38eafc83 (branch v3-5-verifier, fifteen commits, six Codex
passes with fix rounds, 91 reverting rules, lead final read and gate on
the merge result; size of 3.0 75,399); the merged head joins no re-pass
list; the orphan-adoption row and the hygiene rows stay on the phase 5
lists. [2.7, phase 5 verifier result] wherever a capture has a cap,
redaction runs first, then the cut, and a partial last line is never
kept (the verifier capture, the lead's tail, the ingest prefix); a one-line
YAML `key: value` under a credential name is redacted in every form (bare
or quoted key, list item, flow mapping); a quoted YAML scalar is redacted
whatever it contains, through its closing quote to the end of the capped
buffer, or to the last more-indented line when unclosed, and every line indented deeper than a credential-named key is
redacted whole until the indentation returns; redaction of a capture runs over the whole
retained buffer with the key rule allowing JSON whitespace between key,
colon and value, and per line as well; a cancel during a running step
kills the verifier group and frees the handle when the step ends; an
attempt archives a verdict only after its seal check, else "invalid"; a
finished step's result is dropped only after the store transaction
commits, and a failed application is retried, never the step; the
deferral count lives in the exits entry and at the limit the task is
blocked verifier_interrupted for the lead's reset. Phase 5 branch
v3-5-verifier. [2.7, reviewer ruling and Codex passes, 2026-10-25]

### 6.2 Chains

Each tier is an ordered list of (runtime, model, effort). The scheduler
takes the first entry that passes four checks: verified, privacy class,
concurrency cap, cost cap. No passing entry: the task waits and the
status names the reason. Pool occupancy against the concurrency cap is
the scheduler's seat view, each seat counted once: live, pending launch,
closing, and the lead's own seat when it shares the pool; tasks are not
the unit. An idle seat of the pool is capacity for that pool's pending
task, not a consumer of it; an asynchronous launch failure holds its
occupancy until the timeout and the confirmed cancellation. The lead's
pool reaches the scheduler process through `--lead-pool`, refused when the
process was not started by the lead's team mode. [2.7, 2b-4 re-pass
ruling, 2026-10-25] Local-only paths never go to a vendor.

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
Both files are a strict YAML subset (comments, scalar keys, flow lists of
scalars, block lists and maps); anything outside it, including anchors,
aliases, tags, a second document or a repeated key, is `grants_invalid`
for guard and a parse error for team, and narrowing keys accumulate or
take the minimum, never replace. [2.7, amendment 27]
After the first usage-limit event in the window no new seat starts
(section 5). [2.5, amendment 22] The fleet grant adds: maximum squads,
cost per day, permitted repositories.

Merge to the default branch is a rules decision by the lead under a
standing merge grant set once per repository. The grant's rules: CI
green, the change inside the task scope, no protected-path change, a
size cap, no force push. The Go core enforces the grant's limits
(repository, base branch, CI green, size cap, no force push, no
protected-path change). The grant also names the push target,
`merge.push_url` (milestone B review, ruling D1): the harness pushes seat
branches to that URL alone, recorded into the commit ledger at launch and
never read from the seat repository's config; without it the harness
commits and does not push (`push_url_missing` in status); squad.yaml
cannot set it. The URL is SSH (`ssh://` or `git@host:`), HTTPS with the
host exactly github.com and no userinfo, or an absolute local path or
`file://`; `http://`, `git://`, `ext::`, `fd::` and other HTTPS hosts
(until a GitHub Enterprise ruling) are refused when the grant is parsed
(`grants_invalid`, surfaced by doctor). A local-path target has no
server-side ruleset behind it, so a real run accepts it only with
`merge.local_push_ack: true` set by the owner, doctor warns that the push
target has no server enforcement, and the merge train refuses a real run
against it (dry run only). The lead decides inside them. A merge outside
the grant is refused by the core, not by the lead's judgment, and that
refusal escalates to an owner-signed gate. The model is never the
enforcement point. Manual mode keeps the per-merge stop; auto mode is
the default. [2.4, S2 report owner decision] In 3.0 the core enforces
the six named limits at the merge. Task scope is enforced earlier, at the
harness-side commit (4.3): the Go core refuses a seat diff outside the
task-declared file set before it is committed, and the refusal goes to
the lead, who may widen the set inside the grant (4.3); the widening is accepted only from the scheduler process, which verifies the lead's decision as the lead's AMQ message against the lead handle in the launch record and the task's squad, never from a `--me` flag or the environment (milestone B review, ruling R1). Evidence: S6
task W5, a file outside its task. [2.5, amendment 21] Later passes
tightened that path (rulings R4, R5, R8, R9). The scheduler's mailbox is
same-UID writable and AMQ derives a sender from the caller's AM_ME, so
the AMQ message is the lead's record of the decision, never its
authority. The decision reaches the scheduler as `task widen` over the
scheduler's own Unix socket and is accepted only when the connecting
process's stamped parent chain reaches the lead's recorded incarnation:
the pane id, pane shell pid and start stamp written to the launch ledger
when the lead seat proved up, re-proved after the request. A pane label
is not an identity and is never consulted after launch; a chain that
reaches no recorded lead is `origin_unknown`, a refusal and never an
alarm, and a lead that did not prove up through the seat launch cannot
widen. [2.7, amendment 28]

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
  codex-cli trusts by repository root, not by ancestor directory
  (milestone B live run, 2026-10-24: a seat repository nested under a
  trusted checkout still stopped at the prompt), so every seat repository
  needs its own `amq-squad trust <path>` from the owner before its first
  Codex seat. [2.7, milestone B result]
  A `workspace-write` Codex seat never commits. The sandbox refuses every
  `.git` write, in a linked worktree and in a plain checkout, with and
  without `--add-dir` (codex-cli 0.163.0, `index.lock: Operation not
  permitted`). The harness commits and pushes the seat branch (4.3), and
  ask 5 is withdrawn. [2.5, amendment 21] seat-exec refuses to start a
  Codex seat without the sandbox and adds the two narrowing sandbox pins
  of 4.3 (upstream ask 17). [2.7, amendment 28]
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
  The train reads a task only through the binding the harness commit
  step writes (the PR number and the pushed head, 4.3; keyed by
  repository and PR number through `task.Store.BindPR` [2.7, 3-2 and
  milestone B results]): a request names
  the PR, never the task, and a PR with no bound task is refused
  (`task_unbound`); a PR head that differs from the bound head is refused
  (`pr_head_moved`); when the train itself rebases and pushes, it re-binds
  the task through the same path before the merge, so the binding is the
  one source of truth. [3-2 Codex pass 2, 2026-10-24]
- The release flow is a separate remote operation from the tag push, and
  GitHub keeps an existing tag's commit when a release names it, so the
  flow refuses to run (`protection_unmet`) unless the repository has a
  tag protection rule that restricts tag update and delete to the owner;
  that rule is part of the branch-protection check before any real run.
  It then publishes in two steps: create the release as a draft, read it
  back and compare the tag's commit to the approved SHA through the API,
  publish only on equality; a mismatch deletes the draft and raises
  `tag_moved`. [3-2 Codex pass 2, 2026-10-24] The tag rule counts only
  when its include patterns are exact (`refs/tags/<tag>`) or a
  single-segment prefix glob (`refs/tags/v*`), with no `**`, no `~ALL`
  with exclusions and no exclusion list, and no bypass actor; anything else
  is `protection_unmet` with the pattern named, because the flow cannot
  reproduce GitHub's matcher. The read-back pins the draft's id, tag name
  and target to what was created and resolves the tag once; the publish
  re-reads the draft immediately before and requires them unchanged, else
  deletes it (`tag_moved` or `draft_edited`); the draft id sits in the
  publication lock record from creation so recovery deletes or completes
  an orphan, and every deletion and its result is audited, a failed one as
  an owner alert. [3-2 Codex pass 3, 2026-10-24]
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
  changed. [2.5, amendment 20] The signed question text and its tags
  carry the squad identity (the AMQ root digest or the namespace id), and
  the gate record stores it. The verifier selects the record by squad
  identity plus gate id plus thread, never by gate id alone, and a record
  without squad identity is unverifiable. Evidence: two squads raising
  the same gate id, action and target in the same second produced
  identical question event ids (4-5 re-pass). [2.7, amendment 30]
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

The strict share is bound to amq-remote's mailbox adapter (upstream ask
15, shipped in v0.111.0 and v0.112.0; floor v0.116.2): serve reads the
owner's DMs and delivers each into the share root's `attach` inbox from
`buzz` with the `nostr:<eventID>` label, and the attachment's one reply
per message (`accepted <gate> <class>`, `refused <gate> <code>`, `routed
<dest>`; ids and code tokens only) is serve's result row. serve's delivery
is a notification, never the authority: the core fetches the signed event
by id on its own short connection and verifies it. The attachment's own DM
reader and its marks are gone (4-5 deviation 1 closed). reply_timeout is
60 s; a verification is capped at 10 s. [5-amq-B, ruling 20]

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

Upstream status on 2026-10-24: asks 1, 4, 8 (part 1), 11, 12 and 13 have
shipped, and issues #1153, #1154, #1155, #1156, #1157 and #1158 are closed.
They landed in releases v0.106.0 to v0.107.0 and on main. Ask 14 (#1159) is
open with PR #1186. Issue #1178 is open for the pi bridge part of ask 8.
Criterion 4 passed on v0.106.1 on 2026-10-23. [2.6, upstream status]

Phase 2 drafted three more asks in `docs/v3-upstream-issues/`. Filed
2026-10-25 under the owner's login: ask 15 = #1210, 16 = #1211, 17 =
#1212; the status table of `docs/v3-upstream-asks.md` carries them. [2.7,
upstream status 2026-10-25] As drafted:
Ask 15: a mailbox adapter kind in amq-remote for a strict relay share
(4-5 deviation 1). Ask 16: amq-owner-sign sets RLIMIT_CORE to 0 before
it reads the owner key (4-5 review). Ask 17: admit the two workspace-write
sandbox pins as Codex provider arguments (4.3, amendment 28). [2.7,
upstream drafts] PR #1188 (ask 1) and PR #1186 (ask 14) are merged on
main and unreleased after v0.107.0 as of 2026-10-25; the release ask to
the maintainer stands. [2.7, upstream status 2026-10-25]

| Ask | Title | Status at v0.102.0 | Recommendation |
| --- | --- | --- | --- |
| 1 | pi adapter in the launch API | merged, PR #1188, unreleased after v0.107.0 | filed as #1153: mint-mode adapter (4.1) [2.5, amendment 20]; shipped as PR #1188, on main after v0.107.0, issue closed; its resume is create-if-missing, not fail-closed (4.1) [2.6, amendment 23] |
| 2 | Provision under a project with no .amqrc | partly shipped | skip, or file as docs fix |
| 3 | Non-interactive consent by digest | shipped | do not file |
| 4 | Advertise admitted Claude and Codex argument forms | shipped v0.106.2 | filed as #1154: upstream derives the forms from its validation tables (4.3) [2.5, amendment 20]; shipped, issue closed [2.6, upstream status] |
| 5 | Codex argv grammar v2 | not shipped | withdrawn: `--add-dir` was measured not to fix the commit, and the harness commits (4.3) [2.5, amendment 21] |
| 6 | iTerm2 launcher backend | not shipped | do not file |
| 7 | Notify chaining for Codex | partly shipped | dropped: declined by the maintainer; a fan-out script is the notify command (16.8) [2.5, amendment 20] |
| 8 | Forward the owner-signed event | part 1 shipped v0.106.0, part 2 open #1178 | filed as #1155: `nostr:<eventID>` label, and the layer verifies against the relay (7.3) [2.5, amendment 20]; part 1 shipped, issue closed; the pi bridge part is open as #1178 [2.6, upstream status] |
| 9 | Codex capture version floor | shipped on main (PR #1108 merged 2026-10-21; AMQ v0.104.0) | no action [2.4, ask 9 shipped] |
| 10 | Phone-side signing of the share grant | not shipped | file after owner decision |
| 11 | Event id escaping of C0 controls differs from NIP-01 | shipped v0.106.0 | filed as #1156 [2.5, upstream asks]; shipped, issue closed [2.6, upstream status] |
| 12 | Capture-mode Codex conversation is finalized `stale` at Apply, so `__codex-notify` never records | shipped v0.106.1 | filed as #1157 (S4 closure) [2.5, upstream asks]; shipped, issue closed, criterion 4 passed on v0.106.1 [2.6, upstream status] |
| 13 | Claude mint-path conversation record hardcodes `backend: commands` | shipped v0.106.1 | filed as #1158, minor [2.5, upstream asks]; shipped, issue closed [2.6, upstream status] |
| 14 | `launchapi` resolves the AMQ executable with `os.Executable()`, so an embedding binary cannot use a managed backend | open, PR #1186 | filed as #1159 (S6 launch blocker) [2.5, upstream asks]; open, PR #1186 [2.6, upstream status] |

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
- R1 One Go executable with a pinned pi runtime and the amq-squad pi package; the pi is `@earendil-works/pi-coding-agent` pinned with a committed lockfile on Node 22.19.0 or later, packaged as option (a) with (b2) as the only fallback, with fd and ripgrep embedded as digest-pinned copies, no run-time download, and a launch-time integrity check against the cold-install manifest. [2.3, amendment 8] The upstream pi adapter pins a verified pi version (issue #1153); the embedded pi stays on a version that adapter verifies. [2.5, amendment 20] Upstream refuses pi below 1.2.0 and non-release builds; the embedded 1.3.0 passes and the pin stays. [2.6, amendment 23]
- R2 The lead and all pi seats use the built-in pi only.
- R3 Claude Code and Codex seats start through the AMQ launch API with the wrapper `seat-exec`.
- R4 Model, effort, and sandbox flags are AMQ provider arguments; the wrapper takes only `--seat <handle>`, sets the seat environment and the role, and execs the provider in place. The role comes only from `role.md`, whose SHA-256 the Go core records in the launch record and `seat-exec` checks before exec [2.3, amendment 2]; `seat-exec` anchors traversal to the verified AMQ root and checks directory ownership and permissions [2.3, amendment 7]; a per-seat CODEX_HOME is rejected [2.3, amendment 4]; amq-squad never writes the owner's Codex config, and `amq-squad trust <repo>` is an owner-run step [2.3, amendment 1]. A seat never commits: the harness commits and pushes the seat branch (`seat-exec` on the seat's done report, or the lead), for Claude and Codex seats alike, and the Go core enforces the task-declared file set on that commit. [2.5, amendment 21] pi seats admit no provider arguments, so the provider-argument rule applies to Claude and Codex seats only (4.3). [2.6, amendment 23] The built-in pi runtime is the one provider not exec'd in place: its launcher supervises node (4.3). [2.7, amendment 26] The one provider flag seat-exec adds is the pair of Codex sandbox pins, until upstream ask 17 ships. [2.7, amendment 28] The harness commit step is `seat commit`, with the hardened git environment, the push to `merge.push_url` only, and the draft PR bound to the task (4.3, 7.2). [2.7, milestone B result]
- R5 pi seats launch and resume through the upstream pi mint-mode adapter (`pi --session-id <launch nonce>`; issue #1153, PR #1188); `amq coop exec` is the fallback. Upstream refuses pi below 1.2.0 and non-release builds, and our pin stays. Upstream resume is create-if-missing, not fail-closed, so `seat-exec` or `doctor` refuses a pi resume when the evidence ledger shows a tool result and the session file for the recorded id is missing. Model and effort cannot travel as provider arguments for pi seats; the model restriction stays on the registry side (R16) plus pi's config, with a watch item for upstream pi forms. [2.6, amendment 23]
- R6 amq-squad never creates, moves, resizes, focuses, or closes a pane.
- R7 First run without `.amq-squad` starts solo mode and writes nothing inside the repository; it writes only under `~/.amq-squad/`.
- R8 The first team proposal shows one digest for all files it creates; nothing is written before approval.
- R48 The Go core surfaces the AMQ `ApplyResult` outcome, reason code and failure detail to the operator. A count of applied commands is never the only signal. Evidence: spike log 08:08, where 2.31 read only `Commands`, reported "0 applied command(s)" and hid `action_required` / `backend_create_failed`. [2.3, spike log 08:08]
- R49 Sending a launch decision never registers the lead as live. The duplicate-live preflight counts only a seat process, not the decision send. Evidence: spike log 08:07, where a decision send wrote an active `lead` presence and the next `--apply` was refused with `duplicate_live` for 90 s. [2.3, spike log 08:07]
- R50 "Next actions" and every suggested action name only flags and commands that work on the current path; a deprecated flag never appears. Evidence: spike log 08:07, where 2.31 suggested `--force-duplicate`, which does nothing on the launchapi path. [2.3, spike log 08:07]
- R51 A launch started from the amq-squad binary always hands the AMQ launch library a real `amq` executable path, or amq-squad itself serves the `coop exec` contract. The library resolves `os.Executable()` when no path is given, and inside amq-squad that is amq-squad. Evidence: spike log 2026-10-22 06:24, where every launchapi start from 2.31 failed at tmux Create because the pane ran `amq-squad coop exec`; the legacy pane driver, which calls `amq` from PATH, launched the same roster at once. Upstream ask 14 asks for an explicit path on the request. [2.4, spike log 2026-10-22 06:24]
- R52 The launch sets the complete AMQ identity in every seat environment (`AM_ME`, `AM_ROOT`, `AM_BASE_ROOT`, `AM_SESSION`) from the launch record only, and proves its identity with one read-only check (`amq env --me H --json`), never a drain, and sends the up proof over the launcher's socket before it reports the seat as up [2.7, amendment 28]; a seat never chooses or repairs its own pin (five of six S6 seats exported `AM_SESSION` themselves, which is the model acting as the enforcement point; the one seat that followed the team rules lost its task). Evidence from the other side: AMQ v0.107.0 #1176 gives every Codex seat its AMQ identity in shell commands. [2.6, amendment 23] Evidence: S6 run of 2026-10-22, where the legacy driver omitted `AM_SESSION`, amq 0.102.0 refused every seat's first drain, five seats repaired their shell themselves and one stayed idle for the whole run. [2.4, S6 report finding A] Corrected in phase 2b: the proof is one read-only identity proof that never drains the mailbox (a drain between the waiting-mail check and the proof consumed and lost a message). The up signal is that proof sent over the launcher-owned Unix socket with the one-time token, valid 60 s from staging, accepted once per launch id and token and only from a pid whose parent chain reaches the pane's shell. The seat's published record is evidence only, and the launcher never re-reads pid or birth time from it. [2.7, amendment 28; briefs ruling 19]

Detection and registry
- R9 First run and `doctor` detect tools, versions, login state, Ollama models, RAM, GPU, and latency; `doctor` lists project roots codex-cli does not trust [2.3, amendment 1]; latency is direct text TTFT [2.3, amendment 10].
- R10 The registry holds one entry per runtime x model x effort with verified, cost tier, privacy class, quota pool, concurrency cap, untrusted_input, probation, latency.
- R11 The lead proposes seats from verified entries only.
- R12 A local entry is verified only after a tool-call and edit probe passes N of N times with zero false successes, N=3 in the spike and N=5 before an entry ships; the probe requires confirmed tool execution, staged and untracked diff checks, an exact final line, and a read. [2.3, amendments 10 and 15]

Modes, lead, scheduler
- R13 Two modes, auto and manual, set globally, per repository, by flag, or by lead command.
- R14 In auto mode the lead chooses runtime, model, and effort per task; a deterministic filter checks the choice against the grant.
- R15 In manual mode a new seat starts only after approval; dispatch to a running seat needs none.
- R16 A standing grant per session sets maximum seats (default 12 per session [2.5, amendment 22]), cost per day, permitted runtimes, permitted privacy class; grants and mode live only in the guarded owner file `~/.amq-squad/grants.yaml`, and `squad.yaml` can only narrow them. The privacy class is enforced in two layers: `seat-exec` execs the provider with an environment scrubbed of `*_API_KEY`, `*_TOKEN`, `*_SECRET` and known vendor names unless the runtime declares a need, and the pi package restricts the model list to the seat's registry entry. [2.3, amendment 12] `seat-exec` denies writes to the owner's global agent config (`~/.claude/settings.json`, `~/.codex/config.toml`), and `doctor` diffs that config against the preflight copy. Evidence: S6 finding B, 2026-10-22, an unattributed change to `~/.claude/settings.json` during the run. [2.4, amendment 18] `doctor` checks the owner's shell profile for exported `*_API_KEY`, `*_TOKEN` and `*_SECRET` names and warns, because provider tool shells source the profile and bypass the process-level scrub; the pi package's tool shell runs without the profile; whether `SSH_AUTH_SOCK` reaches a seat is a phase 2b decision. [2.4, amendment 17] The scrub removes `SSH_AUTH_SOCK` (4.3). [2.7, 2b-2 milestone A result] The config-write rule is enforced in layers (4.3): the Codex sandbox for Codex seats; guard plus the post-exit diff on every terminal path, with the owner-cleared quarantine, for Claude seats. [2.7, amendment 28] The grant files are the strict YAML subset of 7.2. [2.7, amendment 27] After the first usage-limit event in the window the scheduler starts no new seat; the usage-limit signal, not RAM, is the enforced stop. [2.5, amendment 22]
- R17 The scheduler is deterministic Go with queue, leases, retries, and pools keyed by runtime x model x effort; dispatch is tracked by task leases, not by mail. [2.3, amendment 11] It runs as the process `amq-squad scheduler run` (4.5). [2.7, briefs ruling 15]
- R18 Idle seats close after a timeout.
- R19 The lead does not poll tasks; it decomposes, labels, prioritizes, converges. The lead loop test is "the lead does not start coding". [2.3, amendment 11] The team lead has no Bash tool. Its tools are the native amq tools, the task verbs through the scheduler, a read-only file read and directory list scoped to the repository root, and `amq-squad status --json`; anything that runs a command is dispatched to a seat. The solo lead keeps its tools. [2.7, amendment 26]

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
- R32 A typed-gate answer is owner-signed with the Buzz key (BIP-340) over gate id, action class, and target digest; `amq-remote` is transport only; the Go core verifies it and records it in an operatorauth envelope; a seat or a model cannot produce a valid answer. The typed prefix stays 16 hex and is checked only against the immutable gate record, which holds the full digest and is written before the question is sent; the verifier follows the conformance rules in 7.3. [2.3, amendment 13] Enforcement stays in the Go core; hooks are UX and evidence. [2.3, amendment 3] The gate text says "reply in this thread"; thread binding stays on; a non-thread answer that matches an open gate (gate id, class, prefix, owner pubkey) makes the core re-post "reply here to confirm" in the gate thread, and the next thread reply is the answer; there is no "only candidate" acceptance. [2.5, amendment 19] A mailbox-binding answer arrives as a `nostr:<eventID>` label, and the layer verifies the event against the relay with the Claude-hook checks. [2.5, amendment 20] The signed question carries the squad identity, and the verifier selects the gate record by squad identity, gate id and thread (7.3). [2.7, amendment 30]
- R33 Seats hold no default-branch or release credentials. The Buzz nsec enters only `amq-owner-sign`, once, and is written nowhere. [2.3, amendment 14] It reaches the signer only through a password-manager pipe, never a terminal, a log or a file. [2.5, amendment 19] Measured and reworded (3-2, 2026-10-24): seats are given no default-branch or release credential; their reads of the owner's credentials are denied by guard (credential_read: gh auth token, security find-*-password -w, git credential fill and credential-*, launchctl getenv SSH_AUTH_SOCK) and scrubbed from the environment (GIT_ASKPASS and SSH_ASKPASS included); the server-side CI-only ruleset is the enforcement for the default branch; for tags and releases the enforcement in 3.0 is detection, not prevention: doctor and the merge train compare the repository's tags and releases against the gate store at every run and raise an owner alert on any tag or release without an accepted gate; the same-UID threat model of 7.3 covers the rest (a same-UID seat can read the owner's gh token from the login keychain today), and phase 5 adds a harness-held GitHub App token or a separate keychain. [2.7, 3-2 result] Owner decision 2026-10-25: 3.0 ships with the owner's gh CLI login as the harness credential through the pinned gh copy; the separate keychain or App token moves to 3.1; the keychain-read limit stays recorded. [2.7, owner decision] The harness is the owner-identity actor, and the push is where that identity is used (milestone B review, ruling D2): every harness git call runs with GIT_CONFIG_GLOBAL=/dev/null and the hardened set of amendment 28, so no credential helper is read from any config file; for an HTTPS github.com push_url the harness itself passes `-c credential.helper=` (clearing the list) and then `-c credential.helper=!<pinned gh> auth git-credential`, with gh at the launch-time copy of the registry entry's binary inside the core-written tools directory (hashed at launch and re-hashed before the push), never PATH, GIT_TERMINAL_PROMPT=0 so a failing helper is a push error and never a prompt, and GH_TOKEN and GITHUB_TOKEN kept on the push call only from the harness environment, never a seat's; an SSH push_url uses the agent and GIT_SSH_COMMAND from the harness environment, with core.sshCommand never read from the seat repository; doctor runs `gh auth status --hostname github.com` when a grant names an HTTPS push_url. Recorded same-UID limit (milestone B fourth pass, beside 4-1's item 2): git itself executes the credential helper by pathname after the harness's check, so a same-UID replacement between the check and git's exec, or between git's get and store calls, runs unverified; the mitigation is the launch-time copy of the pinned gh into the core-written 0700 directory, hashed there (never the registry path a seat can see), the re-hash immediately before the push, and the push-only token exception. [2.7, milestone B review] The Buzz share's body key has two authenticated relay connections, each with a bounded purpose (ruling 20 point 1): serve's (the DM surface, presence, result rows) and the core's, which is opened per use and closed after it, never subscribes to the DM channel, and does only this: post the gate question at `gate raise` or `gate post`, and per answer read the answer event by id, read the question's and the answer's edit and delete history, and post the gate's one re-ask. The key goes from the core's connection to an upstream read-by-id surface if one appears (follow-ups). [5-amq-B]
- R34 Merges go through the standing merge grant and a deterministic merge train; the batch digest covers the ordered (PR, head SHA) pairs plus the base SHA and is written to the audit log, unsigned; any rebase that changes a tree re-runs the rules check; CI-only branch protection on the server stays, and no seat process holds a merge path to the default branch. [2.4, S2 report owner decision]
- R35 In the built-in pi and Claude Code, hooks inject role and rules every turn and record evidence on every tool result; Codex, Grok Build, and Cursor receive role and rules at launch only, and Codex evidence comes from AMQ capture or Codex session files.
- R36 In the built-in pi a guard failure blocks the call; Claude Code PreToolUse fails open on timeout, on non-zero exit without a deny, and on a missing command, a timeout costs exactly the configured timeout, and the docs say so; no other runtime blocks in session. [2.3, amendment 3] In a Claude seat the permission boundary is guard: an allowed call carries an explicit allow and Claude's own permission prompt never stops the seat; the plugin's presence is a seat-exec preflight check. [2.7, 4-2 result] Corrected 2026-10-25: the pre-approval is the seat's settings allow list from the launch record, the hook is deny-only, and dontAsk auto-denies everything outside the list (4.3). [2.7, hooks re-check, reviewer ruling] A Claude seat runs under permission mode dontAsk with a seat-specific settings file written by the core (the permission mode and an empty allow list, its digest in the launch record), so a hook failure leaves the call to Claude's permission rules, which deny anything that would have prompted; the hook's fail-open never becomes an unguarded call, and doctor warns when the owner's user-scope settings carry allow rules. [2.7, milestone B ruling] Claude seats authenticate with a long-lived token from `claude setup-token` held in the guarded owner file `~/.amq-squad/claude-token` (0600, in the protected set, checked like grants.yaml, refused as claude_token_insecure when missing or insecure), reaching the seat as CLAUDE_CODE_OAUTH_TOKEN through the runtime's credential need, never the owner's config dir, because a fresh per-seat config dir cannot use the Keychain login; the token sits in the seat process environment (the same-UID limit of 7.3, bounded to the owner's subscription), never in a record, ledger or log, and doctor warns past the rotation window. [2.7, milestone B ruling]
- R37 Hook configuration and rules files are guarded paths.

Fleet, mayor, remote
- R38 One operator session holds the fleet registry and receives all gates and status through AMQ routing.
- R39 The operator session and a squad lead are the same program with different grants.
- R40 In auto mode the operator session, the mayor, starts squads inside a fleet grant: maximum squads, cost per day, permitted repositories; a cap with no meter is fail-open, so when the grant sets cost_per_day and no meter can read the outcome log the mayor holds every start with wait_reason cost_unmetered and the status says so, and the owner removes the cap to proceed (4-3 re-pass ruling, 2026-10-25) [2.7, 4-3 re-pass]; the Go core provisions the roster through the launch API package, and where a new project root needs `amq launch` to provision an unknown session (upstream ask 2) the fallback is: the mayor proposes, the owner starts.
- R41 The mayor never approves a typed gate.
- R42 `amq-remote` plus Buzz is the remote face; Buzz attaches to the operator session. Enrollment defaults to `--enable buzz-profile --presence`, at the cost of one extra owner signature. [2.5, amendment 19] The share's target is amq-remote's mailbox adapter with DM commands on; serve delivers, the core verifies (section 9). [5-amq-B] The busy window (5-amq-B fix round, a 3.0 limit): the mailbox adapter runs one request at a time and rejects an owner DM that arrives while the previous one is open as `busy` (amq-remote v0.116.2, no queue; the rejected DM never reaches the attachment, since the carrier submits busy=reject and admission writes a terminal tombstone before dispatch). 3.0 accepts this: the owner waits for each result row (about a second) before the next DM; a gate answer lost to busy costs a re-answer, since the gate stays open; a routed message lost to busy is re-sent by the owner. Upstream ask 18 asks for a queue on mailbox targets, or a retry hint in the busy row. [2.8, 5-amq-B]
- R43 `status --json` is the versioned contract in 4.6 with a golden fixture in CI; it carries the command-center fields, the bucket table and the "new task" argv. [2.6, amendment 24] The fleet form is the document of 4.6. [2.7, briefs ruling 16]
- R44 amq-noc reads that contract and AMQ primitives only; amq-noc 1.0 is the command center and its only view (4.7). [2.6, amendment 24] The old board goes by the delete list of 4.7. [2.7, briefs ruling 17]

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
| 2a Subtraction | delete the v2 code in section 10; add the three verbs. From the C11 cut of 2a-1 until `seat-exec` lands in 2b, the v3 binary cannot launch seats; every run in that window (the Codex follow-up, S6 variant 2, any owner demo) uses the installed 2.31 binary. Not a bug. [2a-1 planner decision 6] Status 2026-10-22: 2a-1 merged (7b8c8f1): internal/ non-test 110,929 to 72,972 lines, tests 3,024 to 2,027 with every removal audited, bubbletea/bubbles/lipgloss/fsnotify out of go.mod. 2a-2 inherits, from `docs/v3-spike/2a1-execution.md`: 40 delete-fate files (10,819 lines) plus status_board.go and lead_identity.go with their kept callers named; six packages (runtimeaction, runtimecontrol, liveidentity, notifier, bootstrapack, tmuxpane) and attention/dedup.go; the holding file internal/cli/v3_holding.go (832 lines, target 0); the coverage debit (each flagged test is restored with a test-side fixture or retired with its subject's shrink, none dropped silently); stale command names in kept text (worktree-isolation remedy, generated team rules, team_overlay.go). [2a-1 result] Status 2026-10-23: 2a-2 merged (4a0ad3db): internal/ non-test 72,972 to 42,992 (phase 2a overall 110,929 to 42,992), holding file to 0, tests 1,115, all delete rows gone, two replace rows deferred to the 2b evidence ledger. Band gate NOT MET by decision: 40,561 against 29,790..36,410; like for like the non-debt rows sit 6.0 percent over their own targets. Ledger in three classes is a named 2b input (`docs/v3-spike/2a2-execution.md`): deferred-2b rows (cli and internal team.go, team_rules*.go, context_resolution.go, launch/record.go, doctor.go) are rewritten in 2b; accepted-3 rows (task.go, operator.go, role_draft.go, state rows, amq_send.go, verify*.go) are re-targeted when 2b's design fixes them; deferred-3 rows (compoundrelease writer) go with the phase 3 release flow. Stale verb names outside internal/ (README, operator cookbook, plugin skills, release docs) go to the 3.0 docs pass. 2a-3 merged 2026-10-24: the three hook verbs `context`, `guard`, `evidence ingest` (1,495 new lines, manifest fate new, outside the v2 band; guard matrix 69 rows incl. 13 review-fix rows; ledger with flock, fsync, HTML-safe encoding; timings as a built binary from aa2dd276: guard p95 8.7 ms, context 6.9 ms, ingest 10.4 ms). **Phase 2a closed**: internal/ non-test 110,929 to 44,496 lines; tests 1,131. The 2a-3 open questions (gate store written by the core, rules digest and seat branch recorded at launch by seat-exec, needs_gate mapped to deny with "raise the gate" in the plugin, insecure grants file, squad.yaml location, credential_change in operatorauth, target-string conventions, external-send classification) are named 2b and phase 3 inputs next to the 2a-2 ledger (`docs/v3-spike/2a3-notes.md`). [2a-3 result] | 2 days |
| 2b Core | task store with leases and pools; status contract with the classifier moved in; registry and detection; routing table and outcome log; `seat-exec` hardening before first use [2.3, amendment 7]; `amq-squad trust <repo>` and the doctor trust check [2.3, amendment 1]; provider environment scrub [2.3, amendment 12]; hold-seam and role-delivery tests [2.3, amendments 11 and 15] Status 2026-10-24: brief 2b-1 merged (606b63a6): task store with leases and pools under the AMQ root, scheduler with request ids per incarnation and launcher-confirmed cancellation, status contract schema 1 with the four buckets and argv actions, evidence list over the hook ledger; ten review findings closed with test rows; band gate MET at 35,807. 2b-2 milestone A merged (f4d3dc13, 2026-10-24): seat-exec with the scrubbed environment (indirect credential sources included), identity at launch from the record only, the read-only identity proof with a launch nonce, the Codex sandbox refusal and the preflight diff (amendment 28), exact birth-time checks and the pending, closing and cancelled launcher states; criterion 4 passed from the v3 binary with real Claude and Codex seats; band MET at 33,723; Codex pass plus the lead's narrow pass (amendment 29). 2b-5 merged (fbf56347, 2026-10-24): squad.yaml schema 1 with the strict YAML subset (amendment 27), the team model as squad.yaml in memory with propose, apply, show, check and draft-rules through the digest flow, the team rows rewritten, internal/atomicfile as the one durable writer, the LaunchSeats adapter onto 2b-2's typed seat list; two Codex passes plus the lead's verification. At this merge the band's floor is retired (a v2 row rewritten below target or deleted with its function in a fate-new row is 2b's intended outcome, so a sub-floor total measures nothing) and manifest check 8 replaces it: every row retired from 2b on names its replacement in replaced_by; the report prints v2 rows kept 29,426 against the ceiling 36,410, 3.0 additions 11,926 lines, size of 3.0 41,352 lines. 2b-3 merged (fec06084, 2026-10-24): registry.yaml with the full runtime re-hash at every launch, the R12 probe at N=5 with the setsid-descendant kill and the pristine copy made only after it, descriptor-anchored traversal, the usage-limit detector behind 2b-1's scheduler interface, amq-squad trust, the doctor rows with the status contract self-check against the embedded golden fixture; three Codex passes with fixes and reverting tests, lead verification; v2 rows kept 29,138, size of 3.0 45,577. 2b-2 fix merged (dc7eedab, 2026-10-24): fixes 3b and 3c from the third and fourth Codex passes on milestone A (the up signal over the launcher-owned Unix socket with the one-time 60 s token bound to the pane chain, the live-socket abort, hop re-read, sender re-check, consumed and expired markers, amendment 28), upstream ask 17 (Codex sandbox pins as provider arguments), and the one-shot proof-window wait (`operator start-squad` keeps the proof socket open until the token window ends; a session mayor sets LongRunning and returns at once); 30 reverting rows re-run on the rebased head; the merged head is on the phase 5 re-pass list. 2b-4 merged (d04932d1, 2026-10-24): the class table with tier chains and routing.yaml, chain selection with escalation and the reviewer family, exploration and the outcome log as a signed hash chain (seq and prev inside the signed bytes), the signed head record through atomicfile (both chains must reach it, the seed's device and inode, the open breaks), the history_tampered hold as a block routed to the lead that clears only on a reset after the router observed the block, the weekly report with owner prices and `routing report --week`, the canonical walk on fsanchor with readlinkat; five Codex passes with fixes and reverting rows (27, 15, 20, 16, 19), lead final read; recorded same-UID limits: forged tags, chains and head, a torn tail stopping routing; v2 rows kept 28,639, size of 3.0 58,207; the merged head is on the phase 5 re-pass list; milestone B removes its temporary `--default-pool` at its merge. 2b-2 milestone B merged (84f1524b, 2026-10-25), and **phase 2 closes**: the harness commit step (`seat commit`: the seat diff against the task-declared file set through task.MatchPath, snapshot by hash-object --no-filters into a temporary index, the push to the grant's push_url only from a per-step shadow git directory with the hardened environment, the draft PR opened and bound to the task through task.Store.BindPR with pr_repository, pr_number and pr_head in one transaction with completion), the lead widening path (`task widen` over the scheduler's own Unix socket, accepted only from a sender whose stamped parent chain reaches the lead's recorded incarnation, re-proved after the request; the AMQ message is the record, never the authority), the growth gate for protected-path and managed-block crossings, the global-config quarantine with the preflight diff and the Claude config-dir digest on every terminal path (done, fail, gone, expired) and `seat clear` as the owner's verb from a real pty that is no pane's, with a signed clear record; `amq-squad scheduler run` as the scheduler process supervised by `lead --team --scheduler-seats`, with 2b-4's router and Flush per tick, the start-up barrier (adopted seats closed and confirmed, exits ledger loaded and writable, decision socket closed until then), slots released only at seat_gone, deferred exit checks persisted; CleanSeat between leases with the discarded diff kept on the ledger; the per-seat CLAUDE_CONFIG_DIR, the owner token file, AMQ_SQUAD_BIN, claude_hooks_missing and claude_hooks_silent; merge.push_url and merge.local_push_ack as grant fields with the pinned gh copied at launch into the core-written tools directory as the only credential helper for an HTTPS push; internal/redact with the real-shape rule used by the redaction step, the docs and evidence scan and the source scan (ruling 18). Live: R19 2 of 2 on CleanSeat, R12 10 of 10 complete through the commit step with exact diffs (strict 3 of 5 with the probe role, misses final-line only); seat-exec to exec 50.8 ms median, commit and push 256 ms, refuse 103 ms, tick at 100 tasks 12.1 ms. Four Codex passes with fixes and reverting rows (24, 24, 13, 9, plus the canary row), rulings R1 to R9 and D1, D2 recorded in 7.2, 7.3, R33 and amendment 28, lead final read. Behaviour changes: `task widen` is accepted only from a lead that proved up through the seat launch (origin_unknown otherwise); `seat clear` needs a real pty on stdin and times out after 60 s; after a restart nothing is dispatched until every adopted seat's close is confirmed. Not run: the Claude live rows (plugin load, probes a and b, the S4 fault table under dontAsk, the Bash-tool scrub candidate, the Claude half of the role matrix) wait for the owner's `claude setup-token` file; 7.5 waits for the owner's `amq-squad trust` of the fixture; both are the first items of phase 5's owner session. Recorded limits: same-UID AMQ mail as the lead, same-UID owner shell by 7.3's model, the credential helper executed by git by pathname, SHA-1 repositories only, the external stand-in's termination by seat_gone, hook reports forgeable by a same-UID process. Phase 5 re-pass ruling (2026-10-25): the commits ledger and the exits queue carry the seed-keyed HMAC per record and a signed head record like the outcome log, and a failed tag or chain is a block routed to the owner (ledger_tampered), never a silent pass; the same-UID limit of the routing chain applies to them as written. v2 rows kept 27,783 against the ceiling 36,410; 3.0 additions 40,942 lines in 205 files; size of 3.0 68,725. The merged head is on the phase 5 re-pass list. [milestone B result] | 2 to 3 days |
| 3 Security | credential custody; branch protection and merge-train checks; Codex sandbox defaults; signed gates for the four typed classes; standing grants, including the per-repository merge grant [2.4, S2 report owner decision]; release gate flow. Status 2026-10-24: brief 3-1 merged (beabeadc): grants schema and narrowing, fail-closed insecure owner file, credential_change class, signed deny record, target-string table and digest, core-written gate store with durable one-decision recovery, verifier and relay client ported from S1 with reviewed libraries, one typed-gate system on v3 (the v2 AMQ-answer path for typed actions deleted). Three Codex review rounds, 18 review test rows. Window until 4-5: a typed gate is recorded but not sent or answered, like the R51 launch window. 3-2 pending. [3-1 result] 3-2 merged (2756d88d, 2026-10-25): the merge train under the standing merge grant (ordered PR set, batch digest in the audit log, rebase through updatePullRequestBranch with the head pinned, task read only through the PR binding keyed by repository and PR number with pr_head_moved and task_unbound, the train's own re-bind after its rebase with the audit entry first, a real run refused against a local push target), the branch and tag protection checks (app-bound required checks only, bypass allowances and actors unmet, the narrow tag-pattern grammar), the release flow (draft created at the approved target_commitish, read back and re-read before publish with the tag resolved twice, audited deletions, orphan recovery through the publication lock record), the Git transport and trust variables in the never-needed set, redaction of URL userinfo and nested shell strings, the drift baseline migration; pr_repository, pr_number, pr_head on task.Task with Store.BindPR; three Codex passes with fixes and reverting rows (14, 2, 16, 2), lead final read; v2 rows kept 27,771, size of 3.0 60,852; the merged head is on the phase 5 re-pass list; acceptance 4 (the real merge of the clean S6 PRs) waits on the owner's merge grant and rulesets; the GraphQL rebase liveness check is on the S6 dry-run list. The gated merge's first parent is 3e17beba; the one docs commit made on v3 during the gate was replayed on top as 85c4149b (rule from here: no commit on v3 while a merge gate runs). [3-2 result] | 3 days |
| 4 Faces | built-in pi package with hooks and native AMQ tools [2.3, amendment 11]; pi seat launch and resume through the upstream mint-mode adapter, `coop exec` as the fallback [2.6, amendment 23]; Claude Code hooks plugin; operator session and mayor; amq-noc 1.0 as the command center and its only view (4.7) [2.6, amendment 24]; Buzz attachment. Status 2026-10-24: 4-2 merged (6b27c0c4): the user-scope Claude Code hooks plugin injects the seat context every turn, maps guard verdicts (needs_gate to deny with the gate reason, never ask; a guard failure fails open and is logged), ingests evidence, and is silent outside a seat; fault table measured through the hook runner, live confirmation at milestone B's live run. 4-1 merged (0ded255d, 2026-10-24): the embedded pi runtime verified against the digest baked into the binary at every launch with the content re-check before launch and the preload self-check (amendment 25), the pi and lead verbs, the team lead's tool set (amendment 26), the @amq-squad/pi package with hooks, native tools and the hold seam, the forced environment with the transpile cache off and a private TMPDIR per seat, internal/fsanchor shared with 2b-3 and 4-3, atomicfile's anchored writer; two Codex passes plus the lead's verification; acceptance 10 (pi launch through AMQ) blocked on the AMQ release carrying PR #1188; v2 rows kept 29,154, size of 3.0 48,363. 4-3 merged (1a74858d, 2026-10-24): one lead-session entrypoint for a squad lead and the operator with the grant alone giving the scope; the fleet registry through fsanchor; status --fleet --json with lead_handle, the operator row and waiting rows; the operator inbox; the mayor inside the fleet grant with owner-controlled repository identity (the grant's repository_paths and the protected identity file, never the checkout's remote), locked admission with fenced reservations that expire only on a gone incarnation or a stalled phase, the squad.yaml digest flow and the roster launcher as adapters, and no path to a typed gate answer; two Codex passes, the lead's pass and a Codex re-pass on the rebased head; v2 rows kept 28,641, size of 3.0 50,464. 4-5 merged (b7522370, 2026-10-24): the owner enrollment script with profile and presence on by default, the key only through a password-manager pipe, no core dump and a filtered signer output; the Buzz agent bound to the operator session's mailbox with presence from the fleet document's live field; gate questions carrying "reply in this thread" and the squad identity (amendment 30) with answers verified in the raising squad's store through the thread's e-tag binding; phone messages routed to squad leads across projects as @squad/lead; the share root under the guarded Buzz directory through the anchored walk; two Codex passes, the lead's pass, a Codex re-pass on the rebased head and a narrow pass; acceptance 4 (one typed gate answered from the phone, one message routed to a lead, real key) is the owner session; v2 rows kept 28,644, size of 3.0 52,810. 4-4 (amq-noc command center) review-complete on its branch in amq-noc, held for the release train by owner decision; its fixture refresh for lead_handle and live follows. [4-5 result] | 3 to 4 days |
| 5 Release 3.0 | `squad.yaml` final form; `migrate` from v2 profiles; real-AMQ CI; release smoke; local-model eval; Jev shadow behind a flag; signed and notarized launcher [2.3, amendment 9]; the phase 2 inputs and the open acceptance rows of 16.4 [2.7, spike log phase 5 lists] | 2 days |

`docs/v3-phase2-briefs.md` is the execution document for 2b, 3 and 4.
The first-wave rule: two implementers start first (2b-1 and 3-1), and a
third and fourth (2b-2 milestone A and 2b-3) join only after the first two
run a full hour without a limit stop. Four at most run at once, for the
whole of phases 2b to 4. Implementers are Opus 5.5 or Sonnet 5.5 at medium
effort. High effort is for 2b-1 and for the 2b-2 commit step only. The
dependency order from its overview: 2b-1, 2b-2, 2b-3, 3-1 and 4-1 start
now; 2b-4 follows 2b-3; 2b-5, 4-3 and 4-4 follow 2b-1; 4-5 follows 3-1;
3-2 and 4-2 follow 3-1 and 2b-2. [2.6, phase 2b/3/4 briefs]

Evidence hygiene binds every brief from 2026-10-24. No test formats the
environment into a failure message or a log line. Every evidence file
passes the hook ledger's redaction before it is committed. A
repository-wide test fails on a credential-shaped value under docs/ or
an evidence directory. No test reaches the network: every remote is a
fake script or a bare repository under the test's temporary directory.
[2.7, briefs ruling 18] The lead's merge gate also scans the whole diff
for the real credential shapes of `redact.RealCredentialShapes`. Every
gate run keeps its full go test output under the brief's evidence
directory, no gate run overlaps a live seat session on the host, and no
commit lands on v3 while a merge gate runs. [2.7, spike log; 3-2
result]

Phase 1 runs first because its numbers set the limits for everything after
it. Phase 2a runs alone and next, because it deletes code the other phases
would otherwise edit. Phase 4 depends on the 2a verbs. amq-noc depends on
the 2b contract. Within those dependencies, 2b, 3, and 4 run in parallel.

Phase 1 roster: the Codex subagent in the orchestrating Claude Code
harness cannot install, clone, or create branches. Opus implements and
Codex subagents review. This says nothing about codex-cli seats under
`amq launch`. [2.3, amendment 6]

Phase 2 roster (owner rule, 2026-10-22, effort rule 2026-10-23): Fable 5.1
high runs only the lead session (planning, rulings, merges, review of agent
output); implementers are Opus 5.5 or Sonnet 5.5 at medium effort in their
own worktrees with the work branch checked out, and high effort only on a
task the lead names as hard, with the reason in the brief (2b-1 and the
2b-2 commit step in the briefs); probes, drafts and log collection are
Sonnet 5.5 at medium;
second-opinion reviews are Codex. No agent starts without an explicit
model. Process evidence for the registry's "verified" rule (R12): on
2026-10-22 three implementer starts made no commit (a Fable agent stopped
by a session rate limit after five cuts, an Opus pane agent that died
silently, an Opus background agent blocked by the worktree guard and by
deletion denials); the fourth, an Opus background agent with the branch
checked out in its own worktree, worked. [2.4, roster rule]

Review (owner rule, 2026-10-24): Codex reviews whenever it has capacity,
and the lead session is the final reviewer on every merge. A Codex pass
is an input. The merge decision is the lead's, made on its own gate
(build, vet, cross-builds, manifest, full tests, race on the touched
packages, the merge result gated in a scratch worktree before v3 moves),
its own reading of the diff, and the kept revert checks. When Codex is
out, the lead's narrow pass replaces the Codex pass and is recorded as
such. That is a recorded deviation from the cross-family review rule of
6.1, and the Codex re-pass list in 16.4 settles it before release. [2.7,
amendment 29]

Phase 2a, step 0 changes the module path from `/v2` to `/v3`.
`go install .../v3/...@$(VERSION)` cannot pass until the first v3 tag
exists, so `make release-smoke` is expected red on `v3` until phase 5.
README, MIGRATION, RELEASING and scripts/check-release-version.py stay on
v2 until the v3 tag. Branch `v3-2a0-module-path` stays unmerged until the
owner's go on the spike table. [2.3, amendment 16] Fact at the time of
this revision: the branch is merged into `v3` as 422199d.

Phase 2b also runs the controlled role-delivery matrix; the spike
comparison was not controlled. [2.3, amendment 15] Milestone A's pointer
arm stands at N=1; the N=3 matrix waits for the owner's Claude token and
the Codex trust step (16.4). [2.7, milestone B result]

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
- pi seats launch through the upstream mint-mode adapter. Two gaps remain: resume is create-if-missing, and model and effort cannot travel as provider arguments. 4.1 and 4.3 name our fixes. [2.6, amendment 23]
- Codex-accepted model ids under the owner's login: measured in the S4
  closure run; the registry checks the id against the pinned codex-cli
  version before launch (section 5). [2.4, amendment 17]
- A Codex seat under `workspace-write` never commits (codex-cli 0.163.0,
  in a worktree and in a plain checkout, with and without `--add-dir`).
  The harness commits and pushes for every seat, and the Go core checks
  the task-declared file set on that commit (4.3, 7.2). Until that
  harness step exists in phase 2b, a Codex seat's work reaches a branch
  only by hand. [2.5, amendment 21] The step landed with 2b-2 milestone
  B (`seat commit`); the live Codex row waits for the owner's trust of
  the fixture (16.4). [2.7, milestone B result]
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
  The enforcement is layered (4.3): the Codex sandbox, or guard plus the
  post-exit diff and the quarantine. [2.7, amendment 28]
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
- 2026-10-29, S6 draft PRs (owner decision, supersedes the 2026-10-22 line): the dry run passes 1 to 5 consumed the drafts as the train's input; a real run cannot merge them (no 3.0 task binding, P1-4, by design). Option A chosen: the 18 drafts stay open until the v3.0.0 tag, then the owner closes them; the tagged binary gets one last dry run against them as a release-evidence row. Acceptance 4 is re-scoped to a harness-bound PR (owner-run fourth item).
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
- 2026-10-24, revision 2.6: amendments 23 and 24, the phase 2a close and the
  phase 2b/3/4 briefs are integrated in place, tagged "[2.6, <source>]".
  Amendment 23 (the upstream pi adapter, PR #1188) lands in 4.1, 4.3, R1, R5,
  R52 and the phase 4 row; upstream resume is create-if-missing, so our side
  refuses a pi resume that would lose context. Amendment 24 (owner
  decision of 2026-10-24: amq-noc 1.0 is a command center and its only
  view; the old board view is removed) lands in 3.6, 4.6, 4.7, R43, R44 and
  the phase 4 row. Briefs rulings 9 and 10 correct 16.6: Jev (R29) is phase
  5, and R4's seat-exec work is phase 2b. Section 13 names
  `docs/v3-phase2-briefs.md` as the execution document and its first-wave
  rule. Section 11 records the upstream status of 2026-10-24.
- 2026-10-25, revision 2.7: phase 2 closed with the merge of 2b-2
  milestone B (84f1524b). Amendments 25 to 30, briefs rulings 15 to 19
  and the phase 2 results are integrated in place, tagged
  "[2.7, <source>]"; no ruling changes. Amendment 25 lands in 4.7 ("n"
  runs `task add`). Amendment 26 (the team lead has no Bash tool; the pi
  launcher supervises node) lands in 3.2, 4.3, R4 and R19. Amendment 27
  lands in 7.2 and R16. Amendment 28 with its addenda (layered config
  enforcement, the read-only identity proof, the up signal over the
  launcher socket, the Codex sandbox pins and ask 17, the widen
  authority over the scheduler socket, `seat clear`, harness git
  hardening, terminal paths, pane labels) lands in 4.3, 7.2, 7.3, 11,
  14, R4, R16 and R52. Amendment 29 lands in the section 13 roster.
  Amendment 30 lands in 7.3 and R32. Rulings 15 to 19 land in 4.3, 4.5,
  4.6, 4.7, 13, 16.5, R17, R43, R44 and R52. 16.1 records the phase 2
  size line, and 16.4 lists the phase 5 inputs and the open acceptance
  rows.

## 16. Amendments from the final review (revision 2.1, 2026-10-20)

Verdict of the final review: green with edits. The inline edits are
applied above. These structural amendments take precedence over sections
10, 12, 13, and 14 where they differ. Revision 2.3 does not change this
section's decisions; it tags the spike amendments that touch it.
Revision 2.4 did the same for the day-2 owner rulings. Revision 2.5 does
the same for amendments 19 to 22 and the 2a-1 result. Revision 2.6 tags the
rest and corrects 16.6 (briefs rulings 9 and 10). [2.6, briefs rulings]
Revision 2.7 adds the phase 2 size line to 16.1 and the phase 5 inputs
and open acceptance rows to 16.4, and changes no decision here. [2.7]

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

2a-2 result (2026-10-23): 72,972 to 42,992 non-test lines; the band gate is recorded as NOT MET by decision (40,561 against the 36,410 top) with the three-class ledger (deferred-2b 3,468 over target, deferred-3 172, accepted-3 3,148) carried into 2b as a named input; manifest-check --final passes only with the `decisions: [end-of-2a-2]` marker and never drops rows from the total. [2a-2 result]

2b-5 result (2026-10-24): the band's floor (29,790) is retired at the 2b-5
merge and the ceiling (36,410) stays; manifest check 8 requires every row
retired from 2b on (deleted, or shrunk below half its target) to name in
`replaced_by` the fate-new row that replaces it or the brief acceptance
item that covers its function. The size line is two numbers and their sum:
v2 rows kept 29,426; 3.0 additions 11,926 (55 fate-new files); size of 3.0
41,352 non-test lines under `internal/`. [2b-5 result]

2a-3 result (2026-10-24): the three verbs are in; phase 2a is closed at 44,496 non-test lines with the 2a-2 ledger and the 2a-3 open questions as the named inputs to 2b and phase 3. [2a-3 result]

Phase 2 close (2b-2 milestone B merged as 84f1524b, 2026-10-25). The
size line:

| Measure | Lines |
| --- | --- |
| v2 rows kept | 27,783 against the ceiling 36,410 |
| 3.0 additions | 40,942 in 205 files |
| size of 3.0 | 68,725 (v2 rows kept plus 3.0 additions, as manifest-check --final prints it) |

The band floor stays retired from the 2b-5 merge, and manifest check 8
is in force: every row retired from 2b on names its replacement in
`replaced_by`. [2.7, milestone B result]

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
  is the only fleet input for amq-noc. Its document shape is in 4.6.
  [2.7, briefs ruling 16]
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

Inputs from phase 2 (2026-10-25). They come from the spike log's phase 5
lists and the phase rows of section 13, and each names what it waits on.
[2.7, spike log phase 5 lists]

Codex re-passes of merged heads. Findings become fix commits before the
3.0 tag. Each waits on Codex capacity (amendment 29).

- The amendment 29 heads: 2b-2 milestone A and its fixes, 4-2 and the 4-2
  fix, 4-3, 4-5. The spike log moved these to 2026-10-24, when Codex
  returned; a head stays on this list until its re-pass is recorded as
  complete. Done 2026-10-25: all four re-passed at the tip, 17 findings
  fixed and merged as 9e8b0921 with the milestone B re-pass (branch
  v3-5-repass-2b2, seven commits, 92 reverting rules, two Codex narrow
  passes, lead gate on the merge result). [2.7, phase 5 re-pass result]
- The 2b-2 fix merge dc7eedab: fixes 3b and 3c, the proof-window wait.
  Covered by the 2b-2 A re-pass at the tip (same three packages; six
  findings on the phase 5 fix branch). [2.7, reviewer, 2026-10-25]
- The 2b-4 merge d04932d1: fix 5's head record, the observed-then-reset
  clearance, chained decisions. Done 2026-10-25: eight findings (two
  rulings: verdict_missing with the configured-verifier switch, pool
  occupancy as the scheduler's seat view) fixed and merged as 53c348f1.
  [2.7, phase 5 re-pass result]
- The 3-2 merge 2756d88d: fix 4's binding in the task store, the pinned
  GraphQL rebase, the draft read-back. Done 2026-10-25: eight findings
  (a lost publish response is completion, the whole timeline paginated,
  unterminated quoted secrets, draft and merge intents journalled, the
  audit tail repaired, evidence read whole or marked incomplete, the
  lease window ending at expiry) merged as 53c348f1. [2.7, phase 5
  re-pass result]
- The milestone B merge 84f1524b: the commit step, the decision socket,
  the exits queue, the pins. Done 2026-10-25: eight findings fixed and
  merged as 9e8b0921 (the commits ledger and the exits queue sealed
  through internal/sealchain, evidence refs seat-scoped and verified,
  pr_pending, the managed path a regular file, canonical declared-set
  digests, every value under a credential name redacted). [2.7, phase 5
  re-pass result] All eight re-passes of this list are done.

Test hygiene. Each waits on a fix commit only, with no row weakened.
Done 2026-10-25: all three merged as 186fe04c (mergetrain under -race 944 s
to 48 s; the PTY deadline derived from a measured warm-up; the seatlaunch
window closed through t.Cleanup). [2.7, phase 5 hygiene result]

- internal/mergetrain under -race takes 940 s against 60 to 125 s
  without, because the fake gh re-executes the race-instrumented test
  binary per call and runs real git each time. Keep about three
  subprocess rows for gh's arguments and JSON parsing, and run the rest
  through an in-process fake over the same bare-repo state; target under
  2 minutes.
- internal/seatlaunch TestExpiredTokenIsRefused failed once in TempDir
  cleanup ("directory not empty") with no assertion failing. It is a
  cleanup race: find the late writer and close it.
- internal/cli TestRealPTYWaitPostureRefusesNamedGateBeforeTimeout ran
  out its 9 s real-PTY deadline once under full-suite load. The fix is a
  deadline scaled by a load factor or a serial test group, not a longer
  constant.

Hardening.

- The runtime executed from a read-only mounted image (hdiutil attach
  -readonly). It closes the verify-to-exec window of amendment 25 (4.1),
  because a same-UID writer must detach the volume and the path then
  fails verification. Waits on the bundle work of this phase.
- A harness-held GitHub App token or a separate keychain, so a same-UID
  seat cannot read the owner's gh token (R33). Waits on the choice
  between the two.
- The Buzz-signed `seat clear`, the second factor that amendment 28 names
  for the owner proof (4.3). Built 2026-10-25 on branch v3-5-clear
  (docs/v3-spike/5-clear-execution.md):
  the pty clear becomes the request half, written to the gate store as a
  typed gate of kind quarantine_clear (nonce, handle, incarnation, the
  config diff's digest), posted over Buzz through the 3-1 and 4-5 path,
  completed only on the owner's signed answer carrying the nonce and
  verified as the four typed gates are; grant key `clear.signature`
  (`required` by default when the Buzz enrolment exists, `pty_only` only
  without one, otherwise grants_invalid); no fallback when Buzz is
  unreachable, the clear waits as an open gate and doctor says why. The
  live row is the third item of the owner's Buzz session. [2.7, reviewer
  plan, 2026-10-25]
  Merged 2026-10-25 as 4d154b8e (branch v3-5-clear, Codex narrow pass
  with two fix rounds, lead gate on the merge result; size of 3.0 72,895):
  every consumer verifies the clear (quarantine_unverified), the sealed
  ClearRecord is the use mark, quarantine_clear is action_reserved for the
  generic raise, the clear is scoped to the launch and condition, a
  task-store-only quarantine gets a typed seeded ledger entry, a missing
  ledger with a sealed head is ledger_tampered, widen whitelists only the
  diff-policy refusal, the exit step blocks on any unnamed refusal, and a
  second completion is already_cleared. [2.7, phase 5 clear result]
- Decided by the owner, 2026-10-25: A. The read-only runtime image moves
  to 3.1; the hash-to-exec window (milliseconds, same-UID only, whose
  writer could also replace the hashing binary) is a recorded same-UID
  limit of 3.0, and the launch path stays as measured with no mount per
  launch (the owner's ground: launch speed). [2.7, owner decision]
  The question as it was put: A
  (recommended by the reviewer): defer the image to 3.1 and record the
  hash-to-exec window as a same-UID limit in 4.1 and R33;
  grounds: amendment 25's full re-hash at every launch covers everything
  before the launch, the image is macOS only (hdiutil), mounts a visible
  volume per launch and waits on the bundle work, so it would be the last
  thing in 3.0 and the first to slip. B: build it in 3.0 (about two
  days, macOS only, after the bundles). [2.7, reviewer, 2026-10-25]
- Decided by the owner, 2026-10-25: 3.0 starts with the owner's existing
  gh CLI login as the harness's push credential, read only through the
  pinned gh copy at push time (R33); no new credential is created, so no
  credential-change gate is needed; the dedicated keychain (A) or the
  GitHub App (B) moves to 3.1. The same-UID limit stands as recorded: a
  seat process can read the owner's gh token from the login keychain,
  guard denies the CLI reads (credential_read) and the scrub removes the
  variables. [2.7, owner decision] The question as it was put: GitHub
  token custody (R33). Both choices
  reduce on macOS to a keychain item whose ACL names the amq-squad binary,
  so a seat's read prompts instead of returning, and guard already denies
  the security CLI to seats. A (recommended): a fine-grained PAT scoped
  to the two repositories with contents and pull_requests write only (no
  release or admin scope), stored once with `amq-squad credential set
  github` into a dedicated amq-squad keychain and read by the harness at
  push time only; the tag ruleset with no bypass actors keeps even that
  token from tagging. B: a GitHub App installed on both repositories,
  its private key stored, one-hour installation tokens minted by the
  harness, every merge and PR attributed to the App, the CI-only rulesets
  admitting it. Recorded limit either way: until the launcher is signed
  (the bundle item), the keychain ACL binds to the unsigned binary's hash
  and every rebuild re-prompts the owner once. Credential change is an
  owner-signed gate kind, so the owner signs this either way; one small
  brief after the answer (credential verb, keychain read at push, doctor
  row). [2.7, reviewer, 2026-10-25]

Release prep.

- Bump the Claude plugin version (plugins/claude, 2.31.0 today). Claude
  Code caches plugins by version, so the 3.0 hooks need a new version to
  install over 2.31. Waits on the release version. Done for the Claude
  plugin (3.0.0, branch v3-5-relprep); the release commit still carries
  the codex plugin manifest, fourteen SKILL.md version lines, the README
  install lines, and scripts/check-release-version.py learning the 3.0
  install path; optional for that commit: pin the ci.yml actions to commit
  SHAs. [2.7, relprep pass, 2026-10-25]
- Recorded limit, with the read-only image question: seat-exec execs the
  provider by pathname after the descriptor hash, so a same-UID
  replacement between the final check and exec runs unverified; closing
  it means exec through the hashed descriptor (fexecve on Linux, /dev/fd/N
  on macOS, where Claude's own re-exec may not survive). It stays
  recorded and is part of the owner's decision on the image. [2.7,
  relprep pass, 2026-10-25]
- The Claude hook watchdog moved from shell to the Go adapter (`guard
  --decision claude`); its Linux row runs under dash in CI before the
  release, since the hook script runs on Linux hosts too. [2.7, 4-2
  re-pass]
- The staged launch record carries the probed claude pin's sha256 and
  seat-exec refuses pin_changed_since_probe when the registry's pin
  differs from it (an owner re-pin between launch and exec, same-UID,
  seconds wide). doctor says "pinned claude is behind the installed
  claude" with the re-pin command when the hard link's bytes are older
  than the installed version. [2.7, reviewer, 2026-10-25]
  Done 2026-10-25 and merged as 0243b631 (branch v3-5-relprep) with
  the Linux CI job for the hook tests under dash, the hooks-API re-check
  (docs/v3-spike/5-hooks-api-recheck.md) and the Claude plugin at
  3.0.0; doctor resolves the installed claude's symlink chain through the
  fsanchor walk one component at a time, under HOME or /opt/homebrew
  only, eight hops, with quoted re-pin operands and a same-filesystem
  check. [2.7, phase 5 relprep result]
- Re-check the Claude hooks API facts the plugin relies on (hooks.json
  shape, CLAUDE_PLUGIN_ROOT, hookSpecificOutput fields, exit 2
  behaviour). They were checked against the installed Claude Code
  2.1.310. Waits on the release's Claude Code version.
- The brief-drafting rows no brief retired (workstream.go,
  briefs_seed.go, drafter/run.go, drafter/config.go), assigned to phase 5
  by briefs ruling 4.
- Stale v2 verb names outside internal/ (README, operator cookbook,
  plugin skills, release docs) go to the 3.0 docs pass (phase 2a row).
- `amq-squad uninstall` (phase 5, branch v3-5-uninstall, 2026-10-25): a
  dry run by default that prints every path it would remove, grouped
  (session state under the AMQ root's extensions/amq-squad, per-seat
  config dirs, the core tools dir, worktrees it created, and under
  ~/.amq-squad the registry, grants, claude-token, gate-binding.json and
  the verifier module cache) and the commands it will not run; --write
  removes, the owner files only with --owner-files as well; it refuses
  while anything runs (uninstall_busy naming the live scheduler, seat,
  proof window or commit step); it never writes the owner's global agent
  config (R16) and prints the `claude plugin uninstall` command and the
  codex trust entries to remove by hand; sealed ledgers, evidence and
  the gate store are kept unless --purge, which removes the evidence refs
  through the hardened git only; secrets go first under --owner-files,
  overwritten once (best effort on APFS) then unlinked, never printed;
  removal runs through the fsanchor descriptor walk and a symlink in
  place of an expected directory is uninstall_refused_symlink with
  nothing removed; idempotent, a partial failure leaves a report and a
  non-zero exit. Rulings from its narrow pass: evidence refs are
  enumerated from git (for-each-ref with symref), never from the ledger,
  a symbolic ref refuses the repository's purge and deletion is
  `update-ref --no-deref -d <ref> <oldoid>`; every read, open, list or
  probe error in the busy check is busy with the path named; a worktree is
  removed only when its project root is the task's project_path from the
  task store, `git worktree list` of that project names it and its .git
  file resolves back into that project; records are removed after
  everything they describe; every named directory is opened O_NOFOLLOW
  before any removal; a hard-linked secret is unlinked without zeroing;
  the uninstall holds the scheduler's own lock for its run and unlinks it
  last; only ENOENT is absent; the seat set comes from this root's launch
  records and task-store snapshot, never a ledger, and a ref is deleted
  only when a sealed ledger entry of this root records it; the live-seat
  probe is strict (a probe failure is busy); the verified worktree is
  held by descriptor from verification to removal with the parent entry
  re-checked by dev+inode (worktree_replaced otherwise); every candidate
  appears in the report with a reason. Merged 2026-10-25 as 889c5cd6
  (branch v3-5-uninstall, five commits, three Codex narrow passes, 54
  reverting rules, lead final read and gate on the merge result; size of
  3.0 79,815). Phase 5's code side is complete with it; the release
  commit follows. [2.7, phase 5 uninstall result]
- After migrate and uninstall merge: the release commit from the
  recorded list, the release gate owner-signed through 7.3's tag-rule
  path, then the owner session rows. Nothing else is planned on the code
  side; the orphan-adoption row and the hygiene rows go to 3.0.x unless
  the owner session surfaces them. [2.7, reviewer plan, 2026-10-25]
- The release commit merged 2026-10-25 as d56d7a0c (branch v3-5-release,
  two commits, reviewer read, lead gate on the merge result): the codex
  plugin manifest and the fourteen skill files at 3.0.0, the README,
  skills docs and RELEASING.md on the /v3 module path, check-release-version
  passing for 3.0.0 with the /v3 install path, docs/v3.0.0-release-notes.md,
  MIGRATION.md's "Moving to 3.0" section, and the eleven CI actions pinned
  to commit SHAs read from the GitHub API with the responses' digests in
  the log. Phase 5's code side is complete; what remains is the owner's:
  the owner-signed release gate (the tag ruleset of 7.3, then the tag and
  release through the release flow) and the owner session rows of this
  section. [2.7, phase 5 release result]
- The hung-guard test flake merged 2026-10-25 as c15ac65b (branch
  v3-5-hygiene2, reviewer read, lead gate on the merge result): the last
  code merge before the tag, test synchronisation only (the stand-in's
  readiness by rename, a measured watchdog floor, a hang guard on the
  pid file). The object-store purge (owner session item 8) is done and
  the pre-push history scan is clean (spike log, release-prep list;
  docs/v3-spike/5-release-evidence/history-scan.txt). The tag target is
  the v3 tip after this entry's commit. [2.7, phase 5 hygiene2 result]
- Owner decision 2026-10-28: the AMQ bump planned as 3.0.1 moves into
  3.0.0 before the tag. Upstream shipped v0.108.0 to v0.116.1 between
  2026-10-25 and 2026-10-28 (21 releases); every ask in section 11 is
  closed, #1178 included (PR #1254). Two briefs: 5-amq-A (go.mod to
  v0.116.1, pi seats through the upstream adapter with PiAdapterInLibrary
  flipped, the two Codex sandbox pins as provider arguments at launch with
  seat-exec verifying rather than injecting, doctor's installed-AMQ floor
  at v0.116.1, the adoption-seam constants) and 5-amq-B (the strict share
  bound to amq-remote's mailbox adapter, 4-5 deviation 1 closed by the
  upstream kind; the attachment's own DM read, marks and redelivery go
  away; the verifier's signed-event check stays; design points from the
  reviewer before dispatch). The tag target moves to the v3 tip after
  both merge. Ask 8 part 2 (the pi bridge event id) stays out of 3.0
  unless the reviewer rules otherwise. [2.8, owner decision]
- Brief 5-amq-A merged 2026-10-28 as fb0363c2 (branch v3-5-amq-a, nine
  commits, Codex pass, one fix round, narrow re-pass, lead final read and
  gate on the merge result; size of 3.0 80,378): go.mod at AMQ v0.116.2
  (the module diff against v0.116.1 confined to internal/acp and
  internal/remote/buzzio in production Go), the adoption-seam constants
  and the registry pin at the same version, doctorMinAMQVersion 0.116.2 as
  the one floor with amq_below_floor rows for amq, amq-remote and
  amq-owner-sign; pi seats through AMQ's pi adapter: the launch writes a
  0700 shim at ~/.amq-squad/pi-launch/pi (atomicfile, exact mode,
  repaired) that answers the adapter's --version and --help probes through
  `amq-squad pi shim` from the verified embedded runtime, the record
  carries pi_shim and pi_agent_dir, seat-exec compares argv[0] with the
  record and checks the shim through one O_NOFOLLOW descriptor
  (pi_not_embedded otherwise; a FIFO refuses at once), then execs
  `amq-squad pi run --seat H -- <adapter argv>` with the record's env_needs
  and credential_needs as --env-need, the compile cache off, NODE_OPTIONS
  empty and NODE_PATH absent; the session id is a v8 UUID of (nonce,
  handle) and the 23(a) resume refusal fires on it; the Codex sandbox pins
  travel as provider arguments in the launch request (AMQ v0.110.0 admits
  them, ask 17) and seat-exec verifies them (sandbox_pins_missing) instead
  of injecting; CI's real-AMQ matrix is one job with a required pinned
  v0.116.2 lane and a non-blocking latest canary, the v0.60.0, v0.74.1 and
  wake lanes retired (two ran nothing); two orchestration_contract
  subtests skipped with the cause named in three places (ported in
  5-amq-C). Live: a pi seat through amq launch 5 of 5 (Prepare 14 ms,
  launch to up 2.5 s with about 1 s the probe pair, up to ready 0.8 s;
  cold node-ready 520.7 ms with the cache off against 488.8 ms on).
  `amq coop exec` as the pi fallback is retired at the v0.116.2 floor; the
  scheduler stand-in's deletion is 5-amq-C (not pure: three test rewrites,
  the scheduler post verb, the sched-live harness, PiAdapterInLibrary).
  Owner list: the CI-only rulesets' required-check names follow the new
  job names. [2.8, phase 5 amq-A result]
- Brief 5-amq-B merged 2026-10-28 as 5f1a8261 (branch v3-5-amq-b, five
  commits, full Codex pass, two fix rounds, narrow re-pass, reviewer read
  of the deletion rule, lead final read and gate on the merge result;
  size of 3.0 81,351): the strict share bound to amq-remote's mailbox
  adapter (ask 15; handle `attach`, reply_timeout 60 s, DM commands on),
  serve delivering the owner's DMs into the share root's inbox with the
  `nostr:<eventID>` label and taking the attachment's one non-status
  reply as the result row; the attachment's DM reader, marks, backlog
  read and quarantine deleted (4-5 deviation 1 closed); every message's
  event fetched by id on the core's own bounded connection (10 s cap,
  closed after) and the handler acting only on the owner-signed event,
  never the AMQ body, routing included; the deletion rule: a kind 5 or
  9005 signed by the owner's pinned key naming the question, an edit of it
  or the answer refuses altered; a body-signed deletion since the question
  refuses altered_history (our body never deletes); an owner-signed
  deletion whose target the relay hides refuses altered_history unless
  every k tag is 7, answered with the gate's one re-ask; the gate store's
  lock bounded by the verification context (verify_timeout); the
  hung-verification alert in the status document's attention queue; routed
  events deduplicated durably; forwards redacted; a doctor row verifying
  the share grants locally; owner-run.md re-walked with the live rows
  waiting on the owner's AMQ upgrade. Recorded limits: the busy window
  (R42), a forward failing after the mailbox copy may deliver that copy
  twice, a gate whose one re-ask is spent is raised again, the k=7
  exemption rests on NIP-09's optional tag (owner-run row 5.6 settles
  whether Buzz Desktop tags the owner's reaction removals), the body key
  still signs the question post and the re-ask (R33). Measured on a
  scratch-built v0.116.2 against a local relay: handler 144 ms, answer to
  delivery 222 ms, answer to the result row 2.0 s. [2.8, phase 5 amq-B result]
- Brief 5-amq-C merged 2026-10-28 as 25fa922e (branch v3-5-amq-c, three
  commits, Codex narrow pass, one fix commit, lead final read and gate on
  the merge result; size of 3.0 81,146, v2 rows kept 27,885): the
  scheduler stand-in deleted (schedproc.External, the request and event
  files, `amq-squad scheduler post`, spike/sched-live, PiAdapterInLibrary;
  grep proofs in docs/v3-spike/5-amq-c-evidence/grep-proofs.txt); the
  three tests that used it rewritten against a fake launcher and
  Scheduler.Post through the Started hook, with the barrier row still
  proving the decision socket stays absent until adopted seats are
  confirmed; the real-AMQ orchestration contract ported to a schema-valid
  team (claude and codex runtimes, destructive_filesystem as the one
  action an AMQ operator can answer in a test) and the review subtest
  replaced by a named-profile refusal row asserting team.ErrNamedProfile
  over an empty real root (dispatch gains a three-line CheckProfile guard
  so the sentinel is typed); the ci -skip gone; the pinned lane green
  against amq v0.116.2 built from the module. Gate note: the race step's
  package list named the deleted spike directory (setup failed); the three
  real packages passed under race and the script now skips deleted
  directories. Follow-up (3.0.x): the scheduler's async-close path has no
  production implementer after the deletion (CloseUnconfirmed's delegation
  always confirms); its removal is recorded in follow-ups and the manifest
  rows. hook_guard's classification of `amq coop exec` stays: AMQ ships
  the verb and guard refuses a seat starting one. [2.8, phase 5 amq-C result]
- Brief 5-ci-D merged 2026-10-29 as 23d8a9e4 (branch v3-5-ci-d, six
  commits, reviewer read of the production diff and rows, lead gate on the
  merge result; size of 3.0 81,230): the first CI run on v3 found three
  Linux-only failures and one runner-dependent test, none visible on
  macOS. D1: the decision socket's dial side used the recorded full path,
  which Linux refuses past sun_path (the listener was already bound-safe
  through /proc/self/fd); seatlaunch.DialPrivate dials through the anchored
  launch directory (or darwin's temp directory, off Linux), requires a
  socket owned by this user with symlinks not followed, canonicalises the
  dialer's root (task widen's root is not symlink-resolved, the
  scheduler's is), and SendDecision takes the root; the deep-root row's
  reverting proof on darwin is CI's Linux job; recorded limit: the amqsq-
  case trusts any 0700 own directory the launch record names, the launch
  record's same-UID limit. D2: a fixture seeded pid 42, a live kernel
  thread on a fresh Linux runner; seeded pids now sit above both pid
  limits. D3: the real-AMQ lane's nested-worktree proof and the
  adoptionseam Apply test get stub claude and tmux probes for their one
  setup call, so no gated test needs a host tool (and the Apply test no
  longer opens a session on the host's tmux server). D4 deferred to 3.0.x
  (status.Collect's bare signal-0 liveness; recorded in follow-ups and
  docs/status-contract.md). Gate note: TestOwnerScriptDryRun (internal/buzz,
  a 20 s wait on the thread answer through a real amq-remote and a local
  relay) failed twice in the full-suite run under the host's load (an
  agent building in parallel) and passed 3 of 3 alone on the merge result;
  on the test-hygiene list for 3.0.x (a measured bound, not a constant).
  [2.8, phase 5 ci-D result]
- Brief 5-skills-E merged 2026-10-29 as 7630adf0 (branch v3-5-skills-e,
  eleven commits, reviewer read, lead gate on the merge result; no Go
  production code): the seven plugin skill sources, their generated
  mirrors and docs/skills.md rewritten for the 3.0 command surface from
  the binary's own help (the help digests of 78 verb and subcommand
  --help calls recorded in the evidence), the v2 verbs removed; make ci's
  skill gates green (48 commands with every path resolving, 131 of 166
  flags verified; 69 documented invocations executed cleanly under a
  throwaway HOME and XDG config with GH_TOKEN, GITHUB_TOKEN and GH_HOST
  dropped and a refusing gh stub first on PATH, so the gate is offline by
  construction; the seven documented doctor invocations that run `gh
  --version` are skip-marked with the reason; the invocation check used to
  run under the real HOME, where a documented typed gate raise could have
  posted to the owner's Buzz DM); the command check reads positional-path
  verbs; pandoc pinned to 3.11 on both sides (the Makefile refuses another
  release; ci.yml installs the 3.11 deb; if Linux renders differently from
  macOS the committed render becomes the Linux one); the propose step's
  digest check written into the wizard and orchestrator skills (with
  --from FILE compare the file's sha256 before apply; after apply compare
  squad.yaml and team-rules.md at the printed paths; team-rules.md cannot
  be checked before apply until the verb shows bytes, 3.0.x). Eleven verb
  gaps found and listed, not worked around: four go to brief F (task
  widen's help showing --me, verify action's exit 12 in its help, the three
  empty or failing --help outputs, a repeated per-seat flag on team
  propose refused instead of silently kept), the rest to 3.0.x. Also
  found: make release-check fails on v3 (check-release-version.py still
  requires the v2 AMQ policy sentence) and README.md and the global
  runbook are still v2 content; brief F. [2.8, phase 5 skills-E result]
- Platform decision 2026-10-29 (reviewer ruling on the plan's own target,
  stated to the owner as reversible; B is 3.1 work): 3.0 ships for darwin
  arm64; on Linux the binary builds and the Claude hook script is tested;
  seats are unsupported. Found by the second CI run on v3 (40955238412,
  make ci on ubuntu-latest, 24 failures in three packages, none on macOS
  where every gate ran): (a) the launch's proof socket under the runner's
  long temp paths (helper_dial_failed, no_proof, bind invalid argument),
  the sun_path family of D1 on the proof side, a production exposure on a
  Linux host with a deep project path; (b) ext4 and tmpfs reuse an inode
  right after unlink and recreate, so the dev+inode identity checks against
  replacement (the routing seed, the proof socket after the probe, the
  anchored directories) pass a replaced object on Linux; APFS never reuses
  one; the same-UID limit does not cover this, since these checks exist to
  catch same-UID replacement; (c) two cancel and close rows (process
  semantics on the runner); (d) gatestore TestSquadIDIsCanonical (likely
  /tmp against /var in the fixture). CI: the gating go test runs on a macOS
  runner; the Linux job keeps build, vet and the hook script and runs the
  suite as a continue-on-error canary with the known-red set listed.
  README and the release notes carry the platform statement; follow-ups
  3.1 carry the four items with their failure scenarios. [2.8, platform]
- Brief 5-docs-F merged 2026-10-29 as ad8fda9b (branch v3-5-docs-f, six
  commits, reviewer read of the Go part, lead gate on the merge result;
  size of 3.0 81,309, v2 rows kept 27,945): README.md rewritten for 3.0
  from the verbs' help and the release notes (26 README commands run under
  a throwaway HOME with the gh stub, 0 failures, four skip-marked with
  their reason; the grants.yaml and pins examples read by doctor; the
  platform paragraph; `amq coop init` as step 1; the stated limit that no
  verb turns squad.yaml into the seats file manual launch takes), README.html
  regenerated with pandoc 3.11; the global orchestrator runbook retired to
  a one-paragraph pointer; scripts/check-release-version.py reads the floor
  from doctor's constant (fail-closed on a missing, repeated or non-literal
  line; a Go row ties it to doctorMinAMQVersion) and `make release-check
  VERSION=v3.0.0` is green; four help and refusal fixes with rows (task
  widen's usage states R4 instead of --me; verify action's exit 12 in its
  help; amq --help, pi verify --help and verify release-plan --help print
  their usage; team propose and apply refuse a repeated per-seat flag
  naming the comma form); the skill gate's gh stub answers --version only
  and the 76 documented invocations run offline; make ci's gating suite on
  a macOS runner with the Linux job as build, vet, hook script and a
  continue-on-error test canary listing the known-red packages. Manifest:
  team.go's shrink target raised 350 to 365 for the refusal (reason in the
  row; target sum 35,185 within the band). Gate note: TestOwnerScriptDryRun
  failed once more in the full suite under load and passed 3 of 3 alone on
  the merge result (the hygiene-list entry). [2.8, phase 5 docs-F result]
- **3.0.0 tagged and released 2026-10-30** at 813818cb through the release
  flow: `release prepare` from the owner's operator session (a scratch
  fleet under the owner's home; a root under /private/tmp is refused by
  the compound release store's owner check, since macOS gives files there
  the wheel group) raised the tag and github_release gates at 05:07:06Z;
  the owner answered both in their Buzz threads (github_release handled
  05:08:23Z, tag 05:08:32Z, both approved and verified); `release publish`
  at 05:08:57Z tagged 813818cb and created the release, published at
  05:09:03Z (https://github.com/omriariav/amq-squad/releases/tag/v3.0.0),
  the tag's commit read back equal to the target. CI run 40958849281 green
  on all four jobs at the tip below the target. Evidence
  docs/v3-spike/5-release-evidence/release-2026-10-30/. [2.8, release]
- migrate and the squad.yaml final form (the phase 5 table row), one
  brief after the verifier merge, because squad.yaml's package takes the
  verifier's add-only classes first. The final form freezes schema 1 for
  3.0: the complete key list in docs with each key's relation to the
  grant (narrow only; add only for verifier_classes; never widen),
  the schema key required, unknown keys refused (the strict subset), an
  embedded golden fixture that doctor self-checks against as 2b-3 did for
  the status contract, and a row that a 3.0.x reader refuses schema 2.
  The digest flow is the only legitimate writer: propose records the
  sha256 of the on-disk squad.yaml (or absent) in the proposal, apply
  re-reads the file under the lock it writes with and refuses
  squad_yaml_changed with both digests when they differ, and a mismatch
  of the proposed content is squad_yaml_digest_mismatch, both code
  tokens; squad.yaml is a protected path in the commit step's policy, so
  a seat diff touching it raises the growth gate, and it is read from the
  lead's checkout at project_path at squad load and scheduler start.
  [2.7, reviewer ruling, 2026-10-25]
  migrate is a one-shot converter from v2 profiles (decision 6): reads
  the v2 files read-only with the field names from main's profile loader,
  writes squad.yaml only when absent (an existing file is refused, never
  overwritten), prints an unmapped-fields report naming every v2 key with
  no 3.0 meaning, never writes grants.yaml or registry.yaml (owner files)
  but writes grants.yaml.proposed (0600) beside them for the owner to
  read and move, and refuses when a v2 field would change a safety
  default (launcher_preference, any allow list) rather than dropping it.
  Every byte the verb emits passes one redaction funnel at the verb
  boundary (a match prints a fixed migrate_refused_credential line with a
  closed location class, exit 2, nothing else); the run holds one root
  descriptor from plan to apply and reads and removes only through it
  (root_changed when the path no longer matches), the plan refusing
  before anything is touched and a change during apply stopping at the
  first refusal with a partial report. Merged 2026-10-25 as bee6ffd5
  (branch v3-5-migrate, four commits after the base, three Codex narrow
  passes, 52 reverting rules, lead final read and gate on the merge
  result; size of 3.0 77,852): docs/squad-yaml.md with the key list and
  the 20 never-settable keys named on refusal, the golden self-check,
  schema 2 refused, squad.yaml a protected path, the composite apply
  digest with squad_yaml_changed and squad_yaml_digest_mismatch, and
  `amq-squad migrate` as a dry run by default with --write, --remove-v2,
  the exact unmapped report, grants.yaml.proposed, the safety-default and
  credential refusals, the one output funnel and the one root
  descriptor. [2.7, phase 5 migrate result]

Owner session. Each item needs the owner at the keyboard; agents prepare
the runs and record them.

1. The Buzz owner run with the real key: one session for 4-5 acceptance
   4 (one typed gate answered from the phone, one message routed to a
   lead) and 3-1 acceptance 9. The runbook is `docs/v3-buzz/owner-run.md`.
2. The `claude setup-token` file `~/.amq-squad/claude-token`. It unblocks
   milestone B's Claude live rows and 4-2's live fault table. Two rows
   added 2026-10-25: install plugin 3.0.0 over 2.31 and read the context
   hook's digest line to settle whether Claude's plugin cache is keyed by
   version; a tool outside the seat's allow list under dontAsk is
   auto-denied with the hook silent (4.3).
3. `amq-squad trust` of the milestone B fixture path. It unblocks
   acceptance 7.5 with a real Codex seat (codex-cli trusts by repository
   root, 7.3).
4. The S6 dry run of the merge train against the 18 open S6 draft PRs,
   then (re-scoped 2026-10-29) the real run on a harness-bound PR for 3-2 acceptance 4 after the owner sets the merge
   grant and the CI-only rulesets for amq-noc and workspace-cli. Both
   runs include the GraphQL rebase liveness check: if a PR that needs a
   rebase never lands, the rebase moves to the local form (fetch, rebase,
   push --force-with-lease, bind the pushed SHA).
5. The AMQ release that carries PR #1188, installed by the owner, then 4-1
   acceptance 10 (pi launch through `amq launch`).
6. amq-noc 4-4 lands by PR on the release train (held by owner decision),
   after its fixture refresh for lead_handle and live.
7. Upstream asks 15 to 17 filed under the owner's login (section 11).
   Done 2026-10-25: #1210, #1211, #1212.
8. The reflog expiry and gc of the shared object store after the
   milestone B exposure, and the rotation of both credentials (briefs
   ruling 18).
9. The Codex notify line: the owner makes the fan-out script Codex's
   notify command in `config.toml`, from the candidate line `amq-squad
   trust <repo>` prints; `doctor` checks its presence (16.8).
10. The claude pin: a hard link named `claude` to the versioned Claude
   Code binary on the same filesystem (a symlink is refused), for example
   `ln ~/.local/share/claude/versions/<version> ~/.amq-squad/bin/claude`,
   pinned in `~/.amq-squad/registry.yaml` with its absolute path and
   sha256; re-pin after every Claude Code update (the hard link keeps the
   old bytes until then, by the digest-pin design). Without it a launch
   with a Claude seat is refused (tool_unpinned). The gh pin the same way
   for an HTTPS push_url. [2.7, phase 5 re-pass]

Open acceptance rows of phases 2 to 4. The last column points at the
owner-session item above.

| Brief | Row | State | Waits on |
| --- | --- | --- | --- |
| 2b-2 | 9 role-delivery matrix, N=3 over Claude and Codex | milestone A's pointer arm at N=1 only | items 2 and 3 |
| 2b-2 milestone B | 7.5 a Codex seat under workspace-write ends with a harness-pushed branch | fixture only | item 3 |
| 2b-2 milestone B | Claude live rows: plugin load, probes a and b, the S4 fault table under dontAsk, the Claude half of the role matrix (the Bash-tool scrub candidate CLAUDE_CODE_SUBPROCESS_ENV_SCRUB is withdrawn for Claude Code 2.1.310, where it forces permission mode default; the token's visibility to the seat's Bash children is R33's recorded limit) | not run | item 2 |
| 3-1 | 9 owner run with the real Buzz key | passed 2026-10-29: the owner's thread answer to gate g45-owner-01 verified by the core (approved, authorization id), gate state accepted, `verify action` exit 0 (docs/v3-spike/4-5-evidence/owner-run-2026-10-29/) | done |
| 3-2 | 4 the first real run merges a PR the 3.0 harness commit step produced and bound (re-scoped 2026-10-29, reviewer ruling: the S6 drafts have no 3.0 task binding, by design P1-4; the tag's merge-train evidence is dry run pass 5, protection met on both repositories, 9 + 4 planned, 5 refused as predicted, digests unchanged) | owner-session row, not a tag gate; the PR lands on amq-noc or workspace-cli (covered by the grant and a CI-only ruleset): the row's task is a small 3.0 squad task on amq-noc (the 4-4 fixture refresh), so the commit step produces the PR the train merges | item 4 |
| 4-1 | 10 pi launch through `amq launch` | the path is merged (5-amq-A, live 5 of 5 on a scratch-built v0.116.2); the row on the installed AMQ waits on the owner's upgrade | item 5 |
| 4-2 | 3 the fault table against a real Claude seat | owed to milestone B's live run | item 2 |
| 4-4 | amq-noc 1.0 command center on amq-noc main | review-complete on its branch | item 6 |
| 4-5 | 4 owner run with the real key | passed 2026-10-29 on the mailbox adapter: enrollment (8 grants, the DM channel bound), one typed gate answered from the phone in the thread (raise 20:48:57Z, handled 20:50:27Z), the re-ask row (an answer in the main box refused not_in_gate_thread, the thread answer accepted), /status answered by serve, one message routed to the squad lead's inbox with the nostr and routed labels, presence off; the signed seat clear live row stays open (its fixture needs core records only the test helpers write; a rehearsal verb is a 3.0.x line) | done; the clear row open |

[2.7, phase 2 results]

### 16.5 Process ownership

The amq-squad binary starts and supervises one scheduler per squad. The
scheduler recovers from the task store on restart. The scheduler is
`amq-squad scheduler run`, supervised by the lead's team mode (4.5);
restart recovery is phase 5 (16.4). [2.7, briefs ruling 15] The lead and the
mayor run on the built-in pi with a named default model from the
registry, on API keys. The fleet registry is `~/.amq-squad/fleet.yaml`.

### 16.6 Requirement to phase map

- Phase 1: R3, R5, R12 probe design, R46; spike exit criteria below.
- Phase 2a: R6, the three verbs behind R35 and R36, section 10 deletions.
- Phase 2b: R9, R10, R11, R13, R17, R18, R19, R20 to R28, R43, R48 to R52 [2.4], and R4's seat-exec work (2b-2). [2.6, briefs ruling 10]
- Phase 3: R16, R30 to R34, R37.
- Phase 4: R1, R2, R4's hook side, R7, R8, R14, R15, R35, R36, R38 to R42, R44. [2.6, briefs rulings 9 and 10]
- Phase 5: R29, R45, R47, migrate, uninstall, CI, bundles, docs. [2.6, briefs ruling 9]

Jev (R29) is phase 5, because section 13 wins over the earlier map. R4's
seat-exec work is phase 2b (2b-2), and R4's hook side stays phase 4. [2.6, briefs rulings 9 and 10]

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
amq-squad never writes `config.toml` (4.3). Decided in 2b-3: `amq-squad
trust <repo>` prints the owner-written notify line as a candidate and
`doctor` checks its presence only; nothing writes `config.toml`; making
it Codex's notify command is the owner's config step, listed under the
owner session as item 9 of 16.4. [2.7, 2b-3 ruling 2] (The 2.5 candidate
direction, the trust step as the natural home, is what was decided.)
[2.5, amendment 20, candidate] Until then Codex
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

### 17.4 Revision 2.6 change list

- Header: revision 2.6, date 2026-10-24; intro sentence names amendments 23 and 24, the phase 2a close and the phase 2b/3/4 briefs, tag form "[2.6, <source>]".
- 3.6: amq-noc is a command center (amendment 24).
- 4.1: pi seats launch and resume through the upstream mint-mode adapter (PR #1188); version rule (upstream floor 1.2.0 release builds, our 1.3.0, pin stays); `coop exec` is the fallback (amendment 23).
- 4.1: upstream resume is create-if-missing; `seat-exec` or `doctor` refuses a pi resume when the ledger shows a tool result and the session file is missing (amendment 23).
- 4.3: provider-argument sentence holds for Claude and Codex only; pi admits no provider arguments; watch item (amendment 23).
- 4.3: pi seats on the launch API, `coop exec` as the fallback (amendment 23).
- 4.6: status contract fields (task title, tokens in and out nullable, last message, prompt state, "new task" argv) and the bucket table (amendment 24, briefs ruling 6).
- 4.7: amq-noc 1.0 command center, only view, layout, keys, mapping, four controls (amendment 24).
- 11: upstream status of 2026-10-24 paragraph; rows 1, 4, 8, 11, 12, 13 and 14 updated (upstream status).
- 12, R1: upstream pi floor 1.2.0, pin stays (amendment 23).
- 12, R4: provider arguments apply to Claude and Codex seats only (amendment 23).
- 12, R5: rewritten for the shipped adapter, create-if-missing resume and our refusal (amendment 23).
- 12, R52: v0.107.0 #1176 as evidence from the other side (amendment 23).
- 12, R43 and R44: command-center fields and the only view (amendment 24).
- 13: phase 4 row names the pi launch adapter and the command center; new paragraph names `docs/v3-phase2-briefs.md`, the first-wave rule and the dependency order (phase 2b/3/4 briefs).
- 14: pi launch risk restated (amendment 23).
- 15: a 2026-10-24 revision 2.6 entry appended.
- 16: intro notes 2.6; 16.6 corrected: Jev (R29) to phase 5, R4 split between phase 2b (seat-exec) and phase 4 (hook side) (briefs rulings 9 and 10).
- 17: this list.

### 17.5 Revision 2.7 change list

- Header: revision 2.7, date 2026-10-25, phase 2 closed (milestone B result); intro names amendments 25 to 30, briefs rulings 15 to 19 and the phase 2 results, tag form "[2.7, <source>]".
- 3.2: the solo lead keeps its tools; the team lead has no Bash tool (amendment 26).
- 4.1: the pi resume refusal landed in milestone A as `pi_resume_context_lost` (2b-2 milestone A result).
- 4.3: the pi launcher supervises node (amendment 26); the Codex sandbox pins as the one added provider flag, ask 17 (amendment 28); dontAsk, the seat settings file, the per-seat config dir and the token file (milestone B ruling); `SSH_AUTH_SOCK` scrubbed and `launchctl getenv` denied (2b-2 and 3-2 results); layered config enforcement, the quarantine on every terminal path and `seat clear` from the owner's real pty (amendment 28); trust step and traversal fixes landed (2b-3, milestone A); `task widen` over the scheduler socket (amendment 28); the shape of the harness commit step, hardened git, the draft PR bound through BindPR, CleanSeat (amendment 28, briefs ruling 19, milestone B result).
- 4.4: the Claude live rows wait for the token file (milestone B result).
- 4.5: the scheduler process, its barrier and slots (briefs rulings 15 and 19, milestone B result).
- 4.6: the bucket table fixed by 2b-1 (2b-1 result); the fleet document (briefs ruling 16).
- 4.7: "n" runs `task add`, dispatch is the scheduler's (amendment 25); the old board's delete list and frame time (briefs ruling 17); amq-noc lands by PR on the release train (4-4 status).
- 7.2: the widen path after rulings R4, R5, R8 and R9, the lead's recorded incarnation, pane label not an identity (amendment 28).
- 7.3: Codex sandbox and pins (amendment 28); the PR binding keyed by repository and PR number (3-2 and milestone B results); squad identity in signed gate questions (amendment 30).
- 11: asks 15 to 17 drafted, not filed (upstream drafts).
- 12: R4 (amendments 26, 28, milestone B result), R16 (amendments 27, 28, milestone A result), R17 (briefs ruling 15), R19 (amendment 26), R32 (amendment 30), R43 (briefs ruling 16), R44 (briefs ruling 17), R52 (amendment 28, briefs ruling 19).
- 13: the phase 5 row points at 16.4; evidence hygiene and the gate process rules (briefs ruling 18, spike log, 3-2 result); the review rule (amendment 29); the role-delivery matrix status (milestone B result).
- 14: the harness step landed (milestone B result); layered config enforcement (amendment 28).
- 15: a 2026-10-25 revision 2.7 entry appended.
- 16: intro notes 2.7; 16.1 the phase 2 size line (milestone B result); 16.3 the fleet document (briefs ruling 16); 16.4 the phase 5 inputs, the owner session and the open acceptance rows (spike log phase 5 lists, phase 2 results); 16.5 the scheduler process (briefs ruling 15).
- 17: this list.
