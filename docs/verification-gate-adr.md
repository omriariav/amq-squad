# ADR: Verification-before-merge gate

Status: Approved for v2.3.0

Issue: #164

## Context

The orchestrator skill already says that child reports are data, not authority,
and that merge or other irreversible actions are lead-only. Issue #164 asks for
a concrete verification gate before merge without coupling amq-squad to GitHub,
one CI vendor, `gh pr merge`, or a `~/.claude` workflow.

The gate has two parts:

- a deterministic preflight that can answer whether CI is green on the current
  head SHA and whether the review surface is clean;
- lead judgment about whether the verified artifacts are sufficient to merge,
  plus when an operator gate is required.

## Decision

Ship a tool-agnostic binary preflight, tentatively `amq-squad verify merge`,
that validates normalized evidence supplied by the lead. Do not ship a
provider-specific query or merge action.

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

## Tool-agnostic Surface

The preflight learns about CI and review state only through normalized evidence.
A provider-specific tool, connected MCP app, local script, or human lead can
collect the facts and emit the evidence schema. amq-squad owns validation of the
facts, not collection of the facts.

This keeps the binary neutral while still removing improvisation from the gate:
different providers can feed the same schema, and the preflight checks the
provider-independent invariants that matter before merge.

Provider adapters can be added later as optional producers of the evidence
schema, but they must remain outside the core merge decision path. The stable
contract is the evidence schema and the `verify merge` result, not any one
provider command.

## Judgment That Stays in the Skill

The orchestrator skill should continue to say:

- merge and other irreversible actions are lead-only;
- a child report is a hypothesis until the lead verifies the artifacts;
- the lead verifies the actual diff, test output, CI result on the current head
  SHA, and review state, not the child's narration;
- `amq-squad verify merge` is a required deterministic preflight before merge,
  but a passing preflight is not an obligation to merge;
- named exceptions, such as sign-off pending, shared infrastructure risk, or
  autonomous wake risk, require an explicit operator gate on a stable
  `gate/<topic>` thread before the lead may proceed.

The skill should not contain provider-specific merge commands. It should point
the lead to gather evidence with the appropriate connected tool, run the
preflight, then apply judgment and route any exception through the operator
gate.

## Rejected Options

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
