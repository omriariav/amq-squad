# 330 pin move 3 cites

Task t172 at TIP `6dc265323928136ab9bd4993d09c0243927ca3a5`: fifteen lead inputs landed byte for byte; this note records the commands.

`shasum -a 256 $IN/MANIFEST.sha256` — exit 0:

```text
da1375e2e15c485b7b3e100b11951b548d65df71695880771cc907be04a621db  MANIFEST.sha256
```

Correction (2026-11-12, a lead edit by hand; the plan reviewer's ruling on
#977): the directory was cut from the printed line above, and the hash is the
file's.

`(cd $IN/tree && shasum -a 256 -c ../MANIFEST.sha256)` — exit 0:

```text
RELEASING.md: OK
docs/v3-buzz/enroll.sh: OK
docs/v3-buzz/owner-run.md: OK
docs/v3-spike/330-pin-move-1/README.md: OK
docs/v3-spike/330-r0-inspect-floor/README.md: OK
internal/adoptionseam/floor.go: OK
internal/buzz/inbound.go: OK
internal/buzz/manifest.go: OK
internal/buzz/relay.go: OK
internal/cli/up.go: OK
internal/launchintent/launchintent.go: OK
internal/scheduler/roster.go: OK
internal/seatlaunch/compile.go: OK
internal/seatlaunch/roster.go: OK
internal/team/squad.go: OK
```

`git diff --stat fe7f5b8f7ae4f533bb2a64d2894bc28e6007d199 6dc265323928136ab9bd4993d09c0243927ca3a5 -- RELEASING.md docs/v3-buzz/enroll.sh docs/v3-buzz/owner-run.md docs/v3-spike/330-pin-move-1/README.md docs/v3-spike/330-r0-inspect-floor/README.md internal/adoptionseam/floor.go internal/buzz/inbound.go internal/buzz/manifest.go internal/buzz/relay.go internal/cli/up.go internal/launchintent/launchintent.go internal/scheduler/roster.go internal/seatlaunch/compile.go internal/seatlaunch/roster.go internal/team/squad.go` — exit 0; no output.

`shasum -a 256 -c $IN/MANIFEST.sha256` — exit 0:

```text
RELEASING.md: OK
docs/v3-buzz/enroll.sh: OK
docs/v3-buzz/owner-run.md: OK
docs/v3-spike/330-pin-move-1/README.md: OK
docs/v3-spike/330-r0-inspect-floor/README.md: OK
internal/adoptionseam/floor.go: OK
internal/buzz/inbound.go: OK
internal/buzz/manifest.go: OK
internal/buzz/relay.go: OK
internal/cli/up.go: OK
internal/launchintent/launchintent.go: OK
internal/scheduler/roster.go: OK
internal/seatlaunch/compile.go: OK
internal/seatlaunch/roster.go: OK
internal/team/squad.go: OK
```

`git diff --summary 6dc265323928136ab9bd4993d09c0243927ca3a5` — exit 0; no output, including no "mode change" line.

`go build ./...` — exit 0; no output.

`go vet ./internal/adoptionseam/ ./internal/buzz/ ./internal/cli/ ./internal/launchintent/ ./internal/scheduler/ ./internal/seatlaunch/ ./internal/team/` — exit 0; no output.

`gofmt -l internal/adoptionseam/floor.go internal/buzz/inbound.go internal/buzz/manifest.go internal/buzz/relay.go internal/cli/up.go internal/launchintent/launchintent.go internal/scheduler/roster.go internal/seatlaunch/compile.go internal/seatlaunch/roster.go internal/team/squad.go` — exit 0; no output.

`go test ./internal/adoptionseam/ -count=1` — exit 0:

```text
ok  	github.com/omriariav/amq-squad/v3/internal/adoptionseam	0.512s
```

`go test ./internal/team/ -count=1` — exit 0:

```text
ok  	github.com/omriariav/amq-squad/v3/internal/team	0.587s
```

`go test ./internal/launchintent/ -count=1` — exit 0:

```text
ok  	github.com/omriariav/amq-squad/v3/internal/launchintent	0.362s
```

`go test ./internal/cli/ -run '^(TestSkillIdentity|TestGeneralOperationFloorUnchanged|TestReleaseCheck)' -count=1` — exit 0:

```text
ok  	github.com/omriariav/amq-squad/v3/internal/cli	0.501s
```

`bash -n docs/v3-buzz/enroll.sh` — exit 0; no output.

`python3 scripts/check-release-version.py --print-amq-floor` — exit 0:

```text
0.140.0
```

`go run ./scripts/redact-evidence -check docs/v3-spike/330-pin-move-1/` — exit 0; no output.

`go run ./scripts/redact-evidence -check docs/v3-spike/330-r0-inspect-floor/` — exit 0; no output.

`go run ./scripts/redact-evidence -check docs/v3-spike/330-pin-move-3-cites/` — exit 0; no output.

`git diff --check` — exit 0; no output.

`git diff -U0 6dc265323928136ab9bd4993d09c0243927ca3a5 -- '*.go' | grep '^[-+]' | grep -v '^[-+][-+]' | grep -v '^[-+][[:space:]]*//'` — exit 1; no output.

`git diff --stat 6dc265323928136ab9bd4993d09c0243927ca3a5` — exit 0:

```text
 RELEASING.md                                 |  6 +++---
 docs/v3-buzz/enroll.sh                       |  6 +++---
 docs/v3-buzz/owner-run.md                    | 15 +++++++-------
 docs/v3-spike/330-pin-move-1/README.md       | 28 ++++++++++++++++----------
 docs/v3-spike/330-r0-inspect-floor/README.md |  5 ++++-
 internal/adoptionseam/floor.go               |  2 +-
 internal/buzz/inbound.go                     | 12 +++++------
 internal/buzz/manifest.go                    |  2 +-
 internal/buzz/relay.go                       |  2 +-
 internal/cli/up.go                           |  2 +-
 internal/launchintent/launchintent.go        |  2 +-
 internal/scheduler/roster.go                 | 20 ++++++++++---------
 internal/seatlaunch/compile.go               | 30 ++++++++++++++--------------
 internal/seatlaunch/roster.go                | 16 +++++++--------
 internal/team/squad.go                       |  4 ++--
 15 files changed, 82 insertions(+), 70 deletions(-)
```

## The on_live check

`git grep -n OnLive -- '*.go' ':!*_test.go'` — exit 0:

```text
internal/launchintent/launchintent.go:159:	OnLive          launchapi.OnLivePolicyV1
internal/launchintent/launchintent.go:395:		OnLive: seat.OnLive,
internal/scheduler/roster.go:24:// (No seat launch names on_live keep: checked by `git grep -n OnLive -- '*.go'
```

Two lines beside the comment that names this command: the field and its copy into the participant. No non-test Go line assigns the field.

## Kept

`git grep -n 'v0\.116\.2' -- '*.go' ':!*_test.go'` — exit 0:

```text
internal/adoptionseam/floor.go:18:// (the go.mod pin; brief 5-amq-A moved it from v0.107.0 to v0.116.2, and the
internal/adoptionseam/floor.go:19:// 3.3.0 pin move moved it from v0.116.2 to v0.140.0). The adoption floor above
internal/buzz/manifest.go:103:		return b, fmt.Errorf("share target %s is kind %q, not mailbox (amq-remote v0.116.2 or later)", b.Target, b.Kind)
internal/cli/remote.go:41:it binds the share to amq-remote's mailbox adapter (amq-remote v0.116.2 or
internal/registry/versions.go:20:	// v0.116.2 (adapter.go ProviderStaticInputCapabilities): claude 2.1.233,
internal/registry/versions.go:23:	"v0.116.2": {
```

Twenty-three such lines were on the tip; seventeen changed here and these six stay.

## Left as dated

`git grep -c 'v0\.116\.2' -- '*_test.go'` — exit 0:

```text
internal/adoptionseam/adoptionseam_test.go:1
internal/buzz/connection_test.go:1
internal/buzz/enroll_test.go:1
internal/buzz/phone_test.go:1
internal/buzz/rehearsal_test.go:2
internal/buzz/relay_test.go:1
internal/buzz/serve_test.go:1
internal/buzz/signal_test.go:1
internal/cli/real_amq_nested_worktree_discovery_test.go:1
internal/cli/remote_serve_test.go:1
internal/cli/remote_test.go:1
internal/cli/up_test.go:1
internal/launchintent/wake301_test.go:1
internal/seatlaunch/wake301_test.go:1
```

Fifteen lines in fourteen test files, left as dated cites of what each test was written against (the plan reviewer's ruling).

## Not run by the seat

- Whole-repo `make ci`: the lead's, on the PR head.
- HTML checks: the lead's, on the PR head.
