# amq-squad 3.0.1, task t9: seats launched with AMQ wake enabled (brief 3.0.1-t9)

Branch `v3-301-t9`, cut from `origin/v3` at a1a67151. One implementer, scratch worktree, three commits: the change, the first gating ruling (the Codex dialog hook as a wake precondition, the shared detection), then the reviewer rulings and Codex's findings (below). The rule: the scheduler's doorbell is the seat's next turn (`seatlaunch.WakeRule`). The defect (session v31, seat dev): every seat launched with wake disabled, so no AMQ wake ran in the pane and a scheduler doorbell sat in the inbox until the owner typed a line.

All AMQ citations are to the pinned module, `github.com/avivsinai/agent-message-queue@v0.116.2` (paths relative to its root).

## What AMQ needs for a pane to be woken

1. **Who runs the wake.** `amq launch` types `amq coop exec --root R --me H [options] <wrapper> -- <wrapper args> <provider> <args>` into each pane (internal/launch/backend_common.go:48-60, internal/launch/commands.go:110-122). coop exec starts `amq wake` as a background child unless it gets `--no-wake` (internal/cli/coop_exec_unix.go:204-213, 255-300). The wake is bound to the coop exec process and exits when that process exits (coop_exec_unix.go:215-217).
2. **What the intent controls.** `execution.wake.mode = disabled` becomes `--no-wake --managed-no-wake-reason <audit_reason>`. `enabled` passes `--require-wake` when `require_wake` is true and `--wake-inject-mode <m>` when an injector is set (internal/launch/commands.go:124-150). `--require-wake` makes a wake that cannot start fail the launch instead of warning (coop_exec_unix.go:1361-1367). amq-squad 3.0 sent `disabled`, so the pane ran with `--no-wake`.
3. **Grammar.** A disabled wake needs an audit reason and forbids `require_wake` and any injector. An enabled wake forbids an audit reason. `claude-native` requires `require_wake` (internal/launch/execution_options_grammar.go:24-55).
4. **Mode per runtime.** With no injector chosen, the wake types the mode the harness table gives. Claude Code and Codex get `raw`, because their Ink input drops an Enter that follows a bracketed paste. pi gets `paste` (internal/harness/harness.go:61-64, 81-85). coop exec resolves that mode and passes `--harness` from the managed ticket's provider, not from the wrapper's file name (internal/cli/coop_exec_named.go:30-36 and 59-64, coop_exec_unix.go:280). So the seat-exec wrapper does not hide the harness from the wake. Codex also gets the LF prelude before its submit (harness.go:68-71, 82).
5. **claude-native is closed to seats.** It requires a fresh managed Claude 2.1.311 launch with no wrapper and no custom `CLAUDE_CONFIG_DIR` (internal/cli/wake_claude_native_unix.go:52-60). Every seat runs behind seat-exec, and a Claude seat has its own config dir.
6. **What runs on the host.** Nothing beyond the amq binary is needed for the wake to ring. The wake runs `--baseline-existing --interrupt-cmd none --refuse-unverified-wake` (coop_exec_unix.go:1369-1400). Mail present when the wake starts never rings; only later mail does (docs/wake-operations.md:54-59). Retries follow the input ladder: 5 s doubling to 2 min, four attempts per unchanged cohort, then parked until new mail (docs/wake-doorbell-acknowledgement.md:56-92).
7. **What keeps a doorbell from answering a dialog.** AMQ's dialog hooks hold the doorbell while a dialog is open. AMQ treats them as a guard, not a wake requirement; the reviewer's ruling makes them conditions of a seat's wake (below).
   - **Codex.** `amq wake install-dialog-hook --harness codex` writes marked entries for six events into `$CODEX_HOME/hooks.json`. The default home is `~/.codex`. The owner must trust the entries in Codex `/hooks` (docs/wake-operations.md:458-492, internal/cli/wake_dialog_codex_unix.go:37-44 and 182-214). Without them, AMQ's own check says "a wake doorbell can answer a Codex approval prompt" (wake_dialog_codex_unix.go:240-295). amq-squad's Codex seats run `--ask-for-approval on-request` (seatlaunch.CodexSandboxArgs). A doorbell at an approval prompt cancels the pending command and runs as a new prompt (docs/wake-operations.md:460-461). Seat-exec does not override `CODEX_HOME` or `HOME` (internal/seatexec/env.go), so Codex seats read the owner's hooks file. The hooks find the mailbox through `AM_ME` and `AM_ROOT`, which seat-exec sets (seatexec_unix.go:512-516).
   - **Claude Code.** `amq wake install-dialog-hook` writes `~/.claude/settings.json` (docs/wake-operations.md:166-176). A Claude seat runs under its own `CLAUDE_CONFIG_DIR`, which seat-exec sets from `internal/claudeseat` (seatexec_unix.go:193-198, 521-522), so the owner's install never reaches a seat. A Claude seat runs `dontAsk`, so no permission dialog opens. AskUserQuestion and ExitPlanMode open a dialog in any mode; amq-squad's guard now denies both in a seat (below), so neither opens.
