# What `monitor` surfaces, and the activity contract

## `monitor` is read-only by design

It polls the task store, the evidence directory, open operator gates, and the
operator inbox, and surfaces structured operator-needed events. It never answers
or clears gates, marks messages read, mutates tasks, dispatches, wakes panes,
pushes, merges, tags, or releases.

That guarantee is why it is safe to run unattended: the worst case is a stale
read, never a state change.

## Bounds are mandatory

```
amq-squad monitor --session S --once --json
amq-squad monitor --session S --interval 30s --timeout 30m
amq-squad monitor --session S --interval 30s --max-ticks 20
```

`--once` runs a single tick. In loop mode pass `--timeout`, `--max-ticks`, or
both. There is no unbounded mode, and asking for one is the hand-rolled loop this
command replaces.

## Reading the result correctly

Exit status is 0 whether an event fired or the run idled out cleanly; only errors
exit non-zero. **Distinguish the two by reading the output**, not the exit code:
`events_found` and the `Events` section carry the answer.

Consume the final snapshot rather than stream order. Stream order is not a
contract; the final snapshot is.

## Activity heartbeats

Workers set activity on claim, on meaningful phase changes, and before
long-running tests:

```
amq-squad activity set --session S --me HANDLE --task ID --phase PHASE
```

Fresh heartbeat activity is a **busy** signal. Task-store ownership alone is only
fallback context, so read activity before concluding an agent has stalled.

Suppression is deliberately narrow: only a heartbeat from the admitted
profile/session that matches the canonical task and assignee exactly, and only
within the bounded phase catalog. Coding and testing windows get explicitly
longer allowances. A malformed, mismatched, stale, or future-skewed claim never
suppresses escalation.

## Zero-model observation

```
amq-squad monitor --session S --once --json
amq-squad notify --session S --deliver
```

Driven by a timer or a Stop hook, this reaches the human with no model turn.
`notify` prints only new or stale-threshold items and records what it showed in
its state file, so repeated runs do not re-alarm. It is an attention primitive
only: it does not approve, answer, clear, or poll.
