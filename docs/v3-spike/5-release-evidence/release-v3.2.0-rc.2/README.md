# amq-squad v3.2.0-rc.2 release evidence (2026-11-11)

- Tag: v3.2.0-rc.2 -> da109b496787ffb7b8a716b8d2d75af19e98a045, the merge commit of the release PR #963 ("release: 3.2.0-rc.2 (the bump, the notes, the 3.2.x list)"). Its parents are e27dca44f638d47fd4df2c9cbe35a9b029a30ea5 (v3 before it) and 75e95278a6833bbb9d6a7beb703f41753aebb8a3 (#963's head). `git rev-parse 'v3.2.0-rc.2^{}'` after `git fetch origin tag v3.2.0-rc.2` prints that commit. The tag is a lightweight tag (`git cat-file -t v3.2.0-rc.2` prints `commit`), as the release flow makes it.
- **Cut by hand, with the two signed gates not answered.** This is the owner's choice, recorded under "The owner's approval" below, and the one exception to RELEASING.md's "Never tag by hand". RELEASING.md is unchanged.
- `release prepare` still ran, by the installed binary /Users/omri/go/bin/amq-squad, which was v3.2.0-rc.1 (cut-binary-version.txt, read at 13:13:58Z, 94 s after the prepare; R54: the installed binary cuts). `release publish` did not run.
- Release: https://github.com/omriariav/amq-squad/releases/tag/v3.2.0-rc.2, GitHub's own values (`gh api repos/omriariav/amq-squad/releases/tags/v3.2.0-rc.2`, release-api.json): created_at 2026-11-11T13:08:23Z, published_at 2026-11-11T13:18:56Z, draft false, prerelease true, target_commitish da109b496787ffb7b8a716b8d2d75af19e98a045, author omriariav. created_at equals the target commit's date (da109b49's author and committer time is 2026-11-11T15:08:23+02:00); it is GitHub's value, not the publish time.
- The release was created with `--prerelease --latest=false`, and `gh release edit v3.2.0-rc.2 --prerelease --latest=false` was run after it as the notes' owner step says (exit 0). release-api.json, read after the edit, has prerelease true. `gh api repos/omriariav/amq-squad/releases/latest` still returns v3.1.2.
- The notes file is docs/v3.2.0-release-notes.md at the tag, 57250 bytes, sha256 c6bfa2d2e508ab13edeb6dc71dbae9f3f456f5ca4344bd588e9ab2bb6e45525d (notes-body.sha256). `git show da109b49:docs/v3.2.0-release-notes.md | shasum -a 256` prints the same digest.
- `make release-check VERSION=v3.2.0-rc.2` on the tag commit, before the prepare: exit 0 (release-check.log: "release metadata matches v3.2.0-rc.2", the release tests OK, 6 and 4).
- Where a sentence below names no record in this folder, it is the lead session's account of what it ran and saw at the time (a command's exit code, a process listing, a notice sent). owner-approvals-excerpt.txt is the lead session's own record too, not a squad log.
- No main join: v3.2.0-rc.2 is a pre-release. `git merge-base --is-ancestor da109b49 origin/main` is false.

## The owner's approval

The words are the owner's, typed in the lead session or chosen in its AskUserQuestion; they are in owner-approvals-excerpt.txt as the lead session recorded them, not in a squad log.

- 2026-11-09, typed: "i approve anything no need to ask me in buzz just get there", and later the same day: "leave these siganutes... i approve... lets get to 3.3.0  with a great amq-squad working!!". The lead session read the second as: the release cuts on the way to 3.3.0 do not wait for his signed Buzz reply.
- 2026-11-10, about 00:05 IST, asked which way he wants rc.2, 3.2.0 and 3.3.0 cut, he chose "A: I cut, no Buzz answers". The option's text was: "I run `release prepare`, then tag and publish by hand under your gh login. You are told before each publish. The evidence note records that the two signed gates were not answered, with your words."
- The plan reviewer's conditions for a cut under A, by the lead session's account: `release prepare` still runs; the hand steps use the prepared digest and a tag commit read from `git rev-parse`; the release is marked a prerelease; the published body is recorded equal to the notes file; a hook or permission refusal stops the cut and goes to the owner; the owner is told before each publish; this note says the gates were not answered and names the exception.
- Told before the publish: at 15:11 IST (13:11Z) the lead session wrote in the session and sent a push notification that it was about to cut v3.2.0-rc.2 on da109b49 by hand under his gh login with the gates not answered. No word from the owner came before the publish at 13:18:52Z. The times of the notice are by the lead session's account.
- By the lead session's account: no signature was forged, no key material was touched and the verifier was not changed, and no hook or permission refusal occurred at any step of the cut. The folder holds no audit that would show these; the commands the cut ran are the ones in the records.

