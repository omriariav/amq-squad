# amq-squad 3.0, phase 2a-1 execution log

Branch `v3-2a1`, cut from `v3` at 74d3e64. Work list: `docs/v3-spike/2a1-worklist.md`. Method: plan 4.1.
Note: a history rewrite on v3 on 2026-10-22 (about 12:5x) rewrote every hash on this branch; the hashes cited for cuts 1-4 below are pre-rewrite (post-rewrite: cut 1 525b139, cut 2 2df5fd0, cut 3 42642a8, cut 4 180f2d8; branch point ded3182). Later entries cite post-rewrite hashes.
Implementer change: after cut 5 the first implementer stopped on a rate limit; one successor left an uncommitted partial cut 6 (saved by the lead as WIP commit abadb4b) and made no commit of its own; the third implementer finished cut 6 from that WIP (amended into the cut 6 commit) and continues from there.
One cut = one commit; each commit green on `go build ./... && go vet ./... && go test ./... && scripts/manifest-check.sh`.

## Baseline (v3 head 74d3e64, module path already `/v3`)

- `go build ./...` and `go vet ./...` green.
- `go test ./...`: 36 packages ok, 254 s wall (internal/cli alone about 230 s; every later run is dominated by it).
- Tests listed by `go test ./... -list '.*'`: 3,024.
- Non-test lines under `internal/`: 110,929 (manifest total matches).
- `scripts/manifest-check.sh`: OK (284 entries, target sum 33,504).

## Rulings received from the lead during execution

1. Holding file. The manifest names no holding file for any extraction. Ruling: create `internal/cli/v3_holding.go` (manifest entry: fate shrink, cluster holding, target 16 now, 0 at the end of 2a), move verbatim only what a surviving non-test caller needs, each block under a comment naming source file and lines. Functions whose only callers are themselves deleted (digestBytes, briefCandidateEnvelope, resolveProjectProfile, compareCWD) are deleted with their files. `internal/wizard/routing.go` ("move to its 3.0 home"): no home named, so the file is deferred.
2. manifest-check check 3 (`v2_lines` within 2 percent of `wc -l`) fails for every surviving file a cut edits. Ruling: `v2_lines` never changes (it is the 2a-2 baseline). The script gains `status: trimmed` plus `current_lines: N` for edited surviving files; check 3 compares `current_lines` for those and `v2_lines` for everything else. Done in cut 3.
3. Accepted deviations (cut 3): C6 folded into the C5 commit; `namespace_migration_native_claims.go` (C7) deleted in the same commit; `goalOrchestratorRole` and `defaultGoalOrchestratorHandle` parked in the holding file (not inlined).
4. (after cut 7) lead_identity.go deferred to 2a-2: moving 130 of its 145 lines into the holding file is a rename, not a deletion. The windows build failure seen at cuts 6-8 (unix.Statfs in namespace_migration_plan.go, syscall.Kill/Setpgid in team_lead.go) was pre-existing and is not a 3.0 target; it went with cuts 8 and 9.
5. (after cut 7) Test sweeps: re-audit the removals of every earlier cut, restore each test of kept code minus its deleted references, one restore commit, with a table (file, test, symbols exercised, symbol survives?, restored yes/no) reconciling the drop in listed tests to tests of deleted code only.
6. (after cut 8) `internal/wizard/routing.go`: fate delete, target 0; goes with the wizard package when nothing kept imports it ("no 3.0 home named" is not a reason to keep it). This replaces the routing half of ruling 1.
7. (after cut 8) The holding file's manifest row carries the 2a-2 debit as a field (`note: "2a-2 debit, target 0 at end of 2a"`), listed by manifest-check. Work in progress may be red between commits; every committed head is green; no WIP commits.
8. (after cut 11) operator_control.go: follow the manifest condition; broadcast is gone, so `operator send` goes too (operator-to-seat messages in 3.0 go through `dispatch` and gates). The same reading was applied to `team profiles` (profiles.go, manifest reason "cut").

## Cuts

### Cut 1: console (orphan package)

- Commit 6fabee8. Deleted: `internal/console/` whole, 11 non-test files, 3,626 lines; 5 test files, 1,956 lines.
- Surviving files edited: none. Manifest: 11 entries flipped to deleted.
- Tests: 36 -> 35 packages ok; 165 s wall (cache warm for untouched packages).

### Cut 2: tmuxharness (orphan package)

- Commit 39fa637. Deleted: `internal/tmuxharness/harness.go` (485 lines) and `harness_test.go` (518 lines).
- Surviving files edited: none. Manifest: 1 entry flipped.
- Tests: 34 packages ok; 244 s wall.

### Cut 3: C5 goal supervision + C6 recovery transition (folded, see deviations)

- Deleted non-test (16 files, 7,787 lines by git numstat; manifest v2 total 7,787):
  C5: goal_attempt.go, goal_supervise_resume.go, goal_supervision_binding_revalidation.go, goal_supervision_delivery_gates.go, goal_supervision_delivery.go, goal_supervision_gates.go, goal_supervision_resume.go, goal_supervision.go, goal.go, resume_goal.go (6,204).
  C6: recovery_transition_cas.go, recovery_transition_contract.go, recovery_transition_publication.go, recovery_transition_scan.go, recovery_transition.go (1,449).
  C7 (early): namespace_migration_native_claims.go (134).
