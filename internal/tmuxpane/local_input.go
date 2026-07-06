package tmuxpane

import (
	"regexp"
	"strings"
)

const localInputTailLines = 12

type LocalInputBlocker struct {
	Kind        string
	Summary     string
	Destructive bool
	Recovery    string
}

type localInputMarker struct {
	name    string
	kind    string
	pattern *regexp.Regexp
}

// localInputPromptMarkers is the single marker list for local approval/input
// prompts. Keep additions tight and prompt-specific; broad TUI words turn this
// best-effort observability hint into noisy pane scraping.
var localInputPromptMarkers = []localInputMarker{
	{name: "permission-rule-confirmation", kind: "approval_prompt", pattern: regexp.MustCompile(`(?i)\bpermission rule\b.*\brequires confirmation\b`)},
	{name: "requires-confirmation", kind: "approval_prompt", pattern: regexp.MustCompile(`(?i)\brequires confirmation\b`)},
	{name: "allow-question", kind: "approval_prompt", pattern: regexp.MustCompile(`(?i)\bdo you want to (allow|approve|proceed|continue)\b`)},
}

// localInputRiskMarkers drives the destructive/risky hint from the detected
// prompt content, not from the marker name. Keep it conservative: a miss only
// removes the hint, while a false positive changes recovery text.
var localInputRiskMarkers = []localInputMarker{
	{name: "recursive-or-forced-rm", kind: "destructive_command", pattern: regexp.MustCompile(`(?i)\brm\s+-[^\s)]*[rf][^\s)]*`)},
	{name: "git-reset-hard", kind: "destructive_command", pattern: regexp.MustCompile(`(?i)\bgit\s+reset\s+--hard\b`)},
	{name: "git-clean-force", kind: "destructive_command", pattern: regexp.MustCompile(`(?i)\bgit\s+clean\s+-[^\s)]*[fdx][^\s)]*`)},
	{name: "recursive-chmod", kind: "risky_command", pattern: regexp.MustCompile(`(?i)\bchmod\s+-R\b`)},
	{name: "recursive-chown", kind: "risky_command", pattern: regexp.MustCompile(`(?i)\bchown\s+-R\b`)},
}

// DetectLocalInputBlocker reports whether paneID's live tail appears to be
// stopped at a local approval/input prompt. Capture failure is returned to the
// caller so status surfaces can degrade to "unknown" without treating absence
// of a signal as proof that the pane is not blocked.
func DetectLocalInputBlocker(paneID string) (LocalInputBlocker, bool, error) {
	out, err := paneCapturer(paneID)
	if err != nil {
		return LocalInputBlocker{}, false, err
	}
	blocker, ok := AnalyzeLocalInputBlocker(out)
	return blocker, ok, nil
}

// AnalyzeLocalInputBlocker is a read-only pane-tail blind-spot detection
// heuristic for local approval/input prompts; it is explicitly not a
// coordination or progress primitive. It inspects only the live tail so a prompt
// mentioned in scrollback does not become a blocker signal.
func AnalyzeLocalInputBlocker(capture string) (LocalInputBlocker, bool) {
	tail := tailLines(capture, localInputTailLines)
	if strings.TrimSpace(tail) == "" {
		return LocalInputBlocker{}, false
	}
	line, kind, ok := matchingLocalInputLine(tail)
	if !ok {
		return LocalInputBlocker{}, false
	}
	summary := summarizeLocalInputLine(line)
	destructive := localInputRisky(tail)
	return LocalInputBlocker{
		Kind:        kind,
		Summary:     summary,
		Destructive: destructive,
		Recovery:    localInputRecovery(destructive),
	}, true
}

func matchingLocalInputLine(tail string) (string, string, bool) {
	lines := strings.Split(tail, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		for _, marker := range localInputPromptMarkers {
			if marker.pattern.MatchString(line) {
				return line, marker.kind, true
			}
		}
	}
	return "", "", false
}

func localInputRisky(text string) bool {
	for _, marker := range localInputRiskMarkers {
		if marker.pattern.MatchString(text) {
			return true
		}
	}
	return false
}

func localInputRecovery(destructive bool) string {
	if destructive {
		return "operator decision required, or ask the worker to use a non-destructive alternative before proceeding"
	}
	return "operator decision required; inspect the worker pane or ask the worker to route the decision through an AMQ gate before proceeding"
}

func summarizeLocalInputLine(line string) string {
	summary := strings.Join(strings.Fields(line), " ")
	const maxLen = 180
	if len(summary) > maxLen {
		summary = strings.TrimSpace(summary[:maxLen-3]) + "..."
	}
	return summary
}
