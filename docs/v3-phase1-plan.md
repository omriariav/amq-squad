# amq-squad 3.0, phase 1 plan

Date: 2026-10-20. Status: proposed, awaiting owner approval. Planning only;
no code was written for this document. Companion to `docs/v3-plan.md`
(revision 2.1). This document plans against the decision log in section 15
and the owner answers; it does not reopen them.

Prepared by the Fable lead with three read-only research passes (keep/delete
manifest, gate transport and launch API evidence, host baseline). Their raw
reports are listed in the appendix.

## 1. Facts verified during planning

These facts bind the spike. Each one was read in the AMQ v0.98.0 clone, the
v2.31 tree, or on this Mac.

Launch API (AMQ v0.98.0, contract 0.61.1):

- Claude admits `--model` and `--effort` as committed arguments today
  (`internal/launch/adapter_claude.go:124-136`). Upstream ask 4 is a request
  to document them as stable forms, not a grammar change.
- Codex admits `--model`, `--sandbox read-only|workspace-write`,
  `--ask-for-approval untrusted|on-request`, and `-c model_reasoning_effort=`
  (`adapter_codex.go:162-187`).
- The env overlay allowlist is exactly COLORTERM, LANG, LC_ALL, NO_COLOR,
  TERM (`adapter_env.go:14-24`). Seat environment can only come from the
  wrapper, as plan 4.3 says.
- The exec line is `cd <cwd> && env K=V... amq coop exec --root R --me H
  <wrapper> -- <wrapper args> <provider> <provider args>` (`tmux.go:756`,
  `commands.go:113,160`). Wrapper args are per participant. The seat gets its
  handle from `AM_ME`, so the wrapper can take `--seat` or read `AM_ME`.
- The wrapper must be an absolute path outside the project root
  (`prepare.go:1007`); the installed `amq-squad` binary qualifies.
- Codex identity capture is pinned to codex-cli 0.147.0 exactly
  (`adapter_codex.go:13`, unchanged since AMQ v0.67.0). Prepare does not
  check it. Apply refuses a fresh Codex seat on any other version with
  `action_required` and reason `capture_version_unsupported`
  (`reconcile.go:1032,1110`). Resume of an already captured conversation
  still works.
- A pi session under `amq coop exec` is reachable as an amq-remote `pi`
  target without the launch API, with Inspect and Submit only, and approvals
  only once the bridge is revision 4.

Gate answers (amq-remote v0.98.0):

- The only owner key amq-remote carries end to end is the owner's nostr key,
  which signs BIP-340 (secp256k1 Schnorr) over NIP-01 event ids.
- Only the Claude PermissionRequest hook path receives the owner's raw signed
  event (`internal/remote/claude/approve.go:86-94`). Codex receives
  `{"decision": option}` and pi an unsigned answer file; amq-remote decides
  for them.
- `amq-owner-sign` signs only grant and policy shapes; it cannot sign a gate
  tuple. The link passkey signs task submits only and never tool approvals.
- amq-squad `operatorauth` verifies Ed25519 only (`signer.go:13`,
  `trust.go:54`, `envelope.go:286`).

Consequence: spike exit criterion 1 cannot pass as written. See section 3,
task S1, for the smallest change.

Host (this Mac, 2026-10-20):

| Item | Value |
| --- | --- |
| Hardware | Apple M5 Max, 64 GB, 1.5 TiB free |
| amq | 0.77.3 installed; v0.98.0 needed; amq-remote and amq-owner-sign absent |
| codex-cli | 0.154.0 (AMQ pin is 0.147.0) |
| claude | 2.1.304, Max subscription, no ANTHROPIC_API_KEY set |
| OPENAI_API_KEY | unset |
| pi, cursor-agent | absent; `agent` on PATH is Grok CLI 1.0.25 |
| tmux, gh, go, node | 3.7c, 2.100.0, 1.27.1, 22.21.1 |
| Ollama | 0.34.4; qwen2.5:32b, gemma3:4b, llama3.2-vision installed; no qwen3.6 |
| Sleep | system sleep 1 minute, held off only by running caffeinate processes |
| GitHub | one account, no bot user, no App; amq-squad is the only repo with a ruleset |
| Codex config | model gpt-6-astra, effort medium, `notify` already set to another client |

v2 tree: 110,929 non-test Go lines under `internal/`, 74,538 of them in
`internal/cli`. `internal/console` (3,626) and `internal/tmuxharness` (485)
have no importers at all.

## 2. Proposed amendments to docs/v3-plan.md

Each is the smallest change that makes a decision buildable. None reopens
the decision log.

