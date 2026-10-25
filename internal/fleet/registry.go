// Package fleet is the fleet side of the operator session (plan 3.5, 16.3,
// 16.5; R38 to R41): the fleet registry ~/.amq-squad/fleet.yaml (each squad,
// its root, its lead handle), the fleet status document over it, the
// operator mailbox routes, the grouped gate list of manual mode, and the
// mayor of auto mode. The operator session is the same program as a squad
// lead; only its grant differs (internal/leadsession).
//
// The registry read and write are the 2a kept-dead extract of
// internal/cli/global_noc.go (the NOC registry), renamed: one strict file,
// a non-symlink directory, an exclusive lock and an atomic replace.
package fleet

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/yamlsub"
)

// Schema is the fleet.yaml schema version.
const Schema = 1

// OperatorHandle is the operator mailbox: the owner's user handle (briefs
// ruling 16). The status contract carries no operator field.
const OperatorHandle = "user"

// Squad is one squad of the fleet.
type Squad struct {
	Name        string `json:"name"`         // the squad's id in the fleet
	Root        string `json:"root"`         // its AMQ session root
	ProjectPath string `json:"project_path"` // its repository root (squad.yaml lives here)
	Repository  string `json:"repository"`   // owner/name, checked against the fleet grant
	Lead        string `json:"lead"`         // the lead seat's handle
	Session     string `json:"session"`      // the AMQ session name; "" is the root's base name
	Project     string `json:"project"`      // the AMQ peer project name for cross-project routing; "" is Name
	// The mayor's start of the squad (Codex review, P1 2): a row the mayor
	// reserved carries its phase, the reservation id, the mayor's
	// incarnation (a token, its pid and the pid's start time) and when the
	// phase began. "" phase is a running squad.
	Phase        string `json:"phase,omitempty"`         // "" | reserved | applying | waiting_seats | launch_failed_partial
	Reservation  string `json:"reservation,omitempty"`   // the reservation id
	Incarnation  string `json:"incarnation,omitempty"`   // the mayor that reserved it
	MayorPID     string `json:"mayor_pid,omitempty"`     // its process id
	MayorStart   string `json:"mayor_start,omitempty"`   // its process start time, RFC 3339 UTC
	PhaseSince   string `json:"phase_since,omitempty"`   // when the phase began, RFC 3339 UTC
	LiveSeats    string `json:"live_seats,omitempty"`    // launch_failed_partial: the seats that are live, comma-separated
	FencedReason string `json:"fenced_reason,omitempty"` // fenced: phase_stalled or liveness_unknown
}

// Reservation phases. reserved: admitted; applying: squad.yaml through the
// digest flow; waiting_seats: the roster launch.
const (
	PhaseReserved      = "reserved"
	PhaseApplying      = "applying"
	PhaseWaitingSeats  = "waiting_seats"
	PhaseFailedPartial = "launch_failed_partial"
	// PhaseFenced is a reservation the registry reclaimed (phase stalled or
	// liveness unknown past its window) while its mayor may still launch:
	// it no longer counts, and its mayor's late promotion closes the seats.
	PhaseFenced = "fenced"
)

// InFlight reports whether the row is a reservation whose launch has not
// ended (reserved, applying, waiting_seats).
func (s Squad) InFlight() bool {
	return s.Phase == PhaseReserved || s.Phase == PhaseApplying || s.Phase == PhaseWaitingSeats
}

// Counts reports whether the row takes a max_squads slot: every
// reservation in flight and every partial launch (until the operator
// resolves it), whatever live says; a running squad when live says so (nil:
// always). Stale reservations are released under the lock before counting
// (ReleaseStale).
func (s Squad) Counts(live func(Squad) bool) bool {
	switch {
	case s.InFlight() || s.Phase == PhaseFailedPartial:
		return true
	case s.Phase == PhaseFenced:
		return false // reclaimed: its slot is free
	}
	return live == nil || live(s)
}

// Release reasons.
const (
	ReleaseIncarnationGone = "incarnation_gone" // the reserving mayor's process is gone
	ReleasePhaseStalled    = "phase_stalled"    // the mayor is alive but its phase has not advanced for a window
	ReleaseLivenessUnknown = "liveness_unknown" // the mayor's liveness could not be read and its phase has not advanced for a window
	ReleaseLaunchFenced    = "launch_fenced"    // a launch that finished after its reservation was fenced: seats closed, row deleted
)

// PhaseWindow is the default time a reservation may stay in one phase.
const PhaseWindow = 10 * time.Minute

