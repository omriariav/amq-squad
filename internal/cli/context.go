package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/procinfo"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

const (
	contextSourceFlags   = "explicit_flags"
	contextSourceEnv     = "injected_environment"
	contextSourceLaunch  = "live_launch_record"
	contextSourceAMQRC   = "project_amqrc"
	contextSourceDefault = "documented_defaults"
)

type contextCandidate struct {
	Field        string `json:"field"`
	Source       string `json:"source"`
	Value        string `json:"value"`
	Selected     bool   `json:"selected"`
	Detail       string `json:"detail,omitempty"`
	rank         int
	tupleProfile string
	tupleSession string
	tupleBound   bool
}

type contextResolution struct {
	ProjectDir          string             `json:"project"`
	Root                string             `json:"root"`
	BaseRoot            string             `json:"base_root"`
	Session             string             `json:"session,omitempty"`
	Profile             string             `json:"profile"`
	Handle              string             `json:"handle,omitempty"`
	PinMode             string             `json:"pin_mode"`
	NamespaceGeneration string             `json:"namespace_generation,omitempty"`
	Sources             map[string]string  `json:"sources"`
	Candidates          []contextCandidate `json:"candidates"`
	Warnings            []string           `json:"warnings,omitempty"`
}

type contextResolveOptions struct {
	ProjectFlag  string
	ProfileFlag  string
	SessionFlag  string
	HandleFlag   string
	RootFlag     string
	BaseRootFlag string

	ProjectExplicit   bool
	ProfileExplicit   bool
	SessionExplicit   bool
	HandleExplicit    bool
	RootExplicit      bool
	BaseRootExplicit  bool
	AllowMalformedEnv bool
}

type injectedContext struct {
	Root, BaseRoot, Session, Profile, Handle string
	PinMode                                  string
	Present                                  bool
}

type launchContext struct {
	Record launch.Record
	Path   string
}

type amqrcContext struct {
	Root, BaseRoot, Session, Profile string
	Path                             string
}

func resolveScopedCommandContext(projectFlag, profileFlag, sessionFlag, handleFlag string, fs *flag.FlagSet) (contextResolution, error) {
	return resolveCanonicalContext(contextResolveOptions{
		ProjectFlag: projectFlag, ProfileFlag: profileFlag, SessionFlag: sessionFlag, HandleFlag: handleFlag,
		ProjectExplicit: flagWasSet(fs, "project"), ProfileExplicit: flagWasSet(fs, "profile"),
		SessionExplicit: flagWasSet(fs, "session"), HandleExplicit: strings.TrimSpace(handleFlag) != "" && flagWasSet(fs, "me"),
	})
}

var (
	contextScanLaunchEntries = launch.ScanEntries
	contextPIDAlive          = procinfo.Alive
)

func contextSourceRank(source string) int {
	switch source {
	case contextSourceFlags:
		return 0
	case contextSourceEnv:
		return 1
	case contextSourceLaunch:
		return 2
	case contextSourceAMQRC:
		return 3
	default:
		return 4
	}
}

func addContextCandidate(candidates *[]contextCandidate, field, source, value, detail string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	*candidates = append(*candidates, contextCandidate{
		Field: field, Source: source, Value: value, Detail: detail, rank: contextSourceRank(source),
	})
}

func addTupleContextCandidate(candidates *[]contextCandidate, field, source, value, detail, profile, session string) {
	before := len(*candidates)
	addContextCandidate(candidates, field, source, value, detail)
	if len(*candidates) == before {
		return
	}
	candidate := &(*candidates)[len(*candidates)-1]
	candidate.tupleProfile = squadnamespace.NormalizeProfile(profile)
	candidate.tupleSession = strings.TrimSpace(session)
	candidate.tupleBound = strings.TrimSpace(profile) != "" || candidate.tupleSession != ""
}

func markContextCandidateLosing(candidate *contextCandidate, reason string) {
	if candidate == nil || strings.Contains(candidate.Detail, reason) {
		return
	}
	candidate.Detail = strings.Trim(strings.TrimSpace(candidate.Detail)+"; losing candidate: "+reason, "; ")
}

func contextCandidateProfileCompatible(candidate contextCandidate, profile string) bool {
	if !candidate.tupleBound || strings.TrimSpace(candidate.tupleProfile) == "" {
		return true
	}
	return squadnamespace.ProfilesEqual(candidate.tupleProfile, profile)
}

func contextCandidateTupleCompatible(candidate contextCandidate, profile, session string) bool {
	if !contextCandidateProfileCompatible(candidate, profile) {
		return false
	}
	if candidate.tupleBound && candidate.tupleSession != "" && session != "" && candidate.tupleSession != session {
		return false
	}
	return true
}

