<!-- amq-squad:managed:begin -->
<!-- Managed by amq-squad. Edit .amq-squad/team-rules.md and run
     `amq-squad team sync --apply` to refresh. -->

# Team Rules

Shared norms for the amq-squad project team. Every agent reads this file
via their priming prompt regardless of binary. Edit this file and run
`amq-squad team sync --apply` to refresh CLAUDE.md and AGENTS.md.

Team members (see .amq-squad/team.json):
- cto (codex): owns technical direction, architecture, sign-off.
- fullstack (claude): implements features end to end.

## Workflow

- All code changes ship via a pull request. No direct pushes to main.
- Open PRs with `gh pr create` from a topic branch. Use a HEREDOC for the
  body per the repo's existing conventions.
- Do not commit until the user explicitly asks. Do not push without permission.
- Prefer creating new commits over amending.

## Approvals

- Every PR requires review and approval from cto before merge.
- Cto review focuses on: architectural shape, Go idioms, test coverage,
  whether the change respects the project's non-negotiables (zero required
  AMQ changes, stdlib only, launch.json as the durable source of truth).
- Disagreement is surfaced on the PR thread (p2p/cto__fullstack) rather
  than blocking silently.

## Quality gates

- `make ci` (vet + tests) must pass before requesting review.
- `gofmt -l .` must be clean.
- New packages ship with tests. Round-trip code (serialize/deserialize,
  plan/apply) gets explicit coverage of both directions.

## Communication

- 1-on-1 threads: p2p/cto__fullstack.
- Decisions that change the shape of the system go in a
  decision/<topic> thread (see AMQ decision protocol).
- Fullstack pings cto early when a design decision feels bigger than the
  PR it sits in. Escalate the shape before writing a lot of code.
- Keep messages focused. One concern per message when possible.

## Style

- No em dashes in any written output or source.
- Default to writing no comments; only justify the WHY when non-obvious.
- No backward-compat shims or dead feature flags. Delete unused code.

<!-- amq-squad:managed:end -->