## One gate series, not answered

- Series v320rc2. The release session root `/Users/omri/code/amq-squad/.agent-mail/v320rc2`, identity `root-sha256:025bb25dc9d5ccd7c76efb896e56b6bdc6b23fef120dd39170d6334a3808abf2` (squadid-expected.txt). The squad itself ran on the v31 session root throughout; v320rc2 carried only the release gates.
- release-prepare.json (the prepare started at 13:12:24Z, prepare-time.txt; exit 0): release id release-v1-e72218c1db92b6da89d46653b7b0ce1d1e23f69c28528533d21eb483c1a90735, state active, gates rel-tag-254e753c28e695988a6e ("git tag v3.2.0-rc.2 da109b496787ffb7b8a716b8d2d75af19e98a045 on omriariav/amq-squad") and rel-gh-455253546caa2cd48ec4 ("gh release create v3.2.0-rc.2 on omriariav/amq-squad"), both with question_sent true.
- The prepared body digest, read from the prepared record's note in the compound-release store (prepared-note.txt, read after the cut): `release body sha256:c6bfa2d2e508ab13edeb6dc71dbae9f3f456f5ca4344bd588e9ab2bb6e45525d`, equal to the notes file's digest. The hand script checked the notes file against it again before either call.
- gate-questions.txt, read from the session's gate store after the cut: both gates were issued at 13:12:29Z with not_after 13:42:29Z; the tag question was posted at 13:12:29Z and the release question at 13:12:30Z; neither gate has an answer file. **Both gates are NOT ANSWERED.** No attach ran for session v320rc2, so no reply could have been accepted, and the attach swap of the rc.1 cut was not done.

## The hand steps

publish-by-hand.sh is the script as it ran, publish-by-hand-out.txt its output (the times and the read-back below are from it). The tag call is the release flow's own (internal/releaseflow/gh.go:77-79). The release call is not the flow's (:116-118, a `gh api` POST with draft=true): it is `gh release create` with the same tag, target commit, name and body.

1. It checks `git rev-parse origin/v3` equals da109b496787ffb7b8a716b8d2d75af19e98a045 and the notes file's sha256 equals the prepared digest; either mismatch ends it before any call.
2. The tag, 13:18:52Z to 13:18:54Z: `gh api -X POST repos/omriariav/amq-squad/git/refs -f ref=refs/tags/v3.2.0-rc.2 -f sha=<the commit>` (tag-create.json: the ref, object type commit, that sha). Read back: `commit da109b496787ffb7b8a716b8d2d75af19e98a045`.
3. The release, 13:18:54Z to 13:18:57Z: `gh release create v3.2.0-rc.2 --repo omriariav/amq-squad --verify-tag --target <the commit> --title v3.2.0-rc.2 --notes-file <the notes file> --prerelease --latest=false`. Unlike the flow, it created the release published, not as a draft that is then published, and it added no nonce marker.

Both calls ran under the owner's gh login (`gh auth status`: account omriariav).

## The published body

Checked with release-api.json (the `body` field, decoded as UTF-8): 57250 bytes, equal byte for byte to `git show da109b49:docs/v3.2.0-release-notes.md`. There is no marker line after it: rc.1's 68 extra bytes were the release flow's nonce marker, and the flow did not create this release.

## The skew check

skew-check.md, before the prepare. `git diff --stat 3966afb72b85e1ab6c0fc8c879f1c5ef03f92a40 da109b496787ffb7b8a716b8d2d75af19e98a045 -- internal/routing internal/scheduler internal/sealchain internal/seatcommit internal/task` (v3.2.0-rc.1 to the tag commit) touches 70 files, +14225/-435; 25 of them are not test files, and skew-check.md lists them. `git diff --stat e27dca44 da109b49` over the same five directories is empty: #963 changes none of them.

