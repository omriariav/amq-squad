# amq-squad 3.0 phase 1 spike log

Times are local (Asia/Jerusalem). Planner and reviewer of record: the Fable
sessions. Seats are Claude Code agents (Opus 5.5, Sonnet 5.5) and Codex
agents launched from the orchestrating session; no amq-squad seats run the
spike itself.

## Day 1, 2026-10-21

Times in this section were corrected at 01:30 from commit timestamps; the
orchestrator had overestimated elapsed time by up to three hours.

- 00:02 Owner go recorded. `v3` branch created from main; docs/v3-plan merged.
- 00:02 amq-squad v2.31.0 installed at ~/go/bin (binary was absent; only the
  plugin was present). amq 0.102.0, amq-remote and amq-owner-sign present.
  codex-cli 0.154.0 on PATH; upstream PR 1108 still open, so S4 uses a
  prefixed codex-cli 0.147.0.
- 00:03 `caffeinate -dims` started.
- 00:05 Max subscription window reference start: first Claude agent launched.
  Six agents live by 00:12: S7 (Sonnet), S8 (Sonnet), S3 (Codex), S4
  (Codex), S5 (Opus), S1 (Opus).
- S1 runs on a synthetic key; exit criterion 1 stays open until a real-key
  run with the owner's Buzz npub and relay URL.
- S6 runs alone after the day-1 agents finish, at the start of a fresh
  window; the start time and any rate-limit events are recorded here.

- 00:15 S7 PASS, merged (ade6393). Manifest 284 files, 110,929 -> 33,504.
  S6 task list drafted, 25 tasks; lead reviewed with no objection.
- 00:18 S8 drafted, merged (e1d28cf). Lead wording fixes applied. Filing
  waits for the owner's morning read.
- 00:19 S4 on the Codex agent: no seat launched. The Codex agent sandbox
  denied network, writes under ~/.amq-squad and branch creation. Partial
  evidence stands: seat-exec preserves argv and execs the provider; AMQ
  0.102.0 Prepare accepts the two-seat wrapper request with model, effort and
  wrapper args in the trust digest. Rerun on Opus (high); reviewer family
  Codex. Roster deviation: Codex subagents review, Opus implements.
  Precision: the failure is a property of the Codex subagent inside this
  Claude Code harness. It says nothing about codex-cli seats launched through
  `amq launch`; S4 and S6 still launch real Codex seats with the 0.147.0
  prefix, and their ability to write and commit is a separate finding.
- 00:20 S3 on the Codex agent: same sandbox failure, nothing measured. Rerun
  on Opus (high); reviewer family Codex. Finding carried over: the pi npm
  scope moved to @earendil-works/pi-coding-agent.
- Overnight rule agreed with the lead for S6: seats open PRs, merge nothing;
  mergeability from gh pr view plus dry-run merges; owner approves after the
  fact in the morning; earliest start 05:05; needs the S4 rerun's 0.147.0
  prefix result.

- 00:21 S4 interim: amq launch Apply on 0.102.0 created both panes with the
  wrapper; the fresh Codex 0.147.0 seat stops on codex's folder-trust
  prompt. Answering it writes to ~/.codex/config.toml. No agent may write
  the owner's Codex config tonight; the Codex half of S4 is blocked by owner
  decision. Lead added risk 16 to the phase 1 plan.
- 00:22 S6 decision (lead, option A): 05:05 start, six Claude seats, 21
  tasks (N11, N12, W10, W11 held back for a Codex follow-up after the owner
  pre-trusts amq-noc and workspace-cli). PRs only, no merges. Sanity check:
  the first Claude seat must show no folder-trust prompt of its own.

- 00:27 S4 report merged (3dbd27b): PASS for the Claude seat, BLOCKED BY
  OWNER DECISION for the Codex seat. Fault table: deny blocks in 0.17 s;
  timeout, exit 1 and missing hook all fail open (5.11 s, 0.36 s, 0.44 s).
  0.154.0 Apply refuses with capture_version_unsupported. Codex reviewer
  (xhigh) started. Open: initial_input is outside the trust digest; a
  possible config.toml rewrite by codex at seat start (unproven).

- 00:36 S4 report re-merged after the Codex review (f81a772): criterion 4
  NOT PASSED, Claude seat demonstrated, Codex seat blocked; closure run
  defined. Amendments 2 corrected and 7 added (276130c).
- 00:39 S1 merged (23ad2a9): VERIFIER READY, REAL-KEY RUN PENDING.
  Synthetic round trip 812 ms median on nos.lol; local-relay dry run of
  realkey-run.sh PASS; 14 tests plus 21 subtests green with -race. Step 6:
  only the body key reads the DM group; interim until ask 8. Codex reviewer
  started. Owner items: npub, relay URL, amq-owner-sign session (nsec typed
  hidden), DM channel id, one approve typed on the phone.

- 00:40 S5 merged (84349d9): PASS, all three parts; 16.3 does not narrow.
  qwen3.6:27b 3/3 on the R12 probe; qwen2.5:32b 2/3 with one false success;
  gemma3:4b no tool calls. Hold seam works and fails closed. Lead loop
  converged twice; one task dropped by drain without a lease (caught by the
  lead). Package scope is @earendil-works/pi-coding-agent. Codex reviewer
  started.
- 00:41 S3 merged (240ce73): PASS. Three packagings start pi with nothing on
  PATH and answer a prompt; launcher 67.3 MB (Node tree) or 34 MB (Bun).
  Node floor >=22.19.0. Quarantined launcher hangs: signing and notarization
  are needed for a browser-downloaded binary. Codex reviewer started.
  A Gatekeeper dialog may be open on the Mac screen from the test.
  Follow-up on spike/s3-embed: lead rulings and the Codex review are
  applied. The decided build is the pruned Node tree plus pinned fd and
  rg: 59.27 MB launcher, 179.25 MB in 6,432 files unpacked, 1.13 s cold
  unpack. First token takes 2.38 s cold and 0.25 s warm. The integrity
  check at every launch costs 43.5 ms. The launcher sets the 2b defaults
  and a per-seat agent dir. S3 runs created ~/.pi/agent; it was removed.

- 00:45 S6 prepared (spike/s6-loadtest, 58e8a88, merged). Both repos set
  up on v2.31 with untracked config, sessions s6-noc (11 tasks) and s6-wcli
  (10 tasks), lead plus two Sonnet implementers each at medium effort, two
  detached worktrees per repo, doctor exit 0 with warnings only. Five
  measurement scripts with dry-run self-tests. Runbook in
  docs/v3-spike/S6-runbook.md. Host at 00:33: 24 percent memory free, 0.9 GB
  swap, with six agents live. Start at 05:05 after every other agent stops.

