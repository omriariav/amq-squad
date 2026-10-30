PKG = "./internal/routing/"
CN = "internal/routing/canon.go"
CASES = [
    ("rebase: a link component is found by readlinkat, never followed", CN, "\t\t\tif _, lerr := d.Readlink(name); lerr == nil {\n\t\t\t\treturn Canonical{Path: strings.Join(out, \"/\"), Alias:", "\t\t\tif _, lerr := d.Readlink(name); lerr == nil && false {\n\t\t\t\treturn Canonical{Path: strings.Join(out, \"/\"), Alias:", "TestFix1PrivacyMatchesTheCanonicalPath$", PKG),
    ("rebase: a link inside a declared tree that leaves it is local-only", CN, "err != nil || rel == \"..\" || strings.HasPrefix(rel, \"..\"+string(filepath.Separator))", "err != nil || rel == \"never\"", "TestFix2DeclaredTreesAreWalked$", PKG),
    ("rebase: the root is resolved before the anchored open", CN, "\t\treturn grants.PrivacyVendor, \"\"\n\t}\n\troot = ResolveRoot(root)", "\t\treturn grants.PrivacyVendor, \"\"\n\t}", "TestFix2DeclaredTreesAreWalked$", PKG),
    ("rebase: a tree over the limit is refused on the anchored walk", CN, "\t\tif *n++; *n > MaxTreeEntries {", "\t\tif *n++; *n < 0 {", "TestFix3UncheckedTreeIsARefusal$", PKG),
]
