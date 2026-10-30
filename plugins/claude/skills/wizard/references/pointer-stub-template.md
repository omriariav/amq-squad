# Pointer Stub Template

`amq-squad team apply` writes a small managed block into `CLAUDE.md` and `AGENTS.md`
at the repository root and in each existing seat directory inside the repository.
`amq-squad team propose` lists those files with their action and sha256 before
anything is written. This file shows the block's shape so reviewers know what to
expect. Do not hand-author the block; the digest flow owns it.

## Markers

The managed block is delimited by exact-match markers:

```
<!-- amq-squad:managed:begin -->
... managed content ...
<!-- amq-squad:managed:end -->
```

The digest flow only touches content between these markers. Any free-form prose
outside the markers is preserved verbatim.

## What goes inside

The managed block points at the durable context layers; it does not duplicate them.

```markdown
<!-- amq-squad:managed:begin -->
This project uses amq-squad for agent team coordination.

- **Team norms:** `.amq-squad/team-rules.md`
- **Your role:** when launched via amq-squad, `<your-agent-dir>/role.md` carries your persona.
- **Active brief:** read `.amq-squad/briefs/<session>.md` for the current workstream (bootstrap names the exact path).

These files are the source of truth. Do not duplicate their content here.
<!-- amq-squad:managed:end -->
```

The exact wording is the binary's responsibility and may evolve. Compare against the
files `team apply` wrote rather than this copy.

## Rules

- The managed blocks are protected paths: guard denies a seat's write to them, and a
  seat diff touching them is refused by the commit step and raises the growth gate.
- If `CLAUDE.md` or `AGENTS.md` is missing, `team apply` creates it with the managed
  block as the only content.
- If a pointer stub changed since the proposal, `team apply` refuses and writes
  nothing; run `team propose` again.
