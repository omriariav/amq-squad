# GOAL — the best agent-teams control CLI

**Make amq-squad the best agent-teams control CLI in existence: perfect _visibility_
and perfect _control_ over mixed Claude+Codex teams coordinating through AMQ — with
the human as a first-class participant who can see everything and act on anything
from one place.**

## North star
- **Leverage the best of AMQ**: the control plane speaks AMQ natively. The human is a
  full two-way citizen of the bus — not just detected (read-only `user` mailbox) but
  able to reply, approve/deny, message, broadcast, and route into the same queue the
  agents use. Human-in-the-loop is real, not a side channel.
- **Perfect control of the team running**: from one screen (the NOC) you can see every
  team, focus/open any of them, and act on anything — approve a pending action, stop or
  resume a squad, jump to the exact agent.

## Principles (non-negotiable)
- **Read-only is the default.** Visibility never mutates. Focusing your view (jump/open)
  is NOT a mutation.
- **Mutation is deliberate + safe.** Every control action that changes squad state is
  confirm-gated and previews first. Launching is an explicit verb, never a side effect
  of "open/focus".
- **Focus-if-present, degrade-if-not.** "Open team" / "jump" focuses tmux windows that
  ALREADY exist; if not running, it SUGGESTS `up`/`resume` — it never auto-spawns.
- **Truth over summaries.** Never fabricate attention/state; freshness is always shown.
- **Real verification.** Interactive surfaces are verified through a real terminal/PTY
  (isolated tmux `-L`, never the default socket) — not just `Update`-level unit tests.
  (Lesson from the NOC nav bug that passed unit tests 4x while broken on screen.)

## Version ladder (Definition of Done)

### 2.0.0 — Foundation + Visibility  (in flight; DO NOT MERGE until reviewed)
Lifecycle redesign (up/stop/resume/rm/archive/status), read-only console, multi-root
NOC (board + tree + jump), `/v2`, docs. Done when: nav confirmed live by the human,
PR #52 reviewed + merged + tagged `v2.0.0`.

### 2.1.0 — Control (act from the TUI)
- Approve / deny a pending action; message an agent; broadcast to a squad — into AMQ.
- Stop / resume / restart a squad from the NOC (confirm-gated).
- **Open/focus team**: on a running squad, focus/raise its existing tmux windows
  (`tmux-session --resume`); if not running, suggest `up` (no auto-spawn).
- **Attention plumbing**: bootstrap/team-rules convention so agents EMIT `APPROVAL:` /
  `DONE:` → the ⏸ APPROVE / ✓ GOAL-REACHED needs-you tier lights up on real squads.

### 2.2.0 — Naming bridge + tmux-session
- Stamp each agent pane with a deterministic title `amq:<session>:<role>`; resolve jump
  by name FIRST → fixes the cpo·codex vs cto·codex ambiguity (two same-engine agents in
  one repo currently mis-resolve). Rotation-proof, no daemon.
- Opt-in `tmux-session` window-per-agent launcher backend (powers per-team open).

### 2.3.0 — Awareness + scale
- Alerts: bell / desktop notification when an agent needs you.
- Command palette: fuzzy jump/open to any agent or team in ~2 keystrokes.
- Flow graph; richer at-a-glance detail (current task / last action / open threads) so a
  team is legible WITHOUT opening it.

## Process
Each slice: build → adversarial verify → `make ci` → REAL terminal/PTY verification →
Codex DX-test the real artifact → fold back → commit → bump RC → log on PR #52.
Serialize tree-editing workflows. **Never merge** — as many 2.0 RCs as needed; the human
reviews. Flag interactive / iTerm2 `-CC` bits that need the human's hands-on.
