package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/omriariav/amq-squad/v3/internal/tmuxpane"
)

// sameResolvedDir reports whether two paths refer to the same directory after
// comparison canonicalization, so symlinked paths (e.g. macOS /var ->
// /private/var TMPDIR) and differently-cased APFS aliases both match tmux's
// resolved #{pane_current_path}. Falls back to a plain absolute comparison when
// a side cannot be resolved.
func sameResolvedDir(a, b string) bool {
	if strings.TrimSpace(a) == "" || strings.TrimSpace(b) == "" {
		return false
	}
	return resolveDir(a) == resolveDir(b)
}

func resolveDir(dir string) string {
	return canonicalFilesystemPath(dir)
}

// paneTitledForDifferentAgent reports whether a pane carries an amq title token
// (amq:<workstream>:<role>) for a role OTHER than the expected one — i.e. the
// recorded pane id was reused by a sibling agent (e.g. after a tmux server
// restart) in the same repo. Such a pane must not be trusted for the recorded
// agent even when its cwd matches, or `send` could deliver to the wrong agent.
// An untitled or clobbered (non-amq) title is not second-guessed: the recorded
// pane id + cwd match stand.
func paneTitledForDifferentAgent(title, workstream, role string) bool {
	title = strings.TrimSpace(title)
	if !strings.HasPrefix(title, "amq:") {
		return false
	}
	return title != paneTitleToken(workstream, role)
}

// paneIDForTarget returns the #{pane_id} of the live pane the resolver selected,
// matched by session+window+pane index. Empty when no live pane matches (e.g.
// older tmux output without ids).
func paneIDForTarget(tgt tmuxpane.TmuxTarget, panes []tmuxpane.TmuxPane) string {
	for _, p := range panes {
		if p.Session == tgt.Session && p.Window == tgt.Window && p.Pane == tgt.Pane {
			return p.PaneID
		}
	}
	return ""
}

// --- focus / open ---

// --- send ---

// readPromptBody resolves the prompt text from --body, --body-file (a path or
// "-" for stdin), or bare stdin when neither flag is set and stdin is piped.
// interactiveStdin reports whether stdin is a terminal; the bare-stdin path
// then returns a usage error instead of blocking forever waiting for EOF.
func readPromptBody(body, bodyFile string, bodySet, fileSet bool, stdin io.Reader, interactiveStdin bool) (string, error) {
	if bodySet && fileSet {
		return "", usageErrorf("use either --body or --body-file, not both")
	}
	if bodySet {
		if strings.TrimSpace(body) == "" {
			return "", usageErrorf("--body cannot be empty")
		}
		return body, nil
	}
	if fileSet {
		if bodyFile == "-" {
			return readAllPrompt(stdin)
		}
		b, err := os.ReadFile(bodyFile)
		if err != nil {
			return "", fmt.Errorf("read --body-file: %w", err)
		}
		if strings.TrimSpace(string(b)) == "" {
			return "", fmt.Errorf("--body-file %s is empty", bodyFile)
		}
		return string(b), nil
	}
	if interactiveStdin {
		return "", usageErrorf("no prompt provided; pass --body, --body-file FILE, or pipe text on stdin")
	}
	return readAllPrompt(stdin)
}

// stdinIsInteractive reports whether os.Stdin is a terminal (char device)
// rather than a pipe or file, so `send` can refuse to block on TTY input.
func stdinIsInteractive() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

func readAllPrompt(stdin io.Reader) (string, error) {
	b, err := io.ReadAll(stdin)
	if err != nil {
		return "", fmt.Errorf("read prompt from stdin: %w", err)
	}
	if strings.TrimSpace(string(b)) == "" {
		return "", usageErrorf("no prompt provided; pass --body, --body-file, or pipe text on stdin")
	}
	return string(b), nil
}

// resolveProjectProfile resolves the --project and --profile flags shared by the
// runtime control verbs.
func resolveProjectProfile(projectFlag, profileFlag string, projectSet bool) (string, string, error) {
	ctx, err := resolveCanonicalContext(contextResolveOptions{
		ProjectFlag: projectFlag, ProfileFlag: profileFlag,
		ProjectExplicit: projectSet, ProfileExplicit: strings.TrimSpace(profileFlag) != "",
	})
	if err != nil {
		return "", "", err
	}
	emitContextDiagnostics(ctx)
	return ctx.ProjectDir, ctx.Profile, nil
}
