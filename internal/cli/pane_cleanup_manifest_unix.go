//go:build darwin || linux

package cli

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

var (
	paneManifestBeforeProjectOpen = func(string) error { return nil }
	paneManifestRootFDObserved    = func(int) {}
	paneManifestBeforeRename      = func(int, string, string) error { return nil }
	paneManifestAfterRename       = func(int, string) error { return nil }
	paneManifestFileSync          = func(f *os.File) error { return f.Sync() }
	paneManifestDirSync           = func(fd int) error { return unix.Fsync(fd) }
	paneManifestRename            = paneManifestRenameNoReplace
)

func (filesystemPaneCleanupManifestStore) Prepare(projectDir string, manifest paneCleanupManifest) (paneCleanupManifestHandle, error) {
	if manifest.Phase != paneCleanupManifestPrepared {
		return paneCleanupManifestHandle{}, fmt.Errorf("prepare requires phase %q", paneCleanupManifestPrepared)
	}
	if err := validatePaneCleanupManifestIdentity(manifest); err != nil {
		return paneCleanupManifestHandle{}, err
	}
	canonicalProject, dirFD, dirPath, err := openPaneManifestDir(projectDir, manifest.Profile, manifest.Session)
	if err != nil {
		return paneCleanupManifestHandle{}, err
	}
	defer unix.Close(dirFD)
	manifestProject, err := cleanupCanonicalDir(manifest.Project)
	if err != nil || manifestProject != canonicalProject {
		return paneCleanupManifestHandle{}, fmt.Errorf("pane cleanup manifest project does not match canonical opened project")
	}
	manifest.Project = canonicalProject
	manifest.Profile, _ = normalizePaneCleanupProfile(manifest.Profile)
	preparedName := manifest.OperationID + ".prepared.json"
	finalName := manifest.OperationID + ".json"
	preparedBytes, err := marshalPaneManifest(manifest)
	if err != nil {
		return paneCleanupManifestHandle{}, err
	}
	if err := writePaneManifestAtomic(dirFD, dirPath, preparedName, manifest); err != nil {
		return paneCleanupManifestHandle{}, fmt.Errorf("persist prepared pane cleanup manifest: %w", err)
	}
	return paneCleanupManifestHandle{
		Project: canonicalProject, Profile: manifest.Profile, Session: manifest.Session, OperationID: manifest.OperationID, Operation: manifest.Operation,
		PreparedSHA256:   fmt.Sprintf("%x", sha256.Sum256(preparedBytes)),
		PreparedManifest: manifest,
		Prepared:         filepath.Join(dirPath, preparedName), Final: filepath.Join(dirPath, finalName),
	}, nil
}

func (filesystemPaneCleanupManifestStore) Finalize(handle paneCleanupManifestHandle, manifest paneCleanupManifest) error {
	if manifest.Phase != paneCleanupManifestFinalized {
		return fmt.Errorf("finalize requires phase %q", paneCleanupManifestFinalized)
	}
	if err := validatePaneCleanupManifestIdentity(manifest); err != nil {
		return err
	}
	manifestProject, err := cleanupCanonicalDir(manifest.Project)
	if err != nil || manifestProject != handle.Project || manifest.OperationID != handle.OperationID || manifest.Operation != handle.Operation || manifest.Profile != handle.Profile || manifest.Session != handle.Session {
		return fmt.Errorf("final pane cleanup manifest identity differs from prepared identity")
	}
	manifest.Project = manifestProject
	if !manifestEntriesRetainIdentity(handle.PreparedManifest, manifest) || !manifest.CreatedAt.Equal(handle.PreparedManifest.CreatedAt) {
		return fmt.Errorf("final pane cleanup manifest drifts from prepared members or timestamp")
	}
	if manifest.PreparedSHA256 == "" || manifest.PreparedSHA256 != handle.PreparedSHA256 {
		return fmt.Errorf("final pane cleanup manifest is not bound to prepared digest")
	}
	canonicalProject, dirFD, dirPath, err := openPaneManifestDir(handle.Project, handle.Profile, handle.Session)
	if err != nil {
		return err
	}
	defer unix.Close(dirFD)
	if canonicalProject != handle.Project {
		return fmt.Errorf("pane cleanup project identity changed: %q != %q", canonicalProject, handle.Project)
	}
	preparedName := handle.OperationID + ".prepared.json"
	preparedDigest, preparedBytes, err := readPaneManifestLeaf(dirFD, preparedName)
	if err != nil {
		return fmt.Errorf("prepared pane cleanup evidence unavailable: %w", err)
	}
	if preparedDigest != handle.PreparedSHA256 {
		return fmt.Errorf("prepared pane cleanup evidence digest changed")
	}
	var persisted paneCleanupManifest
	if err := json.Unmarshal(preparedBytes, &persisted); err != nil || persisted.Phase != paneCleanupManifestPrepared || persisted.OperationID != handle.OperationID || persisted.Operation != handle.Operation || persisted.Project != handle.Project || persisted.Profile != handle.Profile || persisted.Session != handle.Session || !persisted.CreatedAt.Equal(handle.PreparedManifest.CreatedAt) || !manifestEntriesRetainIdentity(handle.PreparedManifest, persisted) {
		return fmt.Errorf("prepared pane cleanup JSON identity is invalid")
	}
	finalName := handle.OperationID + ".json"
	if filepath.Join(dirPath, preparedName) != handle.Prepared || filepath.Join(dirPath, finalName) != handle.Final {
		return fmt.Errorf("pane cleanup manifest path identity changed")
	}
	if err := writePaneManifestAtomic(dirFD, dirPath, finalName, manifest); err != nil {
		return fmt.Errorf("persist final pane cleanup manifest (prepared evidence retained at %s): %w", handle.Prepared, err)
	}
	return nil
}

