package cli

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/flock"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

const (
	externalOrchestratorRegistrySchema = 1
	externalOrchestratorRegistryDir    = "external-orchestrators"
)

type externalOrchestratorRegistrationState string

const (
	externalOrchestratorStatePlanned          externalOrchestratorRegistrationState = "planned"
	externalOrchestratorStateMailboxInvoked   externalOrchestratorRegistrationState = "mailbox_invoked"
	externalOrchestratorStateMailboxUncertain externalOrchestratorRegistrationState = "mailbox_uncertain"
	externalOrchestratorStateMailboxVerified  externalOrchestratorRegistrationState = "mailbox_verified"
	externalOrchestratorStateRuntimeVerified  externalOrchestratorRegistrationState = "runtime_verified"
	externalOrchestratorStateRegistered       externalOrchestratorRegistrationState = "registered"
	externalOrchestratorStateStale            externalOrchestratorRegistrationState = "stale"
	externalOrchestratorStateDead             externalOrchestratorRegistrationState = "dead"
)

// externalOrchestratorScope is the immutable registry key. External
// orchestrators are companions to one exact project/profile/session and never
// occupy Team.Members or become the configured goal authority.
type externalOrchestratorScope struct {
	ProjectDir string `json:"project_dir"`
	Profile    string `json:"profile"`
	Session    string `json:"session"`
	Handle     string `json:"handle"`
}

// externalOrchestratorRuntimeIdentity distinguishes replacements for one
// scoped handle. These fields come from the caller's current tmux/terminal
// identity; a stale/dead generation must be terminalized before a different
// runtime identity can become the current generation.
type externalOrchestratorRuntimeIdentity struct {
	TmuxSession string `json:"tmux_session,omitempty"`
	WindowID    string `json:"window_id,omitempty"`
	WindowName  string `json:"window_name,omitempty"`
	PaneID      string `json:"pane_id"`
	TTY         string `json:"tty,omitempty"`
}

type externalOrchestratorIdentity struct {
	Scope   externalOrchestratorScope           `json:"scope"`
	Runtime externalOrchestratorRuntimeIdentity `json:"runtime"`
}

// externalOrchestratorTransitionEvidence is deliberately registration-only.
// Later goal/mailbox wiring can persist its typed receipt boundary here, but
// nothing in this registry grants goal authority or performs an external
// action itself.
type externalOrchestratorTransitionEvidence struct {
	AttemptID     string `json:"attempt_id,omitempty"`
	CanonicalRoot string `json:"canonical_root,omitempty"`
	MailboxPath   string `json:"mailbox_path,omitempty"`
	ReceiptPath   string `json:"receipt_path,omitempty"`
	Outcome       string `json:"outcome,omitempty"`
	WakePID       int    `json:"wake_pid,omitempty"`
	LaunchPath    string `json:"launch_path,omitempty"`
	Detail        string `json:"detail,omitempty"`
}

type externalOrchestratorStateTransition struct {
	From     externalOrchestratorRegistrationState  `json:"from,omitempty"`
	To       externalOrchestratorRegistrationState  `json:"to"`
	At       time.Time                              `json:"at"`
	Evidence externalOrchestratorTransitionEvidence `json:"evidence,omitempty"`
}

type externalOrchestratorRegistration struct {
	ID            string                                `json:"id"`
	Generation    uint64                                `json:"generation"`
	Identity      externalOrchestratorIdentity          `json:"identity"`
	State         externalOrchestratorRegistrationState `json:"state"`
	Authoritative bool                                  `json:"authoritative"`
	CreatedAt     time.Time                             `json:"created_at"`
	UpdatedAt     time.Time                             `json:"updated_at"`
	Transitions   []externalOrchestratorStateTransition `json:"transitions"`
}

type externalOrchestratorRegistry struct {
	SchemaVersion     int                                `json:"schema_version"`
	Scope             externalOrchestratorScope          `json:"scope"`
	CurrentGeneration uint64                             `json:"current_generation"`
	UpdatedAt         time.Time                          `json:"updated_at"`
	Registrations     []externalOrchestratorRegistration `json:"registrations"`
}

var externalOrchestratorRegistryFault = func(string) error { return nil }
var externalOrchestratorRegistryFileSync = func(f *os.File) error { return f.Sync() }
var externalOrchestratorRegistryDirectorySync = syncExternalOrchestratorRegistryDirectory
var externalOrchestratorRegistryContainmentHook = func(string, string) error { return nil }

