package compoundrelease

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/omriariav/amq-squad/v2/internal/flock"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/operatorauth"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

const (
	MaxSessionSeries = 64
	seriesIDPrefix   = "release-series-v1-"
)

var ErrStoreBusy = errors.New("compound release store_busy")

// SessionScope names the descriptor-confined directory containing all release
// series for one namespace. ParentGate deliberately is not part of this scope:
// it is recovered from each validated prepared manifest.
type SessionScope struct {
	ProjectDir          string
	Profile             string
	Session             string
	NamespaceGeneration string
}

// LockArtifact is the exact, immutable observation of a pre-existing
// store.lock made through the same descriptor which is subsequently leased.
type LockArtifact struct {
	Bytes         []byte
	Mode          os.FileMode
	ModTimeUnixNS int64
}

// SeriesInspection is a non-mutating lifecycle view. RecordAhead is true only
// for the validated active-record/publishing-pointer crash window.
type SeriesInspection struct {
	SeriesID       string
	Scope          Scope
	Snapshot       Snapshot
	RecordAhead    bool
	SuccessorAhead bool
	Lock           LockArtifact
}

type enumeratedSeries struct {
	store *Store
	name  string
}

// enumerateSessionSeries performs one bounded Readdir and opens every selected
// directory through its parent descriptor. It never follows a path component.
func enumerateSessionSeries(scope SessionScope) (*os.Root, string, []enumeratedSeries, error) {
	normalized, err := normalizeSessionScope(scope)
	if err != nil {
		return nil, "", nil, err
	}
	rel := filepath.Join(team.DirName, "evidence", normalized.Profile, normalized.Session, "compound-release")
	root, path, err := openContainedRoot(scope.ProjectDir, rel, false)
	if errors.Is(err, os.ErrNotExist) {
		return nil, "", nil, nil
	}
	if err != nil {
		return nil, "", nil, err
	}
	dir, err := root.Open(".")
	if err != nil {
		root.Close()
		return nil, "", nil, err
	}
	entries, readErr := dir.Readdir(MaxSessionSeries + 1)
	dir.Close()
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		root.Close()
		return nil, "", nil, fmt.Errorf("enumerate release series: %w", readErr)
	}
	if len(entries) > MaxSessionSeries {
		root.Close()
		return nil, "", nil, fmt.Errorf("release series enumeration exceeds cap %d", MaxSessionSeries)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	series := make([]enumeratedSeries, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if !canonicalSeriesID(name) {
			return root, path, series, fmt.Errorf("non-canonical release series entry %q", name)
		}
		before, statErr := root.Lstat(name)
		if statErr != nil || before.Mode()&os.ModeSymlink != 0 || !before.IsDir() {
			return root, path, series, fmt.Errorf("release series entry %q must be a non-symlink directory", name)
		}
		entryPath := filepath.Join(path, name)
		if hookErr := storeContainmentHook("after_series_validation", entryPath); hookErr != nil {
			return root, path, series, hookErr
		}
		child, openErr := root.OpenRoot(name)
		if openErr != nil {
			return root, path, series, openErr
		}
		opened, openedErr := statRoot(child)
		visible, visibleErr := root.Lstat(name)
		if openedErr != nil || visibleErr != nil || visible.Mode()&os.ModeSymlink != 0 || !visible.IsDir() || !os.SameFile(before, opened) || !os.SameFile(visible, opened) {
			child.Close()
			return root, path, series, fmt.Errorf("release series identity changed during open: %s", entryPath)
		}
		series = append(series, enumeratedSeries{name: name, store: &Store{
			scope:    Scope{ProjectDir: normalized.ProjectDir, Profile: normalized.Profile, Session: normalized.Session, NamespaceGeneration: normalized.NamespaceGeneration},
			seriesID: name, dir: child, dirPath: entryPath,
		}})
	}
	return root, path, series, nil
}

func normalizeSessionScope(scope SessionScope) (SessionScope, error) {
	scope.Profile = squadnamespace.NormalizeProfile(scope.Profile)
	scope.Session = strings.TrimSpace(scope.Session)
	if scope.Profile != team.DefaultProfile {
		if err := team.ValidateProfileName(scope.Profile); err != nil {
			return SessionScope{}, err
		}
	}
	if err := team.ValidateSessionName(scope.Session); err != nil {
		return SessionScope{}, err
	}
	if err := operatorauth.ValidateCanonicalSingleLineField("namespace generation", scope.NamespaceGeneration, true); err != nil {
		return SessionScope{}, err
	}
	if strings.TrimSpace(scope.ProjectDir) == "" {
		return SessionScope{}, fmt.Errorf("project directory is required")
	}
	return scope, nil
}

func closeEnumerated(series []enumeratedSeries) {
	for _, item := range series {
		_ = item.store.Close()
	}
}

