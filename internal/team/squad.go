package team

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/omriariav/amq-squad/v3/internal/grants"
	"github.com/omriariav/amq-squad/v3/internal/yamlsub"
)

// squad.yaml schema 1 (plan 3.3, 3.4, 4.3, 7.2; brief 2b-5). One file per
// repository at <repo root>/squad.yaml (ruling E2). The lead writes it
// through the digest flow (team propose, team apply); a seat cannot, because
// squad.yaml is in grants.Implicit. It compiles to one AMQ launch request
// (Seats). Its grant keys only narrow the owner grant: guard reads them
// leniently through grants.ParseSquadNarrowing, and Parse reads them through
// the same function and refuses, by name, any key that would widen.

const (
	SquadSchema   = 1
	SquadFileName = "squad.yaml"
)

// Runtimes a seat may name: the providers AMQ v0.116.2's launch adapters
// serve (internal/launch/adapter.go:21-36 there; brief 31-1, Q4). Another
// runtime is refused by name at parse.
var Runtimes = []string{"claude", "codex", "cursor-agent", "grok", "pi"}

// Execution modes (2a-2 ruling 10: the execution-mode fields live here).
const (
	ExecutionModeGlobalOrchestrator = "global_orchestrator"
	ExecutionModeProjectLead        = "project_lead"
	ExecutionModeProjectTeam        = "project_team"
	ExecutionModeDirectLeadSession  = "direct_lead_session"
)

// ExecutionModes is the closed list.
var ExecutionModes = []string{ExecutionModeGlobalOrchestrator, ExecutionModeProjectLead, ExecutionModeProjectTeam, ExecutionModeDirectLeadSession}

// Tool policy profiles (the seat's least-required capability class).
var ToolProfiles = []string{ToolProfileMinimal, ToolProfileCoding, ToolProfileBrowser, ToolProfileData, ToolProfileFull}

// Seat is one seat of squad.yaml. Its fields are the typed seat list of the
// 2b-2 launch compiler (seatlaunch.Seat): handle, runtime, model, effort,
// role, worktree.
type Seat struct {
	Handle    string `json:"handle"`
	Role      string `json:"role"`
	Runtime   string `json:"runtime"`
	Model     string `json:"model,omitempty"`
	Effort    string `json:"effort,omitempty"`
	Worktree  string `json:"worktree,omitempty"`   // repo-relative or absolute; "" is the repo root
	Tools     string `json:"tools,omitempty"`      // tool policy profile; "" is full
	ActorMode string `json:"actor_mode,omitempty"` // implementation | review; "" derives
}

// SeatDefaults fill a seat field the seat leaves empty.
type SeatDefaults struct {
	Runtime string `json:"runtime,omitempty"`
	Model   string `json:"model,omitempty"`
	Effort  string `json:"effort,omitempty"`
	Tools   string `json:"tools,omitempty"`
}

// Squad is squad.yaml in memory.
type Squad struct {
	Schema             int    `json:"schema"`
	Session            string `json:"session,omitempty"`
	Lead               string `json:"lead,omitempty"` // a seat handle
	LeadMode           string `json:"lead_mode,omitempty"`
	ExecutionMode      string `json:"execution_mode,omitempty"`
	ControlRoot        string `json:"control_root,omitempty"`
	TargetProjectRoot  string `json:"target_project_root,omitempty"`
	SharedCwdException string `json:"shared_cwd_exception,omitempty"`
	// OperatorInteraction is how the owner answers gates: lead_pane,
	// separate_terminal or noc ("" is unspecified).
	OperatorInteraction string       `json:"operator_interaction,omitempty"`
	Defaults            SeatDefaults `json:"defaults"`
	Seats               []Seat       `json:"seats"`
	// Grants is the narrowing (mode, max_seats, cost_per_day_usd,
	// permitted_runtimes, privacy_class, protected_paths, merge_repositories,
	// merge_size_cap_lines), as grants.ParseSquadNarrowing reads it.
	Grants grants.Narrowing `json:"grants"`
}