func newExternalOrchestratorScope(projectDir, profile, session, handle string) (externalOrchestratorScope, error) {
	projectDir = strings.TrimSpace(projectDir)
	if projectDir == "" {
		return externalOrchestratorScope{}, fmt.Errorf("external orchestrator registry requires project_dir")
	}
	abs, err := filepath.Abs(projectDir)
	if err != nil {
		return externalOrchestratorScope{}, fmt.Errorf("resolve external orchestrator project: %w", err)
	}
	if evaluated, evalErr := filepath.EvalSymlinks(abs); evalErr == nil {
		abs = evaluated
	} else if !os.IsNotExist(evalErr) {
		return externalOrchestratorScope{}, fmt.Errorf("resolve external orchestrator project identity: %w", evalErr)
	}
	profile = squadnamespace.NormalizeProfile(profile)
	if err := team.ValidateProfileName(profile); err != nil {
		return externalOrchestratorScope{}, err
	}
	session = strings.TrimSpace(session)
	if err := team.ValidateSessionName(session); err != nil {
		return externalOrchestratorScope{}, err
	}
	handle = strings.TrimSpace(handle)
	if err := team.ValidateHandle(handle); err != nil {
		return externalOrchestratorScope{}, err
	}
	return externalOrchestratorScope{ProjectDir: filepath.Clean(abs), Profile: profile, Session: session, Handle: handle}, nil
}

func normalizeExternalOrchestratorIdentity(identity externalOrchestratorIdentity) (externalOrchestratorIdentity, error) {
	scope, err := newExternalOrchestratorScope(identity.Scope.ProjectDir, identity.Scope.Profile, identity.Scope.Session, identity.Scope.Handle)
	if err != nil {
		return externalOrchestratorIdentity{}, err
	}
	runtimeIdentity := externalOrchestratorRuntimeIdentity{
		TmuxSession: strings.TrimSpace(identity.Runtime.TmuxSession),
		WindowID:    strings.TrimSpace(identity.Runtime.WindowID),
		WindowName:  strings.TrimSpace(identity.Runtime.WindowName),
		PaneID:      strings.TrimSpace(identity.Runtime.PaneID),
		TTY:         strings.TrimSpace(identity.Runtime.TTY),
	}
	if runtimeIdentity.PaneID == "" {
		return externalOrchestratorIdentity{}, fmt.Errorf("external orchestrator registry requires exact pane_id")
	}
	return externalOrchestratorIdentity{Scope: scope, Runtime: runtimeIdentity}, nil
}

func normalizeExternalOrchestratorEvidence(e externalOrchestratorTransitionEvidence) (externalOrchestratorTransitionEvidence, error) {
	if e.WakePID < 0 {
		return externalOrchestratorTransitionEvidence{}, fmt.Errorf("external orchestrator evidence wake_pid cannot be negative")
	}
	e.AttemptID = strings.TrimSpace(e.AttemptID)
	e.CanonicalRoot = strings.TrimSpace(e.CanonicalRoot)
	e.MailboxPath = strings.TrimSpace(e.MailboxPath)
	e.ReceiptPath = strings.TrimSpace(e.ReceiptPath)
	e.Outcome = strings.TrimSpace(e.Outcome)
	e.LaunchPath = strings.TrimSpace(e.LaunchPath)
	e.Detail = strings.TrimSpace(e.Detail)
	return e, nil
}

func externalOrchestratorRegistryPath(scope externalOrchestratorScope) string {
	return filepath.Join(scope.ProjectDir, team.DirName, externalOrchestratorRegistryDir, scope.Profile, scope.Session, scope.Handle, "registry.json")
}

