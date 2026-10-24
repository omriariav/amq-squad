// Package grants is the standing-grant schema 1 (plan 7.2, R16): the guarded
// owner file ~/.amq-squad/grants.yaml, narrowed by a repository's
// <repo root>/squad.yaml, else the built-in defaults. guard, the scheduler,
// seat-exec, squad.yaml (2b-5), doctor and the fleet grant (4-3) read it
// through Load and Narrow; nothing else parses the owner file.
//
// The owner file is strict: an unknown key, a malformed line or a bad value
// makes Load fail, and guard denies with grants_invalid. An owner file that
// is present but untrusted (group- or world-writable, not owned by the user,
// or in such a directory) fails closed: no new seat starts, its protected
// paths still apply, typed gates stay needs_gate, the reason code is
// owner_grants_insecure, and doctor reports the mode (3-1 ruling 1).
package grants

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const (
	Schema          = 1
	DefaultMaxSeats = 12 // plan 7.2, amendment 22
	// ReasonInsecure is the reason code of an owner file that is present but
	// untrusted.
	ReasonInsecure = "owner_grants_insecure"
)

// Modes (plan 3.4). Manual is the narrower one.
const (
	ModeAuto   = "auto"
	ModeManual = "manual"
)

// Privacy classes (plan 5). local is narrower than vendor: a vendor grant
// admits local and vendor entries, a local grant admits local entries only.
const (
	PrivacyVendor = "vendor"
	PrivacyLocal  = "local"
)

// Merge is the standing merge grant for one repository (plan 7.2, R34). Two
// limits are fixed and are not fields: no force push and no protected-path
// change. 3-2 enforces the grant at the merge.
type Merge struct {
	Repository   string `json:"repository"` // owner/name
	BaseBranch   string `json:"base_branch"`
	CIRequired   bool   `json:"ci_required"`
	SizeCapLines int    `json:"size_cap_lines"` // 0: no merge fits the grant
}

// Fleet is the fleet grant (plan 3.5, 7.2), reserved for 4-3. The zero value
// grants nothing.
type Fleet struct {
	MaxSquads             int      `json:"max_squads,omitempty"`
	CostPerDayUSD         float64  `json:"cost_per_day_usd,omitempty"`
	PermittedRepositories []string `json:"permitted_repositories,omitempty"`
	// RepositoryPaths maps a repository to the canonical checkout the owner
	// permits for it (4-3, additive): an owner-controlled identity source
	// for the mayor, as the merge grant names its repository (plan 7.2).
	RepositoryPaths []RepositoryPath `json:"repository_paths,omitempty"`
}

// RepositoryPath is one repository and its permitted checkout. Remote, when
// set, is the checkout's origin as host plus full path (transport and .git
// stripped, e.g. github.com/owner/app); a checkout whose origin differs is
// not that repository.
type RepositoryPath struct {
	Repository string `json:"repository"`
	Path       string `json:"path"`
	Remote     string `json:"remote,omitempty"`
}

// Grants is one standing grant.
type Grants struct {
	Mode     string `json:"mode"`
	MaxSeats int    `json:"max_seats"`
	// CostPerDayUSD 0 means no cost cap is set.
	CostPerDayUSD float64 `json:"cost_per_day_usd,omitempty"`
	// PermittedRuntimes nil means any registry runtime; empty admits none.
	PermittedRuntimes []string `json:"permitted_runtimes"`
	PrivacyClass      string   `json:"privacy_class"`
	DefaultBranch     string   `json:"default_branch,omitempty"` // "": origin/HEAD, else main
	ProtectedPaths    []string `json:"protected_paths,omitempty"`
	Merge             []Merge  `json:"merge,omitempty"`
	Fleet             Fleet    `json:"fleet"`
}

// Defaults are the built-in grants when no owner file exists.
func Defaults() Grants {
	return Grants{Mode: ModeAuto, MaxSeats: DefaultMaxSeats, PrivacyClass: PrivacyVendor}
}

// Implicit is the protected set every grant carries (R37, 3-1 ruling 3):
// team-rules.md, the managed blocks of CLAUDE.md and AGENTS.md,
// .claude/settings*.json, the hook plugin files, squad.yaml itself (the
// lead writes it through the digest flow; a seat must not), and the
// repository identity file .amq-squad/repository (4-3). Entries are
// repo-relative unless they start with ~/ or /; a trailing / covers a tree;
// * ? [ are globs. A "managed:" entry protects only the amq-squad managed
// block of that file (internal/rules markers): a seat may edit the rest.
// The owner's global agent config is included (R16, amendment 18).
var Implicit = []string{
	".amq-squad/team-rules.md",
	ManagedPrefix + "CLAUDE.md",
	ManagedPrefix + "AGENTS.md",
	".claude/settings*.json",
	".claude/hooks/",
	"~/.claude/settings*.json",
	"~/.claude/plugins/",
	"~/.codex/config.toml",
	"squad.yaml",
	// the repository identity file the mayor authorizes a checkout by when it
	// has no origin remote (internal/fleet.IdentityFile; 4-3)
	".amq-squad/repository",
}

