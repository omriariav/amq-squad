package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/avivsinai/agent-message-queue/launchapi"
	"github.com/omriariav/amq-squad/v3/internal/adoptionseam"
	"github.com/omriariav/amq-squad/v3/internal/team"
	"github.com/omriariav/amq-squad/v3/internal/worktreeplan"
)

// doctorMinAMQVersion is the lowest AMQ release this build of amq-squad
// expects to interoperate with. Bumped manually when amq-squad starts to
// depend on newer AMQ behavior; the doctor check compares the running amq
// binary's reported version against this floor.
const doctorMinAMQVersion = "0.60.0"

type doctorStatus string

const (
	doctorOK   doctorStatus = "ok"
	doctorWarn doctorStatus = "warn"
	doctorFail doctorStatus = "fail"
)

// doctorCheck is one diagnostic result.
type doctorCheck struct {
	Name   string       `json:"name"`
	Status doctorStatus `json:"status"`
	Detail string       `json:"detail,omitempty"`
	Kind   string       `json:"kind,omitempty"`
	Role   string       `json:"role,omitempty"`
}

// doctorEnvelopeData is the kind="doctor" payload. team_home is the project
// root the report was scoped to. profile is the selected team profile.
// workstream is set only when that profile resolves one.
type doctorEnvelopeData struct {
	TeamHome   string                      `json:"team_home"`
	Profile    string                      `json:"profile,omitempty"`
	Workstream string                      `json:"workstream,omitempty"`
	Checks     []doctorCheck               `json:"checks"`
	Profiles   []doctorProfileEnvelopeData `json:"profiles,omitempty"`
}

// doctorProfileEnvelopeData is one per-profile doctor result when
// `doctor --all-profiles --json` is requested.
type doctorProfileEnvelopeData struct {
	Profile    string        `json:"profile"`
	Workstream string        `json:"workstream,omitempty"`
	Checks     []doctorCheck `json:"checks"`
}

// doctorExecution is the shared seam every doctor check reads from, so unit
// tests can supply fakes for `amq env` and the liveness probe without touching
// the real binaries.
type doctorExecution struct {
	ProjectDir     string
	Out            io.Writer
	JSON           bool
	ResolveAMQEnv  func(projectDir string) (amqEnv, error)
	RunAMQOps      func(projectDir string, env amqEnv) ([]byte, error)
	LookPath       func(name string) (string, error)
	Probe          duplicateLaunchProbe
	WorkstreamHint string
	Profile        string
	// Getenv reads process environment (injectable for tests). Defaults to os.Getenv.
	Getenv func(name string) string
	// LookupEnv preserves the distinction between an absent value and an
	// explicitly empty AM_SESSION. AMQ 0.42.1 introduced that distinction as
	// part of the injected identity contract; 0.60.0 is the supported floor.
	LookupEnv func(name string) (string, bool)
	// RunningVersion is the version of the binary executing `doctor` (threaded
	// from Run). Empty or "dev" for an unstamped local build.
	RunningVersion string
	// PathBinaryVersion resolves the `amq-squad` found on PATH and its reported
	// version. found=false when none is on PATH. Injectable so the version-skew
	// check never shells a real binary in tests.
	PathBinaryVersion func() (path, version string, found bool)
	// ResolveBaseRoot resolves the AMQ base root whose launch records are
	// treated as current.
	ResolveBaseRoot func(projectDir string) (string, error)
	// WorktreeDiagnostics provides the stable machine-readable isolation rows.
	// A nil seam emits skipped rows so unit tests never invoke a real Git binary.
	WorktreeDiagnostics func(t team.Team, profile, session string) ([]worktreeplan.Diagnostic, error)
}

func defaultDoctorExecution(projectDir string) doctorExecution {
	return doctorExecution{
		ProjectDir: projectDir,
		Out:        os.Stdout,
		ResolveAMQEnv: func(projectDir string) (amqEnv, error) {
			return resolveAMQEnvInDir(projectDir, "", "", "amq-squad")
		},
		RunAMQOps:           defaultDoctorAMQOps,
		LookPath:            exec.LookPath,
		Probe:               defaultDuplicateLaunchProbe,
		Getenv:              os.Getenv,
		LookupEnv:           os.LookupEnv,
		PathBinaryVersion:   defaultPathBinaryVersion,
		ResolveBaseRoot:     scanBaseRootForProject,
		WorktreeDiagnostics: defaultDoctorWorktreeDiagnostics,
	}
}

