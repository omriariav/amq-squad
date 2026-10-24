# amq-squad 3.0, brief 3-2: merge train, branch protection and the release flow

Branch `v3-3b`, cut from `v3` at 2c7a38e6. One implementer, own worktree, no helper agents. Every commit passes the standard gate (`go build ./... && go vet ./... && go test ./... && scripts/manifest-check.sh --final`, `GOOS=linux` and `GOOS=windows` builds) plus `go test -race` on the packages it touches. The full output of every gate run is under `docs/v3-spike/3-2-evidence/gate-*.txt`, and every revert run is under `reverts-*.txt`. Only read-only gh calls touched amq-noc and workspace-cli. Nothing was merged or closed, and acceptance 4 (the real run) was not run.

## Lead rulings received during the brief

- Size cap default (2026-10-24): `size_cap_lines` 300, the largest clean PR of the S6 dry run (207) rounded up to the next hundred. `size_cap_files` 5, the largest clean PR's file count (2) rounded up to the next five. Both are optional grant fields with these defaults, and squad.yaml can only lower them. The owner sets 400 for a repository where he wants it. Milestone B (impl-2b2-b) reads the same two names. 3-2 owns the grants.go change, and whichever branch lands first carries the fields.
- The two dry-run findings go to the owner: the CI-only rulesets are missing on both repositories, and W2, W3, W4 and W6 fail CI on skill docs. They are recorded as deviations from the S6 prediction.
- The rebase-for-CI plan for the amq-noc PRs (base older than the CI workflow) is accepted.
- The skill-doc CI failures are evidence for the file-set rule: a task's declared_files must include the documentation targets the repository's CI requires. See "required companions" under open rulings.

## Commit 1: the merge train, branch protection, credential custody, the Codex approval check

