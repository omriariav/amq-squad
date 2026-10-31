# squad.yaml, schema 1

`squad.yaml` describes one squad: its seats and the narrowing of the owner's
standing grant for this repository (plan 3.3, 3.4, 4.3, 7.2; briefs 2b-5 and
v3-5-migrate). The Go form is `internal/team` (`squad.go`, `squad_keys.go`,
`golden.go`): `ParseSquad`, `LoadSquad`, `Render`, `ExampleSquad`,
`SquadKeys`, `NeverSettableKeys`, `GoldenCheck`. `SquadKeys` is the one list: the parser's grant keys are built from it and a test proves the parser and the table agree both ways.

**Schema 1 is frozen for 3.0.** The key list below is complete; a 3.0.x
build adds no key to it, and a file of any other schema is refused
(`squad_yaml_schema_unsupported`, below). A v2 checkout becomes a squad.yaml
with `amq-squad migrate` (see "From v2").

## Location and writer

- One file per repository: `<repo root>/squad.yaml` (ruling E2). guard,
  context and the launch compiler read it there. The task store does not
  follow it; it lives under the AMQ root (2b-1).
- The lead writes it, a seat does not: `squad.yaml` is in the implicit
  protected set (`grants.Implicit`, R37), so guard denies a seat's write.
- The write goes through the digest flow (R8): `amq-squad team propose`
  prints one digest for every file it would create and writes nothing;
  `amq-squad team apply --digest <d>` writes them when the digest still
  matches. `amq-squad team show` prints the parsed file and its effective
  grant; `amq-squad team check` validates it.

## Who reads it

squad.yaml is a repository file, so a seat can put it in its diff. Three
things follow, and each is a rule with a test:

- **The reader is the lead's checkout.** The file is read at
  `project_path`, the lead's checkout, at squad load and at scheduler start
  (`grants.Load`, `team.LoadSquad`). A seat's worktree copy is never read:
  an edit a seat makes there changes nothing until the lead merges it, and
  the merge is a protected-path crossing.
- **A seat diff touching squad.yaml is a crossing.** The commit step
  (`internal/seatcommit`) checks every changed path of a seat's diff against
  the protected set, which holds `squad.yaml` by default
  (`grants.Implicit`). A diff that touches it is refused as `protected_path`
  and raises the protected-paths growth gate; only the owner's answer to that
  exact gate lets it through (plan 4.3, 7.2).
- **No key of it sets an owner value.** The never-settable keys below are
  refused at parse, so even an accepted edit cannot carry a push target, a
  signature mode, a verifier command or a pin.

## The digest

`team propose` plans, in this order, `squad.yaml`, `.amq-squad/team-rules.md`
(rendered from the squad), then `CLAUDE.md` and `AGENTS.md` for the
repository root and each existing seat directory inside the repository
(sorted). The digest is `sha256:` + hex(sha256(path NUL content NUL ...))
over those files. `team apply --digest D` recomputes the plan and writes
only when the digest is equal; the pointer stubs are refused if they
changed since the plan. The Go form is `team.NewPlan` and `Plan.Apply`
(`internal/team/digest.go`); the pi lead of 4-1 uses the same function.

## Changing it

A change lands through the digest flow above and no other way: `team
propose` shows it, the owner (manual) or the grant (auto) approves the
digest, `team apply --digest` writes it. The file plus that flow are the
final form. What a hand edit produces:

- The token `team propose` prints and `team apply --digest` takes is one
  string, `sha256:<content>@<disk>`: the digest of the files the flow would
  write, and the sha256 of the squad.yaml on disk when it was proposed
  (`absent` when there was none). `team apply` recomputes both, and the
  mutating step re-reads squad.yaml under the lock it writes with.
- Options changed after the proposal: the content part differs and the apply
  refuses with `squad_yaml_digest_mismatch` (it was the prose `digest
  mismatch` in 2b-5). Nothing is written.
- squad.yaml edited by hand after the proposal: the content part still
  matches, the disk part does not, and the apply refuses with
  `squad_yaml_changed`, naming the path and both disk digests. Nothing is
  written and the edit stays. The next `team propose` lists squad.yaml as
  `replace` and prints the new disk digest, so the edit is the diff the owner
  sees and decides on; approving that proposal replaces it. The same guard
  runs for every caller of `Plan.Apply` (the fleet adapter of 4-3 included).
  Between its read and its rename the apply also checks that the file is
  still the one it planned over (`<path> changed since the proposal`).
