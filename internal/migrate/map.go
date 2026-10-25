package migrate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/omriariav/amq-squad/v3/internal/grants"
	"github.com/omriariav/amq-squad/v3/internal/team"
)

func jsonUnmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }

const proposedName = "grants.yaml.proposed"

var sessionPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// mapper turns the v2 profile into a squad.yaml schema 1 and records what it
// could not map.
type mapper struct {
	in       *inputs
	squad    team.Squad
	unmapped map[string]string
	safety   []string
	roleOf   map[string]string // handle -> role
	maxSeats int               // autonomous.max_active_agents, for the proposal
}

func (m *mapper) unmap(key, reason string) {
	if _, ok := m.unmapped[key]; !ok {
		m.unmapped[key] = reason
	}
}

func (m *mapper) refuseSafety(path string) { m.safety = append(m.safety, path) }

func (m *mapper) run() error {
	prof := m.in.Profile
	m.squad = team.Squad{Schema: team.SquadSchema}
	m.roleOf = map[string]string{}
	for _, key := range sortedKeys(prof) {
		v := prof[key]
		if isEmpty(v) {
			continue
		}
		switch key {
		case "schema", "members", "lead", "orchestrated", "workstream":
			// consumed below
		case "lead_mode":
			s := strOf(v)
			if s == team.LeadModeBuilder || s == team.LeadModePlanner {
				m.squad.LeadMode = s
			} else {
				m.unmap(key, "not builder or planner")
			}
		case "execution_mode":
			s := strOf(v)
			if contains(team.ExecutionModes, s) {
				m.squad.ExecutionMode = s
			} else {
				m.unmap(key, topReasons[key])
			}
		case "control_root":
			m.squad.ControlRoot = m.rootPath(key, v)
		case "target_project_root":
			m.squad.TargetProjectRoot = m.rootPath(key, v)
		case "shared_cwd_exception":
			m.squad.SharedCwdException = strOf(v)
		case "operator":
			m.operator(v)
		case "trust":
			if s := strOf(v); s == "approve-for-me" || s == "trusted" {
				m.refuseSafety(key)
			} else {
				m.unmap(key, topReasons[key])
			}
		case "autonomous":
			m.autonomous(v)
		case "goal_supervision":
			if mode, _ := v.(map[string]any); strOf(mode["mode"]) == "safe-auto" {
				m.refuseSafety("goal_supervision.mode")
			}
			m.unmap(key, reasonOr(key, reasonNoMeaning))
		case "binary_args":
			m.binaryArgs(v)
		default:
			m.unmap(key, reasonOr(key, "not read by main's profile loader"))
		}
		if key != "members" {
			checkSafety(key, key, v, m.refuseSafety)
		}
	}
	if err := m.members(); err != nil {
		return err
	}
	m.session()
	m.lead()
	if len(m.safety) > 0 {
		return &Refusal{Code: CodeSafetyDefault, Fields: outermost(m.safety), Detail: "a v2 field would change a safety default in 3.0 (an allow list, a launcher, a launcher preference, a trust or approval mode, an argument that skips a prompt); migrate refuses rather than drop it silently. Remove the field from the v2 profile, or set the authority in the owner's grants.yaml by hand, and run again"}
	}
	return nil
}

func reasonOr(key, fallback string) string {
	if r, ok := topReasons[key]; ok {
		return r
	}
	return fallback
}

func (m *mapper) rootPath(key string, v any) string {
	s := strOf(v)
	if s == "" {
		return ""
	}
	if !filepath.IsAbs(s) {
		s = filepath.Join(m.in.Project, s)
	}
	return filepath.Clean(s)
}

