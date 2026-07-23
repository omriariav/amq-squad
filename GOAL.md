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

## Version ladder (current as of v2.23.1)

The old 2.1.0–2.3.0 roadmap mixed aspirations with release claims long after the
product had moved on. The ladder now separates shipped foundations from the next
acceptance boundary. Canonical release detail remains in `docs/vX.Y.Z-release-notes.md`.

### 2.0.0–2.10.x — Shipped: lifecycle and team composition
- Project-aware team/profile/session lifecycle, mutable rosters, native tasks,
  resume/stop/archive/remove, console/status visibility, and Claude+Codex launch
  composition became the stable CLI foundation.
- Deterministic tmux identities, focus/open behavior, and action objects made
  runtime state addressable without treating navigation as a mutation.

### 2.11.0–2.18.0 — Shipped: authority and operator safety
- The operator became an explicit non-runnable AMQ participant with typed gates,
  action verification, bounded self-operator rules, notification surfaces, and
  actor-relative implementation policy.
- Owned sends gained crash-visible durable delivery receipts. Canonical action
  objects and terminal/runtime capability checks made unavailable actions fail
  closed instead of degrading into best-effort writes.

### 2.19.0–2.23.1 — Shipped: prepared execution and exact identity
- Canonical project/profile/session/root resolution, atomic task lifecycle,
  namespace migration, durable launch/bootstrap records, and managed wait posture
  closed the major split-brain paths between CLI state and the AMQ bus.
- Prepared launches, staged admission, exact runtime identity, terminal recovery,
  and bound command evidence made launch and review outcomes reproducible.
- v2.23.1 is the current released baseline. Its exact claims and compatibility
  floor are recorded in `docs/v2.23.1-release-notes.md`.

### 2.24.0 — Current target: control, scale, and squad reuse
- Complete the human control loop: preview and explicitly confirm console replies,
  gate approvals/denials, arbitrary-agent messages, and squad broadcasts; every
  AMQ write must expose a stable message id and durable receipt.
- Harden delivery against AMQ 0.46 behavior, add deterministic isolated-worktree
  ownership, improve reusable squad/roster flows, and make model economics
  visible without weakening actor-relative policy.
- Definition of done: every implementation slice is reviewed at an exact commit,
  the full compatibility/CI matrix passes, and merge/tag/release remain separate
  verified operator-gated actions.

**Multi-root console feasibility (not a 2.24.0 implementation claim):** the console
currently owns one canonical project/profile/base-root context, and its action
confirmations deliberately re-resolve inside that boundary. A trustworthy
multi-root NOC needs an explicit neutral-root discovery model plus a per-row
project/profile/session identity carried through snapshot, preview, admission,
and receipt. That is feasible, but it is a separate authority-model slice rather
than a safe additive loop over roots, so this work keeps the single-root boundary.

## Process
Each slice: build → adversarial verify → `make ci` → REAL terminal/PTY verification →
Codex DX-test the real artifact → fold back → commit → bump RC → log on PR #52.
Serialize tree-editing workflows. **Never merge** — as many 2.0 RCs as needed; the human
reviews. Flag interactive / iTerm2 `-CC` bits that need the human's hands-on.
