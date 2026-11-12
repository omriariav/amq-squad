# The merge train's compare proof

The merge train now checks that GitHub's API diff and compare file list describe the same change in `internal/mergetrain/gh.go:227-434`. The dispatch TIP is `2d52b79dc133d070e5e45c91eff7371f999055bc`; this worktree's later HEAD is `8c915ba47b7d90a2de38af0bd0a53436c43e22bc`, whose scoped precheck is empty. The rule is:

```text
The API diff stays the proof's bytes; the `patch == null` arm is replaced by the train's own check that the diff and the JSON describe the same change: the diff's `diff --git` file set equals the JSON's filenames (previous_filename for renames), and per file the diff's added and removed line counts equal the JSON's `additions` and `deletions`; the 300-file arm and the 406/422 arms stay; any mismatch is ErrIncompleteDiff with its words. No fetch, no credentials, no repository path in the package, no change to any id, and the rebind gets it through the same PatchID. A truncated 200 is caught by the counts, not assumed away; the note says the media type's wholeness on a 200 is otherwise unmeasured.

a 0/0 file's section holds no hunk, and either an `index` line or a 100% rename's three lines
```

The second line replaced an earlier clause after measurement.

## What was measured

On 2026-11-12 at `7542de96b745bb23f0465539e76f0de1f6cfe396`, the lead measured #991's compare: its JSON named two files, with additions and deletions 551/411 and 7/3, while the 962-line note had no `patch`; the 136,190-byte diff held both sections and exactly those per-file counts. The refusal was caused by the JSON `patch` test, not an incomplete diff.

Across eight repository pairs and forty files, every diff section began `diff --git a/<previous_filename, or filename> b/<filename>`, and hunk-length accounting matched JSON additions and deletions. Files with no changed line appeared as the following three forms:

```diff
diff --git a/docs/v3-research/codex-agent-command-center-2026-10-24.png b/docs/v3-research/codex-agent-command-center-2026-10-24.png
new file mode 100644
index 000000000..8dbd5ec6b
Binary files /dev/null and b/docs/v3-research/codex-agent-command-center-2026-10-24.png differ

diff --git a/internal/cli/v3_holding_test.go b/internal/cli/cli_fixture_support_test.go
similarity index 100%
rename from internal/cli/v3_holding_test.go
rename to internal/cli/cli_fixture_support_test.go

diff --git a/docs/v3-spike/3-2-evidence/s6-dry-run/pass5-amq-noc.txt b/docs/v3-spike/3-2-evidence/s6-dry-run/pass5-amq-noc.txt
new file mode 100644
index 000000000..e69de29bb
```

Across 1,930 non-merge commits and 15,651 files, hunk lengths agreed with `git diff --numstat -M` in every case. Counting lines only by their first character failed on two commits because text beginning `--` looked like a file header. A `\ No newline at end of file` marker counted on neither side.

A 2,903-file compare returned 300 JSON files and no second page, while the diff media type returned the complete 54,652,486-byte diff. Therefore a JSON list at the 300-file limit is refused before the diff is read (`internal/mergetrain/gh.go:240-242`).

The real path was measured on gh 2.100.0 not to trim: `internal/mergetrain/gh.go:71-89` returns `cmd.Output()` bytes through `internal/mergetrain/gh.go:39-53`, and the 11,590-byte compare `3bf07c72bccc...e7b23c061595` ended in `"for 3.2.x\n \n"`, byte for byte the local diff. Of 1,930 measured commits, 202 diffs ended in a blank context line. The test fake now preserves those bytes in `internal/mergetrain/fakegh_test.go:184-190` and `internal/mergetrain/fakegh_test.go:500-528`. A proxy stripping trailing whitespace and gh versions other than 2.100.0 are unmeasured and would fail closed as a cut hunk.

The diff media type's wholeness on a 200 is otherwise unmeasured: the check, and not the status, is what says a diff is whole.

A binary file's patch id takes in its index line, whose object ids GitHub abbreviates to a length that was 8 on some compares and 9 on others; both sides of one proof come from the API's diff, so the dependence is the same on both sides, and no test holds an abbreviated index line on both sides of one proof.

A patch id journaled for a PR with a binary file before this change would not be comparable with one computed after it; none exists, since a behind, bound PR with a binary file was refused.

The id pinned by the tests is of a text-only pair; the id of a pair with a binary file, which takes in the index line's abbreviation, is not pinned.

## What the check does

`PatchID` reads the file list with the constant at `internal/mergetrain/gh.go:200-201`, refuses 300 files before reading the diff, reads the diff bytes unchanged, checks them, and passes those same bytes to `git patch-id --stable` (`internal/mergetrain/gh.go:227-275`). The file-list query is:

```jq
.files[]? | {path: .filename, previous_path: (.previous_filename // ""), status, additions, deletions}
```

`the diff's files are not the compare's files` means a listed section is absent, unexpected, duplicated, or does not use the list's previous filename for a rename.

`the diff's added and removed lines are not the compare's counts` means a file's completed hunks do not equal that file's JSON additions and deletions.

`a file with no changed line holds a hunk, or neither an index line nor a 100% rename's three lines` means a 0/0 section lacks the measured header evidence or improperly carries a hunk.

`the diff is cut short inside a hunk` means a hunk ends, another section starts, or a non-diff line appears before the hunk header's old and new lengths are consumed. Each mismatch wraps `ErrIncompleteDiff`, preserving `errors.Is`, while callers retain the fixed owner-facing refusal (`internal/mergetrain/gh.go:261-263`).

## Known limits

A mode change alone, a name git quotes, a file of status `copied`, a symlink, and a submodule receive no special handling; each is refused or accepted as the general rule gives, GitHub's form is unmeasured, and no test claims otherwise.

The owner's refusal words do not identify which of the four mismatches occurred; the proof error does.

The JSON `patch` is no longer read, so a file omitted from both the diff and the list is not visible.

A list of 300 files or more is refused before the diff is read, whatever the diff contains.

The fake serves a binary file as `git diff --binary` prints it, while GitHub's `Binary files ... differ` form is covered directly by the table test from the saved measurement.

A diff whose final blank context line is lost anywhere between GitHub and the check is refused as a cut hunk, not tolerated.

A deleted empty file is accepted on its index line; GitHub's form for one is not measured.

## The tests

`TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged` builds a behind bound PR whose legacy completeness answer has one missing patch and proves it updates and merges.

`TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate` builds two files, removes one diff section, asserts that ground, and proves the first mismatch refuses before mutation.

`TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate` makes one file's list count too large and proves the per-file count mismatch refuses before mutation.

`TestBehindPRWithABinaryFileIsUpdatedAndMerged` builds binary and text files, asserts the binary's 0/0 list row, missing legacy patch, index, and no hunk, then proves update and merge.

`TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate` removes only the binary section's index and proves the third mismatch refuses before mutation.

`TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead` counts reads at 300 and 299 list entries, proving zero and one diff reads respectively.

`TestCompareDiffMismatchWords` holds the measured and fail-closed table forms for all four mismatch texts, including per-file swapped counts, previous filenames, both binary forms, zero-change headers, and cut hunks.

`TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck` reads the 2,680-byte saved compare and pins `d98a700eeca038b3cf05beae0453c0fc52246412`.

`TestLeadRebindOverAFileWithNoPatchIsProvedAndBound` asserts missing legacy patches at both heads and proves `Rebind` binds the moved head.

`TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged` journals an update, moves the branch, asserts both grounds, and proves recovery merges and records the rebase.

`TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged` asserts the fake diff ends with the complete blank context line and proves update and merge.

The fake fields `NoPatchFiles`, `OmitDiffFiles`, `OmitDiffIndex`, and `ExtraAdditionFiles` create the required JSON and diff disagreements (`internal/mergetrain/fakegh_test.go:64-71`). `TruncateAfterRebase` now drops the updated diff's last line. The diff answer reads through `gitBytes` and writes with `stdout.Write` so it no longer trims through `gitOut` and restores a newline with `Fprintln` (`internal/mergetrain/fakegh_test.go:500-528`). The saved answer is compare `b46cb806c6b780fd1fab086b655f3812b86edef6...18dd49977d5d5a192dc104e8d777eaeea9555c50`, blob `fe0ff3c4d3fb09d2554a353e2976d42c5bd4caf3`, with pinned id `d98a700eeca038b3cf05beae0453c0fc52246412`.

## Rows

### R0

`bash ../../../../amq-squad-lead/T/trainfix/inputs/rows-trainfix.sh first`

```text
=== row R0 ===
edit: none (your file)
$ go test ./internal/mergetrain/ -count=1 -timeout 240s -v -run '^(TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged|TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate|TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate|TestBehindPRWithABinaryFileIsUpdatedAndMerged|TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate|TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead|TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged|TestCompareDiffMismatchWords|TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck|TestLeadRebindOverAFileWithNoPatchIsProvedAndBound|TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged|TestIncompleteCompareRefusesWithoutRebind|TestMaxFileCompareRefusesWithoutRebind|TestHTTP422CompareRefusesWithoutRebind|TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead|TestStrictBehindUsesPinnedCompareAndRebases)$'
exit: 0
--- PASS: TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate
--- PASS: TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead
--- PASS: TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate
--- PASS: TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate
--- PASS: TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged
--- PASS: TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged
--- PASS: TestBehindPRWithABinaryFileIsUpdatedAndMerged
--- PASS: TestCompareDiffMismatchWords
--- PASS: TestHTTP422CompareRefusesWithoutRebind
--- PASS: TestIncompleteCompareRefusesWithoutRebind
--- PASS: TestLeadRebindOverAFileWithNoPatchIsProvedAndBound
--- PASS: TestMaxFileCompareRefusesWithoutRebind
--- PASS: TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead
--- PASS: TestStrictBehindUsesPinnedCompareAndRebases
--- PASS: TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck
--- PASS: TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged
pass 16 fail 0 skip 0
failed subtests:
messages (the first 12 lines, each cut at 240 characters; commit ids printed as <sha>; the texts are your tests'):
restore: gh.go cmp exit 0
```
### R1

`bash ../../../../amq-squad-lead/T/trainfix/inputs/rows-trainfix.sh first`

```text
=== row R1 ===
edit: internal/mergetrain/gh.go:227-275 (your PatchID, 49 lines) is the TIP's PatchID (49 lines), whole
$ go test ./internal/mergetrain/ -count=1 -timeout 240s -v -run '^(TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged|TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate|TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate|TestBehindPRWithABinaryFileIsUpdatedAndMerged|TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate|TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead|TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged|TestCompareDiffMismatchWords|TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck|TestLeadRebindOverAFileWithNoPatchIsProvedAndBound|TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged|TestIncompleteCompareRefusesWithoutRebind|TestMaxFileCompareRefusesWithoutRebind|TestHTTP422CompareRefusesWithoutRebind|TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead|TestStrictBehindUsesPinnedCompareAndRebases)$'
exit: 1
--- FAIL: TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate
--- FAIL: TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead
--- FAIL: TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate
--- FAIL: TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate
--- FAIL: TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged
--- FAIL: TestBehindPRWithABinaryFileIsUpdatedAndMerged
--- FAIL: TestIncompleteCompareRefusesWithoutRebind
--- FAIL: TestLeadRebindOverAFileWithNoPatchIsProvedAndBound
--- FAIL: TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged
--- PASS: TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged
--- PASS: TestCompareDiffMismatchWords
--- PASS: TestHTTP422CompareRefusesWithoutRebind
--- PASS: TestMaxFileCompareRefusesWithoutRebind
--- PASS: TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead
--- PASS: TestStrictBehindUsesPinnedCompareAndRebases
--- PASS: TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck
pass 7 fail 9 skip 0
failed subtests:
    --- FAIL: TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead/at_the_limit
    --- FAIL: TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead/one_under
messages (the first 12 lines, each cut at 240 characters; commit ids printed as <sha>; the texts are your tests'):
    compare_proof_test.go:87: #6 was not rebased and merged: status refused; force pushes recorded 0; head unchanged true; task bound to its old head true; audit: refusal: the update could not be proved: GitHub returned an incomplete diff
    compare_proof_test.go:101: not refused in the fixed words: {Number:6 Task:t1 Head:<sha> Draft:true Changed:2 Files:[one.txt two.txt] Refusals:[] GateID: GateError: Lifted:false After:[] RebaseForCI:false RebaseRule: RebaseReason: PatchP
    compare_proof_test.go:115: not refused in the fixed words: {Number:6 Task:t1 Head:<sha> Draft:true Changed:3 Files:[one.txt two.txt] Refusals:[] GateID: GateError: Lifted:false After:[] RebaseForCI:false RebaseRule: RebaseReason: PatchP
    compare_proof_test.go:130: #6 was not rebased and merged: status refused; force pushes recorded 0; head unchanged true; task bound to its old head true; audit: refusal: the update could not be proved: GitHub returned an incomplete diff
    compare_proof_test.go:144: proof error GitHub returned an incomplete diff, want a file with no changed line holds a hunk, or neither an index line nor a 100% rename's three lines
    compare_proof_test.go:173: limit: err=gh api repos/owner/fixture/compare/base...head --jq: unexpected call: unexpected reads=0 want 0
    compare_proof_test.go:173: under: err=gh api repos/owner/fixture/compare/base...head --jq: unexpected call: unexpected reads=0 want 1
    compare_proof_test.go:310: rebind: rebind refused (rebind_unproved): the update could not be proved: GitHub returned an incomplete diff {Run: Task: Repository: Base: PR:0 From: To: Parent: ParentIs: PatchID: Push:} bound=<sha>
    compare_proof_test.go:331: recovery: <nil> {Number:1 Task:t1 Head:<sha> Draft:true Changed:2 Files:[pkg/a.go] Refusals:[{Rule:force_push Detail:head was force-pushed cd40e24a8e35 -> b5fe10a1a993 by owner} {Rule:pr_head_moved Detail:PR #
    strict_policy_test.go:88: incomplete compare was not refused in words: <nil> {Number:1 Task:t1 Head:<sha> Draft:true Changed:2 Files:[pkg/a.go] Refusals:[{Rule:rebase_patch_changed Detail:its change moved during the update}] GateID: Gat
restore: gh.go cmp exit 0
```
### R2

`bash ../../../../amq-squad-lead/T/trainfix/inputs/rows-trainfix.sh first`

