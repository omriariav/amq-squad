# amq-squad 3.0: upstream asks for avivsinai/agent-message-queue

Task S8 of the phase 1 spike. Draft only. Nothing here has been filed.
Verified against AMQ `v0.102.0` (commit 44d6ce2) and the open issue and PR
lists on 2026-10-21. File paths in the bodies are relative to the AMQ
repository root. "Layer-neutral" means the change fits
`docs/adr-layer-extensions.md`: AMQ stays team-unaware and keeps owning
transport and launch, and a layer only consumes a documented contract.

Open upstream at the time of writing: issues #1107, #1046, #891; PRs
#1108, #1104, #1101. Only #1107 and #1108 touch these asks.

| Ask | Title | Status at v0.102.0 | Recommendation |
| --- | --- | --- | --- |
| 1 | pi adapter in the launch API | not shipped | file |
| 2 | Provision under a project with no .amqrc | partly shipped | skip, or file as docs fix |
| 3 | Non-interactive consent by digest | shipped | do not file |
| 4 | Advertise admitted Claude and Codex argument forms | not shipped | file |
| 5 | Codex argv grammar v2 | not shipped | file after spike S4 |
| 6 | iTerm2 launcher backend | not shipped | do not file |
| 7 | Notify chaining for Codex | partly shipped | file, narrowed |
| 8 | Forward the owner-signed event | not shipped | file |
| 9 | Codex capture version floor | filed (#1107, PR #1108) | no action |
| 10 | Phone-side signing of the share grant | not shipped | file after owner decision |
| 11 | Event id escaping of C0 controls differs from NIP-01 | not shipped | file after reviewer confirms |

---

## Ask 1

**Title:** Add a pi adapter to the launch API

**Body**

### Problem
`amq launch` prepares and applies Claude, Codex, Cursor, and Grok seats only.
pi seats start through `amq coop exec` by hand. They get no digest-gated
apply, no resume, no trust record, and no launch liveness.

### What amq-squad needs
amq-squad 3.0 compiles a roster into one launch request. pi is one of its
runtimes. It wants the same Prepare and Apply path for a pi seat as for the
other four.

### Evidence (v0.102.0)
- `docs/launch-api.md:171`: "Pi remains direct-only because it has no launch
  API adapter."
- `internal/launch/adapter.go:16-19,28-33`: the provider set is claude, codex,
  cursor-agent, grok. Any other executable maps to "".
- A pi identity source already exists. The bridge publishes `session_id` in
  `bridge.liveness` (`docs/pi-bridge-protocol.md:277-288`,
  `integrations/pi/amq-bridge.ts:420`).

### Proposed change
Add a `pi` provider with `StaticInputCapabilities`, a deny-by-default argv
grammar, and `Capabilities`. Take identity from the bridge `session_id`.
Resume by that id. Advertise the grammar version in `PreviewV1.capabilities`.
The maintainer picks the exact argv forms. Please also re-verify the pi bridge
against the current package `@earendil-works/pi-coding-agent` 1.3.0; the
evidence pin in `docs/remote-compat.md` is pi 0.85.1.

### Revision 4 feedback
pi 1.3.0 emits `tool_execution_start` before the `tool_call` hook runs. A call
an extension holds for approval therefore shows as started in the activity
stream, though it has not executed. The bridge protocol should state how a
held call is represented.

### Why layer-neutral
It adds a provider to AMQ's own launch seam. It names no team, role, or layer
file. Every caller of the launch API gets pi seats.

**Status at v0.102.0:** not shipped
**Duplicates:** none open. Closed #480 and #557 built the launch API; neither
covers pi.

---

## Ask 2

**Title:** Document, or plan, session provisioning under a project with no .amqrc

**Body**

### Problem
`docs/cli.md` says `amq launch` never creates unknown sessions. That is true
only for the committed-config path. The launch API provisions a new session
today. One case is still refused: a project root with no `.amqrc`.

### What amq-squad needs
In auto mode a fleet operator starts squads in repositories it has not
touched before. It wants one Prepare and Apply to create the session root,
not a separate setup step.

### Evidence (v0.102.0)
- `docs/cli.md:205`: "Unknown sessions are never created." This is the
  `--session` path. It misleads readers of the `--request` path.
- Checked with a v0.102.0 build. With `.amqrc` and an unknown session,
  `amq launch --request --prepare` returns `ready`. Apply returns
  `provisioned_no_runnable` and creates `.agent-mail/newsess`. Code:
  `internal/launch/prepare.go:893,941` ("intended-child").
- With `target.base_root` set and a missing profile base, Prepare plans
  `create_base_root` (`internal/launch/prepare.go:237`,
  `docs/launch-api.md:196-199`).
- With no `.amqrc`: without `base_root`, Prepare fails with
  `stat delivery root ... no such file or directory`. With `base_root`, it
  returns `unsupported` / `base_root_unauthorized`
  (`internal/launch/base_root.go:19,41-48`).

### Proposed change
Option (a): add a planned write that creates the project `.amqrc`, shown in
Prepare and performed in Apply under the same exclusive-create rules.
Option (b): keep the refusal, fix the `docs/cli.md` sentence, and state in
`docs/launch-api.md` that the caller runs `amq init` first. Option (b) is
enough for us.

### Why layer-neutral
Both options use AMQ's own config file and launch plan. No team concept
enters.

**Status at v0.102.0:** partly shipped: unknown sessions and missing profile
base roots are provisioned through the launch API; a project root with no
`.amqrc` is refused
**Duplicates:** none open. Closed #557 (finding 1) and #648 added `base_root`.
Recommend skipping, or filing only option (b). amq-squad can run `amq init`
itself.

---

## Ask 3

**Title:** (not for filing) Non-interactive consent by digest on the launch path

**Body**

### Finding
This already ships. A caller that holds the Prepare result supplies one
decision per required action in Apply. No prompt is involved.

### Evidence (v0.102.0)
- `internal/launch/prepare.go:218-224,700-711`: required action kinds
  (`trust_confirmation`, `stale_conversation_decision`,
  `rebind_confirmation`, `unsupported_capability_ack`), with
  `AllowedDecisions: trust_exact_subject, deny`.
- `launchapi/types.go:340-352`: `DecisionV1{action_id, choice}` and
  `ApplyRequestV1{subject_digest, decisions}`.
- `internal/launch/apply.go:668`: `trust_exact_subject` is honored.
- `docs/launch-api.md:182-186,253-280`: Apply "recomputes the subject" and
  takes "one explicit decision for each required action."
- `docs/cli.md:205` ("Non-interactive trust ... exit 6") describes the
  committed-config CLI path only.

### What remains for amq-squad
Nothing upstream. amq-squad supplies decisions only after the owner-signed
gate (ask 8 path) and treats `subject_changed` as a re-gate.

**Status at v0.102.0:** shipped: `launchapi/types.go:340`,
`internal/launch/apply.go:668`; do not file
**Duplicates:** none

---

## Ask 4

**Title:** Advertise admitted --model/--effort (Claude) and --model/--sandbox/--ask-for-approval (Codex) in AllowedArgumentForms and docs

**Body**

### Problem
The launch grammar already admits Claude `--model` and `--effort`. It does
not advertise them. A caller reading `PreviewV1.capabilities` or
`docs/launch-api.md` cannot tell they are stable, so it cannot rely on them.

### What amq-squad needs
Per-seat model and effort travel as provider arguments in the launch
request. amq-squad needs to detect support by capability, not by trial.

### Evidence (v0.102.0)
- `internal/launch/adapter_claude.go:124-136`: `--model` and `--effort`
  (`low|medium|high|xhigh|max`) are admitted.
- `internal/launch/adapter.go:80-84`: Claude `AllowedArgumentForms` lists only
  `--allowedTools`, `-n`, `--name`.
- `docs/launch-api.md:70-76` documents `--allowedTools` and names. The words
  `--model` and `--effort` do not appear in the file.
- Same gap for Codex. `internal/launch/adapter_codex.go:162-171` admits
  `--model`, `--sandbox`, `--ask-for-approval`, `--search`, `--no-alt-screen`,
  but `adapter.go:85-96` lists only `-c`.

### Proposed change
Add `--model` and `--effort` to the Claude `AllowedArgumentForms`. Add
`--model`, `--sandbox`, `--ask-for-approval` to Codex's. Document them in
`docs/launch-api.md` with their value rules. The grammar does not change, so
`GrammarVersion` stays.

### Why layer-neutral
It documents what AMQ already accepts. Every launch caller benefits.

**Status at v0.102.0:** not shipped
**Duplicates:** none open. Closed #557 (finding 5) reported the admission
gap. It was fixed; the advertising was not.

---

## Ask 5

**Title:** Codex launch grammar v2: admit --add-dir, --profile, and more -c keys

**Body**

### Problem
Codex grammar is version 1. The admitted forms are `-c` (two keys),
`--model`, `--sandbox`, `--ask-for-approval`, `--search`, `--no-alt-screen`.
A seat that needs an extra writable path or a named profile cannot launch
through the API. Strict decode rejects the request before Prepare.

### What amq-squad needs
Seats work in git worktrees. Under `--sandbox workspace-write`, a worktree's
shared git directory can sit outside the writable root, so a seat cannot
commit. amq-squad needs `--add-dir`. Seat configuration may also want a named
`--profile`. The exact scope is a draft. Confirm it after spike S4.

### Evidence (v0.102.0)
- `internal/launch/adapter_codex.go:162-171`: the full admitted list.
- `internal/launch/adapter_codex.go:185-186`: only `model_reasoning_effort`
  and `approvals_reviewer` are admitted as `-c` keys.
- `internal/launch/adapter.go:85-96`: `GrammarVersion: 1`.
- codex-cli 0.154.0 `--help` lists `--add-dir <DIR>`, `-p/--profile`, and
  `-C/--cd`.

### Proposed change
Grammar v2: admit `--add-dir <abs-path>` (clean, absolute, resolved,
repeatable), `--profile <name>`, and a short allowlist of further `-c` keys
chosen with the maintainer. Bump `GrammarVersion` to 2. Stay deny-by-default.
Paths enter the plan and trust digests.

### Why layer-neutral
A provider grammar is AMQ's launch concern. The change names no layer.

**Status at v0.102.0:** not shipped
**Duplicates:** none open. Closed #648 (allowedTools, approvals_reviewer) set
the pattern.

---

## Ask 6

**Title:** Add an iTerm2 launcher backend

**Body**

### Problem
Registered launcher backends are commands, tmux, cmux, and ghostty. There is
no iTerm2 backend. amq-squad 3.0 uses tmux, with `tmux -CC` for iTerm2
users, so it works without this.

### Evidence (v0.102.0)
- `internal/launch/backend.go:258-261`: the backend map.
- `docs/launch-api.md:164-169`: the list of registered backends.

### Proposed change
An iTerm2 `Backend` that uses the existing placement model. Optional.

### Why layer-neutral
Terminal placement is launch UX that AMQ already owns.

**Status at v0.102.0:** not shipped
**Duplicates:** none found by keyword search. Closed #281 matched "iterm"
in passing only.
Recommend: do not file. The plan marks it optional, `tmux -CC` covers the
use case, and it asks the maintainer to own a macOS-only backend.

---

## Ask 7

**Title:** Let AMQ's Codex notify hook forward to more than one command

**Body**

### Problem
Codex has one `notify` slot. AMQ takes it for identity capture, then forwards
the payload to the operator's `notify` command. It forwards to exactly one
command, read from `CODEX_HOME/config.toml`. A second consumer cannot
register without replacing the first.

### What amq-squad needs
Codex turn evidence for routing reports. It must run after AMQ's capture and
must not displace the user's own notify command. On the owner's Mac, `notify`
already points at another client.

### Evidence (v0.102.0)
- `docs/launch-api.md:149-158`: AMQ forwards "the identical payload to the
  operator's configured Codex notify command after publication."
- `internal/cli/codex_notify.go:53-58`: forward after `RecordCodexNotify`.
  A failure is logged, not fatal.
- `internal/cli/codex_notify.go:18,66-99,120-136`: reads `notify` from
  `CODEX_HOME/config.toml` and runs that one argv with a 10 s timeout.

### Proposed change
Forward to an ordered list. Two options:
(a) Read `notify_chain = [[...], [...]]` from the same `CODEX_HOME/config.toml`
that AMQ already reads. Run `notify` first, then each entry in order. Same
payload, same timeout, same no-fail rule. Recommended.
(b) A documented launch field that carries extra commands.
Document the order in `docs/launch-api.md`.

### Why layer-neutral
AMQ already forwards. This widens one command to a list. Option (a) is
configuration in the operator's own file: no layer registers anything with
AMQ, so it sits inside the ADR, which says AMQ will not "run extension hooks"
(`docs/adr-layer-extensions.md:40-41`). Option (b) must stay a generic
command list, not a layer hook.

**Status at v0.102.0:** partly shipped: single-command forward to the
operator's notify (`internal/cli/codex_notify.go:66`); no chaining
**Duplicates:** none open. Closed #334 (notifier-provider protocol) is
adjacent but covers wake notifiers.

---

## Ask 8

**Title:** Forward the raw owner-signed event to gate answers through the attach binding

**Body**

### Problem
amq-remote receives the owner's signed Nostr event (a kind 7 reaction or a
kind 9 message). Only the Claude hook path receives it. Codex and pi get a
bare option string that amq-remote decided. A layer cannot verify that the
owner, not a model, answered.