- 00:48 Codex reviews (xhigh, read-only; the Codex subagent cannot run go
  test because its sandbox blocks cache writes): S4 accept with edits,
  applied and merged (f81a772, 9848898); S5 accept with edits, dispatched;
  S1 REJECT on conformance majors (s < n, strict decoding, window bounds,
  digest binding, envelope cross-checks), fix list dispatched, owner run
  waits for the re-accept; S3 accept with edits (launch-time integrity
  check, fd and rg pinning, notarization test), dispatched. S1 follow-ups
  merged (e5cbd14): no phone-side grant signing exists, owner question is
  "export the Buzz nsec once, yes or no"; S1-owner-run.md written.

- 00:57 S5 closed after the Codex edits (a39ed19); S3 closed after the
  rulings and Codex edits (2e0f7cb): launch integrity check 43.5 ms, fd and
  rg embedded, post-strip launcher 59.3 MB. S1 fixes merged (712b364):
  20 tests, 70 subtests, -race green on v3; first line "VERIFIER READY
  (re-review pending), REAL-KEY RUN PENDING"; Codex re-review requested.
  Owner notes: the S1 dry run left a throwaway public npub on the Mac
  clipboard; a Gatekeeper dialog may be open from the S3 quarantine test.

- 01:15 S1 Codex re-review: REJECT on three narrow items (consume without
  envelope when no issuer key; envelope-verify unbound when the wrapper has
  no question; run sheet option B put the nsec on the clipboard). All other
  fixes passed with file:line. Fixes dispatched; third round follows. Lead
  ruling: S6 does not wait for the re-review; the Codex reviewer is logged
  as the one non-S6 process.

- 01:29 S1 third round merged (478cd9f): consume is a side effect of the
  envelope write only; envelope-verify fails closed without a question;
  event id, issuer key id and owner npub checked against the gate record;
  commit serialized under a gate lock (25 concurrent runs, one winner each);
  nsec never on the clipboard; enrollment retry has an automated test.
  25 tests, 71 subtests, -race green on v3. Third Codex review requested.

- 01:40 Lead approved two mechanical phase 2a items for the idle hours,
  outside the five-criteria table: step 0 module path /v2 -> /v3 (branch
  v3/2a0-module-path, not merged until the owner's go and the lead's
  accept) and the 2a-1 work list from deadcode and compile probes (branch
  v3/2a1-worklist, planning output, no deletion committed). Both on Sonnet,
  hard stop 04:30, with a 04:42 stop check before the 05:03 S6 trigger.

- 02:05 Phase 2a step 0 done on branch v3-2a0-module-path (c899e8f, not
  merged): module path v2 -> v3 in go.mod, 369 Go files and the Makefile
  release-smoke line; 36 packages ok and 4,792 tests before and after; go
  mod tidy changed only the module line. README, MIGRATION, RELEASING and
  scripts/check-release-version.py still say v2 and change together at the
  v3 tag. No test depends on the path string.

- 02:10 2a-1 work list merged as planning output (d586e5b): 30 cuts,
  49,894 lines by whole-file deletion, 61,035 remaining, 27,531 left for
  2a-2; six planner decisions listed in the document. Lead accepted c899e8f
  (module path), unmerged until the owner's go.
- 02:10 The orchestrating session reached its usage limit. S1 round 4 may
  still be running; its result is picked up in the morning. The 04:42 stop
  check and the 05:03 S6 trigger stand; the fresh window starts at 05:05.

- 05:03 Stop check: every spike agent had reported completion; tmux shows
  only the owner's two pre-existing sessions; two claude --agent-id
  processes remain and are logged as non-S6 baseline: PID 10505 is this
  session's idle s1-review Codex reviewer (about 420 MB RSS), PID 11552 is
  an idle teammate of the fix-codex-version session (about 300 MB RSS). Free memory about 7 GB. S6 operator agent (Sonnet)
  started from docs/v3-spike/S6-runbook.md; seats start at or after 05:05.

- 05:06 S6 preflight failed on "other agent-like processes": 13 idle
  --agent-id processes, 4 interactive claude sessions, 1 codex, app-servers,
  claude-mem and chroma. Seven were this session's finished reviewers and
  researchers; stopped. The rest belong to live sessions and stay idle.
  Decision: run now, labeled "shared host, other agents idle; window
  shared with idle sessions"; preflight process check recorded as an
  overridden warning (script unchanged); per-seat RSS is the clean RAM
  column, system columns marked contaminated by the listed baseline.

- 05:08 S6 NOT RUN. The Claude Code auto-mode classifier denied the
  operator's first seat launch ("Create Unsafe Agents"). No retry, no
  workaround, no seat started; samplers stopped; the 05:07:36 start is
  recorded as aborted before the first seat. Criterion 5 stays open. The
  owner launches S6 from his own session in the morning with the command
  sequence in docs/v3-spike/S6-report.md (runbook fixed: v2.31 takes
  `--session <name>`). Fourth S1 Codex review started meanwhile.

- 05:12 The fourth S1 Codex review could not start: the Codex subagent
  opens an iTerm2 split pane and iTerm2's socket answered "Resource
  temporarily unavailable" twice. Deferred to the morning; no Opus reviewer
  substituted, so the family rule holds. S1 stays "re-review pending".

## Rate-limit and usage events

- 02:10 Max subscription usage limit reached by the orchestrating session,
  2 h 05 min after the 00:05 window reference, after running up to six
  Opus and Sonnet agents in parallel plus three Codex reviewers. This is
  the first measured time-to-limit of the spike (R46 data point, not the
  S6 number).
- 05:07 Preflight process check overridden by lead (option b): 1 FAIL other-agent processes recorded in host-state-at-start.txt; 7 of the lead's subagent processes still present at 05:07 despite stop. Run label: Claude-only, 21 tasks; shared host, other agents idle; window shared with idle sessions

## S6 run `s6-claude-20261021`

- 05:07 Run start. Window reference 2026-10-21 00:05:00 IDT (first Claude agent of the spike); the 5-hour window reset was due at 05:05. Repos: /Users/omri/code/amq-noc /Users/omri/code/workspace-cli.
- Samplers write to /Users/omri/.amq-squad/spike/s6/s6-claude-20261021. Time to first usage limit is measured from this start.
- 05:08 Run end for `s6-claude-20261021`: elapsed 0h01m. aborted before first seat: launch denied by auto-mode classifier (Create Unsafe Agents); no seat ever started
- 05:08 S6 outcome: NOT RUN. Seat launch denied twice by the Claude Code auto-mode classifier ([Create Unsafe Agents]); the 05:07:36 start is "aborted before first seat". No seat, tmux session or squad state was created. See S6-report.md "Owner run".

## S6 run `s6-claude-20261021`

