# Host baseline (Mac, 2026-10-20), read-only

Shell note: probed with `bash -c 'command -v X; X --version'`.

## 1. Tools
| Tool | Result |
|---|---|
| `claude --version` | 2.1.304 (~/.local/bin/claude) |
| `codex --version` | codex-cli 0.154.0 (/opt/homebrew/bin) |
| `amq --version` | 0.77.3 (/opt/homebrew/bin) |
| `amq-remote --version` | MISSING |
| `amq-owner-sign --version` | MISSING |
| `tmux -V` | tmux 3.7c |
| `gh --version` | 2.100.0 (2026-09-03) |
| `gh auth status` | logged in, account omriariav (keyring), scopes gist, read:org, repo, workflow. One account only |
| `ollama --version` | 0.34.4 |
| `pi --version` | MISSING |
| `agent --version` | grok 1.0.25 (~/.grok/bin/agent; this is the Grok CLI, NOT Cursor) |
| `cursor-agent --version` | MISSING (so Cursor CLI is absent) |
| `grok --version` | grok 1.0.25 |
| `node --version` | v22.21.1 (nvm) |
| `go version` | go1.27.1 darwin/arm64 |

Comparisons:
- codex-cli 0.154.0 vs pinned 0.147.0: **MISMATCH** (7 minors newer). Identity capture pin does not match; either install 0.147.0 for the spike or verify capture on 0.154.0.
- amq 0.77.3 vs v0.98.0: **MISMATCH**, installed binary is 21 minors behind. amq-squad go.mod pins module v0.75.0 (`grep agent-message-queue go.mod`), so the installed binary is just above the module pin and far below v0.98.0. amq-remote / amq-owner-sign (shipped in the newer AMQ) are absent; upgrade of amq is required before the spike (install not done, read-only task).

## 2. Hardware
- `sysctl hw.memsize machdep.cpu.brand_string`: 68719476736 B = 64 GB RAM, Apple M5 Max
- `df -h ~`: 1.8Ti total, 254Gi used, **1.5Ti free** (14%)
- `pmset -g`: `sleep 1 (sleep prevented by powerd, caffeinate x5, sharingd)`; displaysleep 2, disksleep 10, womp 0, powernap 1, standby 1. System sleep timer is 1 minute but currently held off by several caffeinate processes; sleep is NOT permanently disabled. Spike needs its own `caffeinate -s` or `pmset` change (plugged in) to run unattended.
- `launchctl list | grep -i amq`: no output, no amq-remote login item.

