# Pointer Stub Template

`amq-squad team sync --apply` writes a small managed block into the project's `CLAUDE.md` and `AGENTS.md`. This file shows the exact shape so reviewers know what to expect. Do not hand-author the block; let `team sync` own it.

## Markers

The managed block is delimited by exact-match markers:

```
<!-- amq-squad:managed:begin -->
... managed content ...
<!-- amq-squad:managed:end -->
```

`team sync` only touches content between these markers. Any free-form prose outside the markers is preserved verbatim.

## What goes inside

The managed block points at the three durable context layers; it does not duplicate them.

```markdown
<!-- amq-squad:managed:begin -->
This project uses amq-squad for agent team coordination.

- **Team norms:** `.amq-squad/team-rules.md`
- **Your role:** when launched via amq-squad, `<your-agent-dir>/role.md` carries your persona.
- **Active brief:** read `.amq-squad/briefs/<session>.md` for the current workstream (bootstrap names the exact path).

These files are the source of truth. Do not duplicate their content here.
<!-- amq-squad:managed:end -->
```

The exact wording is `amq-squad team sync`'s responsibility and may evolve. Reviewers should compare against `team sync` output (`amq-squad team sync` previews drift; exit 1 means drift exists).

## Rules

- Hand-editing inside the markers is unsupported. `team sync --apply` will overwrite it.
- If a project's `CLAUDE.md` / `AGENTS.md` is missing, `team sync --apply` creates it with the managed block as the only content.
- If the markers are missing but the file exists, `team sync --apply` appends a managed block at the end of the file.
- Removing the managed block manually is fine; the next `team sync --apply` will re-add it.

## Drift detection

`amq-squad team sync` (no flag) is a read-only preview:

- Exit 0: no drift; files match the desired stub.
- Exit 1: drift; the printed diff shows what would change.

CI can run `amq-squad team sync` to enforce that the managed block stays in sync without writing.
