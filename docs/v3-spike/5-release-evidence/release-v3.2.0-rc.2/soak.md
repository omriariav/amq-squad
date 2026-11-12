# The rc.2 soak's record (2026-11-11 to 2026-11-12)

The soak is the run of the squad's own session v31 on the installed v3.2.0-rc.2 build after the rc.2 cut. This note records it against the five conditions R1 to R5 of the soak's plan, whose words are: "final is not proposed until all five hold".

- Window: from the lead's restart on the installed rc.2 build to the merge of #982. The first decision at or after 2026-11-11T13:53:57Z is line 21544 of the scheduler's decision log, `ledger_layout_migrated` at 2026-11-11T13:53:57.611166Z. #982 merged at 2026-11-12T08:18:29Z (GitHub's `mergedAt`).
- The decision log was copied once at 2026-11-12T08:25:19Z: 21640 lines, 4566220 bytes, sha256 782b49a7833fb9367e5c1893d9865dcaad66968f670eca686f0e366692c00788. Its last line is `pr_bound` for t176 at 2026-11-12T07:58:48.002711Z. decisions-soak.jsonl is lines 21544 to 21640 of that copy, 97 lines. Every decision count below is a count over those 97 lines unless the sentence says "the whole log".
- The log is in time order: 0 lines of the copy carry an instant earlier than the line before them.
- Sources: decisions-soak.jsonl; soak-task-table.md (the per-task table); lead-screen-2026-11-12.txt (the lead's screen); the task store, read with `amq-squad task show <id> --session v31 --json`; the merge train's own files per run (binary.txt, dry.json, real.json, dry.err, real.err), which stay in the lead session's scratch; GitHub's values for each PR (`gh pr view`); README.md of this folder for the restart; the lead session's approvals record and notes.
- Where a sentence names no record in this folder and no command, it is the lead session's account, and the sentence says so.

## The five conditions

### R1

The plan's words: "R1. At least two seat PRs merged by the rc.2 train."

Met: eleven seat PRs merged by the rc.2 train. Each has a train directory whose real.json holds one PR entry with status `merged`, and GitHub reports each as MERGED into v3. PR, task and the real run's id:

- #966, t162, mt-20261111T143634Z-8c795eb4
- #967, t163, mt-20261111T154007Z-3ccc5e93
- #969, t164, mt-20261111T160555Z-5b5a3267
- #970, t165, mt-20261111T163541Z-d154788f
- #971, t166, mt-20261111T173109Z-89e01535
- #972, t167, mt-20261111T182056Z-b851ed61
- #975, t170, mt-20261111T203249Z-575585aa
- #976, t171, mt-20261111T215256Z-471dfb9c
- #977, t172, mt-20261111T224329Z-aa975bff
- #980, t174, mt-20261112T042315Z-b66aeae3
- #982, t176, mt-20261112T081812Z-146d388b

The head branch of the first ten is `seat/reviewer-<task>`; the head branch of #982 is `seat/dev2-t176`.

### R2

The plan's words: "R2. The post-restart record (#965) merged and clean."

Met: #965 merged at 2026-11-11T14:28:30Z as 6242458ed96ed1d51864ab8a4f6ee90f7ab0b2f0, with parents e70c33db6e13ae637748a681b2e93376b26a32da and 97db68f21426e25d26255b4e5b6a20c0b17c84ea. That commit is an ancestor of origin/release/v3.2. By the lead session's approvals record of 2026-11-11 16:28 IST (14:28Z): CI run 43037600928 on 97db68f2 passed its four checks with no rerun, the redactor's `-check` exited 0, `git diff --check` was clean, the locked docs-html-check exited 0, and the second Codex pass reported no findings.

### R3

The plan's words: "R3. No new defect in the four classes: ledger_tampered on a sound chain, a lease lost under steady activity, a control byte on the screen, a train run ended by an escape byte."

Met, by these counts.

1. `ledger_tampered` on a sound chain. Decisions whose kind or reason contains `ledger_tampered`: 0 of 97. The same search over the whole log matches 1951 lines (1949 `seat_prepare_failed`, 2 `exit_refused`), each before line 21544.
2. A lease lost under steady activity. Decisions of kind `lease_expired`: 0 of 97. The whole log holds 11, each before line 21544. Decisions whose kind or reason contains `expired`: 0 of 97. Each of the fifteen tasks t162 to t176 has one attempt in the task store, with outcome `completed`.
3. A control byte on the screen. Captures of the lead's pane on two days: two on the first, one on the second.
   - 2026-11-11: lead-first-screen.txt (13:54:07Z) and lead-screen-30s.txt (13:54:25Z), both in this folder since #965: 1428 bytes each, 0 ESC bytes, 0 other C0 bytes apart from the line feed, 0 DEL bytes, 0 C1 characters.
   - 2026-11-12: lead-screen-2026-11-12.txt (07:04:11Z by the lead session's note and the file's time): 3768 bytes (3773 as captured, before the placeholder), 42 lines, 0 ESC bytes, 0 other C0 bytes apart from the line feed, 0 DEL bytes, 0 C1 characters.
   - A second file of the same pane, written at 2026-11-12T07:04:03Z and kept in the lead session's scratch (lead-screen-2026-11-12.raw.txt, 3892 bytes), holds 21 ESC bytes. Each of the 21 starts a sequence of the form ESC `[` digits `m`. It holds 0 other C0 bytes apart from the line feed and 0 C1 characters. It is not proposed for this folder.
4. A train run ended by an escape byte. Eleven real runs, eleven with status `merged`. The 44 files of the eleven train directories that hold a run's output (dry.json, real.json, dry.err, real.err) hold 0 ESC bytes and 0 lines with the JSON escape `u001b`. The 22 `.err` files are 0 bytes each.

### R4

The plan's words: "R4. One task completed through the rc.2 train by a seat LAUNCHED by the rc.2 binary. This needs the owner's relaunch from the lead's pane. It is asked of him today as one line; nothing else in the soak waits for it."

Met at the merge of #982.

- The seat: dev2, pool codex:gpt-5.6-sol:high, pid 18464 (the pid is from the lead session's approvals record, read at 2026-11-12T05:32:12Z).
- Its launch: `roster_launched` at 2026-11-12T05:32:05.366312Z ("launch 36f9c35929a8ea0700347c769800365c: 3 seats in one apply") and `seat_up` for dev2 at 2026-11-12T05:32:05.375383Z, in the relaunch the owner ran (see "The relaunch of 2026-11-12"). The first line of dev2's hook ledger is at 2026-11-12T05:32:11.731209Z.
- The binary that launched it: the scheduler is pid 15228, started 2026-11-11T13:53:57.599413Z by the restart on the rc.2 build (README.md, "Step 6: the squad's own lead on the installed rc.2 build"). The decision log holds 0 `scheduler_started` and 0 `scheduler_stopped` lines after line 21547, so the same scheduler process made the launch.
- The task: t176, created 2026-11-12T07:52:45.008277Z, one attempt, holder dev2, granted 2026-11-12T07:52:45.849960Z, ended 2026-11-12T07:58:47.825405Z, outcome `completed`. The lease was claimed at 2026-11-12T07:53:13Z by the lead session's approvals record; the first line of dev2's hook ledger that carries the attempt is at 2026-11-12T07:53:13.992681Z.
- Its decisions, five lines: `dispatched` 2026-11-12T07:52:47.607713Z, `lease_renewed` 07:54:32.371777Z, `commit_running` 07:58:41.967364Z, `completed` 07:58:47.998618Z, `pr_bound` 07:58:48.002711Z ("omriariav/amq-squad pr 982 head 150120b6406b5a5f866efb8f397c34795e4c7268"), the last four on the same date.
- The PR: #982, head 150120b6406b5a5f866efb8f397c34795e4c7268, merged at 2026-11-12T08:18:29Z as cc73ada1b4aba282c500b1ddd5203c05d52879af.
- The train run: mt-20261112T081812Z-146d388b, status `merged`, after the dry run mt-20261112T081803Z-88adaa71 (status `planned`); the two digests are equal (sha256:4628a6e7197a2c6fd486c6d5c5d71cb8532ed4934c0f702033256c80fb3b9daa). The run's binary.txt: run 2026-11-12T08:18:03Z, `amq-squad v3.2.0-rc.2`, sha256 e978bdeff8e5df78631090f39f4eb8208804b7a89f97917ba4a99bb9914491df.

The thirteen tasks t162 to t174 ran on the seat reviewer, which the restart adopted and which was launched on 2026-11-10T14:06:08Z, when the installed binary was the rc.1 build (README.md, "The adopted roster"). They count for R1 and not for R4.

### R5

The plan's words, as first stated: "R5 (mine, for your word): the two commit ledgers' `ledger_layout_migrated` lines exist, one per ledger, and no `ledger_tampered` follows either. The reviewer seat's comes with t162's commit; the dev seat's needs a terra task, or the relaunched seat's first commit if dev is the seat relaunched."

The plan's words, as ruled: "R5, narrowed: the reviewer seat's commit ledger shows its one `ledger_layout_migrated` line and no `ledger_tampered` follows it. First half met: decisions line 21556, 14:05:35.731785Z, seq 454, at t162's dispatch (the dispatch's task-start entry is the next ordinary write, as P7-P8 say); recorded in #965. The soak's record carries the "none follows" half." And: "The dev seat's half is not a row."

Met up to the copy of 2026-11-12T08:25:19Z. The second half runs to the release cut, so it is to be counted again at the cut.

- The whole log holds two `ledger_layout_migrated` lines:
  - line 21544, 2026-11-11T13:53:57.611166Z, ledger_kind exits, seq 409881, "legacy exits ledger migrated to raw-bytes-v1 at link 409881";
  - line 21556, 2026-11-11T14:05:35.731785Z, ledger_kind commits, seat reviewer, seq 454, "legacy commit ledger migrated to raw-bytes-v1 at link 454".
- Decisions of kind `ledger_tampered` after line 21556: 0 of 84. Decisions in the 97 lines whose kind or reason contains `tamper`, in either case: 0 of 97.
- The dev seat: 0 `dispatched` lines name seat dev in the 97 lines, and `commits/dev.json` was last written at 2026-11-11T00:56:09Z, before the restart. Its commit ledger was not written during the soak.
- The relaunched seat dev2: `commits/dev2.json` holds two JSON documents, the record and a one-line trailer with the keys `prev`, `schema`, `seq`, `tag`, `version`; 0 `ledger_layout_migrated` lines name dev2.

## The tasks

Fifteen tasks, t162 to t176, each dispatched once: 13 `dispatched` lines for seat reviewer and 2 for seat dev2. The table is soak-task-table.md. Every time in it is UTC in 2026, written month-day and time. "renewals in the seat's ledger" counts the lines of the seat's hook ledger inside the attempt's window whose `lease_renewal` is `renewed`. A `-` under the train columns means there is no train directory for that PR.

| task | seat | granted (UTC) | ended (UTC) | minutes | renewals in the seat's ledger | PR | PR head | train run (real) | train result | merge commit |
|---|---|---|---|---|---|---|---|---|---|---|
| t162 | reviewer | 11-11 14:05:33 | 11-11 14:21:09 | 16 | 1 | #966 | 04585aff255d | mt-20261111T143634Z-8c795eb4 | merged | 8d66f50e58a5 |
| t163 | reviewer | 11-11 14:46:55 | 11-11 15:02:45 | 16 | 1 | #967 | d1b4c18db59a | mt-20261111T154007Z-3ccc5e93 | merged | 0c72f069e388 |
| t164 | reviewer | 11-11 15:40:55 | 11-11 15:51:59 | 11 | 2 | #969 | 6f8ec5b2f89f | mt-20261111T160555Z-5b5a3267 | merged | ce87665cb1c8 |
| t165 | reviewer | 11-11 15:53:41 | 11-11 16:18:11 | 25 | 3 | #970 | 660a6efd201b | mt-20261111T163541Z-d154788f | merged | bb4334e2a8db |
| t166 | reviewer | 11-11 16:47:31 | 11-11 17:10:53 | 23 | 2 | #971 | 873da94f8006 | mt-20261111T173109Z-89e01535 | merged | 6a0fe4b1742f |
| t167 | reviewer | 11-11 17:32:15 | 11-11 17:47:31 | 15 | 2 | #972 | 3876ee265401 | mt-20261111T182056Z-b851ed61 | merged | 8b86ded84b4b |
| t168 | reviewer | 11-11 17:50:05 | 11-11 18:11:07 | 21 | 7 | #973 | abafeaf440b9 | - | - | - |
| t169 | reviewer | 11-11 18:49:37 | 11-11 19:08:39 | 19 | 3 | #974 | 3f77ee2361da | - | - | - |
| t170 | reviewer | 11-11 19:37:57 | 11-11 19:44:33 | 7 | 1 | #975 | 5f109c694b09 | mt-20261111T203249Z-575585aa | merged | fe7f5b8f7ae4 |
| t171 | reviewer | 11-11 20:58:41 | 11-11 21:27:05 | 28 | 5 | #976 | cdb734228868 | mt-20261111T215256Z-471dfb9c | merged | 6dc265323928 |
| t172 | reviewer | 11-11 22:15:17 | 11-11 22:22:27 | 7 | 0 | #977 | e658efe11e72 | mt-20261111T224329Z-aa975bff | merged | 4d649b5d1d98 |
| t173 | reviewer | 11-11 23:59:57 | 11-12 00:29:09 | 29 | 3 | #979 | e58e1da54459 | - | - | - |
| t174 | reviewer | 11-12 02:58:11 | 11-12 03:23:01 | 25 | 2 | #980 | 27578be04b7a | mt-20261112T042315Z-b66aeae3 | merged | 1a3db2f13466 |
| t175 | dev2 | 11-12 06:44:03 | 11-12 07:19:57 | 36 | 27 | #981 | b1910543f07a | - | - | - |
| t176 | dev2 | 11-12 07:52:45 | 11-12 07:58:47 | 6 | 4 | #982 | 150120b6406b | mt-20261112T081812Z-146d388b | merged | cc73ada1b4ab |

Eleven rows carry a merge commit. Four do not:

- t168: #973 was closed unmerged at 2026-11-11T19:10:05Z. By the lead session's approvals record it was closed with a comment naming #974 (t169), under the plan reviewer's ruling "ROUND 2 replacing #973".
- t169: #974 was closed unmerged at 2026-11-11T21:31:24Z. By the lead session's approvals record its closing comment reads "Replaced by #976 (task t171, S1-1c round 3)". #976 merged.
- t173: #979 was closed unmerged at 2026-11-12T03:30:07Z.
- t175: #981 is open at 2026-11-12T08:35:48Z and is not merged. By the lead session's approvals record the plan reviewer ruled at 2026-11-12T07:46Z: "ROUND 2 REPLACES #981", "The lead closes #981 when round 2's PR opens. #981 merges nowhere."

t172 has four decisions, not five: 14 of the 15 tasks have one `lease_renewed` line each and t172 has 0. Its row shows 0 renewals in the ledger and an attempt of 7 minutes.

## Decisions by kind since the restart

Counted on decisions-soak.jsonl (97 lines). Kind, count, first and last time (UTC):

| kind | count | first | last |
|---|---|---|---|
| commit_running | 15 | 2026-11-11T14:21:05.889176Z | 2026-11-12T07:58:41.967364Z |
| completed | 15 | 2026-11-11T14:21:09.912627Z | 2026-11-12T07:58:47.998618Z |
| dispatched | 15 | 2026-11-11T14:05:36.252341Z | 2026-11-12T07:52:47.607713Z |
| pr_bound | 15 | 2026-11-11T14:21:09.916769Z | 2026-11-12T07:58:48.002711Z |
| lease_renewed | 14 | 2026-11-11T14:09:59.819561Z | 2026-11-12T07:54:32.371777Z |
| seat_clean_superseded | 6 | 2026-11-11T13:53:59.109212Z | 2026-11-11T13:53:59.126255Z |
| seat_up | 3 | 2026-11-12T05:32:05.369311Z | 2026-11-12T05:32:05.375383Z |
| ledger_layout_migrated | 2 | 2026-11-11T13:53:57.611166Z | 2026-11-11T14:05:35.731785Z |
| seat_adopted | 2 | 2026-11-11T13:53:57.635341Z | 2026-11-11T13:53:57.639076Z |
| seat_closed | 2 | 2026-11-12T05:32:05.354479Z | 2026-11-12T05:32:05.358430Z |
| seat_index_refreshed | 2 | 2026-11-12T05:32:01.398754Z | 2026-11-12T05:32:02.808035Z |
| decision_socket_open | 1 | 2026-11-11T13:53:59.133623Z | 2026-11-11T13:53:59.133623Z |
| roster_incomplete | 1 | 2026-11-11T13:53:59.129387Z | 2026-11-11T13:53:59.129387Z |
| roster_launched | 1 | 2026-11-12T05:32:05.366312Z | 2026-11-12T05:32:05.366312Z |
| roster_relaunch_requested | 1 | 2026-11-12T05:32:05.350080Z | 2026-11-12T05:32:05.350080Z |
| roster_reloaded | 1 | 2026-11-12T05:32:05.362538Z | 2026-11-12T05:32:05.362538Z |
| scheduler_started | 1 | 2026-11-11T13:53:57.642097Z | 2026-11-11T13:53:57.642097Z |

Seventeen kinds, 97 lines. Kinds with 0 lines in the 97, each present in the whole log before line 21544: `lease_expired`, `commit_refused`, `exit_refused`, `seat_launch_failed`, `roster_launch_failed`, `seat_prepare_failed`, `task_reset`, `blocked`.

## The merge train

Eleven PRs, one dry run and one real run each, one PR per run. Each dry run's entry has status `planned` and each real run's entry has status `merged`.

| PR | task | binary.txt run line (UTC) | dry run | real run | digests equal | merged as |
|---|---|---|---|---|---|---|
| #966 | t162 | 2026-11-11T14:36:24Z | mt-20261111T143624Z-ba9befec | mt-20261111T143634Z-8c795eb4 | no | 8d66f50e58a5d8c0635cd0d44d7dc21cdf54b488 |
| #967 | t163 | 2026-11-11T15:39:58Z | mt-20261111T153958Z-11c5de40 | mt-20261111T154007Z-3ccc5e93 | yes | 0c72f069e388ef461e6c0b341a9156c291a4d4a4 |
| #969 | t164 | 2026-11-11T16:05:46Z | mt-20261111T160546Z-fa545776 | mt-20261111T160555Z-5b5a3267 | no | ce87665cb1c86f2a335e073651d5be2eaaf27f14 |
| #970 | t165 | 2026-11-11T16:35:31Z | mt-20261111T163531Z-badf6159 | mt-20261111T163541Z-d154788f | no | bb4334e2a8db8703b8b5fd270d7c218c6f9e1bfa |
| #971 | t166 | 2026-11-11T17:30:59Z | mt-20261111T173059Z-cc96782c | mt-20261111T173109Z-89e01535 | yes | 6a0fe4b1742f5c331d8e3c87d6cf9a6c603a1e41 |
| #972 | t167 | 2026-11-11T18:20:47Z | mt-20261111T182047Z-0d1547cd | mt-20261111T182056Z-b851ed61 | yes | 8b86ded84b4b0adbac623309c331adf8037e48ac |
| #975 | t170 | 2026-11-11T20:32:40Z | mt-20261111T203241Z-d55c1c50 | mt-20261111T203249Z-575585aa | yes | fe7f5b8f7ae4f533bb2a64d2894bc28e6007d199 |
| #976 | t171 | 2026-11-11T21:52:46Z | mt-20261111T215246Z-e46206ed | mt-20261111T215256Z-471dfb9c | yes | 6dc265323928136ab9bd4993d09c0243927ca3a5 |
| #977 | t172 | 2026-11-11T22:43:20Z | mt-20261111T224321Z-28224f29 | mt-20261111T224329Z-aa975bff | yes | 4d649b5d1d98015129d1eb566b2ae5e26ff8c746 |
| #980 | t174 | 2026-11-12T04:23:07Z | mt-20261112T042308Z-c68c1668 | mt-20261112T042315Z-b66aeae3 | yes | 1a3db2f13466502a8de7948fc7e1fa07574843d9 |
| #982 | t176 | 2026-11-12T08:18:03Z | mt-20261112T081803Z-88adaa71 | mt-20261112T081812Z-146d388b | yes | cc73ada1b4aba282c500b1ddd5203c05d52879af |

The three runs whose digests differ are the three with a patch proof. In each the dry run's entry has `rebase_for_ci` true, the real run's `patch_proof` has `match` true, and the dry run's digest is the first of the real run's two `digests`:

- #966: old head 239eb6f7bbc29aef59fde6bb56313ee861ada041, new head 04585aff255dfa88ef02ae8f80ec7c21ddff5c01, old and new patch id f4e99ac7b6c4ca5a5caf4e91a4fcd4f0b8212170.
- #969: old head da0c96b42a7d4b419d5cb42d0a2d77409d10d719, new head 6f8ec5b2f89fa88802424913034398d37ce27783, old and new patch id aec174d439a595cf77d4bf5a2a994c4e00ad4c77.
- #970: old head 1ddd90a994ada60aea51f18a11ad447e54ed84ad, new head 660a6efd201bee9dc0195cc5b5b3e518ed9dccef, old and new patch id dc7fa67080539871fa094641d372b5ad46acf015.

The other eight real runs carry a null `patch_proof`.

The binary: apart from its `run` line, each of the eleven binary.txt files holds the same three lines (each distinct line occurs 11 times): the binary's path, the version line `amq-squad v3.2.0-rc.2`, and sha256 e978bdeff8e5df78631090f39f4eb8208804b7a89f97917ba4a99bb9914491df. That digest is the one README.md gives for the installed build and the tag build (under "Step 6: the squad's own lead on the installed rc.2 build", the line starting "After:").

## The relaunch of 2026-11-12

At the restart the roster came up without its third seat: `roster_incomplete` for dev2 at 2026-11-11T13:53:59.129387Z (line 21554; README.md, Things seen 4). The owner ran the relaunch from the lead's pane on 2026-11-12; the lead session's approvals record has it under "2026-11-12 07:32 IST" (05:32Z). lead-screen-2026-11-12.txt still shows the command, `amq-squad scheduler relaunch --reason "rc.2 launch path for the soak"`, and its answer, which starts "scheduler relaunch: accepted".

The ten decisions from 2026-11-12T05:31:00Z to 2026-11-12T05:34:00Z, in order (lines 21621 to 21630; every time is on 2026-11-12):

| line | time (UTC) | kind | seat | reason or pool |
|---|---|---|---|---|
| 21621 | 05:32:01.398754Z | seat_index_refreshed | dev | "seat dev's index was stale; refreshed" |
| 21622 | 05:32:02.808035Z | seat_index_refreshed | reviewer | "seat reviewer's index was stale; refreshed" |
| 21623 | 05:32:05.35008Z | roster_relaunch_requested | | "rc.2 launch path for the soak" |
| 21624 | 05:32:05.354479Z | seat_closed | dev | "roster_relaunch" |
| 21625 | 05:32:05.35843Z | seat_closed | reviewer | "roster_relaunch" |
| 21626 | 05:32:05.362538Z | roster_reloaded | | "squad.yaml compiled again at the relaunch's drain: a roster of 3 seats; added none; removed none (each closed in the drain, not launched again); at most 4 leases at once" |
| 21627 | 05:32:05.366312Z | roster_launched | | "launch 36f9c35929a8ea0700347c769800365c: 3 seats in one apply" |
| 21628 | 05:32:05.369311Z | seat_up | dev | codex:gpt-5.6-terra:medium |
| 21629 | 05:32:05.372307Z | seat_up | reviewer | codex:gpt-5.6-sol:high |
| 21630 | 05:32:05.375383Z | seat_up | dev2 | codex:gpt-5.6-sol:high |

The decision before them is t174's `pr_bound` at 2026-11-12T03:23:02.088852Z (line 21620) and the one after them is t175's `dispatched` at 2026-11-12T06:44:05.619207Z (line 21631).

The three seats, by the lead session's approvals record (read at 2026-11-12T05:32:12Z with `lsof`, `ps` and `amq-squad status`): dev pid 18451, reviewer pid 18458, dev2 pid 18464, each a codex process started about 2026-11-12T05:32:01Z; the lead (pid 15222) and the scheduler (pid 15228) were unchanged.

dev2 came up. Lines 21623 to 21640 (18 lines) hold 0 occurrences of `binding_present_without_resumable_conversation`, the code in the `roster_incomplete` line of the restart. The 97 lines hold 0 decisions whose kind contains `launch_failed`. The first task after the relaunch, t175, was dispatched to dev2.

## Things seen

### 1. The stall marker: a dialog line on the lead's screen while the lease renewed

A screen observation of 2026-11-12, recorded as seen. Cause not read.

- The lead's screen (lead-screen-2026-11-12.txt, 2026-11-12T07:04:11Z; 3768 bytes (3773 as captured, before the placeholder), 42 lines, 0 ESC, 0 other C0, 0 C1, 0 DEL) showed "! seat dev2 asked for a permission 1m45s ago this turn; if it is not moving, check its pane" and, below the rule, "dev2: seat_stalled_at_dialog: a harness dialog has been open since 2026-11-12T07:02:25Z (1m45s); the seat waits at it and AMQ holds its doorbell".
- By the lead session's note: at 2026-11-12T07:04:25Z t175's lease read renewed_at 07:03:22Z, renewals 13, last_renewal activity, so the lease was renewed after the dialog's stated start; at 2026-11-12T07:04:30Z the seat's pane (a read-only capture of pane %152) showed "Working (20m 24s)", its status line "Approve for me", and no dialog. By the same note, nothing was typed into any pane.
- dev2's hook ledger agrees on the renewal: its line seq 60, at 2026-11-12T07:03:22.430557Z, carries `lease_renewal` `renewed`, and it is the 13th such line from t175's grant.
- The decision log holds 0 lines containing `stall` or `dialog`.
- The lead session's approvals record gives the plan reviewer's ruling of 2026-11-12T07:46Z on it in these words: "not one of R3's four classes; recorded in the soak record under the stall marker item (7a, with B3) as written; no action in the soak."

### 2. An open owner question of 2026-11-05 stays on the relaunched reviewer seat's row

lead-screen-2026-11-12.txt shows the relaunched reviewer seat as "waiting: on your answer to its question". By the lead session's approvals record, status at 2026-11-12T05:32:12Z gave that seat `needs_you` with the reason `question_to_owner`. That reason is set at `internal/status/classify.go:471-473` at the v3.2.0-rc.2 tag (da109b496787ffb7b8a716b8d2d75af19e98a045), for a seat with an open owner thread. The thread is `gate/t94-cli-test-scope`, subject "APPROVAL: widen t94 for its required CLI regression rows", `from` reviewer, `to` user; its message was created at 2026-11-05T20:11:04Z (2026-11-05T20:11:04.17371Z in the message's header). The same item was on the first rc.2 status of 2026-11-11 (README.md, "The scheduler's first pass over session v31"). The thirteen tasks t162 to t174 were dispatched to the seat reviewer in that time.