1. 16.7 criterion 1 and R32: replace "an operatorauth envelope signed by an
   owner key" with "an owner-signed BIP-340 answer over the gate id, action
   class, and target digest, carried by amq-remote, verified by the Go core,
   and wrapped as evidence in an operatorauth envelope". The owner key is the
   owner's Buzz nostr key, which already pins the share. Phase 3 adds a
   `bip340` evidence scheme to operatorauth beside Ed25519.
2. 16.1: `reviewrebind` is the merge-train rebind proof (`rebind.go:86`
   computes tree and patch-id proofs so a rebase that keeps the tree keeps
   the review). It stays at about 700 lines for R34. It is not a `squad.yaml`
   concern.
3. 4.5 and 16.6: the v2 task store has leases (`task/task.go:151`) but no
   pools. Pools are new phase 2b work, about 300 to 500 lines.
4. 11 ask 4: reword to "advertise `--model` and `--effort` as stable Claude
   argument forms in docs/launch-api.md". Add ask 8: forward the raw
   owner-signed nostr event through the attach binding, with a gate
   interaction kind (design C in section 3). Add ask 9: report
   `capture_version_unsupported` at Prepare, and treat the Codex capture
   version as a floor, not an exact pin. Filed 2026-10-20 as issue #1107
   with PR #1108. Upstream main is at v0.102.0; this plan was verified
   against v0.98.0.
5. 13: split phase 2a into 2a-1 (whole files and packages, about 55k lines)
   and 2a-2 (in-file cuts, about 22k lines). 2b, 3, and 4 start after 2a-1.
6. 13: before 2a-1, one commit changes the module path from `/v2` to `/v3`.
   Go requires it for a 3.0 tag.
7. 7.3, R34, and 16.7 criterion 2 (owner decision, 2026-10-20): amq-squad
   does not create or hold a GitHub identity. The developer owns their
   identities. GitHub does not count a review by the PR author, so the
   "one review" rule is not required; the owner-signed typed gate is the
   review, and branch protection requires CI. A developer who wants the
   GitHub review rule sets up a second identity themselves; the docs say
   how.
8. R46, 13, and 16.7 criterion 5 (owner decision, 2026-10-20): the phase 1
   load test is 2 squads, 6 seats, 25 tasks, 2 repositories. The 12-seat
   caps in R47 are extrapolated from it and labeled so in the registry
   until a full run before release (phase 5).

## 3. Phase 1 spike plan

Goal of the spike: produce the five pass or fail results in 16.7, the load
test numbers in R46, and the manifest in 16.1, in 2 to 3 days of wall time.
The spike uses the installed v2.31 squad tooling and does not build from the
3.0 tree.

Tier rules from section 6 apply to every seat. Spike tasks touch more than
three files or have no mechanical verifier, so implementation seats sit at
the plan tier and reviewers at the advisor tier of the other family.

### S0. Prerequisites (day 0, owner and lead, about 2 hours)

| Item | Who | Check |
| --- | --- | --- |
| Upgrade amq to 0.98.0 through the channel that installed 0.77.3 | owner | `amq --version`, `amq-remote --version`, `amq-owner-sign --version` all print |
| Codex version for the spike (question Q1) | owner | `codex --version` prints the chosen version in the spike shell |
| Buzz: owner npub and relay URL available to the lead | owner | written in the spike brief |
| `caffeinate -dims` for the duration of the spike | lead | `pmset -g` shows sleep prevented |
| Time `go test ./...` in amq-noc and workspace-cli | manifest-analyst | wall time recorded |

Nothing below starts before the amq upgrade.

### S1. Signed gate answer through amq-remote (criterion 1)

- Owner: security-dev (Claude Code, Opus 5.5, high). Reviewer: reviewer-openai
  (Codex, gpt-5.5, xhigh).
- Inputs: gate-transport report, `internal/operatorauth`, amq-remote 0.98.0,
  a scratch AMQ root with an operator handle, the owner's Buzz identity.
- Steps:
  1. Enroll a Buzz DM share for the operator handle with `amq-remote share
     --enable buzz-dm`, `amq-owner-sign`, and `amq-remote up`.
  2. Post a gate question from the operator handle. The Go core renders the
     text: gate id, action class, target digest, and the ordered PR list.
  3. The owner types `approve <gate-id> <class> <digest-prefix>` in Buzz on
     the phone. Buzz signs a kind 9 event with the owner's key.
  4. A Go prototype resolves the delivered AMQ message to its nostr event id
     from the amq-acp journal (`internal/acp/event.go:98`), fetches the event
     from the relay by id, verifies BIP-340 under the pinned owner npub,
     checks kind 9, the DM channel tag, the parsed tuple, and the gate
     window, then writes an operatorauth envelope carrying the raw event as
     evidence.
  5. Negative tests: wrong pubkey, altered content, event outside the window,
     a model-authored `approve` sent from a seat handle, a `deny`.
  6. Record whether a key other than the share's body key can read the NIP-29
     DM group. If not, the prototype reads through the enrolled body key and
     the report marks that as interim.
