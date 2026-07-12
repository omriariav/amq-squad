# Wizard decision tree and resume path (#431 + #428)

Status: design for operator review. No implementation is included in this
change.

Revision 2 resolves the exact-head QA findings from PR #434 at `df47588`:
state precedence and restore guards are explicit, resume overrides are scoped to
actions the backend can honor, existing-profile freshness uses a full discovery
fingerprint at both mutation boundaries, and acceptance is table-driven.

## Goal

Make the wizard read like the decisions an operator is making, while keeping
its safety contract unchanged:

- scope is the first decision inside the wizard;
- project runs fork early between an existing profile and a new profile;
- the selected profile branch determines how the session is obtained;
- a stopped or partially stopped existing squad can be resumed through the
  same wizard;
- every screen explains the consequence of its choice in plain language;
- the final action is still a canonical preview followed by a separate,
  explicit, default-No launch confirmation.

The wizard remains a front end over existing commands. It does not become a
second launch or resume implementation.

## Scope

In scope:

- the full-screen TUI and accessible numbered adapter;
- project `run start`, project `resume`, and global/NOC start as wizard backend
  commands;
- read-only project, profile, session, launch-history, and liveness discovery;
- the answer model, decision order, screen labels, notes, review summary, and
  canonical command composition.

Out of scope:

- changing the non-interactive command behavior;
- terminal portability work planned for v2.21.0;
- implementing the configurable model/effort catalog from #432;
- changing stored profile contracts while running the wizard;
- automatically focusing, stopping, deleting, or replacing a live squad.

Issue #432 is an input to the picker boundary, not part of this design's
implementation scope. Model and effort screens must consume one injected,
merged catalog rather than owning hard-coded choices. The merged catalog,
custom values, and warn-not-reject validation remain #432's responsibility.

## Current behavior to replace

The current flow has five visible phases:

`Project -> Team -> Topology -> Goal -> Review`

It has four structural problems:

1. Scope is a line prompt before Bubble Tea starts. Global scope then leaves
   the TUI entirely and uses another sequence of line prompts.
2. Existing and new profiles share one "Team profile" picker, but the
   authoritative-vs-configurable consequence is only revealed on later
   screens.
3. The session screen is free text for every branch. A pinned existing profile
   therefore invites an invalid answer and rejects it after the fact.
4. "Topology" also contains layout, operator contract, notifications, and
   launcher-pane policy. "Review" omits goal and seed source.

The existing discovery model only gives each profile one inferred pinned
session and roster facts. It does not expose session liveness. The new flow must
reuse the same liveness verdict and per-member resume actions used by
`status --json` and `resume --json`; it must not invent a wizard-only process
check.

## Design principles

1. **The fork comes before configuration.** Choosing an existing profile means
   its roster, lead mode, operator contract, and notification policy are
   authoritative. Choosing a new profile means the wizard will ask for them.
2. **Session follows the fork.** Existing profiles show known sessions and
   their state. New profiles ask for one new session name, with the current git
   suggestion prefilled. A pinned existing profile never receives a free-text
   session field.
3. **Resume is an existing-profile outcome.** It is not a top-level wizard and
   not a sibling of the profile decision.
4. **Read-only discovery stays read-only.** Opening the wizard and moving
   between screens never creates a profile, mailbox root, launch record, pane,
   or task.
5. **One answer model, multiple composers.** UI state records operator intent;
   a backend-specific composer renders `run start`, `resume`, or
   `global start` arguments.
6. **No hidden policy mutation.** Existing profile contract screens are
   summaries with a single Continue action. They never pretend to offer an
   override that the command will ignore.
7. **Every screen stands alone.** The title asks the decision, option labels
   describe consequences, and the note explains what becomes fixed.

## Phase rails

The rail is scope-specific so it never advertises phases that the selected
branch cannot enter.

### Project rail

`Scope -> Profile & run -> Team -> Run controls -> Brief -> Review`

### Global/NOC rail

`Scope -> Agent -> Run controls -> Review`

### Phase naming changes

