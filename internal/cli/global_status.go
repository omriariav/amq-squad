package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/runtimeaction"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

const (
	globalStatusHealthy  = "healthy"
	globalStatusDegraded = "degraded"
	globalStatusStopped  = "stopped"
	globalStatusUnknown  = "unknown"
)

// globalStatusSourceError is a structured, fail-visible projection failure.
// Status never turns an unreadable or contradictory source into a healthy row.
type globalStatusSourceError struct {
	Source      string `json:"source"`
	NamespaceID string `json:"namespace_id,omitempty"`
	Detail      string `json:"detail"`
}

type globalStatusRegistryView struct {
	State             string    `json:"state"`
	Path              string    `json:"path"`
	SchemaVersion     int       `json:"schema_version,omitempty"`
	CurrentGeneration uint64    `json:"current_generation,omitempty"`
	UpdatedAt         time.Time `json:"updated_at,omitempty"`
}

type globalStatusRuntimeIdentity struct {
	Live        bool `json:"live"`
	PIDLive     bool `json:"pid_live"`
	PaneLive    bool `json:"pane_live"`
	PIDAlive    bool `json:"pid_alive"`
	BinaryMatch bool `json:"binary_match"`
}

type globalStatusNOCView struct {
	LaunchID       string                      `json:"launch_id"`
	Generation     uint64                      `json:"generation"`
	PersistedState string                      `json:"persisted_state"`
	Health         string                      `json:"health"`
	Binary         string                      `json:"binary"`
	Session        string                      `json:"session"`
	Role           string                      `json:"role"`
	PaneID         string                      `json:"pane_id,omitempty"`
	Runtime        globalStatusRuntimeIdentity `json:"runtime"`
	Backstop       globalNOCBackstop           `json:"stall_backstop"`
	CreatedAt      time.Time                   `json:"created_at"`
	UpdatedAt      time.Time                   `json:"updated_at"`
	Detail         string                      `json:"detail,omitempty"`
	Actions        []runtimeActionJSON         `json:"actions"`
}

type globalStatusLeadView struct {
	Role        string               `json:"role"`
	Handle      string               `json:"handle"`
	Binary      string               `json:"binary"`
	Status      statusState          `json:"status"`
	RecordState string               `json:"record_state"`
	Detail      string               `json:"detail,omitempty"`
	Signals     statusSignals        `json:"signals"`
	Namespace   squadnamespace.Ref   `json:"namespace"`
	Terminal    *terminalRuntimeJSON `json:"terminal,omitempty"`
}

type globalStatusRegistrationView struct {
	State      string                           `json:"state"`
	Policy     string                           `json:"policy"`
	Provenance *launch.OrchestratorRegistration `json:"provenance,omitempty"`
}

type globalStatusGateView struct {
	Thread      string    `json:"thread"`
	Subject     string    `json:"subject"`
	GateKind    string    `json:"gate_kind,omitempty"`
	Age         string    `json:"age"`
	LastEventAt time.Time `json:"last_event_at,omitempty"`
	Inspect     string    `json:"inspect"`
}

type globalStatusGatesView struct {
	Open      int                    `json:"open"`
	OldestAge string                 `json:"oldest_age,omitempty"`
	Items     []globalStatusGateView `json:"items"`
}

type globalStatusBackstopView struct {
	Health string            `json:"health"`
	Mode   string            `json:"mode"`
	Config globalNOCBackstop `json:"config"`
	Detail string            `json:"detail,omitempty"`
}

type globalStatusReadiness struct {
	State   string   `json:"state"`
	Ready   bool     `json:"ready"`
	Reasons []string `json:"reasons,omitempty"`
}

type globalStatusRunView struct {
	ID             string                       `json:"id"`
	Namespace      squadnamespace.Ref           `json:"namespace"`
	Project        string                       `json:"project"`
	Profile        string                       `json:"profile"`
	Session        string                       `json:"session"`
	Health         string                       `json:"health"`
	Lead           globalStatusLeadView         `json:"lead"`
	Registration   globalStatusRegistrationView `json:"registration"`
	OperatorGates  globalStatusGatesView        `json:"operator_gates"`
	Watcher        notificationWatcherStatus    `json:"watcher"`
	Backstop       globalStatusBackstopView     `json:"stall_backstop"`
	Readiness      globalStatusReadiness        `json:"readiness"`
	LastRefresh    time.Time                    `json:"last_refresh"`
	RegistryUpdate time.Time                    `json:"registry_updated_at"`
	SourceErrors   []globalStatusSourceError    `json:"source_errors"`
	Actions        []runtimeActionJSON          `json:"actions"`
}

