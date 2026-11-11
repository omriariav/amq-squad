#!/usr/bin/env python3
"""Brief 320-train-escape-legacy: the revert runs.

Each case applies one temporary edit (a product-code revert, or a fixture
mutation where the case says so), runs the named row, records the command
and the first failing line of that run, and restores the file. A case whose
row does not fail is printed as MISSED and the script exits 1.

Run from the repository root, under the host lock where one is in use:

    python3 docs/v3-spike/320-train-escape-legacy/revert-runs.py

It is not run by CI or by any test. The Go cases need go and git, the pi
case needs node and pi/node_modules (npm ci --ignore-scripts in pi/).
"""
import pathlib, re, shlex, subprocess, sys

ROOT = pathlib.Path(__file__).resolve().parents[3]
GH, DRIFT, TRAIN = "internal/mergetrain/gh.go", "internal/mergetrain/drift.go", "internal/mergetrain/train.go"
FAKE, PRINT, WORDS = "internal/mergetrain/fakegh_test.go", "internal/cli/merge_train.go", "pi/extensions/lib/words.ts"

def go(pkg, run):
    return (["go", "test", "./internal/" + pkg, "-run", run, "-count=1"], ".")

MT = lambda run: go("mergetrain", run)
L4 = "^TestDriftClearsAStoreAlertOnlyForAPathThisRunRead$/^%s$"
SKIP = "				if kind, legacy := store.Legacy(id); legacy {\n"
REACHED = "		why, ok := read.reached[a.Name]\n		if !ok {\n			continue\n		}\n"
NEGATIVE = "		why := read.reached[a.Name]\n		if slices.ContainsFunc(read.bad, func(u unreadableStore) bool { return u.path == a.Name }) {\n			continue\n		}\n"
LISTED = "		if fi, err := os.Stat(store.Dir(\"gates\")); err == nil && fi.IsDir() {\n"
RETRY = "		diff, err = g.run(ctx, \"api\", path,\n			\"-H\", \"Accept: application/vnd.github.diff\")\n	}\n"

