PKG = "./internal/routing/"
R = "internal/routing/router.go"
O = "internal/routing/outcome.go"
U = "internal/routing/usage.go"
CASES = [
    ("explore below the share", R, "if Draw(r.o.Seed, t.ID) < share {", "if Draw(r.o.Seed, t.ID) < 2*share {", "TestAcceptance4Exploration$", PKG),
    ("explore propensity is the share", R, "return PolicyExplore, share, away", "return PolicyExplore, 1, away", "TestAcceptance4Exploration$", PKG),
    ("table propensity is one minus the share", R, "return PolicyTable, 1 - share, tableTier", "return PolicyTable, 1, tableTier", "TestAcceptance4Exploration$", PKG),
    ("row records the share in force", R, "row.Policy, row.Propensity, row.ExplorationShare = d.Policy, d.Propensity, d.ExplorationShare", "row.Policy, row.Propensity, row.ExplorationShare = d.Policy, d.Propensity, 0", "TestAcceptance4Exploration$", PKG),
    ("draw is uniform", R, "return float64(binary.BigEndian.Uint64(h[:8])>>11) / float64(uint64(1)<<53)", "return float64(binary.BigEndian.Uint64(h[:8])>>11) / float64(uint64(1)<<53) / 2", "TestDrawIsUniform$", PKG),
    ("every ended attempt gets a row", R, "\t\t\tif !r.logged[k] {\n\t\t\t\trow := r.row(", "\t\t\tif !r.logged[k] && a.N == 1 {\n\t\t\t\trow := r.row(", "TestAcceptance5EveryAttemptHasARow$", PKG),
    ("a restart never logs twice", R, "\tr.logged[key(row.Task, row.Attempt)] = true", "\tr.logged[key(row.Task, row.Attempt)] = row.Task == \"\"", "TestAcceptance5EveryAttemptHasARow$", PKG),
    ("decision survives a restart", R, "\t\t\tr.decisions[key(d.Task, d.Attempt)] = &d\n", "\t\t\t_ = d\n", "TestDecisionSurvivesRestart$", PKG),
    ("flock serializes appends", O, "\treturn flock.WithFile(f, path, func() error {", "\t_ = flock.WithFile\n\treturn func(fn func() error) error { return fn() }(func() error {", "TestAppendWaitsForTheLock$", PKG),
    ("torn tail terminated", O, "\t\t\tif last[0] != '\\n' {", "\t\t\tif last[0] == 0 {", "TestOutcomeTornTail$", PKG),
    ("outcome log is private", O, "os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)", "os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o644)", "TestAcceptance5ConcurrentAppendKeepsLinesWhole$", PKG),
    ("verifier result fail", R, "\tcase task.OutcomeFailed, task.OutcomeCommitRefused, task.OutcomeBlocked:\n\t\treturn VerifierFail", "\tcase task.OutcomeCommitRefused, task.OutcomeBlocked:\n\t\treturn VerifierFail", "TestAcceptance5EveryAttemptHasARow$", PKG),
    ("no verifier, no check", R, "\tif !Mechanical(verifier) {\n\t\treturn VerifierNoCheck", "\tif false {\n\t\treturn VerifierNoCheck", "TestAcceptance5EveryAttemptHasARow$", PKG),
    ("review findings from the evidence", R, "\t\trow.ReviewFindings = findings(t.Evidence)\n", "\t\trow.ReviewFindings = nil\n", "TestAcceptance5EveryAttemptHasARow$", PKG),
    ("consumed smol outputs", R, "\t\tif r.usedTier[dep] == TierSmol {", "\t\tif false {", "TestConsumedSmolOutputs$", PKG),
    ("ledger tokens in the attempt window", U, "at.Before(a.GrantedAt) || at.After(a.EndedAt)", "at.IsZero()", "TestAcceptance5EveryAttemptHasARow$", PKG),
    ("cost from the price", U, "\t\t\t\tu.CostUSD = &c\n", "\t\t\t\t_ = c\n", "TestAcceptance5EveryAttemptHasARow$", PKG),
    ("wait time since pending", R, "\t\trow.WaitMS = a.GrantedAt.Sub(pendingSince).Milliseconds()\n", "\t\trow.WaitMS = 0\n", "TestAcceptance5EveryAttemptHasARow$", PKG),
    ("wall time", R, "\t\trow.WallMS = a.EndedAt.Sub(a.GrantedAt).Milliseconds()\n", "\t\trow.WallMS = 0\n", "TestAcceptance5EveryAttemptHasARow$", PKG),
]
