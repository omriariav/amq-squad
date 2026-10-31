# Brief 31-1, PR 1: the deletions, squad.yaml as the one source, one lead per root

Branch `v3-31-1` from `origin/v3` at `d5094b68`. Authority: docs/v3.1-plan.md
section 7 (the amendment of 2026-10-31, the owner's A), read from the
amendment's scratch copy until it merges. This note is PR 1 of the brief:
section 1 (the deletions), section 2 (squad.yaml), section 4 (one lead per
root) and the start stamp. PR 2 (`up`, the status additions, the launch
budget, the screen) is a later agent's; its seams are named at the end.

## 1. Before any code

### 1.1 The baseline

Source: the lead's read-only copy of the 2026-10-31 session (decisions.jsonl,
the seats' staged launch records and accepted proofs, their outboxes), never
the live session. Script: `baseline.py` beside this note.

```sh
python3 -I docs/v3-spike/31-evidence/baseline.py <copy dir>
```

Only the last roster launch, `0de97c89b15b5262f41e4fd3ee0b1a87` (3.0.2,
2026-10-31), still has full launch records: every earlier launch's staged
record was overwritten by the next. Its output:

| Seat | Runtime and model | Prepare | Mint | Apply | Seat-exec and proof | To seat live | To first message | After |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| dev | codex gpt-5.6-terra | 3.201 s | 0.602 s | 0.538 s | 0.144 s | 4.485 s | none in the copy | |
| reviewer | codex gpt-5.6-sol | 3.201 s | 0.602 s | 0.538 s | 0.078 s | 4.419 s | 57.449 s | |
| scribe | claude sonnet | 3.201 s | 0.602 s | 0.538 s | 0.325 s | 4.666 s | none | `claude_hooks_silent`, closed |

- **The segment it covers.** From the scheduler's own start
  (`scheduler_started`, 19:24:32.963Z, the first decision of the incarnation
  that launched the roster) to the seat's accepted proof and to its first
  sent message. Nothing stamps the owner's start: the scheduler's
  `launch.json` (internal/schedproc/requests.go:21-31) has only the process's
  own `started_at`, and the lead keeps no pid file. The segment before the
  scheduler (the owner's command, the lead's start, the scheduler's spawn and
  admission) is not in the baseline. This PR adds the start stamp (1.3).
- **Prepare** is `scheduler_started` to the staged record's `started_at`
  (Compile's clock; admission, adoption and the first tick precede it).
  **Mint** is `started_at` to the proof ticket's `issued_at`: the preflight
  copy, the Claude config dirs and the Claude mode probe (one probe for the
  one Claude seat) and the proof socket. **Apply** is `issued_at` to AMQ's
  apply outcome. **Seat-exec and proof** is the apply outcome to the accepted
  proof (`proof.json`'s `at`): seat live.
- `roster_launched` is logged at 19:24:37.720Z, after every proof (+4.757 s).
- dev's first message after this launch is not in the copy (its last sent
  message is from 2026-10-30). The scribe sent none: it was quarantined
  `claude_hooks_silent` and closed in the same tick, the brief's motivating
  case. The 2026-10-30 20:46:50 launch (`1ab6e1aa…`) shows the same shape
  (reviewer's first message 66.6 s after `roster_launched`; the scribe again
  quarantined `claude_hooks_silent`) but has no launch records left.

### 1.2 The open questions (plan 7.9)

Each is answered by a reading recorded here, before the first code commit.

**Q1, the Claude login without a token file: not answered by a reading; an
owner-run row.** Today seat-exec reads `~/.amq-squad/claude-token` only
(internal/claudeseat/token.go:56-90: a regular 0600 file in a 0700 dir, one
token on one line), refuses `claude_token_insecure` when it is missing or
insecure (internal/seatexec/seatexec_unix.go, the Claude branch of
`prepare`), and sets `CLAUDE_CODE_OAUTH_TOKEN` from it for Claude seats only
(`overlay`). The only measurement on record is token.go's comment: a fresh
`CLAUDE_CONFIG_DIR` finds no login because the macOS Keychain item is keyed
by the config dir ("Not logged in" on Claude Code 2.1.310). Until the
owner-run check below runs on the installed Claude Code, the token refusal
stays as it is (this PR does not touch it).

Owner-run row (from the owner's own terminal, no seat):

```sh
d=$(mktemp -d)
env -u CLAUDE_CODE_OAUTH_TOKEN -u ANTHROPIC_API_KEY CLAUDE_CONFIG_DIR="$d" \
  claude -p 'Reply with the single word OK' --model sonnet; echo "exit $?"
rm -rf "$d"
claude --version
```

"OK" and exit 0 means a seat logs in without the token file, and
`claude_token_insecure` should then refuse only a token file that is present
and insecure. "Not logged in" (or a login prompt) keeps the token a
requirement.

**Q2, the trust subject on a binary upgrade: replacing a binary at the same
path does not change it, and the standing answer covers a changed one.** In
the pinned AMQ the trust confirmation is asked when the prepare's trust
digest is not in the trust store (internal/launch/prepare.go:714-735 at
v0.116.2). The digest is `PrepareTrustDigestWithAuthority` over
`PrepareTrustPlanDigest` (internal/launch/plan.go:270-310): each agent's argv
values, env overlay, cwd, adapter mode, resume policy, execution options,
initial-input kind (not its text) and wrapper, all strings. No file's bytes,
inode or version enter it, so a binary replaced in place keeps the subject. A
binary at a new path (a versioned install, or amq-squad's own path, which
the wrapper records after `EvalSymlinks`) changes it, and every launch now
carries the standing `trust_exact_subject` answer (item 6), so that launch is
answered too. (The apply's subject digest also names the amq executable's
identity, prepare.go:767-774, but that binds a prepare to its apply, not the
trust store.)

**Q3, Codex at an approval prompt without AMQ's dialog hook: it cancels the
pending command.** AMQ's own docs: "A doorbell at a Codex approval prompt
cancels the pending command and runs as a new prompt" (docs/wake-operations.md
:460-461 at v0.116.2); the dialog hook (`amq wake install-dialog-hook
--harness codex`, `PermissionRequest` opening the marker) is what holds the
doorbell, and the baseline's reviewer `.wake.log` shows it doing so ("holding
the doorbell while a harness dialog is open … PermissionRequest … codex").
So, as the brief rules, the Codex wake keeps the hook as its one condition:
a Codex seat without it launches with its wake off and the named reason (the
screen shows it idle, brief 31-1 PR 2). The Codex worktree trust is not a
wake condition any more; it stays doctor's `codex trust` warning row.

**Q4, the generic provider mapping: AMQ adapts five providers, and only
those.** internal/launch/adapter.go:21-36 at v0.116.2 names the built-in
providers claude, codex, cursor-agent, grok and pi (`ProviderForExecutable`
maps an executable to one of them or ""). Beyond claude and codex, the
argument rules are grok's `--model` and `--effort low|medium|high|xhigh|max`
(adapter_grok.go:133-134) and cursor-agent's `--model` only
(adapter_cursor.go:135). So the runtime lists open to exactly those five;
grok passes model and effort, cursor-agent model only (an effort on a
cursor-agent seat is refused by name), and any other runtime is refused by
name at parse. `CheckArgumentForms` still checks every form against what AMQ
advertises at prepare.

**Q6, the pi seat wake: the embedded runtime does not carry AMQ's pi
extension, so a pi seat stays wake-off.** AMQ's pi dialog guard runs "only
when the AMQ pi extension is loaded" (docs/wake-operations.md:587-599 at
v0.116.2: `integrations/pi/amq-bridge.ts` writes the `.wake.dialog`
marker). scripts/build-pi-runtime.sh:92 stages amq-squad's own
`pi/extensions` only, and nothing in it writes that marker. A pi seat keeps
`PiWakeAuditReason`; the screen says so (PR 2).

### 1.3 The design

- **Deleted** (plan 7.2 (b), the owner's A), each with the tests that pin it:
  the silent hook as a quarantine reason; the config-drift preflight copy and
  diff, `exitChanges`, `global_config_changed` and `claude_config_changed`;
  with them the whole quarantine (the task store's record, the scheduler's
  quarantine closing, the ledger's quarantine and signed clears, the hook's
  run-time drift deny) and `seat clear` (its only callers were those
  quarantines), the `quarantine_clear` gate kind and the `clear.signature`
  grant key; the Claude pin and the mode probe; the Claude config-dir
  refusals; the wake conditions as refusals; `--trust`; the seats file
  (`launch --seats`, `--scheduler-seats`, `--lead`); the empty-worktree
  exclusion; `codexModelLists` as a gate; the pi model refusal; the launch's
  push-target refusal.
- **Moved.** A silent hook, worktree allow rules and an external CLAUDE.md
  include become seat warnings in status (`seat.warnings`). The push-target
  check runs only in the commit step. The trust confirmation's answer is a
  constant every launch carries. A Codex seat's wake reads AMQ's dialog hook
  only (Q3). The router admits a roster seat's own pool without a registry
  entry or a table chain. A ledger_tampered refusal at a seat's exit (the one
  non-config producer the quarantine had) closes the seat and tells the lead;
  the commit step refuses the same way, so nothing of it commits.
- **squad.yaml is the one source.** `lead --team` in a repository with a
  squad.yaml reads its lead handle and session, takes the root's lead lock,
  and runs `scheduler run --project <repo>`, which reads the same file. A
  seat's worktree is derived per handle (`<repo>/.worktrees/<handle>`), and a
  squad.yaml worktree naming the repository root is a parse error.
- **One lead per root.** A lead lock (`lead.lock`, flock) and its record
  (`lead.json`: pid, raw start stamp, tmux pane) beside the scheduler's
  `run.lock`. A second `lead --team` refuses `lead_running` with the first's
  pid and pane before its scheduler starts. A scheduler's `ErrRunning` is a
  refusal the supervisor does not restart. A lead exiting non-zero stops its
  scheduler before `os.Exit`.
- **The start stamp.** `lead --team` sets `AMQ_SQUAD_START` (when `up` has
  not) and the scheduler records it in its `launch.json` as `start`.
- **Decisions log kinds added:** `exit_refused` (a refusal at a seat's exit,
  replacing `seat_quarantined` there). `seat_live` and `seat_first_message`
  are PR 2's.
- **The screen's pi surface (PR 2's decision, read now).** pi 1.3.0's
  extension UI has persistent surfaces: `setWidget(key, string[] | component,
  {placement})` (dist/core/extensions/types.d.ts:101-104), `setFooter`
  (:111), `setHeader`, `custom` with an overlay (:121), and raw keys through
  `onTerminalInput` (:82). So the screen can live in the pi extension, inside
  the lead's process (R4 by construction for brief 31-2's keys), as a widget
  fed by `status --json` on the `.generation`/`updated_at` poll; no separate
  `amq-squad screen` process is needed.

