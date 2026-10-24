PKG = "./internal/routing/"
R = "internal/routing/router.go"
D = "internal/routing/durable.go"
O = "internal/routing/outcome.go"
S = "internal/routing/seed.go"
SU = "internal/routing/seed_unix.go"
CN = "internal/routing/canon.go"
T1 = "TestFix3TamperedRowsAreAbsentAndReported$"
CASES = [
    # ruling 1: HMAC
    ("R1 rows are signed when written", R, "\t\t\t\t\trow = row.Sign(r.key)\n", "\t\t\t\t\t_ = r.key\n", T1, PKG),
    ("R1 rows are verified on every full read", D, "\tgood, bad := VerifyRows(rows, r.key)", "\tgood, bad := rows, []Row(nil)", T1, PKG),
    ("R1 a same-size, same-mtime edit is read within the interval", D, "\tif st.same(r.logStat) && now.Sub(r.checkedAt) < FullCheckInterval && !now.Before(r.checkedAt) {", "\tif st.same(r.logStat) {", T1, PKG),
    ("R1 the stamp includes the mtime", D, " && s.info.ModTime().Equal(o.info.ModTime())", "", T1, PKG),
    ("R1 decisions are verified on start", R, "\t\tif !d.verify(r.key) {", "\t\tif false {", T1, PKG),
    ("R1 the report leaves tampered rows out", S, "\tgood, tampered = VerifyRows(rows, SeedKey(seed))", "\tgood, _ = rows, seed", T1, PKG),
    # ruling 2: policy closed list, lead_pin only for a pin
    ("R2 row policy on the closed list", O, " && slices.Contains(Policies, r.Policy)", " && (slices.Contains(Policies, r.Policy) || true)", "TestFix3PolicyIsAClosedListAndLeadPinIsReal$", PKG),
    ("R2 decision policy on the closed list", R, " && slices.Contains(Policies, d.Policy)", "", "TestFix3PolicyIsAClosedListAndLeadPinIsReal$", PKG),
    ("R2 a mismatched dispatch is manual", R, "\t\t\trow.Policy, row.Propensity = PolicyManual, 0\n", "\t\t\trow.Policy, row.Propensity = PolicyLeadPin, 1\n", "TestFix3PolicyIsAClosedListAndLeadPinIsReal$", PKG),
    ("R2 no decision is manual", R, "row.Policy, row.Propensity, row.ExplorationShare = PolicyManual, 0, r.o.Table.ExplorationShare", "row.Policy, row.Propensity, row.ExplorationShare = PolicyLeadPin, 1, r.o.Table.ExplorationShare", "TestFix2HistoryIsCheckedNotTrusted$", PKG),
    # ruling 3 (N1, N2): the actual entry's tier
    ("R3/N1 the first attempt's actual entry tier", R, "\t\tbase = entryTier(tbl, r.classTiers(t.Class, spec.Tier, false), recorded, entry)", "\t\tbase = recorded", "TestFix3ActualTierNotTheRecordedString$", PKG),
    ("R3 the recorded string only breaks a tie", R, "\tif slices.Contains(allowed, recorded) && slices.Contains(t.Tiers[recorded].Chain, entry) {", "\tif slices.Contains(allowed, recorded) {", "TestFix3ActualTierNotTheRecordedString$", PKG),
    ("R3/N1 a hand dispatch off the class tiers is refused", R, "\t\tif base == \"\" {\n\t\t\treturn d, scheduler.Route{Block: BlockPinUnresolved, Detail: fmt.Sprintf(\"routing: the first attempt", "\t\tif base == \"\" && false {\n\t\t\treturn d, scheduler.Route{Block: BlockPinUnresolved, Detail: fmt.Sprintf(\"routing: the first attempt", "TestFix2HistoryIsCheckedNotTrusted$", PKG),
    ("R3/N2 the floor on the actual tier", R, "\t\tif escalated && TierRank(base) < TierRank(tableTier) {\n\t\t\tbase, pin = tableTier, nil", "\t\tif false {\n\t\t\tbase, pin = tableTier, nil", "TestFix3ActualTierNotTheRecordedString$", PKG),
    # ruling 4 (P1-4): tail before flush, recovery keeps the policy
    ("R4/P1-4 the tail before any append", D, "\t// The tail before any append: a flush must not cover it.\n\tif err := r.checkTail(); err != nil {\n\t\treturn err\n\t}\n", "", "TestFix3TailBeforeFlushAndRecoveryKeepsPolicy$", PKG),
    ("R4 recovery takes the kept decision's policy", R, "\t\t\t\tr.note(row)\n\t\t\t\tif r.o.OnRow != nil {", "\t\t\t\tr.note(row)\n\t\t\t\tdelete(r.decisions, k)\n\t\t\t\tif r.o.OnRow != nil {", "TestFix3TailBeforeFlushAndRecoveryKeepsPolicy$", PKG),
    ("R4/P1-4 a truncated log is read again", D, "\tif st.same(r.logStat) && now.Sub(r.checkedAt) < FullCheckInterval && !now.Before(r.checkedAt) {\n\t\treturn nil\n\t}", "\tif st.size() >= 0 {\n\t\treturn nil\n\t}", "TestFix3TailBeforeFlushAndRecoveryKeepsPolicy$", PKG),
    # ruling 5: the seed through a no-follow open
    ("R5 the seed is opened without following a link", SU, "os.O_RDONLY|syscall.O_NOFOLLOW", "os.O_RDONLY|0*syscall.O_NOFOLLOW", "TestFix3SeedIsNotReadThroughALink$", PKG),
    # ruling 6: an unchecked tree is a refusal
    ("R6 an unchecked tree blocks the task", R, "\tif f.Unchecked != \"\" {", "\tif false && f.Unchecked != \"\" {", "TestFix3UncheckedTreeIsARefusal$", PKG),
    ("R6 never a silent local-only", CN, "\t\t\trefuse = fmt.Sprintf(\"the declared tree %s has more than %d entries; declare narrower paths\", dir, MaxTreeEntries)", "\t\t\twhy = fmt.Sprintf(\"the declared tree %s has more than %d entries; declare narrower paths\", dir, MaxTreeEntries)", "TestFix3UncheckedTreeIsARefusal$", PKG),
]
