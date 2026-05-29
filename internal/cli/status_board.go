package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/omriariav/amq-squad/internal/state"
)

// boardState is the rolled-up run-state of a whole session, derived from its
// agents' computed liveness. It is TEXT-led on purpose (per the DX review):
// the literal token is the source of truth and color is layered on top, never
// the other way around.
//
//   - running:  at least one agent is alive and none are at risk.
//   - degraded: at least one agent is alive but another is dead-mailbox-live
//     (zombie heartbeat) or stale — the session is up but unhealthy.
//   - stopped:  no agent is alive (all dead / stale / missing).
type boardState string

const (
	boardStateRunning  boardState = "running"
	boardStateDegraded boardState = "degraded"
	boardStateStopped  boardState = "stopped"
)

// sessionsEnvelopeData is the kind="sessions" payload: the resolved base root
// plus one row per discovered session. This is a NEW envelope kind; the
// existing kind="status" (single-session --session detail) is unchanged and so
// is the global schema_version.
type sessionsEnvelopeData struct {
	BaseRoot string            `json:"base_root"`
	Sessions []sessionBoardRow `json:"sessions"`
	Notice   string            `json:"notice,omitempty"`
}

// sessionBoardRow is one session's board line in both the human table and the
// JSON envelope. AgentsTotal/AgentsAlive back the "N/M alive" health column;
// AtRisk flags the dead-mailbox-live (zombie heartbeat) case so an operator
// sees a live session that is actually unhealthy.
type sessionBoardRow struct {
	Name         string     `json:"name"`
	Root         string     `json:"root"`
	State        boardState `json:"state"`
	AgentsTotal  int        `json:"agents_total"`
	AgentsAlive  int        `json:"agents_alive"`
	AtRisk       int        `json:"at_risk"`
	Brief        string     `json:"brief,omitempty"`
	LastActivity time.Time  `json:"last_activity,omitempty"`
}

// statusBoardExecution carries the inputs for the multi-session board so tests
// can drive it with a seeded fixture, an injected state.Probe, and a captured
// writer — no real subprocess, no real wall clock.
type statusBoardExecution struct {
	ProjectDir string
	// BaseRoot, when set, is used verbatim and ResolveBaseRoot is NOT called.
	// Tests seed this directly; production leaves it empty and lets
	// ResolveBaseRoot shell out (best-effort) for the AMQ base root once.
	BaseRoot string
	// ResolveBaseRoot resolves the AMQ base root exactly once. Injected so the
	// front-door board degrades gracefully (and deterministically in tests)
	// when `amq` is missing or unresolvable. Defaults to scanBaseRootForProject.
	ResolveBaseRoot func(projectDir string) (string, error)
	Probe           state.Probe
	Now             func() time.Time
	Out             io.Writer
	JSON            bool
}

// runStatusBoard is the no-selector status entrypoint: a docker-ps / git
// branch-v style board over ALL discovered sessions. It is also the bare
// `amq-squad` default invocation, so it MUST NOT hard-error when `amq` is
// missing/unresolvable or there are no sessions: it resolves the base root
// best-effort and renders a clear, non-fatal guidance state instead.
func runStatusBoard(projectDir string, jsonOut bool) error {
	return executeStatusBoard(statusBoardExecution{
		ProjectDir: projectDir,
		Out:        os.Stdout,
		JSON:       jsonOut,
	})
}

