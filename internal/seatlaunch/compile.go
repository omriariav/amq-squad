// Package seatlaunch is the 3.0 launch compiler and runner (plan 4.3, R3 to
// R5, R48 to R52): a typed seat list compiles to one AMQ launch request in
// which every seat runs behind `amq-squad seat-exec --seat <handle>`. The
// Go core stages each seat's launch record and role.md under the session
// root before the launch; seat-exec verifies them at exec time.
package seatlaunch

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/avivsinai/agent-message-queue/launchapi"

	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/launchintent"
	"github.com/omriariav/amq-squad/v3/internal/seatexec"
)

// Seat is one typed seat. squad.yaml (2b-5) compiles to this list.
type Seat struct {
	Handle  string `json:"handle"`
	Runtime string `json:"runtime"` // claude | codex | pi
	Model   string `json:"model,omitempty"`
	Effort  string `json:"effort,omitempty"`
	Role    string `json:"-"` // role.md text; required
	// RoleName is the seat's role id (lead, implementer, ...); the launch
	// record carries it, and status joins it into the seat row.
	RoleName string   `json:"role,omitempty"`
	Worktree string   `json:"worktree"`
	Branch   string   `json:"branch,omitempty"`
	EnvNeeds []string `json:"env_needs,omitempty"`
	// CredentialNeeds are the credential names the registry entry declares
	// (the only way a scrubbed credential reaches a seat; fix 3 item 6).
	CredentialNeeds []string `json:"credential_needs,omitempty"`
	Resume          bool     `json:"resume,omitempty"`
	// InitialInput is extra first-turn text. It may point at files; it may
	// never carry the role (plan 4.3, amendment 2).
	InitialInput string `json:"initial_input,omitempty"`
	// Provider is the provider executable; empty resolves the runtime on PATH.
	Provider string `json:"provider,omitempty"`
	// RequestID is the scheduler's SeatRequest.ID; it travels in the launch
	// record and the seat environment (AMQ_SQUAD_REQUEST_ID) and comes back
	// in the seat's SeatUp event.
	RequestID string `json:"request_id,omitempty"`

	// roleInFirstMessage is the role-delivery matrix's control arm only
	// (plan 13, amendment 15): the role text rides in initial_input instead
	// of the pointer. Unexported, so no squad file or caller can set it.
	roleInFirstMessage bool
}

// Squad is one launch: one AMQ session, one request.
type Squad struct {
	ProjectRoot string `json:"project_root"`
	BaseRoot    string `json:"base_root"` // default <project>/.agent-mail
	Session     string `json:"session"`
	Operator    string `json:"operator,omitempty"` // default user
	TeamHome    string `json:"team_home,omitempty"`
	Profile     string `json:"profile,omitempty"`
	RulesPath   string `json:"rules_path,omitempty"`
	Launcher    string `json:"launcher,omitempty"` // tmux (default), or another AMQ launcher
	Seats       []Seat `json:"seats"`
	// Wrapper is the absolute amq-squad executable; AMQPath the real amq (R51).
	Wrapper string `json:"-"`
	AMQPath string `json:"-"`
}

// Runtimes the compiler knows. pi admits no provider arguments (amendment
// 23 (b)); its model restriction stays on the registry side (R16).
var Runtimes = []string{"claude", "codex", "pi"}

// PiAdapterInLibrary says whether the pinned AMQ library carries the pi
// mint-mode adapter (PR #1188). v0.107.0 does not: flip this with the bump
// to the first tag that carries it. Until then a pi seat is refused here and
// pi seats run on the `amq coop exec` fallback (plan 4.3).
const PiAdapterInLibrary = false

// CodexSandboxArgs are the Codex sandbox and approval flags (plan 4.3, 7.3).
var CodexSandboxArgs = []string{"--sandbox", "workspace-write", "--ask-for-approval", "on-request"}

// RolePointer is the whole of the role's part in initial_input: a pointer.
const RolePointer = "Your seat role is in the file named by the environment variable AMQ_SQUAD_ROLE_PATH. Read it first and follow it."

var handlePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// ProviderArgs returns the provider arguments of a seat: model and effort
// (and Codex's sandbox and approval flags) for Claude and Codex, none for pi.
func ProviderArgs(s Seat) []string {
	var args []string
	switch s.Runtime {
	case "claude":
		if s.Model != "" {
			args = append(args, "--model", s.Model)
		}
		if s.Effort != "" {
			args = append(args, "--effort", s.Effort)
		}
	case "codex":
		if s.Model != "" {
			args = append(args, "--model", s.Model)
		}
		if s.Effort != "" {
			args = append(args, "-c", "model_reasoning_effort="+s.Effort)
		}
		args = append(args, CodexSandboxArgs...)
	}
	return args
}

// SessionRoot is <base root>/<session>.
func (q Squad) SessionRoot() string { return filepath.Join(q.BaseRoot, q.Session) }

