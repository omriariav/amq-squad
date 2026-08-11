# LEARNINGS — wizard

Field failures from setup and launch runs, newest first. Entries graduate into
the Gotchas table in `SKILL.md` once they generalise.

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
`team-rules.md`; regenerate it deliberately with `amq-squad team rules init
--force`.
