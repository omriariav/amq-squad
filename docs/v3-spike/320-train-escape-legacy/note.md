# 320-train-escape-legacy: the diff read with escape bytes, and legacy gate records skipped by the drift reader

Brief: 320-train-escape-legacy, approved by the plan reviewer on 2026-11-11, an
rc.2 gate. Base: origin/v3 at c65a709c690dd0ed5877076cfeebf470da339262. Branch:
`v3-train-escape-legacy`. Written by a helper agent in its own worktree.

Line numbers below are this branch's unless a sentence says "on the base".

## The two defects as measured

These are the lead's runs of 2026-11-11, quoted from the brief. This PR did
not run gh against the real repository and did not read the v31 session.

1. The diff read. `GH.PatchID` read the raw compare diff with
   `gh api <compare> -H "Accept: application/vnd.github.diff"`
   (`internal/mergetrain/gh.go:243-244` on the base). gh 2.100.0 refuses a
   response that contains terminal escape bytes, also when its output is
   captured: exit 1, no bytes, and on stderr "the response contains terminal
   escape sequences; pass --allow-escape-sequences to output it anyway". The
   train then failed at "prove the reviewed patch before updating #N"
   (`train.go:705` on the base) and the PR was never updated. The diff of #948
   has 273 lines with an escape byte (five captured run logs). With the flag
   the same call answers 372,686 bytes, and `git patch-id --stable` over them
   equals the local `git diff c65a709c...6150ccc9 | git patch-id --stable`
   (2fc1edf4d0844d7e8dcee2998f73e83ce1e9c617).
2. The legacy records. `sessionTargets` (`internal/mergetrain/drift.go:230-280`
   on the base) turned every record that does not load into a
   `gate_store_unreadable` alert. A record of a kind the catalog no longer
   carries fails `Record.Validate` ("gate record is malformed"), so the three
   `quarantine_clear` records in the v31 session (owner-signed seat clears of
   2026-10-30 and 2026-10-31; the kind was added by a724b3db and left the
   catalog in fdfa28ff) have raised three alerts since 2026-11-05 17:20Z on
   every train run. Brief 31-1's rule is in
   `internal/gatestore/legacy.go:11-15`: "Readers list it as legacy and skip
   it with one line naming the kind and the gate id; it is never a refusal."
   `gate status`, the hook store and `status` apply it; the drift reader did
   not.

## The change

Part 1, the diff read (`internal/mergetrain/gh.go:243-254`, `:274-278`).

- That one call passes `--allow-escape-sequences` (`gh.go:248`). Its bytes go
  only to `git patch-id --stable` on stdin, as before. No other gh call passes
  the flag: row E2 reads the fake's call log and fails on any other line that
  carries it.