func beginExternalOrchestratorRegistration(identity externalOrchestratorIdentity, now time.Time) (registration externalOrchestratorRegistration, replayed bool, retErr error) {
	identity, err := normalizeExternalOrchestratorIdentity(identity)
	if err != nil {
		return externalOrchestratorRegistration{}, false, err
	}
	now = now.UTC()
	if now.IsZero() {
		return externalOrchestratorRegistration{}, false, fmt.Errorf("external orchestrator registration requires a non-zero timestamp")
	}
	err = withExternalOrchestratorRegistryLock(identity.Scope, func(registryFS *externalOrchestratorRegistryFS) error {
		registry, found, err := loadExternalOrchestratorRegistry(registryFS)
		if err != nil {
			return err
		}
		if !found {
			registry = externalOrchestratorRegistry{SchemaVersion: externalOrchestratorRegistrySchema, Scope: identity.Scope}
		}
		if len(registry.Registrations) > 0 {
			current := registry.Registrations[len(registry.Registrations)-1]
			if current.Identity == identity && current.State != externalOrchestratorStateStale && current.State != externalOrchestratorStateDead {
				registration, replayed = current, true
				return nil
			}
			if current.State != externalOrchestratorStateStale && current.State != externalOrchestratorStateDead {
				return fmt.Errorf("external orchestrator generation %d is still %s for pane %s; mark it stale/dead before replacing runtime identity", current.Generation, current.State, current.Identity.Runtime.PaneID)
			}
			if now.Before(current.UpdatedAt) {
				return fmt.Errorf("external orchestrator generation timestamp moved backwards")
			}
		}
		generation := registry.CurrentGeneration + 1
		registration = externalOrchestratorRegistration{
			ID:            externalOrchestratorRegistrationID(identity, generation),
			Generation:    generation,
			Identity:      identity,
			State:         externalOrchestratorStatePlanned,
			Authoritative: false,
			CreatedAt:     now,
			UpdatedAt:     now,
			Transitions: []externalOrchestratorStateTransition{{
				To: externalOrchestratorStatePlanned, At: now,
			}},
		}
		registry.CurrentGeneration = generation
		registry.UpdatedAt = now
		registry.Registrations = append(registry.Registrations, registration)
		return writeExternalOrchestratorRegistry(registryFS, registry)
	})
	if err != nil {
		return externalOrchestratorRegistration{}, false, err
	}
	return registration, replayed, nil
}

func transitionExternalOrchestratorRegistration(scope externalOrchestratorScope, generation uint64, next externalOrchestratorRegistrationState, evidence externalOrchestratorTransitionEvidence, now time.Time) (registration externalOrchestratorRegistration, replayed bool, retErr error) {
	canonicalScope, err := newExternalOrchestratorScope(scope.ProjectDir, scope.Profile, scope.Session, scope.Handle)
	if err != nil {
		return externalOrchestratorRegistration{}, false, err
	}
	evidence, err = normalizeExternalOrchestratorEvidence(evidence)
	if err != nil {
		return externalOrchestratorRegistration{}, false, err
	}
	now = now.UTC()
	if now.IsZero() {
		return externalOrchestratorRegistration{}, false, fmt.Errorf("external orchestrator transition requires a non-zero timestamp")
	}
	if !validExternalOrchestratorState(next) || next == externalOrchestratorStatePlanned {
		return externalOrchestratorRegistration{}, false, fmt.Errorf("invalid external orchestrator transition target %q", next)
	}
	err = withExternalOrchestratorRegistryLock(canonicalScope, func(registryFS *externalOrchestratorRegistryFS) error {
		registry, found, err := loadExternalOrchestratorRegistry(registryFS)
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("external orchestrator registry does not exist")
		}
		if registry.CurrentGeneration != generation || len(registry.Registrations) == 0 {
			return fmt.Errorf("external orchestrator generation changed: current=%d requested=%d", registry.CurrentGeneration, generation)
		}
		index := len(registry.Registrations) - 1
		current := registry.Registrations[index]
		if current.Generation != generation {
			return fmt.Errorf("external orchestrator current generation record mismatch")
		}
		if current.State == next {
			last := current.Transitions[len(current.Transitions)-1]
			if !reflect.DeepEqual(last.Evidence, evidence) {
				return fmt.Errorf("external orchestrator duplicate transition to %s has different evidence", next)
			}
			registration, replayed = current, true
			return nil
		}
		if !externalOrchestratorTransitionAllowed(current.State, next) {
			return fmt.Errorf("external orchestrator transition is not monotonic: %s -> %s", current.State, next)
		}
		if now.Before(current.UpdatedAt) {
			return fmt.Errorf("external orchestrator transition timestamp moved backwards")
		}
		current.State = next
		current.UpdatedAt = now
		current.Authoritative = false
		current.Transitions = append(current.Transitions, externalOrchestratorStateTransition{From: registry.Registrations[index].State, To: next, At: now, Evidence: evidence})
		registry.Registrations[index] = current
		registry.UpdatedAt = now
		if err := writeExternalOrchestratorRegistry(registryFS, registry); err != nil {
			return err
		}
		registration = current
		return nil
	})
	if err != nil {
		return externalOrchestratorRegistration{}, false, err
	}
	return registration, replayed, nil
}

