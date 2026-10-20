Criterion 4 NOT PASSED. Claude seat demonstrated; Codex seat blocked by owner decision.

Closure run: the owner pre-trusts the scratch root
`/Users/omri/.amq-squad/spike/s4/project` for codex-cli. Then a fresh
Prepare and Apply runs with the same two-seat shape and Codex 0.147.0
(`request.py` unchanged). It must capture both role acknowledgements
(`S4_ROLE_VISIBLE` from each seat) and a completed Codex identity capture
(conversation `ready` through `__codex-notify`). The Claude fault
measurements below stand and do not need to be rerun.

# S4: wrapper launch of one Claude seat and one Codex seat

Run 2026-10-21 00:17 to 00:27 IDT on the owner's Mac. AMQ 0.102.0 (contract
0.61.1), claude 2.1.305, codex-cli 0.147.0 (spike prefix) and 0.154.0
(Homebrew), tmux 3.7c. Branch `spike/s4-wrapper`, cut from `v3`.

Roster note: the implementer was swapped from Codex to Opus because of the
sandbox on the Codex subagent in this Claude Code harness. The reviewer
family is Codex.

## Grade

- **Claude seat: demonstrated.** The launch API accepted the wrapper shape. The
  trust digest binds `--model sonnet --effort medium` and the wrapper argv.
  The pane started under AMQ. The wrapper wrote the launch record and
  `role.md`, set only the three `AMQ_SQUAD_*` variables, and exec'd Claude in
  place. SessionStart saw the variables and injected the role. The model read
  the role and answered `S4_ROLE_VISIBLE — seat handle: s4-claude`. The
  PreToolUse fail-open behavior is measured below.
- **Codex seat: BLOCKED BY OWNER DECISION.** This is not a FAIL. The launch
  API accepted the wrapper shape on 0.147.0. The trust digest binds the
  model, the effort, the sandbox, the approval flags, and the wrapper. Apply
  committed a pane, and the wrapper ran: it wrote the record and role and
  set the environment. codex-cli then stopped on its own folder-trust prompt
  before the model ran. Answering "Yes" writes to `~/.codex/config.toml`, and
  the owner's rule keeps that file untouched, so I answered "No" and the seat
  quit. The seat therefore never read the role, and the notify forward and
  the worktree commit test could not run.

**S6 dependency:** the 0.147.0 prefix **does** pass the AMQ capture gate:
Apply accepted it, committed, and created the pane. It **did not** produce a
working fresh Codex seat, because of codex's trust prompt. That prompt
appears for any project root not already trusted in `CODEX_HOME/config.toml`.
On 0.154.0, Apply refuses with `capture_version_unsupported` before creating
anything.

## Finding: a fresh Codex seat stops on codex's folder-trust prompt

- A fresh Codex seat under the AMQ launch API shows codex's own prompt: "Do
  you trust the contents of this directory? 1. Yes, continue 2. No, quit".
  Codex applies trust to the git repository root, and for a linked worktree
  that is the main repository's root (see
  `spike/seatexec/evidence/pane-codex-0147-trust-prompt.txt`).
- "Yes" persists `[projects."<root>"] trust_level = "trusted"` in
  `CODEX_HOME/config.toml`. "No" quits the seat. I answered "No". The SHA-256
  of `~/.codex/config.toml` was the same before and after that keypress
  (`6fa20145…fe619`).
- The launch grammar has no override for this. Codex `-c` admits only
  `model_reasoning_effort` and `approvals_reviewer` (`adapter_codex.go`,
  `codexConfigOverrideValues`). Passing `--sandbox` and `--ask-for-approval`
  does not suppress the prompt on 0.147.0.
- The wrapper may set only environment and role (R4), so it cannot add
  `-c projects...` or point `CODEX_HOME` elsewhere.
- So amq-squad 3.0 must do one of two things. Either it asks the owner to
  pre-trust each project root in `CODEX_HOME/config.toml` (a doctor check
  plus an owner action), or it treats the prompt as a seat-start step the
  owner answers in the pane.
- S6 will hit this prompt for amq-noc and workspace-cli. Neither is in the
  trusted list.
