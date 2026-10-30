PKG = "./internal/routing/"
CLI = "./internal/cli/"
RP = "internal/routing/report.go"
R = "internal/routing/router.go"
F = "internal/routing/file.go"
C = "internal/cli/routing.go"
A6 = "TestAcceptance6WeeklyReport$"
REF = "TestRoutingFileRefusals/"
CASES = [
    ("proposal: raise on redo", RP, "case canRaise && s.RedoRate >= RedoRateRaise:", "case false:", A6, PKG),
    ("proposal: raise on dominated cost", RP, "s.Dominated = s.CostUSD != nil && s.UpCostUSD != nil && *s.CostUSD >= *s.UpCostUSD", "s.Dominated = false", A6, PKG),
    ("proposal: findings traced to smol", RP, "case s.Tier == TierSmol && s.SmolFindings > 0:", "case false:", A6, PKG),
    ("proposal: long wait", RP, "case time.Duration(s.MeanWaitMS)*time.Millisecond > LongWait && len(s.Waits) > 0:", "case false:", A6, PKG),
    ("proposal: a tier down", RP, "case s.Tasks >= MinTasksForDown && s.Redone == 0 && s.SmolFindings == 0 && tbl.MayRouteDown(s.Class) && down(s.Tier) != \"\":", "case false:", A6, PKG),
    ("proposal: too few tasks", RP, "\tcase s.Tasks < MinTasksForChange:", "\tcase false:", A6, PKG),
    ("runtimes with incomplete evidence excluded", RP, "if perRuntime[r.Runtime].Incomplete == 0 {", "if true {", A6, PKG),
    ("evidence is the token counts", "internal/routing/outcome.go", "return r.TokensIn != nil && r.TokensOut != nil\n", "return r.TokensIn != nil && r.TokensOut != nil && r.CostUSD != nil\n", A6, PKG),
    ("the week window", RP, "if r.EndedAt.Before(rep.Since) || !r.EndedAt.Before(now) {", "if false {", A6, PKG),
    ("redo only at a higher tier", RP, "if TierRank(later.UsedTier) > TierRank(r.UsedTier) {", "if later.UsedTier != \"\" {", "TestReportRedoAndCostRules$", PKG),
    ("unpriced attempt means unknown cost", RP, "\t\t\tif r.CostUSD == nil {\n\t\t\t\tknown = false\n\t\t\t\tbreak\n\t\t\t}", "\t\t\tif r.CostUSD == nil {\n\t\t\t\tcontinue\n\t\t\t}", "TestReportRedoAndCostRules$", PKG),
    ("unpriced entries named", RP, "\t\t\t\trep.Unpriced = append(rep.Unpriced, r.Entry)\n", "\t\t\t\t_ = r.Entry\n", A6, PKG),
    ("findings once per bucket", RP, "if b, ok := smolOut[dep]; ok && !traced[b] {", "if b, ok := smolOut[dep]; ok {", A6, PKG),
    ("findings traced by attempt id", RP, "\t\t\t\tsmolOut[key(id, r.Attempt)] = b\n", "\t\t\t\tsmolOut[id] = b\n", A6, PKG),
    ("row names the consumed attempt", R, "\t\tif k := r.smolDone[dep]; k != \"\" {\n\t\t\trow.ConsumedSmolOutputs = append(row.ConsumedSmolOutputs, k)", "\t\tif k := r.smolDone[dep]; k != \"\" {\n\t\t\trow.ConsumedSmolOutputs = append(row.ConsumedSmolOutputs, dep)", "TestConsumedSmolOutputs$", PKG),
    ("row records cost_index", R, "\t\t\trow.CostIndex = reg.CostIndex\n", "\t\t\t_ = reg\n", "TestAcceptance5EveryAttemptHasARow$", PKG),
    ("ledger usage by default under a root", R, "if o.Usage == nil && o.Root != \"\" {", "if false {", "TestAcceptance5EveryAttemptHasARow$", PKG),
    ("budget stop only for priced entries", R, "if _, priced := r.o.Table.Prices[e]; priced {", "if true {", "TestGrantDailyBudgetStops$", PKG),
    ("prices in the owner file only", F, "\t\t\tif repo {\n\t\t\t\terr = e.Errf(\"only the owner's file", "\t\t\tif false {\n\t\t\t\terr = e.Errf(\"only the owner's file", REF + "prices_in_the_repository_file$", PKG),
    ("price complete", F, "if pl.Runtime == \"\" || !in || !out {", "if pl.Runtime == \"\" || (!in && !out && false) {", REF + "price_incomplete$", PKG),
    ("price not repeated", F, "if _, dup := t.Prices[pl]; dup {", "if _, dup := t.Prices[pl]; dup && false {", REF + "price_repeated$", PKG),
    ("price entry form", F, "if pl, err = task.ParsePool(s); err == nil && pl.Effort == \"\" {", "if pl, err = task.ParsePool(s + \":x:y\"); err == nil && pl.Effort == \"\" {", REF + "price_bad_entry$", PKG),
    ("status names the wait reason", "internal/status/classify.go", "row.State, row.ReasonCode = StateWaiting, firstNonEmpty(t.WaitReason, \"queued\")", "row.State, row.ReasonCode = StateWaiting, \"queued\"", "TestStatusNamesTheRoutingWaitReason$", CLI),
    ("router sets the privacy wait", R, "for _, r := range []string{SkipConcurrencyCap, SkipCostCap, SkipPrivacyClass, SkipReviewerFamily} {", "for _, r := range []string{SkipConcurrencyCap, SkipCostCap, SkipReviewerFamily} {", "TestStatusNamesTheRoutingWaitReason$", CLI),
    ("report needs --week", C, "\tif !*week {", "\tif false && !*week {", "TestRoutingReportWeekOnTheFixtureLog$", CLI),
    ("report reads the repository file", C, "\ttbl, err := routing.Load(home, repoRoot)", "\ttbl, err := routing.Default(), error(nil)\n\t_, _ = repoRoot, home", "TestRoutingReportRefusesABadRepositoryFile$", CLI),
    ("doctor fails an invalid table", C, "\tif err != nil {\n\t\treturn doctorCheck{Name: \"routing\", Status: doctorFail", "\tif err != nil && false {\n\t\treturn doctorCheck{Name: \"routing\", Status: doctorFail", "TestDoctorRoutingRow$", CLI),
    ("doctor warns on unpriced entries", C, "\tif len(unpriced) > 0 {", "\tif false {", "TestDoctorRoutingRow$", CLI),
]