## 2. What PR 1 changed

### 2.1 The deletions (section 1), each with its removed tests

1. **The hook probe as a quarantine reason.** `exitChanges`' silent-hook
   branch and `claudeseat.HooksSilent` as a quarantine code are gone; the
   hook still records a report on every context, guard and ingest call
   (`recordHookReport`, internal/cli/hook_context.go), and status turns a
   Claude seat that ran with no report since its launch into the seat
   warning `claude_hooks_silent` (internal/status/collect.go,
   `seatWarnings`). Removed: seatcommit fix2_test.go
   `TestClaudeSeatWithNoHookReportIsQuarantinedAtExit`; clearflow_test.go
   `TestClearedHooksSilenceIsNotRaisedAgainOnTheSameIncarnation`.
2. **The config-drift preflight baseline and the quarantine.** Gone:
   `seatexec.WritePreflight`, `PreflightDiff`, `ConfigChange`,
   `PreflightDir`, `PreflightFiles`; seat-exec's preflight write; the
   launch's refresh and `Options.KeepPreflight`; the scheduler's `Admitted`
   hook (its only use was the baseline); `exitChanges`,
   `global_config_changed`, `claude_config_changed`, `quarantine_tree_changed`,
   `quarantine_unverified`; `claudeseat.ConfigDrift`; the verifier's second
   config diff; the hook's run-time drift deny and its task-store
   quarantine; the task store's quarantine (internal/task/quarantine.go,
   its Grant and Claim check) and `Store.Rearm`; the scheduler's quarantine
   closing and `CommitResult.Quarantine`; the ledger's quarantine handling,
   `ClearRecord`, `ClearVerifier`, `ClearSigner`, `ClearQuarantine*` and
   `ClearEventUsed`; `CleanSeat`'s clear argument; `seat clear` with its
   gate (`seat_clear_gate.go`), the owner-terminal proof (`seat_owner*.go`,
   whose only verb it was) and its doctor rows; the `quarantine_clear` gate
   kind (operatorauth catalog, gatetarget class, gatestore requests,
   gateverify clear) and `operatorauth.Reserved`; the `clear.signature`
   grant key and `Grants.ClearMode`; `hostcheck`'s config diff and redaction
   with doctor's `owner config/*` rows; `panechain.List`. seat-exec's deny of
   a write to the owner's global agent config is untouched.
   Kept as data only: the ledger's `Quarantine`, `Cleared` and `Seeded`
   fields and the record shapes under them, because the ledger's seal is
   computed over the struct and a ledger sealed before 3.1 must still
   verify; nothing writes or honours them. An owner grants file that still
   carries a `clear:` block loads (the block is ignored).
   The one quarantine producer that was not a config reason, a
   `ledger_tampered` refusal at a seat's exit, now closes the seat with the
   decision `exit_refused` and a lead notice (`SeatExitRefused`); the commit
   step refuses ledger_tampered at that seat's next done report, and
   `CleanSeat` cannot read its ledger, so nothing of it commits.
   Removed tests: seatcommit clearflow_test.go (renamed
   widen_refusal_test.go) `TestEveryConsumerOfAClearVerifiesIt`,
   `TestSeedEntryIsContinuedByRecordBaseAndKeepsTheClear`,
   `TestClearRecordOnTheLedgerIsTheOnlyUseMark`,
   `TestConcurrentClearsWriteOneRecordAndOneIsAlreadyCleared`, `switchTrust`,
   and the seeded half of `TestWidenRefusesWhatTheDryRunRefuses`;
   commitstep302_test.go `TestQuarantineClearIsLiftedOnlyAfterTheBindingCheck`,
   `TestQuarantineWritesNothingBeforeTheBindingCheck`,
   `TestAnotherTasksLeaseRefusesBeforeTheQuarantine`; commit_test.go
   `TestGlobalConfigChangeFailsTheTask`,
   `TestGlobalConfigQuarantineKeepsTheTreeAndPushesNothing`,
   `TestPreflightDiffIgnoresOnlyCodexSelfRewrite`; fix1b_test.go
   `TestExitCheckQuarantinesAtAnyExit`,
   `TestClaudeConfigDriftAtTheExitQuarantines`; verifier_test.go
   `TestOwnerConfigChangeDuringTheVerifierDiscardsTheVerdict`; the clear
   signer and trust fixtures. task quarantine_test.go (whole file).
   scheduler milestoneb_test.go `TestQuarantineClosesTheSeatAndDispatchesElsewhere`
   (fix1b_test.go's two exit-path rows now assert `exit_refused`). schedproc
   the quarantine half of `TestCommitterDeferralsAndQuarantine` (now
   `TestCommitterDeferrals`), `TestAdmissionComesBeforeTheBaselineAndTheLaunchRecord`
   (now `TestAdmissionComesBeforeTheLaunchRecord`). seatexec fix1_test.go
   `TestPreflightDiffCatchesAGlobalConfigWrite`,
   `TestPreflightCopyConcurrentWriters`; seatexec_test.go
   `TestSeatExecPreflightCopy`. hostcheck `TestDiffOwnerConfig`,
   `TestDiffOwnerConfigRedactsNestedSecrets`, `TestDiffOwnerConfigParsesTOML`,
   `TestRedactPathsDoNotCollide`, `TestDiffOwnerConfigTrailingJSONIsADiff`.
   cli seat_clear_test.go and seat_clear_signed_test.go (whole files);
   seat_fix1b_test.go `ownerProcess`, `quarantinedSeat`,
   `TestSeatClearRunsOnlyFromTheOwnersTerminal`,
   `TestUnsignedClearKeepsTheSeatQuarantined`,
   `TestContextCallQuarantinesAChangedClaudeConfigDir`,
   `TestRejectedSchedulerNeverTouchesThePreflightBaseline`,
   `TestSeatClearNeedsTheOwnersOwnTerminal`,
   `TestOwnerTerminalIsTheRealPtyOfStdin`; the signed-clear half of
   `TestRehearsalThroughServe`; `TestGuardAndIngestRunTheSharedConfigDirCheck`
   (now `TestGuardAndIngestRecordAHookReport`); the preflight rows of
   `TestDoctorHostFileChecks`. claudeseat `TestAFlagCacheMidRunIsDrift`.
   gateverify clear_test.go and grants clear_test.go (whole files);
   operatorauth's `quarantine_clear` catalog row; team's `clear` and
   `clear.signature` never-settable cases. panechain `TestListReads…`,
   `TestListTimesOutOnAHungServer`.
3. **The probe-and-pin step.** Gone: `Options.PinFor`, `registryClaudePin`,
   `Squad.ClaudePins` and Compile's pin branch, `ReasonProviderNotPinned`,
   `pinnedProvider` with `tool_unpinned`, `pin_changed_since_probe` and
   `claude_pin_mismatch` for Claude, the record's `ClaudePin` (and the now
   unread `ClaudeConfig` and `AllowRules`), the mode probe (`Options.ProbeMode`,
   claudeseat/probe.go, `ModeUnverified`, `ProbeKind`), doctor's `claude pin`
   row (`claudePinCheck`, `resolveInstalledClaude` and the device helpers)
   and README's claude pin. A Claude seat resolves through `providerPath`.
   `--permission-mode dontAsk` stays (`ProviderArgs`), and so does the gh pin.
   Removed: seatlaunch repass5_42_test.go and repass5_fix2_test.go (whole
   files); cli relprep_claudepin_test.go (whole file: the doctor row's four
   tests); seatexec repass5_fix2_test.go (whole file), sixth_test.go
   `TestAnUpgradedClaudeIsRefusedBeforeTheConfigChecks`, the fixture's pin;
   claudeseat repass5_test.go, repass5_fix2_test.go, probe_live_test.go
   (whole files).
