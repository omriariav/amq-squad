PKG = "./internal/routing/"
F = "internal/routing/file.go"
T = "internal/routing/table.go"
R = "TestRoutingFileRefusals/"
CASES = [
    ("strict subset read", F, "yamlsub.Split(text, source, true)", "yamlsub.Split(text, source, false)", R + "malformed_line$", PKG),
    ("unknown top-level key", F, '\t\tdefault:\n\t\t\terr = e.Errf("unknown key")\n\t\t}\n\t\tif err != nil {\n\t\t\treturn fail(err)', '\t\tdefault:\n\t\t}\n\t\tif err != nil {\n\t\t\treturn fail(err)', R + "unknown_key$", PKG),
    ("schema required", F, "\tif !sawSchema {", "\tif false && !sawSchema {", R + "missing_schema$", PKG),
    ("schema 1 only", F, "err == nil && n != Schema {", "err == nil && false {", R + "schema_2$", PKG),
    ("share 0 to 1", F, "if err != nil || v < 0 || v > 1 {", "if err != nil {", R + "share_above_1$", PKG),
    ("closed class list", [(F, "\t\tif !task.ValidClass(class) {\n\t\t\treturn row.Errf", "\t\tif false {\n\t\t\treturn row.Errf"),
                           (T, "\t\tif !task.ValidClass(k) {", "\t\tif false {")], R + "unknown_class$", PKG),
    ("known tiers only", [(F, "\t\tif TierRank(tier) < 0 {\n\t\t\treturn row.Errf", "\t\tif false {\n\t\t\treturn row.Errf"),
                          (T, "\t\tif TierRank(k) < 0 {", "\t\tif false {")], R + "unknown_tier$", PKG),
    ("plan never routes down", T, "if TierRank(s.Tier) < TierRank(d.Tier) && !def.MayRouteDown(c) {", "if false {", R + "plan_routed_down$", PKG),
    ("review never routes down", T, "if TierRank(s.Tier) < TierRank(d.Tier) && !def.MayRouteDown(c) {", "if false {", R + "review_routed_down$", PKG),
    ("advisor is review only", T, 'if (c == "review") != (s.Tier == TierAdvisor) {', "if false {", R + "implement_on_advisor$", PKG),
    ("verifier fixed", F, 'err = f.Errf("the verifier is fixed by plan 6.1")', "err = nil", R + "verifier_changed$", PKG),
    ("chain entry form", F, 'if perr != nil || pl.Effort == "" {', "if false && perr != nil {", R + "bad_chain_entry$", PKG),
    ("chain not empty", T, "if !ok || len(s.Chain) == 0 {", "if !ok {", R + "empty_chain$", PKG),
    ("chain entry unique", T, "\t\t\tif seen[e] {\n\t\t\t\treturn fmt.Errorf", "\t\t\tif false {\n\t\t\t\treturn fmt.Errorf", R + "repeated_chain_entry$", PKG),
    ("cost cap not negative", F, "if err != nil || v < 0 {\n\t\treturn 0, e.Errf(\"want a number at or above 0", "if err != nil {\n\t\treturn 0, e.Errf(\"want a number at or above 0", R + "negative_cost_cap$", PKG),
    ("repository-only keys", F, "\t\t\tif !repo {\n", "\t\t\tif false {\n", R + "privacy_in_user_file$", PKG),
    ("privacy class closed", F, "r.Class, err = f.OneOf(grants.PrivacyLocal, grants.PrivacyVendor)", "r.Class, err = f.Scalar()", R + "privacy_class_unknown$", PKG),
    ("privacy glob relative", F, "if clean, err = task.CleanDeclaredFiles([]string{g}); err == nil && len(clean) == 1 {", "if clean, err = []string{g}, nil; err == nil && len(clean) == 1 {", R + "privacy_path_escapes$", PKG),
    ("privacy rule complete", F, 'if r.Glob == "" || r.Class == "" {', "if false {", R + "privacy_rule_incomplete$", PKG),
    ("local path rule matches", F, "if r.Class == grants.PrivacyLocal && mayTouch(files, r.Glob) {", "if false {", "TestLoadOverlaysUserThenRepository$", PKG),
    ("companions from the class row", T, "for _, c := range t.Classes[class].RequiredCompanions {", "for _, c := range []string(nil) {", "TestRequiredCompanionsDefaultEmptyAndRepositoryList$", PKG),
    ("no max effort default", T, 'pool("claude", "opus", "xhigh"),', 'pool("claude", "opus", "max"),', "TestDefaultTableHoldsThePlanInvariants$", PKG),
]
