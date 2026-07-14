//go:build darwin || linux

package cli

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func manifestFixture(project, operationID string, phase paneCleanupManifestPhase) paneCleanupManifest {
	return paneCleanupManifest{
		Schema: paneCleanupManifestSchema, OperationID: operationID, Operation: "rm", Phase: phase,
		Project: project, Profile: "default", Session: "issue-465", CreatedAt: time.Unix(1, 0).UTC(),
		Entries: []paneCleanupManifestEntry{{Role: "cto", Handle: "cto", Requested: true, Identity: PaneCleanupIdentity{PaneID: "%9"}}},
	}
}

func TestPaneCleanupManifestPrepareFinalizeDurableEvidence(t *testing.T) {
	project := t.TempDir()
	store := filesystemPaneCleanupManifestStore{}
	prepared := manifestFixture(project, "op-positive", paneCleanupManifestPrepared)
	handle, err := store.Prepare(project, prepared)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	final := prepared
	final.Phase = paneCleanupManifestFinalized
	final.PreparedSHA256 = handle.PreparedSHA256
	final.NamespaceMutation = "succeeded"
	final.FinalizedAt = time.Unix(2, 0).UTC()
	final.Entries[0].AgentStatus = "stopped"
	final.Entries[0].Pane = &PaneCleanupResult{Outcome: PaneCleanupClosed}
	if err := store.Finalize(handle, final); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	for _, path := range []string{handle.Prepared, handle.Final} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read retained evidence %s: %v", path, err)
		}
		if len(data) == 0 {
			t.Fatalf("empty evidence: %s", path)
		}
	}
	canonicalProject, err := filepath.EvalSymlinks(project)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(handle.Prepared) != filepath.Join(canonicalProject, ".amq-squad", "pane-cleanup", "default", "issue-465") {
		t.Fatalf("manifest escaped canonical project: %s", handle.Prepared)
	}
}

