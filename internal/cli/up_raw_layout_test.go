package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/sealchain"
	"github.com/omriariav/amq-squad/v3/internal/seatlaunch"
)

func rawLayoutUpRun(t *testing.T) *upRun {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "s")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	seed, err := sealchain.SessionKey(root)
	if err != nil {
		t.Fatal(err)
	}
	_, err = sealchain.WriteRaw(root, "commits.qa", seed, "test", nil, []byte(`{"schema":1}`), "v3.2.0-rc.2", 1, func([]byte) error { return nil }, time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	return &upRun{q: seatlaunch.Squad{BaseRoot: base, Session: "s"}}
}

func TestUpRefusesEveryBinaryBeforeTheRawLayout(t *testing.T) {
	u := rawLayoutUpRun(t)
	for _, version := range []string{"v3.1.2", "v3.2.0-rc.1"} {
		err := u.checkLedgerDowngrade(version)
		if err == nil || !strings.Contains(err.Error(), "predates that layout") || !strings.Contains(err.Error(), "roll forward or start a new session") {
			t.Fatalf("%s: %v", version, err)
		}
	}
	for _, version := range []string{"v3.2.0-rc.2", "v3.2.0", "v4.0.0"} {
		if err := u.checkLedgerDowngrade(version); err != nil {
			t.Fatalf("%s: %v", version, err)
		}
	}
}

func TestUpRefusesAnUnparseableTargetWhenRawLedgersExist(t *testing.T) {
	err := rawLayoutUpRun(t).checkLedgerDowngrade("(devel)")
	if err == nil || !strings.Contains(err.Error(), "could not be read") || !strings.Contains(err.Error(), "install a released build") {
		t.Fatal(err)
	}
}

func TestUpChecksRawLayoutBeforeTaggedPiBuild(t *testing.T) {
	u := rawLayoutUpRun(t)
	u.ctx = context.Background()
	u.report.Project = t.TempDir()
	old := squadRun
	t.Cleanup(func() { squadRun = old })
	var calls []string
	squadRun = func(_ context.Context, _ string, _ []string, _ string, args ...string) ([]byte, error) {
		calls = append(calls, strings.Join(args, " "))
		return []byte("amq-squad (devel)\n"), nil
	}
	err := u.checkLedgerTarget("/tmp/devel-amq-squad")
	if err == nil || !strings.Contains(err.Error(), "layout support cannot be known") {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0] != "version" {
		t.Fatalf("calls = %v; want only the version preflight", calls)
	}
}

func TestRawLayoutSemverPrecedence(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{
		{"v3.2.0-rc.1", "v3.2.0-rc.2", -1},
		{"v3.2.0-rc.2", "v3.2.0", -1},
		{"v3.2.0", "v3.2.0-rc.2", 1},
		{"v3.2.0", "v3.2.0", 0},
		{"v3.2.0+build.1", "v3.2.0+other", 0},
	} {
		if got, ok := compareReleaseVersions(tc.a, tc.b); !ok || got != tc.want {
			t.Errorf("compare(%s,%s) = %d,%v; want %d,true", tc.a, tc.b, got, ok, tc.want)
		}
	}
	for _, invalid := range []string{"(devel)", "v03.2.0", "v3.02.0", "v3.2.00"} {
		if _, ok := compareReleaseVersions(invalid, "v3.2.0"); ok {
			t.Fatalf("%s parsed as a release", invalid)
		}
	}
}

// E7: without a raw chain there is no downgrade floor to enforce.
func TestUpWithoutRawChainProceeds(t *testing.T) {
	base := t.TempDir()
	u := &upRun{q: seatlaunch.Squad{BaseRoot: base, Session: "s"}}
	if err := os.MkdirAll(u.q.SessionRoot(), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, version := range []string{"(devel)", "v3.1.2", "v03.2.0"} {
		if err := u.checkLedgerDowngrade(version); err != nil {
			t.Fatalf("%s without a raw chain: %v", version, err)
		}
	}
}
