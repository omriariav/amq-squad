package status

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/task"
)

// v3-5-verifier, the fourth round's item 3: a malformed unclean marker is an
// attention row for the lead naming its file.
func TestStatusNamesAnInvalidUncleanMarker(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(task.DirFor(root), "reserved", "s3")
	os.MkdirAll(filepath.Dir(p), 0o700)
	os.WriteFile(p, []byte("garbage"), 0o600)
	st := Build(Collect(CollectOptions{Root: root, Now: time.Unix(100, 0)}))
	for _, a := range st.Attention {
		if a.Kind == "unclean_marker_invalid" && a.Seat == "s3" && a.Bucket == BucketNeedsYou && strings.Contains(a.Subject, p) {
			return
		}
	}
	t.Fatalf("no unclean_marker_invalid row naming %s", p)
}
