package seatcommit

import (
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// 3.0.1 t16 round 7 (Codex 2): a real refusal's outside paths are exact and
// untruncated (OutsidePaths): ten files outside the declared set, names with
// ", ", " (" and a glob character among them, all listed whole, while the
// display text (Detail) truncates at eight. Reverted (no structured list),
// the lead's widening could only parse the truncated, ambiguous text.
func TestRefusalOutsidePathsAreExact(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n// declared\n")
	want := []string{"docs/a, b.md", "src/ (note)", "src/*.go", "z/1.txt", "z/2.txt", "z/3.txt", "z/4.txt", "z/5.txt", "z/6.txt", "z/7.txt"}
	for _, p := range want {
		write(t, filepath.Join(f.wt, filepath.FromSlash(p)), "x\n")
	}
	res := f.run(f.reported("cmd/root.go"))
	if res.ReasonCode != ReasonOutside {
		t.Fatalf("want %s, got %+v", ReasonOutside, res)
	}
	got := res.OutsidePaths()
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("outside paths:\n got %q\nwant %q", got, want)
	}
	if !strings.Contains(res.Detail, "and 2 more") {
		t.Fatalf("the display text should truncate at eight: %q", res.Detail)
	}
}
