# 3.0.2 t19 fix: the team-mode lead takes the squad's identity, with the reviewer's rule

Defect (first squad run on 3.0.1, session v31): `amq-squad lead --team
--scheduler-seats FILE` started the scheduler on the seats file's session,
but the pi lead itself took its identity from AM_ME and AM_ROOT only and,
without them, ran in solo mode (AM_SESSION `solo`, a root under
~/.amq-squad/solo/). The manager's task tools read an empty solo store while
the scheduler leased from the squad's store.

Day-2 workaround: the owner started the lead in tmux with the squad's
identity exported (AM_ME, AM_ROOT, AM_BASE_ROOT, AM_SESSION set to the
seats file's session by hand), so the lead took the AMQ-launch branch.

## The seat's commit

The reviewer seat built task t19 as e9e6c53b77e819a6a02b7ec9e399e6fbecb65e94
(PR #816, parent a1a67151): `resolveTeamLeadIdentity` in
internal/cli/lead_scheduler.go reads the seats file and builds a
`piruntime.TeamIdentity` (internal/piruntime/team_identity.go: AM_ME the
lead handle, AM_ROOT `<base_root>/<session>`, AM_BASE_ROOT, AM_SESSION);
`runLead` gains `--lead HANDLE`, starts the scheduler with that handle and
passes the identity to pi as an overlay. It is cherry-picked here with the
seat's authorship. It conflicted with 3.0.1 t16 in both files; the
resolution keeps t16's `--trust` flag and its validation
(`--trust needs --team --scheduler-seats`), `ExitSchedulerRefused`, the
`--trust` pass-through to `startLeadScheduler`, and the lead proof from the
scheduler's parent process (`startedByLeadTeam`, unchanged), and takes the
seat's identity derivation, `--lead` flag and overlay. The usage text
carries both.

## The corrections (reviewer rulings 2026-10-30, Codex on e9e6c53b)

1. **The rule.** When the environment carries an identity (AM_ME and
   AM_ROOT, the proved identity under an AMQ launch), the seats file's
   root, base root and session must agree with it (paths compared cleaned;
   an unset AM_BASE_ROOT or AM_SESSION is filled, not compared). A
   disagreement is a refusal at start, before the scheduler starts, naming
   both values:
   `the environment's AM_ROOT="..." disagrees with the seats file FILE ("...")`.
   Without an environment identity the seats file fills it. The seat's
   overlay had silently replaced an inherited AM_ROOT, AM_BASE_ROOT and
   AM_SESSION. The `--lead` versus AM_ME refusal stays.
2. **AMQ_SQUAD_PROJECT** is the seats file's project_root in team mode
   with a seats file, with or without an environment identity. The seat's
   change took it from `prepareSoloLead`'s current directory, so from
   another checkout the repo tools read the wrong repository.
3. **The scheduler's handle** is the lead's handle (`teamIdentity.Handle`);
   the lead proof stays the parent process (R4 as amended, plan 3.4).
4. **Rows through runLead.** `startLeadScheduler` became a variable so the
   rows drive `runLead` end to end without running `scheduler run`.

## Rows

| Row | File | What it proves |
|---|---|---|
| TestLeadTeamTakesTheSquadIdentityFromTheSeatsFile | internal/cli/pi_lead_test.go | No environment identity, cwd another checkout, `--lead cto --trust`: the scheduler starts once with lead `cto`, the seats path and trust; pi gets AM_ME `cto`, AM_ROOT `<proj>/.agent-mail/v31`, AM_BASE_ROOT, AM_SESSION `v31`, AMQ_SQUAD_PROJECT `<proj>`, and the team role. |
| TestLeadTeamRefusesAnEnvironmentIdentityThatDisagrees | internal/cli/pi_lead_test.go | AM_ME `cto` and AM_ROOT set; AM_ROOT, AM_BASE_ROOT or AM_SESSION disagreeing each refuses naming the environment's value and the seats file's value; `--lead rev` against AM_ME `cto` refuses. No scheduler start and no pi exec after a refusal. |
| TestLeadTeamKeepsAnAgreeingEnvironmentIdentity | internal/cli/pi_lead_test.go | AM_ME `cto` and an agreeing AM_ROOT (trailing separator, compared cleaned): the scheduler starts with `cto`, pi keeps the identity, AM_BASE_ROOT and AM_SESSION are filled from the seats file, AMQ_SQUAD_PROJECT is project_root. |
| TestLeadTeamTaskToolsSeeTheSquadStore | internal/cli/pi_lead_test.go | The manager path: a temp store at the seats file's session root with one task; after runLead, `task list --json` (amq_task_list's argv) and `task show ID --json`, run with the pi lead's AM_* environment, return that task. |

The seat's internal/piruntime/team_identity_test.go rows stay (the
helper's shape and refusals). No row reaches the network, the real HOME,
pi, tmux or AMQ: HOME is a test directory, pi is the `piRun` seam, the
scheduler is the `startLeadScheduler` seam.

## Revert checks

| Mutation | Rows that fail |
|---|---|
| The environment-identity agreement check removed (correction 1) | TestLeadTeamRefusesAnEnvironmentIdentityThatDisagrees (root, base root, session) |
| The AMQ_SQUAD_PROJECT line removed (correction 2) | TestLeadTeamTakesTheSquadIdentityFromTheSeatsFile (cwd's other-checkout), TestLeadTeamKeepsAnAgreeingEnvironmentIdentity (empty) |
| The identity overlay branch skipped (the seat's change) | TestLeadTeamTakesTheSquadIdentityFromTheSeatsFile, TestLeadTeamKeepsAnAgreeingEnvironmentIdentity, TestLeadTeamTaskToolsSeeTheSquadStore |
| The scheduler handle back to `firstOf(AM_ME, "lead")` (correction 3) | TestLeadTeamTakesTheSquadIdentityFromTheSeatsFile |

## Gates

```
go build ./... && go vet ./internal/cli ./internal/piruntime    ok
go list -deps ./internal/task | grep piruntime                   (empty)
go test -count=1 ./internal/cli ./internal/piruntime             ok, ok
make readme-html-check                                           in sync
```

README's lead --team bullet gains one sentence: the lead takes the seats
file's AMQ identity, and an AM_ME/AM_ROOT already in the environment must
match it or the lead refuses.
