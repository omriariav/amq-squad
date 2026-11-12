# amq-squad 3.0: follow-ups after the release

Items deferred out of 3.0 by a recorded decision, each with the plan section
that records it (docs/v3-plan.md unless named otherwise). They outlive the
planning sessions that made them; nothing here is a release blocker.

## 3.2.x

One list for 3.2.x, grouped by area. It folds in the items the plan reviewer
named for 3.2.x while rc.2 was built (2026-11-07 to 2026-11-11); the rc.2
notes link here and repeat no list. An item that moved out of rc.2 is marked
"moved from rc.2, 2026-11-10"; an rc.2 item that landed is marked done with
its PR. Nothing here is promised by date.

### Flakes and test robustness

- **Load-sensitive test timings.** Fix rule for every row: no wall-clock
  budget that host load or the test's own setup can exhaust. Counts, timings
  and loads below are as reported in the 2026-11-06 and 2026-11-07 agent and
  lead run reports unless a row cites the record that holds them.

  1. `internal/seatlaunch TestPiSeatPlansThroughTheUpstreamAdapter` failed at
     about 5.2 s against a 5 s budget with
     `amqpi_test.go:75: outcome unsupported (launcher_not_available)`. It was
     seen twice on 2026-11-06 in quiet-tree `make ci` runs by the 3a and 0c-B
     agents. Sources: the test at `internal/seatlaunch/amqpi_test.go:75` and
     #903's 3a execution note at
     `docs/v3-spike/320-3a-train/async-update.md:1618`.
  2. `internal/registry TestMeasureTTFT` also spends wall-clock time inside
     the test and belongs under the same rule. Source:
     `internal/registry/probe_test.go:169`.
  3. `internal/cli TestContextHostKillsAHangingCommandsGroupAndNamesTheFactMissing`
     lost its fake git at the 4 s deadline before the script wrote
     `child.pid`; `TestContextHostSharesOneBudgetAndLeavesNoProcessBehind`
     returned an empty branch after its shared 4 s budget was spent. Both
     were seen on 2026-11-06 at load about 10, when the first execution of a
     fresh temporary script took 0.3 to 0.5 s. Sources:
     `internal/cli/first_turn_unix_test.go:52` and `:98`, and the #837 row in
     `docs/v3-spike/31-evidence/31-3-pr2-r53-first-turn.md:299`.
  4. `internal/buzz TestOwnerScriptDryRun` hung at
     `enroll_test.go:447`, with no signal for "the attach verifies the thread
     answer", until its 5 minute budget expired against the 127.0.0.1 relay
     under load; it passed on rerun. Source: the test and its signal rule in
     `docs/v3-spike/5-301-evidence/t18-buzz-dryrun-signal.md`.
  5. The pi R53 guard rows use a 5 s guard timeout. Under host load two R53
     rows took 8.2 s and 9.2 s and failed with `amq-squad guard failed closed
     (timed out after 5000 ms)`; the rerun passed, with the load average at
     17.5 at its end (`docs/v3-spike/320-3a-train/async-update.md:1806-1816`).
     An R53 row also failed once at 7.0 s while the host gate lock was held,
     with load 12.5 from work outside the lock (`async-update.md:1865-1869`).
     The R53 tests are in `pi/test/phase.test.ts`.
  6. In the same run the pi screen row `row 2: the screen's last line ...`
     took 11.4 s and failed with `TypeError: Cannot read properties of
     undefined (reading 'lines')`; it is a separate screen-row timing
     failure, not an R53 guard row. Source:
     `docs/v3-spike/320-3a-train/async-update.md:1808-1812`.
  7. `internal/cli/repass5_fix_test.go:74`
     `TestDecisionAdapterKillsAHungGuardGroup` reported a hung guard and
     unparsable guard output after 30.0 s at load 16.7. Source: the test's
     watchdog assertion.
- **The live-holder test's helper** (`internal/schedproc/cancel_tamper_recording_test.go`;
  the #947 gate, named for 3.2.x, not a gate): the helper process exits by
  itself 5 s after readiness, so a host slow enough to take 5 s between the
  helper's start and the cleanup fails at the cleanup with "reservation
  helper did not exit before its deadline" although the product is right
  (measured body 0.32 s, the lead's run, 23:58 IST 2026-11-10); and the
  readiness-timeout path kills the child and reads its output buffer without
  `cmd.Wait()`, so the child is not reaped and the read races the copier.
  Failure-path only. Fix: a wider lifetime tied to the test's deadline, and
  Wait before the read. Source: Codex's pass on #947, findings 6 and 7, read
  not measured.
- **"a concurrent cold pi install can refuse one caller"** (the #953 re-gate):
  `internal/piruntime/install.go:245` runs `verifyAnchored` before the lock,
  and `:277-290` `missingCleanly`: another installer's rename landing between
  `OpenAnchored`'s ENOENT and the Lstat walk turns "not present" into a
  rejection. Seen once in CI, run 42948282398,
  `TestConcurrentInstallUnpacksOnce` at `piruntime_test.go:337`. Rule until
  fixed: a required check failing on it is rerun once and recorded with the
  run id; twice in one PR's gate goes to the reviewer.
- **"TestProofConnectionsAreBounded fails when the server's malformed reply
  beats its own write deadline"** (`internal/seatlaunch`: `proof_unix.go:285-286`,
  `:298`; `fix3c_test.go:175-176`). Ruled 3.2.x at the #955 ruling,
  2026-11-11, under the rerun-once rule: rerun once, recorded each time;
  twice in one PR's gate goes to the plan reviewer. First seen: the locked go
  test on #955's head 12cc6f42, 05:41 to 05:46 IST.
- **The real-AMQ pinned check failed once on a docs-only commit** (the #960
  ruling, point c; a behaviour, not a timeout):
  `TestRealAMQCompatibility/coop_exec_drains_preexisting_goal_with_zero_injection`
  failed once in CI on #960's 4743946d (run 42989602925, job 130418945093,
  the check "real AMQ compatibility (pinned v0.116.2)"; canary latest passed
  in the same run; the pinned check passed on 98e47d8c before it and on
  8534d65c after it). The failure's words, `real_amq_compatibility_test.go:982`:
  "coop exec injected pre-existing backlog instead of relying on bootstrap
  drain: AMQ doorbell run amq drain --include-body then act on it". Read it
  as the product injecting a backlog message that the bootstrap drain should
  have taken, under some ordering; the row's owner finds the ordering. Rule
  until fixed: rerun once and record with the run id; twice in one PR's gate
  goes to the reviewer.
- **The rehearsal's plant recount** (Codex's finding 1 on #948): it recounts
  through four separate calls on attach's output buffer
  (`Len`/`Write`/`String`/`Truncate`, `rehearsal_test.go:299-304`) while
  attach may still write; make it one locked step or take it after attach
  stops.
