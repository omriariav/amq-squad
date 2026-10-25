# Phase 5: amq-squad uninstall (execution log)

Branch `v3-5-uninstall`, from `v3` at 76f9c896. Plan 4.3 (per-seat config dirs, the token file, the core tools dir), 4.5 (the scheduler process, its socket and exits queue), 7.2 and R16 (the owner files; amq-squad never writes the owner's global agent config), 16.4 (the uninstall entry). Evidence in `5-uninstall-evidence/` (`gate.txt`, `revert-check.txt`, both through `scripts/redact-evidence`). The user-facing description is `docs/uninstall.md`.

## What changed

- `internal/uninstall` (new): `Run(Options)` builds the plan from the layout (read only), checks for anything live, and with `Write` executes it. Files: `uninstall.go` (options, coded errors, Run), `plan.go` (discovery through the fsanchor walk, the grouped plan), `busy.go` (the `uninstall_busy` checks), `execute.go` (group order, removal, secret overwrite, the report).
- `internal/cli/uninstall.go` (new) and two lines in `command_registry.go` plus the catalog row in `cli_test.go`: the verb `amq-squad uninstall [--root | --project --session] [--write [--owner-files] [--purge]]`. Exit codes: 0, 3 on a partial failure (`PartialError`), 2 on `uninstall_busy` and `uninstall_refused_symlink`, 1 on usage.
- `internal/seatcommit/evidence_purge.go` (new, additive): `Ledger.EvidenceRefs()` and `DeleteEvidenceRefs`, the purge's one git call, through the commit step's hardened environment (`harnessEnv`, `safeConfig`, `seatRepo`). Only refs under `refs/amq-squad/evidence/` are accepted.

## Rules and rows

One failing-when-reverted row per rule and sub-rule (`5-uninstall-evidence/revert-check.txt`, 40 reverts after the fix round, each seen to fail on its assertion and not at the build, then restored and seen to pass).

| rule | row (test) |
| --- | --- |
| U1 no flag, nothing changes (dev, inode, mtime, mode, size across root, home, project); every group and the by-hand commands named; `--owner-files`/`--purge` without `--write` still change nothing | TestDryRunChangesNothingAndNamesEveryGroup (uninstall), TestUninstallVerbIsADryRunUnlessWriteIsGiven (cli) |
| U1 owner files only with `--owner-files` | TestSealedLedgersAndEvidenceAreKeptByDefault, TestSecretsStayWithoutOwnerFiles |
| U2 busy, one row each: scheduler lock held; decision socket open; live seat; open proof window; commit step in the exits queue. Each also under `--all`, naming what is live, tree unchanged | TestRefusesWhileTheSchedulerLockIsHeld, ...TheDecisionSocketIsOpen, ...ASeatIsLive, ...AProofWindowIsOpen, ...ACommitStepIsInTheExitsQueue; TestTheDecisionSocketNameIsTheSchedulersOwn holds the repeated socket name to schedproc's |
| U3 the owner's global agent config is untouched (`~/.claude` incl. the plugin cache, `~/.codex/config.toml`: dev, inode, mtime, bytes), the plugin uninstall command and the codex trust entry are printed | TestNeverWritesTheOwnersGlobalAgentConfig |
| U4 sealed ledgers, evidence refs, clear records and gate store kept by default | TestSealedLedgersAndEvidenceAreKeptByDefault |
| U4 `--purge` removes the evidence refs through the hardened git only: a `reference-transaction` hook in the seat repository does not run; branches, HEAD and the tree object remain | TestPurgeRemovesEvidenceRefsOnlyAndRunsNoRepositoryHook |
| U5 secrets first and unconditionally (another group fails, they still go), overwritten once with zeros (read back through a descriptor held open across the unlink), never printed | TestSecretsAreZeroedThenUnlinkedFirstAndNeverPrinted |
| U6 a symlink in place of a worktree dir (and of the worktrees dir, the extension dir, a tools dir, a seat config dir, `~/.amq-squad`) is `uninstall_refused_symlink`, nothing removed, the link's target untouched | TestASymlinkWhereADirectoryIsExpectedIsRefusedAndNothingIsRemoved (six subtests) |
| U7 idempotent: a second run reports nothing to do (after the default run and after everything) | TestASecondRunHasNothingToDo, TestUninstallVerbWriteIsIdempotent |
| U7 a partial failure (the extension directory made read-only) lists what remains, the others still run, exit non-zero (3 through the verb); a retry finishes and a third run has nothing to do | TestAPartialFailureReportsWhatRemainsAndFails, TestUninstallVerbPartialFailureExitsNonZero |

The fixtures are real: a git repository with a linked worktree under `.claude/worktrees`, a real evidence ref, a commits ledger written from `seatcommit.Ledger`, launch records from `launch.Record`, a scheduler directory with a lock file, flock held in the test for the busy row. No network, no environment in any failure message, no credential-shaped literal (the two secret files hold plain placeholder strings that are only compared, never formatted).

## Judgment calls

- Everything the brief lists as "core tools dir" and "verifier module cache" lives under the AMQ root's `extensions/amq-squad` (`tools/`, `verifiers/gomodcache`), not under `~/.amq-squad`. The tools dir has its own group; the module cache is listed (annotated) with the `verifiers` entry in session state and goes with `--write`, not `--owner-files`: it is a rebuildable cache beside the session, not an owner file.
- "A live seat (any pane with a staged record)": a seat is live when its accepted proof entry (`launch/<seat>/proof.json`) is not closed and the pid carries the recorded start stamp, the test the scheduler itself uses on restart (`schedproc.LiveSeats`). A staged record with no proof entry is not counted; its proof window, if still open, is.
- An open proof window is a staged ticket whose token is neither consumed nor expired and whose 60 s have not run out, whatever the launcher's liveness (fail closed). An unreadable exits queue counts as a possible commit step, and an unprobeable lock as live.
- The decision socket counts as open when its node is in the launch directory and the scheduler launch record's pid is alive (or the record is unreadable); the lock check is the authoritative one.
- A secret that is a symlink is refused (`uninstall_refused_symlink`) rather than unlinked: overwriting through it is never done and the owner may have linked it on purpose.
- Evidence refs are the ones the commits ledgers record (read as JSON, not through the seal check, so a tampered ledger does not stop a purge). They run before the worktrees go; the repository is reached through the ledger's worktree, else the scheduler launch record's project root, else the staged records' worktrees. If none resolves the ref group fails and is reported as remaining.
- Worktrees are those named by the commits ledgers and staged launch records whose path is exactly `<project>/.claude/worktrees/<name>`; others are left. The directory is removed; `git worktree prune` is printed, not run (the seat repository's bookkeeping is not touched). Uncommitted work in them is lost, and the report says so.
- A path whose ancestors cross a symlink (macOS `/tmp`, `/var`) is refused by the anchor, so the AMQ root and project paths must be the resolved ones.
- `~/.amq-squad` entries that are not the four owner files (`bin`, `runtime`, `fleet.yaml`, `authorization-trust.json`, `issuer-key.pem`, ...) are listed as "left in place", not removed.
- The extension directory, and `tools/`, are removed when empty after the run; the AMQ mailboxes and the seat-exec published copies of launch records in them are AMQ's and stay.
- The seatcommit helper is the one edit outside the listed packages (a new file, additive), so the purge reuses the commit step's hardened git instead of a second copy.

## Fix round (reviewer's narrow pass on 829c108f)

One fix commit; each item has a failing-when-reverted row (`5-uninstall-evidence/revert-check.txt`, 40 reverts in all, none a build failure).

| item | fix | row |
| --- | --- | --- |
| P1-1 a symbolic evidence ref deleted a branch | refs are listed by git itself (`for-each-ref --format='%(refname) %(objectname) %(symref)'`, this squad's seats only), never taken from a ledger; a symbolic ref refuses the repository (`evidence_ref_symbolic`, nothing deleted there); deletion is `update-ref --no-deref -d <ref> <oid>` | TestASymbolicEvidenceRefNeverDeletesABranch, TestARefReplacedSinceItWasListedIsLeft, TestALedgerNamingABranchAsEvidenceDeletesNothing |
| P1-2 busy failed open | every read, open, list or probe error in busy.go is busy with the path; unsupported locking is busy; the live-seat check is `schedproc.LiveSeats` | TestEveryUnreadableBusyInputRefuses (five inputs) |
| P1-3 a staged record aimed the removal elsewhere | a worktree is removed only when its project is a `project_path` the task store records for the seat, `git worktree list --porcelain` of that project (hardened) names it, and its `.git` resolves into the project's `.git/worktrees` (`seatcommit.VerifyWorktree`); the rest is listed "not removed" with the reason | TestAStagedRecordCannotAimTheRemovalAtAnotherProject (a real worktree of another project; a plain directory in the right one) |
| P2-1 retry information destroyed | a failed evidence group holds back the worktrees and the records (ledgers, staged records, tasks); a failed worktree group holds back the records; held-back entries are in the report | TestAFailedEvidenceGroupKeepsTheRecordsAndTheRetryFinishes |
| P2-2 nested symlinks | every directory below the extension directory to three levels is opened O_NOFOLLOW in the discovery pass | TestASymlinkedNestedDirectoryIsRefused |
| P2-3 hard-linked secret | a link count above one: unlinked without zeroing, a note says why; documented | TestAHardLinkedSecretIsUnlinkedWithoutZeroing |
| P2-4 no exclusion | a writing run that has work takes the scheduler's own lock (`scheduler/run.lock`, created if absent and removed again if it created it), plans again and re-runs the busy check with it held; a second uninstall or a scheduler gets the lock refusal | TestAWritingRunHoldsTheSchedulersLock, TestTheBusyCheckRunsAgainWithTheLockHeld |
| P2-5 false success | only a missing entry is absent; any other open error is a failed entry (partial, exit 3) | TestAnUnreadableTargetIsAFailedEntry |
| codes | busy and symlink are an explicit `codedExitError` with `ExitSystem` | TestUninstallVerbRefusalsExitWithTheSystemCode |

Judgment calls of the round: (a) a run whose plan is empty takes no lock, so the idempotent second run touches nothing; (b) if the lock file or its directory cannot be created or opened the run is refused as busy rather than run unlocked; (c) the task store is read through `task.Snapshot` (no lock, no write); a seat with no task naming its project keeps its worktrees, listed; (d) the first-round rows are re-confirmed against the changed code, with the patterns that moved updated in the revert script.

## Not done

- No `--json` envelope and no `--yes`-style prompt: the dry run is the confirmation.
- The `claude plugin uninstall` and codex edits are printed, never run (R16).
- No live run against a real squad; the fixtures stand in.
- The overwrite of a secret is best effort on APFS and other copy-on-write file systems (the doc says so).

## Gate

Quiet host (`ps -axo command` showed no seat-exec, scheduler run, pi run, amq coop, amq-remote or amq-squad lead/team/launch process). Output in `5-uninstall-evidence/gate.txt`; the revert check in `5-uninstall-evidence/revert-check.txt`.