```text
=== row R2 ===
edit: internal/mergetrain/gh.go:279
  before: func compareDiffMismatch(files []FileChange, diff []byte) string {
  after:  func compareDiffMismatchYours(files []FileChange, diff []byte) string {
  appended: a compareDiffMismatch that calls yours and answers "" when its answer starts with:
    the diff's files are not the compare's files:
$ go test ./internal/mergetrain/ -count=1 -timeout 240s -v -run '^(TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged|TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate|TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate|TestBehindPRWithABinaryFileIsUpdatedAndMerged|TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate|TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead|TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged|TestCompareDiffMismatchWords|TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck|TestLeadRebindOverAFileWithNoPatchIsProvedAndBound|TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged|TestIncompleteCompareRefusesWithoutRebind|TestMaxFileCompareRefusesWithoutRebind|TestHTTP422CompareRefusesWithoutRebind|TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead|TestStrictBehindUsesPinnedCompareAndRebases)$'
exit: 1
--- FAIL: TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead
--- FAIL: TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate
--- FAIL: TestCompareDiffMismatchWords
--- PASS: TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate
--- PASS: TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate
--- PASS: TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged
--- PASS: TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged
--- PASS: TestBehindPRWithABinaryFileIsUpdatedAndMerged
--- PASS: TestHTTP422CompareRefusesWithoutRebind
--- PASS: TestIncompleteCompareRefusesWithoutRebind
--- PASS: TestLeadRebindOverAFileWithNoPatchIsProvedAndBound
--- PASS: TestMaxFileCompareRefusesWithoutRebind
--- PASS: TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead
--- PASS: TestStrictBehindUsesPinnedCompareAndRebases
--- PASS: TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck
--- PASS: TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged
pass 13 fail 3 skip 0
failed subtests:
    --- FAIL: TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead/one_under
    --- FAIL: TestCompareDiffMismatchWords/file_empty_diff
    --- FAIL: TestCompareDiffMismatchWords/missing_listed_file
    --- FAIL: TestCompareDiffMismatchWords/unexpected_file
    --- FAIL: TestCompareDiffMismatchWords/duplicate_section
    --- FAIL: TestCompareDiffMismatchWords/rename_without_previous
    --- FAIL: TestCompareDiffMismatchWords/quoted_name_local_form
messages (the first 12 lines, each cut at 240 characters; commit ids printed as <sha>; the texts are your tests'):
    compare_proof_test.go:101: not refused in the fixed words: {Number:6 Task:t1 Head:<sha> Draft:true Changed:2 Files:[one.txt two.txt] Refusals:[] GateID: GateError: Lifted:false After:[] RebaseForCI:false RebaseRule: RebaseReason: PatchP
    compare_proof_test.go:173: under: err=git patch-id --stable returned no patch id reads=1 want 1
    compare_proof_test.go:245: got "", want prefix "the diff's files are not the compare's files: "
    compare_proof_test.go:245: got "", want prefix "the diff's files are not the compare's files: "
    compare_proof_test.go:245: got "", want prefix "the diff's files are not the compare's files: "
    compare_proof_test.go:245: got "", want prefix "the diff's files are not the compare's files: "
    compare_proof_test.go:245: got "", want prefix "the diff's files are not the compare's files: "
    compare_proof_test.go:245: got "", want prefix "the diff's files are not the compare's files: "
restore: gh.go cmp exit 0
```
### R3

`bash ../../../../amq-squad-lead/T/trainfix/inputs/rows-trainfix.sh first`

```text
=== row R3 ===
edit: internal/mergetrain/gh.go:279
  before: func compareDiffMismatch(files []FileChange, diff []byte) string {
  after:  func compareDiffMismatchYours(files []FileChange, diff []byte) string {
  appended: a compareDiffMismatch that calls yours and answers "" when its answer starts with:
    the diff's added and removed lines are not the compare's counts:
$ go test ./internal/mergetrain/ -count=1 -timeout 240s -v -run '^(TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged|TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate|TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate|TestBehindPRWithABinaryFileIsUpdatedAndMerged|TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate|TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead|TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged|TestCompareDiffMismatchWords|TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck|TestLeadRebindOverAFileWithNoPatchIsProvedAndBound|TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged|TestIncompleteCompareRefusesWithoutRebind|TestMaxFileCompareRefusesWithoutRebind|TestHTTP422CompareRefusesWithoutRebind|TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead|TestStrictBehindUsesPinnedCompareAndRebases)$'
exit: 1
--- FAIL: TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate
--- FAIL: TestCompareDiffMismatchWords
--- PASS: TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate
--- PASS: TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead
--- PASS: TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate
--- PASS: TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged
--- PASS: TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged
--- PASS: TestBehindPRWithABinaryFileIsUpdatedAndMerged
--- PASS: TestHTTP422CompareRefusesWithoutRebind
--- PASS: TestIncompleteCompareRefusesWithoutRebind
--- PASS: TestLeadRebindOverAFileWithNoPatchIsProvedAndBound
--- PASS: TestMaxFileCompareRefusesWithoutRebind
--- PASS: TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead
--- PASS: TestStrictBehindUsesPinnedCompareAndRebases
--- PASS: TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck
--- PASS: TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged
pass 14 fail 2 skip 0
failed subtests:
    --- FAIL: TestCompareDiffMismatchWords/one_addition_missing
    --- FAIL: TestCompareDiffMismatchWords/removed_line_missing
    --- FAIL: TestCompareDiffMismatchWords/swapped_per-file_counts
messages (the first 12 lines, each cut at 240 characters; commit ids printed as <sha>; the texts are your tests'):
    compare_proof_test.go:115: not refused in the fixed words: {Number:6 Task:t1 Head:<sha> Draft:true Changed:3 Files:[one.txt two.txt] Refusals:[] GateID: GateError: Lifted:false After:[] RebaseForCI:false RebaseRule: RebaseReason: PatchP
    compare_proof_test.go:245: got "", want prefix "the diff's added and removed lines are not the compare's counts: "
    compare_proof_test.go:245: got "", want prefix "the diff's added and removed lines are not the compare's counts: "
    compare_proof_test.go:245: got "", want prefix "the diff's added and removed lines are not the compare's counts: "
restore: gh.go cmp exit 0
```
### R4

`bash ../../../../amq-squad-lead/T/trainfix/inputs/rows-trainfix.sh first`