- Deleted test files (18, 7,484 lines): the seven `goal*_test.go` / `resume_goal_test.go` basename matches, `goal_supervision_decision_test.go`, `goal_supervision_policy_mutation_test.go`, `goal_supervision_u6_falsifiers_test.go`, six `recovery_transition_*_test.go`, `namespace_migration_native_claims_test.go`, and `codex_convergence_fix_pins_test.go` (every test in it exercised recovery transitions, goal delivery gates, or the native-claim migration blocker; nothing remained).
- Registry: `goal` removed from commandCatalog and commandRegistry (`command_registry.go` 131 -> 129).
- Holding file created: `internal/cli/v3_holding.go` (16 lines): consts `goalOrchestratorRole`, `defaultGoalOrchestratorHandle` from goal.go:314-317 (callers: global_noc.go, team_launch.go, and C3/C9/C18 files cut later).
- Surviving files edited (v2 -> now; what was cut), all marked `status: trimmed` in the manifest:
  - bootstrap.go 703 -> 679: bootstrapGoalBindingMode and bootstrapRecordIsVisibleLead deleted; bootstrapExecution passes the constant mode "amq_task_brief" (the fallback the deleted function returned when no goal binding existed).
  - doctor.go 1,917 -> 1,878: doctorCheck.GoalSupervision field and doctorCheckGoalSupervision (already unreferenced by runDoctorChecks) deleted.
  - execution_mode.go 682 -> 651: applyLeadModeToDraftContract and rosterRoles deleted (no callers left).
  - launch.go 1,214 -> 1,181: goalBindingFromArgs, nativeGoalBindingFromArgs deleted with their two call sites (restore-goal-binding guard, GoalBinding fill on the record).
  - restore.go 758 -> 746: the goal-binding argv strip block in restoreArgvFromRecord deleted.
  - status.go 1,951 -> 1,783: statusEnvelopeData.GoalBinding/GoalSupervision fields, goalBindingForNamespace, goalBindingForStatus, verifyClaimedGoalBindingForStatus, applyGoalBindingOpenBlockers, projectExecutionModeRequiresGoalBinding, goalMissingBinding deleted; executionContractForTeam receives "amq_task_brief"; operatorVisibilityForLead loses the launchRecordHasGoalBinding clause. nativeGoalBindingBlocked and blockedNativeGoalsInSnapshot stay (operator.go calls them; no deleted symbol).
  - team.go 2,579 -> 2,579 (2 lines changed): goalBindingForNamespace(...).Mode inlined to its only possible value "amq_task_brief".
  - target_root.go 157 -> 140: goalTargetProjectRootLine deleted (caller was goal.go).
  - command_registry.go 131 -> 129.
  - Later-cut files given throwaway edits to compile (they die in C3/C4/C7/C9/C18): external_orchestrator_lifecycle.go 365 -> 223 (beginExternalOrchestratorLifecycle, ensureExternalOrchestratorMailbox, externalOrchestratorAgentUnion; callers were in goal.go), namespace_migration_adapters.go 293 -> 143 (goal artifact rewrite and launch-generation helpers; goal_attempts artifacts now return an error from migrationTransformFile), namespace_migration_plan.go 665 -> 664 (native-claims inspector call), lead_identity.go 145 (default branch of launchRecordAuthorizesProjectLead returns false), simple_wizard.go 800 (RunBriefDraft: drafter.Run, which is what runGoalDrafter was), status_board.go 765 -> 763 (GoalSupervision field, goal binding), team_lead.go 1,175 -> 1,103 (preserveExternalGoalBinding and friends plus their one call).
- Test files edited (test functions that only exercised deleted code removed; tests of surviving code kept with the goal reference dropped): bootstrap_test.go (2 tests removed; TestBootstrapPromptIncludesExecutionMode loses the "Goal binding: prompt_goal_missing" expectation), status_test.go (7 goal-binding tests removed; TestStatusUsesPlainTaskStateAndOmitsGoalSupervision and TestExecuteStatusJSONIncludesSpawnMetadata kept minus their GoalSupervision/GoalBinding assertions), launch_test.go (3), namespace_migration_test.go (1), external_orchestrator_lifecycle_test.go (1), profiles_test.go (2), operator_identity_test.go (1), team_lead_test.go (2), help_test.go (goal entries in two table tests; TestGoalGroupHelp and TestGoalUnknownSubcommandEnumeratesCompleteSurface removed), completion_test.go ("goal" entry).
- manifest-check.sh: `trimmed` status and `current_lines` added (ruling 2); header comment documents it.
- Tests: 3,024 -> 2,787 listed; 34 packages ok; 260 s wall.
- Deviations: C6 folded in (ruling 3); native_claims.go early (ruling 3); two consts parked without an extract entry (ruling 3); throwaway edits in seven later-cut files (listed above) so the commit compiles.

### Cut 4: C11 up, down, resume (taken before C4; plus agent.go per decision 6)

- Order deviation: the worklist has C4 before C11, but the probe after deleting C4 showed C11's files (resume_exec.go, resume.go, up.go, down.go) as the heaviest callers of C4 symbols (simple_start plan/request types, preview flags, peelProjectFlag/runInProject), while C4 calls C11 at only three sites (new.go -> runUp, existingSessionRefusal; simple_start.go -> teamRoleList). Plan 4.1 says callers before callees, so C11 went first; the C4 working-tree changes were reverted untouched before this cut.
- Deleted non-test (7 files, 3,432 lines): down.go (1,780), resume_exec.go (249), resume_required_actions.go (167), resume_shared.go (124), resume.go (342), up.go (431), and agent.go (339, C23): the hidden `agent` entry point was removed from the registry per decision 6 and runAgent's only purpose was to call runUp/runRestore, so the file cannot compile without it.
- Deferred inside C11: `internal/cli/visibility.go` (107 lines, fate delete) stays until its last consumers go: layout_preset.go (C1, 8 sites), runtime_json.go (C2, 2 sites), team.go (2 sites, visibilityPreviewLaunchCommand). It is cut with C2.
- Deleted test files (8, 4,076 lines): down_test.go, resume_exec_test.go, resume_required_actions_test.go, resume_shared_test.go, resume_test.go, up_test.go, up_pr6_test.go (every test called runUp), error_taxonomy_520_test.go (its single test required `amq-squad resume` to execute).
- Registry: down, resume, agent removed (`command_registry.go` 129 -> 121).
- Holding file additions (v3_holding.go 16 -> 188 lines), all verbatim with source lines, none in a manifest extract field: profileInitCommand (up.go:404-411; dispatch.go, doctor.go, operator.go, status.go, team.go), teamRoleList (resume_shared.go:37-44; callers plan.go and simple_start.go die in C4, drop it then), canonicalPath and compactNativeValue (resume_shared.go:46-63; task.go, effort.go, model_defaults.go), findMemberRestoreRecord (resume_shared.go:65-107; workstream.go), downStatus, processTerminator, signalTerminator, newSignalTerminator, Terminate, SignalName, signalNameOf (down.go:22, 58-69, 90-134; session_reset_support.go host field Terminator and pane_cleanup_rm.go C1), downAgentJSON (down.go:1613-1616; pane_cleanup_rm.go only, drop with C1).
- Test-side holding file created: `internal/cli/v3_holding_test.go` (255 lines) with shared fixtures moved verbatim from deleted test files because 36+ surviving tests use them: chdir, seedTeam, seedTeamWithoutSharedCwdException, fakeBackend (+methods), useFakeBackend (up_test.go:15-127); recordingTerminator, selfCleaningTerminator, downFakeProbe, seedAgentRecord (down_test.go); writeMemberLaunchRecord, resumeChdir (resume_shared_test.go). fakeMissingWakeCheck was moved and then removed by the sweep (its only callers were down tests).
- Surviving files edited (marked trimmed): command_registry.go 129 -> 121. Throwaway edits in later-cut files: new.go (C4; the `new session` delegation to runUp now returns a usage error, existingSessionRefusal inlined as a plain error), team_member.go (C18; teamMemberLaunch/teamMemberStop seams now return an explicit "removed in 3.0" error instead of calling runResume/runDown).
- Test files edited: the compile sweep removed test functions that referenced deleted symbols in 29 files (per-file deletions in the commit numstat; largest: stop_namespace_conflict_test.go 496 lines, pane_cleanup_down_wiring_test.go 386, deprecation_test.go 361, real_amq_compatibility_test.go 338, parent_amq_floor_test.go 292, json_envelopes_test.go 247, operator_handoff_card_493_test.go 246, real_amq_wake_compatibility_test.go 246). Runtime assertions on the removed verbs dropped from help_test.go (down/resume/agent rows; TestAgentHelpDocumentsProject), completion_test.go, deprecation_test.go, cli_test.go (agent dispatchable check), new_test.go (three `new session` -> up delegation tests, C4 file).
  Review note for the lead: the sweep removes the whole enclosing test function when any line references a deleted symbol. The removed names are visible in the commit diff; a few may be table tests that also covered surviving code (TestScopedShortFlagAliasesRegistered in scoped_flags_test.go is the one I would check first). Helper-only test files left behind by the sweep (pane_cleanup_down_wiring_test.go, real_amq_wake_compatibility_test.go, pane_lifecycle_test.go) compile and are 2a-2 cleanup.
