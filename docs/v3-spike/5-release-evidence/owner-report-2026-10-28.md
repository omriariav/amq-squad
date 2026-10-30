# amq-squad 3.0: where we are and what is yours (re-issued 2026-10-28)

The AMQ bump you folded into 3.0.0 is merged. Three briefs landed today on top of the release commit d56d7a0c, each with a Codex pass, fix rounds, the reviewer's read and a quiet-host gate: A (AMQ pin v0.116.2, pi seats through AMQ's adapter, the Codex sandbox pins through launch admission, one CI floor), B (the Buzz share on amq-remote's mailbox adapter) and C (the scheduler stand-in deleted, the real-AMQ contract tests ported). The tag target is the v3 tip, 25b65368. Nothing is tagged or pushed.

## Two decisions (unchanged)

1. S6 merge train. A: dry run only against the 18 open S6 draft PRs. B: dry run, then the real run after you set the merge grant and the CI-only rulesets. Recommendation: A first, then B the same day if the dry run is clean.
2. amq-noc command center. A: lands by PR on amq-noc main now. B: rides the first real merge-train run. Recommendation: A.

## Your actions, in the order they must happen

1. Rotate: confirm both exposed credentials are rotated (the Gemini key, the lead session's messaging token). Nothing else before this is true.
2. History scan: done, twice. Saturday's scan over every object reachable from v3 (the two values and the 1Password environment id absent; the rewritten-away commits gone from the repository) and today's delta over the objects added since (418 objects, 0 shape hits; the messaging token is covered by Saturday's literal check and your rotation, not by the delta). Three historical blobs carry the labelled test canary that looks like a Google API key.
3. gc: done Saturday.
4. Install AMQ v0.116.2 (amq, amq-remote, amq-owner-sign). doctor refuses below it now, and the Buzz live rows and the pi acceptance row run on it. Then run `amq-squad doctor`.
5. Push v3 to origin. If GitHub push protection fires for a Google API key pattern, that is the canary in the three blobs; bypass with "used in tests". Any other push-protection hit: stop and send the reported path to the reviewer session. The first push also runs CI for the first time: the pinned real-AMQ lane at v0.116.2 is required; the "canary latest" lane is non-blocking and its failure reads as upstream drift. A red pinned lane is a stop before the tag.
6. Tag ruleset on amq-squad: `refs/tags/v*`, single-segment prefix, no exclusions, tag create, update and delete restricted to you, no bypass actors.
7. Release gate: run the prepare command below and answer the two typed gates (tag, github_release) from the phone in their threads.
   ```
   amq-squad release prepare --repository omriariav/amq-squad --tag v3.0.0 --commit 25b65368
   ```
8. Tag and release: `amq-squad release publish` with the same arguments.
9. Token file: `claude setup-token`, the token into `~/.amq-squad/claude-token` at 0600.
10. Pins: a hard link named `claude` to the versioned Claude Code binary on the same filesystem, pinned in the registry with path and sha256, re-pinned after every Claude Code update; the gh pin the same way for any HTTPS push target.
11. Trust the fixture: `amq-squad trust <milestone B fixture path>`.
12. Buzz session, about 15 minutes, the real key through the password-manager pipe only: docs/v3-buzz/owner-run.md, re-walked for the mailbox adapter. New rows: the share binds the mailbox adapter with DM commands on; doctor's floor and bindings rows; one connection during a verification and none after; row 5.6, whether Buzz Desktop tags your reaction removals (if not, an untagged removal on a gate thread costs one re-answer); and the busy rule: wait for each result row (about a second) before the next message, since serve rejects a second message during an open request and the attachment never sees it.
13. Merge grant and rulesets: the per-repository merge grant for amq-noc and workspace-cli, CI-only rulesets on their default branches. The required-check names follow the new CI job names (the pinned real-AMQ job). Then decision 1's runs.
14. File upstream ask 18 under your login: docs/v3-upstream-issues/18-mailbox-busy-queue.md (queue owner DMs on a mailbox target instead of rejecting them busy, or a retry hint).
15. Codex notify line: the candidate line `amq-squad trust <repo>` prints, as Codex's notify command in config.toml.
16. `rm -rf /tmp/jiti`.

## State

Phase 5 closed at d56d7a0c; the AMQ bump merged after it. Size of 3.0: 81,146 lines, v2 rows kept 27,885 against the ceiling 36,410.

What 3.0 does not do, with where it is recorded: no read-only runtime image (plan 4.1 and 16.4, 3.1); GitHub custody through your gh login (R33, 3.1); tag and release protection by detection, not prevention, against a same-UID holder of your gh token (R33, 7.3); the orphaned verifier after a scheduler death defers to the lead (plan 6.1, 3.0.x); the mailbox adapter's busy window (R42, ask 18); the scheduler's async-close path has no production implementer and is removed in 3.0.x (follow-ups); the Linux hook CI job and the pinned real-AMQ lane get their first real runs on the first push.

Upstream: every ask filed is shipped and closed; ask 18 waits for you to file.

## Follow-ups

docs/v3-followups.md on v3 lists the 3.1 and 3.0.x items with their plan sections, and the acceptance rows that wait on your session.
