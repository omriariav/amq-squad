# S1-2b: the block and wait reasons, each constant beside its writer sites

The lead's input, landed byte for byte. Line numbers as of `1a3db2f13466502a8de7948fc7e1fa07574843d9`.

It is the lead's go/types trace of every value that non-test code can pass into
`task.Task.BlockReason` and `task.Task.WaitReason`, written out by constant. The
trace follows assignments and not guards; it is the lead's reading and no test
rebuilds it. The data in `internal/statecontract/reasons.go` declares the same
rows without line numbers, and `TestReasonsAreTiedToTheirWriters` checks the
part of them a test can check.

The list is not every value a task can show: both vocabularies are open, and
section 6 names the doors.

## 1. The block list

45 constants with a writer, 58 rows.

| constant | value | declared at | writer function | door | sites |
|---|---|---|---|---|---|
| `claudeseat.TokenInsecure` | `claude_token_insecure` | `internal/claudeseat/token.go:30` | `seatexec.prepare` | `param seatexec.refusef#0` | `internal/seatexec/seatexec_unix.go:200` |
| `registry.PinMismatch` | `tool_pin_mismatch` | `internal/registry/pins.go:31` | `seatcommit.commitAndPush` | `param seatcommit.(Result).refuse#0` | `internal/seatcommit/commit.go:1311` |
| `registry.PinMismatch` | `tool_pin_mismatch` | `internal/registry/pins.go:31` | `registry.OpenPinned` | `result registry.OpenPinned#1` | `internal/registry/pins.go:90`, `internal/registry/pins.go:95`, `internal/registry/pins.go:99`, `internal/registry/pins.go:104`, `internal/registry/pins.go:109` |
| `registry.PinMissing` | `tool_unpinned` | `internal/registry/pins.go:30` | `registry.OpenPinned` | `result registry.OpenPinned#1` | `internal/registry/pins.go:86` |
| `routing.BlockPinUnresolved` | `pin_unresolved` | `internal/routing/router.go:217` | `routing.(*Router).decide` | `field scheduler.Route.Block` | `internal/routing/router.go:579`, `internal/routing/router.go:593` |
| `routing.BlockTreeUnchecked` | `declared_tree_unchecked` | `internal/routing/canon.go:41` | `routing.(*Router).decide` | `field scheduler.Route.Block` | `internal/routing/router.go:542` |
| `routing.HistoryTampered` | `history_tampered` | `internal/routing/outcome.go:41` | `routing.(*Router).holdTampered` | `field scheduler.Route.Block` | `internal/routing/tamper.go:227` |
| `scheduler.GoneProcessExited` | `process_exited` | `internal/scheduler/launchfail.go:30` | `scheduler.(*Scheduler).checkLiveness` | `field scheduler.Event.ReasonCode` | `internal/scheduler/launchfail.go:157` |
| `scheduler.ReasonCommitFailed` | `commit_failed` | `internal/scheduler/hooks.go:212` | `scheduler.(*Scheduler).commit` | `local code in scheduler.(*Scheduler).commit` | `internal/scheduler/scheduler.go:770` |
| `scheduler.ReasonLedgerTampered` | `ledger_tampered` | `internal/scheduler/tamper.go:43` | `scheduler.(*Scheduler).blockOnTamper` | `param task.(Store).Block#2` | `internal/scheduler/tamper.go:197` |
| `scheduler.ReasonResourcePresent` | `seat_replacement_resource_present` | `internal/scheduler/roster.go:89` | `seatlaunch.rosterOutcome` | `field scheduler.RosterOutcome.ReasonCode` | `internal/seatlaunch/roster.go:258` |
| `scheduler.ReasonSeatRelaunch` | `seat_replacement_needs_relaunch` | `internal/scheduler/roster.go:82` | `scheduler.(*Scheduler).rosterNoIdleSeat` | `param task.(Store).Block#2` | `internal/scheduler/roster.go:549` |
| `scheduler.ReasonTaskBranchUnprepared` | `task_branch_unprepared` | `internal/scheduler/hooks.go:64` | `scheduler.(*Scheduler).dispatch` | `param task.(Store).Block#2` | `internal/scheduler/scheduler.go:991` |
| `scheduler.ReasonVerifierInterrupted` | `verifier_interrupted` | `internal/scheduler/hooks.go:213` | `scheduler.(*Scheduler).commit` | `local code in scheduler.(*Scheduler).commit` | `internal/scheduler/scheduler.go:772` |
| `seatcommit.ReasonBaseUnrecorded` | `base_unrecorded` | `internal/seatcommit/policy.go:34` | `seatcommit.run` | `param seatcommit.(Result).refuse#0` | `internal/seatcommit/commit.go:256`, `internal/seatcommit/commit.go:265` |
| `seatcommit.ReasonGrants` | `grants_invalid` | `internal/seatcommit/policy.go:37` | `seatcommit.run` | `param seatcommit.(Result).refuse#0` | `internal/seatcommit/commit.go:247` |
| `seatcommit.ReasonGrants` | `grants_invalid` | `internal/seatcommit/policy.go:37` | `seatcommit.planVerifier` | `result seatcommit.planVerifier#1` | `internal/seatcommit/verifier.go:140`, `internal/seatcommit/verifier.go:144`, `internal/seatcommit/verifier.go:147`, `internal/seatcommit/verifier.go:152` |
| `seatcommit.ReasonGrowthGate` | `growth_gate_required` | `internal/seatcommit/widen.go:24` (`= task.ReasonGrowthGateRequired`) | `cli.applyLeadWiden` | `param task.(Store).Escalate#1` | `internal/cli/task_widen.go:361` |
| `seatcommit.ReasonLeaseTaskMismatch` | `lease_task_mismatch` | `internal/seatcommit/policy.go:49` | `seatcommit.run` | `param seatcommit.(Result).refuse#0` | `internal/seatcommit/commit.go:274` |
| `seatcommit.ReasonLedgerTampered` | `ledger_tampered` | `internal/seatcommit/ledger.go:120` | `seatcommit.(Result).tamperedRefusal` | `param seatcommit.(Result).refuse#0` | `internal/seatcommit/commit.go:987` |
| `seatcommit.ReasonLedgerTampered` | `ledger_tampered` | `internal/seatcommit/ledger.go:120` | `seatcommit.(Result).writeOutcome` | `param seatcommit.(Result).refuse#0` | `internal/seatcommit/commit.go:1014` |
| `seatcommit.ReasonLinkTarget` | `link_target_refused` | `internal/seatcommit/policy.go:30` | `seatcommit.check` | `field seatcommit.Violation.Reason` | `internal/seatcommit/commit.go:1153` |
| `seatcommit.ReasonLinkTarget` | `link_target_refused` | `internal/seatcommit/policy.go:30` | `seatcommit.verify` | `param seatcommit.(Result).refuse#0` | `internal/seatcommit/verifier.go:226` |
| `seatcommit.ReasonNested` | `nested_repository` | `internal/seatcommit/policy.go:33` | `seatcommit.check` | `field seatcommit.Violation.Reason` | `internal/seatcommit/commit.go:1162` |
| `seatcommit.ReasonNewerWriter` | `newer_writer` | `internal/seatcommit/ledger.go:125` | `seatcommit.(Result).ledgerRefusal` | `param seatcommit.(Result).refuse#0` | `internal/seatcommit/commit.go:997` |
| `seatcommit.ReasonOutside` | `outside_declared_files` | `internal/seatcommit/policy.go:31` | `seatcommit.check` | `field seatcommit.Violation.Reason` | `internal/seatcommit/commit.go:1159` |
| `seatcommit.ReasonPRBoundElsewhere` | `pr_bound_elsewhere` | `internal/seatcommit/policy.go:42` | `seatcommit.openPR` | `param seatcommit.(Result).refuse#0` | `internal/seatcommit/commit.go:1540`, `internal/seatcommit/commit.go:1545`, `internal/seatcommit/commit.go:1564`, `internal/seatcommit/commit.go:1568` |
| `seatcommit.ReasonPRBoundElsewhere` | `pr_bound_elsewhere` | `internal/seatcommit/policy.go:42` | `seatcommit.prCheck` | `param seatcommit.(Result).refuse#0` | `internal/seatcommit/commit.go:1495` |
| `seatcommit.ReasonPRPending` | `pr_pending` | `internal/seatcommit/commit.go:1331` | `seatcommit.openPR` | `field seatcommit.Result.ReasonCode` | `internal/seatcommit/commit.go:1519` |
| `seatcommit.ReasonPRPending` | `pr_pending` | `internal/seatcommit/commit.go:1331` | `seatcommit.prCheck` | `field seatcommit.Result.ReasonCode` | `internal/seatcommit/commit.go:1483` |
| `seatcommit.ReasonProtected` | `protected_path` | `internal/seatcommit/policy.go:29` | `seatcommit.check` | `field seatcommit.Violation.Reason` | `internal/seatcommit/commit.go:1156` |
| `seatcommit.ReasonPushRejected` | `push_rejected` | `internal/seatcommit/policy.go:36` | `seatcommit.commitAndPush` | `param seatcommit.(Result).refuse#0` | `internal/seatcommit/commit.go:1317` |
| `seatcommit.ReasonPushTargetUnacknowledged` | `push_target_unacknowledged` | `internal/seatcommit/ledger.go:170` | `seatcommit.run` | `param seatcommit.(Result).refuse#0` | `internal/seatcommit/commit.go:390`, `internal/seatcommit/commit.go:394` |
| `seatcommit.ReasonRecordedDiffMismatch` | `recorded_diff_mismatch` | `internal/seatcommit/policy.go:45` | `seatcommit.commitAndPush` | `param seatcommit.(Result).refuse#0` | `internal/seatcommit/commit.go:1254` |
| `seatcommit.ReasonSchemaMismatch` | `schema_mismatch` | `internal/seatcommit/ledger.go:128` | `seatcommit.(Result).ledgerRefusal` | `param seatcommit.(Result).refuse#0` | `internal/seatcommit/commit.go:999` |
| `seatcommit.ReasonSizeCap` | `size_cap_exceeded` | `internal/seatcommit/policy.go:32` | `seatcommit.check` | `field seatcommit.Violation.Reason` | `internal/seatcommit/commit.go:1178` |
| `seatcommit.ReasonStepCancelled` | `commit_cancelled` | `internal/seatcommit/commit.go:173` | `seatcommit.run` | `field seatcommit.Result.ReasonCode` | `internal/seatcommit/commit.go:410` |
| `seatcommit.ReasonStepCancelled` | `commit_cancelled` | `internal/seatcommit/commit.go:173` | `seatcommit.verify` | `field seatcommit.Result.ReasonCode` | `internal/seatcommit/verifier.go:233` |
| `seatcommit.ReasonVerifierInterrupted` | `verifier_interrupted` | `internal/seatcommit/verifier.go:70` | `seatcommit.verify` | `field seatcommit.Result.ReasonCode` | `internal/seatcommit/verifier.go:211` |
| `seatcommit.ReasonWidenSnapshotMismatch` | `widen_snapshot_attempt_mismatch` | `internal/seatcommit/commit.go:177` | `seatcommit.run` | `param seatcommit.(Result).refuse#0` | `internal/seatcommit/commit.go:314` |
| `seatcommit.ReasonWidenSnapshotMissing` | `widen_snapshot_missing` | `internal/seatcommit/commit.go:176` | `seatcommit.run` | `param seatcommit.(Result).refuse#0` | `internal/seatcommit/commit.go:316` |
| `seatcommit.ReasonWorktree` | `worktree_mismatch` | `internal/seatcommit/policy.go:35` | `seatcommit.Run` | `param seatcommit.(Result).refuse#0` | `internal/seatcommit/commit.go:150`, `internal/seatcommit/commit.go:155` |
| `seatcommit.ReasonWorktree` | `worktree_mismatch` | `internal/seatcommit/policy.go:35` | `seatcommit.commitAndPush` | `param seatcommit.(Result).refuse#0` | `internal/seatcommit/commit.go:1262` |
| `seatcommit.ReasonWorktree` | `worktree_mismatch` | `internal/seatcommit/policy.go:35` | `seatcommit.run` | `param seatcommit.(Result).refuse#0` | `internal/seatcommit/commit.go:251`, `internal/seatcommit/commit.go:268`, `internal/seatcommit/commit.go:284`, `internal/seatcommit/commit.go:289` |
| `seatexec.ClaudeHooksMissing` | `claude_hooks_missing` | `internal/seatexec/claude.go:26` (`= claudeseat.HooksMissing`) | `seatexec.CheckClaudeHooks` | `param seatexec.refusef#0` | `internal/seatexec/claude.go:54`, `internal/seatexec/claude.go:65` |
| `seatexec.ClaudePermissionMismatch` | `claude_permission_mismatch` | `internal/seatexec/claude.go:27` (`= claudeseat.PermissionMismatch`) | `seatexec.CheckClaudePermission` | `param seatexec.refusef#0` | `internal/seatexec/claude.go:75`, `internal/seatexec/claude.go:81`, `internal/seatexec/claude.go:83`, `internal/seatexec/claude.go:91` |
| `seatexec.IdentityIncomplete` | `identity_incomplete` | `internal/seatexec/env.go:23` | `seatexec.checkIdentity` | `param seatexec.refusef#0` | `internal/seatexec/seatexec_unix.go:430` |
| `seatexec.IdentityIncomplete` | `identity_incomplete` | `internal/seatexec/env.go:23` | `seatexec.prepare` | `param seatexec.refusef#0` | `internal/seatexec/seatexec_unix.go:184` |
| `seatexec.PiNotEmbedded` | `pi_not_embedded` | `internal/seatexec/pi.go:68` | `seatexec.piShimProvider` | `param seatexec.refusef#0` | `internal/seatexec/pi.go:82`, `internal/seatexec/pi.go:85` |
| `seatexec.SandboxPinsMissing` | `sandbox_pins_missing` | `internal/seatexec/codex.go:11` | `seatexec.CheckCodexSandboxPins` | `param seatexec.refusef#0` | `internal/seatexec/seatexec_unix.go:588`, `internal/seatexec/seatexec_unix.go:593` |
| `task.BlockVerdictFailed` | `verdict_failed` | `internal/task/class.go:59` | `seatcommit.verify` | `param seatcommit.(Result).refuse#0` | `internal/seatcommit/verifier.go:198`, `internal/seatcommit/verifier.go:257` |
| `task.BlockVerdictFailed` | `verdict_failed` | `internal/task/class.go:59` | `task.(Store).complete` | `param task.block#1` | `internal/task/lease.go:737` |
| `task.BlockVerdictMissing` | `verdict_missing` | `internal/task/class.go:58` | `task.(Store).complete` | `param task.block#1` | `internal/task/lease.go:733` |
| `task.BlockVerdictMissing` | `verdict_missing` | `internal/task/class.go:58` | `seatcommit.planVerifier` | `result seatcommit.planVerifier#1` | `internal/seatcommit/verifier.go:130` |
| `task.BlockVerdictTreeMismatch` | `verdict_tree_mismatch` | `internal/task/class.go:62` | `seatcommit.verify` | `param seatcommit.(Result).refuse#0` | `internal/seatcommit/verifier.go:223`, `internal/seatcommit/verifier.go:252` |
| `task.ReasonGrantsInvalid` | `grants_invalid` | `internal/task/verdict.go:46` | `routing.(*Router).decide` | `field scheduler.Route.Block` | `internal/routing/router.go:521` |
| `task.ReasonGrantsInvalid` | `grants_invalid` | `internal/task/verdict.go:46` | `scheduler.(*Scheduler).dispatch` | `param task.(Store).Block#2` | `internal/scheduler/scheduler.go:964` |
| `task.RefusalPRBoundElsewhere` | `pr_bound_elsewhere` | `internal/task/scope.go:107` | `scheduler.(*Scheduler).applyCommit` | `field scheduler.CommitResult.ReasonCode` | `internal/scheduler/scheduler.go:802` |

