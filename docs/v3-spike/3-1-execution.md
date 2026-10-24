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

## Commit 2: credential_change in the taxonomy and the signed deny record

Built: operatorauth action `credential_change` (gate kind `credential`, human-only, no alias), so a credential gate can verify (2a-3 open question 8, S1 open question 5). `denial.go`: `NewDenialEnvelope`, `VerifyDenialEnvelope`, `ValidateDenialPayload`, decision `denied` under its own signing domain `amq-squad-authz-deny-v1-lpbe`. `ValidateAuthorizationPayload` and `VerifyAuthorizationEnvelope` keep their contract (approvals only); a deny relabelled as an approval, or an approval relabelled as a deny, fails the signature. The canonical encoder takes the domain as a parameter (`canonicalPayload`); the approval bytes are unchanged.

Tests: `TestCredentialChangeIsATypedHumanOnlyAction`, `TestDenialEnvelopeNeverVerifiesAsApproval` (both relabel directions, tamper); the catalog test lists the new action.

Manifest: denial.go new (85); canonical.go 87 -> 94, envelope.go 345 -> 351, policy.go 280 -> 284 (keep rows, current_lines updated).

## Commit 3: the target-string table

Built: `internal/gatetarget` (new): the conventions of 2a-3 open question 9 in one place (`Command`, `TagPush`, `MCPSend`, `GWSSend`, `GitConfig`, `Write`, `Grow`/`ParseGrow`), the guard's command table (`Commands`, moved from hook_guard.go), the plan class per action (`Class`, moved from `guardGateClass`), `Validate`, and `Digest` (moved from `hookGateDigest`; the S1 non-merge formula, pinned by a hand recomputation in the test). Guard calls it for every target; the gate store writer and the verifier call it too.

Acceptance 7 met: `TestGuardCredentialGateOpensWithVerifiedAnswer` (`gh auth login --with-token` is needs_gate `gate_credential` with target `gh auth login`, then allow after a verified credential_change answer; `gh auth logout` stays gated; a `~/.ssh/config` write opens only with its own answer).

Deviation: `internal/commandevidence/subject.go` is not folded into the table. It is the evidence ledger's command-subject snapshot (callers in commandevidence/evidence.go, cli/evidence.go, task/command_evidence.go) and shares no code with gate target strings. It ends `shrunk` at 397 (target 400): `stringInSet` replaced by `slices.Contains`, and the redundant clauses of `isGoChdirToken` collapsed (same behavior; commandevidence and task tests pass).

Manifest: gatetarget.go new (137); hook_guard.go 503 -> 488, hook_store.go 121 -> 104; subject.go accepted-3 -> shrunk (405 -> 397) with a note quoting the reason above.

## Commit 4: the gate store writer

Built: `internal/gatestore` (new), the only writer of `<root>/extensions/amq-squad/`:

| path | written | by |
| --- | --- | --- |
| `gates/<id>.json` | O_EXCL, fsync, directory fsync, before the question is sent | `Store.Raise` / `Create` |
| `gates/<id>.question.json` | once, after the transport posted it | `PostQuestion` |
| `reasks/<id>.json` | once: the one "reply here to confirm" re-post | `PutReask` |
| `answers/<id>.json` | approval: operatorauth envelope + the owner's raw event + unsigned copies of record and question | `Commit` |
| `denials/<id>.json` | signed deny record (commit 2's `NewDenialEnvelope`) | `Commit` |
| `consumed/gates/<id>`, `consumed/events/<event id>`, `consumed/locks/<id>.lock` | marks O_EXCL fsynced after the answer file, under an exclusive flock | `Commit` |

The record (schema `amq-squad-gate/1`) keeps the S1 fields the 2a-3 reader reads (`gate_id`, `class` = the operatorauth action, `digest`, `not_after`) and adds `raised_by` (2a-3 open question 1), `gate_kind`, `typed_class` (what the owner types: release, send, credential, grow), `target`, `text`/`text_sha256`, `issued_at`/`not_before`, `owner_pubkey`, `issuer_key_id`. `Validate` rebuilds the digest from the target (gatetarget) and the text from the fields, so an edited record fails to load. Window default 30 minutes (ruling 5), 1 minute to 24 hours; clock skew 60 s.

Gate text says "Reply in this thread with one line:" (amendment 19) and shows the target string. Gate id grammar: operatorauth's gate grammar restricted to one owner-typable path segment, `[a-z0-9._-]{1,64}`, dot-led allowed (2a-3 fix13), not `.`/`..`, not ending in `.question`. `ParseAnswer` is S1's tuple rule (CR or LF anywhere refused, case folded, exactly four tokens, 16..64 hex prefix).

The consumed marks live under `consumed/`, not S1's `ledger/`: 2a-3 put the evidence ledger at `<root>/extensions/amq-squad/ledger/<handle>.jsonl`, so S1's `ledger/gate-<id>` would share that directory. Locks, gate marks and event marks have one subdirectory each, so no gate id can name another kind of file.

Reader change (hook_store.go): a gate whose deny record verifies against the trust store and binds gate, action and digest is `denied`, not `open`, so context stops listing it. A deny never opens guard. Otherwise the contract is unchanged.

Acceptance 1 met: `TestRaiseWritesBeforeSend` runs a child test process that raises a gate and exits in the hook between the record write and the send; the record loads, no question marker exists and no question is recorded, and a second raise with the same id fails with `os.ErrExist` and sends nothing; `PostQuestion` then sends the stored gate's question once.
Other tests: render and validate with eight tamper cases and eight bad specs; answer tuple parse; single use (replay of the gate, replay of the event across gates, deny closes, a deny writes no approval file); acceptance is terminal (accepted 48 h past not_after); crash between the answer file and the marks re-commits; 20 rounds of two approves and one deny racing, exactly one winner each, never an approval file after a deny won; re-ask once; the reader fields present, mode 0600. `go test -race -count=1 ./internal/gatestore/` passes. cli: `TestGateStoreWrittenByCoreIsReadByGuardAndContext` (raise via the store, commit a signed approval and a signed deny, guard allows the first, keeps the second gated, context lists neither).

Manifest: record.go 256, store.go 448, syncdir_unix.go 17, syncdir_windows.go 8 (fate new, status new); hook_store.go 104 -> 114.
