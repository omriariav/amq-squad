# amq v0.71.0 / v0.72.0 / v0.73.0 adoption verdict

Measured re-verification of amq v0.71.0, v0.72.0, v0.73.0 (released
2026-08-26/27) against amq-squad v2.30.0's launchapi path, in the style of
the t13 v0.70.0 diff-verdict. No source changes in this PR.

Method: each amq version installed into its own temp `GOBIN`
(`GOBIN=/tmp/amq-verdict-bins/vX.Y.Z go install
github.com/avivsinai/agent-message-queue/cmd/amq@vX.Y.Z`), never touching
the operator's `/Users/omri.a/.local/bin/amq` (confirmed unchanged at
`0.60.5` throughout). Sections 3, 4, 5, and 6 are additionally measured
in-process, calling `launchapi.Prepare`/`launchapi.Validate`/
`launchapi.Compatibility()` directly from two standalone Go programs pinned
to the v0.70.0 and v0.73.0 modules respectively, since the CLI binary and
the in-process package path can diverge and only the latter is what
amq-squad actually calls.

## 1. Real-AMQ lanes locally

`AMQ_SQUAD_REAL_AMQ` pointed at each version's temp-GOBIN binary.

```
$ AMQ_SQUAD_REAL_AMQ=/tmp/amq-verdict-bins/v0.71.0/amq AMQ_NO_UPDATE_CHECK=1 \
    go test ./internal/cli -run '^TestRealAMQCompatibility$' -count=1 -v
--- PASS: TestRealAMQCompatibility (16.39s)  [all 13 subtests PASS]

$ AMQ_SQUAD_REAL_AMQ=/tmp/amq-verdict-bins/v0.72.0/amq AMQ_NO_UPDATE_CHECK=1 \
    go test ./internal/cli -run '^TestRealAMQCompatibility$' -count=1 -v
--- FAIL: TestRealAMQCompatibility/production_cleanup_recovers_owner-bound_managed_wake
    (one run only; reran 3 more times, all 3 PASS -- pre-existing timing
    flake, not a v0.72.0 regression; all other subtests PASS every run)

$ AMQ_SQUAD_REAL_AMQ=/tmp/amq-verdict-bins/v0.73.0/amq AMQ_NO_UPDATE_CHECK=1 \
    go test ./internal/cli -run '^TestRealAMQCompatibility$' -count=1 -v
--- PASS: TestRealAMQCompatibility (16.32s)  [all 13 subtests PASS]

$ AMQ_SQUAD_REAL_AMQ=/tmp/amq-verdict-bins/{v0.71.0,v0.72.0,v0.73.0}/amq \
    go test ./internal/cli -run '^TestRealAMQWakeCompatibility$' -count=1
ok (all three versions, ~29-30s each)

$ AMQ_SQUAD_REAL_AMQ=/tmp/amq-verdict-bins/{v0.71.0,v0.72.0,v0.73.0}/amq \
    go test ./internal/launchintent ./internal/cli \
    -run '^(TestCompiledIntentAcceptedByReleasedAMQ|TestLaunchapiBackendDualRunParity)$' -count=1 -v
--- PASS (all three versions, both tests)
```

**Verdict: adopt.** All three versions pass the full real-AMQ lane suite.
The one v0.72.0 failure did not reproduce on 3 reruns; isolated in a
single-test run it also passes cleanly. Not a version-specific regression.

## 2. v0.72.0 claim 1: project-root authority

Reproduced the gh#734 nested-worktree bug against real binaries: a parent
dir with its own `.agent-mail`/`.amqrc`, a nested `task-worktree` subdir
with no `.amqrc` of its own (identity env stripped with
`env -u AM_ROOT -u AM_ME -u AM_BASE_ROOT -u AM_SESSION -u AM_ROOT_ID
-u AM_BASE_ROOT_ID` so the shell's own live-agent identity doesn't mask
discovery).

```
$ cd task-worktree && amq env --json   # v0.70.0, v0.71.0, v0.72.0, v0.73.0 -- IDENTICAL on all four
{
  "root": ".../PARENT/.agent-mail",         <- parent's, not the nested project's
  "root_source": "project_amqrc",
  ...
}
```

**The bug still reproduces identically on all four versions**, including
v0.73.0. `amq setup --project-root` is an opt-in, write-time fix, not a
change to the read-time discovery algorithm:

```
$ cd task-worktree && amq setup -project-root "$PWD" -agents claude \
    -default-session s -launcher-preference tmux -y -json
{"status": "configured", "preview": {"project_root": ".../task-worktree", ...}}

$ amq env --json   # AFTER setup wrote task-worktree/.amqrc
{"root": ".../task-worktree/.agent-mail", "root_source": "project_amqrc"}   <- now correctly scoped
```

Symlinked root refused as documented:

```
$ ln -s task-worktree symlinked-worktree && cd symlinked-worktree
$ amq setup -project-root "$PWD" -agents claude -default-session s2 -launcher-preference tmux -preview -json
error (exit 2): --project-root ".../symlinked-worktree" is a symlink; pass the real directory
```