// Normalize fills the defaults and checks the squad.
func (q Squad) Normalize() (Squad, error) {
	q.ProjectRoot = filepath.Clean(strings.TrimSpace(q.ProjectRoot))
	if !filepath.IsAbs(q.ProjectRoot) {
		return q, fmt.Errorf("project root %q must be absolute", q.ProjectRoot)
	}
	if q.BaseRoot == "" {
		q.BaseRoot = filepath.Join(q.ProjectRoot, ".agent-mail")
	}
	q.BaseRoot = filepath.Clean(q.BaseRoot)
	if !handlePattern.MatchString(q.Session) {
		return q, fmt.Errorf("session %q is not a valid AMQ session name", q.Session)
	}
	if q.Operator == "" {
		q.Operator = "user"
	}
	if q.Launcher == "" {
		q.Launcher = "tmux"
	}
	if q.TeamHome == "" {
		q.TeamHome = q.ProjectRoot
	}
	if !filepath.IsAbs(q.Wrapper) || !filepath.IsAbs(q.AMQPath) {
		return q, fmt.Errorf("the wrapper (%q) and the amq path (%q) must be absolute (R51)", q.Wrapper, q.AMQPath)
	}
	if len(q.Seats) == 0 {
		return q, fmt.Errorf("the squad has no seats")
	}
	seen := map[string]bool{q.Operator: true}
	for i, s := range q.Seats {
		if !handlePattern.MatchString(s.Handle) || seen[s.Handle] {
			return q, fmt.Errorf("seat %d: handle %q is invalid or repeated", i, s.Handle)
		}
		seen[s.Handle] = true
		if !slices.Contains(Runtimes, s.Runtime) {
			return q, fmt.Errorf("seat %s: runtime %q is not one of %v", s.Handle, s.Runtime, Runtimes)
		}
		if !filepath.IsAbs(s.Worktree) {
			return q, fmt.Errorf("seat %s: worktree %q must be absolute", s.Handle, s.Worktree)
		}
		if err := seatexec.ValidateNeeds(s.EnvNeeds, s.CredentialNeeds); err != nil {
			return q, fmt.Errorf("seat %s: %w", s.Handle, err)
		}
		if err := checkRoleDelivery(s); err != nil {
			return q, fmt.Errorf("seat %s: %w", s.Handle, err)
		}
	}
	return q, nil
}

// checkRoleDelivery: the role comes only from role.md; role text in
// initial_input without role.md is a malformed launch (plan 4.3).
func checkRoleDelivery(s Seat) error {
	if s.roleInFirstMessage && strings.TrimSpace(s.Role) != "" {
		return nil
	}
	if strings.TrimSpace(s.Role) == "" {
		if strings.TrimSpace(s.InitialInput) != "" {
			return fmt.Errorf("malformed launch: initial_input text without a role.md (the role comes only from role.md)")
		}
		return fmt.Errorf("no role: every seat needs role.md text")
	}
	in := launchintent.NormalizeGoalPrompt(s.InitialInput)
	for _, line := range strings.Split(s.Role, "\n") {
		if l := launchintent.NormalizeGoalPrompt(line); len(l) >= 24 && strings.Contains(in, l) {
			return fmt.Errorf("malformed launch: initial_input carries role text (%q); it may carry only a pointer to role.md", l)
		}
	}
	return nil
}

// Compiled is the launch request plus the staged records and roles.
type Compiled struct {
	Request launchapi.PrepareRequestV1
	Records []launch.Record
	Roles   [][]byte
}

