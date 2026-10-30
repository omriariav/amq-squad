# amq-squad 3.0 keep/delete manifest pass (read-only research, 2026-10-20)

Method: `go list` import graph; per-file skim of internal/cli; and a mechanical
compile probe. For each cli cluster I removed its files (and their tests) in a
scratch copy of the repo and ran `go build -gcflags=-e ./internal/cli`. The
"kept code still references" lists below come from the undefined-symbol output,
so they are measured, not guessed. Line counts are non-test. File:line cites are
against the tree on branch docs/v3-plan (bbb6bb3). The scratch copy is under
`.../scratchpad/copy`; the repo was not touched.

## 1. Dependency graph (internal to internal)

Edges: `X -> Y` means X imports Y. Only edges that matter are listed. Leaves
(no internal imports): activity, agentcatalog, amqexec, bootstrapack, catalog,
drafter, flock, launchintent, liveidentity, operatorauth, procinfo, reviewrebind,
role, rules, runtimecontrol, tmuxharness.

```
cli      -> 33 of 34 packages (not console, not tmuxharness)
state    -> activity launch namespace operatorauth procinfo task
task     -> commandevidence flock namespace team
team     -> drafter flock operatorauth
namespace-> team                       (cycle-free: team does not import namespace)
compoundrelease -> flock namespace operatorauth state team
commandevidence -> namespace team
worktreeplan    -> flock namespace team
launch   -> bootstrapack flock
adoptionseam -> launchintent
tmuxpane -> state     attention -> state     notifier -> attention
act -> amqexec state  runtimeaction -> namespace runtimecontrol
autonomy -> team      userconfig -> drafter  wizard -> agentcatalog catalog
console  -> act runtimeaction state
```

Importer counts (who depends on it), highest first:

| pkg | importers | who |
| --- | --- | --- |
| team | 7 | autonomy cli commandevidence compoundrelease namespace task worktreeplan |
| namespace | 7 | cli commandevidence compoundrelease runtimeaction state task worktreeplan |
| state | 6 | act attention cli compoundrelease console tmuxpane |
| flock | 6 | cli compoundrelease launch task team worktreeplan |
| operatorauth | 4 | cli compoundrelease state team |
| drafter | 3 | cli team userconfig |
| 2 each | | amqexec, launchintent, act, activity, agentcatalog, attention, bootstrapack, catalog, commandevidence, launch, procinfo, runtimeaction, runtimecontrol, task |

Leaf utilities that keepers depend on (must stay or be inlined):
- `flock` (233): 6 importers incl. task, team, launch, worktreeplan, compoundrelease. Keep. `flock/flock.go:25` Exclusive.
- `namespace` (120): 7 importers and 34 cli files. `namespace.go:16` Ref, `:55` Resolve, `:96` TasksPath, `:109` AMQRoot. Keep as is; it is the path scheme for squad/session roots. Note it imports `team` only for profile normalisation; with squad.yaml that import can go.
- `team` (1689): 7 importers and 56 cli files. Keep but rewrite around squad.yaml. It is the highest fan-in node, so 2a must not delete from it, only 2b rewrites it. `team.go:53` Member, `:601` Team, `:870` Read, `:1091` Validate.
- `operatorauth` (2033): signing and gates. `envelope.go:91` AuthorizationEnvelope, `:285` VerifyAuthorizationEnvelope. Keep.
- `state` (3444): the future classifier base. Imports `launch`, `procinfo`, `activity`; those three must stay in some form while state stays.
- `drafter`: pulled by team (`team` holds drafter config in the profile, schema 6). Keeps drafter alive until squad.yaml replaces the profile; otherwise inline-able.
- `procinfo`: used by state, context_resolution, operator_identity, preflight (liveness of PIDs). Keep `procinfo.go:57` Alive, `:45` StartTime, `:75` Match; TTY/ChildrenIndex (`:35`, `:93`) are pane-only and go.
- `launch` (704): launch.json record, read by state and 15 cli files. Keep as a much smaller record (see table).

Orphans, importable by nobody (free to delete first, zero edits elsewhere):
- `internal/console` (3626): no importer anywhere, not even cli (cli has its own global_noc.go/status_board.go). Dead code today.
- `internal/tmuxharness` (485): no importer in non-test or test code outside itself.

