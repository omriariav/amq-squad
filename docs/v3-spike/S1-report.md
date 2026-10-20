VERIFIER READY, REAL-KEY RUN PENDING

# S1 spike report: owner-signed gate answer over amq-remote, verified in Go

Date: 2026-10-21. Owner: security-dev. Branch `spike/s1-gate` (from `v3`).
Host: amq, amq-remote, amq-owner-sign, amq-acp 0.102.0; Go 1.27.1. Upstream
read at agent-message-queue tag v0.102.0 (commit 44d6ce22), so the paths
below are v0.102.0 paths. The plan cites v0.98.0.

**Exit criterion 1 is not claimed.** Every run here used throwaway keys.
Criterion 1 passes only after `realkey-run.sh` prints PASS with the owner's
real Buzz npub, a real Buzz relay, and a real kind 9 event typed on the
owner's phone.

## 1. What was built

`spike/gateverify/` is a Go module of its own
(`github.com/omriariav/amq-squad/v2/spike/gateverify`). The main module is
untouched: `go list ./...` from the repo root lists no spike package, and
there is no diff under `internal/`, `go.mod` or `go.sum`. Because the
spike's module path sits under the main module's path, Go lets it import
`internal/operatorauth` as is, so the envelopes below are real operatorauth
envelopes, made and verified by the unchanged package.

| File | Role |
| --- | --- |
| `gate.go` | `render`: gate id, class, target digest, ordered PR numbers. The digest covers repo, base SHA and the ordered (PR, head SHA) pairs (R34). The text never shows the repo, SHAs, paths or code. |
| `nostr.go`, `npub.go` | Strict NIP-01 parsing, id serialization, BIP-340 verification, NIP-19 npub |
| `verify.go` | `verify`: pin, id, signature, kind 9, `h` channel, tuple, window, optional thread binding, single-use ledger. Each refusal returns a typed code. |
| `relay.go`, `share.go` | `fetch` (REQ `{"ids":[id]}`), `publish`, `watch`, NIP-42 sign-in with the enrolled share body key (interim) |
| `resolve.go` | Maps a delivery to its event id through the three upstream journals (section 4) |
| `envelope.go` | operatorauth envelope (Ed25519 issuer) plus `bip340` evidence, and the phase 3 slot-in verifier |
| `e2e.go` | `e2e-synthetic` on a public relay |
| `enroll.sh`, `teardown.sh`, `realkey-run.sh` | Enrollment, cleanup, and the real-key run |
| `*_test.go` | 14 tests plus 21 negative subtests. They pass with `-race`. |

The owner answers with one line: `approve <gate-id> <class> <digest-prefix>`
(or `deny ...`). The prefix is at least 16 hex characters. Case is folded and
whitespace runs collapse, because phone keyboards capitalize the first word.
Any extra token is refused. Example gate text:

```
amq-squad gate s1-probe-01
class: merge
digest: sha256:227c75428f1dc3087e4f36158d0417ecdf216c2796754d56deeb380eec391f50
PRs in merge order: #101 #102
Answer with one line:
approve s1-probe-01 merge 227c75428f1dc308
deny s1-probe-01 merge 227c75428f1dc308
```

## 2. Synthetic results

### 2.1 Public relay end to end (throwaway owner key, synthetic content)

Each run does the following. A throwaway key signs a kind 9 approve over a
freshly rendered gate. The event is published, then fetched back by id over a
new connection. The core verifies it, builds the operatorauth envelope with
the scratch issuer key, and verifies the envelope again.

| Relay | Runs | Publish (OK) | Fetch by id | Verify + envelope | Round trip |
| --- | --- | --- | --- | --- | --- |
| wss://nos.lol, warm | 8 | 373 to 493 ms | 359 to 452 ms | 0.7 to 1.2 ms | median 812 ms (760 to 886) |
| wss://nos.lol, first connection | 1 | 2063 ms | 363 ms | under 1 ms | 2427 ms |
| wss://relay.damus.io | 2 | 654 / 2519 ms | 686 / 853 ms | 0.5 / 0.7 ms | 1341 / 3373 ms (warm / cold) |

