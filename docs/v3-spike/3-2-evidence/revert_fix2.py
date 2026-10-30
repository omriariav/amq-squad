#!/usr/bin/env python3
"""Revert check for the 3-2 fix 2 commit (second Codex pass on 87e45acd)."""
import subprocess, sys, os

W = "/Users/omri/code/amq-squad/.claude/worktrees/agent-a83ff47e3e447b571"
MT, CLI, SE, RF, LG = "./internal/mergetrain/", "./internal/cli/", "./internal/seatexec/", "./internal/releaseflow/", "./internal/ledger/"
REVERTS = [
    ("P1-1 a check naming no app is unmet", "internal/mergetrain/gh.go", "\t\t\t\tif c.IntegrationID <= 0 {", "\t\t\t\tif false && c.IntegrationID <= 0 {", MT, "TestRequiredCheckWithoutAnAppIsUnmet"),
    ("N2 classic contexts add no wildcard", "internal/mergetrain/gh.go", "\t\t\t\tfor _, s := range c.Checks {", "\t\t\t\tfor _, x := range c.Contexts {\n\t\t\t\t\taddCheck(RequiredCheck{Context: x})\n\t\t\t\t}\n\t\t\t\tfor _, s := range c.Checks {", MT, "TestClassicContextsAddNoWildcardCheck"),
    ("P1-2 classic PR bypass allowances", "internal/mergetrain/gh.go", "\t\tcase reviews != nil && reviews.Bypass != nil &&", "\t\tcase false && reviews != nil && reviews.Bypass != nil &&", MT, "TestClassicPRBypassAllowancesAreUnmet"),
    ("P1-4 a PR with no bound task", "internal/mergetrain/checks.go", '\tcase in.item.Unbound != "":', "\tcase false:", MT, "TestRealRunReadsTheTaskBoundToThePR"),
    ("P1-4 pr_head_moved", "internal/mergetrain/checks.go", '\tcase in.item.BoundHead != "" && in.pr.HeadSHA != in.item.BoundHead', '\tcase false && in.item.BoundHead != "" && in.pr.HeadSHA != in.item.BoundHead', MT, "TestRealRunReadsTheTaskBoundToThePR"),
    ("P1-4 a real request names PR numbers only", "internal/cli/merge_train.go", '\t\tcase strings.TrimSpace(it.Task) != "":', "\t\tcase false:", CLI, "TestMergeTrainRealRunNamesPRNumbersOnly"),
    ("P1-5 git transport and trust variables", "internal/seatexec/env.go", '"GIT_SSH_COMMAND": true, ', "", SE, "TestGitTransportAndTrustVariablesNeverReachASeat"),
    ("P1-6a tag rule required", "internal/releaseflow/releaseflow.go", "\t} else if !ok {\n\t\treturn nil, fmt.Errorf(\"%w: %s\", ErrProtectionUnmet, why)", "\t} else if false && !ok {\n\t\treturn nil, fmt.Errorf(\"%w: %s\", ErrProtectionUnmet, why)", RF, "TestReleaseRequiresATagRule"),
    ("P1-6b draft read back and compared", "internal/releaseflow/releaseflow.go", "\tif named != tag || commit != approved {", "\tif false && (named != tag || commit != approved) {", RF, "TestDraftReadBackCatchesAMovedTag"),
    ("P1-7 URL userinfo", "internal/ledger/ledger.go", '\ts = urlUserinfo.ReplaceAllString(s, "${1}[redacted]@")', "\t_ = urlUserinfo", LG, "TestRedactURLsNestedShellAndCountList"),
    ("P1-7 explicit count list", "internal/ledger/ledger.go", '\treturn k == "input_tokens" || k == "output_tokens" || strings.HasPrefix(k, "cache_") && strings.HasSuffix(k, "_tokens")', '\treturn strings.HasSuffix(k, "_tokens")', LG, "TestRedactURLsNestedShellAndCountList"),
    ("N1 nested quoted command strings", "internal/ledger/ledger.go", "\t\tcase w.quoted:", "\t\tcase false && w.quoted:", LG, "TestRedactURLsNestedShellAndCountList"),
    ("N3 old baseline migrated", "internal/mergetrain/drift.go", "\tif err := json.Unmarshal(raw, &base); err == nil {", "\tif err := json.Unmarshal(raw, &base); err == nil || true {", MT, "TestDriftBaselineMigratesOnceAndAnInvalidOneStops"),
    ("N3 an invalid baseline stops the run", "internal/mergetrain/train.go", "\t\tif errors.Is(derr, ErrBaselineInvalid) {", "\t\tif false && errors.Is(derr, ErrBaselineInvalid) {", MT, "TestDriftBaselineMigratesOnceAndAnInvalidOneStops"),
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
