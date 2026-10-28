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

	"github.com/omriariav/amq-squad/v3/internal/seatexec"
)

// pinSquad is one Codex seat whose provider, wrapper, worktree and session
// exist on disk, so the pinned AMQ library's own Prepare (admission and the
// trust digest, in process; Prepare never executes the provider) can run
// against the compiled request. No network, no amq binary, no tmux.
func pinSquad(t *testing.T, network bool) (Squad, string) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", filepath.Join(base, "home"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(base, "state"))
	bin := filepath.Join(base, "bin")
	for _, d := range []string{filepath.Join(base, "home"), bin, filepath.Join(base, "wt-dev"), filepath.Join(base, "proj", ".agent-mail", "s1", "agents")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"amq-squad", "amq"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// The adapter's Prepare-time probe: a codex-cli 0.163.0 stand-in that
	// answers --version, --help and resume --help as the capture contract
	// needs.
	codex := "#!/bin/sh\ncase \"$1 $2\" in\n\"--version \") echo 'codex-cli 0.163.0';;\n\"--help \") echo 'Commands: resume';;\n\"resume --help\") echo 'Usage: codex resume [SESSION_ID]';;\n*) exit 2;;\nesac\n"
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte(codex), 0o700); err != nil {
		t.Fatal(err)
	}
	// The project's committed AMQ config authorizes the base root (as
	// `amq setup` writes it).
	if err := os.WriteFile(filepath.Join(base, "proj", ".amqrc"), []byte(`{"root":".agent-mail"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	seat := Seat{Handle: "dev", Runtime: "codex", Model: "gpt-6-astra", Effort: "medium", Role: "# Role\n\nYou are the dev seat of this squad.\n", Worktree: filepath.Join(base, "wt-dev"), Provider: filepath.Join(bin, "codex")}
	if network {
		seat.EnvNeeds = []string{seatexec.CodexNetworkNeed}
	}
	q := Squad{ProjectRoot: filepath.Join(base, "proj"), Session: "s1", Launcher: "tmux", Wrapper: filepath.Join(bin, "amq-squad"), AMQPath: filepath.Join(bin, "amq"), Seats: []Seat{seat}}
	q, err = q.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	return q, bin
}

func preparePins(t *testing.T, q Squad, mutate func(args []string) []string) (launchapi.PrepareResultV1, []string, error) {
	t.Helper()
	c, err := Compile(q, "", lookPath, time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	p := &c.Request.Intent.Participants[1]
	if mutate != nil {
		p.Args = mutate(slices.Clone(p.Args))
	}
	res, err := launchapi.Prepare(context.Background(), c.Request)
	if err == nil && res.Outcome != launchapi.PrepareOutcomeReady && res.Outcome != launchapi.PrepareOutcomeActionRequired {
		t.Fatalf("prepare outcome %s (%s)", res.Outcome, res.Reason)
	}
	return res, p.Args, err
}

func swapArg(from, to string) func([]string) []string {
	return func(a []string) []string {
		for i := range a {
			if a[i] == from {
				a[i] = to
			}
		}
		return a
	}
}

// Brief 5-amq-A acceptance 4: a Codex seat's launch request carries the two
// sandbox pins as provider arguments; the pinned AMQ library admits them
// (its own Prepare, in process: the outcome is the trust confirmation, and
// it advertises both keys); the trust digest is stable for one request and
// changes when a pin changes (network false to true by hand, and through the
// seat's declared CODEX_NETWORK_ACCESS); a widening writable-roots value is
// not admitted at all.
func TestCodexSandboxPinsAreAdmittedAndDigested(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("the library plans a tmux launch only with tmux on PATH (Prepare reads the backend, never starts it)")
	}
	q, _ := pinSquad(t, false)
	closed, args, err := preparePins(t, q, nil)
	if err != nil {
		t.Fatalf("the pinned library refused the compiled pins: %v", err)
	}
	if !slices.Contains(args, "sandbox_workspace_write.writable_roots=[]") || !slices.Contains(args, "sandbox_workspace_write.network_access=false") {
		t.Fatalf("request args %q carry no pins", args)
	}
	var keys []string
	for _, c := range closed.Preview.Capabilities {
		if c.Provider == "codex" {
			for _, o := range c.ConfigOverrides {
				keys = append(keys, o.Key)
			}
		}
	}
	if !slices.Contains(keys, seatexec.CodexWritableRootsPin) || !slices.Contains(keys, seatexec.CodexNetworkAccessPin) {
		t.Fatalf("the library advertises codex overrides %v, want both pins", keys)
	}
	if err := CheckArgumentForms(q, closed.Preview.Capabilities); err != nil {
		t.Fatalf("the library's own advertisement refused the pins: %v", err)
	}
	again, _, err := preparePins(t, q, nil)
	if err != nil || closed.TrustDigest == "" || again.TrustDigest != closed.TrustDigest {
		t.Fatalf("the trust digest is not stable for one request: %q vs %q (%v)", closed.TrustDigest, again.TrustDigest, err)
	}
	// Row 1: the network pin flipped in the same request moves the digest.
	flipped, _, err := preparePins(t, q, swapArg("sandbox_workspace_write.network_access=false", "sandbox_workspace_write.network_access=true"))
	if err != nil {
		t.Fatal(err)
	}
	if flipped.TrustDigest == closed.TrustDigest {
		t.Fatalf("trust digest did not move when the network pin changed: %s", closed.TrustDigest)
	}
	// Row 2: dropping the writable-roots pin moves the digest too (the pin
	// is bound, not decoration).
	dropped, _, err := preparePins(t, q, func(a []string) []string {
		for i := range a {
			if a[i] == "sandbox_workspace_write.writable_roots=[]" {
				return append(a[:i-1], a[i+1:]...)
			}
		}
		return a
	})
	if err != nil {
		t.Fatal(err)
	}
	if dropped.TrustDigest == closed.TrustDigest {
		t.Fatal("trust digest did not move when the writable-roots pin was dropped")
	}
	// A widening writable-roots value is not admitted by the library.
	if _, _, err := preparePins(t, q, swapArg("sandbox_workspace_write.writable_roots=[]", `sandbox_workspace_write.writable_roots=["/"]`)); err == nil || !strings.Contains(err.Error(), "writable_roots") {
		t.Fatalf("a widened writable_roots was admitted: %v", err)
	}
	// The declared network need compiles to network_access=true.
	qn, _ := pinSquad(t, true)
	if _, argsN, err := preparePins(t, qn, nil); err != nil || !slices.Contains(argsN, "sandbox_workspace_write.network_access=true") {
		t.Fatalf("a seat with %s: args %q (%v)", seatexec.CodexNetworkNeed, argsN, err)
	}
}