- An older gh answers the flag with "unknown flag: --allow-escape-sequences"
  and exit 1. On exactly that answer (`unknownEscapeFlag`, `gh.go:275`: the
  `GHError`'s `Stderr` holds those words) the call is made once more without
  the flag (`gh.go:249-254`). Any other error is handled as before the change:
  `compareTooLarge` gives `ErrIncompleteDiff`, anything else is returned. A
  second read that fails returns its own error.

Part 2, legacy gate records (`internal/mergetrain/drift.go:299-362`).

- In `sessionTargets`, on a load error, `store.Legacy(id)` true skips the
  record (`drift.go:337-341`): no alert, and `gatestore.LegacyLine(id, kind)`
  is collected once for that record. Any other load error keeps the alert, by
  the same line as on the base (`drift.go:344`).
- `Drift` and `DriftResolved` keep their signatures and return the same
  alerts. The lines travel by a sibling, `DriftNoted` (`drift.go:134`), which
  returns a `DriftRun`: the alerts raised, the earlier alerts resolved (each a
  `ResolvedAlert`, the alert with the words of why) and `Notes`.
  `DriftResolved` is now a wrapper over it (`drift.go:108-114`).
- The train's result has `Notes []string` with `json:"notes,omitempty"`
  (`train.go:222`), so a run with no legacy record encodes what it encoded
  before. The train journals one audit entry of kind `note` per line
  (`train.go:297-301`), and `printMergeTrain` prints each line on its own line
  after the protection line (`internal/cli/merge_train.go:328-332`).
- The doctor's check reads through `Drift` and prints no line.
- The pi words table maps the new kind: `note: "noted a skipped legacy gate
  record"` (`pi/extensions/lib/words.ts:745`). The parity row of
  `pi/test/words.test.ts` ("row 7", `:161`) reads every journaled kind out of
  the Go sources and fails on one without words; the mapping's own assertion
  is at `:187`.

Part 3, the alerts already on disk (`drift.go:239-264`, `:176`).

- `clearStoreAlerts` removes an on-disk `gate_store_unreadable` alert only
  when this run read the path the alert names: it loaded the record
  (`drift.go:347`), it skipped the record as legacy (`drift.go:339`), or it
  listed the gates directory (`drift.go:328`). The condition is positive. A
  path this run did not reach leaves the alert where it is, and a path this
  run flagged again is never among the paths read.
- The removed alert is returned as resolved with words of its own, and the
  train writes `alert_cleared` once with them (`train.go:302-306`): "; the
  record now loads", "; the record is a legacy kind and is skipped", "; the
  gate store can be listed again". An ungated alert that a gate now covers
  keeps the words it had, "; a verified owner gate now covers it"
  (`drift.go:212`).
- `alert_cleared` was already mapped at `pi/extensions/lib/words.ts:742`
  ("cleared an alert"), so the clearing needs no new words on a screen line.

## The rows

All Go rows are in `internal/mergetrain/escape_legacy_320_test.go` unless the
table names another file. Every row asserts behaviour; none reads source text.

| Row | What it holds | Test |
| --- | --- | --- |
| E1 | The fake gh answers a compare diff holding escape bytes with gh's words, exit 1 and no bytes unless the flag is passed, and prints it with the flag. Under `OldGH` it answers the flag with "unknown flag" and prints the bytes without it. Asserted on the fake directly. | `TestFakeGHRefusesADiffWithEscapeBytesUnlessAllowed` |
| E2 | A BEHIND PR whose reviewed diff adds a file holding escape bytes is rebased and merged: one force push, patch ids equal and non-empty, one rebind entry, the task bound to the new head. Every diff read carries the flag and no other gh call does. | `TestBehindPRWithEscapeBytesInItsDiffIsRebasedAndMerged` |
| E3 | Under `OldGH` the same PR is rebased and merged with equal patch ids, and each proof reads the diff exactly twice: with the flag, then without. | `TestAnOlderGHThatDoesNotKnowTheFlagIsReadOnceMoreWithoutIt` |
| E4 | A diff read that fails with HTTP 502, with a diff too large, or with gh's own refusal of the bytes is read once and the error is the one returned before the change. After an unknown flag, a second read that fails returns its own error (HTTP 502, or an incomplete diff). | `TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead` (five subtests) |
| L1 | A store with one accepted release gate and one `quarantine_clear` record raises no alert on two runs, the tag the release gate covers raises no `ungated` alert, and exactly one legacy line names the kind and the gate id. `Drift` and `DriftResolved` raise nothing for it either. | `TestDriftSkipsALegacyGateRecordWithOneLine` |
| L2 | A malformed record still alerts and is not noted as legacy: torn JSON, a known kind with a bad target, a legacy kind whose `gate_id` is not its file's name. | `TestDriftNamesAGateRecordThatDoesNotLoad` (`brief3112_test.go:184`, untouched) and `TestDriftStillNamesAMalformedGateRecord` (two subtests) |
| L3 | A train dry run over a root holding the legacy record: no alert, `Notes` the one line, the line once in the result's JSON, one `note` audit entry a run. A second run says it again and writes no alert file. A run with no such record has no notes and no `notes` key. | `TestTrainNotesASkippedLegacyGateRecord` |
| L3, printed | The same dry run's result, printed by `cli.FormatMergeTrain`: the line once, on its own line after the protection line, and no alert words. | `TestTrainDryRunPrintsASkippedLegacyGateRecordOnce` (`escape_legacy_320_text_test.go`) |
| L3, printed | Two notes print one a line after the protection line; a result with none prints what it printed before. | `TestMergeTrainPrintsEachNoteOnItsOwnLineAfterTheProtectionLine` (`internal/cli/merge_train_test.go`) |
| L3, pi | The kind `note` has words in the one table. | "row 7: every wait and block reason in Go's sources maps to plain words in the one table" (`pi/test/words.test.ts:161`, the assertion at `:187`) |
| L4 (a) | A planted alert naming a legacy record is removed by the run that skips it and returned as resolved with the legacy words. | `TestDriftClearsAStoreAlertOnlyForAPathThisRunRead/a_legacy_record_this_run_skipped` |
| L4 (b) | The same for a record that now loads. | `.../a_record_this_run_loaded` |
| L4, store level | An alert naming a gates directory is removed by the run that lists it, and stays when this run did not list it. | `.../a_gates_directory_this_run_listed`, `.../a_gates_directory_this_run_did_not_list` |
| L4 (c) | Not removed: the record absent, its session directory missing, the parent of the sessions unlistable. | `.../the_record_absent`, `.../its_session_directory_missing`, `.../the_parent_of_the_sessions_unlistable` |
| L4 (d) | An alert this run raises again stays, under the same id. | `.../an_alert_this_run_raises_again` |
| L4, the train | The train writes `alert_cleared` once for each clearing in its own words: the legacy record, the record that now loads, and an ungated tag a gate now covers. | `TestTrainRecordsAStoreAlertsClearingInItsOwnWords` |

The row for the unlistable parent is skipped when the tests run as root.

## The reverts

Every revert is a case of `revert-runs.py` in this directory: it applies one
temporary edit (the exact old and new text are in the script), runs the row,
prints the command and the first failing line of that run, and restores the
file. Two cases mutate the fixture, not product code, and say so. The run
below is one run of the script from the worktree's root, under the host lock
(`gate-lock.sh trainfix --`) and between two `date -u` lines, after the last
change to any row:

    python3 docs/v3-spike/320-train-escape-legacy/revert-runs.py

It started 2026-11-11T01:23:02Z and answered "30 cases, 30 caught, 0 missed",
exit 0. Its output is `revert-runs.txt` in this directory, without the lock's
waiting lines. Each case's command below is the one the script ran for that
case. A first failing line longer than this page is cut here and marked
"[...]"; the whole line is in `revert-runs.txt`.

1. Row E1: fixture mutation: the fake prints escape bytes without the flag. The edit is in `internal/mergetrain/fakegh_test.go`.
   Command: `go test ./internal/mergetrain -run '^TestFakeGHRefusesADiffWithEscapeBytesUnlessAllowed$' -count=1`
   First failing line: `escape_legacy_320_test.go:60: escape bytes without the flag were not refused in gh's words: out="diff --git a/evidence/run.log b/evidence/run.log\nnew file mode 100644\nindex 0000000..15ce85c\n--- /dev/null\n+++ b/evidence/run.log\n@@ -0,0 +1,2 @@\n+\x1b[32mok\x1b[0m  \tpkg/a\t0.2s\n+\x1b[31mFAIL\x1b[0m\tpkg/b\t0.1s\n" stderr="" [...]`
2. Row E1: fixture mutation: the fake's older gh accepts the flag. The edit is in `internal/mergetrain/fakegh_test.go`.
   Command: `go test ./internal/mergetrain -run '^TestFakeGHRefusesADiffWithEscapeBytesUnlessAllowed$' -count=1`
   First failing line: `escape_legacy_320_test.go:70: an older gh knows no such flag: out="diff --git a/evidence/run.log b/evidence/run.log\nnew file mode 100644\nindex 0000000..15ce85c\n--- /dev/null\n+++ b/evidence/run.log\n@@ -0,0 +1,2 @@\n+\x1b[32mok\x1b[0m  \tpkg/a\t0.2s\n+\x1b[31mFAIL\x1b[0m\tpkg/b\t0.1s\n" stderr="" err=<nil>`
3. Row E2: the flag dropped from the diff read. The edit is in `internal/mergetrain/gh.go`.
   Command: `go test ./internal/mergetrain -run '^TestBehindPRWithEscapeBytesInItsDiffIsRebasedAndMerged$' -count=1`
   First failing line: `escape_legacy_320_test.go:142: #6 was not rebased and merged: status skipped; force pushes recorded 0; head unchanged true; task bound to its old head true; audit: reconcile_read_failed: GitHub could not read PR #6 while proving its reviewed patch before mutation: prove the reviewed patch before updating #6: gh api repos/owner/f [...]`
4. Row E3: the retry removed. The edit is in `internal/mergetrain/gh.go`.
   Command: `go test ./internal/mergetrain -run '^TestAnOlderGHThatDoesNotKnowTheFlagIsReadOnceMoreWithoutIt$' -count=1`
   First failing line: `escape_legacy_320_test.go:169: #6 was not rebased and merged: status skipped; force pushes recorded 0; head unchanged true; task bound to its old head true; audit: reconcile_read_failed: GitHub could not read PR #6 while proving its reviewed patch before mutation: prove the reviewed patch before updating #6: gh api repos/owner/f [...]`
5. Row E4: the retry on any error. The edit is in `internal/mergetrain/gh.go`.
   Command: `go test ./internal/mergetrain -run '^TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead$/^HTTP_502$' -count=1`
   First failing line: `escape_legacy_320_test.go:217: diff reads: want 1 (the first with the flag, a second without), got 2: [["api" "repos/owner/fixture/compare/5150ebbbdb84e12b3cacfee60638f51051cfb431...9e3347043a8181c22b63fcbc908d5fab475a35c4" "-H" "Accept: application/vnd.github.diff" "--allow-escape-sequences"] ["api" "repos/owner/fixture/compare [...]`
6. Row E4: the retry on any error (a diff too large). The edit is in `internal/mergetrain/gh.go`.
   Command: `go test ./internal/mergetrain -run '^TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead$/^a_diff_too_large$' -count=1`
   First failing line: `escape_legacy_320_test.go:217: diff reads: want 1 (the first with the flag, a second without), got 2: [["api" "repos/owner/fixture/compare/a860731fae52f126d0e2703a4bdae3ed64455d7c...4bd46f6c7a867200dc55f4b345b75fa3a007ae48" "-H" "Accept: application/vnd.github.diff" "--allow-escape-sequences"] ["api" "repos/owner/fixture/compare [...]`
7. Row E4: the retry on any error (gh's own refusal of the bytes). The edit is in `internal/mergetrain/gh.go`.
   Command: `go test ./internal/mergetrain -run '^TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead$/^gh'"'"'s_own_refusal_of_the_bytes$' -count=1`
   First failing line: `escape_legacy_320_test.go:217: diff reads: want 1 (the first with the flag, a second without), got 2: [["api" "repos/owner/fixture/compare/031da3d1af293a0d3b0395f6ad8a0d347b034a47...ef1f6b138043fa425cda962fadccb92a2f03c7bb" "-H" "Accept: application/vnd.github.diff" "--allow-escape-sequences"] ["api" "repos/owner/fixture/compare [...]`
8. Row E4: a failed second read returns the first read's error. The edit is in `internal/mergetrain/gh.go`.
   Command: `go test ./internal/mergetrain -run '^TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead$/^an_unknown_flag,_then_HTTP_502$' -count=1`
   First failing line: `escape_legacy_320_test.go:226: the error returned: want gh's "gh: Bad Gateway (HTTP 502)", got gh api repos/owner/fixture/compare/1bc5af1b859231a3d26dda113a126ff2e16c2930...0cc0a927f50a48dbda3e6643f9ce4b925dcb7f56 -H: exit status 1: unknown flag: --allow-escape-sequences`
9. Row L1: the skip removed. The edit is in `internal/mergetrain/drift.go`.
   Command: `go test ./internal/mergetrain -run '^TestDriftSkipsALegacyGateRecordWithOneLine$' -count=1`
   First failing line: `escape_legacy_320_test.go:322: run 1: a legacy record beside an accepted release gate raised an alert: <nil> [{ID:gate-store-unreadable-53cece2e14f5 Repository:owner/app Kind:gate_store_unreadable Name:/var/folders/gx/wg9xtfn96wx9z4gkb9mp_hxr0000gn/T/TestDriftSkipsALegacyGateRecordWithOneLine3089159978/001/v320/extensions/amq-sq [...]`
10. Row L2: the skip applied to every load error (a known kind with a bad target). The edit is in `internal/mergetrain/drift.go`.
   Command: `go test ./internal/mergetrain -run '^TestDriftStillNamesAMalformedGateRecord$/^a_known_kind_with_a_bad_target$' -count=1`
   First failing line: `escape_legacy_320_test.go:363: a malformed record raised no alert naming it: <nil> [], on disk []`
11. Row L2: the skip applied to every load error (a legacy kind under another gate id). The edit is in `internal/mergetrain/drift.go`.
   Command: `go test ./internal/mergetrain -run '^TestDriftStillNamesAMalformedGateRecord$/^a_legacy_kind_under_another_gate_id$' -count=1`
   First failing line: `escape_legacy_320_test.go:363: a malformed record raised no alert naming it: <nil> [], on disk []`
12. Row L2: the skip applied to every load error (torn JSON, the earlier row). The edit is in `internal/mergetrain/drift.go`.
   Command: `go test ./internal/mergetrain -run '^TestDriftNamesAGateRecordThatDoesNotLoad$' -count=1`
   First failing line: `brief3112_test.go:196: a torn gate record: <nil> []`
13. Row L3: the line dropped by the drift reader. The edit is in `internal/mergetrain/drift.go`.
   Command: `go test ./internal/mergetrain -run '^TestTrainNotesASkippedLegacyGateRecord$' -count=1`
   First failing line: `escape_legacy_320_test.go:441: run 1: the result's notes: []`
14. Row L3: the line dropped by the drift reader (the drift row). The edit is in `internal/mergetrain/drift.go`.
   Command: `go test ./internal/mergetrain -run '^TestDriftSkipsALegacyGateRecordWithOneLine$' -count=1`
   First failing line: `escape_legacy_320_test.go:325: run 1: the legacy line, once, naming the kind and the gate id: []`
15. Row L3: the line not carried into the train's result. The edit is in `internal/mergetrain/train.go`.
   Command: `go test ./internal/mergetrain -run '^TestTrainNotesASkippedLegacyGateRecord$' -count=1`
   First failing line: `escape_legacy_320_test.go:441: run 1: the result's notes: []`
16. Row L3: the note entry not journaled. The edit is in `internal/mergetrain/train.go`.
   Command: `go test ./internal/mergetrain -run '^TestTrainNotesASkippedLegacyGateRecord$' -count=1`
   First failing line: `escape_legacy_320_test.go:448: run 1: one note entry a run: []`
17. Row L3: notes encoded when empty (omitempty dropped). The edit is in `internal/mergetrain/train.go`.
   Command: `go test ./internal/mergetrain -run '^TestTrainNotesASkippedLegacyGateRecord$' -count=1`
   First failing line: `escape_legacy_320_test.go:434: a run with no legacy record encodes a notes field: {"run":"mt-20261111T012328Z-7ccda71c","repository":"owner/fixture","base":"main","base_sha":"7cab0e493beb2b7133ffe567a4c8d41919e93663","dry_run":true,"manual":false,"protection":{"required_checks":[{"context":"ci","app_id":15368}],"direct_push_refu [...]`
18. Row L3: the line not printed (the train's dry run). The edit is in `internal/cli/merge_train.go`.
   Command: `go test ./internal/mergetrain -run '^TestTrainDryRunPrintsASkippedLegacyGateRecordOnce$' -count=1`
   First failing line: `escape_legacy_320_text_test.go:24: the legacy line, once, on its own line after the protection line ("gate clear-0a227ca8d88dc97c65c540a4d0fa18a7 is a legacy quarantine_clear gate (a kind this build no longer carries); skipped"):`
19. Row L3: the line not printed (the print row). The edit is in `internal/cli/merge_train.go`.
   Command: `go test ./internal/cli -run '^TestMergeTrainPrintsEachNoteOnItsOwnLineAfterTheProtectionLine$' -count=1`
   First failing line: `merge_train_test.go:60: the notes, one a line, after the protection line:`
20. Row L3: the note kind's words removed from the pi table. The edit is in `pi/extensions/lib/words.ts`.
   Command: `cd pi && node --test test/words.test.ts`
   First failing line: `not ok 1 - row 7: every wait and block reason in Go's sources maps to plain words in the one table | error: 'train entry kind note has no words'`
21. Row L4: no removal (a). The edit is in `internal/mergetrain/drift.go`.
   Command: `go test ./internal/mergetrain -run '^TestDriftClearsAStoreAlertOnlyForAPathThisRunRead$/^a_legacy_record_this_run_skipped$' -count=1`
   First failing line: `escape_legacy_320_test.go:555: the alert was not cleared in its own words ("the record is a legacy kind and is skipped"): resolved [], on disk [{ID:gate-store-unreadable-698f2b6be6e9 Repository:owner/app Kind:gate_store_unreadable Name:/var/folders/gx/wg9xtfn96wx9z4gkb9mp_hxr0000gn/T/TestDriftClearsAStoreAlertOnlyForAPathThisRun [...]`
22. Row L4: no removal (b). The edit is in `internal/mergetrain/drift.go`.
   Command: `go test ./internal/mergetrain -run '^TestDriftClearsAStoreAlertOnlyForAPathThisRunRead$/^a_record_this_run_loaded$' -count=1`
   First failing line: `escape_legacy_320_test.go:555: the alert was not cleared in its own words ("the record now loads"): resolved [], on disk [{ID:gate-store-unreadable-5db0f275bb12 Repository:owner/app Kind:gate_store_unreadable Name:/var/folders/gx/wg9xtfn96wx9z4gkb9mp_hxr0000gn/T/TestDriftClearsAStoreAlertOnlyForAPathThisRunReada_record_this_2763 [...]`
23. Row L4: no removal (a gates directory). The edit is in `internal/mergetrain/drift.go`.
   Command: `go test ./internal/mergetrain -run '^TestDriftClearsAStoreAlertOnlyForAPathThisRunRead$/^a_gates_directory_this_run_listed$' -count=1`
   First failing line: `escape_legacy_320_test.go:555: the alert was not cleared in its own words ("the gate store can be listed again"): resolved [], on disk [{ID:gate-store-unreadable-bc92f5db6dbd Repository:owner/app Kind:gate_store_unreadable Name:/var/folders/gx/wg9xtfn96wx9z4gkb9mp_hxr0000gn/T/TestDriftClearsAStoreAlertOnlyForAPathThisRunReada_ga [...]`
24. Row L4: no removal (the train's entries). The edit is in `internal/mergetrain/drift.go`.
   Command: `go test ./internal/mergetrain -run '^TestTrainRecordsAStoreAlertsClearingInItsOwnWords$' -count=1`
   First failing line: `escape_legacy_320_test.go:583: run 2: <nil> [], on disk [{ID:gate-store-unreadable-7c6fa484291c Repository:owner/fixture Kind:gate_store_unreadable Name:/var/folders/gx/wg9xtfn96wx9z4gkb9mp_hxr0000gn/T/TestTrainRecordsAStoreAlertsClearingInItsOwnWords2610914798/001/root/extensions/amq-squad/gates/clear-0a227ca8d88dc97c65c540a4d0 [...]`
25. Row L4: removal on the negative condition, not flagged this run (c: the record absent). The edit is in `internal/mergetrain/drift.go`.
   Command: `go test ./internal/mergetrain -run '^TestDriftClearsAStoreAlertOnlyForAPathThisRunRead$/^the_record_absent$' -count=1`
   First failing line: `escape_legacy_320_test.go:550: an alert this run had no cause to clear (its path not read, or flagged again) was removed: resolved [{Alert:{ID:gate-store-unreadable-d6f8f3cc0309 Repository:owner/app Kind:gate_store_unreadable Name:/var/folders/gx/wg9xtfn96wx9z4gkb9mp_hxr0000gn/T/TestDriftClearsAStoreAlertOnlyForAPathThisRunReadt [...]`
26. Row L4: removal on the negative condition, not flagged this run (c: its session directory missing). The edit is in `internal/mergetrain/drift.go`.
   Command: `go test ./internal/mergetrain -run '^TestDriftClearsAStoreAlertOnlyForAPathThisRunRead$/^its_session_directory_missing$' -count=1`
   First failing line: `escape_legacy_320_test.go:550: an alert this run had no cause to clear (its path not read, or flagged again) was removed: resolved [{Alert:{ID:gate-store-unreadable-fa0ae2ad31b8 Repository:owner/app Kind:gate_store_unreadable Name:/var/folders/gx/wg9xtfn96wx9z4gkb9mp_hxr0000gn/T/TestDriftClearsAStoreAlertOnlyForAPathThisRunReadi [...]`
27. Row L4: removal on the negative condition, not flagged this run (c: the parent of the sessions unlistable). The edit is in `internal/mergetrain/drift.go`.
   Command: `go test ./internal/mergetrain -run '^TestDriftClearsAStoreAlertOnlyForAPathThisRunRead$/^the_parent_of_the_sessions_unlistable$' -count=1`
   First failing line: `escape_legacy_320_test.go:550: an alert this run had no cause to clear (its path not read, or flagged again) was removed: resolved [{Alert:{ID:gate-store-unreadable-cd8f479cbe53 Repository:owner/app Kind:gate_store_unreadable Name:/var/folders/gx/wg9xtfn96wx9z4gkb9mp_hxr0000gn/T/TestDriftClearsAStoreAlertOnlyForAPathThisRunReadt [...]`
28. Row L4: removal on the negative condition, not flagged this run (a gates directory this run did not list). The edit is in `internal/mergetrain/drift.go`.
   Command: `go test ./internal/mergetrain -run '^TestDriftClearsAStoreAlertOnlyForAPathThisRunRead$/^a_gates_directory_this_run_did_not_list$' -count=1`
   First failing line: `escape_legacy_320_test.go:550: an alert this run had no cause to clear (its path not read, or flagged again) was removed: resolved [{Alert:{ID:gate-store-unreadable-506806101d91 Repository:owner/app Kind:gate_store_unreadable Name:/var/folders/gx/wg9xtfn96wx9z4gkb9mp_hxr0000gn/T/TestDriftClearsAStoreAlertOnlyForAPathThisRunReada [...]`
29. Row L4: removal of every store alert on disk (d: an alert this run raises again). The edit is in `internal/mergetrain/drift.go`.
   Command: `go test ./internal/mergetrain -run '^TestDriftClearsAStoreAlertOnlyForAPathThisRunRead$/^an_alert_this_run_raises_again$' -count=1`
   First failing line: `escape_legacy_320_test.go:550: an alert this run had no cause to clear (its path not read, or flagged again) was removed: resolved [{Alert:{ID:gate-store-unreadable-1cff0c3715fd Repository:owner/app Kind:gate_store_unreadable Name:/var/folders/gx/wg9xtfn96wx9z4gkb9mp_hxr0000gn/T/TestDriftClearsAStoreAlertOnlyForAPathThisRunReada [...]`
30. Row L4: every clearing in the covered alert's words. The edit is in `internal/mergetrain/train.go`.
   Command: `go test ./internal/mergetrain -run '^TestTrainRecordsAStoreAlertsClearingInItsOwnWords$' -count=1`
   First failing line: `escape_legacy_320_test.go:599: the clearing of gate-store-unreadable-f0dafb6f4154, once, in its own words ("the record is a legacy kind and is skipped"): ["a gate store of this squad could not be read, so no gate in it clears an alert; fix or remove the record named below the line\n\n----------\nthe gate store /var/folders/gx/wg [...]`

In cases 3 and 4 the PR was left as the brief asks: no force push recorded by
the fake, the head unchanged, the task still bound to its old head, and gh's
words in the entry.

## Q1's answer: the before state

Q1: three `gate-store-unreadable-*` alert files exist in the v31 session. Does
a run that no longer raises them resolve them?

No. On the base nothing removes a `gate-store-unreadable-*` alert file, also
after its record is removed or replaced by one that loads. Measured on the
unmodified base c65a709c with the probe saved here as `q1_probe_test.go.txt`,
copied to `internal/mergetrain/q1_probe_test.go` and run as:

    gate-lock.sh trainfix -- go test ./internal/mergetrain -run TestQ1Probe -count=1 -v

Its output is `q1-base.txt`. For each of three forms (a torn record then
removed, a torn record then replaced by one that loads, a `quarantine_clear`
record then removed), runs 1 and 2 with the record in place returned the one
alert and left its file on disk; runs 3 and 4, with the record no longer
flagged, returned no alert, returned nothing as resolved, and left the same
file on disk. The last line of the form with the legacy record:

    a quarantine_clear record, then removed: run 4, the record no longer flagged: err=<nil> returned=[] resolved=[] on disk=[gate_store_unreadable gate-store-unreadable-ddba9a352002]

The resolving defer of `DriftResolved` on the base removed only ids it had
covered this run, which are `ungated-*`. The alert's own words ("fix or remove
the record named below the line") therefore led to nothing.

No alert file was edited by hand anywhere, and the v31 session's files were
not read or touched. That the next train run there clears its three alerts
rests on row L4 (a), whose planted alert has the id and the words `drift.go`
writes; it was not measured on the real files.

## The doctor's check: measured

The reviewer struck "and doctor check" from the release note unless the doctor
raising the alert was measured. It was measured, with the probe saved here as
`doctor_probe_test.go.txt`, copied to `internal/cli/doctor_probe_test.go` and
run as:

    gate-lock.sh trainfix -- go test ./internal/cli -run 'TestProbeDoctor' -count=1 -v

The probe calls `doctorReleaseDrift` three times over a root whose own gate
store holds one `quarantine_clear` record. On the unmodified base
(`doctor-base.txt`) run 1 wrote the baseline and answered ok; runs 2 and 3
answered `status=fail` with "1 without an accepted owner gate (owner alert
raised): gate_store_unreadable .../gates/clear-0a227ca8d88dc97c65c540a4d0fa18a7.json",
and the alert file `gate-store-unreadable-b44fc04f83a6` was on disk. On this
branch (`doctor-after.txt`) all three runs answered ok and nothing was on
disk. The doctor's check prints no line for the record.

The "Release note" section below is the reviewer's text, unchanged: whether
the sentence gains "and doctor check" is the reviewer's ruling.

## The train result's JSON and the contract docs

The reviewer's condition: a contract doc that describes the train result's
JSON gains the field in the same PR. None does. The result's field names are
in no doc outside `docs/v3-spike/`:

    git grep -l -e protection_met -e ci_checks -e wall_ms c65a709c -- . ':!docs/v3-spike'

answers `internal/mergetrain/train.go` and three files of `internal/routing`
(the outcome record's own `wall_ms`). `docs/status-contract.md:47` describes
the status contract's `train` block, read from the audit log (`runs[]` and
`alerts[]`), not the train's result. So no doc gains the field.

## Limits, and what is not as the brief wrote it

1. The other gh call sites in `internal/mergetrain` count 18, not the 16 the
   lead read (the command is under the 3.2.x items).
2. On this base a failed proof before the update does not end the run:
   `preMutationReadFailure` (`train.go:1004-1018`) journals
   `reconcile_read_failed`, the PR is skipped and the run goes on. Reverted,
   rows E2 and E3 show exactly that (cases 3 and 4): status skipped, gh's
   words in the entry.
3. The removal is in the drift reader, so a `doctor` run that reads the record
   also removes the alert, and writes no `alert_cleared` entry. That is the
   limit `docs/v3.1.1-release-notes.md:598` already records for alerts a gate
   covers. Read, with no row of its own.
4. The first run for a repository writes the baseline and returns before any
   store is read (`drift.go:146-148`), so it notes nothing and clears nothing.
   Read, with no row of its own.
5. An alert that names a session root or the parent of the sessions is never
   removed: the pass records no such path as read. Read, with no row of its
   own.
6. There are more rows and reverts than the lettered ones: two fixture
   mutations for E1, the failed second read for E4, the result, the audit
   entry, the `omitempty` and the two printed forms for L3, the store-level
   alert and the unconditional removal for L4, and the words of each clearing.
7. `LegacyDryRuns` is an exported helper in a `_test.go` file: the package's
   external tests print a real dry run's result through `internal/cli`, which
   this package cannot import.
8. `docs/v3-spike/311-evidence/311-followups-revert-runs.py:162` quotes the
   `alert_cleared` line of `train.go` as it was; that line is rewritten here,
   so that case of that script no longer finds its text. The file is outside
   this PR's declared set and is not changed.
9. The kind list in the comment above `Entry` (`internal/mergetrain/audit.go:56-66`)
   does not name `note`. It did not name `alert` or `alert_cleared` either,
   and the file is outside the declared set.

## 3.2.x items

- "escape bytes in a title or body: gh refuses today; a sanitised path before
  any flag". The other gh call sites: 18 in `internal/mergetrain/gh.go` (19
  on the base with the diff read) and 12 in `internal/releaseflow/gh.go`,
  counted on the base with
  `git grep -c 'g\.run(ctx' c65a709c -- internal/mergetrain internal/releaseflow`.
  None of them passes the flag.
- redact-evidence passes escape bytes in evidence logs unflagged: strip or
  flag. This is the lead's item; this PR did not measure it.
- The alert's words overstate a per-record failure: "a gate store of this
  squad could not be read, so no gate in it clears an alert" (`drift.go:172`)
  is written also when one record does not load and every other gate in the
  store still clears.

## Release note

Fixed: the merge train stopped before updating a PR whose diff contains terminal escape bytes (a captured log, for example), on gh 2.100's refusal to print them. The train now reads that one diff with `--allow-escape-sequences`; the bytes go only to `git patch-id`.
Fixed: a gate record of a kind this build no longer carries (a 3.0.x `quarantine_clear`) raised a `gate_store_unreadable` alert on every train run. The train now skips it with one line, as `gate status` and `status` already did. An alert that record left behind is cleared by the next train run that reads the record.
