R = "./internal/routing/"
TK = "./internal/task/"
SC = "./internal/scheduler/"
CLI = "./internal/cli/"
L = "internal/task/lease.go"
RT = "internal/routing/router.go"
RP = "internal/routing/report.go"
SD = "internal/routing/seed.go"
CO = "internal/status/collect.go"
R1T = "TestRepassMechanicalClassCompletesOnlyOnAHarnessVerdict$"
R1R = "TestRepassVerdictMissingIsNeverAPassAndEscalates$"
R2 = "TestRepassConcurrencyCountsSeatsNotTasks$"
P3 = "TestRepassUsageCountersByNameNestedFlatAndAliases$"
P4 = "TestRepassInvalidCountersAreUnknownUsageAndReported$"
P5 = "TestRepassTaskHistoriesKeepTheFirstAttempt$"
# the gate as it stands after the class switch (the ruling on ruling 1)
GATE = "\tif required, why := s.verifierRequired(*t); required || v != nil {"
GATE_OFF = "\tif required, why := s.verifierRequired(*t); false && (required || v != nil) {"
CASES = [
    # ruling 1
    ("R1 the store completes a mechanical class only on a verdict", L, GATE, GATE_OFF, R1T, TK),
    ("R1 (router row) a done report with no verdict is no pass", L, GATE, GATE_OFF, R1R, R),
    ("R1 a fail verdict never completes", L, "\t\tcase v.Result != VerdictPass:\n\t\t\tendAttempt", "\t\tcase false:\n\t\t\tendAttempt", R1T, TK),
    ("R1 verifier_result pass only from a recorded verdict", RT, "\tif a.Verdict != nil {\n\t\tif a.Verdict.Result == task.VerdictPass {",
     "\tif a.Outcome == task.OutcomeCompleted {\n\t\treturn VerifierPass\n\t}\n\tif a.Verdict != nil {\n\t\tif a.Verdict.Result == task.VerdictPass {", R1R, R),
    # ruling 2
    ("R2 the cap counts the scheduler's seat view (a closing seat)", RT, "\tif r.o.Seats != nil {\n\t\tfor _, s := range r.o.Seats() {", "\tif false {\n\t\tfor _, s := range r.o.Seats() {", R2, R),
    ("R2 the lead's seat counts when it shares the pool", RT, "\tfor _, p := range r.o.LeadPools {", "\tfor _, p := range r.o.LeadPools[:0] {", R2, R),
    ("R2 each seat once (a seat and its leased task)", RT, "\t\t\tcount(\"seat:\"+s.Handle, s.Pool)", "\t\t\tcount(\"view:\"+s.Handle, s.Pool)", R2, R),
    ("R2 the lead's pool by runtime and model", RT, "\t\tif e.Runtime == p.Runtime && e.Model == p.Model {", "\t\tif false && e.Runtime == p.Runtime && e.Model == p.Model {", R2, R),
    ("R2 the scheduler's view lists pending requests", "internal/scheduler/scheduler.go", "\tfor _, r := range s.requests {\n\t\tout = append(out, SeatView{Handle: \"request \"",
     "\tfor _, r := range s.requests[:0] {\n\t\tout = append(out, SeatView{Handle: \"request \"", "TestRepassOccupancyNamesLiveClosingAndRequestedSeats$", SC),
    # P2-3
    ("P2-3 counters by name beside nested objects", CO, "\tvar bad string\n\tif c.In, c.InOK, bad = counter(usage, in); bad != \"\" {",
     "\tvar bad string\n\tif b, _ := json.Marshal(usage); json.Unmarshal(b, new(map[string]float64)) != nil {\n\t\treturn c\n\t}\n\tif c.In, c.InOK, bad = counter(usage, in); bad != \"\" {", P3, R),
    ("P2-3 the prompt_tokens alias", CO, "\"input_tokens\", \"inputTokens\", \"prompt_tokens\", \"promptTokens\"}", "\"input_tokens\", \"inputTokens\"}", P3, R),
    ("P2-3 the completion_tokens alias", CO, "\"output_tokens\", \"outputTokens\", \"completion_tokens\", \"completionTokens\"}", "\"output_tokens\", \"outputTokens\"}", P3, R),
    ("P2-3 counters flat on the payload", CO, "\t} else {\n\t\tusage = p\n\t}", "\t} else {\n\t\treturn c\n\t}", P3, R),
    # P2-4
    ("P2-4 a negative counter is no count", CO, "\t\tcase f < 0:", "\t\tcase f < -1e18:", P4, R),
    ("P2-4 a fractional counter is no count", CO, "\t\tcase math.IsNaN(f) || f != math.Trunc(f):", "\t\tcase math.IsNaN(f):", P4, R),
    ("P2-4 an out-of-range counter is no count", CO, "\t\tcase f > MaxTokenCount:", "\t\tcase f > 1e300:", P4, R),
    ("P2-4 a row carries no negative usage or cost", [
        (RT, "\tcase bad(u.TokensIn) || bad(u.TokensOut):", "\tcase bad(nil):"),
        (RT, "\tcase u.CostUSD != nil && !validCost(*u.CostUSD):", "\tcase u.CostUSD == nil && false:"),
    ], P4, R),
    ("P2-4 the report counts usage_invalid rows", RP, "\t\tif r.UsageInvalid != \"\" {", "\t\tif false {", P4, R),
    # P2-5
    ("P2-5 whole task histories (no attempt becomes the first)", [
        (RP, "\tfor _, r := range rows {\n\t\thist[r.Task] = append(hist[r.Task], r)", "\tfor _, r := range used {\n\t\thist[r.Task] = append(hist[r.Task], r)"),
        (RP, "\t\t\tif r.Attempt != i+1 || r.CostUSD == nil || excluded(r) {", "\t\t\tif r.CostUSD == nil || excluded(r) || i < 0 {"),
    ], P5, R),
    ("P2-5 an excluded required attempt makes the cost unknown", RP, "\t\t\tif r.Attempt != i+1 || r.CostUSD == nil || excluded(r) {", "\t\t\tif r.Attempt != i+1 || r.CostUSD == nil || (excluded(r) && false) {", P5, R),
    ("P2-5 a missing first attempt makes the cost unknown", RP, "\t\t\tif r.Attempt != i+1 || r.CostUSD == nil || excluded(r) {", "\t\t\tif r.CostUSD == nil || excluded(r) || i < 0 {", P5, R),
    # P2-6
    ("P2-6 unpriced names before the runtime filter", [
        (RP, "\t\tif r.CostUSD == nil && r.TokensIn != nil && r.TokensOut != nil && r.Entry != \"\" && !containsStr(rep.Unpriced, r.Entry) {",
         "\t\tif false && r.CostUSD == nil && r.TokensIn != nil && r.TokensOut != nil && r.Entry != \"\" && !containsStr(rep.Unpriced, r.Entry) {"),
        (RP, "\t\tif perRuntime[r.Runtime].Incomplete == 0 {\n\t\t\tused = append(used, r)\n",
         "\t\tif perRuntime[r.Runtime].Incomplete == 0 {\n\t\t\tused = append(used, r)\n\t\t\tif r.CostUSD == nil && r.Entry != \"\" && !containsStr(rep.Unpriced, r.Entry) {\n\t\t\t\trep.Unpriced = append(rep.Unpriced, r.Entry)\n\t\t\t}\n"),
    ], "TestRepassUnpricedEntriesBeforeTheRuntimeFilter$", R),
    # P2-7
    ("P2-7 the report refuses a missing head", SD, "\tif herr != nil {\n\t\treturn nil, nil, true, herr", "\tif herr != nil && found {\n\t\treturn nil, nil, true, herr", "TestRepassReportRunsTheStartChecks$", R),
    ("P2-7 the report checks the seed identity", SD, "checkHead(root, key, dev, ino)", "checkHead(root, key, 0*dev, 0*ino)", "TestRepassReportRunsTheStartChecks$", R),
    # P2-8
    ("P2-8 scheduler run refuses with routing_invalid", "internal/cli/scheduler_run.go", "\t\treturn fmt.Errorf(\"scheduler run: the router: %w\", err)", "\t\treturn fmt.Errorf(\"scheduler run: the router: %v\", err)",
     "TestRepassSchedulerRunRefusesAnInvalidTableAsRoutingInvalid$", CLI),
]