Both relays rate-limited back-to-back dials (damus HTTP 503 after 2 runs,
nos.lol HTTP 429 after 6). The real-key path makes about 4 connections per
gate, well inside those limits. Reference event:
`4f26828f38750df8efe428872a4f6456830aec9073341fd43370a0c801a69377` on nos.lol
(throwaway npub `npub1wk0j0eajqw7t8hzhh4aha0zmjn7kzfrjm58cv25c4l8qr22l9tfqunsa2e`).

This is machine round-trip time. The criterion asks for phone to verified
envelope, which includes the owner reading and typing. `realkey-run.sh`
measures that time ("gate posted -> envelope verified") on the real run.

### 2.2 Full real-key path against a loopback Buzz-like relay

This run executed `realkey-run.sh` itself. It used the real `amq-remote serve`
and a scratch probe built on upstream's own `internal/relay/relaytest`. That
relay verifies NIP-42 AUTH and the owner's NIP-OA grant with
`internal/remote/bodykey`. The probe added relay-signed NIP-29 snapshots
(39002 members = owner + body, 39000 `private`, `t=dm`) and a simulated phone
that answered with the throwaway owner key 3 s after each gate appeared.

The result was **PASS** for gate `g-dry2-02`:

- The doctor showed relay_auth authenticated and the DM surface active.
- The gate was posted through the share.
- `serve` claimed the owner's kind 9 in its ingress ledger, and the event id
  was resolved from that ledger.
- The event was fetched by id after signing in with the body key, then
  verified with the thread binding required.
- The envelope was written and envelope-verify passed.
- Three negatives on the real event were rejected: replay
  (`already_consumed`), another gate id (`gate_mismatch`), and the body key
  as pin (`wrong_pubkey`).

From gate posted to verified envelope took 6181 ms, of which 3 s was the
simulated typing delay.

A separate probe used the share that `enroll.sh` had enrolled with the real
`amq-owner-sign`. Upstream's relaytest accepted gateverify's NIP-42 sign-in
3 out of 3 times, and the body-signed gate post carried its kind 9 NIP-OA tag.

This is not evidence about the Buzz relay itself. It shows only that our side
of the protocol matches what upstream's own code expects.

### 2.3 Interop with upstream's nostr library

I checked against fiatjaf.com/nostr v0.0.0-20260916040958-27e395a0f6e7, the
library amq-remote v0.102.0 verifies with. Events the library signed verify
in gateverify, and events gateverify signed pass the library's CheckID and
VerifySignature. The test content covered quotes, backslash, `\n \r \t`,
`<>&`, U+2028/2029, emoji and Hebrew.

**Finding.** The library escapes only `" \ \n \r \t` when it builds the id
commitment (`event.go` escTable). NIP-01 also escapes `\b` and `\f`, and
serde-style encoders escape the other C0 controls. The same signed event
therefore gets a different id in different libraries when its content or tags
hold `\b`, `\f`, or any other C0 control except `\t \n \r`. The verifier
refuses such events instead of picking one convention
(`nostr.go: ambiguousControl`). A gate answer is one line of ASCII, so this
costs nothing.

### 2.4 amq-acp journal: worked example (real amq-acp 0.102.0)

I drove `amq-acp` in cockpit mode (`AM_ME=buzz`, `AMQ_ACP_TO=operator`) over
stdio. The prompt carried `_meta.nostr.eventId` = the nos.lol event above.

- Delivered message
  `agents/operator/inbox/new/2026-10-20T21-35-04.803Z_pid1086_b9e4ccd4.md`,
  from `buzz`, labels `["acp","cockpit","nostr:4f26828f…9377"]`
  (`internal/acp/deliver.go:72-73`).
