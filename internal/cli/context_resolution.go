package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	squadnamespace "github.com/omriariav/amq-squad/v3/internal/namespace"
	"github.com/omriariav/amq-squad/v3/internal/team"
)

const (
	contextSourceFlags   = "explicit_flags"
	contextSourceEnv     = "injected_environment"
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

// contextResolveOptions are the flag inputs. 3.0 (2b-2): the launch-side
// inputs (--root and --base-root of the v2 agent launch, resume --last's
// project-only resolution, the malformed-environment override for launch)
// are gone: a seat's identity comes from its launch record only (R52).
type contextResolveOptions struct {
	ProjectFlag, ProfileFlag, SessionFlag, HandleFlag                 string
	ProjectExplicit, ProfileExplicit, SessionExplicit, HandleExplicit bool
}

type injectedContext struct {
	Root, BaseRoot, Session, Profile, Handle string
	PinMode                                  string
	Present                                  bool
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

func contextSourceRank(source string) int {
	switch source {
	case contextSourceFlags:
		return 0
	case contextSourceEnv:
		return 1
	case contextSourceAMQRC:
		return 2
	default:
		return 3
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
		remedy := "pass an explicit --" + contextFlagName(field)
		return "", "", usageErrorf("ambiguous %s at %s precedence; no winner; every candidate: %s; %s", field, contextSourceName(best), strings.Join(all, "; "), remedy)
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

func emitContextDiagnostics(ctx contextResolution) {
	for _, line := range contextDiagnosticLines(ctx) {
		fmt.Fprintln(os.Stderr, line)
	}
}

// contextFieldGenuinelyAmbiguous reports whether a field's candidates leave
// more than one distinct value tied at the best (lowest) source rank. A
// clean win by a higher-priority source (e.g. --profile over the project
// .amqrc default) is expected, deterministic resolution, not ambiguity.
func contextFieldGenuinelyAmbiguous(candidates []contextCandidate) bool {
	best := -1
	atBest := map[string]bool{}
	for _, candidate := range candidates {
		switch {
		case best == -1 || candidate.rank < best:
			best = candidate.rank
			atBest = map[string]bool{candidate.Value: true}
		case candidate.rank == best:
			atBest[candidate.Value] = true
		}
	}
	return len(atBest) > 1
}

func contextDiagnosticLines(ctx contextResolution) []string {
	verbose := outputPolicyCurrent().Verbose
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
		// Expected multi-source resolution (a higher-priority source cleanly
		// overriding lower-priority fallbacks) is normal for named
		// profiles/sessions; only surface it by default when the winner
		// itself is ambiguous (a tie at the best rank).
		if !verbose && !contextFieldGenuinelyAmbiguous(candidates) {
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
		out = append(out, fmt.Sprintf("warning: context %s candidates: %s; inspect with 'amq-squad doctor'", field, strings.Join(parts, "; ")))
	}
	for _, warning := range ctx.Warnings {
		out = append(out, "warning: context: "+warning)
	}
	return out
}

func contextSourceName(rank int) string {
	for _, source := range []string{contextSourceFlags, contextSourceEnv, contextSourceAMQRC, contextSourceDefault} {
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

	envContext, envWarnings, envErr := readInjectedContext(projectDir)
	resolution.Warnings = append(resolution.Warnings, envWarnings...)
	if envErr != nil {
		if !opts.ProfileExplicit || !opts.SessionExplicit || strings.TrimSpace(opts.ProfileFlag) == "" || strings.TrimSpace(opts.SessionFlag) == "" {
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
	handle, handleSource, err := selectContextCandidate(handleCandidates, "handle")
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
		if candidate.Field == "root" && !contextRootMatches(candidate.Value, candidate.Source, projectDir, profile, session, envContext, amqrc) {
			candidate.Detail = strings.TrimSpace(candidate.Detail + "; losing candidate for a different profile/session")
			candidates[i] = candidate
			continue
		}
		if candidate.Field == "base_root" && !contextBaseRootMatches(candidate.Value, candidate.Source, projectDir, profile, session, envContext, amqrc) {
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
	resolution.NamespaceGeneration = namespaceGenerationNone

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

// canonicalContextComparisonPath keeps context-root comparison on the shared
// pathnorm seam. That seam resolves symlinks and on-disk case in the longest
// existing prefix while preserving a not-yet-created suffix. The original
// spelling remains in command output and diagnostics.
func canonicalContextComparisonPath(path string) string {
	return canonicalFilesystemPath(path)
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

func contextRootMatches(root, source, projectDir, profile, session string, env injectedContext, amqrc amqrcContext) bool {
	root = absoluteAMQRoot(projectDir, root)
	if source == contextSourceFlags {
		return true
	}
	if inferredProfile, inferredSession, ok := profileSessionFromAMQRoot(projectDir, root); ok {
		return squadnamespace.ProfilesEqual(inferredProfile, profile) && (session == "" || inferredSession == "" || inferredSession == session)
	}
	if env.Root != "" && filepath.Clean(env.Root) == filepath.Clean(root) {
		if env.Profile == "" && !externalContextRootMatchesProject(root, amqrc) {
			return false
		}
		return (env.Profile == "" || squadnamespace.ProfilesEqual(env.Profile, profile)) && (env.Session == "" || session == "" || env.Session == session)
	}
	return profile == team.DefaultProfile
}

func contextBaseRootMatches(baseRoot, source, projectDir, profile, session string, env injectedContext, amqrc amqrcContext) bool {
	baseRoot = absoluteAMQRoot(projectDir, baseRoot)
	if source == contextSourceFlags {
		return true
	}
	if source == contextSourceEnv && filepath.Clean(env.BaseRoot) == filepath.Clean(baseRoot) {
		if env.Profile == "" && !externalContextRootMatchesProject(env.Root, amqrc) {
			return false
		}
		return (env.Profile == "" || squadnamespace.ProfilesEqual(env.Profile, profile)) && (env.Session == "" || session == "" || env.Session == session)
	}
	return source == contextSourceAMQRC && profile == team.DefaultProfile
}

func externalContextRootMatchesProject(root string, amqrc amqrcContext) bool {
	root = canonicalContextComparisonPath(root)
	if configured := strings.TrimSpace(amqrc.Root); configured != "" {
		configured = canonicalContextComparisonPath(configured)
		if rel, err := filepath.Rel(configured, root); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
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

// resolveProjectProfile resolves the --project and --profile flags (role draft, via
// resolveExistingTeamProfile).
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

// resolveExistingTeamProfile resolves the project dir + profile (reusing the
// shared resolver) and requires the profile to already exist — roster
// mutation needs a team to mutate.
func resolveExistingTeamProfile(projectFlag, profileFlag string, projectSet bool) (string, string, error) {
	projectDir, profile, err := resolveProjectProfile(projectFlag, profileFlag, projectSet)
	if err != nil {
		return "", "", err
	}
	if !team.ExistsProfile(projectDir, profile) {
		return "", "", fmt.Errorf("no team configured for profile %q. Run '%s' first.", profile, profileInitCommand(profile))
	}
	return projectDir, profile, nil
}

// resolveProfileFlag normalizes a --profile value: empty or "default" maps
// to the implicit default profile; non-default names are validated against
// the slug rules. Returns the canonical profile name and any error.
func resolveProfileFlag(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || name == team.DefaultProfile {
		return team.DefaultProfile, nil
	}
	if err := team.ValidateProfileName(name); err != nil {
		return "", fmt.Errorf("--profile: %w", err)
	}
	return name, nil
}