func readExternalOrchestratorRegistry(scope externalOrchestratorScope) (externalOrchestratorRegistry, error) {
	canonicalScope, err := newExternalOrchestratorScope(scope.ProjectDir, scope.Profile, scope.Session, scope.Handle)
	if err != nil {
		return externalOrchestratorRegistry{}, err
	}
	registryFS, err := openExternalOrchestratorRegistryFS(canonicalScope, false)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return externalOrchestratorRegistry{}, os.ErrNotExist
		}
		return externalOrchestratorRegistry{}, err
	}
	defer registryFS.Close()
	registry, found, err := loadExternalOrchestratorRegistry(registryFS)
	if err != nil {
		return externalOrchestratorRegistry{}, err
	}
	if !found {
		return externalOrchestratorRegistry{}, os.ErrNotExist
	}
	return registry, nil
}

func loadExternalOrchestratorRegistry(registryFS *externalOrchestratorRegistryFS) (externalOrchestratorRegistry, bool, error) {
	f, err := registryFS.openFile("registry.json", os.O_RDONLY, 0, false)
	if os.IsNotExist(err) {
		return externalOrchestratorRegistry{}, false, nil
	}
	if err != nil {
		return externalOrchestratorRegistry{}, false, fmt.Errorf("read external orchestrator registry: %w", err)
	}
	b, readErr := io.ReadAll(f)
	closeErr := f.Close()
	if readErr != nil {
		return externalOrchestratorRegistry{}, false, fmt.Errorf("read external orchestrator registry: %w", readErr)
	}
	if closeErr != nil {
		return externalOrchestratorRegistry{}, false, fmt.Errorf("close external orchestrator registry: %w", closeErr)
	}
	var registry externalOrchestratorRegistry
	if err := json.Unmarshal(b, &registry); err != nil {
		return externalOrchestratorRegistry{}, false, fmt.Errorf("parse external orchestrator registry: %w", err)
	}
	if err := validateExternalOrchestratorRegistry(registry, registryFS.scope); err != nil {
		return externalOrchestratorRegistry{}, false, err
	}
	return registry, true, nil
}

func writeExternalOrchestratorRegistry(registryFS *externalOrchestratorRegistryFS, registry externalOrchestratorRegistry) error {
	if err := validateExternalOrchestratorRegistry(registry, registry.Scope); err != nil {
		return err
	}
	if registry.Scope != registryFS.scope {
		return fmt.Errorf("external orchestrator registry scope does not match contained directory")
	}
	b, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal external orchestrator registry: %w", err)
	}
	tmpName, err := externalOrchestratorRegistryTempName()
	if err != nil {
		return err
	}
	tmp, err := registryFS.openFile(tmpName, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600, true)
	if err != nil {
		return fmt.Errorf("create external orchestrator registry temp: %w", err)
	}
	defer func() { _ = registryFS.directory.Remove(tmpName) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod external orchestrator registry temp: %w", err)
	}
	if _, err := io.WriteString(tmp, string(b)+"\n"); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write external orchestrator registry temp: %w", err)
	}
	if err := externalOrchestratorRegistryFileSync(tmp); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("fsync external orchestrator registry temp: %w", err)
	}
	if err := externalOrchestratorRegistryFault("after_file_sync"); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close external orchestrator registry temp: %w", err)
	}
	tmpInfo, err := registryFS.directory.Lstat(tmpName)
	if err != nil {
		return fmt.Errorf("stat external orchestrator registry temp: %w", err)
	}
	if err := registryFS.validateTargetBeforeRename("registry.json"); err != nil {
		return err
	}
	if err := registryFS.directory.Rename(tmpName, "registry.json"); err != nil {
		return fmt.Errorf("publish external orchestrator registry: %w", err)
	}
	publishedInfo, err := registryFS.directory.Lstat("registry.json")
	if err != nil || publishedInfo.Mode()&os.ModeSymlink != 0 || !publishedInfo.Mode().IsRegular() || !os.SameFile(tmpInfo, publishedInfo) {
		return fmt.Errorf("external orchestrator registry publication identity changed")
	}
	if err := externalOrchestratorRegistryFault("after_rename"); err != nil {
		return err
	}
	if err := registryFS.syncDirectory(); err != nil {
		return fmt.Errorf("fsync external orchestrator registry directory: %w", err)
	}
	if err := externalOrchestratorRegistryFault("after_directory_sync"); err != nil {
		return err
	}
	return nil
}