// WidenError is a squad.yaml field that would widen the owner grant. It is a
// parse error (acceptance 1), never a silent drop.
type WidenError struct {
	Source string
	Field  string // e.g. "max_seats", "protected_paths", "mode"
	Detail string
}

func (e *WidenError) Error() string {
	return fmt.Sprintf("%s: %s widens the owner grant: %s (squad.yaml can only narrow, plan 7.2)", e.Source, e.Field, e.Detail)
}

// ErrWidens matches every *WidenError with errors.Is.
var ErrWidens = errors.New("squad.yaml widens the owner grant")

func (e *WidenError) Is(target error) bool { return target == ErrWidens }

// SquadPath is <repoRoot>/squad.yaml.
func SquadPath(repoRoot string) string { return filepath.Join(repoRoot, SquadFileName) }

var (
	handlePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	tokenPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/\[\]-]{0,127}$`)
)

// grantKeys are the keys grants.ParseSquadNarrowing reads.
var grantKeys = grantKeyNames()

// LoadSquad reads <repoRoot>/squad.yaml against the owner grant at
// grants.OwnerPath. A missing file is fs.ErrNotExist.
func LoadSquad(repoRoot string) (Squad, error) {
	return LoadSquadWith(grants.OwnerPath(), repoRoot)
}

// LoadSquadWith is LoadSquad with an explicit owner file path.
func LoadSquadWith(ownerPath, repoRoot string) (Squad, error) {
	raw, err := os.ReadFile(SquadPath(repoRoot))
	if err != nil {
		return Squad{}, err
	}
	owner, err := grants.LoadFrom(ownerPath, "")
	if err != nil {
		return Squad{}, err
	}
	return ParseSquad(string(raw), SquadPath(repoRoot), owner.Grants)
}

// ParseSquad parses squad.yaml strictly against the owner grant: every key
// known, every value valid, no field that widens the grant.
func ParseSquad(text, source string, owner grants.Grants) (Squad, error) {
	s := Squad{Schema: SquadSchema}
	entries, err := yamlsub.Split(text, source, true)
	if err != nil {
		return s, err
	}
	wrap := func(err error) error { return fmt.Errorf("%s: %w", source, err) }
	// A schema this build does not read is named first, whatever else the
	// file holds: a newer file's keys are unknown here, and the version is
	// the reason (final form A3).
	for _, e := range entries {
		if e.Key != "schema" {
			continue
		}
		if v, verr := e.Integer(1); verr == nil && v != SquadSchema {
			return s, &SchemaError{Source: source, Version: v}
		}
	}
	sawSchema := false
	for _, e := range entries {
		var err error
		if IsNeverSettable(e.Key) {
			// an owner-only key: refused at parse, named as written (A4)
			return s, &WidenError{Source: source, Field: neverSettablePath(e), Detail: "only the owner files ~/.amq-squad/grants.yaml and registry.yaml set it"}
		}
		switch e.Key {
		case "schema":
			_, err = e.Integer(1)
			sawSchema = true
		case "session":
			if s.Session, err = e.Scalar(); err == nil && !handlePattern.MatchString(s.Session) {
				err = e.Errf("bad session name %q", s.Session)
			}
		case "lead":
			s.Lead, err = e.Scalar()
		case "lead_mode":
			s.LeadMode, err = e.OneOf(LeadModeBuilder, LeadModePlanner)
		case "execution_mode":
			s.ExecutionMode, err = e.OneOf(ExecutionModes...)
		case "control_root":
			s.ControlRoot, err = absPath(e)
		case "target_project_root":
			s.TargetProjectRoot, err = absPath(e)
		case "shared_cwd_exception":
			s.SharedCwdException, err = e.Scalar()
		case "operator_interaction":
			if s.OperatorInteraction, err = e.Scalar(); err == nil {
				err = ValidateOperatorInteractionMode(s.OperatorInteraction)
			}
		case "defaults":
			s.Defaults, err = parseDefaults(e)
		case "seats":
			s.Seats, err = parseSeats(e)
		case "protected_paths":
			err = noRemoval(e, source)
		default:
			if !slices.Contains(grantKeys, e.Key) {
				err = e.Errf("unknown key")
			}
		}
		if err != nil {
			var w *WidenError
			if errors.As(err, &w) {
				return s, err
			}
			return s, wrap(err)
		}
	}
	if !sawSchema {
		return s, wrap(fmt.Errorf("schema: missing (want schema: %d)", SquadSchema))
	}
	if s.Grants, err = grants.ParseSquadNarrowing(text, source); err != nil {
		return s, err
	}
	effective, ignored := grants.Narrow(owner, s.Grants)
	if len(ignored) > 0 {
		field, detail, _ := strings.Cut(ignored[0], " ")
		if field == "runtime" {
			field, detail = "permitted_runtimes", detail+" is not in the owner's permitted runtimes"
		}
		return s, &WidenError{Source: source, Field: field, Detail: detail}
	}
	if err := s.validate(source, effective); err != nil {
		return s, err
	}
	return s, nil
}

// noRemoval refuses a protected_paths entry that tries to remove a path.
func noRemoval(e yamlsub.Entry, source string) error {
	items, err := e.List()
	if err != nil {
		return err
	}
	for _, p := range items {
		if strings.HasPrefix(p, "!") || strings.HasPrefix(p, "-") {
			return &WidenError{Source: source, Field: "protected_paths", Detail: fmt.Sprintf("%q removes a protected path; squad.yaml only adds", p)}
		}
	}
	return nil
}

func absPath(e yamlsub.Entry) (string, error) {
	p, err := e.Scalar()
	if err == nil && !filepath.IsAbs(p) {
		err = e.Errf("want an absolute path, got %q", p)
	}
	return filepath.Clean(p), err
}

func token(e yamlsub.Entry) (string, error) {
	v, err := e.Scalar()
	if err == nil && !tokenPattern.MatchString(v) {
		err = e.Errf("bad value %q", v)
	}
	return v, err
}

func parseDefaults(e yamlsub.Entry) (SeatDefaults, error) {
	var d SeatDefaults
	sub, err := e.Map()
	if err != nil {
		return d, err
	}
	for _, f := range sub {
		switch f.Key {
		case "defaults.runtime":
			d.Runtime, err = f.OneOf(Runtimes...)
		case "defaults.model":
			d.Model, err = token(f)
		case "defaults.effort":
			d.Effort, err = token(f)
		case "defaults.tools":
			d.Tools, err = f.OneOf(ToolProfiles...)
		default:
			err = f.Errf("unknown key")
		}
		if err != nil {
			return d, err
		}
	}
	return d, nil
}

func parseSeats(e yamlsub.Entry) ([]Seat, error) {
	items, err := e.MapItems()
	if err != nil {
		return nil, err
	}
	out := []Seat{}
	for _, it := range items {
		var seat Seat
		for _, f := range it {
			switch f.Key {
			case "seats.handle":
				seat.Handle, err = f.Scalar()
			case "seats.role":
				seat.Role, err = f.Scalar()
			case "seats.runtime":
				seat.Runtime, err = f.OneOf(Runtimes...)
			case "seats.model":
				seat.Model, err = token(f)
			case "seats.effort":
				seat.Effort, err = token(f)
			case "seats.worktree":
				seat.Worktree, err = f.Scalar()
			case "seats.tools":
				seat.Tools, err = f.OneOf(ToolProfiles...)
			case "seats.actor_mode":
				seat.ActorMode, err = f.OneOf(ActorModeImplementation, ActorModeReview)
			default:
				err = f.Errf("unknown key")
			}
			if err != nil {
				return nil, err
			}
		}
		out = append(out, seat)
	}
	return out, nil
}

// Resolved returns the seats with the defaults applied.
func (s Squad) Resolved() []Seat {
	out := slices.Clone(s.Seats)
	for i := range out {
		if out[i].Runtime == "" {
			out[i].Runtime = s.Defaults.Runtime
		}
		if out[i].Runtime != "pi" {
			if out[i].Model == "" {
				out[i].Model = s.Defaults.Model
			}
			if out[i].Effort == "" && out[i].Runtime != "cursor-agent" {
				out[i].Effort = s.Defaults.Effort
			}
		}
		if out[i].Tools == "" {
			out[i].Tools = s.Defaults.Tools
		}
		if out[i].Role == "" {
			out[i].Role = out[i].Handle
		}
	}
	return out
}

func (s Squad) validate(source string, effective grants.Grants) error {
	fail := func(format string, args ...any) error {
		return fmt.Errorf("%s: %s", source, fmt.Sprintf(format, args...))
	}
	if len(s.Seats) == 0 {
		return fail("seats: a squad needs at least one seat")
	}
	if len(s.Seats) > effective.MaxSeats {
		return &WidenError{Source: source, Field: "seats", Detail: fmt.Sprintf("%d seats above max_seats %d", len(s.Seats), effective.MaxSeats)}
	}
	seen := map[string]bool{DefaultOperatorHandle: true}
	for i, seat := range s.Resolved() {
		switch {
		case !handlePattern.MatchString(seat.Handle) || seen[seat.Handle]:
			return fail("seats[%d]: handle %q is missing, invalid, repeated or the operator's", i, seat.Handle)
		case ValidateRoleID(seat.Role) != nil:
			return fail("seat %s: bad role %q", seat.Handle, seat.Role)
		case seat.Runtime == "":
			return fail("seat %s: runtime missing (set it or defaults.runtime)", seat.Handle)
		case !effective.RuntimePermitted(seat.Runtime):
			return &WidenError{Source: source, Field: "seats", Detail: fmt.Sprintf("seat %s runtime %s is not a permitted runtime", seat.Handle, seat.Runtime)}
		case seat.Runtime == "cursor-agent" && seat.Effort != "":
			// AMQ's cursor-agent adapter maps --model only (Q4 of brief 31-1)
			return fail("seat %s: a cursor-agent seat takes no effort (AMQ's cursor-agent adapter maps --model only)", seat.Handle)
		case strings.ContainsAny(seat.Worktree, "\x00\r\n"):
			return fail("seat %s: bad worktree", seat.Handle)
		case seat.Worktree != "" && SeatWorktree(filepath.Dir(source), seat) == filepath.Clean(filepath.Dir(source)):
			// the commit step moves the seat's branch, so a seat never works
			// in the repository root (brief 31-1: leave worktree out and one
			// is derived per handle)
			return fail("seat %s: worktree %q is the repository root; leave it out and the seat's worktree is %s/<handle>", seat.Handle, seat.Worktree, SeatWorktreesDir)
		}
		seen[seat.Handle] = true
	}
	if s.Lead != "" && !slices.ContainsFunc(s.Seats, func(x Seat) bool { return x.Handle == s.Lead }) {
		return fail("lead: %q is not a seat handle", s.Lead)
	}
	return nil
}

// SeatWorktreesDir is where a seat's worktree is derived when squad.yaml
// names none: <repo root>/.worktrees/<handle> (brief 31-1).
const SeatWorktreesDir = ".worktrees"

// SeatWorktree is the seat's absolute worktree: squad.yaml's worktree
// resolved against repoRoot, else derived per handle under
// <repoRoot>/.worktrees/ (brief 31-1: never the repository root; validate
// refuses a squad.yaml worktree that names it).
func SeatWorktree(repoRoot string, seat Seat) string {
	if seat.Worktree == "" {
		return filepath.Join(repoRoot, SeatWorktreesDir, seat.Handle)
	}
	if filepath.IsAbs(seat.Worktree) {
		return filepath.Clean(seat.Worktree)
	}
	return filepath.Join(repoRoot, seat.Worktree)
}

// Render writes s in canonical schema-1 form; ParseSquad(Render(s)) == s.
func Render(s Squad) string {
	var b strings.Builder
	line := func(k, v string) {
		if v != "" {
			fmt.Fprintf(&b, "%s: %s\n", k, yamlsub.Quote(v))
		}
	}
	list := func(k string, items []string) {
		if items == nil {
			return
		}
		fmt.Fprintf(&b, "%s:", k)
		if len(items) == 0 {
			b.WriteString(" []\n")
			return
		}
		b.WriteString("\n")
		for _, it := range items {
			fmt.Fprintf(&b, "  - %s\n", yamlsub.Quote(it))
		}
	}
	fmt.Fprintf(&b, "schema: %d\n", SquadSchema)
	line("session", s.Session)
	line("lead", s.Lead)
	line("lead_mode", s.LeadMode)
	line("execution_mode", s.ExecutionMode)
	line("control_root", s.ControlRoot)
	line("target_project_root", s.TargetProjectRoot)
	line("shared_cwd_exception", s.SharedCwdException)
	line("operator_interaction", s.OperatorInteraction)
	g := s.Grants
	line("mode", g.Mode)
	if g.MaxSeats > 0 {
		line("max_seats", strconv.Itoa(g.MaxSeats))
	}
	if g.CostPerDayUSD > 0 {
		line("cost_per_day_usd", strconv.FormatFloat(g.CostPerDayUSD, 'f', -1, 64))
	}
	if g.RuntimesSet {
		list("permitted_runtimes", append([]string{}, g.PermittedRuntimes...))
	}
	line("privacy_class", g.PrivacyClass)
	list("protected_paths", g.ProtectedPaths)
	list("merge_repositories", g.MergeRepositories)
	if g.MergeSizeCapLines > 0 {
		line("merge_size_cap_lines", strconv.Itoa(g.MergeSizeCapLines))
	}
	if g.MergeSizeCapFiles > 0 {
		line("merge_size_cap_files", strconv.Itoa(g.MergeSizeCapFiles))
	}
	list("verifier_classes", g.VerifierClasses)
	if s.Defaults != (SeatDefaults{}) {
		b.WriteString("defaults:\n")
		for _, kv := range [][2]string{{"runtime", s.Defaults.Runtime}, {"model", s.Defaults.Model}, {"effort", s.Defaults.Effort}, {"tools", s.Defaults.Tools}} {
			if kv[1] != "" {
				fmt.Fprintf(&b, "  %s: %s\n", kv[0], yamlsub.Quote(kv[1]))
			}
		}
	}
	b.WriteString("seats:\n")
	for _, seat := range s.Seats {
		first := true
		for _, kv := range [][2]string{{"handle", seat.Handle}, {"role", seat.Role}, {"runtime", seat.Runtime}, {"model", seat.Model}, {"effort", seat.Effort}, {"worktree", seat.Worktree}, {"tools", seat.Tools}, {"actor_mode", seat.ActorMode}} {
			if kv[1] == "" {
				continue
			}
			prefix := "    "
			if first {
				prefix, first = "  - ", false
			}
			fmt.Fprintf(&b, "%s%s: %s\n", prefix, kv[0], yamlsub.Quote(kv[1]))
		}
	}
	return b.String()
}

// ExampleSquad is the documented example (docs/squad-yaml.md carries the
// same text; a test keeps the two equal and checks it parses).
const ExampleSquad = `# squad.yaml schema 1: one squad per repository, at the repository root.
# The lead proposes it as a digest (amq-squad team propose) and writes it
# after approval (amq-squad team apply); seats cannot write it.
schema: 1
session: feature-x            # the AMQ session name
lead: lead                    # a seat handle
lead_mode: planner            # builder | planner
execution_mode: project_team  # global_orchestrator | project_lead | project_team | direct_lead_session
# Grant narrowing: each key can only narrow ~/.amq-squad/grants.yaml.
mode: manual                  # auto -> manual narrows; manual -> auto is an error
max_seats: 4                  # at or under the owner cap
permitted_runtimes: [claude, codex, pi]
protected_paths:              # added to the owner's and the implicit set
  - docs/adr/
defaults:                     # filled into a seat that leaves the field empty
  runtime: claude
  effort: medium
seats:
  - handle: lead
    role: cto
    model: opus
    actor_mode: review
  - handle: dev
    role: senior-dev
    runtime: codex
    model: gpt-5.5
    worktree: ../feature-x-dev
    tools: coding
  - handle: scout
    role: researcher
    runtime: pi                # a pi model, when named, is PROVIDER/ID
`
