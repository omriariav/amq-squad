package seatcommit

import (
	"archive/zip"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/grants"
	"github.com/omriariav/amq-squad/v3/internal/registry"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// The rows of the fix commit on the Codex pass of v3-5-verifier.

// editLease rewrites the task file's lease, as a same-UID writer without the
// session seed would.
func (f *fixture) editLease(id string, fn func(*task.Lease)) {
	f.t.Helper()
	p := filepath.Join(task.DirFor(f.root), id+".json")
	raw, err := os.ReadFile(p)
	if err != nil {
		f.t.Fatal(err)
	}
	var tk task.Task
	if err := json.Unmarshal(raw, &tk); err != nil {
		f.t.Fatal(err)
	}
	fn(tk.Lease)
	out, _ := json.Marshal(tk)
	if err := os.WriteFile(p, out, 0o600); err != nil {
		f.t.Fatal(err)
	}
}

// P1-a: an unsealed pass verdict written into the lease, on the snapshot's
// tree and the grant's command, is no verdict at the commit step: the
// verifier runs (here it fails), and nothing is pushed on the stale pass.
func TestUnsealedPassIsNotReusedAtTheCommitStep(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n\n// stale pass\n")
	f.verifying(f.script("v", "echo verifier-ran\nexit 1\n"))
	tk := f.reportedBare("cmd/**")
	dry := f.opts(tk)
	dry.DryRun = true
	tree := Run(dry).Tree
	digest := task.CommandDigest(f.grants.Verifiers["implement"])
	f.editLease(tk.ID, func(l *task.Lease) {
		l.Snapshot, l.SnapshotCommand = tree, digest
		l.Verdict = &task.Verdict{Verifier: task.VerifierTests, Result: task.VerdictPass, Source: VerifierSource, Tree: tree, CommandDigest: digest, At: f.now}
	})
	res := f.run(tk)
	if _, ran := f.evidence(); !ran || res.ReasonCode != task.BlockVerdictFailed || res.Pushed || f.originBranch() != "" {
		t.Fatalf("an unsealed pass: verifier ran %v, %s, pushed %v", ran, res.ReasonCode, res.Pushed)
	}
}

// P1-b: truncation never defeats the redaction: a credential value that
// straddles the output cap is dropped with its partial line, from the
// ledger copy and from the 4 KiB tail.
func TestTruncatedOutputNeverKeepsACutCredential(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n\n// cut\n")
	value := "Ab9" + "xY7qR" + strings.Repeat("Lm2Wq8Zt", 4) // built at run time, never a literal
	// 123 filler lines of 33 bytes end at byte 4059: the password line
	// starts 37 bytes before the 4096-byte cap and its value crosses it.
	script := "i=0\nwhile [ $i -lt 123 ]; do echo 0123456789abcdef0123456789abcdef; i=$((i+1)); done\n" +
		"printf '%s\\n' '{\"password\":\"" + value + "\"}'\necho after\n/bin/sleep 5\n"
	f.verifying(f.script("v", script))
	f.grants.VerifierOutputLimit = 4096
	tk := f.reportedBare("cmd/**")
	res := f.run(tk)
	if res.ReasonCode != task.BlockVerdictFailed || res.Verifier == nil || !res.Verifier.Truncated {
		t.Fatalf("a capture over its limit: %s %+v", res.ReasonCode, res.Verifier)
	}
	l, _ := f.evidence()
	if strings.Contains(l.Payload.Output, value[:8]) || strings.Contains(res.Detail, value[:8]) {
		t.Fatal("a credential cut at the cap reached the ledger or the block detail")
	}
	if !strings.Contains(l.Payload.Output, "the partial last line was dropped") {
		t.Fatal("the marker does not say the partial line was dropped")
	}
}

// P2-a: a grant command changed between two runs of one attempt (a sealed
// pass, then a deferred push) supersedes the verdict on the same tree: the
// second run records, the push happens, the task completes, and the new
// verdict names the one it superseded.
func TestChangedCommandSupersedesTheVerdict(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n\n// supersede\n")
	first := f.script("v1", "echo first\n")
	f.verifying(first)
	tk := f.reportedBare("cmd/**")
	objects := filepath.Join(f.origin, "objects")
	chmodTree(objects, 0o555)
	res := f.run(tk)
	chmodTree(objects, 0o755)
	if !res.Deferred || f.lease(tk.ID).Verdict == nil {
		t.Fatalf("a push that cannot write the remote: deferred %v %s", res.Deferred, res.Detail)
	}
	f.verifying(f.script("v2", "echo second\n"))
	res = f.run(tk)
	if !res.OK || !res.Pushed {
		t.Fatalf("the run after the command changed: ok %v %s %s", res.OK, res.ReasonCode, res.Detail)
	}
	after, err := f.store.CompleteBound(tk.ID, fixtureRepo, 0, res.Commit, f.now)
	if err != nil || after.State != task.StateCompleted {
		t.Fatalf("completion: %s %s %v", after.State, after.BlockReason, err)
	}
	if v := after.Attempts[len(after.Attempts)-1].Verdict; v == nil || v.PrevCommandDigest != task.CommandDigest(first) {
		t.Fatal("the completing verdict does not name the one it superseded")
	}
}

func chmodTree(dir string, mode os.FileMode) {
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			os.Chmod(p, mode)
		}
		return nil
	})
}

