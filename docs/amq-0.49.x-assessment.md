# AMQ 0.49.x support and leverage assessment

AMQ 0.49.x is the supported series for amq-squad. The minimum supported release
is 0.49.0 and compatibility is tested through 0.49.1. Both real-AMQ CI matrices
run pinned `v0.49.0`, pinned `v0.49.1`, and `latest`; `latest` remains a
forward-compatibility canary. Older AMQ versions fail doctor diagnostics and
the child `agent up` gate before launch-record or process side effects. Parent
team launch, `resume --exec`, and `up --reset` also validate every selected
member's resolved AMQ version before creating roots, briefs, watchers, or
panes, and before deleting an existing session.

Historical capability boundaries remain named in code for archaeology:

| Capability | First AMQ release |
| --- | --- |
| Complete injected identity tuple | 0.42.1 |
| Reply refs | 0.43.1 |
| Exact wake retirement | 0.45.0 |
| Owner-bound coop wake lifecycle | 0.47.0 |
| Fixed shell-inert `coop exec` doorbell | 0.47.1 |
| Configured-mailbox repair | 0.48.0 |
| Fixed shell-inert standalone/default wake doorbell | 0.49.1 |

They are not compatibility lanes or reasons to preserve below-floor branches.

## Message trace

AMQ 0.49.0 adds `amq trace <message-id> --root <path> --json`, a read-only join
over evidence already present in one queue root. It can correlate current
message copies, persisted addressing, DLQ entries, receipts, and thread refs.

Decision: use trace as an operator diagnostic, but do not consume it as
authoritative input to amq-squad's immutable command-evidence, delivery-receipt,
claim-once, retry, or gate decisions.

The upstream `amq/trace/v1` contract explicitly does not prove the original
directory-sync result from current file presence and reports notification
history as `no_evidence` until a durable notification-attempt ledger exists.
Those limits match amq-squad's committed-indeterminate and do-not-resend
boundaries. Treating trace as stronger authority would weaken them.

No local follow-up is filed for Phase A. Re-evaluate integration only when
upstream trace gains durable notification-attempt evidence or another new
artifact that can strengthen, rather than project, amq-squad's existing
receipts.

## Doctor discovered-mailbox remedies

AMQ 0.49.0 adds actionable remedy text for discovered-only malformed mailboxes:
register a valid handle and run explicit repair, or preserve messages and remove
an abandoned mailbox. Invalid entries receive a preserve-then-rename/remove
remedy.

Decision: keep ownership in upstream AMQ. `amq-squad doctor` continues to run
the read-only `amq doctor --ops --json` health check and does not duplicate
mailbox-inventory parsing or invoke `--fix-mailboxes`. Operators use `amq doctor
--json` for canonical remedy text and opt into `amq doctor --fix-mailboxes
--json` themselves. This preserves the v0.48 contract that repair is explicit,
non-destructive, and fail-closed.

No follow-up is required: the missing operator guidance was fixed upstream and
the amq-squad README now routes directly to it.

## Wake delivery and retirement

The pre-0.47 fixture workaround that ignored `wake retire` refusal is removed.
Supported AMQ `coop exec --wake-inject-via` helpers are owner-bound: the coop
member now releases that exact wake through `wake recover-owner`, authenticated
by the inherited `AMQ_WAKE_OWNER` token and matching OS session. The fixture
asserts prompt exit and absence of the claim. The ownerless `wake retire` path
correctly refuses this owner-bound state; when the owner is already dead,
`recover-owner` remains the explicit fail-closed recovery path and does not
require a live-owner token.

Production cleanup applies the same three-way boundary. A persisted inject-via
wake first receives structured `wake recover-owner --json` handling:
owner-bound claims recover exactly after the signaled owner exits, while the
exact structured ownerless refusal routes to `wake retire`. Unknown, corrupt,
or mismatched results fail closed and preserve evidence. Raw wakes or
historical records without inject-via identity retain the bounded exact-PID
signal path because AMQ cannot address those wakes by injector identity.

The fixed doorbell has two historical boundaries inside the supported series.
Managed `coop exec` wakes use it in both 0.49.0 and 0.49.1 because that contract
started in 0.47.1. Standalone/default wakes carry subject text in 0.49.0 and
switch to the fixed shell-inert doorbell in 0.49.1. The version-aware
compatibility fixture proves zero injection before the post-baseline resend and
exactly one version-correct injection afterward; the final drain proves both
messages remained durable.

AMQ 0.49.1 adds transparent hardening around that contract: held notifications
retry after transient foreground-process-group handoff, max-hold delivery
demotes to out-of-band output, quiet detection requires consecutive samples,
and periodic capability checks report only verified preconditions. None
requires an amq-squad production version branch.

The `latest` lanes in CI run `30248307062` resolved AMQ `v0.49.2` and passed
both the queue-compatibility and wake-compatibility suites, including exact
inject-via retirement and structured owner-bound cleanup. Upstream `v0.49.2`
changes `coop exec -y` startup reclamation and unverified/live raw-orphan
handling, not the structured `recover-owner` contract consumed here; the
`wake_owner_recover_unix.go` blob is identical to `v0.49.1`
(`fe6444fafa8a4e7ac3bec2962483923a7d7075e0`). This is forward-canary evidence,
not an expansion of the compatibility-tested-through boundary.

## Optional Stop-hook hardening

AMQ 0.49.1's optional Claude Stop hook blocks once per fresh message content,
persists a bounded atomic 0600 guard without draining mail, resolves root and
session through `amq env`, and recovers an incomplete ambient session pin.
Configured-context and mailbox failures emit a visible `systemMessage`.

amq-squad does not install or invoke this upstream hook; its `stop` and `resume`
verbs manage agent processes and launch records directly. The upstream change
is additive hardening for operators who separately enable the Claude hook and
does not add an amq-squad production branch.
