package status

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/sealchain"
)

// D1 of the 2b-2 milestone B second pass: a seat whose commit ledger records
// no push URL commits and does not push; status names it (push_url_missing,
// needs_you), and a seat with a push URL is not named.
func TestStatusNamesASeatWithNoPushURL(t *testing.T) {
	for _, layout := range []string{"legacy", "raw"} {
		t.Run(layout, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "extensions", "amq-squad", "commits")
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			writeCommitStatusFixture(t, filepath.Join(dir, "s1.json"), []byte(`{"schema":1,"handle":"s1","base":"x"}`), layout)
			writeCommitStatusFixture(t, filepath.Join(dir, "s2.json"), []byte(`{"schema":1,"handle":"s2","push_url":"git@github.com:o/r.git"}`), layout)
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
		})
	}
}

func writeCommitStatusFixture(t *testing.T, path string, body []byte, layout string) {
	t.Helper()
	raw := body
	if layout == "raw" {
		var err error
		raw, err = sealchain.JoinRaw(body, sealchain.RawSeal{Seq: 1, Tag: "fixture", Version: "v3.2.0-rc.2", Schema: 1})
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}