func selectContextCandidate(candidates []contextCandidate, field string) (string, string, error) {
	best := 99
	values := map[string]bool{}
	for _, candidate := range candidates {
		if candidate.Field != field || candidate.Value == "" {
			continue
		}
		if candidate.rank < best {
			best = candidate.rank
			values = map[string]bool{candidate.Value: true}
		} else if candidate.rank == best {
			values[candidate.Value] = true
		}
	}
	if best == 99 {
		return "", "", nil
	}
	if len(values) > 1 {
		var conflicting, lower []string
		seen := map[string]bool{}
		for _, candidate := range candidates {
			if candidate.Field != field || candidate.Value == "" {
				continue
			}
			provenance := candidate.Source
			if candidate.Detail != "" {
				provenance += ": " + candidate.Detail
			}
			entry := fmt.Sprintf("%q from %s", candidate.Value, provenance)
			key := fmt.Sprintf("%d\x00%s", candidate.rank, entry)
			if seen[key] {
				continue
			}
			seen[key] = true
			if candidate.rank == best {
				conflicting = append(conflicting, entry+" [no winner]")
			} else {
				lower = append(lower, entry+" [lower precedence]")
			}
		}
		sort.Strings(conflicting)
		sort.Strings(lower)
		all := append(conflicting, lower...)
		return "", "", usageErrorf("ambiguous %s at %s precedence; no winner; every candidate: %s; pass an explicit --%s", field, contextSourceName(best), strings.Join(all, "; "), contextFlagName(field))
	}
	for value := range values {
		for _, candidate := range candidates {
			if candidate.Field == field && candidate.rank == best && candidate.Value == value {
				return value, candidate.Source, nil
			}
		}
	}
	return "", "", nil
}

func selectOptionalHandleCandidate(candidates []contextCandidate) (string, string, error) {
	handle, source, err := selectContextCandidate(candidates, "handle")
	if err == nil {
		return handle, source, nil
	}
	// Multiple active agents commonly share one coherent profile/session/root.
	// A command that merely selects that namespace must not invent a sender or
	// reject the tuple. An explicit flag/environment candidate already outranks
	// launches; within launch rank, the current pane is a sufficient identity
	// signal when exactly one record matches it.
	pane := strings.TrimSpace(os.Getenv("TMUX_PANE"))
	if pane != "" {
		var matches []contextCandidate
		for _, candidate := range candidates {
			if candidate.Field == "handle" && candidate.Source == contextSourceLaunch && strings.Contains(candidate.Detail, "current TMUX_PANE match") {
				matches = append(matches, candidate)
			}
		}
		if len(matches) == 1 {
			return matches[0].Value, matches[0].Source, nil
		}
	}
	best := 99
	values := map[string]bool{}
	for _, candidate := range candidates {
		if candidate.Field != "handle" || candidate.Value == "" {
			continue
		}
		if candidate.rank < best {
			best = candidate.rank
			values = map[string]bool{candidate.Value: true}
		} else if candidate.rank == best {
			values[candidate.Value] = true
		}
	}
	if best == contextSourceRank(contextSourceLaunch) && len(values) > 1 {
		return "", "", nil
	}
	return "", "", err
}

func emitContextDiagnostics(ctx contextResolution) {
	for _, line := range contextDiagnosticLines(ctx) {
		fmt.Fprintln(os.Stderr, line)
	}
}

