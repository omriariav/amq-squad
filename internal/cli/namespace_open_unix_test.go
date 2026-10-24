//go:build unix

package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	squadnamespace "github.com/omriariav/amq-squad/v3/internal/namespace"
	"github.com/omriariav/amq-squad/v3/internal/team"
)

func TestNamespaceAdmissionContainedOpenRejectsAncestorSwapWithoutOutsideSideEffects(t *testing.T) {
	project := t.TempDir()
	controlDir := filepath.Join(project, team.DirName)
	if err := os.MkdirAll(controlDir, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	sentinel := filepath.Join(outside, "sentinel")
	if err := os.WriteFile(sentinel, []byte("unchanged\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	originalHook := namespaceLockBeforeContainedCreate
	var once sync.Once
	namespaceLockBeforeContainedCreate = func(gotProject, _ string) error {
		var hookErr error
		once.Do(func() {
			if filepath.Clean(gotProject) != filepath.Clean(project) {
				hookErr = fmt.Errorf("unexpected project %s", gotProject)
				return
			}
			if err := os.Rename(controlDir, controlDir+".original"); err != nil {
				hookErr = err
				return
			}
			hookErr = os.Symlink(outside, controlDir)
		})
		return hookErr
	}
	t.Cleanup(func() { namespaceLockBeforeContainedCreate = originalHook })

	lock, err := acquireNamespaceWriterAdmission(project, squadnamespace.DefaultProfile, "source-run")
	if lock != nil {
		lock.close()
	}
	if err == nil {
		t.Fatal("ancestor swap unexpectedly admitted writer")
	}
	entries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	if !reflect.DeepEqual(names, []string{"sentinel"}) {
		t.Fatalf("contained lock creation changed outside tree: %v", names)
	}
	assertMigrationPayload(t, sentinel, "unchanged\n")
}
