# amq-squad v3.1.2 release evidence (2026-11-05)

- Tag: v3.1.2 -> 5bd394e6dcf9fd06254f80ebc846d92130dc9048, the merge commit of the hotfix PR #883 (parents e10bbef4, #882's merge on v3, and d0ba09a1, #883's head). The tag commit and #883's merge commit are the same commit: the release was tagged on the merge, with no bump PR of its own, because #883 carried the 3.1.2 bump. `git rev-parse 'v3.1.2^{}'` after `git fetch --tags` prints that commit. Cut by the installed binary, `/Users/omri/go/bin/amq-squad version`, which printed at prepare and at publish:

  ```
  amq-squad v3.1.0-rc.1
  ```

  That binary ran both prepare and publish (R54: the installed binary cuts). It was rc.1 because the squad had run on rc.1 since the rollback from 3.1.1: the lead of 3.1.0 and 3.1.1 did not start (docs/v3.1.2-release-notes.md).
- Release: https://github.com/omriariav/amq-squad/releases/tag/v3.1.2, GitHub's own values (`gh api repos/omriariav/amq-squad/releases/tags/v3.1.2`, release-api.json): created_at 2026-11-05T13:26:18Z and published_at 2026-11-05T13:47:27Z, draft false, prerelease false, target_commitish 5bd394e6dcf9fd06254f80ebc846d92130dc9048. created_at equals the target commit's date (5bd394e6's author and committer time is 2026-11-05T13:26:18Z); it is GitHub's value, not the publish time. The publish at 13:47:27Z came 28 s after the second approval, inside the gates' window. `gh api releases/latest` returns v3.1.2, so it is the latest release.
- Before prepare: `make release-check VERSION=v3.1.2` at the tag commit, in the scratch worktree /Users/omri/scratch-v312, without the marketplace flag: "release metadata matches v3.1.2". The main join had not happened (`git merge-base --is-ancestor origin/v3 origin/main` false), as for 3.1.0 and 3.1.1.
- The notes file is docs/v3.1.2-release-notes.md at the tag, sha256 85753101403e757224b77a552eebeb35029373c2fa6a878375b35f59b257cbe3 (notes-body.sha256), 4802 bytes. It was both prepare's and publish's `--notes-file`, with the gate note "release body set from docs/v3.1.2-release-notes.md at 5bd394e6; notes sha256 85753101403e757224b77a552eebeb35029373c2fa6a878375b35f59b257cbe3". `git show 5bd394e6:docs/v3.1.2-release-notes.md | shasum -a 256` prints the same digest.

## One gate series

- Series v312. The session root `/Users/omri/code/amq-squad/.agent-mail/v312` was created before prepare, identity `root-sha256:40cdbe00f4023bddc337014fb837a976f522b4f1d9364004d6e3304fccc14c06` (squadid-expected.txt). release-prepare.json: release id release-v1-b363535d8722c2f5860064afb943fcadb4f81dc9dfd969142fd83fbae2d5b3af, state active, gates rel-tag-20b52d1d21e64a8c3472 ("git tag v3.1.2 5bd394e6dcf9fd06254f80ebc846d92130dc9048 on omriariav/amq-squad") and rel-gh-b6fa8a1269d1cbefdf9c ("gh release create v3.1.2 on omriariav/amq-squad"), both with question_sent true. The questions were raised at about 13:46:30Z with a 31-minute window, by the lead session's account; release-prepare.json carries no times.
- The owner's approvals (attach-v312.jsonl, the attach on the mailbox "user"), each a reply in its gate's thread:
  - rel-tag approved at 13:46:47Z ("accepted rel-tag-20b52d1d21e64a8c3472 tag", event 3607aaebad098210b3f21a1f6767face5b8266153632f491ab49703f601f4579, authorization authz-v1-41b413c3946f4b21f2cd1e3be010a9e435f486b511a13c1c4c05f2ac9d04c73c);
  - rel-gh approved at 13:46:59Z ("accepted rel-gh-b6fa8a1269d1cbefdf9c github_release", event cb7395a3c3af7cdbf0055d859685edce93cff1dc26edb9ab28be3b00e2e6d620, authorization authz-v1-4b3b184de6dd3845af9f1b993812b8034e6fb842446d91d5cc4a5d6ee79e6ffe).

  Both came within about 30 s of the questions, inside the window. No reply was refused and no second series was needed (3.1.1 needed two: a plain DM and a lapsed window).

