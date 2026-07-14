package cli

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/tmuxpane"
)

type prunePanesExecution struct {
	ProjectDir      string
	Session         string
	ExplicitSession bool
	Yes             bool
	BaseRoot        string
	ResolveBaseRoot func(projectDir string) (string, error)
	PaneLister      tmuxpane.PaneLister
	Confirm         io.Reader
	Out             io.Writer
}

type orphanPane struct {
	PaneID  string
	Title   string
	Session string
	Role    string
}

func runPrunePanes(args []string) error {
	fs := flag.NewFlagSet("prune-panes", flag.ContinueOnError)
	sessionFlag := fs.String("session", "", "only prune orphan panes for this workstream session")
	yes := fs.Bool("yes", false, "skip the confirmation prompt (for automation)")
	fs.BoolVar(yes, "y", false, "shorthand for --yes")
	projectFlag := fs.String("project", "", "project/team-home directory to target (default: cwd)")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad prune-panes - reclaim orphaned amq-squad tmux panes

Usage:
  amq-squad prune-panes [--project DIR] [--session S] [--yes|-y]

Scans tmux for panes titled amq:<session>:<role>. A pane is considered orphaned
when no current launch record points at that exact title and pane id. By
default this command prints a preview and asks for confirmation. Declining makes
zero changes. Pass --session to limit the scan to one workstream, or omit it to
scan all amq-squad pane titles visible to tmux.

Every close is identity-checked immediately before kill-pane: the pane id must
still exist, still carry the same amq:<session>:<role> title, and still have no
current launch record. Reused pane ids and freshly adopted panes are left open.

Examples:
  amq-squad prune-panes --session issue-96
  amq-squad prune-panes --session issue-96 --yes
  amq-squad prune-panes --yes
`)
	}
	args = allowInterspersedFlags(fs, args)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return usageErrorf("prune-panes takes no positional arguments; got %d", fs.NArg())
	}
	ctx, err := resolveCanonicalContext(contextResolveOptions{
		ProjectFlag: *projectFlag, SessionFlag: *sessionFlag,
		ProjectExplicit: flagWasSet(fs, "project"), SessionExplicit: flagWasSet(fs, "session"),
	})
	if err != nil {
		return err
	}
	emitContextDiagnostics(ctx)
	return executePrunePanes(prunePanesExecution{
		ProjectDir:      ctx.ProjectDir,
		Session:         strings.TrimSpace(*sessionFlag),
		ExplicitSession: flagWasSet(fs, "session"),
		Yes:             *yes,
		BaseRoot:        ctx.BaseRoot,
		PaneLister:      statusPaneLister,
		Confirm:         os.Stdin,
		Out:             os.Stdout,
	})
}

func executePrunePanes(e prunePanesExecution) error {
	out := e.Out
	if out == nil {
		out = os.Stdout
	}
	session := strings.TrimSpace(e.Session)
	if e.ExplicitSession {
		if err := validateWorkstreamName(session); err != nil {
			return err
		}
	}
	resolve := e.ResolveBaseRoot
	if resolve == nil {
		resolve = scanBaseRootForProject
	}
	baseRoot := e.BaseRoot
	if strings.TrimSpace(baseRoot) == "" {
		var err error
		baseRoot, err = resolve(e.ProjectDir)
		if err != nil {
			return fmt.Errorf("resolve AMQ base root: %w", err)
		}
	}
	baseRoot = filepath.Clean(baseRoot)

	lister := e.PaneLister
	if lister == nil {
		lister = statusPaneLister
	}
	panes, err := lister()
	if err != nil {
		if tmuxpane.IsPermissionDenied(err) {
			return errTmuxAccessDenied()
		}
		return fmt.Errorf("list tmux panes: %w", err)
	}
	records, err := liveLaunchPaneTokens(e.ProjectDir, baseRoot)
	if err != nil {
		return err
	}
	orphans := findOrphanPanes(panes, records, session)
	renderPrunePanesPreview(out, session, orphans)
	if len(orphans) == 0 {
		return nil
	}
	if !e.Yes {
		if !confirmPrunePanes(out, e.Confirm) {
			fmt.Fprintln(out, "prune-panes: aborted; no panes closed.")
			return nil
		}
	}
	for _, p := range orphans {
		closed, skip := closeOrphanPaneSafely(p, e.ProjectDir, baseRoot)
		if closed {
			fmt.Fprintf(out, "closed tmux pane %s (%s)\n", p.PaneID, p.Title)
		} else if skip != "" {
			fmt.Fprintf(out, "left tmux pane open: %s\n", skip)
		}
	}
	return nil
}

func liveLaunchPaneTokens(projectDir, baseRoot string) (map[string]bool, error) {
	entries, err := launch.ScanEntriesInRoot(projectDir, baseRoot)
	if err != nil {
		return nil, fmt.Errorf("scan launch records: %w", err)
	}
	out := map[string]bool{}
	for _, e := range entries {
		if e.Record.Tmux == nil {
			continue
		}
		paneID := strings.TrimSpace(e.Record.Tmux.PaneID)
		session := strings.TrimSpace(e.Record.Session)
		role := strings.TrimSpace(e.Record.Role)
		if role == "" {
			role = strings.TrimSpace(e.Record.Handle)
		}
		if paneID == "" || session == "" || role == "" {
			continue
		}
		out[launchPaneKey(paneID, paneTitleToken(session, role))] = true
	}
	return out, nil
}

func findOrphanPanes(panes []tmuxpane.TmuxPane, liveRecords map[string]bool, sessionFilter string) []orphanPane {
	var out []orphanPane
	for _, p := range panes {
		session, role, ok := parsePaneTitleToken(p.Title)
		if !ok {
			continue
		}
		if sessionFilter != "" && session != sessionFilter {
			continue
		}
		paneID := strings.TrimSpace(p.PaneID)
		if paneID == "" {
			continue
		}
		if liveRecords[launchPaneKey(paneID, p.Title)] {
			continue
		}
		out = append(out, orphanPane{
			PaneID:  paneID,
			Title:   p.Title,
			Session: session,
			Role:    role,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Session != out[j].Session {
			return out[i].Session < out[j].Session
		}
		if out[i].Role != out[j].Role {
			return out[i].Role < out[j].Role
		}
		return out[i].PaneID < out[j].PaneID
	})
	return out
}

func launchPaneKey(paneID, title string) string {
	return strings.TrimSpace(paneID) + "\x00" + strings.TrimSpace(title)
}

func parsePaneTitleToken(title string) (session, role string, ok bool) {
	parts := strings.Split(strings.TrimSpace(title), ":")
	if len(parts) != 3 || parts[0] != "amq" || parts[1] == "" || parts[2] == "" {
		return "", "", false
	}
	return parts[1], parts[2], true
}

func renderPrunePanesPreview(out io.Writer, session string, panes []orphanPane) {
	fmt.Fprintln(out, "# amq-squad prune-panes — preview")
	if strings.TrimSpace(session) != "" {
		fmt.Fprintf(out, "# session:  %s\n", session)
	} else {
		fmt.Fprintln(out, "# session:  all")
	}
	fmt.Fprintf(out, "# orphan panes: %d\n\n", len(panes))
	if len(panes) == 0 {
		fmt.Fprintln(out, "No orphan amq-squad panes found.")
		return
	}
	for _, p := range panes {
		fmt.Fprintf(out, "  CLOSE  %s  %s\n", p.PaneID, p.Title)
	}
}

func confirmPrunePanes(out io.Writer, r io.Reader) bool {
	if r == nil {
		r = os.Stdin
	}
	fmt.Fprint(out, "Close orphan tmux panes? [y/N] ")
	scanner := bufio.NewScanner(r)
	if !scanner.Scan() {
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(scanner.Text()))
	return answer == "y" || answer == "yes"
}

func closeOrphanPaneSafely(p orphanPane, projectDir, baseRoot string) (closed bool, skip string) {
	live, ok := statusPaneInspector(p.PaneID)
	if !ok {
		return false, ""
	}
	if live.Title != p.Title {
		return false, fmt.Sprintf("pane %s title changed from %q to %q; left open", p.PaneID, p.Title, live.Title)
	}
	records, err := liveLaunchPaneTokens(projectDir, baseRoot)
	if err != nil {
		return false, fmt.Sprintf("pane %s launch-record recheck failed: %v; left open", p.PaneID, err)
	}
	if records[launchPaneKey(p.PaneID, p.Title)] {
		return false, fmt.Sprintf("pane %s now has a live launch record for %s; left open", p.PaneID, p.Title)
	}
	if err := paneCloser(p.PaneID); err != nil {
		return false, fmt.Sprintf("pane %s close failed: %v; left open", p.PaneID, err)
	}
	return true, ""
}
