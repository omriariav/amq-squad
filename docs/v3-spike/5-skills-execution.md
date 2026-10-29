# Brief 5-skills-E: execution log

Branch `v3-5-skills-e` from the v3 tip `590e66d3`. The seven skill sources under
`plugins/skills-src/` and `docs/skills.md` described the v2 command surface; the
release commit had moved their versions to 3.0.0 without rewriting them. Every
command, subcommand and flag below was taken from the 3.0 binary's own help
(`go build -o amq-squad ./cmd/amq-squad`, then `--help` per verb and subcommand)
and from docs/squad-yaml.md, docs/claude-hooks.md, docs/uninstall.md,
docs/v3.0.0-release-notes.md and MIGRATION.md "Moving to 3.0", and every
documented invocation was executed in a fixture before it was written down.

No Go production code changed.

## Per skill

Command paths named by the skill and its references before (at `590e66d3`) and
after, as `scripts/check-skill-commands.py` extracts them.

### amq-squad:cli

- **Removed:** `evidence run`, `evidence recover` (and the `show`/`list`/`lookup`
  projections of the v2 evidence store), `amq drain`, `amq thread`, `amq route
  explain`, `start`, `resume`, `down`, `send`, `focus`, `verify release-plan` as a
  release step, `doctor --session`, `verify merge --project --evidence`, the
  project/profile/session precedence table and the tmux runtime-control section.
- **Added:** `status --fleet`, `context`, `task show`, `task renew`, `task fail`,
  `task block --route`, `task release`, `evidence list` (the hook ledger) and
  `evidence ingest` (what the hook runs), `guard`, `gate status`, `verify action`,
  `verify merge --repository --pr --task --root`, `verify release --tag`,
  `amq env`, `amq route --to`, `trust`, `roles`, `version`, `dispatch` and `launch`
  (as primitives), `migrate` (as the v2 conversion).
- **Kept:** `status`, `doctor`, `task list`, `task claim`, `task done`, `gate raise
  --list-kinds`.
- **Required policy:** the AMQ compatibility section is kept and moved to the 3.0
  floor (0.116.2, the pinned real-AMQ lane, latest as a canary).

### amq-squad:orchestrator

- **Removed:** `role draft` + `team member add` + `resume --exec --role` (dynamic
  membership), `team member rm --stop --close-panes`, `records[].local_input`
  supervision, `evidence run`, `evidence recover`, `amq-squad send`, the v2
  `.data.records[]` status paths, `verify merge` as a lead preflight.
- **Added:** the status contract (buckets, `attention[]`, argv actions),
  `task add --class --files --pool`, `dispatch --create-task --task-class`,
  `seat commit --dry-run` / `--open-pr`, `task widen`, `merge-train --request
  --dry-run`, `gate raise --action tag` (typed), `gate status`, `verify action`,
  `release prepare`, `task release`, `task reset`, `task set`, `task cancel`,
  `task reconcile`, `team propose --from`, `team check`, `routing report --week`,
  `status --fleet`, `operator inbox`.
- **Kept:** `status`, `doctor`, `task list`.

### amq-squad:wizard

- **Removed:** `wizard --goal`, `init --cwd`, `init --shared-cwd-exception`,
  `team member update --cwd`, `team shared-cwd-exception set`, `team sync --apply`,
  `team rules init --force`, the `start`/`plan` launch review.
- **Added:** `doctor --project`, `trust`, `team propose` (flags and `--from`),
  `team apply --digest`, `team check`, `team show`, `brief --goal` / `--seed-from`
  / `--dry-run`, `launch --seats --dry-run`, `lead --team --scheduler-seats` and
  `operator start-squad` (as launch entry points), `worktree plan`, `worktree
  inspect`, `status`; the squad.yaml draft form and the digest as the approval
  surface.
- **Kept:** `roles`, `role draft` (now without `--binary`, `--profile`, `--session`).

### amq-squad (router)

- **Removed:** `status`, `doctor`, `start`, `team sync`, `verify merge` from the
  team-rules reference; the v2.22 wording.
- **Added:** 3.0 routing by intent, the removed-verb note; the team-rules reference
  now shows the shape `team propose` renders (`team propose`, `brief`, `task claim`,
  `gate raise`, `verify`).

### amq-squad-orchestrator, amq-squad-role-creator, amq-team-setup (redirects)

Still redirects; the wording names the 3.0 flow (no `start`). No command paths.

### Generator

`scripts/generate-plugin-skills.py`: the Claude `argument-hint` values drop
`resume`, `down`, `start`, `focus`, `send`. Mirrors regenerated with
`make skills-generate`; every plugin version line stays 3.0.0.

## Check scripts fixed for 3.0 (each with test rows)

- `scripts/check-skill-invocations.py`: documented invocations ran under the real
  HOME. 3.0 verbs read `~/.amq-squad` (gate binding, grants, fleet) and
  `~/.config/amq-squad/config.json` (drafter), so a documented typed `gate raise`
  could post to the owner's Buzz DM and a documented `role draft` could call the
  owner's drafter. The gate now sets HOME and XDG_CONFIG_HOME inside its temporary
  directory and resolves that directory (macOS `/var` is a symlink; anchored opens
  refuse it). Placeholders ROOT, SHA, TAG, N added. Tests:
  `IsolatedEnvironment` (three rows), `StoreRootPlaceholders`.