Packages that become dead once their cli callers go: act (only cli/operator_control.go + console), runtimeaction (cli global_status, goal_supervision, namespace_conflict, runtime_json + console), runtimecontrol (cli liveness, runtime_actions, runtime_json, run_start_terminal_context, team_launch_terminal, team_launch_iterm2, status + runtimeaction), wizard (cli run_start_terminal_context, resume_goal only), notifier (cli operator_attention only), liveidentity (cli live_identity, runtime_json, status, team_member_control_continue), autonomy (cli namespace_migration_plan only), bootstrapack (launch + cli bootstrap/team_lead), commandevidence (cli evidence.go + task), tmuxpane (still used by 6 cli files that are NOT deleted: dispatch, doctor, global_noc, operator_attention, status, team_launch; those need tmux cuts first).

## 2. Non-cli packages

Target = my estimate of lines after 2a (mechanical deletion plus function-level cuts), not after later feature work. "Plan" = fate the plan names.

| package | v2 | fate | 3.0 | reason and evidence |
| --- | --- | --- | --- | --- |
| act | 400 | delete | 0 | Builds `amq` reply/approve argv for NOC controls (`act/control.go:71` Reply, `:82` Approve). Importers: cli/operator_control.go, console. amq-noc owns controls now (plan 4.7). |
| activity | 263 | shrink | 200 | Per-seat activity file feeding seat state (`activity.go:60` Path, `:170` Read, `:188` Write). Used by state and cli status/task. Keep Normalize/Read/Write; drop SymphonySnapshot (`:121`). |
| adoptionseam | 298 | keep | 250 | The launchapi Prepare/Apply seam (`adoptionseam.go:86` Prepare, `:137` Apply; `floor.go:87` NegotiateAdoptionFloor). It is the launch compiler's front door (plan 4.3). Plan names it. |
| agentcatalog | 174 | keep | 174 | Built-in runtime/model/effort tiers (`catalog.go:52` Builtins, `:129` Merge). Seed for the registry (R10) and the routing tiers. |
| amqexec | 32 | keep | 32 | `env.go:17` NoUpdateCheckEnv. Used by cli amq*, act. |
| attention | 169 | shrink | 50 | Keep `event.go:10` Event + key builders (`:33-46`: GateKey, SelfApprovedKey), the stable-id basis for `attention[]` in the status contract. Drop `dedup.go` (Select/Commit: push-notification renotify logic, `:33`/`:80`). |
| autonomy | 283 | delete | 0 | `AuthorizeSpawn/Prune` (`autonomy.go:69,74`) on team.AutonomousPolicy. Replaced by the grants file `~/.amq-squad/grants.yaml` (R16). Only caller: cli/namespace_migration_plan.go. |
| bootstrapack | 246 | delete | 0 | Marker that a seat acked its bootstrap prompt (`bootstrapack.go:81` Write, `:149` Read). In 3.0 hooks inject every turn; Codex gets launch-time only and cannot ack. Callers: launch pkg, cli bootstrap/team_lead. Owner may revisit for Codex. |
| catalog | 245 | keep | 245 | Role catalog (`catalog.go:21` Role, `:164` All, `:171` Lookup). Plan: role catalog stays. |
| commandevidence | 2069 | replace | 0 | `evidence.go:258` Store.Run executes a command and journals it. The ledger is hook-fed ingest, not a runner. Port first: redaction/secret policy (`TestRedactionAndSecretPolicy`, evidence.go) and `subject.go:46` ResolveCommandSubject if guard needs target digests. Callers: cli/evidence.go, `task/command_evidence.go:23`. Plan 16.1 names it. |
| compoundrelease | 3499 | keep | 3200 | Release gate flow (`guard.go:18` InvocationGuard, `store.go:110` Open). Plan 16.1: stays. `store.go` (1515) and `reconcile.go` (494) get a line-by-line review for v2 compat and tmux/pane references. Imports state, team, namespace, operatorauth, flock, so it pins those keepers. |
| console | 3626 | delete | 0 | Bubbletea fleet console, `model.go`, `view.go:969`. Zero importers. Drops bubbletea, bubbles, lipgloss, fsnotify from go.mod once cli/wizard are also gone. |
| drafter | 586 | shrink | 450 | Brief drafting stays as an off-path verb (plan 10). `config.go:40` Config, `:99` Resolve; `run.go` runs the backend. Cut fallback modes you do not need. |
| flock | 233 | keep | 233 | Leaf, 6 importers (`flock.go:25`). Drop `flock_other.go` only if Mac-only (R45); keep for safety. |
| launch | 704 | shrink | 250 | v2 launch.json record with tmux, wake, symphony, terminal fields (`record.go:30` Record; `:328` Write; scans `:394-508`). 3.0 seat-exec writes its own record (plan 4.3). Keep a minimal Record and Read/Write/Scan; drop terminal/wake/legacy-path fields. 15 cli files and state read it, so cut it last. |
| launchintent | 538 | keep | 450 | The launch intent compiler (`launchintent.go:240` Compile, `:299` compileSeat, `:474` seatInitialInputText). Plan names it. Drop below-floor branches once the AMQ floor is raised to the pin (tests at `launchintent_test.go:81,202`). |
| liveidentity | 257 | delete | 0 | `identity.go:149` Verify checks pane/tty/wake identity layers. AMQ launchapi owns liveness now. Callers: cli live_identity, runtime_json, status, team_member_control_continue. |
| namespace | 120 | keep | 120 | `namespace.go:16,55`. 7 importers. Review for the "default profile" concept that squad.yaml drops. |
| notifier | 155 | delete | 0 | Desktop/command sinks (`sink.go:10` Sink, `desktop_darwin.go`). attention[] is a status field now; NOC renders. Only caller cli/operator_attention.go. |
| operatorauth | 2033 | keep | 1800 | Typed gates and operator auth (`envelope.go:91,285`, `policy.go:260` Evaluate, `release.go` release spec). Plan names it. `release.go` (483) overlaps compoundrelease; review. |
| procinfo | 503 | shrink | 250 | See leaf list. Drop TTY and child-tree helpers (`procinfo.go:35,93`, `native_linux.go` if Mac-only). |
| reviewrebind | 837 | keep | 700 | Plan 16.1 says "shrink to what squad.yaml needs", but evidence says otherwise: `rebind.go:86` Prove computes tree/patch-id proofs so a rebase that keeps the tree keeps the review (R34 merge train). Callers: cli/verify.go, verify_rebind.go. Not a squad.yaml concern; correct the plan wording. |
| role | 589 | keep | 450 | `role.go:30` ExtensionDir, `:35` Path, `:108` EnsureContent; `parse.go` reads md/yaml/json roles. ExtensionDir is the seat dir seat-exec writes into. Drop JSON/YAML role forms if squad.yaml carries them. |
| rules | 301 | keep | 250 | `rules.go:116` Plan, `:142` Apply: managed block in AGENTS.md/CLAUDE.md pointer stubs. Rules stay (plan 10); R37 guards these paths. |
| runtimeaction | 154 | delete | 0 | Canonical action object (`actions.go:10` Action, `:69` Member). Port the Action struct (argv + id) into the status contract first (plan 4.6 "action as argv plus action id"). |
| runtimecontrol | 551 | delete | 0 | Terminal capability registry for tmux/iTerm2/Terminal.app (`capabilities.go:21`). Plan 10. |
| state | 3444 | shrink | 2200 | Becomes the status classifier base. Keep message.go (618), mailbox.go (122), coordination.go (629: `:36` ProjectCoordination, `:223` ResolveOperatorGate), snapshot.go (398: `:29` Build), match/flow/files/external_evidence. Review `collapse.go` (996: thread collapse + attn classification, `:585` ClassifyAttnSubject) and cut probe/pane fields in state.go/snapshot.go (`Probe`). Line-by-line review still needed (plan 16.1). |
| task | 3463 | shrink | 2900 | Five-state store with leases already exists (`task.go:79` Task, `:151` Lease, `:69` DefaultLeaseDuration; `lifecycle.go:20` Claim, `:83` RenewLease, `:147` DoneAtomic; outbox `:921-1288`). No pool concept yet. Cut `command_evidence.go` (225, imports commandevidence). Review `leadership.go` (156: lead epoch CAS) and `completion_reconcile.go`. |
| team | 1689 | shrink | 650 | Profile JSON schema 6. Rewrite around squad.yaml (`team.go:53` Member, `:601` Team, `:870` Read, `:1091` Validate 300 lines). Delete `clone.go` (49), AutonomousPolicy (`:1390`). Highest fan-in; do the rewrite in 2b, not 2a. In 2a cut only the parts whose cli callers are deleted. |
| tmuxharness | 485 | delete | 0 | Orphan (no importers). |
| tmuxpane | 1686 | delete | 0 | `tmux.go:109` PaneExists, `:120` SendKeysToPane, `deliver.go`, `busy.go`. R6: never touch a pane. Blocked until dispatch, doctor, global_noc, operator_attention, status, team_launch drop their imports. |
| userconfig | 150 | keep | 150 | `config.go:32` Path (~/.amq-squad), `:49` Read. Home of global config; grants.yaml sits beside it. |
| wizard | 4962 | delete | 0 | Bubbletea and numbered wizard. Importers: cli run_start_terminal_context.go (deleted), resume_goal.go (deleted). Extract first: `routing.go` (308): `:81` WorkClassPolicyFor, `:190` RecommendModelEffort, `:122` DefaultWorkClassForRole. It is the nearest ancestor of the routing table (R20-R26) and `composition_policy.go` (39) worktree advice. Park both under a new `internal/routing` or lift into agentcatalog. |
| worktreeplan | 1647 | keep | 1300 | Worktree per seat (`model.go:55` Record, `service.go:25` NewService; `residue.go` cleanup). Plan names it. Trim shared-cwd exception (`model.go:48`) and doctor-only diagnostics (`:145-153`). |
| **subtotal** | **36,391** | | **16,304** | exact sums below |