### What amq-squad needs
Owner-signed gate answers over (gate id, action class, target digest). The
layer's Go core verifies the BIP-340 signature and stores the event as
evidence. No model may relay an answer.

### Evidence (v0.102.0)
- `internal/remote/protocol/protocol.go:204-208`: `Command.Evidence` is
  `json:"-"`, in-process only, "grants nothing by itself."
- `internal/remote/buzzio/carrier.go:1036-1048`: evidence is attached only
  for an approve gesture.
- `internal/remote/core/endpoint.go:1373`: used only if the attachment is an
  `EvidenceResponder` (`internal/remote/core/attachment.go:184-191`).
- The only implementer is Claude (`internal/remote/claude/approvals.go:521`).
  Codex sends `{"decision": option}`
  (`internal/remote/codex/attachment.go:1038`). pi writes an unsigned
  `answerRecord`; `subject` is reserved and unchecked
  (`internal/remote/pi/deliver.go:166-174`,
  `docs/pi-bridge-protocol.md:367`).
- `internal/acp/event.go:28-35` journals the Nostr event id per delivered
  message, not the event.

### Proposed change
For a "gate" interaction kind on the attach `--self` binding, deliver the raw
signed event, unmodified, in the AMQ message that amq-acp hands to the
handle. Use a documented header such as `nostr_event`. Document it as a
contract. AMQ verifies nothing on the layer's behalf.

