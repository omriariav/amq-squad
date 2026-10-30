# amq-squad 3.0.1, task t9: seats launched with AMQ wake enabled (brief 3.0.1-t9)

Branch `v3-301-t9`, cut from `origin/v3` at a1a67151. One implementer, scratch worktree, two commits: the change, then the reviewer ruling that gates the wake (below). The rule: the scheduler's doorbell is the seat's next turn (`seatlaunch.WakeRule`). The defect (session v31, seat dev): every seat launched with wake disabled, so no AMQ wake ran in the pane and a scheduler doorbell sat in the inbox until the owner typed a line.

All AMQ citations are to the pinned module, `github.com/avivsinai/agent-message-queue@v0.116.2` (paths relative to its root).

## What AMQ needs for a pane to be woken

1. **Who runs the wake.** `amq launch` types `amq coop exec --root R --me H [options] <wrapper> -- <wrapper args> <provider> <args>` into each pane (internal/launch/backend_common.go:48-60, internal/launch/commands.go:110-122). coop exec starts `amq wake` as a background child unless it gets `--no-wake` (internal/cli/coop_exec_unix.go:204-213, 255-300). The wake is bound to the coop exec process and exits when that process exits (coop_exec_unix.go:215-217).
2. **What the intent controls.** `execution.wake.mode = disabled` becomes `--no-wake --managed-no-wake-reason <audit_reason>`. `enabled` passes `--require-wake` when `require_wake` is true and `--wake-inject-mode <m>` when an injector is set (internal/launch/commands.go:124-150). `--require-wake` makes a wake that cannot start fail the launch instead of warning (coop_exec_unix.go:1361-1367). amq-squad 3.0 sent `disabled`, so the pane ran with `--no-wake`.
3. **Grammar.** A disabled wake needs an audit reason and forbids `require_wake` and any injector. An enabled wake forbids an audit reason. `claude-native` requires `require_wake` (internal/launch/execution_options_grammar.go:24-55).
4. **Mode per runtime.** With no injector chosen, the wake types the mode the harness table gives. Claude Code and Codex get `raw`, because their Ink input drops an Enter that follows a bracketed paste. pi gets `paste` (internal/harness/harness.go:61-64, 81-85). coop exec resolves that mode and passes `--harness` from the managed ticket's provider, not from the wrapper's file name (internal/cli/coop_exec_named.go:30-36 and 59-64, coop_exec_unix.go:280). So the seat-exec wrapper does not hide the harness from the wake. Codex also gets the LF prelude before its submit (harness.go:68-71, 82).
5. **claude-native is closed to seats.** It requires a fresh managed Claude 2.1.311 launch with no wrapper and no custom `CLAUDE_CONFIG_DIR` (internal/cli/wake_claude_native_unix.go:52-60). Every seat runs behind seat-exec, and a Claude seat has its own config dir.
6. **What runs on the host.** Nothing beyond the amq binary is needed for the wake to ring. The wake runs `--baseline-existing --interrupt-cmd none --refuse-unverified-wake` (coop_exec_unix.go:1369-1400). Mail present when the wake starts never rings; only later mail does (docs/wake-operations.md:54-59). Retries follow the input ladder: 5 s doubling to 2 min, four attempts per unchanged cohort, then parked until new mail (docs/wake-doorbell-acknowledgement.md:56-92).
7. **What keeps a doorbell from answering a dialog.** These are AMQ's dialog hooks, and they are a guard, not a wake requirement.
   - **Codex.** `amq wake install-dialog-hook --harness codex` writes marked entries for six events into `$CODEX_HOME/hooks.json`. The default home is `~/.codex`. The owner must trust the entries in Codex `/hooks` (docs/wake-operations.md:458-492, internal/cli/wake_dialog_codex_unix.go:37-44 and 182-214). Without them, AMQ's own check says "a wake doorbell can answer a Codex approval prompt" (wake_dialog_codex_unix.go:240-295). amq-squad's Codex seats run `--ask-for-approval on-request` (seatlaunch.CodexSandboxArgs). A doorbell at an approval prompt cancels the pending command and runs as a new prompt (docs/wake-operations.md:460-461). Seat-exec does not override `CODEX_HOME` or `HOME` (internal/seatexec/env.go), so Codex seats read the owner's hooks file. The hooks find the mailbox through `AM_ME` and `AM_ROOT`, which seat-exec sets (seatexec_unix.go:512-516).
   - **Claude Code.** `amq wake install-dialog-hook` writes `~/.claude/settings.json` (docs/wake-operations.md:166-176). A Claude seat runs under its own `CLAUDE_CONFIG_DIR`, which seat-exec sets from `internal/claudeseat` (seatexec_unix.go:193-198, 521-522). The owner's install therefore never reaches a seat. A Claude seat runs `dontAsk`, so no permission dialog opens. AskUserQuestion and ExitPlanMode still can, and the doorbell's Enter could answer them. Follow-up: claudeseat would write AMQ's `AMQ_DIALOG_HOOK` entries into the seat's settings. That package is not declared for this task.
