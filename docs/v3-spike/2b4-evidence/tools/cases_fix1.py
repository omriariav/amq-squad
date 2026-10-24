PKG = "./internal/routing/"
R = "internal/routing/router.go"
F = "internal/routing/file.go"
CN = "internal/routing/canon.go"
U = "internal/routing/usage.go"
RP = "internal/routing/report.go"
T = "internal/routing/table.go"
O = "internal/routing/open.go"
REF = "TestRoutingFileRefusals/"
CASES = [
    # P1-1
    ("P1-1 pin must resolve to a class tier", R, '\t\tif pinTier == "" {\n\t\t\treturn d, scheduler.Route{Block: BlockPinUnresolved', '\t\tif pinTier == "" && false {\n\t\t\treturn d, scheduler.Route{Block: BlockPinUnresolved', "TestLeadPin$", PKG),
    ("P1-1 review pins only on advisor", R, '\tif class == "review" {\n\t\treturn []string{TierAdvisor}\n\t}', '\tif class == "review" && false {\n\t\treturn []string{TierAdvisor}\n\t}', "TestLeadPin$", PKG),
    # limit a / ruling 4
    ("limit a: the pin fixes the starting tier", R, "\tcase t.Attempt > 0 && haveFirst:", "\tcase false && haveFirst:", "TestLeadPinFixesTheStartingTier$", PKG),
    ("limit a: a pinned attempt runs the pin", R, "\tif pin != nil && failures == 0 {", "\tif pin != nil && false {", "TestLeadPinFixesTheStartingTier$", PKG),
    # P1-2
    ("P1-2 a symlink alias is local-only", CN, '\t\tif c.Alias != "" && len(t.PrivacyPaths) > 0 {', "\t\tif false {", "TestFix1PrivacyMatchesTheCanonicalPath$", PKG),
    ("P1-2 the walk does not follow a link", CN, "\t\tif kind&fs.ModeSymlink != 0 {", "\t\tif false && kind&fs.ModeSymlink != 0 {", "TestFix1PrivacyMatchesTheCanonicalPath$", PKG),
    ("P1-2 on-disk name on a folding filesystem", CN, "\tif fold {\n\t\tfor _, e := range entries {", "\tif false {\n\t\tfor _, e := range entries {", "TestFix1PrivacyMatchesTheCanonicalPath$", PKG),
    ("P1-2 case-folded match", CN, "\tif fold {\n\t\tglob = strings.ToLower(glob)", "\tif false {\n\t\tglob = strings.ToLower(glob)", "TestFix1PrivacyMatchesTheCanonicalPath$", PKG),
    ("P1-2 the router uses the canonical match", R, "\tf.Privacy, f.PrivacyGlob = tbl.RequiredPrivacyAt(root, t.DeclaredFiles)", "\tf.Privacy, f.PrivacyGlob = tbl.RequiredPrivacy(t.DeclaredFiles)", "TestFix1PrivacyMatchesTheCanonicalPath$", PKG),
    ("P1-2 sensitive paths canonical", CN, "\t\tif c.Alias != \"\" {\n\t\t\treturn true\n\t\t}", "\t\tif false {\n\t\t\treturn true\n\t\t}", "TestFix1PrivacyMatchesTheCanonicalPath$", PKG),
    # P1-3
    ("P1-3 exact child keys", F, 'func child(parent, e yamlsub.Entry) string { return strings.TrimPrefix(e.Key, parent.Key+".") }', 'func child(parent, e yamlsub.Entry) string { _ = parent; return e.Key[strings.LastIndex(e.Key, ".")+1:] }', REF, PKG),
    ("P1-3 start refuses the widened rule", F, 'func child(parent, e yamlsub.Entry) string { return strings.TrimPrefix(e.Key, parent.Key+".") }', 'func child(parent, e yamlsub.Entry) string { _ = parent; return e.Key[strings.LastIndex(e.Key, ".")+1:] }', "TestFix1OpenRefusesAnInvalidTable$", PKG),
    # P1-4
    ("P1-4 unwritable outcome log stops dispatch", R, "\t\tif err := r.flush(list, now); err != nil {", "\t\tif err := r.flush(list, now); err != nil && false {", "TestFix1UnwritableLogStopsDispatch$", PKG),
    ("P1-4 unwritable decision log stops dispatch", R, "\t\tif err := r.remember(d, t, route); err != nil {", "\t\tif err := r.remember(d, t, route); err != nil && false {", "TestFix1UnwritableLogStopsDispatch$", PKG),
    # P2-5
    ("P2-5 one counter is unknown usage", U, "\t\t\tcase inOK || outOK:\n\t\t\t\tpartial = true", "\t\t\tcase false:\n\t\t\t\tpartial = true", "TestFix1UsageNeedsBothCounters$", PKG),
    ("P2-5 a lone counter never sums as zero", U, "\t\t\tcase inOK && outOK:", "\t\t\tcase inOK || outOK:", "TestFix1UsageNeedsBothCounters$", PKG),
    # P2-6
    ("P2-6 non-finite numbers refused", F, "if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {", "if err != nil || math.IsNaN(0) {", REF, PKG),
    # P2-7
    ("P2-7 retry reads the recorded skips", R, "\t\td.Skipped = append(d.Skipped, r.recordedSkips(t.ID, t.Attempts[k-1].N)...)", "\t\t_ = k", "TestRetryKeepsTheRecordedSkips$", PKG),
    ("P2-7 the row is the record", R, "\tif h, ok := r.hist[key(id, n)]; ok {\n\t\treturn slices.Clone(h.Skipped)\n\t}", "\tif h, ok := r.hist[key(id, n)]; ok && false {\n\t\treturn slices.Clone(h.Skipped)\n\t}", "TestRetryKeepsTheRecordedSkips$", PKG),
    # P2-8
    ("P2-8 a tier down needs verifier passes", RP, "\tcase s.Passed >= MinTasksForDown && s.Redone == 0", "\tcase s.Tasks >= MinTasksForDown && s.Redone == 0", "TestReportRedoAndCostRules$", PKG),
    # limit b
    ("limit b: random persisted session seed", R, "\t\tseed, err := SessionSeed(o.Root)", "\t\tseed, err := o.Root, error(nil)", "TestFix1SessionSeed$", PKG),
    ("limit b: the seed is private", "internal/routing/seed.go", "\tif err := tmp.Chmod(0o600); err == nil {", "\tif err := tmp.Chmod(0o644); err == nil {", "TestFix1SessionSeed$", PKG),
    ("limit b: a damaged seed is refused", "internal/routing/seed.go", "\tif len(s) != 64 {", "\tif len(s) == 0 {", "TestFix1SessionSeed$", PKG),
    # ruling 1
    ("ruling 1: companions union", T, "append(slices.Clone(t.Classes[class].RequiredCompanions), repository...)", "slices.Clone(t.Classes[class].RequiredCompanions)", "TestRequiredCompanionsDefaultEmptyAndRepositoryList$", PKG),
    ("ruling 1: each companion once", T, "if !slices.Contains(files, c) && !slices.Contains(out, c) {", "if !slices.Contains(files, c) {", "TestRequiredCompanionsDefaultEmptyAndRepositoryList$", PKG),
    # ruling 2
    ("ruling 2: start refuses routing_invalid", O, "\tif err != nil {\n\t\treturn nil, &InvalidError{Err: err}\n\t}", "\tif err != nil {\n\t\ttbl = Default()\n\t}", "TestFix1OpenRefusesAnInvalidTable$", PKG),
    ("ruling 2: New refuses a broken table", R, "\t\treturn nil, &InvalidError{Err: err}", "\t\treturn nil, err", "TestFix1OpenRefusesAnInvalidTable$", PKG),
]