// defaultPathBinaryVersion resolves the `amq-squad` on PATH and runs
// `<path> version` to read its reported version (e.g. "amq-squad v2.0.0" ->
// "v2.0.0"). found=false when no amq-squad is on PATH. READ-ONLY.
func defaultPathBinaryVersion() (string, string, bool) {
	path, err := exec.LookPath("amq-squad")
	if err != nil {
		return "", "", false
	}
	out, err := exec.Command(path, "version").Output()
	if err != nil {
		return path, "", true
	}
	return path, parseAmqSquadVersion(string(out)), true
}

// parseAmqSquadVersion extracts the version token from `amq-squad version`
// output ("amq-squad v2.0.0" -> "v2.0.0"). Returns the trimmed last field, or
// the trimmed line when it has no spaces.
func parseAmqSquadVersion(out string) string {
	line := strings.TrimSpace(out)
	if i := strings.LastIndex(line, " "); i >= 0 {
		return strings.TrimSpace(line[i+1:])
	}
	return line
}

// doctorCheckVersionSkew warns when the `amq-squad` on PATH differs in version
// from the binary running `doctor`. amq-squad launches every agent into a shell
// that calls bare `amq-squad` (resolved via PATH), so if the operator runs a
// different build than what is on PATH, each spawned agent — and a lead's whole
// orchestration — silently uses the PATH version, not this one. (Exactly the
// 2.0.0-rc dogfood trap: agents inherited a 1.9.1 on PATH and lost the new
// team member/task primitives.)
func doctorCheckVersionSkew(d doctorExecution) doctorCheck {
	const name = "amq-squad on PATH"
	const install = "go install github.com/omriariav/amq-squad/v3/cmd/amq-squad@latest"

	// Skip (and do NOT shell out) for an unstamped dev/test build: the version
	// is only meaningful for an installed/released binary.
	running := strings.TrimSpace(d.RunningVersion)
	if running == "" || running == "dev" || running == "(devel)" {
		return doctorCheck{Name: name, Status: doctorOK,
			Detail: "running a dev/unstamped build; version-skew check skipped"}
	}
	resolve := d.PathBinaryVersion
	if resolve == nil {
		resolve = defaultPathBinaryVersion
	}
	path, pathVer, found := resolve()
	if !found {
		return doctorCheck{Name: name, Status: doctorWarn,
			Detail: "amq-squad is not on PATH; agents launched by amq-squad call bare `amq-squad`, which must be on PATH. Install: " + install}
	}
	if pathVer == "" {
		return doctorCheck{Name: name, Status: doctorWarn,
			Detail: fmt.Sprintf("could not read the version of amq-squad on PATH (%s); cannot confirm spawned agents use this build (%s)", path, running)}
	}
	if pathVer == running {
		return doctorCheck{Name: name, Status: doctorOK,
			Detail: fmt.Sprintf("matches this build (%s) at %s", running, path)}
	}
	return doctorCheck{Name: name, Status: doctorWarn,
		Detail: fmt.Sprintf("version skew: PATH amq-squad is %s (%s) but this process is %s; agents launched by amq-squad inherit the PATH binary, so orchestration uses %s. Reinstall to align: %s", pathVer, path, running, pathVer, install)}
}

func runDoctor(args []string, version string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "emit a schema-versioned doctor envelope instead of the human table")
	projectFlag := fs.String("project", "", "project/team-home directory to check (default: cwd)")
	profileFlag := fs.String("profile", "", "team profile to check (default: default profile)")
	sessionFlag := fs.String("session", "", "workstream whose registered worktrees should be checked")
	registerScopedFlagAliases(fs, projectFlag, sessionFlag, profileFlag)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad doctor - check this project's amq-squad / AMQ setup

Usage:
  amq-squad doctor [--project DIR] [--profile NAME] [--session NAME] [--json]

Checks: the AMQ version, launch API, adoption floor, module and identity pins,
AMQ ops diagnostics, the amq-squad on PATH vs this build (version skew), the
selected team profile, and the worktree plan of the selected workstream.
Read-only. Exits non-zero if any check is "fail".

Examples:
  amq-squad doctor
  amq-squad doctor --project ~/Code/app
  amq-squad doctor --profile review
  amq-squad doctor --json | jq '.data.checks[] | select(.status=="fail")'