```text
=== row R4 ===
edit: internal/mergetrain/gh.go:279
  before: func compareDiffMismatch(files []FileChange, diff []byte) string {
  after:  func compareDiffMismatchYours(files []FileChange, diff []byte) string {
  appended: a compareDiffMismatch that calls yours and answers "" when its answer starts with:
    a file with no changed line holds a hunk, or neither an index line nor a 100% rename's three lines:
$ go test ./internal/mergetrain/ -count=1 -timeout 240s -v -run '^(TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged|TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate|TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate|TestBehindPRWithABinaryFileIsUpdatedAndMerged|TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate|TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead|TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged|TestCompareDiffMismatchWords|TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck|TestLeadRebindOverAFileWithNoPatchIsProvedAndBound|TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged|TestIncompleteCompareRefusesWithoutRebind|TestMaxFileCompareRefusesWithoutRebind|TestHTTP422CompareRefusesWithoutRebind|TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead|TestStrictBehindUsesPinnedCompareAndRebases)$'
exit: 1
--- FAIL: TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate
--- FAIL: TestCompareDiffMismatchWords
--- PASS: TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead
--- PASS: TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate
--- PASS: TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate
--- PASS: TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged
--- PASS: TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged
--- PASS: TestBehindPRWithABinaryFileIsUpdatedAndMerged
--- PASS: TestHTTP422CompareRefusesWithoutRebind
--- PASS: TestIncompleteCompareRefusesWithoutRebind
--- PASS: TestLeadRebindOverAFileWithNoPatchIsProvedAndBound
--- PASS: TestMaxFileCompareRefusesWithoutRebind
--- PASS: TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead
--- PASS: TestStrictBehindUsesPinnedCompareAndRebases
--- PASS: TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck
--- PASS: TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged
pass 14 fail 2 skip 0
failed subtests:
    --- FAIL: TestCompareDiffMismatchWords/zero_with_hunk
    --- FAIL: TestCompareDiffMismatchWords/binary_no_index
    --- FAIL: TestCompareDiffMismatchWords/zero_only_header
    --- FAIL: TestCompareDiffMismatchWords/rename_lacks_to
    --- FAIL: TestCompareDiffMismatchWords/rename_wrong_from
    --- FAIL: TestCompareDiffMismatchWords/rename_99_percent
    --- FAIL: TestCompareDiffMismatchWords/mode_only_local_form
messages (the first 12 lines, each cut at 240 characters; commit ids printed as <sha>; the texts are your tests'):
    compare_proof_test.go:144: not refused in the fixed words: {Number:6 Task:t1 Head:<sha> Draft:true Changed:1 Files:[asset.bin note.txt] Refusals:[] GateID: GateError: Lifted:false After:[] RebaseForCI:false RebaseRule: RebaseReason: Pat
    compare_proof_test.go:245: got "", want prefix "a file with no changed line holds a hunk, or neither an index line nor a 100% rename's three lines: "
    compare_proof_test.go:245: got "", want prefix "a file with no changed line holds a hunk, or neither an index line nor a 100% rename's three lines: "
    compare_proof_test.go:245: got "", want prefix "a file with no changed line holds a hunk, or neither an index line nor a 100% rename's three lines: "
    compare_proof_test.go:245: got "", want prefix "a file with no changed line holds a hunk, or neither an index line nor a 100% rename's three lines: "
    compare_proof_test.go:245: got "", want prefix "a file with no changed line holds a hunk, or neither an index line nor a 100% rename's three lines: "
    compare_proof_test.go:245: got "", want prefix "a file with no changed line holds a hunk, or neither an index line nor a 100% rename's three lines: "
    compare_proof_test.go:245: got "", want prefix "a file with no changed line holds a hunk, or neither an index line nor a 100% rename's three lines: "
restore: gh.go cmp exit 0
```
### R5

`bash ../../../../amq-squad-lead/T/trainfix/inputs/rows-trainfix.sh first`

```text
=== row R5 ===
edit: internal/mergetrain/gh.go:279
  before: func compareDiffMismatch(files []FileChange, diff []byte) string {
  after:  func compareDiffMismatchYours(files []FileChange, diff []byte) string {
  appended: a compareDiffMismatch that refuses every file with 0 additions and 0 deletions, then calls yours
$ go test ./internal/mergetrain/ -count=1 -timeout 240s -v -run '^(TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged|TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate|TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate|TestBehindPRWithABinaryFileIsUpdatedAndMerged|TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate|TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead|TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged|TestCompareDiffMismatchWords|TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck|TestLeadRebindOverAFileWithNoPatchIsProvedAndBound|TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged|TestIncompleteCompareRefusesWithoutRebind|TestMaxFileCompareRefusesWithoutRebind|TestHTTP422CompareRefusesWithoutRebind|TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead|TestStrictBehindUsesPinnedCompareAndRebases)$'
exit: 1
--- FAIL: TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate
--- FAIL: TestBehindPRWithABinaryFileIsUpdatedAndMerged
--- FAIL: TestCompareDiffMismatchWords
--- PASS: TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead
--- PASS: TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate
--- PASS: TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate
--- PASS: TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged
--- PASS: TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged
--- PASS: TestHTTP422CompareRefusesWithoutRebind
--- PASS: TestIncompleteCompareRefusesWithoutRebind
--- PASS: TestLeadRebindOverAFileWithNoPatchIsProvedAndBound
--- PASS: TestMaxFileCompareRefusesWithoutRebind
--- PASS: TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead
--- PASS: TestStrictBehindUsesPinnedCompareAndRebases
--- PASS: TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck
--- PASS: TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged
pass 13 fail 3 skip 0
failed subtests:
    --- FAIL: TestCompareDiffMismatchWords/binary_GitHub
    --- FAIL: TestCompareDiffMismatchWords/binary_git
    --- FAIL: TestCompareDiffMismatchWords/zero_count_index
    --- FAIL: TestCompareDiffMismatchWords/rename_no_change
    --- FAIL: TestCompareDiffMismatchWords/added_empty
    --- FAIL: TestCompareDiffMismatchWords/deleted_empty_local_form
    --- FAIL: TestCompareDiffMismatchWords/rename_without_previous
    --- FAIL: TestCompareDiffMismatchWords/zero_with_hunk
    --- FAIL: TestCompareDiffMismatchWords/binary_no_index
    --- FAIL: TestCompareDiffMismatchWords/zero_only_header
    --- FAIL: TestCompareDiffMismatchWords/rename_lacks_to
    --- FAIL: TestCompareDiffMismatchWords/rename_wrong_from
    --- FAIL: TestCompareDiffMismatchWords/rename_99_percent
    --- FAIL: TestCompareDiffMismatchWords/mode_only_local_form
messages (the first 12 lines, each cut at 240 characters; commit ids printed as <sha>; the texts are your tests'):
    compare_proof_test.go:130: #6 was not rebased and merged: status refused; force pushes recorded 0; head unchanged true; task bound to its old head true; audit: refusal: the update could not be proved: GitHub returned an incomplete diff
    compare_proof_test.go:144: proof error GitHub returned an incomplete diff: rows-trainfix.sh R5: a file with no changed line is refused, want a file with no changed line holds a hunk, or neither an index line nor a 100% rename's three li
    compare_proof_test.go:242: got "rows-trainfix.sh R5: a file with no changed line is refused"
    compare_proof_test.go:242: got "rows-trainfix.sh R5: a file with no changed line is refused"
    compare_proof_test.go:242: got "rows-trainfix.sh R5: a file with no changed line is refused"
    compare_proof_test.go:242: got "rows-trainfix.sh R5: a file with no changed line is refused"
    compare_proof_test.go:242: got "rows-trainfix.sh R5: a file with no changed line is refused"
    compare_proof_test.go:242: got "rows-trainfix.sh R5: a file with no changed line is refused"
    compare_proof_test.go:245: got "rows-trainfix.sh R5: a file with no changed line is refused", want prefix "the diff's files are not the compare's files: "
    compare_proof_test.go:245: got "rows-trainfix.sh R5: a file with no changed line is refused", want prefix "a file with no changed line holds a hunk, or neither an index line nor a 100% rename's three lines: "
    compare_proof_test.go:245: got "rows-trainfix.sh R5: a file with no changed line is refused", want prefix "a file with no changed line holds a hunk, or neither an index line nor a 100% rename's three lines: "
    compare_proof_test.go:245: got "rows-trainfix.sh R5: a file with no changed line is refused", want prefix "a file with no changed line holds a hunk, or neither an index line nor a 100% rename's three lines: "
restore: gh.go cmp exit 0
```
### R6

