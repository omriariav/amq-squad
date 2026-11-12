# amq-squad v3.2.0 release evidence (2026-11-12)

- Tag: v3.2.0 -> e93ab08c378265e8b0a7d7a6123bc834feec5c67, the merge commit of the release PR #985 (its subject is "Merge pull request #985 from omriariav/lead/v32-320-final"). Its parents are 05f6b489f458cba304b015b6834ad704a1fd6cf3 and cba0114787e5e88a89c9117a0a5ded0200446054 (`git rev-list --parents -n 1`); by the lead session's approvals record the second is #985's head and the merge was into release/v3.2. `git rev-parse 'v3.2.0^{}'` prints that commit and `git rev-parse origin/release/v3.2` prints the same. The tag is a lightweight tag (`git cat-file -t v3.2.0` prints `commit`; tag-create.json has object type commit).
- **Cut by hand, with the two signed gates not answered.** This is the owner's choice, recorded under "The owner's approval" below, and it is an exception to RELEASING.md's "Never tag by hand" (RELEASING.md:40). The sentence is unchanged: `git diff da109b49 e93ab08c -- RELEASING.md` has three hunks and that line is in none of them.
- `release prepare` still ran, by the installed binary `<home>/go/bin/amq-squad`, which was v3.2.0-rc.2 (cut-binary-version.txt; the file's time is 10:13:22Z, 5 s after the prepare started). By the lead session's approvals record no `release publish` ran.
- Release: https://github.com/omriariav/amq-squad/releases/tag/v3.2.0. GitHub's own values (release-api-fields.json, a selection of the fields of `gh api repos/omriariav/amq-squad/releases/tags/v3.2.0` as the lead session saved it at 11:04:19Z by the file's time): created_at 2026-11-12T10:11:11Z, published_at 2026-11-12T11:04:07Z, draft false, prerelease false, target_commitish e93ab08c378265e8b0a7d7a6123bc834feec5c67, author omriariav. created_at equals the target commit's date (e93ab08c's author and committer time is 2026-11-12T12:11:11+02:00); it is GitHub's value, not the publish time.
- The release was created with `--latest` and is not a prerelease. The script's last call, `gh api repos/omriariav/amq-squad/releases/latest`, printed `latest: v3.2.0` (publish-by-hand-out.txt).
- The notes file is docs/v3.2.0-release-notes.md at the tag, 63690 bytes, sha256 8e6568fe96b986e9735c051280b057001790b559cb6d5150c3bc763526651df9 (`git show e93ab08c:docs/v3.2.0-release-notes.md | shasum -a 256` and `| wc -c`, run for this note).
- release-check.log (the file's time is 10:12:06Z, before the prepare): "release metadata matches v3.2.0", the release tests OK (6 and 4). The record holds neither the command line nor its exit code.
- Where a sentence below says "by the lead session's approvals record", its source is owner-approvals-excerpt.txt, whole lines of the lead session's own running record. That record is not a squad log.
- The main join: PR #986, merged, by the lead session's approvals record (line 1704) by hand on the plan reviewer's ruling. The values below are from commands run for this note in its worktree after `git fetch origin main`. The PR's head is 6ce0e4bf9a96b945262b8dd3b85899b90c5e3431; its parents are 2fbcd9e46e4cd66a7f771b7e74654e185b97bb3e and e93ab08c378265e8b0a7d7a6123bc834feec5c67 (`git rev-list --parents -n 1`), by the approvals record main's tip before the join and the tag's commit; its tree is 6c86edf5a65b9ee662a00e974e01300776bd7a0e, which is what `git rev-parse 'v3.2.0^{tree}'` prints. GitHub's merge commit is c09fd23154d4a8643b68d7b4c4714e5448185842 (`git rev-parse origin/main`); its parents are 2fbcd9e46e4cd66a7f771b7e74654e185b97bb3e and 6ce0e4bf9a96b945262b8dd3b85899b90c5e3431, and its tree is the same 6c86edf5a65b9ee662a00e974e01300776bd7a0e. `git merge-base --is-ancestor e93ab08c origin/main` exits 0. On main, `jq -r .version` prints 3.2.0 for plugins/claude/.claude-plugin/plugin.json and 3.2.0 for plugins/codex/.codex-plugin/plugin.json. The two marketplace files, .claude-plugin/marketplace.json and .agents/plugins/marketplace.json, name the plugin by a local source path (`./plugins/claude` and `./plugins/codex`) and carry no version of their own (`.plugins[].version` is null in each). An install through a marketplace client was not measured. Before the join, at 11:04:17Z, the tag commit was not an ancestor of origin/main (the approvals record, line 1691: "the commit is not an ancestor of origin/main (exit 1)").

## The owner's approval

The words are the owner's, typed in the lead session or chosen in its AskUserQuestion; they are in owner-approvals-excerpt.txt as the lead session recorded them, not in a squad log.

- 2026-11-10, 00:05 IST, asked which way he wants rc.2, 3.2.0 and 3.3.0 cut, he chose "A: I cut, no Buzz answers". The option's text was: "I run `release prepare`, then tag and publish by hand under your gh login. You are told before each publish. The evidence note records that the two signed gates were not answered, with your words." (lines 14 and 15 of the record). Line 17: "The plan reviewer's conditions for A apply at each cut (see the 2026-11-09 entry)."
- Told before the publish, by the lead session's approvals record: the owner was told in the session at 10:13:42Z ("the commit, by hand under his gh login, the two Buzz questions need no answer, latest and not a prerelease, no restart and no join yet"); a five-minute hold ran; there was "no word from him in it".
- The refusal, by the same record: the lead session's own run of `sh T/v320/publish-by-hand.sh` "was REFUSED before it ran by the harness's auto mode classifier, reason "[Create Public Surface]". Nothing was created: `git ls-remote origin refs/tags/v3.2.0` 0 lines, releases/latest v3.1.2, no publish-by-hand.out content." The record names the condition of answer A it applied ("a hook or permission refusal stops the cut and goes to him") and says: "NOT retried, NOT worked around, not routed through another session. The owner is asked to run the script himself or to allow it."
- The publish, by the same record, under the heading "v3.2.0 PUBLISHED BY THE OWNER'S OWN COMMAND": "After the permission refusal he ran, at the session's prompt: `sh <home>/amq-squad-lead/T/v320/publish-by-hand.sh 2>&1 | tee <home>/amq-squad-lead/T/v320/publish-by-hand.out` (the ruled script, unchanged)."
- After the publish, by the record's entry of 11:08:26Z: asked three lettered questions (R restart the lead on the 3.2.0 build; W empty dev2's worktree, patch saved first; L the relaunch typed in the lead's pane; order W, R, L), the owner typed "R - i will approve. do it" / "W - ok" / "L - i will approve, do it". The lead session's reading, in the record's words: "his word for the lead session to do all three, W then R then L, and that he will approve a permission prompt if one comes. Not read as: approval of anything else in a seat's worktree or pane, or of working around a refusal; a refusal still goes back to him as a fact."

## One gate series, not answered

- Series v320: the prepare ran with `--session v320`. The command, by the lead session's approvals record: `<home>/go/bin/amq-squad release prepare --repository omriariav/amq-squad --tag v3.2.0 --commit e93ab08c378265e8b0a7d7a6123bc834feec5c67 --notes-file <home>/scratch-v320/docs/v3.2.0-release-notes.md --branch release/v3.2 --project <home>/code/amq-squad --session v320 --json`.
- `--branch release/v3.2` is there because the flow checks `main` when no branch is named: the flag's default is "main" (internal/cli/release_flow.go:60; its help text at :36-37 says prepare "requires the remote --branch (default main) to contain it (commit_not_on_branch)"), the flag's value is the flow's `Branch` (:101), and `CheckCommit` returns `ErrCommitNotOnBranch` ("commit_not_on_branch", internal/releaseflow/releaseflow.go:146) when the remote branch does not contain the commit (:273-278). The tag commit is on release/v3.2. It was not on main at the prepare: the join into main merged later (the header's last item), and at 11:04:17Z, after the publish, the approvals record (line 1691) still has "the commit is not an ancestor of origin/main (exit 1)". No run without the flag is recorded.
- release-prepare.json (the prepare started at 10:13:17Z, prepare-time.txt; exit 0 by the lead session's approvals record): release id release-v1-c0174eaaa4849a3060806111de2af6db4000672a8ad50873278db4ffa029a16d, state active, gates rel-tag-febadaefa50d6c911a51 ("git tag v3.2.0 e93ab08c378265e8b0a7d7a6123bc834feec5c67 on omriariav/amq-squad") and rel-gh-c03a383c637a3b75983e ("gh release create v3.2.0 on omriariav/amq-squad"), both with question_sent true.
- The prepared body digest, read from the prepared record's note (prepared-note.txt; the file's time is 10:13:27Z): `release body sha256:8e6568fe96b986e9735c051280b057001790b559cb6d5150c3bc763526651df9`, equal to the notes file's digest. The hand script checked the notes file against it again before either call.
- gate-questions.txt, read from the session's gate store at 10:19:58Z: the tag gate was issued at 10:13:21Z with not_after 10:43:21Z and its question was posted at 10:13:21Z; the release gate was issued at 10:13:22Z with not_after 10:43:22Z and its question was posted at 10:13:22Z; each has 0 answer files. **Both gates are NOT ANSWERED.** By the lead session's approvals record they "lapsed unanswered at 10:43:21Z and 10:43:22Z, before the tag".

## The hand steps

publish-by-hand.sh is the script, with the home path replaced by the placeholder; publish-by-hand-out.txt is the output of the owner's run (the times and the read-back below are from it). The tag call is the release flow's own (internal/releaseflow/gh.go:77-79). The release call is not the flow's (:116-118, a `gh api` POST with draft=true): it is `gh release create` with the same tag, target commit, name and body.

1. It fetches release/v3.2 and checks that `git rev-parse origin/release/v3.2` equals e93ab08c378265e8b0a7d7a6123bc834feec5c67, that the notes file's sha256 equals the prepared digest, and that `git ls-remote origin refs/tags/v3.2.0` prints 0 lines; any of the three ends it before any call.
2. The tag, 11:04:04Z to 11:04:05Z: `gh api -X POST repos/omriariav/amq-squad/git/refs -f ref=refs/tags/v3.2.0 -f sha=<the commit>` (tag-create.json: the ref, object type commit, that sha). Read back: `commit e93ab08c378265e8b0a7d7a6123bc834feec5c67`.
3. The release, 11:04:05Z to 11:04:07Z: `gh release create v3.2.0 --repo omriariav/amq-squad --verify-tag --target <the commit> --title v3.2.0 --notes-file <the notes file> --latest`. Its output is the release's URL (release-create-out.txt). Unlike the flow, it created the release published, not as a draft that is then published.
4. `gh api repos/omriariav/amq-squad/releases/latest` printed `latest: v3.2.0`.

The lead session's own run of this script was refused by the permission system before it ran, and the owner then ran the same script himself; both are quoted from the approvals record under "The owner's approval". The release's author in GitHub's record is omriariav (release-api-fields.json).

## The published body

By the lead session's approvals record, checked at 11:04:17Z: "the published body 63690 bytes, sha256 8e6568fe…, `cmp` exit 0 against `git show e93ab08c:docs/v3.2.0-release-notes.md`".

Checked again for this note from the lead session's saved API record (the `body` field, written with `jq -j`): 63690 bytes, sha256 8e6568fe96b986e9735c051280b057001790b559cb6d5150c3bc763526651df9, the digest of the notes file at the tag. The two saved copies in the job's scratch, published-body.md and notes-at-tag.md, are 63690 bytes each with that sha256, and `cmp` finds each pair equal. Neither is copied here: each is the notes file at the tag. release-api-fields.json carries the body's byte length and sha256. The body equals the notes file byte for byte, so no marker line follows it.

## The skew check

skew-check.md, run for this note after the cut. `git diff --stat da109b496787ffb7b8a716b8d2d75af19e98a045 e93ab08c378265e8b0a7d7a6123bc834feec5c67 -- internal/routing internal/scheduler internal/sealchain internal/seatcommit internal/task` (v3.2.0-rc.2 to the tag commit) prints nothing: no file in the five directories differs between rc.2 and 3.2.0. The whole range is 25 commits, 82 files, +1999/-77. The #985 merge against its first parent is 26 files, +150/-73, and one of them is under `internal`: internal/claudeseat/relprep_version_test.go.

`FirstRawLayoutVersion` is still "v3.2.0-rc.2" (internal/sealchain/raw.go:30). This note adds no cross-version claim of its own.

The rollback line (rollback.txt), written before the restart of the squad's own lead: the rc.2 tag build is still at `<home>/scratch-v320rc2/build/pi/amq-squad` (sha256 e978bdeff8e5df78631090f39f4eb8208804b7a89f97917ba4a99bb9914491df, "equal to the installed one"); if 3.2.0's `up` fails or its lead does not come up, stop what is up with SIGTERM and run that build's `up --binary` with itself as the target. In the file's words: "`up --binary` admits v3.2.0-rc.2 over a raw-layout session (internal/sealchain/raw.go:30, internal/cli/up.go:400, :403; the 3.2.0 notes, Rolling back). No row measures a roll back from 3.2.0 to rc.2." and "Not run without the plan reviewer's word and the owner's." No rollback is recorded here.

## The build

build-smoke.sh is the script. It ran in the scratch worktree `<home>/scratch-v320`, where `git describe --tags --exact-match` printed v3.2.0 (describe.txt). `scripts/build-pi-runtime.sh all` (build.log; build-times.txt: 11:05:38Z to 11:05:57Z, exit 0): the pi-1.3.0-node-22.21.1-darwin-arm64 payload, 6448 files, payload sha256 4f3c9775645c1a92d12e0f2f0399f4458458c1288a09d4de725547c5ad6c98b8 and manifest sha256 572687a3826781755c1d8af9333a8fd1a62bfdd936c53309eae32e5f7e98c1c6; binary 73209858 bytes at `build/pi/amq-squad`. `build/pi/amq-squad version` prints `amq-squad v3.2.0` (build-times.txt). The worktree had 0 porcelain lines after the build and the smoke (the last line of build-times.txt). By the lead session's approvals record the binary's sha256 is 0e6b6b098242ef764a1974d06bc7f606fb62361e92b5e487a6c04f6d20fe13c7.

`make release-smoke VERSION=v3.2.0` (release-smoke.log; exit 0, done 11:06:24Z, build-times.txt): "release metadata matches v3.2.0", the release tests OK (6 and 4), then "go: downloading github.com/omriariav/amq-squad/v3 v3.2.0" and `amq-squad v3.2.0`.

## Step 6: the lead's first screen, the scratch-repository route

RELEASING.md step 6 gives two routes: restart the squad's own lead on the new binary, or run `amq-squad lead --team -- --offline` in a scratch repository. This cut recorded both. This section is the second.

- The run is the lead session's own (scratch-lead.sh, the script; scratch-lead-run.txt), 11:07:19Z to 11:07:31Z: a new directory under /private/tmp with an empty `git init` repository and no `squad.yaml`, a scratch `HOME`, a tmux server on its own socket, and `env -i` with `HOME`, `PATH`, `TERM`, `GIT_CEILING_DIRECTORIES` and `AMQ_NO_UPDATE_CHECK=1`. Unlike rc.2's run, the host's amq is on `PATH` (`amq: 0.116.2 at <home>/go/bin/amq`) and `amq coop init` ran in the repository first, as RELEASING.md step 6 says at v3.2.0 (RELEASING.md:66-68). The command is `<home>/scratch-v320/build/pi/amq-squad lead --team -- --offline`, the binary built from the tag above, which prints `amq-squad v3.2.0`.
- `amq coop init`, with stdin closed and a 60 s alarm: `amq coop init exit=0`; its output is scratch-coop-init.txt ("Initialized co-op mode:", root `.agent-mail`, "Created: .amqrc"). After it: `.amqrc present: yes; .agent-mail present: yes`.
- At 3 s and at 10 s the pane was alive: `dead=0 status= cmd=amq-squad` (pane pid 99776 at 10 s; the capture is stamped 11:07:29Z).
- The first screen at 10 s (lead-first-screen-scratch.txt; below is every non-blank line of it but the two rule lines):

  ```
  amq-squad: lead - · root /private/tmp/v320lead.nlK9Ni/home/.amq-squad/solo/proj-fdf7ce5aba1a/root
  seats (1)
    lead  -  stopped
  tasks (0)
    No tasks yet; add one with /add.
  train
    No merge train has run yet.
  waiting on you (0)
    Nothing is waiting on you.
  /add  /reset  /dismiss  /answer  /widen  /team hide
  /private/tmp/v320lead.nlK9Ni/proj (main)
  0.0%/131k (auto)                                                                qwen3.6:27b • medium
  ```

  The team screen is up and the capture holds no "Failed to load extension" line (`grep -c` prints 0).
- The screen's root is under the scratch `HOME`'s `.amq-squad/solo/`, not the repository's `.agent-mail`, and the screen has one seat, `lead`. The script's `find` for a scheduler decision log under the repository's `.agent-mail` copied none: the job's scratch holds no scratch-decisions.jsonl. See "Corrections found at the cut", item 1.
- The tmux server was then killed; scratch-lead-run.txt lists no process naming the scratch directory after it.

## Step 6: the squad's own lead on the installed 3.2.0 build

Under the owner's "R - i will approve. do it" (the approvals record's entry of 11:08:26Z, quoted under "The owner's approval"). The run is the lead session's own (restart-lead.sh, the script; restart-pre.txt, restart-run.txt, up-320.json):

