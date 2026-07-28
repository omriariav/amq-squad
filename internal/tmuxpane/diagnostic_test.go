package tmuxpane

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
)

// #577 r10, STEPS 1 AND 3 TOGETHER.
//
// Step 1 put the tmux stderr into the Unavailable detail so the failure could be OBSERVED instead
// of guessed at. Step 3 acts on what was observed: "no server running" becomes proven-Gone.
//
// The file keeps both because they are one argument. The diagnostic is what made the
// classification change evidence-based rather than plausible, and the classification pins are what
// keep the change narrow. Read the anti-overbroad set below as the load-bearing half.

// exitErrorWithStderr builds an *exec.ExitError carrying stderr, which is the ONLY place tmux's
// actual message lives for a failed capture. err.Error() is just "exit status N".
func exitErrorWithStderr(t *testing.T, stderr string) error {
	t.Helper()
	// `false` exits 1 without output; we attach the stderr the way exec.Cmd.Output does.
	cmd := exec.Command("false")
	runErr := cmd.Run()
	var ee *exec.ExitError
	if !errors.As(runErr, &ee) {
		t.Fatalf("fixture: expected an ExitError from `false`, got %v", runErr)
	}
	ee.Stderr = []byte(stderr)
	return ee
}

// THE FALSIFIER FOR THE WHOLE ROUND: an unrecognized stderr must reach the detail. If it does not,
// the next CI run reports "exit status 1" again and r10 step 3 stays a guess.
func TestUnavailableDetailCarriesTheUnrecognizedStderr(t *testing.T) {
	// Deliberately an UNRECOGNISED stderr. This used the no-server message until step 3 made that
	// string proven-Gone -- at which point this test would have been asserting Unavailable about an
	// input that is no longer Unavailable, and would have failed for a reason that has nothing to
	// do with what it tests. A test's fixture has to keep meaning what the test is about.
	const stderr = "server exited unexpectedly: some unrecognised tmux condition"
	restore := swapCapture(t, "", exitErrorWithStderr(t, stderr))
	_ = restore

	result := InspectPaneExactByID("%2")

	if result.State != PaneInspectionUnavailable {
		t.Fatalf("state = %q, want unavailable: this test is about the DETAIL of an unavailable "+
			"result, and a different state means the classification changed under it", result.State)
	}
	if !strings.Contains(result.Detail, stderr) {
		t.Errorf("the detail must carry the tmux stderr that explains the failure.\n"+
			"Without it the operator, the CI log and the reviewer all see THAT classification fell "+
			"through to unavailable and never see what it fell through ON -- which is exactly why "+
			"#577 r10's cause could only be hypothesised.\ngot: %q\nwant it to contain: %q",
			result.Detail, stderr)
	}
	// The bare exec text must remain too: dropping it would lose the exit status.
	if !strings.Contains(result.Detail, "exit status") {
		t.Errorf("the detail must keep the exec error alongside the stderr; got %q", result.Detail)
	}
}

// SANITIZATION, both halves. A detail that reaches operator output must not break the report it
// lands in, and must not be able to flood a log.
func TestUnavailableDetailSanitizesTheStderr(t *testing.T) {
	t.Run("collapses newlines and tabs to one line", func(t *testing.T) {
		restore := swapCapture(t, "", exitErrorWithStderr(t, "line one\nline two\tand three\n\n"))
		_ = restore

		detail := InspectPaneExactByID("%2").Detail

		if strings.ContainsAny(detail, "\n\t") {
			t.Errorf("the detail must be a single line: it is embedded in structured operator "+
				"reports and a raw multi-line tmux message would break them.\ngot: %q", detail)
		}
		for _, want := range []string{"line one", "line two", "and three"} {
			if !strings.Contains(detail, want) {
				t.Errorf("collapsing must preserve content; %q missing from %q", want, detail)
			}
		}
	})

	t.Run("bounds a runaway", func(t *testing.T) {
		restore := swapCapture(t, "", exitErrorWithStderr(t, strings.Repeat("x", 5000)))
		_ = restore

		detail := InspectPaneExactByID("%2").Detail

		if len(detail) > tmuxErrorDetailLimit+128 {
			t.Errorf("a runaway stderr must be bounded; detail length %d", len(detail))
		}
		if !strings.Contains(detail, "truncated") {
			t.Errorf("truncation must be VISIBLE, or a reader cannot tell a bounded message from a "+
				"complete one and may conclude tmux said nothing more; got %q", detail)
		}
	})
}