What the layer verifies, so AMQ's part stays small: the event pubkey against
a pinned owner npub, the BIP-340 signature over the event id, and the gate
id, action class and digest inside the content.

This is parity, not a new concept: the Claude hook path already receives
this evidence (`internal/remote/claude/approvals.go:521`). The ask extends
the same bytes to the attach binding. Line numbers are from v0.102.0.

### Why layer-neutral
It forwards bytes the owner signed. It adds no team concept. Any layer can
verify them.

**Status at v0.102.0:** not shipped
**Duplicates:** none open. Closed #1024 is about share reconciliation.
Design context: `docs/v3-research/gate-transport.md` (design C).

---

## Ask 9

**Title:** Report capture_version_unsupported at Prepare; treat the Codex capture version as a floor

**Status at v0.102.0:** filed, not shipped. Do not redraft.
**Duplicates:** issue #1107 (open, no comments, updated 2026-10-20) and PR
#1108 (open, ready for review, mergeable, review required, updated
2026-10-20). The pin is still exact: `internal/launch/adapter_codex.go:13,65`
(`codexCaptureVersion = "0.147.0"`). PR #1104 (release v0.102.1) does not
carry the fix.

---

## Ask 10

**Title:** Let the owner sign a strict share's grants on the phone, without exporting the nsec

