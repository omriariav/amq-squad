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
8. Section 4.2 and R1 (from S3): the embedded pi is
   `@earendil-works/pi-coding-agent` (old scope deprecated), pinned by
   version with a committed lockfile; Node floor >=22.19.0; packaging (a)
   Node 22 plus the npm tree, digest-pinned fetch once, offline build;
   optional strip of maps and type files (51 MB). Embedded lead and seats
   run with PI_OFFLINE=1, PI_SKIP_VERSION_CHECK=1, PI_TELEMETRY=0 and an
   agent dir under ~/.amq-squad/; fd and ripgrep pinned and embedded or left
   to the host (decision pending review).
9. Section 16.4 and section 3 first run (from S3): a quarantined launcher
   hangs on first start. Phase 5 bundles are signed and notarized, and the
   release test downloads the launcher through a browser.
10. Section 6 registry (from S5): "verified" for a local model means N of N
    probe passes with zero false successes; a model that passes some runs
    is "probation"; a model without tool support gets its own state and is
    never routed implement or script work. Registry latency uses direct text
    TTFT, not first event on a tool-call turn.
11. Section 4.2 pi package, phase 4 (from S5): register native amq_send,
    amq_drain and amq_reply tools; dispatch is tracked by task leases, not by
    mail (drain consumes mail with no lease and a mid-turn doorbell can drop
    a task); role injection through before_agent_start must be tested, since
    a role appended to the system prompt alone failed; the hold seam works
    with no pi change, but pi emits tool_execution_start before the hook, so
    the activity stream shows a held call as started.
12. R16 (from S5): vendor API keys in the owner's environment reach
    local-only seats (pi offered 23 Google models with GEMINI_API_KEY set).
    The wrapper strips provider keys for seats whose registry entry is
    local-only, and the package restricts the model list to the registry
    entry. Decision pending review.
