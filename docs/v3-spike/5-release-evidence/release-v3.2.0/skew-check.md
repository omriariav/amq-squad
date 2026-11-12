# Skew check, the v3.2.0 tag commit e93ab08c378265e8b0a7d7a6123bc834feec5c67 against v3.2.0-rc.2 (da109b496787ffb7b8a716b8d2d75af19e98a045)

Run for this note on 2026-11-12, after the cut, in the note's own worktree (at the tag commit).

`git diff --stat da109b496787ffb7b8a716b8d2d75af19e98a045 e93ab08c378265e8b0a7d7a6123bc834feec5c67 -- internal/routing internal/scheduler internal/sealchain internal/seatcommit internal/task`

prints nothing (exit 0); `git diff --name-only` over the same range and directories prints 0 lines.

The whole range, `git diff --shortstat da109b49 e93ab08c`:

 82 files changed, 1999 insertions(+), 77 deletions(-)

`git rev-list --count da109b49..e93ab08c` prints 25.

The #985 merge against its first parent, `git diff --stat 05f6b489f458cba304b015b6834ad704a1fd6cf3 e93ab08c378265e8b0a7d7a6123bc834feec5c67 | tail -1`:

 26 files changed, 150 insertions(+), 73 deletions(-)

The same range over `internal pi cmd scripts`:

 internal/claudeseat/relprep_version_test.go | 2 +-
 1 file changed, 1 insertion(+), 1 deletion(-)

internal/sealchain/raw.go:30 at the tag commit:

const FirstRawLayoutVersion = "v3.2.0-rc.2"
