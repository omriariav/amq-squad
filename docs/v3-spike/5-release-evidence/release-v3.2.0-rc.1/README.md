# amq-squad v3.2.0-rc.1 release evidence (2026-11-07)

- Tag: v3.2.0-rc.1 -> 3966afb72b85e1ab6c0fc8c879f1c5ef03f92a40, the merge commit of the release PR #910 ("release: 3.2.0-rc.1 bump and the 3.2 notes so far": the version bump and docs/v3.2.0-release-notes.md). Its parents are fcc0beb72cd3e36f55002aab5a9446a570eedf30 (#903's merge on v3) and 3233be09520cb0a0bbe7db336d557999d61c639d (#910's head). `git rev-parse 'v3.2.0-rc.1^{}'` after `git fetch origin tag v3.2.0-rc.1` prints that commit. A plain `git fetch --tags` fails on unrelated old v2 tags, so fetch the one tag.
- Cut by the installed binary, /Users/omri/go/bin/amq-squad, which was then v3.1.2 (R54: the installed binary cuts). owner-approvals.txt records the approved route as "release prepare with the installed 3.1.2 binary"; the binary's version line at prepare and at publish was not kept, so the version is by the lead session's account.
- Release: https://github.com/omriariav/amq-squad/releases/tag/v3.2.0-rc.1, GitHub's own values (`gh api repos/omriariav/amq-squad/releases/tags/v3.2.0-rc.1`, release-api.json): created_at 2026-11-06T12:13:48Z, published_at 2026-11-07T16:43:45Z, draft false, prerelease true, target_commitish 3966afb72b85e1ab6c0fc8c879f1c5ef03f92a40. created_at equals the target commit's date (3966afb7's author and committer time is 2026-11-06T14:13:48+02:00); it is GitHub's value, not the publish time. The publish at 16:43:45Z came 39 s after the second approval.
- After publish, `gh release edit v3.2.0-rc.1 --prerelease --latest=false`. release-api.json, read after the edit, has prerelease true. `gh api repos/omriariav/amq-squad/releases/latest` still returns v3.1.2 (I re-ran it for this note).
- The notes file is docs/v3.2.0-release-notes.md at the tag, 18961 bytes, sha256 a645f12edbc423dfeb525b2d4e3e733a3c4d9b3ac550cfb8c663d29c30c6a74d (notes-body.sha256). `git show 3966afb7:docs/v3.2.0-release-notes.md | shasum -a 256` prints the same digest.
- No main join: v3.2.0-rc.1 is a pre-release. `git merge-base --is-ancestor 3966afb7 origin/main` is false.

## The skew check

skew-check.md, step 1 of the cut, before the restart. `git diff 5bd394e6dcf9fd06254f80ebc846d92130dc9048 fcc0beb72cd3e36f55002aab5a9446a570eedf30 -- internal/routing internal/scheduler internal/sealchain internal/seatcommit internal/task` (v3.1.2 to the tag candidate) touches 21 files, +2588/-101. The json-tagged fields it adds live in the task file, which is not sealed: task.Task Budget and BudgetExtensions, task.Lease Attempt and BudgetEndsAt, and the new task.BudgetExtension type. The one other added tag is a local decode struct in seatcommit/quarantine.go. The sealed exits queue and the commit ledger are unchanged. Result: no sealed struct changed, so rc.1 and 3.1.2 read each other's ledgers.

The rerun on the tag commit: `git diff fcc0beb7 3966afb7` over the same five directories is empty; the only Go file #910 changes is internal/claudeseat/relprep_version_test.go. I re-ran both diffs for this note, with the same results (21 files, +2588/-101; empty).