func openPaneManifestDir(projectDir, profile, session string) (string, int, string, error) {
	profile, err := normalizePaneCleanupProfile(profile)
	if err != nil {
		return "", -1, "", err
	}
	if err := validateWorkstreamName(session); err != nil {
		return "", -1, "", err
	}
	abs, err := filepath.Abs(strings.TrimSpace(projectDir))
	if err != nil {
		return "", -1, "", err
	}
	canonical, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", -1, "", fmt.Errorf("canonicalize pane cleanup project: %w", err)
	}
	var pre unix.Stat_t
	if err := unix.Lstat(canonical, &pre); err != nil {
		return "", -1, "", fmt.Errorf("validate canonical pane cleanup project: %w", err)
	}
	if pre.Mode&unix.S_IFMT != unix.S_IFDIR {
		return "", -1, "", fmt.Errorf("validate canonical pane cleanup project: canonical project is not a non-symlink directory")
	}
	if err := paneManifestBeforeProjectOpen(canonical); err != nil {
		return "", -1, "", err
	}
	rootFD, err := unix.Open(canonical, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return "", -1, "", fmt.Errorf("open canonical pane cleanup project: %w", err)
	}
	// rootFD and every directory descriptor derived from it remain raw-unix
	// owned. No os.File wrapper is ever created for the directory walk.
	paneManifestRootFDObserved(rootFD)
	var opened unix.Stat_t
	openErr := unix.Fstat(rootFD, &opened)
	var post unix.Stat_t
	postErr := unix.Lstat(canonical, &post)
	if openErr != nil || postErr != nil ||
		opened.Mode&unix.S_IFMT != unix.S_IFDIR || post.Mode&unix.S_IFMT != unix.S_IFDIR ||
		pre.Dev != opened.Dev || pre.Ino != opened.Ino || opened.Dev != post.Dev || opened.Ino != post.Ino {
		_ = unix.Close(rootFD)
		if openErr != nil {
			return "", -1, "", fmt.Errorf("stat opened pane cleanup project: %w", openErr)
		}
		if postErr != nil {
			return "", -1, "", fmt.Errorf("revalidate pane cleanup project: %w", postErr)
		}
		return "", -1, "", fmt.Errorf("pane cleanup project identity changed during open")
	}
	currentFD := rootFD
	currentPath := canonical
	for _, component := range []string{".amq-squad", "pane-cleanup", profile, session} {
		if !isSafePaneCleanupComponent(component) && component != ".amq-squad" {
			unix.Close(currentFD)
			return "", -1, "", fmt.Errorf("invalid pane cleanup path component %q", component)
		}
		created := false
		if err := unix.Mkdirat(currentFD, component, 0o700); err != nil {
			if !errors.Is(err, unix.EEXIST) {
				unix.Close(currentFD)
				return "", -1, "", fmt.Errorf("create pane cleanup directory %q: %w", component, err)
			}
		} else {
			created = true
		}
		nextFD, err := unix.Openat(currentFD, component, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
		if err != nil {
			unix.Close(currentFD)
			return "", -1, "", fmt.Errorf("open pane cleanup directory %q without following links: %w", component, err)
		}
		if created {
			if err := paneManifestDirSync(currentFD); err != nil {
				unix.Close(nextFD)
				unix.Close(currentFD)
				return "", -1, "", fmt.Errorf("sync parent after creating pane cleanup directory %q: %w", component, err)
			}
		}
		unix.Close(currentFD)
		currentFD = nextFD
		currentPath = filepath.Join(currentPath, component)
	}
	return canonical, currentFD, currentPath, nil
}

