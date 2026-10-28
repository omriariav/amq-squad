package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/avivsinai/agent-message-queue/launchapi"
	"github.com/omriariav/amq-squad/v3/internal/adoptionseam"
	"github.com/omriariav/amq-squad/v3/internal/registry"
)

// doctorMinAMQVersion is the lowest AMQ release this build of amq-squad
// runs with: the release of the module go.mod pins (5-amq-A fix round, one
// floor for one binary). Every 3.0 path needs that release's launch
// adapters (the pi mint adapter, the Codex sandbox pins) and its mailbox.
// The amq env check, the installed-binary rows (doctor_amq_floor.go) and
// the real-AMQ CI lane's pinned version all read this one value.
const doctorMinAMQVersion = "0.116.2"

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
}

// doctorEnvelopeData is the kind="doctor" payload. team_home is the project
// root the report was scoped to (the repository whose Codex trust is checked).
type doctorEnvelopeData struct {
	TeamHome string        `json:"team_home"`
	Checks   []doctorCheck `json:"checks"`
}

// doctorExecution is the shared seam every doctor check reads from, so unit
// tests can supply fakes for `amq env` and the liveness probe without touching
// the real binaries.
type doctorExecution struct {
	ProjectDir    string
	Out           io.Writer
	JSON          bool
	ResolveAMQEnv func(projectDir string) (amqEnv, error)
	RunAMQOps     func(projectDir string, env amqEnv) ([]byte, error)
	LookPath      func(name string) (string, error)
	Probe         duplicateLaunchProbe
	// Getenv reads process environment (injectable for tests). Defaults to os.Getenv.
	Getenv func(name string) string
	// LookupEnv preserves the distinction between an absent value and an
	// explicitly empty AM_SESSION. AMQ 0.42.1 introduced that distinction as
	// part of the injected identity contract; 0.60.0 is the supported floor.
	LookupEnv func(name string) (string, bool)
	// RunningVersion is the version of the binary executing `doctor` (threaded
	// from Run). Empty or "dev" for an unstamped local build.
	RunningVersion string
	// Fix lets the pi runtime rows repair (doctor --fix, brief 4-1).
	Fix bool
	// ReleaseDrift, when set, adds the tag and release checks (3-2).
	ReleaseDrift func() []doctorCheck
	// PathBinaryVersion resolves the `amq-squad` found on PATH and its reported
	// version. found=false when none is on PATH. Injectable so the version-skew
	// check never shells a real binary in tests.
	PathBinaryVersion func() (path, version string, found bool)
	// ToolVersion runs `<name> --version` for the installed AMQ floor rows
	// (brief 5-amq-A); nil skips them, so unit tests never shell a real amq.
	ToolVersion func(name string) (path, out string, found bool, err error)
	// Host is the plan 16.2 probe of the host (doctor_host.go). A nil Host
	// skips the host rows, so unit tests never probe the real machine.
	Host *doctorHost
}