- `scripts/check-skill-commands.py`: `trust` and `migrate` take one positional path;
  their probe answer matched neither subcommand contract, so `amq-squad trust P` was
  unobservable and the gate refused a verdict. `POSITIONAL_ONLY` reads that answer
  (line-anchored, bound to the probed verb). Tests: three rows in
  `ThirdRoundFindings`; a removal proof (branch disabled) failed 17 rows.
- `scripts/test_check_skill_commands.py`: the rows that pinned v2 verbs (`start`,
  `team sync`, `evidence run`, `task add` as Go's flag printer, amq's fourteen
  subcommands, team's error-list-only subcommands) pin the same properties on 3.0
  verbs (`verify merge`, `status`, task's `ls`/`complete` aliases, amq's `env` and
  `route`, a wrapped-span probe).

## Pandoc

CI installed pandoc 3.8.3; README.html in the tree was rendered with 3.11, and the
two releases differ (the `<html>` attributes, the generator meta, inline CSS: 26
diff lines on README.html). The checks were therefore unable to agree between this
host (3.11) and CI. Decision: pin both sides to 3.11. The Makefile names
`PANDOC_VERSION = 3.11` and a `PANDOC` override, and every render and check target
refuses another release with a version error instead of a false "stale"
(verified with a scratch 3.8.3: `pandoc 3.11 is required for the HTML renders
(found: pandoc 3.8.3)`); `.github/workflows/ci.yml` installs the 3.11 deb. README.html
is unchanged; docs/skills.html was regenerated with 3.11. Not verified here: that the
Linux 3.11 build renders byte-identically to the macOS one (no Linux host); the next
CI run shows it.

## Check outputs

PyYAML from a scratch venv on PATH (CI's ubuntu runner provides it); binary built by
the targets from this branch.

`make skill-command-check` (full output: `5-skills-evidence/skill-command-check.txt`):

```
skills name 48 command(s); all verb/subcommand paths resolve, 131 of 166 flag(s) verified
flags NOT verifiable from --help for 35 command(s) (help errors without required positionals, or delegates its flag set):
  ...
Ran 60 tests in 6.385s

OK
```

`make skill-invocation-check` (full output: `5-skills-evidence/skill-invocation-check.txt`;
per invocation exit codes: `5-skills-evidence/invocation-exit-codes.txt`, 40 exit 0,
34 exit 2 at state resolution in the empty fixture, 2 exit 12 `no_gate`):

```
skill-invocation-check: 76 documented invocation(s) execute cleanly
  17 flagless inline reference(s) not executed (existence gate covers them):
  ...
Ran 23 tests in 0.004s

OK
```

`make skill-routing-check`:

```
current docs and generated team rules route to wizard/cli/orchestrator
```

`make skills-check`:

```
all 14 SKILL.md frontmatters valid; version markers match manifests
```

`make docs-html-check readme-html-check` (pandoc 3.11):

```
docs/skills.html is in sync with docs/skills.md
README.html is in sync with README.md
```

## What a 3.0 verb lacks that a skill needs (listed, not worked around)

1. No verb compiles squad.yaml into the typed seat list that `launch --seats` and
   `scheduler run --seats` / `lead --team --scheduler-seats` read; only
   `operator start-squad` compiles it internally. The wizard can only point at the
   JSON shape in `launch --help`.
2. `team propose` prints each file's path, action and sha256 and the digest, never
   the bytes, so the owner approves a digest without seeing the squad.yaml or
   team-rules.md it covers (`--from` lets the reviewed draft be the input, for
   squad.yaml only).
3. `task widen` help shows `--me LEAD`; the parser refuses `--me`.
4. `verify action` exits 12 (`no_gate`), which its help's exit-code list omits.
5. A repeated per-seat flag on `team propose` (`--worktree a=x --worktree b=y`)
   silently keeps only the last; only the comma form works.
6. `team propose --from FILE` reports a parse error against
   `<project>/squad.yaml:1` instead of FILE.
7. The worktree isolation check (`internal/cli/worktree_isolation.go`) has no
   production caller, while the rendered team-rules say "readiness fails closed
   without" `shared_cwd_exception`. The skills say the launch does not check it.
8. The rendered team-rules.md has no Skills section, so a seat is not told which
   skill to load.
9. Help defects: `amq-squad amq --help` exits 1 (`unknown 'amq' subcommand:
   "--help"`); `pi verify --help` prints nothing and exits 0; the top-level help
   lists `doctor` twice; `context`, `guard` and `lead` print their help twice;
   `verify release-plan --help` prints no usage or flags.
10. 35 named flags are unverifiable from help: the task verbs write
    `--project/--profile/--session` as one slash token, `roles` has no Usage block,
    and team's option lines put several flags on one line.
11. A typed `gate raise` has no dry run: raising always writes the gate record and
    posts to Buzz, so no documented typed raise can be executed offline (the skills
    mark it skip with the reason).

## Outside this brief, found on the way

- `make release-check VERSION=v3.0.0` fails on the v3 tip:
  `scripts/check-release-version.py` still requires the v2 AMQ 0.60.0 policy
  sentence in README.md, README.html, docs/skills.md, docs/skills.html,
  docs/global-orchestrator-runbook.md and the cli skill. README already states
  0.116.2; this branch states 0.116.2 in the cli skill and the guide. The script's
  policy constant needs the 3.0 floor.
- README.md and docs/global-orchestrator-runbook.md still describe v2 (What's new
  v2.31.0, the v2 quickstart, the 0.60 policy).