func contextDiagnosticLines(ctx contextResolution) []string {
	byField := map[string][]contextCandidate{}
	for _, candidate := range ctx.Candidates {
		byField[candidate.Field] = append(byField[candidate.Field], candidate)
	}
	var fields []string
	for field := range byField {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	var out []string
	for _, field := range fields {
		candidates := byField[field]
		values := map[string]bool{}
		hasRejected := false
		for _, candidate := range candidates {
			values[candidate.Value] = true
			if strings.Contains(candidate.Detail, "losing candidate") {
				hasRejected = true
			}
		}
		if len(values) < 2 && !hasRejected {
			continue
		}
		parts := make([]string, 0, len(candidates))
		for _, candidate := range candidates {
			state := "loser"
			if candidate.Selected {
				state = "winner"
			}
			part := fmt.Sprintf("%s=%q [%s]", candidate.Source, candidate.Value, state)
			if candidate.Detail != "" {
				part += " (" + candidate.Detail + ")"
			}
			parts = append(parts, part)
		}
		out = append(out, fmt.Sprintf("warning: context %s candidates: %s; inspect with 'amq-squad context explain'", field, strings.Join(parts, "; ")))
	}
	for _, warning := range ctx.Warnings {
		out = append(out, "warning: context: "+warning)
	}
	return out
}

func contextSourceName(rank int) string {
	for _, source := range []string{contextSourceFlags, contextSourceEnv, contextSourceLaunch, contextSourceAMQRC, contextSourceDefault} {
		if contextSourceRank(source) == rank {
			return source
		}
	}
	return "unknown"
}

func contextFlagName(field string) string {
	switch field {
	case "handle":
		return "me"
	case "base_root":
		return "base-root"
	default:
		return strings.ReplaceAll(field, "_", "-")
	}
}

func resolveCanonicalContext(opts contextResolveOptions) (contextResolution, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return contextResolution{}, fmt.Errorf("getwd: %w", err)
	}
	projectFlag := opts.ProjectFlag
	projectExplicit := opts.ProjectExplicit
	projectSource := contextSourceDefault
	if projectExplicit {
		projectSource = contextSourceFlags
	} else if launchProject, ok := projectFromInjectedLaunch(cwd); ok {
		projectFlag = launchProject
		projectExplicit = true
		projectSource = contextSourceLaunch
	}
	projectDir, err := resolveProjectDirFlag(cwd, projectFlag, projectExplicit)
	if err != nil {
		return contextResolution{}, err
	}
	projectDir, err = filepath.Abs(projectDir)
	if err != nil {
		return contextResolution{}, fmt.Errorf("resolve project directory: %w", err)
	}

	resolution := contextResolution{
		ProjectDir: projectDir,
		Sources:    map[string]string{"project": projectSource},
	}
	var candidates []contextCandidate

	if opts.ProfileExplicit {
		profile, err := resolveProfileFlag(opts.ProfileFlag)
		if err != nil {
			return contextResolution{}, err
		}
		addContextCandidate(&candidates, "profile", contextSourceFlags, profile, "--profile")
	}
	if opts.SessionExplicit {
		session := strings.TrimSpace(opts.SessionFlag)
		if err := team.ValidateSessionName(session); err != nil {
			return contextResolution{}, usageErrorf("invalid --session: %v", err)
		}
		addContextCandidate(&candidates, "session", contextSourceFlags, session, "--session")
	}
	if opts.HandleExplicit {
		handle := strings.TrimSpace(opts.HandleFlag)
		if handle == "" {
			return contextResolution{}, usageErrorf("--me requires a handle")
		}
		if err := team.ValidateHandle(handle); err != nil {
			return contextResolution{}, usageErrorf("invalid --me: %v", err)
		}
		addContextCandidate(&candidates, "handle", contextSourceFlags, handle, "--me")
	}
	if opts.RootExplicit {
		root := absoluteAMQRoot(projectDir, opts.RootFlag)
		if root == "" {
			return contextResolution{}, usageErrorf("--root requires a directory")
		}
		addContextCandidate(&candidates, "root", contextSourceFlags, root, "--root")
		if profile, session, ok := profileSessionFromAMQRoot(projectDir, root); ok {
			addContextCandidate(&candidates, "profile", contextSourceFlags, profile, "inferred from --root")
			addContextCandidate(&candidates, "session", contextSourceFlags, session, "inferred from --root")
		}
	}
	if opts.BaseRootExplicit {
		base := absoluteAMQRoot(projectDir, opts.BaseRootFlag)
		if base == "" {
			return contextResolution{}, usageErrorf("--base-root requires a directory")
		}
		addContextCandidate(&candidates, "base_root", contextSourceFlags, base, "--base-root")
	}

	envContext, envWarnings, envErr := readInjectedContext(projectDir)
	resolution.Warnings = append(resolution.Warnings, envWarnings...)
	if envErr != nil {
		if !opts.AllowMalformedEnv && !explicitContextCanOverrideMalformedEnv(opts, projectDir) {
			return contextResolution{}, envErr
		}
		resolution.Warnings = append(resolution.Warnings, envErr.Error())
		tupleProfile, tupleSession := envContext.Profile, envContext.Session
		if profile, session, ok := profileSessionFromAMQRoot(projectDir, envContext.Root); ok {
			tupleProfile, tupleSession = profile, session
			addTupleContextCandidate(&candidates, "profile", contextSourceEnv, profile, "inferred from malformed AM_ROOT; losing candidate", tupleProfile, tupleSession)
			addTupleContextCandidate(&candidates, "session", contextSourceEnv, session, "inferred from malformed AM_ROOT; losing candidate", tupleProfile, tupleSession)
		}
		addTupleContextCandidate(&candidates, "handle", contextSourceEnv, envContext.Handle, "AM_ME from malformed injected identity", tupleProfile, tupleSession)
		addTupleContextCandidate(&candidates, "root", contextSourceEnv, envContext.Root, "AM_ROOT from malformed injected identity; losing candidate", tupleProfile, tupleSession)
		addTupleContextCandidate(&candidates, "base_root", contextSourceEnv, envContext.BaseRoot, "AM_BASE_ROOT from malformed injected identity; losing candidate", tupleProfile, tupleSession)
	} else if envContext.Present {
		addTupleContextCandidate(&candidates, "profile", contextSourceEnv, envContext.Profile, "AM_ROOT/AM_BASE_ROOT", envContext.Profile, envContext.Session)
		addTupleContextCandidate(&candidates, "session", contextSourceEnv, envContext.Session, "AM_SESSION or AM_ROOT", envContext.Profile, envContext.Session)
		addTupleContextCandidate(&candidates, "handle", contextSourceEnv, envContext.Handle, "AM_ME", envContext.Profile, envContext.Session)
		addTupleContextCandidate(&candidates, "root", contextSourceEnv, envContext.Root, "AM_ROOT", envContext.Profile, envContext.Session)
		addTupleContextCandidate(&candidates, "base_root", contextSourceEnv, envContext.BaseRoot, "AM_BASE_ROOT", envContext.Profile, envContext.Session)
	}

	launches, err := matchingLiveLaunchContexts(projectDir, opts, envContext)
	if err != nil {
		return contextResolution{}, err
	}
	for _, live := range launches {
		profile := squadnamespace.NormalizeProfile(live.Record.TeamProfile)
		detail := live.Path
		if pane := strings.TrimSpace(os.Getenv("TMUX_PANE")); pane != "" && live.Record.Tmux != nil && live.Record.Tmux.PaneID == pane {
			detail += "; current TMUX_PANE match"
		}
		addTupleContextCandidate(&candidates, "profile", contextSourceLaunch, profile, detail, profile, live.Record.Session)
		addTupleContextCandidate(&candidates, "session", contextSourceLaunch, live.Record.Session, detail, profile, live.Record.Session)
		addTupleContextCandidate(&candidates, "handle", contextSourceLaunch, live.Record.Handle, detail, profile, live.Record.Session)
		addTupleContextCandidate(&candidates, "root", contextSourceLaunch, absoluteAMQRoot(projectDir, live.Record.Root), live.Path, profile, live.Record.Session)
		addTupleContextCandidate(&candidates, "base_root", contextSourceLaunch, absoluteAMQRoot(projectDir, live.Record.BaseRoot), live.Path, profile, live.Record.Session)
	}

	amqrc, err := readProjectAMQRCContext(projectDir)
	if err != nil {
		return contextResolution{}, err
	}
	addTupleContextCandidate(&candidates, "profile", contextSourceAMQRC, amqrc.Profile, amqrc.Path, amqrc.Profile, amqrc.Session)
	addTupleContextCandidate(&candidates, "session", contextSourceAMQRC, amqrc.Session, amqrc.Path, amqrc.Profile, amqrc.Session)

	addContextCandidate(&candidates, "profile", contextSourceDefault, team.DefaultProfile, "implicit default profile")
	profile, profileSource, err := selectContextCandidate(candidates, "profile")
	if err != nil {
		return contextResolution{}, err
	}
	if profile == "" {
		profile = team.DefaultProfile
		profileSource = contextSourceDefault
	}
	profile = squadnamespace.NormalizeProfile(profile)
	if err := team.ValidateProfileName(profile); err != nil {
		return contextResolution{}, usageErrorf("resolved profile %q is invalid: %v", profile, err)
	}

	// Profile is the first tuple anchor. Session candidates from another
	// profile remain observable but cannot be spliced into the selected
	// profile. Conversely, an explicit session without an explicit profile is
	// an intentional same-profile switch: the best profile candidate may carry
	// forward, but the old tuple's session, sender, and roots may not.
	sessionCandidates := make([]contextCandidate, 0, len(candidates)+1)
	for i := range candidates {
		candidate := candidates[i]
		if candidate.Field == "session" && !contextCandidateProfileCompatible(candidate, profile) {
			markContextCandidateLosing(&candidate, fmt.Sprintf("tuple profile %q conflicts with selected profile %q", candidate.tupleProfile, profile))
			candidates[i] = candidate
			continue
		}
		sessionCandidates = append(sessionCandidates, candidate)
	}
	defaultSession, defaultDetail := defaultSessionForProfile(projectDir, profile)
	addTupleContextCandidate(&candidates, "session", contextSourceDefault, defaultSession, defaultDetail, profile, defaultSession)
	sessionCandidates = append(sessionCandidates, candidates[len(candidates)-1])
	session, sessionSource, err := selectContextCandidate(sessionCandidates, "session")
	if err != nil {
		return contextResolution{}, err
	}
	if session != "" {
		if err := team.ValidateSessionName(session); err != nil {
			return contextResolution{}, usageErrorf("resolved session %q is invalid: %v", session, err)
		}
	}
	handleCandidates := make([]contextCandidate, 0, len(candidates))
	for i := range candidates {
		candidate := candidates[i]
		if candidate.Field == "handle" && !contextCandidateTupleCompatible(candidate, profile, session) {
			markContextCandidateLosing(&candidate, fmt.Sprintf("tuple %q/%q conflicts with selected tuple %q/%q", candidate.tupleProfile, candidate.tupleSession, profile, session))
			candidates[i] = candidate
			continue
		}
		handleCandidates = append(handleCandidates, candidate)
	}
	handle, handleSource, err := selectOptionalHandleCandidate(handleCandidates)
	if err != nil {
		return contextResolution{}, err
	}

	// Root candidates are tuple-aware. A stale lower-precedence root for a
	// different profile/session remains visible in diagnostics but cannot win.
	compatible := make([]contextCandidate, 0, len(candidates)+4)
	for i := range candidates {
		candidate := candidates[i]
		if candidate.Field != "root" && candidate.Field != "base_root" {
			compatible = append(compatible, candidate)
			continue
		}
		if !contextCandidateTupleCompatible(candidate, profile, session) {
			markContextCandidateLosing(&candidate, fmt.Sprintf("tuple %q/%q conflicts with selected tuple %q/%q", candidate.tupleProfile, candidate.tupleSession, profile, session))
			candidates[i] = candidate
			continue
		}
		if candidate.Field == "root" && !contextRootMatches(candidate.Value, candidate.Source, projectDir, profile, session, launches, envContext, amqrc) {
			candidate.Detail = strings.TrimSpace(candidate.Detail + "; losing candidate for a different profile/session")
			candidates[i] = candidate
			continue
		}
		if candidate.Field == "base_root" && !contextBaseRootMatches(candidate.Value, candidate.Source, projectDir, profile, session, launches, envContext, amqrc) {
			candidate.Detail = strings.TrimSpace(candidate.Detail + "; losing candidate for a different profile/session")
			candidates[i] = candidate
			continue
		}
		compatible = append(compatible, candidate)
	}

	if amqrc.Root != "" {
		probe := contextCandidate{tupleProfile: squadnamespace.NormalizeProfile(amqrc.Profile), tupleSession: amqrc.Session, tupleBound: amqrc.Profile != "" || amqrc.Session != ""}
		eligible := contextCandidateTupleCompatible(probe, profile, session) && (profile == team.DefaultProfile || amqrc.Profile != "")
		if eligible {
			root := amqrc.Root
			if profile == team.DefaultProfile && session != "" {
				if _, configuredSession, ok := profileSessionFromAMQRoot(projectDir, root); !ok || configuredSession == "" {
					root = filepath.Join(root, session)
				}
			}
			addTupleContextCandidate(&compatible, "root", contextSourceAMQRC, root, amqrc.Path, profile, session)
			addTupleContextCandidate(&compatible, "base_root", contextSourceAMQRC, amqrc.BaseRoot, amqrc.Path, profile, session)
		} else {
			for _, field := range []struct{ name, value string }{{"root", amqrc.Root}, {"base_root", amqrc.BaseRoot}} {
				addTupleContextCandidate(&candidates, field.name, contextSourceAMQRC, field.value, amqrc.Path, amqrc.Profile, amqrc.Session)
				markContextCandidateLosing(&candidates[len(candidates)-1], fmt.Sprintf("tuple %q/%q conflicts with selected tuple %q/%q", amqrc.Profile, amqrc.Session, profile, session))
			}
		}
	}
	defaultRoot := filepath.Join(projectDir, ".agent-mail")
	if session != "" {
		defaultRoot = squadnamespace.AMQRoot(projectDir, profile, session)
	}
	defaultBase := filepath.Join(projectDir, ".agent-mail")
	if profile != team.DefaultProfile {
		defaultBase = defaultRoot
	}
	addContextCandidate(&compatible, "root", contextSourceDefault, defaultRoot, "profile/session storage contract")
	addContextCandidate(&compatible, "base_root", contextSourceDefault, defaultBase, "profile/session storage contract")

	root, rootSource, err := selectContextCandidate(compatible, "root")
	if err != nil {
		return contextResolution{}, err
	}
	baseRoot, baseSource, err := selectContextCandidate(compatible, "base_root")
	if err != nil {
		return contextResolution{}, err
	}
	if profile != team.DefaultProfile {
		baseRoot = root
		baseSource = rootSource
	}

	resolution.Profile = profile
	resolution.Session = session
	resolution.Handle = handle
	resolution.Root = filepath.Clean(root)
	resolution.BaseRoot = filepath.Clean(baseRoot)
	resolution.Sources["profile"] = profileSource
	resolution.Sources["session"] = sessionSource
	resolution.Sources["handle"] = handleSource
	resolution.Sources["root"] = rootSource
	resolution.Sources["base_root"] = baseSource
	resolution.PinMode = "sessionful"
	if profile != team.DefaultProfile {
		resolution.PinMode = "exact_root"
	}
	if err := validateResolvedContext(resolution); err != nil {
		return contextResolution{}, err
	}
	resolution.NamespaceGeneration, err = namespaceEndpointGeneration(projectDir, profile, session)
	if err != nil {
		return contextResolution{}, fmt.Errorf("resolve namespace generation: %w", err)
	}

	selected := map[string]struct {
		value, source string
	}{
		"profile":   {resolution.Profile, profileSource},
		"session":   {resolution.Session, sessionSource},
		"handle":    {resolution.Handle, handleSource},
		"root":      {resolution.Root, rootSource},
		"base_root": {resolution.BaseRoot, baseSource},
	}
	all := append(candidates, compatible...)
	seen := map[string]bool{}
	for _, candidate := range all {
		key := candidate.Field + "\x00" + candidate.Source + "\x00" + candidate.Value + "\x00" + candidate.Detail
		if seen[key] {
			continue
		}
		seen[key] = true
		if winner, ok := selected[candidate.Field]; ok {
			candidate.Selected = candidate.Value == winner.value && candidate.Source == winner.source
		}
		candidate.rank = 0
		resolution.Candidates = append(resolution.Candidates, candidate)
	}
	sort.SliceStable(resolution.Candidates, func(i, j int) bool {
		if resolution.Candidates[i].Field != resolution.Candidates[j].Field {
			return resolution.Candidates[i].Field < resolution.Candidates[j].Field
		}
		return contextSourceRank(resolution.Candidates[i].Source) < contextSourceRank(resolution.Candidates[j].Source)
	})
	return resolution, nil
}

