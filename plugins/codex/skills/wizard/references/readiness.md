# Launch diagnostics and preflight

Simple Mode has no readiness stage or readiness manifest. Flow A uses
`amq-squad setup` for machine-level drafter defaults and `doctor` for runtime
sanity. `start` performs ordinary preflight against the authoritative roster
and current external runtime before it asks for approval and again under the
launch lock before it spawns.

Run doctor from the target project. If it reports that no eligible AMQ root is
configured, follow its printed project-root initialization/configuration
remedy and rerun doctor; a global mailbox is not implicit authority for a Git
worktree. Before a profile exists, a missing-team warning is expected. Doctor
checks project/profile health and uses `--session` only for additive worktree
diagnostics, so inspect a proposed new namespace with `status --json` and the
subsequent start plan rather than claiming doctor proved it unused.

## Drafter readiness

`setup` writes the global user config and reports detected backends. The
effective resolver chooses the complete profile block when present, otherwise
the complete global block, otherwise `in_session`; it never merges fields
between layers. A configured chain tries only its explicit order.

Role draft, custom team-rules prose, and a missing goal-first brief print the
resolved config source plus every attempt and fall-through. Missing binary,
credentials, timeout, non-zero exit, empty or oversized output, and validation
failure are not successes. `on_failure: error` fails closed. The default
`in_session` fallback prints a filled prompt and stops before mutation.

## What preflight checks

- the project, profile, session, and canonical AMQ root resolve unambiguously;
- each configured binary exists and accepts its selected model/trust options;
- mutation-capable actors satisfy the worktree-isolation rule;
- launch records are valid and do not describe multiple live matches;
- a launcher-stamped unmanaged pane will not be duplicated;
- the tmux backend and target are available.
- a generated goal-first brief passed exact title and six-section validation
  before any brief, namespace, lock, or pane mutation.

These checks observe current inputs and external runtime. They do not produce a
digest, token, accepted generation, or second record that certifies the roster.

## Reading failures

Use the exact class and target in the error:

| Class | Meaning | Safe response |
|---|---|---|
| `duplicate_live` | more than one live record matches | inspect the named records; do not elect a winner |
| `record_invalid` | a selected launch record is inconsistent | inspect that record and the reported field |
| `unmanaged` | a launcher-stamped pane exists without a launch record | inspect the named pane before any new launch |
| `stopped` | the recorded process or pane is no longer live | rerun `start` to reconcile it |
| `live/config-diverged` | the actor is live but current roster config differs | keep it live until the operator chooses `down` then `start` |
| drafter chain exhausted | every configured backend failed | inspect each recorded attempt; fix the first intended backend or complete the filled prompt in-session |
| generated prose invalid | output failed deterministic structure/content checks | do not write it; fix the backend/prompt inputs and generate a fresh preview |

After an interrupted launch, rerun `start`. It keeps verified live actors and
rolls the partial launch forward; it does not require manual namespace cleanup.
