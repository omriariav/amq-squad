# amq-squad 3.0, phase 2a-3: the three hook verbs

Branch `v3-2a3`, cut from `v3` at 4f2f0a36. Scope: plan 4.2 (`context`, `guard`, `evidence ingest`) as registered verbs in internal/cli, with the second reviewer's six redirects applied (grants source, verified answers, guard output contract, what guard sees, ingest locking and oversize, timing). No change to the task store, the gate flow or the launch; the gaps are named below.

Every hook verb is non-interactive and resolves identity only from `AM_ME`, `AM_ROOT` (the session root) and `AM_SESSION` plus flags (`--me`, `--root`, `--session`). A flag may fill an unset variable but never contradicts a set one. The seat must have a mailbox at `<AM_ROOT>/agents/<handle>`. There is no discovery (R52).

## What each verb reads and writes

| verb | reads | writes | exit |
| --- | --- | --- | --- |
| `context --me H [--json]` | `<root>/agents/H/extensions/io.github.omriariav.amq-squad/role.md` (role text and sha256); the rules file (see below; sha256 only); the launch record `launch.json` beside role.md (team home, profile, session); task files under `<team home>/.amq-squad/tasks[/<profile>]/<session>/*.json`, read lock-free; the gate store | nothing | 0 with output; 2 on a usage or identity error, one stderr line |
| `guard --me H --stdin` (primary) or `--tool T --arg k=v ... [--cwd D]` (tests, manual) | the hook payload (Claude Code PreToolUse `tool_name`/`tool_input`/`cwd`, or pi `toolName`/`input`); `~/.amq-squad/grants.yaml`; `<repo>/squad.yaml`; `.git/HEAD`, `commondir`, `refs/remotes/origin/HEAD` (files only, no git subprocess); `<root>/agents/` (seat count); the gate store and `~/.amq-squad/authorization-trust.json`; `AMQ_SQUAD_BRANCH` | nothing | 0 for allow, needs_gate and deny alike; 1 with no stdout on any error (bad flag, identity, malformed or over-1-MiB payload). Never 2. |
| `evidence ingest --me H` | one payload on stdin | one line appended to `<root>/extensions/amq-squad/ledger/H.jsonl` | 0 (also for malformed and oversize payloads); 2 on a usage or identity error |

Rules file for `context`: `$AMQ_SQUAD_RULES_PATH` when set, else `<team home>/.amq-squad/team-rules.md` with the team home from the launch record (or `--project`). The digest is of the file as it is now, not as it was at launch: see open question 3.

### guard output contract

One JSON object on one stdout line:

```json
{"verdict":"needs_gate","reason_code":"gate_release","reason":"no verified gate answer for this action and target; raise the gate","action_class":"tag","target_digest":"sha256:8502...","target":"git tag v1","grants":"default"}
```

Fields: `verdict` (allow | needs_gate | deny), `reason_code`, `reason` (human text), `action_class` and `target_digest` (and `target`) when a typed gate is involved, `grants` (the grants source), `grants_reason_code` (`owner_grants_insecure` when the owner file was ignored). Reason codes: `allowed`, `gate_release`, `gate_send`, `gate_credential`, `gate_grow`, `seat_merge`, `force_push`, `push_delete`, `push_other_branch`, `protected_path`, `grants_invalid`.

Guard is the in-session advisory check. Enforcement stays at the harness-side commit and the core merge; the model is never the enforcement point (R32, R36). Mapping a verdict to the hook's `permissionDecision` is the phase 4 / 2b plugin's job. Note for that plugin: `needs_gate` cannot map to `ask` in an unattended seat; the plugin should deny with a "raise the gate" reason built from `action_class` and `target`.

### What guard sees

