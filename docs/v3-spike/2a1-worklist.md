# amq-squad 3.0, phase 2a-1 work list (whole-file and whole-package deletion)

Status: complete (not stopped early). Planning output only: nothing here is deleted on any branch.
Branch `v3-2a1-worklist` cut from `v3`; evidence file `docs/v3-spike/2a1-deadcode.txt`.
Sources: `docs/v3-phase1-plan.md` section 4, `docs/v3-manifest.yaml` (284 entries), `scripts/manifest-check.sh`, `docs/v3-research/manifest-pass.md`.

## 0. How to read this

- The unit is one cut = one commit in 2a-1: a delete cluster's whole files, or a package. Order is callers before callees (plan 4.1 step 3), packages when `go list` shows no non-test importer (step 4).
- "Cut files" are manifest entries with `fate: delete` in that cluster, minus the eight host files below. The manifest also carries `fate: delete` files inside clusters the task list did not name (C3, C10, C17, C18, C20, C23, C26 and a few package files); they are included as extra cuts and marked as such, because they are whole-file deletions and count toward the 2a-1 total.
- Two compile probes per cut, both `go build -gcflags=-e ./internal/cli 2>&1 | grep 'undefined:'`: "alone" deletes only that cluster from the registry-removed baseline (shows the cluster's total fan-in); "cumulative" deletes clusters in plan order without restoring (shows what is left after each commit). Errors that sit in files another delete cluster will remove are not real work; "in surviving files" is the real call-site count. Nothing was fixed; the probe output is the data.
- Probe artifacts: per-cluster raw output was kept in the scratchpad, not committed; the first errors are reproduced per cut. The deadcode run is committed.

## 1. Baseline and tooling

- `go build ./...` green, 2.9 s wall (7.3 s CPU, warm cache); `go vet ./...` green, 1.5 s wall. Module path is still `github.com/omriariav/amq-squad/v2` (module path change to /v3 is step 0, not this task).
- `deadcode` was missing; installed with `go install golang.org/x/tools/cmd/deadcode@latest` into `~/go/bin`. Run time 1.6 s.
- Branch name: the task said `git checkout -b v3/2a1-worklist v3`. That is impossible: a local branch `v3` exists, so git cannot create the ref `v3/2a1-worklist` (directory/file ref conflict). I used `v3-2a1-worklist`. See decisions.

## 2. Orphans (plan 4.1 step 1)

`internal/console` (11 files, 3,626 lines) and `internal/tmuxharness` (1 file, 485 lines): confirmed no importers.

- `go list -deps ./cmd/amq-squad | grep -E 'internal/(console|tmuxharness)'` prints nothing (exit 1).
- Repo-wide import grep over all `*.go`, tests included, finds no importer outside the packages themselves.
- Consequence: both are invisible to deadcode (not linked), so they have no lines in `2a1-deadcode.txt`; they are cut by the importer count alone. `internal/console` is the only non-test user of bubbletea, bubbles, lipgloss together with `internal/wizard`.

## 3. Entry-point removal experiment (plan 4.1 step 2)

Registration sites: `internal/cli/command_registry.go` is the only one (`commandCatalog` for help and completion, `commandRegistry` for dispatch, `lookupCommand` for hidden helpers). `cli.go` iterates the catalog but registers nothing.

Removed in the working tree (reverted afterwards):

| registration | handler | defined in | cluster |
| --- | --- | --- | --- |
| setup | runSetup | setup.go | C4 |
| init | runInit | init.go (host) | C4 |
| new | runNew | new.go | C4 |
| start | runStart | simple_start.go | C4 |
| plan | runPlan | plan.go | C4 |
| wizard | runWizardWithVersion | simple_wizard.go | C4 |
| lead | runLead | team_lead.go | C9 (file is C18) |
| goal | runGoalWithVersion | goal.go | C5 |
| namespace | runNamespace | namespace_migration_cli.go | C7 |
| broadcast | runBroadcast | operator_control.go | C2 |
| focus, open | runFocus | runtime_actions.go | C2 |
| send | runSend | runtime_actions.go | C2 |
| down | runDown | down.go | C11 |
| resume | runResume | resume.go | C11 |
| agent (hidden, catalog-excluded) | runAgent | agent.go | C11 / C23 |
| completion | runCompletion | completion.go | C24 |
| hook: claude rename helper | runClaudeSessionRename | claude_rename.go | C1 |
| hook: `_notification-watch` | runNotificationWatcher | notification_watcher.go | C8 |
| hook: `_session-notify` | runSessionNotifier | session_notifier.go | C8 |

Left registered: roles, role, brief, team, task, worktree, evidence, verify, gate, operator, status, dispatch, amq, doctor. C6 and C28 have no registration of their own; they only go dead through C5/C11 and agent/start.

Results:
- `go build ./...` stays green after the registry edit (0 undefined). The registry is the root, so nothing else breaks; the compile probe is clean at this point.
- `deadcode ./cmd/amq-squad`: 999 unreachable-func lines saved to `docs/v3-spike/2a1-deadcode.txt`. By package:

| package | unreachable funcs |
| --- | --- |
| `internal/cli` | 684 |
| `internal/wizard` | 150 |
| `internal/compoundrelease` | 46 |
| `internal/task` | 26 |
| `internal/act` | 12 |
| `internal/autonomy` | 12 |
| `internal/tmuxpane` | 10 |
| `internal/role` | 9 |
| `internal/bootstrapack` | 8 |
| `internal/state` | 8 |
| `internal/operatorauth` | 5 |
| `internal/flock` | 4 |
| `internal/liveidentity` | 4 |
| `internal/attention` | 3 |
| `internal/launch` | 3 |
| `internal/notifier` | 3 |
| `internal/team` | 3 |
| `internal/runtimeaction` | 2 |
| `internal/userconfig` | 2 |
| `internal/activity` | 1 |
| `internal/procinfo` | 1 |
| `internal/rules` | 1 |
| `internal/runtimecontrol` | 1 |
| `internal/worktreeplan` | 1 |

- Not every function in a cut file goes dead: many remain reachable from the kept entry points (`team`, `status`, `doctor`, `task`, `dispatch`, `operator`, `amq`). That residue is exactly the call-site work in section 6, and it is why the compile probe, not deadcode alone, is the work list. deadcode also flags 46 dead funcs in `compoundrelease` and 26 in `task`; those are keep or shrink packages, so they are 2a-2 candidates, not 2a-1.

## 4. Host files: do not delete; shrink to budget

These eight files sit inside delete clusters but carry the 4.3 shrink budget (manifest header rule 3). They are excluded from every cut below. Their in-file cuts are 2a-2, one cluster per commit.

| file | cluster | manifest fate | v2 lines | target | note |
| --- | --- | --- | --- | --- | --- |
| `internal/cli/init.go` | C4 | shrink | 284 | 150 | rules pointer-stub digest flow (`:110`, `:266`) |
| `internal/cli/namespace_migration_guard.go` | C7 | shrink | 573 | 133 | admission locks (`:42,67,84,253`); 16 undefined sites after C7 |
| `internal/cli/namespace_open_unix.go` | C7 | keep | 117 | 117 | safe open |
| `internal/cli/exit.go` | C11 | keep | 174 | 174 | whole file kept |
| `internal/cli/wait_posture.go` | C11 | shrink | 303 | 100 | `:67,170` |
| `internal/cli/restore.go` | C11 | shrink | 758 | 80 | 4 undefined sites after cuts |
| `internal/cli/session_reset_support.go` | C11 | shrink | 691 | 46 | 33 undefined sites after cuts (largest host fan-out) |
| `internal/cli/launch.go` | C28 | shrink | 1,214 | 300 | record write and rollback (`:757,780`), role write (`:1182-1196`), AMQ env resolver (`:917`) into seat-exec; 13 undefined sites after cuts |

The `namespace_rename_darwin.go` / `_linux.go` / `_other.go` stubs and `namespace_open_other.go` are fate delete and are cut with C7. All three rename stubs define the same symbol (`namespaceRenameNoReplace`); the darwin one shows as dead because the migration tx was its only caller on this platform. Check whether the kept guard host or `namespace_open_unix.go` needs a rename primitive before removing them (see decision 5).

## 5. Order of cuts

| # | cut | files | lines | probe alone: undefined (distinct syms, in surviving files) | cumulative undefined after |
| --- | --- | --- | --- | --- | --- |
| 1 | P console | 11 | 3,626 | none (orphan) | 0 |
| 2 | P tmuxharness | 1 | 485 | none (orphan) | 0 |
| 3 | C5 goal supervision | 10 | 6,204 | 131 (42, 36) | 131 |
| 4 | C6 recovery transition | 5 | 1,449 | 44 (23, 0) | 66 |
| 5 | C4 wizard, start, new, setup, plan, list, preview_flags | 8 | 4,189 | 48 (31, 19) | 110 |
| 6 | C11 up, down, resume, visibility | 7 | 3,200 | 48 (20, 12) | 129 |
| 7 | C8 notifications | 3 | 2,361 | 32 (14, 19) | 155 |
| 8 | C9 external orchestrator, lead identity | 3 | 1,220 | 37 (13, 17) | 183 |
| 9 | C18 team_* delete files (extra) | 7 | 4,197 | 62 (24, 41) | 202 |
| 10 | C7 namespace migration | 10 | 2,888 | 56 (18, 36) | 226 |
| 11 | C28 launch.go | 0 | 0 | host only, no cut | 226 |
| 12 | C3 status_board, global_status_doctor (extra) | 2 | 989 | 9 (7, 8) | 229 |
| 13 | C17 operator_delivery, handoff_card (extra) | 2 | 333 | 20 (5, 17) | 246 |
| 14 | C10 live_identity, version_alignment (extra) | 2 | 641 | 32 (16, 12) | 265 |
| 15 | C2 runtime control | 4 | 1,650 | 144 (34, 83) | 346 |
| 16 | C1 tmux, terminal, layout, wake, panes | 25 | 5,473 | 129 (70, 61) | 361 |
| 17 | C26 profiles.go (extra) | 1 | 144 | 8 (2, 3) | 364 |
| 18 | C23 agent.go (extra) | 1 | 339 | 0 | 364 |
| 19 | C20 collect_* (extra; evidence.go is replace, not cut) | 4 | 690 | 2 (2, 0) | 364 |
| 20 | C24 completion | 1 | 996 | 0 | 364 |
| 3 (pkg) to 13 (pkg) | 11 packages, section 7: act, autonomy, wizard, runtimeaction, runtimecontrol, liveidentity, notifier, bootstrapack, tmuxpane, commandevidence, attention | 31 | 8,820 | see section 7 | |

Two sequencing notes from the probe data:
- Cutting C5 before C6 matters: C6 alone is 44 undefined lines but 0 in surviving files, because only goal and resume reach it. C6 is a pure follow-on of C5 and C11.
- C2 (144) and C1 (129) have the widest fan-in. Most of it lands in `down.go`, `status.go`, `dispatch.go`, `global_status.go` and `session_reset_support.go`, which the earlier C11 and C3 cuts shrink or remove, so these two are cheapest late. Cutting C1 or C2 first would touch the same status and dispatch call sites twice.
- Platform caveat: deadcode and the probe ran on darwin only. Files carrying linux, windows or `_other` build tags (`namespace_rename_linux.go`, `namespace_rename_other.go`, `namespace_open_other.go`, `pane_cleanup_manifest_other.go`, `pane_cleanup_manifest_rename_linux.go`, `team_launch_native_terminal.go` variants) are not compiled on darwin, so their "0 unreachable" rows and their probe contribution are artifacts. Run `GOOS=linux go build ./...` after each cut.
- Entries 9, 12, 13, 14, 17, 18, 19 are not in the task's delete-cluster list; they are `fate: delete` files in shrink clusters (see section 0). Entry 19 excludes `evidence.go` (fate replace).

## 6. Cuts in order (internal/cli clusters)

C-numbered cuts below are listed in the section 5 order, except that the two orphan packages and all packages are in section 7.

### C5: goal supervision

- Cut: 10 files, 6,204 lines (manifest `v2_lines`).
- Manifest entries (cluster C5, fate delete):
  - `internal/cli/goal_attempt.go` (130)
  - `internal/cli/goal_supervise_resume.go` (564)
  - `internal/cli/goal_supervision_binding_revalidation.go` (171)
  - `internal/cli/goal_supervision_delivery_gates.go` (285)
  - `internal/cli/goal_supervision_delivery.go` (468)
  - `internal/cli/goal_supervision_gates.go` (346)
  - `internal/cli/goal_supervision_resume.go` (571)
  - `internal/cli/goal_supervision.go` (1067)
  - `internal/cli/goal.go` (1983)
  - `internal/cli/resume_goal.go` (619)
- Entry points removed: registry: goal (runGoalWithVersion). Also reached through resume (resume_goal.go).
- Deadcode (after the registry removal; full output in `docs/v3-spike/2a1-deadcode.txt`): 109 of 131 top-level funcs in the cut files are reported unreachable; the rest are still reachable from the remaining entry points, directly or through other cut files that are themselves still reachable (see the probe lines).

  | file | funcs | unreachable | deadcode.txt lines | still reachable (first 5) |
  | --- | --- | --- | --- | --- |
  | `goal_attempt.go` | 5 | 1 | 250-250 | goalAttemptDir, goalAttemptPath, goalAttemptClaimPath, readGoalAttempt |
  | `goal_supervise_resume.go` | 5 | 5 | 251-255 | none |
  | `goal_supervision_binding_revalidation.go` | 1 | 1 | 285-285 | none |
  | `goal_supervision_delivery_gates.go` | 3 | 3 | 294-296 | none |
  | `goal_supervision_delivery.go` | 9 | 8 | 286-293 | supervisionDeliveredDirectivePublisher |
  | `goal_supervision_gates.go` | 5 | 5 | 297-301 | none |
  | `goal_supervision_resume.go` | 3 | 3 | 302-304 | none |
  | `goal_supervision.go` | 30 | 29 | 256-284 | digestGoalSupervisionString |
  | `goal.go` | 51 | 40 | 210-249 | goalDeliveryContractForBinary, prompt, goalBindingPayload, exactGoalBinding, launchRecordHasGoalBinding |
  | `resume_goal.go` | 19 | 14 | 563-576 | validateResumeGoalAttempt, validateResumeGoalClaim, parseNativeGoalBindingCommand, splitGeneratedGoalTokens, digestBytes |
- Compile probe, this cluster alone (against the registry-removed baseline): 131 undefined lines, 42 distinct symbols; 36 of them sit in files that survive 2a-1, the rest in files other delete clusters remove.
  First errors (surviving files first):
  - `internal/cli/doctor.go:50` undefined: `GoalSupervisionAssessment`
  - `internal/cli/doctor.go:836` undefined: `resolveGoalSupervisionAssessment`
  - `internal/cli/status.go:116` undefined: `goalBindingData`
  - `internal/cli/status.go:117` undefined: `GoalSupervisionAssessment`
  - `internal/cli/status.go:878` undefined: `launchRecordHasGoalBinding`
  - `internal/cli/status.go:1077` undefined: `goalBindingData`
- Compile probe, cumulative in plan order (callers before callees): 131 undefined lines total after this step, 36 in surviving files.
- Extract before the cut (manifest `extract`):
  - `resume_goal.go`: digestBytes (:383)

### C6: recovery transition

- Cut: 5 files, 1,449 lines (manifest `v2_lines`).
- Manifest entries (cluster C6, fate delete):
  - `internal/cli/recovery_transition_cas.go` (146)
  - `internal/cli/recovery_transition_contract.go` (297)
  - `internal/cli/recovery_transition_publication.go` (195)
  - `internal/cli/recovery_transition_scan.go` (229)
  - `internal/cli/recovery_transition.go` (582)
- Entry points removed: no direct registration; reachable only through goal and resume paths, so it goes dead with C5 and C11.
- Deadcode (after the registry removal; full output in `docs/v3-spike/2a1-deadcode.txt`): 27 of 27 top-level funcs in the cut files are reported unreachable; the rest are still reachable from the remaining entry points, directly or through other cut files that are themselves still reachable (see the probe lines).

  | file | funcs | unreachable | deadcode.txt lines | still reachable (first 5) |
  | --- | --- | --- | --- | --- |
  | `recovery_transition_cas.go` | 3 | 3 | 524-526 | none |
  | `recovery_transition_contract.go` | 4 | 4 | 527-530 | none |
  | `recovery_transition_publication.go` | 3 | 3 | 531-533 | none |
  | `recovery_transition_scan.go` | 9 | 9 | 534-542 | none |
  | `recovery_transition.go` | 8 | 8 | 516-523 | none |
- Compile probe, this cluster alone (against the registry-removed baseline): 44 undefined lines, 23 distinct symbols; 0 of them sit in files that survive 2a-1, the rest in files other delete clusters remove.
  First errors (surviving files first):
  - `internal/cli/goal_supervision_gates.go:127` undefined: `pauseLedgerScan`
  - `internal/cli/goal_supervision_gates.go:280` undefined: `supervisionClaimKey`
  - `internal/cli/goal_supervision_resume.go:186` undefined: `supervisionReservedClaimLoader`
  - `internal/cli/goal_supervision_resume.go:201` undefined: `scanRecoveryTransitionsForPause`
  - `internal/cli/goal_supervision_resume.go:219` undefined: `newRecoveryTransitionRecord`
  - `internal/cli/goal_supervision_resume.go:219` undefined: `recoveryTransitionInput`
- Compile probe, cumulative in plan order (callers before callees): 66 undefined lines total after this step, 36 in surviving files.
- Extract before the cut: none listed in the manifest.

### C4: wizard, start, new, setup, plan, list, preview_flags (init.go is a host)

- Cut: 8 files, 4,189 lines (manifest `v2_lines`).
- Manifest entries (cluster C4, fate delete):
  - `internal/cli/list.go` (226)
  - `internal/cli/new.go` (654)
  - `internal/cli/plan.go` (256)
  - `internal/cli/preview_flags.go` (193)
  - `internal/cli/setup.go` (479)
  - `internal/cli/simple_start_brief_draft.go` (236)
  - `internal/cli/simple_start.go` (1345)
  - `internal/cli/simple_wizard.go` (800)
- Do not delete; shrink to budget (host files):
  - `internal/cli/init.go` v2 284 -> target 150 (fate shrink); in-file cut is 2a-2.
- Entry points removed: registry: setup, init, new, start, plan, wizard.
- Deadcode (after the registry removal; full output in `docs/v3-spike/2a1-deadcode.txt`): 68 of 123 top-level funcs in the cut files are reported unreachable; the rest are still reachable from the remaining entry points, directly or through other cut files that are themselves still reachable (see the probe lines).

  | file | funcs | unreachable | deadcode.txt lines | still reachable (first 5) |
  | --- | --- | --- | --- | --- |
  | `list.go` | 9 | 7 | 352-358 | readPresenceForEntry, presencePath |
  | `new.go` | 18 | 15 | 431-445 | peelProjectFlag, runInProject, publishTeamInitFlagSet |
  | `plan.go` | 5 | 3 | 511-513 | planPrepareFiltered, printPlanResult |
  | `preview_flags.go` | 5 | 0 | - | registerPreviewFlags, toEmitOptions, registerLiveLaunchFlags, buildLiveLaunchOptions, parseLaunchapiDecisions |
  | `setup.go` | 16 | 16 | 618-633 | none |
  | `simple_start_brief_draft.go` | 7 | 0 | - | draftSimpleStartBrief, buildSimpleStartBriefPrompt, validateSimpleStartBriefDraft, nonEmptyTrimmedLines, normalizeMarkdownBulletMarker |
  | `simple_start.go` | 36 | 0 | - | Error, Unwrap, callSimpleStartCheckpoint, simpleStartLaunch, defaultSimpleStartDependencies |
  | `simple_wizard.go` | 27 | 27 | 634-660 | none |
- Compile probe, this cluster alone (against the registry-removed baseline): 48 undefined lines, 31 distinct symbols; 19 of them sit in files that survive 2a-1, the rest in files other delete clusters remove.
  First errors (surviving files first):
  - `internal/cli/team_launch.go:85` undefined: `simpleStartCheckpoint`
  - `internal/cli/team_launch.go:177` undefined: `registerPreviewFlags`
  - `internal/cli/team_launch.go:178` undefined: `registerLiveLaunchFlags`
  - `internal/cli/team_launch.go:230` undefined: `buildLiveLaunchOptions`
  - `internal/cli/brief.go:20` undefined: `defaultSimpleStartDependencies`
  - `internal/cli/brief.go:29` undefined: `simpleStartDependencies`
- Compile probe, cumulative in plan order (callers before callees): 110 undefined lines total after this step, 55 in surviving files.
- Extract before the cut (manifest `extract`):
  - `init.go`: rules pointer-stub digest flow (:110 rules.Plan, :266 freshPlans, :92)
  - `new.go`: peelProjectFlag, runInProject arg helpers

### C11: up, down, resume, visibility, resume_* (exit, wait_posture, restore, session_reset_support are hosts)

- Cut: 7 files, 3,200 lines (manifest `v2_lines`).
- Manifest entries (cluster C11, fate delete):
  - `internal/cli/down.go` (1780)
  - `internal/cli/resume_exec.go` (249)
  - `internal/cli/resume_required_actions.go` (167)
  - `internal/cli/resume_shared.go` (124)
  - `internal/cli/resume.go` (342)
  - `internal/cli/up.go` (431)
  - `internal/cli/visibility.go` (107)
- Do not delete; shrink to budget (host files):
  - `internal/cli/exit.go` v2 174 -> target 174 (fate keep); in-file cut is 2a-2.
  - `internal/cli/restore.go` v2 758 -> target 80 (fate shrink); in-file cut is 2a-2.
  - `internal/cli/session_reset_support.go` v2 691 -> target 46 (fate shrink); in-file cut is 2a-2.
  - `internal/cli/wait_posture.go` v2 303 -> target 100 (fate shrink); in-file cut is 2a-2.
- Entry points removed: registry: down, resume, agent (up/restore run via agent). Note init.go hosts profileInitCommand callers.
- Deadcode (after the registry removal; full output in `docs/v3-spike/2a1-deadcode.txt`): 11 of 70 top-level funcs in the cut files are reported unreachable; the rest are still reachable from the remaining entry points, directly or through other cut files that are themselves still reachable (see the probe lines).

  | file | funcs | unreachable | deadcode.txt lines | still reachable (first 5) |
  | --- | --- | --- | --- | --- |
  | `down.go` | 40 | 1 | 138-138 | newSignalTerminator, Terminate, SignalName, signalNameOf, runDown |
  | `resume_exec.go` | 3 | 0 | - | runResumeExec, runResumeExecWithDependencies, resolveResumeLeadGate |
  | `resume_required_actions.go` | 7 | 2 | 577-578 | resumeActionID, newLeadNotLiveAction, newExternalLeadRecordDeadAction, resolveResumeRequiredActions, resumeDecisionChoicesToStrings |
  | `resume_shared.go` | 6 | 0 | - | parseResumeRoles, teamRoleList, canonicalPath, compactNativeValue, findMemberRestoreRecord |
  | `resume.go` | 3 | 0 | - | stderrIsTerminal, resolveLastSessionForProfile, runResume |
  | `up.go` | 6 | 5 | 701-705 | profileInitCommand |
  | `visibility.go` | 5 | 3 | 712-714 | normalizeLaunchVisibility, visibilityPreviewLaunchCommand |
- Compile probe, this cluster alone (against the registry-removed baseline): 48 undefined lines, 20 distinct symbols; 12 of them sit in files that survive 2a-1, the rest in files other delete clusters remove.
  First errors (surviving files first):
  - `internal/cli/session_reset_support.go:74` undefined: `processTerminator`
  - `internal/cli/dispatch.go:208` undefined: `profileInitCommand`
  - `internal/cli/doctor.go:1395` undefined: `profileInitCommand`
  - `internal/cli/effort.go:229` undefined: `compactNativeValue`
  - `internal/cli/model_defaults.go:52` undefined: `compactNativeValue`
  - `internal/cli/operator.go:834` undefined: `profileInitCommand`
- Compile probe, cumulative in plan order (callers before callees): 129 undefined lines total after this step, 67 in surviving files.
- Extract before the cut (manifest `extract`):
  - `exit.go`: kept whole: exit-code taxonomy, PartialError (:39), ActionDecisionError (:49), parseFlags (:103)
  - `restore.go`: shellQuote (:713), shellCommand (:731)
  - `session_reset_support.go`: allowInterspersedFlags (:98)
  - `up.go`: briefCandidateEnvelope (:424)
  - `wait_posture.go`: guardOwnedWait (:67), durableSendWaitPosture (:170)

### C8: notifications (watcher, AMQ watch, session notifier)

- Cut: 3 files, 2,361 lines (manifest `v2_lines`).
- Manifest entries (cluster C8, fate delete):
  - `internal/cli/notification_amq_watch.go` (303)
  - `internal/cli/notification_watcher.go` (1192)
  - `internal/cli/session_notifier.go` (866)
- Entry points removed: lookupCommand hooks: _notification-watch, _session-notify.
- Deadcode (after the registry removal; full output in `docs/v3-spike/2a1-deadcode.txt`): 34 of 63 top-level funcs in the cut files are reported unreachable; the rest are still reachable from the remaining entry points, directly or through other cut files that are themselves still reachable (see the probe lines).

  | file | funcs | unreachable | deadcode.txt lines | still reachable (first 5) |
  | --- | --- | --- | --- | --- |
  | `notification_amq_watch.go` | 7 | 7 | 446-452 | none |
  | `notification_watcher.go` | 27 | 8 | 453-460 | PID, Signal, Release, notificationWatcherRuntimePath, notificationWatcherLockPath |
  | `session_notifier.go` | 29 | 19 | 588-606 | sessionNotifierRuntimePath, sessionNotifierLockPath, sessionNotifierLogPath, readSessionNotifierRecord, writeSessionNotifierRecord |
- Compile probe, this cluster alone (against the registry-removed baseline): 32 undefined lines, 14 distinct symbols; 19 of them sit in files that survive 2a-1, the rest in files other delete clusters remove.
  First errors (surviving files first):
  - `internal/cli/global_status.go:125` undefined: `notificationWatcherStatus`
  - `internal/cli/global_status.go:157` undefined: `notificationWatcherStatus`
  - `internal/cli/global_status.go:431` undefined: `inspectNotificationWatcher`
  - `internal/cli/status.go:110` undefined: `notificationWatcherStatus`
  - `internal/cli/status.go:448` undefined: `inspectNotificationWatcher`
  - `internal/cli/status.go:479` undefined: `inspectNotificationWatcher`
- Compile probe, cumulative in plan order (callers before callees): 155 undefined lines total after this step, 86 in surviving files.
- Extract before the cut: none listed in the manifest.

### C9: external orchestrator, lead identity

- Cut: 3 files, 1,220 lines (manifest `v2_lines`).
- Manifest entries (cluster C9, fate delete):
  - `internal/cli/external_orchestrator_lifecycle.go` (365)
  - `internal/cli/external_orchestrator_registry.go` (710)
  - `internal/cli/lead_identity.go` (145)
- Entry points removed: registry: lead (runLead, team_lead.go).
- Deadcode (after the registry removal; full output in `docs/v3-spike/2a1-deadcode.txt`): 36 of 47 top-level funcs in the cut files are reported unreachable; the rest are still reachable from the remaining entry points, directly or through other cut files that are themselves still reachable (see the probe lines).

  | file | funcs | unreachable | deadcode.txt lines | still reachable (first 5) |
  | --- | --- | --- | --- | --- |
  | `external_orchestrator_lifecycle.go` | 11 | 11 | 146-156 | none |
  | `external_orchestrator_registry.go` | 24 | 23 | 157-179 | syncExternalOrchestratorRegistryDirectory |
  | `lead_identity.go` | 12 | 2 | 350-351 | projectExecutionMode, launchRecordMatchesIdentity, launchRecordProfileMatches, launchRecordMatchesPane, launchRecordAuthorizesProjectLead |
- Compile probe, this cluster alone (against the registry-removed baseline): 37 undefined lines, 13 distinct symbols; 17 of them sit in files that survive 2a-1, the rest in files other delete clusters remove.
  First errors (surviving files first):
  - `internal/cli/amq.go:806` undefined: `currentEnvProvesTeamRole`
  - `internal/cli/amq.go:856` undefined: `projectExecutionMode`
  - `internal/cli/amq.go:857` undefined: `launchRecordAuthorizesProjectLead`
  - `internal/cli/amq.go:859` undefined: `launchRecordMatchesIdentity`
  - `internal/cli/bootstrap.go:228` undefined: `sameFilesystemPath`
  - `internal/cli/operator_identity.go:38` undefined: `findLaunchRecordByPane`
- Compile probe, cumulative in plan order (callers before callees): 183 undefined lines total after this step, 103 in surviving files.
- Extract before the cut (manifest `extract`):
  - `lead_identity.go`: sameFilesystemPath, currentWorkingDirMatches

### C18: team_* files with fate delete inside shrink cluster C18 (not in the task list; discovered in manifest)

- Cut: 7 files, 4,197 lines (manifest `v2_lines`).
- Manifest entries (cluster C18, fate delete):
  - `internal/cli/team_autonomous.go` (147)
  - `internal/cli/team_lead.go` (1175)
  - `internal/cli/team_member_diff.go` (149)
  - `internal/cli/team_member.go` (1051)
  - `internal/cli/team_overlay.go` (1310)
  - `internal/cli/team_rm.go` (223)
  - `internal/cli/team_shared_cwd.go` (142)
- Entry points removed: no registration removed (team stays registered as the 2b rewrite host); these files go dead only after the C4, C9, C11 cuts.
- Deadcode (after the registry removal; full output in `docs/v3-spike/2a1-deadcode.txt`): 15 of 110 top-level funcs in the cut files are reported unreachable; the rest are still reachable from the remaining entry points, directly or through other cut files that are themselves still reachable (see the probe lines).

  | file | funcs | unreachable | deadcode.txt lines | still reachable (first 5) |
  | --- | --- | --- | --- | --- |
  | `team_autonomous.go` | 5 | 0 | - | runTeamAutonomous, runTeamAutonomousShow, runTeamAutonomousMutation, readAutonomousTeam, resolveAutonomousProjectProfile |
  | `team_lead.go` | 29 | 14 | 685-698 | normalizeWakeRetryUntil, runTeamLead, runTeamLeadSet, runTeamLeadClear, runTeamLeadShow |
  | `team_member_diff.go` | 5 | 0 | - | memberFieldChanges, displayMemberArgs, displayMemberScalar, displayMemberFieldValue, writeMemberUpdatePreview |
  | `team_member.go` | 24 | 0 | - | wantsHelp, runTeamMember, runTeamMemberList, orDash, peelPositional |
  | `team_overlay.go` | 37 | 1 | 699-699 | overlayPath, runTeamOverlay, runTeamOverlayInit, applyGeneratedToolPolicies, applyGeneratedToolPolicyPlans |
  | `team_rm.go` | 6 | 0 | - | runTeamRemove, executeTeamRemove, executeTeamRemoveAll, configuredTeamProfiles, confirmTeamRemove |
  | `team_shared_cwd.go` | 4 | 0 | - | runTeamSharedCwdException, runTeamSharedCwdExceptionSet, runTeamSharedCwdExceptionClear, runTeamSharedCwdExceptionShow |
- Compile probe, this cluster alone (against the registry-removed baseline): 62 undefined lines, 24 distinct symbols; 41 of them sit in files that survive 2a-1, the rest in files other delete clusters remove.
  First errors (surviving files first):
  - `internal/cli/mutation_json.go:43` undefined: `memberFieldChange`
  - `internal/cli/global_noc.go:102` undefined: `currentPaneIdentity`
  - `internal/cli/amq_root_authority.go:517` undefined: `inheritedSession`
  - `internal/cli/doctor.go:1078` undefined: `validateMemberOverlayPaths`
  - `internal/cli/init.go:55` undefined: `wantsHelp`
  - `internal/cli/pathnorm.go:242` undefined: `dedupeSortedStrings`
- Compile probe, cumulative in plan order (callers before callees): 202 undefined lines total after this step, 144 in surviving files.
- Extract before the cut (manifest `extract`):
  - `team_launch_launchapi.go`: cut tmuxTeamLaunchBackend.Validate (:37) and legacyTeamLaunchBackend (:111); inline tmux presence check if still needed; rewrite in 2b
  - `team_rules.go`: renderTeamRules (:40)

### C7: namespace migration, conflict, rename (guard and open_unix are hosts)

- Cut: 10 files, 2,888 lines (manifest `v2_lines`).
- Manifest entries (cluster C7, fate delete):
  - `internal/cli/namespace_conflict.go` (514)
  - `internal/cli/namespace_migration_adapters.go` (293)
  - `internal/cli/namespace_migration_cli.go` (239)
  - `internal/cli/namespace_migration_native_claims.go` (134)
  - `internal/cli/namespace_migration_plan.go` (665)
  - `internal/cli/namespace_migration_tx.go` (998)
  - `internal/cli/namespace_open_other.go` (18)
  - `internal/cli/namespace_rename_darwin.go` (9)
  - `internal/cli/namespace_rename_linux.go` (9)
  - `internal/cli/namespace_rename_other.go` (9)
- Do not delete; shrink to budget (host files):
  - `internal/cli/namespace_migration_guard.go` v2 573 -> target 133 (fate shrink); in-file cut is 2a-2.
  - `internal/cli/namespace_open_unix.go` v2 117 -> target 117 (fate shrink); in-file cut is 2a-2.
- Entry points removed: registry: namespace.
- Deadcode (after the registry removal; full output in `docs/v3-spike/2a1-deadcode.txt`): 63 of 92 top-level funcs in the cut files are reported unreachable; the rest are still reachable from the remaining entry points, directly or through other cut files that are themselves still reachable (see the probe lines).

  | file | funcs | unreachable | deadcode.txt lines | still reachable (first 5) |
  | --- | --- | --- | --- | --- |
  | `namespace_conflict.go` | 21 | 0 | - | ensureNoNamespaceCreationCollision, namespaceCreationCollision, pathContains, suggestedCollisionProfile, allowsExactNamedProfileDown |
  | `namespace_migration_adapters.go` | 11 | 11 | 362-372 | none |
  | `namespace_migration_cli.go` | 9 | 9 | 373-381 | none |
  | `namespace_migration_native_claims.go` | 1 | 1 | 385-385 | none |
  | `namespace_migration_plan.go` | 19 | 17 | 386-402 | canonicalMigrationProject, dedupeMigrationStrings |
  | `namespace_migration_tx.go` | 26 | 24 | 403-426 | readNamespaceMigrationJournal, rejectMigrationSymlinkComponents |
  | `namespace_open_other.go` | 2 | 0 | - | openNamespaceNoFollow, openContainedNamespaceLock |
  | `namespace_rename_darwin.go` | 1 | 1 | 428-428 | none |
  | `namespace_rename_linux.go` | 1 | 0 | - | namespaceRenameNoReplace |
  | `namespace_rename_other.go` | 1 | 0 | - | namespaceRenameNoReplace |
- Compile probe, this cluster alone (against the registry-removed baseline): 56 undefined lines, 18 distinct symbols; 36 of them sit in files that survive 2a-1, the rest in files other delete clusters remove.
  First errors (surviving files first):
  - `internal/cli/namespace_migration_guard.go:96` undefined: `dedupeMigrationStrings`
  - `internal/cli/namespace_migration_guard.go:115` undefined: `dedupeMigrationStrings`
  - `internal/cli/namespace_migration_guard.go:143` undefined: `dedupeMigrationStrings`
  - `internal/cli/namespace_migration_guard.go:174` undefined: `rejectMigrationSymlinkComponents`
  - `internal/cli/namespace_migration_guard.go:225` undefined: `dedupeMigrationStrings`
  - `internal/cli/namespace_migration_guard.go:332` undefined: `namespaceMigrationPlan`
- Compile probe, cumulative in plan order (callers before callees): 226 undefined lines total after this step, 180 in surviving files.
- Extract before the cut (manifest `extract`):
  - `namespace_migration_guard.go`: acquireNamespaceWriterAdmission (:253), acquireRevalidatedContextWriter (:42), acquireRevalidatedAMQWriter (:67), validateReResolvedContext (:84), namespaceAdmissionLocks
  - `namespace_open_unix.go`: safe open kept whole

### C28: launch.go (host only; nothing to delete whole-file)

- Cut: 0 files, 0 lines (manifest `v2_lines`) (no whole-file cut).
- Manifest entries (cluster C28, fate delete):
- Do not delete; shrink to budget (host files):
  - `internal/cli/launch.go` v2 1214 -> target 300 (fate shrink); in-file cut is 2a-2.
- Entry points removed: no registration; launch.go is reached from agent/start/team launch paths. Host file: shrink to 300, not deleted whole-file.
- Deadcode (after the registry removal; full output in `docs/v3-spike/2a1-deadcode.txt`): 0 of 0 top-level funcs in the cut files are reported unreachable; the rest are still reachable from the remaining entry points, directly or through other cut files that are themselves still reachable (see the probe lines).

  | file | funcs | unreachable | deadcode.txt lines | still reachable (first 5) |
  | --- | --- | --- | --- | --- |
- Compile probe, this cluster alone (against the registry-removed baseline): 0 undefined lines, 0 distinct symbols; 0 of them sit in files that survive 2a-1, the rest in files other delete clusters remove.
  Build is green after the cut (0 undefined symbols).
- Compile probe, cumulative in plan order (callers before callees): 226 undefined lines total after this step, 180 in surviving files.
- Extract before the cut (manifest `extract`):
  - `launch.go`: writeLaunchRecordWithSnapshot (:757), rollbackLaunchRecordIfCurrent (:780), role write (:1182-1196), resolveAMQEnvForLaunch (:917), normalizedAgentBinary (:1093), ensureLauncherExecutable (:900), stringListFlag (:84) into seat-exec

### C3: board: status_board.go, global_status_doctor.go (fate delete inside shrink cluster C3)

- Cut: 2 files, 989 lines (manifest `v2_lines`).
- Manifest entries (cluster C3, fate delete):
  - `internal/cli/global_status_doctor.go` (224)
  - `internal/cli/status_board.go` (765)
- Entry points removed: none removed; status (kept) still references these, see probe.
- Deadcode (after the registry removal; full output in `docs/v3-spike/2a1-deadcode.txt`): 1 of 31 top-level funcs in the cut files are reported unreachable; the rest are still reachable from the remaining entry points, directly or through other cut files that are themselves still reachable (see the probe lines).

  | file | funcs | unreachable | deadcode.txt lines | still reachable (first 5) |
  | --- | --- | --- | --- | --- |
  | `global_status_doctor.go` | 3 | 0 | - | doctorCheckGlobalNOCRegistration, doctorCheckGlobalNOCRegistrationAtRoot, sameGlobalNOCRegistration |
  | `status_board.go` | 28 | 1 | 663-663 | runStatusBoard, runStatusBoardWithVersion, executeStatusBoard, enrichBoardNotificationWatcher, boardProfilesForProject |
- Compile probe, this cluster alone (against the registry-removed baseline): 9 undefined lines, 7 distinct symbols; 8 of them sit in files that survive 2a-1, the rest in files other delete clusters remove.
  First errors (surviving files first):
  - `internal/cli/amq_env.go:117` undefined: `defaultBaseRootName`
  - `internal/cli/amq_env.go:264` undefined: `defaultBaseRootName`
  - `internal/cli/amq_env.go:270` undefined: `defaultBaseRootName`
  - `internal/cli/cli.go:169` undefined: `runStatusBoard`
  - `internal/cli/doctor.go:1816` undefined: `doctorCheckGlobalNOCRegistration`
  - `internal/cli/status.go:344` undefined: `runStatusBoardWithVersion`
- Compile probe, cumulative in plan order (callers before callees): 229 undefined lines total after this step, 188 in surviving files.
- Extract before the cut (manifest `extract`):
  - `global_noc.go`: fleet registry read/write (:117-265) kept per 4.4 default

### C17: operator_delivery.go, operator_handoff_card.go (fate delete inside shrink cluster C17)

- Cut: 2 files, 333 lines (manifest `v2_lines`).
- Manifest entries (cluster C17, fate delete):
  - `internal/cli/operator_delivery.go` (117)
  - `internal/cli/operator_handoff_card.go` (216)
- Entry points removed: none removed; operator (kept) references these.
- Deadcode (after the registry removal; full output in `docs/v3-spike/2a1-deadcode.txt`): 0 of 9 top-level funcs in the cut files are reported unreachable; the rest are still reachable from the remaining entry points, directly or through other cut files that are themselves still reachable (see the probe lines).

  | file | funcs | unreachable | deadcode.txt lines | still reachable (first 5) |
  | --- | --- | --- | --- | --- |
  | `operator_delivery.go` | 3 | 0 | - | operatorDeliveryForTeam, operatorDeliveryForTeamAtRoot, operatorDeliverySummary |
  | `operator_handoff_card.go` | 6 | 0 | - | newOperatorHandoffCard, operatorHandoffScope, operatorConsoleSurface, operatorHandoffCardField, writeOperatorHandoffCard |
- Compile probe, this cluster alone (against the registry-removed baseline): 20 undefined lines, 5 distinct symbols; 17 of them sit in files that survive 2a-1, the rest in files other delete clusters remove.
  First errors (surviving files first):
  - `internal/cli/bootstrap.go:41` undefined: `operatorDeliveryData`
  - `internal/cli/bootstrap.go:351` undefined: `operatorDeliveryData`
  - `internal/cli/bootstrap.go:358` undefined: `operatorDeliveryData`
  - `internal/cli/bootstrap.go:360` undefined: `operatorDeliveryForTeamAtRoot`
  - `internal/cli/operator.go:67` undefined: `operatorDeliveryData`
  - `internal/cli/operator.go:1325` undefined: `operatorDeliveryForTeam`
- Compile probe, cumulative in plan order (callers before callees): 246 undefined lines total after this step, 205 in surviving files.
- Extract before the cut (manifest `extract`):
  - `operator_attention.go`: collectGateAttentionProjection (:433)

### C10: live_identity.go, version_alignment.go (fate delete inside shrink cluster C10)

- Cut: 2 files, 641 lines (manifest `v2_lines`).
- Manifest entries (cluster C10, fate delete):
  - `internal/cli/live_identity.go` (323)
  - `internal/cli/version_alignment.go` (318)
- Entry points removed: none removed; liveness/status (kept) references these.
- Deadcode (after the registry removal; full output in `docs/v3-spike/2a1-deadcode.txt`): 6 of 31 top-level funcs in the cut files are reported unreachable; the rest are still reachable from the remaining entry points, directly or through other cut files that are themselves still reachable (see the probe lines).

  | file | funcs | unreachable | deadcode.txt lines | still reachable (first 5) |
  | --- | --- | --- | --- | --- |
  | `live_identity.go` | 15 | 3 | 359-361 | Error, VerifyTerminalActorLiveIdentity, verifyTerminalActorLiveIdentity, verifyRuntimeActionWithRecord, verifyRuntimeActionByHandle |
  | `version_alignment.go` | 16 | 3 | 709-711 | defaultVersionAlignmentSources, versionAlignmentSourcesFromDoctor, buildVersionAlignment, normalizeRuntimeVersion, devRuntimeVersion |
- Compile probe, this cluster alone (against the registry-removed baseline): 32 undefined lines, 16 distinct symbols; 12 of them sit in files that survive 2a-1, the rest in files other delete clusters remove.
  First errors (surviving files first):
  - `internal/cli/doctor.go:60` undefined: `versionAlignmentData`
  - `internal/cli/doctor.go:151` undefined: `defaultClaudeSkillCacheRoot`
  - `internal/cli/doctor.go:521` undefined: `buildVersionAlignment`
  - `internal/cli/doctor.go:521` undefined: `versionAlignmentSourcesFromDoctor`
  - `internal/cli/doctor.go:566` undefined: `buildVersionAlignment`
  - `internal/cli/doctor.go:566` undefined: `versionAlignmentSourcesFromDoctor`
- Compile probe, cumulative in plan order (callers before callees): 265 undefined lines total after this step, 217 in surviving files.
- Extract before the cut (manifest `extract`):
  - `liveness.go`: verdict vocabulary (agentLivenessVerdict) as closed seat-state list; cut pane and wake inputs

### C2: runtime control: runtime_actions, runtime_json, runtime_identity, operator_control

- Cut: 4 files, 1,650 lines (manifest `v2_lines`).
- Manifest entries (cluster C2, fate delete):
  - `internal/cli/operator_control.go` (393)
  - `internal/cli/runtime_actions.go` (639)
  - `internal/cli/runtime_identity.go` (164)
  - `internal/cli/runtime_json.go` (454)
- Entry points removed: registry: focus, open, send, broadcast.
- Deadcode (after the registry removal; full output in `docs/v3-spike/2a1-deadcode.txt`): 11 of 56 top-level funcs in the cut files are reported unreachable; the rest are still reachable from the remaining entry points, directly or through other cut files that are themselves still reachable (see the probe lines).

  | file | funcs | unreachable | deadcode.txt lines | still reachable (first 5) |
  | --- | --- | --- | --- | --- |
  | `operator_control.go` | 11 | 4 | 466-469 | runOperatorSend, resolveOperatorControlContext, admitOperatorControl, ensureConfiguredOperatorTargets, splitOperatorRecipients |
  | `runtime_actions.go` | 21 | 5 | 581-585 | resolveMemberRuntime, compareCWD, recordedPaneID, recordedTerminalBackend, nativePromptInjectionDisabledReason |
  | `runtime_identity.go` | 4 | 0 | - | classifyLaunchRuntimeIdentity, launchRecordProcessMatcher, launchRuntimeProbeFromDuplicate, classifyLaunchPIDRuntimeIdentity |
  | `runtime_json.go` | 20 | 2 | 586-587 | tmuxRuntimeFromInfo, terminalRuntimeFromInfo, terminalRuntimeFromTmuxInfo, syncTerminalRuntimeFromTmux, memoizePaneLister |
- Compile probe, this cluster alone (against the registry-removed baseline): 144 undefined lines, 34 distinct symbols; 83 of them sit in files that survive 2a-1, the rest in files other delete clusters remove.
  First errors (surviving files first):
  - `internal/cli/global_status.go:66` undefined: `runtimeActionJSON`
  - `internal/cli/global_status.go:78` undefined: `terminalRuntimeJSON`
  - `internal/cli/global_status.go:131` undefined: `runtimeActionJSON`
  - `internal/cli/global_status.go:144` undefined: `runtimeActionJSON`
  - `internal/cli/global_status.go:158` undefined: `launchRuntimeIdentity`
  - `internal/cli/global_status.go:247` undefined: `runtimeActionJSON`
- Compile probe, cumulative in plan order (callers before callees): 346 undefined lines total after this step, 300 in surviving files.
- Extract before the cut (manifest `extract`):
  - `operator_control.go`: runBroadcast/runOperatorSend if broadcast stays as a verb
  - `runtime_actions.go`: argv+id action shape into status; resolveProjectProfile, compareCWD, sameResolvedDir, readPromptBody generic helpers

### C1: tmux, iTerm2, Terminal, layout, wake, panes, orchestrate, bootstrap_failure, claude_rename, team_member_control_continue

- Cut: 25 files, 5,473 lines (manifest `v2_lines`).
- Manifest entries (cluster C1, fate delete):
  - `internal/cli/bootstrap_failure.go` (402)
  - `internal/cli/claude_rename.go` (143)
  - `internal/cli/layout_finalize.go` (224)
  - `internal/cli/layout_preset.go` (111)
  - `internal/cli/orchestrate.go` (404)
  - `internal/cli/orphan_panes.go` (89)
  - `internal/cli/pane_adopt.go` (78)
  - `internal/cli/pane_cleanup_manifest_other.go` (13)
  - `internal/cli/pane_cleanup_manifest_rename_darwin.go` (9)
  - `internal/cli/pane_cleanup_manifest_rename_linux.go` (9)
  - `internal/cli/pane_cleanup_manifest_unix.go` (324)
  - `internal/cli/pane_cleanup_manifest.go` (138)
  - `internal/cli/pane_cleanup_rm.go` (312)
  - `internal/cli/pane_cleanup.go` (637)
  - `internal/cli/run_shell_silence.go` (31)
  - `internal/cli/run_start_terminal_context.go` (59)
  - `internal/cli/team_launch_iterm2.go` (198)
  - `internal/cli/team_launch_native_terminal.go` (36)
  - `internal/cli/team_launch_terminal.go` (175)
  - `internal/cli/team_launch_tmux_session.go` (417)
  - `internal/cli/team_launch_tmux.go` (1096)
  - `internal/cli/team_member_control_continue.go` (328)
  - `internal/cli/tmux_exact_id.go` (27)
  - `internal/cli/wake_inject.go` (42)
  - `internal/cli/wake_lock_compat.go` (171)
- Entry points removed: lookupCommand hook: claude rename helper (claudeRenameHelperCommand); agent/start/resume/down reach the rest.
- Deadcode (after the registry removal; full output in `docs/v3-spike/2a1-deadcode.txt`): 60 of 204 top-level funcs in the cut files are reported unreachable; the rest are still reachable from the remaining entry points, directly or through other cut files that are themselves still reachable (see the probe lines).

  | file | funcs | unreachable | deadcode.txt lines | still reachable (first 5) |
  | --- | --- | --- | --- | --- |
  | `bootstrap_failure.go` | 8 | 2 | 88-89 | paneBootstrapFailure, bootstrapFailureError, printBootstrapFailureRecovery, paneIsAtShell, tmuxBootstrapProbes |
  | `claude_rename.go` | 5 | 5 | 99-103 | none |
  | `layout_finalize.go` | 8 | 7 | 340-346 | layoutFinalizationWarningPath |
  | `layout_preset.go` | 3 | 3 | 347-349 | none |
  | `orchestrate.go` | 10 | 10 | 474-483 | none |
  | `orphan_panes.go` | 4 | 0 | - | liveLaunchPaneTokens, findOrphanPanes, launchPaneKey, parsePaneTitleToken |
  | `pane_adopt.go` | 3 | 0 | - | verifiedAgentPID, childrenPidTree, adoptLivePane |
  | `pane_cleanup_manifest_other.go` | 2 | 0 | - | Prepare, Finalize |
  | `pane_cleanup_manifest_rename_darwin.go` | 1 | 0 | - | paneManifestRenameNoReplace |
  | `pane_cleanup_manifest_rename_linux.go` | 1 | 0 | - | paneManifestRenameNoReplace |
  | `pane_cleanup_manifest_unix.go` | 8 | 8 | 489-496 | none |
  | `pane_cleanup_manifest.go` | 4 | 4 | 485-488 | none |
  | `pane_cleanup_rm.go` | 14 | 13 | 497-509 | addPaneOutcomeSummary |
  | `pane_cleanup.go` | 23 | 1 | 484-484 | withDefaults, PreparePaneCleanup, prepareGoneAgentPaneCleanup, recordedPaneAffirmativelyGone, paneProcessOrDescendant |
  | `run_shell_silence.go` | 2 | 0 | - | runShellLogPath, silentRunShellPayload |
  | `run_start_terminal_context.go` | 2 | 2 | 579-580 | none |
  | `team_launch_iterm2.go` | 12 | 2 | 680-681 | init, Name, Validate, DryRun, Launch |
  | `team_launch_native_terminal.go` | 3 | 0 | - | nativeTerminalWindowName, nativeTerminalLaunchPayload, posixSingleQuote |
  | `team_launch_terminal.go` | 10 | 0 | - | init, Name, Validate, DryRun, Launch |
  | `team_launch_tmux_session.go` | 16 | 0 | - | init, Name, Validate, DryRun, Launch |
  | `team_launch_tmux.go` | 47 | 3 | 682-684 | init, Name, Validate, DryRun, Launch |
  | `team_member_control_continue.go` | 7 | 0 | - | runTeamMemberControlContinue, resolveExactTmuxControlContinue, listExactTmuxControlClients, readExactTmuxControlClients, parseExactTmuxControlClients |
  | `tmux_exact_id.go` | 3 | 0 | - | exactTmuxPaneID, exactTmuxWindowID, exactTmuxID |
  | `wake_inject.go` | 3 | 0 | - | normalizeWakeInjectMode, resolveWakeInjectModeForBinary, validateWakeInjectConfig |
  | `wake_lock_compat.go` | 5 | 0 | - | decodeWakeLockFile, validateWakeLockStateBinding, wakeLockSameMachine, wakeLockHasStateBinding, wakeLockHasOwnerBinding |
- Compile probe, this cluster alone (against the registry-removed baseline): 129 undefined lines, 70 distinct symbols; 61 of them sit in files that survive 2a-1, the rest in files other delete clusters remove.
  First errors (surviving files first):
  - `internal/cli/session_reset_support.go:92` undefined: `PaneCleanupDependencies`
  - `internal/cli/session_reset_support.go:93` undefined: `paneCleanupManifestStore`
  - `internal/cli/session_reset_support.go:95` undefined: `rmPaneWork`
  - `internal/cli/session_reset_support.go:347` undefined: `rmPaneWork`
  - `internal/cli/session_reset_support.go:351` undefined: `snapshotRmPaneWork`
  - `internal/cli/session_reset_support.go:360` undefined: `newPaneCleanupOperationID`
- Compile probe, cumulative in plan order (callers before callees): 361 undefined lines total after this step, 361 in surviving files.
- Extract before the cut: none listed in the manifest.

### C26: profiles.go (fate delete inside shrink cluster C26)

- Cut: 1 files, 144 lines (manifest `v2_lines`).
- Manifest entries (cluster C26, fate delete):
  - `internal/cli/profiles.go` (144)
- Entry points removed: none removed.
- Deadcode (after the registry removal; full output in `docs/v3-spike/2a1-deadcode.txt`): 0 of 3 top-level funcs in the cut files are reported unreachable; the rest are still reachable from the remaining entry points, directly or through other cut files that are themselves still reachable (see the probe lines).

  | file | funcs | unreachable | deadcode.txt lines | still reachable (first 5) |
  | --- | --- | --- | --- | --- |
  | `profiles.go` | 3 | 0 | - | resolveProfileFlag, runTeamProfiles, profileDisplayWorkstream |
- Compile probe, this cluster alone (against the registry-removed baseline): 8 undefined lines, 2 distinct symbols; 3 of them sit in files that survive 2a-1, the rest in files other delete clusters remove.
  First errors (surviving files first):
  - `internal/cli/context_resolution.go:419` undefined: `resolveProfileFlag`
  - `internal/cli/team.go:63` undefined: `runTeamProfiles`
  - `internal/cli/team.go:286` undefined: `resolveProfileFlag`
- Compile probe, cumulative in plan order (callers before callees): 364 undefined lines total after this step, 364 in surviving files.
- Extract before the cut (manifest `extract`):
  - `context_resolution.go`: resolveScopedCommandContext (:94)

### C23: agent.go (fate delete inside shrink cluster C23)

- Cut: 1 files, 339 lines (manifest `v2_lines`).
- Manifest entries (cluster C23, fate delete):
  - `internal/cli/agent.go` (339)
- Entry points removed: registry: agent (runAgent, agent.go).
- Deadcode (after the registry removal; full output in `docs/v3-spike/2a1-deadcode.txt`): 8 of 8 top-level funcs in the cut files are reported unreachable; the rest are still reachable from the remaining entry points, directly or through other cut files that are themselves still reachable (see the probe lines).

  | file | funcs | unreachable | deadcode.txt lines | still reachable (first 5) |
  | --- | --- | --- | --- | --- |
  | `agent.go` | 8 | 8 | 37-44 | none |
- Compile probe, this cluster alone (against the registry-removed baseline): 0 undefined lines, 0 distinct symbols; 0 of them sit in files that survive 2a-1, the rest in files other delete clusters remove.
  Build is green after the cut (0 undefined symbols).
- Compile probe, cumulative in plan order (callers before callees): 364 undefined lines total after this step, 364 in surviving files.
- Extract before the cut (manifest `extract`):
  - `agent_defaults.go`: claudeInScopePreauthAllowlist (:42)

### C20: collect_* lock and support files (fate delete; evidence.go itself is fate replace and is NOT cut here)

- Cut: 4 files, 690 lines (manifest `v2_lines`).
- Manifest entries (cluster C20, fate delete):
  - `internal/cli/collect_lock_exclusive.go` (59)
  - `internal/cli/collect_lock_unix.go` (51)
  - `internal/cli/collect_lock_windows.go` (9)
  - `internal/cli/collect_support.go` (571)
- Entry points removed: none removed; evidence verb is fate replace and stays until the runner exists.
- Deadcode (after the registry removal; full output in `docs/v3-spike/2a1-deadcode.txt`): 30 of 31 top-level funcs in the cut files are reported unreachable; the rest are still reachable from the remaining entry points, directly or through other cut files that are themselves still reachable (see the probe lines).

  | file | funcs | unreachable | deadcode.txt lines | still reachable (first 5) |
  | --- | --- | --- | --- | --- |
  | `collect_lock_exclusive.go` | 1 | 1 | 104-104 | none |
  | `collect_lock_unix.go` | 1 | 1 | 105-105 | none |
  | `collect_lock_windows.go` | 1 | 0 | - | lockCollectJournalContext |
  | `collect_support.go` | 28 | 28 | 106-133 | none |
- Compile probe, this cluster alone (against the registry-removed baseline): 2 undefined lines, 2 distinct symbols; 0 of them sit in files that survive 2a-1, the rest in files other delete clusters remove.
  First errors (surviving files first):
  - `internal/cli/notification_amq_watch.go:142` undefined: `isCollectWatchTimeout`
  - `internal/cli/notification_amq_watch.go:183` undefined: `executeCollectDrainContext`
- Compile probe, cumulative in plan order (callers before callees): 364 undefined lines total after this step, 364 in surviving files.
- Extract before the cut (manifest `extract`):
  - `evidence.go`: evidenceSecretName (:747), evidenceTaskLink (:561) semantics for the ledger

### C24: completion

- Cut: 1 files, 996 lines (manifest `v2_lines`).
- Manifest entries (cluster C24, fate delete):
  - `internal/cli/completion.go` (996)
- Entry points removed: registry: completion.
- Deadcode (after the registry removal; full output in `docs/v3-spike/2a1-deadcode.txt`): 1 of 6 top-level funcs in the cut files are reported unreachable; the rest are still reachable from the remaining entry points, directly or through other cut files that are themselves still reachable (see the probe lines).

  | file | funcs | unreachable | deadcode.txt lines | still reachable (first 5) |
  | --- | --- | --- | --- | --- |
  | `completion.go` | 6 | 1 | 134-134 | buildBashCompletionScript, buildZshCompletionScript, buildFishCompletionScript, zshQuote, fishQuote |
- Compile probe, this cluster alone (against the registry-removed baseline): 0 undefined lines, 0 distinct symbols; 0 of them sit in files that survive 2a-1, the rest in files other delete clusters remove.
  Build is green after the cut (0 undefined symbols).
- Compile probe, cumulative in plan order (callers before callees): 364 undefined lines total after this step, 364 in surviving files.
- Extract before the cut: none listed in the manifest.


## 7. Packages (plan 4.1 step 4)

Importer data from a repo-wide import grep (non-test files, after excluding the package itself); "surviving" means importer files that are not themselves fate delete, i.e. 2a-2 work. Test files that import these packages (tests: tmuxpane 33, runtimecontrol 5, liveidentity 4, notifier 2, bootstrapack 2, wizard 1, act 1, commandevidence 1) are deleted or rewritten with them.

Order in which packages become importer-free:

1. console, tmuxharness: already importer-free (step 1).
2. act: after C2 (`operator_control.go`) and console. Zero surviving importers.
3. autonomy: after C7 (`namespace_migration_plan.go`). Zero surviving importers.
4. wizard: after C5 (`resume_goal.go`) and C1 (`run_start_terminal_context.go`). Zero surviving importers. Take the `routing.go` extract first.
5. runtimeaction: after C2, C5, C7, console. One surviving importer, `global_status.go` (C3 shrink, 2a-2).
6. runtimecontrol: after runtimeaction, C1, C2. Surviving: `liveness.go` (C10), `status.go` (C16).
7. liveidentity: after C1, C2, C10 delete files. Surviving: `status.go` (C16).
8. notifier: surviving `operator_attention.go` (C17). Needs the 2a-2 cut first.
9. bootstrapack: surviving `internal/launch/record.go` and `bootstrap.go` (C12). Owner default is delete.
10. tmuxpane (next to last): seven surviving importers (operator_attention, dispatch, launch, team_launch, global_noc, status, doctor); 33 test files.
11. commandevidence (last): only `doc.go` (7 lines) is delete-fate; `evidence.go` is fate replace, importer `cli/evidence.go` and `task/command_evidence.go` remain until the runner exists. `subject.go` is kept for the guard.

Package cuts that have a surviving importer cannot land in 2a-1 as pure file deletions; they land when their 2a-2 importer cut lands. That covers runtimeaction, runtimecontrol, liveidentity, notifier, bootstrapack, tmuxpane, commandevidence (and attention's dedup.go is independent). console, tmuxharness, act, autonomy and wizard can land inside 2a-1.

### P: internal/console

- Cut: 11 files, 3,626 lines; becomes importer-free: step 1: orphan, no importers (confirmed).
- Manifest entries: `actions.go` (115), `attach.go` (133), `control.go` (346), `filter.go` (269), `labels.go` (277), `model.go` (255), `rows.go` (422), `run.go` (332), `update.go` (330), `view.go` (969), `watcher.go` (178)
- Entry points removed: none needed (library package); it is cut when `go list` shows no non-test importer. Surviving importers at that point: none.
- Deadcode: not applicable; the package is not linked into ./cmd/amq-squad at all (157 funcs), so deadcode never sees it. Confirmed orphan by `go list -deps ./cmd/amq-squad | grep` (no match) and by a repo-wide import grep (zero importers, tests included).
- Compile probe: the package is not part of ./internal/cli; probe is `go build ./...` after its importers are cut. Build breaks only at the importer files listed above.
- Extract before the cut: none listed in the manifest.

### P: internal/tmuxharness

- Cut: 1 files, 485 lines; becomes importer-free: step 1: orphan, no importers (confirmed).
- Manifest entries: `harness.go` (485)
- Entry points removed: none needed (library package); it is cut when `go list` shows no non-test importer. Surviving importers at that point: none.
- Deadcode: not applicable; the package is not linked into ./cmd/amq-squad at all (18 funcs), so deadcode never sees it. Confirmed orphan by `go list -deps ./cmd/amq-squad | grep` (no match) and by a repo-wide import grep (zero importers, tests included).
- Compile probe: the package is not part of ./internal/cli; probe is `go build ./...` after its importers are cut. Build breaks only at the importer files listed above.
- Extract before the cut: none listed in the manifest.

### P: internal/act

- Cut: 1 files, 400 lines; becomes importer-free: after C2 (operator_control.go) and console.
- Manifest entries: `control.go` (400)
- Entry points removed: none needed (library package); it is cut when `go list` shows no non-test importer. Surviving importers at that point: none surviving.
- Deadcode: 12 of 19 funcs in the package unreachable (deadcode.txt lines 1-12).
- Compile probe: the package is not part of ./internal/cli; probe is `go build ./...` after its importers are cut. Build breaks only at the importer files listed above.
- Extract before the cut: none listed in the manifest.

### P: internal/autonomy

- Cut: 1 files, 283 lines; becomes importer-free: after C7 (namespace_migration_plan.go).
- Manifest entries: `autonomy.go` (283)
- Entry points removed: none needed (library package); it is cut when `go list` shows no non-test importer. Surviving importers at that point: none surviving.
- Deadcode: 12 of 12 funcs in the package unreachable (deadcode.txt lines 17-28).
- Compile probe: the package is not part of ./internal/cli; probe is `go build ./...` after its importers are cut. Build breaks only at the importer files listed above.
- Extract before the cut: none listed in the manifest.

### P: internal/wizard

- Cut: 13 files, 4,962 lines; becomes importer-free: after C5 (resume_goal.go) and C1 (run_start_terminal_context.go); also leaves the module graph for bubbletea/bubbles/lipgloss.
- Manifest entries: `bubbletea.go` (1942), `capabilities.go` (100), `composition_policy.go` (39), `discovery.go` (249), `goal_binding.go` (98), `launch_shape.go` (90), `numbered.go` (1018), `previews.go` (32), `profile_flow.go` (468), `routing.go` (308), `spec.go` (453), `terminal_context.go` (125), `tool_policy.go` (40)
- Entry points removed: none needed (library package); it is cut when `go list` shows no non-test importer. Surviving importers at that point: none surviving.
- Deadcode: 150 of 150 funcs in the package unreachable (deadcode.txt lines 849-998).
- Compile probe: the package is not part of ./internal/cli; probe is `go build ./...` after its importers are cut. Build breaks only at the importer files listed above.
- Extract before the cut: routing table ancestor: move to its 3.0 home before deletion

### P: internal/runtimeaction

- Cut: 1 files, 154 lines; becomes importer-free: after C2, C5, C7 and console; one surviving importer: global_noc/global_status.go (C3 shrink).
- Manifest entries: `actions.go` (154)
- Entry points removed: none needed (library package); it is cut when `go list` shows no non-test importer. Surviving importers at that point: internal/cli/global_status.go.
- Deadcode: 2 of 9 funcs in the package unreachable (deadcode.txt lines 797-798).
- Compile probe: the package is not part of ./internal/cli; probe is `go build ./...` after its importers are cut. Build breaks only at the importer files listed above.
- Extract before the cut: argv plus id action shape into the status contract first

### P: internal/runtimecontrol

- Cut: 3 files, 551 lines; becomes importer-free: after runtimeaction and C1/C2; surviving importers: liveness.go (C10), status.go (C16).
- Manifest entries: `capabilities.go` (237), `context.go` (222), `controller.go` (92)
- Entry points removed: none needed (library package); it is cut when `go list` shows no non-test importer. Surviving importers at that point: internal/cli/liveness.go, internal/cli/status.go.
- Deadcode: 1 of 33 funcs in the package unreachable (deadcode.txt lines 799-799).
- Compile probe: the package is not part of ./internal/cli; probe is `go build ./...` after its importers are cut. Build breaks only at the importer files listed above.
- Extract before the cut: none listed in the manifest.

### P: internal/liveidentity

- Cut: 1 files, 257 lines; becomes importer-free: after C1, C2, C10; surviving importer: status.go (C16).
- Manifest entries: `identity.go` (257)
- Entry points removed: none needed (library package); it is cut when `go list` shows no non-test importer. Surviving importers at that point: internal/cli/status.go.
- Deadcode: 4 of 5 funcs in the package unreachable (deadcode.txt lines 774-777).
- Compile probe: the package is not part of ./internal/cli; probe is `go build ./...` after its importers are cut. Build breaks only at the importer files listed above.
- Extract before the cut: none listed in the manifest.

### P: internal/notifier

- Cut: 4 files, 155 lines; becomes importer-free: surviving importer: operator_attention.go (C17), cut in 2a-2.
- Manifest entries: `command.go` (51), `desktop_darwin.go` (43), `desktop_other.go` (20), `sink.go` (41)
- Entry points removed: none needed (library package); it is cut when `go list` shows no non-test importer. Surviving importers at that point: internal/cli/operator_attention.go.
- Deadcode: 3 of 9 funcs in the package unreachable (deadcode.txt lines 778-780).
- Compile probe: the package is not part of ./internal/cli; probe is `go build ./...` after its importers are cut. Build breaks only at the importer files listed above.
- Extract before the cut: none listed in the manifest.

### P: internal/bootstrapack

- Cut: 1 files, 246 lines; becomes importer-free: surviving importers: internal/launch/record.go (shrink), bootstrap.go (C12 shrink).
- Manifest entries: `bootstrapack.go` (246)
- Entry points removed: none needed (library package); it is cut when `go list` shows no non-test importer. Surviving importers at that point: internal/launch/record.go, internal/cli/bootstrap.go.
- Deadcode: 8 of 8 funcs in the package unreachable (deadcode.txt lines 29-36).
- Compile probe: the package is not part of ./internal/cli; probe is `go build ./...` after its importers are cut. Build breaks only at the importer files listed above.
- Extract before the cut: none listed in the manifest.

### P: internal/tmuxpane

- Cut: 4 files, 1,686 lines; becomes importer-free: last but one: seven surviving importers (operator_attention, dispatch, launch, team_launch, global_noc, status, doctor).
- Manifest entries: `busy.go` (64), `deliver.go` (389), `local_input.go` (118), `tmux.go` (1115)
- Entry points removed: none needed (library package); it is cut when `go list` shows no non-test importer. Surviving importers at that point: operator_attention.go, dispatch.go, launch.go, team_launch.go, global_noc.go, status.go, doctor.go.
- Deadcode: 10 of 66 funcs in the package unreachable (deadcode.txt lines 837-846).
- Compile probe: the package is not part of ./internal/cli; probe is `go build ./...` after its importers are cut. Build breaks only at the importer files listed above.
- Extract before the cut: none listed in the manifest.

### P: internal/commandevidence

- Cut: 1 files, 7 lines; becomes importer-free: last: only the one delete-fate file (7 lines) can go; evidence.go is fate replace, importers evidence.go (C20) and task/command_evidence.go remain.
- Manifest entries: `doc.go` (7)
- Entry points removed: none needed (library package); it is cut when `go list` shows no non-test importer. Surviving importers at that point: internal/cli/evidence.go, internal/task/command_evidence.go.
- Deadcode: 0 of 80 funcs in the package unreachable.
- Compile probe: the package is not part of ./internal/cli; probe is `go build ./...` after its importers are cut. Build breaks only at the importer files listed above.
- Extract before the cut: redaction rules before deletion; whole file ported into guard

### P: internal/attention

- Cut: 1 files, 119 lines; becomes importer-free: package is shrink; only the delete-fate file goes whole-file.
- Manifest entries: `dedup.go` (119)
- Entry points removed: none needed (library package); it is cut when `go list` shows no non-test importer. Surviving importers at that point: see manifest.
- Deadcode: 3 of 9 funcs in the package unreachable (deadcode.txt lines 14-16).
- Compile probe: the package is not part of ./internal/cli; probe is `go build ./...` after its importers are cut. Build breaks only at the importer files listed above.
- Extract before the cut: none listed in the manifest.


## 8. Surviving call-site register (what 2a-2 inherits)

After all 138 whole-file deletions (cumulative state), `go build -gcflags=-e ./internal/cli` reports 364 undefined lines, 194 distinct symbols, in 36 files. None is in a file that 2a-1 deletes. 359 are in shrink files, 3 in keep files (`mutation_json.go`, `cli.go`, `gate_close.go`), 2 in the review file (`execution_mode.go`). These are the lines the planner should budget for the in-file cuts; the 4.3 targets already assume most of them.

| undefined lines | distinct symbols | file | cluster | fate |
| --- | --- | --- | --- | --- |
| 82 | 58 | `internal/cli/status.go` | C16 | shrink |
| 33 | 30 | `internal/cli/session_reset_support.go` | C11 | shrink |
| 30 | 24 | `internal/cli/team_launch.go` | C18 | shrink |
| 29 | 24 | `internal/cli/team.go` | C18 | shrink |
| 21 | 12 | `internal/cli/dispatch.go` | C19 | shrink |
| 20 | 8 | `internal/cli/global_status.go` | C3 | shrink |
| 16 | 13 | `internal/cli/doctor.go` | C15 | shrink |
| 16 | 6 | `internal/cli/namespace_migration_guard.go` | C7 | shrink |
| 16 | 5 | `internal/cli/task.go` | C19 | shrink |
| 13 | 13 | `internal/cli/launch.go` | C28 | shrink |
| 11 | 8 | `internal/cli/operator.go` | C17 | shrink |
| 9 | 4 | `internal/cli/team_launch_launchapi.go` | C18 | shrink |
| 7 | 5 | `internal/cli/bootstrap.go` | C12 | shrink |
| 6 | 3 | `internal/cli/context_resolution.go` | C26 | shrink |
| 6 | 5 | `internal/cli/global_noc.go` | C3 | shrink |
| 6 | 5 | `internal/cli/operator_identity.go` | C17 | shrink |
| 5 | 4 | `internal/cli/liveness.go` | C10 | shrink |
| 5 | 5 | `internal/cli/brief.go` | C27 | shrink |
| 5 | 3 | `internal/cli/preflight.go` | C12 | shrink |
| 4 | 4 | `internal/cli/amq.go` | C14 | shrink |
| 4 | 3 | `internal/cli/restore.go` | C11 | shrink |
| 3 | 1 | `internal/cli/amq_env.go` | C14 | shrink |
| 2 | 1 | `internal/cli/execution_mode.go` | C25 | review |
| 2 | 2 | `internal/cli/team_rules_draft.go` | C18 | shrink |
| 2 | 2 | `internal/cli/role_draft.go` | C23 | shrink |
| 1 | 1 | `internal/cli/mutation_json.go` | C29 | keep |
| 1 | 1 | `internal/cli/target_root.go` | C26 | shrink |
| 1 | 1 | `internal/cli/amq_root_authority.go` | C13 | shrink |
| 1 | 1 | `internal/cli/cli.go` | C29 | keep |
| 1 | 1 | `internal/cli/effort.go` | C23 | shrink |
| 1 | 1 | `internal/cli/gate_close.go` | C21 | keep |
| 1 | 1 | `internal/cli/init.go` | C4 | shrink |
| 1 | 1 | `internal/cli/model_defaults.go` | C23 | shrink |
| 1 | 1 | `internal/cli/pathnorm.go` | C26 | shrink |
| 1 | 1 | `internal/cli/team_operator.go` | C17 | shrink |
| 1 | 1 | `internal/cli/workstream.go` | C27 | shrink |

Keep-fate or review-fate files that lose a symbol, so something a delete cluster owns is still used by a kept file; port these before the cut: `cli.go:169` runStatusBoard (status_board.go, C3), `gate_close.go:112` ensureNoNamespaceConflict (namespace_conflict.go, C7), `mutation_json.go:43` memberFieldChange (team_member_diff.go, C18), `execution_mode.go:393,440` goalRosterMember (goal.go, C5).

## 9. Not whole-file cuts in 2a-1

- `internal/cli/evidence.go` (774, C20) and `internal/commandevidence/evidence.go` (1,657): fate replace. The runner/ingest verb must exist first; port redaction rules and `evidenceSecretName`, `evidenceTaskLink` first. Their combined 2,431 lines are not in the totals below.
- All host files in section 4.
- Everything else with fate shrink, keep, or review.

## 10. Test files

Whole-file deletion also removes test files. 104 test files (37,604 lines) live in the deleted packages or share a basename with a deleted file; they go in the same commits. This is not counted in the totals below (manifest counts non-test lines). Tests with other names that reference deleted symbols in `internal/cli` will fail to compile; expect a second sweep per cut, found by `go vet ./...` after each commit.

## 11. Totals

| | lines |
| --- | --- |
| manifest total, v2 non-test lines under `internal/` | 110,929 |
| removed by 2a-1 whole-file/whole-package deletion (138 files, hosts excluded, replace-fate excluded) | 49,894 |
| of which internal/cli (95 files) | 36,963 |
| of which non-cli packages (13 package cuts, 43 files incl. the two orphans) | 12,931 |
| expected remaining after 2a-1 | 61,035 |
| in-file 2a-2 cuts still to make (61,035 minus 33,504) | 27,531 |
| manifest target after 2a | 33,504 (script tolerance 33,100 plus or minus 10 percent) |

Check against plan 4.3: "Whole-file deletion alone leaves roughly 55k to 60k". Measured: 61,035, slightly above the range because the replace-fate files (2,431 lines) and eight host files (4,114 v2 lines, 1,100 target) are retained.

Cut count: 17 internal/cli clusters with whole-file cuts (10 task-listed delete clusters plus 7 extras; C28 contributes none), plus 13 package cuts = 30 cuts, plus the C28 host-only entry listed for completeness = 31 entries.

Packages leaving the module graph. Experiment: delete all 138 files plus the console, wizard, tmuxharness directories, then `go mod tidy -e` (tidy errors on cli tests that still import wizard, so this is the lower bound; reverted afterwards):
- direct requires dropped: `github.com/charmbracelet/bubbletea v1.3.10`, `github.com/charmbracelet/bubbles v1.0.0`, `github.com/charmbracelet/lipgloss v1.1.0`.
- indirect requires dropped: atotto/clipboard, aymanbagabas/go-osc52/v2, charmbracelet/colorprofile, x/ansi, x/cellbuf, x/term, clipperhouse/displaywidth, stringish, uax29/v2, erikgeiser/coninput, lucasb-eyer/go-colorful, mattn/go-isatty, go-localereader, go-runewidth, muesli/ansi, cancelreader, termenv, rivo/uniseg, xo/terminfo, golang.org/x/text v0.14.0 (20 entries); `go.sum` shrinks by 52 diff lines.
- Only `internal/wizard` and `internal/console` import charmbracelet (13 files, tests included), so both must be gone for the three direct dependencies to leave; `go mod tidy` (plan step 6) is the closing step.

## 12. Decisions for the planner

1. Branch name. `v3/2a1-worklist` cannot coexist with branch `v3`; I used `v3-2a1-worklist`. Pick the final name before merge.
2. Extra delete-fate files (7 clusters, about 7,300 lines) were not on the delete-cluster list but are whole-file deletions per the manifest; confirm they belong to 2a-1 rather than the shrink commit for their cluster.
3. Package cuts with surviving importers (runtimeaction through commandevidence) cannot be pure file deletions in 2a-1; accept deferring them to land with their 2a-2 importer cut, or move the importer cuts forward.
4. `bootstrapack` is still imported by `internal/launch/record.go`; the owner default (delete) needs an explicit 2a-2 change in the launch record.
5. Confirm the `namespace_rename_*` stubs go with C7: only the darwin file could be probed here, and the migration tx was its only caller on darwin. Linux and other builds need a `GOOS=linux go build` check.
6. Whether the hidden `agent` entry point (`runAgent`, `agent.go`, C23) is removed in the C11 commit (as done in this experiment) or stays until seat-exec replaces it; removing it is what makes `launch.go` go mostly dead.

## 13. Planner rulings (2026-10-22, lead)

1. Branch `v3-2a1`. 2. The seven extra delete-fate clusters are 2a-1 cuts. 3. A package with a surviving non-test importer after all file cuts is deferred to 2a-2 with its importer named. 4. `bootstrapack` deferred the same way (importer `internal/launch/record.go`). 5. The `namespace_rename_*` stubs and `namespace_open_other.go` go with C7; the C7 commit also passes `GOOS=linux go build ./...` and `GOOS=windows go build ./...`. 6. The hidden `agent` entry point goes in the C11 cut. Consequence of 6: from the C11 cut until `seat-exec` lands in 2b, the v3 binary cannot launch seats; every run in that window uses the installed 2.31 binary. Not a bug; do not file it.

