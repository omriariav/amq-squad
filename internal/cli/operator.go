package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/flock"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/state"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

const defaultOperatorPollLeaseTTL = 2 * time.Minute

type operatorExecution struct {
	ProjectDir      string
	Profile         string
	Session         string
	BaseRoot        string
	ReadOnly        bool
	Owner           string
	OwnerID         string
	LeaseTTL        time.Duration
	Force           bool
	ForceReason     string
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
	ReadOnly         bool                 `json:"readonly"`
	Operator         statusOperatorView   `json:"operator"`
	OperatorDelivery operatorDeliveryData `json:"operator_delivery"`
	OperatorLoop     operatorLoopStatus   `json:"operator_loop"`
	Attention        []operatorAttention  `json:"attention,omitempty"`
	OperatorGates    bool                 `json:"operator_gates"`
	Message          string               `json:"message,omitempty"`
	operatorCursor   string
}

type operatorLoopStatus struct {
	Mode              string `json:"mode"`
	PollRequired      bool   `json:"poll_required"`
	State             string `json:"state"`
	Owner             string `json:"owner"`
	OwnerID           string `json:"owner_id,omitempty"`
	LeaseTTL          string `json:"lease_ttl,omitempty"`
	LeaseExpiresAt    string `json:"lease_expires_at,omitempty"`
	LastPollAt        string `json:"last_poll_at,omitempty"`
	Cursor            string `json:"cursor,omitempty"`
	Backlog           int    `json:"backlog"`
	GatesOpen         int    `json:"gates_open"`
	DirectivesUnacked int    `json:"directives_unacked"`
	DegradedReason    string `json:"degraded_reason,omitempty"`
}

type operatorLoopLeaseFile struct {
	SchemaVersion  int       `json:"schema_version"`
	Profile        string    `json:"profile"`
	Session        string    `json:"session"`
	NamespaceID    string    `json:"namespace_id"`
	Mode           string    `json:"mode"`
	Owner          string    `json:"owner"`
	OwnerID        string    `json:"owner_id"`
	LeaseTTL       string    `json:"lease_ttl"`
	LeaseExpiresAt time.Time `json:"lease_expires_at"`
	LastPollAt     time.Time `json:"last_poll_at"`
	Cursor         string    `json:"cursor,omitempty"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type operatorLoopForceAuditRecord struct {
	At                   time.Time `json:"at"`
	ProjectDir           string    `json:"project_dir"`
	Profile              string    `json:"profile"`
	Session              string    `json:"session"`
	NamespaceID          string    `json:"namespace_id"`
	ActorOwner           string    `json:"actor_owner"`
	ActorOwnerID         string    `json:"actor_owner_id"`
	PreviousOwner        string    `json:"previous_owner"`
	PreviousOwnerID      string    `json:"previous_owner_id"`
	PreviousLeaseExpires time.Time `json:"previous_lease_expires_at"`
	Reason               string    `json:"reason"`
}

func runOperator(args []string) error {
	if len(args) == 0 {
		return usageErrorf("operator requires a subcommand (status)")
	}
	switch args[0] {
	case "status":
		return runOperatorStatus(args[1:])
	case "poll":
		return runOperatorPoll(args[1:])
	default:
		return usageErrorf("unknown 'operator' subcommand: %q. Try 'status' or 'poll'.", args[0])
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
	data, err := buildOperatorStatusData(o)
	if err != nil {
		return err
	}
	return writeOrRenderOperatorStatus(o.Out, "operator_status", "operator status", data, o.JSON)
}

func runOperatorPoll(args []string) error {
	fs := flag.NewFlagSet("operator poll", flag.ContinueOnError)
	projectFlag := fs.String("project", "", "project/team-home directory to inspect (default: cwd)")
	profileFlag := fs.String("profile", "", "team profile to inspect (default: default profile)")
	sessionFlag := fs.String("session", "", "AMQ workstream/session to inspect")
	readonly := fs.Bool("readonly", false, "read the operator loop state without claiming a poll lease")
	owner := fs.String("owner", "cli", "poll lease owner class (cli, noc, daemon)")
	ownerID := fs.String("owner-id", "", "stable poll lease owner identity (default: cli:<hostname>:<pid>)")
	leaseTTL := fs.Duration("ttl", defaultOperatorPollLeaseTTL, "poll lease duration")
	force := fs.Bool("force", false, "steal an active poll lease from another owner")
	forceReason := fs.String("reason", "", "required reason when --force steals an active lease")
	jsonOut := fs.Bool("json", false, "emit a schema-versioned operator poll envelope")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad operator poll - read the operator polling workload

Usage:
  amq-squad operator poll [--project DIR] [--profile NAME] [--session NAME] [--owner NAME] [--owner-id ID] [--ttl D] [--force --reason WHY] [--json]
  amq-squad operator poll --readonly [--project DIR] [--profile NAME] [--session NAME] [--json]

Reads the canonical operator inbox and operator-loop counters without moving
mailbox messages. Without --readonly, this command claims or refreshes a local
operator-loop lease for the resolved profile/session.
`)
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if *leaseTTL <= 0 {
		return usageErrorf("--ttl must be > 0")
	}
	if *readonly && *force {
		return usageErrorf("--force cannot be combined with --readonly")
	}
	if *force && strings.TrimSpace(*forceReason) == "" {
		return usageErrorf("operator poll --force requires --reason <why>")
	}
	if err := validateOperatorOwner(*owner); err != nil {
		return err
	}
	projectDir, profile, err := resolveProjectProfile(*projectFlag, *profileFlag, flagWasSet(fs, "project"))
	if err != nil {
		return err
	}
	return executeOperatorPoll(operatorExecution{
		ProjectDir:      projectDir,
		Profile:         profile,
		Session:         *sessionFlag,
		ReadOnly:        *readonly,
		Owner:           *owner,
		OwnerID:         *ownerID,
		LeaseTTL:        *leaseTTL,
		Force:           *force,
		ForceReason:     *forceReason,
		JSON:            *jsonOut,
		Out:             os.Stdout,
		ResolveBaseRoot: scanBaseRootForProject,
		Probe:           state.DefaultProbe,
		Now:             time.Now,
	})
}

