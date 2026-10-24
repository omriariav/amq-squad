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

## Open rulings

1. Required companions (reviewer addition): a per-repository list in the merge grant of files the repository's CI requires beside a change, which the lead's widening path adds to a task's declared_files by default. For workspace-cli: `skills/*/SKILL.md` and `skills/*/references/commands.md` beside any `cmd/*.go` that adds a CLI command. The exact paths need the owner's confirmation. Evidence: W2, W3, W4 and W6. 2b-4 gets the matching class-table item from the lead.
2. Credential custody (ruling 12): the proposal above.
3. Acceptance 4 (the real run) waits for ruling 1 (the owner's merge grant) and for the CI-only rulesets on both repositories.
4. The task-scope matcher `mergetrain.Declared` duplicates what milestone B's commit-step check will need. One of the two should own it when both land (proposal: `internal/task`).
5. The outcome log reader for merge and release evidence waits for 2b-4's file (see commit 3).
