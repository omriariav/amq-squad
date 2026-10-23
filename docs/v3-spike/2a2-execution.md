# amq-squad 3.0, phase 2a-2 execution log

Branch `v3-2a2`, cut from `v3` at c8f030d. Scope: the in-file shrink (plan 4.1 step 5) plus the 2a-1 deferrals (`docs/v3-spike/2a1-execution.md`, sections "Deferred", "Coverage debit for 2a-2", "Holding file debit for 2a-2").
One cluster = one commit. Every commit is green on `go build ./... && go vet ./... && go test ./... && scripts/manifest-check.sh`, `GOOS=linux go build ./...` and `GOOS=windows go build ./...`. The gate script also vets internal/cli for linux and windows.

## Running total

Non-test lines under internal/ (`find internal -name '*.go' ! -name '*_test.go' | xargs wc -l | tail -1`). Start 72,972. Manifest target 33,504; the band is 33,100 plus or minus 10 percent (29,790 to 36,410).

| commit | cluster | lines after | holding file | tests listed |
| --- | --- | --- | --- | --- |
| start (c8f030d) | | 72,972 | 832 | 2,027 |
| 1 | C28 launch driver + C11 hosts | 70,555 | 786 | 1,918 |
| 2 | C18 team launch path + C1 tmux/pane + C4 preview_flags + team overlay | 63,459 | 557 | 1,714 |
| 3 | C19 dispatch: queue-then-send, pane nudges dropped (+ C10 live_identity) | 62,385 | 547 | 1,672 |
| 4 | C16 status + C2 runtime + C10 liveness + C3 board + C1 rest; packages runtimeaction, runtimecontrol, liveidentity, tmuxpane | 54,627 | 459 | 1,334 |
| 5 | C13 verify and AMQ root authority (helper h-c13, integrated) | 54,496 | 459 | 1,345 |
| 6 | C14 amq verb: env and route only (+ C11 wait_posture) (helper h-c14, integrated) | 53,305 | 459 | 1,303 |
| 7 | C23 roles, agents, model defaults, effort, native args (helper h-c23, integrated) | 52,842 | 450 | 1,291 |
| 8 | C27 brief drafting; simple_start_brief_draft.go deleted (helper h-c27, integrated) | 52,500 | 399 | 1,287 |
| 9 | C16/C10 follow-up under ruling 5; C2 runtime_identity.go and runtime_actions.go moved to homes | 52,242 | 390 | 1,283 |
| 10 | C17 operator: directive/poll/watch, notifications and operator delivery cut; packages notifier, attention/dedup.go | 50,426 | 383 | 1,242 |

Overage budget (lead ruling 5): with every remaining row on target the total lands near 32,700. The band top is 36,410, so the whole phase tolerates about 3,700 lines of combined over-target. Every per-row deferral draws from that budget. "Over-target now" sums (wc -l minus target_lines, floored at 0) over present non-keep, non-replace rows, using scratchpad overage.py. "Deferred so far" sums the rows the log records as deferred, with reasons.

| after commit | over-target now (all rows) | deferred so far |
| --- | --- | --- |
| 8 | 15,688 | 3,583 (see "Deferral ledger") |
| 9 | 15,197 | 3,583 |
| 10 | 13,406 | 3,583 |

## Rulings received from the lead

1. (start) `team overlay` follows the manifest: the verb and team_overlay.go are cut, and the spawn-time tool-policy check (validateMemberOverlayPaths) goes with them unless the manifest names it as an extract. 2b item: "tool policy comes from squad.yaml at launch (replaces validateMemberOverlayPaths)".
2. (start) Named manifest extracts are kept, with the helpers they need, even when deadcode reports them unreachable; they are the 2b ancestors. The rest of the dead code is cut. The kept-dead extracts are listed below with line counts.
3. (start) Keep-fate rows are not cut. Review-fate rows are reviewed line by line: code that deadcode reports unreachable and only tests reference is cut with those tests, each named. compoundrelease stays as a package; its test-only dead code (about 1.4k lines in store.go, reconcile.go, transition.go) falls under this rule. Replace rows (evidence.go, commandevidence/evidence.go) are deferred to 2b because they need the ledger: status `deferred-replace`, lines listed. The total is reported as measured. manifest-check computes the plus or minus 10 percent check over current lines excluding deferred-replace rows (see Tooling); the manifest targets are not lowered.

4. (commit 2) Team launch path: bare `team` prints the usage. The tmux backend, bootstrap_failure.go, the pane files, team_launch.go's driver and the legacy backend are cut. team_launch_launchapi.go keeps the launchapi Prepare/Apply backend as a kept-dead 2b ancestor; adoptionseam stays keep-fate.
5. (halfway checkpoint, accepted at a0d07d5)
   - The overage budget above.
   - liveness.go's 250 target predates keeping the launchapi Inspect floor, which 3.0 keeps as AMQ-owned liveness. Raise the target to the measured floor (code and blank lines of the kept Inspect path, plus a comment budget), record the decision with file:line evidence, and halve the comments.
   - status.go: name a 55-line code cut or charge it to the budget. "Revisit later" entries are not allowed.
   - Helper branches are integrated serially by the lead implementer: rebase, resolve by hand, full gate, log row and section, then remove the helper worktree. No more helper agents without asking.
   - The end checkpoint watch list: amq.go, internal/team/team.go, task.go, launch/record.go, namespace_migration_guard.go, role_draft.go and cli team.go.

End-of-2a gate (from ruling 3): every shrink row at or under target, every delete row gone, review rows reviewed with the dead cuts named, replace rows deferred and listed, holding file at zero.

## Tooling (scratchpad `2a2/`)

- `deadcode ./cmd/amq-squad` (golang.org/x/tools) lists unreachable functions. It is transitive, so one run lists every function that only dead code reaches.
- `cutdead.py TARGET...` deletes the unreachable functions declared in the cluster's files and honors `skip.txt`, the list of manifest extracts. It builds for darwin, linux and windows. On `undefined` errors it either cuts the enclosing caller, when deadcode also lists that caller as dead (recorded as a cross-cluster dead caller), or protects the symbol, when a live or extract function uses it.
- `cutunused.py TARGET...` runs golangci-lint `unused` (tests=false) and deletes top-level types, vars and consts that no non-test code uses any more, to a fixpoint. With `TESTS=1` it removes unused test-side helpers.
- `gocut` (stdlib Go) deletes declarations, with their doc comments and a deleted type's methods, and prunes and regroups imports.
- `sweep.sh` runs the 2a-1 test sweep: it deletes each test declaration that references a deleted symbol and logs every removal. The log entries below name every retired test.
- `mupdate.py` keeps manifest statuses in step with the tree.
- manifest-check.sh (commit 1): new statuses `shrunk` (a shrink row at or under target, current_lines <= target_lines enforced) and `deferred-replace` (fate replace rows kept for 2b, current_lines). Check 3 compares current_lines for trimmed, shrunk and deferred-replace rows. New check 5: the current total (wc -l of present files, excluding deferred-replace rows) against 33,100 plus or minus 10 percent. Every run prints it; it counts as a violation only with `--final`, the end-of-2a gate. Documented in the script header.

## Kept-dead extracts (2b inherits these; deadcode reports them unreachable)

| file | declarations | lines | 2b home |
| --- | --- | --- | --- |
| internal/cli/launch.go | launchRecordWriteSnapshot (4), writeLaunchRecordWithSnapshot (22), rollbackLaunchRecordIfCurrent (28), ensureLauncherExecutable (19), seedRoleStub (29, the role write) | 102 | seat-exec |
| internal/cli/session_reset_support.go | allowInterspersedFlags (29), flagName (4), isBoolFlag (4) | 37 (the whole file, 46 with imports) | shared flag parsing |

| internal/cli/team_launch_launchapi.go | launchapiTeamLaunchBackend (apply, prepare, callPrepare, buildIntentInput), capabilitiesByProvider, reviewerOverrideAllowedFrom, resolveAgentExecutable, wakeInjectorOptions, assertNoForbiddenNewPathArgv, sortedKeys, resolveLaunchapiDecisions, launchapiGateThread, launchapiGateFromHandle, surfaceRequiredActionsAsOperatorGates, recordAppliedLaunchapiDecisions, decisionChoicesToStrings, printLaunchapiPreview | 579 (whole file) | 2b launch over the AMQ launch API |
| internal/cli/global_noc.go | the fleet registry read/write extract (globalNOCRegistryPath, canonicalGlobalNOCControlRoot, ensureGlobalNOCRegistryDirectory, readGlobalNOCRegistry, writeGlobalNOCRegistry, validateGlobalNOCRegistry) and its closure (detectRegisteredGlobalNOC, the registry/launch/run types) | 265 (whole file) | `status --fleet` reading fleet.yaml (plan 16.3) |
| internal/cli/team_launch.go | teamLaunchOptions (fields the launchapi ancestor reads), resolveTeamLaunchAMQEnv, buildTeamPreflights | 89 (whole file) | 2b launch |

