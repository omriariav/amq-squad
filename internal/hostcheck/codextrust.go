package hostcheck

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// CodexConfigPath is $CODEX_HOME/config.toml, else ~/.codex/config.toml.
// codex-cli owns this file and rewrites it at start (amendment 17 (d));
// amq-squad reads it and never writes it (R4, amendment 1).
func CodexConfigPath(home, codexHome string) string {
	if strings.TrimSpace(codexHome) != "" {
		return filepath.Join(codexHome, "config.toml")
	}
	return filepath.Join(home, ".codex", "config.toml")
}

var (
	projectHeaderRE = regexp.MustCompile(`^\s*\[\s*projects\s*\.\s*("(?:[^"\\]|\\.)*"|'[^']*')\s*\]\s*(#.*)?$`)
	tableHeaderRE   = regexp.MustCompile(`^\s*\[`)
	trustLevelRE    = regexp.MustCompile(`^\s*trust_level\s*=\s*"([^"]*)"`)
	notifyRE        = regexp.MustCompile(`^\s*notify\s*=\s*(.*)$`)
)

// CodexTrust is what doctor reads from codex-cli's config.
type CodexTrust struct {
	Trusted map[string]bool // project path -> trust_level "trusted"
	Notify  string          // the top-level notify value, "" when absent
}

// ParseCodexTrust reads the [projects."<path>"] trust_level entries and the
// top-level notify line. It is a reader for those two keys only, not TOML.
func ParseCodexTrust(text string) CodexTrust {
	ct := CodexTrust{Trusted: map[string]bool{}}
	current, topLevel := "", true
	for _, line := range strings.Split(text, "\n") {
		if m := projectHeaderRE.FindStringSubmatch(line); m != nil {
			current, topLevel = unquoteTOML(m[1]), false
			continue
		}
		if tableHeaderRE.MatchString(line) {
			current, topLevel = "", false
			continue
		}
		if topLevel {
			if m := notifyRE.FindStringSubmatch(line); m != nil {
				ct.Notify = strings.TrimSpace(m[1])
			}
		}
		if current != "" {
			if m := trustLevelRE.FindStringSubmatch(line); m != nil && m[1] == "trusted" {
				ct.Trusted[filepath.Clean(current)] = true
			}
		}
	}
	return ct
}

func unquoteTOML(s string) string {
	if strings.HasPrefix(s, "'") {
		return strings.Trim(s, "'")
	}
	if u, err := strconv.Unquote(s); err == nil {
		return u
	}
	return strings.Trim(s, `"`)
}

// ReadCodexTrust reads the config file; a missing file trusts nothing.
func ReadCodexTrust(path string) (CodexTrust, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return ParseCodexTrust(""), nil
	}
	if err != nil {
		return CodexTrust{}, err
	}
	return ParseCodexTrust(string(raw)), nil
}

// UntrustedRoots lists the roots codex-cli does not trust, before a Codex
// seat launches there (amendment 1). Only an exact [projects."<root>"]
// entry counts: a trusted parent is not assumed to cover a child, so the
// list can name a root codex would accept, never the other way round.
func (ct CodexTrust) UntrustedRoots(roots []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, root := range roots {
		if root = filepath.Clean(root); root == "." || root == "" || seen[root] {
			continue
		}
		seen[root] = true
		if !ct.Trusted[root] {
			out = append(out, root)
		}
	}
	return out
}

// NotifyFanoutName is the command name of the candidate notify fan-out
// (16.8): one script that runs AMQ's capture and then a second receiver.
const NotifyFanoutName = "amq-squad-codex-notify"

// HasNotifyFanout reports whether the owner-written notify line names the
// fan-out. doctor checks presence only (2b-3 ruling 2).
func (ct CodexTrust) HasNotifyFanout() bool {
	return strings.Contains(ct.Notify, NotifyFanoutName)
}

// TrustSteps is the text `amq-squad trust <repo>` prints: the owner-run
// steps to trust a repository in codex-cli and the candidate notify line.
// Nothing here writes a file.
func TrustSteps(repo, configPath, fanoutPath string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Trust %s in codex-cli. amq-squad never writes %s: codex-cli owns it.\n\n", repo, configPath)
	b.WriteString("Either open codex once in the repository and accept its folder-trust prompt:\n\n")
	fmt.Fprintf(&b, "  cd %s && codex\n\n", shellQuote(repo))
	fmt.Fprintf(&b, "or add these lines to %s yourself:\n\n", configPath)
	fmt.Fprintf(&b, "  [projects.%s]\n  trust_level = \"trusted\"\n\n", strconv.Quote(repo))
	b.WriteString("Candidate, not decided (plan 16.8): the Codex notify fan-out, so AMQ's session capture and a second\n")
	b.WriteString("receiver both run. If you adopt it, write this top-level line yourself (it replaces an existing notify line;\n")
	b.WriteString("keep AMQ's capture command inside the fan-out). doctor checks only that the line is present:\n\n")
	fmt.Fprintf(&b, "  notify = [%s]\n\n", strconv.Quote(fanoutPath))
	b.WriteString("Then run 'amq-squad doctor' to confirm the repository is no longer listed as untrusted.\n")
	return b.String()
}

func shellQuote(s string) string {
	if s != "" && strings.IndexFunc(s, func(r rune) bool {
		return !(r == '/' || r == '.' || r == '-' || r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z')
	}) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