- Output: the prototype, the test log, a round-trip time from phone to
  verified envelope, and the answer to step 6.
- Pass: the phone-typed answer verifies; every negative case is rejected.
- Fail: no path yields an owner-signed event the Go core can verify. Phase 3
  does not start; upstream ask 8 becomes blocking.
- Wall time: 1.5 days. Can start on day 1 with reading and share setup.

Design C (ask 8) stays the long-term target. Design B, riding the Claude
PermissionRequest answer files, is rejected: it depends on an internal file
schema and a Claude-only target.

### S2. Seat push and merge under branch protection (criterion 2, amended)

- Owner: loadtest-operator (Claude Code, Sonnet 5.5, medium). Reviewer: lead.
- Inputs: the owner's own GitHub identity, amq-noc (no protection today).
- Steps: add a ruleset to amq-noc that requires one status check and blocks
  direct pushes to main; a seat pushes a branch and opens a PR with the
  owner's identity; the owner answers the typed merge gate; the merge lands
  through the required path. Try to add a `merge_queue` rule on the same
  public repo and record whether GitHub accepts it.
- Output: PR URL, ruleset JSON, merge-queue yes or no.
- Pass: the seat's PR merges only after CI passes and the gate is answered.
- Fail: the ruleset does not block a direct push, or the merge path needs a
  second identity after all.
- Wall time: 2 hours, plus 10 minutes of owner time.

### S3. darwin-arm64 build with the embedded pi (criterion 3)

- Owner: launch-dev (Codex, gpt-5.5, high). Reviewer: reviewer-anthropic
  (Claude Code, Opus 5.5, xhigh).
- Inputs: pi's npm distribution, Go 1.27, the pi extension notes.
- Steps: determine what a pinned pi runtime needs (Node or Bun); choose
  between a tarball of a Node binary plus the pi package tree and a
  Bun-compiled binary; embed with `go:embed`; unpack to
  `~/.amq-squad/runtime/<version>/` with a digest check; start a pi lead from
  the unpacked copy with PATH stripped of `pi` and `node`; measure binary
  size, unpack time, time to first prompt; check Gatekeeper and quarantine
  behavior on the unpacked binary.
- Output: a throwaway branch build, the size and timing table, the packaging
  decision with reasons.
- Pass: the binary starts the lead from the unpacked copy with no pi or Node
  on PATH and answers one prompt.
- Fail: no packaging runs without a host Node, or the bundle exceeds 250 MB.
- Wall time: 1 day. Independent.

### S4. Wrapper launch of one Claude seat and one Codex seat (criterion 4)

- Owner: launch-dev (Codex, gpt-5.5, high). Reviewer: reviewer-anthropic.
- Inputs: `internal/launchintent` (the compiler already targets the released
  AMQ), `internal/adoptionseam`, the launch API doc.
- Steps:
  1. Prototype `seat-exec`: reads `AM_ME` and `AM_ROOT` or `--seat`, writes
     the launch record and the role into the seat's extension directory under
     the reserved namespace `io.github.omriariav.amq-squad`, sets
     `AMQ_SQUAD_*` environment, execs the provider argv it was given. It adds
     no provider flags.
  2. Compile one intent with two runnable participants. Claude args:
     `--model sonnet --effort medium`. Codex args: `--model <id> -c
     model_reasoning_effort=medium --sandbox workspace-write
     --ask-for-approval on-request`. Wrapper: the absolute path of the
     installed binary with `seat-exec --seat <handle>`.
  3. Prepare; confirm the preview and the trust digest carry the provider
     args and the wrapper. Apply; confirm two tmux panes under AMQ.
  4. In the Claude seat, a user-scope hooks plugin stub echoes the seat
     environment at SessionStart. Measure PreToolUse under three faults: a
     hook that exceeds its timeout, a hook that exits non-zero without a deny
     decision, and a hook binary that is missing. Record whether the tool
     call proceeds and the measured timeout.
  5. Repeat the Codex launch on codex-cli 0.147.0 and on 0.154.0. Record the
     Apply result for each.
  6. Verify that AMQ's notify override forwards to the owner's configured
     Codex notify command after capture, as docs/launch-api.md:155 states.
- Output: the prototype, the Prepare preview, the Apply result for each
  Codex version, the PreToolUse fault table.
- Pass: both seats live under AMQ with model and effort in the trust digest,
  the wrapper having set only environment and role; the PreToolUse fail-open
  behavior is documented with numbers.
- Fail: the launch API refuses the wrapper shape, or the seat does not see
  the role.
- Wall time: 1 day. Depends on S0 (amq upgrade and the Codex version).

### S5. pi seat with Ollama through coop exec, and a task loop with a pi lead