**Verdict: our fail-closed `base_root` seam is NOT redundant, remains
necessary defense in depth.** `amq setup --project-root` fixes the bug only
when a caller explicitly invokes it before first use. amq-squad's own
worktree creation (`git worktree add`) never calls `amq setup` at all, so
every nested task worktree hits the still-live discovery bug unless our own
seam (`adoptionseam.Prepare`'s `ErrEmptyBaseRoot` guard, never calling `amq
env`/CLI discovery in the first place) refuses to let an implicit/discovered
root through. This matches the lead's expectation exactly.

## 3. v0.72.0 claim 2: `--allowedTools` grammar

Measured in-process (`launchapi.LaunchIntentV1.Validate()`) against both the
v0.70.0 and v0.73.0 modules, one claude seat per row:

| Form | v0.70.0 | v0.73.0 |
|---|---|---|
| `--allowedTools=Bash(gh pr create:*)` (equals-joined) | REJECT (not allowed by adapter grammar) | REJECT (not allowed by adapter grammar) |
| `--allowedTools` `Bash(gh pr create:*)` (two-token) | REJECT (invalid value) | **ACCEPT** |
| `--allowedTools` `Bash(gh pr view:*,gh pr create:*),Read` | REJECT | **ACCEPT** |
| `--allowedTools` `Bash:*` | REJECT | REJECT (name regex fails without parens) |
| `--allowedTools` `--dangerously-skip-permissions` | **ACCEPT** (old grammar didn't check value content) | REJECT (any value starting with `-` now rejected) |
| `--allowedTools` `Bash(--verbose)` | REJECT | REJECT (spec starting with `-` rejected) |
| `--allowedTools` `Bash` (bare, no scope) | ACCEPT | ACCEPT |
| `--allowedTools` `<513-byte value>` | n/a (old cap was 128 per code comment) | REJECT (over 512-byte cap) |
| `--allowedTools` `<512-byte value, under cap>` | n/a | ACCEPT |

**The equals-joined single-token form our legacy composer emits
(`internal/cli/agent_defaults.go`'s `claudePreauthChildArgs`) is REJECTED on
every version tested, v0.70.0 through v0.73.0.** `argumentRuleFor` in
`internal/launch/adapter.go` only special-cases `--name=` for inline/equals
parsing; nothing else, on any version. **The exact form the new-path
compiler must emit is the two-token form: `--allowedTools` as one argv
entry, the scoped-pattern value as the next entry.**

Reinterpretation-as-a-flag safety, requested by lead: the legacy
equals-joined form existed specifically so a value could never be
reinterpreted as a flag if validation was bypassed. On v0.73.0 that risk is
now closed a different way, at the value-grammar level: `--allowedTools`
followed immediately by a flag-looking token (`--dangerously-skip-permissions`)
is REJECTED outright, because `validClaudeAllowedTools` rejects any entry
starting with `-` (`strings.HasPrefix(part, "-")`) and any scoped spec
starting with `-` (`specBody[0] == '-'`). So the two-token form is now safe
on its own merits: the grammar itself refuses a flag-looking value,
independent of argv-token joining. t10 does not need an equals-joining
workaround to preserve that guarantee, only the value grammar's own reject.

## 4. v0.72.0 claim 3: `-c approvals_reviewer`

Same in-process method, one codex seat per row:

| Value | v0.70.0 | v0.73.0 |
|---|---|---|
| `approvals_reviewer="auto_review"` (quoted, our legacy literal) | REJECT (key/value not recognized at all) | **ACCEPT** |
| `approvals_reviewer=auto_review` (bare) | REJECT | **ACCEPT** |
| `approvals_reviewer="user"` | REJECT | **ACCEPT** |
| `approvals_reviewer="guardian_subagent"` | REJECT | **ACCEPT** |
| `approvals_reviewer="not_a_real_value"` | REJECT | REJECT (unknown value) |

**Our legacy literal `approvals_reviewer="auto_review"` (quoted) is
confirmed accepted on v0.73.0**, both quoted and bare spellings work for all
three allowed values.

Exact codex capability entry for t10's presence gate (from
`PrepareResultV1.Preview.Capabilities[]` where `provider == "codex"`,
`.config_overrides[]`):

```json
{
  "key": "approvals_reviewer",
  "allowed_values": ["user", "auto_review", "guardian_subagent"]
}
```

t10 gates on the presence of an entry with `key == "approvals_reviewer"` in
that provider's `config_overrides` slice (absent entirely on v0.70.0's
capability output; present on v0.73.0's).

## 5. v0.73.0 named seats

**No `launchapi` type or field changed.** Confirmed independently:
`launchapi.LaunchIntentV1`/`ParticipantV1` are byte-identical between
v0.70.0 and v0.73.0 (matches fullstack's md5-of-every-file finding for the
whole `launchapi` package). Named seats reach the system purely as an
accepted argv value, not a schema change:

- Claude gains `-n`/`--name` in its `argRules` (v0.73.0 only; absent on
  v0.70.0). Value grammar: `validSessionLabel` splits on `/`, requires at
  most 2 parts, each matching the canonical session pattern and not
  starting with `-`. So the emitted form is `-n SESSION/HANDLE` or
  `--name SESSION/HANDLE`, two-token (no equals-joined special-case exists
  for `-n`, only `--name=` is inline-special-cased, matching the pattern in
  section 3).
- `supportsManagedPlanNaming(provider)` gates it to `provider ==
  ClaudeProvider` only. Codex's `argRules` on v0.73.0 has no `-n`/`--name`
  entry at all.
- No schema-version gate anywhere: since `LaunchIntentV1`/`ParticipantV1`
  are unchanged, there is nothing to version-gate. V1 plans are
  byte-identical whether or not a seat carries `-n`/`--name`; it is just
  another accepted `Args` entry, validated the same way `--allowedTools` is.

## 6. `launchapi.Compatibility()` on v0.73.0

```json
{
  "contract_semver": "0.61.1",
  "intent_versions": [1],
  "result_versions": [1],
  "features": [
    "launch_intent_v1", "prepare_apply_v1", "lifecycle_v1",
    "managed_tmux_v1", "plan_only_commands_v1", "initial_input",
    "placement", "base_root", "on_live", "caller_context",
    "executable_identity", "wrapper"
  ]
}
```

**Unchanged from v0.70.0, byte-for-byte** (confirmed both by re-running the
same probe against the v0.70.0 module, and against fullstack's independent
md5 diff of the whole `launchapi` package). None of the three #648 asks nor
named seats added a `launchapi.Feature*` constant or a raw feature string:
they landed in the module's *internal* grammar tables
(`internal/launch/adapter_*.go`) and surface only at Prepare-call runtime,
never in `Compatibility()`/`Negotiate()`'s static, ahead-of-call view.

**So the adoption floor is not (and cannot be) a `Negotiate`-feature-list
change.** It is enforced two ways, neither of which is `Negotiate`:

1. The go.mod pin itself, since `launchapi` and the internal grammar tables
   it calls into all run inside this binary as one compiled unit.
2. A runtime check on `PrepareResultV1.Preview.Capabilities[].GrammarVersion`
   per provider, per call.

**GrammarVersion is a reliable, deterministic per-call gate, not
provider-probed.** Confirmed by calling `launchapi.Prepare` with no real
`claude`/`codex` binary installed on the measuring machine at all:
`PlannedOutcome` correctly came back `"unsupported"` for both seats (no
executable to actually plan a command for), but `Preview.Capabilities[]`
was still fully populated: claude `grammar_version` 1 (v0.70.0) -> 2
(v0.73.0), `allowed_argument_forms` gains `-n`/`--name`; codex stays
`grammar_version` 1 both times but its `config_overrides` gains the
`approvals_reviewer` entry only on v0.73.0. This data comes from the static
grammar tables compiled into the module, not from probing an installed
provider binary, so it is safe to gate on before any real agent binary is
even resolved.

**`Prepare` is read-only and deterministic, safe to call twice in one
request (t10/t11's two-phase probe design).** Verified directly: two
identical `Prepare` calls against the same request, back to back, in the
same process. The project root's file tree was byte-identical before the
first call, after the first call, and after the second call (zero writes,
confirmed by hashing every file under the root each time, not just checking
`Outcome`). `Preview` (including `Capabilities`) serialized identically
between the two calls, and `SubjectDigest` matched exactly
(`sha256:1da22cf3ce813676b2895281ed57700d3134e5917dadaf80c06be2294da17ff5`
both times). A probe `Prepare` call to read `Preview.Capabilities` and a
second `Prepare` call on the recompiled phase-2 request are therefore safe:
neither call mutates anything, and the same input always yields the same
output, so the probe cannot itself introduce drift between what it observed
and what phase 2 sends to `Apply`.

## Adopt/defer summary

| Item | Verdict |
|---|---|
| 1. Real-AMQ lanes on v0.71.0/v0.72.0/v0.73.0 | Adopt: all pass |
| 2. Project-root authority | Adopt as evidence; our `base_root` seam stays as defense in depth, not redundant |
| 3. `--allowedTools` scoped grammar | Adopt: t10 must emit the two-token form `--allowedTools` `<scoped-pattern-value>` |
| 4. `approvals_reviewer` allowlist | Adopt: t10 can restore the legacy literal `approvals_reviewer="auto_review"` on the new path, gated on the codex `config_overrides` presence check above |
| 5. Named seats | Adopt as informational for t11: no schema change needed, claude-only `-n`/`--name SESSION/HANDLE`, two-token |
| 6. `Compatibility()`/adoption floor mechanism | Adopt: floor enforcement is go.mod pin + runtime `GrammarVersion` check (t9/t10), not a `Negotiate` feature-list change |

## Facts posted to task/t8 for t10/t11 ahead of this doc

- Exact accepted `--allowedTools` form: two-token only, `--allowedTools`
  `<value>`, never equals-joined, on any version measured.
- `GrammarVersion` is deterministic and available without a real provider
  binary present.

Both also recorded as comments on gh#747/gh#748 per lead's request.
