package cli

import (
	"strings"
	"testing"
)

func TestRunVersionCommand(t *testing.T) {
	stdout, stderr, err := captureOutput(t, func() error {
		return Run([]string{"version"}, "v-test")
	})
	if err != nil {
		t.Fatalf("Run version: %v\nstderr:\n%s", err, stderr)
	}
	if strings.TrimSpace(stdout) != "amq-squad v-test" {
		t.Fatalf("stdout = %q", stdout)
	}
}

func TestRunHelpIncludesVersionCommand(t *testing.T) {
	stdout, stderr, err := captureOutput(t, func() error {
		return Run([]string{"--help"}, "v-test")
	})
	if err != nil {
		t.Fatalf("Run help: %v\nstderr:\n%s", err, stderr)
	}
	if !strings.Contains(stdout, "version   Print the amq-squad version") {
		t.Fatalf("help missing version command:\n%s", stdout)
	}
}
