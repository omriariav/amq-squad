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

Go and the running `amq-squad` version are required. If AMQ is absent or
`amq version` fails, creation still succeeds and `amq_version` records an
explicit `unavailable: <short reason>` value instead of losing the review
worktree.

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

Both modes remove every ambient variable whose name starts with `AM_*`,
`AMQ_SQUAD_*`, `TMUX`, or `GIT_` before starting the process. This includes
agent and terminal identity plus Git repository, worktree, index, object,
alternate-object, namespace, and replace-ref controls such as `GIT_DIR`,
`GIT_WORK_TREE`, `GIT_COMMON_DIR`, `GIT_INDEX_FILE`, `GIT_OBJECT_DIRECTORY`,
`GIT_ALTERNATE_OBJECT_DIRECTORIES`, `GIT_NAMESPACE`, and
`GIT_NO_REPLACE_OBJECTS`. Helper-owned Git commands use the same sanitization,
so an ambient Git override cannot defeat `--repo`. Normal process settings
such as `PATH`, `HOME`, `SHELL`, `GOCACHE`, and `TMPDIR` remain available.

The worktree is intentionally retained after the command or shell exits so
the evidence and manifest do not disappear. Its location and paired cleanup
command are printed before the reviewer process starts.

## Clean up safely

```sh
amq-squad review-worktree remove /tmp/amq-squad-review-123456
```

Removal is fail-closed: the path must be under the system temporary directory,
have the helper's generated name and matching manifest, remain a detached
worktree at the manifest's exact commit and tree, share the recorded
repository's canonical common Git directory, and still be registered there.
Registered-worktree cleanup uses only:

```sh
git worktree remove --force <path>
```

The helper never runs `rm` or `rm -rf` for worktree cleanup. If Git rejects
`worktree add` before registering or populating the freshly allocated empty
directory, the helper removes only that empty directory with non-recursive
`os.Remove`.
