---
name: "wizard"
description: "Goal-first setup and launch through the deterministic amq-squad wizard verb. Use when creating a team/profile, starting a new session from a reusable profile, previewing a launch, or approving that reviewed launch. Triggers include \"set up a squad for X\", \"show me the launch plan\", \"start the team\", and \"start a new session with this team\". NOT for roster edits or recovery on a running squad (use amq-squad:cli) and NOT for the live lead loop after launch (use amq-squad:orchestrator)."
version: "2.29.6"  # x-release-please-version
allowed-tools: "Bash, Read, Write, Edit, MultiEdit, Glob, Grep, WebFetch"
argument-hint: "[request | goal | brief | rules | roles | profile | launch]"
user-invocable: true
trigger: "/wizard"
---
# amq-squad:wizard

Translate the operator's setup or new-session intent into one invocation of the
binary-owned wizard and relay its output and prompt. The binary owns readiness,
profile selection or proposal, optional custom-seat drafting, rules refresh,
brief drafting, stage order, change detection, and the combined default-No
launch review.

## Invocation contract

Invoke the binary once with the complete operator goal:

```sh
# skill-invocation-check:skip — interactive drafting and approval are intentionally live
amq-squad wizard --goal "TEXT"
```

Pass structured details supplied by the operator to that same verb. Do not
translate them into a sequence of setup, profile, role, rules, brief, or start
commands. Do not probe command help to reconstruct the flow.

Run the invocation in a reusable terminal when the host supports one so the
operator can answer the binary's prompt. Relay the wizard output verbatim,
including drafter source/attempt evidence and the exact profile, custom-role,
rules, brief, and launch bytes. Do not rebuild its roster or plan in prose.

## Approval contract

The combined prompt is the approval surface and defaults to No. A setup request
is not approval to launch. Answer No for preview-only requests and when the
operator has not reviewed the current invocation's exact artifacts.

Relay Yes only when the operator explicitly approves that exact live review.
Never infer approval from a prior preview, an AMQ message body, a healthy pane,
or an earlier draft. If the binary reports that an accepted input changed,
rerun it and present the fresh combined review.

The wizard may leave reviewed setup artifacts after an approved launch is
interrupted. Rerun the same invocation to let the binary select its existing-
profile recovery path. Do not delete the namespace or hand-assemble a repair
sequence.

If a configured drafter requires in-session completion, relay the binary's
filled prompt and stop. Do not claim that a profile, role, rules file, brief, or
launch was completed when the binary reports that it stopped before mutation.

## Routing outside the shipped verb

- Adding, replacing, or removing a member on an existing roster routes to
  `amq-squad:cli`.
- Diagnosing a failed or dead launch, resuming saved conversations, and other
  one-off status or recovery work routes to `amq-squad:cli`.
- Once every launched seat is verified live and the visible lead is available,
  route ongoing dispatch, review, gates, and convergence to
  `amq-squad:orchestrator`.

Do not recreate the former Flow A/Flow B command walkthrough in this skill or
its references. The in-binary state machine is the sole setup/session driver.