func projectFromInjectedLaunch(cwd string) (string, bool) {
	root := absoluteAMQRoot(cwd, os.Getenv("AM_ROOT"))
	handle := strings.TrimSpace(os.Getenv("AM_ME"))
	if root == "" || handle == "" {
		return "", false
	}
	rec, err := launch.Read(filepath.Join(root, "agents", handle))
	if err != nil {
		return "", false
	}
	active := rec.AgentPID > 0 && contextPIDAlive(rec.AgentPID)
	pane := strings.TrimSpace(os.Getenv("TMUX_PANE"))
	if !active && pane != "" && rec.Tmux != nil && rec.Tmux.PaneID == pane {
		active = true
	}
	if !active {
		return "", false
	}
	for _, candidate := range []string{rec.TeamHome, rec.CWD} {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate, true
		}
	}
	return "", false
}

func readInjectedContext(projectDir string) (injectedContext, []string, error) {
	root, rootSet := os.LookupEnv("AM_ROOT")
	base, baseSet := os.LookupEnv("AM_BASE_ROOT")
	session, sessionSet := os.LookupEnv("AM_SESSION")
	handle, handleSet := os.LookupEnv("AM_ME")
	root, base, session, handle = strings.TrimSpace(root), strings.TrimSpace(base), strings.TrimSpace(session), strings.TrimSpace(handle)
	ctx := injectedContext{Handle: handle, Present: rootSet || baseSet || sessionSet || handleSet}
	if !rootSet && !baseSet && !sessionSet {
		return ctx, nil, nil
	}
	if root != "" {
		ctx.Root = absoluteAMQRoot(projectDir, root)
	}
	if base != "" {
		ctx.BaseRoot = absoluteAMQRoot(projectDir, base)
	}
	if rootSet != baseSet || root == "" || base == "" {
		return ctx, nil, usageErrorf("injected AMQ identity is incomplete (AM_ROOT and AM_BASE_ROOT must both be non-empty); stop and resume/relaunch the shell or pass explicit --profile and --session")
	}
	root, base = ctx.Root, ctx.BaseRoot
	ctx.Root, ctx.BaseRoot = root, base
	profile, inferredSession, underProject := profileSessionFromAMQRoot(projectDir, root)
	if underProject && inferredSession != "" {
		// Existing paths receive the legacy resolver's symlink-containment
		// validation. Fresh namespaces may not exist yet (#470); lexical parsing
		// is sufficient there because no on-disk link can rewrite the identity.
		if _, statErr := os.Stat(root); statErr == nil {
			if _, _, err := namedProfileFromInheritedAMQRoot(projectDir, inferredSession); err != nil {
				return ctx, nil, err
			}
		}
	}
	if sessionSet {
		if session == "" {
			return ctx, nil, usageErrorf("injected AMQ identity has an explicitly empty AM_SESSION; stop and resume/relaunch the shell")
		}
		if filepath.Clean(root) != filepath.Clean(filepath.Join(base, session)) {
			return ctx, nil, usageErrorf("injected AMQ identity is inconsistent: AM_ROOT %q is not AM_BASE_ROOT/AM_SESSION %q", root, filepath.Join(base, session))
		}
		if underProject && profile != team.DefaultProfile {
			return ctx, nil, usageErrorf("injected named-profile root %q must be exact-root/sessionless; AM_SESSION must be omitted", root)
		}
		ctx.Profile, ctx.Session, ctx.PinMode = profile, session, "sessionful"
		return ctx, nil, nil
	}
	if filepath.Clean(root) != filepath.Clean(base) {
		return ctx, nil, usageErrorf("injected exact-root identity is inconsistent: AM_ROOT %q must equal AM_BASE_ROOT %q when AM_SESSION is omitted", root, base)
	}
	if underProject {
		if profile == team.DefaultProfile && inferredSession != "" {
			return ctx, nil, usageErrorf("injected default-profile root %q is missing AM_SESSION=%q; stop and resume/relaunch the shell", root, inferredSession)
		}
		ctx.Profile, ctx.Session = profile, inferredSession
	}
	ctx.PinMode = "exact_root"
	return ctx, nil, nil
}