`bash ../../../../amq-squad-lead/T/trainfix/inputs/rows-trainfix.sh first`

```text
=== row R6 ===
edit: internal/mergetrain/gh.go:240
  before: if len(files) >= MaxCompareFiles {
  after:  if len(files) >= MaxCompareFiles+1<<30 {
$ go test ./internal/mergetrain/ -count=1 -timeout 240s -v -run '^(TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged|TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate|TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate|TestBehindPRWithABinaryFileIsUpdatedAndMerged|TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate|TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead|TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged|TestCompareDiffMismatchWords|TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck|TestLeadRebindOverAFileWithNoPatchIsProvedAndBound|TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged|TestIncompleteCompareRefusesWithoutRebind|TestMaxFileCompareRefusesWithoutRebind|TestHTTP422CompareRefusesWithoutRebind|TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead|TestStrictBehindUsesPinnedCompareAndRebases)$'
exit: 1
--- FAIL: TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead
--- PASS: TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate
--- PASS: TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate
--- PASS: TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate
--- PASS: TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged
--- PASS: TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged
--- PASS: TestBehindPRWithABinaryFileIsUpdatedAndMerged
--- PASS: TestCompareDiffMismatchWords
--- PASS: TestHTTP422CompareRefusesWithoutRebind
--- PASS: TestIncompleteCompareRefusesWithoutRebind
--- PASS: TestLeadRebindOverAFileWithNoPatchIsProvedAndBound
--- PASS: TestMaxFileCompareRefusesWithoutRebind
--- PASS: TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead
--- PASS: TestStrictBehindUsesPinnedCompareAndRebases
--- PASS: TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck
--- PASS: TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged
pass 15 fail 1 skip 0
failed subtests:
    --- FAIL: TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead/at_the_limit
messages (the first 12 lines, each cut at 240 characters; commit ids printed as <sha>; the texts are your tests'):
    compare_proof_test.go:173: limit: err=GitHub returned an incomplete diff: the diff's files are not the compare's files: missing section for "f/000" reads=1 want 0
restore: gh.go cmp exit 0
```
### R7

`bash ../../../../amq-squad-lead/T/trainfix/inputs/rows-trainfix.sh first`

```text
=== row R7 ===
edit: internal/mergetrain/gh.go:444
  before: return strings.Contains(s, "diff too large") || strings.Contains(s, "http 406") || strings.Contains(s, "http 422")
  after:  return strings.Contains(s, "diff too large") || strings.Contains(s, "http 406")
$ go test ./internal/mergetrain/ -count=1 -timeout 240s -v -run '^(TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged|TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate|TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate|TestBehindPRWithABinaryFileIsUpdatedAndMerged|TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate|TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead|TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged|TestCompareDiffMismatchWords|TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck|TestLeadRebindOverAFileWithNoPatchIsProvedAndBound|TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged|TestIncompleteCompareRefusesWithoutRebind|TestMaxFileCompareRefusesWithoutRebind|TestHTTP422CompareRefusesWithoutRebind|TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead|TestStrictBehindUsesPinnedCompareAndRebases)$'
exit: 1
--- FAIL: TestHTTP422CompareRefusesWithoutRebind
--- PASS: TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate
--- PASS: TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead
--- PASS: TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate
--- PASS: TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate
--- PASS: TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged
--- PASS: TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged
--- PASS: TestBehindPRWithABinaryFileIsUpdatedAndMerged
--- PASS: TestCompareDiffMismatchWords
--- PASS: TestIncompleteCompareRefusesWithoutRebind
--- PASS: TestLeadRebindOverAFileWithNoPatchIsProvedAndBound
--- PASS: TestMaxFileCompareRefusesWithoutRebind
--- PASS: TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead
--- PASS: TestStrictBehindUsesPinnedCompareAndRebases
--- PASS: TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck
--- PASS: TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged
pass 15 fail 1 skip 0
failed subtests:
messages (the first 12 lines, each cut at 240 characters; commit ids printed as <sha>; the texts are your tests'):
    strict_policy_test.go:273: incomplete compare was not refused in words: prove the updated patch of #1: gh api repos/owner/fixture/compare/<sha>...<sha> --jq: exit status 1: gh: compare result unavailable (HTTP 422) {Number:1 Task:t1 Hea
restore: gh.go cmp exit 0
keep directory removed: yes
```
### R8

`bash ../../../../amq-squad-lead/T/trainfix/inputs/rows-trainfix.sh second`

```text
=== row R8 ===
edit: internal/mergetrain/gh.go:444
  before: return strings.Contains(s, "diff too large") || strings.Contains(s, "http 406") || strings.Contains(s, "http 422")
  after:  return strings.Contains(s, "diff too large") || strings.Contains(s, "http 422")
$ go test ./internal/mergetrain/ -count=1 -timeout 240s -v -run '^(TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged|TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate|TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate|TestBehindPRWithABinaryFileIsUpdatedAndMerged|TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate|TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead|TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged|TestCompareDiffMismatchWords|TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck|TestLeadRebindOverAFileWithNoPatchIsProvedAndBound|TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged|TestIncompleteCompareRefusesWithoutRebind|TestMaxFileCompareRefusesWithoutRebind|TestHTTP422CompareRefusesWithoutRebind|TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead|TestStrictBehindUsesPinnedCompareAndRebases)$'
exit: 1
--- FAIL: TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead
--- PASS: TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate
--- PASS: TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead
--- PASS: TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate
--- PASS: TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate
--- PASS: TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged
--- PASS: TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged
--- PASS: TestBehindPRWithABinaryFileIsUpdatedAndMerged
--- PASS: TestCompareDiffMismatchWords
--- PASS: TestHTTP422CompareRefusesWithoutRebind
--- PASS: TestIncompleteCompareRefusesWithoutRebind
--- PASS: TestLeadRebindOverAFileWithNoPatchIsProvedAndBound
--- PASS: TestMaxFileCompareRefusesWithoutRebind
--- PASS: TestStrictBehindUsesPinnedCompareAndRebases
--- PASS: TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck
--- PASS: TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged
pass 15 fail 1 skip 0
failed subtests:
    --- FAIL: TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead/a_diff_too_large
    --- FAIL: TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead/an_unknown_flag,_then_a_diff_too_large
messages (the first 12 lines, each cut at 240 characters; commit ids printed as <sha>; the texts are your tests'):
    escape_legacy_320_test.go:227: a diff too large is an incomplete diff: gh api repos/owner/fixture/compare/<sha>...<sha> -H: exit status 1: gh: Sorry, the diff exceeded the maximum number of lines (HTTP 406)
    escape_legacy_320_test.go:227: a diff too large is an incomplete diff: gh api repos/owner/fixture/compare/<sha>...<sha> -H: exit status 1: gh: Sorry, the diff exceeded the maximum number of lines (HTTP 406)
restore: gh.go cmp exit 0
```
### R9

`bash ../../../../amq-squad-lead/T/trainfix/inputs/rows-trainfix.sh second`