type globalStatusEnvelopeData struct {
	ControlRoot  string                    `json:"control_root"`
	ObservedAt   time.Time                 `json:"observed_at"`
	Health       string                    `json:"health"`
	ReadOnly     bool                      `json:"readonly"`
	Registry     globalStatusRegistryView  `json:"registry"`
	NOC          *globalStatusNOCView      `json:"noc,omitempty"`
	Runs         []globalStatusRunView     `json:"runs"`
	Readiness    globalStatusReadiness     `json:"readiness"`
	SourceErrors []globalStatusSourceError `json:"source_errors"`
	Actions      []runtimeActionJSON       `json:"actions"`
}

type globalStatusExecution struct {
	ControlRoot string
	Out         io.Writer
	JSON        bool
	Now         func() time.Time

	ReadRegistry func(string) (globalNOCRegistry, error)
	ReadTeam     func(string, string) (team.Team, error)
	ClassifyLead func(team.Team, string, team.Member, string) statusRecord
	OperatorData func(string, string, string, string, func() time.Time) (operatorStatusEnvelopeData, error)
	Watcher      func(team.Team, string, string, time.Time) notificationWatcherStatus
	NOCIdentity  func(launch.Record, string) launchRuntimeIdentity
}

func runGlobalStatus(args []string) error {
	fs := flag.NewFlagSet("global status", flag.ContinueOnError)
	root := fs.String("root", defaultGlobalNOCControlRoot(), "neutral NOC control root")
	jsonOut := fs.Bool("json", false, "emit a schema-versioned global status envelope")
	fs.Usage = func() { _ = runGlobal([]string{"-h"}) }
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return usageErrorf("unexpected argument %q", fs.Arg(0))
	}
	if strings.TrimSpace(*root) == "" {
		return usageErrorf("global status requires --root (could not infer a home directory)")
	}
	return executeGlobalStatus(globalStatusExecution{
		ControlRoot: *root,
		Out:         os.Stdout,
		JSON:        *jsonOut,
	})
}

func executeGlobalStatus(s globalStatusExecution) error {
	out := s.Out
	if out == nil {
		out = io.Discard
	}
	nowFn := s.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	observedAt := nowFn().UTC()
	readRegistry := s.ReadRegistry
	if readRegistry == nil {
		readRegistry = readGlobalNOCRegistry
	}

	// The registry is read exactly once. Everything below projects this
	// validated snapshot, so removal or replacement during the projection
	// cannot splice two registry generations into one report.
	registry, registryErr := readRegistry(s.ControlRoot)
	controlRoot := strings.TrimSpace(s.ControlRoot)
	if canonical, err := canonicalGlobalNOCControlRoot(s.ControlRoot); err == nil {
		controlRoot = canonical
	}
	data := globalStatusEnvelopeData{
		ControlRoot:  controlRoot,
		ObservedAt:   observedAt,
		Health:       globalStatusUnknown,
		ReadOnly:     true,
		Runs:         []globalStatusRunView{},
		SourceErrors: []globalStatusSourceError{},
		Registry: globalStatusRegistryView{
			State: "healthy",
			Path:  globalNOCRegistryPath(controlRoot),
		},
	}
	if registryErr != nil {
		state := "unreadable"
		if errors.Is(registryErr, os.ErrNotExist) {
			state = "missing"
		}
		sourceErr := globalStatusSourceError{Source: "registry", Detail: registryErr.Error()}
		data.Registry.State = state
		data.SourceErrors = append(data.SourceErrors, sourceErr)
		data.Readiness = globalStatusReadiness{
			State: globalStatusUnknown, Ready: false,
			Reasons: []string{"NOC registry is " + state},
		}
		data.Actions = globalNOCStatusActions(controlRoot, nil, state)
		return renderGlobalStatus(out, data, s.JSON)
	}

	data.ControlRoot = registry.ControlRoot
	data.Registry = globalStatusRegistryView{
		State:             "healthy",
		Path:              globalNOCRegistryPath(registry.ControlRoot),
		SchemaVersion:     registry.SchemaVersion,
		CurrentGeneration: registry.CurrentGeneration,
		UpdatedAt:         registry.UpdatedAt,
	}
	if len(registry.Launches) > 0 {
		current := registry.Launches[len(registry.Launches)-1]
		data.NOC = projectGlobalStatusNOC(current, s.NOCIdentity)
	}
	data.Actions = globalNOCStatusActions(registry.ControlRoot, data.NOC, "healthy")
	if data.NOC != nil {
		data.NOC.Actions = append([]runtimeActionJSON(nil), data.Actions...)
	}

	runs := append([]globalNOCRun(nil), registry.Runs...)
	sort.SliceStable(runs, func(i, j int) bool {
		if runs[i].Namespace.ID != runs[j].Namespace.ID {
			return runs[i].Namespace.ID < runs[j].Namespace.ID
		}
		if runs[i].CreatedAt.Equal(runs[j].CreatedAt) {
			return runs[i].ID < runs[j].ID
		}
		return runs[i].CreatedAt.Before(runs[j].CreatedAt)
	})
	for _, run := range runs {
		row := projectGlobalStatusRun(s, registry, run, observedAt)
		data.Runs = append(data.Runs, row)
		data.SourceErrors = append(data.SourceErrors, row.SourceErrors...)
	}
	data.Health = globalStatusOverallHealth(data.NOC, data.Runs, data.SourceErrors)
	data.Readiness = globalStatusReadinessFor(data.Health, data.SourceErrors)
	return renderGlobalStatus(out, data, s.JSON)
}

