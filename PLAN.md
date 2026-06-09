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
- Roadmap order is theme-grouped, not strictly sequenced; the active line is
  v1.5.1 → the #7 status-actions follow-up → (when there's demand) #76.