- **The position-pinning row for the end-of-flow approved count** (item d.2
  of the attach-hang follow-up, both flows; the #948 gate, a named limit).
  The end count exists (`internal/buzz/enroll_test.go:504`,
  `rehearsal_test.go:293-294` on #948's head 6150ccc9) and nothing pins where
  it sits: the lead's four reverts that move it back to right after the
  answer, or drop the tally, are green (d-dry-move, d-dry-tally, d-reh-move,
  d-dry-const; 2026-11-11 00:28 to 00:34 IST).
  `checkApprovedOutcomes("dry run", approvedOutcomes+1, …)` at
  `enroll_test.go:525` is arithmetic, not a second record. The row: a planted
  second outcome that goes into the flow's own tally (or into attach's
  output) BEFORE the one end count, reported through the recording function;
  red when the count moves back.
- **Done (#951): the Buzz owner-resend helper's same-id defect**
  (`internal/buzz/helpers_test.go:219-245`), listed only if t151 was unmerged
  when the release PR opened; t151 merged as #951 (946da84b).
- **The owner-sender row compares the two events by id**, and has a row for
  the wait and for the injected clock (the reviewer's ruling on #951,
  2026-11-11 03:20 IST): the second send dated first-1 is green, a timer of
  zero is green, and the injected clock ignored is green;
  `internal/buzz/owner_sender_test.go:28` reads the store sorted by
  CreatedAt (`relay_test.go:103`).

### Ledgers, seals and recovery

- **rc.2 0e, done (#925, #930, #931):** the commit ledger can be downgraded
  or reinterpreted because its raw bytes are not sealed with schema-specific
  refusal reasons. Seal the raw bytes and refuse downgrades with distinct
  `newer_writer` and `schema_mismatch` reasons; #904 and #909 were to land
  after it (neither merged; #934 and #943 replaced them).
- **rc.2, done (#939, #940):** today identical refused bytes reuse their slot
  (`sameSlot` at `internal/seatcommit/quarantine.go:333`, and the reuse
  branch at `:213-221`), which allows the same-uid slot-reuse replay. A slot
  named by a recovery record must never be reused: identical bytes refused
  again get a new .k slot.
- **Done (#934):** `uninstall --write` deletes quarantine evidence without
  `--purge` because `internal/uninstall/plan.go` does not protect the
  quarantine directory. Protect it alongside the sealed and gate names.
  Source: `internal/uninstall/plan.go:78`. Since #934 `quarantine` is one of
  `sealedNames` (`internal/uninstall/plan.go:82`).
- **The evidence anchor's `update-ref`** (`internal/seatcommit/commit.go:839`
  at 6c881623; `:928` on e27dca44) should pass an expected-old value, so
  that it can never overwrite an earlier chain's evidence ref; today it
  passes none. (The #933 gate.)
- **An orphaned tamper block.** A crash between blockOnTamper's store write
  (`internal/scheduler/tamper.go:191`) and the tick's state write
  (`scheduler.go:537`) leaves a task blocked for an alarm no hold carries;
  today the lead runs `amq-squad task reset`. Fix: the hold is durable before
  the block, or a restart adopts blocks that name an alarm no hold carries.
  (The #933 gate.)
- **A retry of `scheduler recover` that finds its own interrupted recovery**
  (the new body under the old refused head) should complete the head
  instead of asking for a second decision; the rc.2 notes' known limit "A
  crash between the recovery's two writes" says what happens today. (The
  #933 gate.)
- **The admitted successor's head** (0e's item, not the 0c-B port's; from
  t129's block on R15): after a write that stopped before its head under a
  raw head, the next locked writer derives its predecessor from the head
  (`sealchain.WriteRawBeforeHead`), so it writes a new N+1 over the admitted
  N+1. The chain stays consistent and the body carries its content forward;
  what is lost is that the crashed successor's own sealed link is never
  completed in the head chain. Rule for 3.2.x: the next locked writer
  completes the admitted successor's head first, then writes its own, so
  every admitted body gets its link.
- **One migration record per chain**, keyed by the handle, since a chain
  migrates once; the second decision after a crash then records nothing new.
  (The plan reviewer's ruling 3 on #937; the duplicate migration seqs are
  admitted for rc.2 under Limits in B-recovery.md.)
- **The listing-before-the-mark interleaving** (a refusing reader lists slot
  X before a recovery marks it): read as no defect by the lead and the
  reviewer on #939 (2026-11-10); measuring it needs an interleaving seam;
  3.2.x by name if a later review wants it.
- **A typed code for a clean on a ledger refused as `newer_writer` or
  `schema_mismatch`** (the #909 port answers with text only, pinned by P6).

### Source guards and test tooling

- **"the source guards scan function bodies only"** (the #940 gate): one
  entry naming all three guards (the reader guard in
  `internal/sealchain/reader_guard_test.go`, the decision-log guard, the
  recovered-mark guard `recoveredMarkReferenceProblems` in
  `internal/seatcommit/quarantine_callers_test.go:337-342`) and one fix, in
  the reviewer's words: "a shared scanner that walks GenDecl initialisers and
  package-level function literals, used by every source guard." Measured on
  #940's head: a package-level alias of `quarantineRecovered` and a
  package-level function literal creating the mark both pass the guard. This
  replaces the per-guard entries where they say the same thing.
- **The raw-writer guard's parser limits**
  (`internal/seatcommit/quarantine_callers_test.go`): it reads function
  bodies only, so a caller in a package-level initialiser or through a dot
  import is not seen. (Codex's pass on #937; outside T5 as briefed.)
- **"the open trace has no positive control"** (the #940 gate): with the open
  trace point removed (`internal/seatcommit/quarantine_unix.go:172`) no test
  fails; T11's unmarked control asserts a read trace only. The static guard
  rejects an open of the mark (`quarantine_callers_test.go:292`).
- **The P12 guard gap** (the #961 gate): `sealedBody`
  (`internal/sealchain/sealchain.go:228`), `RawSeal` (`raw.go:57`) and
  `rawSealFields` (`raw.go:65`) are in no key guard's list. (Also by Codex on
  #962: no guard checks what a listed row function asserts.)
- **Guard round 4 (test tooling), moved from rc.2, 2026-11-10.** Brief:
  brief-320-guard-round4.md.

### Rollback and `up`

The rollback note is
[docs/v3-spike/320-rc2-rollback/execution.md](v3-spike/320-rc2-rollback/execution.md),
as #962 corrected it.

- **rc.2 0f, moved from rc.2, 2026-11-10, confirmed by P13 on 487e8560:** the
  routing decision log's tamper rows accept only four kinds
  (`internal/routing/tamper.go:63-69`) and are tagged over a re-encoded
  struct (`:57-59`), so an older reader rejects rows with an unfamiliar kind;
  a field change can also break verification, because the tag covers a
  re-encoded struct. Ordinary verified decisions and the four known kinds are
  still accepted (`internal/routing/chain.go:161-172`). Tag the raw row bytes
  and tolerate new kinds during verification; 0f lands before any new
  routing-log row kind. 0f row tags were moved to 3.2.x on the condition that
  rollback round 2 showed no old build raising a tamper alarm on a verdict,
  snapshot or outcome row. P13 (p13.sh on the two tags' archives, 16 of 16
  exit 0, no alarm or refusal either way, 0 differing inventory lines)
  confirmed it: 0f does not return as an rc.2 gate. Brief:
  brief-320-0f-row-tags.md.
- **P-1 (i):** `up --binary` to a build before `FirstRawLayoutVersion` is
  refused while a scheduler is live on the session (the lead step's lead
  record and launch record already tell `up` so), or the check moves after
  the lead step. (The reviewer wrote "step 5"; `up.go:231-237` has seven
  steps and the lead step is the fifth.) Until then the rc.2 notes' known
  limit says to stop the team before `up --binary` to an older build.
- **P-1 (ii):** a mismatched pair is re-read once before it is called legacy.
- **P-1 (iii):** P-4's two calls (`checkLedgerTarget` `up.go:379`,
  `checkLedgerDowngrade` `:391`) folded into one read.
- **The P-2 row:** "a planted unreadable commit or exits document whose head
  is present refuses every `up --binary` and `--skip-install` target, current
  and later included, with ledger_layout_unreadable". (By Codex's pass on
  #962: the document is read only for a head that is listed, `up.go:422`,
  `:443`, `:453`.)
- **P-3 words:** doctor's detail names the file that failed (head or
  document) and says "could not be read" for a read error, "do not verify"
  only for a verification failure (`internal/cli/doctor.go:390-392`).
- **"a wrong-tag predecessor beside a legacy head" has a row** (the matrix's
  "wrong sequence" fixture changes the sequence only,
  `internal/cli/up_raw_layout_test.go:85-87`).
- **"P5/P6 probes assert the per-tick counts, the status, the appended
  kinds"** (today they log them; the note cites the lead's rerun).
- **The alarms file has older-reader rows:** v3.2.0-rc.1's and v3.1.2's
  `uninstall` plan (`internal/uninstall/plan.go:330-343` in both, which
  tries each name in the commits directory as a commit ledger) and `status`
  (`internal/status/collect.go:491-515` in both, which reads only names that
  end in `.json`) are run on a session that holds
  `commits/<handle>.alarms.jsonl`, and a row says what each prints. (Placed
  by the plan reviewer on #963, 2026-11-11. Today there is a reading of the
  two tags only, the lead's and Codex's; the rc.2 notes' known limit 13 says
  so. The comment at `internal/seatcommit/alarms.go:24-25`, "No released build
  reads it", is inexact for that `uninstall` read and is corrected with the
  rows.)
- **An old `status` and an old screen read a `ledger_layout_migrated`
  decisions line** and a row says what they show. (Placed by the plan
  reviewer on #963, 2026-11-11. P5 has the old processes ticking over a
  decisions file that holds two such lines without error; no row has an old
  `status` or screen reading one.)

### Cancel and clean

- **"the superseded drop records cleaned_at though no clean ran"** (the #909
  port's Addendum 3): `dropSuperseded` (`internal/scheduler/exits.go` at
  3d984371) calls `MarkAttemptCleaned`. The reviewer's words: "the drop should
  record \"superseded, nothing cleaned\" on the attempt rather than
  cleaned_at, since no clean ran and the next preparation's archive is what
  makes the worktree safe; a store field that says cleaned when nothing was
  is the same shape as the superseded sentence 3d984371 already corrected."
  The port records it in row D2 and one Limits sentence and changes nothing.
- **From the #909 port, only if its rows P10/P11 hold without a fix:** a
  direct claim outside a live scheduler after a failed clean (C5b closes it);
  the exits matcher ignoring the attempt, beside the survey's candidate 8.
- `CancelCommit` can launch an extra seat outside roster mode when its wait
  is zero. Prevent that launch at `internal/schedproc/committer.go:131`.
- **Done (#943):** the roster plan can reject a clean worktree as `not
  uptodate` because of stat-only index drift in `MIGRATION.md`, seen on
  2026-11-05 and 2026-11-07. Refresh the index before deciding that the
  worktree is dirty. (Roster planning now refreshes a stale index and
  refuses a dirty seat worktree in plain words.)

### Leases, renewal and seats

- **rc.2, done (#932):** upgrade normalisation can end a live legacy attempt
  on the first scheduler tick. Stamp its budget end as the later of grant
  plus the default budget and now plus one lease length. Source: the legacy
  rule in `docs/v3.2.0-release-notes.md:102-110`.
- **rc.2, done (#952):** the first-renewal decision uses `Renewals == 1`, so
  a manual renewal can consume the marker before the attempt's first
  activity. Key the decision on the first activity renewal and label every
  renewal as `activity` or `manual` in the ledger.
- **rc.2, done another way (#952):** the task doorbell reads Codex hook trust
  at launch, so trust added mid-session does not remove the manual-renew
  instruction from later tasks. Read hook trust at dispatch for every
  doorbell. As built, trust is still read once at the seat's current launch,
  and a tool result row written by that same handle since the launch also
  removes the fallback (`docs/v3-spike/320-renewal-ab/execution.md:24-27`).
- **"A decision append error is logged once"** (the #949 gate): the hook
  ingest ignores the error of its decision append
  (`_ = schedproc.AppendDecision(...)`, `internal/cli/hook_ingest.go:151` on
  81f60af0), so a first-activity decision that fails to write is lost
  silently.
- **Every ring of an unproven Codex seat scans its whole hook ledger**
  (`HasActivitySince`, `internal/ledger/ledger.go:40` on 81f60af0; #949's
  finding 9). A bounded read (from the launch offset, or newest first) is the
  candidate.
- **rc.2 C5, moved from rc.2, 2026-11-10 (C5a PR 1):** Codex seats pin
  `writable_roots=[]` at `internal/seatexec/seatexec_unix.go:562-571` (on
  e27dca44 the pin is set at `internal/seatexec/codex.go:29` and required at
  `internal/seatexec/seatexec_unix.go:575-597`), so a seat cannot drain or
  claim without an approval prompt and the terra seat gives up. Add only the
  seat's own mailbox directory and the exact lock paths required by `amq
  drain` and task claim; never make the session root,
  `extensions/amq-squad/`, or another seat's mailbox writable, because that
  denial protects sealed state.
  - C5a PR 1 (the trusted harness files out of the agent directory), moved
    from rc.2, 2026-11-10. Alone it changes nothing the owner sees; the
    known limit is the rc.2 notes' sentence on a Codex seat's drain
    approval. Brief: brief-320-C5a-v2.md.
  - A fact for C5a (measured 2026-11-11 at t153's block; the reviewer: "it is
    the same boundary C5 is about"): a carried task needs the worktree index
    writable, and a seat worktree's index lives under the main repository's
    `.git/worktrees/<seat>`, outside the Codex seat's writable root.
    `git update-index -q --refresh`, `git apply --3way` and
    `git add -A && git write-tree` each need escalated permissions there;
    inside the sandbox the refresh exits 128 in under a millisecond with no
    output.
- **F1, one long call loses the lease** (t141's lease expiry, placed by the
  plan reviewer 2026-11-10). The lease is 10 minutes and only a finished
  tool call renews it; a seat's single 22-minute command cost the seat, a
  roster relaunch and the attempt's work (t141, 15:52 IST). NOT a 3.2.x code
  item: (i) words now: THE CALL RULE in every seat brief's gate lines ("your
  lease is 10 minutes and only a finished command renews it; no single
  command runs longer than 8 minutes"), and the doorbell sentence with its
  row inside the renewal task (row B7 of
  `docs/v3-spike/320-renewal-ab/execution.md`); (ii) rc.2 Known limits, one
  sentence with the cost named; (iii) the fix is 3.3.0 section S (S-call). It moves to 3.2.x
  only if a seat loses a lease again under the call rule before rc.2 ships,
  and the reviewer says so then.
- **F2, close does not end the seat's processes.** After `seat_closed` the
  seat's command kept running in its worktree (pid 89034, 14 minutes). The
  fix is 3.3.0 section S (S-close). No row in the rows round.
- **THE STANDING LEAD CHECK** (in force from 2026-11-10 until S-close lands),
  the reviewer's words: "before any clean, relaunch or new attempt on a
  worktree, the lead lists processes whose cwd is in it (lsof or ps) and
  waits or ends them by name, as you did today."
- **"run.lock on a filesystem without flock lets two schedulers run; refuse
  or warn once"** (the reviewer's ruling on #944, point 1). Source:
  `schedproc/process.go:101`, `flock.ErrUnsupported` falls through without a
  lock (read, not measured). It is the one way a live cancel marker of
  another scheduler can be met, which is why the reservation's guard stays.
- A seat launch can hang indefinitely while fetching from origin because the
  fetch has no time limit. Reuse the bounded runner already provided by
  `seatcommit`.

### The merge train

- `CompleteBound` can overwrite a newer merge-train head. A seat commit
  result cached at head O can reach `CompleteBound` after the train has bound
  head H, and there is no admission or from-head check; `seat commit` also
  binds a PR directly. Route both bindings through the train's admission and
  from-head check, or refuse the older head. This must land before the train
  lands a PR whose task still has a live seat attempt. Until it does, the lead
  checks before every land that the PR's task has no live lease and no queued
  commit result. Sources: the #903 review,
  `internal/task/scope.go:131`, `internal/cli/seat_commit.go:123` and `:221`,
  and `internal/scheduler/scheduler.go:741`.
- Three small fixes remain from the #903 merge-train review.
  `definiteRefusal` should match only gh's ` (HTTP 4NN)` suffix on the last
  stderr line; `headAdmitted`, `trainHeads` and `journaledBases` should compare
  repository names with `EqualFold`, as the store does; and `trainHeads`
  should report that the journal read failed when `Audit.Read` fails instead
  of suggesting a seat push. Sources: #903 and
  `internal/mergetrain/update.go:109`, `audit.go:192`, `audit.go:213`, and
  `rebind.go:208`.
- **Answered (#953, the 320-train-escape-legacy PR):** the three records under
  `gates/clear-*.json` in the v31 session that have failed gate-record
  validation (`gate record is malformed`) since 2026-11-05 17:20Z were written
  by the 3.0.x seat clear's owner gate, kind `quarantine_clear`: the kind was
  added by a724b3db and left the catalog in fdfa28ff. The drift reader now
  skips such a record as legacy with one line, and the train clears the alert
  it left behind. Evidence:
  [note.md](v3-spike/320-train-escape-legacy/note.md).
- **Done (#927):** a branch-update merge conflict was reported as
  `strict_required_status_checks_policy`, which hides the actual refusal from
  the lead screen and audit. Give this refusal its own `update_conflict` code.
- **rc.2 items 5 and 6, done (#927, #929):** the merge train never raises an
  owner gate whose "yes" cannot be acted on safely. A strict refusal, a
  branch-update conflict and a PR already merged at another head each end in
  words with no gate, and an accepted legacy gate record never lifts them; a
  read error during reconcile that is not definite (bare EOF, 5xx,
  transport, deadline, killed `gh`, unknown wording, and exact HTTP 4xx
  other than 401, 403 or 404) writes `reconcile_read_failed`, skips that PR
  and the run continues, except a non-definite Checks error in `waitCI`
  inside `reconcileUpdates`, which ends the run in the recovery words. See
  the audit table below.
- **rc.2 item 7d, done in the rc.2 notes (known limit 11):** the rc.2 known
  limits re-state that the merge train's dry run still appends its audit
  entries (flagged `DryRun`); making the dry run read-only is 3.2.x.
- **"a train dry run writes nothing under alerts/"** (the reviewer's name,
  #953 ruling point 3): today the drift pass runs before any dry-run check
  (`train.go:290-299` on c65a709c) and a dry run writes and removes alert
  files.
- **"escape bytes in a title or body: gh refuses today; a sanitised path
  before any flag"** (the other gh call sites: the lead read 16 in
  `internal/mergetrain` and 12 in `internal/releaseflow`; the train-fix PR
  #953's note re-counted `internal/mergetrain` as 18 other sites, so the count
  here is 18, not 16).
- **The `gate_store_unreadable` alert's overstated words:** "so no gate in it
  clears an alert" is written for a per-record failure, where the reader goes
  on past the bad record (`drift.go:259-264`) and the other gates in the
  store still clear.
- **"the unknown-flag-twice row tells the two errors apart"**
  (`internal/mergetrain/escape_legacy_320_test.go`, the "an unknown flag
  twice" subtest; both fake answers carry the same words, so `note.md:120`'s
  "the second error is the one returned" is not shown by that subtest).
- **"a symlinked gates directory has a row"** (drift.go item a: Lstat in
  place of Stat is not caught; a `gates` symlink to a real, listable
  directory has no row).
- `docs/v3-spike/311-evidence/311-followups-revert-runs.py:162` quotes the
  `alert_cleared` line of `train.go` as it was before #953; that case of the
  script no longer finds its text (#953 ruling, out of that PR's set).
- The kind list in the comment above `Entry`
  (`internal/mergetrain/audit.go:56-66`) names neither `note`, `alert` nor
  `alert_cleared` (#953 ruling, out of that PR's set).
- Plan text (item 7b): the merge-train plan still prints the run header, the
  raw `ci_app_unbound` note, short heads and rule names first
  (`internal/cli/merge_train.go:317-362`); put them in words with the codes
  below.
- Cap-off (item 7c): `size_cap_lines` and `size_cap_files` accept no off
  value (`internal/grants/parse.go:258-261`); add an explicit off with the
  growth gate following it.

### Control bytes and status text

The control-bytes note is
[docs/v3-spike/320-control-bytes/README.md](v3-spike/320-control-bytes/README.md),
as #960 corrected it.

- One `amq-squad status --json` response immediately after a scheduler start
  at 2026-11-07 16:46Z (as reported in the lead's run report) contained a raw
  control character and `jq` rejected it; six later runs were valid. The
  command writes through `json.NewEncoder(os.Stdout)`
  (`internal/cli/status.go:80-82`), which escapes control characters, so the
  likely cause is another writer interleaving with stdout; that is a
  hypothesis. Capture the failing bytes next.
- **Entry checks** (the reviewer's #958 ruling, 2026-11-11 09:31 IST): entry
  checks for control characters where a pool, a declared file or a root
  enters (`ParsePool`, `CleanDeclaredFiles`'s C1, `up`, `fleet add`) and
  `actor()`; holders already on disk.
- **What the control-bytes trace names for 3.2.x, by name** (the reviewer's
  #959 ruling, step 2c and the trace): the shared plain-text rule at the
  phone-bound publish boundary (`internal/buzz/relay.go:435`); the seat
  doorbell and lead notice (`internal/schedproc/amq.go:72-122`);
  `printTaskResult` and the other task verbs' results and refusals; `up`'s
  decision outcome and refusal; the scheduler's stdout lines that bypass
  status; the merge train's human output (`printMergeTrain`); the fleet
  envelope's own fields; the decisions log's two human readers; and "reserve
  it at the verb" for every block code the table marks so (rc.2 reserves
  only `ledger_tampered`). Reserving `outside_declared_files` changes a
  product test's row (`internal/cli/lead_tools_proof_test.go:218`, a seat
  blocks its own task with it today).
- **The reserve-at-the-verb item's words** (the reviewer's #960 ruling,
  point a): "reserve at the verb every name refusal.go classifies that a seat
  has no reason to type", so it covers the eleven names
  `internal/routing/refusal.go:18-71` classifies that no product code writes
  as a block code (the list is in the control-bytes note), not only the
  table's rows marked so.
- **(Conditional on the #955 follow-up's trace)** "a warning's record reaches
  the lead screen without a control-byte filter (screen.ts:262-266,
  words.ts:700-706; classify.go:455 redacts only)". Stands as a 3.2.x item
  only if the follow-up's read-only trace finds no path for seat-written text
  into a warning's record; if it finds one, the path goes to the plan
  reviewer before the follow-up's PR merges.

### The lead's screen

- The lead screen's "waiting on you" section lists only open gates, so it can
  say "Nothing is waiting on you" while a lead-routed stop is open. #904's
  phase two was to list the tamper alarm there; #904 did not merge (#934
  replaced it), so the tamper alarm is not listed there either. Every
  lead-routed stop kind should be listed there.
- **Done (#955):** (Low priority) Write a decision line per task wait change,
  so a wait is on record when the screen cannot show its row. (A wait change
  now writes one `wait_changed` decision.)
- Stall marker (item 7a): the seat warning comes from `.wake.dialog`'s
  existence alone (`internal/status/screen.go:239-240`); clear it when the
  prompt is answered, or use a liveness signal.
- **"The CSI suffix scan is bounded"** (#950's finding 11): the carried
  quadratic suffix scan in `pi/extensions/amq-squad/width.ts` (`:33` on
  fcd95493).

### Routing and tasks

- **Routing: escalation climbs effort before the model** (M1 of the model
  triage). Its own 3.3.x brief; it changes #876's escalation rule and the
  owner's cost shape; needs a row that the attempt counter and the
  MaxAttempts block are unchanged. (Reviewer, 2026-11-09.)
- **Routing table refresh after S1** (M2 the advisor chain's Claude entry, M3
  the plan tier). 3.3.x. (Reviewer, 2026-11-09.)
- **The weekly report's failed label** (`routing/router.go` `failedOutcome`)
  against the router's count (`routing/refusal.go`); S1's declaration makes
  them one.
- **The state machine survey's remaining candidates by name**, those not
  taken by S4 in 3.3.0.
- Not on this list (3.3.0): M4 and M5 of the model triage ride with B0a/B0b
  (Claude prices in the default table; cost per completed task per pool in
  the weekly report).
- `task add` accepted a plan task pinned to the Codex terra tier even though
  the registry could never resolve that class and pool, then left it blocked
  as `pin_unresolved`. Refuse an impossible class and pool pair at add time
  and explain the mismatch in words.
- **`task add` accepts a pin the router then refuses in the same second:** the
  task reads "pending" first and the `pin_unresolved` block comes after (t154,
  2026-11-11; an implement task with more than three declared entries goes to
  the plan tier, `internal/routing/table.go:127-130`, `router.go:514`). The
  add should refuse, or say so at once.

### Buzz and AMQ

- **A verified owner answer never arrives through remote attach** (one
  sighting). Locked make ci on 6b519c9e (PR #929), 2026-11-09: internal/buzz
  `TestOwnerScriptDryRun` hung at `rehearsal_test.go:190` ("the attach
  verifies the thread answer"); the go test gate passed the same test on the
  same head and the make ci rerun passed. It fails closed (the gate expires
  and is asked again). First task: a count-N reproduction, starting from the
  two facts that come before the silence: the third serve's "ingest ...:
  edit waits for the clock to pass the previous edit's second" and the
  presence going stale at 21:30:56Z. It moves to 3.3.0's A lane only if it
  reproduces, and then A0 cites it before A1 builds on that path.
  (Reviewer, 2026-11-09.)
- **`--poll` for the Buzz endpoint**, a candidate only AFTER Aviv answers the
  upstream report on the reconcile-tick race: amq-squad starts the endpoint
  with no `--poll` (`internal/buzz/endpoint.go:108`), so amq-remote's 500 ms
  default applies. A longer interval: "it narrows the window, does not close
  it, slows the import" (the reviewer's words). The race is measured on AMQ
  0.116.2 and on v0.138.0 (the lead's runs, 2026-11-10: at 5 ms a request
  refused in 4 of 9 runs on v0.138.0), and the same code is read in v0.134.0
  and on main at 71ef0f47; the fix belongs upstream and rides the AMQ lane of
  3.3.0. (Ruled onto this list on #945's packet, point 5.)
- **The AMQ pin** (2026-11-11): AMQ v0.139.0 removed `launchapi.Inspect`; the
  pin cannot pass v0.138.0 until 3.3.0's R0. The rc.2 notes carry one
  sentence on the supported pin.
  Corrected 2026-11-12 (the rc.2 soak's record): the rc.2 notes' sentence
  says the seat liveness check calls `launchapi.Inspect`. It does not: the
  line that names it (`internal/cli/liveness.go:209`) and the call through it
  (`:247`, which names `launchapi.InspectRequestV1`) are in a liveness
  classifier that no command reaches (`deadcode` on 8d66f50e; the rc.2
  soak's record has the run). They were removed on v3 by #969; the 3.2 line
  keeps them, and at its pin v0.116.2 they build. The corrected sentence
  goes into the 3.2.0 notes with the release PR.

### Evidence tooling

- **An identity check beside `scripts/redact-evidence`** (ruled onto this list
  on #945's packet, point 5, as written): redact-evidence looks for
  credential-shaped values only. On t142's carried logs it flagged nothing,
  while two logs carried the live seat's AM_ROOT and AM_SESSION values (8
  lines) and the live mail root (6 lines). By its own rule the tool missed
  nothing. The item is an identity check beside it (the six AM_* names with
  a value, the live mail root).
- **redact-evidence passes ESC bytes in evidence logs unflagged: strip or
  flag.** (The five poll-5ms run logs carry 21 ESC bytes each; they stopped
  the train on #948.)

### Process and briefs

- **rc.2 item 8, done (#921):** the cut procedure (RELEASING.md and the
  orchestrator skill) names the publish as the lead's own step; the release
  agent prepares and checks, never publishes; a refused step goes back to
  the owner, never to another session.
- **A hand cut's `release prepare` still posts the gate questions** (seen
  at the rc.2 cut, 2026-11-11; the plan reviewer's item): the owner chose a
  cut by hand with the two gates not answered, and the prepare posted both
  questions to his Buzz channel with a 30-minute window and no attach to
  accept a reply
  (`docs/v3-spike/5-release-evidence/release-v3.2.0-rc.2/gate-questions.txt`).
  Either a prepare for a hand cut suppresses the questions, or the notes'
  owner steps say the questions arrive and need no answer.
- **The notes' prerelease step assumes the flow:** the owner steps say to mark
  the prerelease after the publish because the flow never does; a hand cut's
  `gh release create` takes `--prerelease --latest=false` itself. Say both.
- Briefs that carry another PR's commits conflict after the merge train
  rebases and lands the carried PR, as #907 did after #901's `12d1a8ba`
  landed as `27fa44f0`. Make briefs depend on that PR instead of carrying its
  commits.
- **The carry brief template names the escalated commands** (the three
  index-writing git commands of a carry), with "any other command that needs
  it is a `task block`".
- The Makefile's `fmt-check` can use a newer host `gofmt` whose alignment
  differs from CI's Go 1.26, because `GOTOOLCHAIN` does not switch the
  standalone formatter. Resolve `gofmt` from the toolchain selected by
  `go.mod`.
- Whole-module tests, `make ci` and `npm test` from different seats compete
  for one host and create load-sensitive failures. Have the harness serialise
  these seat gates, as the lead's agents already do with a host gate lock.

### The 3.1.2 plan's eight items, audited against origin/v3 (2026-11-07, at a4dff16c)

Draft 2 of the 3.1.2 plan (2026-11-05) listed eight items. 3.1.2 shipped as the hotfix #883 without most of them; 3.2.0's work since then carried three. Each row is read from origin/v3's code.

| Item | Status | PR | Where it lives, or what still behaves the old way |
| --- | --- | --- | --- |
| 1. Codex leases renew from the seat's activity, bounded by the task budget; #876's rule kept | Landed | #891 | `internal/cli/hook_ingest.go:121` (`renewLeaseOnToolResult`) and `:160`; `internal/task/lease.go:167-168` (the budget cap), `:417-434`; `internal/routing/refusal.go:73-76` (an expired lease counted, never escalated); `TestActivityRenewalIsBoundedByTaskBudgetOnBothPaths`; live row proven on t109, recorded in the rc.1 evidence (`docs/v3-spike/5-release-evidence/release-v3.2.0-rc.1/`, #916: renewal-row-t109.log and renewal-decisions-t109.log); `docs/v3-spike/320-evidence/activity-renewal.md:190` and the rc.1 notes predate it. Open: the two rc.2 renewal items below. |
| 2. `task claim ID` checks the task's pool | Landed | #901 (carrying #899) | `internal/task/lease.go:121-137` (`requireServingPool`, `claim_pool_mismatch`, `claim_pool_unknown`); `internal/cli/task.go:551-579`; `TestClaimServingChecksAConcurrentRepinInsideTheClaimUpdate`. |
| 3. The lapsed-holder skip resets on relaunch | Landed | #901 | `internal/scheduler/scheduler.go:1003-1007` (skip only for attempts granted since `joinedAt`), `:559`; `internal/scheduler/roster.go:132-140` (adoption keeps the launch anchor); `TestLapsedHolderIsEligibleAgainOnlyAfterItsCurrentJoin`. |
| 4. C5, the seat's approvals policy and writable roots | Not | none | `internal/seatexec/codex.go:29` still emits `writable_roots=[]`; `seatexec_unix.go:561-567`, `:575-576` require it; the seat launch (`internal/seatlaunch/compile.go:111`) carries no auto reviewer and no trusted choice, unlike `internal/cli/agent_defaults.go:10-11`. The bypass flag is already refused (`seatexec_unix.go:330`). The writable roots are C5a and C5b (rc.2). The trust choice (the auto reviewer, or the trusted path when the owner chose it) stays out of both: it meets the auto-reviewer escalation residual, so it goes to the owner as one A/B once C5b lands, not into a brief now. |
| 5. Train: a transient read error skips one PR; a PR merged at another head is refused from the plan | Not | none | `internal/mergetrain/train.go:907-910` and `:338-339` end the whole run on a read error; a PR merged at another head is audited and skipped (`:1004-1010`, `:919-920`), then refused `pr_state` (`checks.go:88-89`) with a `grow merge` owner gate (`refuse()`, `:487-523`) on every run. |
| 6. The strict refusal is not liftable by an owner gate | Not | none | `train.go:474` adds `strict_required_status_checks_policy`; `refuse()` raises the `grow merge` gate (`internal/gatetarget/gatetarget.go:136`) and an accepted gate lifts it (`train.go:490-495`); no wording says no gate overrides GitHub's rule (`internal/cli/merge_train.go:357-360`, `words.ts:611`). On 2026-11-07 the owner approved such a gate for #907; it was never used (`gate-merge-907.json` in the rc.1 evidence, #916). |
| 7a. The stall marker (C4) | Not | none | `internal/status/screen.go:239-240` warns from `.wake.dialog`'s existence alone; the seat's Stop hook clears it at turn end (`internal/seatexec/codexhook.go:59-63`), and whether AMQ's own hook clears it on any other event is not verified here. |
| 7b. The plan text's code lines | Not | none | `internal/cli/merge_train.go:317` (run id and full SHA header), `:318-319` (the raw `ci_app_unbound` note first), `:342` (short heads), `:362` (rule names first). |
| 7c. An explicit cap-off for size_cap_lines/files | Not | none | `internal/grants/parse.go:258-261` (minimum 1), `docs/squad-yaml.md:241-242`; no off value, so a very high cap silences the growth gate. |
| 7d. The dry run's audit entries read-only or stated | Partly | #877 (stated only) | Stated as 3.1.1 known limit 12; the code still appends them (`train.go:264-271`, `:282`, `:557`, `:380`), and rc.1's known limits do not repeat it. |
| 8. The cut procedure names the publish as the lead's step | Not | none | `RELEASING.md:29-63` names no actor for steps 2-6; the orchestrator skill (`plugins/skills-src/orchestrator/SKILL.md:151-162`) and `docs/skills.md:190-192` say only that publish runs under the owner's gh outside any seat; `README.md:509-510` says only that publish needs verified owner answers. |

Where each open item goes: rc.2: 4 (C5, split into C5a and C5b), 5, 6, 8, and 7d's re-statement in the rc.2 known limits. 3.2.x: 7a, 7b, 7c, and making the dry run read-only (7d).

## 3.1

**Direction for 3.1 (owner, 2026-10-30, in his words):** "we want to copy
claude agents team but with more binaries and models, and experience needs
to be seamless; the other main objective is able to work remotely via buzz
or grok bot." Measured against the first day of use, the seams are: a team
needs git and four commands before it exists (the documents squad and the
solo-lead-to-squad bridge below); the pi lead runs local models only (the
cloud entries below); the phone is one-way (agents' replies do not reach
Buzz; a Grok bot face does not exist). Each 3.1 brief names which seam it
closes.


- The team rules and a seat added live (31-2 PR 2, the plan reviewer's
  line of 2026-11-01): `team apply --add-seat` keeps
  `.amq-squad/team-rules.md` as the owner approved it, so the rules do not
  carry a seat added live until the next `team propose`. The rules should
  list the seats from squad.yaml when they are read, not by hand at render
  time. The same root as day 3's row that the rules named the lead `cto` as
  a literal.

- A documents squad (owner, 2026-10-30: "why is git strict if the role is
  non-coding? I can also trigger a team in a PM folder"): in 3.0 a team
  needs a git repository because the worktrees isolate seats and the commit
  step is the evidence of each seat's change; the work being documents
  changes nothing in that. Today a `git init` with no remote is enough: the
  commit step commits and does not push (push_url_missing is a note, not a
  refusal). 3.1: a squad mode for non-code folders that keeps a repository
  underneath for isolation and evidence without the owner setting it up or
  seeing it, with the seats' output presented as files and messages.

- Linux seats (platform decision 2026-10-29, plan 16.4; CI run
  40955238412). (a) The launch's proof socket binds and dials through
  descriptors on both ends (D1's pattern, the proof-side twin): on a Linux
  host with a project path over the sun_path bound the launch's proof fails
  (helper_dial_failed, no_proof) and no seat starts. (b) Identity on Linux:
  ext4 and tmpfs reuse an inode right after unlink and recreate, so a
  same-UID replacement of the routing seed, of the proof socket after the
  probe, or of an anchored directory passes the dev+inode check that exists
  to catch it; hold the descriptor and fstat it instead of re-stating the
  path wherever the check guards replacement, and where a path must be
  re-stated add statx birth time (stx_btime) beside dev and inode; the three
  sites are the seed, the proof socket after the probe, the anchored
  directory. (c) The cancel and close rows (the cancelled seat still runs;
  an unconfirmed close reported cancelled): process semantics on the
  runner, pid start stamp or signal timing; a Linux VM is the first step.
  (d) gatestore TestSquadIDIsCanonical, likely /tmp against /var in the
  fixture; a test fix that rides with the rest.

- Read-only runtime image for the embedded pi runtime (the hash-to-exec
  window, milliseconds, same-UID only). Owner decision 2026-10-25 on launch
  speed. Plan 4.1 and 16.4 (hardening).
- GitHub token custody: a dedicated amq-squad keychain item with a
  fine-grained PAT, or a GitHub App installation token; 3.0 uses the owner's
  gh CLI login through the pinned gh copy. Owner decision 2026-10-25. Plan
  R33 and 16.4.
- A typed release-body digest field on operatorauth.ReleaseSpec
  (internal/operatorauth/release.go): 3.0.2 records the --notes-file
  digest in the one note field (Note.Summary, "release body sha256:<hex>",
  written by prepare only, exactly one parsed at publish). Move it to its
  own field when the spec shape changes anyway (reviewer ruling,
  2026-10-31).
- One helper for the push's authentication: the commit step's
  pushCredentials (internal/seatcommit/commit.go) calls
  seatcommit.FetchCredentials (internal/seatcommit/fetchcred.go), which the
  launch's base fetch uses since 3.0.2, so the push and the fetch cannot
  drift. 3.0.2-t22.
- The create-to-bind window of the commit step's PR: GitHub creates the
  draft PR and the process dies before BindPR persists the binding; the
  next attempt sees an open PR bound to no task on the task's branch and
  refuses pr_bound_elsewhere instead of binding it again. The fix (the
  reviewer's shape): an open PR whose branch equals the task's own branch
  name (compared whole, never parsed) and whose head equals the commit the
  ledger records as pushed for that task is this task's PR, so the next
  attempt binds it again instead of refusing pr_bound_elsewhere. 3.0.2-t24.

## 3.0.x

- 3.0.2 leaves this open: `release publish` must still repeat prepare's
  `--notes` and `--notes-file`; when the notes file changed, the CLI's
  spec-change refusal (ErrSpecChangeRequiresSuccessor) fires before
  release_body_mismatch and does not name the field. The fix: publish takes
  the prepared spec from the series store and refuses only a body mismatch.
- Upstream ask 20 (AMQ): join a fresh participant into a present binding.
  AMQ v0.116.2 refuses a fresh seat launched into a session whose binding
  is present (`binding_present_without_resumable_conversation`,
  internal/launch/reconcile.go:1358), and its on_live keep join creates
  only a seat missing from the binding's resources (liveseat.go:58-72), so
  a seat that ran and is gone cannot be replaced. 3.0.1 t16 launches the
  roster in one apply and replaces a gone seat by relaunching the whole
  roster (`amq-squad scheduler relaunch`); with the ask, the scheduler can
  replace one seat in place. The lead files the ask.
- Conditional (3.0.1 t16 ruling 3): pi seats park like the others, on the
  rule that the model memory is the local server's (ollama's keep_alive
  unloads an idle model; the seat process holds none). If the owner run
  measures GPU memory held by a parked pi seat after the keep-alive window,
  close-on-park for pi seats becomes a 3.0.x item.
- Speed of launches and communication (owner, 2026-10-30, the first
  dogfood squad: "we need to speed up the agent launches and comms, it's
  taking too much"). Measured on the day: a seat launch to its first turn
  is seconds, but the path from "task added" to "seat working" ran through
  the trust confirmation (the mayor's start cannot carry it, t8), the
  quarantine cycle (each owner config change costs a signed clear), the
  first-run screens of a Claude seat (t6), the Codex folder trust per
  worktree, and the missing wake that leaves a seat idle after its first
  turn (t9); and every owner-side step was a terminal and a phone round
  trip. The 3.1 measure: time from `task add` to the seat's first tool
  call, and from a seat's reply to the owner's phone, both recorded per
  run; the targets are set when the first clean run gives the baseline.

- The 3.0 Claude plugin is unreachable through the public marketplace: the
  marketplace source is the repository (omriariav/amq-squad) and Claude
  Code reads its default branch, main, which carries v2.31; the owner's
  own install needed the marketplace re-pointed at the v3 checkout. Found
  2026-10-30 at the first Claude seat start. One choice for the owner,
  three options: (1) main carries 3.0 by the owner's merge on his
  repository, outside the standing approval; recommended (reviewer), as
  the first 3.0.x action, because a public marketplace that resolves to v2
  while the release says 3.0 misleads every installer; (2) v3 becomes the
  default branch (main stays as the v2 line under a new name); (3) a
  marketplace entry naming the branch, the stopgap only if Claude Code's
  marketplace format takes a ref, else the local re-point is the stopgap.
  Either way: a release-check row that fails the next tag closed when the
  marketplace's resolved ref does not carry the released version (3.0's
  release check missed it).
- Blocker for the train, not hygiene (reviewer ruling 2026-10-30):
  internal/redact TestBlockIsLinearOnALongLine bounds a 1 MiB line at 2 s
  of wall clock; the shared macOS runner missed it once (PR #800's first
  run, green on the re-run), and with make ci strict and no bypass actors a
  wall-clock bound fails the train at random. Fix before brief 0's PR
  reaches the train: the linearity proof as a ratio measured in one
  process (a 1 MiB line against a 64 KiB line within a 64x factor), no
  absolute bound. Task t2 in the v31 store.
- Closed by 3.0.1 t18 (PR #810, branch v3-301-t18): the buzz dry-run
  bound, the test-hygiene entry from 2026-10-29 (spike log).
  TestOwnerScriptDryRun waited on the serve loop with polling deadlines
  (five of 20 s among eleven, six more in its rehearsal) and one ran out at
  random under load. Each wait now blocks on what the serve loop emits on
  the test relay, one 5 minute budget ends a hung run and names the step,
  and no duration fails the test. Evidence:
  docs/v3-spike/5-301-evidence/t18-buzz-dryrun-signal.md.

- The pi lead on a cloud model (owner, 2026-10-30: "can I start pi and use
  a codex / claude model in it?"): the registry builds pi entries for
  Ollama models only (internal/registry/build.go); Anthropic and OpenAI
  models are entries of the claude and codex runtimes. pi itself speaks the
  Anthropic and OpenAI APIs with an API key, and brief 5-amq-A already
  carries a seat's declared credential (ANTHROPIC_API_KEY) into the pi
  process. Add pi entries for anthropic/<model> and openai/<model> when the
  key is present (credential_needs on the entry, privacy class and cost tier
  set), probed like the Ollama ones, so `amq-squad lead --model
  anthropic/<model>` runs the lead on a cloud model.

- Solo lead and teams (owner's first solo session, 2026-10-30, pi session
  01a18b83 under ~/code/amq-test on ollama/qwen3.6:27b): asked to "review
  this repo with two agents", the solo lead proposed a two-seat team in
  prose, then answered "why not using a team?" with "I'm solo here, there
  are no other seats" and did the review alone as two perspectives. The
  solo role prompt should say what the lead can and cannot start, and the
  lead should hand the owner the path that does start a team (team propose
  and apply with the seats it proposed, then the launch; or the mayor),
  instead of imitating one. Candidate for 3.1: the solo lead proposes a
  squad.yaml draft (`team propose --from`) so one owner confirmation turns
  the conversation into a squad ("the mayor proposes, the owner starts").
- Solo lead host facts: the lead guessed a repository path (/repos/...)
  before asking; the role prompt should carry where the owner's
  repositories live (the fleet grant's repository_paths, or ~/code) and
  the rule to ask for a path it cannot find.
- Local model latency: on ollama/qwen3.6:27b a turn with a long tool result
  took about two minutes; the solo lead should show a working indicator
  and the registry row should carry a measured turn time so the owner can
  pick a model knowingly.

- Solo lead first turn (owner observation 2026-10-30, the first human run
  of `amq-squad lead`): the lead's role instruction takes effect only after
  the owner's first prompt; nothing is said at start. The solo lead should
  open with its own first turn (who it is, which model, what it can do
  here) before the owner types, or the instruction should be applied at
  session start rather than on the first message.

- Install and self-update write to a temp name and rename, never a copy
  over the existing file: the macOS kernel kills a binary copied over an
  existing signed executable (the cached code-signature check, signal 9);
  a fresh inode runs. Seen installing 3.0.0 on 2026-10-30. The README's
  install step says so. Reviewer ruling 2026-10-30.

- The compound release store refuses a session root whose files carry a
  group other than the owner's primary gid: a scratch fleet under
  /private/tmp (macOS gives files there the wheel group) failed `release
  prepare` with "immutable temp identity invalid" on 2026-10-30. Accept the
  owner's uid with either the owner's primary gid or the gid the directory
  hands out (setgid-style inheritance is normal); the refusal names the
  directory and the gid it saw; a doctor row pre-checks the fleet root's
  group. Reviewer ruling 2026-10-30; owner-run.md 0.3.

- `amq-squad seat clear --rehearse` (or a script): build the quarantine
  fixture of the signed-clear live row (a scratch repository with a seat
  worktree, the staged launch record, the worktree base record, a grant
  naming a local bare origin, a task done by the seat, the owner config
  change) so the owner-run third item can run without hand-written core
  records. Owner session 2026-10-29; owner-run.md.

- Status liveness by launch-record identity, not bare signal-0: `status.Collect`
  (internal/status/collect.go:134) marks a seat `live` when its recorded
  `AgentPID` answers signal-0, while `classifyLaunchRuntimeIdentity`
  (internal/cli/launch.go:90) also checks the recorded binary or launcher and
  that the process was not born after the record. Failure: the lead dies
  without a `StoppedAt` and its pid is reused, so the lead's row stays `live`,
  the Buzz attachment's presence stays "present", and the owner's messages
  route to a dead lead. The fix moves the identity check where status can
  call it (the matcher lives in internal/cli) and gives Collect the probes
  as seams; the status tests that stub `PIDAlive` need the new seams. Found
  in 5-ci-D (D2, the Linux pid-42 fixture); deferred by diff size.
- Scheduler async close: no production implementer after the stand-in's removal (5-amq-C); CloseUnconfirmed's delegation always confirms; removal of AsyncCloser, the helper, the delegation, the awaitGone branches, state and rendering, and five tests, with holdForExit's exits release kept.
- Orphaned-verifier adoption: the exits queue entry for a running commit step
  records the verifier child's pgid and start time; the adopting scheduler
  kills that group when the start time matches and re-runs the verifier once
  on the same sealed snapshot (interrupted_reruns=1 in the sealed bytes); a
  second interruption defers to the lead as today. Spike log, phase 5
  hardening list; plan 6.1.
- Stale verifier waiting-slot rows: a waiting row left by a crash persists
  until the next step; status marks a row older than the wait bound as stale.
  Spike log, test-hygiene list.
- Test flakes still open after the hygiene branches: internal/seatlaunch
  TestProofConnectionsAreBounded and
  TestAmbiguousRequestIsAbandonedOnlyWhenNoPaneExists (TempDir cleanup
  races); the internal/cli hung-guard row was fixed on v3-5-hygiene2
  (merged c15ac65b). Keep the failing full-run output before any fix. Spike
  log, test-hygiene list.
- `amq-squad uninstall --json`: the verb has no JSON envelope. Plan 16.4
  (uninstall).
- YAML redaction trade-offs recorded in internal/redact's package doc: a bare
  multi-word value under a credential name is skipped by the one-line rule
  (the shape rules still run inside it) and `${VAR}` is skipped by the
  alphanumeric test; the per-line prose-scan narrowings (null and booleans,
  masked values, call expressions without an 8-plus alphanumeric run, the
  key `key`). Plan 6.1; briefs ruling 18.
- The Linux CI job for the Claude hook script (claude-hooks-linux on
  ubuntu-latest under dash) has not had a real run; it runs on the first push.
  Plan 16.4 (release prep).
- The amq-noc 1.0 command center lands by PR on amq-noc main on the owner's
  go, after its fixture refresh for lead_handle and live. Plan 4.7 and 16.4,
  owner session item 6.
- MIGRATION.md keeps its v2 sections; the "Moving to 3.0" section points at
  docs/squad-yaml.md and `amq-squad migrate`; a fuller v2-to-3.0 guide can
  follow once the first real migration runs. Plan 16.4 (migrate).
- 3.1, only if a pi lead ever takes phone approvals: ask 8 part 2 (PR #1254,
  the event id through the pi bridge). No 3.0 path takes a single tool
  approval through the pi bridge; typed gates go over Buzz with the owner's
  signature. Briefs ruling 20 point 5.
- The core's own body-key connection (gate question post, the per-answer
  read by id and history, the re-ask) goes when amq-remote offers a
  read-by-id and post surface on serve's connection. Ruling 20 point 1; plan
  R33.
- `/status` from the phone is now amq-remote's own command (request state);
  the 4-5 activity line (bucket counts, open gates) no longer reaches the
  phone. A plain-text keyword, or an upstream hook for a target-defined
  status, can bring it back. 5-amq-B.
- A hung verification is an `attention` line in `remote attach`'s output,
  not a row in the status document's attention queue. 5-amq-B.
- AMQ's startup hold misses every amq-squad seat (3.0.1 t9, a stated
  limit). coop exec decides its Startup marker from the command binary
  (internal/cli/coop_exec_trust.go:74-75 and coop_exec_unix.go:994 at AMQ
  v0.116.2), and a seat's command binary is the seat-exec wrapper, so no
  seat gets the hold, and a wake doorbell can reach a folder-trust, update
  or hook-review screen. Claude's first-run screens close with t6 (the
  seat's config dir born complete; claudeseat.StartupScreensClosed turns a
  Claude seat's wake on only then). For AMQ's hold under a wrapper, one of
  two: seat-exec names the inner binary to AMQ, or AMQ honours a binary
  the launch declares (an upstream ask). Until then a Codex seat's wake
  requires the Codex config's trust entry for its worktree, which rules out
  the trust screen only.
- Claude seat config digest (3.0.1 t6, reviewer ruling 2026-10-30): if
  the owner-run live row (docs/v3-spike/5-claudeseat-execution.md) shows
  that Claude Code 2.1.314 never creates a `projects` entry for the
  worktree's canonical repository root, tighten the `.claude.json` digest
  from "keys carrying trust or a permission input" to the bare `projects`
  key set, so any added entry moves it. If the entry does appear, record
  its keys there and keep the current digest.
- The Claude seat config checks are facts about the pinned Claude Code
  binary (3.0.1 t6): the bookkeeping sets pI and kI and the walk of their
  keys against the binary's permission readers. Today a re-pin's step is to
  re-extract internal/claudeseat/testdata/claude-*-bookkeeping.js and re-walk
  by hand (the execution note's method); make the re-walk a check, so that
  doctor's pin row refuses a pin whose sets differ from the code's.
- Claude seats and external CLAUDE.md includes (3.0.1 t6, fifth commit):
  when a CLAUDE.md the seat loads at start imports a file outside its
  worktree, Claude Code 2.1.314 opens its external-includes dialog unless
  the project entry approves the includes. claudeseat never seeds that
  approval, so such a seat's wake stays off and seat-exec refuses it
  (claude_external_includes_pending). Decide how approval is granted: an
  owner-signed per-worktree approval written into the seat's config at
  launch, or a host check that names the imports for the owner to move
  inside the worktree.
- Seats run with the owner's HOME, so `~/.amq-squad` is reachable by any
  tool a seat has; 3.0.1's doctor refusal keys on the seat environment
  marker, a fence against a brief's or an agent's mistake, not against a
  seat that unsets its environment. The real fence: a seat-owned
  `AMQ_SQUAD_HOME` with a read-only registry mirror. 3.0.1-t15.
- 3.0.2: `release prepare` checks only the shape of `--commit` (internal/releaseflow/releaseflow.go:116): a well-formed SHA that names nothing raises two owner gates (v3.0.1's void series). Resolve the commit before any gate: `git cat-file -e` locally and containment in the named branch on the remote, as TagCommit resolves the tag at :46; refuse with a named reason.
- 3.0.x: `remote attach` does not start the amq-remote endpoint (`amq-remote up`, the enrollment script's step D) and does not notice it gone; moving the attach between sessions on 2026-10-30 left the owner's gate answers undelivered until the endpoint was restarted by hand. The attach checks for a live endpoint (pid file and socket) at start and each interval and refuses or says so; it never stops one.
- 3.0.x: `release publish` must repeat the prepare's `--notes` exactly (the notes are part of the release spec); the refusal "release spec changed" does not say which field moved. Publish takes the prepared spec from the series store and refuses only a mismatch.
- 3.0.x: `release prepare` on a session whose root does not exist computes the squad identity before it creates the root, so every gate record carries the path-only "missing" identity and the attach refuses each verified answer no_squad_identity (v3.0.2's void series, session v302). Prepare refuses session_root_missing ("run amq init for the session first"); and at its end it checks the identity it wrote into each gate record against the store's identity and refuses on a difference.
- 3.0.x: `remote attach` starts when another attach already holds the same mailbox; with two on one mailbox the newest reply wins, so a verified answer can show as refused (v31 and v302 on 2026-10-31). The attach refuses to start while another attach holds its mailbox.
- 3.0.x: a `release join` step in the release flow's containment check (#819's CheckCommit, internal/releaseflow/releaseflow.go:253): cut main-<version> off main, merge the release commit, prove the empty tree diff (`git diff --stat` against the release branch), open the PR, so the lead stops doing it by hand (done by hand as #813 for 3.0.1 and #824 for 3.0.2).
- 3.0.x: internal/cli TestRealPTYWaitPostureRefusesNamedGateBeforeTimeout (wait_posture_pty_test.go) runs a `go build` of ./cmd/amq-squad that reaches proxy.golang.org (briefs ruling 18: no test reaches the network). The build runs offline, as 3.0.1 t18 made the buzz dry run's.
- 3.0.x, flakes to watch (seen 2026-10-31, keep the failing full-run output before any fix): internal/cli TestDecisionAdapterKillsAHungGuardGroup ran out its 30 s watchdog once under load; internal/redact TestBlockIsLinearOnALongLine failed once on #814's CI at 78.5x against the 64x bound (the t2 evidence).

## Acceptance rows that wait on the owner session (not deferred, open)

See plan 16.4's table: 3-2 acceptance 4 (the real S6 merge), milestone B's
Claude live rows and the Codex seat row, 4-1 acceptance 10 (the AMQ release
carrying PR #1188), 4-5 acceptance 4 and 3-1 acceptance 9 (the Buzz session,
with the signed seat clear as its third item), 4-2 acceptance 3.
