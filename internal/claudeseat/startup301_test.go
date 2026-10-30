package claudeseat

import (
	"path/filepath"
	"testing"
	"time"
)

// 3.0.1 t9 (reviewer ruling 2): a Claude seat's startup screens are not
// verified closed until t6 seeds and verifies the config dir, so the seat's
// wake stays off; StartupScreensClosed says so for the dir Write produces.
func TestStartupScreensClosedIsUnverifiedUntilT6(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "config")
	if _, err := Write(dir, "dontAsk", "", time.Unix(0, 0)); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{dir, t.TempDir()} {
		if ok, why := StartupScreensClosed(d); ok || why != OnboardingUnverified {
			t.Fatalf("%s: %t %q", d, ok, why)
		}
	}
}
