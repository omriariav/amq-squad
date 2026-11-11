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

The rollback line (rollback.txt), written from those two sections for the restart of the squad's own lead, which has not run (see step 6): before this build has written a commit or exits ledger in session v31, stop the rc.2 lead and run `/Users/omri/scratch-v320rc1/build/pi/amq-squad up --binary /Users/omri/scratch-v320rc1/build/pi/amq-squad`, which prints amq-squad v3.2.0-rc.1; once a raw-layout head or an authenticated interrupted migration exists, `up --binary` refuses every target before v3.2.0-rc.2 with `ledger_layout_downgrade`, and the way is forward or a new session.

## The build

Built from the tag in the scratch worktree /Users/omri/scratch-v320rc2 (detached at da109b49 before the tag existed; `git describe --tags --exact-match` there prints v3.2.0-rc.2 after it, describe.txt) with `scripts/build-pi-runtime.sh all` (build.log; build-times.txt: 13:19:29Z to 13:19:47Z, exit 0): the pi-1.3.0-node-22.21.1-darwin-arm64 payload, 6448 files, payload sha256 1c14cc226626231e26ac27e10c02dbee1d62fe514fc250c339653cd2657b43f4 and manifest sha256 266cfb9f8e54f2b191843bf7ba90bc5782d519a5926a5c1ea1e71a83e048a92a; binary 73209858 bytes at `build/pi/amq-squad`. `build/pi/amq-squad version` prints `amq-squad v3.2.0-rc.2` (build-times.txt). The worktree had 0 porcelain lines after the build and the smoke.

`make release-smoke VERSION=v3.2.0-rc.2` (release-smoke.log, exit 0, done 13:20:12Z): "release metadata matches v3.2.0-rc.2", the release tests OK (6 and 4), then `go install github.com/omriariav/amq-squad/v3/cmd/amq-squad@v3.2.0-rc.2` into a temporary GOBIN, whose binary prints `amq-squad v3.2.0-rc.2`.

## Step 6: the lead's first screen, the scratch-repository route

RELEASING.md step 6 gives two routes: restart the squad's own lead on the new binary, or run `amq-squad lead --team -- --offline` in a scratch repository. This note records the second. The squad's own lead has not been restarted: at 13:44Z it was still the rc.1 lead of 2026-11-07 (pid 26009, `/Users/omri/go/bin/amq-squad lead --team`), and the installed binary is still v3.2.0-rc.1. The restart and the install wait for the owner's word, asked at 15:20 IST; ending a live lead is his to approve. Under RELEASING.md step 6 the release does not count as done until the installed binary is the rc.2 build and the squad's own lead runs on it; this note closes the first-screen record only.

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

What this route does not show is the squad's own session on rc.2: the adopted roster, the scheduler's first pass over session v31, and the first rc.2 ledger write there, after which the rollback line in rollback.txt applies. Those are recorded when the owner's restart runs.

## Things seen

### 1. `release prepare` posts the gate questions even for a hand cut

The prepare posted both gate questions to the owner's Buzz channel (question_sent true; gate-questions.txt has the two events' ids and times), with a 30-minute answer window, though the cut was to be by hand and no attach ran to accept a reply. The owner was told in the session that they need no answer. The plan reviewer named it a 3.2.x item: a hand cut under answer A should run the prepare with the gate questions suppressed, or the notes' owner steps should say the questions arrive and need no answer.

### 2. The notes' prerelease step assumes the flow

The notes' "Owner and lead steps for 3.2.0-rc.2" say the release flow never marks a release as a prerelease, so the owner marks it after the publish. In a hand cut `gh release create` takes `--prerelease --latest=false` itself; the edit after it changed nothing.

## The files

All copied from the job's scratch records through `go run ./scripts/redact-evidence` (rule 18; it replaced no value, and `-check` is clean):

build.log, build-times.txt, cut-binary-version.txt, describe.txt, gate-questions.txt, lead-first-screen-scratch.txt, notes-body.sha256, owner-approvals-excerpt.txt, prepare-time.txt, prepared-note.txt, publish-by-hand-out.txt (the job's publish-by-hand.out, renamed because the repository ignores `*.out`), publish-by-hand.sh, release-api.json, release-check.log, release-prepare.json, release-smoke.log, rollback.txt, scratch-lead-run.txt, scratch-lead.sh, skew-check.md, squadid-expected.txt, tag-create.json. owner-approvals-excerpt.txt holds whole lines 8, 9, 10, 14, 15, 17, 1327 and 1328 of the job's approvals record; the published body is not copied (it is the notes file at the tag).
