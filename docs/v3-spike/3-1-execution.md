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

## Lead rulings at the halfway checkpoint (44f30dcc)

1. Consumed marks under `extensions/amq-squad/consumed/`: accepted. The whole `<root>/extensions/amq-squad/` tree was already guard's first implicit entry. `TestGuardProtectsGateStoreAndOwnerGateFiles` now pins consumed/, denials/ and reasks/. The 2a-3 guard note (docs/v3-spike/2a3-notes.md, protected paths) has a 3-1 update line.
2. The gate text shows the target string: accepted. It is single-line, at most 512 bytes and built by the core (`gatestore.Render`), and it is in the digest preimage: digest = sha256(domain, action, sha256(target)), with gatetarget.Digest over exactly `Record.Target`. `Record.Validate` re-derives both the digest from the target and the text from the record's fields, so the text cannot drift from what is hashed (`TestRenderAndValidate`, tamper rows "target" and "text").
3. `wrong_gate_thread`, no re-ask: accepted.
4. Body key and share loading go to 4-5: accepted.
5. subject.go stays the evidence helper: accepted.
6. The implicit set's ~/.claude/plugins/, .claude/hooks/ and ~/.codex/config.toml: accepted; 4-2 names the exact plugin paths when it lands.
- Ruling P, quoted: "For the six typed actions: `gate raise` writes the store via the core, `operator answer` refuses them with the message naming `amq-squad gate answer`, `verify action` decides from the store, `verify authorization --gate` verifies answers/<id>.json; delete the v2 AMQ-answer code for those actions and the compound-release approval tests that exercise it, keep the compoundrelease writer and the domain classification for 3-2, keep the v2 path for the merge kinds until 3-2's train, and list every deleted test by name in the log with the replacing test named beside it. Record the refusal message as a behavior change in the commit and the log."
- Reviewer item, explicit s < n: the check is `nostr.CheckSigScalars` at internal/nostr/event.go:440-450 (r >= p and s >= n refused). It is called at event.go:415, before btcec's `schnorr.ParseSignature` (:418) and the library verify (:422). `TestSignatureScalarBounds` counts library calls and proves s == n, s == n+1, s == 2^256-1, r == p and r == 2^256-1 are refused with zero calls into btcec. `TestConformanceRules` repeats s == n and s > n through the core.

## Commit 6: the typed-gate CLI on the gate store; the v2 typed AMQ path retired

Built:
- `internal/cli/gate_store.go` (new):
  - `gate raise` for a typed action (tag, github_release, package_publish, external_send, credential_change, spawn; `--kind` optional, `--window`, default 30m): the core writes the record under the session root with `raised_by` = the resolved handle, then posts the question through the transport. Without a transport the record is still written, the output says "question not sent yet", and `gate post --gate ID` sends it once.
  - `gate answer --event ID` or `--from HANDLE --label ...` (the mailbox label path) runs `Core.HandleEventID` / `HandleLabel`. Exit 0 approved, 11 denied, 10 re-asked, 13 rejected.
  - `gate status [--gate ID] [--json]`.
  - `verify action` for a typed action decides from the store: approved only when answers/<id>.json passes `gateverify.VerifyAnswerFile` against the binding's pin and the owner's trust store; denied, pending, unbound otherwise.
  - `verify authorization --gate ID --action A --target T` runs the same check plus the caller's exact action and target against the record.
  - `gateTransport` is the seam brief 4-5 fills: Buzz attachment, relay connection, body key and NIP-OA tag. Its default returns "no Buzz transport is attached".
- `internal/gateverify/binding.go` (new): the owner's `~/.amq-squad/gate-binding.json` (schema 1: relay, channel, owner and body keys as npub or hex, carriers, issuer_key; default issuer key `~/.amq-squad/issuer-key.pem`). It holds no secret and is read through operatorauth's owner-controlled, no-symlink, not group/world-writable file check. Guard protects both files.
- `internal/gatestore/accepted.go` (new): `Accepted`, `AcceptedFor`, and the growth gate. `SeatRequest(grants, root, seats, trust)` is for the scheduler's seat request past the cap. `LimitRequest(grants, root, limit, value, within, trust)` is for 2b-2's widening past a grant limit, with target `gatetarget.GrowLimit(root, limit, value)` = "grow <root> <limit>=<value>". Insecure grants keep every request gated with `owner_grants_insecure`. `ParseGrow` was dropped (nothing reads a target back).
- The core's `Insecure` flag comes from `grants.Load("")` (the owner file's trust state).

