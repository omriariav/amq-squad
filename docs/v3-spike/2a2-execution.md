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
| 11 | C15 doctor: framework, AMQ version/launchapi/floor/pin/ops, version skew, team config, worktrees | 48,832 | 383 | 1,203 |
| 12 | Self-operator cut (lead ruling 6): self-approve, self_operator mode, pane-verified actor, lead_identity.go | 47,613 | 383 | 1,168 |
| 13 | C7 namespace: conflict scanner and migration guard cut; writer admission kept | 46,458 | 241 | 1,159 |
| 14 | C12 bootstrap: bootstrapack package deleted, preflight.go, bootstrap.go kept-dead closure, sibling workstreams | 45,946 | 241 | 1,154 |
| 15 | C12 follow-up (lead ruling 7): bootstrap.go target raised to its extract-closure floor; memberHandle moved | 45,947 | 241 | 1,154 |
| 16 | C26 part 1: pathnorm.go dead set helpers and comments, target_root.go deleted | 45,642 | 241 | 1,149 |
| 17 | C26 part 2 (lead ruling 8): live launch-record context source cut | 45,460 | 241 | 1,143 |
| 18 | compoundrelease review (lead ruling 9): writer kept-dead, no code change | 45,460 | 241 | 1,143 |
| 19 | C25 execution_mode.go review (lead ruling 10): unreached contract parts cut, target set to the floor | 45,147 | 241 | 1,142 |
| 20 | state review (collapse.go) and state shrink rows: board projections, wake, tmux, goal and freshness-source cut | 44,509 | 241 | 1,136 |
| 21 | holding file to zero: every item moved to its 3.0 home or inlined; v3_holding.go deleted | 44,443 | 0 | 1,136 |
| 22 | stale command names in kept text; registry-vs-text guards extended | 44,414 | 0 | 1,138 |
| 23 | coverage debit from 2a-1 settled (log only) | 44,414 | 0 | 1,138 |
| 24 | launch/record.go (watch list): minimal launch record, test-only helpers cut | 44,251 | 0 | 1,133 |
| 25 | internal/team (watch list): test-only wrappers, clone.go and goal supervision cut; notes shortened | 44,026 | 0 | 1,126 |
| 26 | internal/task: default-profile wrappers and the dead dispatch-delivery lifecycle cut | 43,573 | 0 | 1,115 |
| 27 | procinfo TTY and child index (manifest "PID liveness only"); remaining dead helpers in activity and amq | 43,304 | 0 | 1,115 |
| 28 | doc notes shortened in the plain-overage rows (no code change) | 43,127 | 0 | 1,115 |
| 29 | end-of-2a-2 ledger in three classes (log only) | 43,127 | 0 | 1,115 |
| 30 | closing commit (lead rulings 11 and 12): class statuses in the manifest, manifest-check --final, kept-dead floors | 42,996 | 0 | 1,115 |

Overage budget (lead ruling 5): with every remaining row on target the total lands near 32,700. The band top is 36,410, so the whole phase tolerates about 3,700 lines of combined over-target. Every per-row deferral draws from that budget. "Over-target now" sums (wc -l minus target_lines, floored at 0) over present non-keep, non-replace rows, using scratchpad overage.py. "Deferred so far" sums the rows the log records as deferred, with reasons.

