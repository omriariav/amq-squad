# ADR: Verification preflights for actions, merges, and releases

Status: Approved incrementally for v2.3.0, v2.14.0, and v2.18.0

Issues: #164 (`verify merge`), #285 (`verify release`), #349 (`verify action`)

## Context

The orchestrator skill already says that child reports are data, not authority,
and that merge or other irreversible actions are lead-only. Issue #164 asks for
a concrete verification gate before merge without coupling amq-squad to GitHub,
one CI vendor, `gh pr merge`, or a `~/.claude` workflow.

The verification surface now has three complementary preflights:

- `verify action` resolves a bound operator gate for one high-risk action and
  exact target;
- `verify merge` validates normalized CI and review evidence at one exact
  change head;
- `verify release` validates the final assembled release commit, its CI,
  second-actor developer co-sign, and operator release approval.

The merge gate has two parts:

- a deterministic preflight that can answer whether CI is green on the current
  head SHA and whether the review surface is clean;
- lead judgment about whether the verified artifacts are sufficient to merge,
  plus when an operator gate is required.

## Merge preflight: `verify merge`

The tool-agnostic `amq-squad verify merge` preflight validates normalized
evidence supplied by the lead. It does not query a provider or perform the
merge.

The verb takes an evidence document, for example by `--evidence <file>` or
stdin, and returns a deterministic result:

```json
{
  "subject": "pr or change identifier",
  "head_sha": "current change head SHA",
  "ci": {
    "state": "success",
    "sha": "SHA that CI evaluated",
    "source": "provider/tool name",
    "checked_at": "RFC3339 timestamp",
    "url": "optional evidence URL"
  },
  "review": {
    "state": "clean",
    "sha": "SHA that review state covers",
    "source": "provider/tool name",
    "checked_at": "RFC3339 timestamp",
    "url": "optional evidence URL"
  },
  "exceptions": []
}
```

The pass criteria are intentionally small:

- `head_sha` is present and exact;
- CI state is `success` for exactly `head_sha`;
- review state is `clean` for exactly `head_sha`;
- there are no unapproved or unnamed exceptions.

Any stale SHA, missing field, pending/failing CI, dirty review surface, or
unapproved exception fails the preflight with a machine-readable reason. The
verb may print human-readable text, but JSON output must identify the failed
condition so a lead can report the blocker without interpretation.

The verb does not:

- call GitHub, GitLab, Gerrit, Buildkite, Jenkins, or any other provider;
- infer the current PR or change from local repository state;
- merge, approve, label, close, push, or mutate remote state;
- read agent-specific config such as `~/.claude`.

## Action gate: `verify action`

High-risk actions use a stable operator gate bound to both an action kind and
an exact target:

```sh
amq-squad verify action \
  --project <repo> --profile <profile> --session <session> \
  --gate <topic> --action github_release \
  --target "draft v2.18.0 release for owner/repo" --json
```

Both the gate question and the configured operator's later answer must contain
matching `Action:` and `Target:` fields. Supported action classes include
default/protected branch pushes, tag operations, GitHub releases, and external
sends. A matching `APPROVED:` answer passes; pending, denied, missing, and
unbound or mismatched answers remain distinct non-zero outcomes.

This is a **callable verification boundary, not command interception**. A lead
or wrapper that never invokes `verify action` is not blocked by the operating
system, shell, Git, GitHub CLI, or another provider client. The guard protects
the normal amq-squad workflow from confused or overreaching execution; it is
not a tamper-proof security boundary against an actor that can bypass the CLI or
forge local AMQ mailbox data. Any wrapper that performs a high-risk operation
must call the preflight before executing that operation.

`verify action` validates the durable gate state only. It never pushes, tags,
publishes a release, sends externally, or otherwise performs the requested
action.

## Release preflight: `verify release`

Release publication has a second, non-substitutable evidence gate at the final
assembled release commit:

```sh
amq-squad verify release --evidence release-evidence.json --json
```

The normalized evidence shape is:

