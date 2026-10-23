# amq-squad 3.0 phase 1 spike: final report

Written 2026-10-22 10:1x by the operator session, branch `v3` (nothing pushed). Each line
links to the task report that holds the evidence. Status words: PASS, PASS with a stated
limit, NOT PASSED, PENDING. The phase 1 plan is `docs/v3-phase1-plan.md`; the plan under
revision is `docs/v3-plan.md` 2.3.

## The five criteria

| C | Criterion (phase 1 plan) | Status | Evidence | What is left |
| --- | --- | --- | --- | --- |
| 1 | An owner-signed gate answer from the owner's Buzz client verifies in Go; every negative case is rejected | **PASS** (2026-10-22, real key) | `S1-report.md` section 12 and `S1-evidence/realkey/`: gate g-owner-02 answered from Buzz Desktop in the thread, verifier approve, envelope ok, replay / mismatch / wrong-pubkey rejected, 23.5 s; fourth Codex review ACCEPT; 97 tests green | Two frictions fixed in the scripts (body needs a Buzz profile; answer must be a thread reply); both become 3.0 defaults |
| 2 | A seat's PR merges under the owner's identity on a personal repo with CI-only protection, with the typed gate as the review | **Mechanism PASS; signature step dropped by owner decision** | `S2-report.md`: amq-noc #78 merged d7ff91b through a CI-only ruleset; direct push refused (GH013); `merge_queue` rule rejected on a user-owned repo (422); synthetic-signer gate approve + envelope ok | Owner decision 2026-10-22: merges are lead rules decisions under a standing grant enforced by the Go core; owner-signed gates only for tag/release, external send, credential change, growth beyond grant. Goes into plan 2.4 (R30, 7.1, 7.3) |
| 3 | A darwin-arm64 binary starts the pi lead from the embedded, unpacked runtime with no pi or Node on PATH | **PASS** | `S3-report.md` | none |
| 4 | One Claude seat and one Codex seat launch through the launch API with model and effort in the trust digest; the wrapper sets only environment and role | **PASS** (2026-10-23, AMQ v0.106.1) | `S4-report.md` section "Re-check on AMQ v0.106.1" and `spike/seatexec/evidence/recheck-106/`: Apply exit 0, Codex conversation `pending` then `ready` with the real thread id after `__codex-notify`, title-thread notify did not bind, operator notify forward ran, Claude record `backend: tmux`, resume applied once and the same Codex thread resumed; both role acks; codex-cli 0.163.0, gpt-6-astra. Earlier: NOT PASSED on 0.102.0 and 0.104.0, fixed upstream by PR #1174 (issues #1157, #1158 filed from this spike) | Codex column of criterion 5 (S6 variant 2) can now run; pi adapter still upstream (#1153) |
| 5 | The five load-test numbers exist (a usage limit is a result, not a failure) | **PASS (Claude column); Codex column PENDING** | `S6-report.md` results of 2026-10-22: 21 tasks, 19 PRs in 10m09s (median 0m52s per task), 1 honest FAIL, 1 unclaimed; 0 usage-limit or 429 events; peak seat RSS 4.19 GB, avg 3.45 GB; 0 prompt stops; 2 predicted cumulative conflicts, 0 real. Load was 10 min of work inside a 3.39 h window; time to first limit not reached | Owner: the 12-seat cap (about 8.4 GB extrapolated); what to do with the 18 open draft PRs. Variant 2 (Codex seats) waits for v0.104.0 |

Side tasks: S5 (pi seat on Ollama under `coop exec`, R12 probe, hold seam, pi lead loop)
PASS, `S5-report.md`. S7 keep/delete manifest done, `S7-manifest.md`. S8 upstream asks
drafted, `docs/v3-upstream-asks.md` (14 asks; 1, 4, 5, 7, 8 ready to file; 9 shipped;
10 to 14 new, unreviewed by the owner).

## What the spike changed in the plan (for revision 2.4)

Requirements added, all from measured defects, all mapped to phase 2b:

- R48 surface the launch `ApplyResult` outcome and reason, never only a command count.
- R49 a launch decision never registers the lead as live.
- R50 suggested actions never name a deprecated flag.
- R51 a launch from the amq-squad binary hands the AMQ library a real `amq` path, or serves
  `coop exec` itself (the library uses `os.Executable()`; every launchapi start from 2.31
  fails at tmux Create; the legacy driver is what launched S6). Upstream ask 14.
- R52 the launch sets the complete AMQ identity in every seat (`AM_ME`, `AM_ROOT`,
  `AM_BASE_ROOT`, `AM_SESSION`) and proves one drain before reporting the seat up (five of
  six S6 seats repaired their own shell; one stayed idle all run).

Decisions recorded in the decision log: criterion numbering; merge gate becomes a lead
rules decision with core enforcement (2026-10-22); Buzz setup replaces the nsec question
as the S1 blocker.

## Harness findings worth keeping

- The Claude Code auto-mode classifier denies `amq-squad start/resume --exec` for the
  operator session ("Create Unsafe Agents"); the owner runs launch lines with `!`.
- An agent must never `git checkout -b` in the main checkout; use a worktree.
- `preflight.sh` keeps only hashes of guarded files; keep copies so a post-run change can
  be shown (S6 finding B: `~/.claude/settings.json` changed at 06:56, unattributed).
- `amq-squad down` cannot inspect seats launched by the legacy driver (no launch record);
  it still closes the panes.

## Open for the owner

1. 12-seat cap decision (criterion 5 numbers above).
2. The 18 open draft PRs on amq-noc and workspace-cli: merge through the rules path as a
   day-3 merge-train test, or close them.
3. Upstream asks: read `docs/v3-upstream-asks.md`; say which to file. The message to the
   maintainer (asks 1, 4, 5, 7, 8) is drafted.
4. Buzz: npub, then the one-time nsec step, then the S1 owner run.

## Proposed next step

Plan revision 2.4 (integrate R51, R52, the merge-gate decision, the S4 and S6 outcomes),
then phase 2a-1 starts from the keep/delete manifest while criterion 4 waits for AMQ
v0.104.0.