- Before, at 11:10:54Z (restart-pre.txt): the build printed `amq-squad v3.2.0` and the installed binary `amq-squad v3.2.0-rc.2`; the lead was pid 15222 and its scheduler pid 15228, both started 2026-11-11 15:53:57 local time; sixteen processes had their working directory in a seat worktree, the two Codex seats (pids 18451 and 18458) and their children; status read "1 needs you, 1 working, 1 ready, 180 inactive"; the scheduler's decisions log had 21654 lines; `scheduler/exits.json` was 230 bytes.
- SIGTERM to pid 15222 at 11:10:56Z. The script waited 1 s for the lead and the scheduler to be gone; the scheduler's last line is `scheduler_stopped` at 11:10:56.985504Z. No SIGKILL was sent; the script stops without one if either pid outlives 30 s.
- `<home>/scratch-v320/build/pi/amq-squad up --binary <home>/scratch-v320/build/pi/amq-squad --json` started at 11:10:57Z (restart-run.txt; up-320.json has start 2026-11-12T11:10:57.915571Z) and was collected with `up exit 0` at 11:11:27Z, after the second screen capture. Its stderr file in the job's scratch, up-320.err, is 0 bytes and is not copied. Its steps (up-320.json): compile ok, "4 seats, lead cto, session v31"; install ok, "installed <home>/scratch-v320/build/pi/amq-squad (v3.2.0) at <home>/go/bin/amq-squad by temp name and rename"; session skipped, the root exists; doctor ok, "41 checks, none failed (5 warnings)"; lead ok, "no recorded lead or scheduler is running"; tmux ok; roster ok with `roster_incomplete` (below).
- After (restart-run.txt): `installed now: amq-squad v3.2.0`; the lead is pid 6643 on pane %153 and its scheduler pid 6645, both started 2026-11-12 13:11:00 local time. By the lead session's approvals record the installed binary's sha256 is 0e6b6b098242ef764a1974d06bc7f606fb62361e92b5e487a6c04f6d20fe13c7, the tag build's.