The rollback line (rollback.txt), recorded at 16:45:47Z before the restart: if the rc.1 lead's first screen fails (a dead pane or "Failed to load extension") or the scheduler refuses a ledger, stop the rc.1 lead and run `/Users/omri/scratch-v312/build/pi/amq-squad up --binary /Users/omri/scratch-v312/build/pi/amq-squad`, which prints amq-squad v3.1.2. It holds while the skew check holds. What a rollback loses: a 3.1.2 rewrite of a task drops Budget, BudgetExtensions, Lease.Attempt and Lease.BudgetEndsAt. The rollback was not needed.

## One gate series

- Series v320rc1. The release session root `/Users/omri/code/amq-squad/.agent-mail/v320rc1`, identity `root-sha256:a64e5d84dbaf6107da62c427a172d623cc39cec189c51233bd5462cd9a5e84d4` (squadid-expected.txt). The squad itself ran on the v31 session root throughout (the `up` records and the lead's first screen); v320rc1 carried only the release gates.
- release-prepare.json: release id release-v1-c84745a76e09bd3b2e4c5069969115c8a6d898d2b5d9d752d5669201a7d17d02, state active, gates rel-tag-bac0d98d5b361923de04 ("git tag v3.2.0-rc.1 3966afb72b85e1ab6c0fc8c879f1c5ef03f92a40 on omriariav/amq-squad") and rel-gh-52f285c7875979d81165 ("gh release create v3.2.0-rc.1 on omriariav/amq-squad"), both with question_sent true. release-prepare.json carries no times.
- The owner's approvals (attach-v320rc1.jsonl, the attach on the mailbox "user"), each a reply in its gate's thread:
  - rel-gh approved at 16:42:54Z ("accepted rel-gh-52f285c7875979d81165 github_release", event a4f05d7fa90dd94247ec8eaed344ce6295503d00dbc4a4d2e31d3808bb7c8313, authorization authz-v1-2a9bda634992c015276b0a2a26a8350f4a6c50987526fe11f100bc68bfba95ad);
  - rel-tag approved at 16:43:06Z ("accepted rel-tag-bac0d98d5b361923de04 tag", event a6dfa4aef256086004b1e3928f0f3df980c5877c42dc7edc98c2efa379a20a6d, authorization authz-v1-5658cf592832e3a00320bd14139d99e37d7003ecc86a3af332e23f62b8734292).

  No reply was refused and no second series was needed.

## The attach and the owner's approvals

- The attach swap, as it ran (attach-swap.txt). One attach may hold the mailbox. The v31 attach (pid 12715, the one restored after the 3.1.2 cut) was stopped at 16:39:19Z and the v320rc1 attach (pid 62631) was started at 16:39:23Z. After publish the v320rc1 attach was stopped at 16:44:04Z and the v31 attach was restored at 16:44:07Z (pid 66439). The attaches' own records run a few seconds earlier than the swap file: attach-v320rc1.jsonl's "attached" record is at 16:39:19Z and its "detached" record at 16:44:04Z; attach-v31-restored.jsonl's "attached" record is at 16:44:04Z. Both attaches are on mailbox user, operator true. The gate replies' message ids carry pid20202, the amq-remote endpoint, which ran throughout.
- attach-v31-restored.jsonl also holds one later gate reply, at 17:20:18Z: "accepted merge-e8c88d76dad1a873 spawn" (event 2246d61cec2fc66dc72beb8f7a23b5465bff523d0309f643b9b929e7073e84f5, authorization authz-v1-7db7a89faeea775b6f678128386ee6c672266164bdf8ee358570fc25e00e6a5c). It is not a release gate; the folder does not say what it approved.
- The owner's approvals of the route (owner-approvals.txt; each from the lead session's AskUserQuestion or its chat, not a squad log):
  - 16:38:58Z, "Approve: start now": stop the v31 attach (pid 12715), start the v320rc1 attach, run release prepare with the installed 3.1.2 binary, publish as the lead's step, restore v31, then `gh release edit --prerelease --latest=false`. And "Approve the restart": build the tag, stop the 3.1.2 lead, `up --binary` with the rc.1 build, with the rollback above.
  - 16:48:41Z, the Codex /hooks trust, delegated to the lead session. By the lead session's account the owner first chose "I'll do it now (Recommended)" for the hook trust, and then wrote in chat:

    > No. Sorry. I can't do it. You will approve it. Use tmux injection

    The same entry records the owner's approval to type the #894 rebind line into the lead's pane.
  - 16:52:33Z, "Approve the relaunch" (by the lead session's account the option read "Approve the relaunch (Recommended)", and its text said the session would run /hooks and trust only the three amq-squad entries), so the codex seats get the rc.1 activity hooks.
  - 17:36:32Z, "Approve, after t107": a second roster relaunch once t107 completed, so the seats launch with the hooks already trusted and the live renewal row can be recorded.
- The publish run: from the lead session, as the lead's own step. release-publish.json: error empty; the tag step ran (result 3966afb72b85e1ab6c0fc8c879f1c5ef03f92a40) and the github_release step ran (result https://github.com/omriariav/amq-squad/releases/tag/v3.2.0-rc.1).

## The published body

Checked with release-api.json (the `body` field, decoded as UTF-8): 19029 bytes. The first 18961 bytes are the notes file at the tag byte for byte (`cmp` against `git show 3966afb7:docs/v3.2.0-release-notes.md`, equal). The 68 bytes after them are two newlines and the release flow's own marker line `<!-- amq-squad-release-nonce: <32 hex> -->` of 66 bytes, with no newline at its end. Nothing else differs.

## The build

Built from the tag in the scratch worktree /Users/omri/scratch-v320rc1 with `scripts/build-pi-runtime.sh all` (build.log): the pi-1.3.0-node-22.21.1-darwin-arm64 payload, 6447 files, payload sha256 075de902aec60fd2890c4e2ac941acb44e7288ee3daef91d2d42856ba8c7f6c0 and manifest sha256 2fb7b3b9f3fcc5d945d8ab54272361e1ec4e519c6d713dcc605d0c8251d19a58; binary 72529042 bytes at `build/pi/amq-squad`. That binary reports v3.2.0-rc.1: up-320rc1.json's install step names it "(v3.2.0-rc.1)", and doctor-after-trust.json's "amq-squad on PATH" check reads "matches this build (v3.2.0-rc.1)". The doctor's pi runtime path ends in 075de902aec6, the payload digest's first 12 hex.

`make release-smoke VERSION=v3.2.0-rc.1` (release-smoke.log): "release metadata matches v3.2.0-rc.1", the release tests OK (6 and 4), then `go install github.com/omriariav/amq-squad/v3/cmd/amq-squad@v3.2.0-rc.1` into a temporary GOBIN, whose binary prints `amq-squad v3.2.0-rc.1`.

## Step 6: the installed lead's first screen

RELEASING.md step 6, run on the squad's own lead.

- 16:46:03Z, with the owner's restart approval, SIGTERM to the 3.1.2 lead, pid 50589 (attach-swap.txt). The squad was working 0 tasks, as the restart rule asks.
- 16:46:04Z, `up --binary` from the tag build (up-320rc1.json, start 16:46:04.404044Z): compile ok (3 seats, lead cto, session v31); install ok ("installed /Users/omri/scratch-v320rc1/build/pi/amq-squad (v3.2.0-rc.1) at /Users/omri/go/bin/amq-squad by temp name and rename"); session skipped (the root exists); doctor ok ("40 checks, none failed (6 warnings)"); lead ok ("no recorded lead or scheduler is running"); tmux ok (squad-lead-amq-squad-f42a4 runs /Users/omri/go/bin/amq-squad lead --team); roster ok ("roster_adopted: every roster seat is live from the previous incarnation; no launch"), so both codex seats kept running from 3.1.2.
- The pane's first screen (lead-first-screen.txt, trimmed below; the file has the whole capture), taken at 16:46:34Z by the lead session's account (the capture carries no time):

  ```
   amq-squad: lead cto (pid 69669, pane %134) · root
   /Users/omri/code/amq-squad/.agent-mail/v31
   seats (3)
     cto  -  stopped
     dev  codex gpt-5.6-terra medium  idle  · up in 4.4s
     reviewer  codex gpt-5.6-sol high  waiting: on your answer to its question  ·
   up in 4.4s, first message 2227.4s
   tasks (104)
   ...
  ~/code/amq-squad (v3)
  0.0%/131k (auto)                                            qwen3.6:27b • medium
  ```

  The team screen is up and there is no "Failed to load extension" line. The lead is pid 69669 (on the screen); its scheduler was pid 69679 by the lead session's process listing. The "up in 4.4s, first message 2227.4s" figures are the adopted seats' launch of 2026-11-06T04:51:53Z (up-320rc1.json's roster timing), not a new launch. The pane's tmux "extended-keys is off" warning is the owner's tmux configuration.

## #894's rebind

The owner approved typing the line into the lead's pane (16:48:41Z above). It was typed as:

```
!amq-squad merge-train rebind --pr 894 --from 68792071d942003613da13562ed2c7cb1dd34529 --to 2555d34aa77084145bd7b3ca58f252c5178ac55a
```

pi's `!` prefix runs the rest as a shell command from the lead's own process, which the scheduler's lead proof requires. rebind-894.txt holds the decision: merge_train_rebound, task t88, seat cto, at 16:50:59Z, "PR #894 re-bound from 68792071d942 to 2555d34aa770 by the lead's decision", patch-id bb04a7c198466144716cd38e5d93fe1053bf32c5, audit run mt-rebind-20261107T165103Z-324576e2.

## The roster relaunch and the hook trust

- The seats adopted from 3.1.2 had been launched without the rc.1 activity hooks: by the lead session's account, /hooks in them showed PostToolUse 0.
- The first relaunch failed. The roster plan refused the clean reviewer worktree on a stat-only stale index ("Entry MIGRATION.md not uptodate", with `git status` clean), and the scheduler stopped after the failed roster launch. At 16:53:51Z `git update-index --refresh` in the reviewer worktree fixed the index (attach-swap.txt; the same fix as on 2026-11-05).
- 16:55:19Z, `up` refused with the rc.1 lead still running (up-320rc1-2.json and up-320rc1-2.err: lead_running, pid 69669, pane %134).
- 16:55:32Z, SIGTERM to the rc.1 lead, pid 69669, under the owner's restart and relaunch approvals (attach-swap.txt).
- 16:55:33Z, `up` again, by the lead session's account with `--skip-install` (up-320rc1-3.json: install "skipped: /Users/omri/go/bin/amq-squad already reports v3.2.0-rc.1"; doctor 40 checks, none failed, 6 warnings; lead ok; roster "roster_launched: launch bce671a0f13ec2acf5092edbf4e34939: 2 seats in one apply"). dev and reviewer were live at 16:55:37Z.
- 16:59:54Z, the /hooks review in each fresh seat, done by the lead session through tmux under the owner's delegation: dev (pane %136) and reviewer (pane %137). Each trusted exactly three session-flag entries: PostToolUse and Stop `'/Users/omri/go/bin/amq-squad' evidence ingest --me "$AM_ME"`, and the Stop AMQ dialog-close copy (AMQ_DIALOG_HOOK). After it, Stop 3/3 and PostToolUse 1/1 were active (attach-swap.txt).
- doctor-after-trust.json, run after the trust: 40 checks, 0 failed, 5 warnings (registry, registry/pi, amq-remote login service, amq-remote login service state, pi runtime versions). Its "codex activity hook trust" check is ok: "Codex trusts all 3 exact amq-squad activity commands in /Users/omri/.codex/config.toml". So the Codex hook warning is gone; the `up` runs before the trust counted 6 warnings. Which check the sixth warning was is by the lead session's account, since no doctor output from before the trust was kept.

## The train on rc.1

interim-rule.txt records the interim rule checked before a train ran: every task whose PR goes in completed with no lease, and the exits queue holds no queued commit result. It has three lines: 17:00:34Z (before #894, #901 and #907: t88, t98, t103), 17:37:49Z (before #911: t106) and 17:44:25Z (before #912: t108). It has no line for #913, #914 or #915.

What landed on v3 after the cut (`gh pr view` for each, re-run for this note):

| PR | Task | Merged (UTC) | Merge commit |
| --- | --- | --- | --- |
| #894 | t88 | 17:09:18Z | 1313650a13deb88c7a9b5e359e5ed65679f1557a |
| #901 | t98 | 17:19:39Z | 27fa44f0e78474e0d53c08d319de678964dbf79c |
| #907 | t103 | not merged, closed | - |
| #911 | t106 | 17:47:01Z | 46b654c97069da6cbf6be002c4346315b39d5dd6 |
| #912 | t108 | 17:55:52Z | 63982a010aae64653227897a6e3b8fb13f579526 |
| #913 | - | 18:07:04Z | d9cea1290cbc325877612044eb7115e41fed4e46 |
| #914 | t109 | 18:23:43Z | f07affc55230ab251eaa96a06cf5074bc8397233 |
| #915 | - | 18:37:21Z | a4dff16c6726caf96a088a16efa1577f9ebd00ad |

- #894 went in through train run mt-20261107T170044Z-879e3790: update_requested, rebase, rebind, merge. By the lead session's account it was the first live asynchronous update. The run's own output is not copied here. What git shows: #894's merged head b91153ae95d41e1d64dfa21be579d2dd4400876e has first parent 3966afb7 (the tag commit), and `git diff 3966afb7 b91153ae | git patch-id --stable` prints bb04a7c198466144716cd38e5d93fe1053bf32c5, the patch-id in rebind-894.txt.
- #907 was refused: by the lead session's account it conflicted after #901's rebase, because it had carried #901's commits. It was redone as #912 (t108, "apply its own diff 12d1a8ba..fd52d772 only"). The two diffs are the same: `git diff 12d1a8ba fd52d772` and #912's commit `git diff 24dcc91d^ 24dcc91d` both have patch-id cbc2f9bf41ebf18d047ba991c40bd07327e39e74 (2 files, +79/-3), and their text is equal apart from the `index` lines, whose blob ids follow the different base.
- The PR heads moved after the seats bound them: t106 bound #911 at head 723ed3f5094e4bba0565368626e3dc262b8737da (renewal-decisions-t106.log) and #911 merged from head b37f4611501cf38f619bbdd4058e9328eb8054e7; t109 bound #914 at head 5a4bc57c79e461a2e54e99f4c70a9f4366900b4c (renewal-decisions-t109.log) and #914 merged from head f1c23cdbfaf15641c7ea185bdf67f7e0306ac0b1.

## The live renewal row

### t106 is not the proof

renewal-row-t106.log samples t106's task record every 30 s. t106's seat was launched at 16:55:33Z, before the trust at 16:59:54Z, and by the lead session's account its doorbell told it to renew its lease by hand, and the seat ran `task renew` 20 times. The row shows the claim at 17:12:39Z, the renewals counter at 19 in the last sample before completion (17:33:49Z, renewed 17:33:39Z), and completion by 17:34:19Z. renewal-decisions-t106.log has completed at 17:34:02Z and pr_bound #911, and no lease_renewed entry. The counter does not tell manual renewals from activity renewals, so t106 cannot show that activity alone renews a lease.

### t109 is the proof

- The seats were relaunched at 17:41:27Z (roster launch d1e5673a, by the lead session's account; at 17:41:12Z `git update-index --refresh` was run first in the clean dev and reviewer worktrees, attach-swap.txt), under the owner's "Approve, after t107". So they launched with the hooks already trusted. By the lead session's account t109's doorbell carried no manual-renew instruction; the doorbell line in reviewer-pane-t109.txt is "AMQ doorbell run amq drain --include-body then act on it".
- renewal-row-t109.log: claimed 2026-11-07T17:47:41.116888Z, with a 10-minute lease (expires 17:57:41Z). Renewed at 17:48:47Z, 17:54:22Z, 17:59:55Z, 18:01:26Z and 18:05:14Z: 5 renewals, attempt 1 throughout. Completed by the 18:06:04Z sample.
- renewal-decisions-t109.log: lease_renewed at 17:48:47.835855Z, "first activity renewal of attempt 1"; commit_running at 18:05:38Z; completed at 18:05:44.21936Z; pr_bound "omriariav/amq-squad pr 914".
- So the seat held its lease from 17:47:41Z to 18:05:44Z, 18 minutes, past a 10-minute lease that would have expired at 17:57:41Z without a renewal.
- By the lead session's account, the seat's ingest ledger had 20 events in that window and none of them was a `task renew` command. The ledger is not copied here.
- reviewer-pane-t109.txt is the reviewer pane after completion: "Doorbell handled and task t109 completed", PR #914, "Worked for 18m 56s". It is only 24 lines because Codex's TUI draws on the alternate screen, so the scrollback keeps nothing of the run. The ingest ledger, not the pane, is the complete record of what the seat ran.

## Things seen

### 1. One `status --json` with a raw control character

By the lead session's account, one `amq-squad status --json` at 16:46:26Z, just after the scheduler started, printed a raw control character and `jq` rejected it; the output was not kept. Six later runs were valid. docs/v3-followups.md, section 3.2.x, Open items, has the item (the encoder escapes control characters, so another writer on stdout is the hypothesis; capture the bytes next).

### 2. Three gate_store_unreadable alerts

By the lead session's account the train raised three gate_store_unreadable alerts, one for each of three `gates/clear-*.json` records in the v31 session that have been malformed since 2026-11-05. They were left as they are. docs/v3-followups.md, section 3.2.x, Open items, has the item (find the writer before anyone clears them).

### 3. The dev seat cannot drain its mailbox

The dev seat (codex, gpt-5.6-terra) cannot drain its mailbox or claim, because Codex seats pin `writable_roots=[]` (C5), so by the lead session's account the tasks after the cut went to the sol pool. docs/v3-followups.md, section 3.2.x, Open items, item "rc.2 C5".

### 4. Two renewal items for rc.2

From the t106 and t109 rows: the first-renewal decision keys on `Renewals == 1`, so a manual renewal can take the marker before the first activity (t106's decisions have no lease_renewed entry), and the doorbell reads hook trust at launch, so trust added mid-session does not drop the manual-renew instruction. docs/v3-followups.md, section 3.2.x, Open items, has both as rc.2 items.

### 5. Step 6 still needed a SIGTERM

`up` refuses a running lead (up-320rc1-2.json), so both restarts (16:46:03Z and 16:55:32Z) were SIGTERMs, as on 3.1.2.

## The files

All copied from the job's scratch records through `go run ./scripts/redact-evidence` (rule 18; it replaced no value, and `-check` is clean):

attach-swap.txt, attach-v320rc1.jsonl, attach-v31-restored.jsonl, build.log, doctor-after-trust.json, interim-rule.txt, lead-first-screen.txt, notes-body.sha256, owner-approvals.txt, rebind-894.txt, release-api.json, release-prepare.json, release-publish.json, release-smoke.log, reviewer-pane-t109.txt, rollback.txt, skew-check.md, squadid-expected.txt, up-320rc1.json, up-320rc1-2.json, up-320rc1-2.err, up-320rc1-3.json. The four renewal files were renamed on copy by task: renewal-row-t106.log, renewal-decisions-t106.log, renewal-row-t109.log and renewal-decisions-t109.log. The full status snapshot, the task briefs and the PR bodies are not copied.
