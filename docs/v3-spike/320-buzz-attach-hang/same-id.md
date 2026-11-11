# Owner resend event IDs

## Problem and construction

`ownerDMSender.send` signs Nostr kind 9 events, whose ID includes `created_at`
at whole-second resolution. A resend with the same content and thread could
therefore reuse its first ID when the old post-wait clock read was still in the
same second. The sender now accepts an injectable clock and, after preserving
the wait through the clock's next second, chooses the later of that clock's
second and `lastCreated + 1`. A nil clock continues to mean `time.Now`.

`TestOwnerSenderResendGetsFreshEventID` holds the clock at one instant across
both sends. It proves the IDs differ, the stored second event is one second
newer, and both owner events reached the relay store.

## Revert proof

Command:

```sh
go test -count=1 -run 'TestOwnerSender' ./internal/buzz/
```

Reverted expression: `created = max(now().Unix(), s.lastCreated+1)` after the
wait, replaced with `created = now().Unix()`.

First failing line:

```text
owner_sender_test.go:22: resend has the same event id: 499bd6af6247c6d159877e71d4bd2b13863f9518f85fb20500cdd72b46b4d9e1
```

The restored expression makes the command pass.

## Gates

| Command | Exit code |
| --- | ---: |
| `go build ./...` | 0 |
| `go vet ./internal/buzz/...` | 0 |
| `go test -count=1 -run 'TestOwnerSender' ./internal/buzz/` | 0 |
| `go test -count=1 ./internal/buzz/...` | 0 |
| `NPM_FLAGS=--offline make ci` | 2 (unrelated seat-environment failures in `internal/cli`: role-rules hashes and harness-only seat-commit rows; `internal/buzz` passed) |
| `go run ./scripts/redact-evidence -check docs/v3-spike/320-buzz-attach-hang/same-id.md` | 0 |