func withExternalOrchestratorRegistryLock(scope externalOrchestratorScope, fn func(*externalOrchestratorRegistryFS) error) error {
	registryFS, err := openExternalOrchestratorRegistryFS(scope, true)
	if err != nil {
		return err
	}
	defer registryFS.Close()
	lockFile, err := registryFS.openFile("registry.json.lock", os.O_CREATE|os.O_RDWR, 0o600, true)
	if err != nil {
		return fmt.Errorf("open external orchestrator registry lock: %w", err)
	}
	defer lockFile.Close()
	return flock.WithFile(lockFile, registryFS.path("registry.json.lock"), func() error { return fn(registryFS) })
}

type externalOrchestratorRegistryFS struct {
	scope         externalOrchestratorScope
	directory     *os.Root
	directoryInfo os.FileInfo
}

func (registryFS *externalOrchestratorRegistryFS) Close() error {
	if registryFS == nil || registryFS.directory == nil {
		return nil
	}
	return registryFS.directory.Close()
}

func (registryFS *externalOrchestratorRegistryFS) path(name string) string {
	return filepath.Join(filepath.Dir(externalOrchestratorRegistryPath(registryFS.scope)), name)
}

func openExternalOrchestratorRegistryFS(scope externalOrchestratorScope, create bool) (*externalOrchestratorRegistryFS, error) {
	projectBefore, err := os.Lstat(scope.ProjectDir)
	if err != nil {
		return nil, fmt.Errorf("stat external orchestrator project root: %w", err)
	}
	if projectBefore.Mode()&os.ModeSymlink != 0 || !projectBefore.IsDir() {
		return nil, fmt.Errorf("external orchestrator project root must be a non-symlink directory")
	}
	if err := externalOrchestratorRegistryContainmentHook("after_project_validation", scope.ProjectDir); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(scope.ProjectDir)
	if err != nil {
		return nil, fmt.Errorf("open external orchestrator project root: %w", err)
	}
	projectOpened, err := statExternalOrchestratorRoot(root)
	if err != nil {
		root.Close()
		return nil, err
	}
	projectAfter, err := os.Lstat(scope.ProjectDir)
	if err != nil || projectAfter.Mode()&os.ModeSymlink != 0 || !projectAfter.IsDir() || !os.SameFile(projectBefore, projectOpened) || !os.SameFile(projectAfter, projectOpened) {
		root.Close()
		return nil, fmt.Errorf("external orchestrator project root identity changed during open")
	}

	components := []string{team.DirName, externalOrchestratorRegistryDir, scope.Profile, scope.Session, scope.Handle}
	currentPath := scope.ProjectDir
	for _, component := range components {
		componentPath := filepath.Join(currentPath, component)
		before, statErr := root.Lstat(component)
		if statErr != nil {
			if !errors.Is(statErr, os.ErrNotExist) || !create {
				root.Close()
				return nil, fmt.Errorf("stat contained registry directory %s: %w", componentPath, statErr)
			}
			if err := root.Mkdir(component, 0o700); err != nil {
				root.Close()
				return nil, fmt.Errorf("create contained registry directory %s: %w", componentPath, err)
			}
			if err := syncExternalOrchestratorRootDirectory(root); err != nil {
				root.Close()
				return nil, fmt.Errorf("fsync contained registry parent %s: %w", currentPath, err)
			}
			before, statErr = root.Lstat(component)
		}
		if statErr != nil || before.Mode()&os.ModeSymlink != 0 || !before.IsDir() {
			root.Close()
			return nil, fmt.Errorf("contained registry component %s must be a non-symlink directory", componentPath)
		}
		if err := externalOrchestratorRegistryContainmentHook("after_component_validation", componentPath); err != nil {
			root.Close()
			return nil, err
		}
		child, err := root.OpenRoot(component)
		if err != nil {
			root.Close()
			return nil, fmt.Errorf("open contained registry directory %s: %w", componentPath, err)
		}
		opened, openStatErr := statExternalOrchestratorRoot(child)
		after, afterErr := root.Lstat(component)
		if openStatErr != nil || afterErr != nil || after.Mode()&os.ModeSymlink != 0 || !after.IsDir() || !os.SameFile(before, opened) || !os.SameFile(after, opened) {
			child.Close()
			root.Close()
			return nil, fmt.Errorf("contained registry directory identity changed during open: %s", componentPath)
		}
		root.Close()
		root = child
		currentPath = componentPath
	}
	info, err := statExternalOrchestratorRoot(root)
	if err != nil {
		root.Close()
		return nil, err
	}
	return &externalOrchestratorRegistryFS{scope: scope, directory: root, directoryInfo: info}, nil
}

