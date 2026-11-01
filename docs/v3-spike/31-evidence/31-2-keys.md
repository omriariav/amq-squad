# Brief 31-2, PR 1: the keys (add, reset, clear, answer)

Branch `v3-31-2`, from origin/v3 at `3f6c995d` (PR #829's merge: brief 31-1
PR 2, `amq-squad up`, the status additions, the two marks, the screen in
the pi extension). Authority: docs/v3.1-plan.md section 7 and
docs/v3-spike/briefs/brief-31-2-keys.md. This is PR 1 of brief 31-2; the
widen key is PR 2 (it waits for t26's typed widen block).

## 1. Q7, the keychain prompt (answered before code)

**The question.** Can this brief build an owner terminal key in the macOS
Keychain with user presence on every signature (Touch ID or the keychain
password prompt), used by a separate `amq-squad gate answer` process, its
public key registered once by a Buzz-signed gate?

**The answer: no, not inside this brief.** The fallback is taken as
written: the answer key shows the gate, posts it over Buzz and says so;
the terminal door waits for a later brief. Nothing is weakened to fit.

**Why, from the platform and from this repository** (read 2026-11-01 on
macOS 26.6.2, arm64; no keychain was read or written):

1. **The user-presence key needs the data protection keychain or CryptoKit.**
   A key whose every use requires user presence is a `SecAccessControl`
   with `kSecAccessControlUserPresence`, either on a Secure Enclave key
   (`SecKeyCreateRandomKey` with `kSecAttrTokenIDSecureEnclave`) or on a
   keychain item. A persistent key of either kind lives in the data
   protection keychain, which refuses a process without the
   `keychain-access-groups` entitlement: `security error -34018` reads
   "A required entitlement isn't present" (errSecMissingEntitlement). The
   amq-squad binary is a Go binary, ad-hoc signed by the linker with no
   entitlements; an entitlement for a keychain group needs a signed binary
   with a provisioning profile, which this project does not ship.
2. **The entitlement-free path is Swift-only.** CryptoKit's
   `SecureEnclave.P256.Signing.PrivateKey` with an access control can keep
   the key as its own `dataRepresentation` blob without the entitlement,
   but CryptoKit has no C API. Go reaches it only through a Swift helper
   compiled with swiftc. The repository builds no Swift, and the owner's
   Mac is not required to carry the Xcode command line tools.
3. **The shipped binary is built without cgo.** The binary `up` installs
   and the squad runs (R54) is built by scripts/build-pi-runtime.sh with
   `CGO_ENABLED=0` (line 114). A cgo binding to Security.framework would be
   left out of that binary, so the terminal door would exist only in test
   builds: production code kept alive by tests, which section 7.2 forbids.
   scripts/ is outside this brief's declared files, and a cgo-free binding
   (purego) is a new module dependency that is not in go.mod and cannot be
   fetched without the network.
4. **The `security` CLI cannot do it.** `security create-keypair` makes
   rsa, dh, dsa or fee keys in the legacy file keychain, guarded by an
   application ACL (`-A`, `-T`); it has no access-control or Secure
   Enclave option and no sign verb. The legacy ACL's prompt offers "Always
   Allow", after which no prompt is shown, so presence would not be on
   every signature: weakened, refused.
5. **A test can only fake it.** The rows' fake keychain could prove the
   verifier's side, but the user-presence prompt itself, the bar's whole
   point, would be checked by no test and by no build the owner runs.

**The fallback, as built** (rows 8 and 9 in their Q7-no form):

- The answer key reads the gate fresh (`amq-squad gate status --gate ID
  --json`), shows its kind, target and 16-hex typed prefix, and, when its
  question has not reached Buzz, posts it (`amq-squad gate post --gate
  ID --json`). It then says: waiting on Buzz; the terminal door is not
  built (Q7); answer in the owner's Buzz DM thread.
- The gates block shows each open typed gate with "waiting on Buzz".
- A gate that is not open (answered over Buzz, denied, expired) is refused
  by the answer key by its state; no verb runs.
- No terminal answer is written: the key never runs `gate answer`, which
  stays the Buzz verifier.

**What a later brief needs for the terminal door:** not the Keychain. The
four facts above close it; the one shape they leave open is section 8's
3.2 candidate.

## 2. Where the keys run

In the lead's own pi. Brief 31-1 PR 2 shipped the screen as a widget below
the lead's editor, in the pi extension, so there is no split pane and no
process to move: every key runs its verb from the lead's process, and a
verb that is a scheduler decision is proved under R4 by construction (its
process descends from the proved lead incarnation, as the t16 tools' do).

## 3. The design

### 3.1 The extension's input path

The keys are subcommands of the screen's existing `/squad` command:
`/squad add`, `/squad reset`, `/squad clear`, `/squad answer` (`/squad`
alone still toggles the screen; the command completes the four names). pi
runs a registered command's handler in the extension and never sends the
text to the model, so the extension's input path reads the key and the
model cannot press one. Each key gathers its input with pi's own dialogs
(`ctx.ui.input`, `ctx.ui.select`) and runs its verb through the
extension's runner (pi/extensions/lib/verbs.ts `run`: the configured
amq-squad binary, no shell). The choice of a command over raw key
bindings is the smallest diff: the command and its UI path exist and are
testable with the stand-in pi; a `registerShortcut` binding can be added
later without changing any verb.

A cancel at any dialog aborts the key with one line, `cancelled, nothing
run`, and no verb runs (Codex P2 2 on #831): pi's dialogs return nothing
on a cancel, and a cancelled files or pool dialog is never read as an
empty value. A typed pool left empty at reset also aborts, since a reset
with the old pool would return the task to dispatch.

After a key, the screen shows its outcome as one line at its foot (`key
<name>: ...`, or `key <name> refused: <the verb's line>`), then reads status
again at once. The foot also lists the keys. Collapsed, the outcome is a
notification.

### 3.2 The verbs each key runs

| Key | Input | Verb(s), from the lead's process | Scheduler decision (R4) |
| --- | --- | --- | --- |
| add | title, class, declared files, pool | `task add --title T --class C [--files F,...] [--pool P] --json` (the lead's `amq_task_add` argv) | no: a store write, as the tool |
| reset | a blocked task, then keep its pool, a seat's pool, or a typed one | `task reset ID --me LEAD [--pool P] --json` (the lead's `amq_task_reset` argv) | no: a store write, as the tool |
| clear | a seat's warning line | `scheduler clear --seat H --warning CODE --launch ID --json` (new) | yes: a fourth kind on the decision socket |
| answer | an open gate | `gate status --gate ID --json`, then `gate post --gate ID --json` when its question has not reached Buzz | no: Q7's fallback writes no answer |

The widen key is PR 2 of brief 31-2. Its seam: a task whose block carries
t26's typed widen reason gets a widen key that shows the block's files and
reason and runs `task widen ID --path ... --reason ... --json` over the
socket, as `amq_task_widen` does. With no typed block there is no widen
key; `/squad widen` is refused as an unknown key today.

The widen key's motivating case is t26 itself, on day 3: the task that
builds the typed widen route was blocked by the free-text route
(`scope_widen_needed`, with six files named in prose), and the decision
waited for the owner because R4 makes a widening the lead's pane's. That
block is the typed widen block's first live case.

### 3.3 Where the clear decision lives

internal/schedproc/clear.go. The clear is a fourth request kind on the
scheduler's decision socket (`amq-squad.clear.v1`, beside the widening and
the relaunch), proved before it is read and again after, like the others.
Its apply step is in schedproc itself (`RecordClear`). The request names a
seat, a warning code (the text before the colon on the screen) and the
seat's launch id. It is accepted only for a warning on the screen now
(Codex P2 on #831):

- the code must be a seat warning, one of `schedproc.ClearableWarnings`
  (status's seven seat warning codes; a status row keeps the two lists
  equal); a refusal code, or anything else, is `clear_not_a_warning`;
- the seat's current launch must be the one named, else
  `clear_launch_stale` (the seat relaunched since the screen showed it);
- status must show that warning for that seat on that launch now, else
  `clear_not_showing`, so nothing is cleared ahead of its condition (a
  `seat_stalled_at_dialog` cleared before any dialog would otherwise hide
  the dialog when it opened). The scheduler reads the seat's warnings
  through `status.CurrentSeatWarnings`, the same computation status shows
  (`seatLaunchState` in internal/status/collect.go), handed to the socket
  by `scheduler run` (`DecideOptions.Warnings`, in `decisionOptions`;
  schedproc cannot import status). Without it every clear is refused.

Then the clear is appended
to `<root>/extensions/amq-squad/scheduler/clears.jsonl` and logged as a
`warning_cleared` decision. Status reads clears.jsonl whole (one line per
owner acknowledgement) and lists a seat's cleared warnings under
`acknowledged_warnings` for that launch only. The clear touches nothing
else: no seat state, no gate, no task.

The verb is `amq-squad scheduler clear` (internal/cli/scheduler_clear.go,
dispatched from scheduler_run.go beside `scheduler relaunch`), with no
`--me`: the scheduler proves the lead from the process.

### 3.4 The answer key (Q7's fallback)

The gates block shows each open typed gate with its 16-hex typed prefix
and "waiting on Buzz". The answer key reads the gate fresh with `gate
status`, so a gate answered over Buzz since the last poll is refused by its
state (`gate_not_open`), with no verb after it; an open gate is shown with
its kind, typed class, target and prefix; a question that never reached
Buzz is posted (`gate post`, which posts once); the line then says the gate
waits on Buzz and the terminal door is not built. The key never runs
`gate answer`, which stays the verifier of the owner's Buzz reply.

**The same gate on both doors** (plan reviewer). The answer key's line
shows the gate as the owner reads it in Buzz: its action (the kind), its
typed class, its target and its 16-hex typed prefix, `<id> <action>
(<typed class>) <target> prefix <16 hex>: ... waiting on Buzz ...`
(pi/extensions/lib/keys.ts, `answer`, the `shown` line). The gates block
shows the id, the action and the same 16-hex prefix; the target is not in
status's gate rows, so the block does not carry it, and the key reads it
from `gate status`. Row 9 asserts the key's line, kind, typed class,
target and prefix for each of the five typed actions (pi/test/keys.test.ts,
"answer, Q7 no: every typed gate kind waits on Buzz ...").

## 4. Rows

| Row | Test |
| --- | --- |
| 1 | pi `each key runs its verb with the lead's own amq-squad binary from the lead's process`; cli `TestClearKeyIsProvedFromTheLeadsDescendant` (the clear from the lead's descendant is proved over the real socket and recorded; from a process outside the lead's descent it is refused `not_lead` and nothing is recorded) |
| 2 | pi `the lead's model tool list carries no key verb and no gate-answer verb` (the lead's tool names are the tip's, exactly; no tool names a gate answer or a clear) |
| 3 | pi `add: the task goes to task add with its title, class, declared files and pool`; cli `TestAddKeysArgvAddsTheTaskWithItsFilesAndPool` (that argv adds the task to a store with its files and pool) |
| 4 | not built: section 5 |
| 5 | PR 2 (the widen key waits for t26) |
| 6 | pi `reset: task reset, --pool for a picked pool, and a refusal by name` (`repin_after_attempt` and `reset refused` shown as the verb printed them) |
| 7 | pi `clear: the acknowledgement names the warning and the launch; the line leaves the screen; the seat's state does not`; status `TestAClearedWarningLeavesTheScreenForItsLaunchOnly` (acknowledged, state, reason code, bucket, live and outcome unchanged, another seat untouched, the next launch's same condition a warning again, a clear of the earlier launch refused); Codex P2 1: schedproc `TestRecordClearAcceptsOnlyAWarningShowingOnTheCurrentLaunch` (`clear_not_a_warning` for refusal codes, `clear_not_showing`, stale, malformed), status `TestAWarningCannotBeClearedBeforeItShows`, `TestTheClearableCodesAreStatusSeatWarnings`; cli `TestClearKeyIsProvedFromTheLeadsDescendant` runs the socket as `decisionOptions` wires it |
| Codex P2 2 | pi `a cancel at any dialog aborts the key and runs no verb` (add cancelled at each of its four dialogs, reset at the pool dialog and at the typed pool, clear and answer at their pick: no verb runs) |
| 8 | Q7 no: pi `answer, Q7 no: an unposted question is posted over Buzz; a gate answered over Buzz is closed and refused`; the model cannot write an answer (row 2) |
| 9 | Q7 no: pi `answer, Q7 no: every typed gate kind waits on Buzz and no terminal answer is written` (tag, github_release, external_send, credential_change, spawn) |

Each row was checked to fail with its change reverted (the status split,
the socket's clear kind, the reset's `--pool`, the gate line's "waiting on
Buzz"; for Codex P2 1, the "showing" check and the status wiring in
`decisionOptions`); row 2 fails whenever a tool is added.

No test reaches the network, the real HOME, tmux, AMQ, Buzz or a keychain:
the pi rows run the stand-in pi with a scripted runner; the cli rows run
the verb in a child of the test process against a temporary root.

## 5. Row 4, the seat add, is not built: the brief's premise and the tip disagree

File:line references in this section are at origin/v3 `3f6c995d`.

The brief says the add key writes the squad.yaml line through `team apply`
with its digest, and "the new seat starts by the roster relaunch". On this
tip the relaunch cannot start it, and its launch budget cannot start at
the key press:

- **The relaunch relaunches the roster the scheduler started with.**
  `scheduler run` compiles squad.yaml once (`squadOfProject` at
  internal/cli/scheduler_run.go:150, then `slots.Roster(*lead)` at :206) into
  `scheduler.Config.Roster` and `Slots.Squad`; the relaunch's
  `launchRoster` iterates `s.Config.Roster` (internal/scheduler/roster.go
  :227) and `PlanRoster` looks each handle up in `Slots.Squad.Seats`
  (internal/schedproc/roster.go:40, 50). A seat added to squad.yaml after
  the scheduler started is in neither.
- **The relaunch stamps the scheduler's start, not the key press.** Every
  launch's start mark is `seatlaunch.Options.Start`, set once from
  `AMQ_SQUAD_START` at `scheduler run` (scheduler_run.go:213 and 273,
  `launchStart`); `RelaunchRequest` carries only a reason
  (internal/schedproc/decide.go). The start stamp row 4 asks for needs the
  request to carry the key press and the relaunch to hand it to its
  launches.

The fix is outside this brief's declared files: internal/scheduler
(re-read the roster on a relaunch), internal/schedproc beyond the clear
kind (the relaunch request's start, the slots' squad) and
internal/cli/scheduler_run.go and internal/seatlaunch (a per-launch
start). A key that wrote squad.yaml and requested a relaunch that does not
start the seat would tell the owner something false, so the add key adds
tasks only, and the seat half waits for the lead to widen the files.

## 6. Owner-run rows

From the owner's terminal, with this branch's tagged build installed by
`up` (R54; the build at the tag, as in 31-1-pr2-up-status-screen.md
section 4):

```sh
git worktree add ~/scratch-v31 v3.1.0 && cd ~/scratch-v31 && scripts/build-pi-runtime.sh all
cd ~/code/amq-squad && amq-squad up --binary ~/scratch-v31/build/pi/amq-squad
tmux attach -t "$(tmux list-sessions -F '#{session_name}' | grep '^squad-lead-' | head -1)"
```

1. **The terminal key's registration through one Buzz-signed gate.** Not
   applicable: Q7 is answered no, so there is no terminal key to register.
   In its place, one typed gate shown at the screen as waiting on Buzz.
   Raise a gate that authorizes nothing real, from the repository:

   ```sh
   amq-squad gate raise --gate keys-check-1 --action tag --target "git tag v0.0.0-keys-check"
   ```

   In the lead's pi: `/squad answer`, pick `keys-check-1`. Expect the
   key's line: `keys-check-1 tag (release) git tag v0.0.0-keys-check
   prefix <16 hex>: waiting on Buzz: the terminal door is not built (Q7);
   ...`, and the gates block's line for it ending `waiting on Buzz`.
2. **One typed gate answered at the screen with the Touch ID or password
   prompt.** Not applicable (Q7 no): no answer is written at the terminal.
3. **One answered over Buzz with the screen showing it closed.** Deny
   `keys-check-1` from the phone, in its Buzz thread, with its typed
   prefix (a deny authorizes nothing). Within a poll the gate leaves the
   gates block. Then `/squad answer` lists no open gate; `amq-squad gate
   status --gate keys-check-1` prints `denied`.
4. **The launch budget from the add key on the day's record.** Not
   applicable in this PR: the seat add is not built (section 5). The task
   add can be checked instead: `/squad add`, title `keys check`, class
   `plan`, files `docs/keys-check.md`, pool empty; then

   ```sh
   amq-squad task list --json | jq '.data.tasks[] | select(.title=="keys check") | {id, state, declared_files, pool}'
   ```

5. **The clear, on a live warning line** (any seat with a warning, such as
   a Codex seat's `wake_off`): `/squad clear`, pick the line. Then

   ```sh
   amq-squad status --json | jq '.data.seats[] | {handle, launch: .launch.launch_id, warnings, acknowledged_warnings, state}'
   tail -n 2 .agent-mail/*/extensions/amq-squad/scheduler/clears.jsonl
   grep warning_cleared .agent-mail/*/extensions/amq-squad/scheduler/decisions.jsonl | tail -n 1
   ```

   Expect the warning under `acknowledged_warnings`, the seat's `state`
   as before, and one `warning_cleared` decision.

## 7. Scope notes and where the brief and the code disagree

- **Declared files.** One file outside the list:
  internal/cli/scheduler_run.go gains the `clear` case in `runScheduler`
  and its usage lines; the verb itself is a new file,
  internal/cli/scheduler_clear.go. The brief declared internal/schedproc
  for the clear decision kind but no CLI home for the verb that sends it;
  `scheduler clear` sits beside `scheduler relaunch`, the other decision
  the lead sends over the socket. internal/gatestore, internal/gateverify
  and internal/cli/team.go, task.go, task_widen.go, gate.go, gate_store.go
  are unchanged: Q7's fallback needs only `gate status` and `gate post`,
  and no verb needed a new flag.
- **Row 1's "the scheduler accepts the decision under R4" holds for one of
  this PR's keys.** On the tip only the widening, the relaunch and now the
  clear are scheduler decisions over the socket. `task add` and `task
  reset` are store writes, the same verbs the lead's `amq_task_add` and
  `amq_task_reset` run; there is no R4 check on them to accept or refuse.
  The add and reset keys run them from the lead's process, as the tools
  do.
- **A clear from outside the lead's descent is refused `not_lead`.** Row 1
  of the brief names `origin_unknown` for the verb from a seat's shell.
  The socket (decide.go `origin`) answers `not_lead` when the sender's
  parent chain is read and does not reach the lead's recorded incarnation
  (a seat's pane shell is a sibling under tmux, not a descendant), and
  `origin_unknown` only for an origin it cannot read or prove (no kernel
  peer, an unreadable start stamp or chain, no readable lead record). Row 1
  tests `not_lead`.
- **The answer key's "second answer from the screen is refused".** With
  no terminal door there is no second answer to refuse; the key reads the
  gate fresh and refuses a gate that is not open by its state.
- **Row 4** is section 5.
- **The day-3 rule on the lead handle (plan reviewer), for a later PR.**
  No file a seat reads names the lead handle as a literal: the handle comes
  from squad.yaml through roleText at launch. On day 3,
  .amq-squad/team-rules.md lines 15 and 57 said "The lead is `cto`" while
  the lead ran as "lead" (no `--lead` was passed on 3.0.2), and the
  reviewer seat on t26 reported "lead inactive/unregistered". A row that
  greps the compiled seat texts for a literal lead handle belongs to a
  later PR; this PR adds no seat text.
- **The commit trailer** names the model this session ran on (Opus 5.5);
  the brief's template named Fable 5.1.

## 8. The 3.2 candidate for the terminal door (not work for this brief)

The plan reviewer's line, so nobody reopens the Keychain: the four facts
of section 1 close every Keychain and Secure Enclave shape for this
binary (no entitlement, no Swift, no cgo, no presence on every use from the
`security` CLI). The one shape they leave open is **a signature made by a
separate agent process that the owner confirms on every use, reached from
Go without cgo**:

- **ssh-agent over its socket.** `amq-squad gate answer` asks the agent
  for a signature over `SSH_AUTH_SOCK` with golang.org/x/crypto/ssh/agent.
  The key is added with `ssh-add -c`, so the agent asks the owner to
  confirm every signature. The key never enters amq-squad's process or
  the lead's; the agent holds it. Read 2026-11-01: golang.org/x/crypto
  v0.58.0 is in this machine's module cache with its `ssh/agent` package,
  and go.sum already carries its hashes, although the main module does
  not require it today (`go mod why -m golang.org/x/crypto`). If a later
  build cannot take it offline, this candidate waits too.
- **Or a FIDO security key** under an OpenSSH that carries security-key
  support (`ecdsa-sk` or `ed25519-sk`): the touch is the presence, on every
  signature, by the key's own hardware. Read 2026-11-01: the system
  OpenSSH is 10.3p1 and ships /usr/libexec/ssh-sk-helper; whether Apple's
  build carries the internal FIDO middleware is the row's first check.
- **Registration, as before:** the public key is registered once through
  a Buzz-signed gate, so the trust chain starts at the phone; gateverify
  accepts the Buzz key or that registered key; the gate store records the
  door (`terminal`) and the key id (the key's SSH fingerprint).
- **The owner setup is the line's first row:** on macOS, `ssh-add -c`
  needs an askpass program for the agent's confirmation (macOS ships
  none; `SSH_ASKPASS` names one, which shows the gate's kind, target and
  16-hex prefix before the owner confirms), or the FIDO key itself. Without
  that setup, the answer key keeps today's fallback: it shows the gate,
  posts it over Buzz and says so.

A candidate for 3.2, carried as a follow-up; nothing in this PR builds it.