func projectGlobalStatusNOC(item globalNOCLaunch, identityFn func(launch.Record, string) launchRuntimeIdentity) *globalStatusNOCView {
	if identityFn == nil {
		identityFn = globalNOCRuntimeIdentity
	}
	identity := identityFn(item.Record, "")
	health := globalStatusStopped
	switch item.State {
	case globalNOCLaunchActive:
		switch {
		case identity.PIDLive && identity.PaneLive:
			health = globalStatusHealthy
		case identity.Live:
			health = globalStatusDegraded
		}
	case globalNOCLaunchPrepared:
		health = globalStatusDegraded
	case globalNOCLaunchFailed:
		health = globalStatusDegraded
	case globalNOCLaunchStopped:
		health = globalStatusStopped
	}
	detail := item.Detail
	if item.State == globalNOCLaunchStopped && identity.Live {
		health = globalStatusDegraded
		contradiction := "persisted NOC state is stopped but the canonical runtime identity is live; start is withheld"
		if strings.TrimSpace(detail) == "" {
			detail = contradiction
		} else {
			detail += "; " + contradiction
		}
	}
	paneID := ""
	if item.Record.Tmux != nil {
		paneID = item.Record.Tmux.PaneID
	}
	return &globalStatusNOCView{
		LaunchID: item.ID, Generation: item.Generation, PersistedState: item.State,
		Health: health, Binary: item.Record.Binary, Session: item.Record.Session,
		Role: item.Record.Role, PaneID: paneID,
		Runtime: globalStatusRuntimeIdentity{
			Live: identity.Live, PIDLive: identity.PIDLive, PaneLive: identity.PaneLive,
			PIDAlive: identity.PIDAlive, BinaryMatch: identity.BinaryMatch,
		},
		Backstop: item.Backstop, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt,
		Detail: detail,
	}
}

