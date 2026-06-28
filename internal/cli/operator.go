package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/state"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

type operatorExecution struct {
	ProjectDir      string
	Profile         string
	Session         string
	BaseRoot        string
	JSON            bool
	Out             io.Writer
	ResolveBaseRoot func(projectDir string) (string, error)
	Probe           state.Probe
	Now             func() time.Time
}

type operatorStatusEnvelopeData struct {
	ProjectDir       string               `json:"project_dir"`
	BaseRoot         string               `json:"base_root,omitempty"`
	Profile          string               `json:"profile"`
	Session          string               `json:"session"`
	Namespace        squadnamespace.Ref   `json:"namespace"`
	Operator         statusOperatorView   `json:"operator"`
	OperatorDelivery operatorDeliveryData `json:"operator_delivery"`
	OperatorLoop     operatorLoopStatus   `json:"operator_loop"`
	Attention        []operatorAttention  `json:"attention,omitempty"`
	OperatorGates    bool                 `json:"operator_gates"`
	Message          string               `json:"message,omitempty"`
}

type operatorLoopStatus struct {
	Mode              string `json:"mode"`
	PollRequired      bool   `json:"poll_required"`
	State             string `json:"state"`
	Owner             string `json:"owner"`
	OwnerID           string `json:"owner_id,omitempty"`
	LeaseExpiresAt    string `json:"lease_expires_at,omitempty"`
	LastPollAt        string `json:"last_poll_at,omitempty"`
	Cursor            string `json:"cursor,omitempty"`
	Backlog           int    `json:"backlog"`
	GatesOpen         int    `json:"gates_open"`
	DirectivesUnacked int    `json:"directives_unacked"`
	DegradedReason    string `json:"degraded_reason,omitempty"`
}

func runOperator(args []string) error {
	if len(args) == 0 {
		return usageErrorf("operator requires a subcommand (status)")
	}
	switch args[0] {
	case "status":
		return runOperatorStatus(args[1:])
	default:
		return usageErrorf("unknown 'operator' subcommand: %q. Try 'status'.", args[0])
	}
}

