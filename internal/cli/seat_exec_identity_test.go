//go:build unix

package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/launch"
)

// Reviewer row (R52, fail closed): a launch record without AM_SESSION makes
// seat-exec exit with its own code, whatever amq would accept.
func TestSeatExecIdentityIncompleteExitCode(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "proj", ".agent-mail", "s1")
	staged := launch.StagedDir(root, "cto")
	for _, d := range []string{filepath.Join(root, "agents", "cto"), staged} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	filepath.Walk(base, func(p string, info os.FileInfo, err error) error {
		if err == nil && info.IsDir() {
			os.Chmod(p, 0o700)
		}
		return nil
	})
	b, _ := launch.Marshal(launch.Record{Handle: "cto", Root: root, BaseRoot: filepath.Dir(root), Runtime: "claude"})
	mustWrite(t, filepath.Join(staged, launch.FileName), string(b))
	t.Setenv("AM_ME", "cto")
	t.Setenv("AM_ROOT", root)
	t.Setenv("AM_SESSION", "")
	_, _, err = captureOutput(t, func() error { return Run([]string{"seat-exec", "--seat", "cto", "/bin/claude"}, "test") })
	if ExitCode(err) != ExitIdentityIncomplete || !strings.Contains(err.Error(), "identity_incomplete") || !strings.Contains(err.Error(), "AM_SESSION") {
		t.Fatalf("record without a session: exit %d, %v", ExitCode(err), err)
	}
}