## 2. The wait list

20 constants with a writer, 27 rows.

| constant | value | declared at | writer function | door | sites |
|---|---|---|---|---|---|
| `claudeseat.TokenInsecure` | `claude_token_insecure` | `internal/claudeseat/token.go:30` | `seatexec.prepare` | `param seatexec.refusef#0` | `internal/seatexec/seatexec_unix.go:200` |
| `routing.SkipConcurrencyCap` | `concurrency_cap` | `internal/routing/router.go:36` (`= task.WaitConcurrencyCap`) | `routing.waitReason` | `element in routing.waitReason` | `internal/routing/router.go:803` |
| `routing.SkipCostCap` | `cost_cap` | `internal/routing/router.go:37` (`= task.WaitCostCap`) | `routing.waitReason` | `element in routing.waitReason` | `internal/routing/router.go:803` |
| `routing.SkipPrivacyClass` | `privacy_class` | `internal/routing/router.go:35` (`= task.WaitPrivacyClass`) | `routing.waitReason` | `element in routing.waitReason` | `internal/routing/router.go:803` |
| `routing.SkipReviewerFamily` | `reviewer_family` | `internal/routing/router.go:31` | `routing.waitReason` | `element in routing.waitReason` | `internal/routing/router.go:803` |
| `scheduler.GoneProcessExited` | `process_exited` | `internal/scheduler/launchfail.go:30` | `scheduler.(*Scheduler).checkLiveness` | `field scheduler.Event.ReasonCode` | `internal/scheduler/launchfail.go:157` |
| `scheduler.ReasonResourcePresent` | `seat_replacement_resource_present` | `internal/scheduler/roster.go:89` | `seatlaunch.rosterOutcome` | `field scheduler.RosterOutcome.ReasonCode` | `internal/seatlaunch/roster.go:258` |
| `scheduler.WaitNoRosterSeat` | `no_roster_seat` | `internal/scheduler/scheduler.go:265` | `scheduler.(*Scheduler).rosterNoIdleSeat` | `param scheduler.(*Scheduler).wait#1` | `internal/scheduler/roster.go:513` |
| `seatexec.ClaudeHooksMissing` | `claude_hooks_missing` | `internal/seatexec/claude.go:26` (`= claudeseat.HooksMissing`) | `seatexec.CheckClaudeHooks` | `param seatexec.refusef#0` | `internal/seatexec/claude.go:54`, `internal/seatexec/claude.go:65` |
| `seatexec.ClaudePermissionMismatch` | `claude_permission_mismatch` | `internal/seatexec/claude.go:27` (`= claudeseat.PermissionMismatch`) | `seatexec.CheckClaudePermission` | `param seatexec.refusef#0` | `internal/seatexec/claude.go:75`, `internal/seatexec/claude.go:81`, `internal/seatexec/claude.go:83`, `internal/seatexec/claude.go:91` |
| `seatexec.IdentityIncomplete` | `identity_incomplete` | `internal/seatexec/env.go:23` | `seatexec.checkIdentity` | `param seatexec.refusef#0` | `internal/seatexec/seatexec_unix.go:430` |
| `seatexec.IdentityIncomplete` | `identity_incomplete` | `internal/seatexec/env.go:23` | `seatexec.prepare` | `param seatexec.refusef#0` | `internal/seatexec/seatexec_unix.go:184` |
| `seatexec.PiNotEmbedded` | `pi_not_embedded` | `internal/seatexec/pi.go:68` | `seatexec.piShimProvider` | `param seatexec.refusef#0` | `internal/seatexec/pi.go:82`, `internal/seatexec/pi.go:85` |
| `seatexec.SandboxPinsMissing` | `sandbox_pins_missing` | `internal/seatexec/codex.go:11` | `seatexec.CheckCodexSandboxPins` | `param seatexec.refusef#0` | `internal/seatexec/seatexec_unix.go:588`, `internal/seatexec/seatexec_unix.go:593` |
| `task.WaitDependencies` | `dependencies` | `internal/task/class.go:106` | `task.Blocker` | `result task.Blocker#0` | `internal/task/lease.go:26`, `internal/task/lease.go:35` |
| `task.WaitFileLock` | `file_lock` | `internal/task/class.go:107` | `task.Blocker` | `result task.Blocker#0` | `internal/task/lease.go:41` |
| `task.WaitNoRoute` | `no_route` | `internal/task/class.go:108` | `routing.(*Router).Route` | `field scheduler.Route.Wait` | `internal/routing/router.go:480` |
| `task.WaitNoRoute` | `no_route` | `internal/task/class.go:108` | `scheduler.(DefaultRouter).Route` | `field scheduler.Route.Wait` | `internal/scheduler/hooks.go:118` |
| `task.WaitNoRoute` | `no_route` | `internal/task/class.go:108` | `scheduler.(*Scheduler).dispatch` | `param scheduler.firstNonEmpty#1` | `internal/scheduler/scheduler.go:906` |
| `task.WaitNoRoute` | `no_route` | `internal/task/class.go:108` | `routing.waitReason` | `result routing.waitReason#0` | `internal/routing/router.go:808` |
| `task.WaitOutcomeLogUnwritable` | `outcome_log_unwritable` | `internal/task/class.go:120` | `routing.(*Router).Route` | `field scheduler.Route.Wait` | `internal/routing/router.go:488`, `internal/routing/router.go:496` |
| `task.WaitOutcomeLogUnwritable` | `outcome_log_unwritable` | `internal/task/class.go:120` | `routing.(*Router).holdTampered` | `field scheduler.Route.Wait` | `internal/routing/tamper.go:219` |
| `task.WaitSeatCap` | `seat_cap` | `internal/task/class.go:110` | `scheduler.(*Scheduler).dispatch` | `param scheduler.(*Scheduler).wait#1` | `internal/scheduler/scheduler.go:914`, `internal/scheduler/scheduler.go:1039` |
| `task.WaitSeatCap` | `seat_cap` | `internal/task/class.go:110` | `scheduler.(*Scheduler).rosterNoIdleSeat` | `param scheduler.(*Scheduler).wait#1` | `internal/scheduler/roster.go:539` |
| `task.WaitSeatStarting` | `seat_starting` | `internal/task/class.go:109` | `scheduler.(*Scheduler).waitForSeat` | `param scheduler.(*Scheduler).wait#1` | `internal/scheduler/launchfail.go:232` |
| `task.WaitUsageLimit` | `usage_limit` | `internal/task/class.go:111` | `scheduler.(*Scheduler).dispatch` | `param scheduler.(*Scheduler).wait#1` | `internal/scheduler/scheduler.go:1035` |
| `task.WaitUsageLimit` | `usage_limit` | `internal/task/class.go:111` | `scheduler.(*Scheduler).rosterNoIdleSeat` | `param scheduler.(*Scheduler).wait#1` | `internal/scheduler/roster.go:534` |