func statExternalOrchestratorRoot(root *os.Root) (os.FileInfo, error) {
	f, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return f.Stat()
}

func (registryFS *externalOrchestratorRegistryFS) openFile(name string, flags int, perm os.FileMode, allowCreate bool) (*os.File, error) {
	before, statErr := registryFS.directory.Lstat(name)
	missing := errors.Is(statErr, os.ErrNotExist)
	if statErr != nil && !missing {
		return nil, statErr
	}
	if missing && !allowCreate {
		return nil, os.ErrNotExist
	}
	if !missing && (before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular()) {
		return nil, fmt.Errorf("contained registry file %s must be a non-symlink regular file", registryFS.path(name))
	}
	if err := externalOrchestratorRegistryContainmentHook("after_file_validation", registryFS.path(name)); err != nil {
		return nil, err
	}
	f, err := registryFS.directory.OpenFile(name, flags, perm)
	if err != nil {
		return nil, err
	}
	opened, openErr := f.Stat()
	after, afterErr := registryFS.directory.Lstat(name)
	if openErr != nil || afterErr != nil || after.Mode()&os.ModeSymlink != 0 || !after.Mode().IsRegular() || !os.SameFile(opened, after) || (!missing && !os.SameFile(before, opened)) {
		f.Close()
		return nil, fmt.Errorf("contained registry file identity changed during open: %s", registryFS.path(name))
	}
	return f, nil
}

func (registryFS *externalOrchestratorRegistryFS) validateTargetBeforeRename(name string) error {
	before, err := registryFS.directory.Lstat(name)
	missing := errors.Is(err, os.ErrNotExist)
	if err != nil && !missing {
		return err
	}
	if !missing && (before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular()) {
		return fmt.Errorf("contained registry target %s must be a non-symlink regular file", registryFS.path(name))
	}
	if err := externalOrchestratorRegistryContainmentHook("before_target_rename", registryFS.path(name)); err != nil {
		return err
	}
	after, err := registryFS.directory.Lstat(name)
	if missing {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		return fmt.Errorf("contained registry target appeared before rename: %s", registryFS.path(name))
	}
	if err != nil || after.Mode()&os.ModeSymlink != 0 || !after.Mode().IsRegular() || !os.SameFile(before, after) {
		return fmt.Errorf("contained registry target identity changed before rename: %s", registryFS.path(name))
	}
	return nil
}

func externalOrchestratorRegistryTempName() (string, error) {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("generate external orchestrator registry temp name: %w", err)
	}
	return ".registry-" + hex.EncodeToString(random) + ".tmp", nil
}

func (registryFS *externalOrchestratorRegistryFS) syncDirectory() error {
	return syncExternalOrchestratorRootDirectory(registryFS.directory)
}

func syncExternalOrchestratorRootDirectory(root *os.Root) error {
	f, err := root.Open(".")
	if err != nil {
		return err
	}
	defer f.Close()
	return externalOrchestratorRegistryDirectorySync(f)
}

func syncExternalOrchestratorRegistryDirectory(f *os.File) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	if err := f.Sync(); err != nil && !errors.Is(err, syscall.EINVAL) && !errors.Is(err, syscall.ENOTSUP) {
		return err
	}
	return nil
}

func externalOrchestratorRegistrationID(identity externalOrchestratorIdentity, generation uint64) string {
	payload := fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%d",
		identity.Scope.ProjectDir, identity.Scope.Profile, identity.Scope.Session, identity.Scope.Handle,
		identity.Runtime.TmuxSession, identity.Runtime.WindowID, identity.Runtime.WindowName, identity.Runtime.PaneID, identity.Runtime.TTY, generation)
	digest := sha256.Sum256([]byte(payload))
	return "external-orchestrator-" + hex.EncodeToString(digest[:])
}