func projectGlobalStatusRun(s globalStatusExecution, registry globalNOCRegistry, run globalNOCRun, observedAt time.Time) globalStatusRunView {
	row := globalStatusRunView{
		ID: run.ID, Namespace: run.Namespace, Project: run.Namespace.TeamHome,
		Profile: squadnamespace.NormalizeProfile(run.Namespace.Profile), Session: run.Namespace.Session,
		Health: globalStatusUnknown, LastRefresh: observedAt, RegistryUpdate: run.UpdatedAt,
		Lead: globalStatusLeadView{
			Role: strings.TrimSpace(run.LeadRole), Namespace: run.Namespace,
		},
		Registration: globalStatusRegistrationView{
			State: run.State, Policy: run.Policy, Provenance: run.ExternalRegistration,
		},
		OperatorGates: globalStatusGatesView{Items: []globalStatusGateView{}},
		SourceErrors:  []globalStatusSourceError{},
	}
	backstop := globalNOCBackstop{}
	if run.NOCGeneration > 0 && run.NOCGeneration <= uint64(len(registry.Launches)) {
		backstop = registry.Launches[run.NOCGeneration-1].Backstop
	}
	row.Backstop = globalStatusRunBackstop(run, backstop)
	appendRunSourceError := func(source, detail string) {
		row.SourceErrors = append(row.SourceErrors, globalStatusSourceError{
			Source: source, NamespaceID: run.Namespace.ID, Detail: detail,
		})
	}

	expectedNamespace := squadnamespace.Resolve(run.Namespace.TeamHome, run.Namespace.Profile, run.Namespace.Session)
	namespaceValid := sameGlobalStatusNamespace(run.Namespace, expectedNamespace) &&
		strings.TrimSpace(run.Namespace.Session) != "" &&
		globalStatusNamespacePathsAbsolute(run.Namespace)
	if !namespaceValid {
		appendRunSourceError("namespace", "registry namespace fields contradict the canonical project/profile/session resolution")
	}
	validateGlobalStatusRegistration(registry, run, appendRunSourceError)
	if !namespaceValid {
		row.Readiness = globalStatusReadinessFor(row.Health, row.SourceErrors)
		row.Actions = []runtimeActionJSON{}
		return row
	}

	readTeam := s.ReadTeam
	if readTeam == nil {
		readTeam = team.ReadProfile
	}
	tm, err := readTeam(run.Namespace.TeamHome, run.Namespace.Profile)
	if err != nil {
		appendRunSourceError("team", err.Error())
		row.Readiness = globalStatusReadinessFor(row.Health, row.SourceErrors)
		row.Actions = confirmGatedSessionActions(run.Namespace)
		return row
	}
	if !sameGlobalStatusPath(tm.Project, run.Namespace.TeamHome) {
		appendRunSourceError("team", "team project contradicts the registered namespace")
		row.Readiness = globalStatusReadinessFor(row.Health, row.SourceErrors)
		row.Actions = []runtimeActionJSON{}
		return row
	}
	leadRole := strings.TrimSpace(run.LeadRole)
	if leadRole == "" {
		leadRole = strings.TrimSpace(tm.Lead)
	}
	if strings.TrimSpace(tm.Lead) != "" && leadRole != strings.TrimSpace(tm.Lead) {
		appendRunSourceError("lead", fmt.Sprintf("registered lead role %q contradicts team lead role %q", leadRole, tm.Lead))
	}
	leadMember, ok := globalStatusLeadMember(tm, leadRole)
	if !ok {
		appendRunSourceError("lead", fmt.Sprintf("registered lead role %q is absent from the team", leadRole))
		row.Lead.Role = leadRole
	} else {
		classify := s.ClassifyLead
		if classify == nil {
			classify = func(t team.Team, profile string, member team.Member, session string) statusRecord {
				return classifyMemberStatus(t, profile, member, session, defaultDuplicateLaunchProbe)
			}
		}
		status := classify(tm, run.Namespace.Profile, leadMember, run.Namespace.Session)
		status.Namespace = expectedNamespace
		row.Lead = globalStatusLeadView{
			Role: status.Role, Handle: status.Handle, Binary: status.Binary,
			Status: status.Status, RecordState: status.RecordState, Detail: status.Detail,
			Signals: status.Signals, Namespace: status.Namespace, Terminal: status.Terminal,
		}
		if strings.TrimSpace(status.ClassificationError) != "" {
			appendRunSourceError("lead", status.ClassificationError)
		}
	}

	operatorProjector := s.OperatorData
	if operatorProjector == nil {
		operatorProjector = func(project, profile, session, baseRoot string, now func() time.Time) (operatorStatusEnvelopeData, error) {
			return buildOperatorStatusData(operatorExecution{
				ProjectDir: project, Profile: profile, Session: session,
				BaseRoot: baseRoot, Now: now,
			})
		}
	}
	baseRoot := globalStatusAMQBaseRoot(run.Namespace)
	operatorData, operatorErr := operatorProjector(run.Namespace.TeamHome, run.Namespace.Profile, run.Namespace.Session, baseRoot, func() time.Time { return observedAt })
	if operatorErr != nil {
		appendRunSourceError("operator_gates", operatorErr.Error())
	} else {
		if !sameGlobalStatusNamespace(operatorData.Namespace, expectedNamespace) {
			appendRunSourceError("operator_gates", "operator projection namespace contradicts the registered namespace")
		} else {
			var attentionErrors []string
			row.OperatorGates, attentionErrors = globalStatusOpenGates(operatorData.Attention, expectedNamespace)
			for _, detail := range attentionErrors {
				appendRunSourceError("operator_gates", detail)
			}
		}
	}

	watcher := s.Watcher
	if watcher == nil {
		watcher = inspectNotificationWatcher
	}
	row.Watcher = watcher(tm, run.Namespace.Profile, run.Namespace.Session, observedAt)
	row.Actions = confirmGatedSessionActions(run.Namespace)
	row.Health = globalStatusRunHealth(row)
	row.Readiness = globalStatusReadinessFor(row.Health, row.SourceErrors)
	return row
}

