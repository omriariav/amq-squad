# Brief 31-1, PR 1: the deletions, squad.yaml as the one source, one lead per root

Branch `v3-31-1` from `origin/v3` at `d5094b68`. Authority: docs/v3.1-plan.md
section 7 (the amendment of 2026-10-31, the owner's A), read from the
amendment's scratch copy until it merges. This note is PR 1 of the brief:
section 1 (the deletions), section 2 (squad.yaml), section 4 (one lead per
root) and the start stamp. PR 2 (`up`, the status additions, the launch
budget, the screen) is a later agent's; its seams are named at the end.

## 1. Before any code

### 1.1 The baseline

Source: the lead's read-only copy of the 2026-10-31 session (decisions.jsonl,
the seats' staged launch records and accepted proofs, their outboxes), never
the live session. Script: `baseline.py` beside this note.

```sh
python3 -I docs/v3-spike/31-evidence/baseline.py <copy dir>
```

Only the last roster launch, `0de97c89b15b5262f41e4fd3ee0b1a87` (3.0.2,
2026-10-31), still has full launch records: every earlier launch's staged
record was overwritten by the next. Its output:

| Seat | Runtime and model | Prepare | Mint | Apply | Seat-exec and proof | To seat live | To first message | After |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| dev | codex gpt-5.6-terra | 3.201 s | 0.602 s | 0.538 s | 0.144 s | 4.485 s | none in the copy | |
| reviewer | codex gpt-5.6-sol | 3.201 s | 0.602 s | 0.538 s | 0.078 s | 4.419 s | 57.449 s | |
| scribe | claude sonnet | 3.201 s | 0.602 s | 0.538 s | 0.325 s | 4.666 s | none | `claude_hooks_silent`, closed |

- **The segment it covers.** From the scheduler's own start
  (`scheduler_started`, 19:24:32.963Z, the first decision of the incarnation
  that launched the roster) to the seat's accepted proof and to its first
  sent message. Nothing stamps the owner's start: the scheduler's
  `launch.json` (internal/schedproc/requests.go:21-31) has only the process's
  own `started_at`, and the lead keeps no pid file. The segment before the
  scheduler (the owner's command, the lead's start, the scheduler's spawn and
  admission) is not in the baseline. This PR adds the start stamp (1.3).
- **Prepare** is `scheduler_started` to the staged record's `started_at`
  (Compile's clock; admission, adoption and the first tick precede it).
  **Mint** is `started_at` to the proof ticket's `issued_at`: the preflight
  copy, the Claude config dirs and the Claude mode probe (one probe for the
  one Claude seat) and the proof socket. **Apply** is `issued_at` to AMQ's
  apply outcome. **Seat-exec and proof** is the apply outcome to the accepted
  proof (`proof.json`'s `at`): seat live.
- `roster_launched` is logged at 19:24:37.720Z, after every proof (+4.757 s).
- dev's first message after this launch is not in the copy (its last sent
  message is from 2026-10-30). The scribe sent none: it was quarantined
  `claude_hooks_silent` and closed in the same tick, the brief's motivating
  case. The 2026-10-30 20:46:50 launch (`1ab6e1aa…`) shows the same shape
  (reviewer's first message 66.6 s after `roster_launched`; the scribe again
  quarantined `claude_hooks_silent`) but has no launch records left.

### 1.2 The open questions (plan 7.9)

Each is answered by a reading recorded here, before the first code commit.

**Q1, the Claude login without a token file: not answered by a reading; an
owner-run row.** Today seat-exec reads `~/.amq-squad/claude-token` only
(internal/claudeseat/token.go:56-90: a regular 0600 file in a 0700 dir, one
token on one line), refuses `claude_token_insecure` when it is missing or
insecure (internal/seatexec/seatexec_unix.go, the Claude branch of
`prepare`), and sets `CLAUDE_CODE_OAUTH_TOKEN` from it for Claude seats only
(`overlay`). The only measurement on record is token.go's comment: a fresh
`CLAUDE_CONFIG_DIR` finds no login because the macOS Keychain item is keyed
by the config dir ("Not logged in" on Claude Code 2.1.310). Until the
owner-run check below runs on the installed Claude Code, the token refusal
stays as it is (this PR does not touch it).

Owner-run row (from the owner's own terminal, no seat):

```sh
d=$(mktemp -d)
env -u CLAUDE_CODE_OAUTH_TOKEN -u ANTHROPIC_API_KEY CLAUDE_CONFIG_DIR="$d" \
  claude -p 'Reply with the single word OK' --model sonnet; echo "exit $?"
rm -rf "$d"
claude --version
```

"OK" and exit 0 means a seat logs in without the token file, and
`claude_token_insecure` should then refuse only a token file that is present
and insecure. "Not logged in" (or a login prompt) keeps the token a
requirement.

**Q2, the trust subject on a binary upgrade: replacing a binary at the same
path does not change it, and the standing answer covers a changed one.** In
the pinned AMQ the trust confirmation is asked when the prepare's trust
digest is not in the trust store (internal/launch/prepare.go:714-735 at
v0.116.2). The digest is `PrepareTrustDigestWithAuthority` over
`PrepareTrustPlanDigest` (internal/launch/plan.go:270-310): each agent's argv
values, env overlay, cwd, adapter mode, resume policy, execution options,
initial-input kind (not its text) and wrapper, all strings. No file's bytes,
inode or version enter it, so a binary replaced in place keeps the subject. A
binary at a new path (a versioned install, or amq-squad's own path, which
the wrapper records after `EvalSymlinks`) changes it, and every launch now
carries the standing `trust_exact_subject` answer (item 6), so that launch is
answered too. (The apply's subject digest also names the amq executable's
identity, prepare.go:767-774, but that binds a prepare to its apply, not the
trust store.)

**Q3, Codex at an approval prompt without AMQ's dialog hook: it cancels the
pending command.** AMQ's own docs: "A doorbell at a Codex approval prompt
cancels the pending command and runs as a new prompt" (docs/wake-operations.md
:460-461 at v0.116.2); the dialog hook (`amq wake install-dialog-hook
--harness codex`, `PermissionRequest` opening the marker) is what holds the
doorbell, and the baseline's reviewer `.wake.log` shows it doing so ("holding
the doorbell while a harness dialog is open … PermissionRequest … codex").
So, as the brief rules, the Codex wake keeps the hook as its one condition:
a Codex seat without it launches with its wake off and the named reason (the
screen shows it idle, brief 31-1 PR 2). The Codex worktree trust is not a
wake condition any more; it stays doctor's `codex trust` warning row.

**Q4, the generic provider mapping: AMQ adapts five providers, and only
those.** internal/launch/adapter.go:21-36 at v0.116.2 names the built-in
providers claude, codex, cursor-agent, grok and pi (`ProviderForExecutable`
maps an executable to one of them or ""). Beyond claude and codex, the
argument rules are grok's `--model` and `--effort low|medium|high|xhigh|max`
(adapter_grok.go:133-134) and cursor-agent's `--model` only
(adapter_cursor.go:135). So the runtime lists open to exactly those five;
grok passes model and effort, cursor-agent model only (an effort on a
cursor-agent seat is refused by name), and any other runtime is refused by
name at parse. `CheckArgumentForms` still checks every form against what AMQ
advertises at prepare.

**Q6, the pi seat wake: the embedded runtime does not carry AMQ's pi
extension, so a pi seat stays wake-off.** AMQ's pi dialog guard runs "only
when the AMQ pi extension is loaded" (docs/wake-operations.md:587-599 at
v0.116.2: `integrations/pi/amq-bridge.ts` writes the `.wake.dialog`
marker). scripts/build-pi-runtime.sh:92 stages amq-squad's own
`pi/extensions` only, and nothing in it writes that marker. A pi seat keeps
`PiWakeAuditReason`; the screen says so (PR 2).

### 1.3 The design

- **Deleted** (plan 7.2 (b), the owner's A), each with the tests that pin it:
  the silent hook as a quarantine reason; the config-drift preflight copy and
  diff, `exitChanges`, `global_config_changed` and `claude_config_changed`;
  with them the whole quarantine (the task store's record, the scheduler's
  quarantine closing, the ledger's quarantine and signed clears, the hook's
  run-time drift deny) and `seat clear` (its only callers were those
  quarantines), the `quarantine_clear` gate kind and the `clear.signature`
  grant key; the Claude pin and the mode probe; the Claude config-dir
  refusals; the wake conditions as refusals; `--trust`; the seats file
  (`launch --seats`, `--scheduler-seats`, `--lead`); the empty-worktree
  exclusion; `codexModelLists` as a gate; the pi model refusal; the launch's
  push-target refusal.
- **Moved.** A silent hook, worktree allow rules and an external CLAUDE.md
  include become seat warnings in status (`seat.warnings`). The push-target
  check runs only in the commit step. The trust confirmation's answer is a
  constant every launch carries. A Codex seat's wake reads AMQ's dialog hook
  only (Q3). The router admits a roster seat's own pool without a registry
  entry or a table chain. A ledger_tampered refusal at a seat's exit (the one
  non-config producer the quarantine had) closes the seat and tells the lead;
  the commit step refuses the same way, so nothing of it commits.
- **squad.yaml is the one source.** `lead --team` in a repository with a
  squad.yaml reads its lead handle and session, takes the root's lead lock,
  and runs `scheduler run --project <repo>`, which reads the same file. A
  seat's worktree is derived per handle (`<repo>/.worktrees/<handle>`), and a
  squad.yaml worktree naming the repository root is a parse error.
- **One lead per root.** A lead lock (`lead.lock`, flock) and its record
  (`lead.json`: pid, raw start stamp, tmux pane) beside the scheduler's
  `run.lock`. A second `lead --team` refuses `lead_running` with the first's
  pid and pane before its scheduler starts. A scheduler's `ErrRunning` is a
  refusal the supervisor does not restart. A lead exiting non-zero stops its
  scheduler before `os.Exit`.
- **The start stamp.** `lead --team` sets `AMQ_SQUAD_START` (when `up` has
  not) and the scheduler records it in its `launch.json` as `start`.
- **Decisions log kinds added:** `exit_refused` (a refusal at a seat's exit,
  replacing `seat_quarantined` there). `seat_live` and `seat_first_message`
  are PR 2's.
- **The screen's pi surface (PR 2's decision, read now).** pi 1.3.0's
  extension UI has persistent surfaces: `setWidget(key, string[] | component,
  {placement})` (dist/core/extensions/types.d.ts:101-104), `setFooter`
  (:111), `setHeader`, `custom` with an overlay (:121), and raw keys through
  `onTerminalInput` (:82). So the screen can live in the pi extension, inside
  the lead's process (R4 by construction for brief 31-2's keys), as a widget
  fed by `status --json` on the `.generation`/`updated_at` poll; no separate
  `amq-squad screen` process is needed.
