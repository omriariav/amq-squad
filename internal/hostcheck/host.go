package hostcheck

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

var pmsetSleepRE = regexp.MustCompile(`(?m)^\s*sleep\s+(\d+)(.*)$`)

// SleepPermitted reads `pmset -g` output (macOS). The host for 3.0 is the
// Mac, and doctor warns when system sleep is permitted (plan 8, R45).
// ok=false when the output has no sleep line.
func SleepPermitted(pmset string) (permitted bool, detail string, ok bool) {
	m := pmsetSleepRE.FindStringSubmatch(pmset)
	if m == nil {
		return false, "", false
	}
	minutes, _ := strconv.Atoi(m[1])
	note := strings.TrimSpace(m[2])
	switch {
	case minutes == 0:
		return false, "system sleep is off (pmset sleep 0)", true
	case strings.Contains(note, "sleep prevented by"):
		return false, fmt.Sprintf("system sleep after %d min, currently %s", minutes, strings.Trim(note, "()")), true
	}
	return true, fmt.Sprintf("system sleep after %d min is permitted; seats stop when the Mac sleeps (sudo pmset -c sleep 0, or run under caffeinate)", minutes), true
}

// LoginServices are the launchd agents that keep amq-remote up after login
// (plan 8, R45): any plist under ~/Library/LaunchAgents whose name mentions
// amq-remote.
func LoginServices(home string) []string {
	matches, _ := filepath.Glob(filepath.Join(home, "Library", "LaunchAgents", "*amq-remote*.plist"))
	return matches
}

// GrantsPath is ~/.amq-squad/grants.yaml.
func GrantsPath(home string) string { return filepath.Join(home, ".amq-squad", "grants.yaml") }

// GrantsMode reports the owner grants file mode and why it is untrusted:
// "" reason when the file is absent or trusted. It checks the file and its
// directory for a symlink and for group or world write; 3-1's
// grants.Insecure (which also checks the owner uid) replaces this check
// when 3-1 merges, with the same reason code.
func GrantsMode(path string) (mode string, present bool, reason string) {
	st, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return "", false, ""
	}
	if err != nil {
		return "", true, err.Error()
	}
	mode = fmt.Sprintf("%04o", st.Mode().Perm())
	if st.Mode()&os.ModeSymlink != 0 {
		return mode, true, path + " is a symlink"
	}
	if runtime.GOOS == "windows" {
		return mode, true, "" // no permission bits to check (3.0 hosts are macOS)
	}
	for _, p := range []string{path, filepath.Dir(path)} {
		info, err := os.Lstat(p)
		if err != nil {
			return mode, true, err.Error()
		}
		if info.Mode().Perm()&0o022 != 0 {
			return mode, true, fmt.Sprintf("%s is group- or world-writable (mode %04o)", p, info.Mode().Perm())
		}
	}
	return mode, true, ""
}

// ReasonGrantsInsecure is 3-1's reason code for an untrusted owner file.
const ReasonGrantsInsecure = "owner_grants_insecure"
