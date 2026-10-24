// Package hostcheck holds the doctor checks of plan 16.2 that read the
// owner's host files: the shell-profile check for exported key names
// (amendment 17), the diff of the owner's global agent config against the
// preflight copy (amendment 18), the codex-cli trust list and notify line
// (amendment 1, 16.8), sleep and the amq-remote login service (plan 8), and
// the grants file mode. Every check is read-only and never prints a secret
// value. All paths derive from the home directory the caller passes, so
// tests run on a temp HOME.
package hostcheck

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ProfileFiles are the shell startup files a provider tool shell may source.
var ProfileFiles = []string{
	".zshenv", ".zprofile", ".zshrc", ".zlogin",
	".bash_profile", ".bash_login", ".bashrc", ".profile",
	filepath.Join(".config", "fish", "config.fish"),
}

// Export is one exported key name in a shell profile. It never carries the value.
type Export struct {
	File string
	Line int
	Name string
}

var (
	exportRE  = regexp.MustCompile(`^\s*(?:export|declare\s+-x|typeset\s+-x)\s+(.*)$`)
	fishSetRE = regexp.MustCompile(`^\s*set\s+(?:-[a-zA-Z]*x[a-zA-Z]*\s+)+([A-Za-z_][A-Za-z0-9_]*)`)
)

// SecretName reports whether an environment name ends in _API_KEY, _TOKEN
// or _SECRET (R16).
func SecretName(name string) bool {
	n := strings.ToUpper(name)
	return strings.HasSuffix(n, "_API_KEY") || strings.HasSuffix(n, "_TOKEN") || strings.HasSuffix(n, "_SECRET")
}

// ShellProfileExports lists exported *_API_KEY, *_TOKEN and *_SECRET names in
// the owner's shell profile files. Provider tool shells source the profile,
// which bypasses the seat-exec scrub (amendment 17 (e)).
func ShellProfileExports(home string) []Export {
	var out []Export
	for _, rel := range ProfileFiles {
		path := filepath.Join(home, rel)
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for n := 1; sc.Scan(); n++ {
			for _, name := range exportedNames(sc.Text()) {
				if SecretName(name) {
					out = append(out, Export{File: path, Line: n, Name: name})
				}
			}
		}
		f.Close()
	}
	return out
}

func exportedNames(line string) []string {
	if t := strings.TrimSpace(line); t == "" || strings.HasPrefix(t, "#") {
		return nil
	}
	if m := fishSetRE.FindStringSubmatch(line); m != nil {
		return []string{m[1]}
	}
	m := exportRE.FindStringSubmatch(line)
	if m == nil {
		return nil
	}
	// "export A=1 B=two" or "export A B": take each name token; values are
	// skipped by only reading a name at a token start.
	var names []string
	rest := m[1]
	for _, tok := range splitTokens(rest) {
		name, _, _ := strings.Cut(tok, "=")
		if validName(name) {
			names = append(names, name)
		}
	}
	return names
}

func validName(s string) bool {
	if s == "" {
		return false
	}
	for i, c := range s {
		if !(c == '_' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || i > 0 && c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

// splitTokens splits on unquoted blanks and stops at an unquoted ';', '#',
// '&' or '|'.
func splitTokens(s string) []string {
	var out []string
	var cur strings.Builder
	quote := rune(0)
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for _, c := range s {
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
			cur.WriteRune(c)
		case c == '"' || c == '\'':
			quote = c
			cur.WriteRune(c)
		case c == ' ' || c == '\t':
			flush()
		case c == ';' || c == '#' || c == '&' || c == '|':
			flush()
			return out
		default:
			cur.WriteRune(c)
		}
	}
	flush()
	return out
}
