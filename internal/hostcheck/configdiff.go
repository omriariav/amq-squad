package hostcheck

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// OwnerConfig is one global agent config file that seat-exec denies writes
// to (R16, amendment 18) and the preflight copy 2b-2 writes before a launch
// (lead ruling: ~/.amq-squad/preflight/<name>).
type OwnerConfig struct {
	Name      string // the preflight file name
	Live      string
	Preflight string
}

// OwnerConfigs are the owner's global agent config files under home.
func OwnerConfigs(home string) []OwnerConfig {
	pre := PreflightDir(home)
	return []OwnerConfig{
		{Name: "claude-settings.json", Live: filepath.Join(home, ".claude", "settings.json"), Preflight: filepath.Join(pre, "claude-settings.json")},
		{Name: "codex-config.toml", Live: filepath.Join(home, ".codex", "config.toml"), Preflight: filepath.Join(pre, "codex-config.toml")},
	}
}

// PreflightDir is ~/.amq-squad/preflight.
func PreflightDir(home string) string { return filepath.Join(home, ".amq-squad", "preflight") }

// ConfigDiff is the result for one file.
type ConfigDiff struct {
	Config  OwnerConfig
	State   string   // same | changed | no_preflight | no_live | unreadable
	Lines   []string // "-old" / "+new" lines, secret values redacted
	Ignored int      // changed lines dropped as codex-cli's own bookkeeping
}

// DiffOwnerConfig compares the live file with its preflight copy.
func DiffOwnerConfig(c OwnerConfig) ConfigDiff {
	d := ConfigDiff{Config: c}
	pre, errPre := os.ReadFile(c.Preflight)
	live, errLive := os.ReadFile(c.Live)
	switch {
	case os.IsNotExist(errPre):
		d.State = "no_preflight"
		return d
	case errPre != nil || errLive != nil && !os.IsNotExist(errLive):
		d.State = "unreadable"
		return d
	case os.IsNotExist(errLive):
		d.State = "no_live"
		return d
	}
	codex := strings.HasSuffix(c.Live, ".toml")
	d.Lines, d.Ignored = diffLines(splitLines(string(pre)), splitLines(string(live)), codex)
	d.State = "same"
	if len(d.Lines) > 0 {
		d.State = "changed"
	}
	return d
}

func splitLines(s string) []string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// codexBookkeeping is the key codex-cli rewrites at every start
// (amendment 17 (d): [marketplaces.*] last_updated); a change there is not
// an owner or seat edit.
var codexBookkeeping = regexp.MustCompile(`^\s*last_updated\s*=`)

// diffLines is a longest-common-subsequence line diff. Inputs are small
// config files; the O(n*m) table is bounded by maxDiffLines.
func diffLines(a, b []string, codex bool) ([]string, int) {
	const maxDiffLines = 4000
	if len(a) > maxDiffLines || len(b) > maxDiffLines {
		return []string{fmt.Sprintf("~file too large to diff (%d and %d lines)", len(a), len(b))}, 0
	}
	lcs := make([][]int, len(a)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	var out []string
	ignored := 0
	emit := func(sign, line string) {
		if codex && codexBookkeeping.MatchString(line) {
			ignored++
			return
		}
		out = append(out, sign+RedactLine(line))
	}
	i, j := 0, 0
	for i < len(a) || j < len(b) {
		switch {
		case i < len(a) && j < len(b) && a[i] == b[j]:
			i, j = i+1, j+1
		case j < len(b) && (i == len(a) || lcs[i][j+1] >= lcs[i+1][j]):
			emit("+", b[j])
			j++
		default:
			emit("-", a[i])
			i++
		}
	}
	return out, ignored
}

var (
	keyValueRE = regexp.MustCompile(`^(\s*"?([A-Za-z0-9_.-]+)"?\s*[:=]\s*)(.*)$`)
	tokenRE    = regexp.MustCompile(`(?i)(sk-[a-z0-9_-]{8,}|gh[pousr]_[A-Za-z0-9]{16,}|xox[abp]-[A-Za-z0-9-]{8,}|AKIA[A-Z0-9]{12,}|eyJ[A-Za-z0-9_-]{16,})`)
)

// RedactLine hides the value of a key whose name looks secret, and any
// value that looks like a known token format, in one diff line.
func RedactLine(line string) string {
	if m := keyValueRE.FindStringSubmatch(line); m != nil {
		key := strings.ToUpper(m[2])
		if SecretName(key) || strings.Contains(key, "PASSWORD") || strings.HasSuffix(key, "KEY") || key == "TOKEN" || key == "SECRET" {
			return m[1] + "<redacted>"
		}
	}
	return tokenRE.ReplaceAllString(line, "<redacted>")
}