Doctor's five warnings (doctor-warnings.txt, the five warn checks of the lead session's doctor document after the restart, whose time is 11:12:54Z by the file; 36 ok and 5 warn) are the registry file out of date, five unverified pi registry entries, the amq-remote login service absent (two checks) and twelve old pi runtime versions.

### The adopted roster

The scheduler adopted the two seats that were alive (decisions-after-restart.jsonl): `seat_adopted` for dev (pool codex:gpt-5.6-terra:medium) at 11:11:00.521447Z and for reviewer (pool codex:gpt-5.6-sol:high) at 11:11:00.525184Z, each with the reason "alive from a previous incarnation; its accepted proof's pid and start stamp match the live process". `scheduler_started` is "incarnation 63a766f63166" at 11:11:00.528266Z. The three roster rows of up-320.json carry the launch id 36f9c35929a8ea0700347c769800365c with the launch timing start 2026-11-12T05:31:59.263421Z, when the installed binary was the rc.2 build.

dev2 was not launched: `roster_incomplete` at 11:11:02.029331Z, "adopted dev,reviewer; no launch: AMQ refuses a fresh seat into the present binding (binding_present_without_resumable_conversation), so a task for a missing seat's pool blocks for the lead's relaunch (amq-squad scheduler relaunch)". dev2 had been closed at 09:45:40.903725Z, `seat_closed` with the reason `lease_expired` (decisions-since-soak-copy.jsonl).

