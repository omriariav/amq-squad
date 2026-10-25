# amq-squad uninstall

`amq-squad uninstall [--root DIR | --project P --session S] [--write [--owner-files] [--purge]]`

Without `--write` it is a dry run: it prints every path it would remove and
changes nothing. The session root is `--root`, else `AM_ROOT`, else the AMQ
root of `--project/--profile/--session`.

## Groups

| Group | What | Removed |
| --- | --- | --- |
| session state | `<root>/extensions/amq-squad`: tasks, staged launch records and proofs, alerts, releases, merge-train audit, the scheduler directory (but its exits queue), the verifier directory with its module cache (`verifiers/gomodcache`) | `--write` |
| per-seat config dirs | `launch/<seat>/claude-config/<launch id>` | `--write` |
| core tools dir | `tools/`: the copies of gh and claude | `--write` |
| worktrees | the seat worktrees the commits ledgers and staged launch records name, only when the path is `<project>/.claude/worktrees/<name>`, the project is a `project_path` the task store records for that seat, `git worktree list` of the project names it and the opened directory's `.git` names, by device and inode, the project's own `.git/worktrees` entry; every other candidate is listed as not removed, with the reason. Removal walks from the verified directory's descriptor, and checks before and after that the entry at the parent is still that directory (`worktree_replaced` otherwise, nothing removed from the replacement). Uncommitted work in a removed one is lost | `--write` |
| owner files | `~/.amq-squad`: `registry.yaml`, `grants.yaml`, `claude-token`, `gate-binding.json` | `--write --owner-files` |
| sealed ledgers | the commits ledger, the exits queue, the outcome log and the session seed under `routing/`, the hook ledger | `--write --purge` |
| evidence refs | `refs/amq-squad/evidence/...` in the seat repositories | `--write --purge` |
| clear records, gate store | `clears/`, `gates/`, `answers/`, `denials/`, `reasks/`, `consumed/` | `--write --purge` |

Everything else in `~/.amq-squad` (`bin`, `runtime`, `fleet.yaml`, ...) is listed
as left in place; the verb does not own it. The AMQ mailboxes and the copies of
launch records seat-exec published into them belong to AMQ and stay.

## Kept by default

The sealed ledgers, the evidence refs, the clear records and the gate store are
the audit trail; `--purge` removes them. Refs are deleted only through the
hardened git environment of the commit step (no repository hook, config or
filter driver runs), and only refs under `refs/amq-squad/evidence/`; no branch
and no object of a seat repository is touched.

## Refusals

- `uninstall_busy`: a scheduler process (the lock `scheduler/run.lock` is held,
  or the decision socket is present and the scheduler's pid is alive), a live
  seat (an accepted proof whose process is alive), an open proof window (a staged
  ticket neither used up nor expired), or a commit step (a `why: commit` entry in
  the exits queue). The check fails closed: a file that cannot be opened, read or
  parsed, a directory that cannot be listed, a lock that cannot be opened or
  probed is busy, naming the path. The message names what is live. A writing run
  holds the scheduler's own lock (`scheduler/run.lock`) for its whole length and
  checks again once it holds it, so a scheduler cannot start and a second
  uninstall is refused (`uninstall_busy`) meanwhile; `run.lock` is not removed
  with the rest, it is unlinked as the run's last action. A live pid whose start
  stamp cannot be read, and a stray file where a seat directory is expected, are
  busy as well.
- `uninstall_refused_symlink`: a symlink (or a non-directory) stands where a
  directory is expected, in the AMQ root (nested directories to three levels
  included, such as `verifiers/gomodcache`), the worktrees, or `~/.amq-squad`.
  Nothing is removed. Both refusals exit 2. Removal runs through the descriptor walk (`internal/fsanchor`)
  from the anchored roots, one component at a time with no link followed; a
  path whose ancestors cross a symlink (for example `/tmp` on macOS) is refused,
  so pass the resolved path.

## Secrets

With `--owner-files` the two secrets, `claude-token` and `gate-binding.json`, go
first and unconditionally (before any other group, and whatever else fails):
overwritten once with zeros, then unlinked. Their contents are never printed.
The files are 0600; on APFS and other copy-on-write file systems the overwrite is
best effort, because the file system may keep the old blocks. Rotate the token
(`claude setup-token`) if that matters. A secret with another hard link is not
zeroed (that would destroy the other name's contents): it is unlinked as it is,
the report says so, and the other name keeps the bytes. Removing
`gate-binding.json` leaves any open clear gate unanswerable until a binding is
restored (enrol again with `amq-squad remote`).

## Done by hand

amq-squad never writes the owner's global agent config (R16). The verb prints:

- `claude plugin uninstall amq-squad@amq-squad` (Claude's plugin cache and
  `~/.claude/settings.json` stay untouched);
- the `[projects."<path>"]` trust entries in `~/.codex/config.toml` that name this
  squad's checkouts, and the notify line if it names the fan-out script
  (codex-cli owns that file);
- `git -C <repo> worktree prune` for each repository that had worktrees removed.

## Idempotent

A second run reports "nothing to do". If a removal fails the others in its group
still run, the records that name a failed group are held back (and listed), the
report lists what remains and the exit code is 3; an entry that cannot be
opened for any reason but not existing is a failed entry, never "already gone".
Run it again after fixing the cause.
