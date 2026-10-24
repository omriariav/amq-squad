// Package seatexec is `amq-squad seat-exec`, the wrapper AMQ runs in front of
// every Claude, Codex and pi seat (plan 4.3, R4, R16, R52). It takes only
// --seat; AM_ME and AM_ROOT come from the environment AMQ sets.
package seatexec

import (
	"context"
	"github.com/omriariav/amq-squad/v3/internal/atomicfile"
	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/procinfo"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Options carries the inputs of one seat-exec run; zero fields take the
// process defaults (tests replace them).
type Options struct {
	Seat     string
	Argv     []string // the provider argv after --seat
	Env      []string
	Home     string
	UID      int
	Now      func() time.Time
	LookPath func(string) (string, error)
	RunAMQ   func(ctx context.Context, amq string, args, env []string, dir string) ([]byte, error)
	Exec     func(path string, argv, env []string) error
	// StartToken reads a process start stamp (procinfo.StartToken).
	StartToken func(pid int) (string, bool)
	// SendProof sends the seat's proof to the launcher (default: the Unix
	// socket named in the staged ticket).
	SendProof func(t launch.ProofTicket, msg launch.ProofMessage) (launch.ProofReply, error)
	// MutateEnv is a test seam applied to the final environment before the
	// identity proof (the S6 finding A case removes or changes AM_SESSION here).
	MutateEnv func([]string) []string
	// BeforeRecheck is a test seam run after the identity proof and before
	// the last re-hash of role and rules.
	BeforeRecheck func()
}

func (o Options) withDefaults() Options {
	if o.Env == nil {
		o.Env = os.Environ()
	}
	if o.Home == "" {
		o.Home, _ = os.UserHomeDir()
	}
	if o.UID == 0 && os.Getuid() != 0 {
		o.UID = os.Getuid()
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.LookPath == nil {
		o.LookPath = exec.LookPath
	}
	if o.RunAMQ == nil {
		o.RunAMQ = runAMQ
	}
	if o.Exec == nil {
		o.Exec = execProvider
	}
	if o.StartToken == nil {
		o.StartToken = procinfo.StartToken
	}
	if o.SendProof == nil {
		o.SendProof = sendProofDefault(o.UID)
	}
	return o
}

var handlePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// PreflightFiles maps the owner's global agent config to its preflight copy
// name under ~/.amq-squad/preflight/ (the shared contract with 2b-3's
// doctor, which diffs the live file against the copy; plan 4.3, R16).
var PreflightFiles = map[string]string{
	".claude/settings.json": "claude-settings.json",
	".codex/config.toml":    "codex-config.toml",
}

// PreflightDir is ~/.amq-squad/preflight.
func PreflightDir(home string) string { return filepath.Join(home, ".amq-squad", "preflight") }

// WritePreflight copies each global agent config file that exists into the
// preflight directory (0600 in 0700). The launch refreshes the copies once
// before it applies (refresh true); seat-exec only fills a missing copy, so
// a later seat start never hides a change made after the launch.
func WritePreflight(home string, refresh bool) error {
	if home == "" {
		return nil
	}
	dir := PreflightDir(home)
	for src, name := range PreflightFiles {
		dst := filepath.Join(dir, name)
		if _, err := os.Lstat(dst); err == nil && !refresh {
			continue
		}
		data, err := os.ReadFile(filepath.Join(home, src))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return refusef("preflight_failed", "read %s: %v", src, err)
		}
		// the shared durable writer: an exclusive random temp per writer
		// (seats start concurrently), fsync, rename
		if err = os.MkdirAll(dir, 0o700); err == nil {
			err = atomicfile.Write(dst, data, 0o600)
		}
		if err != nil {
			return refusef("preflight_failed", "copy %s: %v", src, err)
		}
	}
	return nil
}

func runAMQ(ctx context.Context, amq string, args, env []string, dir string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, amq, args...)
	cmd.Env, cmd.Dir = env, dir
	return cmd.CombinedOutput()
}

func writePreflight(home string) error { return WritePreflight(home, false) }

// ConfigChange is one global agent config file that differs from its
// preflight copy.
type ConfigChange struct {
	File, Preflight, Detail string
}

// PreflightDiff compares the owner's global agent config with the preflight
// copies (amendment 28: for Claude seats the seat commit step runs this at
// every seat exit and fails the task on a change; milestone B wires it). A
// file present on one side only is a change too; neither side present is
// not.
func PreflightDiff(home string) ([]ConfigChange, error) {
	var out []ConfigChange
	for _, src := range []string{".claude/settings.json", ".codex/config.toml"} {
		live, copyPath := filepath.Join(home, src), filepath.Join(PreflightDir(home), PreflightFiles[src])
		a, errA := os.ReadFile(live)
		b, errB := os.ReadFile(copyPath)
		switch {
		case os.IsNotExist(errA) && os.IsNotExist(errB):
			continue
		case errA != nil && !os.IsNotExist(errA):
			return nil, errA
		case errB != nil && !os.IsNotExist(errB):
			return nil, errB
		case os.IsNotExist(errA) || os.IsNotExist(errB):
			out = append(out, ConfigChange{File: live, Preflight: copyPath, Detail: "present on one side only"})
		case string(a) != string(b):
			out = append(out, ConfigChange{File: live, Preflight: copyPath, Detail: "content differs from the preflight copy"})
		}
	}
	return out, nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func trimOut(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 400 {
		s = s[:400] + "..."
	}
	return s
}