## The attach and the owner's approvals

- The attach swap, as it ran (attach-swap.txt). One attach may hold the mailbox. The v31 attach (pid 95237) was stopped at 13:46:13Z, and the v312 attach (pid 10605) started the same second: attach-v312.jsonl's first line is its "attached" record at 13:46:13Z (mailbox user, operator true). After publish the v312 attach was stopped at 13:48:03Z (its "detached" record) and the usual v31 attach was started again at 13:48:03Z (pid 12715). The amq-remote endpoint (pids 20200 and 20202; the gate replies' message ids carry pid20202) ran throughout, so no `amq-remote up` was needed.
- The owner's approvals of the route, quoted from the lead session's AskUserQuestion at 13:46:13Z (its own chat, not a squad log; owner-approvals.txt records the time and the two choices). The two gate acceptances approve the tag and the release; these answers approve the route.
  - The question: "Do you approve the swap and are you at Buzz now?" The owner chose "Approve: do the swap now (Recommended)", described as: "I stop pid 95237, start the v312 attach, run prepare, and tell you when the two gate questions are on Buzz. You reply yes in each thread. Publish follows on the accepted gates; the v31 attach is restored right after."
  - The question: "Do you approve installing 3.1.2 over the amq-squad on PATH and restarting the squad on it (`up --binary`, which stops the rc.1 lead, the scheduler and both codex seats; …)?" (the "…" is the lead session's elision of the question's rest). The owner chose "Approve the restart (Recommended)", described as: "I run up --binary from the tag build; the squad comes back on 3.1.2; the lead's first screen goes into the release evidence as step 6."
- The publish run: from the lead session, as the lead's own step, exit 0, with nothing on stderr. The tag step ran (result 5bd394e6dcf9fd06254f80ebc846d92130dc9048) and the github_release step ran (result https://github.com/omriariav/amq-squad/releases/tag/v3.1.2). release-publish.json holds the output; its error field is empty.

## The published body

Checked after publish with `gh api repos/omriariav/amq-squad/releases/tags/v3.1.2` (the `body` field, decoded as UTF-8; the response is release-api.json): 4870 bytes. The first 4802 bytes are the notes file at the tag byte for byte (`cmp` equal). The 68 bytes after them are two newlines and the release flow's own marker line `<!-- amq-squad-release-nonce: <32 hex> -->` of 66 bytes, with no newline at its end. Nothing else differs.

## The known-issue line on 3.1.0 and 3.1.1

Covered by the owner's choice A. The 495-byte line in known-issue-312.md ("Known issue (2026-11-05): on this release the lead does not start …", with the link to v3.1.2 and the rc.1 workaround) was prepended to the bodies of the v3.1.0 and v3.1.1 releases with `gh release edit`:

| Release | Body before | Body after | Check |
| --- | --- | --- | --- |
| v3.1.0 | 43280 bytes | 43775 bytes | the published body equals the after file; its first 495 bytes are known-issue-312.md and the rest is the before body byte for byte; the nonce line is kept |
| v3.1.1 | 37605 bytes | 38100 bytes | the same |

The first `gh release edit`, at about 13:47Z, rewrote each body unchanged, because the line file was at the wrong path. The second, at about 13:48Z, applied it. The before, after and published bodies were compared in the job's scratch records and are not copied here: each is a whole release body, and the table states every byte that changed.

## The build

Built from the tag in the scratch worktree /Users/omri/scratch-v312 with `scripts/build-pi-runtime.sh all` (build.log): the pi-1.3.0-node-22.21.1-darwin-arm64 payload, exit 0, payload sha256 31613f1e96b91f8713ad3def5baa03afcf413d0d84fd1b1deaf102251fb1d75c and manifest sha256 78652b333d893f8c6d9ac96e18dacef1a88886c8e12ac5ecc9e556db3d1f7f89, the same digests as the hotfix branch's build (docs/v3-spike/312-evidence/hotfix-hostfacts.md); binary 72345074 bytes at `build/pi/amq-squad`; `amq-squad version` from it prints `amq-squad v3.1.2`.

`make release-smoke VERSION=v3.1.2` (release-smoke.log): the release check and its tests OK, then `go install github.com/omriariav/amq-squad/v3/cmd/amq-squad@v3.1.2` into a temporary GOBIN, whose binary prints `amq-squad v3.1.2`.

## Step 6: the installed lead's first screen