func explicitContextCanOverrideMalformedEnv(opts contextResolveOptions, projectDir string) bool {
	if opts.ProfileExplicit && opts.SessionExplicit && strings.TrimSpace(opts.ProfileFlag) != "" && strings.TrimSpace(opts.SessionFlag) != "" {
		return true
	}
	if !opts.RootExplicit || strings.TrimSpace(opts.RootFlag) == "" {
		return false
	}
	if opts.SessionExplicit && strings.TrimSpace(opts.SessionFlag) != "" {
		return true
	}
	_, session, ok := profileSessionFromAMQRoot(projectDir, absoluteAMQRoot(projectDir, opts.RootFlag))
	return ok && strings.TrimSpace(session) != ""
}

func profileSessionFromAMQRoot(projectDir, root string) (string, string, bool) {
	base := filepath.Join(filepath.Clean(projectDir), ".agent-mail")
	root = absoluteAMQRoot(projectDir, root)
	if profile, session, ok := profileSessionFromComparableRoots(base, root); ok {
		return profile, session, true
	}
	return profileSessionFromComparableRoots(canonicalContextComparisonPath(base), canonicalContextComparisonPath(root))
}

func profileSessionFromComparableRoots(base, root string) (string, string, bool) {
	rel, err := filepath.Rel(base, root)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", "", false
	}
	if rel == "." || rel == "" {
		return team.DefaultProfile, "", true
	}
	parts := strings.Split(filepath.Clean(rel), string(os.PathSeparator))
	switch len(parts) {
	case 1:
		return team.DefaultProfile, parts[0], true
	case 2:
		if parts[0] == team.DefaultProfile || team.ValidateProfileName(parts[0]) != nil {
			return "", "", false
		}
		return parts[0], parts[1], true
	default:
		return "", "", false
	}
}

