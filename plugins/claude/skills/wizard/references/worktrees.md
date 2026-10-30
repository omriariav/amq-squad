# Worktree isolation: why, when, and how

## The rule

Two or more **mutation-capable** seats must not share one working directory.

A shared directory means a shared Git index. Two agents editing and staging in one
index interleave each other's partial work, and neither knows which hunks are theirs.
The harness commit step snapshots a seat's whole tree, so a tree that holds a second
seat's edits is refused for the paths outside the task's declared files.

Review seats (`actor_mode: review`) do not count.

## Two ways to satisfy it

**Give each seat its own worktree.** In squad.yaml, `seats[].worktree` is a
repository-relative or absolute path; on the command line it is one comma-separated
`--worktree r=path,r2=path2` (a repeated `--worktree` keeps only the last one):

```sh
amq-squad team propose --project P --roles cto,fullstack,backend-dev --worktree fullstack=../app-fullstack,backend-dev=../app-backend --lead cto --session S
```

**Or accept the shared checkout deliberately.** Record the reason as
`shared_cwd_exception` in a squad.yaml draft and propose it with `team propose --from
FILE`. The exception is recorded in squad.yaml and in the rendered rules, so a later
reader sees the decision and its reason.

v2's `team shared-cwd-exception` verb and `--cwd` member edits are gone; squad.yaml is
the only place either choice lives.

## Which to choose

Isolate when seats edit overlapping files, when a run is long enough that recovery
matters, or when a reviewer needs to check out one seat's work independently. Accept
the exception for short single-file runs, or when only one seat will actually mutate
despite several being capable.

## What enforces it

The launch does not check isolation, so the squad's seat list is where it holds.
Inside a run, the task store's file locks keep two tasks off the same declared files,
and the commit step refuses any path outside a task's declared set.

## Worker worktrees for a task

`amq-squad worktree` plans and materializes one deterministic worktree per role and
task from an exact base:

```sh
# skill-invocation-check:skip — plan reads squad.yaml and the repository at an exact base commit
amq-squad worktree plan --role ROLE --task ID --base SHA --scope internal --session S --project P --json
amq-squad worktree inspect --project P --session S --json
```

`plan` and `inspect` are read-only; `materialize`, `activate`, `handoff` and `cleanup`
are explicit `--yes` operations, and `cleanup` removes only a clean worktree at the
exact registered path.

## Ownership boundary

One isolated worktree per mutation-capable seat, branched from the exact base the
task names. Stay inside the task's declared files. A shared hotspot (generated files,
schemas, manifests, central registries, dependency locks) gets one explicit
integrator; do not edit it from a second branch. Never merge or reconcile a peer's
branch: seats never merge.