- Write/Edit/MultiEdit/NotebookEdit and pi `write`/`edit`: the target is `tool_input.file_path` (`notebook_path`, `path`).
- Bash (and pi `bash`): the command is split best-effort (quotes, `;`, `&&`, `|`, `$( )`, backquotes, `bash -c`, env-assignment and `sudo`/`env`/`nohup` prefixes, `>`/`>>` targets). Recognized: `git push` with `--force`, `-f`, `--force-with-lease`, `--force-if-includes`, `+refspec`, `--delete`/`:branch`, `--all`/`--mirror`, `--tags`/`--follow-tags`, `refs/tags/...`; `git push` or `gh pr merge` (and the merge API) to the default branch; `git merge` while on the default branch; writes by `rm mv tee touch chmod truncate mkdir cp install ln dd sed -i perl -i` and redirections; the typed-gate commands in the `guardCommands` table (git tag, gh release, package publish, gh auth/secret/ssh-key, npm login, ssh-keygen, security add/delete-*-password, docker login, aws configure, gcloud auth, mail/sendmail, `gws ... send|reply|forward`, `amq launch` / `amq coop exec` past the seat cap); `git config credential.*`.
- MCP tools whose last name segment contains send, post, reply, forward, tweet or publish: external send.

Rule order: (1) typed gates, needs_gate unless a verified answer exists; (2) merge to the default branch, deny; (3) force, delete, `--all` and other-branch pushes, deny; (4) protected-path writes, deny; (5) allow. A grants file that does not parse is deny `grants_invalid`.

Protected paths are the grant's list plus three implicit entries: `<root>/extensions/amq-squad/` (gate store, answers, ledger), `~/.amq-squad/grants.yaml` and `~/.amq-squad/authorization-trust.json`. A seat that could write those could widen its grant or forge a gate answer. Writes into credential locations (`~/.ssh/`, `~/.aws/`, `~/.netrc`, `~/.git-credentials`, `~/.config/gh/`, `~/.docker/config.json`, `~/.npmrc`, `~/.gnupg/`, `~/.kube/config`) are the credential gate, which comes first.

### Accepted gate answer

A typed gate counts as answered only when `<root>/extensions/amq-squad/answers/<id>.json` carries an `envelope` (operatorauth `AuthorizationEnvelope`) that passes `ValidateAuthorizationPayload` and `VerifyAuthorizationEnvelope` against the owner's trust store `~/.amq-squad/authorization-trust.json`, and binds gate id (`payload.gate == "gate/<id>"`), action class (`payload.action == record.class`) and target digest (`payload.target == record.digest`), where `gates/<id>.json` is the gate record (S1 layout) and `record.digest == hookGateDigest(action, target)`. The digest is the S1 non-merge formula: sha256 over the length-prefixed `amq-squad-gate-target-v1`, the action, and the sha256 of the guard's target string. Thread text, an answer file without a valid signature, an answer for a different digest or a different action never counts. Action classes: `tag`, `github_release`, `package_publish` (gate class release), `external_send` (send), `spawn` (grow), `credential_change` (credential; not in taxonomy v1, so it cannot verify yet).

## Grants file format (provisional)

Source order: the guarded owner file `~/.amq-squad/grants.yaml` if present, narrowed by the repository's `squad.yaml` (`<repo root>/squad.yaml`) if present, else built-in defaults (12 seats, no protected paths). The session-root `grants.yaml` fallback of the first brief is dropped. An owner file that is group- or world-writable is treated as absent and guard reports `grants_reason_code: owner_grants_insecure`.

```yaml
# strict YAML subset, one key per line, '#' comments
schema: 1
max_seats: 12            # session seat cap (plan 7.2)
default_branch: main     # optional; else origin/HEAD, else main
protected_paths:         # ~/, absolute or repo-relative; trailing / or an existing dir covers the tree
  - .amq-squad/team-rules.md
  - conf/*.yaml          # * ? [ are filepath.Match globs
```

The owner file is strict: an unknown key or malformed line makes guard deny `grants_invalid`. `squad.yaml` is read leniently (2b owns its real schema): only its top-level `max_seats` (can only lower the cap) and `protected_paths` (can only add) count; `default_branch` there is ignored. The format is provisional until 2b defines `squad.yaml` and the grant schema.

## Ledger line schema (schema 1)

One Go type, `hookLedgerLine` in internal/cli/hook_ingest.go:

| field | meaning |
| --- | --- |
| `schema` | 1 |
| `seq` | monotonic per ledger file, one past the last parseable line, assigned under the flock |
| `at` | wall clock, RFC 3339 UTC |
| `handle`, `session` | the seat |
| `event`, `tool`, `tool_use_id` | from `hook_event_name`/`type`, `tool_name`/`toolName`, `tool_use_id`/`toolCallId` |
| `outcome` | the payload's `outcome`, else `ok`/`error` from `tool_response`/`toolResult`/`result` (`is_error`, `isError`, `error`) |
| `payload_sha256`, `payload_bytes` | over the full raw stdin bytes |
| `payload` | the compacted JSON payload (valid JSON) |
| `malformed`, `payload_b64` | invalid JSON: the raw bytes in base64 |
| `truncated`, `payload_b64` | payload over 1 MiB: the first 64 KiB in base64, sha256 and length of the full bytes |

File 0600, directory 0700, `O_APPEND`, an exclusive flock on the ledger file itself across the seq read and the append (O_APPEND alone does not keep lines above PIPE_BUF whole, and seq would race), fsync after each line. A torn last line (crash mid-write) is newline-terminated before the next line and skipped by the seq scan. The concurrency test runs 8 writer processes with 512 KiB payloads and asserts 8 whole lines with seq 1..8; with the flock removed it fails (checked by hand).

## Guard test matrix (TestGuardRuleMatrix, 56 rows, each run twice for determinism)

| class | allow | needs_gate | deny |
| --- | --- | --- | --- |
| tag and release | `git tag v1.2.0` with a verified answer; `gh release create` with a verified answer; `git tag -l` | `git tag v1.2.0`, `gh release create`, `npm publish` without an answer; **answer for the same action but a different target digest** (`git tag v9` against the v1.2.0 answer); answer signed for another digest; forged signature; answer for another action | verified tag answer but `&& gh pr merge 7` (seat_merge) |
| external send | MCP send with a verified answer; MCP read tool | MCP `send_message`; `gws gmail send` | verified answer but owner grants invalid (grants_invalid) |
| credential change | `gh auth status` | `gh auth login`; Write `~/.ssh/config`; `git config credential.helper`; `echo >> ~/.ssh/authorized_keys` | `gh auth status; git push -f` (force_push) |
| growth beyond the grant | `amq launch` within 12; `amq coop exec` past the cap with a verified spawn answer | `amq launch` past owner `max_seats: 2`; past a `squad.yaml` cap of 2 | verified spawn answer but `git push origin main` (seat_merge) |
| merge to default | `git merge origin/main` on the seat branch | `git tag v3 && gh pr merge 1` (gate first) | `gh pr merge`; merge API `PUT .../pulls/12/merge`; `git push origin seat-cto:main` |
| pushes | own branch, implicit `git push`, `HEAD`, `git -C . push` | `--tags`; `refs/tags/v1` | other branch; `--force`; `-f`; `+refspec`; `--force-with-lease`; `--all`; `:branch` delete; inside `bash -c` |
| protected paths | another file; redirect to /dev/null | credential write under a protected `~/.ssh/` | Write of a listed file; dir tree; glob; `rm`; a `squad.yaml`-added dir; the gate store; `> ~/.amq-squad/grants.yaml` |