4. **The Claude config-dir refusals.** Gone: `claudeseat.Check`,
   `screensGap`, `StartupScreensClosed`, `Digest`, `ForbiddenSettingsEnv`
   and globalconfig.go, with their reason codes
   (`claude_settings_env_forbidden`, `claude_onboarding_missing`,
   `claude_trust_missing`, `claude_config_unexpected`,
   `claude_trust_root_refused`); seat-exec's calls (before the proof, in the
   re-check and as the last step), `claude_dialog_guard_missing` and
   `ClaudeDialogGuardWired`, `claude_allow_rules_present` as a refusal with
   `PermitWorktreeAllowRules`, the external includes refusal. `Write` keeps
   seeding the plugin and the first-run screens. `claude_allow_rules_present`
   and `claude_external_includes_pending` are status warnings, and doctor's
   `claude seat allow rules` row, which said the seats were refused, is gone
   with its test (cli doctor_allow_rules_test.go). Removed:
   claudeseat startup301_test.go, variants_test.go (whole files), the digest
   and Check rows of claudeseat_test.go, firstrun_test.go, globalconfig_test.go
   and fifth_test.go (listed in the commit; the Write and NFC rows stay and
   the external-includes detection row is `TestExternalIncludesAreDetected`);
   seatexec claude_test.go `TestClaudeSeatConfigDirIsChecked`,
   `TestClaudeSeatRefusesWorktreeAllowRules`, fix1b_test.go
   `TestClaudeConfigDirIsRecheckedImmediatelyBeforeExec`,
   `TestClaudeConfigDirIsHashedLastBeforeExec`, claudeguard_test.go,
   repass5_42_test.go, wakerecheck301_test.go (whole files).
