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
