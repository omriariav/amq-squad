package migrate

import (
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// The v2 files migrate reads, and the v2 field names it maps. Every name
// below comes from main's loaders (git show main:<path>, main at 8fd51b93);
// docs/v3-spike/5-migrate-execution.md lists each file and the commit that
// last touched it. A name is never guessed: a key the loaders do not know is
// reported as unmapped, and one whose name says it is an allow list or a
// launcher preference refuses the migration (safety.go).
//
//	internal/team/team.go       Team, Member, OperatorConfig, SelfOperatorPolicy,
//	                            AutonomousPolicy, GoalSupervisionPolicy,
//	                            LeadExecution, ProfilePath, ReadProfile, Validate
//	internal/cli/execution_mode.go  the four execution modes
//	internal/namespace/namespace.go BriefPath (.amq-squad/briefs/[<profile>/]<session>.md)
//	internal/rules/rules.go     Path (.amq-squad/team-rules.md)
//	internal/role/role.go       Path, LegacyPath, ExistingPath (role.md)
//	internal/cli/launch.go      seedRoleStub (roles/<id>.md, role.md)
//	internal/cli/cli.go         the agents directories under .agent-mail/<session>/agents
const (
	dirName         = ".amq-squad"
	teamFile        = "team.json"
	teamsDir        = "teams"
	rolesDir        = "roles"
	briefsDir       = "briefs"
	rulesFile       = "team-rules.md"
	launchLocalFile = "launch.local.json"
	roleFile        = "role.md"
	roleLayer       = "io.github.omriariav.amq-squad"
	agentMailDir    = ".agent-mail"
	defaultProfile  = "default"
	maxInputBytes   = 8 << 20
)

// inputs is everything migrate reads of one v2 checkout.
type inputs struct {
	Root         *root
	Project      string
	ProfilePath  string
	ProfileName  string
	Profile      map[string]any
	ProfileText  []byte
	LaunchLocal  *file // .amq-squad/launch.local.json, when present
	TeamRules    *file
	Briefs       []file // profile briefs, .amq-squad/briefs/[<profile>/]*.md
	StagedRoles  map[string]file
	AgentRoleMDs []roleMD
	Read         []string // every file opened, absolute, in the order read
}

type file struct {
	Path string
	Data []byte
}

type roleMD struct {
	Handle string
	file
}

// locate derives the project and the profile name from an absolute profile
// path: <project>/.amq-squad/team.json (the default profile) or
// <project>/.amq-squad/teams/<name>.json. The profile's project-relative path
// is returned too.
func locate(abs string) (project, name, rel string, err error) {
	dir, base := filepath.Dir(abs), filepath.Base(abs)
	switch {
	case base == teamFile && filepath.Base(dir) == dirName:
		return filepath.Dir(dir), defaultProfile, dirName + "/" + teamFile, nil
	case strings.HasSuffix(base, ".json") && filepath.Base(dir) == teamsDir && filepath.Base(filepath.Dir(dir)) == dirName:
		name = strings.TrimSuffix(base, ".json")
		return filepath.Dir(filepath.Dir(dir)), name, dirName + "/" + teamsDir + "/" + base, nil
	}
	return "", "", "", &Refusal{Code: CodeInput, Fields: []string{"the profile path"}, Detail: "the argument is not a v2 profile path (want <project>/.amq-squad/team.json or <project>/.amq-squad/teams/<name>.json)"}
}

// readInputs opens the project root once and reads the profile and every v2
// file that sits with it through that descriptor (M2). The caller closes
// in.Root. The absolute paths are checked for a credential shape before
// anything is read (M1).
func readInputs(profilePath string) (in *inputs, err error) {
	abs, err := filepath.Abs(profilePath)
	if err != nil {
		return nil, &Refusal{Code: CodeInput, Fields: []string{"the profile path"}, Detail: "the profile path cannot be made absolute"}
	}
	if looksLikeCredential(abs) {
		return nil, &Refusal{Code: CodeCredential, Fields: []string{"the profile path"}, Detail: "the profile path has the shape of a real credential; nothing is read or written"}
	}
	project, name, profileRel, err := locate(abs)
	if err != nil {
		return nil, err
	}
	r, err := openRoot(project)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			r.Close()
		}
	}()
	in = &inputs{Root: r, Project: r.path, ProfileName: name, ProfilePath: r.abs(profileRel), StagedRoles: map[string]file{}}
	prof, err := r.read(profileRel)
	if err != nil {
		return nil, err
	}
	if prof == nil {
		return nil, &Refusal{Code: CodeInput, Fields: []string{"the profile"}, Detail: "the v2 profile does not exist"}
	}
	in.Read = append(in.Read, in.ProfilePath)
	in.ProfileText = prof.Data
	if err := json.Unmarshal(prof.Data, &in.Profile); err != nil || in.Profile == nil {
		return nil, &Refusal{Code: CodeInput, Fields: []string{"the profile"}, Detail: "the profile is not a v2 profile (a JSON object)"}
	}
	optional := func(rel string) (*file, error) {
		f, err := r.read(rel)
		if f != nil {
			in.Read = append(in.Read, f.Path)
		}
		return f, err
	}
	if in.LaunchLocal, err = optional(dirName + "/" + launchLocalFile); err != nil {
		return nil, err
	}
	if in.TeamRules, err = optional(dirName + "/" + rulesFile); err != nil {
		return nil, err
	}
	briefs := dirName + "/" + briefsDir
	if name != defaultProfile {
		briefs += "/" + name
	}
	matches, err := r.glob(briefs, "*.md")
	if err != nil {
		return nil, err
	}
	for _, p := range matches {
		f, err := optional(p)
		if err != nil {
			return nil, err
		}
		if f != nil {
			in.Briefs = append(in.Briefs, *f)
		}
	}
	if matches, err = r.glob(dirName+"/"+rolesDir, "*.md"); err != nil {
		return nil, err
	}
	for _, p := range matches {
		f, err := optional(p)
		if err != nil {
			return nil, err
		}
		if f != nil {
			in.StagedRoles[strings.TrimSuffix(path.Base(p), ".md")] = *f
		}
	}
	for _, h := range memberHandles(in.Profile) {
		dirs, err := agentDirs(r, name, h)
		if err != nil {
			return nil, err
		}
		for _, dir := range dirs {
			for _, p := range []string{dir + "/extensions/" + roleLayer + "/" + roleFile, dir + "/" + roleFile} {
				f, err := optional(p)
				if err != nil {
					return nil, err
				}
				if f != nil {
					in.AgentRoleMDs = append(in.AgentRoleMDs, roleMD{Handle: h, file: *f})
				}
			}
		}
	}
	return in, nil
}