| Old phase | New phase | What the new phase actually decides |
| --- | --- | --- |
| Project | Scope | Project run vs global/NOC, then the owning project or neutral root. |
| Team | Profile & run | Existing-vs-new profile, session derivation, liveness, and start-vs-resume backend. |
| Team | Team | Fresh roster/lead choices or launch-only overrides of an authoritative roster. |
| Topology | Run controls | Placement, layout, operator contract, notifications, and launcher behavior that apply to the selected backend. |
| Goal | Brief | Goal and seed for a new run, or the preserved brief for a resume. |
| Review | Review | Every collected or preserved answer, the selected backend, and the exact preview/live command pair. |

"Run controls" deliberately replaces "Topology": topology is only one of the
decisions in that phase. "Brief" replaces "Goal": the phase contains both goal
text and seed source.

## Read-only discovery contract

After the project root is confirmed, discovery returns profiles and their
known sessions. Each session summary contains:

- profile name and whether it is `default` or named;
- authoritative roster, lead, lead mode, operator contract, notifications,
  stored model/effort values, and member count;
- session name and source: member pin, profile workstream, or launch history;
- brief path plus goal excerpt and seed source when readable;
- per-member resume action from the shared planner: `live`, `restore`,
  `launch fresh`, or `blocked`;
- matching launch-record count plus rollup counts for live, restore, launch-fresh,
  and blocked members;
- a session state derived from those actions.

The rollup states are mutually exclusive and exhaustive. Classification uses
this precedence; the first matching row wins:

| Precedence | State | Shared-planner and record facts | Wizard consequence |
| --- | --- | --- | --- |
| 1 | Blocked | The profile has no members; any member action is `blocked`; or profile/session/namespace resolution is ambiguous. | Show the blocker and its read-only diagnostic command; do not offer execution. |
| 2 | Running | Every configured member action is `live`. | No launch action is offered; go back or create a new profile. |
| 3 | Not started | No member is live, the matching launch-record count is zero, and every member action is `launch fresh`. | Existing authoritative profile can start its known/pinned run with `run start`. This wins over the planner's superficial all-`launch fresh` resemblance to resume. |
| 4 | Partly running, resumable | At least one member is `live`, at least one other member is `restore` or `launch fresh`, and none is blocked. | Offer Restore missing members; live members are skipped. This includes live-plus-fresh with zero matching launch records. |
| 5 | Stopped, resumable | No member is live, the matching launch-record count is greater than zero, at least one member is `restore` or `launch fresh`, and none is blocked. | Offer Resume. A stopped state therefore always has durable history to restore or reconcile. |

Any fact combination not covered by rows 1-5 is an internal discovery
inconsistency and is rendered as Blocked with diagnostic detail. The wizard
never guesses a sixth executable state.

The resume command includes `--restore-existing` if and only if the selected
session has at least one matching launch record. An all-fresh/no-record session
uses `run start`, not resume. A live-plus-fresh/no-record repair is Partly
running and uses `resume` **without** `--restore-existing`; live members are
skipped and missing members launch fresh. This makes the guard truthful instead
of emitting a command that must fail at `recordCount == 0`.

The wizard must call the shared planner once per selected session and retain the
result used for review.

### Discovery fingerprint and freshness gate

Every existing-profile discovery result carries a deterministic fingerprint of
the full decision input, not only the rolled-up state or member action labels.
The fingerprint covers:

- roster identity and order: role, handle, binary, cwd, member session, native
  args, stored model, and stored effort;
- configured lead, effective lead mode, operator contract, self-operator
  policy, and notification policy;
- selected session name and source (member pin, profile workstream, or matching
  history set);
- brief identity: resolved path, provenance/source metadata, and content digest
  of the full brief (not the truncated Review excerpt);
- namespace-conflict result and every fact used to produce it;
- matching launch-record identities/count and the complete per-member plan,
  including action, liveness verdict/signals, saved-launch identity, and
  blockers.

The wizard refreshes and compares this fingerprint twice for **every**
existing-profile branch: immediately before canonical preview and again
immediately before execution after the operator answers Yes. Any delta, even
when the rollup label remains the same, invalidates all downstream answers and
returns to Profile & run with:

`The selected profile or run changed while the wizard was open. Review the refreshed facts before continuing.`

The second refresh is required because profile policy, brief content, or launch
state can change after preview but before execution. A changed fingerprint is
never reduced to a warning and never auto-accepted.

## Full decision tree