## 3. Constants with a mark

### 3a. "no writer"

A constant that names a reason and that no writer site passes by name. The
mark is about the name, not the string: the value of a marked constant can
still reach a task under another constant's name (the pairs of section 8;
`task.WaitPrivacyClass`, `task.WaitConcurrencyCap` and `task.WaitCostCap` are
written under the names `routing.SkipPrivacyClass`, `routing.SkipConcurrencyCap`
and `routing.SkipCostCap`, which are declared by reference to them) or through
an open door of section 6 (`widen_requested` through the `--reason` flag).
The list is a reading: the test checks that a marked constant has no declared
writer and no use by name at a declared door, and does not find a constant
that is missing from it. Beside each: every line of non-test Go that holds its
name as a word (`git grep -n -w <Name> 1a3db2f1 -- '*.go' ':!*_test.go'`, the
commit prefix cut).

- block list: `task.ReasonRePinAfterAttempt` = `repin_after_attempt`, declared at `internal/task/lease.go:926`. 4 lines:
  - `internal/task/lease.go:924`: `// ReasonRePinAfterAttempt is the refusal of a pool change on a task whose`
  - `internal/task/lease.go:926`: `const ReasonRePinAfterAttempt = "repin_after_attempt"`
  - `internal/task/lease.go:928`: `// ErrRePinAfterAttempt wraps every refusal of ReasonRePinAfterAttempt, so a`
  - `internal/task/lease.go:930`: `var ErrRePinAfterAttempt = errors.New(ReasonRePinAfterAttempt)`
