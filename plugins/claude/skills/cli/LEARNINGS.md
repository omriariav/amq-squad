# LEARNINGS — cli

Field failures, newest first. Each entry is a real failure with its root cause and the
exact fix. When an entry generalises, it graduates into the Gotchas table in
`SKILL.md`; this file is where the raw material accumulates so that section improves
per release instead of decaying.

Add an entry when something cost you more than a minute of confusion. The value is in
the ones that *looked* like user error and were not.

---

## Verifying a claim: how assertions fail silently

Seven distinct ways a check passed while proving nothing, all observed while building
the v2.25.0 drift gate. They are listed because each one was caught by review after
being written by someone confident it was fine.

**1. Vacuous assertion.** The assertion cannot fail. A test asserted
`math.ceil(23 * 0.5) == 12` — which tests the language, not the code under test, and
stays green when production truncates instead.

**2. Vacuous fixture.** The setup never reaches the code. A byte-identity test called a
builder with an empty argument, which returned no plans, so the comparison loop never
executed. Green, and blind.

**3. Vacuous at the chosen constants.** The assertion is real but the inputs cannot
distinguish the behaviours. At 23 items a ceiling and a truncation both yield 12; only
at 25 do they diverge (13 vs 12). *Choose inputs where the behaviours differ.*

**4. Skip masquerading as pass.** A fixture that cannot reach the code under test
skipped, and a skip is indistinguishable from a pass in the summary line. A fixture
that cannot reach its target is BROKEN, not environmentally absent — fail it.

**5. Confounded proof, two variables.** A removal proof changed a constant *and* a
calculation in one edit. The observed failure proved only that one of them mattered.
*Vary exactly one thing.*

**6. Confounded proof, two mechanisms.** A per-format test passed because a second,
unrelated source independently satisfied it. The union masked the component. *Isolate
the mechanism, not just the variable* — disable the other source.

**7. Mutation that misses.** A probe edited the wrong file, or a string replacement
silently no-op'd on a wrong target, and the unchanged run read as "no regression".
*Assert the target exists before mutating it.*

The working rule: every assertion gets a removal proof; the proof varies exactly one
thing; and the assertion is evaluated where the behaviours diverge.

---

## Flag existence is not invocation correctness

A drift gate that verifies every named flag EXISTS still cannot tell you the command
would run. Four documented invocations in this skill errored as written while passing the
gate:

    evidence run ID --me H --subject TEXT        missing the required `-- COMMAND`
    gate raise --session S                       missing required --gate/--kind/--action/--target
    verify merge --session S                     --session is not a flag of this command
    verify release-plan --session S               same

The gate could not refute any of them. Two were incomplete rather than wrong — every flag
present was real, just insufficient — and two named a flag the command does not accept, on
an illustrative help surface the gate can only confirm from, never refute against.

A reviewer found them by EXECUTING the table. That is the only method that works here.

Practical rules:

- **Copy a form you have actually run.** Not one assembled from a flag list.
- **Run the command with no arguments** to learn what it requires; these four all name
  their missing pieces immediately (`requires TASK [flags] -- COMMAND [ARG...]`,
  `project is required`, `--evidence is required`).
- **Treat "the gate is green" as covering existence only.** Verified flag count is a floor
  on correctness, not a measure of it.

The general shape, and the reason this sits in a skill rather than a commit message: a
check proves the property it tests, and its silence about everything else is not evidence.
Three separate gates in this repo were green on these lines.

## Posture: what a check should do when it cannot tell

**Probe posture is context-derived, not code-shaped.** The same observation code
defaults opposite ways depending on what a wrong answer costs. A launch-path probe
fails OPEN, because a false positive kills a working launch. A readiness or verification
probe fails CLOSED, because "cannot verify" is a blocker. Reusing probe-shaped code
without re-deriving its default produced a fail-open verifier.

**Fail the RIGHT thing.** An unreadable observation should fail the BUILD; a definitive
negative should fail the REFERENCE. Getting that backwards either invents drift or hides
it.

**Both directions are defects.** Fail-open invents safety. Fail-closed on incomplete
data invents drift. A fix that only moves the error to the other side is not a fix.

**A source may CONFIRM; only a proven-exhaustive source may REFUTE.** Help text is
illustrative unless exhaustive by construction. Treating an illustrative list as
complete turns "not shown" into "not accepted" and rejects working commands.

---

## Extraction: never filter on well-formedness

A pattern-based skip at extraction time converts drift into silence. A renamed
command containing an unexpected character vanished from a checker three separate times
— in the verb position, the subcommand position, and the flag position — because each
extractor filtered malformed tokens instead of passing them to verification.

*Extract anything reference-shaped; let verification judge it.*

---

## Rewrites can delete required content

Converting invariant prose into runnable commands removed an ACTIVE compatibility
policy, because it read as stale narrative. Nothing caught it: a drift gate proves that
named things EXIST, not that required things are PRESENT. Those are different
guarantees.

*Mark required policy as required, in the file, so the next rewriter recognises it.*

---

## Namespace and shell traps

**Ambiguous profile.** `error: ambiguous profile at live_launch_record precedence` means
several live launch records resolve. Pass `--profile NAME`; the CLI prints that fix
itself, and `context explain` lists the candidates with why each won or lost.

**Relative paths anchor to the project, not your shell.** A value recorded relative to a
shell means something different the next time a command runs from elsewhere. This caused
identity-drift errors whose two operands printed identically.

**Shell-special text in message bodies.** Backticks and `$()` in an inline body are
expanded before the tool sees them, silently mangling or emptying the message. Pass the
body from a file or stdin.

**Whitespace classes match newlines.** A regex using a generic whitespace class crossed
a line boundary and captured the next line's token. Decide per pattern whether newlines
belong.