decisions-after-restart.jsonl holds eight lines, from the stop on. By kind: 1 `scheduler_stopped`, 2 `seat_adopted`, 1 `scheduler_started`, 1 `roster_incomplete`, 1 `seat_prepare_failed`, 1 `decision_socket_open`, 1 `seat_prepare_suppressed`. None is a `seat_up` or a `roster_launched`, so the restart launched no seat. None is a `ledger_layout_migrated`, a `ledger_tampered` or a lease decision.

### The reviewer seat refused again after the restart

The adopted reviewer seat was refused at prepare again, for the same task and with the same reason as before the restart:

- Before the restart (decisions-since-soak-copy.jsonl): `seat_prepare_failed` for t179 at 09:51:20.543677Z, "the commit ledger of seat reviewer: seat_head_detached_unmerged: seat reviewer: the worktree's branch seat/reviewer-t178 holds an unrecorded commit 91081d70ea66e5c6c0f7747935328fc1467909a9; the harness never discards a local commit", then `seat_prepare_suppressed` at 09:51:22.35277Z.
- After it (decisions-after-restart.jsonl): `seat_prepare_failed` for t179 at 11:11:02.033259Z with the same reason text, then `seat_prepare_suppressed` at 11:11:02.938484Z.

This is the refusal of Known limit 35 of the 3.2.0 notes and of Things seen 4 of the rc.2 soak's record. A restart alone did not clear it, measured once here.

### The lead's first screen

Captured 12 s after `up` started (11:11:09Z, lead-first-screen.txt) and again 18 s later (11:11:27Z, lead-screen-30s.txt), with `tmux capture-pane` on session squad-lead-amq-squad-f42a4; at both the pane was `%153 squad-lead-amq-squad-f42a4 pid=6643 cmd=amq-squad dead=0`. The two captures are 24 lines each and differ in one line, the age in the dev2 permission line ("1h25m41s" in the first, "1h25m59s" in the second). Every non-blank line of the first but the two rule lines and the first line, which is the tail of pi's own tmux notice:

```
amq-squad: lead cto (pid 6643, pane %153) · root <home>/code/amq-squad/...
seats (4)
  cto  -  stopped
  dev  codex gpt-5.6-terra medium  idle  · up in 6.0s
  dev2  codex gpt-5.6-sol high  stopped  · up in 6.0s
    ! seat dev2 asked for a permission 1h25m41s ago this turn; if it is not m...
  reviewer  codex gpt-5.6-sol high  waiting: on your answer to its question  ...
  seat reviewer's prepare failure for t179 repeats; further repeats are count...
tasks (179)
train
  running: none
  last run: merged a PR, 2h ago; PRs #983; 1 merged
    ! release v3.2.0-rc.2 on omriariav/amq-squad has no accepted owner gate
    ! tag v3.2.0-rc.2 on omriariav/amq-squad has no accepted owner gate
waiting on you (0)
  Nothing is waiting on you.
----------
  dev2: seat_stalled_at_dialog: a harness dialog has been open since 2026-11-...
  seat_prepare_suppressed: reviewer the same prepare failure repeats; further...
~/code/amq-squad (v3)
```

The team screen is up and neither capture holds a "Failed to load extension" line (`grep -c` prints 0 for each). Each holds 0 ESC bytes.

## The relaunch from the lead's pane

Under the owner's "W - ok" and "L - i will approve, do it". By the lead session's approvals record the plan reviewer's word was "the word is given for all three, in that order", and the order run was W, R, L: dev2's worktree was emptied before the restart above, and the relaunch was typed after it.

### dev2's worktree emptied first

Why, by the approvals record's entry on the relaunch walk (a read of the code at the tag, not a run): "dev2's worktree holds 7 changed paths (09:59:03Z) and slots.go:445-446 refuses seat_worktree_dirty, so by the read the roster launch fails with the seats closed." At the tag, internal/schedproc/slots.go:445-446 returns `ReasonSeatWorktreeDirty` when the worktree has tracked or untracked changes ("the worktree has local changes; archive or remove them before launch").

dev2-empty.sh is the script and dev2-empty-out.txt its output, 11:10:47Z to 11:10:49Z (exit 0 by the approvals record):