// Compile turns a normalized squad into one PrepareRequestV1 and the staged
// records. rulesSHA is the digest of RulesPath ("" when there is none).
func Compile(q Squad, rulesSHA string, lookPath func(string) (string, error), now time.Time) (Compiled, error) {
	var out Compiled
	seats := make([]launchintent.SeatFacts, 0, len(q.Seats))
	for _, s := range q.Seats {
		if s.Runtime == "pi" && !PiAdapterInLibrary {
			return out, fmt.Errorf("seat %s: the pinned AMQ library has no pi launch adapter (upstream PR #1188 is not in a release yet); launch pi seats with the amq coop exec fallback", s.Handle)
		}
		provider := s.Provider
		if provider == "" {
			p, err := lookPath(s.Runtime)
			if err != nil {
				return out, fmt.Errorf("seat %s: %s is not on PATH: %w", s.Handle, s.Runtime, err)
			}
			provider = p
		}
		input := ""
		if s.Runtime != "pi" {
			input = strings.TrimSpace(RolePointer + " " + launchintent.NormalizeGoalPrompt(s.InitialInput))
		}
		if s.roleInFirstMessage {
			input = launchintent.NormalizeGoalPrompt(s.Role)
		}
		policy := launchapi.ResumePolicyFresh
		if s.Resume {
			policy = launchapi.ResumePolicyResume
		}
		seats = append(seats, launchintent.SeatFacts{
			Handle: s.Handle, Executable: provider, Args: ProviderArgs(s),
			Wrapper:         &launchapi.WrapperV1{Executable: q.Wrapper, Args: []string{"seat-exec", "--seat", s.Handle}},
			Cwd:             launchintent.SeatCWD{Kind: launchapi.WorkingDirectoryAbsolute, Path: s.Worktree},
			ResumePolicy:    policy,
			BootstrapPrompt: input,
			NoGitignore:     true,
			WakeAuditReason: "amq-squad 3.0 seats learn of work through task leases and doorbells (2b-1)",
		})
		role := []byte(s.Role)
		out.Roles = append(out.Roles, role)
		out.Records = append(out.Records, launch.Record{
			Handle: s.Handle, Root: q.SessionRoot(), BaseRoot: q.BaseRoot, Session: q.Session,
			Runtime: s.Runtime, Model: s.Model, Effort: s.Effort, RoleSHA256: digest(role),
			RulesPath: q.RulesPath, RulesSHA256: rulesSHA, Branch: s.Branch, Worktree: s.Worktree,
			AMQPath: q.AMQPath, EnvNeeds: s.EnvNeeds, CredNeeds: s.CredentialNeeds, Resume: s.Resume, RequestID: s.RequestID,
			CWD: s.Worktree, Binary: s.Runtime, Role: s.RoleName, TeamHome: q.TeamHome, TeamProfile: q.Profile, StartedAt: now.UTC(),
		})
	}
	intent, target, err := launchintent.Compile(launchintent.Input{
		Operator: launchintent.OperatorFacts{Handle: q.Operator},
		Seats:    seats,
		Target:   launchintent.TargetFacts{ProjectRoot: q.ProjectRoot, BaseRoot: q.BaseRoot, SessionRoot: q.SessionRoot(), Session: q.Session},
	})
	if err != nil {
		return out, err
	}
	out.Request = launchapi.PrepareRequestV1{
		RequestVersion: launchapi.RequestVersionV1, Target: target, Launcher: q.Launcher, Intent: intent,
		CallerContext: map[string]string{"caller": "amq-squad", "session": q.Session},
	}
	return out, nil
}

// CheckArgumentForms checks every seat's provider arguments against the
// forms AMQ advertises (v0.106.2 and later derive them from the validation
// tables); amq-squad keeps no list of forms of its own (plan 4.3).
func CheckArgumentForms(q Squad, caps []launchapi.ProviderCapabilitiesV1) error {
	byProvider := map[string]launchapi.ProviderCapabilitiesV1{}
	for _, c := range caps {
		byProvider[c.Provider] = c
	}
	for _, s := range q.Seats {
		args := ProviderArgs(s)
		if len(args) == 0 {
			continue
		}
		c, ok := byProvider[s.Runtime]
		if !ok {
			return fmt.Errorf("seat %s: AMQ advertised no argument forms for %s", s.Handle, s.Runtime)
		}
		for i := 0; i < len(args); i++ {
			if !strings.HasPrefix(args[i], "-") {
				continue
			}
			if !slices.Contains(c.AllowedArgumentForms, args[i]) {
				return fmt.Errorf("seat %s: AMQ does not admit %s for %s (admitted: %v)", s.Handle, args[i], s.Runtime, c.AllowedArgumentForms)
			}
			if args[i] == "-c" && i+1 < len(args) {
				key, value, _ := strings.Cut(args[i+1], "=")
				if !overrideAllowed(c.ConfigOverrides, key, value) {
					return fmt.Errorf("seat %s: AMQ does not admit -c %s for %s", s.Handle, args[i+1], s.Runtime)
				}
			}
		}
	}
	return nil
}

func overrideAllowed(overrides []launchapi.ConfigOverrideCapabilityV1, key, value string) bool {
	for _, o := range overrides {
		if o.Key == key {
			return len(o.AllowedValues) == 0 || slices.Contains(o.AllowedValues, value)
		}
	}
	return false
}

// Stage writes each seat's record and role.md into launch.StagedDir under
// the session root (0700 directories, 0600 files). The session root must
// exist: AMQ never creates an unknown session.
func Stage(c Compiled) error {
	for i, rec := range c.Records {
		if st, err := os.Stat(rec.Root); err != nil || !st.IsDir() {
			return fmt.Errorf("session root %s does not exist; create the session first (amq session create %s --root %s --me <operator>)", rec.Root, rec.Session, rec.BaseRoot)
		}
		dir := launch.StagedDir(rec.Root, rec.Handle)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		if err := writeAtomic(filepath.Join(dir, launch.RoleFileName), c.Roles[i]); err != nil {
			return err
		}
		if err := launch.WriteFile(filepath.Join(dir, launch.FileName), rec); err != nil {
			return err
		}
	}
	return nil
}

func writeAtomic(path string, data []byte) error {
	tmp := fmt.Sprintf("%s.%d.tmp", path, os.Getpid())
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func digest(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// FileDigest is the sha256 of a file, "" for an empty path.
func FileDigest(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return digest(b), nil
}