```text
Scope
├─ Project run
│  └─ Project root
│     └─ Profile fork
│        ├─ Existing profile
│        │  └─ Known session (auto-selected when pinned/unique)
│        │     └─ Shared liveness plan
│        │        ├─ stopped or partly running
│        │        │  ├─ Resume existing squad
│        │        │  │  └─ member action summary
│        │        │  │     ├─ live: keep running, no override
│        │        │  │     ├─ restore: replay saved args, no override
│        │        │  │     └─ launch fresh: optional model override only
│        │        │  │        └─ resume placement/layout
│        │        │  │           └─ authoritative contract summary
│        │        │  │              └─ preserved brief
│        │        │  │                 └─ review -> fingerprint check -> resume preview
│        │        │  │                    └─ default-No exec -> fingerprint check -> execute
│        │        │  └─ Back and create a new profile
│        │        ├─ not started
│        │        │  ├─ Start the profile's known session
│        │        │  │  └─ launch-only team overrides
│        │        │  │     └─ run controls
│        │        │  │        └─ goal + seed
│        │        │  │           └─ review -> fingerprint check -> run-start preview
│        │        │  │              └─ default-No launch -> fingerprint check -> execute
│        │        │  └─ Back and create a new profile
│        │        ├─ running
│        │        │  ├─ Back to profile list
│        │        │  └─ Create a new profile for another run
│        │        └─ blocked
│        │           ├─ Show diagnostic command
│        │           └─ Back to profile list
│        └─ Create a new profile
│           └─ Profile name
│              └─ New session name
│                 └─ fresh roster, binaries, models, efforts, lead, lead mode
│                    └─ run controls
│                       └─ goal + seed
│                          └─ review -> run-start preview -> default-No launch
└─ Global / NOC orchestrator
   └─ Neutral root
      └─ agent binary -> model -> effort -> native args
         └─ window name
            └─ review -> global-start preview -> default-No launch
```

The "Create a new profile" option is always reachable from the profile fork.
An operator who chose an existing stopped squad but really wants unrelated new
work goes back to that branch; the wizard does not smuggle a new session into a
pinned profile.

## Screen contracts and copy

The tables below are normative copy intent. Minor punctuation can change during
implementation, but each screen must retain its question, consequence, and
plain-language note.

### Shared Scope screens

| Screen / question | Options | Determines | Note shown on screen |
| --- | --- | --- | --- |
| **What do you want to run?** | `Project squad` / `Global / NOC orchestrator` | Scope and downstream rail. | `Project runs use one repository's profiles and sessions. Global/NOC starts one coordinator and does not own project wake mailboxes.` |
| **Which project owns this squad?** | Text field, nearest git root prefilled. | Project root and discovery namespace. | `Choose the repository root that owns .amq-squad. The nearest git root is suggested; no network access is used.` |
| **Where should the global orchestrator run?** | Text field, current neutral-root default. | Global root. | `This is a neutral control root, not a project profile or session.` |

`--scope` and other prefills select a default row or field value; they do not
move the scope decision outside the wizard.

### Profile & run screens

| Screen / question | Options | Determines | Note shown on screen |
| --- | --- | --- | --- |
| **Use an existing team setup or create a new one?** | One row per existing profile, then `Create a new profile`. Existing row format: `<profile> · <N> members · <session/state summary> · roster and contract stay authoritative`. | Existing-vs-new branch. | `An existing profile keeps its roster, lead, and operator contract. A new profile lets you choose them.` |
| **Which existing run do you want?** | Known sessions for the profile, each with `running`, `stopped`, `partly running`, `not started`, or `blocked` counts. Skip this screen when the selected profile has one pinned/known session. | Session from profile facts; never arbitrary free text. | `These sessions belong to the selected profile. To type a new session name, go back and create a new profile.` |
| **What should happen to <profile>/<session>?** (stopped) | `Resume this squad · <N> stopped members` / `Back and create a new profile`. | Resume backend or return to new-profile branch. | `Resume restores saved conversations when available and starts a member fresh only when no restorable conversation exists.` |
| **What should happen to <profile>/<session>?** (partly running) | `Restore <N> missing members · keep <M> live members` / `Back and create a new profile`. | Resume backend; existing live members are skipped. | `Resume will not replace live members. The canonical preview shows each member action before anything opens.` |
| **Start this profile's first run?** (not started) | `Start <session> with this authoritative profile` / `Back and create a new profile`. | `run start` backend with existing profile. | `The stored roster and contract stay unchanged. This creates the selected session only after final approval.` |
| **This squad is already running** | `Back to profiles` / `Create a new profile for another run`. | No executable backend. | `<N>/<N> members are live. The wizard will not duplicate or replace them.` |
| **This run needs attention before it can start** | `Back to profiles`; read-only diagnostic command is copyable. | No executable backend. | `<plain-language blocker>. Run the shown status or resume preview command to inspect it; the wizard will not force through it.` |
| **Name the new profile** | Text field, `squad-<suggested-session>` prefilled. | New profile namespace. | `This name identifies a reusable team setup. Nothing is written until the final command is approved.` |
| **Name the new session** | Text field, branch/project-derived suggestion prefilled. | New workstream. | `This is the new run's mailbox, brief, task, and launch-history namespace.` |

