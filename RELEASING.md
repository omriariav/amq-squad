# Releasing amq-squad

Release changes must go through a PR and `make ci` before merge.

## Patch Release Checklist

1. Update user-facing install references, usually the README `go install` tag.
2. Merge the release PR.
3. Tag the merge commit:

   ```sh
   git tag -a v0.5.1 -m "amq-squad v0.5.1"
   git push origin v0.5.1
   ```

4. Create the GitHub release for the tag.
5. Smoke test the published install path:

   ```sh
   make release-smoke VERSION=v0.5.1
   ```

The smoke test installs `github.com/omriariav/amq-squad/cmd/amq-squad@VERSION`
into a temporary `GOBIN` and fails unless `amq-squad version` prints the same
version. This catches releases where the source tag works but the documented
`go install` path reports `dev` or an old version.

## Major / breaking release checklist

A major version bump (e.g. 1.x -> 2.0.0) is a breaking release with extra,
mandatory steps on top of the patch checklist.

1. **`/v2` module path (Go semantic import versioning).** A v2+ tag is only
   `go install`-able when the module path carries the major-version segment.
   `go.mod` must read `module github.com/omriariav/amq-squad/v2`, and every
   internal import must use the `/v2/...` path. Without this, `go install
   ...@v2.0.0` fails with a path/version mismatch even though the tag exists.

2. **Update the documented install + smoke command to the `/v2` path.** The
   README `go install` line and the smoke command both move under `/v2/`:

   ```sh
   go install github.com/omriariav/amq-squad/v2/cmd/amq-squad@v2.0.0
   ```

   Smoke test the published install path into a throwaway `GOBIN` and assert the
   version round-trips (the v2 analogue of `make release-smoke`):

   ```sh
   tmp="$(mktemp -d)"
   GOBIN="$tmp" GOPROXY=direct \
     go install github.com/omriariav/amq-squad/v2/cmd/amq-squad@v2.0.0
   "$tmp/amq-squad" version   # must print: amq-squad v2.0.0
   ```

   This catches the classic v2 trap: the source tag builds, but the documented
   non-`/v2` path reports `dev` or 404s.

3. **Ship migration notes.** A breaking release MUST land with a "Migrating from
   N-1.x to N.0" section in the README that maps every removed/renamed verb,
   dropped default, and changed semantic to its replacement, plus the new
   install path. Do not tag a major until those notes are merged.

### v2.0.0 changelog summary

- **Lifecycle redesign.** A single state machine — `(none) --up--> running
  --stop--> stopped --rm/archive--> (none)`, with `resume` returning a stopped
  session to running. `up` now means NEW work and refuses an existing session
  (`resume` to continue, `up --reset` to start over); `stop` is the primary
  teardown (state preserved, resumable) and `down` is a deprecated alias; `rm`
  and `archive` are the only destructive ops, both confirm-gated. The pinned
  `team.json` `workstream` default is dropped behind a deprecation shim
  (removal in 2.1). Removed verbs: `launch`, `restore`, `list`, `team show`,
  `team launch` (each prints a migration hint). The brief auto-stubs on `up`.
- **Mission Control.** New read-only `amq-squad console` TUI (board / detail /
  collapsed-thread bus / peek / triage rollup; `--once` for CI), and the bare
  `amq-squad` now renders a multi-session status board.
- **`/v2` module path.** Install with
  `go install github.com/omriariav/amq-squad/v2/cmd/amq-squad@v2.0.0`.
