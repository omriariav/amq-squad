# Brief 3.1-31-1: the launch path and the screen

Repository: /Users/omri/code/amq-squad (Go module github.com/omriariav/amq-squad/v3; AMQ pinned at github.com/avivsinai/agent-message-queue v0.116.2, source in ~/go/pkg/mod/github.com/avivsinai/agent-message-queue@v0.116.2). Work ONLY in a fresh scratch worktree under $HOME: `git -C /Users/omri/code/amq-squad fetch -q origin && git -C /Users/omri/code/amq-squad worktree add /Users/omri/scratch-31-1 -b v3-31-1 origin/v3`. Never touch the main checkout, .worktrees/, ~/.amq-squad, ~/.claude, ~/.codex, or the live session under /Users/omri/code/amq-squad/.agent-mail. Never push, amend or rebase. Never run the amq-squad binary from your branch against owner state, the real tmux server or the real AMQ. `go build`, `go vet`, `go test` and `npm test` in pi/ are fine. No network, no live AMQ, no tmux in tests: the existing seatlaunch and schedproc tests fake AMQ through seatlaunch.Options.RunAMQ and the launcher through the scheduler's SeatLauncher interface; the pi tests use pi/test/fakepi.ts. Follow them.

Authority: docs/v3.1-plan.md section 7 (the amendment of 2026-10-31, the owner's A). Every file:line below is at d5094b68 and comes from the launch-path map of the day; re-read each site before you change it, since the lines move as you delete.

## The goal

A squad.yaml line to every seat's first AMQ message within the launch budget, with no owner step between, and one read-only screen in the lead's process that shows the squad. The motivating case is the scribe: quarantined since day 1 by a hook probe, quarantined again at launch on 3.0.2 (`claude_hooks_silent`, 2026-10-31 19:24:37Z in decisions.jsonl). After this brief a silent hook is a line on the screen and the seat starts.

## Before any code (the execution note's first section)

1. **The baseline.** The lead hands you a read-only copy of the 2026-10-31 session's `scheduler/decisions.jsonl`, its staged and published launch records and the seats' first mailbox messages; never read them in place. Compute, per seat, the time from the first roster decision to the seat's first AMQ message. Stamps: decisions.jsonl (internal/schedproc/requests.go:17-52), the launch records (internal/launch/record.go:123-139, 185-244), the proof (internal/launch/proof.go:137-155). Nothing stamps the start today (`launch.json` has no start stamp, requests.go:21-53), so say which segment the baseline covers.
2. **The open questions** (plan 7.9), each a named row answered by a reading or a scripted check you record, before the first code commit:
   - Q1, the Claude login without a token file: does a seat under its own `CLAUDE_CONFIG_DIR` log in without `~/.amq-squad/claude-token` (internal/seatexec/seatexec_unix.go:212-216; internal/claudeseat/token.go:56-90)? A live check is an owner-run row with the exact commands; until it runs, the token refusal stays as it is.
   - Q2, the trust subject on a binary upgrade: does replacing the binary at the same path change AMQ's trust subject? Answer from the pinned AMQ source.
   - Q3, Codex at an approval prompt without the hook: what a Codex doorbell does on an approval prompt without AMQ's dialog hook (internal/seatexec/codexhook.go:22-29). If the answer is "it cancels the pending command", the Codex wake keeps the hook as its one condition and the screen shows a Codex seat without it as idle.
   - Q4, the generic provider mapping: how model and effort map to flags for a runtime beyond claude, codex and pi (internal/seatlaunch/compile.go:380-405; seatexec_unix.go:144-160). If AMQ's adapters give no mapping, a fourth runtime is refused by name and the closed lists open only as far as AMQ advertises.
   - Q6, the pi seat wake: does the embedded runtime carry AMQ's pi extension (compile.go:134, 209)? If not, pi stays wake-off and the screen says so.
3. **The design**, half a page: what is deleted, what moves, the decisions log kinds added, the screen's pi surface (below).

## The change

### 1. The deletions (plan 7.2 (b)), each with its tests

Delete the code and the tests that pin it; no flag, no "off by default", no production code kept alive by a test.

1. The hook probe as a quarantine reason: `claude_hooks_silent` and `HooksSilent` (internal/claudeseat/reports.go:20-22) as an input to the exit check. A silent hook report becomes a seat warning in status and a line on the screen.
2. The config-drift preflight baseline: the preflight copy and diff (internal/seatexec/options.go:92-189; internal/cli/scheduler_run.go:262-267; internal/seatlaunch/run.go:310; seatexec_unix.go:241), `exitChanges` (internal/seatcommit/commit.go:206-247), `global_config_changed` and `claude_config_changed`, the quarantine, and the quarantine path of `seat clear` (internal/cli/seat_clear_gate.go header) with the `quarantine_clear` gate kind and the grant key `clear.signature`. If `seat clear` has no caller left, delete the verb and its registry entry. `ConfigDrift` goes with the quarantine. seat-exec's deny of a write to the owner's global agent config stays.
3. The probe-and-pin step: the Claude pin (`PinFor`, `registryClaudePin`, the `ClaudePins` compile branch: run.go:89-91, 193-234, 416-438; compile.go:80-84, 95-97, 523-538), `pinnedProvider` with `tool_unpinned`, `pin_changed_since_probe` and `claude_pin_mismatch` for Claude (seatexec_unix.go:144-152, 269-280, 284-335), the `ClaudePin` record field, the doctor claude-pin row and README.md:153-170. Claude resolves through `providerPath` (seatexec_unix.go:576). The mode probe: run.go:85-88, 178-180, 313-345, internal/claudeseat/probe.go and `claudeseat.ModeUnverified`. Keep `--permission-mode dontAsk` (compile.go:390). Keep the gh pin.
4. The Claude config-dir refusals: `claudeseat.Check` and `screensGap` as refusals (internal/claudeseat/claudeseat.go:603-711) and their calls (seatexec_unix.go:186-211, 269-273), with `claude_dialog_guard_missing` (:203-207), `claude_allow_rules_present` (:209-211), `PermitWorktreeAllowRules` (run.go:77-80, 346-348), `claude_settings_env_forbidden` (claudeseat.go:642-644) and the external includes refusal (claudeseat.go:687-690). Keep `claudeseat.Write`'s seeding (the plugin and the first-run screens).
5. The wake conditions: `SeatWakeConditions`, `readWakeConditions`, `scratchClaudeWakeConditions`, `codexTrusts` (compile.go:136-302), the wake seams (run.go:104-117, 184-192, 245-247), `admittedWakes` as a refusal (compile.go:304-370; run.go:281-284). `SeatWake` is on for Claude and Codex and off for pi, subject to Q3 and Q6. AMQ's admitted wake is recorded on the seat as a fact. Keep the hook plugin's deny of AskUserQuestion and ExitPlanMode. The Codex trust lines stay a doctor warning row.
6. `--trust`: scheduler_run.go:123, 202-204, 316-321; internal/cli/pi_lead.go:75, 109-111; internal/cli/lead_scheduler.go:184-195, and the exit 17 handling (lead_scheduler.go:68-74, 121-123). The standing `trust_exact_subject` answer is always on (subject to Q2).
7. The missing session as a refusal: scheduler_run.go:166-169 and compile.go:659-661 give way to `up` creating the root and session.
8. Models and runtimes: the router admits each roster seat's own pool without a registry entry or a table chain (internal/routing/router.go:593-640, 261-271, 540; internal/routing/open.go); `codexModelLists` stops gating (internal/registry/codexmodel.go:21-24, used at internal/registry/build.go:99-130); the pi model refusal goes (internal/team/squad.go:367-369; `PiLauncherArgv` already passes `--model`, internal/seatexec/pi.go:102-114); the runtime lists open as far as Q4 allows (squad.go:31; compile.go:101; `byRuntime`, scheduler_run.go:213, defaults to the AMQ launcher). The registry stays as optional cap and cost metadata.
9. The push-target check moves from launch (`LaunchTarget` and `CheckTarget`: scheduler_run.go:187-190; run.go:374-386) into the commit step, which already reads the target.

Kept, untouched (plan 7.2 (a) and the machinery): the base-tip and seat-branch refusals (internal/schedproc/slots.go:97-262, 407), the binding check and one branch per task, CheckCommit and the body digest, the R4 lead proof (scheduler_run.go:395-412; internal/schedproc/decide.go:144-184, 264-340; lead_scheduler.go:197-205), the proof socket and identity proof (run.go:353-360; seatexec_unix.go:252-262, 343-366, 594-626), `seat_replacement_resource_present` (internal/seatlaunch/roster.go:88-108), the role and rules digests and the Codex sandbox (seatexec_unix.go:85-141, 169-179), the grants (squad.go:210-224, 346-379), `CheckArgumentForms` (compile.go:613-643), the `AM_*` agreement checks (lead_scheduler.go:139-173).

### 2. squad.yaml is the one source

- Fields: per seat `handle`, `role`, `runtime` (the binary; the schema-1 key stays), `model`, `effort`; the squad's `lead` handle and `session`. The worktree is derived per handle in `team.SeatWorktree` (squad.go:382-390), never the repository root; the empty-worktree exclusion goes (internal/schedproc/roster.go:33-36, 80-86). Today the checked-in squad.yaml refuses with "has no seat that can start" (scheduler_run.go:198-200); after this brief it launches.
- Delete `launchSeatFile`, `readLaunchSeats` and `launch --seats` (internal/cli/launch_seats.go:18-136). `scheduler run` takes `--project` instead of `--seats` (scheduler_run.go:120, 147-150). `lead --team` drops `--scheduler-seats` (pi_lead.go:74, 104-125) and `--lead`; `resolveTeamLeadIdentity` reads the squad (lead_scheduler.go:134-176).
- Build `seatlaunch.Squad` from `team.LoadSquad` (squad.go:120-135), `Squad.LaunchSeats` (internal/team/launch.go:18-29) and `roleText` (internal/cli/fleet_adapters.go:226-236).
- Update README.md:235-260 and 403-405 and docs/squad-yaml.md.

### 3. `amq-squad up`

New internal/cli/up.go and a registry entry (internal/cli/command_registry.go:15-88). In order, each step's refusal by name:

1. Install the tagged pi binary onto PATH by temp name and rename, never a copy over the existing file (the macOS signature cache kills a copied-over binary; follow-up "Install and self-update"). The binary is the output of `scripts/build-pi-runtime.sh` at a tag, never a build of the working tree (R54). Refuse `ErrNotEmbedded` (internal/piruntime/manifest.go:43) and a runtime `*Reject` by name (`pi_runtime_rejected`, `pi_not_embedded`).
2. Run the doctor checks (internal/cli/doctor.go:297-375) and refuse on a failing row by its name (`amq_below_floor`, `owner_grants_insecure`, `routing_invalid`, `seat_owner_state_refused`). A deleted guard leaves no doctor row behind.
3. Create the AMQ root and session (`amq coop init` and `amq session create` underneath).
4. Compile squad.yaml through `team.LoadSquad` and `Squad.LaunchSeats`.
5. Stop a stale lead and scheduler by name: the lead's tmux session by its name, the scheduler by the `run.lock` probe (internal/schedproc/process.go:96-105), `ReadLaunchRecord`, procinfo and `LeadState`; reuse uninstall's `schedulerLive` (internal/uninstall/busy.go:62-110). `launch.json` gains a start stamp first (requests.go:21-31).
6. Start the lead in its own tmux session, `tmux new-session -d` under a name outside AMQ's `amq-*` pattern (AMQ v0.116.2 internal/launch/tmux_names.go:11-23 names its sessions `amq-...`; use `squad-lead-<project<=30>-<sha5>`), running `amq-squad lead --team` so the lead proof's parent match holds (lead_scheduler.go:200-205). The lead stays out of AMQ's roster session (internal/schedproc/roster.go:21-31).
7. Tail decisions.jsonl for `roster_launched`, `roster_adopted`, `roster_incomplete` or `roster_launch_failed`, then print the roster from `status --json`, and print how to attach to the lead's tmux session.

`up` surfaces by name what the scheduler and the roster launch refuse: `ErrRunning`, the `AM_*` disagreements, `lead_gone`, `scheduler_starting`, `seat_replacement_resource_present`, `binding_present_without_resumable_conversation`, `seat_replacement_needs_relaunch`, AMQ's "tmux session ... already exists".

### 4. One lead per root

- A second `lead --team` on the same root refuses `lead_running`, naming the first lead's pid and tmux pane, before its scheduler starts. A lead lock beside `run.lock` holds the pid, the raw start stamp and the pane.
- The supervisor stops retrying `ErrRunning` (lead_scheduler.go:62-84; it is mapped to `ExitSystem` at internal/cli/exit.go:111 and retried five times today).
- A lead that exits non-zero stops its scheduler: `piExit` calls `os.Exit` (internal/cli/pi.go:277-278) and skips the deferred `sup.stop()` (pi_lead.go:122), so today the scheduler lives until its next lead_gone check (process.go:203-207).
- Day-3 evidence (plan 7.10): the old 3.0.1 lead in the old tmux session held the scheduler, the new pi showed nothing, and a second lead ran in the owner's terminal beside the tmux one; the lead handle defaulted to "lead" with no `--lead` (cto the day before).

### 5. The launch budget

The number: the time from the start (a squad.yaml line followed by `amq-squad up`; the screen's add key is brief 31-2's) to each seat's first AMQ message, with no owner step between. The ceiling is **10 seconds**; the owner may lower it. `up` writes the start stamp; the scheduler records each seat's first AMQ message time as a decision (`seat_first_message`); status and the screen show the budget per seat; a seat over the ceiling is a warning line naming the slowest segment (prepare, apply, seat-exec, proof, first turn) from the records. The day-4 record carries one row per seat with its runtime and model, beside the baseline you computed.

### 6. Status for the screen

Add to `status --json` (internal/status/collect.go, internal/status/schema.go; docs/status-contract.md grows): the roster with each seat's runtime, model, effort, worktree, branch, launch outcome, timing and admitted wake (today returned only by `Launch`, run.go:132); a decisions tail; lead and scheduler liveness (pid, start stamp, tmux pane); train runs from the audit log (internal/mergetrain/audit.go:82-160) and the alerts (internal/mergetrain/drift.go:61); each task's evidence and PR fields (the status task row has none, schema.go:183-203); the launch budget; the seat warnings that replace the deleted refusals (a silent hook, a config change seen, an external include). Fix the `amq_status` description (pi/extensions/lib/tools.ts:256). The status read stays lock-free (`task.Snapshot`, collect.go:59; scheduler.json, :62).

### 7. The read-only screen, in the lead's process

- **Where:** in the pi extension (pi/extensions/amq-squad.ts and lib/), so it runs inside the lead's process. Today the extension's only UI is `setStatus` (amq-squad.ts:227-233), `notify` (:237; lib/amq-bridge.ts:688), the hold dialogs (:167-185) and the first-turn role message (:70-71); it adds no widget, footer, overlay or keybinding. Read the installed pi's extension UI surface (@earendil-works/pi-coding-agent 1.3.0 in pi/package.json) and name in the design which component carries the screen.
- **What it shows:** the first line, the lead handle and the root. Then four blocks, each with its refusal line (the newest refusal name and detail that stopped an item in that block, or none):
  - seats: handle, runtime and model, state, launch outcome and its budget, warnings;
  - tasks: id, state, seat, PR, the block reason when blocked;
  - train: the current and last runs, alerts;
  - gates: open gates with kind, target and age.
- **From which store:** `status --json` (internal/cli/status.go:78), which reads the tasks (`R/extensions/amq-squad/tasks/`), scheduler.json, the scheduler directory, the launch records, the gate store (internal/gatestore/store.go:105-330) and the train's audit log.
- **When:** a poll keyed on `tasks/.generation` (internal/task/task.go:155-156, 241-284) and scheduler.json's `updated_at` (internal/scheduler/scheduler.go:968-991, 1042-1057, rewritten every 2 s tick): stat both on a short timer, run `status --json` only when either changed. No other poll is added (the hold's guard re-run, amq-squad.ts:144-163, is today's only one).
- **Read-only:** no key changes state in this brief; brief 31-2 adds the keys on this screen.

## Rows (each fails when its change is reverted)

1. A Claude seat whose hook report is silent starts, takes a task and ends it through the commit step; status carries the warning; no quarantine is recorded; no `seat clear` is asked (plan acceptance 7).
2. A Claude seat launches with claude resolved on PATH and no registry pin; a changed claude binary does not refuse.
3. A launch with a Claude seat runs no mode probe (the fake probe is never called).
4. A Claude seat dir carrying allow rules, or a CLAUDE.md with an external include, starts, with a status warning.
5. Claude and Codex seats compile with the wake on and no wake-condition read; pi compiles wake-off; a differing admitted wake is recorded, not refused.
6. `--trust` is an unknown flag on `lead` and `scheduler run`, and every launch carries the standing answer.
7. `up` on a project with no AMQ root creates the root and session and launches.
8. The checked-in squad.yaml (no worktrees) launches every seat with derived worktrees, none at the repository root; `--seats`, `--scheduler-seats` and `--lead` are unknown flags; the lead's handle is squad.yaml's.
9. A Codex id outside `codexModelLists` and a pi `PROVIDER/ID` model each get a task from their roster pool.
10. A launch with no gh pin succeeds; the commit step's push without it refuses as before.
11. A second `lead --team` on the same root refuses `lead_running` with the first's pid and pane; a scheduler's `ErrRunning` is attempted once, not five times.
12. A lead exiting non-zero stops its scheduler.
13. `up`'s seven steps run in order against fake tmux and fake AMQ; a stale lead session and scheduler are stopped by name; the lead's session name never matches `amq-*`; the install renames a temp file and never writes over the target in place.
14. The launch budget from fixture records: the start stamp to the first message per seat; a seat over the ceiling yields the warning naming the slowest segment.
15. The status additions appear in the golden fixture; the screen's poll runs `status --json` only after `.generation` or `updated_at` changes.
16. The screen renders from a status fixture (pi/test with fakepi): the first line carries the handle and the root; each block shows its refusal line.
17. The kept checks' existing rows stay green, unedited: the base-tip and seat-branch refusals, the binding check, one branch per task, CheckCommit, the R4 proof.

Owner-run rows, with the exact commands: Q1's login check; the day-4 launch budget run (`up` on this repository from squad.yaml, the per-seat numbers against the baseline).

## Declared files

internal/seatlaunch/, internal/seatexec/, internal/claudeseat/, internal/seatcommit/ (the exit check and the push-target move only), internal/schedproc/, internal/scheduler/ (the first-message decision only), internal/routing/, internal/registry/, internal/team/, internal/status/, internal/uninstall/ (reuse only), internal/cli/ (up.go, command_registry.go, pi_lead.go, pi.go, lead_scheduler.go, launch_seats.go, scheduler_run.go, seat_clear*.go, seat_owner*.go, doctor*.go, status.go, exit.go, and their tests), internal/gatestore/ and internal/grants/ (the clear kind and the `clear.signature` key only), pi/extensions/, pi/test/, README.md, README.html (regenerated), docs/squad-yaml.md, docs/status-contract.md, docs/v3-spike/31-evidence/. Anything else: stop and report; the lead widens.

## Rules

Briefs ruling 18: no test reaches the network; no test touches the real HOME, the real tmux server or the real AMQ; canaries must not match redact.RealCredentialShapes; never print a credential value. `go build ./... && go vet ./... && go test -count=1 ./...` and `cd pi && npm test` green, and `make ci` green before the report. Every change has a row that fails when reverted; every deleted guard's tests go with it, and the execution note lists them. Execution note at docs/v3-spike/31-evidence/31-1-launch-and-screen.md: the baseline, the open questions' answers, the design, the deletions with their removed tests, the rows, the owner-run rows.

Commits on v3-31-1 (several are fine; each message ends with these two lines exactly):
```
Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01DweoyBP65psWdJtwN3gdnu
```

## Report

Branch, head SHA, `git log --oneline origin/v3..HEAD`, `git diff --stat origin/v3...HEAD`, the test output, the baseline, the open questions' answers, the design summary, anything this brief got wrong.
