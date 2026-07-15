package cli

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteImmutableAuthorizationHandlesShortWritesAndCleansFailures(t *testing.T) {
	dir := shortTestTempDir(t, "amq-authz-write-")

	originalWrite, originalSync := authorizationArtifactWrite, authorizationArtifactSync
	originalClose, originalRemove := authorizationArtifactClose, authorizationArtifactRemove
	t.Cleanup(func() {
		authorizationArtifactWrite, authorizationArtifactSync = originalWrite, originalSync
		authorizationArtifactClose, authorizationArtifactRemove = originalClose, originalRemove
	})

	raw := []byte("signed authorization bytes")
	shortPath := filepath.Join(dir, "short.json")
	authorizationArtifactWrite = func(f *os.File, data []byte) (int, error) {
		if len(data) > 3 {
			data = data[:3]
		}
		return f.Write(data)
	}
	if err := writeImmutableAuthorization(shortPath, raw); err != nil {
		t.Fatalf("retrying short writes failed: %v", err)
	}
	if got, err := os.ReadFile(shortPath); err != nil || !bytes.Equal(got, raw) {
		t.Fatalf("short-write artifact=%q err=%v", got, err)
	}

	authorizationArtifactWrite = func(f *os.File, data []byte) (int, error) {
		if len(data) > 2 {
			_, _ = f.Write(data[:2])
			return 2, io.ErrUnexpectedEOF
		}
		return 0, io.ErrUnexpectedEOF
	}
	writeFailurePath := filepath.Join(dir, "write-failure.json")
	if err := writeImmutableAuthorization(writeFailurePath, raw); err == nil {
		t.Fatal("injected write failure was accepted")
	}
	if _, err := os.Lstat(writeFailurePath); !os.IsNotExist(err) {
		t.Fatalf("partial write artifact remained: %v", err)
	}

	authorizationArtifactWrite = originalWrite
	authorizationArtifactSync = func(*os.File) error { return errors.New("injected sync failure") }
	syncFailurePath := filepath.Join(dir, "sync-failure.json")
	if err := writeImmutableAuthorization(syncFailurePath, raw); err == nil {
		t.Fatal("injected sync failure was accepted")
	}
	if _, err := os.Lstat(syncFailurePath); !os.IsNotExist(err) {
		t.Fatalf("unsynced artifact remained: %v", err)
	}
}

func TestWriteImmutableAuthorizationRejectsUnsafeExistingArtifacts(t *testing.T) {
	dir := shortTestTempDir(t, "amq-authz-collision-")
	raw := []byte("same bytes")

	existing := filepath.Join(dir, "existing.json")
	if err := os.WriteFile(existing, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeImmutableAuthorization(existing, raw); err != nil {
		t.Fatalf("identical regular immutable retry failed: %v", err)
	}
	if err := writeImmutableAuthorization(existing, []byte("different")); err == nil {
		t.Fatal("different bytes reused immutable artifact")
	}

	target := filepath.Join(dir, "target.json")
	if err := os.WriteFile(target, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := writeImmutableAuthorization(link, raw); err == nil {
		t.Fatal("symlink collision was accepted as an immutable retry")
	}

	hardlink := filepath.Join(dir, "hardlink.json")
	if err := os.Link(target, hardlink); err != nil {
		t.Fatal(err)
	}
	if err := writeImmutableAuthorization(hardlink, raw); err == nil {
		t.Fatal("multiply-linked collision was accepted as an immutable retry")
	}
}