```text
=== row R9 ===
edit: internal/mergetrain/gh.go:279
  before: func compareDiffMismatch(files []FileChange, diff []byte) string {
  after:  func compareDiffMismatchYours(files []FileChange, diff []byte) string {
  appended: a compareDiffMismatch that calls yours and answers "" when its answer starts with:
    the diff is cut short inside a hunk:
$ go test ./internal/mergetrain/ -count=1 -timeout 240s -v -run '^(TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged|TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate|TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate|TestBehindPRWithABinaryFileIsUpdatedAndMerged|TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate|TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead|TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged|TestCompareDiffMismatchWords|TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck|TestLeadRebindOverAFileWithNoPatchIsProvedAndBound|TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged|TestIncompleteCompareRefusesWithoutRebind|TestMaxFileCompareRefusesWithoutRebind|TestHTTP422CompareRefusesWithoutRebind|TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead|TestStrictBehindUsesPinnedCompareAndRebases)$'
exit: 1
--- FAIL: TestCompareDiffMismatchWords
--- FAIL: TestIncompleteCompareRefusesWithoutRebind
--- PASS: TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate
--- PASS: TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead
--- PASS: TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate
--- PASS: TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate
--- PASS: TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged
--- PASS: TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged
--- PASS: TestBehindPRWithABinaryFileIsUpdatedAndMerged
--- PASS: TestHTTP422CompareRefusesWithoutRebind
--- PASS: TestLeadRebindOverAFileWithNoPatchIsProvedAndBound
--- PASS: TestMaxFileCompareRefusesWithoutRebind
--- PASS: TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead
--- PASS: TestStrictBehindUsesPinnedCompareAndRebases
--- PASS: TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck
--- PASS: TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged
pass 14 fail 2 skip 0
failed subtests:
    --- FAIL: TestCompareDiffMismatchWords/cut_final_context
    --- FAIL: TestCompareDiffMismatchWords/next_section_inside_hunk
    --- FAIL: TestCompareDiffMismatchWords/blank_end_cut
messages (the first 12 lines, each cut at 240 characters; commit ids printed as <sha>; the texts are your tests'):
    compare_proof_test.go:245: got "", want prefix "the diff is cut short inside a hunk: "
    compare_proof_test.go:245: got "", want prefix "the diff is cut short inside a hunk: "
    compare_proof_test.go:245: got "", want prefix "the diff is cut short inside a hunk: "
    strict_policy_test.go:88: incomplete compare was not refused in words: <nil> {Number:1 Task:t1 Head:<sha> Draft:true Changed:2 Files:[pkg/a.go] Refusals:[{Rule:rebase_patch_changed Detail:its change moved during the update}] GateID: Gat
restore: gh.go cmp exit 0
```
### R10

`bash ../../../../amq-squad-lead/T/trainfix/inputs/rows-trainfix.sh second`

```text
=== row R10 ===
edit: internal/mergetrain/gh.go:279
  before: func compareDiffMismatch(files []FileChange, diff []byte) string {
  after:  func compareDiffMismatchYours(files []FileChange, diff []byte) string {
  appended: a compareDiffMismatch that calls yours with every file's previous name emptied
$ go test ./internal/mergetrain/ -count=1 -timeout 240s -v -run '^(TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged|TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate|TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate|TestBehindPRWithABinaryFileIsUpdatedAndMerged|TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate|TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead|TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged|TestCompareDiffMismatchWords|TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck|TestLeadRebindOverAFileWithNoPatchIsProvedAndBound|TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged|TestIncompleteCompareRefusesWithoutRebind|TestMaxFileCompareRefusesWithoutRebind|TestHTTP422CompareRefusesWithoutRebind|TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead|TestStrictBehindUsesPinnedCompareAndRebases)$'
exit: 1
--- FAIL: TestCompareDiffMismatchWords
--- PASS: TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate
--- PASS: TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead
--- PASS: TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate
--- PASS: TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate
--- PASS: TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged
--- PASS: TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged
--- PASS: TestBehindPRWithABinaryFileIsUpdatedAndMerged
--- PASS: TestHTTP422CompareRefusesWithoutRebind
--- PASS: TestIncompleteCompareRefusesWithoutRebind
--- PASS: TestLeadRebindOverAFileWithNoPatchIsProvedAndBound
--- PASS: TestMaxFileCompareRefusesWithoutRebind
--- PASS: TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead
--- PASS: TestStrictBehindUsesPinnedCompareAndRebases
--- PASS: TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck
--- PASS: TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged
pass 15 fail 1 skip 0
failed subtests:
    --- FAIL: TestCompareDiffMismatchWords/renamed_with_changes
    --- FAIL: TestCompareDiffMismatchWords/rename_no_change
    --- FAIL: TestCompareDiffMismatchWords/rename_lacks_to
    --- FAIL: TestCompareDiffMismatchWords/rename_wrong_from
    --- FAIL: TestCompareDiffMismatchWords/rename_99_percent
messages (the first 12 lines, each cut at 240 characters; commit ids printed as <sha>; the texts are your tests'):
    compare_proof_test.go:242: got "the diff's files are not the compare's files: unexpected section \"diff --git a/old.txt b/new.txt\""
    compare_proof_test.go:242: got "the diff's files are not the compare's files: unexpected section \"diff --git a/old.txt b/new.txt\""
    compare_proof_test.go:245: got "the diff's files are not the compare's files: unexpected section \"diff --git a/old.txt b/new.txt\"", want prefix "a file with no changed line holds a hunk, or neither an index line nor a 100% rename's th
    compare_proof_test.go:245: got "the diff's files are not the compare's files: unexpected section \"diff --git a/old.txt b/new.txt\"", want prefix "a file with no changed line holds a hunk, or neither an index line nor a 100% rename's th
    compare_proof_test.go:245: got "the diff's files are not the compare's files: unexpected section \"diff --git a/old.txt b/new.txt\"", want prefix "a file with no changed line holds a hunk, or neither an index line nor a 100% rename's th
restore: gh.go cmp exit 0
```
### R11

`bash ../../../../amq-squad-lead/T/trainfix/inputs/rows-trainfix.sh second`

```text
=== row R11 ===
edit: internal/mergetrain/gh.go:279
  before: func compareDiffMismatch(files []FileChange, diff []byte) string {
  after:  func compareDiffMismatchYours(files []FileChange, diff []byte) string {
  appended: a compareDiffMismatch that calls yours and answers "" to its counts text when the totals over all files agree
$ go test ./internal/mergetrain/ -count=1 -timeout 240s -v -run '^(TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged|TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate|TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate|TestBehindPRWithABinaryFileIsUpdatedAndMerged|TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate|TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead|TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged|TestCompareDiffMismatchWords|TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck|TestLeadRebindOverAFileWithNoPatchIsProvedAndBound|TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged|TestIncompleteCompareRefusesWithoutRebind|TestMaxFileCompareRefusesWithoutRebind|TestHTTP422CompareRefusesWithoutRebind|TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead|TestStrictBehindUsesPinnedCompareAndRebases)$'
exit: 1
--- FAIL: TestCompareDiffMismatchWords
--- PASS: TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate
--- PASS: TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead
--- PASS: TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate
--- PASS: TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate
--- PASS: TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged
--- PASS: TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged
--- PASS: TestBehindPRWithABinaryFileIsUpdatedAndMerged
--- PASS: TestHTTP422CompareRefusesWithoutRebind
--- PASS: TestIncompleteCompareRefusesWithoutRebind
--- PASS: TestLeadRebindOverAFileWithNoPatchIsProvedAndBound
--- PASS: TestMaxFileCompareRefusesWithoutRebind
--- PASS: TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead
--- PASS: TestStrictBehindUsesPinnedCompareAndRebases
--- PASS: TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck
--- PASS: TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged
pass 15 fail 1 skip 0
failed subtests:
    --- FAIL: TestCompareDiffMismatchWords/swapped_per-file_counts
messages (the first 12 lines, each cut at 240 characters; commit ids printed as <sha>; the texts are your tests'):
    compare_proof_test.go:245: got "", want prefix "the diff's added and removed lines are not the compare's counts: "
restore: gh.go cmp exit 0
```
### R12

