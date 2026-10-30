package status

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// D1 of the 2b-2 milestone B second pass: a seat whose commit ledger records
// no push URL commits and does not push; status names it (push_url_missing,
// needs_you), and a seat with a push URL is not named.
func TestStatusNamesASeatWithNoPushURL(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "extensions", "amq-squad", "commits")
	os.MkdirAll(dir, 0o700)
	os.WriteFile(filepath.Join(dir, "s1.json"), []byte(`{"schema":1,"handle":"s1","base":"x"}`), 0o600)
	os.WriteFile(filepath.Join(dir, "s2.json"), []byte(`{"schema":1,"handle":"s2","push_url":"git@github.com:o/r.git"}`), 0o600)
	st := Build(Collect(CollectOptions{Root: root, Now: time.Unix(100, 0)}))
	var seen []string
	for _, a := range st.Attention {
		if a.Kind == "push_url_missing" {
			seen = append(seen, a.Seat)
			if a.Bucket != BucketNeedsYou {
				t.Fatalf("bucket %s", a.Bucket)
			}
		}
	}
	if len(seen) != 1 || seen[0] != "s1" {
		t.Fatalf("push_url_missing for %v", seen)
	}
}
