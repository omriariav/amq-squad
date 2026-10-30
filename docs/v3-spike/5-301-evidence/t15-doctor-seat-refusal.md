# 3.0.1-t15: doctor refuses --probe and --write-registry inside a seat

Brief: 3.0.1-t15 (session v31, task t5 finding). Branch `v3-301-t15` from
`origin/v3` at a1a67151.

## The defect

A seat ran `doctor --probe` from a binary built from the branch it edits.
The probe writes the owner registry (`~/.amq-squad/registry.yaml`), so the
seat rewrote owner state (R54). Reviewer ruling: fail closed on the seat
environment marker, not a read-only mode.

## The marker

No shared Go "am I in a seat" function existed. The hooks' own test is
`AM_ME`: the hook adapter `plugins/claude/hooks/amq-squad-hook.sh` exits
before anything else when `AM_ME` is unset or empty ("outside a seat"), and
the hook verbs take the seat handle from `AM_ME` (`hookFlags.identity` in
`internal/cli/hook_identity.go`). The new `seatEnvMarker` lives beside that
identity code in `internal/cli/hook_identity.go`. It returns the first of
`AM_ME`, `AMQ_SQUAD_SEAT`, `AMQ_SQUAD_ROLE_PATH` that is set and non-blank.
The last two are seat-exec's overlay (`internal/seatexec/seatexec_unix.go`),
so a seat whose `AM_ME` was stripped still counts. `AMQ_SQUAD_LEAD` alone is
the lead and is not a marker; a pi lead carries `AM_ME=lead` and so refuses.
The shell adapter cannot call Go, so it keeps its own `AM_ME` test; doctor
calls `seatEnvMarker`.

## The change

- `runDoctor` calls `refuseDoctorWriteInSeat(os.Getenv, writeRegistry, probe)`
  right after flag validation. That is before the working directory, the
  embedded runtime pin, the grants read, the registry read and any provider
  call. With a marker set and `--probe` or `--write-registry`, it returns
  `doctor: seat_owner_state_refused: <MARKER> is set, ...` and names the
  owner-run path. The process exits non-zero.
- Plain `doctor` (no write flag) still runs inside a seat.
- `doctorExecute` is a seam for `executeDoctor` so the rows can swap in a
  fake host. A reverted guard then writes only the temp registry and never
  probes the machine.

## Rows (internal/cli/doctor_seat_test.go)

Every row drives `runDoctor` (the real flag path) with `HOME=t.TempDir()`,
all seat markers and `AM_ROOT` cleared first, and the embedded runtime
stubbed.

| Row | What it holds |
|---|---|
| TestDoctorSeatRefusesProbeAndWriteRegistry | 4 flag sets x 3 markers x registry absent/present: refusal with the reason, the marker name and the owner-run command; doctor never reaches execute; registry bytes identical, absent stays absent |
| TestDoctorSeatPlainDoctorStillRuns | `AM_ME` set, no flags: doctor runs, writes no registry |
| TestDoctorWithoutSeatMarkerWritesRegistry | no marker (blank `AM_ME`, with and without `AMQ_SQUAD_LEAD`): `--write-registry` runs and writes the temp registry |
| TestSeatEnvMarker | each marker detected; blank and lead-only are not seats |

The existing doctor rows (doctor_host_test.go, doctor_test.go) drive
`executeDoctor` with injected seams and are unchanged and green.

## Revert check

With the guard call in `runDoctor` replaced by a no-op, the refusal row
fails: all 24 subtests of TestDoctorSeatRefusesProbeAndWriteRegistry fail
(doctor reaches execute and, for the write flags, the temp registry changes).
Restoring the guard turns them green.

## Second commit: --fix joins the refusal

The lead widened the scope after review. `doctor --fix` removes pi runtimes
under `~/.amq-squad/runtime` (a rejected current runtime, and runtimes
beyond the last two), so it is owner state too. `refuseDoctorWriteInSeat`
now takes the fix flag and refuses `--fix` inside a seat with the same
reason, `seat_owner_state_refused`, naming `~/.amq-squad/runtime` and the
owner-run `amq-squad doctor --fix`. The pi lead stays refused (it carries
`AM_ME=lead`); the lead records that decision for the reviewer.

| Row | What it holds |
|---|---|
| TestDoctorSeatRefusesFix | per marker: a temp HOME with an unpacked current runtime and two older ones (`--fix` would prune the oldest); `--fix` refuses with the reason and the owner-run command, never reaches execute, and the runtime tree (path, mode, size, mtime) is unchanged |

