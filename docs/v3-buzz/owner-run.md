# Owner run: brief 4-5 acceptance 4 (and 3-1 acceptance 9)

One owner session of about 15 minutes, held at the end of brief 4-5
(rulings 3 and 7). The owner is present with the real Buzz key in the
password manager and the Buzz app (phone or Desktop). The lead drives the
Mac. Nothing in this run prints, logs or stores the key: it goes from the
password manager's read command into `amq-owner-sign` through the pipe and
nowhere else (plan 9, R33; `docs/v3-buzz/README.md`, "The secret rule").

What it proves:

- 4-5 acceptance 4: one typed gate answered from the phone in the thread,
  verified by the core (3-1), and one plain message from the phone routed to
  a named lead; times recorded;
- 3-1 acceptance 9: the owner run with the real Buzz key on the production
  path (the gate store, the core's question through the share body, the
  verifier, the answer file);
- 4-5 acceptance 5 on the real Buzz profile (the dry run met it against a
  local relay): online while the operator session runs, offline after;
- the third item (phase 5): the Buzz-signed `seat clear` answered from the
  phone, with the clear record carrying the gate id and the answer's event
  id.

Each step lists who acts, the command, the expected output and what to
record. Write the times into the table at the end (wall clock, HH:MM:SS).

## Before the owner arrives (lead, about 10 minutes, no key)

0.1 Build the branch binary and put it first on PATH for this shell only:

```
cd <worktree of v3-4e>
go build -o /tmp/owner45/bin/amq-squad ./cmd/amq-squad
export PATH=/tmp/owner45/bin:$PATH AMQ_SQUAD=/tmp/owner45/bin/amq-squad AMQ_NO_UPDATE_CHECK=1
amq --version; amq-remote --version; amq-owner-sign --version
```

Expected: `0.107.0` three times (or later; record the versions).

0.2 Check that no other endpoint holds the Buzz share root and that no
earlier enrollment exists (this run enrolls the 3.0 agent for real):

```
pgrep -fl 'amq-remote up' || echo none
ls ~/.amq-squad/buzz 2>/dev/null || echo "no enrollment yet"
ls ~/.amq-squad/gate-binding.json 2>/dev/null || echo "no binding yet"
```

Expected: `none`, `no enrollment yet`, `no binding yet`. If an enrollment
exists, stop and ask the owner whether to renew it (`BUZZ_RENEW=1`) instead.

0.3 A scratch fleet in its own registry file (the owner's real
`~/.amq-squad/fleet.yaml` is not touched): one squad `app` with its lead
`cto` in session `owner45`, and a scratch operator session (the `user`
mailbox) in session `ops`; the operator's project names the squad as an AMQ
peer for cross-project routing:

```
P=/tmp/owner45/project; OP=/tmp/owner45/operator; FL=/tmp/owner45/fleet.yaml
mkdir -p "$P" "$OP"
T="--project $P --roles cto --runtime cto=codex --lead cto --session owner45"
D=$(amq-squad team propose $T --json | sed -n 's/.*"digest": "\(sha256:[0-9a-f]*\)".*/\1/p')
amq-squad team apply --digest "$D" $T
R="$P/.agent-mail/owner45"; ROP="$OP/.agent-mail/ops"
amq init --root "$R" --agents buzz,cto
# stand-in for the launch record the lead's launch leaves: a message is
# routed only to a registry lead whose seat still names a lead role
d="$R/agents/cto/extensions/io.github.omriariav.amq-squad"; mkdir -p "$d"
printf '{"schema":1,"handle":"cto","role":"cto","binary":"codex","cwd":"%s","agent_pid":0,"started_at":"%s","root":"%s"}\n' \
  "$P" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$R" >"$d/launch.json"
amq init --root "$ROP" --agents buzz,user
printf '{"root": ".agent-mail", "project": "fleet", "peers": {"app": "%s"}}\n' "$P/.agent-mail" >"$OP/.amqrc"
printf '{"root": ".agent-mail", "project": "app", "peers": {"fleet": "%s"}}\n' "$OP/.agent-mail" >"$P/.amqrc"
amq-squad fleet operator --fleet-file "$FL" --root "$ROP" --amq-project fleet --session ops
amq-squad fleet add --fleet-file "$FL" --name app --root "$R" --project-path "$P" --repository owner/app --lead cto --session owner45
```