**Status:** draft. File only after the owner decides (see
`docs/v3-spike/S1-report.md` section 8.1). If the owner agrees to a one-time
hidden-prompt export on the Mac, do not file.

**Body**

### Problem
Enrolling a strict relay share needs the owner's nsec in a terminal.
`amq-owner-sign` reads it with a hidden prompt and signs the per-kind NIP-OA
grants, the kind 30177 policy, and one NIP-42 AUTH event. An owner whose key
lives only in the Buzz phone app must export it to the Mac to enroll, and
again at every renewal.

### What amq-squad needs
amq-squad wants owner-signed typed gate answers, where the owner key never
leaves the owner's device. The answers already work this way: Buzz signs a
kind 9 on the phone. Only enrollment needs the key elsewhere.

### Evidence (v0.102.0)
- `cmd/amq-owner-sign/main.go:140-176`: the key is read once. It signs every
  grant through `bodykey.SignAuthTag` (`:148`), the policy (`:155`,
  `:290-297`), and the AUTH used to publish it (`:311`).
- `internal/remote/bodykey/bodykey.go:280-309`: a grant is a BIP-340
  signature over sha256(`nostr:agent-auth:<body>:<conditions>`). That is a
  raw hash, not a NIP-01 event, so a stock NIP-46 `sign_event` cannot produce
  it.
- No NIP-46, bunker, nostrconnect or QR path exists in the source. The
  vendored fiatjaf.com/nostr ships `nip46`, but it is unused.
