# t43 typed block archive

Base: `b67df349` (#853 merged).

## Result

The scheduler now archives a stopped typed-widen attempt when its ended
attempt first appears in the task store. The archive is keyed by task and
attempt in the sealed seat ledger, so a lead can widen immediately after the
block without waiting for `CleanSeat`. A later `CleanSeat` finds the same key
and does not append a second discard.

The block stores its attempt explicitly. A pending task cannot manufacture a
typed `widen_requested` block from old attempt history, while an ordinary lead
hold on a pending task remains valid. Reset and accepted widening clear the
binding; growth-gate escalation preserves it for the approved retry.

The snapshot regression uses a temporary `HOME`. The CLI rows use a real live
attempt and archived ledger snapshot, including an over-cap widen before seat
reuse and a reset that cannot select the old archive.

## Verification

- `go test ./internal/task ./internal/cli ./internal/seatcommit ./internal/schedproc ./internal/scheduler -count=1`: pass with live AMQ seat variables removed.
- Focused typed-block, growth-gate archive, duplicate archive, pending refusal,
  pending hold, reset, and immediate size-cap rows: pass.
- `git diff --check`: pass.
