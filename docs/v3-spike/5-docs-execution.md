# Brief 5-docs-F: execution log

Branch `v3-5-docs-f`, from the v3 tip 415bbc5e, rebased onto c864882c (brief
5-skills-E merged as 7630adf0) when the lead said so. Evidence:
`docs/v3-spike/5-docs-evidence/`.

## Part 1: docs

### README.md, per section

The v2 README (776 lines) was replaced (412 lines). Its job is kept: what
3.0 is, install, requirements, the first run, the owner's Buzz face, what 3.0
does not do, where the plan and evidence live.

| Section | Removed (v2) | Added (3.0) |
| --- | --- | --- |
| Intro, mental model | "launches Claude and Codex over AMQ", terminals as runtime surfaces (tmux, iTerm2, Terminal.app), operator gates for pushes and merges | one squad per repository inside the owner's grant; squad.yaml through a digest; grants.yaml outside the repository; seats never commit, merge or release; the four typed gates answered from Buzz |
| What's new | v2.31.0 (launchapi default, `.amqrc` plus `brief` as mandatory steps) | 3.0 in short, pointing at docs/v3.0.0-release-notes.md |
| Install | "Install the v2 module path", `@latest`, the plugin marketplaces | `go install .../v3/cmd/amq-squad@v3.0.0`; the plain statement that a `go install` build has no pi runtime and that only the darwin arm64 `scripts/build-pi-runtime.sh all` build embeds it; the Claude plugin at user scope (claude-hooks.md form) |
| Requirements | tmux Tier A, iTerm2 Tier B, Terminal.app Tier C; the AMQ 0.4x/0.5x history (doorbell, TIOCSTI, trace, identity tuple, keepalive), "0.60.0 is the supported floor for the 0.60.x series" | the tools as doctor lists them (amq, claude, codex, pi, ollama, tmux, gh); the one floor sentence; the Platforms paragraph (reviewer ruling: macOS on Apple silicon; Linux seats are 3.1 work, the long-path proof socket and ext4/tmpfs inode reuse) |
| Quickstart | `init`, `brief`, `start`/`start --yes`, `dispatch`, `down`, `resume --exec` | "First run", ten owner steps: `amq coop init`, doctor, `doctor --write-registry`, the claude and gh pins (hard link, registry `pins:` shape), the `claude setup-token` file, grants.yaml with an example, `trust`, `team propose`/`apply`/`show`/`check`, `brief`, the three launch entry points (`launch --seats`, `lead --team --scheduler-seats`, `operator start-squad`) and solo mode |
| Execution modes, bounded self-operator, core concepts, orchestration protocol | removed (dispatch-era orchestration) | "Running a squad": status contract, the commit step, `task widen` without `--me`, merge train, release, quarantine, `verify action` exit codes 0, 10, 11, 12, 13 |
| Terminal capability matrix | removed (no pane drivers in 3.0) | none |
| Command map | v2 verbs (`start`, `plan`, `resume`, `down`, `goal`, `send`, `notify`, `completion`, ...) | the 3.0 verbs from `amq-squad --help`, grouped; the exit codes |
| Skills and model guidance, customize, cross-project teams | removed (v2 profiles, `.amqrc` peers by hand, role overrides) | the skills are a pointer to docs/skills.md (E's version) |
| (new) The owner's Buzz face | | enrollment (`enroll.sh`, the password-manager pipe, `remote bind` and the gate binding), the mailbox adapter, gate answers in the gate thread |
| (new) What 3.0 does not do | | the recorded limits: same-UID boundary, hooks fail open, the busy DM, no seats-file compiler, pi darwin arm64 only, the open acceptance rows of plan 16.4, no v2 verb survives |
| (new) Uninstall, Upgrading from v2 | `MIGRATION.md` for 1.x verbs | `uninstall` dry run; `migrate` dry run |
| Reference and moved details | operator cookbook, verification ADR, iTerm2 smoke docs, v2 release history | "Where the plan and evidence live": v3-plan.md, the briefs, docs/v3-spike/, release notes, squad-yaml, status contract, hooks, skills, Buzz, uninstall, follow-ups |

README.html regenerated with pandoc 3.11 (the Makefile pin); `make
readme-html-check`: `README.html is in sync with README.md`.

### The command-run record

Every `amq-squad` invocation in README.md (fences and inline spans with a flag)
was executed by `5-docs-evidence/readme_runs.py.txt`, which loads
scripts/check-skill-invocations.py and uses its extraction, placeholder
substitution, fixture and environment: a throwaway HOME and XDG_CONFIG_HOME, no
AM_*, AMQ_*, GH_TOKEN, GITHUB_TOKEN or GH_HOST, the gate's gh stub first on PATH,
one fresh git fixture per command, against the binary built from this branch.
The same rule as the skills: exit 1 (usage), a timeout, a panic or a gh call
fails.

Result (`5-docs-evidence/readme-runs.txt`): **26 invocations, 0 failures**
(14 exit 0, 11 exit 2 at state resolution in the empty fixture, 1 exit 12
`no_gate`). Four README commands carry `skill-invocation-check:skip`, each
with its reason, because they exit 1 by design where nothing is recorded:
`verify merge` and `verify release` (failed evidence), `remote status` (no
operator session in fleet.yaml), `migrate` (no v2 profile). Six flagless inline
mentions are listed, not executed, as in the skill gate.

Two YAML examples were checked by hand (`5-docs-evidence/yaml-examples.txt`):
the README's grants.yaml, extracted from README.md byte for byte into a
throwaway `~/.amq-squad/grants.yaml` (0600), reads as `mode manual; seat cap 6`
in doctor and refuses a seven-seat proposal as widening; the `pins:` shape
with a real path and sha256 appended to a written registry gives doctor's
`gh pin` row `ok`.

What the README could not say truthfully and says as a limit instead: no verb
compiles squad.yaml into the typed seat list that `launch --seats` and `lead
--scheduler-seats` take (only `operator start-squad` compiles it inside its
own launch), so a manual launch takes a hand-written seats file.

### docs/global-orchestrator-runbook.md: retired

Retired to a one-paragraph pointer. Its v2 content was a hand-run board over
`init`, `start`, `dispatch` and `down`; in 3.0 the same job is the product:
`lead --team --scheduler-seats`, the fleet registry, `status --fleet`,
`operator inbox` and the mayor's `operator start-squad`. A rewrite would
restate those verbs' help. It is dropped from check-release-version.py's
policy list; check-current-skill-routing.py still finds
`amq-squad:orchestrator` in it.

### scripts/check-release-version.py

The AMQ floor is read from one place, `const doctorMinAMQVersion = "x.y.z"` in
internal/cli/doctor.go (exactly one match, else the check fails closed), and
the policy sentence is built from it: `AMQ <floor> is the minimum supported
release`, required in README.md, docs/skills.md, the three cli SKILL.md copies,
README.html and docs/skills.html, with the README line `- `amq` <floor> or later
on `PATH``. No second hand-typed 0.116.2 remains in the script. Rows:
`TestReleaseCheckReadsTheDoctorFloor` (internal/cli: runs `--print-amq-floor`
and compares with `doctorMinAMQVersion`), two Python rows (the floor read from
the repository's doctor.go; a changed constant followed, a missing, doubled or
malformed one refused).

`make release-check VERSION=v3.0.0` on this branch:

```
release metadata matches v3.0.0
current docs and generated team rules route to wizard/cli/orchestrator
```

On the v3 tip before this branch it failed on the v2 "AMQ 0.60.x" sentence.

### The skill gate's gh stub (added item, reviewer)

scripts/check-skill-invocations.py: the refusing gh stub answers `gh
--version` alone with a fixed line (`gh version 0.0.0-skill-gate-stub`) and
exit 0, unlogged; every other argv is refused and logged as before. doctor runs
`gh --version` for its pin row and otherwise reads login state from file
presence only (registry/detect.go), and its ollama probe goes to localhost. The
seven `doctor probes gh` skip marks are removed from plugins/skills-src and the
mirrors regenerated; all seven execute cleanly. Row:
`test_gh_version_is_answered_and_any_other_argv_is_reported` (a stand-in that
runs `gh --version` is not reported and sees the fixed line; `gh --version
extra`, `gh version` and `gh auth status` are). `make skill-invocation-check`:
`76 documented invocation(s) execute cleanly; none reached for gh (stubbed)`
(E's log: 69).

### CI (added item, reviewer ruling)

.github/workflows/ci.yml: the `make ci` job (name unchanged) runs on
`macos-latest`, the runner label of the real-AMQ job. pandoc 3.11 comes from
the pinned `pandoc-3.11-arm64-macOS.pkg` release asset (the Makefile's version
check stays); PyYAML from a venv put first on PATH. The Linux job (name
unchanged, `Claude hook script (dash, linux)`) gates build, `go vet ./...` and
the hook script under dash, and runs `go test ./...` as the step "Linux test
canary" with continue-on-error. The known-red set from the first Linux test
run, run 40955238412, is in the workflow comment by package:

- internal/seatlaunch: the proof socket under long paths (helper_dial_failed,
  no_proof, bind: invalid argument); the replaced-directory and replaced-socket
  identity rows; two cancel and close rows.
- internal/routing: TestFix5SeedReplacedIsRefused,
  TestRepassReportRunsTheStartChecks.
- internal/gatestore: TestSquadIDIsCanonical.

Not run here: the workflow itself (no push from this branch); the first CI run
on the merge shows the macOS job.

## Part 2: help and refusal rows

| Row | Fix | Test |
| --- | --- | --- |
| 4 `task widen` | the task usage line shows `task widen ID --files GLOB,... --reason TEXT` and says there is no `--me` (task.go); the parser (task_widen.go:90) unchanged | `TestDocsFTaskUsageShowsWidenWithoutMe` |
| 5 `verify action` | `12 no gate` in the help's exit codes | `TestDocsFVerifyActionHelpListsExit12` |
| 6 three help paths | `amq --help`/`amq help` print the amq usage and exit 0; `pi verify --help` prints its usage; `verify release-plan --help` prints a usage line and its flags | `TestDocsFHelpPathsPrintUsageAndExitZero` |
| 7 `team propose` | the six per-seat flags (`--runtime`, `--model`, `--effort`, `--worktree`, `--tools`, `--actor-mode`) take one occurrence; a second is refused naming the comma form (`--worktree cto=x,fullstack=y`), for propose and apply | `TestDocsFTeamProposeRefusesRepeatedPerSeatFlag` (the repeated form refused, exit 1; the comma form proposed, applied and read back) |

Left for 3.0.x as the brief says: the doubled doctor line in the top-level help
and the double help prints of context, guard and lead.

Revert check (`5-docs-evidence/revert-check-f.txt`): each fix file reverted
alone to the commit before the fix, its row run (fails), the file restored,
the row run again (passes). Every row fails on revert.

## make ci

`make ci` on this branch (macOS, pandoc 3.11, PyYAML from a scratch venv): exit 0; README.html in sync, the skill command check, `skill-invocation-check: 76 documented invocation(s) execute cleanly`, the release validator, the go files scope test and the Claude hook tests all pass. `go test ./internal/redact` (the ruling 18 docs and evidence scan) passes with the evidence of this brief in place.
