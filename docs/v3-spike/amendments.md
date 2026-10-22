# Amendments to docs/v3-plan.md collected during the phase 1 spike

Running list. Each item names the plan section it changes. The final spike
report turns this into revision 2.3 of the plan.

1. Section 7, R4, and doctor (from S4, risk 16 in the phase 1 plan): a fresh
   codex-cli seat stops on its folder-trust prompt. amq-squad never writes
   the owner's Codex config and never answers the prompt for the owner.
   Phase 2b adds an owner-run `amq-squad trust <repo>` step and a doctor
   check that lists untrusted roots before a Codex seat launches.
2. R4 (from S4 reviewer ruling 1, corrected): the role comes only from
   `role.md` in the seat directory; `initial_input` carries at most a
   pointer. Role text in `initial_input` without `role.md` is a malformed
   launch; the seat stops. The AMQ trust digest binds the wrapper path and
   argv, not the wrapper binary bytes and not `role.md` bytes. Content
   integrity of `role.md` is amq-squad's job: the Go core records the
   SHA-256 of `role.md` in the seat's launch record when it writes the file;
   `seat-exec` recomputes it before exec and refuses to start on mismatch.
   The record lives under the AMQ root, outside the project tree. Wrapper
   binary integrity is the same trust as the `amq` binary itself (absolute
   path outside the project, owner's filesystem): accepted, not solved.
3. R32 and section 4.4 (from S4 fault table): Claude PreToolUse fails open
   on timeout, non-zero exit without a deny, and a missing command, and a
   timeout costs exactly the configured timeout. Enforcement stays in the Go
   core; hooks are UX and evidence. The three rows are cited.
4. R4 (from S4 reviewer ruling 2): per-seat CODEX_HOME is rejected; the env
   overlay allowlist cannot carry it. If codex-cli rewrites its config at
   start, document that codex-cli owns its config file.
5. Section 11 ask 4 (from S4): Claude capabilities advertise only
   `--allowedTools`, `-n`, `--name` while `--model` and `--effort` are
   accepted; the Codex grammar has the same gap. Ask text in
   docs/v3-upstream-asks.md.
6. Section 13 phase 1 roster (process): the Codex subagent in this Claude
   Code harness cannot install, clone, or create branches. Implementation
   ran on Opus; Codex subagents review. This says nothing about codex-cli
   seats under `amq launch`.
7. Section 4.3 seat-exec spec (from the S4 Codex review), fix before first
   use in 2b: anchor directory traversal to the verified AMQ root with no
   symlink following in the initial path; check ownership and permissions of
   existing directories; drop the `--root` flag (interface is `--seat` only,
   `AM_ME` and `AM_ROOT` from the environment); ship the Claude hook as the
   user-scope plugin, not a project-scope stub.
8. Section 4.1 and R1 (from S3): the embedded pi is
   `@earendil-works/pi-coding-agent` (old scope deprecated), pinned by
   version with a committed lockfile; Node floor >=22.19.0; packaging (a)
   Node 22 plus the npm tree, digest-pinned fetch once, offline build;
   strip of maps and type files applied (decided); (b2) upstream Bun binary
   documented as fallback only; (b1) rejected as not faithful. Embedded lead
   and seats run with PI_OFFLINE=1, PI_SKIP_VERSION_CHECK=1, PI_TELEMETRY=0
   and the agent dir ~/.amq-squad/pi-agent/. fd and ripgrep are embedded as
   digest-pinned copies fetched once in build.sh; no run-time downloads.
   AMQ's own pi evidence pin (0.85.1) lags; ask 1 asks upstream to re-verify
   the bridge against the current scope and version. (Lead rulings 1 to 3.)
   From the S3 Codex review and lead rulings, 2b requirements under R1:
   (i) launch-time integrity: a manifest of path, size, mode and SHA-256
   is written at cold install; every launch verifies the manifest hash,
   every executable, and every directory on the path to the entrypoint for
   symlinks; full file hashing only when the marker is absent or doctor
   asks; measured cost 43.5 ms median per warm launch (S3 report), 190 ms for a full re-hash. Post-strip launcher 59.3 MB, unpacked 179 MB in 6,432 files including pinned fd 10.5.0 and ripgrep 15.2.0; cold start to first token 2.38 s, warm 0.25 s. (ii) fd and rg are
   pinned, digest-checked copies in a managed agent-bin inside the payload,
   so tools-manager's run-time download (tools-manager.js:75) is never
   reached. (iii) the launcher itself sets PI_OFFLINE=1,
   PI_SKIP_VERSION_CHECK=1, PI_TELEMETRY=0 and PI_CODING_AGENT_DIR under
   ~/.amq-squad/pi-agent/<seat>; these flags do not isolate the network,
   which stays a host concern (plan Q1). (iv) the own-Bun fallback is
   dropped; (b2) upstream Bun is the only fallback.
9. Section 16.4 and section 3 first run (from S3, lead ruling 4): a
   quarantined launcher hangs on first start. Release risk, not a spike
   failure. Phase 5 requirement: the launcher ships signed and notarized,
   and release-smoke includes a browser-download start on a clean user
   account, cold-launching through the extracted children. Until then the
   install paths are go install and brew only. The hang itself is
   "unexplained, reproduce under notarization".

Criterion numbering for the final report (16.7): C1 = S1, C2 = S2, C3 = S3,
C4 = S4, C5 = S6; S5 answers amendment 16.3.
10. Section 5 registry (from S5, lead ruling 6): "verified" for a local
    model means N of N probe passes with zero false successes, N=3 in the
    spike and N=5 before a registry entry ships; qwen3.6:27b verified at
    3/3; qwen2.5:32b probation (one false success); gemma3:4b in a "no tool
    calls" state, never routed implement or script work. Registry latency
    uses direct text TTFT, because on a tool-call turn Ollama returns the
    whole call as the first event.
11. Section 4.1 pi package, phase 4 (from S5, lead rulings 7, 8, 10):
    register native amq_send, amq_drain and amq_reply tools; dispatch is
    tracked by task leases, not by mail (drain consumes mail with no lease
    and a mid-turn doorbell dropped a task: direct evidence for D5, cited
    under criterion 3 in the final report); role text goes in the first
    message and also in the system prompt, since the appended system prompt
    alone failed with the "AMQ = Advanced Message Queuing" misread as the
    failure signature, and the lead loop test is "the lead does not start
    coding"; before_agent_start injection is tested in 2b; the hold seam
    works with no pi change and fails closed, but pi emits
    tool_execution_start before the hook (revision 4 feedback in the bridge
    ask); parallel held calls and a local confirm racing a remote answer are
    2b tests.
12. R16 (from S5, lead ruling 9): vendor API keys in the owner's
    environment reach local-only seats (pi offered 23 Google models with
    GEMINI_API_KEY set). Both fixes: seat-exec execs the provider with a
    scrubbed environment (deny-list of *_API_KEY, *_TOKEN, *_SECRET and the
    known vendor names, unless the seat's runtime declares it needs one),
    and the pi package restricts the model list to the seat's registry
    entry. The S4 report states whether a Claude seat under the wrapper
    inherits the full parent environment.
13. R32 and section 7.3 (from the S1 Codex review and lead ruling): the
    typed digest prefix stays 16 hex for the owner's thumbs. The collision
    game is closed by binding, not by length: the gate record, written by
    the Go core under the AMQ root before the question is sent, holds the
    full target digest and is immutable; the verifier compares the typed
    prefix against that record only; the action after an approval executes
    against the record's full digest, never against anything a seat
    presents later. Verifier conformance rules: explicit s < n on the
    Schnorr signature; strict event decoding (invalid UTF-8, duplicate keys,
    null tag elements rejected); presentation window created_at - 60 s <=
    now <= not_after; CR and LF rejected anywhere in the answer; envelope
    first, then consumed, both fsynced. Same-UID writes to the ledger are
    the host threat model (single-user Mac), accepted for phase 1.
14. Section 9 and R33 (from S1): amq-remote 0.102.0 has no phone-side grant
    signing; enrollment needs the Buzz nsec once in amq-owner-sign (hidden
    prompt or password-manager pipe; written nowhere). Owner decision; if
    no, criterion 1 stays pending behind upstream ask 10. Interop finding
    on C0 escaping in event ids is ask 11 (file after reviewer confirms).
15. Section 5 registry (and 6.2) (from the S5 Codex review): a false claim mid-run
    that ends in RESULT: FAIL is not a false success; qwen2.5:32b is "2/3,
    unverified". The probe predicate requires confirmed tool execution,
    staged and untracked diff checks, an exact final line, and a read.
    Hook awaiting in pi 1.3.0 is sequential, so one held call delays the
    approval of later calls: 2b tests multiple held calls and local-versus-
    remote races. The role-delivery comparison was not controlled; 2b runs
    the controlled matrix.
16. Section 13 phase 2a (lead, after accepting c899e8f): release-smoke's
    `go install .../v3/...@$(VERSION)` cannot pass until the first v3 tag
    exists, so `make release-smoke` is expected red on v3 until phase 5.
    README, MIGRATION, RELEASING and scripts/check-release-version.py stay
    on v2 until the v3 tag. Branch v3-2a0-module-path stays unmerged until
    the owner's go on the spike table. Update 2026-10-21 morning: the owner
    gave the go ("go, ok") and it is merged into v3 as 422199d; build green.

Section numbers corrected 2026-10-21 (plan revision 2.3 placement): amendments 8 and 11 belong to 4.1, amendments 10 and 15 to section 5 (registry) and 6.2. All sixteen are integrated in docs/v3-plan.md revision 2.3.

17. Sections 4.3, 5 and 14, R16 (from the S4 closure run, 2026-10-21
    08:16-08:26): (a) Criterion 4 stays NOT PASSED for an upstream reason:
    AMQ 0.102.0 finalizes a capture-mode Codex conversation as `stale` at
    Apply and `__codex-notify` accepts only `pending`, so no Codex identity
    capture or notify forward can complete on any backend (upstream ask 12).
    Role visibility through `role.md` holds for both seats and the folder
    trust prompt is gone after the owner's pre-trust. (b) The codex-cli
    0.147.0 capture pin and the owner's default model are incompatible
    (`gpt-6-astra` needs a newer codex; `gpt-5.5` is refused on a ChatGPT
    account; `gpt-5.6-sol` works). Prepare accepts any model id, so the
    registry must check the model against the pinned codex version before
    launch, not after the first turn. (c) Upstream ask 5 is answered: a
    `workspace-write` Codex seat cannot commit in a linked worktree whose
    git dir is outside the writable root (`index.lock: Operation not
    permitted`). (d) codex-cli rewrites its own `config.toml` at start
    (`[marketplaces.*] last_updated`); amq-squad documents that codex-cli
    owns that file and never writes it. (e) The seat process environment is
    the tmux server's global environment plus the AMQ overlay; the
    seat-exec scrub (amendment 12) holds at process level, but provider
    tool shells source the owner's shell profile, which added
    `GEMINI_API_KEY` inside the Codex seat's shell tool. R16 gains: the
    doctor checks the owner's shell profile for exported `*_API_KEY`,
    `*_TOKEN`, `*_SECRET` names and warns; the pi package's tool shell runs
    without the profile. `SSH_AUTH_SOCK` reaches both seats and is a 2b
    decision. Integrated in 2.4.

18. seat-exec denies writes to the owner's global agent config (~/.claude/settings.json, ~/.codex/config.toml); doctor diffs it against the preflight copy. Evidence: S6 finding B (2026-10-22, unattributed change at 06:56). Section 4.1 / R16. Integrated in 2.4.

Items 17 and 18 are integrated in docs/v3-plan.md revision 2.4 (2026-10-22).

19. From the S1 owner run (2026-10-22, reviewer ruling): (a) `--enable buzz-profile --presence` is the enrollment default, one extra owner signature; (b) the gate text says "reply in this thread"; (c) thread binding stays on; a non-thread answer that matches an open gate (gate id, class, prefix, owner pubkey) makes the core re-post "reply here to confirm" in the gate thread, and the next thread reply is the answer; no "only candidate" acceptance. Evidence: S1 report section 12 (g-owner-01 rejected, g-owner-02 passed). Sections 7 and 9. For 2.5.

20. Maintainer rulings of 2026-10-22 on the pre-filing asks (plan inputs, for 2.5): (a) pi adapter as a mint-mode adapter, `pi --session-id <launch nonce>`, pinned verified pi version, fail-closed resume: section 4.1 pi package, the launch section, R1 and the launchapi requirements; (b) argument forms derived upstream from the validation tables: section 4.3, seat-exec stops carrying its own list once upstream ships; (c) Codex grammar narrowed to `--add-dir` plus a fixed `-c` key list, no `--profile`: sections 4.3 and 14, plus a phase 2b task "one measured run with `--add-dir <git common dir>` on codex-cli 0.163.0" that must pass before ask 5 is filed under its new title; (d) notify chain declined: the Codex notify design uses a fan-out script as the notify command and amq-squad never writes config.toml (consistent with amendment 17); (e) signed event carried as a `nostr:<eventID>` label, the layer verifies against the relay with the Claude-hook checks: sections 7 and 9; the S1 verifier already does resolve/fetch/verify, so the gate transport is confirmed, not changed. Also: AMQ v0.104.0 (2026-10-22) ships asks 9 and the #1137 capture-receiver rewrite; the C4 re-run targets v0.104.0.

