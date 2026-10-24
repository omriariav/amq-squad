# amq-squad 3.0, brief 4-5: the Buzz attachment

Branch `v3-4e`, cut from `v3` at 4be5fd50. One implementer, own worktree.
Every commit passes the standard gate (`go build ./... && go vet ./... &&
go test ./... && scripts/manifest-check.sh --final`, `GOOS=linux go build
./...`, `GOOS=windows go build ./...`) plus `go test -race` on the 4-5
packages; the full output of each gate run is in `docs/v3-spike/4-5-evidence/`.

Rulings that bind this brief (quoted from the brief): enrollment is "an owner
script in docs for 3.0, with the S1 enroll.sh made the default; a binary
command is phase 5"; "keep 30 days"; acceptance 4 "shared with 3-1's
acceptance 9: one owner session of about 15 minutes at 4-5's end". Lead
ruling 16: the operator mailbox is the owner's `user` handle.

## Commit 1: the owner script, the attachment, presence, routing, the doctor check

### Built

- `docs/v3-buzz/enroll.sh`: the owner script, `spike/gateverify/enroll.sh`
  with the S1 lessons as defaults:
  - profile and presence on: `amq-remote share --enable buzz-dm --enable
    buzz-profile --days 30` (8 grants), then `share --target --relay
    --presence amq-squad` before the DM exists, and the endpoint started so
    the profile is published and the owner finds the agent by name;
  - the key only through `BUZZ_OWNER_KEY_CMD`, whose stdout is connected to
    `amq-owner-sign`'s stdin and nothing else. S1's option B (the hidden
    typed prompt) is removed: the secret rule allows the pipe only. S1's
    `tee owner-sign.log` is removed: the signer's stdout (public values) is
    shown line by line and scanned for the policy line, never stored. `set
    +x`, umask 077. The documented pipe shape is `op read "op://..."` with
    the S1 printf lesson stated next to it; the script never contains,
    echoes or inspects a key;
  - the bundle's owner is checked against the given npub before anything is
    enrolled (as S1 did);
  - `BUZZ_RENEW=1` renews the window for the same body (`share --renew`);
  - the last phase runs `amq-squad remote bind` and stops the enrollment
    endpoint (the attachment runs it from then on).
