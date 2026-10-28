//go:build unix

package seatexec

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/launch"
)

// Brief 5-amq-A acceptance 4: the sandbox pins travel in the launch request,
// so seat-exec adds none and refuses an admitted argv that does not carry
// both exactly as the record requires (sandbox_pins_missing); the refusal is
// published as the seat's exec record. A widening value keeps amendment 28's
// codex_sandbox_missing.
func TestSeatExecRefusesAnArgvWithoutTheSandboxPins(t *testing.T) {
	base := []string{"--sandbox", "workspace-write", "--ask-for-approval", "on-request"}
	roots, closed, open := []string{"-c", CodexWritableRootsPin + "=[]"}, []string{"-c", CodexNetworkAccessPin + "=false"}, []string{"-c", CodexNetworkAccessPin + "=true"}
	join := func(parts ...[]string) []string { return slices.Concat(parts...) }
	for _, c := range []struct {
		name    string
		network bool
		args    []string
		code    string
	}{
		{"no pins", false, base, SandboxPinsMissing},
		{"roots only", false, join(base, roots), SandboxPinsMissing},
		{"network only", false, join(base, closed), SandboxPinsMissing},
		{"network open without the need", false, join(base, roots, open), SandboxPinsMissing},
		{"network closed with the need", true, join(base, roots, closed), SandboxPinsMissing},
		{"a pin twice", false, join(base, roots, closed, roots), SandboxPinsMissing},
		{"the network pin attached", false, join(base, roots, []string{"-c" + CodexNetworkAccessPin + "=true"}), SandboxPinsMissing},
		{"widened roots", false, join(base, []string{"-c", CodexWritableRootsPin + `=["/"]`}, closed), "codex_sandbox_missing"},
		{"another sandbox key", false, join(base, roots, closed, []string{"-c", "sandbox_workspace_write.exclude_slash_tmp=false"}), "codex_sandbox_missing"},
		{"pins as compiled", false, join(base, CodexSandboxPins(false)), ""},
		{"pins as compiled, network declared", true, join(base, CodexSandboxPins(true)), ""},
	} {
		f := newFixture(t, "codex")
		if c.network {
			f.rec.EnvNeeds = []string{CodexNetworkNeed}
		}
		f.stage()
		argv := append([]string{filepath.Join(f.bin, "codex")}, c.args...)
		err := f.run(Options{Argv: argv})
		if refusalCode(err) != c.code {
			t.Errorf("%s: %v, want %q", c.name, err, c.code)
			continue
		}
		if c.code == "" {
			if !slices.Equal(f.execArgv, argv) {
				t.Errorf("%s: exec argv %q, want the admitted argv unchanged", c.name, f.execArgv)
			}
			continue
		}
		if f.execPath != "" {
			t.Errorf("%s: exec ran", c.name)
		}
		rec, rerr := launch.Read(filepath.Join(f.root, "agents", "cto"))
		if rerr != nil || rec.Exec == nil || rec.Exec.ReasonCode != c.code {
			t.Errorf("%s: published record %+v %v", c.name, rec.Exec, rerr)
		}
	}
}
