# Brief v3-5-migrate execution log: squad.yaml final form and `migrate`

Branch `v3-5-migrate` from `v3` at 6d5090b2 (the verifier producer merge,
38eafc83, plus the plan and spike log commit). Implementer: Sonnet 5.5.
Brief: `docs/v3-plan.md` section 16.4 (phase 5 additions, the migrate
paragraph), decision 6 (a converter, not a compatibility layer).

## Part A: the squad.yaml final form (schema 1 frozen for 3.0)

Built:
- `docs/squad-yaml.md` rewritten: the complete key table (key, type, default,
  relation to the owner grant), the never-settable table, "Who reads it",
  "Changing it", schema and codes, "From v2".
- `internal/team/squad_keys.go` (new): `SquadKeys` (the key list with each
  key's relation: plain, narrow, add), `NeverSettableKeys` / `IsNeverSettable`
  / `NeverSettableOwner`, the codes (`squad_yaml_invalid`, `grants_invalid`,
  `squad_yaml_schema_unsupported`, `squad_yaml_golden_drift`), `SchemaError`,
  `CodeOf`.
- `internal/team/squad.go`: a schema other than 1 is a `SchemaError` named
  before any other key is read; a never-settable key is a `WidenError` naming
  the key as written; `verifier_classes` is accepted (it was accepted by
  `grants.ParseSquadNarrowing` and guard but refused as an unknown key by the
  strict `ParseSquad`) and rendered, so the documented list and the parser
  agree.
- `internal/team/golden.go` and `golden/squad-v1.yaml`,
  `golden/squad-v1.expected.json` (new): the embedded golden and the
  structure it must parse to; `GoldenCheck` also requires every key of
  `SquadKeys` in the golden and a Render round trip.
- `internal/cli/doctor_squad.go` (new) and a row in `doctor_host.go`: the
  `squad.yaml golden` check; a drift is a failing row of kind
  `squad_yaml_golden_drift`.

The never-settable set as found (the keys only the owner's files set; each
refused at parse, flat, dotted or as a block): `merge`, `merge.push_url`,
`merge.local_push_ack`, `push_url`, `local_push_ack`, `clear`,
`clear.signature`, `verifiers`, `verifier_goproxy`, `verifier_timeout`,
`verifier_output_limit`, `verifier_parallelism`, `default_branch`, `fleet`,
`max_squads`, `permitted_repositories`, `pins`, `registry`,
`unprotected_paths`, `remove_protected_paths` (20 keys). It was found as:
the keys of `grants.ParseOwner` that `grants.ParseSquadNarrowing` does not
read (default_branch, merge, fleet, clear, verifiers, verifier_*), the
registry's owner data (pins), and the two removal keys 2b-5 already refused.
Before this brief only `merge`, `default_branch` and the two removal keys were
refused by name; the rest were "unknown key".

Rows (test, in `internal/team` unless noted):
- A1 `TestSquadYamlKeyListIsDocumentedKeyForKey` (docs table row per key with
  its relation, and per never-settable key);
  `TestSquadYamlSchemaIsRequiredAndAnUnknownKeyIsRefused`;
  `TestSquadYamlVerifierClassesOnlyAdd`.
- A2 `TestSquadYamlGoldenParsesToTheExpectedStructure`;
  `TestDoctorSquadYamlGoldenSelfCheck` (internal/cli).
- A3 `TestSquadYamlSchemaTwoIsRefusedAndNamed`.
- A4 `TestSquadYamlNeverSettableKeysAreRefusedWithTheKeyNamed` (one subtest per
  key, plus the block forms), `TestNeverSettableCoversTheKeysUnderOne`.
- A5 `TestSeatDiffTouchingSquadYamlRaisesTheGrowthGate`
  (internal/seatcommit). squad.yaml was already in `grants.Implicit` (R37);
  the row is new and the docs name the reader.
- A6 `TestSquadYamlHandEditIsNamedByTheDigestFlow` (internal/cli).

Finding on A6: 2b-5's refusal is the text `digest mismatch` (no code token).
The digest covers the content the flow would write, not the file on disk, so
a hand edit between propose and apply is not refused by the apply; it shows
as `replace` in the next proposal and the apply rewrites the file from its
options. The docs state exactly this. The only in-apply guard on squad.yaml is
the check, between the apply's read and its rename, that the file is still
the one it planned over. I did not change the digest flow (brief: do not
widen); if the lead wants a hand edit refused at apply, it is a 2b-5 follow-up.

## Part B: migrate

`amq-squad migrate [--write] <v2 profile path>` and `--remove-v2 [--write]`
(`internal/cli/migrate.go`, `internal/migrate`).

### The v2 loaders read from main (B5, B6)

main at 8fd51b93; the last commit touching each file in brackets. Field names
are copied from these, never guessed.

| File on main | Last commit | What migrate takes from it |
| --- | --- | --- |
| `internal/team/team.go` | c0a616f3 | `Team`, `Member`, `OperatorConfig`, `SelfOperatorPolicy`, `AutonomousPolicy`, `GoalSupervisionPolicy`, `LeadExecution` JSON tags; `ProfilePath` (`.amq-squad/team.json`, `.amq-squad/teams/<name>.json`); `EffectiveActorMode`; `Validate` (trust values, handle and role slugs) |
| `internal/cli/execution_mode.go` | cfa2c355 | the four execution modes |
| `internal/namespace/namespace.go` | e30b9ebb | `BriefPath` (`.amq-squad/briefs/[<profile>/]<session>.md`) |
| `internal/cli/briefs.go` | e30b9ebb | `briefsDirName` |
| `internal/rules/rules.go` | a5838218 | `Path` (`.amq-squad/team-rules.md`) |
| `internal/role/role.go` | 33d3f801 | `Path`, `LegacyPath` (role.md under `extensions/io.github.omriariav.amq-squad/` or directly in the agent dir) |
| `internal/cli/launch.go` | 22278eb0 | `seedRoleStub`: staged custom roles at `.amq-squad/roles/<id>.md` seed role.md |
| `internal/cli/cli.go` | 14dd95f7 | the agents directories at `<project>/.agent-mail/<session>/agents/<handle>` |
| `internal/userconfig/config.go` | ccede332 | read, not migrated: `~/.config/amq-squad/config.json` is user-level and kept by 3.0 |
| `internal/launch/record.go` | main | read, not migrated: `launch.json` is a runtime record in an AMQ mailbox, not project configuration (decision 6) |

`launch.local.json`: the brief names it, but no loader on main reads a file of
that name (`git grep -i 'launch.local'` on main finds nothing). Judgment call:
migrate reads `.amq-squad/launch.local.json` if it is present, only as raw JSON
for the credential scan, never maps a key from it (no field names to take), lists
the file as unmapped and as dead, and copies no value. Likewise
`launcher_preference` is not a field on main; migrate refuses any v2 key of that
name (a safety key by name) rather than guess where it lives.

### Mapping

Profile keys (JSON field names of `Team`), with where each goes:
- `workstream` or the one session every member shares -> `session`;
  `lead_mode`, `execution_mode` (one of the four), `control_root`,
  `target_project_root` (relative paths joined to the project),
  `shared_cwd_exception` -> the same squad.yaml keys; `lead` (a role) -> `lead`
  (the handle of the seat with that role); `operator.interaction_mode`
  (`lead_pane`, `separate_terminal`, `noc`) -> `operator_interaction`;
  `schema` and `orchestrated` are consumed.
- `members[]`: `role`, `binary` (runtime; claude or codex), `handle`
  (default role), `model`, `cwd` (-> `worktree`, repo-relative inside the
  project), `actor_mode` (else v2's `EffectiveActorMode`: a planner lead reviews),
  `tool_profile` (the five standard names; `custom` is reported).
- Unmapped (named in the report, by key path or by file): `trust` when
  `sandboxed`, `binary_args`, `drafter`, `composition`, `autonomous.*` except
  `max_active_agents`, `goal_supervision`, `target_contract`, `lead_execution`,
  `max_spawn_depth`, `created_at`, `operator.{enabled,handle,kind,participant,
  runnable,assignable,wake_supported,poll_required,notifications}`,
  `members[h].{session when it differs,tool_config,tool_mcp_config,
  tool_blocklist,tool_policy_sources,tool_policy_drift,tool_disable_all_hooks,
  spawn_origin,spawn_depth,claude_args,codex_args}`, any key main's loader does
  not know, and by file: `team-rules.md`, `launch.local.json`, a catalog role's
  role.md or one that differs from its staged prompt, and the briefs of a named
  profile. A field that is empty (null, "", false, 0, [], {}) is not set and is
  not reported.
- Kept (3.0 reads them at the same path): the default profile's briefs and
  `.amq-squad/roles/*.md`. Dead under decision 6 (left in place): the profile,
  `launch.local.json`, and the agents' role.md files. `team-rules.md` is neither:
  `team propose` regenerates it at the same path.
- Role prompts: a custom role's role.md becomes `.amq-squad/roles/<id>.md` when
  none is staged; migrate prints `amq-squad trust <project>` and runs nothing.
- `grants.yaml.proposed` (0600, beside the owner's grants.yaml): `permitted_runtimes`
  from the seats, `max_seats` from `autonomous.max_active_agents` when it is
  within the default cap of 12. It parses as an owner file (checked at plan time).

Refusals (`migrate_refused_safety_default`, naming the outermost field path):
a launcher or `launcher_args` on a member; any set key whose name contains
`allow` (not `disallow`, `deny`, `block`), at any depth, including
`tool_allowlist`, `permission_allowlist` and `autonomous.allowed_roles`;
`launcher_preference`; `trust` of `trusted` or `approve-for-me`;
`operator.self_operator` or the `self_operator` interaction mode;
`goal_supervision.mode` of `safe-auto`; a `claude_args`, `codex_args` or
`binary_args` argument containing one of `dangerous`, `bypass`,
`danger-full-access`, `yolo`, `full-auto`, `allowedtools`, `allowed-tools`,
`permission-mode` (a closed list); `autonomous.max_active_agents` above 12.
There is no override flag; the remedy printed is to remove the field from the
v2 profile or set the authority in grants.yaml by hand. Other refusals:
`squad_yaml_exists`, `migrate_proposed_exists`, `migrate_refused_credential`,
`migrate_input_invalid`, `migrate_squad_invalid` (the output does not load
against the owner's grant), `migrate_remove_needs_squad`.

Writes are create-only: temp file beside the target, mode set explicitly,
hard-linked into place (fails if the target exists); a failure removes what the
call created. A dry run reads only.

### Judgment calls

1. The key is `schema`, not `schema_version`: 2b-5 and the owner file use
   `schema`, and schema 1 is frozen; a rename would break existing files. The
   brief's "schema_version required" is this key.
2. `launch.local.json` and `launcher_preference`: not on main, see above.
3. `tool_allowlist` is an allow list, so any v2 member with a non-empty one is
   refused even when it is the materialization of a standard tool profile. The
   brief says "any allow list".
4. Native CLI arguments (`claude_args`, `codex_args`, `binary_args`) are reported
   as unmapped, and refused only when they match the closed widening list above.
5. `max_seats` in the proposal comes only from `autonomous.max_active_agents`;
   no merge or protected-path proposal is invented.
6. `--remove-v2` also refuses a squad.yaml that does not load (the brief says
   absent); it never removes `team-rules.md`, the briefs or the staged roles.
7. Pools: the brief lists pools among narrowing keys; schema 1 has no pool key
   (pools are registry entries), so the docs say so.
8. The verb is registered as `migrate` in the command catalog and the help
   example list; `team.ErrNamedProfile` still says "(phase 5)" and now points at
   a verb that exists (text unchanged).

### Rows (migrate)

- B1 `TestMigrateFixtureProfileMapsToTheGoldenSquadYaml` (byte-exact golden
  `testdata/expected-squad.yaml`, 0644, loads), `TestMigrateReadsTheV2FilesReadOnly`
  (dev+inode+mtime+content unchanged), `TestMigrateRefusesAnExistingSquadYaml`.
- B2 `TestMigrateUnmappedReportIsExactAndComplete` (exact list, and every fixture
  key is mapped or reported).
- B3 `TestMigrateNeverWritesTheOwnerFilesAndProposesBesideThem` (grants.yaml and
  registry.yaml dev+inode+mtime unchanged, proposal 0600, parses),
  `TestMigrateRefusesASafetyDefaultNamingTheField` (14 cases).
- B6 `TestMigrateStagesRolePromptsAndLeavesTrustToTheOwner`.
- B7 `TestMigrateDryRunChangesNothingOnDisk` (dev+inode+mtime across project and
  HOME), `TestMigrateVerb` (internal/cli).
- B8 `TestMigrateRefusesACredentialAndWritesNothing` (the ghp_ shape built at run
  time; launch.local.json, a profile field and a role.md).
- B9 `TestMigrateDeletesNothingWithoutTheRemoveFlag`,
  `TestMigrateRemoveRefusesASquadYamlThatDoesNotLoad`.
- Named profile: `TestMigrateNamedProfile`.

## Gate

Quiet host (0 matching processes). Head 2375e600. Output in
`docs/v3-spike/5-migrate-evidence/gate.txt` (copied through
`scripts/redact-evidence`, 0 values replaced): go build, go vet, GOOS=linux and
GOOS=windows builds, `scripts/manifest-check.sh --final` (OK; 10 new rows, four
rows refreshed: squad.go, doctor_host.go, command_registry.go, cli.go),
`go test -count=1 ./...` and `go test -count=1 -race -timeout 45m` on
internal/team, internal/migrate, internal/seatcommit, internal/cli: all exit 0.
Revert check: `docs/v3-spike/5-migrate-evidence/revert-check.txt`, 29 rows (one
or more per rule A1 to A6 and B1 to B9), each FAILS when its line is reverted,
0 rows not biting. The first revert pass found five weak rows (the golden key
coverage hidden by the structure compare, the doctor wiring revert that did not
build, a never-settable key covered by its parent, the create-only write
hidden by the pre-check, the absent-squad refusal hidden by the load check);
each got a sharper test (`TestSquadYamlGoldenParsesToTheExpectedStructure`
regenerated-expected case, `TestSquadYamlNeverSettableKeysAreRefusedWithTheKeyNamed`
literal key list, `TestCreateNewNeverReplacesAFile`, the detail assertion in
`TestMigrateDeletesNothingWithoutTheRemoveFlag`) and the pass above is the rerun.

Not done: nothing in the brief is left out. Open for the lead: the A6 finding
(a hand edit of squad.yaml is not refused by the apply, only shown as
`replace`), and the unmerged `uninstall` (out of scope).