// canonicalContextComparisonPath resolves symlinks in the longest existing
// prefix while preserving a not-yet-created suffix. macOS commonly reports
// cwd under /private/var while test/process environment paths use /var; those
// spellings must compare as one project without requiring a fresh AMQ root to
// exist. The original spelling remains in command output and diagnostics.
func canonicalContextComparisonPath(path string) string {
	path = filepath.Clean(path)
	current := path
	var suffix []string
	for {
		if resolved, err := filepath.EvalSymlinks(current); err == nil {
			for i := len(suffix) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, suffix[i])
			}
			return filepath.Clean(resolved)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return path
		}
		suffix = append(suffix, filepath.Base(current))
		current = parent
	}
}

func matchingLiveLaunchContexts(projectDir string, opts contextResolveOptions, env injectedContext) ([]launchContext, error) {
	entries, err := contextScanLaunchEntries(projectDir)
	if err != nil {
		return nil, fmt.Errorf("scan launch records: %w", err)
	}
	seen := map[string]bool{}
	addDirect := func(root, handle string) {
		if root == "" || handle == "" {
			return
		}
		agentDir := filepath.Join(root, "agents", handle)
		rec, err := launch.Read(agentDir)
		if err == nil && launchRecordMatchesProject(rec, projectDir) {
			entries = append(entries, launch.Entry{Record: rec, AgentDir: agentDir, Source: launch.FileName})
		}
	}
	addDirect(env.Root, env.Handle)
	pane := strings.TrimSpace(os.Getenv("TMUX_PANE"))
	var out []launchContext
	for _, entry := range entries {
		rec := entry.Record
		key := launch.ExistingPath(entry.AgentDir)
		if seen[key] {
			continue
		}
		seen[key] = true
		active := rec.AgentPID > 0 && contextPIDAlive(rec.AgentPID)
		if !active && pane != "" && rec.Tmux != nil && rec.Tmux.PaneID == pane {
			active = true
		}
		if !active {
			continue
		}
		out = append(out, launchContext{Record: rec, Path: launch.ExistingPath(entry.AgentDir)})
	}
	return out, nil
}