8. **Codex notify fan-out is not a wake requirement.** AMQ adds its own `-c notify=[amq, __codex-notify, ...]` override to every Codex seat for conversation-identity capture (internal/launch/adapter_codex.go:186-211). `amq __codex-notify` then forwards to the owner's own `notify` line when one exists (internal/cli/codex_notify.go:25-70). The existing doctor row "codex notify fan-out" is unchanged.
9. **What Prepare and Apply report.** Prepare's preview echoes each runnable participant's execution options: `require_wake`, wake mode, audit reason and injector (launchapi/prepare.go:200-203 and 249-258). `ApplyResultV1` and its observations carry no wake field (launchapi/types.go:304-314 and 367-393). The live wake state is in `<root>/agents/<handle>/.wake.lock` and its siblings, which `amq wake check --me H --json` reads (docs/wake-operations.md:9-52).
10. **Close.** No explicit close is needed. coop exec re-execs `amq __launch-exec`, which preserves the PID (coop_exec_unix.go:1083-1108). seat-exec then calls `syscall.Exec` on the provider (internal/seatexec/seatexec_unix.go:608). So the wake's owner PID is the seat's provider PID, and the wake exits when it does (coop_exec_unix.go:215-217). `amq wake retire` applies only to inject-via wakes (docs/wake-operations.md:844-881).
11. **pi.** The same coop exec path would wake a pi seat with `paste`. Its dialog guard runs only when pi loads AMQ's pi extension, `integrations/pi/amq-bridge.ts` (docs/wake-operations.md:587-599). amq-squad's embedded runtime does not load it. A pi seat therefore stays as it was, with wake disabled, under a new audit reason that says why (`seatlaunch.PiWakeAuditReason`).
12. **Startup screens with a wrapper.** coop exec decides its startup-prompt hold from `c.binaryPath`. For a wrapped seat that is the amq-squad wrapper, not the provider (coop_exec_unix.go:591 and 994, coop_exec_trust.go:74-75). Managed launches also skip the Codex launch decision (coop_exec_unix.go:940). So no Startup marker is ever written for a seat, and a doorbell that rings while a trust, update or hook-review screen shows could answer it. Claude seats are pre-trusted in their seeded config (internal/claudeseat/claudeseat.go:78-79). Codex trust is the existing doctor row "codex trust". This is an AMQ upstream item.

## The change

- `internal/seatlaunch/compile.go`
  - `SeatWake(runtime)` sets each seat's policy. Claude and Codex get `require_wake` true, wake enabled and injector `raw`. pi stays disabled with `PiWakeAuditReason`.
  - `WakeRule` names the 3.0.1 rule. It cannot be the audit reason of an enabled wake, so it lives in the code, in the refusal text and in this note.
  - `SeatWakeFacts` and `admittedWakes` read AMQ's Prepare echo per seat. A listed seat whose admitted `require_wake`, mode or injector differs from the compiled one refuses the launch before Apply, as `wake_not_admitted`. This catches a future AMQ that disables the wake, for example the ACP inbox-consumer override (internal/launch/reconcile.go:1232-1257). An older AMQ that lists no execution options yields no facts.
- `internal/launchintent/launchintent.go` refuses an injector on a disabled wake before any AMQ call, naming the seat. AMQ would refuse it at Prepare anyway (execution_options_grammar.go:32-34).
- `internal/seatlaunch/run.go` and `report.go` change by a few lines: the admitted-wake check after the argument-form check, `SeatState.Wake` on each seat of the result, and one report line per seat (`seat H wake: mode enabled, require_wake true, injector raw (AMQ prepare)`). The declared files name only compile.go in this package, while item 3 of the brief names internal/seatlaunch. The lead can keep or drop these lines.
- `internal/cli/doctor_host.go` adds the row "codex wake dialog hook" to doctor's Codex checks, which run when codex is on the host. The row is read-only and has three outcomes.
  - It warns `codex_dialog_hook_disabled` when `[features] hooks` or `codex_hooks` is false in the Codex `config.toml`.
  - It warns `codex_dialog_hook_missing` naming the events that lack an `AMQ_DIALOG_HOOK=1` entry in `hooks.json`.
  - It reports ok when all six events are present.
  - It prints event and key names only, never a command or a value.
  - The fix line is `amq wake install-dialog-hook --harness codex, then open /hooks in Codex and trust the AMQ_DIALOG_HOOK entries`.
  - Whether Codex trusts the entries is Codex's hash, and `amq doctor --ops` reads it.

