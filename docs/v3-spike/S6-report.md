# S6 load test report: Claude-only, 21 tasks

Status: SKELETON. The operator fills every `TBD` from `~/.amq-squad/spike/s6/<run-id>/five-numbers.txt`
(`spike/loadtest/analysis.sh`). Nothing below is a result until the TBD is replaced by a measured value.

- Run id: TBD
- Start / end (local): TBD / TBD, elapsed TBD
- Why the run ended: TBD (all 21 tasks terminal / 3 hours elapsed / usage limit / owner stop)
- Shape: 2 repositories (amq-noc, workspace-cli), 2 squads, 6 Claude seats (per squad: 1 lead, 2 implementers),
  Sonnet 5.5 at medium effort (`claude-sonnet-5-5`, `--effort medium`; the v2.31 role catalog did not force another model), 21 tasks,
  v2.31.0 binary, amq 0.102.0, launch API on tmux. Seats open draft PRs under the owner's GitHub identity and merge nothing.
- Held back for the later Codex run: N11, N12, W10, W11. The Codex column below stays "pending".
- Window: reference 00:05 (first Claude agent of the spike), fresh window from 05:05. Start time: TBD.
- Deviations from the plan (fill in, include anything the runbook did not predict): TBD

## The five numbers

| # | Number | Claude (6 seats) | Codex |
|---|---|---|---|
| 1 | Peak RAM (sum of RSS over seat process trees, MB) and the system view (free %, swap, compressed) at the peak | TBD MB at TBD; system: TBD | pending |
| 2 | 429 events and usage-limit events (count each; `limit-summary.txt`) | 429: TBD (plain), retried: TBD; usage limit: TBD | pending |
| 3 | Wall time per task (median, min, max; table below) and total (first claim to last terminal) | median TBD, min TBD, max TBD; total TBD | pending |
| 4 | Merge conflicts (GitHub CONFLICTING; solo dry-run conflicts; cumulative dry-run conflicts; build after cumulative merge) | GitHub: TBD; solo: TBD; cumulative: TBD; build: TBD | pending |
| 5 | Time to first usage limit (minutes after run start; "none" if no limit was hit) | TBD | pending |

Registry cap proposal: the 6-seat figures above are for 6 seats. Caps for 12 seats are EXTRAPOLATED from them and must be
marked as extrapolated until a 12-seat run happens before release. Proposal: TBD (Claude), pending (Codex).

## Per-task table (Claude-only, 21 tasks)

Wall time is first claim to terminal state (done or failed). Source: `tasks.csv`, `mergeability.csv`. Fill from `per-task-table.md`.

| Task | Repo | Outcome | Seat | Wall time | PR | GitHub mergeable | Solo dry-run | Cumulative dry-run | Notes |
|---|---|---|---|---|---|---|---|---|---|
| N1 | amq-noc | TBD | TBD | TBD | TBD | TBD | TBD | TBD | |
| N2 | amq-noc | TBD | TBD | TBD | TBD | TBD | TBD | TBD | |
| N3 | amq-noc | TBD | TBD | TBD | TBD | TBD | TBD | TBD | must not touch the Makefile |
| N4 | amq-noc | TBD | TBD | TBD | TBD | TBD | TBD | TBD | |
| N5 | amq-noc | TBD | TBD | TBD | TBD | TBD | TBD | TBD | |
| N6 | amq-noc | TBD | TBD | TBD | TBD | TBD | TBD | TBD | |
| N7 | amq-noc | TBD | TBD | TBD | TBD | TBD | TBD | TBD | |
| N8 | amq-noc | TBD | TBD | TBD | TBD | TBD | TBD | TBD | |
| N9 | amq-noc | TBD | TBD | TBD | TBD | TBD | TBD | TBD | |
| N10 | amq-noc | TBD | TBD | TBD | TBD | TBD | TBD | TBD | |
| N13 | amq-noc | TBD | TBD | TBD | TBD | TBD | TBD | TBD | |
| W1 | workspace-cli | TBD | TBD | TBD | TBD | TBD | TBD | TBD | may fail; reason recorded |
| W2 | workspace-cli | TBD | TBD | TBD | TBD | TBD | TBD | TBD | |
| W3 | workspace-cli | TBD | TBD | TBD | TBD | TBD | TBD | TBD | conflicts with W7 (sheets files) |
| W4 | workspace-cli | TBD | TBD | TBD | TBD | TBD | TBD | TBD | |
| W5 | workspace-cli | TBD | TBD | TBD | TBD | TBD | TBD | TBD | file choice recorded in PR body: TBD |
| W6 | workspace-cli | TBD | TBD | TBD | TBD | TBD | TBD | TBD | |
| W7 | workspace-cli | TBD | TBD | TBD | TBD | TBD | TBD | TBD | conflicts with W3 |
| W8 | workspace-cli | TBD | TBD | TBD | TBD | TBD | TBD | TBD | |
| W9 | workspace-cli | TBD | TBD | TBD | TBD | TBD | TBD | TBD | |
| W12 | workspace-cli | TBD | TBD | TBD | TBD | TBD | TBD | TBD | conflicts with W5 if W5 chose factory.go |

## Seat and platform observations

- Folder-trust sanity check (lead of s6-noc, before anything else): TBD (no prompt / prompt seen, stopped)
- Trust or permission prompts seen on any seat (never answered): TBD
- Seats that stalled, restarted, or needed the lead's gate: TBD
- Memory pressure notes (swap growth, pageouts, mem free %): TBD
- Config files changed during the run (`preflight.sh post`): TBD (expected: none)
- Process model: RSS summed over trees double counts shared pages, so the peak is an upper bound: TBD

## Raw data

`~/.amq-squad/spike/s6/<run-id>/`: `ram.csv`, `ram-seats.csv`, `limit-events.csv`, `limit-summary.txt`, `tasks.csv`,
`tasks-report.txt`, `mergeability.csv`, `mergeability.txt`, `five-numbers.txt`, `per-task-table.md`, `pane-check.log`.
Copies committed under `docs/v3-spike/S6-data/` on this branch: TBD.