Sum check: v2 non-cli = 110,929 - 74,538 = 36,391. 3.0 non-cli = 200+250+174+32+50+245+3200+450+233+250+450+120+1800+250+700+450+250+2200+2900+650+150+1300 = 16,304.

## 3. internal/cli clusters (74,538 lines, 175 files incl. platform stubs)

I re-clustered by reading file heads and by compile probe. Corrections to your list:
- `orchestrate.go` (404) is NOT wizard. It is the tmux orchestration runner (`insideTmux`, `orchestrateTmuxRun`). Moved to the tmux cluster.
- `bootstrap_failure.go` (402, 24 tmux mentions) is pane recovery, not bootstrap. Moved to tmux.
- `claude_rename.go` (143) is a tmux-pane helper (imports tmuxpane). Moved to tmux. `run_shell_silence.go` (31) likewise.
- `team_member_control_continue.go` (328) is tmux control-mode (`validateTmuxControlClientName`). Moved to tmux, not team.
- `lead_identity.go` (145) belongs with external orchestrators, not liveness.
- `list.go` (226, legacy scanner) and `preview_flags.go` (193) are legacy start/launch scaffolding, moved to the wizard/start cluster.
- `collect_*` (690) is not generic: it is the AMQ drain journal for wake delivery. Delete.
- `global_status.go`/`global_status_doctor.go`/`global_noc.go`/`status_board.go` are the fleet registry and board that amq-noc replaces. Not "console" in the package sense: the `console` package is dead; these are cli-owned.
- `team_launch_launchapi.go` (794) is a KEEPER, the launch compiler caller. It still names `tmuxTeamLaunchBackend.Validate` (`:37`) and `legacyTeamLaunchBackend` (`:111`); cut those.
- `launch.go` (1214): LEGACY LAUNCH DRIVER. `runLaunch` (`:97`) is reached only from `agent.go:76` (`agent up`), `restore.go:230`, and itself (`:212`). It writes the launch record, runs `amq coop exec` (`execAMQCoop :824`), manages wake and symphony. Delete, after extraction below. It is not in `commandCatalog`.

