# Skew check, rc.2 candidate da109b496787ffb7b8a716b8d2d75af19e98a045 against v3.2.0-rc.1 (3966afb72b85e1ab6c0fc8c879f1c5ef03f92a40)

`git diff --stat 3966afb72b85e1ab6c0fc8c879f1c5ef03f92a40 da109b496787ffb7b8a716b8d2d75af19e98a045 -- internal/routing internal/scheduler internal/sealchain internal/seatcommit internal/task`

 70 files changed, 14225 insertions(+), 435 deletions(-)

non-test files changed:
internal/routing/outcome.go
internal/routing/refusal.go
internal/routing/router.go
internal/scheduler/exits.go
internal/scheduler/hooks.go
internal/scheduler/roster.go
internal/scheduler/scheduler.go
internal/scheduler/tamper.go
internal/sealchain/migration.go
internal/sealchain/raw.go
internal/sealchain/sealchain.go
internal/seatcommit/alarms.go
internal/seatcommit/baseshadow.go
internal/seatcommit/commit.go
internal/seatcommit/ledger.go
internal/seatcommit/quarantine.go
internal/seatcommit/quarantine_other.go
internal/seatcommit/quarantine_unix.go
internal/seatcommit/recover.go
internal/task/class.go
internal/task/files.go
internal/task/lease.go
internal/task/reset_if.go
internal/task/task.go
internal/task/unclean.go

#963 merge against its first parent, same five packages (expected empty):
0