An existing profile with no pin and no known session is the one ambiguous case;
see the first open question. The recommended behavior is to treat it as an
unused authoritative profile and offer one suggested first session, without a
separate generic session picker.

### Team screens

| Screen / question | Options | Determines | Note shown on screen |
| --- | --- | --- | --- |
| **Which roles belong to the new team?** | Comma-separated roles, current defaults prefilled. | Fresh roster order. | `Choose the roles this profile will store. You will choose a binary, model, and effort for each role next.` |
| **Which binary runs <role>?** | Binary catalog choices. | Role binary. | `The binary determines which model and effort catalog is shown.` |
| **Which model should <role> use?** | Merged catalog values / `Automatic` / `Custom`. | Stored model for a new profile. | `Catalog choices are suggestions. Custom model names pass through to the selected binary.` |
| **How much effort should <role> use?** | Merged catalog values / `Automatic` / `Custom` when #432 lands. | Stored effort for a new profile. | `The catalog catches likely typos but does not decide what the underlying binary accepts.` |
| **Which role leads the team?** | Role list, `cto` preferred when present. | Lead role. | `The lead is the visible owner of dispatch, review, and operator handoff.` |
| **How should the lead work?** | `Builder` / `Planner`. | Lead mode. | `Builder may implement and delegate. Planner dispatches mutations to workers and reviews the result.` |
| **Change <role> for this launch?** (`run start`) | `Keep profile model and effort` / `Override for this launch only`. | Whether existing-profile start override screens are entered. | `The stored profile is unchanged: model=<value>, effort=<value>.` |
| **Model override for <role>** (`run start`) | `Keep stored value` / merged catalog values / `Custom`. | Per-role launch-only model assignment. | `This applies only to the run-start command previewed by this wizard.` |
| **Effort override for <role>** (`run start`) | `Keep stored value` / merged catalog values / `Custom` when #432 lands. | Per-role launch-only effort assignment. | `This applies only to the run-start command previewed by this wizard.` |
| **Saved launch for <role> will be restored** (`resume`, action=`restore`) | `Continue` only; show saved binary/model/effort/native args. | No override. | `Restored members replay their saved launch arguments. This wizard does not rewrite them.` |
| **<role> is already live** (`resume`, action=`live`) | `Continue` only; show current stored/runtime facts. | No override and no relaunch. | `Resume keeps this member running and does not change its launch arguments.` |
| **Model for fresh <role>** (`resume`, action=`launch fresh`) | `Keep profile model` / merged catalog values / `Custom`. | Per-role model assignment passed through resume's fresh-member `--model` support. | `This member has no restorable launch. The model choice applies only to its fresh launch.` |
| **Effort for fresh <role>** (`resume`, action=`launch fresh`) | No screen in v1. | Stored profile effort remains authoritative. | `Resume has no per-role effort override contract yet; the wizard will not offer a control it cannot apply.` |

The v1 resume contract is deliberately action-scoped. A `restore` action replays
its saved record unchanged, so the wizard shows saved model/effort/native args
read-only. A `live` action is also read-only. Only `launch fresh` may expose a
model override, because current resume `--model role=value` applies only to fresh
members. Resume exposes no effort override screen: current native-arg flags are
not a truthful per-role replacement. `run start` keeps its existing per-role
model and effort controls. Rewriting saved launch records for restored members
and adding per-role effort parity are explicit follow-up choices, not hidden v1
behavior.