5. **The wake conditions.** Gone: `SeatWakeConditions`,
   `readWakeConditions`, `scratchClaudeWakeConditions`, `codexTrusts`, the
   seams `ClaudeWakeConditions` and `CodexTrust`, `wake_not_admitted`.
   `SeatWake` is on for Claude, on for Codex when AMQ's dialog hook is on
   the host (Q3; the one read, `CodexDialogHook`), off for pi (Q6) and any
   other runtime. `admittedWakes` records AMQ's preview as a fact.
   Removed: wake301_test.go `allMet`, `metHost`,
   `TestSeatWakeConditionsPerRuntime`, `TestCodexTrustsTheSeatWorktree`,
   `TestClaudeWakeTurnsOnOnceTheStartupScreensAreClosed`,
   `TestLaunchRefusesAWakeAMQDidNotAdmit` (now
   `TestLaunchRecordsAWakeAMQAdmittedDifferently`); the Codex read row is
   `TestLaunchReadsTheCodexDialogHook`.
6. **`--trust`.** Gone from `scheduler run`, `lead` and
   `leadSchedulerCommand`, with `Options.Standing` and the trust text of
   `rosterRefusal`; `seatlaunch.StandingAnswers` is the constant every
   launch carries. Removed: `TestLeadTeamPassesTrustToTheScheduler` (now
   `TestDeletedLaunchFlagsAreUnknown`); `TestSchedulerRunRefusesAtStartNamingTrust`
   (now `TestSchedulerRunRefusesAtStartOnce`); the pending-decisions row
   uses a rebind action, since a trust confirmation is always answered.
   Exit 17 stays (see 5).