func globalStatusAMQBaseRoot(namespace squadnamespace.Ref) string {
	return namespace.AMQRoot
}

func globalStatusNamespacePathsAbsolute(namespace squadnamespace.Ref) bool {
	for _, path := range []string{
		namespace.TeamHome,
		namespace.AMQRoot,
		namespace.Paths.ProfileConfig,
		namespace.Paths.AMQRoot,
		namespace.Paths.Brief,
		namespace.Paths.Tasks,
	} {
		if strings.TrimSpace(path) == "" ||
			!filepath.IsAbs(path) ||
			filepath.Clean(path) != path {
			return false
		}
	}
	return true
}

func sameGlobalStatusNamespace(a, b squadnamespace.Ref) bool {
	if !sameGlobalStatusPath(a.TeamHome, b.TeamHome) ||
		a.Profile != b.Profile ||
		a.Session != b.Session ||
		a.ID != b.ID ||
		a.Display != b.Display ||
		a.AMQSession != b.AMQSession {
		return false
	}
	for _, pair := range [][2]string{
		{a.AMQRoot, b.AMQRoot},
		{a.Paths.ProfileConfig, b.Paths.ProfileConfig},
		{a.Paths.AMQRoot, b.Paths.AMQRoot},
		{a.Paths.Brief, b.Paths.Brief},
		{a.Paths.Tasks, b.Paths.Tasks},
	} {
		if !sameOptionalGlobalStatusPath(pair[0], pair[1]) {
			return false
		}
	}
	return true
}

func sameOptionalGlobalStatusPath(a, b string) bool {
	if strings.TrimSpace(a) == "" || strings.TrimSpace(b) == "" {
		return strings.TrimSpace(a) == strings.TrimSpace(b)
	}
	return sameGlobalStatusPath(a, b)
}

func sameGlobalStatusPath(a, b string) bool {
	if strings.TrimSpace(a) == "" || strings.TrimSpace(b) == "" {
		return false
	}
	return canonicalFilesystemPath(a) == canonicalFilesystemPath(b)
}

func globalStatusLeadMember(t team.Team, role string) (team.Member, bool) {
	for _, member := range t.Members {
		if strings.TrimSpace(member.Role) == strings.TrimSpace(role) {
			return member, true
		}
	}
	return team.Member{}, false
}

func validateGlobalStatusRegistration(registry globalNOCRegistry, run globalNOCRun, add func(string, string)) {
	registration := run.ExternalRegistration
	if run.State == globalNOCRunRegistered && registration == nil {
		add("registration", "wake_registered run has no external registration provenance")
		return
	}
	if run.State != globalNOCRunRegistered && registration != nil {
		add("registration", fmt.Sprintf("%s run unexpectedly carries wake registration provenance", run.State))
	}
	if registration == nil {
		return
	}
	if strings.TrimSpace(registration.Policy) != strings.TrimSpace(run.Policy) ||
		registration.State != globalNOCRunRegistered {
		add("registration", "external registration policy/state contradicts the registry run")
	}
	if !sameGlobalStatusPath(registration.NOCControlRoot, registry.ControlRoot) ||
		registration.NOCLaunchID != run.NOCLaunchID ||
		registration.NOCGeneration != run.NOCGeneration ||
		registration.NOCRunRegistrationID != run.ID {
		add("registration", "external registration provenance contradicts the registry run binding")
	}
	if strings.TrimSpace(registration.Handle) == "" ||
		strings.TrimSpace(registration.ExternalRegistrationID) == "" ||
		registration.ExternalGeneration == 0 ||
		registration.RegisteredAt.IsZero() {
		add("registration", "external registration provenance is incomplete")
	}
}

