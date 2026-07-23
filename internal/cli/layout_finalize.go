package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

const layoutFinalizationWaitTicks = 200

type layoutFinalizationContext struct {
	LauncherPaneID   string
	LauncherWindowID string
}

type layoutFinalizationPlan struct {
	Selection      runStartLayoutSelection
	ParentPID      int
	LauncherPaneID string
	LeadPaneID     string
	LeadWindowID   string
	WarningPath    string
	// ControlRoot is the project root used to build the run-shell log path
	// the scheduled script's output is silenced into (#525).
	ControlRoot string
}

var (
	layoutFinalizationRunCommand = runCommand
	// layoutFinalizationScheduler schedules the finalization script via tmux
	// run-shell -b, wrapped so it can never display output or a
	// nonzero-exit overlay on the lead pane (#525): see
	// silentRunShellPayload.
	layoutFinalizationScheduler = func(controlRoot, script string) error {
		return layoutFinalizationRunCommand("tmux", "run-shell", "-b", silentRunShellPayload(controlRoot, script))
	}
	layoutFinalizationParentPID = os.Getpid
)

func prepareLayoutFinalization(selection runStartLayoutSelection) (layoutFinalizationContext, error) {
	if !selection.requestedFinalization() {
		return layoutFinalizationContext{}, nil
	}
	paneID, paneErr := exactTmuxPaneID(os.Getenv("TMUX_PANE"))
	if paneErr != nil {
		return layoutFinalizationContext{}, fmt.Errorf("layout finalization requires an exact launcher TMUX_PANE id before spawn")
	}
	id, err := currentPaneIdentity()
	if err != nil {
		return layoutFinalizationContext{}, fmt.Errorf("resolve exact launcher identity before spawn: %w", err)
	}
	if id == nil {
		return layoutFinalizationContext{}, fmt.Errorf("layout finalization requires exact launcher pane and window ids before spawn")
	}
	resolvedPaneID, paneErr := exactTmuxPaneID(id.PaneID)
	resolvedWindowID, windowErr := exactTmuxWindowID(id.WindowID)
	if paneErr != nil || windowErr != nil {
		return layoutFinalizationContext{}, fmt.Errorf("layout finalization requires exact launcher pane and window ids before spawn")
	}
	if resolvedPaneID != paneID {
		return layoutFinalizationContext{}, fmt.Errorf("launcher identity mismatch: TMUX_PANE=%s, resolved pane=%s", paneID, id.PaneID)
	}
	return layoutFinalizationContext{LauncherPaneID: paneID, LauncherWindowID: resolvedWindowID}, nil
}

func buildLayoutFinalizationPlan(project, profile, session, lead string, selection runStartLayoutSelection, ctx layoutFinalizationContext, result teamLaunchResult, externalLead bool) (layoutFinalizationPlan, error) {
	if !selection.requestedFinalization() {
		return layoutFinalizationPlan{}, nil
	}
	launcherPaneID, paneErr := exactTmuxPaneID(ctx.LauncherPaneID)
	launcherWindowID, windowErr := exactTmuxWindowID(ctx.LauncherWindowID)
	if paneErr != nil || windowErr != nil {
		return layoutFinalizationPlan{}, fmt.Errorf("layout finalization requires exact launcher pane/window ids: pane=%q window=%q", ctx.LauncherPaneID, ctx.LauncherWindowID)
	}
	for _, pane := range result.Panes {
		if _, err := exactTmuxPaneID(pane.PaneID); err != nil {
			return layoutFinalizationPlan{}, fmt.Errorf("layout finalization result for role %q: %w", pane.Role, err)
		}
		if _, err := exactTmuxWindowID(pane.WindowID); err != nil {
			return layoutFinalizationPlan{}, fmt.Errorf("layout finalization result for role %q: %w", pane.Role, err)
		}
	}
	plan := layoutFinalizationPlan{
		Selection: selection, ParentPID: layoutFinalizationParentPID(), LauncherPaneID: launcherPaneID,
		WarningPath: layoutFinalizationWarningPath(project, profile, session),
		ControlRoot: project,
	}
	if externalLead {
		plan.LeadPaneID = launcherPaneID
		plan.LeadWindowID = launcherWindowID
	} else {
		for _, pane := range result.Panes {
			if strings.EqualFold(strings.TrimSpace(pane.Role), strings.TrimSpace(lead)) {
				if plan.LeadPaneID != "" {
					return layoutFinalizationPlan{}, fmt.Errorf("layout finalization found multiple runtime panes for configured lead %q", lead)
				}
				plan.LeadPaneID, _ = exactTmuxPaneID(pane.PaneID)
				plan.LeadWindowID, _ = exactTmuxWindowID(pane.WindowID)
			}
		}
	}
	if plan.LeadPaneID == "" || plan.LeadWindowID == "" {
		return layoutFinalizationPlan{}, fmt.Errorf("layout finalization missing exact pane/window ids for configured lead %q", lead)
	}
	return plan, nil
}

