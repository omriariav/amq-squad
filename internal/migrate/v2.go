package migrate

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
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

// locate derives the project and the profile name from a profile path:
// <project>/.amq-squad/team.json (the default profile) or
// <project>/.amq-squad/teams/<name>.json.
func locate(profile string) (project, name string, err error) {
	abs, err := filepath.Abs(profile)
	if err != nil {
		return "", "", err
	}
	dir, base := filepath.Dir(abs), filepath.Base(abs)
	switch {
	case base == teamFile && filepath.Base(dir) == dirName:
		return filepath.Dir(dir), defaultProfile, nil
	case strings.HasSuffix(base, ".json") && filepath.Base(dir) == teamsDir && filepath.Base(filepath.Dir(dir)) == dirName:
		return filepath.Dir(filepath.Dir(dir)), strings.TrimSuffix(base, ".json"), nil
	}
	return "", "", &Refusal{Code: CodeInput, Detail: fmt.Sprintf("%s is not a v2 profile path (want <project>/.amq-squad/team.json or <project>/.amq-squad/teams/<name>.json)", profile)}
}

// readFile opens path read-only (O_RDONLY, never written) and returns its
// bytes; a missing file is (nil, nil). It refuses a file that is not regular
// or is larger than maxInputBytes.
func readFile(path string) (*file, error) {
	f, err := os.OpenFile(path, os.O_RDONLY, 0)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, &Refusal{Code: CodeInput, Detail: fmt.Sprintf("open %s: %v", path, err)}
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, &Refusal{Code: CodeInput, Detail: fmt.Sprintf("stat %s: %v", path, err)}
	}
	if !info.Mode().IsRegular() || info.Size() > maxInputBytes {
		return nil, &Refusal{Code: CodeInput, Detail: fmt.Sprintf("%s is not a regular file of at most %d bytes", path, maxInputBytes)}
	}
	data, err := io.ReadAll(io.LimitReader(f, maxInputBytes+1))
	if err != nil {
		return nil, &Refusal{Code: CodeInput, Detail: fmt.Sprintf("read %s: %v", path, err)}
	}
	return &file{Path: path, Data: data}, nil
}

// readInputs reads the profile and every v2 file that sits with it.
func readInputs(profilePath string) (*inputs, error) {
	project, name, err := locate(profilePath)
	if err != nil {
		return nil, err
	}
	abs, _ := filepath.Abs(profilePath)
	in := &inputs{Project: project, ProfilePath: abs, ProfileName: name, StagedRoles: map[string]file{}}
	prof, err := readFile(abs)
	if err != nil {
		return nil, err
	}
	if prof == nil {
		return nil, &Refusal{Code: CodeInput, Detail: fmt.Sprintf("%s does not exist", abs)}
	}
	in.Read = append(in.Read, abs)
	in.ProfileText = prof.Data
	if err := json.Unmarshal(prof.Data, &in.Profile); err != nil || in.Profile == nil {
		return nil, &Refusal{Code: CodeInput, Detail: fmt.Sprintf("%s is not a v2 profile (a JSON object): %v", abs, err)}
	}
	v2 := filepath.Join(project, dirName)
	optional := func(path string) (*file, error) {
		f, err := readFile(path)
		if f != nil {
			in.Read = append(in.Read, path)
		}
		return f, err
	}
	if in.LaunchLocal, err = optional(filepath.Join(v2, launchLocalFile)); err != nil {
		return nil, err
	}
	if in.TeamRules, err = optional(filepath.Join(v2, rulesFile)); err != nil {
		return nil, err
	}
	briefs := filepath.Join(v2, briefsDir)
	if name != defaultProfile {
		briefs = filepath.Join(briefs, name)
	}
	for _, p := range globSorted(filepath.Join(briefs, "*.md")) {
		f, err := optional(p)
		if err != nil {
			return nil, err
		}
		if f != nil {
			in.Briefs = append(in.Briefs, *f)
		}
	}
	for _, p := range globSorted(filepath.Join(v2, rolesDir, "*.md")) {
		f, err := optional(p)
		if err != nil {
			return nil, err
		}
		if f != nil {
			in.StagedRoles[strings.TrimSuffix(filepath.Base(p), ".md")] = *f
		}
	}
	for _, h := range memberHandles(in.Profile) {
		for _, dir := range agentDirs(project, h) {
			for _, p := range []string{filepath.Join(dir, "extensions", roleLayer, roleFile), filepath.Join(dir, roleFile)} {
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

func globSorted(pattern string) []string {
	m, _ := filepath.Glob(pattern)
	sort.Strings(m)
	return m
}

// agentDirs are the agent mailbox directories of handle under the project:
// <project>/.agent-mail/<session>/agents/<handle> (main's cli.go scans
// <base root>/<session>/agents).
func agentDirs(project, handle string) []string {
	return globSorted(filepath.Join(project, agentMailDir, "*", "agents", handle))
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