- manifest-check.sh: `comm` now runs under LC_ALL=C like the sorts; without it macOS comm (session locale) mis-collated `agent.go` against `agent_args.go` once agent.go was gone and reported three present files as missing. Latent bug, first exposed here.
- Manifest: 6 C11 entries plus agent.go flipped to deleted; v3_holding.go entry v2_lines/target_lines set to 188 (its current size, per ruling 1).
- Tests: 2,787 -> 2,578 listed; 34 packages ok; internal/cli 138 s.

### Cut 5: C4 wizard, start, new, setup, plan, list (two files deferred by ruling)

- Deleted non-test (6 files, 3,760 lines): list.go (226), new.go (654), plan.go (256), setup.go (479), simple_start.go (1,345), simple_wizard.go (800). Registry: setup, init, new, wizard, start, plan removed (command_registry.go 121 -> 109). init.go (host) stays, now unreachable from the registry as the worklist planned.
- Deferred by ruling (load-bearing for kept commands, recorded for 2a-2): `preview_flags.go` (193; registerPreviewFlags/registerLiveLaunchFlags/buildLiveLaunchOptions called by team_launch.go runTeamLaunch and team.go runTeamShow, both C18 shrink) and `simple_start_brief_draft.go` (236; draftSimpleStartBrief and cliDrafterRunner/nonEmptyTrimmedLines called by brief.go `--goal` and team_rules_draft.go, both kept). Manifest status left pending for both.
- Deleted test files (8, 3,735 lines): the six basename tests plus v228_contract_goal_delivery_test.go and v228_contract_roster_change_test.go, which the sweep emptied (every test drove `start`).
- Holding file additions (v3_holding.go 188 -> 422): peelProjectFlag and runInProject (new.go:488-525, 596-621; named in the manifest extract; callers launch.go, team.go); wakeHealthForEntry, looksActive, readPresenceForEntry, presencePath (list.go:146-215; callers restore.go, liveness.go, namespace_migration_plan.go; note wakeHealthForEntry still leans on wake_lock_compat.go (C1) and runtime_identity.go (C2)); writeSimpleStartDrafterAttempts (simple_start.go:1291-1308; brief.go); the checkpoint block simpleStartCheckpoint, its consts, simpleStartCheckpointError, callSimpleStartCheckpoint (simple_start.go:30-58): a dead path (simple_start.go was the only setter of teamLaunchOptions.AfterCheckpoint) parked because team_launch.go, team_launch_launchapi.go (kept) and team_launch_tmux.go (C1) name the type at about a dozen sites; drop with C1 and the 2a-2 team_launch shrink. NOT verbatim, flagged for review: `simpleStartDependencies`, defaultSimpleStartDependencies, normalizeSimpleStartDependencies (simple_start.go:60-73, 94-107, 110-172) were reduced to the two drafter-seam fields (ResolveDrafter, RunDrafter) that brief.go, the deferred brief_draft file and brief_test.go use; the full struct named simple_start plan types and launch seams that no longer exist, so a verbatim move was impossible without keeping most of simple_start.go.
- Test-side holding file: validSimpleStartBriefDraft (simple_start_test.go:1483-1495) moved so TestBriefGoalDraftsAndWritesReviewedDocument (brief --goal, kept code) could be restored verbatim after the sweep had removed it for that one helper reference.
- Surviving files edited (trimmed): command_registry.go; team.go 2,579 -> 2,572 (the publishTeamInitFlagSet test-introspection call and its comment; the snapshot var lived in new.go); exit.go 174 -> 173 (the stale `list` entry in commaSeparatedProjectFlagCommands, which the self-policing pathnorm test rejects once list.go is gone). cmd/amq-squad/main_test.go: the two `start` exit-code rows replaced by an equivalent `doctor --banana` usage row.
- Test files edited (20): sweep removals in custom_role_test.go (15 `new team`/`new profile` role tests), worktree_isolation_reachability_test.go (seed helpers used `new team`; 7 tests of the fix-text remedies went with them: coverage loss on surviving worktree-isolation code, flagged for 2a-2), v228 start-harness files (now helper-only shells: v228_contract_crash_injection_test.go, v228_contract_goal_delivery_step3_test.go, v228_contract_restore_step3_test.go, v228_contract_start_harness_test.go, v228_contract_legacy_namespace_test.go), effort_test.go (3 `new`-driven team-init effort tests), new_profile_flag_forwarding_test.go, team_no_session_pin_test.go, team_shared_cwd_test.go, brief_test.go (one test restored, see above). Runtime assertions on the six verbs dropped from help_test.go (rows and the start/new-only tests), completion_test.go, cli_test.go, exit_test.go, init_test.go (new team/new profile rows), pathnorm_test.go (list exemption rows and the list-only test).
- Tests: 2,578 -> 2,435 listed; 34 packages ok; internal/cli 86 s.

### Cut 6: C8 notifications (watcher, AMQ watch, session notifier)

- Finished from the WIP commit abadb4b (state coherent: build and vet green); amended into the cut 6 commit.
- Deleted non-test (3 files, 2,361 lines): notification_amq_watch.go (303), notification_watcher.go (1,192), session_notifier.go (866).
- Entry points removed: lookupCommand hooks `_notification-watch`, `_session-notify` (command_registry.go 109 -> 101).
- Deleted test files (4, 3,046 lines): notification_amq_watch_test.go, notification_watcher_test.go, session_notifier_test.go, v228_contract_idle_wake_step3_test.go (helper-only after the sweep; its helpers fed only the deleted session-notifier tests).
- Holding file: no additions.
- Surviving files edited (marked trimmed; callers cut, nothing stubbed):
  - doctor.go 1,878 -> 1,842: doctorCheckNotificationWatcher and its runDoctorChecks call.
  - global_status.go 752 -> 742: globalStatusRunView.Watcher field, globalStatusExecution.Watcher seam, the watcher projection, the watcher clause of globalStatusRunHealth, the WATCHER table column.
  - namespace_migration_plan.go 664 -> 644 (C7, dies next-but-one): watcher runtime blockers and watcher lock paths in inspectNamespaceOperationalOwners / inspectNamespaceLocks.
  - session_reset_support.go 691 -> 663 (host): the watcher fence (inspect, stop before --stop-agents, restart on mutation failure) in executeRmReportDeclined.
  - status.go 1,783 -> 1,760: statusEnvelopeData.NotificationWatcher, the `# notification_watcher:` text line, statusNotificationWatcherWarnings.
  - status_board.go 763 -> 738 (C3, cut later): NotificationWatcher row field, enrichBoardNotificationWatcher, boardNotificationWatcherCell, the WATCHER column.
  - team_launch.go 1,063 -> 1,039: reconcileNotificationWatcherStarted calls (external-lead path and main path), watcher generation tracking and its rollback cleanup.
  - command_registry.go 109 -> 101.