# (row, what the edit is, file, old, new, command)
CASES = [
    ("E1", "fixture mutation: the fake prints escape bytes without the flag", FAKE,
     'if strings.Contains(diff, "\\x1b") && !s.OldGH && !allowEscape {', "if false {",
     MT("^TestFakeGHRefusesADiffWithEscapeBytesUnlessAllowed$")),
    ("E1", "fixture mutation: the fake's older gh accepts the flag", FAKE,
     "	if s.OldGH && allowEscape {\n", "	if false {\n",
     MT("^TestFakeGHRefusesADiffWithEscapeBytesUnlessAllowed$")),
    ("E2", "the flag dropped from the diff read", GH,
     '"-H", "Accept: application/vnd.github.diff", "--allow-escape-sequences")', '"-H", "Accept: application/vnd.github.diff")',
     MT("^TestBehindPRWithEscapeBytesInItsDiffIsRebasedAndMerged$")),
    ("E3", "the retry removed", GH,
     "	if unknownEscapeFlag(err) {\n", "	if false {\n",
     MT("^TestAnOlderGHThatDoesNotKnowTheFlagIsReadOnceMoreWithoutIt$")),
    ("E4", "the retry on any error", GH,
     "	if unknownEscapeFlag(err) {\n", "	if err != nil {\n",
     MT("^TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead$/^HTTP_502$")),
    ("E4", "the retry on any error (a diff too large)", GH,
     "	if unknownEscapeFlag(err) {\n", "	if err != nil {\n",
     MT("^TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead$/^a_diff_too_large$")),
    ("E4", "the retry on any error (gh's own refusal of the bytes)", GH,
     "	if unknownEscapeFlag(err) {\n", "	if err != nil {\n",
     MT("^TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead$/^gh's_own_refusal_of_the_bytes$")),
    ("E4", "a failed second read returns the first read's error", GH,
     RETRY, "		if again, rerr := g.run(ctx, \"api\", path,\n			\"-H\", \"Accept: application/vnd.github.diff\"); rerr == nil {\n			diff, err = again, nil\n		}\n	}\n",
     MT("^TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead$/^an_unknown_flag,_then_HTTP_502$")),
    ("L1", "the skip removed", DRIFT,
     SKIP, SKIP.replace("legacy {", "legacy && false {"),
     MT("^TestDriftSkipsALegacyGateRecordWithOneLine$")),
    ("L2", "the skip applied to every load error (a known kind with a bad target)", DRIFT,
     SKIP, SKIP.replace("kind, legacy := store.Legacy(id); legacy {", "kind, _ := store.Legacy(id); true {"),
     MT("^TestDriftStillNamesAMalformedGateRecord$/^a_known_kind_with_a_bad_target$")),
    ("L2", "the skip applied to every load error (a legacy kind under another gate id)", DRIFT,
     SKIP, SKIP.replace("kind, legacy := store.Legacy(id); legacy {", "kind, _ := store.Legacy(id); true {"),
     MT("^TestDriftStillNamesAMalformedGateRecord$/^a_legacy_kind_under_another_gate_id$")),
    ("L2", "the skip applied to every load error (torn JSON, the earlier row)", DRIFT,
     SKIP, SKIP.replace("kind, legacy := store.Legacy(id); legacy {", "kind, _ := store.Legacy(id); true {"),
     MT("^TestDriftNamesAGateRecordThatDoesNotLoad$")),
    ("L3", "the line dropped by the drift reader", DRIFT,
     "					notes = append(notes, gatestore.LegacyLine(id, kind))\n", "					_ = kind\n",
     MT("^TestTrainNotesASkippedLegacyGateRecord$")),
    ("L3", "the line dropped by the drift reader (the drift row)", DRIFT,
     "					notes = append(notes, gatestore.LegacyLine(id, kind))\n", "					_ = kind\n",
     MT("^TestDriftSkipsALegacyGateRecordWithOneLine$")),
    ("L3", "the line not carried into the train's result", TRAIN,
     "		res.Alerts, res.Notes = drift.Alerts, drift.Notes\n", "		res.Alerts = drift.Alerts\n",
     MT("^TestTrainNotesASkippedLegacyGateRecord$")),
    ("L3", "the note entry not journaled", TRAIN,
     '			_ = entry(Entry{Kind: "note", Detail: n})\n', "			_ = n\n",
     MT("^TestTrainNotesASkippedLegacyGateRecord$")),
    ("L3", "notes encoded when empty (omitempty dropped)", TRAIN,
     '`json:"notes,omitempty"`', '`json:"notes"`',
     MT("^TestTrainNotesASkippedLegacyGateRecord$")),
    ("L3", "the line not printed (the train's dry run)", PRINT,
     "	for _, n := range r.Notes {\n		fmt.Fprintln(w, n)\n	}\n", "	for _, n := range r.Notes {\n		_ = n\n	}\n",
     MT("^TestTrainDryRunPrintsASkippedLegacyGateRecordOnce$")),
    ("L3", "the line not printed (the print row)", PRINT,
     "	for _, n := range r.Notes {\n		fmt.Fprintln(w, n)\n	}\n", "	for _, n := range r.Notes {\n		_ = n\n	}\n",
     go("cli", "^TestMergeTrainPrintsEachNoteOnItsOwnLineAfterTheProtectionLine$")),
    ("L3", "the note kind's words removed from the pi table", WORDS,
     '	note: "noted a skipped legacy gate record",\n', "",
     (["node", "--test", "test/words.test.ts"], "pi")),
    ("L4", "no removal (a)", DRIFT,
     "	run.Resolved = clearStoreAlerts(dir, read)\n", "",
     MT(L4 % "a_legacy_record_this_run_skipped")),
    ("L4", "no removal (b)", DRIFT,
     "	run.Resolved = clearStoreAlerts(dir, read)\n", "",
     MT(L4 % "a_record_this_run_loaded")),
    ("L4", "no removal (a gates directory)", DRIFT,
     "	run.Resolved = clearStoreAlerts(dir, read)\n", "",
     MT(L4 % "a_gates_directory_this_run_listed")),
    ("L4", "no removal (the train's entries)", DRIFT,
     "	run.Resolved = clearStoreAlerts(dir, read)\n", "",
     MT("^TestTrainRecordsAStoreAlertsClearingInItsOwnWords$")),
    ("L4", "removal on the negative condition, not flagged this run (c: the record absent)", DRIFT,
     REACHED, NEGATIVE, MT(L4 % "the_record_absent")),
    ("L4", "removal on the negative condition, not flagged this run (c: its session directory missing)", DRIFT,
     REACHED, NEGATIVE, MT(L4 % "its_session_directory_missing")),
    ("L4", "removal on the negative condition, not flagged this run (c: the parent of the sessions unlistable)", DRIFT,
     REACHED, NEGATIVE, MT(L4 % "the_parent_of_the_sessions_unlistable")),
    ("L4", "removal on the negative condition, not flagged this run (a gates directory this run did not list)", DRIFT,
     REACHED, NEGATIVE, MT(L4 % "a_gates_directory_this_run_did_not_list")),
    ("L4", "removal of every store alert on disk (d: an alert this run raises again)", DRIFT,
     REACHED, "		why := read.reached[a.Name]\n", MT(L4 % "an_alert_this_run_raises_again")),
    ("L4", "every clearing in the covered alert's words", TRAIN,
     'Detail: a.Detail + "; " + a.Why, GateID: a.ID})', 'Detail: a.Detail + "; a verified owner gate now covers it", GateID: a.ID})',
     MT("^TestTrainRecordsAStoreAlertsClearingInItsOwnWords$")),
    # the #953 fix round
    ("L4", "the gates directory marked as listed whatever the path is, as on b203e8fe (a gates path that is a dangling symlink)", DRIFT,
     LISTED, "		if true {\n", MT(L4 % "a_gates_path_that_is_a_dangling_symlink")),
    ("E4", "M2: any unknown flag answered by a second read (another flag's refusal)", GH,
     'strings.Contains(ge.Stderr, "unknown flag: --allow-escape-sequences")', 'strings.Contains(ge.Stderr, "unknown flag")',
     MT("^TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead$/^another_flag's_refusal$")),
    ("E4", "M3: the read without the flag made up to twice (an unknown flag twice)", GH,
     "	if unknownEscapeFlag(err) {\n", "	for i := 0; i < 2 && unknownEscapeFlag(err); i++ {\n",
     MT("^TestOnlyAnUnknownFlagIsAnsweredByASecondDiffRead$/^an_unknown_flag_twice$")),
]