- 0 processes had their working directory in dev2's worktree.
- Before: HEAD cc73ada1b4aba282c500b1ddd5203c05d52879af on refs/heads/seat/dev2-t178; the status was the seven lines the script expects, two modified tracked files and five untracked files.
- Saved outside the repository, in the job's scratch: a patch of the two tracked files and copies of the five untracked files. Their six sha256 lines are dev2-leftover-SHA256SUMS.txt. The saved files are not copied here; they are a cancelled attempt's leftover and this note verifies nothing in them.
- Then the two tracked files were restored from HEAD and the five untracked files removed; "removed the emptied directory docs/v3-spike/330-s1-2b-reasons".
- After: `porcelain lines: 0`; HEAD and branch as before.

### The line typed

By the lead session's approvals record: "no task in progress, exits queue 0; the one line typed into the lead's pane %153 by tmux send-keys at 11:13:03Z under "L - i will approve, do it" (no classifier refusal): !amq-squad scheduler relaunch --reason "the reviewer seat is refused at prepare (seat_head_detached_unmerged) and dev2 closed on a lease expiry; 3.2.0 cut"". relaunch-times.txt has `typed 2026-11-12T11:13:03Z`.

### The decisions

decisions-relaunch.jsonl, nine lines:

- `roster_relaunch_requested` at 11:13:10.990876Z, with the typed reason;
- `seat_closed` for dev (11:13:10.99696Z) and for reviewer (11:13:10.999791Z), each with the reason `roster_relaunch`;
- `roster_reloaded` at 11:13:11.003091Z, "squad.yaml compiled again at the relaunch's drain: a roster of 3 seats; added none; removed none (each closed in the drain, not launched again); at most 4 leases at once";
- `roster_launched` at 11:13:11.006831Z, "launch 0f8d18e46d01e76695f6de05a06ae55c: 3 seats in one apply";
- `seat_up` for dev (11:13:11.011026Z), reviewer (11:13:11.014936Z) and dev2 (11:13:11.019115Z);
- `dispatched` for t179 to dev2 (pool codex:gpt-5.6-sol:high) at 11:13:11.02288Z.

The nine lines hold no `seat_prepare_failed`. These three seats are the first that the 3.2.0 binary launched in session v31.

### The three bases, the worktrees, t179

By the lead session's approvals record, read at 11:13:44Z: "the three launch records' base 91081d70ea66e5c6c0f7747935328fc1467909a9; worktrees dev [seat/dev], reviewer [seat/reviewer], dev2 [seat/dev2-t179], each at 91081d70; no seat_prepare_failed after the relaunch; seat panes %154 dev pid 9371, %155 reviewer pid 9377, %156 dev2 pid 9384; the standing lead check now lists 24 pids". 91081d70ea66e5c6c0f7747935328fc1467909a9 is the commit the refusal named. The record's own words on it: "MEASURED NOW: a relaunch clears the reviewer seat's refusal (Things seen 4 said "not measured")." It is measured once.

The lead's screen after the relaunch (lead-screen-after-relaunch.txt; the file's time is 11:13:44Z; 24 lines, 0 ESC bytes, 0 "Failed to load extension" lines). Every non-blank line but the three rule lines:

```
amq-squad: lead cto (pid 6643, pane %153) · root <home>/code/amq-squad/...
seats (4)
  cto  -  stopped
  dev  codex gpt-5.6-terra medium  idle  · up in 5.1s
    ! seat dev asked for a permission 2s ago this turn; if it is not moving, ...
  dev2  codex gpt-5.6-sol high  starting on t179: 3.3.0 S1-2b, round 2, secon...
  reviewer  codex gpt-5.6-sol high  waiting: on your answer to its question  ...
tasks (179)
  t179 in progress on dev2: 3.3.0 S1-2b, round 2, second dispatch: carries b1...
  178 closed
train
  running: none
  last run: merged a PR, 2h ago; PRs #983; 1 merged
    ! release v3.2.0-rc.2 on omriariav/amq-squad has no accepted owner gate
    ! tag v3.2.0-rc.2 on omriariav/amq-squad has no accepted owner gate
waiting on you (0)
  Nothing is waiting on you.
----------
  dev: seat_stalled_at_dialog: a harness dialog has been open since 2026-11-1...
~/code/amq-squad (v3)
```

By the approvals record: "The squad runs on v3.2.0. v3 stays frozen for the lead's merges until t179's PR opens." What t179 did after its dispatch is not in this note.

## The soak conditions at the cut

The rc.2 soak's record (../release-v3.2.0-rc.2/soak.md) counted its conditions up to a copy of the decision log made at 2026-11-12T08:25:19Z, 21640 lines, and says of R5 that its second half "runs to the release cut, so it is to be counted again at the cut". recount-at-cut.txt is that count, the lead's run at 2026-11-12T10:12:49Z, before the prepare.

- The copy at the cut: 21654 lines, 4568538 bytes, sha256 02db73a7fea02e84698fe4855b312cc68e1ead21b21723c7648cfb13235afad6. Its first 21640 lines hash to 782b49a7833fb9367e5c1893d9865dcaad66968f670eca686f0e366692c00788, the soak record's copy. Both digests and both counts were computed again for this note from the copy in the job's scratch and match. The copy is not in this folder (4568538 bytes).
- Since the soak's copy: 14 lines, 2026-11-12T08:31:34.341521Z to 2026-11-12T09:51:22.35277Z, which are decisions-since-soak-copy.jsonl (equal to the copy's last 14 lines by `cmp`, run for this note). By kind: dispatched 3, lease_renewed 2, seat_prepare_suppressed 1, seat_prepare_failed 1, seat_closing 1, seat_closed 1, seat_cleaned 1, pr_bound 1, lease_expired 1, completed 1, commit_running 1.
- Since the restart on rc.2 (line 21544 on): 111 lines.
- The log still had 21654 lines at 11:10:54Z, before the restart on 3.2.0 (restart-pre.txt), so no decision was added between the recount and that restart.

### R3

The recount's lines for the four classes of R3 ("ledger_tampered on a sound chain, a lease lost under steady activity, a control byte on the screen, a train run ended by an escape byte", the plan's words as the soak's record gives them):

