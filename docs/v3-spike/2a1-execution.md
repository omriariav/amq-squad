# amq-squad 3.0, phase 2a-1 execution log

Branch `v3-2a1`, cut from `v3` at 74d3e64. Work list: `docs/v3-spike/2a1-worklist.md`. Method: plan 4.1.
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

## Deferred (running list)

- `internal/wizard/routing.go`: extract field says "move to its 3.0 home before deletion"; no home named. Kept; wizard package cut will leave this file (and so the package) in place unless ruled otherwise.
- Packages with a surviving non-test importer after all file cuts: recorded as they are reached.

## Holding file debit for 2a-2

`internal/cli/v3_holding.go`, updated per cut:
- goalOrchestratorRole, defaultGoalOrchestratorHandle (goal.go:314-317), 2 consts.