`bash ../../../../amq-squad-lead/T/trainfix/inputs/rows-trainfix.sh second`

```text
=== row R12 ===
edit: internal/mergetrain/gh.go:265
  before: cmd.Stdin = bytes.NewReader(diff)
  after:  cmd.Stdin = bytes.NewReader(diff[:len(diff)/2])
$ go test ./internal/mergetrain/ -count=1 -timeout 240s -v -run '^(TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged|TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate|TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate|TestBehindPRWithABinaryFileIsUpdatedAndMerged|TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate|TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead|TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged|TestCompareDiffMismatchWords|TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck|TestLeadRebindOverAFileWithNoPatchIsProvedAndBound|TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged|TestIncompleteCompareRefusesWithoutRebind|TestMaxFileCompareRefusesWithoutRebind|TestHTTP422CompareRefusesWithoutRebind|TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead|TestStrictBehindUsesPinnedCompareAndRebases)$'
exit: 1
--- FAIL: TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck
--- PASS: TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate
--- PASS: TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead
--- PASS: TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate
--- PASS: TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate
--- PASS: TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged
--- PASS: TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged
--- PASS: TestBehindPRWithABinaryFileIsUpdatedAndMerged
--- PASS: TestCompareDiffMismatchWords
--- PASS: TestHTTP422CompareRefusesWithoutRebind
--- PASS: TestIncompleteCompareRefusesWithoutRebind
--- PASS: TestLeadRebindOverAFileWithNoPatchIsProvedAndBound
--- PASS: TestMaxFileCompareRefusesWithoutRebind
--- PASS: TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead
--- PASS: TestStrictBehindUsesPinnedCompareAndRebases
--- PASS: TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged
pass 15 fail 1 skip 0
failed subtests:
messages (the first 12 lines, each cut at 240 characters; commit ids printed as <sha>; the texts are your tests'):
    compare_proof_test.go:291: patch id "<sha>" err <nil>
restore: gh.go cmp exit 0
```
### R13

`bash ../../../../amq-squad-lead/T/trainfix/inputs/rows-trainfix.sh second`

```text
=== row R13 ===
edit: internal/mergetrain/gh.go:279
  before: func compareDiffMismatch(files []FileChange, diff []byte) string {
  after:  func compareDiffMismatchYours(files []FileChange, diff []byte) string {
  appended: a compareDiffMismatch that calls yours with the diff's end trimmed
$ go test ./internal/mergetrain/ -count=1 -timeout 240s -v -run '^(TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged|TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate|TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate|TestBehindPRWithABinaryFileIsUpdatedAndMerged|TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate|TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead|TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged|TestCompareDiffMismatchWords|TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck|TestLeadRebindOverAFileWithNoPatchIsProvedAndBound|TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged|TestIncompleteCompareRefusesWithoutRebind|TestMaxFileCompareRefusesWithoutRebind|TestHTTP422CompareRefusesWithoutRebind|TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead|TestStrictBehindUsesPinnedCompareAndRebases)$'
exit: 1
--- FAIL: TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged
--- FAIL: TestCompareDiffMismatchWords
--- PASS: TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate
--- PASS: TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead
--- PASS: TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate
--- PASS: TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate
--- PASS: TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged
--- PASS: TestBehindPRWithABinaryFileIsUpdatedAndMerged
--- PASS: TestHTTP422CompareRefusesWithoutRebind
--- PASS: TestIncompleteCompareRefusesWithoutRebind
--- PASS: TestLeadRebindOverAFileWithNoPatchIsProvedAndBound
--- PASS: TestMaxFileCompareRefusesWithoutRebind
--- PASS: TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead
--- PASS: TestStrictBehindUsesPinnedCompareAndRebases
--- PASS: TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck
--- PASS: TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged
pass 14 fail 2 skip 0
failed subtests:
    --- FAIL: TestCompareDiffMismatchWords/blank_context_end
messages (the first 12 lines, each cut at 240 characters; commit ids printed as <sha>; the texts are your tests'):
    compare_proof_test.go:242: got "the diff is cut short inside a hunk: \"a.txt\""
    compare_proof_test.go:358: #6 was not rebased and merged: status refused; force pushes recorded 0; head unchanged true; task bound to its old head true; audit: refusal: the update could not be proved: GitHub returned an incomplete diff
restore: gh.go cmp exit 0
```
### R14

`bash ../../../../amq-squad-lead/T/trainfix/inputs/rows-trainfix.sh second`

```text
=== row R14 ===
edit: internal/mergetrain/gh.go:279
  before: func compareDiffMismatch(files []FileChange, diff []byte) string {
  after:  func compareDiffMismatchYours(files []FileChange, diff []byte) string {
  appended: a compareDiffMismatch that calls yours and answers "" to its third text when every file with no changed line has a section line that starts "index "
$ go test ./internal/mergetrain/ -count=1 -timeout 240s -v -run '^(TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged|TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate|TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate|TestBehindPRWithABinaryFileIsUpdatedAndMerged|TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate|TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead|TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged|TestCompareDiffMismatchWords|TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck|TestLeadRebindOverAFileWithNoPatchIsProvedAndBound|TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged|TestIncompleteCompareRefusesWithoutRebind|TestMaxFileCompareRefusesWithoutRebind|TestHTTP422CompareRefusesWithoutRebind|TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead|TestStrictBehindUsesPinnedCompareAndRebases)$'
exit: 1
--- FAIL: TestCompareDiffMismatchWords
--- PASS: TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate
--- PASS: TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead
--- PASS: TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate
--- PASS: TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate
--- PASS: TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged
--- PASS: TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged
--- PASS: TestBehindPRWithABinaryFileIsUpdatedAndMerged
--- PASS: TestHTTP422CompareRefusesWithoutRebind
--- PASS: TestIncompleteCompareRefusesWithoutRebind
--- PASS: TestLeadRebindOverAFileWithNoPatchIsProvedAndBound
--- PASS: TestMaxFileCompareRefusesWithoutRebind
--- PASS: TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead
--- PASS: TestStrictBehindUsesPinnedCompareAndRebases
--- PASS: TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck
--- PASS: TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged
pass 15 fail 1 skip 0
failed subtests:
    --- FAIL: TestCompareDiffMismatchWords/zero_with_hunk
messages (the first 12 lines, each cut at 240 characters; commit ids printed as <sha>; the texts are your tests'):
    compare_proof_test.go:245: got "", want prefix "a file with no changed line holds a hunk, or neither an index line nor a 100% rename's three lines: "
restore: gh.go cmp exit 0
```
### R15

