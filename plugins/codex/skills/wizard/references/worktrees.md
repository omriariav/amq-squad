# Worktree isolation: why, when, and how

This reference explains the `worktree_isolation` launch preflight and matching
runtime doctor check.

## The rule

Two or more **mutation-capable** members must not share one working directory.

A shared directory means a shared Git index. Two agents editing and staging in one
index will interleave each other's partial work, and the result is not recoverable by
either of them: neither knows which hunks are theirs. Launch preflight therefore
fails closed on a detected collision rather than warning.

Review-only members do not count. The check looks at members whose actor mode is
implementation.

## Two ways to satisfy it, both executable

**Give each member its own directory.** At creation:

```
amq-squad init --profile NAME --cwd "dev-1=/path/to/wt-a,dev-2=/path/to/wt-b"
```

On an existing roster, one member at a time:

```
amq-squad team member update dev-2 --cwd /path/to/wt-b --project P --profile NAME
```

A relative `--cwd` resolves against the **project**, not against your shell's working
directory. That distinction is deliberate: a value recorded relative to a shell would
mean something different the next time a command ran from elsewhere.

Passing an empty value clears the override and returns the member to the team-home.

**Or accept the shared checkout deliberately.** At creation:

```
amq-squad init --profile NAME --shared-cwd-exception "single checkout accepted for this run"
```

On an existing profile:

```
amq-squad team shared-cwd-exception set "<reason>" --project P --profile NAME
```

The exception is recorded, so a later reader can see the decision and its reason
rather than guessing why the check passed.

## Which to choose

Isolate when members will edit overlapping files, when a run is long enough that
recovery matters, or when a reviewer needs to check out one member's work
independently. Accept the exception for short single-file runs, or when only one member
will actually mutate despite several being capable.

## What launch preflight reports

A blocked preflight names the colliding directory and the roles sharing it, then
names both remedies with exact commands scoped to your project and profile. Read
the fix text before reaching for anything else; it is generated from your roster,
so the role names in it are yours.

Where a member's directory does not exist yet, preflight groups by planned directory
and says so. That is a prediction, not an observation: two planned directories that
would resolve to one checkout can still be caught by `doctor` at runtime.

## Relationship to `doctor`

`doctor` checks the same condition at runtime and reports it as
`shared-index-collision`. Launch preflight and doctor honour the same exception.
Preflight runs before a new agent starts and fails closed; `doctor` sees live
processes and can be liveness-aware.

Same condition, same exception, stage-appropriate severity and remedy.

## Ownership boundary

One isolated worktree per mutation-capable member, branched from the exact base the
task names. Stay inside the reported scope. A shared hotspot — generated files,
schemas, manifests, central registries, dependency locks — gets one explicit
integrator; do not edit it from a second branch. Never merge or reconcile a peer's
branch unless the task assigns you integration.
