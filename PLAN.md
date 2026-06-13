# amq-squad roadmap

Living roadmap. The GitHub issue tracker is the source of truth; this file groups
the open issues by theme + status so the near-term path is legible at a glance.

## Release state

- **v2.0.0 — in progress (goal-first dynamic teams).** A binary-neutral lead
  composes its team from a goal (Codex can lead, not just be led); manual setup
  stays the floor; spectrum manual → seeded (default) → autonomous (2.1). Full
  plan + executable brief: `docs/v2.0.0.md`, `docs/v2.0.0-goal.md`. Proven
  additively in the 1.x line first (Phase 0: A mutable roster → B native task
  model → C orchestrate skill → D dogfood+eval gate) before the Phase 1 breaking
  cut + `/v2`.
  - **Slice A — `team member add/rm`** (runtime roster mutation): add/remove a
    roster member at runtime via atomic, file-locked (`internal/flock`),
    re-validated writes through `team.WriteProfile`, so a lead grows/shrinks its
    team mid-session and the change persists for resume. Additive; no
    removed/renamed verbs, no schema change.
- **v1.5.0 — shipped.** tmux runtime contract (persisted pane/window ids,
  `pane_alive`, per-agent `actions[]`, `focus`/`open`/`send` verbs, `--target
  new-window`), custom roles, Claude + Codex plugin marketplaces.