- Test files edited: global_status_test.go (the WIP sweep had removed five global-status tests for a Watcher seam field; restored verbatim minus the seam, the "watcher" run row and its degraded assertion; run count 6 -> 5), parent_amq_floor_test.go (WIP sweep removed three team-launch floor tests for one notificationWatcherRuntimePath path in each no-mutation list; restored minus that path), namespace_migration_test.go (TestNamespaceMigrationExpiredWatcherLeaseWithLiveMatchingPIDBlocks removed, it tested the deleted blocker; TestNamespaceMigrationFreezesPreviouslyUnguardedWriters restored minus its two watcher rows), real_amq_wake_compatibility_test.go (realAMQBusyWakeOwnershipCase, a session-notifier driver).
- Platform: `GOOS=linux go build ./...` green. `GOOS=windows go build ./...` fails before and after this cut in namespace_migration_plan.go (unix.Statfs) and team_lead.go (syscall.Kill, Setpgid); pre-existing, not a 3.0 target (second reviewer confirmed on a clean rebuild of 5a7742f); both files die in C7/C18, and the windows gate applies at C7.
- Tests: 2,435 -> 2,363 listed; 34 packages ok; 149 s wall.

### Cut 7: C9 external orchestrator (lead_identity.go deferred)

- Deleted non-test (2 files, 933 lines by numstat; manifest v2 1,075): external_orchestrator_lifecycle.go (223 now, 365 v2; cut 3 had trimmed it), external_orchestrator_registry.go (710).
- Entry point removed: registry `lead` (runLead, team_lead.go; the file itself is C18, next cut). command_registry.go 101 -> 99.
- Deferred: `internal/cli/lead_identity.go` (145, C9 delete). The manifest extract names only sameFilesystemPath and currentWorkingDirMatches, but the probe shows 10 of its 13 symbols with surviving non-test callers in kept commands: amq.go (currentEnvProvesTeamRole, projectExecutionMode, launchRecordAuthorizesProjectLead, launchRecordMatchesIdentity), operator_identity.go (findLaunchRecordByPane, launchRecordMatchesPane, launchRecordAuthorizesProjectLeadPane, sameFilesystemPath, currentWorkingDirMatches), status.go and team_launch.go (projectExecutionMode, adoptionMode consts, findLaunchRecordByPane, launchRecordMatchesIdentity), bootstrap.go (sameFilesystemPath). Moving them would put about 130 of its 145 lines into the holding file (a rename, not a deletion), so it is deferred like preview_flags.go: load-bearing for kept commands. leadRegisterTargetMode and launchRecordMatchesSamePaneIdentity go dead with C18 (team_lead.go) and stay as 2a-2 debris. Needs a lead ruling if the holding-file move is preferred.
- Deleted test files (2, 494 lines): external_orchestrator_lifecycle_test.go, external_orchestrator_registry_test.go.
- Holding file: no additions.
- Surviving files edited (trimmed): command_registry.go. Test edit: completion_test.go (the "lead" row of the catalog-derived top-command table).
- Tests: 2,363 -> 2,354 listed; 34 packages ok; 89 s wall. GOOS=linux build green.

### Cut 8: C18 team_* delete-fate files (team_overlay.go deferred)

- Deleted non-test (6 files, 2,815 lines by numstat; manifest v2 2,887): team_autonomous.go (147), team_lead.go (1,103 now, 1,175 v2; cut 3 had trimmed it), team_member_diff.go (149), team_member.go (1,051), team_rm.go (223), team_shared_cwd.go (142).
- Entry points removed (team.go dispatch arms, team stays registered): `team lead`, `team member`, `team autonomous`, `team rm` / `team delete`, `team shared-cwd-exception`; the unknown-subcommand hint list shrinks with them. printTeamUsage still names the removed subcommands (team.go is the 2b rewrite host; left for 2a-2).
- Deferred: `internal/cli/team_overlay.go` (1,310, C18 delete). validateMemberOverlayPaths (called by kept team.go, team_launch.go, doctor.go) runs the full tool-policy drift predicate (validateMemberToolPolicy and its Claude/Codex capability discovery, materialization rendering, canonical path sets), which lives in team_overlay.go; moving it would carry most of the file into the holding file, and removing it would drop the spawn-time policy check of a kept command. `team overlay` therefore stays dispatched. 2a-2 with the C18 shrink.
- Deleted test files (6, 3,008 lines): team_lead_test.go, team_member_test.go, team_member_update_test.go, team_member_diff_616_test.go, team_rm_test.go, team_shared_cwd_test.go.
- Holding file additions (v3_holding.go 422 -> 571), verbatim, each under a source-and-callers header: memberFieldChange (team_member_diff.go:11-17; mutation_json.go, a keep file: the port the worklist section 8 asked for), wantsHelp, orDash, peelPositional, shellJoin, resolveExistingTeamProfile, withProfileLock, inheritedSession, memberCWDOverride (team_member.go; callers init.go, task.go, role_draft.go, team.go, team_operator.go, amq_root_authority.go), currentPaneIdentity, memberByRole (team_lead.go; global_noc.go, team_launch.go, layout_finalize.go), configuredTeamProfiles (team_rm.go; status_board.go, C3). Imports tmuxpane added.
- Test-side holding: wakeDrainInject (team_lead.go:62-76) into v3_holding_test.go; it has no surviving non-test caller, but two status tests of kept wake-auto-drain status code build their fixture with it.
- Manifest: the holding entry carries `note: "2a-2 debit, target 0 at end of 2a"` (lead ruling); manifest-check.sh parses the optional `note` key and lists every "2a-2 debit" entry in its summary. TSV empty fields now print as "-" (tab is IFS whitespace, so `read` would otherwise shift a note into current_lines). v2_lines/target_lines of the holding entry follow its size (571).
- Surviving files edited (trimmed): team.go 2,572 -> 2,561 (dispatch arms). Throwaway edit in a later-cut file: team_member_control_continue.go (C1; its help branch printed teamMemberUsageText, now a one-line usage; the function is unreachable since `team member` is gone, the file stays because runtime_json.go (C2) uses two of its helpers).
- Test files edited, each removal checked against its trigger (sweep log kept in the scratchpad): team_test.go (setupFakeAMQSessionRoots called setupFakeExternalWakeBinder, a seam of the deleted external-lead wake; the call and the helper were removed by hand so the sweep could not take setupFakeAMQSessionRoots and its hundreds of callers with it; TestRunTeamAutonomousPauseAndDisable and TestRunTeamAutonomousSerializesProfileReadModifyWrite drive `team autonomous`), effort_test.go (TestTeamMemberAddEffortDryRunJSONAndLiveParity, TestTeamMemberAddEffortKnownAutomaticAndAtomicFailure: drive `team member add`), pane_cleanup_dead_pane_689_test.go (two `team member rm` tests), team_member_control_continue_test.go (TestRunTeamMemberControlContinueHoldsAdmissionAndDoubleVerifies drives `team member control-continue`), stop_namespace_conflict_test.go (unused swapTeamMemberStopRunner helper), namespace_migration_test.go (the "lead register" writer row), help_test.go and init_test.go (help rows for the removed subcommands). No removed test exercised kept code only.
- Tests: 2,354 -> 2,248 listed; 34 packages ok; 90 s wall. GOOS=linux build green.

