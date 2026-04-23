package cli

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"

	"github.com/omriariav/amq-squad/internal/catalog"
	"github.com/omriariav/amq-squad/internal/team"
)

func runTeamOpen(args []string) error {
	fs := flag.NewFlagSet("team open", flag.ContinueOnError)
	layout := fs.String("layout", "vertical", "pane layout: vertical | horizontal")
	dryRun := fs.Bool("dry-run", false, "print the osascript without running it")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad team open - launch every team member in one iTerm2 window

Usage:
  amq-squad team open [--layout vertical|horizontal] [--dry-run]

Opens a new iTerm2 window, splits it into one pane per team member, and
pastes each launch command. Vertical layout stacks panes side-by-side
(divider is vertical); horizontal stacks them top-to-bottom. macOS + iTerm2
only.

--dry-run prints the generated osascript to stdout and exits.
`)
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	splitVerb, err := splitVerbFor(*layout)
	if err != nil {
		return err
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	if !team.Exists(cwd) {
		return fmt.Errorf("no team configured. Run 'amq-squad team init' first.")
	}
	t, err := team.Read(cwd)
	if err != nil {
		return fmt.Errorf("read team: %w", err)
	}
	if len(t.Members) == 0 {
		return fmt.Errorf("team has no members")
	}

	members := sortedMembers(t.Members)

	squadBin := "amq-squad"
	if p, err := os.Executable(); err == nil {
		squadBin = p
	}

	script := buildITermScript(t.Project, squadBin, members, splitVerb)

	if *dryRun {
		fmt.Println(script)
		return nil
	}

	if runtime.GOOS != "darwin" {
		return fmt.Errorf("team open requires macOS + iTerm2; on %s, use 'team show' and paste the commands yourself", runtime.GOOS)
	}

	cmd := exec.Command("osascript", "-")
	cmd.Stdin = strings.NewReader(script)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("osascript: %w", err)
	}
	fmt.Fprintf(os.Stderr, "Opened %d pane(s) in iTerm2 (%s layout).\n", len(members), *layout)
	return nil
}

// splitVerbFor maps the user-facing layout name to the AppleScript verb
// iTerm2 uses. In iTerm2's terminology: "split vertically" creates a pane
// to the right (divider is vertical); "split horizontally" creates a pane
// below (divider is horizontal).
//
// Accepts the short aliases "v" and "h" alongside the full names, matching
// the --help / README usage strings.
func splitVerbFor(layout string) (string, error) {
	switch strings.ToLower(layout) {
	case "vertical", "v":
		return "split vertically", nil
	case "horizontal", "h":
		return "split horizontally", nil
	default:
		return "", fmt.Errorf("unknown layout %q (want: vertical|v or horizontal|h)", layout)
	}
}

func sortedMembers(members []team.Member) []team.Member {
	idx := make(map[string]int, len(catalog.IDs()))
	for i, id := range catalog.IDs() {
		idx[id] = i
	}
	out := append([]team.Member(nil), members...)
	sort.SliceStable(out, func(i, j int) bool {
		return idx[out[i].Role] < idx[out[j].Role]
	})
	return out
}

// buildITermScript assembles the osascript that opens a new iTerm2 window,
// splits it one pane per member in catalog order, and writes each launch
// command into its pane.
func buildITermScript(projectDir, squadBin string, members []team.Member, splitVerb string) string {
	var b strings.Builder
	b.WriteString(`tell application "iTerm"` + "\n")
	b.WriteString(`  activate` + "\n")
	b.WriteString(`  set newWindow to (create window with default profile)` + "\n")
	b.WriteString(`  set pane1 to (current session of current tab of newWindow)` + "\n")

	for i, m := range members {
		cmd := emitTeamCommand(m.EffectiveCWD(projectDir), squadBin, m)
		paneVar := fmt.Sprintf("pane%d", i+1)
		if i > 0 {
			// iTerm2: `split ...` is a session command, not a tab command.
			// Split off the immediately preceding pane so new panes cascade
			// naturally rather than repeatedly subdividing pane1.
			prevPane := fmt.Sprintf("pane%d", i)
			fmt.Fprintf(&b, `  tell %s to set %s to (%s with default profile)`+"\n", prevPane, paneVar, splitVerb)
		}
		fmt.Fprintf(&b, `  tell %s to write text %s`+"\n", paneVar, applescriptString(cmd))
	}
	b.WriteString(`end tell` + "\n")
	return b.String()
}

// applescriptString renders a Go string as an AppleScript string literal.
// AppleScript accepts double-quoted strings with \\ and \" escapes, which
// is a subset of Go's %q encoding; the common cases (paths, single-quoted
// shell args, unicode text) round-trip correctly.
func applescriptString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