### Run controls screens

| Screen / question | Options | Determines | Note shown on screen |
| --- | --- | --- | --- |
| **Where should agents appear?** | `One window per agent` / `Panes in this window` / `Detached squad`. | For start: visibility. For resume: `new-window`, `current-window`, or `new-session` target. | `The diagram shows where each agent will appear. The review screen shows the exact backend flag.` |
| **How should panes be arranged?** | Choices valid for the selected placement. | Start layout preset or resume layout. Skip when placement has no layout choice. | `Layout uses exact pane and window IDs; display names are labels only.` |
| **How will the operator interact with this team?** (new profile) | Capability-aware operator contract choices. | Stored operator contract. | `This chooses where questions and approvals appear. Unavailable choices stay visible and explain what is missing.` |
| **Confirm the stored operator contract** (existing profile) | `Continue with <mode>` only. | No mutation; acknowledges authoritative state. | `<mode> means <plain-language contract summary>. Change the profile with team operator set before running if this is wrong.` |
| **Allow self-operator merge gates?** | Existing explicit merge selection flow. | New-profile self-operator allowlist. | `No gate is preselected. Spawn, release, tag, publish, external send, and destructive filesystem remain human-only; a second verified actor executes an approved merge.` |
| **Send attention-only desktop notifications?** (new profile) | `No` / `Yes`. | Stored notification add-on. | `Notifications ask for attention. They never approve a gate or type into a pane.` |
| **Confirm stored notification policy** (existing profile) | `Continue · enabled=<bool>` only. | No mutation. | `This profile's notification policy is authoritative.` |
| **What should happen to this wizard pane?** (`run start`) | `Close after successful start` / `Keep open`, constrained by topology/external lead. | Launcher-pane policy. | `Close happens only after successful spawn, goal delivery, and final output. Detached or external-lead runs keep this pane.` |

For resume, the launcher-pane screen is omitted because `resume --exec` has no
launcher-pane contract. The placement note says: `This wizard pane stays open;
resume only opens the missing agent panes.` If implementation later adds
launcher policy to resume, it can use the same screen without changing the
tree.

### Brief screens

| Screen / question | Options | Determines | Note shown on screen |
| --- | --- | --- | --- |
| **What is this run trying to accomplish?** (`run start`) | Optional text field. | `--goal`. | `This text is delivered to the lead and written into the run's goal context. Leave blank only when the brief source already says enough.` |
| **Should the brief start from an existing source?** (`run start`) | Optional `file:`, `issue:`, or `gh:` reference. | `--seed-from`. | `Accepted forms: file:path · issue:393 · gh:owner/repo#393. The review keeps this reference verbatim.` |
| **Brief preserved for resume** | `Continue` only; shows brief path, goal excerpt, and seed source when available. | No goal or seed mutation. | `Resume keeps the existing session brief. It does not replace the goal or seed source.` |

### Global/NOC screens

| Screen / question | Options | Determines | Note shown on screen |
| --- | --- | --- | --- |
| **Which binary runs the global orchestrator?** | Binary catalog choices. | Global agent. | `This starts one coordinator, not a project squad.` |
| **Which model should it use?** | Merged catalog / `Automatic` / `Custom`. | Global model. | `Catalog choices are suggestions; custom names pass through.` |
| **How much effort should it use?** | Merged catalog / `Automatic` / `Custom` when #432 lands. | Global effort/native args. | `The selected effort is rendered for this binary in the final command.` |
| **Extra native arguments** | Optional text field for the selected binary. | Remaining native args. | `Do not repeat model or effort here; the wizard composes those from earlier answers.` |
| **Name the global window** | Text field, current default prefilled. | Global window name. | `This is only the visible control-window name; it is not a project session.` |

## Resume branch end to end

1. Operator chooses **Project squad**, confirms the project root, and selects an
   existing profile row.
2. The wizard derives the session from the profile's pin/known-session list. A
   unique pinned session is shown as selected context, not as a text input.
3. The shared resume planner returns member actions and liveness. The wizard
   applies the precedence table, displays exact record/action counts, and offers
   Resume only when no member is blocked. No-record/all-fresh selects Not
   started and `run start`, not resume.