func globalStatusOpenGates(items []operatorAttention, expected squadnamespace.Ref) (globalStatusGatesView, []string) {
	out := globalStatusGatesView{Items: []globalStatusGateView{}}
	var sourceErrors []string
	var oldest operatorAttention
	for _, item := range items {
		if !isOpenGateAttention(item) {
			continue
		}
		if strings.TrimSpace(item.Profile) == "" ||
			!squadnamespace.ProfilesEqual(item.Profile, expected.Profile) ||
			item.Session != expected.Session ||
			item.NamespaceID != expected.ID {
			sourceErrors = append(sourceErrors, fmt.Sprintf(
				"attention item %q contradicts registered namespace %s (profile=%q session=%q namespace_id=%q)",
				item.Thread, expected.ID, item.Profile, item.Session, item.NamespaceID,
			))
			continue
		}
		out.Items = append(out.Items, globalStatusGateView{
			Thread: item.Thread, Subject: item.Subject, GateKind: item.GateKind,
			Age: item.Age, LastEventAt: item.LastEventAt, Inspect: item.Inspect,
		})
		if oldest.LastEventAt.IsZero() || (!item.LastEventAt.IsZero() && item.LastEventAt.Before(oldest.LastEventAt)) {
			oldest = item
		}
	}
	sort.SliceStable(out.Items, func(i, j int) bool {
		if out.Items[i].LastEventAt.Equal(out.Items[j].LastEventAt) {
			return out.Items[i].Thread < out.Items[j].Thread
		}
		return out.Items[i].LastEventAt.Before(out.Items[j].LastEventAt)
	})
	out.Open = len(out.Items)
	if out.Open > 0 {
		out.OldestAge = oldest.Age
	}
	return out, sourceErrors
}

func globalStatusRunBackstop(run globalNOCRun, config globalNOCBackstop) globalStatusBackstopView {
	out := globalStatusBackstopView{Config: config}
	switch run.State {
	case globalNOCRunRegistered:
		out.Health = "standby"
		out.Mode = "wake_with_bounded_polling_fallback"
		out.Detail = "wake registration is primary; bounded polling remains the stall backstop"
	case globalNOCRunPollRequired:
		out.Health = "required"
		out.Mode = "bounded_polling_required"
		out.Detail = "wake registration is unavailable; bounded polling is required"
	case globalNOCRunOptOut:
		out.Health = "required"
		out.Mode = "bounded_polling_opt_out"
		out.Detail = "wake registration was explicitly declined; bounded polling is required"
	default:
		out.Health = "pending"
		out.Mode = "registration_pending"
		out.Detail = "registration has not reached a terminal projection state"
	}
	return out
}

func globalStatusRunHealth(row globalStatusRunView) string {
	if len(row.SourceErrors) > 0 {
		return globalStatusUnknown
	}
	switch row.Lead.Status {
	case statusStateMissing, statusStateStale:
		return globalStatusStopped
	case statusStateWakeLive:
		return globalStatusDegraded
	case statusStateLive:
	default:
		return globalStatusUnknown
	}
	if row.Registration.State != globalNOCRunRegistered {
		return globalStatusDegraded
	}
	if row.OperatorGates.Open > 0 {
		return globalStatusDegraded
	}
	if row.Watcher.Enabled && row.Watcher.Health != "healthy" && row.Watcher.Health != "external-active" {
		return globalStatusDegraded
	}
	return globalStatusHealthy
}

func globalStatusOverallHealth(noc *globalStatusNOCView, runs []globalStatusRunView, sourceErrors []globalStatusSourceError) string {
	if len(sourceErrors) > 0 || noc == nil {
		return globalStatusUnknown
	}
	health := noc.Health
	for _, row := range runs {
		switch row.Health {
		case globalStatusUnknown:
			return globalStatusUnknown
		case globalStatusDegraded:
			if health == globalStatusHealthy || health == globalStatusStopped {
				health = globalStatusDegraded
			}
		case globalStatusStopped:
			if health == globalStatusHealthy {
				health = globalStatusDegraded
			}
		}
	}
	return health
}

func globalStatusReadinessFor(health string, sourceErrors []globalStatusSourceError) globalStatusReadiness {
	out := globalStatusReadiness{State: health, Ready: health == globalStatusHealthy}
	for _, sourceErr := range sourceErrors {
		out.Reasons = append(out.Reasons, sourceErr.Source+": "+sourceErr.Detail)
	}
	switch {
	case len(out.Reasons) > 0:
	case health == globalStatusDegraded:
		out.Reasons = append(out.Reasons, "one or more runtime, registration, watcher, or backstop signals are degraded")
	case health == globalStatusStopped:
		out.Reasons = append(out.Reasons, "canonical runtime identity is not live")
	case health == globalStatusUnknown:
		out.Reasons = append(out.Reasons, "required status evidence is incomplete")
	}
	return out
}