func launchRecordMatchesProject(rec launch.Record, projectDir string) bool {
	want := canonicalContextComparisonPath(projectDir)
	for _, candidate := range []string{rec.TeamHome, rec.CWD} {
		candidate = strings.TrimSpace(candidate)
		if candidate != "" && canonicalContextComparisonPath(candidate) == want {
			return true
		}
	}
	return false
}

func readProjectAMQRCContext(projectDir string) (amqrcContext, error) {
	path := filepath.Join(projectDir, ".amqrc")
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return amqrcContext{}, nil
	}
	if err != nil {
		return amqrcContext{}, fmt.Errorf("read %s: %w", path, err)
	}
	var cfg struct {
		Root string `json:"root"`
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return amqrcContext{}, usageErrorf("parse %s: %v", path, err)
	}
	root := absoluteAMQRoot(projectDir, cfg.Root)
	ctx := amqrcContext{Root: root, BaseRoot: root, Path: path}
	if profile, session, ok := profileSessionFromAMQRoot(projectDir, root); ok {
		ctx.Profile, ctx.Session = profile, session
		if session != "" && profile == team.DefaultProfile {
			ctx.BaseRoot = filepath.Dir(root)
		}
	}
	return ctx, nil
}

func defaultSessionForProfile(projectDir, profile string) (string, string) {
	if team.ExistsProfile(projectDir, profile) {
		if configured, err := team.ReadProfile(projectDir, profile); err == nil {
			if inferred := inferredSharedMemberSession(configured); inferred != "" {
				return inferred, "shared member session in team profile"
			}
			if pinned := strings.TrimSpace(configured.Workstream); pinned != "" {
				return pinned, "deprecated team workstream default"
			}
		}
	}
	return defaultWorkstreamName(projectDir), "sanitized project basename"
}

func contextRootMatches(root, source, projectDir, profile, session string, launches []launchContext, env injectedContext, amqrc amqrcContext) bool {
	root = absoluteAMQRoot(projectDir, root)
	if source == contextSourceFlags {
		return true
	}
	if inferredProfile, inferredSession, ok := profileSessionFromAMQRoot(projectDir, root); ok {
		return squadnamespace.ProfilesEqual(inferredProfile, profile) && (session == "" || inferredSession == "" || inferredSession == session)
	}
	if env.Root != "" && filepath.Clean(env.Root) == filepath.Clean(root) {
		if env.Profile == "" && !externalContextRootMatchesProject(root, projectDir, launches, amqrc) {
			return false
		}
		return (env.Profile == "" || squadnamespace.ProfilesEqual(env.Profile, profile)) && (env.Session == "" || session == "" || env.Session == session)
	}
	for _, live := range launches {
		if filepath.Clean(absoluteAMQRoot(projectDir, live.Record.Root)) == filepath.Clean(root) {
			return squadnamespace.ProfilesEqual(live.Record.TeamProfile, profile) && (session == "" || live.Record.Session == session)
		}
	}
	return profile == team.DefaultProfile
}

func contextBaseRootMatches(baseRoot, source, projectDir, profile, session string, launches []launchContext, env injectedContext, amqrc amqrcContext) bool {
	baseRoot = absoluteAMQRoot(projectDir, baseRoot)
	if source == contextSourceFlags {
		return true
	}
	if source == contextSourceEnv && filepath.Clean(env.BaseRoot) == filepath.Clean(baseRoot) {
		if env.Profile == "" && !externalContextRootMatchesProject(env.Root, projectDir, launches, amqrc) {
			return false
		}
		return (env.Profile == "" || squadnamespace.ProfilesEqual(env.Profile, profile)) && (env.Session == "" || session == "" || env.Session == session)
	}
	for _, live := range launches {
		if filepath.Clean(absoluteAMQRoot(projectDir, live.Record.BaseRoot)) == filepath.Clean(baseRoot) {
			return squadnamespace.ProfilesEqual(live.Record.TeamProfile, profile) && (session == "" || live.Record.Session == session)
		}
	}
	return source == contextSourceAMQRC && profile == team.DefaultProfile
}