Revert check: with the guard passing `false` for fix, all three subtests
fail. With the error and reached assertions also removed, the tree
comparison alone fails ("runtime dir changed inside a seat"): the reverted
run pruned `pi-old-1`.

The wizard reference `plugins/skills-src/wizard/references/readiness.md`
(and its two mirrors, byte-identical to the source) now says the owner runs
`--probe`, `--write-registry` and `--fix`, and seats never do.

## Third commit: plain doctor skips the release-drift check in a seat

Codex pass blocker. Plain `doctor` inside a seat still installed the
release-drift hook. With an absolute `AM_ROOT` and owner grants carrying a
merge entry it called GitHub, and `mergetrain.Drift` wrote baselines,
coverage bindings and owner alerts under `<AM_ROOT>/extensions/amq-squad/alerts`.
That is a seat reaching the network and writing squad state the owner
session owns.

- `doctorReleaseDriftFor` (doctor_release_drift.go) replaces the direct
  `doctorReleaseDriftHook` call in `runDoctor`. Outside a seat it is the old
  hook. Inside a seat it reads no grants and returns one row, `release drift`
  (ok): "skipped: <MARKER> is set, so this process is a seat; ... so the
  owner runs it".
- The seat clear checks that share the hook stay: they only read the gate
  store (`gatestore.Open` creates nothing).
- `doctorReleaseLister` is a seam for gh, so the rows count calls and never
  reach GitHub, even reverted.
- The fake execute now runs the drift hook `runDoctor` built and records
  the rows; before, it discarded `ReleaseDrift`.

| Row | What it holds |
|---|---|
| TestDoctorSeatSkipsReleaseDrift | per marker: temp HOME with a merge grant, absolute temp `AM_ROOT`; plain doctor runs, the only drift row is the seat skip row naming the marker and the owner, gh is called 0 times, nothing exists under `<AM_ROOT>/extensions` |
| TestDoctorWithoutSeatRunsReleaseDrift | same fixture, no marker: gh is called, the `release drift o/r` row is present, the baseline is written (the seat row is not vacuous) |

Revert check (runDoctor back on `doctorReleaseDriftHook`): the seat row
fails on each assertion alone. The drift-row check sees
`release drift o/r`; the gh-call check sees 2 calls; the extensions check
finds `<AM_ROOT>/extensions` written.

### Plain doctor in a seat: every write or network path

One walk of `internal/cli/doctor.go`, `internal/cli/doctor_host.go` and
`internal/cli/doctor_release_drift.go`, following each call into its
package. Lines are at this commit. "Plain"
means no `--probe`, `--write-registry` or `--fix`. Those three are refused
before any of this runs (doctor.go:218).