func globalNOCStatusActions(root string, noc *globalStatusNOCView, registryState string) []runtimeActionJSON {
	scope := " --root " + runtimeaction.ShellQuote(root)
	actions := []runtimeActionJSON{
		{
			Kind: "global_status", Label: "inspect global NOC status", Scope: "global",
			Command: "amq-squad global status" + scope + " --json",
			Mutates: false, NeedsConfirmation: false, Available: true,
		},
	}
	if noc != nil && noc.PersistedState == globalNOCLaunchStopped && noc.Runtime.Live {
		return runtimeaction.ApplyCanonical(actions)
	}
	repairAvailable := registryState == "missing" || (registryState == "healthy" && (noc == nil || noc.Health == globalStatusStopped))
	repairReason := "confirm-gated: launches and persists a new global NOC runtime"
	if !repairAvailable {
		repairReason = "a new NOC launch is available only when the registry is absent or the current canonical runtime is stopped"
	}
	actions = append(actions,
		runtimeActionJSON{
			Kind: "global_start", Label: "confirm launch global NOC", Scope: "global",
			Command: "amq-squad global start" + scope + " --go",
			Mutates: true, NeedsConfirmation: true, Available: repairAvailable, Reason: repairReason,
		},
	)
	return runtimeaction.ApplyCanonical(actions)
}

func confirmGatedSessionActions(namespace squadnamespace.Ref) []runtimeActionJSON {
	actions := sessionActions(namespace.TeamHome, namespace.Profile, namespace.Session, "")
	filtered := make([]runtimeActionJSON, 0, 3)
	for _, action := range actions {
		switch action.Kind {
		case "status", "resume_preview", "resume_new_session":
		default:
			continue
		}
		if action.Mutates {
			action.NeedsConfirmation = true
			if !strings.Contains(strings.ToLower(action.Label), "confirm") {
				action.Label = "confirm " + action.Label
			}
			action.Reason = "confirm-gated: mutates the registered session runtime"
			runtimeaction.SyncUnavailableReason(&action)
		}
		filtered = append(filtered, action)
	}
	return filtered
}

func renderGlobalStatus(out io.Writer, data globalStatusEnvelopeData, jsonOut bool) error {
	if jsonOut {
		return writeJSONEnvelope(out, "global_status", data)
	}
	fmt.Fprintf(out, "global NOC status: %s (registry: %s)\n", data.Health, data.Registry.State)
	fmt.Fprintf(out, "root: %s\n", data.ControlRoot)
	if data.NOC != nil {
		fmt.Fprintf(out, "NOC: generation %d %s, runtime %s, pane %s\n",
			data.NOC.Generation, data.NOC.PersistedState, data.NOC.Health, emptyCell(data.NOC.PaneID))
	}
	if len(data.Runs) > 0 {
		tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "NAMESPACE\tHEALTH\tLEAD\tDETAIL\tREGISTRATION\tGATES\tWATCHER\tLAST-REFRESH")
		for _, row := range data.Runs {
			gates := fmt.Sprintf("%d", row.OperatorGates.Open)
			if row.OperatorGates.OldestAge != "" {
				gates += " (" + row.OperatorGates.OldestAge + ")"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s/%s\t%s\t%s\t%s\t%s\t%s\n",
				row.Namespace.ID, row.Health, emptyCell(row.Lead.Role), emptyCell(row.Lead.Status),
				emptyCell(row.Lead.Detail), row.Registration.State, gates, emptyCell(row.Watcher.Health), row.LastRefresh.Format(time.RFC3339))
		}
		_ = tw.Flush()
	}
	for _, sourceErr := range data.SourceErrors {
		scope := sourceErr.NamespaceID
		if scope == "" {
			scope = "global"
		}
		fmt.Fprintf(out, "source error [%s/%s]: %s\n", scope, sourceErr.Source, sourceErr.Detail)
	}
	return nil
}

func emptyCell[T ~string](value T) string {
	if strings.TrimSpace(string(value)) == "" {
		return "-"
	}
	return string(value)
}
