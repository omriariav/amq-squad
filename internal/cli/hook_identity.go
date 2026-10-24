package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// The hook verbs of plan 4.2 (context, guard, evidence ingest) are
// non-interactive, read the injected identity (AM_ME, AM_ROOT = the session
// root, AM_SESSION) and flags only, never discover a team (R52), and finish
// fast. Notes: docs/v3-spike/2a3-notes.md.

// hookError is a usage or identity error of a hook verb: not a UsageError, so
// main exits 2 and prints it on one stderr line.
type hookError string

func (e hookError) Error() string { return string(e) }

func hookErrorf(format string, args ...any) error { return hookError(fmt.Sprintf(format, args...)) }

var (
	hookStdin         io.Reader = os.Stdin // test seams
	hookNow                     = time.Now
	hookHandlePattern           = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
)

const hookMaxPayload = 1 << 20 // stdin cap for one hook payload

type hookIdentity struct {
	Handle   string `json:"handle"`
	Root     string `json:"root"`
	Session  string `json:"session,omitempty"`
	AgentDir string `json:"-"`
}

type hookFlags struct {
	fs                *flag.FlagSet
	me, root, session *string
	jsonOut           *bool
}

func newHookFlags(name, usage string) hookFlags {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	return hookFlags{
		fs:      fs,
		me:      fs.String("me", "", "seat handle (must equal AM_ME when AM_ME is set)"),
		root:    fs.String("root", "", "session root (default AM_ROOT; must equal it when set)"),
		session: fs.String("session", "", "session name (default AM_SESSION; must equal it when set)"),
		jsonOut: fs.Bool("json", false, "emit JSON"),
	}
}

// parse parses args; flag.ErrHelp passes through (exit 0), every other
// failure is a hookError (exit 2).
func (h hookFlags) parse(args []string) error {
	if err := h.fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			h.fs.Usage()
			return err
		}
		return hookErrorf("%s: %v", h.fs.Name(), err)
	}
	if h.fs.NArg() > 0 {
		return hookErrorf("%s takes no positional arguments; got %q", h.fs.Name(), h.fs.Arg(0))
	}
	return nil
}

// identity resolves the seat from flags and the injected environment. A flag
// may fill an unset variable but never contradicts a set one.
func (h hookFlags) identity() (hookIdentity, error) {
	var vals [3]string
	for i, p := range [3][3]string{{"me", *h.me, "AM_ME"}, {"root", *h.root, "AM_ROOT"}, {"session", *h.session, "AM_SESSION"}} {
		flagValue, envValue := strings.TrimSpace(p[1]), strings.TrimSpace(os.Getenv(p[2]))
		if flagValue != "" && envValue != "" && flagValue != envValue {
			return hookIdentity{}, hookErrorf("%s: --%s %q disagrees with %s=%q", h.fs.Name(), p[0], flagValue, p[2], envValue)
		}
		vals[i] = envValue
		if flagValue != "" {
			vals[i] = flagValue
		}
	}
	me, root := vals[0], vals[1]
	if !hookHandlePattern.MatchString(me) {
		return hookIdentity{}, hookErrorf("%s: seat handle %q is missing or invalid (pass --me or set AM_ME)", h.fs.Name(), me)
	}
	if root == "" || !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return hookIdentity{}, hookErrorf("%s: session root %q must be a clean absolute path (AM_ROOT)", h.fs.Name(), root)
	}
	agentDir := filepath.Join(root, "agents", me)
	if st, err := os.Stat(agentDir); err != nil || !st.IsDir() {
		return hookIdentity{}, hookErrorf("%s: no mailbox for %q under %s", h.fs.Name(), me, root)
	}
	return hookIdentity{Handle: me, Root: root, Session: vals[2], AgentDir: agentDir}, nil
}

// readHookPayload reads one hook payload from stdin, refusing more than
// hookMaxPayload bytes.
func readHookPayload(verb string) ([]byte, error) {
	raw, err := io.ReadAll(io.LimitReader(hookStdin, hookMaxPayload+1))
	if err != nil {
		return nil, hookErrorf("%s: read stdin: %v", verb, err)
	}
	if len(raw) > hookMaxPayload {
		return nil, hookErrorf("%s: payload exceeds %d bytes", verb, hookMaxPayload)
	}
	return raw, nil
}

// hookExtPath is a path under the session root's amq-squad extension dir.
func hookExtPath(root string, parts ...string) string {
	return filepath.Join(append([]string{root, "extensions", "amq-squad"}, parts...)...)
}
