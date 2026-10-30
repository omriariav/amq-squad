# LEARNINGS — wizard

Field failures from setup and launch runs, newest first. Entries graduate into
the Gotchas table in `SKILL.md` once they generalise.

> **3.0 note.** Entries written before 3.0 name v2 verbs that 3.0 removed (`start`,
> `resume`, `down`, `send`, `agent up`, `team member`, `evidence run`, profiles). The
> lesson stands; the command does not. Use `SKILL.md` for the 3.0 form.

---

## A repeated per-seat flag keeps only the last value (v3.0.0)

`team propose --worktree fullstack=../a --worktree backend-dev=../b` writes only
backend-dev's worktree; fullstack silently falls back to the repository root. The
per-seat options (`--runtime`, `--model`, `--effort`, `--worktree`, `--tools`,
`--actor-mode`) take one comma-separated list: `--worktree fullstack=../a,backend-dev=../b`.
Read the proposed squad.yaml back (`team show` after apply) before launching.

## The digest is the approval, not the bytes (v3.0.0)

`team propose` prints each file's path, action and sha256 and one digest; it does not
print the bytes. When the owner needs to review exact squad.yaml content, write the
draft yourself and propose it with `--from FILE`, so the reviewed file is the input.

---

## The launch result must describe runtime truth

**A generated brief and its approval stay in one invocation.** When `start
--goal` finds no active brief, the configured drafter can return different prose
on a later run. Review and answer Yes at the same interactive prompt so the
reviewed-byte lock protects exactly what was shown. A cancelled preview is not
authorization for a later `--yes` redraft.

**Drafter evidence is part of the decision.** Profile config replaces the whole
global block, and an explicit chain can fall through several commands. Preserve
the reported config source and every attempt instead of summarizing only the
last backend.

**A launcher must not report success it did not verify.** A pane can exist while
its agent process has already exited. `start` verifies that every launched pane
owns its live child before reporting success; a dead child is a launch failure
with a role-specific remedy.

**One prompt is enough.** The old prepare/readiness/go protocol asked the
operator to approve owned representations of the same inputs. Simple Mode shows
the complete plan once, defaults to No, and launches only after explicit
approval.

**Interrupted launch is a reconciliation case.** Rerun `start`. It keeps roles
whose recorded processes are verified live and starts only missing or stopped
roles. Deleting the namespace first discards useful recovery state.

---

## Standing traps

**Unscoped commands can target another roster.** Named profiles need
`--profile`. Always keep project, profile, and session coordinates explicit in
automation and durable instructions.

**Actor mode drives the isolation check.** A roster where every member defaults
to implementation will block on a shared directory even when only one member
was expected to write code. Assign accurate actor modes or record an explicit
shared-CWD exception.

**An existing rules file is not silently rewritten.** Adding `--orchestrated`
to an existing roster does not add the generated reporting norm to existing
`team-rules.md`; regenerate it deliberately. (In 3.0 the rules are rendered from
squad.yaml by `team propose` on every proposal.)
