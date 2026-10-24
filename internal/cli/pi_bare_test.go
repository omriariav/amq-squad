package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/piruntime"
)

func setTTY(t *testing.T, tty bool) {
	old := stdinIsTerminal
	t.Cleanup(func() { stdinIsTerminal = old })
	stdinIsTerminal = func() bool { return tty }
}

// Ruling 1: bare `amq-squad` with the payload and a TTY starts the lead.
func TestBareWithPayloadAndTTYStartsLead(t *testing.T) {
	call, _ := piFixture(t)
	chdir(t, t.TempDir())
	setTTY(t, true)
	if err := Run(nil, "test"); err != nil {
		t.Fatal(err)
	}
	if call.path == "" || call.getenv("AM_ME") != "lead" || call.getenv("AM_SESSION") != "solo" {
		t.Fatalf("bare did not start the solo lead: path %q", call.path)
	}
}

func TestBareWithoutTTYPrintsHelpAndHint(t *testing.T) {
	call, _ := piFixture(t)
	chdir(t, t.TempDir())
	setTTY(t, false)
	stdout, _, err := captureOutput(t, func() error { return Run(nil, "test") })
	if err != nil {
		t.Fatal(err)
	}
	if call.path != "" || !strings.Contains(stdout, "Usage:") || !strings.Contains(stdout, "or `amq-squad lead`, to start the lead") {
		t.Fatalf("no-TTY bare run: exec %q, output:\n%s", call.path, stdout)
	}
}

// Review item 5: /dev/null is a character device but not a terminal; the
// TTY check is the termios ioctl, so `amq-squad </dev/null` prints the help.
func TestDevNullIsNotATerminal(t *testing.T) {
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if isTerminal(int(f.Fd())) {
		t.Fatal("/dev/null reads as a terminal")
	}
	old := os.Stdin
	os.Stdin = f
	t.Cleanup(func() { os.Stdin = old })
	if stdinIsTerminal() {
		t.Fatal("stdin from /dev/null reads as a terminal")
	}
	call, _ := piFixture(t)
	chdir(t, t.TempDir())
	if _, _, err := captureOutput(t, func() error { return Run(nil, "test") }); err != nil {
		t.Fatal(err)
	}
	if call.path != "" {
		t.Fatal("amq-squad </dev/null started the lead")
	}
}

func TestBareWithoutPayloadPrintsHelpAndHint(t *testing.T) {
	old := piLoad
	t.Cleanup(func() { piLoad = old })
	piLoad = func() (piruntime.Embedded, error) { return piruntime.Embedded{}, piruntime.ErrNotEmbedded }
	setTTY(t, true)
	chdir(t, t.TempDir())
	stdout, _, err := captureOutput(t, func() error { return Run(nil, "test") })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "Usage:") || !strings.Contains(stdout, "no built-in pi") {
		t.Fatalf("payload-less bare run output:\n%s", stdout)
	}
}
