# Gate transport and launch API facts for amq-squad 3.0 (read-only research)

Upstream paths are relative to the AMQ v0.98.0 clone root. amq-squad paths are relative to `/Users/omri/code/amq-squad`.
Verdict up front: **exit criterion 1 cannot be met as written ("operatorauth envelope signed by an owner key") with zero change anywhere.**
The only owner key that amq-remote carries end to end is the owner's **nostr secp256k1 (BIP-340 Schnorr)** key. Today's operatorauth is
**Ed25519-only**. So criterion 1 needs either a new operatorauth signature scheme (an amq-squad-only change), or an owner Ed25519 key
on a device that amq-remote does not carry today (that needs an upstream change). Recommendation: reword criterion 1 to "an owner-signed
answer (BIP-340 over a gate text that names gate id, action class and target digest), carried by amq-remote, verified by the Go core
and wrapped in an operatorauth envelope as evidence". Design A below meets that with zero upstream change.

---

## A. Gate-answer transport

### A1. What amq-remote serve receives, and what reaches each target

- When the owner reacts ✅ or types yes, the relay delivers a **signed nostr event**: a kind 7 reaction or a kind 9 DM message.
  `ApproveEvidence{Reaction nostr.Event; Message nostr.Event}` is built from the owner's raw event and the body's stored approval
  message, "as received and stored, never re-encoded from parts" (`internal/remote/buzzio/approve_proof.go:28-37`, built at `:57-63`).
- The carrier attaches that evidence **only for an approve gesture** (`internal/remote/buzzio/carrier.go:1021-1029`). It passes the
  evidence in-process as `protocol.Command.Evidence`, which is `json:"-"`. It never goes on the wire, and the code comment says it
  "grants nothing by itself" (`internal/remote/protocol/protocol.go:204-208`).
- The endpoint passes the evidence to the target only when the attachment implements `EvidenceResponder`. Otherwise it calls plain
  `Respond(key, epoch, iid, option)` (`internal/remote/core/endpoint.go:1356-1364`; interface at `internal/remote/core/attachment.go:177-185`).
- Who implements it:
  - **Claude**: the only `RespondWithEvidence` implementation (`internal/remote/claude/approvals.go:521`). It writes
    `answers/<iid>.json` with `evidence` (`internal/remote/claude/approve.go:86-94`, dir layout `:25-37`). The PermissionRequest hook then
    **re-verifies the owner's BIP-340 signature itself** against a pin on its command line: `allowConfig` → `buzzio.VerifyApproveEvidence`
    plus `buzzio.CheckHistory` against the relay (`cmd/amq-remote/claude_approval.go:34-66`).
    Checks: kind 7 ✅ or kind 9 typed approve; `CheckID` and `VerifySignature`; pubkey equals the pinned owner; the last `e` tag (or thread
    refs) names the approval message; the event is dated inside the window; the message is kind 9, signed by the body, in the DM channel
    (`h` tag), and shows exactly the hook's own rendering of the call (`approve_proof.go:104-153`). A deny needs no proof
    (`approve.go:43-46`).
    Scope: an allow applies only to a **Bash call with command/description/timeout/run_in_background shown whole**
    (README `cmd/amq-remote/README.md:792-794`).
  - **Codex**: `Respond` sends `{"decision": option}` over the app-server JSON-RPC (`internal/remote/codex/attachment.go:989-1015`).
    **No signature reaches Codex.** amq-remote decides yes or no.
  - **pi**: `Respond` publishes an `answerRecord{protocol, ref, interaction_id, manifest_hash, option, subject?, at}`
    (`internal/remote/pi/deliver.go:166-174`; `pi.go:1428-1470`). It carries **no signature**. `subject` is "reserved, never checked,
    never written" (`docs/pi-bridge-protocol.md:367`).
