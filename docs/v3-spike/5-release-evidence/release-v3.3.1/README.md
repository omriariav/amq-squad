# amq-squad v3.3.1 release evidence (2026-11-18 to 2026-11-19)

3.3.1 ships one fix, #1033: a gate raised as the first write into a new
session gets the squad's real identity, so it can be answered (3.3.0's known
limit 7). It was cut as 3.3.1-rc.1, soaked for 24 hours on the owner's squad,
and published as 3.3.1 with only the version bump and the final notes added.

Where a sentence names no file, its source is the lead session's approvals
record (T/v320rc1/owner-approvals.txt, entries of 2026-11-18 and 2026-11-19)
and the lead's private evidence folders, not a squad log. No key, no message
text and no owner credential is copied here.

## The two cuts

| | 3.3.1-rc.1 | 3.3.1 |
|---|---|---|
| Release PR | #1043 (#1033 picked with `-x`, the bump, the notes) | #1047 (the bump, the final notes) |
| Tag commit on `release/v3.3` | `4d34b9239b5eb3e039a7b05cc1379989fc0838ea` | `af6962186f919779d031ad7f7c71d7cbcf42e2b3` |
| Published (`published_at`) | 2026-11-18T09:59:32Z, prerelease | 2026-11-19T18:21:34Z, latest, not a prerelease |
| Notes body sha256 | `cb89d824ec0e4c860a3b8f591ee4fbfcae52e1ba3ef26e108718fc8ecec75451` | `1b69aa7fcc8732d9b3ed52a2b86a439d201b6c35e3e67f13a7e68b672a79451b` (9972 bytes) |
| Gates raised, not answered | `rel-tag-6a09783bd8765b6d9022`, `rel-gh-8c40fb0b93169e66225a` | `rel-tag-204a2fc8c9c12c8b3617`, `rel-gh-a742366e93a4db7a28c8` |
| Installed build sha256 | `59216f99dfc84dc65ee2e0943ea6c452134ab92e96b621dbd11041139c9d2e45` | `4044c0f81f800e550a2c2c19aead6f60da636712bdcd49594859f194bb510842` |

- Both cuts used route A: the owner published by hand under his own gh login,
  and both release gates were raised and left unanswered (they expired). For
  each cut the owner answered, each in its own question in the lead session:
  "Yes, publish now", "Yes, install it", "Yes, restart the lead" and "Yes,
  restart it" (the phone attach).
- The final's read-back: the tag ref is a commit at `af696218`; prerelease
  false, draft false, target `af696218`; the release body decoded from the
  API hashes to `1b69aa7f…`, equal to the notes file at the tip, and is
  byte-equal to it; `releases/latest` is v3.3.1.
- Before the final's push: `make release-check VERSION=v3.3.1` ("release
  metadata matches v3.3.1"), the diff from `4d34b923` was the 22 bump and
  notes files, and a locked, uncached `make ci` exited 0. #1047's CI passed
  4 of 4.
- No permission refusal stopped any step.

## #1033 live: row 8's two proofs

1. **A scratch HOME, 3.3.1-rc.1 against 3.3.0 (2026-11-18).** Each binary
   raised one gate into a fresh session with no `mkdir -p`, under `env -i`
   and a scratch HOME. rc.1's record carried
   `root-sha256:757001a369b62e9ea8fc20fa48350c209752b182a76417f1c07068378079cc8f`,
   the root's real identity. 3.3.0's control carried
   `root-sha256:4a58e8b71291016016182fa18796ba307317cc25d43cd5e75bce2cd14db3d924`,
   the missing-root placeholder (known limit 7's cause). Nothing was sent (the
   scratch HOME had no share), the scratch keys were deleted, and the owner's
   binding, grants and registry were unchanged by size, time and sha.
2. **The final's own `release prepare` (2026-11-19T18:11:45Z).** It ran on the
   installed 3.3.1-rc.1 into the fresh session `v331final`, whose root was
   absent before the prepare. Both gates carry
   `root-sha256:2d541919b65b3b4999097071beaefc53521d0793bc3feef92834be656f941247`.
   The missing-root placeholder for that root would be
   `root-sha256:760786243f4f1e5f289f1d3bfb85b3d715a873d00377f0ec1089dae120ab60ed`;
   neither gate carries it. Both questions were sent to the owner's Buzz
   channel (posted 18:11:48Z and 18:11:49Z) with no error.