1. `ledger_tampered` since the restart: 0; "tamper" in kind or reason: 0.
2. `lease_expired` since the restart: 1 (2026-11-12T09:45:40.893326Z, t178, dev2). See below.
3. ESC bytes in the 111 lines: 0; `u001b`: 0. The four captures of the lead's pane in this folder hold 0 ESC bytes each (`LC_ALL=C grep -c` for the byte, run for this note).
4. The train's real runs since the copy: 1 (#983, mt-20261112T090533Z-cc593ff8, status merged); its four files hold 0 ESC bytes and 0 `u001b`.

### The one `lease_expired` line

The line, in decisions-since-soak-copy.jsonl: `lease_expired` for t178 on dev2 at 2026-11-12T09:45:40.893326Z, reason "expired". The three lines after it are `seat_closing` for t178 on dev2 (reason `lease_expired`), `dispatched` for t178 to the reviewer seat, and `seat_closed` for dev2 (reason `lease_expired`). t178 had been dispatched to dev2 at 08:59:33.057203Z and the decision log has one `lease_renewed` line for it, at 09:01:13.167137Z ("first activity renewal of attempt 1").

The facts, by the lead session's approvals record (its entry of 09:51:34Z): "Attempt 1 (dev2): granted 08:59:29Z, outcome expired, "lease expired at 2026-11-12T09:45:37Z"; decisions lease_expired 09:45:40.893326Z, seat_closing, seat_closed (dev2). dev2's hook ledger: last renewal 09:35:37Z, last line 09:36:31Z a Bash call of the one word `zsh`; no finished tool call in the 9 min 6 s before the expiry; cause not established; the seat's Codex transcript not read."

The plan reviewer's words on it, as the same entry quotes them: "R3 item 2 is 'a lease lost under STEADY activity', and this record shows the opposite of steady activity, so it is not that class on this record; the recount prints the line and these facts and says so in those words. If the Codex transcript is ever read by the owner and shows finished commands in those nine minutes, the reading changes then."

The prepare refusal that followed (the section "The reviewer seat refused again after the restart") is, in the plan reviewer's words as the approvals record quotes them (line 1679, its entry of 09:56:11Z headed "t179 NOT DISPATCHABLE"): "A defect of rc.2 and of v3's tip, outside R3's four classes; R3 stands, and the record names it as a defect found in the soak window".

### R5

`ledger_layout_migrated` in the whole log: 2 (2026-11-11T13:53:57.611166Z and 2026-11-11T14:05:35.731785Z), the two lines the soak's record names. `ledger_tampered` since the restart: 0. The eight lines of the restart on 3.2.0 and the nine of the relaunch add neither kind.

## Corrections found at the cut

Three sentences that were merged before the cut do not hold as written. Each was found at the cut, from the scratch run of 2026-11-12T11:07:19Z. The tag and the published release body are unchanged; each sentence stands where it is until a later docs PR carries the correction.

1. **RELEASING.md step 6, the scratch route, does not need `amq coop init`.** At v3.2.0 the step reads "(`git init` in an empty directory, then `amq coop init`; no `squad.yaml`; a scratch `HOME`)" (RELEASING.md:67-68); the init step is #985's change. That route has no `squad.yaml`, so the lead comes up solo: "With no squad.yaml it is the solo lead; its state lives under `~/.amq-squad/solo/` and nothing is written in the repository." (README.md:148-149). The two recorded runs show the same solo screen with and without the init: rc.2's run without it (../release-v3.2.0-rc.2/lead-first-screen-scratch.txt, root `/private/tmp/rc2lead.fmoVPO/home/.amq-squad/solo/proj-3006abf4cc53/root`) and this cut's run after it (root `/private/tmp/v320lead.nlK9Ni/home/.amq-squad/solo/proj-fdf7ce5aba1a/root`); each has "seats (1)" and the one row `lead  -  stopped`.
2. **Known limit 33 of the 3.2.0 notes holds for a repository that has a `squad.yaml`.** The sentence (docs/v3.2.0-release-notes.md:798) begins "`lead --team` in a repository where `amq coop init` has not run is refused at the roster launch with `base_root_unauthorized`" and does not state that condition. rc.2's scratch run is a repository where `amq coop init` had not run and that had no `squad.yaml`: its screen is the solo screen and shows no refusal. The limit's source, measurement 1b, ran in a repository that held a `squad.yaml` (item 3). The entry of docs/v3-followups.md that cites the limit (:358-361) opens with the same words.
3. **Measurements 1 and 1b of the rc.2 soak's record ran in a scratch repository that held a `squad.yaml`, written by the measurement's script, and the record does not say so.** The record says "a scratch repository with no `.amqrc` and no `.agent-mail`" (soak.md:239) and "a scratch repository with no `.amqrc`" (soak.md:257) and names no `squad.yaml` for either. What was read for this note, in the job's scratch:
   - Measurement 1b's record names the root `/private/tmp/ctl972ctl-codex-noinit.6SzIqX`. The prefix `ctl972` is the `mktemp` template of gate972/launch-ctl.sh:8, and its line 12 is `cat > "$S/proj/squad.yaml" <<YAML`; the document it writes has `lead: cto` and two seats, `cto` with `runtime: pi` and `dev` with the runtime given as the script's second argument. The record's provider line names codex.
   - Measurement 1's record names the root `/private/tmp/upmup-codex-noinit.ZL1c3o`. The prefix `upm` is the `mktemp` template of meas/up.sh:8, and its line 12 is the same `cat > "$S/proj/squad.yaml" <<YAML` with the same two seats.
   - The lead session's list of standing corrections (its section J) names gate972/launch.sh:12 and launch-ctl.sh:12. That line is the same `squad.yaml` line, but that script's template is `pin972` (:8), which is the root of neither record; the script whose template matches measurement 1's root is meas/up.sh.

Also measured at the cut: Things seen 4 of the rc.2 soak's record ends "Not measured: whether a relaunch clears it; a cancel or an expiry in place of the block." (soak.md:223), and Known limit 35 says "Whether a relaunch clears it is not measured." (docs/v3.2.0-release-notes.md:805). This cut measured it once: after the relaunch from the lead's pane the three launch records' base was the commit the refusal named and no `seat_prepare_failed` followed (the section "The relaunch from the lead's pane"); the restart alone did not clear it (the section "The reviewer seat refused again after the restart").

## Things seen

### 1. The permission system refused the lead session's publish

The lead session's run of the hand script was refused before it ran, with the reason "[Create Public Surface]", and nothing was created (the approvals record, quoted under "The owner's approval"). That entry's time is 10:19:22Z; the tag's start in the owner's run is 11:04:04Z.