- block list: `task.ReasonWidenRequested` = `widen_requested`, declared at `internal/task/task.go:27`. 13 lines:
  - `internal/cli/task_widen.go:471`: `(t.BlockReason == task.ReasonWidenRequested || t.BlockReason == seatcommit.ReasonGrowthGate)`
  - `internal/routing/refusal.go:63`: `task.ReasonWidenRequested:            false,`
  - `internal/scheduler/exits.go:463`: `(t.BlockReason == task.ReasonWidenRequested || t.BlockReason == task.ReasonGrowthGateRequired) {`
  - `internal/seatcommit/commit.go:558`: `return ArchiveAttemptReason(root, handle, gitBin, env, taskID, attempt, "blocked: "+task.ReasonWidenRequested, now)`
  - `internal/task/lease.go:848`: `if reason == ReasonWidenRequested {`
  - `internal/task/lease.go:854`: `return Task{}, fmt.Errorf("a %s block needs at least one path", ReasonWidenRequested)`
  - `internal/task/lease.go:858`: `return Task{}, fmt.Errorf("block paths are only valid with reason %s", ReasonWidenRequested)`
  - `internal/task/lease.go:869`: `if reason == ReasonWidenRequested {`
  - `internal/task/lease.go:870`: `return fmt.Errorf("%s: a typed widen request requires the holder of a live attempt", ReasonWidenRequested)`
  - `internal/task/lease.go:879`: `if reason == ReasonWidenRequested {`
  - `internal/task/scope.go:55`: `(t.BlockReason == ReasonWidenRequested || t.BlockReason == ReasonGrowthGateRequired)`
  - `internal/task/task.go:25`: `// ReasonWidenRequested is a seat's typed request for the lead to add the`
  - `internal/task/task.go:27`: `const ReasonWidenRequested = "widen_requested"`
- block list: `task.ReasonGrowthGateRequired` = `growth_gate_required`, declared at `internal/task/task.go:31`. 6 lines:
  - `internal/cli/task_widen.go:212`: `case task.ReasonGrowthGateRequired:`
  - `internal/scheduler/exits.go:463`: `(t.BlockReason == task.ReasonWidenRequested || t.BlockReason == task.ReasonGrowthGateRequired) {`
  - `internal/seatcommit/widen.go:24`: `const ReasonGrowthGate = task.ReasonGrowthGateRequired`
  - `internal/task/scope.go:55`: `(t.BlockReason == ReasonWidenRequested || t.BlockReason == ReasonGrowthGateRequired)`
  - `internal/task/task.go:29`: `// ReasonGrowthGateRequired is a typed widen request escalated for the`
  - `internal/task/task.go:31`: `const ReasonGrowthGateRequired = "growth_gate_required"`
- block list: `seatcommit.RefusalSeatHeadDetachedUnmerged` = `seat_head_detached_unmerged`, declared at `internal/seatcommit/commit.go:635`. 3 lines:
  - `internal/seatcommit/commit.go:627`: `// RefusalSeatHeadDetachedUnmerged refuses a clean of a worktree whose HEAD`
  - `internal/seatcommit/commit.go:635`: `const RefusalSeatHeadDetachedUnmerged = "seat_head_detached_unmerged"`
  - `internal/seatcommit/commit.go:695`: `return false, fmt.Errorf("%s: seat %s: the worktree's branch %s holds an unrecorded commit %s; the harness never discards a local commit", RefusalSeatHeadDetachedUnmerged, handle, firstNonEmpty(strings.TrimPrefix(rp.HeadRef, "refs/heads/"), "(detached HEAD)"), ahead)`
- block list: `seatcommit.ReasonEvidenceRefSymbolic` = `evidence_ref_symbolic`, declared at `internal/seatcommit/evidence_purge.go:15`. 4 lines:
  - `internal/routing/refusal.go:40`: `seatcommit.ReasonEvidenceRefSymbolic:      false,`
  - `internal/seatcommit/evidence_purge.go:13`: `// ReasonEvidenceRefSymbolic: an evidence ref is a symbolic ref (a seat can`
  - `internal/seatcommit/evidence_purge.go:15`: `const ReasonEvidenceRefSymbolic = "evidence_ref_symbolic"`
  - `internal/seatcommit/evidence_purge.go:84`: `return fmt.Errorf("%s: %s is a symbolic ref to %s; nothing was deleted in this repository", ReasonEvidenceRefSymbolic, r.Name, r.Symref)`
- block list: `seatcommit.ReasonVerifierTimeout` = `verifier_timeout`, declared at `internal/seatcommit/verifier.go:63`. 5 lines:
  - `internal/routing/refusal.go:69`: `seatcommit.ReasonVerifierTimeout: true,`
  - `internal/seatcommit/verifier.go:63`: `ReasonVerifierTimeout = "verifier_timeout"`
  - `internal/seatcommit/verifier.go:267`: `case ReasonVerifierTimeout:`
  - `internal/seatcommit/verifier.go:268`: `head = fmt.Sprintf("verifiers.%s: %s after %s, the process group was killed", p.class, ReasonVerifierTimeout, p.timeout)`
  - `internal/seatcommit/verifier.go:360`: `timer := time.AfterFunc(p.timeout, func() { kill(ReasonVerifierTimeout) })`
- block list: `seatcommit.ReasonVerifierOutput` = `verifier_output_exceeded`, declared at `internal/seatcommit/verifier.go:64`. 5 lines:
  - `internal/routing/refusal.go:70`: `seatcommit.ReasonVerifierOutput:  true,`
  - `internal/seatcommit/verifier.go:64`: `ReasonVerifierOutput  = "verifier_output_exceeded"`
  - `internal/seatcommit/verifier.go:269`: `case ReasonVerifierOutput:`
  - `internal/seatcommit/verifier.go:270`: `head = fmt.Sprintf("verifiers.%s: %s, over %d bytes of output, the process group was killed", p.class, ReasonVerifierOutput, p.limit)`
  - `internal/seatcommit/verifier.go:349`: `buf.over = func() { kill(ReasonVerifierOutput) }`
