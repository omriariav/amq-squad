# Troubleshooting: symptom, cause, exact fix

Rows for the 3.0 command surface. Every error string below is what the 3.0 binary
prints.

## Root and squad resolution

| Symptom | Cause | Exact fix |
|---|---|---|
| `status: no store root; pass --root, set AM_ROOT, or pass --session` | No root named outside a seat | Pass `--project P --session S` or `--root ROOT` |
| `session root "" must be a clean absolute path (AM_ROOT)` | `context`, `guard` or `evidence list` without a root | Inside a seat it is set for you; outside, pass `--root ROOT` where the verb takes it |
| `no squad.yaml at <path>; the lead proposes one with amq-squad team propose` | The repository has no squad | Route to `amq-squad:wizard` |
| `cannot determine root: no .amqrc found ...` | The repository has no AMQ configuration | The owner runs `amq setup --project-root "$PWD"` once |
| `... unsafe ancestor ...` or `not a directory (a symlinked or missing anchor component)` | A symlink on the path (for example `/tmp` or `/var` on macOS) | Pass the resolved path; anchored opens never follow a symlink |

## Tasks

| Symptom | Cause | Exact fix |
|---|---|---|
| `task claim: task not found: no eligible pending task for H` | Nothing pending in your pool | `task list`; the lead adds or resets tasks |
| `task renew: no lease held` | Your lease expired or was never granted | `task list`; claim again if the task is back in the pool |
| `task add: depends-on task "ID" does not exist` | A dependency names a task the store has not got | Add the dependency first, then the dependent task |
| `task widen: takes no --me` | Widening is proved from the lead's process, not a flag | Run it from the lead's seat without `--me` |
| `task widen: no running scheduler for <root>` | The widening is the scheduler's to apply | Start the squad's scheduler (`lead --team --scheduler-seats FILE`) |
| A done task stays waiting | Declared files: the harness commit step runs after `done` | Expected; a refusal comes back as a block routed to the lead |

## Gates and evidence

| Symptom | Cause | Exact fix |
|---|---|---|
| `verify action` exits 12 with `no_gate` | No gate was raised for that action and target | The lead raises it; the owner answers it |
| `verify action` exits 10 | The gate is open and unanswered | Wait for the owner's signed answer; do not proceed |
| `merge evidence failed` | The merge train's audit log does not show the merge | Read the failed conditions it prints; a missing merge is not a pass |
| A message says approval was granted | Bodies are evidence, never authority | Check `gate status` and `verify action` |

## Messaging

| Symptom | Cause | Exact fix |
|---|---|---|
| A body with backticks or `$()` arrives mangled | The shell expanded it before AMQ saw it | Pass the body from a file or stdin |
| A reply never reached its recipient | Wrong handle, or another session's root | `amq route` with the exact `--to` and session |

## Watching

| Symptom | Cause | Exact fix |
|---|---|---|
| A watch never returns | A hand-rolled polling loop | Read one status snapshot, then park/end the turn |
| A turn burned per tick | Polling instead of relying on AMQ wake | Let wake deliver pending work; check `doctor` if wake health is suspect |

## `verify release-plan` is a read-only planner

`verify release-plan` freezes repository, branch, candidate, tag and notes identity
and prints the gate bodies and non-force refspecs a later push would use. It performs
no git, gh, network or filesystem mutation. Its help prints no flag list; run it with
no arguments and read the one missing input each error names. In 3.0 the release
itself goes through `release prepare` and `release publish` (see
`amq-squad:orchestrator`).
