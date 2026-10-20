# Amendments to docs/v3-plan.md collected during the phase 1 spike

Running list. Each item names the plan section it changes. The final spike
report turns this into revision 2.3 of the plan.

1. Section 7, R4, and doctor (from S4, risk 16 in the phase 1 plan): a fresh
   codex-cli seat stops on its folder-trust prompt. amq-squad never writes
   the owner's Codex config and never answers the prompt for the owner.
   Phase 2b adds an owner-run `amq-squad trust <repo>` step and a doctor
   check that lists untrusted roots before a Codex seat launches.
2. R4 (from S4 reviewer ruling 1): the role comes only from `role.md` in the
   seat directory, bound through the wrapper argv in the trust digest.
   `initial_input` carries at most a pointer. Role text in `initial_input`
   without `role.md` is a malformed launch; the seat stops.
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