func (m *mapper) operator(v any) {
	op, _ := v.(map[string]any)
	for _, k := range sortedKeys(op) {
		val := op[k]
		if isEmpty(val) {
			continue
		}
		key := "operator." + k
		switch k {
		case "interaction_mode":
			switch s := strOf(val); s {
			case team.OperatorInteractionLeadPane, team.OperatorInteractionSeparateTerminal, team.OperatorInteractionNOC:
				m.squad.OperatorInteraction = s
			case "self_operator":
				m.refuseSafety(key)
			default:
				m.unmap(key, "not a 3.0 operator interaction mode")
			}
		case "self_operator":
			m.refuseSafety(key)
		case "notifications":
			m.unmap(key, "3.0 raises gates to the owner through its own attention surfaces")
		default:
			m.unmap(key, "the 3.0 operator is always the owner; "+reasonNoMeaning)
		}
	}
}

func (m *mapper) autonomous(v any) {
	au, _ := v.(map[string]any)
	for _, k := range sortedKeys(au) {
		val := au[k]
		if isEmpty(val) {
			continue
		}
		key := "autonomous." + k
		if k == "max_active_agents" {
			if n, ok := val.(float64); ok && n >= 1 && n <= grants.DefaultMaxSeats {
				m.maxSeats = int(n)
				continue
			}
			m.refuseSafety(key)
			continue
		}
		m.unmap(key, "runtime composition is the scheduler's in 3.0")
	}
}

func (m *mapper) binaryArgs(v any) {
	ba, _ := v.(map[string]any)
	for _, bin := range sortedKeys(ba) {
		args := strListOf(ba[bin])
		if len(args) == 0 {
			continue
		}
		if _, widens := widensArgs(args); widens {
			m.refuseSafety("binary_args." + bin)
		}
	}
	m.unmap("binary_args", topReasons["binary_args"])
}

func (m *mapper) members() error {
	list, _ := m.in.Profile["members"].([]any)
	if len(list) == 0 {
		return &Refusal{Code: CodeInput, Fields: []string{"members"}, Detail: "the profile has no members"}
	}
	leadRole := strOf(m.in.Profile["lead"])
	leadMode := m.squad.LeadMode
	for i, raw := range list {
		mm, ok := raw.(map[string]any)
		if !ok {
			return &Refusal{Code: CodeInput, Fields: []string{fmt.Sprintf("members[%d]", i)}, Detail: "a member is not an object"}
		}
		role := strOf(mm["role"])
		handle := strOf(mm["handle"])
		if handle == "" {
			handle = role
		}
		binary := strings.ToLower(strOf(mm["binary"]))
		if binary != "claude" && binary != "codex" {
			return &Refusal{Code: CodeInput, Fields: []string{fmt.Sprintf("members[%d].binary", i)}, Detail: "a v2 member runs claude or codex"}
		}
		path := "members[" + handle + "]"
		seat := team.Seat{Handle: handle, Role: role, Runtime: binary, Model: strOf(mm["model"])}
		seat.Worktree = m.worktree(strOf(mm["cwd"]))
		seat.ActorMode = strOf(mm["actor_mode"])
		if seat.ActorMode == "" {
			// main's team.EffectiveActorMode: a planner lead reviews, everyone else implements
			seat.ActorMode = team.ActorModeImplementation
			if leadRole != "" && strings.EqualFold(role, leadRole) && leadMode == team.LeadModePlanner {
				seat.ActorMode = team.ActorModeReview
			}
		}
		for _, k := range sortedKeys(mm) {
			val := mm[k]
			if isEmpty(val) {
				continue
			}
			key := path + "." + k
			switch k {
			case "role", "binary", "handle", "model", "cwd", "actor_mode", "session":
			case "tool_profile":
				switch s := strOf(val); s {
				case team.ToolProfileMinimal, team.ToolProfileCoding, team.ToolProfileBrowser, team.ToolProfileData, team.ToolProfileFull:
					seat.Tools = s
				default:
					m.unmap(key, "a custom tool profile has no 3.0 name; the seat keeps the default (full)")
				}
			case "launcher", "launcher_args":
				m.refuseSafety(key)
			case "claude_args", "codex_args":
				if _, widens := widensArgs(strListOf(val)); widens {
					m.refuseSafety(key)
				} else {
					m.unmap(key, memberReasons[k])
				}
			default:
				if r, ok := memberReasons[k]; ok {
					m.unmap(key, r)
				} else if !safetyKey(k) {
					m.unmap(key, "not read by main's profile loader")
				}
			}
			checkSafety(key, k, val, m.refuseSafety)
		}
		m.squad.Seats = append(m.squad.Seats, seat)
		m.roleOf[handle] = role
	}
	return nil
}