### Cut 9: C7 namespace migration (namespace_conflict.go and namespace_open_other.go deferred)

- Deleted non-test (7 files, 2,051 lines by numstat; manifest v2 2,222): namespace_migration_adapters.go (143 now, 293 v2), namespace_migration_cli.go (239), namespace_migration_plan.go (644 now, 665 v2), namespace_migration_tx.go (998), namespace_rename_darwin.go, namespace_rename_linux.go, namespace_rename_other.go (9 each; namespaceRenameNoReplace had no caller outside the migration tx on any platform: grep, and the linux and windows builds below). namespace_migration_native_claims.go went in cut 3.
- Entry point removed: registry `namespace` (command_registry.go 99 -> 97).
- Deferred: `internal/cli/namespace_conflict.go` (514, C7 delete). All 21 functions are reachable from kept commands (worklist deadcode: 0 of 21 unreachable); the probe after deleting it showed ensureNoNamespaceConflict / ensureNoNamespaceConflictWithOverride / namespaceConflictOverrideOptions in gate_close.go (keep), dispatch.go, operator.go, task.go, launch.go, team.go, and namespaceConflictData / namespaceConflictForProfileSession / disableNamespaceConflictActions / pluralProfileList in status.go and the guard host. Load-bearing for kept commands; 2a-2.
- Deferred: `internal/cli/namespace_open_other.go` (18, C7 delete). It is the `!unix` twin of the kept host namespace_open_unix.go; deleting it leaves openContainedNamespaceLock undefined for the guard host on windows, which the C7 windows gate rejects (ruling 5 had both go with C7; the guard keeps calling the open primitives). 2a-2 with the guard shrink.
- Deleted test files: none whole (namespace_migration_test.go keeps the admission and repository-lock tests).
- Holding file additions (v3_holding.go 571 -> 713), all for the guard host (namespace_migration_guard.go reads on-disk migration journals for its writer freeze): namespaceMigrationArtifact, namespaceMigrationInputEvidence, namespaceMigrationLiveness, namespaceMigrationPlan (namespace_migration_plan.go:25-73; the journal's Plan field), dedupeMigrationStrings (:632-644), canonicalMigrationProject (:219-233), readNamespaceMigrationJournal (namespace_migration_tx.go:376-400), rejectMigrationSymlinkComponents (:591-615). The manifest extract field names the guard's admission functions, which stay in the guard.
- Test-side holding: namespaceMigrationBaseRoot (namespace_migration_adapters.go:79-84), used by the restored fixture below.
- Platform: `GOOS=linux go build ./...` and `GOOS=windows go build ./...` both green (the windows failure recorded at cut 6 went with namespace_migration_plan.go and team_lead.go).
- Test files edited: namespace_migration_test.go: the sweep removed 19 migration tests (they drive executeNamespaceMigration, recoverNamespaceMigration, rollbackNamespaceMigration, planNamespaceMigration, acquireNamespaceMigrationLocks or the migration failpoints, all deleted) and the fixture; the fixture (newNamespaceMigrationFixture, newNamespaceMigrationFixtureProfiles; its only deleted reference was namespaceMigrationBaseRoot) and TestNamespaceWriterUnchangedContextProceedsAfterPreAdmissionPause (kept writer-admission path, drives task add) were restored verbatim. Not restored, flagged: TestNamespaceMigrationFreezesPreviouslyUnguardedWriters, TestNamespaceMigrationNonterminalJournalFreezesBothEndpoints and TestNamespaceMigrationFreezesDirectAgentUpBeforeLaunchMutation exercise the kept guard freeze, but only through a journal built by the deleted planner (fx.plan, planNamespaceMigration) and written by the deleted writeNamespaceMigrationJournal; restoring them means holding the planner and journal writer on the test side. The guard's journal freeze is itself 2a-2 shrink material (573 -> 133). completion_test.go ("namespace" row), exit_test.go (the root-help substring check listed "up" and "stop", verbs removed in cut 4; it passed only because namespace's summary contained those substrings).
- Tests: 2,248 -> 2,230 listed; 34 packages ok; 84 s wall.

### Cut 10: C3, C17, C10 extras (four of six files deferred)

