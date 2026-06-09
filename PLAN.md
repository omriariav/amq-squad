# amq-squad roadmap

Living roadmap. The GitHub issue tracker is the source of truth; this file groups
the open issues by theme + status so the near-term path is legible at a glance.

## Release state

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

## Themes

### Runtime orchestration (the v1.5.x arc)

- **#76 — Agent orchestrator** *(roadmap; substrate shipped).* A lead agent
  spawns, drives, monitors child agents over the tmux contract; children push
  `[AGENT-EVENT]` envelopes back. Mostly a protocol + skill on top of the shipped
  primitives (`send`/`focus`/`--target new-window`/`PaneBusy`/pane-id
  addressing); new binary work is `spawn` / `emit-event` / `watch`. **Never in
  the NOC** — at most the NOC observes an orchestration. This is the headline
  forward item.
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
  from workstream. Largely realized by the 2.0 reshape — *audit & close/refile.*
- #27 — Unified team resume UX (fresh launch vs restore). *Likely addressed by
  the resume rework — verify.*
- #26 — Team rules template library for modern agent squads.
- #22 — Support a per-member target model in team config.
- #39 — `team init --roles` rejects custom names. ✅ custom roles shipped in
  v1.5.0 — *pending close.*
- #25 — Prevent launching duplicate live agents for the same role/handle/
  workstream. ✅ roster preflight + duplicate detection shipped — *pending close.*
- #19 — Rework Codex trust defaults for generated launches.
- #11 — Replace placeholder `role.md` stubs with actionable role guidance.

### AMQ integration & operator

- #53 — Support a virtual operator participant in squad teams. *Partially
  addressed by the schema-3 operator (`user` handle / `--operator`) — verify
  scope vs close.*
- #30 — Adopt new AMQ features (require-wake, inject-via, from-session); drop
  stale acks.
- #58 — Clarify the AMQ message-kind enum in the skill routing docs. *Largely
  addressed in the v1.5.0 skill update (#70) — verify & close.*

### Bugs

- #46 — `amq env` failures are opaque; the launch path leaks shell
  `AM_ROOT`/`AM_ME` identity.
- #45 — Exiting agent leaves an orphan `amq wake` process that blocks relaunch.
- #44 — `down` leaves an orphaned `amq wake` process and stale live presence.

## Notes

- Several issues above are implemented but still open (marked *pending close*);
  they're listed for an accurate picture and should be triaged closed.
- Roadmap order is theme-grouped, not strictly sequenced. v1.5.2 shipped the
  #79 liveness unification and the session-scope `status --json` actions
  (amq-noc#7 producer side). Remaining near-term: board/project-scope actions
  (deferred — needs per-session profile in the board envelope); then, when
  there's demand, #76 (the agent orchestrator).
