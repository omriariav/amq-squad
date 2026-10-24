package cli

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/omriariav/amq-squad/v3/internal/seatexec"
)

const seatExecUsage = `amq-squad seat-exec - the seat wrapper AMQ runs in front of a provider

Usage:
  amq-squad seat-exec --seat HANDLE [--] PROVIDER [ARGS...]

AMQ's launch API runs this wrapper; it is not run by hand. AM_ME and AM_ROOT
come from the environment, and --seat must equal AM_ME. There is no --root.
seat-exec verifies the AMQ root without following a symlink, checks the
staged launch record, the role.md and rules sha256, publishes role.md and
launch.json into the seat's extension directory, scrubs credentials from the
environment, sets AM_ME, AM_ROOT, AM_BASE_ROOT, AM_SESSION and
AMQ_SQUAD_BRANCH from the record only, proves one amq drain, and execs the
provider in place. It never adds a provider flag. A refusal exits 1 with one
stderr line naming its reason code.
`

func runSeatExec(args []string) error {
	seat, argv, err := parseSeatExecArgs(args)
	if err != nil {
		return err
	}
	if err := seatexec.Run(seatexec.Options{Seat: seat, Argv: argv}); err != nil {
		return UsageError(err.Error()) // exit 1, one stderr line
	}
	return nil
}

func parseSeatExecArgs(args []string) (string, []string, error) {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help") {
		fmt.Fprint(os.Stderr, seatExecUsage)
		return "", nil, flag.ErrHelp
	}
	seat := ""
	switch {
	case len(args) >= 2 && (args[0] == "--seat" || args[0] == "-seat"):
		seat, args = args[1], args[2:]
	case len(args) >= 1 && strings.HasPrefix(args[0], "--seat="):
		seat, args = strings.TrimPrefix(args[0], "--seat="), args[1:]
	default:
		return "", nil, usageErrorf("seat-exec takes --seat HANDLE first, then the provider argv")
	}
	if len(args) > 0 && args[0] == "--" {
		args = args[1:]
	}
	if len(args) == 0 {
		return "", nil, usageErrorf("seat-exec: no provider argv after --seat %s", seat)
	}
	if strings.HasPrefix(args[0], "-") {
		return "", nil, usageErrorf("seat-exec takes only --seat; got %q", args[0])
	}
	return seat, args, nil
}