Other guard tests: seat and default branch sources (on `main`, AMQ_SQUAD_BRANCH, grants `default_branch`); grants sources and format (session root not read, squad.yaml narrows only, insecure owner file ignored with the reason code, strict parse errors); output contract (one JSON line per verdict, action_class and target_digest on needs_gate, exit 1 and empty stdout on a malformed payload). Context tests: seeded role, rules, four tasks (the most recently renewed own lease wins), four gates (forged answer stays open, another seat's gate hidden). Identity errors: six cases per verb, exit 2 for context and ingest, exit 1 for guard, one stderr line, no stdout.

## Timings

Unit test (`TestHookVerbsColdRunBound`, in-process, 20 gate records, a three-segment command): cold run bounded at 200 ms. Logged on this Mac: guard cold 1.2 ms, warm p50 1.1 ms, p95 1.5 ms; context cold 1.1 ms, warm p50 0.8 ms, p95 1.1 ms.

Process level, the built binary as a hook would run it (scratchpad timing.py; 20 gate records, 30 task files, a 40-line role; 20 warm runs after one first run):

| verb | first run | warm p50 | warm p95 | p95 under 50 ms |
| --- | --- | --- | --- | --- |
| guard --stdin | 408.7 ms | 6.0 ms | 8.7 ms | yes |
| context --json | 6.5 ms | 6.5 ms | 6.9 ms | yes |
| evidence ingest (fsync) | 11.4 ms | 9.1 ms | 10.4 ms | yes |

The 408.7 ms first run is macOS's first exec of a freshly built binary (signature assessment), paid once per build, not per hook; every later run of the same binary is warm.

## Lines and manifest

New non-test files, fate keep, v2_lines 0, target_lines equal to the size: hook_context.go 163, hook_guard.go 398, hook_guard_env.go 231, hook_identity.go 122, hook_ingest.go 184, hook_store.go 218 (1,316). Edited: evidence.go 774 -> 777 (deferred-replace, the `ingest` dispatch and usage line), command_registry.go 84 -> 88, cli.go 192 -> 194 (root usage examples). Non-test lines under internal/: 42,992 -> 44,317 (+1,325). manifest-check --final: target sum 36,403 against the 36,410 band top (7 lines of headroom: the verbs were compacted to fit check 4); the gate total 41,883 is NOT MET and accepted by the end-of-2a-2 decision as before.

Tests changed in kept files: cli_test.go (`context` leaves the v2-removed list, as `brief` did; `context` and `guard` join the public catalog), help_test.go (help surfaces and exit-0 help for the three verbs and `evidence --help`).

## Open questions for 2b and phase 3

1. Gate store location today vs 2b. Today `gate raise` / `gate close` / `verify action` work on AMQ mailbox threads and nothing in the binary writes `<root>/extensions/amq-squad/gates/` or `answers/`; the verbs read the S1 layout so they work the day the core writes it. 2b/phase 3 must decide that the core writes the gate record (O_EXCL, before the question) and the answer file there, add `raised_by` to the record (S1's record has no raiser; context lists records without one for every seat), and settle the trust store path (`~/.amq-squad/authorization-trust.json` here; `verify` takes `--trust-store`).
2. "Current task" before the 2b task store. Today it is the v2 store's in-progress task whose lease the handle owns, most recently renewed, read lock-free from the launch record's team home and profile. With no launch record and no `--project` there is no task. 2b's task store with leases and pools replaces this read.
3. Which file the rules digest hashes before seat-exec exists. Today: `$AMQ_SQUAD_RULES_PATH`, else `<team home>/.amq-squad/team-rules.md` (team home from `launch.json`), hashed as it is now. Nothing records the digest at launch; seat-exec (2b) should record the rules path and sha256 in the launch record, as R4 does for role.md, and context should report that digest and flag drift.
4. Owner grants file permissions: a group- or world-writable `~/.amq-squad/grants.yaml` is ignored (defaults apply) with `owner_grants_insecure`. Should 2b fail closed instead (deny every typed-gate class), and should `doctor` check the mode?
5. squad.yaml location and schema: read from `<repo root>/squad.yaml`, top-level `max_seats` and `protected_paths` only. 2b fixes both.
6. Seat branch: guard uses `AMQ_SQUAD_BRANCH` when set, else the checked-out branch unless it is the default. The launch does not set `AMQ_SQUAD_BRANCH` yet; seat-exec should.
7. R37 says hook configuration and rules files are guarded paths; per the brief the defaults protect nothing beyond the implicit three. Should the defaults include team-rules.md, CLAUDE.md/AGENTS.md managed blocks and `.claude/settings*.json`?
8. credential_change has no operatorauth taxonomy v1 action, so a credential gate can never be answered yet (phase 3 taxonomy).
9. needs_gate in the Claude plugin: unattended seats cannot `ask`; the plugin denies with "raise the gate", and the lead raises it. The target string conventions (`git tag v1`, `gh release create v1`, `<mcp tool> <recipient>`, `grow <root> seats=N`, `write <path>`) are what the gate record's subject must hash; 2b/phase 3 should own them in one place.
10. External send coverage is a name heuristic for MCP tools plus a short command list; `curl -X POST` and similar are not classified.
