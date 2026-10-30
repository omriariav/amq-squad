package registry

// One test row per item of the third Codex review pass on 3cc7b779
// (registry side).

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// Item 1: detection reads no runtime file; a FIFO at the manifest path
// cannot hang it.
func TestDetectReadsNoRuntimeFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fifo")
	}
	home := t.TempDir()
	rt := filepath.Join(home, ".amq-squad", "runtime", "1.3.0-abc")
	os.MkdirAll(rt, 0o700)
	if err := mkfifo(filepath.Join(rt, RuntimeManifestName)); err != nil {
		t.Skip("mkfifo: " + err.Error())
	}
	f := &fakeHost{}
	done := make(chan Detection, 1)
	go func() { done <- Detect(context.Background(), f.env(home)) }()
	select {
	case d := <-done:
		if pi := d.Tool("pi"); !pi.Found || pi.Path != rt || pi.Version != "" {
			t.Fatalf("pi row %+v", pi)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("detection hung on a FIFO manifest")
	}
}

// Item 2: the RESULT line is compared exactly, without trimming.
func TestResultLineMustBeExact(t *testing.T) {
	for _, last := range []string{" RESULT: PASS ", "RESULT: PASS ", "\tRESULT: PASS"} {
		r := Evaluate(honestEvents(t, last), cleanWorktree)
		if r.Pass || r.Checks["final_line_exact"] || !r.FalseSuccess {
			t.Fatalf("%q: %+v", last, r)
		}
	}
	if r := Evaluate(honestEvents(t, "RESULT: PASS"), cleanWorktree); !r.Pass {
		t.Fatalf("the exact line: %+v", r)
	}
}

// Item 3: a descendant that calls setsid leaves the probe's process group
// and session; it is still killed before the verifier looks, and the
// pristine copy is made under the verify root and removed.
func TestProbeKillsSetsidDescendants(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process groups")
	}
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not on PATH")
	}
	marker := filepath.Join(t.TempDir(), "escaped")
	script := "#!/bin/sh\n" +
		"\"" + py + "\" -I -c 'import os,sys,time; os.setsid(); time.sleep(1.5); open(sys.argv[1],\"w\").write(\"x\")' \"" + marker + "\" &\n" +
		"sleep 0.5\n" +
		"printf '%s\\n' '{\"type\":\"message_end\",\"message\":{\"role\":\"assistant\",\"content\":[{\"type\":\"text\",\"text\":\"RESULT: FAIL\"}],\"stopReason\":\"stop\"}}'\n"
	pi := filepath.Join(t.TempDir(), "pi")
	if err := os.WriteFile(pi, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	verify := t.TempDir()
	env := []string{"GOCACHE=" + filepath.Join(t.TempDir(), "gocache"), "HOME=" + t.TempDir()}
	if _, err := RunProbe(context.Background(), ProbeConfig{Pi: []string{pi}, Env: env, Model: "ollama/fake", WorkDir: t.TempDir(), VerifyRoot: verify}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Second)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a setsid descendant survived the probe and wrote after it")
	}
	if left, _ := os.ReadDir(verify); len(left) != 0 {
		t.Fatalf("pristine copies left under the verify root: %v", left)
	}
}
