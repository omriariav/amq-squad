# amq-squad 3.0, brief 3-1: gates, grants and the gate store written by the core

Branch `v3-3-1` (the lead's name; the brief says `v3-3a`), cut from `v3` at 2736ebdd. One implementer, own worktree. Every commit passes the standard gate: `go build ./... && go vet ./... && go test ./... && scripts/manifest-check.sh --final`, `GOOS=linux go build ./...`, `GOOS=windows go build ./...`.

## Lead rulings received during the brief

- Dependencies (asked before commit 1): add `github.com/btcsuite/btcd/btcec/v2 v2.5.0` and `github.com/coder/websocket v1.8.15`, the versions the S1 spike pinned and the fourth Codex review accepted. Reason, quoted: "A hand-written BIP-340 verifier in math/big is exactly the kind of code the plan keeps out of the security path, however well cross-checked; signature verification uses the reviewed library." The relay transport stays behind an interface; tests use a fake relay.

## Commit 1: grants schema 1 and the narrowing function

Built: `internal/grants` (new package). This is the shared contract 2b-5 (squad.yaml), 2b-2 (seat-exec widening), 2b-3 (doctor) and 4-3 (fleet grant) code against.

- `Grants`: mode (`auto` | `manual`), `max_seats` (default 12), `cost_per_day_usd`, `permitted_runtimes` (absent = any), `privacy_class` (`vendor` | `local`), `default_branch`, `protected_paths`, `merge` (per repository: `repository`, `base_branch`, `ci_required`, `size_cap_lines`; no force push and no protected-path change are fixed refusals, not fields, and a `force_push` key is an error), `fleet` (reserved for 4-3: `max_squads`, `cost_per_day_usd`, `permitted_repositories`).
- `ParseOwner`: strict YAML subset (scalars, `[a, b]` lists, `- x` lists, a list of maps, one nested map). Unknown key, repeated key, malformed line, tab indentation or bad value is an error; guard denies `grants_invalid`.
- `Narrowing` and `Narrow(g, n) (Grants, ignored)`: the one narrowing function. Each field can only narrow (lower cap, lower cost, runtime intersection, auto to manual, vendor to local, added protected paths, fewer merge grants, lower merge size cap). A widening field is ignored and named in `ignored` for doctor. Narrow never mutates its input.
- `ParseSquadNarrowing`: the lenient squad.yaml reader guard uses until 2b-5 lands. Other top-level keys (2b-5's schema) are skipped; a grant key with a bad value is an error, never silently dropped. `default_branch` is the owner's alone.
- `Load` / `LoadFrom`: owner file `~/.amq-squad/grants.yaml` if present, else defaults; narrowed by `<repo root>/squad.yaml` if present. `Resolved` carries `Source`, `ReasonCode`, `Insecure`, `InsecureWhy`, `Ignored`.
- Insecure owner file (ruling 1, fail closed): a file that is a symlink, group- or world-writable, owned by another uid, or in a directory that is, counts as present but untrusted. `Resolved` then has `max_seats` 0 (every seat request is a growth gate), mode manual, privacy local, no runtime, no merge or fleet grant, and the file's own protected paths kept. Guard reports `grants_reason_code: owner_grants_insecure`, and a typed gate with a verified answer stays needs_gate with `reason_code: owner_grants_insecure`. A squad.yaml cannot lift the zero cap. `grants.Insecure(path)` is the reason string doctor prints. Off unix the check is a no-op (documented in secure_other.go).
- `Implicit` (ruling 3, R37): `.amq-squad/team-rules.md`, `managed:CLAUDE.md`, `managed:AGENTS.md`, `.claude/settings*.json`, `.claude/hooks/`, `~/.claude/settings*.json`, `~/.claude/plugins/`, `~/.codex/config.toml`, `squad.yaml`. Guard also protects `$AMQ_SQUAD_RULES_PATH` when set.
- `SeatRequest(seats)`, `RuntimePermitted`, `PrivacyPermitted`, `MergeFor`: the helpers the scheduler, seat-exec and 3-2 call.

Guard changes (hook_store.go, hook_guard.go, hook_guard_env.go): the provisional `hookGrants` reader is gone; guard calls `grants.LoadFrom`. `protectedPath` walks `ProtectedEntries()` (implicit set first). A `managed:` entry covers a file only when it holds the amq-squad managed block (internal/rules markers) and the write is not known to keep it: a Write whose new content has the same block, or an Edit/MultiEdit whose old_string is outside the block and that names no marker, is allowed; a shell write to such a file is denied.

Behavior changes named:
1. An insecure owner file no longer falls back to defaults (12 seats, typed gates answerable). It fails closed as ruled.
2. Without any grants file, the R37 set is protected (before: only the extension dir, the grants file and the trust store).
3. The owner file accepts `[a, b]` flow lists (`protected_paths: [a]` was an error).

Tests: `internal/grants` (schema round trip, 23 rejected owner files, narrowing never widens and never mutates, sources, insecure modes: file mode, directory mode, symlink, squad.yaml cannot lift the zero cap, implicit set). internal/cli: `TestHookGrantsSourcesAndFormat` rewritten for fail-closed (protected path still denies, seat start is gate_grow, verified tag answer stays needs_gate `owner_grants_insecure`, and opens again at 0600; each bad owner file gives `grants_invalid`); `TestGuardImplicitProtectedSet`, 19 rows. The 69-row `TestGuardRuleMatrix` passes unchanged.

Manifest: four new rows (grants.go 301, parse.go 458, secure_unix.go 37, secure_other.go 7; fate new, status new); hook_store.go 231 -> 121 (reason updated, status new), hook_guard.go 495 -> 503, hook_guard_env.go 261 -> 335.
