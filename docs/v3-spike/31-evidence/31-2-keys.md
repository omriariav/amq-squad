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

**What a later brief needs for the terminal door:** a signed amq-squad
build with a keychain-access-groups entitlement (or a Swift helper with
CryptoKit, built and shipped beside the binary), the release script's cgo
switch, a gate kind that registers the terminal public key over Buzz, and
gateverify accepting that key with the door and key id in the store.

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

### 3.3 Where the clear decision lives

internal/schedproc/clear.go. The clear is a fourth request kind on the
scheduler's decision socket (`amq-squad.clear.v1`, beside the widening and
the relaunch), proved before it is read and again after, like the others.
Its apply step is in schedproc itself (`RecordClear`), so `scheduler run`
needs no new hook: the request names a seat, a warning code (the text
before the colon on the screen) and the seat's launch id; the seat's staged
launch record must carry that launch id (else `clear_launch_stale`: the
seat relaunched since the screen showed the line); the clear is appended
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

## 4. Rows

| Row | Test |
| --- | --- |
| 1 | pi `each key runs its verb with the lead's own amq-squad binary from the lead's process`; cli `TestClearKeyIsProvedFromTheLeadsDescendant` (the clear from the lead's descendant is proved over the real socket and recorded; from a process outside the lead's descent it is refused `not_lead` and nothing is recorded) |
| 2 | pi `the lead's model tool list carries no key verb and no gate-answer verb` (the lead's tool names are the tip's, exactly; no tool names a gate answer or a clear) |
| 3 | pi `add: the task goes to task add with its title, class, declared files and pool`; cli `TestAddKeysArgvAddsTheTaskWithItsFilesAndPool` (that argv adds the task to a store with its files and pool) |
| 4 | not built: section 5 |
| 5 | PR 2 (the widen key waits for t26) |
| 6 | pi `reset: task reset, --pool for a picked pool, and a refusal by name` (`repin_after_attempt` and `reset refused` shown as the verb printed them) |
| 7 | pi `clear: the acknowledgement names the warning and the launch; the line leaves the screen; the seat's state does not`; status `TestAClearedWarningLeavesTheScreenForItsLaunchOnly` (acknowledged, state, reason code, bucket, live and outcome unchanged, another seat untouched, the next launch's same condition a warning again, a clear of the earlier launch refused); schedproc `TestRecordClearRefusesWhatIsNotAWarningOnALaunch` |
| 8 | Q7 no: pi `answer, Q7 no: an unposted question is posted over Buzz; a gate answered over Buzz is closed and refused`; the model cannot write an answer (row 2) |
| 9 | Q7 no: pi `answer, Q7 no: every typed gate kind waits on Buzz and no terminal answer is written` (tag, github_release, external_send, credential_change, spawn) |

Each row was checked to fail with its change reverted (the status split,
the socket's clear kind, the reset's `--pool`, the gate line's "waiting on
Buzz"); row 2 fails whenever a tool is added.

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
- **A seat's shell is refused `not_lead`, not `origin_unknown`.** Row 1
  names `origin_unknown` for the verb from a seat's shell. The socket
  answers `not_lead` when the sender's parent chain is read and does not
  reach the lead (a seat's pane shell is a sibling under tmux, not a
  descendant), and `origin_unknown` only when the chain or the lead's
  record cannot be read (decide.go `origin`). The row tests `not_lead`.
- **The answer key's "second answer from the screen is refused".** With
  no terminal door there is no second answer to refuse; the key reads the
  gate fresh and refuses a gate that is not open by its state.
- **Row 4** is section 5.
- **The commit trailer** names the model this session ran on (Opus 5.5);
  the brief's template named Fable 5.1.
