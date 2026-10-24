package seatcommit

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// git runs the git CLI for the harness. Every call disables hooks and the
// fsmonitor and untracked caches with command-line config (which outranks
// the repository's config, so a seat-written .git/config cannot add a
// hook, a reference-transaction hook or an fsmonitor command to the
// harness's git), and runs with an environment that carries no GIT_*
// redirection from the caller (GIT_DIR, GIT_INDEX_FILE, GIT_CONFIG_*...).
type git struct {
	bin string
	dir string
	env []string
}

// safeConfig is prepended to every git call.
func safeConfig() []string {
	return []string{"-c", "core.hooksPath=" + os.DevNull, "-c", "core.fsmonitor=false", "-c", "core.untrackedCache=false", "-c", "advice.detachedHead=false"}
}

// keptGitVars are the GIT_* names the harness keeps from its environment:
// the transport's credential and SSH plumbing for the push. Everything else
// that starts with GIT_ is dropped.
var keptGitVars = map[string]bool{"GIT_SSH": true, "GIT_SSH_COMMAND": true, "GIT_SSH_VARIANT": true, "GIT_ASKPASS": true}

// harnessEnv is the caller's environment minus GIT_* redirection, plus a
// fixed locale and no terminal prompt.
func harnessEnv(parent []string) []string {
	out := make([]string, 0, len(parent)+3)
	for _, kv := range parent {
		name, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(name, "GIT_") && !keptGitVars[name] {
			continue
		}
		if name == "LC_ALL" || name == "LANG" {
			continue
		}
		out = append(out, kv)
	}
	return append(out, "LC_ALL=C", "LANG=C", "GIT_TERMINAL_PROMPT=0")
}

// gitError is a failed git call with its stderr.
type gitError struct {
	args   []string
	code   int
	stderr string
}

func (e *gitError) Error() string {
	return fmt.Sprintf("git %s: exit %d: %s", strings.Join(e.args, " "), e.code, strings.TrimSpace(e.stderr))
}

// run executes git with the safe config; extra environment entries are
// appended (GIT_INDEX_FILE for the temporary index, the identity).
func (g git) run(stdin []byte, extra []string, args ...string) ([]byte, error) {
	cmd := exec.Command(g.bin, append(safeConfig(), args...)...)
	cmd.Dir = g.dir
	cmd.Env = append(append([]string(nil), g.env...), extra...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if err != nil {
		code := -1
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		}
		return stdout.Bytes(), &gitError{args: args, code: code, stderr: stderr.String() + errText(err, code)}
	}
	return stdout.Bytes(), nil
}

func errText(err error, code int) string {
	if code >= 0 {
		return ""
	}
	return err.Error()
}

// line runs git and returns its trimmed stdout.
func (g git) line(args ...string) (string, error) {
	out, err := g.run(nil, nil, args...)
	return strings.TrimSpace(string(out)), err
}

// numstat is one changed path of a diff with its line counts; Binary is set
// when git counts no lines.
type numstat struct {
	Path           string
	Added, Deleted int
	Binary         bool
}

// parseNumstat reads `diff-tree --numstat -z --no-renames` output: records
// "added\tdeleted\tpath\0".
func parseNumstat(out []byte) ([]numstat, error) {
	var list []numstat
	for _, rec := range strings.Split(string(out), "\x00") {
		if rec == "" {
			continue
		}
		parts := strings.SplitN(rec, "\t", 3)
		if len(parts) != 3 || parts[2] == "" {
			return nil, fmt.Errorf("unexpected numstat record %q", rec)
		}
		n := numstat{Path: parts[2]}
		if parts[0] == "-" && parts[1] == "-" {
			n.Binary = true
		} else {
			a, errA := strconv.Atoi(parts[0])
			d, errD := strconv.Atoi(parts[1])
			if errA != nil || errD != nil {
				return nil, fmt.Errorf("unexpected numstat record %q", rec)
			}
			n.Added, n.Deleted = a, d
		}
		list = append(list, n)
	}
	return list, nil
}

// rawEntry is one record of `diff-tree --raw -z --no-renames`.
type rawEntry struct {
	OldMode, NewMode, Status, Path string
}

// parseRaw reads ":oldmode newmode oldsha newsha status\0path\0" records.
func parseRaw(out []byte) ([]rawEntry, error) {
	fields := strings.Split(string(out), "\x00")
	var list []rawEntry
	for i := 0; i < len(fields); i++ {
		head := fields[i]
		if head == "" {
			continue
		}
		if !strings.HasPrefix(head, ":") || i+1 >= len(fields) {
			return nil, fmt.Errorf("unexpected raw diff record %q", head)
		}
		meta := strings.Fields(strings.TrimPrefix(head, ":"))
		if len(meta) != 5 {
			return nil, fmt.Errorf("unexpected raw diff record %q", head)
		}
		i++
		list = append(list, rawEntry{OldMode: meta[0], NewMode: meta[1], Status: meta[4], Path: fields[i]})
	}
	return list, nil
}
