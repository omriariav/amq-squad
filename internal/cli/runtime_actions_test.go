package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSameResolvedDir(t *testing.T) {
	real, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "lk")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if !sameResolvedDir(link, real) {
		t.Errorf("a symlink and its target must compare equal")
	}
	if sameResolvedDir(real, real+"-other") {
		t.Errorf("different dirs must not compare equal")
	}
	if sameResolvedDir("", real) {
		t.Errorf("empty path must not match")
	}
}

func TestReadPromptBody(t *testing.T) {
	// --body wins.
	got, err := readPromptBody("hello", "", true, false, strings.NewReader("ignored"), false)
	if err != nil || got != "hello" {
		t.Fatalf("--body: got %q err %v", got, err)
	}
	// --body-file - reads stdin even when interactive (explicit request).
	got, err = readPromptBody("", "-", false, true, strings.NewReader("from stdin\nline2"), true)
	if err != nil || got != "from stdin\nline2" {
		t.Fatalf("--body-file -: got %q err %v", got, err)
	}
	// bare stdin when neither flag set and stdin is piped (not a TTY).
	got, err = readPromptBody("", "", false, false, strings.NewReader("piped"), false)
	if err != nil || got != "piped" {
		t.Fatalf("stdin: got %q err %v", got, err)
	}
	// bare stdin on an interactive TTY -> usage error, never blocks.
	if _, err := readPromptBody("", "", false, false, strings.NewReader(""), true); err == nil {
		t.Fatal("interactive stdin with no body should be a usage error")
	} else if _, ok := err.(UsageError); !ok {
		t.Fatalf("want UsageError for interactive stdin, got %T", err)
	}
	// both flags -> usage error.
	if _, err := readPromptBody("x", "f", true, true, strings.NewReader(""), false); err == nil {
		t.Fatal("--body + --body-file should error")
	}
	// empty body -> error.
	if _, err := readPromptBody("   ", "", true, false, strings.NewReader(""), false); err == nil {
		t.Fatal("empty --body should error")
	}
	// empty stdin -> error.
	if _, err := readPromptBody("", "", false, false, strings.NewReader("  \n"), false); err == nil {
		t.Fatal("empty stdin should error")
	}
}