`bash ../../../../amq-squad-lead/T/trainfix/inputs/rows-trainfix.sh second`

```text
=== row R15 ===
edit: internal/mergetrain/gh.go:279
  before: func compareDiffMismatch(files []FileChange, diff []byte) string {
  after:  func compareDiffMismatchYours(files []FileChange, diff []byte) string {
  appended: a compareDiffMismatch that calls yours and answers "" to its third text when every file with no changed line has a section line that is "similarity index 100%"
$ go test ./internal/mergetrain/ -count=1 -timeout 240s -v -run '^(TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged|TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate|TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate|TestBehindPRWithABinaryFileIsUpdatedAndMerged|TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate|TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead|TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged|TestCompareDiffMismatchWords|TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck|TestLeadRebindOverAFileWithNoPatchIsProvedAndBound|TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged|TestIncompleteCompareRefusesWithoutRebind|TestMaxFileCompareRefusesWithoutRebind|TestHTTP422CompareRefusesWithoutRebind|TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead|TestStrictBehindUsesPinnedCompareAndRebases)$'
exit: 1
--- FAIL: TestCompareDiffMismatchWords
--- PASS: TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate
--- PASS: TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead
--- PASS: TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate
--- PASS: TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate
--- PASS: TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged
--- PASS: TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged
--- PASS: TestBehindPRWithABinaryFileIsUpdatedAndMerged
--- PASS: TestHTTP422CompareRefusesWithoutRebind
--- PASS: TestIncompleteCompareRefusesWithoutRebind
--- PASS: TestLeadRebindOverAFileWithNoPatchIsProvedAndBound
--- PASS: TestMaxFileCompareRefusesWithoutRebind
--- PASS: TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead
--- PASS: TestStrictBehindUsesPinnedCompareAndRebases
--- PASS: TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck
--- PASS: TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged
pass 15 fail 1 skip 0
failed subtests:
    --- FAIL: TestCompareDiffMismatchWords/rename_lacks_to
    --- FAIL: TestCompareDiffMismatchWords/rename_wrong_from
messages (the first 12 lines, each cut at 240 characters; commit ids printed as <sha>; the texts are your tests'):
    compare_proof_test.go:245: got "", want prefix "a file with no changed line holds a hunk, or neither an index line nor a 100% rename's three lines: "
    compare_proof_test.go:245: got "", want prefix "a file with no changed line holds a hunk, or neither an index line nor a 100% rename's three lines: "
restore: gh.go cmp exit 0
keep directory removed: yes
```


## What ran

`go build ./...` exited 0 and printed nothing.

`go vet ./internal/mergetrain/` exited 0 and printed nothing.

`gofmt -l internal/mergetrain` exited 0 and printed nothing.

`bash ../../../../amq-squad-lead/T/trainfix/inputs/rows-trainfix.sh R0` first exposed an invalid hunk header in the test's ground and exited 0 as a driver call with 15 passing tests and one failing test; after that ground was corrected, the same command exited 0 as a driver call with 16 passes and 0 failures.

`bash ../../../../amq-squad-lead/T/trainfix/inputs/rows-trainfix.sh first` exited 0 as a driver call; R0 exited 0 with 16/0, and R1 through R7 exited 1 with exactly the required failing tests and counts shown above.

`bash ../../../../amq-squad-lead/T/trainfix/inputs/rows-trainfix.sh second` exited 0 as a driver call; R8 through R15 exited 1 with exactly the required failing tests and counts shown above.

`go run ./scripts/redact-evidence -check docs/v3-spike/330-train-compare-proof/` exited 0 and printed nothing.

`git diff --check` exited 0 and printed nothing.

`git diff --no-index --check /dev/null docs/v3-spike/330-train-compare-proof/README.md` first exited 3 on four driver-emitted spaces after mismatch labels; after those four trailing bytes were removed to satisfy the note's whitespace rule, it printed nothing and exited 1.

`git diff --no-index --check /dev/null internal/mergetrain/compare_proof_test.go` printed nothing and exited 1.

`git diff --no-index --check /dev/null internal/mergetrain/testdata/compare-b46cb806-18dd4997.json` printed nothing and exited 1.

`git hash-object internal/mergetrain/testdata/compare-b46cb806-18dd4997.json` exited 0 and printed `fe0ff3c4d3fb09d2554a353e2976d42c5bd4caf3`.

`grep -cE '/Us''ers/|/private/t''mp|rows-trainfix-ke''ep' docs/v3-spike/330-train-compare-proof/README.md` printed `0` and exited 1.

`grep -cE '[[:space:]]+$' docs/v3-spike/330-train-compare-proof/README.md` printed `0` and exited 1.

`git status --porcelain --untracked-files=all` printed the seven declared paths and exited 0.

`git diff --stat` printed the four modified files and no other path.

### `python3 ../../../../amq-squad-lead/T/trainfix/inputs/cites.py`

`python3 ../../../../amq-squad-lead/T/trainfix/inputs/cites.py docs/v3-spike/330-train-compare-proof/README.md` exited 0 and printed:

```text
note:3 internal/mergetrain/gh.go:227-434 | first: func (g *GH) PatchID(ctx context.Context, repository, base,  | last: } | next:  | closes inside
note:37 internal/mergetrain/gh.go:240-242 | first: if len(files) >= MaxCompareFiles { | last: } | next: // gh refuses to print a response holdin | closes inside
note:39 internal/mergetrain/gh.go:71-89 | first: func (g *GH) exec(ctx context.Context, args []string) ([]byt | last: } | next:  | closes inside
note:39 internal/mergetrain/gh.go:39-53 | first: func (g *GH) run(ctx context.Context, args ...string) ([]byt | last: } | next:  | closes inside
note:39 internal/mergetrain/fakegh_test.go:184-190 | first: // gitBytes runs fixture git without trimming its output byt | last: } | next:  | closes inside
note:39 internal/mergetrain/fakegh_test.go:500-528 | first: if slices.Contains(args, "Accept: application/vnd.github.dif | last: } | next: if slices.Contains(args, ".behind_by") { | closes inside
note:51 internal/mergetrain/gh.go:200-201 | first: // compareProofFilesJQ is the file list PatchID checks again | last: const compareProofFilesJQ = `.files[]? | {path: .filename, p | next:  | closes inside
note:51 internal/mergetrain/gh.go:227-275 | first: func (g *GH) PatchID(ctx context.Context, repository, base,  | last: } | next:  | closes inside
note:63 internal/mergetrain/gh.go:261-263 | first: if mismatch := compareDiffMismatch(files, diff); mismatch != | last: } | next: cmd := exec.CommandContext(ctx, "git", " | closes inside
note:105 internal/mergetrain/fakegh_test.go:64-71 | first: // NoPatchFiles are files for which the completeness answer  | last: ExtraAdditionFiles []string `json:"extra_addition_files,omit | next: CIName             string   `json:"ci_na | closes inside
cites 10, short 0
```

The seat ran the sixteen keyed tests by name and no package whole; the whole run of internal/mergetrain is the lead's, on the PR head.
