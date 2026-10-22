# amq-squad 3.0, phase 2a-2 execution log

Branch `v3-2a2`, cut from `v3` at c8f030d. Scope: the in-file shrink (plan 4.1 step 5) plus the 2a-1 deferrals (`docs/v3-spike/2a1-execution.md`, sections "Deferred", "Coverage debit for 2a-2", "Holding file debit for 2a-2").
One cluster = one commit. Every commit is green on `go build ./... && go vet ./... && go test ./... && scripts/manifest-check.sh`, `GOOS=linux go build ./...` and `GOOS=windows go build ./...`. The gate script also vets internal/cli for linux and windows.

## Running total

Non-test lines under internal/ (`find internal -name '*.go' ! -name '*_test.go' | xargs wc -l | tail -1`). Start 72,972. Manifest target 33,504; the band is 33,100 plus or minus 10 percent (29,790 to 36,410).

| commit | cluster | lines after | holding file | tests listed |
| --- | --- | --- | --- | --- |
| start (c8f030d) | | 72,972 | 832 | 2,027 |
| 1 | C28 launch driver + C11 hosts | 70,555 | 786 | 1,918 |

## Rulings received from the lead

1. (start) `team overlay` follows the manifest: the verb and team_overlay.go are cut, and the spawn-time tool-policy check (validateMemberOverlayPaths) goes with them unless the manifest names it as an extract. 2b item: "tool policy comes from squad.yaml at launch (replaces validateMemberOverlayPaths)".
2. (start) Named manifest extracts are kept, with the helpers they need, even when deadcode reports them unreachable; they are the 2b ancestors. The rest of the dead code is cut. The kept-dead extracts are listed below with line counts.
3. (start) Keep-fate rows are not cut. Review-fate rows are reviewed line by line: code that deadcode reports unreachable and only tests reference is cut with those tests, each named. compoundrelease stays as a package; its test-only dead code (about 1.4k lines in store.go, reconcile.go, transition.go) falls under this rule. Replace rows (evidence.go, commandevidence/evidence.go) are deferred to 2b because they need the ledger: status `deferred-replace`, lines listed. The total is reported as measured. manifest-check computes the plus or minus 10 percent check over current lines excluding deferred-replace rows (see Tooling); the manifest targets are not lowered.

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
