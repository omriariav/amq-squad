package task

import "testing"

// MatchPath is the commit step's declared-files check (2b-2 milestone B).
func TestMatchPath(t *testing.T) {
	cases := []struct {
		globs []string
		path  string
		want  bool
	}{
		{[]string{"cmd/root.go"}, "cmd/root.go", true},
		{[]string{"cmd/root.go"}, "cmd/root_test.go", false},
		{[]string{"cmd/"}, "cmd/sub/x.go", true},
		{[]string{"cmd/**"}, "cmd/x.go", true},
		{[]string{"cmd/"}, "cmdx/a.go", false},
		{[]string{"cmd/*.go"}, "cmd/a.go", true},
		{[]string{"cmd/*.go"}, "cmd/sub/a.go", false},
		{[]string{"**/factory_test.go"}, "internal/client/factory_test.go", true},
		{[]string{"**/factory_test.go"}, "factory_test.go", true},
		{[]string{"internal/**/x.go"}, "internal/x.go", true},
		{[]string{"internal/**/x.go"}, "internal/a/b/x.go", true},
		{[]string{"**"}, "anything/at/all", true},
		{[]string{"Cmd/root.go"}, "cmd/root.go", false}, // case-sensitive: fail closed
		{nil, "a.go", false},
		{[]string{"a[.go"}, "a[.go", false}, // a bad pattern matches nothing
	}
	for _, c := range cases {
		if got := MatchPath(c.globs, c.path); got != c.want {
			t.Errorf("MatchPath(%v, %q) = %v, want %v", c.globs, c.path, got, c.want)
		}
	}
}