func executeStatusBoard(s statusBoardExecution) error {
	resolve := s.ResolveBaseRoot
	if resolve == nil {
		resolve = scanBaseRootForProject
	}
	now := s.Now
	if now == nil {
		now = time.Now
	}

	baseRoot := s.BaseRoot
	var resolveErr error
	if baseRoot == "" {
		baseRoot, resolveErr = resolve(s.ProjectDir)
	}

	// GRACEFUL FRONT-DOOR DEGRADATION: the base root could not be resolved
	// (e.g. `amq` missing or not on PATH). This is the default invocation, so
	// render a non-fatal guidance state naming what was looked for and return
	// success — never crash the bare command.
	if resolveErr != nil || strings.TrimSpace(baseRoot) == "" {
		notice := boardUnresolvedNotice(resolveErr)
		if s.JSON {
			return writeJSONEnvelope(s.Out, "sessions", sessionsEnvelopeData{
				Sessions: []sessionBoardRow{},
				Notice:   notice,
			})
		}
		fmt.Fprintln(s.Out, notice)
		return nil
	}

	snap, buildErr := state.Build(s.ProjectDir, baseRoot, s.Probe)
	if buildErr != nil {
		// A scan failure (e.g. an unreadable base root) is still a front-door
		// invocation; degrade to guidance rather than a hard error.
		notice := fmt.Sprintf("amq-squad: could not scan AMQ base root %s: %v", baseRoot, buildErr)
		if s.JSON {
			return writeJSONEnvelope(s.Out, "sessions", sessionsEnvelopeData{
				BaseRoot: baseRoot,
				Sessions: []sessionBoardRow{},
				Notice:   notice,
			})
		}
		fmt.Fprintln(s.Out, notice)
		return nil
	}

	rows := make([]sessionBoardRow, 0, len(snap.Sessions))
	for _, sess := range snap.Sessions {
		rows = append(rows, boardRowFor(s.ProjectDir, sess))
	}

	if s.JSON {
		return writeJSONEnvelope(s.Out, "sessions", sessionsEnvelopeData{
			BaseRoot: snap.BaseRoot,
			Sessions: rows,
		})
	}
	return renderBoardTable(s.Out, snap.BaseRoot, rows, now())
}

// boardRowFor rolls one discovered session up into a board row: a state
// derived from agent liveness, an alive/total health count with an at-risk
// flag, the brief one-liner, and the most recent agent LastSeen.
func boardRowFor(projectDir string, sess state.Session) sessionBoardRow {
	row := sessionBoardRow{
		Name:        sess.Name,
		Root:        sess.Root,
		AgentsTotal: len(sess.Agents),
	}
	var latest time.Time
	for _, a := range sess.Agents {
		switch a.Liveness {
		case state.LivenessAlive:
			row.AgentsAlive++
		case state.LivenessDeadMailboxLive:
			row.AtRisk++
		}
		if a.LastSeen.After(latest) {
			latest = a.LastSeen
		}
	}
	row.LastActivity = latest
	row.State = rollupBoardState(row.AgentsAlive, row.AtRisk, row.AgentsTotal)
	row.Brief = briefOneLiner(projectDir, sess.Name)
	return row
}

// rollupBoardState maps an alive/at-risk/total triple to the session state.
// at-risk (dead-mailbox-live) outranks a clean running roll-up: a session with
// a zombie heartbeat is degraded even if some agents are genuinely alive.
func rollupBoardState(alive, atRisk, total int) boardState {
	switch {
	case atRisk > 0:
		return boardStateDegraded
	case alive == 0:
		return boardStateStopped
	case alive < total:
		// Some agents alive, some not (dead/stale/missing) — up but incomplete.
		return boardStateDegraded
	default:
		return boardStateRunning
	}
}

// briefOneLiner reads the first meaningful (non-blank, non-heading) line of the
// workstream brief for session, returning "" when there is no brief or no
// meaningful content. Headings ("# ...") and HTML comments are skipped so the
// stub template's "# <session>" title never becomes the one-liner.
func briefOneLiner(projectDir, session string) string {
	path := briefPath(projectDir, session)
	if path == "" {
		return ""
	}
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "<!--") {
			continue
		}
		return line
	}
	return ""
}

// boardUnresolvedNotice composes the guidance line shown when the AMQ base
// root cannot be resolved. It names what was looked for (the `amq` env probe)
// so the operator knows the failure is environmental, not a missing team.
func boardUnresolvedNotice(err error) string {
	if err != nil {
		return "amq-squad: could not resolve the AMQ base root via `amq env` " +
			"(is `amq` installed and on PATH?): " + err.Error()
	}
	return "amq-squad: the AMQ base root resolved empty; no sessions to show. " +
		"Run 'amq-squad up' to launch your team, or 'amq-squad doctor' to check setup."
}

