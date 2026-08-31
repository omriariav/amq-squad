# amq v0.74.0 / v0.74.1 / v0.75.0 adoption verdict

Measured re-verification of amq v0.74.0, v0.74.1, v0.75.0 against
amq-squad's launchapi path, in the style of the v0.73.0 diff-verdict
(`docs/amq-0.73.0-adoption-verdict.md`). No source changes in this section;
sections below record what changed.

Method: each amq version installed into its own temp `GOBIN`
(`GOBIN=/tmp/amq-verdict-bins/vX.Y.Z go install
github.com/avivsinai/agent-message-queue/cmd/amq@vX.Y.Z`), reusing the
directory the v0.73.0 doc's methodology already established (v0.70.0
through v0.73.0 binaries were still cached there). v0.76.0 was also
installed and measured, one version past this floor's pin, per an
operator note that it released upstream the same day this task ran.

## 1. `launchapi/` byte-diff

```
$ git diff v0.73.0 v0.75.0 -- launchapi/    # empty
$ git diff v0.75.0 v0.76.0 -- launchapi/    # also empty
```

**Byte-identical from v0.73.0 through v0.76.0.** `Compatibility()`/`Negotiate()`
still cannot see anything in this line; the per-call gate stays
`Preview.Capabilities[].GrammarVersion`, unchanged from the v0.73.0 verdict's
section 6 and 3/4 findings, which continue to hold without re-measurement.

## 2. Real-AMQ lanes, golden intent, dual-run parity

```
$ AMQ_SQUAD_REAL_AMQ=/tmp/amq-verdict-bins/v0.74.1/amq AMQ_NO_UPDATE_CHECK=1 \
    go test ./internal/cli -run '^TestRealAMQCompatibility$' -count=1
ok  (16.75s)

$ AMQ_SQUAD_REAL_AMQ=/tmp/amq-verdict-bins/v0.75.0/amq AMQ_NO_UPDATE_CHECK=1 \
    go test ./internal/cli -run '^TestRealAMQCompatibility$' -count=1
ok  (16.31s)

$ AMQ_SQUAD_REAL_AMQ=/tmp/amq-verdict-bins/v0.74.1/amq \
    go test ./internal/cli -run '^TestRealAMQWakeCompatibility$' -count=1
ok  (28.78s)

$ AMQ_SQUAD_REAL_AMQ=/tmp/amq-verdict-bins/{v0.74.1,v0.75.0}/amq \
    go test ./internal/launchintent ./internal/cli \
    -run '^(TestCompiledIntentAcceptedByReleasedAMQ|TestLaunchapiBackendDualRunParity)$' -count=1
ok  (both versions, both tests)
```

**Verdict: adopt.** Both v0.74.1 and v0.75.0 pass the full real-AMQ lane
suite, the golden intent round-trip, and dual-run parity, with no failures
(not even the one-off v0.72.0 timing flake the v0.73.0 doc noted).

## 3. gh#734 re-verification: upstream #676 "isolate nested worktree root discovery"

The v0.73.0 doc's own repro (a plain nested subdirectory, no `.amqrc`) was
insufficient to exercise the actual vulnerable code: amq's general
`.amqrc`/`.agent-mail` walk-up was already git-worktree-ceiling-aware before
v0.74.0. The bug upstream #676 fixed lived specifically in
`findRootInParents` (relative-root resolution) and `findAmqrcForRoot`
(pre-resolved-root walk), neither of which that plain-subdirectory repro
ever reached.

Corrected repro, direct proof: ported v0.74.0's own regression test
(`internal/cli/nested_worktree_discovery_test.go`,
`TestNestedWorktreeDoesNotAdoptParentLiveBaseRoot`) onto a v0.73.0 checkout
of the upstream repo.

```
$ git checkout v0.73.0
$ cp <v0.74.0's nested_worktree_discovery_test.go> internal/cli/
$ go test ./internal/cli -run TestNestedWorktreeDoesNotAdoptParentLiveBaseRoot -v
--- FAIL: .../symlinked_path_into_nested_worktree_does_not_adopt_parent
    findAmqrcForRoot via symlinked path adopted .../parent/.amqrc
--- FAIL: .../relative_.agent-mail_does_not_walk_into_parent_live_queue
    resolveRoot(".agent-mail") adopted parent live queue .../parent/.agent-mail
--- PASS (other 5 subtests)

$ git checkout v0.74.0    # test file already present upstream
$ go test ./internal/cli -run TestNestedWorktreeDoesNotAdoptParentLiveBaseRoot -v
--- PASS (all 7 subtests)
```

**The bug is real pre-v0.74.0** for the specific relative-root and
symlinked-path code paths, and **upstream's fix closes it**, confirmed
directly rather than assumed from the changelog.

