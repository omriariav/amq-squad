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

## B. The fixture's git under its own HOME

internal/mergetrain/fakegh_test.go's `gitOut` ran git with the process
environment, so under the owner's real HOME (a standing-rule 18 breach):
`GIT_CONFIG_GLOBAL=/dev/null` and `GIT_CONFIG_NOSYSTEM=1` kept the global
and system config out, but git still reads the default ignore and attributes
files under `$XDG_CONFIG_HOME/git/`, else `$HOME/.config/git/`. The
fixture's git now runs under a temporary HOME made in `TestMain`
(`os.MkdirTemp`, removed at the end; the fake gh's own run, when the train
calls it, gets one too) and `XDG_CONFIG_HOME` under it, keeping the author,
committer and config variables it already set.

**Row.** internal/mergetrain/fixture_home_test.go
`TestTheFixtureGitDoesNotReadTheRunningHome`: the test process runs under a
stand-in home whose `.config/git/ignore` hides every `.txt` file; a plain
git under it hides a new `a.txt` (the stand-in works), and the fixture's
`gitOut` shows it untracked.

The plan reviewer asked for this fix in the seat's t47-assert task, which is
mid-flight; doing it here was the lead's call.

## Revert runs

Each reverts production or fixture code only (the row's own test stays):

| row | revert | result | the failing output's first line |
| --- | --- | --- | --- |
| A | the owner_only deny names "(/squad confirm)" again (internal/cli/hook_guard.go) | caught | `--- FAIL: TestNoTextNamesARemovedSquadSubcommand`: `internal/cli/hook_guard.go:262 names a removed subcommand of` `/squad` |
| B | the fixture's git inherits HOME (the `HOME=` and `XDG_CONFIG_HOME=` line removed from `gitOut`) | caught | `--- FAIL: TestTheFixtureGitDoesNotReadTheRunningHome`: `the fixture's git read the running home: status "", err <nil>` |

## Gates

At code head `9aba6904`:

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
ℹ duration_ms 15418.099209
exit 0
```

- `make ci` (PyYAML venv on PATH; it now runs the pi suite last): exit 0. Tail:

```
ℹ tests 110
ℹ suites 0
ℹ pass 110
ℹ fail 0
ℹ cancelled 0
ℹ skipped 0
ℹ todo 0
ℹ duration_ms 43461.391375
exit 0
```