- block list: `seatcommit.ReasonVerifierStart` = `verifier_start_failed`, declared at `internal/seatcommit/verifier.go:65`. 5 lines:
  - `internal/routing/refusal.go:53`: `seatcommit.ReasonVerifierStart:       false,`
  - `internal/seatcommit/verifier.go:65`: `ReasonVerifierStart   = "verifier_start_failed"`
  - `internal/seatcommit/verifier.go:271`: `case ReasonVerifierStart:`
  - `internal/seatcommit/verifier.go:272`: `head = fmt.Sprintf("verifiers.%s: %s: %s", p.class, ReasonVerifierStart, run.startErr)`
  - `internal/seatcommit/verifier.go:356`: `run.exit, run.reason, run.startErr = -1, ReasonVerifierStart, err.Error()`
- block list: `seatcommit.ReasonVerifierCancelled` = `verifier_cancelled`, declared at `internal/seatcommit/verifier.go:73`. 6 lines:
  - `internal/routing/refusal.go:55`: `seatcommit.ReasonVerifierCancelled:   false,`
  - `internal/seatcommit/verifier.go:71`: `// ReasonVerifierCancelled: the task was cancelled while its verifier ran;`
  - `internal/seatcommit/verifier.go:73`: `ReasonVerifierCancelled = "verifier_cancelled"`
  - `internal/seatcommit/verifier.go:231`: `if run.reason == ReasonVerifierCancelled {`
  - `internal/seatcommit/verifier.go:232`: `r := res.defer_(ReasonVerifierCancelled + ": the task was cancelled while its verifier ran; the process group was killed, no verdict is recorded, nothing is pushed")`
  - `internal/seatcommit/verifier.go:366`: `kill(ReasonVerifierCancelled)`
- block list: `seatcommit.ReasonCommitFailed` = `commit_failed`, declared at `internal/seatcommit/policy.go:38`. 4 lines:
  - `internal/routing/refusal.go:51`: `seatcommit.ReasonCommitFailed:        false,`
  - `internal/scheduler/hooks.go:212`: `ReasonCommitFailed        = "commit_failed"`
  - `internal/scheduler/scheduler.go:770`: `code := ReasonCommitFailed`
  - `internal/seatcommit/policy.go:38`: `ReasonCommitFailed   = "commit_failed"          // the step kept failing (repeated deferrals)`
- block list: `scheduler.ReasonSeatIdentityUnproven` = `seat_identity_unproven`, declared at `internal/scheduler/exits.go:647`. 5 lines:
  - `internal/scheduler/exits.go:645`: `// ReasonSeatIdentityUnproven is the stop of a cancelled attempt whose`
  - `internal/scheduler/exits.go:647`: `const ReasonSeatIdentityUnproven = "seat_identity_unproven"`
  - `internal/scheduler/exits.go:674`: `if s.cleanDeferred[key] == ReasonSeatIdentityUnproven {`
  - `internal/scheduler/exits.go:677`: `s.cleanDeferred[key] = ReasonSeatIdentityUnproven`
  - `internal/scheduler/exits.go:678`: `log(Decision{Kind: "seat_may_be_writing", TaskID: it.TaskID, Seat: it.Handle, ReasonCode: ReasonSeatIdentityUnproven,`
- block list: `routing.ReasonInvalid` = `routing_invalid`, declared at `internal/routing/open.go:16`. 6 lines:
  - `internal/cli/routing.go:122`: `return doctorCheck{Name: "routing", Status: doctorFail, Kind: routing.ReasonInvalid, Detail: err.Error()}`
  - `internal/routing/open.go:13`: `// ReasonInvalid is the reason code of a routing table that does not load: a`
  - `internal/routing/open.go:16`: `const ReasonInvalid = "routing_invalid"`
  - `internal/routing/open.go:19`: `// to start on it with ReasonInvalid; it never falls back to the default`
  - `internal/routing/open.go:23`: `func (e *InvalidError) Error() string { return ReasonInvalid + ": " + e.Err.Error() }`
  - `internal/routing/open.go:51`: `// the process must refuse to start with ReasonInvalid.`
- block list: `scheduler.AMQBindingPresent` = `binding_present_without_resumable_conversation`, declared at `internal/scheduler/roster.go:85`. 5 lines:
  - `internal/scheduler/roster.go:83`: `// AMQBindingPresent is the reason AMQ gives a fresh seat launched into`
  - `internal/scheduler/roster.go:85`: `AMQBindingPresent = "binding_present_without_resumable_conversation"`
  - `internal/scheduler/roster.go:326`: `strings.Join(live, ","), AMQBindingPresent, RelaunchVerb)})`
  - `internal/scheduler/roster.go:543`: `log(Decision{Kind: ReasonSeatRelaunch, TaskID: t.ID, Seat: strings.Join(gone, ","), Pool: p.Key(), Reason: AMQBindingPresent})`
  - `internal/scheduler/roster.go:545`: `p.Key(), strings.Join(gone, ", "), AMQBindingPresent, RelaunchVerb)`
- block list: `schedproc.ReasonSeatHeadDetachedUnmerged` = `seat_head_detached_unmerged`, declared at `internal/schedproc/slots.go:259`. 3 lines:
  - `internal/schedproc/slots.go:213`: `return seat, fmt.Errorf("%s: seat %s: the worktree's HEAD is at %s, with commits the %s tip %s does not hold (%s); the harness never discards a local commit: push, merge or branch it, then launch again", ReasonSeatHeadDetachedUnmerged, seat.Handle, short(head), firstNonEmpty(s.BaseBranch, "main"), short(tip), strings.Join(strings.Fields(ahead), ", "))`
  - `internal/schedproc/slots.go:255`: `// ReasonSeatHeadDetachedUnmerged refuses a seat's launch when its worktree's`
  - `internal/schedproc/slots.go:259`: `const ReasonSeatHeadDetachedUnmerged = "seat_head_detached_unmerged"`
- wait list: `task.WaitPrivacyClass` = `privacy_class`, declared at `internal/task/class.go:114`. 2 lines:
  - `internal/routing/router.go:35`: `SkipPrivacyClass        = task.WaitPrivacyClass`
  - `internal/task/class.go:114`: `WaitPrivacyClass   = "privacy_class"   // only vendor entries passed the other checks, and a declared path or the grant is local-only`
- wait list: `task.WaitConcurrencyCap` = `concurrency_cap`, declared at `internal/task/class.go:115`. 2 lines:
  - `internal/routing/router.go:36`: `SkipConcurrencyCap      = task.WaitConcurrencyCap`
  - `internal/task/class.go:115`: `WaitConcurrencyCap = "concurrency_cap" // every passing entry's concurrency pool is full`
- wait list: `task.WaitCostCap` = `cost_cap`, declared at `internal/task/class.go:116`. 2 lines:
  - `internal/routing/router.go:37`: `SkipCostCap             = task.WaitCostCap`
  - `internal/task/class.go:116`: `WaitCostCap        = "cost_cap"        // the tier's cost_index cap or the grant's per-day budget stops the entry`
- wait list: `task.WaitReviewerFamily` = `reviewer_family`, declared at `internal/task/class.go:117`. 1 line:
  - `internal/task/class.go:117`: `WaitReviewerFamily = "reviewer_family" // a review whose only passing entries share the implementer's family`