Unlike rc.1, the result is not "no sealed struct changed". rc.2 changes the sealed layout: the commit and exits ledgers move to the raw-byte layout, and `FirstRawLayoutVersion` is "v3.2.0-rc.2" (internal/sealchain/raw.go:30). What each older build answers when it meets the new layout, and what `up --binary` refuses, is measured in docs/v3-spike/320-rc2-rollback/execution.md (rows M1-M5 and P1-P15) and stated in the notes' "Upgrading from rc.1 or 3.1.2" and "Rolling back" sections. This note adds no cross-version claim of its own.

The rollback line (rollback.txt), written from those two sections for the restart of the squad's own lead, which had not run when this line was written (see step 6): before this build has written a commit or exits ledger in session v31, stop the rc.2 lead and run `/Users/omri/scratch-v320rc1/build/pi/amq-squad up --binary /Users/omri/scratch-v320rc1/build/pi/amq-squad`, which prints amq-squad v3.2.0-rc.1; once a raw-layout head or an authenticated interrupted migration exists, `up --binary` refuses every target before v3.2.0-rc.2 with `ledger_layout_downgrade`, and the way is forward or a new session.

## The build

Built from the tag in the scratch worktree /Users/omri/scratch-v320rc2 (detached at da109b49 before the tag existed; `git describe --tags --exact-match` there prints v3.2.0-rc.2 after it, describe.txt) with `scripts/build-pi-runtime.sh all` (build.log; build-times.txt: 13:19:29Z to 13:19:47Z, exit 0): the pi-1.3.0-node-22.21.1-darwin-arm64 payload, 6448 files, payload sha256 1c14cc226626231e26ac27e10c02dbee1d62fe514fc250c339653cd2657b43f4 and manifest sha256 266cfb9f8e54f2b191843bf7ba90bc5782d519a5926a5c1ea1e71a83e048a92a; binary 73209858 bytes at `build/pi/amq-squad`. `build/pi/amq-squad version` prints `amq-squad v3.2.0-rc.2` (build-times.txt). The worktree had 0 porcelain lines after the build and the smoke.

`make release-smoke VERSION=v3.2.0-rc.2` (release-smoke.log, exit 0, done 13:20:12Z): "release metadata matches v3.2.0-rc.2", the release tests OK (6 and 4), then `go install github.com/omriariav/amq-squad/v3/cmd/amq-squad@v3.2.0-rc.2` into a temporary GOBIN, whose binary prints `amq-squad v3.2.0-rc.2`.

## Step 6: the lead's first screen, the scratch-repository route

RELEASING.md step 6 gives two routes: restart the squad's own lead on the new binary, or run `amq-squad lead --team -- --offline` in a scratch repository. This note records the second. The squad's own lead has not been restarted: at 13:44Z it was still the rc.1 lead of 2026-11-07 (pid 26009, `/Users/omri/go/bin/amq-squad lead --team`), and the installed binary is still v3.2.0-rc.1. The restart and the install wait for the owner's word, asked at 15:20 IST; ending a live lead is his to approve. Under RELEASING.md step 6 the release does not count as done until the installed binary is the rc.2 build and the squad's own lead runs on it; this note closes the first-screen record only. Added after the restart: since 2026-11-11T13:53:57Z the installed binary is the rc.2 build and the squad's own lead runs on it; the record is the section "Step 6: the squad's own lead on the installed rc.2 build" below.

