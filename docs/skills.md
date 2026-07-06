# Using the amq-squad skills

amq-squad ships **two primary skills** to two plugin marketplaces — one for
**Claude Code** and one for **Codex**. The primary skills are `amq-squad` for
operator/member workflows and `amq-squad-orchestrator` for lead-agent bootstrap.
The older `amq-team-setup` and `amq-squad-role-creator` entries remain as
redirect stubs so existing invocations point back to `amq-squad`.

For the binary's full verb/flag reference, see the main [README](../README.md).
This document is about the skills.

- [The mental model](#the-mental-model)
- [Which skill do I reach for?](#which-skill-do-i-reach-for)
- [Installing and invoking](#installing-and-invoking)
- [`amq-squad` — coordinate a live team](#amq-squad--coordinate-a-live-team)
- [`amq-squad-orchestrator` — lead a squad](#amq-squad-orchestrator--lead-a-squad)
- [Setup inside `amq-squad`](#setup-inside-amq-squad)
- [Role authoring inside `amq-squad`](#role-authoring-inside-amq-squad)
- [End-to-end walkthrough](#end-to-end-walkthrough)
- [Troubleshooting](#troubleshooting)

## The mental model

The two primary skills map onto the actor model:

| Phase | Skill | You use it... |
| --- | --- | --- |
| Setup, role authoring, live coordination | **`amq-squad`** | operators and member agents use it to capture a goal, draft the brief, pick roles/profile, author custom roles, bring members up, drain inboxes, route handoffs, request reviews, check status, stop/resume/fork. |
| Lead orchestration | **`amq-squad-orchestrator`** | when one agent is the **lead** that spawns, dispatches, and monitors the others and owns the deliverable. |
| Deprecated redirects | `amq-team-setup`, `amq-squad-role-creator` | kept for compatibility; both tell you to use `amq-squad` and its Setup / Role Authoring sections. |

They sit on top of three durable layers that setup creates and coordination
consumes:

- **Norms** — `.amq-squad/team-rules.md` (generated; the single source of truth).
- **Goal** — `.amq-squad/briefs/<session>.md` for the default profile, or
  `.amq-squad/briefs/<profile>/<session>.md` for a named profile.
- **Persona** — `<agent-dir>/role.md` (seeded at launch, then user-editable).

`CLAUDE.md` / `AGENTS.md` carry only a small managed **pointer stub** linking to
those three; they never duplicate the content.

## Which skill do I reach for?

| If you want to... | Reach for |
| --- | --- |
| Start from a ticket / prompt / doc and stand up a new team | `amq-squad` Setup section |
| Turn a Jira/GitHub/URL goal into a confirmed brief | `amq-squad` Setup section |
| Decide who leads an orchestrated squad | `amq-squad` Setup section wires it; `amq-squad-orchestrator` runs it |
| Bring the configured team up and coordinate it | `amq-squad` |
| Drain your inbox, route a handoff, request a review | `amq-squad` |
| Check the status board / Mission Control / health | `amq-squad` |
| Spawn child agents and drive them to completion as the lead | `amq-squad-orchestrator` |
| Add a role that isn't in the catalog | `amq-squad` Role Authoring section |
| Debug raw AMQ outside a squad | the separate `amq-cli` skill |

Rule of thumb: **operators use `amq-squad`; lead agents use
`amq-squad-orchestrator` at bootstrap**. Do not collapse the two further.

## Installing and invoking

Install the marketplace once; the skills are then discovered automatically.

- **Claude Code:** `/plugin install amq-squad@amq-squad`, then invoke a skill as
  `/amq-squad:<skill>` — e.g. `/amq-squad:amq-squad`.
- **Codex:** install the Codex marketplace, then invoke a skill as `$<skill>` —
  e.g. `$amq-squad`.

The primary skills are user-invocable and require the `amq-squad` binary on
`PATH` (`go install github.com/omriariav/amq-squad/v2/cmd/amq-squad@latest`).
Their workflows are equivalent across the two marketplaces; the platform
metadata differs (a Claude skill carries `trigger` / `allowed-tools` /
`argument-hint`; the Codex skill omits them).

---

## Setup Inside `amq-squad`

**Invoke:** `/amq-squad:amq-squad` (Claude) · `$amq-squad` (Codex), then use
the `## Setup` section. **Use when:** no team exists yet, or you are starting a
new piece of work and want a real goal/brief in place before launch.

It is a **wizard**: it walks five steps and confirms with you at each gate.
Setup stays read-only until the final create step (no live launch).

### The five steps

1. **Capture the goal (any source).** Tell the skill where the goal lives and it
   fetches it with whatever tool the agent has, detecting the source type:

   | You give it | It fetches with |
   | --- | --- |
   | a one-line prompt / inline text | the text itself |
   | a local file / path (`./design.md`) | reads the file |
   | a GitHub issue (`#96`, a URL, `owner/repo#96`) | `gh issue view` |
   | a GitHub PR | `gh pr view` |
   | a Jira key (`PROJ-123`) | the Atlassian MCP / a `jira` CLI |
   | a URL (Confluence / doc) | the Atlassian MCP / a fetch tool |

   Tracker integrations live **in the skill**, never in the amq-squad binary —
   the core stays tracker-neutral. If no integration is available, the skill
   asks you to paste rather than inventing content.

2. **Draft and confirm the brief.** The skill normalizes whatever it fetched
   into one canonical shape and **shows it to you to edit before saving** (a raw
   ticket description is not a brief):

   ```md
   # <session> brief
   ## Goal          # the outcome, 1-2 sentences
   ## Source        # JIRA PROJ-123 / gh#96 / URL / file: / "operator prompt"
   ## Scope
   ## Out of scope
   ## Acceptance     # how we know it's done; who signs off
   ```

3. **Roles and profile.** Pick the roster (built-in personas or custom roles),
   the binary behind each (`codex`/`claude`), the team-home, and whether to use
   the default profile or a named one.

4. **Orchestrated? Who leads?** Default is **no** (a flat, peer-to-peer squad).
   Say yes and name exactly one lead role to run an orchestrated squad.

5. **Review and create.** The skill prints a summary, then on your confirmation
   creates `team.json` + `team-rules.md`, saves the brief, writes the pointer
   stubs, validates, and prints the next commands. For a squad with two or
   more Claude members it also asks whether the **workers** should run with a
   trimmed plugin/hook surface and, if so, generates + wires the context
   overlays in one command (`amq-squad team overlay init --workers ...`,
   v1.9.0+) — the lead keeps the full configuration.

### What it runs

```sh
# flat team
amq-squad new team --roles cto,fullstack,qa --binary cto=codex --sync

# orchestrated team (step 4 said yes, cto leads)
amq-squad new team --roles cto,fullstack,qa --orchestrated --lead cto --sync

# preview anything first
amq-squad new team --dry-run --json --roles cto,fullstack,qa --orchestrated --lead cto
```

`--orchestrated [--lead ROLE]` records the lead in `team.json` and writes a
generated `## Orchestration` reporting norm into `team-rules.md` when that file
is first seeded — a structured flag, not pasted prose, so it can't drift. (An
existing `team-rules.md` is left untouched; regenerate with `amq-squad team
rules init --force`.) Default off; exactly one lead; the lead is a team member,
never the operator.

When the wizard is done, continue in **`amq-squad`** for the first live launch.

---

## `amq-squad` — coordinate a live team

**Invoke:** `/amq-squad:amq-squad` (Claude) · `$amq-squad` (Codex)
**Use when:** `.amq-squad/team.json` already exists and you want to run the team.

This is the everyday skill. The lifecycle is one small state machine:

```
(none) --up--> running --stop--> stopped --rm/archive--> (none)
                  ^                  |
                  +----- resume -----+
```

### The daily loop

1. **Orient.** Confirm the team-home + profile + workstream, then read the
   selected namespace's brief.
2. **Discover live state.** `amq-squad status` (board), `status --session <name>`
   (detail), `amq-squad console` (live TUI), `amq-squad doctor` (health,
   including PATH and Codex skill-cache alignment).
3. **Bring members up.** `amq-squad up <session>` (NEW work; refuses an existing
   session), or `resume` to continue one.
4. **Route + drain.** Hand off over AMQ, request reviews, drain your inbox.
5. **Stop / fork / tear down** as the work finishes.

### Key verbs

| Goal | Command |
| --- | --- |
| Bring the team up on new work | `amq-squad up <session>` |
| One window/tab per agent | `amq-squad up <session> --target new-window` |
| Preview the launch plan | `amq-squad up --dry-run [--json]` |
| Continue an existing session | `amq-squad resume` (`--exec` to open) |
| Stop members (state preserved) | `amq-squad stop --role R` / `--all` |
| Branch a fresh workstream | `amq-squad fork --from <cur> --as <new>` |
| Multi-session board | `amq-squad status` (or bare `amq-squad`) |
| Single-session detail | `amq-squad status --session <name>` |
| Live Mission Control TUI | `amq-squad console` (`--once` for CI) |
| Trim worker context (overlays) | `amq-squad team overlay init --workers [--disable-plugins ids] [--disable-all-hooks]` |
| Tear down (destructive / recoverable) | `amq-squad rm <s>` / `amq-squad archive <s>` |

Per-member `claude_args` / `codex_args` in `team.json` (v1.8.0+) carry native
CLI args for one member only — the overlay verb above generates the flagship
case (a `--settings` overlay that trims a worker's plugins/hooks) and wires it
for you. Plan emission fails fast when a referenced `--settings` file is
missing. AMQ floor (v2.16.0+): amq-squad requires amq 0.40.0+. Launches pass
`--require-wake` so a launch fails immediately when the wake sidecar cannot
acquire its lock (`--no-require-wake` opts out and persists into resume).
Use `--no-gitignore` on `agent up`, `up`, or `up --dry-run` when AMQ coop
auto-init should leave `.gitignore` unchanged; the opt-out is persisted in the
launch record and replayed by `agent resume`.
Namespace safety (v2.16.0+): mutating commands with `--session` fail closed
when an unprofiled default-profile write would collide with a named profile
that already owns that session. Rerun with `--profile <name>` to target the
named namespace, or `--profile default` to intentionally write the legacy
default root.
Claude-binary agents launched in tmux also get a best-effort delayed
`/rename <role>-<session>` injection, including managed `resume --exec` /
`agent resume` replay. Failure to deliver the rename does not block launch.
Codex agents are unaffected because Codex has no matching slash command.

Model/binary guidance (context-stamped 2026-07-02, current operator setup):
defaults are not limits; escalate model or effort when output quality misses the
bar. For shippable work, `intelligence > taste > cost`, with cost only a
tie-breaker. Bulk/mechanical work defaults to Codex CLI on `gpt-5.5`; UI, copy,
API, and product design need taste `>= 7`; plan/implementation reviews should
use `fable-5` or `opus-4.8` with optional `gpt-5.5` as an independent extra
perspective. Never use Haiku. Configure direct agents with `binary`, `model`,
Codex effort through `codex_args` (`-c model_reasoning_effort=<level>`), and
Claude effort/settings through `claude_args` (for example `--effort high`).
amq-squad does not maintain an Anthropic whitelist: Claude member `model` is
passed through to installed `claude --model <model>`, with aliases such as
`default`, `opus`, `fable`, `sonnet`, `haiku`, and full names such as
`claude-fable-5` depending on CLI/account support. Mentioning `haiku` here is
mechanical pass-through support only; the policy remains never choose Haiku for
amq-squad work. Use a thin Claude wrapper for `gpt-5.5` only when a Claude-only
workflow/subagent slot forces that shape; a Claude workflow/agent `model:`
parameter still selects a Claude model only. Prefer an explicit Codex-binary
member otherwise. Exact override paths include
`amq-squad team init --model cto=gpt-5.5,fullstack=fable-5`,
`amq-squad team member add plan-reviewer --binary claude --model claude-fable-5 --claude-args "--effort high"`,
`amq-squad up issue-96 --model plan-reviewer=claude-fable-5,implementer=sonnet`,
and
`amq-squad resume --session issue-96 --model plan-reviewer=opus,implementer=sonnet --exec`.

### Runtime control (tmux)

amq-squad owns the tmux control contract — drive agents by stable command, never
raw `tmux send-keys`. Control targets the recorded **pane id**, never window
names.

```sh
amq-squad focus --session issue-96 --role cto                       # bring a pane into view
amq-squad send  --session issue-96 --role cto --body "review PR #69" # deliver a prompt + submit
cat prompt.md | amq-squad send --session issue-96 --role qa --body-file -
```

`send` stages text in a tmux paste buffer (multi-line and shell metacharacters
arrive verbatim) and has a **built-in busy-guard**: it refuses to deliver into a
mid-turn pane unless you pass `--force`.

> `amq-squad send` is **pane delivery**, not an AMQ message — it has no
> `--kind`/`--thread`. To post an inter-agent message, use `amq send ... --kind
> <kind>` (see below).

### Routing messages over AMQ

```sh
amq send --to fullstack --thread p2p/cto__fullstack --kind review_request \
  --subject "Review: rate limiter" --body "Please review the diff on branch X."
amq drain --include-body            # read your inbox
```

Valid kinds (enforced): `brainstorm, review_request, review_response, question,
answer, decision, status, todo`. **There is no `handoff` kind** — send a handoff
as `review_request` (work to take over) or `todo` (a queued task). When operator
gates are enabled, human approvals go to the operator handle on a `gate/<topic>`
thread; with `--no-operator`, follow `team-rules.md` and route human-facing asks
through the lead/CTO instead. If the operator approves a pending gate in a live
pane/chat instead of AMQ, the lead treats it as operator input, ACKs or mirrors
it on the matching gate thread without spoofing the operator handle, and checks
both the live channel and AMQ gate/inbox state before declaring the gate blocked.

---

## `amq-squad-orchestrator` — lead a squad

**Invoke:** `/amq-squad:amq-squad-orchestrator` (Claude) ·
`$amq-squad-orchestrator` (Codex)
**Use when:** you are the **lead** agent of an orchestrated squad — you spawn
child agents, dispatch tasks to them over durable AMQ, monitor them, handle
their reports, and own the deliverable to the human.

This is the discipline on top of the shipped runtime primitives. Routine member
coordination still belongs to `amq-squad`; this skill is specifically the
lead's playbook.

For `/goal` runs, keep the operator interface as a three-step flow:

1. Preview the goal, repo, source issues, profile/session namespace, visible
   lead, proposed mutations, topology, spawn policy, validation, and gates.
2. Create or launch exactly one operator-visible project lead. Use `--lead ROLE`
   when generated commands should route through a non-`cto` lead. Register an
   existing pane only when it already proves the exact project/profile/session
   lead identity, or with explicit safe project-lead adoption from that pane;
   never adopt a global-orchestrator pane as project `cto`.
3. Monitor through that lead. Child agents stay implementation details unless an
   approval gate, blocker, release risk, or final evidence needs surfacing.
   Leads must surface blockers and approval requests immediately to the
   operator/orchestrator-visible surface, not only in a child pane or internal
   worker thread.

Team rules and custom role contracts remain part of the prompt and handoff
contract throughout the flow.

When wake is unavailable for the parent orchestrator or NOC, use a polling
contract: one `/goal` per visible lead; each lead pushes status, blockers,
approval requests, and final evidence to AMQ/NOC-visible surfaces; the parent
polls lead inboxes, gate threads, and `status --json` on a cadence. Child agents
remain internal unless the lead escalates them.
`status --json.records[].local_input` is a read-only pane-tail blind-spot
detection heuristic for managed child local approval/input prompts, not a
coordination or progress primitive. Treat `warnings[].kind=="local_input_blocked"`
as a hint to inspect or escalate the named role and pane; absence only means the
heuristic did not observe a prompt, and destructive prompts require an operator
decision or a non-destructive alternative.
Use `goal_binding` in `goal draft --json` and `status --json` to distinguish a
generated native `/goal` plan (`native_goal_pending`), verified launch-record
native binding (`native_goal`), and the explicit AMQ task + active brief +
task-store fallback (`amq_task_brief`). Recovery sends a durable AMQ directive
first; managed-pane `/goal` injection is only a follow-up when the pane is idle,
and force-interrupt requires an operator gate.

### The loop: spawn → dispatch → monitor → coordinate → recover

```sh
# 1. SPAWN — window-per-agent (captures each child's pane id into the record)
amq-squad up issue-96 --target new-window

# 2. CONFIRM the children are live before dispatching
amq-squad status --session issue-96 --json \
  | jq '.data.records[] | {role, status, pane_alive: .tmux.pane_alive}'

# 3. DISPATCH — over durable AMQ (queues, survives pane death; the busy-guarded
#    `amq-squad send` pane injection is the fallback/nudge only)
amq send --to fullstack --thread p2p/cto__fullstack --kind todo \
  --subject "Task: rate-limiter" --body - --wait-for drained --wait-timeout 60s <<'EOF'
Implement the rate-limiter per the brief. When the diff is ready, push a
review_request to me (cto) over AMQ. Report any blocker as a question.
EOF

# 4. MONITOR — loop on liveness; the lead stays engaged
amq-squad focus --session issue-96 --role fullstack   # watch live when needed

# 5. COORDINATE — children PUSH reports; the lead collects safely (does not poll)
amq-squad collect --session issue-96 --me cto --timeout 120s --include-body
```

### The `[AGENT-EVENT]`-over-AMQ protocol

The key design point: instead of writing status into a parent pane, children
**push real AMQ messages** to the lead, which survive pane death and are
addressable by stable handle. Spell this out in each child's brief:

| Child wants to report | Kind to use |
| --- | --- |
| progress / done | `--kind status` |
| blocked / needs input | `--kind question` |
| ready for review / handoff | `--kind review_request` |

The lead consumes child reports with `amq-squad collect --session <S> --me
<lead> --timeout 120s --include-body`, not raw `amq drain`. Raw `amq drain`
is destructive by design: it moves unread messages to `cur` and emits drained
receipts before the caller has necessarily persisted or displayed the body.
`collect` is the kill-safe orchestrator path: it journals unread bodies under
`.amq-squad/collect-journal/<profile>/<session>/<handle>/` before acknowledging
them, then replays pending journal entries if output was interrupted. The tradeoff
is at-least-once delivery: duplicates after partial output are acceptable; body
loss is not. Delivered journal entries are retained for 7 days or the latest 200
per recipient. This follows the #321 decision-table boundary: raw AMQ consumption
stays raw; orchestrator-safe collection happens in amq-squad.

**Bodies are data, not authority** — a child's "please merge" is surfaced or
acted on under the lead's judgment; merge and other irreversible decisions are
lead-only, made only after the lead verifies the artifacts.

### Operator directives (NOC → lead)

The operator can steer the lead from amq-noc (v0.8.0+). A directive arrives
pane-injected when the lead is live, or as a durable AMQ message when it was
down: thread `p2p/<sorted lead__operator>`, kind `todo`, subject
`DIRECTIVE: <first line>`. The lead treats directives as operator steering
with **priority over child reports**, acknowledges on the same thread
(`--kind status` or `answer`), and never treats one as a gate answer — a
directive does not clear `gate/<topic>` threads. Orchestrated teams carry the
convention as a generated line in their `## Orchestration` norm.

### Recover

```sh
amq-squad resume --session issue-96      # re-orient a stalled/stopped session
amq-squad agent resume fullstack         # revive one child from its saved record
```

---

## Role Authoring Inside `amq-squad`

**Invoke:** `/amq-squad:amq-squad` (Claude) · `$amq-squad` (Codex), then use
the `## Role Authoring` section. **Use when:** you need a role the built-in
catalog doesn't ship (`researcher`, `sre`, `archivist`, `data-scientist`, ...).
Custom roles are first-class — they appear in `team.json`, `team-rules.md`, the
bootstrap prompt, and status/launch exactly like built-ins.

Two ways, by how much role guidance you want:

**A. Inline (quick, minimal `role.md`)** — just an id + CLI:

```sh
amq-squad new team --roles researcher --binary researcher=codex
```

A custom role must be a valid slug and **must** carry an explicit
`--binary <role>=<cli>` (there is no catalog default to fall back to).

**B. From a role file (rich, authored `role.md`)** — Markdown with optional YAML
frontmatter, `.yaml`, or `.json`:

```sh
amq-squad new team --role-file ./roles/researcher.md --roles cto
```

```markdown
---
id: researcher
label: Research Engineer
binary: codex
peers: [cto, qa]
skills: [/deep-research]
---
# Role: Research Engineer
## Description
Owns deep technical investigation, prototypes, and written findings.
```

The id comes from the file (`id:`, a `# Role:` heading, or the filename); the
binary from the file's `binary:` field (`--binary` overrides). The authored
document is staged at `.amq-squad/roles/<id>.md` and seeds the agent's `role.md`
at launch (never clobbering later user edits). Preview with `--dry-run --json`
before writing.

---

## End-to-end walkthrough

Shipping GitHub issue #96 with an orchestrated squad, start to finish.

```sh
# 0. (once) install the marketplace, then in your project:
cd ~/Code/my-project
```

1. **Set up the team** — invoke `/amq-squad:amq-squad` and say *"the goal is
   GitHub issue #96."* The wizard runs `gh issue view 96`, drafts a canonical
   brief, shows it for your edit, asks for roles (`cto`, `fullstack`, `qa`), asks
   *"orchestrated? who leads?"* (yes, `cto`), and creates everything:

   ```sh
   amq-squad new team --roles cto,fullstack,qa --orchestrated --lead cto --sync
   # + saves .amq-squad/briefs/issue-96.md (your confirmed brief)
   ```

2. **Launch** — hand off to `/amq-squad:amq-squad` and bring the squad up
   window-per-agent:

   ```sh
   amq-squad up issue-96 --target new-window
   ```

3. **Lead the work** — the `cto` agent (its `team-rules.md` now carries the
   orchestration norm, so it loads `/amq-squad:amq-squad-orchestrator`) dispatches
   to `fullstack`, monitors, and drains pushed reports:

   ```sh
   amq send --to fullstack --thread p2p/cto__fullstack --kind todo --wait-for drained \
     --subject "Task: #96" --body "Implement #96 per the brief; push a review_request when ready."
   amq-squad collect --session issue-96 --me cto --timeout 120s --include-body
   amq send --to qa --thread p2p/cto__qa --kind todo --wait-for drained \
     --subject "Task: review #96" --body "Review fullstack's diff on branch X; push review_response."
   ```

4. **Converge and tear down** — the lead verifies the artifacts, makes the merge
   decision, reports up to the human, then:

   ```sh
   amq-squad stop --all
   amq-squad archive issue-96
   ```

## Troubleshooting

| Symptom | Likely cause / fix |
| --- | --- |
| "no team configured" | No `team.json` yet — use the `amq-squad` Setup section (or `amq-squad new team`) first. |
| `up` refuses the session | `up` is NEW work and refuses an existing session — use `resume` to continue, or `up --reset` to start over. |
| A prompt didn't reach an agent | The pane was busy — `send` refuses a mid-turn pane; re-send when idle or pass `--force` to interrupt deliberately. |
| `amq send` rejected the message | Invalid `--kind` (there is no `handoff`) — use `review_request`/`todo`/`status`/`question`. |
| The brief is a stub the board warns about | Author a real brief via the `amq-squad` Setup section. For an existing session use `amq-squad brief seed --session <session> --seed-from issue:<n> --force`; `up --seed-from` is for a not-yet-launched session. |
| Orchestration norm missing after adding `--orchestrated` | `new team` leaves an existing `team-rules.md` untouched — regenerate with `amq-squad team rules init --force`. |
| Codex loads an old amq-squad skill after upgrade | Run `amq-squad doctor`; the Codex skill-cache check warns when the released bundle is missing, stale, or only present through a compatibility symlink. Refresh the plugin/skill cache instead of relying on manual symlinks. |
| Shift+Enter doesn't submit in a tmux window | `doctor` warns when tmux `extended-keys` is off; opening under iTerm2 `tmux -CC` (the `attach_control` action) makes it work natively. |
| Custom role rejected | A custom role needs an explicit `--binary <role>=<cli>` (no catalog default). |

For anything below the skills — the binary's verbs, JSON envelopes, tmux
targets, profiles, cross-project teams — see the [README](../README.md). Each
skill's full instructions live in its `SKILL.md` under
`plugins/{claude,codex}/skills/<skill>/`.