- 07:22 Run start. Window reference 2026-10-21 00:05:00 IDT (first Claude agent of the spike); the 5-hour window reset was due at 05:05. Repos: /Users/omri/code/amq-noc /Users/omri/code/workspace-cli.
- Samplers write to /Users/omri/.amq-squad/spike/s6/s6-claude-20261021. Time to first usage limit is measured from this start.
- 07:21 Preflight pre: all PASS except the known "other agent-like processes" warning. Owner (Omri) is in the loop: launch commands run from his `!` prompt in the kickoff session (the auto-mode classifier denied the same `start`/`resume --exec` commands from the operator again at 06:2x and 07:2x, reason "Create Unsafe Agents").
- 08:06 Owner ran `start s6-noc ... --apply <digest> --decision <id>=trust_exact_subject`: trust recorded ("Sent launchapi decision applied"), then `error: launchapi: 0 applied command(s) for 3 team member(s)`. Same for s6-wcli at 08:06:23.
- 08:07 Retry of the plain `--apply` refused with `duplicate_live` for `lead`: the decision send itself writes an active `lead` presence (last_seen = the send time), and the launch preflight treats it as a live lead for 90 s (`presenceFreshness`). `--force-duplicate` is deprecated on the launchapi path and does nothing, yet "Next actions" still names it. Workaround: wait 90 s.
- 08:08 Plain `--apply` after the window: `0 applied command(s)` again, outcome `ready`, no required action. Root cause found by reading the pinned library (agent-message-queue v0.75.0, in-process): the S6 setup had left `.amq/launch.local.json` in both repos with `launcher_preference: [tmux, commands]` (files dated 00:29, listed in S6-runbook.md as untracked setup state). With tmux first, the library's own TmuxBackend.Create runs instead of the "commands" backend amq-squad 2.31 drives; it builds the pane command from `os.Executable()` = `/Users/omri/go/bin/amq-squad` as `<amq> coop exec ...`; amq-squad has no `coop` command, the first pane exits at once, the session is gone before the second `split-window`, Create fails, the journal stays at phase `intent` with execution tickets, and `Apply` returns `outcome=action_required reason=backend_create_failed` with `FailureDetail`. amq-squad 2.31's seam (`internal/cli/team_launch_launchapi.go` tmuxPlanFromCommands) reads only `Commands` and reports the count mismatch, so the reason never reaches the operator. Evidence: `~/.amq-squad/spike/s6/s6-claude-20261021/stale-launch-state/*/journal.json` and `executions/*.json` (`amq_executable: /Users/omri/go/bin/amq-squad`).
- 08:12 [SUPERSEDED 2026-10-22 06:24: the cause was the os.Executable() amq path, not the launcher preference; the preference is never read on this path] Fix in spike scope only: both `launch.local.json` set to `["commands"]` (originals kept as `launch.local.json.<repo>.before` in the run folder); both `.agent-mail/<session>/meta/launch/` moved to the run folder (`stale-launch-state/`), because the library's recovery path (`recoverJournal`) would recreate with the journaled tmux backend and fail the same way; `meta/launch` did not exist before 08:06. Both plans `ready` again; subject digests changed (s6-noc 7c5102cf…, s6-wcli bb912997…), plan digests unchanged. Three 2.31 findings for the v3 plan: (1) seam must surface ApplyResult outcome, reason_code and failure detail; (2) a decision send must not register the lead as live; (3) `Next actions` must not name a deprecated flag. v3 owns launch end to end (R3, R40), so (1) and (2) land in the Go core, not in a wrapper.
- 08:15 Fourth S1 Codex review: RE-REVIEW ACCEPT (four notes, no finding; both third-review items CLOSED with file:line evidence). Operator ran `go test -race -count=1` in spike/gateverify: green, 97 cases. S1-report.md first line updated; section 11.3 added. Criterion 1 now waits only on the owner nsec decision and the real-key run.
- 08:26 S4 closure run done (Opus implementer, commit a2dfad1): role visible from both seats (PASS), Codex capture FAIL for an upstream reason (ask 12), notify forward not observed, worktree commit NO (ask 5 confirmed), isolated CODEX_HOME shows codex rewriting config.toml at start, env names clean at process level but GEMINI_API_KEY appears in the Codex shell tool via the owner's shell profile. Criterion 4 stays NOT PASSED; the block is upstream. Asks 12 and 13 drafted; amendment 17.
- 08:30 Harness incident: the plan-revision agent ran `git checkout -b v3-plan-2-3 v3` in the main checkout instead of its worktree, so the main checkout left `v3`; the lead's commits 309b89d to bb1769b and the S4 agent's a2dfad1 landed on that branch. History stayed linear on top of c63122a, so `v3` was fast-forwarded to a2dfad1 and the two side branches deleted. Nothing lost. Rule for the next agents: the lead creates the branch, or the agent uses `git worktree add`; never `git checkout -b` in /Users/omri/code/amq-squad.

- 11:59 S6 launch lines with the current digests (311701fe.. noc, 41dc7cc6.. wcli; required_actions none) written into S6-report.md for the owner to paste from remote; peer session v3-0-0-work owns the nsec restatement.
- 12:05 Upstream PR #1108 (Codex capture floor, ask 9) merged 04faebf; issue #1107 closed; v0.103.1 release PR #1121 open. Asks table row 9 and section updated: shipped on main; 0.163.0 verified, Cursor and contained-provider per-seat refusals covered, not to be filed. Codex follow-up prefers v0.103.1 over the 0.147.0 prefix once out. Asks 12 and 13 unaffected. Reported by peer session v3-0-0-work, verified with gh.

## S6 run `s6-claude-20261022`