### The reviewer ruling (second commit): the wake is gated on the seat never prompting

Raw typing into a pane means a doorbell that lands on a permission prompt answers it unattended. So the wake is required only for a seat that never prompts unattended (`seatlaunch.SeatWake(runtime, permissionMode, codexHookReady)`).

- **Claude Code.** The wake is required only when the seat's launch permission mode is `dontAsk`, which never prompts and is what `internal/claudeseat` writes today. Any other mode keeps the wake off with `ClaudeWakeAuditReason`. That reason names the missing AMQ dialog hook: the seat's own config dir carries none (finding 7).
- **Codex.** The wake is required only when AMQ's Codex dialog hook entries are present on the host. Otherwise it is off with `CodexWakeAuditReason`, which names the owner's step. The host step is a precondition the launch checks:
  - The detection is one read-only function, `seatexec.ReadCodexDialogHook` in the new `internal/seatexec/codexhook.go`.
  - The launch calls it through `Options.CodexDialogHook`, and doctor's row calls it too.
  - It reads key and event names only, never a command or a value.
  - The launch reads it only for a squad with a Codex seat, on the Codex home the seats read: `$CODEX_HOME` of the launch environment, else `<home>/.codex`.
  - The result reaches Compile through `Squad.CodexDialogHookReady`. That field is `json:"-"`, so a seat file cannot claim it.
  - Presence is what the launch can check. Whether Codex trusts the entries is Codex's own hash, which `amq doctor --ops` reads, so trusting them stays an owner step.
- **pi.** Unchanged: off, with `PiWakeAuditReason`.
- **Report.** The seat's wake line now ends with the audit reason when the wake is off. A Codex seat without the hook therefore shows the owner's step in the launch report.

### Not done: the per-seat launch record (stop and report)

The per-seat `launch.json` is `launch.Record` in `internal/launch/record.go`, which is not declared. The scheduler's own `internal/schedproc` `launch.json` is the scheduler process record and holds no seats. To record the wake there, the following changes are needed.

- `internal/launch/record.go`: add `Wake *Wake \`json:"wake,omitempty"\`` to `Record`, with `Wake{RequireWake bool; Mode, Injector, Source string}`.
- `internal/seatlaunch/run.go`: `Stage`, or `recordApply` at run.go:462, writes `admittedWakes(...)[handle]` into each staged record. The values are already computed in `Launch`.
- The proof (`launch.ProofEntry`) needs nothing. The wake shares the seat's PID (finding 10).

## Rows

| rule | row (package) | fails when reverted |
| --- | --- | --- |
| Claude and Codex compile `require_wake` true, mode enabled, injector raw; pi disabled with its reason; AMQ's validation passes | TestSeatWakePerRuntime (seatlaunch) | `SeatWake` returning the 3.0 disabled line, a nil injector, or an empty pi reason |
| the result carries the wake AMQ admitted (Prepare echo from a fake AMQ through `Options.RunAMQ`); the report prints it | TestLaunchRecordsTheWakeAMQAdmitted (seatlaunch) | dropping the attach in run.go, or the report line |
| a preview that admits another wake (disabled, not required, paste, mode only) refuses before Apply | TestLaunchRefusesAWakeAMQDidNotAdmit (seatlaunch) | dropping the check, or any one of its three field compares |
| an injector needs a required wake | TestCompileWakeInjectorNeedsARequiredWake (launchintent) | dropping the guard |
| doctor row: missing, partial, installed, `features.hooks = false`, no value printed | TestCodexWakeDialogHookRow (cli) | dropping the missing-events or features branch |
| doctor row is wired into the Codex checks on `CODEX_HOME` | TestCodexWakeDialogHookRowIsInDoctor (cli) | dropping the append in `codexChecks` |
| ruling: Claude wake only in `dontAsk`, Codex wake only with the hook, pi off; both branches through `SeatWake` and through Compile; the Codex reason names the owner's step | TestSeatWakeIsGatedOnTheSeatNeverPrompting (seatlaunch) | either gate forced open, or Compile not passing the readiness |
| ruling: the launch reads the hook through the seam once, on `CODEX_HOME`, only for a squad with a Codex seat; a missing hook launches the Codex seat without its wake and the report names the step | TestLaunchChecksTheCodexHookBeforeTheCodexWake (seatlaunch) | the read ignored, `CODEX_HOME` ignored, the read made for any squad, `Ready` ignoring missing events, or the reason dropped from the report |
| the shared detection: AMQ's marker only (the owner's own hook on an event does not count), `features.hooks` | TestCodexWakeDialogHookRow (cli) | the marker test or the features read removed |