// agentDirs are the agent mailbox directories of handle below the root:
// .agent-mail/<session>/agents/<handle> for the default profile and
// .agent-mail/<profile>/<session>/agents/<handle> for a named one (main's
// namespace.AMQRoot at 8fd51b93; cli.go scans <base root>/<session>/agents).
// A symlink among them is a refusal; a plain file is skipped.
func agentDirs(r *root, profile, handle string) ([]string, error) {
	base := agentMailDir
	if profile != defaultProfile {
		base += "/" + profile
	}
	sessions, err := r.names(base)
	if err != nil {
		return nil, err
	}
	sort.Strings(sessions)
	var out []string
	for _, s := range sessions {
		dir := base + "/" + s + "/agents/" + handle
		isDir, err := r.isDir(dir)
		if err != nil {
			return nil, err
		}
		if isDir {
			out = append(out, dir)
		}
	}
	return out, nil
}

// memberHandles are the handles of the profile's members (a member without a
// handle answers to its role), valid handles only.
func memberHandles(prof map[string]any) []string {
	var out []string
	members, _ := prof["members"].([]any)
	for _, m := range members {
		mm, _ := m.(map[string]any)
		h := strOf(mm["handle"])
		if h == "" {
			h = strOf(mm["role"])
		}
		if h != "" && !strings.ContainsAny(h, `/\*?[`+"\x00") && h != "." && h != ".." {
			out = append(out, h)
		}
	}
	return out
}

func strOf(v any) string {
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

// isEmpty reports a JSON value that carries nothing: null, "", false, 0, an
// empty list or an empty object. An empty value is not a set field.
func isEmpty(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(x) == ""
	case bool:
		return !x
	case float64:
		return x == 0
	case []any:
		return len(x) == 0
	case map[string]any:
		return len(x) == 0
	}
	return false
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// walkStrings calls fn for every string leaf of v with its dotted path.
func walkStrings(path string, v any, fn func(path, s string)) {
	switch x := v.(type) {
	case string:
		fn(path, x)
	case []any:
		for i, e := range x {
			walkStrings(fmt.Sprintf("%s[%d]", path, i), e, fn)
		}
	case map[string]any:
		for _, k := range sortedKeys(x) {
			next := k
			if path != "" {
				next = path + "." + k
			}
			walkStrings(next, x[k], fn)
		}
	}
}

func strListOf(v any) []string {
	var out []string
	list, _ := v.([]any)
	for _, e := range list {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
