package hostcheck

import (
	"fmt"
	"path/filepath"
	"regexp"
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

// GrantsPath is ~/.amq-squad/grants.yaml under home (grants.OwnerPath for
// the user's own home).
func GrantsPath(home string) string { return filepath.Join(home, ".amq-squad", "grants.yaml") }

// LoginServices are the launchd agents that keep amq-remote up after login
// (plan 8, R45): any plist under ~/Library/LaunchAgents whose name mentions
// amq-remote.
func LoginServices(home string) []string {
	matches, _ := filepath.Glob(filepath.Join(home, "Library", "LaunchAgents", "*amq-remote*.plist"))
	return matches
}
