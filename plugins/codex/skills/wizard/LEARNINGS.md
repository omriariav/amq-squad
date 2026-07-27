# LEARNINGS — wizard

Field failures from real preparation and launch runs, newest first. Entries graduate
into the Gotchas table in `SKILL.md` once they generalise.

---

## v2.25.0 launch run: six blockers in one session

Every one of these was hit preparing a single squad, which is why the Gotchas table
exists. All are fixed in v2.25.0; the recovery lines matter for operators on older
builds.

**A remedy that named a flag but no command.** `worktree_isolation` said to give each
member "its own `--cwd`" without naming a command that accepts it. The flag existed on
`team init` and `new profile`; nothing said so, so it read as nonexistent. Cost more
operator time than any other item in the run. *A remedy the reader cannot execute is
not a remedy.*

**A remedy that required a profile the failure had just deleted.** The other suggested
fix modified an existing profile — but preparation is transactional, so the failed run
had rolled that profile back. The loop had no exit until you knew to create the profile
separately first. *Guidance must state which case you are in.*

**A flag silently rejected as a positional.** `new profile NAME --actor-mode ...` failed
with "takes exactly one profile name", blaming the operator's own argument for a gap in
an internal allowlist. Seventeen value-taking flags were affected, including
`--shared-cwd-exception`, which is itself one of the two documented remedies above.

**Every agent dead at bootstrap with identical operands.** `namespace drift:
accepted=squad/v2-25-0 current=squad/v2-25-0` — the compared field was the project path,
recorded relative and resolved absolute, while the message printed only the namespace.
*An error whose two operands read the same is a message bug, not a state bug.*

**`up` reported success over three dead panes.** Panes existed and were correctly
titled, but no binary was running; the only signal was a later, unrelated-looking
readiness timeout. *A launcher must not report success it did not verify.*

**Readiness passed every row, then spawn refused.** Readiness emitted a ready row without
running the check spawn would run. *Readiness is only useful if it checks what the next
stage checks.*

---

## Standing traps

**`--prepare-plan`, `--prepare`, and `--go` are not aliases.** Never move an operator
from a generic preview straight to `--go`. Preparation approves what will be WRITTEN;
launch approves what will RUN.

**The digest is the approval.** `--prepare-plan` emits it and `--go --goal-digest`
consumes it. Re-summarising the proposal before asking invites the operator to approve
text that differs from what the digest attests.

**Unscoped commands can target another roster.** Named profiles need `--profile`.
Unscoped resolution may pick a different live record, and the mutation succeeds against
the wrong team.

**Actor mode drives the isolation check.** A roster where every member defaults to
implementation will block on a shared directory even when only one member was ever going
to write code.