- Journal `agents/buzz/outbox/acp-events/4f26828f…9377.json`
  (`internal/acp/event.go:28-35,98`):
  `{"schema":1,"event_id":"4f26828f…9377","message_id":"2026-10-20T21-35-04.803Z_pid1086_b9e4ccd4","to":"operator","thread":"cockpit/session/acp_1eb1…","created":"2026-10-20T21:35:04.803798Z"}`
- `gateverify resolve --mode cockpit` returned `4f26828f…9377`. The id was
  then fetched from nos.lol and verified: `{"result":"approve"}`.

The resolver requires three things. The message must be from the amq-acp
sender handle, it must be addressed to the operator, and the journal entry
must match both its own file name and the message's `nostr:` label.

## 3. Negative tests (`go test`, all pass)

`TestNegative` covers the six required cases, plus the boundary cases the
verifier must also hold.

| Case | Result |
| --- | --- |
| Wrong pubkey: another key signs a valid tuple | rejected `wrong_pubkey` |
| Wrong pubkey: the share body key signs (a model via `amq reply`) | rejected `wrong_pubkey` |
| Altered content, id kept | rejected `id_mismatch` |
| Altered content, id recomputed | rejected `bad_signature` |
| Owner pubkey with a foreign signature | rejected `bad_signature` |
| Created before the window | rejected `outside_window` |
| Created after the window | rejected `outside_window` |
| Valid answer presented after the gate closed (core clock) | rejected `outside_window` |
| Replayed event for a different gate id | rejected `gate_mismatch` |
| Kind 1 instead of 9 | rejected `wrong_kind` |
| Wrong DM channel; two `h` tags | rejected `wrong_channel` (both) |
| Wrong class | rejected `class_mismatch` |
| Wrong digest prefix | rejected `digest_mismatch` |
| Prefix shorter than 16 hex; bare `yes`; extra token after the tuple | rejected `bad_tuple` (all three) |
| Unknown event field | rejected `malformed_event` |
| `deny` from the owner | **classified deny**, never approve, and it never becomes an envelope |
| `deny` signed by a seat key | rejected `wrong_pubkey` (a model cannot cancel a gate either) |
| Thread required, answer not under the gate question | rejected `not_in_gate_thread` |

Other tests:

- `TestReplaySameEventConsumedOnce`: the same event presented twice, and a
  later deny after an approve, both return `already_consumed`. The first
  answer closes the gate.
- `TestModelAuthoredFromSeatHandle`: a seat handle `cto` writes an AMQ message
  to `operator` with a `nostr:` label and a forged journal entry.
  - Cockpit and mailbox resolution reject it with `wrong_amq_sender`.
  - The event it can sign is rejected with `wrong_pubkey`.
- `TestEnvelopeRoundTripAndTamper`: each of these is rejected:
  - swapping in a seat-signed raw event;
  - changing the payload target;
  - pointing the evidence at another owner;
  - pointing the answer at another gate;
  - an envelope from an issuer key that is not in the trust store, which
    operatorauth itself rejects.

  The envelope also decodes under operatorauth's strict decoder.
- `TestAmbiguousControlRefused`: the escaping finding in 2.3.
- `TestFetchThroughBodyKeySignIn`: on the local private-group relay, signing
  in with the body key and fetching works. Without sign-in the fetch gets
  `auth-required`. A non-member key cannot sign in. A substituted event is
  ignored.
- `TestLoadBodyAuthRefusals`: an expired grant, another owner's share, a 0644
  body key, and a path-like session are all refused.

## 4. Event id resolution: three journals, one per delivery path

Upstream has three places that record an event id, one per delivery path. All
three are implemented in `resolve.go`. In every case the event id is only a
pointer: authority comes from fetching the event and verifying it.