func executeOperatorPoll(o operatorExecution) error {
	data, err := buildOperatorStatusData(o)
	if err != nil {
		return err
	}
	data.ReadOnly = o.ReadOnly
	if !o.ReadOnly {
		now := operatorNow(o)
		owner := strings.TrimSpace(o.Owner)
		if owner == "" {
			owner = "cli"
		}
		if err := validateOperatorOwner(owner); err != nil {
			return err
		}
		ownerID := strings.TrimSpace(o.OwnerID)
		if ownerID == "" {
			ownerID = defaultOperatorOwnerID(owner)
		}
		ttl := o.LeaseTTL
		if ttl <= 0 {
			ttl = defaultOperatorPollLeaseTTL
		}
		lease, err := claimOperatorLoopLease(data.ProjectDir, data.Profile, data.Session, data.Namespace.ID, owner, ownerID, ttl, data.operatorCursor, now, o.Force, o.ForceReason)
		if err != nil {
			return err
		}
		applyOperatorLoopLease(&data.OperatorLoop, lease, now)
	}
	return writeOrRenderOperatorStatus(o.Out, "operator_poll", "operator poll", data, o.JSON)
}

func buildOperatorStatusData(o operatorExecution) (operatorStatusEnvelopeData, error) {
	now := time.Now
	if o.Now != nil {
		now = o.Now
	}
	t, err := team.ReadProfile(o.ProjectDir, o.Profile)
	if err != nil {
		return operatorStatusEnvelopeData{}, fmt.Errorf("read team: %w", err)
	}
	workstream, err := resolveTeamWorkstreamName(t, o.Session, strings.TrimSpace(o.Session) != "")
	if err != nil {
		return operatorStatusEnvelopeData{}, err
	}
	ns := squadnamespace.Resolve(t.Project, o.Profile, workstream)
	operator := statusOperatorForTeam(t, ns)
	delivery := operatorDeliveryForTeam(t)
	data := operatorStatusEnvelopeData{
		ProjectDir:       t.Project,
		Profile:          squadnamespace.NormalizeProfile(o.Profile),
		Session:          workstream,
		Namespace:        ns,
		ReadOnly:         true,
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
		return data, nil
	}

	baseRoot := strings.TrimSpace(o.BaseRoot)
	if baseRoot == "" {
		resolve := o.ResolveBaseRoot
		if resolve == nil {
			resolve = scanBaseRootForProject
		}
		baseRoot, err = resolve(o.ProjectDir)
		if err != nil {
			return operatorStatusEnvelopeData{}, fmt.Errorf("resolve AMQ base root: %w", err)
		}
	}
	snap, err := state.BuildWithThresholds(o.ProjectDir, baseRoot, o.Probe, state.Thresholds{OperatorHandle: operator.Handle})
	if err != nil {
		return operatorStatusEnvelopeData{}, fmt.Errorf("scan AMQ base root: %w", err)
	}
	data.BaseRoot = snap.BaseRoot
	items := collectOperatorAttention(o.ProjectDir, o.Profile, snap, operator.Handle, workstream, now())
	session, sessionOK := operatorSessionSnapshot(snap, o.Profile, workstream)
	backlog := 0
	directivesUnacked := 0
	operatorCursor := ""
	if sessionOK {
		backlog = operatorUnreadBacklog(session.Coordination.Threads, operator.Handle)
		directivesUnacked = operatorDirectivesUnacked(session.Coordination.Threads, operator.Handle, teamLeadHandle(t))
		operatorCursor = operatorInboxHighWater(session.Coordination.Threads, operator.Handle)
	}
	gatesOpen := operatorOpenGates(items)
	data.Attention = items
	data.operatorCursor = operatorCursor
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
	lease, err := readOperatorLoopLease(operatorLoopLeasePath(t.Project, data.Profile, workstream))
	if err != nil {
		return operatorStatusEnvelopeData{}, err
	}
	applyOperatorLoopLease(&data.OperatorLoop, lease, now())
	return data, nil
}