RELEASING.md step 6, its first run on a cut.

- 13:48:58Z, `up --binary` from the tag build (up-312.json): compile ok (3 seats, lead cto, session v31); install ok ("installed /Users/omri/scratch-v312/build/pi/amq-squad (v3.1.2) at /Users/omri/go/bin/amq-squad by temp name and rename"); session skipped (the root exists); doctor ok (39 checks, none failed, 5 warnings); lead refused `lead_running` ("a lead of /Users/omri/code/amq-squad/.agent-mail/v31 is running (pid 42373, tmux pane %125); one lead per root"). That lead was the rc.1 lead. `up` never stops a live lead.
- 13:49:23Z, the same refusal again (up-312-2.json, the same steps and detail, start 13:49:23Z). At the same time `amq-squad down` was tried and refused, "unknown command: \"down\"" (down-v31.err): 3.x has no down verb.
- 13:50:12Z, with the owner's restart approval quoted above, SIGTERM to the rc.1 lead, pid 42373. The lead and its scheduler (pid 42375) ended, and its tmux pane closed.
- 13:50:13Z, `up --binary` again (up-312-3.json), exit 0, nothing on stderr: compile, install, doctor ok as before; lead ok ("no recorded lead or scheduler is running"); tmux ok ("tmux session squad-lead-amq-squad-f42a4 runs /Users/omri/go/bin/amq-squad lead --team in /Users/omri/code/amq-squad"); roster ok ("roster_adopted: every roster seat is live from the previous incarnation; no launch"), so both codex seats kept running. The lead started at 13:50:17Z.
- 13:50:34Z, 17 s after the lead started, the pane's first screen (lead-first-screen.txt, trimmed below; the file has the whole capture):

  ```
   amq-squad: lead cto (pid 15270, pane %128) · root
   /Users/omri/code/amq-squad/.agent-mail/v31
   seats (3)
     cto  -  stopped
     dev  codex gpt-5.6-terra medium  idle  · up in 3.7s, first message 80.9s
     reviewer  codex gpt-5.6-sol high  starting on t71: 3.2.0-1: a seat's lease
   renews from the seat's own activi...  · up in 3.6s
     seat reviewer could not be prepared for t71; ask the lead to relaunch the
   roster
   tasks (71)
   ...
  ~/code/amq-squad (v3)
  0.0%/131k (auto)                                            qwen3.6:27b • medium
  ```

  The team screen is up and there is no "Failed to load extension" line. The lead (pid 15270) and its scheduler (pid 15272) both run /Users/omri/go/bin/amq-squad, which prints `amq-squad v3.1.2` (the lead session's process listing). The 3.1.0 and 3.1.1 lead exited before this screen.
- Seen on the screen and recorded, not a release matter: "seat reviewer could not be prepared for t71; ask the lead to relaunch the roster". t71 is a 3.2.0 task (a seat's lease renewal), and the warning is the squad's work, not the release's. The pane's tmux "extended-keys is off" warning is the owner's tmux configuration.

## The main join

PR #884, "Join v3.1.2 into main", from branch main-312 into main, after the tag, as #881 was for 3.1.1. Head a831b0ac2f2c37169bc134a5b16383d00f2c6b12, a merge commit of v3.1.2 into main with parents e9950d00ac9123811ea058083f82a25ce6ed1a7a (origin/main, #881's merge) and 5bd394e6dcf9fd06254f80ebc846d92130dc9048 (`v3.1.2^{}`). Its tree is 6726bd2d0c8b94433400f01df97d61b83d7c60ea, equal to the tree of v3.1.2 (`git rev-parse 'origin/main-312^{tree}' 'v3.1.2^{tree}'` prints that one value twice), so main carries the tagged tree. `make release-check VERSION=v3.1.2 MARKETPLACE_REF=HEAD` on that head prints "release metadata matches v3.1.2".

## The files

All copied from the job's scratch records through `go run ./scripts/redact-evidence` (rule 18; it replaced no value, and `-check` is clean):

attach-swap.txt, attach-v312.jsonl, build.log, down-v31.err, known-issue-312.md, lead-first-screen.txt, notes-body.sha256, owner-approvals.txt, release-api.json, release-prepare.json, release-publish.json, release-smoke.log, squadid-expected.txt, up-312.json, up-312-2.json, up-312-3.json. release-view.json was empty in the records and is not copied; its values (targetCommitish, draft, prerelease) are in release-api.json.
