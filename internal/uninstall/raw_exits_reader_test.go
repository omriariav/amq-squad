//go:build unix

package uninstall

import (
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/sealchain"
)

func TestCommitStepsReadsLegacyAndRawExitsLayouts(t *testing.T) {
	for _, tc := range []struct {
		name, layout, why, want string
	}{
		{"legacy idle", "legacy", "dispatch", ""},
		{"legacy commit", "legacy", "commit", "commit step of seat seat-a (task t9)"},
		{"raw idle", "raw", "dispatch", ""},
		{"raw commit", "raw", "commit", "commit step of seat seat-a (task t9)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			body := []byte(`{"schema":1,"queue":[{"handle":"seat-a","task_id":"t9","why":"` + tc.why + `"}]}`)
			if tc.layout == "raw" {
				var err error
				body, err = sealchain.JoinRaw(body, sealchain.RawSeal{Seq: 1, Tag: "fixture", Version: "v3.2.0-rc.2", Schema: 1})
				if err != nil {
					t.Fatal(err)
				}
			}
			f.writeExits(string(body))
			got := readCommitSteps(t, f)
			if tc.want == "" && len(got) != 0 {
				t.Fatalf("idle exits queue is busy: %v", got)
			}
			if tc.want != "" && (len(got) != 1 || !strings.Contains(got[0], tc.want)) {
				t.Fatalf("commit exits queue = %v, want %q", got, tc.want)
			}
		})
	}
}

func TestCommitStepsFailsClosedOnNeitherExitsLayout(t *testing.T) {
	f := newFixture(t)
	f.writeExits(`{"schema":1,"queue":[`)
	got := readCommitSteps(t, f)
	if len(got) != 1 || !strings.Contains(got[0], "cannot be parsed, so a commit step cannot be ruled out") {
		t.Fatalf("malformed exits queue = %v", got)
	}
}

func readCommitSteps(t *testing.T, f *fixture) []string {
	t.Helper()
	x, err := openDir(f.root, []string{extDir, amqDir})
	if err != nil {
		t.Fatal(err)
	}
	defer x.Close()
	sd, err := x.Sub("scheduler")
	if err != nil {
		t.Fatal(err)
	}
	defer sd.Close()
	return commitSteps(f.x, sd)
}
