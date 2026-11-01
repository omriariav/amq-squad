# amq-squad

**amq-squad 3.0 runs a squad of coding agents (Claude Code, Codex and the
built-in pi) on one repository, inside grants the owner sets, over durable
[AMQ](https://github.com/avivsinai/agent-message-queue)** by
[Aviv Sinai](https://github.com/avivsinai). It is a rebuild on AMQ's coop
primitives: the v2 launch and orchestration code is gone, and what remains is a
smaller, security-first core.

The model in five lines:

- **AMQ is the rail.** Seats start through AMQ's launch API; mail, wake and
  cross-project routing are AMQ's.
- **The squad is one file.** `<repo root>/squad.yaml` names the seats, the lead
  and the narrowing of the owner's grant. It is written only through a digest
  the owner (manual mode) or the grant (auto mode) approves.
- **The owner's limits live outside the repository.** `~/.amq-squad/grants.yaml`
  sets the mode, the seat and cost caps, the runtimes and the merge grants;
  squad.yaml can only narrow it.
- **Seats never commit, merge or release.** A seat edits its worktree; the
  harness commit step commits only the task's declared files, the merge train
  lands PRs inside the merge grant, and tags and releases wait for the owner.
- **Four typed gates stop for the owner:** tag and release, external send,
  credential change, and growth beyond the grant. The owner answers from Buzz on
  the phone with a signature the Go core verifies.

## Contents

- [What's new in 3.0](#whats-new-in-30)
- [Install](#install)
- [Requirements](#requirements)
- [First run](#first-run)
- [Running a squad](#running-a-squad)
- [The owner's Buzz face](#the-owners-buzz-face)
- [Command map](#command-map)
- [What 3.0 does not do](#what-30-does-not-do)
- [Uninstall](#uninstall)
- [Upgrading from v2](#upgrading-from-v2)
- [Where the plan and evidence live](#where-the-plan-and-evidence-live)

## What's new in 3.0

Everything: [docs/v3.0.0-release-notes.md](docs/v3.0.0-release-notes.md) lists
the release by phase. 3.0.1 is the first fix release, from the first squad run
on this repository: [docs/v3.0.1-release-notes.md](docs/v3.0.1-release-notes.md).
3.0.2 is the second, from the second day of that run and the 3.0.1 release:
[docs/v3.0.2-release-notes.md](docs/v3.0.2-release-notes.md). In short:

- `squad.yaml` schema 1 replaces the v2 profile, through `team propose` and
  `team apply` ([docs/squad-yaml.md](docs/squad-yaml.md)).
- A task store with leases and pools, and a scheduler process beside the lead.
- `seat-exec` in front of every seat: a scrubbed environment, the identity from
  the launch record, a read-only identity proof.
- The harness commit step, the merge train under the standing merge grant, and
  the release flow behind the `tag` and `github_release` gates.
- A runtime registry with the owner's pins, re-hashed at every launch.
- The Claude Code hooks plugin ([docs/claude-hooks.md](docs/claude-hooks.md)),
  the built-in pi package, and the Buzz attachment on amq-remote's mailbox
  adapter.
- `amq-squad migrate` for v2 profiles and `amq-squad uninstall`
  ([docs/uninstall.md](docs/uninstall.md)).

## Install

```sh
go install github.com/omriariav/amq-squad/v3/cmd/amq-squad@v3.0.2
amq-squad version
```

The module path carries `/v3`. A `go install` build has **no embedded pi
runtime**: `amq-squad lead`, solo mode and pi seats refuse with "this amq-squad
build carries no pi runtime". The pi runtime is embedded only by a darwin arm64
build from a checkout, with `scripts/build-pi-runtime.sh all` (one networked
fetch of Node, fd and rg with checked SHA-256s, then an offline `go build -tags
amqpi` to `build/pi/amq-squad`). Claude and Codex seats need no pi runtime.

The Claude Code plugin carries the skills and the seat hooks. Install it at
user scope ([docs/claude-hooks.md](docs/claude-hooks.md)):

```
claude plugin marketplace add omriariav/amq-squad --scope user
claude plugin install amq-squad@amq-squad --scope user
```

For Codex, add the same marketplace with `codex plugin marketplace add
omriariav/amq-squad`. The skills (`amq-squad:wizard`, `amq-squad:cli`,
`amq-squad:orchestrator`) are described in [docs/skills.md](docs/skills.md).

## Requirements

`amq-squad doctor` probes all of these and names what is missing:

- `amq` 0.116.2 or later on `PATH` (and `amq-remote`, `amq-owner-sign` at the
  same release when installed); `amq-squad doctor` refuses an older one
- Claude Code (`claude`) for Claude seats, with the owner's pin and token file
  (below)
- Codex (`codex`) for Codex seats, with each seat repository trusted in
  `~/.codex/config.toml`
- the built-in pi (a pi-runtime build, above) for the pi lead and pi seats;
  `ollama` for local pi models
- `tmux`: AMQ creates each seat as a tmux pane
- `gh`, pinned in the registry, for the harness's PRs and HTTPS pushes
- macOS on Apple silicon (see "Platforms" below); Go 1.25+ to build
- `pandoc` 3.11 only to regenerate or check `README.html` and `docs/skills.html`

AMQ 0.116.2 is the minimum supported release: it is the AMQ module 3.0 links,
and every 3.0 path needs its launch adapters (pi seats through the pi adapter,
the Codex sandbox pins in the launch request) and its mailbox. `amq-squad
doctor` refuses an installed `amq`, `amq-remote` or `amq-owner-sign` below it
and prints the upgrade line (`amq upgrade --all -y`, or `brew upgrade
avivsinai/tap/amq`). CI runs the real-AMQ lane at the pinned v0.116.2, which
is required, and at `latest` as a non-blocking canary; a canary failure reads
as upstream drift, not as a support claim.

### Platforms

3.0 supports macOS on Apple silicon. On Linux the binary builds and the Claude
hook script is tested, but seats are not supported in 3.0, for two reasons
found on the first Linux test run. First, the launch's proof socket fails under
a long project path (the Unix socket path bound). Second, ext4 and tmpfs reuse
an inode right after an unlink and recreate, so the device-and-inode identity
checks the harness uses against replacement of its seed, its sockets and its
anchored directories are weaker there than on APFS, which never reuses one.
Linux seats are 3.1 work.

## First run

Each step below is the owner's, at the owner's own terminal. amq-squad never
writes the owner's Claude or Codex configuration and never answers a trust
prompt for you.

**1. The AMQ root.** The repository needs an AMQ queue first: run `amq coop
init` once inside it (AMQ's verb; it writes `.amqrc` and `.agent-mail/`).

**2. Probe the host.** doctor checks AMQ and its launch API, the tools and
their logins, the registry, the pins, the token file, the grants file, the
Codex trust rows and the owner's global agent config. It writes nothing
unless asked:

```sh
amq-squad doctor
amq-squad doctor --project .
amq-squad doctor --json
```

**3. The registry.** `amq-squad doctor --write-registry` writes
`~/.amq-squad/registry.yaml`: one entry per runtime, model and effort it
detected. The lead proposes seats from verified entries only. Seats never
probe: inside a seat `doctor --probe` and `doctor --write-registry` refuse
(`seat_owner_state_refused`), and the owner runs `amq-squad doctor --probe`
from their own terminal.

**4. The gh pin.** A seat's provider (claude, codex, grok, cursor-agent)
resolves on `PATH`; nothing is pinned or probed before a seat starts. The one
pin is gh, needed for an HTTPS push target: the registry's `pins:` list names
it by absolute path and sha256, and the commit step re-hashes it before the
push (`tool_unpinned` or `tool_pin_mismatch` refuse that push, never the
launch). doctor's `gh pin` row prints the exact entry to add.

```yaml
pins:
  - name: gh
    path: /opt/homebrew/bin/gh
    sha256: <64 hex>
```

**5. The token file.** Claude seats authenticate with a long-lived token, not
your login: run `claude setup-token` and save the token it prints, alone, to
`~/.amq-squad/claude-token` with mode 0600. A missing or loosely-permissioned
file refuses Claude seats (`claude_token_insecure`). Never paste the token into
a command line.

**6. The grants file.** `~/.amq-squad/grants.yaml` (strict YAML subset, mode
0600; a group- or world-writable file fails closed to manual mode with no merge
grant). Without it the built-in defaults apply: auto mode, 12 seats per
session, no merge grant. A small example:

```yaml
schema: 1
mode: manual
max_seats: 6
permitted_runtimes: [claude, codex, pi]
merge:
  - repository: you/app
    base_branch: main
    ci_required: true
    size_cap_lines: 400
```

The keys a squad can narrow, and the ones it can never set, are listed in
[docs/squad-yaml.md](docs/squad-yaml.md).

**7. Codex trust.** A fresh Codex seat stops on its folder-trust prompt. This
prints the lines you add to `~/.codex/config.toml` yourself, and the candidate
Codex notify fan-out line:

```sh
amq-squad trust .
```

**8. The squad.** `team propose` prints every file the squad needs
(`squad.yaml`, `.amq-squad/team-rules.md`, the `CLAUDE.md` and `AGENTS.md`
pointer stubs) with one digest, and writes nothing. `team apply` with that
digest and the same options writes them; a changed option
(`squad_yaml_digest_mismatch`) or a hand edit in between
(`squad_yaml_changed`) writes nothing:

```sh
amq-squad roles
amq-squad team propose --project . --roles cto,fullstack,qa --runtime fullstack=codex --worktree fullstack=../app-96 --lead cto --session issue-96
amq-squad team apply --digest <digest> --project . --roles cto,fullstack,qa --runtime fullstack=codex --worktree fullstack=../app-96 --lead cto --session issue-96
amq-squad team show --project .
amq-squad team check --project .
```

A per-seat flag takes one comma list: `--worktree a=x,b=y`, never the flag
twice (a repeated per-seat flag is refused). A custom role is staged first with
`amq-squad role draft researcher --purpose "Investigate ambiguous behavior"`.

Adding a seat to a running squad: `--add-seat HANDLE:ROLE:RUNTIME:MODEL:EFFORT`
appends one seat to the squad.yaml on disk through the same digest (the team
rules stay as they are), and the next `scheduler relaunch` reads squad.yaml
again and launches it with the roster. The lead's screen does all three with
`/squad add`, then `seat`, and one confirmation (every seat relaunches):

```sh
amq-squad team propose --project . --add-seat rev-1:implementer:codex:gpt-5.4:high
amq-squad team apply --digest <digest> --project . --add-seat rev-1:implementer:codex:gpt-5.4:high
amq-squad scheduler relaunch --reason "add rev-1" --start 2026-11-01T09:00:00Z
```

The relaunch swaps the roster in one step after every seat finished what it
holds and closed. It stops no seat itself: a seat removed from squad.yaml
leaves at that close and is not launched again, and a changed line applies at
that seat's launch. A squad.yaml that does not compile refuses the relaunch
`squad_invalid` before anything drains. `--start` is the key press: the added
seat's launch budget counts from it.

**9. The brief.** The workstream's goal, at `.amq-squad/briefs/<session>.md`:

```sh
amq-squad brief --goal "fix issue 96" --session issue-96 --dry-run
amq-squad brief --seed-from file:./prompt.md --session issue-96
```

**10. Launch.** squad.yaml is the one source: each seat's handle, role,
runtime (the binary), model and effort, the session and the lead's handle
(`lead:`). A seat's worktree is derived per handle under
`<repo>/.worktrees/<handle>`, never the repository root. The first run is
one command, in the repository:

```sh
amq-squad up
```

`up` runs seven steps in order and refuses at the first that fails, by
name: it compiles squad.yaml (before anything is installed or created);
installs the tagged pi build (`scripts/build-pi-runtime.sh` at a tag,
`build/pi/amq-squad` or `--binary PATH`) next to the `amq-squad` on PATH
under a temporary name and renames it over it (`--skip-install` keeps an
installed binary that already reports a tag); creates the AMQ root and the
session when absent; runs that binary's doctor in the root; checks the
recorded lead (a lead whose pid is alive and whose tmux pane
exists is never stopped: `lead_running`, with its pid, its pane and the
attach command; tmux that cannot be asked is `tmux_unavailable`, and both
stop nothing; a dead one, and a scheduler serving it, are stopped by their
recorded identity and recorded as decisions); starts the lead in its own tmux session,
`squad-lead-<repo>-<sha5>`; and waits for the roster's outcome in the
decisions log, then prints the roster and `tmux attach -t <session>`.
`up --json` prints the steps and their outcomes. Each seat's launch budget
is in status: the start (`up`'s own) to seat live, its accepted proof,
within a 10 s ceiling, and to its first AMQ message, with no ceiling; a seat
over the ceiling carries a warning naming its slowest segment.

In the lead's pi, the squad screen sits below the editor: the lead handle
and the root, then seats (runtime and model, state, launch outcome and
budget, warnings), tasks (state, seat, PR, block reason), the merge train
(the current and last runs, alerts) and open gates, each block closed by
its newest refusal. It refreshes when the task store or the scheduler
state changes; `/squad` collapses it to one footer line of counts.

The screen has four keys, typed in the lead's pi as `/squad <key>`. pi
hands a command to the extension, never to the model, and each key runs its
verb from the lead's own process, so a key that is a scheduler decision is
proved like the lead's own tools. The model's tools gain nothing.

- `/squad add` adds a task: the title, the class, the declared files and
  the pool go to `task add`.
- `/squad reset` resets a blocked task through `task reset`, onto another
  pool when you pick one (`--pool`).
- `/squad clear` acknowledges a seat's warning line through `scheduler
  clear`. The line leaves the screen for that launch only, and the seat's
  state does not change.
- `/squad answer` shows an open typed gate with its kind, target and
  16-hex typed prefix. It posts the question over Buzz when it has not
  reached Buzz, and says the gate is waiting there. Buzz is the one door
  for the four typed gates; the terminal door is not built yet.

A key's refusal shows on the screen as the verb printed it.

Two entry points, each one AMQ launch request with every seat behind
`seat-exec`:

- the pi lead in team mode with the scheduler beside it, run in the
  repository (what `up` starts in its tmux session); the lead never edits
  code, it dispatches through the task store, and the scheduler launches
  every seat but the lead at start:

  ```sh
  amq-squad lead --team --dry-run
  ```

  (drop `--dry-run` to start it; a pi-runtime build only). `scheduler run`
  creates an absent AMQ root and session the way `up` does. The lead takes
  squad.yaml's AMQ identity (its session root, its `lead:` handle); an
  `AM_ME`/`AM_ROOT` already in the environment must match it or it refuses.
  One lead per root: a second `lead --team` on the same root is refused
  `lead_running`, naming the first lead's pid and tmux pane. Every launch
  carries the owner's standing answer to AMQ's trust confirmation.
- the mayor inside the owner's fleet grant: `operator start-squad` writes the
  squad.yaml through the same digest flow and launches it; in manual mode it
  returns a proposal instead.

In a directory with no AMQ identity, `amq-squad lead` (or a bare `amq-squad`
on a terminal) starts the pi lead in solo mode: its state lives under
`~/.amq-squad/solo/`, nothing is written in the repository.

## Running a squad

The lead's loop is one status read, one bounded action, then park; AMQ wake
delivers the next message. `amq-squad:orchestrator` is the lead's playbook.

```sh
amq-squad status --project . --session issue-96 --json
amq-squad task add --title "Fix the parser" --class implement --files internal/parse/ --pool claude:sonnet:medium --project . --session issue-96
amq-squad task list --project . --session issue-96
amq-squad gate status --project . --session issue-96 --json
```

- **Status** is the versioned contract of [docs/status-contract.md](docs/status-contract.md):
  four buckets (Needs you, Working, Ready, Inactive), seats (with each
  seat's worktree, branch, launch outcome, launch budget and admitted
  wake), tasks (with evidence and PR), the attention queue and gates, with
  argv actions; the lead and the scheduler (pid, start), the decisions
  log's tail and the merge train's runs. amq-noc renders it for the
  fleet; `amq-squad status --fleet --json` reads every squad of
  `~/.amq-squad/fleet.yaml`.
- **The commit step.** On a seat's done report the scheduler runs
  `seat commit`: every changed path against the task's declared files, the
  protected paths and the size cap, then a commit under the harness identity
  and a fast-forward push of the task's branch. A path outside the set comes back
  to the lead, whose decision is `task widen ID --files ... --reason ...`
  (no `--me`: the scheduler proves the lead from the process). Binding is
  the default under a merge grant: the step opens the pushed branch's draft
  PR and binds it to the task with no flag, and `--no-open-pr` is refused
  while the repository has a merge grant. The binding check runs before the
  commit: a task branch that carries an open PR bound to another task is
  refused (`pr_bound_elsewhere`) with nothing committed or pushed. Each task
  commits on its own branch, `seat/<handle>-<task id>`, from the remote base
  tip fetched when it is granted, with its own draft PR; between tasks the
  seat's worktree rests on `seat/<handle>` at the launch's base.
- **The roster.** The scheduler launches every seat of squad.yaml but the
  lead in one AMQ launch at start; a seat with no lease is
  parked and gets the next task of its pool. At every launch, the roster's
  included, a seat's worktree and its `seat/<handle>` branch start from the
  base branch's tip fetched from the grant's push URL, the launch record
  carries that base, and a seat branch holding commits the tip does not,
  remote and unmerged or local and unpushed, refuses the launch by name
  (`seat_branch_unmerged`, `seat_branch_unpushed`) for the lead. `max_seats` caps the leases held
  at once, not the seats up. AMQ refuses a fresh seat into a session whose
  binding is present, so a gone seat is not replaced on its own: its pool's
  next task blocks for the lead (`seat_replacement_needs_relaunch`), and the
  lead's `amq-squad scheduler relaunch` lets the other seats finish, closes
  them and launches the roster again, as squad.yaml reads at that moment (a
  seat added since the start launches with it). The lead and the scheduler run outside
  the roster's tmux session (the lead's own seat is never in the roster). A
  restarted scheduler adopts the live seats. A roster launch that AMQ refuses stops the scheduler with the stage,
  AMQ's reason and the detail in its decisions log, and the lead's
  supervisor does not restart it, nor a scheduler refused because another
  holds the squad's lock. A seat launch that fails or never resolves
  is a `seat_launch_failed` decision with its stage, AMQ's reason code and the
  redacted detail; status shows that reason on the seat's row in place of
  `seat_starting`, and a second failure of the pool with the same reason
  blocks the waiting task for the lead instead of retrying. Each tick the
  scheduler checks every live roster seat's process against its accepted
  proof (pid and start stamp) and takes a seat whose process ended as gone
  (`process_exited`), so its pool's next task takes the relaunch path.
- **Merge.** `merge-train --request FILE --dry-run` plans and checks the lead's
  ordered PR set against the merge grant; the real run uses the owner's gh,
  outside any seat. Seats never merge.
- **Release.** `release prepare` raises the `tag` and `github_release` gates;
  `release publish` runs each only with its verified owner answer.
- **Seat warnings.** What used to stop a seat at launch is a warning on its
  status row: a Claude seat whose hook made no report since its launch
  (`claude_hooks_silent`), its worktree's Claude allow rules
  (`claude_allow_rules_present`), a CLAUDE.md importing a file outside its
  worktree (`claude_external_includes_pending`). The seat starts; nothing is
  quarantined. seat-exec still denies a seat's write to the owner's global
  agent config. So are a seat over the launch ceiling
  (`launch_budget_exceeded`), a seat whose wake is off with AMQ's reason
  (`wake_off`: a Codex seat without AMQ's dialog hook, a pi seat), a seat at
  an open harness dialog (`seat_stalled_at_dialog`) and a pi seat with no key
  for its provider (`pi_provider_key_missing`).

Check whether an action is approved, and the evidence of a merge or a release:

```sh
amq-squad verify action --project . --session issue-96 --gate rel-v3 --action tag --target "git tag v3.0.0" --json
# skill-invocation-check:skip — exits 1 ("merge evidence failed") where no merge-train run is recorded, by design
amq-squad verify merge --repository you/app --pr 12 --root /path/to/app/.agent-mail/issue-96 --json
# skill-invocation-check:skip — exits 1 ("release evidence failed") where no release flow is recorded, by design
amq-squad verify release --tag v3.0.0 --root /path/to/app/.agent-mail/issue-96 --json
```

`verify action` exits 0 approved, 10 pending, 11 denied, 12 no gate, 13 unbound.

## The owner's Buzz face

One Buzz agent, the body of one `amq-remote` share, attaches to the operator
session's mailbox (or with `--lead` to one squad lead). The owner's guide is
[docs/v3-buzz/README.md](docs/v3-buzz/README.md).

- **Enrollment** is the owner script `docs/v3-buzz/enroll.sh <owner-npub>
  <relay-url>`, run once per 30-day grant window. The Buzz key reaches
  `amq-owner-sign` only through a password-manager pipe
  (`BUZZ_OWNER_KEY_CMD`); nothing in amq-squad, `amq` or `amq-remote` reads it.
  The script ends with `remote bind`, which writes the owner's gate binding
  `~/.amq-squad/gate-binding.json` (no secret in it).
- **The mailbox adapter.** `amq-remote serve` delivers each DM from the owner
  into the share's mailbox; `remote attach` answers each once (`accepted`,
  `refused`, `routed`), and serve posts the reply under the message. A message
  starting with `@<lead>` is routed to that squad lead.
- **The gates.** A typed gate's question goes to the owner's DM with its reply
  thread; the owner answers in that thread, and the core of the squad that
  raised it fetches and verifies the signed event itself. An answer outside the
  thread is refused with one re-ask.

```sh
# skill-invocation-check:skip — exits 1 until fleet.yaml names the operator session (or --lead names a launched lead)
amq-squad remote status
```

## Command map

`amq-squad --help` and each verb's `--help` are the reference. The verbs:

| Area | Verbs |
| --- | --- |
| Host and setup | `doctor`, `trust`, `roles`, `role draft`, `team propose/apply/show/check/draft-rules`, `brief`, `migrate`, `uninstall`, `version` |
| Launch | `lead`, `scheduler run/relaunch`, `seat-exec` (AMQ runs it), `pi run/verify/path` |
| Work | `task add/list/show/claim/renew/done/fail/block/release/reset/set/cancel/widen/reconcile`, `dispatch`, `seat commit`, `worktree` |
| Hook verbs | `context`, `guard`, `evidence ingest`, `evidence list` |
| Gates and landing | `gate raise/answer/status/post/close`, `verify action/authorization/merge/release/release-plan/rebind`, `merge-train`, `release prepare/publish` |
| Fleet and owner | `status`, `status --fleet`, `fleet list/add/remove/operator`, `operator inbox/answer/start-squad`, `remote bind/attach/status`, `routing report --week`, `amq env/route` |

Exit codes common to every verb: 0 success, 1 usage or user error, 2 system or
runtime error, 3 partial success; a verb with more (`launch` 10, `verify action`
10 to 13, `seat commit` and `task widen` 15) lists them in its help.

## What 3.0 does not do

These are the recorded limits of 3.0. The full list, with its reasoning, is in
[docs/v3-plan.md](docs/v3-plan.md) (section 7 for the security model, section
16.4 for the phase 5 items and the open acceptance rows):

- **Same-UID boundary.** Seats run as the owner's user. A same-UID process can
  read the owner's gh token from the login keychain (guard denies the CLI reads
  and the scrub removes the variables, but neither is a wall), forge AMQ mail
  as the lead, forge hook reports, and in principle swap the pi runtime in the
  milliseconds between its re-hash and its exec. The ledgers are sealed so a
  forgery is detected and blocks, not prevented. A dedicated keychain or GitHub
  App token and a read-only runtime image move to 3.1.
- **Hooks are UX and evidence, not enforcement.** Claude Code's PreToolUse hook
  fails open on a timeout or an error; enforcement is the commit step and the
  merge train. Codex, Grok Build and Cursor seats have no hooks.
- **One owner DM at a time.** The mailbox adapter answers a second DM `busy`
  while one is open; wait for each result row.
- **The pi runtime is darwin arm64 only** and is not in a `go install` build.
- **Open acceptance rows at the tag** (plan 16.4): the Claude live rows wait on
  the owner's token file, the first real merge-train run on a harness-bound PR,
  the pi launch through `amq launch` on the installed AMQ, and amq-noc 1.0.
- No v2 verb survives: `init`, `start`, `plan`, `resume`, `down`, `wizard`,
  `goal`, `team init/sync` and the pane drivers are gone, with no aliases.

## Uninstall

```sh
amq-squad uninstall --project . --session issue-96
```

A dry run by default: it prints every path it would remove. `--write` removes
the session state, the per-seat config dirs, the pinned tool copies and the
worktrees it created; the owner files only with `--owner-files`, the sealed
ledgers only with `--purge`. It prints the Claude plugin and Codex trust
commands for you to run. See [docs/uninstall.md](docs/uninstall.md).

## Upgrading from v2

[MIGRATION.md](MIGRATION.md) "Moving to 3.0" is the list. The converter is a
dry run unless `--write` is given, never writes `grants.yaml` or
`registry.yaml` (it writes `grants.yaml.proposed` beside them), and prints every
v2 field it did not map:

```sh
# skill-invocation-check:skip — exits 1 (migrate_input_invalid) where no v2 profile exists, by design
amq-squad migrate .amq-squad/team.json
```

## Where the plan and evidence live

| What | Where |
| --- | --- |
| The 3.0 plan, its rulings, phases and recorded limits | [docs/v3-plan.md](docs/v3-plan.md) |
| The phase briefs and their rulings | [docs/v3-phase2-briefs.md](docs/v3-phase2-briefs.md) |
| Execution logs and evidence per brief | [docs/v3-spike/](docs/v3-spike/) |
| Release notes | [docs/v3.0.2-release-notes.md](docs/v3.0.2-release-notes.md), [docs/v3.0.1-release-notes.md](docs/v3.0.1-release-notes.md), [docs/v3.0.0-release-notes.md](docs/v3.0.0-release-notes.md) |
| squad.yaml schema 1 | [docs/squad-yaml.md](docs/squad-yaml.md) |
| Status contract, schema 1 | [docs/status-contract.md](docs/status-contract.md) |
| Claude Code seat hooks | [docs/claude-hooks.md](docs/claude-hooks.md) |
| The skills | [docs/skills.md](docs/skills.md) |
| Buzz, the owner's guide and run | [docs/v3-buzz/](docs/v3-buzz/) |
| Uninstall | [docs/uninstall.md](docs/uninstall.md) |
| Follow-ups after 3.0 | [docs/v3-followups.md](docs/v3-followups.md) |

amq-squad is MIT licensed ([LICENSE](LICENSE)).