- The trusted project paths (read-only from `~/.codex/config.toml`) are:
  `/Users/omri/code/{mac-migration, agent-message-queue, amq-squad,
  tmux-resurrect-launchd, sari_plygrnd, screenshots-housekeeping,
  personal-memory, yoetz}` and `/Users/omri/Documents/Codex/2026-10-06/t`.
  None of them is scratch-safe. A linked git worktree of a trusted repository
  placed under `~/.amq-squad/spike/` would inherit trust, because trust is
  applied at the repository root. But creating one writes into that
  repository's `.git`, so I did not do it and did not launch there.
- **Observation (writer unknown):** after the seat started,
  `~/.codex/config.toml` changed. Its mtime moved to 00:19:17, and
  `[marketplaces.omri-marketplace] last_updated` reads
  `2026-10-20T21:19:14Z`. Another Codex session (tmux `memory-wpp`) was live
  on the host at the same time. This run does not identify the writer, and
  the file was left as it was.
- **Reproduction test** (not yet run):
  1. Launch the exact binary (`~/.amq-squad/spike/codex-0.147/…/codex`)
     against a disposable, non-symlinked copy of the relevant `CODEX_HOME`
     state. Only that process tree may use it.
  2. Hash, diff and trace writes to that copy (for example with `fs_usage`
     filtered to the process tree) before launch and again at the
     unanswered trust prompt.
  3. Leave the owner's `~/.codex/config.toml` untouched throughout.

  If the copy changes, codex-cli writes its own config on start. If it does
  not, the writer was the other session.

## Prepare preview (0.147.0 on PATH)

`amq launch --request request.json --prepare --json` exited 6 with
`action_required` / `untrusted_config_digest`. The only required action was
`trust_confirmation` with the choices `trust_exact_subject` and `deny`.

```text
subject_schema 2
subject_digest sha256:b4fd7570f408f81b9408c4a17e7c2a80f01e65f31bef941d0b4300bd4f8ed0ee
plan_digest    sha256:4bddb1d695822c516c5d1ff06f85842300de5b0962bb3ab3d4fdf2dc1b277cd1
trust_digest   sha256:d7a93ce9bc59663340e8653ea9fa22471780a9ae73afd9c28423d653e6f55c33
backend tmux, profile tmux/darwin/v1, placement session/columns

s4-claude argv:
  /Users/omri/.amq-squad/spike/bin/seat-exec --seat s4-claude
  /Users/omri/.local/bin/claude --model sonnet --effort medium --name s4-claude
  --session-id ${launch_nonce} "<role bootstrap>"
  cwd /Users/omri/.amq-squad/spike/s4/project
s4-codex argv:
  /Users/omri/.amq-squad/spike/bin/seat-exec --seat s4-codex
  /Users/omri/.amq-squad/spike/codex-0.147/node_modules/.bin/codex --model gpt-6-astra
  -c model_reasoning_effort=medium --sandbox workspace-write --ask-for-approval on-request
  -c notify=["/opt/homebrew/Cellar/amq/0.102.0/bin/amq","__codex-notify","--root",
     "/Users/omri/.amq-squad/spike/s4/project/.agent-mail/s4","--handle","s4-codex"]
  "<role bootstrap>"
  cwd /Users/omri/.amq-squad/spike/s4/project-wt   (linked worktree; external cwd admitted)
```

Grammar notes:

- `--name s4-claude` (the plain handle) is admitted. When `--name` is
  already present, AMQ does not add its own `--name <session>/<handle>`
  (`adapter_claude.go:66`).
- `preview.capabilities[claude].allowed_argument_forms` lists only
  `--allowedTools`, `-n`, and `--name`. `--model` and `--effort` are accepted
  (`adapter_claude.go:127-128`) but not advertised. That is upstream ask 4.
- Codex `verified_provider_version` is 0.147.0, and Prepare does not check
  the installed version.

Digest sensitivity (`evidence/digest-sensitivity.json`, Prepare-only, one
field changed per probe):

