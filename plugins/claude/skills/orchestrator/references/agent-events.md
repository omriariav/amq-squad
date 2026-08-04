# Status attention and progress contract

## `status` is the operator projection

```
amq-squad status --session S --json
```

`status` projects live launch records, task state, inbox state, and open or aged
operator gates without becoming mutation authority. Read the complete JSON
snapshot rather than reconstructing state from pane titles or message prose.

## Progress travels over durable AMQ

Workers push ACK, meaningful progress, blockers, review requests, and DONE on the
durable task thread. Pane delivery is wake/fallback only. A fresh status report
with matching profile/session/task/actor context is evidence that work is moving;
a claimed task without current process or message evidence is only fallback
context, not proof that the worker is healthy.

## Park instead of polling

After one status read, act or park/end the turn. Do not create a hand-written
sleep loop or block the visible lead pane waiting for change. `start` establishes
a namespace-scoped notifier that nudges the recorded pane for pending AMQ work,
including messages that arrived while the notifier was absent. `doctor` diagnoses
that notifier contract.

## Operator attention remains non-authorizing

Open and aged gates surface through `status`; configured operator watcher plumbing
may emit attention outside the model loop. Those signals never approve, answer,
clear, or poll a gate. Only the matching durable operator answer can resolve a
human decision.
