R = "./internal/routing/"
TK = "./internal/task/"
GR = "./internal/grants/"
CLI = "./internal/cli/"
RF = "./internal/releaseflow/"
RT = "internal/routing/router.go"
SW = "TestRepassUnconfiguredVerifierRoutesAtTierAndCompletes$"
GATE = "TestRepassGateOnlyForAConfiguredVerifier$"
CASES = [
    # the ruling on ruling 1: the class switch
    ("switch: route below the tier only with a configured verifier", RT, "\treturn r.o.Table.MayRouteDown(class) && r.verifierConfigured(class)", "\treturn r.o.Table.MayRouteDown(class)", SW, R),
    ("switch: no configured verifier records none", RT, "\tif !r.o.Grants.VerifierRequired(t.Class) {", "\tif false && !r.o.Grants.VerifierRequired(t.Class) {", SW, R),
    ("switch: the store gate only for a configured verifier", "internal/task/lease.go", "\treturn required, \"\"", "\treturn required || true, \"\"", GATE, TK),
    ("switch (router row): done completes with no verifier", "internal/task/lease.go", "\treturn required, \"\"", "\treturn required || true, \"\"", SW, R),
    ("switch: an unreadable source fails closed", "internal/task/lease.go", "\t\treturn true, \" (the verifier configuration could not be read: \" + err.Error() + \")\"", "\t\treturn false, \"\"", GATE, TK),
    ("switch: verifiers only for classes with a mechanical verifier", "internal/grants/parse.go", "\t\tif !verifierClass(class) {", "\t\tif false && !verifierClass(class) {", "TestVerifiersFromTheGrantAndOnlyNarrowed$", GR),
    # the reviewer's correction: squad.yaml adds classes to the gate, never removes one
    ("correction: a squad.yaml omitting a verified class is grants_invalid", "internal/grants/grants.go", "\tif omitted := VerifierOmissions(r.Grants, n); len(omitted) > 0 {\n\t\treturn Resolved", "\tif omitted := VerifierOmissions(r.Grants, n); false && len(omitted) > 0 {\n\t\treturn Resolved", "TestVerifiersFromTheGrantAndOnlyNarrowed$", GR),
    ("correction: an added class joins the gate", "internal/grants/grants.go", "\t\t\tif g.Verifiers[c] == \"\" && !slices.Contains(out.VerifierGate, c) {", "\t\t\tif false && g.Verifiers[c] == \"\" && !slices.Contains(out.VerifierGate, c) {", "TestVerifiersFromTheGrantAndOnlyNarrowed$", GR),
    ("correction: Narrow keeps a verified class a list omits", "internal/grants/grants.go", "\tout.Verifiers = maps.Clone(g.Verifiers)\n", "\tout.Verifiers = maps.Clone(g.Verifiers)\n\tif n.VerifierClasses != nil {\n\t\tmaps.DeleteFunc(out.Verifiers, func(c, _ string) bool { return !slices.Contains(n.VerifierClasses, c) })\n\t}\n", "TestVerifiersFromTheGrantAndOnlyNarrowed$", GR),
    ("correction: a gated class with no grant command blocks, named", "internal/task/lease.go", "\tif required && cmd == \"\" {", "\tif false && required && cmd == \"\" {", GATE, TK),
    ("coverage: the CLI and scheduler run stores read the grant", "internal/cli/task.go", "\ts.Verifiers = grants.TaskVerifier\n\treturn s, nil", "\t_ = grants.TaskVerifier\n\treturn s, nil", "TestRepassCLIStoresReadTheVerifierGrant$", CLI),
    # R2 (a), (b) and the seat-view wiring
    ("R2a an idle seat of the pool is capacity", RT, "\t\t\tif idle && s.Pool == own {", "\t\t\tif false && idle && s.Pool == own {", "TestRepassSchedulerSeatViewHoldsTheCap$/idle_seat", R),
    ("R2 the scheduler's view holds a closing seat", RT, "\tif r.o.Seats != nil {\n\t\tfor _, s := range r.o.Seats() {", "\tif false {\n\t\tfor _, s := range r.o.Seats() {", "TestRepassSchedulerSeatViewHoldsTheCap$/closing_seat", R),
    ("R2b --lead-pool only from the lead's team mode", "internal/cli/scheduler_run.go", "\tif len(leadPoolFlags) > 0 && !startedByLeadTeam() {", "\tif false && len(leadPoolFlags) > 0 && !startedByLeadTeam() {", "TestRepassLeadPoolOnlyFromTheLeadsTeamMode$", CLI),
    ("coverage: scheduler run's seat-view wiring", "internal/cli/scheduler_run.go", "\t\treturn (*sched).Occupancy()", "\t\treturn nil", "TestRepassSchedulerRunSeatViewWiring$", CLI),
    ("coverage: the lead forwards its pools", "internal/cli/lead_scheduler.go", "\t\t\targs = append(args, \"--lead-pool\", p)", "\t\t\t_ = p", "TestRepassLeadForwardsItsPools$", CLI),
    ("coverage: verify --task carries incomplete", "internal/cli/verify.go", "\t\tres.Incomplete = len(res.IncompleteReasons) > 0", "\t\tres.Incomplete = false", "TestRepassVerifyTaskEvidenceIncomplete$", CLI),
    ("coverage: verify --task prints the reasons", "internal/cli/verify.go", "\t\t\tfmt.Printf(\"- incomplete evidence: %s\\n\", why)", "\t\t\t_ = why", "TestRepassVerifyTaskEvidenceIncomplete$", CLI),
    ("coverage: the spend guard in note()", RT, "\tif row.CostUSD != nil && validCost(*row.CostUSD) {", "\tif row.CostUSD != nil {", "TestRepassNegativeCostRowNeverLowersTheSpend$", R),
    # P2-5
    ("P2-5 the first attempt's bucket is instantiated", "internal/routing/report.go", "\t\t\tget(first)\n", "", "TestRepassTaskHistoriesKeepTheFirstAttempt$", R),
    # 3-2 P1-1
    ("3-2 P1-1 a published release is never deleted", "internal/releaseflow/releaseflow.go", "\t\tif !d.Draft {", "\t\tif false && !d.Draft {", "TestRepassPublishedMismatchIsNeverDeleted$", RF),
    ("3-2 P1-1 an unwritten owner alert is an error", "internal/releaseflow/releaseflow.go", "\t\treturn fmt.Errorf(\"%w: %s alert: %v\", ErrAlertUnwritten, kind, err)", "\t\treturn nil", "TestRepassPublishedMismatchIsNeverDeleted$", RF),
]