// renderBoardTable writes the human-facing, TEXT-led session board. Columns:
// SESSION, STATE, AGENTS (N/M alive + at-risk note), BRIEF, LAST-ACTIVITY.
// State is the literal token first; color (when enabled) is layered on top so
// the table is never glyph- or color-dependent.
func renderBoardTable(out io.Writer, baseRoot string, rows []sessionBoardRow, now time.Time) error {
	if len(rows) == 0 {
		fmt.Fprintf(out, "amq-squad: no sessions found under %s.\n", baseRoot)
		fmt.Fprintln(out, "Run 'amq-squad up' to launch your team, or 'amq-squad doctor' to check setup.")
		return nil
	}
	// Stable order: by session name so the board is reproducible.
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })

	policy := outputPolicyCurrent()
	fmt.Fprintf(out, "# AM_BASE_ROOT: %s\n\n", baseRoot)
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "SESSION\tSTATE\tAGENTS\tBRIEF\tLAST-ACTIVITY")
	for _, r := range rows {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			boardSessionName(r.Name),
			colorBoardState(policy, r.State),
			boardAgentsCell(r),
			boardBriefCell(r.Brief),
			boardLastActivity(r.LastActivity, now),
		)
	}
	return w.Flush()
}

// boardSessionName renders the session name, substituting a visible token for
// the rootless ("") layout so the column is never blank.
func boardSessionName(name string) string {
	if strings.TrimSpace(name) == "" {
		return "(root)"
	}
	return name
}

// boardAgentsCell renders the agent-health column: "N/M alive" plus an explicit
// at-risk note when any agent is dead-mailbox-live (a zombie heartbeat behind a
// dead process). The text is stable; callers do not depend on color here.
func boardAgentsCell(r sessionBoardRow) string {
	cell := fmt.Sprintf("%d/%d alive", r.AgentsAlive, r.AgentsTotal)
	if r.AtRisk > 0 {
		cell += fmt.Sprintf(" (%d at-risk)", r.AtRisk)
	}
	return cell
}

// boardBriefCell renders the brief one-liner, substituting an em-dash for an
// absent brief and truncating overly long lines so the table stays scannable.
func boardBriefCell(brief string) string {
	brief = strings.TrimSpace(brief)
	if brief == "" {
		return "-"
	}
	const max = 60
	if len(brief) > max {
		return brief[:max-1] + "…"
	}
	return brief
}

// boardLastActivity renders an agent LastSeen as a relative age with freshness
// honesty: a zero time becomes "never" (no agent has reported), and a future
// timestamp (clock skew) is clamped to "just now" rather than a negative age.
func boardLastActivity(t, now time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := now.Sub(t)
	if d < time.Minute {
		// Sub-minute (or clock-skewed future) reads as "just now" with no
		// "ago" suffix, avoiding the awkward "just now ago".
		return "just now"
	}
	return relativeAge(d) + " ago"
}

// relativeAge renders a duration as a coarse, human relative age. It stays
// honest about granularity (seconds/minutes/hours/days) rather than
// over-precising stale data.
func relativeAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		m := int(d / time.Minute)
		return fmt.Sprintf("%dm", m)
	case d < 24*time.Hour:
		h := int(d / time.Hour)
		return fmt.Sprintf("%dh", h)
	default:
		days := int(d / (24 * time.Hour))
		return fmt.Sprintf("%dd", days)
	}
}

// colorBoardState returns the TEXT-led state token with color layered on when
// the policy permits. The literal token is always present; color is secondary.
func colorBoardState(policy outputPolicy, st boardState) string {
	switch st {
	case boardStateRunning:
		return colorize(policy, ansiGreen, string(st))
	case boardStateDegraded:
		return colorize(policy, ansiYellow, string(st))
	case boardStateStopped:
		return colorize(policy, ansiRed, string(st))
	default:
		return string(st)
	}
}
