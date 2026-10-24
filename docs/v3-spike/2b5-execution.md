# Brief 2b-5 execution log: squad.yaml and the team rows

Branch `v3-2b-5` from `v3` at f0129ebf. Implementer: Opus 5.5. Standard
gate on every commit: `go build ./... && go vet ./... && go test ./... &&
scripts/manifest-check.sh --final`, `GOOS=linux go build ./...`,
`GOOS=windows go build ./...`.

## Lead rulings at the start (2026-10-24)

1. Stay on `v3`. squad.yaml seats are a typed list with the fields of 2b-2's
   `seatlaunch.Seat` (read from `v3-2b-2`, not merged). Argument forms are
   checked against a `launchapi.ProviderCapabilitiesV1` fixture. Acceptance 4
   is recorded as "compiles once 2b-2 merges"; the one-function adapter is a
   follow-up. The files 2b-2 deletes are avoided.
2. The v2 profile model goes; `internal/team` becomes the in-memory form of
   `<repo root>/squad.yaml`, one squad per repository. The namespace profile
   dimension stays as identity; "default" moves to `internal/namespace`. A
   named non-default profile with no squad config gets a named error that
   points at phase 5 `migrate`. Strict parse goes through
   `grants.ParseSquadNarrowing`, every "ignored" narrowing becomes a named
   widening error; guard keeps its lenient reader and the two agree on the
   narrowing. Both are behavior changes.

## Commit 1: squad.yaml schema 1, location, loader and validator

Built:
- `internal/yamlsub/yamlsub.go` (new, 261): the strict YAML subset reader,
  moved out of `internal/grants/parse.go` with exported names, so the owner
  file and squad.yaml share one syntax. `grants/parse.go` 458 -> 227 (new
  row, current_lines updated); behavior unchanged (grants and guard tests
  pass unchanged).
- `internal/team/squad.go` (new, 488): squad.yaml schema 1. `Squad` (session,
  lead, lead_mode, execution_mode, control_root, target_project_root,
  shared_cwd_exception, defaults, seats, the grant narrowing), `Seat`
  (handle, role, runtime, model, effort, worktree, tools, actor_mode),
  `ParseSquad` (strict, against the owner grant), `LoadSquad`,
  `LoadSquadWith`, `SquadPath`, `SeatWorktree`, `Resolved` (defaults
  applied; pi never takes model or effort), `Render` (canonical form, round
  trips), `WidenError` / `ErrWidens`, `ExampleSquad`.
- `docs/squad-yaml.md`: schema, location, writer, narrowing rules, the
  example (a test keeps it equal to `ExampleSquad`).

Rulings applied: location `<repo root>/squad.yaml` (E2); mode narrows auto to
manual, never widens (ruling 2); R37 defaults stay in `grants.Implicit`,
squad.yaml only adds protected paths (ruling 3).

Tests (`internal/team/squad_test.go`):
- acceptance 1: `TestSquadWideningIsANamedParseError`, ten widening cases
  (max_seats above the owner cap, `!` path removal, a removal key, manual to
  auto, privacy widened, runtimes outside the owner list, a seat runtime
  outside the grant, more seats than the cap, owner-only `default_branch`
  and `merge`), each a `WidenError` naming the field.
- acceptance 2: the guard tests in `internal/cli/hook_verbs_test.go`
  (fix9 row included) and `internal/grants` pass unchanged; guard still
  reads through `grants.LoadFrom`, and `TestSquadNarrowingMatchesGuardReader`
  checks that squad check's narrowing equals guard's.
- strict parse: twenty refusal cases, none reported as widening.
- example parse and round trip, doc equality.

Behavior changes: none at runtime yet (no caller of the new loader). A
squad.yaml that `team check` refuses for widening still narrows guard
leniently, as before.

Deviations: none.
