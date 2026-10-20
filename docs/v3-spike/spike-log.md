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

## Rate-limit and usage events

(none yet)