8. **Codex notify fan-out is not a wake requirement.** AMQ adds its own `-c notify=[amq, __codex-notify, ...]` override to every Codex seat for conversation-identity capture (internal/launch/adapter_codex.go:186-211). `amq __codex-notify` then forwards to the owner's own `notify` line when one exists (internal/cli/codex_notify.go:25-70). The existing doctor row "codex notify fan-out" is unchanged.
9. **What Prepare and Apply report.** Prepare's preview echoes each runnable participant's execution options: `require_wake`, wake mode, audit reason and injector (launchapi/prepare.go:200-203 and 249-258). `ApplyResultV1` and its observations carry no wake field (launchapi/types.go:304-314 and 367-393). The live wake state is in `<root>/agents/<handle>/.wake.lock` and its siblings, which `amq wake check --me H --json` reads (docs/wake-operations.md:9-52).
10. **Close.** No explicit close is needed. coop exec re-execs `amq __launch-exec`, which preserves the PID (coop_exec_unix.go:1083-1108). seat-exec then calls `syscall.Exec` on the provider (internal/seatexec/seatexec_unix.go:608). So the wake's owner PID is the seat's provider PID, and the wake exits when it does (coop_exec_unix.go:215-217). `amq wake retire` applies only to inject-via wakes (docs/wake-operations.md:844-881).
11. **pi.** The same coop exec path would wake a pi seat with `paste`. Its dialog guard runs only when pi loads AMQ's pi extension, `integrations/pi/amq-bridge.ts` (docs/wake-operations.md:587-599). amq-squad's embedded runtime does not load it. A pi seat therefore stays as it was, with wake disabled, under a new audit reason that says why (`seatlaunch.PiWakeAuditReason`).
12. **Startup screens with a wrapper (a stated limit).** coop exec decides its startup-prompt hold from `c.binaryPath`. For a wrapped seat that is the amq-squad wrapper, not the provider (coop_exec_unix.go:591 and 994, coop_exec_trust.go:74-75). Managed launches also skip the Codex launch decision (coop_exec_unix.go:940). So no Startup marker is ever written for a seat, and a doorbell that rings while a folder-trust, update or hook-review screen shows could answer it. Claude seats are not pre-trusted today: `claudeseat.Seed` is empty (internal/claudeseat/claudeseat.go:78-82), so a fresh seat config dir can show first-run screens. What closes it:
    - **Claude's screens:** t6, which makes the seat's config dir born complete. Until then `claudeseat.StartupScreensClosed` reports `claude_onboarding_unverified` and a Claude seat's wake stays off.
    - **AMQ's hold under a wrapper:** a 3.0.x line in docs/v3-followups.md. Either seat-exec names the inner binary to AMQ, or AMQ honours a binary the launch declares (an upstream ask).
    - **Codex:** until then a Codex seat's wake requires the Codex config's trust entry for its worktree. That rules out the folder-trust screen only; the update and hook-review screens stay this limit.

## The change (first commit)

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

### The reviewer rulings (second and third commits)

Raw typing into a pane means a doorbell that lands on a prompt or a dialog answers it unattended. The rulings make a seat's wake a set of fail-closed conditions per runtime, checked at compile (`seatlaunch.SeatWake(runtime, permissionMode, SeatWakeConditions)`). The zero value of `SeatWakeConditions` meets none. An unmet condition keeps the wake off with a named audit reason. It never fails the launch: the seat still runs and is kicked by hand, as in 3.0.

- **The guard denies AskUserQuestion and ExitPlanMode in a seat** (ruling 1). `evaluateGuard` in `internal/cli/hook_guard.go` returns `deny`, reason code `seat_dialog`, for both tools (`seatexec.SeatDeniedDialogTools`). The rationale, in the code comment: a seat never answers a dialog, and a seat's question to anyone is AMQ mail to the lead. The two tools are hook-denied in every Claude seat, whatever its wake.
- **Claude Code** (ruling 2). All three must hold:
  - **Mode:** the permission mode is `dontAsk`.
  - **Guard wired:** the seat's config dir runs that guard deny (`seatexec.ClaudeDialogGuardWired`). This read-only check reads the dir's `settings.json` (`enabledPlugins`), `plugins/installed_plugins.json` (the install must be inside the dir), and the installed plugin's `hooks/hooks.json`. That file needs a PreToolUse group whose matcher covers both tools and runs the hook script's `pre-tool` verb.
  - **Screens closed:** `claudeseat.StartupScreensClosed(dir)` holds. It returns false with `claude_onboarding_unverified` until t6 fills it in. So a Claude seat's wake turns on by itself when t6 merges, and never before.