- Net: **only the Claude hook path delivers owner-signed proof to the target.** Codex and pi get a bare option string that amq-remote
  decided. Any process running as the same user can write pi `answers/` and Claude `answers/` (README `:788-789`, "an allow in it proves
  nothing by itself").

### A2. Owner key model

| Key | Where it lives | What it signs | Arbitrary payload? |
|---|---|---|---|
| Owner nostr key (npub pinned as the share owner; nsec in the owner's Buzz client) | Owner's Buzz app or phone | Every Buzz event the owner sends: kind 7 reactions, **kind 9 DM text** | **Yes**, for typed text. The kind 9 `content` is whatever the owner types, signed BIP-340 over the NIP-01 event id. A reaction signs only "✅ + e-tag of message X", so it is a function of the message the body posted. |
| `amq-owner-sign` | The owner's terminal, held in memory only (`cmd/amq-owner-sign/main.go:1-8`, reads nsec at `:319-355`) | Only the NIP-OA grant preimages that `amq-remote share` printed. Each must equal `bodykey.AuthTag{Conditions}.Preimage(body)` or it is refused (`:276-283`). Plus the kind 30177 policy event (`:290-297`). | **No.** It is fixed to grant and policy shapes. |
| Relay NIP-OA grant | Public signed tags in a bundle | The owner's authorization for the body key to post the given kinds under conditions | **No.** It is a capability grant, not a per-decision signature. |
| Link consent passkey (`amq.remote.link/1`) | A WebAuthn passkey on the owner's device, created by the third-party server, pinned by typed fingerprint (`README.md:386-392`) | An `amq.remote.consent/2` document, which is a `request.submit` with free `input.text`. ES256 or Ed25519 (`testdata/link/README.md:11-13,27-28,88-92`; sample `testdata/link/consent/document.json`). | Partly: the text is free, but the document is a **submit**, and the server composes it. A link "never answers the agent's prompts": `approve_tool` and `answer_question` are always false (`testdata/link/README.md:76-77`; `cmd/amq-remote/link.go:316`). |
| Link local passkey (`--consent local`) | Mac loopback page, Touch ID (`README.md:409-418`) | SHA-256(`"amq.remote.link/1\0confirm\0"` + digest of a held consent doc) (`testdata/link/README.md:118-125`) | No. It confirms a held submit only. |

So the keys that **could** sign something that is a function of (gate id, action class, target digest) are:

1. **The owner nostr key**, through a typed kind 9 whose text contains the tuple, or a ✅ on a body message whose text contains it.
2. **The link consent passkey**, through a submit whose text contains it. amq-remote verifies that one; I did not confirm whether the
   assertion is forwarded to the target, and the README implies only the submit text is delivered.

`amq-owner-sign` and the NIP-OA grant cannot sign it.

### A3. amq-squad operatorauth today

- Signer: an `AuthorizationSigner` interface. The only implementation is `fileEd25519Signer`, loaded from a single **unencrypted PKCS#8
  PRIVATE KEY PEM** block (`internal/operatorauth/signer.go:13-61`, PEM check at `:37-39`). `const authorizationSignatureAlgorithm = "ed25519"`
  (`signer.go:13`). The key id is `"ed25519-sha256:"+hex(sha256(pub))` (`signer.go:26-28`).
- Trust store: entries with `algorithm`, raw public key and `status` (`trusted` or `revoked`). Any algorithm other than ed25519 is rejected
  at load (`trust.go:54-61`). Verification is `ed25519.Verify` (`trust.go:84-92`).
- Envelope: `VerifyAuthorizationEnvelope` hard-rejects `Signature.Algorithm != "ed25519"` (`envelope.go:286`). The payload already carries
  `Gate`, `GateKind`, `Action`, `Target`, `QuestionMessageID`, `AnswerMessageID`, `Actor`, `Issuer`, `Evidence[]`, `EvidenceSHA256`
  (`envelope.go:59-83`). The canonical form is LPBE (`canonical.go:37`).
- **Yes, verifying a nostr BIP-340 signature needs a new scheme** in signer, trust and envelope: secp256k1 x-only pubkey, a key id such as
  `bip340-sha256:`, and verification over the NIP-01 event id rather than the LPBE payload bytes. The owner's Buzz client signs a NIP-01
  serialization, never our canonical payload. So the honest shape is: **the operatorauth envelope carries the raw owner event as
  `Evidence`**, the Go core verifies BIP-340 plus content binding, and the envelope's own signature (if any) is by a local issuer key that
  attests "I verified", not by the owner. Alternatively, add a second verification mode where the "signature" *is* the embedded nostr
  event.

### A4. Candidate designs for criterion 1 (ranked)

**Design A: owner-typed kind 9 over the gate text, fetched by event id. Zero upstream change. Rank 1.**
- Key: the owner nostr key in Buzz, the same npub already pinned as the share owner.
- What signs: the owner types `approve <gate-id> <class> <digest-prefix≥16 hex>` (or `deny …`) in the Buzz DM. The Buzz client signs a
  kind 9 event.
- What amq-remote carries: with `attach --self` (amq-acp binding mode), Buzz DMs arrive as AMQ messages from `buzz`, and the handle's
  `amq reply` posts back (`README.md:557-562`). amq-acp journals the **nostr event id** for each delivered message at
  `agents/<me>/outbox/acp-events/<event_id>.json` → `{event_id, message_id, …}` (`internal/acp/event.go:28-35,98-100`). The gate question
  goes out as the operator handle's reply, so the body key signs it.
- What the Go core verifies: it resolves the AMQ message to its event id, **fetches that event from the relay by id**, checks `CheckID` and
  BIP-340 under the pinned owner npub, `kind == 9`, `h` == DM channel, content parses to exactly (gate id, class, full-digest prefix match),
  and `created_at` is inside the gate window. Optionally it checks that the event replies to the body's gate message. It then writes an
  operatorauth envelope with the raw event as `Evidence`.
- Upstream change: none. amq-squad needs a nostr client: an id-addressed REQ and NIP-42 sign-in. Sign-in needs a key that the relay
  admits to the DM group. Either amq-squad reads the share's enrolled body secret, as `cmd/amq-remote/claude_approval.go:51-55` does via
  `relayConfigFor` (an internal amq-remote layout, so fragile), or amq-squad gets its own member key. **Verify in the spike** that a non-body
  key can read the NIP-29 DM group.
- Attacks it does not stop: (1) **the owner approving a digest they did not check.** A model controls the gate text the owner reads, so
  the owner sees whatever summary the lead wrote; only the digest is bound. Mitigation: the Go core, not a model, renders the gate
  message, and the PR list ships with it. (2) A **compromised owner Buzz device** signs anything. (3) Relay withholding is irrelevant here:
  there is no history check, because the owner's own text is the payload.

**Design B: ride the Claude PermissionRequest hook evidence. Zero upstream change, fragile. Rank 3.**
- Key: the owner nostr key, used through a ✅ reaction.
- What signs: the owner ✅s a body-posted approval message. That message shows a Bash call
  `amq-squad gate answer --gate G --class C --digest D` issued by a Claude seat whose session a relay share with `approve` submitted to.
- What amq-remote carries: `ApproveEvidence` (reaction + message) into `~/.claude/sessions/amq-approve/<sid>/answers/<iid>.json`
  (`approve.go:25-37,86-94`).
- What the Go core verifies: the gate command reads that answer file and re-runs `VerifyApproveEvidence`-equivalent checks plus the
  history check (`approve_proof.go:104-153`; `CheckHistory` from `:155`) for its own argv.
- Upstream change: none. But it depends on an **internal file schema** (`amq-claude-approval/v1`), on Claude-only targets, and on the
  "Bash whole" rule. A model can issue the gate command for a gate the owner did not mean, and the owner must read the digest in the
  command preview.
- Attacks it does not stop: the pin is defeated by `bypassPermissions`, a `permissions.allow` rule that allows every Bash command, or
  "allow editing .claude" (`README.md:837-841`). It also does not stop a same-user process replacing the `amq-remote` binary
  (`README.md:843-846`). The relay is trusted for edit and deletion history.

**Design C: generic owner-signed answer passthrough. Needs an upstream change. Rank 2, the long-term target.**
- Key: the owner nostr key (today). A link passkey could be added later.
- What signs: a ✅ on, or a typed reply to, a **gate message** that amq-remote posts with the body key, whose content is the canonical
  gate text that amq-squad supplied.
- What amq-remote carries: an upstream ask to make the in-process `Evidence` (`protocol.go:204-208`) available to any `EvidenceResponder`,
  and add a "gate" interaction kind on the attach --self binding, or forward the raw owner event in the AMQ message that amq-acp delivers
  (a header such as `nostr_event`).
- What the Go core verifies: BIP-340 + binding, as in Design A, but without its own relay client. It also verifies the body-signed gate
  message, which gives the Claude-path guarantee that the owner reacted to the exact text the core rendered.
- Upstream change: yes, roughly (a) forward raw signed events through amq-acp delivery, and (b) document it as a contract.
- Attacks it does not stop: a compromised owner device, and a same-user process with the body secret posting a look-alike gate message.
  The owner still has to ✅ it, so this is a phishing attack, not a forgery.

Not recommended: **the link passkey.** `approve_tool` is always false on links, the third-party server composes the document, and WebAuthn
shows the owner only the RP, not the text. **A local Ed25519 owner key behind Touch ID on the Mac** is not remote, so it does not satisfy
"delivered through amq-remote". It is a valid fallback for a desk-only gate.

Buildable with zero upstream change: **A** and **B**. A is the one to spike.

---

## B. Launch API for seats

### B1. Wrapper argv and env_overlay
- `Wrapper{Executable, Args}` is **per participant** (`internal/launch/wrapper.go:12-15`; `PrepareParticipant.Wrapper`
  `internal/launch/prepare.go:131-145`). Args may differ per participant (docs example `docs/launch-api.md:133`). The wrapper must be an
  absolute clean path with no empty or NUL args (`wrapper.go:32-48`), must not be contained in the project (`prepare.go:1007`), and is
  identity-probed into subject schema 2 (`prepare.go:1010-1023`).
- Plan argv = `[wrapper.exe, wrapper.args..., provider.exe, provider.args...]` (`wrapper.go:91-112`). The provider is located at index
  `len(wrapper.Args)+1` (`wrapper.go:114-123`).
- Exec line (tmux): `cd <cwd> && env K=V... AMQ_<launch-nonce>=… amq coop exec --root <root> --me <handle> [opts] <wrapper.exe> -- <wrapper.args...> <provider.exe> <provider.args...>`
  (`internal/launch/tmux.go:756-765`; `commandLine` `internal/launch/commands.go:160-180`; `coopExecArgv` `commands.go:113-122`).
  **env_overlay is applied as an `env` prefix in the tmux command**, so it is in the environment of `amq coop exec`, and the wrapper and
  provider inherit it. cmux, Ghostty and plan-only use the same `cloneEnv(agent.EnvOverlay)` (`cmux.go:705`, `ghostty.go:597`, `commands.go:75`).
- Allowlist confirmed as exactly `COLORTERM, LANG, LC_ALL, NO_COLOR (1|true), TERM`, with values matching `^[A-Za-z0-9_.@+:-]{1,128}$`
  (`internal/launch/adapter_env.go:14-24`). Any other key fails `validateCommittedEnv` (`adapter_env.go:26-37`). Seat env (AMQ_SQUAD_*)
  must therefore be set **by the wrapper**, not by the launch request. That matches plan 4.3.

### B2. Codex version != 0.147.0
- `Capabilities`: on a version mismatch, `Available=true, Fresh=true, Resume=true, Capture=false, Reason="capture_version_unsupported"`
  (`internal/launch/adapter_codex.go:56-69`; pin at `:13`).
- **Prepare does not consult Capabilities.** `prepareParticipant` calls `PlanFresh` or `PlanResume` directly and reports
  `planned_outcome` "fresh" or "resume" (`prepare.go:1059-1075`). The only `Capabilities` callers are `reconcile.go:636,986` and
  `cli/setup.go:519`. Prepare therefore **looks fine**.
- **Apply refuses the fresh launch.** `freshAllowed := capabilities.Fresh && (mode != Capture || capabilities.Capture)` → false for
  Codex (`internal/launch/reconcile.go:1032`). The seat gets `Code 6, DispositionActionRequired, Reason "capture_version_unsupported"`
  (`reconcile.go:1110-1121`).
- It is **not** "launch with Capture=false". Exception: **resume of an already captured Codex conversation is still allowed**, because
  `capabilities.Resume` is true (`reconcile.go:1054-1062,1069-1077`).
- The result says: `ApplyResult.Outcome = action_required`, `ReasonCode` = the first failing agent's reason, here
  `capture_version_unsupported` (`internal/launch/apply.go:356-363,417-421`).
- Other seats: `Reconcile` short-circuits only when **nothing** is planned (`reconcile.go:188-198`), so the remaining seats appear to
  proceed. I did not trace the launch tail to confirm a partial launch, so verify in the spike.
- Consequence for 3.0: every codex-cli upgrade away from 0.147.0 breaks fresh Codex seats until AMQ bumps the pin. Add a doctor check, or
  pin the codex-cli version.

### B3. Claude `--model` / `--effort`
- Both are admitted committed args today: `--model` (safeArgumentValue) and `--effort` (`low|medium|high|xhigh|max`)
  (`internal/launch/adapter_claude.go:124-136`). The same rules also admit `--permission-mode`, `--allowedTools` (scoped grammar), `-n/--name`.
- Codex admits `--model`, `--sandbox read-only|workspace-write`, `--ask-for-approval untrusted|on-request`, and `-c model_reasoning_effort=…`
  / `-c approvals_reviewer=…` (`adapter_codex.go:162-187`).
- **Plan upstream ask 4 is therefore documentation and stability, not grammar.** The gap is that `docs/launch-api.md:78` lists Claude's
  scoped `--allowedTools` and `-n/--name` but does not advertise `--model` or `--effort` as a stable form. Compare `grammar_version` in
  `PreviewV1.capabilities` (`docs/launch-api.md:40-45`) instead of relying on the doc.

### B4. Passing role or seat handle to the seat
- Request fields per participant: `handle, runnable, provider/executable, args, bypass args, cwd, env_overlay, resume_policy, execution,
  initial_input, on_live, wrapper` (`prepare.go:131-145`).
- `caller_context` is opaque and evidence-only. It enters subject schema 2 and immutable evidence, "never the plan or trust digest", and
  Apply echoes it (`docs/launch-api.md:209-215`). The seat never sees it.
- The seat receives the following. **The handle** arrives implicitly: `amq coop exec --me <handle>` sets `AM_ME` and `AM_ROOT`
  (`commands.go:114`; `docs/pi-bridge-protocol.md:16-19`), so the wrapper can derive the seat from `AM_ME` with no wrapper args at all.
  The other channels are wrapper args, `initial_input` (argument carrier only; stdin and file are typed refusals, "including when a
  wrapper is present"; `docs/launch-api.md:216-219`), and the 5 env keys.
- There is no other carrier for role text. Writing the role to the seat directory is the wrapper's job, as plan 4.3 already says.

---

## C. pi bridge

- **What revision 4 adds to revision 3** (`docs/pi-bridge-protocol.md:265-300,345-370`). Nothing in revision 3 changes, and the protocol
  string stays `amq:pi-bridge:v1`. Revision 4 adds:
  - (a) A **required `session_id`** in `bridge.liveness`: pi's `sessionManager.getSessionId()`, stable across reload and compaction, new on
    `/new` and fork. A share pins it as the native session id (`:279-287`).
  - (b) **Tool approval**: `interaction` and `interaction_resolved` lines in `events/<ref>.jsonl`, and the adapter writes
    `answers/<sha256(ref\0iid)>.json` with `{protocol, ref, interaction_id, manifest_hash, option, at}` (`:345-370`). The extension must hold
    the tool call until the first answer arrives, from a local face or from `answers/` (`:272-273`).
  - Adapter side: `ApproveBridgeRevision = 4` (`internal/remote/pi/deliver.go:75`). Approval is offered only while the live record shows ≥4
    (`pi.go:1452`). Owner DM commands on pi need `session_id` (`cmd/amq-remote/README.md:325-330`).
- **Reference extension**: `export const BRIDGE_REVISION = 3` (`integrations/pi/amq-bridge.ts:14`), accepting 3 to 3 (`:18,367-372`). It
  already **publishes `session_id`**, the revision 4 identity part (`:420,540-541`), but not approvals. Reason: "Stock pi exposes no
  approval that an extension can answer from outside, so the reference extension … is revision 3" (`docs/pi-bridge-protocol.md:272-275`).
  **Risk for plan 16.3:** our pi package must implement the hold-and-answer seam itself, inside a pi extension. Stock pi gives no hook for
  it, so check in the spike that a pi extension can block a tool call pending an external file.
- **Is a pi session reachable without the launch API? Yes.** A `pi` target config is just `{handle, upgrade_hint?}`, plus the existing
  `agents/<handle>/extensions/pi-bridge/` directory under the root (`cmd/amq-remote/README.md:99`). The extension activates from
  `AM_ROOT`/`AM_ME`, which `amq coop exec` sets (`docs/pi-bridge-protocol.md:16-19`). `attach --self --native` finds a pi chat through
  the pid and session id in `bridge.liveness` (`README.md:564-568`).
  Limits: Inspect and Submit only, submit evidence `submitted` (so the share needs `min_evidence submitted`), no cancel, no activity
  export (`README.md:99,319,336-339`), and ApproveTool only at revision ≥4. A pi session must be driven by the endpoint of its own AMQ
  root (`README.md:400-402`).
