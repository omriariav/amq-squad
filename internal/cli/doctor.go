package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/omriariav/amq-squad/internal/rules"
	"github.com/omriariav/amq-squad/internal/team"
)

// doctorMinAMQVersion is the lowest AMQ release this build of amq-squad
// expects to interoperate with. Bumped manually when amq-squad starts to
// depend on newer AMQ behavior; the doctor check compares the running amq
// binary's reported version against this floor.
const doctorMinAMQVersion = "0.34.0"

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
}

// doctorEnvelopeData is the kind="doctor" payload. team_home is the project
// root the report was scoped to. workstream is set only when a valid
// default team resolves one.
type doctorEnvelopeData struct {
	TeamHome   string        `json:"team_home"`
	Workstream string        `json:"workstream,omitempty"`
	Checks     []doctorCheck `json:"checks"`
}

// doctorExecution is the shared seam every doctor check reads from. Mirrors
// the statusExecution / downExecution pattern so unit tests can supply
// fakes for `amq env`, tmux discovery, and the liveness probe without
// touching the real binaries.
type doctorExecution struct {
	ProjectDir     string
	Out            io.Writer
	JSON           bool
	ResolveAMQEnv  func(projectDir string) (amqEnv, error)
	LookPath       func(name string) (string, error)
	Probe          duplicateLaunchProbe
	WakeOverride   func(t team.Team, workstream string) []doctorCheck
	WorkstreamHint string
}

func defaultDoctorExecution(projectDir string) doctorExecution {
	return doctorExecution{
		ProjectDir: projectDir,
		Out:        os.Stdout,
		ResolveAMQEnv: func(projectDir string) (amqEnv, error) {
			return resolveAMQEnvInDir(projectDir, "", "", "amq-squad")
		},
		LookPath: exec.LookPath,
		Probe:    defaultDuplicateLaunchProbe,
	}
}

func runDoctor(args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "emit a schema-versioned doctor envelope instead of the human table")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad doctor - check this project's amq-squad / AMQ setup

Usage:
  amq-squad doctor [--json]

