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

## Commit 5: the verifier path for the four classes

Dependencies (lead ruling (a), quoted above): `github.com/btcsuite/btcd/btcec/v2 v2.5.0` (BIP-340 verify) and `github.com/coder/websocket v1.8.15` (relay client), the S1 pins. `go mod tidy` (offline, module cache) also records their indirect modules: `btcd/chainhash/v2 v2.0.0`, `decred/dcrd/crypto/blake256 v1.1.0`, `decred/dcrd/dcrec/secp256k1/v4 v4.4.0`. Every go.sum line equals the spike's go.sum line for the same module. This commit adds the two modules and the code that imports them; the go.mod change is tidy's output and nothing else. (The ruling said "nothing else in it"; I read that as no other module change, because tidy drops a requirement no code imports, so the deps cannot land without their first importer.)

Built:
- `internal/nostr` (new, ported from spike/gateverify nostr.go and npub.go with their tests): strict NIP-01 decoding, the canonical id serialization, BIP-340 verify through btcec after an explicit r < p and s < n check, `ThreadRefs` (upstream buzzio order), npub. `key.go` holds the signer used for the body key (the core's own questions, re-asks and NIP-42 sign-in) and for test keys. It is never the owner key.
- `internal/gateverify` (new):
  - `Verify(raw, record, question, reask, pin, now)`: pin, id, signature, kind 9, one `h` tag, the tuple, gate id, typed class, the 16-hex prefix against the stored record's full digest only, the window rules, the stored question (body-signed kind 9 whose content is the record's text), and the thread last. A reply in the gate thread names the question, or the gate's stored re-ask (which sits in that thread). No thread reference at all is `not_in_gate_thread` (the re-ask case). A thread reference to anything else is `wrong_gate_thread`.
  - `CheckHistory` (S1 open question 3): upstream amq-remote's CheckHistory rules on the gate question: an edit (kind 40003, by body or owner) with other text, any deletion (5, 9005) naming the question or an edit, any body deletion, an owner deletion since the question: `question_altered`; a failed read or a full page of edits: `history_unknown`.
  - `BuildAnswerFile`: approve to an operatorauth envelope, deny to the signed deny record. Issuer `amq-squad`, signed by the issuer key the record names; evidence = the raw event digest and the gate-text digest.
  - `VerifyAnswerFile`: the S1 slot-in for consumers (release step, merge escalation). It never rests on the issuer key alone: stored record and question (the file's copies must equal them), the envelope against the trust store and the record's issuer, every payload field against the record, and the raw owner event re-verified at verified_at.
  - `Core`: `Raise` (record O_EXCL first, then the body-signed question published through the relay) and `HandleEvent`: the tuple names a stored gate; replay is `already_consumed`; Verify. A non-thread answer that matches the open gate (all other checks passed, so id, class, prefix and owner pubkey match) gets one re-ask: the body posts "...Reply here to confirm, with one line:" plus the tuple, tagged `["e", <question>, <relay>, "root"]`, recorded O_EXCL in `reasks/`, and the answer is not accepted. A second non-thread answer is refused with no second re-ask. Then the history check, the answer file, the commit. While the owner grants file is insecure, an approval is refused (`owner_grants_insecure`, not consumed) and a deny still closes the gate.
  - `HandleLabel` (amendment 20e): a mailbox message must come from a carrier handle (`wrong_amq_sender` otherwise, before any fetch) and carry exactly one `nostr:<64 hex>` label (`bad_label`). The event is fetched by id (`fetch_failed` if absent or substituted), then HandleEvent runs the full check set.
  - `Relay` interface (FetchByID, Query, Publish) and `WSRelay` (S1 relay.go ported: wss only, ws only on loopback, no redirects, NIP-42 AUTH with the body key and its NIP-OA tag).

Acceptance met:
- 2: `TestFourClassesThroughTheCore`, one subtest per class (tag, external_send, credential_change, spawn), on synthetic keys: approve (envelope verifies through operatorauth and `VerifyAnswerFile`), deny (the deny record verifies, no approval file), replay of the same event and a deny after an approve (`already_consumed`), approve after a deny (`already_consumed`), wrong digest prefix (`digest_mismatch`), the answer checked against another gate's record (`gate_mismatch`), wrong key and the body key (`wrong_pubkey`), forged signature (`bad_signature`). The brief maps "wrong digest" to `gate_mismatch`. At the core, a wrong typed prefix is `digest_mismatch` and an answer for another gate is `gate_mismatch`; both rows are tested.
- 3: `TestConformanceRules` through the core: s == n and s > n (`bad_signature`, before the library is called: `TestSignatureScalarBounds` in internal/nostr counts library calls), invalid UTF-8, duplicate key, null tag element (`malformed_event`), LF at the end, CR at the start, LF inside the tuple (`bad_tuple`), created_at 61 s ahead (`created_in_future`). Also the edges: exactly 60 s ahead accepted; past not_after refused; before not_before refused; no refusal consumed; the answer file is written before the consumed mark. internal/nostr `TestStrictDecoding` keeps S1's 28 wire forms.
- 4: `TestThreadReask`: the g-owner-01 shape (h and p tags only) gets one re-ask in the thread (body-signed, threaded to the question, published) and is not accepted; a second plain answer gets no second re-ask; the g-owner-02 shape (`["e", <root>, "", "reply"]`) is accepted; a reply to the re-ask is accepted on a second gate; a non-matching plain answer and a seat's plain answer get no re-ask. `TestS1OwnerRunEventsParse` parses and verifies the two real owner events in docs/v3-spike/S1-evidence/realkey with this package unchanged (01 has no thread reference, 02 has one). Their class is S1's `merge`, so they are not replayable as typed answers.
- 5: `TestLabelPathAgainstATestRelay`: a real websocket test relay (NIP-42; only the body may sign in; REQ before AUTH is CLOSED). The core signs in with the body key, raises two gates (the questions go through the relay), and the owner's answers are on the relay. A carrier message labelled `nostr:<id>` resolves, fetches, verifies and commits (`VerifyAnswerFile` passes, evidence names the relay). A label for an event whose e tag names the other gate's question is `wrong_gate_thread`. A seat sender is `wrong_amq_sender`. Zero or two labels, or a bad id, are `bad_label`. An unknown id is `fetch_failed`.
- 6: `TestAcceptanceFinalAndDotLedID`: a dot-led id `.g-dot` is raised, answered and committed; 48 h past not_after the store says accepted and `VerifyAnswerFile` passes (it re-verifies at verified_at). The 2a-3 fix12 and fix13 rows pass unchanged.
- 10: `TestNoPackageUnderInternalReadsAnNsec`: walks every non-test Go file under internal/ (164), blanks comments, and fails on any `nsec` in code (identifier, string, env var name; "insecure" excluded) or a nip19/nip49/fiatjaf import. Mutation: a planted `var ownerNsec` fails it.

Other tests: history (7 cases), insecure grants, 10 rounds of concurrent approve/approve/deny events with exactly one winner, `VerifyAnswerFile` tamper (a trusted issuer re-signing another action, target or question; a swapped raw event; an edited record copy; an issuer the record does not name). `go test -race -count=1` passes on internal/gatestore, internal/gateverify, internal/nostr, internal/grants. Mutation checks by hand: with the history check disabled `TestHistoryCheck` fails; with the thread check forced true `TestThreadReask` and `TestLabelPathAgainstATestRelay` fail.

Measurement: test relay, local websocket, synthetic keys, raise of two gates to a verified committed envelope: 96 ms (`TestLabelPathAgainstATestRelay` log line). S1's real-key run measured 23 490 ms from post to envelope; the owner run of acceptance 9 measures the production number.

Deviations and notes:
- The body key path stays INTERIM as in S1 (until upstream ask 8): `WSRelay.Auth` takes the body secret and its NIP-OA tag. Loading them from amq-remote's share layout is left to the attachment (4-5), which owns enrollment and the binding. 3-1 provides the `BodyAuth` type and the `Core` fields.
- The re-ask is posted as a thread reply with marker `root` (it is the core's own message in the gate thread). Buzz thread replies from the phone carry `reply`; ThreadRefs reads both.
- `wrong_gate_thread` is a new code. An answer threaded under another gate's question is refused, not re-asked: the re-ask is for answers outside any thread (amendment 19's case).

Manifest: nostr/event.go 499, key.go 59, npub.go 116; gateverify/verify.go 214, history.go 90, answer.go 194, core.go 242, relay.go 290 (fate new, status new).