// ManagedPrefix marks an Implicit entry that protects a managed block only.
const ManagedPrefix = "managed:"

// Narrowing is what a repository's squad.yaml may say about grants. Every
// field can only narrow; the zero value narrows nothing. 2b-5 fills it from
// its squad.yaml schema and calls Narrow.
type Narrowing struct {
	Mode          string
	MaxSeats      int
	CostPerDayUSD float64
	// PermittedRuntimes is intersected with the grant's when RuntimesSet.
	PermittedRuntimes []string
	RuntimesSet       bool
	PrivacyClass      string
	ProtectedPaths    []string // added to the grant's
	// MergeRepositories, when non-nil, keeps only the merge grants for these
	// repositories; MergeSizeCapLines lowers each kept cap.
	MergeRepositories []string
	MergeSizeCapLines int
	// FleetKeys are fleet-grant keys a squad.yaml named. A repository's grant
	// is a squad grant, so Narrow ignores each one by name (4-3).
	FleetKeys []string
}

// ReasonFleetInSquad is the Ignored entry of a squad.yaml fleet key.
const ReasonFleetInSquad = "fleet grant (the operator session's alone; a squad.yaml cannot grant it)"

// Narrow applies n to g. A field of n that would widen g is ignored and named
// in the returned list, so doctor can report it; the result is never wider
// than g on any field.
func Narrow(g Grants, n Narrowing) (Grants, []string) {
	out := g
	out.PermittedRuntimes = slices.Clone(g.PermittedRuntimes)
	out.ProtectedPaths = slices.Clone(g.ProtectedPaths)
	out.Merge = slices.Clone(g.Merge)
	var ignored []string
	switch n.Mode {
	case "", g.Mode:
	case ModeManual:
		out.Mode = ModeManual
	default:
		ignored = append(ignored, "mode "+n.Mode)
	}
	if n.MaxSeats > 0 {
		if n.MaxSeats <= g.MaxSeats {
			out.MaxSeats = n.MaxSeats
		} else {
			ignored = append(ignored, fmt.Sprintf("max_seats %d above %d", n.MaxSeats, g.MaxSeats))
		}
	}
	if n.CostPerDayUSD > 0 {
		if g.CostPerDayUSD == 0 || n.CostPerDayUSD <= g.CostPerDayUSD {
			out.CostPerDayUSD = n.CostPerDayUSD
		} else {
			ignored = append(ignored, fmt.Sprintf("cost_per_day_usd %g above %g", n.CostPerDayUSD, g.CostPerDayUSD))
		}
	}
	if n.RuntimesSet {
		keep := []string{}
		for _, r := range n.PermittedRuntimes {
			if g.PermittedRuntimes == nil || slices.Contains(g.PermittedRuntimes, r) {
				keep = append(keep, r)
			} else {
				ignored = append(ignored, "runtime "+r)
			}
		}
		out.PermittedRuntimes = keep
	}
	switch n.PrivacyClass {
	case "", g.PrivacyClass:
	case PrivacyLocal:
		out.PrivacyClass = PrivacyLocal
	default:
		ignored = append(ignored, "privacy_class "+n.PrivacyClass)
	}
	for _, k := range n.FleetKeys {
		ignored = append(ignored, k+" "+ReasonFleetInSquad)
	}
	out.ProtectedPaths = append(out.ProtectedPaths, n.ProtectedPaths...)
	if n.MergeRepositories != nil {
		out.Merge = slices.DeleteFunc(out.Merge, func(m Merge) bool {
			return !slices.ContainsFunc(n.MergeRepositories, func(r string) bool { return strings.EqualFold(r, m.Repository) })
		})
	}
	if n.MergeSizeCapLines > 0 {
		for i := range out.Merge {
			out.Merge[i].SizeCapLines = min(out.Merge[i].SizeCapLines, n.MergeSizeCapLines)
		}
	}
	return out, ignored
}

// Resolved is the grant a caller acts on, with where it came from.
type Resolved struct {
	Grants
	// Source is "default" or the owner file path, followed by "+<squad.yaml
	// path>" when a squad.yaml narrowed it.
	Source string `json:"source"`
	// ReasonCode is ReasonInsecure when the owner file is present but
	// untrusted, else "".
	ReasonCode string `json:"reason_code,omitempty"`
	// Insecure: no new seat starts (MaxSeats 0), typed gates stay needs_gate
	// even with a verified answer, there is no merge or fleet grant.
	Insecure bool `json:"insecure,omitempty"`
	// InsecureWhy says what made the owner file untrusted.
	InsecureWhy string `json:"insecure_why,omitempty"`
	// Ignored names the squad.yaml fields that tried to widen.
	Ignored []string `json:"ignored,omitempty"`
}

