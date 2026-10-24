package gateverify

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 4-5 review item 2: share_root must name a directory under ~/.amq-squad/buzz/
// (guard's protected tree); anywhere else is a parse error.
func TestBindingShareRootMustBeUnderTheBuzzDir(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".amq-squad")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	owner, body := mustKey(t), mustKey(t)
	write := func(shareRoot string) error {
		raw, _ := json.Marshal(Binding{Schema: 1, Relay: "wss://r", Channel: "c", Owner: owner.PublicHex(), Body: body.PublicHex(), Carriers: []string{"buzz"}, ShareRoot: shareRoot})
		p := filepath.Join(dir, "gate-binding.json")
		if err := os.WriteFile(p, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		_, _, err := LoadBinding(p)
		return err
	}
	for _, ok := range []string{"", filepath.Join(home, ".amq-squad", "buzz", "root"), filepath.Join(home, ".amq-squad", "buzz", "other", "root")} {
		if err := write(ok); err != nil {
			t.Errorf("share_root %q refused: %v", ok, err)
		}
	}
	for _, bad := range []string{
		filepath.Join(home, "code", "repo", ".buzz"), // a seat-writable workspace
		filepath.Join(home, ".amq-squad", "buzz"),    // the directory itself, not inside it
		filepath.Join(home, ".amq-squad", "buzz-evil", "root"),
		filepath.Join(home, ".amq-squad", "buzz", "..", "x"),
		"relative/root",
	} {
		if err := write(bad); err == nil || !strings.Contains(err.Error(), "share_root") {
			t.Errorf("share_root %q accepted: %v", bad, err)
		}
	}
}