### 3b. "traced, guarded at <function>"

A constant the trace shows at a writer site and that, by the lead's reading of
the code (read, not run), never reaches the field. The seat confirms each guard
by reading and cites the lines in the note.

- `scheduler.GoneProcessExited` = `process_exited`, declared at `internal/scheduler/launchfail.go:30`, on the block and the wait list: `traced, guarded at scheduler.(*Scheduler).applyEvents`. `scheduler.(*Scheduler).checkLiveness` posts it as the code of a `SeatGone` event (`internal/scheduler/launchfail.go:157`). The `SeatGone` arm of `applyEvents` (`internal/scheduler/scheduler.go:620-652`) uses the event's code once, in the decision line at `:630`.
- `seatcommit.ReasonPRPending` = `pr_pending`, declared at `internal/seatcommit/commit.go:1331`, on the block list: `traced, guarded at scheduler.(*Scheduler).commit`. It is set only on a deferred result: both sites follow a call of `defer_` (`internal/seatcommit/commit.go:1482-1483`, `:1518-1519`), which sets `Deferred` (`internal/seatcommit/commit.go:134-137`), and `internal/schedproc/committer.go:91` copies a deferred result with its code. For a deferred result `scheduler.(*Scheduler).commit` returns (`internal/scheduler/scheduler.go:765`) or replaces the whole result at `:774`, with the code `ReasonCommitFailed` or `ReasonVerifierInterrupted` (`:770-773`), before `applyCommit` is called at `:784`.
- `seatcommit.ReasonStepCancelled` = `commit_cancelled`, declared at `internal/seatcommit/commit.go:173`, on the block list: `traced, guarded at scheduler.(*Scheduler).commit`. The same guard: both sites follow a call of `defer_` (`internal/seatcommit/commit.go:409-410`, `internal/seatcommit/verifier.go:232-233`).

## 4. Not a reason

Six constants of `internal/task` that are refusal codes and never a task's
block or wait reason. Each is declared on the not-a-reason list and on neither
of the other two.

- `task.RefusalFileLock` = `file_lock_conflict`, declared at `internal/task/scope.go:17`
- `task.RefusalSelfWiden` = `self_widen`, declared at `internal/task/scope.go:21`
- `task.RefusalClaimOfferRequired` = `claim_offer_required`, declared at `internal/task/lease.go:106`
- `task.RefusalClaimOfferExpired` = `claim_offer_expired`, declared at `internal/task/lease.go:107`
- `task.RefusalClaimPoolMismatch` = `claim_pool_mismatch`, declared at `internal/task/lease.go:108`
- `task.RefusalClaimPoolUnknown` = `claim_pool_unknown`, declared at `internal/task/lease.go:109`

## 5. Values written as a string literal only

Not in the data: a value with no constant cannot be a constant reference. Making
them constants is S2's "every code a constant" work, in two items.

### 5a. The launch family: 58 values, 93 sites

Each is traced into both fields, and the chain the trace prints for each of
the 93 sites goes through `scheduler.(LaunchFailure).Code` (section 6).
Sites by package: seatexec 49, seatlaunch 33, piruntime 5, schedproc 3, scheduler 3.
Four of them are stage or state words that the lead reads as never reaching a
task (section 5c).

| value | sites |
|---|---|
| `applied` | `internal/seatlaunch/run.go:344` |
| `apply_ambiguous` | `internal/seatlaunch/launcher.go:234` |
| `apply_failed` | `internal/seatlaunch/roster.go:243`, `internal/seatlaunch/run.go:340` |
| `argv_missing` | `internal/seatexec/seatexec_unix.go:96` |
| `codex_approval_missing` | `internal/seatexec/seatexec_unix.go:345`, `internal/seatexec/seatexec_unix.go:353` |
| `codex_sandbox_missing` | `internal/seatexec/seatexec_unix.go:331`, `internal/seatexec/seatexec_unix.go:343`, `internal/seatexec/seatexec_unix.go:350` |
| `decisions_pending` | `internal/seatlaunch/run.go:275` |
| `dir_writable_by_others` | `internal/seatexec/anchor_unix.go:92` |
| `env_needs_invalid` | `internal/seatexec/env.go:168`, `internal/seatexec/env.go:171`, `internal/seatexec/env.go:176`, `internal/seatexec/env.go:179`, `internal/seatexec/env.go:182` |
| `identity_forged` | `internal/seatlaunch/proof_unix.go:424`, `internal/seatlaunch/proof_unix.go:425` |
| `identity_mismatch` | `internal/seatexec/seatexec_unix.go:93`, `internal/seatexec/seatexec_unix.go:433` |
| `identity_proof_failed` | `internal/seatexec/seatexec_unix.go:516`, `internal/seatexec/seatexec_unix.go:524`, `internal/seatexec/seatexec_unix.go:527`, `internal/seatexec/seatexec_unix.go:530` |
| `identity_proof_timeout` | `internal/seatexec/seatexec_unix.go:513` |
| `launch_error` | `internal/schedproc/roster.go:92`, `internal/schedproc/roster.go:95`, `internal/schedproc/roster.go:101`, `internal/scheduler/launchfail.go:167`, `internal/scheduler/roster.go:368`, `internal/scheduler/roster.go:391`, `internal/seatlaunch/launcher.go:230`, `internal/seatlaunch/roster.go:30`, `internal/seatlaunch/roster.go:245` |
| `ledger_unreadable` | `internal/piruntime/session.go:132` |
| `mailbox_missing` | `internal/seatexec/seatexec_unix.go:124` |
| `mkdir_failed` | `internal/seatexec/anchor_unix.go:105` |
| `no_proof` | `internal/seatlaunch/run.go:458` |
| `not_observed` | `internal/seatlaunch/launcher.go:236` |
| `not_started` | `internal/seatlaunch/run.go:458` |
| `path_invalid` | `internal/seatexec/anchor_unix.go:62` |
| `pending` | `internal/seatlaunch/run.go:469` |
| `pi_resume_context_lost` | `internal/piruntime/session.go:142` |
| `pi_session_id_invalid` | `internal/piruntime/session.go:128` |
| `pi_session_id_missing` | `internal/piruntime/session.go:125` |
| `pi_session_unreadable` | `internal/piruntime/session.go:139` |
| `prepare_refused` | `internal/seatlaunch/run.go:247` |
| `prepared` | `internal/seatlaunch/run.go:269` |
| `proof_expired` | `internal/seatexec/seatexec_unix.go:265`, `internal/seatlaunch/proof_unix.go:356` |
| `proof_ledger_failed` | `internal/seatlaunch/proof_unix.go:409` |
| `proof_malformed` | `internal/seatlaunch/proof_unix.go:289` |
| `proof_marker_failed` | `internal/seatlaunch/proof_unix.go:398` |
| `proof_origin_unknown` | `internal/seatlaunch/proof_unix.go:363`, `internal/seatlaunch/proof_unix.go:373`, `internal/seatlaunch/proof_unix.go:383`, `internal/seatlaunch/proof_unix.go:389` |
| `proof_refused` | `internal/seatexec/seatexec_unix.go:274` |
| `proof_ticket_consumed` | `internal/seatexec/seatexec_unix.go:263` |
| `proof_ticket_missing` | `internal/seatexec/seatexec_unix.go:259` |
| `proof_token_invalid` | `internal/seatlaunch/proof_unix.go:347` |
| `proof_unacknowledged` | `internal/seatexec/seatexec_unix.go:271` |
| `proof_unknown` | `internal/seatlaunch/proof_unix.go:342` |
| `provider_missing` | `internal/seatexec/seatexec_unix.go:489` |
| `record_invalid` | `internal/seatexec/pi.go:77`, `internal/seatexec/seatexec_unix.go:115` |
| `record_missing` | `internal/seatexec/seatexec_unix.go:106`, `internal/seatexec/seatexec_unix.go:111` |
| `refused` | `internal/seatlaunch/run.go:477`, `internal/seatlaunch/run.go:484` |
| `resource_present` | `internal/seatlaunch/run.go:263` |
| `role_mismatch` | `internal/seatexec/seatexec_unix.go:139`, `internal/seatexec/seatexec_unix.go:305` |
| `role_missing` | `internal/seatexec/seatexec_unix.go:136` |
| `root_invalid` | `internal/seatexec/anchor_unix.go:33` |
| `root_unreadable` | `internal/seatexec/anchor_unix.go:37` |
| `rules_mismatch` | `internal/seatexec/seatexec_unix.go:290`, `internal/seatexec/seatexec_unix.go:315` |
| `rules_missing` | `internal/seatexec/seatexec_unix.go:287` |
| `runtime_mismatch` | `internal/seatexec/pi.go:74`, `internal/seatexec/seatexec_unix.go:485` |
| `seat_invalid` | `internal/seatexec/seatexec_unix.go:90` |
| `seat_not_up` | `internal/seatlaunch/launcher.go:234`, `internal/seatlaunch/launcher.go:236`, `internal/seatlaunch/launcher.go:238` |
| `stat_failed` | `internal/seatexec/anchor_unix.go:86` |
| `symlink_in_path` | `internal/seatexec/anchor_unix.go:67`, `internal/seatexec/anchor_unix.go:126` |
| `up` | `internal/seatlaunch/run.go:473` |
| `write_failed` | `internal/seatexec/anchor_unix.go:148`, `internal/seatexec/anchor_unix.go:163` |
| `wrong_owner` | `internal/seatexec/anchor_unix.go:89` |