- The run is the lead session's own (scratch-lead.sh, as it ran; scratch-lead-run.txt), 13:44:12Z to 13:44:24Z, with the scratch repository RELEASING.md names and otherwise in the form of the 3.1.2 note (docs/v3-spike/312-evidence/hotfix-hostfacts.md, "Load proof: the built lead in tmux"): a new directory under /private/tmp with an empty `git init` repository and no `squad.yaml`, a scratch `HOME`, a tmux server on its own socket, and `env -i` with `HOME`, `PATH=/usr/bin:/bin:/usr/sbin:/sbin`, `TERM` and `GIT_CEILING_DIRECTORIES` only (no `amq` on PATH, no key). The command is `/Users/omri/scratch-v320rc2/build/pi/amq-squad lead --team -- --offline`, the binary built from the tag above, which prints `amq-squad v3.2.0-rc.2`.
- At 3 s and at 10 s the pane was alive: `dead=0 cmd=amq-squad` (pane pid 5041 at 10 s).
- The first screen at 10 s (lead-first-screen-scratch.txt; below is every non-blank line of it but the two rule lines):

  ```
  amq-squad: lead - · root /private/tmp/rc2lead.fmoVPO/home/.amq-squad/solo/proj-3006abf4cc53/root
  seats (1)
    lead  -  stopped
  tasks (0)
    No tasks yet; add one with /add.
  train
    No merge train has run yet.
  waiting on you (0)
    Nothing is waiting on you.
  /add  /reset  /dismiss  /answer  /widen  /team hide
  /private/tmp/rc2lead.fmoVPO/proj (main)
  0.0%/131k (auto)                                                                qwen3.6:27b • medium
  ```

  The team screen is up and the capture holds no "Failed to load extension" line (`grep -c` prints 0).
- The tmux server was then killed; afterwards no process named the scratch directory or the tag build's binary, and none had its working directory there (the lead session's `ps` and `lsof -d cwd` after the run; scratch-lead-run.txt holds the listing for the scratch directory only).

What this route does not show is the squad's own session on rc.2: the adopted roster, the scheduler's first pass over session v31, and the first rc.2 ledger write there, after which the rollback line in rollback.txt applies. Those are recorded when the owner's restart runs. Added after the restart: they are in the section "Step 6: the squad's own lead on the installed rc.2 build" below.

## Step 6: the squad's own lead on the installed rc.2 build

The owner's word, typed in the lead session at 15:52 IST (13:52Z) on 2026-11-11 in answer to the restart ask (dated 15:20 IST under the scratch-repository route above): "Approved all. Restart what u need". The lead session read it as approval to end the squad's own lead with SIGTERM and to run `up --binary` with the tag build, and not as approval for anything the permission system refuses, for typing into a pane, or for any outward message; at 15:56 IST it also read a `scheduler relaunch` from its own shell as inside those words (owner-approvals-excerpt-restart.txt: three whole lines copied from the job's approvals record, which stays in scratch).

The run is the lead session's own (restart-lead.sh, as it ran; restart-pre.txt, restart-run.txt, up-320rc2.json, restart-post.txt):

- Before, at 13:53:53Z (restart-pre.txt): the installed binary printed `amq-squad v3.2.0-rc.1`; the lead was pid 26009 and its scheduler pid 26011, both started 2026-11-07 18:55:34 IST; sixteen processes had their working directory in a seat worktree, the two Codex seats (pids 18592 and 18598) and their children; status read "1 needs you, 0 working, 1 ready, 162 inactive"; the scheduler's decisions log had 21542 lines.
- SIGTERM to pid 26009 at 13:53:54Z. The lead and the scheduler were gone 1 s later; the scheduler's last line is `scheduler_stopped` at 13:53:54.388896Z. No SIGKILL was sent; the script stops without one if either pid outlives 30 s.
- `/Users/omri/scratch-v320rc2/build/pi/amq-squad up --binary /Users/omri/scratch-v320rc2/build/pi/amq-squad --json` started at 13:53:55.309533Z and was collected with exit status 0 at 13:54:25Z, after the second screen capture (restart-run.txt; restart-lead.sh waits for both captures before it collects the status). Its stderr file, up-320rc2.err in the job's scratch, is 0 bytes and is not copied. Its steps (up-320rc2.json): compile ok, "4 seats, lead cto, session v31"; install ok, "installed /Users/omri/scratch-v320rc2/build/pi/amq-squad (v3.2.0-rc.2) at /Users/omri/go/bin/amq-squad by temp name and rename"; session skipped, the root exists; doctor ok, "41 checks, none failed (5 warnings)"; lead ok, "no recorded lead or scheduler is running"; tmux ok; roster ok with `roster_incomplete` (below).
- After: `/Users/omri/go/bin/amq-squad version` prints `amq-squad v3.2.0-rc.2`, and its sha256 equals the tag build's, e978bdeff8e5df78631090f39f4eb8208804b7a89f97917ba4a99bb9914491df (the lead session's `shasum -a 256` of both at 14:08Z; no record file). The lead is pid 15222 on pane %149 and its scheduler pid 15228, both started 15:53:57 IST (restart-run.txt, restart-post.txt).