| Change | Trust digest | Plan digest |
| --- | --- | --- |
| Claude `--model opus` | changed | changed |
| Claude `--effort high` | changed | changed |
| Codex `--model gpt-5.5` | changed | changed |
| Codex `model_reasoning_effort=high` | changed | changed |
| Codex `--sandbox read-only` | changed | changed |
| Wrapper args `--seat other` | changed | changed |
| `initial_input` text | **unchanged** | changed |

Argv trust is not content integrity. The trust digest binds the wrapper's
path and args (`internal/launch/wrapper.go:12`, the `Wrapper{Executable,
Args}` struct). It does not bind the wrapper binary's bytes or the bytes of
`role.md`, which the wrapper writes at exec time.
`TrustSemanticDigest` (`plan.go:244`) also excludes the initial-input
content. Subject schema 2 does record the on-disk identity of the provider
and wrapper at Prepare and refuses a swap after authorization
(`authorized_identity_changed`, `docs/launch-api.md`). That is identity
pinning for one launch, not a trusted content hash.

## Apply results

| Codex version | Apply result | Panes | Notes |
| --- | --- | --- | --- |
| 0.147.0 (spike prefix) with Claude | exit 6, `action_required`, `reason_code: evidence_missing`, `disposition: committed`, binding generation `3881094c-…` | `%14` Claude, `%15` Codex, in tmux session `amq-project-ad3b7461b1-s4-5b840157` | The capture gate passed. Per-seat observations: Claude conversation `pending`; Codex conversation `stale` (`evidence/apply-0147.json:51`); both executions `pending`. The Codex seat then stopped on codex's trust prompt (see the finding above). |
| 0.154.0 (Homebrew), Codex only, session `s4-0154` | exit 6, `action_required`, `reason_code: capture_version_unsupported`, `disposition: not_applied`, empty trust digest | none | Matches the pin at `adapter_codex.go:13,65`. Prepare on 0.154.0 had succeeded with `untrusted_config_digest`. |

Live checks after the 0.147.0 Apply:

- `tmux list-panes -a` showed two AMQ panes, with `@amq_pane_agent` set to
  `s4-claude` and `s4-codex` (`evidence/list-panes.txt`).
- Both seat extension directories hold `launch.json` (handle, runtime,
  provider argv, timestamp) and `role.md`, with mode 0600 in a 0700
  directory (`evidence/launch-record-*.json`,
  `evidence/seat-extension-modes.txt`).
- Exec in place: the process watcher saw PID 43362 run
  `amq __launch-exec …`, then `seat-exec --seat s4-codex …`, then
  `node …/codex …` (`evidence/codex-exec-chain.txt`). In the Claude pane,
  `pane_current_command` was `2.1.305`, meaning the pane's process is Claude
  itself. AMQ 0.102.0 runs the pane through `amq __launch-exec`; the plan's
  `amq coop exec` line is from older releases.
- The Codex seat's environment held `AMQ_SQUAD_SEAT`, `AMQ_SQUAD_ROOT`,
  `AMQ_SQUAD_ROLE_PATH`, plus AMQ's own `AM_ME`, `AM_ROOT`, `AM_ROOT_ID`,
  `AMQ_LAUNCH_NONCE`, and `AMQ_WAKE_OWNER` (`evidence/codex-seat-env.txt`).
- The Claude SessionStart file (`evidence/sessionstart-env.log`) contained:

  ```
  2026-10-21T00:20:15 {"AMQ_SQUAD_ROLE_PATH": "/Users/omri/.amq-squad/spike/s4/project/.agent-mail/s4/agents/s4-claude/extensions/io.github.omriariav.amq-squad/role.md", "AMQ_SQUAD_ROOT": "/Users/omri/.amq-squad/spike/s4/project/.agent-mail/s4", "AMQ_SQUAD_SEAT": "s4-claude", "AM_ME": "s4-claude", "AM_ROOT": "/Users/omri/.amq-squad/spike/s4/project/.agent-mail/s4"}
  ```

- The Claude status line read `Son5.5 M`, which is Sonnet at medium effort.
  The bootstrap ran `cat "$AMQ_SQUAD_ROLE_PATH"`, and the model replied
  `S4_ROLE_VISIBLE — seat handle: s4-claude`.
- Claude showed its own workspace-trust dialog first. I accepted it. This
  writes the project entry to `~/.claude.json`, not to
  `~/.claude/settings.json`.