One commit for the three small extra clusters (each probe-checked file by file).
- Deleted non-test (2 files, 440 lines): global_status_doctor.go (224, C3; doctor's "global NOC registration" check, one caller line cut in doctor.go doctorCheckWake), operator_handoff_card.go (216, C17; the #493 handoff card printed at the end of `team launch`, two call sites cut in team_launch.go).
- Deferred, each load-bearing for a kept command:
  - `status_board.go` (C3, 738 now): runStatusBoard / runStatusBoardWithVersion are the bare `amq-squad` entry (cli.go, a keep file) and the no-`--session` mode of `status`; defaultBaseRootName is used by amq_env.go.
  - `operator_delivery.go` (C17, 117): operatorDeliveryData and operatorDeliveryForTeam(AtRoot) are the operator_delivery block of the status JSON, the bootstrap prompt, `operator` and `team show`; the whole file is needed.
  - `live_identity.go` (C10, 323): verifyRuntimeActionByHandle (dispatch.go, two sites), bindLaunchWakeRecord (launch.go host), errIncompleteLaunchRecord (pane_cleanup.go).
  - `version_alignment.go` (C10, 318): the `versions` field of the doctor and status JSON envelopes (buildVersionAlignment, versionAlignmentData, versionAlignmentSources) and doctor's defaultClaudeSkillCacheRoot.
- Deleted test files (1, 180 lines): operator_handoff_card_493_test.go (all seven tests rendered the deleted card).
- Holding file: no additions.
- Surviving files edited (trimmed): doctor.go 1,842 -> 1,841, team_launch.go 1,039 -> 1,031.
- Test files edited: global_status_test.go (TestDoctorGlobalNOCRegistrationProjection, it called the deleted doctorCheckGlobalNOCRegistrationAtRoot; nothing else).
- Tests: 2,230 -> 2,222 listed; 34 packages ok; 76 s wall. linux and windows builds green.

### Cut 11: C2 runtime control entry points (all five files deferred)

- Entry points removed: registry `focus`, `open`, `send`, `broadcast` (command_registry.go 97 -> 89). runFocus, runSend (runtime_actions.go) and runBroadcast (operator_control.go) are unreachable from here on; their files stay, see below.
- Deleted non-test: none. A file-by-file probe (each file removed alone, scratchpad probe1.sh) showed every file load-bearing for a kept command:
  - `runtime_actions.go` (639): dispatch.go delivers through memberRuntime, resolveMemberRuntime, resolveControlTarget, sendPromptToPane, paneBusyForSend, errTmuxAccessDenied (16 sites); sameResolvedDir is used by doctor.go, launch.go, status.go, team_launch.go, namespace_conflict.go; readPromptBody / stdinIsInteractive by dispatch.go and operator.go (`operator directive`). The manifest extract (resolveProjectProfile, compareCWD, sameResolvedDir, readPromptBody) is the generic part; the rest is dispatch's pane delivery, which is the 2a-2 C19 shrink's to cut.
  - `runtime_identity.go` (164): launchRuntimeIdentity / classifyLaunchRuntimeIdentity / launchRuntimeProbe(FromDuplicate) / classifyLaunchPIDRuntimeIdentity are the liveness classification under status.go, liveness.go, global_noc.go, global_status.go, context_resolution.go, preflight.go, team_launch.go and the holding file.
  - `runtime_json.go` (454): runtimeActionJSON, tmuxRuntimeJSON, terminalRuntimeJSON, sessionActions, statusTopologyForRows and the pane-liveness fill helpers are the status and global-status JSON shapes (about 30 sites in status.go and global_status.go).
  - `operator_control.go` (393): runOperatorSend is the whole of `operator send`, a subcommand of the kept `operator` verb that sends a durable AMQ message. The manifest extract keeps it "if broadcast stays as a verb"; broadcast is unregistered here, but cutting `operator send` removes a kept subcommand, so it waits for a ruling (cut the `operator send` arm, or keep it with the 2a-2 C17 shrink).
  - `visibility.go` (107, deferred since cut 4): consumers runtime_json.go (deferred above), layout_preset.go (C1), team.go (visibilityPreviewLaunchCommand).
- Test files edited: completion_test.go (focus, open, send, broadcast rows of the catalog-derived table). Tests that call runSend/runFocus/runBroadcast directly still compile and pass; they go with their files in 2a-2.
- Tests: 2,222 listed (unchanged); 34 packages ok.

### Cut 12: C2 follow-up, operator_control.go (lead ruling)

- Ruling: follow the manifest condition; broadcast is gone, so `operator send` goes. Operator-to-seat messages in 3.0 go through the kept `dispatch` verb and the gate flow, not a bare send. Had a kept test or command still needed the durable-send helper itself, the helper would have moved verbatim to the holding file; none does (probe clean after the cut).
- Deleted non-test (1 file, 393 lines): operator_control.go (runOperatorSend, runBroadcast and their helpers).
- Surviving files edited (trimmed): operator.go (the `send` dispatch arm and its unknown-subcommand hint entry). printOperatorUsage text may still mention send; 2a-2 C17 shrink.
- Deleted test files (1): operator_control_test.go (four `operator send` tests and two broadcast tests, all drive the deleted subcommands).
- Test files edited: help_test.go (expected unknown-subcommand hint without 'send').
- Tests: 2,222 -> 2,216 listed; 34 packages ok; 73 s wall.

### Cut 13: C1 tmux, terminal, layout, wake, panes (12 of 25 files; 13 deferred)

- Method: all 25 files removed, cumulative probe, then the files a kept caller needs restored one group at a time until the surviving-file undefined list was empty.
- Deleted non-test (12 files, 2,215 lines): claude_rename.go (143), layout_finalize.go (224), layout_preset.go (111), orchestrate.go (404; the unregistered `global start|status` group), orphan_panes.go (89), run_shell_silence.go (31), run_start_terminal_context.go (59), team_launch_iterm2.go (198), team_launch_native_terminal.go (36), team_launch_terminal.go (175), team_launch_tmux_session.go (417), team_member_control_continue.go (328; unreachable since cut 8).
- Entry points removed: lookupCommand hook for the Claude session-rename helper (command_registry.go 89 -> 86). Backends `iterm2`, `terminal`, `tmux-session` leave teamLaunchBackends (their registrations lived in the deleted files); `tmux` and `launchapi` remain.
- Callers cut in surviving files (trimmed): launch.go 1,181 -> 1,178 (maybeScheduleClaudeSessionRename call), doctor.go 1,841 -> 1,798 (doctorCheckOrphanPanes and its call; the "orphan panes" doctor row), global_status.go 742 -> 720 (runGlobalStatus, whose only caller was runGlobal in orchestrate.go; executeGlobalStatus stays, its tests use it).
- Deferred, each needed by a kept caller:
  - `team_launch_tmux.go` (1,096): the tmux backend of kept team_launch_launchapi.go (tmuxLaunchPlan, runTmuxLaunchPlanWithResult, tmuxTeamLaunchBackend, defaultTmuxSessionName), stampCapturedLaunchPane (launch.go, team_launch.go), tmuxControlModeClients (status.go), paneTitleToken (runtime_actions.go, runtime_identity.go), tmuxOutputCommand (runtime_json.go). 2b rewrite per the manifest extract.
  - `bootstrap_failure.go` (402): waitForPaneBootstrap, tmuxBootstrapProbes, bootstrapFailureError, printBootstrapFailureRecovery for team_launch_tmux.go.
  - `tmux_exact_id.go` (27): exactTmuxPaneID / exactTmuxWindowID for team_launch.go, pane_cleanup.go, team_launch_tmux.go (whole file needed).
  - `wake_inject.go` (42): normalizeWakeInjectMode, resolveWakeInjectModeForBinary, validateWakeInjectConfig for launch.go, restore.go, team.go, preview_flags.go (whole file needed).
  - `wake_lock_compat.go` (171): wakeLockFile, decodeWakeLockFile, wakeLockSameMachine, wakeLockHasStateBinding for status.go, preflight.go, live_identity.go, the holding file.
  - `pane_adopt.go` (78): adoptLivePane (status.go), childrenPidTree / verifiedAgentPID (status.go, runtime_actions.go).
  - `pane_cleanup.go`, `pane_cleanup_rm.go`, `pane_cleanup_manifest.go`, `pane_cleanup_manifest_unix.go`, `pane_cleanup_manifest_other.go`, `pane_cleanup_manifest_rename_darwin.go`, `pane_cleanup_manifest_rename_linux.go` (1,442 together): the rm pane-cleanup flow of the session_reset_support.go host (about 30 symbols); 2a-2 with the C11 host shrink.
- Holding file additions (v3_holding.go 713 -> 828): listExactTmuxControlClients, readExactTmuxControlClients, parseExactTmuxControlClients, validateTmuxControlClientName (team_member_control_continue.go:238-301; runtime_json.go, deferred), iterm2OutputCommand, focusITerm2Window, iterm2FocusArgv (team_launch_iterm2.go:27, 168-198; runFocus in runtime_actions.go, unregistered since cut 11; drop with it), layoutFinalizationWarningPath (layout_finalize.go:196-202; status.go).
- Deleted test files (10): claude_rename_test.go, layout_finalize_test.go, layout_preset_test.go, orphan_panes_test.go, run_shell_silence_test.go, team_launch_terminal_test.go, team_member_control_continue_tmux_test.go (emptied by the sweep or helper-only), team_launch_typed_delivery_test.go (its NOC dispatch test drove orchestrate.go; its send-keys scan found no production send-keys site left, so the check went vacuous and its stale "noc-bootstrap-dispatch" exemption named a deleted site).
- Test files edited, removals checked against their triggers (sweep log in the scratchpad): global_status_test.go (two runGlobalStatus tests), team_launch_iterm2_test.go, team_launch_tmux_session_test.go (launch-argv, dry-run and registration tests of the deleted backends; TestITerm2FocusArgvShape and TestDefaultTmuxBackendStillEmitsSplitPanePlan stay), team_launch_pane_verify_test.go (TestTmuxSessionAttributesTheCreatedPaneOrRefuses, tmux-session backend), team_member_control_continue_test.go (three resolve/continue tests of the deleted command; the client-name and status-action tests stay), launch_identity_stamp_test.go (TestEveryMismatchWrapperGuardsTheIncompleteSentinel: every wrap site it policed was in team_member_control_continue.go, so it went vacuous), restore_test.go (TestLaunchArgsFromRecordPreservesClaudeIdentityForRenameOnResume restored minus its claudeSessionRenameName assertion), team_launch_pane_delivery_test.go (anti-vacuity floor 3 -> 2 delivery sites: the session backend's site is gone), v2310_legacy_still_reachable_test.go (expected backends now tmux, launchapi).
- Flagged, not restored: TestRunTmuxCurrentWindowMapsExactResultToConfiguredNonFirstLead and TestRunTmuxOneWindowMapsExactResultToConfiguredNonFirstLead (team_launch_tmux_result_test.go) exercise the deferred tmux backend, but build their input with the deleted layout planner (buildLayoutFinalizationPlan, runStartLayoutSelection, layout presets).
- Tests: 2,216 -> 2,161 listed; 34 packages ok; 87 s wall. linux and windows builds green.

### Cut 14: C26 profiles.go (C23 needs no commit)

- Deleted non-test (1 file, 144 lines): profiles.go. Manifest reason: "cut: context resolution kept, simplified by squad.yaml"; following the manifest as for `operator send` (lead ruling at cut 12), the `team profiles` subcommand goes with it.
- Entry point removed: the `team profiles` dispatch arm and its unknown-subcommand hint entry (team.go, trimmed).
- Holding file addition (v3_holding.go 828 -> 843): resolveProfileFlag (profiles.go:30-42; context_resolution.go, team.go). The manifest extract (resolveScopedCommandContext) lives in context_resolution.go and is untouched.
- Test files edited: profiles_test.go (four runTeamProfiles tests; the 20 tests of kept profile-scoped status, dispatch, init and sync code stay, plus TestResolveProfileFlag for the held function), json_envelopes_test.go (four team_profiles envelope tests), help_test.go and init_test.go (team profiles help rows).
- C23 (agent.go) needs no commit: agent.go went in cut 4 (ruling 6); its extract (claudeInScopePreauthAllowlist) sits in agent_defaults.go, a kept file.
- Tests: 2,161 -> 2,153 listed; 34 packages ok; 82 s wall.

### Cut 15: C20 collect_* lock and support files

- Deleted non-test (4 files, 690 lines): collect_lock_exclusive.go (59), collect_lock_unix.go (51), collect_lock_windows.go (9), collect_support.go (571). Their only callers were notification_amq_watch.go (cut 6). evidence.go (fate replace) is untouched; the manifest extract (evidenceSecretName, evidenceTaskLink) lives there.
- Surviving files edited: none. Holding file: no additions. darwin, linux and windows builds green.
- Deleted test files (2): collect_lock_exclusive_test.go, collect_support_test.go (seven collect-journal tests; the helper fixtures left had no other user).
- Tests: 2,153 -> 2,146 listed; 34 packages ok; 75 s wall.

### Cut 16: C24 completion

- Deleted non-test (1 file, 996 lines): completion.go. Registry `completion` removed (command_registry.go 86 -> 84).
- Surviving files edited (trimmed): command_registry.go only. Holding file: no additions.
- Deleted test files (1): completion_test.go (shell-script and flag-coverage tests of the deleted generator). Its TestCompletionTopCommandsMatchesPublicCatalog pins the public catalog, not completion: it moved to cli_test.go reading commandNames("") (what completionTopCommands was), minus the "completion" row.
- Test files edited: cli_test.go (completionTopCommands references dropped from two registry tests), deprecation_test.go (TestCompletionDropsRemovedVerbs), output_test.go (TestCompletionContainsGlobalFlags), gate_test.go (TestGateListKindsIsContextFreeAndCatalogDriven restored minus its completion-script loop), role_draft_test.go (TestRoleCommandIsPublicAndCompletable restored minus its completion checks), exit_test.go and help_test.go (completion help rows).
- Tests: 2,146 -> 2,135 listed; 34 packages ok; 62 s wall.

### Cut 17: package internal/act

- `go list`/import grep: no importer, test or non-test (operator_control.go, its last importer, went in cut 12).
- Deleted: internal/act/control.go (400 lines), control_test.go. Manifest: 1 entry flipped. No surviving file edited.
- darwin, linux and windows builds green; tests 2,135 -> 2,130 listed; full gate green.

### Cut 18: package internal/autonomy

- Import grep: no importer, test or non-test (namespace_migration_plan.go, its last importer, went in cut 9).
- Deleted: internal/autonomy/autonomy.go (283 lines), autonomy_test.go. Manifest: 1 entry flipped. No surviving file edited.
- darwin, linux and windows builds green; tests 2,130 -> 2,117 listed; full gate green.

### Cut 19: package internal/wizard (with routing.go)

- Import grep: no importer, test or non-test (the last ones, resume_goal.go and run_start_terminal_context.go, went in cuts 3 and 13).
- Deleted: all 13 non-test files (4,962 lines): bubbletea.go (1,942), capabilities.go (100), composition_policy.go (39), discovery.go (249), goal_binding.go (98), launch_shape.go (90), numbered.go (1,018), previews.go (32), profile_flow.go (468), routing.go (308), spec.go (453), terminal_context.go (125), tool_policy.go (40); 15 test files and testdata/topology.golden (used only by the package's own tests). Manifest: 13 entries flipped.
- routing.go: lead ruling (second reviewer): its manifest row is fate delete, target 0, and nothing kept imports it, so it goes with the package; "no 3.0 home named" is not a reason to keep it. The routing-table ancestor stays readable in git history (v3 at 74d3e64).
- No surviving file edited. darwin, linux and windows builds green; tests 2,117 -> 1,971 listed; full gate green.

## Deferred (running list)

- `internal/cli/visibility.go` (C11, 107 lines): consumers runtime_json.go (C2, deferred), layout_preset.go (C1), team.go; 2a-2.
- `internal/cli/runtime_actions.go` (C2, 639): dispatch.go pane delivery, sameResolvedDir in five kept files, readPromptBody; 2a-2 with the C19 shrink.
- `internal/cli/runtime_identity.go` (C2, 164): liveness classification in status, liveness, global NOC/status, preflight, team launch; 2a-2.
- `internal/cli/runtime_json.go` (C2, 454): status and global-status JSON runtime shapes; 2a-2 with the C16 shrink.
- `internal/cli/lead_identity.go` (C9, 145 lines): 10 of 13 symbols called by kept amq.go, operator_identity.go, status.go, team_launch.go, bootstrap.go; 2a-2 with the C14/C17 shrinks (or the holding file, if ruled).
- `internal/cli/team_overlay.go` (C18, 1,310 lines): validateMemberOverlayPaths and the tool-policy drift predicate behind it are called by kept team.go, team_launch.go, doctor.go; 2a-2 with the C18 shrink.
- `internal/cli/namespace_conflict.go` (C7, 514 lines): conflict guards called by kept gate_close.go, dispatch.go, operator.go, task.go, launch.go, team.go, status.go and the guard host; 2a-2.
- `internal/cli/namespace_open_other.go` (C7, 18 lines): `!unix` twin of the kept namespace_open_unix.go; the guard host needs it on windows; 2a-2 with the guard shrink.
- `internal/cli/status_board.go` (C3): bare `amq-squad` and `status` without `--session` (cli.go keep, status.go); defaultBaseRootName for amq_env.go; 2a-2.
- `internal/cli/operator_delivery.go` (C17, 117): operator_delivery data in status JSON, bootstrap, operator, team show; 2a-2.
- `internal/cli/live_identity.go` (C10, 323): dispatch.go verifyRuntimeActionByHandle, launch.go bindLaunchWakeRecord, pane_cleanup.go errIncompleteLaunchRecord; 2a-2.
- `internal/cli/version_alignment.go` (C10, 318): `versions` in the doctor and status JSON envelopes; 2a-2.
- C1 (13 files): team_launch_tmux.go and bootstrap_failure.go (tmux backend of kept launchapi; 2b), tmux_exact_id.go, wake_inject.go, wake_lock_compat.go, pane_adopt.go (kept launch, status, preflight callers), the seven pane_cleanup*.go files (session_reset_support.go host); 2a-2.
- `internal/cli/preview_flags.go` (C4, 193 lines): kept callers team_launch.go (runTeamLaunch) and team.go (runTeamShow); 2a-2 with the C18 shrink.
- `internal/cli/simple_start_brief_draft.go` (C4, 236 lines): kept callers brief.go (`--goal`) and team_rules_draft.go (cliDrafterRunner, nonEmptyTrimmedLines); 2a-2 with the C27 shrink.
- Helper-only test files left by the sweeps (compile, no tests): pane_cleanup_down_wiring_test.go, pane_lifecycle_test.go, real_amq_wake_compatibility_test.go, new_profile_flag_forwarding_test.go, v228_contract_crash_injection_test.go, v228_contract_goal_delivery_step3_test.go, v228_contract_legacy_namespace_test.go, v228_contract_restore_step3_test.go, v228_contract_start_harness_test.go; 2a-2 test cleanup.

- Packages with a surviving non-test importer after all file cuts: recorded as they are reached.

## Holding file debit for 2a-2

`internal/cli/v3_holding.go` (843 lines after cut 14), updated per cut:
- (cut 14) resolveProfileFlag (profiles.go:30-42).
- (cut 13) listExactTmuxControlClients, readExactTmuxControlClients, parseExactTmuxControlClients, validateTmuxControlClientName (team_member_control_continue.go:238-301); iterm2OutputCommand, focusITerm2Window, iterm2FocusArgv (team_launch_iterm2.go:27, 168-198; drop with runFocus); layoutFinalizationWarningPath (layout_finalize.go:196-202).
- (cut 9) namespaceMigrationArtifact, namespaceMigrationInputEvidence, namespaceMigrationLiveness, namespaceMigrationPlan, dedupeMigrationStrings, canonicalMigrationProject (namespace_migration_plan.go:25-73, 219-233, 632-644); readNamespaceMigrationJournal, rejectMigrationSymlinkComponents (namespace_migration_tx.go:376-400, 591-615); all for the guard host.
- (cut 9, test side) namespaceMigrationBaseRoot (namespace_migration_adapters.go:79-84).
- (cut 8) memberFieldChange (team_member_diff.go:11-17); wantsHelp, orDash, peelPositional, shellJoin, resolveExistingTeamProfile, withProfileLock, inheritedSession, memberCWDOverride (team_member.go:78-1051); currentPaneIdentity, memberByRole (team_lead.go:22, 1082-1089); configuredTeamProfiles (team_rm.go:187-198, drop with C3).
- (cut 8, test side) wakeDrainInject (team_lead.go:62-76) in v3_holding_test.go.
- (cut 5) peelProjectFlag, runInProject (new.go:488-525, 596-621).
- (cut 5) wakeHealthForEntry, looksActive, readPresenceForEntry, presencePath (list.go:146-215).
- (cut 5) simpleStartDependencies (REDUCED to ResolveDrafter, RunDrafter), defaultSimpleStartDependencies, normalizeSimpleStartDependencies (simple_start.go:60-73, 94-107, 110-172).
- (cut 5) writeSimpleStartDrafterAttempts (simple_start.go:1291-1308).
- (cut 5) simpleStartCheckpoint + consts, simpleStartCheckpointError (Error, Unwrap), callSimpleStartCheckpoint (simple_start.go:30-58) — dead path, drop with C1.
- (cut 5, test side) validSimpleStartBriefDraft (simple_start_test.go:1483-1495) in v3_holding_test.go.
- goalOrchestratorRole, defaultGoalOrchestratorHandle (goal.go:314-317), 2 consts.
- profileInitCommand (up.go:404-411).
- teamRoleList (resume_shared.go:37-44) — temporary, drop with the C4 cut.
- canonicalPath, compactNativeValue (resume_shared.go:46-63).
- findMemberRestoreRecord (resume_shared.go:65-107).
- downStatus, processTerminator, signalTerminator, newSignalTerminator, Terminate, SignalName, signalNameOf (down.go:22, 58-69, 90-134).
- downAgentJSON (down.go:1613-1616) — drop with the C1 cut.

`internal/cli/v3_holding_test.go` (255 lines): chdir, seedTeam, seedTeamWithoutSharedCwdException, fakeBackend, useFakeBackend, recordingTerminator, selfCleaningTerminator, downFakeProbe, seedAgentRecord, writeMemberLaunchRecord, resumeChdir.
