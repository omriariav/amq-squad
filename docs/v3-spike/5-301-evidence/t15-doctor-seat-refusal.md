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
- Not edited (outside the declared files): the wizard reference
  `plugins/skills-src/wizard/references/readiness.md` also names `--probe`.

## Commands

```
go build ./... && go vet ./internal/cli && go test -count=1 ./internal/cli
make readme-html-check
python3 scripts/check-skill-commands.py <temp build>
python3 scripts/check-current-skill-routing.py
```

No doctor ran against the real HOME. No test reaches the network.