// OwnerPath is ~/.amq-squad/grants.yaml, "" without a home directory.
func OwnerPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".amq-squad", "grants.yaml")
}

// Load resolves the grant for a repository root ("" for none): the owner file
// (OwnerPath) if present, else Defaults, narrowed by <repoRoot>/squad.yaml if
// present. An owner file that does not parse is an error (grants_invalid), so
// is a squad.yaml whose grant keys do not parse.
func Load(repoRoot string) (Resolved, error) {
	return LoadFrom(OwnerPath(), repoRoot)
}

// LoadFrom is Load with an explicit owner file path.
func LoadFrom(ownerPath, repoRoot string) (Resolved, error) {
	r := Resolved{Grants: Defaults(), Source: "default"}
	if ownerPath != "" {
		raw, err := os.ReadFile(ownerPath)
		switch {
		case os.IsNotExist(err):
		case err != nil:
			return Resolved{Source: ownerPath}, err
		default:
			g, err := ParseOwner(string(raw), ownerPath)
			if err != nil {
				return Resolved{Source: ownerPath}, err
			}
			r.Grants, r.Source = g, ownerPath
			if why := Insecure(ownerPath); why != "" {
				r = failClosed(g, ownerPath, why)
			}
		}
	}
	if repoRoot == "" {
		return r, nil
	}
	// A repository's grant is a squad grant: the fleet grant is the operator
	// session's alone (plan 3.5, R39; 4-3), so a squad lead never holds it.
	r.Fleet = Fleet{}
	p := filepath.Join(repoRoot, "squad.yaml")
	raw, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return r, nil
	}
	if err != nil {
		// present but unreadable: its narrowing cannot be known, fail closed
		return Resolved{Source: r.Source + "+" + p}, fmt.Errorf("read %s: %w", p, err)
	}
	n, err := ParseSquadNarrowing(string(raw), p)
	if err != nil {
		return Resolved{Source: r.Source + "+" + p}, err
	}
	r.Grants, r.Ignored = Narrow(r.Grants, n)
	r.Source += "+" + p
	return r, nil
}

// failClosed is the grant of a present but untrusted owner file: no seats,
// manual mode, local privacy, no runtime, no merge or fleet grant, and the
// file's protected paths kept (an untrusted file may protect more, never
// less).
func failClosed(g Grants, path, why string) Resolved {
	d := Defaults()
	d.MaxSeats, d.Mode, d.PrivacyClass = 0, ModeManual, PrivacyLocal
	d.PermittedRuntimes = []string{}
	d.ProtectedPaths = slices.Clone(g.ProtectedPaths)
	return Resolved{Grants: d, Source: path, ReasonCode: ReasonInsecure, Insecure: true, InsecureWhy: why}
}

// ProtectedEntries is the implicit set followed by the grant's own list.
func (g Grants) ProtectedEntries() []string {
	return append(slices.Clone(Implicit), g.ProtectedPaths...)
}

// MergeFor returns the merge grant for a repository and base branch.
func (g Grants) MergeFor(repository, base string) (Merge, bool) {
	for _, m := range g.Merge {
		if strings.EqualFold(m.Repository, repository) && m.BaseBranch == base {
			return m, true
		}
	}
	return Merge{}, false
}

// FleetScope reports whether the grant is a fleet grant: the operator
// session's (plan 3.5). A squad grant (Load with a repository root) never is.
func (r Resolved) FleetScope() bool { return !r.Insecure && r.Fleet.MaxSquads > 0 }

// RepositoryPermitted reports whether the fleet grant permits a repository
// (owner/name, case-insensitive). An empty list permits none.
func (f Fleet) RepositoryPermitted(repository string) bool {
	return slices.ContainsFunc(f.PermittedRepositories, func(r string) bool { return strings.EqualFold(r, repository) })
}

// RuntimePermitted reports whether a runtime may start a seat.
func (g Grants) RuntimePermitted(runtime string) bool {
	return g.PermittedRuntimes == nil || slices.Contains(g.PermittedRuntimes, runtime)
}

// PrivacyPermitted reports whether a registry entry's privacy class fits.
func (g Grants) PrivacyPermitted(class string) bool {
	return class == PrivacyLocal || (class == PrivacyVendor && g.PrivacyClass == PrivacyVendor)
}

// SeatRequest is the growth-gate rule (R30): a request for seat number
// `seats` (live seats after the start) inside the grant is allowed; past
// MaxSeats it is the growth gate, which an owner-signed spawn answer for
// GrowTarget opens. An insecure grant has MaxSeats 0, so every request is a
// growth gate, and its gates never open (Resolved.Insecure).
func (r Resolved) SeatRequest(seats int) (withinGrant bool) {
	return !r.Insecure && seats <= r.MaxSeats
}
