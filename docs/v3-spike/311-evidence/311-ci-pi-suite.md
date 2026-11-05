# 3.1.1: make ci runs the pi suite

Finding (plan reviewer, 2026-11-05): the Makefile's `ci` target had no pi
target and .github/workflows/ci.yml had no Node step, so the pi suite (the
3.1.1-3 screen rows, the /confirm door rows, the words-table completeness
row) guarded no merge. v3 went red on the words-table row after #867/#871
and CI did not notice. The required check is the `make ci` job (ruleset
26199029), so the fix lands inside `make ci`; the ruleset is unchanged.

Branch `v3-311-ci`, from origin/v3 `2dcff7e2d799beda78923f8a6cfd39a6cf82a4c7` (#871).

## The change

- **Makefile.** A `pi-test` target, `cd pi && npm ci --ignore-scripts $(NPM_FLAGS) && npm test`,
  is the last dependency of `ci`. `--ignore-scripts` is fixed. `NPM_FLAGS` is
  empty by default, so CI's install may reach the registry (setup, not a
  test); `NPM_FLAGS=--offline make ci` works locally with a warm npm cache.
- **ci.yml.** The `make ci` job gains a "Set up Node" step after "Set up Go":
  `actions/setup-node@49933ea5288caeca8642d1e84afbd3f7d6820020 # v4.4.0`
  (the v4.4.0 tag's commit), `node-version: 22.19.0`, the floor
  pi/package.json's `engines` declares (`>=22.19.0`), exact rather than the
  range so the runner's Node does not float. Nothing else in the workflow
  changed.

## The row: a red pi test fails make ci

The tip this branch starts from is itself red in the pi suite (the
words-table row: `train rule strict_required_status_checks_policy has no
words`; PR #873 adds the words). So the proof ran in a throwaway detached
worktree at PR #873's head `c9b749994bc111a38ef238643a504d8045836199` with
this branch's diff applied uncommitted, PyYAML venv first on PATH, pandoc
3.11, local Node v22.21.1.

Command, each run: `make ci`.

1. **Baseline**: green. Tail: `ℹ tests 110`, `ℹ pass 110`, `ℹ fail 0`, exit 0.
   The log shows the target ran: `cd pi && npm ci --ignore-scripts  && npm test`.
2. **Injected failure**: a new file pi/test/zz-ci-proof.test.ts with one test,
   `assert.equal(1, 2, "injected failure: make ci must fail on a red pi test")`.
   First failing line: `✖ ci proof: an injected pi failure fails make ci (0.488667ms)`.
   Tail:
   ```
   ℹ pass 110
   ℹ fail 1
   AssertionError [ERR_ASSERTION]: injected failure: make ci must fail on a red pi test
   make: *** [pi-test] Error 1
   ```
   `make ci` exit 2.
3. **Restored** (the file removed): green. Tail: `ℹ tests 110`, `ℹ pass 110`,
   `ℹ fail 0`, exit 0.

On this branch alone (no #873) `make ci` fails at `pi-test` on the
words-table row, which is the regression this target now catches.

## Unchecked

- Node 22.19.0 itself was not run locally (only v22.21.1 is installed); the
  suite's `.ts` files need type stripping, unflagged since Node 22.18.0. The
  first CI run on this PR is the 22.19.0 check.

## For the 3.1.1 release notes

- `make ci`, the required check, now runs the pi extension's test suite
  (`make pi-test`), and the CI job sets up Node 22.19.0, so a red pi test
  blocks a merge.
