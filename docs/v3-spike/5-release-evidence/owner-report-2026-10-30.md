# amq-squad 3.0: the morning report (2026-10-30)

Everything before the tag is done. The tag target is the v3 tip, 813818cb, pushed to origin with CI green on all four jobs (run 40958849281). Nothing is tagged yet; the tag needs your two phone answers.

## What happened since you went to sleep

Three more briefs merged after the AMQ bump, each with a Codex pass or the reviewer's read, fix rounds and a quiet-host gate:

- **D, the first CI run's failures.** The decision socket could not be dialled on Linux past the socket path bound (a real exposure on a deep project path; the dial now goes through descriptors like the listener). A test fixture seeded a pid that is a live kernel thread on Linux runners. The real-AMQ lane needed a claude on the runner; it now stubs the probes, and a test that used to open a session on your real tmux server no longer does.
- **E, the plugin skills.** The seven skill files and the skills guide, which Claude and Codex agents read to run amq-squad, still described v2. They are rewritten from the 3.0 verbs' own help; every documented command is executed by a gate under a throwaway HOME with network calls refused (that gate used to run under your real HOME, where a documented gate raise could have posted to your Buzz DM).
- **F, the README and the release check.** README is rewritten for 3.0 with every command run; the global runbook is retired to a pointer; the release version check reads the AMQ floor from doctor's constant; four help defects fixed; a repeated per-seat flag on team propose is refused instead of silently dropped.

**Platform decision (the reviewer's, on the plan's own target; yours to reverse, B is 3.1 work):** 3.0 ships for macOS on Apple silicon. On Linux the binary builds and the Claude hook script is tested; seats are unsupported. The first Linux test run found the launch's proof socket failing under long project paths, and ext4 and tmpfs reusing an inode right after unlink and recreate, which weakens the device-and-inode identity checks the harness uses against replacement. CI's gating suite now runs on a macOS runner; Linux runs the suite as a non-blocking canary. The repository is public, so the macOS minutes are free. CI cost of the green run: make ci on macOS 6.0 minutes, the pinned real-AMQ lane 0.6, the canary 1.3, the Linux job 1.9, about 10 runner-minutes per push.

**Your Buzz session** is recorded as passed: enrollment on the mailbox adapter, gate 01 verified (3-1 acceptance 9), the re-ask row, the routed message to the squad lead (4-5 acceptance 4). The signed seat clear stays an open row with a rehearsal-verb follow-up.

**The real merge-train run** could not take the S6 drafts by design; your decision A is recorded (open until the tag, then closed, with one last dry run as release evidence). Acceptance 4 is re-scoped to the first harness-bound PR, an owner-session row on amq-noc.

**History scan delta** since the Saturday scan: 396 objects since yesterday's target, 0 shape hits (and 418 the day before, 0 hits).

## The tag, when you say "ready"

1. I raise the two release gates with:
   ```
   amq-squad release prepare --repository omriariav/amq-squad --tag v3.0.0 --commit 813818cb
   ```
2. Two questions arrive on your phone within a minute: `tag` and `github_release`. Answer each in its thread with the approve line it shows. They expire 30 minutes after the raise.
3. I run `amq-squad release publish` with the same arguments. It tags 813818cb, creates the draft release from docs/v3.0.0-release-notes.md, reads it back, and publishes only if the tag's commit equals the target.
4. Then: install 3.0 over your v2.31 (`go install github.com/omriariav/amq-squad/v3/cmd/amq-squad@v3.0.0` for the CLI; the darwin build with the embedded pi runtime from scripts/build-pi-runtime.sh for pi), close the 18 S6 drafts after one last dry run, and the amq-noc PR.

## Still yours after the tag

- The Codex notify line in config.toml (the candidate from `amq-squad trust <repo>`).
- The live rows that wait: a mayor-started squad from the phone, the harness-bound PR through the merge train, the Claude live rows (the token file is in place), the Codex seat row (trust the fixture), the signed seat clear (rehearsal verb first).

## Follow-ups

docs/v3-followups.md: the 3.1 items (Linux seats with the three findings, the runtime image, GitHub custody, the pi bridge event id) and the 3.0.x items (the async-close path, the seat-list compile verb, propose showing bytes, the help formatting, the test-hygiene list with the buzz dry-run bound, the rehearsal verb, ask 18 once Aviv answers).
