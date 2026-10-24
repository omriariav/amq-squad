package grants

import "testing"

// 3-2: both size caps are optional with the S6-measured defaults, and
// squad.yaml can only lower them.
func TestMergeSizeCapDefaultsAndNarrowing(t *testing.T) {
	g, err := ParseOwner("merge:\n  - repository: a/b\n    base_branch: main\n", "g")
	if err != nil || len(g.Merge) != 1 || g.Merge[0].SizeCapLines != DefaultSizeCapLines || g.Merge[0].SizeCapFiles != DefaultSizeCapFiles {
		t.Fatalf("defaults: %v %+v", err, g.Merge)
	}
	g, err = ParseOwner("merge:\n  - repository: a/b\n    base_branch: main\n    size_cap_lines: 400\n    size_cap_files: 8\n", "g")
	if err != nil || g.Merge[0].SizeCapLines != 400 || g.Merge[0].SizeCapFiles != 8 {
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
