package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #577 finding 1 was a FOURTH production path that still typed its command, and finding 6
// asked that a fifth cannot hide. The pairing test in team_launch_pane_delivery_test.go
// structurally cannot catch either: it enumerates deliverPaneCommand sites, and a path that
// types is precisely a path that never calls it. An enumeration keyed on the CORRECT
// mechanism is blind to code using the wrong one.
//
// This enumerates the wrong mechanism instead. Every production `send-keys` must be named
// here with a reason it is exempt from the #571 fix. A new one fails until someone either
// converts it or justifies it, which is the check that would have caught finding 1.
func TestEveryTypedDeliverySiteIsJustified(t *testing.T) {
	// #577 round 2 finding 5: exemptions were keyed by FILENAME, so a second, unreviewed
	// send-keys added to orchestrate.go would ride the existing exemption silently. The key is
	// now the exact site -- an anchor comment that must sit on the send-keys line itself -- so an
	// exemption covers ONE call and cannot be inherited by a neighbour.
	//
	// Keying on an in-code anchor rather than a line number is deliberate: a line-number key
	// goes stale on every edit above it, and a stale key is how a real site gets waved through.
	const anchor = "#571-exempt-typed-delivery:"
	exempt := map[string]string{
		// exec must preserve the pane PID that beginGlobalNOCLaunch persisted, so respawn-pane
		// is wrong here. The tty boundary is avoided by keeping the payload in a file and
		// bounding the composed line (checkNOCDispatchLineBound), and the payload is digest-
		// verified before exec so a failed read cannot launch an agent without its contract.
		"noc-bootstrap-dispatch": "exec preserves the pane PID; payload is file-substituted, digest-guarded and length-bounded",
	}

	files, err := filepath.Glob(filepath.Join(".", "*.go"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}

	total := 0
	used := map[string]bool{}
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatalf("read %s: %v", path, readErr)
		}
		for i, line := range strings.Split(string(raw), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			if !strings.Contains(line, `"send-keys"`) {
				continue
			}
			total++
			site := ""
			if idx := strings.Index(line, anchor); idx >= 0 {
				site = strings.TrimSpace(line[idx+len(anchor):])
			}
			if site == "" {
				t.Errorf("%s:%d issues tmux send-keys with no exemption anchor.\n"+
					"send-keys delivers through the pane tty, which drops any line over MAX_CANON=1024 "+
					"and still reports success (#571). Either deliver the command as the pane process "+
					"(deliverPaneCommand), or add a trailing comment %s<site-key> and register that key "+
					"here with why typing is required and how the length boundary is bounded.",
					filepath.Base(path), i+1, anchor)
				continue
			}
			if _, ok := exempt[site]; !ok {
				t.Errorf("%s:%d claims exemption key %q, which is not registered in this test. "+
					"An unregistered key is an unreviewed exemption.", filepath.Base(path), i+1, site)
				continue
			}
			if used[site] {
				t.Errorf("%s:%d reuses exemption key %q, which is already claimed by another site. "+
					"One key per call, or a second site inherits the first's justification -- the exact "+
					"hole that filename-keyed exemptions had.", filepath.Base(path), i+1, site)
			}
			used[site] = true
		}
	}

	// Self-destructing exemptions: a key nothing claims is stale, and a stale key is how the
	// next real one gets waved through.
	for key := range exempt {
		if !used[key] {
			t.Errorf("exemption %q is stale -- no send-keys site claims it. Remove it, so the list "+
				"keeps meaning what it says.", key)
		}
	}

	// Anti-vacuity: this check is only meaningful while it can still SEE typed delivery.
	if total == 0 {
		t.Fatal("found no send-keys sites in production code; the scan is broken, not the tree clean")
	}
}

// The boundary itself, asserted rather than trusted: the NOC dispatch line must stay short no
// matter how large the bootstrap grows. #577 finding 1 was not "someone typed a long string",
// it was "the typed length is proportional to a payload that only grows".
func TestNOCDispatchLineDoesNotScaleWithBootstrapSize(t *testing.T) {
	dir := t.TempDir()
	huge := strings.Repeat("x", 64*1024)

	path, err := writeGlobalNOCBootstrapPayload(dir, "launch-1", huge)
	if err != nil {
		t.Fatalf("write payload: %v", err)
	}
	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read payload: %v", err)
	}
	if string(onDisk) != huge {
		t.Fatalf("payload must round-trip byte-identical: got %d bytes, want %d", len(onDisk), len(huge))
	}

	// PRODUCTION's construction, not a copy of it. Rebuilding the string here would prove the
	// technique works while leaving this green if production reverted to typing the payload --
	// so the argv is passed to the real function, bootstrap as the last element exactly as
	// appendGeneratedBootstrapPrompt leaves it.
	command := nocDispatchCommand([]string{"claude", "--model", "opus", "--", huge}, path, nocPromptDigest(huge))
	if len(command) > nocDispatchLineBound {
		t.Errorf("dispatch line is %d bytes for a 64KiB bootstrap, over the production bound %d; it must not scale with the payload:\n%s",
			len(command), nocDispatchLineBound, command)
	}
	// The guard must be PRESENT, or the line is short and still fails open (#577 finding 1).
	if !strings.Contains(command, "shasum") || !strings.Contains(command, nocPromptDigest(huge)) {
		t.Errorf("dispatch line must verify the payload digest before exec:\n%s", command)
	}
	if !strings.Contains(command, "exit 1") {
		t.Errorf("a failed verification must exit nonzero so the PID watch fails:\n%s", command)
	}
	// MAX_CANON on macOS/BSD. The margin matters: a line at 1000 bytes is one flag away from
	// silently truncating.
	if len(command) >= 1024 {
		t.Errorf("dispatch line is %d bytes, at or over the MAX_CANON=1024 tty boundary", len(command))
	}
	// Quoting is load-bearing: bare $(cat ...) word-splits the prompt into hundreds of args.
	if !strings.Contains(command, `"$(cat `) {
		t.Errorf("substitution must be double-quoted or the prompt is word-split: %s", command)
	}
}
