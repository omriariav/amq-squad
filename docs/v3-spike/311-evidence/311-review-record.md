# 3.1.1: the review record

The 3.1.1 findings that came from review traffic, not from an execution note:
Codex passes on the PRs, the plan reviewer's rulings, and a read-only routing
investigation on 2026-11-05. The 3.1.1 release notes' known limits point
here. Each entry gives what was found, where in the code, which review found
it, who, the date, and the ruling.

The findings and rulings are the lead's account of that traffic. The
"Checked" line of each entry is what this record's author read at v3
`133f3422` (#876 merged): the code at the cited lines, and the live squad's
records under `.agent-mail/v31` (read only). Where a cited line did not
match, the entry gives the line that does.

## 1. A seat can claim any pending task by id, whatever its pool

- **What:** `task claim ID` grants a pending task to the claiming seat with no
  check that the seat serves the task's pool. The grant then writes the
  claimer's pool onto the task.
- **Where:** internal/task/lease.go:185-190 (the claim by id calls `grant`),
  :64-93 (`grant` checks state, blockers, the seat's other lease and its
  leasability, never the pool against `t.Pool`; at :86-89 it sets `t.Pool`
  from the claimer's pool); internal/cli/task.go:397-398 (the `claim` verb).
- **Found by:** the routing investigation, 2026-11-05. After the roster
  relaunch, the dev seat claimed t64, a task on the sol pool.
- **Ruling:** 3.1.2.
- **Known-limits line:** A seat can claim any pending task by its id, whatever
  pool the task is routed to.
- **Checked:** the code as cited. t64's record holds an attempt by dev granted
  at 2026-11-05T04:39:54Z, cancelled at 04:41:09Z. The decision log has no
  `dispatched` decision for that attempt; the only `dispatched` for t64 is
  the reviewer's at 2026-11-04T23:26:33Z.

## 2. A seat whose attempt lapsed is not handed that task again

- **What:** the scheduler's `idleSeat` skips every seat that let an earlier
  attempt of the task lapse (unclaimed or expired).
- **Where:** internal/scheduler/scheduler.go:973-990 (`idleSeat`; the
  `lapsed` map at :974-977, the skip at :986). Its one caller is :824.
- **Found by:** the routing investigation, 2026-11-05.
- **Ruling:** 3.1.2.
- **Known-limits line (as given):** A seat whose attempt lapsed is not handed
  that task again until the roster relaunches.
- **Checked:** the map is built on every call from the task's own attempt
  history (`t.Attempts`), keyed by seat handle. Nothing in this function
  clears it at a roster relaunch, so the "until the roster relaunches" part
  is not confirmed by this code. The release notes' line says only what the
  code shows: the scheduler does not hand that task to that seat again.

## 3. A Codex seat must run `task renew` itself

- **What:** a Codex seat's lease is renewed only when the seat runs `task
  renew`. A seat that forgets is closed when its lease runs out. Claude seats
  renew from their hook on every tool result; Codex seats have no such hook.
- **Where:** internal/cli/hook_ingest.go:107-123 (`renewLeaseOnToolResult`,
  renewal on a tool result); internal/seatexec/codexhook.go:22 (the Codex
  hook events AMQ wires: `Stop`, `UserPromptSubmit`, `Interrupt`,
  `SessionStart`, `SessionEnd`, `PermissionRequest`; no `PostToolUse`);
  internal/scheduler/scheduler.go:61 (the `SeatActivity` event, "renews its
  claimed lease"), consumed at :563.
- **Found by:** the routing investigation, 2026-11-05. The reviewer seat
  worked t64 from about 23:27Z to 23:36Z on 2026-11-04, never renewed, and
  was closed at 23:37:10Z with `lease_expired`.
- **Ruling:** 3.1.2, first on its list.
- **Known-limits line:** A Codex seat keeps its lease only by renewing it
  itself. A seat that forgets is closed when its lease runs out.
- **Checked:** the lead's message cited `task.go:35` for `SeatActivity`.
  internal/task/task.go:35 is `DefaultLeaseDuration`, 10 minutes. The event
  is at internal/scheduler/scheduler.go:61, and no production file posts it:
  the only senders are internal/scheduler/scheduler_test.go and
  spike/pi-r19/main.go. The decision log has `dispatched` t64 to reviewer at
  23:26:33Z, `lease_expired` at 23:37:10.760Z and `seat_closed` reviewer
  `lease_expired` at 23:37:10.760Z. t64's attempt was granted at
  23:26:30Z, 10 minutes 40 seconds before it expired.

## 4. The merge train's plan text shows commit ids and rule names above its rule

- **What:** parts of `merge-train`'s printed plan still carry a commit id or a
  rule name in the line a reader sees first.
- **Where:** internal/cli/merge_train.go:291 (the header line ends with the
  base SHA), :292-293 (the protection line joins `Protection.Notes`, where
  `ci_app_unbound: ...` notes sit; written in internal/mergetrain/gh.go:379
  and :406), :310 (the PR summary line, with the short head), :330 (a
  refusal other than the up-to-date rule prints `refused <rule>: <detail>`).
- **Found by:** Codex on PR #874, 2026-11-05.
- **Ruling:** 3.1.2.
- **Known-limits line:** The merge train's plan text still shows some commit
  ids and rule names above its line.
- **Checked:** the four lines as cited, in `printMergeTrain`. The t65b note
  already names "the plan header's base SHA" as a pre-existing limit.

## 5. A passing read error in reconcile stops the run

- **What:** while reconciling, a failed read of one PR returns an error, and
  the whole run stops with "read PR #N while reconciling". Running it again
  fixes it.
- **Where:** internal/mergetrain/train.go:767 (`reconcileMerges`), the branch
  for a PR with no journal entry at :782; the read at :786 and the return at
  :788.
- **Found by:** Codex on PR #868, 2026-11-04.
- **Ruling:** 3.1.2: audit and skip that PR instead.
- **Known-limits line:** A passing error reading GitHub while reconciling
  stops the whole run. Running it again fixes it.
- **Checked:** the code as cited. The failing read is the PR read
  (`Forge.PR`), so the notes' line says "reading GitHub", not "reading the
  compare".

## 6. A PR merged at another head is read and audited again on every run

- **What:** a PR GitHub reports merged at a head other than the bound one is
  audited and not recorded. Nothing marks it, so every later real run reads
  it and audits it again.
- **Where:** internal/mergetrain/train.go, the same branch: :790-792 (the
  merged check) and :793-800 (`reconcileTaskMerge` with the server's head;
  not recorded, so the loop continues).
- **Found by:** Codex on PR #868, 2026-11-04.
- **Ruling:** 3.1.2: refuse it from the plan, with the audit as its reason.
- **Known-limits line:** A PR that GitHub reports merged at another head is
  audited again on every run.
- **Checked:** the code as cited. The t53b-2b note covers the audit, not its
  repeat.

## 7. A very high size cap stops the size-driven growth gate

- **What:** a widen whose diff crosses the grant's size cap goes to the
  owner's growth gate. With the cap set very high, nothing crosses it, so
  that gate stops firing. The protected-path gate is unchanged.
- **Where:** internal/grants/grants.go:57-58 (`SizeCapLines` and
  `SizeCapFiles`; 0 means no merge fits the grant);
  internal/seatcommit/widen.go:166-168 (a size-cap violation becomes a
  `size_cap` crossing, which goes to the growth gate).
- **Found by:** the plan reviewer, 2026-11-05. The owner had set this
  repository's grant to 100000 lines and 1000 files.
- **Ruling:** 3.1.2: an explicit cap-off value.
- **Known-limits line:** With the size cap set very high, the size-driven
  growth gate stops firing. The protected-path gate is unchanged.
- **Checked:** the lead's message cited widen.go:132. That line is the
  protected-path crossing. The size crossing is at :166-168.

## 8. The archive is cut at the typed block, and the commit step measures again

- **What:** a typed widen's size check measures the attempt as archived when
  the seat blocked. The commit step measures the diff again at commit.
- **Where:** the notes carry the first half. t43: the scheduler archives the
  stopped attempt at the typed block. t44: the widen archives on demand when
  that archive is missing. t40: the dry run measures the archive by task and
  attempt, "never the live tree". t47c: a retried attempt archives its own.
- **Found by:** the commit step measuring again is in no note. It is recorded
  here as the plan reviewer's, on PRs #855 and #856, 2026-11-02.
- **Ruling:** a known limit of 3.1.1.
- **Known-limits line:** The widen's size check measures the attempt as
  archived at the block. The commit step measures again when it commits.
- **Checked:** the four notes, as quoted.

## 9. The permission-request warning says only that a request was made this turn

- **What:** `seat_stalled_at_dialog` comes from AMQ's `.wake.dialog` marker.
  AMQ's Codex hook writes it on a permission request and removes it only at
  the turn's end. An approved prompt leaves it in place while the seat works.
- **Where:** internal/status/screen.go:239-240 (the marker's presence makes
  the warning); internal/seatexec/codexhook.go:22 (the hook's events).
- **Found by:** the routing investigation, 2026-11-05.
- **Ruling:** PR #873 changed the screen's words to what the record holds. The
  marker's meaning is 3.1.2.
- **Known-limits line:** The permission warning says only that a request was
  made this turn. It cannot tell a stuck seat from a working one.
- **Checked:** the code as cited, and the
  [311-squad-ux-stalled-words.md](311-squad-ux-stalled-words.md) note.

## 10. The lead-only rule's live evidence

- **What:** three refusals show that only the lead's process can act on the
  scheduler's decisions.
- **Where:** internal/scheduler's decision socket answers `not_lead` when the
  sender's parent chain does not reach the lead (31-2-keys.md, a 3.1.0 note).
- **The evidence:**
  - A test row: the widen verb's refusal from a sender that is not the lead
    is `not_lead`, shown in words
    ([311-squad-ux.md](311-squad-ux.md), section 4).
  - Live, during t42: the seat's own typed widen of its task was refused
    `not_lead`. The t42 and t43 task records say so; no note does.
  - Live, on 2026-11-05 at about 04:35Z: the lead session ran `scheduler
    relaunch` from a process that is not the lead's, and it was refused
    `not_lead`. Quoted from the lead session's transcript, not a squad log:

    ```
    $ amq-squad scheduler relaunch --root /Users/omri/code/amq-squad/.agent-mail/v31 --reason "..." --json
    {"schema_version":1,"kind":"scheduler_relaunch","data":{"ok":false,"code":"not_lead","detail":"pid 31443 does not descend from the lead cto's recorded incarnation"}}
    ```

    The relaunch then ran only from the lead's own pi, after the owner typed
    one line into its pane. The lead says the owner's approval is recorded in
    the release evidence. Neither the decision log nor the scheduler log of
    session v31 holds a `not_lead` line. The decision log does hold the
    relaunch itself: `roster_relaunch_requested` at 2026-11-05T04:39:15Z.
- **Found by:** the lead, from the run and the reviews.
- **Ruling:** a known limit of 3.1.1: no execution note records the live
  refusals.
- **Known-limits line:** The lead-only rule held live twice: a seat's own
  widen and a relaunch from outside the lead were refused. No execution note
  records it.
- **Checked:** the task records' text ("direct typed widening is lead-only and
  was refused not_lead"), the 311-squad-ux.md row, and the decision log's
  relaunch line. The CLI output is the lead's quote; it is in no squad log.

## 11. An owner gate can lift the up-to-date refusal, and GitHub still refuses the merge

- **What:** when a behind PR cannot be brought up to date safely, the train
  refuses it under the up-to-date rule. Like any refusal, it escalates to an
  owner gate, and an accepted gate lifts it. The train then plans the merge,
  but GitHub's strict protection refuses a branch that is behind.
- **Where:** internal/mergetrain/train.go:449-451 (`applyStrict` adds the
  `strict_required_status_checks_policy` refusal); :461-471 (`refuse`: an
  accepted owner gate for this head and these rules lifts any refusal,
  this one included).
- **Found by:** Codex on PR #870. The pass's own date was not given; #870
  merged on 2026-11-04.
- **Ruling:** 3.1.2.
- **Known-limits line:** An owner gate can lift the train's up-to-date
  refusal. GitHub then refuses the merge anyway.
- **Checked:** the code as cited. GitHub's refusal of a behind branch under
  strict protection was not exercised here.