### 5b. The two router literals

- `class_unknown` (block list): `internal/routing/router.go:518`
- `retries_exhausted` (block list): `internal/routing/router.go:537`, `internal/routing/router.go:653`, `internal/scheduler/hooks.go:115`

Two block values have a constant and are also written as a literal, in a spike
file of the root module; the literals are the spike's and are left:

- `commit_failed`: `spike/pi-r19/main.go:400`, `spike/pi-r19/main.go:417`, `spike/pi-r19/main.go:420`
- `outside_declared_files`: `spike/pi-r19/main.go:410`

### 5c. The four launch words read as guarded

The lead's reading of the code (read, not run). They are literals, so they carry
no mark in the data.

- `pending`: guarded at `seatlaunch.WaitSeats`. `seatState` starts every state at `pending` (`internal/seatlaunch/run.go:469`); before `WaitSeats` returns, every state still `pending` is rewritten to state `not_started` with reason code `no_proof` (`internal/seatlaunch/run.go:455-463`).
- `up`: guarded at `seatlaunch.(*Launcher).settleOne`. The state `up` is set only with `Up` true (`internal/seatlaunch/run.go:473`), and a seat that is up settles as up or closing (`internal/seatlaunch/launcher.go:191-194`), never as a failure.
- `prepared`: guarded by the absence of a writer. `seatlaunch.Launch` sets the stage `prepared` only in its dry-run arm (`internal/seatlaunch/run.go:268-273`), and no non-test code sets `seatlaunch.Options.DryRun`.
- `applied`: never a code. `seatlaunch.launchFailure` returns the roster's stage only when it is not `applied` (`internal/seatlaunch/launcher.go:229-231`).

## 6. The open doors

Places where a reason enters that no list of ours holds. The data names each by
package and function; the lines are here.

### The block list: 11

- the launch failure code: the reason code, else the stage. Named in the data by: `scheduler.(LaunchFailure).Code`. Lines: `internal/scheduler/launchfail.go:60` (`return firstNonEmpty(f.ReasonCode, f.Stage)`).
- AMQ launchapi field Reason, copied by seatlaunch.rosterOutcome. Named in the data by: `seatlaunch.rosterOutcome`. Lines: `internal/seatlaunch/roster.go:247` (`out.ReasonCode = res.Prepare.Reason`); the field is `launchapi/types.go:335` of AMQ v0.140.0.
- AMQ launchapi field ReasonCode, copied by seatlaunch.rosterOutcome. Named in the data by: `seatlaunch.rosterOutcome`. Lines: `internal/seatlaunch/roster.go:256` (`out.ReasonCode = res.Apply.ReasonCode`); the field is `launchapi/types.go:371` of AMQ v0.140.0.
- the pending action kinds joined with a comma. Named in the data by: `seatlaunch.rosterOutcome`. Lines: `internal/seatlaunch/roster.go:253` (`strings.Join(kinds, ",")`).
- the --reason flag of task block. Named in the data by: `cli.runTaskTransition`. Lines: `internal/cli/task.go:527`, in the function declared at `internal/cli/task.go:524`.
- decoded field launch.ProofReply.Code. Named in the data by: `seatexec.DialProof`. Lines: the field `internal/launch/proof.go:70`; decoded at `internal/seatexec/seatexec_unix.go:652`.
- decoded field launch.Exec.ReasonCode. Named in the data by: `launch.Decode`, `uninstall.seatsLive`. Lines: the field `internal/launch/record.go:93`; decoded at `internal/launch/record.go:184` and `internal/uninstall/busy.go:183`.
- decoded field scheduler.LaunchFailure.Stage. Named in the data by: `scheduler.ReadState`. Lines: the field `internal/scheduler/launchfail.go:43`; decoded at `internal/scheduler/scheduler.go:1373`.
- decoded field scheduler.LaunchFailure.ReasonCode. Named in the data by: `scheduler.ReadState`. Lines: the field `internal/scheduler/launchfail.go:44`; decoded at `internal/scheduler/scheduler.go:1373`.
- field task.Task.BlockReason read back from disk. Named in the data by: `task.snapshotOnce`, `task.readDir`, `task.recoverJournal`. Lines: the field `internal/task/task.go:73`; decoded at `internal/task/task.go:382`, `internal/task/task.go:542` and `internal/task/transaction.go:100`.
- parameter reasonCode of task.(Store).RefuseCommit, which no code outside tests calls. Named in the data by: `task.(Store).RefuseCommit`. Lines: `internal/task/lease.go:781`.

### The wait list: 10

