# AMQ 0.51.x support and leverage assessment

AMQ 0.51.x is the supported series for amq-squad. The minimum supported
release is 0.51.1. Both real-AMQ CI matrices run pinned `v0.51.1` and `latest`;
`latest` remains a forward-compatibility canary and is not a support claim.
Every older AMQ release is rejected by `doctor` and all managed launch paths.

The 0.51.1 floor makes the old capability branches dead. amq-squad now always
uses canonical exact-root authority, `--require-wake`, explicit wake injection
mode, `--no-gitignore`, and `--baseline-existing` where those options apply.
There is no fallback to session shorthand or a pre-canonical-root container.

## 0.49.11 through 0.51.1 integration review

| Upstream change | amq-squad disposition |
| --- | --- |
| Wake retry, attention generation fencing, recovery, and torn-lock-read retries | Relied on as upstream wake guarantees. The pinned real-PTY lane exercises managed start, delivery, stop, resume, and cleanup against 0.51.1. No downstream retry loop is added. |
| Routed and ambiguous self-delivery refusal | Compatible with the existing explicit sender/recipient routing contract. The real-AMQ queue lane covers round trips and durable routing; amq-squad does not bypass the refusal. |
| Verified sessionless root pins | Adopted structurally. Supported launches always use the exact resolved root and materialize its authority config; pre-0.49.8 root/session shims are removed. |
| Versioned `amq wake check` schema and `agent_safe` / `operator_only` / `unavailable` classification | Evaluated but not adopted as a restart actuator. `wake check` is read-only, upstream agent-safe restart remains open, and 0.51.1 advertises `reload` without implementing a reload command. Existing amq-squad lifecycle code continues to use its bound launch record, wake PID, owner token, and verified teardown/restart path. |
| Self-resume advertisements and claims | Not substituted for amq-squad native goal supervision. The two protocols bind different durable evidence, and accepting an advertisement would not authorize pane input or a restart. |
| Attention retry decay | Consumed as upstream behavior. amq-squad retains its own bounded operator notification and goal-supervision policies rather than duplicating AMQ cadence logic. |

## Upgrade contract

Install AMQ 0.51.1 or newer and then stop and resume/relaunch agents so their
parent shells receive the complete identity tuple from the new AMQ process.
`amq-squad doctor` reports the resolved version and rejects an older,
unparseable, or missing version with an actionable `amq upgrade` remedy.