// Released records a reservation the registry released.
type Released struct {
	Name        string `json:"name"`
	Reservation string `json:"reservation"`
	Incarnation string `json:"incarnation"`
	Phase       string `json:"phase"`
	Reason      string `json:"reason"` // incarnation_gone | phase_stalled
	At          string `json:"at"`     // RFC 3339 UTC
}

// keepReleased bounds the released record.
const keepReleased = 20

// Liveness is what a reservation's mayor incarnation is known to be.
type Liveness int

const (
	LivenessAlive   Liveness = iota // the pid is live with the recorded start time
	LivenessGone                    // the pid is dead, or reused (another start time)
	LivenessUnknown                 // the pid is live but its start time cannot be read
)

// ReleaseStale releases, inside a registry mutation (so under the lock),
// every reservation in flight whose mayor incarnation is gone, or whose
// phase has not advanced for window. A reservation does not expire while
// its mayor is alive and advancing. Unknown liveness counts as alive (the
// row stays counted): only the stall window releases it, with the reason
// liveness_unknown. alive(pid, start) checks the incarnation's process.
// Each release is recorded with its reason.
func (r *Registry) ReleaseStale(now time.Time, window time.Duration, alive func(pid int, start time.Time) Liveness) {
	if window <= 0 {
		window = PhaseWindow
	}
	var keep []Squad
	for _, s := range r.Squads {
		if !s.InFlight() && s.Phase != PhaseFenced {
			keep = append(keep, s)
			continue
		}
		pid, _ := strconv.Atoi(s.MayorPID)
		start, serr := time.Parse(time.RFC3339Nano, s.MayorStart)
		live := LivenessGone
		if serr == nil {
			live = alive(pid, start)
		}
		if s.Phase == PhaseFenced {
			// a fenced row waits for its mayor's late close; a gone mayor
			// will never come back for it
			if live == LivenessGone {
				r.record(s, ReleaseIncarnationGone, now)
				continue
			}
			keep = append(keep, s)
			continue
		}
		since, perr := time.Parse(time.RFC3339, s.PhaseSince)
		stalled := perr != nil || now.Sub(since) >= window
		reason := ""
		switch {
		case live == LivenessGone:
			reason = ReleaseIncarnationGone
		case stalled && live == LivenessUnknown:
			reason = ReleaseLivenessUnknown
		case stalled:
			reason = ReleasePhaseStalled
		}
		switch reason {
		case "":
			keep = append(keep, s)
		case ReleaseIncarnationGone:
			// no launch of a gone mayor is outstanding: the row is deleted
			r.record(s, reason, now)
		default:
			// the mayor may still be launching: the row is fenced, not
			// deleted. It no longer counts; a late promotion against it
			// closes the launched seats (launch_fenced).
			r.record(s, reason, now)
			s.Phase, s.FencedReason = PhaseFenced, reason
			keep = append(keep, s)
		}
	}
	r.Squads = keep
}

// record appends a release of row s, bounded to the last keepReleased.
func (r *Registry) record(s Squad, reason string, now time.Time) {
	r.Released = append(r.Released, Released{Name: s.Name, Reservation: s.Reservation, Incarnation: s.Incarnation,
		Phase: s.Phase, Reason: reason, At: now.UTC().Format(time.RFC3339)})
	if n := len(r.Released); n > keepReleased {
		r.Released = r.Released[n-keepReleased:]
	}
}

// Operator is where the operator session's mailbox lives.
type Operator struct {
	Root    string `json:"root"`    // the operator's AMQ session root
	Project string `json:"project"` // the AMQ peer project name squads route to
	Session string `json:"session"` // the operator's AMQ session name
}

// Waiting is a squad the mayor declined or deferred for the fleet cap
// (max_squads or cost_per_day_usd). It is not a registered squad; status
// --fleet shows it with wait_reason fleet_cap so the owner sees why it is
// not up. The mayor removes it when the squad starts.
type Waiting struct {
	Name        string `json:"name"`
	Repository  string `json:"repository"`
	ProjectPath string `json:"project_path"`
	Cap         string `json:"cap"`   // max_squads | cost_per_day | cost_unmetered
	Since       string `json:"since"` // RFC 3339 UTC
	Detail      string `json:"detail,omitempty"`
}

// Registry is fleet.yaml in memory.
type Registry struct {
	Schema   int       `json:"schema"`
	Operator Operator  `json:"operator"`
	Squads   []Squad   `json:"squads"`
	Waiting  []Waiting `json:"waiting,omitempty"`
	// Released are the reservations the registry released, newest last
	// (bounded).
	Released []Released `json:"released,omitempty"`
}