// worktree is a member's cwd as a squad.yaml worktree: "" for the project,
// repo-relative inside it, absolute elsewhere.
func (m *mapper) worktree(cwd string) string {
	if cwd == "" {
		return ""
	}
	cwd = filepath.Clean(cwd)
	r, err := filepath.Rel(m.in.Project, cwd)
	switch {
	case err != nil || strings.HasPrefix(r, ".."):
		return cwd
	case r == ".":
		return ""
	}
	return filepath.ToSlash(r)
}

// session maps the squad's session: the deprecated workstream, else the one
// session every member shares. A member session that differs is unmapped.
func (m *mapper) session() {
	prof := m.in.Profile
	session := strOf(prof["workstream"])
	if session != "" && !sessionPattern.MatchString(session) {
		m.unmap("workstream", "not a valid 3.0 session name")
		session = ""
	}
	list, _ := prof["members"].([]any)
	if session == "" {
		shared, all := "", true
		for _, raw := range list {
			mm, _ := raw.(map[string]any)
			s := strOf(mm["session"])
			switch {
			case s == "":
				all = false
			case shared == "":
				shared = s
			case shared != s:
				all = false
			}
		}
		if all && shared != "" && sessionPattern.MatchString(shared) {
			session = shared
		}
	}
	m.squad.Session = session
	for i, raw := range list {
		mm, _ := raw.(map[string]any)
		if s := strOf(mm["session"]); s != "" && s != session {
			m.unmap("members["+m.squad.Seats[i].Handle+"].session", memberReasons["session"])
		}
	}
}