// (i): a grant command as an argv list keeps an argument with a blank.
func TestVerifierCommandAsAnArgvList(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n\n// argv\n")
	p := f.script("v", "echo \"[$1]\"\n")
	g, err := grants.ParseOwner("schema: 1\nverifiers:\n  implement: ["+p+", \"two words\"]\n", "grants.yaml")
	if err != nil {
		t.Fatal(err)
	}
	f.grants.Verifiers, f.grants.VerifierArgv = g.Verifiers, g.VerifierArgv
	res := f.run(f.reportedBare("cmd/**"))
	l, _ := f.evidence()
	if !res.OK || !strings.Contains(l.Payload.Output, "[two words]") {
		t.Fatalf("an argv list: ok %v %s", res.OK, res.ReasonCode)
	}
}

// (b): the verifier's module cache is its own under the session root
// (0700), shared across runs, read-only to the build: two runs, one fetch
// (the second runs with the proxy gone). GOPROXY is the grant's
// (verifier_goproxy, a file:// proxy: no network).
func TestVerifierModuleCacheTwoRunsOneFetch(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go on PATH to pin")
	}
	if goBin, err = filepath.EvalSymlinks(goBin); err != nil {
		t.Fatal(err)
	}
	f := newFixture(t)
	t.Cleanup(func() { chmodTree(filepath.Join(VerifierDir(f.root), "gomodcache"), 0o755) })
	sum, err := registry.FileSHA256(goBin)
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Write(registry.Path(f.home), registry.Registry{Schema: 1, Pins: []registry.Pin{{Name: "go", Path: goBin, SHA256: sum}}}); err != nil {
		t.Fatal(err)
	}
	proxy := filepath.Join(f.dir, "proxy")
	goSum := moduleProxy(t, goBin, proxy)
	write(t, filepath.Join(f.wt, "go.mod"), "module example.com/seat\n\ngo 1.22\n\nrequire example.com/dep v1.0.0\n")
	write(t, filepath.Join(f.wt, "go.sum"), goSum)
	f.verifying("go mod download")
	f.grants.VerifierGoproxy = "file://" + proxy
	tk := f.reportedBare("go.mod", "go.sum", "cmd/**")
	res := f.run(tk)
	if !res.OK {
		l, _ := f.evidence()
		t.Fatalf("the first run: %s (%d bytes of output)", res.ReasonCode, len(l.Payload.Output))
	}
	if _, err := f.store.CompleteBound(tk.ID, fixtureRepo, 0, res.Commit, f.now); err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(VerifierDir(f.root), "gomodcache")
	if st, err := os.Stat(cache); err != nil || st.Mode().Perm() != 0o700 {
		t.Fatalf("the module cache: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cache, "cache", "download", "example.com", "dep", "@v", "v1.0.0.zip")); err != nil {
		t.Fatal("the first run did not fetch into the verifier's module cache")
	}
	if err := os.RemoveAll(proxy); err != nil {
		t.Fatal(err)
	}
	// The second task starts from the base (3.0.2 t23): it brings its own
	// module files, and the cache it shares with the first run serves them.
	write(t, filepath.Join(f.wt, "go.mod"), "module example.com/seat\n\ngo 1.22\n\nrequire example.com/dep v1.0.0\n")
	write(t, filepath.Join(f.wt, "go.sum"), goSum)
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n\n// second run\n")
	res = f.run(f.reportedBare("go.mod", "go.sum", "cmd/**"))
	if !res.OK {
		t.Fatalf("the second run fetched again (the proxy is gone): %s", res.ReasonCode)
	}
}

// moduleProxy writes a file:// module proxy holding example.com/dep v1.0.0
// and returns the go.sum lines for it, computed by the pinned go itself with
// the checksum database off (a test-side step; the verifier keeps it on).
func moduleProxy(t *testing.T, goBin, dir string) string {
	t.Helper()
	at := filepath.Join(dir, "example.com", "dep", "@v")
	write(t, filepath.Join(at, "list"), "v1.0.0\n")
	write(t, filepath.Join(at, "v1.0.0.info"), `{"Version":"v1.0.0","Time":"2026-01-01T00:00:00Z"}`)
	mod := "module example.com/dep\n\ngo 1.22\n"
	write(t, filepath.Join(at, "v1.0.0.mod"), mod)
	zf, err := os.Create(filepath.Join(at, "v1.0.0.zip"))
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(zf)
	for name, body := range map[string]string{"go.mod": mod, "dep.go": "package dep\n\n// V is the dependency's value.\nconst V = 1\n"} {
		w, err := zw.Create("example.com/dep@v1.0.0/" + name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(body))
	}
	zw.Close()
	zf.Close()
	scratch := t.TempDir()
	t.Cleanup(func() { chmodTree(scratch, 0o755) })
	cmd := exec.Command(goBin, "mod", "download", "-json", "example.com/dep@v1.0.0")
	cmd.Dir = scratch
	cmd.Env = []string{"HOME=" + scratch, "GOPATH=" + filepath.Join(scratch, "gopath"), "GOMODCACHE=" + filepath.Join(scratch, "modcache"), "GOCACHE=" + filepath.Join(scratch, "gocache"),
		"GOPROXY=file://" + dir, "GOSUMDB=off", "GOTOOLCHAIN=local", "GOENV=off", "GOFLAGS=-mod=mod"}
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("the test's go mod download: %v", err)
	}
	var m struct{ Sum, GoModSum string }
	if err := json.Unmarshal(out, &m); err != nil || m.Sum == "" || m.GoModSum == "" {
		t.Fatalf("the test's go mod download gave no sums: %v", err)
	}
	return "example.com/dep v1.0.0 " + m.Sum + "\nexample.com/dep v1.0.0/go.mod " + m.GoModSum + "\n"
}
