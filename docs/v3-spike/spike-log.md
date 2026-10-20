# amq-squad 3.0 phase 1 spike log

Times are local (Asia/Jerusalem). Planner and reviewer of record: the Fable
sessions. Seats are Claude Code agents (Opus 5.5, Sonnet 5.5) and Codex
agents launched from the orchestrating session; no amq-squad seats run the
spike itself.

## Day 1, 2026-10-21

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

- 00:34 S7 PASS, merged (ade6393). Manifest 284 files, 110,929 -> 33,504.
  S6 task list drafted, 25 tasks; lead reviewed with no objection.
- 00:45 S8 drafted, merged (e1d28cf). Lead wording fixes applied. Filing
  waits for the owner's morning read.
- 00:46 S4 on the Codex agent: no seat launched. The Codex agent sandbox
  denied network, writes under ~/.amq-squad and branch creation. Partial
  evidence stands: seat-exec preserves argv and execs the provider; AMQ
  0.102.0 Prepare accepts the two-seat wrapper request with model, effort and
  wrapper args in the trust digest. Rerun on Opus (high); reviewer family
  Codex. Roster deviation: Codex subagents review, Opus implements.
  Precision: the failure is a property of the Codex subagent inside this
  Claude Code harness. It says nothing about codex-cli seats launched through
  `amq launch`; S4 and S6 still launch real Codex seats with the 0.147.0
  prefix, and their ability to write and commit is a separate finding.
- 00:52 S3 on the Codex agent: same sandbox failure, nothing measured. Rerun
  on Opus (high); reviewer family Codex. Finding carried over: the pi npm
  scope moved to @earendil-works/pi-coding-agent.
- Overnight rule agreed with the lead for S6: seats open PRs, merge nothing;
  mergeability from gh pr view plus dry-run merges; owner approves after the
  fact in the morning; earliest start 05:05; needs the S4 rerun's 0.147.0
  prefix result.

- 01:10 S4 interim: amq launch Apply on 0.102.0 created both panes with the
  wrapper; the fresh Codex 0.147.0 seat stops on codex's folder-trust
  prompt. Answering it writes to ~/.codex/config.toml. No agent may write
  the owner's Codex config tonight; the Codex half of S4 is blocked by owner
  decision. Lead added risk 16 to the phase 1 plan.
- 01:15 S6 decision (lead, option A): 05:05 start, six Claude seats, 21
  tasks (N11, N12, W10, W11 held back for a Codex follow-up after the owner
  pre-trusts amq-noc and workspace-cli). PRs only, no merges. Sanity check:
  the first Claude seat must show no folder-trust prompt of its own.

- 01:40 S4 report merged (3dbd27b): PASS for the Claude seat, BLOCKED BY
  OWNER DECISION for the Codex seat. Fault table: deny blocks in 0.17 s;
  timeout, exit 1 and missing hook all fail open (5.11 s, 0.36 s, 0.44 s).
  0.154.0 Apply refuses with capture_version_unsupported. Codex reviewer
  (xhigh) started. Open: initial_input is outside the trust digest; a
  possible config.toml rewrite by codex at seat start (unproven).

- 02:05 S4 report re-merged after the Codex review (f81a772): criterion 4
  NOT PASSED, Claude seat demonstrated, Codex seat blocked; closure run
  defined. Amendments 2 corrected and 7 added (276130c).
- 02:30 S1 merged (23ad2a9): VERIFIER READY, REAL-KEY RUN PENDING.
  Synthetic round trip 812 ms median on nos.lol; local-relay dry run of
  realkey-run.sh PASS; 14 tests plus 21 subtests green with -race. Step 6:
  only the body key reads the DM group; interim until ask 8. Codex reviewer
  started. Owner items: npub, relay URL, amq-owner-sign session (nsec typed
  hidden), DM channel id, one approve typed on the phone.

- 02:50 S5 merged (84349d9): PASS, all three parts; 16.3 does not narrow.
  qwen3.6:27b 3/3 on the R12 probe; qwen2.5:32b 2/3 with one false success;
  gemma3:4b no tool calls. Hold seam works and fails closed. Lead loop
  converged twice; one task dropped by drain without a lease (caught by the
  lead). Package scope is @earendil-works/pi-coding-agent. Codex reviewer
  started.
- 03:00 S3 merged (240ce73): PASS. Three packagings start pi with nothing on
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

## Rate-limit and usage events

(none yet)
