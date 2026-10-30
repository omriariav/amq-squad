# S4 seat-exec prototype

Standalone spike module (own `go.mod`; the main module does not build it).
Results are in [`docs/v3-spike/S4-report.md`](../../docs/v3-spike/S4-report.md).

| File | Purpose |
| --- | --- |
| `main.go` | `seat-exec`: reads `--seat`/`--root` or `AM_ME`/`AM_ROOT`, writes `launch.json` and `role.md` under `<root>/agents/<handle>/extensions/io.github.omriariav.amq-squad/`, sets `AMQ_SQUAD_SEAT`, `AMQ_SQUAD_ROLE_PATH`, `AMQ_SQUAD_ROOT`, execs the provider argv in place. Adds no provider flags. |
| `smoke.py` | Offline check of the wrapper (python stands in for the provider). |
| `request.py` | Renders the PrepareRequestV1 (contract 0.61.1). |
| `apply-request.py` | Builds the Apply request from a saved Prepare (trust_exact_subject only). |
| `amq-spike.sh` | Runs `amq` from the scratch project with `XDG_STATE_HOME` scoped to the spike; `CODEX_PIN=0147` puts codex-cli 0.147.0 first on PATH. |
| `digest-probe.py` | Prepare-only, one-field mutations; reports trust/plan digest changes. |
| `s4hook.py` | Project-scope Claude hook (SessionStart, UserPromptSubmit, PreToolUse, PostToolUse). PreToolUse fault mode is read at run time. |
| `fault-run.sh` | Drives one PreToolUse fault trial in the live Claude pane. |
| `fault-table.py` | Builds the fault table from hook events and the Claude session JSONL. |
| `notify-watch.sh` | Passive process-table watcher for the Codex notify forward. |
| `notify-watch.py` | Closure-run notify observer: libproc scan (~2 ms), argv only, logs `amq __codex-notify` and SkyComputerUseClient processes. |
| `env-names.py` | Prints environment variable names (never values) of a live process; flags `*_API_KEY`/`*_TOKEN`/`*_SECRET`. |
| `evidence/` | Raw outputs from the live run on 2026-10-21 (`closure/` for the morning closure run; `rerun-104/` for the AMQ v0.104.0 re-run on 2026-10-22, launch nonce redacted). |

Build: `cd spike/seatexec && go build -o ~/.amq-squad/spike/bin/seat-exec .`