7. **The missing session as a refusal: not in PR 1.** It "gives way to
   `up` creating the root and session"; deleting it before `up` exists
   would turn a clear refusal into an AMQ failure. PR 2 deletes
   scheduler_run.go's root check and compile.go's `Stage` check with `up`.
8. **Models and runtimes.** The router admits each roster seat's own pool
   (`Options.RosterPools`, `routing.StartOptions.RosterPools`): a pin to it
   resolves at the class's tier when the table places it nowhere, an
   unpinned task falls to it after the tier's chain, and it needs no
   registry entry or verification (an entry still caps it).
   `codexModelLists` is gone: only a recorded probe refuses a Codex id. The
   pi model refusal is gone (`PiLauncherArgv` passes it). The runtime lists
   are AMQ's five providers (Q4); `byRuntime` maps every roster runtime to
   the AMQ launcher. Removed: registry `TestCheckCodexModelBeforeLaunch`
   (now `TestCheckCodexModelGatesOnlyOnAProbe`); team squad_test.go's
   "pi with a model" refusal case.
9. **The push-target check.** `LaunchTarget` and `RecordWorktreeBase` no
   longer call `CheckTarget`; the launch records the grant's target and the
   commit step refuses at the push (commit.go's D2 check, unchanged).
   Removed: seatcommit `TestLaunchRefusesAnUnacknowledgedPushTarget` (now
   `TestTheCommitStepRefusesAnUnacknowledgedPushTarget`).

Also deleted with its only caller: the `launch` verb's `seatlaunch.Report`
(launchfail_redact_test.go `TestReportRedactsBeforeItFlattens` is now
`TestAMQDetailRedactsAndIsIdempotent`; the other Report uses assert on the
Result).

### 2.2 squad.yaml is the one source (section 2)

`launch --seats` (launch_seats.go: `launchSeatFile`, `readLaunchSeats`,
`runLaunch`) and its registry entry are gone. `scheduler run --project DIR`
builds the squad with `squadOfProject` (team.LoadSquad,
`Squad.LaunchSeats`, `roleText`, the repository's team rules). `lead --team`
in a repository with a squad.yaml (`projectTop` of the working directory)
reads it through `resolveTeamLeadIdentity(project)`: the handle is
squad.yaml's `lead` and the session its `session`, both required for a
launch; without a squad.yaml `lead --team` stays the solo team lead it was.
`team.SeatWorktree` derives `<repo>/.worktrees/<handle>` for a seat that
names none, and squad.yaml refuses a worktree naming the repository root;
the roster's empty-worktree exclusion is gone.

### 2.3 One lead per root and the start stamp (section 4)

internal/schedproc/leadlock.go: `AcquireLeadLock` (flock on `lead.lock`
beside `run.lock`, then `lead.json` with pid, raw start stamp and
`$TMUX_PANE`), `LeadRunningError` (`lead_running: … pid N, tmux pane %P …;
attach to it with: tmux attach -t %P`), `ReadLeadRecord`. `lead --team`
takes it before its scheduler starts and releases it when it ends, and wraps
`piExit` so a non-zero pi exit stops the scheduler and drops the lock before
`os.Exit`. `scheduler run` maps `ErrRunning` to exit 17, which the
supervisor does not restart. `lead --team` sets `AMQ_SQUAD_START` when the
caller did not, and `scheduler run` records it as `launch.json`'s `start`.

## 3. Rows

| Row | Test |
| --- | --- |
| 1 | seatcommit `TestASilentClaudeSeatCommitsAndIsNotQuarantined` (exit check OK, the done report commits and pushes, no quarantine anywhere, while the owner's config changed); status `TestClaudeSeatWarningsReplaceTheDeletedRefusals` (the warning); cli `TestGuardAndIngestRecordAHookReport` |
| 2 | seatexec `TestClaudeSeatExecsWithoutARegistryPin` (no registry, then a claude changed in place); seatlaunch `TestClaudeSeatLaunchesWithNoPinAndNoProbe` |
| 3 | seatlaunch `TestClaudeSeatLaunchesWithNoPinAndNoProbe` (the probe seam is deleted; on the old code the launch fails at the missing pin before any probe) |
| 4 | seatexec `TestClaudeSeatExecsPastTheDeletedConfigDirRefusals` (allow rules, an external include, a changed config dir); status `TestClaudeSeatWarningsReplaceTheDeletedRefusals` |
| 5 | seatlaunch `TestSeatWakePerRuntime`, `TestLaunchRecordsAWakeAMQAdmittedDifferently`, `TestLaunchReadsTheCodexDialogHook` |
| 6 | cli `TestDeletedLaunchFlagsAreUnknown`; seatlaunch `TestEveryLaunchCarriesTheStandingTrustAnswer` |
| 7 | PR 2 (`up`) |
| 8 | cli `TestTheCheckedInSquadYAMLLaunchesWithDerivedWorktrees`, `TestSchedulerRunLaunchesTheRosterAtStart`, `TestDeletedLaunchFlagsAreUnknown`; team `TestSeatWorktree`, `TestSquadStrictParse` (root worktree refused) |
| 9 | routing `TestRosterPoolsAreAdmittedWithoutRegistryOrChain`; registry `TestCheckCodexModelGatesOnlyOnAProbe`; team `TestSquadAdmitsAPiModelAndTheAdaptedRuntimes` |
| 10 | seatlaunch `TestLaunchWithNoGhPinApplies`; seatcommit `TestTheCommitStepRefusesAnUnacknowledgedPushTarget` |
| 11 | cli `TestSecondLeadOnTheSameRootIsRefused`, `TestSchedulerErrRunningIsARefusalAttemptedOnce`, `TestSupervisorDoesNotRestartARefusal` |
| 12 | cli `TestLeadExitingNonZeroStopsItsScheduler` |
| 13-16, 18 (`up`), 19 | PR 2 |
| 17 | the base-tip and seat-branch refusal rows, the binding check, one branch per task, CheckCommit and the R4 proof rows pass unedited (slots.go is unedited; the one roster row edited, `TestSlotsRosterAndItsPlan`, is the empty-worktree exclusion's) |
| start stamp | cli `TestSchedulerRecordsTheLaunchStart`, `TestLeadTeamTakesTheSquadIdentityFromSquadYAML` |

Each row fails on origin/v3: there, a silent Claude seat or a changed owner
config quarantines (1), a launch without a claude pin refuses
`tool_unpinned` before any probe, and so does seat-exec (2, 3), seat-exec
refuses the config-dir codes (4), a differing admitted wake refuses
`wake_not_admitted` and the Claude wake reads its scratch conditions (5),
`--trust` parses and a trust confirmation without it stays pending (6), the
checked-in squad.yaml has no `--project` path and its seats' empty
worktrees are excluded (8), a pool with no registry entry is skipped
`not_in_registry` and a pi model is a parse error (9), the launch refuses
the https target with no gh pin (10), a second `lead --team` has no lock and
`ErrRunning` is `ExitSystem` (2), restarted as a crash (11), and `piExit` skips the stop (12).

## 4. Owner-run rows

- **Q1, the Claude login without a token file:** the commands in 1.2.
- **The day-4 launch budget run (PR 2's `up`):** from the owner's terminal
  in this repository, with this branch's binary installed by PR 2's `up`:
  `amq-squad up`, then `amq-squad status --json` for each seat's two marks,
  set beside the baseline in 1.1.

## 5. Seams for PR 2

- `schedproc.LeadRecord`, `ReadLeadRecord` and the lead lock: `up`'s
  step 5 probes the lock (a held `lead.lock` is a live lead), reads
  `lead.json` for the pid and pane, and refuses `lead_running` with the
  attach command (`LeadRunningError` already prints it).
- `AMQ_SQUAD_START` (`leadStartEnv`): `up` sets it before it starts the lead
  in its tmux session; the scheduler records it as `launch.json`'s `start`,
  the launch budget's first mark.
- `seatlaunch.SeatState.Wake` (AMQ's admitted wake) and `seat.warnings` in
  status: the screen's seat lines (a Codex seat without the dialog hook
  shows its wake off and why; a pi seat shows the pi reason).
- The missing-session refusal (item 7) to delete with `up`'s root and
  session creation; `exit_refused` joins the decisions tail.
- The screen's surface: pi 1.3.0 `ctx.ui.setWidget` (1.3).

## 6. Scope notes and where the brief and the code disagreed

- **Files outside the declared list,** touched only for the deletions:
  internal/task (the quarantine, `Rearm`), internal/scheduler (the
  quarantine branches, now `exit_refused`), internal/launch (the record's
  dead fields), internal/hostcheck (the config diff), internal/gatetarget,
  internal/gateverify, internal/operatorauth (the `quarantine_clear` kind),
  internal/panechain (`List`), internal/uninstall (two test calls),
  internal/cli hook_context.go, hook_guard.go, hook_identity.go,
  hook_ingest.go, seat_commit.go (where `seat clear` lived), gate.go,
  gate_store.go, task_widen.go, fleet.go (usage text), cli_test.go;
  the skills (plugins/skills-src and the generated mirrors) and
  docs/skills.md with docs/skills.html, which named the deleted verb and
  flags (`make ci`'s skill-command-check). The lead was told when it began.
- **Exit 17 is not `--trust`'s.** The brief lists "the exit 17 handling" with
  `--trust`, but exit 17 is 3.0.1 t16's exit for any roster launch AMQ
  refuses. Deleting it would make the supervisor restart a refused launch
  five times, the bug section 4 removes for `ErrRunning`. The trust text is
  gone; exit 17 stays and now also carries `ErrRunning`.
- **Item 7 waits for `up`** (2.1, item 7).
- **`checkWorktree` stays** as `ensureWorktree`'s own refusal: slots.go
  97-262 is unedited by the brief's rule, and it calls it. The roster's call
  is gone, and squad.yaml can no longer produce a root worktree.
- **The quarantine had a second producer** (`ledger_tampered` at an exit),
  which the brief does not name; it is now `exit_refused` (2.1, item 2).
- **Legacy data.** A sealed ledger keeps its legacy quarantine fields (2.1,
  item 2). A `quarantine_clear` gate record already in a live gate store
  no longer validates, so `gate status` without `--gate` refuses on it; no
  such record is in the baseline copy, and the live store was not read.
  Task-store quarantine files under `tasks/quarantine/` are ignored.
- **The commit trailer** names the model this session ran on (Opus 5.5)
  rather than the brief's Fable 5.1 line.