Plan 13 lists these in phase 1 without an exit criterion. They produce the
R12 probe design and test the risk in 16.3.

- Owner: pi-dev (Claude Code, Opus 5.5, high). Reviewer: reviewer-openai.
- Inputs: pi, Ollama with qwen2.5:32b (installed) and qwen3.6:27b (to pull),
  the pi bridge protocol, the reference extension.
- Steps:
  1. `amq coop exec --root <root> --me pi-1 -- pi` against Ollama.
  2. Run the R12 probe: one scripted task that reads a file, edits it, runs a
     test, and reports. Record pass or fail per model and the time to first
     token.
  3. In a pi extension, hold a tool call until an external file appears.
     This is the revision 4 approval seam that stock pi does not offer
     (`docs/pi-bridge-protocol.md:272`).
  4. A pi lead decomposes three small tasks, dispatches them over AMQ to the
     pi seat, and converges.
- Output: the probe script, per-model results, the hold-seam verdict, the
  lead loop transcript.
- Pass: the probe passes on at least one local model, the hold seam works,
  the three tasks complete.
- Fail on the hold seam alone narrows 16.3 to session identity; pi seats then
  get no remote tool approvals until pi changes.
- Wall time: 1 day. Independent.

### S6. Load test (criterion 5, R46, reduced by owner decision)

- Owner: loadtest-operator (Sonnet, medium) with the owner present at start.
  Reviewer: lead.
- Shape (owner answer, 2026-10-20, option B): 2 repositories, 2 squads of 3
  seats, 6 seats, 25 small real tasks that build and test. Run on the
  installed v2.31 binary. The registry caps for 12 seats are extrapolated
  from the 6-seat numbers and marked as extrapolated until a 12-seat run
  happens before release.
- Repositories: amq-noc and workspace-cli, the two Go repositories from the
  owner's option A. ai-cli-handoff and claude-pulse stay in reserve. Forks
  are excluded. amq-squad itself is excluded as a target because it is the
  project under development.
- Seats: per squad one Claude lead, one Claude implementer, one Codex
  implementer: 4 Claude and 2 Codex. Codex seats need the AMQ fix from
  issue #1107 or codex-cli 0.147.0; on 0.154.0 with AMQ 0.98.0 they cannot
  launch fresh through the launch API.
- Tasks: 12 or 13 per repository, each touching about one file with an
  existing build and test command, drawn from open issues first and from
  `go vet`, lint, and TODO findings second. The manifest-analyst drafts the
  list on day 1; the owner approves it before the run.
- Measurement, all scripted before the run:
  - peak RAM: a sampler every 10 seconds summing resident memory over seat
    process trees plus `vm_stat`;
  - 429 and usage-limit events per provider: Claude session JSONL under
    `~/.claude/projects`, Codex session files under `~/.codex/sessions`,
    tmux pane captures as fallback;
  - wall time per task from task-store timestamps, plus total;
  - merge conflicts: PR mergeability from `gh pr view` plus a dry-run merge
    of each PR onto the base at the end;
  - time to the first subscription usage limit, if one occurs.
- Schedule: at the start of a fresh 5-hour subscription window, with no
  other seats running. If the limit hits, record the time and continue with
  Codex seats only.
- Output: `docs/v3-loadtest-<date>.md` with the five numbers, the raw
  samples, and the proposed registry caps per provider.
- Pass: the five numbers exist. A usage limit is a result, not a failure.
- Fail: the run did not complete and the numbers are partial.
- Wall time: 2 hours setup, 2 to 3 hours run, 2 hours analysis. One
  afternoon, exclusive of S1 to S5 seat usage.

### S7. Keep/delete manifest (16.1)

- Owner: manifest-analyst (Sonnet, medium). Reviewer: lead.
- Inputs: section 4 of this document, the manifest-pass report.
- Steps: write `docs/v3-manifest.yaml` with one entry per non-test Go file
  under `internal/`; write `scripts/manifest-check.sh`; get owner decisions
  on the four open items in 4.4.
- Pass: every file appears once, the sum of targets matches section 4 within
  10 percent, the check script runs green.
- Wall time: half a day. Independent.

### S8. File the upstream asks

- Owner: lead. Asks 1 to 7 from sections 11 and 16.8, plus asks 8 and 9 from
  section 2 of this document. Ask 4 in its reworded form.
- Wall time: 2 hours, day 1.

### Dependencies and parallelism

| Task | Depends on | Runs in parallel with |
| --- | --- | --- |
| S0 | owner | nothing |
| S1 | S0 (amq-remote), Buzz identity | S3, S5, S7, S8 |
| S2 | S1 and S4 (owner identity, CI-only ruleset) | S3, S5 |
| S3 | none | everything except S6 |
| S4 | S0 (amq, Codex version) | S1, S3, S5 |
| S5 | none | S1, S3, S4 |
| S6 | S0, task list from S7's owner | nothing; runs alone |
| S7 | none | S1, S3, S5, S8 |
| S8 | none | all |