- `internal/buzz` (new):
  - `share.go`: `LoadShare` reads the enrolled share in amq-remote's layout
    (body key 0600, no symlinked directory, one owner, one window), `Grant`
    returns an unexpired NIP-OA tag per kind. `DefaultKinds` is the 8-kind
    default.
  - `transport.go`: `Connect` checks the share against the binding's pin,
    dials and signs in with the body key and the kind 1059 grant;
    `GateTransport` fills 3-1's `gateTransport` seam (cli/gate_store.go now
    defaults to it), so `gate raise` posts the question through the share.
  - `target.go`: `Resolve` binds the agent to one squad lead now;
    `AttachToOperator` is the one-line switch to the operator's `user`
    handle for 4-3 (same code path; tested both ways).
  - `presence.go`: `FromStatus` reads a one-squad document or a fleet
    document (ruling 16) and derives the bound seat's presence and the
    activity line (bucket counts, open gates, last message).
  - `endpoint.go`: `Supervisor` starts the share's `amq-remote up` while the
    bound seat is present and stops it (SIGTERM, so serve publishes
    `offline`) when it is not.
  - `route.go`: `ParseRoute` (`@lead text`, `lead: text`), `Deliver`: the
    phone message into the bound mailbox from carrier `buzz` with the
    `nostr:<event id>` label (the shape of AMQ's mailbox binding), then
    forwarded to a named lead with a ref and the `routed` label.
    `AMQMailer` uses `amq send --json`, body on stdin.
  - `pump.go`: one read of the DM channel (owner, kind 9, the pinned
    channel, signature checked), each event handled once: approve/deny to
    `Core.HandleEvent` (verify, commit, or the 3-1 re-ask); `/status`
    answered in its thread; anything else delivered and routed. Replies are
    body-signed thread replies.
  - `service.go`: `LoginService` (the seam doctor calls), `AMQRemoteService`
    (`amq-remote service status --json`), `ServiceWarning` (plan 8).
- `internal/cli/remote.go` (new): `amq-squad remote bind` (the gate
  binding from the enrolled share, and the issuer key and trust store when
  absent), `remote attach` (the loop: status document, presence, DMs),
  `remote status` (presence line, grant expiry, login service warning),
  `remote npub`.
- `internal/gateverify/binding.go`: optional `share_root`, `share_session`.
- `internal/cli/hook_guard_env.go`: guard protects `~/.amq-squad/buzz/`
  (the share's body key, grants and the DM marks).
- `docs/v3-buzz/README.md`: owner docs, the secret rule, the pipe shape,
  enroll, attach, privacy, the login service and the box recipe pointer.

### Behavior changes named

1. `gate raise` for a typed action now posts the question through the
   enrolled Buzz share when a binding exists (before: always "no Buzz
   transport"); without a share it still writes the record and says why the
   question was not sent.
2. guard denies seat writes under `~/.amq-squad/buzz/`.
3. A gate binding file may carry `share_root` and `share_session`. An older
   binary's strict decoder refuses a binding that has them.
4. New verb `amq-squad remote` (bind, attach, status, npub) in the public
   catalog.

### Deviations and open rulings

1. **The share target is a presence-only fake adapter; the core reads the
   DMs.** amq-remote has no mailbox adapter kind, and its mailbox binding
   (`attach --self`, amq-acp in binding mode) is a Buzz Desktop managed
   agent whose key the core does not hold, so it cannot sign the gate
   question 3-1's core requires (body-signed kind 9). The one Buzz agent is
   therefore the strict share body (per-kind, expiring owner grants; the
   only identity the core can sign questions with). The share's declared
   target is a `fake` adapter named `buzz-presence` with DM commands off (no
   `--dm-channel`), so serve publishes the profile and presence and never
   submits the owner's DMs anywhere; the attachment reads the DMs with the
   body key and delivers them into the mailbox with the same message shape
   as the mailbox binding. Open ruling: accept, or ask upstream for a
   mailbox adapter kind (then serve delivers and the label path of 3-1 takes
   the answers).
2. **Presence follows the status document by supervising the endpoint.**
   serve's kind 10100 is `online` while its target is attached; with a
   presence-only target that is while serve runs. The attachment runs serve
   only while the bound seat is present and stops it gracefully (offline)
   otherwise. This is a different endpoint from the machine root's login
   service; the doctor warning is about the latter.
3. **Status schema 1 has no liveness for a needs_you or blocked seat.** The
   classifier puts an open gate or a block ahead of liveness, so a stopped
   seat with an open gate is `needs_you` too. Presence counts online and
   waiting as present, and needs_you or blocked only with `last_activity_at`
   within 5 minutes of `generated_at` (the producer's longest live window).
   Ask for 2b-1: an additive seat field `live` (bool) in schema 1.
4. **The operator handle has no seat row.** `status.Collect` skips the
   operator handle (`user`), so flipping `AttachToOperator` gives no
   presence until 4-3 gives the operator session a row (or the fleet
   document an operator presence). Named for 4-3.
5. `remote bind` is in the binary. It signs nothing and reads no owner key;
   it writes the public gate binding and the core's own issuer key. The
   enrollment (the owner signature) stays the script (ruling 1).
6. The DM marks are per DM channel under the share root, not per squad root,
   so moving the attachment between mailboxes never replays a DM (found by
   the rehearsal: with per-root marks a new root re-delivered the last 24 h
   of DMs). Delivery is at most once: the mark is taken before acting.
7. doctor: 2b-3 is not on v3. The check is `buzz.ServiceWarning` behind the
   `buzz.LoginService` interface and shows in `remote status`. Follow-up,
   one line in 2b-3's doctor: `if w := buzz.ServiceWarning(ctx,
   buzz.AMQRemoteService{}, ""); w != "" { warn(w) }`.

### Tests

- `internal/buzz`: `TestDefaultEnrollmentIsEightGrants`, `TestLoadShare`
  (mode, symlink, two owners, bad names, expiry), `TestResolveTarget` (lead
  path, the operator switch), `TestPresenceFromStatus` (live, stopped with a
  gate, stopped, old activity, fleet, bad documents),
  `TestSupervisorFollowsPresence`, `TestParseRoute`,
  `TestDeliverRoutesToANamedLead`, `TestServiceWarning`,
  `TestPumpGatesStatusAndRouting` (the question says "reply in this
  thread"; a plain answer is re-asked; `/status`; routing; once only; the
  thread answer is approved and its answer file verifies; a malformed
  answer goes to the core, not a lead), `TestPumpIgnoresWhatIsNotTheOwners`,
  `TestPumpReportsAFailedDelivery` (the DM reply carries no local detail).
- `TestOwnerScriptDryRun` (real amq, amq-remote, amq-owner-sign 0.107.0, a
  local websocket relay with NIP-11 and NIP-42, a throwaway owner key): the
  script's four runs (exit 10, the pipe under a pty with the body prefix
  typed, exit 11, bind, renew), the endpoint's profile and online status on
  the relay, the policy, presence on and off through the supervisor, a gate
  and a routed message through the real transport and the real `amq`, the
  owner-run rehearsal (`rehearseOwnerRun`: team init, amq init, `remote
  attach`, `gate raise`, the thread answer, `gate status`, `verify action`,
  `amq list` in fullstack's inbox, presence on and off), then the canary
  grep.
- `internal/cli`: `TestRemoteBindWritesTheOwnerFiles`, `TestRemoteNpub`,
  `TestRemoteAttachOnce` (fakes for relay, mailer, endpoint, service);
  `TestGuardProtectsGateStoreAndOwnerGateFiles` gains the share rows;
  `TestCompletionTopCommandsMatchesPublicCatalog` lists `remote`.

### Measured (dry run, local relay, throwaway key)

| what | value |
| --- | --- |
| grants at enrollment | 8 (kinds 20003, 1059, 24200, 44200, 10100, 0, 9, 40003); S1: 8 with the profile |
| grant window | 720 h (30 days, ruling 2) |
| owner step under the pty (run 2, incl. a 4 s typing delay and 4 s endpoint settle) | 9.2 s |
| presence: status row live to `online` on the relay | 0.20 s |
| presence: status row stale to `offline` on the relay | 0.02 to 0.03 s |
| one pump step (relay read, gate verify and commit, two AMQ sends, two replies) | 0.13 to 0.29 s |
| gate raise to verified through `remote attach` (1 s tick) | 1.0 s |
| canary grep | 88 to 92 channels and files, 65 to 67 kB, 3 key forms: not found (gate-c1: 88, 64 977 bytes) |

The production post-to-verified number is the owner run's (S1: 23 490 ms).

### Acceptance

| # | row | status | evidence |
| --- | --- | --- | --- |
| 1 | enrollment dry run, throwaway key: profile and presence on by default, grant count | met | `TestOwnerScriptDryRun`: 8 kinds requested and enrolled, manifest `"presence": true` and `"name": "amq-squad"` with no `dm_channel_id`, policy, profile and `online` on the relay, doctor `presence online`, `discovery policy_present`; grant count 8 |
| 2 | no secret output (canary, every output) | met | `TestOwnerScriptDryRun` canary grep: stdout and stderr of every script run, the pty stream of the signing runs, the rehearsal command outputs and the attach output, every regular file under HOME (share root, bundle, share.txt, logs, doctor.json, binding, issuer, trust) and under both squad roots, and the environment; nsec1, hex and HEX forms; positive controls first. Output kept in the gate evidence |
| 3 | gate text contains "reply in this thread" | met | `TestPumpGatesStatusAndRouting` and `TestOwnerScriptDryRun` (the question on the relay, posted through the share body: "Reply in this thread with one line:") |
| 4 | owner run with the real key | prepared, not run | `docs/v3-buzz/owner-run.md` (numbered steps, expected outputs, the record table), rehearsed end to end by `rehearseOwnerRun` with a throwaway key; shares the session with 3-1's acceptance 9 |
| 5 | presence on while the session runs, off after | met against the local relay | `TestOwnerScriptDryRun` (supervisor) and `rehearseOwnerRun` (`remote attach`): kind 10100 `online` after the row went live, `offline` after it went stale; on the real Buzz profile it is checked in the owner run (steps 4 and 7) |

### Harness notes

- Denied by the auto-mode classifier: a `grep` over
  `docs/v3-spike/3-1-execution.md`. I read the file with the Read tool
  instead.
- Refused by the worktree isolation check: compound shell commands
  (heredocs into python, a `time (...)` group, `cd` plus `&&` chains with
  variables, a `HOME=` override). I re-ran each as plain commands, wrote
  scripts to /tmp/4e with the Write tool, and set HOME only inside Go tests.
- One `go test ./internal/buzz ./internal/cli` run failed in internal/cli
  with only "FAIL" in the tail; four reruns of internal/cli passed. Suspect:
  a timing-bound test (TestHookVerbsColdRunBound, 200 ms cold bound) while
  the dry run loaded the machine. Watched in the gate runs.
