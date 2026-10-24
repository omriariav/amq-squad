package grants

import "testing"

// 3-2: both size caps are optional with the S6-measured defaults, and
// squad.yaml can only lower them.
func TestMergeSizeCapDefaultsAndNarrowing(t *testing.T) {
	g, err := ParseOwner("merge:\n  - repository: a/b\n    base_branch: main\n", "g")
	if err != nil || len(g.Merge) != 1 || g.Merge[0].SizeCapLines != DefaultSizeCapLines || g.Merge[0].SizeCapFiles != DefaultSizeCapFiles {
		t.Fatalf("defaults: %v %+v", err, g.Merge)
	}
	if len(g.Merge[0].RequiredCompanions) != 0 {
		t.Fatalf("required companions default %v", g.Merge[0].RequiredCompanions)
	}
	g, err = ParseOwner("merge:\n  - repository: a/b\n    base_branch: main\n    size_cap_lines: 400\n    size_cap_files: 8\n    required_companions: [skills/*/SKILL.md, skills/*/references/commands.md]\n", "g")
	if err != nil || g.Merge[0].SizeCapLines != 400 || g.Merge[0].SizeCapFiles != 8 || len(g.Merge[0].RequiredCompanions) != 2 {
		t.Fatalf("explicit: %v %+v", err, g.Merge)
	}
	n, err := ParseSquadNarrowing("merge_size_cap_files: 3\nmerge_size_cap_lines: 900\n", "squad.yaml")
	if err != nil {
		t.Fatal(err)
	}
	out, _ := Narrow(g, n)
	if out.Merge[0].SizeCapFiles != 3 || out.Merge[0].SizeCapLines != 400 {
		t.Fatalf("narrowed %+v", out.Merge)
	}
	if out, _ = Narrow(g, Narrowing{MergeSizeCapFiles: 20}); out.Merge[0].SizeCapFiles != 8 {
		t.Fatalf("squad.yaml widened the file cap: %+v", out.Merge)
	}
}

// The size cap (ruling 5, measured by 3-2): 300 changed lines and 5 changed
// files by default; a merge grant's size_cap_lines and size_cap_files
// replace them; squad.yaml only lowers them.
func TestSizeCapDefaultsAndFields(t *testing.T) {
	if l, f := Defaults().SizeCap("a/b", "main"); l != 300 || f != 5 {
		t.Fatalf("defaults = %d lines, %d files", l, f)
	}
	g, err := ParseOwner("merge:\n  - repository: a/b\n    base_branch: main\n    size_cap_lines: 900\n    size_cap_files: 12\n", "grants.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if l, f := g.SizeCap("a/b", "main"); l != 900 || f != 12 {
		t.Fatalf("merge grant cap = %d, %d", l, f)
	}
	if l, f := g.SizeCap("c/d", "main"); l != 300 || f != 5 {
		t.Fatalf("another repository = %d, %d", l, f)
	}
	n, err := ParseSquadNarrowing("merge_size_cap_files: 40\nmerge_size_cap_lines: 100\n", "squad.yaml")
	if err != nil {
		t.Fatal(err)
	}
	narrowed, _ := Narrow(g, n)
	if l, f := narrowed.SizeCap("a/b", "main"); l != 100 || f != 12 {
		t.Fatalf("narrowed = %d, %d (squad.yaml may only lower)", l, f)
	}
	g2, _ := ParseOwner("merge:\n  - repository: a/b\n    base_branch: main\n    size_cap_lines: 900\n", "grants.yaml")
	n2, _ := ParseSquadNarrowing("merge_size_cap_files: 3\n", "squad.yaml")
	if narrowed, _ := Narrow(g2, n2); narrowed.Merge[0].SizeCapFiles != 3 {
		t.Fatalf("lowering from the default: %+v", narrowed.Merge[0])
	}
}
