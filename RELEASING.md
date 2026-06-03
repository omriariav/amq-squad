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

## Minor release checklist

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
  teardown (state preserved, resumable) and `down` is a deprecated alias; `rm`
  and `archive` are the only destructive ops, both confirm-gated. The pinned
  `team.json` `workstream` default is dropped behind a deprecation shim
  (removal in 2.1). Removed verbs: `launch`, `restore`, `list`, `team show`,
  `team launch` (each prints a migration hint). The brief auto-stubs on `up`.
- **Mission Control.** New read-only `amq-squad console` TUI (board / detail /
  collapsed-thread bus / peek / triage rollup; `--once` for CI), and the bare
  `amq-squad` now renders a multi-session status board.
- **Major-version module path.** A future v2 release would install from the
  matching `/v2` import path.