Proposed days:

- Day 1: S0 in the morning. S7 and S8 and S1 setup while the load test task
  list is drafted. S6 in the afternoon with no other seats running. S3 and
  S5 start on day 1 only if they fit before S6.
- Day 2: S1, S3, S4, S5 in parallel; S2 in a gap.
- Day 3: S1 negative tests and the fault table in S4; results, the go or
  no-go table for the five criteria, and the registry cap proposal from S6.

## 4. Keep/delete manifest: method and first pass

### 4.1 Method

Leaf-first for packages, root-first for `internal/cli`. A package is deleted
only when its importer count is zero. A cli cluster is cut by removing its
entry points so everything it owns becomes unreachable, then deleting the
files, then fixing the remaining call sites from the compiler's undefined
symbol list.

Steps, one commit each, each green on `go build ./... && go vet ./... &&
go test ./...`:

0. Module path to `/v3`; baseline build and test count recorded.
1. Delete the orphans `internal/console` and `internal/tmuxharness`.
2. Remove entry points from `internal/cli/command_registry.go` and run
   `deadcode ./cmd/amq-squad`; its output is the work list.
3. Cluster by cluster, callers before callees: goal supervision, recovery
   transition, wizard and start, up/down/resume, notifications, external
   orchestrator, namespace migration, `launch.go`, runtime control, tmux and
   terminal, completion, evidence and collect. Extractions listed in 4.3 go
   into one temporary holding file named in the manifest. Probe command:
   `go build -gcflags=-e ./internal/cli 2>&1 | grep 'undefined:'`.
4. Packages, once `go list` shows no importer: runtimeaction, act,
   runtimecontrol, wizard, notifier, liveidentity, autonomy, bootstrapack,
   tmuxpane last, commandevidence last.
5. In-file cuts for the shrink clusters (2a-2), one cluster per commit.
6. `go mod tidy`; bubbletea, bubbles, and lipgloss leave the module graph.

The manifest is `docs/v3-manifest.yaml`, one entry per path with
`v2_lines`, `fate`, `target_lines`, `cluster`, `extract`, `reason`, and
`status`. Executors flip `status` in the same commit that deletes the files.
`scripts/manifest-check.sh` enforces that every non-test file under
`internal/` appears once, no deleted path exists, and targets sum within
tolerance. Do not stub deleted functions with empty bodies; cut the callers.

### 4.2 Non-cli packages, first pass

| Package | v2 | Fate | 3.0 | Reason |
| --- | --- | --- | --- | --- |
| act | 400 | delete | 0 | NOC control argv; amq-noc owns controls |
| activity | 263 | shrink | 200 | seat activity file feeds seat state |
| adoptionseam | 298 | keep | 250 | launchapi Prepare and Apply seam |
| agentcatalog | 174 | keep | 174 | built-in runtime, model, effort tiers; seed for the registry |
| amqexec | 32 | keep | 32 | AMQ env helper |
| attention | 169 | shrink | 50 | stable ids for `attention[]`; drop renotify dedup |
| autonomy | 283 | delete | 0 | replaced by `grants.yaml` |
| bootstrapack | 246 | delete | 0 | hooks inject every turn; owner may revisit for Codex |
| catalog | 245 | keep | 245 | role catalog |
| commandevidence | 2,069 | replace | 0 | runner, not ingest; port redaction rules first |
| compoundrelease | 3,499 | keep | 3,200 | release gate flow; review store.go and reconcile.go for pane references |
| console | 3,626 | delete | 0 | no importers |
| drafter | 586 | shrink | 450 | brief drafting as an off-path verb |
| flock | 233 | keep | 233 | six importers |
| launch | 704 | shrink | 250 | minimal launch record; seat-exec writes it |
| launchintent | 538 | keep | 450 | the launch compiler |
| liveidentity | 257 | delete | 0 | AMQ owns liveness |
| namespace | 120 | keep | 120 | path scheme; drop the profile import |
| notifier | 155 | delete | 0 | NOC renders attention |
| operatorauth | 2,033 | keep | 1,800 | gates and envelopes; adds bip340 evidence in phase 3 |
| procinfo | 503 | shrink | 250 | PID liveness only |
| reviewrebind | 837 | keep | 700 | merge-train rebind proof (amendment 2) |
| role | 589 | keep | 450 | role files in the seat directory |
| rules | 301 | keep | 250 | managed rules block; guarded path |
| runtimeaction | 154 | delete | 0 | port the argv plus id action shape into the status contract first |
| runtimecontrol | 551 | delete | 0 | terminal capability registry |
| state | 3,444 | shrink | 2,200 | classifier base; line-by-line review |
| task | 3,463 | shrink | 2,900 | five states and leases exist; pools are new |
| team | 1,689 | shrink | 650 | rewrite around `squad.yaml` in 2b, not 2a; highest fan-in |
| tmuxharness | 485 | delete | 0 | no importers |
| tmuxpane | 1,686 | delete | 0 | R6; last, after six cli callers are cut |
| userconfig | 150 | keep | 150 | `~/.amq-squad`; grants.yaml lives beside it |
| wizard | 4,962 | delete | 0 | extract `routing.go` first: nearest ancestor of the routing table |
| worktreeplan | 1,647 | keep | 1,300 | worktree per seat |
| Subtotal | 36,391 | | 16,304 | |

