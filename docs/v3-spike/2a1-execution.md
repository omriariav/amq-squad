# amq-squad 3.0, phase 2a-1 execution log

Branch `v3-2a1`, cut from `v3` at 74d3e64. Work list: `docs/v3-spike/2a1-worklist.md`. Method: plan 4.1.
Note: a history rewrite on v3 on 2026-10-22 (about 12:5x) rewrote every hash on this branch; the hashes cited for cuts 1-4 below are pre-rewrite (post-rewrite: cut 1 525b139, cut 2 2df5fd0, cut 3 42642a8, cut 4 180f2d8; branch point ded3182). Later entries cite post-rewrite hashes.
Implementer change: after cut 5 the first implementer stopped on a rate limit; one successor left an uncommitted partial cut 6 (saved by the lead as WIP commit abadb4b) and made no commit of its own; the third implementer finished cut 6 from that WIP (amended into the cut 6 commit) and continues from there.
One cut = one commit; each commit green on `go build ./... && go vet ./... && go test ./... && scripts/manifest-check.sh`.

## Status at the end of this run

- 21 cuts plus the module tidy and the test-restore commit; every committed head green on build, vet, test and manifest-check (darwin; linux and windows builds green from cut 9 on).
- Non-test lines under internal/: 110,929 -> 72,982. The worklist expected about 61,035 after 2a-1; the gap is the deferred delete-fate files still present (42 files, 11,702 lines, listed below with their kept callers) plus the holding file (832), less the lines cut from trimmed survivors (614): 61,035 + 11,702 + 832 - 614 = 72,955 (measured 72,982; the 27-line rest is the difference between manifest v2 figures and current sizes of files trimmed and later deleted).
- Listed tests: 3,024 -> 2,027 (reconciled in the test-sweep re-audit below).
- go.mod: charmbracelet bubbletea, bubbles, lipgloss and fsnotify gone (cut 20).

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

### Cut 20: go mod tidy (plan 4.1 step 6)

- `go mod tidy` (GOPROXY=off; nothing downloaded) after console (cut 1) and wizard (cut 19) left no charmbracelet importer.
- go.mod: direct requires dropped: github.com/charmbracelet/bubbles v1.0.0, github.com/charmbracelet/bubbletea v1.3.10, github.com/charmbracelet/lipgloss v1.1.0, and github.com/fsnotify/fsnotify v1.10.1 (its importers were cut too; the worklist experiment did not list it). The whole indirect block (20 entries: atotto/clipboard through golang.org/x/text v0.14.0) goes. Remaining requires: avivsinai/agent-message-queue v0.75.0, golang.org/x/sys v0.47.0.
- go.sum: 50 lines removed. `go mod verify`: all modules verified. darwin, linux, windows builds, vet, tests and manifest-check green.

### Cut 21: internal/commandevidence/doc.go (package stays)

- The package stays (evidence.go is fate replace; importers internal/cli/evidence.go, internal/task/command_evidence.go); its only delete-fate file, the package doc comment, goes. No code depended on it.
- Deleted: internal/commandevidence/doc.go (7 lines). Manifest: 1 entry flipped. Gate green on all three platforms; test count unchanged (1,971).
- Not cut, recorded: internal/attention/dedup.go (119, fate delete): Select, Commit, NormalizeDeliveryError, State, Item, Delivery are used by kept internal/cli/operator_attention.go (C17 shrink); 2a-2 with that importer.

### Test-sweep re-audit (ruling 5; restore commit)

- Method: for every commit ded3182..HEAD, every top-level Test/Example/Fuzz/Benchmark function present in a `_test.go` before the commit and absent after it (files deleted or trimmed) was listed (1,054 functions). For each, the package-local identifiers it references were split into those that still exist in the kept tree and those that do not (the deleted symbols it exercises). Rows whose deleted symbols were empty, or only incidental (a seam, a fixture path, a table row, a wrapper verb that delegated to a kept verb), were restored and run; rows driving a deleted verb or asserting deleted state stay removed, with the symbol named. Scripts (scratchpad): testaudit.py, tryrestore.py, mktable.py; per-test transforms in specB/C/D.json.
- Reconciliation: 3,024 listed at the baseline; 1,054 functions removed over cuts 1-19; 1 moved (TestCompletionTopCommandsMatchesPublicCatalog, cut 16); 56 restored in this commit; 9 restored inside the cut 6 commit itself (never net-removed, first rows of the table). 3,024 - 1,054 + 1 + 56 = 2,027 listed now (`go test ./... -list`). Every one of the 997 functions still removed exercises deleted code; the table names the symbol or the deleted verb for each.
- Restored here (56), by kind:
  - verbatim, no deleted reference (15): TestBootstrapContextCanonicalizesRuntimeCoordinates (bootstrap_test.go), TestSelectStatusLaunchRecordRequiresExactHandleIdentity, TestStatusTreatsExternalLeadPaneAsLiveAndActionable, TestStatusKeepsActionsAvailableWhenAgentPIDVerifiesButTmuxScanMisses (status_test.go), TestSimpleStartCheckpointWrapsSentinel (v3_holding_test.go, it tests held code), TestStartApplyRequiresExactDecisionsForEveryRequiredAction (team_launch_launchapi_test.go), TestValidateSimpleStartBriefDraftExactShape, TestAllMarkdownBulletsAcceptsAsteriskAndPlusMarkers, TestNormalizeMarkdownBulletMarkerLeavesNonBulletProseUntouched (brief_test.go), TestNotificationReservationCoversConfiguredSinkTimeout (operator_attention_test.go), TestTeamLaunchDryRunSkipsRegisteredExternalLead, TestTeamLaunchAutoRegistersCurrentPaneExternalLead, TestTeamLaunchDoesNotAutoRegisterProjectLeadWithoutAMRoot, TestTeamInitAcceptsSharedCwdExceptionFlag, TestSimpleStartCommandPinsCanonicalInputsAndExactInstruction (team_test.go). They lived in basename test files of deleted code (simple_start_test.go, team_lead_test.go, team_shared_cwd_test.go, notification_watcher_test.go) but test kept functions.
  - deleted reference taken out (12): TestValidateOperatorLaunchRecordAuthorizationAndRuntimeBinding, TestStatusJSONProvesExternalOrchestratorBinding, TestNamedProfileConflictBlocksDirectRuntimeCommands, TestParseFlagsWrapsUnknownFlag (example command up -> doctor), TestParseFlagsUsageErrorAcrossCommands, TestScopedShortFlagAliasesRegistered (the table test cut 4 flagged), TestSameProfileSessionUpStatusDispatchNoNamespaceConflict, TestAMQNoisyFakeNativeJSONStartsAtByteZero, TestRoleControlCommandsRefuseOperatorTarget, TestRealAMQCompatibility (skips without AMQ_SQUAD_REAL_AMQ; two subtests of deleted wake helpers dropped), TestQuietDoesNotSuppressWarningsOrErrors and TestQuietDoesNotSuppressDryRunStdout (driver up --dry-run -> team show).
  - driver swapped to the kept verb the deleted one delegated to (29): `new team` / `new profile` -> `team init` (new.go delegated to it): 15 custom_role_test.go tests, 3 effort_test.go tests, 3 worktree_isolation_reachability_test.go tests plus their seed helpers; `up --dry-run` -> `team show` (same plan): 4 team_overlay_test.go tool-policy tests, TestEmittedCommandsCarryDefaultProfileImplicitly, 3 json_envelopes_test.go envelope tests.
