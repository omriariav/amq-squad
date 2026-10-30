//go:build unix

package schedproc

import (
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/panechain"
)

// linuxSunPath is sizeof(sockaddr_un.sun_path) on Linux (108 bytes, the
// terminating NUL included); darwin's is 104.
const linuxSunPath = 108

// 5-ci-D D1 (the first v3 CI run, ubuntu): the decision socket under a
// session root whose launch path exceeds sun_path. The listener binds it
// (through /proc/self/fd/N/name on Linux, in a private temp directory on
// darwin) and the launch record carries the walked path; the lead's dial
// went to that path by name, which Linux refuses with EINVAL past 108
// bytes. SendDecision dials through seatlaunch.DialPrivate, bounded the
// way the bind is, so the decision is delivered at any root depth.
func TestDecisionSocketDialsUnderARootPastSunPath(t *testing.T) {
	base, _ := filepath.EvalSymlinks(t.TempDir())
	root := filepath.Join(base, strings.Repeat("d", 40), strings.Repeat("e", 40), ".agent-mail", "s")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	walked := filepath.Join(root, "extensions", "amq-squad", "launch", DecisionSocketName)
	if len(walked) <= linuxSunPath {
		t.Fatalf("the fixture root is too shallow: %d bytes", len(walked))
	}
	lead := panechain.Pane{Shell: 80, Start: "s80", Agent: "lead"}
	applied := 0
	s, err := ServeDecisions(DecideOptions{Root: root, Lead: "lead",
		Apply:      func(string, WidenRequest) DecideReply { applied++; return DecideReply{OK: true} },
		LeadPane:   func(string, string) (panechain.Pane, error) { return lead, nil },
		Reaches:    func(int, string, []panechain.Pane) (*panechain.Pane, bool) { return &lead, true },
		StartToken: func(int) (string, bool) { return "s", true },
		Peer:       func(*net.UnixConn) (int, bool) { return 4242, true },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	switch runtime.GOOS {
	case "linux":
		// the record carries the walked path, past sun_path; dialing it by
		// name is the failure the CI run hit
		if s.Path() != walked {
			t.Fatalf("recorded %s, want the walked path %s", s.Path(), walked)
		}
		if c, err := net.Dial("unix", s.Path()); err == nil {
			c.Close()
			t.Fatalf("a %d-byte path dialed by name on linux", len(s.Path()))
		}
	default:
		// darwin's listener moved to its private temp directory
		if len(s.Path()) >= 104 {
			t.Fatalf("recorded a %d-byte path on %s: %s", len(s.Path()), runtime.GOOS, s.Path())
		}
	}
	reply, err := SendDecision(root, s.Path(), WidenRequest{Kind: WidenRequestKind, Task: "t1", Files: []string{"b.go"}, Reason: "r"})
	if err != nil || !reply.OK || applied != 1 {
		t.Fatalf("the decision under a %d-byte launch path: %+v %v (applied %d)", len(walked), reply, err, applied)
	}
}