### 4.3 internal/cli clusters, first pass

| Cluster | v2 | Fate | 3.0 | Extract before delete |
| --- | --- | --- | --- | --- |
| C1 tmux, iTerm2, Terminal, layout, wake, panes, orchestrate, bootstrap_failure, claude_rename, team_member_control_continue | 5,473 | delete | 0 | nothing; inline a tmux presence check for `team_launch_launchapi.go:37` |
| C2 runtime control: runtime_actions, runtime_json, runtime_identity, operator_control | 1,650 | delete | 0 | argv plus id action shape; broadcast verb if kept; generic path helpers |
| C3 board: global_noc, status_board, global_status, global_status_doctor | 2,342 | shrink | 450 | fleet registry read and write (`global_noc.go:117-265`) pending owner decision |
| C4 wizard, start, new, setup, init, plan, list, preview_flags | 4,473 | delete | 150 | rules pointer-stub digest flow (`init.go:110,266`); brief candidate envelope (`up.go:424`) |
| C5 goal supervision | 6,204 | delete | 0 | `digestBytes` (`resume_goal.go:383`) |
| C6 recovery transition | 1,449 | delete | 0 | nothing |
| C7 namespace migration, conflict, open and rename | 3,578 | delete | 250 | admission locks (`namespace_migration_guard.go:42,67,84,253`), safe open |
| C8 notification watcher, AMQ watch, session notifier | 2,361 | delete | 0 | nothing |
| C9 external orchestrator, lead identity | 1,220 | delete | 0 | path helpers from lead_identity.go |
| C10 liveness, live identity, version alignment | 1,282 | shrink | 250 | liveness verdict vocabulary as the closed seat-state list |
| C11 up, down, resume, restore, session reset, exit, wait posture, visibility | 5,126 | delete | 400 | `exit.go` whole; `wait_posture.go:67,170`; shell quoting helpers |
| C12 bootstrap, preflight, team launch bootstrap | 1,535 | shrink | 450 | context verb ancestors (`bootstrap.go:77,98,247`) |
| C13 verify, verify action, authorization, rebind, amq root authority | 2,676 | shrink | 1,800 | guard verb ancestors (`verify_action.go:81,569`) |
| C14 amq, amq send, amq env | 2,194 | shrink | 700 | context resolution, send, env pinning |
| C15 doctor | 1,912 | shrink | 600 | framework and version, launchapi, floor, pin checks; the probe is new code |
| C16 status | 1,951 | shrink | 900 | snapshot build and render; contract fields are new |
| C17 operator session, attention, self-approve, handoff, identity | 4,425 | shrink | 1,600 | gate raising and answering; attention projection (`operator_attention.go:433`) |
| C18 team roster, overlay, lead, member, launch, launchapi, rules | 9,385 | shrink | 1,650 | `team_launch_launchapi.go`, team rules, launch preflights; rewrite in 2b |
| C19 task, dispatch, selection, completion evidence | 2,764 | shrink | 1,650 | queue-then-send dispatch; drop pane nudges |
| C20 evidence, collect | 1,464 | replace | 0 | secret naming and task link semantics for the ledger |
| C21 gates, release plan, compound release | 1,553 | keep | 1,400 | |
| C22 worktree, worktree isolation | 683 | keep | 600 | |
| C23 roles, agents, model defaults, effort, native args | 2,412 | shrink | 1,300 | provider-argument assembly is R4; delete `agent.go` |
| C24 completion | 996 | delete | 0 | regenerate small if wanted |
| C25 execution mode | 682 | review | 150 | fault remedy action object |
| C26 context resolution, target root, pathnorm, profiles | 1,743 | shrink | 950 | `resolveScopedCommandContext`; delete profiles |
| C27 briefs, drafter config, workstream | 1,013 | shrink | 650 | |
| C28 launch.go, the legacy launch driver | 1,214 | delete | 300 | launch record write and rollback (`:757,780`), role write (`:1182-1196`), AMQ env resolver (`:917`) into seat-exec |
| C29 cli core, registry, output, json | 778 | keep | 600 | registry rewritten for the 3.0 command set |
| Total cli | 74,538 | | 16,800 | |