func operatorNow(o operatorExecution) time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now()
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

func operatorLoopLeasePath(projectDir, profile, session string) string {
	base := filepath.Join(projectDir, team.DirName, "operator-loop")
	profile = squadnamespace.NormalizeProfile(profile)
	if profile != team.DefaultProfile {
		base = filepath.Join(base, profile)
	}
	return filepath.Join(base, session+".json")
}

func operatorLoopLeaseLockPath(projectDir, profile, session string) string {
	return operatorLoopLeasePath(projectDir, profile, session) + ".lock"
}

func readOperatorLoopLease(path string) (operatorLoopLeaseFile, error) {
	var lease operatorLoopLeaseFile
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return lease, nil
		}
		return lease, fmt.Errorf("read operator loop lease: %w", err)
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		return lease, nil
	}
	if err := json.Unmarshal(b, &lease); err != nil {
		return lease, fmt.Errorf("parse operator loop lease %s: %w", path, err)
	}
	return lease, nil
}

func claimOperatorLoopLease(projectDir, profile, session, namespaceID, owner, ownerID string, ttl time.Duration, cursor string, now time.Time, force bool, forceReason string) (operatorLoopLeaseFile, error) {
	path := operatorLoopLeasePath(projectDir, profile, session)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return operatorLoopLeaseFile{}, fmt.Errorf("ensure operator loop dir: %w", err)
	}
	var next operatorLoopLeaseFile
	err := flock.WithLock(operatorLoopLeaseLockPath(projectDir, profile, session), func() error {
		current, err := readOperatorLoopLease(path)
		if err != nil {
			return err
		}
		liveForeign := current.OwnerID != "" && current.OwnerID != ownerID && now.Before(current.LeaseExpiresAt)
		if liveForeign && !force {
			return usageErrorf("operator poll lease already held by %s until %s; pass --force --reason <why> to steal it", current.OwnerID, current.LeaseExpiresAt.UTC().Format(time.RFC3339))
		}
		if liveForeign && strings.TrimSpace(forceReason) == "" {
			return usageErrorf("operator poll --force requires --reason <why>")
		}
		next = operatorLoopLeaseFile{
			SchemaVersion:  1,
			Profile:        squadnamespace.NormalizeProfile(profile),
			Session:        session,
			NamespaceID:    namespaceID,
			Mode:           "poll",
			Owner:          owner,
			OwnerID:        ownerID,
			LeaseTTL:       ttl.String(),
			LeaseExpiresAt: now.Add(ttl).UTC(),
			LastPollAt:     now.UTC(),
			Cursor:         cursor,
			UpdatedAt:      now.UTC(),
		}
		if liveForeign {
			if err := writeOperatorLoopForceAudit(projectDir, profile, session, namespaceID, owner, ownerID, current, forceReason, now); err != nil {
				return err
			}
		}
		return writeOperatorLoopLease(path, next)
	})
	if err != nil {
		return operatorLoopLeaseFile{}, err
	}
	return next, nil
}