| Path | Where | In a seat |
|---|---|---|
| Release-drift hook selection | doctor.go:238 into doctor_release_drift.go:76 (`doctorReleaseDriftFor`: in a seat it returns the skip row, :82; outside, the hook, :79) | skipped in a seat (one ok row naming the marker and the owner) |
| Owner grants read for the drift hook | doctor_release_drift.go:59 (`grants.Load`) | skipped in a seat (never called) |
| gh tag and release listing (network) | doctor_release_drift.go:63 via `doctorReleaseLister`, :68 (`mergetrain.GH`) | skipped in a seat |
| `mergetrain.Drift`: writes baselines, coverage bindings and owner alerts under `<AM_ROOT>/extensions/amq-squad/alerts` | doctor_release_drift.go:38 (gate store opened at :25) | skipped in a seat |
| Registry write `~/.amq-squad/registry.yaml` | doctor_host.go:228 (`registry.Write`, only under `--write-registry`/`--probe`) | skipped in a seat (refused, doctor.go:218) |
| Provider probes: Codex model-id turns, R12 local probe, TTFT | doctor_host.go:220 (`runProbes`, only under `--probe`) | skipped in a seat (refused) |
| Runtime removal and prune under `~/.amq-squad/runtime` | doctor_pi.go:77-78 and :113-114 (only under `--fix`) | skipped in a seat (refused) |
| Registry reads | doctor_host.go:215, :476, :644 | read-only |
| Runtime pin sync | doctor.go:237 (`syncRegistryRuntimePin`: in-memory pin from the embedded manifest) | read-only |
| Runtime re-hash `~/.amq-squad/runtime/<name>` | doctor_host.go:99 into doctor_pi.go (`piruntime.Open`, descriptor-anchored reads); versions row doctor.go:372 (`piruntime.List`) | read-only |
| `amq env --json` | doctor.go:356 into amq_env.go:54 (update check off: amq_env.go:58) | read-only (local subprocess) |
| `amq doctor --ops --json` | doctor.go:360, :390 into amq.go:32 (update check off: amq.go:36). amq's ops checks write only under `--fix-wake-locks`/`--fix-mailboxes`, which are not passed (amq v0.116.2 internal/cli/doctor_ops.go) | read-only (local subprocess) |
| PATH `amq-squad version` | doctor.go:361 into :113 (prints the version) | read-only (local subprocess) |
| `amq`, `amq-remote`, `amq-owner-sign --version` | doctor.go:363 into doctor_amq_floor.go:46 (update check off: :48) | read-only (local subprocess) |
| Tool detection: `<tool> --version` for claude, codex, grok, cursor-agent, ollama, amq, amq-remote, gh; `tmux -V`; 2 s timeout | doctor_host.go:96 into registry/detect.go:84, :118 | read-only (local subprocess; the third-party tool's own behaviour on `--version` is outside amq-squad's code) |
| Login state per tool | registry/detect.go `detectLogin` (file presence only, no login command) | read-only |
| Host facts: `sysctl`, `nvidia-smi` | registry/detect.go:348, :351, :365 | read-only (local subprocess) |
| Ollama model list over HTTP | registry/detect.go:414-416 (refused unless the host is loopback) | read-only (loopback only, no internet) |
| `pmset -g` | doctor_host.go:329 | read-only (local subprocess) |
| `~/Library/LaunchAgents` listing | doctor_host.go:341 | read-only |
| `amq-remote service status --json` | doctor_host.go:351 into buzz/service.go:44 | read-only (local subprocess) |
| Buzz share grant signatures | doctor_host.go:355 (`buzzGrantCheck`: binding and share files, local signature check) | read-only |
| Codex trust and notify line in the Codex config | doctor_host.go:118 | read-only |
| Shell profile exports (names only), owner agent config against the preflight copy | doctor_host.go:119-120 | read-only |
| Grants file and its mode, routing table, worktree allow rules | doctor_host.go:123-125 | read-only |
| Push target and gh pin: grants read, `pin.Verify` re-hash, `command -v gh` and a sha256 of it | doctor_host.go:126 into :476-487 | read-only (gh is hashed, never run) |
| Claude seat token status, claude pin | doctor_host.go:127-128 (token never printed; the installed claude is resolved and hashed, never run) | read-only |
| Status contract self-check, squad.yaml golden | doctor_host.go:129-130 (in-memory) | read-only |
| `--usage-scan` session-file scan | doctor_host.go:132 (only with the flag) | read-only |
| Seat clear checks: clear mode from grants and the binding, open clear gates | doctor.go:238 into seat_clear_gate.go:293, :306 (`gatestore.Open` creates nothing; `IDs`, `Load`, `State`, `QuestionPublished` read) | read-only |
| Output | doctor.go:300, :307 (stdout) | read-only |

Nothing else in plain doctor writes or reaches past the host. Only the
release-drift hook was neither read-only nor already refused, and it is
the one this commit skips in a seat.

### What the marker fences, and what it does not

The seat environment marker is a fence against a brief's or an agent's
mistake: a seat told to run `doctor --probe` (the t5 incident), or one
that reaches for it unprompted, is refused with the reason. It is not a
fence against a seat that unsets its own environment: `env -u AM_ME -u
AMQ_SQUAD_SEAT -u AMQ_SQUAD_ROLE_PATH amq-squad doctor --probe` passes it.
While seats run with the owner's HOME, `~/.amq-squad` (the registry, the
runtimes, the grants) is reachable by any tool a seat has, doctor or not.
The real fence is a seat-owned `AMQ_SQUAD_HOME` with a read-only registry
mirror, so a seat never holds a path to the owner's state. That is a 3.0.x
item (docs/v3-followups.md), not 3.0.1.

## Docs

- README.md, the registry step of the doctor section: seats never probe.
  README.html regenerated with `make readme-html` (CI `readme-html-check`).
- plugins/skills-src/cli/SKILL.md (the doctor skill names `--probe`) and its
  two generated mirrors under plugins/claude and plugins/codex, edited by
  hand because PyYAML is not installed here; the mirror bodies match the
  source. The sentence avoids an inline `amq-squad doctor --probe` span on
  purpose: the skill-invocation gate executes inline spans with flags, and a
  real probe reaches the providers.
- docs/skills.md does not name `--probe`, so it is unchanged.
- The wizard reference readiness.md: see the second commit above.

## Commands

```
go build ./... && go vet ./internal/cli && go test -count=1 ./internal/cli
make readme-html-check
python3 scripts/check-skill-commands.py <temp build>
python3 scripts/check-current-skill-routing.py
```

No doctor ran against the real HOME. No test reaches the network.
