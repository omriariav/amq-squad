package cli

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestExitCodeTaxonomy(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"nil success", nil, ExitSuccess},
		{"usage error", UsageError("bad flag"), ExitUser},
		{"generic error", errors.New("io failed"), ExitSystem},
		{"partial error", &PartialError{Message: "1 of 2 failed"}, ExitPartial},
		{"wrapped usage error", fmt.Errorf("ctx: %w", UsageError("missing selector")), ExitUser},
		{"wrapped partial error", fmt.Errorf("ctx: %w", &PartialError{Message: "mixed"}), ExitPartial},
		// PartialError must dominate when it wraps a UsageError: the outer
		// command surfaced "partial success", not "user error".
		{"partial wrapping usage", &PartialError{Message: "mixed", Cause: UsageError("bad row")}, ExitPartial},
	}
	for _, tc := range cases {
		if got := ExitCode(tc.err); got != tc.want {
			t.Errorf("%s: ExitCode(%v) = %d, want %d", tc.name, tc.err, got, tc.want)
		}
	}
}

// Help paths must still exit nil through Run; parseFlags does not wrap
// flag.ErrHelp.
func TestParseFlagsLeavesHelpUnchanged(t *testing.T) {
	for _, args := range [][]string{
		{"doctor", "--help"},
		{"version", "--help"},
	} {
		_, _, err := captureOutput(t, func() error { return Run(args, "test") })
		if err != nil {
			t.Errorf("Run %v: want nil, got %v", args, err)
		}
	}
}

func TestPartialErrorUnwrap(t *testing.T) {
	inner := errors.New("inner")
	pe := &PartialError{Message: "outer", Cause: inner}
	if !errors.Is(pe, inner) {
		t.Error("PartialError should unwrap to its Cause")
	}
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

func TestRootHelpIncludesExitCodeTable(t *testing.T) {
	stdout, _, err := captureOutput(t, func() error { return Run([]string{"--help"}, "test") })
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Exit codes:",
		"0  success",
		"1  usage",
		"2  system",
		"3  partial",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("root --help missing %q in:\n%s", want, stdout)
		}
	}
	// Command list must remain intact.
	for _, want := range []string{"team", "status", "doctor", "version"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("root --help missing command %q in:\n%s", want, stdout)
		}
	}
}