- **When the Claude conditions are read.** Compile runs before the seat's own config dir is written: the launch writes it only after Prepare and the decisions, and a row asserts nothing is staged before them. So the launch writes the dir `claudeseat.Write` produces into a scratch directory, reads the conditions there, and removes it (`scratchClaudeWakeConditions`). The conditions depend only on Write's inputs: mode, plugin files and seed. The seat's own dir therefore carries the same answer.
- **Codex** (ruling 2). Both must hold:
  - **Hook present:** AMQ's Codex dialog hook entries are on the host. This is the shared read-only `seatexec.ReadCodexDialogHook`, which doctor's row also uses.
  - **Worktree trusted:** the Codex config has `[projects."<worktree>"] trust_level = "trusted"` for the seat's worktree, as given or with symlinks resolved. This reuses doctor's read, `hostcheck.ReadCodexTrust`, and reads key names only.
  - **How the launch reads them:** once per launch, only for a squad with a Codex seat, on the Codex home the seats read (`$CODEX_HOME` of the launch environment, else `<home>/.codex`).
  - **Trust of the hook entries:** the launch checks only that they are present. Whether Codex trusts them is Codex's own hash, which `amq doctor --ops` reads, so trusting them stays an owner step.
- **pi:** unchanged, off with `PiWakeAuditReason`.
- **Admission evidence per seat** (ruling 3, Codex finding 1). Every runnable seat needs Prepare's evidence. A preview that lists no participant for a seat, or lists one without execution options, refuses the launch before Apply as `wake_not_admitted`. The pinned AMQ always echoes execution options for runnable participants (launchapi/prepare.go:200-203). The tests' fake AMQ now echoes them as AMQ does.
- **Plumbing.** The conditions reach Compile through `Squad.WakeConditions`, which is `json:"-"`, so a seat file cannot claim them. They are read through three seams on the launch options: `CodexDialogHook`, `CodexTrust` and `ClaudeWakeConditions`.
- **Report.** The seat's wake line ends with the audit reason when the wake is off.

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
| the guard denies AskUserQuestion and ExitPlanMode in a seat (`seat_dialog`); a read is allowed | TestGuardDeniesSeatDialogs (cli) | dropping the deny |
| every condition's branch through `SeatWake` (Claude mode, guard, screens; Codex hook, trust; pi), the zero value meets none, and Compile with no conditions read keeps both wakes off | TestSeatWakeConditionsPerRuntime (seatlaunch) | any one condition skipped |
| the Claude guard condition: the dir `claudeseat.Write` produces is wired; a matcher naming both tools or empty is wired; Bash only, one of the two tools, another verb, the plugin not enabled, an install outside the dir, an empty dir are not | TestClaudeDialogGuardWired (seatexec) | the matcher, enabled, inside-dir or pre-tool check removed |
| the Claude screens condition is unverified until t6 | TestStartupScreensClosedIsUnverifiedUntilT6 (claudeseat) | returning true |
| the default Claude read: guard wired, screens unverified, so a Claude seat's wake is off through Launch, and the read stages nothing | TestClaudeWakeStaysOffUntilTheStartupScreensAreClosed (seatlaunch) | the scratch read ignoring the guard, or the screens condition skipped |
| the launch reads the Codex hook and trust once, on `CODEX_HOME`, only with a Codex seat; met, hook missing and worktree untrusted, each with the report line | TestLaunchReadsTheCodexWakeConditions (seatlaunch) | either read ignored or not passed to Compile |
| the Codex trust condition: trusted through a symlink, untrusted with the key-name reason, an unreadable config | TestCodexTrustsTheSeatWorktree (seatlaunch) | the symlink resolution or the unreadable branch removed |
| admission evidence per seat: no participant, no execution options (wake on or off) refuse before Apply with the evidence named | TestLaunchRefusesAWakeAMQDidNotAdmit (seatlaunch) | either evidence check removed |
| the shared Codex hook detection: AMQ's marker only (the owner's own hook on an event does not count), `features.hooks` | TestCodexWakeDialogHookRow (cli) | the marker test or the features read removed |

The brief asked for a row with wake facts "from a fake apply result". AMQ's ApplyResult has no wake field (finding 9), so the row reads the facts from the fake Prepare echo, which is where AMQ reports them.

