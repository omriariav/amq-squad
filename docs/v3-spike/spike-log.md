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

## Rate-limit and usage events

(none yet)
