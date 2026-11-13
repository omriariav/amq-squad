# The merge train's compare proof

The merge train checks that GitHub's API diff and compare file list describe the same change. The file-list constant is at `internal/mergetrain/gh.go:200-201`, and the check is at `internal/mergetrain/gh.go:279-471`. The dispatch TIP of the third round's task was `8c915ba47b7d90a2de38af0bd0a53436c43e22bc`, and the worktree's HEAD at the precheck was that commit. The rule is:

```text
The API diff stays the proof's bytes; the `patch == null` arm is replaced by the train's own check that the diff and the JSON describe the same change: the diff's `diff --git` file set equals the JSON's filenames (previous_filename for renames), and per file the diff's added and removed line counts equal the JSON's `additions` and `deletions`; the 300-file arm and the 406/422 arms stay; any mismatch is ErrIncompleteDiff with its words. No fetch, no credentials, no repository path in the package, no change to any id, and the rebind gets it through the same PatchID. A truncated 200 is caught by the counts, not assumed away; the note says the media type's wholeness on a 200 is otherwise unmeasured.

a 0/0 file's section holds no hunk, and either an `index` line or a 100% rename's three lines

(i) a non-empty diff ends with a newline; otherwise it is cut short

(ii) outside a hunk a file's section holds only header lines (`diff --git`, the mode lines, `similarity`/`rename`/`copy`, `index`, `---`/`+++`, `Binary files … differ`, a `GIT binary patch` section's own lines, and the line `\ No newline at end of file` directly after a hunk); any other line is a mismatch

(iii) the diff's first non-empty line is a `diff --git` line; anything before it is the first mismatch

inside a hunk, a `\ No newline at end of file` line may stand directly after any line of the hunk (a `-`, `+` or context line) and counts on neither side; the hunk's lengths are read over the other lines

(iv) a 0/0 file whose section holds an `index` line also holds a `Binary files … differ` line or a `GIT binary patch` part, unless the index line's ids are the zero id and the empty blob's (an added or deleted empty file)

(v) in a hunk header each start and each length is one or more digits and nothing else; a line that starts `@@ ` and is not such a header is a cut hunk

(vi) in a `GIT binary patch` part a data line's first letter gives the bytes it holds (`A` to `Z` 1 to 26, `a` to `z` 27 to 52) and the line holds five characters after the letter for every four bytes, the last group whole; a data line of another length is a stray line
```

The second line replaced an earlier clause after measurement. #998 was this task's first PR and did not merge: two existing tests of the package failed on its head and its check accepted a diff cut inside its last line; this PR carries #998's head `88934e527615c31486eca750efe341ae7ef3f90d` and replaces it.

#999 was this task's second PR and did not merge: its check refused two whole diffs of this repository's history, each with a `\ No newline at end of file` line inside a hunk, and accepted a binary file's section cut after its index line; this PR carries #999's head `5312c778d79220d19d2f7194b63c0350cc6cc05e` and replaces it.

## What was measured

On 2026-11-12 at `7542de96b745bb23f0465539e76f0de1f6cfe396`, the lead measured #991's compare. Its JSON named two files, with additions and deletions 551/411 and 7/3, while the 962-line note had no `patch`; the 136,190-byte diff held both sections and exactly those per-file counts. The refusal was caused by the JSON `patch` test, not an incomplete diff.

Across eight repository pairs and forty files, every diff section began `diff --git a/<previous_filename, or filename> b/<filename>`, and hunk-length accounting matched JSON additions and deletions. The measured 0/0 forms came from the binary pair `1d1e5f7e7a08...b5af721a90f1`, unchanged-rename pair `a2d1f87e6cbe...82b4b6c2b5df`, and added-empty pair `01d111900205...465b2651f004`.

As GitHub served them, those three sections were:

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

Across 1,930 non-merge commits and 15,651 files, hunk lengths agreed with `git diff --numstat -M` in every case. In the history, 5 of the 1,930 non-merge commits with a parent hold a rename with no change and 6 an added empty file; none holds a deleted empty file. Counting lines only by their first character failed on `5b92601b9d03`, whose removed text began `-- the copy-ready command ...`, and `656f61353c2d`, whose removed text began `-- launch fails closed naming that exact command otherwise.` A `\ No newline at end of file` marker counted on neither side.

The lead's measurement on 2026-11-12 and 2026-11-13 found 69 marker lines in 26 history commits, identically with `git diff -M` and `git diff -M --binary`: 2 inside a hunk after a `-` line with hunk lines following, 0 inside after context or `+`, 64 after a hunk's last `+`, 2 after its last `-`, and 1 after its last context line. One of the 64 is the second marker of the one hunk with two. The two commits with an inside marker were `207b66dbfc7bdd6830c16ac74c8e86d75d09a3fd...d5dcf85599b9424432bb34586d3865f723630491` in `docs/v2.0.0-goal-condensed.md` and `91f618abda3fafd42b5f941351f683e6bb85cc4f...41cafd2fa45ba79e181a17a88cff76f244e85432` in `README.md`.

On #998's head, the carried check and the lead's reference both accepted these four malformed forms: a one-hunk diff cut inside its final `+sec` line; a 0/0 diff cut inside its `index ` line; an out-of-hunk `+unaccounted content` line; and a complete one-hunk diff with only its final newline removed. Across GitHub's 8 saved answers, every byte offset produced 274,352 cuts. The check accepted 40,749: 40,713 ended inside the last content line and changed `git patch-id --stable`; 36 lost only the final newline, or cut in or before a final marker, and retained the id. Inserting a line starting `+`, `-`, or a space after every header and at every section end produced 879 inputs; all were accepted and 849 changed the patch id.

With rules (i), (ii), and (iii), the lead's reference refused none of GitHub's 8 saved answers, the 1,930 history diffs made with `git diff -M` and with `git diff -M --binary`, or the 11 distinct diffs served in 145 fake answers. Of the 274,352 cuts, one remains accepted: the cut just before a final marker, whose patch id is unchanged. None of the 879 inserted lines remains accepted. No measured diff starts with text or an empty line before its first `diff --git` line.

Every out-of-hunk line form found in those sources had these counts, in the order GitHub's 8 answers / history / history with `--binary` / fake:

| line form | GitHub | history | binary history | fake |
|---|---:|---:|---:|---:|
| `diff --git` | 40 | 15,651 | 15,651 | 17 |
| `index` | 39 | 15,629 | 15,629 | 17 |
| `---` | 36 | 15,580 | 15,580 | 16 |
| `+++` | 36 | 15,580 | 15,580 | 16 |
| `new file mode` | 6 | 3,383 | 3,383 | 7 |
| `deleted file mode` | 2 | 570 | 570 | 0 |
| `similarity index` | 2 | 37 | 37 | 0 |
| `rename from` | 2 | 37 | 37 | 0 |
| `rename to` | 2 | 37 | 37 | 0 |
| `Binary files ... differ` | 1 | 1 | 0 | 0 |
| `\ No newline at end of file` after a hunk | 1 | 67 | 67 | 0 |
| `\ No newline at end of file` inside a hunk | 0 | 2 | 2 | 0 |
| `GIT binary patch` | 0 | 0 | 1 | 1 |
| `literal N` | 0 | 0 | 2 | 2 |
| a base85 data line | 0 | 0 | 15,026 | 2 |
| an empty binary-patch line | 0 | 0 | 2 | 2 |
| anything else, or an empty line elsewhere | 0 | 0 | 0 | 0 |

The forms `old mode`, `new mode`, `dissimilarity index`, `copy from`, `copy to`, and `delta N` are documented by git but were not measured in a diff of GitHub's or of the history.

Removing each of the 3,739 lines of GitHub's 8 saved answers in turn left 120 accepted diffs. The patch id changed in 83: 72 removed `---` or `+++`, 8 removed mode lines, and 3 removed a rename or similarity line from a rename with changes. The remaining 37, comprising 36 `index` lines and 1 marker, retained the id. A prefix cut produces none of these.

A 2,903-file compare returned 300 JSON files and no file on a second page. The diff media type answered 54,652,486 bytes in 2,904 sections: it showed the pair's one rename as a deletion and an addition. Against local `git diff --no-renames --abbrev=9` of the same pair it has the same 783,733 lines but for the text after the second `@@` of 10 hunk headers, and the same `git patch-id --stable`. One of four reads of it was answered HTTP 422, "The diff is taking too long to generate." Therefore a JSON list at the 300-file limit is refused before the diff is read (`internal/mergetrain/gh.go:240-242`).

The real path was measured on gh 2.100.0 not to trim. The 11,590-byte compare `3bf07c72bccc...e7b23c061595` ended in `"for 3.2.x\n \n"`, byte for byte the local diff. Of 1,930 measured commits, 202 diffs ended in a blank context line. The fake writes those bytes directly at `internal/mergetrain/fakegh_test.go:502-530`. A proxy stripping trailing whitespace and gh versions other than 2.100.0 are unmeasured and fail closed as a cut hunk.

The diff media type's wholeness on a 200 is otherwise unmeasured: the check, and not the status, is what says a diff is whole.

A binary file's patch id takes in its index line, whose object ids GitHub abbreviates to a length that was 8 on some compares and 9 on others; both sides of one proof come from the API's diff, so the dependence is the same on both sides, and no test holds an abbreviated index line on both sides of one proof.

A patch id journaled for a PR with a binary file before this change would not be comparable with one computed after it; none exists, since a behind, bound PR with a binary file was refused.

The id pinned by the tests is of a text-only pair; the id of a pair with a binary file, which takes in the index line's abbreviation, is not pinned.

For a binary section placed last, the lead tried every byte cut. GitHub's 5,034-byte saved answer admitted one cut, directly after `index 000000000..8dbd5ec6b`, and its patch id changed. Local git's added, modified, and deleted binary forms each admitted the corresponding cut after the index and changed the id. In the fake's `git diff -M --binary` form, the check also admitted six line-boundary cuts inside the binary patch part, each retaining the whole diff's id.

Every measured no-hunk section was one of four forms: a binary file with an index and `Binary files … differ`; a binary file with an index and a `GIT binary patch` part; an added empty file whose index ids were zeros and an `e69de29…` prefix; or a 100% rename's three lines without an index. GitHub used 9-digit ids, the fake 7 and 40, and the history 8 and 40. Local git printed both added and deleted empty files at 7, 8, 9, and 40 digits. The deleted form was documented and unseen in measured diffs.

The made cases showed that #999 accepted a 0/0 index-only binary prefix, hunk headers `@@ -garbage,0 +garbage @@` and `@@ -1,+1 +1,+1 @@`, and the binary data line `hello`. None of 34,557 history hunk headers or 136 GitHub hunk headers had a non-digit number. None of 15,026 history binary data lines or 2 fake data lines had a length different from the first letter's encoding.

The lead's final mutation measurement refused none of GitHub's 8 saved answers and blank-ending diff, the 1,930 history commits in either form, or the fake's 12 distinct diffs. Of 274,352 prefix cuts, only the cut before a final marker remained accepted, with the same patch id. None of 879 insertions was accepted. Of 3,739 single-line removals, 120 remained accepted and 83 changed the patch id, as counted above.

On 2026-11-13 the lead built 24 forms with local git in a temporary repository, each as `git diff -M --abbrev=9` and as `git diff -M --binary`, 48 diffs in all, with the file list from local git. Forty-two were accepted and six were refused, the two forms of three cases: the type change and quoted name with the first text, and the mode change alone with the third. These are LOCAL GIT'S forms; GitHub's form of each is not measured.

| form | what local git prints | verdict |
|---|---|---|
| symlink added | `new file mode 120000`, one `+` target line and marker | accepted |
| symlink changed | `index … 120000`, old and new target lines, two markers | accepted |
| symlink deleted | `deleted file mode 120000`, one `-` target line and marker | accepted |
| submodule added | `new file mode 160000`, one `+Subproject commit …` line | accepted |
| submodule changed | `index … 160000`, old and new `Subproject commit …` lines | accepted |
| submodule deleted | `deleted file mode 160000`, one removed `Subproject commit …` line | accepted |
| text to binary | binary numstat, index and `Binary files … differ` | accepted |
| binary to text | binary numstat, index and `Binary files … differ` | accepted |
| file emptied | a zero-new-side hunk and three removed lines | accepted |
| empty file given content | a zero-old-side hunk and one added line | accepted |
| empty file renamed | 100% similarity and the two rename lines | accepted |
| empty file's mode changed | old and new mode, no index, 0/0 numstat | third text |
| binary file renamed unchanged | the three rename lines, binary numstat | accepted |
| binary file renamed with change | 97% similarity, rename lines, index and binary body | accepted |
| regular file becomes symlink | two sections with one `diff --git` line for one list entry | first text |
| file added without final newline | marker after its last added line | accepted |
| file without final newline deleted | marker after its last removed line | accepted |
| line added at file start | hunk `@@ -1,3 +1,4 @@` | accepted |
| line added at file end | a hunk whose last line is added | accepted |
| CRLF content changed | removed and added lines ending in carriage returns | accepted |
| no-newline line changed | old line and marker, new line and marker | accepted |
| rename changed, old file without final newline | 93% similarity and a marker inside the hunk | accepted |
| mode and content changed | old mode, new mode, index and a hunk | accepted |
| space name and quoted name added | the space name is taken; the quoted name's header is unexpected | first text |

## What the check does

`PatchID` reads the file list, refuses 300 files before reading the diff, reads the diff bytes unchanged, checks them, and passes those same bytes to `git patch-id --stable` (`internal/mergetrain/gh.go:227-275`). Its file-list query is unique among `gh.go` calls:

```jq
.files[]? | {path: .filename, previous_path: (.previous_filename // ""), additions, deletions, status}
```

The seven mismatch texts, returned in diff order before list-order checks, are:

1. `the diff's files are not the compare's files` means a listed section is absent, unexpected, duplicated, or does not use the list's previous filename. Text before the first `diff --git` line also gives this mismatch; an empty line before the first section is allowed.
2. `the diff's added and removed lines are not the compare's counts` means a file's completed hunks do not equal that file's JSON additions and deletions.
3. `a file with no changed line holds a hunk, or neither an index line nor a 100% rename's three lines` means a 0/0 section lacks the measured header evidence or improperly carries a hunk.
4. `the diff is cut short inside a hunk` means a hunk ends, another section starts, or a non-diff line appears before the hunk header's old and new lengths are consumed. A hunk header whose start or length is not digits also gives this text.
5. `the diff does not end with a newline` is returned first when a non-empty diff's final byte is not a newline.
6. `a file's section holds a line that is neither a header line nor in a hunk` means an out-of-hunk line is not one of the allowed forms below. A binary data line whose length does not match its first letter also gives this text.
7. `a file with no changed line holds an index line and neither a binary part nor an empty file's ids` means a 0/0 file passed the third check on an index line but has neither a binary body nor the exact empty-file exception.

Measured header forms are `index `; `new file mode ` and `deleted file mode `; `similarity index `, `rename from `, and `rename to `; `Binary files ... differ`; and `--- ` or `+++ ` before the first hunk. Forms documented by git but unmeasured in a diff of GitHub's or of the history are `old mode `, `new mode `, `dissimilarity index `, `copy from `, and `copy to `; the follow-up measured the first two in local git's output. A marker is allowed directly after a line of a hunk, inside it or after its last line, and counts on neither side. At most two markers belong to one hunk; a third gives the sixth text. The place after a `-` line was measured; the allowed places after a context or `+` line inside a hunk were unseen. A `GIT binary patch` section with no hunk allows measured `literal N`, documented `delta N`, measured base85 data, and measured empty lines; the follow-up measured `delta N` in local git's output. The section recognizer is at `internal/mergetrain/gh.go:279-471`, with the binary-body grammar at `internal/mergetrain/gh.go:498-529`.

The empty-file exception recognizes the two ids in either order: a run of 7 to 40 zeros and a 7- to 40-character prefix of `e69de29bb2d1d6434b8b29ae775ad8c2e48c5391`. The zeros-first added form is measured. The deleted empty file's reversed form is allowed, documented, and unseen. `index 0000..e69d` is refused: git's shortest abbreviation is read as 4 digits, and no id shorter than 7 was measured served.

Each mismatch wraps `ErrIncompleteDiff`, preserving `errors.Is`, while callers retain the fixed owner-facing refusal (`internal/mergetrain/gh.go:261-263`).

## Known limits

The check refuses every cut and every stray line measured that would change the patch id; two cuts that cannot change it are accepted (before a final marker line; inside a `GIT binary patch` part).

The check does not prove every header line is present: a diff with one header line removed from its middle is accepted in 120 of 3,739 measured removals, and in 83 of them the patch id differs.

A diff cut just before a final `\ No newline at end of file` line is accepted: no count reads that line, and the patch id cannot move.

