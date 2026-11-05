# 3.1.1-3 follow-up: no text names a removed /squad subcommand; the fixture's HOME

Authority: the plan reviewer's read of the 3.1.1 notes. Branch
`v3-311-ux4`, from origin/v3 `133f3422edf75450b1a2c52099306dfe035cd00d`.

## A. The removed `/squad` subcommands

3.1.1-3 replaced `/squad confirm <prefix>` with `/confirm` (pi's confirm
dialog quotes the plan; there is nothing to type) and `/squad add` with
`/add` (`/add seat` for a seat); the phone door is a bare `yes` replied to
the lead's Buzz post of the plan (3.1.1-1). Go and a user doc still named
the old commands. Text only, no logic; the digest stays in the doors text
for the model and the Buzz door.

The strings changed:

| where | was | now |
| --- | --- | --- |
| internal/cli/hook_guard.go:262 (the owner_only deny the solo lead's model passes to the owner) | "the lead's pane (/squad confirm) or the owner's signed Buzz DM" | "the lead's pane (/confirm, a dialog with nothing to type) or the owner's signed bare yes replied to the lead's Buzz post of the plan" |
| internal/cli/team_confirm.go:186 (`doorsText`, every refusal that names the doors) | "(1) type /squad confirm %s in the lead's pane; (2) on the phone, reply yes to the lead's Buzz post that relays this plan with its digest %s" | "(1) type /confirm in the lead's pane (a dialog quotes the plan; there is nothing to type); (2) on the phone, reply yes to the lead's Buzz post that relays this plan with its digest %s" (the prefix argument goes) |
| internal/cli/team_confirm.go:39 (the doors comment) | "`/squad confirm <prefix>`: the extension sends the digest and its pane" | "`/confirm`: pi's confirm dialog quotes the plan, and on the owner's yes the extension sends the digest the dialog showed and its pane" |
| internal/cli/lead_start.go:306 (the `shell_door_removed` detail) | "confirm at the lead's pane (/squad confirm) or over Buzz" | "confirm at the lead's pane (/confirm, a dialog with nothing to type) or over Buzz" |
| internal/cli/lead_start.go:439 (the `peer_not_lead_pi` detail) | "/squad confirm is typed in the lead's pane" | "/confirm runs in the lead's pane, in the pi this lead spawned" |
| internal/cli/team.go:70 (`team apply`'s usage) | "(/squad confirm in its pane, or a bare yes ...)" | "(/confirm in its pane, or a bare yes ...)" |
| docs/squad-yaml.md:105 | "`/squad confirm <prefix>` in the lead's pane, or the owner's signed Buzz DM `yes <prefix>`" | "`/confirm` in the lead's pane (pi's confirm dialog quotes the plan; there is nothing to type), or the owner's signed bare `yes` replied to the lead's Buzz post of the plan (a `yes <prefix>` still verifies ... but no text asks for it)" |
| docs/squad-yaml.md:187 | "`/squad add`, `seat`, and one confirmation" | "`/add seat` and one confirmation" |

The brief named five places; the scan found two more, `lead_start.go:306`
and the `team_confirm.go:39` comment. The four test assertions that pinned
the old doors text (internal/cli lead_start_test.go:289 and :480,
team_confirm_test.go:89 and :151) now pin "/confirm in the lead's pane".

**Row.** internal/cli/squad_text_test.go
`TestNoTextNamesARemovedSquadSubcommand` walks the repository and fails on
any line that names a `/squad` followed by a word in a Go source (every
`.go` file, tests included), README.md, a user doc (`docs/**/*.md`) or a
skill source (`plugins/skills-src/**`). Allowed: the historical notes under
`docs/v3-spike/` and the release notes (`*release-notes*`), which keep what
was true when written. The pattern is built at run time so the test does
not carry it.

## B. The test binary's git under its own HOME

internal/mergetrain's tests ran git under the owner's real HOME (a
standing-rule 18 breach): `gitOut` (fakegh_test.go, the helper `mustGit`
and `newFixture` call) set `GIT_CONFIG_GLOBAL=/dev/null` and
`GIT_CONFIG_NOSYSTEM=1`, which keep the global and system config out, but
git still reads the default ignore and attributes files under
`$XDG_CONFIG_HOME/git/`, else `$HOME/.config/git/`; and production's own
`git patch-id` (gh.go), which a fixture's train runs, inherited the
environment as it was.

The isolation is set once, in the shared place every mergetrain test runs
under (the plan reviewer: the shared fixture setup, so a later fixture is
covered too): `TestMain` in fakegh_test.go makes a temporary HOME
(`os.MkdirTemp`, removed at exit) and calls `isolateGitHome`, which sets
`HOME` to it, unsets `XDG_CONFIG_HOME`, and sets `GIT_CONFIG_GLOBAL=/dev/null`
and `GIT_CONFIG_NOSYSTEM=1` on the process before any test runs. `gitOut`,
`mustGit`, `newFixture`, production's `git patch-id`, the subprocess fake gh
(the train starts it with the process environment) and any fixture a later
test adds inherit it; `gitOut` keeps the author, committer and config
variables it already set. It is the process and not `newFixture` because
`t.Setenv` panics in a parallel test, and 22 of the tests that build a
fixture run in parallel.

**Row.** internal/mergetrain/fixture_home_test.go
`TestTheTestBinarysGitDoesNotReadTheRunningHome`: a stand-in for the owner's
home carries `.config/git/ignore` hiding every `.txt` file; under it a plain
git hides a new `a.txt` (the stand-in works); after `isolateGitHome`, a plain
git (as production's `git patch-id` runs) and the fixtures' `gitOut` show it
untracked. The test restores every variable it touches.

The plan reviewer asked for this fix in the seat's t47-assert task, which is
mid-flight; doing it here was the lead's call. The seat PR t68 (touching
internal/mergetrain's tests) merges first; when this branch takes v3, a
conflict in fakegh_test.go is taken on this side and the HOME row's revert
run is made again after that merge.

## Revert runs

Each reverts production or fixture code only (the row's own test stays):

| row | revert | result | the failing output's first line |
| --- | --- | --- | --- |
| A | the owner_only deny names "(/squad confirm)" again (internal/cli/hook_guard.go) | caught | `--- FAIL: TestNoTextNamesARemovedSquadSubcommand`: `internal/cli/hook_guard.go:262 names a removed subcommand of` `/squad` |
| B | the test binary keeps the running HOME (the `HOME` line removed from `isolateGitHome`) | caught | `--- FAIL: TestTheTestBinarysGitDoesNotReadTheRunningHome`: `a git under the test binary's environment read the running home: status ""` |

## Gates

At code head `15cade69`:

- `go build ./... && go vet ./... && go test -count=1 ./...`: exit 0, 56 packages ok, 0 FAIL lines. Tail:

```
?   	github.com/omriariav/amq-squad/v3/scripts/redact-evidence	[no test files]
?   	github.com/omriariav/amq-squad/v3/spike/claude-probes	[no test files]
?   	github.com/omriariav/amq-squad/v3/spike/pi-r19	[no test files]
exit 0
```

- `cd pi && npm ci --offline --ignore-scripts && npm test`: exit 0. Tail:

```
ℹ tests 110
ℹ suites 0
ℹ pass 110
ℹ fail 0
ℹ cancelled 0
ℹ skipped 0
ℹ todo 0
ℹ duration_ms 15434.255166
exit 0
```

- `make ci` (PyYAML venv on PATH; it runs the pi suite last): exit 0. Tail:

```
ℹ tests 110
ℹ suites 0
ℹ pass 110
ℹ fail 0
ℹ cancelled 0
ℹ skipped 0
ℹ todo 0
ℹ duration_ms 44291.588958
exit 0
```