| Path | Journal | Message |
| --- | --- | --- |
| amq-acp cockpit / BYOH (`AMQ_ACP_TO`) | `<root>/agents/<AM_ME>/outbox/acp-events/<event_id>.json` = {schema, event_id, message_id, to, thread, created} (`internal/acp/event.go:28-35,98`) | from AM_ME, label `nostr:<id>` |
| amq-acp binding mode, mailbox carrier (`attach --self`) | `~/.amq/remote/acp/remote-events/<agent pubkey>/<event_id>.mailbox.json` = {message_id, created, thread, channel, deadline} (`internal/acp/mailbox.go:389-419`, `remote.go:458-475`) | from `buzz` (`mailbox.go:25`), labels `[acp, buzz]`, **no** `nostr:` label |
| amq-remote serve, strict share | `<root>/extensions/remote/shares/<session>/buzz/ingress/<event_id>.json` = Claim{event_id, owner, body, relay, channel, …, created_at} (`internal/remote/buzzio/ledger.go:41-91`, opened at `cmd/amq-remote/relay_dm.go:128`) | none: the owner DM becomes a submit to the share's target |

**Deviation from the plan.** The plan's step 4 resolves an AMQ message
through the amq-acp journal. In both amq-acp modes the DM is posted and read
by the Buzz Desktop managed agent, whose key "belong[s] to Desktop, and
amq-acp never receives the key" (`cmd/amq-acp/README.md` "Trust limits"). The
Go core then has no member key for the private group (section 5) and cannot
fetch by id. The real-key run therefore uses the strict share, and resolves
the id from serve's ingress ledger, with a relay watch as fallback. The
amq-acp journal mapping is demonstrated (2.4), but it is not on the real-key
path.

## 5. Step 6: can a key other than the share's body key read the NIP-29 DM?

**No, not without breaking the share.** The prototype reads through the
enrolled body key. This is marked **INTERIM** until upstream ask 8 lands.

Evidence (agent-message-queue v0.102.0):

- `internal/remote/buzzio/membership.go:37-38`: reading the relay-signed
  membership snapshots requires that "the authenticated body must itself be a
  member to read them" (pinned Buzz source a929532). The DM group is private
  (39000 `private`), and only authenticated members can read it.
- `internal/remote/buzzio/membership.go:111-118`, `ErrMembership` at `:30`:
  the DM surface opens only when the members are exactly {owner, body}. If a
  third reader key is added, `len(got) != 2` fails and amq-remote closes the
  surface ("DM channel membership is not owner and body only"). README
  `cmd/amq-remote/README.md:377-383` says the same.
- `cmd/amq-remote/README.md:276`: "The body cannot open a DM channel". Only
  the owner creates and administers the group, so a reader could be added only
  by an owner action, and that action would trip the check above.
- `cmd/amq-remote/claude_approval.go:28-31,50-57`: upstream's own verifier
  (the Claude PermissionRequest hook) reads the relay "signed in with the
  share's enrolled body key as serve is", via `relayConfigFor`
  (`cmd/amq-remote/relay.go:64-95`). Upstream has no separate reader identity
  either.

Interim mechanism: `share.go` reads
`<root>/extensions/remote/keys/<session>/body.key` and the kind 1059 grant
from `share.json`. This is the layout in `internal/remote/sharestate/
sharestate.go:46-51,71-155`, which is not a published contract. It refuses a
key that is not 0600, an expired grant, and another owner's share. The Go
core also posts the gate question with the body key and the kind 9 grant
(`post-gate`), as serve would (`buzzio/carrier.go:96-108,1432-1433`). Because
`serve` does not do the post, the question is not in serve's outbox ledger.
Both reading and posting with the body key go away with ask 8, design C.
The security property that matters holds: no model process and no seat holds the owner key, and the body key cannot produce a valid owner answer. Ask 8 closes the INTERIM.

