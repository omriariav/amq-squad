#!/usr/bin/env python3
"""Revert check for the 3-2 fix commit (Codex pass on 700b1940)."""
import subprocess, sys, os

W = "/Users/omri/code/amq-squad/.claude/worktrees/agent-a83ff47e3e447b571"
MT, CLI, SE, RF, LG, GR = "./internal/mergetrain/", "./internal/cli/", "./internal/seatexec/", "./internal/releaseflow/", "./internal/ledger/", "./internal/grants/"
REVERTS = [
    ("P1-1 app-bound check", "internal/mergetrain/checks.go", "r.Name == req.Context && r.AppID == req.AppID && (latest == nil", "r.Name == req.Context && (latest == nil", MT, "TestRequiredCheckIsBoundToItsAppAndLatestResult"),
    ("P1-1 no-app rule needs every result green", "internal/mergetrain/checks.go", 'if s := stateOf(r); state == "absent" || state == "success" {', 'if s := stateOf(r); state != "success" {', MT, "TestRequiredCheckIsBoundToItsAppAndLatestResult"),
    ("P1-2 bypassable ruleset checks", "internal/mergetrain/gh.go", "\t\t\tif !enforced {\n\t\t\t\tp.Notes = append(p.Notes, src+\" has bypass actors: its required checks", "\t\t\tif false && !enforced {\n\t\t\t\tp.Notes = append(p.Notes, src+\" has bypass actors: its required checks", MT, "TestBypassableProtectionIsUnmet"),
    ("P1-2 classic without enforce_admins", "internal/mergetrain/gh.go", "\t\t\tif !admins {", "\t\t\tif false && !admins {", MT, "TestBypassableProtectionIsUnmet"),
    ("P1-3 head re-read after the checks", "internal/mergetrain/train.go", "\t\tif again.HeadSHA == pr.HeadSHA {\n\t\t\tbreak", "\t\tif again.HeadSHA == pr.HeadSHA || true {\n\t\t\tbreak", MT, "TestHeadMovingDuringTheCheckIsReadAgain"),
    ("P1-4 real run takes declared files only from the store", "internal/cli/merge_train.go", "\t\tcase len(it.DeclaredFiles) > 0:", "\t\tcase false:", CLI, "TestMergeTrainRealRunTakesDeclaredFilesOnlyFromTheTaskStore"),
    ("P1-5 never-needed names stay scrubbed when declared", "internal/seatexec/env.go", "if Scrubbed(name) && (!keep[name] || NeverNeeded(name)) {", "if Scrubbed(name) && !keep[name] {", SE, "TestGitConfigFamilyAndGHTokensNeverReachASeat"),
    ("P1-5 GIT_CONFIG_ family", "internal/seatexec/env.go", 'return neverNeeded[upper] || strings.HasPrefix(upper, "GIT_CONFIG_")', "return neverNeeded[upper]", SE, "TestGitConfigFamilyAndGHTokensNeverReachASeat"),
    ("P1-6 moved tag refused before the release", "internal/releaseflow/releaseflow.go", 'if remote != "" && remote != approved {', 'if false && remote != "" && remote != approved {', RF, "TestReleaseRefusesAMovedTag"),
    ("P1-7 argv element after a secret flag", "internal/ledger/ledger.go", "i > 0 && ok && secretFlag(prev) {", "i > 0 && ok && false && secretFlag(prev) {", LG, "TestRedactArgvQuotesAndNumbers"),
    ("P1-7 only count fields keep numbers", "internal/ledger/ledger.go", "SecretName(k) && !(isNumber && countField(k)) {", "SecretName(k) && !isNumber {", LG, "TestRedactArgvQuotesAndNumbers"),
    ("P1-7 quoted words", "internal/ledger/ledger.go", "\t\tcase c == '\\'' || c == '\"':\n", "\t\tcase false && (c == '\\'' || c == '\"'):\n", LG, "TestRedactArgvQuotesAndNumbers"),
    ("P2-8 case-folded protected paths", "internal/mergetrain/checks.go", "\tp = strings.ToLower(p) // case-folded", "\tp = strings.TrimSpace(p) // case-folded", MT, "TestProtectedPathsAreCaseFolded"),
    ("P2-9 confirmation after the final check", "internal/mergetrain/train.go", "\tif t.Manual && (t.Confirm == nil || !t.Confirm(Step{Index: index,", "\tif false && t.Manual && (t.Confirm == nil || !t.Confirm(Step{Index: index,", MT, "TestManualConfirmationIsForTheHeadThatMerges"),
    ("P2-10 retry reconciles an existing tag", "internal/releaseflow/releaseflow.go", "\t\t\tif remote == \"\" {\n\t\t\t\terr = f.Exec.Tag(", "\t\t\tif true {\n\t\t\t\terr = f.Exec.Tag(", RF, "TestRetryAfterACrashReconcilesTheTag"),
    ("P2-11 audit errors propagate", "internal/mergetrain/train.go", "if err != nil && auditErr == nil {", "if false && err != nil && auditErr == nil {", MT, "TestAuditFailureStopsBeforeAnyMerge"),
    ("P2-12 one gate covers one object", "internal/mergetrain/drift.go", "ok && bindCovered(dir, id, object) {", "ok && (bindCovered(dir, id, object) || true) {", MT, "TestDriftBindsAGateToOneReleaseAndOneTagCommit"),
    ("P2-13 standalone credential helpers", "internal/cli/hook_guard.go", '\t\tstrings.HasPrefix(filepath.Base(cmd), "git-credential-"),', '\t\tfalse && strings.HasPrefix(filepath.Base(cmd), "git-credential-"),', CLI, "TestGuardRuleMatrix"),
    ("ruling 4 merge kinds refused", "internal/cli/verify_action.go", 'if action == "default_branch_push" || action == "protected_branch_push" {', 'if false && action == "default_branch_push" {', CLI, "TestVerifyActionRefusesMergeKindsForTheTrain"),
    ("ruling 6 required_companions field", "internal/grants/parse.go", '\t\t\tcase "merge.required_companions":\n\t\t\t\tm.RequiredCompanions, err = f.list()\n', "", GR, "TestMergeSizeCapDefaultsAndNarrowing"),
    ("squad identity matched", "internal/mergetrain/escalate.go", 'return squad == "" || rec.Squad == "" || rec.Squad == squad', 'return squad == "" || rec.Squad == "" || rec.Squad == squad || true', MT, "TestGatesCarryAndMatchTheSquadIdentity"),
    ("squad identity matched in the release flow", "internal/mergetrain/escalate.go", 'return squad == "" || rec.Squad == "" || rec.Squad == squad', 'return squad == "" || rec.Squad == "" || rec.Squad == squad || true', RF, "TestReleaseGatesCarryTheSquadIdentity"),
    ("squad identity set on raise", "internal/mergetrain/escalate.go", "\t\tspec.Squad = e.Squad\n", "", MT, "TestGatesCarryAndMatchTheSquadIdentity"),
]