Each revert was applied by hand to a copy, the row was run and failed, and the file was restored. The canary strings in the doctor row were checked against `redact.RealCredentialShapes` and match none. No test reads a real Codex home. As a tripwire, both default Codex reads were made to panic on the real `~/.codex` or `$CODEX_HOME`, and the full suite raised no panic; the tripwire was then removed. No row reaches the network, HOME or tmux: the Codex reads are faked or use temp homes, the Claude reads use scratch dirs, and the seatlaunch rows fake AMQ.

Gate: `go build ./...`, `go vet`, and `go test -count=1` over seatlaunch, launchintent, seatexec, claudeseat and cli passed. The full `go test -count=1 ./...` passed. Two load-timing tests, buzz `TestOwnerScriptDryRun` and seatlaunch `TestAmbiguousRequestIsAbandonedOnlyWhenNoPaneExists`, failed once under the tripwire run's load and passed when run alone. `scripts/manifest-check.sh` passes after the line counts and the rows for `internal/seatexec/codexhook.go` and `claudeguard.go` were updated in `docs/v3-manifest.yaml`. That file is outside the declared files, and the edit is mechanical.

## Owner-run row (3.0.1 owner run)

This cannot run here: it needs a live AMQ, tmux and real seats. It uses the v31 roster in `squad.yaml`, run on the 3.0.1 build with this branch. `SEATS` is the JSON seat list the v31 start passed to `--seats`; `amq-squad launch` does not read squad.yaml directly.

The rule (reviewer rulings): a seat gets its AMQ wake only when every condition of its runtime holds. An unmet one keeps the wake off and names itself in the launch report; the seat still runs.
- **Claude Code:** `dontAsk`, the guard deny wired in the seat's config dir, and the startup screens closed. The last one is false until t6, so the Claude seats (senior-dev, scribe) launch without a wake in this run.
- **Codex:** AMQ's Codex dialog hook on the host, and the Codex config trusting the seat's worktree.

```sh
# Host step 1 (one time): install AMQ's Codex dialog hook into the Codex home the seats read,
# then open /hooks in Codex and trust the AMQ_DIALOG_HOOK entries (or "Trust all" at the next
# Codex start). The launch checks the entries' presence; Codex's trust of them shows in:
amq wake install-dialog-hook --harness codex
amq doctor --ops
amq-squad doctor | grep 'codex wake dialog hook'     # expect: ok ... all 6 events

# Host step 2 (per Codex seat worktree): the Codex config trusts the worktree.
amq-squad trust <qa's worktree>                       # prints the owner-run steps
amq-squad doctor | grep 'codex trust'

# 1. The admitted wake, before anything starts.
amq-squad launch --seats "$SEATS" --dry-run --json \
  | jq '.prepare.preview.participants[] | select(.runnable) | {handle, execution}'
# expect qa (Codex): require_wake true, wake.mode "enabled", injector raw
#   (without a host step: wake.mode "disabled", the audit reason naming the missing one)
# expect senior-dev, scribe (Claude): wake.mode "disabled", reason ... claude_onboarding_unverified (until t6)
# expect cto (pi): wake.mode "disabled" with the pi audit reason

# 2. Start the squad as in the v31 start (the scheduler launches the seats).
amq-squad scheduler run --seats "$SEATS" --lead cto
# expect in the launch report: "seat qa wake: mode enabled, require_wake true, injector raw (AMQ prepare)"

# 3. The Codex seat has a live wake.
amq wake check --me qa --root "$PWD/.agent-mail/v31" --json      # live_wake true, health_status healthy

# 4. Let qa finish its first turn and sit idle, then give it a task.
amq-squad task add --title "t9 live row: claim this task and report its id" --class <a class qa's pool serves> --pool codex:gpt-5.6-sol:high
```

Pass criteria:

- **A new turn without a keystroke.** Within the debounce and first input attempt (a few seconds) of the scheduler's doorbell landing in `agents/qa/inbox/new`, qa's pane shows a new turn, and no key is typed by the owner.
- **The turn claims the task.** `amq-squad task show <id> --json` shows qa as the lease holder.
- **The wake logged the attempt.** `agents/qa/.wake.log` and `agents/qa/notification-attempts.jsonl` record the input attempt.
- **The wake exits with its seat.** Close qa, then `amq wake check --me qa ...` reports no live wake.

The Claude half of the live row (senior-dev woken by a doorbell) waits for t6.

Watch item: a doorbell that lands while Codex still shows an update or hook-review screen could answer it (finding 12, the stated limit). Do not rely on a doorbell before the seat's first turn ends.