Target after 2a: about 33,100 lines under `internal/` (range 30k to 38k),
a 70 percent removal. Whole-file deletion alone (2a-1) leaves roughly 55k to
60k; the rest is in-file cuts (2a-2).

Ten largest removals: team cluster 7,735; goal supervision 6,204; tmux and
terminal 5,473; wizard package 4,962; up/down/resume 4,726; start and setup
4,323; console 3,626; namespace migration 3,328; operator cluster 2,825;
notifications 2,361.

The three verbs have no hook ancestors in v2; v2 has no hooks. Nearest
ancestors: context from `bootstrap.go:77,247`, `rules.go:116`,
`role.go:108`; guard from `verify_action.go:81,569`,
`operatorauth/policy.go:260`, `compoundrelease/guard.go:18`; evidence from
the redaction and task-link rules in `evidence.go` and
`task/command_evidence.go:23`. The verbs are mostly new code.

### 4.4 Open items for the owner inside the manifest

Decisions that change targets by a few hundred lines; the manifest-analyst
can proceed with the default in brackets:

- bootstrapack: delete, or keep for Codex launch-only seats [delete].
- fleet registry in `global_noc.go`: keep about 150 lines, or let amq-noc
  own it [keep; 16.3 says `status --fleet --json` reads `fleet.yaml`].
- `down`: return as a thin stop-squad verb on the launch API [yes, about
  150 lines, phase 2b].
- `commandevidence/subject.go`: keep as the guard's target-digest helper
  [port the 400 lines into guard].

## 5. Proposed spike roster

Section 6 rules: plan and review never below their tier; the reviewer's
family differs from the implementer's. Every seat runs on v2.31 tooling.
Fable (this session) is the planner and reviewer of record, not a seat.

| Seat | Role | Runtime | Model | Effort | Tasks |
| --- | --- | --- | --- | --- | --- |
| lead | squad lead | Claude Code | Opus 5.5 | high | dispatch, converge, S8, reviews of S2, S6, S7 |
| security-dev | implementer | Claude Code | Opus 5.5 | high | S1 |
| launch-dev | implementer | Codex | gpt-5.5 | high | S3, S4 |
| pi-dev | implementer | Claude Code | Opus 5.5 | high | S5 |
| loadtest-operator | operator | Claude Code | Sonnet 5.5 | medium | S6, S2 |
| manifest-analyst | analyst | Claude Code | Sonnet 5.5 | medium | S7, load test task list |
| reviewer-openai | reviewer | Codex | gpt-5.5 | xhigh | S1, S5 |
| reviewer-anthropic | reviewer | Claude Code | Opus 5.5 | xhigh | S3, S4 |

Eight seats, at most six live at once. All Claude seats share the Max
subscription unless Q5 sets an API key for the leads. The load test runs
with none of these seats live.

## 6. Risks the plan does not list

Each with a mitigation inside the existing phases.

1. Codex capture pin. codex-cli 0.154.0 is installed; AMQ launches fresh
   Codex seats only on 0.147.0. Phase 1: pin 0.147.0 for the spike (Q1) and
   file ask 9. Phase 2b: the registry marks a Codex entry verified only when
   the AMQ capability reports Capture, and doctor names the mismatch.
2. Signature scheme mismatch. The owner key is BIP-340; operatorauth is
   Ed25519. Phase 1: amendment 1. Phase 3: a bip340 evidence scheme with the
   raw event as evidence and an issuer signature by the Go core.
3. Gate text is relay-visible. Buzz DMs are not end-to-end encrypted. Phase
   3: the gate message carries ids, class, digest, and PR numbers only; never
   code, paths, or secrets.
4. Non-body relay read. Design A needs a reader key the relay admits to the
   DM group. Phase 1: S1 step 6 answers it. Fallback: read through the
   enrolled body key, marked interim, until ask 8 lands.
5. One subscription for every Claude seat. Twelve seats on one Max window
   can truncate the load test and starve the spike. Phase 1: the load test
   runs alone at the start of a window and records the limit; leads on API
   keys (Q5). Phase 2b: the quota pool field in the registry separates
   subscription and API entries, which section 5 already requires.
6. Load test and spike contention. Both need seats, RAM, and quota. Phase 1:
   the schedule in section 3 runs S6 exclusively.
7. Building 3.0 with the tool under construction. Squads run the installed
   v2.31 binary and plugin, never a build from the working tree. Phases 2
   to 4: `make install` is forbidden on the 3.0 branch; the brief names the
   installed binary path.
8. Module path. A v3 tag needs the `/v3` import path. Phase 2a step 0, one
   mechanical commit.
