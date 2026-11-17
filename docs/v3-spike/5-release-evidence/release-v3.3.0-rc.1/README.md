# amq-squad v3.3.0-rc.1 release evidence (2026-11-16 to 2026-11-17)

- Tag: v3.3.0-rc.1 -> 312a27d3eeb5652d126c59768e3d60426413132b, the merge commit of the
  release PR #1025 into `release/v3.3` (tag-create.json: the ref, object type commit,
  that sha). It is a lightweight tag, as the hand script makes it.
- **Cut by hand, with the two signed gates raised and not answered.** This was the
  owner's answer A, "A: I cut, no Buzz answers" (2026-11-10), reaffirmed 2026-11-12:
  "I approve you to do anything you need to deliver 3.3.0". It is the same exception
  to RELEASING.md's "Never tag by hand" that rc.2 and 3.2.0 used. For this cut the
  owner answered "Yes, publish now" in the lead session.
- `release prepare` ran with the installed binary, which was v3.2.0
  (cut-binary-version.txt; prepare-time.txt 2026-11-16T09:36:14Z; R54: the installed
  binary cuts). `release publish` did not run.
- Release: https://github.com/omriariav/amq-squad/releases/tag/v3.3.0-rc.1.
  release-api-fields.json holds GitHub's values: draft false, prerelease true,
  target_commitish 312a27d3…, author omriariav, created_at 2026-11-16T09:35:53Z (the
  target commit's date) and published_at 2026-11-16T10:16:04Z.
  `gh api …/releases/latest` still returned v3.2.0 (publish-by-hand-out.txt).
- The notes file is `docs/v3.3.0-release-notes.md` at 312a27d3, 14221 bytes, sha256
  d9b9e6ff8f86a9733ffa33a55355fb6b587592415781b95d5553c24b45490676.
- Where a sentence names no file in this folder, its source is the lead session's
  own approvals record (T/v320rc1/owner-approvals.txt, entries of 2026-11-16 and
  2026-11-17), not a squad log.

## The owner's approval

- 2026-11-10: "A: I cut, no Buzz answers"; 2026-11-12: "I approve you to do anything
  you need to deliver 3.3.0" (the 3.2.0 evidence note, "The owner's approval").
- 2026-11-16, in the lead session's AskUserQuestion, each its own question:
  - "Yes, publish now" for v3.3.0-rc.1 under route A;
  - "Yes, install it" and "Yes, restart the lead";
  - "Yes, relaunch now";
  - "Yes, restart it" for the phone attach;
  - "Yes, start now" for each phone test, and "Yes, run it with cues".
- No permission refusal stopped any step of this cut.

## One gate series, raised and not answered

- release-prepare.json: release id release-v1-372835783b96f2c7…, state active, and
  two gates, both with question_sent true:
  - rel-tag-11b8b5bab5ce10a849d8 ("git tag v3.3.0-rc.1 312a27d3… on
    omriariav/amq-squad");
  - rel-gh-e9647e4675429cd8ffc3 ("gh release create v3.3.0-rc.1 on
    omriariav/amq-squad").
- The prepared body digest (prepared-note.txt, gate-questions.txt) is sha256:d9b9e6ff…,
  equal to the notes file.
- gate-status.json, read at the cut: both gates open. **Both gates are NOT
  ANSWERED**, as route A needs.
- Found later (the phone test, below): both records carry the missing-root squad
  identity. The fresh session v330rc1 had no root when v3.2.0's `release prepare`
  raised them, and they cannot be answered (`no_squad_identity`). Route A needs no
  answer, so the cut was unaffected. The hash is under "The phone test".

## The hand steps

publish-by-hand-out.txt is the output of the hand script, in 3.2.0's form:
- the tag, 10:15:59Z to 10:16:02Z, read back `commit 312a27d3…`;
- the release, 10:16:03Z to 10:16:04Z (release-create-out.txt: the URL), with
  `--prerelease --latest=false`;
- read back: prerelease true, target 312a27d3, latest v3.2.0.

## The published body

Read back from GitHub: 14221 bytes, sha256 d9b9e6ff…, BYTE-EQUAL to the notes file at
312a27d3 (release-api-fields.json `body_bytes`, and the lead session's sha256 of both).

## The build

`scripts/build-pi-runtime.sh all` from the tag in a scratch worktree: exit 0 in 20 s
(build-build-times.txt). The binary is at `build/pi/amq-squad`, not at `pi/amq-squad`
as the plan said. It prints `amq-squad v3.3.0-rc.1` (build-version.txt), sha256
5c2557edfeae1353d530e684efce6ae61af5e9c713a0ba410f6c4c5eafa11469
(build-binary.sha256). `make release-smoke VERSION=v3.3.0-rc.1`: exit 0, and the
module-proxy install prints v3.3.0-rc.1 (build-smoke.log).

## Step 6: the install and the squad's own lead on rc.1

- Before the install:
  - the installed v3.2.0 was copied to a backup, sha256 0e6b6b09… (rollback.txt has
    the rollback line);
  - a read-only check of the live store's 20 stored answers (17 typed tuples and 3
    plain `yes`) found none that 3.3.0's word list refuses.
- Installed at 2026-11-16T16:20:25Z; the installed sha256 equals the build's.
- Restart (restart-pre.txt, restart-run.txt, up-330rc1.json), on the owner's word:
  - SIGTERM to the 3.2.0 lead, pid 6643, at 16:21:04Z; the lead and scheduler
    were gone in 1 s, with no SIGKILL;
  - `up --binary <tag build> --json` from 16:21:05Z, exit 0: compile ok (4 seats,
    lead cto, v31); install ok by temp name and rename; session skipped; doctor
    41 checks, none failed (5 warnings); lead ok; tmux ok; `roster_adopted` (no
    launch);
  - the new lead is pid 62484 on pane %166, with scheduler 62486.
- The lead's first screen (lead-first-screen.txt, lead-screen-30s.txt) has no
  `Failed to load extension` line. It shows the four expected `no accepted owner
  gate` lines for rc.2's and 3.2.0's tags and releases (route A).

## The relaunch (bound ii)

- On the owner's word, the roster relaunch was typed into the lead's pane at
  16:23:55Z.
- decisions-relaunch.jsonl:
  - `roster_relaunch_requested` 16:24:01Z;
  - `seat_closed` dev, dev2 and reviewer;
  - `roster_reloaded` (3 seats, none added or removed);
  - `roster_launched cf6d2c3b9e7865d82d9727449c55885d` (3 seats in one apply);
  - `seat_up` dev, reviewer and dev2, launched by the rc.1 scheduler.
- The worktree process count went 24 -> 23 -> 24 (relaunch-pids-before,
  -after and -later, with no pid in common across the relaunch). No process was
  missing. The 23 was counted just before one seat's `codex-code-mode-host`
  started at 16:24:09Z. At 16:24:50Z the count was 24: 8 per seat, the same shape
  for dev, dev2 and reviewer. It was a timing artifact of the count.

## The phone attach ran v3.1.2 code

- The live phone attach, pid 66439, was started on 2026-11-07: the restore of the
  v31 attach in 3.2.0-rc.1's attach swap. Its process start and its `attached`
  row are 16:44:04Z (attach-restart.txt); the 3.2.0-rc.1 note's attach-swap.txt
  records the restore at 16:44:07Z. It ran from `<home>/go/bin/amq-squad` while that file was still
  v3.1.2. The 3.1.2 lead was stopped at 16:46:03Z and 3.2.0-rc.1 was installed
  after.
- So 66439 ran v3.1.2 code in memory through 3.2.0-rc.1, rc.2, 3.2.0 and the first
  hours of 3.3.0-rc.1. Its text inode was 99571722; the installed file's inode is
  262532815.
- Its command line: `remote attach --fleet-file <home>/scratch-owner45/fleet.yaml
  --project <home>/code/amq-squad --session v31 --me cto --interval 3s --json`.
  It was started by nohup from a lead job's shell, with no launchd item.
- Restarted onto rc.1 on the owner's word (attach-restart.txt): SIGTERM 66439 at
  2026-11-16T16:50:00Z; the new pid 18360 has text inode 262532815, the installed
  rc.1. Its log (attach-v31-rc1.jsonl) starts with the `attached` line, and its
  stderr is 0 bytes.
- The 3.2.0 evidence note makes no claim about which code the phone path ran: its
  Buzz statements are about the release gates' questions only. This note records
  the finding alone.

## Presence reads stale; the endpoint is the share root

`remote status` printed "user: not present (stale)" and a warning that amq-remote's
login service is absent for root `<home>/.amq/remote/root` (attach-presence.txt). A
read-only check of the rc.1 source and the host found both cosmetic for delivery:

- The `user` line is the operator row of the fleet status document
  (`internal/buzz/presence.go:69-141`; `live` false, `:33`; `no_live_signal`,
  `internal/status/classify.go:479`). `status.OperatorSeat` builds it
  (`internal/status/fleet.go:47-61`) from the fleet file's operator root,
  `<home>/scratch-owner45/operator/.agent-mail/ops`, a leftover rehearsal fleet.
  Its only signal is `agents/user/presence.json`, last written 2026-10-30, so the
  row has read stale since then, whatever the binary.
- Presence only starts and stops the attach's own `amq-remote up` child
  (`internal/buzz/endpoint.go:29-41`). The attach reads the share's inbox every tick
  regardless (`internal/cli/remote.go:640-656`).
- The 2026-11-07 log has no `presence` row. The copy in the 3.2.0-rc.1 folder
  (`release-v3.2.0-rc.1/attach-v31-restored.jsonl`) has two rows: `attached`, and
  `handled` at 17:20:18Z (merge-e8c88d76dad1a873, accepted). The live file gained
  a `detached` row when the attach stopped on 2026-11-16. So that answer was
  delivered while the row read stale.
- The relay runs the share root: `amq-remote up` and `serve` both run
  `--root <home>/.amq-squad/buzz/root`, the same root as the gate binding's
  `share_root`. `<home>/.amq/remote/root` does not exist, and launchd holds only the
  Buzz desktop app.
- DEFECT (3.3.x): `remote status` calls `ServiceWarning(…, "")` with an empty root
  (`internal/cli/remote.go:508`). So it checks amq-remote's default root, not the
  share root.

## The phone test (bound iv): four attempts, satisfied in part

Each attempt was on the owner's word. Before each attach swap, no gate was open in
v31 or owner45 (iv-pre-check.txt). iv-swap.txt records each swap's stop and start
times and pids, and each restore of the v31 attach. By the lead session's approvals
record, each restored v31 attach had text inode == rc.1, an `attached` line and an
empty stderr.

1. Attempt 1, 16:56Z: stopped before any gate.
   - The planned scratch attach `remote attach --project … --session rc330win --me
     cto` exited at once (iv-attach-rc330win.err): "the fleet registry has no
     operator session (amq-squad fleet operator), or attach to one squad with
     --lead" (`internal/cli/remote.go:343`).
   - The rehearsal fact behind that command form was false: the rehearsal fixture
     had a fleet operator.
   - v31 was restored at 16:56:40Z, down 18 s. Nothing was sent to the owner.
2. Attempt 2, 16:58Z: the live command's form with `--session rc330win` (operator
   mode with the fleet file; iv-attach-rc330win-2.jsonl `attached`).
   - `gate raise soak-window-1` at 16:58:17Z (iv-raise.json), with a 2-minute window.
   - The owner replied at 16:58:42Z, inside the window. The attach handled it at
     16:58:45Z: `refused soak-window-1 no_squad_identity`.
   - The cause: the record's squad (iv-gate-record-soak-window-1.json) is
     `root-sha256:c703a2e9a2f819f10d4e47023b86f812f9ac23cc7139a94d2e4c8e9eba92967e`,
     which equals sha256("missing\x00" + `<home>/code/amq-squad/.agent-mail/rc330win`).
     That is the missing-root form (`internal/gatestore/squad.go:16-25`): the raise
     took the store's identity before the new session's root existed
     (`internal/cli/gate_store.go:99`).
   - The rc.1 release gates have the same form. Their squad,
     `root-sha256:0e1ecb33bca7c925b83033b5b2a819830c8ee16650cdb09e53b8035b107804e7`
     (gate-status.json), equals sha256("missing\x00" +
     `<home>/code/amq-squad/.agent-mail/v330rc1`).
   - v31 was restored at 16:59:22Z, down 76 s.
3. Attempt 3, 17:02Z: `soak-window-2` (iv-raise-2.json).
   - The record's squad is `root-sha256:bb709a3a…`, checked not to be the missing
     form before the owner's turn.
   - The owner replied `yes` at 17:02:16Z, inside the window. Handled at 17:02:21Z:
     approved (`authz-v1-f889263586c6…`), reply `accepted soak-window-2 spawn
     approved` (iv-attach-rc330win-3.jsonl).
   - The owner also pasted "amq-squad: running / The request reached the session;
     its start is not proven (this share accepts submitted evidence)". That is
     amq-remote's interim status text for the same request (input `yes`, minimum
     evidence `submitted`), followed by its result `accepted soak-window-2 spawn
     approved`. It is upstream status text, not a second outcome.
   - v31 was restored at 17:02:49Z, down 53 s.
4. Attempt 4, 17:05Z: `soak-window-3` (iv-raise-3.json), real squad identity.
   - The owner replied `yes` at 17:05:01Z, 16 s after the raise and before any cue,
     inside the window. Handled at 17:05:06Z: approved (`authz-v1-f64fb68da3b0…`),
     `accepted soak-window-3 spawn approved` (iv-attach-rc330win-4.jsonl).
   - The owner confirmed he had replied at once.
   - v31 was restored at 17:05:17Z as pid 8076, down 40 s.

iv-gate-status-rc330win.json (read after attempt 3) shows soak-window-1 expired and
soak-window-2 accepted. iv-swap.txt records soak-window-3 accepted at 17:05:07Z.

Ruled: (iv) is SATISFIED IN PART. The rc.1 live phone path (owner identity, signed
answer, receipt line) is proven end to end, twice approved. The window path (lapse,
re-ask, approve) rests on the in-repository A1-3 rows. The first-write identity
defect is a 3.3.0 known limit; its fix is first on the 3.3.x list.

## The soak drivers (v3 only, not in 3.3.0)

- #1026 (t217, /answer): launched by the rc.1 roster (cf6d2c3b) and completed by
  dev2. Merged by the rc.1 train on the installed rc.1 binary, run
  mt-20261116T170838Z-dfa6c41b, as 8a2b512e30932e0899c82d95c0bbc7612d2db1ed (no
  rebase, digest match).
- #1027 (t218, the real-AMQ lane): merged by the rc.1 train, run
  mt-20261116T171053Z-7687f9bf, as 2cccf126f72d46dcbd812a2c5d9586afe0101107. It was
  rebased; patch_proof match, old == new patch-id.
- Its CI run 43912469004 was the lane's first real run: both owner dry-run tests
  PASSED in the pinned v0.140.0 lane and in the canary.
- From #1027 on, GitHub CI exercises amq-remote's owner path; the local lane run is
  gate1027-lane-local.txt.

## The bound for final

- The soak ran from 2026-11-16T16:21Z to 2026-11-17T19:47Z, about 27.5 h. The lead
  62484, the scheduler 62486 and the attach 8076 were alive at the close.
- (i) Two seat PRs merged by the rc.1 train: #1026 and #1027. Met.
- (ii) One task completed by a seat the rc.1 binary launched: t217 by dev2. Met.
- (iii) No defect in the four 3.2.0 classes: no `ledger_tampered`, `lease_expired`,
  refused commit or train failure in v31's decisions since 16:21Z; the lead screen
  had 0 ESC or C1 bytes. Met.
- (iv) Satisfied in part (ruled), above.
- (v) Zero `native_error` or "delivery never started" in the relay's `up.log` since
  rc.1, and none in the live attach log. Met.
- (vi) At least 24 h, ending before the Buzz grants expire at
  2026-11-28T20:31:45Z. Met.

make-ci-tally.tsv lists the cycle's 8 locked `make ci` runs: all exit 0, no flakes.

## Things seen

### 1. The t94 attention thread

- The reviewer seat's row reads "waiting: on your answer to its question", while the
  screen's "waiting on you" list says "(0) Nothing is waiting on you"
  (lead-first-screen.txt).
- The row comes from the status `attention` item `thread:gate/t94-cli-test-scope`,
  kind question, seat reviewer, open since 2026-11-05T20:11:04Z: "APPROVAL: widen
  t94 for its required CLI regression rows". It is a stale widen thread on the
  owner's list.
- 3.2.0 showed it the same way. In all three 3.2.0 screens
  (`release-v3.2.0/lead-first-screen.txt`, `lead-screen-30s.txt`,
  `lead-screen-after-relaunch.txt`) and in rc.2's two
  (`release-v3.2.0-rc.2/lead-first-screen.txt`, `lead-screen-2026-11-12.txt`), the
  reviewer row reads "waiting: on your answer to its question" beside "waiting on
  you (0)".
- The thread predates both cuts. rc.1 changes neither the row nor the list.

### 2. The rc.1 release gates carry the missing-root identity

See "One gate series" and the phone test, attempt 2. RELEASING.md now creates the
release session's root before `release prepare`.

## The files

The `<home>` placeholder replaces the home path in every file.

- From the cut:
  - cut-binary-version.txt, prepare-time.txt, release-prepare.json,
    prepared-note.txt, gate-questions.txt, gate-status.json;
  - publish-by-hand-out.txt, tag-create.json, release-create-out.txt,
    release-api-fields.json (a selection of GitHub's release fields, and the body's
    byte count);
  - rollback.txt, restart-pre.txt, restart-run.txt, up-330rc1.json;
  - lead-first-screen.txt, lead-screen-30s.txt.
- The build: build-build-times.txt, build-version.txt, build-binary.sha256,
  build-build.log, build-smoke.log.
- The relaunch: decisions-relaunch.jsonl, lead-screen-after-relaunch.txt,
  relaunch-pids-before.txt, relaunch-pids-after.txt, relaunch-pids-later.txt.
- The phone attach: attach-restart.txt, attach-presence.txt, attach-v31-rc1.jsonl.
- The phone test:
  - iv-pre-check.txt, iv-swap.txt, iv-attach-rc330win.err;
  - iv-raise.json, iv-raise-2.json, iv-raise-3.json;
  - iv-gate-record-soak-window-1.json, -2.json, -3.json;
  - iv-gate-status-rc330win.json;
  - iv-attach-rc330win-2.jsonl, -3.jsonl, -4.jsonl.
- The soak driver #1027's local lane run: gate1027-lane-local.txt.
- The tally: make-ci-tally.tsv.