// THE ANTI-OVERBROAD PIN SET. Three of these four rows assert what does NOT change, and they are
// what stops the step-3 marker from becoming a broad exit-1 => Gone.
//
// This matters more than the flip itself. A broad mapping would satisfy BOTH headline properties --
// cleanup passes, launch refuses -- while misclassifying genuinely unsafe failures as proven
// absence. The cleanup-pass/launch-refuse pair alone is therefore insufficient evidence, and the
// rows that must hold are the ones saying a permission denial and an unrecognised transient are
// STILL Unavailable.
//
// In step 1 this set also pinned no-server AS Unavailable, to stop the fix arriving before the
// observation that justified it. That row has now flipped deliberately; the other three have not
// moved, and they are the reason the flip is narrow rather than a widening.
func TestClassificationChangesOnlyForTheObservedNoServerCase(t *testing.T) {
	for _, tc := range []struct {
		name   string
		stderr string
		want   PaneInspectionState
		why    string
	}{
		{
			// THE DELIBERATE FLIP. In step 1 this row asserted Unavailable, because narrowing the
			// marker was step 3 and must not have arrived early and unobserved. Step 2's diagnostic
			// then put the real stderr in the wake-lane log -- "no server running on
			// /private/tmp/amq-wake-tmux-.../tmux-501/default" -- confirming the hypothesis by
			// OBSERVATION. So this row flips, and the flip IS the fix. It is the one expected value
			// in this file that changes, changed deliberately and named in the commit rather than
			// quietly edited. The pin did its job: it made the change impossible to slip in early.
			name: "no server running is PROVEN GONE (observed in the wake lanes)", stderr: "no server running on /tmp/tmux-501/default",
			want: PaneInspectionGone,
			why:  "a server that is not running cannot be hiding a pane; there is no tmux left for it to exist in",
		},
		{
			name: "generic transient stays unavailable", stderr: "error connecting to /tmp/tmux-501/default (No such file or directory)",
			want: PaneInspectionUnavailable,
			why:  "an unrecognized failure is unproven, never proven-absent",
		},
		{
			name: "permission denial stays unavailable", stderr: "error connecting to /tmp/tmux-501/default (Operation not permitted)",
			want: PaneInspectionUnavailable,
			why:  "a sandboxed agent cannot see the pane; that is not evidence the pane is gone",
		},
		// ONE ROW PER ORIGINAL MARKER. dev-2's G3: the guard recognizes THREE independent strings,
		// and a single representative row cannot support the claim that all three keep working --
		// while a mutation deleting the whole loop would be "killed" by any one of them. That is the
		// single-defect-per-row rule I argued for in r9, applied to my own coverage claim.
		{
			name: "original marker: can't find pane", stderr: "can't find pane: %2",
			want: PaneInspectionGone,
			why:  "tmux is running and says this pane does not exist in it",
		},
		{
			name: "original marker: no such pane", stderr: "no such pane: %2",
			want: PaneInspectionGone,
			why:  "an independent no-target phrasing; deleting it must fail a row of its own",
		},
		{
			name: "original marker: unknown pane", stderr: "unknown pane: %2",
			want: PaneInspectionGone,
			why:  "the third independent no-target phrasing, likewise separately pinned",
		},
		{
			// THE NEAR-MISS THAT MUST NOT BE RECOGNIZED. This is the string the old comment called a
			// socket/server failure, and it is one word away from the no-server marker while meaning
			// something different: we could not REACH a server, not that none is running. If the
			// marker match ever widens to substring-of-socket-error, this row is what catches it.
			name: "socket connect failure is NOT proven absence", stderr: "error connecting to /tmp/tmux-501/default (No such file or directory)",
			want: PaneInspectionUnavailable,
			why:  "failing to reach a server is not evidence that no server is running",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			restore := swapCapture(t, "", exitErrorWithStderr(t, tc.stderr))
			_ = restore

			got := InspectPaneExactByID("%2").State

			if got != tc.want {
				t.Errorf("state = %q, want %q.\n%s\nExactly ONE input may map to Gone by this change. "+
					"A broader mapping satisfies both headline properties -- cleanup passes, launch "+
					"refuses -- while misclassifying genuinely unsafe failures as proven absence, and "+
					"the same green CI would confirm it.", got, tc.want, tc.why)
			}
		})
	}
}