The amq-acp journal (`internal/acp/event.go:98`) cannot serve the real run.
In both amq-acp modes, the Buzz Desktop managed agent posts and reads the DM,
and Buzz Desktop holds that agent's key: amq-acp "never receives the key"
(`cmd/amq-acp/README.md:332-334`). The Go core then has no member key to
sign in to the private group with, so it cannot fetch the answer by id. The
event id source for the real run is therefore amq-remote serve's ingress
ledger (`<root>/extensions/remote/shares/<session>/buzz/ingress/`,
`cmd/amq-remote/relay_dm.go:128`). If serve has no claim for the answer 15 s
after the relay shows it, the id comes from a relay watch: the owner's kind 9
in the DM channel, read with the body key.

## 6. Envelope shape and how it slots into operatorauth

The answer file is `{schema, scheme: "bip340", envelope, bip340_evidence,
gate}`:

- `envelope` is an unchanged `operatorauth.AuthorizationEnvelope`. Its
  algorithm is `ed25519` and it is signed by the core issuer key. The payload
  holds:
  - `gate: gate/<id>`, `target: <digest>`
  - `gate_kind`/`action` from the class map: merge →
    `merge/default_branch_push`, release → `release/github_release`,
    send → `external_send`, grow → `spawn`
  - `answer_message_id: nostr:<event id>`
  - `actor {operator, owner, human}`
  - evidence `[{kind: "bip340.nostr-event", identity: "nostr:<id>", sha256:
    <sha256 of the raw bytes>}, {kind: "gate.text", identity: "gate/<id>",
    sha256: <text digest>}]`
- `bip340_evidence.raw_event` is the event exactly as fetched. Relays reorder
  fields (nos.lol did), so the digest covers the bytes as fetched, and the id
  is recomputed from the fields.

The slot-in, for phase 3 and not done in this spike, is
`VerifyBIP340Evidence` in `envelope.go`:

1. Today's `VerifyAuthorizationEnvelope` runs as is and checks the issuer.
2. For evidence kind `bip340.nostr-event`, operatorauth re-hashes the raw
   bytes against the evidence digest and re-runs the full BIP-340 and tuple
   verification against the gate record, at the stored `verified_at`. An
   approve therefore never rests on the issuer key alone.

Phase 3 changes:

- `trust.go:54` admits `algorithm: "bip340"` entries for the owner pin, with
  key id `bip340-sha256:<hex sha256 of the x-only pubkey>`.
- Evidence verification is dispatched by kind.
- A typed gate class taxonomy is added: `credential` has no v1 gate kind, so
  `BuildAnswerFile` refuses it today.
- A deny record: `Decision` is fixed to `approved` (`envelope.go:100`), so a
  verified deny closes the gate in the ledger and writes no envelope.

## 7. Enrollment dry run (`enroll.sh` with a throwaway owner key)

| Phase | Who | Dry-run result |
| --- | --- | --- |
| A `amq init` (handles operator, remote), manifest with a `fake` target (needs an explicit `epoch`), `amq-remote share --enable buzz-dm` | local | OK: 7 grants requested (5 base kinds + 9, 40003) |
| B `amq-owner-sign --share --out --relay` | **owner, at a terminal** (types the body prefix, then the nsec) | Bundle written. The kind 30177 policy publish failed on nos.lol ("no challenge, can't AUTH"); it needs the Buzz relay. The dry run drove a pty with a throwaway key; the real run is interactive. |
| C `amq-remote share --bundle` | local | OK: `keys/<session>/{body.key 0600, share.json}` enrolled, and gateverify parsed the real files |
| D open a 1:1 DM with the body npub in Buzz, read the channel id | **owner, phone** | not possible without Buzz; a synthetic channel id was used |
| E `share --target --relay --dm-channel --native-session` | local | OK |
| F `amq-remote up`, doctor | local | serve came up. On nos.lol: `relay_auth auth_pending`, `dm_surface configured`. On the loopback Buzz-like relay: doctor ok. |
| answer | **owner, phone** | simulated (2.2) |

Two side effects surfaced, and both are fixed in the scripts:

- `amq-remote up` creates `~/.amq/keepalive/registry.json` by default. The
  scripts now pass `--registry` into the scratch directory. The one
  `~/.amq/keepalive` this run created (empty registry, created at 00:28:36) was
  removed.
- The owner's typed answer is also submitted to the fake target as a prompt.
  This is harmless, and serve posts a result row for it.

No `amq-remote` process is left running, and both scratch roots were torn
down.

## 8. What the real-key run needs from the owner

### 8.1 Owner key at enrollment: the decision

Read from agent-message-queue v0.102.0 (`cmd/amq-owner-sign/main.go`) and,
for (c), from block/buzz main at ca351142 (2026-10-21).

**(a) What the nsec signs.** It signs one enrollment step, and nothing per
gate. The key is read once (`:140`) and used three times, all in that single
run:

- the NIP-OA grant tags that `amq-remote share` printed, one per kind. That is
  7 for a buzz-dm share: 5 base kinds plus 9 and 40003 (`:146-153`, via
  `bodykey.SignAuthTag`). Each is a Schnorr signature over
  sha256(`nostr:agent-auth:<body>:<conditions>`)
  (`internal/remote/bodykey/bodykey.go:280-309`);
- the kind 30177 policy event for the body (`:155`, `:290-297`);
- one NIP-42 AUTH event, so it can publish that policy as the owner (`:311`).

The step repeats only to renew the grants: every `--days` window, 1 to 90 days
with a default of 30 (`cmd/amq-remote/README.md:269,665`). Gate answers are
never signed by this tool. The owner signs them on the phone, in Buzz.

**(b) How the nsec is read and kept.**

- It is read only after the body-prefix confirmation on `/dev/tty`
  (`:136-143`, `:206-216`).
- With a terminal on stdin, it is read with `term.ReadPassword`, hidden
  (`:324-326`). Otherwise it reads one line from stdin, for a password-manager
  pipe (`:327-331`).
- The input buffer is cleared (`:333`) and the parsed key is zeroed on return
  (`:144`).
- The only file written is the bundle of public tags, mode 0600 (`:168`). The
  file header states it: "never written, printed, or passed to amq or
  amq-remote" (`:7-8`).
- The file has no environment write, no log of the key, and no keepalive or
  registry access.
- Errors name the format, never the value (`:341-356`).
- Residue: `parseKey(strings.TrimSpace(string(line)))` (`:337`) makes Go
  string copies that cannot be zeroed. They live in process memory until the
  process exits, seconds later.

**(c) Can Buzz sign the grant on the phone instead?** No, not today.

- amq 0.102.0 has no remote-signer path. The source contains no NIP-46,
  bunker, nostrconnect or QR anywhere. The vendored fiatjaf.com/nostr has a
  `nip46` package, but AMQ does not import it.
- A NIP-46 signer would not be enough anyway. NIP-46 signs NIP-01 events,
  while the grant is a signature over a raw preimage hash, not an event.
- Buzz mobile only verifies NIP-OA (`mobile/lib/shared/crypto/nip_oa.dart:79`,
  `Schnorr.verify`). The NIP-OA signers in Buzz are Rust: `crates/buzz-sdk/
  src/nip_oa.rs`, and `crates/buzz-core/src/private_managed_agent.rs`, which
  is Desktop's managed-agent grant with no kind limit and no expiry
  (`cmd/amq-acp/README.md:333-334`).
- Buzz's QR pairing (NIP-AB, `crates/buzz-core/src/pairing/NIP-AB.md`)
  transfers the nsec itself, or bootstraps a NIP-46 session. That is an
  export by another route, not a way to avoid one.

Because (c) does not exist, there is no option A. **The question for the
owner is: export the Buzz nsec once into a hidden terminal prompt on the Mac,
yes or no?**

- **Yes**: run `S1-owner-run.md` as written. The nsec stays in
  amq-owner-sign's memory for one run per grant window. No model process and
  no seat ever holds it.