9. Embedded runtime packaging. pi needs a JavaScript runtime; a bundled Node
   or Bun binary raises size, codesigning, and Gatekeeper questions. Phase
   1: S3 decides. Phase 5: codesigned bundles, as 16.4 says.
10. pi has no external approval seam. The reference bridge is revision 3 for
    that reason. Phase 1: S5 step 3 tests whether an extension can hold a
    tool call. Phase 4: if not, 16.3 narrows to session identity.
11. Codex notify is already configured on this Mac. AMQ overrides it per
    launch and forwards after capture. Phase 1: S4 step 6 verifies the
    forward. Phase 5: uninstall leaves the owner's notify untouched.
12. Phase 2a is underestimated. Whole-file deletion is 2 days; the 22k lines
    of in-file cuts are 1 to 2 more. Amendment 5 splits 2a so 2b, 3, and 4
    start after 2a-1.
13. tmuxpane cannot go first. Six kept cli files import it. The manifest
    order cuts those callers in 2a-1 and deletes the package last.
14. Sleep. System sleep is one minute, held off only by other processes.
    Phase 1: caffeinate in S0. Phase 5: the doctor warning in R45.
15. Reviewer capacity. Two xhigh reviewers for four implementation tasks.
    If S1 and S5 finish together, reviewer-openai serializes them; the
    schedule absorbs half a day.
16. Codex folder trust (found in S4, 2026-10-21). A fresh codex-cli seat
    stops on its own "Do you trust the contents of this directory?" prompt
    until the project is listed with `trust_level = "trusted"` under
    `[projects."<path>"]` in `CODEX_HOME/config.toml`. The launch grammar
    has no override and the wrapper does not write owner config. Phase 1:
    the owner pre-trusts each load-test repository once; the spike never
    writes the owner's Codex config. Phase 2b: an owner-run
    `amq-squad trust <repo>` step that writes the block with consent, and a
    doctor check that lists untrusted project roots before a Codex seat
    launches. Not a launch API ask; this is codex-cli behaviour.

## 7. Questions for Omri

Each question has two options and one recommendation. The spike uses the
recommendation unless you say otherwise. Answer with A or B.

Question 1. Codex version.
Your Codex is version 0.154.0. AMQ starts a new Codex seat only with version
0.147.0. With 0.154.0, AMQ refuses the seat.
- Option A: Install Codex 0.147.0 in a second folder. Use it for the spike.
- Option B: Keep 0.154.0. Run the load test with Claude seats only.
- I recommend A.
- Update, 2026-10-20: upstream issue #1107 and PR #1108 (version floor,
  verified on 0.154.0, Prepare-time refusal) are open on
  avivsinai/agent-message-queue. If a release with that fix ships before the
  spike, option A is not needed: upgrade amq to that release instead.

Question 2. Bot identity.
Seats must push branches with an identity that is not yours. Then you can
give the required review.
- Option A: Make a second GitHub user account. This needs a second email
  address.
- Option B: Make a GitHub App. Its tokens expire after one hour.
- I recommend A now, and B later for the merge train. Make it before day 2.
- Owner answer, 2026-10-20: neither. No bot account. The developer owns
  their identities. See amendment 7 and task S2.

Question 3. Load test repositories.
The load test needs four repositories that you own.
- Option A: amq-noc, workspace-cli, ai-cli-handoff, claude-pulse.
- Option B: Replace one of them with amq-squad.
- Tasks come from open issues first, then from lint findings.
- I recommend A. Owner answer, 2026-10-20: A.

Question 4. Branch for 3.0.
The plugin marketplace reads from main. If we build 3.0 on main, v2 users
get 3.0 plugin files before release.
- Option A: Build 3.0 on a branch named v3. Keep main at v2.31 until release.
- Option B: Build on main. Keep v2 on a branch named release/v2.
- I recommend A. Owner answer, 2026-10-20: A.

Question 5. API key for the leads.
All Claude seats use your Max subscription. Twelve seats share one 5-hour
limit. The load test can stop early.
- Option A: Set ANTHROPIC_API_KEY for the lead seats only. The other seats
  stay on the subscription. This costs money per token.
- Option B: Keep everything on the subscription. Accept that the test can
  stop early.
- I recommend A.
- Closed, 2026-10-20: the spike runs with 1 to 2 seats and the load test
  with 6, all on the subscription. No API key is needed in phase 1. The
  load test records the time to the first usage limit, if any.

## Appendix: research inputs

Read-only reports produced for this plan, committed under
`docs/v3-research/` on 2026-10-21 so the evidence travels with the plan:

- `docs/v3-research/manifest-pass.md`
- `docs/v3-research/gate-transport.md`
- `docs/v3-research/host-baseline.md`

Spike execution notes live in `docs/v3-spike/spike-log.md` and the per-task
reports `docs/v3-spike/S<n>-report.md`.