func writePaneManifestAtomic(dirFD int, dirPath, target string, manifest paneCleanupManifest) error {
	if !isSafePaneCleanupFilename(target) {
		return fmt.Errorf("unsafe pane cleanup manifest filename %q", target)
	}
	data, err := marshalPaneManifest(manifest)
	if err != nil {
		return err
	}
	temp := "." + strings.TrimSuffix(target, ".json") + ".tmp"
	fd, err := unix.Openat(dirFD, temp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return fmt.Errorf("create manifest temp %q: %w", temp, err)
	}
	f := os.NewFile(uintptr(fd), filepath.Join(dirPath, temp))
	if f == nil {
		_ = unix.Close(fd)
		return fmt.Errorf("wrap manifest temp descriptor")
	}
	// Ownership transferred to f. From here on, f.Close is the only close;
	// raw fd use is limited to non-owning Fstat checks.
	renamed := false
	defer func() {
		_ = f.Close()
		if !renamed {
			_ = unix.Unlinkat(dirFD, temp, 0)
		}
	}()
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("write manifest temp: %w", err)
	}
	if err := paneManifestFileSync(f); err != nil {
		return fmt.Errorf("sync manifest temp: %w", err)
	}
	var opened unix.Stat_t
	if err := unix.Fstat(fd, &opened); err != nil {
		return fmt.Errorf("stat opened manifest temp: %w", err)
	}
	if err := paneManifestBeforeRename(dirFD, temp, target); err != nil {
		return err
	}
	var current unix.Stat_t
	if err := unix.Fstatat(dirFD, temp, &current, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return fmt.Errorf("revalidate manifest temp: %w", err)
	}
	if opened.Dev != current.Dev || opened.Ino != current.Ino || current.Mode&unix.S_IFMT != unix.S_IFREG || opened.Nlink != 1 || current.Nlink != 1 {
		return fmt.Errorf("manifest temp identity changed before rename")
	}
	if err := paneManifestRename(dirFD, temp, target); err != nil {
		return fmt.Errorf("publish manifest %q: %w", target, err)
	}
	renamed = true
	if err := paneManifestAfterRename(dirFD, target); err != nil {
		return err
	}
	var published unix.Stat_t
	if err := unix.Fstatat(dirFD, target, &published, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return fmt.Errorf("revalidate published manifest: %w", err)
	}
	if opened.Dev != published.Dev || opened.Ino != published.Ino || published.Mode&unix.S_IFMT != unix.S_IFREG || published.Nlink != 1 {
		return fmt.Errorf("published manifest identity changed before directory sync")
	}
	if err := paneManifestDirSync(dirFD); err != nil {
		return fmt.Errorf("sync manifest directory after publishing %q: %w", target, err)
	}
	return nil
}

func readPaneManifestLeaf(dirFD int, name string) (string, []byte, error) {
	fd, err := unix.Openat(dirFD, name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return "", nil, err
	}
	f := os.NewFile(uintptr(fd), name)
	if f == nil {
		_ = unix.Close(fd)
		return "", nil, fmt.Errorf("wrap manifest evidence descriptor")
	}
	// Ownership transferred to f. f.Close is the only close on this fd.
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		if err == nil {
			err = fmt.Errorf("manifest evidence is not a regular file")
		}
		return "", nil, err
	}
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil || st.Nlink != 1 {
		if err == nil {
			err = fmt.Errorf("manifest evidence link count is not one")
		}
		return "", nil, err
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return "", nil, err
	}
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum), data, nil
}

func manifestEntriesRetainIdentity(prepared, final paneCleanupManifest) bool {
	if len(prepared.Entries) != len(final.Entries) {
		return false
	}
	for i := range prepared.Entries {
		p, f := prepared.Entries[i], final.Entries[i]
		if p.Role != f.Role || p.Handle != f.Handle || p.Requested != f.Requested || p.Identity != f.Identity {
			return false
		}
	}
	return true
}

func marshalPaneManifest(manifest paneCleanupManifest) ([]byte, error) {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func isSafePaneCleanupFilename(name string) bool {
	if !strings.HasSuffix(name, ".json") || strings.ContainsAny(name, `/\\`) {
		return false
	}
	stem := strings.TrimSuffix(name, ".json")
	stem = strings.TrimSuffix(stem, ".prepared")
	return isSafePaneCleanupComponent(stem)
}
