package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/amqexec"
)

// doctorInstalledAMQFloor is doctorMinAMQVersion: one floor for one binary
// (5-amq-A fix round), applied to amq, amq-remote and amq-owner-sign.
const doctorInstalledAMQFloor = doctorMinAMQVersion

// doctorAMQBelowFloor is the code of a floor row's fail (Codex A small (c)):
// below the floor, a prerelease of it, an unreadable version, or no amq.
const doctorAMQBelowFloor = "amq_below_floor"

// doctorAMQInstallLine is the install line a below-floor row prints.
const doctorAMQInstallLine = "amq upgrade --all -y (Homebrew: brew upgrade avivsinai/tap/amq)"

// doctorAMQBinaries are the installed AMQ binaries the floor applies to. amq
// is required; amq-remote (remote access) and amq-owner-sign (the
// Buzz-signed clear) only when installed, and a missing one is a warning.
var doctorAMQBinaries = []struct {
	name     string
	required bool
}{{"amq", true}, {"amq-remote", false}, {"amq-owner-sign", false}}

var doctorVersionToken = regexp.MustCompile(`v?\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?`)

// defaultToolVersion runs `<name> --version` from PATH (5 s bound) and
// returns the resolved path and the output. found=false when the name is not
// on PATH.
func defaultToolVersion(name string) (path, out string, found bool, err error) {
	path, lerr := exec.LookPath(name)
	if lerr != nil {
		return "", "", false, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "--version")
	// The suppression every amq child gets: no update notice or check.
	cmd.Env = amqexec.NoUpdateCheckEnv(envWithoutAMQIdentity(os.Environ()))
	b, err := cmd.Output()
	return path, string(b), true, err
}

// doctorCheckInstalledAMQFloor is one row per installed AMQ binary: at or
// above doctorInstalledAMQFloor is ok; below it, unparseable, or a required
// binary missing is a refusal row with the install line.
func doctorCheckInstalledAMQFloor(d doctorExecution) []doctorCheck {
	if d.ToolVersion == nil {
		return nil
	}
	var out []doctorCheck
	for _, b := range doctorAMQBinaries {
		name := b.name + " installed floor"
		path, raw, found, err := d.ToolVersion(b.name)
		switch {
		case !found && b.required:
			out = append(out, doctorCheck{Name: name, Status: doctorFail, Kind: doctorAMQBelowFloor, Detail: fmt.Sprintf("%s is not on PATH; install AMQ %s or later: %s", b.name, doctorInstalledAMQFloor, doctorAMQInstallLine)})
			continue
		case !found:
			out = append(out, doctorCheck{Name: name, Status: doctorWarn, Detail: fmt.Sprintf("%s is not installed (needed for remote access and the Buzz-signed clear); it must be %s or later: %s", b.name, doctorInstalledAMQFloor, doctorAMQInstallLine)})
			continue
		case err != nil:
			out = append(out, doctorCheck{Name: name, Status: doctorFail, Kind: doctorAMQBelowFloor, Detail: fmt.Sprintf("%s --version failed (%v); reinstall AMQ %s or later: %s", path, err, doctorInstalledAMQFloor, doctorAMQInstallLine)})
			continue
		}
		got := doctorVersionToken.FindString(strings.TrimSpace(raw))
		if got == "" {
			out = append(out, doctorCheck{Name: name, Status: doctorFail, Kind: doctorAMQBelowFloor, Detail: fmt.Sprintf("%s --version printed no version; reinstall AMQ %s or later: %s", path, doctorInstalledAMQFloor, doctorAMQInstallLine)})
			continue
		}
		if !semverMeetsStableFloor(got, doctorInstalledAMQFloor) {
			out = append(out, doctorCheck{Name: name, Status: doctorFail, Kind: doctorAMQBelowFloor, Detail: fmt.Sprintf("%s %s is below the installed floor %s; upgrade: %s", path, got, doctorInstalledAMQFloor, doctorAMQInstallLine)})
			continue
		}
		out = append(out, doctorCheck{Name: name, Status: doctorOK, Detail: fmt.Sprintf("%s %s (floor %s)", path, got, doctorInstalledAMQFloor)})
	}
	return out
}