func externalContextRootMatchesProject(root, projectDir string, launches []launchContext, amqrc amqrcContext) bool {
	root = canonicalContextComparisonPath(root)
	if configured := strings.TrimSpace(amqrc.Root); configured != "" {
		configured = canonicalContextComparisonPath(configured)
		if rel, err := filepath.Rel(configured, root); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			return true
		}
	}
	for _, live := range launches {
		if canonicalContextComparisonPath(absoluteAMQRoot(projectDir, live.Record.Root)) == root && launchRecordMatchesProject(live.Record, projectDir) {
			return true
		}
	}
	return false
}

func validateResolvedContext(ctx contextResolution) error {
	if ctx.Root == "" || ctx.BaseRoot == "" {
		return usageErrorf("could not resolve AMQ root/base root; pass explicit --project, --profile, and --session")
	}
	if ctx.Profile == team.DefaultProfile {
		if ctx.Session != "" && filepath.Clean(ctx.Root) != filepath.Clean(filepath.Join(ctx.BaseRoot, ctx.Session)) {
			return usageErrorf("resolved default-profile context is inconsistent: root %q is not base_root/session %q", ctx.Root, filepath.Join(ctx.BaseRoot, ctx.Session))
		}
		return nil
	}
	if filepath.Clean(ctx.Root) != filepath.Clean(ctx.BaseRoot) {
		return usageErrorf("resolved named-profile context is inconsistent: exact root %q must equal base root %q", ctx.Root, ctx.BaseRoot)
	}
	return nil
}

func runContext(args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Fprint(os.Stderr, `amq-squad context - explain canonical project/profile/session/root resolution

Usage:
  amq-squad context explain [--project DIR] [--profile NAME] [--session NAME]
                            [--me HANDLE] [--root DIR] [--base-root DIR] [--json]

Resolution precedence is explicit flags, injected environment, matching live
launch record, project .amqrc, then documented defaults.

An explicit --session without --profile deliberately keeps the selected
profile, but rejects the prior tuple's session, handle, root, and base root.
`)
		if len(args) == 0 {
			return usageErrorf("context requires the 'explain' subcommand")
		}
		return nil
	}
	if args[0] != "explain" {
		return usageErrorf("unknown 'context' subcommand %q; use 'context explain'", args[0])
	}
	return runContextExplain(args[1:])
}

func runContextExplain(args []string) error {
	fs := flag.NewFlagSet("context explain", flag.ContinueOnError)
	project := fs.String("project", "", "project/team-home directory (default: cwd)")
	profile := fs.String("profile", "", "team profile namespace")
	session := fs.String("session", "", "AMQ session/workstream")
	me := fs.String("me", "", "AMQ handle")
	root := fs.String("root", "", "explicit AMQ root")
	baseRoot := fs.String("base-root", "", "explicit AMQ base root")
	jsonOut := fs.Bool("json", false, "emit a schema-versioned context_explain envelope")
	registerScopedFlagAliases(fs, project, session, profile)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad context explain - show the resolved context and every candidate

Usage:
  amq-squad context explain [--project DIR] [--profile NAME] [--session NAME]
                            [--me HANDLE] [--root DIR] [--base-root DIR] [--json]

An explicit --session without --profile keeps the selected profile while
invalidating the prior tuple's session, handle, root, and base root.
`)
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	ctx, err := resolveCanonicalContext(contextResolveOptions{
		ProjectFlag: *project, ProfileFlag: *profile, SessionFlag: *session, HandleFlag: *me, RootFlag: *root, BaseRootFlag: *baseRoot,
		ProjectExplicit: flagWasSet(fs, "project"), ProfileExplicit: flagWasSet(fs, "profile"), SessionExplicit: flagWasSet(fs, "session"),
		HandleExplicit: flagWasSet(fs, "me"), RootExplicit: flagWasSet(fs, "root"), BaseRootExplicit: flagWasSet(fs, "base-root"),
		AllowMalformedEnv: true,
	})
	if err != nil {
		return err
	}
	if *jsonOut {
		return printJSONEnvelope("context_explain", ctx)
	}
	fmt.Println("# amq-squad context explain")
	for _, field := range []struct{ name, value string }{
		{"project", ctx.ProjectDir}, {"root", ctx.Root}, {"base_root", ctx.BaseRoot}, {"session", ctx.Session}, {"profile", ctx.Profile}, {"handle", ctx.Handle}, {"pin_mode", ctx.PinMode},
	} {
		source := ctx.Sources[field.name]
		if field.name == "pin_mode" {
			source = "derived"
		}
		fmt.Printf("%-10s %s (%s)\n", field.name+":", field.value, source)
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "FIELD\tSOURCE\tWINNER\tVALUE\tDETAIL")
	for _, candidate := range ctx.Candidates {
		winner := ""
		if candidate.Selected {
			winner = "yes"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", candidate.Field, candidate.Source, winner, candidate.Value, candidate.Detail)
	}
	if err := w.Flush(); err != nil {
		return err
	}
	for _, warning := range ctx.Warnings {
		fmt.Fprintf(os.Stderr, "warning: %s\n", warning)
	}
	return nil
}
