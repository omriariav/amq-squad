PKG = "./internal/routing/"
R = "internal/routing/router.go"
D = "internal/routing/durable.go"
CN = "internal/routing/canon.go"
S = "internal/routing/seed.go"
CASES = [
    # N1
    ("N1 a retry re-validates the recorded tier", R, "\t\tif !slices.Contains(r.classTiers(t.Class, spec.Tier, false), base) {", "\t\tif false {", "TestFix2HistoryIsCheckedNotTrusted$", PKG),
    ("N1 validated by the class's own rules", R, "\t\tif !slices.Contains(r.classTiers(t.Class, spec.Tier, false), base) {", "\t\tif !slices.Contains(r.classTiers(t.Class, tableTier, escalated), base) {", "TestFix2EscalationIsAFloor$", PKG),
    # N2
    ("N2 the escalation is a floor", R, "\t\tif escalated && TierRank(base) < TierRank(tableTier) {", "\t\tif false {", "TestFix2EscalationIsAFloor$", PKG),
    ("N2 a pin below the floor is dropped", R, "\t\t\tbase, pin = tableTier, nil", "\t\t\tbase = tableTier", "TestFix2EscalationIsAFloor$", PKG),
    # P1-4 residuals
    ("P1-4 the log is re-verified each route", D, "\tif size == r.logSize && (size == 0 || tailParses(path)) {\n\t\treturn nil\n\t}", "\tif true {\n\t\treturn nil\n\t}", "TestFix2LogDurabilityIsReverified$", PKG),
    ("P1-4 the last row must parse", D, "\tif size := fileSize(OutcomePath(r.o.Root)); size > 0 && !tailParses(OutcomePath(r.o.Root)) {", "\tif size := fileSize(OutcomePath(r.o.Root)); size < 0 {", "TestFix2LogDurabilityIsReverified$", PKG),
    ("P1-4 a pending decision stops every route", D, "\tif err := r.writePendingDecisions(); err != nil {\n\t\treturn err\n\t}", "\tif err := r.writePendingDecisions(); err != nil && false {\n\t\treturn err\n\t}", "TestFix2LogDurabilityIsReverified$", PKG),
    ("P1-4 a failed decision stays pending", D, "\t\tif err := AppendJSONL(DecisionPath(r.o.Root), r.pendingDec[0]); err != nil {\n", "\t\tif err := AppendJSONL(DecisionPath(r.o.Root), r.pendingDec[0]); err != nil {\n\t\t\tr.pendingDec = nil\n", "TestFix2LogDurabilityIsReverified$", PKG),
    # P1-2 residuals
    ("P1-2 declared trees are walked", CN, "\tif expand {\n\t\tif why := treeLeaves(dir); why != \"\" {", "\tif false && expand {\n\t\tif why := treeLeaves(dir); why != \"\" {", "TestFix2DeclaredTreesAreWalked$", PKG),
    ("P1-2 globs are walked under their prefix", CN, "\t\t\tout = append(out, segs[i:]...)\n\t\t\texpand = true\n\t\t\tbreak", "\t\t\tout = append(out, segs[i:]...)\n\t\t\texpand = tree\n\t\t\tbreak", "TestFix2DeclaredTreesAreWalked$", PKG),
    ("P1-2 a link leaving the tree is local-only", CN, "err != nil || rel == \"..\" || strings.HasPrefix(rel, \"..\"+string(filepath.Separator))", "err != nil || rel == \"never\"", "TestFix2DeclaredTreesAreWalked$", PKG),
    ("P1-2 the root's ancestors resolved once", CN, "\t\treturn grants.PrivacyVendor, \"\"\n\t}\n\troot = ResolveRoot(root)", "\t\treturn grants.PrivacyVendor, \"\"\n\t}", "TestFix2DeclaredTreesAreWalked$", PKG),
    # limit b
    ("limit b a missing seed is refused", S, "\tcase sessionStarted(root):", "\tcase false && sessionStarted(root):", "TestFix2SeedIsCheckedNotRegenerated$", PKG),
    ("limit b mode 0600 required", S, "runtime.GOOS != \"windows\" && st.Mode().Perm() != 0o600", "runtime.GOOS != \"windows\" && false", "TestFix2SeedIsCheckedNotRegenerated$", PKG),
    ("limit b 32 raw bytes required", [(S, "\tif st.Size() != SeedBytes {", "\tif false {"), (S, "\tif len(raw) != SeedBytes {", "\tif len(raw) == 0 {")], "TestFix2SeedIsCheckedNotRegenerated$", PKG),
]