// DefaultPath is ~/.amq-squad/fleet.yaml, "" without a home directory.
func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".amq-squad", "fleet.yaml")
}

var (
	namePattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	repoPattern  = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}/[A-Za-z0-9._-]{1,100}$`)
	tokenPattern = regexp.MustCompile(`^[a-f0-9]{8,64}$`)
)

// SessionName is the squad's AMQ session name.
func (s Squad) SessionName() string {
	if s.Session != "" {
		return s.Session
	}
	return filepath.Base(s.Root)
}

// ProjectName is the squad's AMQ peer project name.
func (s Squad) ProjectName() string {
	if s.Project != "" {
		return s.Project
	}
	return s.Name
}

// SameRoot reports whether two session roots are one directory: the same
// clean path, the same canonical path (symlinks resolved), or, when both
// exist, the same device and inode.
func SameRoot(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	ca, errA := filepath.EvalSymlinks(a)
	cb, errB := filepath.EvalSymlinks(b)
	if errA == nil && errB == nil && ca == cb {
		return true
	}
	fa, errA := os.Stat(a)
	fb, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(fa, fb)
}

// Find returns the squad with this name.
func (r Registry) Find(name string) (Squad, bool) {
	i := slices.IndexFunc(r.Squads, func(s Squad) bool { return s.Name == name })
	if i < 0 {
		return Squad{}, false
	}
	return r.Squads[i], true
}

// Validate checks every field.
func (r Registry) Validate() error {
	if r.Schema != Schema {
		return fmt.Errorf("fleet.yaml: schema %d is not %d", r.Schema, Schema)
	}
	if r.Operator != (Operator{}) {
		if !absClean(r.Operator.Root) || !namePattern.MatchString(r.Operator.Project) || !namePattern.MatchString(r.Operator.Session) {
			return errors.New("fleet.yaml: operator needs an absolute root, a project name and a session name")
		}
	}
	seen := map[string]bool{}
	for _, s := range r.Squads {
		switch {
		case !namePattern.MatchString(s.Name) || seen[s.Name]:
			return fmt.Errorf("fleet.yaml: squad name %q is missing, invalid or repeated", s.Name)
		case !absClean(s.Root) || !absClean(s.ProjectPath):
			return fmt.Errorf("fleet.yaml: squad %s needs an absolute root and project_path", s.Name)
		case !repoPattern.MatchString(s.Repository):
			return fmt.Errorf("fleet.yaml: squad %s repository %q is not owner/name", s.Name, s.Repository)
		case !namePattern.MatchString(s.Lead) || s.Lead == OperatorHandle:
			return fmt.Errorf("fleet.yaml: squad %s lead handle %q is invalid or the operator's", s.Name, s.Lead)
		case s.Session != "" && !namePattern.MatchString(s.Session), s.Project != "" && !namePattern.MatchString(s.Project):
			return fmt.Errorf("fleet.yaml: squad %s has a bad session or project name", s.Name)
		}
		switch s.Phase {
		case "":
			if s.Reservation != "" || s.Incarnation != "" || s.MayorPID != "" || s.MayorStart != "" || s.PhaseSince != "" || s.LiveSeats != "" || s.FencedReason != "" {
				return fmt.Errorf("fleet.yaml: squad %s is running but carries reservation fields", s.Name)
			}
		case PhaseReserved, PhaseApplying, PhaseWaitingSeats, PhaseFailedPartial, PhaseFenced:
			if (s.Phase == PhaseFenced) != (s.FencedReason == ReleasePhaseStalled || s.FencedReason == ReleaseLivenessUnknown) {
				return fmt.Errorf("fleet.yaml: squad %s phase %s has fenced_reason %q", s.Name, s.Phase, s.FencedReason)
			}
			_, perr := time.Parse(time.RFC3339, s.PhaseSince)
			_, serr := time.Parse(time.RFC3339Nano, s.MayorStart)
			pid, ierr := strconv.Atoi(s.MayorPID)
			if perr != nil || serr != nil || ierr != nil || pid <= 0 || !tokenPattern.MatchString(s.Reservation) || !tokenPattern.MatchString(s.Incarnation) {
				return fmt.Errorf("fleet.yaml: squad %s phase %s needs a reservation, an incarnation with its pid and start time, and phase_since", s.Name, s.Phase)
			}
			if strings.ContainsAny(s.LiveSeats, "\" \x00\r\n") {
				return fmt.Errorf("fleet.yaml: squad %s has bad live_seats", s.Name)
			}
		default:
			return fmt.Errorf("fleet.yaml: squad %s phase %q is unknown", s.Name, s.Phase)
		}
		seen[s.Name] = true
	}
	// One squad per session root (phase 5 re-pass of 4-3, P2-1): two rows on
	// one root, or on a root and its symlink alias, would make fleet status
	// show one session twice and attribute both copies' questions to the
	// first squad.
	for i, a := range r.Squads {
		for _, b := range r.Squads[i+1:] {
			if SameRoot(a.Root, b.Root) {
				return fmt.Errorf("fleet.yaml: squads %s and %s share the session root %s (root identity: one squad per root)", a.Name, b.Name, a.Root)
			}
		}
	}
	for _, x := range r.Released {
		if _, err := time.Parse(time.RFC3339, x.At); err != nil || !namePattern.MatchString(x.Name) || !tokenPattern.MatchString(x.Reservation) ||
			(x.Reason != ReleaseIncarnationGone && x.Reason != ReleasePhaseStalled && x.Reason != ReleaseLivenessUnknown && x.Reason != ReleaseLaunchFenced) {
			return fmt.Errorf("fleet.yaml: released %q is invalid", x.Name)
		}
	}
	seenWait := map[string]bool{}
	for _, w := range r.Waiting {
		_, err := time.Parse(time.RFC3339, w.Since)
		switch {
		case !namePattern.MatchString(w.Name) || seenWait[w.Name] || seen[w.Name]:
			return fmt.Errorf("fleet.yaml: waiting squad %q is invalid, repeated or registered", w.Name)
		case !repoPattern.MatchString(w.Repository) || !absClean(w.ProjectPath):
			return fmt.Errorf("fleet.yaml: waiting squad %s needs owner/name and an absolute project_path", w.Name)
		case w.Cap != ReasonMaxSquads && w.Cap != ReasonCostPerDay && w.Cap != ReasonCostUnmetered:
			return fmt.Errorf("fleet.yaml: waiting squad %s cap %q is not max_squads, cost_per_day or cost_unmetered", w.Name, w.Cap)
		case err != nil:
			return fmt.Errorf("fleet.yaml: waiting squad %s since %q is not RFC 3339", w.Name, w.Since)
		case strings.ContainsAny(w.Detail, "\"\x00\r\n"):
			return fmt.Errorf("fleet.yaml: waiting squad %s has a bad detail", w.Name)
		}
		seenWait[w.Name] = true
	}
	return nil
}

func absClean(p string) bool {
	return p != "" && filepath.IsAbs(p) && filepath.Clean(p) == p && !strings.ContainsAny(p, "\"\x00\r\n")
}

// Parse reads fleet.yaml strictly through internal/yamlsub (the grants and
// squad.yaml subset): top-level "schema: 1", an "operator:" map, and the
// "squads:", "waiting:" and "released:" lists of maps. Every key is known
// and none repeats at any level.
func Parse(text, source string) (Registry, error) {
	r := Registry{}
	entries, err := yamlsub.Split(text, source, true)
	if err != nil {
		return r, err
	}
	wrap := func(err error) error { return fmt.Errorf("%s: %w", source, err) }
	sawSchema := false
	for _, e := range entries {
		var err error
		switch e.Key {
		case "schema":
			var v int
			if v, err = e.Integer(1); err == nil && v != Schema {
				err = e.Errf("schema %d is not %d", v, Schema)
			}
			r.Schema, sawSchema = Schema, err == nil
		case "operator":
			err = fields(e.Map, map[string]*string{"operator.root": &r.Operator.Root,
				"operator.project": &r.Operator.Project, "operator.session": &r.Operator.Session})
		case "squads":
			err = listOf(e, func() map[string]*string {
				r.Squads = append(r.Squads, Squad{})
				s := &r.Squads[len(r.Squads)-1]
				return map[string]*string{"squads.name": &s.Name, "squads.root": &s.Root, "squads.project_path": &s.ProjectPath,
					"squads.repository": &s.Repository, "squads.lead": &s.Lead, "squads.session": &s.Session, "squads.project": &s.Project,
					"squads.phase": &s.Phase, "squads.reservation": &s.Reservation, "squads.incarnation": &s.Incarnation,
					"squads.mayor_pid": &s.MayorPID, "squads.mayor_start": &s.MayorStart, "squads.phase_since": &s.PhaseSince,
					"squads.live_seats": &s.LiveSeats, "squads.fenced_reason": &s.FencedReason}
			})
		case "waiting":
			err = listOf(e, func() map[string]*string {
				r.Waiting = append(r.Waiting, Waiting{})
				w := &r.Waiting[len(r.Waiting)-1]
				return map[string]*string{"waiting.name": &w.Name, "waiting.repository": &w.Repository, "waiting.project_path": &w.ProjectPath,
					"waiting.cap": &w.Cap, "waiting.since": &w.Since, "waiting.detail": &w.Detail}
			})
		case "released":
			err = listOf(e, func() map[string]*string {
				r.Released = append(r.Released, Released{})
				x := &r.Released[len(r.Released)-1]
				return map[string]*string{"released.name": &x.Name, "released.reservation": &x.Reservation, "released.incarnation": &x.Incarnation,
					"released.phase": &x.Phase, "released.reason": &x.Reason, "released.at": &x.At}
			})
		default:
			err = e.Errf("unknown key")
		}
		if err != nil {
			return r, wrap(err)
		}
	}
	if !sawSchema {
		return r, fmt.Errorf("%s: schema missing (want schema: 1)", source)
	}
	return r, nil
}

// fields reads a map block into the named string fields; every key must be
// one of them.
func fields(read func() ([]yamlsub.Entry, error), into map[string]*string) error {
	sub, err := read()
	if err != nil {
		return err
	}
	for _, f := range sub {
		dst, ok := into[f.Key]
		if !ok {
			return f.Errf("unknown key")
		}
		if *dst, err = f.Scalar(); err != nil {
			return err
		}
	}
	return nil
}

// listOf reads a list of maps; next returns the fields of a new element.
func listOf(e yamlsub.Entry, next func() map[string]*string) error {
	items, err := e.MapItems()
	if err != nil {
		return err
	}
	for _, it := range items {
		it := it
		if err := fields(func() ([]yamlsub.Entry, error) { return it, nil }, next()); err != nil {
			return err
		}
	}
	return nil
}

// Render writes r in canonical form; Parse(Render(r)) == r.
func Render(r Registry) string {
	var b strings.Builder
	kv := func(indent, k, v string) {
		if v != "" {
			fmt.Fprintf(&b, "%s%s: %s\n", indent, k, yamlsub.Quote(v))
		}
	}
	list := func(name string, n int, item func(i int) [][2]string) {
		if n == 0 {
			if name == "squads" {
				b.WriteString("squads: []\n")
			}
			return
		}
		b.WriteString(name + ":\n")
		for i := 0; i < n; i++ {
			for j, f := range item(i) {
				if j == 0 {
					fmt.Fprintf(&b, "  - %s: %s\n", f[0], yamlsub.Quote(f[1]))
					continue
				}
				kv("    ", f[0], f[1])
			}
		}
	}
	b.WriteString("# amq-squad fleet registry (plan 3.5, 16.3): each squad, its root, its lead handle.\n")
	fmt.Fprintf(&b, "schema: %d\n", Schema)
	if r.Operator != (Operator{}) {
		b.WriteString("operator:\n")
		kv("  ", "root", r.Operator.Root)
		kv("  ", "project", r.Operator.Project)
		kv("  ", "session", r.Operator.Session)
	}
	list("squads", len(r.Squads), func(i int) [][2]string {
		s := r.Squads[i]
		return [][2]string{{"name", s.Name}, {"root", s.Root}, {"project_path", s.ProjectPath}, {"repository", s.Repository},
			{"lead", s.Lead}, {"session", s.Session}, {"project", s.Project}, {"phase", s.Phase}, {"reservation", s.Reservation},
			{"incarnation", s.Incarnation}, {"mayor_pid", s.MayorPID}, {"mayor_start", s.MayorStart}, {"phase_since", s.PhaseSince},
			{"live_seats", s.LiveSeats}, {"fenced_reason", s.FencedReason}}
	})
	list("waiting", len(r.Waiting), func(i int) [][2]string {
		w := r.Waiting[i]
		return [][2]string{{"name", w.Name}, {"repository", w.Repository}, {"project_path", w.ProjectPath}, {"cap", w.Cap},
			{"since", w.Since}, {"detail", w.Detail}}
	})
	list("released", len(r.Released), func(i int) [][2]string {
		x := r.Released[i]
		return [][2]string{{"name", x.Name}, {"reservation", x.Reservation}, {"incarnation", x.Incarnation}, {"phase", x.Phase},
			{"reason", x.Reason}, {"at", x.At}}
	})
	return b.String()
}
