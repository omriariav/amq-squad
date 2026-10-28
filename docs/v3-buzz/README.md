# Buzz on your phone: the owner's guide

amq-squad 3.0 has one Buzz agent. It is the body of one `amq-remote` strict
share that you enroll once with your Buzz key, and it is attached to the
operator session's mailbox (your `user` handle, from the fleet registry), or
with `--lead` to one squad lead (plan 3.6, 9; R42). The share is bound to
amq-remote's mailbox adapter (amq-remote v0.116.2 or later): `amq-remote
serve` reads your DMs, delivers each into the share's `attach` inbox as one
AMQ message, and posts the attachment's reply under your message as its
result row (brief 5-amq-B). From the phone you can:

- see whether the attached session is present (the agent shows online in
  Buzz while the session runs, offline after it stops);
- answer a typed gate (tag and release, external send, credential change,
  growth beyond the grant) by replying in the gate's thread;
- send any other message: it lands in the attached mailbox, and a message
  that starts with `@<lead>` or `<lead>:` is routed on to that lead through
  AMQ. Only a lead is a destination: attached to the operator, a squad's
  lead in the fleet registry (reached by AMQ cross-project routing); with
  `--lead`, a handle whose launch record names a lead role (`lead`, `cto`,
  or the team's lead role). attach refuses any other `--leads` handle, and a
  message naming one stays in the mailbox.

Every message gets one reply, ids and code words only, never your text:

| reply | meaning |
| --- | --- |
| `accepted <gate> <class>` | your answer verified and committed |
| `refused <gate> <code>` | not accepted; the code is the verifier's (`already_consumed`, `wrong_pubkey`, `outside_window`, `bad_signature`, `bad_tuple`, `not_in_gate_thread`, ...) |
| `refused <gate> not_in_gate_thread` and `re-ask: answer in the gate thread (event <id>)` | answer again as a reply in that thread |
| `routed <lead or mailbox>` | delivered (and forwarded to the lead) |
| `routed <mailbox> not_forwarded <code>` | delivered, not forwarded (`ambiguous_lead`, `not_a_lead`) |

While a verification runs you may see `verifying <gate>` (a status line).
`/status`, `/inspect` and `/cancel <ref>` are amq-remote's own commands:
serve answers them and they never reach the mailbox. amq-remote runs one
request at a time, so a DM you send before the previous one has its result
row is answered `busy`; send it again.

## The secret rule

Your Buzz key (the nsec) reaches the signer only through a password-manager
pipe into `amq-owner-sign`. The same command with its output on a terminal,
in a log or in a file is not allowed (plan 9, R33).

What this means in practice:

- The key is used once per grant window (30 days), by `amq-owner-sign`
  alone, to sign your grants for the agent's body key. Nothing in amq-squad,
  `amq` or `amq-remote` reads it; no seat and no model ever holds it.
- The owner script takes the key only from `BUZZ_OWNER_KEY_CMD`, a command
  that prints the key on stdout. The script connects that stdout to
  `amq-owner-sign`'s stdin and to nothing else. There is no typed or pasted
  path.
- The pipe shape, with the 1Password CLI as the example:

  ```
  BUZZ_OWNER_KEY_CMD='op read "op://<vault>/<item>/<field>"' \
    docs/v3-buzz/enroll.sh <your npub> wss://<your buzz relay>
  ```

  `op read` prints the field and one newline, which is what
  `amq-owner-sign` reads. Do not wrap it in `printf` or `echo` (the S1 run
  failed once because `printf %s\n` inside `sh -c` appended a literal `n`).
  `op run --no-masking` is acceptable only in the same pipe position.
- Never run the key command by itself. Its output on a terminal, in a
  file, in a log, in a clipboard or in a chat is the forbidden form.
- The command text names where the key is, not the key, so it may sit in
  shell history.
- No crash copy: the script runs with `ulimit -c 0` and starts the signer
  with `GOTRACEBACK=none`, so no core file is written; after the key prompt
  only the signer's own lines reach the terminal, and a crash trace is
  dropped with a one-line notice. The same guarantee inside
  `amq-owner-sign` is upstream draft 16.
- The share lives under `~/.amq-squad/buzz/` only (`BUZZ_HOME` elsewhere is
  refused, and so is a binding whose `share_root` is elsewhere): that tree is
  protected from seat writes.
- Only public values are shown or copied: your npub, the agent's npub, the
  gate id, the answer line and the DM channel id.

## Enroll (once per 30 days)

`docs/v3-buzz/enroll.sh <owner-npub> <relay-url>` is resumable: each phase
skips when its output exists, and the script stops with exit 10 or 11 when it
needs you.

1. First run, without `BUZZ_OWNER_KEY_CMD`: it checks amq-remote is
   v0.116.2 or later, creates the share root `~/.amq-squad/buzz/root` with
   the handles `remote` (serve's) and `attach` (the mailbox), declares the
   share's target as the mailbox adapter (`config.handle` `attach`,
   `reply_timeout` 60s), asks `amq-remote` for 8 grants (5 base kinds,
   buzz-dm 9 and 40003, buzz-profile 0) and stops (exit 10) to ask for the
   pipe.
2. Second run, with `BUZZ_OWNER_KEY_CMD`: `amq-owner-sign` shows the session,
   the body pubkey and the 8 kinds with their expiry. Check the body against
   the line the script printed, type its first 8 characters, and the key
   comes through the pipe. It signs, writes the bundle (public tags only) and
   publishes your kind 30177 policy, so Buzz Desktop lists the agent as
   yours. The script enrolls the grants, turns on presence with the name
   `amq-squad` (`BUZZ_NAME` changes it), starts the endpoint so the agent's
   profile is published, and stops (exit 11).
3. In Buzz, find the agent by its name, open a DM with it, and copy the DM
   channel id (a public value).
4. Third run, with `BUZZ_DM_CHANNEL=<channel id>`: it turns on DM commands
   on the mailbox target (`amq-remote share --target amq-squad --dm-channel
   <id> --native-session attach --min-evidence submitted`; nothing to sign),
   then `amq-squad remote bind` checks that binding in the manifest and
   writes your gate binding `~/.amq-squad/gate-binding.json` (relay, channel, your public key,
   the agent's public key, the carrier handle `buzz`, the share; no secret),
   creates the core's issuer key and trust store if they are absent, and
   stops the enrollment endpoint.

Renewal, before the 30 days run out (`amq-squad remote status` warns three
days ahead): `BUZZ_RENEW=1 BUZZ_OWNER_KEY_CMD='...' BUZZ_DM_CHANNEL=<channel
id> docs/v3-buzz/enroll.sh <owner-npub> <relay-url>`. It asks `amq-remote`
for a new window for the same body (the same agent and DM in Buzz), and the
key goes through the pipe once more. A new `BUZZ_HOME` would mint a new body,
a new agent.

To revoke early, remove the agent's relay access (or ban its body key on the
relay); archive the DM in Buzz.

A share enrolled before 5-amq-B (bound to the presence-only `buzz-presence`
fake target) is refused by the script, `remote bind` and `remote attach`:
move `~/.amq-squad/buzz` aside and enroll again. There is no fallback.

## Attach

```
amq-squad remote attach --project <squad dir> --session <session>
amq-squad remote attach --lead <lead> [--leads <other leads>] --project <dir> --session <session>
```

The first form attaches to the operator session: the mailbox is `user` in
the operator's AMQ root from `~/.amq-squad/fleet.yaml` (`--fleet-file`
another), every squad lead of the registry is a destination, presence
follows the fleet document's operator row, and each squad's gate answers are
verified and committed in that squad's gate store. The second form attaches
to one squad lead. It runs until Ctrl-C. Each tick (5 s) it reads the status
document, starts the share's `amq-remote` endpoint while the bound seat is
present and stops it when the seat is not (Buzz then shows the agent
offline), and takes what serve delivered to the `attach` inbox: a gate
answer goes to the core, which opens its own short connection to the relay
with the agent's body key, reads your signed event by its id (and the
question's edit and delete history), verifies it, and closes the
connection; every other message is delivered. A verification is capped at
10 s; one that hangs gets no reply (serve's 60 s reply timeout ends the
request), attach prints an `attention` line, and the gate stays open for
you to answer again. `amq-squad remote status` (same
flags) prints the presence line, the grant expiry and the login service
warning; `amq-squad doctor` shows the login service state too.

Gate questions say "Reply in this thread with one line:" and show the two
answer lines. Answer with a thread reply. An answer typed in the main DM box
is not accepted; the core re-posts "Reply here to confirm" in the gate thread
once, the reply to your message points at it, and your next thread reply is
the answer (amendment 19). Removing a reaction in the thread does not void
an answer; editing or deleting your answer does.

## Upgrade note (squad identity)

Gates raised before the squad field are closed; raise them again under a
new id. Each gate record and gate question carries its squad's identity
(amendment 28), and the core refuses a record without one. `gate raise`
prints this sentence when it meets an old record under the same id. The
only such records are the spike's and the rehearsals' on this machine.

## Privacy

Buzz DMs are not end-to-end encrypted. The relay operator can read your
messages, the gate texts and the result rows (ids and code words). The
agent's name and presence are clear
text too; do not put a path or prompt data in `BUZZ_NAME`.

## The login service and the box recipe

`amq-remote service status` reports whether a login service restarts an
`amq-remote` endpoint after a reboot. doctor warns when it is absent (plan
8); doctor shows it, and so does `amq-squad remote status`. The Buzz attachment supervises its own endpoint for the share root,
so it needs no login service; the warning is about the machine root's
endpoint (links, `attach --self` bindings).

The box recipe (a Hetzner box or a Mac mini with Tailscale, or Cloudflare
Tunnel plus Access) is a docs item of plan 8 and is not written yet. For
Buzz it adds nothing to this guide: the share root, the enrollment and the
attachment run on the box exactly as on the Mac, and the key still enters
only through the pipe on the box's terminal session. SagiMedina/handoff
(the tmux mirror over Tailscale) composes on the same tmux and is the other
recipe item (plan 9).

## Files

| path | what | secret |
| --- | --- | --- |
| `~/.amq-squad/buzz/root/extensions/remote/keys/amq-squad/body.key` | the agent's body key (minted by `amq-remote`, 0600) | the body key, not yours |
| `~/.amq-squad/buzz/root/extensions/remote/keys/amq-squad/share.json` | your 8 signed grants | no |
| `~/.amq-squad/buzz/share.txt`, `bundle.json` | the grant preimages, the signed tags | no |
| `~/.amq-squad/gate-binding.json` | relay, channel, public keys, carrier, share | no |
| `~/.amq-squad/issuer-key.pem`, `authorization-trust.json` | the core's envelope key and its trust store | the issuer key |
| `~/.amq-squad/buzz/root/extensions/remote/manifest.json` | the share's target (the mailbox adapter) and its relay binding, written by `amq-remote` | no |
| `~/.amq-squad/buzz/root/agents/attach/inbox/` | your DMs as serve delivered them (taken once, then in `cur`) | your DM text |
| `~/.amq-squad/buzz/root/attach/up.log` | the endpoint's log | no |

guard protects all of `~/.amq-squad/buzz/` and the owner files above
from seat writes.

The owner run for acceptance 4 is `docs/v3-buzz/owner-run.md`.
