# amq-squad 3.0: where we are and what is yours

Phase 5 is closed on the code side. The 3.0.0 release commit is d56d7a0c; the tag target is the v3 tip, 6c318455 (the release commit plus the last test-hygiene merge and the docs). Nothing is tagged or pushed; both are yours.

## Two decisions

1. S6 merge train. A: dry run only against the 18 open S6 draft PRs on amq-noc and workspace-cli (no merge, the liveness check recorded). B: dry run, then the real run that merges the 16 clean PRs after you set the merge grant and the CI-only rulesets on both repositories. My recommendation: A first, B the same day if the dry run is clean.
2. amq-noc command center (brief 4-4). A: it lands by PR on amq-noc main now, after its fixture refresh for lead_handle and live. B: it waits for the first real merge-train run and rides it as one of the PRs. My recommendation: A; it is review-complete and the train has enough to prove without it.

## Your actions, in the order they must happen

1. Rotate: confirm both exposed credentials (the Gemini key, the lead session's messaging token) are rotated. Nothing else should be done before this is true.
2. History scan: done. Every object reachable from v3 was scanned; the two values and the earlier 1Password environment id are absent, and the rewritten-away commits no longer exist in the repository. Three historical blobs carry the labelled test canary that looks like a Google API key.
3. gc: done this morning (your reflog expire and gc). Nothing to do.
4. Push v3 to origin. If GitHub push protection fires for a Google API key pattern, that is the canary in those three blobs; bypass with "used in tests". Any other push-protection hit: stop and send the reported path to the reviewer session.
5. Tag ruleset on amq-squad: a ruleset on `refs/tags/v*` (a single-segment prefix, no `**`, no exclusions) that restricts tag create, update and delete to you, with no bypass actors. The release flow refuses to run without it.
6. Release gate: run `amq-squad release prepare --repository omriariav/amq-squad --tag v3.0.0 --commit 6c318455` and answer the two typed gates (tag, github_release) from the phone in their threads.
7. Tag and release: `amq-squad release publish` with the same arguments. It tags, reads the draft back, compares the tag's commit to the approved target and publishes only on equality.
8. Token file: `claude setup-token`, the token into `~/.amq-squad/claude-token` (0600). This unblocks milestone B's Claude live rows and the hooks fault table.
9. Pins: a hard link named `claude` to the versioned Claude Code binary on the same filesystem (a symlink is refused), pinned in `~/.amq-squad/registry.yaml` with its path and sha256; re-pin after every Claude Code update (doctor says "pinned claude is behind the installed claude"). The gh pin the same way, needed for any HTTPS push_url.
10. Trust fixture: `amq-squad trust <milestone B fixture path>` so a real Codex seat runs acceptance 7.5.
11. Buzz session (about 15 minutes, the real key through the password-manager pipe only): `docs/v3-buzz/owner-run.md`. It proves one typed gate answered from the phone, one message routed to a lead, and the Buzz-signed `seat clear` as its third item.
12. Merge grant and rulesets: the per-repository merge grant in grants.yaml for amq-noc and workspace-cli, and CI-only rulesets on their default branches. Then decision 1's run(s).
13. Codex notify line: paste the candidate line `amq-squad trust <repo>` prints as Codex's notify command in config.toml; doctor checks it is there.
14. `rm -rf /tmp/jiti`: the world-readable transpile cache pi wrote before the cache-off fix; the 3.0 runtime never writes there.

## State

- Phase 5 closed at d56d7a0c. Size of 3.0: 79,815 lines, v2 rows kept 27,862 against the ceiling 36,410.
- Merged in phase 5: test hygiene (two branches, the hung-guard flake fix is the last), the eight Codex re-passes of the earlier heads, release prep, the Buzz-signed seat clear, class verifiers at the commit step, squad.yaml final form with migrate, uninstall, the 3.0.0 release commit. Every merge gated on a quiet host in a scratch worktree with the full suite, race on the touched packages and a credential-shape scan of the diff.
- What 3.0 does not do, with where it is recorded: no read-only runtime image (plan 4.1 and 16.4, your decision for launch speed, 3.1); GitHub custody through your gh login, not a dedicated keychain or App token (R33, 3.1); tag and release protection by detection, not prevention, when a same-UID process holds your gh token (R33, 7.3); orphaned verifier after a scheduler death defers to the lead (plan 6.1, 3.0.x); the Linux hook CI job has not had a real run until the first push (16.4).
- Upstream: asks 15 to 17 filed as #1210, #1211, #1212; the pi launch row waits on an AMQ release carrying PR #1188.

## Follow-ups

`docs/v3-followups.md` on v3 lists the 3.1 and 3.0.x items with their plan sections, and the acceptance rows that wait on your session.