func runOperatorStatus(args []string) error {
	fs := flag.NewFlagSet("operator status", flag.ContinueOnError)
	projectFlag := fs.String("project", "", "project/team-home directory to inspect (default: cwd)")
	profileFlag := fs.String("profile", "", "team profile to inspect (default: default profile)")
	sessionFlag := fs.String("session", "", "AMQ workstream/session to inspect")
	jsonOut := fs.Bool("json", false, "emit a schema-versioned operator status envelope")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad operator status - show the operator polling contract

Usage:
  amq-squad operator status [--project DIR] [--profile NAME] [--session NAME] [--json]

Reports the canonical operator inbox, poll-required state, and read-only
operator attention counts for the resolved workstream. This command does not
claim a poll lease or move mailbox messages.
`)
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	projectDir, profile, err := resolveProjectProfile(*projectFlag, *profileFlag, flagWasSet(fs, "project"))
	if err != nil {
		return err
	}
	return executeOperatorStatus(operatorExecution{
		ProjectDir:      projectDir,
		Profile:         profile,
		Session:         *sessionFlag,
		JSON:            *jsonOut,
		Out:             os.Stdout,
		ResolveBaseRoot: scanBaseRootForProject,
		Probe:           state.DefaultProbe,
		Now:             time.Now,
	})
}

func executeOperatorStatus(o operatorExecution) error {
	out := o.Out
	if out == nil {
		out = os.Stdout
	}
	now := time.Now
	if o.Now != nil {
		now = o.Now
	}
	t, err := team.ReadProfile(o.ProjectDir, o.Profile)
	if err != nil {
		return fmt.Errorf("read team: %w", err)
	}
	workstream, err := resolveTeamWorkstreamName(t, o.Session, strings.TrimSpace(o.Session) != "")
	if err != nil {
		return err
	}
	ns := squadnamespace.Resolve(t.Project, o.Profile, workstream)
	operator := statusOperatorForTeam(t, ns)
	delivery := operatorDeliveryForTeam(t)
	data := operatorStatusEnvelopeData{
		ProjectDir:       t.Project,
		Profile:          squadnamespace.NormalizeProfile(o.Profile),
		Session:          workstream,
		Namespace:        ns,
		Operator:         operator,
		OperatorDelivery: delivery,
		OperatorGates:    team.SupportsOperatorGates(t),
	}
	if !team.SupportsOperatorGates(t) || !operator.Enabled {
		data.OperatorLoop = operatorLoopStatus{
			Mode:           "disabled",
			State:          "unconfigured",
			Owner:          "none",
			DegradedReason: "operator gates disabled for this profile",
		}
		data.Message = "operator gates disabled"
		return writeOrRenderOperatorStatus(out, data, o.JSON)
	}

	baseRoot := strings.TrimSpace(o.BaseRoot)
	if baseRoot == "" {
		resolve := o.ResolveBaseRoot
		if resolve == nil {
			resolve = scanBaseRootForProject
		}
		baseRoot, err = resolve(o.ProjectDir)
		if err != nil {
			return fmt.Errorf("resolve AMQ base root: %w", err)
		}
	}
	snap, err := state.BuildWithThresholds(o.ProjectDir, baseRoot, o.Probe, state.Thresholds{OperatorHandle: operator.Handle})
	if err != nil {
		return fmt.Errorf("scan AMQ base root: %w", err)
	}
	data.BaseRoot = snap.BaseRoot
	items := collectOperatorAttention(o.ProjectDir, o.Profile, snap, operator.Handle, workstream, now())
	session, sessionOK := operatorSessionSnapshot(snap, o.Profile, workstream)
	backlog := 0
	directivesUnacked := 0
	if sessionOK {
		backlog = operatorUnreadBacklog(session.Coordination.Threads, operator.Handle)
		directivesUnacked = operatorDirectivesUnacked(session.Coordination.Threads, operator.Handle, teamLeadHandle(t))
	}
	gatesOpen := operatorOpenGates(items)
	data.Attention = items
	data.OperatorLoop = operatorLoopStatus{
		Mode:              "poll",
		PollRequired:      delivery.PollRequired,
		State:             operatorLoopState(delivery.PollRequired),
		Owner:             "none",
		Backlog:           backlog,
		GatesOpen:         gatesOpen,
		DirectivesUnacked: directivesUnacked,
	}
	if data.Operator.Poll != nil {
		data.Operator.Poll.Unread = backlog
		data.Operator.Poll.OpenGates = gatesOpen
	}
	return writeOrRenderOperatorStatus(out, data, o.JSON)
}

func operatorSessionSnapshot(snap state.Snapshot, profile, session string) (state.Session, bool) {
	profile = squadnamespace.NormalizeProfile(profile)
	for _, candidate := range snap.Sessions {
		if candidate.Name == session && squadnamespace.ProfilesEqual(candidate.TeamProfile, profile) {
			return candidate, true
		}
	}
	return state.Session{}, false
}

func operatorUnreadBacklog(threads []state.ThreadSummary, operatorHandle string) int {
	count := 0
	for _, th := range threads {
		if notifyUnreadBy(th, operatorHandle) {
			count++
		}
	}
	return count
}

func operatorOpenGates(items []operatorAttention) int {
	count := 0
	for _, item := range items {
		if strings.HasPrefix(item.Thread, "gate/") {
			count++
		}
	}
	return count
}

func operatorDirectivesUnacked(threads []state.ThreadSummary, operatorHandle, leadHandle string) int {
	operatorHandle = strings.TrimSpace(operatorHandle)
	leadHandle = strings.TrimSpace(leadHandle)
	if operatorHandle == "" || leadHandle == "" {
		return 0
	}
	count := 0
	for _, th := range threads {
		if !strings.HasPrefix(strings.TrimSpace(th.Subject), "DIRECTIVE:") || !notifyUnreadBy(th, leadHandle) {
			continue
		}
		a, b, ok := parseP2PThread(th.ID)
		if !ok {
			continue
		}
		if (a == operatorHandle && b == leadHandle) || (a == leadHandle && b == operatorHandle) {
			count++
		}
	}
	return count
}

func operatorLoopState(pollRequired bool) string {
	if pollRequired {
		return "poll_required_unowned"
	}
	return "unconfigured"
}

func writeOrRenderOperatorStatus(out io.Writer, data operatorStatusEnvelopeData, jsonOut bool) error {
	if jsonOut {
		return writeJSONEnvelope(out, "operator_status", data)
	}
	inboxRoot := ""
	if data.Operator.CanonicalInbox != nil {
		inboxRoot = data.Operator.CanonicalInbox.Root
	}
	fmt.Fprintf(out, "# operator status: %s/%s\n", data.Profile, data.Session)
	fmt.Fprintf(out, "# inbox: %s handle=%s\n", inboxRoot, data.Operator.Handle)
	fmt.Fprintf(out, "# loop: %s owner=%s backlog=%d\n\n", data.OperatorLoop.State, data.OperatorLoop.Owner, data.OperatorLoop.Backlog)
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "THREAD\tREASON\tFROM\tSUBJECT")
	for _, item := range data.Attention {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", item.Thread, item.Reason, item.From, item.Subject)
	}
	return w.Flush()
}