Doctor's five warnings (doctor-warnings.txt, from `amq-squad doctor --json` at 13:55Z, 36 ok and 5 warn) are the registry file out of date, five unverified pi registry entries, the amq-remote login service absent (two checks) and eleven old pi runtime versions. None is a Codex hook trust warning, so the notes' step 4 was not needed on this host. Step 5 is for an upgrade from 3.1.2 with a task in progress; this was an upgrade from rc.1 with none (working 0).

### The adopted roster

The scheduler adopted the two seats that were alive: `seat_adopted` for dev (pool codex:gpt-5.6-terra:medium, pid 18592) at 13:53:57.635341Z and for reviewer (pool codex:gpt-5.6-sol:high, pid 18598) at 13:53:57.639076Z, each with the reason "alive from a previous incarnation; its accepted proof's pid and start stamp match the live process". Both were launched on 2026-11-10T14:06:08Z (up-320rc2.json, each roster row's launch timing), when the installed binary was the rc.1 build, so neither has been through rc.2's launch path; the sixteen worktree processes are the same sixteen at 13:59:57Z (restart-post.txt). So this record shows no seat launched by the rc.2 binary; that is for the soak's record.

The third seat of the working squad.yaml, dev2, was not launched: `roster_incomplete` at 13:53:59.129387Z, "adopted dev,reviewer; no launch: AMQ refuses a fresh seat into the present binding (binding_present_without_resumable_conversation), so a task for a missing seat's pool blocks for the lead's relaunch (amq-squad scheduler relaunch)". See Things seen 4.

### The scheduler's first pass over session v31

decisions-after-restart.jsonl is the log from the stop on: lines 21543 to 21555, thirteen lines, and no line was added between 13:53:59Z and 13:59:57Z (restart-post.txt counts 21555). By kind: 1 `scheduler_stopped`, 1 `ledger_layout_migrated`, 2 `seat_adopted`, 1 `scheduler_started` ("incarnation 6aaf2fd93ca7", 13:53:57.642097Z), 6 `seat_clean_superseded`, 1 `roster_incomplete`, 1 `decision_socket_open`. The six `seat_clean_superseded` lines drop queued clean items of earlier attempts (t11, t22, t24, t54, t58, t64) because the seat holds a later lease; each says "nothing is closed or cleaned for it". No refusal, no `ledger_tampered`, no lease decision.

Status at 13:54:53Z (`amq-squad status --session v31 --json`, producer version v3.2.0-rc.2; the file stays in the job's scratch: 251293 bytes, sha256 660c7a48e045cd9c28055bfcd6d5fba9d13e7fb2be2d8d0e8db30fd7c14b77b5): counts all 164, needs_you 1, working 0, ready 1, inactive 162; 161 task rows, 91 completed and 70 cancelled; the lead object is handle cto, pid 15222, pane %149, recorded_at 2026-11-11T13:53:57.549956Z, live true; the scheduler object is pid 15228, started_at 2026-11-11T13:53:57.599413Z, launch_start 2026-11-11T13:53:55.309533Z, live_seats 2, live true. The one needs_you item is the reviewer seat's question of 2026-11-05 on thread gate/t94-cli-test-scope, which was there before the restart. The three warnings are in Things seen 3.

### The first rc.2 ledger write

One line, once: `ledger_layout_migrated` at 13:53:57.611166Z, ledger_kind exits, seq 409881, "legacy exits ledger migrated to raw-bytes-v1 at link 409881". `scheduler/exits.json` was 226 bytes before and is 230 after (restart-pre.txt, restart-post.txt). From that line on the rollback line in rollback.txt applies in its second form: `up --binary` to a build before v3.2.0-rc.2 is refused with `ledger_layout_downgrade`. No rollback attempt is recorded here.

The restart itself wrote no per-seat commit ledger: `commits/dev.json` (10852 bytes) and `commits/reviewer.json` (269978 bytes) have the same sizes and times before and after (restart-pre.txt, restart-post.txt). The reviewer seat's ledger migrated at the dispatch of its first rc.2 task, not at a commit: `ledger_layout_migrated` at 14:05:35.731785Z, ledger_kind commits, seat reviewer, seq 454, "legacy commit ledger migrated to raw-bytes-v1 at link 454", half a second before the `dispatched` line for t162 at 14:05:36.252341Z (decisions-first-dispatch.jsonl, lines 21556 to 21558 of the same log; the third is t162's first activity renewal). The dev seat's ledger had not been written by rc.2 when this record closed; its line, if one comes, is for the soak's record.

### The lead's first screen

Captured 12 s after `up` started (13:54:07Z, lead-first-screen.txt) and again at 30 s (13:54:25Z, lead-screen-30s.txt), with `tmux capture-pane` on session squad-lead-amq-squad-f42a4; at both the pane was `%149 pid=15222 cmd=amq-squad dead=0`. The two captures are equal byte for byte. Every non-blank line but the two rule lines and the first, which is the tail of pi's own tmux notice:

```
amq-squad: lead cto (pid 15222, pane %149) · root /Users/omri/code/amq-squad...
seats (3)
  cto  -  stopped
  dev  codex gpt-5.6-terra medium  idle  · up in 3.4s, first message 39007.6s
  reviewer  codex gpt-5.6-sol high  waiting: on your answer to its question  ...
  the roster came up without seat dev2; ask the lead to relaunch the roster
tasks (161)
  161 closed
train
  running: none
  last run: merged a PR, 3h ago; PRs #961; 1 merged
waiting on you (0)
  Nothing is waiting on you.
  ! the gate store could not be read; its error is below the line
----------
  roster_incomplete: dev2 adopted dev,reviewer; no launch: AMQ refuses a fres...
  train last run: mt-20261111T104912Z-0a791de1 merge
  gate store: gate clear-0a227ca8d88dc97c65c540a4d0fa18a7 is a legacy quarant...
~/code/amq-squad (v3)
0.0%/131k (auto)                                            qwen3.6:27b • medium
```

The team screen is up and neither capture holds a "Failed to load extension" line (`grep -c` prints 0 for each).

### A relaunch from outside the lead is refused

At 13:55:56Z the lead session ran `amq-squad scheduler relaunch` from its own shell (with the session's root, a one-sentence reason and `--json`; the command line was not saved), to bring dev2 in. The build refused it: exit 15, `not_lead`, "pid 18008 does not descend from the lead cto's recorded incarnation" (relaunch.json, relaunch-err.txt, relaunch-times.txt). It was not retried. The lead-origin check held on the live squad: only a process that descends from the recorded lead may relaunch the roster. The relaunch from the lead's pane is the owner's.

## Things seen

### 1. `release prepare` posts the gate questions even for a hand cut

The prepare posted both gate questions to the owner's Buzz channel (question_sent true; gate-questions.txt has the two events' ids and times), with a 30-minute answer window, though the cut was to be by hand and no attach ran to accept a reply. The owner was told in the session that they need no answer. The plan reviewer named it a 3.2.x item: a hand cut under answer A should run the prepare with the gate questions suppressed, or the notes' owner steps should say the questions arrive and need no answer.

### 2. The notes' prerelease step assumes the flow

The notes' "Owner and lead steps for 3.2.0-rc.2" say the release flow never marks a release as a prerelease, so the owner marks it after the publish. In a hand cut `gh release create` takes `--prerelease --latest=false` itself; the edit after it changed nothing.

### 3. The lead's screen says the gate store could not be read when three legacy records were skipped

On the squad's own first rc.2 screen (Step 6, the squad's own lead): "! the gate store could not be read; its error is below the line". The store was read. Status carried three warnings and no read error (gate-store-warnings.txt): `gate store: gate clear-<id> is a legacy quarantine_clear gate (a kind this build no longer carries); skipped`, for the ids 0a227ca8d88dc97c65c540a4d0fa18a7, a48ee38fac1fd3503e010e1c66554a58 and a93731e99c650d475b3a19ed6854f9a6. `internal/cli/status.go:98` gives a skipped legacy record the same `gate store: ` prefix as a failed read (:93), and `pi/extensions/lib/screen.ts:336-342` prints the could-not-be-read line when a warning starts `gate store:` and no gate alert takes the line (lines on da109b496787ffb7b8a716b8d2d75af19e98a045). The shared prefix is in every release since v3.1.0 (`git tag --contains 0f566282` for `internal/gatestore/legacy.go`; v3.1.0's status.go:93 and :98), where the screen printed the warning itself, shortened, after "refusal: " (v3.1.0's screen.ts:169-171 and :104-106). The could-not-be-read sentence is in every release since v3.1.1 (screen.ts:271 in v3.1.1 and v3.1.2; v3.2.0-rc.1's screen.ts:294-299). The fix is the first task of the rc.2 soak, t162 of session v31, dispatched to the reviewer seat at 14:05:36.252341Z (decisions-first-dispatch.jsonl).

### 4. dev2 was not launched: `roster_incomplete`

The squad.yaml that `up` compiled ("4 seats, lead cto") names three seats besides the lead: dev, reviewer and dev2. It is the lead session's working copy, an uncommitted edit (`git status --porcelain squad.yaml` prints ` M squad.yaml` there; no record file); the committed squad.yaml names other seats. `up` adopted the two live seats and launched nothing, with the words quoted under "The adopted roster". Those words are the scheduler's own fixed sentence, not an answer AMQ gave in this run: `internal/scheduler/roster.go:307-325` logs `roster_incomplete` when some roster seats are live and some are missing, and tries no launch, on the ground its comment gives at :15-23, that AMQ v0.116.2 refuses a fresh seat into a present binding (the reason's name is the constant at :83). A missing seat comes in only with a relaunch of the whole roster, the lead's verb, and here the owner's to ask from the lead's pane. This run did not establish why dev2 was absent before the restart or what that relaunch returns.

## The files

All copied from the job's scratch records through `go run ./scripts/redact-evidence` (rule 18; it replaced no value, and `-check` is clean):

build.log, build-times.txt, cut-binary-version.txt, describe.txt, gate-questions.txt, lead-first-screen-scratch.txt, notes-body.sha256, owner-approvals-excerpt.txt, prepare-time.txt, prepared-note.txt, publish-by-hand-out.txt (the job's publish-by-hand.out, renamed because the repository ignores `*.out`), publish-by-hand.sh, release-api.json, release-check.log, release-prepare.json, release-smoke.log, rollback.txt, scratch-lead-run.txt, scratch-lead.sh, skew-check.md, squadid-expected.txt, tag-create.json. owner-approvals-excerpt.txt holds whole lines 8, 9, 10, 14, 15, 17, 1327 and 1328 of the job's approvals record; the published body is not copied (it is the notes file at the tag).

Added with the record of the restart: decisions-after-restart.jsonl, decisions-first-dispatch.jsonl, doctor-warnings.txt, gate-store-warnings.txt, lead-first-screen.txt, lead-screen-30s.txt, owner-approvals-excerpt-restart.txt, relaunch.json, relaunch-err.txt, relaunch-times.txt, restart-lead.sh, restart-post.txt, restart-pre.txt, restart-run.txt, up-320rc2.json. doctor-warnings.txt and gate-store-warnings.txt are the lead session's extracts, each with one heading line, of the doctor and status documents; neither document is copied, and the status document's size and sha256 are in the section above. The lead ran `go run ./scripts/redact-evidence` over the folder, which replaced no value, and `go run ./scripts/redact-evidence -check` on it exits 0.

Added with the record of the soak (soak.md): decisions-soak.jsonl, soak-task-table.md, lead-screen-2026-11-12.txt, measure-up-noinit-run.txt, measure-up-noinit-decisions.jsonl, measure-claude-token-run.txt, measure-claude-token-decisions.jsonl, deadcode-liveness.txt, measure-lead-noinit-run.txt, measure-lead-noinit-decisions.jsonl, measure-lead-noinit-doctor.txt.