## The rc.1 soak (2026-11-18T17:45Z to 2026-11-19T17:46Z)

The lead (`lead --team`), its scheduler and the phone attach ran the rc.1
build for the whole window; a watcher checked every minute for an exit of
any of the three, a new `ledger_tampered` or `lease_expired`, and a
`native_error` in the attach log. It ended with no event.

| | Baseline 17:45:26Z | End 17:46Z |
|---|---|---|
| Scheduler decisions | 21955 | 21963 (+8: two tasks dispatched, committed, completed and bound to their PRs) |
| `ledger_tampered` | 1951 | 1951 (+0) |
| `lease_expired` | 26 | 26 (+0) |
| Attach `native_error` | 0 | 0 |
| Lead, scheduler, attach | started before the baseline | the same processes, no exit or restart |

- The four 3.2.0 defect classes: no `ledger_tampered` on a sound chain, no
  lease lost, no control byte on the lead's screen (0 ESC, 0 C1), and the one
  merge-train run on rc.1's changed train code (#1046) merged with no gate,
  no escalation and no escape byte.
- A task dispatched by the rc.1 scheduler to a live seat completed (t226,
  #1045, closed and replaced by t227, #1046).
- **`native_error`: vacuous in the window, real after it.** The attach carried
  no relay traffic during the soak, so its count of 0 showed nothing about
  delivery; the final notes say so, and the plan reviewer accepted it because
  #1033 touches no relay path. After the soak, two real round-trips ran on
  rc.1: the final's two gate questions (above), and a plain message the owner
  sent from the phone on his own at 2026-11-19T18:47:30Z, which the rc.1
  attach handled at 18:47:33Z (routed, accepted, `native_error` 0, an empty
  error file). It named no lead, so it went to the attach's operator mailbox,
  which is the rehearsal fleet's until 3.3.2 (3.3.x item 8), not the live
  lead's.

## The final on the owner's squad (2026-11-19)

- Install at 18:23:40Z by temporary name and rename from the tag build. The
  rc.1 binary was kept as a copy first, beside the 3.3.0 copy; the rollback
  goes to rc.1 first, then to 3.3.0, each by temporary name and rename or
  `up --binary`, never by copying over the installed file.
- `doctor`: 41 checks, none failed, the same 5 warnings as at rc.1.
- Lead restart: SIGTERM at 18:47:13Z, the lead and scheduler gone in 1 s;
  `up --binary` exited 0 with the same steps as at rc.1 (the roster adopted,
  no launch). The new processes run the installed file. No "Failed to load
  extension" on the first screen or at 30 s.
- Phone attach: SIGTERM at 19:23:04Z, gone in 1 s; the new attach started on
  the installed build and logged `attached` with the same leads as before,
  with an empty error file.
- **The warnings lag.** The lead's screen lists releases and tags with no
  accepted owner gate. At 3.3.0's restart (2026-11-17) v3.3.0 was not listed;
  it was listed at rc.1's restart (2026-11-18). At rc.1's restart v3.3.1-rc.1
  was not listed; it was listed at the final's restart (2026-11-19, the first
  screen). At the final's restart, and again after the attach swap, v3.3.1 is
  not listed yet. If it is still missing a day later, it becomes a follow-up
  row, not a release item.

## Slips, disclosed

- The rc.1 build's first attempt ran in the main checkout after a transient
  tag-fetch failure: a dirty binary was built in the gitignored `build/pi`.
  It was never installed or used (the release build was redone in a scratch
  worktree), it is left in place for the owner to delete, and the tracked
  files were unchanged.
- During row 8's before-and-after check, one content scan read the bytes of
  the owner's grants file against the instruction; it matched nothing,
  printed nothing and changed nothing. Later checks use stat and sha only.
- #1045's revert run was voided because its driver was edited mid-gate;
  round 2 (#1046) ran from a read-only snapshot.
- The rc.1 install landed before the plan reviewer's message on the install
  form; it was by temporary name and rename, so no fault.

## Afterwards

- The join into main (#1048) and the merge-back into v3 (#1049), each a merge
  commit: the join's tree equals `af696218`'s; the merge-back changes v3 by
  the 22 bump and notes files only, since #1033 is already on v3 as
  `ed0c49e7`.
- This PR also carries known limit 10's gate-answer wording on v3 (the final
  notes' sentence).
