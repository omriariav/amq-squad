package piruntime

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

var amqHandleRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// TeamIdentity is the AMQ identity of a built-in pi lead that supervises a
// squad scheduler. The scheduler and the lead's tools must address the same
// handle and the same session root.
type TeamIdentity struct {
	Handle   string
	Root     string
	BaseRoot string
	Session  string
}

// NewTeamIdentity builds the complete AMQ pin from a compiled seats file's
// base root and session. It refuses partial or relative identities instead of
// letting the pi lead fall back to its unrelated solo task store. The base
// root and session are taken as given (never trimmed), as the scheduler's
// normalization takes them, so the lead's root is the scheduler's.
func NewTeamIdentity(handle, baseRoot, session string) (TeamIdentity, error) {
	handle = strings.TrimSpace(handle)
	if !amqHandleRE.MatchString(handle) {
		return TeamIdentity{}, fmt.Errorf("lead handle %q is not a valid AMQ handle", handle)
	}
	if baseRoot == "" || !filepath.IsAbs(baseRoot) || filepath.Clean(baseRoot) != baseRoot {
		return TeamIdentity{}, fmt.Errorf("seats base root %q must be an absolute clean path", baseRoot)
	}
	if !amqHandleRE.MatchString(session) {
		return TeamIdentity{}, fmt.Errorf("seats session %q is not a valid AMQ session name", session)
	}
	return TeamIdentity{Handle: handle, Root: filepath.Join(baseRoot, session), BaseRoot: baseRoot, Session: session}, nil
}

// Overlay is the complete AMQ context passed to pi. ScrubEnv replaces any
// inherited values with these, so a stale owner shell pin cannot split the
// lead from the scheduler it started.
func (i TeamIdentity) Overlay() map[string]string {
	return map[string]string{
		"AM_ME":        i.Handle,
		"AM_ROOT":      i.Root,
		"AM_BASE_ROOT": i.BaseRoot,
		"AM_SESSION":   i.Session,
	}
}