Built:
- `internal/mergetrain` (new):
  - `Request`: the lead's ordered set, schema 1: repository, base, 1..50 PRs, each with its task and declared_files, plus optional extra `required_checks`.
  - `Train.Run` checks every PR at its current head and orders the set. It writes the batch digest to the audit log and then, unless it is a dry run, lands the PRs in order through the `Forge`.
  - Rules (`checks.go`), one refusal code each: `repository`, `base_branch`, `ci_green` (every required check `success` at the head SHA; a check with no run counts as red), `size_cap` (changed lines, and changed files), `force_push` (any `head_ref_force_pushed` event except one landing on a head the train's own rebase produced, read from the audit log), `protected_path` (grant entries, the implicit R37 set included), `task_scope` (every path, rename sources included, inside the task's declared_files), and `pr_state`.
  - A refusal goes to the lead in the result, and the train raises an owner-signed gate through 3-1's store (`StoreEscalator`: record first, then the question when a transport is attached). The gate is action `spawn`, typed class `grow` ("growth beyond the grant"), with target `gatetarget.MergeBeyond`: `grow merge <owner/repo>#<pr> head=<sha> rules=<codes>`.
  - An accepted gate for exactly that target lifts exactly that refusal (`Gates`, wired to `gateverify.Verifier.AcceptedFor`, the full owner-signature check). A new head or another rule needs a new gate.
  - Batch digest: the S1 merge formula (domain `amq-squad-gate-target-v1`, class `merge`, repository, base SHA, count, ordered pairs). A test pins it to the S2 gate s2-merge-01 digest `sha256:46f31e9a87fc27f3…b764a`. Nobody signs it.
  - Audit log: `<session root>/extensions/amq-squad/mergetrain/audit.jsonl` (0600, flock, fsync, under guard's protected extension dir). Entry kinds: protection, batch, refusal, rebase, merge, stop, plan.
  - Ordering: every PR is marked `after` the earlier PRs of the set whose file sets overlap it (the predicted conflicts). In the real run a PR is rebased (`gh pr update-branch --rebase`, the owner's identity) when it touches a file the run already landed, when the server says CONFLICTING, or when it is `rebase_for_ci`.
  - Rebase for CI: no required check ran at the head and the PR's base is behind. The plan marks the PR, the run rebases it, and CI then runs.
  - A head that changed (the train's rebase or a seat's push) recomputes the digest, waits for CI on the new head (20 min default) and re-runs the whole check.
  - Merge: the REST merge endpoint with `merge_method=squash` and `sha=<head>`. The train never uses `gh pr merge`, which turns into auto-merge when checks are pending.
  - Manual mode (`--manual` or grant mode manual) keeps the per-merge stop: a `y` on a terminal merges the next PR, anything else stops.
  - A real run from a seat process (`AMQ_SQUAD_SEAT` set) is refused (`ErrSeat`, R34). This is UX and evidence, not enforcement, because a same-UID process can unset the variable.
  - Branch protection (`gh.go` `evaluateProtection`) reads the effective rules (`rules/branches/<b>`, with each ruleset's bypass actors) and the classic protection together. "Met" means at least one required check and direct push refused for the owner: a ruleset pull_request rule with no bypass actors, or classic required reviews with enforce_admins on. The check runs before every real run, not only the first, because S2 removed its ruleset after the merge. A run where it is not met refuses to start (`ErrProtection`).
  - `SeatCredentialLeaks`: the credential custody check over 2b-2's `seatexec.SeatEnv`.
- `internal/cli/merge_train.go` (new): the `amq-squad merge-train` verb. `--dry-run` with `--root` and `--grants` runs a dry run without a session or the owner's file; a real run resolves its session and reads the owner's grants only.
- `internal/grants`: `Merge.SizeCapFiles`, `DefaultSizeCapLines` 300 and `DefaultSizeCapFiles` 5; `size_cap_lines` and `size_cap_files` are optional; `Narrowing.MergeSizeCapFiles` and squad.yaml `merge_size_cap_files`.
- `internal/gatetarget`: `MergeBeyond` and its convention line.
- `internal/seatexec`: `CheckCodexSandbox` also requires `--ask-for-approval on-request` (also `-a` and the `=` forms). An `approval_policy` config override is refused (`codex_approval_missing`). `GIT_ASKPASS` and `SSH_ASKPASS` are scrubbed: the custody check found that they passed the scrub.
- guard (`hook_guard.go`): new deny `credential_read` for `gh auth token`, `gh auth status --show-token`/`-t`, `security find-*-password -w|-g`, `git credential fill`, `git credential-*`, and `launchctl getenv SSH_AUTH_SOCK`.

Behavior changes named:
1. A Codex seat without `--ask-for-approval on-request` is refused by seat-exec (`codex_approval_missing`). The launch compiler already emits it (`seatlaunch.CodexSandboxArgs`).
2. A merge grant without `size_cap_lines` used to be a parse error. It now defaults to 300, and `size_cap_files` to 5.
3. guard denies the six credential reads above (`credential_read`). `gh auth status` without the token flag stays allowed.
4. `GIT_ASKPASS` and `SSH_ASKPASS` no longer reach a seat.
5. A new public verb, `merge-train`.

Tests (`internal/mergetrain`, with a fake gh: the test binary itself answers the exact gh command lines over a local bare repository with real diffs, merge-tree conflicts, squash merges and rebases):
- Acceptance 1, `TestAcceptance1FiveCleanPRsLandInOrder`: five draft PRs land as five squash commits in the lead's order. The audit log holds the plan digest. PR 4 (shares shared.txt with PR 2) is ordered after 2, rebased after 2 lands, and gets a new head. The second batch digest differs and matches `BatchDigest` over the new pair, CI is read at the new head, and no force-push refusal fires for the train's own rebase.
- Acceptance 2, `TestAcceptance2Refusals`: one row each for CI red, size cap (lines), size cap (files), force push, protected path (`.amq-squad/team-rules.md`), wrong base (`dev`), and file outside the task. Each raises a gate record (`spawn`/`grow`, target with that head and rule) and merges nothing: main is unchanged and no merge call is made.
- Acceptance 5, `TestAcceptance5NoRulesetRefusesToStart`: with no ruleset, CI required but direct push allowed, or classic protection that exempts admins, the train refuses to start and reads no PR. Classic protection with enforce_admins lands.
- Also: `TestDryRunMergesAndRaisesNothing`, `TestAcceptedGateLiftsExactlyThatRefusal`, `TestManualModeStopsBeforeEachMerge`, `TestSeatProcessHoldsNoMergePath`, `TestNewHeadAfterPlanIsRechecked` (a seat push between plan and merge is re-checked and refused on task scope), `TestCIAbsentOnABehindBaseIsRebasedNotRefused`, `TestBatchDigestIsTheS2Formula`, `TestDeclaredAndProtectedMatchers`, `TestEvaluateProtection`, `TestSeatEnvironmentCarriesNoForgeCredential`.
- `internal/grants` `TestMergeSizeCapDefaultsAndNarrowing`; `internal/seatexec` `TestCodexSeatRequiresApprovalOnRequest`; guard rows `cred/*` in `TestGuardRuleMatrix`. The seatexec fixtures that launched Codex without the approval flag now carry it.
- Reverts (`reverts-c1.txt`): 16 rows. Each fix was undone, its row failed on its assertion, and the row passed again after restore: ci_green, size_cap_lines, size_cap_files, force_push, protected_path, base_branch, task_scope, protection, protection_bypass, rebase_redigest, rebase_recheck, seat_no_merge_path, dry_run_raises_nothing, custody_askpass, codex_approval, guard_credential_read.

### Acceptance 3: the S6 dry run (attached)

Evidence: `docs/v3-spike/3-2-evidence/s6-dry-run/` holds the requests, the proposed grants (not the owner's file; ruling 1 is open), and the text and JSON outputs of pass 1 (sizes measured with an unreachable cap) and pass 2 (cap 300 and 5 files). Only read-only gh calls ran: 48 per repository. Nothing was merged, rebased, marked ready or raised. Wall time: 18.7 s for amq-noc (9 PRs) and 18.8 s for workspace-cli (9 PRs), 37.5 s for the S6 set of 18. declared_files come from S6-tasklist.md (ruling 11). The required check names are `ci` for amq-noc (the S2 ruleset) and `build (1.25)` for workspace-cli (its CI job).

Branch protection on both repositories: **not met**. amq-noc has no ruleset and no branch protection, because S2's ruleset 25612981 was removed after its merge. workspace-cli has classic protection with no required checks and no PR requirement, so direct push is allowed. A real run refuses to start on both until the owner re-creates the S2 CI-only ruleset.

| repo | PR | task | changed lines | files | plan | note |
| --- | --- | --- | --- | --- | --- | --- |
| amq-noc | #79 | N10 | 109 | 1 | planned, rebase for CI | |
| amq-noc | #80 | N3 | 127 | 2 | planned, rebase for CI | |
| amq-noc | #81 | N2 | 46 | 2 | planned, rebase for CI | |
| amq-noc | #82 | N5 | 184 | 1 | planned, rebase for CI | |
| amq-noc | #83 | N6 | 207 | 1 | planned, rebase for CI | largest clean PR: the size cap source |
| amq-noc | #84 | N7 | 187 | 1 | planned, rebase for CI | |
| amq-noc | #85 | N8 | 78 | 1 | planned, rebase for CI | |
| amq-noc | #86 | N9 | 137 | 1 | planned, rebase for CI | |
| amq-noc | #87 | N13 | 75 | 2 | planned, rebase for CI | |
| workspace-cli | #216 | W7 | 119 | 1 | planned | first of the pair W7/W3 |
| workspace-cli | #217 | W3 | 143 | 2 | refused ci_green; after #216 (cmd/sheets_test.go) | CI fails: `gws sheets set-validation` not documented in SKILL.md; the task file set omitted SKILL.md |
| workspace-cli | #218 | W12 | 43 | 1 | planned | first of the pair W12/W5 |
| workspace-cli | #219 | W5 | 157 | 3 | refused task_scope; after #218 (internal/client/factory_test.go) | as predicted: factory_test.go is outside W5's declared files |
| workspace-cli | #220 | W2 | 141 | 2 | refused ci_green | CI fails on the undocumented new command; the task file set omitted SKILL.md |
| workspace-cli | #221 | W6 | 315 | 2 | refused ci_green and size_cap (315 > 300) | CI fails on the undocumented new command; the task file set omitted SKILL.md; over the cap |
| workspace-cli | #222 | W4 | 191 | 2 | refused ci_green | CI fails on the undocumented new command; the task file set omitted SKILL.md |
| workspace-cli | #223 | W8 | 172 | 1 | planned | |
| workspace-cli | #224 | W9 | 88 | 1 | planned | |

Against the S6 prediction ("16 clean, two predicted pairs that need ordering (W3/W7, W5/W12), one that fails the task file-set rule (W5)"):
- Matches: the two predicted pairs are the only overlaps the train finds, ordered W7 then W3 and W12 then W5. W5 is refused on the task file-set rule (`internal/client/factory_test.go`).
- Deviation 1 (CI, not measured by S6, which checked a cumulative `go build` only): W2, W3, W4 and W6 fail the repository's required CI. Their `Test` step's skills test reports `CLI command '...' exists but not documented in SKILL.md` and `references/commands.md` (W3: job 119626110384). These PRs failed because their task file sets omitted the documentation the repository's CI requires.
- Deviation 2 (protection): neither repository has the CI-only ruleset today.
- Deviation 3 (amq-noc CI): no amq-noc PR has a CI run at its head. All of them were opened on base 9bedf33, before N1 (#78, d7ff91b) added the workflow. The plan for each is rebase for CI (lead accepted).
- Result: 13 planned (9 amq-noc, 4 workspace-cli) and 5 refused (W5 on scope; W2, W3, W4 and W6 on CI; W6 also on size). With W5 refused, W12 needs no ordering at merge time. With W3 refused, W7 needs none.

### Credential custody: the measurement (lead ruling 12)

Evidence: `custody-measurement.txt` from `custody.sh`, read-only, printing names, counts and attributes, never a value. Measured 2026-10-24 13:28Z, uid 501, macOS 26.6.2:
- Environment: the owner's environment has `GEMINI_API_KEY`, `CLAUDE_CODE_MESSAGING_TOKEN` and `SSH_AUTH_SOCK`, and no `GH_TOKEN` or `GITHUB_TOKEN`. The seat-exec scrub removes all three. `SeatCredentialLeaks` over the real environment is empty.
- The gh keychain entry: the token is in the login keychain (`gh:github.com`, genp), scopes `gist, read:org, repo, workflow`. From a seat-like environment (HOME and PATH only), `gh auth status` still reports logged in, and `gh auth token` exits 0 with a 40-character token. The env scrub does not stop a same-UID seat from using the owner's GitHub identity.
- git: `credential.helper` is `osxkeychain`, and the remotes are https, so `git credential fill` hands out the same token.
- The SSH agent: `SSH_AUTH_SOCK` is scrubbed, but `launchctl getenv SSH_AUTH_SOCK` returns the launchd socket (mode srw-rw-rw-), which a seat can rediscover. Today it holds 0 identities, and `~/.ssh` holds no key files.

Ruling proposal for the lead (ruling 12), with the measurement above:
1. For 3.0 the env scrub plus guard's `credential_read` denials plus server-side protection is the custody. This is the same-UID threat plan 7.3 accepts.
2. The enforcement is the server: the CI-only ruleset refuses direct push (GH013) and requires CI. A seat that obtains the owner's token can still open or merge a PR through the API, around the train's six limits, but not around CI or the PR requirement.
3. Guard's denials are UX and evidence (R32), not a boundary.
4. Phase 5: a separate gh credential for the train in a keychain locked while seats run (a login keychain item cannot be hidden from the same UID), or a GitHub App token held by the harness. Seats need no GitHub credential at all, because the harness commits and pushes (amendment 21).
5. Follow-up for the plan: R33 says "seats hold no default-branch or release credentials". As measured, seats can reach them, so the plan text should say "seats are given none, and their reads are denied" until phase 5.

Deviations:
- The harness refused a separate `s6-dry-run/summary.md` ("subagents should return findings as text"). The annotated table therefore lives here and in the report to the lead, beside the raw outputs.
- `managed:` grant entries protect the whole file at the merge, which is stricter than guard: the train sees a diff, not the managed block's text.
- Entries outside the repository (`~/`, `/`) cannot appear in a PR diff and are skipped.

## Commit 2: the release gate flow on the compoundrelease writer

Built:
- `internal/releaseflow` (new):
  - `Spec` builds the compound release spec for one tag at one full commit SHA on one repository. The two child targets are `gatetarget.ReleaseTag` ("git tag <tag> <commit> on <owner/repo>") and `gatetarget.ReleaseCreate` ("gh release create <tag> on <owner/repo>"), so the owner's approval binds the commit and the repository.
  - `Prepare` writes the release into the compoundrelease store (`Create`, then `Reconcile`). Each child's question is a typed gate raised through 3-1's writer, record first and then the question. The gate id is derived from the child's attempt identity (`GateID`), so a retry finds the same gate and never posts a second question. A gate under that id that binds another action or target is a conflict, and the release goes to `conflict`.
  - `Publish` runs the children of the active release in order, the tag before the release, and each only after its gate's verified owner answer (`Accepted`, wired in the CLI to `gateverify.Verifier.Accepted`, the full owner-signature check). A child without one is refused (`ErrNotApproved`) and nothing after it runs. A child that ran writes a done marker (O_EXCL, `<root>/extensions/amq-squad/releases/<release id>/<role>.json`) and never runs again.
  - The executor `GH` runs under the owner's identity: the tag is a ref created through the API at the approved commit, and the release uses `--verify-tag`.
- `internal/compoundrelease/reconcile.go` (494 to 328, target 452, `shrunk`):
  - `Reconcile` is the 3.0 publication path. A planned release enters publishing, then each child in order is claimed, its gate raised or found through `ReconcileAdapter.ChildGate`, and adopted. The generation activates once both children are published. A child left `sending` by a crash asks for the same gate on the next reconcile. `ErrChildConflict` terminalizes the release.
  - The mailbox scan, the invocation types (`ReleaseChildInvocation`, `ReleaseChildInvokeOutcome`), `assessReleaseChild` and `invokeReconciledChild` are deleted.
  - The mailbox readers that `eligibility.go` and `guard.go` (keep rows, the v2 operator path) still call stay at the end of the file, with a comment naming 4-3 as their rewrite.
- `internal/cli/release_flow.go` (new): `amq-squad release prepare|publish`, refused in a seat process.
- `gatetarget`: `ReleaseTag`, `ReleaseCreate` and their convention lines.

Behavior changes named:
1. Compound release children adopt gate ids, not mailbox message ids. The v2 operator attention projection still reads mail, so it finds no counterpart for a 3.0 release.
2. New public verb `release`.

Tests:
- Acceptance 6, `TestAcceptance6ReleaseFlow`, on a fixture bare repository.
  - Prepare activates the release with two typed gates whose targets match the plan.
  - With no answer, publish refuses the tag and nothing runs.
  - With the tag's answer, the tag ref is created at the commit and the release is still refused.
  - With both answers, the release is created after its tag exists, and a retry runs neither child again.
- Also `TestPrepareIsIdempotentAndRefusesAForeignGate`.
- In compoundrelease: `TestReconcileRaisesBothChildGatesAndActivates`, `TestReconcileFaultSeamsConvergeOnOneGatePerChild` (after the claim and after the gate, for both children), `TestReconcileTransportFailureAndForeignGate`.
- Deleted with the mailbox reconcile, each replaced by the three reconcile tests above: `TestReconcileAcceptedSendPersistsTransportMessageIdentity`, `TestReconcilePreInvocationFailureRollsBackClaim`, `TestReconcileSuccessWithoutMessageIdentityIsAmbiguous`, `TestReconcileCrashRecoveryAdoptsExactMailboxMessageWithoutResend`, `TestReconcileDuplicateExactMessagesTerminalizeConflict`, `TestReconcilePublishedMessageIdentityDivergenceConflicts`, `TestReconcilePublishedMissingStoredMessageIsRecordInvalid`, `TestReconcileActivatesAfterBothExactMailboxMessages`, `TestReconcileNearMessageConflictsBeforeInvocation`, `TestReconcilePhysicalCopyAndWarningBarriers`, `TestReconcilePlannedEvidenceAndEarlySecondChildMatrix`, `TestReconcileFaultSeamsConvergeWithoutResend`, and the cli `TestCompoundReleaseReconcileUsesMailboxMessageIdentity`.
- Kept and moved into `mailbox_fixture_test.go`: the mailbox fixture the series-inspection tests use, plus `TestExactReleaseMessageRejectsMailboxIdentityMutations` and `TestClassifyReleaseMessageGroupsOwnershipBranches`, which cover the kept readers.
- The cli release fixture adopts its children's mail ids through `ChildGate`.
- Reverts (`reverts-c2.txt`): release_tag_unverified, release_runs_once, release_foreign_gate, reconcile_conflict, reconcile_sending_retry. Each fails on its assertion with the fix undone and passes after restore.

Deviation:
- The flow test's `Accepted` is a fake keyed by gate id. The owner-signature check behind it is 3-1's `Verifier.Accepted`, which is tested there, and the CLI wires the real one.

Not yet done: `compoundrelease/store.go` (1,515 / 1,385, deferred-3) waits for the lead's ruling between deleting the successor path and `keep`.

## Lead rulings after the halfway checkpoint (2026-10-24)

- Halfway (c39b7c7c) accepted.
- Ruling 12 (credential custody) is the proposal above. The reviewer confirmed it with stronger wording, because the ruleset does not cover everything the token can do: a seat that reads the owner's gh token cannot land on main, but it can create a tag or a release, open or close PRs, and read private repositories as the owner, and the typed gates bind only actions the core performs.
- Two additions came with ruling 12. The first is detection: doctor and the merge train compare the repository's tags and releases against the gate store at every run, and raise an owner alert for any tag or release without an accepted gate (commit 3). The second is the measured facts below. The R33 plan wording is the lead's.
- `GIT_ASKPASS` and `SSH_ASKPASS` stay scrubbed.
- store.go: the delete path is confirmed (PrepareSuccessor and completeSuccessor with their tests). Supersession reading stays. It goes to the reviewer before the merge, and a keep ruling would be restored in one commit.
- The 20-minute CI wait after a rebase stays a proposal.

### Credential custody: measured facts (`custody-measurement.txt`, 2026-10-24 13:28Z)

- The token is readable from a HOME-plus-PATH environment. `env -i HOME=$HOME PATH=$PATH gh auth token` exits 0 with a 40-character token from the login keychain item `gh:github.com`. The whole custody script (ten probes, gh's two network calls included) ran in about 2 s. The token read itself is a local keychain read with no prompt.
- The token's scopes are `gist, read:org, repo, workflow`. `repo` and `workflow` are enough to create tags and releases, open and close PRs, and read private repositories.
- git's `credential.helper` is `osxkeychain`, and the S6 remotes are https, so `git credential fill` hands out the same token.
- `SSH_AUTH_SOCK` is scrubbed from the seat environment, but `launchctl getenv SSH_AUTH_SOCK` re-exposes the launchd agent socket (`/var/run/com.apple.launchd.*/Listeners`, mode srw-rw-rw-). Today the agent holds 0 identities, and `~/.ssh` holds no key files.
- The env scrub removes everything credential-like in the owner's environment (`GEMINI_API_KEY`, `CLAUDE_CODE_MESSAGING_TOKEN`, `SSH_AUTH_SOCK`). `GIT_ASKPASS` and `SSH_ASKPASS` passed the scrub before commit 1.

## Commit 3: evidence from the ledger, verify and amq_send shrunk, tag and release detection

Built:
- `internal/ledger` (new) holds the readers of 3.0's evidence and carries the two semantics of the retired `commandevidence/evidence.go`:
  - `Path` and `Tail` read a seat's hook ledger.
  - `Redact` applies the secret-name rule (v2 `evidenceSecretName`/`secretName`; numbers such as token counts stay) and the secret-argument rule (v2 `obviousSecretArg`: `--token X`, `password=...`) to every string.
  - `ForTask` is the task link (v2 `evidenceTaskLink`): for every ended attempt and the live lease, the holder's ledger lines inside that lease's window.
  - `Outcomes` returns the outcome-log rows that name a task, at `OutcomePath` (`<root>/extensions/amq-squad/outcomes.jsonl`). The file name is provisional until 2b-4 writes the log.
  - `evidence list` now reads through it.
- `internal/cli/verify.go` (605 to 218, target 500, `shrunk`): `verify merge --repository R --pr N [--task ID]` and `verify release --tag T [--task ID]` read the evidence 3.0 keeps.
  - For a merge, the train's audit log: the last real entry is a merge whose head is the head of the last batch digest naming the PR.
  - For a release, the release flow's done markers and each child gate's accepted decision.
  - With `--task`, the task's ledger lines and outcome rows, redacted.
  - The v2 evidence-file validators, the co-sign schema and the review-rebinding consumer are deleted. `verify rebind` stays and records its artifact.
- `internal/cli/amq_send.go` (314 to 121, target 150, `shrunk`): the owned send boundary keeps the wait-posture guard, one AMQ invocation, the stable message id and the committed-delivery recovery. The v2 durable-invocation seam of the release answer path is gone, and with it `operatorSendOptions.Invocation` and the `reconciled_existing` status in operator.go.
- `internal/commandevidence/evidence.go` (1,657): `deleted`. `subject.go` keeps its two shape checks as regexps (397 to 400, target 400).
- Tag and release detection (ruling 12):
  - `mergetrain.Drift` compares a repository's tags and releases (`GH.Tags`, `GH.Releases`) against the session's gate store. The first run on a repository records the existing set as its baseline, under `<root>/extensions/amq-squad/alerts/`.
  - After that, every new tag or release needs an accepted tag or release gate for it: the release flow's targets, or a seat's gated command (`git tag <name>`, `git push refs/tags/<name>`, `gh release create <name>`).
  - Each uncovered one is written once as an owner alert.
  - The merge train runs it at every run, dry runs included, and records an `alert` audit entry and `Result.Alerts`.
  - doctor runs it for every repository with a merge grant (`doctor_release_drift.go`, one hook line in doctor.go) and fails the check.
  - The status contract reads the alerts into needs_you attention (`alert:<id>`, kind `ungated_tag` or `ungated_release`, severity high).

Behavior changes named:
1. `verify merge --evidence` and `verify release --evidence` are gone. Both verbs read the audit log, the release records and the ledger.
2. The operator send's `reconciled_existing` status is gone.
3. `evidence list` also redacts secret-looking arguments.
4. doctor makes read-only gh calls for each repository with a merge grant.
5. status shows owner alerts under needs_you.

Tests:
- `internal/ledger`: `TestRedactKeysAndArguments`, `TestForTaskKeepsLeaseWindowsOnly`, `TestOutcomesNameTheTask`.
- cli: `TestVerifyMergeReadsTheAuditLog` (merged, refused, unknown, head not the digest's), `TestVerifyReleaseReadsTheFlowRecords` (tag only, release gate not accepted, both), `TestVerifyRequiresKnownSubcommand` (kept), `TestDoctorReleaseDriftFailsOnAnUngatedTag`.
- mergetrain: `TestDriftAlertsOnUngatedTagsAndReleases` (baseline, an ungated tag and release, then the tag covered by an accepted gate, three audit alert entries).
- status: `TestOwnerAlertBecomesNeedsYouAttention`.
- Deleted with the v2 validators: verify_test.go (`TestVerifyMergePassesCleanEvidence`, `TestVerifyMergeReportsFailuresInJSON`, `TestVerifyMergeSuccessJSONIncludesEvidenceSummary`, `TestVerifyMergeFailureJSONOmitsEvidenceSummary`, `TestVerifyMergeRejectsMissingEvidenceFlag`, `TestVerifyMergeRejectsUnknownFields`, `TestVerifyMergeFailureCodes`, `TestReadVerifyMergeEvidenceFromStdinReader`), verify_release_test.go (`TestVerifyReleasePassesExactSHACosign`, `TestVerifyReleaseJSONEnvelope`, `TestVerifyReleaseFailureCodes`, `TestVerifyReleaseGatesAreNonSubstitutable`), and in verify_rebind_test.go `TestVerifyMergeAcceptsPatchIDRebindingOnlyAtRecordedNewBase`, `TestVerifyMergeRejectsUnnecessaryRebinding` and the merge-consumption halves of the two kept rebind tests. They are replaced by the two verify rows above.
- commandevidence/evidence_test.go is deleted with its file. `TestRunOwnedAMQSendReconcilesWithoutTransportInvocation` and the two reconciled-send subtests of `TestSendOperatorAMQTransportAndOnSentEventOrder` are deleted with the durable seam.
- Reverts: `reverts-c3.txt` holds ledger_secret_names, ledger_secret_args, ledger_task_window, verify_merge_digest and verify_release_gates. `reverts-c3b.txt` holds drift_alerts, drift_gate_covers, status_owner_alert and doctor_release_drift. Each fails on its assertion and passes after restore. A first try of ledger_task_window and drift_alerts produced a build failure rather than an assertion failure; both were redone so the row fails on its assertion.

## Commit 4: compoundrelease/store.go, the successor writer out of production (lead ruling)

- `internal/compoundrelease/store.go` (1,515 to 1,380, target 1,385, `shrunk`): `PrepareSuccessor` and `completeSuccessor` are removed from production. In 3.0 a changed release spec is a new tag, a new parent gate and so a new series (ruling). `ErrSpecChangeRequiresSuccessor` now says so.
- The store still reads and validates the superseded and multi-generation states a v2 release left on disk: the lifecycle validator, the predecessor chain, series inspection and the invocation guard. Supersession reading stays, as ruled.
- Deviation from "with their tests", recorded for the reviewer: the two functions moved into `successor_fixture_test.go` (package-internal, test-only). The reader tests still build those on-disk states with them, so the kept validators keep their coverage.
- Deleted, because they test the writer as a production API: `TestStoreSuccessorOrderingAndRecovery`, `TestStoreActivateVersusSuccessorRace`, `TestStoreConcurrentSuccessorPreservesExactChain`, and in cli `TestCompoundReleaseChildrenTombstoneOnInactiveSuccessor`. The cli package cannot reach a test-only method of compoundrelease.
- If the reviewer rules keep, one commit restores both functions to store.go and the four tests.
- Acceptance 7: `scripts/manifest-check.sh --final` shows no deferred-3 and no deferred-replace row (class 2: 0 lines).

## End checkpoint

| Acceptance | Status | Evidence |
| --- | --- | --- |
| 1 fixture train, five PRs in order, digest, rebase re-digest and re-check | met | `TestAcceptance1FiveCleanPRsLandInOrder` (6.5 s with the fake gh over a bare repository) |
| 2 refusals, one test each, gate raised, nothing merged | met | `TestAcceptance2Refusals` (7 rows: CI red, size cap lines, size cap files, force push, protected path, wrong base, file outside the task) |
| 3 S6 dry run | met, with three measured deviations | `s6-dry-run/` pass 1, 2 and 3; table in commit 1 |
| 4 S6 real run | not run (not this brief's; waits for the owner's merge grant and the CI-only rulesets) | |
| 5 no ruleset, train refuses to start | met | `TestAcceptance5NoRulesetRefusesToStart` |
| 6 release flow on a fixture repo | met | `TestAcceptance6ReleaseFlow` |
| 7 no deferred-3, no deferred-replace | met | `gate-c4.txt` manifest-check --final |

Manifest statuses of this brief's rows:
- `compoundrelease/store.go`: shrunk 1,380 / 1,385.
- `compoundrelease/reconcile.go`: shrunk 328 / 452.
- `commandevidence/evidence.go`: deleted.
- `cli/evidence.go`: already deleted by 2b-1.
- `cli/verify.go`: shrunk 218 / 500.
- `cli/amq_send.go`: shrunk 121 / 150.
- New rows: mergetrain audit, checks, custody, drift, escalate, forge, gh and train; releaseflow releaseflow and gh; ledger; cli merge_train, release_flow and doctor_release_drift.

Train wall time for the S6 set:
- Dry run (pass 3, with the drift check): 19.2 s for amq-noc (9 PRs, 50 gh calls) and 20.2 s for workspace-cli (9 PRs, 50 gh calls), so 39.4 s for the 18 PRs, about 1.1 s per PR. Each gh call is a network round trip.
- Pass 3 also recorded the drift baselines: amq-noc 16 tags and 16 releases, workspace-cli 55 tags and 46 releases.
- A real run adds, per PR, a ready, a merge and, where needed, a rebase plus the CI wait on the new head. The CI wait dominates and is a proposal (20 min maximum), not measured: acceptance 4 was not run.

Defaults measured (ruling 14):
- Size cap: 300 lines and 5 files (ruled).
- CI wait after a rebase: 20 min, a proposal.
- Gate window for refusal and release gates: 3-1's 30 minutes, unchanged.

## Commit 5: fix 1, the Codex pass on 700b1940 and the six held rulings

One commit with a test row per item. Every row fails on its assertion when its fix is reverted and passes when restored (`fix1-revert-check.txt`, 23 rows, driver `revert_fix1.py`). Gate output: `gate-fix1.txt`.

The first gate run is kept as `gate-fix1-race-timeout.txt` (it predates the rename of the squad field to 4-5's `squad`). Its race run of `internal/mergetrain` hit go test's default 10-minute timeout. Every package was green without race. Under race the fake gh is the race-built test binary itself, so each of its now 68 calls per run is slow, and the timeout came during `TestAuditFailureStopsBeforeAnyMerge` at 4 s of its own time. `gate.sh` now passes `-timeout 45m` to the race run. The second run was stopped (TaskStop) when 4-5's field name arrived; the harness denied a `pkill` of its children, but none were left running. The gate was run again after the rename.

| # | Finding | Fix | Test row |
| --- | --- | --- | --- |
| P1-1 | ci_green ignored the check's app | `RequiredCheck{Context, AppID}` from the rule (ruleset `integration_id`, classic `checks[].app_id`). A pinned app counts only by that app's latest result (highest check-run id). A rule that pins no app needs every same-named result green | `TestRequiredCheckIsBoundToItsAppAndLatestResult` (6 unit cases, the integration id read, and the fixture end to end with a second app's success) |
| P1-2 | bypassable protection counted toward Met | A ruleset with bypass actors contributes no required check, and classic required checks without enforce_admins do not count. Notes give the reason | `TestBypassableProtectionIsUnmet` |
| P1-3 | diff and head were separate snapshots | `Forge.Diff` reads the compare endpoint for the pinned base...head pair (never `pulls/N/files`). The head is re-read after the checks and a moved head is checked again (up to 3 tries, then an error). A diff at the compare endpoint's 300-file limit is a size_cap refusal | `TestHeadMovingDuringTheCheckIsReadAgain` (also asserts no `/files` call) |
| P1-4 | a request could carry declared_files | In a real run declared files come only from the task store. A request with declared_files, or a PR without a task, is refused before anything is resolved. The dry run keeps the feature (the S6 run) | `TestMergeTrainRealRunTakesDeclaredFilesOnlyFromTheTaskStore` |
| P1-5 | GIT_CONFIG_* reached the seat | `seatexec.NeverNeeded`: the `GIT_CONFIG_*` family and GH_TOKEN, GITHUB_TOKEN, GH_ENTERPRISE_TOKEN, GITHUB_ENTERPRISE_TOKEN and GH_CONFIG_DIR are scrubbed even when declared. A launch record that declares one in env_needs is refused (`env_needs_refused`) before anything is published | `TestGitConfigFamilyAndGHTokensNeverReachASeat` |
| P1-6 | a tag moved after the approved tag step was released | Before each step the flow resolves the remote tag (`Executor.TagCommit`). A tag at another commit is `tag_moved`: nothing is published and the owner alert is raised (`mergetrain.WriteAlert`; status shows kind `tag_moved`) | `TestReleaseRefusesAMovedTag` |
| P1-7 | argv redaction was element by element; numbers kept | The element after a secret flag in an argv array is redacted. `flag=value` keeps the key and redacts the value. Command strings are split quote-aware, so a quoted secret is one word. `Bearer`/`Basic` credentials and `Authorization:` headers are redacted. Only numeric count fields (`*_tokens`) stay | `TestRedactArgvQuotesAndNumbers` (8 cases) |
| P2-8 | protected paths compared case-sensitively | case-folded match | `TestProtectedPathsAreCaseFolded` |
| P2-9 | manual confirmation for head A used for head B | The confirmation is asked inside the landing step after the final rebase, check and digest, for that head and digest. A push after it is refused by the pinned merge | `TestManualConfirmationIsForTheHeadThatMerges` |
| P2-10 | a crash after the tag made every retry fail | `Publish` holds a per-release `publish.lock`. A remote tag at the approved commit with no done marker is reconciled (recorded as ran, no second tag) and the flow continues | `TestRetryAfterACrashReconcilesTheTag` |
| P2-11 | audit failures were ignored | The first failed append is kept, and no merge starts after it (checked before each landing and right before each merge) | `TestAuditFailureStopsBeforeAnyMerge` |
| P2-12 | a recreated release escaped drift | Releases compare by id, tags by name and commit. One accepted gate covers one object (`alerts/covered-<gate>.json`), so a recreated release or a moved tag needs a new gate | `TestDriftBindsAGateToOneReleaseAndOneTagCommit` |
| P2-13 | an absolute path to git-credential-osxkeychain was not classified | Any `git-credential-*` executable, by path or name, is credential_read | `TestGuardRuleMatrix` rows `cred/deny standalone helper by path`, `... on PATH` |

The six held rulings:
1. store.go: `PrepareSuccessor` and `completeSuccessor` are deleted, not kept as a test fixture. `successor_fixture_test.go` now writes the superseded and pointer-ahead states as data, file by file, under the store lock (`writeSuccessorState`, `writePointerAheadSuccessor`).
   - Deleted, because they tested the writer's own crash recovery: `TestPointerAheadSuccessorRealCrashInspection` and the five `successor_*` phases of `TestMutableRealCrashRecoveryAcrossLifecycle` and its subprocess helper.
   - `TestPointerAheadSuccessorInspectionIsNonMutating` now writes its pointer-ahead state.
2. The task-scope matcher: milestone B is not merged, so `mergetrain.Declared` stays. The swap to `task.MatchPath` is named for the rebase.
3. The outcome log is read at `<root>/extensions/amq-squad/routing/outcomes.jsonl` (2b-4's path).
4. verify_action.go: `default_branch_push` and `protected_branch_push` are refused with `merge_train`, because the train replaces them. `feature_branch_push` keeps the v2 path. The seven tests that used the two kinds as a vehicle now use `feature_branch_push`: `TestVerifyActionBlocksGatedReleaseBeforeOperatorAnswer`, `TestVerifyActionPassesBoundOperatorAnswer`, `TestVerifyActionLatestOperatorAnswerWinsOverOlderApproval`, `TestVerifyActionRejectsApprovedAnswerForDifferentTarget`, `TestVerifyActionRejectsProseOnlyOperatorAnswer`, `TestVerifyActionDeniedExitCode`, `TestVerifyActionEmitsSignedAuthorizationOnlyWithProvisionedKey`. New row: `TestVerifyActionRefusesMergeKindsForTheTrain`. The row stays at its target (700 / 700, two comment lines reflowed to make room). Its note says the train replaces the kinds.
5. The drift baseline is a recorded limit: what exists at a repository's first run is the baseline, so the owner's first run is the baseline review.
6. Required companions: `merge.required_companions` (globs) is a grant field with a documented empty default. The owner names the paths, and the lead's widening path adds them. Row `TestMergeSizeCapDefaultsAndNarrowing`.

Addition from 4-5's re-pass, squad identity:
- The gate record gains 4-5's field `squad` (c3859fec): the root-sha256 of the gate store's root. 4-5's `Store.Raise` fills it and its verifier refuses a record without it.
- On this branch the field is added to `gatestore.Record` and `Spec` with the same name and JSON key, so the rebase onto 4-5 keeps one field.
- The merge train's escalator and the release flow set it on every gate they raise (`mergetrain.SquadOf(root)`).
- Matching an accepted answer to a record reads it: the train's lift (`InSquad`), drift coverage, and the release flow's adoption and publish checks.
- A record from another squad does not match. Until the rebase, a record with no `squad` still matches; 4-5's verifier refuses it after the rebase.
- Rows: `TestGatesCarryAndMatchTheSquadIdentity`, `TestReleaseGatesCarryTheSquadIdentity`.

S6 dry run again with the fixes (pass 4, read-only): the plan, the refusals and both batch digests are unchanged. gh calls rose from 50 to 68 per repository, for the compare endpoint and the head re-read. Wall time is 27.7 s for amq-noc and 29.0 s for workspace-cli.

## Commit 6: fix 2, the second Codex pass on 87e45acd

The second pass found P1-1 (for app-bound rules), P1-3, P2-8, P2-9, P2-10, P2-11 and P2-13 complete. The rest were partial, and it found three new defects.

This commit adds a test row per item. Every row fails on its assertion when its fix is reverted and passes when restored (`fix2-revert-check.txt`, 14 rows, driver `revert_fix2.py`). The gate ran on a quiet host: `gate.sh` now lists any seat-exec, scheduler run, pi run, amq coop or amq-remote process before it starts (`gate-fix2.txt`).

| # | Finding | Fix | Test row |
| --- | --- | --- | --- |
| P1-1 | a rule naming no app accepted any app's success | A required check without an app does not count. The protection is unmet with `ci_app_unbound: ... bind it to its app in the ruleset`. The fixture's CI is now app-bound (GitHub Actions, 15368). A dry run's request-named checks (app 0) keep the every-result-green rule | `TestRequiredCheckWithoutAnAppIsUnmet` |
| P1-2 | classic bypass_pull_request_allowances ignored | Any allowance (users, teams, apps) means direct push is not refused; unmet, with the note | `TestClassicPRBypassAllowancesAreUnmet` |
| P1-4 | the train trusted the request's task id | Lead ruling: a real run's request names PR numbers only. A task or declared_files in it is refused. `mergetrain.BindTasks` reads the task the harness commit step bound to the PR, through milestone B's fields `pr_number` (int) and `pr_head` (string), written by `task.Store.BindPR`. They are read by name until milestone B merges. No bound task, or more than one, is `task_unbound`. A PR head that differs from `pr_head` is `pr_head_moved`: a push after the binding re-binds through the commit step first. The train's own rebase heads (from the audit log) are exempt, so a rebase inside the run does not refuse its PR. This exemption is mine; the lead's message did not name it | `TestRealRunReadsTheTaskBoundToThePR`, `TestMergeTrainRealRunNamesPRNumbersOnly` |
| P1-5 | git transport and trust variables reached the seat | GIT_SSH_COMMAND, GIT_SSH, GIT_PROXY_COMMAND, GIT_EXEC_PATH, GIT_TEMPLATE_DIR, CURL_CA_BUNDLE and SSL_CERT_FILE are in `NeverNeeded`: scrubbed even when declared, and refused in env_needs | `TestGitTransportAndTrustVariablesNeverReachASeat` |
| P1-6 | check-then-release is two remote operations | Lead ruling. (a) Before anything runs, the release flow requires an active tag ruleset that covers the tag and carries update and deletion rules (`GH.TagProtected`, `TagRuleCovers`); otherwise `protection_unmet` and nothing runs. (b) The release is created as a draft and read back; its tag is resolved and compared to the approved SHA through the API, and only then published. A mismatch deletes the draft and raises `tag_moved` | `TestReleaseRequiresATagRule`, `TestDraftReadBackCatchesAMovedTag`, `TestTagRuleCoversPatterns` |
| P1-7 | basic-auth URLs kept; every `*_tokens` exempt | URL userinfo is redacted. The count exemption is the explicit list input_tokens, output_tokens and cache_*_tokens | `TestRedactURLsNestedShellAndCountList` |
| N1 | a nested `sh -c '...'` was one opaque word | Quoted words are scrubbed again inside (any depth). `;`, `&` and `|` end a word. A line that ends inside an open quote falls back to the whitespace scanner. There is no revert row for the fallback alone: an open quote becomes one quoted word, and the recursion already redacts it | same row |
| N2 | classic `contexts` added a wildcard check beside the app-bound one | Only app-bound `checks` count | `TestClassicContextsAddNoWildcardCheck` |
| N3 | the baseline format changed with no migration | The first format (releases as tag names) is read once and rewritten in the object form, each tag taking its releases' current ids. A baseline that does not decode stops the train (`drift_baseline_invalid`) and fails the doctor check, never a silent continue | `TestDriftBaselineMigratesOnceAndAnInvalidOneStops` |

Note for the reviewer: while I rewrote the P1-4 CLI test, one run of the previous version reached `gh api repos/o/r/rules/branches/main` on github.com (a read-only call for a repository that does not exist, answered 404). The new test only exercises the refusals that happen before any network call.

## Open rulings

1. Required companions: the field exists with an empty default (fix 1). The owner names the paths per repository. For workspace-cli the S6 evidence points at `skills/*/SKILL.md` and `skills/*/references/commands.md` beside a new `cmd/*.go` command.
2. Credential custody (ruling 12): ruled; the R33 plan wording is the lead's.
3. Acceptance 4 (the real run) waits for the owner's merge grant and for the CI-only rulesets on both repositories.
4. Task-scope matcher: ruled to be milestone B's `task.MatchPath`. Swap `mergetrain.Declared` for it at the rebase if milestone B lands first.
5. Outcome log: ruled to be 2b-4's `<root>/extensions/amq-squad/routing/outcomes.jsonl`. Its row schema (the `task` field) is 2b-4's to confirm.
6. Squad identity: 4-5's field `squad`. At the rebase take 4-5's `Store.Raise` fill and its root-sha256 format, and drop `mergetrain.SquadOf` if 4-5 exports one.
7. Recorded limits: the drift baseline (the owner's first run is the baseline review), the same-UID threat (plan 7.3), and the 20-minute CI wait after a rebase (a proposal).
8. Task-to-PR binding: milestone B's `pr_number` and `pr_head` are read by name; switch to milestone B's type at the rebase. The train exempts its own rebase heads from `pr_head_moved`, which needs the lead's confirmation.
9. P1-6 residual: between the draft read-back and the publish call, a tag update is stopped only by the tag rule. The owner identity can bypass it, so a seat holding the owner's token is the same-UID threat plan 7.3 accepts.