func defaultDoctorExecution(projectDir string) doctorExecution {
	return doctorExecution{
		ProjectDir: projectDir,
		Out:        os.Stdout,
		ResolveAMQEnv: func(projectDir string) (amqEnv, error) {
			return resolveAMQEnvInDir(projectDir, "", "", "amq-squad")
		},
		RunAMQOps:         defaultDoctorAMQOps,
		LookPath:          exec.LookPath,
		Probe:             defaultDuplicateLaunchProbe,
		Getenv:            os.Getenv,
		LookupEnv:         os.LookupEnv,
		PathBinaryVersion: defaultPathBinaryVersion,
		ToolVersion:       defaultToolVersion,
		Host:              defaultDoctorHost(),
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
	projectFlag := fs.String("project", "", "repository to check for Codex trust (default: cwd)")
	writeRegistry := fs.Bool("write-registry", false, "write ~/.amq-squad/registry.yaml from this probe")
	probe := fs.Bool("probe", false, "also run the slow probes: R12 tool-call and edit probe per local model, direct TTFT, Codex model ids (implies --write-registry)")
	runs := fs.Int("runs", registry.ShipN, "R12 probe runs per local model with --probe")
	usageScan := fs.Duration("usage-scan", 0, "also list the Claude and Codex session files that carried a usage-limit or 429 signal in this window (e.g. 168h)")
	fixFlag := fs.Bool("fix", false, "repair: remove a rejected pi runtime and runtimes beyond the last two")
	registerScopedFlagAliases(fs, projectFlag, nil, nil)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad doctor - probe this host for amq-squad 3.0 (plan 5 and 16.2)

Usage:
  amq-squad doctor [--project DIR] [--json] [--write-registry] [--probe [--runs N]] [--usage-scan DUR] [--fix]

Checks: the AMQ version and launch API, the amq-squad on PATH vs this build,
the AMQ identity pin and ops; then the tools on PATH (claude, codex, the
built-in pi, grok, cursor-agent, ollama, amq, amq-remote, tmux, gh) with their
versions against the versions the AMQ launch API verified and their login
state, RAM and GPU, the registry entries, the full re-hash of the embedded pi
runtime, sleep and the amq-remote login service, project roots codex-cli does
not trust, the Codex notify fan-out line, exported key names in the shell
profile, the owner's global agent config against the preflight copy, and the
grants file mode. A missing optional tool is one status line, not a failure.
Without --write-registry, --probe or --fix (pi runtime repair) doctor writes nothing. Exits non-zero if
any check is "fail".

Examples:
  amq-squad doctor
  amq-squad doctor --project ~/Code/app
  amq-squad doctor --write-registry
  amq-squad doctor --probe --runs 5
  amq-squad doctor --usage-scan 168h
  amq-squad doctor --json | jq '.data.checks[] | select(.status=="fail")'
`)
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return usageErrorf("doctor takes no positional arguments; got %d", fs.NArg())
	}
	if *runs < 1 {
		return usageErrorf("--runs must be at least 1")
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	projectDir, err := resolveProjectDirFlag(cwd, *projectFlag, flagWasSet(fs, "project") || flagWasSet(fs, "p"))
	if err != nil {
		return err
	}
	d := defaultDoctorExecution(projectDir)
	d.JSON = *jsonOut
	d.RunningVersion = version
	d.Host.WriteRegistry = *writeRegistry || *probe
	d.Host.Probe = *probe
	d.Host.ProbeRuns = *runs
	d.Host.UsageScan = *usageScan
	d.Fix = *fixFlag
	syncRegistryRuntimePin()
	d.ReleaseDrift = withClearChecks(doctorReleaseDriftHook(os.Getenv("AM_ROOT")), os.Getenv("AM_ROOT"))
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
	dir, err := filepath.Abs(project)
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
	checks := runDoctorChecks(d)
	if d.JSON {
		if err := writeJSONEnvelope(d.Out, "doctor", doctorEnvelopeData{
			TeamHome: canonicalFilesystemPath(d.ProjectDir),
			Checks:   checks,
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

// runDoctorChecks runs the AMQ checks, then the host probe of plan 16.2.
// The v2 team, worktree, pane and wake-mirror checks are gone (16.2).
func runDoctorChecks(d doctorExecution) []doctorCheck {
	checks := []doctorCheck{
		doctorCheckAMQVersion(d),
		doctorCheckLaunchapiCompatibility(d),
		doctorCheckAdoptionFloor(d),
		doctorCheckAMQIdentityPin(d),
		doctorCheckAMQOps(d),
		doctorCheckVersionSkew(d),
	}
	checks = append(checks, doctorCheckInstalledAMQFloor(d)...)
	if d.ReleaseDrift != nil {
		checks = append(checks, d.ReleaseDrift()...)
	}
	if d.Host != nil {
		d.Host.Fix = d.Fix
		checks = append(checks, d.Host.checks(d.ProjectDir)...)
		// The host probe shows the pi runtime row (brief 4-1); the versions
		// row and its --fix prune follow it.
		checks = append(checks, doctorPiRuntimeVersionsRows(d.Fix)...)
	} else {
		checks = append(checks, doctorCheckPiRuntime(d.Fix)...)
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
			Kind:   doctorAMQBelowFloor,
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