## 3. Logins and defaults
- Claude Code: **subscription** (`~/.claude.json` oauthAccount: billingType=stripe_subscription, organizationType=claude_max, hasExtraUsageEnabled=false; settings.json env has no API key). 12 Claude seats share one Max rate limit; expect throttling.
- Codex `~/.codex/config.toml`: model = "gpt-6-astra", model_reasoning_effort = "medium", approvals_reviewer = "auto_review", personality pragmatic. No `approval_policy` or `sandbox_mode` set at top level (defaults apply). `notify` is already set to a Computer Use client (conflicts with AMQ's notify override for evidence capture, check). MCP servers: chrome-devtools, node_repl.
- `ANTHROPIC_API_KEY`: unset. `OPENAI_API_KEY`: unset (checked in a bash subshell).

## 4. Load-test repository candidates
All owned by omriariav on GitHub, all PUBLIC (`gh api repos/omriariav/<n>`). Lines = tracked source (go/ts/js/py/sh/swift/rs) via `git ls-files | xargs cat | wc -l`. Default branch main everywhere.

GitHub merge queue rule: available on public repos, and on private repos only with Team/Enterprise. `gh api user -q .plan.name` returned empty (plan field not exposed to this token), so the plan is unknown; irrelevant for public repos. Merge queue also needs a ruleset containing a `merge_queue` rule; **no repo has one** (only amq-squad has a ruleset, rules: pull_request, non_fast_forward, deletion).

| Repo | Fork? | Lang | Build | Test | LOC | Tests | Ruleset | Branch protection |
|---|---|---|---|---|---|---|---|---|
| amq-squad | no | Go | `make build` | `make test` (go test -timeout 20m ./...); `make ci` is heavy (fmt, vet, html/skill checks) | 228k | 322 test files; wall time unmeasured, likely minutes | 1 ("Protect main", active) | yes, required check "make ci" |
| amq-noc | no | Go | `make build` (`go build ./...` took 1s) | `make test` (go test ./...) | 58k | 67 test files; unmeasured | none | none (404 "Branch not protected") |
| agent-message-queue | **fork of avivsinai/agent-message-queue** | Go | `make build` | `make test` | 264k | n/a | none | none |
| claude-pulse | no | shell | none (scripts) | tests/ dir, CI .github/workflows/tests.yml | 3k | small | none | yes (0 required contexts) |
| personal-memory | **fork of vladimanaev/personal-memory** | TS | package.json | `npm test` (node --test src/*.test.ts + bash migration test) | 12k | small | none | none |
| handoff | **fork of SagiMedina/handoff** | mixed/docs | none | none obvious | 3k | none | none | none |
| omri-marketplace | no | JSON/md plugins | none | CI workflow ci.yml | 6k | CI only | none | yes, required check "CI" |
| workspace-cli | no | Go | `make build` (`go build ./...` 4s) | `make test` (go test ./...) | 62k | 34 test files | none | yes (0 required contexts) |
| ai-cli-handoff | no | Python | pyproject | unmeasured | 18.6k | n/a | none | none |
| memory-daemon | no | ? | none | none | 33k | n/a | none | none |
| yoetz | **fork of avivsinai/yoetz** | Go | Makefile | n/a | 119k | n/a | none | none |
| claude-coacher | no | md/hooks | none | none | 45 | none | none | none |
| tmux-resurrect-launchd | no | shell | none | none | 1.4k | none | none | none |

Not run: full test suites (task said no long suites). Only `go build ./...` run for amq-noc and workspace-cli.

Notes: amq-squad is the squad's own host repo, 228k LOC and a heavy `make ci`, and it is the project under development, so using it as a load-test target risks recursion; it is also the only repo with a ruleset. Forks (agent-message-queue, personal-memory, handoff, yoetz) have an upstream owner; PRs go to the fork by default but easy to mis-target with `gh pr create`; avoid for load test. claude-pulse and omri-marketplace have no real compiled tests.

**Recommended 4 for the load test** (owner-controlled, non-forks, Go with fast builds, non-critical):
1. amq-noc (Go, 58k, no protection so squads can merge freely; best candidate)
2. workspace-cli (Go, 62k, build 4s, `go test ./...`, protection with 0 required checks)
3. ai-cli-handoff (Python, pyproject; verify tests exist and speed first) or memory-daemon (verify first; unknown test story)
4. claude-pulse (shell tests/ + CI) or amq-squad itself for one realistic heavy-CI seat.
Before the spike: time `go test ./...` in amq-noc and workspace-cli (not done). To exercise a merge queue, add a ruleset with `merge_queue` to the chosen repos (none have it).

## 5. GitHub bot identity
- `gh auth status` lists a single account: omriariav. `~/.config/gh/hosts.yml`: user omriariav only (`users:` has one entry). `ls ~/.config/gh`: config.yml, hosts.yml.
- `gh api /user/installations`: 403 (token is not a GitHub App token), so no App visible from here. No second account, no known GitHub App. A bot identity must be created.

## 6. Ollama
`ollama list`:
- gemma3:4b: 3.3 GB
- llama3.2-vision:latest: 7.8 GB
- qwen2.5:32b: 19 GB

`ollama show qwen3.6:35b-a3b` and `qwen3.6:27b`: "model not found". **Neither is installed.** Download size not determinable offline (no pull; not looked up on the library). Typical 4-bit quant of a 27B-35B model is about 17-24 GB each; treat as estimate. Disk has 1.5Ti free, RAM 64 GB fits.
