# The two stepped start flows

Simple Mode has one launch command and one default-No approval. There is no
prepare stage, readiness manifest, accepted digest, or separate go command.
The wizard chooses one of these flows and keeps its coordinates explicit.

## Flow A: new team profile

1. **Machine readiness**

   ```sh
   amq-squad setup --drafter-chain yoetz,claude,codex --drafter-on-failure in_session
   amq-squad doctor --project P
   ```

   Setup probes available drafter CLIs and writes the global config only. Omit
   its flags when the operator wants the interactive prompts.
   Decide the ordered chain and failure policy. A profile `drafter` block, when
   present, replaces the complete global block; otherwise global wins, then
   `in_session`. Stop on invalid config, missing required binaries, or blocking
   doctor findings.

2. **Profile**

   ```sh
   amq-squad new profile R --project P --session S \
     --roles cto,fullstack --binary cto=codex,fullstack=claude \
     --actor-mode cto=review,fullstack=implementation \
     --orchestrated --lead cto --dry-run --json
   amq-squad new profile R --project P --session S \
     --roles cto,fullstack --binary cto=codex,fullstack=claude \
     --actor-mode cto=review,fullstack=implementation \
     --orchestrated --lead cto --sync
   ```

   Compare preview and write. Use `new team` for default or
   `--no-session-pin` for a reusable profile. Resolve multiple implementation
   actors with isolated working directories or an explicit shared-CWD exception.

3. **Custom seat personas**

   ```sh
   amq-squad role draft researcher --binary codex \
     --purpose "Investigate ambiguous product behavior" \
     --project P --profile R --session S
   amq-squad team member add researcher --binary codex --actor-mode review \
     --project P --profile R --session S
   ```

   Review the staged file before member add. Record config source and every
   attempt/fall-through. Manual fallback stages nothing; a valid draft must pass
   frontmatter and Mission/Boundaries/Protocol validation.

4. **Team rules**

   ```sh
   amq-squad team rules init --project P --profile R --template auto --force
   ```

   Custom charter/scope prose uses the shared drafter, then deterministic policy
   sections wrap the validated fragment. Manual fallback writes nothing.

5. **Brief and launch**

   ```sh
   amq-squad start --project P --profile R --session S --goal "Ship the reviewed change"
   ```

   If the brief is absent, the configured drafter must report source and ordered
   attempts, pass exact six-section validation, and print the proposed bytes.
   Approve those bytes and the launch plan at the same interactive prompt. No,
   invalid output, or fallback writes nothing and starts no pane. A live skill
   agent may instead draft the same shape in-session, show and save it, then let
   `start` reuse it at `P/.amq-squad/briefs/R/S.md` (or
   `P/.amq-squad/briefs/S.md` for the default profile). Display the saved bytes
   and require the start plan's `brief:` line to match. After Yes, trust success
   only after every pane owns its live child. Run the attach command printed for
   a detached tmux session, then inspect `amq-squad status --project P --profile
   R --session S --json`; do not hand off while a visible-lead invariant is
   still failing.

## Flow B: new session from an existing profile

1. **Coordinates and health**

   ```sh
   amq-squad doctor --project P --profile R --session S
   ```

   Doctor validates project/profile health and uses `S` only for additive
   session-plan diagnostics. Select a reusable unpinned profile, then inspect
   the namespace directly with `amq-squad status --project P --profile R
   --session S --json`. Do not silently reuse a pinned profile or an existing
   namespace.

2. **Brief choice**

   Leave `P/.amq-squad/briefs/R/S.md` absent for configured `start --goal`
   drafting. To reuse named profile `R`'s `OLD` brief, first run `test ! -e
   P/.amq-squad/briefs/R/S.md`, then `mkdir -p P/.amq-squad/briefs/R` and `cp
   P/.amq-squad/briefs/R/OLD.md P/.amq-squad/briefs/R/S.md`. The default-profile
   path omits `/R`. Edit and display the copied title, Goal, Source, Scope, Out
   of scope, Team shape, and Acceptance. A live agent may draft that exact shape
   in-session and write it to the same canonical path after review. Raw tickets,
   wrong default/named paths, and stale copied briefs are not accepted scope.

3. **Preview and launch**

   ```sh
   amq-squad start --project P --profile R --session S --goal "Ship the reviewed change"
   ```

   With no brief, apply Flow A's same-invocation draft approval. With a reviewed
   brief, inspect the reused path and complete launch plan. Use `--yes` only for
   automation after a reviewed brief exists and inputs remain unchanged. Apply
   Flow A's attach/status verification, then route the visible lead to
   `amq-squad:orchestrator`.

## Recovery

- Add a role to the roster, then rerun `start`; only the missing role starts.
- To replace a role, run `down`, update its entry, then rerun `start`.
- After an interrupted fresh launch, rerun `start`; do not delete the namespace.
- Use `resume` when preserving and reattaching saved conversations is the goal.