Separately, a real-binary reproduction with an actual `git worktree add`
nested checkout (not a plain subdirectory) against amq-squad's own CLI
invocation shape: `amq env --json` from inside the nested worktree now
refuses outright ("cannot determine an eligible AMQ root while cwd is
inside Git worktree or bare repository ...") instead of silently resolving
anything, on both v0.74.1 and v0.75.0. See
`TestNestedWorktreeRootDiscoveryOnAMQ074` (`internal/cli`), the durable
real-AMQ-gated regression for this.

### Does this change our own `base_root` seam?

**No.** `internal/adoptionseam.Prepare` never calls `amq env`/CLI discovery
at all — that is gh#734's entire design ("Go-API-only adoption seam with
explicit base_root, never CLI upward discovery"). `ErrEmptyBaseRoot`'s
refusal was never contingent on upstream's bug existing, so it cannot
become contingent on upstream's fix either. gh#734's own acceptance
criteria says it plainly: *"Even if upstream closes the discovery gap, this
guard remains defense-in-depth and these tests stay meaningful."*

What changes is only the **narrative**, not the refusal:

- **Before this task:** the seam's doc comments framed it as the sole
  defense against a bug upstream had not yet fixed ("required defense").
- **After this task:** upstream has independently closed the specific
  discovery paths gh#734 exploited. The seam is no longer the only thing
  standing between amq-squad and that bug class — it is now
  **belt-and-braces**, recorded in the exported, purely informational
  `adoptionseam.BaseRootSeamStatus = "belt_and_braces"` constant. This
  value never gates or weakens `ErrEmptyBaseRoot`; it only documents which
  of the two framings currently holds, and `TestNestedWorktreeRootDiscoveryOnAMQ074`
  is the real-AMQ evidence backing it.

### Deviation from gh#768's literal text

gh#768 says: *"if it no longer reproduces, downgrade the seam from
'required defense' to 'belt-and-braces' in docs and **make its absence a
warning rather than a refusal** at or above the new floor."* Read literally,
that final clause would make `ErrEmptyBaseRoot`'s hard refusal conditional
on the pinned/negotiated amq version — i.e. weaker at or above v0.74.1. This
directly conflicts with gh#734's own explicit, permanent stance quoted
above, and would make a fail-closed safety property depend on which amq
version happens to be pinned. Treated as a drafting error in the issue text
(cto decision, recorded on task/t5): the refusal stays **unconditional at
every amq version**; only the documentation and the new status constant
change. This deviation will be folded into gh#768 at close-out.

## 4. v0.74.1: `launch: retry tmux new-session once after server exit race` (#692)

The fix (`internal/launch/tmux.go`'s `runNewSessionWithTransientRetry`) is
entirely inside amq's own unexported `internal/launch` package, used by
amq's `internal/launch.TmuxBackend.Create` — amq's *own* CLI launch
mechanism (`amq launch`/`amq coop`).

**amq-squad's launchapi path cannot inherit this by construction**:
`adoptionseam.Prepare`/`Apply` call the public `launchapi` package
exclusively and never shell out to `amq launch` at all (the same
Go-API-only contract gh#734 established). `launchapi/` itself is
byte-identical across this entire range (section 1), so there is no wire
surface through which this internal retry could reach amq-squad even
indirectly.

**amq-squad's own tmux new-session path is entirely separate, unaffected
code**: `internal/cli/team_launch_tmux.go` (lines composing
`tmux new-session ...` directly via `tmuxOutputCommand`/`tmuxRunCommand`)
has no retry logic today and does not share any code with amq's
`internal/launch` package. Confirmed by direct inspection, not assumed.

**Recommendation (cto-approved, out of scope for this task):** adding the
same transient-retry behavior to amq-squad's own tmux new-session call site
is a reasonable follow-up, but gh#768's named tests do not ask for it and
bundling it into this floor-bump would widen the change beyond what was
reviewed. Tracked as a follow-up note, not implemented here.

## 5. v0.75.0: bridge-only

`launchapi/` unchanged (section 1); no launch-relevant changelog entries.
**Confirmed no launch impact**, as expected.

## 6. v0.76.0 (informational, out of scope)

Released the same day this task ran. `launchapi/` is also byte-identical
between v0.75.0 and v0.76.0 (section 1). **Deferring its adoption to
v2.32.0 carries no urgency** — nothing launch-relevant changed between the
version this floor pins and v0.76.0 either.

## Adopt/defer summary

| Item | Verdict |
|---|---|
| 1. `launchapi/` byte-diff v0.73.0–v0.76.0 | Adopt as evidence: unchanged, floor mechanism unchanged |
| 2. Real-AMQ/golden/dual-run lanes on v0.74.1/v0.75.0 | Adopt: all pass, no flakes |
| 3. gh#734 re-verification | Adopt: upstream's fix is real and closes the specific vulnerable paths; our own seam's refusal stays unconditional regardless (docs-only reframing, see deviation note) |
| 4. v0.74.1 tmux retry (#692) | Adopt as evidence: confirmed no inheritance via launchapi or our own code; follow-up noted, not implemented here |
| 5. v0.75.0 bridge-only | Adopt: confirmed no launch impact |
| 6. v0.76.0 | Informational: adoption deferred to v2.32.0, no urgency |

## Floor and pin

- `internal/adoptionseam.AdoptionFloorAMQVersion`: `v0.72.0` → `v0.74.1`.
- `internal/adoptionseam.AdoptionFloorContractSemver`: unchanged, `>=0.61.1`
  (launchapi's negotiable contract has not moved since v0.70.0).
- go.mod pin: `v0.73.0` → `v0.75.0`.
- General-operation floor (`doctorMinAMQVersion`): unchanged, `0.60.0` — the
  two floors remain independent per gh#746's design; they merge only when
  the launchapi backend becomes the sole default (already true as of
  gh#755/v2.31.0, but that flip did not itself change either floor's
  value).
- CI `adoption-floor-compatibility` lane: job name, install version, and
  `AMQ_SQUAD_REAL_AMQ_VERSION` all moved `v0.72.0` → `v0.74.1`
  (`.github/workflows/ci.yml`).