func validExternalOrchestratorState(state externalOrchestratorRegistrationState) bool {
	switch state {
	case externalOrchestratorStatePlanned, externalOrchestratorStateMailboxInvoked, externalOrchestratorStateMailboxUncertain,
		externalOrchestratorStateMailboxVerified, externalOrchestratorStateRuntimeVerified, externalOrchestratorStateRegistered,
		externalOrchestratorStateStale, externalOrchestratorStateDead:
		return true
	default:
		return false
	}
}

func externalOrchestratorTransitionAllowed(from, to externalOrchestratorRegistrationState) bool {
	if to == externalOrchestratorStateStale || to == externalOrchestratorStateDead {
		return from != externalOrchestratorStateStale && from != externalOrchestratorStateDead
	}
	switch from {
	case externalOrchestratorStatePlanned:
		return to == externalOrchestratorStateMailboxInvoked
	case externalOrchestratorStateMailboxInvoked:
		return to == externalOrchestratorStateMailboxUncertain || to == externalOrchestratorStateMailboxVerified
	case externalOrchestratorStateMailboxUncertain:
		return to == externalOrchestratorStateMailboxVerified
	case externalOrchestratorStateMailboxVerified:
		return to == externalOrchestratorStateRuntimeVerified
	case externalOrchestratorStateRuntimeVerified:
		return to == externalOrchestratorStateRegistered
	default:
		return false
	}
}

func validateExternalOrchestratorRegistry(registry externalOrchestratorRegistry, expected externalOrchestratorScope) error {
	if registry.SchemaVersion != externalOrchestratorRegistrySchema {
		return fmt.Errorf("unsupported external orchestrator registry schema %d", registry.SchemaVersion)
	}
	if registry.Scope != expected {
		return fmt.Errorf("external orchestrator registry scope mismatch")
	}
	if len(registry.Registrations) == 0 || registry.CurrentGeneration == 0 {
		return fmt.Errorf("external orchestrator registry has no generation")
	}
	for i, record := range registry.Registrations {
		generation := uint64(i + 1)
		if record.Generation != generation {
			return fmt.Errorf("external orchestrator registry generation sequence is invalid")
		}
		identity, err := normalizeExternalOrchestratorIdentity(record.Identity)
		if err != nil || identity != record.Identity || identity.Scope != expected {
			return fmt.Errorf("external orchestrator generation %d identity is invalid", generation)
		}
		if record.ID != externalOrchestratorRegistrationID(record.Identity, generation) {
			return fmt.Errorf("external orchestrator generation %d id mismatch", generation)
		}
		if record.Authoritative {
			return fmt.Errorf("external orchestrator generation %d cannot be authoritative", generation)
		}
		if !validExternalOrchestratorState(record.State) || len(record.Transitions) == 0 {
			return fmt.Errorf("external orchestrator generation %d state history is invalid", generation)
		}
		state := externalOrchestratorRegistrationState("")
		var at time.Time
		for j, transition := range record.Transitions {
			if j == 0 {
				if transition.From != "" || transition.To != externalOrchestratorStatePlanned {
					return fmt.Errorf("external orchestrator generation %d must begin planned", generation)
				}
			} else if transition.From != state || !externalOrchestratorTransitionAllowed(state, transition.To) {
				return fmt.Errorf("external orchestrator generation %d has non-monotonic history", generation)
			}
			if transition.At.IsZero() || (!at.IsZero() && transition.At.Before(at)) {
				return fmt.Errorf("external orchestrator generation %d timestamps are not monotonic", generation)
			}
			if _, err := normalizeExternalOrchestratorEvidence(transition.Evidence); err != nil {
				return err
			}
			state, at = transition.To, transition.At
		}
		if state != record.State || record.CreatedAt != record.Transitions[0].At || record.UpdatedAt != record.Transitions[len(record.Transitions)-1].At {
			return fmt.Errorf("external orchestrator generation %d current state does not match history", generation)
		}
		if i < len(registry.Registrations)-1 && record.State != externalOrchestratorStateStale && record.State != externalOrchestratorStateDead {
			return fmt.Errorf("external orchestrator generation %d was replaced before stale/dead terminalization", generation)
		}
	}
	current := registry.Registrations[len(registry.Registrations)-1]
	if registry.CurrentGeneration != current.Generation || registry.UpdatedAt != current.UpdatedAt {
		return fmt.Errorf("external orchestrator registry current generation metadata mismatch")
	}
	return nil
}
