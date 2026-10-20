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

## Rate-limit and usage events

(none yet)
