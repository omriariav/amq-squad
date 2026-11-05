# 3.2.0-0d screen and store evidence

Recorded on 2026-11-05. The commands below use temporary one-line production
reverts in the seat worktree. Each production line was restored immediately
after its expected failing run. No test used a real AMQ session root.

## A file-lock wait names the holder and declaration

Command:

```sh
cd pi
node --test --test-reporter=spec --test-name-pattern='file lock' test/screen.test.ts
```

Reverted line: the `waitWords` branch for `file_lock` with populated
`waits_for` was disabled in `pi/extensions/lib/words.ts`.

First failing line:

```text
✖ a pending task waiting on a file lock names its holder and shared declaration
```

The failure showed the old shipped text, `t26 waiting: t71 hold its files`, in
place of `t26 waits for t71: both declare docs/v3-spike/320-evidence/`.

## `task add` reports overlap in text and JSON

Command:

```sh
GOCACHE=/private/tmp/amq-squad-reviewer-gocache go test ./internal/cli -run '^TestTaskAddReportsFileWaitsInTextAndJSON$' -count=1
```

Reverted line: the `AddWithFileWaits` append guarded by
`task.OverlapDeclaration` was disabled in `internal/task/task.go`.

First failing line:

```text
--- FAIL: TestTaskAddReportsFileWaitsInTextAndJSON (0.07s)
```

The assertion received only `Added t2 "overlap": pending` and required
`t2 will wait for t1: both declare docs/x.md`. The fixture deliberately uses
`docs/**` against `docs/x.md`, a pair for which a prefix-only test disagrees
with `task.FilesOverlap`.

## `pin_unresolved` names the class chain

Command:

```sh
GOCACHE=/private/tmp/amq-squad-reviewer-gocache go test ./internal/routing -run '^TestLeadPin$' -count=1
```

Reverted line: the `pinUnresolvedDetail` result in
`internal/routing/router.go` was replaced with the bare `pin_unresolved`
detail.

First failing line:

```text
--- FAIL: TestLeadPin (0.00s)
```

The route carried `Detail:pin_unresolved` instead of the pinned class chain.

## Green verification

```sh
GOCACHE=/private/tmp/amq-squad-reviewer-gocache go test ./internal/task -run 'TestFilesOverlap' -count=1
GOCACHE=/private/tmp/amq-squad-reviewer-gocache go test ./internal/cli -run '^TestTaskAddReportsFileWaitsInTextAndJSON$' -count=1
GOCACHE=/private/tmp/amq-squad-reviewer-gocache go test ./internal/status -run 'TestAFileLockWaitNamesItsHolders|TestGoldenRoundTripAdditiveBothWays|TestFleetGoldenIsTheSquadGoldenWrapped' -count=1
GOCACHE=/private/tmp/amq-squad-reviewer-gocache go test ./internal/routing -run '^TestLeadPin$' -count=1
cd pi
node --test --test-reporter=spec --test-name-pattern='file lock|pin_unresolved|class chain' test/screen.test.ts test/words.test.ts
node scripts/lint.mjs
```