`)
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return usageErrorf("doctor takes no positional arguments; got %d", fs.NArg())
	}
	ctx, err := resolveScopedCommandContext(*projectFlag, *profileFlag, *sessionFlag, "", fs)
	if err != nil {
		return err
	}
	emitContextDiagnostics(ctx)
	d := defaultDoctorExecution(ctx.ProjectDir)
	d.JSON = *jsonOut
	d.Profile = ctx.Profile
	d.WorkstreamHint = ctx.Session
	d.RunningVersion = version
	return executeDoctor(d)
}

func resolveProjectDirFlag(cwd, project string, explicit bool) (string, error) {
	if !explicit {
		return cwd, nil
	}
	project = strings.TrimSpace(project)
	if project == "" {
		return "", usageErrorf("--project requires a directory")
	}
	dir, err := expandPath(project)
	if err != nil {
		return "", fmt.Errorf("resolve --project: %w", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		return "", fmt.Errorf("--project %s: %w", dir, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("--project %s is not a directory", dir)
	}
	return dir, nil
}

func executeDoctor(d doctorExecution) error {
	checks, workstream := runDoctorChecks(d)
	if d.JSON {
		if err := writeJSONEnvelope(d.Out, "doctor", doctorEnvelopeData{
			TeamHome:   canonicalFilesystemPath(d.ProjectDir),
			Profile:    doctorProfile(d),
			Workstream: workstream,
			Checks:     checks,
		}); err != nil {
			return err
		}
	} else {
		writeDoctorTable(d.Out, checks)
	}
	if fails := countFails(checks); fails > 0 {
		return fmt.Errorf("doctor: %d check(s) failed", fails)
	}
	return nil
}

func countFails(checks []doctorCheck) int {
	n := 0
	for _, c := range checks {
		if c.Status == doctorFail {
			n++
		}
	}
	return n
}

func writeDoctorTable(out io.Writer, checks []doctorCheck) {
	policy := outputPolicyCurrent()
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "STATUS\tCHECK\tDETAIL")
	for _, c := range checks {
		detail := c.Detail
		if detail == "" {
			detail = "-"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", colorStatus(policy, string(c.Status)), c.Name, detail)
	}
	w.Flush()
	if policy.Verbose {
		fmt.Fprintf(out, "\nverbose: %d checks total (%d fail / %d non-ok)\n", len(checks), countFails(checks), countNonOK(checks))
	}
}

func countNonOK(checks []doctorCheck) int {
	n := 0
	for _, c := range checks {
		if c.Status != doctorOK {
			n++
		}
	}
	return n
}

func runDoctorChecks(d doctorExecution) ([]doctorCheck, string) {
	checks := []doctorCheck{}
	checks = append(checks, doctorCheckAMQVersion(d))
	checks = append(checks, doctorCheckLaunchapiCompatibility(d))
	checks = append(checks, doctorCheckAdoptionFloor(d))
	checks = append(checks, doctorCheckAMQModulePin(d))
	checks = append(checks, doctorCheckAMQIdentityPin(d))
	checks = append(checks, doctorCheckAMQOps(d))
	checks = append(checks, doctorCheckVersionSkew(d))
	checks = append(checks, doctorCheckTeamConfig(d))
	workstream := doctorWorkstream(d)
	checks = append(checks, doctorCheckWorktrees(d, workstream)...)
	return checks, workstream
}

// doctorWorkstream resolves the selected profile's workstream for the worktree
// checks ("" when the profile is missing or unresolvable). 3.0 (2a-2): this used
// to come out of the per-member wake checks, which are cut (manifest C15).
func doctorWorkstream(d doctorExecution) string {
	t, err := team.ReadProfile(d.ProjectDir, doctorProfile(d))
	if err != nil {
		return ""
	}
	workstream, err := resolveTeamWorkstreamName(t, d.WorkstreamHint, d.WorkstreamHint != "")
	if err != nil {
		return ""
	}
	return workstream
}

var worktreeDiagnosticKinds = []string{
	"store",
	"shared-index-collision",
	"duplicate-worktree-identity",
	"worktree-plan-drift",
	"worktree-handoff-cleanliness",
	"coordination-root-divergence",
	"registered-worktree-liveness",
}

func defaultDoctorWorktreeDiagnostics(t team.Team, profile, session string) ([]worktreeplan.Diagnostic, error) {
	service, err := worktreeplan.NewService(t, profile, session, worktreeplan.ExecGit{}, nil)
	if err != nil {
		return nil, err
	}
	inspection, err := service.Inspect()
	if err != nil {
		return nil, err
	}
	rows := doctorRuntimeRows(t, profile, session, defaultDuplicateLaunchProbe)
	inspection.Diagnostics = stateAwareSharedIndexDiagnostics(inspection, rows, profile, session)
	return inspection.Diagnostics, nil
}

// doctorRuntimeRows computes only the runtime state needed by the worktree
// collision check. Keeping this independent from buildStatusRows avoids
// recursively attaching worktree diagnostics back into doctor.
func doctorRuntimeRows(t team.Team, profile, session string, probe duplicateLaunchProbe) []statusRecord {
	var rows []statusRecord
	for _, member := range orderedTeamMembers(t.Members) {
		if team.EffectiveActorMode(t, member) != team.ActorModeImplementation {
			continue
		}
		cwd := member.EffectiveCWD(t.Project)
		env, err := resolveAMQEnvForTeamProfile(cwd, profile, session, member.Handle)
		if err != nil {
			rows = append(rows, statusRecord{
				Role: member.Role, Status: statusStateStale,
				Detail: fmt.Sprintf("runtime resolution for role %s failed: %v", member.Role, err),
			})
			continue
		}
		root := absoluteAMQRoot(cwd, env.Root)
		handle := member.Handle
		if env.Me != "" {
			handle = env.Me
		}
		live := classifyAgentLivenessFromDisk(filepath.Join(root, "agents", handle), root, profile, handle, member.Binary, probe)
		rows = append(rows, statusRecord{Role: member.Role, Status: live.Status})
	}
	return rows
}

func stateAwareSharedIndexDiagnostics(inspection worktreeplan.Inspection, rows []statusRecord, profile, session string) []worktreeplan.Diagnostic {
	if inspection.Set.SharedCWDException.Enabled {
		return inspection.Diagnostics
	}
	liveRoles := map[string]bool{}
	var runtimeUncertainty []string
	for _, row := range rows {
		if row.Status == statusStateLive {
			liveRoles[strings.ToLower(strings.TrimSpace(row.Role))] = true
		}
		if strings.TrimSpace(row.Detail) != "" {
			runtimeUncertainty = append(runtimeUncertainty, strings.TrimSpace(row.Detail))
		}
	}
	allByIndex := map[string][]string{}
	liveByIndex := map[string][]string{}
	for _, member := range inspection.Members {
		if member.State == worktreeplan.StateCleaned || strings.TrimSpace(member.Index) == "" {
			continue
		}
		allByIndex[member.Index] = append(allByIndex[member.Index], member.Role)
		if liveRoles[strings.ToLower(strings.TrimSpace(member.Role))] {
			liveByIndex[member.Index] = append(liveByIndex[member.Index], member.Role)
		}
	}
	allCollisions := sharedIndexCollisionDetails(allByIndex)
	if len(allCollisions) == 0 {
		return inspection.Diagnostics
	}
	liveCollisions := sharedIndexCollisionDetails(liveByIndex)
	remedy := fmt.Sprintf(
		"remedy before concurrent mutation: run 'amq-squad worktree plan --role R --task ID --base SHA --scope PATH... --profile %s --session %s', then 'amq-squad worktree materialize --role R --task ID --base SHA --scope PATH... --profile %s --session %s --yes'",
		profile, session, profile, session,
	)
	replacement := worktreeplan.Diagnostic{
		Kind: "shared-index-collision", Status: worktreeplan.DiagnosticWarn,
		Detail: strings.Join(allCollisions, "; ") + "; fewer than two affected members are live, so this warning does not fail doctor; " + remedy,
	}
	if len(liveCollisions) > 0 {
		replacement.Status = worktreeplan.DiagnosticFail
		replacement.Detail = strings.Join(liveCollisions, "; ") + " while live; " + remedy
	}
	if len(runtimeUncertainty) > 0 {
		sort.Strings(runtimeUncertainty)
		replacement.Detail += "; runtime liveness uncertainty: " + strings.Join(runtimeUncertainty, "; ")
	}
	out := append([]worktreeplan.Diagnostic(nil), inspection.Diagnostics...)
	for i := range out {
		if out[i].Kind == "shared-index-collision" {
			out[i] = replacement
			return out
		}
	}
	return append(out, replacement)
}

func sharedIndexCollisionDetails(byIndex map[string][]string) []string {
	var collisions []string
	for index, roles := range byIndex {
		if len(roles) < 2 {
			continue
		}
		sort.Strings(roles)
		collisions = append(collisions, strings.Join(roles, ",")+" share "+index)
	}
	sort.Strings(collisions)
	return collisions
}

func doctorCheckWorktrees(d doctorExecution, workstream string) []doctorCheck {
	if strings.TrimSpace(workstream) == "" {
		return skippedWorktreeDoctorChecks("workstream unresolved; skipped")
	}
	t, err := team.ReadProfile(d.ProjectDir, doctorProfile(d))
	if err != nil {
		return skippedWorktreeDoctorChecks("team config unavailable; skipped")
	}
	if d.WorktreeDiagnostics == nil {
		return skippedWorktreeDoctorChecks("worktree diagnostic seam unavailable; skipped")
	}
	diagnostics, err := d.WorktreeDiagnostics(t, doctorProfile(d), workstream)
	if err != nil {
		checks := skippedWorktreeDoctorChecks("worktree inspection unavailable; skipped")
		checks[0].Status = doctorFail
		checks[0].Detail = err.Error()
		if strings.Contains(strings.ToLower(err.Error()), "duplicate") {
			checks[2].Status = doctorFail
			checks[2].Detail = err.Error()
		}
		return checks
	}
	byKind := map[string]worktreeplan.Diagnostic{}
	for _, diagnostic := range diagnostics {
		byKind[diagnostic.Kind] = diagnostic
	}
	checks := make([]doctorCheck, 0, len(worktreeDiagnosticKinds))
	for _, kind := range worktreeDiagnosticKinds {
		diagnostic, ok := byKind[kind]
		if !ok {
			checks = append(checks, doctorCheck{Name: "worktree/" + kind, Kind: kind, Status: doctorFail, Detail: "diagnostic row missing"})
			continue
		}
		status := doctorOK
		switch diagnostic.Status {
		case worktreeplan.DiagnosticWarn:
			status = doctorWarn
		case worktreeplan.DiagnosticFail:
			status = doctorFail
		}
		checks = append(checks, doctorCheck{
			Name: "worktree/" + kind, Kind: kind, Role: diagnostic.Role,
			Status: status, Detail: diagnostic.Detail,
		})
	}
	return checks
}

func skippedWorktreeDoctorChecks(detail string) []doctorCheck {
	checks := make([]doctorCheck, 0, len(worktreeDiagnosticKinds))
	for _, kind := range worktreeDiagnosticKinds {
		checks = append(checks, doctorCheck{Name: "worktree/" + kind, Kind: kind, Status: doctorOK, Detail: detail})
	}
	return checks
}

func defaultDoctorAMQOps(projectDir string, env amqEnv) ([]byte, error) {
	root := absoluteAMQRoot(projectDir, env.Root)
	ctx := amqContext{
		ProjectDir: projectDir,
		Env:        env,
		Root:       root,
		Me:         env.Me,
	}
	return runAMQCommand(amqCommandRequest{
		Dir: projectDir,
		Env: amqCommandEnv(ctx),
		Arg: []string{"doctor", "--ops", "--json"},
	})
}

func doctorProfile(d doctorExecution) string {
	if strings.TrimSpace(d.Profile) == "" {
		return team.DefaultProfile
	}
	return strings.TrimSpace(d.Profile)
}

func doctorCheckAMQVersion(d doctorExecution) doctorCheck {
	env, err := d.ResolveAMQEnv(d.ProjectDir)
	if err != nil {
		return doctorCheck{
			Name:   "amq version",
			Status: doctorFail,
			Detail: fmt.Sprintf("amq env failed: %v", err),
		}
	}
	got := strings.TrimSpace(env.AMQVersion)
	if got == "" {
		return doctorCheck{
			Name:   "amq version",
			Status: doctorFail,
			Detail: "amq env returned no version (compatibility unknown)",
		}
	}
	parsed, ok := parseSemverParts(got)
	if !ok {
		return doctorCheck{
			Name:   "amq version",
			Status: doctorFail,
			Detail: fmt.Sprintf("amq returned unparseable version %q", got),
		}
	}
	min, _ := parseSemverParts(doctorMinAMQVersion)
	if compareSemverParts(parsed, min) < 0 || (compareSemverParts(parsed, min) == 0 && semverHasPrerelease(got)) {
		return doctorCheck{
			Name:   "amq version",
			Status: doctorFail,
			Detail: fmt.Sprintf("amq %s is older than required %s; run `amq upgrade` to update", got, doctorMinAMQVersion),
		}
	}
	return doctorCheck{
		Name:   "amq version",
		Status: doctorOK,
		Detail: fmt.Sprintf("amq %s (min %s)", got, doctorMinAMQVersion),
	}
}

// doctorCheckLaunchapiCompatibility reports the launchapi contract this
// build was compiled against (gh#766). launchapi.Compatibility() is a pure,
// static function -- no I/O, no amq env resolution -- so this check can
// never fail; it exists purely to surface the negotiated contract surface
// (ContractSemver, Features) alongside `amq doctor`'s own version report.
func doctorCheckLaunchapiCompatibility(d doctorExecution) doctorCheck {
	compat := launchapi.Compatibility()
	// Detail intentionally reports the feature COUNT, not the raw feature
	// name strings: those are internal launchapi negotiation keys (e.g.
	// "managed_tmux_v1"), not operator-meaningful text, and several literal
	// substring collisions with other doctor checks' own bare-word matching
	// in tests (e.g. "tmux") -- caught by TestExecuteDoctorTmuxMissingFails.
	return doctorCheck{
		Name:   "launchapi compatibility",
		Status: doctorOK,
		Detail: fmt.Sprintf("contract %s (%d negotiable feature(s))", compat.ContractSemver, len(compat.Features)),
	}
}

// pinnedAMQModulePath is the launchapi module amq-squad's go.mod pins.
const pinnedAMQModulePath = "github.com/avivsinai/agent-message-queue"

// pinnedAMQModuleVersion resolves the linked agent-message-queue module
// version via runtime/debug.ReadBuildInfo(). A package var so tests can
// stub it: `go test -c` binaries in this toolchain embed zero dependency
// entries (see adoptionseam's TestPinnedAMQModuleAtOrAboveAdoptionFloor doc
// comment for the confirmed empirical finding across packages), so a real
// ReadBuildInfo call inside `go test` would always report not-found
// regardless of what is actually pinned. A normally built/installed binary
// (not a test binary) does carry the real Deps list.
var pinnedAMQModuleVersion = defaultPinnedAMQModuleVersion

func defaultPinnedAMQModuleVersion() (string, bool) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "", false
	}
	for _, dep := range info.Deps {
		if dep.Path == pinnedAMQModulePath {
			return dep.Version, true
		}
	}
	return "", false
}

// doctorCheckAdoptionFloor warns (never fails) when the PATH `amq` binary
// reports a version below adoptionseam.AdoptionFloorAMQVersion -- the
// version the launchapi backend's negotiated contract was verified against
// (gh#766). This is advisory: the actual enforcement for the in-process
// launchapi path is the go.mod pin itself (adoptionseam's
// TestPinnedAMQModuleAtOrAboveAdoptionFloor), since this seam never shells
// out to the amq CLI. An old PATH amq only matters to the legacy
// composer/wake path, which doctorCheckAMQVersion's doctorMinAMQVersion
// already floors separately (a lower, distinct requirement).
func doctorCheckAdoptionFloor(d doctorExecution) doctorCheck {
	const name = "amq vs launchapi adoption floor"
	env, err := d.ResolveAMQEnv(d.ProjectDir)
	if err != nil || strings.TrimSpace(env.AMQVersion) == "" {
		return doctorCheck{Name: name, Status: doctorOK, Detail: "amq version unavailable; see 'amq version' check"}
	}
	got := strings.TrimSpace(env.AMQVersion)
	parsed, ok := parseSemverParts(got)
	if !ok {
		return doctorCheck{Name: name, Status: doctorOK, Detail: fmt.Sprintf("amq returned unparseable version %q; see 'amq version' check", got)}
	}
	floor, _ := parseSemverParts(adoptionseam.AdoptionFloorAMQVersion)
	if compareSemverParts(parsed, floor) < 0 {
		return doctorCheck{Name: name, Status: doctorWarn,
			Detail: fmt.Sprintf("PATH amq %s is below the launchapi adoption floor %s; the launchapi launch backend was verified against %s and later -- older amq releases are untested for it (advisory only, does not block legacy operation)", got, adoptionseam.AdoptionFloorAMQVersion, adoptionseam.AdoptionFloorAMQVersion)}
	}
	return doctorCheck{Name: name, Status: doctorOK, Detail: fmt.Sprintf("amq %s meets the launchapi adoption floor %s", got, adoptionseam.AdoptionFloorAMQVersion)}
}

// doctorCheckAMQModulePin warns (never fails) when the PATH `amq` binary
// reports a version newer than the github.com/avivsinai/agent-message-queue
// module amq-squad's go.mod pins (gh#766). The launchapi backend runs
// in-process against the pinned module, not the PATH amq binary, so a newer
// PATH amq cannot make the launchapi path use newer behavior either way --
// this is purely a heads-up that amq-squad's own launchapi integration has
// not yet been verified against what the operator has installed.
func doctorCheckAMQModulePin(d doctorExecution) doctorCheck {
	const name = "amq vs pinned launchapi module"
	env, err := d.ResolveAMQEnv(d.ProjectDir)
	if err != nil || strings.TrimSpace(env.AMQVersion) == "" {
		return doctorCheck{Name: name, Status: doctorOK, Detail: "amq version unavailable; see 'amq version' check"}
	}
	got := strings.TrimSpace(env.AMQVersion)
	parsed, ok := parseSemverParts(got)
	if !ok {
		return doctorCheck{Name: name, Status: doctorOK, Detail: fmt.Sprintf("amq returned unparseable version %q; see 'amq version' check", got)}
	}
	resolve := pinnedAMQModuleVersion
	if resolve == nil {
		resolve = defaultPinnedAMQModuleVersion
	}
	pinned, found := resolve()
	if !found {
		return doctorCheck{Name: name, Status: doctorOK, Detail: "pinned launchapi module version unavailable (unstamped/test build); check skipped"}
	}
	pinnedParsed, ok := parseSemverParts(pinned)
	if !ok {
		return doctorCheck{Name: name, Status: doctorOK, Detail: fmt.Sprintf("pinned module version %q unparseable; check skipped", pinned)}
	}
	if compareSemverParts(parsed, pinnedParsed) > 0 {
		return doctorCheck{Name: name, Status: doctorWarn,
			Detail: fmt.Sprintf("PATH amq %s is newer than the %s module this build pins (%s); amq-squad's launchapi integration has not been verified against %s", got, pinnedAMQModulePath, pinned, got)}
	}
	return doctorCheck{Name: name, Status: doctorOK, Detail: fmt.Sprintf("amq %s is at or below the pinned launchapi module version %s", got, pinned)}
}

func doctorCheckAMQIdentityPin(d doctorExecution) doctorCheck {
	lookup := d.LookupEnv
	if lookup == nil {
		lookup = func(name string) (string, bool) {
			if d.Getenv == nil {
				return "", false
			}
			value := d.Getenv(name)
			return value, value != ""
		}
	}
	root, rootOK := lookup("AM_ROOT")
	base, baseOK := lookup("AM_BASE_ROOT")
	session, sessionOK := lookup("AM_SESSION")
	me, meOK := lookup("AM_ME")
	if !rootOK && !baseOK && !sessionOK && !meOK {
		return doctorCheck{Name: "amq identity pin", Status: doctorOK, Detail: "no injected AMQ agent-shell identity"}
	}
	root = strings.TrimSpace(root)
	base = strings.TrimSpace(base)
	session = strings.TrimSpace(session)
	me = strings.TrimSpace(me)
	if rootOK && baseOK && meOK && root != "" && base != "" && me != "" {
		if !sessionOK && sameResolvedDir(root, base) {
			return doctorCheck{Name: "amq identity pin", Status: doctorOK, Detail: "healthy exact-root/sessionless pin (AM_ROOT=AM_BASE_ROOT; AM_SESSION omitted; AM_ME set)"}
		}
		if sessionOK && session == "" {
			return doctorCheck{Name: "amq identity pin", Status: doctorWarn, Detail: "legacy or inconsistent injected AMQ identity pin (AM_SESSION is present but empty); stop and resume/relaunch the agent shell after upgrading to amq 0.60.0; a child command cannot repair its parent shell"}
		}
		if session != "" && sameResolvedDir(root, filepath.Join(base, session)) {
			return doctorCheck{Name: "amq identity pin", Status: doctorOK, Detail: "healthy sessionful pin (AM_ROOT=AM_BASE_ROOT/AM_SESSION; AM_ME set)"}
		}
	}
	sessionState := "absent"
	if sessionOK {
		sessionState = "empty"
		if session != "" {
			sessionState = "set"
		}
	}
	shape := fmt.Sprintf("AM_ROOT=%t AM_BASE_ROOT=%t AM_SESSION=%s AM_ME=%t", rootOK, baseOK, sessionState, meOK)
	return doctorCheck{
		Name:   "amq identity pin",
		Status: doctorWarn,
		Detail: "legacy or inconsistent injected AMQ identity pin (" + shape + "); stop and resume/relaunch the agent shell after upgrading to amq 0.60.0; a child command cannot repair its parent shell",
	}
}

func doctorCheckAMQOps(d doctorExecution) doctorCheck {
	if d.RunAMQOps == nil {
		return doctorCheck{
			Name:   "amq ops",
			Status: doctorWarn,
			Detail: "amq doctor --ops check unavailable",
		}
	}
	env, err := d.ResolveAMQEnv(d.ProjectDir)
	if err != nil {
		return doctorCheck{
			Name:   "amq ops",
			Status: doctorFail,
			Detail: fmt.Sprintf("amq env failed: %v", err),
		}
	}
	if _, err := d.RunAMQOps(d.ProjectDir, env); err != nil {
		return doctorCheck{
			Name:   "amq ops",
			Status: doctorFail,
			Detail: fmt.Sprintf("amq doctor --ops failed: %v", err),
		}
	}
	return doctorCheck{
		Name:   "amq ops",
		Status: doctorOK,
		Detail: "amq doctor --ops ok",
	}
}

func doctorCheckTeamConfig(d doctorExecution) doctorCheck {
	profile := doctorProfile(d)
	path := team.ProfilePath(d.ProjectDir, profile)
	if !team.ExistsProfile(d.ProjectDir, profile) {
		if profile == team.DefaultProfile {
			if profiles, err := team.ListProfiles(d.ProjectDir); err == nil && len(profiles) > 0 {
				return doctorCheck{
					Name:   "team config",
					Status: doctorWarn,
					Detail: "no default team profile; configured profiles: " + strings.Join(profiles, ", ") + " (run 'amq-squad doctor --profile <name>')",
				}
			}
		}
		return doctorCheck{
			Name:   "team config",
			Status: doctorWarn,
			Detail: fmt.Sprintf("no team profile %q (run '%s')", profile, profileInitCommand(profile)),
		}
	}
	if _, err := team.ReadProfile(d.ProjectDir, profile); err != nil {
		return doctorCheck{
			Name:   "team config",
			Status: doctorFail,
			Detail: fmt.Sprintf("invalid %s: %v", filepath.Base(path), err),
		}
	}
	return doctorCheck{
		Name:   "team config",
		Status: doctorOK,
		Detail: path,
	}
}

// compareSemverParts returns -1, 0, or 1 comparing a to b.
func compareSemverParts(a, b [3]int) int {
	for i := 0; i < 3; i++ {
		if a[i] < b[i] {
			return -1
		}
		if a[i] > b[i] {
			return 1
		}
	}
	return 0
}

// parseSemverParts parses a major.minor.patch version string. Accepts a
// leading "v" and strips "-pre" / "+build" suffixes. Returns ok=false when
// no numeric major/minor/patch component is present so the caller can flag
// the version as unparseable.
func parseSemverParts(s string) ([3]int, bool) {
	var out [3]int
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	for _, sep := range []string{"-", "+"} {
		if idx := strings.Index(s, sep); idx >= 0 {
			s = s[:idx]
		}
	}
	if s == "" {
		return out, false
	}
	parts := strings.SplitN(s, ".", 3)
	for i := 0; i < 3; i++ {
		if i >= len(parts) {
			return out, false
		}
		n, err := strconv.Atoi(parts[i])
		if err != nil {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

func semverHasPrerelease(version string) bool {
	version = strings.TrimPrefix(strings.TrimSpace(version), "v")
	if build := strings.IndexByte(version, '+'); build >= 0 {
		version = version[:build]
	}
	return strings.Contains(version, "-")
}

// semverMeetsStableFloor compares a possibly-prerelease version against a
// stable minimum. A prerelease with the same core is older than the stable
// release (0.49.0-rc1 < 0.49.0), while a prerelease with a higher core remains
// newer (0.49.1-rc1 > 0.49.0).
func semverMeetsStableFloor(version, minimum string) bool {
	got, ok := parseSemverParts(strings.TrimSpace(version))
	if !ok {
		return false
	}
	min, ok := parseSemverParts(strings.TrimSpace(minimum))
	if !ok {
		return false
	}
	switch compareSemverParts(got, min) {
	case -1:
		return false
	case 1:
		return true
	default:
		return !semverHasPrerelease(version)
	}
}