func scheduleLayoutFinalization(plan layoutFinalizationPlan) error {
	if plan.LeadPaneID == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(plan.WarningPath), 0o755); err != nil {
		return err
	}
	_ = os.Remove(plan.WarningPath)
	script := layoutFinalizationScript(plan)
	if err := layoutFinalizationScheduler(plan.ControlRoot, script); err != nil {
		warning := fmt.Sprintf("schedule layout finalization: %v", err)
		_ = os.WriteFile(plan.WarningPath, []byte(warning+"\n"), 0o644)
		return err
	}
	return nil
}

func warnLayoutFinalization(project, profile, session string, err error) {
	if err == nil {
		return
	}
	path := layoutFinalizationWarningPath(project, profile, session)
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = os.WriteFile(path, []byte(err.Error()+"\n"), 0o644)
	quietNotice("warning: layout finalization skipped: %v; launched agents remain running (status will retain this warning).\n", err)
}

func layoutFinalizationScript(plan layoutFinalizationPlan) string {
	warningDir := filepath.Dir(plan.WarningPath)
	lines := []string{
		"fail() { mkdir -p " + shellQuote(warningDir) + "; printf '%s\\n' \"$1\" > " + shellQuote(plan.WarningPath) + "; exit 0; }",
		fmt.Sprintf("ticks=0; while kill -0 %d 2>/dev/null; do ticks=$((ticks+1)); [ \"$ticks\" -ge %d ] && fail 'layout finalization timed out waiting for parent CLI exit'; sleep 0.05; done", plan.ParentPID, layoutFinalizationWaitTicks),
		"lead_pane_probe=$(tmux display-message -p -t " + shellQuote(plan.LeadPaneID) + " " + shellQuote(tmuxRunShellFormat("#{pane_id}")) + " 2>/dev/null); [ \"$lead_pane_probe\" = " + shellQuote(plan.LeadPaneID) + " ] || fail 'layout finalization skipped: lead pane is missing'",
		"lead_window_probe=$(tmux display-message -p -t " + shellQuote(plan.LeadWindowID) + " " + shellQuote(tmuxRunShellFormat("#{window_id}")) + " 2>/dev/null); [ \"$lead_window_probe\" = " + shellQuote(plan.LeadWindowID) + " ] || fail 'layout finalization skipped: lead window is missing'",
	}
	if plan.Selection.LauncherPane == launcherPaneCloseAfterStart && plan.LauncherPaneID != "" && plan.LauncherPaneID != plan.LeadPaneID {
		lines = append(lines, "launcher_probe=$(tmux display-message -p -t "+shellQuote(plan.LauncherPaneID)+" "+shellQuote(tmuxRunShellFormat("#{pane_id}"))+" 2>/dev/null); if [ \"$launcher_probe\" = "+shellQuote(plan.LauncherPaneID)+" ]; then tmux kill-pane -t "+shellQuote(plan.LauncherPaneID)+" || fail 'layout finalization could not close launcher pane'; fi")
	}
	paneDimension := ""
	panePosition := ""
	if plan.Selection.MainPaneOption != "" {
		dimension := "window_width"
		paneDimension = "pane_width"
		panePosition = "pane_left"
		minimum, reserve := 20, 10
		if plan.Selection.MainPaneOption == "main-pane-height" {
			dimension, paneDimension, minimum, reserve = "window_height", "pane_height", 8, 4
			panePosition = "pane_top"
		}
		lines = append(lines,
			"total=$(tmux display-message -p -t "+shellQuote(plan.LeadWindowID)+" "+shellQuote(tmuxRunShellFormat("#{"+dimension+"}"))+"); case \"$total\" in ''|*[!0-9]*) fail 'layout finalization could not read window dimension';; esac",
			fmt.Sprintf("main_size=$((total*60/100)); [ \"$main_size\" -lt %d ] && main_size=%d; max_size=$((total-%d)); [ \"$max_size\" -lt 1 ] && max_size=1; [ \"$main_size\" -gt \"$max_size\" ] && main_size=$max_size", minimum, minimum, reserve),
			"tmux set-option -w -t "+shellQuote(plan.LeadWindowID)+" "+shellQuote(plan.Selection.MainPaneOption)+" \"$main_size\" || fail 'layout finalization could not set main pane dimension'",
		)
	}
	if plan.Selection.FinalLayout != "" {
		lines = append(lines, "tmux select-layout -t "+shellQuote(plan.LeadWindowID)+" "+shellQuote(plan.Selection.FinalLayout)+" || fail 'layout finalization could not apply layout'")
	}
	if plan.Selection.LeadMain {
		mainPaneFormat := tmuxRunShellFormat("#{pane_id} #{" + panePosition + "} #{" + paneDimension + "}")
		lines = append(lines,
			"main=$(tmux list-panes -t "+shellQuote(plan.LeadWindowID)+" -F "+shellQuote(mainPaneFormat)+" | awk -v size=\"$main_size\" '$2 == 0 && $3 == size { print $1; exit }'); [ -n \"$main\" ] || fail 'layout finalization could not resolve main pane by exact geometry'",
			"[ \"$main\" = "+shellQuote(plan.LeadPaneID)+" ] || tmux swap-pane -d -s "+shellQuote(plan.LeadPaneID)+" -t \"$main\" || fail 'layout finalization could not move lead into main pane'",
		)
	}
	if paneDimension != "" {
		lines = append(lines,
			"actual=$(tmux display-message -p -t "+shellQuote(plan.LeadPaneID)+" "+shellQuote(tmuxRunShellFormat("#{"+paneDimension+"}"))+"); case \"$actual\" in ''|*[!0-9]*) fail 'layout finalization could not read lead pane dimension';; esac",
			"[ \"$actual\" -eq \"$main_size\" ] || fail \"layout finalization lead pane dimension mismatch: expected $main_size, got $actual\"",
		)
	}
	lines = append(lines,
		"tmux select-window -t "+shellQuote(plan.LeadWindowID)+" || fail 'layout finalization could not focus lead window'",
		"tmux select-pane -t "+shellQuote(plan.LeadPaneID)+" || fail 'layout finalization could not focus lead pane'",
		"rm -f "+shellQuote(plan.WarningPath),
	)
	return strings.Join(lines, "; ")
}