### 3. The lead's screen still prints "the gate store could not be read"

lead-screen-2026-11-12.txt, line 33: "! the gate store could not be read; its error is below the line". Line 39 is the status warning for a skipped legacy record: "gate store: gate clear-0a227ca8d88dc97c65c540a4d0fa18a7 is a legacy quarantine_clear gate (a kind this build no longer carries); skipped". This is README.md's Things seen 3. The fix is #966 (t162), merged as 8d66f50e58a5d8c0635cd0d44d7dc21cdf54b488, which is not an ancestor of the v3.2.0-rc.2 tag. The installed binary is the rc.2 build (the eleven binary.txt files, the last at 2026-11-12T08:18:03Z), so the screen prints the rc.2 words.

## What this PR corrects

Three corrections ride with this record, each ruled by the plan reviewer.

1. `docs/v3-spike/320-gate-legacy-words/README.md:25` (#966's note). Two sentences were wider than the rows: "It never prints a legacy record's gate id." and "The count is independent of a gate alert or read failure." They now read: "It never prints the gate id of a legacy warning carrying the new prefix (S-legacy-3); an old-prefix line still prints it (S-old). The count is printed with a read failure (S-both) and, by the code at `pi/extensions/lib/screen.ts:347-349`, with a gate alert; no row asserted the alert case in v3.2.0 (the row S-alert, #982 on v3, added afterwards)." The cite is of 8d66f50e58a5d8c0635cd0d44d7dc21cdf54b488, this branch's tip before this PR.
2. `docs/v3-followups.md`, the entry "The AMQ pin". The rc.2 notes (`docs/v3.2.0-release-notes.md:629`, published with the tag and not edited) say AMQ v0.139.0 removed `launchapi.Inspect`, "which the seat liveness check calls". The line that names it is `internal/cli/liveness.go:209` (`var launchapiInspect = launchapi.Inspect`) and the call through it is `:247`, which names `launchapi.InspectRequestV1`, inside `launchapiSessionInspectUncached` (`:246`). `deadcode` reports that function and nine others of the file unreachable from main (measurement 3 below). The followups entry now says so, and that #969 removed the lines on v3 while this branch keeps them, where they build at the pin v0.116.2. At this PR's head the notes file is still rc.2's; the release PR for 3.2.0 puts the corrected sentence into it under "Corrections to the rc.2 notes".
3. The row the first correction names. #966's note had no row for the count beside a gate alert. That row is #982 (t176) on v3, the task that also meets R4. It is 3.2.x content for v3 and is not part of the 3.2.0 release.

## Three measurements the 3.2.0 notes cite

Each is the lead's run, kept here as the run's own record. The first two were asked by the plan reviewer at #972's merge; the third is the run correction 2 rests on.

### 1. `up` in a repository where `amq coop init` has not run

The command, run by the scratch script whose record is measure-up-noinit-run.txt: `amq-squad up --skip-install --wait 150s --json`, the v3.2.0-rc.2 binary, host amq 0.116.2, in a scratch repository with no `.amqrc` and no `.agent-mail`. From the record:

```text
start 2026-11-11T18:28:16Z
amq-squad: amq-squad v3.2.0-rc.2 at /private/tmp/upmup-codex-noinit.ZL1c3o/bin/amq-squad
amq: 0.116.2 at <home>/go/bin/amq
before up: .amqrc absent; .agent-mail absent
up exit=0
after up: .amqrc {  "root": ".agent-mail"}
doctor after up exit=0
roster decision seen=yes at 2026-11-11T18:28:22Z (after about 2 s)
end 2026-11-11T18:28:52Z
```

The run's decision log, measure-up-noinit-decisions.jsonl, holds 5 lines, in order: `session_created`, `scheduler_started`, `roster_launched`, `seat_up`, `decision_socket_open`. The first names what `up` ran: `amq coop init` and `amq session create`. So the documented route (`up`) creates the session itself. The limit the 3.2.0 notes state is of the direct route only: `lead --team` in a repository where `amq coop init` has not run creates no session. No run of that direct route is in this folder; the sentence in the notes is the plan reviewer's, from #972's gate.

### 2. A Claude seat with a token file of the right shape

The run's record is measure-claude-token-run.txt: the v3.2.0-rc.2 binary, host amq 0.116.2, a scratch HOME holding a token file with a made-up value. From the record:

```text
start 2026-11-11T18:29:30Z
amq-squad: amq-squad v3.2.0-rc.2 at /private/tmp/tokmtok-rc2-amq0116.aKQwBq/bin/amq-squad
amq: 0.116.2 at <home>/go/bin/amq
provider: <home>/.local/bin/claude 2.1.319 (Claude Code)
amq coop init exit=0
amq session create exit=0
scratch token file: mode=600 size=39; dir mode=700
doctor exit=0
roster decision seen=yes at 2026-11-11T18:29:33Z (after about 2 s)
end 2026-11-11T18:30:09Z
```

Its decision log, measure-claude-token-decisions.jsonl, as kind, stage, reason code and reason:

```text
{"kind":"scheduler_started","stage":null,"reason_code":null,"reason":"incarnation 34159ed15cca"}
{"kind":"roster_launch_failed","stage":null,"reason_code":null,"reason":"the roster launch failed at stage apply_failed, reason launch_action_required: launch_action_required"}
{"kind":"seat_launch_failed","stage":"apply_failed","reason_code":"launch_action_required","reason":"launch_action_required"}
{"kind":"scheduler_stopped","stage":null,"reason_code":null,"reason":"the roster launch failed at stage apply_failed, reason launch_action_required: launch_action_required"}
```

The launch failed at stage `apply_failed` with the reason code `launch_action_required` and no other reason text. The cause was not established. A second run of the same script against the head of #972 with AMQ 0.140.0 gave the same stage and code by the lead session's record; its files are not in this folder.

### 3. `deadcode` on this branch's tip

`deadcode ./...` (golang.org/x/tools/cmd/deadcode) in a worktree at 8d66f50e58a5d8c0635cd0d44d7dc21cdf54b488, run on 2026-11-11: exit 0, 235 lines. deadcode-liveness.txt is its ten lines for `internal/cli/liveness.go`:

```text
internal/cli/liveness.go:52:24: unreachable func: agentLiveness.Live
internal/cli/liveness.go:65:6: unreachable func: classifyAgentLivenessFromDisk
internal/cli/liveness.go:180:6: unreachable func: launchapiObservationForHandle
internal/cli/liveness.go:201:6: unreachable func: launchapiObservationIsNotable
internal/cli/liveness.go:214:6: unreachable func: launchapiSessionInspect
internal/cli/liveness.go:246:6: unreachable func: launchapiSessionInspectUncached
internal/cli/liveness.go:282:6: unreachable func: applyLaunchapiInspectionFloor
internal/cli/liveness.go:318:6: unreachable func: launchapiAppendObservationDetail
internal/cli/liveness.go:338:6: unreachable func: launchapiNoLaunchRecordDetail
internal/cli/liveness.go:351:6: unreachable func: classifyAgentLivenessForRollup
```

## The files

Beside this note, each passed through `go run ./scripts/redact-evidence` (it replaced no value, and `-check` is clean). A host path under the home directory is replaced by hand with the placeholder `<home>`; the count of replacements is beside each file that had one. After it the eight files and this note hold 0 lines with a home path.

- decisions-soak.jsonl: 97 lines, 13571 bytes, sha256 a0fd1833d3c7f6b6f7a3fbb378f0ee62df18dc3443dc9578f4059375307f3f8e. Lines 21544 to 21640 of the decision log's copy. 1 placeholder.
- soak-task-table.md: 17 lines, 2116 bytes, sha256 a077f49a4dc5d8017c51b69d629235706d4ad8334b847083e989aa9a7ba3baeb. The table under "The tasks".
- lead-screen-2026-11-12.txt: 42 lines, 3768 bytes, sha256 3c9db393b56484189ef1af00a6800076086db8556cf5d006c02bc4f317522e25. The lead's pane at 2026-11-12T07:04:11Z. 1 placeholder.
- measure-up-noinit-run.txt: 26 lines, 1462 bytes, sha256 8f7cd5f26b5791fb4ac0eece9627c92ad05bd87511584457da774a64eafe5e4e. Measurement 1's record. 1 placeholder.
- measure-up-noinit-decisions.jsonl: 5 lines, 665 bytes, sha256 38bc4f7794922db197307a1b2cac5b96055e2a3e961852c8f8a1e98c0086974f. Measurement 1's decision log.
- measure-claude-token-run.txt: 26 lines, 1275 bytes, sha256 0d313aae4316f9d89acb58a0372e53e80d93ec79e49d9aa4d3aa1b87ed93d11e. Measurement 2's record. 2 placeholders.
- measure-claude-token-decisions.jsonl: 4 lines, 660 bytes, sha256 103041b4397d4722f99da8a63e58acbad3d5cbdfca082cc95ab45b9b0d140d35. Measurement 2's decision log.
- deadcode-liveness.txt: 10 lines, 788 bytes, sha256 a287d2d29c21cbf495a3ed9291e74c3bd71cacedd3cd86eca39b9c015083e293. Measurement 3's ten lines.

Already in this folder and named above: README.md, lead-first-screen.txt, lead-screen-30s.txt. Not copied: the full copy of the decision log (21640 lines, 4566220 bytes), the task store's answers, the eleven train directories, the other captures of the two measurement runs, the rest of deadcode's output, the lead session's approvals record and notes.