- Tried and not restored (they fail once the deleted part is gone, so the kept part was reached only through deleted code): TestExecuteStatusJSONReportsBlockedNativeGoalForLiveLeadRecord (open_blockers came from the deleted goal-binding projection), TestRunUpProfileDryRunUsesNamedRoster and TestRunUpProfileUnknownErrors (up's --profile flag; team show has none), TestSharedCwdExceptionFixCommandExecutesAsDisplayed (runs the deleted `team shared-cwd-exception`), three worktree-isolation tests that call the deleted `team member update`.
- Flagged for 2a-2 (kept subject, deleted fixture; not restorable without holding deleted code on the test side): the three guard-freeze tests of cut 9 (journal from the deleted planner), the two tmux-result tests of cut 13 (layout planner), the up-driven brief-seeding tests of cut 4 (`up --seed-from`; `team show` has no seeding, the `brief` verb is the kept surface).
- The full table follows at the end of this log (appendix).

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

- Packages with a surviving non-test importer after all file cuts (ruling: deferred to 2a-2 with the importer named):
  - `internal/runtimeaction` (154): global_status.go, namespace_conflict.go (deferred), runtime_json.go (deferred).
  - `internal/runtimecontrol` (551): liveness.go, status.go, runtime_json.go and runtime_actions.go (deferred), internal/runtimeaction.
  - `internal/liveidentity` (257): status.go, runtime_json.go and live_identity.go (deferred).
  - `internal/notifier` (155): operator_attention.go.
  - `internal/bootstrapack` (246): internal/launch/record.go, bootstrap.go (ruling 4 of the worklist).
  - `internal/tmuxpane` (1,686): dispatch.go, doctor.go, global_noc.go, launch.go, operator_attention.go, status.go, team_launch.go, the holding file, and the deferred runtime_actions.go, runtime_json.go, team_launch_tmux.go, bootstrap_failure.go, pane_cleanup.go, pane_adopt.go.
  - `internal/attention/dedup.go` (119; the package is shrink): operator_attention.go.
  - `internal/commandevidence`: only doc.go was delete-fate (cut 21); evidence.go is replace.

## Holding file debit for 2a-2

`internal/cli/v3_holding.go` (832 lines at the end of the run), updated per cut:
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
- teamRoleList (resume_shared.go:37-44): dropped in the closing commit (its callers went in cut 5; it had been left behind).
- canonicalPath, compactNativeValue (resume_shared.go:46-63).
- findMemberRestoreRecord (resume_shared.go:65-107).
- downStatus, processTerminator, signalTerminator, newSignalTerminator, Terminate, SignalName, signalNameOf (down.go:22, 58-69, 90-134).
- downAgentJSON (down.go:1613-1616) — drop with the C1 cut.

`internal/cli/v3_holding_test.go` (319 lines at the end of the run; 255 after cut 4, then validSimpleStartBriefDraft, wakeDrainInject, namespaceMigrationBaseRoot and the restored TestSimpleStartCheckpointWrapsSentinel): chdir, seedTeam, seedTeamWithoutSharedCwdException, fakeBackend, useFakeBackend, recordingTerminator, selfCleaningTerminator, downFakeProbe, seedAgentRecord, writeMemberLaunchRecord, resumeChdir.

## Appendix: test-sweep audit table (ruling 5)

Summary per cut, then one row per removed or restored test function.

| cut | tests removed (net of the commit) | restored in this commit or later |
| --- | --- | --- |
| 1 | 67 | 0 |
| 2 | 11 | 0 |
| 3 | 159 | 3 |
| 4 | 209 | 17 |
| 5 | 143 | 29 |
| 6 | 72 | 1 |
| 7 | 9 | 0 |
| 8 | 106 | 6 |
| 9 | 18 | 0 |
| 10 | 8 | 0 |
| 12 | 6 | 0 |
| 13 | 55 | 0 |
| 14 | 8 | 0 |
| 15 | 7 | 0 |
| 16 | 12 | 1 |
| 17 | 5 | 0 |
| 18 | 13 | 0 |
| 19 | 146 | 0 |

| cut | file | test | deleted symbols it exercises (generic names filtered) | survives in the kept tree? | restored |
| --- | --- | --- | --- | --- | --- |
| 6 (WIP) | global_status_test.go | TestGlobalStatusProjectsMixedRunsFromOneRegistrySnapshot | notificationWatcherStatus / notificationWatcherRuntimePath (seam or path only) | yes, the subject is kept | yes, minus the Watcher seam, the watcher run row and its assertion |
| 6 (WIP) | global_status_test.go | TestGlobalStatusNamedProfileUsesIsolatedAMQBaseRoot | notificationWatcherStatus / notificationWatcherRuntimePath (seam or path only) | yes, the subject is kept | yes, minus the Watcher seam |
| 6 (WIP) | global_status_test.go | TestGlobalStatusMalformedLeadClassificationIsUnknownAndHumanVisible | notificationWatcherStatus / notificationWatcherRuntimePath (seam or path only) | yes, the subject is kept | yes, minus the Watcher seam |
| 6 (WIP) | global_status_test.go | TestGlobalStatusContradictorySourcesFailClosed | notificationWatcherStatus / notificationWatcherRuntimePath (seam or path only) | yes, the subject is kept | yes, minus the Watcher seam |
| 6 (WIP) | global_status_test.go | TestGlobalStatusTeamProjectContradictionStopsCrossRootProjection | notificationWatcherStatus / notificationWatcherRuntimePath (seam or path only) | yes, the subject is kept | yes, minus the Watcher seam and watcherRead |
| 6 (WIP) | parent_amq_floor_test.go | TestTeamLaunchRejectsPreFloorAMQBeforeParentMutations | notificationWatcherStatus / notificationWatcherRuntimePath (seam or path only) | yes, the subject is kept | yes, minus notificationWatcherRuntimePath |
| 6 (WIP) | parent_amq_floor_test.go | TestTeamLaunchDryRunReportsPreFloorAMQAndRendersPreviewWithoutMutations | notificationWatcherStatus / notificationWatcherRuntimePath (seam or path only) | yes, the subject is kept | yes, minus notificationWatcherRuntimePath |
| 6 (WIP) | parent_amq_floor_test.go | TestExternalLeadLateMemberPreflightRejectsBeforePaneStampOrLaunchRecord | notificationWatcherStatus / notificationWatcherRuntimePath (seam or path only) | yes, the subject is kept | yes, minus notificationWatcherRuntimePath |
| 6 (WIP) | namespace_migration_test.go | TestNamespaceMigrationFreezesPreviouslyUnguardedWriters | notificationWatcherStatus / notificationWatcherRuntimePath (seam or path only) | yes, the subject is kept | yes, minus two watcher rows (later removed with C7 in cut 9: journal built by the deleted planner) |
| 1 | console_test.go | TestUpdateReplacesSnapshotOnMsg | whole package `console` deleted | no | no |
| 1 | console_test.go | TestUpdateBuildErrKeepsPriorSnapshot | whole package `console` deleted | no | no |
| 1 | console_test.go | TestUpdateQuitOnQ | whole package `console` deleted | no | no |
| 1 | console_test.go | TestUpdateWatchErrTriggersRebuild | whole package `console` deleted | no | no |
| 1 | console_test.go | TestViewRendersRollupHeadline | whole package `console` deleted | no | no |
| 1 | console_test.go | TestNoTeamScreenExplains | whole package `console` deleted | no | no |
| 1 | console_test.go | TestDebouncerCoalescesBurst | whole package `console` deleted | no | no |
| 1 | console_test.go | TestDebouncerResyncSticky | whole package `console` deleted | no | no |
| 1 | console_test.go | TestDebouncerIgnoresEmpty | whole package `console` deleted | no | no |
| 1 | console_test.go | TestClassifyWatchErrorAlwaysResyncs | whole package `console` deleted | no | no |
| 1 | console_test.go | TestWatchTargetsAreDirsNotLeaves | whole package `console` deleted | no | no |
| 1 | console_test.go | TestStaticBoardRendersRollupAndSessions | whole package `console` deleted | no | no |
| 1 | console_test.go | TestStaticBoardEmptyDiscovery | whole package `console` deleted | no | no |
| 1 | control_test.go | TestActionOverlayRequiresDedicatedConfirmationKey | whole package `console` deleted | no | no |
| 1 | control_test.go | TestConsoleConfirmFlowsProduceTypedActIntents | whole package `console` deleted | no | no |
| 1 | control_test.go | TestConsoleConflictedGateCannotReachConfirm | whole package `console` deleted | no | no |
| 1 | control_test.go | TestConsoleReplyUsesConfiguredOperatorHandle | whole package `console` deleted | no | no |
| 1 | control_test.go | TestActionResultMessageNeverMutatesSnapshot | whole package `console` deleted | no | no |
| 1 | filter_rollup_test.go | TestFilterSnapshotRecomputesRollup | whole package `console` deleted | no | no |
| 1 | filter_rollup_test.go | TestFilterAMQMetadata | whole package `console` deleted | no | no |
| 1 | filter_rollup_test.go | TestFilterStaleBlocked | whole package `console` deleted | no | no |
| 1 | run_test.go | TestRunOnceRendersStaticBoard | whole package `console` deleted | no | no |
| 1 | run_test.go | TestRunOnceNoTeamNoticeToStdout | whole package `console` deleted | no | no |
| 1 | run_test.go | TestRunOnceInitialFilterScopesBoard | whole package `console` deleted | no | no |
| 1 | run_test.go | TestRunInteractiveTTYFailureIsReported | whole package `console` deleted | no | no |
| 1 | run_test.go | TestStaticBoardShowsUnresolvedThreadRows | whole package `console` deleted | no | no |
| 1 | run_test.go | TestStaticBoardOmitsUnresolvedSectionWhenClear | whole package `console` deleted | no | no |
| 1 | run_test.go | TestStaticBoardRendersExternalEvidenceBeforeUnresolved | whole package `console` deleted | no | no |
| 1 | run_test.go | TestStaticBoardStoppedSessionLabelsAgentsStopped | whole package `console` deleted | no | no |
| 1 | run_test.go | TestStaticBoardRunningSessionStateAwareLabelsWithAge | whole package `console` deleted | no | no |
| 1 | run_test.go | TestStaticBoardHeadlineLabelsBlockedThreads | whole package `console` deleted | no | no |
| 1 | views_test.go | TestStaticBoardShowsAgentActivity | whole package `console` deleted | no | no |
| 1 | views_test.go | TestConsoleSurfacesAgedOperatorGate | whole package `console` deleted | no | no |
| 1 | views_test.go | TestEnterDrillsBoardToDetail | whole package `console` deleted | no | no |
| 1 | views_test.go | TestSpacePeeksReadOnly | whole package `console` deleted | no | no |
| 1 | views_test.go | TestEnterNeverPeeksOrAttaches | whole package `console` deleted | no | no |
| 1 | views_test.go | TestLEntersLogMode | whole package `console` deleted | no | no |
| 1 | views_test.go | TestTTogglesTimeline | whole package `console` deleted | no | no |
| 1 | views_test.go | TestEscIsBackNotQuit | whole package `console` deleted | no | no |
| 1 | views_test.go | TestEscClosesOverlay | whole package `console` deleted | no | no |
| 1 | views_test.go | TestQQuits | whole package `console` deleted | no | no |
| 1 | views_test.go | TestActionsAreInertAndShowCommands | whole package `console` deleted | no | no |
| 1 | views_test.go | TestActionPaletteForAgentIncludesControlAndDispatch | whole package `console` deleted | no | no |
| 1 | views_test.go | TestActionPaletteShowsUnavailableReasonAtNarrowWidth | whole package `console` deleted | no | no |
| 1 | views_test.go | TestActionPaletteIncludesNamedProfile | whole package `console` deleted | no | no |
| 1 | views_test.go | TestFilterPredicatesSelectRightSessions | whole package `console` deleted | no | no |
| 1 | views_test.go | TestFilterThreadPredicates | whole package `console` deleted | no | no |
| 1 | views_test.go | TestFilterEntryViaSlash | whole package `console` deleted | no | no |
| 1 | views_test.go | TestFilterBackspaceEdits | whole package `console` deleted | no | no |
| 1 | views_test.go | TestAgentFilterViaSlash | whole package `console` deleted | no | no |
| 1 | views_test.go | TestFilterEscCancels | whole package `console` deleted | no | no |
| 1 | views_test.go | TestSelectionSurvivesSnapshotReplace | whole package `console` deleted | no | no |
| 1 | views_test.go | TestSelectionFallsBackToNearestSameGroup | whole package `console` deleted | no | no |
| 1 | views_test.go | TestBoardSummaryRendersRollupCounts | whole package `console` deleted | no | no |
| 1 | views_test.go | TestBusRowRendersUrgencyShape | whole package `console` deleted | no | no |
| 1 | views_test.go | TestDetailRendersEdgesAndAgents | whole package `console` deleted | no | no |
| 1 | views_test.go | TestDetailRendersExternalEvidenceReadOnly | whole package `console` deleted | no | no |
| 1 | views_test.go | TestDetailOmitsExternalEvidenceWhenAbsent | whole package `console` deleted | no | no |
| 1 | views_test.go | TestHelpOverlay | whole package `console` deleted | no | no |
| 1 | views_test.go | TestRefreshNowIssuesRebuild | whole package `console` deleted | no | no |
| 1 | views_test.go | TestInteractiveStoppedSessionLabelsAgentsStopped | whole package `console` deleted | no | no |
| 1 | views_test.go | TestInteractiveRunningSessionStateAwareLabelsWithAge | whole package `console` deleted | no | no |
| 1 | views_test.go | TestAgentStateLabelDeadMailboxLiveKeepsDistinctLabel | whole package `console` deleted | no | no |
| 1 | views_test.go | TestInteractiveRunningSessionKeepsDeadMailboxLiveLabel | whole package `console` deleted | no | no |
| 1 | views_test.go | TestInteractiveHeadlineLabelsBlockedThreads | whole package `console` deleted | no | no |
| 1 | views_test.go | TestAttachOverlayStartsWithReadOnlyNotice | whole package `console` deleted | no | no |
| 1 | views_test.go | TestFooterActionsNotExecutable | whole package `console` deleted | no | no |
| 2 | harness_test.go | TestServerEnvStripsAmbientTmuxAndHarnessIdentity | whole package `tmuxharness` deleted | no | no |
| 2 | harness_test.go | TestCommandEnvUsesCapturedExactLauncherIdentity | whole package `tmuxharness` deleted | no | no |
| 2 | harness_test.go | TestExactIDValidationRejectsNamesAndMalformedIDs | whole package `tmuxharness` deleted | no | no |
| 2 | harness_test.go | TestResolveTmuxPathPrefersInheritedHarnessBinary | whole package `tmuxharness` deleted | no | no |
| 2 | harness_test.go | TestRoutedWrapperFailsClosedAndPreservesFormatArgument | whole package `tmuxharness` deleted | no | no |
| 2 | harness_test.go | TestRemoveOwnedRootRejectsAnythingOutsideDirectTempChild | whole package `tmuxharness` deleted | no | no |
| 2 | harness_test.go | TestCleanupRetainsPrivateRootWhenKillFails | whole package `tmuxharness` deleted | no | no |
| 2 | harness_test.go | TestStartRetainsPrivateRootWhenStartupKillFails | whole package `tmuxharness` deleted | no | no |
| 2 | harness_test.go | TestCloseRefusesReplacementServerOnSameSocket | whole package `tmuxharness` deleted | no | no |
| 2 | harness_test.go | TestNestedHarnessBypassesOuterRoutingWrapper | whole package `tmuxharness` deleted | no | no |
| 2 | harness_test.go | TestHarnessIsolatesNestedClientsAndTeardown | whole package `tmuxharness` deleted | no | no |
| 3 | bootstrap_test.go | TestBootstrapPromptReportsPromptGoalBindingForCodexVisibleLead | codexGoalControlPrompt | no | no |
| 3 | bootstrap_test.go | TestBootstrapGoalBindingModeStrictClaudeVerification | bootstrapGoalBindingMode | no | no |
| 3 | codex_convergence_fix_pins_test.go | TestContractRefusesANewAttemptIDItsDeliveryReaderRejects | completeNativeTransitionInput, goalAttemptPath, newRecoveryTransitionRecord, recoveryTransitionInput, recoveryTransitionKindRedeliver, validRedeliverBaseForAttempt | no | no |
| 3 | codex_convergence_fix_pins_test.go | TestContractStillAcceptsAReaderValidNewAttemptID | completeNativeTransitionInput, goalAttemptPath, newRecoveryTransitionRecord, recoveryTransitionInput, recoveryTransitionKindRedeliver, validRedeliverBaseForAttempt | no | no |
| 3 | codex_convergence_fix_pins_test.go | TestPlannerBlocksMigrationWhenTheSourceHoldsAnOrphanConsumedNativeCompanion | completeNativeTransitionInput, goalAttemptDir, namespaceMigrationPlannerOptions, newRecoveryTransitionRecord, planNamespaceMigration, recognizeRecoveryTransitionName (+2) | no | no |
| 3 | codex_convergence_fix_pins_test.go | TestMigrationRefusesAMalformedTransitionLikeCompanion | companionReservationBase, currentRecoveryTransitionPrefix, inspectNamespaceNativeRecoveryClaims, recognizeRecoveryTransitionName, recoveryNameMalformed | no | no |
| 3 | codex_convergence_fix_pins_test.go | TestMigrationAllowsALegacyConsumedCompanion | blocking, companionReservationBase, inspectNamespaceNativeRecoveryClaims, legacyRecoveryTransitionPrefix, recognizeRecoveryTransitionName, recoveryNameRecognized | no | no |
| 3 | codex_convergence_fix_pins_test.go | TestTheNativeClaimBlockerNeverAdvisesDeletingAReservation | completeNativeTransitionInput, inspectNamespaceNativeRecoveryClaims, newRecoveryTransitionRecord | no | no |
| 3 | codex_convergence_fix_pins_test.go | TestDeliveryBoundaryRegatesAfterTheDirectiveAndImmediatelyBeforeInput | GoalSupervisionAssessment, newSupervisionResumeDelivery, supervisionResumeRefusal, u6DeliveringFixture | no | no |
| 3 | codex_convergence_fix_pins_test.go | TestGateRefusalAfterTheDirectiveTypesNothingAndIsDefiniteNonDelivery | GoalSupervisionAssessment, newSupervisionResumeDelivery, supervisionDefiniteNonDeliveryError, supervisionResumeRefusal, u6DeliveringFixture | no | no |
| 3 | codex_convergence_fix_pins_test.go | TestDeliveryBoundaryRefusesWhenNoGateIsSupplied | GoalSupervisionAssessment, newSupervisionResumeDelivery, supervisionDefiniteNonDeliveryError, u6DeliveringFixture | no | no |
| 3 | codex_convergence_fix_pins_test.go | TestDirectiveFailureIsReportedAsProvenNonDeliveryNotUnknown | GoalSupervisionAssessment, digestGoalSupervisionString, executeSupervisionResume, newSupervisionResumeDelivery, passingGenerationReader, passingPaneReader (+5) | no | no |
| 3 | codex_convergence_fix_pins_test.go | TestPostDirectiveGateRefusalReportsBothTheProofAndTheGateClause | GoalSupervisionAssessment, digestGoalSupervisionString, executeSupervisionResume, passingGenerationReader, passingPaneReader, readReservedRecoveryTransition (+4) | no | no |
| 3 | codex_convergence_fix_pins_test.go | TestBothAmbiguousPaneInputResultsProduceTheTypedUnknownAndReportUnknown | GoalSupervisionAssessment, digestGoalSupervisionString, executeSupervisionResume, newSupervisionResumeDelivery, passingGenerationReader, passingPaneReader (+6) | no | no |
| 3 | codex_convergence_fix_pins_test.go | TestAnUntypedDeliveryErrorRemainsUnknownRatherThanDefinite | GoalSupervisionAssessment, digestGoalSupervisionString, executeSupervisionResume, passingGenerationReader, passingPaneReader, readReservedRecoveryTransition (+2) | no | no |
| 3 | external_orchestrator_lifecycle_test.go | TestExternalOrchestratorMailboxFailedInvocationIsUncertainAndNotReplayed | beginExternalOrchestratorLifecycle, ensureExternalOrchestratorMailbox, externalOrchestratorStateMailboxUncertain, readExternalOrchestratorRegistry, resolveGoalTargetOptions | no | no |
| 3 | goal_supervision_binding_revalidation_test.go | TestRevalidationPassesWhenTheClaimStillMatchesTheAssessment | revalidateNativeBindingAgainstAssessment, revalidationFixture | no | no |
| 3 | goal_supervision_binding_revalidation_test.go | TestRevalidationRefusesEveryDisagreeingBoundField | GoalSupervisionAssessment, revalidateNativeBindingAgainstAssessment, revalidationFixture | no | no |
| 3 | goal_supervision_binding_revalidation_test.go | TestRevalidationRefusesAnInternallyInconsistentClaim | recoveryTransitionKindRedeliver, resumeGoalTransitionRecord, revalidateNativeBindingAgainstAssessment, revalidationFixture | no | no |
| 3 | goal_supervision_decision_test.go | TestEveryOutcomeStateMapsToItsOwnDecisionLiteral | supervisionPreMutationGateOrder, supervisionResumeDecision, supervisionResumeOutcome | no | no |
| 3 | goal_supervision_decision_test.go | TestUnknownDeliveryOutranksDeliveredWhenBothAreSet | supervisionResumeDecision, supervisionResumeOutcome | no | no |
| 3 | goal_supervision_decision_test.go | TestTheTwoIndeterminateLiteralsAreNotConfusable | supervisionResumeDecision, supervisionResumeOutcome | no | no |
| 3 | goal_supervision_delivery_gates_test.go | TestAnAssessmentThatExpiresDuringLedgerWorkIsRefusedAtDeliveryTime | GoalSupervisionAssessment, digestGoalSupervisionString, executeSupervisionResume, passingGenerationReader, passingPaneReader, readReservedRecoveryTransition (+5) | no | no |
| 3 | goal_supervision_delivery_gates_test.go | TestLaunchGenerationDigestDriftIsRefusedAtDeliveryTime | GoalSupervisionAssessment, deliveryGateRefusal, requireDeliveryGateRefusal, wantGenerationClause | no | no |
| 3 | goal_supervision_delivery_gates_test.go | TestLaunchGenerationModTimeDriftIsRefusedEvenWithAnIdenticalDigest | GoalSupervisionAssessment, deliveryGateRefusal, requireDeliveryGateRefusal, wantGenerationClause | no | no |
| 3 | goal_supervision_delivery_gates_test.go | TestAnUnreadableLaunchGenerationIsRefusedRatherThanAssumedUnchanged | GoalSupervisionAssessment, deliveryGateRefusal, requireDeliveryGateRefusal, wantGenerationClause | no | no |
| 3 | goal_supervision_delivery_gates_test.go | TestUnmanagedPaneIsRefusedAtDeliveryTime | GoalSupervisionAssessment, deliveryGateRefusal, passingObservation, requireDeliveryGateRefusal, supervisionPaneObservation, wantPaneClause | no | no |
| 3 | goal_supervision_delivery_gates_test.go | TestAPaneThatIsNotAffirmativelyPresentIsRefused | GoalSupervisionAssessment, deliveryGateRefusal, passingObservation, requireDeliveryGateRefusal, supervisionPaneObservation, wantPaneClause | no | no |
| 3 | goal_supervision_delivery_gates_test.go | TestAPresentPaneWithNoLiveProcessIsRefused | GoalSupervisionAssessment, deliveryGateRefusal, passingObservation, requireDeliveryGateRefusal, supervisionPaneObservation, wantPaneClause | no | no |
| 3 | goal_supervision_delivery_gates_test.go | TestUnknownPaneBusynessIsRefusedSeparatelyFromBusy | GoalSupervisionAssessment, deliveryGateRefusal, passingObservation, requireDeliveryGateRefusal, supervisionPaneObservation, wantPaneClause | no | no |
| 3 | goal_supervision_delivery_gates_test.go | TestABusyPaneIsRefusedAtDeliveryTime | GoalSupervisionAssessment, deliveryGateRefusal, passingObservation, requireDeliveryGateRefusal, supervisionPaneObservation, wantPaneClause | no | no |
| 3 | goal_supervision_delivery_gates_test.go | TestAnObservationForADifferentPaneIsRefused | GoalSupervisionAssessment, deliveryGateRefusal, passingObservation, requireDeliveryGateRefusal, supervisionPaneObservation, wantPaneClause | no | no |
| 3 | goal_supervision_delivery_gates_test.go | TestNilDeliveryTimeReadersRefuseRatherThanSkip | evaluateSupervisionDeliveryTimeGates, passingGenerationReader, passingPaneReader, u6DeliveringFixture, wantGenerationClause, wantPaneClause | no | no |
| 3 | goal_supervision_delivery_gates_test.go | TestAllDeliveryTimeGatesPassForAValidObservation | evaluateSupervisionDeliveryTimeGates, passingGenerationReader, passingPaneReader, u6DeliveringFixture | no | no |
| 3 | goal_supervision_delivery_gates_test.go | TestAnAssessmentExpiringDuringTheDeliveryTimeReadsIsRefused | GoalSupervisionAssessment, digestGoalSupervisionString, executeSupervisionResume, passingObservation, readReservedRecoveryTransition, requireDeliveryGateRefusal (+5) | no | no |
| 3 | goal_supervision_delivery_gates_test.go | TestExactFreshUntilEqualityIsExpiredAtDeliveryTime | evaluateSupervisionDeliveryTimeGates, passingGenerationReader, passingPaneReader, u6DeliveringFixture, wantFreshnessClause, wantFreshnessDetail | no | no |
| 3 | goal_supervision_gates_test.go | TestPreMutationGateOrderAndFirstFailureArePinned | GoalSupervisionAction, GoalSupervisionAssessment, canonicalGateOrderLiteral, describe, digestGoalSupervisionString, evaluateSupervisionPreMutationGates (+23) | no | no |
| 3 | goal_supervision_gates_test.go | TestPreMutationGatesPassForAValidFixture | canonicalGateOrderLiteral, digestGoalSupervisionString, evaluateSupervisionPreMutationGates, firstFailure, gateFixture, gateNamesEqual (+4) | no | no |
| 3 | goal_supervision_policy_mutation_test.go | TestPolicyGateBlocksTheExecutorFromReservingOrDelivering | GoalSupervisionAssessment, GoalSupervisionBinding, GoalSupervisionBlockerEvidence, GoalSupervisionGoalIdentity, GoalSupervisionPaneIdentity, currentRecoveryTransitionPrefix (+14) | no | no |
| 3 | goal_supervision_resume_test.go | TestSupervisionResumeConsumesThePauseGenerationItIsGiven | currentRecoveryTransitionName, nativeTransitionBase, newRecoveryTransitionRecord, recoveryTransitionInput, recoveryTransitionKindNativeGoalResume, supervisionClaimKey (+1) | no | no |
| 3 | goal_supervision_resume_test.go | TestEligibleButPolicyForbiddenRefusesWithoutReservingOrDelivering | GoalSupervisionAssessment, GoalSupervisionBinding, GoalSupervisionGoalIdentity, currentRecoveryTransitionPrefix, digestGoalSupervisionString, executeSupervisionResume (+7) | no | no |
| 3 | goal_supervision_resume_test.go | TestReservationWhoseFilenameAndBodyDisagreeAboutKindBlocks | currentRecoveryTransitionName, recoveryTransitionKindNativeGoalResume, reservationBlocker | no | no |
| 3 | goal_supervision_resume_test.go | TestSupervisionResumeRefusesAForeignNamespaceID | nativeTransitionBase, newRecoveryTransitionRecord, recoveryTransitionInput, recoveryTransitionKindNativeGoalResume, validNativeBinding | no | no |
| 3 | goal_supervision_test.go | TestAssessGoalSupervisionEligibilityTruthTable | GoalSupervisionBlockerEvidence, GoalSupervisionBudgetEvidence, GoalSupervisionClaimProjection, GoalSupervisionGateEvidence, GoalSupervisionGoalTerminal, GoalSupervisionLeadDown (+11) | no | no |
| 3 | goal_supervision_test.go | TestAssessGoalSupervisionEligibilityReasonsAreExplicitAndOrdered | assessGoalSupervision, eligibleGoalSupervisionInput, gotCodes | no | no |
| 3 | goal_supervision_test.go | TestGoalSupervisionManagedTmuxTargetRequiresCanonicalManagedTarget | GoalSupervisionLeadDown, assessGoalSupervision, eligibleGoalSupervisionInput, goalSupervisionManagedTmuxTarget | no | no |
| 3 | goal_supervision_test.go | TestGoalSupervisionExactPaneIdentityRequiresAllStableIDsAndTitle | goalSupervisionExactPaneIdentity | no | no |
| 3 | goal_supervision_test.go | TestVerifyGoalSupervisionBlockedBindingContentsIsExact | nativeGoalControlPrompt, verifyGoalSupervisionBlockedBindingContents | no | no |
| 3 | goal_supervision_test.go | TestBuildGoalSupervisionAssessmentContentVerificationDoesNotOverrideStatusVeto | GoalSupervisionGateEvidence, buildGoalSupervisionAssessment, goalBindingForStatus, goalSupervisionGateObservation, nativeGoalControlPrompt | no | no |
| 3 | goal_supervision_test.go | TestGoalSupervisionLifecycleObservationFailsClosed | goalSupervisionLifecycleObservation | no | no |
| 3 | goal_supervision_test.go | TestGoalSupervisionResumableLifecycleRequiresGoalBlockedPhase | GoalSupervisionLifecycleEvidence, assessGoalSupervision, eligibleGoalSupervisionInput, goalSupervisionReasonPassed | no | no |
| 3 | goal_supervision_test.go | TestGoalSupervisionMutatingActionsBindFingerprintAndAttempt | assessGoalSupervision, eligibleGoalSupervisionInput | no | no |
| 3 | goal_supervision_test.go | TestGoalSupervisionFingerprintIgnoresObservationTimeButBindsAttempt | assessGoalSupervision, eligibleGoalSupervisionInput | no | no |
| 3 | goal_supervision_test.go | TestGoalSupervisionPauseGenerationIgnoresUnrelatedLaunchRecordDrift | GoalSupervisionBinding, eligibleGoalSupervisionInput, goalSupervisionPauseGeneration | no | no |
| 3 | goal_supervision_test.go | TestGoalSupervisionProjectionJSONConsistentAcrossStatusBoardDoctor | assessGoalSupervision, eligibleGoalSupervisionInput | no | no |
| 3 | goal_supervision_test.go | TestDoctorGoalSupervisionWarnsWhenAssessmentCannotResolve | doctorCheckGoalSupervision | no | no |
| 3 | goal_supervision_test.go | TestInspectGoalSupervisionGatesReadsDurableOpenGate | inspectGoalSupervisionGates | no | no |
| 3 | goal_supervision_test.go | TestInspectGoalSupervisionGatesFailsClosedWhenNamespaceCannotBeScanned | inspectGoalSupervisionGates | no | no |
| 3 | goal_supervision_test.go | TestInspectGoalSupervisionGatesMarksMailboxWarningsAmbiguous | inspectGoalSupervisionGates | no | no |
| 3 | goal_supervision_u6_falsifiers_test.go | TestFailedDeliveredDirectiveIsReportedWithoutFailingTheCommand | GoalSupervisionAssessment, digestGoalSupervisionString, executeSupervisionResume, passingGenerationReader, passingPaneReader, readReservedRecoveryTransition (+4) | no | no |
| 3 | goal_supervision_u6_falsifiers_test.go | TestFailedStatusPollIsReportedAsObservabilityOnly | GoalSupervisionAssessment, digestGoalSupervisionString, executeSupervisionResume, passingGenerationReader, passingPaneReader, readReservedRecoveryTransition (+3) | no | no |
| 3 | goal_supervision_u6_falsifiers_test.go | TestUnknownPaneInputIsTerminalIndeterminateAndConsumesNothing | GoalSupervisionAssessment, digestGoalSupervisionString, executeSupervisionResume, passingGenerationReader, passingPaneReader, readReservedRecoveryTransition (+3) | no | no |
| 3 | goal_supervision_u6_falsifiers_test.go | TestConsumeFailureAfterKnownDeliveryReportsBothFacts | GoalSupervisionAssessment, digestGoalSupervisionString, executeSupervisionResume, passingGenerationReader, passingPaneReader, readReservedRecoveryTransition (+3) | no | no |
| 3 | goal_supervision_u6_falsifiers_test.go | TestNativeReservationPersistsTheResumedAttemptAsItsMatchableIdentity | nativeTransitionBase, newRecoveryTransitionRecord, recoveryTransitionInput, recoveryTransitionKindNativeGoalResume, validNativeBinding | no | no |
| 3 | goal_supervision_u6_falsifiers_test.go | TestNativeReservationRefusesASmuggledDisagreeingAttemptID | nativeTransitionBase, newRecoveryTransitionRecord, recoveryTransitionInput, recoveryTransitionKindNativeGoalResume, validNativeBinding | no | no |
| 3 | goal_supervision_u6_falsifiers_test.go | TestRedeliverReservationKeepsItsOwnNewAttemptIDUntouched | newRecoveryTransitionRecord, recoveryTransitionInput, recoveryTransitionKindRedeliver, resumeGoalTransitionRecord | no | no |
| 3 | goal_supervision_u6_falsifiers_test.go | TestNativeReservationPersistsTheAuthorisingSupervisor | nativeTransitionBase, newRecoveryTransitionRecord, recoveryTransitionInput, recoveryTransitionKindNativeGoalResume, validNativeBinding | no | no |
| 3 | goal_supervision_u6_falsifiers_test.go | TestNativeReservationRefusesABlankSupervisor | nativeTransitionBase, newRecoveryTransitionRecord, recoveryTransitionInput, recoveryTransitionKindNativeGoalResume, validNativeBinding | no | no |
| 3 | goal_supervision_u6_falsifiers_test.go | TestNativeReservationRefusesThePlaceholderSupervisor | nativeTransitionBase, newRecoveryTransitionRecord, recoveryTransitionInput, recoveryTransitionKindNativeGoalResume, supervisorIdentityPlaceholder, validNativeBinding | no | no |
| 3 | goal_supervision_u6_falsifiers_test.go | TestRedeliverReservationRefusesASuppliedSupervisor | newRecoveryTransitionRecord, recoveryTransitionInput, recoveryTransitionKindRedeliver, resumeGoalTransitionRecord | no | no |
| 3 | goal_supervision_u6_falsifiers_test.go | TestRedeliverReservationStillSucceedsWithoutASupervisor | newRecoveryTransitionRecord, recoveryTransitionInput, recoveryTransitionKindRedeliver, resumeGoalTransitionRecord | no | no |
| 3 | goal_supervision_u6_falsifiers_test.go | TestRedeliverReservationRefusesASupervisorSmuggledThroughBase | newRecoveryTransitionRecord, recoveryTransitionInput, recoveryTransitionKindRedeliver, resumeGoalTransitionRecord | no | no |
| 3 | goal_supervision_u6_falsifiers_test.go | TestRedeliverReservationRefusesAWhitespaceOnlySupervisor | newRecoveryTransitionRecord, recoveryTransitionInput, recoveryTransitionKindRedeliver, resumeGoalTransitionRecord | no | no |
| 3 | goal_supervision_u6_falsifiers_test.go | TestNativeReservationRefusesADisagreeingBaseSupervisor | nativeTransitionBase, newRecoveryTransitionRecord, recoveryTransitionInput, recoveryTransitionKindNativeGoalResume, resumeGoalTransitionRecord, validNativeBinding | no | no |
| 3 | goal_supervision_u6_falsifiers_test.go | TestRedeliverReservationRefusesAPauseGenerationSmuggledThroughBase | newRecoveryTransitionRecord, recoveryTransitionInput, recoveryTransitionKindRedeliver, resumeGoalTransitionRecord | no | no |
| 3 | goal_supervision_u6_falsifiers_test.go | TestRedeliverReservationRefusesAWhitespaceOnlyPauseGeneration | newRecoveryTransitionRecord, recoveryTransitionInput, recoveryTransitionKindRedeliver, resumeGoalTransitionRecord | no | no |
| 3 | goal_supervision_u6_falsifiers_test.go | TestNativeReservationRefusesDisagreeingBaseDerivedFields | nativeTransitionBase, newRecoveryTransitionRecord, recoveryTransitionInput, recoveryTransitionKindNativeGoalResume, resumeGoalTransitionRecord, validNativeBinding | no | no |
| 3 | goal_supervision_u6_falsifiers_test.go | TestNativeReservationRequiresTheFlatLaunchGeneration | completeNativeTransitionInput, newRecoveryTransitionRecord, recoveryTransitionInput | no | no |
| 3 | goal_supervision_u6_falsifiers_test.go | TestTheCompleteNativeFixtureIsAccepted | completeNativeTransitionInput, fixtureLaunchDigest, fixtureLaunchID, fixtureLaunchModTime, newRecoveryTransitionRecord | no | no |
| 3 | goal_supervision_u6_falsifiers_test.go | TestNativeReservationAcceptsTheCompleteExactBinding | nativeTransitionBase, newRecoveryTransitionRecord, recoveryTransitionInput, recoveryTransitionKindNativeGoalResume, resumeGoalTransitionSchemaVersion, validNativeBinding | no | no |
| 3 | goal_supervision_u6_falsifiers_test.go | TestNativeExactBindingRefusesEachMissingField | nativeTransitionBase, newRecoveryTransitionRecord, recoveryTransitionInput, recoveryTransitionKindNativeGoalResume, resumeGoalNativeBinding, validNativeBinding | no | no |
| 3 | goal_supervision_u6_falsifiers_test.go | TestBaseSuppliedNativeBindingIsRefusedOnBothKinds | newRecoveryTransitionRecord, recoveryTransitionInput, recoveryTransitionKindNativeGoalResume, recoveryTransitionKindRedeliver, resumeGoalTransitionRecord, validNativeBinding | no | no |
| 3 | goal_supervision_u6_falsifiers_test.go | TestRedeliverRefusesAnExplicitNativeBindingButSucceedsWithout | newRecoveryTransitionRecord, recoveryTransitionInput, recoveryTransitionKindRedeliver, resumeGoalTransitionRecord, validNativeBinding | no | no |
| 3 | goal_supervision_u6_falsifiers_test.go | TestNativeBindingAttemptIdentityMustEqualTheReservationAttempt | nativeTransitionBase, newRecoveryTransitionRecord, recoveryTransitionInput, recoveryTransitionKindNativeGoalResume, validNativeBinding | no | no |
| 3 | goal_test.go | TestSimpleGoalSendsOneOperatorTodoWithoutGoalState | goalAttemptDir, runGoal | no | no |
| 3 | goal_test.go | TestGoalLegacySubcommandsAreGone | runGoalWithVersion | no | no |
| 3 | goal_test.go | TestGoalDraftJSONIncludesMilestoneIssues | fakeGoalGh, goalDraftData, runGoalDraft | no | no |
| 3 | goal_test.go | TestGoalDraftPlannerLeadModeSurfacesDelegationContract | goalDraftData, runGoalDraft | no | no |
| 3 | goal_test.go | TestBuildGoalDraftTargetRootSource | buildGoalDraft, goalDraftOptions, mutationsContainTargetRoot | no | no |
| 3 | goal_test.go | TestGoalDraftSkillInvocationCompleteness | buildGoalDraft, goalDraftOptions | no | no |
| 3 | goal_test.go | TestGoalDraftLaunchMutationOmitsDefaultReasoningEffort | goalDraftData, runGoalDraft | no | no |
| 3 | goal_test.go | TestGoalDraftFieldSourcesAndSteps | buildGoalDraft, goalDraftOptions | no | no |
| 3 | goal_test.go | TestGoalDraftMarkdownIsPreviewOnly | runGoalDraft | no | no |
| 3 | goal_test.go | TestGoalDraftNamedProfileCommandsCarryNamespace | goalDraftData, runGoalDraft | no | no |
| 3 | goal_test.go | TestGoalDraftCustomLeadCarriesThroughPlan | goalDraftData, runGoalDraft | no | no |
| 3 | goal_test.go | TestGoalDraftUsesConfiguredDrafterAndValidatesBrief | applyGoalBriefDraft, buildGoalDraft, goalDraftOptions, runGoalDrafter | no | no |
| 3 | goal_test.go | TestGoalDraftExecutionModeContract | goalDraftData, runGoalDraftWithVersion | no | no |
| 3 | goal_test.go | TestGoalDraftAutonomousPreviewRequiresAndEmitsPolicy | goalDraftData, runGoalDraft | no | no |
| 3 | goal_test.go | TestGoalDraftJSONIncludesVisibleLaunchMutation | goalBindingFromArgs, goalDraftData, parseCodexGoalControlPrompt, runGoalDraft | no | no |
| 3 | goal_test.go | TestGoalDraftSkillInvocationOutput | runGoalDraft | no | no |
| 3 | goal_test.go | TestGoalDraftJSONIncludesSkillInvocation | goalDraftData, runGoalDraft | no | no |
| 3 | goal_test.go | TestGoalDraftVisibilityOverrides | goalDraftData, runGoalDraft | no | no |
| 3 | goal_test.go | TestGoalDraftRejectsUnknownVisibility | runGoalDraft | no | no |
| 3 | goal_test.go | TestGoalDraftAutonomousRejectsMissingPolicy | runGoalDraft | no | no |
| 3 | goal_test.go | TestGoalDraftMilestoneRequiresRepo | runGoalDraft | no | no |
| 3 | goal_test.go | TestGoalDraftMilestoneGhErrorNamesSource | fakeGoalGh, runGoalDraft | no | no |
| 3 | help_test.go | TestGoalGroupHelp | help of the deleted `goal` verb | no | no |
| 3 | help_test.go | TestGoalUnknownSubcommandEnumeratesCompleteSurface | drives the deleted `goal` verb by name | no | no |
| 3 | launch_test.go | TestNativeGoalBindingFromArgsDetectsGoalPrompt | nativeGoalBindingFromArgs | no | no |
| 3 | launch_test.go | TestGoalBindingFromArgsUsesStructuredPromptForCodex | codexGoalControlPrompt, goalBindingFromArgs | no | no |
| 3 | launch_test.go | TestLaunchRecordGoalBindingDeliveryStateUpgradeCompatibility | goalBindingDeliveryDelivered, goalBindingDeliveryReserved, goalDeliveryContractForBinary, launchRecordHasGoalBinding | no | no |
| 3 | namespace_migration_native_claims_test.go | TestMigrationRefusesANamespaceHoldingNativeRecoveryClaims | completeNativeTransitionInput, currentRecoveryTransitionPrefix, inspectNamespaceNativeRecoveryClaims, newRecoveryTransitionRecord, recognizeRecoveryTransitionName, recoveryNameMalformed (+1) | no | no |
| 3 | namespace_migration_native_claims_test.go | TestHelperAllowsANamespaceWithOnlyLegacyRedeliveryRecords | blocking, inspectNamespaceNativeRecoveryClaims, legacyRecoveryTransitionPrefix, recognizeRecoveryTransitionName, recoveryNameRecognized | no | no |
| 3 | namespace_migration_native_claims_test.go | TestMigrationRefusesWhenTheClaimDirectoryCannotBeInspected | inspectNamespaceNativeRecoveryClaims | no | no |
| 3 | namespace_migration_native_claims_test.go | TestPlannerBlocksMigrationWhenTheSourceHoldsANativeRecoveryClaim | completeNativeTransitionInput, goalAttemptDir, namespaceMigrationPlannerOptions, newRecoveryTransitionRecord, planNamespaceMigration | no | no |
| 3 | namespace_migration_native_claims_test.go | TestMigrationDoesNotBlockWhenThereIsNoClaimDirectoryAtAll | inspectNamespaceNativeRecoveryClaims | no | no |
| 3 | namespace_migration_test.go | TestNamespaceMigrationBoundTransitionUsesPairedHandle | migratedLaunchGeneration, resumeGoalTransitionBound, resumeGoalTransitionRecord, resumeGoalTransitionSchemaVersion, rewriteMigrationGoalArtifact | no | no |
| 3 | operator_identity_test.go | TestValidateOperatorLaunchRecordAuthorizationAndRuntimeBinding | goalBindingDeliveryDelivered | partly | yes, minus the native-goal external-record block |
| 3 | profiles_test.go | TestNamedProfileConflictBlocksDirectRuntimeCommands | runGoal, runUp | partly | yes, minus the goal and up rows |
| 3 | profiles_test.go | TestSimpleGoalNamespaceConflictRequiresAuditedOverride | runGoal | no | no |
| 3 | recovery_transition_cas_test.go | TestBindIsIdempotentForTheSameGenerationAndRefusesADifferentOne | bindRecoveryTransitionGeneration, recoveryReservation, resumeGoalTransitionRecord, resumeGoalTransitionSchemaVersion | no | no |
| 3 | recovery_transition_cas_test.go | TestLegacyShapedRecordsSerialiseWithoutThePR5Keys | recoveryTransitionKindNativeGoalResume, resumeGoalTransitionRecord, resumeGoalTransitionSchemaVersion | no | no |
| 3 | recovery_transition_cas_test.go | TestConstructorOverwritesASmuggledTransitionID | newRecoveryTransitionRecord, recoveryTransitionInput, recoveryTransitionKindRedeliver, redeliverTransitionBase, resumeGoalTransitionID, resumeGoalTransitionPath | no | no |
| 3 | recovery_transition_cas_test.go | TestRecoveryTransitionInputGainsNoDirectTransitionIDField | TestConstructorOverwritesASmuggledTransitionID, recoveryTransitionInput, resumeGoalTransitionRecord | no | no |
| 3 | recovery_transition_cas_test.go | TestReserveAndConsumeBothRefuseALostRace | consumeRecoveryTransition, newRecoveryTransitionRecord, recoveryTransitionInput, recoveryTransitionKindRedeliver, redeliverTransitionBase, reserveRecoveryTransition (+1) | no | no |
| 3 | recovery_transition_legacy_test.go | TestLegacyReservationsAreRecognisedAndBlockTheirOwnPause | blocking, legacyReservationFor, recognizeRecoveryTransitionName, recoveryNameRecognized, recoveryTransitionKindRedeliver, scanRecoveryTransitionsForPause | no | no |
| 3 | recovery_transition_legacy_test.go | TestALegacyReservationForADifferentAttemptDoesNotBlockThisPause | blocking, describe, legacyReservationFor, resumeGoalTransitionID, scanRecoveryTransitionsForPause | no | no |
| 3 | recovery_transition_legacy_test.go | TestAConsumedLegacyReservationStillBlocks | blocking, legacyReservationFor, resumeGoalTransitionConsumedPath, scanRecoveryTransitionsForPause | no | no |
| 3 | recovery_transition_publication_test.go | TestPublicationAcceptsTheConstructorsOwnOutputForBothKinds | completeNativeTransitionInput, newRecoveryTransitionRecord, recoveryTransitionInput, recoveryTransitionKindRedeliver, redeliverTransitionBase, reserveRecoveryTransition | no | no |
| 3 | recovery_transition_publication_test.go | TestPublicationRefusesACanonicalNameInTheWrongDirectory | completeNativeTransitionInput, newRecoveryTransitionRecord, reserveRecoveryTransition | no | no |
| 3 | recovery_transition_publication_test.go | TestPublicationRefusesAMinimalNativeRecordAtItsCanonicalPath | currentRecoveryTransitionName, fixturePauseGeneration, goalAttemptDir, recoveryTransitionKindNativeGoalResume, reserveRecoveryTransition, resumeGoalTransitionRecord (+2) | no | no |
| 3 | recovery_transition_publication_test.go | TestPublicationRefusesABlankKindRecord | completeNativeTransitionInput, newRecoveryTransitionRecord, reserveRecoveryTransition | no | no |
| 3 | recovery_transition_publication_test.go | TestPublicationRefusesARedeliverRecordUnderTheNativeNamingVocabulary | currentRecoveryTransitionName, newRecoveryTransitionRecord, recoveryTransitionInput, recoveryTransitionKindRedeliver, redeliverTransitionBase, reserveRecoveryTransition | no | no |
| 3 | recovery_transition_publication_test.go | TestPublicationRefusesEveryMissingNativeContractField | completeNativeTransitionInput, newRecoveryTransitionRecord, reserveRecoveryTransition, resumeGoalTransitionRecord, supervisorIdentityPlaceholder | no | no |
| 3 | recovery_transition_publication_test.go | TestPublicationRefusesARecordWhoseDerivedIdentityIsWrong | completeNativeTransitionInput, currentRecoveryTransitionName, goalAttemptDir, newRecoveryTransitionRecord, recoveryTransitionInput, recoveryTransitionKindNativeGoalResume (+6) | no | no |
| 3 | recovery_transition_publication_test.go | TestPublicationRefusesAnIncompleteRedeliverRecord | newRecoveryTransitionRecord, recoveryTransitionInput, recoveryTransitionKindRedeliver, redeliverTransitionBase, reserveRecoveryTransition, resumeGoalTransitionRecord | no | no |
| 3 | recovery_transition_publication_test.go | TestNonCanonicalScopeIsRefusedAtConstructionSoPathsCannotDrift | completeNativeTransitionInput, newRecoveryTransitionRecord, reserveRecoveryTransition | no | no |
| 3 | recovery_transition_publication_test.go | TestPublicationRefusesACanonicalNameInASubdirectoryOfItsOwnDirectory | completeNativeTransitionInput, newRecoveryTransitionRecord, reserveRecoveryTransition | no | no |
| 3 | recovery_transition_publication_test.go | TestExecutorRefusesADriftedPersistedClaimWithoutDelivering | GoalSupervisionAssessment, describe, digestGoalSupervisionString, executeSupervisionResume, passingGenerationReader, passingPaneReader (+4) | no | no |
| 3 | recovery_transition_publication_test.go | TestExecutorRefusesAPersistedClaimWithADriftedDurableIdentity | GoalSupervisionAssessment, digestGoalSupervisionString, executeSupervisionResume, passingGenerationReader, passingPaneReader, readReservedRecoveryTransition (+3) | no | no |
| 3 | recovery_transition_publication_test.go | TestExecutorRefusesANilReservedClaimLoaderRatherThanSkipping | GoalSupervisionAssessment, digestGoalSupervisionString, executeSupervisionResume, passingGenerationReader, passingPaneReader, u6DeliveringFixture (+1) | no | no |
| 3 | recovery_transition_publication_test.go | TestPublicationRefusesAStaleSchemaVersion | completeNativeTransitionInput, newRecoveryTransitionRecord, reserveRecoveryTransition | no | no |
| 3 | recovery_transition_publication_test.go | TestPublicationRefusesARecordWithNoScopeTriple | legacyRecoveryTransitionPrefix, recoveryTransitionKindRedeliver, reserveRecoveryTransition, resumeGoalTransitionRecord, resumeGoalTransitionSchemaVersion | no | no |
| 3 | recovery_transition_roundtrip_test.go | TestRedeliverReservationRoundTripsThroughItsExistingReaders | digestBytes, newRecoveryTransitionRecord, recognizeRecoveryTransitionName, recoveryNameRecognized, recoveryTransitionInput, recoveryTransitionKindRedeliver (+5) | no | no |
| 3 | recovery_transition_roundtrip_test.go | TestRedeliverInputCarryingAPauseGenerationIsRefused | newRecoveryTransitionRecord, recoveryTransitionInput, recoveryTransitionKindRedeliver, resumeGoalTransitionRecord | no | no |
| 3 | recovery_transition_writer_pin_test.go | TestOnlyOneProductionSiteReservesARecoveryTransition | bindRecoveryTransitionGeneration, consumeRecoveryTransition, publishGoalJSON, reserveRecoveryTransition | no | no |
| 3 | recovery_transition_writer_pin_test.go | TestNoProductionCodeBuildsRecoveryTransitionLiterals | describe, newRecoveryTransitionRecord, recoveryTransitionInput, resumeGoalTransitionRecord | no | no |
| 3 | resume_goal_test.go | TestResumeGoalAttemptIdentityIsExact | goalAttemptRecord, mustGoalAttemptPath, readGoalAttempt, validateResumeGoalAttempt, writeTestJSON | no | no |
| 3 | resume_goal_test.go | TestParseGeneratedGoalBindingIsQuoteAware | nativeGoalControlPrompt, parseGeneratedGoalBinding | no | no |
| 3 | resume_goal_test.go | TestRestoreGoalBindingIsMetadataNotChildArg | codexGoalControlPrompt | no | no |
| 3 | resume_goal_test.go | TestRunLaunchRestoresGoalBindingMetadataWithoutActivation | codexGoalControlPrompt | no | no |
| 3 | status_test.go | TestExecuteStatusJSONReportsPromptGoalForLiveCodexLeadRecord | codexGoalControlPrompt | no | no |
| 3 | status_test.go | TestExecuteStatusJSONReportsClaimedReservedPromptGoalForLiveCodexLead | codexGoalControlPrompt, goalAttemptClaim, goalAttemptClaimPath, goalAttemptPath, goalAttemptRecord, goalBindingDeliveryReserved (+2) | no | no |
| 3 | status_test.go | TestExecuteStatusJSONReportsBlockedNativeGoalForLiveLeadRecord | restore attempt minus the goal_binding assertions failed: open_blockers came from the deleted goal-binding projection | no | no |
| 3 | status_test.go | TestExecuteStatusJSONReportsMissingPromptGoalForLiveCodexProjectLead | asserts statusEnvelopeData.GoalBinding, deleted in cut 3 | no | no |
| 3 | status_test.go | TestExecuteStatusJSONRejectsInvalidClaudeGoalBinding | asserts statusEnvelopeData.GoalBinding, deleted in cut 3 | no | no |
| 3 | status_test.go | TestStatusJSONProvesExternalOrchestratorBinding | goalBindingDeliveryDelivered | partly | yes, minus the GoalBinding fixture field and goal_binding assertion |
| 3 | team_lead_test.go | TestLeadRegisterPreservesExistingNativeGoalBinding | externalLeadWakeProcessGroupSignal, leadWakeOptions, leadWakeResult, leadWakeStarter, runLead | no | no |
| 3 | team_lead_test.go | TestPreserveExternalGoalBindingRejectsInvalidClaudeBinding | preserveExternalGoalBinding | no | no |
| 4 | amq_update_suppression_integration_test.go | TestAMQNoisyFakeNativeJSONStartsAtByteZero | planEnvelopeData, runResume, seedBriefAt | partly | yes, minus the resume row and its brief seed |
| 4 | briefs_live_test.go | TestRunUpLiveCreatesTeamHomeBriefOnce | runUp | no | no |
| 4 | briefs_live_test.go | TestUpDryRunDoesNotCreateBrief | runUp | no | no |
| 4 | briefs_live_test.go | TestRunUpLiveBriefIsTeamHomeOnly | runUp | no | no |
| 4 | briefs_seed_test.go | TestRunUpForceWithoutSeedErrors | runUp | no | no |
| 4 | briefs_seed_test.go | TestRunUpSeedFromDryRunPrintsBriefOnly | runUp | no | no |
| 4 | briefs_seed_test.go | TestRunUpDryRunWithoutSeedStillMatchesTeamShow | runUp | no | no |
| 4 | briefs_seed_test.go | TestRunUpLiveSeedFromWritesBriefThenLaunches | runUp | no | no |
| 4 | briefs_seed_test.go | TestRunUpLiveSeedFromRefusesExistingWithoutForce | runUp | no | no |
| 4 | briefs_seed_test.go | TestRunUpLiveSeedFromDoesNotWriteOnFreshExistingRejection | runUp | no | no |
| 4 | briefs_seed_test.go | TestRunUpLiveSeedFromForceOverwrites | runUp | no | no |
| 4 | deprecation_test.go | TestAgentUpDryRunStillWorks | runAgentUp | no | no |
| 4 | deprecation_test.go | TestAgentUpProjectDryRunTargetsOtherDir | runAgentUp | no | no |
| 4 | deprecation_test.go | TestAgentResumeRoutingStillWorks | runAgentResume | no | no |
| 4 | deprecation_test.go | TestTopLevelResumeStillWorks | runResume, seedBriefAt | no | no |
| 4 | deprecation_test.go | TestUpDryRunStillWorks | runUp | no | no |
| 4 | deprecation_test.go | TestTranslateAgentUpArgs | translateAgentUpArgs | no | no |
| 4 | deprecation_test.go | TestAgentUpPostBinaryNamedProfileRootDoesNotBecomeChildArg | runAgentUp | no | no |
| 4 | deprecation_test.go | TestDefaultMeFromRole | defaultMeFromRole | no | no |
| 4 | deprecation_test.go | TestAgentUpHonorsPostBinaryFlags | runAgentUp | no | no |
| 4 | deprecation_test.go | TestAgentDispatchCovered | runAgent | no | no |
| 4 | down_test.go | TestRetireWakeWithAMQUsesExactPersistedInjectorIdentity | retireWakeWithAMQ, runExactWakeRetire | no | no |
| 4 | down_test.go | TestRetireWakeWithAMQReplaysInjectedRetryPolicy | retireWakeWithAMQ, runExactWakeRetire, wakeRetryUntilInjected | no | no |
| 4 | down_test.go | TestReapRawWakeRetirementFallbackIsReported | fakeMissingWakeCheck, reapStaleArtifacts, summary | no | no |
| 4 | down_test.go | TestWakeQuiescenceWaitsForDelayedAuthoritativeMissing | waitForWakeQuiescence, wakeCheckRequestIdentity, wakeQuiescencePoll, wakeQuiescenceTimeout | no | no |
| 4 | down_test.go | TestWakeQuiescenceUnavailableFallsBackToConsecutiveStableSamples | waitForWakeQuiescence, wakeQuiescencePoll, wakeQuiescenceStableSamples, wakeQuiescenceTimeout | no | no |
| 4 | down_test.go | TestWakeQuiescenceBlockedCheckLeavesFallbackBudget | waitForWakeQuiescence, wakeQuiescenceCheckTimeout, wakeQuiescencePoll, wakeQuiescenceStableSamples, wakeQuiescenceTimeout | no | no |
| 4 | down_test.go | TestWakeQuiescenceUnavailableAcceptsStableExactDeadPIDStaleLock | waitForWakeQuiescence, wakeQuiescencePoll, wakeQuiescenceTimeout | no | no |
| 4 | down_test.go | TestWakeQuiescenceAcceptsAuthoritativeStaleWithStableExactLock | waitForWakeQuiescence, wakeCheckRequestIdentity, wakeQuiescencePoll, wakeQuiescenceTimeout | no | no |
| 4 | down_test.go | TestWakeQuiescenceExactLivePIDLockFailsClosed | waitForWakeQuiescence, wakeQuiescencePoll, wakeQuiescenceTimeout | no | no |
| 4 | down_test.go | TestWakeQuiescenceIdentityMismatchNeverFallsBack | fakeMismatchedWakeCheck, waitForWakeQuiescence | no | no |
| 4 | down_test.go | TestReapRawCorruptWakeFailsClosedAndPreservesLock | fakeMissingWakeCheck, reapStaleArtifacts | no | no |
| 4 | down_test.go | TestReapExactWakeRetirementAcceptsResidueAfterLockRemoval | fakeMissingWakeCheck, reapStaleArtifacts, runExactWakeRetire, stubOwnerlessWakeRecovery, wakeRetryUntilInjected | no | no |
| 4 | down_test.go | TestReapExactWakeRetirementRecognizesSelfCleanup | fakeMissingWakeCheck, nativeWakeRetireSelfCleaned, reapStaleArtifacts, runExactWakeRetire, stubOwnerlessWakeRecovery | no | no |
| 4 | down_test.go | TestReapExactWakeRetirementRefusalNeverFallsBackToSignal | fakeMismatchedWakeCheck, reapStaleArtifacts, runExactWakeRetire, stubOwnerlessWakeRecovery | no | no |
| 4 | down_test.go | TestReapExactWakeRetirementSuccessNeverFallsBackToSignal | fakeMismatchedWakeCheck, reapStaleArtifacts, runExactWakeRetire, stubOwnerlessWakeRecovery | no | no |
| 4 | down_test.go | TestReapExactWakeRetirementRejectsMismatchedPIDWithoutFallback | fakeMismatchedWakeCheck, reapStaleArtifacts, runExactWakeRetire, stubOwnerlessWakeRecovery | no | no |
| 4 | down_test.go | TestReapOwnerBoundWakeUsesRecoverOwnerWithoutOwnerlessRetire | fakeMissingWakeCheck, reapStaleArtifacts, runExactWakeRecoverOwner, runExactWakeRetire, wakeOwnerRecoveryPoll, wakeOwnerRecoveryTimeout | no | no |
| 4 | down_test.go | TestReapResumeOwnerWakeUsesRecoverOwnerWithoutPersistedInjector | fakeMissingWakeCheck, reapStaleArtifacts, runExactWakeRecoverOwner, runExactWakeRetire | no | no |
| 4 | down_test.go | TestReapOwnerRecoveryRejectsMismatchedOwnerWithoutFallback | fakeMismatchedWakeCheck, reapStaleArtifacts, runExactWakeRecoverOwner, runExactWakeRetire | no | no |
| 4 | down_test.go | TestReapCorruptOwnerBoundWakeWithNoPersistedWakePIDStillFails | fakeMismatchedWakeCheck, reapStaleArtifacts, runExactWakeRecoverOwner, runExactWakeRetire | no | no |
| 4 | down_test.go | TestReapResultOwnerRecoveryStatusesFailWithoutPID | reapResult | no | no |
| 4 | down_test.go | TestReapPresenceReassertsOfflineAfterWakeDies | fakeMissingWakeCheck, reapStaleArtifacts | no | no |
| 4 | down_test.go | TestReapPresenceFlipSkipsReassertWhenWakeAlreadyDead | fakeMissingWakeCheck, reapStaleArtifacts | no | no |
| 4 | down_test.go | TestExecuteDownDryRunUsesCanonicalRecordWithoutMutation | downEnvelopeData, downExecution, downStatusPlanned, executeDown, runDownExec | no | no |
| 4 | down_test.go | TestExecuteDownStoppedRecordCannotBootstrapImplicitProject | downExecution, runDownExec | no | no |
| 4 | down_test.go | TestRunStopRejectsRoleAndAll | runDown | no | no |
| 4 | down_test.go | TestRunStopProjectTargetsOtherDir | runDown | no | no |
| 4 | down_test.go | TestExecuteDownRejectsUnknownRole | downExecution, runDownExec | no | no |
| 4 | down_test.go | TestExecuteDownSendsTermToVerifiedPID | downExecution, runDownExec | no | no |
| 4 | down_test.go | TestExecuteDownNotLiveForDeadPID | downExecution, runDownExec | no | no |
| 4 | down_test.go | TestExecuteDownMaybeLiveForNoPIDWithFreshPresence | downExecution, notificationWatcherProcess, runDownExec, sessionNotifierSpawn | no | no |
| 4 | down_test.go | TestRenderDownReportsMaybeLiveWithFailureStaysPartial | downReport, downStatusFailed, downStatusMaybeLive, renderDownReports | no | no |
| 4 | down_test.go | TestRestoreDownSessionServicesAttemptsBothAfterPartialStop | restoreDownSessionServices | no | no |
| 4 | down_test.go | TestExecuteDownNotLiveForNoPIDStalePresence | downExecution, runDownExec | no | no |
| 4 | down_test.go | TestExecuteDownNotLiveForMissingRecord | downExecution, runDownExec | no | no |
| 4 | down_test.go | TestExecuteDownNotLiveForBinaryMismatch | downExecution, runDownExec | no | no |
| 4 | down_test.go | TestExecuteDownPartialFailureReturnsAggregateError | downExecution, runDownExec | no | no |
| 4 | down_test.go | TestExecuteDownResolvesDefaultWorkstream | downExecution, runDownExec | no | no |
| 4 | down_test.go | TestExecuteDownReapsOrphanWakeOnDeadAgent | downExecution, runDownExec | no | no |
| 4 | down_test.go | TestExecuteDownReapsZombiePresenceWhenWakeDead | downExecution, runDownExec | no | no |
| 4 | down_test.go | TestExecuteDownReapDoesNotTouchForeignPresence | downExecution, runDownExec | no | no |
| 4 | down_test.go | TestExecuteDownReapsLockOnPIDReuse | downExecution, runDownExec | no | no |
| 4 | down_test.go | TestExecuteDownAllScopedToConfiguredMembers | downExecution, runDownExec | no | no |
| 4 | down_test.go | TestExecuteDownReapKeepsLockOnSignalFailure | downExecution, runDownExec | no | no |
| 4 | down_test.go | TestExecuteDownReapRejectsForeignRootWake | downExecution, downRealMatcherProbe, runDownExec | no | no |
| 4 | down_test.go | TestExecuteDownAgentKilledButWakeReapFailsIsFailed | downExecution, downRealMatcherProbe, runDownExec | no | no |
| 4 | down_test.go | TestRenderDownReportsCleanedAndFailedStaysPartial | downReport, downStatusCleaned, downStatusFailed, renderDownReports, summary | no | no |
| 4 | error_taxonomy_520_test.go | TestLiveIdentityRecoveryNamesRegisteredExecutableCommands | seedBriefAt | no | no |
| 4 | exit_test.go | TestParseFlagsWrapsUnknownFlag | runUp | partly | yes, example command up -> doctor |
| 4 | exit_test.go | TestParseFlagsUsageErrorAcrossCommands | runCompletion, runUp | partly | yes, minus the up, completion, team profiles rows |
| 4 | exit_test.go | TestDownPartialExitMapping | downReport, downStatusFailed, downStatusNotLive, downStatusStopped, renderDownReports | no | no |
| 4 | help_test.go | TestAgentHelpDocumentsProject | help of the deleted `agent` entry point | no | no |
| 4 | json_envelopes_test.go | TestRunUpDryRunJSONEnvelope | runUp | partly | yes, driver up --dry-run -> team show (same plan) |
| 4 | json_envelopes_test.go | TestRunUpDryRunJSONIncludesOrchestration | runUp | partly | yes, driver up --dry-run -> team show (same plan) |
| 4 | json_envelopes_test.go | TestRunUpDryRunJSONUsesCustomOperator | runUp | partly | yes, driver up --dry-run -> team show (same plan) |
| 4 | json_envelopes_test.go | TestRunUpJSONRejectsWithoutDryRun | runUp | no | no |
| 4 | json_envelopes_test.go | TestRunUpDryRunJSONSeedFromUsesOneClockReading | briefCandidate, runUp | no | no |
| 4 | json_envelopes_test.go | TestRunUpDryRunJSONWithSeedFromEmitsBriefCandidate | briefCandidate, runUp | no | no |
| 4 | json_envelopes_test.go | TestRunTeamShowJSONMatchesUpDryRunPlan | runUp | no | no |
| 4 | launch_test.go | TestRunLaunchNamedProfileResumeAndDynamicPathsUseExactRootChildShim | runAgentUp | no | no |
| 4 | live_identity_test.go | TestStopClosePanesAcceptsPaneProcessRecordFromDeliveryPath | downStatusStopped, fakeMissingWakeCheck, terminateMember | no | no |
| 4 | live_identity_test.go | TestPaneCleanupRejectsUnrelatedPaneProcessPID | downStatusStopped, fakeMissingWakeCheck, terminateMember | no | no |
| 4 | new_test.go | TestRunNewSessionDelegatesToUpDryRun | runNew | no | no |
| 4 | new_test.go | TestRunNewSessionSeedFromDryRunPrintsCandidateBrief | runNew | no | no |
| 4 | new_test.go | TestRunNewSessionProjectDelegatesFromOtherCWD | runNew | no | no |
| 4 | notification_watcher_test.go | TestNotificationWatcherPartialAndFinalRoleStopDecision | shouldStopNotificationWatcherAfterDown | no | no |
| 4 | operator_guard_test.go | TestAgentUpRefusesOperatorRoleAndHandle | runAgentUp | no | no |
| 4 | operator_guard_test.go | TestAgentUpAlwaysReservesUserAndConfiguredOperator | runAgentUp | no | no |
| 4 | operator_guard_test.go | TestOperatorGuardAllowsNormalRole | runAgentUp | no | no |
| 4 | operator_guard_test.go | TestRoleControlCommandsRefuseOperatorTarget | runDown, runResume | partly | yes, minus the down and resume rows |
| 4 | operator_handoff_card_493_test.go | TestLiveLaunchPrintsOperatorHandoffCard | handoffCardTeam, operatorEnabled, runUp | no | no |
| 4 | operator_handoff_card_493_test.go | TestQuietDoesNotSuppressOperatorHandoffCard | handoffCardTeam, operatorEnabled, runUp | no | no |
| 4 | operator_handoff_card_493_test.go | TestDryRunPrintsNoOperatorHandoffCard | handoffCardTeam, operatorEnabled, runUp | no | no |
| 4 | operator_handoff_card_493_test.go | TestLiveLaunchCardWarnsWhenNotificationsOff | handoffCardTeam, operatorEnabled, runUp | no | no |
| 4 | operator_handoff_card_493_test.go | TestResumeExecPrintsOperatorHandoffCard | handoffCardTeam, newSimpleStartRunFixture, operatorEnabled, resumeExecRequest, runResumeExecWithDependencies, seedBriefAt (+1) | no | no |
| 4 | operator_handoff_card_493_test.go | TestLaunchCardProjectFlagSurvivesRemoteProject | handoffCardTeam, operatorEnabled, runUp | no | no |
| 4 | output_test.go | TestDownHumanOutputColorMatrix | downReport, downStatusFailed, downStatusStopped, renderDownReports | no | no |
| 4 | output_test.go | TestQuietDoesNotSuppressWarningsOrErrors | runUp | partly | yes, driver up --dry-run -> team show |
| 4 | output_test.go | TestQuietDoesNotSuppressDryRunStdout | runUp | partly | yes, driver up --dry-run -> team show |
| 4 | pane_cleanup_dead_pane_689_test.go | TestDownClosePanesClosesDeadPaneOfDeadAgent | fakeMissingWakeCheck, terminateMember | no | no |
| 4 | pane_cleanup_dead_pane_689_test.go | TestDownClosePanesPreservesLivePaneOfDeadAgent | fakeMissingWakeCheck, terminateMember | no | no |
| 4 | pane_cleanup_dead_pane_689_test.go | TestDownClosePanesReportsAlreadyGoneForVanishedDeadAgentPane | fakeMissingWakeCheck, terminateMember | no | no |
| 4 | pane_cleanup_down_wiring_test.go | TestStopPanePrepareSignalCloseOrdering | downStatusStopped, fakeMissingWakeCheck, terminateMember | no | no |
| 4 | pane_cleanup_down_wiring_test.go | TestStopRequiresAuthoritativeMissingWithoutPersistedWakePID | downStatusStopped, fakeMissingWakeCheck, terminateMember | no | no |
| 4 | pane_cleanup_down_wiring_test.go | TestStopFallbackFailsClosedWhenWakeLockReappearsAfterPaneTeardown | downStatusFailed, terminateMember, wakeQuiescencePoll, wakeQuiescenceTimeout | no | no |
| 4 | pane_cleanup_down_wiring_test.go | TestResumedSecondStopAcceptsPreservedDeadPIDStaleWakeLock | downStatusStopped, terminateMember, wakeQuiescencePoll, wakeQuiescenceTimeout | no | no |
| 4 | pane_cleanup_down_wiring_test.go | TestStopClosePanesAcceptsPriorGenerationBaseRootShape | downStatusStopped, fakeMissingWakeCheck, terminateMember | no | no |
| 4 | pane_cleanup_down_wiring_test.go | TestStopClosePanesPriorGenerationShapeStillRefusesForeignPane | downStatusStopped, fakeMissingWakeCheck, terminateMember | no | no |
| 4 | pane_cleanup_down_wiring_test.go | TestStopSignalsWhenPanePreparationRefusesAndReturnsPartial | downReport, downStatusStopped, fakeMissingWakeCheck, renderDownReportsScoped, terminateMember | no | no |
| 4 | pane_cleanup_down_wiring_test.go | TestStopRefusesToSignalReusedSameBinaryPID | downStatusNotLive, fakeMissingWakeCheck, terminateMember | no | no |
| 4 | pane_cleanup_down_wiring_test.go | TestStopPaneFailurePrecedesAgentFailureInJSON | downEnvelopeData, downReport, downStatusFailed, renderDownReportsScoped | no | no |
| 4 | parent_amq_floor_test.go | TestUpDryRunReportsPreFloorAMQAndRendersCommandsWithoutMutations | notificationWatcherRuntimePath, runUp | no | no |
| 4 | parent_amq_floor_test.go | TestUpDryRunJSONReportsPreFloorAMQAndRetainsFullPlan | notificationWatcherRuntimePath, runUp | no | no |
| 4 | parent_amq_floor_test.go | TestUpResetRejectsPreFloorAMQBeforeDeletingSession | notificationWatcherRuntimePath, runUp | no | no |
| 4 | parent_amq_floor_test.go | TestUpResetRejectsNewlyPreFloorResolutionBeforeByteIdenticalSessionDeletion | runUp | no | no |
| 4 | parent_amq_floor_test.go | TestUpResetExternalLeadConsumesPinnedAMQSnapshotAfterDeletion | runUp | no | no |
| 4 | parent_amq_floor_test.go | TestResumeExecRejectsPreFloorAMQBeforeParentMutations | buildSimpleStartPlan, notificationWatcherRuntimePath, resumeExecRequest, runResumeExec, runSimpleStartWithRequest, seedBriefAt | no | no |
| 4 | profiles_test.go | TestRunUpProfileDryRunUsesNamedRoster | restore attempt with team show failed: up's --profile flag has no team show equivalent | no | no |
| 4 | profiles_test.go | TestEmittedCommandsCarryDefaultProfileImplicitly | runUp | partly | yes, driver up --dry-run -> team show (same plan) |
| 4 | profiles_test.go | TestRunUpProfileUnknownErrors | restore attempt with team show failed: up's --profile flag has no team show equivalent | no | no |
| 4 | profiles_test.go | TestRunDownProfileScopedToNamedRoster | downExecution, runDownExec | no | no |
| 4 | profiles_test.go | TestResumeNamedProfileBlocksLegacySessionRootConflict | planEnvelopeData, runResume | no | no |
| 4 | profiles_test.go | TestSameProfileSessionUpStatusDispatchNoNamespaceConflict | runUp | partly | yes, minus the up step (status and dispatch steps kept) |
| 4 | real_amq_compatibility_test.go | TestRealAMQCompatibility | realAMQExactInjectViaWakeRetirement, realAMQProductionOwnerBoundCleanupContract | partly | yes, minus two subtests of deleted wake helpers (skips without AMQ_SQUAD_REAL_AMQ) |
| 4 | real_amq_wake_compatibility_test.go | TestRealAMQWakeCompatibility | realAMQBusyWakeOwnershipCase, realAMQExactInjectViaWakeRetirement | no | no |
| 4 | resume_exec_test.go | TestResumeExecRoleFilterDigestIgnoresLivenessOutsideSubset | TestStartApplyRefusesWhenLivenessChangedSinceDigest, buildSimpleStartPlan, seedBriefAt, simpleStartRequest, simpleStartStubLaunchapiAMQEnv | no | no |
| 4 | resume_exec_test.go | TestStartRestoresLaunchapiMintedConversationNatively | buildSimpleStartPlan, seedBriefAt, simpleStartRequest, simpleStartStubLaunchapiAMQEnv | no | no |
| 4 | resume_required_actions_test.go | TestResumeActionIDIsNamespacedAndDeterministic | isResumeNamespacedActionID, resumeActionID, resumeActionKindLeadNotLive | no | no |
| 4 | resume_required_actions_test.go | TestSplitResumeDecisionsSeparatesNamespaces | isResumeNamespacedActionID, resumeActionID, resumeActionKindExternalLeadRecordDead, resumeActionKindLeadNotLive, resumeDecisionAbort, resumeDecisionProceedWithoutLead (+1) | no | no |
| 4 | resume_required_actions_test.go | TestSplitResumeDecisionsEmptyInputYieldsNilMaps | splitResumeDecisions | no | no |
| 4 | resume_required_actions_test.go | TestResolveResumeRequiredActionsDecidesAndReportsMissing | newExternalLeadRecordDeadAction, newLeadNotLiveAction, resolveResumeRequiredActions, resumeActionID, resumeActionKindExternalLeadRecordDead, resumeActionKindLeadNotLive (+2) | no | no |
| 4 | resume_required_actions_test.go | TestResolveResumeRequiredActionsRejectsStaleAnswer | newLeadNotLiveAction, resolveResumeRequiredActions, resumeActionID, resumeActionKindLeadNotLive, resumeDecisionAbort, resumeRequiredAction | no | no |
| 4 | resume_required_actions_test.go | TestResolveResumeRequiredActionsRejectsDisallowedChoice | newExternalLeadRecordDeadAction, resolveResumeRequiredActions, resumeActionID, resumeActionKindExternalLeadRecordDead, resumeDecisionProceedWithoutLead, resumeRequiredAction | no | no |
| 4 | resume_required_actions_test.go | TestNewExternalLeadRecordDeadActionHasNoProceedBypass | newExternalLeadRecordDeadAction, resumeDecisionAbort, resumeDecisionProceedWithoutLead | no | no |
| 4 | resume_test.go | TestResumeAliasPlanIsByteIdenticalToPlan | runPlan, runResume, seedBriefAt | no | no |
| 4 | resume_test.go | TestRunResumePlanRejectsLaunchOverrideFlags | runResume | no | no |
| 4 | resume_test.go | TestRunResumeRequiresTeam | runResume | no | no |
| 4 | resume_test.go | TestRunResumeProjectTargetsOtherDir | runResume, seedBriefAt | no | no |
| 4 | resume_test.go | TestRunResumeRoleFilterSelectsSubset | extractPlanRosterLine, planPrepareFiltered, runResume, seedBriefAt | no | no |
| 4 | resume_test.go | TestRunResumeRoleFilterRejectsUnknown | runResume | no | no |
| 4 | resume_test.go | TestRunResumeReorientsSeatWithoutConversation | runResume, seedBriefAt | no | no |
| 4 | resume_test.go | TestRunResumeSurfacesNativeGoalBlockedRecovery | TestResumeAliasPlanIsByteIdenticalToPlan, runResume, seedBriefAt | no | no |
| 4 | resume_test.go | TestRunResumeExecSurfacesBlockedGoalRecoveryForMixedRoster | buildSimpleStartPlan, seedBriefAt, simpleStartRequest | no | no |
| 4 | resume_test.go | TestRunResumeRejectsFreshFlag | runResume | no | no |
| 4 | resume_test.go | TestRunResumeHonorsExplicitSession | runResume, seedBriefAt | no | no |
| 4 | resume_test.go | TestRunResumeLastPicksTheOnlyLiveSession | runResume, seedBriefAt, writeSessionPresence | no | no |
| 4 | resume_test.go | TestRunResumeLastPicksMostRecentAmongMultipleLiveSessions | runResume, seedBriefAt, writeSessionPresence | no | no |
| 4 | resume_test.go | TestRunResumeLastRejectsExplicitSession | runResume | no | no |
| 4 | resume_test.go | TestRunResumeRestoreExistingPropagates | runResume, seedBriefAt | no | no |
| 4 | resume_test.go | TestRunResumeDoesNotMutateAMQRoot | fileTreeFingerprint, runResume, seedBriefAt | no | no |
| 4 | resume_test.go | TestRunResumeRejectsExecWithDryRun | runResume | no | no |
| 4 | resume_test.go | TestRunResumePositionalSessionHonored | runResume, seedBriefAt | no | no |
| 4 | resume_test.go | TestRunResumePositionalAndFlagIsError | runResume | no | no |
| 4 | resume_test.go | TestRunResumeTooManyPositionalsIsError | runResume | no | no |
| 4 | runtime_actions_test.go | TestResumeExecRejectsNonTmuxTerminal | runResume | no | no |
| 4 | runtime_json_test.go | TestResumeJSONRejectsExec | runResume | no | no |
| 4 | scoped_flags_test.go | TestScopedShortFlagAliasesRegistered | runAgent, runDown, runLead, runResume | partly | yes, minus the down, resume, agent up/resume, lead register rows |
| 4 | stop_namespace_conflict_test.go | TestRunDownExactNamedProfileConflictStopsAllAndPreservesLegacyState | downRunnerForTest | no | no |
| 4 | stop_namespace_conflict_test.go | TestExactNamedProfileDownWakeLockRootValidation | downRunnerForTest | no | no |
| 4 | stop_namespace_conflict_test.go | TestRunDownExactNamedProfileRoleAndClosePane | downRunnerForPaneTest | no | no |
| 4 | stop_namespace_conflict_test.go | TestRunDownExactNamedProfileRequiresEveryScopeFlag | downRunnerForTest | no | no |
| 4 | stop_namespace_conflict_test.go | TestRunDownImplicitDefaultRefusesMultipleNamedOwners | downRunnerForTest | no | no |
| 4 | stop_namespace_conflict_test.go | TestExactNamedProfileDownLaunchIdentityMismatchFailsBeforeMutation | downRunnerForTest | no | no |
| 4 | stop_namespace_conflict_test.go | TestExactNamedProfileDownSafetyRegressions | downRunnerForPaneTest, downRunnerForTest | no | no |
| 4 | stop_namespace_conflict_test.go | TestTeamMemberRmDownExactNamedConflictAndMissingSession | downRunnerForTest, runTeamMember, swapTeamMemberStopRunner | no | no |
| 4 | stop_test.go | TestExecuteDownForceSendsSIGKILL | downExecution, runDownExec | no | no |
| 4 | stop_test.go | TestExecuteDownDoesNotSignalReusedPID | downExecution, runDownExec | no | no |
| 4 | stop_test.go | TestRunDownRendersUnderDownHeader | runDown | no | no |
| 4 | stop_test.go | TestRunDownRequiresSelector | runDown | no | no |
| 4 | team_lead_test.go | TestStopDoesNotCloseExternalLeadPane | downExecution, runDownExec | no | no |
| 4 | team_lead_test.go | TestResumeDoesNotRestoreDeadExternalLeadRecord | projectLeadExternalRecordBoundaryViolation, resolveResumeLeadGate, resumeExecRequest, runResumeExec, seedBriefAt | no | no |
| 4 | team_overlay_test.go | TestUpDryRunRejectsDanglingSettings | runUp | partly | yes, driver up --dry-run -> team show (same plan) |
| 4 | team_overlay_test.go | TestGeneratedToolPoliciesDiscoverInheritancePreserveAllowAndRevokeConflicts | runUp | partly | yes, driver up --dry-run -> team show (same plan) |
| 4 | team_overlay_test.go | TestGeneratedToolPolicyDriftBlocksLaunchPlan | runUp | partly | yes, driver up --dry-run -> team show (same plan) |
| 4 | team_overlay_test.go | TestUpDryRunFailsOnMissingOverlayFile | runUp | partly | yes, driver up --dry-run -> team show (same plan) |
| 4 | up_pr6_test.go | TestRunUpNewSessionAutoStubsAndWarns | runUp, teamWith | no | no |
| 4 | up_pr6_test.go | TestRunUpSeedFromAuthorsRealBriefNoWarning | runUp, teamWith | no | no |
| 4 | up_pr6_test.go | TestRunUpPositionalSessionName | runUp, teamWith | no | no |
| 4 | up_pr6_test.go | TestRunUpPositionalAndSessionConflict | runUp, teamWith | no | no |
| 4 | up_pr6_test.go | TestRunUpResetDeclinedMakesZeroChanges | runUp, teamWith, withResetSeams | no | no |
| 4 | up_pr6_test.go | TestRunUpResetYesWipesAndRelaunches | runUp, teamWith, withResetSeams | no | no |
| 4 | up_pr6_test.go | TestRunUpResetRefusesLiveSessionWithoutForce | runUp, teamWith, withResetSeams | no | no |
| 4 | up_pr6_test.go | TestRunUpResetForceWipesLiveSession | runUp, teamWith, withResetSeams | no | no |
| 4 | up_pr6_test.go | TestRunUpRefusesOnRestorableRecord | runUp, teamWith | no | no |
| 4 | up_test.go | TestUpDryRunMatchesTeamShowCore | runUp | no | no |
| 4 | up_test.go | TestUpDryRunDefaultsToSiblingTabsVisibility | runUp | no | no |
| 4 | up_test.go | TestUpDryRunJSONIncludesClaudeWorkerPreauthAndBootstrapStatus | runUp | no | no |
| 4 | up_test.go | TestUpDryRunPermissionAllowlistIsPerMemberAndAuditable | runUp | no | no |
| 4 | up_test.go | TestUpDryRunCrossCWDClaudeWorkerPreauthMatchesLiveLaunch | runUp | no | no |
| 4 | up_test.go | TestRunUpProjectDryRunTargetsOtherDir | runUp | no | no |
| 4 | up_test.go | TestRunUpProjectSeedFromFileResolvesInsideProject | runUp | no | no |
| 4 | up_test.go | TestUpDryRunMatchesTeamShowWithFlags | runUp | no | no |
| 4 | up_test.go | TestUpDryRunWakeInjectModeNonePropagates | runUp | no | no |
| 4 | up_test.go | TestUpDryRunVisibilityChangesPreviewTopology | runUp | no | no |
| 4 | up_test.go | TestUpDryRunVisibilityPlanIsPreviewOnly | runUp | no | no |
| 4 | up_test.go | TestUpDryRunJSONCarriesVisibilityTopology | runUp | no | no |
| 4 | up_test.go | TestUpDryRunRequiresTeam | runUp | no | no |
| 4 | up_test.go | TestRunUpLiveDelegatesToBackendLaunch | runUp | no | no |
| 4 | up_test.go | TestRunUpLiveHonorsBackendFlags | runUp | no | no |
| 4 | up_test.go | TestRunUpCustomEffortIsLaunchOnlyAndWarns | runUp | no | no |
| 4 | up_test.go | TestRunUpDryRunDoesNotCallBackend | runUp | no | no |
| 4 | up_test.go | TestRunUpLiveRefusesExistingSessionByDefault | runUp | no | no |
| 4 | up_test.go | TestRunUpLiveFreshIsNoOp | runUp | no | no |
| 4 | up_test.go | TestRunUpLiveRequiresTeam | runUp | no | no |
| 4 | up_test.go | TestRunUpDryRunMixedSessionFiltersOutCrossSessionMembers | runUp | no | no |
| 4 | up_test.go | TestRunUpDryRunAllCrossSessionErrors | runUp | no | no |
| 4 | v228_contract_roster_change_test.go | TestV228ContractDownStopsExactlyTheNamedRole | downEnvelopeData, downExecution, runDownExec, v228RunStart | no | no |
| 4 | v228_contract_worktree_remnant_test.go | TestV228ContractWorktreeRemnantDownTargetsCanonicalRoot | downEnvelopeData, downExecution, executeDown, runDownExec | no | no |
| 5 | custom_role_test.go | TestNewTeamCustomRoleDryRunJSON | runNew | partly | yes, driver `new team|profile` -> `team init` (what new delegated to) |
| 5 | custom_role_test.go | TestNewTeamGenericBuiltInRolesDoNotRequireBinary | runNew | partly | yes, driver `new team|profile` -> `team init` (what new delegated to) |
| 5 | custom_role_test.go | TestNewProfileCustomRoleDryRun | runNew | partly | yes, driver `new team|profile` -> `team init` (what new delegated to) |
| 5 | custom_role_test.go | TestCustomRoleMissingBinaryFails | runNew | partly | yes, driver `new team|profile` -> `team init` (what new delegated to) |
| 5 | custom_role_test.go | TestCustomRoleBinaryForOtherRoleFails | runNew | partly | yes, driver `new team|profile` -> `team init` (what new delegated to) |
| 5 | custom_role_test.go | TestCustomAndBuiltinMix | runNew | partly | yes, driver `new team|profile` -> `team init` (what new delegated to) |
| 5 | custom_role_test.go | TestInvalidCustomSlugFails | runNew | partly | yes, driver `new team|profile` -> `team init` (what new delegated to) |
| 5 | custom_role_test.go | TestRoleFileFrontmatterBinarySatisfiesRequirement | runNew | partly | yes, driver `new team|profile` -> `team init` (what new delegated to) |
| 5 | custom_role_test.go | TestRoleFileInlinePathStagesVerbatimDoc | runNew | partly | yes, driver `new team|profile` -> `team init` (what new delegated to) |
| 5 | custom_role_test.go | TestStagedCustomRoleDiscoveredByBareID | runNew | partly | yes, driver `new team|profile` -> `team init` (what new delegated to) |
| 5 | custom_role_test.go | TestStagedCustomRoleLiveWriteDoesNotRestage | runNew | partly | yes, driver `new team|profile` -> `team init` (what new delegated to) |
| 5 | custom_role_test.go | TestExplicitRoleFileOverridesDiscoveredRole | runNew | partly | yes, driver `new team|profile` -> `team init` (what new delegated to) |
| 5 | custom_role_test.go | TestRoleFileCatalogIDCollisionFails | runNew | partly | yes, driver `new team|profile` -> `team init` (what new delegated to) |
| 5 | custom_role_test.go | TestRoleFileMissingBinaryFails | runNew | partly | yes, driver `new team|profile` -> `team init` (what new delegated to) |
| 5 | custom_role_test.go | TestRoleFileYAMLRendersStubDoc | runNew | partly | yes, driver `new team|profile` -> `team init` (what new delegated to) |
| 5 | effort_test.go | TestTeamInitEffortPersistsOnlyExistingMemberArgs | runNew | partly | yes, driver `new team|profile` -> `team init` (what new delegated to) |
| 5 | effort_test.go | TestTeamInitEffortKnownAndCustomWarningContract | runNew | partly | yes, driver `new team|profile` -> `team init` (what new delegated to) |
| 5 | effort_test.go | TestTeamInitEffortRejectsUnknownRole | runNew | partly | yes, driver `new team|profile` -> `team init` (what new delegated to) |
| 5 | help_test.go | TestNewSessionHelpDocumentsSeededBriefs | help of the deleted `new session` | no | no |
| 5 | help_test.go | TestLifecycleHelpDocumentsProject | help of the deleted `start` (its only row) | no | no |
| 5 | help_test.go | TestStartHelpDescribesSingleReconcileCommand | drives the deleted `start` verb by name | no | no |
| 5 | list_test.go | TestListRecordsFromEntriesIncludesSource | listRecordsFromEntries | no | no |
| 5 | list_test.go | TestFormatListTime | formatListTime | no | no |
| 5 | new_profile_flag_forwarding_test.go | TestNewProfileForwardsEveryValueTakingTeamInitFlag | newProfileValueFlags, teamInitFlagSet | no | no |
| 5 | new_profile_flag_forwarding_test.go | TestNewProfileValueFlagsHasNoStaleEntries | newProfileValueFlags, teamInitFlagSet | no | no |
| 5 | new_profile_flag_forwarding_test.go | TestNewProfilePeelsNameAndForwardsActorMode | newProfileTeamArgs | no | no |
| 5 | new_test.go | TestRunNewRequiresSubcommand | runNew | no | no |
| 5 | new_test.go | TestRunNewTeamDelegatesToTeamInit | runNew | no | no |
| 5 | new_test.go | TestRunNewTeamProjectWritesTargetDir | runNew | no | no |
| 5 | new_test.go | TestRunNewTeamSyncWritesPointerStubs | runNew | no | no |
| 5 | new_test.go | TestRunNewTeamDryRunSyncPrintsPlanWithoutWriting | runNew | no | no |
| 5 | new_test.go | TestRunNewTeamDryRunSyncJSONIncludesSyncCommandWithoutWriting | runNew | no | no |
| 5 | new_test.go | TestRunNewTeamSyncNamedProfilePassesProfileToSync | runNew | no | no |
| 5 | new_test.go | TestRunNewProfileDelegatesToNamedTeamInit | runNew | no | no |
| 5 | new_test.go | TestRunNewProfileForwardsEffortOverride | runNew | no | no |
| 5 | new_test.go | TestRunNewTeamRejectsProfileSessionCollisionBeforeWrite | runNew | no | no |
| 5 | new_test.go | TestRunNewProfileRejectsProfileSessionCollisionBeforeWrite | runNew | no | no |
| 5 | new_test.go | TestRunNewProfileWarnsWhenSharedRulesDescribeDifferentRoster | runNew | no | no |
| 5 | new_test.go | TestRunNewProfileNoWarnWhenSharedRulesMatchRoster | runNew | no | no |
| 5 | new_test.go | TestRunNewProfileProjectBeforeNameDryRunJSON | runNew | no | no |
| 5 | new_test.go | TestRunNewProfileRejectsDefaultAndExplicitProfileFlag | runNew | no | no |
| 5 | new_test.go | TestRunNewTeamAllowOutsideRequiresSync | runNew | no | no |
| 5 | new_test.go | TestRunNewTeamRoleSelectionShortcuts | runNew | no | no |
| 5 | new_test.go | TestRunNewProjectFlagRequiresExistingDirectory | runNew | no | no |
| 5 | new_test.go | TestRunNewProjectFlagRequiresNonEmptyDirectory | runNew | no | no |
| 5 | new_test.go | TestPeelNewSessionGoal | peelNewSessionGoal | no | no |
| 5 | new_test.go | TestRunNewSessionGoalUsesGoalFirstSurface | runNewSession | no | no |
| 5 | new_test.go | TestRunDispatchesNew | drives the deleted `new` verb by name | no | no |
| 5 | pathnorm_test.go | TestListCommandProjectFlagIsExemptFromPathNormalization | exemption row of the deleted `list` verb | no | no |
| 5 | plan_test.go | TestPlanIsZeroWrite | planPrepare, planTestSetup | no | no |
| 5 | plan_test.go | TestPlanJSONRequestRoundTripsThroughAMQLaunchPrepare | planEnvelopeData, planPrepare, planTestSetup | no | no |
| 5 | plan_test.go | TestPlanPrintsRequiredActionsVerbatim | planEnvelopeData, printPlanResult | no | no |
| 5 | plan_test.go | TestPlanWithoutBriefFailsClosedNamingBriefCommand | planPrepare, planTestSetup | no | no |
| 5 | setup_test.go | TestSetupNonInteractiveWritesGlobalDrafterAndPreservesOtherSettings | runSetupWithDependencies, setupDependencies | no | no |
| 5 | setup_test.go | TestSetupInteractiveDefaultsToDetectedChain | runSetupWithDependencies, setupDependencies | no | no |
| 5 | setup_test.go | TestSetupInteractiveWithoutDetectedBackendsUsesInSession | runSetupWithDependencies, setupDependencies | no | no |
| 5 | setup_test.go | TestSetupNonInteractiveUpdatesExistingTrustedCustomConfigWithoutDiscardingCommand | runSetupWithDependencies, setupDependencies | no | no |
| 5 | setup_test.go | TestSetupRejectsInvalidNonInteractiveChainBeforeWrite | runSetupWithDependencies, setupDependencies | no | no |
| 5 | setup_test.go | TestSetupRejectsYoetzHopWithoutModel | runSetupWithDependencies, setupDependencies | no | no |
| 5 | setup_test.go | TestSetupIsPublicAndCompletable | completionCommonFlags, completionTopCommands | no | no |
| 5 | setup_test.go | TestSetupShowPrintsEffectiveDrafterConfigWithoutWriting | runSetupWithDependencies, setupDependencies | no | no |
| 5 | setup_test.go | TestSetupShowJSONEmitsEffectiveEnvelope | runSetupWithDependencies, setupDependencies, setupShowData | no | no |
| 5 | setup_test.go | TestSetupShowUnconfiguredReportsInSessionDefault | runSetupWithDependencies, setupDependencies | no | no |
| 5 | setup_test.go | TestSetupShowRejectsMutationFlagsAndBareJSON | runSetupWithDependencies, setupDependencies | no | no |
| 5 | simple_start_test.go | TestSimpleStartCommandPinsCanonicalInputsAndExactInstruction | - | yes | yes, verbatim (no deleted reference) |
| 5 | simple_start_test.go | TestParseSimpleStartRequestAcceptsOneSessionSurface | parseSimpleStartRequest | no | no |
| 5 | simple_start_test.go | TestBootstrapContextCanonicalizesRuntimeCoordinates | - | yes | yes, verbatim (no deleted reference) |
| 5 | simple_start_test.go | TestSelectStatusLaunchRecordRequiresExactHandleIdentity | - | yes | yes, verbatim (no deleted reference) |
| 5 | simple_start_test.go | TestReconcileSimpleStartRolesUsesRecordedRuntimeAndClassifiesDrift | reconcileSimpleStartRoles, simpleStartRecord | no | no |
| 5 | simple_start_test.go | TestReconcileSimpleStartRolesRejectsDuplicateLive | reconcileSimpleStartRoles, simpleStartConflictError, simpleStartRecord | no | no |
| 5 | simple_start_test.go | TestReconcileSimpleStartRolesTreatsSameRoleWrongHandleAsUnmanaged | reconcileSimpleStartRoles, simpleStartRecord | no | no |
| 5 | simple_start_test.go | TestFilterSimpleStartRolesBySubsetSpawnsOnlyRequestedRoles | filterSimpleStartRolesBySubset, simpleStartRolePlan | no | no |
| 5 | simple_start_test.go | TestFilterSimpleStartRolesBySubsetRejectsUnknownRole | filterSimpleStartRolesBySubset, simpleStartRolePlan | no | no |
| 5 | simple_start_test.go | TestClassifyRecordlessSimpleStartPaneIsUnmanagedConflict | classifyRecordlessSimpleStartPanes, simpleStartConflictError, simpleStartPlan, simpleStartRolePlan | no | no |
| 5 | simple_start_test.go | TestSimpleStartCheckpointWrapsSentinel | - | yes | yes, verbatim (no deleted reference) |
| 5 | simple_start_test.go | TestSimpleStartFailsClosedOnSharedImplementationCWD | buildSimpleStartPlan, newSimpleStartRunFixture, simpleStartRequest | no | no |
| 5 | simple_start_test.go | TestSimpleStartNewSessionAcceptsOwnedLivePaneAfterTitleRewrite | buildSimpleStartPlan, newSimpleStartRunFixture, seedRecord, simpleStartRequest | no | no |
| 5 | simple_start_test.go | TestValidateSimpleStartTmuxTargetRequiresCorroboratedRecordInTargetSession | simpleStartConflictError, simpleStartRecord, validateSimpleStartTmuxTarget | no | no |
| 5 | simple_start_test.go | TestRunStartWithDependenciesApprovalDefaultsNo | newSimpleStartRunFixture, runStartWithDependencies, simpleStartLockPath | no | no |
| 5 | simple_start_test.go | TestRunStartWithDependenciesHoldsExactLockThroughSpawnVerification | newSimpleStartRunFixture, runStartWithDependencies, seedRecord, simpleStartLaunchResult, simpleStartLockPath | no | no |
| 5 | simple_start_test.go | TestRunStartWithDependenciesRejectsDeadPIDWithSurvivingTitledPane | newSimpleStartRunFixture, runStartWithDependencies, seedRecord, simpleStartLaunchResult | no | no |
| 5 | simple_start_test.go | TestRunStartWithDependenciesLauncherPIDImageIsAccepted | newSimpleStartRunFixture, runStartWithDependencies, seedRecord, simpleStartLaunchResult | no | no |
| 5 | simple_start_test.go | TestRunStartWithDependenciesSpawnsConfiguredHandleBesideForeignSameRoleRecord | newSimpleStartRunFixture, runStartWithDependencies, seedRecord, simpleStartLaunchResult | no | no |
| 5 | simple_start_test.go | TestRunStartWithDependenciesClassifiesUnmanagedInvalidAndRemovedRecords | newSimpleStartRunFixture, runStartWithDependencies, seedRecord, simpleStartConflictError | no | no |
| 5 | simple_start_test.go | TestSimpleStartRestoreComposesRecordedConversationWithoutBootstrap | buildSimpleStartPlan, newSimpleStartRunFixture, seedRecord, simpleStartRequest, validateSimpleStartRestoreResultCommands | no | no |
| 5 | simple_start_test.go | TestRunStartRejectsRestoreResultThatDropsRecordedConversation | newSimpleStartRunFixture, runStartWithDependencies, seedRecord | no | no |
| 5 | simple_start_test.go | TestStartRefusesLegacyMintedRestoreOnLaunchapiPath | newSimpleStartRunFixture, runStartWithDependencies, seedRecord | no | no |
| 5 | simple_start_test.go | TestStartApplyRejectsStaleSubjectDigest | newSimpleStartRunFixture, runStartWithDependencies, simpleStartStubLaunchapiAMQEnv | no | no |
| 5 | simple_start_test.go | TestStartApplyRequiresExactDecisionsForEveryRequiredAction | - | yes | yes, verbatim (no deleted reference) |
| 5 | simple_start_test.go | TestStartApplyRefusesWhenLivenessChangedSinceDigest | flipOnFirstReadReader, runStartWithDependencies, simpleStartLaunch, simpleStartStubLaunchapiAMQEnv | no | no |
| 5 | simple_start_test.go | TestStartDeprecatedTrustFlagPrintsEquivalentDecision | parseSimpleStartRequest | no | no |
| 5 | simple_start_test.go | TestStartHonorsLegacyOptOutForRestore | newSimpleStartRunFixture, runStartWithDependencies, seedRecord | no | no |
| 5 | simple_start_test.go | TestSimpleStartGoalIsLastAndNeverResentOnSpawnlessRerun | newSimpleStartRunFixture, runStartWithDependencies, seedRecord, seedSimpleStartBrief, simpleStartLaunchResult, simpleStartPlan | no | no |
| 5 | simple_start_test.go | TestSimpleStartGoalFailureWarnsAfterSuccessfulLaunch | newSimpleStartRunFixture, runStartWithDependencies, seedRecord, seedSimpleStartBrief, simpleStartLaunchResult, simpleStartPlan | no | no |
| 5 | simple_start_test.go | TestValidateSimpleStartBriefDraftExactShape | - | yes | yes, verbatim (no deleted reference) |
| 5 | simple_start_test.go | TestAllMarkdownBulletsAcceptsAsteriskAndPlusMarkers | - | yes | yes, verbatim (no deleted reference) |
| 5 | simple_start_test.go | TestNormalizeMarkdownBulletMarkerLeavesNonBulletProseUntouched | - | yes | yes, verbatim (no deleted reference) |
| 5 | simple_start_test.go | TestEnsureSimpleStartBriefPublishesWithoutOverwrite | ensureSimpleStartBrief | no | no |
| 5 | simple_start_test.go | TestReadSimpleStartRecordsRejectsMismatchedCanonicalCoordinates | newSimpleStartRunFixture, readSimpleStartRecords, seedRecord, simpleStartConflictError | no | no |
| 5 | simple_start_test.go | TestVerifySimpleStartRecordsPollsForRecordPublication | newSimpleStartRunFixture, seedRecord, simpleStartLaunchResult, simpleStartPlan, verifySimpleStartRecords | no | no |
| 5 | simple_start_test.go | TestRunStartDefaultsToLaunchapiOnTmux | simpleStartLaunch | no | no |
| 5 | simple_start_test.go | TestStartWithoutBriefFailsClosedNamingBriefCommand | buildSimpleStartPlan, newSimpleStartRunFixture, simpleStartRequest | no | no |
| 5 | simple_start_test.go | TestStartNeverInvokesDrafter | buildSimpleStartPlan, newSimpleStartRunFixture, simpleStartRequest | no | no |
| 5 | simple_wizard_test.go | TestWizardCommandIsDiscoverable | completionTopCommands, runWizardWithDependencies, simpleWizardTestDependencies | no | no |
| 5 | simple_wizard_test.go | TestWizardIsCompositionOfBriefInitPlanStart | runWizardWithDependencies, simpleWizardTestDependenciesForMembers | no | no |
| 5 | simple_wizard_test.go | TestWizardRequiresRolesForNewProfile | runWizardWithDependencies, simpleWizardTestDependencies | no | no |
| 5 | simple_wizard_test.go | TestWizardNamesRoleDraftForUndocumentedCustomRole | runWizardWithDependencies, simpleWizardTestDependenciesForMembers | no | no |
| 5 | simple_wizard_test.go | TestWizardWorktreeIsolationRefusalNamesBothEscapeHatches | buildSimpleStartPlan, runWizardWithDependencies, simpleWizardTestDependencies, wizardStartArgs | no | no |
| 5 | simple_wizard_test.go | TestWizardDefaultNoLeavesEveryArtifactAbsent | runWizardWithDependencies, simpleWizardTestDependencies | no | no |
| 5 | simple_wizard_test.go | TestWizardYesWritesReviewedArtifactsThenDelegatesToStart | runWizardWithDependencies, simpleStartStubLaunchapiAMQEnv, simpleWizardTestDependencies | no | no |
| 5 | simple_wizard_test.go | TestWizardRealHandoffAppliesComputedDigestAndInvokesLaunch | buildSimpleStartPlan, buildSimpleWizardPlan, installFakeClaudeBinary, parseSimpleStartRequest, runStartWithDependencies, runWizardWithDependencies (+4) | no | no |
| 5 | simple_wizard_test.go | TestStartCancelsOnEmptyReaderWithoutApplyOnLaunchapiPath | newSimpleStartRunFixture, runStartWithDependencies, simpleStartStubLaunchapiAMQEnv | no | no |
| 5 | simple_wizard_test.go | TestWizardRefusesPlanChangedAtConfirmation | runWizardWithDependencies, simpleWizardTestDependencies, wizardMutationReader | no | no |
| 5 | simple_wizard_test.go | TestWizardReadinessFailsBeforeDrafterRuns | runWizardWithDependencies, simpleWizardTestDependencies | no | no |
| 5 | simple_wizard_test.go | TestWizardReadinessFailsOnYoetzWithoutModel | runWizardWithDependencies, simpleWizardTestDependencies | no | no |
| 5 | simple_wizard_test.go | TestWizardImplicitProfileCollisionUsesExistingFlow | runWizardWithDependencies, simpleWizardTestDependenciesForMembers | no | no |
| 5 | simple_wizard_test.go | TestWizardExistingProfileRejectsExplicitDefaultValuedNewProfileFlag | runWizardWithDependencies, simpleWizardTestDependenciesForMembers | no | no |
| 5 | simple_wizard_test.go | TestWizardExistingReusableProfileUsesFlowBWithoutRewritingProfileOrRules | runWizardWithDependencies, simpleWizardArtifact, simpleWizardTestDependenciesForMembers | no | no |
| 5 | simple_wizard_test.go | TestInSessionFallthroughIncludesAttemptEvidence | runWizardWithDependencies, simpleWizardTestDependencies, simpleWizardTestDependenciesForMembers | no | no |
| 5 | team_no_session_pin_test.go | TestNewProfileForwardsNoSessionPin | runNew | no | no |
| 5 | team_shared_cwd_test.go | TestTeamSharedCwdExceptionSetAcceptsValidSessionScope | drives the deleted `team shared-cwd-exception` subcommand | no | no |
| 5 | team_shared_cwd_test.go | TestTeamSharedCwdExceptionSetRejectsInvalidSessionScope | drives the deleted `team shared-cwd-exception` subcommand | no | no |
| 5 | v228_contract_crash_injection_test.go | TestV228ContractAbortAfterNamespaceCreationConverges | v228AssertCrashConverges | no | no |
| 5 | v228_contract_crash_injection_test.go | TestV228ContractAbortAfterPaneCreationConverges | v228AssertCrashConverges | no | no |
| 5 | v228_contract_crash_injection_test.go | TestV228ContractAbortAfterChildDispatchConverges | v228AssertCrashConverges | no | no |
| 5 | v228_contract_crash_injection_test.go | TestV228ContractAbortAfterLaunchRecordWriteConverges | v228AssertCrashConverges | no | no |
| 5 | v228_contract_crash_injection_test.go | TestV228ContractChildDeathIsPerRoleAndRerunConverges | v228RunCrashStart | no | no |
| 5 | v228_contract_crash_injection_test.go | TestV228ContractProductionStartHasNoCheckpointHook | asserts the AfterCheckpoint seam of the deleted start dependencies | no | no |
| 5 | v228_contract_goal_delivery_step3_test.go | TestV228ContractWizardGoalDeliveredOnlyAfterAllAgentsLive | v228RunGoalStart | no | no |
| 5 | v228_contract_goal_delivery_step3_test.go | TestV228ContractSkippedGoalCanBeAssignedLater | deliverSimpleStartGoal, simpleStartPlan, v228RunGoalStart | no | no |
| 5 | v228_contract_goal_delivery_step3_test.go | TestV228ContractStartRerunNeverResendsGoal | v228RunGoalStart | no | no |
| 5 | v228_contract_goal_delivery_test.go | TestV228ContractGoalIsNotALaunchPrecondition | v228RunStart | no | no |
| 5 | v228_contract_legacy_namespace_test.go | TestV228ContractDeadEndedV227NamespaceRecoversWithoutDeletion | v228RunStart | no | no |
| 5 | v228_contract_restore_step3_test.go | TestV228ContractRestoredAgentRetainsPreExitContext | runStartWithDependencies | no | no |
| 5 | v228_contract_restore_step3_test.go | TestV228ContractRestoreWithoutConversationFailsBeforeStarted | runStartWithDependencies | no | no |
| 5 | v228_contract_roster_change_test.go | TestV228ContractRosterAddSpawnsOnlyTheNewAgent | v228RunStart | no | no |
| 5 | worktree_isolation_reachability_test.go | TestSharedCwdRemedyIsolatedCwdsReachableInOneCommand | runNew | partly | yes, driver `new team|profile` -> `team init` (what new delegated to) |
| 5 | worktree_isolation_reachability_test.go | TestSharedCwdRemedyExceptionReachableInOneCommand | runNew | partly | yes, driver `new team|profile` -> `team init` (what new delegated to) |
| 5 | worktree_isolation_reachability_test.go | TestSharedCwdRemedyIsolatedCwdReachableOnExistingRoster | runNew, runTeamMember | no | no |
| 5 | worktree_isolation_reachability_test.go | TestWorktreeIsolationFixNamesScopedExecutableRemedies | - | yes | yes, driver `new team|profile` -> `team init` (what new delegated to) |
| 5 | worktree_isolation_reachability_test.go | TestSharedCwdExceptionFixCommandExecutesAsDisplayed | executes the displayed `team shared-cwd-exception` fix, a deleted subcommand (restore attempt failed at run time) | no | no |
| 5 | worktree_isolation_reachability_test.go | TestFixTextCommandExecutesAsDisplayed | runTeamMember | no | no |
| 5 | worktree_isolation_reachability_test.go | TestRelativeCwdResolvesAgainstProjectNotShellCwd | runNew, runTeamMember | no | no |
| 6 | namespace_migration_test.go | TestNamespaceMigrationExpiredWatcherLeaseWithLiveMatchingPIDBlocks | namespaceMigrationPlannerOptions, notificationWatcherPIDAlive, notificationWatcherProcessMatch, notificationWatcherRecord, notificationWatcherRuntimePath, notificationWatcherSchema (+2) | no | no |
| 6 | notification_amq_watch_test.go | TestNotificationAMQWatchRealChildHelper | notificationAMQRealChildEnv | no | no |
| 6 | notification_amq_watch_test.go | TestManagedNotificationAMQWatchCollectsExactMailboxOncePerSignal | notificationAMQContext, notificationAMQWatchResult, notificationWatcherExecution, runManagedNotificationAMQWatch | no | no |
| 6 | notification_amq_watch_test.go | TestManagedNotificationAMQWatchReplaysCollectBeforeFirstSignal | notificationAMQWatchResult, runManagedNotificationAMQWatch | no | no |
| 6 | notification_amq_watch_test.go | TestManagedNotificationAMQWatchRetriesPendingCollectWithoutAnotherSignal | notificationAMQWatchResult, runManagedNotificationAMQWatch | no | no |
| 6 | notification_amq_watch_test.go | TestManagedNotificationAMQWatchBoundsCrashRestarts | notificationAMQWatchResult, runManagedNotificationAMQWatch | no | no |
| 6 | notification_amq_watch_test.go | TestManagedNotificationAMQWatchRecoveryResetsFailureCount | notificationAMQWatchResult, runManagedNotificationAMQWatch | no | no |
| 6 | notification_amq_watch_test.go | TestManagedNotificationAMQWatchOwnershipLossIsFatalWithoutRetry | errNotificationAMQOwnershipLost, notificationAMQWatchResult, runManagedNotificationAMQWatch | no | no |
| 6 | notification_amq_watch_test.go | TestManagedNotificationAMQWatchStopCancelsBlockedWatch | notificationAMQWatchResult, runManagedNotificationAMQWatch | no | no |
| 6 | notification_amq_watch_test.go | TestNotificationWatcherReapsRealAMQChildOnEveryExitClass | TestNotificationAMQWatchRealChildHelper, assertInactiveWatcherTombstone, executeNotificationWatcher, notificationAMQRealChildEnv, notificationWatcherExecution, notificationWatcherRecord (+4) | no | no |
| 6 | notification_amq_watch_test.go | TestNotificationWatcherStopCancelsRealCollectWaitingOnJournalLock | assertInactiveWatcherTombstone, collectJournalDirectoryPerm, collectNotificationAMQ, executeNotificationWatcher, newCollectJournal, notificationAMQContext (+3) | no | no |
| 6 | notification_amq_watch_test.go | TestValidateNotificationAMQContextRejectsWrongRoot | notificationAMQContext, notificationWatcherExecution, validateNotificationAMQContext | no | no |
| 6 | notification_amq_watch_test.go | TestRunNotificationAMQWatchWaitsForMissingRootWithoutCreatingIt | runNotificationAMQWatch | no | no |
| 6 | notification_amq_watch_test.go | TestParseNotificationAMQWatchOutputRequiresValidSignaledEvent | parseNotificationAMQWatchOutput | no | no |
| 6 | notification_amq_watch_test.go | TestExecuteCollectDrainContextStopsBeforeJournalMutation | executeCollectDrainContext, newCollectJournal | no | no |
| 6 | notification_amq_watch_test.go | TestNotificationWatcherRecordIncludesManagedAMQBackendState | notificationWatcherRecord, notificationWatcherSchema | no | no |
| 6 | notification_amq_watch_test.go | TestNotificationWatcherDuplicateAMQSignalsDoNotNotifyWithoutCollection | executeNotificationWatcher, notificationWatcherExecution, notificationWatcherRecord, notificationWatcherRuntimePath, notificationWatcherTeam, stopTestNotificationWatcher (+1) | no | no |
| 6 | notification_amq_watch_test.go | TestNotificationWatcherAMQExhaustionIsVisibleAndKeepsFallbackActive | executeNotificationWatcher, notificationWatcherExecution, notificationWatcherRecord, notificationWatcherRuntimePath, notificationWatcherTeam, stopTestNotificationWatcher (+1) | no | no |
| 6 | notification_watcher_test.go | TestNotificationWatcherInitialScanFSNotifyAndPeriodicRescan | inspectNotificationWatcher, notificationWatcherRecord, notificationWatcherRuntimePath, notificationWatcherTeam, startTestNotificationWatcher, stopTestNotificationWatcher (+1) | no | no |
| 6 | notification_watcher_test.go | TestNotificationWatcherSignalReleaseAllowsImmediateRestart | assertInactiveWatcherTombstone, notificationWatcherRecord, notificationWatcherRuntimePath, notificationWatcherTeam, startTestNotificationWatcher, stopTestNotificationWatcher (+1) | no | no |
| 6 | notification_watcher_test.go | TestNotificationWatcherPolicyDisableReleaseAllowsImmediateReenable | assertInactiveWatcherTombstone, notificationWatcherRecord, notificationWatcherRuntimePath, notificationWatcherTeam, startTestNotificationWatcher, stopTestNotificationWatcher (+1) | no | no |
| 6 | notification_watcher_test.go | TestNotificationWatcherReleasedGenerationCannotClearOrResurrectLease | assertInactiveWatcherTombstone, notificationWatcherRecord, notificationWatcherRuntimePath, notificationWatcherSchema, notificationWatcherTeam, readNotificationWatcherRecord (+3) | no | no |
| 6 | notification_watcher_test.go | TestStopNotificationWatcherToleratesChildSelfReleaseAndESRCH | assertInactiveWatcherTombstone, notificationWatcherPIDAlive, notificationWatcherProcessMatch, notificationWatcherRecord, notificationWatcherRuntimePath, notificationWatcherSchema (+6) | no | no |
| 6 | notification_watcher_test.go | TestStopNotificationWatcherSuccessfulSignalAcceptsChildTombstone | assertInactiveWatcherTombstone, notificationWatcherPIDAlive, notificationWatcherProcessMatch, notificationWatcherRecord, notificationWatcherRuntimePath, notificationWatcherSchema (+6) | no | no |
| 6 | notification_watcher_test.go | TestStopNotificationWatcherSuccessfulSignalClearsObservedGeneration | assertInactiveWatcherTombstone, notificationWatcherPIDAlive, notificationWatcherProcessMatch, notificationWatcherRecord, notificationWatcherRuntimePath, notificationWatcherSchema (+4) | no | no |
| 6 | notification_watcher_test.go | TestStopNotificationWatcherSuccessfulSignalPreservesSameTokenCrossScopeRecord | notificationWatcherPIDAlive, notificationWatcherProcessMatch, notificationWatcherRecord, notificationWatcherRuntimePath, notificationWatcherSchema, notificationWatcherSignal (+4) | no | no |
| 6 | notification_watcher_test.go | TestStopNotificationWatcherSignalErrorPreservesReplacementOwner | notificationWatcherPIDAlive, notificationWatcherProcessMatch, notificationWatcherRecord, notificationWatcherRuntimePath, notificationWatcherSchema, notificationWatcherSignal (+4) | no | no |
| 6 | notification_watcher_test.go | TestStopNotificationWatcherSuccessfulSignalPreservesReplacementOwner | notificationWatcherPIDAlive, notificationWatcherProcessMatch, notificationWatcherRecord, notificationWatcherRuntimePath, notificationWatcherSchema, notificationWatcherSignal (+4) | no | no |
| 6 | notification_watcher_test.go | TestStopNotificationWatcherRejectsInvalidTombstoneAfterSignal | notificationWatcherPIDAlive, notificationWatcherProcessMatch, notificationWatcherRecord, notificationWatcherRuntimePath, notificationWatcherSchema, notificationWatcherSignal (+4) | no | no |
| 6 | notification_watcher_test.go | TestStopNotificationWatcherSignalErrorPreservesObservedOwner | notificationWatcherPIDAlive, notificationWatcherProcessMatch, notificationWatcherRecord, notificationWatcherRuntimePath, notificationWatcherSchema, notificationWatcherSignal (+4) | no | no |
| 6 | notification_watcher_test.go | TestNotificationWatcherStartsBeforeSessionWithoutCreatingIt | notificationWatcherRecord, notificationWatcherRuntimePath, notificationWatcherTeam, startTestNotificationWatcher, stopTestNotificationWatcher, waitWatcherRecord | no | no |
| 6 | notification_watcher_test.go | TestNotificationWatcherLeaseExpiryRecoveryAndSingleOwner | executeNotificationWatcher, notificationWatcherExecution, notificationWatcherRecord, notificationWatcherRuntimePath, notificationWatcherTeam, startTestNotificationWatcher (+3) | no | no |
| 6 | notification_watcher_test.go | TestNotificationWatcherFreshRemoteLeaseFencesAndStopRefuses | notificationWatcherRecord, notificationWatcherRuntimePath, notificationWatcherTeam, readNotificationWatcherRecord, stopNotificationWatcher, writeNotificationWatcherRecord | no | no |
| 6 | notification_watcher_test.go | TestNotificationWatcherReadinessRequiresInitialScan | notificationWatcherReady, notificationWatcherStatus | no | no |
| 6 | notification_watcher_test.go | TestNotificationWatcherStartupPollingWaitsForDegradedFallbackScan | notificationWatcherProcess, notificationWatcherRecord, notificationWatcherRuntimePath, notificationWatcherSchema, notificationWatcherSleep, notificationWatcherSpawn (+4) | no | no |
| 6 | notification_watcher_test.go | TestNotificationWatcherRemoteHealthIsNotMaskedAndNeverSpawnsRival | doctorCheckNotificationWatcher, enrichBoardNotificationWatcher, inspectNotificationWatcher, notificationWatcherProcess, notificationWatcherRecord, notificationWatcherRuntimePath (+6) | no | no |
| 6 | notification_watcher_test.go | TestNotificationWatcherNamespaceIsolation | notificationWatcherRecord, notificationWatcherRuntimePath, notificationWatcherTeam, startTestNotificationWatcher, stopTestNotificationWatcher, waitWatcherRecord | no | no |
| 6 | notification_watcher_test.go | TestNotificationWatcherSinkFailureDegradesThenRetries | notificationWatcherRecord, notificationWatcherRuntimePath, notificationWatcherTeam, startTestNotificationWatcher, stopTestNotificationWatcher, waitWatcherRecord | no | no |
| 6 | notification_watcher_test.go | TestNotificationWatcherHeartbeatContinuesAndStopCancelsBlockedSink | notificationWatcherRecord, notificationWatcherRuntimePath, notificationWatcherTeam, startTestNotificationWatcher, stopTestNotificationWatcher, waitWatcherRecord | no | no |
| 6 | notification_watcher_test.go | TestNotificationWatcherFSNotifyFailureUsesVisiblePeriodicFallback | executeNotificationWatcher, notificationWatcherExecution, notificationWatcherRecord, notificationWatcherRuntimePath, notificationWatcherTeam, stopTestNotificationWatcher (+1) | no | no |
| 6 | notification_watcher_test.go | TestLeadPaneNotificationsStartDespitePollNotRequired | inspectNotificationWatcher, notificationWatcherPIDAlive, notificationWatcherProcess, notificationWatcherProcessMatch, notificationWatcherRecord, notificationWatcherRuntimePath (+5) | no | no |
| 6 | notification_watcher_test.go | TestLeadPaneDirectGateDeliversOnceAcrossRescanAndRestart | notificationWatcherRecord, notificationWatcherRuntimePath, notificationWatcherTeam, startTestNotificationWatcher, stopTestNotificationWatcher, waitWatcherRecord (+1) | no | no |
| 6 | notification_watcher_test.go | TestNotificationReservationCoversConfiguredSinkTimeout | - | yes | yes, verbatim (no deleted reference) |
| 6 | notification_watcher_test.go | TestNotificationWatcherRejectsTraversalScope | executeNotificationWatcher, notificationWatcherExecution | no | no |
| 6 | notification_watcher_test.go | TestNotificationWatcherStopTimeoutPreservesOwnership | notificationWatcherNow, notificationWatcherPIDAlive, notificationWatcherProcessMatch, notificationWatcherRecord, notificationWatcherRuntimePath, notificationWatcherSignal (+5) | no | no |
| 6 | notification_watcher_test.go | TestNotificationWatcherAliveIdentityMismatchFailsClosed | notificationWatcherPIDAlive, notificationWatcherProcess, notificationWatcherProcessMatch, notificationWatcherRecord, notificationWatcherRuntimePath, notificationWatcherSpawn (+5) | no | no |
| 6 | notification_watcher_test.go | TestNotificationWatcherCopiedRuntimeProjectBindingRejected | inspectNotificationWatcher, notificationWatcherRecord, notificationWatcherRuntimePath, notificationWatcherTeam, writeNotificationWatcherRecord | no | no |
| 6 | notification_watcher_test.go | TestNotificationWatcherMissingNamespaceWatchIsReadOnly | addNotificationWatchTree | no | no |
| 6 | notification_watcher_test.go | TestNotificationWatcherStatusAndDoctorHealthSurfaces | doctorCheckNotificationWatcher, inspectNotificationWatcher, notificationWatcherRecord, notificationWatcherRuntimePath, notificationWatcherTeam, statusNotificationWatcherWarnings (+1) | no | no |
| 6 | notification_watcher_test.go | TestNotificationWatcherStaleLeaseStatusAndDoctorUnhealthy | doctorCheckNotificationWatcher, notificationWatcherRecord, notificationWatcherRuntimePath, notificationWatcherSchema, notificationWatcherTeam, statusNotificationWatcherWarnings (+1) | no | no |
| 6 | notification_watcher_test.go | TestStopNotificationWatcherWithoutRuntimeWritesInactiveTombstone | inspectNotificationWatcher, notificationWatcherTeam, stopNotificationWatcher | no | no |
| 6 | notification_watcher_test.go | TestStopNotificationWatcherPreservesPreexistingInactiveTombstone | notificationWatcherRecord, notificationWatcherRuntimePath, notificationWatcherSchema, notificationWatcherTeam, readNotificationWatcherRecord, stopNotificationWatcher (+1) | no | no |
| 6 | notification_watcher_test.go | TestStopNotificationWatcherPreservesInvalidPreexistingBlankOwnerRecord | notificationWatcherRecord, notificationWatcherRuntimePath, notificationWatcherSchema, notificationWatcherTeam, readNotificationWatcherRecord, stopNotificationWatcher (+1) | no | no |
| 6 | notification_watcher_test.go | TestStatusBoardNotificationWatcherDegradesLiveSession | boardNotificationWatcherCell, enrichBoardNotificationWatcher, notificationWatcherTeam | no | no |
| 6 | notification_watcher_test.go | TestExecuteRmRefusesRemoteWatcherBeforeNamespaceMutation | notificationWatcherRecord, notificationWatcherRuntimePath, notificationWatcherTeam, writeNotificationWatcherRecord | no | no |
| 6 | notification_watcher_test.go | TestLaunchFailureCleanupIsGenerationAwareAndRetainsPartialAgent | cleanupCreatedNotificationWatcherAfterLaunchFailure, inspectNotificationWatcher, notificationWatcherPIDAlive, notificationWatcherPartialLaunchGrace, notificationWatcherPartialLaunchInterval, notificationWatcherProcessMatch (+6) | no | no |
| 6 | session_notifier_test.go | TestSessionNotifierUnverifiedWakeFallsBackExactlyOncePerMessage | newSessionNotifierAttemptLedger, notifySessionInboxArrival, sessionNotifierWakeAbsent, writeSessionNotifierMessage | no | no |
| 6 | session_notifier_test.go | TestSessionNotifierAttemptLedgerNamespacesMessageIDsByHandle | Reserve, newSessionNotifierAttemptLedger | no | no |
| 6 | session_notifier_test.go | TestSessionNotifierAttemptLedgerIsBoundedAndPruned | Prune, Reserve, newSessionNotifierAttemptLedger, sessionNotifierAttemptLimit, sessionNotifierAttempted | no | no |
| 6 | session_notifier_test.go | TestSessionNotifierMalformedMessageFailsClosed | newSessionNotifierAttemptLedger, notifySessionInboxArrival, sessionNotifierWakeAbsent | no | no |
| 6 | session_notifier_test.go | TestSessionNotifierMalformedStartupMessageDoesNotBlockValidWork | executeSessionNotifier, sessionNotifierExecution, sessionNotifierWakeAbsent, writeSessionNotifierMessage | no | no |
| 6 | session_notifier_test.go | TestSessionNotifierFailedNudgeIsReservedWithoutRetry | newSessionNotifierAttemptLedger, notifySessionInboxArrival, sessionNotifierWakeAbsent, writeSessionNotifierMessage | no | no |
| 6 | session_notifier_test.go | TestSessionNotifierVerifiedWakeOwnsInput | newSessionNotifierAttemptLedger, notifySessionInboxArrival, sessionNotifierWakeAbsent, writeSessionNotifierMessage | no | no |
| 6 | session_notifier_test.go | TestSessionNotifierForeignRootWakeFallsBackBeforeReservationBecomesPermanent | newSessionNotifierAttemptLedger, notifySessionInboxArrival, writeSessionNotifierMessage | no | no |
| 6 | session_notifier_test.go | TestSessionNotifierPersistentReservationSurvivesFailedSend | newPersistentSessionNotifierAttemptLedger, notifySessionInboxArrival, readSessionNotifierRecord, sessionNotifierAttempted, sessionNotifierRecord, sessionNotifierRuntimePath (+4) | no | no |
| 6 | session_notifier_test.go | TestSessionNotifierStartupNudgesPendingInboxMessage | executeSessionNotifier, readSessionNotifierRecord, sessionNotifierAttempted, sessionNotifierExecution, sessionNotifierRuntimePath, sessionNotifierWakeAbsent (+1) | no | no |
| 6 | session_notifier_test.go | TestSessionNotifierHeartbeatRescansUnseenInboxMessage | newSessionNotifierAttemptLedger, rescanSessionInboxMessages, sessionNotifierWakeAbsent, writeSessionNotifierMessage | no | no |
| 6 | session_notifier_test.go | TestSessionNotifierRerunAdoptsExistingProcessWithoutTmux | notificationWatcherProcess, reconcileSessionNotifierStarted, sessionNotifierNow, sessionNotifierPIDAlive, sessionNotifierProcessMatch, sessionNotifierRecord (+5) | no | no |
| 6 | session_notifier_test.go | TestStopSessionNotifierRecreatesRuntimeParentBeforeLock | notificationWatcherSignal, readSessionNotifierRecord, sessionNotifierNow, sessionNotifierPIDAlive, sessionNotifierProcessMatch, sessionNotifierRecord (+4) | no | no |
| 6 | v228_contract_idle_wake_step3_test.go | TestV228ContractIdleAgentActsOnMessageWithoutKeystrokes | newSessionNotifierAttemptLedger, notifySessionInboxArrival, sessionNotifierWakeAbsent, v228SeedInboxMessage | no | no |
| 6 | v228_contract_idle_wake_step3_test.go | TestV228ContractNotifierAnnouncesPendingInboxOnStartup | v228NewNotifierFixture, v228RunNotifierUntil, v228SeedInboxMessage | no | no |
| 6 | v228_contract_idle_wake_step3_test.go | TestV228ContractNotifierIsPerSessionAndSpawnedByStart | inspectSessionNotifier, notificationWatcherProcess, notificationWatcherSignal, reconcileSessionNotifierStarted, sessionNotifierNow, sessionNotifierPIDAlive (+9) | no | no |
| 7 | external_orchestrator_lifecycle_test.go | TestExternalOrchestratorMailboxRejectsIntermediateSameInodeAliasSwap | canonicalExternalOrchestratorPath, createExternalOrchestratorMailboxFixture, externalOrchestratorMailboxContainmentHook, verifyExternalOrchestratorMailbox | no | no |
| 7 | external_orchestrator_registry_test.go | TestExternalOrchestratorRegistryScopedIdentityAndGeneration | beginExternalOrchestratorRegistration, externalOrchestratorRegistryPath, externalOrchestratorStatePlanned, externalOrchestratorStateStale, externalOrchestratorTransitionEvidence, readExternalOrchestratorRegistry (+2) | no | no |
| 7 | external_orchestrator_registry_test.go | TestExternalOrchestratorRegistryMonotonicTransitionsAndDuplicateReplay | beginExternalOrchestratorRegistration, externalOrchestratorStateDead, externalOrchestratorStateMailboxInvoked, externalOrchestratorStateMailboxUncertain, externalOrchestratorStateMailboxVerified, externalOrchestratorStateRegistered (+4) | no | no |
| 7 | external_orchestrator_registry_test.go | TestExternalOrchestratorRegistryAtomicFsyncCrashRestartRead | beginExternalOrchestratorRegistration, externalOrchestratorRegistryDirectorySync, externalOrchestratorRegistryFault, externalOrchestratorRegistryFileSync, externalOrchestratorRegistryPath, externalOrchestratorStateMailboxInvoked (+6) | no | no |
| 7 | external_orchestrator_registry_test.go | TestExternalOrchestratorRegistryConcurrentDuplicateIsSingleTransition | beginExternalOrchestratorRegistration, externalOrchestratorStateMailboxInvoked, externalOrchestratorTransitionEvidence, readExternalOrchestratorRegistry, testExternalOrchestratorIdentity, transitionExternalOrchestratorRegistration | no | no |
| 7 | external_orchestrator_registry_test.go | TestExternalOrchestratorRegistryRejectsExistingAncestorSymlink | assertExternalOrchestratorOutsideSentinel, beginExternalOrchestratorRegistration, testExternalOrchestratorIdentity, writeExternalOrchestratorOutsideSentinel | no | no |
| 7 | external_orchestrator_registry_test.go | TestExternalOrchestratorRegistryRejectsValidationOpenAncestorSwap | assertExternalOrchestratorOutsideSentinel, beginExternalOrchestratorRegistration, externalOrchestratorRegistryContainmentHook, testExternalOrchestratorIdentity, writeExternalOrchestratorOutsideSentinel | no | no |
| 7 | external_orchestrator_registry_test.go | TestExternalOrchestratorRegistryRejectsTargetSwapBeforeRename | assertExternalOrchestratorOutsideSentinel, beginExternalOrchestratorRegistration, externalOrchestratorRegistryContainmentHook, externalOrchestratorRegistryPath, externalOrchestratorStateMailboxInvoked, externalOrchestratorTransitionEvidence (+3) | no | no |
| 7 | external_orchestrator_registry_test.go | TestExternalOrchestratorRegistryStaleDeadRecordsRemainNonAuthoritative | advanceExternalOrchestratorToRegistered, beginExternalOrchestratorRegistration, externalOrchestratorRegistryPath, externalOrchestratorStateDead, externalOrchestratorStateStale, externalOrchestratorTransitionEvidence (+3) | no | no |
| 8 | effort_test.go | TestTeamMemberAddEffortDryRunJSONAndLiveParity | runTeamMember | no | no |
| 8 | effort_test.go | TestTeamMemberAddEffortKnownAutomaticAndAtomicFailure | runTeamMember | no | no |
| 8 | pane_cleanup_dead_pane_689_test.go | TestTeamMemberRmRemovesDespitePartialPaneCleanup | runTeamMember, teamMemberStop, teamMembers | no | no |
| 8 | pane_cleanup_dead_pane_689_test.go | TestTeamMemberRmHardStopRefusalStillPreservesRoster | runTeamMember, teamMemberStop, teamMembers | no | no |
| 8 | team_lead_test.go | TestTeamLeadSetShowClear | runTeamLead, teamLeadData | no | no |
| 8 | team_lead_test.go | TestTeamLeadSetPlannerLeadMode | runTeamLead, teamLeadData | no | no |
| 8 | team_lead_test.go | TestTeamLeadSetPreservesPlannerLeadModeWhenFlagOmitted | runTeamLead | no | no |
| 8 | team_lead_test.go | TestTeamLeadClearRemovesStaleLeadMode | runTeamLead | no | no |
| 8 | team_lead_test.go | TestLeadRegisterWritesExternalRecordAndSetsLead | leadWakeOptions, leadWakeResult, leadWakeStarter, runLead | no | no |
| 8 | team_lead_test.go | TestLeadRegisterRejectsGlobalPaneAsProjectLead | leadWakeOptions, leadWakeResult, leadWakeStarter, runLead | no | no |
| 8 | team_lead_test.go | TestLeadRegisterProjectLeadNoWakeRequiresCompatReason | runLead | no | no |
| 8 | team_lead_test.go | TestLeadRegisterGlobalOrchestratorNoWakeAllowed | leadWakeOptions, leadWakeResult, leadWakeStarter, runLead | no | no |
| 8 | team_lead_test.go | TestLeadRegisterStartsDrainInjectingWakeSidecar | leadWakeOptions, leadWakeResult, leadWakeStarter, runLead | no | no |
| 8 | team_lead_test.go | TestWakeDrainInjectPinsRootWithoutOverridingPaneIdentity | pins wakeDrainInject, whose production callers (lead register, goal orchestrator) are deleted; the function is now a test fixture | no | no |
| 8 | team_lead_test.go | TestLeadRegisterReapplyDrainInjectOnReRegister | leadWakeOptions, leadWakeResult, leadWakeStarter, runLead | no | no |
| 8 | team_lead_test.go | TestLeadRegisterWriteFailurePreservesTeamLeadConfig | externalLeadWakeProcessGroupSignal, leadWakeOptions, leadWakeResult, leadWakeStarter, runLead | no | no |
| 8 | team_lead_test.go | TestLeadRegisterPostWriteRollbackCASGatesWakeOwnership | externalLeadAfterRecordWrite, externalLeadWakeProcessGroupSignal, leadWakeOptions, leadWakeResult, leadWakeStarter, runLead | no | no |
| 8 | team_lead_test.go | TestLeadRegisterNoWakeSkipsWakeStarter | leadWakeOptions, leadWakeResult, leadWakeStarter, runLead | no | no |
| 8 | team_lead_test.go | TestLeadRegisterExplicitWakeStartsWakeStarter | leadWakeOptions, leadWakeResult, leadWakeStarter, runLead | no | no |
| 8 | team_lead_test.go | TestLeadRegisterWakeAndNoWakeConflict | runLead | no | no |
| 8 | team_lead_test.go | TestLeadRegisterWakeFailureCanBeNonRequired | leadWakeOptions, leadWakeResult, leadWakeStarter, runLead | no | no |
| 8 | team_lead_test.go | TestLeadRegisterWakeInjectModeNoneIsZeroInput | leadWakeOptions, leadWakeResult, leadWakeStarter, runLead, setupFakeExternalWakeBinder | no | no |
| 8 | team_lead_test.go | TestLeadRegisterDefaultsManagedBinaryWakeModeToRaw | leadWakeOptions, leadWakeResult, leadWakeStarter, runLead, setupFakeExternalWakeBinder | no | no |
| 8 | team_lead_test.go | TestResolveExternalWakeInjectConfigMigratesLegacyManagedModesAndPreservesOverrides | resolveExternalWakeInjectConfig, wakeInjectConfig | no | no |
| 8 | team_lead_test.go | TestResolveExternalWakeInjectConfigMigratesInheritedLegacyInjector | resolveExternalWakeInjectConfig, wakeInjectConfig, wakeRetryUntilDrained, wakeRetryUntilInjected | no | no |
| 8 | team_lead_test.go | TestResolveExternalWakeInjectConfigMigratesPersistedDrainedOnlyDuringReregistration | resolveExternalWakeInjectConfig, wakeInjectConfig, wakeRetryUntilDrained, wakeRetryUntilInjected | no | no |
| 8 | team_lead_test.go | TestResolveExternalWakeInjectConfigAuditsExplicitSameLegacyInjector | resolveExternalWakeInjectConfig, wakeInjectConfig, wakeRetryUntilDrained, wakeRetryUntilInjected | no | no |
| 8 | team_lead_test.go | TestResolveExternalWakeInjectConfigScopesAndPreservesRetryAudit | resolveExternalWakeInjectConfig, wakeInjectConfig, wakeRetryUntilDrained, wakeRetryUntilInjected | no | no |
| 8 | team_lead_test.go | TestResolveExternalWakeInjectConfigUsesInjectedForNewInjector | resolveExternalWakeInjectConfig, wakeInjectConfig, wakeRetryUntilInjected | no | no |
| 8 | team_lead_test.go | TestStartExternalLeadWakeRequiredTimeoutStopsSpawnedProcess | assertStoppedAfterCleanup, installLeadWakeHelper, leadWakeOptions, startExternalLeadWake | no | no |
| 8 | team_lead_test.go | TestStartExternalLeadWakeDisablesChildUpdateCheck | externalLeadWakeCommand, leadWakeOptions, startExternalLeadWake | no | no |
| 8 | team_lead_test.go | TestStartExternalLeadWakeAlwaysUsesBaselineExisting | externalLeadWakeCommand, leadWakeOptions, startExternalLeadWake | no | no |
| 8 | team_lead_test.go | TestStartExternalLeadWakePassesPersistedRetryPolicy | externalLeadWakeCommand, leadWakeOptions, startExternalLeadWake, wakeRetryUntilInjected | no | no |
| 8 | team_lead_test.go | TestStartExternalLeadWakeUsesCompleteExactRootTuple | externalLeadWakeCommand, leadWakeOptions, startExternalLeadWake | no | no |
| 8 | team_lead_test.go | TestStartExternalLeadWakeNonRequiredTimeoutStopsSpawnedProcessAndReportsNoPID | assertStoppedAfterCleanup, installLeadWakeHelper, leadWakeOptions, startExternalLeadWake | no | no |
| 8 | team_lead_test.go | TestStartExternalLeadWakeRequiredFailureStopsSpawnedProcess | assertStoppedAfterCleanup, installLeadWakeHelper, leadWakeOptions, startExternalLeadWake | no | no |
| 8 | team_lead_test.go | TestStartExternalLeadWakeNonRequiredFailureStopsSpawnedProcessAndReportsNoPID | assertStoppedAfterCleanup, installLeadWakeHelper, leadWakeOptions, startExternalLeadWake | no | no |
| 8 | team_lead_test.go | TestLeadRegisterWakeFailureRequiredErrorsBeforeTeamMutation | leadWakeOptions, leadWakeResult, leadWakeStarter, runLead | no | no |
| 8 | team_lead_test.go | TestWaitForLeadWakeHelperStartedFailsClosed | waitForLeadWakeHelperStarted | no | no |
| 8 | team_lead_test.go | TestWaitExternalLeadWakeProcessGroupGoneResignalsUntilESRCH | externalLeadWakePollInterval, externalLeadWakeProcessGroupSignal, externalLeadWakeStopTimeout, waitExternalLeadWakeProcessGroupGone | no | no |
| 8 | team_lead_test.go | TestWaitExternalLeadWakeProcessGroupGoneTimesOutWhileLive | externalLeadWakePollInterval, externalLeadWakeProcessGroupSignal, externalLeadWakeStopTimeout, waitExternalLeadWakeProcessGroupGone | no | no |
| 8 | team_lead_test.go | TestWaitExternalLeadWakeProcessGroupGoneScopesDarwinEPERMAfterSuccess | externalLeadWakeProcessGroupSignal, waitExternalLeadWakeProcessGroupGone | no | no |
| 8 | team_lead_test.go | TestLeadWakeHelperProcess | appendLeadWakeHelperEvent | no | no |
| 8 | team_lead_test.go | TestStatusTreatsExternalLeadPaneAsLiveAndActionable | - | yes | yes, verbatim (no deleted reference) |
| 8 | team_lead_test.go | TestStatusKeepsActionsAvailableWhenAgentPIDVerifiesButTmuxScanMisses | - | yes | yes, verbatim (no deleted reference) |
| 8 | team_lead_test.go | TestTeamLaunchDryRunSkipsRegisteredExternalLead | - | yes | yes, verbatim (no deleted reference) |
| 8 | team_lead_test.go | TestTeamLaunchAutoRegistersCurrentPaneExternalLead | - | yes | yes, verbatim (no deleted reference) |
| 8 | team_lead_test.go | TestTeamLaunchDoesNotAutoRegisterProjectLeadWithoutAMRoot | - | yes | yes, verbatim (no deleted reference) |
| 8 | team_member_control_continue_test.go | TestRunTeamMemberControlContinueHoldsAdmissionAndDoubleVerifies | productionTmuxControlContinueDeps, runTeamMember, tmuxControlContinueDeps | no | no |
| 8 | team_member_diff_616_test.go | TestMemberUpdateDryRunShowsFieldLevelDiff | runTeamMember, seedDiffTeam | no | no |
| 8 | team_member_diff_616_test.go | TestMemberUpdateDryRunDiffReportsEffortAsWrittenArgs | runTeamMember, seedDiffTeam | no | no |
| 8 | team_member_diff_616_test.go | TestMemberUpdateDryRunDiffIsNotEmptyForEveryEdit | runTeamMember, seedDiffTeam | no | no |
| 8 | team_member_diff_616_test.go | TestMemberUpdateDryRunNoOpSaysSo | runTeamMember, seedDiffTeam | no | no |
| 8 | team_member_diff_616_test.go | TestMemberUpdateDryRunStillWritesNothing | runTeamMember, seedDiffTeam, teamMembers | no | no |
| 8 | team_member_diff_616_test.go | TestMemberUpdateDryRunJSONCarriesChanges | runTeamMember, seedDiffTeam | no | no |
| 8 | team_member_diff_616_test.go | TestMemberUpdateAppliedRunHasNoChangesKey | runTeamMember, seedDiffTeam | no | no |
| 8 | team_member_diff_616_test.go | TestMemberFieldChangesUnsetRendering | memberFieldChanges, memberFieldUnset | no | no |
| 8 | team_member_diff_616_test.go | TestMemberArgsDiffPreservesArgvBoundaries | memberFieldChanges | no | no |
| 8 | team_member_diff_616_test.go | TestMemberArgsDiffStillReportsNoChangeForIdenticalArgv | memberFieldChanges | no | no |
| 8 | team_member_diff_616_test.go | TestMemberUpdateDryRunEndToEndArgvBoundaries | runTeamMember | no | no |
| 8 | team_member_diff_616_test.go | TestMemberUpdateDryRunJSONDistinguishesArgvBoundaries | runTeamMember | no | no |
| 8 | team_member_diff_616_test.go | TestMemberArgsDiffHandlesEmptyTokens | memberFieldChanges | no | no |
| 8 | team_member_diff_616_test.go | TestMemberUpdateDryRunUnsetSentinelIsInjective | memberFieldUnset, runTeamMember | no | no |
| 8 | team_member_diff_616_test.go | TestMemberUpdateDryRunJSONUnsetSentinelIsInjective | memberFieldUnset, runTeamMember | no | no |
| 8 | team_member_test.go | TestWithProfileLockUnderLockWriteDoesNotSelfDeadlock | teamMembers | no | no |
| 8 | team_member_test.go | TestTeamMemberAddAppendsAndPersists | runTeamMember, teamMembers | no | no |
| 8 | team_member_test.go | TestTeamMemberMutationJSONEnvelopes | runTeamMember | no | no |
| 8 | team_member_test.go | TestTeamMemberAddRecordsSpawnDepthAndRejectsChildSpawn | runTeamMember, teamMembers | no | no |
| 8 | team_member_test.go | TestTeamMemberAddNormalizesCaseAndPrintsFaithfulHint | runTeamMember, teamMembers | no | no |
| 8 | team_member_test.go | TestTeamMemberAddRequiresValidBinary | runTeamMember | no | no |
| 8 | team_member_test.go | TestTeamMemberAddLaunchDryRunDoesNotPersist | runTeamMember, teamMembers | no | no |
| 8 | team_member_test.go | TestTeamMemberAddLaunchRunsResumeAfterPersist | runTeamMember, teamMemberLaunch, teamMembers | no | no |
| 8 | team_member_test.go | TestTeamMemberAddRejectsDuplicateRoleAndHandle | runTeamMember, teamMembers | no | no |
| 8 | team_member_test.go | TestTeamMemberAddBinaryMismatchedArgsRejected | runTeamMember, teamMembers | no | no |
| 8 | team_member_test.go | TestTeamMemberRmRemovesAndPersists | runTeamMember, teamMembers | no | no |
| 8 | team_member_test.go | TestTeamMemberRmAbsentRoleIsIdempotentNoOp | runTeamMember | no | no |
| 8 | team_member_test.go | TestTeamMemberRmStopDryRunDoesNotPersist | runTeamMember, teamMembers | no | no |
| 8 | team_member_test.go | TestTeamMemberRmStopRunsBeforeRemove | runTeamMember, teamMemberStop, teamMembers | no | no |
| 8 | team_member_test.go | TestTeamMemberRmRefusesOrchestrationLead | runTeamMember, teamMembers | no | no |
| 8 | team_member_test.go | TestTeamMemberListShowsRoster | runTeamMember | no | no |
| 8 | team_member_test.go | TestTeamMemberListFlatTeamAndEmptyAndAlias | runTeamMember | no | no |
| 8 | team_member_test.go | TestTeamMemberListEmptyRoster | runTeamMember | no | no |
| 8 | team_member_test.go | TestTeamMemberRequiresExistingTeam | runTeamMember | no | no |
| 8 | team_member_test.go | TestTeamMemberRequiresSubcommandAndRole | runTeamMember | no | no |
| 8 | team_member_update_test.go | TestTeamMemberUpdateChangesOnlyPassedFields | runTeamMember, teamMembers | no | no |
| 8 | team_member_update_test.go | TestTeamMemberUpdateRejectsConcurrentProfileChangeWithoutOverwritingIt | runTeamMember, teamMemberBeforeRosterMutation, teamMembers | no | no |
| 8 | team_member_update_test.go | TestTeamMemberUpdateSession | runTeamMember, teamMembers | no | no |
| 8 | team_member_update_test.go | TestTeamMemberUpdateNoSessionPinClearsSession | runTeamMember, teamMembers | no | no |
| 8 | team_member_update_test.go | TestTeamMemberUpdateRejectsSessionAndNoSessionPinTogether | runTeamMember | no | no |
| 8 | team_member_update_test.go | TestTeamMemberUpdateRequiresAtLeastOneChange | runTeamMember | no | no |
| 8 | team_member_update_test.go | TestTeamMemberUpdateUnknownRoleFails | runTeamMember | no | no |
| 8 | team_member_update_test.go | TestTeamMemberUpdateRejectsCrossBinaryArgs | runTeamMember | no | no |
| 8 | team_member_update_test.go | TestTeamMemberUpdateRejectsInvalidBinary | runTeamMember | no | no |
| 8 | team_member_update_test.go | TestTeamMemberUpdateWorksOnOrchestrationLead | runTeamMember, teamMembers | no | no |
| 8 | team_member_update_test.go | TestTeamMemberUpdateJSONEnvelope | runTeamMember | no | no |
| 8 | team_member_update_test.go | TestTeamMemberUpdateDryRunDoesNotMutate | runTeamMember, teamMembers | no | no |
| 8 | team_rm_test.go | TestExecuteTeamRemoveDryRunKeepsProfile | executeTeamRemove, teamRemoveExecution, teamRemoveFixture | no | no |
| 8 | team_rm_test.go | TestExecuteTeamRemoveDeclinedKeepsProfile | executeTeamRemove, teamRemoveExecution, teamRemoveFixture | no | no |
| 8 | team_rm_test.go | TestExecuteTeamRemoveYesDeletesOnlySelectedProfile | executeTeamRemove, teamRemoveExecution, teamRemoveFixture | no | no |
| 8 | team_rm_test.go | TestExecuteTeamRemoveAllProfilesRequiresProjectNameAndKeepsNonProfileFiles | executeTeamRemove, teamRemoveExecution, teamRemoveFixture | no | no |
| 8 | team_rm_test.go | TestRunTeamRemoveAcceptsPositionalProfile | runTeamRemove, teamRemoveFixture | no | no |
| 8 | team_shared_cwd_test.go | TestTeamInitAcceptsSharedCwdExceptionFlag | - | yes | yes, verbatim (no deleted reference) |
| 8 | team_shared_cwd_test.go | TestTeamSharedCwdExceptionSetShowClear | drives the deleted `team shared-cwd-exception` subcommand | no | no |
| 8 | team_shared_cwd_test.go | TestTeamSharedCwdExceptionSetRequiresReason | drives the deleted `team shared-cwd-exception` subcommand | no | no |
| 8 | team_test.go | TestRunTeamAutonomousPauseAndDisable | runTeamAutonomous | no | no |
| 8 | team_test.go | TestRunTeamAutonomousSerializesProfileReadModifyWrite | runTeamAutonomous, teamAutonomousAfterRead | no | no |
| 9 | namespace_migration_test.go | TestNamespaceMigrationPreviewIsReadOnlyAndStable | drives the deleted migration planner (fx.plan, planNamespaceMigration) | no | no |
| 9 | namespace_migration_test.go | TestNamespaceMigrationCommitAndReverseRollbackPreservePayloads | executeNamespaceMigration, rollbackNamespaceMigration | no | no |
| 9 | namespace_migration_test.go | TestNamespaceMigrationCrashAfterFirstPublishRecoversSource | executeNamespaceMigration, namespaceMigrationFailpoint, recoverNamespaceMigration | no | no |
| 9 | namespace_migration_test.go | TestNamespaceMigrationCrashRecoveryEveryJournalPhase | executeNamespaceMigration, namespaceMigrationFailpoint, recoverNamespaceMigration | no | no |
| 9 | namespace_migration_test.go | TestNamespaceMigrationLaunchBaseRootDirectionMatrix | executeNamespaceMigration | no | no |
| 9 | namespace_migration_test.go | TestNamespaceMigrationPlannerExcludesSelfOwnedProfileLocks | acquireNamespaceMigrationLocks, namespaceMigrationPlannerOptions, planNamespaceMigration | no | no |
| 9 | namespace_migration_test.go | TestNamespaceMigrationBackupDigestCheckpointPrecedesPublication | ensureMigrationPathDigest, executeNamespaceMigration, migrationPlanArtifact, namespaceMigrationFailpoint | no | no |
| 9 | namespace_migration_test.go | TestNamespaceMigrationReservationFreezesPreAdmittedWriterBeforeLockedReplan | executeNamespaceMigration, namespaceMigrationBeforeAdmission, namespaceMigrationBeforeReservation, namespaceMigrationResult | no | no |
| 9 | namespace_migration_test.go | TestNamespaceMigrationHoldsWriterAdmissionsThroughCommittedManifest | executeNamespaceMigration, namespaceMigrationFailpoint, namespaceMigrationResult | no | no |
| 9 | namespace_migration_test.go | TestNamespaceRecoveryHoldsWriterAdmissionsThroughCommittedManifest | executeNamespaceMigration, namespaceMigrationFailpoint, namespaceMigrationResult, recoverNamespaceMigration | no | no |
| 9 | namespace_migration_test.go | TestNamespaceMigrationLockRootSymlinkRefusedBeforeCreation | acquireNamespaceMigrationLocks | no | no |
| 9 | namespace_migration_test.go | TestNamespaceWriterResolvedBeforeMigrationCommitRefusesWithoutExternalAMQ | executeNamespaceMigration | no | no |
| 9 | namespace_migration_test.go | TestNamespaceWriterResolvedBeforeForwardRecoveryCommitRefusesWithoutSourceRecreation | executeNamespaceMigration, namespaceMigrationFailpoint, recoverNamespaceMigration | no | no |
| 9 | namespace_migration_test.go | TestNamespaceMigrationLockedReplanSeesPostReservationLockAndAborts | executeNamespaceMigration, namespaceMigrationFailpoint | no | no |
| 9 | namespace_migration_test.go | TestNamespaceMigrationSourceRecreationBeforeCommitForcesRecovery | executeNamespaceMigration, namespaceMigrationFailpoint, recoverNamespaceMigration | no | no |
| 9 | namespace_migration_test.go | TestNamespaceMigrationNonterminalJournalFreezesBothEndpoints | writeNamespaceMigrationJournal | no | no |
| 9 | namespace_migration_test.go | TestNamespaceMigrationFreezesDirectAgentUpBeforeLaunchMutation | writeNamespaceMigrationJournal | no | no |
| 9 | namespace_migration_test.go | TestNamespaceMigrationFreezesPreviouslyUnguardedWriters | writeNamespaceMigrationJournal | no | no |
| 10 | global_status_test.go | TestDoctorGlobalNOCRegistrationProjection | doctorCheckGlobalNOCRegistrationAtRoot | no | no |
| 10 | operator_handoff_card_493_test.go | TestOperatorHandoffCardReportsConfiguredSinks | handoffCardTeam, newOperatorHandoffCard, operatorEnabled, writeOperatorHandoffCard | no | no |
| 10 | operator_handoff_card_493_test.go | TestOperatorHandoffCardConsolePerInteractionMode | handoffCardTeam, newOperatorHandoffCard, operatorEnabled | no | no |
| 10 | operator_handoff_card_493_test.go | TestOperatorHandoffCardLeadPaneWithoutLeadRole | handoffCardTeam, newOperatorHandoffCard, operatorEnabled | no | no |
| 10 | operator_handoff_card_493_test.go | TestOperatorHandoffCardWhenOperatorGatesDisabled | handoffCardTeam, newOperatorHandoffCard, writeOperatorHandoffCard | no | no |
| 10 | operator_handoff_card_493_test.go | TestOperatorHandoffScopeCarriesProjectProfileSession | operatorHandoffScope | no | no |
| 10 | operator_handoff_card_493_test.go | TestOperatorHandoffCommandsAreShellSafe | handoffCardTeam, newOperatorHandoffCard, operatorEnabled | no | no |
| 10 | operator_handoff_card_493_test.go | TestRenderedCardHasNoUsageNotation | handoffCardTeam, newOperatorHandoffCard, operatorEnabled, writeOperatorHandoffCard | no | no |
| 12 | operator_control_test.go | TestOperatorSendPreviewIsReadOnly | drives the deleted `operator send` subcommand (ruling 8) | no | no |
| 12 | operator_control_test.go | TestOperatorSendYesIsAcceptedAndThreadVisibleWithoutReceipt | drives the deleted `operator send` subcommand (ruling 8) | no | no |
| 12 | operator_control_test.go | TestOperatorSendRejectsUnknownOrOperatorTargetBeforeAMQ | drives the deleted `operator send` subcommand (ruling 8) | no | no |
| 12 | operator_control_test.go | TestOperatorSendCannotBypassGateAnswerAuthority | drives the deleted `operator send` subcommand (ruling 8) | no | no |
| 12 | operator_control_test.go | TestBroadcastPreviewAndConfirmedTransportAcceptance | runBroadcast | no | no |
| 12 | operator_control_test.go | TestBroadcastRecipientSetIsDeterministic | operatorBroadcastRecipients | no | no |
| 13 | claude_rename_test.go | TestClaudeSessionRenameNameUsesRoleAndSession | claudeSessionRenameName | no | no |
| 13 | claude_rename_test.go | TestMaybeScheduleClaudeSessionRenameStartsDetachedHelper | claudeRenameHelperCommand, claudeRenameHelperExecutable, claudeRenameHelperStart, defaultClaudeRenameDelay, maybeScheduleClaudeSessionRename | no | no |
| 13 | claude_rename_test.go | TestMaybeScheduleClaudeSessionRenameFallsBackToCWDControlRoot | claudeRenameHelperExecutable, claudeRenameHelperStart, maybeScheduleClaudeSessionRename | no | no |
| 13 | claude_rename_test.go | TestMaybeScheduleClaudeSessionRenameSkipsNoneMode | claudeRenameHelperExecutable, claudeRenameHelperStart, maybeScheduleClaudeSessionRename | no | no |
| 13 | claude_rename_test.go | TestMaybeScheduleClaudeSessionRenameSkipsCodexAndMissingPane | claudeRenameHelperStart, maybeScheduleClaudeSessionRename | no | no |
| 13 | claude_rename_test.go | TestRunClaudeSessionRenameDeliversSingleLineSlashCommand | runClaudeSessionRename | no | no |
| 13 | claude_rename_test.go | TestRunClaudeSessionRenameTreatsDeadPaneAsBenignNoOp | runClaudeSessionRename | no | no |
| 13 | claude_rename_test.go | TestRunClaudeSessionRenamePropagatesOtherSendErrors | runClaudeSessionRename | no | no |
| 13 | claude_rename_test.go | TestClaudeRenameHelperIsDispatchableButHiddenFromHelp | claudeRenameHelperCommand | no | no |
| 13 | global_status_test.go | TestRunGlobalStatusRoutesAndKeepsDegradationNonFatal | runGlobal | no | no |
| 13 | global_status_test.go | TestRunGlobalStatusHumanOutputNamesVisibleDegradation | runGlobal | no | no |
| 13 | launch_identity_stamp_test.go | TestEveryMismatchWrapperGuardsTheIncompleteSentinel | AST guard whose every policed site was in the deleted team_member_control_continue.go (vacuous) | no | no |
| 13 | layout_finalize_test.go | TestPrepareLayoutFinalizationFailsClosedBeforeSpawn | launcherPaneCloseAfterStart, layoutPresetLeadLeft, prepareLayoutFinalization, runStartLayoutSelection | no | no |
| 13 | layout_finalize_test.go | TestBuildLayoutFinalizationPlanUsesConfiguredLeadResult | buildLayoutFinalizationPlan, layoutFinalizationContext, layoutPresetLeadLeft, resolveRunStartLayout, runStartLayoutInput | no | no |
| 13 | layout_finalize_test.go | TestBuildLayoutFinalizationPlanRejectsEveryNonExactRuntimeID | buildLayoutFinalizationPlan, launcherPaneKeep, layoutFinalizationContext, layoutPresetLeadLeft, runStartLayoutSelection | no | no |
| 13 | layout_finalize_test.go | TestLayoutFinalizationScriptExactIDsBoundedAndRenameAgnostic | launcherPaneCloseAfterStart, layoutFinalizationPlan, layoutFinalizationScript, layoutPresetLeadLeft, runStartLayoutSelection | no | no |
| 13 | layout_finalize_test.go | TestLayoutFinalizationScriptVerifiesLeadTopGeometryAfterReflow | launcherPaneCloseAfterStart, layoutFinalizationPlan, layoutFinalizationScript, layoutPresetLeadTop, runStartLayoutSelection | no | no |
| 13 | layout_finalize_test.go | TestLayoutFinalizationLauncherSafetyAndWindowPreset | launcherPaneCloseAfterStart, layoutFinalizationPlan, layoutFinalizationScript, layoutPresetOneWindowPerAgent, runStartLayoutSelection | no | no |
| 13 | layout_finalize_test.go | TestExternalLeadFinalizationUsesCapturedPaneAndForcesKeep | buildLayoutFinalizationPlan, launcherPaneKeep, layoutFinalizationContext, layoutFinalizationScript, layoutPresetEvenGrid, resolveRunStartLayout (+1) | no | no |
| 13 | layout_finalize_test.go | TestScheduleLayoutFinalizationPersistsWarningWithoutTeardown | launcherPaneKeep, layoutFinalizationPlan, layoutFinalizationScheduler, layoutPresetEvenGrid, runStartLayoutSelection, scheduleLayoutFinalization | no | no |
| 13 | layout_finalize_test.go | TestScheduleLayoutFinalizationUsesBackgroundRunShell | ensure, launcherPaneCloseAfterStart, layoutFinalizationPlan, layoutFinalizationRunCommand, layoutPresetLeadLeft, runShellLogPath (+2) | no | no |
| 13 | layout_finalize_test.go | TestPrintLayoutFinalizationDryRunShowsBoundedBackgroundExactIDFlow | launcherPaneCloseAfterStart, layoutPresetLeadLeft, printLayoutFinalizationDryRun, runStartLayoutSelection | no | no |
| 13 | layout_preset_test.go | TestRunStartLayoutPresetMappings | launcherPaneCloseAfterStart, layoutPresetEvenGrid, layoutPresetLeadLeft, layoutPresetLeadTop, layoutPresetOneWindowPerAgent, resolveRunStartLayout (+1) | no | no |
| 13 | layout_preset_test.go | TestRunStartLayoutContradictionAndDefaultMatrix | launcherPaneCloseAfterStart, launcherPaneKeep, layoutPresetLeadLeft, resolveRunStartLayout, runStartLayoutInput | no | no |
| 13 | orphan_panes_test.go | TestFindOrphanPanesExcludesLiveLaunchRecordsAndFiltersSession | findOrphanPanes, launchPaneKey, orphanPane | no | no |
| 13 | orphan_panes_test.go | TestLiveLaunchPaneTokensReadsCurrentRecords | launchPaneKey, liveLaunchPaneTokens | no | no |
| 13 | run_shell_silence_test.go | TestSilentRunShellPayloadSuppressesOutputAndForcesZeroExit | runShellLogPath, silentRunShellPayload | no | no |
| 13 | run_shell_silence_test.go | TestSilentRunShellPayloadAppendsAcrossInvocationsNeverTruncates | runShellLogPath, silentRunShellPayload | no | no |
| 13 | run_shell_silence_test.go | TestRunShellLogPathIsUnderControlRootAmqSquadDir | runShellLogPath | no | no |
| 13 | run_shell_silence_test.go | TestRunShellScheduledPayloadsAgainstBogusPaneNeverEnterViewMode | claudeRenameHelperCommand, layoutFinalizationPlan, layoutFinalizationScript, runShellLogPath, silentRunShellPayload | no | no |
| 13 | team_launch_iterm2_test.go | TestITerm2LaunchArgvShape | iterm2LaunchArgv | no | no |
| 13 | team_launch_iterm2_test.go | TestITerm2LaunchPayloadQuotesHostileValues | iterm2LaunchArgv | no | no |
| 13 | team_launch_iterm2_test.go | TestITerm2DryRunLines | iterm2DryRunLines, iterm2LaunchPlan | no | no |
| 13 | team_launch_iterm2_test.go | TestITerm2BackendRegistered | asserts the deleted iterm2 backend registration | no | no |
| 13 | team_launch_pane_verify_test.go | TestTmuxSessionAttributesTheCreatedPaneOrRefuses | runTmuxSessionLaunchPlan, tmuxSessionLaunchPlan, tmuxSessionLookPath, tmuxSessionRunCommand | no | no |
| 13 | team_launch_terminal_test.go | TestTerminalAppLaunchArgvShape | terminalAppLaunchArgv | no | no |
| 13 | team_launch_terminal_test.go | TestTerminalAppLaunchPayloadQuotesHostileValues | terminalAppLaunchArgv | no | no |
| 13 | team_launch_terminal_test.go | TestTerminalAppDryRunLines | terminalAppDryRunLines, terminalAppLaunchPlan | no | no |
| 13 | team_launch_terminal_test.go | TestTerminalAppBackendRegistered | asserts the deleted terminal backend registration | no | no |
| 13 | team_launch_tmux_result_test.go | TestRunTmuxCurrentWindowMapsExactResultToConfiguredNonFirstLead | buildLayoutFinalizationPlan, launcherPaneKeep, layoutFinalizationContext, layoutPresetLeadLeft, runStartLayoutSelection | no | no |
| 13 | team_launch_tmux_result_test.go | TestRunTmuxOneWindowMapsExactResultToConfiguredNonFirstLead | buildLayoutFinalizationPlan, launcherPaneCloseAfterStart, layoutFinalizationContext, layoutPresetOneWindowPerAgent, runStartLayoutSelection | no | no |
| 13 | team_launch_tmux_session_test.go | TestTmuxSessionDryRunLines_WindowPerAgent | tmuxSessionDryRunLines, tmuxSessionLaunchPlan | no | no |
| 13 | team_launch_tmux_session_test.go | TestTmuxSessionCreateArgv_Shape | tmuxSessionCreateArgv | no | no |
| 13 | team_launch_tmux_session_test.go | TestTmuxSessionRenameArgv_StampsTitleToken | tmuxSessionRenameArgv | no | no |
| 13 | team_launch_tmux_session_test.go | TestTmuxSessionBuildPlanUsesSharedWorkstreamAndOrder | tmuxSessionTeamLaunchBackend | no | no |
| 13 | team_launch_tmux_session_test.go | TestTmuxSessionValidateMissingBinary | tmuxSessionTeamLaunchBackend, withStubTmuxSessionBinary | no | no |
| 13 | team_launch_tmux_session_test.go | TestTmuxSessionValidatePresentBinary | tmuxSessionTeamLaunchBackend, withStubTmuxSessionBinary | no | no |
| 13 | team_launch_tmux_session_test.go | TestTmuxSessionValidateRejectsNegativeStagger | tmuxSessionTeamLaunchBackend, withStubTmuxSessionBinary | no | no |
| 13 | team_launch_tmux_session_test.go | TestTmuxSessionBackendRegistered | asserts the deleted tmux-session backend registration | no | no |
| 13 | team_launch_typed_delivery_test.go | TestEveryTypedDeliverySiteIsJustified | checkNOCDispatchLineBound | no | no |
| 13 | team_launch_typed_delivery_test.go | TestNOCDispatchLineDoesNotScaleWithBootstrapSize | nocDispatchCommand, nocDispatchLineBound | no | no |
| 13 | team_member_control_continue_test.go | TestContinueExactTmuxControlClientRejectsUnsafeSelectorAndDocumentsVersionFloor | continueExactTmuxControlClient, tmuxControlContinueTarget | no | no |
| 13 | team_member_control_continue_test.go | TestResolveExactTmuxControlContinueFailsClosed | resolveExactTmuxControlContinue, tmuxControlContinueDeps | no | no |
| 13 | team_member_control_continue_test.go | TestContinueExactTmuxControlClientUsesOnlyExactArgv | continueExactTmuxControlClient, tmuxControlContinueTarget | no | no |
| 13 | team_member_control_continue_tmux_test.go | TestPrivateTmuxExactPausedStateContinuesOnlyExactVerifiedPane | continueExactTmuxControlClient, pathWithinTestRoot, resolveExactTmuxControlContinue, synchronizedTestBuffer, tmuxControlContinueDeps, waitProtocolLine | no | no |
| 14 | json_envelopes_test.go | TestRunTeamProfilesJSONEnvelope | runTeamProfiles, teamProfilesEnvelopeData | no | no |
| 14 | json_envelopes_test.go | TestRunTeamProfilesJSONLegacySchema2AdvertisesImplicitOperatorGates | runTeamProfiles, teamProfilesEnvelopeData | no | no |
| 14 | json_envelopes_test.go | TestRunTeamProfilesJSONStdoutValidWhenDefaultUnreadable | runTeamProfiles, teamProfilesEnvelopeData | no | no |
| 14 | json_envelopes_test.go | TestRunTeamProfilesJSONStdoutValidWhenProfileUnreadable | runTeamProfiles, teamProfilesEnvelopeData | no | no |
| 14 | profiles_test.go | TestRunTeamProfilesListsDefaultFirstThenNamedSorted | runTeamProfiles | no | no |
| 14 | profiles_test.go | TestRunTeamProfilesInfersSharedMemberSession | runTeamProfiles | no | no |
| 14 | profiles_test.go | TestRunTeamProfilesProjectTargetsOtherDir | runTeamProfiles | no | no |
| 14 | profiles_test.go | TestRunTeamProfilesEmptyDirSilent | runTeamProfiles | no | no |
| 15 | collect_lock_exclusive_test.go | TestLockCollectJournalExclusiveContextBoundsStaleSentinel | collectJournalFilePerm, lockCollectJournalExclusiveContext | no | no |
| 15 | collect_support_test.go | TestExecuteCollectNonEmptyDrainSkipsWaitPostureGuard | collectTestContext, executeCollectWithWaitPosture, seedCollectMessage, withCollectAMQSeams | no | no |
| 15 | collect_support_test.go | TestExecuteCollectEmptyDrainGuardsImmediatelyBeforeWatch | collectTestContext, executeCollectWithWaitPosture, withCollectAMQSeamsFunc | no | no |
| 15 | collect_support_test.go | TestCollectReplaysJournaledMessageAfterInterruptedBeforeAck | collectTestContext, deliveredPath, executeCollect, moveCollectMessageToCur, newCollectJournal, pendingPath (+2) | no | no |
| 15 | collect_support_test.go | TestCollectReplaysJournaledMessageAfterInterruptedBetweenAckAndOutput | collectTestContext, errorWriter, executeCollect, moveCollectMessageToCur, newCollectJournal, pendingPath (+2) | no | no |
| 15 | collect_support_test.go | TestCollectJournalScopesByProfileSessionAndRecipient | collectTestContext, newCollectJournal | no | no |
| 15 | collect_support_test.go | TestCollectJournalCleansOldDeliveredEntries | cleanupDelivered, collectJournalEntry, collectTestContext, deliveredPath, ensure, newCollectJournal (+1) | no | no |
| 16 | completion_test.go | TestRunCompletionRejectsMissingShell | runCompletion | no | no |
| 16 | completion_test.go | TestCompletionIncludesGlobalStatus | bashCompletionScript, fishCompletionScript, zshCompletionScript | no | no |
| 16 | completion_test.go | TestRunCompletionRejectsExtraArgs | runCompletion | no | no |
| 16 | completion_test.go | TestRunCompletionRejectsUnsupportedShell | runCompletion | no | no |
| 16 | completion_test.go | TestRunCompletionBashContainsRepresentativeTokens | runCompletion | no | no |
| 16 | completion_test.go | TestRunCompletionZshContainsRepresentativeTokens | runCompletion | no | no |
| 16 | completion_test.go | TestRunCompletionFishContainsRepresentativeTokens | runCompletion | no | no |
| 16 | completion_test.go | TestCompletionFlagsCoverDispatcher | completionCommonFlags | no | no |
| 16 | completion_test.go | TestCompletionRootFlagsOfferedAsFirstToken | runCompletion | no | no |
| 16 | completion_test.go | TestCompletionTopCommandsMatchesPublicCatalog | completionTopCommands | partly | moved to cli_test.go, reads commandNames("") |
| 16 | deprecation_test.go | TestCompletionDropsRemovedVerbs | completionTeamSubcommands, completionTopCommands, runCompletion | no | no |
| 16 | output_test.go | TestCompletionContainsGlobalFlags | runCompletion | no | no |
| 17 | control_test.go | TestControlActionPreviewsAreExactFirstClassCommands | whole package `act` deleted | no | no |
| 17 | control_test.go | TestReplyExcludesCustomOperatorHandle | whole package `act` deleted | no | no |
| 17 | control_test.go | TestSendUsesExactPreviewArgvAndReturnsDurableReceipt | whole package `act` deleted | no | no |
| 17 | control_test.go | TestSendFailsClosedBeforeSubprocess | whole package `act` deleted | no | no |
| 17 | control_test.go | TestDecodeControlReceiptRejectsMissingStableEvidence | whole package `act` deleted | no | no |
| 18 | autonomy_test.go | TestSeededDefaultDeniesAutonomousSpawn | whole package `autonomy` deleted | no | no |
| 18 | autonomy_test.go | TestAutonomousSpawnWithinPolicy | whole package `autonomy` deleted | no | no |
| 18 | autonomy_test.go | TestAutonomousSpawnDeniedByMaxAgentRoleAndBudget | whole package `autonomy` deleted | no | no |
| 18 | autonomy_test.go | TestAutonomousDeniesChildAuthorityAndPauseDisable | whole package `autonomy` deleted | no | no |
| 18 | autonomy_test.go | TestAutonomousPruneWithinPolicy | whole package `autonomy` deleted | no | no |
| 18 | autonomy_test.go | TestAutonomousPruneEnforcesIdleReapAndTaskLinkage | whole package `autonomy` deleted | no | no |
| 18 | autonomy_test.go | TestAuthorizeSpawnPersistsCountersAndAuditBeforeAllowedReturn | whole package `autonomy` deleted | no | no |
| 18 | autonomy_test.go | TestAuthorizeSpawnProfileLockDoesNotSelfDeadlock | whole package `autonomy` deleted | no | no |
| 18 | autonomy_test.go | TestAuthorizePrunePersistsBudgetCounterIdleEvidenceAndAudit | whole package `autonomy` deleted | no | no |
| 18 | autonomy_test.go | TestAuthorizePruneDeniesMissingTaskLinkageEvidenceWithAudit | whole package `autonomy` deleted | no | no |
| 18 | autonomy_test.go | TestAuthorizeDeniedDecisionWritesAuditWithoutCounters | whole package `autonomy` deleted | no | no |
| 18 | autonomy_test.go | TestAuthorizeInvalidSessionDoesNotReturnAllowedOrMutateCounters | whole package `autonomy` deleted | no | no |
| 18 | autonomy_test.go | TestAppendAuditWritesJSONL | whole package `autonomy` deleted | no | no |
| 19 | bubbletea_clone_test.go | TestBubbleOffersCloneWhenSuggestionMismatchesPinnedSession | whole package `wizard` deleted | no | no |
| 19 | bubbletea_clone_test.go | TestBubbleSingleSessionMatchingSuggestionStaysFrictionless | whole package `wizard` deleted | no | no |
| 19 | bubbletea_clone_test.go | TestBubbleTemplateProfileAcceptsTypedSession | whole package `wizard` deleted | no | no |
| 19 | bubbletea_clone_test.go | TestBubbleProfileChoicesPresentTemplateFraming | whole package `wizard` deleted | no | no |
| 19 | bubbletea_test.go | TestBubbleModelStartsWithProjectDefaultsAndPhaseRail | whole package `wizard` deleted | no | no |
| 19 | bubbletea_test.go | TestBubbleToolPolicyRecommendedDisplaysCatalogMinimumAssignments | whole package `wizard` deleted | no | no |
| 19 | bubbletea_test.go | TestBubbleToolPolicyFullAllIsExplicitAndWarnsInReview | whole package `wizard` deleted | no | no |
| 19 | bubbletea_test.go | TestBubbleSelfOperatorAllowBackDeselectReselect | whole package `wizard` deleted | no | no |
| 19 | bubbletea_test.go | TestBubbleModelExistingProfileOverridesAndExplicitNotificationMismatchArePreserved | whole package `wizard` deleted | no | no |
| 19 | bubbletea_test.go | TestBubbleModelPinnedSessionIsDerivedWithoutFreeText | whole package `wizard` deleted | no | no |
| 19 | bubbletea_test.go | TestWizardAdaptersConsumeSameSuggestedFirstSummary | whole package `wizard` deleted | no | no |
| 19 | bubbletea_test.go | TestBubbleExistingProfileWithoutCLIDiscoveryFailsClosed | whole package `wizard` deleted | no | no |
| 19 | bubbletea_test.go | TestBubbleMultipleKnownSessionsUseListAndBackDropsStaleRun | whole package `wizard` deleted | no | no |
| 19 | bubbletea_test.go | TestBubbleResumeActionScopedControls | whole package `wizard` deleted | no | no |
| 19 | bubbletea_test.go | TestBubbleResumeUsesAgreedSavedPlacementAndShowsPreservedReviewEvidence | whole package `wizard` deleted | no | no |
| 19 | bubbletea_test.go | TestBubbleResumeGoalDefaultsNoAndRendersSafeEvidence | whole package `wizard` deleted | no | no |
| 19 | bubbletea_test.go | TestBubblePhaseRailsAreScopeSpecific | whole package `wizard` deleted | no | no |
| 19 | bubbletea_test.go | TestBubbleProjectPhaseRailNeverRegressesAcrossMultiMemberFlows | whole package `wizard` deleted | no | no |
| 19 | bubbletea_test.go | TestGlobalBranchRunsThroughBothRealAdaptersToIdenticalReview | whole package `wizard` deleted | no | no |
| 19 | bubbletea_test.go | TestGlobalCatalogChoicesMatchAcrossAdapters | whole package `wizard` deleted | no | no |
| 19 | bubbletea_test.go | TestProjectCatalogChoicesMatchAcrossAdapters | whole package `wizard` deleted | no | no |
| 19 | bubbletea_test.go | TestGlobalCustomEffortIsPreservedAndWarnedInBothReviews | whole package `wizard` deleted | no | no |
| 19 | bubbletea_test.go | TestBubbleResumeUsesInjectedCatalogAndPrefillsStoredCustomValues | whole package `wizard` deleted | no | no |
| 19 | bubbletea_test.go | TestBubbleRunningAndBlockedExistingRunsRemainNonExecutable | whole package `wizard` deleted | no | no |
| 19 | bubbletea_test.go | TestNewBubbleModelRestartStartsAtProfileWithFreshContext | whole package `wizard` deleted | no | no |
| 19 | bubbletea_test.go | TestBubbleBackRefreshRejectsChangedDiscoveryAndRetainsB | whole package `wizard` deleted | no | no |
| 19 | bubbletea_test.go | TestBubbleBackRefreshRestoresCompatibleSnapshotAndRetainsFreshA | whole package `wizard` deleted | no | no |
| 19 | bubbletea_test.go | TestBubbleBackRefreshErrorRejectsAndRetainsLastKnownContext | whole package `wizard` deleted | no | no |
| 19 | bubbletea_test.go | TestSnapshotCompatibleFailsClosedOnMissingFingerprints | whole package `wizard` deleted | no | no |
| 19 | bubbletea_test.go | TestBubbleNewProfileUsesProfileAndSessionSuggestions | whole package `wizard` deleted | no | no |
| 19 | bubbletea_test.go | TestBubbleFreshAndNamedOnlyProjectsUseSingleNewProfilePath | whole package `wizard` deleted | no | no |
| 19 | bubbletea_test.go | TestBubbleModelHydratesEnabledAuthoritativeNotifications | whole package `wizard` deleted | no | no |
| 19 | bubbletea_test.go | TestBubbleModelCapabilityRowsAreGatedByInjectedCatalog | whole package `wizard` deleted | no | no |
| 19 | bubbletea_test.go | TestBubbleExistingSelfOperatorShowsAuthoritativePolicy | whole package `wizard` deleted | no | no |
| 19 | bubbletea_test.go | TestBubbleModelDetachedDefaultsSeparateOperatorTerminal | whole package `wizard` deleted | no | no |
| 19 | bubbletea_test.go | TestBubbleModelBackRestoresChoiceCursor | whole package `wizard` deleted | no | no |
| 19 | bubbletea_test.go | TestBubbleModelBackRestoresProjectContext | whole package `wizard` deleted | no | no |
| 19 | bubbletea_test.go | TestBubbleModelBlankExistingModelRemovesEarlierLaunchOverride | whole package `wizard` deleted | no | no |
| 19 | bubbletea_test.go | TestBubbleModelOffersModelsPerBinaryWithCustomEscape | whole package `wizard` deleted | no | no |
| 19 | bubbletea_test.go | TestBubbleModelOffersCurrentClaudeEffortsWithCustomEscape | whole package `wizard` deleted | no | no |
| 19 | bubbletea_test.go | TestBubbleModelEditingRolesPrunesAssignmentsAndInvalidLead | whole package `wizard` deleted | no | no |
| 19 | bubbletea_test.go | TestBubbleModelCancelDoesNotProducePreview | whole package `wizard` deleted | no | no |
| 19 | bubbletea_test.go | TestBubbleModelConfirmationShowsCanonicalReadOnlyBoundary | whole package `wizard` deleted | no | no |
| 19 | bubbletea_test.go | TestTopologyPreviewGolden | whole package `wizard` deleted | no | no |
| 19 | composition_policy_test.go | TestRecommendWorktreeIsolationSingleDevNoRecommendation | whole package `wizard` deleted | no | no |
| 19 | composition_policy_test.go | TestRecommendWorktreeIsolationTwoImplementationDevsRecommends | whole package `wizard` deleted | no | no |
| 19 | composition_policy_test.go | TestRecommendWorktreeIsolationPlannerLeadExcludedFromCount | whole package `wizard` deleted | no | no |
| 19 | composition_policy_test.go | TestRecommendWorktreeIsolationPlannerLeadWithTwoWorkersStillRecommends | whole package `wizard` deleted | no | no |
| 19 | composition_policy_test.go | TestRecommendWorktreeIsolationBuilderLeadCountsAsMutationCapable | whole package `wizard` deleted | no | no |
| 19 | composition_policy_test.go | TestRecommendWorktreeIsolationEmptyRolesIgnored | whole package `wizard` deleted | no | no |
| 19 | default_cursor_coverage_test.go | TestEveryChoiceStagePositionsItsCursor | whole package `wizard` deleted | no | no |
| 19 | default_cursor_coverage_test.go | TestDefaultCursorExemptionsAreNotStale | whole package `wizard` deleted | no | no |
| 19 | discovery_test.go | TestClassifyExistingRunPrecedenceAndRestoreGuard | whole package `wizard` deleted | no | no |
| 19 | discovery_test.go | TestDiscoveryFingerprintCanonicalizesSetsButPreservesRosterOrder | whole package `wizard` deleted | no | no |
| 19 | discovery_test.go | TestDiscoveryFingerprintChangesForEveryDecisionInputClass | whole package `wizard` deleted | no | no |
| 19 | discovery_test.go | TestDiscoveryFingerprintCanonicalizesNilAndEmptyCollections | whole package `wizard` deleted | no | no |
| 19 | global_posture_test.go | TestGlobalPostureArgsAreEmittedPerBinary | whole package `wizard` deleted | no | no |
| 19 | global_posture_test.go | TestTmuxCapablePostureIsListedFirst | whole package `wizard` deleted | no | no |
| 19 | global_posture_test.go | TestEveryPostureStatesItsConsequence | whole package `wizard` deleted | no | no |
| 19 | global_posture_test.go | TestPostureArgsPrecedeOperatorFreeText | whole package `wizard` deleted | no | no |
| 19 | global_posture_test.go | TestUnknownPostureDegradesVisibly | whole package `wizard` deleted | no | no |
| 19 | global_posture_test.go | TestReviewLineStatesTheTmuxConsequence | whole package `wizard` deleted | no | no |
| 19 | global_posture_test.go | TestStoredSaferPostureSurvivesAcceptingDefaultsInBubble | whole package `wizard` deleted | no | no |
| 19 | global_posture_test.go | TestStoredSaferPostureSurvivesAcceptingDefaultsInNumbered | whole package `wizard` deleted | no | no |
| 19 | global_posture_test.go | TestUnknownStoredPostureIsNotCoercedInBubble | whole package `wizard` deleted | no | no |
| 19 | global_posture_test.go | TestUnknownStoredPostureIsNotCoercedInNumbered | whole package `wizard` deleted | no | no |
| 19 | global_posture_test.go | TestStoredPostureSurvivesCaseAndWhitespace | whole package `wizard` deleted | no | no |
| 19 | global_posture_test.go | TestArgsAndReviewAgreeOnTheSameStoredPosture | whole package `wizard` deleted | no | no |
| 19 | goal_binding_test.go | TestResolveGoalBindingExplicit | whole package `wizard` deleted | no | no |
| 19 | goal_binding_test.go | TestResolveGoalBindingDerivesFromAcceptedBriefWithoutRewritingIt | whole package `wizard` deleted | no | no |
| 19 | goal_binding_test.go | TestResolveGoalBindingRejectsBlankOrStubBrief | whole package `wizard` deleted | no | no |
| 19 | goal_binding_test.go | TestResolveGoalBindingBacktrackedExplicitGoalRecomputesSourceAndDigest | whole package `wizard` deleted | no | no |
| 19 | goal_binding_test.go | TestGoalBindingReviewExposesDeliveryValidationStatus | whole package `wizard` deleted | no | no |
| 19 | launch_shape_test.go | TestApplyLaunchShapeSwitchRestoresAuthoredComposition | whole package `wizard` deleted | no | no |
| 19 | launch_shape_test.go | TestBubbleLaunchShapeBackAndReselectRestoresWorkers | whole package `wizard` deleted | no | no |
| 19 | numbered_clone_test.go | TestRunNumberedOffersCloneWhenSuggestionMismatchesPinnedSession | whole package `wizard` deleted | no | no |
| 19 | numbered_clone_test.go | TestRunNumberedSingleSessionMatchingSuggestionStaysFrictionless | whole package `wizard` deleted | no | no |
| 19 | numbered_clone_test.go | TestRunNumberedTemplateProfileAcceptsAnyTypedSession | whole package `wizard` deleted | no | no |
| 19 | numbered_clone_test.go | TestRunNumberedTemplateProfilePresentedAsTemplateInPicker | whole package `wizard` deleted | no | no |
| 19 | numbered_test.go | TestRunNumberedEnterThroughDefaults | whole package `wizard` deleted | no | no |
| 19 | numbered_test.go | TestRunNumberedDetachedDefaultsSeparateOperatorTerminal | whole package `wizard` deleted | no | no |
| 19 | numbered_test.go | TestRunNumberedAcceptsNumberedChoices | whole package `wizard` deleted | no | no |
| 19 | numbered_test.go | TestRunNumberedClaudeEffortCustomEscapePreservesSpelling | whole package `wizard` deleted | no | no |
| 19 | numbered_test.go | TestRunNumberedReusesCallerReaderAndPreservesFollowingConsent | whole package `wizard` deleted | no | no |
| 19 | numbered_test.go | TestRunNumberedExistingProfileKeepsRosterAuthoritative | whole package `wizard` deleted | no | no |
| 19 | numbered_test.go | TestRunNumberedRejectsUnknownChoice | whole package `wizard` deleted | no | no |
| 19 | numbered_test.go | TestRunNumberedListsExistingProfilesAndUsesPinnedSession | whole package `wizard` deleted | no | no |
| 19 | numbered_test.go | TestRunNumberedOffersModelListWithCustomEscape | whole package `wizard` deleted | no | no |
| 19 | numbered_test.go | TestRunNumberedToolPolicyRecommendedDisplaysCatalogMinimumAssignments | whole package `wizard` deleted | no | no |
| 19 | numbered_test.go | TestRunNumberedToolPolicyFullAllIsExplicitAndWarnsInReview | whole package `wizard` deleted | no | no |
| 19 | numbered_test.go | TestRunNumberedDerivesPinnedSessionWithoutPrompt | whole package `wizard` deleted | no | no |
| 19 | numbered_test.go | TestRunNumberedNoSessionProfileUsesSuggestedFirstRun | whole package `wizard` deleted | no | no |
| 19 | numbered_test.go | TestRunNumberedExistingProfileWithoutCLIDiscoveryFailsClosed | whole package `wizard` deleted | no | no |
| 19 | numbered_test.go | TestRunNumberedMultipleSessionsUseKnownRunListAndComposeResume | whole package `wizard` deleted | no | no |
| 19 | numbered_test.go | TestRunNumberedResumePlacementDefaultsAndExplicitOverride | whole package `wizard` deleted | no | no |
| 19 | numbered_test.go | TestRunNumberedResumeActionScopedControls | whole package `wizard` deleted | no | no |
| 19 | numbered_test.go | TestRunNumberedRunningAndBlockedExistingRunsRemainNonExecutable | whole package `wizard` deleted | no | no |
| 19 | numbered_test.go | TestRunNumberedUnknownProfilePrefillDefaultsToCreate | whole package `wizard` deleted | no | no |
| 19 | numbered_test.go | TestPromptOperatorChoiceCapabilityGating | whole package `wizard` deleted | no | no |
| 19 | numbered_test.go | TestRunNumberedExistingProfileCollectsLaunchOnlyOverrides | whole package `wizard` deleted | no | no |
| 19 | profile_flow_test.go | TestProfileFlowClearsIncompatibleAnswers | whole package `wizard` deleted | no | no |
| 19 | profile_flow_test.go | TestExistingSessionChangeClearsDownstreamAndDerivesBackend | whole package `wizard` deleted | no | no |
| 19 | profile_flow_test.go | TestResumePlacementDefaultsUseOnlyUnanimousSavedTargets | whole package `wizard` deleted | no | no |
| 19 | profile_flow_test.go | TestSelectExistingSessionExplicitPlacementPrefillWinsDerivedDefault | whole package `wizard` deleted | no | no |
| 19 | profile_flow_test.go | TestGoalExcerptAndCommandFormsPreserveReviewEvidence | whole package `wizard` deleted | no | no |
| 19 | profile_flow_test.go | TestRunInvalidationClearsGoalBindingEvidence | whole package `wizard` deleted | no | no |
| 19 | profile_flow_test.go | TestSelectedSessionAndSpecCloneDeepCopyResumeMembers | whole package `wizard` deleted | no | no |
| 19 | profile_flow_test.go | TestInvalidateExistingRunClearsPolicyAndAllDownstreamState | whole package `wizard` deleted | no | no |
| 19 | profile_flow_test.go | TestFormatSavedNativeArgsIsBoundedAndTerminalSafe | whole package `wizard` deleted | no | no |
| 19 | profile_flow_test.go | TestNoSessionProfileConsumesCLIDiscoveredSuggestedFirstRun | whole package `wizard` deleted | no | no |
| 19 | profile_flow_test.go | TestProfileSessionsNeverSynthesizesExecutableDiscovery | whole package `wizard` deleted | no | no |
| 19 | routing_test.go | TestWorkClassPoliciesAreCompleteAndConsistent | whole package `wizard` deleted | no | no |
| 19 | routing_test.go | TestRecommendModelEffortWellSpecifiedLeafDefaultsEconomical | whole package `wizard` deleted | no | no |
| 19 | routing_test.go | TestRecommendModelEffortArchitectureDefaultsFrontier | whole package `wizard` deleted | no | no |
| 19 | routing_test.go | TestRecommendModelEffortMechanicalDefaultsFastAndMinimal | whole package `wizard` deleted | no | no |
| 19 | routing_test.go | TestRecommendModelEffortEscalatesWellSpecifiedLeafWhenAmbiguous | whole package `wizard` deleted | no | no |
| 19 | routing_test.go | TestRecommendModelEffortHighBlastRadiusEscalatesEvenMechanical | whole package `wizard` deleted | no | no |
| 19 | routing_test.go | TestRecommendModelEffortLowTestabilityRaisesEffortOnly | whole package `wizard` deleted | no | no |
| 19 | routing_test.go | TestRecommendModelEffortDecorrelatedReviewAvoidsPeerModel | whole package `wizard` deleted | no | no |
| 19 | routing_test.go | TestRecommendModelEffortUnknownWorkClassIsLowConfidenceNotError | whole package `wizard` deleted | no | no |
| 19 | routing_test.go | TestRecommendModelEffortMetadataLessCatalogStillRecommends | whole package `wizard` deleted | no | no |
| 19 | routing_test.go | TestDefaultWorkClassForRoleKnownAndUnknown | whole package `wizard` deleted | no | no |
| 19 | routing_test.go | TestRecommendLeadModeMultiWorkerSuggestsPlanner | whole package `wizard` deleted | no | no |
| 19 | routing_test.go | TestRecommendLeadModeSingleMemberSuggestsBuilder | whole package `wizard` deleted | no | no |
| 19 | routing_test.go | TestRecommendationOutcomeDetectsOverride | whole package `wizard` deleted | no | no |
| 19 | spec_test.go | TestSpecSerializesExplicitSelfOperatorPolicy | whole package `wizard` deleted | no | no |
| 19 | spec_test.go | TestSpecSerializesNOCRegistrationChoice | whole package `wizard` deleted | no | no |
| 19 | spec_test.go | TestSpecArgsStableAndPreviewOnly | whole package `wizard` deleted | no | no |
| 19 | spec_test.go | TestSpecGlobalArgsNeverLeakProjectRunFlags | whole package `wizard` deleted | no | no |
| 19 | spec_test.go | TestSpecGlobalClaudeEffortUsesOnlyClaudeNativeArgs | whole package `wizard` deleted | no | no |
| 19 | spec_test.go | TestSpecArgsOmitsEmptyFields | whole package `wizard` deleted | no | no |
| 19 | spec_test.go | TestSpecArgsOmitsLegacyUnspecifiedOperatorMode | whole package `wizard` deleted | no | no |
| 19 | spec_test.go | TestSpecCarriesExplicitBackendWithoutAffectingLegacyRunStartArgs | whole package `wizard` deleted | no | no |
| 19 | spec_test.go | TestResumeArgsCanonicalComposition | whole package `wizard` deleted | no | no |
| 19 | spec_test.go | TestResumeArgsRejectsSemanticLeakage | whole package `wizard` deleted | no | no |
| 19 | spec_test.go | TestResumeArgsEmitsOnlyRedeliverGoalIntent | whole package `wizard` deleted | no | no |
| 19 | spec_test.go | TestResumeArgsPreservesEligibleNoWithoutGoalText | whole package `wizard` deleted | no | no |
| 19 | spec_test.go | TestDiscoveryFingerprintIgnoresGoalSelectionButTracksEvidence | whole package `wizard` deleted | no | no |
| 19 | spec_test.go | TestGoalExcerptStripsTerminalControlsAndBounds | whole package `wizard` deleted | no | no |
| 19 | spec_test.go | TestResumeGoalChoiceIsDownstreamAndClearedOnInvalidation | whole package `wizard` deleted | no | no |
| 19 | terminal_context_test.go | TestTerminalContextRecommendsDetachedWithoutOverridingExplicitChoice | whole package `wizard` deleted | no | no |
| 19 | terminal_context_test.go | TestTerminalContextSurfacesControlModeAtTopologyStep | whole package `wizard` deleted | no | no |
| 19 | terminal_context_test.go | TestTerminalContextDoesNotMislabelNonITermControlMode | whole package `wizard` deleted | no | no |
| 19 | terminal_context_test.go | TestTerminalContextRenderingIsBoundedAndDoesNotRenderReasons | whole package `wizard` deleted | no | no |
| 19 | terminal_context_test.go | TestTerminalContextIsRenderedByBothWizardAdapters | whole package `wizard` deleted | no | no |
| 19 | terminal_context_test.go | TestBubbleTopologyDefaultsDetachedOutsideTmux | whole package `wizard` deleted | no | no |