## PreToolUse fault table (Claude 2.1.305, live seat)

Setup: project-scope `.claude/settings.json` only (copy in
`evidence/claude-project-settings.json`). The PreToolUse matcher is `Bash`,
with command `.claude/hooks/pretool.py` and a configured `timeout: 5`. The
fault mode is read at run time, so all trials ran in one live seat with no
restart. Each trial sent one prompt over `tmux send-keys`: "Use the Bash tool
exactly once to run: ls -1 # s4-fault-<mode>". Claude ran in the owner's
default auto mode.

Registration: the hook is a blocking `"type": "command"` hook with
`"timeout": 5` and no `async` setting (`evidence/claude-project-settings.json`).
The fail-open result is therefore not an artifact of a non-blocking
registration.

Other hooks ran in the same tool calls: the owner's user-scope claude-mem
`PreToolUse` hook (`claude-mem hook claude-code file-context`, 132–183 ms,
`hook_success`). Its time is inside column A. There is one
observation per condition and no repeats.

Timing sources:

- Column A: `tool_use` to `tool_result`, from the timestamps in the session
  JSONL.
- Column B: Claude's own hook attachment record for `pretool.py`
  (`durationMs`, `timedOut`, `exitCode`).
- Column C: whether our `PostToolUse` hook fired.
- Excerpts with tool_use ids are in `evidence/fault-jsonl-excerpts.jsonl`.

| Condition | tool_use id | Tool call proceeded? | A: tool_use to result | B: `pretool.py` as recorded by Claude | C: PostToolUse | Pane shows |
| --- | --- | --- | --- | --- | --- | --- |
| Baseline: exit 0, command `cat "$AMQ_SQUAD_ROLE_PATH"` | `toolu_014UCz…` | yes | 0.884 s (21:20:17.767 to 21:20:18.651Z) | exit 0 (Pre to Post hook 0.65 s) | fired | normal |
| Control: JSON deny | `toolu_01TaTq…` | **no** (blocked) | 0.166 s (21:20:52.723 to 52.889Z), `is_error` true | deny honored | **absent** (PostToolUse missing because the tool was denied) | "PreToolUse:Bash hook error: S4 control: deny" |
| (a) sleeps 30 s, timeout 5 s | `toolu_01ESvT…` | **yes** | **5.111 s** (21:22:16.518 to 21:22:21.629Z) | `hook_cancelled`, `durationMs 5034`, `timedOut true`, `timeoutMs 5000`; the process was killed (its post-sleep log line never appeared) | fired | nothing visible to the model |
| (b) exit 1, no JSON deny | `toolu_013Hwy…` | **yes** | **0.356 s** (21:23:10.933 to 11.289Z) | `hook_non_blocking_error`, `exitCode 1`, `durationMs 44` | fired | "Failed with non-blocking status code: s4 fault: exit 1 without JSON deny" |
| (c) hook command missing | `toolu_01YByG…` | **yes** | **0.440 s** (21:23:42.141 to 42.581Z) | `hook_non_blocking_error`, `exitCode 127`, `durationMs 15` | fired | "Failed with non-blocking status code: /bin/sh: …/pretool.py: No such file or directory" |

Conclusion: the PreToolUse hook failed open for these three faults on Claude
2.1.305. Only the explicit JSON deny blocked. Exit 2 blocks by Claude's
documented contract, but this run did not test it. In the timeout case the
hook duration was 5.034 s, and the tool-use-to-result time was 5.111 s. Plan
4.4 ("fails open on crash") holds for this version, and security must not
depend on this hook (plan 7.3).

## seat-exec prototype limits

- `os.OpenRoot(seatPath)` at `spike/seatexec/main.go:65` follows symlinks
  in the initial path. If the `agents` directory or the seat directory is
  redirected, the extension writes can land outside the AMQ root. `os.Root`
  only constrains traversal below the opened directory.
- File ownership and the permissions of existing directories are not
  checked. Only the files the wrapper creates get 0600, and only the
  directories it creates get 0700.
- Production must anchor traversal to the verified AMQ root, for example
  through AMQ's delivery-root identity (`AM_ROOT_ID`). Every component must
  be opened without following symlinks.