Checks: AMQ version, default team config, tmux availability, configured
members' wake health, and CLAUDE.md / AGENTS.md marker integrity.
Read-only. Exits non-zero if any check is "fail".
`)
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return usageErrorf("doctor takes no positional arguments; got %d", fs.NArg())
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	d := defaultDoctorExecution(cwd)
	d.JSON = *jsonOut
	return executeDoctor(d)
}

func executeDoctor(d doctorExecution) error {
	checks, workstream := runDoctorChecks(d)
	if d.JSON {
		if err := writeJSONEnvelope(d.Out, "doctor", doctorEnvelopeData{
			TeamHome:   d.ProjectDir,
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
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "STATUS\tCHECK\tDETAIL")
	for _, c := range checks {
		detail := c.Detail
		if detail == "" {
			detail = "-"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", c.Status, c.Name, detail)
	}
	w.Flush()
}

func runDoctorChecks(d doctorExecution) ([]doctorCheck, string) {
	checks := []doctorCheck{}
	checks = append(checks, doctorCheckAMQVersion(d))
	checks = append(checks, doctorCheckTeamConfig(d))
	checks = append(checks, doctorCheckTmux(d))
	checks = append(checks, doctorCheckMarkerIntegrity(d)...)
	wakeChecks, workstream := doctorCheckWake(d)
	checks = append(checks, wakeChecks...)
	return checks, workstream
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
	if compareSemverParts(parsed, min) < 0 {
		return doctorCheck{
			Name:   "amq version",
			Status: doctorFail,
			Detail: fmt.Sprintf("amq %s is older than required %s", got, doctorMinAMQVersion),
		}
	}
	return doctorCheck{
		Name:   "amq version",
		Status: doctorOK,
		Detail: fmt.Sprintf("amq %s (min %s)", got, doctorMinAMQVersion),
	}
}

func doctorCheckTeamConfig(d doctorExecution) doctorCheck {
	if !team.Exists(d.ProjectDir) {
		return doctorCheck{
			Name:   "team config",
			Status: doctorWarn,
			Detail: "no .amq-squad/team.json (run 'amq-squad team init')",
		}
	}
	if _, err := team.Read(d.ProjectDir); err != nil {
		return doctorCheck{
			Name:   "team config",
			Status: doctorFail,
			Detail: fmt.Sprintf("invalid team.json: %v", err),
		}
	}
	return doctorCheck{
		Name:   "team config",
		Status: doctorOK,
		Detail: team.Path(d.ProjectDir),
	}
}

func doctorCheckTmux(d doctorExecution) doctorCheck {
	path, err := d.LookPath("tmux")
	if err != nil {
		return doctorCheck{
			Name:   "tmux",
			Status: doctorFail,
			Detail: "tmux not found on PATH (required for team launch)",
		}
	}
	return doctorCheck{Name: "tmux", Status: doctorOK, Detail: path}
}

func doctorCheckMarkerIntegrity(d doctorExecution) []doctorCheck {
	files := []string{rules.ClaudeFile, rules.AgentsFile}
	out := make([]doctorCheck, 0, len(files))
	for _, name := range files {
		out = append(out, inspectMarkerIntegrity(d.ProjectDir, name))
	}
	return out
}

func inspectMarkerIntegrity(dir, name string) doctorCheck {
	path := filepath.Join(dir, name)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return doctorCheck{
				Name:   "markers " + name,
				Status: doctorWarn,
				Detail: name + " not found (run 'amq-squad team sync --apply' to create it)",
			}
		}
		return doctorCheck{
			Name:   "markers " + name,
			Status: doctorFail,
			Detail: fmt.Sprintf("read %s: %v", path, err),
		}
	}
	body := string(data)
	begins := strings.Count(body, rules.BeginMarker)
	ends := strings.Count(body, rules.EndMarker)
	if begins == 0 && ends == 0 {
		return doctorCheck{
			Name:   "markers " + name,
			Status: doctorWarn,
			Detail: "no amq-squad managed block (run 'amq-squad team sync --apply')",
		}
	}
	if begins != 1 || ends != 1 {
		return doctorCheck{
			Name:   "markers " + name,
			Status: doctorFail,
			Detail: fmt.Sprintf("unbalanced markers: %d begin / %d end", begins, ends),
		}
	}
	beginIdx := strings.Index(body, rules.BeginMarker)
	endIdx := strings.Index(body, rules.EndMarker)
	if beginIdx > endIdx {
		return doctorCheck{
			Name:   "markers " + name,
			Status: doctorFail,
			Detail: "end marker appears before begin marker",
		}
	}
	return doctorCheck{
		Name:   "markers " + name,
		Status: doctorOK,
		Detail: path,
	}
}

// doctorCheckWake reuses classifyMemberStatus to classify the default
// profile's configured members. Returns the resolved workstream alongside
// the checks so the JSON envelope can include it. No live signals or
// "missing" map to ok; "stale" maps to warn. AMQ env resolution failures
// surface as warn with the underlying error in detail.
func doctorCheckWake(d doctorExecution) ([]doctorCheck, string) {
	if d.WakeOverride != nil {
		// Tests can inject a deterministic wake-check builder.
		t, err := team.Read(d.ProjectDir)
		if err != nil {
			return nil, ""
		}
		workstream, err := resolveTeamWorkstreamName(t, d.WorkstreamHint, d.WorkstreamHint != "")
		if err != nil {
			return nil, ""
		}
		return d.WakeOverride(t, workstream), workstream
	}
	if !team.Exists(d.ProjectDir) {
		return []doctorCheck{{
			Name:   "wake",
			Status: doctorWarn,
			Detail: "no team configured; skipping wake checks",
		}}, ""
	}
	t, err := team.Read(d.ProjectDir)
	if err != nil {
		return []doctorCheck{{
			Name:   "wake",
			Status: doctorWarn,
			Detail: "team config unreadable; skipping wake checks",
		}}, ""
	}
	workstream, err := resolveTeamWorkstreamName(t, d.WorkstreamHint, d.WorkstreamHint != "")
	if err != nil {
		return []doctorCheck{{
			Name:   "wake",
			Status: doctorWarn,
			Detail: fmt.Sprintf("could not resolve workstream: %v", err),
		}}, ""
	}
	probe := d.Probe
	checks := make([]doctorCheck, 0, len(t.Members))
	for _, m := range orderedTeamMembers(t.Members) {
		rec := classifyMemberStatus(t, m, workstream, probe)
		checks = append(checks, doctorCheckFromStatus(rec))
	}
	return checks, workstream
}

// doctorCheckFromStatus maps a statusRecord into a doctorCheck. live and
// missing are ok; stale is warn. Any amq-env failure that classifyMember
// reports as missing-with-detail surfaces as warn so the operator sees the
// underlying error.
func doctorCheckFromStatus(s statusRecord) doctorCheck {
	name := "wake " + s.Role
	switch s.Status {
	case statusStateLive:
		return doctorCheck{Name: name, Status: doctorOK, Detail: s.Detail}
	case statusStateMissing:
		if strings.HasPrefix(s.Detail, "amq env unresolved") {
			return doctorCheck{Name: name, Status: doctorWarn, Detail: s.Detail}
		}
		return doctorCheck{Name: name, Status: doctorOK, Detail: s.Detail}
	case statusStateStale:
		return doctorCheck{Name: name, Status: doctorWarn, Detail: s.Detail}
	default:
		return doctorCheck{Name: name, Status: doctorWarn, Detail: s.Detail}
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
