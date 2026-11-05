# 3.2.0 activity renewal evidence

Observed on 2026-11-05 with Codex CLI 0.166.0.

## Codex event catalogue

The installed binary contains these hook event names in its exact event enum:

```text
PreModelRequest PostModelRequest PreToolUse PermissionRequest PostToolUse
PreCompact PostCompact SessionStart SessionEnd UserPromptSubmit SubagentStart
SubagentStop Stop Interrupt
```

`PostToolUse` is an in-turn work event, so the brief's stop condition did not
apply. The seat launch adds per-process `PostToolUse` and `Stop` handlers by
Codex config arguments. It does not write the owner's `CODEX_HOME`. A strict
config parse was checked with:

```sh
codex --strict-config \
  -c 'hooks.PostToolUse=[{hooks=[{type="command",command="true",timeout=5}]}]' \
  -c 'hooks.Stop=[{hooks=[{type="command",command="true",timeout=5}]}]' \
  --help
```

The command exited 0. `PermissionRequest` and `UserPromptSubmit` are not
activity producers.

## Contract checks

- Store tests cover attempt mismatch, late activity, both blocking and
  non-blocking renewal paths, the absolute task budget, and lead extension.
- Scheduler tests cover silence expiry, first-activity decisions, one warning
  before budget end, and the budget-spent decision.
- Runtime tests cover Codex's real-work event set and Pi's tool-result adapter.
- Status and screen fixtures carry lease and budget clocks.

The core bound's revert proof removed the expiry clamp in `claim` and ran:

```sh
go test ./internal/task -run TestActivityRenewalIsBoundedByTaskBudgetOnBothPaths -count=1
```

Both `renew` and `renew_no_wait` failed: their leases ended at 12:34 while
the recorded budget ended at 12:25. Restoring the clamp made both pass.

Final gates after restoration:

```text
go build ./...                                      PASS
go vet ./...                                        PASS
go test -count=1 ./...                              PASS
pi: npm test                                        115 pass, 1 expected skip
make ci components                                  PASS
```

The first `make ci` continuation rejected a newly documented `TITLE`
placeholder. Replacing it with an executable example made
`skill-invocation-check` pass all 78 documented invocations; the remaining
release, scope, Claude-hook, and Pi targets then passed.

The spike subtree outside this evidence directory was not modified.