FIRST = re.compile(r"^\s*(\S+_test\.go:\d+: .*|.*\bAssertionError\b.*|not ok .*|.*\[build failed\].*|\S+\.go:\d+:\d+: .*)$", re.M)
NODE_ERROR = re.compile(r"^\s*(error: .*)$", re.M)

def main():
    missed = 0
    for i, (row, what, path, old, new, (cmd, cwd)) in enumerate(CASES, 1):
        f = ROOT / path
        text = f.read_text()
        if text.count(old) != 1:
            sys.exit(f"case {i} ({row}: {what}): {path} holds the text to revert {text.count(old)} times, not once")
        try:
            f.write_text(text.replace(old, new))
            p = subprocess.run(cmd, cwd=ROOT / cwd, capture_output=True, text=True)
        finally:
            f.write_text(text)
        out = p.stdout + p.stderr
        m = FIRST.search(out)
        built = "[build failed]" not in out and "[setup failed]" not in out
        caught = p.returncode != 0 and built and m is not None
        print(f"case {i} | row {row} | {what}")
        print(f"  file: {path}")
        print(f"  command: {'cd ' + cwd + ' && ' if cwd != '.' else ''}{shlex.join(cmd)}")
        print(f"  result: {'caught' if caught else 'MISSED'} (exit {p.returncode})")
        first = m.group(1).strip() if m else "(none)"
        if first.startswith("not ok") and (e := NODE_ERROR.search(out)):
            first += " | " + e.group(1).strip()  # node's test runner names the assertion below its "not ok" line
        print(f"  first failing line: {first}")
        if not caught:
            missed += 1
            print("  output:\n" + "\n".join("    " + l for l in out.splitlines()[-40:]))
        sys.stdout.flush()
    print(f"{len(CASES)} cases, {len(CASES) - missed} caught, {missed} missed")
    sys.exit(1 if missed else 0)

if __name__ == "__main__":
    main()