### 2. `release prepare` posted the gate questions for a hand cut again

As at rc.2 (that note's Things seen 1): both gates have question_sent true, and the questions were posted at 10:13:21Z and 10:13:22Z with a 30-minute window (gate-questions.txt). By the lead session's approvals record: "The questions were posted to the owner's Buzz channel and under his answer A are NOT ANSWERED."

### 3. The lead's screen names rc.2's tag and release as having no accepted owner gate

All three captures of the squad's own lead on the 3.2.0 build print, under "train":

```
    ! release v3.2.0-rc.2 on omriariav/amq-squad has no accepted owner gate
    ! tag v3.2.0-rc.2 on omriariav/amq-squad has no accepted owner gate
```

No line of the three names v3.2.0's own tag or release. This note records the lines and no cause.

### 4. dev2's rows at the restart

dev2 was closed at 09:45:40.903725Z (`seat_closed`, `lease_expired`). At the restart (`up` started at 11:10:57Z), up-320.json's roster row for dev2 has state `stale`, live false and reason_code `no_live_signal`, its launch object has outcome `live`, and its warnings hold "seat_stalled_at_dialog: a harness dialog has been open since 2026-11-12T09:45:28Z (1h25m34s); the seat waits at it and AMQ holds its doorbell". The first screen prints the row as `dev2  codex gpt-5.6-sol high  stopped  · up in 6.0s` with the line "! seat dev2 asked for a permission 1h25m41s ago this turn; if it is not m...". This note records the rows and no cause.

### 5. The dev seat's row after the relaunch

The screen after the relaunch prints, under the dev seat, "! seat dev asked for a permission 2s ago this turn; if it is not moving, ..." and, below the rule, "dev: seat_stalled_at_dialog: a harness dialog has been open since 2026-11-1...". What followed is not in this note.

## The files

Each file but this note is a copy of a record in the job's scratch or an extract made for this note. In every copy the home path is replaced by the placeholder `<home>`; the count of replacements is beside each file that had one. A script copied here is therefore the script as it ran but for that path. Trailing blanks were stripped from four lines of restart-pre.txt, and a final newline was added to tag-create.json; no other byte of a copy differs from its source. The folder holds 0 lines with a home path, 0 ESC bytes and 0 lines with a trailing blank. The lines, bytes and sha256 values below were measured after the last edit of each file.

- build-smoke.sh: 12 lines, 664 bytes, sha256 7d7715735ead28c0d99beed185034ed5596e73e1e7a72ccd551838a318f6842b. The build and smoke script. 2 placeholders.
- build-times.txt: 7 lines, 142 bytes, sha256 552a7189b2caad596659c2fd3e98c075b0388c187f610125edd74493c99455e6. The build's and the smoke's times and exits, the built binary's version, the porcelain count.
- build.log: 9 lines, 682 bytes, sha256 3ccf16556d7055014b0385cd66429ccf6896944894198abe1de53901e1f4c484. The output of `scripts/build-pi-runtime.sh all`. 1 placeholder.
- cut-binary-version.txt: 1 line, 22 bytes, sha256 f9f6d83509ba87aaabe82e7f4db7933cd30a9fdd93585174eb44c3115af8c26b. The installed binary's version at the prepare.
- decisions-after-restart.jsonl: 8 lines, 1902 bytes, sha256 59b6f47f27dc6f1ebb7c17c67dc8144a9ce8e7d869b232144abb5000e3de35ad. The scheduler's eight decision lines from the stop on. 1 placeholder.
- decisions-relaunch.jsonl: 9 lines, 1202 bytes, sha256 b960e5cccf62d048e9e9361061aea22d92bf0e528ccda66a34b5db62b53cee98. The nine decision lines of the relaunch.
- decisions-since-soak-copy.jsonl: 14 lines, 2318 bytes, sha256 80de5c6711851fb8834c75e183f96537736c91e3c4e064865bf06b3465339df1. The 14 decision lines after the soak record's copy, up to the cut.
- describe.txt: 1 line, 7 bytes, sha256 2cd4ab5444b04c763f9864518ade0005512c803496726444ad14630adffd6844. `git describe --tags --exact-match` in the build's worktree.
- dev2-empty-out.txt: 27 lines, 1379 bytes, sha256 0914f176c69e6a7e464a096fa22aa7cbad9d0d619802888cec2e8ec7ddfeecd6. The output of dev2-empty.sh (the job's dev2-empty.out, renamed because the repository ignores `*.out`).
- dev2-empty.sh: 38 lines, 2782 bytes, sha256 a1e851d538489862e192a7569de4f4d39033f0eeba98bc121d648fd342cf08e1. The script that saved and removed dev2's leftover. One of the three placeholders stands for the escaped form of the path in the script's awk pattern. 3 placeholders.
- dev2-leftover-SHA256SUMS.txt: 6 lines, 672 bytes, sha256 7f105ddd2a77deffda6a57f61a5f2448f751c1da73034f1353671787c0e8aa0d. The six sha256 lines of the saved leftover (the job's dev2-leftover/SHA256SUMS).
- doctor-warnings.txt: 6 lines, 1815 bytes, sha256 f135b6b59ac0cf0d2d194d00c965e07f57cbdfd3dd9c5821329d4a929cfd2002. An extract made for this note: one heading line, then the five warn checks of the doctor document after the restart. 3 placeholders.
- gate-questions.txt: 9 lines, 858 bytes, sha256 f8dba98477108fdc65863fcc30285bcc8dc29ac974e087659aff38ca44791126. The lead session's reading of the two gates and their questions, as it is.
- lead-first-screen-scratch.txt: 30 lines, 1071 bytes, sha256 cfe9181b32b803fb7f1a3cfd4065abc8028e07c84fc319b04d5ccbdeabe1c47b. The scratch route's first screen at 10 s.
- lead-first-screen.txt: 24 lines, 1473 bytes, sha256 cbe81cfe4b8d9b76fbad1d5a220869e96decd9f4775da0ab523842bd687a347e. The squad's own lead, 12 s after `up` started; 1478 bytes as captured, before the placeholder. 1 placeholder.
- lead-screen-30s.txt: 24 lines, 1473 bytes, sha256 33600764f6ca4c5a1c5592c0f42e116d270c806a893c8e69e959bf8b7ee15bd3. The same pane 18 s later; 1478 bytes as captured, before the placeholder. 1 placeholder.
- lead-screen-after-relaunch.txt: 24 lines, 1616 bytes, sha256 1a97aadf602eb9fc97f9c64a8968df24e116a0f92ba2c204c68811d1bf287065. The same pane after the relaunch; 1621 bytes as captured, before the placeholder. 1 placeholder.
- owner-approvals-excerpt.txt: 39 lines, 15274 bytes, sha256 29faea00fe3cecf5efeb73acc376f4d948b9f67f3b8b796d2d19b66c64b450ea. Whole lines 14, 15, 17, 1675, 1679, 1682, 1683, 1685, 1687, 1689, 1691, 1693, 1695, 1697, 1698, 1699, 1700, 1702 and 1704 of the lead session's approvals record, each after its line number, under one heading line. 11 placeholders.
- prepare-time.txt: 1 line, 21 bytes, sha256 84de33905debf543388c31d5beb113066aefb992bcc3093eb7d8b1bd40e56404. The prepare's start.
- prepared-note.txt: 3 lines, 207 bytes, sha256 0be7b8330004ea5cb3008d831385652602f1d71c280217a3e9b531d730a221ea. The prepared record's state, release id and note.
- publish-by-hand-out.txt: 7 lines, 252 bytes, sha256 b292bb87de739d54482b61e5a703fd742da616316cebb3585d937c30619c00cd. The output of the owner's run of publish-by-hand.sh (the job's publish-by-hand.out, renamed).
- publish-by-hand.sh: 23 lines, 1423 bytes, sha256 8e3fa9183d7e15f61d455e2243a740f52d171525d967cc147cace67f1e8d5b8c. The hand steps' script. 3 placeholders.
- recount-at-cut.txt: 11 lines, 1103 bytes, sha256 f670b878c71f2e4177c46c12864f63f61774da12b9ac54f4dc90a64f7f0b7307. The soak conditions counted again at the cut.
- relaunch-times.txt: 1 line, 27 bytes, sha256 0fb0911b50d24f24205ca2345358480e39abf4325c6887b2411bddc4105078a7. The time the relaunch line was typed.
- release-api-fields.json: 13 lines, 451 bytes, sha256 7f5c1b727732bd5f97dddf6530f53653673bae1ff074fa7b1e10b33943079424. Made for this note with jq: ten fields of the lead session's saved API record and the body's sha256.
- release-check.log: 18 lines, 1037 bytes, sha256 251baf62fc4de570924f036cec617ced6305d2fc37b4729b1d69352aaeebbe23. The release check's output before the prepare. 4 placeholders.
- release-create-out.txt: 1 line, 59 bytes, sha256 4f9fd9378bdda3c0c0dbadfa52cbc1c46e7a017b21f8ed8f1665476a5c9734bf. The output of `gh release create` (the job's release-create.out, renamed).
- release-prepare.json: 22 lines, 633 bytes, sha256 690a4c84c79636bb04faaee38f38bcf0245855d6d678f521e780d73db62ac20e. The prepare's JSON output.
- release-smoke.log: 20 lines, 1111 bytes, sha256 51e704b4f62f51489b507f5f65221e93eff0fc40890077c81e8bfe6353c60039. The output of `make release-smoke VERSION=v3.2.0`. 4 placeholders.
- restart-lead.sh: 64 lines, 2946 bytes, sha256 745cd9a7125b31c119b82610c6d15595492963ccd50675b1461854dee4104597. The restart script. One of the nine placeholders stands for the escaped form of the path in the script's awk pattern. 9 placeholders.
- restart-pre.txt: 60 lines, 4203 bytes, sha256 9b50600b9d59c57a78f65d36242078eee056dad51884cf590843d090beb3a6fb. The state before the restart; trailing blanks stripped from four lines. 17 placeholders.
- restart-run.txt: 12 lines, 586 bytes, sha256 6eaf6cc9278c2e3d17830d0e087f914d596faa3f30e55b81e70feb89c96bb26e. The restart's times, the two captures' pane lines, `up`'s exit, the new lead and scheduler. 3 placeholders.
- rollback.txt: 7 lines, 772 bytes, sha256 e6ebb5a2fb432e71b885357cc2a6f2a6086328d4925c46ce0ddd178bbe67b571. The rollback line written before the restart. 3 placeholders.
- scratch-coop-init.txt: 12 lines, 347 bytes, sha256 feb4fff70c5e3f9f4907624004f3468ddd135192defae1e15c74558084c1fcec. The output of `amq coop init` in the scratch repository.
- scratch-lead-run.txt: 11 lines, 805 bytes, sha256 c3f780acbcfdb99b578fccb21ba4ca7712793adcf2e5df9ba9afa8e8353c0040. The scratch route's run record. 4 placeholders.
- scratch-lead.sh: 37 lines, 2407 bytes, sha256 11428810d71ea9a5c3aea3432a50fa4c0803238bbf11e2b7ce2153ca370a8adc. The scratch route's script. 3 placeholders.
- skew-check.md: 26 lines, 1151 bytes, sha256 2ec599898a1072e063cdf30f0f98a4655630ae8fb5d9064b00efa2adaa3c7e30. Made for this note: the skew check's commands and their output.
- tag-create.json: 1 line, 340 bytes, sha256 fe3b01fca8e7635e2412f71031da63fa35cced9fa82ba8340965b7bcaecb0028. GitHub's answer to the tag call; a final newline added (339 bytes in the job's scratch).
- up-320.json: 277 lines, 8866 bytes, sha256 27c11198bf9c28ea043ab843eef72d173f66e894663461a12cb6ba0234ec6832. The JSON output of `up --binary`. 14 placeholders.

Not copied: the copy of the decision log at the cut (21654 lines, 4568538 bytes, its sha256 above); published-body.md and notes-at-tag.md (the notes file at the tag, 63690 bytes each, sha256 above); the lead session's whole API record (66465 bytes, sha256 58f24d69284585fc88bd706de122c6cad63dbf8f3b0ead2cef5cabd6ecfdf6c5; release-api-fields.json is the selection); the whole doctor document (8065 bytes, sha256 2390bdb8f9a3c131ac4bf198fc675bca99f51a5d56c193c613308b6a43eb1404; doctor-warnings.txt is the extract); the saved leftover of dev2's worktree; the six files of the gate store's gates directory; the two 0-byte stderr files; the lead session's approvals record as a whole.
