# 3.0.2 follow-ups: the release body from a file, the widen and re-pin hints, the marketplace row (execution note)

Branch v3-302-followups on origin/v3 2f88eca1, in the scratch worktree
/Users/omri/scratch-302-followups. Brief: 3.0.2-followups, the reviewer
rulings of 2026-10-30. No network: the release rows fake gh and git as the
existing releaseflow rows do (a bare fixture repository is the remote);
the CLI rows run under internal/cli's TestMain HOME; the pi tests ran
offline (`npm ci --offline --ignore-scripts`). No canary or credential
shape was added.

## 1. The release body comes from a file; --commit must name a commit

On 2026-10-30 the gate warning given as `--notes` became v3.0.1's public
release body and was fixed by hand. Now:

- `release prepare` and `release publish` require `--notes-file PATH`. The
  file's content is the GitHub release body (the draft's body, with the
  creation nonce marker after it). `--notes` stays the gate note, shown in
  the gate question only.
- The body's sha256 is part of the release spec recorded at prepare. The
  spec's one note field (operatorauth.ReleaseNote.Summary, single line,
  integrity-bearing) carries it as `<note> release body sha256:<hex>`, so
  the owner's gate question also shows the digest of the body being
  approved. operatorauth is outside the declared files, so no new spec
  field was added; this is the smallest diff that records the digest in
  the spec.
- `Flow.Publish` refuses, before anything runs (not even the tag), a body
  whose digest is not the recorded one: `release_body_mismatch`. A release
  prepared without a recorded digest is refused the same way (fail closed).
  At the CLI, a changed notes file also changes the spec, so the existing
  `store.Create(spec)` check in publish refuses first
  (`ErrSpecChangeRequiresSuccessor`); the flow's own refusal is the named
  one behind it.
- Before any gate is raised, `Flow.Prepare` runs `CheckCommit`: the commit
  must resolve locally (`git -C <project> cat-file -e <sha>^{commit}`,
  else `commit_unknown`) and the remote branch must contain it, resolved on
  the remote through the executor as TagCommit resolves the tag (new
  `Executor.BranchContains`; GH reads `repos/R/compare/<branch>...<sha>`,
  identical or behind is contained, 404 is not), else
  `commit_not_on_branch`. The branch is the new `--branch` flag, default
  main (the brief names "the named branch"; main is where every tag is cut
  from 3.0.1 on).

Rows (internal/releaseflow/releaseflow_test.go, internal/cli/release_flow_notes_test.go):

| Row | Reverted change | Result when reverted |
|---|---|---|
| TestReleaseBodyIsTheNotesFile | the draft body back to the gate note | FAIL |
| TestPublishRefusesABodyThatIsNotThePreparedFile | the digest check in Publish | FAIL |
| TestPrepareRefusesACommitThatNamesNothingBeforeAnyGate | the CheckCommit call in Prepare | FAIL |
| TestReleaseRequiresTheNotesFile | the --notes-file requirement | FAIL |

## 2. The widen tool's message and the re-pin refusal hint

- pi/extensions/lib/tools.ts: the amq_task_widen refusal for a task not
  blocked with outside_declared_files now ends with what the lead can do:
  for a seat's own block with a widen request, return the task with
  `amq-squad task reset <id> --pool RUNTIME:MODEL:EFFORT`, until the typed
  block for a widen request exists (t20). Row: pi/test/tools.test.ts, in
  the argv widening test; FAIL when reverted.
- internal/cli/task.go: origin/v3 has no re-pin refusal yet (t12fix adds
  one in internal/task/lease.go, outside these declared files). `task set
  --pool` on a task whose attempt has started is refused in the CLI with
  "task set: task <id> has started attempt <n>; pool cannot be changed
  after the first claim; use task reset --pool". The wording matches
  t12fix's store refusal plus the hint, and the CLI check runs first, so
  with t12fix merged the user sees one line. At merge, keep one: either
  this pre-check, or t12fix's store message with the hint appended.
  Row: TestTaskCLISetRepinRefusalNamesReset (end of task_test.go, a name
  distinct from t12fix's TestTaskCLISetRepinBoundary); FAIL when reverted.

## 3. The release-check row for the marketplace

scripts/check-release-version.py gains `require_marketplace_ref`: both
plugin manifests at `origin/main` (the local view of main, which the
marketplaces install from) must say the version being released; a missing
ref or an unreadable manifest fails too. The comment records the owner's A
of 2026-10-30 (v3 is merged to main before every tag from 3.0.1 on). Rows
in scripts/test_check_release_version.py (RequireMarketplaceRefTest): main
on the version passes; main on another version fails; a missing ref fails;
main() runs the row. Reverting the version compare or the call in main()
fails a row. On this worktree `check-release-version.py v3.0.1` passes:
origin/main already carries 3.0.1.

Open for the lead: CI's "Run release metadata check" step runs this script
on a release pull request. A version-bump PR into v3 runs it while main
does not carry the version yet, so that step now fails until v3 is merged
to main. That matches the ruling's order only if the bump reaches main
first. Changing the CI step (for example, running this row only at tag
time) touches .github/workflows/ci.yml, outside the declared files, so it
was not changed.

## Not changed, outside the declared files

The orchestrator skill's release example (plugins/skills-src/orchestrator/
SKILL.md:160 and its claude and codex mirrors) runs `release prepare`
without `--notes-file`, which is now required. `make skill-command-check
skill-invocation-check` still passes (they check names, not required
flags), but the example needs `--notes-file PATH` added by a skills change.

## Verification

```
go build ./... && go vet ./internal/releaseflow ./internal/cli && go test -count=1 ./internal/releaseflow ./internal/cli
ok  	github.com/omriariav/amq-squad/v3/internal/releaseflow
ok  	github.com/omriariav/amq-squad/v3/internal/cli
make release-validator-test   (16 tests OK, 4 tests OK)
cd pi && npm test             (39 pass, 0 fail); npm run lint: lint ok
```
