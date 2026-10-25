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
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/task"
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
	SizeCapFiles int    `json:"size_cap_files"` // changed files; 0: no merge fits the grant
	// RequiredCompanions are repository-relative globs the repository's CI
	// requires beside a change (workspace-cli: the skill docs of a new CLI
	// command, S6 W2/W3/W4/W6). The lead's widening path adds them to a
	// task's declared files. The owner names them; the default is empty.
	RequiredCompanions []string `json:"required_companions,omitempty"`
	// PushURL is where the harness pushes seat branches for this repository
	// (2b-2 milestone B rulings D1, D2): SSH, HTTPS github.com, or an
	// absolute local path / file://. Any other scheme is grants_invalid.
	// LocalPushAck acknowledges a local push target: without it a local
	// push_url does not parse (grants_invalid, R6 of 2b-2 milestone B); with
	// it doctor warns and the merge train still refuses a real run against one
	// (push_target_local), because no server-side ruleset stands behind it.
	PushURL      string `json:"push_url,omitempty"`
	LocalPushAck bool   `json:"local_push_ack,omitempty"`
}

// Push URL kinds (D2 of the 2b-2 milestone B second pass): an absolute URL
// of the admitted schemes only.
const (
	PushSSH   = "ssh"   // ssh://host/path or user@host:path; the agent authenticates
	PushHTTPS = "https" // https://github.com/owner/name; the pinned gh is the credential helper
	PushLocal = "local" // an absolute path or file://; needs merge.local_push_ack
)

// PushKind classifies a push URL; "" is not admitted (a relative name such
// as origin, http://, git://, another https host, credentials in the URL,
// ext:: or fd::, a leading dash, a space or control character).
func PushKind(u string) string {
	if u == "" || strings.HasPrefix(u, "-") {
		return ""
	}
	for _, c := range u {
		if c <= ' ' || c == 0x7f {
			return ""
		}
	}
	switch {
	case strings.HasPrefix(u, "ssh://"):
		rest := strings.TrimPrefix(u, "ssh://")
		if i := strings.IndexByte(rest, '/'); i > 0 && i < len(rest)-1 {
			return PushSSH
		}
		return ""
	case strings.HasPrefix(u, "https://"):
		rest := strings.TrimPrefix(u, "https://")
		host, path, ok := strings.Cut(rest, "/")
		if !ok || host != "github.com" || strings.Count(strings.TrimSuffix(strings.Trim(path, "/"), ".git"), "/") != 1 {
			return ""
		}
		return PushHTTPS
	case strings.HasPrefix(u, "file://"):
		if strings.HasPrefix(strings.TrimPrefix(u, "file://"), "/") {
			return PushLocal
		}
		return ""
	case strings.HasPrefix(u, "/"):
		return PushLocal
	case strings.Contains(u, "://"), strings.Contains(u, "::"):
		return ""
	}
	// scp-like user@host:path (git's own rule: a colon before any slash).
	user, rest, ok := strings.Cut(u, "@")
	host, path, ok2 := strings.Cut(rest, ":")
	if ok && ok2 && user != "" && host != "" && path != "" && !strings.ContainsAny(user+host, "/:") {
		return PushSSH
	}
	return ""
}

// PushTargetLocal reports a push_url that is a local path or file:// URL
// (PushKind's local kind: one definition with the launch and commit step).
func PushTargetLocal(url string) bool { return PushKind(url) == PushLocal }

// ValidPushURL accepts SSH (ssh://host/path or user@host:path), HTTPS on
// github.com (exactly https://github.com/owner/name), or an absolute local
// path / file:// URL; http://, git://, ext::, fd::, other HTTPS hosts,
// credentials in the URL and anything else are refused. It is PushKind's
// admission (one definition: the parser, the launch, the commit step and
// the merge train share it).
func ValidPushURL(url string) bool { return PushKind(url) != "" }

