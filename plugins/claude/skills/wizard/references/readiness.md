# Launch diagnostics and preflight

Simple Mode has no readiness stage or readiness manifest. `start` performs
ordinary preflight against the authoritative roster and current external
runtime before it asks for approval and again under the launch lock before it
spawns.

## What preflight checks

- the project, profile, session, and canonical AMQ root resolve unambiguously;
- each configured binary exists and accepts its selected model/trust options;
- mutation-capable actors satisfy the worktree-isolation rule;
- launch records are valid and do not describe multiple live matches;
- a launcher-stamped unmanaged pane will not be duplicated;
- the tmux backend and target are available.

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

After an interrupted launch, rerun `start`. It keeps verified live actors and
rolls the partial launch forward; it does not require manual namespace cleanup.
