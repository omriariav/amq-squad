#!/usr/bin/env python3
"""Revert check for the 3-2 fix 4 commit (third Codex pass on 17e4e987)."""
import subprocess, sys, os

W = "/Users/omri/code/amq-squad/.claude/worktrees/agent-a83ff47e3e447b571"
MT, RF, TK = "./internal/mergetrain/", "./internal/releaseflow/", "./internal/task/"
REVERTS = [
    ("1 binding keyed by repository and PR", "internal/mergetrain/binding.go", "if t.PRNumber == pr && strings.EqualFold(t.PRRepository, repository) {", "if t.PRNumber == pr && (strings.EqualFold(t.PRRepository, repository) || true) {", MT, "TestRealRunReadsTheTaskBoundToThePR"),
    ("2 the train binds the SHA its rebase reported", "internal/mergetrain/train.go",
     "\t\t\t\tif err := t.Binder.BindPR(it.Task, req.Repository, p.Number, pushed); err != nil {",
     "\t\t\t\tif rb, _ := t.Forge.PR(ctx, req.Repository, p.Number); rb.HeadSHA != \"\" {\n\t\t\t\t\tpushed = rb.HeadSHA\n\t\t\t\t}\n\t\t\t\tif err := t.Binder.BindPR(it.Task, req.Repository, p.Number, pushed); err != nil {",
     MT, "TestTrainRebindsAfterItsRebaseAndRefusesAnUnboundPush"),
    ("3 the check re-reads the task store", "internal/mergetrain/train.go", "\tif bound {\n\t\t// the check reads the binding", "\tif false && bound {\n\t\t// the check reads the binding", MT, "TestTrainRebindsAfterItsRebaseAndRefusesAnUnboundPush"),
    ("3 the rebind audit entry before the binding write", "internal/mergetrain/train.go",
     "\t\t\t\tif err := entry(Entry{Kind: \"rebind\", PR: p.Number, Task: it.Task, Head: it.BoundHead, NewHead: pushed}); err != nil {\n\t\t\t\t\treturn err\n\t\t\t\t}\n\t\t\t\tif err := t.Binder.BindPR(it.Task, req.Repository, p.Number, pushed); err != nil {\n\t\t\t\t\treturn fmt.Errorf(\"%w: task %s to %s: %v\", ErrBindingFailed, it.Task, short(pushed), err)\n\t\t\t\t}",
     "\t\t\t\tif err := t.Binder.BindPR(it.Task, req.Repository, p.Number, pushed); err != nil {\n\t\t\t\t\treturn fmt.Errorf(\"%w: task %s to %s: %v\", ErrBindingFailed, it.Task, short(pushed), err)\n\t\t\t\t}\n\t\t\t\tif err := entry(Entry{Kind: \"rebind\", PR: p.Number, Task: it.Task, Head: it.BoundHead, NewHead: pushed}); err != nil {\n\t\t\t\t\treturn err\n\t\t\t\t}",
     MT, "TestTrainRebindsAfterItsRebaseAndRefusesAnUnboundPush"),
    ("4 a binding without pr_head is unbound", "internal/mergetrain/binding.go", '\tcase bound[0].PRHead == "":', "\tcase false:", MT, "TestBindingWithoutAHeadIsUnbound"),
    ("5 BindPR writes the binding in the store transaction", "internal/task/bind.go", "\t\tt.PRRepository, t.PRNumber, t.PRHead = repository, pr, head\n", "\t\t_ = t\n", TK, "TestBindPRSurvivesMutations"),
    ("6 tag rule bypass actors", "internal/releaseflow/gh.go", "\tcase len(rs.BypassActors) > 0:", "\tcase false:", RF, "TestTagRulesetWithBypassActorsIsUnmet"),
    ("7 no exclusion list", "internal/releaseflow/gh.go", "\tif len(exclude) > 0 {", "\tif false {", RF, "TestTagRuleCoversPatterns"),
    ("7 one * only", "internal/releaseflow/gh.go", ' || strings.Count(rest, "*") > 1:', ":", RF, "TestTagRuleCoversPatterns"),
    ("8 read-back compared to the created draft", "internal/releaseflow/releaseflow.go", "\t\tif !found || !same(got, want) || !same(created, want) {", "\t\tif !found {", RF, "TestEditedDraftIsDeletedAuditedAndAlerted"),
    ("8 re-read before the publish", "internal/releaseflow/releaseflow.go", "\tif !found || !same(again, d) {", "\tif !found {", RF, "TestEditedDraftIsDeletedAuditedAndAlerted"),
    ("9 a failed deletion is a draft_orphan alert", "internal/releaseflow/releaseflow.go", 'f.alert(root, repo, "draft_orphan", tag', 'f.alert(root, repo, "draft_other", tag', RF, "TestEditedDraftIsDeletedAuditedAndAlerted"),
    ("9 the deletion is audited", "internal/releaseflow/releaseflow.go", '\t\taerr := note("draft_"+strings.TrimPrefix(cause.Error(), "draft_"), d, why+"; "+result)', "\t\t_ = result\n\t\tvar aerr error", RF, "TestEditedDraftIsDeletedAuditedAndAlerted"),
    ("10 the draft id is recorded at creation", "internal/releaseflow/releaseflow.go", "\t\tif err := writeOnce(record, created); err != nil {", "\t\tif err := error(nil); err != nil {", RF, "TestOrphanDraftIsRecoveredOnTheNextRun"),
    ("D1/D2 a local push target refuses a real run", "internal/mergetrain/train.go", '\t\tif !t.DryRun {\n\t\t\tres.Stopped = fmt.Sprintf("push_target_local', '\t\tif false {\n\t\t\tres.Stopped = fmt.Sprintf("push_target_local', MT, "TestLocalPushTargetRefusesARealRun"),
    ("D1/D2 push_url scheme check", "internal/grants/parse.go", "err == nil && !ValidPushURL(m.PushURL) {", "err == nil && false {", MT, "TestLocalPushTargetRefusesARealRun"),
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
        assertion = r.returncode != 0 and "build failed" not in text
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