func TestPaneCleanupManifestReleasedRootFDHasNoLateWrapperClose(t *testing.T) {
	project := t.TempDir()
	rootFD := -1
	oldObserve := paneManifestRootFDObserved
	paneManifestRootFDObserved = func(fd int) { rootFD = fd }
	t.Cleanup(func() { paneManifestRootFDObserved = oldObserve })
	if _, err := (filesystemPaneCleanupManifestStore{}).Prepare(project, manifestFixture(project, "fd-owner", paneCleanupManifestPrepared)); err != nil {
		t.Fatal(err)
	}
	if rootFD < 0 {
		t.Fatal("root descriptor was not observed")
	}

	sentinelPath := filepath.Join(t.TempDir(), "sentinel")
	if err := os.WriteFile(sentinelPath, []byte("sentinel"), 0o600); err != nil {
		t.Fatal(err)
	}
	sentinelFD, err := unix.Open(sentinelPath, unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	if sentinelFD != rootFD {
		if err := unix.Dup2(sentinelFD, rootFD); err != nil {
			unix.Close(sentinelFD)
			t.Fatal(err)
		}
		if err := unix.Close(sentinelFD); err != nil {
			unix.Close(rootFD)
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _ = unix.Close(rootFD) })

	for i := 0; i < 3; i++ {
		runtime.GC()
		debug.FreeOSMemory()
		runtime.Gosched()
	}
	var stat unix.Stat_t
	if err := unix.Fstat(rootFD, &stat); err != nil {
		t.Fatalf("recycled sentinel fd was closed by late manifest cleanup: %v", err)
	}
	buf := make([]byte, len("sentinel"))
	if _, err := unix.Pread(rootFD, buf, 0); err != nil || string(buf) != "sentinel" {
		t.Fatalf("recycled sentinel fd invalid after GC: data=%q err=%v", buf, err)
	}
}

func TestPaneCleanupManifestRejectsHardlinkedPreparedEvidence(t *testing.T) {
	project := t.TempDir()
	store := filesystemPaneCleanupManifestStore{}
	prepared := manifestFixture(project, "hardlink-op", paneCleanupManifestPrepared)
	handle, err := store.Prepare(project, prepared)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Link(handle.Prepared, handle.Prepared+".link"); err != nil {
		t.Fatal(err)
	}
	final := handle.PreparedManifest
	final.Phase = paneCleanupManifestFinalized
	final.PreparedSHA256 = handle.PreparedSHA256
	final.NamespaceMutation = "succeeded"
	final.FinalizedAt = time.Unix(2, 0).UTC()
	if err := store.Finalize(handle, final); err == nil {
		t.Fatal("hardlinked prepared evidence must fail closed")
	}
}

func TestPaneCleanupManifestDetectsPublishedTargetSwap(t *testing.T) {
	project := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := paneManifestAfterRename
	paneManifestAfterRename = func(dirFD int, target string) error {
		if err := unix.Unlinkat(dirFD, target, 0); err != nil {
			return err
		}
		return unix.Symlinkat(outside, dirFD, target)
	}
	t.Cleanup(func() { paneManifestAfterRename = old })
	_, err := (filesystemPaneCleanupManifestStore{}).Prepare(project, manifestFixture(project, "published-swap", paneCleanupManifestPrepared))
	if err == nil {
		t.Fatal("published target swap must fail closed")
	}
}

func TestPaneCleanupManifestDetectsProjectRootSwap(t *testing.T) {
	project := t.TempDir()
	oldHook := paneManifestBeforeProjectOpen
	paneManifestBeforeProjectOpen = func(canonical string) error {
		if err := os.Rename(canonical, canonical+".old"); err != nil {
			return err
		}
		return os.Mkdir(canonical, 0o700)
	}
	t.Cleanup(func() { paneManifestBeforeProjectOpen = oldHook })
	_, err := (filesystemPaneCleanupManifestStore{}).Prepare(project, manifestFixture(project, "root-swap", paneCleanupManifestPrepared))
	if err == nil {
		t.Fatal("project root swap must fail closed")
	}
}

func TestPaneCleanupManifestRejectsSymlinkedAncestors(t *testing.T) {
	for _, component := range []string{".amq-squad", "pane-cleanup", "default", "issue-465"} {
		t.Run(component, func(t *testing.T) {
			project := t.TempDir()
			outside := t.TempDir()
			parts := []string{".amq-squad", "pane-cleanup", "default", "issue-465"}
			current := project
			for _, part := range parts {
				path := filepath.Join(current, part)
				if part == component {
					if err := os.Symlink(outside, path); err != nil {
						t.Fatal(err)
					}
					break
				}
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
				current = path
			}
			_, err := (filesystemPaneCleanupManifestStore{}).Prepare(project, manifestFixture(project, "op-symlink", paneCleanupManifestPrepared))
			if err == nil {
				t.Fatal("symlinked ancestor must fail closed")
			}
			if _, err := os.Stat(filepath.Join(outside, "op-symlink.prepared.json")); !os.IsNotExist(err) {
				t.Fatalf("manifest escaped through symlink: %v", err)
			}
		})
	}
}

func TestPaneCleanupManifestRejectsTargetSymlinkAndCollision(t *testing.T) {
	project := t.TempDir()
	_, fd, dir, err := openPaneManifestDir(project, "default", "issue-465")
	if err != nil {
		t.Fatal(err)
	}
	unix.Close(fd)
	target := filepath.Join(dir, "same-op.prepared.json")
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, target); err != nil {
		t.Fatal(err)
	}
	_, err = (filesystemPaneCleanupManifestStore{}).Prepare(project, manifestFixture(project, "same-op", paneCleanupManifestPrepared))
	if err == nil {
		t.Fatal("symlink target must fail closed")
	}
	data, _ := os.ReadFile(outside)
	if string(data) != "outside" {
		t.Fatalf("outside target changed: %q", data)
	}
}