- **v1.5.1 — shipped.** `capabilities.runtime_actions` (#73), `send` busy-pane
  idle-check (#74 / #68), structured action metadata
  `label`/`scope`/`mutates`/`needs_confirmation`/`reason` (#75).
- **v1.5.2 — shipped.**
  - **#79 — NOC runtime JSON contract gaps.** Unified `status`/`resume` liveness
    behind one shared classifier so `resume --json` can't contradict `status
    --json` (incl. a zombie-heartbeat guard that also fixes the latent #44
    "stale live presence" bug); a `liveness` block on `resume.plan[]`; honest
    `resume --help`; regression coverage. (Ask #1, `runtime_actions`, shipped in
    v1.5.1.)
  - **Session-scope action catalog** on single-session `status --json`
    (`data.actions`: status / resume_preview / resume_current_window /
    resume_new_session / stop) — closes amq-noc#7's producer side. Board/
    project-scope actions deferred (the board envelope carries no per-session
    profile).
- **v1.5.3 — shipped.**
  - **#86 — prompt delivery dropped the Enter.** `send` now submits robustly in
    plain tmux: settle, Enter, verify the input region changed, retry, and a
    clear error instead of silently leaving text staged.
  - **#85 — iTerm2 `tmux -CC`.** An `attach_control` session action
    (`tmux -CC attach -t <session>`) when the workstream has a live tmux session;
    opening under `-CC` also makes Shift+Enter work natively.
  - **shift+enter doctor hint** — `doctor` warns (informational) when tmux
    `extended-keys` is off, with the opt-in fix; amq-squad never mutates the
    user's tmux server.
  - **#87** — plain `resume` vs `--json`: shipped a consistency regression test
    (the same-invocation paths share one plan). NOTE: #87 was later **reopened**
    with a deterministic repro and the real cause fixed in v1.5.4.
  - **#81** — post-release peer-notification norm in the default team-rules.
- **v1.5.4 — shipped.**
  - **#87 (reopened)** — `status`/`doctor` called live launch records stale
    while `resume` saw them live, with `ps` proving the PIDs alive. Root cause:
    `ProcessMatch` forked `ps`, which returns `EAGAIN` under fork pressure (a
    large process table), demoting a live agent/wake to stale on some surfaces
    but not others. Fix: read process command lines **fork-free** (darwin
    `KERN_PROCARGS2` / linux `/proc`, `ps -ww` fallback) and `EPERM`⇒alive,
    unified into one shared `internal/procinfo` probe consumed by BOTH
    `internal/cli` and `internal/state` (the status board + NOC snapshots), so
    every surface reads liveness identically.
- **v1.6.0 — shipped. Closes the Sagi-spawn gap.**
  - **#95 — Adopt externally-launched panes.** Agents launched outside
    amq-squad's tmux backend (raw `tmux new-window`, Sagi-style) now have their
    live pane adopted by PID lineage (a fork-free `procinfo.ChildrenIndex`
    snapshot), so `focus`/`send`/`attach_control` and `pane_alive` work for them.
    A PID-lineage match is definitive and bypasses the cwd/engine heuristics, but
    only for a verified live agent pid (guards stale/reused pids).
  - **#76 — Agent-orchestrator skill** in both marketplaces: a lead-agent
    playbook (the Sagi `spawn.md` equivalent) over the shipped primitives —
    spawn / dispatch (busy-guarded `send`) / monitor (`status --json`) / the
    `[AGENT-EVENT]`-over-AMQ reporting protocol / recover. Plus the corrected
    `send` busy-guard note in the `amq-squad` skill.
- **v1.9.0 — shipped. Context-budget + operator-steering release.**
  - **`team overlay` primitive (#111 follow-up).** `amq-squad team overlay
    init (--role R | --workers)` generates `.amq-squad/overlays/
    <role>.claude.json` and wires the member's `claude_args` to load it via
    `--settings` — one command to trim a worker's plugin/hook surface in a
    same-cwd squad. No-clobber + idempotent + `--force`-gated replacement;
    `--workers` excludes the orchestration lead; plan emission (up / resume /
    tmux launch) fails fast when a referenced `--settings` file is missing.
    (PR #120; skills wizard beat + docs in PR #122, both marketplaces.)
  - **#117 — operator DIRECTIVE convention.** The squad-side contract for
    amq-noc v0.8.0's direct-lead flow: an "Operator directives" section in the
    `amq-squad-orchestrator` skill (arrival shapes, priority over child
    reports, ack on the same p2p thread, never clears `gate/<topic>`), plus a
    generated line in the orchestrated `## Orchestration` team-rules norm
    naming the lead handle (the #81/#101 generated-norm pattern). (PR #121)
  - **Triage:** closed #46 (amq env identity strip + stderr surfacing both
    shipped earlier) and #11 (role.md stubs already actionable, TODO text
    survives only as the upgrade-path test fixture).
- **v1.8.0 — shipped. The dogfooding release: every item came out of the first
  real-world orchestrated-squad run (pm-copilot, 2026-06-10).**
  - **#109 — stop → rm refused for the 90s presence-freshness window.** A fresh
    presence write with `status:"offline"` is the terminal act of a clean stop,
    not a zombie writer: it no longer counts as a mailbox touch, so the
    post-stop state classifies stale immediately (same as after the window) and
    the documented `stop` → `rm` sequence works back-to-back. rm's refusal now
    suggests `stop` (not the deprecated `down` alias) and names the freshness
    window + time remaining when dead-mailbox-live agents hold the gate.
    (PR #112)
  - **#111 — per-member `claude_args`/`codex_args` overlay in team.json.**
    Same-cwd squads can trim each worker's plugin/hook surface via a Claude
    `--settings` overlay without touching the lead: member args append after
    team-level `binary_args` (member wins by position), must match the
    member's binary (validated), ride the existing `--claude-args`/`--codex-args`
    plumbing (persisted in launch records, replayed on resume), are
    trust-checked per member at plan time, and surface in plan JSON +
    `--dry-run`. Skills in both marketplaces document the pattern. (PR #113)
  - **#110 — amq-team-setup wizard doc gaps** from the same run, both mirrors:
    `--binary` one-comma-list semantics, `--session` in the step-5 create
    examples (+ the dir-name default warning), a launch-name consistency
    warning in the handoff (a `--session` override at launch boots a new
    workstream with a stub brief), role-file staging normalization, and the
    show-role-bodies-before-create gate. (PR #114)
  - **#30 item 1 — `coop exec --require-wake` adoption.** Launches fail at the
    door when the wake sidecar cannot acquire its lock, version-gated on amq
    0.34.1+ (empty/unparseable versions omit the flag), with a
    `--no-require-wake` escape hatch that persists in the launch record so
    resume reproduces it. The prevention half of the orphan-wake class.
    (PR #115; #30 items 2–3 remain open)
  - **Triage sweep:** closed shipped-but-open issues #22 (per-member model),
    #19 (codex trust profiles), #53 (virtual operator), #27 (resume UX), #44 +
    #45 (orphan wake detection/cleanup), and the #31 lifecycle epic.
- **v1.7.0 — shipped. Closes #101 (one-setup orchestrated squad).**
  - **#101 — `amq-team-setup` wizard.** A wizard-style 5-step flow in both
    marketplaces (Claude + Codex): (A) capture a goal from ANY source — inline
    prompt, local `.md`, GitHub issue or PR (`gh`), Jira key (Atlassian MCP /
    `jira` CLI), or doc URL (Confluence / fetch) — fetched agent-side so core
    stays tracker-neutral; (B) normalize into a canonical per-session brief
    (`references/briefs-template.md`: Goal / Source / Scope / Out of scope /
    Acceptance), drafted then confirmed (a raw ticket is not a brief); (C) an
    orchestration opt-in driven by a STRUCTURED CLI primitive — `team.json`
    gains `orchestrated`/`lead`, `amq-squad new team --orchestrated [--lead
    ROLE]` records the lead and injects the orchestration reporting norm into
    the generated `team-rules.md` (mirrors the #81 norm pattern, generated +
    tested, never pasted prose), default off, exactly one lead, never the NOC.
    Shipped as PR #103 (CLI primitive) + PR #104 (wizard skill).

## Themes

### Runtime orchestration (the v1.5.x arc)

- **#101 — `amq-team-setup` wizard: goal→brief + orchestration opt-in**
  ✅ **shipped in v1.7.0** (PR #103 CLI primitive + #104 wizard skill). A
  wizard-style flow that captures a goal from any source (Jira / GitHub issue or
  PR / `.md` / URL / inline prompt) and normalizes it into a canonical brief,
  then optionally wires the squad for orchestration via a structured `new team
  --orchestrated --lead <role>` primitive (injects the team-rules reporting norm;
  never pasted prose). Tracker-neutral core (the skill fetches), default off,
  Claude + Codex, **never in the NOC**. Makes a one-setup orchestrated squad —
  the amq-squad analog of Sagi's always-on protocol.
- **#76 — Agent orchestrator.** ✅ shipped in v1.6.0: the `amq-squad-orchestrator`
  skill (lead spawns/dispatches/monitors child agents over the tmux contract; the
  `[AGENT-EVENT]`-over-AMQ reporting protocol) plus #95 external-pane adoption. The
  protocol + skill rode on the shipped primitives (`send`/`focus`/`--target
  new-window`/`PaneBusy`/pane-id); no new binary verbs were needed.
- #79 — NOC runtime JSON contract gaps in v1.5.0. ✅ shipped in v1.5.2 (shared
  liveness classifier + `resume.plan[].liveness` + session-scope `status --json`
  actions) — *pending close.*
- #61 — Expose first-class tmux orchestration metadata for NOC clients.
  ✅ shipped in v1.5.0 — *pending close.*
- #62 — Make tmux prompt delivery deterministic for launched agents.
  ✅ shipped in v1.5.0 — *pending close.*
- #47 — Batch execute mode for resume (`up --restore`).
  ✅ `resume --exec` shipped in v1.2.0 — *pending close.*

### Team lifecycle & DX

- #31 — *(epic, breaking)* Unify team lifecycle verbs; separate team identity
  from workstream. ✅ realized by the 2.0 reshape — closed in the v1.8.0 triage.
- #27 — Unified team resume UX (fresh launch vs restore). ✅ shipped via the
  resume planner — closed in the v1.8.0 triage.
- #26 — Team rules template library for modern agent squads.
- #22 — Support a per-member target model in team config. ✅ shipped
  (`Member.Model` + `--model role=model`) — closed in the v1.8.0 triage.
- #39 — `team init --roles` rejects custom names. ✅ custom roles shipped in
  v1.5.0 — *pending close.*
- #25 — Prevent launching duplicate live agents for the same role/handle/
  workstream. ✅ roster preflight + duplicate detection shipped — *pending close.*
- #19 — Rework Codex trust defaults for generated launches. ✅ shipped
  (sandboxed-by-default trust profiles) — closed in the v1.8.0 triage.
- #11 — Replace placeholder `role.md` stubs with actionable role guidance.
  ✅ closed in the v1.9.0 triage (already shipped; pinned by tests).

### AMQ integration & operator

- #53 — Support a virtual operator participant in squad teams. ✅ shipped as the
  schema-3 operator — closed in the v1.8.0 triage.
- #30 — Adopt new AMQ features; drop stale acks. Item 1 (`--require-wake`) ✅
  shipped in v1.8.0; items 2–3 (`wake --inject-via`, from-session) remain.
- #58 — Clarify the AMQ message-kind enum in the skill routing docs. *Largely
  addressed in the v1.5.0 skill update (#70) — verify & close.*

### Bugs

- #46 — `amq env` failures are opaque; identity leak. ✅ closed in the v1.9.0
  triage (envWithoutAMQIdentity at both resolution and exec; stderr folded
  into errors).
- #45 — Exiting agent leaves an orphan `amq wake` that blocks relaunch.
  ✅ closed in the v1.8.0 triage (v1.5.x preflight auto-clean + actionable
  blocker; prevention shipped as `--require-wake` in v1.8.0).
- #44 — `down` leaves an orphaned wake and stale live presence. ✅ closed in
  the v1.8.0 triage (v1.5.2 classifier + v1.5.4 probe + stop-side reaping).

## Notes

- Several issues above are implemented but still open (marked *pending close*);
  they're listed for an accurate picture and should be triaged closed.
- Roadmap order is theme-grouped, not strictly sequenced. v1.5.2 shipped the
  #79 liveness unification and the session-scope `status --json` actions
  (amq-noc#7 producer side). Remaining near-term: board/project-scope actions
  (deferred — needs per-session profile in the board envelope); then, when
  there's demand, #76 (the agent orchestrator).