- the launch failure code: the reason code, else the stage. Named in the data by: `scheduler.(LaunchFailure).Code`. Lines: `internal/scheduler/launchfail.go:60` (`return firstNonEmpty(f.ReasonCode, f.Stage)`).
- AMQ launchapi field Reason, copied by seatlaunch.rosterOutcome. Named in the data by: `seatlaunch.rosterOutcome`. Lines: `internal/seatlaunch/roster.go:247` (`out.ReasonCode = res.Prepare.Reason`); the field is `launchapi/types.go:335` of AMQ v0.140.0.
- AMQ launchapi field ReasonCode, copied by seatlaunch.rosterOutcome. Named in the data by: `seatlaunch.rosterOutcome`. Lines: `internal/seatlaunch/roster.go:256` (`out.ReasonCode = res.Apply.ReasonCode`); the field is `launchapi/types.go:371` of AMQ v0.140.0.
- the pending action kinds joined with a comma. Named in the data by: `seatlaunch.rosterOutcome`. Lines: `internal/seatlaunch/roster.go:253` (`strings.Join(kinds, ",")`).
- decoded field launch.ProofReply.Code. Named in the data by: `seatexec.DialProof`. Lines: the field `internal/launch/proof.go:70`; decoded at `internal/seatexec/seatexec_unix.go:652`.
- decoded field launch.Exec.ReasonCode. Named in the data by: `launch.Decode`, `uninstall.seatsLive`. Lines: the field `internal/launch/record.go:93`; decoded at `internal/launch/record.go:184` and `internal/uninstall/busy.go:183`.
- decoded field scheduler.LaunchFailure.Stage. Named in the data by: `scheduler.ReadState`. Lines: the field `internal/scheduler/launchfail.go:43`; decoded at `internal/scheduler/scheduler.go:1373`.
- decoded field scheduler.LaunchFailure.ReasonCode. Named in the data by: `scheduler.ReadState`. Lines: the field `internal/scheduler/launchfail.go:44`; decoded at `internal/scheduler/scheduler.go:1373`.
- field task.Task.WaitReason read back from disk. Named in the data by: `task.snapshotOnce`, `task.readDir`, `task.recoverJournal`. Lines: the field `internal/task/task.go:72`; decoded at `internal/task/task.go:382`, `internal/task/task.go:542` and `internal/task/transaction.go:100`.
- the sentence task.DependencyMergeWait formats. Named in the data by: `task.DependencyMergeWait`. Lines: `internal/task/class.go:128-130` (`fmt.Sprintf`).

## 7. The door set

A door is where the trace shows a value entering the path to `block` or
`SetWait`: a parameter, a field, a result, a local or an element. A listed
constant's writer is the function that uses it by name at a door.

### The block list: 14

- `field scheduler.CommitResult.ReasonCode`: `internal/scheduler/hooks.go:127`
- `field scheduler.Event.ReasonCode`: `internal/scheduler/scheduler.go:96`
- `field scheduler.RosterOutcome.ReasonCode`: `internal/scheduler/roster.go:53`
- `field scheduler.Route.Block`: `internal/scheduler/hooks.go:84`
- `field seatcommit.Result.ReasonCode`: `internal/seatcommit/commit.go:96`
- `field seatcommit.Violation.Reason`: `internal/seatcommit/policy.go:60`
- `local code in scheduler.(*Scheduler).commit`: `internal/scheduler/scheduler.go:742`
- `param seatcommit.(Result).refuse#0`: `internal/seatcommit/commit.go:126`
- `param seatexec.refusef#0`: `internal/seatexec/env.go:25`
- `param task.(Store).Block#2`: `internal/task/lease.go:838`
- `param task.(Store).Escalate#1`: `internal/task/scope.go:152`
- `param task.block#1`: `internal/task/lease.go:808`
- `result registry.OpenPinned#1`: `internal/registry/pins.go:84`
- `result seatcommit.planVerifier#1`: `internal/seatcommit/verifier.go:121`

### The wait list: 9

- `element in routing.waitReason`: `internal/routing/router.go:799`
- `field scheduler.Event.ReasonCode`: `internal/scheduler/scheduler.go:96`
- `field scheduler.RosterOutcome.ReasonCode`: `internal/scheduler/roster.go:53`
- `field scheduler.Route.Wait`: `internal/scheduler/hooks.go:83`
- `param scheduler.(*Scheduler).wait#1`: `internal/scheduler/scheduler.go:1197`
- `param scheduler.firstNonEmpty#1`: `internal/scheduler/scheduler.go:1445`
- `param seatexec.refusef#0`: `internal/seatexec/env.go:25`
- `result routing.waitReason#0`: `internal/routing/router.go:799`
- `result task.Blocker#0`: `internal/task/lease.go:18`

## 8. One string, two constants

Each is two entries, each with its own sites or its own mark. Uniting a pair is
S4's.

- block list, `commit_failed`: `scheduler.ReasonCommitFailed`, `seatcommit.ReasonCommitFailed`
- block list, `grants_invalid`: `seatcommit.ReasonGrants`, `task.ReasonGrantsInvalid`
- block list, `growth_gate_required`: `seatcommit.ReasonGrowthGate`, `task.ReasonGrowthGateRequired`
- block list, `ledger_tampered`: `scheduler.ReasonLedgerTampered`, `seatcommit.ReasonLedgerTampered`
- block list, `pr_bound_elsewhere`: `seatcommit.ReasonPRBoundElsewhere`, `task.RefusalPRBoundElsewhere`
- block list, `seat_head_detached_unmerged`: `schedproc.ReasonSeatHeadDetachedUnmerged`, `seatcommit.RefusalSeatHeadDetachedUnmerged`
- block list, `verifier_interrupted`: `scheduler.ReasonVerifierInterrupted`, `seatcommit.ReasonVerifierInterrupted`
- wait list, `concurrency_cap`: `routing.SkipConcurrencyCap`, `task.WaitConcurrencyCap`
- wait list, `cost_cap`: `routing.SkipCostCap`, `task.WaitCostCap`
- wait list, `privacy_class`: `routing.SkipPrivacyClass`, `task.WaitPrivacyClass`
- wait list, `reviewer_family`: `routing.SkipReviewerFamily`, `task.WaitReviewerFamily`

## 9. The empty string

Not in the data: it is no constant and no reason. The trace finds it at both
fields, and this is the lead's reading of each site (read, not run).

### The block field: 12 sites

- 6 direct writes of `""` to the field, in `internal/task`: `internal/task/lease.go:744`, `internal/task/lease.go:915`, `internal/task/reset_by.go:34`, `internal/task/reset_if.go:29`, `internal/task/scope.go:91`, `internal/task/scope.go:98`. Each is in a statement that sets the task's state to a state other than blocked (completed at `internal/task/lease.go:744`; pending at `internal/task/lease.go:915`, `internal/task/reset_by.go:34` and `internal/task/reset_if.go:29`; in progress at `internal/task/scope.go:91`), except `internal/task/scope.go:98`, which follows the statement at `:97` that sets it to pending.
- 6 sites where the trace shows `""` reaching `block`'s `reason` parameter: `internal/registry/pins.go:111`, `internal/seatcommit/verifier.go:124`, `internal/seatcommit/verifier.go:132`, `internal/seatcommit/verifier.go:160`, `internal/seatexec/options.go:124`, `internal/seatlaunch/proof_unix.go:547`. Guarded at `task.block`: it returns an error for a reason that is empty once trimmed (`internal/task/lease.go:815-817`), before the write at `:819`.

### The wait field: 7 sites

- 4 direct writes of `""` to the field, in `internal/task`: `internal/task/lease.go:818`, `internal/task/lease.go:90`, `internal/task/lease.go:968`, `internal/task/scope.go:97`. Each is in a statement that sets the task's state (in progress at `internal/task/lease.go:90`, blocked at `:818`, cancelled at `:968`, pending at `internal/task/scope.go:97`).
- `internal/task/lease.go:45`, the last return of `task.Blocker`. Guarded at `scheduler.(*Scheduler).dispatch`: it passes `Blocker`'s result to `wait` only when the result is not empty (`internal/scheduler/scheduler.go:889-894`).
- `internal/seatexec/options.go:124`, `internal/seatlaunch/proof_unix.go:547`, traced through `scheduler.(LaunchFailure).Code` (`internal/scheduler/launchfail.go:60`) to the call of `wait` at `internal/scheduler/launchfail.go:230`. Not read as guarded: `wait` (`internal/scheduler/scheduler.go:1197-1206`) and `SetWait` (`internal/task/lease.go:1035-1047`) have no check for an empty reason, and the lead did not read what keeps a launch failure's reason code and stage from both being empty (both are also read back from the scheduler's state file, an open door of section 6).
