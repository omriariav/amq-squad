package seatlaunch

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/avivsinai/agent-message-queue/launchapi"

	"github.com/omriariav/amq-squad/v3/internal/piruntime"
)

// piPrepare compiles one pi seat behind a real shim (piruntime.EnsureShim)
// for a stand-in amq-squad whose `pi shim --version` prints version, and
// runs the pinned AMQ library's Prepare on it in process.
func piPrepare(t *testing.T, version string) (launchapi.PrepareResultV1, string) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(base, "home")
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", filepath.Join(base, "state"))
	bin := filepath.Join(base, "bin")
	for _, d := range []string{home, bin, filepath.Join(base, "wt"), filepath.Join(base, "proj", ".agent-mail", "s1", "agents")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(base, "proj", ".amqrc"), []byte(`{"root":".agent-mail"}`), 0o600)
	self := filepath.Join(bin, "amq-squad")
	stand := "#!/bin/sh\n[ \"$1 $2\" = 'pi shim' ] || exit 2\ncase \"$3\" in\n--version) echo '" + version + "';;\n--help) echo 'Usage: pi [options]'; echo '  --session-id <id>  Use an exact session id';;\n*) exit 2;;\nesac\n"
	if err := os.WriteFile(self, []byte(stand), 0o700); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(bin, "amq"), []byte("#!/bin/sh\nexit 0\n"), 0o700)
	shim, err := piruntime.EnsureShim(home, self)
	if err != nil {
		t.Fatal(err)
	}
	q := Squad{ProjectRoot: filepath.Join(base, "proj"), Session: "s1", Launcher: "tmux", Wrapper: self, AMQPath: filepath.Join(bin, "amq"), PiShim: shim,
		Seats: []Seat{{Handle: "scout", Runtime: "pi", Model: "ollama/qwen3.6:27b", Role: "# Role\n\nThe scout seat.\n", Worktree: filepath.Join(base, "wt")}}}
	q, err = q.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	c, err := Compile(q, "", lookPath, time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	res, err := launchapi.Prepare(context.Background(), c.Request)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	return res, shim
}

// Brief 5-amq-A acceptance 2 (in process): the pinned library's pi adapter
// probes the shim (through it, the embedded runtime's answer), accepts 1.3.0,
// and plans `<wrapper> seat-exec --seat H <shim> --session-id <id>`, the id a
// slot the library fills from the launch nonce; a pi below the 1.2.0 floor is
// not planned.
func TestPiSeatPlansThroughTheUpstreamAdapter(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("the library plans a tmux launch only with tmux on PATH")
	}
	res, shim := piPrepare(t, "1.3.0")
	if res.Outcome != launchapi.PrepareOutcomeActionRequired && res.Outcome != launchapi.PrepareOutcomeReady {
		t.Fatalf("outcome %s (%s)", res.Outcome, res.Reason)
	}
	var argv []string
	for _, p := range res.Preview.Participants {
		if p.Handle == "scout" {
			if p.PlannedOutcome != "fresh" || p.Command == nil {
				t.Fatalf("scout planned %q", p.PlannedOutcome)
			}
			argv = p.Command.Argv
		}
	}
	i := slices.Index(argv, shim)
	if i < 3 || argv[i-3] != "seat-exec" || argv[i+1] != "--session-id" || !strings.Contains(argv[i+2], "${") || len(argv) != i+3 {
		t.Fatalf("planned argv %q, want <wrapper> seat-exec --seat scout %s --session-id <slot>", argv, shim)
	}
	var caps []string
	for _, c := range res.Preview.Capabilities {
		if c.Provider == "pi" {
			caps = append(caps, c.AllowedArgumentForms...)
			if c.GrammarVersion != 1 {
				t.Errorf("pi grammar %d", c.GrammarVersion)
			}
		}
	}
	if len(caps) != 0 {
		t.Fatalf("pi admits provider arguments now: %v (amendment 23 (b) watch item)", caps)
	}
	// The library does not probe a mint-mode adapter at Prepare ("Mint-mode
	// adapters are not probed", internal/launch/prepare.go): the 1.2.0
	// version floor and the --session-id help check run at Apply, against
	// the shim, which answers from the embedded runtime (the live row).
	if old, _ := piPrepare(t, "1.1.9"); old.Outcome != res.Outcome {
		t.Fatalf("Prepare probed the pi shim: %s vs %s", old.Outcome, res.Outcome)
	}
}