Fate table. "Refs" = kept code that references deleted symbols (the call sites 2a must cut). "Extract" = keep before deleting.

| cluster | v2 | fate | 3.0 | extract before delete, and refs |
| --- | --- | --- | --- | --- |
| C1 tmux/iTerm2/Terminal/layout/wake/panes/orchestrate/bootstrap_failure/claude_rename/team_member_control_continue (25 files) | 5,473 | delete | 0 | Extract: nothing. Inline tmux presence check where `team_launch_launchapi.go:37` needs it. Refs: 72 undefined symbols, e.g. `tmuxTeamLaunchBackend`, `exactTmuxPaneID` (`tmux_exact_id.go`), `normalizeWakeInjectMode`, `runClaudeSessionRename` (called from `command_registry.go`), `runGlobal`, `defaultGlobalNOCControlRoot` (orchestrate.go). |
| C2 runtime control: runtime_actions, runtime_json, runtime_identity, operator_control (4) | 1,650 | delete | 0 | Extract: argv+id action shape (`runtimeaction/actions.go:10`) into status; `runBroadcast`/`runOperatorSend` (operator_control.go) if broadcast stays as a verb. `runFocus`/`runSend` go (pane-only). `resolveProjectProfile`, `compareCWD`, `sameResolvedDir`, `readPromptBody` in runtime_actions.go are generic helpers: move. Refs: 37. |
| C3 NOC/board: global_noc 601, status_board 765, global_status 752, global_status_doctor 224 | 2,342 | shrink | 450 | Keep `global_noc.go:117` registration plan + `:153-265` registry read/write (amq-noc reads the fleet registry; review with owner). Fold `global_status.go` into status (300). Delete `status_board.go` and `global_status_doctor.go`. Refs: tmuxpane and runtimeaction imports. |
| C4 wizard/start/new/setup/init/plan/list/preview_flags (9) | 4,473 | delete | 150 | Extract: rules pointer-stub sync with digest gate (`init.go:110` rules.Plan, `:266` freshPlans, `:92` verify comment) is the R8 "one digest for all files"; `up.go:424` briefCandidateEnvelope; `new.go` `peelProjectFlag`, `runInProject` (arg helpers). Refs: 37 (`runInit`, `runStart`, `runPlan`, `runSetup`, `runNew`, `runWizardWithVersion`, `registerPreviewFlags`). |
| C5 goal supervision (goal*, resume_goal; 10) | 6,204 | delete | 0 | Extract: `digestBytes` (`resume_goal.go:383`). goal.go already prints removed-subcommand redirects (gh#761). Refs: 43 (goalBindingData etc. used from bootstrap, team_launch*, status, doctor). Also doctor check `doctorCheckGoalSupervision` (`doctor.go:827`). |
| C6 recovery_transition* (5) | 1,449 | delete | 0 | Extract: nothing. Refs: 23, from goal and resume paths only. |
| C7 namespace_* (12) | 3,578 | delete | 250 | No v2 compat, so migration (`namespace_migration_*`, `_plan`, `_tx`, `_adapters`, `_native_claims`, `_cli`) goes. Extract the admission locks that task and amq mutations rely on: `namespace_migration_guard.go:253` acquireNamespaceWriterAdmission, `:42` acquireRevalidatedContextWriter, `:67` acquireRevalidatedAMQWriter, `:84` validateReResolvedContext, type `namespaceAdmissionLocks`; and `namespace_open_unix.go` (117, safe open). `namespace_conflict.go:ensureNoNamespaceCreationCollision` can go with default-profile concept. Refs: 26. |
| C8 notification_watcher, notification_amq_watch, session_notifier | 2,361 | delete | 0 | Supervised child processes (`_notification-watch`, `_session-notify` in `command_registry.go:94-100`). Attention queue now lives in status. Refs: 16 (lifecycle start/stop in up/down/doctor/status). |
| C9 external_orchestrator_registry, _lifecycle, lead_identity | 1,220 | delete | 0 | Lead is built-in pi (R2). Extract `sameFilesystemPath`, `currentWorkingDirMatches` (lead_identity.go). Refs: 13. |
| C10 liveness 641, live_identity 323, version_alignment 318 | 1,282 | shrink | 250 | Keep `liveness.go` verdict vocabulary (`agentLivenessVerdict`, shared by status and resume) as the closed seat-state list for the contract; cut pane and wake inputs. Delete live_identity (liveidentity pkg) and version_alignment (v2 cross-version compat). |
| C11 up 431, down 1780, resume 342, resume_shared 124, resume_exec 249, resume_required_actions 167, restore 758, session_reset_support 691, exit 174, wait_posture 303, visibility 107 | 5,126 | delete (mostly) | 400 | Keep: `exit.go` (174) exit-code taxonomy plus `PartialError :39`, `ActionDecisionError :49`, `parseFlags :103` (every command uses them). Keep ~100 of `wait_posture.go` (`guardOwnedWait :67`, `durableSendWaitPosture :170`; amq send uses them). Extract `allowInterspersedFlags` (`session_reset_support.go:98`), `shellQuote :713` and `shellCommand :731` (`restore.go`). `down` returns later as a ~150-line stop-squad verb on AMQ/launchapi. Refs: 36. |
| C12 bootstrap 703, preflight 681, team_launch_bootstrap 151 | 1,535 | shrink | 450 | `bootstrap.go` is the context-verb ancestor (section 4). Keep `buildBootstrapPrompt :77`, `bootstrapContextFor :247`, `sanitizeBootstrapContext :98`, routeCommandFor :529; cut pane/wake/goal bits. `preflight.go`: duplicate-launch blocker over presence files; delete except version checks. |
| C13 verify* + amq_root_authority (verify 611, verify_action 1010, verify_authorization 337, verify_rebind 126, amq_root_authority 592) | 2,676 | shrink | 1,800 | Guard ancestor (section 4). Keep action decision flow, signed authorization, merge and release checks. `amq_root_authority.go`: `reconcileAMQRootConfig` rewrites AMQ root config; review (200 target). |
| C14 amq 1572, amq_send 338, amq_env 284 | 2,194 | shrink | 700 | Project-aware `amq` wrappers: `resolveAMQContext :158`, `amqCommandEnv :376`, `runAndWriteAMQ :401`. Keep context resolution, send, env pinning; drop the diagnostics subcommands and confirm-gated maintenance. |
| C15 doctor | 1,912 | shrink | 600 | Probe per plan 16.2. Keep the framework (`doctorCheck :44`, `runDoctorChecks :661`) and the checks for versions (`:1151`), launchapi compat (`:1196`), adoption floor (`:1245`), module pin (`:1271`), AMQ ops (`:1349`), skill/version skew (`:220,364`). Delete tmux (`:1455,1477`), orphan panes (`:1521`), marker (`:1563`), notification (`:1095`), goal (`:827`), wake (`:1773`), team/pointer/rules-roster checks (`:1379,1423,1665`). Detection of Ollama, RAM, GPU, latency is new code. |
| C16 status | 1,951 | shrink | 900 | Contract producer. Keep snapshot build and render; add schema_version etc. Cut pane/runtime/terminal fields (49 tmux mentions). |
| C17 operator* (operator 1775, attention 1210, self_approve 669, handoff_card 216, identity 153, delivery 117, guard 63, mode 15, team_operator 207) | 4,425 | shrink | 1,600 | Keep gate raising and answering, self-approve under policy, attention projection (`operator_attention.go:433` collectGateAttentionProjection feeds `attention[]`). Delete handoff card, delivery polling, notifier wiring (imports attention, notifier, tmuxpane). |
| C18 team* (team 2579, overlay 1310, lead 1175, launch 1063, member 1051, launchapi 794, rules 515, rules_draft 237, remaining small) | 9,385 | shrink | 1,650 | Keep: `team_launch_launchapi.go` (600 after strip), `team_rules.go` + `team_rules_draft.go` (450, `renderTeamRules :40`), `team_launch.go` (250: preflights `:646-735`, result validation `:557`; drop backend registry `:160` and tmux panes `:759-801`), team.go (300: show). Delete `team_overlay.go` (per-member Claude settings overlays, `overlaysDirName`; replaced by the hooks plugin), `team_lead.go` (wake sidecar: `wakeDrainInject`), `team_member.go`, `team_autonomous`, `team_shared_cwd`, `team_rm`, `team_member_diff`. Rewrite in 2b against squad.yaml. |
| C19 task 1309, dispatch 853, task_selection 216, task_completion_evidence 386 | 2,764 | shrink | 1,650 | Keep task lifecycle CLI, `taskSelection` (immutable identity under admission), completion evidence. `dispatch.go`: keep the queue-then-AMQ-send; delete `dispatchNudgePrompt` pane injection (tmuxpane, 12 mentions). |
| C20 evidence 774 + collect_* 690 | 1,464 | replace/delete | 0 | Replaced by `evidence ingest`. Extract `evidenceSecretName` (`evidence.go:747`) and `evidenceTaskLink :561` semantics for the ledger. Collect: delete. |
| C21 gate 178, gate_close 253, release_plan 435, compound_release_attention 301, compound_release_domain 386 | 1,553 | keep | 1,400 | Plan 16.1. Imports compoundrelease, operatorauth, state. |
| C22 worktree 362, worktree_isolation 321 | 683 | keep | 600 | Imports worktreeplan. |
| C23 role_draft 493, roles 146, agent 339, agent_args 235, agent_defaults 459, agent_catalog 162, model_defaults 170, effort 239, native_args 169 | 2,412 | shrink | 1,300 | Provider-argument assembly is R4: keep agent_args, agent_defaults (`claudeInScopePreauthAllowlist :42`), model_defaults, effort, native_args, agent_catalog. Delete `agent.go` (internal `agent up/resume` boundary that calls runLaunch). Role drafting is off-path: keep role_draft, roles. |
| C24 completion | 996 | delete | 0 | Regenerate small if wanted. Ref: 1 (`runCompletion`). |
| C25 execution_mode | 682 | review | 150 | Execution contract (`executionContract :233`) with goal/lead modes; keep the faultRemedy action object (head of file) only. |
| C26 context_resolution 1020, target_root 157, pathnorm* 4 files ~370, profiles 144, project_dirs 33, thread_session 16 | 1,743 | shrink | 950 | `resolveScopedCommandContext :94` is how every command picks project/profile/session/handle. Keep, simplified by squad.yaml. Delete profiles.go (profile concept). |
| C27 brief 127, briefs 197, briefs_seed 234, drafter_config 102, workstream 353 | 1,013 | shrink | 650 | Brief drafting kept (plan 10). `workstream.go` member filtering by session shrinks to ~50. |
| C28 launch.go | 1,214 | delete (legacy launch driver) | 300 | Extract into the seat-exec file: `writeLaunchRecordWithSnapshot :757` + `rollbackLaunchRecordIfCurrent :780` (record write with rollback), role write `:1182-1196` (role.EnsureContent/EnsureStub), `resolveAMQEnvForLaunch :917`, `normalizedAgentBinary :1093`, `ensureLauncherExecutable :900`, `stringListFlag :84`. Everything else (wake, symphony, coop exec `:824`, terminal env `:829`, duplicates `:874`) goes. Refs: 21. |
| C29 core: cli 279, command_registry 131, output 197, json 47, mutation_json 53, body_warning 71 | 778 | keep | 600 | `command_registry.go` is rewritten for the 3.0 command set. |
| **total cli** | **74,538** | | **16,800** | |

Cluster totals in this table add to 74,538 (checked by script). Your cluster 1 (4,165) becomes 5,473 once the five misfiled files are added; your wizard cluster (4,458) becomes 4,473 after moving orchestrate out and list/preview_flags in.

## 4. The three verbs: nearest v2 ancestors

v2 has no Claude/pi hooks at all (grep for PreToolUse/SessionStart/UserPromptSubmit in Go returns nothing; `plugins/` holds only skill markdown). Today injection happens once at launch, and checks happen as explicit CLI calls.

context (role + rules + task + gates):
- `internal/cli/bootstrap.go:77` buildBootstrapPrompt and `:247` bootstrapContextFor build the role/team/roster/rules text; `:237` appendGeneratedBootstrapPrompt injects it as the first prompt. `launchintent.go:474` seatInitialInputText routes it to AMQ initial input.
- Rules: `team_rules.go:40` renderTeamRules; `internal/rules/rules.go:116` Plan, `:142` Apply (pointer stubs in AGENTS.md/CLAUDE.md).
- Role: `internal/role/role.go:35` Path, `:108` EnsureContent; written at launch by `launch.go:1182-1196`.
- Open gates: `operator_attention.go:433` collectGateAttentionProjection. Current task: `task_selection.go` + `task/task.go:79`.

guard (allow / needs gate / deny):
- `verify_action.go:81` runVerifyAction → `:271` executeVerifyActionInSelectedContext → `:569` decideTypedVerifyActionWithAuthorityPolicy. Returns `verifyActionResult.Decision` (`:54`; values `approved`, `pending`, `denied`-class, constants `:23-24`). Action class normalised at `:465` normalizeHighRiskAction.
- `operatorauth/policy.go:260` Evaluate(kind, action, allowlist); `envelope.go:285` VerifyAuthorizationEnvelope; `verify_authorization.go` (signed path).
- Release: `compoundrelease/guard.go:18` InvocationGuard, `:41` Run.
- Operator target check: `operator_guard.go:14,33`. Tool allowlists at launch: `agent_defaults.go:42` claudeInScopePreauthAllowlist; launchintent allowedTools (tests `launchintent_test.go:117`).

evidence ingest:
- `evidence.go:120` runEvidenceRun → `commandevidence/evidence.go:258` Store.Run (runs and journals; the opposite of ingest). `task/command_evidence.go:23` LinkCommandEvidenceForProfile links an attempt to a task. `evidence.go:571` sendEvidenceReport posts it over AMQ.
- Seat activity: `activity/activity.go:188` Write. External evidence rows: `state/external_evidence.go:28` BuildExternalEvidence. Completion: `task_completion_evidence.go:62` assessTaskCompletionEvidence.

Gap: all three verbs are mostly new code; v2 supplies the decision logic (guard), the text (context), and the redaction/link rules (evidence).

## 5. Sum and top-10 deletions

| | v2 | 3.0 (after 2a) |
| --- | --- | --- |
| internal/cli | 74,538 | 16,800 |
| other packages | 36,391 | 16,304 |
| **internal total** | **110,929** | **~33,100** (range 30k-38k; about 70% removed) |

The 16.8k assumes 2a includes function-level cuts inside the shrink files. Whole-file/whole-package deletion alone (shrink and keep files untouched; not computed per file, rough estimate ~55-60k) leaves the last ~25k as the line-level cuts in C13-C19, doctor, status, state, team, task. Split 2a into 2a-1 (whole files and packages) and 2a-2 (in-file cuts) if you want two gates.

Ten largest net removals (v2 minus 3.0 target):
1. cli team* cluster: 7,735 (9,385 to 1,650)
2. cli goal supervision: 6,204
3. cli tmux/terminal/layout/wake/panes: 5,473
4. internal/wizard: 4,962
5. cli up/down/resume/restore/reset: 4,726
6. cli wizard/start/new/setup/init/plan: 4,323
7. internal/console (orphan): 3,626
8. cli namespace migration: 3,328
9. cli operator*: 2,825
10. cli notification/session notifier: 2,361

Next: commandevidence 2,069; C3 board 1,892; tmuxpane 1,686; runtime control 1,650. Also drops go.mod deps: bubbletea, bubbles, lipgloss (wizard, console), and fsnotify unless the status watcher needs it.

## 6. Mechanical method for 2a

Principle: leaf-first for packages, root-first for cli. A package is deleted only when its importer count is 0; a cli cluster is cut by removing its entry points first so everything it owns becomes unreachable.

Steps (each step = one commit, each must be green):
0. Baseline: `go build ./... && go vet ./... && go test ./...` on main. Record the first-run test count so shrinkage is explainable.
1. Orphans (zero edits elsewhere): delete `internal/console`, `internal/tmuxharness` with their tests. Verify `go build ./... && go vet ./... && go test ./...`.
2. Cut entry points in `command_registry.go` (`commandCatalog` and `commandRegistry`) and `lookupCommand` (`_notification-watch`, `_session-notify`, claude rename helper). Run `golang.org/x/tools/cmd/deadcode ./cmd/amq-squad` to list what is now unreachable; it is the work list for the next steps.
3. Per cluster, in this order (so callers go before callees): C5 goal, C6 recovery, C4 wizard/start, C11 up/down/resume, C8 notification, C9 external orchestrator, C7 namespace migration (after extracting admission locks), C28 launch.go (after extraction), C2 runtime control, C1 tmux/terminal, C24 completion, C20 evidence/collect. For each: do the extractions listed in section 3 into one new file `internal/cli/keep_extracted.go` (a temporary holding file, named in the manifest), delete the cluster's non-test and test files, fix the remaining call sites using the "refs" list (probe command: `go build -gcflags=-e ./internal/cli 2>&1 | grep 'undefined:'`), then run the three commands.
4. Packages, only when `go list -f '{{.ImportPath}} {{join .Imports " "}}' ./... | grep <pkg>` shows no importer: runtimeaction, act, runtimecontrol, wizard (after routing extraction), notifier, liveidentity, autonomy, bootstrapack, tmuxpane (last, after dispatch/doctor/global_noc/operator_attention/status/team_launch lose their imports), commandevidence (after `task/command_evidence.go` and the evidence cli are gone). Order within this step is topological: runtimeaction before runtimecontrol; notifier before attention trim.
5. In-file cuts (2a-2) for shrink clusters: C3, C10, C12-C19, C23, C25-C27, plus state, task, team (only parts with deleted callers), launch, procinfo, attention. One cluster per commit.
6. `go mod tidy`, then check module graph shrank (bubbletea, bubbles, lipgloss out).

Verification after every batch: `go build ./... && go vet ./... && go test ./...`. `go vet` also compiles test files, so deleted-source test fallout shows up there. Additional gates: the deadcode list is empty for deleted symbols; `scripts/`/`l2-smoke.sh` still run; the commands removed from the registry no longer appear in `commandCatalog` golden/help tests (update those goldens in the same commit).

Risks seen in the probe: 72 undefined symbols for tmux alone, but all are in kept files that also contain deleted-cluster calls (status.go, doctor.go, dispatch.go, team_launch.go). Cut those callers by hand; do not stub with empty functions or they survive as dead code.

Recording the manifest: one file, `docs/v3-manifest.yaml` (machine-readable, one entry per path):

```yaml
- path: internal/cli/goal_supervision.go   # file or package dir
  v2_lines: 1067
  fate: delete        # keep | shrink | delete | replace | review
  target_lines: 0
  cluster: C5
  extract_to: null    # or internal/cli/keep_extracted.go
  extract: []         # [{sym: digestBytes, at: resume_goal.go:383}]
  reason: "goal supervision, replaced by squad.yaml + scheduler"
  status: pending     # pending | done (set by the 2a executor)
```

Add `scripts/manifest-check.sh`: every non-test `.go` under `internal/` must appear once; no path with `fate: delete, status: done` exists; sum of `target_lines` equals the target. A markdown table in the plan is for reading; the YAML is the source of truth and the executors flip `status` in the same commit that deletes the files.

## 7. Open points for the lead

- Plan 16.1 says reviewrebind shrinks "to what squad.yaml needs". Evidence says it is merge-train proof (`rebind.go:86`), so it stays; fix the plan text.
- Plan 4.5 says the task store has pools; it does not yet (leases exist at `task.go:151`). Pools are new work, not a manifest item.
- go.mod still pins `agent-message-queue v0.75.0`; manifest assumes launchapi stays at the 0.61.1 contract.
- Owner decisions: bootstrapack (delete vs keep for Codex launch-only), global_noc registry (keep ~150 vs let amq-noc own it), `down` return as a thin verb, commandevidence `subject.go` (405) as guard digest helper.