The brief asked for a row with wake facts "from a fake apply result". AMQ's ApplyResult has no wake field (finding 9), so the row reads the facts from the fake Prepare echo, which is where AMQ reports them.

Each revert was applied by hand to a copy, the row was run and failed, and the file was restored. The canary strings in the doctor row were checked against `redact.RealCredentialShapes` and match none. No test reads a real Codex home. As a tripwire, the default hook read was made to panic on the real `~/.codex` or `$CODEX_HOME`, and the full suite passed; the tripwire was then removed. No row reaches the network, HOME or tmux: the doctor rows use temp Codex homes, and the seatlaunch rows fake AMQ.

Gate: `go build ./... && go vet ./internal/seatlaunch ./internal/launchintent ./internal/seatexec && go test -count=1 ./internal/seatlaunch ./internal/launchintent ./internal/seatexec ./internal/cli` passed. The full `go test -count=1 ./...` also passed. `scripts/manifest-check.sh` failed on the grown line counts of compile.go, report.go and doctor_host.go, so their `current_lines` and `target_lines` in `docs/v3-manifest.yaml` were updated. That file is outside the declared files; the edit is mechanical, and the check passes again.

## Owner-run row (3.0.1 owner run)

This cannot run here: it needs a live AMQ, tmux and real seats. It uses the v31 roster in `squad.yaml`, run on the 3.0.1 build with this commit. `SEATS` is the JSON seat list the v31 start passed to `--seats`; `amq-squad launch` does not read squad.yaml directly.

```sh
# The rule (reviewer ruling): a seat gets its wake only if it never prompts unattended.
# Claude seats: only in dontAsk (the launch's mode). Codex seats: only with AMQ's Codex
# dialog hook on the host; without it the launch starts them with no wake and says why.

# Host step 1 (one time): install AMQ's Codex dialog hook into the Codex home the seats read.
amq wake install-dialog-hook --harness codex
# Host step 2 (one time): open /hooks in Codex and trust the AMQ_DIALOG_HOOK entries
# (or choose "Trust all" at the next Codex start). The launch checks presence only;
# trust is Codex's own hash, so check it with:
amq doctor --ops
amq-squad doctor | grep 'codex wake dialog hook'     # expect: ok ... all 6 events, Codex seats launch with an AMQ wake

# 1. The admitted wake, before anything starts.
amq-squad launch --seats "$SEATS" --dry-run --json \
  | jq '.prepare.preview.participants[] | select(.runnable) | {handle, execution}'
# expect senior-dev, scribe (Claude, dontAsk): require_wake true, wake.mode "enabled", injector raw
# expect qa (Codex): the same with the hook installed; without it wake.mode "disabled"
#   and an audit reason naming 'amq wake install-dialog-hook --harness codex'
# expect cto (pi): wake.mode "disabled" with the pi audit reason

# 2. Start the squad as in the v31 start (the scheduler launches the seats).
amq-squad scheduler run --seats "$SEATS" --lead cto
# expect in the launch report: "seat qa wake: mode enabled, require_wake true, injector raw (AMQ prepare)"

# 3. Each Claude Code and Codex seat has a live wake.
amq wake check --me qa --root "$PWD/.agent-mail/v31" --json      # live_wake true, health_status healthy
amq wake check --me senior-dev --root "$PWD/.agent-mail/v31" --json

# 4. Let qa finish its first turn and sit idle, then give it a task.
amq-squad task add --title "t9 live row: claim this task and report its id" --class <a class qa's pool serves> --pool codex:gpt-5.6-sol:high
```

Pass criteria:

- **A new turn without a keystroke.** Within the debounce and first input attempt (a few seconds) of the scheduler's doorbell landing in `agents/qa/inbox/new`, qa's pane shows a new turn, and no key is typed by the owner.
- **The turn claims the task.** `amq-squad task show <id> --json` shows qa as the lease holder.
- **The wake logged the attempt.** `agents/qa/.wake.log` and `agents/qa/notification-attempts.jsonl` record the input attempt.

Repeat step 4 for senior-dev, a Claude seat. Then close a seat and check that `amq wake check --me qa ...` reports no live wake: the wake exited with the seat.

Watch item: a doorbell that lands while Codex still shows a startup screen could answer it (finding 12). Launch only on a trusted root, which the doctor row "codex trust" checks, and do not rely on a doorbell before the seat's first turn ends.
