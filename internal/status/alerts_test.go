package status

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// 3-2 ruling 12: an ungated tag or release the merge train or doctor found
// reaches the owner as a needs_you attention item.
func TestOwnerAlertBecomesNeedsYouAttention(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "extensions", "amq-squad", "alerts")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := `{"id":"ungated-tag-0a1b2c","repository":"o/r","kind":"tag","name":"v9","sha":"abc","at":"2026-10-24T12:00:00Z","detail":"tag v9 on o/r has no accepted owner gate"}`
	if err := os.WriteFile(filepath.Join(dir, "ungated-tag-0a1b2c.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	// a baseline file is not an alert
	if err := os.WriteFile(filepath.Join(dir, "baseline-o--r.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	in := Collect(CollectOptions{Root: root, Session: "s", Now: time.Date(2026, 10, 24, 13, 0, 0, 0, time.UTC)})
	if len(in.Alerts) != 1 {
		t.Fatalf("alerts %+v (warnings %v)", in.Alerts, in.Warnings)
	}
	s := Build(in)
	var found *Attention
	for i := range s.Attention {
		if s.Attention[i].ID == "alert:ungated-tag-0a1b2c" {
			found = &s.Attention[i]
		}
	}
	if found == nil || found.Bucket != BucketNeedsYou || found.Kind != "ungated_tag" || found.Severity != "high" {
		t.Fatalf("attention %+v", s.Attention)
	}
}
