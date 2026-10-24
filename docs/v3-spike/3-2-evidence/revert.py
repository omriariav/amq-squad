#!/usr/bin/env python3
"""Revert runs for brief 3-2: undo one fix, run its test row, confirm it fails on
its assertion, restore. usage: revert.py <out file> [name ...]"""
import subprocess, sys, os

W = "/Users/omri/code/amq-squad/.claude/worktrees/agent-a83ff47e3e447b571"

REVERTS = [
    # name, file, old, new, package, test pattern
    ("ci_green", "internal/mergetrain/checks.go",
     "if why, _ := ciRed(in.required, in.runs); why != \"\" {", "if why, _ := ciRed(in.required, in.runs); false && why != \"\" {",
     "./internal/mergetrain/", "TestAcceptance2Refusals/ci_red"),
    ("size_cap_lines", "internal/mergetrain/checks.go",
     "ok && n > merge.SizeCapLines {", "ok && n > merge.SizeCapLines*1000 {",
     "./internal/mergetrain/", "TestAcceptance2Refusals/size_cap$"),
    ("size_cap_files", "internal/mergetrain/checks.go",
     "ok && n > merge.SizeCapFiles {", "ok && n > merge.SizeCapFiles*1000 {",
     "./internal/mergetrain/", "TestAcceptance2Refusals/size_cap_files"),
    ("force_push", "internal/mergetrain/checks.go",
     "if !in.trainHeads[fp.After] {", "if false && !in.trainHeads[fp.After] {",
     "./internal/mergetrain/", "TestAcceptance2Refusals/force_push"),
    ("protected_path", "internal/mergetrain/checks.go",
     "if e, hit := protectedHit(p, entries); hit {", "if e, hit := protectedHit(p, entries); false && hit {",
     "./internal/mergetrain/", "TestAcceptance2Refusals/protected_path"),
    ("base_branch", "internal/mergetrain/checks.go",
     "case in.pr.BaseRef != in.base:", "case false && in.pr.BaseRef != in.base:",
     "./internal/mergetrain/", "TestAcceptance2Refusals/wrong_base"),
    ("task_scope", "internal/mergetrain/checks.go",
     "if len(in.item.DeclaredFiles) > 0 && !Declared(p, in.item.DeclaredFiles) {", "if false && len(in.item.DeclaredFiles) > 0 && !Declared(p, in.item.DeclaredFiles) {",
     "./internal/mergetrain/", "TestAcceptance2Refusals/file_outside_the_task"),
    ("protection", "internal/mergetrain/forge.go",
     "func (p Protection) Met() bool { return len(p.RequiredChecks) > 0 && p.DirectPushRefused }", "func (p Protection) Met() bool { return true }",
     "./internal/mergetrain/", "TestAcceptance5NoRulesetRefusesToStart"),
    ("protection_bypass", "internal/mergetrain/gh.go",
     "if bypass[r.RulesetID] == 0 {", "if bypass[r.RulesetID] >= 0 {",
     "./internal/mergetrain/", "TestEvaluateProtection"),
    ("rebase_redigest", "internal/mergetrain/train.go",
     "\tif moved {\n\t\tt.digest(res, entry)", "\tif false && moved {\n\t\tt.digest(res, entry)",
     "./internal/mergetrain/", "TestAcceptance1FiveCleanPRsLandInOrder"),
    ("rebase_recheck", "internal/mergetrain/train.go",
     "\tfresh, err := t.checkPR(ctx, req, it, res.CIChecks)\n\tif err != nil {\n\t\treturn err\n\t}\n\tfresh.After",
     "\tfresh, err := t.checkPR(ctx, req, it, res.CIChecks)\n\tif err != nil {\n\t\treturn err\n\t}\n\tfresh.Refusals = nil\n\tfresh.After",
     "./internal/mergetrain/", "TestNewHeadAfterPlanIsRechecked"),
    ("seat_no_merge_path", "internal/mergetrain/train.go",
     "if t.InSeat && !t.DryRun {", "if false && t.InSeat && !t.DryRun {",
     "./internal/mergetrain/", "TestSeatProcessHoldsNoMergePath"),
    ("dry_run_raises_nothing", "internal/mergetrain/train.go",
     "\tcase t.DryRun:\n", "\tcase false:\n",
     "./internal/mergetrain/", "TestDryRunMergesAndRaisesNothing"),
    ("custody_askpass", "internal/seatexec/env.go",
     "\"GIT_ASKPASS\": true, \"SSH_ASKPASS\": true,", "",
     "./internal/mergetrain/", "TestSeatEnvironmentCarriesNoForgeCredential"),
    ("codex_approval", "internal/seatexec/seatexec_unix.go",
     "if approval != \"on-request\" {", "if false && approval != \"on-request\" {",
     "./internal/seatexec/", "TestCodexSeatRequiresApprovalOnRequest"),
    ("guard_credential_read", "internal/cli/hook_guard.go",
     "if a.credRead != \"\" {", "if false && a.credRead != \"\" {",
     "./internal/cli/", "TestGuardRuleMatrix"),
]


def main():
    out = open(sys.argv[1], "w")
    names = set(sys.argv[2:])
    failed_to_fail = []
    for name, rel, old, new, pkg, pat in REVERTS:
        if names and name not in names:
            continue
        path = os.path.join(W, rel)
        orig = open(path).read()
        if orig.count(old) != 1:
            out.write(f"## {name}: SETUP ERROR, anchor found {orig.count(old)} times in {rel}\n")
            failed_to_fail.append(name)
            continue
        open(path, "w").write(orig.replace(old, new))
        try:
            r = subprocess.run(["go", "test", "-count=1", "-run", pat, pkg], cwd=W, capture_output=True, text=True)
        finally:
            open(path, "w").write(orig)
        verdict = "FAILS with the fix reverted (good)" if r.returncode != 0 else "PASSES with the fix reverted (BAD)"
        if r.returncode == 0:
            failed_to_fail.append(name)
        out.write(f"## {name}: revert {rel}; go test -run '{pat}' {pkg}: {verdict}\n")
        out.write(f"-- reverted: {old!r}\n-- to:       {new!r}\n")
        out.write(r.stdout + r.stderr + "\n")
        # confirm restored
        r2 = subprocess.run(["go", "test", "-count=1", "-run", pat, pkg], cwd=W, capture_output=True, text=True)
        out.write(f"-- restored: exit {r2.returncode}\n{r2.stdout.strip()[-400:]}\n\n")
        if r2.returncode != 0:
            failed_to_fail.append(name + " (restored run failed)")
    out.write("SUMMARY: " + ("all reverted rows failed and pass restored" if not failed_to_fail else "problems: " + ", ".join(failed_to_fail)) + "\n")
    print("problems:", failed_to_fail)


main()