```json
{
  "subject": "release v2.18.0",
  "version": "v2.18.0",
  "release_commit_sha": "<final assembled release commit SHA>",
  "ci": {
    "state": "success",
    "sha": "<release_commit_sha>",
    "source": "provider/tool",
    "checked_at": "RFC3339 timestamp"
  },
  "cosign": {
    "state": "approved",
    "sha": "<release_commit_sha>",
    "reviewer": "<developer other than release lead>",
    "distinct_from_release_lead": true,
    "source": "provider/tool",
    "checked_at": "RFC3339 timestamp"
  },
  "operator_release_approval": {
    "approved": true,
    "gate": "gate/<topic>",
    "source": "operator gate evidence",
    "checked_at": "RFC3339 timestamp"
  }
}
```

Publish-ready requires all of the following:

- `release_commit_sha` is the final assembled release commit;
- CI is successful for exactly that SHA;
- a developer distinct from the release lead co-signs exactly that SHA;
- operator release approval is present with a gate or source reference.

A co-sign on an earlier per-delta SHA is stale. Operator approval alone never
substitutes for the exact-SHA co-sign, and the co-sign never substitutes for the
operator gate. Like the other preflights, `verify release` validates normalized
evidence only; it never pushes, tags, or publishes.

## Tool-agnostic surface

The merge and release preflights learn about CI, review, co-sign, and approval
state only through normalized evidence. A provider-specific tool, connected MCP
app, local script, or human lead can collect the facts and emit the evidence
schema. amq-squad owns validation of the facts, not collection of the facts.

This keeps the binary neutral while still removing improvisation from the gate:
different providers can feed the same schema, and the preflight checks the
provider-independent invariants that matter before merge.

Provider adapters can be added later as optional producers of the evidence
schemas, but they must remain outside the core decision path. The stable
contracts are the evidence schemas and verification results, not any one
provider command.

## Judgment that stays in the skill

The orchestrator skill should continue to say:

- merge and other irreversible actions are lead-only;
- a child report is a hypothesis until the lead verifies the artifacts;
- the lead verifies the actual diff, test output, CI result on the current head
  SHA, and review state, not the child's narration;
- `amq-squad verify merge` is a required deterministic preflight before merge,
  but a passing preflight is not an obligation to merge;
- `amq-squad verify release` is required on the final assembled release commit
  before publish, but it never performs the publish;
- named exceptions, such as sign-off pending, shared infrastructure risk, or
  autonomous wake risk, require an explicit operator gate on a stable
  `gate/<topic>` thread before the lead may proceed.

The skill should not contain provider-specific merge commands. It should point
the lead to gather evidence with the appropriate connected tool, run the
preflight, then apply judgment and route any exception through the operator
gate.

## Rejected options

### Documentation-only recipe

A documented recipe is provider-neutral, but it leaves the deterministic half as
prose. Leads would still have to remember and manually apply stale-SHA and clean
review checks. That is exactly the improvisation #164 is meant to remove.

### GitHub-coupled binary command

A command that shells out to `gh`, calls GitHub APIs directly, or performs
`gh pr merge` would violate the issue constraint. amq-squad should not own the
provider query or the merge action.

### Provider plugin system in v2.3.0

A full provider adapter interface is more surface area than #164 needs. The
v2.3.0 contract should be the normalized evidence schema plus deterministic
validation. Optional collectors can come later.

## Consequences

- v2.3.0 implementation work is split cleanly: add the evidence-validating
  preflight verb, then update both orchestrator skill mirrors with the judgment
  prose and the instruction to run the preflight.
- The gate is useful for GitHub today but not GitHub-shaped.
- A lead can cite an objective preflight result in AMQ review threads.
- The human/operator still owns merge permission when team rules or exceptions
  require it.
- Action verification remains explicit and callable rather than pretending the
  binary intercepts arbitrary shell, Git, provider, or messaging commands.
- Release evidence cannot collapse operator approval and a second-actor
  exact-SHA co-sign into one substitutable signal.
