package cli

import (
	"testing"
)

// R5 (#505 review D3): a client name usable as a -t selector must not start
// with "-", which tmux would parse as a flag rather than the target value.
func TestValidateTmuxControlClientNameRejectsLeadingDash(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value string
		want  bool
	}{
		{name: "ordinary tty", value: "/dev/ttys001", want: true},
		{name: "leading dash", value: "-t", want: false},
		{name: "leading dash bare", value: "-", want: false},
		{name: "empty", value: "", want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := validateTmuxControlClientName(tc.value)
			if (err == nil) != tc.want {
				t.Fatalf("validateTmuxControlClientName(%q) err=%v, want ok=%t", tc.value, err, tc.want)
			}
		})
	}
}
