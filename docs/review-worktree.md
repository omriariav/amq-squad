# Exact-commit review worktrees

`amq-squad review-worktree` creates a detached worktree for a single resolved
commit. It prevents three common review failures: reviewing a moving ref,
running tests with a live agent's AMQ/tmux identity, and producing evidence
without machine-readable commit provenance.

## Create and inspect

```sh
amq-squad review-worktree --repo ~/Code/app <ref>
```

`<ref>` may be a full SHA, tag, or branch. The helper resolves it once with
Git, checks out the resulting full commit SHA under the system temporary
directory, verifies the checkout's tree and clean state, and prints its path.
It never uses the source checkout's uncommitted files.

Every review worktree contains `.amq-squad-review.json` with:

- exact commit SHA and tree hash;
- UTC creation timestamp;
- Go, `amq-squad`, and AMQ versions; and
- source repository, source ref, and worktree identity.

Keep generated evidence inside this worktree until it has been inspected or
copied to its durable destination. The manifest then travels alongside the
evidence and proves which tree produced it.

## Run with an isolated environment

Run one reviewer command:

```sh
amq-squad review-worktree exec --repo ~/Code/app <ref> -- go test ./...
```

Or open the user's preferred shell in the isolated checkout:

```sh
amq-squad review-worktree shell --repo ~/Code/app <ref>
```

Both modes remove ambient `AM_*`, `AMQ_SQUAD_*`, and `TMUX*` variables before
starting the process. This includes `AM_ROOT`, `AM_BASE_ROOT`, `AM_ME`,
`AM_SESSION`, `AM_WAKE_FD`, `TMUX`, and `TMUX_PANE`. Normal process settings
such as `PATH`, `HOME`, and `SHELL` remain available.

The worktree is intentionally retained after the command or shell exits so
the evidence and manifest do not disappear. Its location and paired cleanup
command are printed before the reviewer process starts.

## Clean up safely

```sh
amq-squad review-worktree remove /tmp/amq-squad-review-123456
```

Removal is fail-closed: the path must be under the system temporary directory,
have the helper's generated name and matching manifest, and still be registered
to the recorded repository. Cleanup uses only:

```sh
git worktree remove --force <path>
```

The helper never runs `rm -rf` for worktree cleanup.