func TestPaneCleanupManifestDetectsTempSwap(t *testing.T) {
	project := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := paneManifestBeforeRename
	paneManifestBeforeRename = func(dirFD int, temp, _ string) error {
		if err := unix.Unlinkat(dirFD, temp, 0); err != nil {
			return err
		}
		return unix.Symlinkat(outside, dirFD, temp)
	}
	t.Cleanup(func() { paneManifestBeforeRename = old })
	_, err := (filesystemPaneCleanupManifestStore{}).Prepare(project, manifestFixture(project, "swap-op", paneCleanupManifestPrepared))
	if err == nil {
		t.Fatal("temp swap must fail closed")
	}
	data, _ := os.ReadFile(outside)
	if string(data) != "outside" {
		t.Fatalf("outside target changed: %q", data)
	}
}

func TestPaneCleanupManifestSyncAmbiguityRetainsPrepared(t *testing.T) {
	project := t.TempDir()
	store := filesystemPaneCleanupManifestStore{}
	prepared := manifestFixture(project, "sync-op", paneCleanupManifestPrepared)
	handle, err := store.Prepare(project, prepared)
	if err != nil {
		t.Fatal(err)
	}
	old := paneManifestDirSync
	paneManifestDirSync = func(int) error { return errors.New("fsync ambiguity") }
	t.Cleanup(func() { paneManifestDirSync = old })
	final := prepared
	final.Phase = paneCleanupManifestFinalized
	final.PreparedSHA256 = handle.PreparedSHA256
	final.NamespaceMutation = "succeeded"
	final.FinalizedAt = time.Unix(2, 0).UTC()
	if err := store.Finalize(handle, final); err == nil {
		t.Fatal("directory fsync ambiguity must fail finalization")
	}
	if _, err := os.Stat(handle.Prepared); err != nil {
		t.Fatalf("prepared evidence must remain: %v", err)
	}
}

func TestPaneCleanupManifestFileSyncAndRenameFailuresFailClosed(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  func(t *testing.T)
	}{
		{name: "file fsync", set: func(t *testing.T) {
			old := paneManifestFileSync
			paneManifestFileSync = func(*os.File) error { return errors.New("file fsync failed") }
			t.Cleanup(func() { paneManifestFileSync = old })
		}},
		{name: "rename ambiguity", set: func(t *testing.T) {
			old := paneManifestRename
			paneManifestRename = func(int, string, string) error { return errors.New("rename interrupted") }
			t.Cleanup(func() { paneManifestRename = old })
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			project := t.TempDir()
			tc.set(t)
			_, err := (filesystemPaneCleanupManifestStore{}).Prepare(project, manifestFixture(project, "failure-op", paneCleanupManifestPrepared))
			if err == nil {
				t.Fatal("uncertain durable write must fail closed")
			}
			canonical, _ := filepath.EvalSymlinks(project)
			target := filepath.Join(canonical, ".amq-squad", "pane-cleanup", "default", "issue-465", "failure-op.prepared.json")
			if _, statErr := os.Lstat(target); !os.IsNotExist(statErr) {
				t.Fatalf("failed prepare published target: %v", statErr)
			}
		})
	}
}

func TestPaneCleanupManifestConcurrentOperationCollision(t *testing.T) {
	project := t.TempDir()
	manifest := manifestFixture(project, "collision-op", paneCleanupManifestPrepared)
	store := filesystemPaneCleanupManifestStore{}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := store.Prepare(project, manifest)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	var success, failure int
	for err := range errs {
		if err == nil {
			success++
		} else {
			failure++
		}
	}
	if success != 1 || failure != 1 {
		t.Fatalf("concurrent collision success=%d failure=%d", success, failure)
	}
}

func TestPaneCleanupManifestRejectsPathEscape(t *testing.T) {
	project := t.TempDir()
	for _, mutate := range []func(*paneCleanupManifest){
		func(m *paneCleanupManifest) { m.Session = "../escape" },
		func(m *paneCleanupManifest) { m.OperationID = "../escape" },
		func(m *paneCleanupManifest) { m.Profile = "../escape" },
	} {
		m := manifestFixture(project, "safe-op", paneCleanupManifestPrepared)
		mutate(&m)
		if _, err := (filesystemPaneCleanupManifestStore{}).Prepare(project, m); err == nil {
			t.Fatalf("path escape accepted: %+v", m)
		}
	}
}
