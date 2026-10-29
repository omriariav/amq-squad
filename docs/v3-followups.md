# amq-squad 3.0: follow-ups after the release

Items deferred out of 3.0 by a recorded decision, each with the plan section
that records it (docs/v3-plan.md unless named otherwise). They outlive the
planning sessions that made them; nothing here is a release blocker.

## 3.1

- Read-only runtime image for the embedded pi runtime (the hash-to-exec
  window, milliseconds, same-UID only). Owner decision 2026-10-25 on launch
  speed. Plan 4.1 and 16.4 (hardening).
- GitHub token custody: a dedicated amq-squad keychain item with a
  fine-grained PAT, or a GitHub App installation token; 3.0 uses the owner's
  gh CLI login through the pinned gh copy. Owner decision 2026-10-25. Plan
  R33 and 16.4.

## 3.0.x

- `amq-squad seat clear --rehearse` (or a script): build the quarantine
  fixture of the signed-clear live row (a scratch repository with a seat
  worktree, the staged launch record, the worktree base record, a grant
  naming a local bare origin, a task done by the seat, the owner config
  change) so the owner-run third item can run without hand-written core
  records. Owner session 2026-10-29; owner-run.md.

- Status liveness by launch-record identity, not bare signal-0: `status.Collect`
  (internal/status/collect.go:134) marks a seat `live` when its recorded
  `AgentPID` answers signal-0, while `classifyLaunchRuntimeIdentity`
  (internal/cli/launch.go:90) also checks the recorded binary or launcher and
  that the process was not born after the record. Failure: the lead dies
  without a `StoppedAt` and its pid is reused, so the lead's row stays `live`,
  the Buzz attachment's presence stays "present", and the owner's messages
  route to a dead lead. The fix moves the identity check where status can
  call it (the matcher lives in internal/cli) and gives Collect the probes
  as seams; the status tests that stub `PIDAlive` need the new seams. Found
  in 5-ci-D (D2, the Linux pid-42 fixture); deferred by diff size.
- Scheduler async close: no production implementer after the stand-in's removal (5-amq-C); CloseUnconfirmed's delegation always confirms; removal of AsyncCloser, the helper, the delegation, the awaitGone branches, state and rendering, and five tests, with holdForExit's exits release kept.
- Orphaned-verifier adoption: the exits queue entry for a running commit step
  records the verifier child's pgid and start time; the adopting scheduler
  kills that group when the start time matches and re-runs the verifier once
  on the same sealed snapshot (interrupted_reruns=1 in the sealed bytes); a
  second interruption defers to the lead as today. Spike log, phase 5
  hardening list; plan 6.1.
- Stale verifier waiting-slot rows: a waiting row left by a crash persists
  until the next step; status marks a row older than the wait bound as stale.
  Spike log, test-hygiene list.
- Test flakes still open after the hygiene branches: internal/seatlaunch
  TestProofConnectionsAreBounded and
  TestAmbiguousRequestIsAbandonedOnlyWhenNoPaneExists (TempDir cleanup
  races); the internal/cli hung-guard row was fixed on v3-5-hygiene2
  (merged c15ac65b). Keep the failing full-run output before any fix. Spike
  log, test-hygiene list.
- `amq-squad uninstall --json`: the verb has no JSON envelope. Plan 16.4
  (uninstall).
- YAML redaction trade-offs recorded in internal/redact's package doc: a bare
  multi-word value under a credential name is skipped by the one-line rule
  (the shape rules still run inside it) and `${VAR}` is skipped by the
  alphanumeric test; the per-line prose-scan narrowings (null and booleans,
  masked values, call expressions without an 8-plus alphanumeric run, the
  key `key`). Plan 6.1; briefs ruling 18.
- The Linux CI job for the Claude hook script (claude-hooks-linux on
  ubuntu-latest under dash) has not had a real run; it runs on the first push.
  Plan 16.4 (release prep).
- The amq-noc 1.0 command center lands by PR on amq-noc main on the owner's
  go, after its fixture refresh for lead_handle and live. Plan 4.7 and 16.4,
  owner session item 6.
- MIGRATION.md keeps its v2 sections; the "Moving to 3.0" section points at
  docs/squad-yaml.md and `amq-squad migrate`; a fuller v2-to-3.0 guide can
  follow once the first real migration runs. Plan 16.4 (migrate).
- 3.1, only if a pi lead ever takes phone approvals: ask 8 part 2 (PR #1254,
  the event id through the pi bridge). No 3.0 path takes a single tool
  approval through the pi bridge; typed gates go over Buzz with the owner's
  signature. Briefs ruling 20 point 5.
- The core's own body-key connection (gate question post, the per-answer
  read by id and history, the re-ask) goes when amq-remote offers a
  read-by-id and post surface on serve's connection. Ruling 20 point 1; plan
  R33.
- `/status` from the phone is now amq-remote's own command (request state);
  the 4-5 activity line (bucket counts, open gates) no longer reaches the
  phone. A plain-text keyword, or an upstream hook for a target-defined
  status, can bring it back. 5-amq-B.
- A hung verification is an `attention` line in `remote attach`'s output,
  not a row in the status document's attention queue. 5-amq-B.

## Acceptance rows that wait on the owner session (not deferred, open)

See plan 16.4's table: 3-2 acceptance 4 (the real S6 merge), milestone B's
Claude live rows and the Codex seat row, 4-1 acceptance 10 (the AMQ release
carrying PR #1188), 4-5 acceptance 4 and 3-1 acceptance 9 (the Buzz session,
with the signed seat clear as its third item), 4-2 acceptance 3.