Rehearse 4.2 and 4.3 without the owner (they fail cleanly with "gate
binding ... no such file" until step 3 has run) and fix anything else first.

0.4 Owner key command: agree with the owner on the exact read command, for
example `op read "op://Private/Buzz/nsec"`. The lead never runs it.

## The session (owner present)

### 1. Enroll (owner and lead, about 4 minutes)

1.1 Lead:

```
docs/v3-buzz/enroll.sh <owner npub> wss://omriariav.communities.buzz.xyz
```

Expected: `grants requested: 8`, the owner-step lines, exit 10. Record T0
(the start).

1.2 Owner, at the Mac terminal (the lead looks away from the screen is not
needed; nothing secret is shown):

```
BUZZ_OWNER_KEY_CMD='op read "op://<vault>/<item>/<field>"' docs/v3-buzz/enroll.sh <owner npub> wss://omriariav.communities.buzz.xyz
```

amq-owner-sign shows `session: amq-squad`, the body pubkey and 8 kinds
(20003, 1059, 24200, 44200, 10100, 0, 9, 40003) with an expiry 30 days out.
The owner checks the body against the script's `body:` line and types its
first 8 characters. The 1Password prompt may ask for Touch ID.

Expected, in order: `owner pubkey: <the owner's hex>`, `wrote 8 signed
grants`, `published kind 30177 policy <id>`, `== C`, `grants enrolled: 8`,
`== D`, doctor lines `"discovery": "policy_present"`, `"presence":
"online"`, `"state": "authenticated"`, the agent npub, exit 11. Record T1
(exit 11) and the grant count.

If amq-owner-sign says the key is not valid: the read command prints more
than the key and one newline. Fix the command, rerun 1.2. Do not print the
key to look at it.

### 2. Open the DM (owner, phone, about 2 minutes)

In Buzz: new message, search `amq-squad`, open the DM with that agent (it
shows online). Copy the DM channel id (Buzz shows it in the conversation
details) and send it to the lead.

Record T2 (channel id received).

### 3. Bind (lead, under a minute)

```
BUZZ_DM_CHANNEL=<channel id> docs/v3-buzz/enroll.sh <owner npub> wss://omriariav.communities.buzz.xyz
```

Expected: `gate binding written: ~/.amq-squad/gate-binding.json`, `8
grants`, `created .../issuer-key.pem`, `created
.../authorization-trust.json`, `== F: stopping the enrollment endpoint`,
exit 0. The agent shows offline on the phone within about 10 s. Record T3.

### 4. Attach, presence on (lead; owner watches the phone)

4.1 The operator session is live (the stand-in: a fresh presence file for
the `user` handle, as a running operator session writes it):

```
printf '{"status":"active","last_seen":"%s"}\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >"$ROP/agents/user/presence.json"
```

4.2 Attach to the operator session (no `--lead`; keep it running in its own
terminal; its output holds no secret). `--project` and `--session` name the
squad whose gate store the answer of step 5 lands in:

```
mkdir -p /tmp/owner45/evidence
amq-squad remote attach --fleet-file "$FL" --project "$P" --session owner45 --me cto --interval 3s --json | tee /tmp/owner45/evidence/attach.jsonl
```

Expected: an `attached` line (`"mailbox":"user"`, `"operator":true`,
`"leads":["app/cto","cto"]`), then a `presence` line with
`"action":"started","present":true`. On the phone the agent shows online.
Record T4 (online seen on the phone).

The presence file goes stale after 90 s; refresh it with 4.1 every minute
during the run (or keep a loop: `while sleep 45; do <4.1>; done`).

### 5. The typed gate (owner on the phone, about 2 minutes)

5.1 Lead, in a second terminal:

```
amq-squad gate raise --project "$P" --session owner45 --me cto --gate g45-owner-01 --action tag --target "git tag v3.0.0-owner45" --json
```

Expected: `"question_sent": true` and a `question_event_id`. Record T5 (the
raise) and the event id.

5.2 Owner: the question appears in the DM:

```
amq-squad gate g45-owner-01
squad: root-sha256:<64 hex of the squad root>
class: release (tag)
target: git tag v3.0.0-owner45
digest: sha256:<64 hex>
Reply in this thread with one line:
approve g45-owner-01 release <16 hex>
deny g45-owner-01 release <16 hex>
```

Reply IN THE THREAD of that message (long-press, reply in thread) with the
approve line exactly. Record T6 (sent).

Expected within one attach tick: an attach `handled` line with `"kind":"gate"`
and `"outcome":"approved"`, and a thread reply on the phone `amq-squad gate
g45-owner-01: approved, verified by the core.` Record T7 (the handled line's
`at`).

5.3 Lead:

```
amq-squad gate status --project "$P" --session owner45 --gate g45-owner-01 --json
amq-squad verify action --project "$P" --session owner45 --gate g45-owner-01 --action tag --target "git tag v3.0.0-owner45"
```

Expected: state `accepted`; verify action approved (exit 0). This is 3-1
acceptance 9.

5.4 Negative, optional (owner, 30 s): send the same approve line again in the
thread. Expected reply: `answer not accepted (already_consumed)`.

### 6. The routed message (owner on the phone, about 1 minute)

6.1 Owner, in the main DM box: `/status`. Expected reply: `user: present
(online, ...). needs you N, working N, ready N, inactive N. ...`.

6.2 Owner: `@cto owner45 ping from the phone`. Record T8 (sent).

Expected: attach `handled` line with `"kind":"route"`, `"lead":"app/cto"`; the
phone reply `delivered to user, routed to app/cto`. Lead:

```
amq list --root "$ROP" --me user --new --json
amq list --root "$R" --me cto --new --json
```

Expected: the operator mailbox holds the owner's message (from `buzz`,
labels `buzz`, `nostr:<the phone event id>`); the squad lead's inbox holds
the routed copy (from `buzz`, subject `Buzz: from the owner, routed by user`,
thread `p2p/fleet:ops:buzz__app:owner45:cto`, labels `buzz`,
`nostr:<the phone event id>`, `routed`, body `owner45 ping from the phone`).
Record T9 (the routed message's created time).

### 7. Presence off (lead; owner watches the phone)

Stop the presence loop and make the operator session stopped:

```
printf '{"status":"stopped","last_seen":"%s"}\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >"$ROP/agents/user/presence.json"
```

Expected: an attach `presence` line `"action":"stopped","present":false`;
the phone shows the agent offline. Record T10 (offline seen).

### Third item: the Buzz-signed seat clear (phase 5; owner at the Mac terminal and on the phone, about 3 minutes)

The live row of the Buzz-signed `seat clear` (plan 4.3, amendment 28's second
factor). The code rows run against the gateverify fixtures and a fake relay
(`docs/v3-spike/5-clear-execution.md`); this item is the same flow with the
real Buzz key, from the owner's real pty and the owner's phone. It runs after
section 7 with the operator session still attached (presence on), so the
attach tick verifies the owner's answer.

9.1 Lead, before the owner types: put a seat in a config-change quarantine in
the scratch session (the steps of the test fixture `signedClearSeat`: a
scratch repository and worktree for seat `q1`, a task claimed by `q1` with a
done report filed, then a harmless key added to `~/.claude/settings.json`
and `amq-squad seat commit --seat q1 --root "$R"`, which refuses with
`global_config_changed` and quarantines `q1`). Remove the key again after the
run. Check the mode in force: `amq-squad doctor` shows `seat clear signature:
required` (the Buzz enrolment exists and the owner file has no `clear`
block). A `pty_only` key beside the enrolment is `grants_invalid`.

9.2 Owner, at the Mac terminal (a real pty that is no seat's pane):

```
amq-squad seat clear --seat q1 --root "$R" --project "$P" --session owner45
```

Type `q1` at the prompt. Expected: the verb exits non-zero with
`clear_unsigned`, prints `seat q1 stays quarantined: clear request
clear-<32 hex>` and that the owner's answer is awaited on Buzz. The
quarantine stays: `amq-squad seat commit --seat q1 --root "$R"` still
refuses with the quarantine. Record T11 (request).

9.3 Owner, on the phone: the question appears in the DM:

```
amq-squad gate clear-<32 hex>
squad: root-sha256:<64 hex of the squad root>
class: clear (quarantine_clear)
target: clear q1 nonce=<32 hex> incarnation=<task>@<unix nanos> config=sha256:<64 hex>
digest: sha256:<64 hex>
Reply in this thread with one line:
approve clear-<32 hex> clear <16 hex>
deny clear-<32 hex> clear <16 hex>
```

Reply IN THE THREAD with the approve line exactly. Record T12 (sent).
Expected within one attach tick: a `handled` line with `"outcome":"approved"`
and the thread reply `approved, verified by the core.` Record T13.

9.4 Owner, at the Mac terminal: run the 9.2 command again and type `q1`.
Expected: `seat q1 cleared (signed by ...)`, exit 0. Then the lead:

```
amq-squad seat commit --seat q1 --root "$R"      # commits the kept tree only
jq '.quarantine.clear | {gate_id, event_id, config_digest}' "$R/extensions/amq-squad/commits/q1.json"
```

Expected: the clear record names the gate and the answer's event id (the
phone reply's event id). Record T14 (cleared).

9.5 Negative, optional (owner, 30 s): send the same approve line again in the
thread. Expected reply: `answer not accepted (already_consumed)`. A second
`seat clear --seat q1` says the seat is not quarantined.

9.6 Buzz unreachable, optional (lead): repeat 9.1 and 9.2 with the relay
blocked. Expected: `clear_unsigned`, the question reported as not on Buzz
with `amq-squad gate post --gate <id>`, and `amq-squad doctor` showing
`seat clear q1` as a warning that names why it waits. There is no pty-only
fallback.

### 8. Wrap up (lead, 2 minutes; the owner may leave)

Ctrl-C the attach. Copy the evidence (all public):

```
cp -R "$R/extensions/amq-squad/gates" "$R/extensions/amq-squad/answers" /tmp/owner45/evidence/
cp ~/.amq-squad/buzz/doctor.json ~/.amq-squad/gate-binding.json /tmp/owner45/evidence/
```

The agent stays enrolled for 3.0 use (the grants expire in 30 days); the
scratch project under /tmp/owner45 can go. Move the evidence into
`docs/v3-spike/4-5-evidence/owner-run/` and fill the table.

There is no real-key grep: grepping for the key would need the key outside
the pipe. The no-secret-output evidence is the dry run's canary test
(acceptance 2), which ran this same script with a throwaway key.

## Record

| item | value |
| --- | --- |
| amq, amq-remote, amq-owner-sign versions | |
| T0 enroll start | |
| T1 exit 11 (grants signed, enrolled) | |
| grants at enrollment (expected 8) | |
| T2 DM channel id received | |
| T3 bound | |
| T4 online on the phone | |
| T5 gate raised (question event id) | |
| T6 owner's thread answer sent | |
| T7 verified (attach handled line) | |
| post to verified, T7 - T5 (S1: 23 490 ms) | |
| answer to verified, T7 - T6 | |
| gate status / verify action | |
| T8 routed message sent | |
| T9 routed message in cto's inbox | |
| phone to lead, T9 - T8 | |
| T10 offline on the phone | |
| T11 seat clear requested (clear_unsigned, quarantine stays) | |
| T12 owner's clear answer sent | |
| T13 clear answer verified (attach handled line) | |
| T14 seat cleared (record names gate and event) | |
| clear answer event id = record event_id | |
| owner minutes, T0 to T10 (target about 15; the third item adds about 3) | |
| frictions | |
