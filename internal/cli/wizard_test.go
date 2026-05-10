package cli

import (
	"bufio"
	"bytes"
	"strings"
	"testing"
)

func TestPromptModelSelectionEmptyKeepsOverrides(t *testing.T) {
	overrides := map[string]string{}
	r := bufio.NewReader(strings.NewReader("\n"))
	var out bytes.Buffer
	if err := promptModelSelection(r, &out, overrides); err != nil {
		t.Fatal(err)
	}
	if len(overrides) != 0 {
		t.Errorf("empty input should leave overrides untouched, got %v", overrides)
	}
}

func TestPromptModelSelectionParsesKV(t *testing.T) {
	overrides := map[string]string{}
	r := bufio.NewReader(strings.NewReader("cto=gpt-5,fullstack=sonnet\n"))
	var out bytes.Buffer
	if err := promptModelSelection(r, &out, overrides); err != nil {
		t.Fatal(err)
	}
	if overrides["cto"] != "gpt-5" || overrides["fullstack"] != "sonnet" {
		t.Errorf("unexpected overrides: %v", overrides)
	}
}

func TestPromptTrustSelectionEnterKeepsCurrent(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("\n"))
	var out bytes.Buffer
	got, err := promptTrustSelection(r, &out, trustModeSandboxed)
	if err != nil {
		t.Fatal(err)
	}
	if got != trustModeSandboxed {
		t.Errorf("Enter should keep sandboxed, got %q", got)
	}
}

func TestPromptTrustSelectionAcceptsTrusted(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("trusted\n"))
	var out bytes.Buffer
	got, err := promptTrustSelection(r, &out, trustModeSandboxed)
	if err != nil {
		t.Fatal(err)
	}
	if got != trustModeTrusted {
		t.Errorf("expected trusted, got %q", got)
	}
}

func TestPromptTrustSelectionRejectsUnknown(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("yolo\n"))
	var out bytes.Buffer
	if _, err := promptTrustSelection(r, &out, trustModeSandboxed); err == nil {
		t.Fatal("unknown trust mode should fail")
	}
}

func TestPromptWorkstreamSelectionEnterKeepsDefault(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("\n"))
	var out bytes.Buffer
	got, err := promptWorkstreamSelection(r, &out, "issue-96")
	if err != nil {
		t.Fatal(err)
	}
	if got != "issue-96" {
		t.Errorf("Enter should keep default, got %q", got)
	}
}

func TestPromptWorkstreamSelectionRejectsInvalid(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("Has Spaces\n"))
	var out bytes.Buffer
	if _, err := promptWorkstreamSelection(r, &out, "issue-96"); err == nil {
		t.Fatal("invalid workstream should fail")
	}
}
