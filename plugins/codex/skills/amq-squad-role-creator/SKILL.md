---
name: amq-squad-role-creator
description: Author custom amq-squad roles that are not in the built-in persona catalog, then wire them into a team. Covers writing a role file (Markdown with optional YAML frontmatter, .yaml, or .json), the required binary, and adding the role via `team init`/`new team`/`new profile` with `--role-file` or an inline path in `--roles`, plus the inline `--roles X --binary X=<cli>` shortcut. Use when someone wants a role like researcher, sre, or data-scientist that the catalog does not ship. For built-in personas and general team design, use `amq-team-setup`; for live coordination use `amq-squad`.
---

# amq-squad-role-creator

Use this skill to add a **custom role** — one that is not in the built-in
persona catalog (`cpo, cto, senior-dev, fullstack, frontend-dev, backend-dev,
mobile-dev, junior-dev, qa, pm, designer, scribe`). Custom roles are first-class team
members: they appear in `team.json`, `team-rules.md`, the bootstrap prompt,
status/history, and launch/resume exactly like built-ins.

Requires amq-squad **v1.5.0+**. Check with `amq-squad version`.

There are two ways to add a custom role. Pick by how much role guidance you want.

## A. Inline (quick, minimal role.md)

For a role that only needs an id + CLI, with the generic custom-role fallback
text seeded into its `role.md`:

```sh
amq-squad new team --roles researcher --binary researcher=codex
amq-squad new team --roles researcher,reviewer --binary researcher=codex,reviewer=claude
amq-squad new profile discovery --roles researcher --binary researcher=codex
amq-squad team init --roles cto,researcher --binary researcher=codex
```

Rules:
- A `--roles`/`--personas` entry that is not a built-in persona is a custom role.
- Each custom role **must** be a valid slug (lowercase `a-z`, `0-9`, `-`, `_`)
  and **must** have an explicit `--binary <role>=<cli>` entry (there is no
  catalog default to fall back to). Built-in roles keep their catalog defaults
  unless overridden.
- Missing binary fails clearly:
  `custom role "researcher" requires --binary researcher=<cli>`.

## B. From a role file (rich, authored role.md)

When you want a real persona description, peers, and skills, author a role file
and pass it with `--role-file` (comma-separated) or inline in `--roles`:

```sh
amq-squad new team --role-file ./roles/researcher.md --roles cto
amq-squad team init --roles "cto,./roles/researcher.md"
amq-squad new profile discovery --role-file ./roles/researcher.md,./roles/sre.yaml
```

What happens:
- The role id is taken from the file (`id:` field, a `# Role: <name>` heading,
  or the filename).
- The binary comes from the file's `binary:` field; `--binary` overrides it.
  If neither is present the command fails with the same binary guidance as above.
- The authored document is staged at `.amq-squad/roles/<id>.md`. At launch,
  `up` / `agent up` seeds that agent's `role.md` from it verbatim (and never
  overwrites later user edits).
- A role file named via `--role-file` is added to the team even if it is not
  also listed in `--roles`.

### File formats

**Markdown with YAML frontmatter** (recommended — frontmatter for metadata,
body becomes `role.md` verbatim):

```markdown
---
id: researcher
label: Research Engineer
binary: codex
peers: [cto, qa]
skills:
  - /deep-research
---
# Role: Research Engineer

## Description
Owns deep technical investigation, prototypes, and written findings. Turns
open questions into evidence the team can act on.

## Peers
- cto
- qa

## Skills
- /deep-research

## System Prompt
Stay within the research scope; hand implementation to developer roles. Use the
amq-squad protocol for handoffs.

## Priming Template
At launch, state your role and handle, summarize relevant context, then wait
for instruction.
```

**Plain Markdown, no frontmatter** (whole file is `role.md`; id from the
`# Role:` heading or filename; supply the binary with `--binary`):

```markdown
# Role: Archivist

## Description
Captures decisions and keeps the team's written record.
```

```sh
amq-squad new team --roles "cto,./roles/archivist.md" --binary archivist=claude
```

**Metadata-only YAML or JSON** (no body; `role.md` is rendered from the
fields):

```yaml
id: sre
label: Site Reliability Engineer
binary: claude
description: Owns reliability, on-call, and incident response.
skills:
  - /run
peers:
  - cto
  - backend-dev
```

```json
{ "id": "analyst", "label": "Analyst", "binary": "codex",
  "description": "Owns reporting and data pulls.", "peers": ["pm"] }
```

### Supported metadata fields

| Field | Meaning |
| --- | --- |
| `id` (or `role`) | role slug + default handle (lowercase `a-z 0-9 - _`) |
| `label` | human title shown in listings and `role.md` |
| `binary` | `codex` or `claude` (any non-control value is accepted) |
| `description` | one-line summary seeded into the rendered `role.md` |
| `skills` | list of slash commands for this role |
| `peers` | list of role ids this role talks to most |
| `body` (JSON only) | optional verbatim `role.md` content |

## Authoring workflow

1. Decide the role id, CLI (`codex`/`claude`), and whether you need rich
   guidance (file) or just an id (inline).
2. For a file: write it under `./roles/<id>.md` (frontmatter + body is the
   sweet spot). Keep the `# Role:`, `## Description`, `## Peers`, `## Skills`,
   `## System Prompt`, `## Priming Template` sections so it matches what the
   binary renders for built-ins.
3. Preview before writing anything:
   ```sh
   amq-squad new team --role-file ./roles/<id>.md --roles cto --dry-run --json
   ```
   Confirm the member shows the right `role`, `handle`, and `binary`.
4. Create the team for real (drop `--dry-run`), or add `--sync` to also write
   the `CLAUDE.md`/`AGENTS.md` pointer stubs.
5. Edit `.amq-squad/roles/<id>.md` later to refine the persona; re-running
   `team init --force` re-stages it, and launch never clobbers an agent's
   already-seeded `role.md`.

## Verification

- `amq-squad new team --role-file ... --dry-run --json` succeeds and the plan
  lists the custom member with the expected binary.
- After a live create, `.amq-squad/roles/<id>.md` exists and `team.json`
  includes the member.
- `amq-squad up --dry-run` shows a launch command for the custom role.

## When NOT to use this skill

- Built-in personas or general team design → `amq-team-setup`.
- Live coordination (drain, route, review, up/stop/resume) → `amq-squad`.
- Raw AMQ debugging → `amq-cli`.