4. The wizard renders controls by member action. `live` is kept unchanged;
   `restore` shows saved launch arguments read-only; `launch fresh` may offer a
   model override. Resume never offers an effort override in v1. Stored profile
   values and saved launch records remain unchanged.
5. The operator chooses placement/layout for missing members. Live members are
   explicitly listed as `keep live` and are not relaunched.
6. The wizard displays the stored operator/notification contract as
   authoritative and the existing brief as preserved.
7. Review shows project, profile, session, matching record count, the full
   per-member plan, action-scoped controls or read-only saved args,
   placement/layout, stored contract, brief path, goal excerpt, seed source,
   selected backend `resume`, and both command forms.
8. Enter refreshes and compares the complete discovery fingerprint. Any delta
   returns to Profile & run and clears downstream answers, even if member action
   labels did not change.
9. The canonical preview is plan-only. When matching records exist, it includes
   the fail-closed restore guard:

   ```sh
   amq-squad resume --project /repo --profile release --session issue-431 --restore-existing --target new-window
   ```

   A live-plus-fresh/no-record repair omits `--restore-existing`:

   ```sh
   amq-squad resume --project /repo --profile release --session issue-431 --target new-window
   ```

   Optional `--model role=value` assignments include only `launch fresh`
   members. No resume effort assignment is emitted in v1.
10. After preview succeeds, the separate prompt appears: `Resume now? [y/N]`.
11. On explicit Yes, the wizard refreshes and compares the complete discovery
    fingerprint a second time. Any delta returns to Profile & run without
    executing. An unchanged fingerprint executes the same canonical arguments
    plus `--exec`. No or EOF exits without mutation.

The wording is **Resume now?**, not **Launch now?**, because the final prompt
must name the action it will perform.

## Review contract

Review is complete enough for an operator to validate the plan without reading
source code or mentally reconstructing earlier screens.

Every project review shows:

- scope and project root;
- branch: existing profile or new profile;
- profile and session, including how the session was derived;
- backend: `run start` or `resume`;
- roster and lead facts, labeled `stored/authoritative`, `new`, or
  `launch-only override`;
- model and effort values for every role, labeled as stored, saved/restored,
  live/unchanged, or fresh-launch override; controls omitted by contract display
  `not offered`, never an empty value;
- matching launch-record count, liveness, and per-member actions for resume;
- placement, layout, operator contract, notifications, and launcher behavior
  when applicable;
- goal and seed source;
- brief path for a resume;
- topology diagram when placement is visual;
- exact preview and live command forms.

Goal copy may be long. Review shows a two-line, 160-character excerpt followed
by `...` when truncated. The full value remains in the answer model and command.
Seed source is never truncated because paths and issue references must be
checkable verbatim. Empty values display as `not provided`; they are never
silently omitted.

Global review shows scope, neutral root, agent, model, effort, native args,
window name, backend, and exact preview/live command forms.

## Canonical command mapping

| Branch | Preview, no mutation | Explicit-Yes action |
| --- | --- | --- |
| New profile, new session | `amq-squad run start <canonical args>` | Same args plus `--go`. |
| Existing profile, known session not started | `amq-squad run start <canonical args>` | Same args plus `--go`. |
| Existing profile, stopped/partly running with matching records | `amq-squad resume <canonical args> --restore-existing` (plan-only; JSON may be used internally for the refreshed plan) | Same args plus `--exec`, after the second fingerprint check. |
| Existing profile, live-plus-fresh with zero matching records | `amq-squad resume <canonical args>` without `--restore-existing`; only fresh-member model overrides may be present. | Same args plus `--exec`, after the second fingerprint check. |
| Existing profile already running/blocked | No executable command. | No confirmation prompt. |
| Global/NOC | `amq-squad global start <canonical args>` | Same args plus `--go`. |

The answer model therefore needs an explicit backend/action field rather than
inferring behavior late from whether a profile happens to exist. The UI package
still returns answers only; command execution remains in the CLI layer. Every
row backed by an existing profile is guarded by the full fingerprint comparison
before preview and again before execution; the new-profile and global branches
have no pre-existing discovery snapshot to compare.

## Back navigation and stale answers

Changing an upstream fork clears answers that no longer apply:

- changing scope clears project/global-only answers from the abandoned branch;
- changing project clears discovered profiles, sessions, liveness, and all
  downstream project answers;