func writeOperatorLoopForceAudit(projectDir, profile, session, namespaceID, owner, ownerID string, previous operatorLoopLeaseFile, reason string, now time.Time) error {
	dir := filepath.Join(projectDir, team.DirName, "operator-loop-audit")
	profile = squadnamespace.NormalizeProfile(profile)
	if profile != team.DefaultProfile {
		dir = filepath.Join(dir, profile)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("ensure operator loop audit dir: %w", err)
	}
	rec := operatorLoopForceAuditRecord{
		At:                   now.UTC(),
		ProjectDir:           projectDir,
		Profile:              profile,
		Session:              session,
		NamespaceID:          namespaceID,
		ActorOwner:           owner,
		ActorOwnerID:         ownerID,
		PreviousOwner:        previous.Owner,
		PreviousOwnerID:      previous.OwnerID,
		PreviousLeaseExpires: previous.LeaseExpiresAt.UTC(),
		Reason:               strings.TrimSpace(reason),
	}
	b, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("marshal operator loop audit: %w", err)
	}
	path := filepath.Join(dir, sanitizeWorkstreamName(session)+".jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open operator loop audit: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("write operator loop audit: %w", err)
	}
	return nil
}

func writeOperatorLoopLease(path string, lease operatorLoopLeaseFile) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("ensure operator loop dir: %w", err)
	}
	b, err := json.MarshalIndent(lease, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal operator loop lease: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return fmt.Errorf("write operator loop lease: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename operator loop lease: %w", err)
	}
	return nil
}

func applyOperatorLoopLease(loop *operatorLoopStatus, lease operatorLoopLeaseFile, now time.Time) {
	if loop == nil || strings.TrimSpace(lease.OwnerID) == "" {
		return
	}
	loop.Owner = lease.Owner
	loop.OwnerID = lease.OwnerID
	loop.LeaseTTL = lease.LeaseTTL
	if !lease.LeaseExpiresAt.IsZero() {
		loop.LeaseExpiresAt = lease.LeaseExpiresAt.UTC().Format(time.RFC3339)
	}
	if !lease.LastPollAt.IsZero() {
		loop.LastPollAt = lease.LastPollAt.UTC().Format(time.RFC3339)
	}
	loop.Cursor = lease.Cursor
	if !lease.LeaseExpiresAt.IsZero() && now.Before(lease.LeaseExpiresAt) {
		loop.State = "poller_active"
		return
	}
	loop.State = "poller_stale"
}

func operatorInboxHighWater(threads []state.ThreadSummary, operatorHandle string) string {
	latest := ""
	for _, th := range threads {
		if !threadParticipant(th, operatorHandle) || th.LatestID == "" {
			continue
		}
		if th.LatestID > latest {
			latest = th.LatestID
		}
	}
	return latest
}

func threadParticipant(th state.ThreadSummary, handle string) bool {
	handle = strings.TrimSpace(handle)
	if handle == "" {
		return false
	}
	for _, p := range th.Participants {
		if p == handle {
			return true
		}
	}
	return false
}

func validateOperatorOwner(owner string) error {
	switch strings.TrimSpace(owner) {
	case "cli", "noc", "daemon":
		return nil
	default:
		return usageErrorf("--owner must be one of cli, noc, or daemon")
	}
}

func defaultOperatorOwnerID(owner string) string {
	host, err := os.Hostname()
	if err != nil || strings.TrimSpace(host) == "" {
		host = "unknown"
	}
	owner = strings.TrimSpace(owner)
	if owner == "" {
		owner = "cli"
	}
	return fmt.Sprintf("%s:%s:%d", owner, host, os.Getpid())
}

func writeOrRenderOperatorStatus(out io.Writer, kind, label string, data operatorStatusEnvelopeData, jsonOut bool) error {
	if out == nil {
		out = os.Stdout
	}
	if jsonOut {
		return writeJSONEnvelope(out, kind, data)
	}
	inboxRoot := ""
	if data.Operator.CanonicalInbox != nil {
		inboxRoot = data.Operator.CanonicalInbox.Root
	}
	fmt.Fprintf(out, "# %s: %s/%s\n", label, data.Profile, data.Session)
	fmt.Fprintf(out, "# inbox: %s handle=%s\n", inboxRoot, data.Operator.Handle)
	fmt.Fprintf(out, "# loop: %s owner=%s backlog=%d\n\n", data.OperatorLoop.State, data.OperatorLoop.Owner, data.OperatorLoop.Backlog)
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "THREAD\tREASON\tFROM\tSUBJECT")
	for _, item := range data.Attention {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", item.Thread, item.Reason, item.From, item.Subject)
	}
	return w.Flush()
}