- 06:22 Run start. Window reference 2026-10-21 00:05:00 IDT (first Claude agent of the spike); the 5-hour window reset was due at 05:05. Repos: /Users/omri/code/amq-noc /Users/omri/code/workspace-cli.
- Samplers write to /Users/omri/.amq-squad/spike/s6/s6-claude-20261022. Time to first usage limit is measured from this start.
- 2026-10-22 06:24 Owner start --apply on s6-noc: 0 applied again with launcher_preference ["commands"]. Journal backend tmux: the seam hardcodes Launcher "tmux" (team_launch_launchapi.go callPrepare), so the preference is never read; library launchapi/prepare.go:53 sets AMQPath from os.Executable() = amq-squad, and the tmux pane runs `amq-squad coop exec`, which does not exist. The launchapi path cannot start a seat from the embedding binary. Yesterday 08:12 diagnosis (preference) corrected. Stale journal moved to the run folder (stale-launch-state/amq-noc-meta-launch-0624).
- 06:41 Owner launched both squads with `--launch-via legacy --yes` (legacy pane driver, real amq 0.102.0 on PATH): s6-noc 06:41:3x, s6-wcli 06:42:3x; pane-check 6 panes, 0 flagged. Measured window starts 06:41. Run id s6-claude-20261022. First progress at 06:43: amq-noc 1 completed, 1 PR open.
- 07:00-07:16 S2 run on amq-noc PR #78: ruleset 25612981 (CI check `ci`, 0 reviews) created and removed after the merge; direct push refused GH013; merge_queue rule rejected 422 on the user-owned repo; gate s2-merge-01 rendered, owner line signed with a throwaway key (owner has no Buzz yet), verifier approve, envelope ok; owner first declined the merge prompt and ruled that 3.0 merges are lead rules decisions, not signed approvals; then said merge: squash d7ff91b by omriariav 07:15. S2-report.md written; plan decision log entry for 2.4.
- 07:5x Owner is installing Buzz; relay URL wss://omriariav.communities.buzz.xyz recorded in S1-owner-run.md; npub pending. Buzz AI-provider choice (claude-agent-acp) is Buzz's managed agent, unrelated to the gate path.
- 09:45 Run end for `s6-claude-20261022`: elapsed 3h23m. 3h limit at 09:45; 20/21 terminal, N4 in_progress on backend-dev-s6-noc since first claim
- 09:45-10:05 S6 ended at the 3 h box: 20/21 terminal, 19 PRs, 0 limit events, peak RSS 4.19 GB, 0 prompt stops; N4 never claimed because the amq-noc backend-dev seat stayed idle on the drain refusal (no AM_SESSION from the legacy driver; five other seats self-repaired) -> R52; preflight post: ~/.claude/settings.json changed at 06:56, unattributed (no seat touched it; Claude Code update at 06:52); squads down (panes closed, down reports failed/inspection_unavailable on the legacy path), samplers stopped, analysis in the run folder; S6-report.md results section written.
- 10:1x Final spike report written: docs/v3-spike/spike-report.md (five-criteria table, R48-R52, owner items, next step).
- 10:2x Peer rulings 1-6 on the S6 results applied: load-duration label, finding A second half (model chose its own pin), N4 as lease evidence, finding B wording and amendment 18, W5 path-check evidence, registry caps paragraph.
- 10:5x Plan revision 2.4 merged (78df597, agent plan-rev-2-4 in a worktree): merge grant with core enforcement into R30/R34/7.1-7.3, amendments 17 and 18, R51/R52 retagged, ask 9 shipped, criterion status per spike-report.md.
- 11:0x Owner npub received: npub1yv3jyq50hejhyfer9ff4vjj3revl56dm4mf0mv6wxrzrr7gfdecs8svg9g (checksum valid); the key guessed from the Buzz agent-pids filenames (npub14y9…h39v) was not his. Owner says Buzz has an iOS app and he will pair it later; the README "being wired up" note is stale or I misread it. S1 inputs complete except the one-time nsec step.
- 11:2x S1 real-key run, first attempt: enroll failed "not a valid nsec" because the option A command used unquoted printf %s\n inside sh -c, which appended a literal n (64 chars). Diagnosed with a stdin shape checker that prints only lengths and positions (spike/gateverify/nsec-shape.py); the key in 1Password is correct. Command fixed to echo; rerun pending.
- 11:4x-12:1x S1 owner run with the real key: enroll ok after the printf fix; Buzz picker could not find a bare npub -> enroll.sh presence path (renew with buzz-profile, --presence, endpoint up before the DM); g-owner-01 rejected not_in_gate_thread (answer in the main DM box, tags h and p only); g-owner-02 PASS in the thread (envelope authz-v1-d588…, 23.5 s, via serve ingress ledger). Evidence copied to docs/v3-spike/S1-evidence/realkey/ (no secret strings). Teardown done, run worktree removed. Criterion 1 PASS.
- 12:2x Reviewer ruling on the S1 lessons recorded as amendment 19 (for 2.5): profile+presence default, "reply in this thread", re-ask instead of heuristic acceptance; --no-masking rule in the report and the run sheet.
- 12:4x Aviv answered the pre-filing message on asks 1, 4, 5, 7, 8: table and a "Maintainer response" section updated (pi mint-mode adapter; wider argument forms; --add-dir only, confirm it fixes the worktree commit; notify chain declined; nostr:<eventID> label instead of a header). Owner: "lets figure it out and proceed to phase 2".
- 12:5x amq-main verified PR #1108 against Aviv's four review points: all in 04faebf; follow-up #1125 (per-seat containment refusal, Cursor documented, verified 0.163.0, Codex grammar version 2, --ask-for-approval untrusted dropped). Owner gave the phase 2 go; planner decisions 1-6 of the 2a-1 worklist taken; agent impl-2a1 started in a worktree on branch v3-2a1.
- 13:0x AMQ v0.104.0 released 07:00Z with #1108, #1125, #1137 (Codex notify receiver rewritten; likely covers asks 12/13, re-check). Docs retargeted from v0.103.1 to v0.104.0; asks 9/12/13 rows updated; plan phase 2a line and worklist section 13 carry the 2a-1 launch gap; amendment 20 (Aviv rulings) queued for 2.5.
- 13:1x Owner said "upgrade": brew upgrade amq -> amq, amq-remote, amq-owner-sign, amq-acp 0.104.0 (no endpoint was running). Agent s4-rerun-104 started in a worktree for the criterion 4 re-run on v0.104.0 (asks 12/13 check).
- 13:4x C4 re-run on AMQ v0.104.0 (agent s4-rerun-104, merged 342a40e): NOT PASSED, same upstream block; asks 12 and 13 confirmed open with new evidence (codex-tui 0.163.0 title-thread notify fires first); asks doc and spike report updated.
- 11:5x --add-dir probe (agent adddir-probe): no commit in any of four runs on codex-cli 0.163.0; workspace-write excludes every .git; ask 5 withdrawn; amendment 21 (harness commits for Codex seats). Filing batch is 1, 4, 8, 11, 12, 13, 14.
- 12:1x Seven upstream issues filed under the owner login after the peer review: #1153 (pi adapter), #1154 (argument forms), #1155 (nostr:<eventID> label), #1156 (event id escaping), #1157 (Codex capture stale), #1158 (Claude record backend), #1159 (launchapi AMQPath). Drafts in docs/v3-upstream-issues/.
- 12:3x Owner decision: seat cap default 12, configurable (grants.yaml), usage-limit signal is the stop; amendment 22.
- 12:3x Owner decision: the 18 S6 draft PRs stay open as the 2b merge-train test set.
- 12:5x History rewrite (owner decision): git filter-repo --replace-text removed the 1Password environment id from the two docs commits; v3 ec6aaee -> f8af8a2, v3-2a1 f0e8b94 -> 180f2d8; no commit or blob on any branch carries the id; origin remote re-added; nothing pushed. Commit hashes quoted in this log before this line are pre-rewrite. Eleven stale spike worktrees and their branches removed beforehand.
- 13:0x Owner posted the filing note to Aviv in the Slack thread (asks 1153-1159 listed, notify chain and --add-dir not filed). Waiting for Aviv on which issues he takes himself before briefing amq-main.
- 12:5x Owner: do not overspend Fable; be precise on models and efforts. Roster from now: Fable 5.1 high = this session only (planning, rulings, merges, review of agent output); implementers Opus 5.5 high; mechanical probes and drafts Sonnet 5.5 medium; second-opinion review Codex. The Fable 2a-1 implementer (5 cuts, one 429 stop) was stopped at 656f613 with cut 6 partial in the worktree; an Opus implementer continues from the execution log in the same worktree. Earlier agents today (plan 2.4, S4 re-run, issue drafts, add-dir probe) ran on Fable by inheritance; recorded as overspend.
- 14:4x 2a-1 checkpoint 1 (agent impl-2a1-opus3, Opus high): 7 cuts on v3-2a1 at 5a7742f, 239 files, -55,012/+1,598 lines, 88,075 non-test lines under internal/ (from 110,929), 2,354 tests listed (from 3,024), gates green; deferred: visibility.go (C2), preview_flags.go, simple_start_brief_draft.go, lead_identity.go (2a-2), wizard/routing.go; windows build failure pre-existing. Rulings: lead_identity deferred; re-audit test sweeps of cuts 1-5 before the final report; merge once at the end.
- 14:5x Reviewer rebuilt 5a7742f cleanly, all gates green; rulings forwarded to the implementer (test re-audit as a table, routing.go deletes with the wizard package unless imported, holding-file debit as a manifest field, no WIP commits); roster rule and the three lost implementer starts recorded in the plan (section 13 roster) as R12 process evidence.
- 16:0x 2a-1 merged into v3 as 7b8c8f1 (merge commit, 24 commits on v3-2a1, final head a5e741c): 260 files, -80,805/+5,151; internal/ non-test 72,972; tests 2,027; both reviewers rebuilt the end state. Deferred list, coverage debit and holding debit in docs/v3-spike/2a1-execution.md. Next: plan 2.5 (amendments 19-22 + 2a-1 result), then 2a-2.
- 16:1x Plan revision 2.5 merged (4531a8f, Opus drafter plan-rev-2-5): amendments 19-22 and the 2a-1 result integrated in place, 47 tags; two 2b design items named (harness commit step shape; how the notify fan-out script becomes the Codex notify command). Line-count note: the plan phase 2a line says 72,972 (measured after the closing commits), the execution log says 72,982 (before them).
- 2026-10-23 07:xx Aviv acted on the filed issues within a day: #1154 closed (PR 1175), #1156 closed (PR 1170, v0.106.0), #1157 and #1158 closed (PR 1174, v0.106.1), #1155 part 1 shipped (PR 1168, v0.106.0; part 2 = #1178), #1159 has open PR 1186, #1153 held behind #1154 and a pi probe. Local amq still 0.104.0; C4 re-run on v0.106.1 proposed to the owner. 2a-2 agent resumed after an Opus rate-limit stop mid-commit 4.
- 2026-10-23 07:xx Owner "upgrade": amq, amq-remote, amq-owner-sign, amq-acp 0.106.1. Agent s4-recheck-106 (Sonnet medium) started for the criterion 4 re-check. 2a-2 commit 4 (f032c02): status, runtime, liveness, board, rest of C1; packages runtimeaction, runtimecontrol, liveidentity, tmuxpane deleted; 54,627 lines, holding 459, tests 1,334.
- 2026-10-23 08:xx Owner reminder: Fable session reviews and plans only; coding and execution agents are Opus 5.5 or Sonnet 5.5 at medium effort. Standing rule from here; impl-2a2 (Opus high, already running) finishes as started.
- 2026-10-23 08:xx C4 re-check on AMQ 0.106.1 (agent s4-recheck-106, Sonnet medium, merged ce762cd): PASS. Apply exit 0, Codex conversation pending then ready with the real thread id, title-thread notify did not bind, forward ran, Claude record backend tmux, resume applied once on the same thread. All five spike criteria now have a pass on the mechanism; only the Codex column of C5 and the pi adapter (#1153) remain upstream-dependent.
- 2026-10-23 09:xx 2a-2 halfway checkpoint at a0d07d5 verified by the reviewer: 54,496 non-test lines, 1,345 tests, manifest-check 52,065 excluding 2,431 deferred-replace, holding 459. Rulings forwarded: 3,700-line overage budget with a running line in the log; liveness.go target raised to the measured Inspect floor with evidence; status.go 55-line cut or budget charge; helper branches c14/c27/c23 integrated serially with the full gate each, no more helper agents without asking; seven over-target rows watched at the end.
- 2026-10-23 1x:xx 2a-2 merged into v3 as 4a0ad3db (31 commits, final head 242c553d): internal/ 42,992 lines, holding 0, tests 1,115; band gate NOT MET by decision (40,561 vs 36,410 top; like for like 6.0 percent over own targets); ledger in three classes carried to 2b; thirteen lead rulings in the log. Both reviewers rebuilt the end state. Next: 2a-3 (the three verbs).
- 2026-10-24 morning: Aviv closed #1153 with PR 1188 (pi seats start and resume through amq launch; on main after v0.107.0); #1154 released in v0.106.2; v0.107.0 carries #1176 (Codex seat AMQ identity in shell commands) and doorbell holds for trust/sign-in prompts. Six of seven asks shipped; #1159 PR 1186 still open. Amendment 23 queued for 2.6. Local amq 0.106.1. 2a-3 agent resumed after an Opus rate-limit stop with the reviewer six redirects.
- 2026-10-24 Owner "upgrade": amq binaries to 0.107.0 (no endpoint running).
- 2026-10-24 Owner: amq-noc to become a command center like Codex Agent command center (screenshot saved under docs/v3-research/); amendment 24 queued for 2.6 as the phase 4 amq-noc brief input.
- 2026-10-24 Owner: the amq-noc command center replaces the current board as the default (amendment 24 closed). 2a-3 final head e691fcd0 verified on all gates; awaiting the reviewer go to merge.
- 2026-10-24 Owner: remove the old amq-noc board view; the command center is the only view (amendment 24 updated).
- 2026-10-24 2a-3 merged into v3 (merge commit after 5099e994): three hook verbs, 69-row guard matrix with 13 review fixes, ledger; phase 2a CLOSED at 44,496 lines, 1,131 tests. Next: 2b/3/4 briefs for the reviewer and the owner.
- 2026-10-24 Phase 2b/3/4 briefs drafted (Opus medium agent briefs-2b34) and merged as docs/v3-phase2-briefs.md (12a472d3) with the lead rulings on the drafter 14 points appended; first wave proposed 2b-1, 3-1, then 2b-2 A and 2b-3; reviewer pass before any implementer starts.
- 2026-10-24 Plan revision 2.6 merged (544a296c, Sonnet drafter): amendments 23 and 24, phase 2a close, briefs named as the 2b/3/4 execution document, 16.6 corrections; roster paragraph aligned with the 10-23 effort rule and the #1154 sentence updated. Reviewer edits E1-E8 and rulings being applied to the briefs by a Sonnet agent.
- 2026-10-24 Owner "go" for wave 1: impl-2b-1 (Opus high, branch v3-2b-1) and impl-3-1 (Opus medium, branch v3-3-1) started from docs/v3-phase2-briefs.md at f63f2610; 2b-2 A and 2b-3 join after one hour without a limit stop; cap four.
- 2026-10-24 10:1x Owner: "you have my approval to keep going until the very end" (standing go for waves, merges and revisions; outward actions still his: no push, no real merge on his repos, no issue filing, no messages sent). One-hour check scheduled for 11:13 to add 2b-2 A and 2b-3.
- 2026-10-24 Standing approval, two limits added (reviewer): the four typed gates of 7.1 stay owner-signed whatever the approval says (a signature, not a message); the owner-present acceptance runs (3-1 acceptance 9 with 4-5 acceptance 4, one session) still need him at the keyboard.
- 2026-10-24 Owner: the v3-0-0-work Fable session must also be planner/reviewer only ("we are bleeding tokens"). Relayed: no Fable rebuilds or Codex-driving; verification by a Sonnet agent or by the lead re-verification already done at every merge; Fable reads checkpoint summaries and ruling-deciding diffs only. Both lead sessions bound.
- 2026-10-24 11:1x One-hour check: no limit stop (3-1 finished at 45ad7409, 2b-1 at commit 4). Second pair started: impl-2b-2 milestone A (Opus medium, v3-2b-2) and impl-2b-3 (Opus medium, v3-2b-3). Four is the cap; three running now.
- 2026-10-24 Brief 3-1 merged into v3 as beabeadc (11 commits, final head ce42533b): three Codex review rounds (7 findings; 4 partial + 1 new; E partial), all closed with 18 review rows; 9/10 acceptance, item 9 pending the owner session with 4-5. 2b-3 halfway at 4ae925b2 (doctor 564/600, probe runner + TTFT at 4e05cc1b). 2b-1 on its scheduler fix after pass 2; 2b-2 A at seat-exec.
- 2026-10-24 Brief 2b-1 merged into v3 as 606b63a6 (nine commits, rebased head 32af0217): three review rounds, ten findings with test rows; band MET 35,807. 2b-2 A commit 4: launch compiler, live tmux launch passed with amq 0.107.0 and fake providers; pi refused until the adapter release. 2b-3 in its ten-finding fix. Wave 2 starting: 2b-5 (squad.yaml) and 4-1 (pi package), both Opus medium; cap four.
- 2026-10-24 Amendment 25 queued for 2.7: full runtime re-hash at every launch stands (registry review findings 3-5), plan 4.1 corrected, S3 43.5 ms kept as the no-re-hash cost, 4-1 acceptance 2 as two numbers; amendment 24 wording ("new" runs task add) folded in.
- 2026-10-24 Two process rules (reviewer, after one unreproduced 3-FAIL run on 2b-2 at 5f7aeb0e during a live seat session): every gate run keeps its full go test output under the brief evidence directory (a FAIL count is not evidence); no gate run overlaps a live seat session on the host. Lead -count=3 run on seatexec, seatlaunch, launch, cli at e0f0906d: clean, log kept as docs/v3-spike/2b2-evidence/2b2-count3.log.

## Phase 5 test-hygiene list
- 2026-10-25 internal/mergetrain under -race takes 940 s against 60 to 125 s without (the 3-2 merge gate hit go test's 10 m default; the package passed alone at 939 s). Cause: the fake gh re-executes the race-instrumented test binary per gh call and runs real git each time. Fix: keep about three subprocess rows for gh's arguments and JSON parsing, run the rest through an in-process fake Forge or a swapped command runner over the same bare-repo state, t.Parallel where fixtures are separate; target under 2 minutes, no row weakened; first step `go test -race -v -json` per-test times.
- 2026-10-25 internal/seatlaunch TestExpiredTokenIsRefused failed once in TempDir cleanup ("directory not empty") with no assertion failing, in milestone B's first canary-fix gate run; passed in the same run's race step and three times alone. A cleanup race; find the late writer and close it.
- 2026-10-25 Phase 2 closed with every merged head gated on a quiet host; the lead's merge gate now also scans the whole diff for real credential shapes (the six patterns of redact.RealCredentialShapes), and a canary that matches a real shape fails it.
- 2026-10-25 The three items above are fixed and merged (186fe04c, branch v3-5-hygiene, Codex narrow pass plus two reviewer additions, lead gate on the merge result): the PTY test derives its deadline from a measured warm-up through the pane (tmux -f /dev/null); the seatlaunch proof window is closed through t.Cleanup and the proof clock moves at the judgeHook seam (4,800 of 4,800 runs); mergetrain's fake gh runs in process behind an unexported GH.runner seam with three subprocess rows keeping the real exec path, 944 s to 48 s under -race. Log docs/v3-spike/5-hygiene-execution.md.
- 2026-10-25 The hung-guard flake (internal/cli TestDecisionAdapterKillsAHungGuardGroup) fixed and merged as c15ac65b (branch v3-5-hygiene2, two commits, reviewer read of the test diff, lead gate on the merge result): the stand-in publishes its sleeper's pid by rename as the readiness signal, the test measures the stand-in's readiness once and sets the watchdog to the larger of 1 s and ten times it, and waits for the pid file behind a 30 s hang guard; production timeout unchanged; revert check 2 of 20 failing before under 160 busy loops, 20 of 20 after. Recorded caveat: a load spike beyond that margin between the measurement and the run still fails the row, through the hang guard. The last merge before the tag; the two seatlaunch TempDir-cleanup flakes stay on the 3.0.x list. Evidence docs/v3-spike/5-hygiene-evidence/hygiene2-*.txt and gate-hygiene2.txt.
- 2026-10-25 Verifier slots: a waiting slot row left by a crash persists until the next step; status marks a waiting row older than the wait bound as stale (verifier Codex pass).
- 2026-10-25 From the verifier branch's gate runs (not its code): internal/cli TestDecisionAdapterKillsAHungGuardGroup failed once under load (the stand-in had not written the sleeper's pid before the 1 s watchdog); internal/seatlaunch TestAmbiguousRequestIsAbandonedOnlyWhenNoPaneExists failed once on a TempDir cleanup race; both pass alone and in the gates. Keep the failing full-run output before any fix.

- 2026-10-24 `TestRealPTYWaitPostureRefusesNamedGateBeforeTimeout` (internal/cli): a 9 s real-PTY deadline ran out once under full-suite load during the 2b-5 fix gate and passed alone (17.7 s) and on rerun; output kept as `docs/v3-spike/2b5-evidence/fix2-run1-pty-flake-go-test.txt`. Fix shape (reviewer): a deadline scaled by a load factor or a serial test group, not a longer constant.

## Phase 5 list (hardening)
- 2026-10-25 The Buzz-signed seat clear merged as 4d154b8e (branch v3-5-clear; 25 reverting rules; the live phone row is owner-run.md item 9). Amendment 28's clear addendum holds the rules.
- 2026-10-25 Verifier follow-up row after the v3-5-verifier merge (reviewer): the exits queue entry for a running commit step records the verifier child's pgid and start time; on adoption the new scheduler kills that group when the start time matches and re-runs the verifier once on the same sealed snapshot with interrupted_reruns=1 inside the sealed bytes; a second interruption defers to the lead as today. This converts the orphaned-verifier limit after a scheduler death into a handled case on unix; the deferral as merged is fail-closed and stays until then.
- 2026-10-25 The verdict producer merged as 38eafc83 (branch v3-5-verifier): seat commit runs the configured class verifier in the shadow worktree under the seat-class environment and records a sealed verdict; the commit step runs off the scheduler tick; the unclean marker; whole-buffer and one-line YAML redaction; write-side ledger redaction. Six Codex passes. Follow-up rows stay: orphan-verifier adoption by pgid and start time, the stale waiting slot row, two flakes.

- 2026-10-24 Runtime from a read-only mounted image (hdiutil attach -readonly): closes the amendment 25 verify-to-exec window (about 295 ms, same UID), because a writer must detach the volume and the path then fails verification. Source: 4-1 second Codex pass.
- 2026-10-24 Codex re-pass of the heads merged under amendment 29 (2b-2, 4-3, 4-5 narrow items) after the Codex limit resets on 2026-10-31; findings become fix commits before the 3.0 tag. Superseded the same day: see the entry below (moved to now).

## Phase 5 release-prep list
- 2026-10-25 Release prep rows merged as 0243b631 (branch v3-5-relprep, Codex narrow pass plus a row-2 fix round, lead gate on the merge result; size of 3.0 71,982): pin_changed_since_probe through the staged record's probed digest; doctor's "pinned claude is behind the installed claude" with the fsanchor-resolved installed chain; the ubuntu-latest claude-hooks-linux CI job under dash; the hooks-API re-check against Claude Code 2.1.310 (nothing the plugin relies on broke; the deny-only hook rule follows from it, plan 4.3); the Claude plugin at 3.0.0 with the drift test. The release commit still carries: plugins/codex/.codex-plugin/plugin.json, fourteen SKILL.md version lines, README.md:89,96 and README.html:405,409, scripts/check-release-version.py's /v2 install paths; optional: pin the ci.yml actions to commit SHAs.
- 2026-10-25 squad.yaml final form and migrate merged as bee6ffd5 (branch v3-5-migrate). The merge gate's race step hit the known internal/cli hung-guard flake once (TestDecisionAdapterKillsAHungGuardGroup, "no sleeper pid"); the package passed alone under race on the merge result (87 s), kept as race-cli.log; the flake stays on the hygiene list.
- 2026-10-25 `amq-squad uninstall` merged as 889c5cd6 (branch v3-5-uninstall; U1 to U7 with the narrow-pass rulings; 54 reverting rules). The last phase 5 code merge; the release commit (v3-5-release) follows, then the owner-signed release gate.
- 2026-10-25 The 3.0.0 release commit merged as d56d7a0c (branch v3-5-release). The merge gate's full-suite run hit the hung-guard flake once more (TestDecisionAdapterKillsAHungGuardGroup); internal/cli passed alone on the merge result (36 s, test-cli.log). The flake's fix is in progress on v3-5-hygiene2 (test synchronisation, not the production timeout) and merges before the tag. Phase 5's code side is complete at this merge; the tag waits on the owner-signed release gate.
- 2026-10-25 Object-store purge after the milestone B exposure (owner session item 8, the owner at the keyboard): reflog expire and gc of the shared object store run this morning; the two exposed values found in 0 blobs afterwards. History scan over every object reachable from v3 before the first push (reviewer's push-order rule; lead): 18,873 objects; 0 hits for the two milestone B values and for the 1Password environment id of the earlier filter-repo rewrite (compared by sha256 against the reviewer's record); the pre-rewrite commits ec6aaee and f0e8b94 absent from the repository; 3 shape hits, all the CANARY-labelled fake provider key in three historical blobs of internal/seatexec/fix1b_test.go (milestone B fix rounds, replaced at 8fbfdd56). Reviewer decision: no rewrite; a push-protection hit on the first push for a Google API key pattern is that canary, bypassed with "used in tests"; any other hit stops the push. Evidence docs/v3-spike/5-release-evidence/history-scan.txt (no values).
- 2026-10-28 Brief 5-amq-A merged as fb0363c2 (AMQ v0.116.2, pi seats through the upstream adapter, the Codex pins at launch, one CI floor). Fixed from the passes: a cloud pi seat lost its declared credential on the handoff (env_needs through --env-need); the shim temp file through atomicfile; exact 0700 with repair, special bits included; the record names the shim and the agent dir; the O_NOFOLLOW descriptor read; a FIFO at the shim path refuses instead of blocking. Two real-AMQ subtests skipped with the cause named (a v2 fixture squad.yaml has rejected since 2b-5), ported in 5-amq-C. Execution doc docs/v3-spike/5-amq-execution.md.
- 2026-10-28 Brief 5-amq-B merged as 5f1a8261 (the Buzz share on the mailbox adapter). Fixed from the passes: routing forwarded the AMQ body without a fetch (now the signed event only); the deletion rule by target and signer with the hidden-target pass; the gate lock bounded by the verification context; attention rows in the status document; durable routed-event dedup; redacted forwards; routes marked after the forward. Upstream ask 18 (queue on mailbox targets) drafted for the owner. Execution doc part B.
- 2026-10-28 Brief 5-amq-C merged as 25fa922e (the scheduler stand-in deleted; the real-AMQ contract subtests ported and running unskipped against v0.116.2). The async-close path without an implementer is a 3.0.x follow-up. The AMQ bump is complete: the tag target is the v3 tip after the docs and the scan delta.
- 2026-10-29 Owner session, first actions (the owner at the keyboard for the decisions, the lead executing on his "do the rest, I approved"): the owner confirmed both milestone B credentials rotated ("rotated"); AMQ v0.116.2 installed as amq, amq-remote and amq-owner-sign built from the module with the release version string (`go build -trimpath -ldflags "-X main.version=0.116.2"` from the module cache into ~/go/bin; a plain `go install` leaves the two helpers reporting `dev`, which doctor's floor row refuses; brew's 0.107.0 unlinked; doctor's three floor rows pass); upstream was at v0.119.0 by then, with the production changes since v0.116.2 on the attach path, the Desktop approval code, Claude's hook and submit paths and the launch lease holder, none on the mailbox adapter, strict shares, owner-sign or launchapi; v3 pushed to origin for the first time (new branch, origin/main not an ancestor; push protection did not fire on the canary blobs); the tag ruleset "Protect release tags" created on refs/tags/v* with update and deletion rules and no bypass actors (creation left open for the release flow's own tag). Doctor at this build still fails `amq version` and `amq ops` outside an AMQ root (a v2 row that wants a root) and `pi runtime` because a plain `go build` embeds no runtime (the release build does).
- 2026-10-29 Owner session, continued: the registry written (`doctor --write-registry`, the embedded-runtime build from scripts/build-pi-runtime.sh, the stale pi runtime removed), the claude pin as a hard link at ~/.amq-squad/bin/claude to the versioned 2.1.312 binary and the gh pin, both green in doctor; /tmp/jiti removed; upstream ask 18 filed as #1304; the CI-only rulesets "CI-only main" created on amq-noc (check `ci`, app 15368) and workspace-cli (check `build (1.25)`, app 15368): pull_request, required_status_checks app-bound and strict, non_fast_forward, deletion, no bypass actors; the owner's grants file is his to write (the lead's write was refused by the session's permission classifier; the content is in the report). S6 dry run pass 5 against the new rulesets (grants-cap300.yaml, the 3-2 requests, scratch roots): protection met on both repositories; amq-noc 9 of 9 planned, batch digest unchanged from pass 4; workspace-cli 4 planned (216, 218, 223, 224) and 5 refused exactly as 3-2 predicted (217, 220, 222 ci_green; 221 ci_green and size_cap 315 over 300; 219 task_scope), digest unchanged from pass 4; about 35 s and 69 gh calls per repository; a classic protection on workspace-cli still allows direct push, noted by the train and overridden by the ruleset. The first CI run on v3 (40945638948): the Linux hook job green; make ci red on three Linux-only failures (two decision-socket dials over sun_path, TestRemoteAttachOnce's start), the pinned lane red because the runner has no claude for the nested-worktree proof; brief 5-ci-D before the tag, reviewer rulings recorded in its brief. Evidence docs/v3-spike/3-2-evidence/s6-dry-run/pass5-*.
- 2026-10-29 Brief 5-ci-D merged as 23d8a9e4 (the decision socket dialable at any root depth; the seeded-pid fixture; probe stubs for the real-AMQ lane). Test-hygiene list gains TestOwnerScriptDryRun's 20 s thread-answer wait (failed twice in full-suite gates under load, 3 of 3 alone). Owner-run.md fixes from the live run: the 0.3 commands need bash (zsh does not split the flag string) and /private/tmp (the fleet registry refuses the /tmp symlink). The owner's Buzz enrollment completed at 20:40Z (8 grants, mailbox adapter, DM channel bound; the key came from a 1Password Environment through `op run --no-masking`, the first attempt named the public key and the signer refused it).

- 2026-10-24 Bump the Claude plugin version (plugins/claude, 2.31.0 today): Claude Code caches plugins by version, so the 3.0 hooks need a new version to install over 2.31. Source: 4-2.
- 2026-10-24 The Claude hooks API facts the plugin relies on (hooks.json shape, CLAUDE_PLUGIN_ROOT, hookSpecificOutput fields, exit 2 behavior) were checked against the installed Claude Code 2.1.310, not repo docs; re-check against the release's Claude Code version. Source: 4-2.
- 2026-10-24 Codex re-pass of the heads merged under amendment 29 before the 3.0 tag. Moved to now: Codex returned the same day; the re-passes run on 2b-2, 4-2, 4-3 and 4-5 as merged or rebased.
- 2026-10-24 Codex re-pass of the 2b-2 fix merge (dc7eedab: fixes 3b, 3c, the proof-window wait) as agreed at its merge; findings become fix commits before the 3.0 tag. Covered 2026-10-25 by the 2b-2 A re-pass at the tip (seatexec, seatlaunch, launch as they stand at 1bc8c680; six findings, three P1 and three P2, on the phase 5 fix branch); no second pass over the same code.
- 2026-10-24 Codex re-pass of the 2b-4 merge (d04932d1: fix 5's head record, observed-then-reset clearance, chained decisions) as agreed at its merge; findings become fix commits before the 3.0 tag.
- 2026-10-25 Codex re-pass of the 3-2 merge (2756d88d: fix 4's binding in the task store, the pinned GraphQL rebase, the draft read-back) as agreed at its merge; findings become fix commits before the 3.0 tag.
- 2026-10-25 Re-passes of d04932d1 (routing) and 2756d88d (mergetrain, releaseflow, ledger) done: 16 findings plus the verifier class switch and five coverage rows, 61 reverting rules, merged as 53c348f1 (branch v3-5-repass-routing, three commits, Codex narrow pass, lead gate on the merge result; size of 3.0 69,697). Hygiene list gains internal/seatlaunch TestProofConnectionsAreBounded (one failure in a full run, passes alone and in the gate; the failing full-run output is kept in the hygiene evidence directory before any fix, since a flake in a bound test can also be the bound not holding under load).
- 2026-10-25 Codex re-pass of the milestone B merge (84f1524b: the commit step, the decision socket, the exits queue, the pins) as agreed at its merge; findings become fix commits before the 3.0 tag.
- 2026-10-25 Re-passes of 2b-2 A (covering dc7eedab), 4-2, 4-3, 4-5 and 84f1524b done: 25 findings plus two narrow-pass rounds (the pinned claude as the seat's executable, the probe's isolation, the Go-owned hook watchdog, the sealchain extraction, the exact-name count exemption), 92 reverting rules, merged as 9e8b0921 (branch v3-5-repass-2b2, seven commits, lead gate on the merge result; size of 3.0 71,770). All eight phase 5 re-passes are done.
- 2026-10-24 3-2 liveness check at the S6 dry run and the first real run (lead's final read of fix 4): the train rebases through GitHub's updatePullRequestBranch mutation with expectedHeadOid pinned and binds the headRefOid it returns; GitHub's REST twin of that call is asynchronous (202), and the GraphQL form may return the pre-update head. Every path is fail-closed (an unchanged return skips the re-bind, a later head is pr_head_moved, and the merge is pinned by sha), so the risk is liveness only: a PR that needs a rebase may never land. If the live run shows it, the rebase moves to the local form (fetch, rebase, push --force-with-lease=<expected>, bind the SHA the push reports).