A diff cut at a line boundary inside a `GIT binary patch` part is accepted: the patch id cannot move (it was the whole diff's in the 6 such cuts measured). The form is local git's and the fake's; GitHub's diff media type was not seen to serve it.

In a section with hunks an index line that holds no ids is accepted: the patch id cannot move.

The lines `dissimilarity index`, `copy from` and `copy to` are allowed from git's documentation and were seen in no measured diff; `old mode`, `new mode` and `delta N` were seen in local git's output alone, in the forms of the follow-up; the base85 data lines measured begin with `z` (15,024), `H` (2), `h` (1) and `I` (1) and come from two binary files.

Two renames whose `diff --git` lines are the same text, which a name holding ` b/` can make, are refused with the first text.

A mode change alone, which local git prints as `old mode` and `new mode` lines with no index line, is refused with the third text; GitHub's form for one is not measured.

A type change, a regular file that becomes a symlink, is refused with the first text: local git prints two sections with one `diff --git` line for the one list entry, and the second is a duplicate section. GitHub's form for one is not measured; the check fails closed.

In the section of a file with no changed line that holds two `index` lines, the later one decides.

Outside a hunk and outside a `GIT binary patch` part, a `Binary files … differ` line is taken wherever it stands in its section, before the index line too, and the bare line `Binary files differ` is taken for one; inside a `GIT binary patch` part that line is the sixth text. (Corrected by the lead on 2026-11-13: the sentence had said "wherever it stands in its section".)

A hunk header's fields may be parted by two spaces or by a tab.

A mode change alone, a name git quotes, a file of status `copied`, a symlink, and a submodule receive no special handling; each is refused or accepted as the general rule gives, GitHub's form is unmeasured, and no test claims otherwise.

The fake serves a binary file as `git diff --binary` prints it, while GitHub's `Binary files ... differ` form is covered directly by the table test from the saved measurement.

A diff whose final blank context line is lost anywhere between GitHub and the check is refused as a cut hunk, not tolerated.

A deleted empty file is accepted on its index line; GitHub's form for one is not measured.

The owner's refusal words do not identify which of the seven mismatches occurred; the proof error does. The JSON `patch` is no longer read, so a file omitted from both the diff and the list is not visible. A list of 300 files or more is refused before the diff is read, whatever the diff contains.

## The tests

`TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged` builds a behind bound PR whose legacy completeness answer has one missing patch and proves it updates and merges.

`TestBehindPRThatChangesALastLineWithNoNewlineIsUpdatedAndMerged` asserts git's two-file diff has three marker lines, including markers inside hunks, and proves the behind PR updates and merges.

`TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate` builds two files, removes one diff section, asserts that ground, and proves the first mismatch refuses before mutation.

`TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate` makes one file's list count too large and proves the per-file count mismatch refuses before mutation.

`TestBehindPRWithABinaryFileIsUpdatedAndMerged` builds binary and text files, asserts the binary's 0/0 list row, missing legacy patch, index, and no hunk, then proves update and merge.

`TestBehindPRWithAnEmptyFileAndAnUnchangedRenameIsUpdatedAndMerged` uses `git mv`, asserts an empty added file and unchanged rename as 0/0 rows, asserts a completeness answer with two missing patches, and proves update and merge.

`TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate` removes only the binary section's index and proves the third mismatch refuses before mutation.

`TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead` counts reads at 300 and 299 list entries, proving zero and one diff reads respectively.

`TestCompareDiffMismatchWords` holds the measured GitHub sections with their three pair ids, marks local-only and documented forms, fixes all seven mismatch texts, tests both cases that reach a third marker, and accepts two hunks with two markers each.

`TestTheCorpusOfRealSectionsGivesItsVerdicts` checks 26 cases by source: 6 GitHub's, 2 made from GitHub's sections, 9 the history's, and 9 local git's; 20 agree and 6 return their named text.

`TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck` reads the 2,680-byte saved compare and pins `d98a700eeca038b3cf05beae0453c0fc52246412`.

`TestASavedCompareCutInsideItsLastLineIsRefused` cuts the saved answer nine bytes before its end, proves the patch ids differ, and proves `PatchID` returns `ErrIncompleteDiff` with the fifth text. The saved answer remains compare `b46cb806c6b780fd1fab086b655f3812b86edef6...18dd49977d5d5a192dc104e8d777eaeea9555c50`, blob `fe0ff3c4d3fb09d2554a353e2976d42c5bd4caf3`.

`TestLeadRebindOverAFileWithNoPatchIsProvedAndBound` asserts missing legacy patches at both heads and proves `Rebind` binds the moved head.

`TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged` journals an update, moves the branch, asserts both grounds, and proves recovery merges and records the rebase.

`TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged` asserts the fake diff ends with the complete blank context line and proves update and merge.

`TestTheCheckOverTheHistoryRefusesNoWholeDiff` reads its list with `compareHistoryPairs` and runs `compareDiffMismatch` over each listed commit as `git diff -M` and `git diff -M --binary`; the default package run skips it, while the two explicit runs printed `commits 1930, refused 0`.

`TestTheHistoryListIsReadWholeOrItsErrorIsReturned` asserts two good pairs, a scanner-overflow list after one good pair, a malformed line, an empty list and an absent file, and proves that an incomplete read returns an error without partial pairs.

`assertCompareRefused` checks `PatchProof.Required`, and recovery requires the rebase entry's run to equal the result's run. The fake's completeness answer counts binary files, explicit omissions, and 0/0 empty or unchanged-rename files as missing patches. `ExtraAdditionFiles` acts on every file-list answer, while the proof-specific control behavior is selected only by the unique query.

## Round 2

The proof's file-list call follows this rule:

```text
the proof's file-list call is told from every other call of `gh.go` by its `--jq` text alone: the constant's text equals no other call's text, and the two existing tests count it as they counted `missing_patch`
```

The one-line constant is `internal/mergetrain/gh.go:201`; R18 restores #998's shared query text and shows both compatibility tests fail.

Rule (i) is: "a non-empty diff ends with a newline; otherwise it is cut short". Rule (ii) is: "outside a hunk a file's section holds only header lines (`diff --git`, the mode lines, `similarity`/`rename`/`copy`, `index`, `---`/`+++`, `Binary files … differ`, a `GIT binary patch` section's own lines, and the line `\ No newline at end of file` directly after a hunk); any other line is a mismatch". They add `the diff does not end with a newline` and `a file's section holds a line that is neither a header line nor in a hunk`. R16 removes the fifth result and R17 removes the sixth result. Rule (iii) keeps the carried first mismatch for text before the first section; R19 discards that prefix and shows the table case fail.

The fake counts both GitHub 0/0 no-patch forms. The new TH test proves an empty file and unchanged rename update and merge. The new TK test proves a saved compare cut inside its last content line changes its patch id and is refused. The carried marker test now puts the marker on its own line, the binary test scopes its index assertion, the refusal helper checks `Required`, the recovered rebase checks the result run, and the table distinguishes GitHub forms, local forms, and documented forms.

This PR's work was carried from #998's head by `git apply`; the seven files' blob hashes after the carry were:

```text
16686cc1c9e12cc74e94cb226777f2b1216d2447  docs/v3-followups.md
1e6d7248881e1df80b5e384d9b30fb37e14af27c  docs/v3-spike/330-train-compare-proof/README.md
63d4425ceb43f4c3fc288a42673bd2de7d7b1665  internal/mergetrain/compare_proof_test.go
af604e36ad83e06287eba4113a5c5edf617443e0  internal/mergetrain/escape_legacy_320_test.go
c487af13bc5376335d3d20c3b8cc935a926e991d  internal/mergetrain/fakegh_test.go
6a3e50ef19a8dee19f9fe89bfc5250cc7a015b03  internal/mergetrain/gh.go
fe0ff3c4d3fb09d2554a353e2976d42c5bd4caf3  internal/mergetrain/testdata/compare-b46cb806-18dd4997.json
```

## Round 3

Rule 1a is: "inside a hunk, a `\ No newline at end of file` line may stand directly after any line of the hunk (a `-`, `+` or context line) and counts on neither side; the hunk's lengths are read over the other lines". R20 restores #999's inside-marker refusal and shows the fixture, corpus, and table cases fail.

Rule 1b is: "(iv) a 0/0 file whose section holds an `index` line also holds a `Binary files … differ` line or a `GIT binary patch` part, unless the index line's ids are the zero id and the empty blob's (an added or deleted empty file)". It adds the seventh mismatch text, and R21 removes that answer and shows the corpus and table cases fail.

Rule 1c requires every hunk-header start and length to contain digits only. R22 normalizes the malformed headers before the check and shows the table's two malformed-header cases fail.

Rule 1d checks each binary data line's encoded byte count against its first letter. R23 bypasses that check and shows the table's `hello` case fail.

R24 turns a carriage return at the diff's end into a newline before the check and shows the table's carriage-return case fail.

`TestBehindPRThatChangesALastLineWithNoNewlineIsUpdatedAndMerged` uses git's own inside-marker form and proves update and merge. `TestTheCorpusOfRealSectionsGivesItsVerdicts` checks all 24 saved real-section cases and their 19/5 verdict split.

The corpus covers 6 GitHub cases, 2 cases made from GitHub sections, 9 history cases, and 7 local-git cases. The explicit history test ran the check across 1,930 commits once in plain form and once with binary patches, refusing none.

This PR's work was carried from #999's head by `git apply`; the seven files' blob hashes after the carry were:

```text
3a8a44549b93c7882adf0960cb90df4e8a416270  docs/v3-followups.md
97c61b57fd0efc2a1301477be08dfe438b12636f  docs/v3-spike/330-train-compare-proof/README.md
47494a7edd78848a9a9fb8c9753f88924427724f  internal/mergetrain/compare_proof_test.go
af604e36ad83e06287eba4113a5c5edf617443e0  internal/mergetrain/escape_legacy_320_test.go
2507c12fcf62c62f6aa3382eb0623457835071c6  internal/mergetrain/fakegh_test.go
5dd9e2aac4187ed303910b185b0f88f6ab666d5e  internal/mergetrain/gh.go
fe0ff3c4d3fb09d2554a353e2976d42c5bd4caf3  internal/mergetrain/testdata/compare-b46cb806-18dd4997.json
```

The corpus and the history test were copied from the round's inputs; their blob hashes were:

```text
8ea830d158aa93df2ddef33c9adabc87da18b476  internal/mergetrain/testdata/compare-corpus.json
1c40a9b29fb378b0d78901c298f9328ff3d5a182  internal/mergetrain/compare_history_test.go
```

## Follow-up

This follow-up's dispatch TIP is `af13b2e070a1ef93f20e5a3125d4f32298bb8e03`; the worktree's HEAD at the precheck is `af13b2e070a1ef93f20e5a3125d4f32298bb8e03`.

Item 1 adds three marker-before-section inputs and makes each return the first text without a panic. R25 removes the nil guard and shows the first table case panic.

Items 2 and 6 make both cap cases reach their third marker and add the two-hunk case. R26 shows both third-marker cases fail without the cap, and R27 shows the two-hunk case fail if the cap is counted over the section.

Item 3 adds `compareHistoryPairs` and `TestTheHistoryListIsReadWholeOrItsErrorIsReturned`. R28 shows that ignoring the scanner error makes the new test fail.

Item 4 names the worktree's HEAD at the precheck in the note's opening and in this section.

Item 5 records the type-change limit, the three laxities, and all 24 local-git forms measured at the gate.

Item 7 restores "both the diff and the list" in the JSON-patch known limit.

Item 8 splits the allowed marker-after-context and marker-after-added-line ground into two table cases.

The corpus was copied from the task's inputs; its blob hash was `854dda1b458eafea18eabab6ddc01f931180f821`.

## Rows

### R0

`bash ../../../../amq-squad-lead/T/trainfix/inputs-fu/rows-trainfix.sh a`

```text
=== row R0 ===
edit: none (your files)
$ go test ./internal/mergetrain/ -count=1 -timeout 240s -v -run '^(TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged|TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate|TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate|TestBehindPRWithABinaryFileIsUpdatedAndMerged|TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate|TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead|TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged|TestCompareDiffMismatchWords|TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck|TestLeadRebindOverAFileWithNoPatchIsProvedAndBound|TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged|TestIncompleteCompareRefusesWithoutRebind|TestMaxFileCompareRefusesWithoutRebind|TestHTTP422CompareRefusesWithoutRebind|TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead|TestStrictBehindUsesPinnedCompareAndRebases|TestBehindPRWithAnEmptyFileAndAnUnchangedRenameIsUpdatedAndMerged|TestASavedCompareCutInsideItsLastLineIsRefused|TestBehindPRThatChangesALastLineWithNoNewlineIsUpdatedAndMerged|TestTheCorpusOfRealSectionsGivesItsVerdicts|TestTheHistoryListIsReadWholeOrItsErrorIsReturned|TestFakeGHRefusesADiffWithEscapeBytesUnlessAllowed|TestBehindPRWithEscapeBytesInItsDiffIsRebasedAndMerged|TestAnOlderGHThatDoesNotKnowTheFlagIsReadOnceMoreWithoutIt|TestDriftSkipsALegacyGateRecordWithOneLine|TestDriftStillNamesAMalformedGateRecord|TestTrainNotesASkippedLegacyGateRecord|TestDriftClearsAStoreAlertOnlyForAPathThisRunRead|TestTrainRecordsAStoreAlertsClearingInItsOwnWords|TestChangedPatchAfterUpdateIsRefusedWithoutRebind|TestStrictOffDoesNotCallFailingCompare|TestLaterStrictCompareErrorSkipsOnlyThatPR|TestNamedDryRunBindsTheSameMovedHeadAsRealRun|TestStrictFreshnessIsRecheckedBeforeEveryLanding|TestStrictOffBehindGreenKeepsCurrentBehavior|TestStrictBehindConflictDryRunRefuses|TestStrictBehindMovedHeadDryRunRefuses|TestStrictUpToDateDoesNotRebase|TestProtectionParsesStrictPolicy)$'
exit: 0
--- PASS: TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate
--- PASS: TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead
--- PASS: TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate
--- PASS: TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate
--- PASS: TestASavedCompareCutInsideItsLastLineIsRefused
--- PASS: TestAnOlderGHThatDoesNotKnowTheFlagIsReadOnceMoreWithoutIt
--- PASS: TestBehindPRThatChangesALastLineWithNoNewlineIsUpdatedAndMerged
--- PASS: TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged
--- PASS: TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged
--- PASS: TestBehindPRWithABinaryFileIsUpdatedAndMerged
--- PASS: TestBehindPRWithAnEmptyFileAndAnUnchangedRenameIsUpdatedAndMerged
--- PASS: TestBehindPRWithEscapeBytesInItsDiffIsRebasedAndMerged
--- PASS: TestChangedPatchAfterUpdateIsRefusedWithoutRebind
--- PASS: TestCompareDiffMismatchWords
--- PASS: TestDriftClearsAStoreAlertOnlyForAPathThisRunRead
--- PASS: TestDriftSkipsALegacyGateRecordWithOneLine
--- PASS: TestDriftStillNamesAMalformedGateRecord
--- PASS: TestFakeGHRefusesADiffWithEscapeBytesUnlessAllowed
--- PASS: TestHTTP422CompareRefusesWithoutRebind
--- PASS: TestIncompleteCompareRefusesWithoutRebind
--- PASS: TestLaterStrictCompareErrorSkipsOnlyThatPR
--- PASS: TestLeadRebindOverAFileWithNoPatchIsProvedAndBound
--- PASS: TestMaxFileCompareRefusesWithoutRebind
--- PASS: TestNamedDryRunBindsTheSameMovedHeadAsRealRun
--- PASS: TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead
--- PASS: TestProtectionParsesStrictPolicy
--- PASS: TestStrictBehindConflictDryRunRefuses
--- PASS: TestStrictBehindMovedHeadDryRunRefuses
--- PASS: TestStrictBehindUsesPinnedCompareAndRebases
--- PASS: TestStrictFreshnessIsRecheckedBeforeEveryLanding
--- PASS: TestStrictOffBehindGreenKeepsCurrentBehavior
--- PASS: TestStrictOffDoesNotCallFailingCompare
--- PASS: TestStrictUpToDateDoesNotRebase
--- PASS: TestTheCorpusOfRealSectionsGivesItsVerdicts
--- PASS: TestTheHistoryListIsReadWholeOrItsErrorIsReturned
--- PASS: TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck
--- PASS: TestTrainNotesASkippedLegacyGateRecord
--- PASS: TestTrainRecordsAStoreAlertsClearingInItsOwnWords
--- PASS: TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged
pass 39 fail 0 skip 0
panic: no
failed subtests:
messages (the first 12 lines, each cut at 240 characters; 40-digit commit ids printed as <sha>, 12-digit ones as <id>; white space at a line's end removed; a panic's stack left out; the texts are your tests'):
restore: gh.go cmp exit 0
restore: compare_history_test.go cmp exit 0
```

### R1

`bash ../../../../amq-squad-lead/T/trainfix/inputs-fu/rows-trainfix.sh a`

```text
=== row R1 ===
edit: internal/mergetrain/gh.go:227-275 (your PatchID, 49 lines) is the TIP's PatchID (49 lines), whole
$ go test ./internal/mergetrain/ -count=1 -timeout 240s -v -run '^(TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged|TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate|TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate|TestBehindPRWithABinaryFileIsUpdatedAndMerged|TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate|TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead|TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged|TestCompareDiffMismatchWords|TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck|TestLeadRebindOverAFileWithNoPatchIsProvedAndBound|TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged|TestIncompleteCompareRefusesWithoutRebind|TestMaxFileCompareRefusesWithoutRebind|TestHTTP422CompareRefusesWithoutRebind|TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead|TestStrictBehindUsesPinnedCompareAndRebases|TestBehindPRWithAnEmptyFileAndAnUnchangedRenameIsUpdatedAndMerged|TestASavedCompareCutInsideItsLastLineIsRefused|TestBehindPRThatChangesALastLineWithNoNewlineIsUpdatedAndMerged|TestTheCorpusOfRealSectionsGivesItsVerdicts|TestTheHistoryListIsReadWholeOrItsErrorIsReturned|TestFakeGHRefusesADiffWithEscapeBytesUnlessAllowed|TestBehindPRWithEscapeBytesInItsDiffIsRebasedAndMerged|TestAnOlderGHThatDoesNotKnowTheFlagIsReadOnceMoreWithoutIt|TestDriftSkipsALegacyGateRecordWithOneLine|TestDriftStillNamesAMalformedGateRecord|TestTrainNotesASkippedLegacyGateRecord|TestDriftClearsAStoreAlertOnlyForAPathThisRunRead|TestTrainRecordsAStoreAlertsClearingInItsOwnWords|TestChangedPatchAfterUpdateIsRefusedWithoutRebind|TestStrictOffDoesNotCallFailingCompare|TestLaterStrictCompareErrorSkipsOnlyThatPR|TestNamedDryRunBindsTheSameMovedHeadAsRealRun|TestStrictFreshnessIsRecheckedBeforeEveryLanding|TestStrictOffBehindGreenKeepsCurrentBehavior|TestStrictBehindConflictDryRunRefuses|TestStrictBehindMovedHeadDryRunRefuses|TestStrictUpToDateDoesNotRebase|TestProtectionParsesStrictPolicy)$'
exit: 1
--- FAIL: TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate
--- FAIL: TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead
--- FAIL: TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate
--- FAIL: TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate
--- FAIL: TestASavedCompareCutInsideItsLastLineIsRefused
--- FAIL: TestAnOlderGHThatDoesNotKnowTheFlagIsReadOnceMoreWithoutIt
--- FAIL: TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged
--- FAIL: TestBehindPRWithABinaryFileIsUpdatedAndMerged
--- FAIL: TestBehindPRWithAnEmptyFileAndAnUnchangedRenameIsUpdatedAndMerged
--- FAIL: TestBehindPRWithEscapeBytesInItsDiffIsRebasedAndMerged
--- FAIL: TestIncompleteCompareRefusesWithoutRebind
--- FAIL: TestLeadRebindOverAFileWithNoPatchIsProvedAndBound
--- FAIL: TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged
--- PASS: TestBehindPRThatChangesALastLineWithNoNewlineIsUpdatedAndMerged
--- PASS: TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged
--- PASS: TestChangedPatchAfterUpdateIsRefusedWithoutRebind
--- PASS: TestCompareDiffMismatchWords
--- PASS: TestDriftClearsAStoreAlertOnlyForAPathThisRunRead
--- PASS: TestDriftSkipsALegacyGateRecordWithOneLine
--- PASS: TestDriftStillNamesAMalformedGateRecord
--- PASS: TestFakeGHRefusesADiffWithEscapeBytesUnlessAllowed
--- PASS: TestHTTP422CompareRefusesWithoutRebind
--- PASS: TestLaterStrictCompareErrorSkipsOnlyThatPR
--- PASS: TestMaxFileCompareRefusesWithoutRebind
--- PASS: TestNamedDryRunBindsTheSameMovedHeadAsRealRun
--- PASS: TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead
--- PASS: TestProtectionParsesStrictPolicy
--- PASS: TestStrictBehindConflictDryRunRefuses
--- PASS: TestStrictBehindMovedHeadDryRunRefuses
--- PASS: TestStrictBehindUsesPinnedCompareAndRebases
--- PASS: TestStrictFreshnessIsRecheckedBeforeEveryLanding
--- PASS: TestStrictOffBehindGreenKeepsCurrentBehavior
--- PASS: TestStrictOffDoesNotCallFailingCompare
--- PASS: TestStrictUpToDateDoesNotRebase
--- PASS: TestTheCorpusOfRealSectionsGivesItsVerdicts
--- PASS: TestTheHistoryListIsReadWholeOrItsErrorIsReturned
--- PASS: TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck
--- PASS: TestTrainNotesASkippedLegacyGateRecord
--- PASS: TestTrainRecordsAStoreAlertsClearingInItsOwnWords
pass 26 fail 13 skip 0
panic: no
failed subtests:
    --- FAIL: TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead/at_the_limit
    --- FAIL: TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead/one_under
messages (the first 12 lines, each cut at 240 characters; 40-digit commit ids printed as <sha>, 12-digit ones as <id>; white space at a line's end removed; a panic's stack left out; the texts are your tests'):
    compare_proof_test.go:89: #6 was not rebased and merged: status refused; force pushes recorded 0; head unchanged true; task bound to its old head true; audit: refusal: the update could not be proved: GitHub returned an incomplete diff
    compare_proof_test.go:130: not refused in the fixed words: {Number:6 Task:t1 Head:<sha> Draft:true Changed:2 Files:[one.txt two.txt] Refusals:[] GateID: GateError: Lifted:false After:[] RebaseForCI:false RebaseRule: RebaseReason: PatchP
    compare_proof_test.go:144: not refused in the fixed words: {Number:6 Task:t1 Head:<sha> Draft:true Changed:3 Files:[one.txt two.txt] Refusals:[] GateID: GateError: Lifted:false After:[] RebaseForCI:false RebaseRule: RebaseReason: PatchP
    compare_proof_test.go:164: #6 was not rebased and merged: status refused; force pushes recorded 0; head unchanged true; task bound to its old head true; audit: refusal: the update could not be proved: GitHub returned an incomplete diff
    compare_proof_test.go:191: #6 was not rebased and merged: status refused; force pushes recorded 0; head unchanged true; task bound to its old head true; audit: refusal: the update could not be proved: GitHub returned an incomplete diff
    compare_proof_test.go:205: proof error GitHub returned an incomplete diff, want a file with no changed line holds a hunk, or neither an index line nor a 100% rename's three lines
    compare_proof_test.go:234: limit: err=gh api repos/owner/fixture/compare/base...head --jq: unexpected call: unexpected reads=0 want 0
    compare_proof_test.go:234: under: err=gh api repos/owner/fixture/compare/base...head --jq: unexpected call: unexpected reads=0 want 1
    compare_proof_test.go:533: cut proof: id="" err=gh api repos/omriariav/amq-squad/compare/<sha>...<sha> --jq: unexpected call: unexpected
    compare_proof_test.go:552: rebind: rebind refused (rebind_unproved): the update could not be proved: GitHub returned an incomplete diff {Run: Task: Repository: Base: PR:0 From: To: Parent: ParentIs: PatchID: Push:} bound=<sha>
    compare_proof_test.go:573: recovery: <nil> {Number:1 Task:t1 Head:<sha> Draft:true Changed:2 Files:[pkg/a.go] Refusals:[{Rule:force_push Detail:head was force-pushed <id> -> <id> by owner} {Rule:pr_head_moved Detail:PR #1 head <id> is n
    escape_legacy_320_test.go:146: one diff read per proof: 0 proofs, reads ["api repos/owner/fixture/compare/<sha>...<sha> -H Accept: application/vnd.github.diff --allow-escape-sequences" "api repos/owner/fixture/compare/<sha>...<sha> -H A
restore: gh.go cmp exit 0
restore: compare_history_test.go cmp exit 0
```

### R2

`bash ../../../../amq-squad-lead/T/trainfix/inputs-fu/rows-trainfix.sh a`

```text
=== row R2 ===
edit: internal/mergetrain/gh.go:279
  before: func compareDiffMismatch(files []FileChange, diff []byte) string {
  after:  func compareDiffMismatchYours(files []FileChange, diff []byte) string {
  appended: a compareDiffMismatch that calls yours and answers "" when its answer starts with:
    "the diff's files are not the compare's files: "
$ go test ./internal/mergetrain/ -count=1 -timeout 240s -v -run '^(TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged|TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate|TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate|TestBehindPRWithABinaryFileIsUpdatedAndMerged|TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate|TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead|TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged|TestCompareDiffMismatchWords|TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck|TestLeadRebindOverAFileWithNoPatchIsProvedAndBound|TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged|TestIncompleteCompareRefusesWithoutRebind|TestMaxFileCompareRefusesWithoutRebind|TestHTTP422CompareRefusesWithoutRebind|TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead|TestStrictBehindUsesPinnedCompareAndRebases|TestBehindPRWithAnEmptyFileAndAnUnchangedRenameIsUpdatedAndMerged|TestASavedCompareCutInsideItsLastLineIsRefused|TestBehindPRThatChangesALastLineWithNoNewlineIsUpdatedAndMerged|TestTheCorpusOfRealSectionsGivesItsVerdicts|TestTheHistoryListIsReadWholeOrItsErrorIsReturned|TestFakeGHRefusesADiffWithEscapeBytesUnlessAllowed|TestBehindPRWithEscapeBytesInItsDiffIsRebasedAndMerged|TestAnOlderGHThatDoesNotKnowTheFlagIsReadOnceMoreWithoutIt|TestDriftSkipsALegacyGateRecordWithOneLine|TestDriftStillNamesAMalformedGateRecord|TestTrainNotesASkippedLegacyGateRecord|TestDriftClearsAStoreAlertOnlyForAPathThisRunRead|TestTrainRecordsAStoreAlertsClearingInItsOwnWords|TestChangedPatchAfterUpdateIsRefusedWithoutRebind|TestStrictOffDoesNotCallFailingCompare|TestLaterStrictCompareErrorSkipsOnlyThatPR|TestNamedDryRunBindsTheSameMovedHeadAsRealRun|TestStrictFreshnessIsRecheckedBeforeEveryLanding|TestStrictOffBehindGreenKeepsCurrentBehavior|TestStrictBehindConflictDryRunRefuses|TestStrictBehindMovedHeadDryRunRefuses|TestStrictUpToDateDoesNotRebase|TestProtectionParsesStrictPolicy)$'
exit: 1
--- FAIL: TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead
--- FAIL: TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate
--- FAIL: TestCompareDiffMismatchWords
--- FAIL: TestTheCorpusOfRealSectionsGivesItsVerdicts
--- PASS: TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate
--- PASS: TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate
--- PASS: TestASavedCompareCutInsideItsLastLineIsRefused
--- PASS: TestAnOlderGHThatDoesNotKnowTheFlagIsReadOnceMoreWithoutIt
--- PASS: TestBehindPRThatChangesALastLineWithNoNewlineIsUpdatedAndMerged
--- PASS: TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged
--- PASS: TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged
--- PASS: TestBehindPRWithABinaryFileIsUpdatedAndMerged
--- PASS: TestBehindPRWithAnEmptyFileAndAnUnchangedRenameIsUpdatedAndMerged
--- PASS: TestBehindPRWithEscapeBytesInItsDiffIsRebasedAndMerged
--- PASS: TestChangedPatchAfterUpdateIsRefusedWithoutRebind
--- PASS: TestDriftClearsAStoreAlertOnlyForAPathThisRunRead
--- PASS: TestDriftSkipsALegacyGateRecordWithOneLine
--- PASS: TestDriftStillNamesAMalformedGateRecord
--- PASS: TestFakeGHRefusesADiffWithEscapeBytesUnlessAllowed
--- PASS: TestHTTP422CompareRefusesWithoutRebind
--- PASS: TestIncompleteCompareRefusesWithoutRebind
--- PASS: TestLaterStrictCompareErrorSkipsOnlyThatPR
--- PASS: TestLeadRebindOverAFileWithNoPatchIsProvedAndBound
--- PASS: TestMaxFileCompareRefusesWithoutRebind
--- PASS: TestNamedDryRunBindsTheSameMovedHeadAsRealRun
--- PASS: TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead
--- PASS: TestProtectionParsesStrictPolicy
--- PASS: TestStrictBehindConflictDryRunRefuses
--- PASS: TestStrictBehindMovedHeadDryRunRefuses
--- PASS: TestStrictBehindUsesPinnedCompareAndRebases
--- PASS: TestStrictFreshnessIsRecheckedBeforeEveryLanding
--- PASS: TestStrictOffBehindGreenKeepsCurrentBehavior
--- PASS: TestStrictOffDoesNotCallFailingCompare
--- PASS: TestStrictUpToDateDoesNotRebase
--- PASS: TestTheHistoryListIsReadWholeOrItsErrorIsReturned
--- PASS: TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck
--- PASS: TestTrainNotesASkippedLegacyGateRecord
--- PASS: TestTrainRecordsAStoreAlertsClearingInItsOwnWords
--- PASS: TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged
pass 35 fail 4 skip 0
panic: no
failed subtests:
    --- FAIL: TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead/one_under
    --- FAIL: TestCompareDiffMismatchWords/marker_before_any_section
    --- FAIL: TestCompareDiffMismatchWords/marker_before_one_section
    --- FAIL: TestCompareDiffMismatchWords/marker_after_empty_line_before_one_section
    --- FAIL: TestCompareDiffMismatchWords/file_empty_diff
    --- FAIL: TestCompareDiffMismatchWords/text_before_first_file
    --- FAIL: TestCompareDiffMismatchWords/missing_listed_file
    --- FAIL: TestCompareDiffMismatchWords/unexpected_file
    --- FAIL: TestCompareDiffMismatchWords/duplicate_section
    --- FAIL: TestCompareDiffMismatchWords/rename_without_previous
    --- FAIL: TestCompareDiffMismatchWords/quoted_name_local_form
messages (the first 12 lines, each cut at 240 characters; 40-digit commit ids printed as <sha>, 12-digit ones as <id>; white space at a line's end removed; a panic's stack left out; the texts are your tests'):
    compare_proof_test.go:130: not refused in the fixed words: {Number:6 Task:t1 Head:<sha> Draft:true Changed:2 Files:[one.txt two.txt] Refusals:[] GateID: GateError: Lifted:false After:[] RebaseForCI:false RebaseRule: RebaseReason: PatchP
    compare_proof_test.go:234: under: err=git patch-id --stable returned no patch id reads=1 want 1
    compare_proof_test.go:376: got "", want prefix "the diff's files are not the compare's files: "
    compare_proof_test.go:376: got "", want prefix "the diff's files are not the compare's files: "
    compare_proof_test.go:376: got "", want prefix "the diff's files are not the compare's files: "
    compare_proof_test.go:376: got "", want prefix "the diff's files are not the compare's files: "
    compare_proof_test.go:376: got "", want prefix "the diff's files are not the compare's files: "
    compare_proof_test.go:376: got "", want prefix "the diff's files are not the compare's files: "
    compare_proof_test.go:376: got "", want prefix "the diff's files are not the compare's files: "
    compare_proof_test.go:376: got "", want prefix "the diff's files are not the compare's files: "
    compare_proof_test.go:376: got "", want prefix "the diff's files are not the compare's files: "
    compare_proof_test.go:376: got "", want prefix "the diff's files are not the compare's files: "
restore: gh.go cmp exit 0
restore: compare_history_test.go cmp exit 0
```

### R3

`bash ../../../../amq-squad-lead/T/trainfix/inputs-fu/rows-trainfix.sh a`

```text
=== row R3 ===
edit: internal/mergetrain/gh.go:279
  before: func compareDiffMismatch(files []FileChange, diff []byte) string {
  after:  func compareDiffMismatchYours(files []FileChange, diff []byte) string {
  appended: a compareDiffMismatch that calls yours and answers "" when its answer starts with:
    "the diff's added and removed lines are not the compare's counts: "
$ go test ./internal/mergetrain/ -count=1 -timeout 240s -v -run '^(TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged|TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate|TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate|TestBehindPRWithABinaryFileIsUpdatedAndMerged|TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate|TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead|TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged|TestCompareDiffMismatchWords|TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck|TestLeadRebindOverAFileWithNoPatchIsProvedAndBound|TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged|TestIncompleteCompareRefusesWithoutRebind|TestMaxFileCompareRefusesWithoutRebind|TestHTTP422CompareRefusesWithoutRebind|TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead|TestStrictBehindUsesPinnedCompareAndRebases|TestBehindPRWithAnEmptyFileAndAnUnchangedRenameIsUpdatedAndMerged|TestASavedCompareCutInsideItsLastLineIsRefused|TestBehindPRThatChangesALastLineWithNoNewlineIsUpdatedAndMerged|TestTheCorpusOfRealSectionsGivesItsVerdicts|TestTheHistoryListIsReadWholeOrItsErrorIsReturned|TestFakeGHRefusesADiffWithEscapeBytesUnlessAllowed|TestBehindPRWithEscapeBytesInItsDiffIsRebasedAndMerged|TestAnOlderGHThatDoesNotKnowTheFlagIsReadOnceMoreWithoutIt|TestDriftSkipsALegacyGateRecordWithOneLine|TestDriftStillNamesAMalformedGateRecord|TestTrainNotesASkippedLegacyGateRecord|TestDriftClearsAStoreAlertOnlyForAPathThisRunRead|TestTrainRecordsAStoreAlertsClearingInItsOwnWords|TestChangedPatchAfterUpdateIsRefusedWithoutRebind|TestStrictOffDoesNotCallFailingCompare|TestLaterStrictCompareErrorSkipsOnlyThatPR|TestNamedDryRunBindsTheSameMovedHeadAsRealRun|TestStrictFreshnessIsRecheckedBeforeEveryLanding|TestStrictOffBehindGreenKeepsCurrentBehavior|TestStrictBehindConflictDryRunRefuses|TestStrictBehindMovedHeadDryRunRefuses|TestStrictUpToDateDoesNotRebase|TestProtectionParsesStrictPolicy)$'
exit: 1
--- FAIL: TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate
--- FAIL: TestCompareDiffMismatchWords
--- PASS: TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate
--- PASS: TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead
--- PASS: TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate
--- PASS: TestASavedCompareCutInsideItsLastLineIsRefused
--- PASS: TestAnOlderGHThatDoesNotKnowTheFlagIsReadOnceMoreWithoutIt
--- PASS: TestBehindPRThatChangesALastLineWithNoNewlineIsUpdatedAndMerged
--- PASS: TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged
--- PASS: TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged
--- PASS: TestBehindPRWithABinaryFileIsUpdatedAndMerged
--- PASS: TestBehindPRWithAnEmptyFileAndAnUnchangedRenameIsUpdatedAndMerged
--- PASS: TestBehindPRWithEscapeBytesInItsDiffIsRebasedAndMerged
--- PASS: TestChangedPatchAfterUpdateIsRefusedWithoutRebind
--- PASS: TestDriftClearsAStoreAlertOnlyForAPathThisRunRead
--- PASS: TestDriftSkipsALegacyGateRecordWithOneLine
--- PASS: TestDriftStillNamesAMalformedGateRecord
--- PASS: TestFakeGHRefusesADiffWithEscapeBytesUnlessAllowed
--- PASS: TestHTTP422CompareRefusesWithoutRebind
--- PASS: TestIncompleteCompareRefusesWithoutRebind
--- PASS: TestLaterStrictCompareErrorSkipsOnlyThatPR
--- PASS: TestLeadRebindOverAFileWithNoPatchIsProvedAndBound
--- PASS: TestMaxFileCompareRefusesWithoutRebind
--- PASS: TestNamedDryRunBindsTheSameMovedHeadAsRealRun
--- PASS: TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead
--- PASS: TestProtectionParsesStrictPolicy
--- PASS: TestStrictBehindConflictDryRunRefuses
--- PASS: TestStrictBehindMovedHeadDryRunRefuses
--- PASS: TestStrictBehindUsesPinnedCompareAndRebases
--- PASS: TestStrictFreshnessIsRecheckedBeforeEveryLanding
--- PASS: TestStrictOffBehindGreenKeepsCurrentBehavior
--- PASS: TestStrictOffDoesNotCallFailingCompare
--- PASS: TestStrictUpToDateDoesNotRebase
--- PASS: TestTheCorpusOfRealSectionsGivesItsVerdicts
--- PASS: TestTheHistoryListIsReadWholeOrItsErrorIsReturned
--- PASS: TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck
--- PASS: TestTrainNotesASkippedLegacyGateRecord
--- PASS: TestTrainRecordsAStoreAlertsClearingInItsOwnWords
--- PASS: TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged
pass 37 fail 2 skip 0
panic: no
failed subtests:
    --- FAIL: TestCompareDiffMismatchWords/one_addition_missing
    --- FAIL: TestCompareDiffMismatchWords/removed_line_missing
    --- FAIL: TestCompareDiffMismatchWords/swapped_per-file_counts
messages (the first 12 lines, each cut at 240 characters; 40-digit commit ids printed as <sha>, 12-digit ones as <id>; white space at a line's end removed; a panic's stack left out; the texts are your tests'):
    compare_proof_test.go:144: not refused in the fixed words: {Number:6 Task:t1 Head:<sha> Draft:true Changed:3 Files:[one.txt two.txt] Refusals:[] GateID: GateError: Lifted:false After:[] RebaseForCI:false RebaseRule: RebaseReason: PatchP
    compare_proof_test.go:376: got "", want prefix "the diff's added and removed lines are not the compare's counts: "
    compare_proof_test.go:376: got "", want prefix "the diff's added and removed lines are not the compare's counts: "
    compare_proof_test.go:376: got "", want prefix "the diff's added and removed lines are not the compare's counts: "
restore: gh.go cmp exit 0
restore: compare_history_test.go cmp exit 0
```

### R4

`bash ../../../../amq-squad-lead/T/trainfix/inputs-fu/rows-trainfix.sh a`

```text
=== row R4 ===
edit: internal/mergetrain/gh.go:279
  before: func compareDiffMismatch(files []FileChange, diff []byte) string {
  after:  func compareDiffMismatchYours(files []FileChange, diff []byte) string {
  appended: a compareDiffMismatch that calls yours and answers "" when its answer starts with:
    "a file with no changed line holds a hunk, or neither an index line nor a 100% rename's three lines: "
$ go test ./internal/mergetrain/ -count=1 -timeout 240s -v -run '^(TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged|TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate|TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate|TestBehindPRWithABinaryFileIsUpdatedAndMerged|TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate|TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead|TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged|TestCompareDiffMismatchWords|TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck|TestLeadRebindOverAFileWithNoPatchIsProvedAndBound|TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged|TestIncompleteCompareRefusesWithoutRebind|TestMaxFileCompareRefusesWithoutRebind|TestHTTP422CompareRefusesWithoutRebind|TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead|TestStrictBehindUsesPinnedCompareAndRebases|TestBehindPRWithAnEmptyFileAndAnUnchangedRenameIsUpdatedAndMerged|TestASavedCompareCutInsideItsLastLineIsRefused|TestBehindPRThatChangesALastLineWithNoNewlineIsUpdatedAndMerged|TestTheCorpusOfRealSectionsGivesItsVerdicts|TestTheHistoryListIsReadWholeOrItsErrorIsReturned|TestFakeGHRefusesADiffWithEscapeBytesUnlessAllowed|TestBehindPRWithEscapeBytesInItsDiffIsRebasedAndMerged|TestAnOlderGHThatDoesNotKnowTheFlagIsReadOnceMoreWithoutIt|TestDriftSkipsALegacyGateRecordWithOneLine|TestDriftStillNamesAMalformedGateRecord|TestTrainNotesASkippedLegacyGateRecord|TestDriftClearsAStoreAlertOnlyForAPathThisRunRead|TestTrainRecordsAStoreAlertsClearingInItsOwnWords|TestChangedPatchAfterUpdateIsRefusedWithoutRebind|TestStrictOffDoesNotCallFailingCompare|TestLaterStrictCompareErrorSkipsOnlyThatPR|TestNamedDryRunBindsTheSameMovedHeadAsRealRun|TestStrictFreshnessIsRecheckedBeforeEveryLanding|TestStrictOffBehindGreenKeepsCurrentBehavior|TestStrictBehindConflictDryRunRefuses|TestStrictBehindMovedHeadDryRunRefuses|TestStrictUpToDateDoesNotRebase|TestProtectionParsesStrictPolicy)$'
exit: 1
--- FAIL: TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate
--- FAIL: TestCompareDiffMismatchWords
--- PASS: TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead
--- PASS: TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate
--- PASS: TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate
--- PASS: TestASavedCompareCutInsideItsLastLineIsRefused
--- PASS: TestAnOlderGHThatDoesNotKnowTheFlagIsReadOnceMoreWithoutIt
--- PASS: TestBehindPRThatChangesALastLineWithNoNewlineIsUpdatedAndMerged
--- PASS: TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged
--- PASS: TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged
--- PASS: TestBehindPRWithABinaryFileIsUpdatedAndMerged
--- PASS: TestBehindPRWithAnEmptyFileAndAnUnchangedRenameIsUpdatedAndMerged
--- PASS: TestBehindPRWithEscapeBytesInItsDiffIsRebasedAndMerged
--- PASS: TestChangedPatchAfterUpdateIsRefusedWithoutRebind
--- PASS: TestDriftClearsAStoreAlertOnlyForAPathThisRunRead
--- PASS: TestDriftSkipsALegacyGateRecordWithOneLine
--- PASS: TestDriftStillNamesAMalformedGateRecord
--- PASS: TestFakeGHRefusesADiffWithEscapeBytesUnlessAllowed
--- PASS: TestHTTP422CompareRefusesWithoutRebind
--- PASS: TestIncompleteCompareRefusesWithoutRebind
--- PASS: TestLaterStrictCompareErrorSkipsOnlyThatPR
--- PASS: TestLeadRebindOverAFileWithNoPatchIsProvedAndBound
--- PASS: TestMaxFileCompareRefusesWithoutRebind
--- PASS: TestNamedDryRunBindsTheSameMovedHeadAsRealRun
--- PASS: TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead
--- PASS: TestProtectionParsesStrictPolicy
--- PASS: TestStrictBehindConflictDryRunRefuses
--- PASS: TestStrictBehindMovedHeadDryRunRefuses
--- PASS: TestStrictBehindUsesPinnedCompareAndRebases
--- PASS: TestStrictFreshnessIsRecheckedBeforeEveryLanding
--- PASS: TestStrictOffBehindGreenKeepsCurrentBehavior
--- PASS: TestStrictOffDoesNotCallFailingCompare
--- PASS: TestStrictUpToDateDoesNotRebase
--- PASS: TestTheCorpusOfRealSectionsGivesItsVerdicts
--- PASS: TestTheHistoryListIsReadWholeOrItsErrorIsReturned
--- PASS: TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck
--- PASS: TestTrainNotesASkippedLegacyGateRecord
--- PASS: TestTrainRecordsAStoreAlertsClearingInItsOwnWords
--- PASS: TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged
pass 37 fail 2 skip 0
panic: no
failed subtests:
    --- FAIL: TestCompareDiffMismatchWords/zero_with_hunk
    --- FAIL: TestCompareDiffMismatchWords/binary_no_index
    --- FAIL: TestCompareDiffMismatchWords/zero_only_header
    --- FAIL: TestCompareDiffMismatchWords/rename_lacks_to
    --- FAIL: TestCompareDiffMismatchWords/rename_wrong_from
    --- FAIL: TestCompareDiffMismatchWords/rename_99_percent
    --- FAIL: TestCompareDiffMismatchWords/mode_only_local_form
messages (the first 12 lines, each cut at 240 characters; 40-digit commit ids printed as <sha>, 12-digit ones as <id>; white space at a line's end removed; a panic's stack left out; the texts are your tests'):
    compare_proof_test.go:205: not refused in the fixed words: {Number:6 Task:t1 Head:<sha> Draft:true Changed:1 Files:[asset.bin note.txt] Refusals:[] GateID: GateError: Lifted:false After:[] RebaseForCI:false RebaseRule: RebaseReason: Pat
    compare_proof_test.go:376: got "", want prefix "a file with no changed line holds a hunk, or neither an index line nor a 100% rename's three lines: "
    compare_proof_test.go:376: got "", want prefix "a file with no changed line holds a hunk, or neither an index line nor a 100% rename's three lines: "
    compare_proof_test.go:376: got "", want prefix "a file with no changed line holds a hunk, or neither an index line nor a 100% rename's three lines: "
    compare_proof_test.go:376: got "", want prefix "a file with no changed line holds a hunk, or neither an index line nor a 100% rename's three lines: "
    compare_proof_test.go:376: got "", want prefix "a file with no changed line holds a hunk, or neither an index line nor a 100% rename's three lines: "
    compare_proof_test.go:376: got "", want prefix "a file with no changed line holds a hunk, or neither an index line nor a 100% rename's three lines: "
    compare_proof_test.go:376: got "", want prefix "a file with no changed line holds a hunk, or neither an index line nor a 100% rename's three lines: "
restore: gh.go cmp exit 0
restore: compare_history_test.go cmp exit 0
keep directory removed: yes
```

### R5

`bash ../../../../amq-squad-lead/T/trainfix/inputs-fu/rows-trainfix.sh b`

```text
=== row R5 ===
edit: internal/mergetrain/gh.go:279
  before: func compareDiffMismatch(files []FileChange, diff []byte) string {
  after:  func compareDiffMismatchYours(files []FileChange, diff []byte) string {
  appended: a compareDiffMismatch that refuses every file with 0 additions and 0 deletions, then calls yours
$ go test ./internal/mergetrain/ -count=1 -timeout 240s -v -run '^(TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged|TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate|TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate|TestBehindPRWithABinaryFileIsUpdatedAndMerged|TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate|TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead|TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged|TestCompareDiffMismatchWords|TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck|TestLeadRebindOverAFileWithNoPatchIsProvedAndBound|TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged|TestIncompleteCompareRefusesWithoutRebind|TestMaxFileCompareRefusesWithoutRebind|TestHTTP422CompareRefusesWithoutRebind|TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead|TestStrictBehindUsesPinnedCompareAndRebases|TestBehindPRWithAnEmptyFileAndAnUnchangedRenameIsUpdatedAndMerged|TestASavedCompareCutInsideItsLastLineIsRefused|TestBehindPRThatChangesALastLineWithNoNewlineIsUpdatedAndMerged|TestTheCorpusOfRealSectionsGivesItsVerdicts|TestTheHistoryListIsReadWholeOrItsErrorIsReturned|TestFakeGHRefusesADiffWithEscapeBytesUnlessAllowed|TestBehindPRWithEscapeBytesInItsDiffIsRebasedAndMerged|TestAnOlderGHThatDoesNotKnowTheFlagIsReadOnceMoreWithoutIt|TestDriftSkipsALegacyGateRecordWithOneLine|TestDriftStillNamesAMalformedGateRecord|TestTrainNotesASkippedLegacyGateRecord|TestDriftClearsAStoreAlertOnlyForAPathThisRunRead|TestTrainRecordsAStoreAlertsClearingInItsOwnWords|TestChangedPatchAfterUpdateIsRefusedWithoutRebind|TestStrictOffDoesNotCallFailingCompare|TestLaterStrictCompareErrorSkipsOnlyThatPR|TestNamedDryRunBindsTheSameMovedHeadAsRealRun|TestStrictFreshnessIsRecheckedBeforeEveryLanding|TestStrictOffBehindGreenKeepsCurrentBehavior|TestStrictBehindConflictDryRunRefuses|TestStrictBehindMovedHeadDryRunRefuses|TestStrictUpToDateDoesNotRebase|TestProtectionParsesStrictPolicy)$'
exit: 1
--- FAIL: TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate
--- FAIL: TestBehindPRWithABinaryFileIsUpdatedAndMerged
--- FAIL: TestBehindPRWithAnEmptyFileAndAnUnchangedRenameIsUpdatedAndMerged
--- FAIL: TestCompareDiffMismatchWords
--- FAIL: TestTheCorpusOfRealSectionsGivesItsVerdicts
--- PASS: TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead
--- PASS: TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate
--- PASS: TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate
--- PASS: TestASavedCompareCutInsideItsLastLineIsRefused
--- PASS: TestAnOlderGHThatDoesNotKnowTheFlagIsReadOnceMoreWithoutIt
--- PASS: TestBehindPRThatChangesALastLineWithNoNewlineIsUpdatedAndMerged
--- PASS: TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged
--- PASS: TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged
--- PASS: TestBehindPRWithEscapeBytesInItsDiffIsRebasedAndMerged
--- PASS: TestChangedPatchAfterUpdateIsRefusedWithoutRebind
--- PASS: TestDriftClearsAStoreAlertOnlyForAPathThisRunRead
--- PASS: TestDriftSkipsALegacyGateRecordWithOneLine
--- PASS: TestDriftStillNamesAMalformedGateRecord
--- PASS: TestFakeGHRefusesADiffWithEscapeBytesUnlessAllowed
--- PASS: TestHTTP422CompareRefusesWithoutRebind
--- PASS: TestIncompleteCompareRefusesWithoutRebind
--- PASS: TestLaterStrictCompareErrorSkipsOnlyThatPR
--- PASS: TestLeadRebindOverAFileWithNoPatchIsProvedAndBound
--- PASS: TestMaxFileCompareRefusesWithoutRebind
--- PASS: TestNamedDryRunBindsTheSameMovedHeadAsRealRun
--- PASS: TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead
--- PASS: TestProtectionParsesStrictPolicy
--- PASS: TestStrictBehindConflictDryRunRefuses
--- PASS: TestStrictBehindMovedHeadDryRunRefuses
--- PASS: TestStrictBehindUsesPinnedCompareAndRebases
--- PASS: TestStrictFreshnessIsRecheckedBeforeEveryLanding
--- PASS: TestStrictOffBehindGreenKeepsCurrentBehavior
--- PASS: TestStrictOffDoesNotCallFailingCompare
--- PASS: TestStrictUpToDateDoesNotRebase
--- PASS: TestTheHistoryListIsReadWholeOrItsErrorIsReturned
--- PASS: TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck
--- PASS: TestTrainNotesASkippedLegacyGateRecord
--- PASS: TestTrainRecordsAStoreAlertsClearingInItsOwnWords
--- PASS: TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged
pass 34 fail 5 skip 0
panic: no
failed subtests:
    --- FAIL: TestCompareDiffMismatchWords/binary_GitHub
    --- FAIL: TestCompareDiffMismatchWords/binary_git
    --- FAIL: TestCompareDiffMismatchWords/zero_count_index
    --- FAIL: TestCompareDiffMismatchWords/rename_no_change
    --- FAIL: TestCompareDiffMismatchWords/added_empty
    --- FAIL: TestCompareDiffMismatchWords/added_empty_seven_digits
    --- FAIL: TestCompareDiffMismatchWords/added_empty_full_ids
    --- FAIL: TestCompareDiffMismatchWords/deleted_empty_local_form
    --- FAIL: TestCompareDiffMismatchWords/deleted_empty_nine_digits
    --- FAIL: TestCompareDiffMismatchWords/documented_binary_delta
    --- FAIL: TestCompareDiffMismatchWords/rename_without_previous
    --- FAIL: TestCompareDiffMismatchWords/zero_with_hunk
    --- FAIL: TestCompareDiffMismatchWords/binary_no_index
    --- FAIL: TestCompareDiffMismatchWords/zero_only_header
    --- FAIL: TestCompareDiffMismatchWords/rename_lacks_to
    --- FAIL: TestCompareDiffMismatchWords/rename_wrong_from
    --- FAIL: TestCompareDiffMismatchWords/rename_99_percent
    --- FAIL: TestCompareDiffMismatchWords/mode_only_local_form
    --- FAIL: TestCompareDiffMismatchWords/cut_zero_index
    --- FAIL: TestCompareDiffMismatchWords/addition_outside_hunk
    --- FAIL: TestCompareDiffMismatchWords/line_after_binary_GitHub
    --- FAIL: TestCompareDiffMismatchWords/non-data_binary_line
    --- FAIL: TestCompareDiffMismatchWords/wrong-length_binary_data
    --- FAIL: TestCompareDiffMismatchWords/marker_without_hunk
    --- FAIL: TestCompareDiffMismatchWords/binary_cut_after_index
    --- FAIL: TestCompareDiffMismatchWords/index_word_alone
    --- FAIL: TestCompareDiffMismatchWords/empty_ids_too_short
messages (the first 12 lines, each cut at 240 characters; 40-digit commit ids printed as <sha>, 12-digit ones as <id>; white space at a line's end removed; a panic's stack left out; the texts are your tests'):
    compare_proof_test.go:164: #6 was not rebased and merged: status refused; force pushes recorded 0; head unchanged true; task bound to its old head true; audit: refusal: the update could not be proved: GitHub returned an incomplete diff
    compare_proof_test.go:191: #6 was not rebased and merged: status refused; force pushes recorded 0; head unchanged true; task bound to its old head true; audit: refusal: the update could not be proved: GitHub returned an incomplete diff
    compare_proof_test.go:205: proof error GitHub returned an incomplete diff: rows-trainfix.sh R5: a file with no changed line is refused, want a file with no changed line holds a hunk, or neither an index line nor a 100% rename's three li
    compare_proof_test.go:373: got "rows-trainfix.sh R5: a file with no changed line is refused"
    compare_proof_test.go:373: got "rows-trainfix.sh R5: a file with no changed line is refused"
    compare_proof_test.go:376: got "rows-trainfix.sh R5: a file with no changed line is refused", want prefix "a file with no changed line holds an index line and neither a binary part nor an empty file's ids: "
    compare_proof_test.go:373: got "rows-trainfix.sh R5: a file with no changed line is refused"
    compare_proof_test.go:373: got "rows-trainfix.sh R5: a file with no changed line is refused"
    compare_proof_test.go:373: got "rows-trainfix.sh R5: a file with no changed line is refused"
    compare_proof_test.go:373: got "rows-trainfix.sh R5: a file with no changed line is refused"
    compare_proof_test.go:373: got "rows-trainfix.sh R5: a file with no changed line is refused"
    compare_proof_test.go:373: got "rows-trainfix.sh R5: a file with no changed line is refused"
restore: gh.go cmp exit 0
restore: compare_history_test.go cmp exit 0
```

### R6

`bash ../../../../amq-squad-lead/T/trainfix/inputs-fu/rows-trainfix.sh b`

```text
=== row R6 ===
edit: internal/mergetrain/gh.go:240
  before: if len(files) >= MaxCompareFiles {
  after:  if len(files) >= MaxCompareFiles+1<<30 {
$ go test ./internal/mergetrain/ -count=1 -timeout 240s -v -run '^(TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged|TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate|TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate|TestBehindPRWithABinaryFileIsUpdatedAndMerged|TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate|TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead|TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged|TestCompareDiffMismatchWords|TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck|TestLeadRebindOverAFileWithNoPatchIsProvedAndBound|TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged|TestIncompleteCompareRefusesWithoutRebind|TestMaxFileCompareRefusesWithoutRebind|TestHTTP422CompareRefusesWithoutRebind|TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead|TestStrictBehindUsesPinnedCompareAndRebases|TestBehindPRWithAnEmptyFileAndAnUnchangedRenameIsUpdatedAndMerged|TestASavedCompareCutInsideItsLastLineIsRefused|TestBehindPRThatChangesALastLineWithNoNewlineIsUpdatedAndMerged|TestTheCorpusOfRealSectionsGivesItsVerdicts|TestTheHistoryListIsReadWholeOrItsErrorIsReturned|TestFakeGHRefusesADiffWithEscapeBytesUnlessAllowed|TestBehindPRWithEscapeBytesInItsDiffIsRebasedAndMerged|TestAnOlderGHThatDoesNotKnowTheFlagIsReadOnceMoreWithoutIt|TestDriftSkipsALegacyGateRecordWithOneLine|TestDriftStillNamesAMalformedGateRecord|TestTrainNotesASkippedLegacyGateRecord|TestDriftClearsAStoreAlertOnlyForAPathThisRunRead|TestTrainRecordsAStoreAlertsClearingInItsOwnWords|TestChangedPatchAfterUpdateIsRefusedWithoutRebind|TestStrictOffDoesNotCallFailingCompare|TestLaterStrictCompareErrorSkipsOnlyThatPR|TestNamedDryRunBindsTheSameMovedHeadAsRealRun|TestStrictFreshnessIsRecheckedBeforeEveryLanding|TestStrictOffBehindGreenKeepsCurrentBehavior|TestStrictBehindConflictDryRunRefuses|TestStrictBehindMovedHeadDryRunRefuses|TestStrictUpToDateDoesNotRebase|TestProtectionParsesStrictPolicy)$'
exit: 1
--- FAIL: TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead
--- PASS: TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate
--- PASS: TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate
--- PASS: TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate
--- PASS: TestASavedCompareCutInsideItsLastLineIsRefused
--- PASS: TestAnOlderGHThatDoesNotKnowTheFlagIsReadOnceMoreWithoutIt
--- PASS: TestBehindPRThatChangesALastLineWithNoNewlineIsUpdatedAndMerged
--- PASS: TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged
--- PASS: TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged
--- PASS: TestBehindPRWithABinaryFileIsUpdatedAndMerged
--- PASS: TestBehindPRWithAnEmptyFileAndAnUnchangedRenameIsUpdatedAndMerged
--- PASS: TestBehindPRWithEscapeBytesInItsDiffIsRebasedAndMerged
--- PASS: TestChangedPatchAfterUpdateIsRefusedWithoutRebind
--- PASS: TestCompareDiffMismatchWords
--- PASS: TestDriftClearsAStoreAlertOnlyForAPathThisRunRead
--- PASS: TestDriftSkipsALegacyGateRecordWithOneLine
--- PASS: TestDriftStillNamesAMalformedGateRecord
--- PASS: TestFakeGHRefusesADiffWithEscapeBytesUnlessAllowed
--- PASS: TestHTTP422CompareRefusesWithoutRebind
--- PASS: TestIncompleteCompareRefusesWithoutRebind
--- PASS: TestLaterStrictCompareErrorSkipsOnlyThatPR
--- PASS: TestLeadRebindOverAFileWithNoPatchIsProvedAndBound
--- PASS: TestMaxFileCompareRefusesWithoutRebind
--- PASS: TestNamedDryRunBindsTheSameMovedHeadAsRealRun
--- PASS: TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead
--- PASS: TestProtectionParsesStrictPolicy
--- PASS: TestStrictBehindConflictDryRunRefuses
--- PASS: TestStrictBehindMovedHeadDryRunRefuses
--- PASS: TestStrictBehindUsesPinnedCompareAndRebases
--- PASS: TestStrictFreshnessIsRecheckedBeforeEveryLanding
--- PASS: TestStrictOffBehindGreenKeepsCurrentBehavior
--- PASS: TestStrictOffDoesNotCallFailingCompare
--- PASS: TestStrictUpToDateDoesNotRebase
--- PASS: TestTheCorpusOfRealSectionsGivesItsVerdicts
--- PASS: TestTheHistoryListIsReadWholeOrItsErrorIsReturned
--- PASS: TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck
--- PASS: TestTrainNotesASkippedLegacyGateRecord
--- PASS: TestTrainRecordsAStoreAlertsClearingInItsOwnWords
--- PASS: TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged
pass 38 fail 1 skip 0
panic: no
failed subtests:
    --- FAIL: TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead/at_the_limit
messages (the first 12 lines, each cut at 240 characters; 40-digit commit ids printed as <sha>, 12-digit ones as <id>; white space at a line's end removed; a panic's stack left out; the texts are your tests'):
    compare_proof_test.go:234: limit: err=GitHub returned an incomplete diff: the diff's files are not the compare's files: missing section for "f/000" reads=1 want 0
restore: gh.go cmp exit 0
restore: compare_history_test.go cmp exit 0
```

### R7

`bash ../../../../amq-squad-lead/T/trainfix/inputs-fu/rows-trainfix.sh b`

```text
=== row R7 ===
edit: internal/mergetrain/gh.go:580
  before: return strings.Contains(s, "diff too large") || strings.Contains(s, "http 406") || strings.Contains(s, "http 422")
  after:  return strings.Contains(s, "diff too large") || strings.Contains(s, "http 406")
$ go test ./internal/mergetrain/ -count=1 -timeout 240s -v -run '^(TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged|TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate|TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate|TestBehindPRWithABinaryFileIsUpdatedAndMerged|TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate|TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead|TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged|TestCompareDiffMismatchWords|TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck|TestLeadRebindOverAFileWithNoPatchIsProvedAndBound|TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged|TestIncompleteCompareRefusesWithoutRebind|TestMaxFileCompareRefusesWithoutRebind|TestHTTP422CompareRefusesWithoutRebind|TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead|TestStrictBehindUsesPinnedCompareAndRebases|TestBehindPRWithAnEmptyFileAndAnUnchangedRenameIsUpdatedAndMerged|TestASavedCompareCutInsideItsLastLineIsRefused|TestBehindPRThatChangesALastLineWithNoNewlineIsUpdatedAndMerged|TestTheCorpusOfRealSectionsGivesItsVerdicts|TestTheHistoryListIsReadWholeOrItsErrorIsReturned|TestFakeGHRefusesADiffWithEscapeBytesUnlessAllowed|TestBehindPRWithEscapeBytesInItsDiffIsRebasedAndMerged|TestAnOlderGHThatDoesNotKnowTheFlagIsReadOnceMoreWithoutIt|TestDriftSkipsALegacyGateRecordWithOneLine|TestDriftStillNamesAMalformedGateRecord|TestTrainNotesASkippedLegacyGateRecord|TestDriftClearsAStoreAlertOnlyForAPathThisRunRead|TestTrainRecordsAStoreAlertsClearingInItsOwnWords|TestChangedPatchAfterUpdateIsRefusedWithoutRebind|TestStrictOffDoesNotCallFailingCompare|TestLaterStrictCompareErrorSkipsOnlyThatPR|TestNamedDryRunBindsTheSameMovedHeadAsRealRun|TestStrictFreshnessIsRecheckedBeforeEveryLanding|TestStrictOffBehindGreenKeepsCurrentBehavior|TestStrictBehindConflictDryRunRefuses|TestStrictBehindMovedHeadDryRunRefuses|TestStrictUpToDateDoesNotRebase|TestProtectionParsesStrictPolicy)$'
exit: 1
--- FAIL: TestHTTP422CompareRefusesWithoutRebind
--- PASS: TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate
--- PASS: TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead
--- PASS: TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate
--- PASS: TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate
--- PASS: TestASavedCompareCutInsideItsLastLineIsRefused
--- PASS: TestAnOlderGHThatDoesNotKnowTheFlagIsReadOnceMoreWithoutIt
--- PASS: TestBehindPRThatChangesALastLineWithNoNewlineIsUpdatedAndMerged
--- PASS: TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged
--- PASS: TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged
--- PASS: TestBehindPRWithABinaryFileIsUpdatedAndMerged
--- PASS: TestBehindPRWithAnEmptyFileAndAnUnchangedRenameIsUpdatedAndMerged
--- PASS: TestBehindPRWithEscapeBytesInItsDiffIsRebasedAndMerged
--- PASS: TestChangedPatchAfterUpdateIsRefusedWithoutRebind
--- PASS: TestCompareDiffMismatchWords
--- PASS: TestDriftClearsAStoreAlertOnlyForAPathThisRunRead
--- PASS: TestDriftSkipsALegacyGateRecordWithOneLine
--- PASS: TestDriftStillNamesAMalformedGateRecord
--- PASS: TestFakeGHRefusesADiffWithEscapeBytesUnlessAllowed
--- PASS: TestIncompleteCompareRefusesWithoutRebind
--- PASS: TestLaterStrictCompareErrorSkipsOnlyThatPR
--- PASS: TestLeadRebindOverAFileWithNoPatchIsProvedAndBound
--- PASS: TestMaxFileCompareRefusesWithoutRebind
--- PASS: TestNamedDryRunBindsTheSameMovedHeadAsRealRun
--- PASS: TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead
--- PASS: TestProtectionParsesStrictPolicy
--- PASS: TestStrictBehindConflictDryRunRefuses
--- PASS: TestStrictBehindMovedHeadDryRunRefuses
--- PASS: TestStrictBehindUsesPinnedCompareAndRebases
--- PASS: TestStrictFreshnessIsRecheckedBeforeEveryLanding
--- PASS: TestStrictOffBehindGreenKeepsCurrentBehavior
--- PASS: TestStrictOffDoesNotCallFailingCompare
--- PASS: TestStrictUpToDateDoesNotRebase
--- PASS: TestTheCorpusOfRealSectionsGivesItsVerdicts
--- PASS: TestTheHistoryListIsReadWholeOrItsErrorIsReturned
--- PASS: TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck
--- PASS: TestTrainNotesASkippedLegacyGateRecord
--- PASS: TestTrainRecordsAStoreAlertsClearingInItsOwnWords
--- PASS: TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged
pass 38 fail 1 skip 0
panic: no
failed subtests:
messages (the first 12 lines, each cut at 240 characters; 40-digit commit ids printed as <sha>, 12-digit ones as <id>; white space at a line's end removed; a panic's stack left out; the texts are your tests'):
    strict_policy_test.go:273: incomplete compare was not refused in words: prove the updated patch of #1: gh api repos/owner/fixture/compare/<sha>...<sha> --jq: exit status 1: gh: compare result unavailable (HTTP 422) {Number:1 Task:t1 Hea
restore: gh.go cmp exit 0
restore: compare_history_test.go cmp exit 0
```

### R8

`bash ../../../../amq-squad-lead/T/trainfix/inputs-fu/rows-trainfix.sh b`

```text
=== row R8 ===
edit: internal/mergetrain/gh.go:580
  before: return strings.Contains(s, "diff too large") || strings.Contains(s, "http 406") || strings.Contains(s, "http 422")
  after:  return strings.Contains(s, "diff too large") || strings.Contains(s, "http 422")
$ go test ./internal/mergetrain/ -count=1 -timeout 240s -v -run '^(TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged|TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate|TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate|TestBehindPRWithABinaryFileIsUpdatedAndMerged|TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate|TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead|TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged|TestCompareDiffMismatchWords|TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck|TestLeadRebindOverAFileWithNoPatchIsProvedAndBound|TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged|TestIncompleteCompareRefusesWithoutRebind|TestMaxFileCompareRefusesWithoutRebind|TestHTTP422CompareRefusesWithoutRebind|TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead|TestStrictBehindUsesPinnedCompareAndRebases|TestBehindPRWithAnEmptyFileAndAnUnchangedRenameIsUpdatedAndMerged|TestASavedCompareCutInsideItsLastLineIsRefused|TestBehindPRThatChangesALastLineWithNoNewlineIsUpdatedAndMerged|TestTheCorpusOfRealSectionsGivesItsVerdicts|TestTheHistoryListIsReadWholeOrItsErrorIsReturned|TestFakeGHRefusesADiffWithEscapeBytesUnlessAllowed|TestBehindPRWithEscapeBytesInItsDiffIsRebasedAndMerged|TestAnOlderGHThatDoesNotKnowTheFlagIsReadOnceMoreWithoutIt|TestDriftSkipsALegacyGateRecordWithOneLine|TestDriftStillNamesAMalformedGateRecord|TestTrainNotesASkippedLegacyGateRecord|TestDriftClearsAStoreAlertOnlyForAPathThisRunRead|TestTrainRecordsAStoreAlertsClearingInItsOwnWords|TestChangedPatchAfterUpdateIsRefusedWithoutRebind|TestStrictOffDoesNotCallFailingCompare|TestLaterStrictCompareErrorSkipsOnlyThatPR|TestNamedDryRunBindsTheSameMovedHeadAsRealRun|TestStrictFreshnessIsRecheckedBeforeEveryLanding|TestStrictOffBehindGreenKeepsCurrentBehavior|TestStrictBehindConflictDryRunRefuses|TestStrictBehindMovedHeadDryRunRefuses|TestStrictUpToDateDoesNotRebase|TestProtectionParsesStrictPolicy)$'
exit: 1
--- FAIL: TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead
--- PASS: TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate
--- PASS: TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead
--- PASS: TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate
--- PASS: TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate
--- PASS: TestASavedCompareCutInsideItsLastLineIsRefused
--- PASS: TestAnOlderGHThatDoesNotKnowTheFlagIsReadOnceMoreWithoutIt
--- PASS: TestBehindPRThatChangesALastLineWithNoNewlineIsUpdatedAndMerged
--- PASS: TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged
--- PASS: TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged
--- PASS: TestBehindPRWithABinaryFileIsUpdatedAndMerged
--- PASS: TestBehindPRWithAnEmptyFileAndAnUnchangedRenameIsUpdatedAndMerged
--- PASS: TestBehindPRWithEscapeBytesInItsDiffIsRebasedAndMerged
--- PASS: TestChangedPatchAfterUpdateIsRefusedWithoutRebind
--- PASS: TestCompareDiffMismatchWords
--- PASS: TestDriftClearsAStoreAlertOnlyForAPathThisRunRead
--- PASS: TestDriftSkipsALegacyGateRecordWithOneLine
--- PASS: TestDriftStillNamesAMalformedGateRecord
--- PASS: TestFakeGHRefusesADiffWithEscapeBytesUnlessAllowed
--- PASS: TestHTTP422CompareRefusesWithoutRebind
--- PASS: TestIncompleteCompareRefusesWithoutRebind
--- PASS: TestLaterStrictCompareErrorSkipsOnlyThatPR
--- PASS: TestLeadRebindOverAFileWithNoPatchIsProvedAndBound
--- PASS: TestMaxFileCompareRefusesWithoutRebind
--- PASS: TestNamedDryRunBindsTheSameMovedHeadAsRealRun
--- PASS: TestProtectionParsesStrictPolicy
--- PASS: TestStrictBehindConflictDryRunRefuses
--- PASS: TestStrictBehindMovedHeadDryRunRefuses
--- PASS: TestStrictBehindUsesPinnedCompareAndRebases
--- PASS: TestStrictFreshnessIsRecheckedBeforeEveryLanding
--- PASS: TestStrictOffBehindGreenKeepsCurrentBehavior
--- PASS: TestStrictOffDoesNotCallFailingCompare
--- PASS: TestStrictUpToDateDoesNotRebase
--- PASS: TestTheCorpusOfRealSectionsGivesItsVerdicts
--- PASS: TestTheHistoryListIsReadWholeOrItsErrorIsReturned
--- PASS: TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck
--- PASS: TestTrainNotesASkippedLegacyGateRecord
--- PASS: TestTrainRecordsAStoreAlertsClearingInItsOwnWords
--- PASS: TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged
pass 38 fail 1 skip 0
panic: no
failed subtests:
    --- FAIL: TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead/a_diff_too_large
    --- FAIL: TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead/an_unknown_flag,_then_a_diff_too_large
messages (the first 12 lines, each cut at 240 characters; 40-digit commit ids printed as <sha>, 12-digit ones as <id>; white space at a line's end removed; a panic's stack left out; the texts are your tests'):
    escape_legacy_320_test.go:227: a diff too large is an incomplete diff: gh api repos/owner/fixture/compare/<sha>...<sha> -H: exit status 1: gh: Sorry, the diff exceeded the maximum number of lines (HTTP 406)
    escape_legacy_320_test.go:227: a diff too large is an incomplete diff: gh api repos/owner/fixture/compare/<sha>...<sha> -H: exit status 1: gh: Sorry, the diff exceeded the maximum number of lines (HTTP 406)
restore: gh.go cmp exit 0
restore: compare_history_test.go cmp exit 0
```

### R9

`bash ../../../../amq-squad-lead/T/trainfix/inputs-fu/rows-trainfix.sh b`

```text
=== row R9 ===
edit: internal/mergetrain/gh.go:279
  before: func compareDiffMismatch(files []FileChange, diff []byte) string {
  after:  func compareDiffMismatchYours(files []FileChange, diff []byte) string {
  appended: a compareDiffMismatch that calls yours and answers "" when its answer starts with:
    "the diff is cut short inside a hunk: "
$ go test ./internal/mergetrain/ -count=1 -timeout 240s -v -run '^(TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged|TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate|TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate|TestBehindPRWithABinaryFileIsUpdatedAndMerged|TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate|TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead|TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged|TestCompareDiffMismatchWords|TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck|TestLeadRebindOverAFileWithNoPatchIsProvedAndBound|TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged|TestIncompleteCompareRefusesWithoutRebind|TestMaxFileCompareRefusesWithoutRebind|TestHTTP422CompareRefusesWithoutRebind|TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead|TestStrictBehindUsesPinnedCompareAndRebases|TestBehindPRWithAnEmptyFileAndAnUnchangedRenameIsUpdatedAndMerged|TestASavedCompareCutInsideItsLastLineIsRefused|TestBehindPRThatChangesALastLineWithNoNewlineIsUpdatedAndMerged|TestTheCorpusOfRealSectionsGivesItsVerdicts|TestTheHistoryListIsReadWholeOrItsErrorIsReturned|TestFakeGHRefusesADiffWithEscapeBytesUnlessAllowed|TestBehindPRWithEscapeBytesInItsDiffIsRebasedAndMerged|TestAnOlderGHThatDoesNotKnowTheFlagIsReadOnceMoreWithoutIt|TestDriftSkipsALegacyGateRecordWithOneLine|TestDriftStillNamesAMalformedGateRecord|TestTrainNotesASkippedLegacyGateRecord|TestDriftClearsAStoreAlertOnlyForAPathThisRunRead|TestTrainRecordsAStoreAlertsClearingInItsOwnWords|TestChangedPatchAfterUpdateIsRefusedWithoutRebind|TestStrictOffDoesNotCallFailingCompare|TestLaterStrictCompareErrorSkipsOnlyThatPR|TestNamedDryRunBindsTheSameMovedHeadAsRealRun|TestStrictFreshnessIsRecheckedBeforeEveryLanding|TestStrictOffBehindGreenKeepsCurrentBehavior|TestStrictBehindConflictDryRunRefuses|TestStrictBehindMovedHeadDryRunRefuses|TestStrictUpToDateDoesNotRebase|TestProtectionParsesStrictPolicy)$'
exit: 1
--- FAIL: TestCompareDiffMismatchWords
--- FAIL: TestIncompleteCompareRefusesWithoutRebind
--- FAIL: TestTheCorpusOfRealSectionsGivesItsVerdicts
--- PASS: TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate
--- PASS: TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead
--- PASS: TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate
--- PASS: TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate
--- PASS: TestASavedCompareCutInsideItsLastLineIsRefused
--- PASS: TestAnOlderGHThatDoesNotKnowTheFlagIsReadOnceMoreWithoutIt
--- PASS: TestBehindPRThatChangesALastLineWithNoNewlineIsUpdatedAndMerged
--- PASS: TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged
--- PASS: TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged
--- PASS: TestBehindPRWithABinaryFileIsUpdatedAndMerged
--- PASS: TestBehindPRWithAnEmptyFileAndAnUnchangedRenameIsUpdatedAndMerged
--- PASS: TestBehindPRWithEscapeBytesInItsDiffIsRebasedAndMerged
--- PASS: TestChangedPatchAfterUpdateIsRefusedWithoutRebind
--- PASS: TestDriftClearsAStoreAlertOnlyForAPathThisRunRead
--- PASS: TestDriftSkipsALegacyGateRecordWithOneLine
--- PASS: TestDriftStillNamesAMalformedGateRecord
--- PASS: TestFakeGHRefusesADiffWithEscapeBytesUnlessAllowed
--- PASS: TestHTTP422CompareRefusesWithoutRebind
--- PASS: TestLaterStrictCompareErrorSkipsOnlyThatPR
--- PASS: TestLeadRebindOverAFileWithNoPatchIsProvedAndBound
--- PASS: TestMaxFileCompareRefusesWithoutRebind
--- PASS: TestNamedDryRunBindsTheSameMovedHeadAsRealRun
--- PASS: TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead
--- PASS: TestProtectionParsesStrictPolicy
--- PASS: TestStrictBehindConflictDryRunRefuses
--- PASS: TestStrictBehindMovedHeadDryRunRefuses
--- PASS: TestStrictBehindUsesPinnedCompareAndRebases
--- PASS: TestStrictFreshnessIsRecheckedBeforeEveryLanding
--- PASS: TestStrictOffBehindGreenKeepsCurrentBehavior
--- PASS: TestStrictOffDoesNotCallFailingCompare
--- PASS: TestStrictUpToDateDoesNotRebase
--- PASS: TestTheHistoryListIsReadWholeOrItsErrorIsReturned
--- PASS: TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck
--- PASS: TestTrainNotesASkippedLegacyGateRecord
--- PASS: TestTrainRecordsAStoreAlertsClearingInItsOwnWords
--- PASS: TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged
pass 36 fail 3 skip 0
panic: no
failed subtests:
    --- FAIL: TestCompareDiffMismatchWords/cut_final_context
    --- FAIL: TestCompareDiffMismatchWords/next_section_inside_hunk
    --- FAIL: TestCompareDiffMismatchWords/blank_end_cut
    --- FAIL: TestCompareDiffMismatchWords/cut_after_marker_inside_hunk
    --- FAIL: TestCompareDiffMismatchWords/marker_directly_after_hunk_header
    --- FAIL: TestCompareDiffMismatchWords/consecutive_markers_inside_hunk
    --- FAIL: TestCompareDiffMismatchWords/garbage_hunk_numbers
    --- FAIL: TestCompareDiffMismatchWords/signed_hunk_lengths
messages (the first 12 lines, each cut at 240 characters; 40-digit commit ids printed as <sha>, 12-digit ones as <id>; white space at a line's end removed; a panic's stack left out; the texts are your tests'):
    compare_proof_test.go:376: got "", want prefix "the diff is cut short inside a hunk: "
    compare_proof_test.go:376: got "", want prefix "the diff is cut short inside a hunk: "
    compare_proof_test.go:376: got "", want prefix "the diff is cut short inside a hunk: "
    compare_proof_test.go:376: got "", want prefix "the diff is cut short inside a hunk: "
    compare_proof_test.go:376: got "", want prefix "the diff is cut short inside a hunk: "
    compare_proof_test.go:376: got "", want prefix "the diff is cut short inside a hunk: "
    compare_proof_test.go:376: got "", want prefix "the diff is cut short inside a hunk: "
    compare_proof_test.go:376: got "", want prefix "the diff is cut short inside a hunk: "
    compare_proof_test.go:424: history: the same section cut just after its in-hunk marker (local git: `git diff -M <sha> <sha>` in omriariav/amq-squad, the section of docs/v2.0.0-goal-condensed.md, cut at the line boundary after its first
    strict_policy_test.go:88: incomplete compare was not refused in words: <nil> {Number:1 Task:t1 Head:<sha> Draft:true Changed:2 Files:[pkg/a.go] Refusals:[{Rule:rebase_patch_changed Detail:its change moved during the update}] GateID: Gat
restore: gh.go cmp exit 0
restore: compare_history_test.go cmp exit 0
keep directory removed: yes
```

### R10

`bash ../../../../amq-squad-lead/T/trainfix/inputs-fu/rows-trainfix.sh c`

```text
=== row R10 ===
edit: internal/mergetrain/gh.go:279
  before: func compareDiffMismatch(files []FileChange, diff []byte) string {
  after:  func compareDiffMismatchYours(files []FileChange, diff []byte) string {
  appended: a compareDiffMismatch that calls yours with every file's previous name emptied
$ go test ./internal/mergetrain/ -count=1 -timeout 240s -v -run '^(TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged|TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate|TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate|TestBehindPRWithABinaryFileIsUpdatedAndMerged|TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate|TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead|TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged|TestCompareDiffMismatchWords|TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck|TestLeadRebindOverAFileWithNoPatchIsProvedAndBound|TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged|TestIncompleteCompareRefusesWithoutRebind|TestMaxFileCompareRefusesWithoutRebind|TestHTTP422CompareRefusesWithoutRebind|TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead|TestStrictBehindUsesPinnedCompareAndRebases|TestBehindPRWithAnEmptyFileAndAnUnchangedRenameIsUpdatedAndMerged|TestASavedCompareCutInsideItsLastLineIsRefused|TestBehindPRThatChangesALastLineWithNoNewlineIsUpdatedAndMerged|TestTheCorpusOfRealSectionsGivesItsVerdicts|TestTheHistoryListIsReadWholeOrItsErrorIsReturned|TestFakeGHRefusesADiffWithEscapeBytesUnlessAllowed|TestBehindPRWithEscapeBytesInItsDiffIsRebasedAndMerged|TestAnOlderGHThatDoesNotKnowTheFlagIsReadOnceMoreWithoutIt|TestDriftSkipsALegacyGateRecordWithOneLine|TestDriftStillNamesAMalformedGateRecord|TestTrainNotesASkippedLegacyGateRecord|TestDriftClearsAStoreAlertOnlyForAPathThisRunRead|TestTrainRecordsAStoreAlertsClearingInItsOwnWords|TestChangedPatchAfterUpdateIsRefusedWithoutRebind|TestStrictOffDoesNotCallFailingCompare|TestLaterStrictCompareErrorSkipsOnlyThatPR|TestNamedDryRunBindsTheSameMovedHeadAsRealRun|TestStrictFreshnessIsRecheckedBeforeEveryLanding|TestStrictOffBehindGreenKeepsCurrentBehavior|TestStrictBehindConflictDryRunRefuses|TestStrictBehindMovedHeadDryRunRefuses|TestStrictUpToDateDoesNotRebase|TestProtectionParsesStrictPolicy)$'
exit: 1
--- FAIL: TestBehindPRWithAnEmptyFileAndAnUnchangedRenameIsUpdatedAndMerged
--- FAIL: TestCompareDiffMismatchWords
--- FAIL: TestTheCorpusOfRealSectionsGivesItsVerdicts
--- PASS: TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate
--- PASS: TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead
--- PASS: TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate
--- PASS: TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate
--- PASS: TestASavedCompareCutInsideItsLastLineIsRefused
--- PASS: TestAnOlderGHThatDoesNotKnowTheFlagIsReadOnceMoreWithoutIt
--- PASS: TestBehindPRThatChangesALastLineWithNoNewlineIsUpdatedAndMerged
--- PASS: TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged
--- PASS: TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged
--- PASS: TestBehindPRWithABinaryFileIsUpdatedAndMerged
--- PASS: TestBehindPRWithEscapeBytesInItsDiffIsRebasedAndMerged
--- PASS: TestChangedPatchAfterUpdateIsRefusedWithoutRebind
--- PASS: TestDriftClearsAStoreAlertOnlyForAPathThisRunRead
--- PASS: TestDriftSkipsALegacyGateRecordWithOneLine
--- PASS: TestDriftStillNamesAMalformedGateRecord
--- PASS: TestFakeGHRefusesADiffWithEscapeBytesUnlessAllowed
--- PASS: TestHTTP422CompareRefusesWithoutRebind
--- PASS: TestIncompleteCompareRefusesWithoutRebind
--- PASS: TestLaterStrictCompareErrorSkipsOnlyThatPR
--- PASS: TestLeadRebindOverAFileWithNoPatchIsProvedAndBound
--- PASS: TestMaxFileCompareRefusesWithoutRebind
--- PASS: TestNamedDryRunBindsTheSameMovedHeadAsRealRun
--- PASS: TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead
--- PASS: TestProtectionParsesStrictPolicy
--- PASS: TestStrictBehindConflictDryRunRefuses
--- PASS: TestStrictBehindMovedHeadDryRunRefuses
--- PASS: TestStrictBehindUsesPinnedCompareAndRebases
--- PASS: TestStrictFreshnessIsRecheckedBeforeEveryLanding
--- PASS: TestStrictOffBehindGreenKeepsCurrentBehavior
--- PASS: TestStrictOffDoesNotCallFailingCompare
--- PASS: TestStrictUpToDateDoesNotRebase
--- PASS: TestTheHistoryListIsReadWholeOrItsErrorIsReturned
--- PASS: TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck
--- PASS: TestTrainNotesASkippedLegacyGateRecord
--- PASS: TestTrainRecordsAStoreAlertsClearingInItsOwnWords
--- PASS: TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged
pass 36 fail 3 skip 0
panic: no
failed subtests:
    --- FAIL: TestCompareDiffMismatchWords/renamed_with_changes
    --- FAIL: TestCompareDiffMismatchWords/rename_no_change
    --- FAIL: TestCompareDiffMismatchWords/rename_lacks_to
    --- FAIL: TestCompareDiffMismatchWords/rename_wrong_from
    --- FAIL: TestCompareDiffMismatchWords/rename_99_percent
messages (the first 12 lines, each cut at 240 characters; 40-digit commit ids printed as <sha>, 12-digit ones as <id>; white space at a line's end removed; a panic's stack left out; the texts are your tests'):
    compare_proof_test.go:191: #6 was not rebased and merged: status refused; force pushes recorded 0; head unchanged true; task bound to its old head true; audit: refusal: the update could not be proved: GitHub returned an incomplete diff
    compare_proof_test.go:373: got "the diff's files are not the compare's files: unexpected section \"diff --git a/old.txt b/new.txt\""
    compare_proof_test.go:373: got "the diff's files are not the compare's files: unexpected section \"diff --git a/internal/cli/v3_holding_test.go b/internal/cli/cli_fixture_support_test.go\""
    compare_proof_test.go:376: got "the diff's files are not the compare's files: unexpected section \"diff --git a/internal/cli/v3_holding_test.go b/internal/cli/cli_fixture_support_test.go\"", want prefix "a file with no changed line hold
    compare_proof_test.go:376: got "the diff's files are not the compare's files: unexpected section \"diff --git a/internal/cli/v3_holding_test.go b/internal/cli/cli_fixture_support_test.go\"", want prefix "a file with no changed line hold
    compare_proof_test.go:376: got "the diff's files are not the compare's files: unexpected section \"diff --git a/internal/cli/v3_holding_test.go b/internal/cli/cli_fixture_support_test.go\"", want prefix "a file with no changed line hold
    compare_proof_test.go:418: GitHub: a rename with no change (as GitHub served it: the diff media type of compare <sha>...<sha> of omriariav/amq-squad, one section): got "the diff's files are not the compare's files: unexpected section \"
restore: gh.go cmp exit 0
restore: compare_history_test.go cmp exit 0
```

### R11

`bash ../../../../amq-squad-lead/T/trainfix/inputs-fu/rows-trainfix.sh c`

```text
=== row R11 ===
edit: internal/mergetrain/gh.go:279
  before: func compareDiffMismatch(files []FileChange, diff []byte) string {
  after:  func compareDiffMismatchYours(files []FileChange, diff []byte) string {
  appended: a compareDiffMismatch that calls yours and answers "" to its counts text when the totals over all files agree
$ go test ./internal/mergetrain/ -count=1 -timeout 240s -v -run '^(TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged|TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate|TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate|TestBehindPRWithABinaryFileIsUpdatedAndMerged|TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate|TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead|TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged|TestCompareDiffMismatchWords|TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck|TestLeadRebindOverAFileWithNoPatchIsProvedAndBound|TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged|TestIncompleteCompareRefusesWithoutRebind|TestMaxFileCompareRefusesWithoutRebind|TestHTTP422CompareRefusesWithoutRebind|TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead|TestStrictBehindUsesPinnedCompareAndRebases|TestBehindPRWithAnEmptyFileAndAnUnchangedRenameIsUpdatedAndMerged|TestASavedCompareCutInsideItsLastLineIsRefused|TestBehindPRThatChangesALastLineWithNoNewlineIsUpdatedAndMerged|TestTheCorpusOfRealSectionsGivesItsVerdicts|TestTheHistoryListIsReadWholeOrItsErrorIsReturned|TestFakeGHRefusesADiffWithEscapeBytesUnlessAllowed|TestBehindPRWithEscapeBytesInItsDiffIsRebasedAndMerged|TestAnOlderGHThatDoesNotKnowTheFlagIsReadOnceMoreWithoutIt|TestDriftSkipsALegacyGateRecordWithOneLine|TestDriftStillNamesAMalformedGateRecord|TestTrainNotesASkippedLegacyGateRecord|TestDriftClearsAStoreAlertOnlyForAPathThisRunRead|TestTrainRecordsAStoreAlertsClearingInItsOwnWords|TestChangedPatchAfterUpdateIsRefusedWithoutRebind|TestStrictOffDoesNotCallFailingCompare|TestLaterStrictCompareErrorSkipsOnlyThatPR|TestNamedDryRunBindsTheSameMovedHeadAsRealRun|TestStrictFreshnessIsRecheckedBeforeEveryLanding|TestStrictOffBehindGreenKeepsCurrentBehavior|TestStrictBehindConflictDryRunRefuses|TestStrictBehindMovedHeadDryRunRefuses|TestStrictUpToDateDoesNotRebase|TestProtectionParsesStrictPolicy)$'
exit: 1
--- FAIL: TestCompareDiffMismatchWords
--- PASS: TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate
--- PASS: TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead
--- PASS: TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate
--- PASS: TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate
--- PASS: TestASavedCompareCutInsideItsLastLineIsRefused
--- PASS: TestAnOlderGHThatDoesNotKnowTheFlagIsReadOnceMoreWithoutIt
--- PASS: TestBehindPRThatChangesALastLineWithNoNewlineIsUpdatedAndMerged
--- PASS: TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged
--- PASS: TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged
--- PASS: TestBehindPRWithABinaryFileIsUpdatedAndMerged
--- PASS: TestBehindPRWithAnEmptyFileAndAnUnchangedRenameIsUpdatedAndMerged
--- PASS: TestBehindPRWithEscapeBytesInItsDiffIsRebasedAndMerged
--- PASS: TestChangedPatchAfterUpdateIsRefusedWithoutRebind
--- PASS: TestDriftClearsAStoreAlertOnlyForAPathThisRunRead
--- PASS: TestDriftSkipsALegacyGateRecordWithOneLine
--- PASS: TestDriftStillNamesAMalformedGateRecord
--- PASS: TestFakeGHRefusesADiffWithEscapeBytesUnlessAllowed
--- PASS: TestHTTP422CompareRefusesWithoutRebind
--- PASS: TestIncompleteCompareRefusesWithoutRebind
--- PASS: TestLaterStrictCompareErrorSkipsOnlyThatPR
--- PASS: TestLeadRebindOverAFileWithNoPatchIsProvedAndBound
--- PASS: TestMaxFileCompareRefusesWithoutRebind
--- PASS: TestNamedDryRunBindsTheSameMovedHeadAsRealRun
--- PASS: TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead
--- PASS: TestProtectionParsesStrictPolicy
--- PASS: TestStrictBehindConflictDryRunRefuses
--- PASS: TestStrictBehindMovedHeadDryRunRefuses
--- PASS: TestStrictBehindUsesPinnedCompareAndRebases
--- PASS: TestStrictFreshnessIsRecheckedBeforeEveryLanding
--- PASS: TestStrictOffBehindGreenKeepsCurrentBehavior
--- PASS: TestStrictOffDoesNotCallFailingCompare
--- PASS: TestStrictUpToDateDoesNotRebase
--- PASS: TestTheCorpusOfRealSectionsGivesItsVerdicts
--- PASS: TestTheHistoryListIsReadWholeOrItsErrorIsReturned
--- PASS: TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck
--- PASS: TestTrainNotesASkippedLegacyGateRecord
--- PASS: TestTrainRecordsAStoreAlertsClearingInItsOwnWords
--- PASS: TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged
pass 38 fail 1 skip 0
panic: no
failed subtests:
    --- FAIL: TestCompareDiffMismatchWords/swapped_per-file_counts
messages (the first 12 lines, each cut at 240 characters; 40-digit commit ids printed as <sha>, 12-digit ones as <id>; white space at a line's end removed; a panic's stack left out; the texts are your tests'):
    compare_proof_test.go:376: got "", want prefix "the diff's added and removed lines are not the compare's counts: "
restore: gh.go cmp exit 0
restore: compare_history_test.go cmp exit 0
```

### R12

`bash ../../../../amq-squad-lead/T/trainfix/inputs-fu/rows-trainfix.sh c`

```text
=== row R12 ===
edit: internal/mergetrain/gh.go:265
  before: cmd.Stdin = bytes.NewReader(diff)
  after:  cmd.Stdin = bytes.NewReader(diff[:len(diff)/2])
$ go test ./internal/mergetrain/ -count=1 -timeout 240s -v -run '^(TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged|TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate|TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate|TestBehindPRWithABinaryFileIsUpdatedAndMerged|TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate|TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead|TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged|TestCompareDiffMismatchWords|TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck|TestLeadRebindOverAFileWithNoPatchIsProvedAndBound|TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged|TestIncompleteCompareRefusesWithoutRebind|TestMaxFileCompareRefusesWithoutRebind|TestHTTP422CompareRefusesWithoutRebind|TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead|TestStrictBehindUsesPinnedCompareAndRebases|TestBehindPRWithAnEmptyFileAndAnUnchangedRenameIsUpdatedAndMerged|TestASavedCompareCutInsideItsLastLineIsRefused|TestBehindPRThatChangesALastLineWithNoNewlineIsUpdatedAndMerged|TestTheCorpusOfRealSectionsGivesItsVerdicts|TestTheHistoryListIsReadWholeOrItsErrorIsReturned|TestFakeGHRefusesADiffWithEscapeBytesUnlessAllowed|TestBehindPRWithEscapeBytesInItsDiffIsRebasedAndMerged|TestAnOlderGHThatDoesNotKnowTheFlagIsReadOnceMoreWithoutIt|TestDriftSkipsALegacyGateRecordWithOneLine|TestDriftStillNamesAMalformedGateRecord|TestTrainNotesASkippedLegacyGateRecord|TestDriftClearsAStoreAlertOnlyForAPathThisRunRead|TestTrainRecordsAStoreAlertsClearingInItsOwnWords|TestChangedPatchAfterUpdateIsRefusedWithoutRebind|TestStrictOffDoesNotCallFailingCompare|TestLaterStrictCompareErrorSkipsOnlyThatPR|TestNamedDryRunBindsTheSameMovedHeadAsRealRun|TestStrictFreshnessIsRecheckedBeforeEveryLanding|TestStrictOffBehindGreenKeepsCurrentBehavior|TestStrictBehindConflictDryRunRefuses|TestStrictBehindMovedHeadDryRunRefuses|TestStrictUpToDateDoesNotRebase|TestProtectionParsesStrictPolicy)$'
exit: 1
--- FAIL: TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck
--- PASS: TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate
--- PASS: TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead
--- PASS: TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate
--- PASS: TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate
--- PASS: TestASavedCompareCutInsideItsLastLineIsRefused
--- PASS: TestAnOlderGHThatDoesNotKnowTheFlagIsReadOnceMoreWithoutIt
--- PASS: TestBehindPRThatChangesALastLineWithNoNewlineIsUpdatedAndMerged
--- PASS: TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged
--- PASS: TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged
--- PASS: TestBehindPRWithABinaryFileIsUpdatedAndMerged
--- PASS: TestBehindPRWithAnEmptyFileAndAnUnchangedRenameIsUpdatedAndMerged
--- PASS: TestBehindPRWithEscapeBytesInItsDiffIsRebasedAndMerged
--- PASS: TestChangedPatchAfterUpdateIsRefusedWithoutRebind
--- PASS: TestCompareDiffMismatchWords
--- PASS: TestDriftClearsAStoreAlertOnlyForAPathThisRunRead
--- PASS: TestDriftSkipsALegacyGateRecordWithOneLine
--- PASS: TestDriftStillNamesAMalformedGateRecord
--- PASS: TestFakeGHRefusesADiffWithEscapeBytesUnlessAllowed
--- PASS: TestHTTP422CompareRefusesWithoutRebind
--- PASS: TestIncompleteCompareRefusesWithoutRebind
--- PASS: TestLaterStrictCompareErrorSkipsOnlyThatPR
--- PASS: TestLeadRebindOverAFileWithNoPatchIsProvedAndBound
--- PASS: TestMaxFileCompareRefusesWithoutRebind
--- PASS: TestNamedDryRunBindsTheSameMovedHeadAsRealRun
--- PASS: TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead
--- PASS: TestProtectionParsesStrictPolicy
--- PASS: TestStrictBehindConflictDryRunRefuses
--- PASS: TestStrictBehindMovedHeadDryRunRefuses
--- PASS: TestStrictBehindUsesPinnedCompareAndRebases
--- PASS: TestStrictFreshnessIsRecheckedBeforeEveryLanding
--- PASS: TestStrictOffBehindGreenKeepsCurrentBehavior
--- PASS: TestStrictOffDoesNotCallFailingCompare
--- PASS: TestStrictUpToDateDoesNotRebase
--- PASS: TestTheCorpusOfRealSectionsGivesItsVerdicts
--- PASS: TestTheHistoryListIsReadWholeOrItsErrorIsReturned
--- PASS: TestTrainNotesASkippedLegacyGateRecord
--- PASS: TestTrainRecordsAStoreAlertsClearingInItsOwnWords
--- PASS: TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged
pass 38 fail 1 skip 0
panic: no
failed subtests:
messages (the first 12 lines, each cut at 240 characters; 40-digit commit ids printed as <sha>, 12-digit ones as <id>; white space at a line's end removed; a panic's stack left out; the texts are your tests'):
    compare_proof_test.go:472: patch id "<sha>" err <nil>
restore: gh.go cmp exit 0
restore: compare_history_test.go cmp exit 0
```

### R13

`bash ../../../../amq-squad-lead/T/trainfix/inputs-fu/rows-trainfix.sh c`

```text
=== row R13 ===
edit: internal/mergetrain/gh.go:279
  before: func compareDiffMismatch(files []FileChange, diff []byte) string {
  after:  func compareDiffMismatchYours(files []FileChange, diff []byte) string {
  appended: a compareDiffMismatch that calls yours with the diff's end trimmed
$ go test ./internal/mergetrain/ -count=1 -timeout 240s -v -run '^(TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged|TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate|TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate|TestBehindPRWithABinaryFileIsUpdatedAndMerged|TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate|TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead|TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged|TestCompareDiffMismatchWords|TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck|TestLeadRebindOverAFileWithNoPatchIsProvedAndBound|TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged|TestIncompleteCompareRefusesWithoutRebind|TestMaxFileCompareRefusesWithoutRebind|TestHTTP422CompareRefusesWithoutRebind|TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead|TestStrictBehindUsesPinnedCompareAndRebases|TestBehindPRWithAnEmptyFileAndAnUnchangedRenameIsUpdatedAndMerged|TestASavedCompareCutInsideItsLastLineIsRefused|TestBehindPRThatChangesALastLineWithNoNewlineIsUpdatedAndMerged|TestTheCorpusOfRealSectionsGivesItsVerdicts|TestTheHistoryListIsReadWholeOrItsErrorIsReturned|TestFakeGHRefusesADiffWithEscapeBytesUnlessAllowed|TestBehindPRWithEscapeBytesInItsDiffIsRebasedAndMerged|TestAnOlderGHThatDoesNotKnowTheFlagIsReadOnceMoreWithoutIt|TestDriftSkipsALegacyGateRecordWithOneLine|TestDriftStillNamesAMalformedGateRecord|TestTrainNotesASkippedLegacyGateRecord|TestDriftClearsAStoreAlertOnlyForAPathThisRunRead|TestTrainRecordsAStoreAlertsClearingInItsOwnWords|TestChangedPatchAfterUpdateIsRefusedWithoutRebind|TestStrictOffDoesNotCallFailingCompare|TestLaterStrictCompareErrorSkipsOnlyThatPR|TestNamedDryRunBindsTheSameMovedHeadAsRealRun|TestStrictFreshnessIsRecheckedBeforeEveryLanding|TestStrictOffBehindGreenKeepsCurrentBehavior|TestStrictBehindConflictDryRunRefuses|TestStrictBehindMovedHeadDryRunRefuses|TestStrictUpToDateDoesNotRebase|TestProtectionParsesStrictPolicy)$'
exit: 1
--- FAIL: TestASavedCompareCutInsideItsLastLineIsRefused
--- FAIL: TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged
--- FAIL: TestCompareDiffMismatchWords
--- FAIL: TestTheCorpusOfRealSectionsGivesItsVerdicts
--- PASS: TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate
--- PASS: TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead
--- PASS: TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate
--- PASS: TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate
--- PASS: TestAnOlderGHThatDoesNotKnowTheFlagIsReadOnceMoreWithoutIt
--- PASS: TestBehindPRThatChangesALastLineWithNoNewlineIsUpdatedAndMerged
--- PASS: TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged
--- PASS: TestBehindPRWithABinaryFileIsUpdatedAndMerged
--- PASS: TestBehindPRWithAnEmptyFileAndAnUnchangedRenameIsUpdatedAndMerged
--- PASS: TestBehindPRWithEscapeBytesInItsDiffIsRebasedAndMerged
--- PASS: TestChangedPatchAfterUpdateIsRefusedWithoutRebind
--- PASS: TestDriftClearsAStoreAlertOnlyForAPathThisRunRead
--- PASS: TestDriftSkipsALegacyGateRecordWithOneLine
--- PASS: TestDriftStillNamesAMalformedGateRecord
--- PASS: TestFakeGHRefusesADiffWithEscapeBytesUnlessAllowed
--- PASS: TestHTTP422CompareRefusesWithoutRebind
--- PASS: TestIncompleteCompareRefusesWithoutRebind
--- PASS: TestLaterStrictCompareErrorSkipsOnlyThatPR
--- PASS: TestLeadRebindOverAFileWithNoPatchIsProvedAndBound
--- PASS: TestMaxFileCompareRefusesWithoutRebind
--- PASS: TestNamedDryRunBindsTheSameMovedHeadAsRealRun
--- PASS: TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead
--- PASS: TestProtectionParsesStrictPolicy
--- PASS: TestStrictBehindConflictDryRunRefuses
--- PASS: TestStrictBehindMovedHeadDryRunRefuses
--- PASS: TestStrictBehindUsesPinnedCompareAndRebases
--- PASS: TestStrictFreshnessIsRecheckedBeforeEveryLanding
--- PASS: TestStrictOffBehindGreenKeepsCurrentBehavior
--- PASS: TestStrictOffDoesNotCallFailingCompare
--- PASS: TestStrictUpToDateDoesNotRebase
--- PASS: TestTheHistoryListIsReadWholeOrItsErrorIsReturned
--- PASS: TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck
--- PASS: TestTrainNotesASkippedLegacyGateRecord
--- PASS: TestTrainRecordsAStoreAlertsClearingInItsOwnWords
--- PASS: TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged
pass 35 fail 4 skip 0
panic: no
failed subtests:
    --- FAIL: TestCompareDiffMismatchWords/blank_context_end
    --- FAIL: TestCompareDiffMismatchWords/cut_added_line
    --- FAIL: TestCompareDiffMismatchWords/cut_zero_index
    --- FAIL: TestCompareDiffMismatchWords/whole_diff_without_final_newline
    --- FAIL: TestCompareDiffMismatchWords/carriage_return_at_end
    --- FAIL: TestCompareDiffMismatchWords/index_word_alone
messages (the first 12 lines, each cut at 240 characters; 40-digit commit ids printed as <sha>, 12-digit ones as <id>; white space at a line's end removed; a panic's stack left out; the texts are your tests'):
    compare_proof_test.go:373: got "the diff is cut short inside a hunk: \"a.txt\""
    compare_proof_test.go:376: got "", want prefix "the diff does not end with a newline: "
    compare_proof_test.go:376: got "a file's section holds a line that is neither a header line nor in a hunk: \"x\"", want prefix "the diff does not end with a newline: "
    compare_proof_test.go:376: got "", want prefix "the diff does not end with a newline: "
    compare_proof_test.go:376: got "", want prefix "the diff does not end with a newline: "
    compare_proof_test.go:376: got "a file's section holds a line that is neither a header line nor in a hunk: \"a.txt\"", want prefix "a file with no changed line holds an index line and neither a binary part nor an empty file's ids: "
    compare_proof_test.go:418: GitHub: a rename with changes (as GitHub served it: the diff media type of compare <sha>...<sha> of omriariav/amq-squad, one section): got "the diff is cut short inside a hunk: \"internal/cli/v2310_legacy_stil
    compare_proof_test.go:533: cut proof: id="<sha>" err=<nil>
    compare_proof_test.go:600: #6 was not rebased and merged: status refused; force pushes recorded 0; head unchanged true; task bound to its old head true; audit: refusal: the update could not be proved: GitHub returned an incomplete diff
restore: gh.go cmp exit 0
restore: compare_history_test.go cmp exit 0
```

### R14

`bash ../../../../amq-squad-lead/T/trainfix/inputs-fu/rows-trainfix.sh c`

```text
=== row R14 ===
edit: internal/mergetrain/gh.go:279
  before: func compareDiffMismatch(files []FileChange, diff []byte) string {
  after:  func compareDiffMismatchYours(files []FileChange, diff []byte) string {
  appended: a compareDiffMismatch that calls yours and answers "" to its third text when every file with no changed line has a section line that starts "index "
$ go test ./internal/mergetrain/ -count=1 -timeout 240s -v -run '^(TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged|TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate|TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate|TestBehindPRWithABinaryFileIsUpdatedAndMerged|TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate|TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead|TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged|TestCompareDiffMismatchWords|TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck|TestLeadRebindOverAFileWithNoPatchIsProvedAndBound|TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged|TestIncompleteCompareRefusesWithoutRebind|TestMaxFileCompareRefusesWithoutRebind|TestHTTP422CompareRefusesWithoutRebind|TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead|TestStrictBehindUsesPinnedCompareAndRebases|TestBehindPRWithAnEmptyFileAndAnUnchangedRenameIsUpdatedAndMerged|TestASavedCompareCutInsideItsLastLineIsRefused|TestBehindPRThatChangesALastLineWithNoNewlineIsUpdatedAndMerged|TestTheCorpusOfRealSectionsGivesItsVerdicts|TestTheHistoryListIsReadWholeOrItsErrorIsReturned|TestFakeGHRefusesADiffWithEscapeBytesUnlessAllowed|TestBehindPRWithEscapeBytesInItsDiffIsRebasedAndMerged|TestAnOlderGHThatDoesNotKnowTheFlagIsReadOnceMoreWithoutIt|TestDriftSkipsALegacyGateRecordWithOneLine|TestDriftStillNamesAMalformedGateRecord|TestTrainNotesASkippedLegacyGateRecord|TestDriftClearsAStoreAlertOnlyForAPathThisRunRead|TestTrainRecordsAStoreAlertsClearingInItsOwnWords|TestChangedPatchAfterUpdateIsRefusedWithoutRebind|TestStrictOffDoesNotCallFailingCompare|TestLaterStrictCompareErrorSkipsOnlyThatPR|TestNamedDryRunBindsTheSameMovedHeadAsRealRun|TestStrictFreshnessIsRecheckedBeforeEveryLanding|TestStrictOffBehindGreenKeepsCurrentBehavior|TestStrictBehindConflictDryRunRefuses|TestStrictBehindMovedHeadDryRunRefuses|TestStrictUpToDateDoesNotRebase|TestProtectionParsesStrictPolicy)$'
exit: 1
--- FAIL: TestCompareDiffMismatchWords
--- PASS: TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate
--- PASS: TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead
--- PASS: TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate
--- PASS: TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate
--- PASS: TestASavedCompareCutInsideItsLastLineIsRefused
--- PASS: TestAnOlderGHThatDoesNotKnowTheFlagIsReadOnceMoreWithoutIt
--- PASS: TestBehindPRThatChangesALastLineWithNoNewlineIsUpdatedAndMerged
--- PASS: TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged
--- PASS: TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged
--- PASS: TestBehindPRWithABinaryFileIsUpdatedAndMerged
--- PASS: TestBehindPRWithAnEmptyFileAndAnUnchangedRenameIsUpdatedAndMerged
--- PASS: TestBehindPRWithEscapeBytesInItsDiffIsRebasedAndMerged
--- PASS: TestChangedPatchAfterUpdateIsRefusedWithoutRebind
--- PASS: TestDriftClearsAStoreAlertOnlyForAPathThisRunRead
--- PASS: TestDriftSkipsALegacyGateRecordWithOneLine
--- PASS: TestDriftStillNamesAMalformedGateRecord
--- PASS: TestFakeGHRefusesADiffWithEscapeBytesUnlessAllowed
--- PASS: TestHTTP422CompareRefusesWithoutRebind
--- PASS: TestIncompleteCompareRefusesWithoutRebind
--- PASS: TestLaterStrictCompareErrorSkipsOnlyThatPR
--- PASS: TestLeadRebindOverAFileWithNoPatchIsProvedAndBound
--- PASS: TestMaxFileCompareRefusesWithoutRebind
--- PASS: TestNamedDryRunBindsTheSameMovedHeadAsRealRun
--- PASS: TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead
--- PASS: TestProtectionParsesStrictPolicy
--- PASS: TestStrictBehindConflictDryRunRefuses
--- PASS: TestStrictBehindMovedHeadDryRunRefuses
--- PASS: TestStrictBehindUsesPinnedCompareAndRebases
--- PASS: TestStrictFreshnessIsRecheckedBeforeEveryLanding
--- PASS: TestStrictOffBehindGreenKeepsCurrentBehavior
--- PASS: TestStrictOffDoesNotCallFailingCompare
--- PASS: TestStrictUpToDateDoesNotRebase
--- PASS: TestTheCorpusOfRealSectionsGivesItsVerdicts
--- PASS: TestTheHistoryListIsReadWholeOrItsErrorIsReturned
--- PASS: TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck
--- PASS: TestTrainNotesASkippedLegacyGateRecord
--- PASS: TestTrainRecordsAStoreAlertsClearingInItsOwnWords
--- PASS: TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged
pass 38 fail 1 skip 0
panic: no
failed subtests:
    --- FAIL: TestCompareDiffMismatchWords/zero_with_hunk
messages (the first 12 lines, each cut at 240 characters; 40-digit commit ids printed as <sha>, 12-digit ones as <id>; white space at a line's end removed; a panic's stack left out; the texts are your tests'):
    compare_proof_test.go:376: got "", want prefix "a file with no changed line holds a hunk, or neither an index line nor a 100% rename's three lines: "
restore: gh.go cmp exit 0
restore: compare_history_test.go cmp exit 0
keep directory removed: yes
```

### R15

`bash ../../../../amq-squad-lead/T/trainfix/inputs-fu/rows-trainfix.sh d`

```text
=== row R15 ===
edit: internal/mergetrain/gh.go:279
  before: func compareDiffMismatch(files []FileChange, diff []byte) string {
  after:  func compareDiffMismatchYours(files []FileChange, diff []byte) string {
  appended: a compareDiffMismatch that calls yours and answers "" to its third text when every file with no changed line has a section line that is "similarity index 100%"
$ go test ./internal/mergetrain/ -count=1 -timeout 240s -v -run '^(TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged|TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate|TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate|TestBehindPRWithABinaryFileIsUpdatedAndMerged|TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate|TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead|TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged|TestCompareDiffMismatchWords|TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck|TestLeadRebindOverAFileWithNoPatchIsProvedAndBound|TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged|TestIncompleteCompareRefusesWithoutRebind|TestMaxFileCompareRefusesWithoutRebind|TestHTTP422CompareRefusesWithoutRebind|TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead|TestStrictBehindUsesPinnedCompareAndRebases|TestBehindPRWithAnEmptyFileAndAnUnchangedRenameIsUpdatedAndMerged|TestASavedCompareCutInsideItsLastLineIsRefused|TestBehindPRThatChangesALastLineWithNoNewlineIsUpdatedAndMerged|TestTheCorpusOfRealSectionsGivesItsVerdicts|TestTheHistoryListIsReadWholeOrItsErrorIsReturned|TestFakeGHRefusesADiffWithEscapeBytesUnlessAllowed|TestBehindPRWithEscapeBytesInItsDiffIsRebasedAndMerged|TestAnOlderGHThatDoesNotKnowTheFlagIsReadOnceMoreWithoutIt|TestDriftSkipsALegacyGateRecordWithOneLine|TestDriftStillNamesAMalformedGateRecord|TestTrainNotesASkippedLegacyGateRecord|TestDriftClearsAStoreAlertOnlyForAPathThisRunRead|TestTrainRecordsAStoreAlertsClearingInItsOwnWords|TestChangedPatchAfterUpdateIsRefusedWithoutRebind|TestStrictOffDoesNotCallFailingCompare|TestLaterStrictCompareErrorSkipsOnlyThatPR|TestNamedDryRunBindsTheSameMovedHeadAsRealRun|TestStrictFreshnessIsRecheckedBeforeEveryLanding|TestStrictOffBehindGreenKeepsCurrentBehavior|TestStrictBehindConflictDryRunRefuses|TestStrictBehindMovedHeadDryRunRefuses|TestStrictUpToDateDoesNotRebase|TestProtectionParsesStrictPolicy)$'
exit: 1
--- FAIL: TestCompareDiffMismatchWords
--- PASS: TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate
--- PASS: TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead
--- PASS: TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate
--- PASS: TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate
--- PASS: TestASavedCompareCutInsideItsLastLineIsRefused
--- PASS: TestAnOlderGHThatDoesNotKnowTheFlagIsReadOnceMoreWithoutIt
--- PASS: TestBehindPRThatChangesALastLineWithNoNewlineIsUpdatedAndMerged
--- PASS: TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged
--- PASS: TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged
--- PASS: TestBehindPRWithABinaryFileIsUpdatedAndMerged
--- PASS: TestBehindPRWithAnEmptyFileAndAnUnchangedRenameIsUpdatedAndMerged
--- PASS: TestBehindPRWithEscapeBytesInItsDiffIsRebasedAndMerged
--- PASS: TestChangedPatchAfterUpdateIsRefusedWithoutRebind
--- PASS: TestDriftClearsAStoreAlertOnlyForAPathThisRunRead
--- PASS: TestDriftSkipsALegacyGateRecordWithOneLine
--- PASS: TestDriftStillNamesAMalformedGateRecord
--- PASS: TestFakeGHRefusesADiffWithEscapeBytesUnlessAllowed
--- PASS: TestHTTP422CompareRefusesWithoutRebind
--- PASS: TestIncompleteCompareRefusesWithoutRebind
--- PASS: TestLaterStrictCompareErrorSkipsOnlyThatPR
--- PASS: TestLeadRebindOverAFileWithNoPatchIsProvedAndBound
--- PASS: TestMaxFileCompareRefusesWithoutRebind
--- PASS: TestNamedDryRunBindsTheSameMovedHeadAsRealRun
--- PASS: TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead
--- PASS: TestProtectionParsesStrictPolicy
--- PASS: TestStrictBehindConflictDryRunRefuses
--- PASS: TestStrictBehindMovedHeadDryRunRefuses
--- PASS: TestStrictBehindUsesPinnedCompareAndRebases
--- PASS: TestStrictFreshnessIsRecheckedBeforeEveryLanding
--- PASS: TestStrictOffBehindGreenKeepsCurrentBehavior
--- PASS: TestStrictOffDoesNotCallFailingCompare
--- PASS: TestStrictUpToDateDoesNotRebase
--- PASS: TestTheCorpusOfRealSectionsGivesItsVerdicts
--- PASS: TestTheHistoryListIsReadWholeOrItsErrorIsReturned
--- PASS: TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck
--- PASS: TestTrainNotesASkippedLegacyGateRecord
--- PASS: TestTrainRecordsAStoreAlertsClearingInItsOwnWords
--- PASS: TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged
pass 38 fail 1 skip 0
panic: no
failed subtests:
    --- FAIL: TestCompareDiffMismatchWords/rename_lacks_to
    --- FAIL: TestCompareDiffMismatchWords/rename_wrong_from
messages (the first 12 lines, each cut at 240 characters; 40-digit commit ids printed as <sha>, 12-digit ones as <id>; white space at a line's end removed; a panic's stack left out; the texts are your tests'):
    compare_proof_test.go:376: got "", want prefix "a file with no changed line holds a hunk, or neither an index line nor a 100% rename's three lines: "
    compare_proof_test.go:376: got "", want prefix "a file with no changed line holds a hunk, or neither an index line nor a 100% rename's three lines: "
restore: gh.go cmp exit 0
restore: compare_history_test.go cmp exit 0
```

### R16

`bash ../../../../amq-squad-lead/T/trainfix/inputs-fu/rows-trainfix.sh d`

```text
=== row R16 ===
edit: internal/mergetrain/gh.go:279
  before: func compareDiffMismatch(files []FileChange, diff []byte) string {
  after:  func compareDiffMismatchYours(files []FileChange, diff []byte) string {
  appended: a compareDiffMismatch that calls yours and answers "" when its answer starts with:
    "the diff does not end with a newline: "
$ go test ./internal/mergetrain/ -count=1 -timeout 240s -v -run '^(TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged|TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate|TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate|TestBehindPRWithABinaryFileIsUpdatedAndMerged|TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate|TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead|TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged|TestCompareDiffMismatchWords|TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck|TestLeadRebindOverAFileWithNoPatchIsProvedAndBound|TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged|TestIncompleteCompareRefusesWithoutRebind|TestMaxFileCompareRefusesWithoutRebind|TestHTTP422CompareRefusesWithoutRebind|TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead|TestStrictBehindUsesPinnedCompareAndRebases|TestBehindPRWithAnEmptyFileAndAnUnchangedRenameIsUpdatedAndMerged|TestASavedCompareCutInsideItsLastLineIsRefused|TestBehindPRThatChangesALastLineWithNoNewlineIsUpdatedAndMerged|TestTheCorpusOfRealSectionsGivesItsVerdicts|TestTheHistoryListIsReadWholeOrItsErrorIsReturned|TestFakeGHRefusesADiffWithEscapeBytesUnlessAllowed|TestBehindPRWithEscapeBytesInItsDiffIsRebasedAndMerged|TestAnOlderGHThatDoesNotKnowTheFlagIsReadOnceMoreWithoutIt|TestDriftSkipsALegacyGateRecordWithOneLine|TestDriftStillNamesAMalformedGateRecord|TestTrainNotesASkippedLegacyGateRecord|TestDriftClearsAStoreAlertOnlyForAPathThisRunRead|TestTrainRecordsAStoreAlertsClearingInItsOwnWords|TestChangedPatchAfterUpdateIsRefusedWithoutRebind|TestStrictOffDoesNotCallFailingCompare|TestLaterStrictCompareErrorSkipsOnlyThatPR|TestNamedDryRunBindsTheSameMovedHeadAsRealRun|TestStrictFreshnessIsRecheckedBeforeEveryLanding|TestStrictOffBehindGreenKeepsCurrentBehavior|TestStrictBehindConflictDryRunRefuses|TestStrictBehindMovedHeadDryRunRefuses|TestStrictUpToDateDoesNotRebase|TestProtectionParsesStrictPolicy)$'
exit: 1
--- FAIL: TestASavedCompareCutInsideItsLastLineIsRefused
--- FAIL: TestCompareDiffMismatchWords
--- PASS: TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate
--- PASS: TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead
--- PASS: TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate
--- PASS: TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate
--- PASS: TestAnOlderGHThatDoesNotKnowTheFlagIsReadOnceMoreWithoutIt
--- PASS: TestBehindPRThatChangesALastLineWithNoNewlineIsUpdatedAndMerged
--- PASS: TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged
--- PASS: TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged
--- PASS: TestBehindPRWithABinaryFileIsUpdatedAndMerged
--- PASS: TestBehindPRWithAnEmptyFileAndAnUnchangedRenameIsUpdatedAndMerged
--- PASS: TestBehindPRWithEscapeBytesInItsDiffIsRebasedAndMerged
--- PASS: TestChangedPatchAfterUpdateIsRefusedWithoutRebind
--- PASS: TestDriftClearsAStoreAlertOnlyForAPathThisRunRead
--- PASS: TestDriftSkipsALegacyGateRecordWithOneLine
--- PASS: TestDriftStillNamesAMalformedGateRecord
--- PASS: TestFakeGHRefusesADiffWithEscapeBytesUnlessAllowed
--- PASS: TestHTTP422CompareRefusesWithoutRebind
--- PASS: TestIncompleteCompareRefusesWithoutRebind
--- PASS: TestLaterStrictCompareErrorSkipsOnlyThatPR
--- PASS: TestLeadRebindOverAFileWithNoPatchIsProvedAndBound
--- PASS: TestMaxFileCompareRefusesWithoutRebind
--- PASS: TestNamedDryRunBindsTheSameMovedHeadAsRealRun
--- PASS: TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead
--- PASS: TestProtectionParsesStrictPolicy
--- PASS: TestStrictBehindConflictDryRunRefuses
--- PASS: TestStrictBehindMovedHeadDryRunRefuses
--- PASS: TestStrictBehindUsesPinnedCompareAndRebases
--- PASS: TestStrictFreshnessIsRecheckedBeforeEveryLanding
--- PASS: TestStrictOffBehindGreenKeepsCurrentBehavior
--- PASS: TestStrictOffDoesNotCallFailingCompare
--- PASS: TestStrictUpToDateDoesNotRebase
--- PASS: TestTheCorpusOfRealSectionsGivesItsVerdicts
--- PASS: TestTheHistoryListIsReadWholeOrItsErrorIsReturned
--- PASS: TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck
--- PASS: TestTrainNotesASkippedLegacyGateRecord
--- PASS: TestTrainRecordsAStoreAlertsClearingInItsOwnWords
--- PASS: TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged
pass 37 fail 2 skip 0
panic: no
failed subtests:
    --- FAIL: TestCompareDiffMismatchWords/cut_added_line
    --- FAIL: TestCompareDiffMismatchWords/cut_zero_index
    --- FAIL: TestCompareDiffMismatchWords/whole_diff_without_final_newline
    --- FAIL: TestCompareDiffMismatchWords/carriage_return_at_end
messages (the first 12 lines, each cut at 240 characters; 40-digit commit ids printed as <sha>, 12-digit ones as <id>; white space at a line's end removed; a panic's stack left out; the texts are your tests'):
    compare_proof_test.go:376: got "", want prefix "the diff does not end with a newline: "
    compare_proof_test.go:376: got "", want prefix "the diff does not end with a newline: "
    compare_proof_test.go:376: got "", want prefix "the diff does not end with a newline: "
    compare_proof_test.go:376: got "", want prefix "the diff does not end with a newline: "
    compare_proof_test.go:533: cut proof: id="<sha>" err=<nil>
restore: gh.go cmp exit 0
restore: compare_history_test.go cmp exit 0
```

### R17

`bash ../../../../amq-squad-lead/T/trainfix/inputs-fu/rows-trainfix.sh d`

```text
=== row R17 ===
edit: internal/mergetrain/gh.go:279
  before: func compareDiffMismatch(files []FileChange, diff []byte) string {
  after:  func compareDiffMismatchYours(files []FileChange, diff []byte) string {
  appended: a compareDiffMismatch that calls yours and answers "" when its answer starts with:
    "a file's section holds a line that is neither a header line nor in a hunk: "
$ go test ./internal/mergetrain/ -count=1 -timeout 240s -v -run '^(TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged|TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate|TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate|TestBehindPRWithABinaryFileIsUpdatedAndMerged|TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate|TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead|TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged|TestCompareDiffMismatchWords|TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck|TestLeadRebindOverAFileWithNoPatchIsProvedAndBound|TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged|TestIncompleteCompareRefusesWithoutRebind|TestMaxFileCompareRefusesWithoutRebind|TestHTTP422CompareRefusesWithoutRebind|TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead|TestStrictBehindUsesPinnedCompareAndRebases|TestBehindPRWithAnEmptyFileAndAnUnchangedRenameIsUpdatedAndMerged|TestASavedCompareCutInsideItsLastLineIsRefused|TestBehindPRThatChangesALastLineWithNoNewlineIsUpdatedAndMerged|TestTheCorpusOfRealSectionsGivesItsVerdicts|TestTheHistoryListIsReadWholeOrItsErrorIsReturned|TestFakeGHRefusesADiffWithEscapeBytesUnlessAllowed|TestBehindPRWithEscapeBytesInItsDiffIsRebasedAndMerged|TestAnOlderGHThatDoesNotKnowTheFlagIsReadOnceMoreWithoutIt|TestDriftSkipsALegacyGateRecordWithOneLine|TestDriftStillNamesAMalformedGateRecord|TestTrainNotesASkippedLegacyGateRecord|TestDriftClearsAStoreAlertOnlyForAPathThisRunRead|TestTrainRecordsAStoreAlertsClearingInItsOwnWords|TestChangedPatchAfterUpdateIsRefusedWithoutRebind|TestStrictOffDoesNotCallFailingCompare|TestLaterStrictCompareErrorSkipsOnlyThatPR|TestNamedDryRunBindsTheSameMovedHeadAsRealRun|TestStrictFreshnessIsRecheckedBeforeEveryLanding|TestStrictOffBehindGreenKeepsCurrentBehavior|TestStrictBehindConflictDryRunRefuses|TestStrictBehindMovedHeadDryRunRefuses|TestStrictUpToDateDoesNotRebase|TestProtectionParsesStrictPolicy)$'
exit: 1
--- FAIL: TestCompareDiffMismatchWords
--- PASS: TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate
--- PASS: TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead
--- PASS: TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate
--- PASS: TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate
--- PASS: TestASavedCompareCutInsideItsLastLineIsRefused
--- PASS: TestAnOlderGHThatDoesNotKnowTheFlagIsReadOnceMoreWithoutIt
--- PASS: TestBehindPRThatChangesALastLineWithNoNewlineIsUpdatedAndMerged
--- PASS: TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged
--- PASS: TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged
--- PASS: TestBehindPRWithABinaryFileIsUpdatedAndMerged
--- PASS: TestBehindPRWithAnEmptyFileAndAnUnchangedRenameIsUpdatedAndMerged
--- PASS: TestBehindPRWithEscapeBytesInItsDiffIsRebasedAndMerged
--- PASS: TestChangedPatchAfterUpdateIsRefusedWithoutRebind
--- PASS: TestDriftClearsAStoreAlertOnlyForAPathThisRunRead
--- PASS: TestDriftSkipsALegacyGateRecordWithOneLine
--- PASS: TestDriftStillNamesAMalformedGateRecord
--- PASS: TestFakeGHRefusesADiffWithEscapeBytesUnlessAllowed
--- PASS: TestHTTP422CompareRefusesWithoutRebind
--- PASS: TestIncompleteCompareRefusesWithoutRebind
--- PASS: TestLaterStrictCompareErrorSkipsOnlyThatPR
--- PASS: TestLeadRebindOverAFileWithNoPatchIsProvedAndBound
--- PASS: TestMaxFileCompareRefusesWithoutRebind
--- PASS: TestNamedDryRunBindsTheSameMovedHeadAsRealRun
--- PASS: TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead
--- PASS: TestProtectionParsesStrictPolicy
--- PASS: TestStrictBehindConflictDryRunRefuses
--- PASS: TestStrictBehindMovedHeadDryRunRefuses
--- PASS: TestStrictBehindUsesPinnedCompareAndRebases
--- PASS: TestStrictFreshnessIsRecheckedBeforeEveryLanding
--- PASS: TestStrictOffBehindGreenKeepsCurrentBehavior
--- PASS: TestStrictOffDoesNotCallFailingCompare
--- PASS: TestStrictUpToDateDoesNotRebase
--- PASS: TestTheCorpusOfRealSectionsGivesItsVerdicts
--- PASS: TestTheHistoryListIsReadWholeOrItsErrorIsReturned
--- PASS: TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck
--- PASS: TestTrainNotesASkippedLegacyGateRecord
--- PASS: TestTrainRecordsAStoreAlertsClearingInItsOwnWords
--- PASS: TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged
pass 38 fail 1 skip 0
panic: no
failed subtests:
    --- FAIL: TestCompareDiffMismatchWords/addition_outside_hunk
    --- FAIL: TestCompareDiffMismatchWords/addition_after_hunk
    --- FAIL: TestCompareDiffMismatchWords/context_after_hunk
    --- FAIL: TestCompareDiffMismatchWords/plain_text_after_hunk
    --- FAIL: TestCompareDiffMismatchWords/old_header_after_hunk
    --- FAIL: TestCompareDiffMismatchWords/new_header_after_hunk
    --- FAIL: TestCompareDiffMismatchWords/addition_before_hunk
    --- FAIL: TestCompareDiffMismatchWords/line_after_binary_GitHub
    --- FAIL: TestCompareDiffMismatchWords/non-data_binary_line
    --- FAIL: TestCompareDiffMismatchWords/wrong-length_binary_data
    --- FAIL: TestCompareDiffMismatchWords/marker_without_hunk
    --- FAIL: TestCompareDiffMismatchWords/second_marker
    --- FAIL: TestCompareDiffMismatchWords/third_marker_after_two_inside
    --- FAIL: TestCompareDiffMismatchWords/three_markers_inside
messages (the first 12 lines, each cut at 240 characters; 40-digit commit ids printed as <sha>, 12-digit ones as <id>; white space at a line's end removed; a panic's stack left out; the texts are your tests'):
    compare_proof_test.go:376: got "", want prefix "a file's section holds a line that is neither a header line nor in a hunk: "
    compare_proof_test.go:376: got "", want prefix "a file's section holds a line that is neither a header line nor in a hunk: "
    compare_proof_test.go:376: got "", want prefix "a file's section holds a line that is neither a header line nor in a hunk: "
    compare_proof_test.go:376: got "", want prefix "a file's section holds a line that is neither a header line nor in a hunk: "
    compare_proof_test.go:376: got "", want prefix "a file's section holds a line that is neither a header line nor in a hunk: "
    compare_proof_test.go:376: got "", want prefix "a file's section holds a line that is neither a header line nor in a hunk: "
    compare_proof_test.go:376: got "", want prefix "a file's section holds a line that is neither a header line nor in a hunk: "
    compare_proof_test.go:376: got "", want prefix "a file's section holds a line that is neither a header line nor in a hunk: "
    compare_proof_test.go:376: got "", want prefix "a file's section holds a line that is neither a header line nor in a hunk: "
    compare_proof_test.go:376: got "", want prefix "a file's section holds a line that is neither a header line nor in a hunk: "
    compare_proof_test.go:376: got "", want prefix "a file's section holds a line that is neither a header line nor in a hunk: "
    compare_proof_test.go:376: got "", want prefix "a file's section holds a line that is neither a header line nor in a hunk: "
restore: gh.go cmp exit 0
restore: compare_history_test.go cmp exit 0
```

### R18

`bash ../../../../amq-squad-lead/T/trainfix/inputs-fu/rows-trainfix.sh d`

```text
=== row R18 ===
edit: internal/mergetrain/gh.go:201
  before: const compareProofFilesJQ = `.files[]? | {path: .filename, previous_path: (.previous_filename // ""), additions, deletions, status}`
  after:  const compareProofFilesJQ = `.files[]? | {path: .filename, previous_path: (.previous_filename // ""), status, additions, deletions}`
$ go test ./internal/mergetrain/ -count=1 -timeout 240s -v -run '^(TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged|TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate|TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate|TestBehindPRWithABinaryFileIsUpdatedAndMerged|TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate|TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead|TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged|TestCompareDiffMismatchWords|TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck|TestLeadRebindOverAFileWithNoPatchIsProvedAndBound|TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged|TestIncompleteCompareRefusesWithoutRebind|TestMaxFileCompareRefusesWithoutRebind|TestHTTP422CompareRefusesWithoutRebind|TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead|TestStrictBehindUsesPinnedCompareAndRebases|TestBehindPRWithAnEmptyFileAndAnUnchangedRenameIsUpdatedAndMerged|TestASavedCompareCutInsideItsLastLineIsRefused|TestBehindPRThatChangesALastLineWithNoNewlineIsUpdatedAndMerged|TestTheCorpusOfRealSectionsGivesItsVerdicts|TestTheHistoryListIsReadWholeOrItsErrorIsReturned|TestFakeGHRefusesADiffWithEscapeBytesUnlessAllowed|TestBehindPRWithEscapeBytesInItsDiffIsRebasedAndMerged|TestAnOlderGHThatDoesNotKnowTheFlagIsReadOnceMoreWithoutIt|TestDriftSkipsALegacyGateRecordWithOneLine|TestDriftStillNamesAMalformedGateRecord|TestTrainNotesASkippedLegacyGateRecord|TestDriftClearsAStoreAlertOnlyForAPathThisRunRead|TestTrainRecordsAStoreAlertsClearingInItsOwnWords|TestChangedPatchAfterUpdateIsRefusedWithoutRebind|TestStrictOffDoesNotCallFailingCompare|TestLaterStrictCompareErrorSkipsOnlyThatPR|TestNamedDryRunBindsTheSameMovedHeadAsRealRun|TestStrictFreshnessIsRecheckedBeforeEveryLanding|TestStrictOffBehindGreenKeepsCurrentBehavior|TestStrictBehindConflictDryRunRefuses|TestStrictBehindMovedHeadDryRunRefuses|TestStrictUpToDateDoesNotRebase|TestProtectionParsesStrictPolicy)$'
exit: 1
--- FAIL: TestAnOlderGHThatDoesNotKnowTheFlagIsReadOnceMoreWithoutIt
--- FAIL: TestBehindPRWithEscapeBytesInItsDiffIsRebasedAndMerged
--- PASS: TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate
--- PASS: TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead
--- PASS: TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate
--- PASS: TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate
--- PASS: TestASavedCompareCutInsideItsLastLineIsRefused
--- PASS: TestBehindPRThatChangesALastLineWithNoNewlineIsUpdatedAndMerged
--- PASS: TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged
--- PASS: TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged
--- PASS: TestBehindPRWithABinaryFileIsUpdatedAndMerged
--- PASS: TestBehindPRWithAnEmptyFileAndAnUnchangedRenameIsUpdatedAndMerged
--- PASS: TestChangedPatchAfterUpdateIsRefusedWithoutRebind
--- PASS: TestCompareDiffMismatchWords
--- PASS: TestDriftClearsAStoreAlertOnlyForAPathThisRunRead
--- PASS: TestDriftSkipsALegacyGateRecordWithOneLine
--- PASS: TestDriftStillNamesAMalformedGateRecord
--- PASS: TestFakeGHRefusesADiffWithEscapeBytesUnlessAllowed
--- PASS: TestHTTP422CompareRefusesWithoutRebind
--- PASS: TestIncompleteCompareRefusesWithoutRebind
--- PASS: TestLaterStrictCompareErrorSkipsOnlyThatPR
--- PASS: TestLeadRebindOverAFileWithNoPatchIsProvedAndBound
--- PASS: TestMaxFileCompareRefusesWithoutRebind
--- PASS: TestNamedDryRunBindsTheSameMovedHeadAsRealRun
--- PASS: TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead
--- PASS: TestProtectionParsesStrictPolicy
--- PASS: TestStrictBehindConflictDryRunRefuses
--- PASS: TestStrictBehindMovedHeadDryRunRefuses
--- PASS: TestStrictBehindUsesPinnedCompareAndRebases
--- PASS: TestStrictFreshnessIsRecheckedBeforeEveryLanding
--- PASS: TestStrictOffBehindGreenKeepsCurrentBehavior
--- PASS: TestStrictOffDoesNotCallFailingCompare
--- PASS: TestStrictUpToDateDoesNotRebase
--- PASS: TestTheCorpusOfRealSectionsGivesItsVerdicts
--- PASS: TestTheHistoryListIsReadWholeOrItsErrorIsReturned
--- PASS: TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck
--- PASS: TestTrainNotesASkippedLegacyGateRecord
--- PASS: TestTrainRecordsAStoreAlertsClearingInItsOwnWords
--- PASS: TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged
pass 37 fail 2 skip 0
panic: no
failed subtests:
messages (the first 12 lines, each cut at 240 characters; 40-digit commit ids printed as <sha>, 12-digit ones as <id>; white space at a line's end removed; a panic's stack left out; the texts are your tests'):
    escape_legacy_320_test.go:146: one diff read per proof: 4 proofs, reads ["api repos/owner/fixture/compare/<sha>...<sha> -H Accept: application/vnd.github.diff --allow-escape-sequences" "api repos/owner/fixture/compare/<sha>...<sha> -H A
    escape_legacy_320_test.go:173: two diff reads per proof: 4 proofs, reads ["api repos/owner/fixture/compare/<sha>...<sha> -H Accept: application/vnd.github.diff --allow-escape-sequences" "api repos/owner/fixture/compare/<sha>...<sha> -H
restore: gh.go cmp exit 0
restore: compare_history_test.go cmp exit 0
```

### R19

`bash ../../../../amq-squad-lead/T/trainfix/inputs-fu/rows-trainfix.sh d`

```text
=== row R19 ===
edit: internal/mergetrain/gh.go:279
  before: func compareDiffMismatch(files []FileChange, diff []byte) string {
  after:  func compareDiffMismatchYours(files []FileChange, diff []byte) string {
  appended: a compareDiffMismatch that calls yours with the diff from its first "diff --git" line on
$ go test ./internal/mergetrain/ -count=1 -timeout 240s -v -run '^(TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged|TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate|TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate|TestBehindPRWithABinaryFileIsUpdatedAndMerged|TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate|TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead|TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged|TestCompareDiffMismatchWords|TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck|TestLeadRebindOverAFileWithNoPatchIsProvedAndBound|TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged|TestIncompleteCompareRefusesWithoutRebind|TestMaxFileCompareRefusesWithoutRebind|TestHTTP422CompareRefusesWithoutRebind|TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead|TestStrictBehindUsesPinnedCompareAndRebases|TestBehindPRWithAnEmptyFileAndAnUnchangedRenameIsUpdatedAndMerged|TestASavedCompareCutInsideItsLastLineIsRefused|TestBehindPRThatChangesALastLineWithNoNewlineIsUpdatedAndMerged|TestTheCorpusOfRealSectionsGivesItsVerdicts|TestTheHistoryListIsReadWholeOrItsErrorIsReturned|TestFakeGHRefusesADiffWithEscapeBytesUnlessAllowed|TestBehindPRWithEscapeBytesInItsDiffIsRebasedAndMerged|TestAnOlderGHThatDoesNotKnowTheFlagIsReadOnceMoreWithoutIt|TestDriftSkipsALegacyGateRecordWithOneLine|TestDriftStillNamesAMalformedGateRecord|TestTrainNotesASkippedLegacyGateRecord|TestDriftClearsAStoreAlertOnlyForAPathThisRunRead|TestTrainRecordsAStoreAlertsClearingInItsOwnWords|TestChangedPatchAfterUpdateIsRefusedWithoutRebind|TestStrictOffDoesNotCallFailingCompare|TestLaterStrictCompareErrorSkipsOnlyThatPR|TestNamedDryRunBindsTheSameMovedHeadAsRealRun|TestStrictFreshnessIsRecheckedBeforeEveryLanding|TestStrictOffBehindGreenKeepsCurrentBehavior|TestStrictBehindConflictDryRunRefuses|TestStrictBehindMovedHeadDryRunRefuses|TestStrictUpToDateDoesNotRebase|TestProtectionParsesStrictPolicy)$'
exit: 1
--- FAIL: TestCompareDiffMismatchWords
--- PASS: TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate
--- PASS: TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead
--- PASS: TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate
--- PASS: TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate
--- PASS: TestASavedCompareCutInsideItsLastLineIsRefused
--- PASS: TestAnOlderGHThatDoesNotKnowTheFlagIsReadOnceMoreWithoutIt
--- PASS: TestBehindPRThatChangesALastLineWithNoNewlineIsUpdatedAndMerged
--- PASS: TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged
--- PASS: TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged
--- PASS: TestBehindPRWithABinaryFileIsUpdatedAndMerged
--- PASS: TestBehindPRWithAnEmptyFileAndAnUnchangedRenameIsUpdatedAndMerged
--- PASS: TestBehindPRWithEscapeBytesInItsDiffIsRebasedAndMerged
--- PASS: TestChangedPatchAfterUpdateIsRefusedWithoutRebind
--- PASS: TestDriftClearsAStoreAlertOnlyForAPathThisRunRead
--- PASS: TestDriftSkipsALegacyGateRecordWithOneLine
--- PASS: TestDriftStillNamesAMalformedGateRecord
--- PASS: TestFakeGHRefusesADiffWithEscapeBytesUnlessAllowed
--- PASS: TestHTTP422CompareRefusesWithoutRebind
--- PASS: TestIncompleteCompareRefusesWithoutRebind
--- PASS: TestLaterStrictCompareErrorSkipsOnlyThatPR
--- PASS: TestLeadRebindOverAFileWithNoPatchIsProvedAndBound
--- PASS: TestMaxFileCompareRefusesWithoutRebind
--- PASS: TestNamedDryRunBindsTheSameMovedHeadAsRealRun
--- PASS: TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead
--- PASS: TestProtectionParsesStrictPolicy
--- PASS: TestStrictBehindConflictDryRunRefuses
--- PASS: TestStrictBehindMovedHeadDryRunRefuses
--- PASS: TestStrictBehindUsesPinnedCompareAndRebases
--- PASS: TestStrictFreshnessIsRecheckedBeforeEveryLanding
--- PASS: TestStrictOffBehindGreenKeepsCurrentBehavior
--- PASS: TestStrictOffDoesNotCallFailingCompare
--- PASS: TestStrictUpToDateDoesNotRebase
--- PASS: TestTheCorpusOfRealSectionsGivesItsVerdicts
--- PASS: TestTheHistoryListIsReadWholeOrItsErrorIsReturned
--- PASS: TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck
--- PASS: TestTrainNotesASkippedLegacyGateRecord
--- PASS: TestTrainRecordsAStoreAlertsClearingInItsOwnWords
--- PASS: TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged
pass 38 fail 1 skip 0
panic: no
failed subtests:
    --- FAIL: TestCompareDiffMismatchWords/marker_before_one_section
    --- FAIL: TestCompareDiffMismatchWords/marker_after_empty_line_before_one_section
    --- FAIL: TestCompareDiffMismatchWords/text_before_first_file
messages (the first 12 lines, each cut at 240 characters; 40-digit commit ids printed as <sha>, 12-digit ones as <id>; white space at a line's end removed; a panic's stack left out; the texts are your tests'):
    compare_proof_test.go:376: got "", want prefix "the diff's files are not the compare's files: "
    compare_proof_test.go:376: got "", want prefix "the diff's files are not the compare's files: "
    compare_proof_test.go:376: got "", want prefix "the diff's files are not the compare's files: "
restore: gh.go cmp exit 0
restore: compare_history_test.go cmp exit 0
keep directory removed: yes
```

### R20

`bash ../../../../amq-squad-lead/T/trainfix/inputs-fu/rows-trainfix.sh e`

```text
=== row R20 ===
edit: internal/mergetrain/gh.go:279
  before: func compareDiffMismatch(files []FileChange, diff []byte) string {
  after:  func compareDiffMismatchYours(files []FileChange, diff []byte) string {
  appended: a compareDiffMismatch that answers the fourth text for a marker line inside a hunk, then calls yours
$ go test ./internal/mergetrain/ -count=1 -timeout 240s -v -run '^(TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged|TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate|TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate|TestBehindPRWithABinaryFileIsUpdatedAndMerged|TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate|TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead|TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged|TestCompareDiffMismatchWords|TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck|TestLeadRebindOverAFileWithNoPatchIsProvedAndBound|TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged|TestIncompleteCompareRefusesWithoutRebind|TestMaxFileCompareRefusesWithoutRebind|TestHTTP422CompareRefusesWithoutRebind|TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead|TestStrictBehindUsesPinnedCompareAndRebases|TestBehindPRWithAnEmptyFileAndAnUnchangedRenameIsUpdatedAndMerged|TestASavedCompareCutInsideItsLastLineIsRefused|TestBehindPRThatChangesALastLineWithNoNewlineIsUpdatedAndMerged|TestTheCorpusOfRealSectionsGivesItsVerdicts|TestTheHistoryListIsReadWholeOrItsErrorIsReturned|TestFakeGHRefusesADiffWithEscapeBytesUnlessAllowed|TestBehindPRWithEscapeBytesInItsDiffIsRebasedAndMerged|TestAnOlderGHThatDoesNotKnowTheFlagIsReadOnceMoreWithoutIt|TestDriftSkipsALegacyGateRecordWithOneLine|TestDriftStillNamesAMalformedGateRecord|TestTrainNotesASkippedLegacyGateRecord|TestDriftClearsAStoreAlertOnlyForAPathThisRunRead|TestTrainRecordsAStoreAlertsClearingInItsOwnWords|TestChangedPatchAfterUpdateIsRefusedWithoutRebind|TestStrictOffDoesNotCallFailingCompare|TestLaterStrictCompareErrorSkipsOnlyThatPR|TestNamedDryRunBindsTheSameMovedHeadAsRealRun|TestStrictFreshnessIsRecheckedBeforeEveryLanding|TestStrictOffBehindGreenKeepsCurrentBehavior|TestStrictBehindConflictDryRunRefuses|TestStrictBehindMovedHeadDryRunRefuses|TestStrictUpToDateDoesNotRebase|TestProtectionParsesStrictPolicy)$'
exit: 1
--- FAIL: TestBehindPRThatChangesALastLineWithNoNewlineIsUpdatedAndMerged
--- FAIL: TestCompareDiffMismatchWords
--- FAIL: TestTheCorpusOfRealSectionsGivesItsVerdicts
--- PASS: TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate
--- PASS: TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead
--- PASS: TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate
--- PASS: TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate
--- PASS: TestASavedCompareCutInsideItsLastLineIsRefused
--- PASS: TestAnOlderGHThatDoesNotKnowTheFlagIsReadOnceMoreWithoutIt
--- PASS: TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged
--- PASS: TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged
--- PASS: TestBehindPRWithABinaryFileIsUpdatedAndMerged
--- PASS: TestBehindPRWithAnEmptyFileAndAnUnchangedRenameIsUpdatedAndMerged
--- PASS: TestBehindPRWithEscapeBytesInItsDiffIsRebasedAndMerged
--- PASS: TestChangedPatchAfterUpdateIsRefusedWithoutRebind
--- PASS: TestDriftClearsAStoreAlertOnlyForAPathThisRunRead
--- PASS: TestDriftSkipsALegacyGateRecordWithOneLine
--- PASS: TestDriftStillNamesAMalformedGateRecord
--- PASS: TestFakeGHRefusesADiffWithEscapeBytesUnlessAllowed
--- PASS: TestHTTP422CompareRefusesWithoutRebind
--- PASS: TestIncompleteCompareRefusesWithoutRebind
--- PASS: TestLaterStrictCompareErrorSkipsOnlyThatPR
--- PASS: TestLeadRebindOverAFileWithNoPatchIsProvedAndBound
--- PASS: TestMaxFileCompareRefusesWithoutRebind
--- PASS: TestNamedDryRunBindsTheSameMovedHeadAsRealRun
--- PASS: TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead
--- PASS: TestProtectionParsesStrictPolicy
--- PASS: TestStrictBehindConflictDryRunRefuses
--- PASS: TestStrictBehindMovedHeadDryRunRefuses
--- PASS: TestStrictBehindUsesPinnedCompareAndRebases
--- PASS: TestStrictFreshnessIsRecheckedBeforeEveryLanding
--- PASS: TestStrictOffBehindGreenKeepsCurrentBehavior
--- PASS: TestStrictOffDoesNotCallFailingCompare
--- PASS: TestStrictUpToDateDoesNotRebase
--- PASS: TestTheHistoryListIsReadWholeOrItsErrorIsReturned
--- PASS: TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck
--- PASS: TestTrainNotesASkippedLegacyGateRecord
--- PASS: TestTrainRecordsAStoreAlertsClearingInItsOwnWords
--- PASS: TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged
pass 36 fail 3 skip 0
panic: no
failed subtests:
    --- FAIL: TestCompareDiffMismatchWords/marker_inside_after_removed_line
    --- FAIL: TestCompareDiffMismatchWords/marker_inside_and_after_hunk
    --- FAIL: TestCompareDiffMismatchWords/marker_after_context_line
    --- FAIL: TestCompareDiffMismatchWords/marker_after_added_line
    --- FAIL: TestCompareDiffMismatchWords/third_marker_after_two_inside
    --- FAIL: TestCompareDiffMismatchWords/three_markers_inside
    --- FAIL: TestCompareDiffMismatchWords/two_hunks_with_two_markers_each
messages (the first 12 lines, each cut at 240 characters; 40-digit commit ids printed as <sha>, 12-digit ones as <id>; white space at a line's end removed; a panic's stack left out; the texts are your tests'):
    compare_proof_test.go:116: #6 was not rebased and merged: status refused; force pushes recorded 0; head unchanged true; task bound to its old head true; audit: refusal: the update could not be proved: GitHub returned an incomplete diff
    compare_proof_test.go:373: got "the diff is cut short inside a hunk: rows-trainfix.sh R20, a marker line inside a hunk"
    compare_proof_test.go:373: got "the diff is cut short inside a hunk: rows-trainfix.sh R20, a marker line inside a hunk"
    compare_proof_test.go:373: got "the diff is cut short inside a hunk: rows-trainfix.sh R20, a marker line inside a hunk"
    compare_proof_test.go:373: got "the diff is cut short inside a hunk: rows-trainfix.sh R20, a marker line inside a hunk"
    compare_proof_test.go:376: got "the diff is cut short inside a hunk: rows-trainfix.sh R20, a marker line inside a hunk", want prefix "a file's section holds a line that is neither a header line nor in a hunk: "
    compare_proof_test.go:376: got "the diff is cut short inside a hunk: rows-trainfix.sh R20, a marker line inside a hunk", want prefix "a file's section holds a line that is neither a header line nor in a hunk: "
    compare_proof_test.go:373: got "the diff is cut short inside a hunk: rows-trainfix.sh R20, a marker line inside a hunk"
    compare_proof_test.go:418: history: a marker inside a hunk after a `-` line, `+` lines following (local git: `git diff -M <sha> <sha>` in omriariav/amq-squad, the section of README.md): got "the diff is cut short inside a hunk: rows-tra
restore: gh.go cmp exit 0
restore: compare_history_test.go cmp exit 0
```

### R21

`bash ../../../../amq-squad-lead/T/trainfix/inputs-fu/rows-trainfix.sh e`

```text
=== row R21 ===
edit: internal/mergetrain/gh.go:279
  before: func compareDiffMismatch(files []FileChange, diff []byte) string {
  after:  func compareDiffMismatchYours(files []FileChange, diff []byte) string {
  appended: a compareDiffMismatch that calls yours and answers "" when its answer starts with:
    "a file with no changed line holds an index line and neither a binary part nor an empty file's ids: "
$ go test ./internal/mergetrain/ -count=1 -timeout 240s -v -run '^(TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged|TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate|TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate|TestBehindPRWithABinaryFileIsUpdatedAndMerged|TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate|TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead|TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged|TestCompareDiffMismatchWords|TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck|TestLeadRebindOverAFileWithNoPatchIsProvedAndBound|TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged|TestIncompleteCompareRefusesWithoutRebind|TestMaxFileCompareRefusesWithoutRebind|TestHTTP422CompareRefusesWithoutRebind|TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead|TestStrictBehindUsesPinnedCompareAndRebases|TestBehindPRWithAnEmptyFileAndAnUnchangedRenameIsUpdatedAndMerged|TestASavedCompareCutInsideItsLastLineIsRefused|TestBehindPRThatChangesALastLineWithNoNewlineIsUpdatedAndMerged|TestTheCorpusOfRealSectionsGivesItsVerdicts|TestTheHistoryListIsReadWholeOrItsErrorIsReturned|TestFakeGHRefusesADiffWithEscapeBytesUnlessAllowed|TestBehindPRWithEscapeBytesInItsDiffIsRebasedAndMerged|TestAnOlderGHThatDoesNotKnowTheFlagIsReadOnceMoreWithoutIt|TestDriftSkipsALegacyGateRecordWithOneLine|TestDriftStillNamesAMalformedGateRecord|TestTrainNotesASkippedLegacyGateRecord|TestDriftClearsAStoreAlertOnlyForAPathThisRunRead|TestTrainRecordsAStoreAlertsClearingInItsOwnWords|TestChangedPatchAfterUpdateIsRefusedWithoutRebind|TestStrictOffDoesNotCallFailingCompare|TestLaterStrictCompareErrorSkipsOnlyThatPR|TestNamedDryRunBindsTheSameMovedHeadAsRealRun|TestStrictFreshnessIsRecheckedBeforeEveryLanding|TestStrictOffBehindGreenKeepsCurrentBehavior|TestStrictBehindConflictDryRunRefuses|TestStrictBehindMovedHeadDryRunRefuses|TestStrictUpToDateDoesNotRebase|TestProtectionParsesStrictPolicy)$'
exit: 1
--- FAIL: TestCompareDiffMismatchWords
--- FAIL: TestTheCorpusOfRealSectionsGivesItsVerdicts
--- PASS: TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate
--- PASS: TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead
--- PASS: TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate
--- PASS: TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate
--- PASS: TestASavedCompareCutInsideItsLastLineIsRefused
--- PASS: TestAnOlderGHThatDoesNotKnowTheFlagIsReadOnceMoreWithoutIt
--- PASS: TestBehindPRThatChangesALastLineWithNoNewlineIsUpdatedAndMerged
--- PASS: TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged
--- PASS: TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged
--- PASS: TestBehindPRWithABinaryFileIsUpdatedAndMerged
--- PASS: TestBehindPRWithAnEmptyFileAndAnUnchangedRenameIsUpdatedAndMerged
--- PASS: TestBehindPRWithEscapeBytesInItsDiffIsRebasedAndMerged
--- PASS: TestChangedPatchAfterUpdateIsRefusedWithoutRebind
--- PASS: TestDriftClearsAStoreAlertOnlyForAPathThisRunRead
--- PASS: TestDriftSkipsALegacyGateRecordWithOneLine
--- PASS: TestDriftStillNamesAMalformedGateRecord
--- PASS: TestFakeGHRefusesADiffWithEscapeBytesUnlessAllowed
--- PASS: TestHTTP422CompareRefusesWithoutRebind
--- PASS: TestIncompleteCompareRefusesWithoutRebind
--- PASS: TestLaterStrictCompareErrorSkipsOnlyThatPR
--- PASS: TestLeadRebindOverAFileWithNoPatchIsProvedAndBound
--- PASS: TestMaxFileCompareRefusesWithoutRebind
--- PASS: TestNamedDryRunBindsTheSameMovedHeadAsRealRun
--- PASS: TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead
--- PASS: TestProtectionParsesStrictPolicy
--- PASS: TestStrictBehindConflictDryRunRefuses
--- PASS: TestStrictBehindMovedHeadDryRunRefuses
--- PASS: TestStrictBehindUsesPinnedCompareAndRebases
--- PASS: TestStrictFreshnessIsRecheckedBeforeEveryLanding
--- PASS: TestStrictOffBehindGreenKeepsCurrentBehavior
--- PASS: TestStrictOffDoesNotCallFailingCompare
--- PASS: TestStrictUpToDateDoesNotRebase
--- PASS: TestTheHistoryListIsReadWholeOrItsErrorIsReturned
--- PASS: TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck
--- PASS: TestTrainNotesASkippedLegacyGateRecord
--- PASS: TestTrainRecordsAStoreAlertsClearingInItsOwnWords
--- PASS: TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged
pass 37 fail 2 skip 0
panic: no
failed subtests:
    --- FAIL: TestCompareDiffMismatchWords/zero_count_index
    --- FAIL: TestCompareDiffMismatchWords/binary_cut_after_index
    --- FAIL: TestCompareDiffMismatchWords/index_word_alone
    --- FAIL: TestCompareDiffMismatchWords/empty_ids_too_short
messages (the first 12 lines, each cut at 240 characters; 40-digit commit ids printed as <sha>, 12-digit ones as <id>; white space at a line's end removed; a panic's stack left out; the texts are your tests'):
    compare_proof_test.go:376: got "", want prefix "a file with no changed line holds an index line and neither a binary part nor an empty file's ids: "
    compare_proof_test.go:376: got "", want prefix "a file with no changed line holds an index line and neither a binary part nor an empty file's ids: "
    compare_proof_test.go:376: got "", want prefix "a file with no changed line holds an index line and neither a binary part nor an empty file's ids: "
    compare_proof_test.go:376: got "", want prefix "a file with no changed line holds an index line and neither a binary part nor an empty file's ids: "
    compare_proof_test.go:424: made from GitHub's sections: the binary section last and cut after its index line (constructed: the case before, cut at the line boundary after the binary section's index line (git patch-id --stable of the cut
restore: gh.go cmp exit 0
restore: compare_history_test.go cmp exit 0
```

### R22

`bash ../../../../amq-squad-lead/T/trainfix/inputs-fu/rows-trainfix.sh e`

```text
=== row R22 ===
edit: internal/mergetrain/gh.go:279
  before: func compareDiffMismatch(files []FileChange, diff []byte) string {
  after:  func compareDiffMismatchYours(files []FileChange, diff []byte) string {
  appended: a compareDiffMismatch that calls yours with every hunk header's numbers made digits
$ go test ./internal/mergetrain/ -count=1 -timeout 240s -v -run '^(TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged|TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate|TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate|TestBehindPRWithABinaryFileIsUpdatedAndMerged|TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate|TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead|TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged|TestCompareDiffMismatchWords|TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck|TestLeadRebindOverAFileWithNoPatchIsProvedAndBound|TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged|TestIncompleteCompareRefusesWithoutRebind|TestMaxFileCompareRefusesWithoutRebind|TestHTTP422CompareRefusesWithoutRebind|TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead|TestStrictBehindUsesPinnedCompareAndRebases|TestBehindPRWithAnEmptyFileAndAnUnchangedRenameIsUpdatedAndMerged|TestASavedCompareCutInsideItsLastLineIsRefused|TestBehindPRThatChangesALastLineWithNoNewlineIsUpdatedAndMerged|TestTheCorpusOfRealSectionsGivesItsVerdicts|TestTheHistoryListIsReadWholeOrItsErrorIsReturned|TestFakeGHRefusesADiffWithEscapeBytesUnlessAllowed|TestBehindPRWithEscapeBytesInItsDiffIsRebasedAndMerged|TestAnOlderGHThatDoesNotKnowTheFlagIsReadOnceMoreWithoutIt|TestDriftSkipsALegacyGateRecordWithOneLine|TestDriftStillNamesAMalformedGateRecord|TestTrainNotesASkippedLegacyGateRecord|TestDriftClearsAStoreAlertOnlyForAPathThisRunRead|TestTrainRecordsAStoreAlertsClearingInItsOwnWords|TestChangedPatchAfterUpdateIsRefusedWithoutRebind|TestStrictOffDoesNotCallFailingCompare|TestLaterStrictCompareErrorSkipsOnlyThatPR|TestNamedDryRunBindsTheSameMovedHeadAsRealRun|TestStrictFreshnessIsRecheckedBeforeEveryLanding|TestStrictOffBehindGreenKeepsCurrentBehavior|TestStrictBehindConflictDryRunRefuses|TestStrictBehindMovedHeadDryRunRefuses|TestStrictUpToDateDoesNotRebase|TestProtectionParsesStrictPolicy)$'
exit: 1
--- FAIL: TestCompareDiffMismatchWords
--- PASS: TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate
--- PASS: TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead
--- PASS: TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate
--- PASS: TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate
--- PASS: TestASavedCompareCutInsideItsLastLineIsRefused
--- PASS: TestAnOlderGHThatDoesNotKnowTheFlagIsReadOnceMoreWithoutIt
--- PASS: TestBehindPRThatChangesALastLineWithNoNewlineIsUpdatedAndMerged
--- PASS: TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged
--- PASS: TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged
--- PASS: TestBehindPRWithABinaryFileIsUpdatedAndMerged
--- PASS: TestBehindPRWithAnEmptyFileAndAnUnchangedRenameIsUpdatedAndMerged
--- PASS: TestBehindPRWithEscapeBytesInItsDiffIsRebasedAndMerged
--- PASS: TestChangedPatchAfterUpdateIsRefusedWithoutRebind
--- PASS: TestDriftClearsAStoreAlertOnlyForAPathThisRunRead
--- PASS: TestDriftSkipsALegacyGateRecordWithOneLine
--- PASS: TestDriftStillNamesAMalformedGateRecord
--- PASS: TestFakeGHRefusesADiffWithEscapeBytesUnlessAllowed
--- PASS: TestHTTP422CompareRefusesWithoutRebind
--- PASS: TestIncompleteCompareRefusesWithoutRebind
--- PASS: TestLaterStrictCompareErrorSkipsOnlyThatPR
--- PASS: TestLeadRebindOverAFileWithNoPatchIsProvedAndBound
--- PASS: TestMaxFileCompareRefusesWithoutRebind
--- PASS: TestNamedDryRunBindsTheSameMovedHeadAsRealRun
--- PASS: TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead
--- PASS: TestProtectionParsesStrictPolicy
--- PASS: TestStrictBehindConflictDryRunRefuses
--- PASS: TestStrictBehindMovedHeadDryRunRefuses
--- PASS: TestStrictBehindUsesPinnedCompareAndRebases
--- PASS: TestStrictFreshnessIsRecheckedBeforeEveryLanding
--- PASS: TestStrictOffBehindGreenKeepsCurrentBehavior
--- PASS: TestStrictOffDoesNotCallFailingCompare
--- PASS: TestStrictUpToDateDoesNotRebase
--- PASS: TestTheCorpusOfRealSectionsGivesItsVerdicts
--- PASS: TestTheHistoryListIsReadWholeOrItsErrorIsReturned
--- PASS: TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck
--- PASS: TestTrainNotesASkippedLegacyGateRecord
--- PASS: TestTrainRecordsAStoreAlertsClearingInItsOwnWords
--- PASS: TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged
pass 38 fail 1 skip 0
panic: no
failed subtests:
    --- FAIL: TestCompareDiffMismatchWords/garbage_hunk_numbers
    --- FAIL: TestCompareDiffMismatchWords/signed_hunk_lengths
messages (the first 12 lines, each cut at 240 characters; 40-digit commit ids printed as <sha>, 12-digit ones as <id>; white space at a line's end removed; a panic's stack left out; the texts are your tests'):
    compare_proof_test.go:376: got "", want prefix "the diff is cut short inside a hunk: "
    compare_proof_test.go:376: got "", want prefix "the diff is cut short inside a hunk: "
restore: gh.go cmp exit 0
restore: compare_history_test.go cmp exit 0
```

### R23

`bash ../../../../amq-squad-lead/T/trainfix/inputs-fu/rows-trainfix.sh e`

```text
=== row R23 ===
edit: internal/mergetrain/gh.go:279
  before: func compareDiffMismatch(files []FileChange, diff []byte) string {
  after:  func compareDiffMismatchYours(files []FileChange, diff []byte) string {
  appended: a compareDiffMismatch that calls yours and answers "" to its sixth text when every data line of every GIT binary patch part is a letter and base85 characters
$ go test ./internal/mergetrain/ -count=1 -timeout 240s -v -run '^(TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged|TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate|TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate|TestBehindPRWithABinaryFileIsUpdatedAndMerged|TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate|TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead|TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged|TestCompareDiffMismatchWords|TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck|TestLeadRebindOverAFileWithNoPatchIsProvedAndBound|TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged|TestIncompleteCompareRefusesWithoutRebind|TestMaxFileCompareRefusesWithoutRebind|TestHTTP422CompareRefusesWithoutRebind|TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead|TestStrictBehindUsesPinnedCompareAndRebases|TestBehindPRWithAnEmptyFileAndAnUnchangedRenameIsUpdatedAndMerged|TestASavedCompareCutInsideItsLastLineIsRefused|TestBehindPRThatChangesALastLineWithNoNewlineIsUpdatedAndMerged|TestTheCorpusOfRealSectionsGivesItsVerdicts|TestTheHistoryListIsReadWholeOrItsErrorIsReturned|TestFakeGHRefusesADiffWithEscapeBytesUnlessAllowed|TestBehindPRWithEscapeBytesInItsDiffIsRebasedAndMerged|TestAnOlderGHThatDoesNotKnowTheFlagIsReadOnceMoreWithoutIt|TestDriftSkipsALegacyGateRecordWithOneLine|TestDriftStillNamesAMalformedGateRecord|TestTrainNotesASkippedLegacyGateRecord|TestDriftClearsAStoreAlertOnlyForAPathThisRunRead|TestTrainRecordsAStoreAlertsClearingInItsOwnWords|TestChangedPatchAfterUpdateIsRefusedWithoutRebind|TestStrictOffDoesNotCallFailingCompare|TestLaterStrictCompareErrorSkipsOnlyThatPR|TestNamedDryRunBindsTheSameMovedHeadAsRealRun|TestStrictFreshnessIsRecheckedBeforeEveryLanding|TestStrictOffBehindGreenKeepsCurrentBehavior|TestStrictBehindConflictDryRunRefuses|TestStrictBehindMovedHeadDryRunRefuses|TestStrictUpToDateDoesNotRebase|TestProtectionParsesStrictPolicy)$'
exit: 1
--- FAIL: TestCompareDiffMismatchWords
--- PASS: TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate
--- PASS: TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead
--- PASS: TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate
--- PASS: TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate
--- PASS: TestASavedCompareCutInsideItsLastLineIsRefused
--- PASS: TestAnOlderGHThatDoesNotKnowTheFlagIsReadOnceMoreWithoutIt
--- PASS: TestBehindPRThatChangesALastLineWithNoNewlineIsUpdatedAndMerged
--- PASS: TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged
--- PASS: TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged
--- PASS: TestBehindPRWithABinaryFileIsUpdatedAndMerged
--- PASS: TestBehindPRWithAnEmptyFileAndAnUnchangedRenameIsUpdatedAndMerged
--- PASS: TestBehindPRWithEscapeBytesInItsDiffIsRebasedAndMerged
--- PASS: TestChangedPatchAfterUpdateIsRefusedWithoutRebind
--- PASS: TestDriftClearsAStoreAlertOnlyForAPathThisRunRead
--- PASS: TestDriftSkipsALegacyGateRecordWithOneLine
--- PASS: TestDriftStillNamesAMalformedGateRecord
--- PASS: TestFakeGHRefusesADiffWithEscapeBytesUnlessAllowed
--- PASS: TestHTTP422CompareRefusesWithoutRebind
--- PASS: TestIncompleteCompareRefusesWithoutRebind
--- PASS: TestLaterStrictCompareErrorSkipsOnlyThatPR
--- PASS: TestLeadRebindOverAFileWithNoPatchIsProvedAndBound
--- PASS: TestMaxFileCompareRefusesWithoutRebind
--- PASS: TestNamedDryRunBindsTheSameMovedHeadAsRealRun
--- PASS: TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead
--- PASS: TestProtectionParsesStrictPolicy
--- PASS: TestStrictBehindConflictDryRunRefuses
--- PASS: TestStrictBehindMovedHeadDryRunRefuses
--- PASS: TestStrictBehindUsesPinnedCompareAndRebases
--- PASS: TestStrictFreshnessIsRecheckedBeforeEveryLanding
--- PASS: TestStrictOffBehindGreenKeepsCurrentBehavior
--- PASS: TestStrictOffDoesNotCallFailingCompare
--- PASS: TestStrictUpToDateDoesNotRebase
--- PASS: TestTheCorpusOfRealSectionsGivesItsVerdicts
--- PASS: TestTheHistoryListIsReadWholeOrItsErrorIsReturned
--- PASS: TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck
--- PASS: TestTrainNotesASkippedLegacyGateRecord
--- PASS: TestTrainRecordsAStoreAlertsClearingInItsOwnWords
--- PASS: TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged
pass 38 fail 1 skip 0
panic: no
failed subtests:
    --- FAIL: TestCompareDiffMismatchWords/wrong-length_binary_data
messages (the first 12 lines, each cut at 240 characters; 40-digit commit ids printed as <sha>, 12-digit ones as <id>; white space at a line's end removed; a panic's stack left out; the texts are your tests'):
    compare_proof_test.go:376: got "", want prefix "a file's section holds a line that is neither a header line nor in a hunk: "
restore: gh.go cmp exit 0
restore: compare_history_test.go cmp exit 0
```

### R24

`bash ../../../../amq-squad-lead/T/trainfix/inputs-fu/rows-trainfix.sh e`

```text
=== row R24 ===
edit: internal/mergetrain/gh.go:279
  before: func compareDiffMismatch(files []FileChange, diff []byte) string {
  after:  func compareDiffMismatchYours(files []FileChange, diff []byte) string {
  appended: a compareDiffMismatch that calls yours with a carriage return at the diff's very end made a newline
$ go test ./internal/mergetrain/ -count=1 -timeout 240s -v -run '^(TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged|TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate|TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate|TestBehindPRWithABinaryFileIsUpdatedAndMerged|TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate|TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead|TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged|TestCompareDiffMismatchWords|TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck|TestLeadRebindOverAFileWithNoPatchIsProvedAndBound|TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged|TestIncompleteCompareRefusesWithoutRebind|TestMaxFileCompareRefusesWithoutRebind|TestHTTP422CompareRefusesWithoutRebind|TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead|TestStrictBehindUsesPinnedCompareAndRebases|TestBehindPRWithAnEmptyFileAndAnUnchangedRenameIsUpdatedAndMerged|TestASavedCompareCutInsideItsLastLineIsRefused|TestBehindPRThatChangesALastLineWithNoNewlineIsUpdatedAndMerged|TestTheCorpusOfRealSectionsGivesItsVerdicts|TestTheHistoryListIsReadWholeOrItsErrorIsReturned|TestFakeGHRefusesADiffWithEscapeBytesUnlessAllowed|TestBehindPRWithEscapeBytesInItsDiffIsRebasedAndMerged|TestAnOlderGHThatDoesNotKnowTheFlagIsReadOnceMoreWithoutIt|TestDriftSkipsALegacyGateRecordWithOneLine|TestDriftStillNamesAMalformedGateRecord|TestTrainNotesASkippedLegacyGateRecord|TestDriftClearsAStoreAlertOnlyForAPathThisRunRead|TestTrainRecordsAStoreAlertsClearingInItsOwnWords|TestChangedPatchAfterUpdateIsRefusedWithoutRebind|TestStrictOffDoesNotCallFailingCompare|TestLaterStrictCompareErrorSkipsOnlyThatPR|TestNamedDryRunBindsTheSameMovedHeadAsRealRun|TestStrictFreshnessIsRecheckedBeforeEveryLanding|TestStrictOffBehindGreenKeepsCurrentBehavior|TestStrictBehindConflictDryRunRefuses|TestStrictBehindMovedHeadDryRunRefuses|TestStrictUpToDateDoesNotRebase|TestProtectionParsesStrictPolicy)$'
exit: 1
--- FAIL: TestCompareDiffMismatchWords
--- PASS: TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate
--- PASS: TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead
--- PASS: TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate
--- PASS: TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate
--- PASS: TestASavedCompareCutInsideItsLastLineIsRefused
--- PASS: TestAnOlderGHThatDoesNotKnowTheFlagIsReadOnceMoreWithoutIt
--- PASS: TestBehindPRThatChangesALastLineWithNoNewlineIsUpdatedAndMerged
--- PASS: TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged
--- PASS: TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged
--- PASS: TestBehindPRWithABinaryFileIsUpdatedAndMerged
--- PASS: TestBehindPRWithAnEmptyFileAndAnUnchangedRenameIsUpdatedAndMerged
--- PASS: TestBehindPRWithEscapeBytesInItsDiffIsRebasedAndMerged
--- PASS: TestChangedPatchAfterUpdateIsRefusedWithoutRebind
--- PASS: TestDriftClearsAStoreAlertOnlyForAPathThisRunRead
--- PASS: TestDriftSkipsALegacyGateRecordWithOneLine
--- PASS: TestDriftStillNamesAMalformedGateRecord
--- PASS: TestFakeGHRefusesADiffWithEscapeBytesUnlessAllowed
--- PASS: TestHTTP422CompareRefusesWithoutRebind
--- PASS: TestIncompleteCompareRefusesWithoutRebind
--- PASS: TestLaterStrictCompareErrorSkipsOnlyThatPR
--- PASS: TestLeadRebindOverAFileWithNoPatchIsProvedAndBound
--- PASS: TestMaxFileCompareRefusesWithoutRebind
--- PASS: TestNamedDryRunBindsTheSameMovedHeadAsRealRun
--- PASS: TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead
--- PASS: TestProtectionParsesStrictPolicy
--- PASS: TestStrictBehindConflictDryRunRefuses
--- PASS: TestStrictBehindMovedHeadDryRunRefuses
--- PASS: TestStrictBehindUsesPinnedCompareAndRebases
--- PASS: TestStrictFreshnessIsRecheckedBeforeEveryLanding
--- PASS: TestStrictOffBehindGreenKeepsCurrentBehavior
--- PASS: TestStrictOffDoesNotCallFailingCompare
--- PASS: TestStrictUpToDateDoesNotRebase
--- PASS: TestTheCorpusOfRealSectionsGivesItsVerdicts
--- PASS: TestTheHistoryListIsReadWholeOrItsErrorIsReturned
--- PASS: TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck
--- PASS: TestTrainNotesASkippedLegacyGateRecord
--- PASS: TestTrainRecordsAStoreAlertsClearingInItsOwnWords
--- PASS: TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged
pass 38 fail 1 skip 0
panic: no
failed subtests:
    --- FAIL: TestCompareDiffMismatchWords/carriage_return_at_end
messages (the first 12 lines, each cut at 240 characters; 40-digit commit ids printed as <sha>, 12-digit ones as <id>; white space at a line's end removed; a panic's stack left out; the texts are your tests'):
    compare_proof_test.go:376: got "", want prefix "the diff does not end with a newline: "
restore: gh.go cmp exit 0
restore: compare_history_test.go cmp exit 0
keep directory removed: yes
```

### R25

`bash ../../../../amq-squad-lead/T/trainfix/inputs-fu/rows-trainfix.sh f`

```text
=== row R25 ===
edit: internal/mergetrain/gh.go:279
  before: func compareDiffMismatch(files []FileChange, diff []byte) string {
  after:  func compareDiffMismatchYours(files []FileChange, diff []byte) string {
  appended: a compareDiffMismatch that reads a marker line before the first section through a nil section, then calls yours
$ go test ./internal/mergetrain/ -count=1 -timeout 240s -v -run '^(TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged|TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate|TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate|TestBehindPRWithABinaryFileIsUpdatedAndMerged|TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate|TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead|TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged|TestCompareDiffMismatchWords|TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck|TestLeadRebindOverAFileWithNoPatchIsProvedAndBound|TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged|TestIncompleteCompareRefusesWithoutRebind|TestMaxFileCompareRefusesWithoutRebind|TestHTTP422CompareRefusesWithoutRebind|TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead|TestStrictBehindUsesPinnedCompareAndRebases|TestBehindPRWithAnEmptyFileAndAnUnchangedRenameIsUpdatedAndMerged|TestASavedCompareCutInsideItsLastLineIsRefused|TestBehindPRThatChangesALastLineWithNoNewlineIsUpdatedAndMerged|TestTheCorpusOfRealSectionsGivesItsVerdicts|TestTheHistoryListIsReadWholeOrItsErrorIsReturned|TestFakeGHRefusesADiffWithEscapeBytesUnlessAllowed|TestBehindPRWithEscapeBytesInItsDiffIsRebasedAndMerged|TestAnOlderGHThatDoesNotKnowTheFlagIsReadOnceMoreWithoutIt|TestDriftSkipsALegacyGateRecordWithOneLine|TestDriftStillNamesAMalformedGateRecord|TestTrainNotesASkippedLegacyGateRecord|TestDriftClearsAStoreAlertOnlyForAPathThisRunRead|TestTrainRecordsAStoreAlertsClearingInItsOwnWords|TestChangedPatchAfterUpdateIsRefusedWithoutRebind|TestStrictOffDoesNotCallFailingCompare|TestLaterStrictCompareErrorSkipsOnlyThatPR|TestNamedDryRunBindsTheSameMovedHeadAsRealRun|TestStrictFreshnessIsRecheckedBeforeEveryLanding|TestStrictOffBehindGreenKeepsCurrentBehavior|TestStrictBehindConflictDryRunRefuses|TestStrictBehindMovedHeadDryRunRefuses|TestStrictUpToDateDoesNotRebase|TestProtectionParsesStrictPolicy)$'
exit: 1
--- FAIL: TestCompareDiffMismatchWords
--- PASS: TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate
--- PASS: TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead
--- PASS: TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate
--- PASS: TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate
--- PASS: TestBehindPRThatChangesALastLineWithNoNewlineIsUpdatedAndMerged
--- PASS: TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged
--- PASS: TestBehindPRWithABinaryFileIsUpdatedAndMerged
--- PASS: TestBehindPRWithAnEmptyFileAndAnUnchangedRenameIsUpdatedAndMerged
--- PASS: TestTheHistoryListIsReadWholeOrItsErrorIsReturned
pass 9 fail 1 skip 0
panic: yes (a test panicked: it has a FAIL line, and the tests after it did not run and have no line)
panic: runtime error: invalid memory address or nil pointer dereference [recovered, repanicked]
failed subtests:
    --- FAIL: TestCompareDiffMismatchWords/marker_before_any_section
messages (the first 12 lines, each cut at 240 characters; 40-digit commit ids printed as <sha>, 12-digit ones as <id>; white space at a line's end removed; a panic's stack left out; the texts are your tests'):
restore: gh.go cmp exit 0
restore: compare_history_test.go cmp exit 0
```

### R26

`bash ../../../../amq-squad-lead/T/trainfix/inputs-fu/rows-trainfix.sh f`

```text
=== row R26 ===
edit: internal/mergetrain/gh.go:279
  before: func compareDiffMismatch(files []FileChange, diff []byte) string {
  after:  func compareDiffMismatchYours(files []FileChange, diff []byte) string {
  appended: a compareDiffMismatch: your check is handed the diff with every marker line of a hunk after its second taken out.
$ go test ./internal/mergetrain/ -count=1 -timeout 240s -v -run '^(TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged|TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate|TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate|TestBehindPRWithABinaryFileIsUpdatedAndMerged|TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate|TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead|TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged|TestCompareDiffMismatchWords|TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck|TestLeadRebindOverAFileWithNoPatchIsProvedAndBound|TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged|TestIncompleteCompareRefusesWithoutRebind|TestMaxFileCompareRefusesWithoutRebind|TestHTTP422CompareRefusesWithoutRebind|TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead|TestStrictBehindUsesPinnedCompareAndRebases|TestBehindPRWithAnEmptyFileAndAnUnchangedRenameIsUpdatedAndMerged|TestASavedCompareCutInsideItsLastLineIsRefused|TestBehindPRThatChangesALastLineWithNoNewlineIsUpdatedAndMerged|TestTheCorpusOfRealSectionsGivesItsVerdicts|TestTheHistoryListIsReadWholeOrItsErrorIsReturned|TestFakeGHRefusesADiffWithEscapeBytesUnlessAllowed|TestBehindPRWithEscapeBytesInItsDiffIsRebasedAndMerged|TestAnOlderGHThatDoesNotKnowTheFlagIsReadOnceMoreWithoutIt|TestDriftSkipsALegacyGateRecordWithOneLine|TestDriftStillNamesAMalformedGateRecord|TestTrainNotesASkippedLegacyGateRecord|TestDriftClearsAStoreAlertOnlyForAPathThisRunRead|TestTrainRecordsAStoreAlertsClearingInItsOwnWords|TestChangedPatchAfterUpdateIsRefusedWithoutRebind|TestStrictOffDoesNotCallFailingCompare|TestLaterStrictCompareErrorSkipsOnlyThatPR|TestNamedDryRunBindsTheSameMovedHeadAsRealRun|TestStrictFreshnessIsRecheckedBeforeEveryLanding|TestStrictOffBehindGreenKeepsCurrentBehavior|TestStrictBehindConflictDryRunRefuses|TestStrictBehindMovedHeadDryRunRefuses|TestStrictUpToDateDoesNotRebase|TestProtectionParsesStrictPolicy)$'
exit: 1
--- FAIL: TestCompareDiffMismatchWords
--- PASS: TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate
--- PASS: TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead
--- PASS: TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate
--- PASS: TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate
--- PASS: TestASavedCompareCutInsideItsLastLineIsRefused
--- PASS: TestAnOlderGHThatDoesNotKnowTheFlagIsReadOnceMoreWithoutIt
--- PASS: TestBehindPRThatChangesALastLineWithNoNewlineIsUpdatedAndMerged
--- PASS: TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged
--- PASS: TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged
--- PASS: TestBehindPRWithABinaryFileIsUpdatedAndMerged
--- PASS: TestBehindPRWithAnEmptyFileAndAnUnchangedRenameIsUpdatedAndMerged
--- PASS: TestBehindPRWithEscapeBytesInItsDiffIsRebasedAndMerged
--- PASS: TestChangedPatchAfterUpdateIsRefusedWithoutRebind
--- PASS: TestDriftClearsAStoreAlertOnlyForAPathThisRunRead
--- PASS: TestDriftSkipsALegacyGateRecordWithOneLine
--- PASS: TestDriftStillNamesAMalformedGateRecord
--- PASS: TestFakeGHRefusesADiffWithEscapeBytesUnlessAllowed
--- PASS: TestHTTP422CompareRefusesWithoutRebind
--- PASS: TestIncompleteCompareRefusesWithoutRebind
--- PASS: TestLaterStrictCompareErrorSkipsOnlyThatPR
--- PASS: TestLeadRebindOverAFileWithNoPatchIsProvedAndBound
--- PASS: TestMaxFileCompareRefusesWithoutRebind
--- PASS: TestNamedDryRunBindsTheSameMovedHeadAsRealRun
--- PASS: TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead
--- PASS: TestProtectionParsesStrictPolicy
--- PASS: TestStrictBehindConflictDryRunRefuses
--- PASS: TestStrictBehindMovedHeadDryRunRefuses
--- PASS: TestStrictBehindUsesPinnedCompareAndRebases
--- PASS: TestStrictFreshnessIsRecheckedBeforeEveryLanding
--- PASS: TestStrictOffBehindGreenKeepsCurrentBehavior
--- PASS: TestStrictOffDoesNotCallFailingCompare
--- PASS: TestStrictUpToDateDoesNotRebase
--- PASS: TestTheCorpusOfRealSectionsGivesItsVerdicts
--- PASS: TestTheHistoryListIsReadWholeOrItsErrorIsReturned
--- PASS: TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck
--- PASS: TestTrainNotesASkippedLegacyGateRecord
--- PASS: TestTrainRecordsAStoreAlertsClearingInItsOwnWords
--- PASS: TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged
pass 38 fail 1 skip 0
panic: no
failed subtests:
    --- FAIL: TestCompareDiffMismatchWords/third_marker_after_two_inside
    --- FAIL: TestCompareDiffMismatchWords/three_markers_inside
messages (the first 12 lines, each cut at 240 characters; 40-digit commit ids printed as <sha>, 12-digit ones as <id>; white space at a line's end removed; a panic's stack left out; the texts are your tests'):
    compare_proof_test.go:376: got "", want prefix "a file's section holds a line that is neither a header line nor in a hunk: "
    compare_proof_test.go:376: got "", want prefix "a file's section holds a line that is neither a header line nor in a hunk: "
restore: gh.go cmp exit 0
restore: compare_history_test.go cmp exit 0
```

### R27

`bash ../../../../amq-squad-lead/T/trainfix/inputs-fu/rows-trainfix.sh f`

```text
=== row R27 ===
edit: internal/mergetrain/gh.go:279
  before: func compareDiffMismatch(files []FileChange, diff []byte) string {
  after:  func compareDiffMismatchYours(files []FileChange, diff []byte) string {
  appended: a compareDiffMismatch: a diff with a section that holds more than two marker lines in all is answered with the sixth text before your check runs.
$ go test ./internal/mergetrain/ -count=1 -timeout 240s -v -run '^(TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged|TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate|TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate|TestBehindPRWithABinaryFileIsUpdatedAndMerged|TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate|TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead|TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged|TestCompareDiffMismatchWords|TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck|TestLeadRebindOverAFileWithNoPatchIsProvedAndBound|TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged|TestIncompleteCompareRefusesWithoutRebind|TestMaxFileCompareRefusesWithoutRebind|TestHTTP422CompareRefusesWithoutRebind|TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead|TestStrictBehindUsesPinnedCompareAndRebases|TestBehindPRWithAnEmptyFileAndAnUnchangedRenameIsUpdatedAndMerged|TestASavedCompareCutInsideItsLastLineIsRefused|TestBehindPRThatChangesALastLineWithNoNewlineIsUpdatedAndMerged|TestTheCorpusOfRealSectionsGivesItsVerdicts|TestTheHistoryListIsReadWholeOrItsErrorIsReturned|TestFakeGHRefusesADiffWithEscapeBytesUnlessAllowed|TestBehindPRWithEscapeBytesInItsDiffIsRebasedAndMerged|TestAnOlderGHThatDoesNotKnowTheFlagIsReadOnceMoreWithoutIt|TestDriftSkipsALegacyGateRecordWithOneLine|TestDriftStillNamesAMalformedGateRecord|TestTrainNotesASkippedLegacyGateRecord|TestDriftClearsAStoreAlertOnlyForAPathThisRunRead|TestTrainRecordsAStoreAlertsClearingInItsOwnWords|TestChangedPatchAfterUpdateIsRefusedWithoutRebind|TestStrictOffDoesNotCallFailingCompare|TestLaterStrictCompareErrorSkipsOnlyThatPR|TestNamedDryRunBindsTheSameMovedHeadAsRealRun|TestStrictFreshnessIsRecheckedBeforeEveryLanding|TestStrictOffBehindGreenKeepsCurrentBehavior|TestStrictBehindConflictDryRunRefuses|TestStrictBehindMovedHeadDryRunRefuses|TestStrictUpToDateDoesNotRebase|TestProtectionParsesStrictPolicy)$'
exit: 1
--- FAIL: TestCompareDiffMismatchWords
--- PASS: TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate
--- PASS: TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead
--- PASS: TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate
--- PASS: TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate
--- PASS: TestASavedCompareCutInsideItsLastLineIsRefused
--- PASS: TestAnOlderGHThatDoesNotKnowTheFlagIsReadOnceMoreWithoutIt
--- PASS: TestBehindPRThatChangesALastLineWithNoNewlineIsUpdatedAndMerged
--- PASS: TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged
--- PASS: TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged
--- PASS: TestBehindPRWithABinaryFileIsUpdatedAndMerged
--- PASS: TestBehindPRWithAnEmptyFileAndAnUnchangedRenameIsUpdatedAndMerged
--- PASS: TestBehindPRWithEscapeBytesInItsDiffIsRebasedAndMerged
--- PASS: TestChangedPatchAfterUpdateIsRefusedWithoutRebind
--- PASS: TestDriftClearsAStoreAlertOnlyForAPathThisRunRead
--- PASS: TestDriftSkipsALegacyGateRecordWithOneLine
--- PASS: TestDriftStillNamesAMalformedGateRecord
--- PASS: TestFakeGHRefusesADiffWithEscapeBytesUnlessAllowed
--- PASS: TestHTTP422CompareRefusesWithoutRebind
--- PASS: TestIncompleteCompareRefusesWithoutRebind
--- PASS: TestLaterStrictCompareErrorSkipsOnlyThatPR
--- PASS: TestLeadRebindOverAFileWithNoPatchIsProvedAndBound
--- PASS: TestMaxFileCompareRefusesWithoutRebind
--- PASS: TestNamedDryRunBindsTheSameMovedHeadAsRealRun
--- PASS: TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead
--- PASS: TestProtectionParsesStrictPolicy
--- PASS: TestStrictBehindConflictDryRunRefuses
--- PASS: TestStrictBehindMovedHeadDryRunRefuses
--- PASS: TestStrictBehindUsesPinnedCompareAndRebases
--- PASS: TestStrictFreshnessIsRecheckedBeforeEveryLanding
--- PASS: TestStrictOffBehindGreenKeepsCurrentBehavior
--- PASS: TestStrictOffDoesNotCallFailingCompare
--- PASS: TestStrictUpToDateDoesNotRebase
--- PASS: TestTheCorpusOfRealSectionsGivesItsVerdicts
--- PASS: TestTheHistoryListIsReadWholeOrItsErrorIsReturned
--- PASS: TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck
--- PASS: TestTrainNotesASkippedLegacyGateRecord
--- PASS: TestTrainRecordsAStoreAlertsClearingInItsOwnWords
--- PASS: TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged
pass 38 fail 1 skip 0
panic: no
failed subtests:
    --- FAIL: TestCompareDiffMismatchWords/two_hunks_with_two_markers_each
messages (the first 12 lines, each cut at 240 characters; 40-digit commit ids printed as <sha>, 12-digit ones as <id>; white space at a line's end removed; a panic's stack left out; the texts are your tests'):
    compare_proof_test.go:373: got "a file's section holds a line that is neither a header line nor in a hunk: rows-trainfix.sh R27, a third marker line in one section"
restore: gh.go cmp exit 0
restore: compare_history_test.go cmp exit 0
```

### R28

`bash ../../../../amq-squad-lead/T/trainfix/inputs-fu/rows-trainfix.sh f`

```text
=== row R28 ===
edit: internal/mergetrain/compare_history_test.go:31
  before: if err := sc.Err(); err != nil {
  after:  if err := sc.Err(); err != nil && false {
$ go test ./internal/mergetrain/ -count=1 -timeout 240s -v -run '^(TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged|TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate|TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate|TestBehindPRWithABinaryFileIsUpdatedAndMerged|TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate|TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead|TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged|TestCompareDiffMismatchWords|TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck|TestLeadRebindOverAFileWithNoPatchIsProvedAndBound|TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged|TestIncompleteCompareRefusesWithoutRebind|TestMaxFileCompareRefusesWithoutRebind|TestHTTP422CompareRefusesWithoutRebind|TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead|TestStrictBehindUsesPinnedCompareAndRebases|TestBehindPRWithAnEmptyFileAndAnUnchangedRenameIsUpdatedAndMerged|TestASavedCompareCutInsideItsLastLineIsRefused|TestBehindPRThatChangesALastLineWithNoNewlineIsUpdatedAndMerged|TestTheCorpusOfRealSectionsGivesItsVerdicts|TestTheHistoryListIsReadWholeOrItsErrorIsReturned|TestFakeGHRefusesADiffWithEscapeBytesUnlessAllowed|TestBehindPRWithEscapeBytesInItsDiffIsRebasedAndMerged|TestAnOlderGHThatDoesNotKnowTheFlagIsReadOnceMoreWithoutIt|TestDriftSkipsALegacyGateRecordWithOneLine|TestDriftStillNamesAMalformedGateRecord|TestTrainNotesASkippedLegacyGateRecord|TestDriftClearsAStoreAlertOnlyForAPathThisRunRead|TestTrainRecordsAStoreAlertsClearingInItsOwnWords|TestChangedPatchAfterUpdateIsRefusedWithoutRebind|TestStrictOffDoesNotCallFailingCompare|TestLaterStrictCompareErrorSkipsOnlyThatPR|TestNamedDryRunBindsTheSameMovedHeadAsRealRun|TestStrictFreshnessIsRecheckedBeforeEveryLanding|TestStrictOffBehindGreenKeepsCurrentBehavior|TestStrictBehindConflictDryRunRefuses|TestStrictBehindMovedHeadDryRunRefuses|TestStrictUpToDateDoesNotRebase|TestProtectionParsesStrictPolicy)$'
exit: 1
--- FAIL: TestTheHistoryListIsReadWholeOrItsErrorIsReturned
--- PASS: TestABinaryFileWithNoIndexLineIsRefusedBeforeTheUpdate
--- PASS: TestACompareAtTheFileLimitIsRefusedBeforeTheDiffIsRead
--- PASS: TestADiffThatLacksAFileOfTheCompareIsRefusedBeforeTheUpdate
--- PASS: TestADiffWhoseLineCountsAreNotTheComparesIsRefusedBeforeTheUpdate
--- PASS: TestASavedCompareCutInsideItsLastLineIsRefused
--- PASS: TestAnOlderGHThatDoesNotKnowTheFlagIsReadOnceMoreWithoutIt
--- PASS: TestBehindPRThatChangesALastLineWithNoNewlineIsUpdatedAndMerged
--- PASS: TestBehindPRWhoseCompareGivesAFileNoPatchIsUpdatedAndMerged
--- PASS: TestBehindPRWhoseDiffEndsInABlankContextLineIsUpdatedAndMerged
--- PASS: TestBehindPRWithABinaryFileIsUpdatedAndMerged
--- PASS: TestBehindPRWithAnEmptyFileAndAnUnchangedRenameIsUpdatedAndMerged
--- PASS: TestBehindPRWithEscapeBytesInItsDiffIsRebasedAndMerged
--- PASS: TestChangedPatchAfterUpdateIsRefusedWithoutRebind
--- PASS: TestCompareDiffMismatchWords
--- PASS: TestDriftClearsAStoreAlertOnlyForAPathThisRunRead
--- PASS: TestDriftSkipsALegacyGateRecordWithOneLine
--- PASS: TestDriftStillNamesAMalformedGateRecord
--- PASS: TestFakeGHRefusesADiffWithEscapeBytesUnlessAllowed
--- PASS: TestHTTP422CompareRefusesWithoutRebind
--- PASS: TestIncompleteCompareRefusesWithoutRebind
--- PASS: TestLaterStrictCompareErrorSkipsOnlyThatPR
--- PASS: TestLeadRebindOverAFileWithNoPatchIsProvedAndBound
--- PASS: TestMaxFileCompareRefusesWithoutRebind
--- PASS: TestNamedDryRunBindsTheSameMovedHeadAsRealRun
--- PASS: TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead
--- PASS: TestProtectionParsesStrictPolicy
--- PASS: TestStrictBehindConflictDryRunRefuses
--- PASS: TestStrictBehindMovedHeadDryRunRefuses
--- PASS: TestStrictBehindUsesPinnedCompareAndRebases
--- PASS: TestStrictFreshnessIsRecheckedBeforeEveryLanding
--- PASS: TestStrictOffBehindGreenKeepsCurrentBehavior
--- PASS: TestStrictOffDoesNotCallFailingCompare
--- PASS: TestStrictUpToDateDoesNotRebase
--- PASS: TestTheCorpusOfRealSectionsGivesItsVerdicts
--- PASS: TestThePatchIDOfASavedCompareIsTheOneGitGaveBeforeTheCheck
--- PASS: TestTrainNotesASkippedLegacyGateRecord
--- PASS: TestTrainRecordsAStoreAlertsClearingInItsOwnWords
--- PASS: TestUnrecordedUpdateOverAFileWithNoPatchIsRecoveredAndMerged
pass 38 fail 1 skip 0
panic: no
failed subtests:
messages (the first 12 lines, each cut at 240 characters; 40-digit commit ids printed as <sha>, 12-digit ones as <id>; white space at a line's end removed; a panic's stack left out; the texts are your tests'):
    compare_history_test.go:133: over-long list did not return its error after one pair
restore: gh.go cmp exit 0
restore: compare_history_test.go cmp exit 0
keep directory removed: yes
```

## What ran

The seat ran the whole internal/mergetrain package once, before the rows.

The seat ran the check over the history's 1,930 commits once in each form, before the rows.

### `go build ./...`

Exit 0. No output.

### `go vet ./internal/mergetrain/`

Exit 0. No output.

### `gofmt -l internal/mergetrain`

Exit 0. No output.

### `go test ./internal/mergetrain/ -count=1 -timeout 470s`

Exit 0.

```text
ok  	github.com/omriariav/amq-squad/v3/internal/mergetrain	128.036s
```

### `TRAINFIX_HISTORY_LIST=$PWD/../../../../amq-squad-lead/T/trainfix/inputs-fu/history/commits.txt go test ./internal/mergetrain/ -count=1 -timeout 300s -v -run '^TestTheCheckOverTheHistoryRefusesNoWholeDiff$/^plain$'`

Exit 0.

```text
=== RUN   TestTheCheckOverTheHistoryRefusesNoWholeDiff
=== RUN   TestTheCheckOverTheHistoryRefusesNoWholeDiff/plain
    compare_history_test.go:102: git diff -M: commits 1930, refused 0
--- PASS: TestTheCheckOverTheHistoryRefusesNoWholeDiff (22.34s)
    --- PASS: TestTheCheckOverTheHistoryRefusesNoWholeDiff/plain (22.34s)
PASS
ok  	github.com/omriariav/amq-squad/v3/internal/mergetrain	22.775s
```

### `TRAINFIX_HISTORY_LIST=$PWD/../../../../amq-squad-lead/T/trainfix/inputs-fu/history/commits.txt go test ./internal/mergetrain/ -count=1 -timeout 300s -v -run '^TestTheCheckOverTheHistoryRefusesNoWholeDiff$/^binary$'`

Exit 0.

```text
=== RUN   TestTheCheckOverTheHistoryRefusesNoWholeDiff
=== RUN   TestTheCheckOverTheHistoryRefusesNoWholeDiff/binary
    compare_history_test.go:102: git diff -M --binary: commits 1930, refused 0
--- PASS: TestTheCheckOverTheHistoryRefusesNoWholeDiff (22.30s)
    --- PASS: TestTheCheckOverTheHistoryRefusesNoWholeDiff/binary (22.30s)
PASS
ok  	github.com/omriariav/amq-squad/v3/internal/mergetrain	22.693s
```

### `bash ../../../../amq-squad-lead/T/trainfix/inputs-fu/rows-trainfix.sh a`

Exit 0. Its complete R0 through R4 output is in Rows.

### `bash ../../../../amq-squad-lead/T/trainfix/inputs-fu/rows-trainfix.sh b`

Exit 0. Its complete R5 through R9 output is in Rows.

### `bash ../../../../amq-squad-lead/T/trainfix/inputs-fu/rows-trainfix.sh c`

Exit 0. Its complete R10 through R14 output is in Rows.

### `bash ../../../../amq-squad-lead/T/trainfix/inputs-fu/rows-trainfix.sh d`

Exit 0. Its complete R15 through R19 output is in Rows.

### `bash ../../../../amq-squad-lead/T/trainfix/inputs-fu/rows-trainfix.sh e`

Exit 0. Its complete R20 through R24 output is in Rows.

### `bash ../../../../amq-squad-lead/T/trainfix/inputs-fu/rows-trainfix.sh f`

Exit 0. Its complete R25 through R28 output is in Rows.

### `go run ./scripts/redact-evidence -check docs/v3-spike/330-train-compare-proof/`

Exit 0. No output.

### `git diff --check`

Exit 0. No output.

### `git hash-object internal/mergetrain/testdata/compare-corpus.json`

Exit 0.

```text
854dda1b458eafea18eabab6ddc01f931180f821
```

### `git diff --numstat -- docs/v3-followups.md`

Exit 0.

```text
10	17	docs/v3-followups.md
```

### `grep -cE '/Us''ers/|/private/t''mp|rows-trainfix-ke''ep|/var/fold''ers' docs/v3-spike/330-train-compare-proof/README.md`

Exit 1.

```text
0
```

### `python3 ../../../../amq-squad-lead/T/trainfix/inputs-fu/cites.py docs/v3-spike/330-train-compare-proof/README.md`

Exit 0.

```text
note:3 internal/mergetrain/gh.go:200-201 | first: // compareProofFilesJQ is unique to the proof so its call is | last: const compareProofFilesJQ = `.files[]? | {path: .filename, p | next:  | closes inside
note:3 internal/mergetrain/gh.go:279-471 | first: func compareDiffMismatch(files []FileChange, diff []byte) st | last: } | next:  | closes inside
note:87 internal/mergetrain/gh.go:240-242 | first: if len(files) >= MaxCompareFiles { | last: } | next: // gh refuses to print a response holdin | closes inside
note:89 internal/mergetrain/fakegh_test.go:502-530 | first: if slices.Contains(args, "Accept: application/vnd.github.dif | last: } | next: if slices.Contains(args, ".behind_by") { | closes inside
note:138 internal/mergetrain/gh.go:227-275 | first: func (g *GH) PatchID(ctx context.Context, repository, base,  | last: } | next:  | closes inside
note:154 internal/mergetrain/gh.go:498-529 | first: func compareBinaryPatchLine(line string) bool { | last: } | next:  | closes inside
note:158 internal/mergetrain/gh.go:261-263 | first: if mismatch := compareDiffMismatch(files, diff); mismatch != | last: } | next: cmd := exec.CommandContext(ctx, "git", " | closes inside
note:242 internal/mergetrain/gh.go:201-201 | first: const compareProofFilesJQ = `.files[]? | {path: .filename, p | last: const compareProofFilesJQ = `.files[]? | {path: .filename, p | next:  | -
cites 8, short 0
```

### `grep -cE '[[:space:]]+$' docs/v3-spike/330-train-compare-proof/README.md`

Exit 1.

```text
0
```

### `git status --porcelain --untracked-files=all`

Exit 0.

```text
 M docs/v3-followups.md
 M docs/v3-spike/330-train-compare-proof/README.md
 M internal/mergetrain/compare_history_test.go
 M internal/mergetrain/compare_proof_test.go
 M internal/mergetrain/gh.go
 M internal/mergetrain/testdata/compare-corpus.json
```

### `git diff --stat -- docs/v3-followups.md docs/v3-spike/330-train-compare-proof/README.md internal/mergetrain/compare_history_test.go internal/mergetrain/compare_proof_test.go internal/mergetrain/gh.go internal/mergetrain/testdata/compare-corpus.json`

Exit 0.

```text
 docs/v3-followups.md                             |  27 +-
 docs/v3-spike/330-train-compare-proof/README.md  | 864 ++++++++++++++++-------
 internal/mergetrain/compare_history_test.go      |  92 ++-
 internal/mergetrain/compare_proof_test.go        |  15 +-
 internal/mergetrain/gh.go                        |   3 +
 internal/mergetrain/testdata/compare-corpus.json |  30 +
 6 files changed, 742 insertions(+), 289 deletions(-)
```
