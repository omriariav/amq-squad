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