// tmux run-shell expands formats before it starts the shell. Doubling the hash
// preserves one literal format token for the nested tmux client in that shell.
func tmuxRunShellFormat(format string) string {
	return strings.ReplaceAll(format, "#{", "##{")
}

func layoutFinalizationWarningPath(project, profile, session string) string {
	profile = strings.TrimSpace(profile)
	if profile == "" {
		profile = team.DefaultProfile
	}
	return filepath.Join(project, team.DirName, "layout-finalization", profile, session+".warning")
}

func printLayoutFinalizationDryRun(selection runStartLayoutSelection, launcherPaneID string) {
	if !selection.requestedFinalization() {
		return
	}
	fmt.Println("# layout-finalization (scheduled only after successful spawn, goal delivery, and final output)")
	launcherPaneID = strings.TrimSpace(launcherPaneID)
	if launcherPaneID == "" {
		launcherPaneID = "$TMUX_PANE"
	}
	fmt.Printf("# launcher_pane_id: %s\n", launcherPaneID)
	fmt.Println("# lead_pane_id: $lead_pane_id  # exact synchronous backend result for configured lead")
	fmt.Println("# lead_window_id: $lead_window_id  # exact synchronous backend result; names are never used")
	fmt.Printf("tmux run-shell -b 'wait up to %d ticks for $parent_cli_pid to exit; then apply the exact-ID commands below'\n", layoutFinalizationWaitTicks)
	if selection.LauncherPane == launcherPaneCloseAfterStart {
		fmt.Println("tmux kill-pane -t \"$launcher_pane_id\"  # idempotently skipped when missing or equal to lead")
	}
	if selection.FinalLayout != "" {
		fmt.Printf("tmux select-layout -t \"$lead_window_id\" %s\n", selection.FinalLayout)
	}
	fmt.Println("tmux select-pane -t \"$lead_pane_id\"")
}
