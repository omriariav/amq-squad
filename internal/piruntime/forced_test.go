package piruntime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The forced environment (ruling (a), the jiti cache fix): present on every
// launch, and neither the parent environment nor a caller's overlay can
// change it; parent JITI_* variables never reach pi.
func TestForcedEnvCannotBeOverridden(t *testing.T) {
	e := fixture(t)
	rt, _, err := Ensure(tempHome(t), e)
	if err != nil {
		t.Fatal(err)
	}
	home := tempHome(t)
	tmp := filepath.Join(t.TempDir(), "agents", "pi-1", "extensions", "x", "tmp")
	parent := []string{"PATH=/usr/bin", "JITI_FS_CACHE=true", "JITI_CACHE=true", "JITI_TRY_NATIVE=true", "JITI_ALIAS={}", "TMPDIR=/tmp", "PI_TELEMETRY=1", "PI_OFFLINE=0",
		"NODE_OPTIONS=--require=/tmp/evil.cjs", "NODE_PATH=/tmp/mods", "NODE_REPL_EXTERNAL_MODULE=/tmp/repl.js", "NODE_EXTRA_CA_CERTS=/tmp/ca.pem"}
	overlay := map[string]string{"JITI_FS_CACHE": "true", "TMPDIR": "/tmp", "PI_TELEMETRY": "1", "PI_CODING_AGENT_DIR": "/elsewhere", "AM_ME": "pi-1"}
	l, err := Prepare(Spec{Runtime: rt, Home: home, Seat: "pi-1", Parent: parent, Overlay: overlay, TmpDir: tmp})
	if err != nil {
		t.Fatal(err)
	}
	count := map[string]int{}
	got := map[string]string{}
	for _, kv := range l.Env {
		k, v, _ := strings.Cut(kv, "=")
		count[k]++
		got[k] = v
	}
	want := Forced(AgentDirFor(home, "pi-1"), tmp, rt.Dir, rt.ManifestSHA256)
	for k, v := range want {
		if got[k] != v || count[k] != 1 {
			t.Fatalf("%s = %q (%d times), want %q once", k, got[k], count[k], v)
		}
	}
	if want["JITI_FS_CACHE"] != "false" || want["TMPDIR"] != tmp {
		t.Fatalf("forced set lost the jiti cache switch or TMPDIR: %v", want)
	}
	for _, k := range []string{"JITI_CACHE", "JITI_TRY_NATIVE", "JITI_ALIAS", "NODE_PATH", "NODE_REPL_EXTERNAL_MODULE", "NODE_EXTRA_CA_CERTS"} {
		if _, ok := got[k]; ok {
			t.Fatalf("%s reached pi", k)
		}
	}
	// Review item 1: NODE_OPTIONS is present and empty, never the parent's.
	if v, ok := got["NODE_OPTIONS"]; !ok || v != "" || count["NODE_OPTIONS"] != 1 {
		t.Fatalf("NODE_OPTIONS = %q (present %t, %d times), want set and empty once", v, ok, count["NODE_OPTIONS"])
	}
	if got["AMQ_SQUAD_RUNTIME_DIR"] != rt.Dir || got["AMQ_SQUAD_RUNTIME_MANIFEST_SHA256"] != rt.ManifestSHA256 || rt.ManifestSHA256 == "" {
		t.Fatal("the in-process runtime check variables are missing")
	}
	if got["AM_ME"] != "pi-1" {
		t.Fatal("a caller overlay outside the forced set was dropped")
	}
	fi, err := os.Lstat(tmp)
	if err != nil || !fi.IsDir() || fi.Mode().Perm() != 0o700 {
		t.Fatalf("seat TMPDIR %s: %v %v", tmp, fi, err)
	}
	// The default TMPDIR is under the agent dir; a symlink in its place is refused.
	l2, err := Prepare(Spec{Runtime: rt, Home: home, Seat: "pi-2"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(l2.Env, "\n"), "TMPDIR="+filepath.Join(AgentDirFor(home, "pi-2"), "tmp")) {
		t.Fatal("default TMPDIR is not under the agent dir")
	}
	bad := filepath.Join(t.TempDir(), "tmp")
	if err := os.Symlink(t.TempDir(), bad); err != nil {
		t.Fatal(err)
	}
	if _, err := Prepare(Spec{Runtime: rt, Home: home, Seat: "pi-3", TmpDir: bad}); err == nil {
		t.Fatal("a symlinked TMPDIR was accepted")
	}
}
