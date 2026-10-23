package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/omriariav/amq-squad/v3/internal/flock"
	squadnamespace "github.com/omriariav/amq-squad/v3/internal/namespace"
	"github.com/omriariav/amq-squad/v3/internal/team"
)

// 3.0 (2a-2, C7): v2 namespace migration is gone, so bindings carry the
// generation "none". The writer admission stays (manifest extract): a shared
// endpoint lock, then the caller's context must re-resolve unchanged under it.
const namespaceGenerationNone = "none"

// openNamespaceAdmissionLock opens a lock without following links; only
// namespace_open_unix.go installs it, so elsewhere admission is refused, as in v2.
var openNamespaceAdmissionLock func(projectDir, lockPath string) (*os.File, error)

// namespaceWriterBeforeAdmission is a test seam between resolution and admission.
var namespaceWriterBeforeAdmission = func(string, string, string) error { return nil }

type namespaceAdmissionLocks struct{ lock *flock.Exclusive }

func (l *namespaceAdmissionLocks) close() {
	if l.lock != nil {
		_ = l.lock.Close()
		l.lock = nil
	}
}

func acquireRevalidatedContextWriter(initial contextResolution, resolve func() (contextResolution, error)) (contextResolution, *namespaceAdmissionLocks, error) {
	return acquireRevalidatedWriter(initial.ProjectDir, initial.Profile, initial.Session, "context", initial, resolve, validateReResolvedContext)
}

func acquireRevalidatedAMQWriter(initial amqContext, resolve func() (amqContext, error)) (amqContext, *namespaceAdmissionLocks, error) {
	return acquireRevalidatedWriter(initial.ProjectDir, initial.Profile, initial.Session, "AMQ context", initial, resolve, validateReResolvedAMQContext)
}

func acquireRevalidatedWriter[T any](projectDir, profile, session, what string, initial T, resolve func() (T, error), validate func(T, T) error) (T, *namespaceAdmissionLocks, error) {
	var zero T
	admission, err := acquireNamespaceWriterAdmission(projectDir, profile, session)
	if err != nil {
		return zero, nil, err
	}
	current, err := resolve()
	if err == nil {
		err = validate(initial, current)
	} else {
		err = fmt.Errorf("namespace writer refused: %s re-resolution under admission failed: %w", what, err)
	}
	if err != nil {
		admission.close()
		return zero, nil, err
	}
	return current, admission, nil
}

func validateReResolvedContext(initial, current contextResolution) error {
	changes := compareNamespaceIdentity(initial.ProjectDir, initial.Profile, initial.Session, initial.Root, initial.Handle, current.ProjectDir, current.Profile, current.Session, current.Root, current.Handle)
	if filepath.Clean(initial.BaseRoot) != filepath.Clean(current.BaseRoot) {
		changes = append(changes, fmt.Sprintf("base_root %q -> %q", initial.BaseRoot, current.BaseRoot))
	}
	return refuseNamespaceChange("namespace writer refused: context", changes)
}

func validateReResolvedAMQContext(initial, current amqContext) error {
	changes := compareNamespaceIdentity(initial.ProjectDir, initial.Profile, initial.Session, initial.Root, initial.Me, current.ProjectDir, current.Profile, current.Session, current.Root, current.Me)
	initialBase, currentBase := absoluteAMQRoot(initial.ProjectDir, initial.Env.BaseRoot), absoluteAMQRoot(current.ProjectDir, current.Env.BaseRoot)
	if filepath.Clean(initialBase) != filepath.Clean(currentBase) {
		changes = append(changes, fmt.Sprintf("base_root %q -> %q", initialBase, currentBase))
	}
	if initial.PinMode != current.PinMode {
		changes = append(changes, fmt.Sprintf("pin_mode %d -> %d", initial.PinMode, current.PinMode))
	}
	return refuseNamespaceChange("namespace writer refused: AMQ context", changes)
}

func refuseNamespaceChange(what string, changes []string) error {
	if len(changes) == 0 {
		return nil
	}
	return fmt.Errorf("%s changed before admission (%s); retry against the current namespace", what, strings.Join(changes, ", "))
}

func compareNamespaceIdentity(initialProject, initialProfile, initialSession, initialRoot, initialHandle, currentProject, currentProfile, currentSession, currentRoot, currentHandle string) []string {
	var changes []string
	add := func(name, a, b string, same bool) {
		if !same {
			changes = append(changes, fmt.Sprintf("%s %q -> %q", name, a, b))
		}
	}
	add("project", initialProject, currentProject, filepath.Clean(initialProject) == filepath.Clean(currentProject))
	add("profile", initialProfile, currentProfile, squadnamespace.ProfilesEqual(initialProfile, currentProfile))
	add("session", initialSession, currentSession, strings.TrimSpace(initialSession) == strings.TrimSpace(currentSession))
	add("root", initialRoot, currentRoot, filepath.Clean(initialRoot) == filepath.Clean(currentRoot))
	add("handle", initialHandle, currentHandle, strings.TrimSpace(initialHandle) == strings.TrimSpace(currentHandle))
	return changes
}

func validateReResolvedEndpoint(operation string, initial, current squadnamespace.Ref, initialHandle, currentHandle string) error {
	changes := compareNamespaceIdentity(initial.TeamHome, initial.Profile, initial.Session, initial.AMQRoot, initialHandle, current.TeamHome, current.Profile, current.Session, current.AMQRoot, currentHandle)
	return refuseNamespaceChange(operation+" refused: namespace target", changes)
}

func acquireNamespaceWriterAdmission(projectDir, profile, session string) (*namespaceAdmissionLocks, error) {
	session = strings.TrimSpace(session)
	if err := team.ValidateSessionName(session); err != nil {
		return nil, fmt.Errorf("namespace writer admission requires valid session: %w", err)
	}
	if err := namespaceWriterBeforeAdmission(projectDir, squadnamespace.NormalizeProfile(profile), session); err != nil {
		return nil, err
	}
	if openNamespaceAdmissionLock == nil {
		return nil, fmt.Errorf("namespace writer admission refused: contained no-follow lock creation is unsupported on this platform")
	}
	path := filepath.Join(projectDir, team.DirName, "namespace-admission", squadnamespace.NormalizeProfile(profile), session+".lock")
	f, err := openNamespaceAdmissionLock(projectDir, path)
	if err != nil {
		return nil, fmt.Errorf("namespace writer admission refused: %w", err)
	}
	lock, acquired, err := flock.TrySharedFile(f)
	if err == nil && !acquired {
		err = fmt.Errorf("namespace writer admission refused: %s is held exclusively", path)
	}
	if err != nil {
		return nil, err
	}
	return &namespaceAdmissionLocks{lock: lock}, nil
}