// Size cap defaults (3-2, measured on the S6 dry run): the largest clean PR,
// 207 changed lines in 2 files, rounded up to the next hundred lines and the
// next five files. Both fields are optional in the owner file and default to
// these; squad.yaml can only lower them.
const (
	DefaultSizeCapLines = 300
	DefaultSizeCapFiles = 5
)

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
	// Verifiers are the per-class verifier commands (`verifiers:`, keyed by
	// a class with a mechanical verifier: extract, script, implement). A
	// class with a command requires the harness's verdict to complete and
	// may route below its tier (phase 5 re-pass, ruling on ruling 1).
	Verifiers map[string]string `json:"verifiers,omitempty"`
	// VerifierGate are the classes a squad.yaml added to the verifier gate
	// (verifier_classes): they require a verdict too, with no command in the
	// grant, so their done reports block. A squad.yaml never removes a
	// class the grant verifies (the reviewer's correction).
	VerifierGate []string `json:"verifier_gate,omitempty"`
	// Clear is the owner-file key clear.signature (phase 5, the Buzz-signed
	// seat clear): how a quarantine's clear is signed.
	Clear Clear `json:"clear,omitempty"`
	// The verifier's bounds (v3-5-verifier rules 1 and 12), owner-settable:
	// wall time (verifier_timeout, a duration such as 10m), combined output
	// in bytes (verifier_output_limit) and how many verifiers run at once
	// (verifier_parallelism). Zero is the default.
	VerifierTimeout     time.Duration `json:"verifier_timeout,omitempty"`
	VerifierOutputLimit int           `json:"verifier_output_limit,omitempty"`
	VerifierParallelism int           `json:"verifier_parallelism,omitempty"`
	// VerifierArgv is a class's argv when the grant names its command as a
	// list (an item may hold a blank); Verifiers then holds its JSON array.
	VerifierArgv map[string][]string `json:"verifier_argv,omitempty"`
	// VerifierGoproxy is the verifier's GOPROXY (verifier_goproxy); "" is
	// Go's own default.
	VerifierGoproxy string `json:"verifier_goproxy,omitempty"`
}

// VerifierArgs is the class's verifier argv: the grant's list, else its
// command split on blanks; nil when none.
func (g Grants) VerifierArgs(class string) []string {
	if a := g.VerifierArgv[class]; len(a) > 0 {
		return slices.Clone(a)
	}
	return strings.Fields(g.Verifiers[class])
}

// Clear signature modes of the owner file's clear.signature.
const (
	// ClearRequired: a clear completes only on the owner's signed Buzz
	// answer (a typed gate of kind quarantine_clear). The default whenever
	// the owner's Buzz enrolment exists.
	ClearRequired = "required"
	// ClearPtyOnly: the owner's real pty alone clears. Accepted only when no
	// Buzz enrolment exists; with one the owner file is grants_invalid.
	ClearPtyOnly = "pty_only"
	// BindingFile is the owner's Buzz enrolment record, beside the owner
	// grants file (gateverify.DefaultBindingPath names the same file).
	BindingFile = "gate-binding.json"
)

// Clear is the clear.signature grant. Signature "" is the default.
type Clear struct {
	Signature string `json:"signature,omitempty"`
}

// ClearMode is the clear mode in force: the grant's own, else required when
// the owner's Buzz enrolment exists, else pty_only.
func (g Grants) ClearMode(enrolled bool) string {
	switch {
	case g.Clear.Signature != "":
		return g.Clear.Signature
	case enrolled:
		return ClearRequired
	}
	return ClearPtyOnly
}

// The verifier's default bounds: 10 minutes, 1 MiB of combined output, two
// verifiers at once.
const (
	DefaultVerifierTimeout     = 10 * time.Minute
	DefaultVerifierOutputLimit = 1 << 20
	DefaultVerifierParallelism = 2
)

// VerifierBounds are the verifier's wall time, output limit and
// parallelism, the defaults where the grant sets none.
func (g Grants) VerifierBounds() (timeout time.Duration, outputLimit, parallelism int) {
	timeout, outputLimit, parallelism = g.VerifierTimeout, g.VerifierOutputLimit, g.VerifierParallelism
	if timeout <= 0 {
		timeout = DefaultVerifierTimeout
	}
	if outputLimit <= 0 {
		outputLimit = DefaultVerifierOutputLimit
	}
	if parallelism <= 0 {
		parallelism = DefaultVerifierParallelism
	}
	return timeout, outputLimit, parallelism
}

// UnconfiguredGate are the classes in the verifier gate with no command,
// sorted: a squad.yaml added them, the grant names no command. A task of
// such a class is never dispatched (v3-5-verifier rule 4).
func (g Grants) UnconfiguredGate() []string {
	var out []string
	for _, c := range g.VerifierGate {
		if g.Verifiers[c] == "" && !slices.Contains(out, c) {
			out = append(out, c)
		}
	}
	slices.Sort(out)
	return out
}

// VerifierFor is the class's configured verifier command, "" when none.
func (g Grants) VerifierFor(class string) string { return g.Verifiers[class] }

// VerifierRequired says whether the class's done reports need the
// harness's verdict: the grant names its command, or a squad.yaml added it.
func (g Grants) VerifierRequired(class string) bool {
	return g.Verifiers[class] != "" || slices.Contains(g.VerifierGate, class)
}