- The extra `--root` flag at `main.go:27` exceeds the `--seat`-only
  interface in R4. The prototype also accepts `AM_ROOT`, which is
  sufficient on its own.
- The hook was a project-scope `.claude/settings.json` stub, which replaced
  the planned user-scope plugin (plan 4.3). The fault behavior is a
  property of Claude's hook runner, but the plugin delivery path itself was
  not exercised.
- **Fix before first use in 2b:** anchor traversal to the verified AMQ root
  with no symlink following in the initial path, drop `--root`, and ship
  the hook as the user-scope plugin.

## Notify finding (AMQ 0.102.0)

Not observed live. The Codex seat never completed a turn, because it stopped
at the trust prompt, so `amq __codex-notify` never ran. The passive
process-table watcher (`notify-watch.sh`, 20 ms polling, with no change to
the owner's command) logged no `__codex-notify` process and no
`SkyComputerUseClient turn-ended` process.

I read `~/.codex/config.toml` read-only. The owner's notify is
`["…/SkyComputerUseClient.app/Contents/MacOS/SkyComputerUseClient", "turn-ended"]`.

From the v0.102.0 source (tag checked out):

- `docs/launch-api.md:155` states that AMQ forwards the identical payload to
  the operator's configured notify command after it publishes the identity.
- `internal/cli/codex_notify.go:53` records the evidence and publishes the
  identity first. `:56` forwards afterwards. A forwarding error only prints
  a warning, and the handler still returns success.
- `:66-99`: the forward reads `notify` from `CODEX_HOME/config.toml` (or
  `~/.codex/config.toml`, `:120`). It appends the payload as the last
  argument. It skips the forward if the target is the AMQ binary itself
  (`:86`). It applies a 10 s timeout (`:18`) and discards stdout and stderr
  (`:103`).
- `internal/launch/adapter_codex.go:129` builds the `-c notify=[amq,
  __codex-notify, --root, R, --handle, H]` override. It replaces the owner's
  notify for the seat, so the owner's client fires only through this forward.

On 0.102.0, then, the forward to the owner's command is designed and coded
to happen after capture. That fits the docs, but it is not empirically
verified. Verifying it needs one completed Codex turn on 0.147.0 in a
project the owner has pre-trusted.

## Worktree commit test (upstream ask 5): not run

The Codex seat was launched with cwd `/Users/omri/.amq-squad/spike/s4/project-wt`.
That is a linked worktree whose shared `.git` sits at `…/s4/project/.git`,
outside the seat's cwd. The seat stopped at codex's trust prompt before the
model could act, so the test did not run. There is no pass/fail result, and
no sandbox evidence for ask 5 came out of the launch API. The test needs the
owner to pre-trust `/Users/omri/.amq-squad/spike/s4/project`, after which the
launch can be repeated unchanged: `request.py` already sets the worktree cwd.

## Exact commands

Wrapper (`spike/seatexec`, own `go.mod`, go 1.25 for `os.Root`):

```sh
cd spike/seatexec && go vet . && go build -o ~/.amq-squad/spike/bin/seat-exec .
python3 spike/seatexec/smoke.py ~/.amq-squad/spike/bin/seat-exec     # PASS
npm_config_cache=~/.amq-squad/spike/npm-cache npm install @openai/codex@0.147.0 --prefix ~/.amq-squad/spike/codex-0.147
```

Scratch project:

```sh
git init -b main ~/.amq-squad/spike/s4/project
# .amqrc {"root":".agent-mail"} mode 0600, README, .gitignore; initial commit
amq init --root ~/.amq-squad/spike/s4/project/.agent-mail --agents operator,s4-claude,s4-codex
amq session create s4 --root ~/.amq-squad/spike/s4/project/.agent-mail --me operator --json
git -C ~/.amq-squad/spike/s4/project worktree add ~/.amq-squad/spike/s4/project-wt -b s4-codex-wt
# .claude/settings.json + .claude/hooks/{s4hook.py,pretool.py} in the project only
```

Launch. `amq-spike.sh` sets `XDG_STATE_HOME=~/.amq-squad/spike/s4/state`
with `amq/launch` pre-created, so the trust grants stay in the spike and
`~/.amq` is never created:

```sh
python3 spike/seatexec/request.py > ~/.amq-squad/spike/s4/request.json
CODEX_PIN=0147 spike/seatexec/amq-spike.sh launch --request ~/.amq-squad/spike/s4/request.json --prepare --json > prepare-0147.json
python3 spike/seatexec/digest-probe.py ~/.amq-squad/spike/s4/request.json spike/seatexec/amq-spike.sh
python3 spike/seatexec/apply-request.py request.json prepare-0147.json > apply-0147.json
CODEX_PIN=0147 spike/seatexec/amq-spike.sh launch --apply apply-0147.json --json
tmux send-keys -t %14 Down Enter            # Claude workspace trust: Yes
tmux send-keys -t %15 2 Enter               # codex trust: No, quit (owner decision)
spike/seatexec/fault-run.sh %14 deny|sleep|exit1|missing
python3 spike/seatexec/fault-table.py evidence/hook-events.jsonl ~/.claude/projects/-Users-omri--amq-squad-spike-s4-project/<session>.jsonl
amq session create s4-0154 --root ~/.amq-squad/spike/s4/project/.agent-mail --me operator --json
python3 spike/seatexec/request.py --only codex --session s4-0154 > request-0154.json
spike/seatexec/amq-spike.sh launch --request request-0154.json --prepare --json   # codex 0.154.0
spike/seatexec/amq-spike.sh launch --apply apply-0154.json --json                 # capture_version_unsupported
tmux kill-session -t amq-project-ad3b7461b1-s4-5b840157
```

## Cleanup and host state

- The spike tmux session is killed, and the watcher is stopped. No spike
  processes remain, and the owner's tmux sessions are untouched.
- `~/.amq` was not created. The launch state lives in
  `~/.amq-squad/spike/s4/state/amq/launch`.
- Outside `~/.amq-squad/spike/`, I wrote nothing directly. The providers
  wrote the following themselves:
  - Claude: its trust entry in `~/.claude.json` and the session JSONL under
    `~/.claude/projects/-Users-omri--amq-squad-spike-s4-project/`.
  - codex 0.147.0: its log and state sqlite files in `~/.codex`.
  - `~/.codex/config.toml` changed during the run, writer unknown (see the
    observation under the finding).
- `~/.claude/settings.json` and `~/go/bin/amq-squad` are untouched, and the
  main module is untouched (the spike has its own `go.mod`).

## Blockers

1. The codex folder-trust prompt needs the owner to grant pre-trust of the
   scratch project root in `~/.codex/config.toml`. It blocks the Codex role
   check, the notify observation, and the worktree commit test.

## Open items (not established by this run)

1. Live notify-forwarding evidence after capture: a completed Codex turn,
   capture `ready`, and the owner's notify command observed to fire through
   `__codex-notify`.
2. The worktree commit probe (upstream ask 5): commit from a
   `--sandbox workspace-write` Codex seat whose shared `.git` sits outside
   its cwd.
3. Codex model availability and registry verification under R9 to R12.
   Prepare accepting `--model gpt-6-astra` on 0.147.0 shows only that the
   grammar admits it. It does not show that the model serves on that
   client, and it is not a tool-call or edit probe.

## Open questions for phase 2b

1. Codex trust: should amq-squad doctor check `CODEX_HOME/config.toml` for
   each project root and ask the owner to add trust, or should the prompt be
   a documented seat-start step? Should upstream offer an AMQ-admitted
   override, such as a `-c projects.<root>.trust_level` form, or a launch
   preflight that reports `provider_trust_required`?
2. If the reproduction test shows that codex-cli writes
   `CODEX_HOME/config.toml` at start, is that acceptable for managed seats?
   See reviewer ruling 2.
3. `initial_input` is outside the trust digest. The role bootstrap text can
   change without fresh trust. Is that acceptable if the role content is
   carried by the wrapper (whose args are trust-bound) and not by the
   prompt?
4. Claude's PreToolUse fails open in all three faults. Is the guard for
   Claude seats acceptable as advisory only, with enforcement in plan 7.3?
   Should the wrapper refuse to exec when the hook command is missing
   (a startup check)?
5. Upstream ask 4 should also cover advertising `--model` and `--effort` in
   `allowed_argument_forms`. They are admitted today but not listed.
6. After the codex pin fix (AMQ #1107/#1108), the 0.147.0 prefix can be
   dropped. Until then S6 needs both the prefix and pre-trusted roots.

## Reviewer rulings (lead session, 2026-10-21 01:50)

1. The role comes only from role.md in the seat directory; initial_input
   may carry at most a pointer. The AMQ trust digest binds the wrapper path
   and argv, not the wrapper binary bytes and not role.md bytes, so content
   integrity of role.md is amq-squad's job: in 2b the Go core records the
   SHA-256 of role.md in the seat's launch record when it writes the file,
   and seat-exec recomputes it before exec and refuses to start on mismatch.
   The record lives under the AMQ root, outside the project tree, so a repo
   cannot alter it. Wrapper binary integrity is the same trust as the amq
   binary itself, an absolute path outside the project on the owner's
   filesystem: accepted, not solved.
2. Codex config mtime moved after seat start with another Codex session
   live. Writer unproven; not an owner question. The morning Codex follow-up
   reproduces it with no other Codex process on the host: hash config.toml
   before launch, after the trust prompt, after the first turn. If codex-cli
   rewrites its own config on start, the answer is "accept and document that
   codex-cli owns its config file". A per-seat CODEX_HOME stays rejected:
   the env overlay allowlist has five keys and CODEX_HOME is not one.
3. The PreToolUse fault table is the spike's best evidence for "hooks are
   UX, not security". The three rows go into the final report under
   criterion 4 and are referenced from the v3-plan amendment that keeps R32
   enforcement in the Go core. To confirm: the hook was registered as a
   blocking hook type, so fail-open is not an artifact of a non-blocking
   registration (Codex reviewer checks this).

Owner question left from S4: pre-trust the scratch project and the two
load-test repos for codex-cli.

## Codex review (reviewer-openai, xhigh)

Verdict: accepted with edits. Criterion 4 is not passed. The Claude seat is
demonstrated, and the Codex seat is blocked by owner decision. The edits
below are applied in this revision.

- **Blocker:** closing the criterion needs Codex role visibility (an
  `S4_ROLE_VISIBLE` acknowledgement from the Codex seat) and live
  notify-forwarding evidence after a completed capture. See the closure run
  at the top.
- **Major:** the earlier text attributed the `~/.codex/config.toml` write
  to the Codex seat without support. It is now an unattributed observation
  with a reproduction test.
- **Major:** the symlink limit of seat-exec was undocumented
  (`os.OpenRoot` at `main.go:65` follows symlinks in the initial path). It
  is now under "seat-exec prototype limits".
- **Minor:** the fault conclusion is scoped to the three faults on Claude
  2.1.305. The timeout row now reports measured numbers (5.034 s hook,
  5.111 s tool-use-to-result) and no longer says "exactly the timeout".
- **Minor:** the real baseline (`cat`, 0.884 s) is named. The co-running
  claude-mem hooks are noted, with one observation per condition.
- **Minor:** in the deny row, the missing event is PostToolUse, not "hook
  never ran". The blocking registration (`type: command`, timeout 5, no
  async) is documented.
- **Minor:** unchecked ownership and permissions of existing directories,
  the extra `--root` flag (`main.go:27`) beyond the `--seat`-only interface,
  and the project-scope hook stub in place of the user-scope plugin.
- **Minor:** added evidence: JSONL excerpts for the fault rows
  (`evidence/fault-jsonl-excerpts.jsonl`) and a file-mode listing
  (`evidence/seat-extension-modes.txt`).
- **Minor:** the Apply description now says the Codex conversation was
  `stale` (`apply-0147.json:51`), not pending. Argv trust is distinguished
  from content integrity (`wrapper.go:12`, `plan.go:244`).
- **Minor:** open items now list live notify forwarding, the worktree commit
  probe (ask 5), and Codex model availability and registry verification
  under R9 to R12, none of which Prepare acceptance establishes.
