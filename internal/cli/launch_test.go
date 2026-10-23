package cli

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureLauncherExecutableAcceptsSymlinkedExecutable(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "injector")
	if err := os.WriteFile(target, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "injector-link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unsupported in this environment: %v", err)
	}
	if err := ensureLauncherExecutable(link); err != nil {
		t.Fatalf("symlinked executable should validate: %v", err)
	}
}

func TestValidateSupportedAMQVersionUses0600Floor(t *testing.T) {
	for _, version := range []string{"0.60.0", "v0.60.0", "0.61.0-rc1"} {
		if err := validateSupportedAMQVersion(version); err != nil {
			t.Errorf("validateSupportedAMQVersion(%q): %v", version, err)
		}
	}
	for _, version := range []string{"0.52.2", "0.54.0", "0.57.3", "0.59.1", "0.60.0-rc1"} {
		err := validateSupportedAMQVersion(version)
		if err == nil || !strings.Contains(err.Error(), "older than required 0.60.0") || !strings.Contains(err.Error(), "amq upgrade") {
			t.Errorf("validateSupportedAMQVersion(%q) = %v, want actionable floor rejection", version, err)
		}
	}
}

func TestEnsureLauncherExecutable(t *testing.T) {
	dir := t.TempDir()

	missing := filepath.Join(dir, "nope.sh")
	if err := ensureLauncherExecutable(missing); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("missing launcher: want 'not found' error, got %v", err)
	}

	notExec := filepath.Join(dir, "plain.sh")
	if err := os.WriteFile(notExec, []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ensureLauncherExecutable(notExec); err == nil || !strings.Contains(err.Error(), "not executable") {
		t.Errorf("non-executable launcher: want 'not executable' error, got %v", err)
	}

	if err := ensureLauncherExecutable(dir); err == nil || !strings.Contains(err.Error(), "directory") {
		t.Errorf("directory launcher: want 'directory' error, got %v", err)
	}

	okExec := filepath.Join(dir, "good.sh")
	if err := os.WriteFile(okExec, []byte("#!/bin/sh\nexec claude \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ensureLauncherExecutable(okExec); err != nil {
		t.Errorf("executable launcher: want nil, got %v", err)
	}
}

func captureOutput(t *testing.T, fn func() error) (string, string, error) {
	t.Helper()
	oldStdout := os.Stdout
	oldStderr := os.Stderr
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	os.Stdout = stdoutW
	os.Stderr = stderrW
	type readResult struct {
		data []byte
		err  error
	}
	stdoutCh := make(chan readResult, 1)
	stderrCh := make(chan readResult, 1)
	go func() {
		data, err := io.ReadAll(stdoutR)
		stdoutCh <- readResult{data: data, err: err}
	}()
	go func() {
		data, err := io.ReadAll(stderrR)
		stderrCh <- readResult{data: data, err: err}
	}()
	runErr := fn()
	if err := stdoutW.Close(); err != nil {
		t.Fatal(err)
	}
	if err := stderrW.Close(); err != nil {
		t.Fatal(err)
	}
	os.Stdout = oldStdout
	os.Stderr = oldStderr

	stdout := <-stdoutCh
	if stdout.err != nil {
		t.Fatal(stdout.err)
	}
	stderr := <-stderrCh
	if stderr.err != nil {
		t.Fatal(stderr.err)
	}
	return string(stdout.data), string(stderr.data), runErr
}