def main():
    out = open(sys.argv[1], "w")
    bad = []
    for name, rel, old, new, pkg, pat in REVERTS:
        path = os.path.join(W, rel)
        orig = open(path).read()
        if orig.count(old) != 1:
            out.write(f"## {name}: SETUP ERROR, anchor found {orig.count(old)} times in {rel}\n\n")
            bad.append(name)
            continue
        open(path, "w").write(orig.replace(old, new))
        try:
            r = subprocess.run(["go", "test", "-count=1", "-run", pat, pkg], cwd=W, capture_output=True, text=True)
        finally:
            open(path, "w").write(orig)
        text = r.stdout + r.stderr
        assertion = r.returncode != 0 and "build failed" not in text and "[setup failed]" not in text
        verdict = "FAILS on its assertion with the fix reverted (good)" if assertion else ("BUILD FAILURE (bad revert)" if r.returncode != 0 else "PASSES with the fix reverted (BAD)")
        if not assertion:
            bad.append(name)
        out.write(f"## {name}: revert {rel}; go test -run '{pat}' {pkg}: {verdict}\n-- reverted: {old!r}\n-- to:       {new!r}\n{text}\n")
        r2 = subprocess.run(["go", "test", "-count=1", "-run", pat, pkg], cwd=W, capture_output=True, text=True)
        out.write(f"-- restored: exit {r2.returncode}\n{r2.stdout.strip()[-300:]}\n\n")
        if r2.returncode != 0:
            bad.append(name + " (restored run failed)")
    out.write("SUMMARY: " + ("every row fails on its assertion when reverted and passes restored" if not bad else "problems: " + ", ".join(bad)) + "\n")
    print("problems:", bad)


main()