// TaskVerifier is the task store's verifier source in production
// (task.VerifierSource): the owner grant, narrowed by the squad.yaml of the
// task's project path (the repository the lead named at task add), never a
// seat's tree. An untrusted owner file is an error, so the store fails
// closed and requires the verdict.
func TaskVerifier(t task.Task) (command string, required bool, err error) {
	g, err := Load(t.ProjectPath)
	if err != nil {
		return "", true, err
	}
	if g.Insecure {
		return "", true, fmt.Errorf("the owner grants file is untrusted: %s", g.InsecureWhy)
	}
	return g.VerifierFor(t.Class), g.VerifierRequired(t.Class), nil
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
	"~/.amq-squad/claude-token", // the owner's Claude token for seats (2b-2 B): never read or written by a seat
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
	// repositories; MergeSizeCapLines and MergeSizeCapFiles lower each kept cap.
	MergeRepositories []string
	MergeSizeCapLines int
	MergeSizeCapFiles int
	// FleetKeys are fleet-grant keys a squad.yaml named. A repository's grant
	// is a squad grant, so Narrow ignores each one by name (4-3).
	FleetKeys []string
	// VerifierClasses, when non-nil, is the squad's verifier gate
	// (verifier_classes): a superset of the classes the grant verifies. A
	// class it adds requires a verdict with no grant command; a class the
	// grant verifies that it omits is refused (grants_invalid).
	VerifierClasses []string
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
	out.Verifiers = maps.Clone(g.Verifiers)
	out.VerifierArgv = maps.Clone(g.VerifierArgv)
	out.VerifierGate = slices.Clone(g.VerifierGate)
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
	if n.VerifierClasses != nil {
		// a stricter gate only: a class is added, never removed
		if omitted := VerifierOmissions(g, n); len(omitted) > 0 {
			ignored = append(ignored, "verifier_classes omits "+strings.Join(omitted, ", ")+" (the owner grant verifies it; a squad.yaml never removes a verifier)")
		}
		for _, c := range n.VerifierClasses {
			if g.Verifiers[c] == "" && !slices.Contains(out.VerifierGate, c) {
				out.VerifierGate = append(out.VerifierGate, c)
			}
		}
	}
	if n.MergeRepositories != nil {
		out.Merge = slices.DeleteFunc(out.Merge, func(m Merge) bool {
			return !slices.ContainsFunc(n.MergeRepositories, func(r string) bool { return strings.EqualFold(r, m.Repository) })
		})
	}
	for i := range out.Merge {
		if n.MergeSizeCapLines > 0 {
			out.Merge[i].SizeCapLines = min(out.Merge[i].SizeCapLines, n.MergeSizeCapLines)
		}
		if n.MergeSizeCapFiles > 0 {
			out.Merge[i].SizeCapFiles = min(out.Merge[i].SizeCapFiles, n.MergeSizeCapFiles)
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
			// pty_only is the owner's choice when no Buzz enrolment exists;
			// with one present it would switch the second factor off
			// (grants_invalid, never a silent downgrade).
			if g.Clear.Signature == ClearPtyOnly {
				if _, serr := os.Lstat(filepath.Join(filepath.Dir(ownerPath), BindingFile)); serr == nil {
					return Resolved{Source: ownerPath}, fmt.Errorf("%s: clear.signature pty_only is accepted only when no Buzz enrolment exists, and %s is present: remove the key (the default is required) or un-enrol", ownerPath, BindingFile)
				}
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
	if omitted := VerifierOmissions(r.Grants, n); len(omitted) > 0 {
		return Resolved{Source: r.Source + "+" + p}, fmt.Errorf("%s: verifier_classes omits %s, which the owner grant verifies: a squad.yaml may add classes to the verifier gate, never remove one", p, strings.Join(omitted, ", "))
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

// VerifierOmissions are the classes the grant verifies that a squad.yaml's
// verifier_classes list leaves out, sorted; none when it names no list.
func VerifierOmissions(g Grants, n Narrowing) []string {
	if n.VerifierClasses == nil {
		return nil
	}
	var out []string
	for c := range g.Verifiers {
		if !slices.Contains(n.VerifierClasses, c) {
			out = append(out, c)
		}
	}
	slices.Sort(out)
	return out
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

// SizeCap is the size cap for a repository (owner/name) and base branch (the
// harness commit step of 2b-2 milestone B): the merge grant's size_cap_lines
// and size_cap_files where set, else the defaults.
func (g Grants) SizeCap(repository, base string) (lines, files int) {
	lines, files = DefaultSizeCapLines, DefaultSizeCapFiles
	if m, ok := g.MergeFor(repository, base); ok {
		if m.SizeCapLines > 0 {
			lines = m.SizeCapLines
		}
		if m.SizeCapFiles > 0 {
			files = m.SizeCapFiles
		}
	}
	return lines, files
}