- **No**: criterion 1 stays pending until upstream offers phone-side grant
  signing. That is ask 10 in `docs/v3-upstream-asks.md`, status "file after
  owner decision".

### 8.2 Items

1. The owner's Buzz **npub**: the key the Buzz phone app signs with.
2. The Buzz **relay URL** (`wss://…`). It must be a Buzz relay that issues
   NIP-42 challenges and serves the NIP-29 DM group.
3. **One terminal session** at the Mac for `amq-owner-sign`. The owner checks
   the body pubkey, types its first 8 characters, then enters the **nsec** for
   that npub, hidden, read only by amq-owner-sign. This requires the owner's
   "yes" in 8.1.
4. On the phone: **open a 1:1 DM with the body npub** the script prints, and
   send the **DM channel id** back (as `S1_DM_CHANNEL`).
5. On the phone: **reply in the gate's thread** with the exact line the
   script prints, `approve <gate-id> merge <prefix>`, within 30 minutes.
   Optionally, a second run answered `deny ...` to see a real deny classified.
6. Afterwards: archive the DM. The grants expire on their own after 30 days;
   removing the body key from the relay revokes it sooner.

Step-by-step owner instructions: `docs/v3-spike/S1-owner-run.md`.

Command: `spike/gateverify/realkey-run.sh <npub> <relay-url> <gate-id>`. It
stops with exit 10 or 11 at each owner step and resumes on rerun. Use
`S1_REQUIRE_THREAD=1` to require the threaded reply. Its last line is PASS or
FAIL.

## 9. Libraries and versions

| Module | Version | Why |
| --- | --- | --- |
| github.com/btcsuite/btcd/btcec/v2 (`schnorr`) | v2.5.0 | BIP-340 sign and verify; the same version upstream v0.102.0 pins |
| github.com/coder/websocket | v1.8.15 | relay client; the same version upstream pins |
| github.com/decred/dcrd/dcrec/secp256k1/v4 | v4.4.0 (indirect) | btcec backend |
| github.com/btcsuite/btcd/chainhash/v2, decred blake256 | v2.0.0, v1.1.0 (indirect) | btcec dependencies |
| golang.org/x/sys | v0.47.0 (indirect) | from the main module, via operatorauth |
| NIP-19 bech32 | in-module (`npub.go`, NIP-19 test vector) | btcutil v1.1.5 would add a 2020 golang.org/x/crypto to the graph |

The module declares `go 1.25.12`, matching the main module. All versions are
older than two weeks.

## 10. Open questions for phase 3

1. **Owner key at the terminal.** This is answered as a yes/no owner
   decision in 8.1. Phone-side grant signing does not exist today; drafted as
   ask 10.
2. **Ask 8 scope.** Design C should forward the raw owner event to an
   operator mailbox, as a gate interaction kind. That removes the body-key
   reads and posts (section 5) and the private `sharestate` layout
   dependency. Until then, a share has no "operator mailbox" target kind; the
   spike borrows `fake`.
3. **Thread binding.** Should phase 3 require the answer to reply to the
   body-posted gate message (`--require-thread`)? That gives the
   Claude-hook-style guarantee that the owner answered the core's own text.
   It also adds a history check (edits and deletions, as `CheckHistory` does)
   and depends on Buzz phone thread UX.
4. **Escaping ambiguity.** The `\b`/`\f`/C0 id divergence between
   fiatjaf.com/nostr and NIP-01 is drafted as ask 11, with the vector pinned
   in `TestAsk11Vector`. The fail-closed rule stays in the core.
5. **Taxonomy.** Map the five typed classes onto operatorauth gate kinds,
   since `credential` is missing. Add a signed deny record.
6. **Clock.** The window trusts the owner's `created_at`, with 30 s of slack,
   plus the core's own receive time. Is the gate window 30 minutes by
   default?
7. **Relay trust.** A relay can withhold the answer but cannot forge one. With
   a thread binding, the history-check trust from upstream applies.