// lead maps v2's lead role to the handle of the seat that has the role.
func (m *mapper) lead() {
	leadRole := strOf(m.in.Profile["lead"])
	if leadRole == "" {
		if !isEmpty(m.in.Profile["orchestrated"]) {
			m.unmap("orchestrated", topReasons["orchestrated"])
		}
		return
	}
	for _, s := range m.squad.Seats {
		if strings.EqualFold(s.Role, leadRole) {
			m.squad.Lead = s.Handle
			return
		}
	}
	m.unmap("lead", topReasons["lead"])
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// outermost sorts the refused field paths, drops repeats and drops a path
// that lies under another refused path.
func outermost(paths []string) []string {
	sort.Strings(paths)
	var out []string
	for _, p := range dedupe(paths) {
		if len(out) > 0 && (strings.HasPrefix(p, out[len(out)-1]+".") || strings.HasPrefix(p, out[len(out)-1]+"[")) {
			continue
		}
		out = append(out, p)
	}
	return out
}

func dedupe(sorted []string) []string {
	var out []string
	for i, s := range sorted {
		if i == 0 || s != sorted[i-1] {
			out = append(out, s)
		}
	}
	return out
}

// rolePrompts stages the live role.md of each custom role as a 3.0 role
// prompt (.amq-squad/roles/<id>.md) when none is staged; a catalog role's
// role.md and a role.md that differs from its staged prompt are unmapped. The
// prompts are not trusted here: the owner runs amq-squad trust.
func (m *mapper) rolePrompts(p *Plan) {
	planned := map[string][]byte{}
	for _, r := range m.in.AgentRoleMDs {
		role := m.roleOf[r.Handle]
		name := rel(m.in.Project, r.Path)
		p.Dead = append(p.Dead, r.Path)
		switch staged, hasStaged := m.in.StagedRoles[role]; {
		case roleKnown(role):
			m.unmap(name, "role.md of a catalog role; 3.0 uses the catalog prompt, so the file is not carried")
		case hasStaged && bytes.Equal(staged.Data, r.Data):
			// the staged prompt is already the live one
		case hasStaged:
			m.unmap(name, "role.md differs from the staged .amq-squad/roles/"+role+".md, which 3.0 keeps")
		case planned[role] != nil && !bytes.Equal(planned[role], r.Data):
			m.unmap(name, "a second role.md for role "+role+" differs from the one staged")
		case planned[role] == nil:
			planned[role] = r.Data
			p.Writes = append(p.Writes, Write{Path: filepath.Join(m.in.Project, dirName, rolesDir, role+".md"), Mode: 0o644, Content: r.Data, Note: "role prompt " + role + " from " + name})
		}
	}
	if len(m.in.AgentRoleMDs) > 0 {
		p.TrustCmd = "amq-squad trust " + m.in.Project
	}
}

// files records the other v2 files: kept, dead or unmapped.
func (m *mapper) files(p *Plan) {
	in := m.in
	p.Dead = append(p.Dead, in.ProfilePath)
	if in.LaunchLocal != nil {
		p.Dead = append(p.Dead, in.LaunchLocal.Path)
		m.unmap(rel(in.Project, in.LaunchLocal.Path), "nothing in 3.0 reads it; its env block is not copied")
	}
	if in.TeamRules != nil {
		m.unmap(rel(in.Project, in.TeamRules.Path), "3.0 renders team-rules.md from squad.yaml (team propose); the v2 prose is not carried")
	}
	for _, b := range in.Briefs {
		if in.ProfileName == defaultProfile {
			p.Kept = append(p.Kept, b.Path)
		} else {
			m.unmap(rel(in.Project, b.Path), "3.0 reads only .amq-squad/briefs/<session>.md")
		}
	}
	for _, id := range sortedRoleIDs(in.StagedRoles) {
		p.Kept = append(p.Kept, in.StagedRoles[id].Path)
	}
	sort.Strings(p.Kept)
	sort.Strings(p.Dead)
	p.Dead = dedupe(p.Dead)
}

func sortedRoleIDs(m map[string]file) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// proposal plans grants.yaml.proposed: the grant-side fields the profile
// implies (the runtimes its seats use, the seat cap of its autonomous
// policy), for the owner to read and move. It is a separate file beside the
// owner's, never grants.yaml.
func (m *mapper) proposal(p *Plan, opts Options) {
	runtimes := map[string]bool{}
	for _, s := range m.squad.Seats {
		runtimes[s.Runtime] = true
	}
	names := make([]string, 0, len(runtimes))
	for r := range runtimes {
		names = append(names, r)
	}
	sort.Strings(names)
	var b strings.Builder
	fmt.Fprintf(&b, "# %s: written by `amq-squad migrate` from the v2 profile %s.\n", proposedName, rel(m.in.Project, m.in.ProfilePath))
	b.WriteString("# amq-squad never writes grants.yaml or registry.yaml. Read this, move the lines\n")
	b.WriteString("# you accept into ~/.amq-squad/grants.yaml yourself, then delete this file.\n")
	fmt.Fprintf(&b, "schema: %d\n", grants.Schema)
	fmt.Fprintf(&b, "permitted_runtimes: [%s]\n", strings.Join(names, ", "))
	if m.maxSeats > 0 {
		fmt.Fprintf(&b, "max_seats: %d\n", m.maxSeats)
	}
	p.Writes = append(p.Writes, Write{Path: filepath.Join(filepath.Dir(ownerPath(opts)), proposedName), Mode: 0o600, Content: []byte(b.String()), Note: "the grant-side fields the profile implies, for the owner"})
}

// proposedExists reports an existing grants.yaml.proposed.
func proposedExists(p *Plan) bool {
	for _, w := range p.Writes {
		if filepath.Base(w.Path) == proposedName {
			_, err := os.Lstat(w.Path)
			return err == nil
		}
	}
	return false
}