- changing existing profile clears session, action, overrides, and preserved
  brief facts;
- switching from existing to new clears authoritative profile facts and resume
  state;
- switching from new to existing clears fresh roster/lead/contract choices;
- changing session refreshes liveness and clears the selected backend;
- changing placement resets incompatible layout and launcher values.

Back navigation restores a snapshot only while its full discovery fingerprint
still matches. Both mandatory freshness checks use the same invalidation path.
A delta in roster, policy, brief identity, namespace conflicts, records,
liveness, or member plan clears downstream answers and explains why the
operator is returned to Profile & run.

## Accessible numbered adapter

The numbered adapter follows the same nodes, option labels, notes, derived
session rules, and backend mapping. It is not allowed to retain the old
free-text scope/session shortcuts. Consequence text prints immediately below
each choice list, and Review prints the same fields as the full-screen TUI.

This keeps accessibility behavior semantically identical without requiring the
full-screen renderer.

## Acceptance criteria for implementation

Core invariants:

- Scope is a first-class wizard state for project and global flows; there is no
  pre-TUI scope prompt.
- Existing-vs-new profile is the first project decision after root discovery.
- A pinned existing profile never enters a free-text session screen.
- The precedence table classifies every valid discovered session into exactly
  one state; invalid or ambiguous discovery is Blocked.
- Resume is reachable only through an existing profile/session and uses shared
  status/resume liveness facts plus matching-record counts.
- Every decision-tree leaf ends in exactly one of: Review, an explicit Back
  choice, or Blocked. There is no dead-end or implicit execution leaf.
- Running and blocked sessions cannot reach an executable final confirmation.
- The final preview and confirmation name the selected backend action.
- Review contains goal and seed source, with goal truncation and seed preserved
  verbatim.
- Every screen has consequence copy understandable without source knowledge.
- Discovery, navigation, and preview remain non-mutating; only explicit Yes
  invokes `--go` or `--exec`. For existing-profile branches, that Yes follows
  an unchanged second fingerprint check.
- Catalog-backed model/effort choices are injected so #432 can change their
  source without restructuring the decision tree.

Required table-driven cases:

| Case | Input facts / action | Expected state and leaf | Assertions shared by Bubble Tea and numbered adapters |
| --- | --- | --- | --- |
| All fresh, no records | Zero live, zero matching records, every member `launch fresh`. | Not started -> start known session -> Review. | Backend is `run start`; no resume screen and no `--restore-existing`. |
| All restore | Zero live, one or more matching records, every member `restore`. | Stopped -> Resume -> Review. | `--restore-existing` present; saved model/effort/native args shown read-only; no override controls. |
| Stopped restore plus fresh | Zero live, matching records > 0, actions include `restore` and `launch fresh`. | Stopped -> Resume -> Review. | `--restore-existing` present; restore rows read-only; model override offered only on fresh rows; no resume effort override. |
| Live plus fresh, no records | One or more `live`, one or more `launch fresh`, zero matching records. | Partly running -> Restore missing members -> Review. | Resume command omits `--restore-existing`; live rows unchanged; model override only on fresh rows. |
| Live plus restore | One or more `live`, one or more `restore`, matching records > 0. | Partly running -> Restore missing members -> Review. | `--restore-existing` present; live and restore rows have no override controls. |
| All live | Every member `live`. | Running -> Back/Create new profile. | No Review or execution confirmation is reachable. |
| Any blocked | At least one member `blocked`, regardless of other actions or record count. | Blocked. | No executable command; blocker and diagnostic command shown. |
| Ambiguous namespace | Namespace/profile resolution reports a collision or ambiguous owner. | Blocked. | No member rollup can override the block; exact scoped diagnostic shown. |
| Empty profile | Existing profile has zero members. | Blocked. | Plain-language invalid-profile note; Back is available. |
| No-session profile | Existing authoritative profile has no pin and no matching history. | Recommended unused-profile first-session branch -> Review, subject to operator question 1. | Suggested session is derived once; no generic existing-session free-text loop; backend `run start`. |
| Multiple history sessions | Profile has two or more matching historical sessions. | Session list -> selected session's unique state leaf. | Each row shows source/state; changing selection clears action, overrides, and preserved brief facts. |
| Restore override attempt | Member action is `restore`. | Read-only saved-launch screen. | Model/effort controls are absent and Review cannot contain an override assignment for the member. |
| Fresh model override | Member action is `launch fresh` on resume. | Model override screen -> Review. | Exact role appears in resume `--model`; no restore/live role appears in the assignment. |
| Resume effort attempt | Any resume member action. | No effort override screen. | Review says stored/saved effort and `not offered`; no `--effort` or role-flattened native-arg workaround is emitted. |
| Upstream scope change | Project answers exist, then scope changes to Global/NOC (and reverse). | New scope's first applicable screen. | All abandoned-branch project/global answers are cleared. |
| Upstream project/profile/session change | Downstream action, override, run-control, and brief answers exist, then an upstream value changes. | Refreshed Profile & run path. | Every dependent answer listed in Back navigation and stale answers is cleared. |
| Fingerprint delta before preview | Any roster, lead/mode, operator/notification, stored model/effort, session source, brief identity, namespace conflict, record, liveness, or member-plan fact changes. | Return to Profile & run. | Preview is not called; downstream answers clear even if the rollup label is unchanged. |
| Fingerprint delta after Yes | Preview passed, then any fingerprint component changes before execution. | Return to Profile & run. | `--go`/`--exec` is not called; the prior Yes is discarded. |
| Fingerprint unchanged twice | Existing-profile branch reaches Review and both refreshes equal the reviewed fingerprint. | Preview, default-No confirmation, then execution only on Yes. | Preview and live argv differ only by `--go` or `--exec`; second check precedes mutation. |
| Review completeness | Goal/seed supplied or preserved; mixed member actions. | Review. | Scope, branch, session source, matching record count, every member action/value source, goal excerpt, full seed, backend, and both commands appear. |
| Leaf audit | Walk every option from every decision node. | Review, Back, or Blocked only. | No missing transition, implicit mutation, or unreachable recovery choice. |
| Adapter parity | Replay every case above through Bubble Tea and numbered adapters. | Same semantic leaf and answer model. | Canonical argv, cleared fields, notes, fingerprint behavior, and Review values match; renderer-only formatting may differ. |

## Open questions for operator review

1. **Existing profile with no pinned or historical session.** Recommendation:
   treat it as an unused authoritative profile and offer one suggested first
   session, then use `run start`. The stricter alternative is to require a new
   profile for every new session.
2. **Existing profile with multiple historical sessions.** Recommendation:
   show only sessions whose launch records match the selected profile and put
   the pinned/current session first. Should archived sessions be hidden or
   shown with an `archived` label?
3. **Partly running squad.** Recommendation: label the action `Restore missing
   members`, use the ordinary resume planner, and skip live members. Should the
   wizard require an extra warning when the lead is one of the live members?
4. **Per-role effort for launch-fresh resume members.** The v1 contract omits
   this control because resume has no truthful per-role effort flag.
   Recommendation: add `resume --effort role=value,...` later for
   `launch fresh` actions only. Is that command-surface addition desired in
   #428 implementation or a follow-up?
5. **Resume placement default.** Recommendation: default to the saved launch
   target when all restorable records agree, otherwise `One window per agent`.
   Should the default always remain the current `resume --exec` target instead?
6. **Goal excerpt length.** Recommendation: two lines / 160 characters in the
   Review panel, full goal in the command. Is another limit preferred?
7. **Already-running selection.** Recommendation: keep this wizard
   non-supervisory and offer only Back/Create new profile. Should it also expose
   a copyable `amq-squad focus` action, even though focus is outside the three
   wizard backend commands?
8. **Overrides for restored members.** The v1 contract shows saved launch args
   read-only and offers controls only for `launch fresh` members. Should a future
   resume explicitly rewrite saved launch arguments for both model and effort
   before restoring, or should override controls remain limited to fresh
   members? Recommendation: keep restored records immutable in #428 and design
   rewrite parity as a separate, auditable feature.

## Issue references

- #431: explicit wizard decision tree and honest phases
- #428: resume an existing squad as an existing-profile branch
- #432: injected configurable model/effort catalog; related, not implemented
  here
- #423: current pinned-session inline validation superseded by derivation
- #424: plain-language operator-step copy carried forward on every screen
