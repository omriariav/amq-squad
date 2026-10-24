package cli

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	squadnamespace "github.com/omriariav/amq-squad/v3/internal/namespace"
	"github.com/omriariav/amq-squad/v3/internal/team"
)

// Context resolution (2a-2 ruling 8, brief 2b-5): explicit flags win, then the
// injected AM_* identity (R52: the launch sets the complete identity), then
// the documented defaults, whose session comes from squad.yaml. The project
// .amqrc source and the candidate diagnostics are gone (squad.yaml replaces
// them); a stale injected tuple for another profile or session cannot be
// spliced into the selected one.
const (
	contextSourceFlags   = "explicit_flags"
	contextSourceEnv     = "injected_environment"
	contextSourceDefault = "documented_defaults"
)

type contextResolution struct {
	ProjectDir          string            `json:"project"`
	Root                string            `json:"root"`
	BaseRoot            string            `json:"base_root"`
	Session             string            `json:"session,omitempty"`
	Profile             string            `json:"profile"`
	Handle              string            `json:"handle,omitempty"`
	PinMode             string            `json:"pin_mode"`
	NamespaceGeneration string            `json:"namespace_generation,omitempty"`
	Sources             map[string]string `json:"sources"`
	Warnings            []string          `json:"warnings,omitempty"`
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

func resolveScopedCommandContext(projectFlag, profileFlag, sessionFlag, handleFlag string, fs *flag.FlagSet) (contextResolution, error) {
	return resolveCanonicalContext(contextResolveOptions{
		ProjectFlag: projectFlag, ProfileFlag: profileFlag, SessionFlag: sessionFlag, HandleFlag: handleFlag,
		ProjectExplicit: flagWasSet(fs, "project"), ProfileExplicit: flagWasSet(fs, "profile"),
		SessionExplicit: flagWasSet(fs, "session"), HandleExplicit: strings.TrimSpace(handleFlag) != "" && flagWasSet(fs, "me"),
	})
}

func emitContextDiagnostics(ctx contextResolution) {
	for _, warning := range ctx.Warnings {
		fmt.Fprintln(os.Stderr, "warning: context: "+warning)
	}
}

func resolveCanonicalContext(opts contextResolveOptions) (contextResolution, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return contextResolution{}, fmt.Errorf("getwd: %w", err)
	}
	projectDir, err := resolveProjectDirFlag(cwd, opts.ProjectFlag, opts.ProjectExplicit)
	if err != nil {
		return contextResolution{}, err
	}
	if projectDir, err = filepath.Abs(projectDir); err != nil {
		return contextResolution{}, fmt.Errorf("resolve project directory: %w", err)
	}
	r := contextResolution{ProjectDir: projectDir, Sources: map[string]string{"project": contextSourceDefault}}
	if opts.ProjectExplicit {
		r.Sources["project"] = contextSourceFlags
	}

	// explicit flags
	var flagProfile, flagSession, flagHandle string
	if opts.ProfileExplicit {
		if flagProfile, err = resolveProfileFlag(opts.ProfileFlag); err != nil {
			return contextResolution{}, err
		}
	}
	if opts.SessionExplicit {
		if flagSession = strings.TrimSpace(opts.SessionFlag); team.ValidateSessionName(flagSession) != nil {
			return contextResolution{}, usageErrorf("invalid --session: %v", team.ValidateSessionName(flagSession))
		}
	}
	if opts.HandleExplicit {
		if flagHandle = strings.TrimSpace(opts.HandleFlag); team.ValidateHandle(flagHandle) != nil {
			return contextResolution{}, usageErrorf("invalid --me: %v", team.ValidateHandle(flagHandle))
		}
	}

	// the injected identity; a malformed one is an error unless --profile and
	// --session select the whole tuple
	env, envWarnings, envErr := readInjectedContext(projectDir)
	r.Warnings = append(r.Warnings, envWarnings...)
	if envErr != nil {
		if !opts.ProfileExplicit || !opts.SessionExplicit || strings.TrimSpace(opts.ProfileFlag) == "" || strings.TrimSpace(opts.SessionFlag) == "" {
			return contextResolution{}, envErr
		}
		r.Warnings = append(r.Warnings, envErr.Error())
		env.Profile, env.Session, _ = profileSessionFromAMQRoot(projectDir, env.Root)
	}

	pick := func(field, flagValue, envValue string, envOK bool, fallback string) string {
		switch {
		case flagValue != "":
			r.Sources[field] = contextSourceFlags
			return flagValue
		case envOK && envValue != "":
			r.Sources[field] = contextSourceEnv
			return envValue
		case fallback != "":
			r.Sources[field] = contextSourceDefault
		}
		return fallback
	}
	profile := squadnamespace.NormalizeProfile(pick("profile", flagProfile, env.Profile, true, squadnamespace.DefaultProfile))
	if err := squadnamespace.ValidateProfile(profile); err != nil {
		return contextResolution{}, usageErrorf("resolved profile %q is invalid: %v", profile, err)
	}
	r.Profile = profile
	envProfileOK := env.Profile == "" || squadnamespace.ProfilesEqual(env.Profile, profile)
	session := pick("session", flagSession, env.Session, envProfileOK, defaultSessionForProfile(projectDir, profile))
	if session != "" && team.ValidateSessionName(session) != nil {
		return contextResolution{}, usageErrorf("resolved session %q is invalid: %v", session, team.ValidateSessionName(session))
	}
	// handle and roots come from the injected tuple only when it is the
	// selected tuple
	envTupleOK := envProfileOK && (env.Session == "" || session == "" || env.Session == session)
	envRootOK := envTupleOK && env.Profile != "" // the injected root lies under this project
	r.Session, r.Handle = session, pick("handle", flagHandle, env.Handle, envTupleOK, "")
	defaultRoot, defaultBase := filepath.Join(projectDir, ".agent-mail"), filepath.Join(projectDir, ".agent-mail")
	if session != "" {
		defaultRoot = squadnamespace.AMQRoot(projectDir, profile, session)
	}
	if profile != squadnamespace.DefaultProfile {
		defaultBase = defaultRoot
	}
	r.Root = filepath.Clean(pick("root", "", env.Root, envRootOK, defaultRoot))
	r.BaseRoot = filepath.Clean(pick("base_root", "", env.BaseRoot, envRootOK, defaultBase))
	r.PinMode = "sessionful"
	if profile != squadnamespace.DefaultProfile {
		r.BaseRoot, r.Sources["base_root"], r.PinMode = r.Root, r.Sources["root"], "exact_root"
	}
	if err := validateResolvedContext(r); err != nil {
		return contextResolution{}, err
	}
	r.NamespaceGeneration = namespaceGenerationNone
	return r, nil
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
		if underProject && profile != squadnamespace.DefaultProfile {
			return ctx, nil, usageErrorf("injected named-profile root %q must be exact-root/sessionless; AM_SESSION must be omitted", root)
		}
		ctx.Profile, ctx.Session, ctx.PinMode = profile, session, "sessionful"
		return ctx, nil, nil
	}
	if filepath.Clean(root) != filepath.Clean(base) {
		return ctx, nil, usageErrorf("injected exact-root identity is inconsistent: AM_ROOT %q must equal AM_BASE_ROOT %q when AM_SESSION is omitted", root, base)
	}
	if underProject {
		if profile == squadnamespace.DefaultProfile && inferredSession != "" {
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
		return squadnamespace.DefaultProfile, "", true
	}
	parts := strings.Split(filepath.Clean(rel), string(os.PathSeparator))
	switch len(parts) {
	case 1:
		return squadnamespace.DefaultProfile, parts[0], true
	case 2:
		if parts[0] == squadnamespace.DefaultProfile || squadnamespace.ValidateProfile(parts[0]) != nil {
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

// defaultSessionForProfile is the squad.yaml session (the shared seat
// session), else the sanitized project basename.
func defaultSessionForProfile(projectDir, profile string) string {
	if configured, err := team.ReadFor(projectDir, profile); err == nil {
		if inferred := inferredSharedMemberSession(configured); inferred != "" {
			return inferred
		}
		if s := strings.TrimSpace(configured.Session); s != "" {
			return s
		}
	}
	return defaultWorkstreamName(projectDir)
}

func validateResolvedContext(ctx contextResolution) error {
	if ctx.Root == "" || ctx.BaseRoot == "" {
		return usageErrorf("could not resolve AMQ root/base root; pass explicit --project, --profile, and --session")
	}
	if ctx.Profile == squadnamespace.DefaultProfile {
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

// resolveProfileFlag normalizes a --profile value: empty or "default" maps
// to the implicit default profile; non-default names are validated against
// the slug rules. Returns the canonical profile name and any error.
func resolveProfileFlag(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || name == squadnamespace.DefaultProfile {
		return squadnamespace.DefaultProfile, nil
	}
	if err := squadnamespace.ValidateProfile(name); err != nil {
		return "", fmt.Errorf("--profile: %w", err)
	}
	return name, nil
}