team_launch_launchapi.go (lead ruling, commit 2): 2b must give the library a real amq executable path (plan R51, upstream #1159) before this backend can launch from the amq-squad binary. The kept-dead closure also holds helpers in files that other commits shrink: memberResolvedModel, resolveModelForLaunch and nativeModelArg (model_defaults.go), memberEffectiveModel (team.go), agentLaunchPreflight (preflight.go), and the composeBinaryArgs / launchDefaultChildArgsWithTrust family (agent_args.go, agent_defaults.go).

resolveAMQEnvForLaunch, normalizedAgentBinary and stringListFlag (launch.go) are live; other kept code calls them. restore.go's shellQuote and shellCommand are live.

## Commits

### Commit 1: C28 launch driver and C11 hosts (restore, session reset, visibility, wait posture)

- What was dead: `runLaunch` (the legacy launch driver; registry entry gone since 2a-1 cut 4), `runRestore` and the record-replay chain (launchArgsFromRecord, emitCommand, trustModeFromRecord, conversation-restore argv), the `rm` session-reset execution (executeRm, executeRmReportDeclined and preview/confirm/archive helpers), and the launch visibility mapping (applyLaunchVisibility). Every removed function is in deadcode's unreachable list. Nothing reachable was cut.
- Files (non-test lines before -> after / target):
  - launch.go 1,178 -> 194 / 300 (shrunk). The kept-dead seat-exec extracts are listed above. Also cut: the unused env constants, the Symphony init seam, stdin probe, tmux pane seam and observer vars.
  - restore.go 746 -> 60 / 80 (shrunk). shellQuote, shellCommand and generatedSquadCommand stay.
  - session_reset_support.go 663 -> 46 / 46 (shrunk). Only the allowInterspersedFlags extract and its two helpers stay. flagName and isBoolFlag were compacted, with the same behavior: the boolFlag interface is inlined and the empty-name and no-`=` branches fold into one strings.Cut.
  - visibility.go (C11, delete) 107 -> 51. normalizeLaunchVisibility and visibilityPreviewLaunchCommand are still live through team.go's launch preview. The file goes with the C18 commit.
  - wait_posture.go 303 -> 303 / 100 (unchanged). Everything in it is live: amq send/watch and dispatch call it. It shrinks with the C14 amq commit.
  - Cross-cluster dead callers cut: v3_holding.go findMemberRestoreRecord (its callers were the restore path); workstream.go teamWorkstreamExistsOrRestorable.
  - exit.go (keep) 173 -> 171: `commaSeparatedProjectFlagCommands` loses its stale `restore` entry, because that command no longer exists. The map stays empty, so a new list-valued --project must still be registered.
- Behavior change: none to a kept verb. Every cut path was unreachable from the binary.
- Tests retired (subject is dead code; 110 functions):
  - launch_test.go: 59 `runLaunch` / launch-driver tests (TestRunLaunch*, TestApplyConversationRestoreArgs*, TestLaunchArgsFromRecordReplays*, TestExecAMQCoop*, TestExactRootChildCommand*, TestDefaultRunSymphonyInit*, TestValidateManagedTmuxLaunchRejectsNonTTY, TestRunWakeBindingExecPersistsExactLockBeforeTargetExec, TestConfiguredAllowlistOptOutFullRecordReplay, TestReplayRecomposesLauncherGrantFromCurrentPolicy, TestExplicitGrantEqualToLauncherPolicySurvivesPolicyRemoval).
  - restore_test.go: 27 record-replay and `restore` tests (TestLaunchArgsFromRecord*, TestEmitCommand*, TestRestore*, TestMatchesRestoreFilters, TestRunRestoreProjectFlagExpandsHome, TestRefreshRecordToolPolicy*, TestNoBootstrapIsConditionalOnConversation, TestRemoveContiguousSubsequenceFirstMatchOnly) and helper assertConversationReplayAccepted.
  - trust_model_test.go: TestTrustModeFromRecord* (4), TestEmitCommandIncludesModel, TestLaunchArgsFromRecordModelRoundTrips.
  - visibility_test.go (file deleted): 6 TestApplyLaunchVisibility* / TestLaunchVisibility*.
  - terminal_env_test.go (file deleted): 3 TestTerminalInfoFromEnv* (terminalInfoFromEnv, a runLaunch helper).
  - v228_contract_restore_conversation_test.go (file deleted): TestV228ContractRestoreCarriesTheRecordedConversation, TestV228ContractRestoreWithoutConversationIsAFailedRestore.
  - pane_cleanup_rm_wiring_test.go: 4 TestRm* tests of the `rm` execution, plus helper runRmExec (retained_fixture_support_test.go).
  - profiles_test.go: TestEmitCommandRoundTripsTeamProfile, TestEmitCommandDefaultProfileNotEmitted.
  - pathnorm_test.go: TestCommaSeparatedProjectFlagCommandsSurviveVerbatim, which drove the `restore` exemption. TestEveryCommaSeparatedProjectFlagIsExempt stays. It loses its vacuity check, because no list-valued --project exists now; its stale-exemption and missing-exemption checks still run.
- Test-side cleanup (no tests): unused helpers removed with `cutunused.py TESTS=1`, most of them 2a-1 leftovers that its closing fix 2 named. They span real_amq_wake_compatibility_test.go (16 wake helpers), stop_namespace_conflict_test.go (7 exact-down helpers), team_launch_pane_verify_test.go, v228_contract_start_harness_test.go (the start fixture), v228_contract_support_test.go, v3_holding_test.go (selfCleaningTerminator), restore_test.go, retained_fixture_support_test.go and team_test.go. The emptied json_envelopes_helpers_test.go (it was already only `package cli`) is deleted.
- Coverage note for 2b: the kept-dead seat-exec extracts lost their only driver tests with runLaunch: the AMQ floor and preauth paths, TestRunLaunchRejectsAMQBelowSupportedFloorBeforeSideEffects and the TestRunLaunchPreauth* tests. seat-exec owes that coverage when it calls them.
- Manifest: launch.go, restore.go and session_reset_support.go are shrunk; visibility.go, workstream.go, exit.go and v3_holding.go are trimmed; evidence.go and commandevidence/evidence.go are deferred-replace.
- Lines 72,972 -> 70,555; holding 832 -> 786; tests listed 2,027 -> 1,918.

### Commit 2: C18 team launch path, C1 tmux and pane files, C4 preview_flags, team overlay (lead rulings 1 and 4)

- Ruling 4 (commit 2): bare `team` prints the usage, and the launch-command preview goes. The tmux backend, bootstrap_failure.go, the pane files, team_launch.go's driver and the legacy backend are cut. team_launch_launchapi.go keeps only the launchapi Prepare/Apply backend, as a kept-dead 2b ancestor under its target and per its extract field. adoptionseam stays as keep-fate.
- Entry points and roots cut by hand: runTeam's bare form (runTeamSmart, emitTeamCommands, the `team show` preview), `team overlay` (dispatch arm, usage lines, the unknown-subcommand hint), and the teamLaunchBackends registrations. Each init() registered a backend, and deadcode counted the tmux and launchapi backends reachable through those registrations. runTeamLaunch and executeTeamLaunch, plus launchapi's Name/Validate/DryRun/Launch/LaunchWithResult, resolveTeamLaunchBackend, terminalResolvesToTmux, registeredLaunchapiBackend, legacyTeamLaunchBackend, tmuxPlanFromCommands, shellCommandFromArgv and sanitizedIdentityVarNames are gone. Everything else went through cutdead.py and cutunused.py over the cluster's files.
- Kept-dead ancestor, NOT verbatim: launchapiTeamLaunchBackend.launch became `apply`. It runs the same Prepare, subject-digest check, decision resolution, operator gates, adoptionseam.Apply, decision recording and argv assertion, then returns the resolved []launchapi.CommandV1. v2 instead turned them into a tmux plan and ran it, and that tmux step is cut. teamLaunchOptions keeps only the 16 fields the ancestor reads; the tmux, terminal, layout, seeding, external-lead and checkpoint fields are gone.
- Files (non-test lines before -> after / target):
  - team.go 2,549 -> 1,877 / 350 (trimmed; see deferral).
  - team_launch.go 1,031 -> 89 / 250 (shrunk). team_launch_launchapi.go 794 -> 579 / 600 (shrunk). team_launch_bootstrap.go (C12) 151 -> 41 / 50 (shrunk).
  - Deleted (fate delete): team_overlay.go 1,310; preview_flags.go 193; bootstrap_failure.go 402; tmux_exact_id.go 27; wake_inject.go 42; pane_cleanup.go 637, pane_cleanup_rm.go 312, pane_cleanup_manifest.go 138, pane_cleanup_manifest_unix.go 324, pane_cleanup_manifest_other.go 13, pane_cleanup_manifest_rename_darwin.go 9, pane_cleanup_manifest_rename_linux.go 9. The pane-cleanup types survived darwin's unused check only because the windows-only `_other` twin named them; deleting the set builds on all three platforms.
  - Still present, delete-fate, with live callers in later clusters: team_launch_tmux.go 1,096 -> 90 (paneTitleToken: runtime_actions/runtime_identity, C2; tmuxControlModeClients: status, C16; tmuxOutputCommand: runtime_json, C2); wake_lock_compat.go 171 -> 158 (status C16, preflight C12, live_identity C10); pane_adopt.go 78 (status C16, runtime_actions C2); visibility.go 51 -> 8 (the visibility constants runtime_json reads, C2).
  - Cross-cluster dead callers cut (deadcode lists every one): amq_root_authority.go 592 -> 473 (planAMQRosterConfigSync, writeTeamProfileWithAMQRosterSyncUnderLock, amqRosterConfigCandidates: the team-launch roster sync); live_identity.go 323 -> 252 (bindLaunchWakeRecord, verifiedWakeRecordBinding); effort.go 239 -> 112 / 180 (shrunk: the launch-time --effort overrides); runtime_actions.go 639 -> 494 (runFocus, focusTarget; `focus` has been unregistered since 2a-1 cut 11).
  - Moved to their 3.0 homes from team_overlay.go: dedupeSortedStrings -> pathnorm.go (243 -> 258), splitCommaList -> team.go.
  - doctor.go 1,798 -> 1,795: the "member tool profiles" check no longer calls validateMemberOverlayPaths (ruling 1). 2b item: tool policy comes from squad.yaml at launch (replaces validateMemberOverlayPaths).
  - v3_holding.go 786 -> 557. Gone with their last callers: downStatus, processTerminator, signalTerminator, newSignalTerminator and its methods, signalNameOf, downAgentJSON, wakeHealthForEntry, looksActive, the simpleStartCheckpoint block (its only reader, teamLaunchOptions.AfterCheckpoint, is gone), inheritedSession, memberByRole, iterm2OutputCommand, focusITerm2Window, iterm2FocusArgv.
- Behavior changes to kept verbs (named):
  1. Bare `amq-squad team` prints the team usage. It used to print the launch-command preview (or run interactive init when no team existed).
  2. `team overlay` is removed (ruling 1). The doctor "member tool profiles" check keeps its full-workers warning but no longer fails on overlay-path or tool-policy drift.
  3. `team init`, `team rules init` and `team sync` no longer print "deprecated; use amq-squad init instead". 3.0 registers no top-level `init` (2a-1 cut 5), so the notice named a removed verb. This is item (5), stale command names, for team.go. The team usage now reads "Print this usage" for bare `team`.
- Deferral: team.go stays at 1,877 against a target of 350. The rest is the `team init` flow (runTeamInitWithOptions 588 lines, its prompts and custom-role staging, about 400) plus `team rules` (200) and `team sync` (129). All of them are kept verbs, and the 350 figure assumes their rewrite around squad.yaml, a 2b design (manifest reason "rewrite around squad.yaml in 2b"). Lines left: 1,527.
- Tests retired (subject removed: team launch/show/emit, tmux backend, pane cleanup, bootstrap failure, overlay and tool-policy drift, focus/iTerm2, launch-time effort, wake-lock record binding, the deprecation notice); 212 functions:
  - amq_root_authority_test.go: TestPrepareSelectedAMQRootsCreatesExactRootAuthority, TestPrepareSelectedAMQRootsPreservesPreexistingRootRepair, TestWriteTeamProfileWithAMQRosterSyncCoversAddUpdateRemove, TestWriteTeamProfileWithAMQRosterSyncRejectsMalformedConfigBeforeProfileWrite
  - bootstrap_failure_test.go: TestDeadPaneAtShellPromptIsReportedAsBootstrapFailure, TestRunningEnginesAreNotReportedAsBootstrapFailure, TestRunningEngineWithErrorTextIsNotABootstrapFailure, TestInconclusiveProbeReportsNoFailure, TestUninspectablePaneReportsNoFailure, TestGoalDeliveryTimeoutDetailCarriesPaneError, TestPaneErrorDetectionIsAnchoredAndReportsLastError, TestBootstrapFailureNamesNonDestructiveRecovery, TestTmuxBootstrapProbesPairPanesWithPlannedEngines, TestUninspectableProbeReturnsWithoutPayingTheBudget, TestProbeRejectsTmuxFallbackPaneRow, TestIntermediateExecutableWithErrorTextIsNotABootstrapDeath, TestKnownShellsWithErrorTextAreBootstrapDeaths, TestPaneIsAtShellRejectsEnginesAndBlanks; helpers: withFakePanes
  - bootstrap_failure_vanished_598_test.go: TestVanishedPaneIsALaunchFailureNotSilentSuccess, TestUninspectablePanesStayInconclusiveWhenTmuxCannotBeAsked, TestHealthyLaunchNeverConsultsThePaneList; helpers: stubVanishedPaneProbes
  - briefs_live_test.go: TestTeamLaunchDryRunDoesNotCreateBrief
  - doctor_test.go: TestDoctorToolProfilesReportsGeneratedLeanPolicyAndFailsOnDrift
  - effort_test.go: TestApplyLaunchEffortOverridesReplacesNativeArgsWithoutMutatingProfile, TestApplyLaunchEffortOverridesRejectsUnknownProfileRole, TestTeamShowEffortUsesProjectCatalogWithoutPersisting
  - json_envelopes_test.go: TestRunUpDryRunJSONEnvelope, TestRunUpDryRunJSONIncludesOrchestration, TestRunUpDryRunJSONUsesCustomOperator
  - live_identity_test.go: TestStrictDescendantRemainsStrictForPaneCleanup, TestPaneProcessOrDescendantRejectsNonPositivePIDs, TestVerifyAgentPaneLineageRejectsUnrelatedAndAcceptsDescendantPID
  - operator_guard_test.go: TestRoleControlCommandsAlwaysReserveUserWithNoOperator, TestRoleControlCommandsRefuseOperatorTarget
  - output_test.go: TestQuietDoesNotSuppressWarningsOrErrors, TestQuietDoesNotSuppressDryRunStdout
  - pane_cleanup_dead_pane_689_test.go: TestPreparePaneCleanupUnconfirmedIdentityStillReportsGonePane, TestClosePreparedDeadPanePreservesWhenPaneComesAlive
  - pane_cleanup_gone_direction_test.go: TestRevalidationGoneBecomesAlreadyGoneWithoutClosing, TestRevalidationUnavailableIsNotAlreadyGone; helpers: countingCleanupDeps
  - pane_cleanup_manifest_unix_test.go: TestPaneCleanupManifestPrepareFinalizeDurableEvidence, TestPaneCleanupManifestReleasedRootFDHasNoLateWrapperClose, TestPaneCleanupManifestRejectsHardlinkedPreparedEvidence, TestPaneCleanupManifestDetectsPublishedTargetSwap, TestPaneCleanupManifestDetectsProjectRootSwap, TestPaneCleanupManifestRejectsSymlinkedAncestors, TestPaneCleanupManifestRejectsTargetSymlinkAndCollision, TestPaneCleanupManifestDetectsTempSwap, TestPaneCleanupManifestSyncAmbiguityRetainsPrepared, TestPaneCleanupManifestFileSyncAndRenameFailuresFailClosed, TestPaneCleanupManifestConcurrentOperationCollision, TestPaneCleanupManifestRejectsPathEscape; helpers: manifestFixture
  - pane_cleanup_rm_wiring_test.go: TestRmKeepPanesStillStopsFreshlyAttestedAgent, TestRmStopAgentsRefusesToSignalReusedSameBinaryPID
  - pane_cleanup_test.go: TestPaneCleanupExactPositiveClosesOnce, TestPaneCleanupSymlinkEqualAndLexicalPrefixNegative, TestPaneCleanupRootAndBaseRootCanonicalExisting, TestPaneCleanupLegacyBaseRootShapeRequiresRootAndSessionBinding, TestPaneCleanupCWDAndInspectionFailuresFailClosed, TestPaneCleanupCanonicalDirUnavailableFailsClosed, TestPaneCleanupRecordFieldMismatchesFailClosed, TestPaneCleanupResumeAbsoluteRecordedBinaryMatchesBareConfigured, TestPaneCleanupBinaryIdentityNegativeMatrix, TestPaneCleanupInspectedRuntimeFieldMismatchesFailClosed, TestPaneCleanupExternalLegacyAndTerminalInconsistency, TestPaneCleanupAgentAttestationAndAncestryFailClosed, TestPaneCleanupRevalidationAndCloseOutcomes, TestPaneCleanupPreparationNeverGatesAgentLifecycle, TestPaneCleanupNotRequestedAndInitiallyGone; helpers: paneCleanupFixture, newPaneCleanupFixture, cleanupDeps
  - parent_amq_floor_test.go: TestTeamLaunchRejectsPreFloorAMQBeforeParentMutations, TestTeamLaunchDryRunReportsPreFloorAMQAndRendersPreviewWithoutMutations, TestExternalLeadLateMemberPreflightRejectsBeforePaneStampOrLaunchRecord; helpers: DryRun, useFloorRenderingBackend
  - preflight_test.go: TestWakeHealthWithSpacesInRoot, TestWakeHealthForRelativeRootStillReportsLive, TestWakeHealthForEntry
  - profiles_test.go: TestNamedProfileConflictBlocksDirectRuntimeCommands, TestEmittedCommandsCarryDefaultProfileImplicitly
  - runtime_actions_test.go: TestFocusITerm2DoesNotRequireTmuxPaneList
  - scoped_flags_test.go: TestFocusAcceptsScopedShortFlagAliases, TestScopedShortFlagAliasesRegistered
  - stop_test.go: TestNewSignalTerminatorMapsForceToSIGKILL
  - team_launch_bootstrap_gate_test.go: TestBootstrapDeathFailsLaunchOnEveryTmuxTarget, TestHealthyLaunchStillSucceedsOnEveryTmuxTarget; helpers: install, bootstrapGatePlan
  - team_launch_iterm2_test.go: TestITerm2FocusArgvShape
  - team_launch_launchapi_test.go: TestResolveTeamLaunchBackendDefaultsToLaunchapiOnTmux, TestResolveTeamLaunchBackendLegacyRequiresExplicitOptIn, TestLaunchapiBackendDualRunParity, TestLaunchapiBackendLaunchRejectsStaleSubjectDigest, TestLaunchapiBackendLaunchAcceptsMatchingSubjectDigest, TestLaunchapiBackendComposedCommandStripsIdentityEnvByDiff, TestLaunchapiBackendComposedCommandOmitsEnvPrefixWhenNothingStripped
  - team_launch_pane_verify_test.go: TestVerifyPaneProcessLaunchedRefusesEveryUnprovenPane, TestVerifyPaneProcessLaunchedAcceptsTheRequestedLivePane, TestVerifyPaneProcessLaunchedRefusesAnEmptyIdentity, TestDeliveredCommandCarriesTheLauncherPATH, TestVerifyRefusesWhenLivenessAnswersAboutADifferentPane; helpers: stubExactPaneInspection
  - team_launch_test.go: TestBuildTmuxLaunchPlanUsesCatalogOrderAndLaunchCommands, TestRunTeamLaunchHelpListsNewDXFlags, TestRegisteredTeamLaunchTerminalsIncludesTmux, TestCommandProfileArgOmitsDefaultAndQuotesNamedProfile, TestRunTeamLaunchRejectsUnsupportedTerminalWithRegistry, TestTmuxDryRunLinesShowPaneFlow, TestTmuxDryRunLinesCanTargetCurrentWindow, TestRunTeamLaunchDryRunDefaultsToCurrentWindow, TestRunTeamLaunchDryRunUsesExplicitSharedWorkstream, TestRunTeamLaunchDryRunUsesBinaryArgs, TestRunTeamLaunchFreshRejectsExistingWorkstream, TestRunTeamLaunchDryRunUsesSharedWorkstreamAcrossMemberCWDs, TestRunTeamLaunchDryRunNewSessionDoesNotAutoAttach, TestRunTmuxLaunchPlanCurrentWindowRequiresTmux, TestTmuxLayoutMapping, TestWarnTmuxControlModeClientsIsOneLineByDefault, TestWarnTmuxControlModeClientsVerboseExpandsDetails, TestRunTeamLaunchDryRunMixedSessionFiltersOutCrossSessionMembers, TestRunTeamLaunchDryRunMixedSessionIncludesUnpinnedMembers, TestRunTeamLaunchAllCrossSessionErrors, TestDefaultTmuxSessionNameSanitizesProject, TestNamedProfileThreeSameCWDLaunchCommandsCarryExactRoleRoots
  - team_launch_tmux_result_test.go: TestRunTmuxCurrentWindowResultFailureSendsNoAgentCommands, TestRunTmuxNewSessionWithResultCapturesExactFirstPaneBeforeSend, TestRunTmuxNewSessionResumeStageReusesExistingLeadSession, TestTmuxLaunchResultRejectsNameLikePaneAndWindowTargets, TestCompleteTeamLaunchResultFailsClosed, TestRunTmuxCurrentWindowLeadMainAppliesMainVertical, TestRunTmuxCurrentWindowLeadMainSkipsSwapWhenLeadAlreadyMain, TestRunTmuxCurrentWindowLeadMainFailureRestoresWindowOptions, TestRunTmuxCurrentWindowLeadMainFailureRestoresPreexistingWidth, TestRunTmuxCurrentWindowLeadMainSkipsWidthWhenSnapshotFails, TestRunTmuxCurrentWindowWithoutLeadMainKeepsLegacyLayout; helpers: stubTmuxResultCommands
  - team_launch_tmux_session_test.go: TestDefaultTmuxBackendStillEmitsSplitPanePlan
  - team_launch_tmux_title_test.go: TestTmuxDryRunLines_StampsDeterministicPaneTitles
  - team_launch_tmux_window_test.go: TestTmuxBackendAcceptsNewWindowTarget, TestTmuxDryRunNewWindowOneWindowPerAgent, TestTmuxDryRunCurrentWindowSplitsPaneForEveryAgent, TestTmuxWindowsHostSessionReusesExistingDetachedSession, TestTmuxWindowsHostSessionRejectsExistingDetachedSessionForFreshLaunch, TestRunTmuxWindowsPlanAddsWindowsToExistingDetachedSession, TestRunTmuxWindowsPlanResultFailureSendsNoAgentCommands, TestTmuxWindowName
  - team_overlay_test.go: TestOverlayInitWiresClaudeMember, TestOverlayInitIdempotent, TestOverlayInitSerializesProfileReadModifyWrite, TestOverlayInitCodexMemberRefused, TestOverlayInitWorkersSkipsLeadAndCodex, TestOverlayInitWorkersOnFlatTeamTargetsAllClaude, TestOverlayInitForeignSettingsRequiresForce, TestOverlayInitNoClobberExistingFile, TestGeneratedToolPoliciesFailClosedWithoutPartialWrites, TestGeneratedToolPoliciesRejectUnsupportedCodexCapabilitySyntax, TestDiscoverCodexCapabilitiesParsesTOMLDottedTableKeys, TestGeneratedToolPolicyMaterializesWithCodexMCPEnvSubtable, TestGeneratedToolPolicyMidApplyFailureRetainsExactRecoveryEvidence, TestOverlayInitDryRunWritesNothing, TestOverlayInitRequiresExactlyOneTarget, TestWireSettingsArg, TestMemberSettingsRefsSkipsInlineJSON, TestUpDryRunRejectsDanglingSettings, TestGeneratedToolPoliciesDiscoverInheritancePreserveAllowAndRevokeConflicts, TestGeneratedToolPolicyDriftBlocksLaunchPlan, TestUpDryRunFailsOnMissingOverlayFile
  - team_test.go: TestEmitTeamCommandsRejectsPersistedSandboxedBypass, TestResolveTeamTrustModeEmptyProfileUsesManagedDefault, TestEmitTeamCommandsExistingProfileWithoutTrustUsesApproveForMe, TestEmitTeamCommandsRejectsUnknownModelRoleKey, TestEmitTeamCommandShape, TestEmitTeamCommandResolvesWakeModeFromUnderlyingMemberBinary, TestEmitTeamCommandIncludesCustomLauncher, TestEmitTeamCommandSandboxedCodexOmitsBypass, TestEmitTeamCommandTrustedCodexIncludesBypass, TestEmitTeamCommandApproveForMeCodexPreset, TestEmitTeamCommandIncludesModelOverride, TestEmitTeamCommandClaudeModelPlacement, TestEmitTeamCommandAddsConfiguredBinaryArgs, TestEmitTeamCommandAddsSymphonyForCodexOnly, TestValidateTeamSymphonyRejectsSharedCodexCWD, TestEmitTeamCommandTransportsPerMemberArgsOnce, TestEmitTeamCommandComposesRecognizedSingletonOnce, TestEmitTeamCommandPerMemberArgsOnly, TestEmitTeamCommandAddsWakeInject, TestEmitTeamCommandAddsZeroInputWakeMode, TestEmitTeamCommandAddsNoGitignore, TestEmitTeamCommandQuotesPathsWithSpaces, TestEmitTeamCommandUsesBinaryPath, TestEmitTeamCommandNoBootstrap, TestRunTeamShowUsesDefaultSharedWorkstream, TestRunTeamShowUsesStoredSharedWorkstream, TestRunTeamShowMergesStoredAndRunBinaryArgs, TestRunTeamShowRejectsEmptyExplicitSession, TestRunTeamShowFreshRejectsExistingWorkstream, TestRunTeamShowFreshAllowsNewWorkstream, TestRunTeamInitStoresSingleMemberSharedWorkstream, TestTeamLaunchDryRunSkipsRegisteredExternalLead, TestTeamLaunchAutoRegistersCurrentPaneExternalLead, TestTeamLaunchDoesNotAutoRegisterProjectLeadWithoutAMRoot, TestSimpleStartCommandPinsCanonicalInputsAndExactInstruction
  - tmux_target_env_test.go: TestWithTmuxTargetEnvPrefixesExportedTarget, TestWithTmuxTargetEnvEmptyTargetUnchanged, TestWithTmuxTargetEnvQuotesValue
  - tool_policy_sources_test.go: TestClaudeMemberWithGeneratedPolicyLaunchesWithProjectLocalSettings, TestToolPolicySourcesAreByteIdenticalAcrossRegeneration, TestLegacyRelativeToolPolicySourceIsNotDriftFromAnyWorkingDirectory, TestGenuinelyChangedCapabilitySourceSetStillFailsClosed, TestReadinessAndSpawnShareOneToolPolicyPredicate, TestRecordOnlyScopeOmitsExactlyTheMaterializationClause, TestMixedOriginRecordingsDoNotReadAsDrift, TestRecordedToolPolicySourcesAreNotSymlinkResolved; helpers: deriveToolPolicyPlanForTest
  - v2310_legacy_still_reachable_test.go: TestV2310LegacyStillReachable
  - v3_holding_test.go: TestSimpleStartCheckpointWrapsSentinel; helpers: LaunchWithResult, useFakeBackend
  - wake_inject_test.go: TestResolveWakeInjectModeForBinary
  - wake_lock_compat_test.go: TestVerifiedWakeRecordBindingUsesAuthoritativeCheckForStateBoundLock, TestVerifiedWakeRecordBindingRejectsUnverifiedStateBoundLock
- Tests restored (the sweep removed them for one deleted reference; their subject is kept):
  - team_test.go TestRunTeamInitStoresSingleMemberSharedWorkstream: the init assertion (init must not pin the workstream) stays; the `team show` inference block is dropped.
  - operator_guard_test.go TestRoleControlCommandsRefuseOperatorTarget and profiles_test.go TestNamedProfileConflictBlocksDirectRuntimeCommands: the `dispatch` case stays; the focus/send rows are dropped. TestRoleControlCommandsAlwaysReserveUserWithNoOperator is retired; it drove only runFocus.
  - scoped_flags_test.go TestScopedShortFlagAliasesRegistered: the focus/open/send rows are dropped.
  - output_test.go TestQuietDoesNotSuppressDryRunStdout and TestQuietDoesNotSuppressWarningsOrErrors: the driver changes from `team show` to `roles` (stdout data) and `team sync` with no rules file (a returned error). The --quiet contract is unchanged.
  - team_launch_launchapi_test.go TestLaunchapiBackendLaunchRejectsStaleSubjectDigest and ...AcceptsMatchingSubjectDigest: they now call `apply`.
  - init_test.go TestDeprecatedCreateVerbsRedirectToInit -> TestTeamCreateVerbsWriteWithoutStaleRedirect: the writes are still asserted, and the test now pins that no stale `amq-squad init` redirect is printed. TestTeamSubcommandHelpDispatch drops its overlay rows. team_usage_registry_test.go adds "overlay" to the removed set.
  - testmain_test.go: TestMain loses its stub of the deleted stampCapturedLaunchPane. The sweep tool now refuses to delete TestMain. It now finds the enclosing declaration from the AST: commit 1's text-based finder mis-ranged on raw strings with a "}" at column 0, and the gate caught no damage from that run.
- Retired whole-file as vacuous: team_launch_pane_delivery_test.go (TestEveryPaneCommandDeliveryIsVerified; with tmux launch gone no deliverPaneCommand site remains). Emptied test files deleted: tmux_target_env, bootstrap_failure, bootstrap_failure_vanished_598, tool_policy_sources, team_launch_iterm2, team_launch_tmux_result, team_launch_tmux_window, team_launch_tmux_session, team_launch_bootstrap_gate, pane_cleanup_rm_wiring, pane_cleanup_gone_direction, pane_cleanup_dead_pane_689, pane_cleanup_down_wiring, pane_cleanup, pane_cleanup_manifest_unix, pane_lifecycle, briefs_live, wake_inject, v2310_legacy_still_reachable, team_overlay, stop (all `_test.go`). The 23 unused helpers left behind were removed with them.
- Coverage debit (2a-1) settled here: the two tmux-result tests (runTmuxLaunchPlanWithResult, runTmuxWindowsPlanWithResult) are retired with their subject, since the tmux backend is cut. The pane-cleanup-through-stop tests are retired with pane_cleanup*.go. The exact-named-profile `down` scope tests: their wake_lock_compat and namespace_conflict subjects remain until C10/C7 and are settled there.
- Lines 70,555 -> 63,459; holding 786 -> 557; tests listed 1,918 -> 1,714.

### Commit 3: C19 dispatch, queue-then-send; pane nudges and runtime identity checks dropped (C10 live_identity.go deleted)

- 3.0 shape (manifest extract "queue-then-AMQ-send; drop dispatchNudgePrompt"): dispatch sends the durable AMQ message, and the task claim when one is requested, then stops. The child picks the message up through AMQ, by its own wake or its next drain. dispatch never types into a terminal pane.
- Cut by hand: the `--force` and `--no-wake` flags (both only steered the pane nudge); the wake-inject-mode refusal; the wake-live branch; the pane nudge and its outcome reporting; both verifyRuntimeActionByHandle calls ("dispatch authorizer" and "dispatch wake"). Those calls proved a launch record's PID and pane were live, and in 3.0 AMQ owns liveness (manifest: liveidentity "AMQ owns liveness"). The dispatchWakePane and dispatchRecipientWakeLive seam vars go too; they held the functions deadcode would otherwise have kept alive. The rest went through cutdead.py and cutunused.py.
- Files (non-test lines before -> after / target):
  - dispatch.go 853 -> 449 / 450 (shrunk). The usage text describes the queue-only flow, and two comments were condensed.
  - live_identity.go (C10, delete) 252 -> deleted. Its last callers were dispatch's two verify calls.
  - runtime_actions.go (C2, delete) 494 -> 125. resolveControlTarget, sendPromptToPane, paneBusyForSend, memberRuntime, the member action lists and the send/focus remnants are gone. What is left: sameResolvedDir (doctor, status, namespace_conflict, runtime_identity), readPromptBody and stdinIsInteractive (dispatch, operator), paneIDForTarget (pane_adopt), paneTitledForDifferentAgent (runtime_identity). The file goes with the status commit.
  - runtime_json.go 454 -> 442 (memberActions, policyAwareMemberActions). task_completion_evidence.go 386 -> 381 (completionDONEOnly, which dead dispatch code called). v3_holding.go 557 -> 547 (wantsHelp: its callers were the team init/sync deprecation notices, cut in commit 2).
  - init.go 284 -> 262: runInit is dead (the `init` verb has been unregistered since 2a-1 cut 5). The rules pointer-stub digest flow extracts stay kept-dead: computeInitPlan, computeInitDigest, applyInitPlan.
  - command_registry.go: the dispatch summary changes from "Queue a durable task for a child and wake it to drain" to "Queue a durable task for a child" (item 5).
- Behavior changes to a kept verb (named):
  1. `dispatch` no longer nudges the recipient's pane. Its JSON status is always "queued"; the queued_and_nudged, queued_nudge_*, queued_wake_delivered, queued_zero_input and queued_wake_refused statuses are gone.
  2. `dispatch --force` and `--no-wake` are removed; passing either is now an unknown-flag usage error.
  3. `dispatch` no longer refuses when the dispatcher's managed launch record is not live (the "dispatch authorizer" runtime check).
- Tests retired (subject removed: pane nudge, wake-first routing, zero-input mode, runtime identity, member action lists, the `init` verb; 2 helpers):
  - body_warning_test.go: TestRunSendAndDispatchWarnOnlyForSuspiciousInlineBody, TestRunDispatchSuspiciousInlineBodyKeepsJSONStdoutPure
  - dispatch_test.go: TestDispatchWaitPostureGuardsOwnedSendAndPreservesNoWait, TestDispatchNudgePromptCarriesNoBody, TestRunDispatchPrintsSessionAwareSummary, TestRunDispatchJSONEnvelope, TestRunDispatchJSONEnvelopeReportsSubmitUnconfirmed, TestRunDispatchJSONEnvelopeReportsSubmitQueued, TestRunDispatchReportsBracketedPastePreEnterFailures, TestClassifyNudgeResult, TestRunDispatchWorktreeMemberWithoutLaunchRecordNudgesCanonicalSendRoot, TestDefaultDispatchWakePaneUsesStatusRecordForWorktreeMember, TestRunDispatchWakeLiveSkipsPaneInjection, TestRunDispatchNotWakeLiveUsesLastResortPane, TestRunDispatchNoneModeSkipsLastResortPane, TestRunDispatchWakeLiveNoneModeReportsNoticeNotDrain, TestRunDispatchForceCannotOverrideNoneMode, TestRunDispatchForceOverridesWakeFirst, TestRunDispatchNoWakeSkipsEverything, TestRunDispatchSendsDurablyThenNudges, TestRunDispatchNoWakeSkipsNudge, TestRunDispatchCreateTaskClaimsBeforeMessageWithoutTaskDeliveryState, TestRunDispatchTaskRejectsMismatchedAssigneeBeforeSend, TestRunDispatchRejectsReviewActorImplementAndLifecycleBeforeSideEffects, TestRunDispatchTaskRejectsBlockedAndTerminalStatusesBeforeSend, TestRunDispatchTaskJSONEnvelope, TestRunDispatchWakeFailureStillSucceeds, TestDispatchMismatchedSenderSetsFromInTask, TestRunDispatchWarnsMismatchedSender, TestRunDispatchNoWarnWhenSenderMatchesLead, TestRunDispatchAnswerDefaultsToDrainedWait, TestRunDispatchAnswerWaitCanBeDisabled, TestRunDispatchAnswerWaitTimeoutIsQueuedUnconfirmed, TestRunDispatchAnswerWaitTimeoutHumanOutputWarnsDoNotResend; helpers: withDispatchWakeSeam, withDispatchWakeLiveSeam
  - dogfood_invariants_test.go: TestInvariantRuntimeActionsBlockChildMutationsInProjectLeadMode
  - init_test.go: TestInitPreviewIsZeroWrite, TestInitApplyRejectsStaleDigest, TestInitRerunOnExistingProfileIsNoOp
  - launch_identity_stamp_test.go: TestIncompleteRecordRefusalIsNotAMismatch
  - live_identity_test.go: TestValidateLiveIdentityTerminalProjectionPreservesNativeTarget, TestValidateLiveIdentityTerminalProjectionRejectsTmuxNativeContradiction, TestLegacyPreparedIdentityFieldsAreOpaque, TestReadManagedLiveLaunchTypesMissingRosterHandleAsUnmanaged
  - runtime_actions_test.go: TestResolveControlTargetSymlinkedCWD, TestResolveControlTargetDifferentlyCasedCWDOnCaseInsensitiveFilesystem, TestCompareCWD, TestResolveControlTargetDynamicTeamNoPinnedCWD, TestResolveControlTargetExactRecordedPane, TestResolveControlTargetDirectInspectUnderCCScanMiss, TestResolveControlTargetNoRecordedIDNilScan, TestResolveControlTargetRejectsReusedPaneTitledForOther, TestResolveControlTargetFallbackReturnsPaneID, TestMemberActions, TestResolveControlTargetRefusesForeignProfileRecord, TestITerm2PaneInjectionVerbsReturnUnsupportedReason, TestRunSendWorktreeMemberUsesCanonicalTeamRootRecord, TestDispatchWakePaneSkipsITerm2Nudge, TestErrTmuxAccessDenied, TestSendRequiresRole
  - team_member_control_continue_test.go: TestTmuxControlContinueStatusActionRequiresExactVerifiedIdentityAndUniqueClient; helpers: controlContinueFixture
  Of the dispatch_test.go names above, 16 were restored (below); the retired ones are TestDispatchNudgePromptCarriesNoBody, TestRunDispatchJSONEnvelopeReportsSubmitUnconfirmed, ...SubmitQueued, TestRunDispatchReportsBracketedPastePreEnterFailures, TestClassifyNudgeResult, TestRunDispatchWorktreeMemberWithoutLaunchRecordNudgesCanonicalSendRoot, TestDefaultDispatchWakePaneUsesStatusRecordForWorktreeMember, TestRunDispatchWakeLiveSkipsPaneInjection, TestRunDispatchNotWakeLiveUsesLastResortPane, TestRunDispatchNoneModeSkipsLastResortPane, TestRunDispatchWakeLiveNoneModeReportsNoticeNotDrain, TestRunDispatchForceCannotOverrideNoneMode, TestRunDispatchForceOverridesWakeFirst, TestRunDispatchNoWakeSkipsEverything, TestRunDispatchNoWakeSkipsNudge, TestRunDispatchWakeFailureStillSucceeds.
  The init_test.go tests (TestInitPreviewIsZeroWrite, TestInitApplyRejectsStaleDigest, TestInitRerunOnExistingProfileIsNoOp) drove the kept-dead init digest extracts through runInit. Coverage of computeInitPlan, computeInitDigest and applyInitPlan is a 2b debit, owed when a verb calls them again.
- Tests restored (the sweep removed them only for the deleted wake seams or flags; their subject is kept): dispatch_test.go TestDispatchWaitPostureGuardsOwnedSendAndPreservesNoWait, TestRunDispatchPrintsSessionAwareSummary, TestRunDispatchJSONEnvelope (status now "queued"), TestRunDispatchAnswerDefaultsToDrainedWait, TestRunDispatchAnswerWaitCanBeDisabled, TestRunDispatchAnswerWaitTimeoutIsQueuedUnconfirmed, TestRunDispatchAnswerWaitTimeoutHumanOutputWarnsDoNotResend, TestRunDispatchCreateTaskClaimsBeforeMessageWithoutTaskDeliveryState, TestRunDispatchTaskRejectsMismatchedAssigneeBeforeSend, TestRunDispatchRejectsReviewActorImplementAndLifecycleBeforeSideEffects, TestRunDispatchTaskRejectsBlockedAndTerminalStatusesBeforeSend, TestRunDispatchTaskJSONEnvelope, TestDispatchMismatchedSenderSetsFromInTask, TestRunDispatchWarnsMismatchedSender, TestRunDispatchNoWarnWhenSenderMatchesLead, and TestRunDispatchSendsDurablyThenNudges -> TestRunDispatchSendsDurably (it now asserts that no pane is nudged); body_warning_test.go TestRunSendAndDispatchWarnOnlyForSuspiciousInlineBody -> TestRunDispatchWarnsOnlyForSuspiciousInlineBody (the dispatch row; `send` is gone) and TestRunDispatchSuspiciousInlineBodyKeepsJSONStdoutPure. The mechanical edits were: drop the seam lines, drop `--no-wake` arguments, drop nudge-count assertions, change "queued_and_nudged" to "queued", and drop the "nudge drain instead" substring (that wording left the timeout message). Other tests that passed `--no-wake` to dispatch lose only that argument: profiles_test.go (3), amq_update_suppression_integration_test.go, real_amq_compatibility_test.go. The `--no-wake` arguments to `amq coop exec` are AMQ's own flag and are unchanged.
- Lines 63,459 -> 62,385; holding 557 -> 547; tests listed 1,714 -> 1,672.

### Commit 4: C16 status, C2 runtime control, C10 liveness inputs, C3 board, the rest of C1; packages runtimeaction, runtimecontrol, liveidentity and tmuxpane deleted

Interruption: an API rate limit stopped this run in the middle of the commit, during the caller fixes across context_resolution, preflight, global_noc, operator, doctor, execution_mode and namespace_conflict. On resume the partial state built and vetted cleanly. The work was finished on top of it and the full gate was run before committing.

- 3.0 shape:
  - `status` is the one-workstream contract producer (manifest C16: "contract producer; cut pane/runtime fields"). The pane and wake inputs to liveness are cut (manifest C10: "liveness vocabulary kept, pane/wake inputs cut").
  - The multi-session board goes to amq-noc (C3).
  - Runtime control and the terminal capability registry go (C2, R6).
- Cut by hand:
  - The bare `amq-squad` board default and `status` without `--session` routing to the board. status_board.go is deleted; defaultBaseRootName moves to amq_env.go, its only remaining user.
  - The status pane seams: statusPaneLister, statusPaneInspector, statusPaneTTYInspector, statusLocalInputDetector, statusTerminalContext, paneCloser.
  - The visibility-invariant and fault-remedy projection (annotateVisibilityInvariants ... visibilityRepairActions). The fault remedies named the removed `resume` and relaunch commands.
  - The local-input blocker feed: statusLocalInput and operator_attention's collectLocalInputAttention. Its inspect command named the removed `focus`.
  - Pane replacement detection: liveReplacementPane and the batch resolver.
  - The wake-lock input: readWakeLock, wakeLiveDetail, and wakeWriterDead in the presence zombie guard.
  - The native goal blocker count: blockedNativeGoalsInSnapshot, a goal-binding leftover that can no longer be written.
  - global_status.go, whose types lost their last user.
  - runtime_json.go and visibility.go.
  - The status JSON fields listed under the behavior changes.
  - deadcode then made the rest of the files below unreachable, and cutdead.py and cutunused.py removed them.
- Moved or rewritten (named):
  - The liveness classifier is classifyAgentLivenessFromDisk. Its inputs are the launch-record PID identity and presence; there are no wake or pane inputs and no replacement-pane fallback. The agentLivenessVerdict constants stay as the closed vocabulary.
  - classifyLaunchRuntimeIdentity (runtime_identity.go) is now PID-only. It keeps the alive, recorded binary/launcher, birth-time reuse and recorded-tty checks, and drops PaneLive and PaneTitleMatch and the pane-title and pane-tty corroboration. The classifyLaunchPIDRuntimeIdentity adapter folds into it. The file is delete-fate and still present, see deferrals.
  - Context resolution decides launch-record wins by PID identity only. The current-TMUX_PANE tie-break for the handle is cut.
  - preflight's presenceWriterIsKnownDead now demotes a fresh presence when the recorded launch writer is known dead. v2 also required a dead wake.lock writer; the wake input is cut.
- Files (non-test lines before -> after / target):
  - status.go 1,760 -> 955 / 900 (trimmed; 55 over, see deferrals)
  - liveness.go 641 -> 523 / 250 (trimmed)
  - context_resolution.go 1,020 -> 989 / 551 (trimmed)
  - preflight.go (C12) 681 -> 162 / 100 (trimmed; the launch preflight checks were dead with the launch driver)
  - global_noc.go 601 -> 265 / 150 (trimmed; kept-dead fleet registry ancestor, see table)
  - global_status.go 720 -> deleted (fate shrink, target 300; no user left)
  - cli.go (keep) 279 -> 192
  - operator_attention.go 1,210 -> 1,098; operator.go 1,773 -> 1,758; operator_self_approve.go 669 -> 583 (dead self-approval question lookups); doctor.go 1,795 -> 1,759; bootstrap.go 679 -> 618; verify_action.go 1,010 -> 952 (executeVerifyAction, dead, which h-c13's commit also cuts); execution_mode.go 651 -> 644; namespace_conflict.go 514 -> 435; version_alignment.go 318 -> 303
  - Deleted (fate delete): status_board.go 738, runtime_json.go 442, visibility.go 8, team_launch_tmux.go 90, pane_adopt.go 78, wake_lock_compat.go 158; packages internal/runtimeaction (1 file, 154), internal/runtimecontrol (3 files, 551), internal/liveidentity (1 file, 257), internal/tmuxpane (4 files, 1,686)
  - Package files trimmed by dead code: state/coordination.go 629 -> 566, state/flow.go 136 -> 26 / 87 (shrunk), state/snapshot.go 398 -> 382 (state.Build, a one-line wrapper used only by tests), attention/dedup.go 119 -> 42, attention/event.go 50 -> 39 (shrunk), notifier/sink.go 41 -> 22, procinfo/procinfo.go 143 -> 128, procinfo/native_darwin.go 82 -> 64
  - runtime_identity.go 164 -> 93; runtime_actions.go 125 -> 96 (both delete-fate, still present)
  - v3_holding.go 547 -> 459: currentPaneIdentity, configuredTeamProfiles, and the exact tmux control-client readers are gone.
  - Cross-cluster dead callers cut: verify_action.go executeVerifyAction; operator_self_approve.go humanApprovalQuestion, latestGateQuestion, latestStrictGateQuestion, selfApprovalGateSnapshot. All of them read state.Build snapshots that only the deleted board or global status built.
- Behavior changes to kept verbs (named):
  1. Bare `amq-squad` prints the root usage. It used to show the multi-session board, or setup guidance naming the unregistered `init`.
  2. `status` without `--session` shows the team's workstream, resolved like every other verb; it no longer shows the board. The usage and catalog summary are rewritten.
  3. `status --json` drops these fields:
     - the envelope's operator_delivery, terminal_context, execution, versions, namespace_conflict, topology and actions;
     - each record's tmux, terminal, operator_visible, adoption_mode, role_boundary, launcher_pane_id, agent_pane_id, managed_target, current_pane_conflict, visibility_problem, visibility_repair_actions, external, orchestrator_registration, wake_auto_drain, local_input, actions, live_identity and live_identity_mode;
     - the signals' wake_pid and wake_alive.
     The human table drops the operator_delivery comment line.
  4. Member liveness ignores wake locks and tmux panes. "wake-live" is no longer produced, and a recorded PID that is dead is no longer rescued by a live pane.
  5. The presence zombie guard needs only a dead launch writer (see "Moved or rewritten").
  6. Context resolution no longer uses TMUX_PANE to pick the handle among several live launch records.
  7. `operator` attention no longer emits local_input_blocked items, and the operator poll's open_blockers is always 0.
  8. The root usage examples name only registered verbs. They used to show setup, init, new session, start, plan, down and `amq route`. The `down` exit-code note is gone.
- Kept-dead closure note: bootstrap.go's kept-dead buildBootstrapPrompt extract still pulls operatorDeliveryForTeamAtRoot (operator_delivery.go, delete-fate C17) and bootstrapack (internal/launch/record.go). Both are settled in the C12/C17 commits.
- Deferrals in this commit:
  - status.go is 55 lines over its 900 target. liveness.go is 273 over 250. Most of liveness is the launchapi session Inspect floor (gh#737/#766), which is AMQ-owned liveness and 3.0 keeps it; the rest is long v2 comments to condense. Both are revisited in the C16 follow-up with comment compaction only.
  - runtime_identity.go (93) and runtime_actions.go (96) are delete-fate. Their remaining functions (the PID identity, sameResolvedDir, readPromptBody, stdinIsInteractive, paneIDForTarget remnants) need 3.0 homes. They move in a later commit.
- Tests retired: their subject was removed (the board, the pane/terminal/visibility/runtime-action fields, wake locks, the launch preflight, global status, the NOC launch lifecycle, version alignment in status, fault remedies, pane adoption, the replacement pane, the goal blocker count, tmux helpers). The list below is every function the sweep removed. The restored ones that follow were re-added.
  - amq_update_suppression_integration_test.go: TestAMQNoisyFakeNativeJSONStartsAtByteZero
  - authorization_envelope_test.go: TestAmbiguousRawGateFrontmatterBlocksVerifierAnswerAndAttention, TestAuthorityDuplicateVariantsFailClosedAcrossSurfaces, TestTypedGateAttentionParityAcrossOperatorModes, TestTypedGateNewerExactBoundAnswerReplacesInvalidAnswerBarrier, TestTypedRawEnvelopeWhitespaceCannotAuthorize
  - bootstrap_test.go: TestBootstrapExpectationLifecycleAndRosterEligibility, TestAppendGeneratedBootstrapPromptTerminatesOptionsOnce, TestBootstrapCurrentTeamFallsBackToRoleWhenHandleMissing, TestBootstrapCurrentTeamKeepsLegacyRoleSessions, TestBootstrapCurrentTeamCrossSessionRouteUsesFromSession, TestBootstrapCurrentTeamUsesExplicitSharedWorkstreamEvenWhenNameMatchesRole, TestBootstrapCurrentTeamDoesNotGuessCrossProjectRouteWithoutProjectIdentity
  - cli_test.go: TestProjectHasFootprintDefaultProfile, TestProjectHasFootprintNamedProfilesOnly, TestProjectHasFootprintSessionsOnly, TestProjectHasFootprintEmptyProject
  - context_resolution_test.go: TestContextRejectsSameBinaryPIDReuseWithSameOrUnknownTTY, TestContextRejectsReusedPaneIDForCurrentAndExternalRecords, TestResolveCanonicalContextSharedTupleDoesNotRequireHandle, TestOrdinaryCommandsDoNotSpliceConflictingTuples, TestResolveCanonicalContextPrecedenceMatrix, TestExplicitSessionSwitchCarriesProfileButNotOldTupleIdentity, TestMatchingLowerTupleContributesHandleAndExactRoot, TestResolveCanonicalContextAmbiguousLaunchesReportEveryProvenance, TestResolveCanonicalContextSkipSessionResolutionAvoidsRootAmbiguity, TestStoppedAndPIDReusedLaunchRecordsDoNotWinContext, TestLegacyLaunchRecordsWithoutStoppedAtUseLivenessProbe, TestExplicitRootOverridesMalformedInjectedIdentityWithWarning, TestExplicitExternalRootWinsForNamedProfile, TestExplicitProjectOutranksForeignInjectedRoot, TestResolveAMQContextUsesCanonicalCustomRoot
  - doctor_test.go: TestDoctorSharedIndexCollisionIsStateAware, TestDoctorDiscoversLiveReplacementPanesForSharedIndexCollision
  - dogfood_invariants_test.go: TestInvariantCurrentPaneCollapseNotOperatorVisible, TestInvariantStatusJSONExposesVisibleLeadFields
  - global_noc_test.go: TestGlobalNOCDetectionRequiresExactStampedCurrentPane, TestGlobalNOCDetectionRejectsStampedPaneWithoutLivePID, TestWaitForGlobalNOCPIDIdentityRejectsPaneOnlyLiveness, TestGlobalNOCRegistrySupersedesGenerationAndTracksPollingRun, TestWriteGlobalNOCRegistryRejectsDuplicateRunIDsWithoutChangingBytes, TestGlobalNOCRegistryRearmsPreparedGenerationAfterExactPaneGone, TestResolveGlobalNOCRegistrationPlanDefaultsAndOptOut, TestGlobalNOCBootstrapPinsContractAndBounds, TestGlobalNOCBootstrapV1Golden, TestGlobalNOCRegistryRejectsControlRootSymlinkAuthoritySwap, TestGlobalNOCRegistryRejectsLaunchControlRootMismatch; helpers: installActiveGlobalNOC, stubVerifiedGlobalNOCPane
  - global_status_test.go: TestGlobalStatusNamespaceComparisonCanonicalizesBothSides, TestGlobalStatusMissingRegistryIsVisibleReadOnlyAndNonFatal, TestGlobalStatusDefaultProfileScannerCannotReadSiblingSession, TestGlobalStatusStoppedNOCWithLiveRuntimeIsDegradedAndCannotStart, TestGlobalStatusOpenGatesRejectsForeignItemBinding, TestGlobalStatusRootSessionCannotEscapeRegistryScope, TestGlobalStatusRealRegistryReadIsBytePreserving, TestStatusRecordProjectsOrchestratorRegistrationProvenance, TestGlobalStatusProjectsMixedRunsFromOneRegistrySnapshot, TestGlobalStatusNamedProfileUsesIsolatedAMQBaseRoot, TestGlobalStatusMalformedLeadClassificationIsUnknownAndHumanVisible, TestGlobalStatusContradictorySourcesFailClosed, TestGlobalStatusTeamProjectContradictionStopsCrossRootProjection; helpers: globalStatusTestEnvelope, globalStatusTestRegistry, globalStatusRowsBySession, assertGlobalStatusActionsConfirmGated, decodeGlobalStatusTestEnvelope
  - json_envelopes_test.go: TestRunStatusJSONHasNoHumanComments
  - liveness_test.go: TestStoppedLaunchRecordOverridesReusedLivePID, TestCanonicalClassifierRejectsReusedExternalPaneID, TestClassifierReplacementLive, TestClassifierPresenceLiveWhenWriterUnknown, TestInspectLivenessUnknownFailsClosed, TestStatusExplainsMissingLaunchRecordForLaunchapiSeats, TestInspectLivenessParityWithPaneProbeForLiveSeats, TestStatusRollupUnchangedForLegacyLaunches, TestInspectAbsentConflictsWithLivePaneProbeFailsClosed, TestInspectErrorFallsBackToExistingSignalsWithWarning, TestLaunchapiInspectMemoCollapsesRosterToOneCall, TestStatusSurfacesNotableLaunchapiObservation, TestStatusOmitsUnremarkableLaunchapiObservation
  - notify_self_operator_test.go: TestCollectSelfOperatorVisibilityCurrentStateAndClear, TestCollectHumanOnlyGateConflictAndReopen, TestCollectSelfApprovalAdversarialTable
  - operator_self_approve_test.go: TestSelfApprovalRecoveryRejectsNoteMismatchAndKeepsReservation
  - output_test.go: TestStatusHumanOutputColorMatrix
  - pane_adopt_test.go: TestAdoptLivePane, TestAdoptLivePaneByPidWhenCommandMismatches, TestVerifiedAgentPID
  - preflight_test.go: TestPreflightStaleWakeLockIsPreservedForAMQ, TestPreflightLiveWakeLockBlocks, TestPreflightLiveWakePIDReuseIsStale, TestPreflightLiveAgentRecordBlocks, TestPreflightDeadAgentRecordIsNonBlocking, TestPreflightReusedSameBinaryAgentPIDIsNonBlocking, TestPreflightTTYReusedAgentPIDIsNonBlocking, TestPreflightActivePresenceWithLiveWakeBlocks, TestPreflightStalePresenceDoesNotBlock, TestPreflightForceDuplicateOverridesAllSignals, TestPreflightDryRunDoesNotRemoveStaleWakeLock, TestWakeProcessMatcherRejectsForeignRoot, TestWakeProcessMatcherAcceptsSymlinkedAbsoluteRoot, TestPreflightLiveWakeWithSpacesInRootBlocks, TestPreflightSiblingWorkstreamRootIsStale, TestPreflightSuffixOfForeignRootIsStale, TestPreflightForeignRootPIDReuseIsStale, TestPreflightLiveWakeWithRelativeRootBlocks, TestPreflightTeamAggregatesBlockers, TestPreflightZombiePresenceDoesNotBlock, TestPreflightReusedSameBinaryPIDDoesNotPreserveFreshPresence, TestPreflightFreshPresenceWithLiveAgentStillBlocks, TestPreflightLiveWakeAndDeadLaunchStillBlocks, TestPreflightCorruptWakeLockKeepsPresenceConservative, TestPreflightWakeLockOnDifferentMachineStillBlocks, TestPreflightDuplicateKnownWakeLockFieldFailsClosed, TestPreflightFreshPresenceWithCodexSeatStillBlocks, TestPreflightFreshPresenceWithoutLockOrRecordStillBlocks; helpers: writeWakeLock
  - profiles_test.go: TestBootstrapCurrentTeamReadsLaunchProfile, TestSameProfileSessionUpStatusDispatchNoNamespaceConflict
  - real_amq_compatibility_test.go: TestRealAMQCompatibility; helpers: realAMQOrchestrationContract
  - removed_command_test_support_test.go: (no tests); helpers: renderNotifyForTest, executeNotifyForTest
  - repair_fault_test.go: TestFaultRemedyRelaunchBuildsCommand, TestFaultRemedyRelaunchUnavailableWithoutSession, TestFaultRemedyResumeBuildsCommand, TestFaultRemedyResumeUnavailableWithoutRole, TestFaultRemedyCommandsIncludeProjectAndNamedProfile, TestFaultObjectLeadPaneCollapsedHasRemedyAndDocRef, TestFaultObjectLeadPaneDeadHasResumeRemedy, TestFaultObjectDetachedSessionHasResumeRemedy, TestFaultObjectActionKindIsAlwaysRepair, TestRepairFaultAppearsInStatusJSONForCollapsedLead, TestRepairFaultAppearsInStatusJSONForDeadPane
  - retained_fixture_support_test.go: (no tests); helpers: isolateCanonicalContextTest
  - runtime_actions_test.go: TestSessionActions, TestFirstLiveTmuxSession
  - runtime_identity_test.go: TestLaunchRuntimeIdentityProcessStartSkewBoundary, TestLaunchRuntimeIdentityPaneTTYTieSurvivesClobberedTitle, TestLaunchRuntimeIdentityPaneTTYTieRequiresLivePIDAndSameTTY
  - runtime_json_test.go: TestTmuxRuntimeFromInfo, TestTmuxRuntimeFromEmptyInfoIsNil, TestTerminalRuntimeMirrorsTmuxIdentity, TestTerminalRuntimeFromITerm2Info, TestPolicyAwareMemberActionsForTerminalAppRow, TestPolicyAwareMemberActionsForITerm2Row, TestSyncTerminalRuntimeFromTmuxUsesSamePaneAlive, TestDecorateTerminalRuntimeCapabilitiesIsExplicitForLocalInput, TestMemoizePaneListerCallsUnderlyingOnce, TestFillPaneAlive, TestFillPaneAliveFromLivenessRejectsReusedExternalPane, TestLivePaneIDSetDegradesOnError; helpers: swapStatusPaneLister
  - status_board_test.go: TestBoardLastActivity, TestBoardRowAgesOutStaleRecords, TestStatusBoardWakeLiveSessionIsDegradedNotStopped, TestStatusBoardSessionsJSONEnvelope, TestStatusBoardSameProfileSessionContainerNotDoubleCounted, TestStatusBoardJSONCarriesProfileActionsAndOrchestration, TestStatusBoardJSONReleaseReadinessBlockedByVisibleLeadInvariant, TestStatusBoardUnresolvedBaseRootDegradesGracefully, TestStatusBoardUnresolvedJSONStillEmitsEnvelope, TestClassifyBriefStubVsNoneVsReal, TestBoardBriefCellLabels, TestBoardAgentsCellStateAware, TestSortBoardRowsAttentionFirst, TestBoardSummaryFoldsNonDefaultRoot, TestRelativeAge, TestBoardLastActivityFreshnessHonesty, TestDuplicateProbeFromStateProbePreservesFullProcessIdentity, TestStatusBoardMultiSession, TestStatusBoardJSONOmitsOrchestrationForFlatTeams, TestStatusBoardJSONHasNoHumanComments, TestStatusBoardEmptyDiscoveryDegradesGracefully, TestStatusBoardBriefOneLiner, TestBoardRendersStubAndNoBriefLabels, TestBoardRendersAttentionFirstOrder, TestBoardSummaryLineCounts, TestBoardSummaryLineShowsZeroAtRisk; helpers: runBoardExec
  - status_replacement_test.go: TestLiveReplacementPane_SameEngineFound, TestLiveReplacementPane_CrossEngineRejected, TestLiveReplacementPane_TitleTokenWins, TestLiveReplacementPane_NoPanesOrError, TestBatchReplacementPaneResolverClaimsGenericPaneOnce, TestBatchReplacementPaneResolverPreservesExactRolePriority, TestBuildStatusRowsDoesNotMapOneReplacementPaneToEveryStaleRole; helpers: withStubPaneLister
  - status_test.go: TestRunStatusSessionRequiresTeam, TestRunStatusBoardNoTeamDegradesGracefully, TestRunStatusProjectTargetsSessionOtherDir, TestRunStatusProjectValidation, TestStatusJSONTopologyFlagsSplitSessionForVisibleMode, TestExecuteStatusIsolatesForeignProfileLaunchRecord, TestExecuteStatusJSONNamedProfileKeepsExactStopActionsOnLegacySessionRootConflict, TestExecuteStatusJSONNamedProfileAllowsPresenceOnlyLegacyRoot, TestRunStatusExplicitNamedProfileDoesNotCreateLegacySessionInbox, TestRunStatusDefaultProfileWarnsWhenNamedProfileHasSameSession, TestExecuteStatusJSONSameProfileSessionAllowsNestedNamedNamespaceOnly, TestStatusJSONSurfacesManagedChildLocalInputBlocker, TestStatusLocalInputSkipsExternalUnmanagedAndDeadPanes, TestExecuteStatusJSONSameProfileSessionKeepsLegacyAgentDurableConflict, TestExecuteStatusJSONSameProfileSessionKeepsLegacyAgentsHandleDurableConflict, TestExecuteStatusJSONSameProfileSessionAllowsSiblingNamedNamespaces, TestExecuteStatusJSONIncludesExecutionMode, TestExecuteStatusJSONIncludesPlannerLeadMode, TestExecuteStatusJSONBlocksReleaseReadyForSoloWithoutReason, TestExecuteStatusJSONReleaseReadyWithVisibleTeamAndReview, TestExecuteStatusJSONBlocksReleaseReadyForReviewCompleteWithoutEvidence, TestExecuteStatusJSONReleaseReadyAllowsTrivialSoloWithoutReason, TestExecuteStatusJSONReleaseReadyBlockedByVisibleLeadInvariant, TestExecuteStatusJSONPolicyDisablesDirectChildControlActions, TestExecuteStatusJSONMarksOperatorVisibleLead, TestExecuteStatusJSONFlagsDetachedVisibleLeadInvariant, TestExecuteStatusJSONFlagsCurrentPaneCollapsedLead, TestExecuteStatusJSONFailClosedWhenPaneOriginUnprovable, TestExecuteStatusJSONAllowsExternalLeadInCurrentPane, TestExecuteStatusJSONDoesNotRecheckExternalLeadPaneByBareID, TestExecuteStatusJSONFlagsGenericExternalProjectLeadBoundary, TestExecuteStatusJSONMarksDirectLeadSessionVisibleWhenExternal, TestExecuteStatusJSONDirectLeadSessionBarePaneNotVisible, TestExecuteStatusJSONFlagsEmptyLeadMultiMemberProjectTeam, TestExecuteStatusJSONGlobalOrchestratorRoleBoundary, TestStatusJSONOrchestratorWakeDrivenUserPollOnly, TestStatusJSONWakeAutoDrainDegradedWhenSidecarDead, TestExecuteStatusWakeLockOnlyWakeLive, TestExecuteStatusWakeLiveWithRelativeAMQRootFromOtherCWD, TestStatusTreatsExternalLeadPaneAsLiveAndActionable, TestStatusKeepsActionsAvailableWhenAgentPIDVerifiesButTmuxScanMisses, TestStatusJSONProvesExternalOrchestratorBinding; helpers: actionsByKind, assertNoLegacyNamespaceConflictReason, swapStatusLocalInputDetector
  - stop_namespace_conflict_test.go: TestAllowsExactNamedProfileDownMatrix
  - task_completion_evidence_test.go: TestStatusLegacyCompletionStateAndRetainedDoctorHelper
  - team_launch_test.go: TestParseTmuxClientsReturnsControlModeClients
  - team_launch_tmux_title_test.go: TestPaneTitleToken_Format
  - team_member_control_continue_test.go: TestValidateTmuxControlClientNameRejectsLeadingDash
  - v228_contract_concurrent_start_test.go: TestV228ContractConcurrentStartsLaunchOneSquad
  - v228_contract_idle_wake_test.go: TestV228ContractWakeTargetIsResolvedFromTheLaunchRecord
  - verify_action_test.go: TestVerifyActionBlocksGatedReleaseBeforeOperatorAnswer, TestVerifyActionPassesBoundOperatorAnswer, TestVerifyActionLatestOperatorAnswerWinsOverOlderApproval, TestVerifyActionSameTimestampUsesIDTiebreak, TestVerifyActionRejectsApprovedAnswerForDifferentTarget, TestVerifyActionQuestionRequiresStrictBindingFields, TestVerifyActionRejectsProseOnlyOperatorAnswer, TestVerifyActionNoGateExitCode, TestVerifyActionRequiresOperatorAnswerSender, TestVerifyActionRejectsApprovalBeforeLatestGateQuestion, TestVerifyActionDeniedExitCode; helpers: runVerifyActionExec
  - version_alignment_test.go: TestStatusJSONIncludesVersionAlignment, TestVersionAlignmentWarningBeforeLaunch
  - wake_lock_compat_test.go: TestDecodeWakeLockRejectsIncompleteStateBinding, TestDecodeWakeLockRejectsWrongTypeMachineID, TestDecodeWakeLockRejectsDuplicateMachineID, TestDecodeWakeLockRejectsNullMachineID, TestWakeLockSameMachine
  - v228_contract_canonical_path_test.go: TestV228ContractMiscasedProjectStatusReportsCanonicalCoordinates
  - v228_contract_duplicate_live_test.go: TestV228ContractTwoLiveMatchingRecordsReportDuplicateLive, TestV228ContractOneLiveOneDeadRecordSelectsTheLiveOne
  - v228_contract_worktree_remnant_test.go: TestV228ContractWorktreeRemnantStatusReportsLiveCanonicalActor, TestV228ContractWorktreeRemnantDoctorAgreesWithStatus
  - operator_attention_test.go: TestNotifyEmitsAndDedupesOperatorGate, TestNotifyEscalatesOperatorGateAgeDespiteThrottle, TestNotifyTerminalGateTombstonesPersistedNotification, TestNotifyStaleTerminalDoesNotTombstoneReraisedGeneration, TestNotifyConflictingGateCopiesStayActive, TestNotifyRepairedRequestWithBoundCloseStaysActive, TestNotifyDegradedScanNeverTombstonesPriorGate, TestNotifyRenotifiesAfterThreshold, TestNotifyUsesCustomOperatorHandle, TestNotifyProfileScopesOperatorAttentionAndStateKeys, TestNotifyIgnoresP2PProseOnlyNeedsYou, TestNotifyNoOperatorReportsDisabled
  - attn_test.go: TestNeedsYouThreads_SortedByReasonThenAge
  - coordination_test.go: TestTriageRollupString
  - flow_test.go: TestFlowGraphEmpty, TestFlowGraphEnrichesAndSorts, TestFlowGraphStaleThreadDoesNotMark, TestFlowGraphMatchesEitherDirection
  - stale_test.go: TestStaleness_BlockedThreadDecaysPastWindow
  - dedup_test.go: TestDedupPerSinkEscalationClearAndFailure, TestLocalFingerprintChanges, TestLocalInputUnknownPreservesAndConfirmedClearResets
  - notifier_test.go: TestFanOutIndependent
  - procinfo_test.go: TestChildrenIndexFindsRealChild
  - operator_test.go: TestOperatorStatusJSONCountsBlockedNativeGoal
- Tests restored (subject kept; test-side transforms in scratchpad xform.py):
  - context_resolution_test.go, 14 tests: TestResolveCanonicalContextPrecedenceMatrix, TestExplicitSessionSwitchCarriesProfileButNotOldTupleIdentity, TestMatchingLowerTupleContributesHandleAndExactRoot, TestResolveCanonicalContextAmbiguousLaunchesReportEveryProvenance, TestResolveCanonicalContextSkipSessionResolutionAvoidsRootAmbiguity, TestStoppedAndPIDReusedLaunchRecordsDoNotWinContext, TestLegacyLaunchRecordsWithoutStoppedAtUseLivenessProbe, TestExplicitRootOverridesMalformedInjectedIdentityWithWarning, TestExplicitExternalRootWinsForNamedProfile, TestExplicitProjectOutranksForeignInjectedRoot, TestResolveAMQContextUsesCanonicalCustomRoot, TestContextRejectsSameBinaryPIDReuseWithSameOrUnknownTTY, TestResolveCanonicalContextSharedTupleDoesNotRequireHandle (its TMUX_PANE handle block is dropped), TestOrdinaryCommandsDoNotSpliceConflictingTuples. Shared fixture isolateCanonicalContextTest loses its pane-title seam.
  - liveness_test.go, 10 tests: the launchapi Inspect floor tests (TestInspectLivenessUnknownFailsClosed, ...ParityWithPaneProbeForLiveSeats, TestStatusRollupUnchangedForLegacyLaunches, TestInspectAbsentConflictsWithLivePaneProbeFailsClosed, TestInspectErrorFallsBackToExistingSignalsWithWarning, TestLaunchapiInspectMemoCollapsesRosterToOneCall, TestStatusSurfacesNotableLaunchapiObservation, TestStatusOmitsUnremarkableLaunchapiObservation) plus TestClassifierPresenceLiveWhenWriterUnknown and TestStoppedLaunchRecordOverridesReusedLivePID on classifyAgentLivenessFromDisk.
  - v228 contract tests (status canonical path, duplicate-live x2, worktree remnant x2): the pane-lister stub is dropped.
  - operator_attention_test.go, 12 TestNotify* tests: their shared helpers renderNotifyForTest and executeNotifyForTest are restored, with the board's pluralize replaced by the test-side pluralForTest.
  - status_test.go TestRunStatusSessionRequiresTeam, TestRunStatusProjectValidation, TestRunStatusExplicitNamedProfileDoesNotCreateLegacySessionInbox and TestRunStatusDefaultProfileWarnsWhenNamedProfileHasSameSession; output_test.go TestStatusHumanOutputColorMatrix; amq_update_suppression_integration_test.go TestAMQNoisyFakeNativeJSONStartsAtByteZero: the dead runStatus wrapper becomes runStatusWithVersion(args, "dev").
  - authorization_envelope_test.go (4) and operator_self_approve_test.go TestSelfApprovalRecoveryRejectsNoteMismatchAndKeepsReservation: state.Build becomes state.BuildWithThresholds(..., state.Thresholds{}). The same rewrite applies to every internal/state test.
  - real_amq_compatibility_test.go and real_amq_wake_compatibility_test.go are restored whole from the base. TestRealAMQCompatibility drops one assertion that looked the question up through the dead humanApprovalQuestion; the raise mutation's message id is still asserted. These tests are opt-in (AMQ_SQUAD_REAL_AMQ).
  - cli_test.go: TestCommandRegistryPowersDispatchHelpAndCompletion checks for "status" instead of "start", which it passed on only because the old examples contained it. The three TestRunBare* board tests are replaced by TestRunBarePrintsUsage. New: TestRootUsageExamplesNameRegisteredCommands (item 5 guard: every root usage example names a registered verb).
- Lines 62,385 -> 54,627; holding 547 -> 459; tests listed 1,672 -> 1,334.

### Commit 5: C13 verify and AMQ root authority (dead paths cut; guard verb deferred to 2b)

Helper commit f9b0015 (h-c13, branch v3-2a2-c13, base d02f6e8), integrated by cherry-pick onto commit 4.
Integration conflicts: verify_action_test.go and authorization_envelope_test.go took the helper's restored tests, which commit 4 had swept for the dead executeVerifyAction and state.Build. Their state.Build calls then became state.BuildWithThresholds(..., state.Thresholds{}). The manifest kept commit 4's version and mupdate re-ran.

- 3.0 shape: every `verify` subcommand stays, and no subcommand or flag is removed. action and authorization are the guard ancestor (manifest: "keep decision flow and signed auth"). merge, rebind and release are the merge-evidence and release-gate flow, and release-plan is keep-fate (C21). doctor --fix-amq-root and the "amq root authority" check stay.
- Cut, all deadcode-unreachable:
  - verify_action.go: executeVerifyAction (the unadmitted state.Build scan path; commit 4 had already cut it as a cross-cluster dead caller) and verifyActionOperatorHandle, plus three verifyActionExecution fields only that path read.
  - amq_root_authority.go: the leftovers of the team-launch roster sync (launchAMQAuthorityHandles, shouldResolveExistingAMQRoot, rollbackAMQRosterConfigSync, restoreAMQRootConfigSnapshot, errorsJoinWithContext, amqRosterConfigSyncTarget, amqRosterConfigCandidate). amqRootConfigDocument moves to its test file, where four tests decode with it.
  - team_launch_bootstrap.go: launchFileSnapshot. Stale comments naming `resume --exec` (#722) are removed.
- Files (non-test lines before -> after / target): verify_action.go 952 -> 931 / 700 (trimmed); amq_root_authority.go 473 -> 371 / 200 (trimmed); team_launch_bootstrap.go 41 -> 33 / 50 (shrunk); verify.go 611 / 500, verify_authorization.go 337 / 300 and verify_rebind.go 126 / 100 are unchanged (nothing dead).
- Kept-dead extract: verify_action.go decideTypedVerifyActionWithPolicy (3 lines). The manifest's :81 and :569 guard ancestors are runVerifyAction and decideTypedVerifyActionWithAuthorityPolicy, and both are live.
- Behavior changes: none. Every cut was unreachable.
- Deferrals (576 lines left in C13):
  - verify_action.go, 231 left. It is live decision flow and signed-auth emission, with helpers shared with operator, operator_attention, operator_self_approve and compound_release_domain; the guard verb is 2b. The self-operator merge-approval branch (about 75 lines) contradicts plan 7.1/3.5, since merge is not a typed gate, but cutting it now breaks C17's `operator self-approve`. Lead ruling requested.
  - amq_root_authority.go, 171 left. It is live through doctor --fix-amq-root and the root-authority check (C15). The target works only if C15 drops them, or if 2b hands root config to the AMQ launch API. Lead ruling requested with C15.
  - verify.go, 111 left: live merge/release evidence validation, read by operator_self_approve.
  - verify_authorization.go, 37 left, and verify_rebind.go, 26 left: live signed-auth and rebind-proof code. The targets look like flat 20 percent estimates.
- Tests retired: verify_action_test.go TestVerifyActionNoGateExitCode. Its exit 12 came only from the cut unadmitted path. The live path returns pending (exit 10), and the usage text still documents 12. Ruling requested: fix the text or map that failure to no_gate.
- Tests restored by the helper: 10 verify_action decision-flow tests through executeVerifyActionInSelectedContext; authorization_envelope_test.go TestAmbiguousRawGateFrontmatterBlocksVerifierAnswerAndAttention through the selected-context path; amq_root_authority (2), doctor TestExecuteDoctorFixAMQRootRepairsAndReports, and the real-AMQ root authority and compatibility tests.
- Lines 54,627 -> 54,496 after integration; holding 459; tests listed 1,334 -> 1,345.

### Commit 6: C14 amq verb keeps env and route; send boundary and env pinning stay (+ C11 wait_posture)

Helper commit a81d30e (h-c14, branch v3-2a2-c14, base d02f6e8), integrated by cherry-pick. The only conflict was the manifest, which kept the lead's version and re-ran mupdate.
Lead ruling (C14, given to the helper): keep `amq env` and `amq route`; drop `amq send` per 2a-1 ruling 8 (seat messages go through `dispatch`).

- 3.0 shape:
  - `amq env` shows the resolved context and pinned env.
  - `amq route` wraps `amq route explain` with the resolved context. bootstrap.md, the generated team rules and the examples point agents at it.
  - runOwnedAMQSend stays as the one send boundary for dispatch, operator, task and gate close.
- Cut by hand: the runAMQ dispatch arms of the removed subcommands; runAndWriteAMQWithStdin folded into runAndWriteAMQ; ownedAMQSendOptions.ReplyRecipient and its reply branch (only the cut `amq reply` set it). Stale comments naming `restore`, `agent up` and the board/console are fixed. cutdead.py and cutunused.py cut 41 functions and 10 declarations.
- Files (non-test lines before -> after / target): amq.go 1,572 -> 453 / 400 (trimmed); amq_send.go 338 -> 314 / 150 (trimmed); amq_env.go 290 -> 263 / 150 (trimmed); wait_posture.go 303 -> 282 / 100 (trimmed); command_registry.go: the `amq` summary text only.
- Behavior changes to the kept `amq` verb (named):
  1. `amq ops`, `amq who` and `amq presence` are removed. doctor's "amq ops" check runs `amq doctor --ops` itself and is unchanged.
  2. `amq send` and `amq reply` are removed, with the send-as authority guard and `--unsafe-send-as` audit, the p2p self-send guard, the `--body-file` rewrite and the wait-posture override flags.
  3. `amq drain` and `amq watch` are removed, with the lead-owned-mailbox consume guard, the `--override-boundary` audit and the watch wait posture.
  4. `amq list`, `amq read` and `amq thread` are removed.
  5. `amq dlq` (list, read, retry, retry-all, purge) and `amq cleanup` are removed.
  6. Bare `amq` and the unknown-subcommand hint list only env and route. The catalog summary is now "Show the resolved AMQ context and explain routes (env, route)".
- Kept-dead extracts: durableSendWaitPosture and its closure (lastAMQSendFlagValue, parseWaitPostureDuration, defaultOwnedSendReceiptWait in wait_posture.go; amqSendPassthroughFlagConsumesValue in amq.go), 61 lines. resolveAMQContext, amqCommandEnv, runAndWriteAMQ and guardOwnedWait are live.
- Deferrals:
  - amq.go, 53 over. Its helpers serve context_resolution (namedProfileFromInheritedAMQRoot), dispatch/operator/task/gate_close/evidence (resolveAMQContextForProject/ForNamespace) and operator (teamLeadHandle, parseP2PThread); they move with those callers.
  - amq_send.go, 164 over. All of it is the live send boundary and committed-delivery evidence recovery.
  - amq_env.go, 113 over. That includes the 65-line launch closure; the rest is env resolution that status, doctor, bootstrap and operator use.
  - wait_posture.go, 182 over. guardOwnedWait and its snapshot, gate lookup and audit chain are live through dispatch and runOwnedAMQSend. Reaching 100 means a 2b operator-session redesign of the wait-posture guard.
- Tests retired (43): amq_test.go, 40 (the TestAMQ* tests of send, reply, drain, read, list, thread, dlq, cleanup and passthrough; the vacuous TestAMQSendAsRefusalsWriteNoAudit; TestDefaultRunAMQStreamingDisablesChildUpdateCheck; TestAMQUnsafeSendAsAuditLifecycle; four TestAMQWatch*; TestSplitAMQPassthroughArgs and ParityFlags; TestPassthroughNeedsStdin; TestAMQReplyAsOperatorHandleRequiresUnsafeOverride); amq_send_test.go TestCommittedDeliveryEvidenceBindsReplyToResolvedRecipient; scoped_flags_test.go TestAMQPassthroughScopedShortAliasesSplit; amq_update_suppression_integration_test.go TestAMQNoisyFakeExactTextSurfaces. Helpers readAMQBoundaryAuditRecords, writeAMQBoundaryTeam and collectCallVerbs went with them.
- Tests restored by the helper:
  - Five TestResolveAMQEnv* tests now call resolveAMQEnvInDir("", ...).
  - TestOrdinaryCommandsDoNotSpliceConflictingTuples drives `amq route --to peer` instead of `amq list`.
  - The unknown-subcommand test expects "Try 'env' or 'route'."
  - New: TestDurableSendWaitPostureUsesLastWaitFlagsAndPreservesNoWait, on the kept-dead extract.
  - TestAMQNoisyFakeRepresentativeJSONSurfaces and TestScopedShortFlagAliasesRegistered keep only their env and route rows.
  - wait_posture_pty_test.go TestRealPTYWaitPostureRefusesNamedGateBeforeTimeout is driven by `dispatch --wait-for drained` instead of `amq watch`. The helper saw it miss its 5 s deadline once under full-suite load; it passes standalone.
- Stale text the helper reported outside its files: dispatch.go's "exactly like 'amq-squad amq send'" is fixed in the stale-names commit. The repository docs and plugin skills that list removed amq subcommands (RELEASING.md, README.md, MIGRATION.md, docs/operator-cookbook.md, docs/skills.*, plugins/*/skills/cli/SKILL.md, scripts/test_check_skill_commands.py) are outside internal/ and recorded for the 3.0 docs pass.
- Lines 54,496 -> 53,305 after integration; holding 459; tests listed 1,345 -> 1,303.

### Commit 7: C23 roles, agents, model defaults, effort, native args

Helper commit 3fd6ce5 (h-c23, branch v3-2a2-c23, base d02f6e8), integrated by cherry-pick.
Conflicts:
- team_test.go: commit 4's test-side unused sweep had removed setupFakeAMQRelativeSessionRoots, and this commit retires TestShouldAppendBootstrapWithDefaultChildArgs and TestEnsureDefaultChildArgs. Both removals stand.
- The manifest kept the lead's version and mupdate re-ran.

- 3.0 shape:
  - `role` (draft) and `roles` stay; plan 10 keeps the role catalog and off-path drafting. No subcommand or flag is removed.
  - The dead provider-argument assembly (R4) is cut:
    - the legacy Claude preauth argv splicing;
    - the default-child-arg prefix helpers;
    - validateMembersTrust;
    - defaultChildArgsForBinary and launchDefaultChildArgs;
    - the native prompt-boundary check and its Cardinality field;
    - joinedAgentArgs and formatBinaryArgs;
    - the Builtins-only effort wrappers.
- Cut by hand:
  - roleDraftNextCommand.
  - The catalog.json routing-metadata fields.
  - applyMemberEffortCatalogMode, folded into its caller. Its warnUnknown=false caller was the launch-time `--effort` cut in commit 2.
  - parseBinaryArgFlags' duplicate per-binary blocks, merged into one loop with the same errors.
  - emitAgentCatalogWarnings, inlined.
- Files (non-test lines before -> after / target):
  - role_draft.go 493 -> 482 / 150 (trimmed; floor)
  - roles.go 146 -> 146 / 80 (usage text only; floor)
  - agent_args.go 235 -> 199 / 200 (shrunk)
  - agent_catalog.go 162 -> 145 / 130 (trimmed; floor)
  - agent_defaults.go 459 -> 167 / 300 (shrunk; the claudeInScopePreauthAllowlist extract is kept)
  - model_defaults.go 170 / 130 (unchanged; the whole file is kept-dead launch closure)
  - native_args.go 169 -> 85 / 130 (shrunk)
  - effort.go 112 -> 98 / 180 (shrunk)
  - v3_holding.go 459 -> 450: shellJoin, whose last caller was role draft's next command.
- Behavior changes (named):
  1. `role draft` no longer prints a "Next:" command, and its JSON drops `next_command`. Both named `amq-squad team member add`, which 3.0 does not have (item 5).
  2. The `roles` usage names `amq-squad team init --roles` instead of the removed top-level `init`.
  3. catalog.json overlays no longer read capability_tier, cost_index, latency_index, strengths or work_classes. No live verb read them, and 3.0 routing reads registry.yaml (plan 5, 2b).
- Kept-dead (the launchapi closure plus the manifest extract):
  - agent_args.go: binaryArgsFor, mergeBinaryArgs, composeBinaryArgs, binarySingletonSpan (107 lines)
  - agent_defaults.go: claudeInScopePreauthAllowlist (extract, 28), claudeWorkerPreauthEligible, defaultChildArgsForBinaryWithTrust, launchDefaultChildArgsWithTrust, modelArgsForBinary, plus the codex arg vars (102 lines)
  - model_defaults.go: the whole file (170)
  - native_args.go: nativeValueSpecForArg and the value-spec tables
- Deferrals:
  - role_draft.go, 332 left. It is the live `role draft` verb: flags, profile and session resolution, brief context, the drafter run with its manual fallback, the JSON envelope, the generated-document validator and atomic staging. The manifest reason ("provider-argument assembly") does not fit this file. Reaching 150 needs the 3.0 role format (plan principle 5: a role is a prompt) and squad.yaml (2b), or cutting the validator. Lead ruling requested.
  - roles.go, 66 left. All of it is live: the catalog table, the numbered market that `team init --roles 2,9` uses, staged custom roles and `roles --json`. Lead ruling requested.
  - agent_catalog.go, 15 left: the catalog overlay loader that team init's --effort uses. registry.yaml replaces it in 2b.
  - model_defaults.go, 40 left: kept-dead launch closure.
- Tests retired (12): agent_args_test.go TestValidateNativePromptBoundaryRejectsDanglingKnownValues and ...CardinalityAliasesAndDelimiter; agent_defaults_gh732_legacy_lock_test.go TestClaudePreauthChildArgsStillEmitsAllowedTools; agent_defaults_test.go TestClaudePreauthChildArgs, TestConfiguredClaudePermissionAllowlistIsStrictlyRoleScoped, ...MergesExplicitAllowedTools and TestStripRecordedLauncherPreauthRecognizesHistoricalAndSafeForms; team_launch_launchapi_test.go TestLegacyBackendRetainsWorkerPreauthDuringDualRun; team_test.go TestValidateMembersTrustRejectsSmuggledBypass, TestShouldAppendBootstrapWithDefaultChildArgs and TestEnsureDefaultChildArgs; agent_catalog_test.go TestLoadAgentCatalogOverlayCanSetRoutingMetadata (behavior change 3).
- Tests restored by the helper: role_draft_test.go (2, now asserting that no "Next:" line or next_command appears); effort_test.go (2, through the catalog-aware functions); trust_model_test.go TestDefaultChildArgsForBinaryWithTrust; restore_test.go TestBinaryArgsJoinedRoundTripPreservesQuotingAndEscapes (the joiner is inlined over shellQuote); legacy_composer_isolation_test.go, whose symbol list narrows to claudeInScopePreauthAllowlist.
- Lines 53,305 -> 52,842 after integration; holding 459 -> 450; tests listed 1,303 -> 1,291.

### Commit 8: C27 brief drafting (off-path verb), simple_start_brief_draft.go deleted, one drafter seam (+ C18 team_rules_draft.go)

Helper commit a26ea61 (h-c27, branch v3-2a2-c27, base d02f6e8), integrated by cherry-pick.
Conflicts:
- status_board_test.go was modified there and deleted in commit 4. It stays deleted with the board, and the three brief-label board tests the helper restored go with it.
- v3_holding_test.go: commit 4 had removed the unused wakeDrainInject, and this commit moves validSimpleStartBriefDraft to brief_test.go. Both removals stand.
- The manifest kept the lead's version and mupdate re-ran.
After integration, cutdead and cutunused over the cluster's files removed two leftovers the earlier commits had made dead: briefStubFirstLine (briefs.go; its only reader was the deleted board) and normalizeScopedShortFlagName (workstream.go; its only caller was the amq passthrough alias split cut in commit 6).

- 3.0 shape: `brief` (--goal drafting, --seed-from) stays as the off-path drafting verb (plan 10).
- Cut by hand:
  - runBriefWithDependencies;
  - the stale "next: amq-squad plan" output line and the start/plan/wizard usage paragraph;
  - the claude/codex/transcript placeholder arms of resolveSeed;
  - the unused seedReadFile seam.
- Moved: the brief draft contract from simple_start_brief_draft.go into briefs.go (draftBrief, buildBriefDraftPrompt, validateBriefDraft). The shared drafting seam (cliDraft, cliDrafterRunner, the resolveDraftConfig and runDraft vars, runCLIDraft) and the draft-text validators go into team_rules_draft.go. The four holding-file drafter items fold into runCLIDraft or are replaced by cliDrafterAttemptsText.
- Renames:
  - draftSimpleStartBrief -> draftBrief
  - validateSimpleStartBriefDraft -> validateBriefDraft
  - validateSimpleStartTeamShape and the team-rules scope matcher -> matchRoleBullets
  - simpleStartDependencies and team rules' resolve/run seams -> resolveDraftConfig / runDraft
- Files (non-test lines before -> after / target):
  - brief.go 127 -> 100 / 100 (shrunk)
  - briefs.go 197 -> 168 / 180 (shrunk)
  - briefs_seed.go 234 -> 213 / 170 (trimmed)
  - workstream.go 327 -> 290 / 100 (trimmed)
  - simple_start_brief_draft.go 236 -> deleted
  - team_rules_draft.go 237 -> 296 / 150 (trimmed; it now holds the shared seam and validators)
  - v3_holding.go 450 -> 399
  - drafter/run.go 324 / 249 and drafter/config.go 262 / 201 are untouched.
- Behavior changes (named):
  1. `brief` no longer prints "next: amq-squad plan ...".
  2. brief --goal's manual path prints drafter attempts in the shared "Drafter attempt (backend): cmd" form, the same as team rules init.
  3. `--seed-from claude:|codex:|transcript:` fails with "unknown kind" instead of the v2 "not implemented in this slice" placeholder.
- Kept-dead (bootstrap.go closure, C12): workstream.go bootstrapWorkstream, siblingWorkstreamSummaries, workstreamHandles, latestWorkstreamModTime, formatOptionalTime; briefs.go resolveBriefHome.
- Deferrals, drawn from the budget:
  - workstream.go, 190 over. It holds the bootstrap closure (about 90 lines), live workstream resolution and the scoped-flag plumbing. The C12 commit decides the closure.
  - briefs_seed.go, 43 over. That is the live file:/issue:/gh: seed sources plus provenance and the atomic writer.
  - team_rules_draft.go, 146 over. Team-rules drafting is "rewrite around squad.yaml in 2b", and the shared seam lands here.
  - drafter/run.go 75 over and drafter/config.go 61 over: everything is live, and the targets are proportional splits. Lead ruling requested on whether to cut a named drafter feature.
- Tests retired: briefs_test.go TestEnsureBriefStubCreatesMissing, ...PreservesExisting, ...SilentWithoutInputs, ...ConcurrentExclusive (the brief stub writer was dead).
- Tests restored or adapted by the helper:
  - briefs_seed_test.go: three tests drive writeSeedBriefForProfile; TestResolveSeedRejectsClaudeAndCodex expects "unknown kind".
  - briefs_test.go: two path tests drive briefPathForProfile.
  - brief_test.go: three --goal tests use installBriefDrafter, and TestBriefSeedFromFileWritesBrief asserts that "amq-squad plan" is absent. TestValidateSimpleStartBriefDraftExactShape is renamed TestValidateBriefDraftExactShape.
  - team_rules_draft_test.go swaps runDraft.
- Lines 52,842 -> 52,500 after integration; holding 450 -> 399; tests listed 1,291 -> 1,287.

## Deferral ledger (draws on the overage budget)

| file | over target | reason | commit |
| --- | --- | --- | --- |
| internal/cli/team.go | 1,527 | team init/rules/sync rewrite around squad.yaml (2b) | 2 |
| internal/cli/verify_action.go | 231 | guard verb (2b); self-operator merge branch awaiting a ruling with C17 | 5 |
| internal/cli/amq_root_authority.go | 171 | live through doctor --fix-amq-root (C15 decides) | 5 |
| internal/cli/verify.go | 111 | live merge/release evidence (2b merge train) | 5 |
| internal/cli/verify_authorization.go, verify_rebind.go | 63 | live signed auth and rebind proof | 5 |
| internal/cli/amq.go, amq_send.go, amq_env.go, wait_posture.go | 512 | callers' helpers and the live send boundary; wait-posture guard redesign (2b) | 6 |
| internal/cli/role_draft.go, roles.go, agent_catalog.go, model_defaults.go | 453 | 3.0 role format and squad.yaml (2b); live catalog overlay; kept-dead launch closure | 7 |
| internal/cli/workstream.go, briefs_seed.go, team_rules_draft.go, drafter/run.go, drafter/config.go | 515 | bootstrap closure (C12), live seed sources, team-rules drafting (2b), live drafter | 8 |
| total | 3,583 (plus status.go 55 and liveness.go, pending under ruling 5) | | |

These are the helpers' floors as they reported them. The watch-list rows (amq.go, role_draft.go, cli team.go) get a second pass before the end checkpoint, and every row here is rechecked against the budget then.

### Commit 9: C16/C10 follow-up (ruling 5) and the C2 runtime files moved to their 3.0 homes

- liveness.go: decision under ruling 5. The target is raised from 250 to the measured floor, 356 (the manifest row now carries target_lines 356). Evidence, measured after compaction:
  - The verdict vocabulary and the disk classifier (liveness.go:1-139) are 139 lines: 110 code, 11 blank, 18 comment. That is under the old 250 budget on its own.
  - The kept launchapi session Inspect floor (liveness.go:140-355: launchapiInspectOutcome through classifyAgentLivenessForRollup, gh#737/#766) is 172 code, 17 blank, 28 comment.
  - 3.0 keeps it as AMQ-owned liveness, which the 250 figure predates.
  - Comments went from 214 to 46 lines, which is more than halved. Every full-line comment was rewritten as a short note per declaration; code is unchanged.
  - liveness.go 523 -> 355 / 356 (shrunk).
- status.go: a named 60-line code cut. 955 -> 895 / 900 (shrunk).
  - The Symphony lifecycle activity fallback goes: symphonyActivitiesByHandle and isSymphonyLifecycleMessage. Symphony was a launch mode of the cut driver, and the activity package's manifest row already says to drop SymphonySnapshot.
  - The layout_finalization warning goes. It read a file only the deleted layout finalizer wrote. layoutFinalizationWarningPath leaves the holding file, 399 -> 390.
- Moved (C2 delete-fate files, now gone):
  - runtime_identity.go 93 -> deleted. The launch-record PID identity (classifyLaunchRuntimeIdentity, launchRecordProcessMatcher, launchRuntimeProbe(FromDuplicate), launchProcessStartSkewEpsilon) moves to launch.go, the seat-exec ancestor file. launch.go 194 -> 281 / 300 (shrunk).
  - runtime_actions.go 96 -> deleted:
    - sameResolvedDir goes to pathnorm.go, with the resolveDir wrapper inlined (258 -> 270, C26 later).
    - readPromptBody, stdinIsInteractive and readAllPrompt go to body_warning.go (keep, 71 -> 126). That file is the message-body helper file for dispatch and operator.
    - resolveProjectProfile goes to context_resolution.go (989 -> 1003, C26 later). Its comment named the cut runtime-control verbs.
- Behavior changes (named): `status` no longer derives a member's activity from Symphony lifecycle messages, and no longer shows a layout_finalization warning.
- Tests retired: status_test.go TestStatusJSONUsesSymphonyActivityFallback, TestStatusJSONSymphonyBeforeRunCanBeFresh, TestStatusJSONSymphonyAfterCreateNeverFresh, TestStatusJSONSymphonyBeforeRemoveNeverFresh. TestStatusJSONHeartbeatWinsOverSymphony stays: it pins the heartbeat path.
- Tests adapted: profiles_test.go calls canonicalFilesystemPath where it called the inlined resolveDir.
- Lines 52,500 -> 52,242; holding 399 -> 390; tests listed 1,287 -> 1,283.

### Commit 10: C17 operator. Gate answer and attention projection kept; directive, poll, watch, notifications and operator delivery cut

- 3.0 shape (manifest C17: "keep gate raise/answer and attention projection"; 2a-1 ruling 8, seat messages through dispatch; notifier: "NOC renders attention"):
  - `operator` dispatches answer, self-approve and status.
  - directive, poll and watch are cut. operatorSubcommands now drives both the dispatch hint and the help test.
- Cut by hand:
  - the three dispatch arms and their usage;
  - operator status's operator_delivery and operator_loop blocks, the poll lease (read, write, apply, path) and the directives-unacked count;
  - the claimed, conflict, watch and notifications envelope fields;
  - the operator-poll view's cursor and open_blockers;
  - operator_delivery.go, which is removed with its uses: the status JSON (commit 4), team init's plan JSON and dry-run line, and the kept-dead bootstrap context and its embedded bootstrap.md template, which drops the five "Operator delivery" lines and switches to the operator view's interaction mode. That template change is non-verbatim and named here.
  - deadcode then made the notify engine in operator_attention.go unreachable: selection, dedup state, reservations, sinks and self-operator visibility notifications. cutdead.py removed it.
  - the notifier package (4 files, 136 lines) and attention/dedup.go (42 lines), whose last importer was that engine.
- Files (non-test lines before -> after / target): operator.go 1,758 -> 964 / 600 (trimmed); operator_attention.go 1,098 -> 457 / 400 (trimmed); operator_delivery.go 117 -> deleted; bootstrap.go 618 -> 592; status.go 895 -> 884; team.go 1,877 -> 1,871; lead_identity.go 145 -> 116 (dead pane matchers); attention/event.go 39 -> 32 (shrunk); v3_holding.go 390 -> 383 (the goalOrchestratorRole const).
- Behavior changes (named):
  1. `operator directive`, `operator poll` and `operator watch` are removed. The operator poll lease and loop no longer exist.
  2. `operator status --json` drops operator_delivery, operator_loop, claimed, conflict, watch and notifications. operator.poll keeps required, owner, unread and open_gates, and drops cursor and open_blockers. The human view prints "# unread: N" instead of the loop line.
  3. Operator attention notifications (desktop and command sinks, throttle and dedup state, self-operator visibility notifications) are removed. The NOC renders attention.
  4. team init's --dry-run JSON drops operator_delivery, and the human dry run drops its "# operator-delivery:" line.
  5. The aged-gate status warning reads "durable AMQ for handle X requires visible escalation". It no longer names the poll or wake delivery mode.
  6. The kept-dead bootstrap prompt shows "Operator interaction mode: <mode>." instead of the operator-delivery contract lines.
- Tests retired (subject removed: notify, poll lease, watch loop, directive, operator delivery). This is the sweep log; the restored ones are re-added below:
  - authorization_envelope_test.go: TestTypedGateAttentionParityAcrossOperatorModes, TestTypedGateNewerExactBoundAnswerReplacesInvalidAnswerBarrier
  - compound_release_attention_test.go: TestSelfOperatorVisibilityReusesFilteredSingleCapture, TestCompoundReleaseChildrenStableKeysAndEvidenceLossTombstone, TestCompoundReleaseStatusPreservesPhysicalCursorAndReadBacklog, TestCompoundReleaseNotifyAndStatusShareProjectedChildren
  - gate_close_test.go: TestTerminalGateSuppressedFromOperatorStatusAndAgedWarnings
  - gate_test.go: TestSafeTypedGateRaiseAnswerVerifyRoundTrip
  - namespace_migration_test.go: TestNamespaceWriterUnchangedContextProceedsAfterPreAdmissionPause; helpers: newNamespaceMigrationFixtureProfiles, newNamespaceMigrationFixture
  - notify_multiprocess_test.go: TestNotificationDeliveryProcessHelper, TestPersistedReservationDeduplicatesAcrossProcesses
  - notify_self_operator_test.go: TestSelfOperatorEventSelectClearReopen, TestScopedSelfOperatorTombstonesModeFlipAndRemoval
  - operator_attention_test.go: TestNotificationReservationCoversConfiguredSinkTimeout, TestNotifyEmitsAndDedupesOperatorGate, TestNotifyEscalatesOperatorGateAgeDespiteThrottle, TestNotifyTerminalGateTombstonesPersistedNotification, TestNotifyStaleTerminalDoesNotTombstoneReraisedGeneration, TestNotifyConflictingGateCopiesStayActive, TestNotifyRepairedRequestWithBoundCloseStaysActive, TestNotifyDegradedScanNeverTombstonesPriorGate, TestNotifyRenotifiesAfterThreshold, TestNotifyUsesCustomOperatorHandle, TestNotifyProfileScopesOperatorAttentionAndStateKeys, TestNotifyIgnoresP2PProseOnlyNeedsYou, TestNotifyNoOperatorReportsDisabled
  - operator_delivery_test.go: TestOperatorDeliveryModeContracts, TestOperatorDeliveryAtRootPinsSeparateTerminalPollIdentity
  - operator_notifications_config_test.go: TestOperatorDeliveryShowsAttentionPolicyWithoutSecrets
  - operator_test.go: TestOperatorStatusJSONReportsPollContractAndAttention, TestOperatorStatusDisabledProfileIsUnconfigured, TestOperatorPollReadOnlyJSONUsesOperatorLoopContract, TestOperatorStatusKeepsGateOpenAfterStatusUpdate, TestOperatorStatusClosesGateAfterOperatorAnswer, TestOperatorPollClaimsLeaseAndCursorHighWater, TestOperatorStatusReportsExistingLeaseReadOnly, TestOperatorStatusReportsExpiredLeaseStale, TestOperatorPollRefusesActiveForeignLease, TestOperatorPollJSONConflictEmitsHolderAndRuntimeError, TestOperatorWatchOnceClaimsLeaseCompactJSON, TestOperatorWatchSuccessfulTickIsSoleNotificationPump, TestOperatorWatchOnceConflictReturnsTypedError, TestOperatorWatchContinuesAfterConflictAndReclaimsExpiredLease, TestOperatorPollClaimsExpiredForeignLease, TestOperatorPollForceStealsLiveLeaseWritesAudit
  - removed_command_test_support_test.go: (no tests); helpers: executeNotifyJSONForTest, renderNotifyForTest, executeNotifyForTest
  Also retired by hand: operator_test.go TestOperatorDirectiveSendsTodoOnCanonicalThread, TestRunOperatorWatchRejectsIntervalTooCloseToTTL, TestRunOperatorPollForceRequiresReason; the directive leg of amq_update_suppression_integration_test.go TestAMQNoisyFakeHighLevelCommands; the directive row of TestOperatorCommandsRefuseOperatorTarget; the operator directive, poll and watch rows of TestScopedShortFlagAliasesRegistered.
- Tests restored (subject kept), with test-side operatorGatesOpenForTest and operatorBacklogForTest reading operator.poll:
  - authorization_envelope_test.go TestTypedGateNewerExactBoundAnswerReplacesInvalidAnswerBarrier
  - gate_close_test.go TestTerminalGateSuppressedFromOperatorStatusAndAgedWarnings
  - gate_test.go TestSafeTypedGateRaiseAnswerVerifyRoundTrip
  - compound_release_attention_test.go TestCompoundReleaseStatusPreservesPhysicalCursorAndReadBacklog, as ...PreservesReadBacklog without the cursor
  - operator_test.go TestOperatorStatusJSONReportsPollContractAndAttention, as ...ReportsAttentionAndUnreadCounts; TestOperatorStatusDisabledProfileIsUnconfigured; TestOperatorStatusKeepsGateOpenAfterStatusUpdate; TestOperatorStatusClosesGateAfterOperatorAnswer
  - namespace_migration_test.go: the migration fixtures, without the notify state seed, and TestNamespaceWriterUnchangedContextProceedsAfterPreAdmissionPause
  - Edited: help_test.go (operator help and the unknown-subcommand list follow operatorSubcommands; the removed names must be absent), bootstrap_test.go (two prompt expectations follow the template), status_test.go TestStatusWarnsAgedOperatorGate ("durable AMQ").
- Remaining over target, with C17 reasons: operator.go 364 (runOperatorAnswer and the release-owned answer envelope and invocation boundary, which are live release-gate flow, C21); operator_attention.go 57; operator_self_approve.go 283, operator_identity.go 53 and team_operator.go 85 (self-operator, pending the ruling requested with this commit); lead_identity.go 116 (delete-fate; its last callers are operator_identity's pane-verified actor).
- Lines 52,242 -> 50,426; holding 390 -> 383; tests listed 1,283 -> 1,242.