func canonicalSeriesID(value string) bool {
	if len(value) != len(seriesIDPrefix)+64 || !strings.HasPrefix(value, seriesIDPrefix) {
		return false
	}
	for _, c := range value[len(seriesIDPrefix):] {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func readLockArtifact(f *os.File) (LockArtifact, error) {
	info, err := f.Stat()
	if err != nil {
		return LockArtifact{}, err
	}
	if err := validateRegularSingleLink(info, f.Name()); err != nil {
		return LockArtifact{}, err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return LockArtifact{}, err
	}
	raw, err := io.ReadAll(f)
	if err != nil {
		return LockArtifact{}, err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return LockArtifact{}, err
	}
	return LockArtifact{Bytes: raw, Mode: info.Mode(), ModTimeUnixNS: info.ModTime().UnixNano()}, nil
}

func sameLockArtifact(a, b LockArtifact) bool {
	return a.Mode == b.Mode && a.ModTimeUnixNS == b.ModTimeUnixNS && bytes.Equal(a.Bytes, b.Bytes)
}

type heldSeriesInspection struct {
	item     enumeratedSeries
	lease    *flock.Exclusive
	artifact LockArtifact
}

func acquireSharedSeries(item enumeratedSeries) (heldSeriesInspection, error) {
	lock, err := item.store.openLeaf("store.lock", os.O_RDONLY, 0, false)
	if err != nil {
		return heldSeriesInspection{}, fmt.Errorf("%s store.lock: %w", item.name, err)
	}
	artifact, err := readLockArtifact(lock)
	if err != nil {
		lock.Close()
		return heldSeriesInspection{}, err
	}
	lease, acquired, err := flock.TrySharedFile(lock)
	if err != nil {
		return heldSeriesInspection{}, err
	}
	if !acquired {
		return heldSeriesInspection{}, ErrStoreBusy
	}
	return heldSeriesInspection{item: item, lease: lease, artifact: artifact}, nil
}

func (h *heldSeriesInspection) closeAndVerify() error {
	if h.lease != nil {
		if err := h.lease.Close(); err != nil {
			return err
		}
		h.lease = nil
	}
	lock, err := h.item.store.openLeaf("store.lock", os.O_RDONLY, 0, false)
	if err != nil {
		return err
	}
	after, readErr := readLockArtifact(lock)
	closeErr := lock.Close()
	if readErr != nil {
		return readErr
	}
	if closeErr != nil {
		return closeErr
	}
	if !sameLockArtifact(h.artifact, after) {
		return fmt.Errorf("%s store.lock changed during inspection", h.item.name)
	}
	return nil
}

func inspectSeriesLocked(item enumeratedSeries, artifact LockArtifact) (SeriesInspection, error) {
	s := item.store
	current, err := s.ReadCurrent()
	recordAhead := false
	if err != nil {
		pointer, pointerErr := s.readPointer()
		if pointerErr != nil {
			return SeriesInspection{}, err
		}
		prepared, preparedErr := s.readPrepared(pointer.Generation)
		if preparedErr != nil {
			return SeriesInspection{}, err
		}
		// ReadCurrent intentionally refuses the valid superseded hand-off
		// state. Inspection still validates and projects it as ineligible.
		if pointer.State == operatorauth.ReleaseStateSuperseded {
			record, recordErr := s.readGeneration(pointer.Generation)
			if recordErr != nil {
				return SeriesInspection{}, recordErr
			}
			var active *operatorauth.ActiveReleaseManifest
			if pointer.ActiveManifestID != "" {
				manifest, activeErr := s.readActive(pointer.Generation, prepared)
				if activeErr != nil {
					return SeriesInspection{}, activeErr
				}
				active = &manifest
			}
			if record.State != operatorauth.ReleaseStateSuperseded {
				if validationErr := s.validatePointerAheadSupersession(pointer, record, prepared, active); validationErr != nil {
					return SeriesInspection{}, validationErr
				}
			} else {
				if validationErr := s.validateLifecycleSnapshot(pointer, record, prepared, active); validationErr != nil {
					return SeriesInspection{}, validationErr
				}
			}
			successor, successorErr := s.readPrepared(pointer.Generation + 1)
			if successorErr != nil || successor.Generation != pointer.Generation+1 || successor.GenerationID != pointer.SuccessorGenerationID || successor.Spec.Namespace != prepared.Spec.Namespace || successor.Spec.ParentGate != prepared.Spec.ParentGate || seriesIdentity(Scope{ProjectDir: s.scope.ProjectDir, Profile: s.scope.Profile, Session: s.scope.Session, NamespaceGeneration: s.scope.NamespaceGeneration, ParentGate: successor.Spec.ParentGate}) != item.name {
				return SeriesInspection{}, fmt.Errorf("superseded release does not bind an exact prepared successor")
			}
			current = Snapshot{Pointer: pointer, Prepared: prepared, Active: active}
			goto validated
		}
		if pointer.State != operatorauth.ReleaseStatePublishing {
			return SeriesInspection{}, err
		}
		record, recordErr := s.readGeneration(pointer.Generation)
		if recordErr != nil || record.State != operatorauth.ReleaseStateActive {
			return SeriesInspection{}, err
		}
		active, activeErr := s.readActive(pointer.Generation, prepared)
		if activeErr != nil {
			return SeriesInspection{}, err
		}
		ahead := pointer
		ahead.State = operatorauth.ReleaseStateActive
		ahead.ActiveManifestID = record.ActiveManifestID
		ahead.ActiveSHA256 = record.ActiveSHA256
		if validationErr := s.validateLifecycleSnapshot(ahead, record, prepared, &active); validationErr != nil {
			return SeriesInspection{}, fmt.Errorf("active record-ahead inspection: %w", validationErr)
		}
		current = Snapshot{Pointer: pointer, Prepared: prepared, Active: &active}
		recordAhead = true
	}

validated:
	derivedScope := Scope{ProjectDir: s.scope.ProjectDir, Profile: s.scope.Profile, Session: s.scope.Session, NamespaceGeneration: s.scope.NamespaceGeneration, ParentGate: current.Prepared.Spec.ParentGate}
	if current.Prepared.Spec.Namespace.ProjectDir != derivedScope.ProjectDir || current.Prepared.Spec.Namespace.Profile != derivedScope.Profile || current.Prepared.Spec.Namespace.Session != derivedScope.Session || current.Prepared.Spec.Namespace.Generation != derivedScope.NamespaceGeneration || seriesIdentity(derivedScope) != item.name {
		return SeriesInspection{}, fmt.Errorf("release series directory does not match validated manifest scope")
	}
	s.scope = derivedScope
	return SeriesInspection{SeriesID: item.name, Scope: derivedScope, Snapshot: current, RecordAhead: recordAhead, SuccessorAhead: current.Pointer.State == operatorauth.ReleaseStateSuperseded, Lock: artifact}, nil
}

// identifySeriesLocked proves the exact prepared-derived scope even when the
// rest of that series lifecycle is corrupt. Callers may then isolate recovery
// to this series instead of degrading unrelated exact series.
func identifySeriesLocked(item enumeratedSeries, artifact LockArtifact) (SeriesInspection, error) {
	pointer, err := item.store.readPointer()
	if err != nil {
		return SeriesInspection{}, err
	}
	prepared, err := item.store.readPrepared(pointer.Generation)
	if err != nil {
		return SeriesInspection{}, err
	}
	scope := Scope{ProjectDir: item.store.scope.ProjectDir, Profile: item.store.scope.Profile, Session: item.store.scope.Session, NamespaceGeneration: item.store.scope.NamespaceGeneration, ParentGate: prepared.Spec.ParentGate}
	if prepared.Spec.Namespace.ProjectDir != scope.ProjectDir || prepared.Spec.Namespace.Profile != scope.Profile || prepared.Spec.Namespace.Session != scope.Session || prepared.Spec.Namespace.Generation != scope.NamespaceGeneration || seriesIdentity(scope) != item.name {
		return SeriesInspection{}, fmt.Errorf("release series directory does not match validated manifest scope")
	}
	item.store.scope = scope
	return SeriesInspection{SeriesID: item.name, Scope: scope, Snapshot: Snapshot{Pointer: pointer, Prepared: prepared}, Lock: artifact}, nil
}

// InspectSessionSeries is the lifecycle-only inspection entry point. It holds
// all stores in canonical order so a busy or corrupt session cannot yield a
// partial authority projection.
func InspectSessionSeries(scope SessionScope) ([]SeriesInspection, error) {
	root, _, items, err := enumerateSessionSeries(scope)
	if root != nil {
		defer root.Close()
	}
	if err != nil || len(items) == 0 {
		closeEnumerated(items)
		return nil, err
	}
	defer closeEnumerated(items)
	held := make([]heldSeriesInspection, 0, len(items))
	for _, item := range items {
		h, acquireErr := acquireSharedSeries(item)
		if acquireErr != nil {
			for i := len(held) - 1; i >= 0; i-- {
				_ = held[i].closeAndVerify()
			}
			return nil, acquireErr
		}
		held = append(held, h)
	}
	result := make([]SeriesInspection, 0, len(held))
	for _, h := range held {
		inspection, inspectErr := inspectSeriesLocked(h.item, h.artifact)
		if inspectErr != nil {
			err = inspectErr
			break
		}
		result = append(result, inspection)
	}
	for i := len(held) - 1; i >= 0; i-- {
		if closeErr := held[i].closeAndVerify(); err == nil && closeErr != nil {
			err = closeErr
		}
	}
	if err != nil {
		return nil, err
	}
	return result, nil
}
