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
    ("release_tag_unverified", "internal/releaseflow/releaseflow.go",
     "if err := f.Accepted(rec); err != nil {", "if err := f.Accepted(rec); false && err != nil {",
     "./internal/releaseflow/", "TestAcceptance6ReleaseFlow"),
    ("release_runs_once", "internal/releaseflow/releaseflow.go",
     "if done, err := os.ReadFile(marker); err == nil {", "if done, err := os.ReadFile(marker); false && err == nil {",
     "./internal/releaseflow/", "TestAcceptance6ReleaseFlow"),
    ("release_foreign_gate", "internal/releaseflow/releaseflow.go",
     "\t\tif rec.Class != child.Action || rec.Target != child.Target {\n\t\t\treturn id, fmt.Errorf(", "\t\tif false {\n\t\t\treturn id, fmt.Errorf(",
     "./internal/releaseflow/", "TestPrepareIsIdempotentAndRefusesAForeignGate"),
    ("reconcile_conflict", "internal/compoundrelease/reconcile.go",
     "if errors.Is(err, ErrChildConflict) {", "if false && errors.Is(err, ErrChildConflict) {",
     "./internal/compoundrelease/", "TestReconcileTransportFailureAndForeignGate"),
    ("reconcile_sending_retry", "internal/compoundrelease/reconcile.go",
     "\t\tcase childPublicationSending:\n\t\tdefault:", "\t\tdefault:",
     "./internal/compoundrelease/", "TestReconcileFaultSeamsConvergeOnOneGatePerChild|TestReconcileTransportFailureAndForeignGate"),
    ("ledger_secret_names", "internal/ledger/ledger.go",
     "if _, isNumber := val.(json.Number); SecretName(k) && !isNumber {", "if _, isNumber := val.(json.Number); false && SecretName(k) && !isNumber {",
     "./internal/ledger/", "TestRedactKeysAndArguments|TestOutcomesNameTheTask"),
    ("ledger_secret_args", "internal/ledger/ledger.go",
     "\t\tif hit {\n\t\t\twords[i], changed", "\t\tif false && hit {\n\t\t\twords[i], changed",
     "./internal/ledger/", "TestRedactKeysAndArguments"),
    ("ledger_task_window", "internal/ledger/ledger.go",
     "if perr == nil && !at.Before(w.from) && !at.After(w.to) {", "if perr == nil && (at.IsZero() || true) {",
     "./internal/ledger/", "TestForTaskKeepsLeaseWindowsOnly"),
    ("verify_merge_digest", "internal/cli/verify.go",
     "case last.Head != batchHead:", "case false && last.Head != batchHead:",
     "./internal/cli/", "TestVerifyMergeReadsTheAuditLog"),
    ("verify_release_gates", "internal/cli/verify.go",
     "if d := gates.Decision(st.GateID); d != gatestore.StateAccepted {", "if d := gates.Decision(st.GateID); false && d != gatestore.StateAccepted {",
     "./internal/cli/", "TestVerifyReleaseReadsTheFlowRecords"),
    ("drift_alerts", "internal/mergetrain/drift.go",
     "\t\tfor _, t := range targets {\n\t\t\tif accepted[t] {", "\t\tfor _, t := range targets {\n\t\t\tif accepted[t] || true {",
     "./internal/mergetrain/", "TestDriftAlertsOnUngatedTagsAndReleases"),
    ("drift_gate_covers", "internal/mergetrain/drift.go",
     "\t\tfor _, t := range targets {\n\t\t\tif accepted[t] {", "\t\tfor _, t := range targets {\n\t\t\tif false && accepted[t] {",
     "./internal/mergetrain/", "TestDriftAlertsOnUngatedTagsAndReleases"),
    ("status_owner_alert", "internal/status/classify.go",
     "\tfor _, a := range in.Alerts {\n", "\tfor _, a := range in.Alerts[:0] {\n",
     "./internal/status/", "TestOwnerAlertBecomesNeedsYouAttention"),
    ("doctor_release_drift", "internal/cli/doctor_release_drift.go",
     "\t\tcase len(alerts) > 0:", "\t\tcase false:",
     "./internal/cli/", "TestDoctorReleaseDriftFailsOnAnUngatedTag"),
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
