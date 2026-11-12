# Releasing amq-squad

Release changes must go through a PR and `make ci` before merge. `make ci`
includes a README.html freshness check, so a release that touches `README.md`
cannot merge without the matching regenerated `README.html` (see step 1).

The fail-closed AMQ minimum is 0.116.2 (`internal/cli/doctor.go`, `doctorMinAMQVersion`).
Release validation must keep the doctor floor, active README/CLI-skill policy,
and both real-AMQ CI matrices aligned on pinned `v0.116.2` plus `latest`.

## Patch Release Checklist

0. The README carries exactly ONE "What's new" section — the release being
   cut. REPLACE the previous release's section (and its Contents entry), never
   append below it; the outgoing text already lives in that release's
   `docs/<tag>-release-notes.md` and GitHub Release (#678).
1. Update user-facing install references (usually the README `go install` tag).
   Any time you change `README.md`, run `make readme-html` to regenerate
   `README.html` (requires `pandoc`) and commit both; `make ci` fails if
   `README.html` is out of sync.
   The skill version is NOT hand-bumped. `scripts/generate-plugin-skills.py` stamps
   frontmatter `version:` in every mirror from that mirror's plugin manifest, tagged
   with an `x-release-please-version` marker, so bumping the manifest and running
   `make skills-generate` is sufficient. `make ci` (`skills-check`) fails if a mirror
   drifts from its manifest.
   Confirm the doctor minimum, README and CLI-skill support text, release-policy
   checker, and both real-AMQ matrices still agree on AMQ 0.116.2 / `v0.116.2`
   plus the `latest` canary.
Who does each step: the lead runs the cut. A release agent the lead starts may
prepare and check (the notes, the skew check, the tag build, the evidence) but never
publishes. The publish (steps 3 and 4, through `amq-squad release publish` once the
owner has answered the `tag` and `github_release` gates) is the lead's own step. A
refused or failed step goes back to the owner in words, never to another session to
retry.

2. Merge the release PR (the lead).
3. Tag the merge commit (the lead, through `release publish` with the owner's
   verified `tag` answer). The lead first runs `release prepare`, which raises
   the `tag` and `github_release` gates; once the owner has answered both, the
   lead runs `release publish` with the same arguments. Never tag by hand.

   ```sh
   amq-squad release prepare --repository omriariav/amq-squad --tag v0.5.1 \
     --commit <merge SHA> --notes-file docs/v0.5.1-release-notes.md
   amq-squad release publish --repository omriariav/amq-squad --tag v0.5.1 \
     --commit <merge SHA> --notes-file docs/v0.5.1-release-notes.md
   ```

4. Create the GitHub release for the tag (the lead: the same `release publish`
   creates it after the tag, with the owner's verified `github_release` answer).
5. Smoke test the published install path:

   ```sh
   make release-smoke VERSION=v0.5.1
   ```

The smoke test installs `github.com/omriariav/amq-squad/v3/cmd/amq-squad@VERSION`
(note the `/v3` module path, as for every v2+ major) into a temporary `GOBIN` and fails unless
`amq-squad version` prints the same version. This catches releases where the
source tag works but the documented `go install` path reports `dev` or an old
version.

6. Start the lead on the installed new binary before the release counts as
   done. Install the binary built from the tag (`scripts/build-pi-runtime.sh
   all`, the one attached to the release). Then either restart the squad's own
   lead on it, or run `amq-squad lead --team -- --offline` in a scratch
   repository (`git init` in an empty directory, no `squad.yaml`, a scratch
   `HOME`). Record the lead's first screen, a tmux `capture-pane` at about
   10 s, in the release evidence (`docs/v3-spike/5-release-evidence/release-<tag>/`).
   A dead pane or any `Failed to load extension` line means the release is
   not done. `make release-smoke` checks only the version, and CI stages no pi
   runtime: 3.1.0 and 3.1.1 shipped a lead that exited at once. The first
   instance is the 3.1.2 evidence: the built lead's first screen, and the
   3.1.1 build's dead pane beside it, in
   `docs/v3-spike/312-evidence/hotfix-hostfacts.md`. The 3.1.2 cut records
   the installed binary's screen in `docs/v3-spike/5-release-evidence/release-v3.1.2/`.

## Minor release checklist

Since v2.0.0, a minor release (for example v2.30.0) follows the same
numbered Patch Release Checklist above: there is one release procedure,
not two. The v1.3.0-era guidance below predates the `/v2` module path and is
kept for historical reference only.

For a minor release such as v1.3.0, keep the module path on
`github.com/omriariav/amq-squad`, update README install examples to the new tag,
run `make ci`, and smoke test with `make release-smoke VERSION=<tag>`.

### v1.3.0 split note

- amq-squad remains the team setup, lifecycle, status, and project-scoped
  console tool.
- The unshipped multi-root command-center work is not part of this release.
- AMQ diagnostics are exposed through `amq-squad amq ...` with preview-first,
  confirm-gated maintenance commands.

## Future major / breaking release checklist

A future major version bump is a breaking release with extra mandatory steps on
top of the patch checklist.

1. **Semantic import versioning.** For v2+, update `go.mod` and all internal
   imports to include the major-version segment, for example
   `module github.com/omriariav/amq-squad/v2`.
2. **Update install docs and smoke commands.** README examples and smoke tests
   must use the same major-version import path as `go.mod`.
3. **Ship migration notes.** A breaking release MUST land with a migration
   section mapping every removed/renamed verb, dropped default, and changed
   semantic to its replacement.

### Historical v2.0.0 changelog draft

- **Lifecycle redesign.** A single state machine — `(none) --up--> running
  --stop--> stopped --rm/archive--> (none)`, with `resume` returning a stopped
  session to running. `up` now means NEW work and refuses an existing session
  (`resume` to continue, `up --reset` to start over); `stop` is the primary
  teardown (state preserved, resumable) and `down` was removed in 2.0 (it had
  been a deprecated alias); `rm` and `archive` are the only destructive ops,
  both confirm-gated. The pinned `team.json` `workstream` default is dropped
  behind a deprecation shim (removal in 2.1). Removed verbs: `down`, `launch`,
  `restore`, `list`, `team show`, `team launch` (each now returns a usage
  error). The brief auto-stubs on `up`.
- **Mission Control.** New read-only `amq-squad console` TUI (board / detail /
  collapsed-thread bus / peek / triage rollup; `--once` for CI), and the bare
  `amq-squad` now renders a multi-session status board.
- **Major-version module path.** A future v2 release would install from the
  matching `/v2` import path.