Behavior changes named:
1. `operator answer` refuses the six typed actions, both as `--action` and when the latest AMQ question carries a typed authorization request. A release-owned question (the compound release's tag and github_release children) is refused the same way. The refusal message, verbatim: "operator answer refused: typed gates (tag and release, external send, credential change, growth) are answered only by the owner's signed Buzz reply in the gate thread; the core verifies it with 'amq-squad gate answer'. An AMQ operator answer never counts (plan 7.3, R32)."
2. `gate raise` with a typed action no longer sends an AMQ request to the operator. It writes the gate store and asks the owner in Buzz. `--note`, `--task` and `--to` are refused for a typed gate.
3. `verify action` for a typed action reads the gate store, never AMQ messages. `--emit-authorization` is refused for typed actions: answers/<id>.json is the authorization.
4. `verify authorization --file` refuses an envelope for a typed action and names `--gate`.
5. `verify action`'s "Resolve with" hint for a typed action names the owner's signed Buzz answer and `gate answer`.
6. Merge kinds (default_branch_push, protected_branch_push, feature_branch_push) keep the v2 AMQ path unchanged until 3-2's train.

Deleted v2 code (the AMQ-answer path for typed actions):
- operator.go: `validateReleaseOperatorChildIdentity`, `exactOperatorAMQContext`, `freezeReleaseOperatorAnswer`, `releaseOperatorAnswerEnvelopeEqual`, `releaseOperatorInvocationBoundary`, `inspectReleaseOperatorAnswerReplay`, `validateReleaseOperatorReplayMessage` and the release-owned send branch (942 -> 756).
- verify_action.go: the release-owned guarded-use branch (compound evidence, signing inside the guard) and `catalogActionNames`. verify_authorization.go: the compound evidence fields.
- compound_release_domain.go: `cliReleaseGuardObservation`, `cliReleaseGuardedUse`, `NewGuardedUse`, `Run` (386 -> 294). The domain classification (`classifyCLIReleaseQuestion` and its checks) stays for 3-2.
- Left in place for 3-2 and now unreachable from main (deadcode): `compoundrelease.NewInvocationGuard` and `InvocationGuard.Run` (internal/compoundrelease/guard.go, a 3-2 row); `newDurableReconciledExistingResult` in amq_send.go (its only caller was the release answer replay; a kept amq_send test still exercises it).

Deleted tests, each with its replacement:

| deleted test | replaced by |
| --- | --- |
| TestCompoundReleaseVerifyActionUsesGuardedHumanAuthority | TestTypedGateCLIRoundTripOnTheStore (verify action from the store); gateverify TestFourClassesThroughTheCore |
| TestCompoundReleaseVerifyActionMarkerStrippedFailsClosed | TestTypedGateCLIRoundTripOnTheStore (pending before the answer, unbound for another target); TestCLIReleaseDomainPhysicalMarkerOwnershipFailsClosed (kept, classification) |
| TestSignedCompoundAuthorizationRejectsSuccessorGeneration | TestTypedGateCLIRoundTripOnTheStore (verify authorization --gate, other target refused); gateverify TestVerifyAnswerFileTamper |
| TestCompoundReleaseOperatorAnswerUsesGuardedExactSelectedRoot | TestOperatorAnswerRefusesTypedGates |
| TestCompoundReleaseOperatorAnswerDeniedGitHubReleaseKeepsChildIdentityIsolated | TestOperatorAnswerRefusesTypedGates; gateverify TestFourClassesThroughTheCore (deny record per class) |
| TestCompoundReleaseOperatorAnswerIdenticalConcurrencyReconcilesAndOppositeFailsClosed | gateverify TestConcurrentAnswers; gatestore TestCommitConcurrentApproveDeny |
| TestCLIReleaseDomainEligibleDefaultAndGuardObservationIsImmutable, guard-observation half (test renamed TestCLIReleaseDomainEligibleDefaultClassification, classification half kept) | TestGateStoreWrittenByCoreIsReadByGuardAndContext; gateverify.Verify reads only stored, validated records |
| TestCLIReleaseDomainDuplicateAndNewerQuestionFailBeforeGuardCallback, subtests "fresh exact-gate conflict" and "newer same gate" (test renamed TestCLIReleaseDomainDuplicateQuestionFailsClassification, subtest "duplicate" kept) | gatestore TestRaiseWritesBeforeSend (one id, one record, O_EXCL); TestRenderAndValidate |

Deleted test helpers that only those tests used: installPersistingOperatorAnswerAMQ, configureReleaseOperatorAnswerTeam, preparedReleaseChildByRole, activeReleaseChildByRole.

Converted, not deleted: these tests used tag, github_release or external_send only as a vehicle for the v2 AMQ binding rules, which stay for merge kinds. They now use a merge kind with the same assertions: TestVerifyActionBlocksGatedReleaseBeforeOperatorAnswer, TestVerifyActionDeniedExitCode, TestVerifyActionLatestOperatorAnswerWinsOverOlderApproval, TestVerifyActionPassesBoundOperatorAnswer, TestVerifyActionQuestionRequiresStrictBindingFields, TestVerifyActionRejectsApprovalBeforeLatestGateQuestion, TestVerifyActionRejectsApprovedAnswerForDifferentTarget, TestVerifyActionRejectsProseOnlyOperatorAnswer, TestVerifyActionRequiresOperatorAnswerSender, TestVerifyActionSameTimestampUsesIDTiebreak, TestVerifyActionEmitsSignedAuthorizationOnlyWithProvisionedKey (protected_branch_push), TestAmbiguousRawGateFrontmatterBlocksVerifierAnswerAndAttention, TestOrdinaryTypedOperatorAnswerUsesNamedSelectedQuestionDespiteDefaultRootResidue, TestOrdinaryTypedOperatorAnswerSelectedQuestionMismatchesFailBeforeSend, TestSafeTypedGateRaiseAnswerVerifyRoundTrip (feature_branch_push or default_branch_push).

New tests: TestTypedGateCLIRoundTripOnTheStore (raise writes the record with raised_by and posts the question; verify action pending; a plain DM answer is re-asked (exit 10); a label from a seat is refused; the carrier's labelled thread reply is approved; verify action approved, another target unbound; verify authorization --gate passes, another target refused; guard allows `git tag v3.0.0`; gate status shows accepted and reasked; a second raise fails), TestTypedGateRaiseWithoutTransport (record first, post once), TestOperatorAnswerRefusesTypedGates (six actions plus a typed AMQ question, no AMQ call), TestGuardProtectsGateStoreAndOwnerGateFiles, gatestore TestGrowthGate (within the grant; past the cap gated with the Grow target and digest; an unanswered gate does not open; an accepted gate opens exactly seat 3, not seat 4; an untrusted issuer does not open; insecure grants; widening past a limit).

Manifest: verify_action.go accepted-3 -> shrunk (746 -> 699, target 700); verify_authorization.go accepted-3 -> shrunk (337 -> 296, target 300); verify_rebind.go accepted-3 -> shrunk (126 -> 100, target 100; usage text condensed, behavior unchanged). Each note quotes the old reason "guard ancestor; keep decision flow and signed auth". operator.go stays accepted-3 for 4-3, 942 -> 756, the moved count in its note. compound_release_domain.go keep 386 -> 294; gate.go keep 175 -> 214. New rows: cli/gate_store.go, gatestore/accepted.go, gateverify/binding.go. Docs that still describe `operator answer` for release gates (plugins/*/skills, operator-cookbook.md) are not changed here. The refusal message names the new command; the skill text belongs to 4-2 and phase 5.

## End checkpoint

Review rounds after the end checkpoint (the commits are logged below):
- First Codex review of 45ad7409: 7 findings (4 high, 3 medium), fixed in 90085638 (commit 8). The residual I flagged (question published before it was recorded) was fixed in 39ede1f7 (commit 9).
- Second pass on 39ede1f7: findings 1, 2, 5 and 6 closed; 3, 4, 7 and 8 partial; 1 new defect (question_sent on a publish failure). All fixed in 62fba504 (commit 10).
- Third pass on 62fba504: the 5 items fixed there are closed; E is partial (directory fsync, `[]` reservations, temp hygiene). Closed by commit 11, which the lead verifies with no further Codex pass.
- Final head: see the commit 11 section.

Branch `v3-3-1`. Commits on top of `v3` (2736ebdd): 69b1f399 grants; b7e11157 operatorauth taxonomy and deny record; 1ea621d0 gatetarget; a6ad7745 gatestore; 44f30dcc gateverify and nostr; 7e042025 gate CLI and retirements; and this log commit. Every commit passed the standard gate.

| # | acceptance | status | evidence |
| --- | --- | --- | --- |
| 1 | write-before-send | met | gatestore TestRaiseWritesBeforeSend (child process killed between write and send) |
| 2 | four classes: approve, deny, replay, wrong digest, wrong key, forged signature | met | gateverify TestFourClassesThroughTheCore |
| 3 | conformance rules | met | gateverify TestConformanceRules; nostr TestStrictDecoding, TestSignatureScalarBounds (explicit s < n at internal/nostr/event.go:440-450, called at :415 before btcec) |
| 4 | thread re-ask | met | gateverify TestThreadReask (g-owner-01 and g-owner-02 shapes), TestS1OwnerRunEventsParse (the real S1 events parse and verify); cli TestTypedGateCLIRoundTripOnTheStore |
| 5 | nostr:<eventID> label against a test relay | met | gateverify TestLabelPathAgainstATestRelay (real websocket relay with NIP-42) |
| 6 | acceptance-final, dot-led id | met | gateverify TestAcceptanceFinalAndDotLedID; gatestore TestCommitSingleUseAndStates; 2a-3 fix12 and fix13 rows unchanged |
| 7 | credential_change opens gh auth login | met | cli TestGuardCredentialGateOpensWithVerifiedAnswer |
| 8 | grants: unknown key is grants_invalid, squad.yaml cannot widen, insecure mode per ruling 1 | met | grants TestParseOwnerRejects, TestNarrowNeverWidens, TestLoadSources, TestInsecureOwnerFailsClosed; cli TestHookGrantsSourcesAndFormat; gateverify TestInsecureGrantsRefuseApprovalsNotDenials; gatestore TestGrowthGate |
| 9 | owner run with the real Buzz key | pending | recorded as pending the shared owner session with 4-5's acceptance 4, held at 4-5's end (ruling 7). S1 already proved the real-key path (23 490 ms post to envelope). The production path needs 4-5's transport behind `gateTransport` and the owner's gate-binding.json |
| 10 | no package under internal/ reads an nsec | met | gateverify TestNoPackageUnderInternalReadsAnNsec (164 files, mutation-checked) |

Gates: the standard gate plus `go test -race -count=1` on internal/gatestore, internal/gateverify, internal/nostr, internal/grants and internal/gatetarget: 37 top-level tests and 11 subtests pass, 0 fail (gatestore 3.2 s, gateverify 6.3 s, nostr 2.1 s, grants 2.7 s, gatetarget 2.3 s).

Measurements:
- Guard in process (TestHookVerbsColdRunBound, 20 gate records): cold 3.5 ms, warm p50 2.4 ms, p95 2.9 ms (2a-3 measured 1.2, 1.1 and 1.5 ms). The rise is the implicit R37 set, which adds ten protected entries and a managed-block read for CLAUDE.md and AGENTS.md writes. It stays far under the 50 ms hook budget.
- Raise to verified envelope on the local websocket test relay with synthetic keys: 96 ms.
- Gate window: the 30-minute default is kept (ruling 5). It is a policy value, not measurable without the owner. The owner run measures post-to-envelope.

Manifest statuses of the Replaces rows: verify_action.go shrunk 699 (target 700); verify_authorization.go shrunk 296 (300); verify_rebind.go shrunk 100 (100); commandevidence/subject.go shrunk 397 (400); operator.go accepted-3 for 4-3, 942 -> 756 with the moved count in its note; hook_store.go new, 114 (schema 1 reader through internal/grants). 3.0 additions: 27 files, 5,546 lines (manifest-check --final).

Contracts for the dependent briefs:
- 2b-5: `grants.Narrowing` and `grants.Narrow`. `grants.ParseSquadNarrowing` is the interim reader of squad.yaml's grant keys.
- 2b-2: `gatestore.Store.LimitRequest` for widening past a grant limit; `grants.Resolved.RuntimePermitted` and `PrivacyPermitted`; the target `gatetarget.GrowLimit`.
- 2b-1 (scheduler): `gatestore.Store.SeatRequest`.
- 2b-3 (doctor): `grants.Insecure(path)`, `Resolved.InsecureWhy` and `Resolved.Ignored`.
- 3-2: `gatestore`, `gateverify.VerifyAnswerFile`, `verify authorization --gate`, and `grants.Merge` / `MergeFor`. The v2 merge-kind AMQ path, the compound domain classification and compoundrelease's InvocationGuard (now unreachable) are left for 3-2's rewrite.
- 4-2: `gatetarget` (target strings, Class), guard's needs_gate reason codes, and `grants.Implicit` for the plugin's exact paths.
- 4-3: `grants.Fleet` (reserved fields).
- 4-5: the `gateTransport` seam (relay, body key, NIP-OA tag), `gateverify.BodyAuth`, `WSRelay`, `~/.amq-squad/gate-binding.json` (schema 1), the issuer key at `~/.amq-squad/issuer-key.pem`, and the carrier handles of the label path.

Open items for the lead:
1. The issuer key path `~/.amq-squad/issuer-key.pem` and the binding file `~/.amq-squad/gate-binding.json` are my choice; no ruling names them. Guard protects both.
2. Skill and cookbook text that still says `operator answer` for release gates (plugins/*/skills, docs/operator-cookbook.md) is not edited here. The refusal names the new verb.
3. The CLI's `Core.Version` is the literal "3". The v2 `authorizationIssuer()` build-info helper could fill it once 3.0 has a version.

## Commit 8: fixes for the Codex review of 45ad7409

Seven findings, one test row each, every row mutation-checked: with its fix reverted the row fails, and with the fix in place it passes.

| # | finding | fix | test row |
| --- | --- | --- | --- |
| 1 | HIGH: growth was authorized on the Ed25519 issuer envelope alone | `gatestore.Accepted`/`AcceptedFor`/`SeatRequest`/`LimitRequest` are removed. The growth gate is now `gateverify.Verifier` (growth.go): the store's recovered decision must be accepted, and the approval must pass the full `VerifyAnswerFile` (owner BIP-340 event re-verified, the record's issuer, the caller's namespace) against the binding's pin and the owner's trust store. Guard's issuer-envelope check stays only for its in-session advisory verdict | gateverify TestReview1GrowthRequiresOwnerSignature (an issuer-only envelope for a spawn gate does not open growth; an owner-signed answer opens exactly that seat; widening and insecure grants rows) |
| 2 | HIGH: an approval replayed across namespaces and generations | `VerifyAnswerFile` takes the caller's `Namespace` and requires the signed binding to equal it exactly (project, profile, session, namespace id, generation). The CLI passes the selected context through one helper, `gateNamespace`, for signing and for verifying | gateverify TestReview2ApprovalDoesNotReplayAcrossNamespaces (session A's whole gate store copied into session B with the same binding is refused; another project, profile or generation is refused) |
| 3 | HIGH: an Edit could strip a managed marker | `managedBlockKept` applies Write, Edit (first match, or all with replace_all) and MultiEdit in memory to the current text. It requires the same begin and end marker counts and the same block text; an absent, empty or unparseable search counts as a change | cli TestReview3EditCannotStripTheManagedMarker (`Intro\n<!-- amq-` -> `Intro\n<!-- other-`, end-marker rewrite, replace_all, absent search, a chained MultiEdit, a Write adding a second begin marker; an edit outside the block is still allowed) |
| 4 | HIGH: a crash between the approval file and its marks let a later deny win | `Store.Commit` recovers under the gate lock before anything else. An existing answers/ or denials/ file is the gate's decision: its missing marks are completed, the retry of that same answer succeeds once, and every other answer is already_consumed. `Store.Decision` is the one recovered decision every reader uses: a deny record or denied mark wins, then an approval file or mark. `State`, `Verifier.Accepted` (so `verify action` and `verify authorization --gate`) and the guard reader all use it; the guard reader puts a present deny record first | gatestore TestReview4CrashBeforeMarksRecoversTheDecision; gateverify TestReview4VerifierUsesTheRecoveredDecision |
| 5 | MEDIUM: mixed thread references bypassed wrong_gate_thread | Every thread reference (reply, root, unmarked) must name the gate's own thread: its question or its verified re-ask. One reference elsewhere is `wrong_gate_thread` | gateverify TestReview5MixedThreadReferencesAreRejected (root B plus unmarked A, reply A plus root B, unmarked A and B, reply A plus unmarked B; root and reply both naming A is accepted) |
| 6 | MEDIUM: an unreadable squad.yaml silently removed the narrowing | Only absence (`fs.ErrNotExist`) narrows nothing; any other read error fails closed (guard denies `grants_invalid`) | grants TestReview6UnreadableSquadYAMLFailsClosed (mode 000, a directory, absence) |
| 7 | MEDIUM: re-asks were published before an exclusive reservation | `Store.ReserveReask` signs and fsyncs the one re-ask under the gate lock before publication. `ReaskPublished`/`MarkReaskPublished` record publication. A failed publish is retried with the same event on the next non-thread answer; once published, further non-thread answers are refused with no re-ask | gatestore TestReview7ReaskReservedOnceBeforePublication (8 concurrent reservations, one record, one event); gateverify TestReview7ReaskPublishedOnceAcrossRetries (publish fails once, the retry publishes the same event id, one re-ask on the relay) |

Behavior changes: TestCommitCrashBeforeMarks is replaced by TestReview4CrashBeforeMarksRecoversTheDecision, because an approval file without marks is now the gate's decision (accepted) rather than an open gate. A deny record makes the guard reader report denied without verifying it; a forged deny file can only close a gate (fail closed), and same-UID tampering is the accepted threat model.

Not in the findings, same class as finding 7: `Store.PostQuestion` still publishes the question before recording it, so a crash between the two can post a second question for the same record on retry. A second question is harmless to verification: only the recorded question counts for the thread, and the record and digest are unchanged. Left as is; flagged for the second pass.

Gates: standard gate green. `go test -race -count=1` on gatestore, gateverify, nostr, grants and gatetarget: 43 top-level tests and 11 subtests pass, 0 fail.

Manifest: gatestore/accepted.go's row removed (the file was added and removed on this branch, never on v3); gateverify/growth.go new; new rows resynced; verify_action.go 699 and verify_authorization.go 296 are unchanged.

## Commit 9: the gate question is recorded before it is published (review residual)

The residual flagged in commit 8, fixed as the lead asked, in the same style as `ReserveReask`. `gatestore.Sender` is now `SignQuestion` plus `PublishQuestion`. `Store.PostQuestion` signs the question under the gate lock and records it (O_EXCL, fsynced, `gates/<id>.question.json`) before anything is published, then publishes the recorded bytes and writes `gates/<id>.question.published`. A failed publish is retried with the same event id. A question already published is refused. Concurrent posts sign exactly one question. `Core.QuestionSender` signs with the body key and publishes the recorded raw event.

Test row: gatestore TestReview8QuestionRecordedBeforePublication (the first publish fails and the question is already recorded; six concurrent retries sign nothing new and publish only the recorded event id; a post after publication is refused and publishes nothing). Mutation-checked: with the recorded-question lookup disabled the row fails. Acceptance 1 (TestRaiseWritesBeforeSend) passes unchanged.

Gates: standard gate green; `go test -race -count=1` on the five gate packages passes.

## Commit 10: second Codex pass on 39ede1f7

Findings 1, 2, 5 and 6 were closed by the second pass. These are the four partial fixes and the one new defect, with one row each; every row is mutation-checked (it fails with its fix reverted and passes with the fix).

| finding | fix | test row |
| --- | --- | --- |
| 3 (partial): only the first managed block was compared | `managedShape` lists every managed block, in order, and every stray marker; the edited text's shape must equal the original's (`managedBlock` removed) | cli TestReview3bEveryManagedBlockIsCompared (an Edit inside the second of two blocks, or inside the first, is protected; edits between and after the blocks are allowed) |
| 4a: Commit checked approvals before denials | `recoverLocked` decides with the same rule as `Decision`: a deny record, else a denied mark, else an approval file. That happens before any write, and the marks follow the deny | gatestore TestReview4aCommitConsultsTheDecisionBeforeAnyWrite (both files, no marks: the approval retry is refused and the gate mark says denied; a denied mark alone refuses an approval and writes no approval file) |
| 4b: the guard reader did not use Store.Decision | `readHookGates` calls `gatestore.Store.Decision` and verifies the envelope only when the decision is accepted. Store, verifier and guard now share one reader rule | cli TestReview4bGuardReaderUsesTheRecoveredDecision (a valid approval file plus a denied mark: guard needs_gate, reader state denied) |
| 4c: a half-marked decision was never repaired | `Store.Recover` (the gate lock plus `recoverLocked`) runs in `Core.HandleEvent` before the replay check, and writes whichever mark is missing | gateverify TestReview4cHalfMarkedDecisionIsRepaired (event mark deleted after an approval; the next answer repairs it, and the event is single-use again) |
| 7 (partial) and 8 (partial): writeOnce created the final name before writing | `writeOnce` writes a temp file in the same directory, fsyncs it, and hard-links it into place: the link fails if the name exists, so create-once still holds, and a kill never leaves an empty final file. A reservation that is empty or not JSON (left by older code) reads as absent, and the reserve path removes it under the gate lock and records a real one | gatestore TestReview7bEmptyReaskReservationIsRepaired; gatestore TestReview8bEmptyQuestionReservationIsRepaired |
| new: a publish failure printed "question posted" | `question_sent` (raise and status) follows the `.question.published` marker, not the event id. A relay rejection prints "question not sent yet: ..." with "send it with: amq-squad gate post --gate ID"; JSON has `question_sent: false` and the error | cli TestReview9PublishFailureReportsUnsent (json and text raise with a rejecting relay; status false, then gate post publishes the recorded event and status is true) |

Gates: standard gate green. `go test -race -count=1 -v` on gatestore, gateverify, nostr, grants and gatetarget: 48 top-level tests pass, 0 fail.

## Commit 11: E (directory durability, reservation decoding, temp hygiene)

- E1: `mkdirDurable` replaces every `os.MkdirAll` in the gate store. It creates each missing directory (gates/, answers/, denials/, reasks/, consumed/...) and fsyncs its parent, so a directory created for a reservation survives a power loss with the reservation in it. Hard-link-into-place stays.
- E2: `readReservation` decodes strictly into the expected struct (unknown fields refused, one value). Any reservation that does not decode, or decodes without this gate's id and a 64-hex event id (`[]`, `{}`, `{"gate_id":...}` alone), reads as absent. The reserve path removes it under the gate lock and records a real one. The `brokenReservation`/`discardBroken` pair (invalid JSON only) is gone. Row: gatestore TestReviewE2ArrayReservationIsReplaced (`[]` question replaced and published; `[]`, `{}` and a partial re-ask read as absent and are replaced).
- E3: `writeOnce` removes its temp before the single directory fsync, so one fsync covers the link and the unlink. `Store.Recover` (under the gate lock) sweeps `.tmp-once-*` and `.tmp-*` files in the store's directories that are older than the lock acquisition minus a one-minute grace, then fsyncs each changed directory once. The grace is my addition, so the sweep never removes a live writer's temp in another gate (writers hold a temp for milliseconds). Row: gatestore TestReviewE3OrphanTempsAreSwept (old orphans in gates/ and answers/ removed, a fresh temp kept, the record intact).

Both rows are mutation-checked. Gates: standard gate green. `go test -race -count=1 -v` on the five gate packages: 50 top-level tests pass, 0 fail.
