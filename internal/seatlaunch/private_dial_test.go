//go:build unix

package seatlaunch

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 5-ci-D D1: DialPrivate dials only a socket of this user's in the root's
// private launch directory. A regular file, a symlink (even to a live
// socket of ours) or a foreign-uid socket planted under the name is
// refused before any connect, and a recorded path outside the launch
// directory and the private temp directory is refused outright.
func TestDialPrivateRefusesAPlantedEntry(t *testing.T) {
	const name = "scheduler-decide.sock"
	newRoot := func(t *testing.T) (string, string) {
		root, _ := filepath.EvalSymlinks(t.TempDir())
		launchDir := filepath.Join(root, "extensions", "amq-squad", "launch")
		if err := os.MkdirAll(launchDir, 0o700); err != nil {
			t.Fatal(err)
		}
		return root, filepath.Join(launchDir, name)
	}
	refused := func(t *testing.T, root, recorded, want string) {
		t.Helper()
		c, err := DialPrivate(root, recorded, name)
		if err == nil {
			c.Close()
			t.Fatalf("DialPrivate connected to %s", recorded)
		}
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("DialPrivate(%s) = %v, want a refusal naming %q", recorded, err, want)
		}
	}

	t.Run("a regular file", func(t *testing.T) {
		root, recorded := newRoot(t)
		if err := os.WriteFile(recorded, []byte("not a socket"), 0o600); err != nil {
			t.Fatal(err)
		}
		refused(t, root, recorded, "is not a socket owned by this user")
	})
	t.Run("a symlink to a live socket", func(t *testing.T) {
		root, recorded := newRoot(t)
		other, _ := filepath.EvalSymlinks(t.TempDir())
		live, err := ListenPrivate(other, name)
		if err != nil {
			t.Fatal(err)
		}
		defer live.Close()
		if err := os.Symlink(live.Path, recorded); err != nil {
			t.Fatal(err)
		}
		refused(t, root, recorded, "is not a socket owned by this user")
	})
	t.Run("a foreign-uid socket", func(t *testing.T) {
		root, _ := newRoot(t)
		sock, err := ListenPrivate(root, name)
		if err != nil {
			t.Fatal(err)
		}
		defer sock.Close()
		// the dial works for its owner
		c, err := DialPrivate(root, sock.Path, name)
		if err != nil {
			t.Fatalf("the owner's dial: %v", err)
		}
		c.Close()
		prev := socketOwnerUID
		socketOwnerUID = func() int { return os.Getuid() + 1 }
		defer func() { socketOwnerUID = prev }()
		refused(t, root, sock.Path, "is not a socket owned by this user")
	})
	t.Run("a directory that is not private", func(t *testing.T) {
		root, _ := newRoot(t)
		sock, err := ListenPrivate(root, name)
		if err != nil {
			t.Fatal(err)
		}
		defer sock.Close()
		// the launch directory, or darwin's private temp directory
		if err := os.Chmod(filepath.Dir(sock.Path), 0o755); err != nil {
			t.Fatal(err)
		}
		refused(t, root, sock.Path, "is not a private (0700, own) directory")
	})
	t.Run("a path outside the launch directory", func(t *testing.T) {
		root, _ := newRoot(t)
		dir, err := os.MkdirTemp("", "dial")
		if err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(dir)
		elsewhere := filepath.Join(dir, name)
		ln, err := net.Listen("unix", elsewhere)
		if err != nil {
			t.Fatal(err)
		}
		defer ln.Close()
		refused(t, root, elsewhere, "neither in")
	})
}