| after commit | over-target now (all rows) | deferred so far |
| --- | --- | --- |
| 8 | 15,688 | 3,583 (see "Deferral ledger") |
| 9 | 15,197 | 3,583 |
| 10 | 13,406 | 3,583 |
| 11 | 12,051 | 3,678 (C13's amq_root_authority row, 171, closed; doctor.go 266 added) |
| 12 | 11,148 | 3,496 (verify_action.go 231 -> 49: the self-operator merge branch is cut) |
| 13 | 10,209 | 3,496 |
| 14 | 9,700 | 3,584 (bootstrap.go 180 added; the C27 group drops 515 -> 423 with the workstream cut) |
| 15 | 9,520 | 3,404 (bootstrap.go row closed by the ruling 7 target decision) |
| 16 | 9,330 | 3,404 |
| 17 | 9,148 | 3,671 (context_resolution.go 267 added as a 2b-design deferral) |
| 18 | 9,148 | 3,843 (compoundrelease store.go 130 and reconcile.go 42 added as phase-3-design deferrals) |
| 19 | 8,654 | 3,843 |
| 20 | 8,144 | 3,843 (collapse.go reviewed; its 180 over target is live triage, see commit 20) |
| 21 | 8,285 | 3,951 (ledger rows re-measured after the holding items landed: team.go, context_resolution.go and the C23 group) |
| 22 | 8,286 | 3,951 |
| 23 | 8,286 | 3,951 |
| 24 | 8,123 | 3,951 |
| 25 | 7,917 | 4,636 (internal/team/team.go 685 added as a 2b-design deferral) |
| 26 | 7,595 | 4,636 |
| 27 | 7,375 | 4,636 |
| 28 | 7,198 | 4,636 |
| 29 | 7,198 | see "End-of-2a-2 ledger in three classes": class 1 2,919 (+552 candidates), class 2 172, class 3 3,555 |
| 30 | 6,792 | class 1 3,471 (deferred-2b), class 2 172 (deferred-3), class 3 3,149 (accepted) |

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

6. (after commit 11) Self-operator, option (a): cut operator self-approve, the self-operator mode (`team operator self pause|resume`, team.EffectiveSelfOperator), the pane-verified actor (operator_identity.go's TMUX_PANE and launch-record check), lead_identity.go, and the self-operator branches in verify_action.go, operator_attention.go and status, as one named behavior change. `operator answer` and `operator status` stay. Phase 3 item: "a local owner answer without Buzz, if ever wanted, is a phase 3 design decision; it will not be pane identity". Then C15, C7, C12.

7. (after commit 14) bootstrap.go: extract content is not cut; the named extracts are the 2b ancestors (ruling 2), and the 300 target predates them, as with liveness.go. The target is raised to the measured floor of the extract closure (code plus blank lines, plus a comment budget), recorded with extract names and line ranges, and anything outside the closure is trimmed. It is a target decision, not a deferral; the effect on the band is stated with it.

8. (after commit 16) context_resolution.go, option (a): cut the live launch-record context source (projectFromInjectedLaunch, matchingLiveLaunchContexts, the contextProcess* PID/TTY/start-time probes and their root matching) as a named behavior change. Context comes from flags, the injected AM_* environment, the project .amqrc and the defaults (ruling 6, plan R52: the launch sets the complete identity; nothing is discovered at runtime). Keep the .amqrc source and the candidate diagnostics until squad.yaml replaces them in 2b; record the remaining over-target as a 2b-design deferral so the end checkpoint can tell design-bound deferrals from plain overage.

9. (after commit 17) compoundrelease: option (C). The plan keeps compoundrelease for the phase 3 release gate flow, which creates releases, so the writer is that flow's ancestor, not dead code: store.go, reconcile.go and transition.go writer paths stay kept-dead, listed by function with "phase 3 release flow" as consumer, and the cli reader tests stay. Review-fate rows over target record the difference as a phase-3-design deferral. Ruling 3 applies to dead test-only code with no named consumer.

10. (after commit 18) execution_mode.go, option (b): the execution and actor contract has live callers (the dispatch and task refusal for a planner lead, team init's flags, the plan JSON block, the rules line) and sits inside the ruling 7 bootstrap closure; the manifest extract predates both. Keep those paths; cut what nothing reaches (release readiness, merge authority, independent review, the invariant evidence, versionCompatibility, the visible-members helpers). Raise the target to the measured floor of what remains as a target decision, with the caller list, line ranges and band effect.

11. (after commit 29) launch/record.go ("seat-exec writes it") and doctor.go ("probe per plan 16.2") are 2b-design rows, class 1. Ruling 7's closure-floor rule covers the three kept-dead extract rows (wait_posture.go, global_noc.go, init.go): each target goes to its measured closure floor, with extract names and line ranges.
12. (after commit 29) Accept class 3 at 3,149 and stop cutting live code in 2a-2: what remains in task.go, operator.go, role_draft.go, the state rows, amq_send.go, task_completion_evidence.go and verify.go is live behavior that 2b redesigns (task store, operator session, routing). Closing commit: class 1 rows status deferred-2b and class 2 rows deferred-3 with the quoted reason; manifest-check --final excludes them like deferred-replace and prints each class total; the end-of-2a-2 decision is written in the log.

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
| internal/cli/bootstrap.go | buildBootstrapPrompt, sanitizeBootstrapContext (+ sanitizePromptValue, promptText), bootstrapContextFor (+ bootstrapActorExecution, bootstrapOrchestration, bootstrapCurrentTeamWithRoster/ForTeam, routingSessionForMember, sameLaunchTarget), routeCommandFor (+ routeExplainCommand, routeExplainResult, pinBootstrapAMQSendArgv, projectIdentity, projectIdentityForCWD, samePath, findProjectName), bootstrap.md | 470 (the whole file) | the 2b context verb |
| internal/compoundrelease/store.go | Open, Store.Create, Store.StartPublishing, Store.Activate, Store.activate, Store.PrepareSuccessor, Store.completeSuccessor, Store.validateSpecScope, newGenerationRecord, adoptedChildRecords, validatePublishedChildrenForActivation, Store.writePrepared, Store.writeActive, Store.writeGeneration, Store.writePointer, Store.withLock, Store.writeImmutable, immutableTempName, Store.immutableTemps, Store.mutableTemps, Store.classifiedTemps, Store.readLinkedLeaf, validateImmutableInfo, Store.writeMutable, mutableTempName, Store.syncDirectory, fileOwner | (part of 1,515) | phase 3 release flow |
| internal/compoundrelease/reconcile.go | Store.Reconcile, Store.readReconcileCurrent, assessReleaseChild, Store.invokeReconciledChild, Store.activateReconciled, Store.terminalReconcileConflict, allReleaseChildrenPublished, classifyReleaseMessageGroups, looseReleaseMessageTargets, releaseMessageIDs | (part of 494) | phase 3 release flow |
| internal/compoundrelease/transition.go | definitelyUninvokedEvidence, Store.ClaimChildSend, Store.rollbackChildSend, Store.AdoptChildPublication, Store.TerminalizeChildConflict, persistedConflictEquals, Store.readPublishingLifecycleForTransition, childClaimToken | (part of 257) | phase 3 release flow |
| internal/compoundrelease/series.go | InspectSessionSeries | (part of 500) | phase 3 release flow (keep-fate row, not cut) |
| internal/cli/namespace_migration_guard.go | acquireRevalidatedAMQWriter (3), validateReResolvedAMQContext (11) | 14 | AMQ-context writers under admission |
| internal/cli/team_launch.go | teamLaunchOptions (fields the launchapi ancestor reads), resolveTeamLaunchAMQEnv, buildTeamPreflights | 89 (whole file) | 2b launch |

team_launch_launchapi.go (lead ruling, commit 2): 2b must give the library a real amq executable path (plan R51, upstream #1159) before this backend can launch from the amq-squad binary. The kept-dead closure also holds helpers in files that other commits shrink: memberResolvedModel, resolveModelForLaunch and nativeModelArg (model_defaults.go), memberEffectiveModel (team.go), agentLaunchPreflight (preflight.go), and the composeBinaryArgs / launchDefaultChildArgsWithTrust family (agent_args.go, agent_defaults.go).

resolveAMQEnvForLaunch, normalizedAgentBinary and stringListFlag (launch.go) are live; other kept code calls them. restore.go's shellQuote and shellCommand are live.

## Notes for 2b and phase 3 (from rulings)

- 2b: tool policy comes from squad.yaml at launch (replaces validateMemberOverlayPaths). (ruling 1)
- 2b: the library needs a real amq executable path (plan R51, upstream #1159) before the kept-dead launchapi backend can launch from the amq-squad binary. (ruling 4)
- 2b: squad.yaml replaces the .amqrc source and the context candidate diagnostics. (ruling 8)
- 2b: squad.yaml will own the execution-mode and lead-mode fields, so team init's --mode, --lead-mode, --control-root, --target-project-root (and the now-inert --target-contract) become config then. (ruling 10)
- Phase 3: a local owner answer without Buzz, if ever wanted, is a phase 3 design decision; it will not be pane identity. (ruling 6)
- Phase 3: the compound release writer (store, reconcile, transition) is the release gate flow's ancestor. (ruling 9)

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
| internal/cli/team.go | 1,582 (1,527 until commit 21) | team init/rules/sync rewrite around squad.yaml (2b); commit 21 moved peelProjectFlag, runInProject and memberCWDOverride here from the holding file | 2, 21 |
| internal/cli/verify_action.go | 49 (231 until commit 12) | guard verb ancestors (2b); the self-operator merge branch is cut in commit 12 | 5, 12 |
| internal/cli/amq_root_authority.go | 0 (deleted in commit 11) | closed: C15 cut --fix-amq-root and the root authority check | 5 |
| internal/cli/doctor.go | 266 | AMQ version/launchapi/floor/pin/ops, version skew, team config and worktree checks are all kept by the manifest; the 600 figure assumes the 2b probe (plan 16.2) | 11 |
| internal/cli/verify.go | 111 | live merge/release evidence (2b merge train) | 5 |
| internal/cli/verify_authorization.go, verify_rebind.go | 63 | live signed auth and rebind proof | 5 |
| internal/cli/amq.go, amq_send.go, amq_env.go, wait_posture.go | 512 | callers' helpers and the live send boundary; wait-posture guard redesign (2b) | 6 |
| internal/cli/role_draft.go, roles.go, agent_catalog.go, model_defaults.go | 478 (453 until commit 21; compactNativeValue moved in) | 3.0 role format and squad.yaml (2b); live catalog overlay; kept-dead launch closure | 7 |
| internal/cli/workstream.go, briefs_seed.go, team_rules_draft.go, drafter/run.go, drafter/config.go | 423 (515 until commit 14) | live seed sources, team-rules drafting (2b), live drafter; workstream.go keeps the session resolver and the deprecated team.json pin shim (C27 second pass) | 8, 14 |
| internal/cli/bootstrap.go | 0 (closed in commit 15) | target raised to the extract-closure floor under ruling 7 | 14, 15 |
| internal/cli/context_resolution.go | 295 (267 until commit 21; resolveExistingTeamProfile and resolveProfileFlag moved in) | **2b design (squad.yaml)**: the .amqrc source and the candidate diagnostics stay until squad.yaml replaces them (ruling 8; the S6 seats and doctor used the candidates line to diagnose the AM_SESSION gap) | 17 |
| internal/compoundrelease/store.go, reconcile.go | 172 (130 + 42) | **phase 3 design (release flow)**: the writer paths are the ancestor the phase 3 release gate flow builds on (ruling 9); transition.go is keep-fate and also holds writer paths | 18 |
| internal/team/team.go | 685 | **2b design (squad.yaml)**: the manifest row itself says "rewrite around squad.yaml in 2b; highest fan-in, do not delete in 2a"; what remains is the live profile model, validation and the operator contract | 25 |
| total | 4,636 | | |

These are the helpers' floors as they reported them. The watch-list rows (amq.go, role_draft.go, cli team.go) get a second pass before the end checkpoint, and every row here is rechecked against the budget then.

## End-of-2a-2 ledger in three classes (lead instruction, after commit 25)

Measured after commit 28. "Over" is wc -l minus target_lines for present shrink and review rows (keep and replace rows excluded), so this table replaces the per-commit "Deferral ledger" above for the end checkpoint. All over-target rows: 7198 lines.

### Class 1: 2b-design deferrals (the manifest row says the row is rewritten around squad.yaml in 2b)

| row | over | lines / target | manifest reason (quoted) |
| --- | --- | --- | --- |
| internal/cli/team.go | 1581 | 1931 / 350 | "rewrite around squad.yaml in 2b" |
| internal/team/team.go | 685 | 1316 / 631 | "rewrite around squad.yaml in 2b; highest fan-in, do not delete in 2a" |
| internal/cli/context_resolution.go | 295 | 846 / 551 | "context resolution kept, simplified by squad.yaml" |
| internal/cli/team_rules.go | 212 | 512 / 300 | "rewrite around squad.yaml in 2b" |
| internal/cli/team_rules_draft.go | 146 | 296 / 150 | "rewrite around squad.yaml in 2b" |
| total | 2919 | | |

Candidates for class 1 that cite a 2b component but not a squad.yaml rewrite (lead to confirm):

| row | over | lines / target | manifest reason (quoted) |
| --- | --- | --- | --- |
| internal/launch/record.go | 286 | 536 / 250 | "minimal launch record; seat-exec writes it" |
| internal/cli/doctor.go | 266 | 866 / 600 | "probe per plan 16.2" |
| total | 552 | | |

### Class 2: phase-3-design deferrals

| row | over | lines / target | manifest reason (quoted) |
| --- | --- | --- | --- |
| internal/compoundrelease/store.go | 130 | 1515 / 1385 | "review for pane references" |
| internal/compoundrelease/reconcile.go | 42 | 494 / 452 | "review for pane references" |
| total | 172 | | |

The compoundrelease writer is the phase 3 release flow ancestor (ruling 9); the reason quoted is the manifest's review reason, and the design consumer is ruling 9's.

### Class 3: plain overage (no design reason)

Kept-dead extract rows, which could take a ruling 7 style target decision (the extract and its closure are the 2b ancestor):

| row | over | lines / target | manifest reason (quoted) |
| --- | --- | --- | --- |
| internal/cli/wait_posture.go | 179 | 279 / 100 | "up/down/resume replaced by launch API; down returns thin in 2b" (extract: "guardOwnedWait (:67), durableSendWaitPosture (:170)") |
| internal/cli/global_noc.go | 115 | 265 / 150 | "fleet board belongs to amq-noc" (extract: "fleet registry read/write (:117-265) kept per 4.4 default") |
| internal/cli/init.go | 112 | 262 / 150 | "legacy wizard/start scaffolding" (extract: "rules pointer-stub digest flow (:110 rules.Plan, :266 freshPlans, :92)") |
| total | 406 | | |

The rest:

| row | over | lines / target | manifest reason (quoted) |
| --- | --- | --- | --- |
| internal/cli/task.go | 496 | 1296 / 800 | "keep lifecycle CLI, drop pane nudges" |
| internal/cli/role_draft.go | 344 | 494 / 150 | "provider-argument assembly is R4" |
| internal/cli/operator.go | 342 | 942 / 600 | "keep gate raise/answer and attention projection" |
| internal/state/message.go | 187 | 582 / 395 | "classifier base; line-by-line review" |
| internal/state/collapse.go | 180 | 815 / 635 | "line-by-line review (largest state file)" |
| internal/cli/amq_send.go | 164 | 314 / 150 | "keep context resolution, send, env pinning" |
| internal/cli/task_completion_evidence.go | 131 | 381 / 250 | "keep lifecycle CLI, drop pane nudges" |
| internal/cli/verify.go | 105 | 605 / 500 | "guard ancestor; keep decision flow and signed auth" |
| internal/cli/amq_env.go | 89 | 239 / 150 | "keep context resolution, send, env pinning" |
| internal/cli/workstream.go | 83 | 183 / 100 | "brief drafting kept" |
| internal/drafter/run.go | 75 | 324 / 249 | "brief drafting as off-path verb" |
| internal/cli/task_selection.go | 66 | 216 / 150 | "keep lifecycle CLI, drop pane nudges" |
| internal/cli/roles.go | 66 | 146 / 80 | "provider-argument assembly is R4" |
| internal/drafter/config.go | 58 | 259 / 201 | "brief drafting as off-path verb" |
| internal/cli/operator_attention.go | 55 | 455 / 400 | "keep gate raise/answer and attention projection" |
| internal/cli/model_defaults.go | 53 | 183 / 130 | "provider-argument assembly is R4" |
| internal/state/coordination.go | 47 | 449 / 402 | "classifier base; line-by-line review" |
| internal/cli/verify_action.go | 47 | 747 / 700 | "guard ancestor; keep decision flow and signed auth" |
| internal/state/external_evidence.go | 40 | 110 / 70 | "classifier base; line-by-line review" |
| internal/procinfo/procinfo.go | 40 | 111 / 71 | "PID liveness only; drop TTY/ChildrenIndex" |
| internal/task/reconcile.go | 39 | 239 / 200 | "five states and leases exist; pools are new" |
| internal/task/command_evidence.go | 37 | 225 / 188 | "five states and leases exist; pools are new" |
| internal/state/mailbox.go | 37 | 115 / 78 | "classifier base; line-by-line review" |
| internal/cli/verify_authorization.go | 37 | 337 / 300 | "guard ancestor; keep decision flow and signed auth" |
| internal/task/completion_reconcile.go | 36 | 233 / 197 | "five states and leases exist; pools are new" |
| internal/task/lifecycle_event.go | 35 | 216 / 181 | "five states and leases exist; pools are new" |
| internal/task/transaction.go | 33 | 216 / 183 | "five states and leases exist; pools are new" |
| internal/cli/briefs_seed.go | 33 | 203 / 170 | "brief drafting kept" |
| internal/procinfo/parse.go | 31 | 62 / 31 | "PID liveness only; drop TTY/ChildrenIndex" |
| internal/state/snapshot.go | 28 | 282 / 254 | "classifier base; line-by-line review" |
| internal/task/task.go | 26 | 702 / 676 | "five states and leases exist; pools are new" |
| internal/cli/verify_rebind.go | 26 | 126 / 100 | "guard ancestor; keep decision flow and signed auth" |
| internal/task/leadership.go | 21 | 152 / 131 | "five states and leases exist; pools are new" |
| internal/cli/amq.go | 17 | 417 / 400 | "keep context resolution, send, env pinning" |
| internal/cli/agent_catalog.go | 15 | 145 / 130 | "provider-argument assembly is R4" |
| internal/state/state.go | 12 | 130 / 118 | "classifier base; line-by-line review" |
| internal/activity/activity.go | 10 | 210 / 200 | "seat activity file feeds seat state; drop SymphonySnapshot" |
| internal/commandevidence/subject.go | 5 | 405 / 400 | "kept as guard target-digest helper (owner default applied)" |
| internal/procinfo/native_other.go | 3 | 13 / 10 | "PID liveness only; drop TTY/ChildrenIndex" |
| total | 3149 | | |

### Against the band and the budget

- Band: 29,790..36,410 around 33,100. manifest-check: current total 40696 excluding deferred-replace (2431 lines deferred): outside 29790..36410 (reported; enforced with --final).
- Overage budget after the target decisions of rulings 5, 7 and 10: about 3,350.
- Class 3 is 3149 lines (3555 with the three kept-dead rows, 552 more if record.go and doctor.go stay in class 3).
- Classes 1 and 2 (3091, or 3643 with the two candidates) are design-bound debt that 2b (squad.yaml, seat-exec, the probe) and phase 3 (release flow) remove by rewriting those rows.

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

### Commit 11: C15 doctor. Framework plus the AMQ, version, profile and worktree checks; tmux, marker, wake, roster, skill, all-profiles and fix-amq-root cut

- 3.0 shape (manifest C15 extract: "framework and version/launchapi/floor/pin/AMQ-ops checks; drop tmux, pane, marker, notification, goal, wake, roster checks"):
  - Kept: the AMQ version, launchapi compatibility, adoption floor, module pin, identity pin and AMQ ops checks, plus version skew, team config and the worktree checks (C22).
- Cut by hand:
  - the member tool profiles check, the team-rules roster check and the AMQ root authority check (roster checks);
  - the tmux and tmux extended-keys checks and the tmux show-options seam;
  - the CLAUDE.md/AGENTS.md marker integrity and pointer-sync checks;
  - the per-member wake checks; doctorWorkstream now resolves the workstream the worktree check needs;
  - the Codex skill cache and SKILL.md version checks, with their cache-root and frontmatter seams;
  - `--all-profiles` (executeDoctorAllProfiles), multi-profile health that squad.yaml replaces in 2b;
  - `--fix-amq-root` (doctorFixAMQRoot) and the "ignoring --session" notice;
  - the envelope's `versions` block.
  - deadcode then emptied amq_root_authority.go, so the file is deleted. equalStrings moves to role_draft.go, its last user; the C13 deferral row for this file closes.
  - team_launch_bootstrap.go is deleted; ensureLaunchDirectoryTracked's last caller was the root config reconciler.
  - version_alignment.go is deleted. defaultClaudeSkillCacheRoot moves to doctor.go and then goes with the skill checks, and status loses its unused VersionSources field.
  - semverMeetsStableFloor stays: the kept-dead launch.go floor check (validateSupportedAMQVersion) uses it.
- Files (non-test lines before -> after / target): doctor.go 1,759 -> 866 / 600 (trimmed; deferral in the ledger); amq_root_authority.go 371 -> deleted (fate shrink, target 200); team_launch_bootstrap.go 33 -> deleted; version_alignment.go 303 -> deleted (fate delete); status.go 884 -> 878; role_draft.go 482 -> 494 (equalStrings, 12 lines).
- Behavior changes (named):
  1. `doctor` no longer runs the tmux, tmux extended-keys, marker integrity, pointer sync, team-rules roster, member tool profile, per-member wake, AMQ root authority or skill cache/version checks.
  2. `doctor --all-profiles` and `doctor --fix-amq-root` are removed. The usage lists the kept checks.
  3. `doctor --json` drops the `versions` block.
  4. `doctor --session` no longer prints an "ignoring --session" notice; the session still selects the worktree plan.
- Tests retired (subject removed). This is the sweep log; the restored ones are re-added below:
  - amq_root_authority_test.go: TestAMQAuthorityHandlesIncludeEnabledOperator, TestReconcileAMQRootConfigCreatesExactAtomicRoster, TestReconcileAMQRootConfigPreservesCreationAndUnknownFields, TestReconcileAMQRootConfigMalformedFailsClosed, TestRepairAMQRootMailboxesPinsExactRootAndReportsWrites, TestRepairAMQRootMailboxesFailsOnCommandOrDoctorErrors, TestRepairTeamAMQRootAuthorityRepairsOnceAndLogsExactWrites, TestRepairTeamAMQRootAuthorityCollapsesLogWithoutVerbose
  - doctor_test.go: TestDoctorToolProfilesWarnsForMultipleNonLeadFullWorkers, TestDoctorCheckAMQRootAuthorityDetectsConfiglessRootWithoutMutation, TestDoctorCheckAMQRootAuthoritySkipsUncreatedRoot, TestExecuteDoctorFixAMQRootRepairsAndReports, TestExecuteDoctorAllProfilesChecksDefaultAndNamed, TestExecuteDoctorAllProfilesJSONNamedOnly, TestDoctorCheckTmuxExtendedKeys, TestExecuteDoctorWakeReuseClassifyMemberStatus, TestExecuteDoctorAMQOpsFailure, TestExecuteDoctorJSONCanonicalizesTeamHome, TestExecuteDoctorAMQVersionTooOld, TestExecuteDoctorAMQVersionMissing, TestExecuteDoctorAMQVersionUnparseable, TestExecuteDoctorAMQEnvCommandFails, TestExecuteDoctorAMQVersionAccepts0600AndNewerCanary, TestExecuteDoctorAMQVersionRejectsOlderThan0600, TestExecuteDoctorTeamConfigMissingWarn, TestExecuteDoctorDefaultMissingButNamedProfilesGuideProfile, TestExecuteDoctorNamedProfile, TestExecuteDoctorNamedProfileMarkersUseMemberCWD, TestExecuteDoctorTeamConfigCorruptFails, TestExecuteDoctorTmuxMissingFails, TestExecuteDoctorMarkerIntegrity, TestExecuteDoctorMarkerIntegrityMissingWarn, TestExecuteDoctorPointerSyncOKWhenApplied, TestExecuteDoctorPointerSyncWarnsOnDrift, TestExecuteDoctorTeamRulesRosterWarnsOnDrift, TestExecuteDoctorTeamRulesRosterOKWhenDescribed, TestExecuteDoctorPointerSyncNamedProfileOutsideHint, TestExecuteDoctorJSONFailKeepsStdoutPure; helpers: newDoctorExec
  - doctor_version_skew_test.go: TestDoctorCheckCodexSkillCache, TestDoctorCheckCodexSkillCacheWarnsOnSymlink, TestDoctorCheckSkillVersion, TestDoctorSkillVersionAppearsInFullDoctorJSON
  - liveness_test.go: TestInspectLivenessParityWithPaneProbeForLiveSeats, TestStatusRollupUnchangedForLegacyLaunches, TestInspectAbsentConflictsWithLivePaneProbeFailsClosed, TestInspectErrorFallsBackToExistingSignalsWithWarning, TestLaunchapiInspectMemoCollapsesRosterToOneCall, TestStatusSurfacesNotableLaunchapiObservation, TestStatusOmitsUnremarkableLaunchapiObservation
  - real_amq_root_authority_compatibility_test.go: TestRealAMQRootAuthorityCompatibility; helpers: realAMQRootAuthorityCompatibilityContract
  - skill_identity_marker_test.go: TestDoctorAcceptsTheRealShippedBundle
  - v228_contract_worktree_remnant_test.go: TestV228ContractWorktreeRemnantDoctorAgreesWithStatus
  - version_alignment_test.go: TestBuildVersionAlignmentReportsBinaryPluginAndSkillMatches, TestBuildVersionAlignmentWarnsOnPluginAndSkillMismatch, TestDoctorJSONIncludesVersionAlignment; helpers: alignedVersionSources
  - real_amq_compatibility_test.go: TestRealAMQCompatibility
  Also retired by hand: doctor_test.go TestRunDoctorRejectsAllProfilesWithProfile, TestRunDoctorFixAMQRootRejectsBroadOrJSONModes and TestExecuteDoctorWakeHandlesAMQEnvErrorPerMember; scoped_flags_test.go TestDoctorExplicitSessionPrintsIgnoreNotice. The emptied amq_root_authority_test.go, real_amq_root_authority_compatibility_test.go and version_alignment_test.go are deleted.
- Tests restored (subject kept):
  - doctor_test.go: the shared newDoctorExec fixture, without the root-authority, wake and skill seams. 13 tests: TestExecuteDoctorAMQOpsFailure, ...JSONCanonicalizesTeamHome, ...AMQVersionTooOld, ...AMQVersionMissing, ...AMQVersionUnparseable, ...AMQEnvCommandFails, ...AMQVersionAccepts0600AndNewerCanary, ...AMQVersionRejectsOlderThan0600, ...TeamConfigMissingWarn, ...DefaultMissingButNamedProfilesGuideProfile, ...NamedProfile, ...TeamConfigCorruptFails, ...JSONFailKeepsStdoutPure.
  - liveness_test.go: seven launchapi Inspect tests. They now use the test-side classifyMemberStatusForTest over the production classifyMemberStatusFromEntries; classifyMemberStatus went with the doctor wake check, its last caller.
  - real_amq_compatibility_test.go TestRealAMQCompatibility, without the root-authority leg.
  - help_test.go TestDoctorHelpDocumentsProfile follows the new usage.
- Lines 50,426 -> 48,832; holding 383; tests listed 1,242 -> 1,203.

### Commit 12: self-operator cut (lead ruling 6). Self-approve, the self_operator mode, the pane-verified actor and lead_identity.go

- One named behavior change: **only the owner answers typed gates** (plan 7.1). amq-squad 3.0 has no self-operator mode and no pane identity.
  1. `operator self-approve` is removed; `operator` lists `answer` and `status` only.
  2. The `self_operator` interaction mode is removed. `team init --operator-mode self_operator` and `team operator set --mode self_operator` fail with "removed in amq-squad 3.0"; `--self-operator-lead`, `--self-operator-allow` and `team operator set --self/--allow` are gone, as is `team operator self pause|resume`. `team operator set` only sets the mode and prints `operator mode=<mode>`.
  3. A persisted team config with `operator.self_operator` or `interaction_mode: self_operator` fails validation with the same message. OperatorConfig.SelfOperator is a json.RawMessage read only to reject it. SelfOperatorPolicy, EffectiveSelfOperator(View) and SelfOperatorPolicyHash are deleted.
  4. `verify action` approves a typed gate only from a valid human operator answer. A gate with no valid human answer is pending ("typed gate has no valid human operator answer"); the release-owned path now reports the same detail instead of "release-owned authorization requires a valid human operator answer". The allowSelf parameter and decideTypedVerifyActionWithAuthorityPolicy fold into decideTypedVerifyActionWithPolicy.
  5. The pane-verified actor is cut: operator_identity.go no longer reads TMUX_PANE or checks launch records. A verified actor is the roster member whose injected AM_ME matches and whose AM_ROOT is the namespace's exact root (defaultVerifiedOperatorActor, currentEnvRosterActor). lead_identity.go is deleted.
  6. The wait-posture guard identifies the lead by that injected identity. A caller without AM_ME, or whose AM_ME is not a roster member, is not guarded; a roster AM_ME whose AM_ROOT does not match fails closed. The refusal reads "refusing <cmd> before blocking as lead <handle>", and the audit record drops `pane_id`.
  7. status drops the `self_operator` block (human and JSON) and the `visibility_notifications` field, which only the self-operator mode set (it was false otherwise); the bootstrap prompt template (kept-dead) drops its self-operator block; the generated team-rules drop the self_operator policy rule.
  8. Policy-mutation ownership (rejectRunnableOperatorPolicyMutation) and incrementPolicyRevision go with the policy they guarded.
- Phase 3 item (ruling 6): a local owner answer without Buzz, if ever wanted, is a phase 3 design decision; it will not be pane identity.
- Kept unchanged: operator answer's receipt writer (operator_self_approve.go keeps selfApprovalStoreDir, selfApprovalReceiptPath and writeSelfApprovalReceipt; the on-disk `evidence/<profile>/<session>/self-operator/` receipt path stays for compatibility). operatorauth is keep-fate and keeps its `self_operator` approval source and ResolvePrecedence's selfHandle parameter (every caller passes ""); the 3.0 envelope work in phase 3 owns them.
- Files (non-test lines before -> after / target): operator_self_approve.go 583 -> 111 / 300 (shrunk); operator_identity.go 153 -> 52 / 100 (shrunk); lead_identity.go 116 -> deleted (fate delete); team_operator.go 207 -> 70 / 122 (shrunk); verify_action.go 931 -> 749 / 700 (trimmed; ledger row 49); verify.go 611 -> 607 / 500 (validateVerifyMergeEvidence, the zero-project wrapper only self-approve called); status.go 878 -> 853 / 900; team.go (cli) 1,871 -> 1,853 / 350 (ledger); bootstrap.go 592 -> 585 / 300 (C12 next); wait_posture.go 282 -> 279 / 100 (ledger); operator.go 964 -> 962 / 600; team_rules.go 515 -> 514 / 300; internal/team/team.go 1,640 -> 1,494 / 631; internal/team/clone.go 49 -> 47 / 19. gofmt dropped a trailing blank line in team_launch.go (89 -> 88), procinfo/native_darwin.go and state/flow.go.
- Interruption note: the first sweep pass ran cutdead over internal/team and internal/operatorauth and cut keep-fate wrappers that only tests use (team.Read/Write/Path, operatorauth functions, compoundrelease Reconcile). That breaks ruling 3, so internal/team, internal/operatorauth, internal/compoundrelease and every test were reset to HEAD, the team self-operator edits were re-applied by hand, and the sweep was re-run (119 -> 21 -> 0 errors).
- Tests retired (subject removed):
  - operator_self_approve_test.go (deleted): TestSelfApprovalReservationConcurrentSingleWinner, TestSelfApprovalReservationRejectsCorruptExpiredAndTransitionTamper, TestSelfApprovalSendingWithoutAnswerBlocksUntilExpiry, TestSelfApprovalReconcilesDuplicateMailboxCopiesByID, TestSelfApprovalRejectsConflictingMailboxCopiesByID, TestSelfApprovalRecoveryRejectsNoteMismatchAndKeepsReservation; helpers selfApprovalRecoveryFixture, selfApprovalRecoverySelected, newSelfApprovalRecoveryFixture, writeSelfApprovalTestMessage
  - self_operator_verify_test.go (deleted): TestVerifyActionSelfApprovalRequiresDistinctVerifiedMergeActor, TestTypedSelfAnswerRequiresExactRoutingSubjectBodyAndNote, TestVerifyActionSelfApprovalRevocationAndHumanPrecedence, TestSelfEvidencePathStaysInsideProject, TestSelfApprovalReceiptRejectsTrailingUnknownAndBindingTamper, TestSelfApprovalEvidenceRejectsStrictAndStaleBindings; helpers selfVerifyFixture, failureHasCode. TestVerifyActionLatestQuestionAndMalformedHumanAreBarriers is restored (below).
  - self_operator_pr2_test.go (deleted): TestTeamInitWritesExactSessionSelfOperatorPolicy
  - operator_identity_test.go (deleted): TestValidateOperatorLaunchRecordRejectsCurrentWorkingDirectoryMismatch, TestDefaultVerifiedCurrentPaneActorRejectsAmbiguousLiveRosterRecordsDeterministically, TestValidateOperatorLaunchRecordAuthorizationAndRuntimeBinding; helpers ambiguousCurrentPaneFixture, newAmbiguousCurrentPaneFixture
  - compound_release_self_approve_test.go: TestOperatorSelfApproveRejectsReleaseDomainBeforeSideEffects, TestOperatorSelfApproveReleaseOwnedPrecedesDisabledSelfPolicy, TestOperatorSelfApproveNamedExactRootRejectsBeforeSideEffects, TestOperatorSelfApproveOrdinaryDistinctSuppressedIDContinues; helpers assertReleaseSelfApproveRejectedWithoutSideEffects, installReleaseSelfApprovalPolicy, ordinarySelfApprovalQuestion, selfApproveArgs (the file keeps snapshotTestDirectory, which other tests use)
  - authorization_envelope_test.go: TestOperatorSelfApproveRejectsConflictingQuestionCopiesBeforeSend, TestSelfEligibleDefaultPushStillRequiresCurrentPolicyAndPreflight, TestTypedVerifierSelfAuthorityForBothMergePushActions, TestTypedVerifierSpawnRemainsHumanOnlyDespiteSelfAllowlist
  - team_operator_test.go: TestTeamOperatorResumeRejectsDisabledUnpausedSession, TestTeamOperatorSetPauseResumeRevisionAndModeHistory, TestTeamOperatorPolicyMutationRejectsVerifiedRunnableActors, TestTeamOperatorPolicyMutationRejectsActorFromDifferentSession, TestTeamOperatorMutationOwnershipIgnoresActionAuthorizationMismatches
  - team_no_session_pin_test.go: TestRunTeamInitNoSessionPinRejectsSelfOperator
  - verify_action_test.go: TestVerifyActionDecisionRequiresPrefixToken (classifyDecision, the legacy untyped decision parser that only this test still called, was cut with the self-operator block of verify_action.go)
  - wait_posture_test.go: TestGuardOwnedWaitDefaultCurrentPaneActorIdentity, TestGuardOwnedWaitFailsClosedOnAmbiguousCurrentPaneActor; helper installRealWaitPosturePane (replaced below)
  - internal/team self_operator_test.go: TestSelfOperatorValidationAndExactSession, TestSelfOperatorValidationRejectsLeadAllowlistAndUnknownFields, TestSelfOperatorValidationRejectsImmutableHumanOnlySpawn, TestSelfOperatorPolicyHashSecurityMatrix; helper selfOperatorTeam
- Tests restored or rewritten (subject kept):
  - wait_posture_test.go: TestGuardOwnedWaitUsesCollapsedCallerGateState, ...GateTerminalLifecycleIntegration, ...TimeoutBoundaryAndOverrideAudit, ...PreservesOutsideModeAndNonLead, ...NamedProfileUsesExactSelectedRoot, ...RealAuditWriteAndSyncFailuresRefuse, TestWriteWaitPostureAuditSerializesCompleteJSONLines and newWaitPostureFixture, with the actor seam's (actor, isMember, err) shape and no pane id. New TestGuardOwnedWaitDefaultEnvActorIdentity (helper installEnvWaitPostureTeam) covers the injected-identity rules in item 6.
  - wait_posture_pty_test.go TestRealPTYWaitPostureRefusesNamedGateBeforeTimeout: the tmux pane is only the PTY; the command carries the lead's AM_ME/AM_ROOT/AM_BASE_ROOT/AM_SESSION instead of a pane-bound launch record.
  - dispatch_test.go TestDispatchWaitPostureGuardsOwnedSendAndPreservesNoWait with amq_test.go installAMQWaitPostureRuntime.
  - verify_test.go TestVerifyMergeFailureCodes, TestReadVerifyMergeEvidenceFromStdinReader; verify_rebind_test.go TestVerifyMergeRejectsUnnecessaryRebinding (they call validateVerifyMergeEvidenceAtProject(e, "")).
  - authorization_envelope_test.go: TestAuthorizationCatalogDrivesHelpAndPositionalRejection (was ...HelpStatus...; status no longer prints the human-only kinds), TestAuthorizationReceiptPathsAreCollisionResistantAndImmutable (was ...ArtifactPaths...; reservation and evidence paths are gone), TestAuthorityDuplicateVariantsFailClosedAcrossSurfaces without its self-approve half.
  - verify_action_test.go TestVerifyActionLatestQuestionAndMalformedHumanAreBarriers, with a human-only fixture.
  - operator_test.go TestOperatorAnswerExplicitSessionIgnoresLegacyCrossSessionResidue, without the adoption-mode field.
  - internal/team: TestValidateOperatorInteractionModes (three modes; self_operator rejected), TestCloneRosterForSessionRestampsSessionAndDropsInstanceState (raw self_operator residue), TestReadSerializedSelfOperatorConfigIsRejectedAcrossSchemas (was ...RejectsSpawn...).
  - New: team_operator_test.go TestTeamOperatorSetRejectsRemovedSelfOperatorMode. Updated: help_test.go operator help and unknown-subcommand hint; team_test.go TestRunTeamInitRejectsOperatorConflicts.
- Stale text outside internal/ (README.md, README.html, docs/operator-cookbook.md, plugins/*/skills/cli/references/primitives.md) still describes self-approve and self_operator; recorded for the 3.0 docs pass with the commit 6 list.
- Lines 48,832 -> 47,613; holding 383; tests listed 1,203 -> 1,168.

### Commit 13: C7 namespace. Conflict scanner and migration guard cut; the writer admission extract kept

- 3.0 shape (manifest C7, "no v2 compat; migration goes"; extract: acquireNamespaceWriterAdmission, acquireRevalidatedContextWriter, acquireRevalidatedAMQWriter, validateReResolvedContext, namespaceAdmissionLocks; namespace_open_unix.go "safe open kept whole"):
  - namespace_migration_guard.go keeps the writer admission: a shared lock on `.amq-squad/namespace-admission/<profile>/<session>.lock`, opened without following links, then the caller's context is re-resolved under it and must be unchanged. The two revalidated writers share one generic helper. acquireRevalidatedAMQWriter (and validateReResolvedAMQContext) has no caller and stays as a named extract (kept-dead, 3 + 11 lines). validateReResolvedEndpoint stays for gate close, operator answer and task.
  - Cut: the profile and watcher admissions, the exclusive migration admission, the migration journal and phase types, activeNamespaceMigration, retiredNamespaceMigrationEndpoint, the generation scanner (namespaceEndpointGeneration), captureNamespaceEndpointIdentity and validateReResolvedEndpointIdentity, ensureNoNamespaceMigration(ForProfile). acquireRevalidatedContextWriter loses its profileWide parameter (every caller passed false).
  - namespace_conflict.go is deleted (fate delete): the default-profile shadow and named-profile collision scanner, the creation-collision check, the override and its audit record.
  - namespace_open_other.go is deleted (fate delete). Its only kept job was refusing admission off unix; that refusal now lives in the guard: openNamespaceAdmissionLock is nil unless namespace_open_unix.go's init installs openContainedNamespaceLock. Windows behavior is unchanged (writers refuse admission). The ancestor-swap test moves to namespace_open_unix_test.go (unix build tag), since its seam is unix-only.
  - Holding file: namespaceMigrationArtifact, namespaceMigrationInputEvidence, namespaceMigrationLiveness, namespaceMigrationPlan, dedupeMigrationStrings, canonicalMigrationProject, readNamespaceMigrationJournal and rejectMigrationSymlinkComponents are deleted with their last user (142 lines).
  - Keep-fate rows touched only to cut calls into deleted code: gate.go (ensureNoNamespaceMigration), gate_close.go (identity capture, conflict and migration checks), compound_release_attention.go (generation scan).
- Behavior changes (named):
  1. Namespace generation is always "none". Typed gate bindings, task completion correlations and the compound-release projection no longer read `.amq-squad/namespace-migrations/`; a gate bound under a v2 migrated generation no longer verifies (no v2 compat).
  2. `gate raise` and `gate close` no longer refuse while a v2 namespace migration is active.
  3. `dispatch`, `operator answer`, `gate close`, `task add` and `task reconcile` no longer refuse a collided namespace; `team init` no longer refuses a creation collision. `dispatch --override-namespace-conflict/--reason` and `operator answer --override-namespace-conflict/--namespace-reason` are removed, with the override audit record.
  4. `status` drops the `default_profile_shadowed` warning and the warnings' `conflicts` field.
  5. The re-resolution checks no longer compare the namespace generation (constant); project, profile, session, root, handle, base root and pin mode are still compared.
- Files (non-test lines before -> after / target): namespace_migration_guard.go 573 -> 133 / 133 (shrunk); namespace_conflict.go 435 -> deleted; namespace_open_other.go 18 -> deleted; namespace_open_unix.go 117 -> 117 / 117 (init added, one comment shortened); v3_holding.go 383 -> 241; status.go 853 -> 812 / 900; task.go 1,309 -> 1,285 / 800; operator.go 962 -> 946 / 600; dispatch.go 449 -> 440 / 450; gate_close.go 253 -> 240 / 228 (keep); gate.go 178 -> 175 / 160 (keep); compound_release_attention.go 301 -> 297 / 271 (keep); context_resolution.go 1,003 -> 1,000 / 551; operator_self_approve.go 111 -> 107; team.go 1,853 -> 1,850.
- Tests retired (subject removed):
  - profiles_test.go: TestUnprofiledDispatchRefusesBeforeWritingLegacyRootWhenNamedProfileOwnsSession, TestNamedProfileDispatchConflictIncludesConcreteRecoveryCommands, TestRootHasDurableStateIgnoresEmptyMailboxSkeletonDebris, TestNamedProfileDispatchNamespaceOverrideQueuesAndAudits, TestNamespaceOverrideRequiresReasonBeforeDispatch, TestUnprofiledDispatchRefusesMultipleNamedProfileOwners, TestDefaultProfileShadowRecoveryDoesNotAdvertiseInertOverride, TestNamedProfileConflictBlocksDirectRuntimeCommands
  - status_test.go: TestRunStatusDefaultProfileWarnsWhenNamedProfileHasSameSession
- Tests changed (subject kept): gate_test.go TestGateRaiseReResolutionDriftRefusesBeforeSend drops its generation case (root case kept); TestOperatorAnswerAndVerifyReResolutionDriftFailBeforeSecurityBoundary's answer leg drifts the root instead of the generation; compound_release_operator_answer_test.go TestOrdinaryTypedOperatorAnswerUsesNamedSelectedQuestionDespiteDefaultRootResidue no longer passes the override flags. TestNamespaceAdmissionContainedOpenRejectsAncestorSwapWithoutOutsideSideEffects moves to namespace_open_unix_test.go unchanged.
- Lines 47,613 -> 46,458; holding 383 -> 241; tests listed 1,168 -> 1,159.

### Commit 14: C12 bootstrap. bootstrapack deleted, preflight.go shrunk, the bootstrap prompt closure trimmed, sibling workstreams cut

- 3.0 shape (manifest C12, "context verb ancestor; cut pane/wake/goal"; bootstrapack owner default "delete: hooks inject every turn"):
  - internal/bootstrapack is deleted (fate delete). launch.Record drops `BootstrapExpectation` (`bootstrap_expectation` in launch.json); records that carry it still decode, the field is ignored. bootstrapContext drops the field too.
  - preflight.go: agentLaunchPreflight (kept-dead launchapi input) keeps its fields with one-line groups; launchWriterDead folds into presenceWriterIsKnownDead, which loses its unused root and handle parameters (liveness.go is its only caller); canonicalRootForMatch and relativeRootMatchesAbsolute fold into rootsMatch; comments shortened. Same results.
  - bootstrap.go (kept-dead, see the extracts table): the sibling workstream scan is cut (Workstreams field, its template block, and workstream.go's siblingWorkstreamSummaries, workstreamHandles, latestWorkstreamModTime, formatOptionalTime and bootstrapWorkstream). The team profile is read once in bootstrapContextFor instead of five times (bootstrapOperator and bootstrapExecution inline; the other helpers take the profile and its read error, keeping each section's own fallback). sanitizeBootstrapContext walks the context by reflection and passes every exported string through promptText, replacing the field-by-field list. Long comments are shortened.
- Ruling request: bootstrap.go stands at 480 against 300. What remains is the closure the four named extracts need (ruling 2). Reaching 300 means cutting extract content: the execution and actor-execution sections (about 60 lines plus template), or routeCommandFor's `amq route explain` path (routeExplainCommand, routeExplainResult, pinBootstrapAMQSendArgv, about 65 lines). Until the lead rules, the 180 lines are a ledger deferral.
- Behavior changes (named; the bootstrap prompt is not rendered by any live path in 3.0, so 1 and 2 only change the kept-dead ancestor):
  1. The bootstrap prompt no longer lists sibling AMQ workstreams.
  2. Bootstrap prompt sanitizing covers every exported string in the context (before: a named subset). promptText only drops control characters and backticks and trims, so the rendered values are unchanged for well-formed data.
  3. launch.json no longer writes or reads `bootstrap_expectation`.
- Files (non-test lines before -> after / target): bootstrapack/bootstrapack.go 246 -> deleted (fate delete); bootstrap.go 585 -> 480 / 300 (ledger 180); preflight.go 162 -> 97 / 100 (shrunk); workstream.go 290 -> 198 / 100 (C27 ledger row, now 423); launch/record.go 704 -> 700 / 250 (watch list; its dead Write/Scan/lock and tmux helpers are the launch-package pass); liveness.go 355 (call site only).
- Tests retired (subject removed):
  - internal/bootstrapack (package deleted): TestEvaluateGraceOverdueLegacyAndNotRequired, TestMarkerStrictPermissionsIdentityAndStaleLaunch, TestMarkerRejectsMalformedUnknownAndConcurrentWrites; helper testExpectation
  - launch_identity_stamp_test.go (deleted): TestNoProductionCodeBuildsExpectationLiterals (the AST guard against bootstrapack.Expectation literals, #572); helpers errorResultProvesNonNil, nonNilGuardedIdent
  - bootstrap_test.go: TestBootstrapPromptListsSiblingWorkstreams
- The remaining bootstrap tests (the prompt render, sanitizing, routes and roster) pass unchanged against the reflective sanitizer and the single profile read.
- Lines 46,458 -> 45,946; holding 241; tests listed 1,159 -> 1,154.

### Commit 15: C12 follow-up (lead ruling 7). bootstrap.go target raised to its extract-closure floor

- Decision under ruling 7: bootstrap.go's target goes from 300 to 470, the measured floor (the manifest row now carries target_lines 470, status shrunk). Evidence, measured on this commit's tree (scratchpad floor.py; line ranges are bootstrap.go lines):
  - buildBootstrapPrompt (:71-93) with the bootstrapContext and bootstrapTeamMember types (:29-70) and the embedded template.
  - sanitizeBootstrapContext (:94-98), with sanitizePromptValue (:99-121) and promptText (:122-140).
  - bootstrapContextFor (:141-211), with bootstrapActorExecution (:212-232), bootstrapOrchestration (:233-246), bootstrapCurrentTeamWithRoster and bootstrapCurrentTeamForTeam (:247-290), routingSessionForMember (:291-298) and sameLaunchTarget (:299-305).
  - routeCommandFor (:319-356), with projectIdentity and DisplayName (:306-318), routeExplainResult and routeExplainCommand (:357-398), pinBootstrapAMQSendArgv (:399-424), projectIdentityForCWD (:425-443), samePath (:444-447) and findProjectName (:448-470).
  - Total: 452 code and blank lines plus 18 comment lines, about 4 percent. The comments were already cut from 56 to 18 in commit 14, so the budget is the 18 lines as measured.
  - Outside the closure: memberHandle, which is live (dispatch, status, operator, wait posture and more). It moves to operator_identity.go beside the roster-actor code (52 -> 63 / 100), so bootstrap.go holds only the closure.
- Effect on the band: the target sum rises by 170. The band stays 29,790..36,410 around 33,100, so the over-target budget shrinks by 170, from about 3,700 to about 3,530. The running total (45,947) is still the real constraint against the band top.
- No behavior change. Lines 45,946 -> 45,947 (gofmt separator); holding 241; tests listed 1,154.

### Commit 16: C26 part 1. pathnorm.go and target_root.go

- pathnorm.go 270 -> 105 / 120 (shrunk). deadcode cut canonicalFilesystemPathIn, canonicalFilesystemPathsIn, absoluteFilesystemPaths, canonicalFilesystemPaths, normalizeFilesystemPathSet and dedupeSortedStrings: the set comparisons served tool_policy_sources, which went with team overlay (ruling 1). The 74-line design note and the per-function notes are rewritten as one short note on the record/compare split (#538, #539, #540); code unchanged.
- target_root.go 140 -> deleted (fate shrink, target 100). deadcode cut repoSlugFromGitConfig, repoSlugFromRemoteURL, classifyDraftTargetProjectRoot and resolveTargetProjectRootCandidates (the global-orchestrator target-root resolver, #290, whose callers were cut with the launch driver); unused then emptied the file of its four source constants.
- context_resolution.go (1,000 / 551) is not touched here; a ruling on the launch-record context source is requested (see the note to the lead) before part 2.
- No behavior change on a live path.
- Tests retired (subject removed): pathnorm_test.go TestRelativePathSetAnchorsToProjectNotProcessCWD, TestPathSetComparisonIsOrderAndRepresentationIndependent; target_root_test.go (emptied, deleted) TestRepoSlugFromRemoteURL, TestResolveTargetProjectRootCandidatesGitRemoteMatch, TestClassifyDraftTargetProjectRoot, helper writeGitConfig.
- Tests restored (subject kept): pathnorm_test.go TestCanonicalPathUsesOnDiskCaseOnCaseInsensitiveFilesystem and TestCanonicalPathResolvesSymlinkPrefixForMissingLeaf, without their set-helper assertions.
- Lines 45,947 -> 45,642; holding 241; tests listed 1,154 -> 1,149.

### Commit 17: C26 part 2 (lead ruling 8). The live launch-record context source is cut

- Named behavior change: **context resolution no longer discovers anything at runtime**. Profile, session, handle, root and base root come from explicit flags, the injected AM_* environment, the project .amqrc and the documented defaults, in that precedence.
  1. The `live_launch_record` candidate source is gone: no scan of the project's live launch.json records (matchingLiveLaunchContexts), no PID, TTY or start-time probes (contextScanLaunchEntries, contextPIDAlive, contextProcessMatch, contextProcessTTY, contextProcessStartTime, contextLaunchRecordRuntimeLive, contextLaunchRuntimeProbe, launchRecordMatchesProject, launchContext).
  2. The project directory is no longer taken from the launch record that AM_ROOT/AM_ME point at (projectFromInjectedLaunch); without --project it is the working directory, as before for non-seat shells.
  3. Root and base-root matching no longer accept a root because a live launch record carries it; an injected root without a profile must sit under the project's .amqrc root.
  4. The ambiguous-profile remedy that listed "choose one live tuple" doctor commands, and the multi-launch handle tolerance (selectOptionalHandleCandidate), went with the source; handle selection is selectContextCandidate like every other field.
  5. Candidate diagnostics stay; they list four sources now (explicit_flags, injected_environment, project_amqrc, documented_defaults).
- Files (non-test lines before -> after / target): context_resolution.go 1,000 -> 818 / 551 (trimmed; 267 in the ledger as a 2b-design deferral, ruling 8).
- Tests retired (subject removed), context_resolution_test.go: TestResolveCanonicalContextAmbiguousLaunchesReportEveryProvenance, TestResolveCanonicalContextSkipSessionResolutionAvoidsRootAmbiguity (gh#722: two runtime-live records tying on root), TestStoppedAndPIDReusedLaunchRecordsDoNotWinContext, TestLegacyLaunchRecordsWithoutStoppedAtUseLivenessProbe, TestContextRejectsSameBinaryPIDReuseWithSameOrUnknownTTY, TestResolveCanonicalContextSharedTupleDoesNotRequireHandle.
- Tests restored (subject kept): retained_fixture_support_test.go isolateCanonicalContextTest without the launch seams (and without TMUX_PANE); context_resolution_test.go TestExplicitSessionSwitchCarriesProfileButNotOldTupleIdentity, TestMatchingLowerTupleContributesHandleAndExactRoot, TestExplicitRootOverridesMalformedInjectedIdentityWithWarning, TestExplicitExternalRootWinsForNamedProfile, TestExplicitProjectOutranksForeignInjectedRoot, TestResolveAMQContextUsesCanonicalCustomRoot unchanged; TestResolveCanonicalContextPrecedenceMatrix and TestOrdinaryCommandsDoNotSpliceConflictingTuples without their live-launch legs.
- Hygiene: import blocks that earlier restores left ungrouped are regrouped (goimports style) in eight test files; no code change.
- Lines 45,642 -> 45,460; holding 241; tests listed 1,149 -> 1,143.

### Commit 18: compoundrelease review rows (lead ruling 9). The writer stays as the phase 3 ancestor

- Reviewed: internal/compoundrelease/store.go (review, 1,515 / 1,385), reconcile.go (review, 494 / 452), and, under ruling 3's naming, transition.go (keep, 257 / 235). The manifest reason is "review for pane references": none of the three files reads a pane, TMUX variable or wake input.
- deadcode lists the release writer (create, publish, activate, successor, reconcile, child claim/adopt/terminalize, the immutable and mutable writers); only tests reach it, because no 3.0 verb creates a release yet. Under ruling 9 it is kept-dead with the phase 3 release gate flow as its consumer; the functions are listed in the kept-dead extracts table. series.go's InspectSessionSeries is in the same state (keep-fate row, not cut).
- Ledger: store.go 130 and reconcile.go 42 are phase-3-design deferrals (172). The manifest rows carry a review note.
- No code or test change. Lines 45,460; holding 241; tests listed 1,143.

### Commit 19: C25 execution_mode.go review (lead ruling 10). Unreached contract parts cut; target set to the measured floor

- Cut (nothing outside the contract read them): versionCompatibility and versionCompatibilityData; the release readiness block (releaseReadinessData, releaseReadinessGateData, releaseReadinessForExecution, requiresSoloJustification); merge authority (mergeAuthorityData, defaultMergeAuthority); independent review (independentReviewData, independentReviewDataForConfig, independentReviewGatePassed, independentReviewEvidencePresent, independentReviewEvidence, and leadExecutionData's IndependentReview field); the invariant evidence (executionInvariantError, invariantEvidence, visibleLeadInvariantDetail and the InvariantsEvaluated, InvariantOK and InvariantErrors fields); visibleMembersForExecutionMode and the VisibleTeamMembers field. executionContract and executionContractForTeam lose their runningVersion/targetContract/visible parameters; the five callers drop the version argument. faultRemedy is the named extract and stays (kept-dead).
- Behavior changes (named):
  1. The `execution` object in `team init --dry-run --json` (the team profile plan) no longer carries `version_compatibility`, `release_readiness`, `visible_team_members`, `invariants_evaluated`, `invariant_ok`, `invariant_errors`, or `lead_execution.independent_review`.
  2. `team init --target-contract` still persists target_contract, but nothing reads it any more (2b note).
  3. Unchanged: the dispatch and task dispatch-next refusal of implement/lifecycle intents for an actor whose contract denies implementation (planner lead), team init's --mode/--lead-mode/--control-root/--target-project-root, the generated rules' execution line, and the kept-dead bootstrap prompt's execution and actor-execution sections.
- Decision under ruling 10: execution_mode.go's target goes from 150 to 331, the measured floor after the cut (manifest target_lines 331, review row now at target). 322 code and blank lines, 9 comment lines. Callers and ranges (execution_mode.go lines):
  - Mode constants and faultRemedy (:13-33, faultRemedy kept-dead extract); leadExecutionData, executionModeData, actorExecutionData (:34-77): the team plan JSON (team.go teamProfilePlan.Execution), bootstrap.go, dispatch.go.
  - normalizeExecutionMode, normalizeLeadMode, leadModeForPersist, cwdOrEmpty, cleanRootOrDefault (:78-132): team init (team.go).
  - executionContract, defaultExecutionModeForTeam, effectiveTeamExecutionMode, executionContractForTeam (:133-208): dispatch.go, task.go (dispatch-next), team_rules.go, team.go (plan and summary), bootstrap.go.
  - actorExecutionContractForTeam, actorRosterMemberForTeam (:209-257): dispatch.go, task.go, bootstrap.go.
  - applyLeadModeContract, plannerMutableActor, plannerMutableActorFromRoles (:258-300): the planner-lead narrowing behind the refusal.
  - applyLeadExecutionContract, leadExecutionDataForConfig (:301-331): the plan's lead_execution block.
- Effect on the band: the target sum rises by 181. The band stays 29,790..36,410, so the over-target budget shrinks by 181 more, to about 3,350 after rulings 5, 7 and 10. The running total is still the constraint.
- Tests retired (subject removed): status_test.go TestExecutionContractForTeamReleaseReadinessNotEvaluatedWithoutRuntimeInvariants, helper readinessGatesByCode.
- Tests restored (subject kept): team_test.go TestRunTeamInitDryRunJSONIncludesExecutionMode (without the visible-members check), TestRunTeamInitPlannerLeadModePersistsAndContractsDelegation (new arity).
- Lines 45,460 -> 45,147; holding 241; tests listed 1,143 -> 1,142.

### Commit 20: state package. collapse.go reviewed; board projections, wake, tmux, goal binding and the freshness source cut

- Cut (no consumer outside internal/state since the board and console went in commits 4 and 10):
  - Coordination.Edges and Coordination.Timeline with buildEdges, buildTimeline, phraseTransition, shortThread, the Edge and TimelineEvent types, and flow.go (FlowEdge, the console flow graph). flow.go is deleted.
  - The triage rollup: Coordination.Rollup, Session.Rollup, Snapshot.Rollup, TriageRollup with Add and countThread. status and operator read threads, not the rollup.
  - The wake inputs (C10's rule applied to state): the `wake-live` liveness verdict, Agent.WakePID and Agent.WakeHealth, the WakeHealth type, wakeState, wakeHealth, looksActiveForWake, hasLiveLeaningSignal (now `rec.AgentPID > 0`), readWakeLock, and match.go's wake process matcher with its root/argv parsers (wakeProcessMatcher, extractMeFromArgs, extractRootFromArgs, joinNonFlagTokens, rootSubstringMatchesBounded, isRootBoundary, rootsMatch, canonicalRootForMatch, relativeRootMatchesAbsolute).
  - The tmux runtime view (Agent.Tmux, TmuxRuntime) and Agent.GoalBinding.
  - Freshness.Source (FreshnessSource and its constants, createdSource, latestHeaderCreated): it told the console how much to trust an age.
  - collapse.go's long notes are shortened; code unchanged.
- Behavior changes (named):
  1. state snapshots no longer read `.wake.lock`. An agent whose only live signal was its wake helper now classifies by its own PID and presence (stale with a recorded PID, else dead or missing) instead of `wake-live`.
  2. Nothing outside state consumed edges, timeline, rollup, tmux, goal binding or freshness source, so no command output changes.
- Review result for collapse.go (review, 996 -> 815 / 635): what remains is the live thread collapse and triage behind `operator status`, attention, the wait-posture guard and status warnings (operator gate resolution and terminal binding, duplicate-copy handling, needs-you history, approve/goal attention reasons, staleness, heartbeat at-risk). The 180 over target is live triage logic, recorded here as reviewed and left in "over-target now"; it is not a ledger deferral.
- Files (non-test lines before -> after / target): collapse.go 996 -> 815 / 635 (review); coordination.go 566 -> 473 / 402; snapshot.go 382 -> 290 / 254; state.go 184 -> 143 / 118; match.go 192 -> 26 / 123 (shrunk); files.go 59 -> 32 / 38 (shrunk); message.go 618 -> 605 / 395; flow.go 25 -> deleted.
- Tests retired (subject removed): coordination_test.go TestEdgeCounts, TestTimelineDerivedHumanReadable; state_test.go TestBuildCarriesTmuxRuntimeForConsoleActions, TestClassifyWakeLiveFromVerifiedWake, TestWakeProcessMatcherAcceptsSymlinkedAbsoluteRoot, TestWakeHealthLabels, helper seedWakeLock.
- Tests changed (subject kept): the triage tests in coordination_test.go drop their 12 rollup assertions (thread-level triage checks stay); stale_test.go's two staleness tests now count Historical and Stale threads instead of reading the rollup; state_test.go TestBuildNeverExecsAMQ no longer seeds a wake lock.
- Interruption note: a broad `git checkout -- internal/state/` meant for the test files also reverted this commit's source edits; the edit scripts were re-run from HEAD before the test fixes.
- Lines 45,147 -> 44,509; holding 241; tests listed 1,142 -> 1,136.

### Commit 21: the holding file to zero

- internal/cli/v3_holding.go (832 at the start of 2a-2, 241 before this commit) is deleted. Each remaining item went to the live file that calls it, without its provenance header:
  - status.go: profileInitCommand (callers: dispatch, doctor, operator, status, team), readPresenceForEntry and presencePath (liveness.go; presenceFile lives in preflight.go). status.go 812 -> 838 / 900, still shrunk.
  - model_defaults.go: compactNativeValue (its only caller). 170 -> 183 / 130.
  - team.go: peelProjectFlag and runInProject (the --project peel for team verbs) and memberCWDOverride (team init's --cwd resolver; its #538 note is shortened). 1,850 -> 1,932 / 350 (ledger row).
  - mutation_json.go: memberFieldChange (its only user).
  - task.go: orDash and peelPositional. 1,285 -> 1,303 / 800.
  - context_resolution.go: resolveExistingTeamProfile (beside resolveProjectProfile) and resolveProfileFlag. 818 -> 846 / 551 (2b-design ledger row).
- Inlined, not moved (one-line wrappers): canonicalPath -> canonicalFilesystemPath (task.go, pathnorm_test.go); withProfileLock -> team.WithProfileLock (team_operator.go).
- v3_holding_test.go held only shared test fixtures (chdir, seedTeam, seedAgentRecord, writeMemberLaunchRecord, resumeChdir, namespaceMigrationBaseRoot); it is renamed cli_fixture_support_test.go, unchanged.
- No behavior change. The holding-file debit from 2a-1 is closed.
- Lines 44,509 -> 44,443 (the provenance headers and the two wrappers); holding 241 -> 0; tests listed 1,136.

### Commit 22: stale command names in kept text; the registry-vs-text guards cover rules, summaries and usages

- Generated team rules (team_rules.go), every line that named a removed verb or input:
  - assigned work goes by `amq send --kind todo` or `amq-squad dispatch` (no pane-prompt fallback);
  - the "four-operation simple path ... legacy --dispatch-next ... until step 6" note becomes "`amq-squad task done` changes the task status";
  - pointer stubs refresh with `amq-squad team sync --apply` (was `amq-squad init`, never a registered verb);
  - the shared-cwd exception is recorded with `amq-squad team init --shared-cwd-exception` (was `team shared-cwd-exception set`);
  - the lead queues tasks with `amq-squad dispatch` and monitors with `status --json` (was `start`, `focus`, the `send` pane fallback and tmux);
  - cut: the `records[].local_input` pane-tail scan line, the `team member add` spawn-origin clause, the NOC `DIRECTIVE:` line (directive went in commit 10), "session wake nudges", "wake delivery", "pane peeking";
  - "Legacy operator delivery requires..." becomes "The operator or parent orchestrator polls/drains...".
- command_registry.go: team summary "(init, rules, operator, sync)" (was lead, member, profiles); task summary "(add, claim, done, reconcile, handoff)" (was dispatch, recovery).
- team usage drops `team operator self pause|resume` (cut in commit 12). task's missing-subcommand error now lists every dispatched subcommand from one taskSubcommands list (leadership and handoff were missing).
- dispatch usage: "exactly like 'amq-squad amq send'" (the amq send wrapper went in commit 6) is rewritten; status's profileInitCommand suggests `amq-squad team init`.
- Worktree-isolation remedy (worktree_isolation.go): it named `team member update --cwd` and `team shared-cwd-exception set`, neither of which exists. The fix now names the exact blocked profile file and its `members[].cwd` and `shared_cwd_exception` fields (squad.yaml owns them in 2b); the unused session parameter goes.
- bootstrap.md (kept-dead prompt): the `amq-squad send` wrapper and the pane-prompt fallback lines go.
- Guards (help_test.go): assertTextNamesRegisteredCommands checks that every backticked `amq-squad VERB` is a registered command and that `amq-squad team|task|operator|amq SUB` names a dispatched subcommand. New tests apply it to the generated rules in all four operator modes (also rejecting tmux, DIRECTIVE:, local_input and wake nudges), the team and task registry summaries, team usage, task usage and dispatch usage. TestRootUsageExamplesNameRegisteredCommands (commit 4) covers the root usage.
- Behavior change (named): generated team-rules.md text, the worktree-isolation remedy text and the team/task summaries change as above; no command behavior changes.
- Tests updated: team_rules_operator_test.go (legacy poll wording), team_test.go TestRunTeamInitSeedsTeamRules and TestRunTeamInitOrchestratedInjectsNorm (the local_input and DIRECTIVE expectations go with their lines), worktree_isolation_test.go and worktree_isolation_reachability_test.go (profile-field remedy), bootstrap_test.go (template line).
- Outside internal/, the docs and plugin skills still carry removed verbs; that list (commits 6 and 12) is for the 3.0 docs pass.
- Lines 44,443 -> 44,414; holding 0; tests listed 1,136 -> 1,138.

### Commit 23: the 2a-1 coverage debit settled

Each flagged group from `docs/v3-spike/2a1-execution.md` "Coverage debit for 2a-2" is either covered again through a kept entry point (named test) or retired with its subject (named commit). None is dropped silently.

| 2a-1 debit group | kept symbol | settlement |
| --- | --- | --- |
| Guard freeze (3 tests) | ensureNoNamespaceMigration, runLaunch's freeze, guardAMQPassthrough, writer admissions | Retired with the subject: the freeze and ensureNoNamespaceMigration went in commit 13, runLaunch in commit 1. The kept writer admission is covered by TestNamespaceAdmissionContainedOpenRejectsAncestorSwapWithoutOutsideSideEffects, TestNamespaceWriterUnchangedContextProceedsAfterPreAdmissionPause, TestGateRaiseReResolutionDriftRefusesBeforeSend and TestOperatorAnswerAndVerifyReResolutionDriftFailBeforeSecurityBoundary. |
| tmux result mapping (2) | runTmuxLaunchPlanWithResult, runTmuxWindowsPlanWithResult | Retired: team_launch_tmux.go was deleted in commit 2 (ruling 4). |
| Brief seeding and live brief stub (10) | resolveSeed, buildSeedBrief, writeSeedBriefForProfile; ensureBriefStubForProfile | Covered through the kept `brief --seed-from` verb: brief_test.go TestBriefSeedFromFileWritesBrief, TestBriefSeedFromRefusesOverwriteWithoutForce, TestBriefDryRunPrintsWithoutWriting, and briefs_seed_test.go's resolveSeed/buildSeedBrief/writeSeedBrief tests (force, refuse, concurrent exclusive). ensureBriefStubForProfile went with the launch driver (the seeded-brief path of executeTeamLaunch, commit 2). |
| Worktree-isolation fix text (4) | worktreeIsolationCheck(ForSession), memberCWDOverride | The remedy text no longer names the deleted verbs (commit 22) and is covered by TestWorktreeIsolationFixNamesScopedExecutableRemedies, TestWorktreeIsolationFixOmitsDefaultProfileScope and TestWorktreeIsolationReadinessRowSharedCwdBlocksWithoutException. memberCWDOverride's project-relative --cwd is covered through `team init` by profiles_test.go TestTeamInitProjectTargetsOtherDir (run from another directory, --cwd qa=../member). The two "executes as displayed" tests are retired with `team member update` and `team shared-cwd-exception` (no command to execute). |
| Pane cleanup through stop (16) | pane_cleanup.go, pane_cleanup_rm.go, live_identity.go | Retired: the pane files went in commit 2 and live_identity.go in commit 3. |
| Exact named-profile stop scope (7) | namespace_conflict.go scope checks, wake_lock_compat.go, wakeProcessMatcher, readPresenceForEntry | Retired for namespace_conflict.go (commit 13), wake_lock_compat.go (commit 4) and state's wakeProcessMatcher (commit 20). readPresenceForEntry (now status.go) is covered through status liveness by the status_test.go tests that write presence.json (writeStatusPresence). |
| AMQ version floor (6) | doctorMinAMQVersion in the launch AMQ resolver | The floor is covered by doctor (TestExecuteDoctorAMQVersionRejectsOlderThan0600, ...Accepts0600AndNewerCanary, ...TooOld, ...Missing, ...Unparseable, commit 11) and, for the kept-dead launch resolver, by launch_test.go TestValidateSupportedAMQVersionUses0600Floor. The `team launch` floor tests went with the driver (commit 2); 2b's seat-exec owns the launch-time floor. |
| Status and roster (3) | blockedNativeGoalsInSnapshot, resolveProfileFlag | blockedNativeGoalsInSnapshot was cut with the goal projection (commit 4); retired. resolveProfileFlag is covered by profiles_test.go TestResolveProfileFlag and by the named-profile status and context tests (TestExplicitExternalRootWinsForNamedProfile, TestOrdinaryCommandsDoNotSpliceConflictingTuples). |
| Wake (opt-in real AMQ) | wake_inject.go, wake_lock_compat.go | Retired with the wake inputs (commits 1 and 4); TestRealAMQCompatibility remains without its wake legs (commit 11). |

No code change.

### Commit 24: launch/record.go (watch list). The minimal launch record

- Manifest reason: "minimal launch record; seat-exec writes it". The Record keeps the fields live or kept-dead code reads (identity, roots, tool and model args, trust, spawn origin/depth, NoGitignore, PreauthorizedActions, AgentPID/AgentTTY, StartedAt/StoppedAt, TeamProfile/TeamHome) and Tmux, which only the kept-dead fleet registry (global_noc.go) reads.
- Cut fields (no reader since the launch driver, wake, adoption, goal and run-start cuts): NoDefaultArgs, NoPreauthorizeInScope, AdoptionMode, LauncherPaneID, NoRequireWake, Symphony, NoWakeReason, External, OrchestratorRegistration (the type stays for global_noc.go), GoalBinding, LauncherPreauthorizedActions, ExplicitAllowedTools, WakeInjectVia/Args/Mode, WakeRetryUntil, WakeRetryTransition, WakeInjectCmd, WakePID, WakeRecordID, WakeRecordDigest, the five PreparedRun* fields, and Terminal. Field notes are shortened.
- Cut functions (deadcode: only tests reached them): TerminalInfoFromTmux, Write (tests write through the kept WriteUnderRecordLock), Scan, ScanRestorableEntries and ScanLegacyEntries (tests call the InRoot forms with the project's .agent-mail base, which is what the wrappers did).
- Behavior change (named): launch.json records written by v2 still decode; the removed keys are ignored, and nothing in 3.0 writes them.
- Files: launch/record.go 704 -> 537 / 250 (trimmed). The remaining over-target is the record scan (ScanEntriesInRoot, the restorable and legacy-mailbox inference that state's snapshot uses); seat-exec in 2b replaces the record writer and its layout.
- Tests retired (subject removed): record_terminal_test.go (deleted) TestTerminalInfoFromTmux, TestTerminalInfoFromEmptyTmuxIsNil; record_test.go TestWakeInjectCmdAdditiveBackCompat, TestWakeRetryUntilAdditiveBackCompat, TestSymphonyAdditiveBackCompat.
- Tests changed (subject kept): TestWriteReadRoundTrip drops its wake, preauthorization-provenance and wake-lock binding fields; the Scan tests use the test-side scanRecordsForTest over ScanEntries; the legacy scan tests call ScanLegacyEntriesInRoot/ScanRestorableEntriesInRoot. Fixtures across cli and state call launch.WriteUnderRecordLock, and struct literals drop the removed fields (scratchpad stripfields.py: operator_test.go's two External: true, and record_test.go's round-trip literal).
- Lines 44,414 -> 44,251; holding 0; tests listed 1,138 -> 1,133.

### Commit 25: internal/team (watch list). Test-only wrappers, clone.go and goal supervision cut; notes shortened

- Cut (deadcode: only tests reached them): the default-profile wrappers Path, Read, Write and Exists (tests call ProfilePath/ReadProfile/WriteProfile/ExistsProfile with DefaultProfile, which is what the wrappers did), DeleteProfile and DeleteProfileUnderLock, and clone.go's CloneRosterForSession (clone.go deleted; the `run start --profile` cloning it served went with the launch driver).
- Cut with the goal inputs: GoalSupervisionPolicy, GoalSupervisionPolicyStatus, EffectiveGoalSupervisionPolicy, validateGoalSupervisionPolicy and Team.GoalSupervision (native /goal pause supervision; nothing outside the package read it).
- Doc notes of four or more lines are cut to their opening sentence (scratchpad shortdocs.py, then two hand fixes where the sentence ended at "e.g."); code unchanged.
- Behavior change (named): team.json read stays lenient, so a profile with `goal_supervision` still loads; a later write (team operator set, team init --force) drops the key.
- Files: internal/team/team.go 1,494 -> 1,316 / 631 (trimmed; 685 in the ledger as a 2b-design deferral); clone.go 47 -> deleted (target 19).
- Tests retired (subject removed): clone_test.go (deleted) TestCloneRosterForSessionRejectsInvalidSession, TestCloneRosterForSessionThenWriteProfileValidates, TestCloneRosterForSessionRestampsSessionAndDropsInstanceState; goal_supervision_test.go (deleted) TestEffectiveGoalSupervisionPolicyDefaultsToManual, TestValidateGoalSupervisionPolicyModes, TestCloneRosterForSessionDropsGoalSupervisionPolicy, TestGoalSupervisionPolicyWriteReadRoundTrip.
- Tests changed (subject kept): 22 test files call the profile forms instead of the wrappers; team_test.go TestDrafterConfigRequiresSchemaSixAndValidPolicy is restored after the sweep with the profile form; two Claude permission patterns ("Read(...)") that the rewrite touched inside string literals were put back by hand.
- Lines 44,251 -> 44,026; holding 0; tests listed 1,133 -> 1,126.

### Commit 26: internal/task. Default-profile wrappers and the dead dispatch-delivery lifecycle cut

- Cut (deadcode: only tests reached them):
  - task.go: Dir, Add, List, Show, Claim, Done, DoneForProfile, Fail, Block, Reset, LinkDispatch (default-profile wrappers; tests call the ForProfile forms with team.DefaultProfile), AttentionLifecycleFor and IsAttentionLifecycleTerminal (the attention projection's lifecycle map, cut with notify in commit 10), and the default-profile terminal, mutate and withLock helpers.
  - lifecycle.go: the dispatch-delivery lifecycle that queue-only dispatch (commit 3) no longer drives: PrepareDispatchForProfile, FinishDispatchForProfile, AttachOutboxReceiptForProfile, authorizeLeadershipUnderLock, containsReceiptLink; and BlockAtomicLifecycleForProfile, CancelAtomicLifecycleForProfile, terminalLifecycleForProfile (the CLI blocks and cancels through BlockForProfile/CancelForProfile).
  - leadership.go: ReadLeadership.
- Kept, test-fixture only: LinkDispatchForProfile. Its doc already calls the metadata "legacy, untracked"; cli tests (status, evidence, task lifecycle) use it to build dispatched-task fixtures, so cutting it would retire kept-subject tests. Listed as class 3 (plain overage) in the final report.
- Test-side helpers replace the cut DoneForProfile: task/done_helper_test.go doneForTest and cli_fixture_support_test.go taskDoneForTest (both call DoneAtomicForProfile with the default completion notice, which is what the wrapper did).
- Coverage note: the structured lifecycle tests below built their fixture through PrepareDispatch/FinishDispatch with a run-generation ref; in 3.0 nothing produces those refs (the run-start generation went with the launch driver), so they are retired with the producer, and 2b's dispatch design owns the structured lifecycle's entry point.
- No behavior change on a live path.
- Files: task/task.go 807 -> 711 / 676; task/lifecycle.go 1,366 -> 1,013 / 1,144 (shrunk); task/leadership.go 156 -> 152 / 131.
- Tests retired (subject removed): atomic_lifecycle_test.go TestSuccessorDispatchCanonicalAuthorityTracksUncertainConfirmedRetryAndDelivery, TestResetReDispatchTombstoneAndPlaceholderRejectStaleSuccessorIntent, TestNilDispatchMigrationRejectsStaleDispatchMutationsWithoutChangingBytes, TestLinkDispatchPreservesCurrentAuthorityAndLegacyCompatibility, TestReceiptReplacementRequiresAndConsumesOrderedRetryAudits; lifecycle_event_test.go (deleted) TestStructuredDoneBindsImmutableCommandEvidenceAndGeneration, TestLifecycleJournalUsesCommittedAppendOrderNotOccurredAt, TestLifecycleRejectsDelayedGenerationDuplicateConflictAndCrossNamespace, TestStructuredBlockAndCancelCommitTerminalStateEventAndOutboxTogether; task_test.go TestAttentionLifecycleProjection; cli task_lifecycle_correlation_test.go (deleted) TestLifecycleCorrelationUsesValueEqualityAndImmutableEvidence; the unused helper extractQuotedCommand (worktree_isolation_reachability_test.go, unused since commit 22).
- Tests changed (subject kept): task_test.go, atomic_lifecycle_test.go and seven cli test files call the ForProfile forms.
- Lines 44,026 -> 43,573; holding 0; tests listed 1,126 -> 1,115.

### Commit 27: procinfo is PID liveness only; remaining dead helpers cut

- procinfo (manifest "PID liveness only; drop TTY/ChildrenIndex"): TTY and readTTYNative (darwin, linux, other), the linux TTY device mapping (ttyDeviceFromStat, linuxDeviceNumber, ttyPathForDevice) and the child index (parentChildIndex, ppidFromStat) are cut. The TTY probe goes from the three probe structs that carried it (cli duplicateLaunchProbe and launchRuntimeProbe, state Probe), and launch.Record drops AgentTTY.
- Behavior change (named): launch-record PID identity is PID alive + binary/launcher match + start time not after the record; a recorded agent TTY no longer participates (it was a pane/terminal-era signal).
- Dead helpers (deadcode) cut: activity.go SymphonySnapshot, symphonyEventClaimsActivity and Clear (manifest "drop SymphonySnapshot"); amq.go teamLeadHandle and parseP2PThread. cutdead protected the rest of its target list because the kept-dead launch and bootstrap closures call them (agent_args, agent_defaults, model_defaults, native_args, restore, briefs, workstream, wait_posture helpers). deadcode runs per GOOS: native_linux.go's parsers are live on linux, so only the cross-platform-dead TTY and child-index code went.
- Files: procinfo.go 128 -> 118 / 71; native_linux.go 195 -> 71 / 97 (shrunk); native_darwin.go 63 -> 31 / 41 (shrunk); native_other.go 21 -> 13 / 10; activity.go 263 -> 213 / 200; amq.go 453 -> 426 / 400; launch.go 281 -> 270; preflight.go 97 -> 95; state.go 143 -> 139; launch/record.go 537 -> 536.
- Tests retired (subject removed; linux-only file): native_linux_test.go TestTTYDeviceFromStatUsesControllingTTYFieldAfterComplexComm, TestTTYDeviceFromStatRejectsNoControllingTTY, TestTTYPathForDeviceDoesNotPromoteRedirectedFDZero, TestTTYPathForDeviceRejectsUnmappedDevice. GOOS=linux and GOOS=windows `go vet ./internal/...` are clean.
- Lines 43,573 -> 43,304; holding 0; tests listed 1,115 (darwin listing; the four retired tests are linux-only).

### Commit 28: doc notes shortened in the plain-overage rows

- Doc comments of four or more lines above a declaration or struct field are cut to their opening sentence (scratchpad shortdocs.py, as in commit 25) in the 35 over-target rows without a design reason (state, task, procinfo, drafter, activity and the cli task, operator, amq, verify, workstream and brief-seed files); one sentence that ended at "i.e." was rewritten by hand. Comments inside function bodies are untouched. 177 lines in 22 files.
- No code or behavior change. Lines 43,304 -> 43,127; tests listed 1,115.

### Commit 29: the end-of-2a-2 ledger in three classes (log only)

- Adds the section "End-of-2a-2 ledger in three classes" (before the commit 9 section) as the lead asked after commit 25: class 1 (2b design, squad.yaml rewrite rows), class 2 (phase 3 design, the compound release writer), class 3 (plain overage, with the three kept-dead extract rows listed apart), each row with its manifest reason quoted, and the totals against the band and the budget. No code change.

### Commit 30: the closing commit (lead rulings 11 and 12)

- Kept-dead extract floors (ruling 11, measured on this commit's tree, scratchpad floor.py):
  - internal/cli/wait_posture.go: target 100 -> 279. The extracts are guardOwnedWait (:64, live, the dispatch wait guard with waitPostureForContext :141, waitPostureUncertain :201, waitPostureCommand :205, waitPostureTimeout :213, callerOpenGates :220, defaultWaitPostureSnapshot :238 and writeWaitPostureAudit :246) and durableSendWaitPosture (:165, kept-dead, with parseWaitPostureDuration :150 and lastAMQSendFlagValue :181). The whole file is extract or closure: 272 code and blank lines, 7 comment lines.
  - internal/cli/global_noc.go: target 150 -> 265. The extract is the fleet registry read/write (globalNOCRegistryPath :81, canonicalGlobalNOCControlRoot :85, ensureGlobalNOCRegistryDirectory :108, readGlobalNOCRegistry :129, writeGlobalNOCRegistry :156, validateGlobalNOCRegistry :193) with its types and constants (:16-80). Whole file: 261 code and blank, 4 comment.
  - internal/cli/init.go: no raise needed. Trimming what is outside the extract closure (the rules pointer-stub digest flow: initPlan :18, computeInitPlan :34, computeInitDigest :80, applyInitPlan :102) cut initUsage (the usage of an `amq-squad init` verb that is not registered), peelInitApplyFlag, printInitPreview, initPreviewData and squadDisplayProfile, none of which the extracts call. init.go 262 -> 131 / 150 (shrunk); no test referenced them.
- Manifest statuses (ruling 12): the class 1 rows carry status deferred-2b and the class 2 rows deferred-3, each with current_lines and a note quoting the manifest reason:
  - deferred-2b: internal/cli/team.go, internal/team/team.go, internal/cli/team_rules.go, internal/cli/team_rules_draft.go ("rewrite around squad.yaml in 2b", the internal/team row adding "highest fan-in, do not delete in 2a"), internal/cli/context_resolution.go ("context resolution kept, simplified by squad.yaml"), internal/launch/record.go ("minimal launch record; seat-exec writes it"), internal/cli/doctor.go ("probe per plan 16.2").
  - deferred-3: internal/compoundrelease/store.go and reconcile.go ("review for pane references"; consumer: the phase 3 release flow, ruling 9).
- scripts/manifest-check.sh: accepts deferred-2b and deferred-3 (they carry current_lines, checked like trimmed), prints each class total (lines and over-target for classes 1 and 2, over-target for class 3), and with --final enforces the current total excluding the design-bound overage: a deferred-2b or deferred-3 row counts at min(wc -l, target_lines), the way a deferred-replace row (target 0) drops out. Documented in the script header.
- manifest-check output at this commit: current total 40,565 excluding deferred-replace; class 1 (deferred-2b) 6,303 lines, 3,471 over target; class 2 (deferred-3) 2,009 lines, 172 over target; class 3 3,149 over target; current total excluding design-bound overage 36,922, outside 29,790..36,410, so `--final` reports one violation, by decision.
- Lines 43,127 -> 42,996; holding 0; tests listed 1,115.

## End-of-2a-2 decision

2a-2 closes with manifest current total 40,565 (36,922 excluding design-bound rows), above the band top of 36,410; the plain overage of about 3,149 is live task, operator, role-draft and state code that 2b redesigns; no further cuts in 2a.

(The lead's wording gave 40,696 and 37,053, measured before ruling 11's init.go closure trim removed 131 lines; the figures above are measured at the closing head.)