- Until an apply replaces it, an edit outside the flow is read as it stands at
  the next squad load: within the rules it takes effect.
- An edit that widens the owner grant, or sets a never-settable key, makes
  the file fail to load: `team check`, `team show` and the launch refuse it
  with the field named (`grants_invalid`, `team.WidenError`). guard reads the
  same keys leniently, ignores the widening field and names it in the
  resolved grant's `ignored` list, so a hand edit never widens what a seat
  may do.
- An edit by a seat is the protected-path crossing described under "Who
  reads it".

## Format

The strict YAML subset of `internal/yamlsub` (the same reader as the owner
file `~/.amq-squad/grants.yaml`): `#` comments, `key: value`, `key: [a, b]`,
and indented blocks of `- item`, `- key: value` maps, or `key: value` maps.
No tabs. An unknown key, a repeated key, a malformed line or a bad value is
a parse error, and so are anchors, aliases, tags and a second document.

## Keys

Every key of schema 1. The Relation column is the key's relation to the
owner grant: `plain` (describes the squad, touches no grant), `narrow` (can
only narrow the owner's value) or `add` (can only add). Keys of `defaults`
and of a seat are written `defaults.x` and `seats[].x`. `schema` is the
required version key (`schema: 1`); a file without it is refused.

| Key | Type | Default | Relation |
| --- | --- | --- | --- |
| `schema` | the integer 1 | none, required | plain |
| `session` | AMQ session name | none | plain |
| `lead` | a seat handle | none | plain |
| `lead_mode` | `builder` or `planner` | `builder` | plain |
| `execution_mode` | `global_orchestrator`, `project_lead`, `project_team` or `direct_lead_session` | none | plain |
| `control_root` | absolute path | none | plain |
| `target_project_root` | absolute path | none | plain |
| `shared_cwd_exception` | text, the reason two mutation-capable seats share a tree | none | plain |
| `operator_interaction` | `lead_pane`, `separate_terminal` or `noc` | none | plain |
| `defaults` | map of runtime, model, effort, tools | empty | plain |
| `seats` | list of seat maps, at least one | none, required | plain |
| `mode` | `auto` or `manual` | the owner's | narrow |
| `max_seats` | integer, at least 1 | the owner's | narrow |
| `cost_per_day_usd` | number | the owner's | narrow |
| `permitted_runtimes` | list of runtimes | the owner's | narrow |
| `privacy_class` | `vendor` or `local` | the owner's | narrow |
| `protected_paths` | list of paths | empty | add |
| `merge_repositories` | list of owner/name | the owner's merge grants | narrow |
| `merge_size_cap_lines` | integer, at least 1 | the owner's | narrow |
| `merge_size_cap_files` | integer, at least 1 | the owner's | narrow |
| `verifier_classes` | list of verifier classes | the owner's | add |
| `defaults.runtime` | `claude`, `codex`, `cursor-agent`, `grok` or `pi` | none | plain |
| `defaults.model` | token | none | plain |
| `defaults.effort` | token | none | plain |
| `defaults.tools` | `minimal`, `coding`, `browser`, `data` or `full` | `full` | plain |
| `seats[].handle` | `[a-z0-9][a-z0-9_-]*`, unique, not the operator's `user` | none, required | plain |
| `seats[].role` | catalog or staged custom role id | the handle | plain |
| `seats[].runtime` | `claude`, `codex`, `cursor-agent`, `grok` or `pi` | `defaults.runtime` | plain |
| `seats[].model` | token; a pi seat's is PROVIDER/ID | `defaults.model` (not for pi) | plain |
| `seats[].effort` | token; a cursor-agent seat takes none | `defaults.effort` (not for pi or cursor-agent) | plain |
| `seats[].worktree` | repo-relative or absolute path, never the repository root | `.worktrees/<handle>` | plain |
| `seats[].tools` | `minimal`, `coding`, `browser`, `data` or `full` | `defaults.tools` | plain |
| `seats[].actor_mode` | `implementation` or `review` | derived from the lead and lead mode | plain |

squad.yaml is the one source of a launch (brief 31-1): `amq-squad lead
--team` in the repository and `scheduler run --project` read it, with no
seats file beside it. The lead's handle is `lead` (required for a launch),
the AMQ session is `session` (required for a launch), and every seat but the
lead is in the roster. A seat's worktree is derived per handle under
`<repo>/.worktrees/<handle>` unless the seat names one; a worktree that
names the repository root is a parse error (the commit step moves a seat's
branch). The runtimes are the providers AMQ v0.116.2's launch adapters serve.

A seat's fields are the typed seat list of the launch compiler. Model and
effort travel as provider arguments: `--model` and the effort for Claude,
Codex and grok, `--model` alone for cursor-agent (its adapter maps no
effort, so a cursor-agent seat with an effort is a parse error). pi admits
no provider argument (plan 4.3, amendment 23): a pi seat's model is a
PROVIDER/ID that seat-exec hands the embedded runtime's launcher. A roster
seat's own pool needs no registry entry. The argument forms are the ones AMQ advertises
(`ProviderCapabilitiesV1.AllowedArgumentForms`, amendment 20 (b)); amq-squad
keeps no list of its own, and a form AMQ does not list is refused before the
launch. Quota and concurrency pools are registry entries (plan 5), not
squad.yaml keys: a squad narrows what it uses through `permitted_runtimes`
and `max_seats`.

### Never settable

These keys are the owner's alone. A squad.yaml that carries one, at any
value and in any spelling (flat, dotted, or as a block), is refused at parse
with the key named as written (a key nested under one by its full path, `merge.push_url`), as `grants_invalid` (`team.WidenError`). The
test `TestSquadYamlNeverSettableKeysAreRefusedWithTheKeyNamed` has one case
per key.

| Key | Where the owner sets it |
| --- | --- |
| `merge` | grants.yaml: the standing merge grants, with merge.push_url and merge.local_push_ack |
| `merge.push_url` | grants.yaml: the push target |
| `merge.local_push_ack` | grants.yaml: the owner's acknowledgement of a local push target |
| `push_url` | grants.yaml: the push target (merge.push_url) |
| `local_push_ack` | grants.yaml: merge.local_push_ack |
| `verifiers` | grants.yaml: the verifier command per class |
| `verifier_goproxy` | grants.yaml: the verifier's GOPROXY |
| `verifier_timeout` | grants.yaml: the verifier's wall time |
| `verifier_output_limit` | grants.yaml: the verifier's output bound |
| `verifier_parallelism` | grants.yaml: verifiers running at once |
| `default_branch` | grants.yaml: the default branch |
| `fleet` | grants.yaml: the operator session's fleet grant |
| `max_squads` | grants.yaml: fleet.max_squads |
| `permitted_repositories` | grants.yaml: fleet.permitted_repositories |
| `pins` | registry.yaml: the owner-pinned harness tools |
| `registry` | registry.yaml: the registry and its pins |
| `unprotected_paths` | a removal from the protected set; squad.yaml only adds |
| `remove_protected_paths` | a removal from the protected set; squad.yaml only adds |

Any other key that is not in the Keys table is an unknown key and a parse
error (`squad_yaml_invalid`).

### Schema and codes

- A squad.yaml of schema 2 (or any schema but 1) is refused by a 3.0.x
  reader as `squad_yaml_schema_unsupported`, naming the version, before any
  other key is read (`team.SchemaError`).
- A parse or validation error is `squad_yaml_invalid`; a widening or
  never-settable key is `grants_invalid` (`team.CodeOf`). `team apply` refuses
  with `squad_yaml_digest_mismatch` (the options changed) or `squad_yaml_changed`
  (the file changed), see "Changing it".
- `amq-squad doctor` embeds a golden squad.yaml (`internal/team/golden`) and
  the structure it must parse to; a build whose parse drifted from it fails
  the `squad.yaml golden` row with kind `squad_yaml_golden_drift`. The golden
  carries every key of the table.

## Grant narrowing

The owner grant lives only in `~/.amq-squad/grants.yaml` (R16), else the
built-in defaults; the R37 protected set lives in the grants defaults
(`grants.Implicit`, ruling 3) and squad.yaml only adds to it. squad.yaml can
narrow the grant and never widen it:

- `mode: manual` narrows `auto`; `mode: auto` under an owner `manual` widens
  (ruling 2). Mode precedence (R13): the owner file sets it globally,
  squad.yaml narrows it per repository, and `--auto` / `--manual` and the
  `/auto` / `/manual` commands are the owner's keystrokes.
- `max_seats` and `cost_per_day_usd` at or under the owner's; the number of
  seats at or under the effective `max_seats`.
- `permitted_runtimes` a subset of the owner's; every seat's runtime in the
  effective list. `privacy_class: local` narrows `vendor`.
- `protected_paths` adds paths. An entry starting with `!` or `-`, or a
  removal key such as `unprotected_paths`, widens.
- `verifier_classes` adds verifier classes to the gate: a list that leaves
  out a class the owner grant verifies widens and is refused.
- `default_branch` and `merge` are the owner's alone.

A widening field is a parse error that names the field (`team.WidenError`,
`errors.Is(err, team.ErrWidens)`). guard reads the same grant keys through
the same function (`grants.ParseSquadNarrowing`) but leniently: a widening
field there is ignored and named in the resolved grant's `ignored` list, so a
hand-edited squad.yaml can never widen what a seat may do.

## From v2

`amq-squad migrate <v2 profile path>` converts a v2 checkout once. It is a
dry run unless `--write` is given, writes squad.yaml only when absent, never
writes the owner files (`grants.yaml`, `registry.yaml`), and prints every v2
key it did not map. See `docs/v3-spike/5-migrate-execution.md` for the field
by field mapping.

- **One root descriptor.** The project root is resolved once (EvalSymlinks) and
  opened `O_DIRECTORY|O_NOFOLLOW`; the descriptor is kept from the plan to the
  apply. Every v2 file is read, every project file is created and every v2 file
  is removed relative to it, one `O_NOFOLLOW` component at a time, so a symlink
  on the way refuses at the read (`migrate_refused_symlink`), and retargeting
  the project symlink between plan and apply changes nothing. If the directory
  at the root path is no longer the one the descriptor holds, the apply
  refuses `root_changed`.
- **Refuses before it touches anything; stops at the first refusal after.**
  The plan's refusals say nothing was written or removed. There is no
  transaction: a change during `--write` or `--remove-v2` (a file that
  appeared, a symlink introduced, a root that moved) stops at the first refusal
  with a partial report of what was already done, which stays done, and exit 3
  (as uninstall).
- **Exit codes and what the CLI adds.** A plan refusal with nothing done is exit
  1; a system error (for example ENOSPC on the first write) is exit 2; a stop
  after something was done is exit 3 with a partial report that lists what was
  done and what was not. The CLI puts its constant `error: ` prefix in front of
  the fixed refusal line. A symlink that is the leaf (the file itself) is
  unlinked as a link and the run continues: it is never followed. A panic is one
  line, `migrate: internal error`, exit 2.
- **One output funnel.** Everything the verb prints, on stdout or as the error
  the CLI renders, passes `redact.Block` once at the verb boundary. If any of
  it would change, the verb prints `migrate_refused_credential: <class>` (a
  closed word: profile path, project path, a key under the profile, a value
  under the profile, a key under launch.local.json, an input file name, a
  removal path, the output), exits 2 and prints nothing else.

## Example

```yaml
# squad.yaml schema 1: one squad per repository, at the repository root.
# The lead proposes it as a digest (amq-squad team propose) and writes it
# after approval (amq-squad team apply); seats cannot write it.
schema: 1
session: feature-x            # the AMQ session name
lead: lead                    # a seat handle
lead_mode: planner            # builder | planner
execution_mode: project_team  # global_orchestrator | project_lead | project_team | direct_lead_session
# Grant narrowing: each key can only narrow ~/.amq-squad/grants.yaml.
mode: manual                  # auto -> manual narrows; manual -> auto is an error
max_seats: 4                  # at or under the owner cap
permitted_runtimes: [claude, codex, pi]
protected_paths:              # added to the owner's and the implicit set
  - docs/adr/
defaults:                     # filled into a seat that leaves the field empty
  runtime: claude
  effort: medium
seats:
  - handle: lead
    role: cto
    model: opus
    actor_mode: review
  - handle: dev
    role: senior-dev
    runtime: codex
    model: gpt-5.5
    worktree: ../feature-x-dev
    tools: coding
  - handle: scout
    role: researcher
    runtime: pi                # a pi model, when named, is PROVIDER/ID
```