- Buzz side (block/buzz main at ca351142, 2026-10-21):
  - mobile only verifies NIP-OA (`mobile/lib/shared/crypto/nip_oa.dart:79`);
  - signing exists in Rust (`crates/buzz-sdk/src/nip_oa.rs`, and Desktop's
    `crates/buzz-core/src/private_managed_agent.rs`);
  - QR pairing (`crates/buzz-core/src/pairing/NIP-AB.md`) moves the key
    itself, or bootstraps NIP-46.

### Proposed change
Two options, in order of preference:

1. **A Buzz-side grant request.** `amq-remote share` prints a QR or link that
   carries the body pubkey and the per-kind conditions. The Buzz phone app
   shows them, signs the NIP-OA preimages and the policy, and returns the
   bundle to the share over the relay, for example NIP-44 encrypted to the
   body. This needs work in Buzz as well as in AMQ.
2. **A remote-signer path in `amq-owner-sign`.** Add NIP-46 support, with a
   documented method for signing a NIP-OA preimage. A signer that refuses
   that method cannot enroll a share.

In both, the phone shows the body pubkey and each kind's expiry before it
signs, which is the same check `amq-owner-sign` asks for today.

### Why layer-neutral
It changes only how the owner produces signatures AMQ already defines. No team
or gate concept enters AMQ.

**Duplicates:** none open.

---

## Ask 11

**Title:** Event id computation escapes C0 controls differently from NIP-01

**Status:** draft. File after the reviewer confirms the vector.

**Body**

### Problem
amq-remote v0.102.0 hashes events with fiatjaf.com/nostr
v0.0.0-20260916040958-27e395a0f6e7. That library's id commitment escapes only
`"`, `\`, `\n`, `\r` and `\t`, and writes every other C0 control byte
verbatim (`event.go:73-87`, the `escTable` init). NIP-01 requires `\b` and `\f` to be
escaped. JSON.stringify, which clients such as nostr-tools hash, escapes `\b`
and `\f` and writes the other controls as `\u00XX`.

The same signed event therefore has two ids when its content or tags hold
`\b`, `\f`, or a C0 control other than `\t`, `\n` and `\r`. An event signed
by a client then fails `CheckID` in amq-remote, and the reverse also happens.
An approval path is the place where that matters.

### Test vector
- pubkey `7e7e9c42a91bfef19fa929e5fda1b72e0ebc1a4c1141673e2794234d86addf4e`
- `created_at` 1700000000, kind 9, tags `[["h","s1-vector"]]`
- the id does not depend on the signature

| content | NIP-01 reference id (JSON.stringify) | fiatjaf.com/nostr `GetID` |
| --- | --- | --- |
| `"a\fb"` | `c1ce746eee1ef178e988713471e2a1062bed99175770cbb2f29cf7382807278c` | `d559be4e02658fe077bc203d34aa73356a5e54854a2a8696cd3c760f01b55174` |
| `"a\bb"` | `a7d8977f8f6a1240d1bc7542ffc9514b8988d1cb7f8418d091c72f013cd7fe22` | `60d419d2c4af0f1eaeaf30cd6bb6f2d4628c391bcf0f8efcb12f43bad7e967e3` |
| `"a\u0001b"` | `37847e11712fa7d0d1197c382c92b79106f736520ac3ccc9ad367a85105db0dd` | `6dde4a54306107d2ae43334cb097cbc4cbeab54a0e5dd167af5cb23cff7d462e` |
| `"a\nb"` (control) | `20123c9e3cb553383e7040d2375ce679933681b53c432a3538f837e4609317c7` | `20123c9e3cb553383e7040d2375ce679933681b53c432a3538f837e4609317c7` |

The reference commitment for the first row is
`[0,"7e7e…df4e",1700000000,9,[["h","s1-vector"]],"a\fb"]`, with `\f` as
the two characters backslash and f. The library writes byte 0x0C there.

### Proposed change
Escape `\b` and `\f` as NIP-01 says, and the remaining C0 controls as
`\u00XX` (lowercase hex, as JSON.stringify writes them). Do this in the id
commitment, or refuse such events before `CheckID`. The fix belongs upstream
in fiatjaf.com/nostr. AMQ can pin a fixed version, or add a guard.

### Why layer-neutral
It is a correctness fix in event id computation. It adds no concept.

**amq-squad's own position, which does not change:** the Go core's verifier
refuses any event with such a byte in content or tags, before it computes an
id (`spike/gateverify/nostr.go`, `ambiguousControl`). This rejects compliant
events with control characters in tags. Real Buzz behaviour is untested, and
ask 11 stands. The rule stays even after an upstream fix, because other
relays and clients may still differ.

**Duplicates:** none open.
