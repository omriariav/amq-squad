# Wizard readiness and launch preflight

The binary-owned wizard begins with a read-only readiness stage. It resolves
the target project and effective global drafter configuration, reports the
config path and source, and checks the AMQ, tmux, and selected agent binaries.
It then builds the exact profile or reusable-profile plan that the combined
default-No review will show. Do not assemble a separate setup/doctor sequence
to certify the launch.

After approval, the existing start primitive performs ordinary preflight
against the reviewed roster and current external runtime under the launch lock
before it spawns. A global mailbox is not implicit authority for a Git
worktree, and a reviewed plan is not a substitute for that live revalidation.

## Drafter readiness

The effective resolver chooses the complete profile block when present,
otherwise the complete global block, otherwise `in_session`; it never merges
fields between layers. A configured chain tries only its explicit order.

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
| `stopped` | the recorded process or pane is no longer live | rerun the same wizard invocation to reconcile it |
| `live/config-diverged` | the actor is live but current roster config differs | keep it live and route recovery to `amq-squad:cli` |
| drafter chain exhausted | every configured backend failed | inspect each recorded attempt; fix the first intended backend or complete the filled prompt in-session |
| generated prose invalid | output failed deterministic structure/content checks | do not write it; fix the backend/prompt inputs and generate a fresh preview |

After an interrupted approved launch, rerun the same wizard invocation. It
reuses the reviewed artifacts and lets start reconciliation keep verified live
actors while rolling the partial launch forward; it does not require manual
namespace cleanup.
