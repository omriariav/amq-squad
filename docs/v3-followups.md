# amq-squad 3.0: follow-ups after the release

Items deferred out of 3.0 by a recorded decision, each with the plan section
that records it (docs/v3-plan.md unless named otherwise). They outlive the
planning sessions that made them; nothing here is a release blocker.

## 3.1

**Direction for 3.1 (owner, 2026-10-30, in his words):** "we want to copy
claude agents team but with more binaries and models, and experience needs
to be seamless; the other main objective is able to work remotely via buzz
or grok bot." Measured against the first day of use, the seams are: a team
needs git and four commands before it exists (the documents squad and the
solo-lead-to-squad bridge below); the pi lead runs local models only (the
cloud entries below); the phone is one-way (agents' replies do not reach
Buzz; a Grok bot face does not exist). Each 3.1 brief names which seam it
closes.


- A documents squad (owner, 2026-10-30: "why is git strict if the role is
  non-coding? I can also trigger a team in a PM folder"): in 3.0 a team
  needs a git repository because the worktrees isolate seats and the commit
  step is the evidence of each seat's change; the work being documents
  changes nothing in that. Today a `git init` with no remote is enough: the
  commit step commits and does not push (push_url_missing is a note, not a
  refusal). 3.1: a squad mode for non-code folders that keeps a repository
  underneath for isolation and evidence without the owner setting it up or
  seeing it, with the seats' output presented as files and messages.

- Linux seats (platform decision 2026-10-29, plan 16.4; CI run
  40955238412). (a) The launch's proof socket binds and dials through
  descriptors on both ends (D1's pattern, the proof-side twin): on a Linux
  host with a project path over the sun_path bound the launch's proof fails
  (helper_dial_failed, no_proof) and no seat starts. (b) Identity on Linux:
  ext4 and tmpfs reuse an inode right after unlink and recreate, so a
  same-UID replacement of the routing seed, of the proof socket after the
  probe, or of an anchored directory passes the dev+inode check that exists
  to catch it; hold the descriptor and fstat it instead of re-stating the
  path wherever the check guards replacement, and where a path must be
  re-stated add statx birth time (stx_btime) beside dev and inode; the three
  sites are the seed, the proof socket after the probe, the anchored
  directory. (c) The cancel and close rows (the cancelled seat still runs;
  an unconfirmed close reported cancelled): process semantics on the
  runner, pid start stamp or signal timing; a Linux VM is the first step.
  (d) gatestore TestSquadIDIsCanonical, likely /tmp against /var in the
  fixture; a test fix that rides with the rest.

- Read-only runtime image for the embedded pi runtime (the hash-to-exec
  window, milliseconds, same-UID only). Owner decision 2026-10-25 on launch
  speed. Plan 4.1 and 16.4 (hardening).
- GitHub token custody: a dedicated amq-squad keychain item with a
  fine-grained PAT, or a GitHub App installation token; 3.0 uses the owner's
  gh CLI login through the pinned gh copy. Owner decision 2026-10-25. Plan
  R33 and 16.4.

## 3.0.x

- Upstream ask 20 (AMQ): join a fresh participant into a present binding.
  AMQ v0.116.2 refuses a fresh seat launched into a session whose binding
  is present (`binding_present_without_resumable_conversation`,
  internal/launch/reconcile.go:1358), and its on_live keep join creates
  only a seat missing from the binding's resources (liveseat.go:58-72), so
  a seat that ran and is gone cannot be replaced. 3.0.1 t16 launches the
  roster in one apply and replaces a gone seat by relaunching the whole
  roster (`amq-squad scheduler relaunch`); with the ask, the scheduler can
  replace one seat in place. The lead files the ask.
- Conditional (3.0.1 t16 ruling 3): pi seats park like the others, on the
  rule that the model memory is the local server's (ollama's keep_alive
  unloads an idle model; the seat process holds none). If the owner run
  measures GPU memory held by a parked pi seat after the keep-alive window,
  close-on-park for pi seats becomes a 3.0.x item.
- Speed of launches and communication (owner, 2026-10-30, the first
  dogfood squad: "we need to speed up the agent launches and comms, it's
  taking too much"). Measured on the day: a seat launch to its first turn
  is seconds, but the path from "task added" to "seat working" ran through
  the trust confirmation (the mayor's start cannot carry it, t8), the
  quarantine cycle (each owner config change costs a signed clear), the
  first-run screens of a Claude seat (t6), the Codex folder trust per
  worktree, and the missing wake that leaves a seat idle after its first
  turn (t9); and every owner-side step was a terminal and a phone round
  trip. The 3.1 measure: time from `task add` to the seat's first tool
  call, and from a seat's reply to the owner's phone, both recorded per
  run; the targets are set when the first clean run gives the baseline.

- The 3.0 Claude plugin is unreachable through the public marketplace: the
  marketplace source is the repository (omriariav/amq-squad) and Claude
  Code reads its default branch, main, which carries v2.31; the owner's
  own install needed the marketplace re-pointed at the v3 checkout. Found
  2026-10-30 at the first Claude seat start. One choice for the owner,
  three options: (1) main carries 3.0 by the owner's merge on his
  repository, outside the standing approval; recommended (reviewer), as
  the first 3.0.x action, because a public marketplace that resolves to v2
  while the release says 3.0 misleads every installer; (2) v3 becomes the
  default branch (main stays as the v2 line under a new name); (3) a
  marketplace entry naming the branch, the stopgap only if Claude Code's
  marketplace format takes a ref, else the local re-point is the stopgap.
  Either way: a release-check row that fails the next tag closed when the
  marketplace's resolved ref does not carry the released version (3.0's
  release check missed it).
- Blocker for the train, not hygiene (reviewer ruling 2026-10-30):
  internal/redact TestBlockIsLinearOnALongLine bounds a 1 MiB line at 2 s
  of wall clock; the shared macOS runner missed it once (PR #800's first
  run, green on the re-run), and with make ci strict and no bypass actors a
  wall-clock bound fails the train at random. Fix before brief 0's PR
  reaches the train: the linearity proof as a ratio measured in one
  process (a 1 MiB line against a 64 KiB line within a 64x factor), no
  absolute bound. Task t2 in the v31 store.
- Closed by 3.0.1 t18 (PR #810, branch v3-301-t18): the buzz dry-run
  bound, the test-hygiene entry from 2026-10-29 (spike log).
  TestOwnerScriptDryRun waited on the serve loop with polling deadlines
  (five of 20 s among eleven, six more in its rehearsal) and one ran out at
  random under load. Each wait now blocks on what the serve loop emits on
  the test relay, one 5 minute budget ends a hung run and names the step,
  and no duration fails the test. Evidence:
  docs/v3-spike/5-301-evidence/t18-buzz-dryrun-signal.md.

- The pi lead on a cloud model (owner, 2026-10-30: "can I start pi and use
  a codex / claude model in it?"): the registry builds pi entries for
  Ollama models only (internal/registry/build.go); Anthropic and OpenAI
  models are entries of the claude and codex runtimes. pi itself speaks the
  Anthropic and OpenAI APIs with an API key, and brief 5-amq-A already
  carries a seat's declared credential (ANTHROPIC_API_KEY) into the pi
  process. Add pi entries for anthropic/<model> and openai/<model> when the
  key is present (credential_needs on the entry, privacy class and cost tier
  set), probed like the Ollama ones, so `amq-squad lead --model
  anthropic/<model>` runs the lead on a cloud model.

- Solo lead and teams (owner's first solo session, 2026-10-30, pi session
  01a18b83 under ~/code/amq-test on ollama/qwen3.6:27b): asked to "review
  this repo with two agents", the solo lead proposed a two-seat team in
  prose, then answered "why not using a team?" with "I'm solo here, there
  are no other seats" and did the review alone as two perspectives. The
  solo role prompt should say what the lead can and cannot start, and the
  lead should hand the owner the path that does start a team (team propose
  and apply with the seats it proposed, then the launch; or the mayor),
  instead of imitating one. Candidate for 3.1: the solo lead proposes a
  squad.yaml draft (`team propose --from`) so one owner confirmation turns
  the conversation into a squad ("the mayor proposes, the owner starts").
- Solo lead host facts: the lead guessed a repository path (/repos/...)
  before asking; the role prompt should carry where the owner's
  repositories live (the fleet grant's repository_paths, or ~/code) and
  the rule to ask for a path it cannot find.
- Local model latency: on ollama/qwen3.6:27b a turn with a long tool result
  took about two minutes; the solo lead should show a working indicator
  and the registry row should carry a measured turn time so the owner can
  pick a model knowingly.

- Solo lead first turn (owner observation 2026-10-30, the first human run
  of `amq-squad lead`): the lead's role instruction takes effect only after
  the owner's first prompt; nothing is said at start. The solo lead should
  open with its own first turn (who it is, which model, what it can do
  here) before the owner types, or the instruction should be applied at
  session start rather than on the first message.

- Install and self-update write to a temp name and rename, never a copy
  over the existing file: the macOS kernel kills a binary copied over an
  existing signed executable (the cached code-signature check, signal 9);
  a fresh inode runs. Seen installing 3.0.0 on 2026-10-30. The README's
  install step says so. Reviewer ruling 2026-10-30.

- The compound release store refuses a session root whose files carry a
  group other than the owner's primary gid: a scratch fleet under
  /private/tmp (macOS gives files there the wheel group) failed `release
  prepare` with "immutable temp identity invalid" on 2026-10-30. Accept the
  owner's uid with either the owner's primary gid or the gid the directory
  hands out (setgid-style inheritance is normal); the refusal names the
  directory and the gid it saw; a doctor row pre-checks the fleet root's
  group. Reviewer ruling 2026-10-30; owner-run.md 0.3.

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
  races); internal/seatlaunch TestPiSeatPlansThroughTheUpstreamAdapter under
  host load (AMQ's tmux detection has a 5 s deadline, backend_common.go:16;
  a loaded host misses it and Prepare says launcher_not_available; seen in
  3.0.1 t16, see its execution note); the internal/cli hung-guard row was fixed on v3-5-hygiene2
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
- AMQ's startup hold misses every amq-squad seat (3.0.1 t9, a stated
  limit). coop exec decides its Startup marker from the command binary
  (internal/cli/coop_exec_trust.go:74-75 and coop_exec_unix.go:994 at AMQ
  v0.116.2), and a seat's command binary is the seat-exec wrapper, so no
  seat gets the hold, and a wake doorbell can reach a folder-trust, update
  or hook-review screen. Claude's first-run screens close with t6 (the
  seat's config dir born complete; claudeseat.StartupScreensClosed turns a
  Claude seat's wake on only then). For AMQ's hold under a wrapper, one of
  two: seat-exec names the inner binary to AMQ, or AMQ honours a binary
  the launch declares (an upstream ask). Until then a Codex seat's wake
  requires the Codex config's trust entry for its worktree, which rules out
  the trust screen only.
- Claude seat config digest (3.0.1 t6, reviewer ruling 2026-10-30): if
  the owner-run live row (docs/v3-spike/5-claudeseat-execution.md) shows
  that Claude Code 2.1.314 never creates a `projects` entry for the
  worktree's canonical repository root, tighten the `.claude.json` digest
  from "keys carrying trust or a permission input" to the bare `projects`
  key set, so any added entry moves it. If the entry does appear, record
  its keys there and keep the current digest.
- The Claude seat config checks are facts about the pinned Claude Code
  binary (3.0.1 t6): the bookkeeping sets pI and kI and the walk of their
  keys against the binary's permission readers. Today a re-pin's step is to
  re-extract internal/claudeseat/testdata/claude-*-bookkeeping.js and re-walk
  by hand (the execution note's method); make the re-walk a check, so that
  doctor's pin row refuses a pin whose sets differ from the code's.
- Claude seats and external CLAUDE.md includes (3.0.1 t6, fifth commit):
  when a CLAUDE.md the seat loads at start imports a file outside its
  worktree, Claude Code 2.1.314 opens its external-includes dialog unless
  the project entry approves the includes. claudeseat never seeds that
  approval, so such a seat's wake stays off and seat-exec refuses it
  (claude_external_includes_pending). Decide how approval is granted: an
  owner-signed per-worktree approval written into the seat's config at
  launch, or a host check that names the imports for the owner to move
  inside the worktree.

## Acceptance rows that wait on the owner session (not deferred, open)

See plan 16.4's table: 3-2 acceptance 4 (the real S6 merge), milestone B's
Claude live rows and the Codex seat row, 4-1 acceptance 10 (the AMQ release
carrying PR #1188), 4-5 acceptance 4 and 3-1 acceptance 9 (the Buzz session,
with the signed seat clear as its third item), 4-2 acceptance 3.
